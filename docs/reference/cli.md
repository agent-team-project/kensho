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
| `agent-team init [ref]` | Instantiate a template into `.agent_team/` |
| `agent-team template ls` | List bundled and cached templates |
| `agent-team template show [ref]` | Show a template manifest |
| `agent-team template pull <ref>` | Cache a template |
| `agent-team template rm <ref>` | Remove a cached template |
| `agent-team template smoke [ref]` | Render a template in a temp repo and validate it; add `--strict-runtime` for CI runtime checks |
| `agent-team template run <ref> <agent>` | One-shot init plus run in a temp or target dir |
| `agent-team upgrade --check|--apply` | Compare current template lock to target or apply clean template changes |
| `agent-team doctor` | Validate local layout, runtime availability, and workflow runtime defaults |
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
| `agent-team runtime probe` | Probe runtime selection, daemon readiness, Codex doctor health, optional Codex exec readiness, and write diagnostics with `--output`; aliases: `doctor`, `check` |
| `agent-team adopt <instance>` | Adopt a live external runtime process into daemon metadata; `runtime adopt` remains available |
| `agent-team resume-plan` | Show resume, attach, and log fallback commands from daemon metadata; filter by `--action`/`--runtime-stale`/`--unhealthy` or summarize with `--summary`; `runtime resume-plan` remains available |

## Instance Lifecycle

| Command | Purpose |
| --- | --- |
| `agent-team run <agent>` | Launch an agent directly or through daemon with `--detach`; use `--runtime` for one-off Claude/Codex selection |
| `agent-team start [instances...]` | Start/resume persistent or selected instances |
| `agent-team stop [instances...]` | Stop selected instances |
| `agent-team kill [instances...]` | Force-stop selected instances |
| `agent-team restart [instances...]` | Restart/resume selected instances |
| `agent-team ps` | List instance rows; filter mixed-runtime views with `--runtime` |
| `agent-team inspect [instances...]` | Show runtime and state detail |
| `agent-team logs [instance]` | Read/follow instance logs, use `--last-message` for clean Codex final responses, or `--clean` to hide known Codex diagnostics |
| `agent-team stats` | Show CPU/RSS data |
| `agent-team attach <instance>` | Interactive runtime resume handoff; `exec` is a Docker-like alias |
| `agent-team wait [instances...]` | Wait for lifecycle or phase conditions |
| `agent-team rm [instances...]` | Remove state and metadata |
| `agent-team prune` | Remove finished old metadata/state |

Shortcuts:

| Shortcut | Equivalent |
| --- | --- |
| `agent-team up` | `agent-team start` |
| `agent-team down` | `agent-team stop` |
| `agent-team ls` | `agent-team ps` |
| `agent-team top` | `agent-team stats` |
| `agent-team exec` | `agent-team attach` |

Collection groups also accept natural plural aliases: `jobs`, `pipelines`, `queues`, `schedules`, and `teams`.

## Topology and Convergence

| Command | Purpose |
| --- | --- |
| `agent-team topology show` | Render loaded topology |
| `agent-team topology graph` | Render full topology graph |
| `agent-team topology summary` | Summarize topology health |
| `agent-team topology reload` | Reload daemon topology, with JSON/template output for scripts |
| `agent-team plan` | Preview desired instance state |
| `agent-team sync` | Reload, reconcile, start/resume desired instances |
| `agent-team tick` | Run one maintenance cycle or loop; use `--runtime` for advanced steps |
| `agent-team drain` | Run maintenance cycles until idle; use `--runtime` for advanced steps |
| `agent-team reload` | Top-level daemon topology reload |

## Jobs

