# Claude Code Plugin Schema Reference

Worked examples and field reference for the squirtle-squad plugin. Produced from Q1 ([SQU-5](https://linear.app/squirtlesquad/issue/SQU-5)) and Q3 ([SQU-7](https://linear.app/squirtlesquad/issue/SQU-7)); verified against current Claude Code docs on 2026-04-24.

## Authoritative sources

- [Plugins Reference](https://code.claude.com/docs/en/plugins-reference.md)
- [Create Plugins](https://code.claude.com/docs/en/plugins.md)
- [Create and Distribute a Plugin Marketplace](https://code.claude.com/docs/en/plugin-marketplaces.md)
- [Discover and Install Plugins](https://code.claude.com/docs/en/discover-plugins.md)
- [Hooks Reference](https://code.claude.com/docs/en/hooks.md)

## Canonical layout

The docs show plugins living in a subdirectory of the marketplace repo (typically `plugins/<name>/`), not at the marketplace root. We follow that convention — it cleanly separates plugin content from non-plugin content (documentation, CI config, etc.) in the same repo.

```
squirtle-squad/                       # repo root — also marketplace root
├── .claude-plugin/
│   └── marketplace.json              # ONLY the marketplace manifest lives here
├── plugins/
│   └── squirtle-squad/                # the plugin
│       ├── .claude-plugin/
│       │   └── plugin.json            # plugin manifest
│       ├── agents/
│       │   ├── ticket-manager.md      # auto-discovered (convention)
│       │   └── worker.md
│       ├── skills/
│       │   ├── linear/
│       │   │   └── SKILL.md           # auto-discovered (convention)
│       │   ├── pull-request/
│       │   │   └── SKILL.md
│       │   └── assign-worker/
│       │       └── SKILL.md
│       └── hooks/                     # optional
│           └── hooks.json
├── documentation/                     # NOT part of the plugin
├── .gitignore
└── README.md
```

**Discovery is convention-based.** Agents under `agents/*.md` and skills under `skills/<name>/SKILL.md` are auto-registered by Claude Code. The `plugin.json` manifest does not list them.

## `plugin.json` — minimal working example

Location: `plugins/squirtle-squad/.claude-plugin/plugin.json`.

Required fields: `name`, `description`.

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

Notes:
- `version` is optional. If set, consumers only receive updates when it changes. If omitted, every commit SHA counts as a new version.
- `name` becomes the skill namespace prefix: skills are invoked as `/squirtle-squad:<skill>`.

## `marketplace.json` — minimal working example

Location: `.claude-plugin/marketplace.json` (at the **repo root**, not inside `plugins/`).

```json
{
  "name": "squirtle-squad",
  "owner": {
    "name": "James Audretsch",
    "email": "james.audretsch@phoebe.ai"
  },
  "metadata": {
    "description": "Self-hosted marketplace for the squirtle-squad plugin.",
    "version": "0.0.1"
  },
  "plugins": [
    {
      "name": "squirtle-squad",
      "source": "./plugins/squirtle-squad",
      "description": "Reusable agents and skills for driving a software-engineering workflow (ticket-manager + worker pool) end-to-end against Linear and git.",
      "version": "0.0.1",
      "author": {
        "name": "James Audretsch",
        "email": "james.audretsch@phoebe.ai"
      },
      "homepage": "https://github.com/jamesaud/squirtle-squad"
    }
  ]
}
```

## Why relative-path `source`?

Two workflows, one manifest:

- **Consumers**: `/plugin marketplace add jamesaud/squirtle-squad` pulls the whole repo from GitHub; the relative path `./plugins/squirtle-squad` resolves inside the fetched repo.
- **Local dev**: `/plugin marketplace add /Users/jamesaud/projects/squirtle-squad` reads this same manifest; relative path resolves against the local checkout, so working-tree edits become installable on the next `/plugin marketplace update`.

The alternative (`source: {source: "github", repo: "jamesaud/squirtle-squad"}`) would work for consumers but force every local-dev iteration to go through `git push`. Relative path is strictly better for our case.

**Caveat** (documented): relative-path sources do *not* work when a marketplace is added via a direct URL to `marketplace.json` (e.g. `/plugin marketplace add https://.../marketplace.json`). We don't plan to distribute that way, so this is fine.

## Plugin source types — full reference

From [Create and Distribute a Plugin Marketplace](https://code.claude.com/docs/en/plugin-marketplaces.md):

| `source` form | Use when | Example |
|---|---|---|
| Relative path string | Plugin lives in the same repo as the marketplace | `"source": "./plugins/squirtle-squad"` |
| `{source: "github", repo}` | Plugin lives in a separate GitHub repo | `{"source": "github", "repo": "owner/plugin-repo"}` |
| `{source: "url", url}` | Plugin lives in a non-GitHub git repo | `{"source": "url", "url": "https://gitlab.com/team/plugin.git"}` |
| `{source: "git-subdir", url, path}` | Plugin is a subdir inside a larger external repo (sparse clone) | `{"source": "git-subdir", "url": "https://github.com/acme/monorepo.git", "path": "tools/plugin"}` |
| `{source: "npm", package, version?}` | Plugin distributed via npm | `{"source": "npm", "package": "@acme/plugin"}` |

All git-based forms support `ref` (branch/tag) and `sha` (exact commit) fields for pinning.

## Install & update commands (consumer side)

```shell
# Add our marketplace (reads .claude-plugin/marketplace.json from default branch):
/plugin marketplace add jamesaud/squirtle-squad

# Pin to a ref:
/plugin marketplace add jamesaud/squirtle-squad#v0.0.1

# Install the plugin (marketplace and plugin both named "squirtle-squad"):
/plugin install squirtle-squad@squirtle-squad

# Update (reloads marketplace listing, updates installed plugins if auto-update is on):
/plugin marketplace update squirtle-squad
/reload-plugins
```

**Unverified**: when the marketplace and plugin share the name `squirtle-squad`, the `@squirtle-squad` suffix is presumably needed to disambiguate (standard install syntax is `plugin-name@marketplace-name`). Whether `/plugin install squirtle-squad` alone works as shorthand is not documented. To verify at M1 smoke test ([SQU-9](https://linear.app/squirtlesquad/issue/SQU-9)).

## Local dev install & reload loop

For developing the plugin against its own source tree (including self-dogfooding at M6):

```shell
# Add this repo as a marketplace:
/plugin marketplace add /Users/jamesaud/projects/squirtle-squad

# Install:
/plugin install squirtle-squad@squirtle-squad

# Iterate — edit plugin source, then:
/plugin marketplace update squirtle-squad
/reload-plugins
```

**Key behaviors:**

- Plugins are **copied** to `~/.claude/plugins/cache/` on install — not symlinked. Edits are invisible until `/plugin marketplace update`.
- Local-development marketplaces have **auto-update disabled by default**. Correct for iteration; the `update` step is never skippable.
- `/reload-plugins` applies changes in-session without a Claude Code restart. Run it after every `update`.
- Clean-slate reset: `rm -rf ~/.claude/plugins/cache` and reinstall.

## Field summary

| Field | Required | Location | Notes |
|---|---|---|---|
| `plugin.json::name` | Yes | `plugins/<name>/.claude-plugin/plugin.json` | Becomes skill namespace prefix (`/<name>:<skill>`) |
| `plugin.json::description` | Yes | Plugin manifest | Shown in `/plugin` UI |
| `plugin.json::version` | No | Plugin manifest | Without it, version == commit SHA |
| `plugin.json::author` | No | Plugin manifest | `{name, email}` metadata |
| `marketplace.json::name` | Yes | `.claude-plugin/marketplace.json` | Marketplace identifier |
| `marketplace.json::owner` | Yes | Marketplace manifest | `{name, email?}` |
| `marketplace.json::plugins[]` | Yes | Marketplace manifest | Each entry needs at minimum `name` + `source` |
| `marketplace.json::metadata.pluginRoot` | No | Marketplace manifest | Prefix for relative `source` paths |
| Plugin entry `::source` | Yes (per entry) | Marketplace manifest | String (relative path) or object (see source types above) |
| `hooks/hooks.json` | Optional | Plugin root | Hook declarations |
| `agents/<name>.md` | — | Plugin root | Auto-discovered; no manifest entry |
| `skills/<name>/SKILL.md` | — | Plugin root | Auto-discovered; no manifest entry |

## Hooks in plugins

Plugins can ship hooks via `hooks/hooks.json` at the plugin root. Supported types: `command`, `http`, `mcp_tool`, `prompt`, `agent`.

For our Q2 SessionStart-based TOML substitution candidate, the relevant form is `type: "command"`. A plugin-shipped SessionStart hook can write files and set env vars via `$CLAUDE_ENV_FILE`.

**Important unresolved timing question**: Claude Code docs don't explicitly state whether a plugin-shipped SessionStart hook runs *before* agent prompts are loaded into context. That matters for Q2 (a) — if the hook runs after prompts are cached, it can't substitute template values at session start.

Tracked as a verification item in [SQU-9](https://linear.app/squirtlesquad/issue/SQU-9) (M1 smoke test) and considered during [SQU-6](https://linear.app/squirtlesquad/issue/SQU-6) (Q2 spike).

## Known gaps in official docs

1. **Hook timing vs. agent prompt loading** — not explicitly documented; integration test required during Q2 spike ([SQU-6](https://linear.app/squirtlesquad/issue/SQU-6)).
2. **Marketplace-level version constraints** (e.g. `^1.0`, `>=1.2`) — not documented. For v1 we pin by git ref or SHA.
3. **Install command disambiguation** — validated in M1 smoke test: `/plugin install squirtle-squad` works *without* the `@squirtle-squad` suffix when marketplace and plugin share a name. Suffix only needed to disambiguate across marketplaces.
4. **Local agent activation path** — surfaced in Q7: local `.claude/agents/<name>.md` does NOT register as an Agent-tool subagent via `/reload-plugins`, unlike local skills. Tracked as Q8.
5. **`/reload-plugins` skill count display.** After an install, the reload status line read `4 plugins · 0 skills · 6 agents · 0 hooks` even though the plugin skill WAS loaded and invokable. Likely a delta-vs-total display quirk; not a functional issue, just minor UI noise worth remembering if it ever becomes confusing during debugging.

## Last verified

2026-04-24 via claude-code-guide agent + WebFetch against docs.claude.com (Q1) and direct WebFetch against plugin-marketplaces docs (Q3).
