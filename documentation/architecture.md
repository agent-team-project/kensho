# Architecture

## Distribution model

Claude Code plugin + self-hosted marketplace, both in this repo. We follow the canonical layout from Claude Code's plugin-marketplaces docs: marketplace manifest at the repo root; the plugin lives in a `plugins/<name>/` subdirectory with its own `.claude-plugin/plugin.json`.

- `.claude-plugin/marketplace.json` — marketplace manifest (at repo root)
- `plugins/squirtle-squad/.claude-plugin/plugin.json` — plugin manifest
- `plugins/squirtle-squad/{agents,skills,hooks}/` — plugin content

Consumers install via:

```
/plugin marketplace add jamesaud/squirtle-squad
/plugin install squirtle-squad@squirtle-squad
```

Private repo; consumers need GitHub access to `jamesaud/squirtle-squad`. Schema + layout details: [`notes/plugin-schema.md`](notes/plugin-schema.md).

## Repo layout

```
squirtle-squad/                           # repo root — also marketplace root
├── .claude-plugin/
│   └── marketplace.json                  # marketplace manifest
├── plugins/
│   └── squirtle-squad/                   # the plugin itself
│       ├── .claude-plugin/
│       │   └── plugin.json               # plugin manifest
│       ├── agents/                       # auto-discovered agent prompts
│       ├── skills/                       # auto-discovered skill dirs
│       └── hooks/                        # optional — hooks.json
├── documentation/                        # NOT part of the plugin
└── .agent_squad/                         # only when self-dogfooding: consumer TOML
```

Paths in this doc are written **relative to the plugin root** (`plugins/squirtle-squad/`) — when you see `agents/worker.md`, the full path is `plugins/squirtle-squad/agents/worker.md`.

## What ships in the plugin

Three categories:

### Agents (`agents/`)

| File | One-line purpose | Origin |
|---|---|---|
| `ticket-manager.md` | Triage Linear tickets; pick, route, create, comment. Dispatches workers. | Extracted from coral `.claude/agents/ticket-manager.md` (154 lines) |
| `worker.md` | Implement one ticket end-to-end in a worktree; open a PR. | Extracted from coral `.claude/agents/worker.md` (222 lines) |

### Skills (`skills/`)

| Skill | One-line purpose | Origin |
|---|---|---|
| `linear/` | Linear GraphQL wrapper: fetch, search, comment, update, create. | Extracted from coral (175 lines); all hardcoded IDs removed |
| `pull-request/` | Create a PR with Linear ticket linking. | Extracted from coral (36 lines); most generic of the set |
| `assign-worker/` | Spawn worker subagent mechanics: team setup, reuse checks, launch. | Extracted from coral (64 lines); worktree path parameterized |

### Not extracted — stays in consumer repos

| Item | Why it stays | Where it lives |
|---|---|---|
| `code-writing/` | 625 lines of coral-specific Python-harness conventions. Too coral-shaped to extract as-is. V1.1+ will ship a minimal customizable `code-writing` template that each consumer forks and fills in per repo. | coral-benchmarks only (v1) |
| `add-benchmark/` | Coral's benchmark-suite scaffolding. Repo-shaped. | coral-benchmarks only |
| `CLAUDE.md` | Every repo keeps its own. | Consumer repo |

## Configuration surface

Every consumer repo gets a `.agent_squad/config.toml`, committed so teammates share a config:

```toml
[squad]
pm_tool = "linear"           # fixed for v1; reserves the name
local_dir = ".agent_squad"

[linear]
team_id       = "fa55c86b-6c78-42e0-9e04-c8a6ecf9cbb2"  # coral: BENCH team
ticket_prefix = "BENCH"
initiative_id = "77940873-41aa-437f-9823-eb8c8efddd5b"
labels        = ["eval-harness", "eval-dataset", "eval-coral-improvement"]

[linear.projects]
calibration = "..."           # Linear project UUIDs for ticket-manager routing
visibility  = "..."
local_dev   = "..."
release     = "..."
general     = "..."

[worktree]
path          = ".worktrees"
branch_prefix = "worker/"
```

**TOML lives in the consumer repo, not the plugin.** Plugin ships zero IDs.

## Customization model

Three layers:

1. **Plugin defaults.** The agents and skills that ship in the plugin. The template workflow.
2. **Consumer TOML** (`.agent_squad/config.toml` in the consumer repo). Overrides scalars: team IDs, paths, labels, ticket prefixes, project UUIDs. Substitution mechanism TBD (`open-questions.md` Q2).
3. **Consumer local files** — with a skill-vs-agent asymmetry validated in Q7:
   - **Skills.** Plugin and local skills coexist cleanly as distinct, namespaced commands. Plugin skills invoke as `/<plugin>:<skill>` (e.g. `/squirtle-squad:linear`); local skills at `<consumer>/.claude/skills/<name>/SKILL.md` invoke unnamespaced as `/<name>`. No precedence rule — different invocation names.
   - **Agents.** Local `.claude/agents/*.md` files do **not** automatically register as Agent-tool subagent_types via `/reload-plugins`. Plugin agents do. For v1 this is not blocking because the squad ships all the agents it needs; for consumer-authored agents, the activation path is unresolved (see `open-questions.md` Q8).

