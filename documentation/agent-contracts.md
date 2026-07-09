# Agent-to-Agent Contracts — typed, checkable handoffs

**DESIGN DECISION** — 2026-07-09. Formalizes the handoff boundaries between Kensho's agents
as explicit contracts, replacing prose-only kickoffs where prose is the weak link. Grounded
in the constitution §II (a spec is a lossy compression of intent; the tower is only as
aligned as its weakest handoff, #263), the two-way clarification channel (#286), the MCP
typed-tool surface (#267), composable units (#223, `composable-units.md` /
`unit-types.md`), and the bounce-classification policy (`model-economy.md` §6).
Ratification per the amendment process; open questions flagged in §8.

---

## 1. The decision in one paragraph

> **Every handoff boundary in Kensho carries a contract: a small, typed, checkable
> agreement — deliverable, gate tier, acceptance criteria, required trailer — that the
> producer commits to and the consumer (or a machine gate) verifies clause by clause.
> The contract lives on the durable job record, is authored by the manager at dispatch
> (compiled from the ticket's acceptance criteria), is rendered into the worker's kickoff,
> is the reviewer's checklist, and is the object a bounce cites. It is not a new parallel
> system: it is the structuring of what the gates, the deliverable check (SQU-155), the
> ticket criteria, and the reviewer protocol already do in prose.**

A contract here means exactly: typed inputs, a typed output (the deliverable), invariants
(scope, conventions-by-reference), preconditions (what must exist before the consumer
runs), and acceptance criteria — each clause independently verifiable by someone other
than the producer. What a contract is **not**: a plan (it never says *how*), a schedule,
a difficulty estimate, or a formal-methods specification language.

## 2. Why contracts over prose

Four arguments, each anchored to a standing Kensho principle:

1. **Verifiability.** "Contract met?" is a checkable predicate; "the kickoff was
   satisfied" is a judgment call. Ground-truth-over-proxy (constitution §III.5) demands
   that every quality claim be independently checkable — a contract is the unit at which
   that checking happens. The gate ledger (`job gate set … --status pass|fail
   --signature`) already proves this works: gates are contract clauses that grew up first.
2. **Reduced intent loss.** §II.1: a spec is a lossy compression of intent. Prose loses
   intent *silently* — the reader cannot tell which sentences are binding and which are
   color. A typed contract is a better codec for the checkable subset of intent: the
   deliverable, the observable behaviors, the gate that must go green are pinned exactly;
   what remains lossy is then *known* to be judgment, and the reviewer's judgment is
   pointed at precisely those places instead of re-deriving the whole spec from prose.
   The residual ambiguity has its own channel — clarification (#286), not guessing.
3. **Composability.** Units compose "because their studs align, not because each pair
   knows bespoke glue" (#223). Studs are contracts. The same is true one level down: a
   pipeline auto-advances safely only because each step's output is what the next step's
   input requires. Typed handoffs are the substrate that lets work compose like typed
   functions — and the context-partitioning machinery of #261: a consumer needs the
   producer's *contract*, never the producer's transcript.
4. **Independent verification.** The reviewer already embodies this: "Read the PR body,
   then distrust it. It is the worker's claim, not evidence" (reviewer agent, Phase 0.3).
   A contract makes the thing verified explicit and shared: producer and verifier hold
   the *same* clause list, so verification is against the agreement, not against the
   producer's own account of the agreement.

And one economic argument: the bounce is Kensho's costliest routine event (a full
verify + review cycle, `model-economy.md` §6.5). A large fraction of bounces are
spec-shaped — the worker built a plausible thing that wasn't what was meant. Contracts
attack that class at the source (the criteria are explicit before work starts), and #286
attacks the remainder in flight (ask before building). Bounce classes are the measurement
of whether it's working (§6).

## 3. What already exists — reuse, don't parallel

Kensho already runs on proto-contracts. This design **promotes** them; it does not build
beside them:

| Existing mechanism | Contract reading |
|---|---|
| **Deliverable verification (SQU-155)** | The first machine-checked contract clause in production. A job's deliverable contract is derived from dispatch intent (`workspace=worktree` / `ticket_to_pr` → PR; `report:<path>`; probes exempt), exported as `AGENT_TEAM_DELIVERABLE`, and verified on done — an actually-open PR, pushed branch, or non-empty diff. Breach → `deliverable_missing` event, job failed, manager mailbox. This is the template for every other clause. |
| **Pipeline gates + gate ledger** | A gate *is* a verified contract clause: named check, pass/fail, one-line signature, infra-vs-content classified. The `agent-team-verify-gates` block in step instructions is already a declared clause list. |
| **Verifier evidence** (`target/agent-evidence/<job>.json`) | The producer-side fulfillment record for the gate clauses, pinned to a commit — and the reviewer already treats an evidence/head mismatch as "proves nothing" (reviewer Phase 0.1). |
| **Acceptance criteria in tickets** | The criteria clauses, today in prose. The reviewer already writes them down "as your checklist before opening any code" (Phase 0.2), and the pipeline `review` step instructions already say "check the specific behaviors, not the PR description." The field report's highest-leverage input — "a ticket with root-cause pointers and acceptance criteria turns a one-shot worker into a reliable implementer" — is contract authoring by another name. |
| **Required PR trailer** (`Closes #n` / `Advances #epic`) | A typed output clause: reviewer bounces on its absence today. |
| **Bounce findings → kickoff** (`## Review findings (bounce N)`) | The breach report channel: findings are appended to the kickoff, the one channel a fresh worker reliably reads. |
| **Traceability skill** (`REQ-n` → jobs → gates → evidence) | The cross-job contract ledger: stable IDs joined to proof, with `delivered / unproven / specified / gap` statuses. Contract criteria IDs feed it directly. |
| **Unit contract studs** (#223) | The same idea at unit grain: events in/out with named schemas, budget draw, capability need, deadline, reap signal. "Deadlines are contract data, not prompt prose" is already doctrine there. |
| **MCP surface** (#267) | The contract at the agent↔system boundary: tool schema = typed clause list = granted capability. Same object, different boundary. |

**Decision: one contract concept, four boundaries, existing rails.** No new store, no new
daemon subsystem, no contract DSL. The job record carries it, the kickoff renders it, the
gates check it, the bounce cites it.

## 4. The handoff boundaries and their contracts

### 4.1 Manager → worker: the dispatch contract

The producer of the *artifact* is the worker; the author of the *contract* is the
manager (the consumer of the output, acting as delegate for intent above it —
constitution §II.3). Authored at dispatch, compiled from the groomed ticket.

**Schema** — a `[contract]` block on the durable job record
(`.agent_team/jobs/<id>.toml`, peer to `[origin]`):

```toml
[contract]
schema      = "agent-team.contract.v1"
work_item   = "#286"                # ticket / issue / job id (pm.provider = "none")
deliverable = "pr"                  # "pr" | "report:<path>" | "none" — SQU-155 semantics, unchanged
trailer     = "Advances #286"       # exact required PR trailer
gates       = "smoke"               # tier from .agent_team/gates.toml the deliverable must pass
scope       = ["internal/daemon", "template/agents/worker"]   # optional: intended surface

[[contract.criteria]]
id     = "AC1"
text   = "A worker mid-implement can emit a clarification_request that lands in the dispatching manager's inbox."
verify = "go test ./internal/daemon -run TestClarificationRoute"   # optional machine check

[[contract.criteria]]
id     = "AC2"
text   = "The manager's answer is appended to the worker's kickoff under '## Clarifications' and survives daemon restart."
verify = "review"                   # default: reviewer hand-verifies

[[contract.clarifications]]         # appended during execution — see §4.5 / #286
criterion = "AC1"
question  = "Should a pending question block the step or continue-at-risk?"
answer    = "Continue-at-risk; policy knob comes later."
by        = "manager"
at        = 2026-07-09T14:02:00Z
```

- **Criteria are observable behaviors**, phrased so a stranger can check them — the same
  bar the reviewer already applies ("hand-verifiable items only," operating-model.md).
  Stable `AC` ids make them citable in findings, gate results, and the traceability
  matrix.
- `verify` is a hint, not a mandate: a command string means the verifier or reviewer can
  check it mechanically; `"review"` (the default) means it's a judgment clause for the
  adversarial reviewer. Most criteria will be `"review"` — that is fine; the point is
  the *list* is shared and pinned, not that everything is scripted.
- `scope` is advisory (it feeds the reviewer's Phase 3 drive-by check and the scope
  bounce class), never a hard filesystem fence — workers legitimately touch tests, docs,
  and callers the manager didn't enumerate.
- **Rendered into the kickoff** under a fixed `## Contract` heading, exactly as bounce
  findings use `## Review findings (bounce N)` — the kickoff remains the one channel a
  fresh worker reliably reads. The durable copy on the job record is what the reviewer
  and daemon read; the kickoff copy is a rendering, never a divergent source.

**What is deliberately left out** (the anti-bureaucracy clause): no priority, no
estimate, no difficulty tag or model tier (**firewall** — the contract is rendered into
observed prompts; model-economy §5 forbids scores there), no implementation plan, no
file-by-file change list, no formal pre/postcondition logic, no JSON-Schema'd payloads in
v1. Every omitted field is something a manager would fill in badly under time pressure
and a worker would either ignore or over-fit to.

**Authoring flow.** Two paths, one source of truth:

- *Manager-dispatched jobs*: `agent-team job create … --contract-file <path>` (or
  repeated `--criterion "AC1: …"` flags) — small mechanism delta, §8.
- *Board-triggered pipeline jobs* (no manager in the loop at trigger time): the contract
  is compiled from the ticket body's `## Acceptance criteria` section at dispatch —
  grooming *is* contract authoring, which is where Kai's judgment already lives
  (model-economy §4: decomposition moves intelligence upstream). A ticket entering the
  agent column without criteria dispatches with a deliverable-only contract and emits a
  `contract_missing` warning to the manager mailbox. V1 warns; the ratchet to
  hard-reject is an open question (§8) — floors ratchet, but not before the floor exists.

### 4.2 Worker → reviewer: the fulfillment contract (and the evidence contract)

Two artifacts cross this boundary, each with a contract:

**(a) The diff-meets-contract claim.** The worker's PR asserts: deliverable exists,
trailer present, gates green, every criterion met. The contract object is the *same*
`[contract]` block — nothing is re-authored. The reviewer's protocol maps onto it
clause-for-clause, and mostly already does:

| Clause | Reviewer verification (existing protocol) |
|---|---|
| `criteria` | Phase 0.2 checklist → Phase 1/2 per-criterion verification; Phase 2.1 "name the test that covers it" per criterion |
| `gates` | Phase 0.1: read evidence, don't re-litigate green, spend judgment where the machine is blind |
| `trailer` | Phase 0.3 trailer bounce (today's rule, now a named clause) |
| `scope` | Phase 3 drive-by/dead-code check |
| `deliverable` | Already daemon-verified before review dispatches (SQU-155) |

The change for the reviewer is **output shape, not workload**: the verdict cites clauses.
`REVIEW: APPROVE` carries the verification ledger keyed by `AC` id (criterion → command /
trace → observed result — already required in prose by Phase 5); `REVIEW: BOUNCE` carries
findings that each name the clause they breach (§6). Findings that map to no clause are
still findings — and are recorded as `clause = "none"`, which is the contract-quality
signal (§6.3).

**(b) The verifier-evidence contract.** Producer: verifier. Consumer: reviewer (and the
approve gate). Clauses, all existing practice made explicit:

- evidence file exists at `target/agent-evidence/<job>.json` with summary;
- pinned to the PR head commit (mismatch = every gate claim unverified — reviewer
  Phase 0.1, verbatim);
- one entry per *declared* gate in the step's `agent-team-verify-gates` block — a
  missing entry is a breach, not an omission;
- each entry mirrored to the gate ledger via `job gate set`.

### 4.3 Pipeline step → step: the advance contract

The pipeline is a chain of contracts, and `auto_advance` is the reason they must be
checked: a headless chain that advances on process-exit-0 advances on *claims*. SQU-155
already fixed this for the implement step ("a step is done when its deliverable
verifiably exists, not when its process exits 0"). **Decision: generalize that rule to
every step — advance-on-verified-output.** Each step declares (or defaults) what it
consumes and what it must leave behind:

| Step | `needs` (preconditions) | `produces` (verified on done) | Gate |
|---|---|---|---|
| `implement` | job + contract + worktree | deliverable (PR/branch/diff — SQU-155, shipped) | watchdog + `max_attempts = 1` |
| `verify` | deliverable, head commit | evidence JSON pinned to head; ledger entry per declared gate | deterministic: every declared gate pass |
| `review` | evidence, contract, diff | `review` gate result + verdict comment with clause-keyed ledger/findings | adversarial |
| `approve` | verdict, gate ledger | merge + ticket closed + step done, or bounce with findings | `gate = "manual"` — the only judgment wait |

Schema delta (pipeline TOML, defaults derived from target kind so the standard pipeline
declares nothing new):

```toml
[[pipelines.ticket_to_pr.steps]]
id       = "verify"
needs    = ["deliverable"]                 # daemon blocks dispatch until verified
produces = ["evidence@head", "gates:smoke"]  # daemon verifies before auto-advance
```

A step whose `produces` cannot be verified on exit finalizes exactly like
`deliverable_missing` today: step failed, typed event, manager mailbox. This closes the
whole "exited 0 with nothing to show" class, not just the docs-writer instance of it.

### 4.4 Unit → unit: the stud contract (#223)

Already designed — this document aligns vocabulary and adds one commitment, it does not
re-decide `composable-units.md`:

- A unit **type's** contract is its studs: events in/out with named schemas
  (`agent-team.work-item.v1`), budget draw, capability need, deadline, reap signal.
  Composition validation checks stud alignment before admission — the contract check at
  composition time.
- A unit **instance's** fulfillment is its unit-report: resource-shaped, evidence URIs,
  consumed by downstream gates ("Output lacks evidence → downstream gate stays blocked;
  parent treats the report as advisory only" — already doctrine).
- **The alignment commitment:** the job `[contract]` of §4.1 is the *intra-unit* grain of
  the same object. When #223 v1 lands, a `feature-delivery` unit's `contract.outputs`
  evidence list (`pr_url`, `branch`, `gate.tests`) is the composition-level projection of
  the job contract's `deliverable` + `gates` clauses, and a unit-report cites the job
  contracts it fulfilled by URI. One envelope (`agent-team.contract.v1`), three grains:
  clause (gate), job (this doc), unit (studs). Do not let these fork.

### 4.5 The clarification channel is part of the contract, not beside it

#286 is how a consumer resolves contract *ambiguity*; this design gives its answers a
durable home. **Decision: clarification answers amend the contract.** A worker's
`clarification_request` names the clause (or `general`); the manager's answer is appended
to `[[contract.clarifications]]` on the job record *and* to the worker's kickoff context.
Consequence: the reviewer verifies against the amended contract — a worker who asked and
was answered can never be bounced against the un-amended reading. Asking is the mechanism
(§II.2); recording the answer is what makes asking *safe*.

## 5. Where the contract lives — and where it doesn't

**Decision: the durable job record is the canonical home.** Rejected alternatives:

- *Ticket metadata*: provider-dependent (Linear / GitHub / `none`), mutable outside the
  daemon's view, and invisible to ticketless jobs. The ticket's `## Acceptance criteria`
  section remains the human-authored **source** the manager compiles from — the job
  contract is the executable, pinned copy at dispatch time. (Compile, don't reference:
  a ticket edited mid-flight must not silently change what the reviewer verifies; a
  deliberate mid-flight change is a clarification amendment, §4.5.)
- *A new contract object/store*: a parallel system with its own lifecycle, exactly what
  §3 forbids. The job record already has the right lifetime (created at dispatch,
  finalized at terminal state, read by every party, retained in outcomes) and the right
  precedent (SQU-155's deliverable contract lives there in all but name).

Who reads it, and how:

- **Worker**: `## Contract` section in the kickoff; durable copy via
  `job show $AGENT_TEAM_JOB_ID --json`.
- **Reviewer / verifier**: the job record — never the PR body (the PR body is the
  producer's claim).
- **Daemon**: `deliverable`, step `needs`/`produces`, `gates` tier — the machine-checkable
  clauses.
- **Observer side** (traceability, org-review, harness-review): criteria IDs joined to
  gate ledger + findings, feeding bounce-class and contract-quality trends. Observer-only,
  per the metrics firewall.
- **MCP surface (#267)**, when it lands: `contract.show`, `clarification.request`,
  `clarification.answer` as typed verbs — the contract becomes literally discoverable
  through the tool schema instead of parsed out of kickoff prose.

**The right grain.** Hard caps, because too heavy kills the small-jobs safety unit:

- **≤ 7 criteria per contract.** More criteria means the ticket is too big — split it
  (that is a scope decision, model-economy §6.2, never a formatting decision). The chess
  retro's #1 lever was fine slicing; a fat contract is a monster job wearing structure.
- **One deliverable per contract.** Multi-deliverable work is multiple jobs.
- Authoring a contract must cost the manager *less* than writing today's good ticket,
  because it is the same content with headings — net-new ceremony ≈ zero. If contract
  authoring is ever the grooming bottleneck, the schema is wrong; cut fields, not corners.

## 6. Verification and breach

### 6.1 The clause → checker → breach map

| Clause | Checked by | On breach |
|---|---|---|
| `deliverable` | daemon, on done (shipped — SQU-155) | `deliverable_missing`, job failed, manager mailbox |
| step `produces` | daemon, before auto-advance (§4.3) | step failed + typed event — never advance on a claim |
| `gates` tier | verifier, deterministic | gate fail + signature → infra/content classified upstream of any judgment |
| `trailer` | reviewer today; mechanizable daemon check later (§8) | bounce citing the trailer clause |
| `criteria` | reviewer, per criterion, ground truth over the PR's prose | `REVIEW: BOUNCE`, findings keyed by `AC` id |
| `scope` | reviewer Phase 3 | scope-class bounce → split, never escalate |
| studs (unit) | composition validation at admission; downstream gate on reports | reject before admitting; report advisory-only without evidence |

### 6.2 Breach reports cite clauses

The bounce findings file gains one field per finding: the clause it breaches.

```
FINDING 1  clause=AC2  file=internal/daemon/event.go:212
  Clarification answers are appended to kickoff but lost on daemon restart.
  How known: killed daemon mid-job; `job show` after restart lacks the section.
  Passing looks like: answer present in kickoff after restart (AC2).

FINDING 2  clause=none  file=internal/daemon/event.go:305
  New write path skips the store mutex held by every other writer.
```

This is strictly sharper than prose findings: the worker's fix target is a named clause
with a named pass condition (findings were already required to be executable work items —
reviewer Phase 5; the clause key just removes the last ambiguity about *which promise*
failed), and the manager's classification (§6.3) gets its evidence pre-sorted.

### 6.3 Breach vs ambiguity — the bounce-classification link

The contract makes model-economy §6.2's classification mechanical instead of forensic:

- **Clause well-formed and unmet** → **capability bounce.** The producer understood the
  promise and failed to keep it. Route per §6.3 of model-economy: findings-guided retry
  at the same tier for mechanical misses, escalate one tier on comprehension failure or
  second bounce. A breach is the *only* bounce class where a bigger model is ever the
  answer.
- **Clause ambiguous / underdetermined** (the finding argues about what the clause
  *meant*) → **spec-ambiguity — not a breach.** The defect is in the contract, not the
  producer. Route to #286: clarify, amend the contract (§4.5), re-dispatch **same tier**.
  A bigger model guesses intent more fluently; it does not know intent. Never escalate on
  ambiguity.
- **Findings with `clause=none` dominate the bounce** → the contract was **incomplete**:
  real defects the criteria never covered. Still a valid bounce (correctness clauses like
  "no concurrency defects" are the repo's standing invariants, in force whether or not
  restated per-job) — but it is charged to *contract authoring*, not just the worker:
  harness-review reads the `clause=none` rate per manager/ticket-template as the
  contract-quality metric, and the fix is a better criteria template, not a bigger
  worker.
- **Gate red matching `infra_signatures`** → infra, re-run, never a tier or contract
  signal. Unchanged.

Standing telemetry (observer-only, ledger-side): breach rate per clause type,
`clause=none` share of bounce findings, clarification count per contract, criteria count
distribution. None of it ever appears in an observed prompt.

## 7. Guardrails — contracts without bureaucracy

The #261 frame is binding: every gate, review, and handoff artifact is a coordination tax
paid because units aren't yet reliable enough to trust unsupervised, and the framework's
job is the *minimal-sufficient* bureaucracy for the current capability frontier — shed as
the frontier advances. Contracts must obey it:

1. **Contracts restructure existing prose; they never add a document.** The v1 contract
   is today's good ticket (criteria, trailer rule, gate expectation) with stable IDs and
   a durable home. If a proposed field has no checker, it does not enter the schema.
2. **Ceremony is asymmetric by checker cost — shed the expensive kind first.**
   Machine-checked clauses (deliverable, `produces`, gate tiers, trailer) are nearly free
   at run time and are the *last* to shed; they are gate discipline, not
   model-compensating ceremony. Judgment-checked clauses (fine-grained criteria,
   scope lists) are the tax that shrinks as models improve: a stronger worker gets one
   outcome-level criterion where today's gets five step-level ones, and `scope` is
   dropped entirely for workers that no longer drive-by. The lever is criteria
   *granularity*, decided by the same routing judgment that picks the tier
   (model-economy §5) — coarser contracts for stronger units is #261's
   "capability-aware slicing" applied to specification. org-review's standing
   "what bureaucracy can we now delete?" question explicitly covers contract fields.
3. **When a prose kickoff is genuinely sufficient, use it.** Deliverable-only contracts
   (the SQU-155 clause plus a trailer, no criteria block) are the sanctioned floor for:
   probe jobs (report-only by existing contract), docs/prose-class work (cheap, loud
   failures — unit-types §2.1), one-line mechanical fixes with exact pointers, and any
   T3-routed job whose thrown-away cost is trivial. The rule of thumb: **if the bounce
   would be cheaper than the authoring, don't author.** The contract system must never
   make a 10-minute fix need a 15-minute dispatch.
4. **Contracts pin outcomes, never designs.** A contract that names functions, files, or
   approaches is a plan in contract clothing — it makes the reviewer verify obedience
   instead of correctness and makes units brittle to legitimate implementation freedom.
   (Root-cause *pointers* in the kickoff prose remain welcome context; they are not
   clauses.)
5. **Rigidity is handled by amendment, not anticipation.** Do not fatten contracts to
   pre-answer every question — that is the "design the right spec upfront" fallacy #286
   exists to kill. Author the checkable core, leave the residual to the clarification
   channel, and let answers amend durably (§4.5).
6. **The firewall, restated.** No difficulty tags, tier names, yields, or any observed
   metric in a contract — it renders into kickoffs and PR threads, which are observed
   prompts. Same rule and reason as model-economy §5 and unit-types §6.3.

## 8. Decision summary and open questions

**Decided:**

1. One contract envelope (`agent-team.contract.v1`), four boundaries: dispatch contract
   (manager→worker, §4.1), fulfillment + evidence contracts (worker→reviewer, §4.2),
   advance contracts (step→step, §4.3 — generalize SQU-155 to advance-on-verified-output),
   stud contracts (unit→unit, §4.4 — vocabulary aligned with #223, no re-design).
2. Canonical home: `[contract]` block on the durable job record; compiled at dispatch
   from the ticket's acceptance criteria; rendered into the kickoff under `## Contract`;
   read by reviewer/verifier from the record, never the PR body.
3. Minimal schema: `work_item`, `deliverable`, `trailer`, `gates` tier, optional `scope`,
   ≤ 7 `criteria` with stable `AC` ids and optional `verify` hints, plus append-only
   `clarifications`. Nothing else in v1.
4. Verification: every clause has a named checker (§6.1); bounces cite clauses;
   `clause=none` findings are the contract-quality signal routed to harness-review.
5. Classification: breach = capability (escalation ladder applies); ambiguity =
   clarification via #286 with durable contract amendment, same tier, never escalate;
   incomplete contract = authoring fix, not worker fix.
6. Guardrails: no field without a checker; judgment clauses shrink with model capability,
   machine clauses persist; deliverable-only contracts are the sanctioned floor for
   low-stakes classes; contracts pin outcomes, never designs; metrics firewall absolute.

**Mechanism deltas required** (small, sequenced): `[contract]` parse/persist on the job
record + kickoff rendering; `job create --contract-file` / `--criterion`;
acceptance-criteria compilation for board-triggered dispatch + `contract_missing`
warning; `clause=` field accepted in findings files; step `needs`/`produces` verification
generalizing SQU-155; `clarification` message kind routing to the dispatching manager
(#286 v1).

**Open questions:**

- **Auto-run `verify` commands from criteria?** A criterion with a command hint could be
  executed by the verifier as a generated gate. Leaning yes-later: it blurs the
  deterministic-gates-are-declared-in-pipeline-TOML discipline, and a wrong criterion
  command red-flags good work. Start with reviewer-run, promote per evidence.
- **Ratchet `contract_missing` to hard-reject?** Once ≥ ~80% of pipeline dispatches carry
  criteria, flip the warning to a dispatch rejection (floors ratchet). Trigger threshold
  and who owns the flip (org-review evidence → reviewed topology change) to be set then.
- **Mechanize the trailer clause** in the daemon's deliverable check (regex on PR body at
  done-verification) and drop it from reviewer Phase 0 — cheap, but sequencing after the
  contract block lands so there is a clause to cite.
- **Structured reviewer verdict artifact.** The clause-keyed ledger/findings currently
  ride the PR comment + findings file; a `review-report.json` peer to verifier evidence
  (the SQU-36 "gates as data" direction) would complete the machine-readable loop. Decide
  when the approve gate or traceability actually needs to parse it.
- **Schema versioning and the unit merge.** When #223 v1 lands, confirm the unit
  `contract.outputs` evidence list and the job contract share the envelope (one `v2`
  bump) rather than drifting — the §4.4 alignment commitment needs a named owner at that
  point.
- **Contract in the A/B harness.** Same-work replays should hold contracts fixed across
  arms (a contract is part of the work definition, not the configuration under test) —
  confirm against `ab-experiment-harness.md` invariants before the first contract-era
  replay.

---

*The kickoff was never the problem; the silence about which sentences were binding was.
A contract is the smallest thing both sides can point at — and the gate can check —
when they ask whether the promise was kept.*
