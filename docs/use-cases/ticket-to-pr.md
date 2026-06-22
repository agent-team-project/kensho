# Use Case: Ticket to PR

This is the default software-delivery path.

## Goal

Given a ticket, create a durable job, dispatch a worker, let it operate in an isolated workspace, track branch/PR ownership, and clean up after merge.

## Setup

```sh
agent-team init \
  --set linear.team_id=00000000-0000-0000-0000-000000000000 \
  --set linear.ticket_prefix=SQU

agent-team daemon start
agent-team sync --wait
```

## Create the Job

```sh
agent-team job create SQU-42 \
  --target worker \
  --kickoff "Implement the ticket and open a PR" \
  --dispatch
```

The job stores:

- ticket
- target agent
- instance name
- status
- branch and worktree when dispatch resolves them
- PR URL when a worker reports or records it

## Monitor Progress

```sh
agent-team job show squ-42
agent-team logs worker-squ-42 --tail 100
agent-team monitor --jobs
```

The worker should report status through the status skill.

## Handle Blocked Work

If `job show` or `job triage` reports blocked status:

```sh
agent-team job unblock squ-42 "The missing token is configured; continue."
```

This sends a mailbox message and marks the durable job running.

## Retry

If dispatch or work fails:

```sh
agent-team job retry squ-42 --dry-run --dispatch
agent-team job retry squ-42 --dispatch
```

If the queue has a dead entry:

```sh
agent-team job queue retry squ-42 --all --dry-run
agent-team job queue retry squ-42 --all
```

## Close and Cleanup

Once the PR is merged:

```sh
agent-team job close squ-42 --status done
agent-team job cleanup squ-42 --dry-run
agent-team job cleanup squ-42 --merged
```

Cleanup removes only job-owned worktree/branch state.

## Success Criteria

- `job show` reports `done`.
- PR URL is recorded.
- Worktree and branch are cleaned after merge.
- `job triage --reason cleanup_ready` no longer reports the job.
- `overview` no longer shows the job as needing attention.
