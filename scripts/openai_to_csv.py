# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""Refresh the `openai` and `openai-codex` rows of models.csv from live sources.

Credentials are supplied at run time and never stored in the repo.

Two modes, because the two providers expose completely different surfaces:

  openai        GET https://api.openai.com/v1/models with an API key. That
                endpoint returns only {id, object, created, owned_by}, so every
                descriptive and pricing column is enriched from OpenRouter's
                public catalog (no key required).

  openai-codex  The ChatGPT-account Codex backend has no list endpoint, so each
                candidate slug is probed against /codex/responses with a ChatGPT
                OAuth access token. Slugs it rejects with
                  "The '<model>' model is not supported when using Codex with a
                   ChatGPT account."
                are dropped. This is the only way to learn the real list; see
                https://github.com/gluk-w/claworc/issues/209.

The credential argument is optional. Omitted, `openai` reads $OPENAI_API_KEY and
`openai-codex` reads tokens.access_token from the Codex CLI's auth.json
($CODEX_HOME, default ~/.codex) — so the token never lands in shell history or
the process table.

Usage:
    make codex-models                      # logs in if needed, then probes
    make openai-models
    uv run scripts/openai_to_csv.py openai-codex --dry-run
    uv run scripts/openai_to_csv.py openai-codex --candidates gpt-5.6-sol,gpt-5.3-codex
    uv run scripts/openai_to_csv.py openai "$OPENAI_API_KEY"
"""
import argparse
import base64
import csv
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

# metadata() lives next door — OpenRouter is the enrichment source for both
# scripts, so the column conversion rules stay in one place.
from openrouter_to_csv import OPENROUTER_MODELS_URL, metadata

OPENAI_MODELS_URL = "https://api.openai.com/v1/models"

# Endpoint and headers mirror internalproxy's openAICodexResponses so the probe
# is indistinguishable from what a real agent sends.
# See control-plane/internal/internalproxy/apitype_impl.go.
CODEX_RESPONSES_URL = "https://chatgpt.com/backend-api/codex/responses"

PROVIDERS = {
    "openai": {
        "provider_key": "openai",
        "provider_label": "OpenAI",
        "icon_key": "openai",
        "api_format": "openai-responses",
        "base_url": "https://api.openai.com/",
    },
    "openai-codex": {
        "provider_key": "openai-codex",
        "provider_label": "OpenAI Codex (ChatGPT subscription)",
        "icon_key": "openai",
        "api_format": "openai-codex-responses",
        "base_url": "https://chatgpt.com/backend-api",
    },
}

# Codex comes with the ChatGPT subscription, so there is no per-token price to
# report and the tag/description are fixed for every row.
CODEX_FIXED = {
    "input_cost": "0",
    "output_cost": "0",
    "cached_read_cost": "0",
    "cached_write_cost": "0",
    "tag": "coding",
    "description": "Included with ChatGPT subscription",
}

# /v1/models lists every model on the account, including embeddings, speech,
# image and moderation endpoints that OpenClaw can't drive as a chat model.
KEEP_RE = re.compile(r"^(gpt|o\d)")
DROP_RE = re.compile(
    r"audio|realtime|image|tts|transcribe|whisper|embedding|moderation|search|dall-e|sora|instruct"
)
DATED_SNAPSHOT_RE = re.compile(r"-\d{4}-\d{2}-\d{2}$")


class AuthError(Exception):
    """The supplied credential was rejected outright."""


# ---------------------------------------------------------------------------
# models.csv
# ---------------------------------------------------------------------------


def read_csv(path):
    with path.open(newline="") as f:
        reader = csv.reader(f)
        header = next(reader)
        rows = [r for r in reader if r]
    return header, rows


def row_dict(header, row):
    """Rows in models.csv are ragged — trailing empty columns are often absent."""
    return {c: (row[i] if i < len(row) else "") for i, c in enumerate(header)}


def write_csv(path, header, rows):
    # lineterminator: csv defaults to CRLF, which would rewrite every line in
    # an LF file and bury the real change in a whole-file diff.
    with path.open("w", newline="") as f:
        w = csv.writer(f, lineterminator="\n")
        w.writerow(header)
        w.writerows(rows)


def splice(rows, provider_key, new_rows):
    """Replace provider_key's rows with new_rows, at the position they held."""
    at = next((i for i, r in enumerate(rows) if r and r[0] == provider_key), None)
    kept = [r for r in rows if not (r and r[0] == provider_key)]
    if at is None:
        return kept + new_rows
    # Everything removed sat at or after `at`, so `at` still indexes into kept
    # as the spot the block used to start at.
    at = min(at, len(kept))
    return kept[:at] + new_rows + kept[at:]


