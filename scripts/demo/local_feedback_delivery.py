#!/usr/bin/env python3
"""Demo local cross-repo feedback delivery.

Usage:
    python3 scripts/demo/local_feedback_delivery.py [bin/agent-team] [--keep]

The demo creates two temporary repos with minimal `.agent_team/` configs:
`source` declares a `[feedback.routes.receiver] type="local"` route pointing
at `target`. It starts the target daemon, submits incident feedback from the
source repo, then shows that the item landed in the target feedback store and
that the target manager mailbox received the incident ping.
"""

from __future__ import annotations

import argparse
import os
import shutil
import subprocess
import sys
import tempfile
import textwrap
from pathlib import Path


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", nargs="?", default="bin/agent-team", help="Path to the agent-team binary.")
    parser.add_argument("--keep", action="store_true", help="Keep the temporary demo repos for inspection.")
    args = parser.parse_args(argv[1:])

    binary = Path(args.binary).resolve()
    if not binary.is_file():
        print(f"agent-team binary not found: {binary}", file=sys.stderr)
        print("Build it first: go build -o bin/agent-team ./cmd/agent-team", file=sys.stderr)
        return 2
    daemon = binary.parent / "agent-teamd"
    if not daemon.is_file():
        print(f"agent-teamd sibling binary not found: {daemon}", file=sys.stderr)
        print("Build it first: go build -o bin/agent-teamd ./cmd/agent-teamd", file=sys.stderr)
        return 2

    root = Path(tempfile.mkdtemp(prefix="agent-team-feedback-demo-", dir="/tmp"))
    source = root / "source"
    target = root / "target"
    env = scrub_agent_team_env(os.environ.copy())
    env["PATH"] = f"{binary.parent}:{env.get('PATH', '')}"
    try:
        write_repo(source, "source-project")
        write_repo(target, "target-project")
        append_source_route(source, target)
        write_target_topology(target)

        step("start target daemon")
        run(binary, "daemon", "start", "--target", target, "--ready-timeout", "5s", "--json", env=env)

        step("submit source incident feedback to local target route")
        submit_env = env | {
            "AGENT_TEAM_INSTANCE": "worker-squ-demo",
            "AGENT_TEAM_ORIGIN_AGENT": "worker",
            "AGENT_TEAM_JOB_ID": "squ-demo",
            "AGENT_TEAM_TICKET": "SQU-DEMO",
        }
        run(
            binary,
            "feedback",
            "submit",
            "target daemon socket was unreachable during demo",
            "--repo",
            source,
            "--route",
            "receiver",
            "--category",
            "incident",
            env=submit_env,
        )

        step("show target feedback store")
        listing = run(binary, "feedback", "ls", "--repo", target, "--status", "all", env=env)
        print(listing.rstrip())
        feedback_id = next((field for field in listing.split() if field.startswith("fb-")), "")
        if not feedback_id:
            raise DemoError("target feedback store did not contain a delivered item")
        print(run(binary, "feedback", "show", feedback_id, "--repo", target, env=env).rstrip())

        step("show target manager incident ping")
        print(run(binary, "inbox", "show", "manager", "--repo", target, "--unread", env=env).rstrip())
        return 0
    except DemoError as exc:
        print(f"demo failed: {exc}", file=sys.stderr)
        return 1
    finally:
        subprocess.run([str(binary), "daemon", "stop", "--target", str(target), "--quiet"], env=env, check=False)
        if args.keep:
            print(f"kept demo root: {root}")
        else:
            shutil.rmtree(root, ignore_errors=True)


class DemoError(RuntimeError):
    pass


def write_repo(root: Path, project_id: str) -> None:
    (root / ".git").mkdir(parents=True)
    team = root / ".agent_team"
    team.mkdir()
    (team / "config.toml").write_text(
        textwrap.dedent(
            f"""
            [project]
            id = "{project_id}"

            [pm]
            provider = "none"

            [team]
            pm_tool = "none"

            [runtime]
            kind = "codex"
            """
        ).strip()
        + "\n",
        encoding="utf-8",
    )


def append_source_route(source: Path, target: Path) -> None:
    with (source / ".agent_team" / "config.toml").open("a", encoding="utf-8") as f:
        f.write(
            textwrap.dedent(
                f"""

                [feedback.routes.receiver]
                type = "local"
                root = "{target}"
                """
            )
        )


def write_target_topology(target: Path) -> None:
    (target / ".agent_team" / "instances.toml").write_text(
        textwrap.dedent(
            """
            [instances.manager]
            agent = "manager"
            """
        ).strip()
        + "\n",
        encoding="utf-8",
    )


def step(label: str) -> None:
    print(f"\n==> {label}")


def run(*args: object, env: dict[str, str]) -> str:
    cmd = [str(arg) for arg in args]
    proc = subprocess.run(cmd, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if proc.returncode != 0:
        raise DemoError(f"{' '.join(cmd)} exited {proc.returncode}\nstdout={proc.stdout}\nstderr={proc.stderr}")
    if proc.stderr:
        print(proc.stderr.rstrip(), file=sys.stderr)
    return proc.stdout


def scrub_agent_team_env(env: dict[str, str]) -> dict[str, str]:
    return {key: value for key, value in env.items() if not key.startswith("AGENT_TEAM_")}


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
