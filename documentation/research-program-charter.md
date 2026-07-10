# Empirical software research program

**Status:** standing charter. The declared implementation lives in the full
topology profile and in this repository's self-dogfood `instances.toml`.

## Purpose and boundaries

The research program improves Kensho by repeatedly using it to build and
evaluate real, locally runnable software. Each study produces two independently
supportable conclusions:

1. a **product verdict** against objective product gates; and
2. a **Kensho/process verdict** against preregistered organizational
   hypotheses.

A green product does not prove the process hypothesis, and a useful process
result cannot excuse a failed product gate. Activity, configured replica count,
and task completion are not success; accepted behavior, reproducible evidence,
falsified assumptions, and reduced uncertainty are.

The program is the prospective evaluation plane. Existing org-review,
harness-review, and feedback loops remain retrospective governance. The
evaluation plane may file or enrich Kensho issues, but it never implements its
own recommendations. Implementation stays in the normal delivery plane and a
later study retests the landed change. This separation prevents the program
from grading work it changed itself.

Local execution is mandatory. Products, gates, and evidence must not depend on
a hosted runtime, paid service, cloud account, or external publication target.
Verified reports or demos may later be distributed through an optional static
host such as Cloudflare Pages, but publication is never part of product
execution or evidence validity.

## Study classes and baselines

Every study is either **controlled** or **observed**:

- A controlled study preregisters a treatment and adds work or product gates.
  V1 permits one controlled study at a time so treatment effects and shared
  integration pressure remain interpretable.
- An observed study measures work already happening. It must add no marginal
  build work or product gate and cannot compete with the controlled study's
  integration or review capacity.

Chess Engine and Excel Lite are frozen contextual baselines, not demos to be
rewritten:

| Baseline | Observed stress | Durable lesson |
| --- | --- | --- |
| Chess Engine | Correctness-heavy state semantics, protocol behavior, native UI, release honesty; effective concurrency 1.00 with no dedicated verifier lane. | Adversarial review found real bugs, while coarse serial slices and manual approval dominated throughput. |
| Excel Lite | Wide feature fan-out and integration; effective concurrency 1.57 despite 187 worker jobs, one reviewer job, no verifier jobs, and direct dispatch outside pipelines. | Worker capacity did not create accepted parallelism; verification, integration ownership, and canonical progress were the bottlenecks. |

Workplane is the first controlled V1 pilot. Its durable-state, permission,
replay/idempotency, realtime, human/agent parity, recovery, search, and UI
contracts exercise the integration and evidence weaknesses exposed by both
baselines. Its role is to test the program, not to establish that the program
is already successful. Study activation and product integration remain manual
decisions backed by verifier and reviewer evidence.

## Standing organization

- `research-manager` is persistent. It owns the portfolio, preregistration,
  graph-safe wave selection, cross-slice contracts, integration arbitration,
  evidence completeness, and escalation. It does not perform routine
  implementation or substitute for independent verification/review.
- `research-worker` is pipeline-bound with up to eight replicas. Each run owns
  one declared report or release-bearing slice.
- `research-verifier` has three replicas. It reproduces deterministic gates on
  exact artifacts or commits and records canonical evidence before review.
- `research-reviewer` has three replicas. It independently checks semantics,
  architecture, claims, and falsification integrity after verification.
- `research-auditor` is persistent and read-only. It freezes preregistration
  digests and reconciles claims against repository, job, gate, daemon, topology,
  and artifact ground truth. It may block a research verdict without blocking
  an otherwise valid product release.

Authority is deliberately narrow. Research workers, verifiers, and reviewers
receive only the ordinary role verbs needed by their pipeline step. The
research manager may advance, bounce, close, or merge its own jobs and maintain
the issue trail, but cannot bypass the pipeline. The auditor can read only.

## Liveness, budget, and absorption

The daemon owns liveness:

- `research-reconcile` wakes `research-manager` every two hours and on daemon
  start.
- `research-evidence-audit` wakes `research-auditor` every 84 hours.
- Pipeline completion and deliverable events wake `research-manager` by
  `pipeline` identity, even when their `target` remains a verifier or reviewer.

Each manager wake must dispatch the next graph-safe work, record a concrete
hold/block with owner and evidence, or close a terminal checkpoint. Silence is
not project state.

The research team receives a generous daily envelope of
`100_000_000_000` tokens and at most 16 in-flight jobs. Per-run soft allowances
are 100M/2h for the manager, 80M/90m for workers, 15M/30m for verifiers, and
30M/45m for reviewers. These are ceilings, not utilization targets.

