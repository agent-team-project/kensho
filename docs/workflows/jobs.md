# Jobs

Jobs are durable work units.

They connect ticket tracking, dispatch, runtime ownership, queue recovery, worktrees, branches, PRs, and pipeline state.

## Why Jobs Exist

Instances answer "what process is running?"

Jobs answer:

- what work exists?
- which agent owns it?
- which instance is running it?
- which branch and PR belong to it?
- is it queued, running, blocked, done, or failed?
- what should happen next?

## Job Files

Jobs live at:

```text
.agent_team/jobs/<job-id>.toml
```

The default id is a normalized lowercase ticket slug:

```text
SQU-42 -> squ-42
https://linear.app/.../SQU-42/... -> squ-42
```

Job files store:

- id
- ticket and ticket URL
- target agent
- instance name
- lifecycle status
- branch
- worktree
- PR URL
- pipeline name
- step state
- last event
- last status
- timestamps

## Creating Jobs

```sh
agent-team job create SQU-42 \
  --target worker \
  --kickoff "Implement the requested API change"
```

Create and dispatch:

```sh
agent-team job create SQU-42 \
  --target worker \
  --kickoff "Implement the ticket" \
  --dispatch
```

Create, dispatch, and wait in scripts:

```sh
agent-team job create SQU-42 \
  --target worker \
  --kickoff "Implement the ticket" \
  --dispatch \
  --wait \
  --wait-status running \
  --wait-timeout 30s
```

Dispatch an existing job and wait:

```sh
agent-team job dispatch squ-42 --dry-run --commands
agent-team job dispatch squ-42 --wait --wait-status running --wait-timeout 30s
agent-team job dispatch squ-42 --wait --wait-timeout 30m --fail-on-failed
```

Advance a pipeline job step and wait:

```sh
agent-team job advance squ-42 --wait --wait-status running --wait-timeout 30s
agent-team job advance squ-42 --wait --wait-next-state running --wait-step implement --wait-timeout 30s
agent-team job advance squ-42 --dry-run --commands
agent-team job update squ-42 --pr https://github.com/acme/repo/pull/42 --advance --dry-run --commands
agent-team job update squ-42 --pr https://github.com/acme/repo/pull/42 \
  --advance \
  --wait \
  --wait-next-state running \
  --wait-step review \
  --wait-timeout 30s
agent-team job step squ-42 implement \
  --status done \
  --advance \
  --wait \
  --wait-next-state running \
  --wait-step review \
  --wait-timeout 30s
agent-team job approve squ-42 \
  --step review \
  --advance \
  --wait \
  --wait-next-state running \
  --wait-step review \
  --wait-timeout 30s
```

With `--wait`, create-and-dispatch, dispatch, advance, update-with-advance,
step-with-advance, approve-with-advance, retry-with-dispatch, and PR reconcile
handoffs wait for terminal status by default.
Add `--wait-status running`, `--wait-event dispatched`,
`--wait-event advance_dispatched`, or `--wait-event closed` when automation needs
a different handoff point. For pipeline jobs, add `--wait-next-state running`
with `--wait-step <id>` when the script should wait for a specific stage owner.

For a broader maintenance pass that may advance several ready jobs, use one-shot
`tick --wait`:

```sh
agent-team tick --wait --wait-status running --wait-timeout 30s
agent-team team tick delivery --wait --wait-status running --wait-timeout 30s
```

Preview first:

```sh
agent-team job create SQU-42 --target worker --kickoff "..." --dispatch --dry-run --json
```

## Showing Jobs

```sh
agent-team job show squ-42
agent-team job show squ-42 --events all
agent-team job show squ-42 --json
```

Human output includes:

- core job metadata
- pipeline steps
- active queue entries
- quarantined queue files owned by the job
- active and quarantined outbox events owned by the job
- status-file previews
- action hints
- recent events when requested

If a Codex one-shot captured `.agent_team/state/<instance>/last-message.txt`,
`job show` includes an action hint for `agent-team job logs <job-id> --last-message`.
If daemon metadata says a job-owned instance crashed, it also suggests
`agent-team job resume-plan <job-id> --status crashed`; add
`--unhealthy` for both crashed and stale recorded running PIDs, or
`--action resume`/`--action logs` to narrow the recovery path. Add
`--sort stale` or `--sort step` when a multi-stage job has several owned
runtimes and the recovery list needs a predictable order, and add `--limit N`
to cap rows after sorting.
Use `agent-team job ps <job-id>` when you need the lifecycle table for every
job-owned runtime before attaching, messaging, or stopping work; add
`--step <id>` for one pipeline stage. Use `agent-team job stats <job-id>` when
you need CPU and memory usage; add `--all` to include stopped or crashed
metadata, or `--summary --json` for scripts.

