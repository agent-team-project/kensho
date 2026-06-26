# Diagnostics and Repair

Diagnostics are a first-class product area.

The CLI should help operators answer:

- what is unhealthy?
- what work needs attention?
- which scope owns it?
- what exact command should I preview or run next?

## Overview

```sh
agent-team overview
agent-team overview --json
agent-team overview -w
```

Overview is the shortest read-only answer to "what needs attention?"

It summarizes:

- health
- topology
- jobs
- queues
- queue quarantine
- runtime metadata
- pipelines
- schedules
- intake
- action hints

JSON output includes both `actions` (the compatibility list of command
strings) and `action_details` with a command, source, reason, and team scope
where applicable.

When daemon metadata contains crashed instances or stale recorded running PIDs,
overview includes runtime counts and suggests `agent-team runtime resume-plan
--status crashed`, `agent-team runtime resume-plan --stale`, or the matching
team-scoped `agent-team team runtime resume-plan <team> ...` command. Add
`--unhealthy` when one report should include both crashed and stale-running
metadata, `--action start|attach|resume|logs` when you only want one recovery
class, or `--summary --json` when dashboards need counts instead of full
commands.
Resume-plan also probes positive recorded PIDs for `running` metadata; stale
rows are marked in JSON/text, unhealthy totals count crashed plus stale-running
rows, and summaries expose both counts before recommending the right start,
resume, or log fallback.

Team scoped:

```sh
agent-team team overview delivery
```

## Next

```sh
agent-team next
agent-team next --team delivery
agent-team next --source queue
agent-team next --reason dead
agent-team next --source jobs --reason stale_running
agent-team team next delivery
agent-team team next delivery --source jobs --reason stale_running
```

`next` is a compact command-hint view derived from overview. Text output stays
focused on copyable commands; JSON output also includes `action_details` so
scripts can group recommendations by source and reason without parsing command
strings.

Use `--source` to narrow recommendations to one subsystem such as `queue`,
`jobs`, `runtime`, or `pipelines`. Use `--reason` when an automation only wants a
specific trigger; values match exactly, or as prefixes before `=`, so
`--reason dead` matches a detail reason like `dead=2`.
When outstanding queue, schedule, status, or ready pipeline work can be drained,
overview adds `agent-team drain` (or `agent-team team drain <team>`). Filter that
shortcut with `agent-team next --source overview --reason drainable_work`.

Use it in scripts or when a human wants a short checklist.

## Health

```sh
agent-team health
agent-team health --jobs
agent-team health --strict-topology
agent-team health --json
agent-team team health delivery --jobs
```

Health exits nonzero when unhealthy in one-shot mode.

With `--jobs`, stuck or failed jobs can make health fail. This is useful in CI or operator dashboards.

Crashed instance issues include an `action=` hint for `agent-team runtime
resume-plan`, scoped to the owning job when daemon metadata records one.

## Monitor and Watch

```sh
agent-team monitor --jobs --schedules
agent-team monitor -w --jobs --events 20
agent-team watch --jobs
agent-team team monitor delivery --jobs --schedules
```

Monitor combines health, instance rows, resources, events, jobs, schedules, and plan previews.

## Snapshot

```sh
agent-team snapshot --output diagnostics.json
agent-team snapshot --json
agent-team pipeline snapshot ticket_to_pr --output ticket-to-pr-diagnostics.json
agent-team team snapshot delivery --output delivery-diagnostics.json
agent-team snapshot diff before-repair.json after-repair.json
agent-team snapshot diff before-repair.json after-repair.json --section instances
agent-team snapshot diff before-repair.json after-repair.json --section queue
agent-team snapshot diff before-repair.json after-repair.json --section queue_quarantine
agent-team snapshot diff before-repair.json after-repair.json --section intake
agent-team snapshot diff before-repair.json after-repair.json --exit-code
```

Snapshots are redacted by default and are designed for debugging or handoff. Use `pipeline snapshot` when the handoff only needs one workflow's pipeline status, explained jobs, owned jobs, job-owned queue/quarantine state, and dry-run advance previews. Use `snapshot diff` to compare two saved artifacts after a tick, repair, or manual intervention; add `--section instances`, `--section queue`, `--section queue_quarantine`, `--section schedules`, `--section intake`, `--section events`, or another section to focus the comparison, and add `--exit-code` when a script should fail on any detected difference. Intake diffs include both delivery rows and duplicate request-id groups.

They include:

- overview and next-action details
- git branch, commit, and dirty-state context
- health
- desired-state plan
- instance rows
- jobs
- job triage
- status-derived job previews
- pipeline status
- pipeline explain step diagnostics
- ready pipeline advance previews
- team doctor findings
- queue items
- queue quarantine inventory
- schedules
- intake deliveries and duplicate request-id groups
- runtime profile
- lifecycle events

Section failures are recorded in the JSON instead of aborting the whole report.

## Doctor

```sh
agent-team doctor
agent-team queue doctor
agent-team intake doctor
agent-team pipeline doctor --all
agent-team team doctor --all
```

Doctor commands validate structure and data integrity.

Use `queue doctor --quarantine --dry-run` before moving malformed queue files out of active queue directories.

