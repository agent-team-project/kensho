#!/usr/bin/env python3
"""End-to-end smoke test for the Go agent-team binary.

Mirrors the shape of `scripts/ci/smoke_init.py` (Python CLI smoke). The Go
binary post-SQU-22 ships `init`, `run`, `doctor`, `instance`, and `template` —
this smoke exercises the `init` and `template show` paths plus the bundled
template's parameter substitution, without requiring `claude` on PATH.

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

        # --- init with --set, the templates-as-images path ---
        run([
            str(binary), "init", "--target", str(target),
            "--set", "linear.team_id=smoke-team-uuid",
            "--set", "linear.ticket_prefix=SMK",
        ])
        for rel in EXPECTED_AFTER_INIT:
            if not (target / rel).exists():
                problems.append(f"missing after init: {rel}")

        # The init-time template manifest must NOT leak into the consumer tree.
        if (target / ".agent_team" / "template.toml").exists():
            problems.append("template.toml leaked into .agent_team/")

        # Resolved config must contain --set values.
        cfg_text = (target / ".agent_team" / "config.toml").read_text()
        if 'team_id = "smoke-team-uuid"' not in cfg_text:
            problems.append(f"--set linear.team_id missing from config.toml: {cfg_text}")
        if 'ticket_prefix = "SMK"' not in cfg_text:
            problems.append(f"--set linear.ticket_prefix missing from config.toml: {cfg_text}")
        try:
            tomllib.loads(cfg_text)
        except Exception as e:  # noqa: BLE001
            problems.append(f"config.toml not valid TOML: {e}")

        # The bundled linear-graphql.sh must remain executable after init.
        sh = target / ".agent_team/skills/linear/scripts/linear-graphql.sh"
        if sh.exists() and not (sh.stat().st_mode & 0o111):
            problems.append(f"{sh} is not executable after init")

        # Re-init without --force should keep the user-edited config.toml.
        cfg_path = target / ".agent_team" / "config.toml"
        cfg_path.write_text("# user-edited\n")
        run([
            str(binary), "init", "--target", str(target),
            "--set", "linear.team_id=should-not-overwrite",
            "--set", "linear.ticket_prefix=NOP",
        ])
        if cfg_path.read_text() != "# user-edited\n":
            problems.append("re-init overwrote a user-edited config.toml (must be untouched)")

        # --- --no-input fails clearly when required params missing ---
        with tempfile.TemporaryDirectory() as tmp2:
            r = subprocess.run(
                [str(binary), "init", "--target", tmp2, "--no-input"],
                capture_output=True, text=True,
            )
            if r.returncode == 0:
                problems.append("--no-input init succeeded but should have failed")
            elif "missing" not in r.stderr.lower():
                problems.append(f"--no-input error message missing 'missing': {r.stderr}")

        # --- template show on the bundled template prints the manifest ---
        r = subprocess.run(
            [str(binary), "template", "show"],
            capture_output=True, text=True,
        )
        if r.returncode != 0:
            problems.append(f"template show failed: {r.stderr}")
        for needle in ("Template: default v", "linear.team_id", "linear.ticket_prefix"):
            if needle not in r.stdout:
                problems.append(f"template show missing {needle!r} in stdout: {r.stdout!r}")

        # --- template ls includes bundled ---
        r = subprocess.run(
            [str(binary), "template", "ls"],
            capture_output=True, text=True,
        )
        if r.returncode != 0:
            problems.append(f"template ls failed: {r.stderr}")
        if "bundled" not in r.stdout:
            problems.append(f"template ls missing 'bundled': {r.stdout!r}")

        # --- doctor on the freshly-initialised tree should pass ---
        # The user-edited config.toml from the earlier step won't have the
        # required keys; rewrite a valid one for this check.
        cfg_path.write_text(cfg_text)
        r = subprocess.run(
            [str(binary), "doctor", "--target", str(target)],
            capture_output=True, text=True,
        )
        if r.returncode != 0:
            problems.append(f"doctor failed on a healthy tree: rc={r.returncode}\nstdout: {r.stdout}\nstderr: {r.stderr}")

    if problems:
        print("smoke_init_go failed:", file=sys.stderr)
        for p in problems:
            print(f"  - {p}", file=sys.stderr)
        return 1
    print("OK  agent-team-go init + template + doctor")
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