Use `job explain` when the question is what pipeline stage can run next, and
`job watch` when that readiness view should refresh continuously:

```sh
agent-team job explain squ-42
agent-team job explain squ-42 --state ready --step review
agent-team job watch squ-42 --state all
```

## Waiting For Jobs

```sh
agent-team job wait squ-42
agent-team job wait squ-42 --status running
agent-team job wait squ-42 --event adopted
agent-team job wait squ-42 --status done --event closed
agent-team job wait squ-42 --next-state ready --step implement --timeout 30s
```

Without flags, `job wait` waits for a terminal status: `done` or `failed`.
Use `--event` to wait for a specific last event such as `adopted`, `closed`,
or `pipeline_done`. When `--event` is set without `--status`, any lifecycle
status is accepted. Use `--next-state` with optional `--step` when automation
needs to block until a pipeline job is ready, blocked, held, done, or pointing
at a specific stage without dispatching it.

## Capturing Job Snapshots

Use `job snapshot` when one job needs a shareable post-mortem artifact:

```sh
agent-team job snapshot squ-42
agent-team job snapshot squ-42 --json
agent-team job snapshot squ-42 --output snapshots/squ-42.json
agent-team job snapshot squ-42 --no-redact --json
```

Snapshots include the durable job file, job audit events, daemon lifecycle rows,
inbox summaries for the job or step owner instances, queue ownership,
quarantined queue files, outbox ownership including quarantine, runtime metadata,
state-file status, and paths for raw logs and Codex last-message sidecars. Log
content is omitted by default; add
`--tail 100` to include the last 100 log lines in JSON output, or `--tail -1`
to include the full log. Queue payload secrets and latest inbox bodies are
redacted by default; use `--no-redact` only for local debugging.

## Dispatching Jobs

```sh
agent-team job dispatch squ-42
```

Dispatch publishes an event through topology resolution. It can create or target an instance based on topology and options.

Important options:

| Option | Meaning |
| --- | --- |
| `--workspace auto` | Let agent-team choose repo or worktree |
| `--workspace worktree` | Force worktree mode |
| `--runtime codex` | Use Codex for this dispatch instead of the repo/env default |
| `--runtime-bin <path>` | Use a specific runtime wrapper or binary for this dispatch |
| `--instance <name>` | Request an instance name |
| `--dry-run` | Preview route and payload |
| `--json` | Emit structured result |

## Adopting External Work

If a live runtime process was started outside the daemon but already belongs to
a durable job, adopt it from the job namespace:

```sh
agent-team job adopt squ-42 --instance worker-squ-42 --pid 12345 --dry-run --json
agent-team job adopt squ-42 --instance worker-squ-42 --pid-file worker.pid --dry-run --json
agent-team job adopt squ-42 --instance worker-squ-42 --pid 12345
```

`job adopt` records daemon metadata and updates the job with the owning
instance. It defaults `--agent` to the job target, `--workspace` to the job
worktree, and branch/PR metadata to the existing job fields. Use top-level
`agent-team adopt <instance> --job <id>` for the same recovery path when you
are starting from an instance PID instead of a job; `runtime adopt` and
`daemon adopt` remain available from narrower namespaces. When the job belongs
to a declared pipeline and you want that ownership checked before metadata is
written, use `agent-team pipeline adopt <pipeline> <job-id> --step <id>`.
Adoption text and JSON include follow-up actions for `inspect`, `logs`, and
`resume-plan`; job-owned adoption also includes the matching job-scoped
commands so scripts can continue from the durable work unit. Pipeline- and
team-scoped adoption add matching scoped status, logs, and resume-plan actions.
Add `--commands` when scripts need only those follow-up commands, one per line.

## Blocking And Unblocking Jobs

```sh
agent-team job block squ-42 "Waiting on staging credentials"
agent-team job block squ-42 "Linear moved ticket to blocked" --actor linear
agent-team job block squ-42 --message "Waiting on staging credentials" --dry-run --commands
agent-team job block squ-42 --message-file blocker.md --dry-run --json
agent-team job unblock squ-42 "Credentials are configured; continue" --dry-run --commands
agent-team job unblock squ-42 "Credentials are configured; continue"
```

