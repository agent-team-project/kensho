#!/usr/bin/env python3
"""Build a requirements traceability matrix from local agent-team records."""

from __future__ import annotations

import argparse
import datetime as dt
import glob
import json
import os
import re
import subprocess
import sys
import tomllib
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any


SCHEMA_VERSION = 1
DEFAULT_SPEC_CANDIDATES = (
    "SPEC.md",
    "SPECIFICATION.md",
    "REQUIREMENTS.md",
    "BACKLOG.md",
    "backlog.md",
    "backlog.json",
    "docs/SPEC.md",
    "docs/spec.md",
    "docs/REQUIREMENTS.md",
    "docs/requirements.md",
    "docs/BACKLOG.md",
    "docs/backlog.md",
    "docs/backlog.json",
    "documentation/SPEC.md",
    "documentation/spec.md",
    "documentation/REQUIREMENTS.md",
    "documentation/requirements.md",
    "documentation/BACKLOG.md",
    "documentation/backlog.md",
    "documentation/backlog.json",
)
REQ_ID_RE = re.compile(r"\b[A-Z][A-Z0-9]{1,12}-[A-Za-z0-9][A-Za-z0-9_.-]*\b")
BULLET_RE = re.compile(r"^\s*(?:[-*+]|\d+[.)])\s+(?:\[[ xX]\]\s+)?(.+?)\s*$")
HEADING_RE = re.compile(r"^(#{1,6})\s+(.+?)\s*$")
STOP_WORDS = {
    "a",
    "an",
    "and",
    "are",
    "as",
    "at",
    "be",
    "by",
    "for",
    "from",
    "has",
    "have",
    "in",
    "into",
    "is",
    "it",
    "its",
    "of",
    "on",
    "or",
    "that",
    "the",
    "their",
    "this",
    "to",
    "with",
}
NORMATIVE_WORDS = {
    "accept",
    "acceptance",
    "allow",
    "build",
    "capture",
    "emit",
    "ensure",
    "expose",
    "include",
    "list",
    "map",
    "must",
    "need",
    "needs",
    "provide",
    "record",
    "require",
    "requirement",
    "shall",
    "should",
    "surface",
    "support",
}


@dataclass
class Requirement:
    req_id: str
    title: str
    body: str
    source: str
    line: int = 0
    explicit_id: bool = False

    def text(self) -> str:
        return " ".join(part for part in (self.req_id, self.title, self.body) if part)


@dataclass
class GateRecord:
    name: str
    status: str
    source: str
    ts: str = ""
    signature: str = ""
    log_ref: str = ""
    actor: str = ""


@dataclass
class EvidenceRecord:
    path: str
    status: str = ""
    summary: str = ""
    gates: list[GateRecord] = field(default_factory=list)


@dataclass
class JobRecord:
    job_id: str
    path: Path
    status: str = ""
    ticket: str = ""
    ticket_url: str = ""
    epic: str = ""
    branch: str = ""
    pr: str = ""
    kickoff: str = ""
    target: str = ""
    pipeline: str = ""
    steps: list[dict[str, Any]] = field(default_factory=list)
    gates: list[GateRecord] = field(default_factory=list)
    evidence: list[EvidenceRecord] = field(default_factory=list)

    def search_text(self) -> str:
        parts: list[str] = [
            self.job_id,
            self.status,
            self.ticket,
            self.ticket_url,
            self.epic,
            self.branch,
            self.pr,
            self.kickoff,
            self.target,
            self.pipeline,
        ]
        for step in self.steps:
            if not isinstance(step, dict):
                continue
            for key in ("id", "target", "status", "instructions", "description", "label"):
                value = value_ci(step, key)
                if isinstance(value, str):
                    parts.append(value)
        return "\n".join(part for part in parts if part)

