#!/usr/bin/env bash
#
# Daemon-mode inbox: read pending messages from this instance's
# mailbox.jsonl, ack them, or send one to another instance.
#
# Bundled with the `inbox` skill in agent-team.
#
# Usage:
#   "$AGENT_TEAM_ROOT"/skills/inbox/scripts/inbox.sh check
#   "$AGENT_TEAM_ROOT"/skills/inbox/scripts/inbox.sh ack <id>
#   "$AGENT_TEAM_ROOT"/skills/inbox/scripts/inbox.sh send <to> <body...>

set -euo pipefail

usage() {
    cat <<'EOF' >&2
usage:
  inbox.sh check
  inbox.sh ack <id>
  inbox.sh send <to> <body...>
EOF
    exit 2
}

require_team_root() {
    if [[ -z "${AGENT_TEAM_ROOT:-}" ]]; then
        echo "inbox.sh: AGENT_TEAM_ROOT not set — must run inside an agent-team session." >&2
        exit 2
    fi
}

require_instance() {
    if [[ -z "${AGENT_TEAM_INSTANCE:-}" ]]; then
        echo "inbox.sh: AGENT_TEAM_INSTANCE not set — must run inside an agent-team session." >&2
        exit 2
    fi
}

socket_path() {
    echo "$AGENT_TEAM_ROOT/daemon.sock"
}

[[ $# -ge 1 ]] || usage
verb="$1"; shift

case "$verb" in
    check)
        require_team_root
        require_instance
        AGENT_TEAM_ROOT="$AGENT_TEAM_ROOT" \
        AGENT_TEAM_INSTANCE="$AGENT_TEAM_INSTANCE" \
        INBOX_VERB=check \
            python3 "$(dirname "$0")/_inbox_read.py"
        ;;
    ack)
        [[ $# -ge 1 ]] || usage
        id="$1"; shift
        require_team_root
        require_instance
        AGENT_TEAM_ROOT="$AGENT_TEAM_ROOT" \
        AGENT_TEAM_INSTANCE="$AGENT_TEAM_INSTANCE" \
        INBOX_VERB=ack \
        INBOX_ACK_ID="$id" \
            python3 "$(dirname "$0")/_inbox_read.py"
        ;;
    send)
        [[ $# -ge 2 ]] || usage
        to="$1"; shift
        body="$*"
        require_team_root
        require_instance
        sock="$(socket_path)"
        if [[ ! -S "$sock" ]]; then
            echo "inbox.sh: daemon not running ($sock missing)." >&2
            echo "  Start it with \`agent-team daemon start\`." >&2
            exit 1
        fi
        # Build JSON via python so arbitrary characters in $body don't
        # need bash-level escaping. Env-var pass keeps the body off argv.
        export INBOX_TO="$to"
        export INBOX_FROM="$AGENT_TEAM_INSTANCE"
        export INBOX_BODY="$body"
        payload=$(python3 -c 'import json, os; print(json.dumps({"to": os.environ["INBOX_TO"], "from": os.environ["INBOX_FROM"], "body": os.environ["INBOX_BODY"]}))')
        curl --unix-socket "$sock" -sS -X POST \
             -H "Content-Type: application/json" \
             -d "$payload" \
             http://daemon/v1/message
        echo
        ;;
    -h|--help|help) usage ;;
    *) usage ;;
esac
