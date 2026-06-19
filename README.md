# agent-team

A CLI for declaring teams of LLM agents and skills, then instantiating them into any repo from a parameterized template. Each **agent** is a directory under `.agent_team/agents/`; `agent-team run <agent>` launches the selected runtime with the team context registered for that session.

The model is templates-as-images: a template is a versioned, parameterized directory of agents + skills. You pull it (or use the one bundled in the binary), supply parameters once at `init`, and the resolved tree lands in `.agent_team/`. Multiple repos share the same template with different parameters; one repo can host multiple instances of the same agent.

A starter "software engineering team" template (a `ticket-manager`, a `manager`, ephemeral `worker`s, plus Linear / PR / assign-worker skills) is bundled as the default. Use it as-is, parameterize it, or write your own template and point `init` at it.

**Status**: pre-v1. Public API is unstable.

## Vocabulary

- **template** — a versioned, parameterized directory of agents + skills with a `template.toml` manifest. Bundled in the binary, or fetched from a local path / git URL into a cache.
- **agent** — a definition. A directory at `.agent_team/agents/<name>/` containing `agent.md` (frontmatter + prompt) and `config.toml` (skill assignment). Authored, static, reusable.
- **instance** — a named runtime spawn of an agent. Identified by the `--name=` flag at spawn time. One agent can have many instances; each has its own state dir.
- **workspace** — the working directory an instance operates in. For code-writing agents (the bundled `worker`): a fresh git worktree per spawn. For others: the repo root.
- **state** — persistent per-instance files (journal, goals, progress) at `.agent_team/state/<instance-name>/`.

## Install

`agent-team` ships as two single-file Go binaries: the user-facing `agent-team` CLI and the per-repo `agent-teamd` daemon. Pick whichever install path fits your setup.

**1. `go install`** (works today; needs Go 1.22+):

```sh
go install github.com/jamesaud/agent-team/cmd/agent-team@latest
go install github.com/jamesaud/agent-team/cmd/agent-teamd@latest
```

This drops both binaries into `$(go env GOPATH)/bin` (typically `~/go/bin`). Make sure that directory is on your `PATH`.

**2. Prebuilt release tarball** (works after the first tagged release):

