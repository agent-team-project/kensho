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
agent-team queue show <id>
```

Job-scoped:

```sh
agent-team job queue squ-42
agent-team job queue squ-42 --summary
```

Team-scoped:

```sh
agent-team team queue delivery --state dead
agent-team team queue delivery --summary
```

## Retry and Drop

Preview retries:

```sh
agent-team queue retry --all --dry-run
agent-team job queue retry squ-42 --all --dry-run
agent-team team queue retry delivery --all --job SQU-42 --dry-run
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
agent-team queue drop --all --state dead --older-than 24h --dry-run
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
```

Inspect:

```sh
agent-team queue quarantine show quarantine/<timestamp>/pending/<id>.json
```

Restore:

```sh
agent-team queue quarantine restore <path> --dry-run
agent-team queue quarantine restore <path>
agent-team queue quarantine restore --all --job SQU-42 --dry-run
```

Drop:

```sh
agent-team queue quarantine drop <path> --dry-run
agent-team queue quarantine drop --all --unrestorable --dry-run
```

## Job-Scoped Quarantine

When a durable job owns preserved files, prefer job-scoped commands:

```sh
agent-team job queue quarantine squ-42
agent-team job queue quarantine show squ-42 <path>
agent-team job queue quarantine restore squ-42 <path> --dry-run
agent-team job queue quarantine restore squ-42 --all --dry-run
agent-team job queue quarantine drop squ-42 <path> --dry-run
```

`job show` and `job triage` surface job-owned quarantined files and include scoped recovery actions.

Global `health` and `overview` also prefer job-scoped quarantine commands when every quarantined file resolves to one job.

## Team-Scoped Quarantine

For repos with teams:

```sh
agent-team team queue quarantine delivery
agent-team team queue quarantine delivery --restorable
agent-team team queue quarantine show delivery <path>
agent-team team queue quarantine restore delivery --all --job SQU-42 --dry-run
agent-team team queue quarantine drop delivery --all --unrestorable --dry-run
```

Team scoping prevents delivery recovery commands from mutating platform-owned queue files in the same repo.

## Recovery Decision Tree

1. Run:

```sh
agent-team overview
```

2. If it reports dead queue items, preview scoped retries:

```sh
agent-team job queue retry squ-42 --all --dry-run
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
