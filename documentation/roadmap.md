# Roadmap

Open-ended timeline — quality over speed. Milestones are ordered by dependency, not calendar.

> **2026-04-27 pivot.** v1 dropped the Claude Code marketplace-plugin distribution model. The squad is now distributed as a Python CLI (`agent-squad`) that vendors prompts into the consumer's repo. Past plugin-era milestones (M0–M5) are preserved in [`notes/archive/`](./notes/archive/) for context but are no longer the active plan. The new milestone numbering starts at C1 (CLI 1).

## Where we are

CLI scaffolded and self-hosting:

- `agent-squad init` copies the bundled template into a consumer repo and generates the Claude Code plugin shim.
- `agent-squad add manager <slug>` scaffolds a new manager scope.
- `agent-squad doctor` validates a vendored squad.
- `agent-squad sync` is a stub for v0.2.

The squad's content (ticket-manager, worker, manager, linear, pull-request, assign-worker, linear-graphql.sh) is bundled in `cli/src/agent_squad/template/` and self-dogfooded via a marketplace.json shim at this repo's root.

## C1 — Self-dogfood the new layout

Validate the CLI/vendor model end-to-end on this repo.

- Reload Claude Code plugins; verify `agent-squad:ticket-manager`, `agent-squad:worker`, `agent-squad:manager` register.
- Spawn a worker on a real SQU ticket via `agent-squad:assign-worker`. Confirm the worker reads `.agent_squad/config.toml` correctly under the new `${CLAUDE_PLUGIN_ROOT}` resolution.
- Open a PR against this repo, end-to-end. PR closes a real SQU ticket.

**Exit criterion**: a SQU ticket closed by a worker-opened PR, where the worker was dispatched via the new agent-squad CLI flow (not the old plugin-marketplace path).

## C2 — Manager dogfood

Use the new persistent-manager role on real work.

- Create one manager scope in this repo: `agent-squad add manager <slug>`. Pick a scope that has multi-session continuity (e.g. `cli-distribution` for ongoing CLI release work, or `coral-canary` for the coral integration).
- Fill in `CLAUDE.md`, `goals.md`. Run a session where the manager dispatches at least two workers across two tickets within scope.
- Verify the manager's working-memory files (`journal.md`, `progress.md`) get updated and persist across sessions.

**Exit criterion**: one manager scope has been engaged across ≥2 sessions, dispatched ≥2 workers, and its journal shows continuity (not "fresh start" each session).

## C3 — Implement `agent-squad sync`

The v0.1 stub says "rerun init --force, resolve with git." Replace with a real sync that:

- Diffs the bundled template against the consumer's vendored copy.
- Identifies vendored files that have local edits (compare to the template version the consumer last synced from — store a hash file at `.agent_squad/.template-version` or similar).
- Three-way merges where it can; reports conflicts where it can't.
- Never touches `config.toml` or `managers/<slug>/` — those are consumer-owned.

**Exit criterion**: bumping the CLI version, then `agent-squad sync` in a consumer with a customised vendored prompt, preserves the customisation while applying upstream changes.

## C4 — Coral canary

Install agent-squad in coral-benchmarks; replace coral's existing local skills/agents with the vendored squad. Currently deferred — preserved here as the v1.1+ goal.

- Run `agent-squad init` in coral.
- Migrate coral's `.agent_squad/config.toml` (already exists from earlier work).
- Delete coral's local copies of agents/skills that the squad now provides.
- Verify a real BENCH ticket can be closed by a worker-opened PR via the vendored squad.

**Exit criterion**: coral closes a BENCH ticket via the vendored squad. Coral's local `.claude/agents/` and `.claude/skills/` for the migrated content are empty or deleted.

## C5 — PyPI + `pipx` distribution

`uvx --from git+...` works for v0.1, but a real package on PyPI lowers the install bar.

- Publish `agent-squad` to PyPI.
- Verify `pipx install agent-squad && agent-squad init` works in a fresh repo.
- Update the README install instructions.

**Exit criterion**: `pipx install agent-squad` works against the published package.

## Parking lot (v1.1+)

Ordered roughly by expected value:

- **PR-review-comment polling loop** (BENCH-209). The single biggest UX gap after v1. Becomes a strong candidate for the runner program (long-lived, watches many PRs).
- **Remote runner.** Long-lived service that supervises workers across repos, watches PRs/Linear events, dispatches managers. Likely Go. Consumes the same `.agent_squad/` layout — that's the architectural payoff of the vendor model.
- **`code-writing` as a customizable template skill.** Ship a minimal scaffold; each consumer fills in repo-specific patterns.
- **Non-user auth tokens.** App / bot / OAuth credentials so the squad isn't tied to a specific human. Prerequisite for the runner.
- **Manager-to-manager messaging.** When multiple managers exist in one repo and one's scope touches another's, they should be able to coordinate without going through the human.
- **`agent-squad show <agent>`** — print the resolved prompt with provenance ("lines 1–40 from template, 41–52 local edit"). Becomes mandatory once `sync` supports overrides beyond fork-editing.
- **Reviewer subagent.** Reviews worker PRs before they reach humans.
- **PM tool adapter pattern.** `pm-linear`, `pm-jira`, `pm-github`. Agents reference an abstract PM capability; adapter skills implement it. Unlocks non-Linear consumers.
- **Public open-sourcing.** Polish docs, set up contribution guidelines, drop the "private repo" framing.