## Repair

```sh
agent-team repair --dry-run --jobs
agent-team repair --skip-daemon
agent-team repair --skip-queue
agent-team repair --skip-tick
agent-team repair --timeout-jobs --dry-run
agent-team pipeline repair ticket_to_pr --timeout-jobs --dry-run --preview-routes
agent-team repair --timeout-jobs --timeout-pipeline ticket_to_pr --dry-run
agent-team repair --timeout-jobs --timeout-target-agent worker --dry-run
agent-team repair --timeout-pipelines --timeout-pipeline ticket_to_pr --dry-run
agent-team repair --timeout-pipelines --timeout-target-agent worker --dry-run
agent-team pipeline repair ticket_to_pr --retry-pipelines --dry-run --preview-routes
agent-team repair --retry-pipelines --dry-run --preview-routes
agent-team repair --retry-pipelines --runtime codex --dry-run --preview-routes
agent-team repair --retry-pipelines --retry-step review --dry-run --preview-routes
agent-team repair --retry-pipelines --retry-force --retry-message "override after fixing dependency"
agent-team repair --until-idle
agent-team drain --all-ready-steps --runtime codex
agent-team team repair delivery --dry-run --jobs
```

Repair can:

1. start or reconcile daemon state
2. retry dead-letter queue items
3. optionally mark stale running job work failed with `--timeout-jobs`
4. optionally retry failed pipeline steps with `--retry-pipelines`
5. run a maintenance tick
6. include before/after health snapshots

`--dry-run` should be the first step.
Use `drain` when a script should keep running global maintenance cycles until
the daemon has no immediate schedule, queue, or pipeline work left.
Use `--timeout-jobs` after status/event reconciliation when stale running work
should become failed before a retry pass. It covers stale pipeline steps and
stale step-less running jobs; use `--timeout-pipelines` when you only want the
older pipeline-step expiration scope. Add `--timeout-pipeline` or
`--timeout-target-agent` with either timeout mode to stay inside one workflow or
agent role.
Use `--retry-step <id>` with `--retry-pipelines` when a broad repair pass should target only one failed stage, such as rerunning review jobs after fixing a reviewer prompt. Add `--retry-force` only when capped steps should be retried after the underlying external issue has been fixed.
Add `--runtime codex` or `--runtime-bin <path>` when repair retry or final tick advancement should use a one-off runtime override instead of the repo default.

## Recovery Rules of Thumb

| Symptom | First command |
| --- | --- |
| Unsure what is wrong | `agent-team overview` |
| Need exact next commands | `agent-team next` |
| CI wants pass/fail | `agent-team health --jobs` |
| Need handoff artifact | `agent-team snapshot --output diagnostics.json` |
| Need one workflow handoff artifact | `agent-team pipeline snapshot ticket_to_pr --output ticket-to-pr-diagnostics.json` |
| Need before/after artifact comparison | `agent-team snapshot diff before.json after.json` |
| Need focused instance drift comparison | `agent-team snapshot diff before.json after.json --section instances` |
| Need focused queue drift comparison | `agent-team snapshot diff before.json after.json --section queue` |
| Need focused quarantine drift comparison | `agent-team snapshot diff before.json after.json --section queue_quarantine` |
| Need focused intake drift comparison | `agent-team snapshot diff before.json after.json --section intake` |
| Need scripted before/after drift detection | `agent-team snapshot diff before.json after.json --exit-code` |
| Queue parsing fails | `agent-team queue doctor --quarantine --dry-run` |
| Dead queue entries | `agent-team repair --dry-run --jobs` |
| Crashed or stale runtime metadata | `agent-team runtime resume-plan --unhealthy` |
| Stale running jobs | `agent-team repair --timeout-jobs --dry-run` |
| Stale workflow work | `agent-team pipeline repair ticket_to_pr --timeout-jobs --dry-run --preview-routes` |
| Stale agent-role work across workflows | `agent-team repair --timeout-jobs --timeout-target-agent worker --dry-run` |
| Failed pipeline steps in one workflow | `agent-team pipeline repair ticket_to_pr --retry-pipelines --dry-run --preview-routes` |
| Failed pipeline steps across workflows | `agent-team repair --retry-pipelines --dry-run --preview-routes` |
| Failed stage across jobs | `agent-team repair --retry-pipelines --retry-step review --dry-run --preview-routes` |
| Capped failed stage after fix | `agent-team repair --retry-pipelines --retry-force --retry-step review --dry-run --preview-routes` |
| One stuck job | `agent-team job show <job-id> --events all` |
| One team stuck | `agent-team team overview <team>` |
| Worker blocked | `agent-team job unblock <job-id> <answer...>` |
| Cleanup after merge | `agent-team job cleanup <job-id> --dry-run`, then `--merged --verify-pr` |

## Design Requirements for New Diagnostics

When adding diagnostic behavior:

1. Prefer read-only output by default.
2. Include JSON.
3. Include human action hints.
4. Scope actions to job or team when ownership is known.
5. Keep broad actions available when ownership is ambiguous.
6. Add tests for text and JSON if both are user-facing.
7. Validate behavior when the daemon is down.