def main(argv: list[str]) -> int:
    args = parse_args(argv)
    repo = resolve_repo(args.repo)
    warnings: list[str] = []
    spec_paths = resolve_spec_paths(repo, args.spec, args.spec_glob, warnings)
    evidence_dir = Path(args.evidence_dir).resolve() if args.evidence_dir else repo / "target" / "agent-evidence"

    report = build_report(repo, spec_paths, evidence_dir, warnings)
    text = json.dumps(report, indent=2, sort_keys=True) + "\n" if args.json else render_markdown(report)

    if args.output:
        output_path = Path(args.output)
        if not output_path.is_absolute():
            output_path = repo / output_path
        output_path.parent.mkdir(parents=True, exist_ok=True)
        output_path.write_text(text, encoding="utf-8")
    else:
        sys.stdout.write(text)

    if args.fail_on_gap and report["summary"]["pending_gap_count"] > 0:
        return 1
    return 0


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", help="Repository root containing .agent_team. Defaults to the main worktree root.")
    parser.add_argument("--spec", action="append", default=[], help="SPEC/backlog file or directory to read. May be repeated.")
    parser.add_argument("--spec-glob", action="append", default=[], help="Repo-relative glob for SPEC/backlog files. May be repeated.")
    parser.add_argument("--evidence-dir", help="Verifier evidence directory. Defaults to target/agent-evidence.")
    parser.add_argument("--output", help="Write the matrix to this path instead of stdout.")
    parser.add_argument("--json", action="store_true", help="Emit structured JSON instead of Markdown.")
    parser.add_argument("--fail-on-gap", action="store_true", help="Exit 1 when any requirement has a pending gap.")
    return parser.parse_args(argv)


def build_report(repo: Path, spec_paths: list[Path], evidence_dir: Path, warnings: list[str] | None = None) -> dict[str, Any]:
    warnings = warnings if warnings is not None else []
    requirements = load_requirements(spec_paths, repo, warnings)
    jobs = load_jobs(repo, evidence_dir, warnings)
    matrix = build_matrix(requirements, jobs, warnings)
    unmatched_jobs = untraced_jobs(jobs, matrix)

    counts = {"delivered": 0, "unproven": 0, "specified": 0, "gap": 0}
    pending_gap_count = 0
    for row in matrix:
        counts[row["status"]] = counts.get(row["status"], 0) + 1
        if row["pending_gaps"]:
            pending_gap_count += 1

    return {
        "schema_version": SCHEMA_VERSION,
        "generated_at": utc_now(),
        "repo": str(repo),
        "specs": [str(path) for path in spec_paths],
        "summary": {
            "requirements": len(requirements),
            "jobs": len(jobs),
            "unmatched_jobs": len(unmatched_jobs),
            "pending_gap_count": pending_gap_count,
            "statuses": counts,
        },
        "requirements": matrix,
        "unmatched_jobs": unmatched_jobs,
        "warnings": warnings,
    }


def resolve_repo(explicit: str | None) -> Path:
    if explicit:
        return Path(explicit).resolve()
    try:
        output = subprocess.check_output(
            ["git", "worktree", "list", "--porcelain"],
            text=True,
            stderr=subprocess.DEVNULL,
        )
        for line in output.splitlines():
            if line.startswith("worktree "):
                return Path(line.split(" ", 1)[1]).resolve()
    except (OSError, subprocess.CalledProcessError):
        pass
    try:
        output = subprocess.check_output(
            ["git", "rev-parse", "--show-toplevel"],
            text=True,
            stderr=subprocess.DEVNULL,
        ).strip()
        return Path(output).resolve()
    except (OSError, subprocess.CalledProcessError) as exc:
        raise SystemExit(f"traceability: cannot resolve repo root: {exc}") from exc


def resolve_spec_paths(repo: Path, explicit: list[str], globs: list[str], warnings: list[str]) -> list[Path]:
    paths: list[Path] = []
    for raw in explicit:
        path = Path(raw)
        if not path.is_absolute():
            path = repo / path
        paths.extend(expand_spec_path(path, warnings))
    for pattern in globs:
        for raw in sorted(glob.glob(str(repo / pattern), recursive=True)):
            paths.extend(expand_spec_path(Path(raw), warnings))
    if not explicit and not globs:
        for candidate in DEFAULT_SPEC_CANDIDATES:
            path = repo / candidate
            if path.exists():
                paths.extend(expand_spec_path(path, warnings))
    return unique_existing_files(paths)


