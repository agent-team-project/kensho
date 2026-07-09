# Unit Types — a learned library of team configurations, indexed by work-class

**DESIGN DECISION** — 2026-07-09. Answers James's question on #200/#223: *"will our
meta-analysis lead to unit types — e.g. studying excel-lite and other applications and
Kensho itself?"* Yes — and this document decides how. Companion to
[`composable-units.md`](./composable-units.md) (the unit *mechanics*: spec, contract studs,
composition, admission), [`templates.md`](./templates.md) (the authoring/distribution
substrate), [`model-economy.md`](./model-economy.md) (tier policy the types parameterize),
[`metrics-methodology.md`](./metrics-methodology.md) (the firewall), and
[`ab-experiment-harness.md`](./ab-experiment-harness.md) (the validation instrument).
Ratification per the amendment process; open questions flagged in §8.

---

## 1. What a unit type is

> **A unit type is a versioned, parameterized team-configuration bundle, bound to a
> work-class it demonstrably fits, carrying the evidence that it fits.**

Three clauses, three load-bearing properties:

1. **Configuration bundle** — everything that determined a deployment's performance in the
   chess and excel-lite studies: which roles/instances exist and at what replica counts,
   which model tier each role binds (per model-economy §3), the pipeline shape and its
   gates (deterministic verifier → adversarial review → visual-QA where applicable → manual
   approve), the decomposition discipline (slice-size policy in the *manager's steering
   surfaces* — the chess retro proved slicing is behavioral, not topological, so a unit
   type must bundle prompts and step instructions, not just `instances.toml` numbers),
   budgets, and which self-examining loops run.
2. **Work-class binding** — the thing that makes it a *type* rather than a one-off
   template. A template answers "what files do I render?"; a type additionally answers
   "*for which class of work is this the right configuration, and how do I recognize that
   class?*" It declares the classification signals (§2) that route a new project to it.
3. **Evidence** — provenance. Which projects validated it, at what sample size, under
   which harness runs, with which model-tier bindings. A type without evidence is a
   hypothesis wearing a type's clothes; the schema forces the distinction (§1.2 maturity).

### 1.1 Relationship to existing designs — one concept, not three

- **Templates-as-images is the substrate, not a competitor.** A unit type *is* a template
  in the `templates.md` sense — a versioned, parameterized directory tree with a
  `template.toml` manifest, distributed by ref, instantiated by `init`, pinned by
  `.template.lock`, upgraded conservatively. Unit types add two manifest sections
  (`[work_class]`, `[evidence]`) and a curation lifecycle. No new loader, no new renderer,
  no new distribution channel.
