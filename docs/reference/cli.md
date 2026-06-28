# CLI Reference

This is a developer-oriented command map. Run `agent-team <command> --help` for exact flag help from the current binary.

Most commands that read an existing `.agent_team/` tree accept the global
`--repo <dir>` selector. Older commands may still expose `--target <dir>` for
the repo root; when both are present, `--repo` wins. Commands that create or
render into a destination, such as `init` and `template run`, keep `--target`
as the output directory instead.

## Project Setup

| Command | Purpose |
| --- | --- |
| `agent-team init [ref] [--dry-run] [--commands] [--format <template>] [--json]` | Instantiate a template into `.agent_team/`, preview it, or emit a machine-readable success record |
| `agent-team template ls [--format <template>] [--json]` | List bundled and cached templates with text, Go-template, or JSON output |
| `agent-team template show [ref] [--format <template>] [--json]` | Show a template manifest with content hash, agents, skills, and parameters |
| `agent-team template pull <ref> [--as <cache-ref>] [--dry-run] [--commands] [--format <template>] [--json]` | Cache a template, or preview/print the apply command |
| `agent-team template rm <ref> [--dry-run] [--commands] [--format <template>] [--json]` | Remove a cached template, or preview/print the apply command |
| `agent-team template smoke [ref] [--format <template>] [--json]` | Render a template in a temp repo and validate it; add `--strict-runtime` for CI runtime checks |
| `agent-team template run <ref> <agent>` | One-shot init plus run in a temp or target dir |
| `agent-team upgrade --check|--apply` | Compare current template lock to target or apply clean template changes |
| `agent-team agent ls` / `agent-team agent show <agent>` | List or inspect runnable agent definitions installed under `.agent_team/agents`; `agents` is a plural alias |
| `agent-team doctor` | Validate local layout, durable job files, runtime availability, and workflow runtime defaults |
| `agent-team dispatch <target> <ticket>` | Publish or preview an `agent.dispatch` topology event; add `--dry-run --commands` to print the matching dispatch apply command for matched routes |
| `agent-team docs cli` | Generate or check markdown reference from the live command tree |

## Runtime and Daemon

| Command | Purpose |
| --- | --- |
| `agent-team daemon start` | Start `agent-teamd` |
| `agent-team daemon status` | Check daemon process and API readiness |
| `agent-team daemon logs` | Read or follow daemon log |
| `agent-team daemon stop` | Stop daemon |
| `agent-team daemon restart` | Restart daemon |
| `agent-team daemon reconcile` | Refresh metadata from process table |
| `agent-team runtime` | Show selected LLM runtime profile; use `--runtime` / `--runtime-bin` to preview one-off overrides |
| `agent-team runtime set` | Persist the repo default runtime profile in `.agent_team/config.toml` |
| `agent-team runtime unset` | Remove the repo default runtime profile from `.agent_team/config.toml` |
| `agent-team runtime profile` | Explicit profile view, with `show` as a shorter alias |
| `agent-team runtime ls` | List supported runtime profiles, availability, and capabilities |
| `agent-team runtime probe` | Probe runtime selection, daemon readiness, Codex doctor health, optional Codex exec readiness, Codex socket reachability with `--exec-socket-check`, loopback HTTP reachability with `--exec-http-check`, print follow-up commands with `--commands`, and write diagnostics with `--output`; aliases: `doctor`, `check` |
| `agent-team adopt <instance>` | Adopt a live external runtime process into daemon metadata and return follow-up actions; add `--commands` for one follow-up command per line; `runtime adopt` remains available |
| `agent-team resume-plan` | Show resume, attach, and log fallback commands from daemon metadata; filter by `--step`, `--action`, `--runtime-stale`, or `--unhealthy`, sort/limit large recovery lists, print one command per line with `--commands`, or summarize with `--summary`; `runtime resume-plan` remains available |

## Instance Lifecycle

| Command | Purpose |
| --- | --- |
| `agent-team run <agent>` | Launch an agent directly or through daemon with `--detach`; use `--runtime` for one-off Claude/Codex selection |
| `agent-team start [instances...]` | Start/resume persistent or selected instances; add `--dry-run --commands` to print the matching apply command when the preview has actionable work |
| `agent-team stop [instances...]` | Stop selected instances; add `--dry-run --commands` to print the matching apply command when the preview has actionable work |
| `agent-team kill [instances...]` | Force-stop selected instances; add `--dry-run --commands` to print the matching apply command when the preview has actionable work |
| `agent-team restart [instances...]` | Restart/resume selected instances; add `--dry-run --commands` to print the matching apply command when the preview has actionable work |
| `agent-team ps` | List instance rows; filter mixed-runtime views with `--runtime` |
| `agent-team inspect [instances...]` | Show runtime and state detail |
| `agent-team logs [instance]` | Read/follow instance logs, use `--last-message` for clean Codex final responses, or `--clean` to hide known Codex diagnostics |
| `agent-team stats` | Show CPU/RSS data |
| `agent-team attach <instance>` | Interactive runtime resume handoff; `exec` is a Docker-like alias |
| `agent-team wait [instances...]` | Wait for lifecycle or phase conditions |
| `agent-team rm [instances...]` | Remove state and metadata; add `--dry-run --commands` to print the matching remove command when the preview has actionable work |
| `agent-team prune` | Remove finished old metadata/state; add `--dry-run --commands` to print the matching prune apply command when the preview has actionable work |

