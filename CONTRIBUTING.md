# Contributing

Questions or discussion? Join the [Discord](https://discord.gg/sBrPh3Amc) —
agents post meaningful shipped-work digests there no more than once in any
rolling 24-hour window, and humans read everything.

`agent-team` is an actively dogfooded, pre-v1 project. External contributions
are welcome when they help clarify the tool, fix concrete bugs, improve
documentation, or propose focused changes that fit the current direction.

## Contribution Posture

External pull requests are proposals. Maintainers review, revise when needed,
and make the final merge decision. Please do not assume that an opened PR will
land as-is, even when the implementation is sound.

Most project work is ticket-driven. If a change is not already tied to a public
issue or maintainer request, open a discussion or issue first with:

- the problem you are trying to solve
- the user-facing impact
- the smallest change that would address it
- any compatibility or migration risk

Small documentation typo fixes can go straight to a PR.

## Development Loop

The repo conventions live in `AGENTS.md` and `CLAUDE.md`. For normal local
validation, start with:

```sh
go test ./...
go build -o bin/agent-team ./cmd/agent-team
python3 scripts/ci/smoke_init.py bin/agent-team
npm run docs:build
```

Broaden validation when touching daemon behavior, topology, queues, jobs, or
runtime launch paths. Keep runtime dependencies minimal and prefer file-backed
structured state.

## Pull Requests

Before opening a PR:

- keep the change focused on one responsibility
- update docs when command behavior, file formats, or workflows change
- avoid committing secrets, `.env` files, build output, or local runtime state
- include the relevant ticket or issue link when one exists
- describe what changed and how you validated it

Maintainers may rewrite, split, or close proposals that do not fit the current
roadmap.

## Feedback Channel

If you are not ready to open an issue or PR, send concise project feedback to
`agentteaminbox@gmail.com`. That address feeds the maintainer/agent feedback
channel for triage. Do not use it for vulnerability reports; follow
`SECURITY.md` for security issues.
