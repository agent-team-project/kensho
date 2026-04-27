#!/usr/bin/env python3
"""Validate that committed TOML files parse cleanly."""

from __future__ import annotations

import sys
import tomllib
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]

TOML_FILES = [
    REPO_ROOT / ".agent_squad" / "config.toml",
    REPO_ROOT / "cli" / "pyproject.toml",
    REPO_ROOT / "cli" / "src" / "agent_squad" / "template" / "config.toml.example",
]


def main() -> int:
    failures: list[str] = []
    for path in TOML_FILES:
        rel = path.relative_to(REPO_ROOT)
        if not path.exists():
            failures.append(f"{rel}: file not found")
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


if __name__ == "__main__":
    sys.exit(main())
