---
name: review-harness
description: Scheduled review of the team's own harness — agent prompts, skills, pipeline instructions — driven by operational evidence (bounce findings, feedback, failure patterns). Files trend-backed improvement tickets; never edits prompts directly. Use when dispatched by the harness-review schedule or asked to review the harness.
---

# Harness review

You review the machinery, not the code: agent prompts (`.agent_team/agents/*/agent.md`), skills (`.agent_team/skills/*/SKILL.md`), and pipeline step `instructions` are the steering surfaces of this deployment, and they drift out of date the moment reality moves. Your input is what actually happened since the last review; your output is at most three trend-backed tickets.

## Evidence sources (read all, in this order)

1. **Bounce findings** — the richest signal. For recently completed jobs (`agent-team job ls --status done`), read the `## Review findings (bounce N)` sections in kickoffs (`job show`) and the reviewer's PR comments. You are looking for *classes*, not instances.
2. **Attention events** — `bounce_attention` job events and `repeated_bounces` triage reasons: jobs the pipeline itself flagged as design-smell.
3. **The feedback store** — resolved AND new items (`agent-team feedback ls --status ticketed`, `--status dismissed`, `--status new`): agents reporting friction with their own instructions is direct harness evidence.
4. **Failure patterns** — watchdog kills, crash-finalizes, infra-signature matches (`job triage`, `job explain` on failed jobs): repeated infra-red on the same gate often means the instructions send workers down a known-bad path.
5. **The audit log** — `$AGENT_TEAM_STATE_DIR/../debt-auditor-*/audit-log.md` if present, and your own `harness-review-log.md` from previous runs (read it first; never re-file what a prior run filed).

## Classify every bounce first: preventable-by-machine vs. genuine-judgment

Before trend-detecting, sort each bounce finding into one of two buckets — this is the highest-leverage step, because the two buckets have *different output types*:

- **Preventable-by-machine** — the finding is a mechanical property a tool could have checked without a human: formatting (`gofmt`), lint, a test that should exist, a build/vet failure, a dead import, a stale generated file, a broken link, a schema/TOML validity error, a missing regression for a named edge case. **These should never reach a human review round.** Even a *single* such bounce is worth a gate proposal if the check is cheap and general — you are not waiting for a trend here, you are removing a whole class. Output: a **CI-gate or pre-handoff-check proposal** (the exact `.github/workflows/ci.yml` step or skill checklist item), citing the bounce that escaped. (squ-123 spent a round on `gofmt` and a round on a missing wildcard-deny regression — both were this bucket; the gofmt CI gate that resulted is the model.)
- **Genuine-judgment** — the finding required understanding intent: a logic error, a wrong abstraction, a security edge case, a spec misread. These are *correct* uses of a human review round; do not try to gate them away. Output: the prompt/skill/instruction trend fixes below.

Report the ratio each run (e.g. "12 bounces: 5 preventable-by-machine → 2 gate proposals; 7 judgment → 1 prompt trend"). A rising preventable ratio means the machine gates are lagging the work.

## Trend detection (for the genuine-judgment bucket)

- **Same finding class on two or more different jobs** → the instructions are the bug, not the workers. A reviewer repeatedly bouncing missed write-backs means the worker prompt or step instructions never named the choke point; fix the instruction, and the class disappears.
- **Reviewer false-positive patterns** → a missing carve-out in the reviewer checklist ("base drift is not a content bounce" existed because of exactly this).
- **Agents ignoring an instruction consistently** → the instruction is unreadable, buried, or conflicts with a stronger signal in the same prompt; propose the rewrite, not a louder repetition.
- **One-off judgment failures are not trends.** Leave them alone — but a one-off *preventable-by-machine* failure still earns a gate proposal (see above); the trend bar applies to judgment fixes, not to closing mechanical classes.

## Filing

At most THREE tickets per run. Each is EITHER a gate proposal (preventable-by-machine bucket) OR a trend fix (judgment bucket):

- Labeled `harness`, filed to Backlog (never the agent-dispatch column).
- Names the exact target: for a **gate**, the CI step or pre-handoff check (the literal `ci.yml` step, `go test` target, validator, or skill checklist line) + the bounce that escaped it; for a **trend**, the steering surface (file + section) + evidence (job ids, finding excerpts, counts).
- Proposes the concrete change — the CI YAML / checklist item / prompt words, not just the direction.
- Folds into an existing open `harness` ticket if already tracked.
- **Prefer a gate over a prompt reminder** whenever the finding is mechanically checkable: a CI gate removes the class permanently; a prompt reminder only nudges. "Tell workers to run gofmt" is worse than "CI runs gofmt."

Create and update PM tickets through the provider-abstracted verb, not through provider-specific API helpers:

```sh
agent-team ticket create --title "<title>" --body-file <ticket-body.md> --label harness --json
agent-team ticket comment <ticket-or-issue> --body-file <evidence.md> --json
```

If the evidence shows no trends, say so and file nothing — a quiet harness is a valid result.

## Closing

1. Append to `$AGENT_TEAM_STATE_DIR/harness-review-log.md`: date, evidence volume scanned (jobs, bounces, feedback items), tickets filed or "quiet", one line each.
2. Send your supervisor a one-message summary.
3. You never edit prompts, skills, or instructions yourself — proposals only. The change itself goes through the normal ticket → pipeline → review path like any other change.
