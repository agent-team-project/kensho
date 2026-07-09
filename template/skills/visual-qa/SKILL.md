---
name: visual-qa
description: Capture macOS GUI screenshots as verifier gate evidence for vision-capable review.
---

# Visual QA

Use this skill when a GUI slice needs visual evidence before review. The first
version is intentionally narrow: it launches the app, captures the current
macOS display with `screencapture`, and writes screenshot evidence for a
vision-capable reviewer to judge. It does not script clicks, form entry, or
multi-step interaction flows yet.

## One-Time macOS Permission

`screencapture` requires Screen Recording permission for the terminal app that
runs the agent runtime. On macOS, grant that once in:

System Settings -> Privacy & Security -> Screen Recording

If permission is missing, `screencapture` can fail with `could not create image`.
The helper treats that as a hard gate failure and prints the permission
requirement; it never silently passes without a screenshot.

## Gate Command

Add the gate only to GUI-specific verifier steps. Non-GUI work should not opt
into this gate.

````toml
instructions = """
Run the verify skill. A vision-capable reviewer must judge the visual-QA
screenshots before approval.

```agent-team-verify-gates
visual-qa :: "$AGENT_TEAM_ROOT/skills/visual-qa/scripts/visual_qa.sh" --app-command "npm run dev -- --host 127.0.0.1 --port 4173" --url "http://127.0.0.1:4173"
```
"""
````

The verifier exposes `AGENT_TEAM_GATE_EVIDENCE_DIR` to gate commands. The
helper writes:

- `screenshot.png`
- `manifest.json`
- `app.log`

The verifier includes those files as `evidence_refs` in
`target/agent-evidence/<job>.json`. The screenshot is evidence capture only; a
vision-capable reviewer such as Claude/Fable must still judge whether the UI is
correct for the ticket.

## Command

```sh
"$AGENT_TEAM_ROOT/skills/visual-qa/scripts/visual_qa.sh" \
  --app-command "npm run dev -- --host 127.0.0.1 --port 4173" \
  --url "http://127.0.0.1:4173"
```

Useful flags:

- `--app-command CMD`: command that launches the app under test.
- `--url URL`: optional URL opened with macOS `open` before capture.
- `--output-dir DIR`: override the evidence directory.
- `--name NAME`: screenshot basename, defaulting to the current gate name.
- `--startup-wait SECONDS`: wait after launching the app before opening/capture.
- `--settle-wait SECONDS`: wait after opening the URL before capture.
- `--allow-app-exit`: allow app commands that return after launching a GUI app.
- `--no-open`: do not call `open`; useful when `--app-command` already shows the target window.