def expand_spec_path(path: Path, warnings: list[str]) -> list[Path]:
    if path.is_dir():
        out: list[Path] = []
        for child in sorted(path.rglob("*")):
            if child.is_file() and child.suffix.lower() in {".md", ".markdown", ".json"}:
                out.append(child)
        return out
    if path.exists() and path.is_file():
        return [path]
    warnings.append(f"spec path not found: {path}")
    return []


def unique_existing_files(paths: list[Path]) -> list[Path]:
    seen: set[Path] = set()
    out: list[Path] = []
    for path in paths:
        resolved = path.resolve()
        if resolved in seen or not resolved.is_file():
            continue
        seen.add(resolved)
        out.append(resolved)
    return out


def load_requirements(spec_paths: list[Path], repo: Path, warnings: list[str]) -> list[Requirement]:
    requirements: list[Requirement] = []
    if not spec_paths:
        requirements.append(
            Requirement(
                req_id="SPEC",
                title="No SPEC or backlog requirements found",
                body="Pass --spec or add a SPEC/BACKLOG file so jobs can be traced.",
                source=str(repo),
                explicit_id=True,
            )
        )
        warnings.append("no SPEC/backlog files found; emitted a synthetic SPEC gap")
        return requirements

    for path in spec_paths:
        suffix = path.suffix.lower()
        try:
            if suffix == ".json":
                requirements.extend(parse_json_requirements(path, repo, warnings))
            else:
                requirements.extend(parse_markdown_requirements(path, repo, warnings))
        except OSError as exc:
            warnings.append(f"could not read {path}: {exc}")
        except json.JSONDecodeError as exc:
            warnings.append(f"could not parse JSON {path}: {exc}")

    return dedupe_requirements(requirements)


def parse_markdown_requirements(path: Path, repo: Path, warnings: list[str]) -> list[Requirement]:
    rel = relpath(path, repo)
    requirements: list[Requirement] = []
    section_stack: list[str] = []
    normative_section = False
    for line_no, raw in enumerate(path.read_text(encoding="utf-8").splitlines(), start=1):
        line = raw.strip()
        if not line:
            continue
        heading = HEADING_RE.match(line)
        if heading:
            level = len(heading.group(1))
            title = clean_markdown(heading.group(2))
            section_stack = section_stack[: level - 1]
            section_stack.append(title)
            normative_section = is_normative_text(title)
            ids = find_ids(title)
            if ids:
                requirements.append(
                    Requirement(
                        req_id=ids[0],
                        title=strip_leading_id(title, ids[0]),
                        body=" > ".join(section_stack[:-1]),
                        source=rel,
                        line=line_no,
                        explicit_id=True,
                    )
                )
            continue
        bullet = BULLET_RE.match(raw)
        if not bullet:
            continue
        text = clean_markdown(bullet.group(1))
        ids = find_ids(text)
        if not ids and not normative_section and not is_normative_text(text):
            continue
        req_id = ids[0] if ids else generated_req_id(rel, line_no)
        requirements.append(
            Requirement(
                req_id=req_id,
                title=strip_leading_id(text, req_id) if ids else text,
                body=" > ".join(section_stack),
                source=rel,
                line=line_no,
                explicit_id=bool(ids),
            )
        )
    if not requirements:
        warnings.append(f"{rel}: no requirement-like Markdown bullets or headings found")
    return requirements


