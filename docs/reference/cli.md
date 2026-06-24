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
| `agent-team template smoke [ref]` | Render a template in a temp repo and validate it |
| `agent-team template run <ref> <agent>` | One-shot init plus run in a temp or target dir |
| `agent-team upgrade --check|--apply` | Compare current template lock to target or apply clean template changes |
| `agent-team doctor` | Validate local layout and runtime availability |
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
| `agent-team runtime ls` | List supported runtime profiles, availability, and capabilities |
| `agent-team runtime probe` | Probe runtime selection, daemon readiness, Codex doctor health, and optional Codex exec readiness before dispatching work |

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
| `agent-team attach <instance>` | Interactive runtime resume handoff |
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

## Topology and Convergence

| Command | Purpose |
| --- | --- |
| `agent-team topology show` | Render loaded topology |
| `agent-team topology graph` | Render full topology graph |
| `agent-team topology summary` | Summarize topology health |
| `agent-team topology reload` | Reload daemon topology |
| `agent-team plan` | Preview desired instance state |
| `agent-team sync` | Reload, reconcile, start/resume desired instances |
| `agent-team tick` | Run one maintenance cycle or loop |
| `agent-team reload` | Top-level daemon topology reload |

## Jobs

| Command | Purpose |
| --- | --- |
| `agent-team job create <ticket>` | Create a durable job |
| `agent-team job ls` | List jobs; filter mixed-runtime ownership with `--runtime` |
| `agent-team job show <job-id>` | Show job detail, runtime metadata, queue, quarantine, status previews, and actions |
| `agent-team job snapshot <job-id>` | Capture one job's post-mortem metadata, events, queue ownership, state files, and optional log tails |
| `agent-team job dispatch <job-id>` | Dispatch a job; use `--runtime` for one-off Claude/Codex selection |
| `agent-team job send <job-id>` | Send message to job instance |
| `agent-team job unblock <job-id>` | Send answer and mark blocked job running |
| `agent-team job retry <job-id>` | Reopen/retry a failed or closed job |
| `agent-team job close <job-id>` | Mark done or failed |
| `agent-team job cleanup <job-id>` | Remove job-owned worktree/branch after merge, optionally verifying the PR with `gh` |
| `agent-team job triage` | Find jobs needing attention |
| `agent-team job ready` | List next pipeline steps |
| `agent-team job advance <job-id>` | Advance pipeline step |
| `agent-team job events <job-id>` | Show job event log |

## Job Queue

| Command | Purpose |
| --- | --- |
| `agent-team job queue <job-id>` | List active queue entries owned by a job; filter queued dispatches with `--runtime` |
| `agent-team job queue show <job-id> <id>` | Inspect one active queue item owned by a job |
| `agent-team job queue retry <job-id> <id>` | Retry one job-owned queue item |
| `agent-team job queue retry <job-id> --all` | Retry matching job-owned queue items; filter batch actions with `--runtime` |
| `agent-team job queue drop <job-id> <id>` | Drop one job-owned queue item |
| `agent-team job queue drop <job-id> --all` | Drop matching job-owned queue items; filter batch actions with `--runtime` |
| `agent-team job queue prune <job-id>` | Age-prune job-owned queue entries; filter prune candidates with `--runtime` |
| `agent-team job queue quarantine <job-id>` | List job-owned quarantined queue files |
| `agent-team job queue quarantine show <job-id> <path>` | Inspect one preserved file |
| `agent-team job queue quarantine restore <job-id> <path>` | Restore one preserved file |
| `agent-team job queue quarantine restore <job-id> --all` | Restore matching restorable files |
| `agent-team job queue quarantine drop <job-id> <path>` | Drop one preserved file |
| `agent-team job queue quarantine drop <job-id> --all` | Drop matching preserved files |

## Global Queue

