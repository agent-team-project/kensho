# Custom Orchestrator (design sketch)

**Status**: scaffolding + lifecycle endpoints landed in SQU-28 (`cmd/agent-teamd/` + `internal/daemon/`). Message routing (`/v1/message`) and log streaming (`/v1/logs/{instance}`) plus daemon-aware CLI (`run` / `ps` / `logs`) and the `inbox` skill landed in SQU-29. Resolved Open Questions are noted inline; the rest of this document captures the design as written.

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
                     │  │ POST /v1/dispatch (SQU-28)    │  │
                     │  │ POST /v1/stop     (SQU-28)    │  │
                     │  │ POST /v1/start    (SQU-28)    │  │
                     │  │ GET  /v1/instances (SQU-28)   │  │
                     │  │ POST /v1/message  (SQU-29)    │  │
                     │  │ GET  /v1/logs/{id} (SQU-29)   │  │
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

## Daemon API

All endpoints over Unix socket at `.agent_team/daemon.sock`. JSON request/response. Versioned `/v1/...` from day one.

**Live in SQU-28:**

```
POST /v1/dispatch
  { "agent": "worker", "name": "worker-squ-14", "prompt": "<task>", "workspace": "<abs-path>" }
  → { "instance_id": "...", "started_at": "...", "pid": <int>, "session_id": "<uuid>" }

POST /v1/stop
  { "instance": "worker-squ-14" }
  → { "stopped": true }

POST /v1/start
  { "instance": "manager-billing" }     # resumes a stopped instance via --resume
  → { "instance_id": "...", "session_resumed": true, "pid": <int> }

GET /v1/instances
  → [{ "instance": "...", "agent": "...", "status": "running|stopped|exited|crashed",
       "pid": <int>, "session_id": "...", "workspace": "...", "started_at": "...", ... }]
```

**Landed in SQU-29:**

```
POST /v1/message
  { "to": "worker-squ-14", "from": "manager", "body": "<message>" }
  → { "delivered": true, "id": "<uuid>", "ts": "<rfc3339>" }

GET /v1/logs/{instance}[?follow=true]
  → chunked text stream of <daemon-root>/<instance>/child.log.
    Without follow=true: dump current file and close.
    With follow=true: dump, then tail until ctx cancels.
```

Chunked text over SSE: the consumer is a CLI doing either a one-shot dump or a long-running tail piped to stdout — neither benefits from SSE's reconnect/event-typed semantics, and chunked text is what `curl --no-buffer` and Go's `http.Client` produce naturally.

The `child.log` file is the canonical per-instance log (stdout+stderr from the claude subprocess, written by the spawner). `/v1/logs/{instance}` reads it; the inbox / ps / messaging code paths share no state with it.

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

`agent-team run <agent>` is daemon-aware (SQU-29): when `--prompt` is set (one-shot mode) AND the daemon is running, the CLI POSTs to `/v1/dispatch` with the full claude argv (so agent / skill resolution stays in the CLI). Without `--prompt` (interactive mode), or with `--no-daemon`, or when no daemon is running, the CLI exec's claude directly. Interactive sessions stay direct because the daemon spawns claude headless against a log file.

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

### SQU-28 layout (concrete paths)

| Path | Owner | Purpose |
|---|---|---|
| `.agent_team/daemon.sock` | daemon (gitignored) | Unix socket. Removed on graceful shutdown; recreated on next start. |
| `.agent_team/daemon.pid` | daemon (gitignored) | Pidfile. Read by `agent-team daemon status/stop`. |
| `.agent_team/daemon/agent-teamd.log` | daemon (gitignored) | Stdout/stderr from a `--detach`'d daemon. Distinct from per-instance child logs. |
| `.agent_team/daemon/<instance>/meta.json` | daemon (gitignored) | Per-instance disk-durable record (PID, session ID, status, started_at, etc.). Source of truth on reconcile. |
| `.agent_team/daemon/<instance>/child.log` | daemon (gitignored) | Stdout/stderr from the claude subprocess for this instance. Streamed by `/v1/logs/{id}` (SQU-29). |
| `.agent_team/daemon/<instance>/mailbox.jsonl` | daemon (gitignored) | Append-only JSONL message inbox. One `{id, from, to, body, ts}` per line. Written by `POST /v1/message` (SQU-29); read by the bundled `inbox` skill. |
| `.agent_team/daemon/<instance>/mailbox-cursor.txt` | daemon (gitignored) | Highest-acked message ID. Updated by `inbox ack <id>`; consulted by `inbox check` to decide what is unread. |

### SQU-28 spawn surface (intentionally minimal)

The daemon spawns claude with the bare-minimum args: `claude --session-id <uuid> [-p <prompt>]`. The session UUID is generated by the daemon on `/v1/dispatch`, persisted, and reused on `/v1/start` via `claude --resume <uuid>` — so resume is deterministic without parsing claude's own output.

