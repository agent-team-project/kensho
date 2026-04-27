# CLAUDE.md

Contributor-facing orientation for `agent-squad`. `README.md` is user-facing; this file is for anyone — human or agent — working *on* the CLI.

This is a pointer doc. Authoritative content lives in [`documentation/`](./documentation); follow the links rather than expecting this file to restate them.

## What agent-squad is

A Python CLI that vendors a reusable software-engineering agent squad — `ticket-manager`, persistent `manager`s, ephemeral `worker`s, and supporting skills (`linear`, `pull-request`, `assign-worker`) — into any repo.

V1 audience is Phoebe teammates. The primary v1 milestone is self-dogfooding: agent-squad uses its own CLI to vendor itself into this repo and close `SQU-*` tickets via worker-opened PRs. Full framing, principles, and non-goals: [`documentation/vision.md`](./documentation/vision.md).

> **2026-04-27 pivot.** v1 dropped the Claude Code marketplace-plugin distribution and pivoted to a CLI that vendors the squad into the consumer's repo. Plugin-era docs are preserved at [`documentation/notes/archive/`](./documentation/notes/archive/) for context. New milestone numbering starts at C1.

## Repo layout

See [`documentation/architecture.md`](./documentation/architecture.md) for the authoritative tree. In short:

- `cli/` — the Python package. `cli/src/agent_squad/cli.py` is the entrypoint; `cli/src/agent_squad/template/` is the canonical squad content (bundled with the wheel).
- `documentation/` — strategy, architecture, roadmap, open questions. Not bundled with the CLI.
- `.agent_squad/config.toml` — this repo's own consumer config (we self-dogfood).
- `.claude-plugin/marketplace.json` — points at `cli/src/agent_squad/template/` so the canonical squad content is live in this repo's Claude Code session without a vendor copy.

The bundled template at `cli/src/agent_squad/template/` follows Claude Code's plugin layout: `agents/*.md` and `skills/<name>/SKILL.md` auto-discover. `.claude-plugin/plugin.json` lives inside the template directory.

## CLI dev loop

From repo root:

```sh
cd cli
uv run --with-editable . agent-squad --help
```

Or install editably and run:

```sh
cd cli && uv pip install -e .
agent-squad --help
```

Smoke-test `init` against a tmp dir:

```sh
mkdir /tmp/squad-smoke && agent-squad init --target /tmp/squad-smoke
```

After editing any template file under `cli/src/agent_squad/template/`, run `/reload-plugins` in your Claude Code session — the marketplace shim points at the template path, so edits are immediately live.

## `.agent_squad/config.toml` conventions

The CLI ships zero IDs in the template. Every consumer-specific value lives in `.agent_squad/config.toml`. Keys expected today:

- `[squad]` — `pm_tool`, `local_dir`.
- `[linear]` — `team_id`, `ticket_prefix`, optional `initiative_id` and `labels`.
- `[linear.projects]` — map of project-name → Linear project UUID, consumed by `ticket-manager` routing.
- `[worktree]` — `path`, `branch_prefix`.

Schema details and the rationale for runtime TOML reads (vs prompt-template substitution): [`documentation/architecture.md`](./documentation/architecture.md) § "Configuration surface" and § "How TOML values reach the logic." The canonical pattern uses `python3 -c 'import tomllib; ...'` inside skill bash.

If you find yourself about to hardcode a UUID, label, or path in a template file, stop — the value belongs in TOML. If TOML doesn't currently expose that key, extend the schema and update the relevant skill, rather than embedding the value.

## Contribution rules

### Branches and worktrees

Workers spawned via `agent-squad:assign-worker` create their own worktree under `.claude/worktrees/<name>/` on a fresh branch. When working by hand, follow the same convention: one branch per ticket, prefixed meaningfully (e.g. `squ-17-claude-md`).

### Ticket prefix and routing

All tickets for this repo use the `SQU` prefix. Routing (which Linear project a ticket lands in) is handled by `ticket-manager` reading `[linear.projects]` from `.agent_squad/config.toml`. Projects sit under the "Version 1.0" initiative on the squirtlesquad Linear workspace — see [`documentation/roadmap.md`](./documentation/roadmap.md) for milestone structure.

### Commit style

Match the existing history (`git log --oneline` to see it). Conventions:

- Prefix with a milestone or category tag: `C1: …`, `docs: …`, `fix(linear skill): …`, `chore: …`.
- Include the ticket identifier when the commit closes or substantially advances one: `C2: scaffold manager dogfood scope (SQU-21)`.
- Trailer line: `Co-authored-by: Claude Opus 4.7 (1M context) <noreply@anthropic.com>` on any commit an agent helped author.

### PR body

Use the `agent-squad:pull-request` skill to open PRs — it handles the gh CLI invocation, footer, and Linear-ticket linking. PR bodies should:

- Lead with a short summary of what changed and why.
- Link the ticket via `Closes https://linear.app/squirtlesquad/issue/SQU-<n>/<slug>` so Linear auto-moves it to Done on merge.
- End with the standard Claude Code footer.

### Quality bar

The principles in [`documentation/vision.md`](./documentation/vision.md) § "Quality & architecture principles" are the bar — minimal surface area, one responsibility per component, strong layer boundaries (CLI ↔ template ↔ vendored copy ↔ consumer extensions), explicit over clever, no half-finished state. If a PR doesn't meet it, it doesn't land.

## When in doubt

- Open questions and unresolved research items: [`documentation/open-questions.md`](./documentation/open-questions.md).
- Roadmap and milestone exit criteria: [`documentation/roadmap.md`](./documentation/roadmap.md).
- Plugin-era history: [`documentation/notes/archive/`](./documentation/notes/archive/).

Keep this file short. When it grows past ~150 lines or starts duplicating what's in `documentation/`, prune or split.
