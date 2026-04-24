# Claude Code Plugin Schema Reference

Worked examples and field reference for the squirtle-squad plugin. Produced from Q1 research ([SQU-5](https://linear.app/squirtlesquad/issue/SQU-5)); verified against current Claude Code docs on 2026-04-24.

## Authoritative sources

- [Plugins Reference](https://code.claude.com/docs/en/plugins-reference.md)
- [Create Plugins](https://code.claude.com/docs/en/plugins.md)
- [Create and Distribute a Plugin Marketplace](https://code.claude.com/docs/en/plugin-marketplaces.md)
- [Discover and Install Plugins](https://code.claude.com/docs/en/discover-plugins.md)
- [Hooks Reference](https://code.claude.com/docs/en/hooks.md)
- [Official Anthropic Marketplace example](https://github.com/anthropics/claude-plugins-official/blob/main/.claude-plugin/marketplace.json)

## Directory layout

A plugin repo that is also a self-hosted marketplace (marketplace-of-one) looks like this:

```
squirtle-squad/
├── .claude-plugin/
│   ├── plugin.json            # plugin manifest (this repo's plugin)
│   └── marketplace.json       # marketplace manifest (self-hosts this plugin)
├── agents/
│   ├── ticket-manager.md      # agent — auto-discovered
│   └── worker.md              # agent — auto-discovered
├── skills/
│   ├── linear/
│   │   └── SKILL.md           # skill — auto-discovered
│   ├── pull-request/
│   │   └── SKILL.md
│   └── assign-worker/
│       └── SKILL.md
├── hooks/
│   └── hooks.json             # optional — hook declarations
└── README.md
```

**Key rule**: agents under `agents/*.md` and skills under `skills/<name>/SKILL.md` are auto-discovered by convention. There is **no** explicit registration step in `plugin.json` — the file tree IS the registration.

## `plugin.json` — minimal working example

Required fields only: `name` and `description`.

```json
{
  "name": "squirtle-squad",
  "description": "Reusable agents and skills for driving a software-engineering workflow (ticket-manager + worker pool) end-to-end against Linear and git."
}
```

With optional metadata (recommended once we start tagging releases):

```json
{
  "name": "squirtle-squad",
  "description": "Reusable agents and skills for driving a software-engineering workflow (ticket-manager + worker pool) end-to-end against Linear and git.",
  "version": "0.0.1",
  "author": {
    "name": "James Audretsch",
    "email": "james.audretsch@phoebe.ai"
  },
  "homepage": "https://github.com/jamesaud/squirtle-squad",
  "repository": "https://github.com/jamesaud/squirtle-squad"
}
```

Location: `.claude-plugin/plugin.json`.

## `marketplace.json` — minimal working example

Required: `name`, `description`, `plugins[]`. Each plugin entry needs `name`, `description`, `source`.

```json
{
  "$schema": "https://anthropic.com/claude-code/marketplace.schema.json",
  "name": "squirtle-squad",
  "description": "Self-hosted marketplace for the squirtle-squad plugin.",
  "owner": {
    "name": "James Audretsch",
    "email": "james.audretsch@phoebe.ai"
  },
  "plugins": [
    {
      "name": "squirtle-squad",
      "description": "Reusable agents and skills for driving a software-engineering workflow (ticket-manager + worker pool) end-to-end against Linear and git.",
      "version": "0.0.1",
      "source": {
        "source": "git-subdir",
        "url": "https://github.com/jamesaud/squirtle-squad.git",
        "path": "."
      },
      "homepage": "https://github.com/jamesaud/squirtle-squad"
    }
  ]
}
```

Location: `.claude-plugin/marketplace.json`.

**Notes:**

- `source.source: "git-subdir"` + `path: "."` is the pattern for a plugin at the repo root.
- If we ever restructure to `plugins/squirtle-squad/`, change `path` to match.
- `version` in the plugin entry is optional; without it, Claude Code uses the commit SHA.

## Install & update commands (consumer side)

```bash
# Add our marketplace (reads .claude-plugin/marketplace.json from default branch)
/plugin marketplace add jamesaud/squirtle-squad

# Pin to a ref
/plugin marketplace add jamesaud/squirtle-squad#v0.0.1

# Install the plugin
/plugin install squirtle-squad

# Update
/plugin update
```

**Open**: when marketplace and plugin share the name `squirtle-squad`, is the install command `/plugin install squirtle-squad` or `/plugin install squirtle-squad@squirtle-squad`? Claude Code docs suggest the `@<marketplace>` suffix is needed only to disambiguate when a plugin name exists in multiple marketplaces. For a single marketplace, the short form should work. **Validate in M1 smoke test ([SQU-9](https://linear.app/squirtlesquad/issue/SQU-9)).**

## Local dev install & reload loop

For developing the plugin against its own source tree (including self-dogfooding at M6), Claude Code supports local-path marketplaces as a first-class source — no symlinks or env vars needed.

```shell
# Add this repo as a marketplace (uses .claude-plugin/marketplace.json):
/plugin marketplace add /Users/jamesaud/projects/squirtle-squad

# Install the plugin from it:
/plugin install squirtle-squad@squirtle-squad

# After editing plugin source in this repo, refresh:
/plugin marketplace update squirtle-squad
/reload-plugins
```

**Key behaviors:**

- Plugins are **copied** to `~/.claude/plugins/cache/` on install (not symlinked). Edits to the source tree are invisible until the next `/plugin marketplace update`.
- Local-development marketplaces have **auto-update disabled by default** — the correct default for iteration, but it means the `update` step is never skippable.
- `/reload-plugins` applies changes in the current session without a Claude Code restart. Use it after every `/plugin marketplace update`.
- If things get weird after a refactor, `rm -rf ~/.claude/plugins/cache` + reinstall is the documented clean-slate reset.

Source: [Discover and install prebuilt plugins](https://code.claude.com/docs/en/discover-plugins.md).

## Field summary

| Field | Required | Location | Notes |
|---|---|---|---|
| `plugin.json::name` | Yes | Plugin manifest | Becomes the skill namespace prefix (e.g. `/squirtle-squad:linear`) |
| `plugin.json::description` | Yes | Plugin manifest | Shown in `/plugin` UI |
| `plugin.json::version` | No | Plugin manifest | Without it, version == commit SHA |
| `plugin.json::author` | No | Plugin manifest | `{name, email}` metadata |
| `marketplace.json::name` | Yes | Marketplace manifest | Marketplace identifier |
| `marketplace.json::description` | Yes | Marketplace manifest | |
| `marketplace.json::plugins[]` | Yes | Marketplace manifest | Each entry needs `name`, `description`, `source` |
| `marketplace.json::plugins[].source` | Yes | Marketplace manifest | `{source, url, path, ref?}` |
| `hooks/hooks.json` | Optional | Plugin root | Hook declarations (see below) |
| `agents/<name>.md` | — | Plugin root | Auto-discovered; no manifest entry |
| `skills/<name>/SKILL.md` | — | Plugin root | Auto-discovered; no manifest entry |

## Hooks in plugins

Plugins can ship hooks via `hooks/hooks.json` at the plugin root. Supported types: `command`, `http`, `mcp_tool`, `prompt`, `agent`.

For our Q2 SessionStart-based TOML substitution candidate, the relevant form is `type: "command"`. A plugin-shipped SessionStart hook can write files and set env vars via `$CLAUDE_ENV_FILE`.

**Important unresolved timing question**: Claude Code docs do not explicitly state whether a plugin-shipped SessionStart hook runs *before* agent prompts are loaded into context. That matters for Q2 (a) — if the hook runs after prompts are already cached, it can't substitute template values into them at session start.

This is the single biggest unknown blocking a Q2 decision. It's tracked as a sub-verification of M1 smoke test ([SQU-9](https://linear.app/squirtlesquad/issue/SQU-9)) and should also be considered when working Q2 ([SQU-6](https://linear.app/squirtlesquad/issue/SQU-6)).

## Known gaps in official docs

1. **Hook timing vs. agent prompt loading** — not explicitly documented; integration test required.
2. **Marketplace-level version constraints** (e.g. `^1.0`, `>=1.2`) — not documented. For v1 we will pin by git ref or SHA.
3. **Install command disambiguation** — the `@<marketplace>` suffix behavior when names collide is referenced in docs but edge cases (marketplace and plugin sharing a name) are not worked through.

## Last verified

2026-04-24 via claude-code-guide agent + WebFetch against docs.claude.com.
