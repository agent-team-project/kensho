#!/usr/bin/env python3
"""End-to-end smoke test for the agent-team binary.

The binary ships `init`, `run`, `doctor`, `instance`, and `template`. This
smoke exercises the `init` and `template show` paths plus the bundled
template's parameter substitution, without requiring `claude` on PATH.

Usage:
    smoke_init.py <path-to-agent-team-binary>
"""

from __future__ import annotations

from dataclasses import dataclass
import subprocess
import sys
import tempfile
import tomllib
import os
import time
import json
import signal
import shlex
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

EXPECTED_AFTER_INIT = [
    ".agent_team/.template.lock",
    ".agent_team/config.toml",
    ".agent_team/instances.toml",
    ".agent_team/agents/manager/agent.md",
    ".agent_team/agents/manager/config.toml",
    ".agent_team/agents/manager/skills/assign-worker/SKILL.md",
    ".agent_team/agents/reviewer/agent.md",
    ".agent_team/agents/reviewer/config.toml",
    ".agent_team/agents/verifier/agent.md",
    ".agent_team/agents/verifier/config.toml",
    ".agent_team/agents/worker/agent.md",
    ".agent_team/agents/worker/config.toml",
    ".agent_team/skills/github/SKILL.md",
    ".agent_team/skills/github/scripts/github-api.sh",
    ".agent_team/skills/github/scripts/github-auth.sh",
    ".agent_team/skills/channel/SKILL.md",
    ".agent_team/skills/inbox/SKILL.md",
    ".agent_team/skills/linear/SKILL.md",
    ".agent_team/skills/linear/scripts/linear-graphql.sh",
    ".agent_team/skills/pull-request/SKILL.md",
    ".agent_team/scripts/skills/python.sh",
    ".agent_team/skills/status/SKILL.md",
    ".agent_team/skills/status/scripts/status.sh",
    ".agent_team/skills/status/scripts/_status_write.py",
    ".agent_team/skills/verify/SKILL.md",
    ".agent_team/skills/verify/scripts/validate_gate_tiers.py",
    ".agent_team/skills/verify/scripts/verify.sh",
    ".agent_team/skills/verify/scripts/verify.py",
]

FORBIDDEN_ARTIFACT_DIRS = {
    "__pycache__",
    ".mypy_cache",
    ".pytest_cache",
    ".ruff_cache",
    "node_modules",
}
FORBIDDEN_ARTIFACT_FILES = {
    ".DS_Store",
    "Thumbs.db",
}
FORBIDDEN_ARTIFACT_SUFFIXES = {
    ".pyc",
    ".pyo",
}

