# Pipelines and Teams

Pipelines and teams are the first layer above individual jobs.

Pipelines define multi-step work. Teams define ownership and scoping.

## Pipelines

Example:

```toml
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "triage"
target = "ticket-manager"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
after = ["triage"]

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
after = ["implement"]
timeout = "2h"
```

The current engine supports:

- step ids
- target instances/agents
- simple `after` dependencies
- optional steps that do not block downstream dependencies when they fail
- per-step stale thresholds with `timeout = "30m"`
- job-file step state
- ready-step inspection
- dry-run route previews
- manual-gate approval
- failed-step retry
- team-scoped advancement

It intentionally does not try to be a full DAG workflow engine yet.

## Pipeline Commands

```sh
agent-team pipeline ls
agent-team pipeline show ticket_to_pr
agent-team topology graph --format mermaid --routes
agent-team pipeline graph ticket_to_pr --format mermaid --routes
agent-team team graph delivery --format mermaid --routes
agent-team pipeline doctor --all
agent-team pipeline run ticket_to_pr SQU-42 --dry-run --dispatch
agent-team pipeline status
agent-team pipeline explain ticket_to_pr
agent-team pipeline explain ticket_to_pr --state failed
agent-team pipeline snapshot ticket_to_pr --output ticket-to-pr-diagnostics.json
agent-team pipeline next
agent-team pipeline next --team delivery
agent-team pipeline ready
agent-team pipeline hold ticket_to_pr "maintenance window"
agent-team pipeline release ticket_to_pr
agent-team pipeline advance ticket_to_pr --runtime codex --dry-run --preview-routes
agent-team pipeline advance ticket_to_pr --all-ready-steps --dry-run --preview-routes
agent-team pipeline approve ticket_to_pr --dry-run
agent-team pipeline approve ticket_to_pr --dispatch --dry-run --preview-routes
agent-team pipeline timeout ticket_to_pr --dry-run
agent-team pipeline retry ticket_to_pr --dry-run
agent-team pipeline retry ticket_to_pr --step review --dry-run
agent-team pipeline retry ticket_to_pr --dispatch --dry-run --preview-routes
agent-team repair --retry-pipelines --dry-run --preview-routes
agent-team repair --all-ready-steps --dry-run --preview-routes
agent-team repair --timeout-jobs --dry-run
agent-team repair --timeout-jobs --timeout-pipeline ticket_to_pr --dry-run
agent-team repair --timeout-jobs --timeout-target-agent worker --dry-run
agent-team repair --timeout-pipelines --dry-run
agent-team repair --timeout-pipelines --timeout-pipeline ticket_to_pr --dry-run
agent-team repair --retry-pipelines --retry-step review --dry-run --preview-routes
```

Job-level equivalents:

```sh
agent-team job next squ-42
agent-team job explain squ-42
agent-team job ready
agent-team job advance squ-42 --dry-run --preview-routes
agent-team job hold squ-42 "waiting for product signoff"
agent-team job release squ-42
agent-team job step squ-42 implement --advance --dry-run
agent-team job step squ-42 review --skip --message "review folded into implementation"
```

Use `topology graph --routes` for the full repo map, `pipeline graph` when you only need one workflow's dependency graph, and `team graph --routes` when you want the owned instances, schedules, pipelines, and dispatch routes in one read-only map.

## Step State

Pipeline step state is stored inside the job file.

Common states:

- `queued`
- `running`
- `blocked`
- `failed`
- `held`
- `done`
- `none`

