#!/usr/bin/env python3
"""End-to-end smoke test: `agent-squad init` against a temp dir.

Exercises the CLI's primary path — vendor template, generate plugin manifests,
write a starter config.toml — and asserts the resulting tree is valid.
"""

from __future__ import annotations

import json
import subprocess
import sys
import tempfile
import tomllib
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
CLI_SRC = REPO_ROOT / "cli" / "src"


def main() -> int:
    with tempfile.TemporaryDirectory() as tmp:
        target = Path(tmp)
        result = subprocess.run(
            [sys.executable, "-m", "agent_squad", "init", "--target", str(target)],
            env={"PYTHONPATH": str(CLI_SRC), "PATH": _path()},
            capture_output=True,
            text=True,
            check=False,
        )
        if result.returncode != 0:
            print("init failed:", file=sys.stderr)
            print(result.stdout, file=sys.stderr)
            print(result.stderr, file=sys.stderr)
            return 1

        problems: list[str] = []

        for rel in [
            ".agent_squad/config.toml",
            ".agent_squad/config.toml.example",
            ".agent_squad/agents/ticket-manager.md",
            ".agent_squad/agents/manager.md",
            ".agent_squad/agents/worker.md",
            ".agent_squad/skills/linear/SKILL.md",
            ".agent_squad/skills/pull-request/SKILL.md",
            ".agent_squad/skills/assign-worker/SKILL.md",
            ".agent_squad/scripts/linear-graphql.sh",
            ".agent_squad/.claude-plugin/plugin.json",
            ".claude-plugin/marketplace.json",
        ]:
            p = target / rel
            if not p.exists():
                problems.append(f"missing after init: {rel}")

        try:
            tomllib.loads((target / ".agent_squad" / "config.toml").read_text())
        except Exception as e:  # noqa: BLE001
            problems.append(f"config.toml not valid TOML: {e}")

        for rel in [
            ".agent_squad/.claude-plugin/plugin.json",
            ".claude-plugin/marketplace.json",
        ]:
            try:
                json.loads((target / rel).read_text())
            except Exception as e:  # noqa: BLE001
                problems.append(f"{rel} not valid JSON: {e}")

        # `add manager <slug>` should scaffold a CLAUDE.md.
        result = subprocess.run(
            [sys.executable, "-m", "agent_squad", "add", "manager", "smoke-test", "--target", str(target)],
            env={"PYTHONPATH": str(CLI_SRC), "PATH": _path()},
            capture_output=True,
            text=True,
            check=False,
        )
        if result.returncode != 0:
            problems.append(f"add manager failed: {result.stderr}")
        elif not (target / ".agent_squad" / "managers" / "smoke-test" / "CLAUDE.md").exists():
            problems.append("add manager did not create the expected CLAUDE.md")

        if problems:
            print("smoke_init failed:", file=sys.stderr)
            for p in problems:
                print(f"  - {p}", file=sys.stderr)
            return 1

        print("OK  agent-squad init + add manager")
        return 0


def _path() -> str:
    import os

    return os.environ.get("PATH", "")


if __name__ == "__main__":
    sys.exit(main())