| Command | Purpose |
| --- | --- |
| `agent-team job create <ticket>` | Create a durable job |
| `agent-team job ls` | List jobs; filter held state, hold deadlines, and mixed-runtime ownership; sort rows and cap output with `--limit` |
| `agent-team job show <job-id>` | Show job detail, runtime metadata, queue, quarantine, status previews, and actions |
| `agent-team job wait <job-id>` | Wait for lifecycle status or last event with `--event` |
| `agent-team job resume-plan <job-id>` | Show runtime resume, attach, and log fallback commands for one job |
| `agent-team job snapshot <job-id>` | Capture one job's post-mortem metadata, events, queue ownership, state files, and optional log tails |
| `agent-team job explain <job-id>` | Explain or watch every pipeline step's readiness, blockers, gates, and next actions |
| `agent-team job approve <job-id>` | Approve a blocked manual pipeline gate, optionally advancing it |
| `agent-team job reject <job-id>` | Reject a blocked manual pipeline gate and mark it failed |
| `agent-team job dispatch <job-id>` | Dispatch a job; use `--runtime` for one-off Claude/Codex selection |
| `agent-team job send <job-id>` | Send message to job instance |
| `agent-team job note <job-id>` | Append an operator or automation note to the job audit log |
| `agent-team job block <job-id>` | Mark a job blocked and record the reason |
| `agent-team job unblock <job-id>` | Send answer and mark blocked job running |
| `agent-team job retry <job-id>` | Reopen/retry a failed or closed job |
| `agent-team job update <job-id>` | Update job metadata; use `--advance --dry-run` to preview unblocked pipeline steps |
| `agent-team job hold <job-id>` | Pause readiness/advance automation without changing lifecycle status; use `--all` for repo-wide freezes, and add `--for` or `--until` for a deadline |
| `agent-team job release <job-id>` | Resume readiness/advance automation for a held job; use `--all --expired` for elapsed time-boxed holds |
| `agent-team job close <job-id>` | Mark done or failed; use `--dry-run` to preview |
| `agent-team job cancel <job-id>` | Fail a job as cancelled, optionally stopping its owner |
| `agent-team job timeout <job-id> or --all` | Mark stale running job steps or stale step-less running jobs failed; add `--pipeline` or `--target-agent` with `--all` to scope a sweep |
| `agent-team job cleanup <job-id>` | Remove job-owned worktree/branch after merge, optionally verifying the PR with `gh` |
| `agent-team job triage` | Find jobs needing attention |
| `agent-team job ready` | List or watch next pipeline steps; filter by `--step`, sort by `--sort`, and cap rows with `--limit` |
| `agent-team job advance <job-id>` | Advance pipeline step |
| `agent-team job events <job-id>` | Show job event log |

## Job Queue

| Command | Purpose |
| --- | --- |
| `agent-team job queue <job-id>` | List or watch active queue entries owned by a job; filter queued dispatches with `--runtime`, sort rows with `--sort`, and cap output with `--limit` |
| `agent-team job queue show <job-id> <id>` | Inspect one active queue item owned by a job |
| `agent-team job queue retry <job-id> <id>` | Retry one job-owned queue item |
| `agent-team job queue retry <job-id> --all` | Retry matching job-owned queue items; filter, sort, and limit batch actions with `--runtime`, `--sort`, and `--limit` |
| `agent-team job queue drop <job-id> <id>` | Drop one job-owned queue item |
| `agent-team job queue drop <job-id> --all` | Drop matching job-owned queue items; filter, sort, and limit batch actions with `--runtime`, `--sort`, and `--limit` |
| `agent-team job queue prune <job-id>` | Age-prune job-owned queue entries; filter and limit prune candidates with `--runtime`, `--ready`, and `--limit` |
| `agent-team job queue quarantine <job-id>` | List job-owned quarantined queue files; sort rows with `--sort` and cap output with `--limit` |
| `agent-team job queue quarantine show <job-id> <path>` | Inspect one preserved file |
| `agent-team job queue quarantine restore <job-id> <path>` | Restore one preserved file |
| `agent-team job queue quarantine restore <job-id> --all` | Restore matching restorable files; sort and cap batch actions with `--sort` and `--limit` |
| `agent-team job queue quarantine drop <job-id> <path>` | Drop one preserved file |
| `agent-team job queue quarantine drop <job-id> --all` | Drop matching preserved files; sort and cap batch actions with `--sort` and `--limit` |

## Global Queue

| Command | Purpose |
| --- | --- |
| `agent-team queue ls` | List active queue entries; filter queued dispatches with `--runtime`, sort rows with `--sort`, and cap output with `--limit` |
| `agent-team queue show <id>` | Inspect one active queue item, including resolved runtime metadata |
| `agent-team queue drain` | Dispatch ready pending entries |
| `agent-team queue retry <id>` | Retry one entry |
| `agent-team queue retry --all` | Retry matching entries; filter, sort, and limit batch actions with `--runtime`, `--sort`, and `--limit` |
| `agent-team queue drop <id>` | Drop one entry |
| `agent-team queue drop --all` | Drop matching entries; filter, sort, and limit batch actions with `--runtime`, `--sort`, and `--limit` |
| `agent-team queue prune` | Age-prune entries; filter and limit prune candidates with `--runtime`, `--ready`, and `--limit` |
| `agent-team queue doctor` | Validate queue files |
| `agent-team queue quarantine ls` | List quarantined queue files; sort rows with `--sort` and cap output with `--limit` |
| `agent-team queue quarantine show <path>` | Inspect quarantined queue file |
| `agent-team queue quarantine restore <path>` | Restore one preserved file |
| `agent-team queue quarantine drop <path>` | Drop one preserved file |
| `agent-team queue quarantine restore --all` | Restore matching restorable files; sort and cap batch actions with `--sort` and `--limit` |
| `agent-team queue quarantine drop --all` | Drop matching preserved files; sort and cap batch actions with `--sort` and `--limit` |

## Pipelines

