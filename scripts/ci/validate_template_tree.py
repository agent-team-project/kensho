#!/usr/bin/env python3
"""Validate that the bundled template tree contains only source artifacts."""

from __future__ import annotations

import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
TEMPLATE_ROOT = REPO_ROOT / "template"

FORBIDDEN_DIR_NAMES = {
    "__pycache__",
    ".mypy_cache",
    ".pytest_cache",
    ".ruff_cache",
    "node_modules",
}
FORBIDDEN_FILE_NAMES = {
    ".DS_Store",
    "Thumbs.db",
}
FORBIDDEN_SUFFIXES = {
    ".pyc",
    ".pyo",
}


def is_forbidden(path: Path) -> bool:
    if any(part in FORBIDDEN_DIR_NAMES for part in path.parts):
        return True
    if path.name in FORBIDDEN_FILE_NAMES:
        return True
    if path.suffix in FORBIDDEN_SUFFIXES:
        return True
    return False


def main() -> int:
    if not TEMPLATE_ROOT.is_dir():
        print(f"template root not found: {TEMPLATE_ROOT}", file=sys.stderr)
        return 1

    failures: list[str] = []
    for path in sorted(TEMPLATE_ROOT.rglob("*")):
        rel = path.relative_to(REPO_ROOT)
        if is_forbidden(path.relative_to(TEMPLATE_ROOT)):
            failures.append(str(rel))

    if failures:
        print("Generated/cache artifacts found under template/:", file=sys.stderr)
        for rel in failures:
            print(f"  - {rel}", file=sys.stderr)
        print("Remove these before building; go:embed all:template would ship them.", file=sys.stderr)
        return 1

    print("OK  template tree contains no generated/cache artifacts")
    return 0


if __name__ == "__main__":
    sys.exit(main())
