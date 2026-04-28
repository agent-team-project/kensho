#!/usr/bin/env python3
"""End-to-end smoke test for the Go agent-team binary's `init` command.

Mirrors the `init` portion of `scripts/ci/smoke_init.py`. SQU-21 is the
foundation Go port and only ships `init`; `run`, `doctor`, and the resource
verbs land in follow-up tickets, so this smoke is intentionally narrower than
the Python one.

Usage:
    smoke_init_go.py <path-to-agent-team-go-binary>
"""

from __future__ import annotations

import subprocess
import sys
import tempfile
import tomllib
from pathlib import Path

EXPECTED_AFTER_INIT = [
    ".agent_team/config.toml",
    ".agent_team/config.toml.example",
    ".agent_team/agents/ticket-manager/agent.md",
    ".agent_team/agents/ticket-manager/config.toml",
    ".agent_team/agents/manager/agent.md",
    ".agent_team/agents/manager/config.toml",
    ".agent_team/agents/manager/skills/assign-worker/SKILL.md",
    ".agent_team/agents/worker/agent.md",
    ".agent_team/agents/worker/config.toml",
    ".agent_team/skills/linear/SKILL.md",
    ".agent_team/skills/linear/scripts/linear-graphql.sh",
    ".agent_team/skills/pull-request/SKILL.md",
]


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        print(f"usage: {argv[0]} <path-to-agent-team-go-binary>", file=sys.stderr)
        return 2
    binary = Path(argv[1]).resolve()
    if not binary.is_file():
        print(f"binary not found: {binary}", file=sys.stderr)
        return 2

    problems: list[str] = []
    with tempfile.TemporaryDirectory() as tmp:
        target = Path(tmp)

        run([str(binary), "init", "--target", str(target)])
        for rel in EXPECTED_AFTER_INIT:
            if not (target / rel).exists():
                problems.append(f"missing after init: {rel}")

        try:
            tomllib.loads((target / ".agent_team" / "config.toml").read_text())
        except Exception as e:  # noqa: BLE001
            problems.append(f"config.toml not valid TOML: {e}")

        # The bundled linear-graphql.sh must remain executable so that skill
        # bash invocations work without a chmod step on the consumer side.
        sh = target / ".agent_team/skills/linear/scripts/linear-graphql.sh"
        if sh.exists() and not (sh.stat().st_mode & 0o111):
            problems.append(f"{sh} is not executable after init")

        # Re-init without --force should keep the user-edited config.toml.
        cfg_path = target / ".agent_team" / "config.toml"
        cfg_path.write_text("# user-edited\n")
        run([str(binary), "init", "--target", str(target)])
        if cfg_path.read_text() != "# user-edited\n":
            problems.append("re-init overwrote a user-edited config.toml (must be untouched)")

    if problems:
        print("smoke_init_go failed:", file=sys.stderr)
        for p in problems:
            print(f"  - {p}", file=sys.stderr)
        return 1
    print("OK  agent-team-go init")
    return 0


def run(cmd: list[str]) -> None:
    r = subprocess.run(cmd, capture_output=True, text=True)
    if r.returncode != 0:
        print(f"command failed: {' '.join(cmd)}", file=sys.stderr)
        print(r.stdout, file=sys.stderr)
        print(r.stderr, file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    sys.exit(main(sys.argv))
