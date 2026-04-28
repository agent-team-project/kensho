# Custom Orchestrator (design sketch)

**Status**: design sketch, not yet built. This document captures the direction for v1.1+ runtime architecture so the next implementer doesn't have to re-derive it. Open questions are flagged at the bottom.

## What it is

A long-lived daemon (Go binary, `agent-teamd`) that owns the lifecycle of agent instances in a repo. It replaces Claude Code's in-session dispatch primitives (`Task` / `TeamCreate` / `SendMessage` / `Agent` tool) with an orchestrator-mediated model.

Each instance is managed like a Docker container: create / start / stop / restart / remove, with ephemeral or persistent state depending on the agent's declared shape.

## Why a custom orchestrator (vs today's model)

Today, `agent-team run <agent>` exec's `claude` directly. Once inside the session, dispatch flows through Claude Code primitives:

- `Task` tool — synchronous one-shot subagent.
- `Agent` tool with `team_name` + `isolation: "worktree"` — long-lived teammate, addressable via `SendMessage`.
- `TeamCreate` — sets up the tmux pane group.

This works, but it locks us in:

1. **Runtime independence**. To support other LLM runtimes (OpenAI Assistants, local models via Ollama, etc.) we need our own dispatch layer. Claude Code's primitives don't generalize.
2. **Persistence across sessions**. Today, when you exit the orchestrator's claude session, all the workers it spawned die with it (their conversations, anyway — their PRs survive). A daemon outlives any single session, so a manager-billing instance can keep running for days, accept new work, and survive your laptop reboot.
3. **Cross-instance observability**. Logs, message routes, and instance state are spread across tmux panes, claude session files, and `.agent_team/state/`. The daemon centralizes the runtime view: one place to see who's running, what they're working on, what's blocked.
4. **Programmatic dispatch**. Cron jobs, CI hooks, webhook handlers — anything that wants to wake an agent without a human session — can POST to the daemon. Today that requires invoking `claude -p "..."` and reasoning about subprocess lifecycle.

None of this is needed for the simplest "run a manager and chat with it" workflow. The today-style direct `agent-team run` mode keeps working. The orchestrator is the layer for users / consumers who outgrow that shape.

## Lifecycle model

Think Docker containers:

| Verb | Today | With orchestrator |
|---|---|---|
| **create + start** | `agent-team run <agent>` (exec claude directly) | `agent-team run <agent>` → POST /dispatch → daemon spawns claude as a child |
| **stop** | Ctrl-C the claude session | `agent-team stop <instance>` → daemon SIGTERMs the child, persists session ID |
| **start (resume)** | (not possible — session ends with claude) | `agent-team start <instance>` → daemon spawns claude with `--resume <session-id>`, conversation continues |
| **list running** | (none) | `agent-team ps` (or `instance ls --running`) |
| **list all** | `agent-team instance ls` | `agent-team ps -a` (or current `instance ls`) |
| **remove** | `agent-team instance rm` | `agent-team instance rm` (daemon ensures the process is stopped first) |
| **logs** | (none) | `agent-team logs <instance>` |

**Ephemeral vs persistent** is a per-agent property, declared in the agent's frontmatter:

```yaml
---
description: ...
ephemeral: true     # default: false
---
```

- **Ephemeral** instances (`worker` is the canonical example): the daemon starts them, they do their thing, they exit, the daemon deletes the state dir. Like `docker run --rm`. Workers' actual artifacts (PRs, branches) live in git, not in state.
- **Persistent** instances (`manager`, `ticket-manager`, anything with cross-session memory): state dir survives stop/start cycles. The daemon keeps a session ID per instance so `start` resumes the conversation.

## Architecture

