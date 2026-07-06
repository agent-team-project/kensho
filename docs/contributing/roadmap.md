# Roadmap Context

This page is the public direction-of-travel for `agent-team`. It is not a
commitment calendar. The project moves quickly because the repo dogfoods its own
agent fleet, so roadmap entries should stay grounded in evidence:

- the latest release tag and
  [CHANGELOG](https://github.com/agent-team-project/agent-team/blob/main/CHANGELOG.md)
- merged GitHub PRs and commits on `main`
- open Linear epics and their active child tickets
- design sketches under
  [`documentation/`](https://github.com/agent-team-project/agent-team/tree/main/documentation/)

When a thing ships, it moves out of "in progress" and into the relevant
"recently shipped" notes with a release, PR, commit, or design link.

## Recently Shipped Foundation

[v0.4.0](https://github.com/agent-team-project/agent-team/releases/tag/v0.4.0)
is the current release. It shipped the provider seam, GitHub provider support,
three-team topology, feedback/debt/harness loops, provenance envelopes,
authority audit mode, durable startup-command shims, and operator polish called
out in the
[changelog](https://github.com/agent-team-project/agent-team/blob/main/CHANGELOG.md#v040--2026-07-05).

`main` has also moved since that tag. Notable post-release work includes the
docs team and weekly freshness loop, the VitePress/ReadTheDocs site, public
open-source hygiene, team budget allowances and hard cutoffs, reserve vs.
oversubscribe allocation, `env_allow` launch-env filtering, local
cross-deployment feedback delivery, the Linear-to-GitHub Projects mirror script,
and the security/distributed-compute research notes linked below. Those are
merged, but not yet part of a tagged release.

## Security And Isolation

Why it matters: autonomous agents read untrusted text, inherit local filesystem
and environment access, and can call the same CLI an operator can. The project
needs blast-radius control that is stronger than prompt discipline while still
preserving a file-backed, inspectable repo workflow.

Recently shipped:

- [SQU-90 / PR #94](https://github.com/agent-team-project/agent-team/pull/94)
  added the origin envelope so jobs, queue items, events, locks, usage records,
  and outbound Linear writes know their project/team/instance/job owner.
- [SQU-92 / PR #96](https://github.com/agent-team-project/agent-team/pull/96)
  added scoped resources and authority allowlists in audit mode. Enforcement is
  intentionally off until the violation stream proves the default ACLs are
  right.
- [SQU-121 / PR #124](https://github.com/agent-team-project/agent-team/pull/124)
  added `env_allow` so topology can filter per-instance launch environments.
- [`documentation/security-model.md`](https://github.com/agent-team-project/agent-team/blob/main/documentation/security-model.md)
  records the adopted security model: per-instance identity, brokered provider
  secrets, capability-style authorization, reader/actor separation for public
  input, and sandbox tiers.
- The SQU-120 probe verdict in that design doc found that naive Codex
  `workspace-write` on macOS blocks the daemon socket and several worker
  necessities. That makes a sandbox-compatible TCP-loopback API path prerequisite
  for a useful default sandbox.

In progress:

- [SQU-123](https://linear.app/squirtlesquad/issue/SQU-123/security-verb-aware-cli-shims-closed-world-allowlists-at-the-tool)
  is narrowing the CLI tool surface with verb-aware shims. The current work is
  still being reviewed and bounced, so the roadmap should treat it as active,
  not landed.

Planned:

- [SQU-122](https://linear.app/squirtlesquad/issue/SQU-122/security-graduate-authority-audit-to-enforcement-for-destructive-verbs)
  graduates destructive daemon/CLI verbs from audit to enforcement once the
  evidence says the allowlists are stable.
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

- [SQU-103 / PR #102](https://github.com/agent-team-project/agent-team/pull/102)
  added team budget caps and admission control.
- [SQU-104 / PR #103](https://github.com/agent-team-project/agent-team/pull/103)
  added per-job and per-agent allowances plus soft budget notices.
- [SQU-105 / PR #104](https://github.com/agent-team-project/agent-team/pull/104)
  added opt-in hard token cutoffs with watchdog semantics.
- [SQU-106 / PR #105](https://github.com/agent-team-project/agent-team/pull/105)
  added reserve vs. oversubscribe allocation, so outstanding child promises can
  be counted against parent headroom when an operator wants strict reservation.
- [`documentation/resource-constraints.md`](https://github.com/agent-team-project/agent-team/blob/main/documentation/resource-constraints.md)
  now describes the full resource surface: build slots, tokens, provider
  throttling, CI/API quotas, local health, priority, preemption, and
  backpressure.

In progress:

- [SQU-128](https://linear.app/squirtlesquad/issue/SQU-128/epic-resource-based-architecture-location-coupling-audit-resource)
  is the distributed-resource phase-one epic. Its scope is deliberately design
  and audit first: catalog path/socket/worktree/env coupling, classify each
  coupling, and draft the resource model in `documentation/distributed-resources.md`.

Planned:

- [SQU-127](https://linear.app/squirtlesquad/issue/SQU-127/daemon-named-deployment-addressing-registry-over-raw-paths-routes-v2)
  introduces named deployment addressing: routes refer to a deployment name
  whose registry entry can resolve to a Unix socket today or a host/token
  transport later.
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

- [SQU-86 / PR #90](https://github.com/agent-team-project/agent-team/pull/90)
  introduced `internal/pmprovider` and `[pm].provider`.
- [SQU-96 / PR #92](https://github.com/agent-team-project/agent-team/pull/92)
  proved the seam with GitHub Issues/Projects intake, write-back, and a GitHub
  skill alongside the Linear skill.
- The post-release Linear-to-GitHub mirror script
  ([commit `2a3c80c`](https://github.com/agent-team-project/agent-team/commit/2a3c80c))
  opened the parallel-run window by mirroring open SQU tickets to GitHub
  Projects while keeping Linear as source of truth.

In progress:

- [SQU-114](https://linear.app/squirtlesquad/issue/SQU-114/pm-migrate-linear-github-projects-deferred-until-post-public-stability)
  tracks the actual migration. Current state: the GitHub board exists, open SQU
  tickets are mirrored, and Linear remains authoritative until one real
  GitHub-provider dispatch cycle is validated and the repo config is flipped.

Planned:

- Cut over `[pm].provider` to `github`, update intake routing and feedback
  destinations, and retire Linear only after the public repo has enough stable
  reps on the GitHub path.
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

- [SQU-97 / PR #97](https://github.com/agent-team-project/agent-team/pull/97)
  made `inbox check` and channel startup commands durable across daemon-routed
  dispatch paths for both Claude and Codex adapters.
- [SQU-83 / PR #86](https://github.com/agent-team-project/agent-team/pull/86)
  added resume visibility: counts, last activity, progression hints, and
  incarnation timelines.
- The VitePress developer docs and ReadTheDocs publishing path landed through
  the public-docs arc, including the rendering fixes in
  [PR #111](https://github.com/agent-team-project/agent-team/pull/111) and the
  docs refresh in [PR #112](https://github.com/agent-team-project/agent-team/pull/112).
- [SQU-126 / PR #126](https://github.com/agent-team-project/agent-team/pull/126)
  added local cross-team feedback delivery over target daemon sockets, so
  feedback routing can cross team boundaries without treating a filesystem path
  as the long-term resource identity.

In progress:

- There is no single operability epic active at the time of this update. The
  active work is spread across security shims, distributed addressing, provider
  migration, and docs freshness.

Planned:

- [SQU-113](https://linear.app/squirtlesquad/issue/SQU-113/release-a-release-pipeline-with-gates-changelog-docs-tag-verify)
  formalizes the manual release cycle as a gated topology pipeline: prepare
  changelog/docs, review, manual approve, tag/release/verify, and announce.
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
  [SQU-97 / PR #97](https://github.com/agent-team-project/agent-team/pull/97).
- The live experiment is documented in
  [`docs/experiment.md`](../experiment.md), including the current five-team
  topology and the evidence-to-ticket-to-PR path.
- The docs team and weekly docs-freshness schedule are now part of the bundled
  topology, and the docs-freshness skill explicitly owns this roadmap as a
  living document.

In progress:

- [SQU-129](https://linear.app/squirtlesquad/issue/SQU-129/docs-bring-the-public-roadmap-current-surface-it)
  is the current docs-freshness repair for this page.

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
