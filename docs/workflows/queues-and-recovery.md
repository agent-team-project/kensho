# Queues and Recovery

The queue makes dispatch durable.

When an ephemeral target is at capacity, when dispatch fails, or when retry state is needed across daemon restarts, queue entries preserve the work.

## Queue Layout

```text
.agent_team/daemon/queue/
├── pending/
│   └── <id>.json
├── dead/
│   └── <id>.json
└── quarantine/
    └── <timestamp>/
        ├── pending/
        │   └── <id>.json
        └── dead/
            └── <id>.json
```

## Queue Item Fields

Queue entries include:

- id
- state
- event type
- instance target
- concrete instance id
- payload
- attempts
- last error
- next retry
- queued timestamp
- updated timestamp
- dead-letter timestamp

## Listing and Showing

```sh
agent-team queue ls
agent-team queue ls --summary
agent-team queue ls --state dead
agent-team queue ls --job SQU-42
agent-team queue ls --runtime codex
agent-team queue ls --state dead --commands
agent-team queue ls --summary --runtime codex
agent-team queue watch --state dead
agent-team queue show <id>
```

Use `--runtime claude|codex` to narrow active queue entries by the runtime recorded in the dispatch payload, falling back to daemon metadata when a queued item already names a concrete instance. Runtime-filtered summaries include a `runtimes` count and exclude quarantined files whose runtime cannot be known from the quarantine index.
Use `queue watch` when a retry, drain, or repair loop is expected to change active queue rows while you are inspecting them.
Add `--commands` to queue or outbox list views when scripts should print only the ACTION-column commands for the currently visible rows after filters, sort, and limit are applied.
When `queue ls`, `queue show`, `queue quarantine ls`, `queue quarantine show`, `outbox ls`, `outbox show`, `outbox quarantine ls`, `outbox quarantine show`, or top-level queue/outbox recovery commands are run with an explicit `--repo` or legacy `--target`, `--commands` output preserves that selector in emitted `agent-team` follow-ups so scripts can run from outside the target checkout.

Preview daemon drain work before applying it:

```sh
agent-team queue drain --dry-run
agent-team queue drain --dry-run --commands
```

Job-scoped:

```sh
agent-team job queue squ-42
agent-team job queue squ-42 --summary
agent-team job queue squ-42 --runtime codex
agent-team job queue squ-42 --state dead --commands
agent-team job queue squ-42 --summary --runtime codex
agent-team job queue show squ-42 <id>
```

Team-scoped:

```sh
agent-team team queue delivery --state dead
agent-team team queue delivery --summary
agent-team team queue delivery --runtime codex
agent-team team queue delivery --state dead --commands
agent-team team queue delivery --summary --runtime codex
agent-team team queue show delivery <id>
```

## Retry and Drop

Preview retries:

```sh
agent-team queue retry --all --sort attempts --limit 10 --dry-run
agent-team queue retry --all --runtime codex --sort attempts --limit 10 --dry-run
agent-team job queue retry squ-42 --all --sort attempts --limit 10 --dry-run
agent-team job queue retry squ-42 --all --runtime codex --sort attempts --limit 10 --dry-run
agent-team team queue retry delivery --all --job SQU-42 --sort attempts --limit 10 --dry-run
agent-team team queue retry delivery --all --runtime codex --sort attempts --limit 10 --dry-run
```

Apply retries:

```sh
agent-team queue retry <id>
agent-team job queue retry squ-42 <id>
```

Drop explicitly:

```sh
agent-team queue drop <id> --dry-run
agent-team queue drop <id>
```

Batch drops default to dead-letter entries:

```sh
agent-team queue drop --all --state dead --dry-run
agent-team queue drop --all --runtime codex --dry-run
agent-team job queue drop squ-42 --all --runtime codex --dry-run
agent-team team queue drop delivery --all --runtime codex --dry-run
```

Age-prune old entries:

```sh
agent-team queue prune --older-than 24h --runtime codex --dry-run
agent-team job queue prune squ-42 --older-than 24h --runtime codex --dry-run
agent-team team queue prune delivery --older-than 24h --runtime codex --dry-run
```

## Queue Doctor

Use `queue doctor` when queue commands cannot parse active queue files.

```sh
agent-team queue doctor --commands
agent-team queue doctor --json
agent-team queue doctor --quarantine --dry-run
agent-team queue doctor --quarantine
```

Quarantine moves suspicious queue files out of active paths without deleting them.

## Quarantine

Quarantined queue files are preserved under:

```text
.agent_team/daemon/queue/quarantine/
```

List:

