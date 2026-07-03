# Recoverable managers (design sketch)

**Status**: design, with Phases 1-3 landed. Tracked by SQU-44 (headline lever from the SQU-42 field report; parked upstream as BENCH-719). Read `orchestrator.md` first — this doc extends its lifecycle model and reuses its vocabulary.

## Problem

The two-plane architecture proved out in production (SQU-42): the daemon plane (events → dispatch → worktree/PR artifacts → auto-advance) survives supervisor loss; jobs, branches, and PRs persist. But managers — the judgment-bearing persistent instances that bridge the daemon plane and the mailbox plane — originally were not first-class daemon citizens:

1. A manager's *conversation context* (in-flight plans, review rationale, dispatch decisions) lives in the runtime's session store, keyed by a session id. Before this arc, only Claude-managed instances tracked that id automatically; Codex needed capture from runtime output.
2. Even where resume worked, nothing reconstructed *operating context* mechanically. The virtual-graph team hand-rolled a `TEAM_STATE.md` + re-spawn procedure; every operator would have needed one.
3. A daemon restart could orphan children: reconcile adopted live PIDs without a reaper, did not re-spawn dead declared managers, and resume rebuilt env from the operator's shell rather than the env the instance was dispatched with.

## Current state (verified against the code)

| Mechanism | State today |
|---|---|
| Session id capture | Claude: daemon generates a UUID and passes `--session-id` at dispatch (`internal/daemon/instance.go` dispatch path). Codex: daemon-managed `codex exec` launches include `--json`, the spawner tails `child.log` for the first `thread.started` JSONL event, and records `thread_id` as `SessionID`. Manual `adopt --session-id` still works for externally started processes. |
| Managed resume | `agent-team start <instance>` resumes Claude with `claude --resume <session>` and Codex with `codex exec resume <session> -`. The lifecycle gate is now a runtime capability lookup; Codex metadata needs a recorded session id to be managed-resumable. |
| Direct resume | `claude --resume <id>` / `codex resume <id>` printed by resume plans (`internal/cli/runtime_resume.go`); operator-run, unmanaged. |
| Daemon restart | Crash-only `Reconcile` (`internal/daemon/reconcile.go`) adopts live PIDs, marks dead PIDs terminal, watches adopted children for later exits, and applies declared restart policy with backoff for persistent instances. |
| Launch env | Daemon and per-instance launch-env snapshots are persisted with sensitive keys stripped. Managed resume uses the instance snapshot instead of the operator's current shell. |
| Kickoff | Fresh spawns get the generated brief prepended to the kickoff. Claude managed resume receives the brief as a mailbox message; Codex managed resume receives it as the stdin prompt to `codex exec resume <session> -`. |
| Attach | Shape A handoff (stop child → exec runtime-native interactive resume in the user's terminal → daemon re-`start`s on exit). Claude uses `claude --resume <session>`; Codex uses `codex resume <session>`. Ephemeral instances are rejected. |

Codex capabilities the design can now rely on (verified against codex-cli 0.142):

- `codex exec --json` emits `{"type":"thread.started","thread_id":"<uuid>"}` as the first JSONL event — machine-readable session capture.
- `codex exec resume <session-id> [PROMPT|-]` — headless continuation of a recorded session with a fresh prompt over stdin.

## Design goals

1. A persistent instance declared in `instances.toml` survives child crashes, daemon restarts, and machine reboots with its conversation intact where the runtime allows, and with mechanically reconstructed operating context where it doesn't.
2. Runtime-agnostic: Claude and Codex managers get the same lifecycle guarantees, differing only in the fidelity of conversation recovery.
3. Crash-only stays the foundation. Recovery is a *reconcile policy*, not new in-flight state.
4. No duplicate children, ever. A resume that races an adopted survivor must lose.

Non-goals: mid-session steering, cross-repo managers, moving manager judgment into the daemon. The daemon recovers context; it does not have opinions.

## Phase 1 — supervision continuity

**`restart` policy per declared instance** (`instances.toml`):

```toml
[instances.manager]
agent   = "manager"
restart = "on-failure"   # never (default) | on-failure | always
```

- Reconcile gains a *desired-state pass* after the existing crash-only pass: for each declared persistent instance with `restart != "never"`, if its metadata says `exited`/`crashed` (or missing) and no live child exists, re-launch it through the same path `instance up` uses today (resume if possible, fresh spawn otherwise).
- `always` also covers clean exits; `on-failure` only non-zero/crash terminations.
- Runs at daemon boot and on `reconcile`/`sync`/`tick`; a per-instance backoff (in metadata, e.g. `restart_backoff_until`) prevents crash-loop spin. Backoff caps at 5m; `agent-team doctor --canary` (SQU-39) is the operator's tool for diagnosing why restarts keep failing.
- **Adopted-children watcher**: the daemon starts a lightweight poll (`kill(pid,0)` on a ticker) for every adopted `running` record so terminal transitions are observed within seconds — and can trigger the same restart policy — instead of waiting for the next manual reconcile.

Invariant: re-launch takes the instance lock, re-probes liveness *after* acquiring it, and reuses the `sameTrackedIncarnation` check so an adopted survivor can never race a restart into a duplicate child.

## Phase 2 — durable operating context (runtime-agnostic)

**`agent-team instance brief <name>`** — generate a catch-up artifact purely from data the daemon already owns:

- identity: instance, agent, declared role, state dir
- owned jobs (`.agent_team/jobs/`): status, next steps, branches, PRs
- pipeline states for pipelines the instance participates in
- unread mailbox messages and channel cursors
- last N lifecycle events touching the instance or its dispatched children
- current fleet snapshot (`ps` rows) scoped to the instance's team

Written to `<state-dir>/brief.md` (also printed / `--json`). This institutionalizes the field-tested `TEAM_STATE.md` as a generated artifact instead of a hand-rolled convention.

**Injection**: every fresh spawn *and* every managed resume of a persistent instance prepends the brief to the kickoff (fresh spawn) or delivers it as the first mailbox message (resume, so the runtime session history stays coherent). Cold-start recovery — session store pruned, `--resume` fails — degrades to fresh spawn + brief, which is exactly the manual recovery procedure the virtual-graph team validated, minus the human.

**Per-instance launch-env snapshot**: persist the resolved dispatch env (same strip-keys treatment as the daemon's own snapshot — `OPENAI_API_KEY` never stored) in the instance metadata dir, and use it on every restart/resume instead of bare `os.Environ()`. This closes the two operational holes the field report flagged: PATH drift after daemon restarts (SQU-39's crash-loop) and auth-mode flips on subscription-auth Codex.

## Phase 3 — session reload for Codex managers

- **Capture**: landed in SQU-53. Daemon-managed Codex dispatches run `codex exec --json`; the spawner tails `child.log` for the first `thread.started` event and records `thread_id` as `SessionID` in metadata. Raw log capture is preserved; the JSONL stream still lands in `child.log`.
- **Resume**: landed in SQU-53. `Start` on a Codex instance with a recorded session runs `codex exec resume <session-id> -` with the brief (Phase 2) as the stdin prompt. `lifecycleMetadataSupportsManagedResume` is a runtime-capability lookup, and the capability matrix (`runtime ls`) reports `managed_resume` for Codex.
- **Preflight**: landed in SQU-53. Before spawning `--resume`/`exec resume`, the daemon validates that the workspace still exists and, for Codex, that the rollout file for the session is present under `~/.codex/sessions` (or `CODEX_HOME/sessions`). On failure it records `resume_fallback` and falls back to fresh-spawn-plus-brief rather than crash-looping.
- `attach` for Codex is implemented by the same Shape A handoff, execing `codex resume <session-id>` interactively.

## Phase 4 — supervisor notifications on transitions (folds SQU-37)

Managers being daemon citizens means the daemon owns the supervisor-facing signal path, so it must not recreate the idle-ping noise the field report measured. The daemon watches persistent instances' `status.toml` phases (it already stats these for `ps`) and publishes a single event on *transition* — `busy→idle`, `*→blocked` — to a `#supervisor` channel:

```toml
[notifications]
phase_transitions = ["idle", "blocked"]   # default: ["blocked"]
idle_renotify     = "0"                    # off unless set, e.g. "30m"
```

No repeated same-state pings, by construction.

## Schema summary

| Surface | Addition |
|---|---|
| `instances.toml` | `restart = "never|on-failure|always"`, `brief = true|false` (default true for persistent) |
| `.agent_team/config.toml` | `[notifications] phase_transitions / idle_renotify` |
| Instance metadata | `restart_backoff_until`, launch-env snapshot file, Codex `SessionID` via thread capture |
| CLI | `instance brief <name> [--json]`; `start`/`up`/`sync`/`plan` honor restart policy; `runtime` capability matrix gains Codex managed resume |

## Failure modes

- **Daemon crash with live children** → unchanged (adopt on reconcile), plus watcher restores supervision latency and restart policy applies when survivors die.
- **Child crash-loop** → backoff caps restarts; `restart` policy + `doctor --canary` (SQU-39) separate "runtime env broken" from "agent keeps failing".
- **Session store gone** (runtime upgrade, pruned rollouts, cleaned `~/.claude` or `~/.codex`) → preflight detects, falls back to fresh spawn + brief; the event log records `resume_fallback` so the operator can see fidelity loss.
- **Duplicate-child race** → instance lock + incarnation check; resume loses to a live survivor.

## Phasing / tickets

1. Phase 1 (restart policy + adopted-child watcher) — new ticket, `internal/daemon/reconcile.go` + `instance.go`.
2. Phase 2 (brief + per-instance launch-env) — new ticket, CLI + daemon metadata.
3. Phase 3 (Codex capture + managed resume) — landed in SQU-53, spawner + `runtime_lifecycle.go` gate.
4. Phase 4 (transition notifications) — re-scope SQU-37 onto this design.

Each phase lands independently and is useful alone; SQU-44 tracks the arc.
