# agent-squad

A CLI that vendors a reusable software-engineering agent squad — a `ticket-manager`, persistent feature/domain `manager`s, ephemeral `worker`s, and supporting skills — into any repo. Drop it into your project, edit one config file, and you have a Claude Code-driven team that can triage Linear tickets and ship PRs.

**Status**: pre-v1. Under active development. See [`documentation/`](./documentation) for the product strategy, architecture, roadmap, and open questions.

## Install + use

Vendor the squad into a repo:

```sh
uvx --from git+https://github.com/jamesaud/agent-squad agent-squad init
```

This creates `.agent_squad/` in the current directory with the squad's agents, skills, and a starter `config.toml`, plus a Claude Code plugin shim at `.claude-plugin/marketplace.json`.

Edit `.agent_squad/config.toml` to match your Linear team. Then, in Claude Code:

```
/plugin marketplace add .
/plugin install agent-squad
```

The vendored agents register as `agent-squad:ticket-manager`, `agent-squad:manager`, `agent-squad:worker`, and the skills as `agent-squad:linear`, `agent-squad:pull-request`, `agent-squad:assign-worker`. Edits to anything under `.agent_squad/` are picked up by `/reload-plugins`.

## CLI commands

| Command | Purpose |
|---|---|
| `agent-squad init` | Vendor the bundled template into the current repo. |
| `agent-squad add manager <slug>` | Scaffold a new persistent manager scope at `.agent_squad/managers/<slug>/`. |
| `agent-squad doctor` | Sanity-check the vendored squad. |
| `agent-squad sync` | (v0.2) Refresh vendored content from a newer CLI version. v0.1 stub points at `init --force`. |

## What's in the squad

- **`ticket-manager`** — manages Linear tickets: search, create, route, comment, transition states.
- **`manager`** — persistent agent scoped to a domain (a feature, an initiative, an ongoing responsibility). Holds working memory across sessions, dispatches workers within scope.
- **`worker`** — ephemeral agent that takes one ticket end-to-end: plan → implement in a worktree → open PR.
- **Skills**: `linear` (GraphQL wrapper), `pull-request` (gh CLI wrapper), `assign-worker` (worker-spawn mechanics).

## Local development of agent-squad itself

This repo dogfoods itself. Claude Code running here reads the squad directly from `cli/src/agent_squad/template/` via the marketplace shim at `.claude-plugin/marketplace.json`.

CLI development:

```sh
cd cli
uv run --with-editable . agent-squad --help
```

After editing any template file under `cli/src/agent_squad/template/`:

```
/reload-plugins
```

Full contributor orientation: [`CLAUDE.md`](./CLAUDE.md). Strategy, architecture, roadmap: [`documentation/`](./documentation/).

## Docs

- [`CLAUDE.md`](./CLAUDE.md) — contributor-facing orientation (repo layout, dev loop, config conventions).
- [`documentation/vision.md`](./documentation/vision.md) — what this is, who it's for, principles.
- [`documentation/architecture.md`](./documentation/architecture.md) — CLI shape, layers, plugin shim, runner direction.
- [`documentation/roadmap.md`](./documentation/roadmap.md) — milestones C1 → C5 + parking lot.
- [`documentation/open-questions.md`](./documentation/open-questions.md) — open research items.
- [`documentation/notes/archive/`](./documentation/notes/archive/) — historical notes from the plugin-era design (pre-2026-04-27 pivot).