```sh
agent-team queue quarantine ls --summary --json
agent-team queue quarantine ls
agent-team queue quarantine ls --job SQU-42
agent-team queue quarantine ls --restorable
agent-team queue quarantine ls --unrestorable
agent-team queue quarantine ls --sort attempts --limit 10
agent-team queue quarantine ls --restorable --commands
```

Use `queue quarantine ls --summary` when automation only needs preserved-file
counts. Filters such as `--job`, `--restorable`, and `--unrestorable` narrow the
summary before counting. Use `queue quarantine ls --commands` when automation
needs restore/drop commands for the visible preserved rows without opening each
file first.

Inspect:

```sh
agent-team queue quarantine show quarantine/<timestamp>/pending/<id>.json
```

Restore:

```sh
agent-team queue quarantine restore <path> --dry-run
agent-team queue quarantine restore <path>
agent-team queue quarantine restore --all --job SQU-42 --sort attempts --limit 10 --dry-run
```

Drop:

```sh
agent-team queue quarantine drop <path> --dry-run
agent-team queue quarantine drop --all --unrestorable --sort modified --limit 10 --dry-run
```

## Job-Scoped Quarantine

When a durable job owns preserved files, prefer job-scoped commands:

```sh
agent-team job queue quarantine squ-42 --summary --json
agent-team job queue quarantine squ-42
agent-team job queue quarantine squ-42 --sort attempts --limit 10
agent-team job queue quarantine squ-42 --commands
agent-team job queue quarantine show squ-42 <path>
agent-team job queue quarantine restore squ-42 <path> --dry-run
agent-team job queue quarantine restore squ-42 --all --sort attempts --limit 10 --dry-run
agent-team job queue quarantine drop squ-42 <path> --dry-run
```

`job show` and `job triage` surface job-owned quarantined files and include scoped recovery actions.

Global `health` and `overview` also prefer job-scoped quarantine commands when every quarantined file resolves to one job.

## Pipeline-Scoped Queue Quarantine

When preserved queue files resolve to one workflow, use pipeline-scoped commands:

```sh
agent-team pipeline queue quarantine ticket_to_pr --summary --json
agent-team pipeline queue quarantine ticket_to_pr
agent-team pipeline queue quarantine ticket_to_pr --restorable --commands
agent-team pipeline queue quarantine ticket_to_pr --sort attempts --limit 10
agent-team pipeline queue quarantine show ticket_to_pr <path>
agent-team pipeline queue quarantine restore ticket_to_pr --all --job SQU-42 --dry-run
agent-team pipeline queue quarantine drop ticket_to_pr --all --unrestorable --dry-run
```

Use `pipeline queue quarantine --commands` without a pipeline name to print
restore/drop commands for visible files owned by any declared workflow.

## Active Outbox

Sandboxed agents write pending fallback events under `.agent_team/outbox/` when daemon transport is unavailable or delayed. Inspect the active outbox before draining or repairing it:

```sh
agent-team outbox ls
agent-team outbox ls --summary
agent-team outbox ls --state failed --commands
agent-team outbox watch --state pending
agent-team job outbox SQU-42 --state failed --commands
agent-team job outbox SQU-42 --watch --state failed
agent-team pipeline outbox ticket_to_pr --state failed --commands
agent-team pipeline outbox ticket_to_pr --watch --summary
agent-team team outbox delivery --state failed --commands
agent-team team outbox delivery --watch --job SQU-42
agent-team outbox drain --dry-run
agent-team outbox drain --dry-run --commands
```

Use `outbox watch` or a scoped `--watch` view while `tick`, `drain`, or `outbox drain` is expected to publish pending fallback events or move failed events after retry.
Use `--commands` on global, job, pipeline, or team outbox lists when scripts need the visible row recovery commands without the table.

## Outbox Quarantine

Sandboxed agents write fallback events under `.agent_team/outbox/` when daemon transport is unavailable. If active outbox files become unreadable, inspect and quarantine them first:

```sh
agent-team outbox doctor --commands
agent-team outbox doctor --json
agent-team outbox doctor --quarantine --dry-run
agent-team outbox doctor --quarantine
```

Preserved outbox files live under:

```text
.agent_team/outbox/quarantine/
```

Use global quarantine commands when triaging the whole repo:

```sh
agent-team outbox quarantine ls --summary --json
agent-team outbox quarantine ls --job SQU-42 --restorable
agent-team outbox quarantine ls --restorable --commands
agent-team outbox quarantine show quarantine/<timestamp>/failed/<id>.json
agent-team outbox quarantine restore <path> --dry-run
agent-team outbox quarantine drop --all --unrestorable --dry-run
```

Use `outbox quarantine ls --summary` for compact preserved outbox counts before
listing individual rows.

