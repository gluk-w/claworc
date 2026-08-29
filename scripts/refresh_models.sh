#!/usr/bin/env bash
#
# Refresh the whole model catalog in models.csv, one source at a time.
# Run via `make models` from the repo root.
#
# Each source is optional: supply the credential to run it, press Enter to skip.
# Credentials are read with `read -s` and exported only into the child process,
# so they never reach your shell history or the process table. Existing
# OPENAI_API_KEY / OPENROUTER_API_KEY in the environment are used without
# prompting, which is also how this runs unattended.
set -uo pipefail

cd "$(dirname "$0")/.."

CSV=models.csv
ran=()
skipped=()

bold() { printf '\033[1m%s\033[0m\n' "$1"; }
note() { printf '  %s\n' "$1"; }

# Prompt for a secret unless the named variable already holds one.
# Sets the variable; empty means "skip this source".
ask_secret() {
	local var=$1 label=$2
	if [ -n "${!var:-}" ]; then
		note "using \$$var from the environment"
		return
	fi
	if [ ! -t 0 ]; then
		printf -v "$var" '%s' ''
		note "not a terminal and \$$var is unset — skipping"
		return
	fi
	local value
	read -rsp "  $label (Enter to skip): " value </dev/tty
	echo
	printf -v "$var" '%s' "$value"
}

confirm() {
	[ -t 0 ] || return 1
	local reply
	read -rp "  $1 [y/N] " reply </dev/tty
	[[ $reply == [yY]* ]]
}

before=$(wc -l <"$CSV")

# --- OpenAI (API key) -------------------------------------------------------
bold "OpenAI — GET /v1/models"
ask_secret OPENAI_API_KEY "OpenAI API key"
if [ -n "${OPENAI_API_KEY:-}" ]; then
	# Deny-list, not allow-list: the account still lists gpt-4 and gpt-3.5 era
	# models nobody should pick for a new agent, but anything OpenAI ships
	# next has to show up on its own. An allow-list would go blind the day
	# gpt-6 lands, which is how #209 happened in the first place.
	exclude=${OPENAI_MODEL_EXCLUDE:-'^(gpt-3|gpt-4|o1|o3-mini)'}
	note "excluding: $exclude  (override with OPENAI_MODEL_EXCLUDE)"
	if OPENAI_API_KEY="$OPENAI_API_KEY" uv run scripts/openai_to_csv.py openai --exclude "$exclude"; then
		ran+=("openai")
	else
		skipped+=("openai (failed)")
	fi
else
	skipped+=("openai")
fi
echo

# --- OpenAI Codex (ChatGPT OAuth) -------------------------------------------
bold "OpenAI Codex — probing which slugs a ChatGPT account accepts"
if ! command -v codex >/dev/null 2>&1; then
	note "'codex' CLI not found (npm i -g @openai/codex) — skipping"
	skipped+=("openai-codex")
elif ! codex login status >/dev/null 2>&1 && ! codex login; then
	note "codex login did not complete — skipping"
	skipped+=("openai-codex")
else
	count=$(awk -F, '$1 == "openai" || $1 == "openai-codex" {print $6}' "$CSV" | sort -u | wc -l | tr -d ' ')
	note "$count candidate slug(s) to probe, one small request each"
	if confirm "Probe now?"; then
		if uv run scripts/openai_to_csv.py openai-codex; then
			ran+=("openai-codex")
		else
			skipped+=("openai-codex (failed)")
		fi
	else
		skipped+=("openai-codex")
	fi
fi
echo

# --- OpenRouter -------------------------------------------------------------
bold "OpenRouter — GET /api/v1/models"
ask_secret OPENROUTER_API_KEY "OpenRouter API key"
if [ -n "${OPENROUTER_API_KEY:-}" ]; then
	if python3 scripts/openrouter_to_csv.py "$OPENROUTER_API_KEY"; then
		ran+=("openrouter")
	else
		skipped+=("openrouter (failed)")
	fi
else
	skipped+=("openrouter")
fi
echo

# --- OpenClaw source --------------------------------------------------------
bold "OpenClaw source — parsing the bundled provider catalogs"
src=${OPENCLAW_SRC:-$HOME/openclaw-github/src}
if [ -d "$src" ]; then
	if python3 scripts/extract_models.py; then
		ran+=("openclaw source")
	else
		skipped+=("openclaw source (failed)")
	fi
else
	note "$src not found — skipping (clone openclaw-github or set OPENCLAW_SRC)"
	skipped+=("openclaw source")
fi
echo

# --- Rebuild and report -----------------------------------------------------
bold "Rebuilding the worker bundle"
(cd website/worker && node build-models.mjs)
echo

after=$(wc -l <"$CSV")
bold "Done"
note "refreshed: ${ran[*]:-none}"
note "skipped:   ${skipped[*]:-none}"
note "$CSV: $((before - 1)) -> $((after - 1)) models"
note "review with: git diff --stat $CSV"
echo
note "models.csv only reaches claworc.com/providers/ once the worker is deployed."
