---
name: assign-worker
description: Spawn a worker subagent to execute a Linear ticket end-to-end in an isolated worktree. Use when the user asks to "assign a worker", "assign a subagent", or points at a Linear ticket / worktree for autonomous execution.
user_invocable: true
---

# Assign a Worker Agent

The worker itself is fully specified in the `worker` agent — don't re-explain its job here. This skill only covers the **launch mechanics** the team-lead is responsible for.

**Reuse is the default, spawn is the fallback.** A running worker has in-memory reasoning about the ticket and an open branch/PR. Re-spawning gets you a fresh worktree (Claude Code's `isolation: "worktree"` always creates new) and forces re-onboarding — so always look for an existing worker before spawning a new one.

## Preflight

1. **Ticket identifier present?** You need `<PREFIX>-<n>` (the consumer's prefix from `.agent_team/config.toml` under `linear.ticket_prefix`) or a Linear URL. If you can't infer it, ask.
2. **Is this follow-up on an open PR?** Review-bot comments, rebase requests, post-merge fixups, and "address that feedback" asks all point at an existing PR. Treat these as reuse cases by default — go to **Reuse an existing worker** below.
3. **Existing worker in this session's team?** Before spawning, look for a teammate whose name matches the ticket or who already opened the target PR. See **Reuse an existing worker**.

## Reuse an existing worker

Do this **before** calling `Agent` / `TeamCreate`. If any of these match, send a message to the existing worker instead of spawning a new one.

- **Discover by name.** `cat ~/.claude/teams/<team>/config.json` and look for a teammate whose name matches the ticket (e.g. `worker-squ-14` for `SQU-14`). The naming convention from **Spawn** below guarantees one name per ticket, so a match is conclusive.
- **Discover by PR.** If the user pointed at a PR instead of a ticket, scan the most recent PR URL each teammate has mentioned (the last `gh pr ...` message in their transcript is enough). A worker that opened the target PR owns follow-up on it.
- **Forward with `SendMessage`.** Send the new instructions to the matched worker: `SendMessage({to: "worker-<ticket>", message: "<the user's follow-up ask>", summary: "..."})`. The idle worker wakes up on receipt and picks up where it left off — its in-memory state is what carries the continuity, not any on-disk worktree (worktrees don't persist across spawns).

Only fall through to **Spawn** when there is no matching worker — i.e. a genuinely new ticket, or the prior worker was explicitly shut down.

## Team setup (one-time per session)

Exactly one team per session — this is a hard constraint.

- Check `ls ~/.claude/teams/` for a team whose `config.json` has `leadSessionId` matching this session. If none, call `TeamCreate` with a **generic, project-level name** (typically the repo name, e.g. `agent-team`, `my-project`) — never per-ticket, because one team has to hold workers for multiple tickets over the session's lifetime.
- If a team from another session already occupies the obvious name, pick a suffix (`agent-team-2`). Orphaned teams from prior sessions are fine to leave alone.

## Spawn (fallback: only when no existing worker matches)

Use the `Agent` tool (not `TeamCreate` alone — that only makes the container). Required parameters:

| Param | Value |
|-------|-------|
| `subagent_type` | `"worker"` |

| `team_name` | same name used in `TeamCreate` |
| `name` | `"worker-<ticket-lowercase>"` e.g. `worker-squ-14` — this is how you and the worker address each other via `SendMessage`, and how future reuse-checks find it |
| `description` | short, e.g. `"SQU-14 worker extraction"` |
| `isolation` | `"worktree"` — Claude Code creates a fresh git worktree + branch for the worker before it starts, and auto-cleans if no changes happen. The worker's prompt is written to expect this. |
| `prompt` | **the ticket identifier and any user-supplied direction** — the worker's own startup sequence handles the rest (plan, implement, PR). Don't re-specify those steps. |

Do **not** pass `run_in_background: true` — that forces background mode and breaks tmux visibility + team messaging. Only use it if the user explicitly says "run in background".

## After spawning (or forwarding)

- Messages from the worker arrive automatically as new turns. Do not poll, do not sleep.
- If the worker asks a question (via `SendMessage` or a `blockers.md` entry surfaced in a message), relay it to the user verbatim — don't answer on the user's behalf unless the answer is mechanical (file paths, existing conventions).
- When the user answers, forward via `SendMessage({to: "worker-<ticket>", ...})` — the idle worker wakes up on receipt.

## Common failure modes

- **Spawned fresh when a worker already existed.** You skipped the reuse check. The new worker re-asks blockers the user already answered, or starts a parallel branch that conflicts with the first worker's PR. Always check `~/.claude/teams/<team>/config.json` first.
- **"Team not found" on spawn.** You skipped `TeamCreate`, or you used a different `team_name` than the one you created. Pass the exact name from the `TeamCreate` result.
- **Worker runs in main repo, not worktree.** The worker's own startup sequence `cd`s into the worktree — but only if the prompt contains the ticket identifier. If you spawned with a vague prompt, it may skip that step. Always include `<PREFIX>-<n>` in the prompt.
- **Two workers, one team, one ticket.** Only possible if you spawned with two different `name` values for the same ticket. Shut one down via `SendMessage({type: "shutdown_request"})`.
