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

Gate commands receive:

- `AGENT_TEAM_EVIDENCE_DIR`: the run's evidence directory.
- `AGENT_TEAM_GATE_EVIDENCE_DIR`: a per-gate directory for files that should be attached as evidence.
- `AGENT_TEAM_GATE_NAME`: the current gate name.
- `AGENT_TEAM_GATE_LOG`: the gate log path.

Files written under `AGENT_TEAM_GATE_EVIDENCE_DIR` are included as
`evidence_refs` in the verifier JSON and summary. GUI slices can opt into the
bundled visual-QA gate:

```agent-team-verify-gates
visual-qa :: "$AGENT_TEAM_ROOT/skills/visual-qa/scripts/visual_qa.sh" --app-command "npm run dev -- --host 127.0.0.1 --port 4173" --url "http://127.0.0.1:4173"
```

The visual-QA helper captures screenshots only. A vision-capable reviewer must
judge the screenshots before approval.

These default verifier gates are the **smoke** tier declared in
`.agent_team/gates.toml`. Passing them means local smoke passed; it does not
prove acceptance or release readiness. Acceptance and release claims must be
checked against the tier config with explicit evidence, for example:

```sh
python3 "${AGENT_TEAM_ROOT:-.agent_team}/skills/verify/scripts/validate_gate_tiers.py" --claim release \
  --evidence target/agent-evidence/<job>.release.json
```

A release claim with only smoke evidence must fail.

## Evidence

The runner writes:

- `target/agent-evidence/<job>.json` - schema version, source commit, gate statuses, timings, log refs, and summary.
- `target/agent-evidence/<job>.summary.md` - short human summary.
- `target/agent-evidence/logs/<job>/<gate>.log` - full combined stdout/stderr for each gate.

The verifier does not edit product files. It writes evidence artifacts and temporary checkout state only.

## Base-broken discriminator

After a gate fails, the runner resolves the remote default branch through
`refs/remotes/origin/HEAD`, computes its merge-base with the worker commit, and
reruns only that failed gate in a detached checkout of the merge-base. For a
plain `go test <packages>` gate, the rerun is narrowed to the failed packages
and anchored top-level test names parsed from the head log. A non-zero base
run counts as reproduction only when at least one of those named tests fails
again, so a package missing at the base cannot become a false infra result.
For every other failed gate (including a Go gate with no parsed test name), the
base run counts as reproduction only when its exit code and normalized failure
signature exactly match the head run. A missing command or file at the base
therefore cannot turn a different head-only failure into `base-broken`.

- If the scoped gate also fails at the merge-base, the result records
  `class: infra` and `signature: base-broken`. The pipeline's semantic
  `base_broken` infra signature makes the durable job gate classification agree.
- If the scoped gate passes at the merge-base, the original failure signature
  is preserved, so existing signature-based classification is unchanged.
- If the remote default branch, merge-base, or base checkout is unavailable,
  the original failure signature is preserved and the evidence contains both
  an `unavailable` comparison result and a warning.

Every failed gate receives a `base_comparison` object in the JSON evidence. A
completed comparison includes the default-branch ref, merge-base commit,
scoped command, reproduction basis, head/base exit statuses and signatures,
duration, and base log path. Passing gate records are unchanged and do not
trigger a base checkout.
