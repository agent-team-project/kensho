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
import signal
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
        run(
            binary,
            "init",
            "--target",
            repo,
            "--set",
            "linear.team_id=demo-team",
            "--set",
            "linear.ticket_prefix=DEMO",
            "--set",
            "linear.agent_column=Ready for Agent",
            "--set",
            "linear.agent_user_id=demo-agent",
        )
        enable_demo_schedule(repo)
        prepare_merge_drift_branch(repo)
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

        step("verify event trace diagnostics")
        verify_event_trace(binary, repo)

        step("verify Linear column intake routing")
        verify_linear_column_intake(binary, repo)

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

        step("verify gate infra classification")
        verify_gate_classification(binary, repo, job_id)

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

        step("verify approval-required manual gate")
        approval_job_id = verify_approval_required_gate(binary, repo)

        step("verify job merge dry-run")
        verify_job_merge_dry_run(binary, repo)

        step("verify instance brief")
        verify_instance_brief(binary, repo, approval_job_id)

        step("verify lock-held queue drain")
        verify_lock_queue(binary, repo)

        step("verify restart policy relaunch")
        verify_restart_policy(binary, repo, env)

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


def run_expect_failure(binary: Path, *args: object, env: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
    cmd = [str(binary), *(str(arg) for arg in args)]
    print("+", " ".join(cmd), "# expected failure")
    child_env = scrub_agent_team_env(os.environ.copy() if env is None else env)
    result = subprocess.run(cmd, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=child_env)
    if result.returncode == 0:
        raise DemoError(f"{' '.join(cmd)} unexpectedly succeeded\nstdout={result.stdout}\nstderr={result.stderr}")
    return result


def run_git(repo: Path, *args: str) -> str:
    cmd = ["git", "-C", str(repo), *args]
    print("+", " ".join(cmd))
    result = subprocess.run(cmd, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if result.returncode != 0:
        raise DemoError(f"{' '.join(cmd)} failed with {result.returncode}\nstdout={result.stdout}\nstderr={result.stderr}")
    if result.stdout.strip():
        print(result.stdout.rstrip())
    if result.stderr.strip():
        print(result.stderr.rstrip())
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


def require_substrings(output: str, *needles: str) -> None:
    for needle in needles:
        if needle not in output:
            raise DemoError(f"expected output to contain {needle!r}; got:\n{output}")


def require_json_field(data: dict, key: str, expected: object) -> None:
    actual = data.get(key)
    if actual != expected:
        raise DemoError(f"expected JSON field {key}={expected!r}; got {actual!r} in {json.dumps(data, indent=2)}")


def require_job_in_attention(snapshot: dict, job_id: str, reason: str | None = None) -> None:
    attention = snapshot.get("attention") or snapshot.get("Attention") or []
    for row in attention:
        if field(row, "job_id", "JobID") != job_id:
            continue
        if reason is None or reason in (row.get("reasons") or row.get("Reasons") or []):
            return
    raise DemoError(f"expected triage attention for {job_id} reason={reason!r}; got {json.dumps(attention, indent=2)}")


def find_step(job_data: dict, step_id: str) -> dict:
    for step in job_data.get("steps") or job_data.get("Steps") or []:
        if field(step, "id", "ID") == step_id:
            return step
    raise DemoError(f"expected step {step_id!r}; got {json.dumps(job_data, indent=2)}")


def prepare_merge_drift_branch(repo: Path) -> None:
    (repo / "README.md").write_text("# agent-team demo\n", encoding="utf-8")
    run_git(repo, "init")
    run_git(repo, "checkout", "-B", "main")
    run_git(repo, "config", "user.email", "demo@example.invalid")
    run_git(repo, "config", "user.name", "agent-team demo")
    run_git(repo, "config", "commit.gpgsign", "false")
    run_git(repo, "add", "README.md")
    run_git(repo, "commit", "-m", "demo base")
    run_git(repo, "checkout", "-B", "worker-demo-merge")
    baseline = repo / "coverage" / "baselines" / "a.json"
    baseline.parent.mkdir(parents=True, exist_ok=True)
    baseline.write_text("{}\n", encoding="utf-8")
    run_git(repo, "add", "coverage/baselines/a.json")
    run_git(repo, "commit", "-m", "update demo baseline")
    run_git(repo, "checkout", "main")


def verify_event_trace(binary: Path, repo: Path) -> None:
    match_trace = run(binary, "event", "trace", "agent.dispatch", "--payload", "target=worker", "--target", repo)
    require_substrings(match_trace, "MATCH", "instances.worker", "MISS", "instances.manager")
    miss_trace = run(binary, "event", "trace", "agent.dispatch", "--payload", "target=missing", "--target", repo)
    require_substrings(miss_trace, "MISS", "WARNING: matched 0 rules")
    print("event trace verified: MATCH/MISS and zero-match warning")


def verify_linear_column_intake(binary: Path, repo: Path) -> None:
    entry = run(
        binary,
        "intake",
        "linear",
        "--payload",
        linear_status_payload("DEMO-COLUMN", "Ready for Agent", "human-user"),
        "--target",
        repo,
        "--dry-run",
        "--preview-triggers",
        "--json",
        parse_json=True,
    )
    require_json_field(entry.get("event") or {}, "type", "ticket.status_changed")
    preview = entry.get("preview") or {}
    if preview.get("pipelines") != ["ticket_to_pr"]:
        raise DemoError(f"entry-column transition did not match ticket_to_pr: {json.dumps(entry, indent=2)}")
    jobs = preview.get("pipeline_jobs") or []
    if len(jobs) != 1 or jobs[0].get("action") != "would_create":
        raise DemoError(f"entry-column transition did not preview job creation: {json.dumps(entry, indent=2)}")

    other = run(
        binary,
        "intake",
        "linear",
        "--payload",
        linear_status_payload("DEMO-OTHER", "Todo", "human-user"),
        "--target",
        repo,
        "--dry-run",
        "--preview-triggers",
        "--json",
        parse_json=True,
    )
    other_preview = other.get("preview") or {}
    if other_preview.get("pipelines"):
        raise DemoError(f"other-column transition unexpectedly matched: {json.dumps(other, indent=2)}")

    self_actor = run(
        binary,
        "intake",
        "linear",
        "--payload",
        linear_status_payload("DEMO-SELF", "Ready for Agent", "demo-agent"),
        "--target",
        repo,
        "--dry-run",
        "--preview-triggers",
        "--json",
        parse_json=True,
    )
    if not self_actor.get("ignored") or "self-authored Linear status change" not in (self_actor.get("ignore_reason") or ""):
        raise DemoError(f"self-actor transition was not ignored: {json.dumps(self_actor, indent=2)}")

    run(binary, "pipeline", "run", "ticket_to_pr", "DEMO-REENTRY", "Re-entry demo", "--repo", repo, "--json", parse_json=True)
    run(binary, "job", "close", "demo-reentry", "--repo", repo, "--status", "done", "--message", "demo terminal", "--json", parse_json=True)
    reentry = run(
        binary,
        "intake",
        "linear",
        "--payload",
        linear_status_payload("DEMO-REENTRY", "Ready for Agent", "human-user"),
        "--target",
        repo,
        "--dry-run",
        "--preview-triggers",
        "--json",
        parse_json=True,
    )
    reentry_jobs = ((reentry.get("preview") or {}).get("pipeline_jobs") or [])
    if len(reentry_jobs) != 1 or reentry_jobs[0].get("action") != "would_noop" or not reentry_jobs[0].get("existing"):
        raise DemoError(f"terminal re-entry did not preview default no-op: {json.dumps(reentry, indent=2)}")
    print("Linear intake verified: entry column, other column, self actor, re-entry no-op")


def linear_status_payload(ticket: str, status: str, actor_id: str) -> str:
    return json.dumps(
        {
            "action": "Issue updated",
            "actor": {"id": actor_id, "name": "Demo Actor"},
            "data": {
                "identifier": ticket,
                "title": "Demo board dispatch",
                "url": f"https://linear.app/demo/issue/{ticket}/demo-board-dispatch",
                "state": {"name": status},
            },
        },
        separators=(",", ":"),
    )


def verify_gate_classification(binary: Path, repo: Path, job_id: str) -> None:
    gate = run(
        binary,
        "job",
        "gate",
        "set",
        job_id,
        "runtime-check",
        "--repo",
        repo,
        "--status",
        "fail",
        "--signature",
        "missing-binary:demo-runtime",
        "--log-ref",
        "logs/runtime-check.txt",
        "--actor",
        "demo",
        "--json",
        parse_json=True,
    )
    require_json_field(gate, "class", "infra")
    require_json_field(gate, "matched_signature", "missing_binary")
    gates = run(binary, "job", "gates", job_id, "--repo", repo, "--json", parse_json=True)
    if not any(row.get("name") == "runtime-check" and row.get("class") == "infra" for row in gates):
        raise DemoError(f"job gates did not surface infra classification: {json.dumps(gates, indent=2)}")
    triage = run(binary, "job", "triage", "--repo", repo, "--infra-only", "--json", parse_json=True)
    require_job_in_attention(triage, job_id, "gate_infra_failed")
    print(f"gate classification verified: {job_id} runtime-check class=infra")


def verify_approval_required_gate(binary: Path, repo: Path) -> str:
    created = run(
        binary,
        "pipeline",
        "run",
        "ticket_to_pr",
        "DEMO-APPROVAL",
        "Approval required demo",
        "--repo",
        repo,
        "--json",
        parse_json=True,
    )
    job_id = str(field(created, "id", "ID"))
    run(binary, "job", "step", job_id, "implement", "--skip", "--message", "demo skips implementation", "--repo", repo, "--json", parse_json=True)
    run(binary, "job", "step", job_id, "review", "--skip", "--message", "demo skips review", "--repo", repo, "--json", parse_json=True)
    blocked = run(binary, "job", "advance", job_id, "--repo", repo, "--json", parse_json=True)
    step = find_step(blocked.get("job") or blocked.get("Job") or {}, "approve")
    if field(step, "id", "ID") != "approve" or field(step, "status", "Status") != "blocked":
        raise DemoError(f"approval gate did not block at approve step: {json.dumps(blocked, indent=2)}")
    if not step.get("approval_required") and not step.get("ApprovalRequired"):
        raise DemoError(f"approve step did not report approval_required: {json.dumps(step, indent=2)}")

    body_file = repo / "approval-request.md"
    body_file.write_text("Approve the demo manual gate.\n", encoding="utf-8")
    requested = run(
        binary,
        "approval",
        "request",
        "--repo",
        repo,
        "--job",
        job_id,
        "--id",
        "demo-approval",
        "--title",
        "Demo approval",
        "--body-file",
        body_file,
        "--step",
        "approve",
        "--actor",
        "demo",
        "--requesting-instance",
        "manager",
        "--json",
        parse_json=True,
    )
    require_json_field(requested, "status", "pending")
    failed = run_expect_failure(binary, "job", "approve", job_id, "--repo", repo, "--step", "approve")
    if "requires approval" not in failed.stderr:
        raise DemoError(f"job approve did not report approval requirement:\n{failed.stderr}")

    approved = run(
        binary,
        "approval",
        "approve",
        "demo-approval",
        "--repo",
        repo,
        "--job",
        job_id,
        "--actor",
        "demo-supervisor",
        "--notes",
        "demo approved",
        "--json",
        parse_json=True,
    )
    require_json_field(approved, "status", "approved")
    # The approval decision itself is the real apply path: approving the linked
    # request must queue the gated step — no --force status mutation, no extra
    # approve command.
    shown = run(binary, "job", "show", job_id, "--repo", repo, "--json", parse_json=True)
    cleared_step = find_step(shown.get("job") or shown.get("Job") or shown, "approve")
    if field(cleared_step, "id", "ID") != "approve" or field(cleared_step, "status", "Status") != "queued":
        raise DemoError(f"approval decision did not queue the approve step: {json.dumps(shown, indent=2)}")
    advance = run(binary, "job", "advance", job_id, "--repo", repo, "--dry-run", "--json", parse_json=True)
    advanced_step = advance.get("step") or advance.get("Step") or find_step(advance.get("job") or advance.get("Job") or {}, "approve")
    if field(advanced_step, "id", "ID") != "approve":
        raise DemoError(f"approved gate is not advanceable: {json.dumps(advance, indent=2)}")
    print(f"approval-required gate verified: {job_id} blocked -> approval approved -> gate queued")
    return job_id


def verify_job_merge_dry_run(binary: Path, repo: Path) -> None:
    created = run(
        binary,
        "pipeline",
        "run",
        "ticket_to_pr",
        "DEMO-MERGE",
        "Merge dry-run demo",
        "--repo",
        repo,
        "--json",
        parse_json=True,
    )
    job_id = str(field(created, "id", "ID"))
    result = run(binary, "job", "merge", job_id, "--repo", repo, "--branch", "worker-demo-merge", "--dry-run", "--json", parse_json=True)
    require_json_field(result, "strategy", "squash")
    require_json_field(result, "action", "would_merge")
    drift = result.get("drift") or {}
    drift_class = field(drift, "classification", "Classification")
    drift_files = drift.get("files") or drift.get("Files") or []
    if drift_class != "reconcilable" or "coverage/baselines/a.json" not in drift_files:
        raise DemoError(f"merge dry-run did not classify owned-path drift: {json.dumps(result, indent=2)}")
    print(f"merge dry-run verified: {job_id} strategy=squash drift=reconcilable")


def verify_instance_brief(binary: Path, repo: Path, job_id: str) -> None:
    # Give the manager real ownership first, then assert inside the Owned Jobs
    # section itself — a job id appearing elsewhere (e.g. Unread Mailbox) must
    # not satisfy this check.
    run(binary, "job", "update", job_id, "--instance", "manager", "--repo", repo, "--json", parse_json=True)
    brief = run(binary, "instance", "brief", "manager", "--target", repo)
    require_substrings(brief, "# Instance brief: manager")
    owned = brief_section(brief, "Owned Jobs")
    if job_id not in owned or "(none)" in owned:
        raise DemoError(f"manager brief Owned Jobs section does not list {job_id}: {owned!r}")
    print(f"instance brief verified: manager owns {job_id}")


def brief_section(brief: str, heading: str) -> str:
    lines = brief.splitlines()
    out: list[str] = []
    inside = False
    for line in lines:
        if line.startswith("## "):
            if inside:
                break
            inside = line[3:].strip() == heading
            continue
        if inside:
            out.append(line)
    if not out and not inside:
        raise DemoError(f"brief has no {heading!r} section: {brief!r}")
    return "\n".join(out)


def verify_lock_queue(binary: Path, repo: Path) -> None:
    first_payload = json.dumps({"target": "worker", "name": "worker-lock-a", "ticket": "DEMO-LOCK-1"})
    second_payload = json.dumps({"target": "worker", "name": "worker-lock-b", "ticket": "DEMO-LOCK-2"})
    first = run(binary, "event", "publish", "agent.dispatch", "--payload", first_payload, "--target", repo, "--json", parse_json=True)
    if "worker" not in (first.get("matched") or []):
        raise DemoError(f"first lock dispatch did not match worker: {json.dumps(first, indent=2)}")
    second = run(binary, "event", "publish", "agent.dispatch", "--payload", second_payload, "--target", repo, "--json", parse_json=True)
    outcomes = second.get("outcomes") or []
    if not any(row.get("action") == "queued" and row.get("reason") == "lock_held" for row in outcomes):
        raise DemoError(f"second lock dispatch was not queued for lock_held: {json.dumps(second, indent=2)}")
    queued = run(binary, "queue", "ls", "--target", repo, "--reason", "lock_held", "--json", parse_json=True)
    if not any(row.get("instance_id") == "worker-lock-b" and row.get("reason") == "lock_held" for row in queued):
        raise DemoError(f"lock-held queue item not found: {json.dumps(queued, indent=2)}")
    locks = run(binary, "locks", "--repo", repo, "--json", parse_json=True)
    if not any(row.get("name") == "demo" and row.get("used") == 1 for row in locks):
        raise DemoError(f"demo lock did not show one holder: {json.dumps(locks, indent=2)}")
    run(binary, "wait", "worker-lock-a", "--target", repo, "--until", "terminal", "--timeout", "10s", "--json", parse_json=True)
    run(
        binary,
        "tick",
        "--target",
        repo,
        "--skip-schedules",
        "--skip-advance",
        "--until-idle",
        "--max-cycles",
        "3",
        "--interval",
        "0s",
        "--json",
        parse_json=True,
    )
    run(binary, "wait", "worker-lock-b", "--target", repo, "--until", "terminal", "--timeout", "10s", "--json", parse_json=True)
    remaining = run(binary, "queue", "ls", "--target", repo, "--reason", "lock_held", "--json", parse_json=True)
    if any(row.get("instance_id") == "worker-lock-b" for row in remaining):
        raise DemoError(f"lock-held queue item did not drain: {json.dumps(remaining, indent=2)}")
    print("lock queue verified: second worker queued with reason=lock_held and drained")


def verify_restart_policy(binary: Path, repo: Path, env: dict[str, str]) -> None:
    before = manager_pid(binary, repo)
    if before == 0:
        run(binary, "start", "manager", "--target", repo, "--wait", "--timeout", "5s", "--json", env=env, parse_json=True)
        before = manager_pid(binary, repo)
    if before == 0:
        raise DemoError("manager did not start for restart-policy demo")
    print(f"killing manager pid {before} to exercise restart policy")
    os.kill(before, signal.SIGKILL)
    time.sleep(0.25)
    run(binary, "daemon", "reconcile", "--target", repo, "--json", parse_json=True)
    deadline = time.time() + 10
    while time.time() < deadline:
        after = manager_pid(binary, repo)
        if after and after != before:
            print(f"restart policy verified: manager relaunched pid {before} -> {after}")
            return
        time.sleep(0.25)
        run(binary, "daemon", "reconcile", "--target", repo, "--json", parse_json=True)
    rows = run(binary, "ps", "--target", repo, "--json", parse_json=True)
    raise DemoError(f"manager was not relaunched by restart policy: {json.dumps(rows, indent=2)}")


def manager_pid(binary: Path, repo: Path) -> int:
    rows = run(binary, "ps", "--target", repo, "--json", parse_json=True)
    for row in rows:
        if field(row, "instance", "Instance") != "manager":
            continue
        if field(row, "status", "Status") != "running":
            return 0
        raw = field(row, "pid", "PID")
        try:
            return int(raw)
        except (TypeError, ValueError):
            return 0
    return 0


def configure_fake_runtime(repo: Path, runtime: str, fake_runtime: Path) -> None:
    cfg = repo / ".agent_team" / "config.toml"
    with cfg.open("a", encoding="utf-8") as f:
        f.write("\n[runtime]\n")
        f.write(f'kind = "{runtime}"\n')
        f.write(f'binary = "{toml_string(str(fake_runtime))}"\n')


def enable_demo_schedule(repo: Path) -> None:
    topology = repo / ".agent_team" / "instances.toml"
    body = topology.read_text(encoding="utf-8")
    manager_header = textwrap.dedent(
        """\
        [instances.manager]
        agent       = "manager"
        ephemeral   = false
        """
    )
    if manager_header not in body:
        raise DemoError("bundled topology no longer has the expected manager header")
    body = body.replace(manager_header, manager_header + 'restart     = "on-failure"\n', 1)
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
    worker_replicas = 'replicas      = 3\nreap_worktree = "on_merge"'
    if worker_replicas not in body:
        raise DemoError("bundled topology no longer has the expected worker replica/reap lines")
    body = body.replace(worker_replicas, 'replicas      = 3\nlocks         = ["demo"]\nreap_worktree = "on_merge"', 1)
    pipeline_header = textwrap.dedent(
        """\
        [pipelines.ticket_to_pr]
        trigger.event = "ticket.status_changed"
        trigger.match.status = "Ready for Agent"
        auto_advance  = true
        redispatch_on_reentry = false
        """
    )
    if pipeline_header not in body:
        raise DemoError("bundled topology no longer has the expected ticket_to_pr pipeline header")
    body = body.replace(
        pipeline_header,
        pipeline_header
        + textwrap.dedent(
            """\

            [pipelines.ticket_to_pr.merge]
            strategy = "squash"
            owned_paths = ["coverage/baselines"]

            [pipelines.ticket_to_pr.infra_signatures]
            missing_binary = "missing-binary:.*"
            """
        ),
        1,
    )
    approve_gate = 'gate         = "manual"\ninstructions = """'
    if approve_gate not in body:
        raise DemoError("bundled topology no longer has the expected approve gate")
    body = body.replace(approve_gate, 'gate         = "manual"\napproval_required = true\ninstructions = """', 1)
    team_schedules = 'schedules   = ["feedback-triage"]'
    if team_schedules in body:
        body = body.replace(team_schedules, 'schedules   = ["demo_due", "feedback-triage"]', 1)
    else:
        team_pipelines = 'pipelines   = ["ticket_to_pr"]'
        if team_pipelines not in body:
            raise DemoError("bundled topology no longer has the expected delivery pipeline list")
        body = body.replace(team_pipelines, team_pipelines + '\nschedules   = ["demo_due"]', 1)
    if 'schedules   = ["demo_due"]' not in body and 'schedules   = ["demo_due", "feedback-triage"]' not in body:
        raise DemoError("bundled topology no longer has the expected delivery pipeline list")
    body += textwrap.dedent(
        """\

        [locks.demo]
        slots = 1

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
                if instance.startswith("worker-lock-"):
                    time.sleep(1.0)
                else:
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
