# Open Questions

Research items and decisions not yet committed for the CLI/vendor model. Plugin-era questions (Q1–Q8) are resolved or obsoleted by the 2026-04-27 pivot — preserved at [`notes/archive/open-questions-plugin-era.md`](./notes/archive/open-questions-plugin-era.md).

---

## Q1 — `agent-squad sync` semantics

**Status**: open. v0.1 ships a stub.

When the bundled template changes (new agent, fixed prompt, updated skill), how does a consumer pull the changes without losing their local edits?

**Candidates:**

1. **Three-way merge with a stored base.** Store `.agent_squad/.template-version` (a hash of the template content the consumer last synced from). Sync diffs (current template vs stored base vs consumer's tree), applies non-conflicting changes, surfaces conflicts via diff markers (or via individual `.rej` files like `git apply --reject`). Most general; most code.
2. **Selective refresh, no merge.** Sync overwrites only files the consumer hasn't edited (compared by content hash to the stored base). Edited files are skipped; the consumer is told to manually reconcile. Simpler; pushes resolution onto the human.
3. **Refuse to sync if dirty.** Sync only runs if `.agent_squad/` is clean relative to the stored base. Consumer must commit or revert local edits first. Forces explicit decisions; doesn't help with mass upgrades.

**Next action**: prototype option 2 in C3 (smaller scope, smaller risk). Upgrade to option 1 if "skipped because edited" turns out to be the common case.

---

## Q2 — Manager-to-manager coordination

**Status**: open; not blocking v1.

When a consumer has multiple manager scopes and one manager realises a request crosses into another's domain, what's the protocol?

Options range from "escalate to human, human re-dispatches" (simplest, current direction) to "managers SendMessage each other within a session" (richer, but unclear what the team-config story looks like across persistent managers).

**Next action**: defer until we have ≥2 active manager scopes in any consumer. Re-evaluate then.

---

## Q3 — Manager spawn-vs-resume mechanics

**Status**: open.

A manager is supposed to be persistent. But Claude Code session lifetime is bounded — when a manager's session ends, its in-memory state is gone, and the next "engage manager-X" has to be a fresh spawn that reads `.agent_squad/managers/<slug>/` to reconstruct context.

What we need to validate:

- Does the working-memory file pattern (`journal.md`, `goals.md`, `progress.md`) actually carry enough context for a fresh spawn to feel continuous?
- Is there a Claude Code mechanism for "resume this subagent" we're underusing? (Team mode + SendMessage handles within-session continuity; across-session is the gap.)
- If across-session continuity is purely file-driven, does the manager prompt need to instruct it to *write more* than current workers do?

**Next action**: surface during C2 (manager dogfood). The first time a manager session ends and is resumed, capture what was lost and what wasn't.

---

## Q4 — Credentials model evolution

**Status**: v1 direction clear; v1.1+ direction open. (Carried over from plugin-era Q6.)

V1: each user sets personal credentials (`LINEAR_USER_API_KEY`, `GITHUB_TOKEN`) as env vars or in `.env`. The skills document which env vars they read.

V1.1+: user API keys break down for:

- Scheduled or remote-runner agents that run without a user session.
- Shared "bot" accounts that should act as the squad rather than as a specific human.
- Attribution: a worker running under user X's key shows up in Linear as X.

Directions to explore: Linear OAuth apps, Linear admin tokens, per-repo service accounts.

**Next action**: revisit when the runner program starts. Until then, env vars are sufficient.

---

## Q5 — Runner architecture

**Status**: open; v1.1+ scope.

The CLI is local-first. A future runner is a separate program that:

- Watches Linear/GitHub events across multiple repos.
- Dispatches manager scopes and workers without a human Claude Code session driving them.
- Polls open PRs for review comments (BENCH-209) and re-engages the right worker.

Open questions for whenever we start it:

- Language: leaning Go (concurrency, single-binary deploy), but Python-asyncio is also viable. Decision deferred until we have a concrete first feature.
- Deployment shape: long-lived service, scheduled K8s jobs, GitHub Actions runners?
- How does the runner consume a `.agent_squad/` layout: clones the repo, mounts a worktree, or something else?
- Auth: tied to the credentials question (Q4). The runner needs non-user tokens.

**Next action**: no work until C5 lands and we have real demand. The vendor model (this CLI) is what makes the runner possible — ship the vendor model first.