PLAN_SHAPE_TOPOLOGY_FIXTURE = """
# Fixture owned by smoke lifecycle shape assertions. Keep exact row/count
# checks coupled to this local topology, not to the bundled template's
# evolving default instances.
[instances.manager]
agent = "manager"

[instances.ticket-manager]
agent = "ticket-manager"

[instances.feedback-triage]
agent = "manager"
ephemeral = true

[instances.harness-reviewer]
agent = "manager"
ephemeral = true

[instances.reviewer]
agent = "reviewer"
ephemeral = true

[instances.worker]
agent = "worker"
ephemeral = true
"""


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        print(f"usage: {argv[0]} <path-to-agent-team-binary>", file=sys.stderr)
        return 2
    binary = Path(argv[1]).resolve()
    if not binary.is_file():
        print(f"binary not found: {binary}", file=sys.stderr)
        return 2
    clean_env = scrub_agent_team_env(os.environ.copy())
    os.environ.clear()
    os.environ.update(clean_env)

    problems: list[str] = []
    with tempfile.TemporaryDirectory() as tmp:
        ticketless_target = Path(tmp) / "ticketless"
        ticketless_target.mkdir()

        # --- init with zero --set flags, the ticketless quickstart path ---
        run([str(binary), "init", "--target", str(ticketless_target)])
        for rel in EXPECTED_AFTER_INIT:
            if not (ticketless_target / rel).exists():
                problems.append(f"missing after ticketless init: {rel}")
        ticketless_cfg_text = (ticketless_target / ".agent_team" / "config.toml").read_text()
        if 'provider = "none"' not in ticketless_cfg_text:
            problems.append(f"ticketless init did not default pm.provider to none: {ticketless_cfg_text}")
        if 'pm_tool = "none"' not in ticketless_cfg_text:
            problems.append(f"ticketless init did not default pm_tool to none: {ticketless_cfg_text}")
        if 'team_id = ""' not in ticketless_cfg_text or 'ticket_prefix = ""' not in ticketless_cfg_text:
            problems.append(f"ticketless init missing empty Linear placeholders: {ticketless_cfg_text}")
        if 'profile = "slim"' not in ticketless_cfg_text:
            problems.append(f"ticketless init did not render slim profile by default: {ticketless_cfg_text}")
        if (ticketless_target / ".agent_team" / "agents" / "ticket-manager").exists():
            problems.append("ticketless slim init unexpectedly rendered ticket-manager agent")
        try:
            tomllib.loads(ticketless_cfg_text)
        except Exception as e:  # noqa: BLE001
            problems.append(f"ticketless config.toml not valid TOML: {e}")

        ticketless_linear = ticketless_target / ".agent_team" / "skills" / "linear" / "scripts" / "linear-graphql.sh"
        r = subprocess.run(
            [str(ticketless_linear), "query { viewer { id } }"],
            cwd=ticketless_target,
            capture_output=True,
            text=True,
        )
        if r.returncode == 0 or "Linear not configured" not in r.stderr:
            problems.append(f"ticketless Linear helper did not fail clearly: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

        r = subprocess.run(
            [str(binary), "doctor", "--strict-daemon", "--target", str(ticketless_target)],
            capture_output=True,
            text=True,
        )
        if r.returncode != 0:
            problems.append(f"doctor --strict-daemon failed on ticketless tree: rc={r.returncode}\nstdout: {r.stdout}\nstderr: {r.stderr}")

        target = Path(tmp) / "linear"
        target.mkdir()

        # --- init with --set, the templates-as-images path ---
        run([
            str(binary), "init", "--target", str(target),
            "--set", "linear.team_id=smoke-team-uuid",
            "--set", "linear.ticket_prefix=SMK",
        ])
        for rel in EXPECTED_AFTER_INIT:
            if not (target / rel).exists():
                problems.append(f"missing after init: {rel}")
        for rel in generated_artifacts(target / ".agent_team"):
            problems.append(f"generated/cache artifact leaked into .agent_team/: {rel}")

        # The init-time template manifest must NOT leak into the consumer tree.
        if (target / ".agent_team" / "template.toml").exists():
            problems.append("template.toml leaked into .agent_team/")

        # Resolved config must contain --set values.
        cfg_text = (target / ".agent_team" / "config.toml").read_text()
        if 'provider = "linear"' not in cfg_text:
            problems.append(f"--set linear.* did not set pm.provider=linear in config.toml: {cfg_text}")
        if 'pm_tool = "linear"' not in cfg_text:
            problems.append(f"--set linear.* did not set legacy team.pm_tool=linear in config.toml: {cfg_text}")
        if 'team_id = "smoke-team-uuid"' not in cfg_text:
            problems.append(f"--set linear.team_id missing from config.toml: {cfg_text}")
        if 'ticket_prefix = "SMK"' not in cfg_text:
            problems.append(f"--set linear.ticket_prefix missing from config.toml: {cfg_text}")
        if 'profile = "slim"' not in cfg_text:
            problems.append(f"linear init did not render slim profile by default: {cfg_text}")
        try:
            tomllib.loads(cfg_text)
        except Exception as e:  # noqa: BLE001
            problems.append(f"config.toml not valid TOML: {e}")
        instances_text = (target / ".agent_team" / "instances.toml").read_text()
        if 'trigger.event = "ticket.status_changed"' not in instances_text or 'trigger.match.status = "Ready for Agent"' not in instances_text:
            problems.append(f"instances.toml missing rendered Linear column trigger: {instances_text}")
        if "{{" in instances_text or "}}" in instances_text:
            problems.append(f"instances.toml contains unrendered template delimiters: {instances_text}")
        try:
            tomllib.loads(instances_text)
        except Exception as e:  # noqa: BLE001
            problems.append(f"instances.toml not valid TOML: {e}")
        check_bundled_topology_canary(binary, target, problems)
        check_ticket_verb_linear_smoke(binary, target, problems)

        # Template provenance must be present and parseable for future upgrade.
        lock_path = target / ".agent_team" / ".template.lock"
        try:
            lock = tomllib.loads(lock_path.read_text())
        except Exception as e:  # noqa: BLE001
            problems.append(f".template.lock not valid TOML: {e}")
        else:
            template_lock = lock.get("template", {})
            for key in ("ref", "content_hash", "name", "version"):
                if not template_lock.get(key):
                    problems.append(f".template.lock missing [template].{key}: {lock}")
            if not str(template_lock.get("content_hash", "")).startswith("sha256:"):
                problems.append(f".template.lock content_hash missing sha256 prefix: {lock}")

        # Bundled .sh scripts must remain executable after init — render.go's
        # `isExecutableTemplate` restores +x because embed.FS drops mode bits.
        for rel in (
            ".agent_team/skills/linear/scripts/linear-graphql.sh",
            ".agent_team/skills/status/scripts/status.sh",
        ):
            sh = target / rel
            if sh.exists() and not (sh.stat().st_mode & 0o111):
                problems.append(f"{sh} is not executable after init")

        # Status skill should default durable work metadata from the
        # daemon-exported session environment.
        status_state = target / ".agent_team" / "state" / "worker-squ-99"
        status_script = target / ".agent_team" / "skills" / "status" / "scripts" / "status.sh"
        env = os.environ.copy()
        env.update({
            "AGENT_TEAM_STATE_DIR": str(status_state),
            "AGENT_TEAM_JOB_ID": "squ-99",
            "AGENT_TEAM_TICKET": "SQU-99",
            "AGENT_TEAM_PR": "https://github.com/acme/repo/pull/99",
            "AGENT_TEAM_BRANCH": "worker-squ-99",
        })
        run([
            str(status_script), "set", "implementing",
            "--desc", "checking status context",
        ], env=env)
        try:
            status_doc = tomllib.loads((status_state / "status.toml").read_text())
        except Exception as e:  # noqa: BLE001
            problems.append(f"status.toml not valid TOML after status skill run: {e}")
        else:
            work = status_doc.get("work", {})
            expected_work = {
                "job": "squ-99",
                "ticket": "SQU-99",
                "pr": "https://github.com/acme/repo/pull/99",
                "branch": "worker-squ-99",
            }
            for key, expected in expected_work.items():
                if work.get(key) != expected:
                    problems.append(f"status skill did not default work.{key}: {status_doc}")

        check_helper_env_loading(target, problems)

        # Re-init without --force should keep the user-edited config.toml.
        cfg_path = target / ".agent_team" / "config.toml"
        cfg_path.write_text("# user-edited\n")
        run([
            str(binary), "init", "--target", str(target),
            "--set", "linear.team_id=should-not-overwrite",
            "--set", "linear.ticket_prefix=NOP",
        ])
        if cfg_path.read_text() != "# user-edited\n":
            problems.append("re-init overwrote a user-edited config.toml (must be untouched)")

        # --- --no-input fails clearly when Linear mode is selected but required params are missing ---
        with tempfile.TemporaryDirectory() as tmp2:
            r = subprocess.run(
                [str(binary), "init", "--target", tmp2, "--set", "team.pm_tool=linear", "--no-input"],
                capture_output=True, text=True,
            )
            if r.returncode == 0:
                problems.append("--no-input Linear init succeeded but should have failed")
            elif "missing" not in r.stderr.lower():
                problems.append(f"--no-input error message missing 'missing': {r.stderr}")

        # --- template show on the bundled template prints the manifest ---
        r = subprocess.run(
            [str(binary), "template", "show"],
            capture_output=True, text=True,
        )
        if r.returncode != 0:
            problems.append(f"template show failed: {r.stderr}")
        for needle in ("Template: default v", "Content hash: sha256:", "template.profile", "linear.team_id", "linear.ticket_prefix"):
            if needle not in r.stdout:
                problems.append(f"template show missing {needle!r} in stdout: {r.stdout!r}")

        # --- template ls includes bundled ---
        r = subprocess.run(
            [str(binary), "template", "ls"],
            capture_output=True, text=True,
        )
        if r.returncode != 0:
            problems.append(f"template ls failed: {r.stderr}")
        if "bundled" not in r.stdout:
            problems.append(f"template ls missing 'bundled': {r.stdout!r}")

        # --- doctor on the freshly-initialised tree should pass ---
        # The user-edited config.toml from the earlier step won't have the
        # required keys; rewrite a valid one for this check.
        cfg_path.write_text(cfg_text)
        r = subprocess.run(
            [str(binary), "doctor", "--strict-daemon", "--target", str(target)],
            capture_output=True, text=True,
        )
        if r.returncode != 0:
            problems.append(f"doctor --strict-daemon failed on a healthy tree: rc={r.returncode}\nstdout: {r.stdout}\nstderr: {r.stderr}")

        # --- upgrade --check compares .template.lock to the bundled ref ---
        r = subprocess.run(
            [str(binary), "upgrade", "--check", "--target", str(target)],
            capture_output=True, text=True,
        )
        if r.returncode != 0:
            problems.append(f"upgrade --check failed: rc={r.returncode}\nstdout: {r.stdout}\nstderr: {r.stderr}")
        for needle in ("Locked ref: bundled", "Target ref: bundled", "already up to date"):
            if needle not in r.stdout:
                problems.append(f"upgrade --check missing {needle!r} in stdout: {r.stdout!r}")

        # --- daemon start / status / stop, when agent-teamd is sibling-binary ---
        # The daemon binary is built alongside agent-team in CI. If a sibling
        # agent-teamd exists, exercise the lifecycle. Otherwise skip silently.
        teamd_path = binary.parent / "agent-teamd"
        if teamd_path.is_file():
            problems.extend(check_daemon_lifecycle(binary, target))

    if problems:
        print("smoke_init_go failed:", file=sys.stderr)
        for p in problems:
            print(f"  - {p}", file=sys.stderr)
        return 1
    print("OK  agent-team init + template + doctor + daemon")
    return 0


def check_bundled_topology_canary(binary: Path, target: Path, problems: list[str]) -> None:
    """Single smoke canary for the bundled template's current topology shape."""
    r = subprocess.run(
        [str(binary), "plan", "--summary", "--json", "--target", str(target)],
        capture_output=True,
        text=True,
    )
    try:
        body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"bundled topology canary returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        return
    summary = body.get("summary") or {}
    # This is the one smoke assertion for the bundled template's current
    # topology. Lifecycle row/count checks below overwrite instances.toml with
    # PLAN_SHAPE_TOPOLOGY_FIXTURE so adding a bundled instance only updates
    # this canary.
    if (
        r.returncode != 0
        or summary.get("total") != 4
        or summary.get("actions", {}).get("start") != 1
        or summary.get("actions", {}).get("on-demand") != 3
        or not summary.get("dry_run")
        or summary.get("statuses", {}).get("unknown") != 4
    ):
        problems.append(f"bundled topology canary returned unexpected summary: rc={r.returncode}\nbody={body}\nstdout={r.stdout}\nstderr={r.stderr}")


def check_ticket_verb_linear_smoke(binary: Path, target: Path, problems: list[str]) -> None:
    """Verify rendered Linear config drives `agent-team ticket create`."""
    requests: list[dict[str, object]] = []

    class LinearHandler(BaseHTTPRequestHandler):
        def do_POST(self) -> None:  # noqa: N802
            length = int(self.headers.get("Content-Length", "0"))
            raw = self.rfile.read(length).decode("utf-8")
            try:
                payload = json.loads(raw)
            except Exception as e:  # noqa: BLE001
                requests.append({"error": f"invalid JSON: {e}", "raw": raw})
                self.send_response(400)
                self.end_headers()
                return
            requests.append({
                "authorization": self.headers.get("Authorization"),
                "payload": payload,
            })
            body = {
                "data": {
                    "issueCreate": {
                        "success": True,
                        "issue": {
                            "id": "lin-smoke-1",
                            "identifier": "SMK-1",
                            "url": "https://linear.app/smoke/issue/SMK-1/ticket-verb-smoke",
                            "title": "Ticket verb smoke",
                            "state": {"name": "Todo"},
                            "labels": {"nodes": []},
                        },
                    },
                },
            }
            data = json.dumps(body).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def log_message(self, _format: str, *_args: object) -> None:
            return

    server = HTTPServer(("127.0.0.1", 0), LinearHandler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        env = os.environ.copy()
        env.update({
            "AGENT_TEAM_LINEAR_GRAPHQL_URL": f"http://127.0.0.1:{server.server_port}",
            "LINEAR_API_KEY": "linear-ticket-verb-token",
            "AGENT_TEAM_TEAM": "smoke",
            "AGENT_TEAM_INSTANCE": "feedback-triage",
            "AGENT_TEAM_ORIGIN_AGENT": "manager",
            "AGENT_TEAM_JOB_ID": "ticket-verb-smoke",
            "AGENT_TEAM_ORIGIN_TRIGGER": "schedule:feedback-triage",
        })
        r = subprocess.run(
            [
                str(binary),
                "ticket",
                "create",
                "--repo",
                str(target),
                "--title",
                "Ticket verb smoke",
                "--body",
                "Smoke body from a skill filing path.",
                "--json",
            ],
            capture_output=True,
            text=True,
            env=env,
        )
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)

    if r.returncode != 0:
        problems.append(f"agent-team ticket create Linear smoke failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}\nrequests={requests}")
        return
    try:
        result = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"agent-team ticket create Linear smoke returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        return
    if result.get("provider") != "linear" or result.get("issue") != "SMK-1":
        problems.append(f"agent-team ticket create Linear smoke returned unexpected result: {result}")
    if len(requests) != 1:
        problems.append(f"agent-team ticket create Linear smoke made {len(requests)} requests, want 1: {requests}")
        return
    req = requests[0]
    if req.get("authorization") != "linear-ticket-verb-token":
        problems.append(f"agent-team ticket create Linear smoke used wrong Authorization header: {req}")
    payload = req.get("payload")
    if not isinstance(payload, dict):
        problems.append(f"agent-team ticket create Linear smoke captured invalid payload: {req}")
        return
    if "issueCreate" not in str(payload.get("query", "")):
        problems.append(f"agent-team ticket create Linear smoke did not call issueCreate: {payload}")
    variables = payload.get("variables")
    if not isinstance(variables, dict):
        problems.append(f"agent-team ticket create Linear smoke payload missing variables: {payload}")
        return
    input_payload = variables.get("input")
    if not isinstance(input_payload, dict):
        problems.append(f"agent-team ticket create Linear smoke payload missing input: {payload}")
        return
    if input_payload.get("teamId") != "smoke-team-uuid":
        problems.append(f"agent-team ticket create Linear smoke used wrong teamId: {input_payload}")
    description = str(input_payload.get("description", ""))
    for needle in ("Smoke body from a skill filing path.", "agent-team-origin:", "team=smoke", "instance=feedback-triage", "job=ticket-verb-smoke"):
        if needle not in description:
            problems.append(f"agent-team ticket create Linear smoke description missing {needle!r}: {description}")


def write_plan_shape_topology_fixture(team_dir: Path) -> None:
    (team_dir / "instances.toml").write_text(PLAN_SHAPE_TOPOLOGY_FIXTURE, encoding="utf-8")


def check_helper_env_loading(target: Path, problems: list[str]) -> None:
    """Regression test for token loading from non-shell-sourceable .env lines."""
    team_dir = target / ".agent_team"
    cfg_path = team_dir / "config.toml"
    env_path = target / ".env"
    linear_helper = team_dir / "skills" / "linear" / "scripts" / "linear-graphql.sh"
    github_helper = team_dir / "skills" / "github" / "scripts" / "github-api.sh"
    query = "query { viewer { id } }"

    if not linear_helper.is_file():
        problems.append(f"missing rendered Linear helper for .env loading smoke: {linear_helper}")
        return
    if not github_helper.is_file():
        problems.append(f"missing rendered GitHub helper for .env loading smoke: {github_helper}")
        return

    original_cfg = cfg_path.read_text()
    original_env = env_path.read_text() if env_path.exists() else None
    linear_token = "linear-$! & token with spaces, \"double quotes\", and 'single quotes'"
    github_token = "github-$! & token with spaces, \"double quotes\", and 'single quotes'"
    # Keep these values unquoted: source/xargs-style loaders treat the spaces and
    # `&` as shell syntax or word splits, while the helper parser reads the key's
    # full line value.
    env_text = (
        "# intentionally not shell-sourceable\n"
        f"LINEAR_API_KEY={linear_token}\n"
        f"export GITHUB_TOKEN={github_token}\n"
    )
    github_cfg = """
[pm]
provider = "github"

[team]
pm_tool = "github"

[github]
owner = "smoke-owner"
repo = "smoke-repo"
"""

    try:
        env_path.write_text(env_text, encoding="utf-8")
        with (
            tempfile.TemporaryDirectory(prefix="agt-helper-old-python-", dir="/tmp") as old_python_dir,
            tempfile.TemporaryDirectory(prefix="agt-helper-bin-", dir="/tmp") as fake_dir,
        ):
            old_python_bin = Path(old_python_dir)
            fake_bin = Path(fake_dir)
            fake_old_python = old_python_bin / "python3"
            fake_old_python.write_text(
                """#!/bin/sh
if [ "${1:-}" = "-c" ]; then
    printf '3.9.18\\n'
    exit 1
fi
echo "old python3 should not run helper bodies" >&2
exit 86
""",
                encoding="utf-8",
            )
            fake_old_python.chmod(0o755)
            fake_python311 = fake_bin / "python3.11"
            fake_python311.write_text(
                f"#!/bin/sh\nexec {shlex.quote(sys.executable)} \"$@\"\n",
                encoding="utf-8",
            )
            fake_python311.chmod(0o755)
            fake_curl = fake_bin / "curl"
            fake_curl.write_text(
                f"#!/bin/sh\nexec {shlex.quote(sys.executable)} - \"$@\" <<'PY'\n"
                + """import json
import os
import sys

args = sys.argv[1:]
expected_auth = os.environ["EXPECTED_AUTHORIZATION"]
expected_query = os.environ["EXPECTED_QUERY"]
headers = [args[i + 1] for i, arg in enumerate(args[:-1]) if arg == "-H"]
if expected_auth not in headers:
    print(f"missing expected Authorization header: {expected_auth!r}; headers={headers!r}", file=sys.stderr)
    sys.exit(17)
payloads = [args[i + 1] for i, arg in enumerate(args[:-1]) if arg == "-d"]
if len(payloads) != 1:
    print(f"expected exactly one -d payload, got {payloads!r}", file=sys.stderr)
    sys.exit(18)
try:
    payload = json.loads(payloads[0])
except Exception as e:
    print(f"payload was not JSON: {e}: {payloads[0]!r}", file=sys.stderr)
    sys.exit(19)
if payload.get("query") != expected_query:
    print(f"unexpected query payload: {payload!r}", file=sys.stderr)
    sys.exit(20)
print(json.dumps({"ok": True}))
PY
""",
                encoding="utf-8",
            )
            fake_curl.chmod(0o755)

            env = os.environ.copy()
            for key in ("LINEAR_API_KEY", "LINEAR_USER_API_KEY", "GITHUB_TOKEN", "GH_TOKEN"):
                env.pop(key, None)
            env.update({
                "AGENT_TEAM_ROOT": str(team_dir),
                "EXPECTED_QUERY": query,
                "PATH": f"{old_python_bin}{os.pathsep}{fake_bin}{os.pathsep}{env.get('PATH', '')}",
            })

            run_helper_env_smoke(
                [str(linear_helper), query],
                target,
                env | {"EXPECTED_AUTHORIZATION": f"Authorization: {linear_token}"},
                "Linear",
                problems,
            )

            cfg_path.write_text(github_cfg, encoding="utf-8")
            run_helper_env_smoke(
                [str(github_helper), "graphql", query],
                target,
                env | {"EXPECTED_AUTHORIZATION": f"Authorization: Bearer {github_token}"},
                "GitHub",
                problems,
            )
            status_state = target / ".agent_team" / "state" / "worker-python-311-smoke"
            run([
                str(team_dir / "skills" / "status" / "scripts" / "status.sh"),
                "set",
                "planning",
                "--desc",
                "python guard smoke",
            ], env=env | {"AGENT_TEAM_STATE_DIR": str(status_state)})
            try:
                status_doc = tomllib.loads((status_state / "status.toml").read_text())
            except Exception as e:  # noqa: BLE001
                problems.append(f"status helper did not run with old python3 first on PATH: {e}")
            else:
                if status_doc.get("status", {}).get("description") != "python guard smoke":
                    problems.append(f"status helper wrote unexpected smoke status: {status_doc}")
    finally:
        cfg_path.write_text(original_cfg, encoding="utf-8")
        if original_env is None:
            env_path.unlink(missing_ok=True)
        else:
            env_path.write_text(original_env, encoding="utf-8")


def run_helper_env_smoke(
    cmd: list[str],
    cwd: Path,
    env: dict[str, str],
    name: str,
    problems: list[str],
) -> None:
    r = run_captured(cmd, cwd=cwd, env=env)
    if r.returncode != 0:
        problems.append(
            f"{name} helper did not preserve shell-special .env token: "
            f"rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}"
        )
        return
    try:
        body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"{name} helper fake curl returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        return
    if body.get("ok") is not True:
        problems.append(f"{name} helper fake curl returned unexpected body: {body}")


def run_captured(cmd: list[str], **kwargs) -> subprocess.CompletedProcess[str]:
    return subprocess.run(cmd, capture_output=True, text=True, **kwargs)


def parse_json_result(
    result: subprocess.CompletedProcess[str],
    problems: list[str],
    context: str,
    fallback,
):
    try:
        return json.loads(result.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"{context}: {e}\nstdout={result.stdout}\nstderr={result.stderr}")
        return fallback


@dataclass(frozen=True)
class DaemonSmokeContext:
    binary: Path
    socket_dir: Path
    fake_bin: Path
    fake_claude: Path
    env: dict[str, str]
    team_dir: Path
    sock: Path
    pid: Path


def prepare_daemon_smoke_context(binary: Path) -> DaemonSmokeContext:
    socket_dir = Path(tempfile.mkdtemp(prefix="agt-smoke-d-", dir="/tmp"))
    fake_bin = Path(tempfile.mkdtemp(prefix="agt-smoke-bin-", dir="/tmp"))
    fake_claude = fake_bin / "claude"
    fake_claude.write_text(
        """#!/bin/sh
set -eu

session=""
mode=""
prev=""
for arg in "$@"; do
    if [ "$prev" = "--session-id" ]; then
        session="$arg"
        mode="start"
        prev=""
        continue
    fi
    if [ "$prev" = "--resume" ]; then
        session="$arg"
        mode="resume"
        prev=""
        continue
    fi
    case "$arg" in
        --session-id|--resume)
            prev="$arg"
            ;;
    esac
done

config_dir="${CLAUDE_CONFIG_DIR:-${HOME:-.}/.claude}"
workspace="$(pwd -P 2>/dev/null || pwd)"
encoded="$(printf '%s' "$workspace" | sed 's/[^[:alnum:]]/-/g')"
if [ -n "$session" ]; then
    session_file="$config_dir/projects/$encoded/$session.jsonl"
    if [ "$mode" = "resume" ] && [ ! -f "$session_file" ]; then
        echo "No conversation found with session ID: $session" >&2
        exit 1
    fi
    if [ "$mode" = "start" ]; then
        mkdir -p "$(dirname "$session_file")"
        printf '{}\\n' >> "$session_file"
    fi
fi

echo "fake claude invoked: $*"
exec sleep 60
"""
    )
    fake_claude.chmod(0o755)
    env = os.environ.copy()
    env["PATH"] = f"{fake_bin}:{env.get('PATH', '')}"
    env["CLAUDE_CONFIG_DIR"] = str(socket_dir / "claude-config")
    team_dir = socket_dir / ".agent_team"
    return DaemonSmokeContext(
        binary=binary,
        socket_dir=socket_dir,
        fake_bin=fake_bin,
        fake_claude=fake_claude,
        env=env,
        team_dir=team_dir,
        sock=team_dir / "daemon.sock",
        pid=team_dir / "daemon.pid",
    )


def cleanup_daemon_smoke_context(ctx: DaemonSmokeContext) -> None:
    pid = ctx.pid
    socket_dir = ctx.socket_dir
    fake_bin = ctx.fake_bin
    # Best-effort: kill any lingering agent-teamd we left running.
    if pid.exists():
        try:
            p = int(pid.read_text().strip())
            subprocess.run(["kill", "-9", str(p)], capture_output=True)
        except (OSError, ValueError):
            pass
    # Clean up the smoke dir.
    import shutil
    shutil.rmtree(socket_dir, ignore_errors=True)
    shutil.rmtree(fake_bin, ignore_errors=True)


def check_daemon_lifecycle(binary: Path, target: Path) -> list[str]:
    """Smoke the daemon-backed lifecycle flow.

    Uses a fake `claude` binary so `agent-team start` can dispatch real
    daemon-managed children without requiring a Claude install. Exercises
    start, ps, logs, stop, and daemon shutdown.
    """
    _ = target
    problems: list[str] = []
    ctx = prepare_daemon_smoke_context(binary)
    try:
        if not _check_daemon_startup_and_planning(ctx, problems):
            return problems
        _check_daemon_logs_events_and_listing(ctx, problems)
        _check_daemon_cleanup_stats_and_monitor(ctx, problems)
        _check_daemon_messaging_status_and_health(ctx, problems)
        _check_daemon_watch_commands(ctx, problems)
        _check_daemon_inspect_kill_attach_and_logs(ctx, problems)
        _check_daemon_reconcile_wait_and_cleanup(ctx, problems)
        _check_daemon_offline_fallbacks(ctx, problems)
    finally:
        cleanup_daemon_smoke_context(ctx)
    return problems


def _check_daemon_startup_and_planning(ctx: DaemonSmokeContext, problems: list[str]) -> bool:
    binary = ctx.binary
    socket_dir = ctx.socket_dir
    env = ctx.env
    team_dir = ctx.team_dir
    sock = ctx.sock
    pid = ctx.pid

    r = run_captured(
        [str(binary), "--help"],
    )
    if r.returncode != 0 or "Docker-like shortcuts:" not in r.stdout:
        problems.append(f"root help missing shortcuts: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif any(text not in r.stdout for text in ("agent-team up", "agent-team down", "agent-team ls", "agent-team top")):
        problems.append(f"root help missing shortcut entries: stdout={r.stdout}\nstderr={r.stderr}")

    # init the smoke target under /tmp so the unix-socket path is short.
    run([
        str(binary), "init", "--target", str(socket_dir),
        "--profile", "full",
        "--set", "linear.team_id=smoke-team-uuid",
        "--set", "linear.ticket_prefix=SMK",
    ])
    team_dir = socket_dir / ".agent_team"
    discord_helper = team_dir / "skills" / "comms" / "scripts" / "discord_delivery.py"
    if not discord_helper.is_file():
        problems.append(f"full template missing shared Discord delivery helper: {discord_helper}")
    full_topology = (team_dir / "instances.toml").read_text(encoding="utf-8")
    if '[schedules.discord-digest]\nevery = "24h"' not in full_topology:
        problems.append("full template Discord digest schedule is not exactly 24h")
    write_plan_shape_topology_fixture(team_dir)
    sock = team_dir / "daemon.sock"
    pid = team_dir / "daemon.pid"

    r = run_captured(
        [str(binary), "daemon", "status", "--target", str(socket_dir)],
    )
    if "not running" not in r.stdout:
        problems.append(f"daemon status before start: {r.stdout!r}")

    r = run_captured(
        [str(binary), "daemon", "start", "--json", "--ready-timeout", "5s", "--target", str(socket_dir)],
        env=env,
    )
    daemon_start_body = parse_json_result(r, problems, "daemon start --json returned invalid JSON", {})
    daemon_start_status = daemon_start_body.get("status") or {}
    if (
        r.returncode != 0
        or daemon_start_body.get("action") != "start"
        or not daemon_start_body.get("changed")
        or not daemon_start_body.get("pid")
        or not daemon_start_body.get("log")
        or not daemon_start_status.get("running")
        or not daemon_start_status.get("ready")
    ):
        problems.append(f"daemon start --json failed: rc={r.returncode}\nbody={daemon_start_body}\nstdout={r.stdout}\nstderr={r.stderr}")
    r = run_captured(
        [str(binary), "daemon", "status", "--wait", "--timeout", "5s", "--json", "--target", str(socket_dir)],
    )
    direct_daemon_status = parse_json_result(
        r,
        problems,
        "daemon status --wait --json after daemon start returned invalid JSON",
        {},
    )
    if r.returncode != 0 or not direct_daemon_status.get("running") or not direct_daemon_status.get("ready"):
        problems.append(
            "daemon status --wait --json after daemon start did not report a ready daemon: "
            f"rc={r.returncode}\nstatus={direct_daemon_status}\nstdout={r.stdout}\nstderr={r.stderr}"
        )
    r = run_captured(
        [str(binary), "daemon", "status", "--format", "{{.Running}}:{{.Ready}}:{{.Instances}}", "--target", str(socket_dir)],
    )
    if r.returncode != 0 or r.stdout.strip() != "true:true:0":
        problems.append(f"daemon status --format after daemon start failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    r = run_captured(
        [str(binary), "daemon", "start", "--format", "{{.Action}}:{{.Changed}}:{{.AlreadyRunning}}:{{.Status.Ready}}", "--target", str(socket_dir)],
        env=env,
    )
    if r.returncode != 0 or r.stdout.strip() != "start:false:true:true":
        problems.append(f"daemon start --format already-running probe failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    r = run_captured(
        [str(binary), "daemon", "status", "--quiet", "--target", str(socket_dir)],
    )
    if r.returncode != 0 or r.stdout or r.stderr:
        problems.append(f"daemon status --quiet after daemon start failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    r = run_captured(
        [str(binary), "daemon", "stop", "--json", "--target", str(socket_dir)],
    )
    daemon_stop_body = parse_json_result(
        r,
        problems,
        "daemon stop --json after direct daemon start returned invalid JSON",
        {},
    )
    daemon_stop_status = daemon_stop_body.get("status") or {}
    if (
        r.returncode != 0
        or daemon_stop_body.get("action") != "stop"
        or not daemon_stop_body.get("changed")
        or not daemon_stop_body.get("previous_pid")
        or not (daemon_stop_body.get("stopped") or daemon_stop_body.get("killed"))
        or daemon_stop_status.get("running")
    ):
        problems.append(f"daemon stop --json after direct daemon start failed: rc={r.returncode}\nbody={daemon_stop_body}\nstdout={r.stdout}\nstderr={r.stderr}")
    r = run_captured(
        [str(binary), "daemon", "status", "--wait", "--down", "--timeout", "5s", "--json", "--target", str(socket_dir)],
    )
    stopped_daemon_status = parse_json_result(
        r,
        problems,
        "daemon status --wait --down --json after daemon stop returned invalid JSON",
        {},
    )
    if r.returncode != 0 or stopped_daemon_status.get("running") or stopped_daemon_status.get("ready"):
        problems.append(
            "daemon status --wait --down --json after daemon stop did not report a stopped daemon: "
            f"rc={r.returncode}\nstatus={stopped_daemon_status}\nstdout={r.stdout}\nstderr={r.stderr}"
        )
    if pid.exists() or sock.exists():
        problems.append(f"daemon runtime files lingered after direct daemon stop: pid={pid.exists()} sock={sock.exists()}")
    r = run_captured(
        [str(binary), "daemon", "stop", "--format", "{{.Action}}:{{.Changed}}:{{.Message}}:{{.Status.Running}}", "--target", str(socket_dir)],
    )
    if r.returncode != 0 or r.stdout.strip() != "stop:false:not running:false":
        problems.append(f"daemon stop --format already-stopped probe failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = run_captured(
        [str(binary), "start", "--dry-run", "--json", "--target", str(socket_dir)],
        env=env,
    )
    dry_start_rows = parse_json_result(r, problems, "start --dry-run --json before daemon returned invalid JSON", [])
    dry_start_by_name = {row.get("instance"): row for row in dry_start_rows if isinstance(row, dict)}
    if r.returncode != 0:
        problems.append(f"start --dry-run before daemon failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif set(dry_start_by_name) != {"manager", "ticket-manager"}:
        problems.append(f"start --dry-run before daemon targeted unexpected instances: {dry_start_rows}")
    elif any(
        (dry_start_by_name.get(name) or {}).get("action") != "start"
        or (dry_start_by_name.get(name) or {}).get("status") != "unknown"
        or not (dry_start_by_name.get(name) or {}).get("dry_run")
        for name in ("manager", "ticket-manager")
    ):
        problems.append(f"start --dry-run before daemon returned unexpected rows: {dry_start_rows}")
    if sock.exists() or pid.exists():
        problems.append(f"start --dry-run created daemon runtime files: sock={sock.exists()} pid={pid.exists()}")

    r = subprocess.run(
        [str(binary), "plan", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        plan_before_start = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"plan --json before start returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        plan_before_start = {}
    plan_before_rows = {
        row.get("instance"): row
        for row in plan_before_start.get("instances") or []
        if isinstance(row, dict)
    }
    plan_before_summary = plan_before_start.get("summary") or {}
    if r.returncode != 0:
        problems.append(f"plan --json before start failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif plan_before_summary.get("start") != 2 or plan_before_summary.get("on_demand") != 4:
        problems.append(f"plan --json before start returned unexpected summary: {plan_before_start}")
    elif any((plan_before_rows.get(name) or {}).get("action") != "start" for name in ("manager", "ticket-manager")):
        problems.append(f"plan --json before start missing start actions: {plan_before_start}")
    elif any((plan_before_rows.get(name) or {}).get("action") != "on-demand" for name in ("reviewer", "worker")):
        problems.append(f"plan --json before start missing on-demand rows: {plan_before_start}")

    r = subprocess.run(
        [str(binary), "plan", "--format", "{{.Instance}}:{{.Action}}", "--agent", "manager", "--status", "unknown", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    formatted_plan_rows = [line.strip() for line in r.stdout.splitlines() if line.strip()]
    if r.returncode != 0 or formatted_plan_rows != ["feedback-triage:on-demand", "harness-reviewer:on-demand", "manager:start"]:
        problems.append(f"plan --format before start failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    worker_state_dir = team_dir / "state" / "worker"
    worker_state_dir.mkdir(parents=True, exist_ok=True)
    (worker_state_dir / "status.toml").write_text(
        '[status]\nphase = "blocked"\ndescription = "smoke blocked"\n',
        encoding="utf-8",
    )
    r = subprocess.run(
        [str(binary), "plan", "--json", "--phase", "blocked", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        phase_plan_before_start = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"plan --phase blocked --json before start returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        phase_plan_before_start = {}
    phase_plan_rows = phase_plan_before_start.get("instances") or []
    if r.returncode != 0:
        problems.append(f"plan --phase blocked --json before start failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif [row.get("instance") for row in phase_plan_rows] != ["worker"]:
        problems.append(f"plan --phase blocked returned unexpected rows: {phase_plan_before_start}")
    elif phase_plan_rows[0].get("phase") != "blocked" or phase_plan_rows[0].get("action") != "on-demand":
        problems.append(f"plan --phase blocked returned unexpected worker row: {phase_plan_before_start}")

    r = subprocess.run(
        [str(binary), "plan", "--format", "{{.Instance}}:{{.Action}}", "--action", "on_demand", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    formatted_action_plan_rows = [line.strip() for line in r.stdout.splitlines() if line.strip()]
    if r.returncode != 0 or set(formatted_action_plan_rows) != {"feedback-triage:on-demand", "harness-reviewer:on-demand", "reviewer:on-demand", "worker:on-demand"}:
        problems.append(f"plan --action on_demand before start failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "sync", "--dry-run", "--format", "{{.Instance}}:{{.Action}}:{{.Status}}", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    formatted_sync_dry_run_rows = {line.strip() for line in r.stdout.splitlines() if line.strip()}
    if r.returncode != 0:
        problems.append(f"sync --dry-run --format before start failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif not {
        "manager:start:unknown",
        "reviewer:on-demand:unknown",
        "ticket-manager:start:unknown",
        "worker:on-demand:unknown",
    }.issubset(formatted_sync_dry_run_rows):
        problems.append(f"sync --dry-run --format before start returned unexpected rows: stdout={r.stdout}\nstderr={r.stderr}")
    if sock.exists() or pid.exists():
        problems.append(f"sync --dry-run --format created daemon runtime files: sock={sock.exists()} pid={pid.exists()}")

    r = subprocess.run(
        [
            str(binary), "sync",
            "--dry-run",
            "--format", "{{.Instance}}:{{.Action}}:{{.Status}}",
            "--agent", "manager",
            "--status", "unknown",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    formatted_filtered_sync_rows = [line.strip() for line in r.stdout.splitlines() if line.strip()]
    if r.returncode != 0 or formatted_filtered_sync_rows != ["feedback-triage:on-demand:unknown", "harness-reviewer:on-demand:unknown", "manager:start:unknown"]:
        problems.append(f"sync --dry-run filtered before start failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [
            str(binary), "sync",
            "--dry-run",
            "--format", "{{.Instance}}:{{.Action}}:{{.Phase}}",
            "--phase", "blocked",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    formatted_phase_sync_rows = [line.strip() for line in r.stdout.splitlines() if line.strip()]
    if r.returncode != 0 or formatted_phase_sync_rows != ["worker:on-demand:blocked"]:
        problems.append(f"sync --dry-run --phase blocked before start failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [
            str(binary), "sync",
            "--dry-run",
            "--format", "{{.Instance}}:{{.Action}}",
            "--agent", "manager",
            "--action", "start",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    formatted_action_sync_rows = [line.strip() for line in r.stdout.splitlines() if line.strip()]
    if r.returncode != 0 or formatted_action_sync_rows != ["manager:start"]:
        problems.append(f"sync --dry-run --action start before start failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [
            str(binary), "plan",
            "--json",
            "--instance", "manager",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    try:
        instance_plan_before_start = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"plan --instance --json before start returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        instance_plan_before_start = {}
    instance_plan_before_rows = instance_plan_before_start.get("instances") or []
    instance_plan_before_summary = instance_plan_before_start.get("summary") or {}
    if r.returncode != 0:
        problems.append(f"plan --instance --json before start failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif instance_plan_before_summary.get("total") != 1 or instance_plan_before_summary.get("start") != 1:
        problems.append(f"plan --instance --json before start returned unexpected summary: {instance_plan_before_start}")
    elif [row.get("instance") for row in instance_plan_before_rows] != ["manager"]:
        problems.append(f"plan --instance --json before start returned unexpected rows: {instance_plan_before_start}")

    r = subprocess.run(
        [
            str(binary), "plan",
            "--json",
            "--agent", "manager",
            "--status", "unknown",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    try:
        filtered_plan_before_start = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"filtered plan --json before start returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        filtered_plan_before_start = {}
    filtered_plan_before_rows = filtered_plan_before_start.get("instances") or []
    filtered_plan_before_summary = filtered_plan_before_start.get("summary") or {}
    if r.returncode != 0:
        problems.append(f"filtered plan --json before start failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif filtered_plan_before_summary.get("total") != 3 or filtered_plan_before_summary.get("start") != 1 or filtered_plan_before_summary.get("on_demand") != 2:
        problems.append(f"filtered plan --json before start returned unexpected summary: {filtered_plan_before_start}")
    elif [row.get("instance") for row in filtered_plan_before_rows] != ["feedback-triage", "harness-reviewer", "manager"]:
        problems.append(f"filtered plan --json before start returned unexpected rows: {filtered_plan_before_start}")

    r = subprocess.run(
        [str(binary), "start", "--wait", "--timeout", "5s", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True, env=env,
    )
    try:
        start_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"start --wait --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        start_body = {}
    start_rows = start_body.get("actions") or []
    start_health = start_body.get("health") or {}
    if r.returncode != 0:
        problems.append(f"agent-team start failed: stdout={r.stdout}\nstderr={r.stderr}")
        return False
    if not start_health.get("healthy") or start_health.get("issues"):
        problems.append(f"start --wait --json did not report healthy fleet: {start_body}")
    start_by_name = {row.get("instance"): row for row in start_rows}
    for name in ("manager", "ticket-manager"):
        row = start_by_name.get(name)
        if not row or row.get("action") != "start" or row.get("status") != "running" or not row.get("pid"):
            problems.append(f"start --wait --json missing running {name}: {start_body}")

    if not sock.exists():
        problems.append(f"daemon socket missing after start: {sock}")
    if not pid.exists():
        problems.append(f"daemon pidfile missing after start: {pid}")

    r = subprocess.run(
        [str(binary), "daemon", "status", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        daemon_status = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"daemon status --json after start returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        daemon_status = {}
    if (
        r.returncode != 0
        or not daemon_status.get("running")
        or not daemon_status.get("ready")
        or not daemon_status.get("socket_exists")
        or daemon_status.get("instances", 0) < 2
    ):
        problems.append(
            "daemon status --json after start did not report a ready daemon: "
            f"rc={r.returncode}\nstatus={daemon_status}\nstdout={r.stdout}\nstderr={r.stderr}"
        )

    r = subprocess.run(
        [str(binary), "start", "--dry-run", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True, env=env,
    )
    try:
        running_dry_start_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"start --dry-run --json with daemon returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        running_dry_start_rows = []
    running_dry_start_by_name = {row.get("instance"): row for row in running_dry_start_rows if isinstance(row, dict)}
    if r.returncode != 0:
        problems.append(f"start --dry-run with daemon failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif any(
        (running_dry_start_by_name.get(name) or {}).get("action") != "skip"
        or (running_dry_start_by_name.get(name) or {}).get("status") != "running"
        or not (running_dry_start_by_name.get(name) or {}).get("dry_run")
        or not (running_dry_start_by_name.get(name) or {}).get("pid")
        for name in ("manager", "ticket-manager")
    ):
        problems.append(f"start --dry-run with daemon returned unexpected rows: {running_dry_start_rows}")

    r = subprocess.run(
        [str(binary), "plan", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        plan_after_start = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"plan --json after start returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        plan_after_start = {}
    plan_after_rows = {
        row.get("instance"): row
        for row in plan_after_start.get("instances") or []
        if isinstance(row, dict)
    }
    plan_after_summary = plan_after_start.get("summary") or {}
    if r.returncode != 0:
        problems.append(f"plan --json after start failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif plan_after_summary.get("keep") != 2 or plan_after_summary.get("on_demand") != 4:
        problems.append(f"plan --json after start returned unexpected summary: {plan_after_start}")
    elif any((plan_after_rows.get(name) or {}).get("action") != "keep" for name in ("manager", "ticket-manager")):
        problems.append(f"plan --json after start missing keep actions: {plan_after_start}")
    elif any((plan_after_rows.get(name) or {}).get("action") != "on-demand" for name in ("reviewer", "worker")):
        problems.append(f"plan --json after start missing on-demand actions: {plan_after_start}")
    return True


def _check_daemon_logs_events_and_listing(ctx: DaemonSmokeContext, problems: list[str]) -> None:
    binary = ctx.binary
    socket_dir = ctx.socket_dir
    env = ctx.env
    team_dir = ctx.team_dir
    sock = ctx.sock
    pid = ctx.pid

    daemon_log = team_dir / "daemon" / "agent-teamd.log"
    with daemon_log.open("a", encoding="utf-8") as f:
        f.write("smoke daemon log sentinel\n")
    r = subprocess.run(
        [str(binary), "logs", "--daemon", "--tail", "1", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or r.stdout != "smoke daemon log sentinel\n":
        problems.append(f"logs --daemon failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "logs", "--daemon", "--tail", "all", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or "smoke daemon log sentinel\n" not in r.stdout:
        problems.append(f"logs --daemon --tail all failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "daemon", "logs", "--tail", "1", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or r.stdout != "smoke daemon log sentinel\n":
        problems.append(f"daemon logs alias failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "daemon", "logs", "--tail", "all", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or "smoke daemon log sentinel\n" not in r.stdout:
        problems.append(f"daemon logs --tail all failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    # /v1/instances over the unix socket should include the persistent instances.
    r = subprocess.run(
        ["curl", "-s", "--unix-socket", str(sock), "http://./v1/instances"],
        capture_output=True, text=True,
    )
    if "manager" not in r.stdout or "ticket-manager" not in r.stdout:
        problems.append(f"/v1/instances missing started instances: {r.stdout!r}")

    r = subprocess.run(
        [str(binary), "ps", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or "running" not in r.stdout or "manager" not in r.stdout:
        problems.append(f"ps after start failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "ls", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or "running" not in r.stdout or "manager" not in r.stdout:
        problems.append(f"ls alias after start failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "events", "--tail", "20", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        event_rows = [json.loads(line) for line in r.stdout.splitlines() if line.strip()]
    except Exception as e:  # noqa: BLE001
        problems.append(f"events --json returned invalid JSONL: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        event_rows = []
    event_pairs = {(row.get("action"), row.get("instance")) for row in event_rows}
    if r.returncode != 0 or ("dispatch", "manager") not in event_pairs or ("dispatch", "ticket-manager") not in event_pairs:
        problems.append(f"events --json missing startup dispatches: rc={r.returncode}\nevents={event_rows}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "events", "--tail", "20", "--summary", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        event_summary = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"events --summary --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        event_summary = {}
    event_summary_actions = event_summary.get("actions") or {}
    event_summary_statuses = event_summary.get("statuses") or {}
    event_summary_instances = event_summary.get("instances") or {}
    if (
        r.returncode != 0
        or event_summary.get("total", 0) < 2
        or event_summary_actions.get("dispatch", 0) < 2
        or event_summary_statuses.get("running", 0) < 2
        or event_summary_instances.get("manager", 0) < 1
        or event_summary_instances.get("ticket-manager", 0) < 1
    ):
        problems.append(
            "events --summary --json missing startup aggregates: "
            f"rc={r.returncode}\nsummary={event_summary}\nstdout={r.stdout}\nstderr={r.stderr}"
        )

    r = subprocess.run(
        [
            str(binary), "events",
            "--tail", "20",
            "--since", "24h",
            "--action", "dispatch",
            "--agent", "manager",
            "--status", "running",
            "--json",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    try:
        filtered_event_rows = [json.loads(line) for line in r.stdout.splitlines() if line.strip()]
    except Exception as e:  # noqa: BLE001
        problems.append(f"filtered events --json returned invalid JSONL: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        filtered_event_rows = []
    if r.returncode != 0 or not filtered_event_rows:
        problems.append(f"filtered events --json missing manager dispatch: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif not any(row.get("action") == "dispatch" and row.get("instance") == "manager" and row.get("agent") == "manager" and row.get("status") == "running" for row in filtered_event_rows):
        problems.append(f"filtered events --json missing manager-agent dispatch row: {filtered_event_rows}")
    elif any(row.get("action") != "dispatch" or row.get("agent") != "manager" or row.get("status") != "running" for row in filtered_event_rows):
        problems.append(f"filtered events --json returned unexpected rows: {filtered_event_rows}")

    r = subprocess.run(
        [
            str(binary), "events",
            "--tail", "20",
            "--since", "24h",
            "--action", "dispatch",
            "--agent", "manager",
            "--status", "running",
            "--format", "{{.Action}}:{{.Instance}}:{{.Status}}",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    formatted_event_rows = {line.strip() for line in r.stdout.splitlines() if line.strip()}
    if r.returncode != 0 or "dispatch:manager:running" not in formatted_event_rows:
        problems.append(f"events --format filtered output failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif any("ticket-manager" in line for line in formatted_event_rows):
        problems.append(f"events --format unexpectedly included ticket-manager: stdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "events", "--agent", "  ", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode == 0 or "non-empty agent" not in r.stderr:
        problems.append(f"events empty --agent validation failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "status", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or "daemon: running" not in r.stdout or "manager" not in r.stdout:
        problems.append(f"status after start failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "status", "--format", "{{.Instance}}:{{.Status}}", "--agent", "manager", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    formatted_status_rows = {line.strip() for line in r.stdout.splitlines() if line.strip()}
    if r.returncode != 0 or "manager:running" not in formatted_status_rows:
        problems.append(f"status --format filtered output failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "ps", "-q", "--status", "running", "--agent", "manager", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    quiet_rows = [line.strip() for line in r.stdout.splitlines() if line.strip()]
    if r.returncode != 0 or quiet_rows != ["manager"]:
        problems.append(f"ps filtered quiet failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "start", "--format", "{{.Instance}}:{{.Action}}:{{.Status}}", "--agent", "manager", "--status", "running", "--target", str(socket_dir)],
        capture_output=True, text=True, env=env,
    )
    formatted_start_rows = {line.strip() for line in r.stdout.splitlines() if line.strip()}
    if r.returncode != 0 or "manager:skip:running" not in formatted_start_rows:
        problems.append(f"start --format running manager probe failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "stop", "--dry-run", "--format", "{{.Instance}}:{{.Action}}:{{.Status}}", "--agent", "manager", "--status", "running", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    formatted_stop_rows = {line.strip() for line in r.stdout.splitlines() if line.strip()}
    if r.returncode != 0 or "manager:stop:running" not in formatted_stop_rows:
        problems.append(f"stop --dry-run --format running manager probe failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "start", "--quiet", "--agent", "manager", "--status", "running", "--target", str(socket_dir)],
        capture_output=True, text=True, env=env,
    )
    if r.returncode != 0 or r.stdout or r.stderr:
        problems.append(f"start --quiet running manager probe failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "restart", "--quiet", "--dry-run", "--agent", "manager", "--status", "running", "--target", str(socket_dir)],
        capture_output=True, text=True, env=env,
    )
    if r.returncode != 0 or r.stdout or r.stderr:
        problems.append(f"restart --quiet --dry-run running manager probe failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "ps", "--format", "{{.Instance}}:{{.Status}}", "--status", "running", "--agent", "manager", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    formatted_ps_rows = [line.strip() for line in r.stdout.splitlines() if line.strip()]
    if r.returncode != 0 or formatted_ps_rows != ["manager:running"]:
        problems.append(f"ps --format filtered output failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [
            str(binary), "run", "manager",
            "--name", "adhoc",
            "--prompt", "smoke ad-hoc dispatch",
            "--json",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    try:
        run_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"run --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        run_body = {}
    if r.returncode != 0 or run_body.get("instance") != "adhoc" or run_body.get("agent") != "manager":
        problems.append(f"daemon-aware ad-hoc run failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif not run_body.get("pid") or not run_body.get("session_id") or "agent-team logs adhoc --follow" not in run_body.get("follow", ""):
        problems.append(f"run --json missing dispatch metadata: {run_body}")

    r = subprocess.run(
        [
            str(binary), "run", "manager",
            "--name", "detached",
            "--detach",
            "--json",
            "--ready-timeout", "5s",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    try:
        detach_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"run --detach --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        detach_body = {}
    if r.returncode != 0 or detach_body.get("instance") != "detached" or detach_body.get("agent") != "manager":
        problems.append(f"detached daemon run failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif not detach_body.get("pid") or not detach_body.get("session_id") or "agent-team logs detached --follow" not in detach_body.get("follow", ""):
        problems.append(f"run --detach --json missing dispatch metadata: {detach_body}")

    r = subprocess.run(
        [
            str(binary), "run", "manager",
            "--name", "formatted-run",
            "--detach",
            "--format", "{{.Instance}}:{{.Agent}}:{{.PID}}",
            "--ready-timeout", "5s",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    formatted_run_parts = r.stdout.strip().split(":")
    if r.returncode != 0 or len(formatted_run_parts) != 3 or formatted_run_parts[:2] != ["formatted-run", "manager"]:
        problems.append(f"run --detach --format failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    else:
        try:
            formatted_run_pid = int(formatted_run_parts[2])
        except ValueError:
            problems.append(f"run --detach --format returned non-numeric pid: stdout={r.stdout}\nstderr={r.stderr}")
        else:
            if formatted_run_pid <= 0:
                problems.append(f"run --detach --format returned invalid pid: stdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "stop", "formatted-run", "--rm", "--quiet", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or r.stdout or r.stderr:
        problems.append(f"cleanup formatted-run failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif (team_dir / "state" / "formatted-run").exists():
        problems.append("cleanup formatted-run left state dir behind")

    proc = subprocess.Popen(
        [
            str(binary), "run", "manager",
            "--name", "attached-run",
            "--attach",
            "--tail", "all",
            "--ready-timeout", "5s",
            "--target", str(socket_dir),
        ],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, env=env,
    )
    attached_log = team_dir / "daemon" / "attached-run" / "child.log"
    attached_log_has_runtime_output = wait_for_file_contains(attached_log, "fake claude invoked:", timeout=5.0)
    proc.send_signal(signal.SIGINT)
    try:
        run_attach_stdout, run_attach_stderr = proc.communicate(timeout=2)
    except subprocess.TimeoutExpired:
        proc.kill()
        run_attach_stdout, run_attach_stderr = proc.communicate()
        problems.append(
            f"run --attach did not terminate\nstdout={run_attach_stdout}\nstderr={run_attach_stderr}"
        )
    else:
        if proc.returncode != 0:
            problems.append(
                f"run --attach exited non-zero: rc={proc.returncode}\nstdout={run_attach_stdout}\nstderr={run_attach_stderr}"
            )
        elif (
            "dispatched attached-run" not in run_attach_stdout
            or "attaching to attached-run" not in run_attach_stdout
            or ("fake claude invoked:" not in run_attach_stdout and not attached_log_has_runtime_output)
        ):
            problems.append(f"run --attach missing dispatch/log output: stdout={run_attach_stdout}\nstderr={run_attach_stderr}")

    r = subprocess.run(
        [str(binary), "stop", "attached-run", "--rm", "--quiet", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or r.stdout or r.stderr:
        problems.append(f"cleanup attached-run failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif (team_dir / "state" / "attached-run").exists():
        problems.append("cleanup attached-run left state dir behind")

    r = subprocess.run(
        [str(binary), "ps", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or "adhoc" not in r.stdout or "detached" not in r.stdout:
        problems.append(f"ps after ad-hoc/detached dispatch failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif "PID" not in r.stdout:
        problems.append(f"ps table missing PID column: stdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "ps", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        ps_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"ps --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        ps_rows = []
    adhoc_rows = [row for row in ps_rows if row.get("instance") == "adhoc"]
    if r.returncode != 0 or not adhoc_rows:
        problems.append(f"ps --json missing adhoc: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif adhoc_rows[0].get("status") != "running" or not adhoc_rows[0].get("pid"):
        problems.append(f"ps --json adhoc row missing runtime fields: {adhoc_rows[0]}")

    r = subprocess.run(
        [str(binary), "ps", "--json", "--status", "running", "--agent", "manager", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        filtered_ps_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"filtered ps --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        filtered_ps_rows = []
    filtered_instances = {row.get("instance") for row in filtered_ps_rows}
    if r.returncode != 0 or "adhoc" not in filtered_instances:
        problems.append(f"filtered ps --json missing adhoc: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif any(row.get("status") != "running" or row.get("agent") != "manager" for row in filtered_ps_rows):
        problems.append(f"filtered ps --json returned unexpected rows: {filtered_ps_rows}")

    r = subprocess.run(
        [str(binary), "ps", "--json", "--phase", "unknown", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        phase_ps_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"phase-filtered ps --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        phase_ps_rows = []
    phase_instances = {row.get("instance") for row in phase_ps_rows}
    if r.returncode != 0 or "adhoc" not in phase_instances:
        problems.append(f"phase-filtered ps --json missing adhoc: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "ps", "--all", "--summary", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        ps_summary = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"ps --summary --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        ps_summary = {}
    if r.returncode != 0 or ps_summary.get("total", 0) < 3 or ps_summary.get("running", 0) < 3:
        problems.append(
            "ps --summary --json missing expected running instances: "
            f"rc={r.returncode}\nsummary={ps_summary}\nstdout={r.stdout}\nstderr={r.stderr}"
        )
    elif (ps_summary.get("phases") or {}).get("unknown", 0) < 3:
        problems.append(f"ps --summary --json missing unknown phase aggregate: {ps_summary}")


def _check_daemon_cleanup_stats_and_monitor(ctx: DaemonSmokeContext, problems: list[str]) -> None:
    binary = ctx.binary
    socket_dir = ctx.socket_dir
    env = ctx.env
    team_dir = ctx.team_dir
    sock = ctx.sock
    pid = ctx.pid

    r = subprocess.run(
        [
            str(binary), "run", "manager",
            "--name", "cleanup-rm",
            "--detach",
            "--json",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    try:
        cleanup_run_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"cleanup run --detach --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        cleanup_run_body = {}
    if r.returncode != 0 or cleanup_run_body.get("instance") != "cleanup-rm":
        problems.append(f"cleanup daemon run failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "stop", "cleanup-rm", "--rm", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        stop_rm_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"stop --rm --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        stop_rm_rows = []
    cleanup_state = team_dir / "state" / "cleanup-rm"
    if r.returncode != 0 or len(stop_rm_rows) != 1:
        problems.append(f"stop cleanup-rm --rm failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif stop_rm_rows[0].get("action") != "stop" or stop_rm_rows[0].get("instance") != "cleanup-rm":
        problems.append(f"stop --rm returned unexpected row: {stop_rm_rows}")
    elif not stop_rm_rows[0].get("removed") or not stop_rm_rows[0].get("daemon_removed") or not stop_rm_rows[0].get("state_removed"):
        problems.append(f"stop --rm did not report full cleanup: {stop_rm_rows}")
    elif cleanup_state.exists():
        problems.append(f"stop --rm left state dir behind: {cleanup_state}")

    r = subprocess.run(
        [
            str(binary), "run", "manager",
            "--name", "cleanup-quiet",
            "--detach",
            "--json",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    try:
        cleanup_quiet_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"cleanup quiet run --detach --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        cleanup_quiet_body = {}
    if r.returncode != 0 or cleanup_quiet_body.get("instance") != "cleanup-quiet":
        problems.append(f"cleanup quiet daemon run failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "stop", "cleanup-quiet", "--quiet", "--rm", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or r.stdout or r.stderr:
        problems.append(f"stop --quiet --rm cleanup-quiet failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif (team_dir / "state" / "cleanup-quiet").exists():
        problems.append("stop --quiet --rm left cleanup-quiet state dir behind")

    r = subprocess.run(
        [
            str(binary), "run", "worker",
            "--name", "cleanup-all",
            "--detach",
            "--json",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    try:
        cleanup_all_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"cleanup all run --detach --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        cleanup_all_body = {}
    if r.returncode != 0 or cleanup_all_body.get("instance") != "cleanup-all":
        problems.append(f"cleanup all daemon run failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [
            str(binary), "rm",
            "--all",
            "--agent", "worker",
            "--force",
            "--json",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    try:
        rm_all_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"rm --all --agent worker --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        rm_all_rows = []
    rm_all_by_name = {row.get("instance"): row for row in rm_all_rows if isinstance(row, dict)}
    if r.returncode != 0:
        problems.append(f"rm --all --agent worker failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif set(rm_all_by_name) != {"cleanup-all"}:
        problems.append(f"rm --all --agent worker targeted unexpected instances: {rm_all_rows}")
    elif not rm_all_by_name["cleanup-all"].get("removed") or not rm_all_by_name["cleanup-all"].get("daemon_removed"):
        problems.append(f"rm --all --agent worker did not remove cleanup-all metadata: {rm_all_rows}")
    elif (team_dir / "state" / "cleanup-all").exists():
        problems.append("rm --all --agent worker left cleanup-all state dir behind")

    r = subprocess.run(
        [str(binary), "stats", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        stats_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"stats --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        stats_rows = []
    stats_adhoc = [row for row in stats_rows if row.get("instance") == "adhoc"]
    if r.returncode != 0 or not stats_adhoc:
        problems.append(f"stats --json missing adhoc: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif stats_adhoc[0].get("status") != "running" or not stats_adhoc[0].get("pid"):
        problems.append(f"stats --json adhoc row missing runtime fields: {stats_adhoc[0]}")
    elif "cpu_percent" not in stats_adhoc[0] or stats_adhoc[0].get("rss_bytes", 0) <= 0:
        problems.append(f"stats --json adhoc row missing process metrics: {stats_adhoc[0]}")

    r = subprocess.run(
        [str(binary), "stats", "--summary", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        stats_summary = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"stats --summary --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        stats_summary = {}
    if (
        r.returncode != 0
        or stats_summary.get("total", 0) < 3
        or stats_summary.get("running", 0) < 3
        or stats_summary.get("measured", 0) < 3
        or stats_summary.get("rss_bytes", 0) <= 0
        or sum((stats_summary.get("phases") or {}).values()) < stats_summary.get("total", 0)
        or (stats_summary.get("phases") or {}).get("unknown", 0) < 1
    ):
        problems.append(
            "stats --summary --json missing expected running resource totals: "
            f"rc={r.returncode}\nsummary={stats_summary}\nstdout={r.stdout}\nstderr={r.stderr}"
        )

    r = subprocess.run(
        [str(binary), "stats", "--json", "--instance", "manager,ticket-manager", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        instance_stats_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"stats --instance --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        instance_stats_rows = []
    instance_stats_names = {row.get("instance") for row in instance_stats_rows}
    if r.returncode != 0:
        problems.append(f"stats --instance --json failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif instance_stats_names != {"manager", "ticket-manager"}:
        problems.append(f"stats --instance --json returned unexpected rows: {instance_stats_rows}")
    elif any(row.get("status") != "running" for row in instance_stats_rows):
        problems.append(f"stats --instance --json returned non-running rows: {instance_stats_rows}")

    r = subprocess.run(
        [str(binary), "stats", "--json", "--phase", "unknown", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        phase_stats_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"stats --phase unknown --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        phase_stats_rows = []
    phase_stats_instances = {row.get("instance") for row in phase_stats_rows}
    if r.returncode != 0 or "adhoc" not in phase_stats_instances:
        problems.append(f"stats --phase unknown --json missing adhoc: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif any(row.get("phase") != "unknown" for row in phase_stats_rows):
        problems.append(f"stats --phase unknown --json returned non-unknown rows: {phase_stats_rows}")

    r = subprocess.run(
        [str(binary), "stats", "--sort", "phase", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or "PHASE" not in r.stdout or "adhoc" not in r.stdout:
        problems.append(f"stats --sort phase table missing expected output: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "top", "--json", "--agent", "manager", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        top_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"top --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        top_rows = []
    top_instances = {row.get("instance") for row in top_rows}
    if r.returncode != 0 or not {"adhoc", "detached", "manager"}.issubset(top_instances):
        problems.append(f"top alias missing manager-agent rows: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif any(row.get("agent") != "manager" or row.get("status") != "running" for row in top_rows):
        problems.append(f"top alias returned unexpected rows: {top_rows}")

    r = subprocess.run(
        [
            str(binary), "stats",
            "--json",
            "--agent", "manager",
            "--status", "running",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    try:
        filtered_stats_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"filtered stats --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        filtered_stats_rows = []
    filtered_stats_instances = {row.get("instance") for row in filtered_stats_rows}
    if r.returncode != 0 or not {"adhoc", "manager"}.issubset(filtered_stats_instances):
        problems.append(f"filtered stats --json missing manager rows: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif "ticket-manager" in filtered_stats_instances:
        problems.append(f"filtered stats --json unexpectedly included ticket-manager: {filtered_stats_rows}")
    elif any(row.get("status") != "running" or row.get("agent") != "manager" for row in filtered_stats_rows):
        problems.append(f"filtered stats --json returned unexpected rows: {filtered_stats_rows}")

    r = subprocess.run(
        [
            str(binary), "stats",
            "--format", "{{.Instance}}:{{.Status}}:{{.Measured}}",
            "--agent", "manager",
            "--status", "running",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    formatted_stats_rows = {line.strip() for line in r.stdout.splitlines() if line.strip()}
    if r.returncode != 0 or "manager:running:true" not in formatted_stats_rows:
        problems.append(f"stats --format filtered output failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif any(line.startswith("ticket-manager:") for line in formatted_stats_rows):
        problems.append(f"stats --format unexpectedly included ticket-manager: stdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "monitor", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        monitor_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"monitor --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        monitor_body = {}
    monitor_health = monitor_body.get("health") or {}
    monitor_instances = monitor_body.get("instances") or []
    monitor_stats = monitor_body.get("stats") or []
    if r.returncode != 0 or not monitor_health.get("healthy") or monitor_body.get("stats_error"):
        problems.append(f"monitor --json unhealthy or missing stats: rc={r.returncode}\nbody={monitor_body}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif "adhoc" not in {row.get("instance") for row in monitor_instances}:
        problems.append(f"monitor --json missing adhoc instance: {monitor_body}")
    elif "adhoc" not in {row.get("instance") for row in monitor_stats}:
        problems.append(f"monitor --json missing adhoc stats: {monitor_body}")

    r = subprocess.run(
        [str(binary), "monitor", "--phase", "unknown", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        phase_monitor_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"monitor --phase unknown --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        phase_monitor_body = {}
    phase_monitor_instances = phase_monitor_body.get("instances") or []
    phase_monitor_stats = phase_monitor_body.get("stats") or []
    if r.returncode != 0 or phase_monitor_body.get("stats_error"):
        problems.append(f"monitor --phase unknown --json failed: rc={r.returncode}\nbody={phase_monitor_body}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif "adhoc" not in {row.get("instance") for row in phase_monitor_instances}:
        problems.append(f"monitor --phase unknown --json missing adhoc instance: {phase_monitor_body}")
    elif "adhoc" not in {row.get("instance") for row in phase_monitor_stats}:
        problems.append(f"monitor --phase unknown --json missing adhoc stats: {phase_monitor_body}")
    elif any(row.get("phase") != "unknown" for row in phase_monitor_instances):
        problems.append(f"monitor --phase unknown --json returned non-unknown instance rows: {phase_monitor_body}")

    r = subprocess.run(
        [
            str(binary), "monitor",
            "--format", "{{.Health.Healthy}}:{{len .Instances}}:{{len .Stats}}",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    monitor_format_parts = r.stdout.strip().split(":")
    if r.returncode != 0 or len(monitor_format_parts) != 3 or monitor_format_parts[0] != "true":
        problems.append(f"monitor --format failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    else:
        try:
            monitor_format_instance_count = int(monitor_format_parts[1])
            monitor_format_stats_count = int(monitor_format_parts[2])
        except ValueError:
            problems.append(f"monitor --format returned non-numeric counts: stdout={r.stdout}\nstderr={r.stderr}")
        else:
            if monitor_format_instance_count < 3 or monitor_format_stats_count < 3:
                problems.append(f"monitor --format missing running rows: stdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "monitor", "--events", "20", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        monitor_events_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"monitor --events --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        monitor_events_body = {}
    monitor_events = monitor_events_body.get("events") or []
    monitor_event_pairs = {(row.get("action"), row.get("instance")) for row in monitor_events if isinstance(row, dict)}
    if r.returncode != 0 or monitor_events_body.get("events_error"):
        problems.append(f"monitor --events --json failed: rc={r.returncode}\nbody={monitor_events_body}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif ("dispatch", "manager") not in monitor_event_pairs or ("dispatch", "ticket-manager") not in monitor_event_pairs:
        problems.append(f"monitor --events --json missing startup dispatches: {monitor_events_body}")

    r = subprocess.run(
        [str(binary), "monitor", "--summary", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        monitor_summary_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"monitor --summary --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        monitor_summary_body = {}
    monitor_summary = monitor_summary_body.get("summary") or {}
    if r.returncode != 0:
        problems.append(f"monitor --summary --json failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif not monitor_summary_body.get("healthy") or monitor_summary_body.get("issues"):
        problems.append(f"monitor --summary --json reported unhealthy fleet: {monitor_summary_body}")
    elif monitor_summary.get("running", 0) < 3:
        problems.append(f"monitor --summary --json missing running summary count: {monitor_summary_body}")
    elif sum((monitor_summary.get("phases") or {}).values()) < monitor_summary.get("total", 0):
        problems.append(f"monitor --summary --json missing phase aggregate: {monitor_summary_body}")

    r = subprocess.run(
        [str(binary), "monitor", "--summary", "--latest", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        monitor_summary_latest_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"monitor --summary --latest --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        monitor_summary_latest_body = {}
    monitor_summary_latest = monitor_summary_latest_body.get("summary") or {}
    monitor_summary_latest_instances = monitor_summary_latest_body.get("instances") or []
    if (
        r.returncode != 0
        or monitor_summary_latest.get("total") != 1
        or len(monitor_summary_latest_instances) != 1
    ):
        problems.append(
            "monitor --summary --latest --json did not scope to one latest row: "
            f"rc={r.returncode}\nbody={monitor_summary_latest_body}\nstdout={r.stdout}\nstderr={r.stderr}"
        )

    r = subprocess.run(
        [str(binary), "monitor", "--plan", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        monitor_plan_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"monitor --plan --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        monitor_plan_body = {}
    monitor_plan = monitor_plan_body.get("plan") or {}
    monitor_plan_rows = {
        row.get("instance"): row
        for row in monitor_plan.get("instances") or []
        if isinstance(row, dict)
    }
    monitor_plan_summary = monitor_plan.get("summary") or {}
    if r.returncode != 0:
        problems.append(f"monitor --plan --json failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif monitor_plan_summary.get("keep") != 2 or monitor_plan_summary.get("on_demand") != 4:
        problems.append(f"monitor --plan --json returned unexpected plan summary: {monitor_plan_body}")
    elif any((monitor_plan_rows.get(name) or {}).get("action") != "keep" for name in ("manager", "ticket-manager")):
        problems.append(f"monitor --plan --json missing keep actions: {monitor_plan_body}")
    elif any((monitor_plan_rows.get(name) or {}).get("action") != "on-demand" for name in ("reviewer", "worker")):
        problems.append(f"monitor --plan --json missing on-demand actions: {monitor_plan_body}")

    r = subprocess.run(
        [str(binary), "monitor", "--plan", "--action", "on_demand", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        monitor_action_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"monitor --plan --action on_demand --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        monitor_action_body = {}
    monitor_action_plan = monitor_action_body.get("plan") or {}
    monitor_action_rows = monitor_action_plan.get("instances") or []
    if r.returncode != 0:
        problems.append(f"monitor --plan --action on_demand --json failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif {row.get("instance") for row in monitor_action_rows} != {"feedback-triage", "harness-reviewer", "reviewer", "worker"}:
        problems.append(f"monitor --plan --action on_demand returned unexpected rows: {monitor_action_body}")
    elif any(row.get("action") != "on-demand" for row in monitor_action_rows):
        problems.append(f"monitor --plan --action on_demand returned unexpected action: {monitor_action_body}")

    r = subprocess.run(
        [
            str(binary), "monitor",
            "--json",
            "--agent", "manager",
            "--status", "running",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    try:
        filtered_monitor_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"filtered monitor --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        filtered_monitor_body = {}
    filtered_monitor_instances = filtered_monitor_body.get("instances") or []
    filtered_monitor_stats = filtered_monitor_body.get("stats") or []
    filtered_monitor_instance_names = {row.get("instance") for row in filtered_monitor_instances}
    filtered_monitor_stat_names = {row.get("instance") for row in filtered_monitor_stats}
    if r.returncode != 0 or not {"adhoc", "manager"}.issubset(filtered_monitor_instance_names):
        problems.append(f"filtered monitor --json missing manager instances: rc={r.returncode}\nbody={filtered_monitor_body}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif "ticket-manager" in filtered_monitor_instance_names:
        problems.append(f"filtered monitor --json unexpectedly included ticket-manager instance: {filtered_monitor_body}")
    elif not {"adhoc", "manager"}.issubset(filtered_monitor_stat_names):
        problems.append(f"filtered monitor --json missing manager stats: {filtered_monitor_body}")
    elif "ticket-manager" in filtered_monitor_stat_names:
        problems.append(f"filtered monitor --json unexpectedly included ticket-manager stats: {filtered_monitor_body}")


def _check_daemon_messaging_status_and_health(ctx: DaemonSmokeContext, problems: list[str]) -> None:
    binary = ctx.binary
    socket_dir = ctx.socket_dir
    env = ctx.env
    team_dir = ctx.team_dir
    sock = ctx.sock
    pid = ctx.pid

    r = subprocess.run(
        [
            str(binary), "send",
            "--from", "smoke",
            "--json",
            "--target", str(socket_dir),
            "adhoc", "hello from smoke",
        ],
        capture_output=True, text=True,
    )
    try:
        send_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"send --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        send_body = {}
    if r.returncode != 0 or not send_body.get("delivered") or send_body.get("to") != "adhoc":
        problems.append(f"send --json failed: rc={r.returncode}\nbody={send_body}\nstdout={r.stdout}\nstderr={r.stderr}")
    mailbox = team_dir / "daemon" / "adhoc" / "mailbox.jsonl"
    try:
        messages = [json.loads(line) for line in mailbox.read_text().splitlines() if line.strip()]
    except Exception as e:  # noqa: BLE001
        problems.append(f"send mailbox read failed: {e}\npath={mailbox}")
        messages = []
    if not any(m.get("from") == "smoke" and m.get("body") == "hello from smoke" for m in messages):
        problems.append(f"send did not append expected mailbox message: {messages}")

    r = subprocess.run(
        [
            str(binary), "send",
            "--from", "smoke-declared",
            "--allow-missing",
            "--json",
            "--target", str(socket_dir),
            "harness-reviewer", "queued for declared instance",
        ],
        capture_output=True, text=True,
    )
    try:
        declared_send_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"send declared stopped --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        declared_send_body = {}
    if (
        r.returncode != 0
        or not declared_send_body.get("delivered")
        or declared_send_body.get("to") != "harness-reviewer"
        or declared_send_body.get("note") != "declared but not running; queued for next spawn/resume"
        or "--allow-missing is deprecated" not in r.stderr
    ):
        problems.append(f"send declared stopped failed: rc={r.returncode}\nbody={declared_send_body}\nstdout={r.stdout}\nstderr={r.stderr}")
    declared_mailbox = team_dir / "daemon" / "harness-reviewer" / "mailbox.jsonl"
    try:
        declared_messages = [json.loads(line) for line in declared_mailbox.read_text().splitlines() if line.strip()]
    except Exception as e:  # noqa: BLE001
        problems.append(f"send declared mailbox read failed: {e}\npath={declared_mailbox}")
        declared_messages = []
    if not any(m.get("from") == "smoke-declared" and m.get("body") == "queued for declared instance" for m in declared_messages):
        problems.append(f"send declared stopped missing mailbox message: {declared_messages}")

    r = subprocess.run(
        [
            str(binary), "send",
            "--from", "smoke-typo",
            "--allow-missing",
            "--json",
            "--target", str(socket_dir),
            "manger", "typo should fail",
        ],
        capture_output=True, text=True,
    )
    if r.returncode == 0 or "did you mean \"manager\"?" not in r.stderr or "--allow-missing is deprecated" not in r.stderr:
        problems.append(f"send typo did not fail helpfully: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [
            str(binary), "send",
            "--from", "smoke-format",
            "--format", "{{.To}}:{{.From}}:{{.Delivered}}",
            "--target", str(socket_dir),
            "adhoc", "hello from formatted smoke",
        ],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or r.stdout.strip() != "adhoc:smoke-format:true":
        problems.append(f"send --format failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [
            str(binary), "send",
            "--agent", "manager",
            "--status", "running",
            "--from", "smoke-broadcast",
            "--json",
            "--target", str(socket_dir),
            "hello manager fleet",
        ],
        capture_output=True, text=True,
    )
    try:
        send_many_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"send --agent --status --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        send_many_body = []
    send_many_names = {row.get("to") for row in send_many_body if isinstance(row, dict)}
    if r.returncode != 0:
        problems.append(f"send --agent manager --status running failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif send_many_names != {"adhoc", "detached", "manager"}:
        problems.append(f"send --agent manager --status running targeted unexpected instances: {send_many_body}")
    for instance in ("adhoc", "detached", "manager"):
        mailbox = team_dir / "daemon" / instance / "mailbox.jsonl"
        try:
            messages = [json.loads(line) for line in mailbox.read_text().splitlines() if line.strip()]
        except Exception as e:  # noqa: BLE001
            problems.append(f"send broadcast mailbox read failed: {e}\npath={mailbox}")
            messages = []
        if not any(m.get("from") == "smoke-broadcast" and m.get("body") == "hello manager fleet" for m in messages):
            problems.append(f"send broadcast missing mailbox message for {instance}: {messages}")

    ticket_state = team_dir / "state" / "ticket-manager"
    ticket_state.mkdir(parents=True, exist_ok=True)
    (ticket_state / "status.toml").write_text(
        '[status]\nphase = "idle"\ndescription = "smoke idle"\n',
        encoding="utf-8",
    )

    r = subprocess.run(
        [
            str(binary), "send",
            "--phase", "idle",
            "--from", "smoke-phase",
            "--json",
            "--target", str(socket_dir),
            "hello idle fleet",
        ],
        capture_output=True, text=True,
    )
    try:
        send_phase_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"send --phase idle --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        send_phase_body = []
    send_phase_names = {row.get("to") for row in send_phase_body if isinstance(row, dict)}
    if r.returncode != 0:
        problems.append(f"send --phase idle failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif send_phase_names != {"ticket-manager"}:
        problems.append(f"send --phase idle targeted unexpected instances: {send_phase_body}")
    ticket_mailbox = team_dir / "daemon" / "ticket-manager" / "mailbox.jsonl"
    try:
        ticket_messages = [json.loads(line) for line in ticket_mailbox.read_text().splitlines() if line.strip()]
    except Exception as e:  # noqa: BLE001
        problems.append(f"send --phase mailbox read failed: {e}\npath={ticket_mailbox}")
        ticket_messages = []
    if not any(m.get("from") == "smoke-phase" and m.get("body") == "hello idle fleet" for m in ticket_messages):
        problems.append(f"send --phase idle missing ticket-manager mailbox message: {ticket_messages}")

    r = subprocess.run(
        [
            str(binary), "stop",
            "--phase", "idle",
            "--dry-run",
            "--json",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    try:
        dry_stop_phase_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"stop --phase idle --dry-run --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        dry_stop_phase_rows = []
    if r.returncode != 0 or len(dry_stop_phase_rows) != 1:
        problems.append(f"stop --phase idle --dry-run failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif (
        dry_stop_phase_rows[0].get("instance") != "ticket-manager"
        or dry_stop_phase_rows[0].get("action") != "stop"
        or dry_stop_phase_rows[0].get("status") != "running"
        or not dry_stop_phase_rows[0].get("dry_run")
    ):
        problems.append(f"stop --phase idle --dry-run returned unexpected row: {dry_stop_phase_rows}")

    r = subprocess.run(
        [str(binary), "status", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        status_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"status --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        status_body = {}
    status_rows = status_body.get("instances") or []
    status_adhoc = [row for row in status_rows if row.get("instance") == "adhoc"]
    if r.returncode != 0 or not (status_body.get("daemon") or {}).get("running"):
        problems.append(f"status --json daemon not running: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif not status_adhoc or status_adhoc[0].get("status") != "running":
        problems.append(f"status --json missing running adhoc row: {status_body}")

    r = subprocess.run(
        [str(binary), "status", "--summary", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        status_summary_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"status --summary --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        status_summary_body = {}
    status_summary = status_summary_body.get("summary") or {}
    if r.returncode != 0:
        problems.append(f"status --summary --json failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif not status_summary_body.get("healthy") or status_summary_body.get("issues"):
        problems.append(f"status --summary --json reported unhealthy fleet: {status_summary_body}")
    elif status_summary.get("running", 0) < 3:
        problems.append(f"status --summary --json missing running summary count: {status_summary_body}")
    elif sum((status_summary.get("phases") or {}).values()) < status_summary.get("total", 0):
        problems.append(f"status --summary --json missing phase aggregate: {status_summary_body}")

    r = subprocess.run(
        [str(binary), "status", "--summary", "--latest", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        status_summary_latest_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"status --summary --latest --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        status_summary_latest_body = {}
    status_summary_latest = status_summary_latest_body.get("summary") or {}
    status_summary_latest_instances = status_summary_latest_body.get("instances") or []
    if (
        r.returncode != 0
        or status_summary_latest.get("total") != 1
        or len(status_summary_latest_instances) != 1
    ):
        problems.append(
            "status --summary --latest --json did not scope to one latest row: "
            f"rc={r.returncode}\nbody={status_summary_latest_body}\nstdout={r.stdout}\nstderr={r.stderr}"
        )

    r = subprocess.run(
        [str(binary), "status", "--json", "--agent", "manager", "--status", "running", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        filtered_status_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"filtered status --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        filtered_status_body = {}
    filtered_status_rows = filtered_status_body.get("instances") or []
    filtered_status_instances = {row.get("instance") for row in filtered_status_rows}
    if r.returncode != 0 or not (filtered_status_body.get("daemon") or {}).get("running"):
        problems.append(f"filtered status --json daemon not running: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif not {"adhoc", "manager"}.issubset(filtered_status_instances):
        problems.append(f"filtered status --json missing manager rows: {filtered_status_body}")
    elif "ticket-manager" in filtered_status_instances:
        problems.append(f"filtered status --json unexpectedly included ticket-manager: {filtered_status_body}")
    elif any(row.get("status") != "running" or row.get("agent") != "manager" for row in filtered_status_rows):
        problems.append(f"filtered status --json returned unexpected rows: {filtered_status_body}")

    r = subprocess.run(
        [str(binary), "status", "--json", "--instance", "manager", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        instance_status_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"status --instance --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        instance_status_body = {}
    instance_status_rows = instance_status_body.get("instances") or []
    if r.returncode != 0 or not (instance_status_body.get("daemon") or {}).get("running"):
        problems.append(f"status --instance --json daemon not running: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif [row.get("instance") for row in instance_status_rows] != ["manager"]:
        problems.append(f"status --instance --json returned unexpected rows: {instance_status_body}")
    elif instance_status_rows[0].get("status") != "running":
        problems.append(f"status --instance --json manager row was not running: {instance_status_body}")

    r = subprocess.run(
        [str(binary), "health", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        health_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"health --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        health_body = {}
    if r.returncode != 0 or not health_body.get("healthy") or health_body.get("issues"):
        problems.append(f"health --json was not healthy: rc={r.returncode}\nbody={health_body}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "health", "--agent", "manager", "--status", "running", "--phase", "unknown", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        health_agent_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"filtered health --agent --status --phase --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        health_agent_body = {}
    health_agent_instances = {row.get("instance") for row in health_agent_body.get("instances") or []}
    if r.returncode != 0 or not health_agent_body.get("healthy") or health_agent_body.get("issues"):
        problems.append(f"filtered health --agent manager --status running --phase unknown --json was not healthy: rc={r.returncode}\nbody={health_agent_body}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif not {"adhoc", "detached", "manager"}.issubset(health_agent_instances):
        problems.append(f"filtered health --agent manager --status running --phase unknown --json missing manager-agent rows: {health_agent_body}")
    elif "ticket-manager" in health_agent_instances:
        problems.append(f"filtered health --agent manager --status running --phase unknown --json unexpectedly included ticket-manager: {health_agent_body}")

    r = subprocess.run(
        [str(binary), "health", "--quiet", "--agent", "manager", "--status", "running", "--phase", "unknown", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or r.stdout or r.stderr:
        problems.append(f"health --quiet filtered probe failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "health", "--instance", "manager", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        health_instance_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"health --instance --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        health_instance_body = {}
    health_instance_rows = health_instance_body.get("instances") or []
    health_instance_declared = health_instance_body.get("declared") or {}
    if r.returncode != 0 or not health_instance_body.get("healthy") or health_instance_body.get("issues"):
        problems.append(f"health --instance manager --json was not healthy: rc={r.returncode}\nbody={health_instance_body}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif [row.get("instance") for row in health_instance_rows] != ["manager"]:
        problems.append(f"health --instance manager --json returned unexpected rows: {health_instance_body}")
    elif health_instance_declared.get("persistent") != 1 or health_instance_declared.get("running") != 1:
        problems.append(f"health --instance manager --json returned unexpected declared counts: {health_instance_body}")

    r = subprocess.run(
        [str(binary), "monitor", "--json", "--plan", "--instance", "manager", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        monitor_instance_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"monitor --instance --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        monitor_instance_body = {}
    monitor_instance_rows = monitor_instance_body.get("instances") or []
    monitor_instance_stats = monitor_instance_body.get("stats") or []
    monitor_instance_plan = (monitor_instance_body.get("plan") or {}).get("instances") or []
    monitor_instance_health = monitor_instance_body.get("health") or {}
    if r.returncode != 0:
        problems.append(f"monitor --instance manager --json failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif [row.get("instance") for row in monitor_instance_rows] != ["manager"]:
        problems.append(f"monitor --instance manager returned unexpected instance rows: {monitor_instance_body}")
    elif [row.get("instance") for row in monitor_instance_stats] != ["manager"]:
        problems.append(f"monitor --instance manager returned unexpected stats rows: {monitor_instance_body}")
    elif [row.get("instance") for row in monitor_instance_plan] != ["manager"]:
        problems.append(f"monitor --instance manager returned unexpected plan rows: {monitor_instance_body}")
    elif (monitor_instance_health.get("summary") or {}).get("total") != 1:
        problems.append(f"monitor --instance manager returned unfiltered health summary: {monitor_instance_body}")

    r = subprocess.run(
        [str(binary), "health", "--wait", "--timeout", "5s", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        wait_health_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"health --wait --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        wait_health_body = {}
    if r.returncode != 0 or not wait_health_body.get("healthy") or wait_health_body.get("issues"):
        problems.append(f"health --wait --json was not healthy: rc={r.returncode}\nbody={wait_health_body}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "health", "--format", "{{.Healthy}}:{{.Daemon.Running}}:{{.Summary.Running}}", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    formatted_health_rows = {line.strip() for line in r.stdout.splitlines() if line.strip()}
    if r.returncode != 0 or not any(line.startswith("true:true:") for line in formatted_health_rows):
        problems.append(f"health --format failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [
            str(binary), "start",
            "--wait",
            "--timeout", "5s",
            "--format", "{{.Instance}}:{{.Action}}:{{.Status}}",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    formatted_start_wait_rows = {line.strip() for line in r.stdout.splitlines() if line.strip()}
    if r.returncode != 0:
        problems.append(f"start --wait --format failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif "manager:skip:running" not in formatted_start_wait_rows:
        problems.append(f"start --wait --format missing manager skip row: stdout={r.stdout}\nstderr={r.stderr}")
    elif "ticket-manager:skip:running" not in formatted_start_wait_rows:
        problems.append(f"start --wait --format missing ticket-manager skip row: stdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [
            str(binary), "sync",
            "--wait",
            "--timeout", "5s",
            "--format", "{{.Instance}}:{{.Action}}:{{.Status}}",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    formatted_sync_wait_rows = {line.strip() for line in r.stdout.splitlines() if line.strip()}
    if r.returncode != 0:
        problems.append(f"sync --wait --format failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif "manager:skip:running" not in formatted_sync_wait_rows:
        problems.append(f"sync --wait --format missing manager skip row: stdout={r.stdout}\nstderr={r.stderr}")
    elif "ticket-manager:skip:running" not in formatted_sync_wait_rows:
        problems.append(f"sync --wait --format missing ticket-manager skip row: stdout={r.stdout}\nstderr={r.stderr}")


def _check_daemon_watch_commands(ctx: DaemonSmokeContext, problems: list[str]) -> None:
    binary = ctx.binary
    socket_dir = ctx.socket_dir
    env = ctx.env
    team_dir = ctx.team_dir
    sock = ctx.sock
    pid = ctx.pid

    proc = subprocess.Popen(
        [str(binary), "status", "--watch", "--json", "--interval", "50ms", "--target", str(socket_dir)],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
    )
    time.sleep(0.2)
    proc.send_signal(signal.SIGINT)
    try:
        watch_stdout, watch_stderr = proc.communicate(timeout=2)
    except subprocess.TimeoutExpired:
        proc.kill()
        watch_stdout, watch_stderr = proc.communicate()
        problems.append(f"status --watch --json did not terminate\nstdout={watch_stdout}\nstderr={watch_stderr}")
    else:
        first = next((line for line in watch_stdout.splitlines() if line.strip()), "")
        try:
            watch_status = json.loads(first)
        except Exception as e:  # noqa: BLE001
            problems.append(
                f"status --watch --json returned invalid JSON: {e}\nstdout={watch_stdout}\nstderr={watch_stderr}"
            )
            watch_status = {}
        if proc.returncode != 0:
            problems.append(
                f"status --watch --json exited non-zero: rc={proc.returncode}\nstdout={watch_stdout}\nstderr={watch_stderr}"
            )
        elif not (watch_status.get("daemon") or {}).get("running"):
            problems.append(f"status --watch --json missing running daemon: {watch_status}")

    proc = subprocess.Popen(
        [str(binary), "watch", "--summary", "--json", "--interval", "50ms", "--target", str(socket_dir)],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
    )
    time.sleep(0.2)
    proc.send_signal(signal.SIGINT)
    try:
        watch_summary_stdout, watch_summary_stderr = proc.communicate(timeout=2)
    except subprocess.TimeoutExpired:
        proc.kill()
        watch_summary_stdout, watch_summary_stderr = proc.communicate()
        problems.append(
            f"watch --summary --json did not terminate\nstdout={watch_summary_stdout}\nstderr={watch_summary_stderr}"
        )
    else:
        first = next((line for line in watch_summary_stdout.splitlines() if line.strip()), "")
        try:
            watch_summary = json.loads(first)
        except Exception as e:  # noqa: BLE001
            problems.append(
                f"watch --summary --json returned invalid JSON: {e}\nstdout={watch_summary_stdout}\nstderr={watch_summary_stderr}"
            )
            watch_summary = {}
        if proc.returncode != 0:
            problems.append(
                f"watch --summary --json exited non-zero: rc={proc.returncode}\nstdout={watch_summary_stdout}\nstderr={watch_summary_stderr}"
            )
        elif not watch_summary.get("healthy") or not (watch_summary.get("daemon") or {}).get("running"):
            problems.append(f"watch --summary --json missing healthy running daemon: {watch_summary}")

    proc = subprocess.Popen(
        [str(binary), "watch", "--events", "20", "--json", "--interval", "50ms", "--target", str(socket_dir)],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
    )
    time.sleep(0.2)
    proc.send_signal(signal.SIGINT)
    try:
        watch_events_stdout, watch_events_stderr = proc.communicate(timeout=2)
    except subprocess.TimeoutExpired:
        proc.kill()
        watch_events_stdout, watch_events_stderr = proc.communicate()
        problems.append(
            f"watch --events --json did not terminate\nstdout={watch_events_stdout}\nstderr={watch_events_stderr}"
        )
    else:
        first = next((line for line in watch_events_stdout.splitlines() if line.strip()), "")
        try:
            watch_events_body = json.loads(first)
        except Exception as e:  # noqa: BLE001
            problems.append(
                f"watch --events --json returned invalid JSON: {e}\nstdout={watch_events_stdout}\nstderr={watch_events_stderr}"
            )
            watch_events_body = {}
        watch_events = watch_events_body.get("events") or []
        watch_event_pairs = {(row.get("action"), row.get("instance")) for row in watch_events if isinstance(row, dict)}
        if proc.returncode != 0:
            problems.append(
                f"watch --events --json exited non-zero: rc={proc.returncode}\nstdout={watch_events_stdout}\nstderr={watch_events_stderr}"
            )
        elif ("dispatch", "manager") not in watch_event_pairs or ("dispatch", "ticket-manager") not in watch_event_pairs:
            problems.append(f"watch --events --json missing startup dispatches: {watch_events_body}")

    proc = subprocess.Popen(
        [str(binary), "watch", "--phase", "unknown", "--json", "--interval", "50ms", "--target", str(socket_dir)],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
    )
    time.sleep(0.2)
    proc.send_signal(signal.SIGINT)
    try:
        watch_phase_stdout, watch_phase_stderr = proc.communicate(timeout=2)
    except subprocess.TimeoutExpired:
        proc.kill()
        watch_phase_stdout, watch_phase_stderr = proc.communicate()
        problems.append(
            f"watch --phase unknown --json did not terminate\nstdout={watch_phase_stdout}\nstderr={watch_phase_stderr}"
        )
    else:
        first = next((line for line in watch_phase_stdout.splitlines() if line.strip()), "")
        try:
            watch_phase_body = json.loads(first)
        except Exception as e:  # noqa: BLE001
            problems.append(
                f"watch --phase unknown --json returned invalid JSON: {e}\nstdout={watch_phase_stdout}\nstderr={watch_phase_stderr}"
            )
            watch_phase_body = {}
        watch_phase_instances = watch_phase_body.get("instances") or []
        watch_phase_stats = watch_phase_body.get("stats") or []
        if proc.returncode != 0:
            problems.append(
                f"watch --phase unknown --json exited non-zero: rc={proc.returncode}\nstdout={watch_phase_stdout}\nstderr={watch_phase_stderr}"
            )
        elif "adhoc" not in {row.get("instance") for row in watch_phase_instances}:
            problems.append(f"watch --phase unknown --json missing adhoc instance: {watch_phase_body}")
        elif "adhoc" not in {row.get("instance") for row in watch_phase_stats}:
            problems.append(f"watch --phase unknown --json missing adhoc stats: {watch_phase_body}")
        elif any(row.get("phase") != "unknown" for row in watch_phase_instances):
            problems.append(f"watch --phase unknown --json returned non-unknown instance rows: {watch_phase_body}")

    clear_sequence = "\x1b[H\x1b[2J"
    proc = subprocess.Popen(
        [
            str(binary), "watch",
            "--format", "{{.Health.Healthy}}:{{len .Instances}}:{{len .Stats}}",
            "--interval", "50ms",
            "--target", str(socket_dir),
        ],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
    )
    time.sleep(0.2)
    proc.send_signal(signal.SIGINT)
    try:
        watch_format_stdout, watch_format_stderr = proc.communicate(timeout=2)
    except subprocess.TimeoutExpired:
        proc.kill()
        watch_format_stdout, watch_format_stderr = proc.communicate()
        problems.append(
            f"watch --format did not terminate\nstdout={watch_format_stdout}\nstderr={watch_format_stderr}"
        )
    else:
        first = next((line.strip() for line in watch_format_stdout.splitlines() if line.strip()), "")
        parts = first.split(":")
        if proc.returncode != 0:
            problems.append(
                f"watch --format exited non-zero: rc={proc.returncode}\nstdout={watch_format_stdout}\nstderr={watch_format_stderr}"
            )
        elif clear_sequence in watch_format_stdout:
            problems.append(
                f"watch --format unexpectedly emitted clear sequence\nstdout={watch_format_stdout}\nstderr={watch_format_stderr}"
            )
        elif len(parts) != 3 or parts[0] != "true":
            problems.append(f"watch --format returned unexpected first row: {first!r}\nstdout={watch_format_stdout}")
        else:
            try:
                instance_count = int(parts[1])
                stats_count = int(parts[2])
            except ValueError:
                problems.append(f"watch --format returned non-numeric counts: {first!r}\nstdout={watch_format_stdout}")
            else:
                if instance_count < 2 or stats_count < 2:
                    problems.append(f"watch --format returned too few live rows: {first!r}\nstdout={watch_format_stdout}")

    proc = subprocess.Popen(
        [str(binary), "health", "--watch", "--no-clear", "--interval", "50ms", "--target", str(socket_dir)],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
    )
    time.sleep(0.2)
    proc.send_signal(signal.SIGINT)
    try:
        health_no_clear_stdout, health_no_clear_stderr = proc.communicate(timeout=2)
    except subprocess.TimeoutExpired:
        proc.kill()
        health_no_clear_stdout, health_no_clear_stderr = proc.communicate()
        problems.append(
            f"health --watch --no-clear text did not terminate\nstdout={health_no_clear_stdout}\nstderr={health_no_clear_stderr}"
        )
    else:
        if proc.returncode != 0:
            problems.append(
                f"health --watch --no-clear text exited non-zero: rc={proc.returncode}\nstdout={health_no_clear_stdout}\nstderr={health_no_clear_stderr}"
            )
        elif clear_sequence in health_no_clear_stdout:
            problems.append(
                f"health --watch --no-clear text unexpectedly emitted clear sequence\nstdout={health_no_clear_stdout}\nstderr={health_no_clear_stderr}"
            )
        elif (
            "health: healthy" not in health_no_clear_stdout
            or "daemon: running" not in health_no_clear_stdout
            or "phases:" not in health_no_clear_stdout
        ):
            problems.append(
                f"health --watch --no-clear text missing health content\nstdout={health_no_clear_stdout}\nstderr={health_no_clear_stderr}"
            )

    proc = subprocess.Popen(
        [str(binary), "watch", "--interval", "50ms", "--target", str(socket_dir)],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
    )
    time.sleep(0.2)
    proc.send_signal(signal.SIGINT)
    try:
        watch_text_stdout, watch_text_stderr = proc.communicate(timeout=2)
    except subprocess.TimeoutExpired:
        proc.kill()
        watch_text_stdout, watch_text_stderr = proc.communicate()
        problems.append(f"watch text did not terminate\nstdout={watch_text_stdout}\nstderr={watch_text_stderr}")
    else:
        if proc.returncode != 0:
            problems.append(f"watch text exited non-zero: rc={proc.returncode}\nstdout={watch_text_stdout}\nstderr={watch_text_stderr}")
        elif clear_sequence not in watch_text_stdout:
            problems.append(f"watch text did not emit clear sequence\nstdout={watch_text_stdout}\nstderr={watch_text_stderr}")
        elif "health: healthy" not in watch_text_stdout or "instances:" not in watch_text_stdout:
            problems.append(f"watch text missing monitor content\nstdout={watch_text_stdout}\nstderr={watch_text_stderr}")

    proc = subprocess.Popen(
        [str(binary), "watch", "--no-clear", "--interval", "50ms", "--target", str(socket_dir)],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
    )
    time.sleep(0.2)
    proc.send_signal(signal.SIGINT)
    try:
        watch_no_clear_stdout, watch_no_clear_stderr = proc.communicate(timeout=2)
    except subprocess.TimeoutExpired:
        proc.kill()
        watch_no_clear_stdout, watch_no_clear_stderr = proc.communicate()
        problems.append(
            f"watch --no-clear text did not terminate\nstdout={watch_no_clear_stdout}\nstderr={watch_no_clear_stderr}"
        )
    else:
        if proc.returncode != 0:
            problems.append(
                f"watch --no-clear text exited non-zero: rc={proc.returncode}\nstdout={watch_no_clear_stdout}\nstderr={watch_no_clear_stderr}"
            )
        elif clear_sequence in watch_no_clear_stdout:
            problems.append(
                f"watch --no-clear text unexpectedly emitted clear sequence\nstdout={watch_no_clear_stdout}\nstderr={watch_no_clear_stderr}"
            )
        elif "health: healthy" not in watch_no_clear_stdout or "instances:" not in watch_no_clear_stdout:
            problems.append(
                f"watch --no-clear text missing monitor content\nstdout={watch_no_clear_stdout}\nstderr={watch_no_clear_stderr}"
            )


def _check_daemon_inspect_kill_attach_and_logs(ctx: DaemonSmokeContext, problems: list[str]) -> None:
    binary = ctx.binary
    socket_dir = ctx.socket_dir
    env = ctx.env
    team_dir = ctx.team_dir
    sock = ctx.sock
    pid = ctx.pid

    r = subprocess.run(
        [str(binary), "inspect", "adhoc", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0:
        problems.append(f"inspect adhoc failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    for needle in ("runtime:", "lifecycle:   running", "pid:", "session_id:", "log:"):
        if needle not in r.stdout:
            problems.append(f"inspect adhoc missing {needle!r}: {r.stdout!r}")

    r = subprocess.run(
        [str(binary), "inspect", "adhoc", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        inspect_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"inspect --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        inspect_body = {}
    runtime = inspect_body.get("runtime") or {}
    if r.returncode != 0 or inspect_body.get("instance") != "adhoc":
        problems.append(f"inspect --json adhoc failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif runtime.get("lifecycle") != "running" or not runtime.get("pid") or not runtime.get("session_id"):
        problems.append(f"inspect --json adhoc missing runtime fields: {inspect_body}")

    r = subprocess.run(
        [str(binary), "inspect", "adhoc", "--format", "{{.Instance}}:{{if .Runtime}}{{.Runtime.Agent}}:{{.Runtime.Lifecycle}}{{end}}", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or r.stdout.strip() != "adhoc:manager:running":
        problems.append(f"inspect --format adhoc failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "inspect", "--all", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        inspect_all = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"inspect --all --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        inspect_all = []
    inspect_all_by_name = {row.get("instance"): row for row in inspect_all if isinstance(row, dict)}
    if r.returncode != 0 or not {"adhoc", "manager", "ticket-manager"}.issubset(inspect_all_by_name):
        problems.append(f"inspect --all --json missing rows: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif (inspect_all_by_name["adhoc"].get("runtime") or {}).get("lifecycle") != "running":
        problems.append(f"inspect --all --json adhoc row missing running runtime: {inspect_all_by_name['adhoc']}")

    r = subprocess.run(
        [str(binary), "inspect", "--agent", "manager", "--status", "running", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        inspect_filtered = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"inspect filtered --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        inspect_filtered = []
    inspect_filtered_by_name = {row.get("instance"): row for row in inspect_filtered if isinstance(row, dict)}
    if r.returncode != 0 or not {"adhoc", "detached", "manager"}.issubset(inspect_filtered_by_name):
        problems.append(f"inspect filtered --json missing manager rows: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif "ticket-manager" in inspect_filtered_by_name:
        problems.append(f"inspect filtered --json unexpectedly included ticket-manager: {inspect_filtered}")
    elif any((row.get("runtime") or {}).get("agent") != "manager" for row in inspect_filtered_by_name.values()):
        problems.append(f"inspect filtered --json returned non-manager runtime: {inspect_filtered}")

    r = subprocess.run(
        [str(binary), "kill", "adhoc", "--dry-run", "--json", "--timeout", "1s", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        dry_kill_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"kill --dry-run --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        dry_kill_rows = []
    if r.returncode != 0 or len(dry_kill_rows) != 1:
        problems.append(f"kill --dry-run adhoc failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif dry_kill_rows[0].get("instance") != "adhoc" or dry_kill_rows[0].get("action") != "kill":
        problems.append(f"kill --dry-run returned unexpected row: {dry_kill_rows}")
    elif dry_kill_rows[0].get("status") != "running" or not dry_kill_rows[0].get("dry_run"):
        problems.append(f"kill --dry-run missing running dry-run row: {dry_kill_rows}")

    r = subprocess.run(
        [
            str(binary), "kill", "adhoc",
            "--wait",
            "--wait-timeout", "5s",
            "--json",
            "--timeout", "1s",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    try:
        kill_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"kill --wait --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        kill_rows = []
    if r.returncode != 0 or not kill_rows:
        problems.append(f"kill --wait adhoc failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif kill_rows[0].get("action") != "kill" or kill_rows[0].get("instance") != "adhoc" or kill_rows[0].get("status") != "stopped":
        problems.append(f"kill --wait --json returned unexpected row: {kill_rows[0]}")
    elif kill_rows[0].get("wait_status") != "stopped":
        problems.append(f"kill --wait --json missing stopped wait_status: {kill_rows[0]}")

    r = subprocess.run(
        [str(binary), "wait", "adhoc", "--timeout", "5s", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        wait_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"wait --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        wait_rows = []
    if r.returncode != 0 or not wait_rows:
        problems.append(f"wait adhoc failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif wait_rows[0].get("instance") != "adhoc" or wait_rows[0].get("status") != "stopped":
        problems.append(f"wait adhoc returned unexpected row: {wait_rows[0]}")

    r = subprocess.run(
        [
            str(binary), "start",
            "--all",
            "--status", "stopped",
            "--json",
            "--ready-timeout", "5s",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True, env=env,
    )
    try:
        start_all_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"start --all --status stopped --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        start_all_rows = []
    start_all_by_name = {row.get("instance"): row for row in start_all_rows}
    if r.returncode != 0:
        problems.append(f"start --all --status stopped failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif (start_all_by_name.get("adhoc") or {}).get("action") != "resume":
        problems.append(f"start --all --status stopped --json did not resume adhoc: {start_all_rows}")
    elif set(start_all_by_name) != {"adhoc"}:
        problems.append(f"start --all --status stopped --json targeted unexpected instances: {start_all_rows}")

    r = subprocess.run(
        [
            str(binary),
            "restart",
            "--all",
            "--status",
            "running",
            "--json",
            "--ready-timeout",
            "5s",
            "--timeout",
            "5s",
            "--wait",
            "--wait-timeout",
            "5s",
            "--target",
            str(socket_dir),
        ],
        capture_output=True, text=True, env=env,
    )
    try:
        restart_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"restart --all --status running --wait --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        restart_body = {}
    restart_rows = restart_body.get("actions") or []
    restart_health = restart_body.get("health") or {}
    if not isinstance(restart_rows, list):
        restart_rows = []
    if not isinstance(restart_health, dict):
        restart_health = {}
    restart_by_name = {row.get("instance"): row for row in restart_rows}
    if r.returncode != 0:
        problems.append(f"restart --all --status running --wait failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif not restart_health.get("healthy"):
        problems.append(f"restart --all --wait --json did not report healthy fleet: {restart_body}")
    elif any((restart_by_name.get(name) or {}).get("action") != "restart" for name in ("adhoc", "manager", "ticket-manager")):
        problems.append(f"restart --all --json missing restart actions: {restart_rows}")
    elif any((restart_by_name.get(name) or {}).get("status") != "running" for name in ("adhoc", "manager", "ticket-manager")):
        problems.append(f"restart --all --json missing running statuses: {restart_rows}")

    last_logs = subprocess.CompletedProcess([], 1, "", "")
    for _ in range(20):
        last_logs = subprocess.run(
            [str(binary), "logs", "manager", "--tail", "5", "--target", str(socket_dir)],
            capture_output=True, text=True,
        )
        if last_logs.returncode == 0 and last_logs.stdout.strip():
            break
        time.sleep(0.1)
    else:
        problems.append(
            "logs manager failed: "
            f"rc={last_logs.returncode}\nstdout={last_logs.stdout}\nstderr={last_logs.stderr}"
        )

    r = subprocess.run(
        [str(binary), "attach", "manager", "--no-follow", "--tail", "all", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or "fake claude invoked:" not in r.stdout:
        problems.append(f"attach manager --no-follow --tail all failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "attach", "--last", "1", "--no-follow", "--tail", "all", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or "fake claude invoked:" not in r.stdout:
        problems.append(f"attach --last 1 --no-follow --tail all failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [
            str(binary), "attach",
            "--agent", "manager",
            "--status", "running",
            "--phase", "unknown",
            "--no-follow",
            "--tail", "all",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or "fake claude invoked:" not in r.stdout:
        problems.append(f"attach --agent manager --status running --phase unknown --no-follow failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    proc = subprocess.Popen(
        [str(binary), "start", "manager", "--attach", "--tail", "all", "--target", str(socket_dir)],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
    )
    time.sleep(0.2)
    proc.send_signal(signal.SIGINT)
    try:
        start_attach_stdout, start_attach_stderr = proc.communicate(timeout=2)
    except subprocess.TimeoutExpired:
        proc.kill()
        start_attach_stdout, start_attach_stderr = proc.communicate()
        problems.append(
            f"start --attach did not terminate\nstdout={start_attach_stdout}\nstderr={start_attach_stderr}"
        )
    else:
        if proc.returncode != 0:
            problems.append(
                f"start --attach exited non-zero: rc={proc.returncode}\nstdout={start_attach_stdout}\nstderr={start_attach_stderr}"
            )
        elif "attaching to manager" not in start_attach_stdout or "fake claude invoked:" not in start_attach_stdout:
            problems.append(f"start --attach missing followed manager log: stdout={start_attach_stdout}\nstderr={start_attach_stderr}")

    proc = subprocess.Popen(
        [str(binary), "restart", "manager", "--attach", "--tail", "all", "--timeout", "5s", "--target", str(socket_dir)],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, env=env,
    )
    time.sleep(0.2)
    proc.send_signal(signal.SIGINT)
    try:
        restart_attach_stdout, restart_attach_stderr = proc.communicate(timeout=2)
    except subprocess.TimeoutExpired:
        proc.kill()
        restart_attach_stdout, restart_attach_stderr = proc.communicate()
        problems.append(
            f"restart --attach did not terminate\nstdout={restart_attach_stdout}\nstderr={restart_attach_stderr}"
        )
    else:
        if proc.returncode != 0:
            problems.append(
                f"restart --attach exited non-zero: rc={proc.returncode}\nstdout={restart_attach_stdout}\nstderr={restart_attach_stderr}"
            )
        elif "attaching to manager" not in restart_attach_stdout or "fake claude invoked:" not in restart_attach_stdout:
            problems.append(f"restart --attach missing followed manager log: stdout={restart_attach_stdout}\nstderr={restart_attach_stderr}")

    r = subprocess.run(
        [str(binary), "logs", "--all", "--tail", "1", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or "manager" not in r.stdout or " | fake claude invoked:" not in r.stdout:
        problems.append(f"logs --all failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "logs", "--all", "--no-prefix", "--tail", "1", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or "fake claude invoked:" not in r.stdout or " | fake claude invoked:" in r.stdout:
        problems.append(f"logs --all --no-prefix failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "logs", "--agent", "manager", "--status", "running", "--phase", "unknown", "--tail", "1", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0:
        problems.append(f"logs --agent manager --phase unknown failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif "adhoc" not in r.stdout or "manager" not in r.stdout or " | fake claude invoked:" not in r.stdout:
        problems.append(f"logs --agent manager --phase unknown missing manager-agent logs: stdout={r.stdout}\nstderr={r.stderr}")
    elif "ticket-manager" in r.stdout:
        problems.append(f"logs --agent manager --phase unknown unexpectedly included ticket-manager: stdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "logs", "--list", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        log_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"logs --list --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        log_rows = []
    log_rows_by_name = {row.get("instance"): row for row in log_rows}
    if r.returncode != 0:
        problems.append(f"logs --list --json failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif not {"adhoc", "manager", "ticket-manager"}.issubset(log_rows_by_name):
        problems.append(f"logs --list --json missing rows: {log_rows}")
    elif any(not (log_rows_by_name.get(name) or {}).get("exists") for name in ("adhoc", "manager", "ticket-manager")):
        problems.append(f"logs --list --json missing existing log files: {log_rows}")
    elif any(not (log_rows_by_name.get(name) or {}).get("log_path") for name in ("adhoc", "manager", "ticket-manager")):
        problems.append(f"logs --list --json missing log paths: {log_rows}")

    r = subprocess.run(
        [str(binary), "logs", "--list", "--format", "{{.Instance}}:{{.Agent}}:{{.Status}}:{{.Exists}}", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    formatted_log_rows = {line.strip() for line in r.stdout.splitlines() if line.strip()}
    if r.returncode != 0 or "manager:manager:running:true" not in formatted_log_rows:
        problems.append(f"logs --list --format missing manager row: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "logs", "--list", "--json", "--status", "running", "--agent", "manager", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        filtered_log_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"filtered logs --list --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        filtered_log_rows = []
    filtered_log_names = {row.get("instance") for row in filtered_log_rows}
    if r.returncode != 0 or not {"adhoc", "manager"}.issubset(filtered_log_names):
        problems.append(f"filtered logs --list --json missing manager logs: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif any(row.get("status") != "running" or row.get("agent") != "manager" for row in filtered_log_rows):
        problems.append(f"filtered logs --list --json returned unexpected rows: {filtered_log_rows}")

    r = subprocess.run(
        [
            str(binary), "logs",
            "--list",
            "--json",
            "--status", "running,stopped",
            "--agent", "manager,ticket-manager",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    try:
        comma_filtered_log_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"comma-filtered logs --list --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        comma_filtered_log_rows = []
    comma_filtered_log_names = {row.get("instance") for row in comma_filtered_log_rows}
    if r.returncode != 0:
        problems.append(f"comma-filtered logs --list --json failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif not {"adhoc", "detached", "manager", "ticket-manager"}.issubset(comma_filtered_log_names):
        problems.append(f"comma-filtered logs --list --json missing expected rows: {comma_filtered_log_rows}")
    elif "worker" in comma_filtered_log_names:
        problems.append(f"comma-filtered logs --list --json unexpectedly included worker: {comma_filtered_log_rows}")

    r = subprocess.run(
        [str(binary), "restart", "manager", "--ready-timeout", "5s", "--target", str(socket_dir)],
        capture_output=True, text=True, env=env,
    )
    if r.returncode != 0 or "restart manager" not in r.stdout:
        problems.append(f"restart manager failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [
            str(binary),
            "restart",
            "--agent",
            "manager,ticket-manager",
            "--status",
            "running",
            "--json",
            "--ready-timeout",
            "5s",
            "--timeout",
            "5s",
            "--wait",
            "--wait-timeout",
            "5s",
            "--target",
            str(socket_dir),
        ],
        capture_output=True, text=True, env=env,
    )
    try:
        restart_agent_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"restart comma --agent --status running --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        restart_agent_body = {}
    restart_agent_rows = restart_agent_body.get("actions") or []
    restart_agent_health = restart_agent_body.get("health") or {}
    restart_agent_by_name = {row.get("instance"): row for row in restart_agent_rows if isinstance(row, dict)}
    if r.returncode != 0:
        problems.append(f"restart comma --agent --status running failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif not restart_agent_health.get("healthy"):
        problems.append(f"restart comma --agent did not report healthy fleet: {restart_agent_body}")
    elif any((restart_agent_by_name.get(name) or {}).get("action") != "restart" for name in ("adhoc", "manager", "ticket-manager")):
        problems.append(f"restart comma --agent missing restart actions: {restart_agent_rows}")
    elif "worker" in restart_agent_by_name:
        problems.append(f"restart comma --agent unexpectedly targeted worker: {restart_agent_rows}")
    elif any((restart_agent_by_name.get(name) or {}).get("status") != "running" for name in ("adhoc", "manager", "ticket-manager")):
        problems.append(f"restart comma --agent missing running statuses: {restart_agent_rows}")


def _check_daemon_reconcile_wait_and_cleanup(ctx: DaemonSmokeContext, problems: list[str]) -> None:
    binary = ctx.binary
    socket_dir = ctx.socket_dir
    env = ctx.env
    team_dir = ctx.team_dir
    sock = ctx.sock
    pid = ctx.pid

    r = subprocess.run(
        [str(binary), "daemon", "restart", "--json", "--ready-timeout", "5s", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        daemon_restart_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"daemon restart --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        daemon_restart_body = {}
    daemon_restart_start = daemon_restart_body.get("start") or {}
    daemon_restart_status = daemon_restart_body.get("status") or {}
    if (
        r.returncode != 0
        or daemon_restart_body.get("action") != "restart"
        or not daemon_restart_body.get("changed")
        or daemon_restart_start.get("action") != "start"
        or not daemon_restart_start.get("pid")
        or not daemon_restart_status.get("running")
        or not daemon_restart_status.get("ready")
    ):
        problems.append(f"daemon restart --json failed: rc={r.returncode}\nbody={daemon_restart_body}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "daemon", "status", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        daemon_status = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"daemon status --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        daemon_status = {}
    if r.returncode != 0 or not daemon_status.get("running") or not daemon_status.get("pid"):
        problems.append(f"daemon status --json missing running daemon: rc={r.returncode}\nbody={daemon_status}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif not daemon_status.get("socket") or not daemon_status.get("log"):
        problems.append(f"daemon status --json missing paths: {daemon_status}")

    r = subprocess.run(
        [str(binary), "daemon", "reconcile", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        reconcile_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"daemon reconcile --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        reconcile_body = {}
    reconcile_instances = {row.get("instance") for row in reconcile_body.get("instances") or []}
    if r.returncode != 0 or not reconcile_body.get("reconciled"):
        problems.append(f"daemon reconcile --json failed: rc={r.returncode}\nbody={reconcile_body}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif reconcile_body.get("changed") != 0 or reconcile_body.get("changes"):
        problems.append(f"daemon reconcile --json reported unexpected changes: {reconcile_body}")
    elif not {"adhoc", "manager", "ticket-manager"}.issubset(reconcile_instances):
        problems.append(f"daemon reconcile --json missing expected instances: {reconcile_body}")
    r = subprocess.run(
        [str(binary), "daemon", "reconcile", "--format", "{{.Reconciled}}:{{.Changed}}:{{len .Instances}}", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    reconcile_format_parts = r.stdout.strip().split(":")
    reconcile_format_count = 0
    if len(reconcile_format_parts) == 3 and reconcile_format_parts[2].isdigit():
        reconcile_format_count = int(reconcile_format_parts[2])
    if (
        r.returncode != 0
        or len(reconcile_format_parts) != 3
        or reconcile_format_parts[0] != "true"
        or reconcile_format_parts[1] != "0"
        or reconcile_format_count < 3
    ):
        problems.append(f"daemon reconcile --format failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "reload", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        reload_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"reload --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        reload_body = {}
    reload_topology_instances = {
        row.get("name")
        for row in (reload_body.get("topology") or {}).get("instances") or []
    }
    reload_reconcile = reload_body.get("reconcile") or {}
    reload_reconcile_instances = {row.get("instance") for row in reload_reconcile.get("instances") or []}
    if r.returncode != 0:
        problems.append(f"reload --json failed: rc={r.returncode}\nbody={reload_body}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif not {"manager", "ticket-manager", "worker"}.issubset(reload_topology_instances):
        problems.append(f"reload --json missing declared topology instances: {reload_body}")
    elif reload_reconcile.get("changed") != 0 or reload_reconcile.get("changes"):
        problems.append(f"reload --json reported unexpected reconcile changes: {reload_body}")
    elif not {"adhoc", "manager", "ticket-manager"}.issubset(reload_reconcile_instances):
        problems.append(f"reload --json missing expected reconciled instances: {reload_body}")
    r = subprocess.run(
        [str(binary), "reload", "--format", "{{len .Topology.Instances}}:{{.Reconcile.Changed}}:{{len .Reconcile.Instances}}", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    reload_format_parts = r.stdout.strip().split(":")
    reload_format_topology_count = 0
    reload_format_reconcile_count = 0
    if len(reload_format_parts) == 3 and reload_format_parts[0].isdigit() and reload_format_parts[2].isdigit():
        reload_format_topology_count = int(reload_format_parts[0])
        reload_format_reconcile_count = int(reload_format_parts[2])
    if (
        r.returncode != 0
        or len(reload_format_parts) != 3
        or reload_format_topology_count < 3
        or reload_format_parts[1] != "0"
        or reload_format_reconcile_count < 3
    ):
        problems.append(f"reload --format failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "ps", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        post_restart_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"ps --json after daemon restart returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        post_restart_rows = []
    post_restart_by_name = {row.get("instance"): row for row in post_restart_rows}
    if r.returncode != 0:
        problems.append(f"ps after daemon restart failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif any((post_restart_by_name.get(name) or {}).get("status") != "running" for name in ("adhoc", "manager", "ticket-manager")):
        problems.append(f"daemon restart did not reconcile running instances: {post_restart_rows}")

    r = subprocess.run(
        [str(binary), "stop", "--agent", "ticket-manager", "--status", "running", "--dry-run", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        dry_stop_agent_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"stop --agent --dry-run --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        dry_stop_agent_rows = []
    if r.returncode != 0 or len(dry_stop_agent_rows) != 1:
        problems.append(f"stop --agent ticket-manager --status running --dry-run failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif dry_stop_agent_rows[0].get("instance") != "ticket-manager" or dry_stop_agent_rows[0].get("action") != "stop":
        problems.append(f"stop --agent ticket-manager --status running --dry-run returned unexpected row: {dry_stop_agent_rows}")
    elif dry_stop_agent_rows[0].get("status") != "running" or not dry_stop_agent_rows[0].get("dry_run"):
        problems.append(f"stop --agent ticket-manager --status running --dry-run missing running dry-run row: {dry_stop_agent_rows}")

    r = subprocess.run(
        [
            str(binary), "stop",
            "--agent", "ticket-manager",
            "--status", "running",
            "--wait",
            "--wait-timeout", "5s",
            "--timeout", "1s",
            "--json",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    try:
        stop_agent_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"stop --agent --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        stop_agent_rows = []
    if r.returncode != 0 or len(stop_agent_rows) != 1:
        problems.append(f"stop --agent ticket-manager --status running failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif stop_agent_rows[0].get("instance") != "ticket-manager" or stop_agent_rows[0].get("action") != "stop":
        problems.append(f"stop --agent ticket-manager returned unexpected row: {stop_agent_rows}")
    elif stop_agent_rows[0].get("status") != "stopped" or stop_agent_rows[0].get("wait_status") != "stopped":
        problems.append(f"stop --agent ticket-manager did not wait for stopped status: {stop_agent_rows}")

    r = subprocess.run(
        [str(binary), "wait", "--agent", "ticket-manager", "--timeout", "5s", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        wait_agent_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"wait --agent ticket-manager --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        wait_agent_rows = []
    if r.returncode != 0 or len(wait_agent_rows) != 1:
        problems.append(f"wait --agent ticket-manager failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif wait_agent_rows[0].get("instance") != "ticket-manager" or wait_agent_rows[0].get("status") != "stopped":
        problems.append(f"wait --agent ticket-manager returned unexpected row: {wait_agent_rows}")

    r = subprocess.run(
        [
            str(binary), "wait",
            "--agent", "ticket-manager",
            "--status", "stopped",
            "--until", "stopped",
            "--timeout", "5s",
            "--json",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    try:
        wait_status_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"wait --agent ticket-manager --status stopped --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        wait_status_rows = []
    if r.returncode != 0 or len(wait_status_rows) != 1:
        problems.append(f"wait --agent ticket-manager --status stopped failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif wait_status_rows[0].get("instance") != "ticket-manager" or wait_status_rows[0].get("status") != "stopped":
        problems.append(f"wait --agent ticket-manager --status stopped returned unexpected row: {wait_status_rows}")

    r = subprocess.run(
        [str(binary), "sync", "--wait", "--timeout", "5s", "--ready-timeout", "5s", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True, env=env,
    )
    try:
        sync_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"sync --wait --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        sync_body = {}
    sync_rows = sync_body.get("actions") or []
    sync_health = sync_body.get("health") or {}
    sync_by_name = {row.get("instance"): row for row in sync_rows if isinstance(row, dict)}
    if r.returncode != 0:
        problems.append(f"sync --wait after stop --agent failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif not sync_health.get("healthy"):
        problems.append(f"sync --wait did not report healthy fleet: {sync_body}")
    elif (sync_by_name.get("ticket-manager") or {}).get("action") != "resume":
        problems.append(f"sync --wait did not resume ticket-manager: {sync_body}")
    elif (sync_by_name.get("ticket-manager") or {}).get("status") != "running":
        problems.append(f"sync --wait did not return running ticket-manager: {sync_body}")
    elif (sync_by_name.get("manager") or {}).get("action") != "skip":
        problems.append(f"sync --wait should skip already-running manager: {sync_body}")

    r = subprocess.run(
        [str(binary), "sync", "--quiet", "--wait", "--timeout", "5s", "--ready-timeout", "5s", "--target", str(socket_dir)],
        capture_output=True, text=True, env=env,
    )
    if r.returncode != 0 or r.stdout or r.stderr:
        problems.append(f"sync --quiet --wait failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "sync", "--format", "{{.Instance}}:{{.Action}}:{{.Status}}", "--ready-timeout", "5s", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    formatted_sync_rows = {line.strip() for line in r.stdout.splitlines() if line.strip()}
    if r.returncode != 0 or "manager:skip:running" not in formatted_sync_rows or "ticket-manager:skip:running" not in formatted_sync_rows:
        problems.append(f"sync --format failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "wait", "ticket-manager", "--until", "running", "--timeout", "5s", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        wait_running_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"wait ticket-manager --until running --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        wait_running_rows = []
    if r.returncode != 0 or len(wait_running_rows) != 1:
        problems.append(f"wait ticket-manager --until running failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif wait_running_rows[0].get("instance") != "ticket-manager" or wait_running_rows[0].get("status") != "running":
        problems.append(f"wait ticket-manager --until running returned unexpected row: {wait_running_rows}")

    ticket_state = team_dir / "state" / "ticket-manager"
    ticket_state.mkdir(parents=True, exist_ok=True)
    (ticket_state / "status.toml").write_text(
        '[status]\nphase = "idle"\ndescription = "smoke idle"\n',
        encoding="utf-8",
    )

    r = subprocess.run(
        [str(binary), "wait", "ticket-manager", "--until-phase", "idle", "--timeout", "5s", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        wait_phase_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"wait ticket-manager --until-phase idle --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        wait_phase_rows = []
    if r.returncode != 0 or len(wait_phase_rows) != 1:
        problems.append(f"wait ticket-manager --until-phase idle failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif (
        wait_phase_rows[0].get("instance") != "ticket-manager"
        or wait_phase_rows[0].get("status") != "running"
        or wait_phase_rows[0].get("phase") != "idle"
    ):
        problems.append(f"wait ticket-manager --until-phase idle returned unexpected row: {wait_phase_rows}")

    r = subprocess.run(
        [str(binary), "wait", "--phase", "idle", "--until", "running", "--timeout", "5s", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        wait_phase_filter_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"wait --phase idle --until running --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        wait_phase_filter_rows = []
    if r.returncode != 0 or len(wait_phase_filter_rows) != 1:
        problems.append(f"wait --phase idle --until running failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif (
        wait_phase_filter_rows[0].get("instance") != "ticket-manager"
        or wait_phase_filter_rows[0].get("phase") != "idle"
    ):
        problems.append(f"wait --phase idle --until running returned unexpected row: {wait_phase_filter_rows}")

    r = subprocess.run(
        [str(binary), "wait", "ticket-manager", "--until", "running", "--timeout", "5s", "--format", "{{.Instance}}:{{.Status}}", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    formatted_wait_rows = {line.strip() for line in r.stdout.splitlines() if line.strip()}
    if r.returncode != 0 or "ticket-manager:running" not in formatted_wait_rows:
        problems.append(f"wait ticket-manager --format --until running failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "wait", "ticket-manager", "--quiet", "--until", "running", "--timeout", "5s", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or r.stdout or r.stderr:
        problems.append(f"wait ticket-manager --quiet --until running failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "stop", "--all", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        stop_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"stop --all --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        stop_rows = []
    if r.returncode != 0:
        problems.append(f"agent-team stop --all failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    stop_by_name = {row.get("instance"): row for row in stop_rows}
    for name in ("adhoc", "manager", "ticket-manager"):
        row = stop_by_name.get(name)
        if not row or row.get("action") != "stop" or row.get("status") != "stopped":
            problems.append(f"stop --all --json missing stopped {name}: {stop_rows}")

    r = subprocess.run(
        [str(binary), "wait", "--all", "--timeout", "5s", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        wait_all_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"wait --all --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        wait_all_rows = []
    wait_all_instances = {row.get("instance") for row in wait_all_rows}
    if r.returncode != 0 or not {"adhoc", "manager", "ticket-manager"}.issubset(wait_all_instances):
        problems.append(f"wait --all failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif any(row.get("status") not in {"stopped", "exited", "crashed"} for row in wait_all_rows):
        problems.append(f"wait --all returned non-terminal rows: {wait_all_rows}")

    r = subprocess.run(
        [str(binary), "monitor", "--all", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        monitor_all_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"monitor --all --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        monitor_all_body = {}
    monitor_all_stats = monitor_all_body.get("stats") or []
    monitor_all_stat_names = {row.get("instance") for row in monitor_all_stats}
    if r.returncode != 0 or not {"adhoc", "manager", "ticket-manager"}.issubset(monitor_all_stat_names):
        problems.append(f"monitor --all --json missing stopped stats: rc={r.returncode}\nbody={monitor_all_body}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif any(row.get("status") != "stopped" for row in monitor_all_stats if row.get("instance") in {"adhoc", "manager", "ticket-manager"}):
        problems.append(f"monitor --all --json returned unexpected stopped rows: {monitor_all_stats}")

    r = subprocess.run(
        [str(binary), "prune", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        prune_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"prune --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        prune_rows = None
    if r.returncode != 0 or prune_rows != []:
        problems.append(f"agent-team prune failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "prune", "--agent", "manager", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        prune_agent_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"prune --agent --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        prune_agent_rows = None
    if r.returncode != 0 or prune_agent_rows != []:
        problems.append(f"agent-team prune --agent failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "prune", "--quiet", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or r.stdout or r.stderr:
        problems.append(f"agent-team prune --quiet failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    format_rm_state = team_dir / "state" / "format-rm"
    format_rm_state.mkdir(parents=True, exist_ok=True)
    r = subprocess.run(
        [str(binary), "rm", "format-rm", "--force", "--format", "{{.Instance}}:{{.Path}}", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    formatted_rm_rows = {line.strip() for line in r.stdout.splitlines() if line.strip()}
    if r.returncode != 0 or "format-rm:.agent_team/state/format-rm" not in formatted_rm_rows:
        problems.append(f"agent-team rm format-rm --format failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif format_rm_state.exists():
        problems.append(f"agent-team rm --format left state dir behind: {format_rm_state}")

    quiet_rm_state = team_dir / "state" / "quiet-rm"
    quiet_rm_state.mkdir(parents=True, exist_ok=True)
    r = subprocess.run(
        [str(binary), "rm", "quiet-rm", "--force", "--quiet", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or r.stdout or r.stderr:
        problems.append(f"agent-team rm quiet-rm --quiet failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif quiet_rm_state.exists():
        problems.append(f"agent-team rm --quiet left state dir behind: {quiet_rm_state}")

    r = subprocess.run(
        [
            str(binary), "rm",
            "--agent", "ticket-manager",
            "--status", "stopped",
            "--force",
            "--json",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    try:
        rm_status_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"rm --agent ticket-manager --status stopped --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        rm_status_rows = []
    if r.returncode != 0 or len(rm_status_rows) != 1:
        problems.append(f"agent-team rm --agent ticket-manager --status stopped failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif rm_status_rows[0].get("instance") != "ticket-manager" or not rm_status_rows[0].get("removed"):
        problems.append(f"rm --agent ticket-manager --status stopped returned unexpected row: {rm_status_rows}")

    r = subprocess.run(
        [str(binary), "rm", "adhoc", "--force", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        rm_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"rm --json returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        rm_rows = []
    if r.returncode != 0 or not rm_rows:
        problems.append(f"agent-team rm adhoc failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    elif rm_rows[0].get("instance") != "adhoc" or not rm_rows[0].get("removed"):
        problems.append(f"rm --json returned unexpected row: {rm_rows[0]}")


def _check_daemon_offline_fallbacks(ctx: DaemonSmokeContext, problems: list[str]) -> None:
    binary = ctx.binary
    socket_dir = ctx.socket_dir
    env = ctx.env
    team_dir = ctx.team_dir
    sock = ctx.sock
    pid = ctx.pid

    r = subprocess.run(
        [str(binary), "ps", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or "adhoc" in r.stdout:
        problems.append(f"ps after rm adhoc failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")

    r = subprocess.run(
        [str(binary), "daemon", "stop", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0:
        problems.append(f"daemon stop failed: {r.stderr}")
    r = subprocess.run(
        [str(binary), "ps", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        stopped_ps_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"ps --json after daemon stop returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        stopped_ps_rows = []
    stopped_ps_by_name = {row.get("instance"): row for row in stopped_ps_rows if isinstance(row, dict)}
    if r.returncode != 0 or (stopped_ps_by_name.get("manager") or {}).get("status") != "stopped":
        problems.append(
            "ps --json local metadata fallback after daemon stop failed: "
            f"rc={r.returncode}\nrows={stopped_ps_rows}\nstdout={r.stdout}\nstderr={r.stderr}"
        )
    r = subprocess.run(
        [str(binary), "start", "--dry-run", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        stopped_start_dry_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"start --dry-run --json after daemon stop returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        stopped_start_dry_rows = []
    stopped_start_dry_by_name = {row.get("instance"): row for row in stopped_start_dry_rows if isinstance(row, dict)}
    if (
        r.returncode != 0
        or (stopped_start_dry_by_name.get("manager") or {}).get("action") != "resume"
        or (stopped_start_dry_by_name.get("manager") or {}).get("status") != "stopped"
    ):
        problems.append(
            "start --dry-run local metadata fallback after daemon stop failed: "
            f"rc={r.returncode}\nrows={stopped_start_dry_rows}\nstdout={r.stdout}\nstderr={r.stderr}"
        )
    r = subprocess.run(
        [str(binary), "restart", "manager", "--dry-run", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        stopped_restart_dry_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"restart manager --dry-run --json after daemon stop returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        stopped_restart_dry_rows = []
    if (
        r.returncode != 0
        or len(stopped_restart_dry_rows) != 1
        or stopped_restart_dry_rows[0].get("instance") != "manager"
        or stopped_restart_dry_rows[0].get("action") != "restart"
        or stopped_restart_dry_rows[0].get("status") != "stopped"
    ):
        problems.append(
            "restart --dry-run local metadata fallback after daemon stop failed: "
            f"rc={r.returncode}\nrows={stopped_restart_dry_rows}\nstdout={r.stdout}\nstderr={r.stderr}"
        )
    r = subprocess.run(
        [str(binary), "stop", "manager", "--dry-run", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        stopped_stop_dry_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"stop manager --dry-run --json after daemon stop returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        stopped_stop_dry_rows = []
    if (
        r.returncode != 0
        or len(stopped_stop_dry_rows) != 1
        or stopped_stop_dry_rows[0].get("instance") != "manager"
        or stopped_stop_dry_rows[0].get("action") != "skip"
        or stopped_stop_dry_rows[0].get("status") != "skipped"
    ):
        problems.append(
            "stop --dry-run local metadata fallback after daemon stop failed: "
            f"rc={r.returncode}\nrows={stopped_stop_dry_rows}\nstdout={r.stdout}\nstderr={r.stderr}"
        )
    r = subprocess.run(
        [str(binary), "wait", "manager", "--until", "stopped", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        stopped_wait_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"wait manager --until stopped --json after daemon stop returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        stopped_wait_rows = []
    if (
        r.returncode != 0
        or len(stopped_wait_rows) != 1
        or stopped_wait_rows[0].get("instance") != "manager"
        or stopped_wait_rows[0].get("status") != "stopped"
    ):
        problems.append(
            "wait manager local metadata fallback after daemon stop failed: "
            f"rc={r.returncode}\nrows={stopped_wait_rows}\nstdout={r.stdout}\nstderr={r.stderr}"
        )
    r = subprocess.run(
        [str(binary), "stats", "--all", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        stopped_stats_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"stats --all --json after daemon stop returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        stopped_stats_rows = []
    stopped_stats_by_name = {row.get("instance"): row for row in stopped_stats_rows if isinstance(row, dict)}
    if r.returncode != 0 or (stopped_stats_by_name.get("manager") or {}).get("status") != "stopped":
        problems.append(
            "stats --all --json local metadata fallback after daemon stop failed: "
            f"rc={r.returncode}\nrows={stopped_stats_rows}\nstdout={r.stdout}\nstderr={r.stderr}"
        )
    r = subprocess.run(
        [str(binary), "logs", "manager", "--tail", "1", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or "fake claude invoked:" not in r.stdout:
        problems.append(f"logs manager local fallback after daemon stop failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    r = subprocess.run(
        [str(binary), "send", "manager", "offline smoke message", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        stopped_send_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"send manager --json after daemon stop returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        stopped_send_body = {}
    manager_mailbox = team_dir / "daemon" / "manager" / "mailbox.jsonl"
    manager_mailbox_body = manager_mailbox.read_text() if manager_mailbox.exists() else ""
    if (
        r.returncode != 0
        or not stopped_send_body.get("delivered")
        or stopped_send_body.get("to") != "manager"
        or "offline smoke message" not in manager_mailbox_body
    ):
        problems.append(
            "send local mailbox fallback after daemon stop failed: "
            f"rc={r.returncode}\nbody={stopped_send_body}\nstdout={r.stdout}\nstderr={r.stderr}"
        )
    r = subprocess.run(
        [str(binary), "channel", "publish", "#offline", "daemon down broadcast", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or "published seq=1" not in r.stdout:
        problems.append(f"channel publish local fallback after daemon stop failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    r = subprocess.run(
        [str(binary), "channels", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    if r.returncode != 0 or "#offline" not in r.stdout:
        problems.append(f"channels local fallback after daemon stop failed: rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}")
    r = subprocess.run(
        [str(binary), "logs", "--list", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        stopped_log_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"logs --list --json after daemon stop returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        stopped_log_rows = []
    stopped_log_by_name = {row.get("instance"): row for row in stopped_log_rows if isinstance(row, dict)}
    if r.returncode != 0 or not (stopped_log_by_name.get("manager") or {}).get("exists"):
        problems.append(
            "logs --list --json local fallback after daemon stop failed: "
            f"rc={r.returncode}\nrows={stopped_log_rows}\nstdout={r.stdout}\nstderr={r.stderr}"
        )
    r = subprocess.run(
        [str(binary), "inspect", "manager", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        stopped_inspect_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"inspect manager --json after daemon stop returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        stopped_inspect_body = {}
    stopped_runtime = stopped_inspect_body.get("runtime") or {}
    if r.returncode != 0 or stopped_runtime.get("agent") != "manager" or stopped_runtime.get("lifecycle") != "stopped":
        problems.append(
            "inspect manager local metadata fallback after daemon stop failed: "
            f"rc={r.returncode}\nbody={stopped_inspect_body}\nstdout={r.stdout}\nstderr={r.stderr}"
        )
    r = subprocess.run(
        [str(binary), "events", "--format", "{{.Action}}:{{.Instance}}", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    stopped_event_rows = {line.strip() for line in r.stdout.splitlines() if line.strip()}
    if r.returncode != 0 or "dispatch:manager" not in stopped_event_rows:
        problems.append(
            "events local fallback after daemon stop failed: "
            f"rc={r.returncode}\nstdout={r.stdout}\nstderr={r.stderr}"
        )
    r = subprocess.run(
        [str(binary), "monitor", "--events", "200", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        stopped_monitor_body = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"monitor --events --json after daemon stop returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        stopped_monitor_body = {}
    stopped_monitor_events = stopped_monitor_body.get("events") or []
    stopped_monitor_pairs = {(row.get("action"), row.get("instance")) for row in stopped_monitor_events if isinstance(row, dict)}
    if (
        r.returncode != 0
        or stopped_monitor_body.get("events_error")
        or stopped_monitor_body.get("stats_error")
        or ("dispatch", "manager") not in stopped_monitor_pairs
    ):
        problems.append(
            "monitor --events --json local fallback after daemon stop failed: "
            f"rc={r.returncode}\nbody={stopped_monitor_body}\nstdout={r.stdout}\nstderr={r.stderr}"
        )
    cleanup_stopped_state = team_dir / "state" / "cleanup-stopped-local"
    cleanup_stopped_meta_dir = team_dir / "daemon" / "cleanup-stopped-local"
    cleanup_stopped_state.mkdir(parents=True, exist_ok=True)
    cleanup_stopped_meta_dir.mkdir(parents=True, exist_ok=True)
    (cleanup_stopped_meta_dir / "meta.json").write_text(json.dumps({
        "instance": "cleanup-stopped-local",
        "agent": "worker",
        "status": "stopped",
    }) + "\n")
    r = subprocess.run(
        [
            str(binary), "rm",
            "--status", "stopped",
            "--agent", "worker",
            "--force",
            "--json",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    try:
        stopped_rm_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"rm --status stopped --agent worker after daemon stop returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        stopped_rm_rows = []
    if (
        r.returncode != 0
        or len(stopped_rm_rows) != 1
        or stopped_rm_rows[0].get("instance") != "cleanup-stopped-local"
        or not stopped_rm_rows[0].get("removed")
        or cleanup_stopped_state.exists()
        or cleanup_stopped_meta_dir.exists()
    ):
        problems.append(
            "rm local metadata fallback after daemon stop failed: "
            f"rc={r.returncode}\nrows={stopped_rm_rows}\nstdout={r.stdout}\nstderr={r.stderr}"
        )
    cleanup_done_state = team_dir / "state" / "cleanup-done-local"
    cleanup_done_meta_dir = team_dir / "daemon" / "cleanup-done-local"
    cleanup_idle_state = team_dir / "state" / "cleanup-idle-local"
    cleanup_idle_meta_dir = team_dir / "daemon" / "cleanup-idle-local"
    cleanup_done_state.mkdir(parents=True, exist_ok=True)
    cleanup_done_meta_dir.mkdir(parents=True, exist_ok=True)
    cleanup_idle_state.mkdir(parents=True, exist_ok=True)
    cleanup_idle_meta_dir.mkdir(parents=True, exist_ok=True)
    (cleanup_done_state / "status.toml").write_text('[status]\nphase = "done"\ndescription = "finished"\n')
    (cleanup_idle_state / "status.toml").write_text('[status]\nphase = "idle"\ndescription = "waiting"\n')
    (cleanup_done_meta_dir / "meta.json").write_text(json.dumps({
        "instance": "cleanup-done-local",
        "agent": "worker",
        "status": "stopped",
    }) + "\n")
    (cleanup_idle_meta_dir / "meta.json").write_text(json.dumps({
        "instance": "cleanup-idle-local",
        "agent": "worker",
        "status": "stopped",
    }) + "\n")
    r = subprocess.run(
        [
            str(binary), "rm",
            "--phase", "done",
            "--agent", "worker",
            "--force",
            "--json",
            "--target", str(socket_dir),
        ],
        capture_output=True, text=True,
    )
    try:
        stopped_rm_phase_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"rm --phase done --agent worker after daemon stop returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        stopped_rm_phase_rows = []
    if (
        r.returncode != 0
        or len(stopped_rm_phase_rows) != 1
        or stopped_rm_phase_rows[0].get("instance") != "cleanup-done-local"
        or not stopped_rm_phase_rows[0].get("removed")
        or cleanup_done_state.exists()
        or cleanup_done_meta_dir.exists()
        or not cleanup_idle_state.exists()
        or not cleanup_idle_meta_dir.exists()
    ):
        problems.append(
            "rm --phase local metadata fallback after daemon stop failed: "
            f"rc={r.returncode}\nrows={stopped_rm_phase_rows}\nstdout={r.stdout}\nstderr={r.stderr}"
        )
    cleanup_crashed_state = team_dir / "state" / "cleanup-crashed-local"
    cleanup_crashed_meta_dir = team_dir / "daemon" / "cleanup-crashed-local"
    cleanup_exited_state = team_dir / "state" / "cleanup-exited-local"
    cleanup_exited_meta_dir = team_dir / "daemon" / "cleanup-exited-local"
    cleanup_crashed_state.mkdir(parents=True, exist_ok=True)
    cleanup_crashed_meta_dir.mkdir(parents=True, exist_ok=True)
    cleanup_exited_state.mkdir(parents=True, exist_ok=True)
    cleanup_exited_meta_dir.mkdir(parents=True, exist_ok=True)
    (cleanup_crashed_meta_dir / "meta.json").write_text(json.dumps({
        "instance": "cleanup-crashed-local",
        "agent": "worker",
        "status": "crashed",
    }) + "\n")
    (cleanup_exited_meta_dir / "meta.json").write_text(json.dumps({
        "instance": "cleanup-exited-local",
        "agent": "worker",
        "status": "exited",
    }) + "\n")
    r = subprocess.run(
        [str(binary), "prune", "--status", "crashed", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        stopped_prune_status_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"prune --status crashed after daemon stop returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        stopped_prune_status_rows = []
    if (
        r.returncode != 0
        or len(stopped_prune_status_rows) != 1
        or stopped_prune_status_rows[0].get("instance") != "cleanup-crashed-local"
        or not stopped_prune_status_rows[0].get("removed")
        or cleanup_crashed_state.exists()
        or cleanup_crashed_meta_dir.exists()
        or not cleanup_exited_state.exists()
        or not cleanup_exited_meta_dir.exists()
    ):
        problems.append(
            "prune --status crashed local metadata fallback failed: "
            f"rc={r.returncode}\nrows={stopped_prune_status_rows}\nstdout={r.stdout}\nstderr={r.stderr}"
        )
    r = subprocess.run(
        [str(binary), "prune", "--status", "exited", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        stopped_prune_status_exited_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"prune --status exited after daemon stop returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        stopped_prune_status_exited_rows = []
    if (
        r.returncode != 0
        or len(stopped_prune_status_exited_rows) != 1
        or stopped_prune_status_exited_rows[0].get("instance") != "cleanup-exited-local"
        or not stopped_prune_status_exited_rows[0].get("removed")
        or cleanup_exited_state.exists()
        or cleanup_exited_meta_dir.exists()
    ):
        problems.append(
            "prune --status exited local metadata fallback failed: "
            f"rc={r.returncode}\nrows={stopped_prune_status_exited_rows}\nstdout={r.stdout}\nstderr={r.stderr}"
        )
    cleanup_finished_state = team_dir / "state" / "cleanup-finished-local"
    cleanup_finished_meta_dir = team_dir / "daemon" / "cleanup-finished-local"
    cleanup_finished_state.mkdir(parents=True, exist_ok=True)
    cleanup_finished_meta_dir.mkdir(parents=True, exist_ok=True)
    (cleanup_finished_meta_dir / "meta.json").write_text(json.dumps({
        "instance": "cleanup-finished-local",
        "agent": "worker",
        "status": "exited",
    }) + "\n")
    cleanup_finished_done_state = team_dir / "state" / "cleanup-finished-done-local"
    cleanup_finished_done_meta_dir = team_dir / "daemon" / "cleanup-finished-done-local"
    cleanup_finished_done_state.mkdir(parents=True, exist_ok=True)
    cleanup_finished_done_meta_dir.mkdir(parents=True, exist_ok=True)
    (cleanup_finished_done_state / "status.toml").write_text('[status]\nphase = "done"\ndescription = "finished"\n')
    (cleanup_finished_done_meta_dir / "meta.json").write_text(json.dumps({
        "instance": "cleanup-finished-done-local",
        "agent": "worker",
        "status": "exited",
    }) + "\n")
    r = subprocess.run(
        [str(binary), "prune", "--phase", "done", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        stopped_prune_phase_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"prune --phase done after daemon stop returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        stopped_prune_phase_rows = []
    if (
        r.returncode != 0
        or len(stopped_prune_phase_rows) != 1
        or stopped_prune_phase_rows[0].get("instance") != "cleanup-finished-done-local"
        or not stopped_prune_phase_rows[0].get("removed")
        or cleanup_finished_done_state.exists()
        or cleanup_finished_done_meta_dir.exists()
    ):
        problems.append(
            "prune --phase local metadata fallback after daemon stop failed: "
            f"rc={r.returncode}\nrows={stopped_prune_phase_rows}\nstdout={r.stdout}\nstderr={r.stderr}"
        )
    r = subprocess.run(
        [str(binary), "prune", "--json", "--target", str(socket_dir)],
        capture_output=True, text=True,
    )
    try:
        stopped_prune_rows = json.loads(r.stdout)
    except Exception as e:  # noqa: BLE001
        problems.append(f"prune --json after daemon stop returned invalid JSON: {e}\nstdout={r.stdout}\nstderr={r.stderr}")
        stopped_prune_rows = []
    if (
        r.returncode != 0
        or len(stopped_prune_rows) != 1
        or stopped_prune_rows[0].get("instance") != "cleanup-finished-local"
        or not stopped_prune_rows[0].get("removed")
        or cleanup_finished_state.exists()
        or cleanup_finished_meta_dir.exists()
    ):
        problems.append(
            "prune local metadata fallback after daemon stop failed: "
            f"rc={r.returncode}\nrows={stopped_prune_rows}\nstdout={r.stdout}\nstderr={r.stderr}"
        )
    # After stop, pidfile and socket should be gone.
    if pid.exists():
        problems.append(f"daemon pidfile lingered after stop: {pid}")
    if sock.exists():
        problems.append(f"daemon socket lingered after stop: {sock}")


def run(cmd: list[str], env: dict[str, str] | None = None) -> None:
    r = subprocess.run(cmd, capture_output=True, text=True, env=env)
    if r.returncode != 0:
        print(f"command failed: {' '.join(cmd)}", file=sys.stderr)
        print(r.stdout, file=sys.stderr)
        print(r.stderr, file=sys.stderr)
        sys.exit(1)


def scrub_agent_team_env(env: dict[str, str]) -> dict[str, str]:
    return {key: value for key, value in env.items() if not key.startswith("AGENT_TEAM_")}


def generated_artifacts(root: Path) -> list[str]:
    found: list[str] = []
    for path in sorted(root.rglob("*")):
        rel = path.relative_to(root)
        if any(part in FORBIDDEN_ARTIFACT_DIRS for part in rel.parts):
            found.append(str(rel))
        elif path.name in FORBIDDEN_ARTIFACT_FILES:
            found.append(str(rel))
        elif path.suffix in FORBIDDEN_ARTIFACT_SUFFIXES:
            found.append(str(rel))
    return found


def wait_for_file_contains(path: Path, needle: str, timeout: float) -> bool:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            if needle in path.read_text(errors="replace"):
                return True
        except FileNotFoundError:
            pass
        time.sleep(0.1)
    return False


if __name__ == "__main__":
    sys.exit(main(sys.argv))
