#!/usr/bin/env python3
"""Tests for scripts/experiments/ab_harness.py."""

from __future__ import annotations

import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("ab_harness.py")
SPEC = importlib.util.spec_from_file_location("ab_harness", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
ab_harness = importlib.util.module_from_spec(SPEC)
sys.modules["ab_harness"] = ab_harness
SPEC.loader.exec_module(ab_harness)


class ABHarnessTests(unittest.TestCase):
    def test_dry_run_report_validates_same_work_with_different_slicing(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            backlog = root / "backlog.json"
            baseline_topology = root / "baseline.instances.toml"
            candidate_topology = root / "candidate.instances.toml"
            report = root / "report.md"
            summary = root / "summary.json"
            write_backlog(backlog)
            write_baseline_topology(baseline_topology)
            write_candidate_topology(candidate_topology)

            rc = ab_harness.main(
                [
                    "--backlog",
                    str(backlog),
                    "--baseline-topology",
                    str(baseline_topology),
                    "--candidate-topology",
                    str(candidate_topology),
                    "--output",
                    str(report),
                    "--summary-json",
                    str(summary),
                ]
            )

            self.assertEqual(rc, 0)
            body = report.read_text()
            self.assertIn("Same canonical work: yes", body)
            self.assertIn("`monster-core`", body)
            self.assertIn("Verifier before review", body)
            parsed = json.loads(summary.read_text())
            self.assertEqual(parsed["backlog"]["items"], 4)
            self.assertEqual(parsed["arms"]["baseline"]["planned_slices"], 1)
            self.assertEqual(parsed["arms"]["slim-verifier-first"]["planned_slices"], 4)

    def test_result_comparison_normalizes_metrics_and_quality(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            backlog = root / "backlog.json"
            baseline_topology = root / "baseline.instances.toml"
            candidate_topology = root / "candidate.instances.toml"
            baseline_results = root / "baseline-results.json"
            candidate_results = root / "candidate-results.json"
            report = root / "report.md"
            write_backlog(backlog)
            write_baseline_topology(baseline_topology)
            write_candidate_topology(candidate_topology)
            baseline_results.write_text(
                json.dumps(
                    [
                        {
                            "id": "monster-core",
                            "status": "done",
                            "created_at": "2026-07-07T10:00:00Z",
                            "finalized_at": "2026-07-07T12:00:00Z",
                            "tokens_consumed": 1200,
                            "bounce_count": 2,
                            "human_interventions": 3,
                            "quality": {"status": "pass", "required_gates_passed": True},
                            "work_units": [
                                {
                                    "started_at": "2026-07-07T10:00:00Z",
                                    "finished_at": "2026-07-07T12:00:00Z",
                                }
                            ],
                        }
                    ]
                )
            )
            candidate_results.write_text(
                json.dumps(
                    [
                        result_record("core", "2026-07-07T10:00:00Z", "2026-07-07T10:40:00Z", 220),
                        result_record("fen", "2026-07-07T10:40:00Z", "2026-07-07T11:00:00Z", 180),
                        result_record("search", "2026-07-07T10:40:00Z", "2026-07-07T11:10:00Z", 260),
                        result_record("uci", "2026-07-07T10:40:00Z", "2026-07-07T11:05:00Z", 210),
                    ]
                )
            )

            rc = ab_harness.main(
                [
                    "--backlog",
                    str(backlog),
                    "--baseline-topology",
                    str(baseline_topology),
                    "--candidate-topology",
                    str(candidate_topology),
                    "--baseline-results",
                    str(baseline_results),
                    "--candidate-results",
                    str(candidate_results),
                    "--output",
                    str(report),
                ]
            )

            self.assertEqual(rc, 0)
            body = report.read_text()
            self.assertIn("Tokens / merged slice", body)
            self.assertIn("Quality floor", body)
            self.assertIn("Compare normalized speed and cost", body)

    def test_completed_records_must_match_planned_slice_ids(self) -> None:
        exact = metrics_from_completed_ids(["a", "b"])
        self.assertTrue(exact.quality_pass)

        duplicated = metrics_from_completed_ids(["a", "a"])
        self.assertFalse(duplicated.quality_pass)
        self.assertIn("missing completed slices: b", duplicated.quality_notes)
        self.assertIn("duplicate completed slices: a", duplicated.quality_notes)

        unknown = metrics_from_completed_ids(["a", "b", "c"])
        self.assertFalse(unknown.quality_pass)
        self.assertIn("unknown completed slices: c", unknown.quality_notes)

    def test_report_fails_quality_for_duplicate_completed_records(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            backlog = root / "backlog.json"
            baseline_topology = root / "baseline.instances.toml"
            candidate_topology = root / "candidate.instances.toml"
            baseline_results = root / "baseline-results.json"
            candidate_results = root / "candidate-results.json"
            report = root / "report.md"
            write_two_slice_backlog(backlog)
            write_baseline_topology(baseline_topology)
            write_candidate_topology(candidate_topology)
            baseline_results.write_text(
                json.dumps([minimal_result_record("a"), minimal_result_record("a")])
            )
            candidate_results.write_text(
                json.dumps([minimal_result_record("a"), minimal_result_record("b")])
            )

            rc = ab_harness.main(
                [
                    "--backlog",
                    str(backlog),
                    "--baseline-topology",
                    str(baseline_topology),
                    "--candidate-topology",
                    str(candidate_topology),
                    "--baseline-results",
                    str(baseline_results),
                    "--candidate-results",
                    str(candidate_results),
                    "--output",
                    str(report),
                ]
            )

            self.assertEqual(rc, 0)
            body = report.read_text()
            self.assertIn("| Quality floor | FAIL | PASS |", body)
            self.assertIn("missing completed slices: b", body)
            self.assertIn("duplicate completed slices: a", body)

    def test_summary_quality_pass_requires_completed_slice_evidence(self) -> None:
        without_ids = ab_harness.metrics_from_summary(
            {"quality": {"status": "pass", "required_gates_passed": True}},
            "arm",
            Path("results.json"),
            ("a", "b"),
        )
        self.assertFalse(without_ids.quality_pass)
        self.assertIn(
            "completed slice ids missing; cannot prove planned slice coverage",
            without_ids.quality_notes,
        )

        with_ids = ab_harness.metrics_from_summary(
            {
                "completed_slice_ids": ["a", "b"],
                "quality": {"status": "pass", "required_gates_passed": True},
            },
            "arm",
            Path("results.json"),
            ("a", "b"),
        )
        self.assertTrue(with_ids.quality_pass)

    def test_missing_arm_coverage_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            backlog = root / "backlog.json"
            backlog.write_text(
                json.dumps(
                    {
                        "items": [
                            {"id": "a", "difficulty": 1},
                            {"id": "b", "difficulty": 1},
                        ],
                        "arms": {
                            "baseline": {"slices": [{"id": "only-a", "items": ["a"]}]},
                            "slim-verifier-first": {"slices": [{"id": "both", "items": ["a", "b"]}]},
                        },
                    }
                )
            )
            with self.assertRaises(ab_harness.HarnessError):
                loaded = ab_harness.load_backlog(backlog)
                ab_harness.slices_for_arm(loaded, "baseline")


def write_backlog(path: Path) -> None:
    path.write_text(
        json.dumps(
            {
                "name": "chess-bootstrap-retro-sample",
                "hypothesis": "Fine-grained verifier-first delivery should beat monster serial slices without lowering quality.",
                "items": [
                    {"id": "core", "title": "Board primitives", "difficulty": 5},
                    {"id": "fen", "title": "FEN parser", "difficulty": 3, "depends_on": ["core"]},
                    {"id": "search", "title": "Search", "difficulty": 5, "depends_on": ["core"]},
                    {"id": "uci", "title": "UCI loop", "difficulty": 3, "depends_on": ["core"]},
                ],
                "arms": {
                    "baseline": {
                        "slices": [
                            {
                                "id": "monster-core",
                                "title": "Bundled core/search/UCI delivery",
                                "items": ["core", "fen", "search", "uci"],
                            }
                        ]
                    },
                    "slim-verifier-first": {
                        "slices": [
                            {"id": "core", "items": ["core"]},
                            {"id": "fen", "items": ["fen"]},
                            {"id": "search", "items": ["search"]},
                            {"id": "uci", "items": ["uci"]},
                        ]
                    },
                },
            }
        )
    )


def write_baseline_topology(path: Path) -> None:
    path.write_text(
        """
[instances.worker]
agent = "worker"
ephemeral = true
replicas = 6

[instances.reviewer]
agent = "reviewer"
ephemeral = true
replicas = 3

[pipelines.ticket_to_pr]
trigger.event = "agent.dispatch"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "reviewer"
""".lstrip()
    )


def write_candidate_topology(path: Path) -> None:
    path.write_text(
        """
[instances.worker]
agent = "worker"
ephemeral = true
replicas = 3

[instances.verifier]
agent = "verifier"
ephemeral = true
replicas = 1

[instances.reviewer]
agent = "reviewer"
ephemeral = true
replicas = 2

[pipelines.ticket_to_pr]
trigger.event = "agent.dispatch"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "verify"
target = "verifier"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "reviewer"
""".lstrip()
    )


def write_two_slice_backlog(path: Path) -> None:
    path.write_text(
        json.dumps(
            {
                "items": [
                    {"id": "a", "difficulty": 1},
                    {"id": "b", "difficulty": 1},
                ],
                "arms": {
                    "baseline": {
                        "slices": [{"id": "a", "items": ["a"]}, {"id": "b", "items": ["b"]}]
                    },
                    "slim-verifier-first": {
                        "slices": [{"id": "a", "items": ["a"]}, {"id": "b", "items": ["b"]}]
                    },
                },
            }
        )
    )


def result_record(slice_id: str, start: str, end: str, tokens: int) -> dict[str, object]:
    return {
        "id": slice_id,
        "status": "done",
        "created_at": start,
        "finalized_at": end,
        "tokens_consumed": tokens,
        "bounce_count": 0,
        "human_interventions": 1,
        "quality": {"status": "pass", "required_gates_passed": True},
        "work_units": [{"started_at": start, "finished_at": end}],
    }


def minimal_result_record(slice_id: str) -> dict[str, object]:
    return {
        "id": slice_id,
        "status": "done",
        "quality": {"status": "pass", "required_gates_passed": True},
    }


def metrics_from_completed_ids(slice_ids: list[str]) -> ab_harness.ArmMetrics:
    return ab_harness.metrics_from_records(
        [
            result_record(slice_id, "2026-07-07T10:00:00Z", "2026-07-07T10:10:00Z", 100)
            for slice_id in slice_ids
        ],
        "arm",
        Path("results.json"),
        ("a", "b"),
    )


if __name__ == "__main__":
    unittest.main()
