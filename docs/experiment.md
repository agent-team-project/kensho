# The Self-Improving Configuration

This is the experiment running in this repository as of 2026-07-12. It is not
a claim that agents should freely rewrite their own harness. It is narrower:
the repo declares agent teams, schedules, budgets, and gates in files; those
agents observe the system while they work; observations become ordinary tickets;
ordinary tickets become ordinary PRs; reviewers and a manual merge gate decide
what lands.

The useful test is whether a skeptical engineer can verify each claim. The
primary sources are:

- `.agent_team/instances.toml` for the live team, instance, schedule, pipeline,
  and budget declarations.
- `documentation/operating-model.md` for the field-tested gate and pipeline
  discipline.
- `documentation/feedback-channel.md` for the feedback capture and triage
  contract.
- `documentation/resource-constraints.md` for the budget economy and authority
  model.
- `CHANGELOG.md` for the release-level record.
- GitHub issues, Projects state, and merged PRs for the actual loops that fired.

## What "Self-Improving" Means Here

The system improves itself by turning operating evidence into reviewed changes
to this repo. It does not skip the normal software delivery path.

The loop is:

1. Evidence is produced by work already happening: feedback submissions,
   repeated review bounces, failure signatures, queue behavior, watchdog
   events, or auditor measurements.
2. A scheduled or board-triggered agent turns that evidence into a ticket, a
   PR, or a review finding.
3. A worker implements the ticket on an isolated branch.
4. An adversarial reviewer checks the content against the ticket.
5. A manual approve step decides whether the PR is merged.

That last step is the safety boundary — held by the manager agent, not a
human. Humans set direction; the discipline comes from separation of minds:
the agent that implements a change never reviews it, and the agent that
reviews it never merges it. No change is written, reviewed, and merged by the
same context.

## The Live Topology

The repo currently declares eight teams, eleven schedules, and seven pipelines.

| Team | What it owns | Running pieces |
| --- | --- | --- |
| `delivery` | Normal ticket-to-PR delivery. | `manager`, `ticket-manager`, worker/verifier/reviewer pools, the `ticket_to_pr` pipeline, and the 2h `feedback-triage` schedule. |
| `platform` | Framework infrastructure work such as provider seams, provenance, scoping, and resource constraints. | A separate worker/verifier/reviewer pool and `platform_ticket_to_pr` pipeline triggered by `platform.ticket`. |
| `quality` | Proactive architecture, tech-debt, harness auditing, org review, sentinel checks, and product verification. | Scheduled `debt-auditor`, `harness-reviewer`, `org-review`, `sentinel`, and `product-verifier` loops. |
| `pr` | Public voice and outward communication. | Daily `comms` digest schedule for shipped-work digests, release announcements, and community intake. |
| `docs` | Documentation authoring and freshness. | Codex `docs-writer`, the durable `docs_freshness` audit pipeline, and a 6h schedule. |
| `release` | The manual release train. | `release-worker`, `manager`, and `comms` run prepare, verify, approve, ship, and announce as separate gated steps. |
| `research` | Preregistered empirical software studies. | Research manager/worker/verifier/reviewer/auditor seats, two study pipelines, and reconciliation/evidence-audit schedules. |
| `frontend` | Terminal-first operator interface and clean web cutover. | Frontend manager/worker/verifier/reviewer seats, a gated slice pipeline, a 2h reconciliation schedule, and the shipped read-only TUI Overview. |

The eleven schedules are:

