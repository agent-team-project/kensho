---
name: product-verify
description: "Run the scheduled product-verifier loop: browser-driven daemon UI checks, UI-vs-CLI data diffing, and capped feedback filing for correctness bugs, UX friction, and product ideas."
---

# Product verifier

You are a synthetic user for the shipped agent-team product. This loop is not a
code audit, harness review, or uptime sentinel. It dogfoods the product surface
the way an operator would, then files feedback into the existing local feedback
store so the feedback-triage loop can cluster and route it.

## Scope

The verifier is a browser-driven synthetic operator when the runtime has
Playwright + Chromium available. It still runs the v1 headless data diff because
the UI-vs-CLI correctness check is browser-independent and catches objective
state mismatches.

Primary objective:

1. Load `/ui` from the daemon HTTP address in headless Chromium.
2. Enter the operator bearer token from `.agent_team/daemon/operator.token`.
3. Click through the available dashboard controls and panel views.
4. Assert the DOM renders: dashboard shell, token flow, connection state,
   summary metrics, and instances/jobs/pipelines/budgets/teams panels.
5. Capture console errors, page errors, failed network requests, and HTTP
   failures for `/ui` and `/v1/*`.
6. Save a screenshot for any broken state and include that screenshot path in
   the feedback item body.
7. Fetch the daemon UI data endpoints with the operator bearer token:
   `/v1/instances`, `/v1/jobs`, and `/v1/topology`.
8. Read equivalent state through the CLI.
9. Diff the two views. Any mismatch is an objective product correctness bug.
10. File each capped mismatch with `agent-team feedback submit --category bug`.

Secondary objective: note subjective UX friction and product ideas from the
user-journey checklist below. File those as `--category friction` or
`--category idea`.

## Guardrails

- Read-only against production state. Do not stop, start, remove, dispatch,
  retry, merge, cancel, or mutate jobs or instances.
- Intended writes are `agent-team feedback submit` and broken-state screenshots
  under the verifier state directory.
- If `.agent_team/daemon/http.addr` is missing or empty and
  `AGENT_TEAM_DAEMON_URL` is not exported, gracefully skip the objective
  endpoint check and file no bug for that. The daemon HTTP listener is opt-in.
- If Playwright or its Chromium browser is unavailable, gracefully skip the
  browser pass and still run the data-diff pass.
- Cap findings per run. Default cap: 5 objective bugs and 5 subjective items.
- Rely on feedback fingerprint deduplication. Use stable, specific one-line
  summaries so repeated runs accumulate frequency instead of creating noise.

## Browser Runtime

A browser-capable verifier runtime needs the Python Playwright package and its
local Chromium browser installed, for example by baking both into a container or
runtime image with:

```sh
python3 -m pip install playwright
python3 -m playwright install chromium
```

The verifier does not connect to an external browser service. The script launches
Playwright's local headless Chromium. If the runtime lacks Playwright or
Chromium, the browser check exits with `status: "skipped"` and code 0 so
non-browser runtimes can keep running the data diff.

## Browser Check

Run from the repo root:

```sh
python3 "$AGENT_TEAM_ROOT/skills/product-verify/scripts/product_verify_browser.py" --max-findings 5
```

The helper:

- reads `.agent_team/daemon/http.addr` or `AGENT_TEAM_DAEMON_URL`;
- reads `.agent_team/daemon/operator.token`;
- launches Playwright headless Chromium;
- loads `/ui`, fills `#tokenInput`, clicks Connect, clicks Refresh, and toggles
  auto-refresh;
- asserts the dashboard DOM renders metrics and panel rows for instances, jobs,
  pipelines, budgets, and teams;
- records browser console errors, page errors, failed requests, and HTTP errors;
- writes a screenshot for broken states under
  `$AGENT_TEAM_STATE_DIR/product-verify/screenshots/` when that env var is set,
  otherwise under `.agent_team/state/product-verifier/product-verify/screenshots/`;
  and
- prints JSON with `status`, `summary`, `checks`, `browser_errors`, and capped
  `findings`.

Exit codes:

- `0`: browser view passed, browser/http was unavailable and skipped, or
  `--no-fail` was set.
- `1`: browser-observed bugs were found. Continue by filing the emitted findings.
- `2`: the check could not run despite an HTTP address and browser dependency
  being configured.

For each emitted finding:

```sh
agent-team feedback submit --category bug "<finding summary> [screenshot: <path>]"
```

Use the `screenshot` field from the helper output when present. The current
feedback CLI stores text only, so include the screenshot path directly in the
feedback body.

## Data-Diff Check

Run from the repo root:

```sh
python3 "$AGENT_TEAM_ROOT/skills/product-verify/scripts/product_verify_diff.py" --max-findings 5
```

The helper:

- reads `.agent_team/daemon/http.addr` or `AGENT_TEAM_DAEMON_URL`;
- reads `.agent_team/daemon/operator.token`;
- fetches `/v1/instances`, `/v1/jobs`, and `/v1/topology`;
- reads the matching repo state with `agent-team --repo <repo> ps --json`,
  `agent-team --repo <repo> job ls --json`, and
  `agent-team --repo <repo> topology show --json`;
- compares explicit stable equivalence projections of the same state; and
- prints JSON with `status`, `comparisons`, and capped `findings`.

The instance equivalence projection is limited to fields both `/v1/instances`
and `agent-team ps --json` faithfully expose for the same daemon-owned state:
`instance`, `agent`, `status`, `runtime`, and `job`. The diff compares only
instance names present in both sources because `ps` can include declared or
status-only rows that are not daemon metadata. CLI-only enrichment such as PR
links, process IDs, runtime binaries, and resume counters is intentionally
excluded, as are `branch` and `workspace` values that `ps` may source from
status files or topology instead of daemon metadata.

Exit codes:

- `0`: views match, or the HTTP listener is not configured and the check was
  skipped.
- `1`: mismatches were found. Continue by filing the emitted findings.
- `2`: the check could not run despite an HTTP address being configured.

For each emitted finding:

```sh
agent-team feedback submit --category bug "<finding summary>"
```

Include the helper output in your own notes if you need detail, but keep the
feedback item itself one clear sentence.

## User-Journey Checklist

Walk these as a product user, not as a maintainer:

1. Dashboard data: do instances, jobs, topology, pipelines, and schedules have
   enough state for an operator to understand what is happening?
2. Correctness: did the browser check and mechanical UI-vs-CLI diff pass?
3. Token flow: does the operator-token requirement make sense in the real UI and
   from the served API behavior and docs?
4. Empty and error states: if a list is empty or a daemon endpoint is skipped,
   is that state understandable?
5. Legibility: are names, statuses, timestamps, budgets, and next actions clear
   from the headless data shape and served UI assets?
6. Representative read-only operator task: choose one non-mutating task, such
   as inspecting topology, jobs, schedules, or instance status, and note any
   friction.

File subjective findings with one sentence each:

```sh
agent-team feedback submit --category friction "<what felt awkward or unclear>"
agent-team feedback submit --category idea "<missing product capability a user would reasonably expect>"
```

## Done Condition

A run is complete when the browser check passed or skipped for lack of
browser/http capability, the mechanical diff passed or skipped for lack of HTTP
configuration, capped bug findings were submitted with screenshot paths when
present, subjective friction or idea findings were submitted when present, and
you have recorded a short summary to your manager state or inbox.