def parse_json_requirements(path: Path, repo: Path, warnings: list[str]) -> list[Requirement]:
    rel = relpath(path, repo)
    raw = json.loads(path.read_text(encoding="utf-8"))
    items = json_requirement_items(raw)
    requirements: list[Requirement] = []
    for index, item in enumerate(items, start=1):
        if isinstance(item, str):
            text = item.strip()
            if text:
                requirements.append(
                    Requirement(
                        req_id=first_id_or_generated(text, rel, index),
                        title=strip_leading_id(text, first_id_or_generated(text, rel, index)),
                        body="",
                        source=rel,
                        explicit_id=bool(find_ids(text)),
                    )
                )
            continue
        if not isinstance(item, dict):
            continue
        req_id = first_string(item, "id", "key", "identifier", "ticket", "name") or generated_req_id(rel, index)
        title = first_string(item, "title", "summary", "name", "text") or req_id
        body_parts = [first_string(item, "description", "body", "details") or ""]
        requirements.append(
            Requirement(
                req_id=req_id,
                title=title,
                body="\n".join(part for part in body_parts if part),
                source=rel,
                explicit_id=bool(first_string(item, "id", "key", "identifier", "ticket")),
            )
        )
        for ac_index, criterion in enumerate(json_criteria(item), start=1):
            text = str(criterion).strip()
            if not text:
                continue
            criterion_ids = find_ids(text)
            child_id = criterion_ids[0] if criterion_ids else f"{req_id}.AC{ac_index}"
            requirements.append(
                Requirement(
                    req_id=child_id,
                    title=strip_leading_id(text, child_id) if criterion_ids else text,
                    body=f"Acceptance criterion for {req_id}",
                    source=rel,
                    explicit_id=bool(criterion_ids),
                )
            )
    if not requirements:
        warnings.append(f"{rel}: no JSON requirements found")
    return requirements


def json_requirement_items(raw: Any) -> list[Any]:
    if isinstance(raw, list):
        return raw
    if not isinstance(raw, dict):
        return []
    for key in ("requirements", "items", "work_items", "workItems", "backlog"):
        value = raw.get(key)
        if isinstance(value, list):
            return value
    return []


def json_criteria(item: dict[str, Any]) -> list[Any]:
    for key in ("acceptance_criteria", "acceptanceCriteria", "criteria", "checks"):
        value = item.get(key)
        if isinstance(value, list):
            return value
        if isinstance(value, str):
            return [line.strip("-* ") for line in value.splitlines() if line.strip()]
    return []


def dedupe_requirements(requirements: list[Requirement]) -> list[Requirement]:
    seen: set[tuple[str, str]] = set()
    out: list[Requirement] = []
    for req in requirements:
        key = (req.req_id.lower(), normalize_space(req.title).lower())
        if key in seen:
            continue
        seen.add(key)
        out.append(req)
    return out


def load_jobs(repo: Path, evidence_dir: Path, warnings: list[str]) -> list[JobRecord]:
    team_dir = repo / ".agent_team"
    jobs_dir = team_dir / "jobs"
    if not jobs_dir.is_dir():
        warnings.append(f"job directory not found: {jobs_dir}")
        return []
    evidence_by_job = load_evidence(evidence_dir, repo, warnings)
    jobs: list[JobRecord] = []
    for path in sorted(jobs_dir.glob("*.toml")):
        if path.name.endswith(".gates.toml"):
            continue
        try:
            raw = tomllib.loads(path.read_text(encoding="utf-8"))
        except (OSError, tomllib.TOMLDecodeError) as exc:
            warnings.append(f"could not parse job record {relpath(path, repo)}: {exc}")
            continue
        job_id = str(value_ci(raw, "id") or path.stem)
        job = JobRecord(
            job_id=job_id,
            path=path,
            status=str(value_ci(raw, "status") or ""),
            ticket=str(value_ci(raw, "ticket") or ""),
            ticket_url=str(value_ci(raw, "ticket_url") or ""),
            epic=str(value_ci(raw, "epic") or ""),
            branch=str(value_ci(raw, "branch") or ""),
            pr=str(value_ci(raw, "pr") or ""),
            kickoff=str(value_ci(raw, "kickoff") or ""),
            target=str(value_ci(raw, "target") or ""),
            pipeline=str(value_ci(raw, "pipeline") or ""),
            steps=value_ci(raw, "steps") if isinstance(value_ci(raw, "steps"), list) else [],
        )
        job.gates = load_gate_records(jobs_dir / f"{safe_name(job_id)}.gates.jsonl", repo, warnings)
        job.evidence = evidence_for_job(evidence_by_job, job_id)
        jobs.append(job)
    return jobs


