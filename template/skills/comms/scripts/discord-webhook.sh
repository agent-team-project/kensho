#!/usr/bin/env bash
# Shared Discord delivery boundary. Durable policy lives in discord_delivery.py.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
PYTHON_HELPER="$SCRIPT_DIR/../../../scripts/skills/python.sh"
if [[ ! -f "$PYTHON_HELPER" && -n "${AGENT_TEAM_ROOT:-}" ]]; then
    PYTHON_HELPER="$AGENT_TEAM_ROOT/scripts/skills/python.sh"
fi
if [[ ! -f "$PYTHON_HELPER" ]]; then
    echo "discord-webhook.sh: missing Python helper: $PYTHON_HELPER" >&2
    exit 1
fi
# shellcheck source=../../../scripts/skills/python.sh
# shellcheck disable=SC1091
source "$PYTHON_HELPER"

AGENT_TEAM_PYTHON_BIN="$(agent_team_python311 "discord-webhook.sh")"
exec "$AGENT_TEAM_PYTHON_BIN" "$SCRIPT_DIR/discord_delivery.py" "$@"