Shortcuts:

| Shortcut | Equivalent |
| --- | --- |
| `agent-team up` | `agent-team start` |
| `agent-team down` | `agent-team stop` |
| `agent-team ls` | `agent-team ps` |
| `agent-team top` | `agent-team stats` |
| `agent-team exec` | `agent-team attach` |

Collection groups also accept natural plural aliases: `agents`, `jobs`, `pipelines`, `queues`, `schedules`, and `teams`.

## Topology and Convergence

| Command | Purpose |
| --- | --- |
| `agent-team topology show` | Render loaded topology |
| `agent-team topology graph` | Render full topology graph |
| `agent-team topology summary` | Summarize topology health |
| `agent-team topology reload` | Reload daemon topology, with JSON/template output for scripts |
| `agent-team plan` | Preview desired instance state; add `--commands` to print the matching dry-run sync command when the selected plan has actionable work |
| `agent-team sync` | Reload, reconcile, start/resume desired instances; add `--dry-run --commands` to print the matching apply command when the preview has actionable work |
| `agent-team tick` | Run one maintenance cycle or loop; use `--runtime` for advanced steps and `--wait-next-state`/`--wait-step` for one-shot stage-aware handoff; `team tick <team>` supports the same scoped handoff |
| `agent-team drain` | Run maintenance cycles until idle; use `--runtime` for advanced steps and `--wait-next-state`/`--wait-step` for bounded stage-aware handoff after drain cycles; `team drain <team>` supports the same scoped handoff |
| `agent-team reload` | Top-level daemon topology reload |

## Jobs

| Command | Purpose |
| --- | --- |
| `agent-team job create <ticket>` | Create a durable job; add `--dispatch --wait` for bounded create-and-run automation and `--wait --wait-next-state`/`--wait-step` for pipeline stage handoff |
| `agent-team job ls` | List jobs; filter held state, hold deadlines, and mixed-runtime ownership; sort rows and cap output with `--limit` |
| `agent-team job show <job-id>` | Show job detail, runtime metadata, queue, quarantine, outbox, status previews, and actions; add `--commands` to print only follow-up commands; `inspect` is an alias |
| `agent-team job doctor` | Validate durable job TOML files, including filename/id ownership and persisted state invariants; `--quarantine --dry-run` previews isolating malformed active job files and `--commands` prints recovery commands |
| `agent-team job quarantine` | Inspect, summarize, restore, or drop job TOML files preserved by `job doctor --quarantine`; `quarantine show` accepts `--commands` |
| `agent-team job wait <job-id>` | Wait for lifecycle status, last event, or next-step state/stage with `--next-state` and `--step` |
| `agent-team job next <job-id>` | Show the next pipeline step without dispatching it; add `--state`, `--step`, or `--commands` when scripts need a compact assertion or next-action commands |
| `agent-team job resume-plan <job-id>` | Show runtime resume, attach, and log fallback commands for one job; add `--step` for one pipeline stage, `--commands` for one command per line, or `--sort`/`--limit` for multi-runtime jobs |
| `agent-team job ps <job-id>` | List daemon-aware instance rows for one job; add `--step` for one pipeline stage |
| `agent-team job stats <job-id>` | Show CPU and memory usage for one job's instances; add `--step` for one pipeline stage |
| `agent-team job top <job-id>` | `agent-team job stats <job-id>` |
| `agent-team job exec <job-id>` | `agent-team job attach <job-id>` |
| `agent-team job snapshot <job-id>` | Capture one job's post-mortem metadata, provenance, events, inboxes, queue/outbox ownership including quarantine, state files, optional log tails, and formatted summary fields |
| `agent-team job explain <job-id>` | Explain or watch pipeline step readiness, blockers, gates, and next actions; add `--state` or `--step` to focus one state or stage, or `--commands` for nested action commands |
| `agent-team job watch <job-id>` | Continuous job explanation shortcut for next-step readiness, blockers, gates, and actions |
| `agent-team job approve <job-id>` | Approve a blocked manual pipeline gate; add `--dry-run --commands` for the apply command or `--advance --wait --wait-next-state`/`--wait-step` for stage-aware handoff |
| `agent-team job reject <job-id>` | Reject a blocked manual pipeline gate and mark it failed; add `--dry-run --commands` for the apply command |
| `agent-team job step <job-id> <step-id>` | Update a pipeline step; add `--dry-run --commands` for the apply command or `--advance --wait --wait-next-state`/`--wait-step` after marking a step done |
| `agent-team job dispatch <job-id>` | Dispatch a job; add `--dry-run --commands` for the apply command, use `--runtime` for one-off Claude/Codex selection, and `--wait` for bounded automation |
| `agent-team job send <job-id>` | Send message to job instance; add `--dry-run --commands` for the apply command |
| `agent-team job note <job-id>` | Append an operator or automation note to the job audit log; add `--dry-run --commands` for the apply command |
| `agent-team job block <job-id>` | Mark a job blocked and record the reason; add `--dry-run --commands` for the apply command |
| `agent-team job unblock <job-id>` | Send answer and mark blocked job running; add `--dry-run --commands` for the apply command |
| `agent-team job reopen|retry <job-id>` | Reopen/retry a failed or closed job; add `--dry-run --commands` for the apply command or `--dispatch --wait --wait-next-state`/`--wait-step` for pipeline recovery handoff |
| `agent-team job update <job-id>` | Update job metadata; add `--dry-run --commands` for the apply command, `--advance --dry-run` to preview unblocked steps, or `--advance --wait --wait-next-state`/`--wait-step` for PR-gate handoff |
| `agent-team job hold <job-id>` | Pause readiness/advance automation without changing lifecycle status; use `--all` for repo-wide freezes, and add `--for` or `--until` for a deadline |
| `agent-team job release <job-id>` | Resume readiness/advance automation for a held job; use `--all --expired` for elapsed time-boxed holds |
| `agent-team job close <job-id>` | Mark done or failed; add `--dry-run --commands` for the apply command |
| `agent-team job cancel <job-id>` | Fail a job as cancelled, optionally stopping its owner; add `--dry-run --commands` for the apply command |
| `agent-team job timeout <job-id> or --all` | Mark stale running job steps or stale step-less running jobs failed; add `--pipeline` or `--target-agent` with `--all` to scope a sweep |
| `agent-team job cleanup <job-id>` | Remove job-owned worktree/branch after merge, optionally verifying the PR with `gh` |
| `agent-team job rm <job-id>` | Remove job files and event logs; add `--dry-run --commands` for the apply command |
| `agent-team job prune` | Remove terminal job files and event logs; add `--dry-run --commands` for the apply command |
| `agent-team job triage` | Find jobs needing attention, including queue/outbox quarantine recovery hints; add `--commands` for attention-row recovery commands |
| `agent-team job ready` | List or watch next pipeline steps; filter by `--step`, sort by `--sort`, cap rows with `--limit`, or print one action per line with `--commands` |
| `agent-team job advance <job-id>` | Advance a pipeline step; add `--dry-run --commands` for the apply command or `--wait --wait-next-state`/`--wait-step` for stage-aware handoff |
| `agent-team job reconcile github` | Reconcile GitHub PR payloads into jobs; add `--advance --wait --wait-next-state`/`--wait-step` for PR-gate handoff |
| `agent-team job events <job-id>` | Show job event log |