def evidence_for_job(evidence_by_job: dict[str, list[EvidenceRecord]], job_id: str) -> list[EvidenceRecord]:
    seen: set[str] = set()
    out: list[EvidenceRecord] = []
    for key in {job_id.lower(), safe_name(job_id)}:
        for evidence in evidence_by_job.get(key, []):
            if evidence.path in seen:
                continue
            seen.add(evidence.path)
            out.append(evidence)
    return out


def load_gate_records(path: Path, repo: Path, warnings: list[str]) -> list[GateRecord]:
    if not path.exists():
        return []
    records: list[GateRecord] = []
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except OSError as exc:
        warnings.append(f"could not read gate ledger {relpath(path, repo)}: {exc}")
        return records
    for line_no, line in enumerate(lines, start=1):
        raw = line.strip()
        if not raw:
            continue
        try:
            data = json.loads(raw)
        except json.JSONDecodeError as exc:
            warnings.append(f"{relpath(path, repo)} line {line_no}: invalid gate JSON: {exc}")
            continue
        if not isinstance(data, dict):
            continue
        records.append(
            GateRecord(
                name=str(value_ci(data, "name") or ""),
                status=str(value_ci(data, "status") or ""),
                ts=str(value_ci(data, "ts") or ""),
                signature=str(value_ci(data, "signature") or ""),
                log_ref=str(value_ci(data, "log_ref") or ""),
                actor=str(value_ci(data, "actor") or ""),
                source=relpath(path, repo),
            )
        )
    return records


def load_evidence(evidence_dir: Path, repo: Path, warnings: list[str]) -> dict[str, list[EvidenceRecord]]:
    out: dict[str, list[EvidenceRecord]] = {}
    if not evidence_dir.is_dir():
        return out
    for path in sorted(evidence_dir.rglob("*.json")):
        if "/logs/" in path.as_posix():
            continue
        try:
            data = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as exc:
            warnings.append(f"could not parse evidence {relpath(path, repo)}: {exc}")
            continue
        if not isinstance(data, dict):
            continue
        job_id = str(value_ci(data, "job_id") or path.stem)
        evidence = EvidenceRecord(
            path=relpath(path, repo),
            status=str(value_ci(data, "status") or ""),
            summary=str(value_ci(data, "summary") or ""),
            gates=evidence_gates(data, relpath(path, repo)),
        )
        for key in {job_id.lower(), safe_name(job_id), path.stem.lower()}:
            out.setdefault(key, []).append(evidence)
    return out


def evidence_gates(data: dict[str, Any], source: str) -> list[GateRecord]:
    gates = value_ci(data, "gates")
    if not isinstance(gates, list):
        return []
    out: list[GateRecord] = []
    for gate in gates:
        if not isinstance(gate, dict):
            continue
        out.append(
            GateRecord(
                name=str(value_ci(gate, "name") or ""),
                status=str(value_ci(gate, "status") or ""),
                ts=str(value_ci(gate, "finished_at") or value_ci(gate, "started_at") or ""),
                signature=str(value_ci(gate, "signature") or ""),
                log_ref=str(value_ci(gate, "log_path") or value_ci(gate, "log_ref") or ""),
                source=source,
            )
        )
    return out


