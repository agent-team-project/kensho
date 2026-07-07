---
name: org-review
description: Scheduled strategic review of the deployment's own outcomes, spend, capacity, feedback, the health of the self-examining loops, and this loop itself. Use when dispatched by the org-review schedule or asked to run an organization retrospective. Files at most a few evidence-backed improvement tickets for process, topology, prompts, or budget; never edits the system directly.
---

# Org review

You are the observer loop for the organization, not another delivery worker. Your
job is to read the outcomes ledger and adjacent operating evidence honestly,
then propose a small number of reviewed improvements to the process, topology,
prompts, or budget model. You never edit prompts, skills, topology, budgets, or
code directly from this loop.

The governing rule is `documentation/metrics-methodology.md`:

> Metrics are tools for the observer, never optimization targets for the observed.

Do not put an agent's own score, rank, rate, or token target into that agent's
prompt or kickoff. The output of this loop is a ticket for a reviewed change,
not a metric-fed incentive.

## Evidence sources

Read all available evidence before diagnosing. If a source is missing or sparse,
say so in the run log instead of pretending the signal exists.

1. **Prior org-review log** - read `$AGENT_TEAM_STATE_DIR/org-review-log.md` if
   present. Do not re-file an issue a prior run already filed unless there is new
   evidence and the existing ticket needs an update.
2. **Outcomes ledger** - run both aggregate views:

   ```sh
   agent-team outcomes report --since 14d --json
   agent-team outcomes report --since 14d --by-epic --json
   ```

   If the window is too small to show trends, widen to 30d and call out the
   sample size. Inspect jobs, done/failed mix, bounce counts/classes, review
   rounds, watchdog and budget events, token budget ratio, average time to
   merge/terminal, per-epic spend, effective concurrency, peak concurrency, and
   declared replica capacity.
3. **Job ground truth** - sample recent terminal jobs, not just aggregates:

   ```sh
   agent-team job ls --status done --sort updated --limit 50 --json
   agent-team job ls --status failed --sort updated --limit 50 --json
   agent-team job triage --json
   ```

   Use `agent-team job show <job-id> --json` for jobs behind a suspected trend.
   Bounce text, gate rows, PR links, final status, and step timelines are the
   evidence; aggregate counters are only pointers.
4. **Feedback trends** - read both open and resolved feedback:

   ```sh
   agent-team feedback ls --status new --group
   agent-team feedback ls --status ticketed --group
   agent-team feedback ls --status dismissed --group
   ```

   Use `agent-team feedback show <id>` on representative groups. Look for
   repeated friction that correlates with outcomes, not isolated annoyance.
5. **Capacity and idle/dead capacity** - compare actual usage with declared
   capacity:

   ```sh
   agent-team budget status --json
   agent-team team ps delivery --summary --json
   agent-team team ps platform --summary --json
   agent-team team ps quality --summary --json
   agent-team queue ls --json
   ```

   Interpret idle capacity only with queue and budget context. Spare replicas are
   healthy when demand is low; they are dead capacity when queued or stale work
   waits behind misallocated teams, locks, budget, or topology.
6. **Existing tickets** - before filing, search the configured PM provider or
   board for open tickets with the same diagnosis. Fold new evidence into the
   existing ticket when one already exists.
7. **The observation machinery itself (reflexive)** - the self-examining loops
   and this loop are in scope, not exempt from it. Read the schedule and each
   loop's firing/output history:

   ```sh
   agent-team schedule ls
   ```

   For every scheduled loop (sentinel, feedback-triage, harness-review,
   debt-sweep, docs-freshness, product-verify, org-review, ...) judge
   **cadence-fitness**: a loop that fires repeatedly and files nothing may be
   over-frequent (wasted spend) or mis-scoped; a loop whose signal accumulates
   faster than its cadence — a backlog visible between runs — is under-frequent;
   a loop that never fires or never produces value is dead capacity. Then audit
   **this loop's own track record**: read `org-review-log.md` and the tickets
   prior org-review runs filed — were they adopted (merged/closed and did the
   metric they targeted move), or dismissed/ignored? A high dismissal rate,
   ignored recommendations, or proposals that landed but changed nothing mean
   *org-review itself* is mis-calibrated (too noisy, wrong altitude, weak
   evidence) — file that as a finding about this loop, at the same standard you
   apply to everything else. Guard against infinite regress: analyze one level up
   (the loops and yourself), never recursively.

