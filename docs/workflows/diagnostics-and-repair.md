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
agent-team overview --source queue --reason dead --commands
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

Use `--source`, `--reason`, `--sort`, or `--limit` when the action hint list
should be filtered or bounded for scripts. These flags do not hide the
underlying overview counts; they only select which action hints are rendered in
text, JSON, templates, or `--commands` output.

When daemon metadata contains crashed instances or stale recorded running PIDs,
overview includes runtime counts and suggests `agent-team resume-plan
--status crashed --sort action --limit 10`, `agent-team resume-plan
--runtime-stale --sort stale --limit 10`, or the matching team- or
pipeline-scoped `resume-plan` command. Add `--unhealthy` when one report
should include both crashed and stale-running metadata,
`--action start|attach|resume|logs` when you only want one recovery class, or
`--summary --json` when dashboards need counts instead of full commands. Add
`--commands` when a script needs one recommended recovery command per line
after filtering, sorting, and limiting.
Resume-plan also probes positive recorded PIDs for `running` metadata; stale
rows are marked in JSON/text, unhealthy totals count crashed plus stale-running
rows, and summaries expose both counts before recommending the right start,
resume, or log fallback.
Add `--last-message` to `overview`, `team overview`, `next`, `team next`, or
the resume-plan command itself when Codex log fallbacks should point at the
clean final response sidecar rather than raw daemon logs.
When daemon metadata resolves to a durable job, resume-plan recommendations
prefer job-scoped attach/log commands in text, JSON, templates, and
`--commands` output. Pipeline step ownership is preserved with `--step` so the
follow-up stays on the exact stage rather than the broader job default.
Docker-like views keep `stale` for old `status.toml` files and expose stale
runtime metadata separately as `runtime_stale`. Their `--unhealthy` filters
include crashed rows, status-stale rows, and runtime-stale rows, so
`agent-team ps --unhealthy --json`, `agent-team health --unhealthy --json`,
`agent-team stats --unhealthy`, `agent-team logs --list --unhealthy`, and
`agent-team events --unhealthy` all surface missing recorded PIDs consistently.
Use `--runtime-stale` on those views when you only want stale recorded runtime
PIDs and do not want crashed or status-stale rows mixed in.
Use `agent-team events --job SQU-42 --step implement` when lifecycle history
needs the same work-unit or pipeline-stage scope as job, pipeline, and team log
views.
Event `--format` rows expose `.Job`, `.Ticket`, `.Branch`, and `.PR`, and
`--summary` includes matching counts when those fields are present.

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
agent-team next --sort source --limit 10
agent-team next --source queue --commands
agent-team next --source jobs --reason stale_running
agent-team team next delivery
agent-team team next delivery --sort command --limit 5
agent-team team next delivery --commands
agent-team team next delivery --source jobs --reason stale_running
```

`next` is a compact command-hint view derived from overview. Text output stays
focused on copyable commands; JSON output also includes `action_details` so
scripts can group recommendations by source and reason without parsing command
strings. Add `--last-message` when runtime recommendations should carry the
same clean Codex final-message preference into their `resume-plan` follow-ups.
Add `--commands` when scripts need only the filtered, sorted, and limited
commands with no headers or reason labels. When `overview`,
`team overview`, `next`, or `team next` was scoped with `--target` or `--repo`,
command-only `agent-team` follow-ups preserve that selected repo.

Use `--source` to narrow recommendations to one subsystem such as `queue`,
`jobs`, `runtime`, or `pipelines`. Use `--reason` when an automation only wants a
specific trigger; values match exactly, or as prefixes before `=`, so
`--reason dead` matches a detail reason like `dead=2`. Use `--sort source`,
`--sort reason`, or `--sort command` before `--limit` when a script needs stable
grouped recommendations.
When outstanding queue, schedule, status, or ready pipeline work can be drained,
overview adds `agent-team drain` (or `agent-team team drain <team>`). Filter that
shortcut with `agent-team next --source overview --reason drainable_work`.
When the ready work should be previewed for just one cycle, pipeline and team
recommendation rows prefer `pipeline tick <pipeline> --dry-run --preview-routes`
or `team tick <team> --dry-run --preview-routes` so queued dispatches and ready
steps stay in the same scoped handoff.

Use it in scripts or when a human wants a short checklist.

## Health

```sh
agent-team health
agent-team health --jobs
agent-team health --strict-topology
agent-team health --json
agent-team health --jobs --commands
agent-team team health delivery --jobs
```

Health exits nonzero when unhealthy in one-shot mode.

With `--jobs`, stuck or failed jobs can make health fail. This is useful in CI or operator dashboards.

Add `--last-message` when runtime issue remediation should carry clean Codex
final-message fallbacks into `resume-plan` follow-ups. Add `--commands` when a
script needs only the issue remediation commands, one per line, while
preserving the same healthy/unhealthy exit code. When `health` or `team health`
is scoped with `--target` or `--repo`, command-only `agent-team` follow-ups
preserve that selected repo.

Health also reports job, queue, and outbox quarantine inventory as warning issues with scoped recovery actions when ownership resolves to one job, pipeline, or team.

Crashed and stale-runtime instance issues include an `action=` hint for
`agent-team runtime resume-plan`, scoped to the owning job when daemon metadata
records one and scoped to `team resume-plan` from team health or monitor.

## Monitor and Watch

```sh
agent-team monitor --jobs --schedules --last-message
agent-team monitor --plan --jobs --commands
agent-team monitor -w --jobs --events 20
agent-team watch --jobs --last-message
agent-team team monitor delivery --jobs --schedules --last-message
agent-team team monitor delivery --plan --jobs --commands
agent-team team watch delivery --jobs --schedules
```

Monitor combines health, job/queue/outbox recovery signals, inbox counts, instance rows, resources, events, jobs, schedules, and plan previews. Add `--last-message` when stale Codex runtime recovery hints should point at clean final-response sidecars, or `--commands` when scripts need one command per line from the visible health, plan, and job sections. `team monitor <team>` applies the same view to team-owned runtime, queue, and outbox quarantine before rendering recovery actions, and `team watch <team>` is the continuous shortcut.

## Snapshot

```sh
agent-team snapshot --output diagnostics.json
agent-team snapshot --json
agent-team snapshot --format '{{.Repo}} {{len .Jobs}}'
agent-team pipeline snapshot ticket_to_pr --output ticket-to-pr-diagnostics.json
agent-team pipeline snapshot ticket_to_pr --format '{{.Pipeline}} {{len .Jobs}}'
agent-team team snapshot delivery --output delivery-diagnostics.json
agent-team team snapshot delivery --format '{{.Team.Name}} {{len .Jobs}}'
agent-team job snapshot squ-42 --output squ-42-diagnostics.json
agent-team job snapshot squ-42 --format '{{.Job.ID}} {{.Job.Status}}'
agent-team snapshot diff before-repair.json after-repair.json
agent-team snapshot diff before-repair.json after-repair.json --section provenance
agent-team snapshot diff before-repair.json after-repair.json --section git
agent-team snapshot diff before-repair.json after-repair.json --section runtime
agent-team snapshot diff before-repair.json after-repair.json --section health
agent-team snapshot diff before-repair.json after-repair.json --section plan
agent-team snapshot diff before-repair.json after-repair.json --section triage
agent-team snapshot diff before-repair.json after-repair.json --section next
agent-team snapshot diff before-repair.json after-repair.json --section instances
agent-team snapshot diff before-repair.json after-repair.json --section inbox
agent-team snapshot diff before-repair.json after-repair.json --section outbox
agent-team snapshot diff before-repair.json after-repair.json --section queue
agent-team snapshot diff before-repair.json after-repair.json --section quarantine
agent-team snapshot diff before-repair.json after-repair.json --section intake
agent-team snapshot diff before-repair.json after-repair.json --section timeline
agent-team snapshot diff before-repair.json after-repair.json --output repair-diff.json
agent-team snapshot diff before-repair.json after-repair.json --action changed
agent-team snapshot diff before-repair.json after-repair.json --summary
agent-team snapshot diff before-repair.json after-repair.json --sort action --limit 20
agent-team snapshot diff before-repair.json after-repair.json --limit 20
agent-team snapshot diff before-repair.json after-repair.json --format '{{.Summary.TotalChanges}} {{.Summary.Queue.Changed}}'
agent-team snapshot diff before-repair.json after-repair.json --exit-code
```

Snapshots are redacted by default and are designed for debugging or handoff. Snapshot JSON includes a top-level `provenance` object with the command, scope, subject, redaction setting, and collection limits used to create the artifact. Use `pipeline snapshot` when the handoff only needs one workflow's pipeline status, explained jobs, owned jobs, bounded audit/lifecycle timeline, inbox summaries, job-owned queue/quarantine state, and dry-run advance previews; use `job snapshot` when the handoff is a post-mortem for one durable job. Global, pipeline, team, and job snapshot commands also accept `--format` for concise script output; it cannot be combined with `--json` or `--output`. Use `snapshot diff` to compare two saved global, team, pipeline, or job artifacts after a tick, repair, or manual intervention; add `--output repair-diff.json` to save the structured comparison as a handoff artifact, add `--section provenance`, `--section git`, `--section runtime`, `--section health`, `--section plan`, `--section triage`, `--section next`, `--section instances`, `--section inbox`, `--section outbox`, `--section queue`, `--section job_quarantine`, `--section outbox_quarantine`, `--section queue_quarantine`, `--section quarantine`, `--section schedules`, `--section intake`, `--section events`, `--section timeline`, or another section to focus the comparison, add `--action added`, `--action removed`, or `--action changed` when a script should compare only selected change kinds, add `--summary` when logs only need metadata and counters, add `--sort action` to group added/removed/changed rows before limiting, add `--limit 20` to keep emitted change details bounded while preserving summary counters, add `--format '{{.Summary.TotalChanges}}'` for script-friendly output, and add `--exit-code` when a script should fail on any detected difference. Git diffs compare branch, commit, upstream, dirty-state, and ahead/behind counts. Runtime diffs compare selected profile, binary/path, env overrides, availability, runtime capabilities, and job runtime lifecycle/exit metadata. Health diffs compare daemon readiness, instance/queue/intake/job summary counts, declared topology counts, and issue-code/severity counts. Plan diffs compare daemon state, desired action counts, and per-instance desired actions. Triage diffs compare job attention rows, reason/severity counts, ready steps, status previews, and triage queue summaries. Next-action diffs compare recommended commands and their source/reason labels, or the action list captured by job snapshots. Inbox diffs compare instance-level mailbox counts, unread cursors, and latest-message identity; outbox diffs compare sandbox dispatch IDs, state, source, job, target, and last error; timeline diffs compare combined audit/lifecycle handoff rows by source, job, timestamp, kind, and owner; intake diffs include both delivery rows and duplicate request-id groups.

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
- inbox summaries
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
agent-team job doctor
agent-team queue doctor
agent-team outbox doctor
agent-team intake doctor
agent-team pipeline doctor --all
agent-team team doctor --all
```