## Job Queue

| Command | Purpose |
| --- | --- |
| `agent-team job queue <job-id>` | List or watch active queue entries owned by a job; filter queued dispatches with `--runtime`, sort rows with `--sort`, and cap output with `--limit` |
| `agent-team job queue show <job-id> <id>` | Inspect one active queue item owned by a job; add `--commands` to print only follow-up commands |
| `agent-team job queue retry <job-id> <id>` | Retry one job-owned queue item |
| `agent-team job queue retry <job-id> --all` | Retry matching job-owned queue items; filter, sort, and limit batch actions with `--runtime`, `--sort`, and `--limit` |
| `agent-team job queue drop <job-id> <id>` | Drop one job-owned queue item |
| `agent-team job queue drop <job-id> --all` | Drop matching job-owned queue items; filter, sort, and limit batch actions with `--runtime`, `--sort`, and `--limit` |
| `agent-team job queue prune <job-id>` | Age-prune job-owned queue entries; filter and limit prune candidates with `--runtime`, `--ready`, and `--limit` |
| `agent-team job queue quarantine <job-id>` | List or summarize job-owned quarantined queue files; sort rows with `--sort` and cap output with `--limit` |
| `agent-team job queue quarantine show <job-id> <path>` | Inspect one preserved file; add `--commands` to print only follow-up commands |
| `agent-team job queue quarantine restore <job-id> <path>` | Restore one preserved file |
| `agent-team job queue quarantine restore <job-id> --all` | Restore matching restorable files; sort and cap batch actions with `--sort` and `--limit` |
| `agent-team job queue quarantine drop <job-id> <path>` | Drop one preserved file |
| `agent-team job queue quarantine drop <job-id> --all` | Drop matching preserved files; sort and cap batch actions with `--sort` and `--limit` |

## Job Outbox

| Command | Purpose |
| --- | --- |
| `agent-team job outbox <job-id>` | List, summarize, or watch sandboxed outbox events owned by one job; filter by state, type, or source |
| `agent-team job outbox show <job-id> <id>` | Inspect one outbox event owned by one job; add `--commands` to print only follow-up commands |
| `agent-team job outbox retry <job-id> <id>` | Move one job-owned processed or failed outbox event back to pending |
| `agent-team job outbox retry <job-id> --all` | Retry matching job-owned outbox events; filter, sort, and limit batch actions with `--state`, `--type`, `--source`, `--sort`, and `--limit` |
| `agent-team job outbox drop <job-id> <id>` | Remove one inspected job-owned outbox event |
| `agent-team job outbox drop <job-id> --all` | Drop matching job-owned outbox events; filter, sort, and limit batch actions with `--state`, `--type`, `--source`, `--sort`, and `--limit` |
| `agent-team job outbox prune <job-id>` | Remove old job-owned processed outbox events by default; pass `--state failed`, `pending`, or `all` for explicit cleanup and bound with `--older-than`, filters, and `--limit` |
| `agent-team job outbox quarantine <job-id>` | List or summarize quarantined outbox files owned by one job; filter by state, type, source, or restorable state |
| `agent-team job outbox quarantine show <job-id> <path>` | Inspect one job-owned quarantined outbox file and its payload when parseable; add `--commands` to print only follow-up commands |
| `agent-team job outbox quarantine restore <job-id> <path>` | Restore one validated job-owned quarantined outbox file to the active outbox |
| `agent-team job outbox quarantine restore <job-id> --all` | Restore matching job-owned restorable quarantined outbox files; filter, sort, and limit batch actions |
| `agent-team job outbox quarantine drop <job-id> <path>` | Drop one job-owned quarantined outbox file after inspection |
| `agent-team job outbox quarantine drop <job-id> --all` | Drop matching job-owned quarantined outbox files; filter by state, source, restorable state, or age before deleting |

