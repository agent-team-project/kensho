# agent-team

A CLI for declaring and launching a custom set of Claude Code subagents and skills. Each **agent** is a directory under `.agent_team/agents/`. Run `agent-team run` and the CLI launches Claude Code with your team registered for that session.

A starter "software engineering team" template (a `ticket-manager`, a `manager`, ephemeral `worker`s, plus Linear / PR / assign-worker skills) is bundled as one example. Use it as-is, edit it, or throw it away and write your own.

**Status**: pre-v1. Public API is unstable.

## Vocabulary

- **agent** — a definition. A directory at `.agent_team/agents/<name>/` containing `agent.md` (frontmatter + prompt) and `config.toml` (skill assignment). Authored, static, reusable.
- **instance** — a named runtime spawn of an agent. Identified by the `name=` parameter at spawn time. One agent can have many instances; each instance has its own state.
- **workspace** — the working directory an instance operates in. For code-writing agents (the bundled `worker`): a fresh git worktree per spawn. For others: the repo root.
- **state** — persistent per-instance files (journal, goals, progress) at `.agent_team/state/<instance-name>/`. Survives across sessions for long-lived instances; ephemeral instances (workers) keep their state inside their worktree.

## Install

```sh
uvx --from "git+https://github.com/jamesaud/agent-team#subdirectory=cli" agent-team init
```

`init` writes a starter `.agent_team/` into the current repo:

```
.agent_team/
├── config.toml                              # consumer-specific runtime values (team IDs, etc.)
├── agents/
│   ├── <name>/
│   │   ├── agent.md                         # frontmatter + prompt body
│   │   ├── config.toml                      # [skills].extra: which skills this agent uses
│   │   └── skills/                          # optional agent-private skills
│   └── ...
├── skills/
│   ├── <name>/SKILL.md                      # shared skills (referenced by any agent)
│   └── ...
└── state/                                   # per-instance state, written at runtime
    └── <instance-name>/                     # journal.md, goals.md, etc. (created on first spawn)
```

Edit anything you like, then:

```sh
agent-team run manager     # or any other agent name from .agent_team/agents/
```

…and you're in a Claude Code session as that agent, with the rest of the team available as subagents it can dispatch.

## Commands

The CLI groups commands by the resource they manage (Docker / kubectl style):

```sh
agent-team init [--template default|empty]       # bootstrap .agent_team/
agent-team doctor                                # validate layout + config
agent-team run <agent> [--name <instance>] [-p "..."]   # launch Claude Code as <agent>

agent-team agent create <name>                   # scaffold a new agent
agent-team agent ls                              # list agents
agent-team agent show <name>                     # show one agent's metadata + resolved skills
agent-team agent rm <name>                       # delete an agent definition

agent-team skill create <name> [--agent <a>]     # scaffold a skill (shared, or agent-private)
agent-team skill ls [--agent <a>]                # list skills
agent-team skill rm <name> [--agent <a>]         # delete a skill

agent-team instance ls                           # list instances (.agent_team/state/*)
agent-team instance show <name>                  # show one instance's state files
agent-team instance rm <name>                    # delete an instance's state
```

## How it works

`agent-team run <agent>` reads every `.agent_team/agents/<name>/agent.md`, parses the YAML frontmatter (`description`) and body (the prompt), resolves each agent's skill set from `agents/<name>/skills/` plus `[skills].extra` in `agents/<name>/config.toml`, builds a tmpdir of symlinks satisfying Claude Code's `--add-dir` skill discovery, and exec's:

```sh
claude --agents '<json>' --add-dir <tmpdir> --append-system-prompt-file <kickoff> <forwarded-args>
```

The named agent's prompt becomes the session's system prompt (via `--append-system-prompt-file`); all other agents stay registered as subagents so the named agent can dispatch them via the Task tool. The launcher also creates `.agent_team/state/<instance>/` (defaults the instance name to the agent name; pass `--name` for a unique identifier) and exports:

- `AGENT_TEAM_ROOT` — absolute path to `.agent_team/`
- `AGENT_TEAM_INSTANCE` — the instance name
- `AGENT_TEAM_STATE_DIR` — absolute path to `.agent_team/state/<instance>/`

Subagents are session-scoped — they exist only for the duration of the spawned `claude` process. Nothing is written into `.claude/agents/`. No plugin install, no marketplace, no global state.

## The bundled starter

`agent-team init` (default template) drops in a software-engineering team:

- **`ticket-manager`** — searches, creates, routes, and transitions Linear tickets.
- **`manager`** — persistent agent. Tracks goals and dispatches workers. State lives at `.agent_team/state/<instance-name>/`. Multiple instances of the manager agent can run side-by-side (e.g. `name=manager-billing`, `name=manager-release`), each with their own state directory.
- **`worker`** — ephemeral. One instance per ticket, each in a fresh git worktree, each delivers a PR. No persistent state — the worktree is the workspace.
- **Skills**: `linear` (GraphQL wrapper), `pull-request` (gh CLI wrapper), `assign-worker` (worker-spawn mechanics, agent-private to the manager).

`agent-team init --template empty` skips the bundled content and gives you just the directory scaffold + a stub `config.toml`.

## Working on agent-team itself

This repo dogfoods itself — its own `.agent_team/agents` and `.agent_team/skills` are symlinks into the bundled template at `cli/src/agent_team/template/`, so edits to template content are immediately live for the next `agent-team run`.

CLI dev loop:

```sh
cd cli
uv run --with-editable . agent-team --help
```

Or install editably:

```sh
cd cli && uv pip install -e .
agent-team --help
```

Smoke-test against a tmp dir:

```sh
agent-team init --target /tmp/team-smoke
```

Contributor orientation: [`CLAUDE.md`](./CLAUDE.md).
