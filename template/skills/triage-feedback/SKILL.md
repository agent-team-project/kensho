---
name: triage-feedback
description: Scheduled triage of the local agent-feedback store — cluster new feedback plus system pain signals, then materialize tickets, fold into existing ones, or dismiss with a reason. Use when dispatched by the feedback-triage schedule or asked to triage feedback.
---

# Feedback triage

You are the judgment layer between raw agent feedback and the project board. Agents submit one-line observations mid-job (`agent-team feedback submit`); the harness stamps context automatically. Your job: decide, per cluster, whether the feedback deserves a ticket — and route it to the right board.

## Procedure

1. **Read the store.** `agent-team feedback ls --status new --group` for the clustered view, then `agent-team feedback show <id>` on each group representative. If the store is empty, check step 2 anyway; if that is also quiet, post a one-line "nothing to triage" summary and exit.
2. **Sweep system signals since the last triage.** The pipeline reports its own pain — include it alongside volunteered feedback:
   - `agent-team job triage` — look for `repeated_bounces` (a design-smell flag, not a worker-quality complaint) and repeated `gate_infra_failed` with the same matched signature.
   - Recent `bounce_attention` job events and watchdog-kill patterns (`agent-team job ls --status failed`, then `job explain`).
3. **Cluster by root cause**, not by wording. The `--group` fingerprint catches identical phrasing; you catch "same problem, different words". Frequency matters: a group with count 4 across three agents outranks a one-off.
4. **Decide per cluster** — exactly one of:
   - **MATERIALIZE**: file a ticket via the routing rules below. The ticket must meet the actionability standard: what happened, exact context (the store already captured instance/job/step/runtime/build — quote it), frequency ("N reports between DATE and DATE"), and a concrete acceptance criterion.
   - **FOLD**: the issue is already tracked — comment the new evidence (frequency, fresh context) onto the existing ticket.
   - **DISMISS**: not worth a ticket (one-off, already fixed, misunderstanding). Always record why — the reason is what stops the same dismissal being re-litigated next sweep.
5. **Resolve every processed item**: `agent-team feedback resolve <id> --ticket <ref>` or `--dismiss "<reason>"`. Nothing stays `new` after a sweep.
6. **Summarize** in one message to your supervisor (inbox or the configured channel): `N items + M signals → X filed (refs), Y folded, Z dismissed`.

## Routing

Read routes from `$PWD/.agent_team/config.toml` (`python3 -c 'import tomllib; ...'`):

```toml
[feedback]
upstream = "agent-team"            # route name for framework-level items

[feedback.routes.agent-team]
kind     = "linear"
team_key = "SQU"                   # destination Linear team
label    = "feedback"              # applied to filed tickets
```

Classify each MATERIALIZE cluster:

- **Deployment-local** (our config, our prompts, our pipeline instructions): file on THIS repo's board (the `[linear]` config) — fixable here without upstream changes.
- **Framework** (the agent-team CLI/daemon itself misbehaved or lacks a capability): file via the `upstream` route.
- **External** (another project's tooling that our agents touch): file via that project's named route if one is declared; otherwise treat as deployment-local with a note.

File through the `linear` skill using the route's `team_key` and `label`. For any non-local route, also apply a `source-project:<project.id>` label where `<project.id>` is read from `[project].id` in this repo's `.agent_team/config.toml`. Include the machine footer `agent-team-origin: project=<id> team=<team> instance=<instance> trigger=<trigger>` at the end of filed ticket descriptions or folded evidence comments; the `linear-graphql.sh` helper appends it automatically when the daemon exported `AGENT_TEAM_*` origin variables.

## Guardrails (hard rules)

- **Backlog only, every destination.** Never file into or move a card toward any team's agent-dispatch column. Feedback proposes; a human or supervising manager promotes.
- **Batch courtesy on non-local routes**: at most 3 tickets per external route per sweep — combine the remainder into one digest ticket if a sweep produces more.
- **You never implement.** No code changes, no dispatches. File, fold, dismiss, resolve, summarize, exit.
