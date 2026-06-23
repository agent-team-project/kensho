# Roadmap Context

This page captures the current product direction so contributors can evaluate whether a change fits.

## Product Direction

The project is moving from "launch an agent" toward a local orchestration layer for teams of agents.

The major direction is:

1. Docker-like instance control.
2. Durable jobs/work units.
3. Job-owned worktree and PR metadata.
4. Reliable persisted queues.
5. Declarative pipelines and teams.
6. External intake from Linear/GitHub/schedules.
7. Rich diagnostics and scoped repair loops.

Most of this is now present in some form. The work is increasingly about making the layers reliable, cohesive, and easy to operate.

## What Is Built

Built or substantially implemented:

- template initialization and template provenance
- bundled default software-engineering team
- direct runtime launch
- daemon binary and daemon-aware CLI
- lifecycle commands
- instance metadata, logs, events, stats, attach
- topology loading and reload
- schedules
- teams
- jobs
- job events
- worktree/branch/PR metadata
- pipelines and ready-step advancement
- persistent queue
- dead-letter retry/drop/prune
- queue doctor and quarantine
- job/team-scoped queue controls
- Linear/GitHub/schedule intake commands
- delivery history, replay, doctor, prune
- overview, next, health, monitor, snapshot, repair
- VitePress developer docs site
- generated CLI reference command and check mode
- topology gallery examples

## What Still Needs Design Care

Areas likely to need continued refinement:

- runtime abstraction beyond the current primary runtime
- richer pipeline semantics without overbuilding a workflow engine
- safer PR merge confirmation for cleanup
- deciding whether to publish generated CLI reference artifacts
- richer docs examples using realistic fake teams
- webhook security hardening and deployment guidance
- template upgrade apply mode
- long-running agent UX and resumability across runtimes

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

## Good Next Feature Areas

High-value follow-up work:

- realistic local demo repo with fake spawner
- richer health policy configuration
- pipeline visualization in docs
- template authoring smoke harness
- `job cleanup` PR merge verification integration
- intake server deployment guide
- runtime profile docs for non-primary runtimes

## Anti-Patterns

Avoid:

- broad repair commands with no dry-run
- queue actions that ignore job/team ownership
- storing durable state only in memory
- commands that require the daemon for read-only inspection when local files are enough
- untested text action hints
- introducing new runtime dependencies for simple parsing or formatting
- burying important state in prompts instead of structured files