# ---------------------------------------------------------------------------
# Fetching
# ---------------------------------------------------------------------------


def get_json(url, headers, timeout=30):
    req = urllib.request.Request(url, headers={"Accept": "application/json", **headers})
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.loads(resp.read())


def fetch_openai_models(api_key):
    try:
        return get_json(OPENAI_MODELS_URL, {"Authorization": f"Bearer {api_key}"})["data"]
    except urllib.error.HTTPError as e:
        if e.code in (401, 403):
            raise AuthError(f"OpenAI rejected the API key (HTTP {e.code})") from e
        raise


def fetch_openrouter_index():
    """OpenAI models keyed by bare id, e.g. 'gpt-5.6-sol' -> catalog entry."""
    data = get_json(OPENROUTER_MODELS_URL, {})["data"]
    return {
        m["id"].split("/", 1)[1]: m
        for m in data
        if m.get("id", "").startswith("openai/")
    }


def keep_model(model_id, extra_filter=None, keep_all=False):
    if extra_filter:
        return bool(extra_filter.search(model_id))
    if keep_all:
        return True
    if not KEEP_RE.match(model_id):
        return False
    if DROP_RE.search(model_id):
        return False
    return not DATED_SNAPSHOT_RE.search(model_id)


# ---------------------------------------------------------------------------
# Codex probe
# ---------------------------------------------------------------------------


def jwt_claims(token):
    """Decode a JWT payload without verifying it. {} if it isn't one."""
    parts = token.split(".")
    if len(parts) < 2:
        return {}
    seg = parts[1]
    try:
        claims = json.loads(base64.urlsafe_b64decode(seg + "=" * (-len(seg) % 4)))
    except Exception:
        return {}
    return claims if isinstance(claims, dict) else {}


def account_id_from_token(token):
    """Read chatgpt_account_id out of the access JWT.

    Mirrors decodeAccountIDFromAccess in
    control-plane/internal/internalproxy/oauth_codex.go, so the operator only
    ever has to supply one credential.
    """
    auth = jwt_claims(token).get("https://api.openai.com/auth")
    return auth.get("chatgpt_account_id", "") if isinstance(auth, dict) else ""


def codex_home():
    return Path(os.environ.get("CODEX_HOME") or Path.home() / ".codex")


def load_codex_token():
    """Read tokens.access_token from the Codex CLI's auth.json.

    Keeping the token off the command line means it never reaches the shell
    history or the process table. `codex login` writes this file; the CLI
    refreshes the token in place as it is used.
    """
    path = codex_home() / "auth.json"
    if not path.exists():
        raise AuthError(
            f"{path} not found — run `codex login` first (or `make codex-login`).\n"
            "If the CLI is storing credentials in the OS keychain rather than a "
            "file, pass the token as an argument instead."
        )
    try:
        doc = json.loads(path.read_text())
    except Exception as e:
        raise AuthError(f"could not parse {path}: {e}") from e

    token = ((doc.get("tokens") or {}).get("access_token") or "").strip()
    if not token:
        raise AuthError(f"no tokens.access_token in {path} — run `codex login` again")

    # Fail now with something actionable rather than after N rejected probes.
    exp = jwt_claims(token).get("exp")
    if isinstance(exp, (int, float)) and exp < time.time():
        raise AuthError(
            f"the access token in {path} expired at "
            f"{time.strftime('%Y-%m-%d %H:%M:%S', time.localtime(exp))}.\n"
            "Run any `codex` command to refresh it, or `codex login` to sign in again."
        )
    return token


def codex_probe_body(model):
    """Smallest request /codex/responses accepts.

    Field set matches rewriteCodexRequestBody in
    control-plane/internal/internalproxy/request_rewriter.go.
    """
    return {
        "model": model,
        "instructions": "You are a helpful assistant.",
        "input": [{"role": "user", "content": [{"type": "input_text", "text": "hi"}]}],
        "store": False,
        "stream": True,
        "tool_choice": "auto",
        "parallel_tool_calls": True,
        "text": {"verbosity": "medium"},
        "include": ["reasoning.encrypted_content"],
    }


def error_detail(payload):
    try:
        doc = json.loads(payload)
    except Exception:
        return payload[:200].strip()
    if isinstance(doc, dict):
        if isinstance(doc.get("detail"), str):
            return doc["detail"]
        err = doc.get("error")
        if isinstance(err, dict) and isinstance(err.get("message"), str):
            return err["message"]
    return payload[:200].strip()


