---
layout: home

hero:
  name: agent-team
  text: Developer documentation
  tagline: Build, run, observe, and recover file-backed teams of LLM agents.
  actions:
    - theme: brand
      text: Start Reading
      link: /getting-started
    - theme: alt
      text: Use Cases
      link: /use-cases/

features:
  - title: File-backed orchestration
    details: Templates, jobs, topology, queues, schedules, and runtime state live under each repo's .agent_team directory.
  - title: Self-improving configuration
    details: This repo dogfoods four declared teams, scheduled feedback and audit loops, budget constraints, and review gates.
  - title: Docker-like CLI control
    details: Start, stop, restart, inspect, monitor, attach, and repair agent instances through a per-repo daemon.
  - title: Durable work units
    details: Jobs connect tickets, agents, instances, queue entries, worktrees, branches, PRs, and pipeline state.
  - title: Operator-first recovery
    details: Health, overview, next, repair, quarantine, and snapshot commands explain what is wrong and which scoped command to run next.
---

## What This Site Covers

This documentation is for developers building, extending, operating, or integrating `agent-team`.

It covers:

- the project model and vocabulary
- the [self-improving configuration](./experiment.md) running in this repository
- template authoring and initialization
- agents, skills, status files, channels, and mailboxes
- the daemon, instance lifecycle, runtime metadata, and logs
- durable jobs, queues, quarantine, pipelines, teams, schedules, and intake
- diagnostics, repair loops, snapshots, and operator workflows
- local development, tests, smoke checks, and contribution expectations
- practical use cases that show how the pieces fit together

Start with [Getting Started](./getting-started.md) if you are installing
`agent-team` into a repo for the first time.

`agent-team` is pre-v1. Treat command shapes and file schemas as active product surface, but still subject to change.