Doctor commands validate structure and data integrity.

Use `job doctor --quarantine --dry-run`, `queue doctor --quarantine --dry-run`, or `outbox doctor --quarantine --dry-run` before moving malformed active files into their quarantine directories.
Add `--commands` to top-level, job, queue, outbox, or intake doctor commands when automation needs only the recommended follow-up commands.
Top-level doctor command output also points at `pipeline doctor --all --json` or `team doctor --all --json` when workflow topology needs a focused detail report.
Use `job quarantine --summary --json` when automation only needs preserved job-file counts, or `job quarantine --commands` when it needs restore/drop dry-run commands for the visible preserved job files.

## Repair

```sh
agent-team repair --dry-run --jobs
agent-team repair --dry-run --jobs --last-message
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
agent-team pipeline repair ticket_to_pr --retry-pipelines --wait --wait-status running --wait-timeout 30s
agent-team repair --retry-pipelines --dry-run --preview-routes
agent-team repair --retry-pipelines --wait --wait-status running --wait-timeout 30s
agent-team repair --retry-pipelines --runtime codex --dry-run --preview-routes
agent-team repair --retry-pipelines --retry-step review --dry-run --preview-routes
agent-team repair --retry-pipelines --retry-force --retry-message "override after fixing dependency"
agent-team tick --wait --wait-status running --wait-timeout 30s
agent-team pipeline tick ticket_to_pr --dry-run --preview-routes
agent-team pipeline tick ticket_to_pr --wait --wait-status running --wait-timeout 30s
agent-team team tick delivery --wait --wait-status running --wait-timeout 30s
agent-team repair --until-idle
agent-team drain --all-ready-steps --runtime codex
agent-team drain --wait --wait-status running --wait-timeout 30s
agent-team pipeline drain ticket_to_pr --wait --wait-status running --wait-timeout 30s
agent-team team repair delivery --dry-run --jobs
agent-team team repair delivery --dry-run --jobs --last-message
agent-team team repair delivery --retry-pipelines --wait --wait-status running --wait-timeout 30s
agent-team team drain delivery --wait --wait-status running --wait-timeout 30s
```

