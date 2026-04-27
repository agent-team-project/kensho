# Architecture

## Distribution model

agent-squad is a Python CLI distributed as a uvx-runnable package from this Git repo.

```sh
uvx --from git+https://github.com/jamesaud/agent-squad agent-squad init
```

Eventually published to PyPI; for v1, Git-URL distribution is sufficient. The CLI bundles the squad template (agents, skills, scripts, manager scaffolding) as package data; `init` copies it into the consumer's repo.

## What the CLI does

| Command | Status | Purpose |
|---|---|---|
| `agent-squad init` | shipped | Vendor the bundled template into `<repo>/.agent_squad/`. Generate the Claude Code plugin shim so vendored agents/skills register. Create `config.toml` from the example if absent. |
| `agent-squad add manager <slug>` | shipped | Scaffold a new manager scope at `.agent_squad/managers/<slug>/CLAUDE.md`. |
| `agent-squad doctor` | shipped | Sanity-check: required keys present in `config.toml`, plugin manifests in place. |
| `agent-squad sync` | stub | (v0.2) Three-way merge bundled template with the consumer's vendored copy, preserving local edits. v0.1 falls back to "rerun init --force, resolve with git." |

## Repo layout

```
agent-squad/                                    # this repo
├── README.md
├── CLAUDE.md
├── cli/
│   ├── pyproject.toml                          # uvx-runnable Python package
│   └── src/agent_squad/
│       ├── cli.py                              # argparse entrypoint
│       ├── commands/                           # init.py, sync.py, add.py, doctor.py
│       └── template/                           # canonical squad content; bundled with the wheel
│           ├── .claude-plugin/plugin.json      # plugin manifest (lives inside template)
│           ├── agents/                         # ticket-manager.md, manager.md, worker.md
│           ├── skills/                         # linear/, pull-request/, assign-worker/
│           ├── scripts/                        # linear-graphql.sh
│           ├── managers/                       # (empty — convention dir for scopes)
│           └── config.toml.example
├── documentation/                              # NOT bundled; contributor-facing
└── .agent_squad/                               # this repo's OWN consumer config (self-dogfood)
    └── config.toml
```

Notes on the layout:

- **`cli/src/agent_squad/template/` is canonical.** When you edit a prompt, you edit it here. Self-dogfood reads it directly via the marketplace.json shim at the repo root (see "Self-dogfood" below).
- **`.agent_squad/` in this repo holds only `config.toml`.** Unlike a consumer repo, we don't vendor a copy of the template into `.agent_squad/agents/` etc. — we read from the canonical source so edits are immediately live.
- **A consumer repo's `.agent_squad/` is fully populated.** `init` copies template content into `agents/`, `skills/`, `scripts/`, `managers/`, plus a `config.toml`.

## Layers

Four layers, each with a clear boundary:

1. **CLI** (Python package, `cli/src/agent_squad/`). Owns the template content and the operations on it (init/sync/add/doctor). Distributed as a wheel; runtime is Python ≥3.11, stdlib only.

2. **Bundled template** (`cli/src/agent_squad/template/`). The squad's agents, skills, scripts, and manager scaffolding. Canonical source for the squad's prompts. Versioned with the CLI.

3. **Consumer-vendored copy** (`<consumer>/.agent_squad/`). The materialised squad in a consumer repo. Copied by `agent-squad init`; committed by the consumer; edits live here. Contains:
   - `config.toml` — consumer's IDs and conventions.
   - `agents/`, `skills/`, `scripts/` — vendored from the template; consumer can edit.
   - `managers/<slug>/` — manager scopes. Created via `agent-squad add manager <slug>`; the scope's `CLAUDE.md` and working-memory files (`journal.md`, `goals.md`, `progress.md`) live here.
   - `.claude-plugin/plugin.json` — generated; tells Claude Code these files are a plugin.

4. **Consumer extensions** (`<consumer>/.claude/...` or new files under `.agent_squad/`). Anything the consumer adds beyond the template — local skills, repo-specific agents, custom hooks. The squad is a starting template, not a closed system.

## How vendored agents and skills register with Claude Code

The CLI uses Claude Code's plugin-discovery mechanism as the activation path, but the source of truth lives in the consumer's repo, not in `~/.claude/plugins/cache/`.

`agent-squad init` generates two manifests:

- `<consumer>/.claude-plugin/marketplace.json` — a single-plugin marketplace pointing at `./.agent_squad`.
- `<consumer>/.agent_squad/.claude-plugin/plugin.json` — the plugin manifest itself (name `agent-squad`, version 0.1.0).

The consumer runs once, in their Claude Code session:

```
/plugin marketplace add .
/plugin install agent-squad
```

