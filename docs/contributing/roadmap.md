# Roadmap Context

This page is the public direction-of-travel for `agent-team`. It is not a
commitment calendar. The project moves quickly because the repo dogfoods its own
agent fleet, so roadmap entries should stay grounded in evidence:

- the latest release tag and
  [CHANGELOG](https://github.com/agent-team-project/kensho/blob/main/CHANGELOG.md)
- merged GitHub PRs and commits on `main`
- open `epic` issues and active child issues in the configured
  `agent-team-project/kensho` GitHub Project
- design sketches under
  [`documentation/`](https://github.com/agent-team-project/kensho/tree/main/documentation/)

When a thing ships, it moves out of "in progress" and into the relevant
"recently shipped" notes with a release, PR, commit, or design link.

## Recently Shipped Foundation

[v0.5.0](https://github.com/agent-team-project/kensho/releases/tag/v0.5.0)
is the release represented by this roadmap update. It moves the self-dogfood
deployment to eight teams, eleven schedules, and seven pipelines; makes GitHub
Issues/Projects the planning source; adds release, research, and terminal-
interface control planes; ships the read-only `agent-team ui` Overview; and
turns budgets, authority, verifier evidence, exact-head review, and release
claims into explicit gates. See the
[v0.5.0 changelog](https://github.com/agent-team-project/kensho/blob/main/CHANGELOG.md#v050--2026-07-12)
for the merged-PR record and pre-v1 migration notes.

The terminal UI is intentionally at walking-skeleton scope. Overview and
`--once` are shipped; additional views, actions, full CLI parity, and removal of
the embedded web dashboard remain active GH-153 work. The standing research
topology is also shipped, while individual studies still advance only through
their preregistration, verification, review, and manual integration gates.

## Security And Isolation

Why it matters: autonomous agents read untrusted text, inherit local filesystem
and environment access, and can call the same CLI an operator can. The project
needs blast-radius control that is stronger than prompt discipline while still
preserving a file-backed, inspectable repo workflow.

Recently shipped:

- [SQU-90 / PR #94](https://github.com/agent-team-project/kensho/pull/94)
  added the origin envelope so jobs, queue items, events, locks, usage records,
  and outbound Linear writes know their project/team/instance/job owner.
- [SQU-92 / PR #96](https://github.com/agent-team-project/kensho/pull/96)
  added scoped resources and authority allowlists in audit mode.
- [SQU-121 / PR #124](https://github.com/agent-team-project/kensho/pull/124)
  added `env_allow` so topology can filter per-instance launch environments.
- [SQU-122](https://linear.app/squirtlesquad/issue/SQU-122/security-graduate-authority-audit-to-enforcement-for-destructive-verbs)
  graduated destructive daemon/CLI verbs from audit-only visibility to
  enforcement.
- [SQU-123](https://linear.app/squirtlesquad/issue/SQU-123/security-verb-aware-cli-shims-closed-world-allowlists-at-the-tool)
  added the verb-aware `agent-team` runtime shim. Under enforcement, the shim
  resolves invocations through the live Cobra tree and denies unknown or
  ungranted verbs before they reach the real CLI.
- [`documentation/security-model.md`](https://github.com/agent-team-project/kensho/blob/main/documentation/security-model.md)
  records the adopted security model: per-instance identity, brokered provider
  secrets, capability-style authorization, reader/actor separation for public
  input, and sandbox tiers.
- The SQU-120 probe verdict in that design doc found that naive Codex
  `workspace-write` on macOS blocks the daemon socket and several worker
  necessities. That makes a sandbox-compatible TCP-loopback API path prerequisite
  for a useful default sandbox.

In progress:

- No separate security epic is active in this section at the time of this
  update. The next hardening layer is the planned control-plane/workspace split.

Planned:

- [SQU-124](https://linear.app/squirtlesquad/issue/SQU-124/security-control-planeworkspace-write-split-agents-mutate-shared-state)
  splits agent workspaces from the shared `.agent_team` control plane so agents
  mutate durable shared state only through authority-checked daemon verbs.
- The sandbox path is staged: first a TCP-loopback/token API that works from
  runtime sandboxes, then `sandbox = "workspace"` for ordinary workers, then
  container workspaces and egress controls for less-trusted or remote execution.
- Public-input agents should get a structural reader/actor split before
  community triage becomes a normal outward-facing loop.

Out of scope for now: treating prompt text alone as a security boundary, or
jumping straight to multi-tenant containers before the daemon API and capability
model are ready.

## Distribution And Resource Model

Why it matters: the first versions assume one repo, one filesystem, one daemon
socket, and local worktrees. That is still the right default. The next step is
to name resources instead of locations so the same model can place work on
another machine or in a container without rewriting the control plane.

Recently shipped:

- [SQU-103 / PR #102](https://github.com/agent-team-project/kensho/pull/102)
  added team budget caps and admission control.
- [SQU-104 / PR #103](https://github.com/agent-team-project/kensho/pull/103)
  added per-job and per-agent allowances plus soft budget notices.
- [SQU-105 / PR #104](https://github.com/agent-team-project/kensho/pull/104)
  added opt-in hard token cutoffs with watchdog semantics.
- [SQU-106 / PR #105](https://github.com/agent-team-project/kensho/pull/105)
  added reserve vs. oversubscribe allocation, so outstanding child promises can
  be counted against parent headroom when an operator wants strict reservation.
- [SQU-127](https://linear.app/squirtlesquad/issue/SQU-127/daemon-named-deployment-addressing-registry-over-raw-paths-routes-v2)
  added stable deployment identity and a read-only deployment registry view:
  `agent-team deployments ls` and `agent-team deployments resolve`.
- The first resource-identity slice also added canonical `agt://` URIs and
  `agent-team read <agt-uri>` for daemon-owned reads of project, instance, job,
  workspace, state, log, usage, mailbox, channel, queue, outbox, lock, and
  topology resources.
- [`documentation/resource-constraints.md`](https://github.com/agent-team-project/kensho/blob/main/documentation/resource-constraints.md)
  now describes the full resource surface: build slots, tokens, provider
  throttling, CI/API quotas, local health, priority, preemption, and
  backpressure.

In progress:

- [SQU-128](https://linear.app/squirtlesquad/issue/SQU-128/epic-resource-based-architecture-location-coupling-audit-resource)
  is the distributed-resource phase-one epic. Its scope is deliberately design
  and audit first: catalog path/socket/worktree/env coupling, classify each
  coupling, and draft the resource model in `documentation/distributed-resources.md`.

Planned:

- Route configuration should start consuming deployment names directly instead
  of raw local paths where cross-deployment work needs a stable address.
- Container workspaces are the portability boundary as well as a security
  boundary: a worker described as image + worktree mount + daemon API endpoint +
  budget + capability token is schedulable away from the local checkout.
- The remaining resource-constraints layers are priority/preemption and provider
  backpressure. Both should keep the current rule: queue rather than fail when a
  constraint is temporary.

Out of scope for now: global marketplace state, a central database, or a
multi-host federation layer before a single remote deployment path has real
usage.

## Provider Surface And GitHub Projects

Why it matters: `agent-team` should not be a Linear-only tool. Teams should be
able to run ticketless, on Linear, or on GitHub Issues/Projects without changing
the worker/reviewer delivery contract.

Recently shipped:

- [SQU-86 / PR #90](https://github.com/agent-team-project/kensho/pull/90)
  introduced `internal/pmprovider` and `[pm].provider`.
- [SQU-96 / PR #92](https://github.com/agent-team-project/kensho/pull/92)
  proved the seam with GitHub Issues/Projects intake, write-back, and a GitHub
  skill alongside the Linear skill.
- The post-release Linear-to-GitHub mirror script
  ([commit `2a3c80c`](https://github.com/agent-team-project/kensho/commit/2a3c80c))
  opened the parallel-run window by mirroring open SQU tickets to GitHub
  Projects.
- `agent-team ticket create|update|comment|close` now exposes provider-backed
  ticket actions through the CLI for Linear and GitHub.
- This repository completed the cutover: `[pm].provider = "github"`, GitHub
  issues/epics and Project status are canonical, and Linear is historical
  context rather than a second planning source.

In progress / planned:

- Keep provider-specific differences behind skills and provider adapters so
  worker/reviewer prompts can stay provider-aware without becoming
  provider-coupled.

Out of scope for now: trying to support every PM system before GitHub Projects
has run as the primary board for this repo.

## Operability

Why it matters: autonomous delivery is only useful if operators can inspect,
recover, release, and repair the system without reverse-engineering logs or
agent prompts.

Recently shipped:

- [SQU-97 / PR #97](https://github.com/agent-team-project/kensho/pull/97)
  made `inbox check` and channel startup commands durable across daemon-routed
  dispatch paths for both Claude and Codex adapters.
- [SQU-83 / PR #86](https://github.com/agent-team-project/kensho/pull/86)
  added resume visibility: counts, last activity, progression hints, and
  incarnation timelines.
- The VitePress developer docs and ReadTheDocs publishing path landed through
  the public-docs arc, including the rendering fixes in
  [PR #111](https://github.com/agent-team-project/kensho/pull/111) and the
  docs refresh in [PR #112](https://github.com/agent-team-project/kensho/pull/112).
- [SQU-126 / PR #126](https://github.com/agent-team-project/kensho/pull/126)
  added local cross-team feedback delivery over target daemon sockets, so
  feedback routing can cross team boundaries without treating a filesystem path
  as the long-term resource identity.
- The daemon now serves an embedded local `/ui` dashboard over its loopback API.
  The static shell loads unauthenticated, while data requests use bearer tokens.
- `agent-team ui` now ships a tested read-only terminal Overview and `--once`
  snapshot over the shared typed daemon client. The frontend team and pipeline
  own later parity slices and the eventual clean web removal.
- Report jobs now have an explicit artifact contract:
  `agent-team job create --kind report --deliverable report:<path>`.
- The five-step `release` pipeline is live: prepare, verify, manual approve,
  ship/tag/assets, and capped comms announcement are distinct owners and gates.

In progress:

- GH-153 continues the terminal-interface parity/cutover work after the shipped
  Overview slice. The daemon HTTP/JSON API remains the shared control plane.

Planned:

- Resource backpressure and priority/preemption should surface through the same
  operator-first queue, health, and budget views rather than hidden runtime
  retries.
- Diagnostics should continue to explain next actions, not only failures. That
  guardrail applies especially to sandbox denial, authority denial, and
  provider throttling.

Out of scope for now: fully automatic releases without a manual gate, or repair
commands that mutate shared state without a dry-run path.

## Self-Improving Loops

Why it matters: the experiment is not "agents freely rewrite themselves." It is
a disciplined loop where operating evidence becomes ordinary tickets, ordinary
tickets become ordinary PRs, independent reviewers check the PRs, and a manual
gate decides what lands.

Recently shipped:

- v0.4.0 shipped the feedback, debt-sweep, and harness-review loops. The first
  auditor sweep produced the remediated queue/events, `--commands`, and
  quarantine refactors; the first harness review produced
  [SQU-97 / PR #97](https://github.com/agent-team-project/kensho/pull/97).
- The live experiment is documented in
  [`docs/experiment.md`](../experiment.md), including the current eight-team,
  eleven-schedule, seven-pipeline topology and evidence-to-issue-to-PR path.
- The Codex docs team runs a durable freshness pipeline every six hours in this
  repo. Research reconciliation/evidence audit and frontend reconciliation are
  also declared loops rather than informal manager habits.

In progress:

- GH-228 remains the rolling docs-freshness evidence ledger; release PRs fold
  current findings into public docs rather than copying old counts.

Planned:

- Keep the loop evidence-backed and small: feedback triage files or folds
  focused tickets, auditors file at most three debt findings per sweep, harness
  review turns repeated bounce classes into prompt/skill/pipeline fixes, and
  docs freshness either fixes stale docs directly or files one ranked ticket.
- As community input grows, public-facing triage and comms loops should adopt
  the reader/actor split from the security model before they gain privileged
  outward actions.

Out of scope for now: bypassing review because a change came from a scheduled
loop, or treating broad "make it better" sweeps as acceptable work items.

## Design Guardrails

Use these when evaluating feature ideas:

1. Keep the repo filesystem the source of truth.
2. Prefer explicit commands over hidden automation for destructive actions.
3. Preserve dry-runs.
4. Scope to job/team when possible.
5. Keep global commands for ambiguous ownership.
6. Do not require a database.
7. Avoid runtime-specific assumptions in new orchestration concepts.
8. Keep templates editable and understandable.
9. Let diagnostics explain next actions.
10. Add tests at the layer where the behavior is promised.