Repair can:

1. start or reconcile daemon state
2. retry dead-letter queue items
3. optionally mark stale running job work failed with `--timeout-jobs`
4. optionally retry failed pipeline steps with `--retry-pipelines`
5. run a maintenance tick
6. include before/after health snapshots

Add `--last-message` when repair health snapshots should prefer clean Codex
final-response sidecar commands for stale runtime recovery hints.

`--dry-run` should be the first step.
Use `drain` when a script should keep running global maintenance cycles until
the daemon has no immediate schedule, outbox, queue, or pipeline work left.
Add `--wait --wait-status running` when it should then wait for jobs advanced
during those drain cycles to have live owners. Use `team drain <team> --wait
--wait-status running` for the same bounded handoff inside one declared team.
Use `pipeline drain <pipeline> --wait --wait-status running` when the finite
drain should stay inside one workflow's queue and ready steps.
Use `tick --wait --wait-status running` for one foreground maintenance cycle
that should block until any pipeline jobs advanced by that cycle have live
owners. Use `pipeline tick <pipeline> --dry-run --preview-routes` to preview
one workflow's queue and ready-step work, or add `--wait --wait-status running`
when that bounded one-cycle handoff should stay inside one workflow. Use
`team tick <team> --wait --wait-status running` when that bounded handoff should
stay inside one declared team's schedules, queue items, and pipelines. `--wait`
is intentionally one-shot and is not combined with `--watch`, `--until-idle`,
or `--skip-advance`.
Use `--timeout-jobs` after status/event reconciliation when stale running work
should become failed before a retry pass. It covers stale pipeline steps and
stale step-less running jobs; use `--timeout-pipelines` when you only want the
older pipeline-step expiration scope. Add `--timeout-pipeline` or
`--timeout-target-agent` with either timeout mode to stay inside one workflow or
agent role.
Use `--retry-step <id>` with `--retry-pipelines` when a broad repair pass should target only one failed stage, such as rerunning review jobs after fixing a reviewer prompt. Add `--retry-force` only when capped steps should be retried after the underlying external issue has been fixed.
Use `pipeline repair <pipeline> --retry-pipelines --wait --wait-status running`
when a scoped repair should block until every retried or newly advanced job has
a live owner. Use `repair --retry-pipelines --wait --wait-status running` for
the same bounded handoff across workflows, or `team repair <team>
--retry-pipelines --wait --wait-status running` to keep it inside one declared
team. The wait applies to dispatched retry rows and final ready-step advance
rows.
Add `--runtime codex` or `--runtime-bin <path>` when repair retry or final tick advancement should use a one-off runtime override instead of the repo default.