## Global Queue

| Command | Purpose |
| --- | --- |
| `agent-team queue ls` | List active queue entries; filter queued dispatches with `--runtime`, sort rows with `--sort`, and cap output with `--limit` |
| `agent-team queue watch` | Continuous active queue list shortcut with the same filters and formatting as `queue ls --watch` |
| `agent-team queue show <id>` | Inspect one active queue item, including resolved runtime metadata; add `--commands` to print only follow-up commands |
| `agent-team queue drain` | Dispatch ready pending entries |
| `agent-team queue retry <id>` | Retry one entry |
| `agent-team queue retry --all` | Retry matching entries; filter, sort, and limit batch actions with `--runtime`, `--sort`, and `--limit` |
| `agent-team queue drop <id>` | Drop one entry |
| `agent-team queue drop --all` | Drop matching entries; filter, sort, and limit batch actions with `--runtime`, `--sort`, and `--limit` |
| `agent-team queue prune` | Age-prune entries; filter and limit prune candidates with `--runtime`, `--ready`, and `--limit` |
| `agent-team queue doctor` | Validate queue files; add `--commands` to print recovery commands only |
| `agent-team queue quarantine ls` | List or summarize quarantined queue files; sort rows with `--sort` and cap output with `--limit` |
| `agent-team queue quarantine show <path>` | Inspect quarantined queue file; add `--commands` to print only follow-up commands |
| `agent-team queue quarantine restore <path>` | Restore one preserved file |
| `agent-team queue quarantine drop <path>` | Drop one preserved file |
| `agent-team queue quarantine restore --all` | Restore matching restorable files; sort and cap batch actions with `--sort` and `--limit` |
| `agent-team queue quarantine drop --all` | Drop matching preserved files; sort and cap batch actions with `--sort` and `--limit` |

## Agent Outbox

| Command | Purpose |
| --- | --- |
| `agent-team outbox ls` | List or watch sandboxed agent outbox events; filter by state, type, source, or job and sort/cap rows |
| `agent-team outbox watch` | Continuous outbox list shortcut with the same filters and formatting as `outbox ls --watch` |
| `agent-team outbox show <id>` | Inspect one outbox event and its payload; add `--commands` to print only follow-up commands |
| `agent-team outbox drain` | Ask the daemon to publish pending outbox events through topology; `--dry-run` previews locally if the daemon is down |
| `agent-team outbox doctor` | Validate persisted outbox files without relying on normal listing paths; `--quarantine --dry-run` previews isolating malformed active files and `--commands` prints recovery commands |
| `agent-team outbox quarantine ls` | List or summarize quarantined outbox files; filter by state, type, source, job, or restorable state and sort/cap rows |
| `agent-team outbox quarantine show <path>` | Inspect one quarantined outbox file and its payload when parseable; add `--commands` to print only follow-up commands |
| `agent-team outbox quarantine restore <path>` | Restore one validated quarantined outbox file to the active outbox |
| `agent-team outbox quarantine restore --all` | Restore matching restorable quarantined outbox files; filter, sort, and limit batch actions |
| `agent-team outbox quarantine drop <path>` | Drop one quarantined outbox file after inspection |
| `agent-team outbox quarantine drop --all` | Drop matching quarantined outbox files; filter by restorable state or age before deleting |
| `agent-team outbox retry <id>` | Move a failed or processed outbox event back to pending |
| `agent-team outbox retry --all` | Retry matching outbox events; filter, sort, and limit batch actions with `--state`, `--type`, `--source`, `--job`, `--sort`, and `--limit` |
| `agent-team outbox drop <id>` | Remove one outbox event after inspection |
| `agent-team outbox drop --all` | Drop matching outbox events; filter, sort, and limit batch actions with `--state`, `--type`, `--source`, `--job`, `--sort`, and `--limit` |
| `agent-team outbox prune` | Remove old processed outbox events by default; pass `--state failed`, `pending`, or `all` for explicit cleanup and bound with `--older-than`, filters, and `--limit` |

## Pipelines

