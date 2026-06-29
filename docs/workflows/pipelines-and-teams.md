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
label = "Triage"
target = "ticket-manager"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
label = "Implementation"
instructions = "Implement the ticket with tests and summarize the branch state."
target = "worker"
workspace = "worktree"
runtime = "codex"
after = ["triage"]

[[pipelines.ticket_to_pr.steps]]
id = "review"
label = "Review"
description = "Check implementation quality and prepare PR follow-up."
target = "manager"
workspace = "repo"
runtime = "claude"
after = ["implement"]
timeout = "2h"
max_attempts = 2
```

The current engine supports:

- step ids
- human-readable step labels and descriptions
- step-specific runtime instructions
- target instances/agents
- step-level workspace selection
- step-level runtime selection
- simple `after` dependencies
- optional steps that do not block downstream dependencies when they fail
- per-step stale thresholds with `timeout = "30m"`
- per-step retry caps with `max_attempts = 2`
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
agent-team pipeline inspect ticket_to_pr
agent-team topology graph --format mermaid --routes
agent-team pipeline graph ticket_to_pr --format mermaid --routes
agent-team team graph delivery --format mermaid --routes
agent-team pipeline doctor --all
agent-team pipeline doctor --all --commands
agent-team pipeline run ticket_to_pr SQU-42 --dry-run --dispatch
agent-team pipeline run ticket_to_pr SQU-42 --dry-run --dispatch --commands
agent-team pipeline run ticket_to_pr SQU-42 --dispatch --wait --wait-status running --wait-timeout 30s
agent-team pipeline run ticket_to_pr SQU-42 --dispatch --wait --wait-next-state running --wait-step implement --wait-timeout 30s
agent-team pipeline status
agent-team pipeline watch ticket_to_pr
agent-team pipeline explain ticket_to_pr
agent-team pipeline explain ticket_to_pr --state failed
agent-team pipeline resume-plan --unhealthy --sort stale --limit 10
agent-team pipeline resume-plan --unhealthy --sort stale --limit 10 --commands
agent-team pipeline resume-plan ticket_to_pr --status crashed --runtime codex
agent-team pipeline adopt ticket_to_pr squ-42 --step review --pid 12345 --dry-run --json
agent-team pipeline send ticket_to_pr --dry-run --commands "please checkpoint current status"
agent-team pipeline ps --runtime codex
agent-team pipeline ps ticket_to_pr --status running
agent-team pipeline stats --runtime codex --summary
agent-team pipeline stats ticket_to_pr --runtime codex
agent-team pipeline logs --runtime codex --last-message
agent-team pipeline logs ticket_to_pr --runtime codex --last-message
agent-team pipeline events --runtime codex --tail 20
agent-team pipeline events ticket_to_pr --runtime codex --tail 20
agent-team pipeline cleanup ticket_to_pr --dry-run
agent-team pipeline queue --state dead --summary
agent-team pipeline queue ticket_to_pr --state dead --summary
agent-team pipeline queue ticket_to_pr --state dead --commands
agent-team pipeline queue retry ticket_to_pr --all --sort attempts --limit 10 --dry-run
agent-team pipeline queue quarantine --summary --json
agent-team pipeline queue quarantine --restorable
agent-team pipeline queue quarantine ticket_to_pr --restorable
agent-team pipeline snapshot ticket_to_pr --output ticket-to-pr-diagnostics.json
agent-team pipeline triage ticket_to_pr --reason queue_dead --commands
agent-team pipeline next
agent-team pipeline next --sort queue --limit 5
agent-team pipeline next ticket_to_pr --reason failed_steps
agent-team pipeline next ticket_to_pr --reason failed_steps --commands
agent-team pipeline next --team delivery
agent-team pipeline wait ticket_to_pr --status done --fail-on-failed --timeout 30m
agent-team pipeline wait ticket_to_pr --next-state ready --step review --timeout 30s
agent-team pipeline ready
agent-team pipeline ready ticket_to_pr --commands
agent-team pipeline hold ticket_to_pr "maintenance window"
agent-team pipeline release ticket_to_pr
agent-team pipeline advance ticket_to_pr --runtime codex --dry-run --preview-routes
agent-team pipeline advance ticket_to_pr --all-ready-steps --dry-run --preview-routes
agent-team pipeline advance ticket_to_pr --wait --wait-status running --wait-timeout 30s
agent-team pipeline advance ticket_to_pr --wait --wait-next-state ready --wait-step review --wait-timeout 30s
agent-team pipeline approve ticket_to_pr --dry-run
agent-team pipeline approve ticket_to_pr --dispatch --dry-run --preview-routes
agent-team pipeline approve ticket_to_pr --dispatch --wait --wait-status running --wait-timeout 30s
agent-team pipeline approve ticket_to_pr --dispatch --wait --wait-next-state running --wait-step review --wait-timeout 30s
agent-team pipeline timeout ticket_to_pr --dry-run
agent-team pipeline timeout ticket_to_pr --target-agent worker --dry-run
agent-team pipeline retry ticket_to_pr --dry-run
agent-team pipeline retry ticket_to_pr --step review --dry-run
agent-team pipeline retry ticket_to_pr --dispatch --dry-run --preview-routes
agent-team pipeline retry ticket_to_pr --dispatch --wait --wait-status running --wait-timeout 30s
agent-team pipeline retry ticket_to_pr --dispatch --wait --wait-next-state running --wait-step implement --wait-timeout 30s
agent-team pipeline retry ticket_to_pr --force --message "retry after fixing credentials"
agent-team pipeline tick ticket_to_pr --dry-run --preview-routes --commands --runtime codex
agent-team pipeline tick ticket_to_pr --wait --wait-status running --wait-timeout 30s
agent-team pipeline tick ticket_to_pr --wait --wait-next-state running --wait-step implement --wait-timeout 30s
agent-team pipeline repair ticket_to_pr --dry-run --preview-routes
agent-team pipeline repair ticket_to_pr --retry-pipelines --dry-run --preview-routes
agent-team pipeline repair ticket_to_pr --retry-pipelines --wait --wait-status running --wait-timeout 30s
agent-team pipeline repair ticket_to_pr --retry-pipelines --wait --wait-next-state running --wait-step implement --wait-timeout 30s
agent-team pipeline drain ticket_to_pr --runtime codex
agent-team pipeline drain ticket_to_pr --wait --wait-status running --wait-timeout 30s
agent-team pipeline drain ticket_to_pr --wait --wait-next-state running --wait-step implement --wait-timeout 30s
agent-team repair --retry-pipelines --dry-run --preview-routes
agent-team repair --retry-pipelines --wait --wait-status running --wait-timeout 30s
agent-team repair --retry-pipelines --wait --wait-next-state running --wait-step implement --wait-timeout 30s
agent-team repair --all-ready-steps --dry-run --preview-routes
agent-team drain --all-ready-steps --runtime codex
agent-team drain --wait --wait-status running --wait-timeout 30s
agent-team drain --wait --wait-next-state running --wait-step implement --wait-timeout 30s
agent-team repair --timeout-jobs --dry-run
agent-team repair --timeout-jobs --timeout-pipeline ticket_to_pr --dry-run
agent-team repair --timeout-jobs --timeout-target-agent worker --dry-run
agent-team repair --timeout-pipelines --dry-run
agent-team repair --timeout-pipelines --timeout-pipeline ticket_to_pr --dry-run
agent-team repair --timeout-pipelines --timeout-target-agent worker --dry-run
agent-team repair --retry-pipelines --retry-pipeline ticket_to_pr --runtime codex --dry-run --preview-routes
agent-team repair --retry-pipelines --retry-step review --dry-run --preview-routes
agent-team tick --dry-run --preview-routes --commands
agent-team tick --wait --wait-status running --wait-timeout 30s
agent-team tick --wait --wait-next-state running --wait-step implement --wait-timeout 30s
agent-team team tick delivery --dry-run --preview-routes --commands
agent-team team tick delivery --wait --wait-status running --wait-timeout 30s
agent-team team tick delivery --wait --wait-next-state running --wait-step implement --wait-timeout 30s
```

