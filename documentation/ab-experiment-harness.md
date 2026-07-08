# A/B Experiment Harness

Status: v1 harness and methodology. This does not execute jobs yet; it validates
the experiment inputs, emits a reproducible dry-run runbook, and compares arm
results once a run has produced outcome JSON.

## Purpose

GitHub #200 asks for a controlled way to evolve team topology from outcomes
without turning raw speed into the target. The first concrete artifact is a
reproducible A/B harness:

- same canonical backlog,
- two topology configs, usually baseline and `slim-verifier-first`,
- arm-specific slicing and dispatch policy,
- quality-inclusive, difficulty-normalized metrics,
- a report that can be reviewed before any broader search or automated
  execution exists.

The chess-project retrospective produced an important causal refinement:
over-provisioning was not the root cause by itself. The dominant lever was
coarse slicing plus serial dispatch. The harness therefore treats topology and
slicing as separate inputs. You can compare baseline monster slices against
fine-grained parallel slices, compare two topologies over the same slices, or
layer the verifier-first topology after isolating slicing.

## Tool

Run the harness with Python 3.11+:

```sh
python3 scripts/experiments/ab_harness.py \
  --backlog /path/to/backlog.json \
  --baseline-topology /path/to/baseline.instances.toml \
  --candidate-topology /path/to/slim-verifier-first.instances.toml \
  --output /path/to/ab-report.md \
  --summary-json /path/to/ab-summary.json
```

Add result files after executing both arms:

```sh
python3 scripts/experiments/ab_harness.py \
  --backlog /path/to/backlog.json \
  --baseline-topology /path/to/baseline.instances.toml \
  --candidate-topology /path/to/slim-verifier-first.instances.toml \
  --baseline-results /path/to/baseline-results.json \
  --candidate-results /path/to/candidate-results.json \
  --output /path/to/ab-report.md
```

The script is intentionally stdlib-only and lives outside `internal/daemon`.
Actual job dispatch is a follow-up. v1 produces the plan and consumes results;
it does not mutate daemon state.

## Backlog Schema

The backlog file is JSON. `items` defines the canonical work. `arms` can define
how each arm groups that same work into slices.

```json
{
  "name": "chess-bootstrap-retro",
  "hypothesis": "Fine-grained verifier-first delivery improves cost and wall-clock without lowering quality.",
  "items": [
    {
      "id": "board-primitives",
      "title": "Board primitives and squares",
      "difficulty": 3
    },
    {
      "id": "fen",
      "title": "FEN parser",
      "difficulty": 3,
      "depends_on": ["board-primitives"]
    },
    {
      "id": "search",
      "title": "Search and move choice",
      "difficulty": 5,
      "depends_on": ["board-primitives"]
    },
    {
      "id": "uci",
      "title": "UCI loop",
      "difficulty": 3,
      "depends_on": ["board-primitives"]
    }
  ],
  "arms": {
    "baseline": {
      "slices": [
        {
          "id": "monster-core",
          "title": "Bundled core/search/UCI delivery",
          "items": ["board-primitives", "fen", "search", "uci"]
        }
      ]
    },
    "slim-verifier-first": {
      "slices": [
        {"id": "board-primitives", "items": ["board-primitives"]},
        {"id": "fen", "items": ["fen"]},
        {"id": "search", "items": ["search"]},
        {"id": "uci", "items": ["uci"]}
      ]
    }
  }
}
```

Rules:

- Every `items[].id` must be unique.
- `difficulty` can be a positive number or one of `xs`, `s`, `m`, `l`, `xl`.
- Dependencies are canonical item ids.
- Each arm's slices must cover every canonical item exactly once.
- Slice difficulty is always the sum of its canonical items, not an arm-supplied
  value. This prevents an arm from making itself look cheaper by relabeling
  difficulty.
- If an arm omits `slices`, each canonical item becomes one slice.

## Topology Inputs

Each topology input is an `instances.toml` file using the schema documented in
`documentation/topology.md` and `docs/authoring/topology.md`. The harness reads:

- declared instances,
- ephemeral replica capacity,
- worker, reviewer, and verifier capacity,
- pipeline step order,
- whether a verifier-like step appears before review.

