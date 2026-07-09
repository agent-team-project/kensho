#!/usr/bin/env python3
"""Run deterministic pipeline gates in a temporary git worktree."""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
from collections import deque
from pathlib import Path
from typing import Any


SCHEMA_VERSION = 1
GATE_BLOCK_NAMES = {"agent-team-verify-gates", "verify-gates"}


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    repo = resolve_repo(args.repo)
    current_repo = resolve_current_repo()
    job_id = args.job or os.environ.get("AGENT_TEAM_JOB_ID") or ""
    pipeline_step = os.environ.get("AGENT_TEAM_PIPELINE_STEP") or "verify"
    evidence_dir = Path(args.evidence_dir).resolve() if args.evidence_dir else repo / "target" / "agent-evidence"
    evidence_dir.mkdir(parents=True, exist_ok=True)
    logs_dir = evidence_dir / "logs" / safe_name(job_id or "manual")
    logs_dir.mkdir(parents=True, exist_ok=True)

    warnings: list[str] = []
    job_data = load_job(job_id, repo, warnings) if job_id else {}
    gates = load_gates(args.gates_file, job_data, pipeline_step, repo, warnings)
    if not gates:
        print("verify: no gates declared and no default gates found", file=sys.stderr)
        return 2

    branch = args.branch or value_ci(job_data, "branch") or ""
    worker_worktree = value_ci(job_data, "worktree") or ""
    pr_url = value_ci(job_data, "pr") or ""
    commit = resolve_commit(repo, current_repo, args.commit, branch, worker_worktree, args.repo is not None, warnings)
    if not commit:
        print("verify: could not resolve worker commit", file=sys.stderr)
        return 2

    started_at = utc_now()
    temp_root = Path(tempfile.mkdtemp(prefix=f"agent-team-verify-{safe_name(job_id or 'manual')}-"))
    checkout = temp_root / "checkout"
    print(f"verify: checking out {commit} in {checkout}", flush=True)
    run_checked(["git", "-C", str(repo), "worktree", "add", "--detach", str(checkout), commit])

    results: list[dict[str, Any]] = []
    status = "pass"
    gate_evidence_root = evidence_dir / "gates" / safe_name(job_id or commit[:12])
    try:
        for index, gate in enumerate(gates, start=1):
            result = run_gate(gate, index, len(gates), checkout, logs_dir, gate_evidence_root, evidence_dir, repo)
            results.append(result)
            if result["status"] != "pass":
                status = "fail"
    finally:
        if args.keep_worktree:
            warnings.append(f"temporary worktree preserved at {checkout}")
        else:
            remove_temp_worktree(repo, checkout, temp_root, warnings)

    finished_at = utc_now()
    summary = summarize(job_id, status, results)
    evidence = {
        "schema_version": SCHEMA_VERSION,
        "job_id": job_id,
        "pipeline": os.environ.get("AGENT_TEAM_PIPELINE") or value_ci(job_data, "pipeline") or "",
        "pipeline_step": pipeline_step,
        "status": status,
        "summary": summary,
        "started_at": started_at,
        "finished_at": finished_at,
        "repo": str(repo),
        "source": {
            "branch": branch,
            "commit": commit,
            "worker_worktree": worker_worktree,
            "pr": pr_url,
        },
        "evidence_dir": str(evidence_dir),
        "gates": results,
        "warnings": warnings,
    }

    evidence_path = evidence_dir / f"{safe_name(job_id or commit[:12])}.json"
    summary_path = evidence_dir / f"{safe_name(job_id or commit[:12])}.summary.md"
    write_json(evidence_path, evidence)
    write_summary(summary_path, evidence)
    print(f"verify: wrote evidence {evidence_path}", flush=True)
    print(f"verify: wrote summary {summary_path}", flush=True)

    if not args.no_record_gates and job_id:
        record_gate_results(job_id, repo, results, warnings)
        evidence["warnings"] = warnings
        write_json(evidence_path, evidence)

    if args.complete_step:
        complete_step(job_id, pipeline_step, repo, status, summary, warnings)
        evidence["warnings"] = warnings
        write_json(evidence_path, evidence)

    print(summary, flush=True)
    return 0 if status == "pass" else 1


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--job", help="Job id. Defaults to AGENT_TEAM_JOB_ID.")
    parser.add_argument("--repo", help="Repository root containing .agent_team.")
    parser.add_argument("--branch", help="Branch/ref to verify. Defaults to the job branch.")
    parser.add_argument("--commit", help="Commit SHA/ref to verify.")
    parser.add_argument("--gates-file", help="File containing gate lines: name :: command.")
    parser.add_argument("--evidence-dir", help="Evidence output directory. Defaults to target/agent-evidence.")
    parser.add_argument("--no-record-gates", action="store_true", help="Do not call agent-team job gate set.")
    parser.add_argument("--complete-step", action="store_true", help="Mark the current pipeline step done/failed and advance on success.")
    parser.add_argument("--keep-worktree", action="store_true", help="Keep the temporary checkout for debugging.")
    return parser.parse_args(argv)