| Command | Purpose |
| --- | --- |
| `agent-team pipeline ls` | List pipeline declarations |
| `agent-team pipeline show <pipeline>` | Show one declaration; `inspect` is an alias |
| `agent-team pipeline graph <pipeline>` | Render text, Mermaid, DOT, or JSON step graphs |
| `agent-team pipeline doctor --all` | Validate workflows; add `--strict-runtime` to fail on unavailable step runtime defaults |
| `agent-team pipeline run <pipeline> <ticket>` | Create pipeline job; `--dispatch` accepts workspace/runtime overrides and `--wait-next-state`/`--wait-step` can block for the first stage handoff |
| `agent-team pipeline status` | Summarize or watch pipeline jobs plus owned queue/outbox and quarantine counts; sort rows and cap output with `--limit`; add `--commands` for one row action command per line |
| `agent-team pipeline watch [<pipeline>]` | Continuous pipeline status shortcut with queue/outbox and quarantine counts |
| `agent-team pipeline triage [<pipeline>]` | Show pipeline-owned jobs needing attention, including queue/outbox quarantine and ready-step recovery hints; add `--commands` for scoped attention-row recovery commands |
| `agent-team pipeline explain <pipeline>` | Expand or watch pipeline jobs as per-step readiness, blockers, gates, and actions; sort and cap large histories with `--sort` and `--limit`, add `--step` to focus one stage, or `--commands` for flattened action commands |
| `agent-team pipeline snapshot <pipeline>` | Capture one pipeline's status, provenance, explained jobs, inboxes, queue/outbox ownership including quarantine, dry-run advance previews, and formatted summary fields |
| `agent-team pipeline next` | Print or watch recommended pipeline actions; use `--commands` for one selected action command per line |
| `agent-team pipeline wait [<pipeline>]` | Wait for pipeline jobs to reach a lifecycle status, event, or next-step state/stage |
| `agent-team pipeline jobs [<pipeline>]` | List, summarize, or watch pipeline jobs; filter held state, hold deadlines, mixed-runtime ownership, sort rows, and cap output with `--limit` |
| `agent-team pipeline ready` | List or watch ready steps; filter by `--step`, sort by `--sort`, cap rows with `--limit`, or print one scoped action per line with `--commands` |
| `agent-team pipeline hold <pipeline>` | Hold matching pipeline jobs without changing lifecycle status; add `--for` or `--until` for a deadline |
| `agent-team pipeline release <pipeline>` | Release held jobs in a pipeline; add `--expired` to release only elapsed deadlines |
| `agent-team pipeline advance <pipeline>` | Advance ready work; use `--workspace`/`--runtime` for dispatched steps and `--wait-next-state`/`--wait-step` for stage-aware handoff |
| `agent-team pipeline approve <pipeline>` | Approve blocked manual gates; add `--dry-run --commands` for the apply command or `--dispatch --wait-next-state`/`--wait-step` for stage-aware approval handoff |
| `agent-team pipeline reject <pipeline>` | Reject blocked manual gates |
| `agent-team pipeline unblock <pipeline>` | Answer blocked pipeline workers; add `--dry-run --commands` to print the matching apply command |
| `agent-team pipeline skip <pipeline> --step <id>` | Mark matching non-running steps intentionally skipped |
| `agent-team pipeline cancel <pipeline>` | Cancel non-terminal pipeline jobs without stopping instances |
| `agent-team pipeline adopt <pipeline> <job-id>` | Adopt a live external process for a job after verifying pipeline ownership; output includes scoped follow-up actions and `--commands` |
| `agent-team pipeline resume-plan [<pipeline>]` | Pipeline-owned runtime recovery commands across all workflows by default; filter by `--step`, `--action`, `--runtime-stale`, or `--unhealthy`, sort/limit large recovery lists, print one command per line with `--commands`, or summarize with `--summary` |
| `agent-team pipeline send <pipeline>` | Send a mailbox message to pipeline-owned daemon-known instances; add `--dry-run --commands` to print the matching scoped send apply command |
| `agent-team pipeline ps [<pipeline>\|--all]` | List daemon-aware instance rows for pipeline-owned jobs across all workflows by default |
| `agent-team pipeline stats [<pipeline>\|--all]` | Show CPU and memory usage for pipeline-owned instances across all workflows by default; filter by `--runtime`, `--status`, `--phase`, or summarize with `--summary` |
| `agent-team pipeline top [<pipeline>\|--all]` | `agent-team pipeline stats [<pipeline>\|--all]` |
| `agent-team pipeline logs [<pipeline>]` | Read daemon-captured logs for pipeline-owned instances across all workflows by default |
| `agent-team pipeline events [<pipeline>]` | Read lifecycle events for pipeline-owned instances across all workflows by default |
| `agent-team pipeline cleanup <pipeline>` | Scoped job cleanup for done jobs in one pipeline |
| `agent-team pipeline queue [<pipeline>]` | List or summarize active/quarantined queue items owned by one or all pipelines; subcommands inspect, retry, drop, prune, or recover items owned by one pipeline; `queue show` and `queue quarantine show` accept `--commands` |
| `agent-team pipeline outbox [<pipeline>]` | List, summarize, or watch outbox events owned by one or all pipelines; subcommands inspect, retry, drop, or prune events owned by one pipeline; `outbox show` accepts `--commands` |
| `agent-team pipeline outbox retry <pipeline> --all` | Retry matching pipeline-owned outbox events; filter, sort, and limit batch actions with `--state`, `--type`, `--source`, `--job`, `--sort`, and `--limit` |
| `agent-team pipeline outbox drop <pipeline> --all` | Drop matching pipeline-owned outbox events; filter, sort, and limit batch actions with `--state`, `--type`, `--source`, `--job`, `--sort`, and `--limit` |
| `agent-team pipeline outbox prune <pipeline>` | Remove old pipeline-owned processed outbox events by default; pass `--state failed`, `pending`, or `all` for explicit cleanup and bound with `--older-than`, filters, and `--limit` |
| `agent-team pipeline outbox quarantine [<pipeline>]` | List or summarize quarantined outbox files owned by one or all pipelines; filter by state, job, or restorable state |
| `agent-team pipeline outbox quarantine show <pipeline> <path>` | Inspect one pipeline-owned quarantined outbox file and its payload when parseable; add `--commands` to print only follow-up commands |
| `agent-team pipeline outbox quarantine restore <pipeline> <path>` | Restore one validated pipeline-owned quarantined outbox file to the active outbox |
| `agent-team pipeline outbox quarantine restore <pipeline> --all` | Restore matching pipeline-owned restorable quarantined outbox files; filter, sort, and limit batch actions |
| `agent-team pipeline outbox quarantine drop <pipeline> <path>` | Drop one pipeline-owned quarantined outbox file after inspection |
| `agent-team pipeline outbox quarantine drop <pipeline> --all` | Drop matching pipeline-owned quarantined outbox files; filter by state, job, restorable state, or age before deleting |
| `agent-team pipeline timeout <pipeline>` | Mark stale running steps failed; add `--target-agent` to scope by role |
| `agent-team pipeline retry <pipeline>` | Retry failed steps, honoring step `max_attempts` caps; add `--dry-run --commands` for the apply command or `--dispatch --wait-next-state`/`--wait-step` for stage-aware recovery handoff |
| `agent-team pipeline tick <pipeline>` | Run or preview one scoped queue drain and ready-step advance cycle for one pipeline; add `--wait-next-state`/`--wait-step` for stage-aware bounded handoff |
| `agent-team pipeline repair <pipeline>` | Scoped repair loop for one pipeline: queue retry, optional timeout/retry, ready-step advance, and `--wait-next-state`/`--wait-step` stage-aware bounded handoff |
| `agent-team pipeline drain <pipeline>` | Run scoped queue drain and ready-step advance cycles until one pipeline is idle; add `--wait-next-state`/`--wait-step` for stage-aware bounded handoff |

