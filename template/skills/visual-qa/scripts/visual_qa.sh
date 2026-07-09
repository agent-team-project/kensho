#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
usage: visual_qa.sh --app-command CMD [--url URL] [--output-dir DIR]
                    [--name NAME] [--startup-wait SECONDS]
                    [--settle-wait SECONDS] [--allow-app-exit] [--no-open]

Launch an app and capture the current macOS display with screencapture.
Writes screenshot.png, app.log, and manifest.json under the gate evidence dir.
EOF
}

fail() {
  printf 'visual-qa: %s\n' "$*" >&2
  exit 1
}

safe_name() {
  printf '%s' "$1" | tr -cs 'A-Za-z0-9_.-' '-' | sed -e 's/^-*//' -e 's/-*$//' | tr '[:upper:]' '[:lower:]'
}

number_or_fail() {
  local label="$1"
  local value="$2"
  if [[ ! "$value" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
    fail "$label must be a non-negative number, got '$value'"
  fi
}

write_manifest() {
  local status="$1"
  local message="$2"
  VISUAL_QA_STATUS="$status" \
  VISUAL_QA_MESSAGE="$message" \
  VISUAL_QA_MANIFEST="$manifest_path" \
  VISUAL_QA_SCREENSHOT="$screenshot_path" \
  VISUAL_QA_APP_LOG="$app_log_path" \
  VISUAL_QA_CAPTURE_STDERR="$capture_stderr_path" \
  VISUAL_QA_APP_COMMAND="$app_command" \
  VISUAL_QA_URL="$url" \
  VISUAL_QA_GATE_NAME="$gate_name" \
  python3 - <<'PY'
import datetime as dt
import json
import os
from pathlib import Path

manifest = Path(os.environ["VISUAL_QA_MANIFEST"])
payload = {
    "schema_version": 1,
    "gate": os.environ["VISUAL_QA_GATE_NAME"],
    "status": os.environ["VISUAL_QA_STATUS"],
    "message": os.environ["VISUAL_QA_MESSAGE"],
    "created_at": dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z"),
    "app_command": os.environ["VISUAL_QA_APP_COMMAND"],
    "url": os.environ["VISUAL_QA_URL"],
    "screenshot": os.environ["VISUAL_QA_SCREENSHOT"],
    "app_log": os.environ["VISUAL_QA_APP_LOG"],
    "capture_stderr": os.environ["VISUAL_QA_CAPTURE_STDERR"],
    "reviewer_note": "A vision-capable reviewer must judge the screenshot; capture alone is not approval.",
}
manifest.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY
}

app_command=""
url=""
output_dir=""
gate_name="${AGENT_TEAM_GATE_NAME:-visual-qa}"
startup_wait="${VISUAL_QA_STARTUP_WAIT:-5}"
settle_wait="${VISUAL_QA_SETTLE_WAIT:-2}"
open_target=1
allow_app_exit=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --app-command)
      [[ $# -ge 2 ]] || fail "--app-command requires a value"
      app_command="$2"
      shift 2
      ;;
    --url)
      [[ $# -ge 2 ]] || fail "--url requires a value"
      url="$2"
      shift 2
      ;;
    --output-dir)
      [[ $# -ge 2 ]] || fail "--output-dir requires a value"
      output_dir="$2"
      shift 2
      ;;
    --name)
      [[ $# -ge 2 ]] || fail "--name requires a value"
      gate_name="$2"
      shift 2
      ;;
    --startup-wait)
      [[ $# -ge 2 ]] || fail "--startup-wait requires a value"
      startup_wait="$2"
      shift 2
      ;;
    --settle-wait)
      [[ $# -ge 2 ]] || fail "--settle-wait requires a value"
      settle_wait="$2"
      shift 2
      ;;
    --allow-app-exit)
      allow_app_exit=1
      shift
      ;;
    --no-open)
      open_target=0
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage >&2
      fail "unknown argument: $1"
      ;;
  esac
done

[[ -n "$app_command" ]] || fail "--app-command is required"
number_or_fail "--startup-wait" "$startup_wait"
number_or_fail "--settle-wait" "$settle_wait"

if ! command -v screencapture >/dev/null 2>&1; then
  fail "screencapture is required; visual-qa currently runs on macOS only"
fi
if [[ "$open_target" -eq 1 && -n "$url" ]] && ! command -v open >/dev/null 2>&1; then
  fail "macOS open is required when --url is provided; pass --no-open to skip opening"
fi
if ! command -v python3 >/dev/null 2>&1; then
  fail "python3 is required to write visual-QA evidence manifests"
fi

job_id="$(safe_name "${AGENT_TEAM_JOB_ID:-manual}")"
safe_gate="$(safe_name "$gate_name")"
if [[ -z "$safe_gate" ]]; then
  safe_gate="visual-qa"
fi

if [[ -z "$output_dir" ]]; then
  if [[ -n "${AGENT_TEAM_GATE_EVIDENCE_DIR:-}" ]]; then
    output_dir="$AGENT_TEAM_GATE_EVIDENCE_DIR"
  elif [[ -n "${AGENT_TEAM_EVIDENCE_DIR:-}" ]]; then
    output_dir="$AGENT_TEAM_EVIDENCE_DIR/gates/$job_id/$safe_gate"
  else
    output_dir="target/agent-evidence/gates/$job_id/$safe_gate"
  fi
fi

mkdir -p "$output_dir"
app_log_path="$output_dir/app.log"
capture_stderr_path="$output_dir/screencapture.stderr"
screenshot_path="$output_dir/${safe_gate}.png"
manifest_path="$output_dir/manifest.json"

printf 'visual-qa: launching app command: %s\n' "$app_command"
bash -lc "$app_command" >"$app_log_path" 2>&1 &
app_pid=$!

cleanup() {
  if kill -0 "$app_pid" >/dev/null 2>&1; then
    terminate_tree "$app_pid"
    wait "$app_pid" 2>/dev/null || true
  fi
}

terminate_tree() {
  local pid="$1"
  local child
  if command -v pgrep >/dev/null 2>&1; then
    while IFS= read -r child; do
      [[ -n "$child" ]] || continue
      terminate_tree "$child"
    done < <(pgrep -P "$pid" 2>/dev/null || true)
  fi
  kill "$pid" >/dev/null 2>&1 || true
}
trap cleanup EXIT

sleep "$startup_wait"

if ! kill -0 "$app_pid" >/dev/null 2>&1; then
  app_status=0
  wait "$app_pid" || app_status=$?
  if [[ "$allow_app_exit" -ne 1 ]]; then
    write_manifest "fail" "app command exited before screenshot capture"
    fail "app command exited before screenshot capture with status $app_status; see $app_log_path"
  fi
  printf 'visual-qa: app command exited before capture; continuing because --allow-app-exit was set\n'
fi

if [[ "$open_target" -eq 1 && -n "$url" ]]; then
  printf 'visual-qa: opening %s\n' "$url"
  open "$url"
  sleep "$settle_wait"
fi

printf 'visual-qa: capturing screenshot: %s\n' "$screenshot_path"
if ! screencapture -x "$screenshot_path" 2>"$capture_stderr_path"; then
  capture_error="$(tr '\n' ' ' <"$capture_stderr_path" | sed 's/[[:space:]]\+/ /g')"
  if grep -qi 'could not create image\|not authorized\|permission' "$capture_stderr_path"; then
    message="macOS Screen Recording permission is required for screencapture; grant it to the terminal/runtime app in System Settings -> Privacy & Security -> Screen Recording, then rerun visual-qa"
    write_manifest "fail" "$message"
    fail "$message (screencapture said: ${capture_error:-no stderr})"
  fi
  write_manifest "fail" "screencapture failed"
  fail "screencapture failed: ${capture_error:-no stderr}"
fi

if [[ ! -s "$screenshot_path" ]]; then
  write_manifest "fail" "screencapture produced an empty screenshot"
  fail "screencapture produced an empty screenshot at $screenshot_path"
fi

write_manifest "pass" "screenshot captured"
printf 'visual-qa: screenshot evidence: %s\n' "$screenshot_path"
printf 'visual-qa: manifest evidence: %s\n' "$manifest_path"