def resolve_repo(explicit: str | None) -> Path:
    if explicit:
        return Path(explicit).resolve()
    try:
        root = subprocess.check_output(
            ["git", "worktree", "list", "--porcelain"],
            text=True,
            stderr=subprocess.DEVNULL,
        )
        for line in root.splitlines():
            if line.startswith("worktree "):
                return Path(line.split(" ", 1)[1]).resolve()
    except (OSError, subprocess.CalledProcessError):
        pass
    try:
        root = subprocess.check_output(
            ["git", "rev-parse", "--show-toplevel"],
            text=True,
            stderr=subprocess.DEVNULL,
        ).strip()
        return Path(root).resolve()
    except (OSError, subprocess.CalledProcessError) as exc:
        raise SystemExit(f"verify: cannot resolve repo root: {exc}") from exc


def resolve_current_repo() -> Path | None:
    try:
        root = subprocess.check_output(
            ["git", "rev-parse", "--show-toplevel"],
            text=True,
            stderr=subprocess.DEVNULL,
        ).strip()
        return Path(root).resolve()
    except (OSError, subprocess.CalledProcessError):
        return None


def load_job(job_id: str, repo: Path, warnings: list[str]) -> dict[str, Any]:
    if not shutil.which("agent-team"):
        warnings.append("agent-team not on PATH; job metadata unavailable")
        return {}
    cmd = ["agent-team", "job", "show", job_id, "--json", "--repo", str(repo)]
    proc = subprocess.run(cmd, text=True, capture_output=True, check=False)
    if proc.returncode != 0:
        warnings.append(f"job show failed: {last_line(proc.stderr) or proc.returncode}")
        return {}
    try:
        data = json.loads(proc.stdout)
    except json.JSONDecodeError as exc:
        warnings.append(f"job show returned invalid JSON: {exc}")
        return {}
    if isinstance(data, dict):
        return data
    warnings.append("job show JSON was not an object")
    return {}


def load_gates(gates_file: str | None, job_data: dict[str, Any], pipeline_step: str, repo: Path, warnings: list[str]) -> list[dict[str, str]]:
    lines: list[str] = []
    if gates_file:
        try:
            lines = Path(gates_file).read_text().splitlines()
        except OSError as exc:
            raise SystemExit(f"verify: cannot read gates file {gates_file}: {exc}") from exc
    else:
        instructions = step_instructions(job_data, pipeline_step)
        lines = extract_gate_block(instructions)
    gates = parse_gate_lines(lines)
    if gates:
        return gates

    defaults = default_gates(repo)
    if defaults:
        warnings.append("no declared gate block found; using repository defaults")
    return defaults


def step_instructions(job_data: dict[str, Any], pipeline_step: str) -> str:
    steps = value_ci(job_data, "steps")
    if not isinstance(steps, list):
        return ""
    fallback = ""
    for step in steps:
        if not isinstance(step, dict):
            continue
        instructions = value_ci(step, "instructions") or ""
        if isinstance(instructions, str) and not fallback:
            fallback = instructions
        step_id = value_ci(step, "id")
        if isinstance(step_id, str) and step_id == pipeline_step:
            return instructions if isinstance(instructions, str) else ""
    return fallback


def extract_gate_block(instructions: str) -> list[str]:
    if not instructions:
        return []
    out: list[str] = []
    in_block = False
    for raw in instructions.splitlines():
        line = raw.strip()
        if not in_block and line.startswith("```"):
            info = line.strip("`").strip()
            if info in GATE_BLOCK_NAMES:
                in_block = True
            continue
        if in_block and line.startswith("```"):
            break
        if in_block:
            out.append(raw)
    return out


def parse_gate_lines(lines: list[str]) -> list[dict[str, str]]:
    gates: list[dict[str, str]] = []
    for raw in lines:
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if "::" in line:
            name, command = line.split("::", 1)
            name = safe_name(name.strip())
            command = command.strip()
        else:
            command = line
            name = f"gate-{len(gates) + 1}"
        if command:
            gates.append({"name": name, "command": command})
    return gates


