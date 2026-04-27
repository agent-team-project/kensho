# agent-squad — Product Vision

## What it is

A CLI that vendors a reusable **software-engineering agent squad** into any repo — a `ticket-manager`, persistent feature/domain `manager`s, ephemeral `worker`s, and supporting skills (`linear`, `pull-request`, `assign-worker`).

Run `agent-squad init` in a repo, edit `.agent_squad/config.toml`, and you have a Claude Code-driven team that can triage Linear tickets and ship PRs against that repo. The squad's prompts live in your repo, in `.agent_squad/`, where you can read them, edit them, and commit them like any other code.

## The frame

Think of it as: *Claude Code's general-purpose coding agent, wrapped in a workflow that makes it act like a software-engineering team with product management and version control built in.* Humans interact with Linear and PRs the way they already do; the squad handles the space between.

The squad is an **execution engine**: feed it a Linear ticket and it produces a reviewable PR. Feed it a domain and it holds context across sessions, dispatches workers, and tracks progress against goals.

## Squad shape

Three roles, two of them new in v1:

| Role | Lifetime | Owns |
|---|---|---|
| **`ticket-manager`** | Ephemeral (per session) | The Linear board: triage, route, comment, create. Picks the right project, applies labels, decides state transitions. |
| **`manager`** | **Persistent** (across sessions) | One scope — a feature, an initiative, an ongoing responsibility. Holds working memory in `.agent_squad/managers/<slug>/`, dispatches workers within scope, tracks goals. |
| **`worker`** | Ephemeral (per ticket) | One ticket → one PR, in an isolated git worktree. |

The manager role is what makes the squad more than a ticket-execution loop. Workers are stateless by design (each spawn gets a fresh worktree); managers are the squad's long-term memory for a domain.

## Why a CLI, not a Claude Code plugin

Earlier prototypes shipped as a Claude Code marketplace plugin. We dropped that for three reasons:

1. **Inspectability.** Plugin content lives at `~/.claude/plugins/cache/...` — invisible to the consumer's grep, code review, and CLAUDE.md. The CLI vendors prompts into the consumer's repo, where they're first-class.
2. **Iteration.** Editing a plugin prompt requires `/plugin marketplace update && /reload-plugins`. Editing a vendored file is just an edit.
3. **Remote execution.** A scheduled or containerized agent runner needs prompts that ship with the repo, not a plugin install step.

The CLI uses Claude Code's plugin discovery as the *activation mechanism* (it generates a thin `.claude-plugin/` shim pointing at `.agent_squad/`) — but the source of truth lives in your repo, not in a global cache.

## Who it's for

**V1 audience**: Phoebe teammates. Small startup. Multiple Linear projects. Every early user is someone we can walk through setup in person, so we trade onboarding polish for speed.

**Aspirational**: open source — any Claude Code user with a Linear workspace. This constrains v1: no Phoebe-specific defaults leak into the template (every org/team/project ID lives in the consumer's TOML, not the bundled template). But we don't build for strangers yet.

## What success looks like (v1)

**One primary milestone: self-dogfooding.**

agent-squad uses its own CLI to vendor itself into this repo and works tickets on the squirtlesquad Linear project — closing at least one ticket end-to-end via a worker-opened PR, dispatched through a manager scope.

**Deferred to v1.1+: coral canary.** The Q2 config pattern was proven end-to-end via self-dogfooding. What's left for coral is distribution and the repo-specific touchpoints — separate concerns from "does the pattern work."

## Customization as a principle

agent-squad ships a *template* of a software-engineering workflow. The template is opinionated so it works out of the box. Every part is meant to be customizable and extendable:

- **Scalars and IDs.** Team IDs, project UUIDs, labels, ticket prefixes, worktree paths, branch prefixes — all live in the consumer's `.agent_squad/config.toml`.
- **Prompts.** `.agent_squad/agents/`, `.agent_squad/skills/`, `.agent_squad/managers/<slug>/CLAUDE.md` are checked into the consumer's repo. Edit them like any other code; commit them like any other code.
- **Manager scopes.** Adding a new manager scope is `agent-squad add manager <slug>` — scaffolds a CLAUDE.md, ready to fill in.
- **New capabilities.** Consumer repos can add entirely new agents and skills under `.agent_squad/` alongside the template's. The squad is a starting template, not a closed system.

Upgrades from a newer CLI version are handled by `agent-squad sync` (v0.2 ships diff-aware merge; v0.1 says "rerun init --force, resolve with git").

## Quality & architecture principles

The squad is agents running agents, and prompts are code. Sloppy compounds fast.

- **Minimal surface area.** Every agent, skill, and config key has a reason to exist. Delete-first instinct. Shorter prompts beat longer ones unless the length buys something concrete.
- **One responsibility per component.** Agents and skills do one thing well. Managers own one scope.
- **Strong boundaries.** CLI ↔ template ↔ consumer-vendored copy ↔ consumer-edited extensions. Each layer is clearly delineated.
- **Explicit over clever.** Hardcoded values are fine when the alternative is a knob only one consumer ever turns. Add parameters when there's a second real caller.
- **No half-finished state.** Each milestone exits cleanly or we haven't finished it.
- **Inspectability is a feature.** Vendored prompts, resolved config, and agent hierarchies should be readable by a human in seconds. `agent-squad doctor` makes this concrete.
- **Own your dependencies.** CLI runtime: Python ≥3.11, stdlib only (zero third-party deps). Squad runtime: Claude Code, bash, `curl`, `jq`, `python3`, `gh`, the Linear API. No hidden toolchain assumptions.

## Future: a runner alongside the CLI

The CLI is local-first by design — it scaffolds and validates a vendored squad. A separate **runner** is the v1.1+ direction for remote execution: a long-lived service that supervises workers across repos, watches PRs for review comments, and routes Linear events to manager scopes.

The CLI and the runner are different programs. The CLI is small, file-IO bound, Python-and-stdlib. The runner is concurrency-heavy, long-lived, and is a strong candidate for Go when we build it. Splitting the two keeps each tool the right size; until the runner exists, the CLI is the entire surface area.

## Non-goals (v1)

- No PM tool other than Linear. Jira/GitHub Issues adapters are v2+.
- No PR-review-comment polling loop. Humans review; if they want the worker to address feedback they re-invoke manually. (Tracked as BENCH-209; becomes v1.1.)
- No reviewer subagent, no cross-ticket dependency scheduling. One level of hierarchy below managers.
- User auth only. V1 uses each user's personal Linear API key. Bot/OAuth tokens are v1.1+.
- No remote execution. Local Claude Code only. The runner is v1.1+.
- No public CLI distribution (PyPI, brew). `uvx --from git+...` works today; PyPI listing is v1.1+ once the API stabilizes.
- No polished onboarding docs for strangers.

## Timeline & quality

Open-ended. Quality over speed. The self-dogfooding milestone is the forcing function, not a calendar.

## Why this shape

Three observations drove v1:

1. **Reusable skills is a thinner story than it looks.** Coral's existing skills had hardcoded IDs and team names woven through prompts. The value isn't the skill files — it's the *orchestration pattern* and the pluggable surfaces.
2. **Plugin distribution hides the prompts.** Once we'd built it as a marketplace plugin, the cost of "I can't see what the agent is reading" became obvious. Vendoring is the fix.
3. **Persistent managers are what missing-from-coral.** Coral has triage and execution. It doesn't have a persistent owner for an ongoing initiative. That's the gap the manager role fills.
