#!/usr/bin/env python3
"""Tests for the bundled traceability matrix helper."""

from __future__ import annotations

import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[3]
SCRIPT = REPO_ROOT / "template" / "skills" / "traceability" / "scripts" / "traceability.py"
SPEC = importlib.util.spec_from_file_location("traceability", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
traceability = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = traceability
SPEC.loader.exec_module(traceability)


class TraceabilityTests(unittest.TestCase):
    def test_delivered_and_gap_rows_from_markdown_spec(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp)
            write_spec(
                repo,
                """
                # SPEC

                ## Requirements

                - [ ] REQ-1: Build a traceability matrix from jobs and gates.
                - [ ] REQ-2: Surface pending gaps explicitly.
                """,
            )
            write_job(
                repo,
                "req-1-impl",
                kickoff="Implements REQ-1 by building a traceability matrix from jobs and gates.",
                status="done",
                pr="https://github.com/acme/widgets/pull/1",
            )
            write_gate(repo, "req-1-impl", "tests", "pass")

            warnings: list[str] = []
            report = traceability.build_report(repo, [repo / "SPEC.md"], repo / "target" / "agent-evidence", warnings)
            rows = {row["id"]: row for row in report["requirements"]}

            self.assertEqual(rows["REQ-1"]["status"], "delivered")
            self.assertEqual(rows["REQ-1"]["pending_gaps"], [])
            self.assertEqual(rows["REQ-2"]["status"], "gap")
            self.assertIn("no job maps", rows["REQ-2"]["pending_gaps"][0])
            self.assertEqual(report["summary"]["pending_gap_count"], 1)

    def test_delivery_without_proof_is_unproven(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp)
            write_spec(repo, "- REQ-7: Emit JSON output for dashboard ingestion.\n")
            write_job(
                repo,
                "json-output",
                kickoff="REQ-7 Emit JSON output for dashboard ingestion.",
                status="running",
                branch="req-7-json",
            )

            report = traceability.build_report(repo, [repo / "SPEC.md"], repo / "target" / "agent-evidence", [])
            row = report["requirements"][0]

            self.assertEqual(row["status"], "unproven")
            self.assertEqual(row["jobs"][0]["proof_status"], "none")
            self.assertIn("no passing gate", row["pending_gaps"][0])

    def test_json_backlog_acceptance_criteria_can_match_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp)
            write_backlog(
                repo,
                {
                    "items": [
                        {
                            "id": "REQ-9",
                            "title": "Record verifier evidence",
                            "acceptance_criteria": ["REQ-9.AC1: Evidence path is listed in the matrix"],
                        }
                    ]
                },
            )
            write_job(
                repo,
                "req-9-evidence",
                kickoff="REQ-9 and REQ-9.AC1 record verifier evidence and list the evidence path.",
                status="done",
                pr="https://github.com/acme/widgets/pull/9",
            )
            evidence_dir = repo / "target" / "agent-evidence"
            evidence_dir.mkdir(parents=True)
            (evidence_dir / "req-9-evidence.json").write_text(
                json.dumps(
                    {
                        "job_id": "req-9-evidence",
                        "status": "pass",
                        "summary": "verify pass",
                        "gates": [{"name": "go-test", "status": "pass", "log_path": "target/log.txt"}],
                    }
                ),
                encoding="utf-8",
            )

            report = traceability.build_report(repo, [repo / "backlog.json"], evidence_dir, [])
            rows = {row["id"]: row for row in report["requirements"]}

            self.assertEqual(rows["REQ-9"]["status"], "delivered")
            self.assertEqual(rows["REQ-9.AC1"]["status"], "delivered")
            self.assertEqual(len(rows["REQ-9.AC1"]["evidence"]), 1)
            self.assertEqual(rows["REQ-9.AC1"]["evidence"][0]["path"], "target/agent-evidence/req-9-evidence.json")

    def test_no_spec_reports_single_unmatched_gap(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp)
            write_job(
                repo,
                "unrelated-spec-work",
                kickoff="Mentions specs but should not match the synthetic no-SPEC row.",
                status="done",
                pr="https://github.com/acme/widgets/pull/3",
            )
            write_gate(repo, "unrelated-spec-work", "tests", "pass")

            report = traceability.build_report(repo, [], repo / "target" / "agent-evidence", [])

            self.assertEqual(report["summary"]["requirements"], 1)
            self.assertEqual(report["summary"]["pending_gap_count"], 1)
            self.assertEqual(report["requirements"][0]["id"], "SPEC")
            self.assertEqual(report["requirements"][0]["jobs"], [])
            self.assertEqual(report["requirements"][0]["status"], "gap")


def write_spec(repo: Path, body: str) -> None:
    (repo / "SPEC.md").write_text("\n".join(line.strip() for line in body.strip().splitlines()) + "\n", encoding="utf-8")


def write_backlog(repo: Path, body: dict) -> None:
    (repo / "backlog.json").write_text(json.dumps(body), encoding="utf-8")


def write_job(
    repo: Path,
    job_id: str,
    *,
    kickoff: str,
    status: str,
    pr: str = "",
    branch: str = "",
) -> None:
    jobs = repo / ".agent_team" / "jobs"
    jobs.mkdir(parents=True, exist_ok=True)
    (jobs / f"{job_id}.toml").write_text(
        f"""
id = "{job_id}"
status = "{status}"
ticket = "{job_id}"
branch = "{branch}"
pr = "{pr}"
kickoff = "{kickoff}"
""".lstrip(),
        encoding="utf-8",
    )


def write_gate(repo: Path, job_id: str, name: str, status: str) -> None:
    jobs = repo / ".agent_team" / "jobs"
    (jobs / f"{job_id}.gates.jsonl").write_text(
        json.dumps({"job_id": job_id, "name": name, "status": status, "ts": "2026-07-08T00:00:00Z"}) + "\n",
        encoding="utf-8",
    )


if __name__ == "__main__":
    unittest.main()