Job-level equivalents:

```sh
agent-team job next squ-42
agent-team job next squ-42 --state ready --step implement
agent-team job next squ-42 --state ready --commands
agent-team job wait squ-42 --next-state ready --step implement --timeout 30s
agent-team job explain squ-42
agent-team job ready
agent-team job ready --commands
agent-team job advance squ-42 --dry-run --json
agent-team job advance squ-42 --wait --wait-status running --wait-timeout 30s
agent-team job advance squ-42 --wait --wait-next-state running --wait-step implement --wait-timeout 30s
agent-team job hold squ-42 "waiting for product signoff"
agent-team job release squ-42
agent-team job step squ-42 implement --advance --dry-run
agent-team job step squ-42 implement --status done --advance --wait --wait-next-state running --wait-step review --wait-timeout 30s
agent-team job approve squ-42 --step review --advance --wait --wait-next-state running --wait-step review --wait-timeout 30s
agent-team job update squ-42 --pr https://github.com/acme/repo/pull/42 --advance --dry-run --commands
agent-team job update squ-42 --pr https://github.com/acme/repo/pull/42 --advance --wait --wait-next-state running --wait-step review --wait-timeout 30s
agent-team job step squ-42 review --skip --message "review folded into implementation"
agent-team pipeline skip ticket_to_pr --step review --dry-run
agent-team team skip delivery --step review --dry-run
agent-team pipeline cancel ticket_to_pr --message "duplicate ticket" --dry-run
agent-team pipeline send ticket_to_pr --dry-run "please checkpoint current status"
agent-team team cancel delivery --message "superseded" --dry-run
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

`job triage`, `job explain`, `pipeline status`, `pipeline explain`, `pipeline next`, `pipeline ready`, `team explain`, and `team triage` all read the same job state. Use `job explain <job-id>` when you need one job's full step-by-step readiness view, or `job watch <job-id>` when that view should refresh continuously: it lists every step with dependencies, gates, waiting reasons, active instance ownership, and suggested next commands; add `--state ready|blocked|failed`, `--step <id>`, or both to focus one state or stage without losing the job-level next-state summary. Use `job next <job-id> --commands` when scripts need only the selected single-job next-action commands. Use `pipeline explain <pipeline>` for the same diagnostic view across every job in one workflow, or `team explain <team>` for the same view scoped to team-owned pipelines; add `--state failed`, `--state blocked`, `--state held`, `--step <id>`, `--sort state|updated|target`, `--limit N`, or `--json` for large histories and scripts. Use `job triage --commands`, `pipeline triage <pipeline> --commands`, or `team triage <team> --commands` when scripts need only attention-row recovery commands after severity or reason filtering. Use `pipeline resume-plan [<pipeline>]` when you need runtime start, attach, direct resume, or log fallback commands for pipeline-owned daemon metadata; omit the pipeline or pass `--all` to inspect every workflow-owned runtime while excluding ad hoc jobs, or pass one pipeline to stay inside a workflow. Add `--unhealthy` for both crashed and stale recorded running PIDs, `--runtime-stale` when you only want stale running metadata, `--managed` for runtimes with daemon-managed resume support, `--can-managed` for rows with enough session metadata for managed restart, `--direct` for direct runtime resume commands, `--sort stale|step|runtime|status` before rendering when scripts need deterministic recovery groups, `--limit N` to cap rows after sorting, or `--commands` when scripts need only one recommended command per line. `--stale` remains a compatibility alias on resume-plan commands. If a live runtime was started outside the daemon, `pipeline adopt <pipeline> <job-id>` adopts it only after verifying the durable job belongs to that pipeline; add `--step <id>` for a specific stage. Use `pipeline run <pipeline> <ticket> --dispatch --wait` or `team run <team> <ticket> --dispatch --wait` when a script should create a job, dispatch its first ready step, and block until the created job reaches a terminal status; add `--wait-status running`, `--wait-event advance_dispatched`, `--wait-next-state running --wait-step implement`, `--wait-timeout`, or `--fail-on-failed` for CI handoff points. Use `pipeline approve <pipeline> --dispatch --wait` or `team approve <team> --dispatch --wait` when an operator approval should immediately hand off to a runtime and block until the approved step starts, reaches another requested status/event, or matches a stage filter such as `--wait-next-state running --wait-step review`. Use `pipeline wait [<pipeline>]` when a script should block until already-created pipeline jobs reach `done`, `failed`, a specific `--status`, a matching `--event`, or a next stage such as `--next-state ready --step review`; omit the pipeline or pass `--all` to wait across all pipeline-owned jobs, add `--job <id>` to wait for one job, and add `--fail-on-failed` when failed terminal jobs should return exit 1. Use `job ready`, `pipeline ready`, or `team ready` when you want the compact row view; add `--step <id>` to focus on one stage, `--sort updated`, `--sort state`, or `--sort target` to reshape large operator queues, `--limit N` to cap the result after sorting, or `--commands` when scripts need one action command per ready row. Use `pipeline snapshot <pipeline> --output <file>` when you need a shareable artifact for one workflow's status, explained jobs, owned jobs, bounded audit/lifecycle timeline, inbox summaries for job or step owners, job-owned queue/outbox quarantine state, git context, and dry-run advance route previews. Add `--timeline all` or `--timeline N` to control how much timeline history is captured. Latest inbox bodies are redacted unless `--no-redact` is used. `pipeline status` includes `manual_gates`, pipeline-owned queue/outbox counts, queue/outbox quarantine counts, and scoped recovery actions; use `pipeline watch [<pipeline>]` as the continuous shortcut for the same table. Sort with `--sort quarantined` when any quarantine should come first, or use `--sort queue-quarantined` / `--sort outbox-quarantined` when scripts need one side. It also recommends `pipeline approve` or the team-scoped equivalent when manual gates are ready. Failed, held, or blocked pipeline status also links to the matching explain view before the compact ready listing. `pipeline next` prints just those recommended commands with a short reason when you do not need the full status table; add `--reason failed_steps`, `--reason ready_steps`, `--reason quarantined`, or another displayed reason prefix to focus automation, add `--sort queue` or any pipeline status sort to choose which workflows contribute actions first, add `--team <team>` to render team-scoped commands for pipelines owned by one declared team, and add `--commands` when scripts need only one selected command per line.
Pipeline and team resume-plan rows keep the owning job and step in their
recommended attach/log follow-ups when that context is known, so generated
commands such as `agent-team job logs <job> --step <id> --follow` stay tied to
the exact failed or stale stage. Add `--last-message` when Codex log fallbacks
should use `agent-team job logs <job> --step <id> --last-message` or the
matching instance-level sidecar command instead.
When these job, pipeline, or team diagnostic views are run with an explicit `--repo`, their `--commands` output preserves that repo selector in emitted `agent-team` follow-ups so scripts can be launched from outside the target checkout.
Use `team wait-jobs <team>` when a script should wait for team-owned jobs to reach a lifecycle status, event, or next stage such as `--next-state ready --step review`; use `team wait <team>` when the condition is about team-owned instance lifecycle instead.

Ready-work hints in `overview`, `next`, `pipeline status`, `pipeline ready`, `pipeline explain`, and team-scoped views prefer tick previews because one tick handles both queued dispatches and ready-step advancement inside the selected scope. Use `pipeline advance <pipeline> --wait` or `team advance <team> --wait` when already-created ready work should dispatch and block until the advanced step starts, reaches another requested status/event, or exposes a next stage such as `--wait-next-state ready --wait-step review` without schedule or queue draining. Use `tick --wait --wait-status running` when a single maintenance cycle should reconcile, drain, fire schedules, advance ready pipeline jobs, and then wait for the jobs it advanced to have live owners; add `--wait-next-state running --wait-step implement` when the handoff must match one stage. Use `pipeline tick <pipeline> --dry-run --preview-routes --commands` when that one-cycle queue and ready-step preview should stay inside one workflow and scripts need the matching apply command, or add `--wait --wait-next-state running --wait-step implement` for a scoped stage-aware one-shot handoff. Use `drain --wait --wait-next-state running --wait-step implement` when a script should keep cycling until idle, then wait for jobs advanced during those drain cycles to have a specific live stage owner; use `pipeline drain <pipeline> --wait --wait-next-state running --wait-step implement` for the same finite handoff inside one workflow. Use the job-level equivalents `job update <job-id> --advance --wait`, `job step <job-id> <step-id> --advance --wait`, and `job approve <job-id> --advance --wait` when a single job should unblock a PR gate, completed step, or manual gate and then wait for the next owner; add `--wait-next-state running --wait-step <id>` when that owner must be a specific stage.
`overview` and `next --source runtime` prefer `pipeline resume-plan <pipeline>` or `pipeline resume-plan --all` when every crashed or runtime-stale instance maps to pipeline-owned jobs; mixed ad hoc/runtime-only metadata still uses the broader `resume-plan` fallback.
Use optional `label` and `description` fields when step ids need to stay short for commands but the operator view needs clearer names. Add `instructions` when the target agent needs step-specific guidance beyond the job kickoff; dispatch payloads append those instructions under a step heading while preserving the original job kickoff. Add `workspace = "repo"` or `workspace = "worktree"` when a stage should default to a specific dispatch workspace; command-line `--workspace repo|worktree` on advance, retry, approve, tick, repair, or run still overrides the step default. Add `runtime = "codex"` or `runtime = "claude"` when a stage should default to a specific LLM runtime; optionally add `runtime_bin = "codex-dev"` or another wrapper binary. Command-line `--runtime` and `--runtime-bin` override step runtime defaults. The metadata is copied into each durable job step, so `job show`/`job inspect`, `job explain`, `job ready --json`, `pipeline show`/`pipeline inspect`, and graph views remain understandable even if the topology is edited later.
When a whole job should stop advancing without changing its lifecycle status, use `agent-team job hold <job-id> [reason...]`. Add `--for 2h` or `--until 2026-06-24T18:00:00Z` when the pause has a known deadline. Use `agent-team job hold --all --dry-run` before a repo-wide incident freeze that should include non-pipeline jobs; add `--state`, `--limit`, `--for`, or `--until` to narrow the batch. Held jobs report next-step state `held`, are skipped by default ready and advance loops, and remain visible through `job ready --state held`, `pipeline explain --state held`, and `team explain --state held`. Use `agent-team job release <job-id> [message...]` to resume normal readiness checks; expired holds stay paused until released.
When an operator intentionally bypasses a stage, `agent-team job step <job-id> <step-id> --skip` records that step as `done` with `skipped = true`, so dependency checks can continue while `job show` still reports the bypass. Use `agent-team pipeline skip <pipeline> --step <id> --dry-run` for a scoped batch preview, or `agent-team team skip <team> --step <id> --dry-run` when the action should stay inside one team's declared pipelines. Batch skip requires `--step` and refuses to mutate running steps; time out or stop active work first when a live owner exists.
When a set of pipeline jobs is obsolete, use `agent-team pipeline cancel <pipeline> --message "..." --dry-run` to preview marking every non-terminal job failed with a `cancelled` audit event. Use `agent-team team cancel <team> --message "..." --dry-run` to keep that cancellation inside one team's declared pipelines. Batch cancellation only updates durable job files and skips terminal jobs; use `agent-team job cancel <job-id> --stop` or `--kill` when an owning instance should be stopped in the same action.
Use `agent-team pipeline send <pipeline> --dry-run "..."` when every running instance owned by jobs in one workflow should receive the same operator instruction, or `agent-team team send <team> --dry-run "..."` when the recipient set should stay inside one declared team. Add `--commands` when scripts should print only the matching scoped send apply command for actionable recipients. Add `--runtime codex`, `--runtime-stale`, `--unhealthy`, `--latest`, `--last N`, or `--all` to narrow or widen recipients before sending. Use `agent-team pipeline ps [<pipeline>]` for the lifecycle table of pipeline-owned runtimes across all workflows by default, or pass one pipeline to stay inside a workflow; add `--runtime codex`, `--status running`, `--quiet`, `--summary`, or `--watch` for scripting and live monitoring. Use `agent-team pipeline stats [<pipeline>]` to inspect CPU and memory usage for pipeline-owned runtimes across all workflows by default, or pass one pipeline to stay inside a workflow; add `--runtime codex`, `--status crashed`, `--phase blocked`, `--unhealthy`, `--summary`, or `--watch` when monitoring busy or degraded stages. Use `agent-team pipeline logs [<pipeline>]` to read pipeline-owned runtime logs across all workflows by default, or pass one pipeline to stay inside a workflow; add `--job <id>` to focus one work unit, `--step <id>` to focus one stage, `--last-message` for clean Codex final responses, or `--list --json` when scripts need log paths and job/step ownership. Use `agent-team team logs <team> --job <id> --step <id>` for the same work-unit and stage-scoped log view inside one declared team. Use `agent-team pipeline events [<pipeline>]` for lifecycle history across all pipeline-owned workflows by default, or pass one pipeline for workflow scope; add `--job <id>`, `--step <id>`, `--summary`, `--action`, `--runtime`, or `--tail` when diagnosing one work unit, one stage, restarts, and crashes. Use `agent-team pipeline job-events [<pipeline>]` for durable job audit history across all pipeline-owned workflows by default, or pass one pipeline for workflow scope; add `--type`, `--status`, `--actor`, `--instance`, `--summary`, `--tail`, or `--follow` when diagnosing job state changes, operator notes, retries, and closes without querying each job separately. Use `agent-team pipeline timeline [<pipeline>]` or `agent-team team timeline <team>` when you want audit and lifecycle rows interleaved in one scoped chronology; add `--source`, `--since`, `--tail`, `--sort newest`, `--json`, or `--format` for handoffs. Use `agent-team team job-events <team>` for the same durable audit drilldown inside one declared team, and `agent-team team events <team> --job <id> --step <id>` for lifecycle drilldown in that boundary. Use `agent-team pipeline queue --state dead --summary` to inspect dead dispatches across all workflows, or `agent-team pipeline queue <pipeline> --state dead --summary` before retrying failed dispatches in one workflow, then preview `agent-team pipeline queue retry <pipeline> --all --sort attempts --limit 10 --dry-run`; add `--commands` to a queue list when automation needs the visible row actions rather than the table. Queue ownership is based on durable pipeline jobs. Global `health` and `overview` prefer these pipeline-scoped queue commands when every affected file resolves to one declared workflow but not one single job. Use `agent-team pipeline queue quarantine --restorable --sort attempts --limit 10` to inspect restorable preserved files across all workflows, or `agent-team pipeline queue quarantine <pipeline> --restorable --sort attempts --limit 10` when queue doctor has preserved invalid files and the recovery should stay inside one workflow; inspect with `show`, then preview `restore --all --sort attempts --limit 10 --dry-run` or `drop --all --sort modified --limit 10 --dry-run`. Use `agent-team pipeline cleanup <pipeline> --dry-run` after PR merge review to preview only done jobs in one workflow; pass `--merged` to remove their job-owned worktrees and branches.
When a stage is best-effort, add `optional = true` to its pipeline step. If that step fails, downstream `after` dependencies can still advance; `job explain`, `pipeline explain`, and retry views keep the optional failure visible, and the job closes as done once all required steps finish.
When a step waits on a manual gate, `agent-team pipeline approve <pipeline>` marks approveable blocked manual gates queued so `pipeline advance`, `team advance`, or `tick` can dispatch them. Add `--step <id>` to approve one stage, add `--dispatch` to approve and dispatch in one command, add `--wait --wait-status running` to block until the approved step has started, or `--wait --wait-next-state running --wait-step review` to wait on the approved stage explicitly. Use `--dry-run --preview-routes` before batch approvals. Use `agent-team job reject <job-id> [reason...] --dry-run` before failing one blocked manual gate with a `manual_gate_rejected` audit event, or `agent-team pipeline reject <pipeline> --dry-run` before rejecting a scoped batch.
If an event-triggered pipeline starts with a manual or PR-gated step, the daemon creates the job and returns a `blocked` event outcome instead of spawning an agent. Use `job next`, `job explain`, or the scoped `pipeline ready --state blocked` view to see the approval or metadata action.
When a step fails, `agent-team pipeline retry <pipeline>` resets retryable failed steps to a blocked-but-ready state so the next `pipeline advance`, `team advance`, or `tick` can dispatch another attempt. Add `--step <id>` to target one failed stage, add `--dispatch` to retry and dispatch in one command, add `--wait --wait-status running` to block until the retried step has started, or `--wait --wait-next-state running --wait-step implement` to wait on the retried stage explicitly. Use `--dry-run --preview-routes` before a batch retry to inspect the resolved routes and payloads, and pass `--message` to record why the retry happened. Add `max_attempts = N` to a pipeline step when retries should stop after N dispatch attempts; `pipeline retry`, `team retry`, and retry-enabled repair sweeps skip capped steps and show the current attempt count in retry/explain output. After fixing an external cause, add `--force` to `pipeline retry` or `team retry` to intentionally override a capped step, or add `--retry-force` with `--retry-pipelines` during repair.
Pipeline and team-scoped dispatch commands accept `--runtime` and `--runtime-bin` for one-off Claude/Codex selection. `tick`, `drain`, `repair`, `pipeline tick`, `pipeline drain`, `pipeline repair`, `team tick`, `team drain`, and `team repair` accept the same overrides for retried and advanced steps. If a step declares `runtime` or `runtime_bin`, that default is stored in the dispatch payload unless an operator override is provided, so queued or delayed starts keep the same intent. `pipeline doctor`, `team doctor`, and top-level `doctor` warn when a step-declared runtime binary cannot be found on the current machine; add `--strict-runtime` when CI or deployment checks should fail on that condition, or add `--commands` to pipeline/team doctor when scripts need route-aware graph and JSON detail follow-ups.
Pipeline status also flags `stale_running_steps` when a running step has exceeded its step `timeout`, or the repo job stale threshold (`[health].job_stale_after`, default 24h) when no step timeout is declared. Start recovery with `agent-team job reconcile events --dry-run` so finished or crashed runtime metadata can update the job; add `--commands` when scripts should print only the matching apply command after an actionable preview. If the step is still running after reconciliation, use `agent-team pipeline timeout <pipeline> --dry-run` to preview marking stale steps failed, then `pipeline retry` when another attempt should run; add `--target-agent` when only one role's stages should expire. For broader maintenance, `agent-team repair --timeout-jobs --retry-pipelines --dry-run --preview-routes` previews stale job expiration and retry phases in one repair report; use `--timeout-pipelines` for pipeline-step-only expiration. Add `--timeout-pipeline` or `--timeout-target-agent` with either timeout mode when a repair sweep should stay inside one workflow or agent role, and add `--retry-pipeline` when the failed-step retry phase should stay inside one workflow.
By default, `pipeline advance` dispatches one ready step per job. Add `--wait --wait-status running` when the command should block until the advanced jobs have live owners, or `--wait --wait-next-state ready --wait-step review` when it should block until the next stage is ready for review. Use `pipeline advance <pipeline> --all-ready-steps` when a job has multiple currently ready independent steps and you want to fan them out in one command. Dependency checks still use the job file: a downstream step waits until all of its `after` steps are marked done, or failed with `optional = true`.
Use `agent-team team approve <team>` for the same manual-gate approval flow scoped to one team's declared pipelines; it accepts the same `--dispatch --wait` handoff and `--wait-next-state`/`--wait-step` stage filters. Use `agent-team team reject <team> --dry-run` before rejecting the team's blocked manual gates, `agent-team team skip <team> --step <id> --dry-run` before bypassing a stage across team-owned jobs, and `agent-team team cancel <team> --dry-run` before cancelling obsolete team-owned jobs.
Use `agent-team team retry <team>` for the same recovery flow scoped to one team's declared pipelines; it accepts the same `--dispatch --wait` handoff and `--wait-next-state`/`--wait-step` stage filters.
Use `agent-team pipeline repair <pipeline> --retry-pipelines` when the broader repair loop should stay inside one workflow, including pipeline-owned dead-letter queue retry and a final scoped advance. Add `--wait --wait-status running` when that scoped repair should block until retried and newly advanced jobs have live owners, or `--wait --wait-next-state running --wait-step implement` when repair should wait on a specific retried or advanced stage. Use `agent-team repair --retry-pipelines` or `agent-team team repair <team> --retry-pipelines` when failed-step retry should happen inside a broader global or team repair loop after daemon reconciliation and dead-letter queue retry; both accept the same stage-aware wait filters for the retry and final tick advance rows they dispatch. Add `--dry-run --preview-routes` first to inspect the dispatch routes, `--retry-pipeline <name>` to target one workflow in global repair, `--retry-step <id>` to target one failed stage, `--retry-message` to record the operator reason, and `--retry-force` only when intentionally overriding step `max_attempts`.
Pipeline status, health, overview, and next-action hints recommend tick previews for ready work, retry and scoped repair dry-runs when failed or stale steps are present, and `pipeline explain ... --state failed` or `team explain ... --state failed` when the operator needs the detailed step diagnostics first.

Supported gates:

- `gate = "manual"`: wait for operator approval with `agent-team pipeline approve <pipeline>`, `agent-team team approve <team>`, or `agent-team job approve <job-id> --step <step-id>`; reject gates with `agent-team pipeline reject <pipeline>`, `agent-team team reject <team>`, or `agent-team job reject <job-id> --step <step-id>`.
- `gate = "pr"`: wait until the job has PR metadata, then advance normally. Use `agent-team job update <job-id> --pr <url> --advance --dry-run` to preview the metadata update and next-step dispatch together before rerunning without `--dry-run`, or add `--commands` when scripts should print the apply command after that preview; add `--wait --wait-status running` when the handoff should block until the unblocked step has a live owner, or `--wait --wait-next-state running --wait-step review` when it should wait for the review stage specifically. GitHub PR webhooks can do the same via `agent-team intake github --reconcile-job --advance --dry-run` or `agent-team job reconcile github --advance --dry-run`; add `--commands` to either dry-run when scripts should print the apply command, then rerun without `--dry-run` and add the same wait flags when a foreground script should wait for the next owner.

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
agent-team team run delivery SQU-42 --dry-run --dispatch --commands
agent-team team run delivery SQU-42 --dispatch --wait --wait-next-state running --wait-step implement --wait-timeout 30s
agent-team team wait-jobs delivery --next-state ready --step review --timeout 30s
agent-team team triage delivery
agent-team team triage delivery --reason queue_dead --commands
agent-team team explain delivery
agent-team team explain delivery --state failed
agent-team team ready delivery
agent-team team ready delivery --commands
agent-team team hold delivery "maintenance window"
agent-team team release delivery
agent-team team adopt delivery squ-42 --step review --pid 12345 --dry-run --json
agent-team team send delivery --dry-run --commands "please checkpoint current status"
agent-team team advance delivery --dry-run --preview-routes
agent-team team advance delivery --all-ready-steps --dry-run --preview-routes
agent-team team advance delivery --wait --wait-status running --wait-timeout 30s
agent-team team advance delivery --wait --wait-next-state ready --wait-step review --wait-timeout 30s
agent-team team approve delivery --dispatch --dry-run --preview-routes
agent-team team approve delivery --dispatch --wait --wait-status running --wait-timeout 30s
agent-team team approve delivery --dispatch --wait --wait-next-state running --wait-step review --wait-timeout 30s
agent-team team retry delivery --dispatch --dry-run --preview-routes
agent-team team retry delivery --dispatch --wait --wait-status running --wait-timeout 30s
agent-team team retry delivery --dispatch --wait --wait-next-state running --wait-step implement --wait-timeout 30s
agent-team team retry delivery --step review --dry-run
agent-team team retry delivery --force --message "operator override after fixing dependency"
agent-team team timeout delivery --dry-run
agent-team team timeout delivery --jobs --dry-run
agent-team team timeout delivery --jobs --target-agent worker --dry-run
agent-team team tick delivery --runtime codex --dry-run --preview-routes --commands
agent-team team tick delivery --all-ready-steps --dry-run
agent-team team tick delivery --wait --wait-status running --wait-timeout 30s
agent-team team tick delivery --wait --wait-next-state running --wait-step implement --wait-timeout 30s
agent-team team repair delivery --dry-run --jobs
agent-team team repair delivery --all-ready-steps --dry-run --preview-routes
agent-team team repair delivery --timeout-jobs --dry-run
agent-team team repair delivery --timeout-jobs --timeout-pipeline ticket_to_pr --dry-run
agent-team team repair delivery --timeout-jobs --timeout-target-agent worker --dry-run
agent-team team repair delivery --timeout-pipelines --dry-run
agent-team team repair delivery --timeout-pipelines --timeout-pipeline ticket_to_pr --dry-run
agent-team team repair delivery --timeout-pipelines --timeout-target-agent worker --dry-run
agent-team team repair delivery --retry-pipelines --runtime codex --dry-run --preview-routes
agent-team team repair delivery --retry-pipelines --wait --wait-status running --wait-timeout 30s
agent-team team repair delivery --retry-pipelines --wait --wait-next-state running --wait-step implement --wait-timeout 30s
agent-team team repair delivery --retry-pipelines --retry-pipeline ticket_to_pr --dry-run --preview-routes
agent-team team repair delivery --retry-pipelines --retry-step review --dry-run --preview-routes
agent-team team repair delivery --retry-pipelines --retry-force --retry-message "override after fix"
agent-team team drain delivery --all-ready-steps --runtime codex
agent-team team drain delivery --wait --wait-status running --wait-timeout 30s
agent-team team drain delivery --wait --wait-next-state running --wait-step implement --wait-timeout 30s
agent-team team snapshot delivery --output delivery.json
```

