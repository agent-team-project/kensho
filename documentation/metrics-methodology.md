# Metrics Methodology — measuring self-improvement without fooling ourselves

*Design doc, 2026-07-06. Authored by Kai (manager) after two docs-writer runs exited
clean with no output — the methodology was fully specified, so the manager wrote it
directly. Design-gated: implementation follows the outcomes ledger (SQU-135) and feeds
org-review (SQU-139).*

## The maxim (read this first, it constrains everything below)

> **Metrics are tools for the observer, never optimization targets for the observed.**

Goodhart's law — "when a measure becomes a target, it ceases to be a good measure" — is
not an aphorism in this system. It is a *mechanical certainty*. Our agents are optimizers
by construction: anything placed in a prompt as a goal *becomes* a target, and any target
that is a proxy gets gamed to the proxy's edge. Therefore every metric in this document
carries two mandatory annotations:

- **Lives at:** where the metric is read (always: org-review, the outcomes report, a human).
- **Never appears in:** the prompt, kickoff, or incentive of any agent whose behavior it
  measures. No exceptions.

A metric that creates adversarial pressure *between* roles is not a KPI — it is a **hazard**,
because the review economy's entire value is the worker↔reviewer tension, and a naive metric
collapses it (reward "fewer escaped defects" → reviewers bounce everything; reward "fewer
bounce rounds" → workers pressure reviewers to approve). When in doubt, do not measure it.

## The measurement problem for judgment roles

You cannot score a reviewer by counting bounces. A reviewer that bounces more is not better
or worse — bounce count without a ground-truth of *correctness* is noise. So the soundness of
everything here rests on finding signals with **independent ground truth**, not proxy counts.

## Reviewer quality over time

**1. Escaped-defect rate (the gold signal).** When a later PR fixes a defect in a
previously-*approved* PR, the approving reviewer missed it. This is objective and traceable:
require fix-PRs to backlink the PR that introduced the defect (a `Fixes-defect-in: #NNN`
trailer, or inferred from `git blame` on the changed lines landing in a reviewed PR).

- Metric: escaped defects attributable to a reviewer ÷ PRs that reviewer approved, over a
  trailing window. Trending **down** = improving.
- Lives at: org-review. Never appears in: the reviewer's prompt. (If a reviewer knew it was
  scored on escapes, it would bounce everything — the collapse.)
- Caveat: attribution is imperfect (some defects are un-catchable at review time; a later
  requirement change is not an escape). Prefer *trend within a reviewer* over cross-reviewer
  ranking, which imports too much difficulty confound.

**2. Mutation / canary testing (the only true oracle).** Escaped-defect rate has no
denominator — you cannot know what fraction of *catchable* bugs a reviewer catches from
natural data alone. So inject known defects and measure catch rate:

- Periodically route a PR containing a *planted, known* defect through review and record
  whether the reviewer caught it.
- Guardrails, non-negotiable: the planted PR is **clearly synthetic** (tagged in a channel
  the reviewer cannot see but the harness can), **sandboxed**, and **never mergeable** —
  a merge gate refuses it regardless of verdict. A canary that can reach main is a footgun.
- Metric: catch rate on planted defects, by class (logic / security / test-gap / perf).
  This is the closest thing to a real reviewer-skill measurement the system can have.
- Lives at: the metrics/analysis capability. Never appears in: any reviewer prompt (a
  reviewer that knows canaries exist changes its behavior — measure blind).

**3. Secondary signals** (weak, use only corroboratively): inter-reviewer agreement on the
same PR; false-positive rate (bounces the manager overturned). Neither is ground-truth;
neither is a KPI.

## Token efficiency over time

Raw tokens-per-PR is **meaningless** — a hard ticket legitimately costs more, so a team doing
harder work looks "worse." Only measure within a **difficulty class**:

- Difficulty denominator (pick one, or a composite, and hold it fixed): files touched, lines
  changed, bounce rounds required, or a pre-assigned complexity tag on the ticket.
- Metric: tokens-per-merged-PR **within a difficulty class**, trended against that class's own
  history. Also useful: tokens-per-bounce-round (is a given quality getting cheaper to reach?).
- Absolute thresholds lie; **longitudinal same-cohort comparison** is the only honest form.
- Lives at: org-review / budget allocation. Never appears in: worker prompts (a worker scored
  on tokens truncates its own diligence — the exact opposite of what we want).

## Role efficiency over time

- **First-pass yield:** PRs merged with zero bounces ÷ total PRs, per role, difficulty-normalized.
- **Rework ratio:** bounce rounds ÷ PRs.
- **Cycle time:** dispatch → merge, per difficulty class.

All difficulty-normalized, all trended within-cohort, all read by org-review, none in any agent
prompt.

## The firewall (the load-bearing architectural rule)

There are two populations of readers, and they must be **separated by construction**:

1. **The observer** (org-review, the outcomes report, humans) reads every metric here. Its job
   is to propose reviewed changes to prompts, skills, budgets, and topology.
2. **The observed** (workers, reviewers, loops) must never see, in prompt or incentive, any
   metric that scores their own behavior. They see *instructions* ("run gofmt", "cite the
   choke-point") — never *scores* ("your escape rate is 0.12, lower it").

The mechanism that improves an agent is a *reviewed change to its instructions*, made by the
observer on the basis of a metric. The agent optimizes against its instructions, not against
the metric. This is what lets us have honest curves: the thing being measured cannot reach the
measurement.

## Where this lives, and whether it's a role

The outcomes ledger (SQU-135) is the substrate; this methodology is how to read it honestly;
org-review (SQU-139) is the actor that turns readings into reviewed change. The open question:
does measurement warrant a **dedicated metrics/analyst role** (skill + instance), distinct from
org-review? Argument for: the methodology here is a genuine specialty (attribution, cohorting,
canary design, Goodhart-avoidance) that differs from "propose org changes." Argument against:
premature separation is a quick-win before we know the shape. Recommendation: **start as a skill
the org-review loop invokes**; graduate to a role only if the analysis consistently needs more
than one agent's context. Measure that decision the way we'd measure any other — by evidence.

## What this makes possible

Until we can measure improvement honestly, "self-improving" is a *claim*, not a *result*. This
methodology is what makes the thesis **falsifiable** — the curves can bend the wrong way, and we
would see it. That falsifiability is the point. A self-improvement system that cannot show its
own improvement, honestly, is indistinguishable from one that only asserts it.
