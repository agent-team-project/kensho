"""`agent-squad sync` — refresh vendored content from a newer CLI version.

v0.1: stub. The intended behaviour is a three-way merge between the bundled
template, the user's current .agent_squad/ tree, and any local edits, so that
upgrading the CLI doesn't clobber customizations. For now this command tells
you what would change and points at a manual diff.
"""

from __future__ import annotations

import argparse
from pathlib import Path


def register(sub: argparse._SubParsersAction) -> None:
    p = sub.add_parser(
        "sync",
        help="(stub) Refresh .agent_squad/ from the bundled template.",
        description="Not yet implemented. v0.2 ships a diff-aware merge; for now, run `agent-squad init --force` and resolve conflicts manually with git.",
    )
    p.add_argument("--target", type=Path, default=Path.cwd())
    p.set_defaults(func=run)


def run(args: argparse.Namespace) -> int:
    print("agent-squad sync is not implemented yet (v0.1 stub).")
    print("Workaround: run `agent-squad init --force` on a clean working tree, then `git diff` to review.")
    return 0