## Recovery Rules of Thumb

| Symptom | First command |
| --- | --- |
| Unsure what is wrong | `agent-team overview` |
| Need exact next commands | `agent-team next --commands` |
| CI wants pass/fail | `agent-team health --jobs` |
| CI wants remediation commands | `agent-team health --jobs --commands` |
| Need only unhealthy instance rows | `agent-team ps --unhealthy --json` |
| Need only stale recorded runtime PIDs | `agent-team ps --runtime-stale --json` |
| Need handoff artifact | `agent-team snapshot --output diagnostics.json` |
| Need script-friendly global snapshot fields | `agent-team snapshot --format '{{.Repo}} {{len .Jobs}}'` |
| Need one workflow handoff artifact | `agent-team pipeline snapshot ticket_to_pr --output ticket-to-pr-diagnostics.json` |
| Need script-friendly workflow snapshot fields | `agent-team pipeline snapshot ticket_to_pr --format '{{.Pipeline}} {{len .Jobs}}'` |
| Need script-friendly team snapshot fields | `agent-team team snapshot delivery --format '{{.Team.Name}} {{len .Jobs}}'` |
| Need one job post-mortem artifact | `agent-team job snapshot squ-42 --output squ-42-diagnostics.json` |
| Need script-friendly job snapshot fields | `agent-team job snapshot squ-42 --format '{{.Job.ID}} {{.Job.Status}}'` |
| Need before/after artifact comparison | `agent-team snapshot diff before.json after.json` |
| Need focused artifact provenance comparison | `agent-team snapshot diff before.json after.json --section provenance` |
| Need focused git context comparison | `agent-team snapshot diff before.json after.json --section git` |
| Need focused runtime profile comparison | `agent-team snapshot diff before.json after.json --section runtime` |
| Need focused health comparison | `agent-team snapshot diff before.json after.json --section health` |
| Need focused desired-plan comparison | `agent-team snapshot diff before.json after.json --section plan` |
| Need focused job-triage comparison | `agent-team snapshot diff before.json after.json --section triage` |
| Need focused next-action comparison | `agent-team snapshot diff before.json after.json --section next` |
| Need focused instance drift comparison | `agent-team snapshot diff before.json after.json --section instances` |
| Need focused inbox drift comparison | `agent-team snapshot diff before.json after.json --section inbox` |
| Need focused outbox drift comparison | `agent-team snapshot diff before.json after.json --section outbox` |
| Need focused queue drift comparison | `agent-team snapshot diff before.json after.json --section queue` |
| Need focused quarantine drift comparison | `agent-team snapshot diff before.json after.json --section quarantine` |
| Need focused intake drift comparison | `agent-team snapshot diff before.json after.json --section intake` |
| Need focused timeline drift comparison | `agent-team snapshot diff before.json after.json --section timeline` |
| Need saved before/after diff artifact | `agent-team snapshot diff before.json after.json --output diff.json` |
| Need only changed artifact diff rows | `agent-team snapshot diff before.json after.json --action changed` |
| Need counter-only artifact diff output | `agent-team snapshot diff before.json after.json --summary` |
| Need bounded added/removed/changed detail rows first | `agent-team snapshot diff before.json after.json --sort action --limit 20` |
| Need bounded artifact diff detail rows | `agent-team snapshot diff before.json after.json --limit 20` |
| Need script-friendly diff counters | `agent-team snapshot diff before.json after.json --format '{{.Summary.TotalChanges}} {{.Summary.Queue.Changed}}'` |
| Need scripted before/after drift detection | `agent-team snapshot diff before.json after.json --exit-code` |
| Job parsing fails | `agent-team job doctor --quarantine --dry-run` |
| Queue parsing fails | `agent-team queue doctor --quarantine --dry-run` |
| Outbox parsing fails | `agent-team outbox doctor --quarantine --dry-run` |
| Dead queue entries | `agent-team repair --dry-run --jobs` |
| Crashed or stale runtime metadata | `agent-team resume-plan --unhealthy` |
| Stale running jobs | `agent-team repair --timeout-jobs --dry-run` |
| Stale workflow work | `agent-team pipeline repair ticket_to_pr --timeout-jobs --dry-run --preview-routes` |
| Stale agent-role work across workflows | `agent-team repair --timeout-jobs --timeout-target-agent worker --dry-run` |
| Failed pipeline steps in one workflow | `agent-team pipeline repair ticket_to_pr --retry-pipelines --dry-run --preview-routes` |
| Failed pipeline steps across workflows | `agent-team repair --retry-pipelines --dry-run --preview-routes` |
| Failed stage across jobs | `agent-team repair --retry-pipelines --retry-step review --dry-run --preview-routes` |
| Capped failed stage after fix | `agent-team repair --retry-pipelines --retry-force --retry-step review --dry-run --preview-routes` |
| One workflow one-cycle queue/ready-step preview | `agent-team pipeline tick ticket_to_pr --dry-run --preview-routes` |
| One workflow pending queue/ready-step drain | `agent-team pipeline drain ticket_to_pr --wait --wait-status running --wait-timeout 30s` |
| One stuck job | `agent-team job show <job-id> --events all` |
| One team stuck | `agent-team team overview <team>` |
| Worker blocked | `agent-team job unblock <job-id> <answer...>` |
| Done job cleanup after merge | `agent-team job cleanup <job-id> --dry-run`, then `--merged --verify-pr` |

## Design Requirements for New Diagnostics

When adding diagnostic behavior:

1. Prefer read-only output by default.
2. Include JSON.
3. Include human action hints.
4. Scope actions to job or team when ownership is known.
5. Keep broad actions available when ownership is ambiguous.
6. Add tests for text and JSON if both are user-facing.
7. Validate behavior when the daemon is down.