`team adopt <team> <job-id>` applies the same external-process adoption flow as `job adopt`, but first verifies the job belongs to the team through its declared pipelines or instance agents. Use it when recovery should stay inside a team boundary. Pipeline- and team-scoped adoption output includes scoped status, logs, and resume-plan follow-up actions, including `--step <id>` when the adoption targets one stage. Add `--commands` when scripts need only the follow-up commands.
`team advance <team> --wait --wait-status running` applies the same bounded handoff as pipeline advance, but only for pipelines declared on that team; use `--wait-next-state ready --wait-step review` when the handoff should stop after the next stage becomes ready instead of after the just-dispatched stage starts. `team tick <team> --wait --wait-next-state running --wait-step implement` applies the same stage-aware one-shot maintenance handoff as global `tick --wait`, but only for jobs advanced by that team's tick. `team drain <team> --wait --wait-next-state running --wait-step implement` applies the same finite drain handoff after team-owned cycles reach idle. `team repair <team> --retry-pipelines --wait --wait-next-state running --wait-step implement` applies the same bounded stage-aware repair handoff to team-owned retry and final tick advance rows. `team advance <team> --all-ready-steps` applies the same parallel-ready fan-out as `pipeline advance --all-ready-steps`. Use it when one team owns a job with independent stages that can run at the same time.
Use `team timeout <team> --dry-run` for the same stale running-step expiration flow as `pipeline timeout`, scoped to the pipelines declared on that team. Add `--jobs` when the same direct sweep should also catch stale step-less jobs whose target instance belongs to the team, and add `--target-agent` to expire only one team role's stale work. Use `team repair <team> --timeout-jobs --dry-run` when the timeout should run inside the broader repair loop; add `--timeout-pipeline` or `--timeout-target-agent` with either timeout mode to keep repair inside one team-owned workflow or agent role.
`team pipelines <team>` includes queue/outbox and quarantine counts for the team's workflows, and its actions use `team queue ...` and `team outbox ...` recovery commands. `pipeline next <pipeline> --team <team>` renders the same scoped recovery commands when a multi-team operator wants one workflow's next actions without leaving the team boundary.
`tick --all-ready-steps`, `pipeline tick <pipeline> --all-ready-steps`, `drain --all-ready-steps`, `pipeline drain <pipeline> --all-ready-steps`, `repair --all-ready-steps`, `team tick <team> --all-ready-steps`, `team repair <team> --all-ready-steps`, and `team drain <team> --all-ready-steps` apply that fan-out during maintenance and recovery cycles, including watch and until-idle loops.
Use `pipeline hold <pipeline>` or `team hold <team>` for scoped maintenance windows and incident freezes. Use `job hold --all --dry-run` when the freeze should span multiple pipelines or include non-pipeline jobs. These commands hold matching jobs in bulk without changing lifecycle status; add `--state failed`, `--state ready`, `--limit N`, `--for 2h`, or `--until 2026-06-24T18:00:00Z` to narrow or time-box the batch, and run with `--dry-run` first. `pipeline release` and `team release` resume held jobs in the same scope; add `--expired` to release only jobs whose `hold_until` has passed. Use `job ls --expired-hold`, `pipeline jobs --expired-hold`, `pipeline jobs <pipeline> --expired-hold`, or `team jobs <team> --expired-hold` to audit elapsed holds before release; `overview`, `next --reason expired_holds`, and team-scoped overview/next views also recommend the matching expired-release dry-run. Use `job release --all --expired --dry-run` when elapsed holds may span multiple pipelines or include non-pipeline jobs.

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
agent-team team health delivery --jobs --commands
agent-team team status delivery
agent-team team monitor delivery --jobs --schedules
```

Team health includes:

- daemon readiness
- team-owned queue dead letters
- team-owned queue and outbox quarantine
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
