# shimlib.sh — shared plumbing for Claworc agent shim verbs (docs/shim.md).
#
# Sourced (never executed) by every verb entrypoint:
#
#   #!/usr/bin/env bash
#   set -euo pipefail
#   source "$(dirname "$0")/lib/shimlib.sh"
#
# The copy in agent/template/shim/lib/ is canonical; every agent image ships a
# byte-identical copy (enforced by agent/tests/shim-contract.ts). Keep it
# agent-agnostic: image specifics (paths, runtimes) belong in the verbs.
#
# Contract exit codes (docs/shim.md): 0 ok, 1 broken/internal, 2 usage,
# 3 unsupported, 4 not ready, 5 timeout, 6 validation.
#
# shellcheck shell=bash disable=SC2034  # SHIM_* outputs are read by the verbs

SHIM_DIR=${SHIM_DIR:-$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)}
SHIM_USER=${SHIM_USER:-claworc}
SHIM_HOME=${SHIM_HOME:-/home/claworc}

# --- errors --------------------------------------------------------------------

# die_usage <msg> — exit 2 (bad arguments).
die_usage() {
    printf '%s\n' "$*" >&2
    exit 2
}

# die_unsupported <msg> — exit 3 (verb/capability not supported).
die_unsupported() {
    printf '%s\n' "$*" >&2
    exit 3
}

# json_string <s> — print <s> as a JSON string literal (quotes included).
json_string() {
    local s=$1 out='' c i
    for ((i = 0; i < ${#s}; i++)); do
        c=${s:i:1}
        case "$c" in
            '"') out+='\"' ;;
            '\') out+='\\' ;;
            $'\n') out+='\n' ;;
            $'\r') out+='\r' ;;
            $'\t') out+='\t' ;;
            *)
                if [[ "$c" == [[:cntrl:]] ]]; then
                    out+=$(printf '\\u%04x' "'$c")
                else
                    out+=$c
                fi
                ;;
        esac
    done
    printf '"%s"' "$out"
}

# die_validation <msg> — print {"error": <msg>} on stdout and exit 6.
die_validation() {
    printf '{"error":%s}\n' "$(json_string "$*")"
    exit 6
}

# --- arguments -----------------------------------------------------------------

