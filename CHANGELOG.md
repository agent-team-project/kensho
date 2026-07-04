# Changelog

## v0.3.0 — 2026-07-04

The board-as-control-plane release. Driven almost entirely by day-1 field feedback from the v0.2.0 deployment and live dogfooding of the shipped defaults.

### The board is the control plane

- **Column-transition dispatch** — Linear-mode pipelines trigger on `ticket.status_changed` matched to `[linear].agent_column` ("Ready for Agent"): dragging the card is the dispatch gesture. Loop-safe by default (the Linear viewer id is resolved and cached; self-authored transitions are ignored fail-closed) and idempotent on re-entry (`redispatch_on_reentry` opt-in). (SQU-67, #72)
- **Board write-back** — pipeline events move the card: dispatch → in-progress, bounce → back with findings comment, no-retry terminal failure → attention state/comment/label. All failed-status transitions route through one idempotent choke point covering every dispatch, kill, reject, cancel, and reconcile path. (SQU-68, #73)

### Messaging: spawn, turn boundary, interrupt

- Unread mailbox rides in daemon-composed dispatch kickoffs — spawn-time mail blindness is impossible. (SQU-64, #69)
- **Turn-boundary soft push**: launcher-generated runtime hooks (Claude `--settings`, Codex `-c hooks.*`) inject unread mail as model-visible context at `UserPromptSubmit`/`PreToolUse`; opt-out via `[runtime.hooks]`. Functionally verified on codex-cli 0.142. (SQU-66, #70)
- **`send --interrupt`**: durable delivery + graceful stop + same-session managed resume with the message as the resume prompt — live-validated mid-`sleep 300`. (SQU-65, #71)
- **`job bounce`**: re-queue a reviewed step with findings appended to the kickoff atomically (`--advance` dispatches); `job update --kickoff` for direct edits; attention escalation warns and flags triage after repeated bounces (`[health].bounce_attention_after`). (SQU-62 #67, SQU-77)

### Observability

- **Usage accounting** — per-run token usage captured at reap (before logs can be destroyed), persisted to jobs and archives; `agent-team usage [--since] [--by job|instance|agent|runtime]`; weekly digest schedule example. (SQU-73, #79)
- **OTel** — probe-verified runtime capability memo; opt-in `[otel]` config propagates runtime-native exporter configuration (Claude OTLP env, Codex `-c otel.*`) with TRACEPARENT correlation and agent-team resource attributes; header secrets via env indirection, never argv; disabled config strips inherited telemetry on every env path. Orchestration spans (jobs as traces) land in this release via a stdlib OTLP/HTTP exporter — no new dependencies. (SQU-74, #78/#82/#83)
- `signatures test <pipeline> --against <log>` dry-runs infra signatures with matched excerpts; triage shows the matched signature so over-broad regexes are visible. (SQU-70, #75)

### Operator verbs & pipeline policy

- `land = squash|merge|rebase` per pipeline with `job merge --land` override — fork-sync workflows land merge commits through the gated pipeline. (SQU-71, #74)
- `retry_on_crash` — one automatic retry for opted-in review-type steps on crash-without-verdict; implementation steps stay `max_attempts = 1`. (SQU-72, #76)
- `job extend` / `extend` push a running watchdog deadline (`/v1/extend`), with budget/elapsed/remaining in `ps` (frozen correctly at exit — SQU-75). (SQU-69 #80, SQU-61 #68)
- Step watchdogs arm on **all** dispatch paths, including operator advances. (SQU-61, #68)

### Fixes

- `job show --json` now emits lowercase snake_case keys for pipeline step objects
  while keeping deprecated capitalized aliases for one release. Scripts should
  migrate from keys such as `ID` and `RuntimeBin` to `id` and `runtime_bin`.
  (SQU-87)
- **Cross-instance lock waiters stall** — freeing a lock now kicks the shared queue drain; previously waiters queued under other instances stalled until a manual `queue drain`. Same-hour fix for a live field report. (SQU-76)
- Bundled-template test expectations updated for the reviewer + 3-step pipeline. (SQU-57)

### Template & onboarding

- **Ticketless quickstart**: `init` with zero flags produces a working team (`pm_tool = "none"` default); Linear params required only when Linear is selected; quickstart docs. (SQU-58, #66)
- Evidence-based agent-prompt authoring pass: inbox-first startup, bounce awareness, gate reporting, staleness refresh, trailer ownership; stale model pins removed. (SQU-63)
- Narrative guides for messaging, board control, and observability. (SQU-78, #81)
- Feedback-channel contract for operating teams (`documentation/feedback-channel.md`).

## v0.2.0 — 2026-07-03

First tagged release. Everything below landed on top of the untagged v0.1.x dev line, most of it driven by the first production field report (SQU-42: ~100 daemon jobs, 35+ merged PRs over a week on a Rust monorepo).

### Recoverable managers (SQU-44)

- **Restart policy** — `restart = "never" | "on-failure" | "always"` per declared instance; topology-aware reconcile relaunches dead persistent instances with a capped, persisted backoff. Adopted children (survivors of a daemon restart) get a PID watcher so exits are observed in seconds and feed the same policy. (SQU-51, #55)
- **Instance briefs** — `agent-team instance brief <name>` generates a catch-up artifact from daemon-owned state (owned jobs, pipeline states, unread mailbox, channel cursors, recent events, team fleet). Fresh spawns prepend it to the kickoff; managed resumes deliver it via mailbox (Claude) or stdin (Codex). (SQU-52, #57)
- **Per-instance launch-env snapshots** — dispatch env is persisted (sensitive keys stripped; `OPENAI_API_KEY` never stored) and reused on resume/relaunch instead of the operator's current shell. (SQU-52, #57)
- **Codex managed resume** — daemon-managed Codex dispatches run `codex exec --json`; the session id is captured from the first `thread.started` event and `agent-team start` resumes with `codex exec resume <id> -`, with rollout/workspace preflight and fresh-spawn fallback. `attach` execs `codex resume` interactively. Live-validated: a Codex session survives a daemon-managed stop/resume with conversation memory intact. (SQU-53, #59)
- **Transition notifications** — the daemon publishes one message per configured `status.toml` phase transition (default `["blocked"]`; optional idle renotify) to `#supervisor` — no repeated same-state pings. (SQU-37, #61)

### Orchestration primitives

- **Named dispatch locks** — `[locks.<name>] slots = N` with `locks = [...]` on instances and pipeline steps; contended dispatches queue as `lock_held` and drain when slots free. Leases persist and recover across daemon crashes. `agent-team locks`, `/v1/locks`. (SQU-35, #60)
- **Structured gate results** — `agent-team job gate set / job gates`: an append-only per-job gate ledger written by the agents that ran the gates, with per-pipeline `infra_signatures` regexes classifying failures infra-vs-content across job/team triage. (SQU-36, #58)
- **Merge strategies** — `[pipelines.<name>.merge]` declares `squash | rebase | script`; `agent-team job merge <id>` applies the mechanics with `--dry-run` and drift classification (`clean`/`reconcilable`/`unclassified`). (SQU-45, #56)
- **Approval requests** — durable approval artifacts on jobs (`approval request/ls/show/approve/reject`), decisions recorded with actor + notes, optional `approval_required` on manual gates. (SQU-47, #62)
- **Event trace** — `agent-team event trace <type> [--payload k=v]` explains every trigger's match/rejection reason; `event publish --trace`; the daemon warns on zero-match publishes. (SQU-46, #53)

### Operations & hygiene

- **`doctor --canary`** — daemon-dispatched throwaway runtime smoke test with failure classification and auto-reap; the 30-second post-restart pre-flight. (SQU-39, #51)
- **Worktree auto-reap** — `reap_worktree = "on_close" | "on_merge"` per instance/pipeline (bundled worker defaults to `on_merge`); `job keep-worktree` opts a job out. (SQU-38, #52)
- **Terminal-job archival** — `[health].terminal_retention` archives old terminal jobs + daemon records into monthly JSONL; `job show`/`job events` read the archive transparently. (SQU-40, #50)
- **Build-identity handshake** — both binaries embed VCS build identity (`--version`, `GET /v1/status`); `daemon status`/`doctor` warn on CLI↔daemon mismatch; requests carry `X-Agent-Team-Build` and the daemon logs one skew warning per client identity. (SQU-54 #54, SQU-55 #63)
- **Wire compatibility** — daemon decoders tolerate unknown request fields, so a newer CLI never bricks a running daemon; the CLI omits optional fields for older daemons. Shipped as a same-hour fix for a live cross-team incident. (SQU-55)
- **Ticket-named branches** — daemon worktree branches are `<ticket>-<tag>` instead of `worktree-<instance>-<tag>`. (SQU-41, #47)
- **Readable Codex logs** — Codex JSONL child logs render as transcripts across `logs` surfaces; `--raw` preserves the stream. (SQU-56, #64)

### Bundled template

- New **adversarial `reviewer` agent** and a field-proven `ticket_to_pr` shape: implement (worktree, 45m watchdog, `max_attempts = 1`) → review (checklist instructions, content-only carve-outs, 30m watchdog) → approve (`gate = "manual"`), with `auto_advance`. (SQU-57)

### Fixes

- `runtime probe` tolerates non-string `codex doctor --json` detail values (codex-cli 0.142.x). (SQU-34, #46)
- Main CI restored: a path-assertion test only passed on macOS via the `/tmp` symlink; demo harness expectations updated after the `--repo` hint migration. (SQU-49)
- Demo and tests are hermetic against inherited `AGENT_TEAM_*` env from parent workers. (SQU-50, #48)
- Doctor no longer warns about template provenance for self-dogfood symlinked teams. (SQU-43, #49)
- Managed Codex resume falls back to a default prompt when the generated brief is empty. (SQU-44)

### Docs

- `documentation/operating-model.md` — field-tested operating defaults (two-plane model, small-jobs safety unit, gate discipline, capacity planning, shared-box upgrade sequence).
- `documentation/recoverable-managers.md` — the SQU-44 design with landed-state tables.
