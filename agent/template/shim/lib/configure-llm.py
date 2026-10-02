"""configure-llm implementation for the template image (docs/shim.md).

Invoked by the bash `configure-llm` entrypoint with the routing document on
stdin and ENV_FILE in the environment. Rewrites the fully managed block in
agent.env (never appends twice), so the verb is idempotent.

The block exports generic CLAWORC_LLM_* variables plus the conventional
provider env vars for the declared style (OPENAI_BASE_URL/OPENAI_API_KEY or
ANTHROPIC_BASE_URL/ANTHROPIC_API_KEY), pointing at the proxy with the first
provider's virtual key. Adapt the block contents to whatever your CHAT_CMD
agent actually reads. Exit 6 when the routing cannot be expressed.
"""
import json
import os
import shlex
import sys
import tempfile

BEGIN = "# >>> claworc-llm >>>"
END = "# <<< claworc-llm <<<"

env_file = os.environ["ENV_FILE"]


def validation_fail(msg):
    print(json.dumps({"error": msg}))
    sys.exit(6)


try:
    doc = json.load(sys.stdin)
except Exception as e:  # noqa: BLE001
    validation_fail(f"invalid JSON routing document: {e}")
if not isinstance(doc, dict):
    validation_fail("routing document must be a JSON object")

style = doc.get("style") or "openai"
if style not in ("openai", "anthropic"):
    validation_fail(f"unsupported llm style {style!r}")

proxy_url = doc.get("proxy_url") or ""
providers = doc.get("providers") or []
if not isinstance(providers, list):
    validation_fail("providers must be an array")
if providers and not proxy_url:
    validation_fail("proxy_url is required when providers are present")

default_model = doc.get("default_model") or ""
fallbacks = [m for m in (doc.get("fallback_models") or []) if isinstance(m, str) and m]
api_key = ""
if providers:
    first = providers[0]
    if not isinstance(first, dict):
        validation_fail("providers entries must be objects")
    api_key = first.get("api_key") or ""

block = [
    BEGIN,
    "# Managed by the Claworc shim configure-llm verb - do not edit inside this block.",
    f"CLAWORC_LLM_PROXY_URL={shlex.quote(proxy_url)}",
    f"CLAWORC_LLM_STYLE={shlex.quote(style)}",
    f"CLAWORC_LLM_API_KEY={shlex.quote(api_key)}",
    f"CLAWORC_LLM_DEFAULT_MODEL={shlex.quote(default_model)}",
    f"CLAWORC_LLM_FALLBACK_MODELS={shlex.quote(','.join(fallbacks))}",
]
if style == "openai":
    block += [
        f"OPENAI_BASE_URL={shlex.quote(proxy_url)}",
        f"OPENAI_API_KEY={shlex.quote(api_key)}",
    ]
else:
    block += [
        f"ANTHROPIC_BASE_URL={shlex.quote(proxy_url)}",
        f"ANTHROPIC_API_KEY={shlex.quote(api_key)}",
    ]
block.append(END)

try:
    with open(env_file, encoding="utf-8") as f:
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
d = os.path.dirname(os.path.abspath(env_file))
fd, tmp = tempfile.mkstemp(dir=d, prefix=".agent.env.")
try:
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        f.write(content)
    os.chmod(tmp, 0o644)
    os.replace(tmp, env_file)
except BaseException:
    try:
        os.unlink(tmp)
    except OSError:
        pass
    raise