`job triage`, `job explain`, `pipeline status`, `pipeline explain`, `pipeline next`, `pipeline ready`, `team explain`, and `team triage` all read the same job state. Use `job explain <job-id>` when you need one job's full step-by-step readiness view: it lists every step with dependencies, gates, waiting reasons, active instance ownership, and suggested next commands. Use `pipeline explain <pipeline>` for the same diagnostic view across every job in one workflow, or `team explain <team>` for the same view scoped to team-owned pipelines; add `--state failed`, `--state blocked`, `--state held`, `--limit N`, or `--json` for large histories and scripts. Use `pipeline snapshot <pipeline> --output <file>` when you need a shareable artifact for one workflow's status, explained jobs, owned jobs, job-owned queue/quarantine state, git context, and dry-run advance route previews. `pipeline status` includes `manual_gates` and recommends `pipeline approve` or the team-scoped equivalent when manual gates are ready. Failed, held, or blocked pipeline status also links to the matching explain view before the compact ready listing. `pipeline next` prints just those recommended commands with a short reason when you do not need the full status table; add `--team <team>` to render team-scoped commands for pipelines owned by one declared team.
When a whole job should stop advancing without changing its lifecycle status, use `agent-team job hold <job-id> [reason...]`. Add `--for 2h` or `--until 2026-06-24T18:00:00Z` when the pause has a known deadline. Use `agent-team job hold --all --dry-run` before a repo-wide incident freeze that should include non-pipeline jobs; add `--state`, `--limit`, `--for`, or `--until` to narrow the batch. Held jobs report next-step state `held`, are skipped by default ready and advance loops, and remain visible through `job ready --state held`, `pipeline explain --state held`, and `team explain --state held`. Use `agent-team job release <job-id> [message...]` to resume normal readiness checks; expired holds stay paused until released.
When an operator intentionally bypasses a stage, `agent-team job step <job-id> <step-id> --skip` records that step as `done` with `skipped = true`, so dependency checks can continue while `job show` still reports the bypass.
When a stage is best-effort, add `optional = true` to its pipeline step. If that step fails, downstream `after` dependencies can still advance; `job explain`, `pipeline explain`, and retry views keep the optional failure visible, and the job closes as done once all required steps finish.
When a step waits on a manual gate, `agent-team pipeline approve <pipeline>` marks approveable blocked manual gates queued so `pipeline advance`, `team advance`, or `tick` can dispatch them. Add `--step <id>` to approve one stage, add `--dispatch` to approve and dispatch in one command, and use `--dry-run --preview-routes` before batch approvals.
If an event-triggered pipeline starts with a manual or PR-gated step, the daemon creates the job and returns a `blocked` event outcome instead of spawning an agent. Use `job next`, `job explain`, or the scoped `pipeline ready --state blocked` view to see the approval or metadata action.
When a step fails, `agent-team pipeline retry <pipeline>` resets retryable failed steps to a blocked-but-ready state so the next `pipeline advance`, `team advance`, or `tick` can dispatch another attempt. Add `--step <id>` to target one failed stage, add `--dispatch` to retry and dispatch in one command, use `--dry-run --preview-routes` before a batch retry to inspect the resolved routes and payloads, and pass `--message` to record why the retry happened.
Pipeline and team-scoped dispatch commands accept `--runtime` and `--runtime-bin` for one-off Claude/Codex selection; the selected runtime is stored in the dispatch payload so queued or delayed starts keep the same intent.
Pipeline status also flags `stale_running_steps` when a running step has exceeded its step `timeout`, or the repo job stale threshold (`[health].job_stale_after`, default 24h) when no step timeout is declared. Start recovery with `agent-team job reconcile events --dry-run` so finished or crashed runtime metadata can update the job. If the step is still running after reconciliation, use `agent-team pipeline timeout <pipeline> --dry-run` to preview marking stale steps failed, then `pipeline retry` when another attempt should run. For broader maintenance, `agent-team repair --timeout-jobs --retry-pipelines --dry-run --preview-routes` previews stale job expiration and retry phases in one repair report; use `--timeout-pipelines` for pipeline-step-only expiration. Add `--timeout-pipeline` with either timeout mode when a repair sweep should stay inside one workflow, or `--timeout-target-agent` with `--timeout-jobs` when it should stay inside one agent role.
By default, `pipeline advance` dispatches one ready step per job. Use `pipeline advance <pipeline> --all-ready-steps` when a job has multiple currently ready independent steps and you want to fan them out in one command. Dependency checks still use the job file: a downstream step waits until all of its `after` steps are marked done, or failed with `optional = true`.
Use `agent-team team approve <team>` for the same manual-gate approval flow scoped to one team's declared pipelines.
Use `agent-team team retry <team>` for the same recovery flow scoped to one team's declared pipelines.
Use `agent-team repair --retry-pipelines` or `agent-team team repair <team> --retry-pipelines` when failed-step retry should happen inside the broader repair loop after daemon reconciliation and dead-letter queue retry. Add `--dry-run --preview-routes` first to inspect the dispatch routes, `--retry-step <id>` to target one failed stage, and `--retry-message` to record the operator reason.
Pipeline status, health, overview, and next-action hints recommend these retry dry-runs when failed steps are present, and include `pipeline explain ... --state failed` or `team explain ... --state failed` when the operator needs the detailed step diagnostics first.

Supported gates:

- `gate = "manual"`: wait for operator approval with `agent-team pipeline approve <pipeline>`, `agent-team team approve <team>`, or `agent-team job step <job-id> <step-id> --status queued`.
- `gate = "pr"`: wait until the job has PR metadata, then advance normally. Use `agent-team job update <job-id> --pr <url> --advance --dry-run` to preview the metadata update and next-step dispatch together before rerunning without `--dry-run`. GitHub PR webhooks can do the same via `agent-team intake github --reconcile-job --advance --dry-run` or `agent-team job reconcile github --advance --dry-run`.

When a ready step targets a persistent instance that is not currently running,
advancement writes the mailbox message and leaves the step `queued` with the
persistent instance name. Once that instance is started, it can drain its
mailbox and report progress. Steps are marked `running` only when dispatch
starts an ephemeral worker or messages a live persistent instance.

## Teams

Teams group resources:

