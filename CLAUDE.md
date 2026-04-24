# CLAUDE.md

Contributor-facing orientation for `squirtle-squad`. `README.md` is user-facing; this file is for anyone — human or agent — working *on* the plugin.

This is a pointer doc. Authoritative content lives in [`documentation/`](./documentation); follow the links rather than expecting this file to restate them.

## What squirtle-squad is

A Claude Code plugin that packages a reusable "software engineering team" — a `ticket-manager` agent, a pool of `worker` agents, and supporting skills (`linear`, `pull-request`, `assign-worker`). Installed into any repo with a `.agent_squad/config.toml`, it lets a human drive a swarm of Claude Code workers to implement Linear tickets end-to-end.

V1 audience is Phoebe teammates. The primary v1 milestone is self-dogfooding: `squirtle-squad` uses its own installed plugin to close `SQU-*` tickets via worker-opened PRs against this repo. Full framing, principles, and non-goals: [`documentation/vision.md`](./documentation/vision.md).

## Repo layout

See [`documentation/architecture.md`](./documentation/architecture.md) § "Repo layout" for the authoritative tree and rationale. In short:

- `.claude-plugin/marketplace.json` — marketplace manifest at the repo root.
- `plugins/squirtle-squad/` — the plugin itself (`.claude-plugin/plugin.json`, `agents/`, `skills/`, `scripts/`).
- `documentation/` — strategy, architecture, roadmap, open questions. Not part of the plugin.
- `.agent_squad/config.toml` — consumer config, committed so teammates share it. Present here because the repo dogfoods its own plugin.

Schema details for plugin/marketplace manifests and the auto-discovery convention for `agents/*.md` + `skills/<name>/SKILL.md`: [`documentation/notes/plugin-schema.md`](./documentation/notes/plugin-schema.md).

## Plugin dev loop

The repo dogfoods itself — Claude Code running inside this checkout uses the plugin from its own source tree via a local-path marketplace. One-time setup:

```shell
/plugin marketplace add /Users/jamesaud/projects/squirtle-squad
/plugin install squirtle-squad@squirtle-squad
```

Iteration loop — after editing any plugin source under `plugins/squirtle-squad/`:

```shell
/plugin marketplace update squirtle-squad
/reload-plugins
```

Plugins are *copied* to `~/.claude/plugins/cache/` on install (not symlinked), so the `update` step is never skippable. Local-dev marketplaces have auto-update disabled by default — that's the correct default for iteration. Full rationale and edge cases: [`documentation/notes/plugin-schema.md`](./documentation/notes/plugin-schema.md) § "Local dev install & reload loop."

## `.agent_squad/config.toml` conventions

The plugin ships zero IDs. Everything team-, project-, or repo-specific lives in `.agent_squad/config.toml` in the consumer repo. Keys expected today:

- `[squad]` — `pm_tool`, `local_dir`.
- `[linear]` — `team_id`, `ticket_prefix`, optional `initiative_id` and `labels`.
- `[linear.projects]` — map of project-name → Linear project UUID, consumed by `ticket-manager` routing.
- `[worktree]` — `path`, `branch_prefix`.

Full schema and rationale — including why the LLM never parses TOML and how skill bash blocks consume it at runtime — is in [`documentation/architecture.md`](./documentation/architecture.md) § "Configuration surface" and § "How TOML values reach the logic." The canonical read pattern uses `python3 -c 'import tomllib; ...'` inside skill bash; see the architecture doc for the exact snippet.

If you find yourself about to hardcode a UUID, label, or path in a plugin file, stop — the value belongs in TOML. If TOML doesn't currently expose that key, extend the schema and update the relevant skill, rather than embedding the value.

## Contribution rules

### Branches and worktrees

Workers spawned via `squirtle-squad:assign-worker` create their own worktree under `.claude/worktrees/<name>/` on a fresh branch. When working by hand, follow the same convention: one branch per ticket, prefixed meaningfully (e.g. `squ-17-claude-md`).

### Ticket prefix and routing

All tickets for this repo use the `SQU` prefix. Routing (which Linear project a ticket lands in) is handled by `ticket-manager` reading `[linear.projects]` from `.agent_squad/config.toml` and any routing conventions this file chooses to document. Projects sit under the "Version 1.0" initiative on the squirtlesquad Linear workspace — see [`documentation/roadmap.md`](./documentation/roadmap.md) for the M0 → M6 milestone structure.

### Commit style

Match the existing history (`git log --oneline` to see it). Conventions in use:

- Prefix with a milestone or category tag when applicable: `M4: …`, `docs: …`, `fix(linear skill): …`, `chore: …`.
- Include the ticket identifier in the subject when the commit closes or substantially advances one: `M5: extract ticket-manager agent into plugin (SQU-16)`.
- Trailer line: `Co-authored-by: Claude Opus 4.7 (1M context) <noreply@anthropic.com>` on any commit an agent helped author.

### PR body

Use the `squirtle-squad:pull-request` skill to open PRs — it handles the gh CLI invocation, footer, and Linear-ticket linking. PR bodies should:

- Lead with a short summary of what changed and why.
- Link the ticket via `Closes https://linear.app/squirtlesquad/issue/SQU-<n>/<slug>` so Linear auto-moves it to Done on merge.
- End with the standard Claude Code footer: `Generated with Claude Code` + the `Co-Authored-By` trailer above.

### Quality bar

The quality & architecture principles in [`documentation/vision.md`](./documentation/vision.md) § "Quality & architecture principles" are the bar — minimal surface area, one responsibility per component, strong layer boundaries (plugin ↔ TOML ↔ consumer local), explicit over clever, no half-finished state. If a PR doesn't meet it, it doesn't land.

## When in doubt

- Open questions and unresolved research items: [`documentation/open-questions.md`](./documentation/open-questions.md).
- Roadmap and milestone exit criteria: [`documentation/roadmap.md`](./documentation/roadmap.md).
- Plugin schema / marketplace mechanics: [`documentation/notes/plugin-schema.md`](./documentation/notes/plugin-schema.md).

Keep this file short. When it grows past ~150 lines or starts duplicating what's in `documentation/`, prune or split (e.g. `plugins/squirtle-squad/CLAUDE.md` for plugin-internal guidance).
