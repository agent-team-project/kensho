#!/usr/bin/env python3
"""Validate YAML frontmatter on every bundled agent and skill.

Walks the bundled template and checks:
  - Each `template/agents/<name>/agent.md`
  - Each `template/skills/<name>/SKILL.md` (shared)
  - Each `template/agents/<name>/skills/<name>/SKILL.md` (agent-private)

Each file must:
  1. Start with a YAML frontmatter block delimited by `---`.
  2. Parse as a mapping.
  3. Contain a non-empty `description` string.
"""

from __future__ import annotations

import sys
from pathlib import Path

import yaml

REPO_ROOT = Path(__file__).resolve().parents[2]
TEMPLATE_ROOT = REPO_ROOT / "template"


def extract_frontmatter(path: Path) -> tuple[dict | None, str | None]:
    text = path.read_text()
    if not text.startswith("---\n") and not text.startswith("---\r\n"):
        return None, "missing opening `---` frontmatter delimiter on line 1"
    lines = text.splitlines()
    try:
        end = next(i for i, line in enumerate(lines[1:], start=1) if line.strip() == "---")
    except StopIteration:
        return None, "missing closing `---` frontmatter delimiter"
    body = "\n".join(lines[1:end])
    try:
        data = yaml.safe_load(body)
    except yaml.YAMLError as e:
        return None, f"YAML parse error — {e}"
    if not isinstance(data, dict):
        return None, f"frontmatter must be a mapping, got {type(data).__name__}"
    return data, None


def validate(path: Path) -> list[str]:
    rel = path.relative_to(REPO_ROOT)
    data, err = extract_frontmatter(path)
    if err is not None:
        return [f"{rel}: {err}"]
    errors = []
    desc = data.get("description")
    if desc is None:
        errors.append(f"{rel}: missing required field `description`")
    elif not isinstance(desc, str) or not desc.strip():
        errors.append(f"{rel}: field `description` must be a non-empty string")
    return errors


def main() -> int:
    targets: list[Path] = []
    targets.extend(sorted(TEMPLATE_ROOT.glob("agents/*/agent.md")))
    targets.extend(sorted(TEMPLATE_ROOT.glob("skills/*/SKILL.md")))
    targets.extend(sorted(TEMPLATE_ROOT.glob("agents/*/skills/*/SKILL.md")))

    if not targets:
        print("No agent/skill files found — nothing to validate.", file=sys.stderr)
        return 1

    failures: list[str] = []
    for path in targets:
        errs = validate(path)
        if errs:
            failures.extend(errs)
        else:
            print(f"OK  {path.relative_to(REPO_ROOT)}")

    if failures:
        print("\nFrontmatter validation failed:", file=sys.stderr)
        for msg in failures:
            print(f"  - {msg}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