- **#223's composable units are the target grain; whole-team templates are today's grain.**
  `composable-units.md` defines the unit spec, contract studs, and the
  agent → unit → composition → fleet layering, sequenced behind dynamic teams (#155).
  Until #223 v2 (charter-from-unit-type) lands, a unit type *materializes* as a whole-repo
  template — that is what `init` can render and what a child daemon runs today. The
  `[work_class]`/`[evidence]` annotations are grain-agnostic by design: when sub-team units
  exist, they attach unchanged to unit specs, and a whole-team type becomes a *composition
  manifest* of units. **Decision: one catalog, two grains over time — do not fork the
  concept into "templates we learned" and "units we learned."** This document is the
  epistemology and lifecycle of #223's v4 "typed catalog"; #223 remains the mechanics.
- **#200 is the loop that produces them.** The meta-learning workstream's concrete output
  artifact ("evolve the bootstrap/team-template from outcomes") *is* the unit-type library.
  The chess retro's recommended slim-verifier-first topology was the first unit-type
  candidate, exactly as #223 predicted: "a recommended starter topology from a retro is a
  unit-type candidate."

### 1.2 Schema

A unit type is a template tree whose manifest carries two new sections:

```toml
[template]                       # unchanged — a unit type IS a template
name        = "objective-oracle-build"
version     = "0.3.0"
description = "Systems build with a machine-checkable ground truth (conformance corpus)."

[work_class]
class   = "objective-oracle"
# Declarative routing hints, evaluated by the intake classifier (§2.3):
signals = [
  "deterministic conformance oracle exists or is buildable in < 1 day",
  "correctness dominates UX in the acceptance criteria",
  "work decomposes into slices with machine-checkable per-slice gates",
]
anti_signals = [
  "acceptance requires visual/pixel judgment",   # → gui-application
  "single hot file serializes most changes",     # → hot-file-serial
]

[evidence]
maturity       = "candidate"     # seed | candidate | validated  (§6.1)
projects       = ["chess-465d4bed", "excel-lite-core"]
harness_runs   = ["ab-2026-07-xx-chess-replay"]   # ab_harness.py summary artifacts
tier_bindings  = "model-economy@2026-07-09"        # §6.7 evidence decay
last_validated = "2026-07-08"

[[parameter]]                    # unchanged mechanics; the *defaults* are what's learned
key     = "workers.replicas"
type    = "int"
default = 3                      # chess: 6 workers, effective concurrency 1.00 — 3 is evidence-backed
# ... model tiers per role, gate timeouts, budget defaults, slice-size ceiling, etc.
```

The tree body carries the learned configuration: `instances.toml` (roles, replicas,
`runtime`/`model` per role), pipeline TOML (step order, gates, `infra_signatures`,
watchdog timeouts, auto-merge policy), agent prompts including the **manager's slicing and
dispatch checklist**, reviewer checklists, verifier gate scripts, and — for the
objective-oracle class — conformance-corpus scaffolding as the mandated first slice.

**What is deliberately NOT in the schema:** any performance statistic visible to the
instantiated agents. `[evidence]` is catalog metadata read by the observer (org-review,
Kan, James), rendered into nothing (§6.3 firewall).

---

## 2. The work-class taxonomy

A work-class is defined by **which levers dominate its economics** — gate type,
serialization constraints, decomposition grain, tier floors — never by domain. Chess and
excel-lite's core are the same class despite being different products, because the same
configuration is optimal for both: that predicate ("the same config would win for both")
is what a class *is*, and it is falsifiable by the transfer test (§5.3).

### 2.1 The initial taxonomy (v1 — seeded from real evidence)

| Class | Exemplars | Dominant levers → configuration signature |
|---|---|---|
| **objective-oracle build** | chess engine (465d4bed), excel-lite compute core | A deterministic oracle exists (perft counts, spreadsheet conformance corpus, protocol suites). Config: **verifier-first** — machine verifier step before LLM review, running the corpus with progress output; corpus seeded as the *first* slice (chess lesson: 2 tactical positions vs 300 required — never start strength work before the corpus exists); fine-grained slices dispatched in parallel (the #1 chess lever: 41 tickets collapsed into 6 monster jobs → effective concurrency 1.00 against capacity 6); T2/T3 workers behind the strong gate (gate arbitrage, model-economy §1); policy-gated **auto-merge** for verifier-green low-risk slices; adversarial review reserved for core-legality/correctness slices; manual gate only at release claims. |
| **GUI application** | excel-lite's Tauri surface, chess GUI | Acceptance is partly non-mechanical. Config: everything above *plus* a **visual-QA gate** (`template/skills/visual-qa`, #254) and product-verify loop; no auto-merge on UI-facing slices; T2 workers, reviewer checklist includes screenshot evidence. The chess GUI shipped with zero visual QA — a named gap in the retro. |
| **daemon-critical / hot-file serial** | Kensho's own `internal/daemon` (reconcile, dispatch, authority) | Gate-blind error classes and shared hot files. Config: **serial dispatch locks** on the hot surface (SQU-35 doctrine), T1 worker floor (model-economy §5 non-negotiable floors), mandatory adversarial review (constitution §III.6: "especially for changes to the daemon that runs everything"), no auto-merge ever, `max_attempts = 1`. Throughput is bought by *going wide across disjoint surfaces*, never by parallelizing the hot file. |
| **parallel-library fan-out** | disjoint-package waves in Kensho itself (the SQU-42 three-stream run) | Many independent slices across disjoint surfaces. Config: replicas up to the build-slot ceiling (capacity is build resources, not agent count — operating-model.md), T2 default workers, standard verify→review, board-gated dispatch. |
| **docs / config / prose** | docs-writer runs, template prose | Cheap, loud failures. Config: T2 codex-subscription seat while the economics hold (model-economy §3), linter/link gates, light review, generous batching. |
| **research-spike / study** | the chess retro itself, the excel-lite autonomy study | Deliverable is a report, not merged code. Config: single capable agent (T1/T0), no worker fan-out, quality floor = evidence citations and reproducibility, maps to #223's `research-spike` unit. |

Kensho's own standing teams (`delivery`, `platform`, `quality` in `instances.toml`) are
recognizable in this table: they are unit types *avant la lettre*, tuned by the SQU-42
field report. §3.3 makes that explicit.

### 2.2 Classification signals

Five signals, collected at project intake, classify a new project:

1. **Oracle availability** — can a deterministic conformance gate be written in under a
   day? (Yes → objective-oracle; the single strongest signal, because it activates the
   gate arbitrage and everything downstream of it.)
2. **Acceptance surface** — does "done" require pixels/human aesthetics? (→ visual-QA
   gate; GUI class or a GUI overlay on another class.)
3. **Coupling topology** — dependency-graph density; is there a hot file/subsystem that
   serializes changes? (→ serial locks + tier floors vs fan-out.)
4. **Blast radius / reversibility** — do defects escape into something that runs
   unattended or ships publicly? (→ review mandatory, no auto-merge, T1 floors.)
5. **Deliverable kind** — merged code vs prose vs report. (→ gate stack and quality floor.)

A project can be a **composition** of classes (excel-lite = objective-oracle core +
GUI-application shell); the classification names a *primary* type per major surface, which
is exactly the #223 composition story arriving early in configuration form.

### 2.3 Who classifies, and where it's recorded

**Decision:** classification is a checklist run by Kan/Kai at project charter time (an
intake step, before `init`), escalating to the T0 advisor when signals conflict. It is
deliberately *manual with a written rubric* first — exactly like the model-economy §5
routing rubric — and automated only after misclassification data exists. The chosen
`class`, the per-signal answers, and the instantiated `type@version` are recorded in the
child's `config.toml` (`[work_class]` block, rendered by the template itself) and in the
parent's project record, so every retro can audit the classification as its first check
(§3.2): *was this project what we thought it was?*

---

## 3. The discovery + synthesis loop

The chess retro and the excel-lite study are the prototype run **by hand, once**. The loop
makes them a standing organ. It has three stages — instrument, retro, synthesize — and it
runs at **two levels** with the same machinery.

### 3.1 Instrument (in the child, during the run)

Nothing new to build: the outcomes ledger (`.agent_team/outcomes/jobs/*.json`) already
records per-job `tokens_consumed`, `review_rounds`, `bounce_count`, budget events,
`work_units` with start/finish timestamps (→ effective concurrency), and terminal status.
Two additions the chess reconstruction had to do forensically, promoted to first-class:

- **Bounce-class labels** on the ledger record (capability / spec-ambiguity / scope /
  infra, per model-economy §6.2) — Kai already classifies every bounce; record it.
- **Config-drift events** — when a project diverges from its instantiated type mid-run
  (the forced team-evolution protocol from the chess-derived bootstrap spec, #200), the
  divergence is logged as an event. Divergence is not failure; it is the *richest* signal
  a retro can carry (§5.3).

### 3.2 Retro (in the child, at project completion — a pipeline step, not a favor)

**Decision: the retro is a mandatory terminal pipeline step in the bootstrap template**,
not an operator-initiated study. The chess bootstrap spec demanded exactly this ("does not
force team meta-evolution"); org-review's own SKILL.md already says it "should also run
when a project or epic completes." The step produces a typed artifact — `retro.json` +
`retro.md` — with a fixed schema derived from what the chess `analysis.md` actually
contained:

| Section | Content (chess exemplar) |
|---|---|
| **Classification audit** | Was the intake class right? What signals were misread? |
| **Outcome aggregates** | Per-slice ledger rollups: tokens/difficulty point, bounce rounds/difficulty point, effective concurrency (chess: 1.00 vs capacity 6), wall-clock attribution (bounce rework 38%, manager-in-loop 16%, approval latency ~8m). |
| **Gate value** | What each gate caught, with evidence (adversarial review found en-passant + UCI-stop bugs beyond the canonical corpus; zero escaped product defects). A gate that caught nothing is a candidate for demotion; a defect class no gate saw is a candidate gate. |
| **Dead capacity** | Provisioned-but-unused roles/replicas (chess: 8 of 9 execution lanes idle the entire run). |
| **Improvised structure** | Roles/gates/protocols the team invented mid-run because the type lacked them (chess improvised requirements-auditing → specialist-role proposals). |
| **Config drift log** | §3.1 divergence events and whether each helped. |
| **Testable hypotheses** | Ranked, ≤ 7, each phrased as an A/B arm (the chess retro's seven are the canonical form). |

The retro author is the child's manager with a dedicated `retro` skill (a specialist
retro agent is a later refinement — open question §8). The step is cheap insurance: one
T1-agent run against evidence that already exists on disk.

### 3.3 Synthesize (in the parent — org-review's job, extended, not a new organ)

**Decision: no new meta-daemon, no "meta-Kensho." Cross-project synthesis is a new
evidence source in org-review's existing charter.** org-review already runs on T0 every
~3 days, already reads the ledger/feedback/capacity, already files ≤ 3 evidence-backed
tickets under the metrics firewall, and is already reflexive. It gains one section:

> **8. Cross-project retros** — read `.agent_team/retros/<project>/retro.json` for any
> retros landed since the last run. For each: fold hypotheses into the open A/B backlog;
> update the `[evidence]` block of the type the project instantiated (validating or
> flagging); detect class-level trends across ≥ 2 retros of the same class; propose type
> version bumps as reviewed tickets. If two projects fail-fit the same signals, propose a
> new class (§6.4).

The synthesis path from signal to durable change is the pipeline that already exists:

```
retro hypotheses ──► org-review (T0, cross-project read)
                          │
                          ▼
              A/B harness when the claim is causal        (scripts/experiments/ab_harness.py,
              (same-work invariant, quality floor,         ab-experiment-harness.md)
              difficulty-normalized composite)
                          │
                          ▼
              reviewed PR bumping the unit type's version
              (ticket → worker → gate → merge — constitution §V.1)
                          │
                          ▼
              next project of the class instantiates the improved type
```

### 3.4 The two levels

The same loop runs on two subjects; neither gets a bespoke mechanism:

- **Level A — unit types for what Kensho builds.** Evidence source: child-deployment
  retros (chess, excel-lite, every future application). Output: version bumps to catalog
  types (`objective-oracle-build`, `gui-application`, …). This is #200's original target.
- **Level B — unit types for building Kensho itself.** Evidence source: Kensho's *own*
  ledger and org-review runs — the standing `delivery`/`platform`/`quality` team shapes,
  the wave/fan-out pattern, the daemon-critical serial discipline. These are already
  unit types in fact (tuned by the SQU-42 field report and codified in
  operating-model.md); this design makes them unit types in *form*: their configuration
  gets the same `[work_class]`/`[evidence]` annotations, and changes to `instances.toml`
  defaults route through the same evidence → org-review → reviewed-PR path (which
  model-economy §6.6 already mandates for tier defaults). Level B is where the flywheel
  closes: a better `daemon-critical` type makes Kensho better at improving Kensho.

Level B has one extra constraint Level A lacks: its subject includes the machinery running
the loop itself. org-review's existing regress guard applies unchanged — analyze one level
up, never recursively.

---

## 4. Cross-project outcome ingestion

Today each application is a separate daemon with its own `.agent_team/outcomes` ledger
(project Kensho on 8787, excel-lite on 8788), and retros were hand-carried. **Decision:
no shared central outcomes store. Ingest the retro artifact, not the raw ledger, over the
already-proven upstream feedback channel.**

The precedent already works in production: the chess deployment (`465d4bed`) submitted
feedback items carrying an `[origin]` block (`project`, `deployment_uri = "agt://…"`,
build) into *this* repo's `.agent_team/feedback/items/`, where feedback-triage folded them
into tickets (e.g. SQU-168, and the #200 bootstrap-spec fold). The retro rides the same
rails:

1. **Child side:** the terminal retro step (§3.2) submits `retro.json` + `retro.md`
   upstream via the configured feedback route — a `category = "retro"` submission with the
   same `[origin]` envelope. The upstream route is a template parameter
   (`meta.upstream_uri`), rendered at `init` — every bootstrapped project ships knowing
   where its retro goes.
2. **Parent side:** feedback-triage recognizes `category = "retro"` and lands the artifact
   at `.agent_team/retros/<project-id>/` (a store parallel to `feedback/items/`), then
   notifies org-review's next run rather than clustering it as ordinary feedback.
3. **Drill-down stays remote:** the raw per-job ledger remains in the child. When
   org-review needs job-level ground truth behind a retro claim, it reads the child's
   resources by `agt://` URI (distributed-resources.md) — the same cross-deployment read
   path units use for reports. Aggregates travel; evidence is referenced, not copied.

Why not a shared store: it couples deployments a fleet model wants independent, and the
raw ledger is the *wrong grain* for synthesis — the chess headline finding (self-inflicted
serialization) required causal reconstruction from event logs and git timestamps, not
ledger sums. The retro is precisely the artifact that carries aggregates *plus the causal
narrative*; centralizing rows would tempt the meta-learner to correlate without causes.

One deliberate consequence: **a project that never completes never retros.** Excel-lite is
parked awaiting manager auto-wake (#264/#270); its retro lands when the study genuinely
ends, not before — hand-nudging it through would fake the very autonomy the study
measures. Interim signal still flows as ordinary feedback (as chess's did mid-run).

---

## 5. The library: storage, selection, evolution

### 5.1 Storage — types are templates, stored where templates live

**Decision:** the catalog lives at `templates/<type-name>/` in this repo (peer to the
existing bundled `template/`, which remains the generic default), each entry a complete
template tree with the §1.2 manifest. Distribution reuses the template machinery verbatim:
git refs, `template pull`, cache, `.template.lock`, semver. When the catalog outgrows the
repo (external consumers, many types), it moves to a dedicated templates repo — a ref
change, not a redesign. Versioning discipline: parameter-default tuning = minor bump;
role/pipeline-shape changes = major bump; `[evidence]` updates alone = patch.

### 5.2 Selection — classify → pick → parameterize

At project charter: run the §2.2 intake checklist → primary class per surface → pick the
class's current type version → `agent-team init <ref> --set …` with project parameters →
record `type@version` + classification in child config and parent project record. Three
selection outcomes are legal, and the third is load-bearing:

1. **Clean fit** — instantiate the type.
2. **Composite fit** — primary type + overlay (e.g. objective-oracle + GUI overlay), until
   #223 v3 makes composition first-class.
3. **No fit** — instantiate the generic default template with *tightened* retro
   instrumentation, and say so in the project record. §6.4 governs when repeated no-fits
   open a new class.

### 5.3 Evolution — the compounding loop, and the transfer test

Each completed project of a class updates its type: retro → org-review synthesis →
(A/B when causal) → reviewed version bump → the *next* project of the class starts from
the improved configuration. The type is the accumulator; projects are the samples. Two
specific rules make the compounding honest:

- **The transfer test.** A type change validated on project N of a class earns
  `validated` status only after helping on project N+1 — a *different* project of the
  same class. Within-project replication (the A/B replay of the same backlog) establishes
  causality; cross-project transfer establishes that the class is real. A "class" whose
  lessons don't transfer is one project wearing a taxonomy.
- **Drift feeds forward.** When a child's forced team-evolution protocol diverges from its
  type mid-run and the retro shows the divergence helped, that is a pre-validated
  hypothesis — it enters org-review's queue ranked above armchair hypotheses. The library
  learns fastest from projects that disobeyed it well.

### 5.4 The bootstrap — seeding version 0.1 from the retros in hand

Seed now, from evidence that already exists; do not wait for the loop to be fully plumbed:

| Type | Seed source | v0.1 content |
|---|---|---|
| `objective-oracle-build` | Chess retro + #200 researcher reconstruction + chess-derived bootstrap spec | Slim verifier-first topology (3 workers / 2 reviewers / 1 machine-verifier / 1 manager / 1 release-time auditor); manager slicing checklist mandating fine-grained parallel dispatch of independent slices (the #1 lever); conformance-corpus-first slice ordering; tiered gates (smoke/acceptance/release, #266); policy-gated auto-merge for verifier-green low-risk slices; progress output on long gates; executable requirements traceability (#253); local-only audit script; honesty docs ("not yet claimed" release evidence). Maturity: `seed` — the recommended topology is retro-derived but not yet A/B-confirmed; the pending chess-backlog replay (#234 harness) is its confirmation run. |
| `gui-application` | Excel-lite retro (pending #264 unpark) + chess GUI gap | objective-oracle base + visual-QA gate (#254) + product-verify loop; no auto-merge on UI slices. Maturity: `seed`, thinner — one exemplar's retro still outstanding. |
| `daemon-critical` (Level B) | SQU-42 field report, operating-model.md, model-economy §5 floors | Serial locks on hot surfaces, T1 worker floor, mandatory adversarial review, `max_attempts = 1`, watchdog timeouts, no auto-merge. Maturity: `candidate` — a week-long ~100-job production run is the strongest evidence any type currently has. |
| `parallel-library-fan-out` (Level B) | Same field report (three concurrent manager streams) | Replicas to build-slot ceiling, board-gated dispatch, T2 workers, standard gate stack. Maturity: `candidate`. |
| `docs-prose`, `research-spike` | Current topology practice; the retros themselves | Thin seeds; codify current defaults. Maturity: `seed`. |

Seeding is a normal reviewed PR series: mechanical codification of decisions already made
elsewhere, which is exactly what makes it cheap.

---

## 6. Guardrails

| # | Failure mode | Guardrail |
|---|---|---|
| 1 | **Overfitting to tiny samples.** n=1 (chess) generalized into policy is superstition with a manifest. | The **maturity ladder** is schema, not vibes: `seed` (single retro; defaults are hypotheses, instantiation is welcome but the retro step is mandatory and instrumented tighter) → `candidate` (≥ 2 projects *or* one controlled A/B with the harness's same-work invariant and quality floor) → `validated` (passed the §5.3 transfer test on a distinct project). Claims travel with maturity: a `seed` type's README may not assert class-level optimality. Structural changes at any maturity require the A/B discipline; only parameter tuning may ride retro evidence alone. |
| 2 | **Premature crystallization.** A frozen catalog turns yesterday's sample into tomorrow's ceiling; teams stop noticing the config could be wrong. | Types are **priors, not mandates**: the forced team-evolution protocol ships *inside every type*, so any project can diverge mid-run with cause, and divergence-that-helped outranks conformity in synthesis (§5.3). No type is ever `frozen`; `validated` types still update on evidence. The selection step's legal "no fit" outcome (§5.2) keeps bespoke first-class. |
| 3 | **Metrics-methodology firewall breach.** Type performance stats leaking into instantiated prompts — a manager told "this config is the class-best, don't deviate," a worker told its class's yield — converts observer tools into targets and collapses the review economy. | `[evidence]` is catalog metadata: **rendered into no `.tmpl`, quoted in no kickoff, visible in no instantiated file.** The child gets configuration and instructions; only org-review, Kan, and James read the league table. Same rule as model-economy §5's difficulty-tag firewall and for the same reason; this extends metrics-methodology.md from agent-level to *organization-level* scores. The A/B harness's existing rule ("do not feed scores back into worker or reviewer prompts") is the enforcement precedent. |
| 4 | **Force-fitting the novel.** Classification confidence is unearned on genuinely new work; a wrong type imports wrong gates and wrong floors, and the retro then "learns" from a self-inflicted mismatch. | The intake checklist must produce a **confidence call**; conflicting or weak signals route to the T0 advisor, and the default under doubt is the generic template + tight instrumentation — a bespoke run is cheaper than a poisoned sample. **New-class rule:** a class is opened only when ≥ 2 projects fail-fit existing types *on the same signals*; one strange project is an anecdote, two agreeing are a category. Novel projects are prized as retro sources precisely because they map the taxonomy's edges. |
| 5 | **Goodhart at the topology level** (#200's own warning). A "fast" type that ships garbage; synthesis optimizing wall-clock into quality collapse. | The objective is the harness's composite: quality floor is *hard* (gates passed, every planned slice completed exactly once, no escaped defects) before any speed/cost comparison is admissible; all metrics difficulty-normalized, longitudinal, within-class. The North-star reframe on #200 is binding: the target is the *balance* — no single objective may be maximized into the others' collapse. |
| 6 | **Survivorship and selection confounds.** Types are assigned to projects non-randomly (by class!), so cross-type comparisons are difficulty comparisons in disguise. | **Never rank types against each other.** A type is compared only against its own history within its class (metrics-methodology's within-cohort rule). The classification audit (§3.2 first row) exists to catch the subtler version — a type "underperforming" because intake misclassified the project. |
| 7 | **Evidence decay.** A type validated on Sonnet-5-tier workers, current gate shapes, and current prices silently rots when bindings rotate (model-economy §2 re-derivation rule). | `[evidence].tier_bindings` pins the model-economy snapshot the evidence was gathered under. org-review flags every type whose bindings have shifted materially since `last_validated`; a flagged `validated` type demotes to `candidate` until re-confirmed. |

---

## 7. Decision summary

1. **A unit type is a template plus a work-class binding plus evidence** (§1.2 schema:
   `[work_class]`, `[evidence]`, maturity ladder). Templates-as-images is the substrate;
   #223's typed catalog is the destination; this doc is the lifecycle between them. One
   catalog, two grains over time.
2. **Work-classes are lever-defined, not domain-defined** (§2): objective-oracle build,
   GUI application, daemon-critical/hot-file serial, parallel fan-out, docs/prose,
   research-spike. Five intake signals classify; classification is a Kan/Kai checklist,
   recorded, audited by every retro.
3. **The loop is instrument → retro → synthesize** (§3): ledger + bounce-class + drift
   events; a mandatory terminal retro pipeline step producing a typed artifact; synthesis
   as a new evidence source in org-review (T0), through the A/B harness for causal claims,
   landing as reviewed type-version bumps. Two levels, one machinery: types for
   applications, and Kensho's own team shapes as Level B types.
4. **Ingestion reuses the upstream feedback channel** (§4): children submit
   `category = "retro"` artifacts with the proven `[origin]` envelope; parent stores them
   at `.agent_team/retros/`; raw ledgers stay in children, drill-down via `agt://` reads.
   No central store, no meta-daemon.
5. **The library lives at `templates/<type>/`, selected by classify → pick →
   parameterize, evolved by the compounding loop** (§5), with the transfer test gating
   `validated` status and drift-that-helped ranked first. Seeds ship now from the chess
   retro, the pending excel-lite retro, and the SQU-42 field report.
6. **Guardrails** (§6): maturity ladder against small-n, types-as-priors against
   crystallization, an absolute firewall keeping type stats out of instantiated prompts,
   advisor-routed bespoke for the novel, hard quality floors against topology-Goodhart,
   within-class-only comparison, and evidence pinned to tier bindings.

## 8. Open questions

- **Catalog home at scale.** `templates/<type>/` in-repo is right for the first ~5 types;
  the trigger and mechanics for moving to a dedicated templates repo (or the #223 catalog
  resource) are undecided.
- **Re-graining under #223 v2/v3.** When charter-from-unit-type lands, does a whole-team
  type decompose into a composition manifest of sub-team units automatically, or by a
  reviewed migration per type? Leaning per-type migration — compositions should be proven,
  not assumed.
- **Retro authorship.** Child manager with a `retro` skill (this doc's v1) vs a dedicated
  retro/requirements-auditor specialist role (the chess bootstrap spec suggests one).
  Decide after the first two pipeline-native retros show whether manager-authored retros
  self-flatter.
- **Automatic classification.** The intake checklist is manual by design; the threshold of
  misclassification data that justifies automating it (and whether the classifier is ever
  more than a rubric) is open.
- **Drift → version-bump automation.** Should a child's evolved config *mechanically*
  open a type-bump proposal on retro submission, or stay a synthesis judgment? Leaning
  judgment — mechanical proposals from n=1 drift recreate guardrail #1.
- **Retro schema versioning** and the minimum schema that lets org-review synthesize
  without coupling to project internals (mirror of #223's unit-report question).
- **Excel-lite dependency.** The `gui-application` seed and the second objective-oracle
  sample both wait on the excel-lite study completing end-to-end (#264/#270 auto-wake).
  Nothing here blocks on it, but §5.4's second column is thin until then.

---

*The first version of every type is a memory of what already worked. The library's value
is not any snapshot — it is that the next project of a class never starts from zero, and
the one after that starts from better than that.*
