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

    def run_verify(self, commit: str, gate_command: str = "./gate.sh") -> dict[str, object]:
        gates = self.root / "gates.txt"
        gates.write_text(f"tests :: {gate_command}\n", encoding="utf-8")
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

    def test_distinct_base_failure_preserves_head_signature(self) -> None:
        self.commit_gate(0, "base clean")
        self.configure_origin_head()
        feature_gate = self.repo / "feature_gate.py"
        feature_gate.write_text(
            "import sys\nprint('OK', file=sys.stderr)\n",
            encoding="utf-8",
        )
        self.git("add", "feature_gate.py")
        self.git("commit", "-m", "add feature gate")

        evidence = self.run_verify(
            self.git("rev-parse", "HEAD"),
            "python3 feature_gate.py >/dev/null && false",
        )

        gate = evidence["gates"][0]
        comparison = gate["base_comparison"]
        self.assertNotIn("class", gate)
        self.assertEqual(gate["signature"], "OK")
        self.assertFalse(comparison["reproduced"])
        self.assertEqual(comparison["reproduction_basis"], "exit-code-and-full-output-fingerprint")
        self.assertEqual(comparison["head_exit_code"], 1)
        self.assertEqual(comparison["exit_code"], 2)
        self.assertNotEqual(comparison["signature"], gate["signature"])
        self.assertIn("fingerprint differs", comparison["reason"])

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

    def test_go_test_requires_every_head_failure_at_merge_base(self) -> None:
        (self.repo / "go.mod").write_text("module example.com/basebroken\n\ngo 1.22\n", encoding="utf-8")
        test_file = self.repo / "base_broken_test.go"
        test_file.write_text(
            "package basebroken\n\n"
            'import "testing"\n\n'
            "func TestBaseBroken(t *testing.T) { t.Fatal(\"base-bug\") }\n",
            encoding="utf-8",
        )
        self.git("add", "go.mod", "base_broken_test.go")
        self.git("commit", "-m", "base has one failing test")
        self.configure_origin_head()
        test_file.write_text(
            "package basebroken\n\n"
            'import "testing"\n\n'
            "func TestBaseBroken(t *testing.T) { t.Fatal(\"base-bug\") }\n"
            "func TestHeadOnlyRegression(t *testing.T) { t.Fatal(\"head-regression\") }\n",
            encoding="utf-8",
        )
        self.git("add", "base_broken_test.go")
        self.git("commit", "-m", "add head-only regression")

        evidence = self.run_verify(self.git("rev-parse", "HEAD"), "go test ./...")

        gate = evidence["gates"][0]
        comparison = gate["base_comparison"]
        self.assertNotIn("class", gate)
        self.assertNotEqual(gate["signature"], "base-broken")
        self.assertFalse(comparison["reproduced"])
        self.assertEqual(comparison["reproduction_basis"], "go-test-identity-subset")
        self.assertEqual(
            comparison["head_failure_identities"],
            [
                "go-test:example.com/basebroken:TestBaseBroken",
                "go-test:example.com/basebroken:TestHeadOnlyRegression",
            ],
        )
        self.assertEqual(
            comparison["base_failure_identities"],
            ["go-test:example.com/basebroken:TestBaseBroken"],
        )

    def test_go_test_comparison_preserves_sibling_subtest_identities(self) -> None:
        (self.repo / "go.mod").write_text("module example.com/subtests\n\ngo 1.22\n", encoding="utf-8")
        test_file = self.repo / "suite_test.go"
        test_file.write_text(
            "package subtests\n\n"
            'import "testing"\n\n'
            "func TestSuite(t *testing.T) {\n"
            '    t.Run("base", func(t *testing.T) { t.Fatal("base-bug") })\n'
            "}\n",
            encoding="utf-8",
        )
        self.git("add", "go.mod", "suite_test.go")
        self.git("commit", "-m", "base has one failing subtest")
        self.configure_origin_head()
        test_file.write_text(
            "package subtests\n\n"
            'import "testing"\n\n'
            "func TestSuite(t *testing.T) {\n"
            '    t.Run("base", func(t *testing.T) { t.Fatal("base-bug") })\n'
            '    t.Run("head-only", func(t *testing.T) { t.Fatal("head-regression") })\n'
            "}\n",
            encoding="utf-8",
        )
        self.git("add", "suite_test.go")
        self.git("commit", "-m", "add head-only sibling subtest")

        evidence = self.run_verify(self.git("rev-parse", "HEAD"), "go test ./...")

        gate = evidence["gates"][0]
        comparison = gate["base_comparison"]
        self.assertNotIn("class", gate)
        self.assertNotEqual(gate["signature"], "base-broken")
        self.assertFalse(comparison["reproduced"])
        self.assertEqual(
            comparison["head_failure_identities"],
            [
                "go-test:example.com/subtests:TestSuite",
                "go-test:example.com/subtests:TestSuite/base",
                "go-test:example.com/subtests:TestSuite/head-only",
            ],
        )
        self.assertEqual(
            comparison["base_failure_identities"],
            [
                "go-test:example.com/subtests:TestSuite",
                "go-test:example.com/subtests:TestSuite/base",
            ],
        )

    def test_go_compile_failure_reproduced_at_merge_base_is_infra(self) -> None:
        (self.repo / "go.mod").write_text("module example.com/compilefail\n\ngo 1.22\n", encoding="utf-8")
        (self.repo / "broken.go").write_text(
            "package compilefail\n\nfunc broken( {\n",
            encoding="utf-8",
        )
        self.git("add", "go.mod", "broken.go")
        self.git("commit", "-m", "base has compile failure")
        self.configure_origin_head()
        (self.repo / "note.txt").write_text("unrelated head change\n", encoding="utf-8")
        self.git("add", "note.txt")
        self.git("commit", "-m", "add unrelated head change")

        evidence = self.run_verify(self.git("rev-parse", "HEAD"), "go test ./...")

        gate = evidence["gates"][0]
        comparison = gate["base_comparison"]
        self.assertEqual(gate["class"], "infra")
        self.assertEqual(gate["signature"], "base-broken")
        self.assertTrue(comparison["reproduced"])
        self.assertEqual(comparison["reproduction_basis"], "exit-code-and-full-output-fingerprint")
        self.assertEqual(comparison["head_failure_identities"], [])
        self.assertEqual(comparison["base_failure_identities"], [])
        self.assertEqual(comparison["head_output_fingerprint"], comparison["base_output_fingerprint"])

    def test_distinct_go_compile_failure_preserves_head_signature(self) -> None:
        (self.repo / "go.mod").write_text("module example.com/compilefail\n\ngo 1.22\n", encoding="utf-8")
        broken = self.repo / "broken.go"
        broken.write_text("package compilefail\n\nfunc broken( {\n", encoding="utf-8")
        self.git("add", "go.mod", "broken.go")
        self.git("commit", "-m", "base has compile failure")
        self.configure_origin_head()
        broken.write_text("package compilefail\n\nfunc broken() { missing }\n", encoding="utf-8")
        self.git("add", "broken.go")
        self.git("commit", "-m", "replace with head compile failure")

        evidence = self.run_verify(self.git("rev-parse", "HEAD"), "go test ./...")

        gate = evidence["gates"][0]
        comparison = gate["base_comparison"]
        self.assertNotIn("class", gate)
        self.assertNotEqual(gate["signature"], "base-broken")
        self.assertFalse(comparison["reproduced"])
        self.assertEqual(comparison["reproduction_basis"], "exit-code-and-full-output-fingerprint")
        self.assertNotEqual(comparison["head_output_fingerprint"], comparison["base_output_fingerprint"])
        self.assertIn("fingerprint differs", comparison["reason"])

    def test_fresh_remote_default_ignores_stale_local_tracking_ref(self) -> None:
        broken_base = self.commit_gate(1, "base broken")
        self.configure_origin_head()
        clean_base = self.commit_gate(0, "base fixed remotely")
        self.git("push", "origin", "main")
        self.git("update-ref", "refs/remotes/origin/main", broken_base)
        self.assertEqual(self.git("rev-parse", "origin/main"), broken_base)
        feature = self.commit_gate(1, "feature regression")

        evidence = self.run_verify(feature)

        gate = evidence["gates"][0]
        comparison = gate["base_comparison"]
        self.assertNotIn("class", gate)
        self.assertNotEqual(gate["signature"], "base-broken")
        self.assertFalse(comparison["reproduced"])
        self.assertEqual(comparison["default_branch"], "origin/main")
        self.assertEqual(comparison["default_branch_sha"], clean_base)
        self.assertEqual(comparison["merge_base"], clean_base)

    def test_generic_same_footer_with_distinct_failures_is_not_reproduced(self) -> None:
        test_file = self.repo / "test_failure.py"
        test_file.write_text(
            "import unittest\n\n"
            "class BaseFailure(unittest.TestCase):\n"
            "    def test_base_behavior(self):\n"
            "        self.fail(\"base-bug\")\n",
            encoding="utf-8",
        )
        self.git("add", "test_failure.py")
        self.git("commit", "-m", "base unittest failure")
        self.configure_origin_head()
        test_file.write_text(
            "import unittest\n\n"
            "class HeadFailure(unittest.TestCase):\n"
            "    def test_head_only_behavior(self):\n"
            "        self.fail(\"head-regression\")\n",
            encoding="utf-8",
        )
        self.git("add", "test_failure.py")
        self.git("commit", "-m", "replace with head-only unittest failure")

        evidence = self.run_verify(self.git("rev-parse", "HEAD"), "python3 -m unittest -v")

        gate = evidence["gates"][0]
        comparison = gate["base_comparison"]
        self.assertEqual(gate["signature"], "FAILED (failures=1)")
        self.assertNotIn("class", gate)
        self.assertFalse(comparison["reproduced"])
        self.assertEqual(comparison["reproduction_basis"], "failure-identity-subset")
        self.assertEqual(
            comparison["head_failure_identities"],
            ["unittest:test_failure.HeadFailure.test_head_only_behavior"],
        )
        self.assertEqual(
            comparison["base_failure_identities"],
            ["unittest:test_failure.BaseFailure.test_base_behavior"],
        )


if __name__ == "__main__":
    unittest.main()