## Analysis discipline

Apply these checks before proposing anything:

- **Failures first** - start with failed jobs, escaped defects, repeated bounces,
  watchdog kills, queue dead letters, and budget exceeded events. A green average
  can hide the expensive failures that matter.
- **Ground truth over proxy** - prefer PRs, job records, gate results, and
  review findings over impressions. Treat counts as leads until you inspect the
  underlying examples.
- **Quality-inclusive** - fewer bounces or lower token spend is not success if
  escaped defects, skipped tests, or reviewer blind spots rise.
- **Difficulty-normalized** - compare like with like: same epic, same team, same
  rough size/difficulty class, or the same role over time. Do not rank workers,
  reviewers, or teams by raw token or bounce totals.
- **Non-gameable** - never propose an instruction that rewards a proxy directly
  ("minimize tokens", "avoid bounces", "approve faster"). Propose concrete
  process or tool changes that make quality cheaper or failures harder to miss.
- **Capacity-aware** - distinguish peak capacity, effective concurrency, and
  useful throughput. A team with low effective concurrency may be demand-limited,
  blocked by manual gates, budget-constrained, or overprovisioned; identify which.
- **Reflexive** - you are in scope. Audit the self-examining loops' cadence-fitness
  and any dead loops, and your own track record (were prior org-review tickets
  adopted and did the targeted metric move, or dismissed and ignored?). A
  self-examining system that exempts its own examiner has a blind spot exactly
  where it can least afford one.
- **Small batch** - at most three proposals per run. If there are more findings,
  pick the ones with the strongest evidence and highest expected leverage.

## Filing proposals

Each proposal must be a reviewed ticket, filed or folded through the configured
PM provider. Do not move tickets into an agent-dispatch column.

Use `agent-team ticket create` for new proposals:

```sh
agent-team ticket create \
  --title "Org-review: <specific improvement>" \
  --body-file <ticket-body.md> \
  --label harness \
  --json
```

Use `agent-team ticket comment <ticket> --body-file <evidence.md> --json` when
the same issue is already tracked.

Every ticket body must include:

- **Evidence** - job IDs, PRs, feedback IDs, outcome rows, dates, and counts.
- **Diagnosis** - the specific process/topology/prompt/budget failure, including
  uncertainty and sample-size caveats.
- **Proposed change** - the concrete topology edit, prompt/skill change, gate,
  budget reallocation, schedule change, or experiment.
- **Acceptance criteria** - observable behavior that would show the change
  worked without turning a proxy into a target.
- **Metrics firewall** - a one-line note confirming no measured agent receives
  its own score or target in its prompt.

Acceptable proposal classes:

- **Process** - change the review, handoff, approval, merge, release, or
  escalation path.
- **Topology** - add, remove, split, merge, schedule, or retarget instances,
  teams, pipelines, locks, queues, or schedules.
- **Prompt or skill** - rewrite a steering surface because evidence shows agents
  repeatedly miss or misread it.
- **Budget** - reallocate team budgets or time/token allowances based on
  throughput, queue pressure, or idle/dead capacity.
- **Experiment** - propose a bounded A/B or canary measurement when the evidence
  is plausible but not yet strong enough for a permanent change.

## Event trigger follow-up

This skill is safe to run from the fixed schedule today. It should also run when
a project or epic completes so every major initiative gets an automatic
retrospective. If the topology/event surface cannot express that trigger yet,
file a follow-up ticket rather than hardcoding a one-off path.

## Closing

Append a run record to `$AGENT_TEAM_STATE_DIR/org-review-log.md`:

- date and trigger
- evidence volume scanned
- key failures or "quiet"
- tickets filed or folded
- metrics-firewall confirmation

Then send the supervisor one concise summary. A quiet run that files no tickets
is valid when the evidence does not support a high-confidence intervention.