| Command | Purpose |
| --- | --- |
| `agent-team queue ls` | List active queue entries; filter queued dispatches with `--runtime` |
| `agent-team queue show <id>` | Inspect one active queue item, including resolved runtime metadata |
| `agent-team queue drain` | Dispatch ready pending entries |
| `agent-team queue retry <id>` | Retry one entry |
| `agent-team queue retry --all` | Retry matching entries; filter batch actions with `--runtime` |
| `agent-team queue drop <id>` | Drop one entry |
| `agent-team queue drop --all` | Drop matching entries; filter batch actions with `--runtime` |
| `agent-team queue prune` | Age-prune entries; filter prune candidates with `--runtime` |
| `agent-team queue doctor` | Validate queue files |
| `agent-team queue quarantine ls` | List quarantined queue files |
| `agent-team queue quarantine show <path>` | Inspect quarantined queue file |
| `agent-team queue quarantine restore <path>` | Restore one preserved file |
| `agent-team queue quarantine drop <path>` | Drop one preserved file |

## Pipelines

| Command | Purpose |
| --- | --- |
| `agent-team pipeline ls` | List pipeline declarations |
| `agent-team pipeline show <pipeline>` | Show one declaration |
| `agent-team pipeline graph <pipeline>` | Render text, Mermaid, DOT, or JSON step graphs |
| `agent-team pipeline doctor --all` | Validate workflows |
| `agent-team pipeline run <pipeline> <ticket>` | Create pipeline job; `--dispatch` accepts runtime overrides |
| `agent-team pipeline status` | Summarize pipeline jobs |
| `agent-team pipeline next` | Print recommended pipeline actions |
| `agent-team pipeline jobs <pipeline>` | List or summarize pipeline jobs; filter mixed-runtime ownership with `--runtime` |
| `agent-team pipeline ready` | List ready steps |
| `agent-team pipeline advance <pipeline>` | Advance ready work; use `--runtime` for dispatched steps |
| `agent-team pipeline approve <pipeline>` | Approve blocked manual gates |
| `agent-team pipeline retry <pipeline>` | Retry failed steps |

## Teams

| Command | Purpose |
| --- | --- |
| `agent-team team ls` | List teams |
| `agent-team team show <team>` | Show team declaration |
| `agent-team team graph <team>` | Render team-owned instance, schedule, and pipeline wiring |
| `agent-team team overview <team>` | Scoped operator overview |
| `agent-team team health <team>` | Scoped health |
| `agent-team team status <team>` | Scoped status |
| `agent-team team monitor <team>` | Scoped dashboard |
| `agent-team team run <team> <ticket>` | Create a team-owned job; `--dispatch` accepts runtime overrides |
| `agent-team team jobs <team>` | Scoped job list or summary; filter mixed-runtime ownership with `--runtime` |
| `agent-team team tick <team>` | Scoped maintenance cycle |
| `agent-team team repair <team>` | Scoped repair loop |
| `agent-team team queue <team>` | Scoped queue list; filter queued dispatches with `--runtime` |
| `agent-team team queue show <team> <id>` | Inspect one active queue item owned by a team |
| `agent-team team queue retry <team> --all` | Retry matching team-owned entries; filter batch actions with `--runtime` |
| `agent-team team queue drop <team> --all` | Drop matching team-owned entries; filter batch actions with `--runtime` |
| `agent-team team queue prune <team>` | Age-prune team-owned entries; filter prune candidates with `--runtime` |
| `agent-team team queue quarantine <team>` | Scoped quarantine list |
| `agent-team team ready <team>` | Scoped ready pipeline steps |
| `agent-team team advance <team>` | Scoped pipeline advance; use `--runtime` for dispatched steps |
| `agent-team team approve <team>` | Scoped manual-gate approval |
| `agent-team team retry <team>` | Scoped failed-step retry |
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
| `agent-team repair` | Start/reconcile/retry/tick recovery loop |

## Communication

| Command | Purpose |
| --- | --- |
| `agent-team send` | Send mailbox message |
| `agent-team channels` | List channels |
| `agent-team channel show <name>` | Show channel messages |
| `agent-team channel publish <name>` | Publish channel message |
| `agent-team channel rm <name>` | Remove channel state |
| `agent-team event publish <type>` | Publish a raw topology event |