Agent resolution (loading `.agent_team/agents/<name>/agent.md`, building the `--agents` JSON, writing the kickoff prompt file, `--add-dir`'ing the skills tmpdir, exporting `AGENT_TEAM_*` env) stays in `agent-team run` for SQU-28. Wiring `agent-team run` into `/v1/dispatch` is SQU-29's job.

### Daemonization mechanism

`agent-team daemon start --detach` spawns `agent-teamd` via `os.StartProcess` with `&syscall.SysProcAttr{Setsid: true}`, redirecting stdin to `/dev/null` and stdout/stderr to `.agent_team/daemon/agent-teamd.log`. The launcher calls `proc.Release()` so it doesn't become the daemon's reaper. We chose `setsid` over a full POSIX double-fork because the parent CLI exits immediately; the daemon ends up reparented to PID 1 either way. Foreground mode (`agent-team daemon start` without `--detach`) just exec's `agent-teamd` directly for live debugging.

## Instance status / observability

Each running instance writes a small `status.toml` to its state dir at phase transitions, so an outside observer (a human running `agent-team instance ps`, or eventually the daemon) can see what every instance is doing without scraping logs or attaching to a session.

The bundled `status` skill is the writer. `agent-team instance ps` is the reader. Both land in v1.0 alongside the CLI; the daemon (when it lands) will cache these files in memory and add long-poll for `ps -w`, but the file format is stable from day one.

### Schema

`<state-dir>/status.toml`:

```toml
[status]
phase       = "implementing"   # one of: planning, implementing, awaiting_review, blocked, idle, done
description = "Porting parameter substitution to Go"
since       = "2026-04-28T13:42:00Z"   # ISO-8601 UTC, when this phase started
last_action = "Edited internal/template/render.go"

[work]                          # optional — the unit of work this instance is on
ticket = "SQU-25"
pr     = "https://github.com/jamesaud/agent-team/pull/26"
branch = "squ-25-status-emission"

[blocking]                      # optional — present only when phase = "blocked"
reason = "Need clarification on the rendered/ subdir contract"
ask_to = "manager"              # instance name or role this instance is asking
```

**Phases.** A small fixed vocabulary so `instance ps` columns align across instances:

| Phase | Meaning |
|---|---|
| `planning` | Reading docs, exploring code, writing a plan. No external artifacts yet. |
| `implementing` | Actively editing code or running commands. |
| `awaiting_review` | PR opened or work handed off; waiting for human / reviewer. |
| `blocked` | Cannot proceed without input from the field in `[blocking]`. |
| `idle` | Persistent instance with no active task — waiting for the next request. |
| `done` | Terminal for ephemeral instances; their state dir will be cleaned up. |

**Atomicity.** The skill writes to `status.toml.tmp` and `rename`s over `status.toml`. The reader never sees a partial write.

**Staleness.** `last_action` is a human string, not a timestamp. The reader uses the file's mtime to judge freshness: if mtime is older than 10 minutes for a non-`idle`/non-`done` instance, the row is annotated `(stale)` to flag a likely-hung agent.

### Writer surface

```sh
status set <phase> [--desc "..."] [--ticket <id>] [--pr <url>] [--branch <name>] [--last-action "..."]
status block --reason "..." --ask <instance-name|role>
status clear-block                     # transitions back to the prior phase
status show                             # debug: print the current file
```

Anything not passed is preserved from the prior write. `since` is auto-managed by the skill: it's reset whenever `phase` changes, untouched when only `description` / `last_action` / `[work]` fields are updated.

### Reader

`agent-team instance ps` walks `.agent_team/state/*/status.toml` and renders a Docker-style table:

```
INSTANCE          AGENT           PHASE             AGE   SUMMARY
manager           manager         idle              2h    waiting on user
worker-squ-25     worker          implementing      8m    Porting parameter substitution
ticket-manager    ticket-manager  blocked           4m    asks manager: clarify rendered/ contract
```

Instances that have a state dir but no `status.toml` (declared but never spawned, or pre-status-emission) show `—` placeholders for PHASE/AGE so the operator still knows they exist.

`agent-team instance show <name>` prints the parsed status with all fields, plus the existing state-dir file listing.

## Open design questions

1. **Per-repo daemon or system daemon?** Per-repo is simpler — one socket per repo, no auth required, isolated lifecycles. System daemon is one process for all your projects but raises multi-tenancy concerns. Recommendation: start per-repo; revisit if pain emerges.

2. **Resume model for stateful instances**. Claude Code has `--resume <session-id>` for thread continuity. The daemon stores session IDs per persistent instance and uses this on `start`. Open: does `--resume` work after a long gap (days/weeks)? Does it work across claude version upgrades? Need to verify.

3. **Failure modes**. What happens when:
   - An instance crashes mid-dispatch — daemon should detect and mark crashed; the parent (e.g. a manager that dispatched a worker) gets notified.
   - The daemon itself crashes — instances become orphaned claude processes. On restart, daemon should reconcile by reading runtime metadata and either re-adopting or marking exited.
   - The user kills the daemon while instances run — same as crash, ideally.
   Crash-only design (no graceful shutdown logic) is simplest if metadata is durable on disk.

   **Resolved (SQU-28)**: crash-only design adopted. Per-instance metadata is fsync'd to `.agent_team/daemon/<instance>/meta.json` before /v1/dispatch returns. On daemon startup, `Reconcile()` walks the daemon root, probes each running-status PID with `kill(pid, 0)`, and marks dead processes as `exited`. Live processes are adopted (status preserved) — but the daemon cannot `Wait()` on a process it didn't fork, so the eventual exit of an adopted child is observed only by subsequent reconciliation passes. We do not auto-restart anything; surfacing accurate state via `/v1/instances` is the contract. Notification of dispatch parents is deferred to SQU-29 alongside `/v1/message`.

4. **Backward compat with `assign-worker` skill**. Two paths:
   - Keep `assign-worker` working in no-daemon mode; ship a new `dispatch` skill for daemon mode; agents detect which mode they're in via env var (`AGENT_TEAM_DAEMON_SOCKET=...`).
   - Migrate everything to the orchestrator API; require the daemon for any multi-agent work.
   The first is more incremental and probably right for v1.1.

5. **Worktree management** — does the daemon spawn worktrees for ephemeral code-writing instances, or does the agent itself? Today, Claude Code's `Agent` tool with `isolation: "worktree"` does it. Without that primitive (since we're bypassing Claude Code's dispatch), the daemon would need to: `git worktree add .claude/worktrees/<name> -b <branch>` before spawning, and `git worktree remove` on cleanup. Reasonable but adds git complexity to the daemon.

6. **Multi-runtime support**. The whole motivation of #1 above. Does the orchestrator have a clean abstraction for "spawn an agent" that could swap claude for `openai-cli` or a local model? Probably a runtime adapter interface that takes (system prompt, skills dir, kickoff) and returns a process handle. Defer the actual non-claude adapter to v1.2.

7. **API surface stability**. Once agents are calling `curl --unix-socket .agent_team/daemon.sock /dispatch`, that's a contract. Versioning the API from day one (`/v1/dispatch`) is cheap insurance.

   **Resolved (SQU-28, extended SQU-29)**: all routes versioned `/v1/...` from day one. SQU-28 shipped `POST /v1/dispatch`, `POST /v1/stop`, `POST /v1/start`, `GET /v1/instances`. SQU-29 added `POST /v1/message` and `GET /v1/logs/{id}` (with `?follow=true`) under the same prefix. `DispatchInput` was extended with `Args` and `Env` so the CLI can hand off the full `--agents/--add-dir/...` machinery without the daemon re-deriving agent resolution.

## What this doesn't change

- Agent definitions stay file-based and human-authored. The daemon is purely a runtime concern.
- `agent-team run <agent>` stays the canonical way to start an instance — it just gets a daemon-aware code path inside.
- Skills stay portable — they're just bash + markdown, runtime-agnostic.
- The `.agent_team/` layout (agents, skills, state) is unchanged. Only adds `.agent_team/daemon/` for gitignored runtime metadata.

## Relationship to templates and topology

Three forward-looking docs partition the design space:

- **This doc (`orchestrator.md`)** — runtime: process lifecycle, message routing, daemon API, instance state. Read before touching `run` / `ps` / `logs` / dispatch.
- [`templates.md`](./templates.md) — authoring/distribution: how `.agent_team/` gets populated via parameterized templates. Read before touching `init` / `loader` / `template` verb / `config.toml` schema.
- [`topology.md`](./topology.md) — declaration: which named instances exist (`instances.toml`), how each is configured, what events trigger each. The trigger-resolution code lives in the daemon (this doc); the schema and consumer authoring live in topology.

How they compose at runtime:

- A daemon-managed instance reads its config from the resolved chain in `templates.md` extended by topology's per-instance declared overrides — so the full chain is: CLI flags → per-instance state file → declared overrides (`instances.toml`) → repo `config.toml` → template defaults.
- The `--set` flag at `agent-team run` flows through this chain; the launcher merges layers and writes the resolved copy to the instance's state dir before spawning the claude subprocess.
- Event triggers (declared in topology, defined here as `POST /event`) become the public dispatch entry. Existing `/dispatch` and `/message` endpoints become the daemon's internal primitives the trigger handler calls.