`job block` changes the lifecycle status to `blocked` and records an audit
event. Use `--actor` when automation records the block. Use `job hold` instead
when work should keep its lifecycle status but automation should stop advancing
it. Add `--commands` to a dry-run when scripts should print only the matching
block apply command.

`job unblock` sends the supplied message to the owning instance and changes job
state from blocked back to running when appropriate. Add `--commands` to a
dry-run when scripts should print only the matching unblock apply command.

## Sending Messages

```sh
agent-team job send squ-42 "Please continue with the new API constraint" --dry-run --commands
agent-team job send squ-42 "Please continue with the new API constraint"
agent-team job send squ-42 --message-file notes.md
agent-team job ps squ-42 --runtime codex
agent-team job ps squ-42 --step review --status running
agent-team job stats squ-42 --runtime codex
agent-team job stats squ-42 --step review --all
```

`job send` targets the current instance. For pipeline jobs with distinct stage
owners, `job ps` and `job stats` show every job-owned runtime by default while
`--step` focuses one stage. Add `--commands` to a dry-run when scripts should
print only the matching send apply command.

## Recording Notes

```sh
agent-team job note squ-42 "Waiting on staging credentials"
agent-team job note squ-42 "Linear status changed to blocked" --actor linear
agent-team job note squ-42 --message-file handoff.md
agent-team job note squ-42 "Will retry after deploy" --dry-run --commands
agent-team job note squ-42 "Will retry after deploy" --dry-run --json
```

`job note` appends an audit event and updates `last_event` / `last_status`
without sending anything to an instance. Use it for human handoffs, incident
context, or external decisions that should remain attached to the job. Use
`--actor` when automation records the note. Add `--commands` to a dry-run when
scripts should print only the matching note apply command.

## Retrying Jobs

```sh
agent-team job retry squ-42 --dry-run --dispatch --commands
agent-team job retry squ-42 --dry-run --dispatch
agent-team job retry squ-42 --dispatch
agent-team job retry squ-42 --dispatch --wait --wait-status running --wait-timeout 30s
agent-team job retry squ-42 --dispatch --wait --wait-next-state running --wait-step implement --wait-timeout 30s
```

For normal jobs this reopens the job and can dispatch a fresh attempt.
`job reopen` is the canonical command and `job retry` is an alias; `--commands`
prints the same spelling used for the dry-run.

For pipeline jobs it resets the first failed step whose dependencies are
satisfied, then advances work. Add `--wait` when recovery automation should
block until the retried job reaches a lifecycle status or event.

## Timing Out Stale Jobs

```sh
agent-team job timeout squ-42 --dry-run
agent-team job timeout squ-42 --message "worker exceeded stage timeout"
agent-team job timeout squ-42 --step implement --dry-run
agent-team job timeout --all --dry-run
agent-team job timeout --all --pipeline ticket_to_pr --dry-run
agent-team job timeout --all --target-agent worker --limit 5 --dry-run
agent-team job timeout --all --limit 5 --message "maintenance timeout sweep"
```

Use `job timeout` after reconciling status when a running job or running
pipeline step is still stale. The command marks only stale running work failed:
pipeline steps use their step `timeout` first, then `[health].job_stale_after`;
step-less jobs use `[health].job_stale_after`. Use `--all` for a direct batch
sweep; add `--pipeline`, `--target-agent`, or `--limit` for bounded operator
passes. It does not stop a process.
Use `job stop`, `job kill`, or `job cancel --stop/--kill` when instance
lifecycle control should happen in the same operator pass.

## Closing Jobs

```sh
agent-team job close squ-42 --status done --dry-run --commands
agent-team job close squ-42 --status done --dry-run
agent-team job close squ-42 --status done
agent-team job close squ-42 --status failed
agent-team job close squ-42 --status done --actor github --message "PR merged"
agent-team job close squ-42 --status failed --message "superseded by SQU-43"
```

Use `--dry-run` to preview the terminal status and message before mutating the
job. Closing records a job event and updates timestamps. Add a positional
message, `--message`, or `--message-file <path|->` when the terminal state
needs an operator-readable reason. Use `--actor` when automation records the
close. Add `--commands` to a dry-run when scripts should print only the
matching close apply command.

## Cancelling Jobs

```sh
agent-team job cancel squ-42 "duplicate ticket" --dry-run --commands
agent-team job cancel squ-42 "duplicate ticket" --dry-run
agent-team job cancel squ-42 "duplicate ticket"
agent-team job cancel squ-42 --message "obsolete attempt" --stop --wait
agent-team job cancel squ-42 --message "superseded by Linear" --actor linear
agent-team job cancel squ-42 --message "hung worker" --kill --json
```