| Command | Purpose |
| --- | --- |
| `agent-team pipeline ls` | List pipeline declarations |
| `agent-team pipeline show <pipeline>` | Show one declaration |
| `agent-team pipeline graph <pipeline>` | Render text, Mermaid, DOT, or JSON step graphs |
| `agent-team pipeline doctor --all` | Validate workflows; add `--strict-runtime` to fail on unavailable step runtime defaults |
| `agent-team pipeline run <pipeline> <ticket>` | Create pipeline job; `--dispatch` accepts workspace and runtime overrides |
| `agent-team pipeline status` | Summarize or watch pipeline jobs plus owned queue/quarantine counts; sort rows and cap output with `--limit` |
| `agent-team pipeline explain <pipeline>` | Expand or watch pipeline jobs as per-step readiness, blockers, gates, and actions; add `--step` to focus one stage |
| `agent-team pipeline snapshot <pipeline>` | Capture one pipeline's status, explained jobs, queue ownership, and dry-run advance previews |
| `agent-team pipeline next` | Print or watch recommended pipeline actions |
| `agent-team pipeline wait <pipeline>` | Wait for pipeline jobs to reach a lifecycle status or event |
| `agent-team pipeline jobs <pipeline>` | List, summarize, or watch pipeline jobs; filter held state, hold deadlines, mixed-runtime ownership, sort rows, and cap output with `--limit` |
| `agent-team pipeline ready` | List or watch ready steps; filter by `--step`, sort by `--sort`, and cap rows with `--limit` |
| `agent-team pipeline hold <pipeline>` | Hold matching pipeline jobs without changing lifecycle status; add `--for` or `--until` for a deadline |
| `agent-team pipeline release <pipeline>` | Release held jobs in a pipeline; add `--expired` to release only elapsed deadlines |
| `agent-team pipeline advance <pipeline>` | Advance ready work; use `--workspace` and `--runtime` for dispatched steps |
| `agent-team pipeline approve <pipeline>` | Approve blocked manual gates |
| `agent-team pipeline reject <pipeline>` | Reject blocked manual gates |
| `agent-team pipeline skip <pipeline> --step <id>` | Mark matching non-running steps intentionally skipped |
| `agent-team pipeline cancel <pipeline>` | Cancel non-terminal pipeline jobs without stopping instances |
| `agent-team pipeline resume-plan <pipeline>` | Pipeline-scoped runtime recovery commands; filter by `--action`/`--runtime-stale`/`--unhealthy` or summarize with `--summary` |
| `agent-team pipeline send <pipeline>` | Send a mailbox message to pipeline-owned daemon-known instances |
| `agent-team pipeline logs <pipeline>` | Read daemon-captured logs for pipeline-owned instances |
| `agent-team pipeline events <pipeline>` | Read lifecycle events for pipeline-owned instances |
| `agent-team pipeline cleanup <pipeline>` | Scoped job cleanup for done jobs in one pipeline |
| `agent-team pipeline queue <pipeline>` | List, inspect, retry, drop, prune, or recover active/quarantined queue items owned by one pipeline; quarantine lists and batch actions support `--sort` and `--limit` |
| `agent-team pipeline timeout <pipeline>` | Mark stale running steps failed; add `--target-agent` to scope by role |
| `agent-team pipeline retry <pipeline>` | Retry failed steps, honoring step `max_attempts` caps; add `--force` for an explicit override |
| `agent-team pipeline repair <pipeline>` | Scoped repair loop for one pipeline: queue retry, optional timeout/retry, and ready-step advance |

## Teams

