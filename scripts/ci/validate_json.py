#!/usr/bin/env python3
"""Validate that required JSON manifests parse cleanly."""

from __future__ import annotations

import json
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]

MANIFESTS = [
    REPO_ROOT / ".claude-plugin" / "marketplace.json",
    REPO_ROOT / "plugins" / "squirtle-squad" / ".claude-plugin" / "plugin.json",
]


def main() -> int:
    failures: list[str] = []
    for path in MANIFESTS:
        rel = path.relative_to(REPO_ROOT)
        if not path.exists():
            failures.append(f"{rel}: file not found")
            continue
        try:
            with path.open("rb") as f:
                json.load(f)
        except json.JSONDecodeError as e:
            failures.append(f"{rel}: invalid JSON — {e.msg} at line {e.lineno} col {e.colno}")
            continue
        print(f"OK  {rel}")

    if failures:
        print("\nJSON validation failed:", file=sys.stderr)
        for msg in failures:
            print(f"  - {msg}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
