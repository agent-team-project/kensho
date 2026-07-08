# Requirements Traceability Matrix

The traceability skill turns the team's file-backed control plane into a
manager handoff report. It answers four questions:

1. What did the SPEC or backlog say should exist?
2. Which durable jobs attempted each requirement?
3. Which gate ledgers or verifier evidence prove the work?
4. Which rows are still pending gaps?

Run it from a repo with an initialized `.agent_team/` directory:

```sh
"$AGENT_TEAM_ROOT"/skills/traceability/scripts/traceability.sh \
  --spec SPEC.md \
  --output target/agent-evidence/traceability.md
```

Use `--json` for dashboards or follow-on gates:

```sh
"$AGENT_TEAM_ROOT"/skills/traceability/scripts/traceability.sh \
  --spec SPEC.md \
  --json \
  --output target/agent-evidence/traceability.json
```

## Inputs

The script is read-only. It inspects:

- Markdown or JSON SPEC/backlog files passed with `--spec` or found at common
  names such as `SPEC.md`, `BACKLOG.md`, `docs/backlog.json`, and
  `documentation/requirements.md`.
- Live job records under `.agent_team/jobs/*.toml`.
- Gate ledgers under `.agent_team/jobs/*.gates.jsonl`.
- Verifier evidence under `target/agent-evidence/*.json`.

Markdown requirements should prefer stable IDs:

```markdown
- [ ] REQ-1: Capture deterministic verifier evidence for delivery jobs.
- [ ] REQ-2: Surface pending gaps explicitly in manager handoff.
```

JSON backlogs can be an array or an object containing `requirements`, `items`,
`work_items`, or `backlog`. `acceptance_criteria` entries become child rows so
criteria can be proven independently.

## Status Model

The output statuses are intentionally small:

- `delivered`: a mapped job has a delivery artifact and passing gate/evidence
  proof.
- `unproven`: a mapped job has a delivery artifact or terminal completion, but
  proof is missing or failed.
- `specified`: a mapped job exists, but it is still queued/running/blocked and
  has no delivery artifact.
- `gap`: no job maps to the requirement, or no SPEC/backlog requirements were
  found.

Every non-`delivered` row contains an explicit `PENDING GAP` reason. With
`--fail-on-gap`, the command exits 1 when any row has a pending gap, which makes
the report usable as a pre-handoff gate.

## Matching Contract

Stable IDs are the primary join key. A requirement like `REQ-17` maps to a job
when that ID appears in the job id, ticket, kickoff, branch, PR metadata, or
step instructions. If a requirement has no explicit ID, the tool falls back to
token overlap between the requirement text and the job text. That fallback is
useful for early drafts, but IDs are the durable contract.

Unmatched jobs are listed separately as "untraced jobs." They are work with no
visible SPEC/backlog row, which is a different gap from a specified requirement
with no job.