def default_gates(repo: Path) -> list[dict[str, str]]:
    if (repo / "go.mod").exists():
        return [
            {"name": "gofmt-check", "command": 'test -z "$(gofmt -l .)"'},
            {"name": "go-vet", "command": "go vet ./..."},
            {"name": "go-test", "command": "go test ./..."},
        ]
    return []


def resolve_commit(
    repo: Path,
    current_repo: Path | None,
    explicit: str | None,
    branch: str,
    worker_worktree: str,
    explicit_repo: bool,
    warnings: list[str],
) -> str:
    if explicit:
        if current_repo and not explicit_repo:
            commit = rev_parse(current_repo, explicit)
            if commit:
                return commit
        commit = rev_parse(repo, explicit)
        if commit:
            return commit
        warnings.append(f"explicit commit/ref did not resolve: {explicit}")
    if branch:
        commit = rev_parse(repo, branch)
        if commit:
            return commit
        fetch_ref = branch.removeprefix("origin/")
        proc = subprocess.run(
            ["git", "-C", str(repo), "fetch", "origin", fetch_ref, "--depth=1"],
            text=True,
            capture_output=True,
            check=False,
        )
        if proc.returncode == 0:
            commit = rev_parse(repo, "FETCH_HEAD")
            if commit:
                return commit
        warnings.append(f"branch did not resolve locally or from origin: {branch}")
    if worker_worktree and Path(worker_worktree).exists():
        commit = rev_parse(Path(worker_worktree), "HEAD")
        if commit:
            return commit
    return rev_parse(repo, "HEAD")


def rev_parse(repo: Path, ref: str) -> str:
    proc = subprocess.run(
        ["git", "-C", str(repo), "rev-parse", "--verify", f"{ref}^{{commit}}"],
        text=True,
        capture_output=True,
        check=False,
    )
    if proc.returncode == 0:
        return proc.stdout.strip()
    return ""


def run_gate(
    gate: dict[str, str],
    index: int,
    total: int,
    checkout: Path,
    logs_dir: Path,
    gate_evidence_root: Path,
    evidence_dir: Path,
    repo: Path,
) -> dict[str, Any]:
    name = gate["name"]
    command = gate["command"]
    log_path = logs_dir / f"{safe_name(name)}.log"
    gate_evidence_dir = gate_evidence_root / safe_name(name)
    gate_evidence_dir.mkdir(parents=True, exist_ok=True)
    started = utc_now()
    start_time = time.monotonic()
    print(f"verify: [{index}/{total}] start {name}: {command}", flush=True)
    tail: deque[str] = deque(maxlen=20)
    env = os.environ.copy()
    env.setdefault("CI", "1")
    env["AGENT_TEAM_EVIDENCE_DIR"] = str(evidence_dir)
    env["AGENT_TEAM_GATE_EVIDENCE_DIR"] = str(gate_evidence_dir)
    env["AGENT_TEAM_GATE_LOG"] = str(log_path)
    env["AGENT_TEAM_GATE_NAME"] = name
    with log_path.open("w", encoding="utf-8") as log:
        proc = subprocess.Popen(
            command,
            cwd=checkout,
            shell=True,
            executable="/bin/bash",
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            encoding="utf-8",
            errors="replace",
            env=env,
        )
        assert proc.stdout is not None
        for line in proc.stdout:
            sys.stdout.write(f"{name}: {line}")
            sys.stdout.flush()
            log.write(line)
            if line.strip():
                tail.append(line.strip())
        exit_code = proc.wait()
    duration_ms = int((time.monotonic() - start_time) * 1000)
    status = "pass" if exit_code == 0 else "fail"
    signature = "" if status == "pass" else failure_signature(exit_code, list(tail))
    print(f"verify: [{index}/{total}] {status} {name} ({duration_ms}ms)", flush=True)
    result = {
        "name": name,
        "command": command,
        "status": status,
        "exit_code": exit_code,
        "started_at": started,
        "finished_at": utc_now(),
        "duration_ms": duration_ms,
        "log_path": relpath(log_path, repo),
        "signature": signature,
    }
    evidence_refs = collect_evidence_refs(gate_evidence_dir, repo)
    if evidence_refs:
        result["evidence_refs"] = evidence_refs
    return result


def record_gate_results(job_id: str, repo: Path, results: list[dict[str, Any]], warnings: list[str]) -> None:
    if not shutil.which("agent-team"):
        warnings.append("agent-team not on PATH; skipped job gate recording")
        return
    for result in results:
        cmd = [
            "agent-team",
            "job",
            "gate",
            "set",
            job_id,
            result["name"],
            "--status",
            result["status"],
            "--repo",
            str(repo),
            "--log-ref",
            result["log_path"],
        ]
        if result["signature"]:
            cmd.extend(["--signature", result["signature"]])
        proc = subprocess.run(cmd, text=True, capture_output=True, check=False)
        if proc.returncode != 0:
            warnings.append(f"gate record failed for {result['name']}: {last_line(proc.stderr) or proc.returncode}")


