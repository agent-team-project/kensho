#!/usr/bin/env python3
from __future__ import annotations

import json
import hashlib
import os
import shlex
import subprocess
import sys
import tempfile
import threading
import time
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

    def test_compound_go_gate_does_not_hide_head_only_failure(self) -> None:
        (self.repo / "go.mod").write_text("module example.com/compound\n\ngo 1.22\n", encoding="utf-8")
        (self.repo / "base_broken_test.go").write_text(
            "package compound\n\n"
            'import "testing"\n\n'
            'func TestBaseBroken(t *testing.T) { t.Fatal("base-bug") }\n',
            encoding="utf-8",
        )
        self.git("add", "go.mod", "base_broken_test.go")
        self.git("commit", "-m", "base has failing Go test")
        self.configure_origin_head()
        (self.repo / "feature_gate.py").write_text(
            "import sys\nprint('head-regression', file=sys.stderr)\nraise SystemExit(2)\n",
            encoding="utf-8",
        )
        self.git("add", "feature_gate.py")
        self.git("commit", "-m", "add head-only gate failure")
        command = "go test ./...; python3 feature_gate.py"

        evidence = self.run_verify(self.git("rev-parse", "HEAD"), command)

        gate = evidence["gates"][0]
        comparison = gate["base_comparison"]
        self.assertNotIn("class", gate)
        self.assertEqual(gate["signature"], "head-regression")
        self.assertFalse(comparison["reproduced"])
        self.assertEqual(comparison["command"], command)
        self.assertEqual(comparison["reproduction_basis"], "exit-code-and-full-output-fingerprint")
        self.assertEqual(comparison["head_exit_code"], 2)
        self.assertEqual(comparison["exit_code"], 2)
        self.assertNotEqual(comparison["head_output_fingerprint"], comparison["base_output_fingerprint"])

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

    def test_compound_unittest_gate_does_not_hide_head_only_failure(self) -> None:
        (self.repo / "test_failure.py").write_text(
            "import unittest\n\n"
            "class BaseFailure(unittest.TestCase):\n"
            "    def test_base_behavior(self):\n"
            "        self.fail('base-bug')\n",
            encoding="utf-8",
        )
        self.git("add", "test_failure.py")
        self.git("commit", "-m", "base has failing unittest")
        self.configure_origin_head()
        (self.repo / "feature_gate.py").write_text(
            "import sys\nprint('head-regression', file=sys.stderr)\nraise SystemExit(2)\n",
            encoding="utf-8",
        )
        self.git("add", "feature_gate.py")
        self.git("commit", "-m", "add head-only gate failure")
        command = "python3 -m unittest -v; python3 feature_gate.py"

        evidence = self.run_verify(self.git("rev-parse", "HEAD"), command)

        gate = evidence["gates"][0]
        comparison = gate["base_comparison"]
        self.assertNotIn("class", gate)
        self.assertEqual(gate["signature"], "head-regression")
        self.assertFalse(comparison["reproduced"])
        self.assertEqual(comparison["command"], command)
        self.assertEqual(comparison["reproduction_basis"], "exit-code-and-full-output-fingerprint")
        self.assertEqual(comparison["head_exit_code"], 2)
        self.assertEqual(comparison["exit_code"], 2)
        self.assertEqual(
            comparison["head_failure_identities"],
            ["unittest:test_failure.BaseFailure.test_base_behavior"],
        )
        self.assertEqual(
            comparison["base_failure_identities"],
            ["unittest:test_failure.BaseFailure.test_base_behavior"],
        )
        self.assertNotEqual(comparison["head_output_fingerprint"], comparison["base_output_fingerprint"])

    def test_pytest_error_is_not_hidden_by_reproduced_failure(self) -> None:
        runner = self.repo / "pytest"
        runner.write_text(
            "#!/bin/sh\n"
            "echo 'FAILED test_sample.py::test_base_broken - AssertionError'\n"
            "echo '=========================== 1 failed in 0.01s ==========================='\n"
            "exit 1\n",
            encoding="utf-8",
        )
        runner.chmod(0o755)
        self.git("add", "pytest")
        self.git("commit", "-m", "base has one pytest failure")
        self.configure_origin_head()
        runner.write_text(
            "#!/bin/sh\n"
            "echo 'FAILED test_sample.py::test_base_broken - AssertionError'\n"
            "echo 'ERROR test_sample.py::test_head_error - RuntimeError'\n"
            "echo '==================== 1 failed, 1 error in 0.01s ===================='\n"
            "exit 1\n",
            encoding="utf-8",
        )
        self.git("add", "pytest")
        self.git("commit", "-m", "add head-only pytest error")

        evidence = self.run_verify(self.git("rev-parse", "HEAD"), "./pytest")

        gate = evidence["gates"][0]
        comparison = gate["base_comparison"]
        self.assertNotIn("class", gate)
        self.assertNotEqual(gate["signature"], "base-broken")
        self.assertFalse(comparison["reproduced"])
        self.assertEqual(comparison["reproduction_basis"], "failure-identity-subset")
        self.assertTrue(comparison["head_failure_identities_complete"])
        self.assertTrue(comparison["base_failure_identities_complete"])
        self.assertEqual(
            comparison["head_failure_identities"],
            [
                "pytest-error:test_sample.py::test_head_error",
                "pytest:test_sample.py::test_base_broken",
            ],
        )
        self.assertEqual(
            comparison["base_failure_identities"],
            ["pytest:test_sample.py::test_base_broken"],
        )