# parse_id_arg <default-id> <allowed-id>... -- "$@"
# Parses `[--id <file-id>]` and sets SHIM_ID. Exit 2 on unknown arguments or
# an id not in the allowed list.
parse_id_arg() {
    local default=$1
    shift
    local allowed=()
    while [[ $# -gt 0 && "$1" != "--" ]]; do
        allowed+=("$1")
        shift
    done
    [[ $# -gt 0 ]] && shift # the "--"
    SHIM_ID=$default
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --id)
                [[ $# -ge 2 ]] || die_usage "--id requires a value"
                SHIM_ID=$2
                shift 2
                ;;
            *) die_usage "unknown argument: $1" ;;
        esac
    done
    local id
    for id in "${allowed[@]}"; do
        [[ "$id" == "$SHIM_ID" ]] && return 0
    done
    die_usage "unknown config file id: $SHIM_ID"
}

# parse_name_arg "$@" — parses the required `--name <name>` and sets SHIM_NAME.
# Names are single safe path segments: [A-Za-z0-9._-]+, not "." or "..".
parse_name_arg() {
    SHIM_NAME=""
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --name)
                [[ $# -ge 2 ]] || die_usage "--name requires a value"
                SHIM_NAME=$2
                shift 2
                ;;
            *) die_usage "unknown argument: $1" ;;
        esac
    done
    [[ -n "$SHIM_NAME" ]] || die_usage "--name is required"
    if [[ ! "$SHIM_NAME" =~ ^[A-Za-z0-9._-]+$ || "$SHIM_NAME" == "." || "$SHIM_NAME" == ".." ]]; then
        die_usage "invalid name: $SHIM_NAME"
    fi
}

# parse_session_args "$@" — parses `--session <key> [--turn <id>]` and sets
# SHIM_SESSION (required), SHIM_TURN (may be empty) and SHIM_SESSION_SLUG (the
# key sanitized for use as a file name; keys are opaque).
parse_session_args() {
    SHIM_SESSION=""
    SHIM_TURN=""
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --session|--turn)
                [[ $# -ge 2 ]] || die_usage "$1 requires a value"
                if [[ "$1" == --session ]]; then SHIM_SESSION=$2; else SHIM_TURN=$2; fi
                shift 2
                ;;
            *) die_usage "unknown argument: $1" ;;
        esac
    done
    [[ -n "$SHIM_SESSION" ]] || die_usage "--session is required"
    SHIM_SESSION_SLUG=$(printf %s "$SHIM_SESSION" | tr -c 'A-Za-z0-9._-' '_')
}

# parse_no_args "$@" — for verbs that take no arguments.
parse_no_args() {
    [[ $# -eq 0 ]] || die_usage "unknown argument: $1"
}

# --- privileges ----------------------------------------------------------------

# is_root — true when running as uid 0 (verbs are invoked as root over SSH).
is_root() {
    [[ "$(id -u)" == "0" ]]
}

# run_as_claworc <cmd> [args...] — exec <cmd> as the claworc user with
# HOME set, so agent state stays claworc-owned. Replaces the current process;
# stdin/stdout/stderr and the exit status pass straight through.
run_as_claworc() {
    if is_root && [[ -x /command/s6-setuidgid ]]; then
        exec env HOME="$SHIM_HOME" /command/s6-setuidgid "$SHIM_USER" "$@"
    fi
    exec "$@"
}

# chown_claworc <path>... — give paths to claworc when running as root.
chown_claworc() {
    if is_root; then
        chown "$SHIM_USER:$SHIM_USER" "$@" 2>/dev/null || true
    fi
}

# --- files ---------------------------------------------------------------------

# mkdir_claworc <dir> — mkdir -p that gives every directory it creates to
# claworc (when root), so e.g. ~/.claude/skills does not leave a root-owned
# ~/.claude behind.
mkdir_claworc() {
    local d=$1 missing=()
    while [[ ! -d "$d" ]]; do
        missing=("$d" ${missing[@]+"${missing[@]}"})
        d=$(dirname -- "$d")
    done
    for d in ${missing[@]+"${missing[@]}"}; do
        mkdir -- "$d"
        chown_claworc "$d"
    done
}

# atomic_write <path> [mode] — replace <path> with stdin, byte for byte.
# Writes a temp file in the same directory, chowns it to claworc (when root),
# then renames it over the target. No parsing or validation: the frontend
# validates by the meta-declared language before calling config-set.
atomic_write() {
    local path=$1 mode=${2:-0644} dir tmp
    dir=$(dirname -- "$path")
    mkdir_claworc "$dir"
    tmp=$(mktemp "$dir/.$(basename -- "$path").shim-tmp.XXXXXX")
    if ! cat > "$tmp"; then
        rm -f -- "$tmp"
        return 1
    fi
    chmod "$mode" "$tmp"
    chown_claworc "$tmp"
    mv -f -- "$tmp" "$path"
}

# cat_file <path> — print a config file, exit 1 when it does not exist.
cat_file() {
    [[ -f "$1" ]] || { printf 'config file not found: %s\n' "$1" >&2; exit 1; }
    exec cat -- "$1"
}

# --- skills --------------------------------------------------------------------

# skill_install <skills_dir> <name> — replace <skills_dir>/<name> with the
# contents of the uncompressed tar on stdin. Rejects absolute paths, ".."
# components, and anything that is not a regular file or directory (links,
# devices, FIFOs) with exit 6, leaving the installed skill untouched.
skill_install() {
    local skills_dir=$1 name=$2 target tmpdir staged old listing names entry
    target="$skills_dir/$name"
    mkdir_claworc "$skills_dir"

    tmpdir=$(mktemp -d "$skills_dir/.$name.install.XXXXXX")
    # shellcheck disable=SC2064
    trap "rm -rf -- '$tmpdir'" EXIT
    cat > "$tmpdir/skill.tar"

    listing=$(tar -tvf "$tmpdir/skill.tar" 2>"$tmpdir/tar.err") ||
        die_validation "invalid skill archive: $(head -n1 "$tmpdir/tar.err")"
    names=$(tar -tf "$tmpdir/skill.tar" 2>/dev/null)
    while IFS= read -r entry; do
        [[ -n "$entry" ]] || continue
        case "${entry:0:1}" in
            -|d) ;;
            *) die_validation "skill archive may only contain regular files and directories: ${entry}" ;;
        esac
    done <<< "$listing"
    while IFS= read -r entry; do
        [[ -n "$entry" ]] || continue
        [[ "$entry" == /* ]] && die_validation "absolute path in skill archive: $entry"
        [[ "/$entry/" == */../* ]] && die_validation "path escapes the skill directory: $entry"
    done <<< "$names"

    staged="$tmpdir/skill"
    mkdir -- "$staged"
    tar -xf "$tmpdir/skill.tar" -C "$staged" --no-same-owner --no-same-permissions
    chmod -R u+rwX,go+rX,go-w,ug-s "$staged"
    chown_claworc -R "$staged"

    old="$tmpdir/old"
    [[ -e "$target" ]] && mv -- "$target" "$old"
    mv -- "$staged" "$target"
    # $tmpdir (archive + previous version) is removed by the EXIT trap.
}

# skill_remove <skills_dir> <name> — delete <skills_dir>/<name>. Idempotent.
skill_remove() {
    rm -rf -- "${1:?}/${2:?}"
}

# --- chat stream ---------------------------------------------------------------

# chat_stream_loop — the generic `chat-stream --session <key>` verb
# (docs/shim.md), built only on this image's own chat-send / chat-abort /
# session-reset verbs. Call after parse_session_args.
#
# stdin: one command per line — `send <turn-id> <base64-message>`, `abort`,
# `reset`. stdout: `{"v":1,"event":"ready"}`, then the chat-send JSONL of
# every turn. A single worker (fed through a FIFO) is the only writer to
# stdout, so lines never interleave; it runs sends and resets in order.
# `abort` runs chat-abort immediately. On stdin EOF queued commands are
# dropped, the in-flight turn is aborted, and the verb exits 0.
chat_stream_loop() {
    local tmpdir fifo worker line cmd turn b64 i
    tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/claworc-chat-stream.XXXXXX")
    fifo="$tmpdir/commands"
    mkfifo -m 0600 "$fifo"
    # shellcheck disable=SC2064
    trap "rm -rf -- '$tmpdir'" EXIT

    _chat_stream_worker "$tmpdir" < "$fifo" &
    worker=$!
    exec 3> "$fifo"
    # shellcheck disable=SC2064
    trap "touch '$tmpdir/closing'; _chat_stream_abort; kill $worker 2>/dev/null; exit 0" TERM HUP INT

    while IFS= read -r line; do
        line=${line%$'\r'}
        read -r cmd turn b64 <<< "$line" || true
        case "$cmd" in
            send)
                if [[ -z "$turn" ]]; then
                    printf 'chat-stream: send without a turn id\n' >&2
                    continue
                fi
                printf 'send %s %s\n' "$turn" "$b64" >&3
                ;;
            reset) printf 'reset\n' >&3 ;;
            abort) _chat_stream_abort ;;
            '') ;;
            *) printf 'chat-stream: unknown command: %s\n' "$cmd" >&2 ;;
        esac
    done

    # EOF: drop anything still queued, abort the in-flight turn, and wait
    # (re-aborting every 2s, giving up after 10s) for the worker to finish.
    touch "$tmpdir/closing"
    exec 3>&-
    _chat_stream_abort
    for ((i = 1; i <= 100; i++)); do
        kill -0 "$worker" 2>/dev/null || break
        ((i % 20 == 0)) && _chat_stream_abort
        sleep 0.1
    done
    kill "$worker" 2>/dev/null || true
    wait "$worker" 2>/dev/null || true
    return 0
}

_chat_stream_abort() {
    "$SHIM_DIR/chat-abort" --session "$SHIM_SESSION" < /dev/null > /dev/null 2>&1 || true
}

# _chat_stream_worker <tmpdir> — reads queued commands on stdin (the FIFO).
_chat_stream_worker() {
    local tmpdir=$1 cmd turn b64 status err
    printf '{"v":1,"event":"ready"}\n'
    while read -r cmd turn b64; do
        [[ -e "$tmpdir/closing" ]] && continue
        case "$cmd" in
            send)
                if ! printf '%s' "$b64" | base64 -d > "$tmpdir/message" 2>/dev/null; then
                    printf '{"v":1,"event":"error","turn":%s,"code":"bad_message","text":"message is not valid base64","fatal":true}\n' "$(json_string "$turn")"
                    printf '{"v":1,"event":"end","turn":%s,"stop_reason":"error","text":""}\n' "$(json_string "$turn")"
                    continue
                fi
                status=0
                "$SHIM_DIR/chat-send" --session "$SHIM_SESSION" --turn "$turn" < "$tmpdir/message" || status=$?
                if ((status != 0)); then
                    # Contract: chat-send exits 0 iff it emitted `end`.
                    printf '{"v":1,"event":"error","turn":%s,"code":"shim_exec_failed","text":%s,"fatal":true}\n' \
                        "$(json_string "$turn")" "$(json_string "chat-send exited with status $status")"
                    printf '{"v":1,"event":"end","turn":%s,"stop_reason":"error","text":""}\n' "$(json_string "$turn")"
                fi
                ;;
            reset)
                if ! err=$("$SHIM_DIR/session-reset" --session "$SHIM_SESSION" < /dev/null 2>&1 > /dev/null); then
                    printf '{"v":1,"event":"error","code":"reset_failed","text":%s,"fatal":false}\n' \
                        "$(json_string "session-reset failed: ${err:-no details}")"
                fi
                ;;
        esac
    done
}
