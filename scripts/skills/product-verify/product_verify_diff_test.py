#!/usr/bin/env python3
from __future__ import annotations

import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

REPO_ROOT = Path(__file__).resolve().parents[3]
# These tests import the shipped template script directly; do not write bytecode
# into the embedded template tree.
sys.dont_write_bytecode = True
sys.path.insert(0, str(REPO_ROOT / "template" / "skills" / "product-verify" / "scripts"))
import product_verify_diff as verifier


class ProductVerifyDiffTest(unittest.TestCase):
    def test_cli_reads_use_global_repo_selector_for_every_command(self) -> None:
        repo = Path("/tmp/product-verify-repo")
        with mock.patch.object(verifier, "run_json_command", return_value=[]) as run_json:
            verifier.fetch_cli_data("agent-team-test", repo)

        self.assertEqual(
            run_json.call_args_list,
            [
                mock.call(["agent-team-test", "--repo", str(repo), "ps", "--json"]),
                mock.call(["agent-team-test", "--repo", str(repo), "job", "ls", "--json"]),
                mock.call(["agent-team-test", "--repo", str(repo), "topology", "show", "--json"]),
            ],
        )

    def test_instance_projection_ignores_cli_only_enrichment(self) -> None:
        ui_instances = [
            {
                "instance": "docs-writer-squ-109",
                "agent": "worker",
                "status": "done",
                "job": "squ-109",
                "runtime": "codex",
            }
        ]
        cli_instances = [
            {
                "instance": "docs-writer-squ-109",
                "agent": "worker",
                "status": "done",
                "job": "squ-109",
                "runtime": "codex",
                "pr": "https://github.com/agent-team-project/kensho/pull/107",
                "pid": 12345,
                "runtime_binary": "codex",
                "resume_count": 2,
            }
        ]

        result = verifier.compare_instances(ui_instances, cli_instances)

        self.assertTrue(result["ok"])
        self.assertEqual(result["diffs"], [])

    def test_instance_projection_ignores_ps_only_declared_row(self) -> None:
        ui_instances = [
            {
                "instance": "platform-worker-squ-187",
                "agent": "worker",
                "status": "running",
                "job": "squ-187",
                "runtime": "codex",
            }
        ]
        cli_instances = [
            {
                "instance": "platform-worker-squ-187",
                "agent": "worker",
                "status": "running",
                "job": "squ-187",
                "runtime": "codex",
            },
            {
                "instance": "harness-reviewer",
                "agent": "reviewer",
                "status": "declared",
                "job": "",
                "runtime": "",
                "branch": "status-file-only",
            },
        ]

        result = verifier.compare_instances(ui_instances, cli_instances)

        self.assertTrue(result["ok"])
        self.assertEqual(result["diffs"], [])

    def test_instance_projection_ignores_stale_status_branch(self) -> None:
        ui_instances = [
            {
                "instance": "platform-reviewer-squ-101-review",
                "agent": "reviewer",
                "status": "done",
                "branch": "",
                "job": "squ-101",
                "runtime": "codex",
            }
        ]
        cli_instances = [
            {
                "instance": "platform-reviewer-squ-101-review",
                "agent": "reviewer",
                "status": "done",
                "branch": "squ-101-ef91baa8",
                "job": "squ-101",
                "runtime": "codex",
            }
        ]

        result = verifier.compare_instances(ui_instances, cli_instances)

        self.assertTrue(result["ok"])
        self.assertEqual(result["diffs"], [])

    def test_instance_projection_reports_projected_field_mismatch(self) -> None:
        ui_instances = [
            {
                "instance": "platform-worker-squ-187",
                "agent": "worker",
                "status": "running",
                "job": "squ-187",
                "runtime": "codex",
            }
        ]
        cli_instances = [
            {
                "instance": "platform-worker-squ-187",
                "agent": "worker",
                "status": "done",
                "job": "squ-187",
                "runtime": "codex",
                "pr": "https://github.com/agent-team-project/kensho/pull/190",
            }
        ]

        result = verifier.compare_instances(ui_instances, cli_instances)

        self.assertFalse(result["ok"])
        self.assertEqual(
            result["diffs"],
            [
                {
                    "type": "field_mismatch",
                    "comparison": "instances",
                    "key": "platform-worker-squ-187",
                    "field": "status",
                    "ui": "running",
                    "cli": "done",
                }
            ],
        )

    def test_job_alias_normalization_matches_daemon_list_shape(self) -> None:
        ui_jobs = [
            {
                "id": "squ-1",
                "ticket": "SQU-1",
                "ticket_url": "https://example.test/SQU-1",
                "target": "worker",
                "implementation_agent": "worker",
                "instance": "worker-squ-1",
                "pipeline": "ticket_to_pr",
                "status": "running",
                "held": False,
                "branch": "squ-1",
                "pr": "",
                "last_event": "dispatched",
                "last_status": "running",
                "created_at": "2026-07-07T10:00:00Z",
                "updated_at": "2026-07-07T10:01:00Z",
            }
        ]
        cli_jobs = [
            {
                "ID": "squ-1",
                "Ticket": "SQU-1",
                "TicketURL": "https://example.test/SQU-1",
                "Target": "worker",
                "ImplementationAgent": "worker",
                "Instance": "worker-squ-1",
                "Pipeline": "ticket_to_pr",
                "Status": "running",
                "Held": False,
                "Branch": "squ-1",
                "LastEvent": "dispatched",
                "LastStatus": "running",
                "CreatedAt": "2026-07-07T10:00:00Z",
                "UpdatedAt": "2026-07-07T10:01:00Z",
                "Steps": [{"ID": "implement", "Status": "running"}],
            }
        ]

        result = verifier.compare_records("jobs", ui_jobs, cli_jobs, verifier.normalize_jobs)

        self.assertTrue(result["ok"])
        self.assertEqual(result["diffs"], [])

    def test_report_caps_bug_findings(self) -> None:
        ui_data = {
            "instances": [
                {"instance": "manager", "agent": "manager", "status": "running"},
                {"instance": "worker", "agent": "worker", "status": "running"},
            ],
            "jobs": [],
            "topology": {"instances": [], "pipelines": [], "schedules": []},
        }
        cli_data = {
            "instances": [
                {"instance": "manager", "agent": "manager", "status": "exited"},
                {"instance": "worker", "agent": "worker", "status": "exited"},
            ],
            "jobs": [],
            "topology": {"instances": [], "pipelines": [], "schedules": []},
        }

        report = verifier.build_report(ui_data, cli_data, max_findings=1)

        self.assertEqual(report["status"], "mismatch")
        self.assertEqual(report["summary"]["mismatches"], 2)
        self.assertEqual(report["summary"]["findings"], 1)
        self.assertTrue(report["summary"]["capped"])
        self.assertEqual(report["findings"][0]["category"], "bug")

    def test_topology_list_order_is_canonicalized(self) -> None:
        ui_topology = {
            "instances": [],
            "pipelines": [],
            "schedules": [],
            "teams": [
                {"name": "quality", "instances": ["sentinel", "debt-auditor"], "schedules": ["sentinel", "debt-sweep"]}
            ],
        }
        cli_topology = {
            "instances": [],
            "pipelines": [],
            "schedules": [],
            "teams": [
                {"name": "quality", "instances": ["debt-auditor", "sentinel"], "schedules": ["debt-sweep", "sentinel"]}
            ],
        }

        result = verifier.compare_topology(ui_topology, cli_topology)

        self.assertTrue(result["ok"])

    def test_missing_http_addr_skips_cleanly(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            with mock.patch.dict(os.environ, {"AGENT_TEAM_DAEMON_URL": ""}):
                self.assertIsNone(verifier.resolve_daemon_url(Path(tmp)))


if __name__ == "__main__":
    unittest.main()