What v1 does *not* ship: named extension slots, append/prepend semantics, or merge-based prompt overlays. If a consumer needs to tweak a plugin prompt beyond what TOML covers, they fork the file into their repo's `.claude/`. Simple mental model; we accept the drift cost in v1 and revisit if it causes real pain.

## How TOML values reach the logic

Skills and agents consult `.agent_squad/config.toml` at runtime as ordinary config I/O — via their existing Bash / Read tool access. **No session-level prompt-template substitution mechanism.** Resolved in [Q2](open-questions.md).

- **Agent prompts reference capabilities, not IDs.** `"use the linear skill to fetch tickets by label"` — no UUIDs in prompt text.
- **Skills own config-dependent behavior.** The `linear` skill parses TOML inside its bash blocks to get team/project/label IDs and builds its own GraphQL queries. Routing rules (which label maps to which project) also live in TOML and get resolved by the skill.
- **Agents with direct config needs** (worker reading worktree path, etc.) use Bash too — same pattern.
- **The LLM never parses TOML.** Only deterministic bash does. No instruction-following risk.

Canonical read pattern:

```bash
TEAM_ID=$(python3 -c 'import tomllib; print(tomllib.load(open(".agent_squad/config.toml","rb"))["linear"]["team_id"])')
```

Likely factored into a `plugins/squirtle-squad/scripts/squad-config` helper once we have a second caller.

### Why this works (short version)

The original Q2 framing assumed prompts had `{{team_id}}` placeholders that needed pre-LLM substitution. Working backwards from the actual coral use case, nothing in any prompt requires pre-substitution — IDs only appear in skill invocations and bash commands, both of which can read config at runtime. The three original candidates (SessionStart hook, CLI, runtime-read) are optimizations of a problem we don't have. See [`open-questions.md` Q2](open-questions.md) for the full reasoning.

## Hierarchy and control flow

V1 is the coral pattern, unchanged:

```
human
  │
  ▼
ticket-manager  ─── reads/writes Linear ───▶  Linear workspace
  │
  │ spawns N in parallel (one per ticket)
  ▼
worker[0]  worker[1]  ...  worker[N]
  │          │                │
  ▼          ▼                ▼
.worktrees/<ticket>-<slug>    (each in its own worktree)
  │          │                │
  ▼          ▼                ▼
PR opened   PR opened    PR opened   ───▶  human reviews & merges
```

One level of agency. Workers don't talk to each other. Ticket-manager does not supervise workers once dispatched.

## Dev loop — running squirtle-squad on itself

Squirtle-squad dogfoods itself, which means running Claude Code inside the squirtle-squad repo must use the plugin from this working tree. Claude Code supports local-path marketplaces as a first-class source, so no symlinks or env-var hacks are needed:

```shell
# One-time — add this repo as a marketplace and install the plugin:
/plugin marketplace add /Users/jamesaud/projects/squirtle-squad
/plugin install squirtle-squad@squirtle-squad

# After editing plugin source in this repo:
/plugin marketplace update squirtle-squad
/reload-plugins
```

Plugins are *copied* to `~/.claude/plugins/cache/` on install — not symlinked — so the `/plugin marketplace update` step is never skippable. Local-dev marketplaces have auto-update disabled by default, which is the correct default for iteration. Full rationale: [`notes/plugin-schema.md`](notes/plugin-schema.md) § "Local dev install & reload loop."

## Boundary with consumer repos

The plugin owns: agent prompts, skill content, the TOML *schema* (what keys are expected).

The consumer repo owns: their `.agent_squad/config.toml` values, their `CLAUDE.md`, any repo-specific skills (like coral's `code-writing`), and any fork-edits of plugin files they chose to make.

Everything in the plugin is designed to be reinstalled/upgraded without touching consumer state.

## Credentials

V1 assumes each user sets personal credentials (`LINEAR_USER_API_KEY`, `GITHUB_TOKEN`, etc.) as environment variables or in a local `.env` file. The plugin documents which env vars it reads; it never ships secrets. No secrets in TOML.

Auth model will evolve — tracked in `open-questions.md` Q6. User API keys are fine for interactive local use but break down for scheduled agents, remote execution, and shared-bot scenarios where the credential shouldn't be tied to a specific human. App tokens / bot tokens / OAuth are candidates for v1.1+. Not in v1.
