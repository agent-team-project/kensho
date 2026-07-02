#!/usr/bin/env python3
"""Run a local end-to-end orchestration demo with a fake agent runtime.

Usage:
    python3 scripts/demo/local_orchestration.py [bin/agent-team] [--runtime claude|codex] [--keep]
    python3 scripts/demo/local_orchestration.py bin/agent-team --runtime codex --real-codex-probe

The demo creates a temporary repo, initializes the bundled team, configures a
small fake runtime, creates a pipeline job, verifies the operator drain hint,
uses the team drain loop to dispatch the worker, then captures operator views.
Claude mode also starts persistent instances; Codex mode exercises the
daemon-managed one-shot worker path. By default it never calls a real LLM
service. Pass --real-codex-probe to run a real Codex `exec -` runtime probe
before the fake runtime is installed.
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
import textwrap
import time
from pathlib import Path


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", nargs="?", default="bin/agent-team", help="Path to the agent-team binary.")
    parser.add_argument("--runtime", choices=("claude", "codex"), default="claude", help="Runtime profile to simulate.")
    parser.add_argument(
        "--real-codex-probe",
        action="store_true",
        help="Before installing the fake runtime, run `agent-team runtime probe --runtime codex --exec` with the real codex binary.",
    )
    parser.add_argument(
        "--real-codex-probe-output",
        help="Optional JSON artifact path for --real-codex-probe. Defaults inside the temporary demo root.",
    )
    parser.add_argument("--keep", action="store_true", help="Keep the temporary demo repo for inspection.")
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
    real_codex = ""
    if args.real_codex_probe:
        real_codex = shutil.which("codex", path=os.environ.get("PATH", ""))
        if not real_codex:
            print("real Codex probe requested, but no `codex` binary was found on PATH.", file=sys.stderr)
            return 2

    root = Path(tempfile.mkdtemp(prefix="agent-team-demo-", dir="/tmp"))
    fake_runtime = root / "fake-bin" / args.runtime
    repo = root / "repo"
    env = scrub_agent_team_env(os.environ.copy())
    env["PATH"] = f"{fake_runtime.parent}:{env.get('PATH', '')}"
    try:
        fake_runtime.parent.mkdir(parents=True, exist_ok=True)
        write_fake_runtime(fake_runtime)
        repo.mkdir(parents=True, exist_ok=True)

        step("init bundled team")
        run(binary, "init", "--target", repo, "--set", "linear.team_id=demo-team", "--set", "linear.ticket_prefix=DEMO")
        enable_demo_schedule(repo)
        if args.real_codex_probe:
            probe_output = Path(args.real_codex_probe_output).resolve() if args.real_codex_probe_output else root / "runtime-probe-codex.json"
            step("probe real Codex runtime")
            probe = run(
                binary,
                "runtime",
                "probe",
                "--target",
                repo,
                "--runtime",
                "codex",
                "--runtime-bin",
                real_codex,
                "--exec",
                "--timeout",
                "2m",
                "--output",
                probe_output,
                "--json",
                env=os.environ.copy(),
                parse_json=True,
            )
            print(f"real Codex probe: ok={probe.get('ok')} output={probe_output}")
        configure_fake_runtime(repo, args.runtime, fake_runtime)

        step("validate topology and runtime")
        run(binary, "runtime", "--repo", repo, "--json", env=env, parse_json=True)
        run(binary, "topology", "summary", "--target", repo, "--json", parse_json=True)
        run(binary, "topology", "graph", "--target", repo, "--routes", "--json", parse_json=True)
        run(binary, "pipeline", "doctor", "--all", "--repo", repo, "--json", parse_json=True)
        run(binary, "team", "graph", "delivery", "--repo", repo, "--routes", "--json", parse_json=True)
        run(binary, "team", "doctor", "--all", "--repo", repo, "--json", parse_json=True)

        step("verify command-only schedule hints")
        schedule_due_commands = run(binary, "schedule", "due", "--repo", repo, "--commands")
        require_command(schedule_due_commands, "agent-team schedule fire --dry-run --preview-triggers")
        schedule_next_commands = run(binary, "schedule", "next", "--repo", repo, "--commands")
        require_command(schedule_next_commands, "agent-team schedule fire --dry-run --preview-triggers")
        team_schedule_commands = run(binary, "team", "schedules", "delivery", "--repo", repo, "--commands")
        require_command(team_schedule_commands, "agent-team team tick delivery --dry-run --preview-routes")
        print("schedule commands verified: schedule fire preview, team tick")

        step("verify command-only plan hints")
        plan_commands = run(binary, "plan", "--target", repo, "--commands")
        require_command(plan_commands, f"agent-team sync --repo {repo} --dry-run")
        team_plan_commands = run(binary, "team", "plan", "delivery", "--repo", repo, "--commands")
        require_command(team_plan_commands, f"agent-team team sync delivery --repo {repo} --dry-run")
        print("plan commands verified: sync preview, team sync preview")

        step("verify command-only sync apply hints")
        sync_commands = run(binary, "sync", "--target", repo, "--dry-run", "--commands")
        require_command(sync_commands, f"agent-team sync --repo {repo}")
        team_sync_commands = run(binary, "team", "sync", "delivery", "--repo", repo, "--dry-run", "--commands")
        require_command(team_sync_commands, f"agent-team team sync delivery --repo {repo}")
        print("sync commands verified: sync apply, team sync apply")

        step("verify command-only lifecycle apply hints")
        start_commands = run(binary, "start", "--target", repo, "--dry-run", "--commands")
        require_command(start_commands, f"agent-team start --repo {repo}")
        team_up_commands = run(binary, "team", "up", "delivery", "--repo", repo, "--dry-run", "--commands")
        require_command(team_up_commands, f"agent-team team up delivery --repo {repo}")
        print("lifecycle commands verified: start apply, team up apply")

        step("start daemon")
        run(binary, "daemon", "start", "--target", repo, "--ready-timeout", "5s", "--json", env=env, parse_json=True)
        if args.runtime == "claude":
            step("start persistent instances")
            start = run(binary, "start", "--target", repo, "--wait", "--timeout", "5s", "--json", env=env, parse_json=True)
            started = [row.get("instance") for row in start.get("actions", []) if row.get("action") in {"start", "skip"}]
            print(f"persistent instances: {', '.join(started) or '(none)'}")
        else:
            print("persistent instances: skipped for Codex one-shot runtime")

        step("create and preview a pipeline job")
        created = run(
            binary,
            "pipeline",
            "run",
            "ticket_to_pr",
            "DEMO-1",
            "Implement the local demo scenario",
            "--repo",
            repo,
            "--json",
            parse_json=True,
        )
        job_id = field(created, "id", "ID")
        print(f"job created: {job_id} pipeline={field(created, 'pipeline', 'Pipeline')} target={field(created, 'target', 'Target')}")
        preview = run(
            binary,
            "pipeline",
            "advance",
            "ticket_to_pr",
            "--repo",
            repo,
            "--dry-run",
            "--preview-routes",
            "--json",
            parse_json=True,
        )
        if not preview:
            raise DemoError("pipeline advance preview returned no ready work")
        first = preview[0]
        print(f"preview: job={first['job_id']} step={first.get('step_id')} action={first['action']}")

        step("verify command-only ready hints")
        job_ready_commands = run(binary, "job", "ready", "--repo", repo, "--commands")
        require_command(job_ready_commands, f"agent-team --repo {repo} job advance demo-1")
        pipeline_ready_commands = run(binary, "pipeline", "ready", "ticket_to_pr", "--repo", repo, "--commands")
        require_command(pipeline_ready_commands, f"agent-team --repo {repo} pipeline tick ticket_to_pr --dry-run --preview-routes")
        team_ready_commands = run(binary, "team", "ready", "delivery", "--repo", repo, "--commands")
        require_command(team_ready_commands, f"agent-team --repo {repo} team tick delivery --dry-run --preview-routes")
        print("ready commands verified: job advance, pipeline tick, team tick")

        step("verify operator drain hint")
        before_drain = run(binary, "team", "overview", "delivery", "--repo", repo, "--json", parse_json=True)
        require_action(before_drain, "agent-team team drain delivery")
        overview_commands = run(binary, "team", "overview", "delivery", "--repo", repo, "--commands")
        require_command(overview_commands, f"agent-team --repo {repo} team drain delivery")
        print("team overview recommends: agent-team team drain delivery")

        step("drain ready work through daemon dispatch")
        drained = run(
            binary,
            "team",
            "drain",
            "delivery",
            "--repo",
            repo,
            "--workspace",
            "repo",
            "--skip-schedules",
            "--max-cycles",
            "1",
            "--interval",
            "0s",
            "--json",
            env=env,
            parse_json=True,
        )
        worker = worker_from_team_drain(drained)
        if not worker:
            raise DemoError(f"team drain did not dispatch the implement worker: {json.dumps(drained, indent=2)}")
        print(f"worker dispatched by drain: {worker} cycles={drained.get('cycles_run')} idle={drained.get('idle')}")

        step("wait for fake worker exit and reconcile")
        run(binary, "wait", worker, "--target", repo, "--until", "terminal", "--timeout", "10s", "--json", parse_json=True)
        run(binary, "tick", "--target", repo, "--skip-schedules", "--skip-drain", "--skip-advance", "--json", parse_json=True)

        step("verify command-only prune apply hints")
        prune_commands = run(binary, "prune", "--target", repo, "--dry-run", "--commands")
        require_command(prune_commands, f"agent-team prune --repo {repo}")
        team_prune_commands = run(binary, "team", "prune", "delivery", "--repo", repo, "--dry-run", "--commands")
        require_command(team_prune_commands, f"agent-team team prune delivery --repo {repo}")
        print("prune commands verified: prune apply, team prune apply")

        step("inspect operator views")
        job_detail = run(binary, "job", "show", "demo-1", "--repo", repo, "--json", parse_json=True)
        print(f"job status: {field(job_detail, 'status', 'Status')} last={field(job_detail, 'last_status', 'LastStatus')}")
        overview = run(binary, "team", "overview", "delivery", "--repo", repo, "--json", parse_json=True)
        print(f"team overview state: {overview.get('state', 'unknown')}")
        snapshot = run(binary, "snapshot", "--target", repo, "--events", "20", "--json", parse_json=True)
        print(f"snapshot sections: jobs={len(snapshot.get('jobs') or [])} events={len(snapshot.get('events') or [])}")

        print(f"\nDemo complete. Repo: {repo}")
        if args.keep:
            print("Kept temporary files because --keep was set.")
        return 0
    except DemoError as exc:
        print(f"demo failed: {exc}", file=sys.stderr)
        return 1
    finally:
        subprocess.run(
            [str(binary), "stop", "--all", "--target", str(repo), "--timeout", "2s"],
            env=env,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        subprocess.run(
            [str(binary), "daemon", "stop", "--target", str(repo), "--timeout", "2s"],
            env=env,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        if args.keep:
            print(f"Preserved demo root: {root}")
        else:
            shutil.rmtree(root, ignore_errors=True)


class DemoError(RuntimeError):
    pass


def step(message: str) -> None:
    print(f"\n==> {message}")


def run(binary: Path, *args: object, env: dict[str, str] | None = None, parse_json: bool = False):
    cmd = [str(binary), *(str(arg) for arg in args)]
    print("+", " ".join(cmd))
    child_env = scrub_agent_team_env(os.environ.copy() if env is None else env)
    result = subprocess.run(cmd, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=child_env)
    if result.returncode != 0:
        raise DemoError(f"{' '.join(cmd)} failed with {result.returncode}\nstdout={result.stdout}\nstderr={result.stderr}")
    if parse_json:
        try:
            return json.loads(result.stdout or "null")
        except json.JSONDecodeError as exc:
            raise DemoError(f"{' '.join(cmd)} did not return JSON: {exc}\nstdout={result.stdout}\nstderr={result.stderr}") from exc
    if result.stdout.strip():
        print(result.stdout.rstrip())
    if result.stderr.strip():
        print(result.stderr.rstrip(), file=sys.stderr)
    return result.stdout


def scrub_agent_team_env(env: dict[str, str]) -> dict[str, str]:
    return {key: value for key, value in env.items() if not key.startswith("AGENT_TEAM_")}


def field(data: dict, *names: str) -> object:
    for name in names:
        if name in data:
            return data[name]
    return ""


def require_action(data: dict, command: str) -> None:
    actions = data.get("actions") or data.get("Actions") or []
    if command not in actions:
        raise DemoError(f"expected action hint {command!r}; got {actions!r}")


def require_command(output: str, command: str) -> None:
    commands = [line.strip() for line in output.splitlines() if line.strip()]
    if command not in commands:
        raise DemoError(f"expected command {command!r}; got {commands!r}")


def worker_from_team_drain(data: dict) -> str:
    for cycle in data.get("cycles") or []:
        tick = cycle.get("tick") or {}
        for row in tick.get("advance") or []:
            if row.get("step_id") == "implement" and row.get("action") == "advanced":
                return row.get("instance") or "worker-demo-1-implement"
    return ""


def configure_fake_runtime(repo: Path, runtime: str, fake_runtime: Path) -> None:
    cfg = repo / ".agent_team" / "config.toml"
    with cfg.open("a", encoding="utf-8") as f:
        f.write("\n[runtime]\n")
        f.write(f'kind = "{runtime}"\n')
        f.write(f'binary = "{toml_string(str(fake_runtime))}"\n')


def enable_demo_schedule(repo: Path) -> None:
    topology = repo / ".agent_team" / "instances.toml"
    body = topology.read_text(encoding="utf-8")
    manager_trigger = textwrap.dedent(
        """\
        [[instances.manager.triggers]]
        event        = "agent.dispatch"
        match.target = "manager"
        """
    )
    schedule_trigger = manager_trigger + textwrap.dedent(
        """\

        [[instances.manager.triggers]]
        event      = "schedule"
        match.name = "demo_due"
        """
    )
    if manager_trigger not in body:
        raise DemoError("bundled topology no longer has the expected manager dispatch trigger")
    body = body.replace(manager_trigger, schedule_trigger, 1)
    team_pipelines = 'pipelines   = ["ticket_to_pr"]'
    if team_pipelines not in body:
        raise DemoError("bundled topology no longer has the expected delivery pipeline list")
    body = body.replace(team_pipelines, team_pipelines + '\nschedules   = ["demo_due"]', 1)
    body += textwrap.dedent(
        """\

        [schedules.demo_due]
        every = "24h"
        run_on_start = true
        payload.target = "manager"
        payload.reason = "demo schedule"
        """
    )
    topology.write_text(body, encoding="utf-8")


def write_fake_runtime(path: Path) -> None:
    path.write_text(
        textwrap.dedent(
            """\
            #!/usr/bin/env python3
            from __future__ import annotations

            import json
            import os
            import sys
            import time
            from pathlib import Path

            instance = os.environ.get("AGENT_TEAM_INSTANCE", "unknown")
            state_dir = Path(os.environ.get("AGENT_TEAM_STATE_DIR", "."))
            state_dir.mkdir(parents=True, exist_ok=True)

            def toml(value: str) -> str:
                return json.dumps(value or "")

            def write_status(phase: str, description: str) -> None:
                body = [
                    "[status]",
                    f"phase = {toml(phase)}",
                    f"description = {toml(description)}",
                    "",
                    "[work]",
                    f"job = {toml(os.environ.get('AGENT_TEAM_JOB_ID', ''))}",
                    f"ticket = {toml(os.environ.get('AGENT_TEAM_TICKET', ''))}",
                    f"branch = {toml(os.environ.get('AGENT_TEAM_BRANCH', ''))}",
                    f"worktree = {toml(os.environ.get('AGENT_TEAM_WORKTREE', ''))}",
                    "",
                ]
                (state_dir / "status.toml").write_text("\\n".join(body), encoding="utf-8")

            runtime = Path(sys.argv[0]).name
            print(f"fake {runtime} instance={instance} args={' '.join(sys.argv[1:])}", flush=True)
            if instance.startswith("worker-") or "-worker-" in instance:
                write_status("implementing", "fake worker running")
                time.sleep(0.2)
                write_status("done", "fake worker completed")
                print(f"fake worker complete: {instance}", flush=True)
                raise SystemExit(0)

            write_status("idle", "fake persistent runtime ready")
            while True:
                time.sleep(1)
            """
        ),
        encoding="utf-8",
    )
    path.chmod(0o755)


def toml_string(value: str) -> str:
    return value.replace("\\", "\\\\").replace('"', '\\"')


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
