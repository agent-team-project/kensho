# agent-team

A CLI for declaring teams of Claude Code subagents and skills, then instantiating them into any repo from a parameterized template. Each **agent** is a directory under `.agent_team/agents/`; `agent-team run <agent>` launches Claude Code with the team registered for that session.

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

The daemon is bypassed; claude is exec'd directly. For long-lived setups where you want `instance ps` / `logs --follow` visibility, use `init` + `run` separately.

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
agent-team plan [--json] [--summary] [--stop-extras] [--format '{{.Instance}} {{.Action}}'] [--agent manager] [--instance manager] [--status running] [--phase idle] [--action start]
                                                # preview desired instance state from topology and daemon metadata
agent-team sync [-q] [--dry-run] [--stop-extras] [--agent manager] [--instance manager] [--status unknown] [--phase idle] [--action start] [--summary] [--format '{{.Instance}} {{.Action}}'] [--ready-timeout 3s] [--wait --timeout 30s] [--json]
                                                # reload topology, reconcile metadata, start/resume persistent instances, and optionally stop running extras
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
agent-team health [-q] [-w] [--no-clear] [--wait --timeout 30s] [--latest | --last N] [--format '{{.Healthy}} {{.Summary.Running}}'] [--agent manager] [--instance manager] [--status running] [--phase idle] [--stale] [--unhealthy] [--strict-topology] [--json]
                                                # check daemon, declarations, crashes, stale status, and optional topology drift
agent-team monitor [-w] [--no-clear] [-a] [--summary [--resources]] [--plan [--stop-extras] [--action start]] [--latest | --last N] [--events N [--event-action stop] [--since 10m]] [--sort status|agent|phase|stale|unhealthy|started|stopped|exited|name] [--stats-sort cpu|mem|rss|status|agent|phase|stale|unhealthy|name] [--format '{{.Health.Healthy}} {{len .Instances}}'] [--json] [--interval 2s] [--strict-topology] [--agent manager] [--instance manager] [--status running] [--phase idle] [--stale] [--unhealthy]
                                                # combined health, instance, resource, and event-history snapshot; uses local metadata if the daemon is down
agent-team watch [--no-clear] [-a] [--summary [--resources]] [--plan [--stop-extras] [--action start]] [--latest | --last N] [--events N [--event-action stop] [--since 10m]] [--sort status|agent|phase|stale|unhealthy|started|stopped|exited|name] [--stats-sort cpu|mem|rss|status|agent|phase|stale|unhealthy|name] [--format '{{.Health.Healthy}} {{len .Instances}}'] [--json] [--interval 2s] [--strict-topology] [--agent manager] [--instance manager] [--status running] [--phase idle] [--stale] [--unhealthy]
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
                                                # interactive claude --resume handoff; daemon resumes supervision afterward
agent-team events [-f] [--tail N] [--latest | --last N] [--since 24h] [--summary] [--format '{{.Action}} {{.Instance}}'] [--action dispatch] [--agent manager] [--instance manager] [--status running] [--phase idle] [--stale] [--unhealthy] [--json]
                                                # show/follow lifecycle events; phase/stale/unhealthy narrow by current status.toml; reads local history if the daemon is down
agent-team wait [<instance>...] [-q] [--all] [--latest | --last N] [--agent manager] [--status running] [--phase idle] [--stale] [--unhealthy] [--until terminal|running|stopped|exited|crashed|removed] [--until-phase done] [--timeout 5m] [--interval 500ms] [--dry-run] [--fail-on-crash] [--summary] [--format '{{.Instance}} {{.Status}} {{.Phase}}'] [--json]
                                                # wait for lifecycle or work-phase conditions, using persisted metadata if the daemon is down
agent-team send [<instance>] <message...> [--all] [--latest | --last N] [--agent manager] [--status running] [--phase idle] [--stale] [--unhealthy] [--from user] [--allow-missing] [--dry-run] [--format '{{.To}} {{.ID}}'] [--json]
                                                # send a daemon mailbox message; phase/stale/unhealthy selectors use current status.toml; appends locally if the daemon is down
agent-team channels                             # list pub/sub channels; reads local channel state if the daemon is down
agent-team channel show <name>                  # show a channel summary and recent messages
agent-team channel publish <name> <body...> [--sender user]
                                                # publish to a channel; appends locally if the daemon is down
agent-team event publish <type> [--payload <json>] [--format '{{len .Matched}} {{len .Dispatched}}'] [--json]
                                                # manually publish a topology event through the daemon
