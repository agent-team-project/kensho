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

`agent-team` is a single Go binary. Install it with:

```sh
go install github.com/jamesaud/agent-team/cmd/agent-team@latest
```

This drops `agent-team` into `$(go env GOPATH)/bin` (typically `~/go/bin`). Make sure that directory is on your `PATH`.

Verify:

```sh
agent-team --version
```

> Prebuilt binaries via `goreleaser` / Homebrew are tracked as a follow-up; for now `go install` is the v1.0 path.

## Lifecycle

```
template pull  →  init  →  run  →  upgrade
```

1. **(Optional) `template pull`** — fetch a template into the local cache. Skip this for the bundled default.
2. **`init`** — instantiate a template into the current repo. Resolves required parameters (`--set k=v` or interactive prompt), writes `.agent_team/` with `.tmpl` files rendered.
3. **`run`** — launch a Claude Code session as one of the agents.
4. **`upgrade`** *(future)* — re-resolve the repo against a newer template version with three-way merge for user-edited files.

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

## Commands

```sh
agent-team init [<ref>] [--set k=v]... [--no-input] [--force]
                                                # instantiate a template into the current repo
agent-team run <agent> [-n <instance>] [--set k=v]... [-p "..."]
                                                # launch Claude Code as <agent>
agent-team doctor                               # validate layout + config
agent-team --version                            # print version

agent-team template ls                          # list bundled + cached templates
agent-team template show [<ref>]                # print manifest (default: bundled)
agent-team template pull <path> [--name <n>]    # copy a local template into the cache
agent-team template rm <ref>                    # remove a cached template

agent-team instance ls                          # list instances (.agent_team/state/*)
agent-team instance show <name>                 # show an instance's state files
agent-team instance rm <name>                   # delete an instance's state
```

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