After that, `.agent_squad/agents/*.md` register as `agent-squad:<name>` subagents and `.agent_squad/skills/<name>/SKILL.md` register as `agent-squad:<name>` skills. Subsequent edits are picked up by `/reload-plugins` — no `/plugin marketplace update` step, because Claude Code's plugin loader reads directly from the path on each reload (no copy step, since the manifest source path *is* the consumer's repo).

## Configuration surface

Every consumer repo gets `.agent_squad/config.toml`, committed so teammates share a config:

```toml
[squad]
pm_tool   = "linear"
local_dir = ".agent_squad"

[linear]
team_id       = "..."
ticket_prefix = "..."
initiative_id = "..."
labels        = ["..."]

[linear.projects]
# project-name = "<uuid>"

[worktree]
path          = ".claude/worktrees"
branch_prefix = "worker/"
```

**Template ships zero IDs.** Every consumer's IDs live in their TOML; the bundled `config.toml.example` has empty strings as placeholders.

## How TOML values reach the logic

Skills and agents read `.agent_squad/config.toml` at runtime via their existing Bash / Read tool access — no session-level prompt-template substitution.

- **Agent prompts reference capabilities, not IDs.** "use the linear skill to fetch tickets by label" — no UUIDs in prompt text.
- **Skills own config-dependent behavior.** `agent-squad:linear` parses TOML inside its bash blocks to get team/project/label IDs and builds GraphQL queries.
- **Agents with direct config needs** (worker reading worktree path, manager reading scope name) use Bash too.
- **The LLM never parses TOML.** Only deterministic bash does. No instruction-following risk.

Canonical read pattern:

```bash
TEAM_ID=$(python3 -c 'import tomllib; print(tomllib.load(open(".agent_squad/config.toml","rb"))["linear"]["team_id"])')
```

## Hierarchy and control flow

V1 hierarchy:

```
human
  │
  ├─▶ ticket-manager  ─── reads/writes Linear ───▶  Linear workspace
  │
  └─▶ manager-<slug>  ─── reads .agent_squad/managers/<slug>/CLAUDE.md
        │
        │ spawns N in parallel (one per ticket)
        ▼
        worker[0]  worker[1]  ...  worker[N]
          │          │                │
          ▼          ▼                ▼
        .claude/worktrees/<auto>     (fresh worktree per spawn)
          │          │                │
          ▼          ▼                ▼
        PR opened   PR opened    PR opened    ───▶  human reviews & merges
```

Two roles can spawn workers: `ticket-manager` (for arbitrary tickets — historical default) and `manager` (for tickets within their scope — the new persistent path). Workers don't talk to each other. A manager persists across sessions; a worker doesn't.

Direct human → worker invocation (skipping ticket-manager and managers) is also valid for one-off tickets. The manager pattern matters when the same domain comes back across sessions.

## Self-dogfood loop

This repo runs the squad on itself. Because the canonical squad content lives in `cli/src/agent_squad/template/`, we don't `agent-squad init` here — that would create a redundant copy. Instead, we point Claude Code's plugin discovery directly at the template:

- `<repo>/.claude-plugin/marketplace.json` — points at `./cli/src/agent_squad/template`.
- `cli/src/agent_squad/template/.claude-plugin/plugin.json` — the plugin manifest, lives inside the template.

After editing any template file:

```
/reload-plugins
```

No `/plugin marketplace update` needed — Claude Code's loader reads from the path on each reload.

## Boundary with consumer repos

The CLI owns: the bundled template, the manifest format, the TOML *schema*.

The consumer repo owns: their `.agent_squad/config.toml`, their CLAUDE.md, any repo-specific extensions, any edits they made to vendored prompts.

`agent-squad sync` (v0.2+) is the upgrade path. v0.1 = `init --force` + git.

## Credentials

Each user sets personal credentials (`LINEAR_USER_API_KEY`, `GITHUB_TOKEN`, etc.) as environment variables or in a local `.env` file. The skills document which env vars they read; the template never ships secrets. No secrets in TOML.

App tokens / bot tokens / OAuth are candidates for v1.1+ — useful when a scheduled or remote runner needs credentials not tied to a specific human.

## Future runner

A v1.1+ remote runner is a separate program from the CLI:

| | CLI | Runner |
|---|---|---|
| Lifetime | One-shot | Long-lived |
| Workload | File copy, TOML validation | Supervising N workers, watching PRs/Linear events |
| Best fit | Python (stdlib, fast to iterate) | Go (concurrency, single-binary deploy) |
| Status | Shipped (v0.1) | Not started |

Splitting the two keeps each tool the right size. The runner consumes the same `.agent_squad/` layout — same agents, same skills, same config — but runs them remotely without a Claude Code session driving them. That's the architectural payoff of vendoring: the squad is portable.
