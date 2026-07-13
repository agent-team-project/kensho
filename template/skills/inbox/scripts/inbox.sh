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
#   "$AGENT_TEAM_ROOT"/skills/inbox/scripts/inbox.sh ack --all
#   "$AGENT_TEAM_ROOT"/skills/inbox/scripts/inbox.sh send <to> <body...>
#   "$AGENT_TEAM_ROOT"/skills/inbox/scripts/inbox.sh send <to> --message-file <path|->

set -euo pipefail

usage() {
    cat <<'EOF' >&2
usage:
  inbox.sh check
  inbox.sh ack <id>
  inbox.sh ack --all
  inbox.sh send <to> <body...>
  inbox.sh send <to> --message-file <path|->
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
    if [[ -n "${AGENT_TEAM_DAEMON_SOCKET:-}" ]]; then
        echo "$AGENT_TEAM_DAEMON_SOCKET"
    else
        echo "$AGENT_TEAM_ROOT/daemon.sock"
    fi
}

daemon_token() {
    if [[ -z "${AGENT_TEAM_DAEMON_TOKEN_FILE:-}" ]]; then
        echo "inbox.sh: AGENT_TEAM_DAEMON_TOKEN_FILE not set for daemon HTTP auth." >&2
        exit 2
    fi
    if [[ ! -f "$AGENT_TEAM_DAEMON_TOKEN_FILE" ]]; then
        echo "inbox.sh: daemon token file missing: $AGENT_TEAM_DAEMON_TOKEN_FILE" >&2
        exit 1
    fi
    local token
    IFS= read -r token < "$AGENT_TEAM_DAEMON_TOKEN_FILE" || true
    if [[ -z "$token" ]]; then
        echo "inbox.sh: daemon token file is empty: $AGENT_TEAM_DAEMON_TOKEN_FILE" >&2
        exit 1
    fi
    printf '%s' "$token"
}

curl_daemon() {
    if [[ -n "${AGENT_TEAM_DAEMON_URL:-}" ]]; then
        local args=("$@")
        local last_index=$((${#args[@]} - 1))
        local endpoint="${args[last_index]}"
        args[last_index]="${AGENT_TEAM_DAEMON_URL%/}${endpoint#http://daemon}"
        local token
        token="$(daemon_token)"
        curl -sS -H "Authorization: Bearer $token" "${args[@]}"
        return
    fi
    local sock
    sock="$(socket_path)"
    if [[ ! -S "$sock" ]]; then
        echo "inbox.sh: daemon not running ($sock missing)." >&2
        echo "  Start it with \`agent-team daemon start\`." >&2
        exit 1
    fi
    curl --unix-socket "$sock" -sS "$@"
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
        ack_all=0
        if [[ "$id" == "--all" ]]; then
            ack_all=1
            id=""
        fi
        require_team_root
        require_instance
        AGENT_TEAM_ROOT="$AGENT_TEAM_ROOT" \
        AGENT_TEAM_INSTANCE="$AGENT_TEAM_INSTANCE" \
        INBOX_VERB=ack \
        INBOX_ACK_ID="$id" \
        INBOX_ACK_ALL="$ack_all" \
            python3 "$(dirname "$0")/_inbox_read.py"
        ;;
    send)
        [[ $# -ge 2 ]] || usage
        to="$1"; shift
        require_team_root
        require_instance
        export INBOX_TO="$to"
        export INBOX_FROM="$AGENT_TEAM_INSTANCE"
        if [[ "${1:-}" == "--message-file" ]]; then
            [[ $# -eq 2 ]] || usage
            message_file="$2"
            if [[ "$message_file" != "-" && ! -f "$message_file" ]]; then
                echo "inbox.sh: --message-file: file not found: $message_file" >&2
                exit 1
            fi
            # Python reads the file/stdin directly while constructing JSON so
            # multiline and shell-sensitive bodies never pass through argv.
            export INBOX_MESSAGE_FILE="$message_file"
            payload=$(python3 -c 'import json, os, pathlib, sys; path = os.environ["INBOX_MESSAGE_FILE"]; data = sys.stdin.buffer.read() if path == "-" else pathlib.Path(path).read_bytes(); body = data.decode("utf-8"); print(json.dumps({"to": os.environ["INBOX_TO"], "from": os.environ["INBOX_FROM"], "body": body}))')
        else
            body="$*"
            # Retain positional transport for short, simple messages. Env-var
            # passing keeps the already-parsed body off Python's argv.
            export INBOX_BODY="$body"
            payload=$(python3 -c 'import json, os; print(json.dumps({"to": os.environ["INBOX_TO"], "from": os.environ["INBOX_FROM"], "body": os.environ["INBOX_BODY"]}))')
        fi
        curl_daemon -X POST \
             -H "Content-Type: application/json" \
             -d "$payload" \
             http://daemon/v1/message
        echo
        ;;
    -h|--help|help) usage ;;
    *) usage ;;
esac
