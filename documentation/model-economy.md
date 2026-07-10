# Model Economy — allocating capability across the organization

**DESIGN DECISION** — 2026-07-09. Authored for the model-allocation policy referenced by the
constitution §VI.1 (#239). Ratification per the amendment process; open questions flagged §9.

The mechanism is topology-owned. `[model_policy]` supplies shared `runtime`, `model`, and
`effort` defaults; explicit instance declarations override it, and explicit pipeline-step
declarations override their resolved target instance. `model` is passed as `--model` on
Claude and Codex launches, and `effort` maps to each runtime's reasoning-effort setting.

## Active binding — 2026-07-10

Every non-Fable Kensho seat runs `runtime = "codex"`, `model = "gpt-5.6-sol"`, and
`effort = "high"`. This includes named and persistent seats, scheduled and replicated
instances, delivery/platform/release/docs pipelines, and dynamic dispatches.

Exactly three advisory seats are exceptions: `advisor`, `harness-reviewer`, and
`org-review`. They remain `runtime = "claude"`, `model = "claude-fable-5"`, and
`effort = "max"`. There is no compatibility alias, dual binding, or lower-model fallback.

The tier analysis below remains the economic framework for a future differentiated policy,
but its older per-role bindings are not the active runtime configuration. Until the operator
changes this decision, the topology binding above is authoritative.

---

## 1. The governing principle

> **Spend the capability premium where judgment-density is highest and the safety net is
> weakest. Spend the minimum where errors are cheap, detectable, and disposable.**

For any role, the right tier is a function of three variables:

- **J — judgment required.** How much of the role is open reasoning (ambiguity, tradeoffs,
  adversarial thinking) versus procedure (run the gates, follow the checklist)?
- **B — blast radius.** What does one error cost before something stops it? A bad worker
  commit dies in a worktree; a bad merge lands in `main`; a bad strategic call misdirects
  weeks of the whole delivery loop.
- **S — safety-net strength.** How reliably is this role's error *caught by something other
  than the role itself* — deterministic gates (gofmt/vet/test), the adversarial review gate,
  the manual approve gate, the sentinel, a human?

Tier requirement rises with `J × B` and falls with `S`. Two corollaries do most of the work:

**Corollary 1 — the gate arbitrage.** A cheap model behind a strong gate is economically
correct because the gate converts *unreliability* into a *bounded, priced retry*: a failed
attempt costs one worker run plus one gate cycle, never a defect in `main`. Kensho is built
for exactly this arbitrage — small jobs whose work is disposable ("scope every job so that
throwing its work away is a tolerable outcome," operating-model.md), worktree isolation,
verify → review → manual-approve in every pipeline. The gate is a fixed asset; once you have
paid for it, the marginal cost of model unreliability is retries, not defects. So buy
reliability from the gate, and buy only as much model as makes the retry rate cheap.

**Corollary 2 — judgment-per-token.** The premium you pay for a bigger model is
`price-premium × tokens consumed`. The value you get is `impact-per-decision × decisions`.
Roles differ enormously in judgment *density*: the advisor consumes almost no tokens and
every one of its outputs steers the organization; a worker consumes tens of millions of
tokens, most on mechanical edit-build-test loops. **Top-tier capability is cheapest exactly
where Kensho needs it most** — advisor, reviewer verdicts, merge decisions — because those
are token-light and judgment-dense. This is why "manager on a large model, implementers
smaller" is not a compromise; it is the optimum.

**Where the logic breaks.** The gate arbitrage fails, and a cheap model becomes false
economy, in three conditions — these define the floors in §3 and the guardrails in §7:

1. **The gate cannot see the error class.** Deterministic gates catch what they test;
   reviewers catch what one PR reveals. Architectural erosion across PRs, gameable tests,
   security subtleties, and anything above the gate (the manager's own merge judgment) are
   gate-blind. For gate-blind error classes, S ≈ 0 and the tier must carry the reliability.
2. **The retry loop costs more than the premium saved.** Every bounce round burns a full
   verify + review cycle plus manager attention and wall-clock. §6.5 derives the exact
   threshold; the short version is that cheap-first only pays when the cheap model's
   first-pass yield on that task class is high.
3. **The cheap model's failures are plausible instead of loud.** The worst cheap-model
   output is not the one that fails `go test` — it is the near-miss that costs the reviewer
   maximum effort to reject, or worse, gets approved. A model too weak for the task doesn't
   just fail more; it fails *quieter*.

## 2. Tiers

Policy is written in tiers; model IDs are bindings that rotate as the market moves. Current
bindings and list-price ratios (input/output per MTok):

| Tier | Meaning | Current binding | Price | Relative cost |
|---|---|---|---|---|
| **T0** | Frontier — hardest reasoning | `claude-fable-5` | $10 / $50 | 1.0× |
| **T1** | Large — strong judgment | `claude-opus-4-8` | $5 / $25 | ~0.5× |
| **T2** | Mid — capable implementer | `claude-sonnet-5` | $3 / $15 | ~0.3× |
| **T3** | Small — mechanical | `claude-haiku-4-5` | $1 / $5 | ~0.1× |

Notes: the codex runtime (subscription-auth, used by `docs-writer`) sits outside this price
ladder and remains attractive for its niche while subscription economics hold; treat it as a
T2-equivalent binding. Thresholds in §6.5 are derived from *ratios*, not dollars — re-derive
when the ratios move materially, not on every price change. T3's 200K context window is a
real constraint: any role that must hold large diffs or long histories cannot bind below T2
regardless of judgment requirements.

## 3. Recommended allocation

| Unit | Tier | J | B | S | Reasoning |
|---|---|---|---|---|---|
| **advisor** (steward) | **T0** | max | max | none | Already pinned. Token-light, judgment-maximal: strategy, architecture, constitutional stewardship. Nothing above it but Kan/James. The cheapest place to buy the most intelligence — never economize here. |
| **manager (Kai)** | **T1** | high | high | weak | Above the gate: its dispatch, bounce-vs-merge, and merge calls are checked only post-hoc (sentinel, Kan). Wrong calls cascade across the whole loop. T1 not T0 because most manager actions are procedural (dispatch mechanics, reading gate reports as data) and its genuinely hard calls escalate to the T0 advisor by design — the escalation channel is what makes T1 sufficient. |
| **reviewer** | **T1 floor** | high | high | partial | THE gate. A miss is an escaped defect in `main`. Invariant: **reviewer tier ≥ worker tier, ideally one above** — the review economy's value is the worker↔reviewer tension, and a reviewer weaker than the worker it judges is theater. Affordable because review is token-light relative to implementation (10M step budget vs the worker's 40M): a T1 reviewer over a T2 worker costs far less than a T1 worker, and buys more. |
| **worker / platform-worker** | **T2 default**, T1/T3 by routing (§5) and escalation (§6) | med | low | strong | The textbook gate-arbitrage position: one well-specified ticket, isolated worktree, behind verify + adversarial review + manual approve. Blast radius = one discarded attempt. This is where the bulk of tokens burn, so this is where the economy is won. |
| **verifier** | **T3** | ≈0 | low | total | Runs scripted gates (`gofmt`, `go vet`, `go test`), streams progress, writes evidence JSON. Zero open judgment; its output is machine-checkable by construction. End state: no LLM at all — a deterministic daemon step (§9). |
| **release-worker** | **T2** | med | high | strong | Ships irreversible public artifacts, but the release skill proceduralizes it and the pipeline's manual approve gate holds the actual judgment. The gate carries it; the approve step (manager, T1) is where the tier lives. |
| **ticket-manager** | **T3** | low | low | strong | Routing and filing against `config.toml`. Errors are visible and trivially corrected. |
| **comms** | **T2** | med | med | partial | Public voice; a bad digest is embarrassing, not corrupting. Writing quality argues against T3. |
| **org-review** | **T0** | max | high | weak | Already pinned to Fable. Reads the outcomes ledger and proposes changes to prompts, budgets, topology — the self-improvement steering wheel. Errors here misdirect the whole system and nothing downstream checks them but Kan. Runs every ~3 days for ~15M tokens: premium is trivial, leverage is total. |
| **harness-review** | **T1** | high | med | partial | Judges steering surfaces (prompts, instructions) from bounce/feedback evidence. Output is tickets, gated by normal review — but the analysis quality is the product. |
| **feedback-triage** | **T2** | med | low | strong | Clustering and filing. Judgment-light; a mis-filed ticket costs a groom cycle. |
| **debt-auditor** | **T2** | med | low | strong | Needs real code-reading judgment to find genuine debt (a T3 auditor files noise, which costs manager attention), but its output is capped tickets, fully gated. |
| **sentinel** | **T3** | low | low | strong | Checks CI/RTD/release assets against expected states — mechanical comparison, incident feedback on mismatch. |
| **product-verify** | **T3** | low | low | strong | UI-vs-CLI ground-truth comparison. Mechanical by design. |
| **docs-writer / docs-freshness** | **T2** (codex binding) | med | low | strong | Narrative quality matters (not T3); errors are stale prose behind review (not T1). Current codex-subscription binding is the cheap seat while it lasts. |

Three structural observations the table encodes:

- **The premium concentrates off the hot path.** T0/T1 sits on advisor, manager, reviewer,
  and the two judgment loops — together a small fraction of total tokens. The token-heavy
  hot path (workers) runs at T2 and below. Estimated blended effect: most spend at ~0.3×
  frontier price, with total spend dominated by exactly the roles whose errors are caught
  free by gates.
- **Floors are about gate-blindness, not difficulty.** Reviewer and manager floors exist
  because their error classes are invisible to the machinery below them, not because their
  tasks are always hard.
- **Budgets must become cost-aware.** `token_budget` values in `instances.toml` (worker 80M,
  reviewer 30M, verifier 5M…) are model-agnostic counts; 40M tokens on T0 costs ~10× the
  same budget on T3. Once tiers diverge, `[budgets.*]` should be denominated in (or weighted
  by) dollars, or a T3→T0 escalation silently 10×es a team's real spend inside an unchanged
  token cap (§9).

## 4. The joint optimization: model × decomposition × gate strength

Tier is not chosen in isolation. Three levers buy the same good — merged, correct PRs — and
substitute for each other:

```
first-pass yield  ≈  f( model tier , spec quality × slice size , gate strength )
```

- **Decomposition granularity.** A smaller, better-specified slice needs less model. The
  field report already proved the extreme: "a ticket with root-cause pointers and acceptance
  criteria turns a one-shot worker into a reliable implementer; a vague ticket produces a
  plausible-looking PR that fails review" (operating-model.md). Decomposition is performed
  by T1 (Kai grooming) — so finer decomposition is literally *moving intelligence upstream*:
  the manager's large model does the abstraction-splitting once, so a small model can execute
  each piece. This is the constitution's "widen capability by decomposing the model soundly"
  (§V.3) restated as economics.
- **Gate strength.** Stronger deterministic gates (ratcheting floors, better
  `infra_signatures`, tighter reviewer checklists) raise the detectable-error fraction,
  which lowers the tier a worker needs — per Corollary 1.
- **Model tier.** The lever of last resort, because it is the only one you pay for on
  *every* attempt, forever. Ticket quality and gate strength are paid for once and reused.

**Tradeoff rule:** when a task class underperforms, spend the levers in this order —
(1) improve the ticket template / split the slice, (2) strengthen the gate or checklist,
(3) raise the tier. Reach for (3) first only under §6's escalation policy, where a specific
job is already failing and re-dispatch is the cheapest immediate fix. And respect the
overhead floor: each slice pays fixed costs (dispatch, worktree, verify, review, merge
≈ one gate cycle), so slicing below ~a gate-cycle's worth of work makes overhead dominate —
decomposition has an optimum, not a monotone benefit.

Conversely, a **bigger implementer model buys back coarseness**: an ambiguous, exploratory,
or cross-cutting ticket that resists clean slicing is a legitimate T1-worker job (§5 routes
it there). Do not pretend every job can be made small; price the ones that can't.

## 5. Per-ticket difficulty routing (a priori)

The worker tier is chosen **per job at dispatch**, not per instance forever. Kai applies a
small, deterministic rubric when dispatching; the score and chosen tier are recorded on the
job (outcomes ledger) so the router itself can be audited.

**Signals and scoring** (start dumb and transparent; org-review tunes weights from evidence):

| Signal | Toward T3 | Toward T2 (default) | Toward T1 |
|---|---|---|---|
| Spec quality | Exact file+line pointers, mechanical change | Clear acceptance criteria | Ambiguous intent, design freedom |
| Scope estimate | 1 file, ≤ ~50 lines | Few files, one package | Cross-package, new abstraction |
| Subsystem risk | Docs, scripts, template prose | CLI, loader, skills | Daemon core, reconcile, dispatch path |
| Class history | First-pass yield ≥ 90% at lower tier | — | Class bounce/escalation rate high (§6.6) |
| Security surface | — | — | **Hard floor T1**: touches `authority`, `env_allow`, secrets, release, merge machinery |
| Operator override | `route:small` label | — | `route:large` label |

Highest triggered column wins; security-surface and daemon-core floors are non-negotiable
regardless of how mechanical the ticket looks (gate-blind error classes, §1). The reviewer
tier follows automatically from the invariant: reviewer ≥ worker tier (T1-reviewed for
T1-workers; the standing T1 reviewer covers T2/T3 workers).

**Firewall (mandatory):** the difficulty tag exists for routing and metrics cohorting
(metrics-methodology.md difficulty classes — one tag, dual use). It is **never** written
into the worker's or reviewer's prompt as a quality signal ("this is an easy ticket") —
an agent told its work is scored easy sandbags, and a reviewer told the ticket is easy
under-reviews. The kickoff carries the ticket, not the score.

**Mechanism:** tier-per-job can be target-per-job or step metadata. Declare tiered
instances of the same agent — `worker-lite` (T3), `worker` (T2 default),
`worker-heavy` (T1) — and route by dispatch target when budget/replica pools differ.
Use step-level `model` and `effort` when one pipeline stage needs a runtime tier without
inventing another instance.

## 6. Dynamic escalation: the bounce-driven control loop

Static allocation is a prior. The posterior comes from the system's richest existing signal:
**the bounce.** A review gate that keeps rejecting a worker's output is direct evidence about
that worker-model-on-that-task — and Kai, who reads every bounce and owns every re-dispatch
(`max_attempts = 1`; the manager re-dispatches, by design), is already positioned as the
controller. The economically optimal strategy is **start at the routed tier, escalate on
demonstrated failure**: pay for capability only after the cheap model has proven insufficient
on this specific job — with the two corrections below, because "bounce" is not one signal.

### 6.1 The loop

```
dispatch (tier from §5 routing)
      │
      ▼
verify ──red──► infra-red? ──yes──► re-run / fix env   (never a tier signal)
      │
      ▼
review ──BOUNCE──► Kai classifies the bounce (§6.2)
      │                   │
   APPROVE          ┌─────┼──────────────┬───────────────┐
      │             ▼     ▼              ▼               ▼
    merge      capability spec-ambiguity scope        infra/flake
      │        (§6.3:     (clarify or    (split the   (re-run same
      ▼         escalate)  rewrite the   ticket —      tier)
  record outcome           ticket —      never
  (ledger: tier,           never         escalate)
  bounces, class)          escalate)
```

Every terminal outcome — merged or abandoned — records `(task class, tier dispatched,
bounce count, bounce classes, escalated?, final tier)` in the outcomes ledger. That record
drives §6.4 and §6.6.

### 6.2 Classify before escalating — most bounces don't earn a bigger model

Escalating the model is the correct response to exactly one bounce class. Misclassifying is
the main false-economy risk in this whole design:

| Bounce class | Evidence shape | Correct response | Why escalation is wrong |
|---|---|---|---|
| **Capability** | Logic error, missed edge case, misapplied pattern, shallow tests, "didn't understand the codebase" | Escalate per §6.3 | — (this is the one) |
| **Spec ambiguity** | Worker built a plausible thing that isn't what was meant; findings argue *intent*, not *correctness* | Clarify: answer the question or rewrite the ticket, re-dispatch same tier | A bigger model guesses intent more fluently — it does not *know* intent. The constitution's two-way channel (§II.2) is the fix: asking is the mechanism, at any tier. Paying 2× to guess better is pure waste. |
| **Scope** | PR sprawls, drive-by edits, reviewer findings span concerns | Split the ticket (§4: decomposition first) | A bigger model completes an oversized slice more often — and normalizes oversized slices, eroding the small-jobs safety unit. |
| **Infra / flake** | Matches `infra_signatures`; base drift; env failure | Re-run; fix signatures/env | "DIRTY from base drift is NOT a content bounce" — already doctrine. Escalating on infra noise trains the router on garbage. |

Reviewer instructions already force structured findings (gates as data, content judged
separately), so Kai classifies from the findings file it already receives via
`job bounce --findings-file`. Classification is recorded with the bounce.

### 6.3 Escalation policy (capability bounces only)

- **Bounce 1, findings mechanical and localized** (named function, named missing test,
  bounded fix): re-dispatch **same tier** with findings in kickoff — the existing
  `agent-team job bounce <id> --findings-file <path> --advance` path. Findings-guided
  retry is the cheapest capability upgrade there is.
- **Bounce 1, findings show comprehension failure** (wrong approach, misread architecture,
  systemic test gaps): **escalate one tier immediately.** A model that misunderstood the
  task does not fix its understanding by being told it was wrong; a same-tier retry is a
  coin-flip you pay full gate-cost to flip.
- **Bounce 2 at the same tier, any capability findings: escalate one tier.** Two rejections
  is the empirical bound — §6.5 shows a third cheap attempt is almost never economic.
- **Bounce at T1** (top implementer tier): do not reach for T0 — Fable is not an implementer
  in this economy. A T1-capability bounce means the ticket, not the model, is the problem:
  back to Kai for re-scoping, or escalate the *question* to the advisor. Hard stop at 3
  total attempts per ticket regardless of tier (existing re-dispatch discipline).
- Escalated re-dispatch is a **new job targeting the heavier instance** (`worker-heavy`),
  carrying prior findings in kickoff. Mechanism note: `job bounce --advance` re-runs the
  same step on the same instance today; escalation needs either a `--target` on bounce or
  manager-driven fresh dispatch — small mechanism delta, §9.

### 6.4 De-escalation — the loop must run both ways

Escalation-only policies ratchet costs upward. The reverse signal is first-pass yield:

- If a task class sustains **first-pass-merge ≥ 90% over a trailing window (≥ 20 jobs)** at
  its current default tier, trial the next tier down on a **canary share (~20%)** of that
  class's jobs, chosen blind (workers never know they are canaries — firewall).
- Promote the lower tier to class default only if canary first-pass yield stays above the
  §6.5 breakeven for that tier pair; revert on the first sustained regression.
- **Asymmetric by design: escalate fast, de-escalate slow.** An under-tiered class announces
  itself immediately (bounces are loud, bounded, and priced); an over-tiered class is a
  silent tax — but a *wrongly de-escalated* class pays the tax in gate cycles and escaped-
  defect risk. Hysteresis prevents oscillation.

### 6.5 The cost accounting — where "cheap first" stops being cheap

The true cost of a cheap attempt includes the gate cycle it drags behind it. Define, per
attempt on a given task:

- `W_t` — worker attempt cost at tier t (tokens × tier price)
- `G` — per-round gate overhead: verify (T3, small) + review (T1, ~10M budget) + Kai's
  attention + wall-clock. **G is paid every round and does not shrink with worker tier.**

With Kensho's shapes (implement ~40M-token budget, review ~10M at T1 prices), `G` is
material — order of *half* of a T1 worker attempt. Using price ratios from §2:
`W_T2 ≈ 0.6·W_T1`, `W_T3 ≈ 0.2·W_T1`.

Compare **cheap-first-then-escalate** against **start big**, one escalation step, letting
`p` = cheap tier's first-pass probability on this class:

```
E[cheap-first] = (W_cheap + G) + (1 − p)(W_big + G)
E[big-first]   = W_big + G

cheap-first wins  ⇔  p > (W_cheap + G) / (W_big + G)
```

Substituting `G ≈ 0.5·W_T1`:

- **T2-first vs T1-first:** breakeven `p ≈ (0.6 + 0.5)/(1 + 0.5) ≈ 0.73`. A T2 default is
  only economic on classes where Sonnet-tier first-pass yield exceeds **~70%**. Below that,
  start at T1 — "cheap first always" is a slogan, not a policy.
- **T3-first vs T1-first:** breakeven `p ≈ (0.2 + 0.5)/1.5 ≈ 0.47`. T3 tolerates much lower
  yield (~50%) because the attempt is nearly free relative to the gate — *but* T3 failures
  must still be the loud kind; §7's plausible-failure and correlated-blindness caveats bind
  hardest here.
- The same algebra sets the **retry bound of §6.3**: a second same-tier attempt costs
  another full `(W_cheap + G)` against an unchanged escalation option — it only pays if the
  findings themselves materially raise `p` for the retry (the mechanical-findings case).
  A third attempt needs `p` gains no real findings deliver. Hence: retry once on mechanical
  findings, otherwise escalate.

Two costs the formula understates, both pushing toward earlier escalation: **latency**
(each round is ~1–2h of pipeline wall-clock; queue positions and manager attention are
scarce under the concurrency ceiling) and **escaped-defect risk** (each extra round is
another chance for a tired-reviewer miss; review quality is not i.i.d. across rounds of the
same PR). When in doubt between one more cheap round and escalating: escalate.

These thresholds are derived from current price *ratios* and current gate shapes. They are
inputs to org-review, not constants — recompute when `[budgets]`, prices, or review budgets
change materially.

### 6.6 Closing the loop into the static policy

The dynamic signal trains the static allocation — this is what makes model allocation a
self-correcting control system rather than a guess:

- The outcomes ledger (SQU-135 substrate) accumulates per-class records from §6.1.
- **org-review** (T0, every ~3 days) reads: escalation rate per class, first-pass yield per
  (class, tier), bounce classes, cost per merged PR per class. Decision rules:
  - class escalation rate > ~25% over a window → **raise the class's default tier** (the
    router was under-pricing the class);
  - class capability-bounce findings cluster on a named weakness → prefer the cheaper
    levers first: ticket-template or checklist improvement ticket (§4 ordering);
  - sustained ≥ 90% first-pass at current tier → **open a de-escalation canary** (§6.4).
- Default-tier changes are **reviewed changes to `instances.toml` / the routing rubric**,
  landing through the normal ticket → worker → gate → merge pipeline (constitution §V.1).
  Kai applies the policy in the moment; only the pipeline changes the policy.
- **Firewall, restated because it is load-bearing** (metrics-methodology.md): every metric
  above lives at org-review and the ledger. None of it — yields, escalation rates, per-model
  bounce rates — ever appears in the prompt of a worker, reviewer, or verifier. The observed
  optimize their instructions; only the observer moves the tiers.

**Division of authority:** *in-the-moment* tier decisions (routing at dispatch,
classification, escalation on bounce) belong to **Kai** — it sees the bounce, owns
re-dispatch, and the decision is reversible (worst case: one wasted attempt). *Durable*
defaults (class tiers, routing rubric weights, the tier bindings in §2) live in **topology
and this document**, changed only by reviewed PR on org-review's evidence. This mirrors the
two-plane model: escalation is daemon-plane-adjacent execution; re-tiering is
mailbox-plane judgment.

## 7. Failure modes and guardrails

| # | Failure mode | What it looks like | Guardrail |
|---|---|---|---|
| 1 | **Gate blind spots** | Cheap workers pass verify+review while eroding architecture across PRs; tests that pass but assert nothing | Tier floors on gate-blind surfaces (§5: daemon core, authority, release at T1). Reviewer checklist already demands "tests would fail without the change" — keep sharpening it. debt-auditor and harness-review exist precisely to see across PRs; their findings feed §6.6. |
| 2 | **Review-cost inflation** | Cheap-model near-misses maximize reviewer burn: plausible PRs that take a T1 reviewer full effort to reject. Total cost quietly exceeds big-model-first | Telemetry: reviewer tokens-per-PR **by worker tier**, bounce rounds per class. Rule of thumb: when a class's expected total cost at the cheap tier (incl. expected bounce rounds, §6.5) exceeds ~0.9× big-tier cost, stop being clever — raise the default. |
| 3 | **Escalating the wrong bounce** | Spec-ambiguity or infra bounces trigger tier escalation; costs 2× for zero yield gain and hides the real defect (bad ticket, bad signature) | §6.2 classification is mandatory before escalation; infra-red is signature-classified upstream and never reaches the tier decision. Track "escalations that still bounced" — a high rate means misclassification, not weak models. |
| 4 | **Correlated blindness** | Worker and reviewer share failure modes (same family, similar tier) and the adversarial tension collapses — approvals that two different minds would have caught | Reviewer ≥ one tier above worker (§3 invariant), and/or **cross-runtime diversity**: Kensho already runs claude + codex; a codex-reviewed claude PR (or vice versa) buys decorrelation for free. Mutation/canary testing (metrics-methodology.md) is the only true measure of reviewer catch-rate — run it per (worker-tier, reviewer) pair. |
| 5 | **Manager under-tiering** | Kai on a small model makes subtly bad dispatch/merge/classification calls — the one place no gate catches errors | Manager floor T1 is non-negotiable; hard calls escalate to T0 advisor. Sentinel + Kan review are the post-hoc net; they detect, not prevent. |
| 6 | **Goodharting the router** | Difficulty tags or per-model scores leak into observed prompts; workers sandbag easy tickets, reviewers relax on "easy" PRs; canaries behave when watched | The firewall (§5, §6.6): tags route and cohort, never appear in kickoffs. De-escalation canaries are blind. This is metrics-methodology doctrine applied to tiering. |
| 7 | **Ratchet-only drift** | Escalations accumulate, de-escalations never happen, blended cost creeps to T1-everywhere | §6.4 de-escalation canaries are a standing org-review duty, reviewed on the same cadence as spend. The asymmetry is deliberate but bounded. |
| 8 | **Token budgets masking cost** | A T3→T1 escalation fits the same `token_budget`, so `[budgets.*]` caps never fire while real spend 10×es | Cost-aware budget denomination (§9). Until then: org-review reads spend in dollars, not tokens. |

**Standing telemetry** (all ledger-side, observer-only): first-pass yield per (class, tier);
bounce rate and bounce-class mix per model; escalation rate and post-escalation yield;
reviewer cost per PR by worker tier; escaped-defect rate per (worker tier, reviewer tier)
pair; cost per merged PR per difficulty class, trended within-cohort per
metrics-methodology.md (absolute thresholds lie; longitudinal same-cohort comparison is the
honest form).

## 8. Decision summary

1. **Principle**: capability premium ∝ judgment-density × blast radius ÷ safety-net
   strength; cheap models belong behind strong gates; premium concentrates where tokens are
   few and judgment is dense.
2. **Static allocation** (§3): advisor + org-review T0; manager, reviewer, harness-review
   T1; workers T2-default with routed exceptions; verifier, sentinel, product-verify,
   ticket-manager T3. Reviewer ≥ worker, always.
3. **Routing** (§5): per-ticket tier by deterministic rubric at dispatch; security/daemon
   surfaces floor at T1; tags never enter observed prompts.
4. **Escalation** (§6): start at routed tier; classify every bounce; escalate one tier on
   comprehension-failure or second capability bounce; never escalate on ambiguity, scope, or
   infra; de-escalate via blind canaries on sustained yield; breakeven ~0.7 first-pass yield
   for T2-vs-T1, ~0.5 for T3-vs-T1.
5. **Learning** (§6.6): outcomes ledger → org-review → reviewed changes to defaults. Kai
   decides in the moment; the pipeline changes the policy.

## 9. Open questions

- **Step-level model routing policy.** Pipeline steps can now declare `model` and `effort`,
  but tiered instances still give per-tier budgets/replicas for free. Decide when the policy
  should prefer step metadata versus explicit `worker-lite`/`worker-heavy` targets.
- **Escalation mechanism.** `job bounce --advance` re-runs the same instance; tier
  escalation needs `job bounce --target <instance>` or a manager-driven fresh dispatch
  carrying findings. Small delta; needed before §6.3 is fully executable.
- **Verifier end state.** Should the verifier be an LLM at all? The gates are scripted; a
  deterministic daemon step (no model) is cheaper and strictly more trustworthy. Proposed
  direction: yes, de-LLM it; the T3 binding is transitional.
- **Cost-denominated budgets.** Extend `[budgets.*]` (and per-instance budgets) with
  price-weighted accounting so caps track dollars, not tokens, across tiers.
- **Where the routing rubric lives.** In Kai's prompt/skill (fast iteration, weak audit) vs
  a `routing` block in `instances.toml` (declarative, reviewed, daemon-enforceable). Leaning
  topology, consistent with everything else declarative.
- **Cross-runtime reviewer diversity** (§7.4): make codex-reviews-claude a deliberate
  policy for T3-worker classes, or leave to org-review evidence?
- **Canary parameters**: 20% share, 20-job windows, 90%/25% thresholds are priors, not
  measurements. org-review owns tuning them — by evidence, like everything else here.

---

*Policy applies from the first metered token. Until then, run the telemetry anyway: the
unlimited period is free training data for the router.*
