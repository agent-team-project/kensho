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
| `--instance <name>` | Request an instance name |
| `--dry-run` | Preview route and payload |
| `--json` | Emit structured result |

## Sending Messages

```sh
agent-team job send squ-42 "Please continue with the new API constraint"
agent-team job send squ-42 --message-file notes.md
agent-team job unblock squ-42 "Credentials are configured; continue"
```

`job send` targets the current instance.

`job unblock` also changes job state from blocked back to running when appropriate.

## Retrying Jobs

```sh
agent-team job retry squ-42 --dry-run --dispatch
agent-team job retry squ-42 --dispatch
```

For normal jobs this reopens the job and can dispatch a fresh attempt.

For pipeline jobs it resets the first failed step whose dependencies are satisfied, then advances work.

## Closing Jobs

```sh
agent-team job close squ-42 --status done
agent-team job close squ-42 --status failed
```

Closing records a job event and updates timestamps.

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
agent-team job queue retry squ-42 --all
agent-team job queue quarantine squ-42
agent-team job queue quarantine restore squ-42 --all --dry-run
agent-team job unblock squ-42 <answer...>
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
