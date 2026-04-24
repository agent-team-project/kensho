#!/usr/bin/env python3
"""Validate YAML frontmatter on every agent and skill definition.

Each `plugins/squirtle-squad/agents/*.md` and
`plugins/squirtle-squad/skills/*/SKILL.md` must:
  1. Start with a YAML frontmatter block delimited by `---`.
  2. Parse as a mapping.
  3. Contain non-empty `name` and `description` string fields.
"""

from __future__ import annotations

import sys
from pathlib import Path

import yaml

REPO_ROOT = Path(__file__).resolve().parents[2]
AGENTS_DIR = REPO_ROOT / "plugins" / "squirtle-squad" / "agents"
SKILLS_DIR = REPO_ROOT / "plugins" / "squirtle-squad" / "skills"

REQUIRED_FIELDS = ("name", "description")


def extract_frontmatter(path: Path) -> tuple[dict | None, str | None]:
    text = path.read_text()
    if not text.startswith("---\n") and not text.startswith("---\r\n"):
        return None, "missing opening `---` frontmatter delimiter on line 1"
    # Skip the opening delimiter and look for the closing one.
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
    for field in REQUIRED_FIELDS:
        value = data.get(field)
        if value is None:
            errors.append(f"{rel}: missing required field `{field}`")
        elif not isinstance(value, str) or not value.strip():
            errors.append(f"{rel}: field `{field}` must be a non-empty string")
    return errors


def main() -> int:
    targets: list[Path] = []
    if AGENTS_DIR.is_dir():
        targets.extend(sorted(AGENTS_DIR.glob("*.md")))
    if SKILLS_DIR.is_dir():
        targets.extend(sorted(SKILLS_DIR.glob("*/SKILL.md")))

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