| Command | Purpose |
| --- | --- |
| `agent-team team ls` | List teams |
| `agent-team team show <team>` | Show team declaration |
| `agent-team team graph <team>` | Render team-owned instance, schedule, and pipeline wiring |
| `agent-team team doctor --all` | Validate team-owned workflow wiring; add `--strict-runtime` to fail on unavailable step runtime defaults |
| `agent-team team overview <team>` | Scoped operator overview |
| `agent-team team health <team>` | Scoped health |
| `agent-team team resume-plan <team>` | Team-scoped runtime recovery commands; filter by `--action`/`--runtime-stale`/`--unhealthy` or summarize with `--summary`; `team runtime resume-plan` remains available |
| `agent-team team status <team>` | Scoped status |
| `agent-team team monitor <team>` | Scoped dashboard |
| `agent-team team run <team> <ticket>` | Create a team-owned job; `--dispatch` accepts workspace and runtime overrides |
| `agent-team team jobs <team>` | Scoped job list, summary, or watch view; filter held state, mixed-runtime ownership, and cap output with `--limit` |
| `agent-team team tick <team>` | Scoped maintenance cycle; use `--workspace` and `--runtime` for advanced steps |
| `agent-team team repair <team>` | Scoped repair loop, including stale-work timeout with `--timeout-jobs`; failed-step retry accepts pipeline/step filters, `--retry-force`, and workspace/runtime overrides |
| `agent-team team queue <team>` | Scoped queue list; filter queued dispatches with `--runtime`, sort rows with `--sort`, and cap output with `--limit` |
| `agent-team team queue show <team> <id>` | Inspect one active queue item owned by a team |
| `agent-team team queue retry <team> --all` | Retry matching team-owned entries; filter, sort, and limit batch actions with `--runtime`, `--sort`, and `--limit` |
| `agent-team team queue drop <team> --all` | Drop matching team-owned entries; filter, sort, and limit batch actions with `--runtime`, `--sort`, and `--limit` |
| `agent-team team queue prune <team>` | Age-prune team-owned entries; filter and limit prune candidates with `--runtime`, `--ready`, and `--limit` |
| `agent-team team queue quarantine <team>` | Scoped quarantine list; sort rows with `--sort` and cap output with `--limit` |
| `agent-team team pipelines <team>` | List or watch team-owned pipeline status rows with queue/quarantine counts; sort rows and cap output with `--limit` |
| `agent-team team explain <team>` | Expand or watch team-owned pipeline jobs as per-step diagnostics; add `--step` to focus one stage |
| `agent-team team ready <team>` | List or watch scoped ready pipeline steps; filter by `--step`, sort by `--sort`, and cap rows with `--limit` |
| `agent-team team hold <team>` | Hold matching pipeline jobs owned by a team; add `--for` or `--until` for a deadline |
| `agent-team team release <team>` | Release held pipeline jobs owned by a team; add `--expired` to release only elapsed deadlines |
| `agent-team team timeout <team>` | Timeout stale team pipeline steps; add `--jobs` for stale step-less team jobs and `--target-agent` to scope by role |
| `agent-team team advance <team>` | Scoped pipeline advance; use `--runtime` for dispatched steps |
| `agent-team team approve <team>` | Scoped manual-gate approval |
| `agent-team team reject <team>` | Scoped manual-gate rejection |
| `agent-team team skip <team> --step <id>` | Scoped intentional step skip |
| `agent-team team cancel <team>` | Cancel non-terminal team pipeline jobs without stopping instances |
| `agent-team team retry <team>` | Scoped failed-step retry, honoring step `max_attempts` caps; add `--force` for an explicit override |
| `agent-team team cleanup <team>` | Scoped job cleanup, optionally verifying PRs with `gh` |
| `agent-team team snapshot <team>` | Scoped diagnostic artifact |

## Intake and Schedules

| Command | Purpose |
| --- | --- |
| `agent-team schedule ls` | List schedules |
| `agent-team schedule due` | Show due schedules |
| `agent-team schedule next` | Show upcoming schedules |
| `agent-team schedule fire` | Fire due schedules |
| `agent-team schedule run <name>` | Publish one schedule event |
| `agent-team intake linear` | Normalize Linear payload |
| `agent-team intake github` | Normalize GitHub payload, reconcile jobs, and optionally verify PR cleanup |
| `agent-team intake schedule` | Normalize schedule payload |
| `agent-team intake serve` | Run local intake server with optional GitHub job reconciliation |
| `agent-team intake service systemd|launchd|compose|kubernetes` | Print a systemd unit, launchd plist, compose service, or Kubernetes manifests for `intake serve` |
| `agent-team intake summary` | Summarize delivery history |
| `agent-team intake duplicates` | List duplicate provider request IDs |
| `agent-team intake deliveries` | Inspect delivery rows |
| `agent-team intake replay` | Replay failed deliveries |
| `agent-team intake doctor` | Validate delivery history |
| `agent-team intake prune` | Drop old delivery rows |

## Diagnostics

| Command | Purpose |
| --- | --- |
| `agent-team overview` | Compact state and action hints; JSON includes structured `action_details` |
| `agent-team next` | Recommended next commands with structured JSON `action_details` |
| `agent-team health` | Scriptable health check |
| `agent-team monitor` | Operator dashboard |
| `agent-team watch` | Continuous monitor |
| `agent-team snapshot` | Redacted diagnostic artifact |
| `agent-team snapshot diff <before.json> <after.json>` | Compare saved diagnostic artifacts, including instances, jobs, queue, schedules, intake, events, and pipeline state |
| `agent-team repair` | Start/reconcile/timeout/retry/tick recovery loop; timeout repair accepts filters, and failed-step retry accepts pipeline/step filters, `--retry-force`, and runtime overrides |

## Communication

| Command | Purpose |
| --- | --- |
| `agent-team send` | Send mailbox message |
| `agent-team channels` | List channels |
| `agent-team channel show <name>` | Show channel messages |
| `agent-team channel publish <name>` | Publish channel message |
| `agent-team channel rm <name>` | Remove channel state |
| `agent-team event publish <type>` | Publish a raw topology event |
