---
name: assign-worker
description: Spawn a worker subagent to execute a Linear ticket end-to-end in an isolated worktree. Use when the user asks to "assign a worker", "assign a subagent", or points at a Linear ticket / worktree for autonomous execution.
user_invocable: true
---

# Assign a Worker Agent

The worker itself is fully specified in the `worker` agent. This skill only covers the **launch / forwarding mechanics** the manager is responsible for.

Use the daemon topology path when `agent-teamd` is running. If daemon transport is unavailable, the helper writes a durable outbox event under `.agent_team/outbox/pending/` for the next `agent-team tick` / `agent-team drain`. Fall back to Claude's in-session `TeamCreate` / `Agent` tools only when topology cannot route the event or the outbox write fails.

## Preflight

1. **Ticket identifier present?** You need `<PREFIX>-<n>` (the consumer's prefix from `.agent_team/config.toml` under `linear.ticket_prefix`) or a Linear URL. If you can't infer it, ask.
2. **Worker name.** Normalize the ticket to lowercase and use `worker-<ticket-lowercase>` (for example `worker-squ-14`). This stable name lets daemon dispatch reject duplicates and lets you forward follow-up messages to the existing worker.
3. **Kickoff text.** Pass only the ticket identifier and user-supplied direction. Do not restate the worker's operating procedure; its agent prompt handles planning, implementation, validation, and PR creation.

## Daemon Dispatch (Preferred)

Dispatch through topology with the helper:

```sh
"$AGENT_TEAM_ROOT"/agents/manager/skills/assign-worker/scripts/assign_worker.sh dispatch \
  --ticket SQU-14 \
  --kickoff "SQU-14: <user's instructions>"
```

The helper prefers `AGENT_TEAM_DAEMON_URL` plus the bearer token in `AGENT_TEAM_DAEMON_TOKEN_FILE`, then the resolved daemon socket (`$AGENT_TEAM_DAEMON_SOCKET`, falling back to `$AGENT_TEAM_ROOT/daemon.sock`). If neither transport is reachable, it writes an outbox event instead of spawning a legacy worker.

For long kickoff text, write it to a temp file and use `--kickoff-file <path>`.

The helper posts:

```json
{"type":"agent.dispatch","payload":{"source":"<manager>","target":"worker","name":"worker-squ-14","job_id":"squ-14","ticket":"SQU-14","kickoff":"...","workspace":"worktree"}}
```

The daemon creates or updates `.agent_team/jobs/squ-14.toml` from this event and records the worker instance, branch, and worktree when the worker starts.

Interpret the JSON response:

- `dispatched` has an entry: a worker started. Report the `instance_id` and continue.
- `queued` is non-empty: the worker event was accepted but replica capacity is full. Tell the user it is queued.
- `outbox` has an entry: the daemon was unreachable from this runtime, but the dispatch event was durably written. Tell the user it will be published on the next `agent-team tick` or `agent-team drain`.
- `rejected` says the requested instance is already running or queued: reuse the existing worker by sending the follow-up to `worker-<ticket-lowercase>`:
  ```sh
  "$AGENT_TEAM_ROOT"/skills/inbox/scripts/inbox.sh send worker-squ-14 "<the user's follow-up ask>"
  ```
- `matched` is empty or `rejected` has another reason: run `agent-team topology show` / `agent-team daemon status` if available, then use the legacy fallback below if the daemon cannot route `agent.dispatch` to `worker`.

Do not call `Agent` after a successful daemon dispatch or queue response.

## Legacy Fallback (No Daemon)

Use this only when the daemon socket is missing or daemon topology routing failed.

### Reuse First

Before spawning, check the current Claude team:

- **Discover by name.** `cat ~/.claude/teams/<team>/config.json` and look for a teammate whose name matches the ticket, e.g. `worker-squ-14`.
- **Discover by PR.** If the user pointed at a PR instead of a ticket, scan recent worker messages for the PR URL. A worker that opened the target PR owns follow-up on it.
- **Forward with `SendMessage`.** Send the new instructions to the matched worker:
  `SendMessage({to: "worker-squ-14", message: "<the user's follow-up ask>", summary: "..."})`.

Only spawn when no matching worker exists.

### Team Setup

Exactly one team per session.

- Check `ls ~/.claude/teams/` for a team whose `config.json` has `leadSessionId` matching this session.
- If none exists, call `TeamCreate` with a generic project-level name, usually the repo name. Never create a per-ticket team.
- If another session already owns the obvious name, add a suffix such as `agent-team-2`.

### Spawn

Use the `Agent` tool with:

| Param | Value |
|-------|-------|
| `subagent_type` | `"worker"` |
| `team_name` | same name used in `TeamCreate` |
| `name` | `"worker-<ticket-lowercase>"` |
| `description` | short, e.g. `"SQU-14 worker extraction"` |
| `isolation` | `"worktree"` |
| `prompt` | ticket identifier and user-supplied direction |

Do **not** pass `run_in_background: true` unless the user explicitly asks for background mode.

## After Spawning or Forwarding

- Messages from a legacy worker arrive as new turns through `SendMessage`.
- Daemon workers receive direct follow-ups through the inbox skill; they may reply through inbox or status files.
- If a worker asks a question, relay it to the user verbatim unless the answer is mechanical and already present in the repo.

## Common Failure Modes

- **Spawned fresh when a worker already existed.** In daemon mode, repeated dispatch to the same worker name should produce an already-running/queued rejection; forward via inbox. In legacy mode, check the Claude team before spawning.
- **Daemon dispatch returns no matches.** `instances.toml` is missing the `worker` trigger for `agent.dispatch` or the daemon needs `agent-team topology reload`.
- **"Team not found" in legacy spawn.** You skipped `TeamCreate` or used a different `team_name`.
- **Worker runs in the main repo.** Daemon dispatch should include `workspace:"worktree"`; legacy spawn should include `isolation:"worktree"`.
