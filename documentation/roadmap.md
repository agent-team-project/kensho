# Roadmap

Open-ended timeline — quality over speed. Milestones are ordered by dependency, not calendar. Each milestone has a concrete exit criterion you can point at.

> **2026-04-24 pivot.** v1 is now *self-dogfooding only* — the coral-benchmarks canary is deferred to v1.1+. Milestone exit criteria below still describe "extract from coral" — that's because coral is the source-of-truth for the prompts we're adapting, not because coral is the validation target. Validation runs against the squirtlesquad Linear workspace using squirtle-squad's own `.agent_squad/config.toml`. See [vision.md](./vision.md) for rationale.

## M0 — Research & decisions (no code)

Answer the three blocking questions from `open-questions.md`:

- **Q1** Plugin manifest schema: fetch current Claude Code docs for `.claude-plugin/plugin.json` and self-hosted marketplace format.
- **Q2** Config substitution mechanism: spike SessionStart hook, CLI, and runtime-read approaches. Pick one.
- **Q3** Local dev install loop: verify how a repo can use a plugin whose source lives in that same repo.

**Exit criterion**: `open-questions.md` has committed answers for Q1, Q2, Q3.

## M1 — Skeleton

- Scaffold `.claude-plugin/plugin.json` + `.claude-plugin/marketplace.json` per the Q1 findings.
- Empty `agents/` and `skills/` directories with one placeholder each.
- Install into a throwaway test repo; confirm Claude Code picks the plugin up.

**Exit criterion**: `/plugin install squirtle-squad` succeeds in a fresh repo and the placeholder agent is invocable.

## M2 — Extract `pull-request` skill

Most generic coral skill (36 lines, minimal coupling). First extraction as a forcing function for the install/upgrade loop.

- Copy from coral, strip any Linear-specific linking into an optional config block.
- Install into coral-benchmarks via the plugin.
- Delete `coral-benchmarks/.claude/skills/pull-request/`.
- Verify coral's existing PR flow still works.

**Exit criterion**: coral still opens correctly formatted PRs, with `pull-request/` no longer in coral's repo.

## M3 — Extract `linear` skill + wire up TOML config

Largest single surface. Plugin reads config via the mechanism picked in M0.

- Extract `linear/SKILL.md`, remove all hardcoded coral IDs (team, initiative, projects, labels).
- Introduce `.agent_squad/config.toml` in coral-benchmarks with the existing coral IDs.
- Verify a ticket-manager-style query (fetch BENCH tickets by label) works via the plugin.
- Delete `coral-benchmarks/.claude/skills/linear/`.

**Exit criterion**: coral's linear skill gone; ticket fetches still work; swapping out the TOML to a different Linear workspace would produce a different set of tickets without code changes.

## M4 — Extract `worker` agent + `assign-worker` skill

Tightly coupled pair; extract together.

- Parameterize worktree path, branch prefix, ticket prefix via TOML.
- Canary: spawn a worker from coral, have it pick up a real BENCH ticket, open a PR.
- Delete coral's local copies.

**Exit criterion**: coral's worker + assign-worker gone; a spawned worker still opens a valid PR.

## M5 — Extract `ticket-manager` agent

Largest prompt and most coral-specific (initiative + 5 project UUIDs for routing). Parameterize all of it into TOML under `[linear.projects]`.

- Canary: ticket-manager can triage BENCH tickets in coral via the plugin.
- Delete coral's local copy.

**Exit criterion**: coral's ticket-manager gone. **Coral canary complete** — `coral-benchmarks/.claude/agents/` and `coral-benchmarks/.claude/skills/` are empty or deleted, all behavior preserved.

## M6 — Squirtle-squad dogfoods itself

- Create `.agent_squad/config.toml` in this repo pointing at the squirtlesquad Linear workspace.
- Add one or two small real tickets on the squirtlesquad Linear project (e.g. "improve README", "add a `squad doctor` command").
- Use ticket-manager + worker from the installed plugin to close one ticket end-to-end via a worker-opened PR against this repo.

**Exit criterion**: one squirtlesquad ticket closed by a PR opened by a worker spawned from the plugin, working on the plugin. **V1 complete.**

## Parking lot (v1.1+)

Ordered roughly by expected value:

- **PR-review-comment polling loop** (BENCH-209). Closes the feedback loop; the single biggest UX gap after v1.
- **Consumer worktree setup hook.** We shipped `setup-worktree.sh` + `.agent_squad/post-worktree-setup.sh` in M4a, then dropped them once we learned the Agent tool's built-in `isolation: "worktree"` already handles worktree creation + auto-cleanup. Re-introduce a hook mechanism when the coral canary lands — coral's flow needs project-specific post-setup (`uv sync`, symlinking `benchmarks/<suite>/profiles.local.yaml`, etc.) that the built-in isolation doesn't run. Likely form: post-isolation hook the worker invokes before it starts work, conditionally sourcing `.agent_squad/post-worktree-setup.sh`.
- **`code-writing` as a customizable template skill.** Ship a minimal scaffold describing structure (language conventions, type-system use, CLI patterns, test conventions). Each consumer forks into their repo and fills in repo-specific patterns. Evolves as we learn what's common across repos.
- **`squirtle-squad doctor` / `squirtle-squad show <agent>`.** Print resolved prompts with provenance ("lines 1–40 plugin, 41–52 local override"). Mandatory once we support overrides beyond plain fork-editing.
- **Non-user auth tokens.** App / bot / OAuth-based credentials so the squad isn't tied to a specific human. Prerequisite for scheduled agents and remote execution.
- **Named extension slots / append/prepend overrides.** Only if multiple consumers diverge enough that fork-editing causes real pain.
- **PM tool adapter pattern.** `pm-linear`, `pm-jira`, `pm-github`. Agents reference an abstract PM capability; adapter skills implement it. Unlocks non-Linear consumers.
- **Reviewer subagent.** Reviews worker PRs before they reach humans.
- **Remote / K8s execution model.** Bake plugin SHA into container images; run workers in remote sandboxes.
- **Public marketplace listing / open source.** Rename, polish docs, set up contribution guidelines.
