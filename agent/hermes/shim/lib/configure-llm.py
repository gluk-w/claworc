"""configure-llm implementation — route Hermes' LLM traffic through the
Claworc LLM proxy (docs/shim.md). Invoked by the bash `configure-llm`
entrypoint with the generic routing document on stdin; rewrites the fully
managed block in ~/.hermes/config.yaml — replace, never append twice — so the
verb is idempotent.

Hermes fact-check (hermes_cli/runtime_provider.py, hermes_cli/config.py,
verified against NousResearch/hermes-agent v2026.8.x):
  - ~/.hermes/config.yaml `model:` section is the single source of truth for
    a custom OpenAI-compatible endpoint:
        model.provider: "custom"   -> plain OpenAI-compatible endpoint
        model.base_url             -> trusted for bare "custom" (loopback or
                                      when provider is already "custom")
        model.api_key              -> read via `for k in ("api_key", "api")`
        model.default              -> default model id, passed through as-is
  - The "custom" provider is an OpenAI client: it POSTs <base_url>/chat/
    completions. The Claworc proxy appends that path to the upstream
    provider's base URL, so base_url must end in /v1, and the model id must be
    the upstream's own id (routing documents use "<provider>/<model>").
  - OPENAI_API_KEY / OPENAI_BASE_URL env vars are host-gated or ignored for
    custom endpoints, so the config file is the correct (and only reliable)
    place to put the proxy routing.

The managed block is a complete top-level `model:` mapping between the
`# BEGIN claworc-managed` / `# END claworc-managed` markers. The image-baked
skeleton config.yaml contains exactly one such block, so replacing it keeps
the YAML free of duplicate keys. Values are emitted with json.dumps (JSON
strings are valid YAML double-quoted scalars).

Exit 6 when the routing cannot be expressed (e.g. style "anthropic": Hermes
would need api_mode plumbing this shim does not manage; meta declares
llm.styles ["openai"] accordingly).
"""
import json
import os
import pwd
import sys
import tempfile

CONFIG = "/home/claworc/.hermes/config.yaml"
BEGIN = "# BEGIN claworc-managed"
END = "# END claworc-managed"


def fail_validation(msg):
    print(json.dumps({"error": msg}))
    sys.exit(6)


def main():
    try:
        doc = json.load(sys.stdin)
    except Exception as e:
        fail_validation(f"invalid JSON routing document: {e}")
    if not isinstance(doc, dict):
        fail_validation("routing document must be a JSON object")

    style = doc.get("style") or "openai"
    if style != "openai":
        fail_validation(f"unsupported llm style {style!r}: this image routes Hermes "
                        "through an OpenAI-compatible endpoint only")

    proxy_url = doc.get("proxy_url") or ""
    providers = doc.get("providers") or []
    if not isinstance(providers, list):
        fail_validation("providers must be an array")
    if providers and not proxy_url:
        fail_validation("proxy_url is required when providers are present")

    if any(not isinstance(p, dict) for p in providers):
        fail_validation("providers entries must be objects")

    # The default model is "<provider-key>/<model>": route with that
    # provider's virtual key and hand Hermes the bare upstream model id.
    # Falls back to the first provider when no key matches the prefix.
    default_model = doc.get("default_model") or ""
    provider = None
    if providers:
        prefix = default_model.split("/", 1)[0] if "/" in default_model else ""
        provider = next((p for p in providers if prefix and p.get("key") == prefix), providers[0])
    api_key = (provider or {}).get("api_key") or ""
    model = default_model
    key = (provider or {}).get("key") or ""
    if key and model.startswith(key + "/"):
        model = model[len(key) + 1:]
    base_url = proxy_url.rstrip("/") + "/v1" if proxy_url else ""

    block = [
        BEGIN,
        "# Managed by the Claworc shim configure-llm verb - do not edit inside this block.",
        "# Routes all Hermes LLM traffic to the Claworc LLM proxy with a virtual key.",
        "model:",
        "  provider: \"custom\"",
        f"  base_url: {json.dumps(base_url)}",
        f"  api_key: {json.dumps(api_key)}",
        f"  default: {json.dumps(model)}",
        END,
    ]

    os.makedirs(os.path.dirname(CONFIG), exist_ok=True)

    try:
        with open(CONFIG, encoding="utf-8") as f:
            lines = f.read().splitlines()
    except FileNotFoundError:
        lines = []

    # Replace the existing managed block in place; append the block when absent.
    out, i, replaced = [], 0, False
    while i < len(lines):
        if lines[i].strip() == BEGIN:
            j = i + 1
            while j < len(lines) and lines[j].strip() != END:
                j += 1
            out.extend(block)
            replaced = True
            i = j + 1  # skip END (or run off the end for an unterminated block)
        else:
            out.append(lines[i])
            i += 1
    if not replaced:
        if out and out[-1].strip():
            out.append("")
        out.extend(block)

    content = "\n".join(out) + "\n"
    d = os.path.dirname(os.path.abspath(CONFIG))
    fd, tmp = tempfile.mkstemp(dir=d, prefix=".config.yaml.")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            f.write(content)
        os.chmod(tmp, 0o644)
        if os.geteuid() == 0:
            try:
                pw = pwd.getpwnam("claworc")
                os.chown(tmp, pw.pw_uid, pw.pw_gid)
            except (KeyError, OSError):
                pass
        os.replace(tmp, CONFIG)
    except BaseException:
        try:
            os.unlink(tmp)
        except OSError:
            pass
        raise
    sys.exit(0)


if __name__ == "__main__":
    main()
