# Use Case: On-call Recovery

This scenario assumes something is stuck and the operator does not yet know why.

## Start With Overview

```sh
agent-team overview
agent-team next
```

Overview provides state and action hints. `next` prints the shortest recommended command list.

## If the Daemon Is Down

```sh
agent-team daemon status
agent-team daemon start
agent-team daemon reconcile
agent-team health
```

`repair --dry-run` combines these checks:

```sh
agent-team repair --dry-run --jobs
```

## If a Job Is Blocked

```sh
agent-team job triage --reason blocked
agent-team job show squ-42 --events all
agent-team job unblock squ-42 "The dependency is available; continue."
```

## If Queue Entries Are Dead

```sh
agent-team job queue squ-42 --state dead
agent-team job queue retry squ-42 --all --dry-run
agent-team job queue retry squ-42 --all
```

If ownership is ambiguous:

```sh
agent-team queue ls --state dead
agent-team queue retry --all --dry-run
```

## If Pipeline Steps Failed

```sh
agent-team pipeline ready --state failed
agent-team repair --retry-pipelines --dry-run --preview-routes
agent-team repair --retry-pipelines --retry-message "retry after fixing credentials"
```

For one owned area, use `agent-team team repair <team> --retry-pipelines --dry-run --preview-routes`.

## If Queue Files Are Quarantined

```sh
agent-team job queue quarantine squ-42
agent-team job queue quarantine show squ-42 <path>
agent-team job queue quarantine restore squ-42 <path> --dry-run
agent-team job queue quarantine restore squ-42 <path>
```

Drop only after inspection:

```sh
agent-team job queue quarantine drop squ-42 <path> --dry-run
agent-team job queue quarantine drop squ-42 <path>
```

## If Intake Failed

```sh
agent-team intake summary
agent-team intake deliveries --unresolved
agent-team intake replay <delivery-id> --dry-run --preview-triggers
agent-team intake replay <delivery-id>
```

## If You Need a Handoff

```sh
agent-team snapshot --output diagnostics.json
```

For a team:

```sh
agent-team team snapshot delivery --output delivery-diagnostics.json
```

## Recovery Principles

- Prefer scoped commands when job or team ownership is known.
- Preview mutating actions.
- Do not drop queue or intake data until inspected.
- Use snapshots for handoff instead of asking the next operator to rediscover state.
