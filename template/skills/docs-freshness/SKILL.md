---
name: docs-freshness
description: Audit the docs against the current codebase and latest release — stale commands, dead references, drifted repo paths, undocumented shipped features. Files a freshness ticket, or fixes directly when dispatched to do so. Use when running a docs freshness sweep or asked to check docs currency.
---

# Docs freshness

Docs rot silently: a command changes, a repo moves, a feature ships, and the prose keeps claiming the old world. Your job is to catch that drift against ground truth and either fix it or file precisely what is stale.

## Ground truth, in priority order

1. **The shipped binary.** Build it (`go build -o bin/agent-team ./cmd/agent-team`) and check every documented command/flag against `--help`. A command in the docs that the binary doesn't have is the highest-severity rot.
2. **The latest release.** `gh release view --repo agent-team-project/agent-team` and the CHANGELOG since the last tag: every user-facing feature shipped since the docs were last touched should be documented somewhere. Undocumented shipped features are the second signal.
3. **Repo identity.** Grep for stale references: old owner/module paths (anything not `agent-team-project/agent-team` or `github.com/agent-team-project/agent-team`), dead relative links, `TODO`/`TKTK` placeholders, broken cross-links between guide pages.
4. **The generated CLI reference.** `agent-team docs cli --check docs/reference/cli.generated.md` must be clean; if it drifts, that is a mechanical fix.
5. **The README's outward links.** Confirm the ReadTheDocs link (https://agent-team.readthedocs.io), the release badge, and quickstart commands all resolve and work.

## What to check every sweep

- README leads accurately (experiment framing intact, status current, RTD link live).
- Quickstart and getting-started commands run verbatim in a fresh `/tmp` repo.
- `docs/experiment.md` still matches `.agent_team/instances.toml` (team/loop/schedule counts and cadences).
- No stale owner/module paths outside CHANGELOG history.
- Every guide page's commands exist in the current binary.
- **The roadmap (`docs/contributing/roadmap.md`) reflects reality** — see below.

## Roadmap ownership

The docs team owns `docs/contributing/roadmap.md` as a living document — it is what the project shares publicly about where it is going, so it must never drift into fiction. Every freshness sweep, reconcile it against ground truth:

- **Shipped → "Recently shipped".** Anything merged/released since last sweep that a roadmap item promised moves out of "In progress"/"Planned" with its PR or release link. The latest release tag + CHANGELOG are the authority for what actually landed.
- **In flight → "In progress".** Open epics with active work (query the PM provider for open epic-labeled tickets) belong here, one honest line each — no dates you can't defend.
- **Designed-not-started → "Planned".** Design docs under `documentation/` with no implementation yet (security-model, distributed-resources, …) are planned items; link the design doc.
- **Framing:** direction-of-travel, not a commitment calendar. Group by theme (security & isolation, distribution, provider surface, operability), lead with why each theme matters. A roadmap that overpromises erodes the trust the experiment runs on.

Keep it current over exhaustive. If nothing changed, touch nothing.

## Output

- **Dispatched to fix**: make the corrections directly on a branch, open a PR (`gh --repo agent-team-project/agent-team`), keep the diff scoped to docs.
- **Scheduled sweep (audit mode)**: file ONE ticket through `agent-team ticket create` labeled `docs` + `stale-docs` to Backlog or the provider's equivalent holding area per sweep, listing each drift with file:line and the ground truth it contradicts, ranked by severity (missing command > undocumented feature > stale link > wording). Fold into an open docs ticket with `agent-team ticket comment` if one already covers the area. If the docs are current, say so and file nothing.

## Rules

- Verify, never assume: every "this is stale" claim cites the command output or file that proves it.
- Docs-only scope; never touch product code (a doc that is wrong because the code is wrong becomes a `bug` ticket for delivery, not a docs edit).
- Prose stays in the project's voice — accurate over impressive, concrete over abstract.