Initial release-bearing WIP is four units across at most two product tracks.
It may expand to six units across at most three tracks only after two
consecutive waves have green verifier and reviewer gates, bounded review age,
no dead-letter or dispatch-churn incident, no red-main interval, and no rise in
escaped defects. It contracts immediately when review age, integration debt,
or host load becomes the bottleneck. Worker WIP must also remain at or below
twice available verifier-plus-reviewer capacity.

Each controlled study preregisters an effective-concurrency floor. Three
consecutive daily observations below that floor pause new dispatch and require
a documented reprice/continue/stop decision. Remaining token headroom never
justifies widening coupled work.

## Pipeline-only execution

Research work has no direct product-dispatch path. Direct release-bearing jobs,
gate waivers, substituted reviewers, or automatic manual-gate decisions are
protocol failures recorded in the study ledger.

`research_study` preserves this order:

```text
preregister -> verify -> review -> activate (manual)
```

Preregistration freezes expected product behavior, hypotheses, objective
oracles, hard gates, metrics, falsifiers, topology and budget, evidence schema,
and known confounders. Verification checks the exact declared report, captures
its SHA-256, and checks verdict separation plus required preregistration
content. Independent review challenges the hypotheses and metrics. Only the
manual activation step may authorize product slices.

`research_slice` preserves this order:

```text
implement -> verify -> review -> integrate (manual)
```

Each implementation step owns one pinned requirement/acceptance slice and its
integration duty. Verification reproduces focused and integration gates
against the exact worker commit. Review judges content and evidence. Manual
integration retains cross-cutting, security, contract, and release judgment.

## Evidence contract

Every study publishes a versioned bundle with these logical records:

1. `study.yaml`: identity, source revision, stress profile, hypotheses,
   expected behavior, oracles, hard gates, falsifiers, topology digest, budget,
   and confounders.
2. `requirements.jsonl` and `edges.jsonl`: the shadow requirements graph and
   evidence-derived state.
3. `runs/<run-id>/manifest.json`: source commit and dirty flag, environment,
   topology/agent/prompt digests, commands, timestamps, results, and artifact
   paths with SHA-256 digests.
4. `process-metrics.json`: effective and peak concurrency, role utilization,
   pipeline conformance, bounces, review findings, interventions, runtime,
   token usage, queue/review age, integration conflicts/debt, escaped defects,
   and acceptance state.
5. `product-audit.md`: the reproducible product verdict only.
6. `kensho-retrospective.md`: the process verdict, hypothesis outcomes, and
   framework finding ledger.
7. `study-result.json`: terminal classification and immutable evidence links.

An append-only event stream records schema version, study, timestamp, kind,
job, unit, role, runtime/model tier, pipeline conformance, and detail. A
`via_pipeline=false` release-bearing event is an integrity failure. Cross-study
metrics are keyed by metric-definition version, and the cumulative hypothesis
ledger uses only `supported`, `contradicted`, or `unresolved` with evidence
digest and replication count. Targets and scores never enter agent prompts.

## Requirements-graph hypothesis

The requirements graph is a shadow experiment, not a control-plane mandate.
Nodes identify requirements, acceptance conditions, gates, deliverables,
decisions, or integration points with source digests, owner, risk, state,
promotion gate, evidence references, and last reconciled revision. Typed edges
express `depends_on`, `blocks`, `conflicts_with`, `integrates_with`, `verifies`,
`satisfies`, and `supersedes` relationships.

The graph succeeds only if it reconstructs frozen baseline state without
regression, agrees with repository/job/gate evidence at checkpoints, prevents
stale prose from causing duplicate work, and makes at least one useful
safe-wave or conflict prediction without becoming a second hand-maintained
source of truth. It is falsified if maintenance exceeds the reconciliation it
replaces, facts require duplicate transcription, canonical evidence cannot
deterministically resolve state, or graph-derived waves increase conflict,
duplication, or escaped defects. Promotion requires successful shadow use in
two materially different studies.

## Findings and sunset rules

Reviewers classify findings as product, study-protocol, Kensho mechanism,
infrastructure, or unresolved. Product findings stay with the study. Kensho
findings are deduplicated against existing issues and then folded or filed with
study id, pinned source, reproduction, severity, affected mechanism, and
falsification context. A severe correctness or liveness failure may justify
immediate action; lower-severity mechanism claims require replication or an
explicit manager/advisor decision. Every accepted improvement names the later
study or checkpoint that will retest it.

The standing topology is reviewed after every study checkpoint and formally
after three studies. A spawned unit with no accepted deliverable or
evidence-bearing terminal decision inside its forecast enters dissolution
review. Recursive units exist only for sustained scale pressure, are limited to
one level in V1, and require an explicit charter, WIP cap, deliverable,
integration duty, evidence obligations, debt ledger, and sunset. The program
continues until the owner ends it, while its organization, budgets, and methods
remain revisable through the same reviewed change path.