## Teams

| Command | Purpose |
| --- | --- |
| `agent-team team ls` | List teams |
| `agent-team team show <team>` | Show team declaration; `inspect` is an alias |
| `agent-team team graph <team>` | Render team-owned instance, schedule, and pipeline wiring |
| `agent-team team doctor --all` | Validate team-owned workflow wiring; add `--strict-runtime` to fail on unavailable step runtime defaults |
| `agent-team team overview <team>` | Scoped operator overview; add `--commands` for one scoped action command per line |
| `agent-team team health <team>` | Scoped health; add `--commands` for one scoped remediation command per line |
| `agent-team team resume-plan <team>` | Team-scoped runtime recovery commands; filter by `--step`, `--action`, `--runtime-stale`, or `--unhealthy`, sort/limit large recovery lists, print one command per line with `--commands`, or summarize with `--summary`; `team runtime resume-plan` remains available |
| `agent-team team status <team>` | Scoped status; add `--commands` for one scoped action command per line |
| `agent-team team monitor <team>` | Scoped dashboard with team-owned queue/outbox recovery signals |
| `agent-team team watch <team>` | Continuous scoped dashboard with team-owned queue/outbox recovery signals |
| `agent-team team top <team>` | `agent-team team stats <team>` |
| `agent-team team run <team> <ticket>` | Create a team-owned job; `--dispatch` accepts workspace/runtime overrides and `--wait-next-state`/`--wait-step` can block for the first stage handoff |
| `agent-team team up <team>` | Start or resume a team's declared persistent instances; add `--dry-run --commands` to print the matching team up apply command when the preview has actionable work |
| `agent-team team down <team>` | Stop a team's persistent instances and active ephemeral children; add `--dry-run --commands` to print the matching team down apply command when the preview has actionable work |
| `agent-team team restart <team>` | Restart or resume a team's declared persistent instances; add `--dry-run --commands` to print the matching team restart apply command when the preview has actionable work |
| `agent-team team plan <team>` | Preview one team's desired instance state; add `--commands` to print the matching dry-run team sync command when the selected plan has actionable work |
| `agent-team team sync <team>` | Reconcile one team's declared persistent instances; add `--dry-run --commands` to print the matching team sync apply command when the preview has actionable work |
| `agent-team team prune <team>` | Remove finished team-owned instances; add `--dry-run --commands` to print the matching team prune apply command when the preview has actionable work |
| `agent-team team send <team>` | Send a mailbox message to team-owned daemon-known instances; add `--dry-run --commands` to print the matching scoped send apply command |
| `agent-team team jobs <team>` | Scoped job list, summary, or watch view; filter held state, mixed-runtime ownership, and cap output with `--limit` |
| `agent-team team wait-jobs <team>` | Wait for team-owned jobs to reach a lifecycle status, event, or next-step state/stage |
| `agent-team team tick <team>` | Scoped maintenance cycle; use `--workspace`/`--runtime` for advanced steps and `--wait-next-state`/`--wait-step` for stage-aware bounded handoff |
| `agent-team team drain <team>` | Scoped drain-until-idle maintenance loop; add `--wait-next-state`/`--wait-step` for stage-aware bounded handoff |
| `agent-team team repair <team>` | Scoped repair loop, including stale-work timeout with `--timeout-jobs`; failed-step retry accepts pipeline/step filters, `--retry-force`, workspace/runtime overrides, and `--wait-next-state`/`--wait-step` stage-aware bounded handoff |
| `agent-team team queue <team>` | Scoped queue list; filter queued dispatches with `--runtime`, sort rows with `--sort`, and cap output with `--limit` |
| `agent-team team queue show <team> <id>` | Inspect one active queue item owned by a team; add `--commands` to print only follow-up commands |
| `agent-team team queue retry <team> --all` | Retry matching team-owned entries; filter, sort, and limit batch actions with `--runtime`, `--sort`, and `--limit` |
| `agent-team team queue drop <team> --all` | Drop matching team-owned entries; filter, sort, and limit batch actions with `--runtime`, `--sort`, and `--limit` |
| `agent-team team queue prune <team>` | Age-prune team-owned entries; filter and limit prune candidates with `--runtime`, `--ready`, and `--limit` |
| `agent-team team queue quarantine <team>` | Scoped quarantine list or summary; sort rows with `--sort`, cap output with `--limit`, and add `--commands` to quarantine show for follow-up commands |
| `agent-team team outbox <team>` | Scoped outbox list, summary, or watch view; filter by state, type, source, or job and sort/cap rows |
| `agent-team team outbox show <team> <id>` | Inspect one outbox event owned by a team; add `--commands` to print only follow-up commands |
| `agent-team team outbox retry <team> <id>` | Move one team-owned failed or processed outbox event back to pending |
| `agent-team team outbox retry <team> --all` | Retry matching team-owned outbox events; filter, sort, and limit batch actions with `--state`, `--type`, `--source`, `--job`, `--sort`, and `--limit` |
| `agent-team team outbox drop <team> <id>` | Remove one inspected team-owned outbox event |
| `agent-team team outbox drop <team> --all` | Drop matching team-owned outbox events; filter, sort, and limit batch actions with `--state`, `--type`, `--source`, `--job`, `--sort`, and `--limit` |
| `agent-team team outbox prune <team>` | Remove old team-owned processed outbox events by default; pass `--state failed`, `pending`, or `all` for explicit cleanup and bound with `--older-than`, filters, and `--limit` |
| `agent-team team outbox quarantine <team>` | List or summarize quarantined outbox files owned by one team; filter by state, job, or restorable state |
| `agent-team team outbox quarantine show <team> <path>` | Inspect one team-owned quarantined outbox file and its payload when parseable; add `--commands` to print only follow-up commands |
| `agent-team team outbox quarantine restore <team> <path>` | Restore one validated team-owned quarantined outbox file to the active outbox |
| `agent-team team outbox quarantine restore <team> --all` | Restore matching team-owned restorable quarantined outbox files; filter, sort, and limit batch actions |
| `agent-team team outbox quarantine drop <team> <path>` | Drop one team-owned quarantined outbox file after inspection |
| `agent-team team outbox quarantine drop <team> --all` | Drop matching team-owned quarantined outbox files; filter by state, job, restorable state, or age before deleting |
| `agent-team team pipelines <team>` | List or watch team-owned pipeline status rows with queue/outbox and quarantine counts; sort rows and cap output with `--limit`; add `--commands` for one scoped row action command per line |
| `agent-team team explain <team>` | Expand or watch team-owned pipeline jobs as per-step diagnostics; sort and cap large histories with `--sort` and `--limit`, add `--step` to focus one stage, or `--commands` for scoped action commands |
| `agent-team team triage <team>` | Show team-owned jobs needing operator attention; add `--commands` for team-scoped attention-row recovery commands |
| `agent-team team ready <team>` | List or watch scoped ready pipeline steps; filter by `--step`, sort by `--sort`, cap rows with `--limit`, or print one team-scoped action per line with `--commands` |
| `agent-team team schedules <team>` | List team-owned schedules; add `--commands` to print the scoped dry-run tick preview command when any are due |
| `agent-team team hold <team>` | Hold matching pipeline jobs owned by a team; add `--for` or `--until` for a deadline |
| `agent-team team release <team>` | Release held pipeline jobs owned by a team; add `--expired` to release only elapsed deadlines |
| `agent-team team timeout <team>` | Timeout stale team pipeline steps; add `--jobs` for stale step-less team jobs and `--target-agent` to scope by role |
| `agent-team team advance <team>` | Scoped pipeline advance; use `--runtime` for dispatched steps and `--wait-next-state`/`--wait-step` for stage-aware handoff |
| `agent-team team approve <team>` | Scoped manual-gate approval; add `--dry-run --commands` for the apply command or `--dispatch --wait-next-state`/`--wait-step` for stage-aware approval handoff |
| `agent-team team reject <team>` | Scoped manual-gate rejection |
| `agent-team team unblock <team>` | Answer blocked team-owned pipeline workers; add `--dry-run --commands` to print the matching apply command |
| `agent-team team skip <team> --step <id>` | Scoped intentional step skip |
| `agent-team team cancel <team>` | Cancel non-terminal team pipeline jobs without stopping instances |
| `agent-team team adopt <team> <job-id>` | Adopt a live external process for a job after verifying team ownership; output includes scoped follow-up actions and `--commands` |
| `agent-team team retry <team>` | Scoped failed-step retry, honoring step `max_attempts` caps; add `--dry-run --commands` for the apply command or `--dispatch --wait-next-state`/`--wait-step` for stage-aware recovery handoff |
| `agent-team team cleanup <team>` | Scoped job cleanup, optionally verifying PRs with `gh` |
| `agent-team team snapshot <team>` | Scoped diagnostic artifact with command provenance, queue/outbox quarantine inventory, and formatted summary fields |

