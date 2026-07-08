---
name: verify
description: Run declared deterministic gates for a pipeline job in a temporary worktree and write machine-readable evidence before LLM review.
---

# Deterministic verification

Use this skill from the `verifier` agent. It checks out the worker commit in a temporary detached worktree, runs declared gates with streamed progress, records job gate results, and writes evidence under `target/agent-evidence/`.

## Command

```sh
"$AGENT_TEAM_ROOT"/skills/verify/scripts/verify.sh --complete-step
```

Useful flags:

```sh
verify.sh [--job JOB_ID] [--repo REPO_ROOT] [--branch REF] [--commit SHA]
          [--gates-file PATH] [--evidence-dir PATH]
          [--no-record-gates] [--complete-step] [--keep-worktree]
```

## Declaring Gates

Prefer declaring gate commands in the verifier pipeline step instructions with a fenced block:

````toml
instructions = """
Run the verify skill.

```agent-team-verify-gates
gofmt-check :: test -z "$(gofmt -l .)"
go-vet :: go vet ./...
go-test :: go test ./...
```
"""
````

Each non-empty line is either `name :: command` or a bare command. Bare commands get generated names. Commands run from the temporary checkout root with Bash.

If no gate block is found and the source repo contains `go.mod`, the runner falls back to `gofmt-check`, `go-vet`, and `go-test`.

## Evidence

The runner writes:

- `target/agent-evidence/<job>.json` - schema version, source commit, gate statuses, timings, log refs, and summary.
- `target/agent-evidence/<job>.summary.md` - short human summary.
- `target/agent-evidence/logs/<job>/<gate>.log` - full combined stdout/stderr for each gate.

The verifier does not edit product files. It writes evidence artifacts and temporary checkout state only.