```
                     ┌─────────────────────────────────────┐
                     │         agent-teamd (Go)            │
                     │  one daemon per repo                │
                     │  socket: .agent_team/daemon.sock    │
                     │                                     │
                     │  ┌─ HTTP-over-unix-socket API ───┐  │
                     │  │ POST /dispatch                │  │
                     │  │ POST /message                 │  │
                     │  │ POST /stop                    │  │
                     │  │ GET  /instances               │  │
                     │  │ GET  /logs/{id} (stream)      │  │
                     │  └───────────────────────────────┘  │
                     │                                     │
                     │  ┌─ instance manager ────────────┐  │
                     │  │ - spawn claude subprocesses   │  │
                     │  │ - track session IDs           │  │
                     │  │ - route messages              │  │
                     │  │ - persist runtime metadata    │  │
                     │  └───────────────────────────────┘  │
                     └──────────┬──────────────────────────┘
                                │ os/exec
                ┌───────────────┼───────────────────┐
                ▼               ▼                   ▼
           claude proc     claude proc         claude proc
           (manager)       (worker-squ-14)     (worker-squ-15)
              │                 │                   │
              └─ all use the orchestrator skill to call back into the daemon
                 (curl --unix-socket .agent_team/daemon.sock)
```

**Components**:

- **`agent-teamd`** — Go daemon, one per repo. Listens on `.agent_team/daemon.sock`. Static binary, no Python dependency.
- **Agent processes** — each instance is a `claude` subprocess. Long-lived persistent instances use `--resume <session-id>` on restart; ephemeral instances run with `--print` and exit.
- **Orchestrator skill** — a bundled skill (`dispatch`, replacing today's `assign-worker`) that wraps the daemon API. Any agent can invoke it; not tied to the manager+worker shape.

## Daemon API (rough)

All endpoints over Unix socket at `.agent_team/daemon.sock`. JSON request/response.

```
POST /dispatch
  { "agent": "worker", "name": "worker-squ-14", "prompt": "<task>", "context": {...} }
  → { "instance_id": "...", "started_at": "..." }

POST /message
  { "to": "worker-squ-14", "body": "<message>" }
  → { "delivered": true }

POST /stop
  { "instance": "worker-squ-14" }
  → { "stopped": true }

POST /start
  { "instance": "manager-billing" }     # resumes a stopped instance
  → { "instance_id": "...", "session_resumed": true }

GET /instances
  → [{ "name": "...", "agent": "...", "status": "running|stopped|exited|crashed", ... }]

GET /logs/{instance}
  → stream of conversation log lines (server-sent events or chunked text)
```

## CLI surface additions

Existing today (no daemon required):

```
agent-team init / doctor / run / agent / skill / instance
```

New, daemon-aware:

```
agent-team daemon start          # boot agent-teamd in this repo
agent-team daemon stop
agent-team daemon status

agent-team ps                    # list running instances (alias: instance ls --running)
agent-team logs <instance>
agent-team start <instance>      # resume a stopped persistent instance
agent-team stop <instance>       # graceful stop, keep state
```

`agent-team run <agent>` becomes daemon-aware: if a daemon is running for this repo, it routes through the daemon. If not, it falls back to the today-style direct claude exec.

## Implementation language

The whole CLI + daemon is Go. The CLI ports landed in SQU-21 / SQU-22 / SQU-23; what remains is the daemon itself. End state: a single Go codebase shipping static binaries with no other runtime dependency. Distribution is `go install` today, `brew install agent-team` / release tarballs as a follow-up.

Two reasonable shapes for the binary split, both fine:

- **One binary, two subcommands.** `agent-team daemon` runs the long-lived mode; `agent-team run` / `ps` / `logs` / `init` etc. are short-lived subcommands that talk to it. Same pattern as `caddy run` vs `caddy reload`.
- **Two binaries.** `agent-team` (user-facing CLI) + `agent-teamd` (daemon). Same pattern as `docker` vs `dockerd`. Cleaner separation, marginally more to ship.

Decide at implementation time; either keeps the public surface identical.

## Persistence

- **Definitions** (committed): `.agent_team/agents/`, `.agent_team/skills/`.
- **Per-instance state** (committed by default): `.agent_team/state/<instance>/` — journal, goals, progress, anything the agent writes.
- **Daemon-owned runtime metadata** (gitignored): `.agent_team/daemon/<instance>/` — claude session ID, process ID, log files, message queue. Recreated/repaired on daemon restart.

## Open design questions

1. **Per-repo daemon or system daemon?** Per-repo is simpler — one socket per repo, no auth required, isolated lifecycles. System daemon is one process for all your projects but raises multi-tenancy concerns. Recommendation: start per-repo; revisit if pain emerges.

2. **Resume model for stateful instances**. Claude Code has `--resume <session-id>` for thread continuity. The daemon stores session IDs per persistent instance and uses this on `start`. Open: does `--resume` work after a long gap (days/weeks)? Does it work across claude version upgrades? Need to verify.

3. **Failure modes**. What happens when:
   - An instance crashes mid-dispatch — daemon should detect and mark crashed; the parent (e.g. a manager that dispatched a worker) gets notified.
   - The daemon itself crashes — instances become orphaned claude processes. On restart, daemon should reconcile by reading runtime metadata and either re-adopting or marking exited.
   - The user kills the daemon while instances run — same as crash, ideally.
   Crash-only design (no graceful shutdown logic) is simplest if metadata is durable on disk.

4. **Backward compat with `assign-worker` skill**. Two paths:
   - Keep `assign-worker` working in no-daemon mode; ship a new `dispatch` skill for daemon mode; agents detect which mode they're in via env var (`AGENT_TEAM_DAEMON_SOCKET=...`).
   - Migrate everything to the orchestrator API; require the daemon for any multi-agent work.
   The first is more incremental and probably right for v1.1.

5. **Worktree management** — does the daemon spawn worktrees for ephemeral code-writing instances, or does the agent itself? Today, Claude Code's `Agent` tool with `isolation: "worktree"` does it. Without that primitive (since we're bypassing Claude Code's dispatch), the daemon would need to: `git worktree add .claude/worktrees/<name> -b <branch>` before spawning, and `git worktree remove` on cleanup. Reasonable but adds git complexity to the daemon.

6. **Multi-runtime support**. The whole motivation of #1 above. Does the orchestrator have a clean abstraction for "spawn an agent" that could swap claude for `openai-cli` or a local model? Probably a runtime adapter interface that takes (system prompt, skills dir, kickoff) and returns a process handle. Defer the actual non-claude adapter to v1.2.

7. **API surface stability**. Once agents are calling `curl --unix-socket .agent_team/daemon.sock /dispatch`, that's a contract. Versioning the API from day one (`/v1/dispatch`) is cheap insurance.

## What this doesn't change

- Agent definitions stay file-based and human-authored. The daemon is purely a runtime concern.
- `agent-team run <agent>` stays the canonical way to start an instance — it just gets a daemon-aware code path inside.
- Skills stay portable — they're just bash + markdown, runtime-agnostic.
- The `.agent_team/` layout (agents, skills, state) is unchanged. Only adds `.agent_team/daemon/` for gitignored runtime metadata.

## Relationship to templates

[`templates.md`](./templates.md) covers the orthogonal concern of how `.agent_team/` gets *populated* — via parameterized templates instantiated into the repo at `init` time. The orchestrator covers what happens at *runtime* once the repo is set up. They compose:

- A daemon-managed instance still reads its config from the resolved chain described in `templates.md` (template defaults → repo `config.toml` → per-instance overrides → CLI flags).
- The `--set` flag at `agent-team run` time flows through both: the launcher merges the config layers, and the daemon (when present) writes the merged copy to the instance's state dir before spawning the claude subprocess.

If you're touching `init`, `loader`, the bundled-starter shape, or `config.toml` schema, read `templates.md` first. If you're touching `run`, `ps`, `logs`, message routing, or instance lifecycle, this doc is the reference.
