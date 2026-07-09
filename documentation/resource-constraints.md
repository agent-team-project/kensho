# Resource constraints (design sketch)

How a large agent fleet stays inside its means. Today the framework measures well and constrains almost nothing; every governor is either manual (`slots`, `replicas`), after-the-fact (usage at reap), or time-shaped (watchdogs). This sketch maps the real constraint surface — grounded in field evidence — and phases the work.

Status: SQU-91 epic sketch. Team admission budgets shipped first; SQU-104 added
soft per-job/per-step allowances and notices. SQU-105 adds opt-in hard runtime
cutoffs (`hard = true` or `hard_multiplier`) that kill runaway jobs with
watchdog semantics.

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
load_weight = 1.0                   # adaptive concurrency units per dispatch

[pipelines.ticket_to_pr.steps.implement]
token_budget = "40M"                # per-run cap, enforced live (see layer 2)
```

Admission check at dispatch: team over budget → queue with `reason=budget_exhausted` (not fail). `agent-team budget status` shows spend vs cap per team/window. Uses only data that already exists at reap.
`load_weight` is also read by the daemon-wide adaptive concurrency governor:
`1.0` preserves the baseline `load_per_dispatch`; heavier teams can declare
larger values so their workers reach the shared ceiling sooner.

### The economic model (added after phase 1 shipped)

Phase 1's team budgets are a governor: admission control, invisible to the governed. The full system is an economy — every team optimizes its output under constraints, which requires three properties the governor lacks:

- **Allowances delegate down a hierarchy — and the hierarchy is a tree invariant.** At every node, allocations to children may never exceed the node's own allocation, recursively: give a team $100 and its manager cannot promise its jobs (or future sub-teams) more than $100 combined. Operator sets team budgets; managers attach sub-allowances to the jobs they dispatch (`job create --budget-tokens 20M --budget-time 45m`); topology sets per-agent-type defaults. Two allocation semantics, declared per budget: `reserve` (granting an allowance debits parent headroom immediately — outstanding promises bounded, right for currency-like budgets) and `oversubscribe` (only consumption counts against the parent; admission gates on spend — right for sliding token windows, the phase-1 default). Topology teams are flat today, so the live tree is operator → team → job; the invariant is defined on the allocation tree, not on team nesting, so sub-teams inherit it unchanged if they ever land.
- **Constraints are visible to the constrained.** An agent cannot optimize under a limit it cannot observe. Soft threshold crossings (default 50/80/100%) deliver `budget_notice` mailbox messages — model-visible mid-run via turn-boundary hook injection — and agents can self-query their remaining allowance. Live token signal comes from the Codex JSONL stream already written to child.log; Claude runtimes expose time reminders only until reliable token telemetry is available.
- **Escalation is the market mechanism.** At 80%, wrapping up and requesting an extension are both rational; `job extend --tokens/--by` serves operators and managers now, approval-gated extension requests route the decision to whoever owns the parent budget later.

Soft and hard are different verbs: soft 100% notifies, flags triage, and lets work finish; hard cutoff (explicit `hard = true` or a multiplier) is the token analog of the time watchdog — kill, crash-finalize, freed slot, attention write-back. Time budgets unify under the same vocabulary and levels.

SQU-106 implements the allocation half of this model for flat topology teams:
`[budgets.<team>].allocation` defaults to `oversubscribe`, while `reserve`
atomically records outstanding child allowance grants and gates on consumed +
allocated + requested headroom. `agent-team budget status` now shows both
consumed tokens and outstanding allocated promises.

### Layer 2 — live usage watchdogs

The Codex JSONL stream emits `turn.completed` usage *during* the run; Claude's OTel telemetry can report live token counts. A usage watchdog is the token analog of the time watchdog: kill (crash-finalize, slot freed, attention write-back) at N tokens. Catches the chatty-wedge failure mode time budgets miss. Same extend verb (`job extend --tokens 10M`) for operator judgment.

SQU-104 implements the soft precursor: the daemon tails the live Codex JSONL
with the same parser used at reap, records `budget_notice` events, and sends
mailbox reminders. SQU-105 layers opt-in hard cutoffs on the same live watcher:
crossing the hard line records `budget_exceeded_hard`, marks the runtime
crashed, terminates it with the watchdog signal path, and lets normal
reap/failure write-back free the slot and surface attention. Claude paths never
fake token counts; they can only trigger time-budget notices and hard time
cutoffs until reliable live token telemetry is available.

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

Attribution (SQU-90) says whose a resource is; scoping says who may act on it. The early repo was one flat namespace where any instance could `job merge`, `kill`, `instance rm`, or dispatch anything. Prompt discipline ("the reviewer never pushes") was trust, not enforcement, and did not compose at fleet scale.

- **Scoped resources, declared not inferred.** Some resources are naturally machine-scoped (build locks — two teams SHOULD share build slots), some team-scoped (channels, schedules), some job-scoped (worktrees). Blanket prefixing is wrong; a `scope = "machine" | "team" | "job"` field on locks/channels/schedules preserves today's behavior by default and lets teams isolate where isolation is meant.
- **Authority as topology.** Per-instance/agent/team verb allowlists on the daemon API: workers get status/inbox/feedback/gate-report on their own job; reviewers add gate verdicts; the persistent manager holds the job lifecycle verbs; loops stay narrow. Identity comes from the origin envelope plus daemon metadata and topology, not caller self-assertion. This is blast-radius control for cooperating agents, not a security boundary against a hostile local process — state that honestly and design within it.
- **Audit mode before enforcement** (same principle as accounting before constraints): first release logs would-be violations (`authority_violation` events, visible in triage) without blocking; the observed violation stream tells us whether the bundled ACLs are right before anyone gets locked out. Enforcement flips to deny once the data is quiet.
- **Cross-scope reads stay open.** Quality auditing delivery's jobs is the point of a quality team; scoping constrains writes/actions, not observation.

Tracked as SQU-92; the origin envelope (SQU-90) is the shared identity substrate for both halves.

## Sequencing

1. **SQU-90 provenance envelope** (prerequisite — budgets and scoping both need identity/attribution).
2. **Phase 1: budgets + admission** (layer 1) — highest value per line; uses existing at-reap data.
3. **Phase 2: live usage watchdog** (layer 2) — extends proven watchdog machinery.
4. **Phase 3: priority + preemption** (layer 3), then **backpressure** (layer 4) — each independently useful.

The vg deployment is the natural pilot: they run sustained multi-worker load on subscription auth and their field reports (SQU-42/76/82) shaped every primitive this builds on.

## North star: allocation as optimization (advisor framing, 2026-07-06)

The endgame for the budget economy + org-review loop: treat the fleet as a portfolio and continuously answer *"how should the budget be allocated to best serve the project goals?"* Three prerequisites, in dependency order:

1. **Goals as declared artifacts.** Optimization needs a target. Today goals live in the roadmap's themes — good enough to start: each roadmap theme becomes a weighted goal the ledger can attribute work to (tickets/epics tagged by theme).
2. **Yield measurement.** The outcomes ledger (SQU-135) prices each stream: goal-attributed completions per token, weighted by review quality (a merged PR that bounced five times cost more than its tokens). Yield per team per theme is the core signal.
3. **Reallocation proposals.** The org-review loop (SQU-139) graduates from health checks to portfolio rebalancing: shift budget from low-yield to high-yield streams *relative to goal weights*, propose team creation where a weighted goal has no owner, dissolution where yield stays marginal. Proposals-only, evidenced, reviewed — the same discipline as everything else.

Honest constraint: outcome *value* is a proxy problem (a merged PR ≠ equal value; a prevented incident is invisible). Start with goal-attributed throughput and iterate on valuation — measuring imperfectly beats optimizing blind.