class VerifyExactHeadTest(unittest.TestCase):
    PR_IDENTITY = {
        "repository": "acme/widgets",
        "owner": "acme",
        "repo": "widgets",
        "pr_number": 42,
        "pr_url": "https://github.com/acme/widgets/pull/42",
    }

    def git(self, *args: str, cwd: Path) -> str:
        return subprocess.check_output(
            ["git", *args],
            cwd=cwd,
            text=True,
            stderr=subprocess.STDOUT,
        ).strip()

    def create_fixture(self, root: Path, *, advance: bool = True) -> dict[str, object]:
        origin = root / "origin.git"
        publisher = root / "publisher"
        verifier_repo = root / "verifier"
        subprocess.check_call(["git", "init", "--bare", str(origin)], stdout=subprocess.DEVNULL)
        publisher.mkdir()
        self.git("init", "-b", "main", cwd=publisher)
        self.git("config", "user.name", "Publisher", cwd=publisher)
        self.git("config", "user.email", "publisher@example.invalid", cwd=publisher)
        gate = publisher / "gate.sh"
        gate.write_text("#!/bin/sh\necho exact-head-gate\nexit 0\n", encoding="utf-8")
        gate.chmod(0o755)
        self.git("add", "gate.sh", cwd=publisher)
        self.git("commit", "-m", "commit A", cwd=publisher)
        commit_a = self.git("rev-parse", "HEAD", cwd=publisher)
        self.git("remote", "add", "origin", str(origin), cwd=publisher)
        self.git("push", "origin", "main", cwd=publisher)
        self.git("push", "origin", "HEAD:refs/pull/42/head", cwd=publisher)
        self.git("symbolic-ref", "HEAD", "refs/heads/main", cwd=origin)
        subprocess.check_call(["git", "clone", str(origin), str(verifier_repo)], stdout=subprocess.DEVNULL)
        self.git("config", "user.name", "Verifier", cwd=verifier_repo)
        self.git("config", "user.email", "verifier@example.invalid", cwd=verifier_repo)
        self.git("checkout", "-b", "feature", commit_a, cwd=verifier_repo)
        commit_r = commit_a
        if advance:
            note = publisher / "remote.txt"
            note.write_text("remote R\n", encoding="utf-8")
            self.git("add", "remote.txt", cwd=publisher)
            self.git("commit", "-m", "commit R", cwd=publisher)
            commit_r = self.git("rev-parse", "HEAD", cwd=publisher)
            self.git("push", "origin", "HEAD:refs/pull/42/head", "--force", cwd=publisher)
        return {
            "origin": origin,
            "publisher": publisher,
            "repo": verifier_repo,
            "commit_a": commit_a,
            "commit_r": commit_r,
        }

    def query(
        self,
        head: str = "",
        *,
        status: str = "authenticated",
        queried_at: str = "2026-07-11T18:00:00Z",
    ) -> dict[str, object]:
        return {
            "repository": self.PR_IDENTITY["repository"],
            "pr_number": self.PR_IDENTITY["pr_number"],
            "pr_url": self.PR_IDENTITY["pr_url"],
            "query_status": status,
            "head_commit": head if status == "authenticated" else "",
            "queried_at": queried_at,
            "authenticated_actor": "fixture-actor" if status == "authenticated" else "",
            "query_transport": "fixture-authenticated-oracle",
            "query_error": "" if status == "authenticated" else "fixture oracle unavailable",
        }

    def raw_fetch(self, repo: Path, ref: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ["git", "-C", str(repo), "fetch", "--no-tags", "--depth=1", "origin", ref],
            text=True,
            capture_output=True,
            check=False,
        )

    def run_pr_verify(
        self,
        fixture: dict[str, object],
        queries: object,
        *,
        branch: str = "feature",
        explicit_commit: str = "",
        gate_command: str = "./gate.sh",
        complete_step: bool = False,
    ) -> tuple[int, dict[str, object], dict[str, object], mock.Mock]:
        root = Path(fixture["repo"]).parent
        gates = root / "gates.txt"
        gates.write_text(f"tests :: {gate_command}\n", encoding="utf-8")
        evidence_dir = root / "evidence"
        args = [
            "--repo",
            str(fixture["repo"]),
            "--job",
            "fixture-a",
            "--gates-file",
            str(gates),
            "--evidence-dir",
            str(evidence_dir),
            "--no-record-gates",
        ]
        if explicit_commit:
            args.extend(["--commit", explicit_commit])
        if complete_step:
            args.append("--complete-step")
        job = {
            "Branch": branch,
            "Worktree": "",
            "PR": self.PR_IDENTITY["pr_url"],
            "Pipeline": "research_slice",
        }
        clean_env = {key: value for key, value in os.environ.items() if not key.startswith("AGENT_TEAM_")}
        completion = mock.Mock()
        with (
            mock.patch.dict(os.environ, clean_env, clear=True),
            mock.patch.object(verify, "load_job", return_value=job),
            mock.patch.object(verify, "query_authenticated_pr_head", side_effect=queries),
            mock.patch.object(verify, "origin_repository", return_value=self.PR_IDENTITY["repository"]),
            mock.patch.object(verify, "run_authenticated_fetch", side_effect=self.raw_fetch),
            mock.patch.object(verify, "complete_step", completion),
        ):
            code = verify.main(args)
        evidence_path = evidence_dir / "fixture-a.json"
        attestation_path = evidence_dir / "fixture-a.exact-head.json"
        evidence = json.loads(evidence_path.read_text(encoding="utf-8"))
        attestation = json.loads(attestation_path.read_text(encoding="utf-8"))
        self.assertEqual(
            hashlib.sha256(evidence_path.read_bytes()).hexdigest(),
            attestation["evidence_sha256"],
        )
        return code, evidence, attestation, completion

    def test_fixture_a_authoritative_head_wins_all_local_ref_shapes(self) -> None:
        shapes = ("stale", "divergent", "local-ahead", "missing-local", "explicit-stale")
        for shape in shapes:
            with self.subTest(shape=shape), tempfile.TemporaryDirectory() as temp:
                fixture = self.create_fixture(Path(temp))
                repo = Path(fixture["repo"])
                commit_a = str(fixture["commit_a"])
                commit_r = str(fixture["commit_r"])
                branch = "feature"
                explicit = ""
                if shape == "divergent":
                    (repo / "local.txt").write_text("divergent local\n", encoding="utf-8")
                    self.git("add", "local.txt", cwd=repo)
                    self.git("commit", "-m", "divergent local commit", cwd=repo)
                elif shape == "local-ahead":
                    self.git("fetch", "origin", "refs/pull/42/head", cwd=repo)
                    self.git("reset", "--hard", "FETCH_HEAD", cwd=repo)
                    (repo / "local.txt").write_text("unpushed local\n", encoding="utf-8")
                    self.git("add", "local.txt", cwd=repo)
                    self.git("commit", "-m", "local ahead unpushed", cwd=repo)
                elif shape == "missing-local":
                    self.git("checkout", "main", cwd=repo)
                    self.git("branch", "-D", "feature", cwd=repo)
                elif shape == "explicit-stale":
                    explicit = commit_a

                code, evidence, attestation, completion = self.run_pr_verify(
                    fixture,
                    [self.query(commit_r), self.query(commit_r, queried_at="2026-07-11T18:00:01Z")],
                    branch=branch,
                    explicit_commit=explicit,
                    complete_step=True,
                )

                self.assertEqual(code, 0)
                self.assertEqual(evidence["status"], "pass")
                self.assertEqual(evidence["source"]["commit"], commit_r)
                self.assertEqual(attestation["evidence_commit"], commit_r)
                self.assertEqual(attestation["github_head_commit"], commit_r)
                self.assertEqual(attestation["query_status"], "authenticated")
                self.assertEqual(attestation["equality"], "equal")
                self.assertEqual(attestation["disposition"], "dispatch")
                self.assertEqual(attestation["reason"], "exact_head_equal")
                completion.assert_called_once()
                self.assertEqual(completion.call_args.args[3], commit_r)
                self.assertEqual(completion.call_args.args[4], "pass")

    def test_authenticated_graphql_oracle_records_closed_provenance(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            helper = Path(temp) / "github-auth.sh"
            helper.touch()
            head = "a" * 40
            payload = json.dumps(
                {
                    "data": {
                        "viewer": {"login": "fixture-actor"},
                        "repository": {
                            "pullRequest": {
                                "url": self.PR_IDENTITY["pr_url"],
                                "headRefOid": head,
                            }
                        },
                    }
                }
            )
            response = subprocess.CompletedProcess([], 0, payload, "")
            with (
                mock.patch.object(verify, "github_auth_helper", return_value=helper),
                mock.patch.object(verify.subprocess, "run", return_value=response) as run,
            ):
                query = verify.query_authenticated_pr_head(Path(temp), self.PR_IDENTITY)

            self.assertEqual(query["query_status"], "authenticated")
            self.assertEqual(query["head_commit"], head)
            self.assertEqual(query["authenticated_actor"], "fixture-actor")
            self.assertEqual(query["query_transport"], "github-auth.sh/gh-graphql")
            command = run.call_args.args[0]
            self.assertEqual(command[:4], [str(helper), "gh", "api", "graphql"])
            self.assertIn("query=", command[5])

            malformed = subprocess.CompletedProcess([], 0, '{"data": {}}', "")
            unavailable = subprocess.CompletedProcess([], 1, "", "authentication failed")
            for response, expected in ((malformed, "malformed"), (unavailable, "unavailable")):
                with self.subTest(expected=expected):
                    with (
                        mock.patch.object(verify, "github_auth_helper", return_value=helper),
                        mock.patch.object(verify.subprocess, "run", return_value=response),
                    ):
                        query = verify.query_authenticated_pr_head(Path(temp), self.PR_IDENTITY)
                        self.assertEqual(query["query_status"], expected)
                        self.assertEqual(query["head_commit"], "")

    def test_fixture_a_missing_origin_fails_closed_without_local_fallback(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            fixture = self.create_fixture(Path(temp))
            repo = Path(fixture["repo"])
            commit_r = str(fixture["commit_r"])
            self.git("remote", "remove", "origin", cwd=repo)

            code, evidence, attestation, _ = self.run_pr_verify(
                fixture,
                [self.query(commit_r)],
            )

            self.assertEqual(code, 1)
            self.assertEqual(evidence["status"], "fail")
            self.assertEqual(evidence["source"]["commit"], "")
            self.assertEqual(attestation["query_status"], "authenticated")
            self.assertEqual(attestation["equality"], "unknown")
            self.assertEqual(attestation["disposition"], "block_infra")
            self.assertEqual(attestation["reason"], "exact_head_unavailable")
            self.assertEqual(evidence["gates"][0]["class"], "infra")

    def test_unknown_resolution_query_fails_closed_before_local_lookup(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            fixture = self.create_fixture(Path(temp))
            code, evidence, attestation, _ = self.run_pr_verify(
                fixture,
                [self.query(status="unavailable")],
            )

            self.assertEqual(code, 1)
            self.assertEqual(evidence["source"]["commit"], "")
            self.assertEqual(attestation["query_status"], "unavailable")
            self.assertEqual(attestation["equality"], "unknown")
            self.assertEqual(attestation["reason"], "exact_head_unavailable")

    def test_fixture_b_head_advance_blocks_green_evidence_and_successful_completion(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            fixture = self.create_fixture(root, advance=False)
            publisher = Path(fixture["publisher"])
            commit_a = str(fixture["commit_a"])
            signal = root / "gate-started"
            release = root / "gate-release"
            head_file = root / "oracle-head"
            head_file.write_text(commit_a, encoding="utf-8")
            barrier = root / "barrier.sh"
            barrier.write_text(
                "#!/bin/sh\n"
                f": > {shlex.quote(str(signal))}\n"
                f"while [ ! -e {shlex.quote(str(release))} ]; do sleep 0.01; done\n"
                "exit 0\n",
                encoding="utf-8",
            )
            barrier.chmod(0o755)

            def live_query(*_args: object) -> dict[str, object]:
                return self.query(head_file.read_text(encoding="utf-8").strip())

            result: dict[str, object] = {}

            def run() -> None:
                try:
                    result["value"] = self.run_pr_verify(
                        fixture,
                        live_query,
                        gate_command=str(barrier),
                        complete_step=True,
                    )
                except BaseException as exc:  # pragma: no cover - surfaced below
                    result["error"] = exc

            thread = threading.Thread(target=run)
            thread.start()
            deadline = time.monotonic() + 10
            while not signal.exists() and time.monotonic() < deadline:
                time.sleep(0.01)
            self.assertTrue(signal.exists(), "fixture gate did not reach the deterministic barrier")
            (publisher / "advanced.txt").write_text("commit B\n", encoding="utf-8")
            self.git("add", "advanced.txt", cwd=publisher)
            self.git("commit", "-m", "advance PR head to B", cwd=publisher)
            commit_b = self.git("rev-parse", "HEAD", cwd=publisher)
            self.git("push", "origin", "HEAD:refs/pull/42/head", "--force", cwd=publisher)
            head_file.write_text(commit_b, encoding="utf-8")
            release.touch()
            thread.join(timeout=10)
            self.assertFalse(thread.is_alive(), "fixture verifier did not leave the barrier")
            if "error" in result:
                raise result["error"]  # type: ignore[misc]
            code, evidence, attestation, completion = result["value"]  # type: ignore[misc]

            self.assertEqual(code, 1)
            self.assertEqual(evidence["status"], "fail")
            self.assertEqual(evidence["source"]["commit"], commit_a)
            self.assertEqual(attestation["evidence_commit"], commit_a)
            self.assertEqual(attestation["github_head_commit"], commit_b)
            self.assertEqual(attestation["equality"], "unequal")
            self.assertEqual(attestation["disposition"], "block_infra")
            self.assertEqual(attestation["reason"], "exact_head_mismatch")
            self.assertEqual(evidence["gates"][-1]["class"], "infra")
            completion.assert_called_once()
            self.assertEqual(completion.call_args.args[3], commit_a)
            self.assertEqual(completion.call_args.args[4], "fail")

    def test_unknown_write_query_fails_closed_instead_of_using_resolution_cache(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            fixture = self.create_fixture(Path(temp))
            commit_r = str(fixture["commit_r"])

            code, evidence, attestation, _ = self.run_pr_verify(
                fixture,
                [self.query(commit_r), self.query(status="unavailable")],
            )

            self.assertEqual(code, 1)
            self.assertEqual(evidence["status"], "fail")
            self.assertEqual(attestation["query_status"], "unavailable")
            self.assertEqual(attestation["equality"], "unknown")
            self.assertEqual(attestation["disposition"], "block_infra")
            self.assertEqual(attestation["reason"], "exact_head_unavailable")
            self.assertEqual(evidence["gates"][-1]["class"], "infra")

    def test_attestation_schema_rejects_invalid_closed_vocabulary_combinations(self) -> None:
        sha = "a" * 40
        resolution = self.query(sha)
        attestation = verify.make_exact_head_attestation(
            "fixture-a",
            "research_slice",
            "verify",
            self.PR_IDENTITY,
            resolution,
            resolution,
            sha,
            "equal",
            "dispatch",
            "exact_head_equal",
            "not_dispatched",
        )
        attestation["evidence_path"] = "target/agent-evidence/fixture-a.json"
        attestation["evidence_sha256"] = "b" * 64
        verify.validate_exact_head_attestation(attestation)

        invalid_rows = (
            {"query_status": "cached"},
            {"equality": "probably"},
            {"disposition": "warn"},
            {"reason": "content_failure"},
            {"review_phase": "reviewing"},
            {"query_status": "unavailable", "equality": "equal"},
            {"equality": "unequal", "disposition": "dispatch"},
            {"disposition": "block_infra", "reason": "exact_head_equal"},
        )
        for changes in invalid_rows:
            with self.subTest(changes=changes):
                invalid = dict(attestation)
                invalid.update(changes)
                with self.assertRaises(ValueError):
                    verify.validate_exact_head_attestation(invalid)


if __name__ == "__main__":
    unittest.main()