Grab the archive for your OS/arch from [GitHub Releases](https://github.com/jamesaud/agent-team/releases), unpack, and put both binaries on your `PATH`:

```sh
# example for macOS arm64 — adjust the URL for your platform
curl -fsSL https://github.com/jamesaud/agent-team/releases/latest/download/agent-team_<version>_darwin_arm64.tar.gz \
  | tar -xz -C /usr/local/bin agent-team agent-teamd
```

**3. Homebrew** *(coming soon — tap not yet published)*:

```sh
# not yet available — pending tap repo creation; see SQU-31
# brew install jamesaud/agent-team/agent-team
```

Verify any install with:

```sh
agent-team --version
```

## Lifecycle

```
template pull  →  init  →  run  →  upgrade
```

1. **(Optional) `template pull`** — fetch a template into the local cache. Skip this for the bundled default.
2. **`init`** — instantiate a template into the current repo. Resolves required parameters (`--set k=v` or interactive prompt), writes `.agent_team/` with `.tmpl` files rendered, and records template provenance in `.template.lock`.
3. **`run`** — launch a Claude Code session as one of the agents.
4. **`upgrade`** — `upgrade --check` compares the repo's template lock to a resolved ref today; full three-way upgrade/apply is future work.

The full design is in [`documentation/templates.md`](./documentation/templates.md).

## Quickstart

```sh
mkdir my-app && cd my-app
agent-team init \
    --set linear.team_id=<your-team-uuid> \
    --set linear.ticket_prefix=APP
```

(Required parameters are prompted for if you omit them; pass `--no-input` to fail-fast in CI.)

`init` writes a starter `.agent_team/` into the current repo:

```
.agent_team/
├── .template.lock             # template ref + source content hash
├── config.toml                # resolved parameter values, repo-wide
├── agents/
│   ├── <name>/
│   │   ├── agent.md           # frontmatter + prompt body
│   │   ├── config.toml        # [skills].extra: which skills this agent uses
│   │   └── skills/            # optional agent-private skills
│   └── ...
├── skills/
│   ├── <name>/SKILL.md        # shared skills (referenced by any agent)
│   └── ...
└── state/                     # per-instance state, written at runtime
    └── <instance-name>/       # journal.md, goals.md, etc.
```

Edit anything you like, then:

```sh
agent-team run manager     # or any other agent name from .agent_team/agents/
```

…and you're in a Claude Code session as that agent, with the rest of the team available as subagents it can dispatch.

## One-shot run

For try-out, CI, or a fresh sandbox — anywhere the two-step `init` + `run` is friction — collapse both into a single command:

```sh
agent-team template run bundled manager \
    --set linear.team_id=<your-team-uuid> \
    --set linear.ticket_prefix=APP \
    -p "kickoff message"
```

This instantiates the template into a tempdir under `~/.agent-team/runs/<timestamp>-<agent>/` (or `$XDG_CACHE_HOME/agent-team/runs/...`), spawns the agent against it, and removes the tempdir when the agent exits. Pass `--keep` to preserve the tempdir, or `--target <dir>` to use a specific directory (which is always preserved). `--no-input` fails if required parameters are missing — useful in CI.

The daemon is bypassed; the selected runtime is exec'd directly. For long-lived setups where you want `instance ps` / `logs --follow` visibility, use `init` + `run` separately.

## Commands

```sh
agent-team init [<ref>] [--set k=v]... [--no-input] [--force]
                                                # instantiate a template into the current repo
agent-team start [<instance>...] [-q] [--all] [--latest | --last N] [--agent manager] [--status stopped] [--phase idle] [--stale] [--unhealthy] [--dry-run] [--summary] [--format '{{.Instance}} {{.Action}}'] [--ready-timeout 3s] [--wait --timeout 30s] [--attach --tail N|all] [--json]
                                                # start daemon, then start/resume persistent or daemon-known instances
agent-team stop [<instance>...] [-q] [--all] [--latest | --last N] [--agent manager] [--status running] [--phase idle] [--stale] [--unhealthy] [-f] [--rm] [--dry-run] [--summary] [--format '{{.Instance}} {{.Action}}'] [--wait --wait-timeout 30s] [--timeout 10s] [--json]
                                                # stop persistent instances, or all daemon-managed instances
agent-team kill [<instance>...] [-q] [--all] [--latest | --last N] [--agent manager] [--status running] [--phase idle] [--stale] [--unhealthy] [--rm] [--dry-run] [--summary] [--format '{{.Instance}} {{.Action}}'] [--timeout 2s] [--wait --wait-timeout 30s] [--json]
                                                # force-stop persistent instances, or all daemon-managed instances
agent-team restart [<instance>...] [-q] [--all] [--latest | --last N] [--agent manager] [--status running] [--phase idle] [--stale] [--unhealthy] [-f] [--dry-run] [--summary] [--format '{{.Instance}} {{.Action}}'] [--ready-timeout 3s] [--timeout 30s] [--wait --wait-timeout 30s] [--attach --tail N|all] [--json]
                                                # restart persistent or daemon-known instances
agent-team reload [--format '{{len .Topology.Instances}} {{.Reconcile.Changed}}'] [--json]
                                                # re-read instances.toml in the daemon and reconcile runtime metadata
agent-team topology show [--json] | summary [--json] | reload
                                                # inspect declared topology, summarize workflow/team route health, or reload the daemon view
agent-team plan [--json] [--summary] [--stop-extras] [--format '{{.Instance}} {{.Action}}'] [--agent manager] [--instance manager] [--status running] [--phase idle] [--action start]
                                                # preview desired instance state from topology and daemon metadata
agent-team sync [-q] [--dry-run] [--stop-extras] [--agent manager] [--instance manager] [--status unknown] [--phase idle] [--action start] [--summary] [--format '{{.Instance}} {{.Action}}'] [--ready-timeout 3s] [--wait --timeout 30s] [--json]
                                                # reload topology, reconcile metadata, start/resume persistent instances, and optionally stop running extras
agent-team tick [-w | --until-idle] [--interval 2s] [--max-cycles N] [--dry-run] [--preview-routes] [--skip-reconcile] [--skip-schedules] [--skip-drain] [--skip-advance] [--limit N] [--workspace auto|worktree|repo] [--format '{{.Queue.Dispatched}} {{len .Advance}}'] [--json]
                                                # run one maintenance cycle, watch cycles, or tick until no immediate job-status/schedule/queue/pipeline work remains
agent-team repair [--dry-run] [--preview-routes] [--jobs] [--skip-daemon] [--skip-queue] [--skip-tick] [--until-idle] [--limit N] [--workspace auto|worktree|repo] [--json]
                                                # recover common unhealthy orchestration state by starting/reconciling the daemon, retrying dead queue items, and ticking work
agent-team overview [-w] [--no-clear] [--interval 2s] [--schedule-limit N] [--format '{{.State}}'] [--json]
                                                # show a read-only operator overview with health, topology, jobs, queue, pipelines, schedules, and action hints
agent-team next [-w] [--no-clear] [--interval 2s] [--team delivery] [--limit N] [--schedule-limit N] [--format '{{.State}}'] [--json]
                                                # print the recommended next operator commands from the current overview
agent-team status [-w] [--no-clear] [--summary [--resources] [--plan [--stop-extras] [--action start]] [--events N [--event-action stop] [--since 10m]] [--strict-topology]] [--latest | --last N] [--format '{{.Instance}} {{.Status}}'] [--json] [--interval 2s] [--agent manager] [--instance manager] [--status running] [--phase idle] [--stale] [--unhealthy]
                                                # show/watch daemon health and current instance snapshot
agent-team daemon start [--detach=false] [--ready-timeout 3s] [--format '{{.Action}} {{.PID}}'] [--json]
                                                # boot agent-teamd; detached by default, foreground with --detach=false
agent-team daemon status [-q] [--wait [--down] --timeout 30s --interval 200ms] [--format '{{.Ready}} {{.PID}}'] [--json]
                                                # show agent-teamd process, API readiness, pid/socket/log paths
agent-team daemon logs [-f] [--tail N|all] [--since 10m] [--grep 'error|panic']
                                                # show/follow the agent-teamd daemon log
agent-team daemon stop [--timeout 5s] [--format '{{.Action}} {{.Changed}}'] [--json]
                                                # stop agent-teamd, escalating after the grace period
agent-team daemon restart [--timeout 5s] [--ready-timeout 3s] [--detach=false] [--format '{{.Action}} {{.Changed}}'] [--json]
                                                # bounce agent-teamd and reconcile existing daemon metadata
agent-team daemon reconcile [--format '{{.Changed}} {{len .Instances}}'] [--json]
                                                # refresh daemon metadata against the live process table without restarting
agent-team health [-q] [-w] [--no-clear] [--wait --timeout 30s] [--latest | --last N] [--format '{{.Healthy}} {{.Summary.Running}}'] [--jobs] [--agent manager] [--instance manager] [--status running] [--phase idle] [--stale] [--unhealthy] [--strict-topology] [--json]
                                                # check daemon, declarations, crashes, stale status, queue dead letters/quarantine, job health, and optional topology drift
agent-team monitor [-w] [--no-clear] [-a] [--summary [--resources]] [--plan [--stop-extras] [--action start]] [--jobs] [--schedules] [--latest | --last N] [--events N [--event-action stop] [--since 10m]] [--sort status|agent|phase|stale|unhealthy|started|stopped|exited|name] [--stats-sort cpu|mem|rss|status|agent|phase|stale|unhealthy|name] [--format '{{.Health.Healthy}} {{len .Instances}}'] [--json] [--interval 2s] [--strict-topology] [--agent manager] [--instance manager] [--status running] [--phase idle] [--stale] [--unhealthy]
                                                # combined health, instance, resource, event-history, and job-status snapshot; uses local metadata if the daemon is down
agent-team runtime [--format '{{.Runtime}} {{.Available}}'] [--json]
                                                # inspect selected LLM runtime profile, binary path, and supported capabilities
agent-team snapshot [--events N|-1] [--intake-deliveries N|-1] [--schedule-limit N] [--no-redact] [--json | --output snapshot.json]
                                                # capture a redacted read-only diagnostic report with health, plan, jobs, job-status previews, queue, schedules, runtime, and recent events
agent-team watch [--no-clear] [-a] [--summary [--resources]] [--plan [--stop-extras] [--action start]] [--jobs] [--schedules] [--latest | --last N] [--events N [--event-action stop] [--since 10m]] [--sort status|agent|phase|stale|unhealthy|started|stopped|exited|name] [--stats-sort cpu|mem|rss|status|agent|phase|stale|unhealthy|name] [--format '{{.Health.Healthy}} {{len .Instances}}'] [--json] [--interval 2s] [--strict-topology] [--agent manager] [--instance manager] [--status running] [--phase idle] [--stale] [--unhealthy]
                                                # continuously redraw the combined operator monitor
agent-team ps [-a] [-w] [--no-clear] [-q] [--summary] [--latest | --last N] [--sort status|agent|phase|stale|unhealthy|started|stopped|exited|name] [--json] [--format '{{.Instance}} {{.Status}}'] [--status running] [--phase blocked] [--stale] [--unhealthy] [--agent worker] [--instance worker-1]
                                                # list/watch/filter instances, or summarize lifecycle and phase counts
agent-team stats [<instance>...] [--all] [--latest | --last N] [-w] [--no-clear] [--summary] [--sort cpu|mem|rss|status|agent|phase|stale|unhealthy|name] [--json] [--format '{{.Instance}} {{.CPUPercent}} {{.RSS}}'] [--agent manager] [--instance manager] [--status running] [--phase idle] [--stale] [--unhealthy]
                                                # show/watch CPU and memory usage, or summarize resources and phases
agent-team inspect [<instance>...] [--all] [--latest | --last N] [--agent manager] [--instance manager] [--status running] [--phase idle] [--stale] [--unhealthy] [--format '{{.Instance}} {{if .Runtime}}{{.Runtime.Lifecycle}}{{end}}'] [--json]
                                                # show runtime metadata, state, status, and topology; reads persisted runtime metadata if the daemon is down
agent-team logs [<instance> | --latest | --last N] [--all | --agent manager] [--status running] [--phase idle] [--stale] [--unhealthy] [--no-prefix] [--list [--format '{{.Instance}} {{.LogPath}}'] [--json]] [--daemon] [-f] [--tail N|all] [--since 10m] [--grep 'error|panic']
                                                # list/show/follow instance or daemon logs; reads daemon-managed logs locally if the daemon is down
agent-team attach <instance> [--no-resume]
                                                # interactive Claude-compatible resume handoff; daemon resumes supervision afterward
agent-team events [-f] [--tail N] [--latest | --last N] [--since 24h] [--summary] [--format '{{.Action}} {{.Instance}}'] [--action dispatch] [--agent manager] [--instance manager] [--status running] [--phase idle] [--stale] [--unhealthy] [--json]
                                                # show/follow lifecycle events; phase/stale/unhealthy narrow by current status.toml; reads local history if the daemon is down
agent-team wait [<instance>...] [-q] [--all] [--latest | --last N] [--agent manager] [--status running] [--phase idle] [--stale] [--unhealthy] [--until terminal|running|stopped|exited|crashed|removed] [--until-phase done] [--timeout 5m] [--interval 500ms] [--dry-run] [--fail-on-crash] [--summary] [--format '{{.Instance}} {{.Status}} {{.Phase}}'] [--json]
                                                # wait for lifecycle or work-phase conditions, using persisted metadata if the daemon is down
agent-team send [<instance>] <message...> [--all] [--latest | --last N] [--agent manager] [--status running] [--phase idle] [--stale] [--unhealthy] [--from user] [--allow-missing] [--dry-run] [--format '{{.To}} {{.ID}}'] [--json]
                                                # send a daemon mailbox message; phase/stale/unhealthy selectors use current status.toml; appends locally if the daemon is down
agent-team dispatch <target> <ticket> [kickoff...] [--name <instance>] [--source <instance>] [--workspace auto|worktree|repo] [--kickoff "..."] [--kickoff-file <path>] [--dry-run] [--format <template>] [--json]
                                                # publish an agent.dispatch topology event, or preview local topology matches without a daemon
agent-team job create <ticket> [kickoff...] [--target worker] [--ticket-url <url>] [--pipeline ticket_to_pr] [--dispatch] [--workspace auto|worktree|repo] [--instance <name>] [--dry-run] [--json]
agent-team job ls [-w] [--summary] [--sort id|status|target|updated|created] [--status queued|running|blocked|done|failed] [--target-agent worker] [--pipeline name] [--instance name] [--json]
agent-team job show <job-id> [--events N|all] [--json] | triage [-w] [--min-severity critical|warning|info] [--reason queue_dead] [--no-clear] [--json] | next <job-id> [--json] | ready [--state ready|queued|all] [--json] | events <job-id> [-f] [--tail N|all] [--type closed] [--actor cli] [--since 24h] [--json]
agent-team job retry <job-id> [--dispatch] [--workspace auto|worktree|repo] [--dry-run] [--json]
                                                # reopen a failed/closed job and optionally dispatch another attempt immediately
agent-team job dispatch <job-id> [--source <instance>] [--workspace auto|worktree|repo] [--dry-run] [--format <template>] [--json]
agent-team job advance <job-id> [--workspace auto|worktree|repo] [--dry-run] [--json]
agent-team job step <job-id> <step-id> [--status done|failed|blocked|running|queued] [--advance] [--workspace auto|worktree|repo] [--dry-run] [--json]
agent-team job cleanup <job-id>|--all [--dry-run|--merged] [--force-branch] [--json]
agent-team job start|stop|kill|wait|logs|attach|send|unblock|update|close|reopen|rm|prune|reconcile ...
                                                # create, monitor, dispatch, control, and clean up durable work units
agent-team pipeline ls [--format '{{.Name}}'] [--json] | show <pipeline> [--format '{{.Name}}'] [--json] | doctor [<pipeline>|--all] [--format '{{.OK}}'] [--json] | status [<pipeline>|--all] [--format '{{.Pipeline}}'] [--json] | jobs <pipeline> [--status running] [--format '{{.ID}}'] [--json] | ready <pipeline>|--all [--state ready|all] [--format '{{.JobID}}'] [--json] | advance <pipeline>|--all [--limit N] [--dry-run] [--preview-routes] [--format '{{.JobID}}'] [--json] | run <pipeline> <ticket> [--ticket-url <url>] [--dispatch] [--dry-run] [--format '{{.ID}}'] [--json]
                                                # inspect declared pipeline workflows from instances.toml
agent-team team ls [--json] | show <team> [--json] | doctor <team>|--all [--format '{{.OK}}'] [--json] | overview <team> [-w] [--no-clear] [--interval 2s] [--schedule-limit N] [--format '{{.State}}'] [--json] | next <team> [-w] [--limit N] [--format '{{.State}}'] [--json] | run <team> <ticket> [kickoff...] [--pipeline name] [--dispatch] [--dry-run] [--json] | up|down|restart <team> [--dry-run] [--json] | sync <team> [-q] [--dry-run] [--stop-extras] [--action start] [--summary] [--format '{{.Instance}} {{.Action}}'] [--wait --timeout 30s] [--json] | plan <team> [--stop-extras] [--action start] [--json] | ps <team> [-w] [--no-clear] [--interval 2s] [--json] | stats <team> [-w] [-a] [--summary] [--sort cpu|mem|rss] [--json] | jobs <team> [--status running] [--sort updated] [--format '{{.ID}}'] [--json] | triage <team> [--min-severity warning] [--reason queue_dead] [-w] [--json] | ready <team> [--state ready|all] [--format '{{.JobID}}'] [--json] | advance <team> [--limit N] [--dry-run] [--preview-routes] [--format '{{.JobID}}'] [--json] | cleanup <team> [--dry-run|--merged] [--force-branch] [--json] | queue <team> [--state dead] [--job SQU-42] [--summary] [--json] | queue quarantine <team> [--state dead] [--job SQU-42] [--restorable|--unrestorable] [--json] | queue quarantine show <team> <path> [--json] | queue quarantine restore <team> <path>|--all [--state dead] [--job SQU-42] [--dry-run] [--json] | queue quarantine drop <team> <path>|--all [--state dead] [--job SQU-42] [--restorable|--unrestorable] [--dry-run] [--json] | queue retry|drop <team> <id>|--all [--dry-run] [--job SQU-42] [--json] | send <team> <message...> [--all] [--latest|--last N] [--dry-run] [--json] | wait <team> [<instance>...] [--until running] [--until-phase idle] [--dry-run] [--summary] [--json] | prune <team> [--dry-run] [--older-than 24h] [--status exited|crashed] [--summary] [--json] | logs <team> [--list] [-f] [--tail N] | events <team> [-f] [--tail N] [--summary] [--action stop] [--json] | monitor <team> [-w] [-a] [--plan] [--jobs] [--schedules] [--events N] [--format '{{.Team.Name}}'] [--json] | tick <team> [-w | --until-idle] [--interval 2s] [--max-cycles N] [--dry-run] [--preview-routes] [--skip-schedules] [--skip-drain] [--skip-advance] [--limit N] [--format '{{.Team.Name}} {{.Tick.Queue.Dispatched}}'] [--json] | drain <team> [--max-cycles N] [--interval 2s] [--limit N] [--format '{{.Team.Name}} {{.Idle}}'] [--json] | repair <team> [--dry-run] [--jobs] [--skip-daemon] [--skip-queue] [--skip-tick] [--until-idle] [--json] | snapshot <team> [--events N|-1] [--schedule-limit N] [--no-redact] [--json | --output snapshot.json] | pipelines <team> [--format '{{.Pipeline}}'] [--json] | schedules <team> [--format '{{.Name}}'] [--json] | health <team> [--jobs] [-q] [--format '{{.Health.Healthy}}'] [--json] | status <team> [-w] [--no-clear] [--interval 2s] [--json]
                                                # inspect declared teams and summarize team-owned instances, jobs, queue, schedules, and pipelines
agent-team schedule ls [--json] | due [--format '{{.Name}} {{.DueReason}}'] [--json] | next [--limit N] [--format '{{.Name}} {{.NextRun}}'] [--json] | fire [--dry-run] [--preview-triggers] [--format '{{.Fired}} {{len .Schedules}}'] [--json] | show <schedule> [--json] | run <schedule> [--payload <json>] [--dry-run] [--preview-triggers] [--json]
                                                # inspect due/upcoming schedules, fire all due schedule events, or manually publish one declared schedule event
agent-team queue ls [-w] [--summary] [--state pending|dead] [--instance worker] [--event-type agent.dispatch] [--job SQU-42] [--ready] [--json] | show <id> | doctor [--quarantine [--dry-run]] [--json] | quarantine ls [--state pending|dead] [--instance worker] [--event-type agent.dispatch] [--job SQU-42] [--restorable|--unrestorable] [--json] | quarantine show <path> [--json] | quarantine restore <path>|--all [--state pending|dead] [--instance worker] [--event-type agent.dispatch] [--job SQU-42] [--dry-run] [--force] [--json] | quarantine drop <path>|--all [--state pending|dead] [--instance worker] [--event-type agent.dispatch] [--job SQU-42] [--restorable|--unrestorable] [--dry-run] [--older-than 24h] [--json] | drain [--dry-run] [--json] | drop <id>|--all [--dry-run] | retry <id>|--all [--dry-run] | prune [--state dead|pending|all] [--older-than 24h] [--dry-run]
                                                # inspect, validate, drain, retry, drop, and prune persisted daemon dispatch queue items; human queue rows include ACTION hints
agent-team intake linear|github (--payload <json> | --payload-file <path|->) [--dry-run] [--preview-triggers] [--format '{{.Event.Type}}'] [--json] [github: --reconcile-job [--cleanup-merged]] | schedule <name> [--dry-run] [--preview-triggers] [--format '{{.Event.Type}}'] [--json] | serve [--addr 127.0.0.1:8787] [--dry-run] [--preview-triggers] [--linear-secret <secret>] [--github-secret <secret>] [--linear-max-age 1m] [--prune-ok-older-than 168h] [--prune-recovered-older-than 168h] [--github-reconcile-job [--github-cleanup-merged]] | summary [--provider linear|github] [--status ok|error] [--replay-status ok|error|none|any] [--unresolved] [--json] | doctor [--json] | deliveries [--tail 20|all] [--provider linear|github] [--status ok|error] [--replay-status ok|error|none|any] [--unresolved] [--json] | replay <delivery-id> [--dry-run] [--preview-triggers] [--json] | replay --all [--provider linear|github] [--status ok|error|all] [--limit N] [--dry-run] [--preview-triggers] [--json] | prune [--status ok|error|all] [--replay-status ok|error|none|any] [--older-than 24h] [--dry-run] [--json]
                                                # normalize external webhook or schedule events, run a local /linear and /github listener, summarize/validate/inspect/prune delivery history with ACTION hints, and replay one or many normalized deliveries; successful replays mark failures recovered
agent-team channels                             # list pub/sub channels; reads local channel state if the daemon is down
agent-team channel show <name>                  # show a channel summary and recent messages
agent-team channel publish <name> <body...> [--sender user]
                                                # publish to a channel; appends locally if the daemon is down
agent-team event publish <type> [--payload <json> | --payload-file <path|->] [--dry-run] [--format '{{len .Matched}} {{len .Dispatched}}'] [--json]
                                                # manually publish a topology event through the daemon
agent-team channel rm <name> -f                 # delete a channel and its durable state
agent-team rm [<instance>...] [-q] [--all] [--finished] [--latest | --last N] [--status stopped] [--phase done] [--stale] [--unhealthy] [--agent manager] [--dry-run] [--summary] [-f] [--format '{{.Instance}} {{.Path}}'] [--json]
                                                # remove instance state and daemon metadata, using persisted metadata if the daemon is down
agent-team prune [-q] [--dry-run] [--older-than 24h] [--agent manager] [--status exited] [--phase done] [--stale] [--unhealthy] [--summary] [--format '{{.Instance}} {{.Path}}'] [--json] # remove finished persisted daemon metadata and state
agent-team run <agent> [-n <instance>] [-d | --attach --tail N|all] [--ready-timeout 3s] [--set k=v]... [-p "..."] [--format '{{.Instance}} {{.PID}}'] [--json]
                                                # launch the selected LLM runtime as <agent>; --detach dispatches via daemon
agent-team upgrade --check [--to <ref>] [--strict] [--format '{{.Differs}}'] [--json]
                                                # compare .template.lock with a template ref; --strict exits 1 on drift
agent-team doctor [--strict-daemon] [--strict-runtime] [--strict-template] [--format '{{.OK}}'] [--json]
                                                # validate layout, config, provenance, skill wiring, pipeline workflows, selected runtime, and daemon binary availability
agent-team --version                            # print version

agent-team template ls                          # list bundled + cached templates
agent-team template show [<ref>]                # print manifest (default: bundled)
agent-team template pull <ref> [--as <n>]       # copy a local template or clone a git ref into the cache
agent-team template rm <ref>                    # remove a cached template
agent-team template run <ref> <agent> [--target <dir>] [--keep] [--set k=v]... [-p "..."]
                                                # one-shot: init into a (temp)dir + spawn the agent

agent-team instance ls                          # list instance state dirs (.agent_team/state/*)
agent-team instance show <name>                 # show an instance's state files
agent-team instance rm [<name>...] [--all] [--finished] [--latest | --last N] [--status stopped] [--phase done] [--stale] [--unhealthy] [--agent manager] [--dry-run] [--summary] [-f] [--json]
                                                # delete instance state and daemon metadata
```

Shortcuts: `agent-team up` = `start`, `agent-team down` = `stop`, `agent-team ls` = `ps`, and `agent-team top` = `stats`.

Lifecycle actions (`start`, `stop`, `kill`, `restart`), desired-state previews (`plan`), topology convergence (`sync`), cleanup (`rm`, `prune`), and completion waits (`wait`) accept `--summary` to show aggregate counts for the same selected instances; `--summary --json` emits a `{ "summary": ... }` object for scripts. Use `pipeline doctor [<pipeline>|--all]` after editing workflow declarations to catch dependency cycles, unroutable step targets, and schedule-triggered pipelines with no matching schedule source. Use `team doctor <team>|--all` after editing `instances.toml` for the same workflow checks scoped to team-owned pipelines, plus team pipeline or schedule routes that point outside the team. Use `team wait <team>` for the same wait output scoped to team-owned instances; it defaults to waiting for persistent team members and live team-owned ephemeral children to be `running`. Use `team stats <team>` for scoped CPU/RSS snapshots, `team monitor <team> --jobs --schedules --events N` for a scoped operator dashboard, and `team prune <team>` to remove only finished daemon-known instances owned by that team.

Use `overview` when you want the shortest non-mutating answer to “what needs attention next?” It summarizes health, topology, jobs, queue, pipelines, schedules, intake deliveries, and next command hints in one screen; use `team overview <team>` for the same view scoped to one declared team. Use `next` when you only want the recommended commands, `next --team <team>` for scoped actions, or `team next <team>` when staying inside the team command namespace. Use `monitor --jobs --schedules` or `job triage` plus `schedule next` when you need fuller live detail. `job triage` and `monitor --jobs` include durable job triage, status-file job update previews, and pipeline status summaries, so unreconciled blocked workers and ready pipeline steps are visible before a maintenance tick; triage rows include ACTION hints for common recovery commands, and `job show <id>` shows the matching queue/status previews and per-job action hints. Use `team triage <team>` for the same attention view scoped to one team's jobs and queue. Use `team ready <team>` when you only need the next advanceable or blocked pipeline steps owned by one team, and `team advance <team> --dry-run --preview-routes` to preview dispatches before advancing them. `health` always includes pipeline workflow doctor problems and unresolved intake delivery failures; add `health --jobs` in scripts when stuck/failed jobs should make the health check fail too. With `--jobs`, health also previews status-file job updates, summarizes pipeline status, includes action hints on job and pipeline issues, and fails if an unreconciled worker status reports a blocked job. Use `job reconcile status` when workers have written `status.toml` and you want to refresh the owning durable jobs without dispatching anything. Use `tick` to act on ready work: it reconciles stale daemon metadata and job status files, fires due schedules, asks the daemon to dispatch ready queued events, and advances ready pipeline jobs. `tick --dry-run` previews job-status, schedule, queue, and pipeline work without mutating state; add `--preview-routes` to include route and dispatch payload previews for ready pipeline steps. `team tick <team>` scopes schedule, queue, and pipeline work to one declared team; add `--dry-run` to preview the same team-owned work without a daemon. Both global and team tick support `--watch` for foreground loops and `--until-idle --max-cycles N` for finite CI/script drains; `team drain <team>` is the shorter team-scoped drain-until-idle command for scripts and operator cleanup. `--json` emits one JSON object per cycle for watch mode, or an aggregate cycle result for until-idle/drain mode.

Use `repair --dry-run` when `health` reports dead-letter queue items or stale daemon state. Queue health issues include retry/repair action hints, quarantined-file inspection hints, and repair dry-runs show those issue actions before the planned repair steps. Use `queue doctor` when queue list, drain, retry, or repair fails to parse persisted queue files; `overview` / `next` recommend it for queue parse failures, and top-level `doctor` includes the same queue validation plus quarantine warnings. Start with `queue doctor --quarantine --dry-run` to preview problem files that would be moved under `.agent_team/daemon/queue/quarantine/`, then rerun without `--dry-run` to remove those files from the active queue without deleting them. Use `queue quarantine ls --job SQU-42`, `--instance worker`, `--restorable`, or `--unrestorable` to narrow preserved files before inspection, then `queue quarantine show <path>` and `queue quarantine restore <path> --dry-run` before moving one validated entry back to pending/dead; use `queue quarantine restore --all --job SQU-42 --dry-run` to preview restoring a filtered restorable batch. Use `queue quarantine drop <path> --dry-run`, `queue quarantine drop --all --job SQU-42 --dry-run`, or `queue quarantine drop --all --unrestorable --dry-run` before discarding inspected files. Queue summaries split quarantined files into `restorable` and `unrestorable` counts so recovery scripts can choose the right listing. `health`, `overview`, `next`, queue summaries, and team-scoped status/health/monitor views surface quarantined files until you inspect, restore, or drop them; use `team queue quarantine <team>` plus its scoped filters, `show`, filtered `restore --all`, and filtered `drop --all` subcommands to act only on files owned by one team. Dry-runs also surface unresolved intake delivery failures with replay commands, but `repair` does not replay webhooks automatically. Add `--preview-routes` to include route and dispatch payload previews for pipeline steps the repair tick would advance. Add `--jobs` to include durable job triage and status-file previews in the before/after health snapshots. `repair` starts and reconciles the daemon, retries dead-letter queue entries, then runs a maintenance tick; add `--skip-daemon`, `--skip-queue`, or `--skip-tick` to narrow the recovery action. Use `team repair <team>` for the same recovery loop scoped to team-owned queue items, schedules, and pipelines.

Use `intake summary` for a compact delivery ledger rollup before replaying or pruning webhook history. It distinguishes successful, failed, unresolved, recovered, replayable, and replay-failed deliveries, includes per-provider counts, and prints the same recovery/prune action hints as the detailed delivery rows. Use `intake doctor` when summary or replay fails to parse history; it reports corrupt JSONL lines, duplicate IDs, invalid statuses, and unreplayable failure rows without mutating the ledger. Top-level `doctor` includes the same intake ledger validation.

Use `job create <ticket> --dry-run --dispatch`, `team run <team> <ticket> --dry-run --dispatch`, `pipeline run <pipeline> <ticket> --dry-run --dispatch`, `pipeline advance <pipeline> --dry-run --preview-routes`, `job dispatch <job-id> --dry-run`, `job advance <job-id> --dry-run`, `job step <job-id> <step-id> --dry-run --advance`, `job retry <job-id> --dry-run --dispatch`, or `dispatch <target> <ticket> --dry-run` before starting the daemon when you want to inspect local topology routes and the exact payload that would be published. `team run` selects the team's only declared pipeline automatically, or accepts `--pipeline` when a team has several. Use `job unblock <job-id> <answer...>` when a blocked worker needs operator input: it accepts blocked status-file previews from `job triage` or `job show`, sends the answer to the owning instance, marks the durable job running, and records an audit event. Older blocked status files are ignored after the unblock until the worker writes a newer status. Add `--dry-run` to preview the target and status transition without sending a mailbox message. Use `job retry <job-id> --dispatch` for the common failed-job recovery path: it records a reopen event, then immediately sends the job back through daemon dispatch. For pipeline jobs, it resets the first failed step whose dependencies are satisfied, then advances the next ready step. Use `job cleanup <job-id> --dry-run` to preview one job-owned worktree and branch removal after a PR merge, `job cleanup --all --dry-run` to preview every done job that still owns cleanup metadata, or `team cleanup <team> --dry-run` to scope that preview to one declared team. `job triage` reports those terminal jobs as `cleanup_ready`, and `overview` / `next` recommend the matching batch dry-run when cleanup-ready jobs exist. After confirming merge, pass `--merged`; add `--force-branch` only when the PR is merged but the local branch is not recognized as merged by git.

Use `snapshot --output diagnostics.json` when you need one read-only artifact for debugging or handoff. It captures health, desired-state plan, instance rows, jobs, job triage, status-derived job update previews, pipeline status summaries, ready pipeline advance previews, team doctor findings, queue items, queue quarantine inventory, schedules, intake deliveries, runtime profile, and recent lifecycle events; sensitive payload keys are redacted by default, and section-level failures are recorded in the JSON instead of aborting the whole report. Use `team snapshot <team>` for the same artifact scoped to one declared team's instances, jobs, queue items, queue quarantine inventory, pipelines, team doctor findings, schedules, and lifecycle events. Use `--no-redact` only for local debugging when raw payload values are required.

`status --summary --events N`, `monitor --summary --events N`, and `watch --summary --events N` add compact recent lifecycle event counts; combine `--events` with `--event-action` and `--since` to narrow event tails before summarizing. `status --summary --resources`, `monitor --summary --resources`, and `watch --summary --resources` add aggregate CPU, memory, RSS, lifecycle, and phase counts. `status --summary --plan`, `monitor --summary --plan`, and `watch --summary --plan` add compact desired-state action counts from topology. Combining `--summary` with `--resources`, `--plan`, and `--events` produces one compact operator snapshot instead of full tables.

`<ref>` for `init` and `template show` accepts:

- **empty / `bundled`** — the default template embedded in the binary.
- **a local path** (`./eng-team`, `/abs/path`) — useful when authoring a template.
- **a cached name** — anything previously `template pull`'d.
- **a Git ref after pull** — `agent-team template pull github.com/foo/bar@v0.1.0`, then `agent-team init github.com/foo/bar@v0.1.0`. HTTPS, SSH, `git@host:path.git@ref`, and `file://...@ref` sources are supported; use `--as <cache-ref>` to store under a custom cache key.

## How `run` works

`agent-team run <agent>` reads every `.agent_team/agents/<name>/agent.md`, parses the YAML frontmatter (`description`) and body (the prompt), resolves each agent's skill set from `agents/<name>/skills/` plus `[skills].extra` in `agents/<name>/config.toml`, builds a tmpdir of symlinks satisfying the runtime's extra-directory discovery, and exec's the selected runtime.

The default runtime is Claude-compatible:

```sh
claude --agents '<json>' --add-dir <tmpdir> --append-system-prompt-file <kickoff> <forwarded-args>
```

With `--detach`, with `--attach`, or with `--prompt` when the daemon is already running, the CLI sends that same resolved argv/env to `agent-teamd`. `--detach` returns a log-follow hint, while `--attach` follows the daemon-captured log immediately.

Runtime selection is repo-configurable and environment-overridable. Put this in `.agent_team/config.toml` to set a repo default:

```toml
[runtime]
kind = "codex"   # or "claude"
binary = "codex" # optional wrapper/binary override
```

Environment variables take precedence:

- `AGENT_TEAM_RUNTIME=claude` (default) enables the full daemon, resume, subagent registry, and queue/event dispatch path.
- `AGENT_TEAM_RUNTIME=codex` launches Codex sessions with `codex` or `codex exec`. The chosen agent prompt and task are passed as the initial Codex prompt, and team agents are listed as coordination context. Direct interactive runs work without the daemon; one-shot runs with `--prompt` can also use `--detach`, `--attach`, `--json`, or `--format` for daemon-managed logs and process metadata. Codex-managed daemon runs do not support `start`/resume or native subagent registration because Codex does not expose the same `--agents` / `--session-id` contract.
- `AGENT_TEAM_RUNTIME_BIN=/path/to/wrapper` overrides the binary for the selected runtime.

Run `agent-team runtime` to confirm the selected profile, resolved binary path, config source, and supported capabilities.

For the Claude-compatible runtime, the named agent's prompt becomes the session's system prompt and all other agents stay registered as subagents so the named agent can dispatch them via the Task tool. The launcher creates `.agent_team/state/<instance>/` (defaults the instance name to the agent name; pass `--name` for a unique identifier) and exports:

- `AGENT_TEAM_ROOT` — absolute path to `.agent_team/`
- `AGENT_TEAM_INSTANCE` — the instance name
- `AGENT_TEAM_STATE_DIR` — absolute path to `.agent_team/state/<instance>/`

Subagents are session-scoped — they exist only for the duration of the spawned `claude` process. Nothing is written into `.claude/agents/`. No plugin install, no marketplace, no global state.

## The bundled default template

`agent-team init` (no ref) uses the default template baked into the binary — a software-engineering team:

- **`ticket-manager`** — searches, creates, routes, and transitions Linear tickets.
- **`manager`** — persistent agent. Tracks goals and dispatches workers. State lives at `.agent_team/state/<instance-name>/`. Multiple instances can run side-by-side (e.g. `--name=manager-billing`, `--name=manager-release`), each with their own state directory.
- **`worker`** — ephemeral. One instance per ticket, each in a fresh git worktree, each delivers a PR. No persistent state — the worktree is the workspace.
- **Skills**: `linear` (GraphQL wrapper), `pull-request` (gh CLI wrapper), `assign-worker` (worker-spawn mechanics, agent-private to the manager).

Required parameters: `linear.team_id`, `linear.ticket_prefix`. Run `agent-team template show` for the full manifest.

`agent-team init --template empty` skips the bundled content and gives you just the directory scaffold + a stub `config.toml`.

## Forward-looking design

- [`documentation/templates.md`](./documentation/templates.md) — full templates-as-images model: parameter declarations, layered config resolution, `upgrade` semantics, worked example.
- [`documentation/orchestrator.md`](./documentation/orchestrator.md) — v1.1+ `agent-teamd` daemon: persistent instance lifecycle, runtime-agnostic execution, replacement of in-session dispatch primitives.

## Working on agent-team itself

Contributor orientation: [`CLAUDE.md`](./CLAUDE.md).