The harness does not require that the topologies be vendored into `.agent_team/`
for a dry-run. For execution, copy each topology into the isolated repo or
template layer used by that arm and record the exact file in the experiment
artifact.

## Result Schema

When real arm execution exists, provide one result JSON file per arm. The
harness accepts either `agent-team outcomes report --json`-style summaries or a
list/object of per-slice records.

Recommended per-slice shape:

```json
[
  {
    "id": "board-primitives",
    "status": "done",
    "created_at": "2026-07-07T10:00:00Z",
    "finalized_at": "2026-07-07T10:40:00Z",
    "tokens_consumed": 2400000,
    "bounce_count": 0,
    "review_rounds": 1,
    "human_interventions": 1,
    "quality": {
      "status": "pass",
      "required_gates_passed": true
    },
    "work_units": [
      {
        "started_at": "2026-07-07T10:00:00Z",
        "finished_at": "2026-07-07T10:40:00Z"
      }
    ]
  }
]
```

Fields aligned with the outcomes ledger are also accepted: `tokens_consumed`,
`review_rounds`, `bounce_count`, `post_merge_defect_backlinks`, `work_units`,
`merged_at`, `finalized_at`, and `usage.input_tokens` plus
`usage.output_tokens`.

## Metrics

The report compares these metrics:

| Metric | Definition | Direction |
| --- | --- | --- |
| Quality floor | Required gates passed, all planned slices merged, no escaped defects. | Must pass |
| Effective concurrency | Sum of runtime work-unit duration divided by wall-clock duration. | Higher |
| Wall-clock / difficulty point | Arm wall-clock minutes divided by canonical difficulty points. | Lower |
| Tokens / merged slice | Tokens consumed divided by merged slices. | Lower |
| Tokens / difficulty point | Tokens consumed divided by canonical difficulty points. | Lower |
| Bounce rounds / difficulty point | Review bounces divided by canonical difficulty points. | Lower |
| Human interventions / difficulty point | Manual approvals, operator fixes, or other human touches divided by difficulty points. | Lower |

Do not declare a faster arm better if the quality floor fails. Speed for garbage
is a failed experiment, not a topology improvement.

## Dry-Run Protocol

1. Freeze the backlog JSON and topology files.
2. Run the harness without result files and commit or attach the dry-run report.
3. Inspect the same-work check, slice waves, and topology summary.
4. Decide which causal lever is being isolated:
   - slicing plus parallel dispatch,
   - verifier-first pipeline,
   - slim topology,
   - auto-merge policy,
   - or a layered combination.
5. If running both arms, create clean isolated repos or job stores. Do not share
   daemon state, queues, usage records, or branches between arms.
6. Randomize or alternate arm order when possible. If order cannot be
   randomized, record why.
7. Dispatch slices wave by wave. Slices in the same wave may run concurrently;
   later waves wait on dependencies.
8. Capture job outcomes, usage, gates, review bounces, human interventions, PR
   merges, and any escaped-defect backlinks.
9. Re-run the harness with result JSON files and review the comparison.
10. Turn conclusions into a normal ticket or PR. Do not feed scores directly
    back into worker or reviewer prompts.

## Quality Firewall

This harness follows `documentation/metrics-methodology.md`: metrics are for
observers, not for the agents being measured. The measured workers and
reviewers should receive concrete process instructions, not their own scores.

Acceptable follow-up examples:

- Change the manager dispatch checklist to split independent work earlier.
- Add a verifier step that records deterministic gate evidence before LLM
  review.
- Adjust topology replica counts when evidence shows capacity is dead weight.

Unsafe follow-up examples:

- Tell workers to minimize tokens.
- Tell reviewers to minimize bounce rounds.
- Optimize for wall-clock without a quality floor.

## Known v1 Limits

- The harness does not dispatch jobs, copy topologies, or start daemons.
- It does not infer human interventions automatically unless the result JSON
  records them.
- `agent-team outcomes report --json` summaries may not contain wall-clock for
  the full arm; per-slice records are preferred for full comparison.
- It does not pick a winner automatically. The output is evidence for a human
  or org-review decision, not an optimization target.
