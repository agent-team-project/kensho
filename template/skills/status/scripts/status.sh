#!/usr/bin/env bash
#
# Emit instance status to $AGENT_TEAM_STATE_DIR/status.toml.
#
# Bundled with the `status` skill in agent-team. Invoked as:
#   "$AGENT_TEAM_ROOT"/skills/status/scripts/status.sh set <phase> [flags]
#   "$AGENT_TEAM_ROOT"/skills/status/scripts/status.sh block --reason "..." --ask <name>
#   "$AGENT_TEAM_ROOT"/skills/status/scripts/status.sh clear-block
#   "$AGENT_TEAM_ROOT"/skills/status/scripts/status.sh show
#
# Atomic: writes status.toml.tmp, then renames over status.toml. Readers never
# see partial writes.

set -euo pipefail

VALID_PHASES="planning implementing awaiting_review idle done"

usage() {
    cat <<'EOF' >&2
usage:
  status.sh set <phase> [--desc TEXT] [--ticket ID] [--pr URL] [--branch NAME] [--last-action TEXT]
  status.sh block --reason TEXT --ask NAME
  status.sh clear-block
  status.sh show

phase ∈ {planning, implementing, awaiting_review, idle, done}
(use `status.sh block` to enter the "blocked" phase, so reason and ask are recorded)
EOF
    exit 2
}

require_state_dir() {
    if [[ -z "${AGENT_TEAM_STATE_DIR:-}" ]]; then
        echo "status.sh: AGENT_TEAM_STATE_DIR not set — must run inside an agent-team session." >&2
        exit 2
    fi
    if [[ ! -d "$AGENT_TEAM_STATE_DIR" ]]; then
        # The launcher creates this; if missing, create it rather than fail —
        # an instance whose state dir was rm'd between launch and first
        # status call shouldn't crash on the call.
        mkdir -p "$AGENT_TEAM_STATE_DIR"
    fi
}

valid_phase() {
    local p="$1"
    for v in $VALID_PHASES; do
        if [[ "$p" == "$v" ]]; then return 0; fi
    done
    return 1
}

[[ $# -ge 1 ]] || usage
verb="$1"; shift

case "$verb" in
    set)
        [[ $# -ge 1 ]] || usage
        phase="$1"; shift
        if ! valid_phase "$phase"; then
            echo "status.sh: invalid phase: $phase (valid: $VALID_PHASES; for blocked use 'status block')" >&2
            exit 2
        fi
        desc="" ticket="" pr="" branch="" last_action=""
        while [[ $# -gt 0 ]]; do
            case "$1" in
                --desc) desc="$2"; shift 2 ;;
                --ticket) ticket="$2"; shift 2 ;;
                --pr) pr="$2"; shift 2 ;;
                --branch) branch="$2"; shift 2 ;;
                --last-action) last_action="$2"; shift 2 ;;
                *) echo "status.sh: unknown flag: $1" >&2; usage ;;
            esac
        done
        require_state_dir
        AGENT_TEAM_STATE_DIR="$AGENT_TEAM_STATE_DIR" \
        STATUS_VERB=set \
        STATUS_PHASE="$phase" \
        STATUS_DESC="$desc" \
        STATUS_TICKET="$ticket" \
        STATUS_PR="$pr" \
        STATUS_BRANCH="$branch" \
        STATUS_LAST_ACTION="$last_action" \
            python3 "$(dirname "$0")/_status_write.py"
        ;;
    block)
        reason="" ask=""
        while [[ $# -gt 0 ]]; do
            case "$1" in
                --reason) reason="$2"; shift 2 ;;
                --ask) ask="$2"; shift 2 ;;
                *) echo "status.sh: unknown flag: $1" >&2; usage ;;
            esac
        done
        if [[ -z "$reason" || -z "$ask" ]]; then
            echo "status.sh: 'block' requires --reason and --ask" >&2
            exit 2
        fi
        require_state_dir
        AGENT_TEAM_STATE_DIR="$AGENT_TEAM_STATE_DIR" \
        STATUS_VERB=block \
        STATUS_REASON="$reason" \
        STATUS_ASK="$ask" \
            python3 "$(dirname "$0")/_status_write.py"
        ;;
    clear-block)
        require_state_dir
        AGENT_TEAM_STATE_DIR="$AGENT_TEAM_STATE_DIR" \
        STATUS_VERB=clear-block \
            python3 "$(dirname "$0")/_status_write.py"
        ;;
    show)
        require_state_dir
        f="$AGENT_TEAM_STATE_DIR/status.toml"
        if [[ -f "$f" ]]; then
            cat "$f"
        else
            echo "(no status.toml at $f)" >&2
            exit 1
        fi
        ;;
    -h|--help|help) usage ;;
    *) usage ;;
esac
