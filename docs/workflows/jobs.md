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
- status-file previews
- action hints
- recent events when requested

If a Codex one-shot captured `.agent_team/state/<instance>/last-message.txt`,
`job show` includes an action hint for `agent-team job logs <job-id> --last-message`.
If daemon metadata says a job-owned instance crashed, it also suggests
`agent-team job resume-plan <job-id> --status crashed`; add
`--unhealthy` for both crashed and stale recorded running PIDs, or
`--action resume`/`--action logs` to narrow the recovery path.

## Waiting For Jobs

```sh
agent-team job wait squ-42
agent-team job wait squ-42 --status running
agent-team job wait squ-42 --event adopted
agent-team job wait squ-42 --status done --event closed
```

Without flags, `job wait` waits for a terminal status: `done` or `failed`.
Use `--event` to wait for a specific last event such as `adopted`, `closed`,
or `pipeline_done`. When `--event` is set without `--status`, any lifecycle
status is accepted.

## Capturing Job Snapshots

Use `job snapshot` when one job needs a shareable post-mortem artifact:

```sh
agent-team job snapshot squ-42
agent-team job snapshot squ-42 --json
agent-team job snapshot squ-42 --output snapshots/squ-42.json
```

Snapshots include the durable job file, job audit events, daemon lifecycle rows,
queue ownership, quarantined queue files, runtime metadata, state-file status,
and paths for raw logs and Codex last-message sidecars. Log content is omitted
by default; add `--tail 100` to include the last 100 log lines in JSON output,
or `--tail -1` to include the full log.

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
agent-team job adopt squ-42 --instance worker-squ-42 --pid 12345
```

`job adopt` records daemon metadata and updates the job with the owning
instance. It defaults `--agent` to the job target, `--workspace` to the job
worktree, and branch/PR metadata to the existing job fields. Use top-level
`agent-team adopt <instance> --job <id>` for the same recovery path when you
are starting from an instance PID instead of a job; `runtime adopt` and
`daemon adopt` remain available from narrower namespaces.

## Blocking And Unblocking Jobs

```sh
agent-team job block squ-42 "Waiting on staging credentials"
agent-team job block squ-42 "Linear moved ticket to blocked" --actor linear
agent-team job block squ-42 --message-file blocker.md --dry-run --json
agent-team job unblock squ-42 "Credentials are configured; continue"
```

`job block` changes the lifecycle status to `blocked` and records an audit
event. Use `--actor` when automation records the block. Use `job hold` instead
when work should keep its lifecycle status but automation should stop advancing
it.

`job unblock` sends the supplied message to the owning instance and changes job
state from blocked back to running when appropriate.

## Sending Messages

```sh
agent-team job send squ-42 "Please continue with the new API constraint"
agent-team job send squ-42 --message-file notes.md
```

`job send` targets the current instance.

## Recording Notes

```sh
agent-team job note squ-42 "Waiting on staging credentials"
agent-team job note squ-42 "Linear status changed to blocked" --actor linear
agent-team job note squ-42 --message-file handoff.md
agent-team job note squ-42 "Will retry after deploy" --dry-run --json
```

`job note` appends an audit event and updates `last_event` / `last_status`
without sending anything to an instance. Use it for human handoffs, incident
context, or external decisions that should remain attached to the job. Use
`--actor` when automation records the note.

## Retrying Jobs

```sh
agent-team job retry squ-42 --dry-run --dispatch
agent-team job retry squ-42 --dispatch
```

For normal jobs this reopens the job and can dispatch a fresh attempt.

For pipeline jobs it resets the first failed step whose dependencies are satisfied, then advances work.

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
close.

## Cancelling Jobs

```sh
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
automation records the cancellation.

## Cleanup

Jobs can own branches and worktrees.

Preview cleanup:

```sh
agent-team job cleanup squ-42 --dry-run
agent-team job cleanup --all --dry-run
```

After confirming the PR is merged:

```sh
agent-team job cleanup squ-42 --merged
```

Add `--verify-pr` to check the recorded GitHub PR with `gh` before cleanup.
Use `--force-branch` only when the PR is merged but the local branch is not recognized as merged by Git.

## Triage

```sh
agent-team job triage
agent-team job triage --min-severity warning
agent-team job triage --reason queue_dead
agent-team job triage --reason queue_quarantined
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
- failed or blocked pipeline steps
- cleanup-ready terminal jobs
- ready pipeline steps
- status-file job update previews

Triage rows include action hints such as:

```sh
agent-team job retry squ-42 --dispatch
agent-team job queue retry squ-42 --all --sort attempts --limit 10
agent-team job queue quarantine squ-42
agent-team job queue quarantine restore squ-42 --all --sort attempts --limit 10 --dry-run
agent-team job unblock squ-42 <answer...>
agent-team job adopt squ-42 --pid <pid> --dry-run
agent-team job timeout squ-42 --dry-run
agent-team job cleanup squ-42 --dry-run
```

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
