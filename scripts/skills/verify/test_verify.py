#!/usr/bin/env python3
from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

REPO_ROOT = Path(__file__).resolve().parents[3]
sys.dont_write_bytecode = True
sys.path.insert(0, str(REPO_ROOT / "template" / "skills" / "verify" / "scripts"))
import verify


class VerifyBaseComparisonTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.repo = self.root / "repo"
        self.repo.mkdir()
        self.git("init", "-b", "main")
        self.git("config", "user.name", "Verifier Test")
        self.git("config", "user.email", "verify@example.invalid")

    def tearDown(self) -> None:
        self.temp.cleanup()

    def git(self, *args: str, cwd: Path | None = None) -> str:
        return subprocess.check_output(
            ["git", *args],
            cwd=cwd or self.repo,
            text=True,
            stderr=subprocess.STDOUT,
        ).strip()

    def commit_gate(self, exit_code: int, message: str) -> str:
        gate = self.repo / "gate.sh"
        gate.write_text(f"#!/bin/sh\necho gate-{exit_code}\nexit {exit_code}\n", encoding="utf-8")
        gate.chmod(0o755)
        self.git("add", "gate.sh")
        self.git("commit", "-m", message)
        return self.git("rev-parse", "HEAD")

    def configure_origin_head(self) -> None:
        origin = self.root / "origin.git"
        subprocess.check_call(["git", "init", "--bare", str(origin)], stdout=subprocess.DEVNULL)
        self.git("remote", "add", "origin", str(origin))
        self.git("push", "origin", "main")
        subprocess.check_call(
            ["git", "symbolic-ref", "HEAD", "refs/heads/main"],
            cwd=origin,
            stdout=subprocess.DEVNULL,
        )
        self.git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

    def run_verify(self, commit: str) -> dict[str, object]:
        gates = self.root / "gates.txt"
        gates.write_text("tests :: ./gate.sh\n", encoding="utf-8")
        evidence_dir = self.root / "evidence"
        clean_env = {key: value for key, value in os.environ.items() if not key.startswith("AGENT_TEAM_")}
        with mock.patch.dict(os.environ, clean_env, clear=True):
            code = verify.main(
                [
                    "--repo",
                    str(self.repo),
                    "--commit",
                    commit,
                    "--gates-file",
                    str(gates),
                    "--evidence-dir",
                    str(evidence_dir),
                    "--no-record-gates",
                ]
            )
        self.assertEqual(code, 1)
        evidence_path = evidence_dir / f"{commit[:12]}.json"
        return json.loads(evidence_path.read_text(encoding="utf-8"))

    def test_failure_reproduced_at_merge_base_is_infra(self) -> None:
        self.commit_gate(1, "base broken")
        self.configure_origin_head()
        (self.repo / "note.txt").write_text("feature\n", encoding="utf-8")
        self.git("add", "note.txt")
        self.git("commit", "-m", "feature")

        evidence = self.run_verify(self.git("rev-parse", "HEAD"))

        gate = evidence["gates"][0]
        self.assertEqual(gate["class"], "infra")
        self.assertEqual(gate["signature"], "base-broken")
        self.assertTrue(gate["base_comparison"]["reproduced"])
        self.assertEqual(gate["base_comparison"]["default_branch"], "origin/main")

    def test_failure_clean_at_merge_base_preserves_signature(self) -> None:
        self.commit_gate(0, "base clean")
        self.configure_origin_head()
        commit = self.commit_gate(1, "feature broken")

        evidence = self.run_verify(commit)

        gate = evidence["gates"][0]
        self.assertNotIn("class", gate)
        self.assertNotEqual(gate["signature"], "base-broken")
        self.assertFalse(gate["base_comparison"]["reproduced"])

    def test_missing_default_branch_preserves_signature_with_warning(self) -> None:
        commit = self.commit_gate(1, "broken without origin")

        evidence = self.run_verify(commit)

        gate = evidence["gates"][0]
        self.assertNotIn("class", gate)
        self.assertNotEqual(gate["signature"], "base-broken")
        self.assertEqual(gate["base_comparison"]["status"], "unavailable")
        self.assertTrue(any("base comparison unavailable" in warning for warning in evidence["warnings"]))

    def test_go_test_comparison_is_narrowed_to_failed_scope(self) -> None:
        log = self.root / "go-test.log"
        log.write_text(
            "--- FAIL: TestBeta (0.01s)\n"
            "--- FAIL: TestAlpha/subcase (0.01s)\n"
            "FAIL\texample.com/project/internal/daemon\t0.123s\n",
            encoding="utf-8",
        )

        command = verify.base_comparison_command("go test ./...", log)

        self.assertEqual(
            command,
            "go test -run '^(?:TestAlpha|TestBeta)$' example.com/project/internal/daemon",
        )


if __name__ == "__main__":
    unittest.main()