| Schedule | Cadence | Owning team | What it does |
| --- | --- | --- | --- |
| `feedback-triage` | Every 2 hours. | `delivery` | Cluster local `agent-team feedback submit` reports plus system pain signals; file, fold, or dismiss tickets. |
| `debt-sweep` | Every 4 hours. | `quality` | Audit one subsystem and file at most three evidence-backed tech-debt tickets. |
| `harness-review` | Every 3 hours. | `quality` | Inspect bounce classes, feedback trends, and failure patterns; propose prompt, skill, or pipeline-instruction tickets. |
| `org-review` | Every 12 hours. | `quality` | Read outcomes, spend, concurrency/capacity, cycle-time, bounces, and feedback trends; propose strategic process/topology/prompt/budget tickets. |
| `sentinel` | Every 3 hours. | `quality` | Check post-merge public surfaces and submit incident feedback when they fail. |
| `product-verify` | Every 4 hours. | `quality` | Compare daemon UI data with CLI ground truth and file capped product feedback. |
| `discord-digest` | Every 24 hours. | `pr` | Draft or publish a shipped-work digest through the sanctioned comms path. |
| `docs-freshness` | Every 6 hours. | `docs` | Audit docs against the shipped binary, latest release, repo identity, and quickstart; fold findings into the existing docs issue. |
| `research-reconcile` | Every 2 hours. | `research` | Reconcile the active study portfolio and dispatch only graph-safe work. |
| `research-evidence-audit` | Every 84 hours. | `research` | Check terminal studies against preregistration digests, durable gates, and artifact evidence. |
| `frontend-reconcile` | Every 2 hours. | `frontend` | Reconcile accepted TUI slices against the advisor-backed parity contract. |

The core self-improvement path is mostly carried by the board-driven delivery
pipeline plus the feedback, debt, harness, org-review, sentinel,
product-verifier, and docs-freshness loops:

| Loop | Trigger or cadence | What it is allowed to do |
| --- | --- | --- |
| Board-driven delivery | A GitHub Project item enters the configured `Ready for Agent` column. | Run implement -> verify -> review -> manual approve. Workers open PRs; verifiers run deterministic gates and write evidence; reviewers report verdicts; the manual gate decides merge. |
| Feedback triage | Every 2 hours. | Cluster local `agent-team feedback submit` reports plus system pain signals; file, fold, or dismiss tickets. Filed issues land outside the dispatch column. |
| Debt sweep | Every 4 hours. | Audit one subsystem and file at most three evidence-backed tech-debt tickets. |
| Harness review | Every 3 hours. | Inspect bounce classes, feedback trends, and failure patterns; propose prompt, skill, or pipeline-instruction issues. |
| Org review | Every 12 hours. | Inspect outcomes, per-epic spend, review quality, capacity utilization, cycle-time, and feedback trends; propose at most a few strategic improvements. |
| Sentinel | Every 3 hours. | Check main CI, docs rendering, release assets, and repo metadata; submit incident feedback when public surfaces fail. |
| Product verifier | Every 4 hours. | Compare daemon UI projections with CLI ground truth and file capped bug, friction, or idea feedback. |
| Docs freshness | Every 6 hours. | Check docs against the live CLI, release record, repo identity, links, and quickstart; fold findings into GH-228. |
| Research | Reconcile every 2 hours; evidence audit every 84 hours. | Dispatch preregistered, graph-safe studies and block unsupported verdicts without blocking product releases. |
| Frontend | Reconcile every 2 hours. | Advance only accepted TUI slices through deterministic terminal verification and independent review. |

The PR team's 24h Discord digest schedule is part of the current topology, but
it is only an opportunity for outward communication. The shared delivery gate
allows at most one successful Discord post per rolling 24 hours across digest,
release, and manual paths; no meaningful material means no post.

### The terminal interface is a shipped slice, not a completed cutover

`agent-team ui` now opens a tested, keyboard-complete, read-only Overview over
the same typed daemon client used by CLI commands. `agent-team ui --once` makes
that view scriptable. The frontend pipeline and reconciliation schedule exist
because this is only the first accepted slice: additional views, mutations,
full parity, and removal of the embedded web dashboard still require their own
acceptance, verification, review, and manual integration gates.

## The Delivery Loop

The delivery pipeline is deliberately conservative:

```toml
[pipelines.ticket_to_pr]
trigger.event = "ticket.status_changed"
trigger.match.status = "Ready for Agent"
auto_advance = true
redispatch_on_reentry = false

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
workspace = "worktree"
timeout = "45m"
token_budget = "40M"
max_attempts = 1

[[pipelines.ticket_to_pr.steps]]
id = "verify"
target = "verifier"
after = ["implement"]
workspace = "repo"
timeout = "20m"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "reviewer"
after = ["verify"]
timeout = "30m"

[[pipelines.ticket_to_pr.steps]]
id = "approve"
target = "manager"
after = ["review"]
gate = "manual"
```