When one durable job owns the files, prefer the scoped command before restoring or dropping:

```sh
agent-team job outbox quarantine squ-42 --summary --json
agent-team job outbox quarantine squ-42
agent-team job outbox quarantine squ-42 --commands
agent-team job outbox quarantine show squ-42 <path>
agent-team job outbox quarantine restore squ-42 <path> --dry-run
agent-team job outbox quarantine restore squ-42 --all --state failed --dry-run
agent-team job outbox quarantine drop squ-42 <path> --dry-run
```

When a workflow owns the files, use pipeline-scoped recovery:

```sh
agent-team pipeline outbox quarantine ticket_to_pr --summary --json
agent-team pipeline outbox quarantine ticket_to_pr
agent-team pipeline outbox quarantine ticket_to_pr --job SQU-42 --restorable
agent-team pipeline outbox quarantine ticket_to_pr --restorable --commands
agent-team pipeline outbox quarantine show ticket_to_pr <path>
agent-team pipeline outbox quarantine restore ticket_to_pr <path> --dry-run
agent-team pipeline outbox quarantine restore ticket_to_pr --all --job SQU-42 --dry-run
agent-team pipeline outbox quarantine drop ticket_to_pr --all --unrestorable --dry-run
```

When a declared team owns the files, use team-scoped recovery:

```sh
agent-team team outbox quarantine delivery --summary --json
agent-team team outbox quarantine delivery
agent-team team outbox quarantine delivery --job SQU-42 --restorable
agent-team team outbox quarantine delivery --restorable --commands
agent-team team outbox quarantine show delivery <path>
agent-team team outbox quarantine restore delivery <path> --dry-run
agent-team team outbox quarantine restore delivery --all --job SQU-42 --dry-run
agent-team team outbox quarantine drop delivery --all --unrestorable --dry-run
```

Use `--commands` on global, job, pipeline, or team outbox quarantine lists when automation needs restore/drop commands for every visible preserved file without opening each file first.
Job, pipeline, and team scoping prevent a recovery command for one ticket, workflow, or ownership boundary from restoring or deleting another owner's preserved outbox file.

`health`, `overview`, and `next --source outbox --reason quarantined` surface preserved outbox files as operator actions. Global views prefer `job outbox quarantine <job-id>` when all preserved files resolve to one durable job, `pipeline outbox quarantine <pipeline>` when all preserved files resolve to one workflow, and team views use `team outbox quarantine <team>` while only counting that team's files.

Diagnostic snapshots include outbox quarantine inventory next to active outbox state. Use `snapshot`, `job snapshot <job-id>`, `pipeline snapshot <pipeline>`, or `team snapshot <team>` before and after recovery, and `snapshot diff --section outbox_quarantine` when a handoff only needs the preserved outbox-file delta.

## Team-Scoped Quarantine

For repos with teams:

```sh
agent-team team queue quarantine delivery --summary --json
agent-team team queue quarantine delivery
agent-team team queue quarantine delivery --restorable --sort attempts --limit 10
agent-team team queue quarantine delivery --restorable --commands
agent-team team queue quarantine show delivery <path>
agent-team team queue quarantine restore delivery --all --job SQU-42 --sort attempts --limit 10 --dry-run
agent-team team queue quarantine drop delivery --all --unrestorable --sort modified --limit 10 --dry-run
```

Team scoping prevents delivery recovery commands from mutating platform-owned queue files in the same repo.
Use `--commands` on global, job, pipeline, or team queue quarantine lists when automation needs restore/drop commands for every visible preserved file without opening each file first.

## Recovery Decision Tree

1. Run:

```sh
agent-team overview
```

2. If it reports dead queue items, preview scoped retries:

```sh
agent-team job queue retry squ-42 --all --sort attempts --limit 10 --dry-run
```

3. If it reports quarantined files, inspect first:

```sh
agent-team job queue quarantine squ-42
agent-team job queue quarantine show squ-42 <path>
```

4. Restore only restorable, validated files:

```sh
agent-team job queue quarantine restore squ-42 <path> --dry-run
agent-team job queue quarantine restore squ-42 <path>
```

5. Drop known-bad preserved files explicitly:

```sh
agent-team job queue quarantine drop squ-42 <path> --dry-run
agent-team job queue quarantine drop squ-42 <path>
```

## Code Areas

Queue behavior lives mostly in:

- `internal/daemon/queue.go`
- `internal/daemon/event.go`
- `internal/cli/queue.go`
- `internal/cli/queue_doctor.go`
- `internal/cli/queue_quarantine.go`
- `internal/cli/job.go`
- `internal/cli/team.go`
- `internal/cli/health.go`
- `internal/cli/overview.go`