def build_matrix(requirements: list[Requirement], jobs: list[JobRecord], warnings: list[str]) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    for req in requirements:
        matches = match_requirement(req, jobs)
        status, pending_gaps = classify_requirement(req, matches)
        rows.append(
            {
                "id": req.req_id,
                "title": req.title,
                "source": source_ref(req),
                "status": status,
                "jobs": [job_summary(job, reason) for job, reason in matches],
                "gates": requirement_gate_summaries([job for job, _reason in matches]),
                "evidence": requirement_evidence_summaries([job for job, _reason in matches]),
                "pending_gaps": pending_gaps,
            }
        )
    if not rows:
        warnings.append("no requirements loaded; matrix is empty")
    return rows


def match_requirement(req: Requirement, jobs: list[JobRecord]) -> list[tuple[JobRecord, str]]:
    if req.req_id == "SPEC" and req.title == "No SPEC or backlog requirements found":
        return []
    matches: list[tuple[JobRecord, str]] = []
    req_ids = [req.req_id] if req.explicit_id else []
    req_tokens = tokenize(req.text())
    for job in jobs:
        text = job.search_text()
        text_lower = text.lower()
        explicit = next((req_id for req_id in req_ids if req_id and req_id.lower() in text_lower), "")
        if explicit:
            matches.append((job, f"explicit id {explicit}"))
            continue
        score = token_score(req_tokens, tokenize(text))
        if score >= 1.0:
            matches.append((job, "strong text match"))
        elif score >= 0.65 and len(req_tokens) >= 4:
            matches.append((job, "text match"))
    matches.sort(key=lambda item: (match_rank(item[1]), item[0].job_id))
    return matches


def token_score(req_tokens: set[str], job_tokens: set[str]) -> float:
    if not req_tokens or not job_tokens:
        return 0.0
    overlap = req_tokens & job_tokens
    if len(overlap) < min(3, len(req_tokens)):
        return 0.0
    denominator = min(len(req_tokens), 8)
    return len(overlap) / denominator


def match_rank(reason: str) -> int:
    if reason.startswith("explicit"):
        return 0
    if reason.startswith("strong"):
        return 1
    return 2


def classify_requirement(req: Requirement, matches: list[tuple[JobRecord, str]]) -> tuple[str, list[str]]:
    if req.req_id == "SPEC" and not matches:
        return "gap", ["PENDING GAP: no SPEC/backlog requirements were found"]
    if not matches:
        return "gap", ["PENDING GAP: no job maps to this requirement"]

    jobs = [job for job, _reason in matches]
    proven_jobs = [job for job in jobs if has_delivery(job) and proof_status(job) == "pass"]
    if proven_jobs:
        return "delivered", []

    delivery_jobs = [job for job in jobs if has_delivery(job) or is_terminal(job)]
    if delivery_jobs:
        gaps: list[str] = []
        for job in delivery_jobs:
            proof = proof_status(job)
            if proof == "fail":
                gaps.append(f"PENDING GAP: {job.job_id} has failing gate/evidence proof")
            else:
                gaps.append(f"PENDING GAP: {job.job_id} has no passing gate/evidence proof")
        return "unproven", sorted(set(gaps))

    gaps = [f"PENDING GAP: {job.job_id} is {job.status or 'not terminal'} with no delivery artifact" for job in jobs]
    return "specified", sorted(set(gaps))


def has_delivery(job: JobRecord) -> bool:
    return bool(job.pr or job.branch)


def is_terminal(job: JobRecord) -> bool:
    return job.status.strip().lower() in {"done", "complete", "completed", "merged", "failed", "cancelled", "canceled"}


def proof_status(job: JobRecord) -> str:
    gates = latest_gates(job.gates)
    evidence_statuses = [evidence.status.strip().lower() for evidence in job.evidence if evidence.status.strip()]
    evidence_gates = latest_gates([gate for evidence in job.evidence for gate in evidence.gates])
    all_gates = list(gates.values()) + list(evidence_gates.values())
    if any(gate.status.strip().lower() == "fail" for gate in all_gates):
        return "fail"
    if all_gates and all(gate.status.strip().lower() == "pass" for gate in all_gates):
        return "pass"
    if any(status == "fail" for status in evidence_statuses):
        return "fail"
    if any(status == "pass" for status in evidence_statuses):
        return "pass"
    return "none"