Cancellation records `last_event = "cancelled"`, marks the job failed, clears
any hold, and writes an audit event. By default it only changes the job file so
operators do not stop a live runtime accidentally. Add `--stop` or `--kill` when
the owning instance should be stopped in the same command; JSON output includes
both the cancelled job and the instance lifecycle action. Use `--actor` when
automation records the cancellation. Add `--commands` to a dry-run when scripts
should print only the matching cancel apply command.

## Cleanup

Jobs can own branches and worktrees. Applying cleanup only removes workspace
metadata for terminal jobs marked `done`; running, queued, blocked, or failed
jobs must be reconciled before their owned workspace is removed.

Preview cleanup:

```sh
agent-team job cleanup squ-42 --dry-run
agent-team job cleanup --all --dry-run
```

After the job is done and the PR is confirmed merged:

```sh
agent-team job cleanup squ-42 --merged
```

Use `job close squ-42 --status done` only when the work really is complete.
Add `--verify-pr` to check the recorded GitHub PR with `gh` before cleanup.
Use `--force-branch` only when the PR is merged but the local branch is not recognized as merged by Git.

Remove terminal job files and event logs only after workspace cleanup or when
the job record is no longer needed:

```sh
agent-team job rm squ-42 --dry-run --commands
agent-team job rm squ-42 --dry-run
agent-team job prune --status done --dry-run --commands
agent-team job prune --status done --dry-run
```

Use `--force` with `job rm` only when intentionally deleting a non-terminal job
record. Add `--commands` to removal and prune dry-runs when scripts should print
only the matching apply command.

## Triage

```sh
agent-team job triage
agent-team job triage --min-severity warning
agent-team job triage --reason queue_dead
agent-team job triage --reason queue_dead --commands
agent-team job triage --reason queue_quarantined
agent-team job triage --reason outbox_quarantined
agent-team job triage --json
```

Triage reports:

- failed jobs
- blocked jobs
- stale running jobs
- stale queued jobs
- running jobs without instances
- dead queue entries
- quarantined queue files
- quarantined outbox files
- failed or blocked pipeline steps
- cleanup-ready terminal jobs
- ready pipeline steps
- status-file job update previews

Triage rows include action hints such as:

```sh
agent-team job retry squ-42 --dispatch
agent-team job queue retry squ-42 --all --sort attempts --limit 10
agent-team job queue quarantine squ-42 --summary --json
agent-team job queue quarantine squ-42
agent-team job queue quarantine restore squ-42 --all --sort attempts --limit 10 --dry-run
agent-team job outbox quarantine squ-42 --summary --json
agent-team job outbox quarantine squ-42
agent-team job outbox quarantine restore squ-42 --all --sort path --limit 10 --dry-run
agent-team job unblock squ-42 <answer...>
agent-team job adopt squ-42 --pid <pid> --dry-run
agent-team job timeout squ-42 --dry-run
agent-team job cleanup squ-42 --dry-run
```

Add `--commands` when scripts need only attention-row recovery commands after
`--reason` or `--min-severity` filtering. Ready pipeline step commands are
available separately with `agent-team job ready --commands`.

## Job File Quarantine

Use `job doctor` when active job TOML files cannot be parsed or no longer match
their filename/id invariants:

```sh
agent-team job doctor --commands
agent-team job doctor --quarantine --dry-run
agent-team job doctor --quarantine
agent-team job quarantine --summary --json
agent-team job quarantine
agent-team job quarantine show quarantine/<timestamp>/squ-42.toml
agent-team job quarantine restore quarantine/<timestamp>/squ-42.toml --dry-run
agent-team job quarantine drop quarantine/<timestamp>/broken.toml --dry-run
```

`job quarantine --summary` reports compact preserved-file counts while
`--restorable` or `--unrestorable` narrows that count before inspection.

## Events

```sh
agent-team job events squ-42 --tail all
agent-team job events squ-42 --type dispatched
agent-team job events squ-42 --json
```

Events give an audit trail for:

- creation
- dispatch
- status transitions
- messages
- retries
- close
- cleanup

## Code Areas

Job behavior lives mostly in:

- `internal/job/job.go`
- `internal/job/events.go`
- `internal/job/reconcile.go`
- `internal/cli/job.go`
- `internal/cli/pipeline.go`
- `internal/cli/team.go`
