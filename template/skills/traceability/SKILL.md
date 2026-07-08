---
name: traceability
description: Build a live requirements traceability matrix from SPEC/backlog requirements, job records, gate ledgers, and verifier evidence.
---

# Requirements traceability

Use this skill when a manager needs to make a handoff checkable: what was
specified, which jobs attempted it, which gates or evidence prove it, and which
requirements still have pending gaps.

The skill is read-only. It inspects files under the current repo:

- SPEC/backlog files such as `SPEC.md`, `BACKLOG.md`, `docs/backlog.json`, or
  paths passed with `--spec`.
- Durable job records under `.agent_team/jobs/*.toml`.
- Gate ledgers under `.agent_team/jobs/*.gates.jsonl`.
- Verifier evidence under `target/agent-evidence/*.json`.

## Command

```sh
"$AGENT_TEAM_ROOT"/skills/traceability/scripts/traceability.sh --spec SPEC.md
```

Useful flags:

```sh
traceability.sh [--repo REPO_ROOT] [--spec PATH]... [--spec-glob GLOB]...
                [--evidence-dir PATH] [--output PATH] [--json]
                [--fail-on-gap]
```

If no `--spec` or `--spec-glob` is provided, the tool looks for common
SPEC/backlog filenames in the repo root, `docs/`, and `documentation/`.

## Requirement Input

Markdown specs work best when requirements have stable IDs:

```markdown
- [ ] REQ-1: Capture deterministic gate evidence for every delivery job.
- [ ] REQ-2: Surface pending gaps explicitly in manager handoff.
```

Backlog JSON may be either an array or an object with `requirements`, `items`,
`work_items`, or `backlog`:

```json
{
  "items": [
    {
      "id": "REQ-1",
      "title": "Capture deterministic gate evidence",
      "acceptance_criteria": ["Evidence path is listed in the matrix"]
    }
  ]
}
```

When Markdown does not contain IDs, checklist bullets and normative bullets in
requirements/acceptance sections are still included. Stable IDs are strongly
preferred because they make matching deterministic.

## Statuses

- `delivered`: at least one matched job has a delivery artifact and passing
  gate/evidence proof.
- `unproven`: a matched job has a delivery artifact or terminal completion, but
  gate/evidence proof is missing or failed.
- `specified`: a matched job exists, but it is still queued/running/blocked and
  has no delivery artifact yet.
- `gap`: no job maps to the requirement, or no SPEC/backlog requirements were
  found.

Every non-`delivered` row includes a `PENDING GAP` reason in the Markdown and
JSON output.

## Handoff Pattern

For a review or manager handoff:

```sh
"$AGENT_TEAM_ROOT"/skills/traceability/scripts/traceability.sh \
  --spec SPEC.md \
  --output target/agent-evidence/traceability.md \
  --fail-on-gap
```

Attach or reference the generated Markdown in the PR/job handoff. Use
`--json --output target/agent-evidence/traceability.json` when another gate or
dashboard needs structured data.
