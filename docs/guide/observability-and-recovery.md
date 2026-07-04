# Observability and Recovery

The operating model is dry-run first: inspect state, preview the mutation, then
apply the smallest scoped command that owns the problem. The same file-backed
state powers human tables and JSON automation.

## Core Signals

Start with the broad views:

```sh
agent-team overview
agent-team next --commands
agent-team health --jobs
agent-team monitor --jobs --schedules
agent-team snapshot --output diagnostics.json
```

They combine daemon health, runtime metadata, status files, inbox counts, jobs,
queues, outbox, schedules, intake deliveries, pipeline status, and action hints.
Use `team overview <team>`, `pipeline status <pipeline>`, or `job show <job-id>`
when ownership is known.

## Usage Rollups

Finalized daemon-managed runtimes can persist token usage onto their durable
jobs. Inspect rollups with:

```sh
agent-team usage
agent-team usage --since 7d --by job
agent-team usage --by runtime --json
agent-team job show squ-42
agent-team job snapshot squ-42 --json
```

`usage` can group by `job`, `instance`, `agent`, or `runtime`. It reads captured
usage records from jobs and runtime metadata; it does not estimate costs or
invent missing records.

## Gates and Signatures

Agents record validation evidence as gate data:

```sh
agent-team job gate set squ-42 tests --status pass
agent-team job gate set squ-42 build \
  --status fail \
  --signature "ld: No space left on device" \
  --log-ref logs/build.txt
agent-team job gates squ-42 --json
```

Pipeline `infra_signatures` classify explicit failed gate signatures as
infrastructure failures when a regex matches. Unmatched failures are content
failures. The gate reporter still decides pass or fail; signatures only help
triage decide whether a failed gate should bounce content work or rerun after
an external problem clears.

Test signatures before relying on them:

```sh
agent-team signatures test ticket_to_pr --against logs/build.txt
```

The dry run reports match/no-match and includes matching excerpts, which makes
over-broad regexes visible before they affect real triage.

## Build Identity

`agent-team --version` prints the CLI build identity. `agent-team daemon status`
reports the daemon build identity when it can reach the daemon, and daemon HTTP
responses include build identity in error bodies. CLI requests also send a
build header; when client and daemon builds differ, the daemon logs the skew
once per client identity. Use this when an operator report might involve a
stale daemon binary:

```sh
agent-team --version
agent-team daemon status --json
agent-team daemon logs --tail 50
```

## Bounce, Extend, and Retry

Use `job bounce` when review findings should go back to the completed owner
step:

```sh
agent-team job bounce squ-42 --findings-file review.md --dry-run --commands
agent-team job bounce squ-42 --findings-file review.md --advance
```

The command appends a numbered `## Review findings (bounce N)` section to the
job kickoff, re-queues the target step, clears stale downstream ownership, and
records a `bounced` audit event. With `--advance`, it dispatches the re-queued
step immediately.

Use `job extend` or top-level `extend` only for a running instance with an armed
watchdog:

```sh
agent-team job extend squ-42 --by 30m --actor ops
agent-team extend worker-squ-42 --by 30m
```

Use retry commands for failed dispatch or failed pipeline steps:

```sh
agent-team job retry squ-42 --dry-run --dispatch
agent-team pipeline retry ticket_to_pr --dry-run --dispatch --preview-routes
agent-team repair --retry-pipelines --dry-run --preview-routes
```

`retry_on_crash = true` is a topology setting for one automatic retry after a
crash or nonzero exit, but only when the instance recorded no output or gate
verdict and the step has made one attempt. The bundled template uses it for the
read-only reviewer step. Keep it off for implementation steps that might have
already opened a PR.

## Landing and Cleanup

Pipeline merge config separates mechanical merge strategy from final PR landing
mode:

```toml
[pipelines.ticket_to_pr.merge]
strategy = "squash" # squash, rebase, or script
land = "merge"      # final gh pr merge mode: squash, merge, or rebase
```

Apply the declared merge action with:

```sh
agent-team job merge squ-42 --dry-run
agent-team job merge squ-42
agent-team job merge squ-42 --land rebase
agent-team job update squ-42 --land merge
```

`job merge` does not dispatch agents or rerun gates. Jobs with a recorded PR use
`gh pr merge` with the selected landing mode. Jobs without a recorded PR require
`--branch` for branch-local merge mechanics. Script strategy runs the configured
script and records a blocked merge if the script fails or leaves tracked files
dirty.

Cleanup is a separate explicit step:

```sh
agent-team job cleanup squ-42 --dry-run
agent-team job cleanup squ-42 --merged --verify-pr
```

`reap_worktree = "on_merge"` can opt worker-created job worktrees into cleanup
after merge; `agent-team job keep-worktree <job-id>` preserves one job.

## Recovery Map

| Symptom | Start with |
| --- | --- |
| Unsure what is wrong | `agent-team overview` |
| Need exact commands | `agent-team next --commands` |
| CI or monitor needs pass/fail | `agent-team health --jobs` |
| Review found content issues | `agent-team job bounce <job-id> --dry-run --commands` |
| Watchdog needs more time | `agent-team job extend <job-id> --by 30m` |
| Failed pipeline step | `agent-team pipeline retry <pipeline> --dry-run --dispatch --preview-routes` |
| Stale running work | `agent-team repair --timeout-jobs --dry-run` |
| Infra gate failure | Fix infra, then retry or rerun the affected gate |
| PR is ready to land | `agent-team job merge <job-id> --dry-run` |
| Done job still owns a worktree | `agent-team job cleanup <job-id> --dry-run` |

## References

- [Jobs](../workflows/jobs.md)
- [Pipelines and Teams](../workflows/pipelines-and-teams.md)
- [Diagnostics and Repair](../workflows/diagnostics-and-repair.md)
- [Runtime Instances](../runtime/instances.md)
- [CLI Reference](../reference/cli.md)
