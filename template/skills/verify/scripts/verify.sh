#!/usr/bin/env bash
#
# Run deterministic verification gates for an agent-team job.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
PYTHON_HELPER="$SCRIPT_DIR/../../../scripts/skills/python.sh"
if [[ ! -f "$PYTHON_HELPER" && -n "${AGENT_TEAM_ROOT:-}" ]]; then
    PYTHON_HELPER="$AGENT_TEAM_ROOT/scripts/skills/python.sh"
fi
if [[ ! -f "$PYTHON_HELPER" ]]; then
    echo "verify.sh: missing Python helper: $PYTHON_HELPER" >&2
    exit 1
fi
# shellcheck source=../../../scripts/skills/python.sh
# shellcheck disable=SC1091
source "$PYTHON_HELPER"

python_bin="$(agent_team_python311 "verify.sh")"
exec "$python_bin" "$SCRIPT_DIR/verify.py" "$@"