def complete_step(job_id: str, pipeline_step: str, repo: Path, status: str, summary: str, warnings: list[str]) -> None:
    if not job_id:
        warnings.append("--complete-step requested without a job id")
        return
    if not shutil.which("agent-team"):
        warnings.append("agent-team not on PATH; skipped step completion")
        return
    step_status = "done" if status == "pass" else "failed"
    cmd = [
        "agent-team",
        "job",
        "step",
        job_id,
        pipeline_step,
        "--status",
        step_status,
        "--message",
        summary,
        "--repo",
        str(repo),
    ]
    if status == "pass":
        cmd.append("--advance")
    proc = subprocess.run(cmd, text=True, capture_output=True, check=False)
    if proc.returncode != 0:
        message = f"step completion failed: {last_line(proc.stderr) or proc.returncode}"
        warnings.append(message)
        raise SystemExit(message)


def remove_temp_worktree(repo: Path, checkout: Path, temp_root: Path, warnings: list[str]) -> None:
    proc = subprocess.run(
        ["git", "-C", str(repo), "worktree", "remove", "--force", str(checkout)],
        text=True,
        capture_output=True,
        check=False,
    )
    if proc.returncode != 0:
        warnings.append(f"temporary worktree removal failed: {last_line(proc.stderr) or proc.returncode}")
    shutil.rmtree(temp_root, ignore_errors=True)


def summarize(job_id: str, status: str, results: list[dict[str, Any]]) -> str:
    passed = sum(1 for result in results if result["status"] == "pass")
    total = len(results)
    subject = job_id or "manual verification"
    return f"verify {status}: {passed}/{total} gates passed for {subject}"


def write_json(path: Path, data: dict[str, Any]) -> None:
    tmp = path.with_suffix(path.suffix + ".tmp")
    tmp.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    tmp.replace(path)


def write_summary(path: Path, evidence: dict[str, Any]) -> None:
    lines = [
        f"# Verification: {evidence['job_id'] or evidence['source']['commit'][:12]}",
        "",
        evidence["summary"],
        "",
        f"- Commit: `{evidence['source']['commit']}`",
        f"- Branch: `{evidence['source']['branch']}`",
        f"- Status: `{evidence['status']}`",
        "",
        "| Gate | Status | Duration | Log | Evidence |",
        "| --- | --- | ---: | --- | --- |",
    ]
    for gate in evidence["gates"]:
        evidence_refs = gate.get("evidence_refs") or []
        evidence_text = ", ".join(f"`{ref}`" for ref in evidence_refs) if evidence_refs else "-"
        lines.append(
            f"| `{gate['name']}` | `{gate['status']}` | {gate['duration_ms']}ms | `{gate['log_path']}` | {evidence_text} |"
        )
    lines.append("")
    path.write_text("\n".join(lines), encoding="utf-8")


def collect_evidence_refs(directory: Path, repo: Path) -> list[str]:
    if not directory.exists():
        return []
    refs: list[str] = []
    for path in sorted(directory.rglob("*")):
        if path.is_file():
            refs.append(relpath(path, repo))
    return refs


def run_checked(cmd: list[str]) -> None:
    proc = subprocess.run(cmd, text=True, capture_output=True, check=False)
    if proc.returncode != 0:
        raise SystemExit(f"verify: command failed: {' '.join(cmd)}\n{proc.stderr}")


def value_ci(data: dict[str, Any], key: str) -> Any:
    if not isinstance(data, dict):
        return None
    lowered = key.lower()
    for current_key, value in data.items():
        if current_key.lower() == lowered:
            return value
    return None


def utc_now() -> str:
    return dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")


def safe_name(value: str) -> str:
    safe = re.sub(r"[^A-Za-z0-9_.-]+", "-", value.strip()).strip("-").lower()
    return safe or "unnamed"


def relpath(path: Path, root: Path) -> str:
    try:
        return str(path.relative_to(root))
    except ValueError:
        return str(path)


def failure_signature(exit_code: int, tail: list[str]) -> str:
    if tail:
        text = tail[-1]
    else:
        text = f"exit {exit_code}"
    text = re.sub(r"\s+", " ", text).strip()
    return text[:200]


def last_line(text: str) -> str:
    for line in reversed(text.splitlines()):
        stripped = line.strip()
        if stripped:
            return stripped
    return ""


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