def probe_codex_model(model, token, account_id, timeout=60):
    """Return (verdict, detail) where verdict is accepted/rejected/unknown.

    The connection is dropped as soon as the first streamed event lands, so an
    accepted model generates next to nothing.
    """
    req = urllib.request.Request(
        CODEX_RESPONSES_URL,
        data=json.dumps(codex_probe_body(model)).encode(),
        method="POST",
        headers={
            "Authorization": f"Bearer {token}",
            "chatgpt-account-id": account_id,
            "OpenAI-Beta": "responses=experimental",
            "originator": "pi",
            "Content-Type": "application/json",
            "Accept": "text/event-stream",
        },
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            resp.readline()  # first SSE line, then close
            return "accepted", ""
    except urllib.error.HTTPError as e:
        detail = error_detail(e.read().decode("utf-8", "replace"))
        if e.code in (401, 403):
            raise AuthError(
                f"ChatGPT rejected the access token (HTTP {e.code}): {detail}\n"
                "Access tokens are short-lived — re-run the Codex OAuth login and retry."
            ) from e
        if e.code == 400 and "is not supported" in detail:
            return "rejected", detail
        return "unknown", f"HTTP {e.code}: {detail}"
    except Exception as e:  # timeout, DNS, TLS, connection reset
        return "unknown", f"{type(e).__name__}: {e}"


# ---------------------------------------------------------------------------
# Row building
# ---------------------------------------------------------------------------


def strip_vendor(name):
    """'OpenAI: GPT-5.6 Sol' -> 'GPT-5.6 Sol' (OpenRouter prefixes its labels)."""
    return name.split(": ", 1)[1] if name.startswith("OpenAI: ") else name


def build_row(model_id, provider, header, existing, enrichment, refresh, fixed=None):
    """Merge catalog constants, existing CSV values and OpenRouter enrichment.

    Existing values win over enrichment unless --refresh-metadata, so curated
    tags and descriptions survive a re-run.
    """
    row = dict(provider)
    row["model_id"] = model_id

    enriched = {}
    if enrichment is not None:
        enriched = dict(metadata(enrichment))
        enriched["model_name"] = strip_vendor(enriched["model_name"])

    for col in header:
        if col in row:
            continue
        was = (existing or {}).get(col, "")
        now = str(enriched.get(col, "") or "")
        row[col] = (now or was) if refresh else (was or now)

    if fixed:
        row.update(fixed)
    return [row.get(c, "") for c in header]


# ---------------------------------------------------------------------------
# Modes
# ---------------------------------------------------------------------------


def run_openai(args, existing, index):
    models = fetch_openai_models(args.credential)
    extra = re.compile(args.filter) if args.filter else None
    ids = [m["id"] for m in models if keep_model(m["id"], extra, args.all)]

    # Newest first, so the flagship heads the provider's list in the UI.
    created = {m["id"]: m.get("created") or 0 for m in models}
    ids.sort(key=lambda i: (-created.get(i, 0), i))

    print(
        f"{len(models)} models on the account, {len(ids)} kept after filtering",
        file=sys.stderr,
    )
    return ids


def run_codex(args, existing, index, sibling=()):
    account_id = args.account_id or account_id_from_token(args.credential)
    if not account_id:
        print(
            "warning: no chatgpt_account_id in the token; pass --account-id if probes fail",
            file=sys.stderr,
        )

    if args.candidates:
        candidates = [c.strip() for c in args.candidates.split(",") if c.strip()]
    elif args.from_api:
        extra = re.compile(args.filter) if args.filter else None
        candidates = [
            m["id"]
            for m in fetch_openai_models(args.from_api)
            if keep_model(m["id"], extra, args.all)
        ]
    else:
        # Default to every slug the catalog already knows about on either
        # OpenAI provider: the useful question is which of the models OpenAI
        # offers this ChatGPT account may actually drive.
        candidates = sorted(set(existing) | set(sibling))

    if not candidates:
        sys.exit("no candidate slugs to probe — pass --candidates or --from-api")

    print(
        f"probing {len(candidates)} candidate(s) against {CODEX_RESPONSES_URL}",
        file=sys.stderr,
    )
    accepted, unknown = [], []
    for model_id in candidates:
        verdict, detail = probe_codex_model(model_id, args.credential, account_id)
        print(f"  {verdict:8} {model_id}{'  ' + detail if detail else ''}", file=sys.stderr)
        if verdict == "accepted":
            accepted.append(model_id)
        elif verdict == "unknown":
            unknown.append(model_id)

    if unknown:
        print(
            f"note: {len(unknown)} slug(s) gave no clear verdict and were left out: "
            + ", ".join(unknown),
            file=sys.stderr,
        )
    if not accepted:
        sys.exit("no candidate was accepted — refusing to write an empty provider")
    return accepted


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def resolve_credential(args):
    """Explicit argument, else the mode's ambient source."""
    if args.credential:
        return args.credential
    if args.provider == "openai-codex":
        return load_codex_token()
    key = os.environ.get("OPENAI_API_KEY", "").strip()
    if not key:
        raise AuthError(
            "no API key given and OPENAI_API_KEY is unset — pass the key as an "
            "argument or export it"
        )
    return key


def main():
    ap = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    ap.add_argument("provider", choices=sorted(PROVIDERS))
    ap.add_argument(
        "credential",
        nargs="?",
        help="OpenAI API key, or a ChatGPT OAuth access token for openai-codex. "
        "Omit it to read $OPENAI_API_KEY, or for openai-codex the Codex CLI's "
        "$CODEX_HOME/auth.json (default ~/.codex/auth.json)",
    )
    ap.add_argument("--csv", default="models.csv", type=Path)
    ap.add_argument("--dry-run", action="store_true", help="print the rows, don't write")
    ap.add_argument("--all", action="store_true", help="skip the chat-model filter")
    ap.add_argument(
        "--keep-unknown",
        action="store_true",
        help="write models with no metadata instead of skipping them",
    )
    ap.add_argument("--filter", help="regex a model id must match to be kept")
    ap.add_argument(
        "--refresh-metadata",
        action="store_true",
        help="let OpenRouter override values already in models.csv",
    )
    ap.add_argument("--candidates", help="openai-codex: comma-separated slugs to probe")
    ap.add_argument(
        "--from-api", help="openai-codex: seed candidates from /v1/models using this API key"
    )
    ap.add_argument("--account-id", help="openai-codex: override chatgpt-account-id")
    args = ap.parse_args()

    try:
        args.credential = resolve_credential(args)
    except AuthError as e:
        sys.exit(str(e))

    provider = PROVIDERS[args.provider]
    header, rows = read_csv(args.csv)
    model_id_col = header.index("model_id")
    existing = {
        r[model_id_col]: row_dict(header, r)
        for r in rows
        if r and r[0] == args.provider and len(r) > model_id_col
    }
    # The other OpenAI provider's rows seed the codex probe's candidate list.
    other = "openai" if args.provider == "openai-codex" else "openai-codex"
    sibling = [
        r[model_id_col] for r in rows if r and r[0] == other and len(r) > model_id_col
    ]
    index = fetch_openrouter_index()

    try:
        if args.provider == "openai":
            ids = run_openai(args, existing, index)
        else:
            ids = run_codex(args, existing, index, sibling)
    except AuthError as e:
        sys.exit(str(e))

    fixed = CODEX_FIXED if args.provider == "openai-codex" else None
    # Codex rows carry no curated data — CODEX_FIXED supplies tag/description
    # and the costs are all zero — so context/vision/etc. always come from
    # enrichment. The row this replaced claimed 400000 and vision FALSE for a
    # model that is 1050000 and vision-capable.
    refresh = args.refresh_metadata or args.provider == "openai-codex"
    new_rows = [
        build_row(i, provider, header, existing.get(i), index.get(i), refresh, fixed)
        for i in ids
    ]

    # A model with no OpenRouter match and no prior CSV row has nothing but an
    # id: no display name, no context window. Writing it produces a blank entry
    # in the model picker, so drop it unless asked otherwise.
    name_col = header.index("model_name")
    unknown = [r[header.index("model_id")] for r in new_rows if not r[name_col]]
    if unknown:
        verb = "kept without metadata" if args.keep_unknown else "skipped"
        print(
            f"{len(unknown)} model(s) have no metadata and were {verb}: "
            + ", ".join(unknown),
            file=sys.stderr,
        )
        if not args.keep_unknown:
            print("  (pass --keep-unknown to write them and fill in by hand)", file=sys.stderr)
            new_rows = [r for r in new_rows if r[name_col]]

    if args.dry_run:
        w = csv.writer(sys.stdout)
        w.writerow(header)
        w.writerows(new_rows)
        print(
            f"dry run: {len(new_rows)} {args.provider} row(s), models.csv untouched",
            file=sys.stderr,
        )
        return

    write_csv(args.csv, header, splice(rows, args.provider, new_rows))
    print(f"wrote {len(new_rows)} {args.provider} row(s) to {args.csv}", file=sys.stderr)


if __name__ == "__main__":
    main()
