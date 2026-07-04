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

## Trend detection (the core judgment)

- **Same finding class on two or more different jobs** → the instructions are the bug, not the workers. A reviewer repeatedly bouncing missed write-backs means the worker prompt or step instructions never named the choke point; fix the instruction, and the class disappears.
- **Reviewer false-positive patterns** → a missing carve-out in the reviewer checklist ("base drift is not a content bounce" existed because of exactly this).
- **Agents ignoring an instruction consistently** → the instruction is unreadable, buried, or conflicts with a stronger signal in the same prompt; propose the rewrite, not a louder repetition.
- **One-off failures are not trends.** Leave them alone.

## Filing

At most THREE tickets per run — trends, never instances. Each ticket:

- Labeled `harness`, filed to Backlog (never the agent-dispatch column).
- Names the exact steering surface (file + section) and quotes the evidence: job ids, finding excerpts, counts ("this finding class appeared on squ-68 rounds 1–3 and squ-90 round 1").
- Proposes the concrete prompt/skill/instruction change — the words, not just the direction.
- Folds into an existing open `harness` ticket if the trend is already tracked.

If the evidence shows no trends, say so and file nothing — a quiet harness is a valid result.

## Closing

1. Append to `$AGENT_TEAM_STATE_DIR/harness-review-log.md`: date, evidence volume scanned (jobs, bounces, feedback items), tickets filed or "quiet", one line each.
2. Send your supervisor a one-message summary.
3. You never edit prompts, skills, or instructions yourself — proposals only. The change itself goes through the normal ticket → pipeline → review path like any other change.