def latest_gates(gates: list[GateRecord]) -> dict[str, GateRecord]:
    latest: dict[str, GateRecord] = {}
    for gate in gates:
        name = gate.name.strip()
        if not name:
            continue
        existing = latest.get(name)
        if existing is None or gate.ts >= existing.ts:
            latest[name] = gate
    return latest


def job_summary(job: JobRecord, reason: str) -> dict[str, Any]:
    return {
        "id": job.job_id,
        "status": job.status,
        "ticket": job.ticket,
        "pr": job.pr,
        "branch": job.branch,
        "proof_status": proof_status(job),
        "match": reason,
    }


def requirement_gate_summaries(jobs: list[JobRecord]) -> list[dict[str, str]]:
    out: list[dict[str, str]] = []
    for job in jobs:
        for gate in latest_gates(job.gates).values():
            out.append(
                {
                    "job": job.job_id,
                    "name": gate.name,
                    "status": gate.status,
                    "signature": gate.signature,
                    "log_ref": gate.log_ref,
                    "source": gate.source,
                }
            )
    return out


def requirement_evidence_summaries(jobs: list[JobRecord]) -> list[dict[str, str]]:
    out: list[dict[str, str]] = []
    for job in jobs:
        for evidence in job.evidence:
            out.append(
                {
                    "job": job.job_id,
                    "status": evidence.status,
                    "summary": evidence.summary,
                    "path": evidence.path,
                }
            )
    return out


def untraced_jobs(jobs: list[JobRecord], matrix: list[dict[str, Any]]) -> list[dict[str, Any]]:
    traced = {
        job["id"]
        for row in matrix
        for job in row["jobs"]
        if isinstance(job, dict) and isinstance(job.get("id"), str)
    }
    out: list[dict[str, Any]] = []
    for job in jobs:
        if job.job_id in traced:
            continue
        out.append(
            {
                "id": job.job_id,
                "status": job.status,
                "ticket": job.ticket,
                "pr": job.pr,
                "branch": job.branch,
                "proof_status": proof_status(job),
            }
        )
    return out


def render_markdown(report: dict[str, Any]) -> str:
    summary = report["summary"]
    lines = [
        "# Requirements Traceability Matrix",
        "",
        f"- Generated: `{report['generated_at']}`",
        f"- Repo: `{report['repo']}`",
        f"- Specs: {format_specs(report['specs'])}",
        f"- Jobs inspected: `{summary['jobs']}`",
        f"- Pending gaps: `{summary['pending_gap_count']}`",
        "",
        "| Requirement | Source | Status | Jobs | Gates / Evidence | Pending gap |",
        "| --- | --- | --- | --- | --- | --- |",
    ]
    for row in report["requirements"]:
        lines.append(
            "| {req} | {source} | `{status}` | {jobs} | {proof} | {gaps} |".format(
                req=escape_cell(f"`{row['id']}` {row['title']}"),
                source=escape_cell(row["source"]),
                status=row["status"],
                jobs=escape_cell(format_jobs(row["jobs"])),
                proof=escape_cell(format_proof(row["gates"], row["evidence"])),
                gaps=escape_cell(format_gaps(row["pending_gaps"])),
            )
        )
    lines.append("")
    pending = [row for row in report["requirements"] if row["pending_gaps"]]
    if pending:
        lines.extend(["## Pending Gaps", ""])
        for row in pending:
            for gap in row["pending_gaps"]:
                lines.append(f"- `{row['id']}`: {gap}")
        lines.append("")
    unmatched = report["unmatched_jobs"]
    if unmatched:
        lines.extend(["## Untraced Jobs", ""])
        for job in unmatched:
            refs = ", ".join(ref for ref in (job.get("pr"), job.get("branch")) if ref) or "no delivery ref"
            lines.append(f"- `{job['id']}` (`{job.get('status') or 'unknown'}`, proof `{job['proof_status']}`): {refs}")
        lines.append("")
    warnings = report["warnings"]
    if warnings:
        lines.extend(["## Warnings", ""])
        for warning in warnings:
            lines.append(f"- {warning}")
        lines.append("")
    return "\n".join(lines)


