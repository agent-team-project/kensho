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
- pipelines
- schedules
- intake
- action hints

Team scoped:

```sh
agent-team team overview delivery
```

## Next

```sh
agent-team next
agent-team next --team delivery
agent-team team next delivery
```

`next` is a compact command-hint view derived from overview.

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
agent-team team snapshot delivery --output delivery-diagnostics.json
```

Snapshots are redacted by default and are designed for debugging or handoff.

They include:

- health
- desired-state plan
- instance rows
- jobs
- job triage
- status-derived job previews
- pipeline status
- ready pipeline advance previews
- team doctor findings
- queue items
- queue quarantine inventory
- schedules
- intake deliveries
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
agent-team repair --retry-pipelines --dry-run --preview-routes
agent-team repair --retry-pipelines --retry-step review --dry-run --preview-routes
agent-team repair --until-idle
agent-team team repair delivery --dry-run --jobs
```

Repair can:

1. start or reconcile daemon state
2. retry dead-letter queue items
3. optionally retry failed pipeline steps with `--retry-pipelines`
4. run a maintenance tick
5. include before/after health snapshots

`--dry-run` should be the first step.
Use `--retry-step <id>` with `--retry-pipelines` when a broad repair pass should target only one failed stage, such as rerunning review jobs after fixing a reviewer prompt.

## Recovery Rules of Thumb

| Symptom | First command |
| --- | --- |
| Unsure what is wrong | `agent-team overview` |
| Need exact next commands | `agent-team next` |
| CI wants pass/fail | `agent-team health --jobs` |
| Need handoff artifact | `agent-team snapshot --output diagnostics.json` |
| Queue parsing fails | `agent-team queue doctor --quarantine --dry-run` |
| Dead queue entries | `agent-team repair --dry-run --jobs` |
| Failed pipeline steps | `agent-team repair --retry-pipelines --dry-run --preview-routes` |
| Failed stage across jobs | `agent-team repair --retry-pipelines --retry-step review --dry-run --preview-routes` |
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
