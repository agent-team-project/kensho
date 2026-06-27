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
agent-team queue ls --summary --runtime codex
agent-team queue show <id>
```

Use `--runtime claude|codex` to narrow active queue entries by the runtime recorded in the dispatch payload, falling back to daemon metadata when a queued item already names a concrete instance. Runtime-filtered summaries include a `runtimes` count and exclude quarantined files whose runtime cannot be known from the quarantine index.

Job-scoped:

```sh
agent-team job queue squ-42
agent-team job queue squ-42 --summary
agent-team job queue squ-42 --runtime codex
agent-team job queue squ-42 --summary --runtime codex
agent-team job queue show squ-42 <id>
```

Team-scoped:

```sh
agent-team team queue delivery --state dead
agent-team team queue delivery --summary
agent-team team queue delivery --runtime codex
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
agent-team queue quarantine ls
agent-team queue quarantine ls --job SQU-42
agent-team queue quarantine ls --restorable
agent-team queue quarantine ls --unrestorable
agent-team queue quarantine ls --sort attempts --limit 10
```

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
agent-team job queue quarantine squ-42
agent-team job queue quarantine squ-42 --sort attempts --limit 10
agent-team job queue quarantine show squ-42 <path>
agent-team job queue quarantine restore squ-42 <path> --dry-run
agent-team job queue quarantine restore squ-42 --all --sort attempts --limit 10 --dry-run
agent-team job queue quarantine drop squ-42 <path> --dry-run
```

`job show` and `job triage` surface job-owned quarantined files and include scoped recovery actions.

Global `health` and `overview` also prefer job-scoped quarantine commands when every quarantined file resolves to one job.

## Outbox Quarantine

Sandboxed agents write fallback events under `.agent_team/outbox/` when daemon transport is unavailable. If active outbox files become unreadable, inspect and quarantine them first:

```sh
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
agent-team outbox quarantine ls --job SQU-42 --restorable
agent-team outbox quarantine show quarantine/<timestamp>/failed/<id>.json
agent-team outbox quarantine restore <path> --dry-run
agent-team outbox quarantine drop --all --unrestorable --dry-run
```

When one durable job owns the files, prefer the scoped command before restoring or dropping:

```sh
agent-team job outbox quarantine squ-42
agent-team job outbox quarantine show squ-42 <path>
agent-team job outbox quarantine restore squ-42 <path> --dry-run
agent-team job outbox quarantine restore squ-42 --all --state failed --dry-run
agent-team job outbox quarantine drop squ-42 <path> --dry-run
```

When a workflow owns the files, use pipeline-scoped recovery:

```sh
agent-team pipeline outbox quarantine ticket_to_pr
agent-team pipeline outbox quarantine ticket_to_pr --job SQU-42 --restorable
agent-team pipeline outbox quarantine show ticket_to_pr <path>
agent-team pipeline outbox quarantine restore ticket_to_pr <path> --dry-run
agent-team pipeline outbox quarantine restore ticket_to_pr --all --job SQU-42 --dry-run
agent-team pipeline outbox quarantine drop ticket_to_pr --all --unrestorable --dry-run
```

Job and pipeline scoping prevent a recovery command for one ticket or workflow from restoring or deleting another owner's preserved outbox file.

## Team-Scoped Quarantine

For repos with teams:

```sh
agent-team team queue quarantine delivery
agent-team team queue quarantine delivery --restorable --sort attempts --limit 10
agent-team team queue quarantine show delivery <path>
agent-team team queue quarantine restore delivery --all --job SQU-42 --sort attempts --limit 10 --dry-run
agent-team team queue quarantine drop delivery --all --unrestorable --sort modified --limit 10 --dry-run
```

Team scoping prevents delivery recovery commands from mutating platform-owned queue files in the same repo.

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
