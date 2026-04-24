# Open Questions

Research tasks and decisions not yet committed. Each has an owner, a blocker relationship, and a "next action" so it's clear how to close it.

---

## Q1 — Plugin manifest & marketplace schema

**Status**: RESOLVED 2026-04-24 via [SQU-5](https://linear.app/squirtlesquad/issue/SQU-5). Full schema reference with worked examples: [`notes/plugin-schema.md`](notes/plugin-schema.md).

**Summary of answers:**

1. **`plugin.json` required fields**: `name`, `description`. Optional: `version`, `author {name, email}`, `homepage`, `repository`, `license`. Location: `.claude-plugin/plugin.json`.
2. **`marketplace.json` format**: Location `.claude-plugin/marketplace.json`. Required: `name`, `description`, `plugins[]`. Each plugin entry: `name`, `description`, `source {source, url, path, ref?}`, optional `version` and `author`.
3. **URL resolution**: `/plugin marketplace add <owner>/<repo>` fetches `.claude-plugin/marketplace.json` from the default branch. Supports `#ref` suffix for pinning (`<owner>/<repo>#v1.0.0` or `#branch`). Also accepts full Git URLs and local paths.
4. **Agent + skill discovery**: Convention-based. Claude Code auto-discovers `agents/*.md` and `skills/<name>/SKILL.md`. No `plugin.json` entries per agent/skill.
5. **Hooks in plugins**: Supported via `hooks/hooks.json` at plugin root. SessionStart hooks supported as `type: "command"`; can write files and set env vars via `$CLAUDE_ENV_FILE`.
6. **Versioning**: `version` field in `plugin.json` is optional; without it, version is the commit SHA. Consumers pin via `#ref` on `marketplace add`. `/plugin update` refreshes marketplaces and updates installed plugins. Marketplace-level version-range constraints are not documented — for v1 we pin by ref or SHA.

**Remaining gaps after M1 smoke test:**

- **Timing of plugin-shipped SessionStart hooks relative to agent-prompt loading.** Still not directly validated — we don't ship a hook yet. Tracked as a verification item for [SQU-6](https://linear.app/squirtlesquad/issue/SQU-6) (Q2 spike), which will ship a test hook for this purpose. This is the single most important open input for Q2 option (a).

**Resolved by M1 smoke test ([SQU-9](https://linear.app/squirtlesquad/issue/SQU-9)):**

- ✅ **Install command shape**: the short form `/plugin install squirtle-squad` works without the `@squirtle-squad` suffix when marketplace and plugin share a name. The suffix is only needed to disambiguate when a plugin name exists in multiple marketplaces.

**Next action**: Q1 done. Unblocks [SQU-6](https://linear.app/squirtlesquad/issue/SQU-6) (Q2 spike), [SQU-7](https://linear.app/squirtlesquad/issue/SQU-7) (Q3 dev install), [SQU-8](https://linear.app/squirtlesquad/issue/SQU-8) (M1 scaffold).

---

## Q2 — Config substitution mechanism

**Status**: three candidates, none tested.
**Blocks**: M3 (linear skill needs parameterized IDs).

| Option | Pros | Cons | Unknowns |
|---|---|---|---|
| (a) SessionStart hook renders templates | Transparent to user; no runtime token cost; user edits TOML and next session picks it up | Depends on hook reliability and timing; hook must run before agents load | Can plugin-shipped hooks write to `~/.claude/plugins/.../resolved/`? Do SessionStart hooks fire before agent prompts are cached? Can they fail gracefully? |
| (b) `squirtle-squad configure` CLI | Predictable; no hook magic; easy to debug (user can read the generated files) | User must re-run on every TOML change; one more tool to distribute and version | How does the CLI get installed alongside the plugin — same install or separate? |
| (c) Runtime LLM read of TOML | Zero machinery; agents just read the file each invocation | Token overhead per invocation; depends on LLM following instructions consistently across 10+ config keys | Token cost on a realistic coral-sized config? Instruction-following reliability for nested keys? |

**Recommended spike**:
1. Write a trivial SessionStart hook that writes a timestamp to a file; verify it fires and agents can read the file.
2. Estimate (c)'s token cost by measuring a sample TOML rendering.
3. Sketch (b)'s CLI surface.

**Decision criteria**: pick (a) if the hook fires reliably and can write files. Fall back to (b) if (a) is flaky. Reserve (c) as an escape hatch if both break.

**Next action**: spike during M0.

---

## Q3 — Local dev install loop

**Status**: RESOLVED 2026-04-24 via [SQU-7](https://linear.app/squirtlesquad/issue/SQU-7). Worked instructions live in [`notes/plugin-schema.md`](notes/plugin-schema.md) under "Local dev install & reload loop."

**Decision**: Claude Code supports local-path marketplaces as a first-class source. We use that — no symlinks, no env-var hacks.

**Dev loop:**

```shell
# One-time setup (from any cwd):
/plugin marketplace add /Users/jamesaud/projects/squirtle-squad
/plugin install squirtle-squad@squirtle-squad

# After editing plugin source in this repo:
/plugin marketplace update squirtle-squad
/reload-plugins
```

**Behavior we rely on:**

- `/plugin marketplace add <local-path>` accepts a directory containing `.claude-plugin/marketplace.json`, or a direct path to a `marketplace.json` file.
- Plugins are *copied* into `~/.claude/plugins/cache/` on install — not symlinked. Edits to the source tree are invisible until the marketplace is refreshed.
- Local-development marketplaces have **auto-update disabled by default**, so we always explicitly run `/plugin marketplace update` when iterating. This is the correct default; we don't want auto-refresh clobbering an in-progress edit.
- `/reload-plugins` applies loaded plugin changes in the current session without a Claude Code restart.

**Self-dogfooding note**: when running Claude Code inside this repo, plugin-provided agents operate on `$PWD` (this repo). The cached plugin files live in `~/.claude/plugins/cache/`, but skills that read `.agent_squad/config.toml` read from `$PWD` — exactly what we want.

**Next action**: Q3 done. Ready for M1 scaffold ([SQU-8](https://linear.app/squirtlesquad/issue/SQU-8)) and smoke test ([SQU-9](https://linear.app/squirtlesquad/issue/SQU-9)).

---

## Q4 — Naming

**Status**: undecided; low priority.

"squirtle-squad" is the internal codename. If the plugin ever goes public, we may want a name that is (a) not a Pokémon trademark reference, and (b) descriptive enough to be searchable.

**Next action**: revisit when open-sourcing is on the roadmap. Cheap to rename today (private repo, one commit), cheap next year, expensive once external consumers depend on it.

---

## Q5 — Plugin versioning & upgrade model

**Status**: partially covered by Q1.

When the plugin ships a change, how do consumers control when they pick it up?

- Does the marketplace manifest support version constraints?
- Is `/plugin update` atomic or does the user have to re-run `/plugin install`?
- What happens if we ship a breaking change to an agent prompt and coral was depending on the old behavior?

For v1 this is mostly theoretical (coral is the only consumer, we control both sides). But if squirtle-squad-on-itself and coral pin different versions of the same plugin, we need a story.

**Next action**: answer as part of Q1 research.

---

## Q6 — Credentials handling & auth model evolution

**Status**: v1 direction clear; v1.1+ direction open.

**V1 plan.** Plugin documents which env vars it reads (`LINEAR_USER_API_KEY`, `GITHUB_TOKEN`, etc.). Consumers set them however they want — `.env`, shell, keychain. No secrets in TOML. Every user uses their personal Linear API key.

**V1.1+ open question.** User API keys are fine for interactive local use but break down for:

- Scheduled agents that run without a user session.
- Shared "bot" accounts that should take actions as the squad rather than as a specific teammate.
- CI or remote execution where pinning credentials to one user is awkward.
- Attribution: a worker running under user X's key shows up in Linear as X commenting, not as "the squad." Sometimes that's desirable, sometimes not.

Directions to explore: Linear OAuth apps, Linear admin API tokens, per-repo service accounts. Each has tradeoffs around scope, rotation, and user attribution.

**Next action**: V1 confirms env-var convention during M3. V1.1+ auth model is a discrete project later.

---

## Q7 — Plugin vs. consumer-local resolution semantics

**Status**: RESOLVED 2026-04-24 via [SQU-9](https://linear.app/squirtlesquad/issue/SQU-9). Mixed results — skills and agents behave differently.

**Test setup**: the plugin ships `plugins/squirtle-squad/skills/placeholder/SKILL.md` (responds "pong") and `plugins/squirtle-squad/agents/placeholder.md` (greeting). We created local fixtures at `.claude/skills/placeholder/SKILL.md` and `.claude/agents/placeholder.md` with intentionally distinct content. Ran `/reload-plugins`, invoked each form.

**Skills — coexist cleanly as distinct namespaced commands.**

| Invocation | Which ran |
|---|---|
| `/placeholder` | Local skill (`.claude/skills/placeholder/SKILL.md`) |
| `/squirtle-squad:placeholder` | Plugin skill (`plugins/squirtle-squad/skills/placeholder/SKILL.md`) |

Both appear as separate entries in Claude Code's available-skills list (the local one unnamespaced, the plugin one as `squirtle-squad:placeholder`). No shadowing, no precedence needed — different invocation names.

**Agents — local `.claude/agents/*.md` does NOT automatically register as a subagent.**

| `subagent_type` | Result |
|---|---|
| `"squirtle-squad:placeholder"` | Plugin agent runs; returns expected greeting |
| `"placeholder"` | **"Agent type 'placeholder' not found"** |

The local agent file existed on disk and `/reload-plugins` ran, but the Agent tool's available-subagent list did not include the local agent. Plugin agents did register (namespaced as `<plugin>:<name>`).

**Implication for `architecture.md` customization model**: skills layer cleanly via Claude Code's native mechanism, but agents have a different (and currently unknown) activation path. See Q8.

**Next action**: Q7 resolved. `architecture.md` customization model updated to reflect the skill/agent split. Local-agent activation tracked in Q8.

---

## Q8 — Local agent activation mechanism

**Status**: NEW — surfaced by Q7 testing on 2026-04-24.
**Blocks**: nothing in v1 (the squad ships all the agents it needs). Becomes relevant when consumers want to add their own custom agents — a v1.1+ concern.

Q7 established that local `.claude/agents/<name>.md` files do NOT automatically register as Agent-tool subagent_types via `/reload-plugins`, even though local `.claude/skills/<name>/SKILL.md` files DO register as skills. Plugin agents register cleanly (namespaced as `<plugin>:<name>`).

So there's an asymmetry: skills have both a plugin path and a local path; agents currently only seem to have a plugin path (via the subagent invocation surface).

**Candidates to investigate when a consumer agent need arises:**

- Explicit enablement via `.claude/settings.json` (maybe an `"agents"` array, mirroring `"enabledPlugins"`).
- A separate `/agent enable <name>` command.
- Local agents activated through a different invocation path (direct prompting, Task tool with an explicit file reference, etc.) rather than the Agent tool's subagent_type list.
- Maybe local agents require being defined via a plugin (which would imply consumers wrap their local agents in a trivial plugin of their own — viable but heavy).

**Next action**: No v1 work. Investigate when we first need consumer-authored agents — likely v1.1+.
