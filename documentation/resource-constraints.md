# Resource constraints (design sketch)

How a large agent fleet stays inside its means. Today the framework measures well and constrains almost nothing; every governor is either manual (`slots`, `replicas`), after-the-fact (usage at reap), or time-shaped (watchdogs). This sketch maps the real constraint surface — grounded in field evidence — and phases the work.

Status: brainstorm (SQU-91 epic). Nothing here is committed API.

## What actually constrains a fleet (field evidence)

1. **Build slots, not agent count.** Proven in production (SQU-42): a 12-core laptop sustains ~3 concurrent Rust builds; beyond that, replicas just queue — and shared build caches make concurrency *destructive* (artifact thrash), which is why dispatch locks (SQU-35) exist. Static `slots = N` works but is a guess the operator must maintain per machine.
2. **Tokens.** The invisible budget. A six-round review cycle on one ticket cost ~150M input tokens (SQU-68; observed via SQU-73 usage capture — *after* spending it). Subscription auth has opaque quotas; API auth has real dollars. There is no cap, no rate governor, and the failure mode is silent: a pathological bounce loop or a wedged-but-chatty runtime burns quota until a human notices.
3. **Provider rate limits.** A 429 or SSE stall from the runtime provider affects the whole fleet at once (all workers share one subscription), including the manager trying to fix it. Managed resume rescues individual hung sessions (SQU-82 field report), but nothing detects "the provider is throttling us" as a fleet-level signal.
4. **External service quotas.** N workers → N CI runs (runner concurrency caps make CI the hidden bottleneck), N `gh`/Linear API bursts. Nothing coordinates; today we stay under limits by staying small.
5. **Local machine health.** `ps` reports RSS/CPU per child now, but nothing acts on it. Watchdogs are time-based only — a runtime pinning a core for 40 minutes under its time budget is invisible.
6. **Disk** — largely handled (worktree reap policies, terminal-registry retention, archive trimming). Not a phase-1 concern.

## Design principles

- **Admission over interruption.** The cheapest place to enforce a constraint is before dispatch (refuse/queue), the second cheapest is a step boundary; mid-run interruption is last resort. The queue-don't-fail semantics (SQU-35) generalize: a budget-exceeded dispatch queues with `reason=budget_exhausted`, visible in `queue ls`, drained when the window resets.
- **Preemption is kill + requeue.** Crash-only design means taking a slot back is the same operation as recovering from a crash — already safe, already tested, already reconciled. Preempting an audit to free a slot for an incident is a policy decision, not new machinery.
- **Accounting before enforcement.** Every limit needs its measurement first. Usage-at-reap (SQU-73) + OTel queue/lock-wait spans (SQU-74) + the provenance envelope (SQU-90, prerequisite: budgets attach to *teams*, so jobs must know their team) are the measurement layer; constraints read it.
- **Constraints are topology.** Budgets and priorities live in `instances.toml` next to what they constrain — declared, versioned, reviewed like everything else.

## The layers

### Layer 1 — budgets (declarative caps, admission-enforced)

```toml
[budgets.delivery]                  # per-team, resolved via the origin envelope
tokens_per_day = 200_000_000        # sum of output+input at reap, sliding window
jobs_in_flight = 4

[pipelines.ticket_to_pr.steps.implement]
token_budget = "40M"                # per-run cap, enforced live (see layer 2)
```

Admission check at dispatch: team over budget → queue with `reason=budget_exhausted` (not fail). `agent-team budget status` shows spend vs cap per team/window. Uses only data that already exists at reap.

### Layer 2 — live usage watchdogs

The Codex JSONL stream emits `turn.completed` usage *during* the run; Claude's OTel telemetry can report live token counts. A usage watchdog is the token analog of the time watchdog: kill (crash-finalize, slot freed, attention write-back) at N tokens. Catches the chatty-wedge failure mode time budgets miss. Same extend verb (`job extend --tokens 10M`) for operator judgment.

### Layer 3 — priority + preemption

Priority classes on pipelines/dispatches (`priority = "incident" | "interactive" | "batch"`; default interactive; audits/sweeps are batch). Three behaviors:
- Queue ordering: higher class drains first (today: FIFO).
- Admission bias: batch work admits only below a load threshold.
- Preemption: an incident dispatch may kill-and-requeue the newest batch job holding a needed slot/lock. Requeued work loses nothing but its in-flight attempt — worktrees and kickoffs are durable.

### Layer 4 — provider backpressure

Detect throttling as a fleet signal (429s / SSE stalls / auth-quota errors matched by infra signatures on child logs) → circuit breaker per runtime: pause new dispatches for that runtime (queue, don't fail), let running work finish, probe with the doctor canary, resume. Prevents the whole fleet grinding against a throttled provider and burning retries.

### Later / explicitly deferred

- Adaptive slot sizing (measure build durations + load average, tune `slots` automatically).
- Cross-repo/fleet-level coordination (multiple daemons sharing one subscription — needs a shared ledger; out of scope until multi-repo deployments are real).
- Disk quotas (existing reap/retention policies suffice).

## Namespaces, scoping, and authority (the other half of the resource model)

Attribution (SQU-90) says whose a resource is; scoping says who may act on it. Today the repo is one flat namespace and every agent holds full authority — any instance can `job merge`, `kill`, `instance rm`, or dispatch anything. Prompt discipline ("the reviewer never pushes") is trust, not enforcement, and does not compose at fleet scale.

- **Scoped resources, declared not inferred.** Some resources are naturally machine-scoped (build locks — two teams SHOULD share build slots), some team-scoped (channels, schedules), some job-scoped (worktrees). Blanket prefixing is wrong; a `scope = "machine" | "team" | "job"` field on locks/channels/schedules preserves today's behavior by default and lets teams isolate where isolation is meant.
- **Authority as topology.** Per-agent (or per-team) verb allowlists on the daemon API: workers get status/inbox/feedback/gate-report on their own job; reviewers add gate verdicts; managers hold the job lifecycle verbs; the auditor is read-only plus ticket filing. Identity comes from the origin envelope that launched agents carry into daemon requests, with daemon metadata used only to fill gaps. This is blast-radius control for cooperating agents, not a security boundary against a hostile local process — state that honestly and design within it.
- **Audit mode before enforcement** (same principle as accounting before constraints): first release logs would-be violations (`authority_violation` events, visible in triage) without blocking; the observed violation stream tells us whether the bundled ACLs are right before anyone gets locked out. Enforcement is a flag flip after the data is quiet.
- **Cross-scope reads stay open.** Quality auditing delivery's jobs is the point of a quality team; scoping constrains writes/actions, not observation.

Tracked as SQU-92; the origin envelope (SQU-90) is the shared identity substrate for both halves.

## Sequencing

1. **SQU-90 provenance envelope** (prerequisite — budgets and scoping both need identity/attribution).
2. **Phase 1: budgets + admission** (layer 1) — highest value per line; uses existing at-reap data.
3. **Phase 2: live usage watchdog** (layer 2) — extends proven watchdog machinery.
4. **Phase 3: priority + preemption** (layer 3), then **backpressure** (layer 4) — each independently useful.

The vg deployment is the natural pilot: they run sustained multi-worker load on subscription auth and their field reports (SQU-42/76/82) shaped every primitive this builds on.