## Intake and Schedules

| Command | Purpose |
| --- | --- |
| `agent-team schedule ls` | List schedules |
| `agent-team schedule due` | Show due schedules; add `--commands` to print the dry-run fire preview command when anything is due |
| `agent-team schedule next` | Show upcoming schedules; add `--commands` to print the dry-run fire preview command when the forecast includes due work |
| `agent-team schedule fire` | Fire due schedules; add `--wait-next-state`/`--wait-step` for schedule-created pipeline jobs |
| `agent-team schedule run <name>` | Publish one schedule event; add `--wait-next-state`/`--wait-step` for schedule-created pipeline jobs |
| `agent-team intake linear` | Normalize Linear payload |
| `agent-team intake github` | Normalize GitHub payload, reconcile jobs, advance PR-gated work with `--wait-next-state`/`--wait-step`, and optionally verify PR cleanup |
| `agent-team intake schedule` | Normalize schedule payload; add `--wait-next-state`/`--wait-step` for schedule-created pipeline jobs |
| `agent-team intake serve` | Run local intake server with optional GitHub job reconciliation |
| `agent-team intake service systemd|launchd|compose|kubernetes` | Print a systemd unit, launchd plist, compose service, or Kubernetes manifests for `intake serve` |
| `agent-team intake summary` | Summarize delivery history; add `--commands` to print recovery/prune commands only |
| `agent-team intake duplicates` | List duplicate provider request IDs; add `--commands` to print matching delivery-inspection commands only |
| `agent-team intake deliveries` | Inspect delivery rows; add `--commands` to print replay commands for matching rows only |
| `agent-team intake replay` | Replay failed deliveries |
| `agent-team intake doctor` | Validate delivery history; add `--commands` to print warning follow-up commands only |
| `agent-team intake prune` | Drop old delivery rows |

