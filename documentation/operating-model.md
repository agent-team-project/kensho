# Operating model (field-tested)

How to run an agent-team deployment at production volume. Everything here was proven during a week-long, ~100-job, 35+-merged-PR run on a Rust monorepo (three concurrent manager streams, Claude managers, subscription-auth Codex workers — see SQU-42 for the full field report). Treat these as defaults to start from, not rules.

## The two-plane model

Operationally, a running team splits into two planes:

- **The daemon plane executes.** Events → pipeline dispatch → worktree/PR artifacts → process-exit auto-advance. Everything here is headless, durable, and survives supervisor loss: jobs, branches, PRs, and queue state persist across session death and daemon restarts.
- **The mailbox plane judges.** Plans, gate reports, approvals, escalations flow as messages between managers and the supervising human/session. Nothing executes here; it's where discretion lives.

Managers bridge the two. Headless execution should never wait on judgment except at explicit gates — if you find a pipeline stalled on a question, the fix is usually a missing gate, not a chattier agent. (Making managers themselves daemon-recoverable is tracked in `recoverable-managers.md` / SQU-44.)

## Small jobs are the unit of safety

There is deliberately no mid-job steering: once a worker is running, your interventions are inbox messages at step boundaries, kill, or wait. That constraint is acceptable — and the system safe — precisely when jobs are small and well-scoped. Scope every job so that throwing its work away is a tolerable outcome; the harness rewards this shape everywhere (worktree isolation, cheap re-dispatch, PR-per-job).

Corollary: **ticket quality is the highest-leverage input.** A ticket with root-cause pointers and acceptance criteria turns a one-shot worker into a reliable implementer; a vague ticket produces a plausible-looking PR that fails review.

## Gate discipline over agent quality

High-volume automation stays safe because every job funnels through the same mandatory, machine-checkable gates — build, tests, lint, coverage floors that only ratchet upward — not because any agent is trusted. The harness's job is to keep that funnel unavoidable:

- Encode gates as the same commands for every worker (`make` targets, `go test ./...`, whatever your repo's truth is). Workers, reviewers, and CI must run the same thing.
- Floors ratchet: a count that can silently decrease is not a gate.
- Structured per-gate results (infra-red vs content-red classification) are tracked in SQU-36; until then, teach reviewers explicitly which failures are not theirs to judge.

## The pipeline shape that works

```toml
[pipelines.ticket_to_pr]
trigger.event = "ticket.status_changed"
trigger.match.status = "Ready for Agent"
redispatch_on_reentry = false
auto_advance = true

[[pipelines.<name>.steps]]
id = "implement"      # ephemeral worker, worktree isolation, watchdog timeout
[[pipelines.<name>.steps]]
id = "review"         # adversarial reviewer, checklist instructions
after = ["implement"]
[[pipelines.<name>.steps]]
id = "approve"        # gate = "manual" — the only place execution waits on judgment
after = ["review"]
gate = "manual"
```

- **The board is the dispatch control plane.** In Linear-backed repos, moving a card into the configured agent column (`linear.agent_column`, default `Ready for Agent`) is the intentional dispatch gesture. Creation-triggered dispatch remains useful for zero-config demos, but production queues should be column-gated.
- `auto_advance` chains headless steps on process exit; the manual gate stops exactly where merge judgment is needed.
- **Re-entry is idempotent by default.** Dragging the same ticket out and back in no-ops while a job is queued/running/blocked, and terminal jobs no-op unless the pipeline sets `redispatch_on_reentry = true`.
- **Loop protection is actor based.** Set `linear.agent_user_id` or cache the Linear `viewer { id }` response so agent-authored status changes do not dispatch the same ticket again.
- **Watchdog timeouts on every headless step** (45–60m implement, 30m review). Runtimes can wedge mid-stream with no client-side timeout; force-kill + crash-finalize + freed slot is the correct recovery.
- **`max_attempts = 1` for implementation.** A hang can strike *after* the PR opens; an auto-retry then opens a duplicate. Let the manager re-dispatch — it knows whether the artifact already exists. Read-only/idempotent review steps can opt into `retry_on_crash = true`, which retries once only after a crash/nonzero exit with no recorded gate/verdict.
- Run parallel pipelines distinguished by trigger event; distinct events mean zero cross-dispatch.

## Reviewer instructions are checklists

Step `instructions` in the pipeline TOML are the highest-leverage steering surface — iterate on them like code. What works:

- Hand-verifiable items only ("hand-check 2–3 expected rows against the spec", "verify the count floor moved up").
- Explicit not-your-job carve-outs: infra state, base drift, anything a signature can classify. "DIRTY from base drift is NOT a content bounce" eliminated a whole class of false rejections.
- Judge content only; report gates as data (SQU-36), not prose verdicts.

## Capacity is build resources, not agent count

Replicas beyond your machine's concurrent-build ceiling just queue (a 12-core/24GB laptop sustains ~3 concurrent Rust builds; Go repos are cheaper). Shared build caches make concurrent builds *interfere* (artifact thrash → spurious failures), which is worse than queueing — serialize via dispatch locks (SQU-35) rather than manager convention. Plan replica counts around build slots and treat everything above that as queue depth.

## Auth-mode fidelity

Subscription-auth Codex flips to API billing if `OPENAI_API_KEY` is present in the daemon environment. The daemon's launch-env snapshot strips it by default; keep it that way, and verify with `codex login status` ("Logged in using ChatGPT") plus `agent-team runtime probe --runtime codex` before high-volume dispatch. Any team mixing auth modes across runtimes will eventually hit this — check it first when Codex behavior changes after a restart.

## Supervisor ergonomics compound

The difference between a smooth operating day and a grinding one is almost always noise, not hard failures: idle pings, spurious bounces, stale registry rows. When supervision feels expensive, fix the signal path (transition-only notifications, structured gate results, terminal-entry retention — SQU-37/36/40) before adding more supervision.

## Upgrading binaries on a box with running daemons

A long-running daemon plus an independently updated CLI is the *normal* state, and two traps live there (both hit in production):

- `go install` replaces the shared binaries, but every running daemon keeps executing the old code — and `daemon restart` relaunches whatever path its launch-env snapshot recorded, which may not be the binary you just rebuilt. `daemon status`/`doctor` warn on CLI↔daemon build mismatch (SQU-54); trust that warning over your memory of what you built.
- Wire compatibility is additive-tolerant (daemons ignore unknown request fields — SQU-55), but don't lean on it across large version gaps.

The validated upgrade sequence: `go install ./cmd/agent-team ./cmd/agent-teamd` → wait for an empty fleet (or accept orphaned-adoption on running workers) → `agent-team daemon restart` (it prints which binary path it relaunched) → `agent-team doctor --canary` before dispatching real work.

## Recovery expectations

- The daemon plane survives supervisor loss; re-attach with `agent-team overview` / `next` / `monitor`, then read owned jobs (`job ls`, `team triage`).
- After any daemon restart, verify the runtime environment before dispatching real work (`doctor --canary` once SQU-39 lands; until then a throwaway dispatch whose kickoff runs `node --version && codex --version`).
- An empty `child.log` seconds after spawn = the runtime binary didn't start (PATH/node resolution) — an environment failure, never a content failure.