The board is the dispatch control plane. Moving a GitHub Project item into
`Ready for Agent` is the intentional handoff. The worker gets a fresh worktree and opens a
PR; the verifier runs deterministic gates and writes evidence; the reviewer is
instructed to judge content only:
acceptance criteria, tests, unrelated edits, and dead code. The approve step is
manual because merge judgment is the place where discretion belongs.

`max_attempts = 1` on implementation is important. A runtime can hang after it
has opened a PR; automatically retrying that step could open a duplicate PR.
The manager or operator re-dispatches after inspecting the artifact.

## Feedback Becomes Work

The feedback path is intentionally low-friction for agents and conservative at
the project boundary.

Any agent can run:

```sh
agent-team feedback submit "<one sentence>"
```

The store records the instance, agent, job, ticket, pipeline step, runtime, and
build identity. Every 2 hours, `feedback-triage` clusters unresolved reports
and either materializes a GitHub issue, folds evidence into an existing issue,
or dismisses it with a reason. Non-local or framework-routed tickets are capped
per sweep. Materialized tickets land in Backlog, not in the dispatch column.

Examples from this repo:

| Evidence | Ticket | Result |
| --- | --- | --- |
| `job show --json` emitted capitalized step keys that forced monitor scripts to special-case one command. | [SQU-87](https://linear.app/squirtlesquad/issue/SQU-87/feedback-normalize-job-show-json-step-keys) | [PR #87](https://github.com/agent-team-project/kensho/pull/87) normalized step JSON keys and added guard coverage. |
| Duplicate job ids and event payload quoting produced supervisor friction. | [SQU-88](https://linear.app/squirtlesquad/issue/SQU-88/feedback-pipeline-run-duplicate-job-id-should-report-conflict) and [SQU-89](https://linear.app/squirtlesquad/issue/SQU-89/feedback-event-publish-should-accept-keyvalue-payload-shorthand) | [PR #88](https://github.com/agent-team-project/kensho/pull/88) clarified duplicate-id output and added `key=value` event payload shorthand. |
| Topology-shape tests broke whenever the bundled template gained a team or instance. | [SQU-98](https://linear.app/squirtlesquad/issue/SQU-98/feedback-decouple-topology-tests-from-bundled-template-shape) | [PR #98](https://github.com/agent-team-project/kensho/pull/98) pinned tests to explicit topology fixtures. |

That is the desired path: a small operational complaint becomes a ticket with
acceptance criteria, then a normal PR with tests.

## Auditors File Debt, Not Fixes

The quality team has a scheduled `debt-sweep` loop. The auditor is read-only
with respect to code: it sweeps one subsystem, gathers hard evidence, and files
at most three Backlog tickets. This keeps the quality loop from becoming an
unreviewed rewrite loop.

The first auditor sweep produced the "auditor trilogy":

| Ticket | Evidence class | Shipped remediation |
| --- | --- | --- |
| [SQU-93](https://linear.app/squirtlesquad/issue/SQU-93/debtcli-split-job-queue-subcommands-out-of-jobgo) | `internal/cli/job.go` carried queue/event commands inside a very large hot file. | [PR #91](https://github.com/agent-team-project/kensho/pull/91) split job queue commands out. |
| [SQU-94](https://linear.app/squirtlesquad/issue/SQU-94/debtcli-centralize-commands-validation-for-jobteampipeline) | `--commands` validation was repeated hundreds of times across CLI families. | [PR #93](https://github.com/agent-team-project/kensho/pull/93) centralized validation. |
| [SQU-95](https://linear.app/squirtlesquad/issue/SQU-95/debtcli-consolidate-queueoutbox-quarantine-command-plumbing) | Queue and outbox quarantine commands had near-parallel plumbing. | [PR #95](https://github.com/agent-team-project/kensho/pull/95) consolidated quarantine helpers. |

The pattern matters more than the specific refactors: the auditor measured,
filed, and stopped. Delivery agents did the implementation through the normal
pipeline.

## Harness Review Tightens the Harness

The scheduled `harness-review` loop looks at the steering surfaces themselves:
agent prompts, skills, pipeline instructions, repeated bounces, feedback
clusters, and failure patterns. It files `harness` tickets, again to Backlog.

The first run found a real command-surface mismatch. Agent prompts told Codex
adapter jobs to run `inbox check` and `channel.sh`, but the short commands were
not on PATH. The harness review aggregated 16 related feedback reports across
worker, reviewer, auditor, and platform jobs and filed
[SQU-97](https://linear.app/squirtlesquad/issue/SQU-97/harness-make-inboxchannel-startup-commands-runnable-in-codex-adapter).
[PR #97](https://github.com/agent-team-project/kensho/pull/97) then made
the startup-command shims durable for daemon-routed dispatches.

The review loop also demonstrated why the adversarial reviewer exists. SQU-97
was bounced once because the first implementation put shims in a CLI-lifetime
temporary directory; daemon-dispatched children inherited a PATH into a
directory that was deleted when the CLI returned. The bounce forced the fix into
the instance state dir, where the child process could actually use it.

That is a useful self-improvement result: the harness found a harness bug, and
the review gate stopped the first plausible-but-wrong repair.

## Org Review Turns Outcomes Into Strategy

The scheduled `org-review` loop is the strategic counterpart to harness review.
Every 12 hours, an ephemeral manager reads the outcomes ledger, per-epic
spend, bounce classes, effective versus peak concurrency, capacity utilization,
cycle time, feedback trends, and failure signals. It proposes at most a few
evidence-backed tickets for process, topology, prompt, or budget changes.

The loop follows `documentation/metrics-methodology.md`: ground truth over
proxy counts, quality-inclusive interpretation, difficulty-normalized
comparisons, and a strict metrics firewall. Agents being measured do not receive
their own scores or targets in prompts; org review turns observations into
reviewed tickets, and those tickets still go through the normal delivery path.
The v1 trigger is scheduled; project or epic completion should become a second
trigger when that event path is available.

## The Budget Economy

Resource limits are now declared in topology rather than left as supervisor
folklore. The live caps are intentionally generous and aimed at runaway
protection:

```toml
[budgets.delivery]
tokens_per_day = 10_000_000_000
jobs_in_flight = 20

[budgets.platform]
tokens_per_day = 10_000_000_000
jobs_in_flight = 16

[budgets.quality]
tokens_per_day = 3_000_000_000
jobs_in_flight = 8

[budgets.research]
tokens_per_day = 100_000_000_000
jobs_in_flight = 16

[budgets.frontend]
tokens_per_day = 50_000_000_000
jobs_in_flight = 8
```

Instances and pipeline steps also carry per-run token and wall-clock allowances.
They are intentionally smaller than the team ceilings and differ by work class;
for example, ordinary release/delivery work and terminal or research slices do
not share one hard-coded allowance.

The budget work landed as a sequence:

| Ticket | What changed | PR |
| --- | --- | --- |
| [SQU-103](https://linear.app/squirtlesquad/issue/SQU-103/budgets-per-team-caps-with-admission-control-squ-91-phase-1) | Team caps and admission control. Over-budget dispatches queue with `reason=budget_exhausted`; they do not fail. | [#102](https://github.com/agent-team-project/kensho/pull/102) |
| [SQU-104](https://linear.app/squirtlesquad/issue/SQU-104/budgets-per-jobagent-allowances-soft-levels-with-mailbox-reminders-squ) | Per-job and per-agent allowances, soft `budget_notice` events, mailbox reminders, and `budget status --job/--self`. | [#103](https://github.com/agent-team-project/kensho/pull/103) |
| [SQU-105](https://linear.app/squirtlesquad/issue/SQU-105/budgets-hard-cutoffs-usage-watchdog-kill-at-the-hard-line-squ-91-p2b) | Opt-in hard cutoffs that kill runaway jobs with watchdog semantics. | [#104](https://github.com/agent-team-project/kensho/pull/104) |
| [SQU-106](https://linear.app/squirtlesquad/issue/SQU-106/budgets-tree-invariant-allocation-reserve-vs-oversubscribe-squ-91-p2c) | `reserve` vs `oversubscribe` allocation. Reserve mode enforces the tree invariant: outstanding child promises plus consumed budget cannot exceed the parent allocation. | [#105](https://github.com/agent-team-project/kensho/pull/105) |

The distinction between soft and hard is deliberate. Soft notices make
constraints visible to the agent so it can wrap up or ask for an extension.
Hard cutoffs are for runaway protection. Reserve allocation is stricter than
oversubscribe allocation: it debits promised child allowance immediately, while
oversubscribe mode gates on actual consumption and exposes overcommitment in
status output.

SQU-104 and SQU-106 both had review findings in Linear history. SQU-104's first
implementation missed fast runtimes that wrote over-budget usage and exited
before the live watcher ticked; the fix added a final reap sweep. SQU-106's
first implementation released reservations only on the live reaper hook; the
review required release through the terminal finalization path so daemon
restart reconciliation could not leak reserve headroom. Those bounces are part
of the experiment: resource control is only useful if the recovery paths are
also covered.

## Provenance, Scoping, and Authority

The self-improvement loop needs attribution before it can have meaningful
limits. The origin envelope introduced by
[SQU-90 / PR #94](https://github.com/agent-team-project/kensho/pull/94)
stamps resources with project, team, instance, agent, job, trigger, and build
identity. Jobs, queue items, events, lock leases, usage records, and outward
Linear writes carry that context.

Scoping audit mode followed in
[SQU-92 / PR #96](https://github.com/agent-team-project/kensho/pull/96).
Topology can declare resource scope as `machine`, `team`, or `job`, and
per-instance/agent/team authority allowlists describe who should be able to call
which daemon verbs. Audit mode still logs would-be violations for triage;
enforce mode now denies disallowed destructive mutations after recording the
same event.

That is an honest boundary. This is blast-radius control for cooperating local
agents, not a security boundary against a hostile local process. The design
logs before it blocks so the bundled ACLs can be validated against real runs.

## What Agents Decide, and What Humans Decide

Agents decide:

- how to implement an assigned ticket inside the branch they were given
- which tests and local checks to run before handoff
- how to summarize a gate result or review finding
- whether feedback evidence should be filed, folded, or dismissed during a
  triage sweep
- whether a budget notice means "wrap up now" or "ask for an extension"

The manager agent additionally decides — autonomously, in this deployment:

- which tickets to dispatch and in what order
- whether a PR merges (after an independent adversarial review approves)
- whether a repeated bounce means another worker round or a direct fix
- team budgets and their adjustments, justified against observed usage
- when releases cut and what ships in them

Humans decide:

- direction: which epics matter, what the experiment should become
- anything involving accounts, credentials, or money
- and they can override or halt any of the above at any time

What constrains the agents is therefore not human sign-off per change but the
framework's own discipline: separation of minds at every gate, adversarial
review before any merge, budget and authority audit trails on every action,
and sanctioned-automation-only rules for anything outward-facing.

The experiment is useful only because the boundary is sharp. The system can
surface problems, propose fixes, and carry those fixes to a reviewable PR. It
still routes irreversible judgment through humans or explicitly delegated
manager gates.

## How to Verify It Locally

Start with the checked-in state:

```sh
agent-team topology show
agent-team graph delivery --routes
agent-team schedule next --limit 10
agent-team budget status --json
```

Then compare the claims above with:

- `.agent_team/instances.toml`
- `documentation/operating-model.md`
- `documentation/feedback-channel.md`
- `documentation/resource-constraints.md`
- `CHANGELOG.md`
- the linked Linear tickets and merged PRs

If those artifacts diverge from this page, the artifacts win. This document is
a snapshot of the experiment as it is configured on 2026-07-05.