## Diagnostics

| Command | Purpose |
| --- | --- |
| `agent-team overview` | Compact state and action hints, including queue/job/outbox quarantine recovery; JSON includes structured `action_details`, and `--commands` prints only action commands |
| `agent-team next` | Recommended next commands with structured JSON `action_details` or one command per line with `--commands`; filter quarantine recovery with `--reason quarantined`, `queue_quarantined`, `job_quarantined`, or `outbox_quarantined` |
| `agent-team health` | Scriptable health check with queue, job, and outbox quarantine warnings plus scoped recovery actions; add `--commands` for one remediation command per line |
| `agent-team monitor` | Operator dashboard with health, job/queue/outbox recovery, inbox, instances, and resources |
| `agent-team watch` | Continuous monitor with health, job/queue/outbox recovery, inbox, instances, and resources |
| `agent-team snapshot` | Redacted diagnostic artifact with command provenance, job/queue/outbox quarantine inventory, and formatted summary fields |
| `agent-team snapshot diff <before.json> <after.json>` | Compare saved global, team, pipeline, or job diagnostic artifacts, or compare one saved artifact with the current repo state using `--current-after` / `--current-before`; includes provenance, git/runtime context, health, plan, triage, next-action hints, job state, job quarantine, inboxes, outbox, outbox quarantine, queue, queue quarantine, schedules, intake, events, pipeline state, saved JSON output, action filters, summary-only output, sorted/bounded detail rows, and formatted counters for scripts |
| `agent-team repair` | Start/reconcile/timeout/retry/tick recovery loop; timeout repair accepts filters, and failed-step retry accepts pipeline/step filters, `--retry-force`, runtime overrides, and `--wait-next-state`/`--wait-step` stage-aware bounded handoff |

## Communication

| Command | Purpose |
| --- | --- |
| `agent-team send` | Send mailbox message; add `--dry-run --commands` to print the matching send apply command |
| `agent-team inbox` | Inspect mailbox summaries, show messages, acknowledge cursors, and prune old acknowledged entries while preserving unread state; `inbox ls --sort/--limit` focuses large mailbox sets, `inbox prune --limit` bounds compaction, `inbox ls/show --commands` print follow-ups, and dry-run ack/prune commands can print apply commands |
| `agent-team channels` | List channels; sort/limit large channel sets or emit `--json` / `--format` for scripts |
| `agent-team channel show <name>` | Show channel summary and recent messages; set `--tail`, or emit `--json` / `--format` for scripts |
| `agent-team channel publish <name>` | Publish channel message; emit `--json` / `--format` for scripts |
| `agent-team channel rm <name>` | Remove channel state; use `--dry-run --commands` to print the force apply command, or emit `--json` / `--format` |
| `agent-team event publish <type>` | Publish a raw topology event |
