#!/usr/bin/env python3
"""Validate that committed TOML files parse cleanly."""

from __future__ import annotations

import re
import sys
import tomllib
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
TEMPLATE_ROOT = REPO_ROOT / "template"
PROFILE_DIRECTIVE_RE = re.compile(
    r"^\s*\{\{\s*(if\s+eq\s+\.template\.profile\s+`([^`]+)`|else|end)\s*-?\}\}\s*$"
)


def collect() -> list[Path]:
    paths: list[Path] = [
        REPO_ROOT / ".agent_team" / "config.toml",
        REPO_ROOT / ".agent_team" / "instances.toml",
        TEMPLATE_ROOT / "template.toml",
        TEMPLATE_ROOT / "instances.toml.tmpl",
    ]
    paths.extend(sorted(TEMPLATE_ROOT.glob("agents/*/config.toml")))
    paths.extend(sorted((REPO_ROOT / "examples").glob("**/*.toml")))
    return paths


def main() -> int:
    failures: list[str] = []
    for path in collect():
        rel = path.relative_to(REPO_ROOT)
        if not path.exists():
            failures.append(f"{rel}: file not found")
            continue
        if path.name == "instances.toml.tmpl":
            for profile in ("slim", "full"):
                try:
                    tomllib.loads(render_profile_template(path.read_text(), profile))
                except tomllib.TOMLDecodeError as e:
                    failures.append(f"{rel} ({profile}): invalid TOML — {e}")
                    continue
                print(f"OK  {rel} ({profile})")
            continue
        try:
            with path.open("rb") as f:
                tomllib.load(f)
        except tomllib.TOMLDecodeError as e:
            failures.append(f"{rel}: invalid TOML — {e}")
            continue
        print(f"OK  {rel}")

    if failures:
        print("\nTOML validation failed:", file=sys.stderr)
        for msg in failures:
            print(f"  - {msg}", file=sys.stderr)
        return 1
    return 0


def render_profile_template(body: str, profile: str) -> str:
    """Render the tiny subset of Go template syntax used by instances.toml.tmpl."""
    active = True
    stack: list[tuple[bool, bool]] = []
    out: list[str] = []
    for line in body.splitlines():
        match = PROFILE_DIRECTIVE_RE.match(line)
        if match:
            action = match.group(1)
            if action.startswith("if "):
                condition = profile == match.group(2)
                stack.append((active, condition))
                active = active and condition
            elif action == "else":
                if not stack:
                    raise ValueError("template else without if")
                parent_active, condition = stack[-1]
                active = parent_active and not condition
            elif action == "end":
                if not stack:
                    raise ValueError("template end without if")
                parent_active, _ = stack.pop()
                active = parent_active
            continue
        if active:
            out.append(line)
    if stack:
        raise ValueError("template if without end")
    return "\n".join(out) + "\n"


if __name__ == "__main__":
    sys.exit(main())