agent-team channel rm <name> -f                 # delete a channel and its durable state
agent-team rm [<instance>...] [-q] [--all] [--finished] [--latest | --last N] [--status stopped] [--phase done] [--stale] [--unhealthy] [--agent manager] [--dry-run] [--summary] [-f] [--format '{{.Instance}} {{.Path}}'] [--json]
                                                # remove instance state and daemon metadata, using persisted metadata if the daemon is down
agent-team prune [-q] [--dry-run] [--older-than 24h] [--agent manager] [--status exited] [--phase done] [--stale] [--unhealthy] [--summary] [--format '{{.Instance}} {{.Path}}'] [--json] # remove finished persisted daemon metadata and state
agent-team run <agent> [-n <instance>] [-d | --attach --tail N|all] [--ready-timeout 3s] [--set k=v]... [-p "..."] [--format '{{.Instance}} {{.PID}}'] [--json]
                                                # launch Claude Code as <agent>; --detach dispatches via daemon
agent-team upgrade --check [--to <ref>]         # compare .template.lock with a template ref
agent-team doctor [--strict-daemon]             # validate layout, config, provenance, skill wiring, and daemon binary availability
agent-team --version                            # print version

agent-team template ls                          # list bundled + cached templates
agent-team template show [<ref>]                # print manifest (default: bundled)
agent-team template pull <path> [--as <n>]      # copy a local template into the cache
agent-team template rm <ref>                    # remove a cached template
agent-team template run <ref> <agent> [--target <dir>] [--keep] [--set k=v]... [-p "..."]
                                                # one-shot: init into a (temp)dir + spawn the agent

agent-team instance ls                          # list instance state dirs (.agent_team/state/*)
agent-team instance show <name>                 # show an instance's state files
agent-team instance rm [<name>...] [--all] [--finished] [--latest | --last N] [--status stopped] [--phase done] [--stale] [--unhealthy] [--agent manager] [--dry-run] [--summary] [-f] [--json]
                                                # delete instance state and daemon metadata
```

Shortcuts: `agent-team up` = `start`, `agent-team down` = `stop`, `agent-team ls` = `ps`, and `agent-team top` = `stats`.

Lifecycle actions (`start`, `stop`, `kill`, `restart`), desired-state previews (`plan`), topology convergence (`sync`), cleanup (`rm`, `prune`), and completion waits (`wait`) accept `--summary` to show aggregate counts for the same selected instances; `--summary --json` emits a `{ "summary": ... }` object for scripts.

`status --summary --events N`, `monitor --summary --events N`, and `watch --summary --events N` add compact recent lifecycle event counts; combine `--events` with `--event-action` and `--since` to narrow event tails before summarizing. `status --summary --resources`, `monitor --summary --resources`, and `watch --summary --resources` add aggregate CPU, memory, RSS, lifecycle, and phase counts. `status --summary --plan`, `monitor --summary --plan`, and `watch --summary --plan` add compact desired-state action counts from topology. Combining `--summary` with `--resources`, `--plan`, and `--events` produces one compact operator snapshot instead of full tables.

`<ref>` for `init` and `template show` accepts:

- **empty / `bundled`** — the default template embedded in the binary.
- **a local path** (`./eng-team`, `/abs/path`) — useful when authoring a template.
- **a cached name** — anything previously `template pull`'d.

Git URL refs (`github.com/foo/bar@v0.1.0`) are tracked as a follow-up — see [`documentation/templates.md`](./documentation/templates.md) § Refs.

## How `run` works

`agent-team run <agent>` reads every `.agent_team/agents/<name>/agent.md`, parses the YAML frontmatter (`description`) and body (the prompt), resolves each agent's skill set from `agents/<name>/skills/` plus `[skills].extra` in `agents/<name>/config.toml`, builds a tmpdir of symlinks satisfying Claude Code's `--add-dir` skill discovery, and exec's:

```sh
claude --agents '<json>' --add-dir <tmpdir> --append-system-prompt-file <kickoff> <forwarded-args>
```

With `--detach`, with `--attach`, or with `--prompt` when the daemon is already running, the CLI sends that same resolved argv/env to `agent-teamd`. `--detach` returns a log-follow hint, while `--attach` follows the daemon-captured log immediately.

The named agent's prompt becomes the session's system prompt; all other agents stay registered as subagents so the named agent can dispatch them via the Task tool. The launcher creates `.agent_team/state/<instance>/` (defaults the instance name to the agent name; pass `--name` for a unique identifier) and exports:

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