```toml
[teams.delivery]
instances = ["manager", "ticket-manager", "worker"]
pipelines = ["ticket_to_pr"]
schedules = ["nightly"]
```

Team commands provide scoped variants:

```sh
agent-team team overview delivery
agent-team team jobs delivery
agent-team team triage delivery
agent-team team explain delivery
agent-team team explain delivery --state failed
agent-team team ready delivery
agent-team team hold delivery "maintenance window"
agent-team team release delivery
agent-team team advance delivery --dry-run --preview-routes
agent-team team advance delivery --all-ready-steps --dry-run --preview-routes
agent-team team approve delivery --dispatch --dry-run --preview-routes
agent-team team retry delivery --dispatch --dry-run --preview-routes
agent-team team retry delivery --step review --dry-run
agent-team team timeout delivery --dry-run
agent-team team timeout delivery --jobs --dry-run
agent-team team tick delivery --dry-run
agent-team team tick delivery --all-ready-steps --dry-run
agent-team team repair delivery --dry-run --jobs
agent-team team repair delivery --all-ready-steps --dry-run --preview-routes
agent-team team repair delivery --timeout-jobs --dry-run
agent-team team repair delivery --timeout-jobs --timeout-pipeline ticket_to_pr --dry-run
agent-team team repair delivery --timeout-jobs --timeout-target-agent worker --dry-run
agent-team team repair delivery --timeout-pipelines --dry-run
agent-team team repair delivery --timeout-pipelines --timeout-pipeline ticket_to_pr --dry-run
agent-team team repair delivery --retry-pipelines --dry-run --preview-routes
agent-team team repair delivery --retry-pipelines --retry-step review --dry-run --preview-routes
agent-team team drain delivery --all-ready-steps
agent-team team snapshot delivery --output delivery.json
```

`team advance <team> --all-ready-steps` applies the same parallel-ready fan-out as `pipeline advance --all-ready-steps`, but only for pipelines declared on that team. Use it when one team owns a job with independent stages that can run at the same time.
Use `team timeout <team> --dry-run` for the same stale running-step expiration flow as `pipeline timeout`, scoped to the pipelines declared on that team. Add `--jobs` when the same direct sweep should also catch stale step-less jobs whose target instance belongs to the team. Use `team repair <team> --timeout-jobs --dry-run` when the timeout should run inside the broader repair loop; add `--timeout-pipeline` with either timeout mode to keep repair inside one team-owned workflow, or `--timeout-target-agent` with `--timeout-jobs` for one team-owned agent role.
`tick --all-ready-steps`, `repair --all-ready-steps`, `team tick <team> --all-ready-steps`, `team repair <team> --all-ready-steps`, and `team drain <team> --all-ready-steps` apply that fan-out during maintenance and recovery cycles, including watch and until-idle loops.
Use `pipeline hold <pipeline>` or `team hold <team>` for scoped maintenance windows and incident freezes. Use `job hold --all --dry-run` when the freeze should span multiple pipelines or include non-pipeline jobs. These commands hold matching jobs in bulk without changing lifecycle status; add `--state failed`, `--state ready`, `--limit N`, `--for 2h`, or `--until 2026-06-24T18:00:00Z` to narrow or time-box the batch, and run with `--dry-run` first. `pipeline release` and `team release` resume held jobs in the same scope; add `--expired` to release only jobs whose `hold_until` has passed. Use `job ls --expired-hold`, `pipeline jobs <pipeline> --expired-hold`, or `team jobs <team> --expired-hold` to audit elapsed holds before release; `overview`, `next --reason expired_holds`, and team-scoped overview/next views also recommend the matching expired-release dry-run. Use `job release --all --expired --dry-run` when elapsed holds may span multiple pipelines or include non-pipeline jobs.

## Why Teams Matter

In a repo with several product areas, broad recovery commands can be too coarse.

Teams let operators say:

- only delivery-owned jobs
- only delivery-owned queues
- only delivery-owned schedules
- only delivery-owned pipelines
- only delivery-owned instance state

This makes diagnostics safer and less noisy.

## Team Health

```sh
agent-team team health delivery --jobs
agent-team team status delivery
agent-team team monitor delivery --jobs --schedules
```

Team health includes:

- daemon readiness
- team-owned queue dead letters
- team-owned queue quarantine
- job attention
- pipeline failures
- schedule state
- team topology warnings

## Development Guidelines

When adding pipeline or team features:

1. Preserve job-file ownership of step state.
2. Add dry-run behavior before mutating commands.
3. Add team-scoped tests whenever global behavior is changed.
4. Ensure global health/overview and team health/overview stay consistent.
5. Avoid adding a complex workflow engine until the simple step model proves insufficient.

## Code Areas

- `internal/topology/topology.go`
- `internal/cli/pipeline.go`
- `internal/cli/job.go`
- `internal/cli/team.go`
- `internal/cli/tick.go`
- `internal/cli/repair.go`