def format_specs(specs: list[str]) -> str:
    if not specs:
        return "`none`"
    return ", ".join(f"`{spec}`" for spec in specs)


def format_jobs(jobs: list[dict[str, Any]]) -> str:
    if not jobs:
        return "none"
    parts = []
    for job in jobs:
        refs = []
        if job.get("pr"):
            refs.append(job["pr"])
        if job.get("branch"):
            refs.append(job["branch"])
        ref_text = f" ({', '.join(refs)})" if refs else ""
        parts.append(f"`{job['id']}` status `{job.get('status') or 'unknown'}` proof `{job['proof_status']}`{ref_text}")
    return "<br>".join(parts)


def format_proof(gates: list[dict[str, str]], evidence: list[dict[str, str]]) -> str:
    parts = []
    for gate in gates:
        detail = f"`{gate['job']}:{gate['name']}={gate['status']}`"
        if gate.get("log_ref"):
            detail += f" log `{gate['log_ref']}`"
        parts.append(detail)
    for item in evidence:
        detail = f"`{item['job']}:evidence={item.get('status') or 'present'}` `{item['path']}`"
        parts.append(detail)
    return "<br>".join(parts) if parts else "none"


def format_gaps(gaps: list[str]) -> str:
    return "<br>".join(gaps) if gaps else ""


def escape_cell(value: str) -> str:
    return value.replace("|", "\\|").replace("\n", "<br>")


def source_ref(req: Requirement) -> str:
    if req.line:
        return f"{req.source}:{req.line}"
    return req.source


def first_string(data: dict[str, Any], *keys: str) -> str:
    for key in keys:
        value = data.get(key)
        if isinstance(value, str) and value.strip():
            return value.strip()
        if value is not None and not isinstance(value, (dict, list)):
            return str(value).strip()
    return ""


def value_ci(data: dict[str, Any], key: str) -> Any:
    if not isinstance(data, dict):
        return None
    lowered = key.lower()
    for current_key, value in data.items():
        if current_key.lower() == lowered:
            return value
    return None


def find_ids(text: str) -> list[str]:
    return REQ_ID_RE.findall(text)


def first_id_or_generated(text: str, source: str, index: int) -> str:
    ids = find_ids(text)
    return ids[0] if ids else generated_req_id(source, index)


def generated_req_id(source: str, line_or_index: int) -> str:
    stem = safe_name(Path(source).stem).upper() or "REQ"
    return f"{stem}-{line_or_index}"


def strip_leading_id(text: str, req_id: str) -> str:
    stripped = text.strip()
    if stripped.lower().startswith(req_id.lower()):
        stripped = stripped[len(req_id) :].lstrip(" :-")
    return stripped or text


def clean_markdown(text: str) -> str:
    text = re.sub(r"`([^`]*)`", r"\1", text)
    text = re.sub(r"\[([^\]]+)\]\([^)]+\)", r"\1", text)
    return normalize_space(text.strip())


def is_normative_text(text: str) -> bool:
    tokens = tokenize(text)
    return bool(tokens & NORMATIVE_WORDS)


def tokenize(text: str) -> set[str]:
    return {
        token
        for token in re.findall(r"[A-Za-z0-9][A-Za-z0-9_.-]*", text.lower())
        if len(token) > 2 and token not in STOP_WORDS
    }


def normalize_space(text: str) -> str:
    return re.sub(r"\s+", " ", text).strip()


def safe_name(value: str) -> str:
    safe = re.sub(r"[^A-Za-z0-9_.-]+", "-", value.strip()).strip("-").lower()
    return safe or "unnamed"


def relpath(path: Path, root: Path) -> str:
    try:
        return str(path.relative_to(root))
    except ValueError:
        return str(path)


def utc_now() -> str:
    return dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
