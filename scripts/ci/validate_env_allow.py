#!/usr/bin/env python3
"""Validate env_allow declarations against prompt/script process-env needs."""

from __future__ import annotations

import argparse
import fnmatch
import re
import sys
import tempfile
import textwrap
import tomllib
from dataclasses import dataclass
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]

ENV_NAME = r"[A-Z][A-Z0-9_]*"
MENTION_RE = re.compile(rf"\b({ENV_NAME})\b")
SHELL_BRACE_RE = re.compile(rf"\$\{{({ENV_NAME})")
SHELL_PLAIN_RE = re.compile(rf"(?<![\w$])\$({ENV_NAME})\b")
SHELL_PRINTENV_RE = re.compile(rf"\bprintenv\s+(?:--\s+)?['\"]?({ENV_NAME})['\"]?\b")
SHELL_ENV_GREP_RE = re.compile(
    rf"\b(?:env|printenv)\b\s*\|\s*grep"
    rf"(?:\s+--?[A-Za-z][A-Za-z0-9_-]*)*"
    rf"(?:\s+--)?\s+['\"]?(?:\^)?({ENV_NAME})(?:=|\b)"
)
PY_ENV_RE = re.compile(
    rf"os\.environ(?:\.get)?\(\s*['\"]({ENV_NAME})['\"]|"
    rf"os\.environ\[\s*['\"]({ENV_NAME})['\"]\s*\]"
)
PY_GETENV_RE = re.compile(rf"os\.getenv\(\s*['\"]({ENV_NAME})['\"]")
PROFILE_DIRECTIVE_RE = re.compile(
    r"^\s*\{\{\s*(if\s+eq\s+\.template\.profile\s+`([^`]+)`|else|end)\s*-?\}\}\s*$"
)

CORE_AGENT_TEAM_VARS = {
    "AGENT_TEAM_BRANCH",
    "AGENT_TEAM_BUDGET_TIME",
    "AGENT_TEAM_BUDGET_TOKENS",
    "AGENT_TEAM_DAEMON_SOCKET",
    "AGENT_TEAM_DAEMON_TOKEN_FILE",
    "AGENT_TEAM_DAEMON_URL",
    "AGENT_TEAM_INSTANCE",
    "AGENT_TEAM_JOB",
    "AGENT_TEAM_JOB_ID",
    "AGENT_TEAM_JOB_KIND",
    "AGENT_TEAM_PIPELINE",
    "AGENT_TEAM_PIPELINE_STEP",
    "AGENT_TEAM_PR",
    "AGENT_TEAM_ROOT",
    "AGENT_TEAM_RUNTIME",
    "AGENT_TEAM_RUNTIME_BIN",
    "AGENT_TEAM_STATE_DIR",
    "AGENT_TEAM_TEAM",
    "AGENT_TEAM_TICKET",
    "AGENT_TEAM_TICKET_URL",
    "AGENT_TEAM_WORKTREE",
}
CORE_AGENT_TEAM_PREFIXES = ("AGENT_TEAM_ORIGIN_",)
AGENT_TEAM_SECRET_MARKERS = ("KEY", "SECRET", "TOKEN", "WEBHOOK")


@dataclass(frozen=True)
class Ref:
    var: str
    source: Path
    kind: str


def rel(path: Path, root: Path) -> str:
    try:
        return str(path.relative_to(root))
    except ValueError:
        return str(path)


def is_required_env_name(name: str) -> bool:
    if name in {"GITHUB_TOKEN", "GH_TOKEN"}:
        return True
    if name.startswith("LINEAR_") or name.startswith("GITHUB_"):
        return True
    if not name.startswith("AGENT_TEAM_"):
        return False
    if name in CORE_AGENT_TEAM_VARS:
        return False
    if any(name.startswith(prefix) for prefix in CORE_AGENT_TEAM_PREFIXES):
        return False
    return any(marker in name for marker in AGENT_TEAM_SECRET_MARKERS)


def allowed_by(patterns: list[str], name: str) -> bool:
    return any(fnmatch.fnmatchcase(name, pattern) for pattern in patterns)


def read_toml(path: Path) -> dict:
    if path.name == "instances.toml.tmpl":
        data = tomllib.loads(render_profile_template(path.read_text(), "full"))
        if not isinstance(data, dict):
            return {}
        return data
    with path.open("rb") as f:
        data = tomllib.load(f)
    if not isinstance(data, dict):
        return {}
    return data


def render_profile_template(body: str, profile: str) -> str:
    """Render the tiny subset of Go template syntax used by instances.toml.tmpl."""
    active = True
    stack: list[tuple[bool, bool]] = []
    out: list[str] = []
    for line in body.splitlines():
        match = PROFILE_DIRECTIVE_RE.match(line)
        if match:
            action = match.group(1)
            if action.startswith("if "):
                condition = profile == match.group(2)
                stack.append((active, condition))
                active = active and condition
            elif action == "else":
                if not stack:
                    raise ValueError("template else without if")
                parent_active, condition = stack[-1]
                active = parent_active and not condition
            elif action == "end":
                if not stack:
                    raise ValueError("template end without if")
                parent_active, _ = stack.pop()
                active = parent_active
            continue
        if active:
            out.append(line)
    if stack:
        raise ValueError("template if without end")
    return "\n".join(out) + "\n"


def agent_dir_for(team_dir: Path, agent: str) -> Path:
    return team_dir / "agents" / agent


def shared_skill_dir(team_dir: Path, spec: str, agent_dir: Path) -> Path:
    if "/" in spec or spec.startswith("."):
        return (agent_dir / spec).resolve()
    return (team_dir / "skills" / spec).resolve()


def resolved_skill_dirs(team_dir: Path, agent_dir: Path) -> list[Path]:
    skills: dict[str, Path] = {}
    local_root = agent_dir / "skills"
    if local_root.is_dir():
        for child in sorted(local_root.iterdir()):
            if child.is_dir() and (child / "SKILL.md").is_file():
                skills[child.name] = child.resolve()

    cfg_path = agent_dir / "config.toml"
    cfg = read_toml(cfg_path) if cfg_path.is_file() else {}
    skill_cfg = cfg.get("skills", {})
    if not isinstance(skill_cfg, dict):
        skill_cfg = {}

    for spec in skill_cfg.get("extra", []) or []:
        if not isinstance(spec, str) or not spec.strip():
            continue
        path = shared_skill_dir(team_dir, spec.strip(), agent_dir)
        if (path / "SKILL.md").is_file():
            skills[path.name] = path

    for name in skill_cfg.get("disable", []) or []:
        if isinstance(name, str):
            skills.pop(name.strip(), None)

    team_cfg_path = team_dir / "config.toml"
    team_cfg = read_toml(team_cfg_path) if team_cfg_path.is_file() else {}
    team_skills = team_cfg.get("skills", {})
    if isinstance(team_skills, dict):
        for spec in team_skills.get("team", []) or []:
            if not isinstance(spec, str) or not spec.strip():
                continue
            path = (team_dir / "skills" / spec.strip()).resolve()
            if (path / "SKILL.md").is_file():
                skills[path.name] = path

    return [skills[name] for name in sorted(skills)]


def script_files_under(path: Path) -> list[Path]:
    if not path.is_dir():
        return []
    return [
        p
        for p in sorted(path.rglob("*"))
        if p.is_file() and p.suffix in {".sh", ".py"}
    ]


def line_safe_env_reads(text: str) -> set[str]:
    names: set[str] = set()
    for line in text.splitlines():
        if "read_env_value" not in line:
            continue
        for match in MENTION_RE.finditer(line):
            name = match.group(1)
            if is_required_env_name(name):
                names.add(name)
    return names


def prompt_refs(path: Path) -> list[Ref]:
    text = path.read_text(errors="ignore")
    refs: list[Ref] = []
    for match in MENTION_RE.finditer(text):
        name = match.group(1)
        if is_required_env_name(name):
            refs.append(Ref(name, path, "prompt"))
    return refs


def script_refs(path: Path) -> list[Ref]:
    text = path.read_text(errors="ignore")
    direct_env = line_safe_env_reads(text)
    names: set[str] = set()

    for pattern in (
        SHELL_BRACE_RE,
        SHELL_PLAIN_RE,
        SHELL_PRINTENV_RE,
        SHELL_ENV_GREP_RE,
        PY_GETENV_RE,
    ):
        for match in pattern.finditer(text):
            names.add(match.group(1))

    for match in PY_ENV_RE.finditer(text):
        names.update(group for group in match.groups() if group)

    return [
        Ref(name, path, "script")
        for name in sorted(names)
        if is_required_env_name(name) and name not in direct_env
    ]


def instance_refs(team_dir: Path, agent: str) -> list[Ref]:
    agent_dir = agent_dir_for(team_dir, agent)
    refs: list[Ref] = []

    prompt = agent_dir / "agent.md"
    if prompt.is_file():
        refs.extend(prompt_refs(prompt))

    for script in script_files_under(agent_dir / "scripts"):
        refs.extend(script_refs(script))

    for skill_dir in resolved_skill_dirs(team_dir, agent_dir):
        for script in script_files_under(skill_dir / "scripts"):
            refs.extend(script_refs(script))

    return refs


def team_dir_for_instance_file(root: Path, instance_file: Path) -> Path:
    if ".agent_team" in instance_file.parts:
        return root / ".agent_team"
    return root / "template"


def validate_instance_file(root: Path, instance_file: Path) -> list[str]:
    data = read_toml(instance_file)
    instances = data.get("instances", {})
    if not isinstance(instances, dict):
        return []

    team_dir = team_dir_for_instance_file(root, instance_file)
    failures: list[str] = []
    for instance_name, raw in sorted(instances.items()):
        if not isinstance(raw, dict) or "env_allow" not in raw:
            continue
        agent = str(raw.get("agent", "")).strip()
        if not agent:
            failures.append(
                f"{rel(instance_file, root)}: instance {instance_name!r}: env_allow set but agent is empty"
            )
            continue
        allow = [str(item).strip() for item in raw.get("env_allow", [])]
        refs = instance_refs(team_dir, agent)
        missing: dict[str, list[Ref]] = {}
        for ref in refs:
            if not allowed_by(allow, ref.var):
                missing.setdefault(ref.var, []).append(ref)
        for var, var_refs in sorted(missing.items()):
            sources = ", ".join(
                sorted({f"{rel(ref.source, root)} ({ref.kind})" for ref in var_refs})
            )
            failures.append(
                f"{rel(instance_file, root)}: instance {instance_name!r} "
                f"(agent {agent!r}) references {var} but env_allow does not match it; "
                f"sources: {sources}"
            )

    return failures


def default_instance_files(root: Path) -> list[Path]:
    paths = [
        root / "template" / "instances.toml.tmpl",
        root / ".agent_team" / "instances.toml",
    ]
    return [path for path in paths if path.is_file()]


def validate(root: Path) -> list[str]:
    failures: list[str] = []
    for instance_file in default_instance_files(root):
        failures.extend(validate_instance_file(root, instance_file))
    return failures


def write(path: Path, body: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(textwrap.dedent(body).lstrip())


def run_self_test() -> None:
    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp)
        write(
            root / "template" / "instances.toml.tmpl",
            """
            [instances.comms]
            agent = "comms"
            env_allow = ["PATH"]
            """,
        )
        write(
            root / "template" / "agents" / "comms" / "agent.md",
            """
            ---
            description: Comms
            ---
            Post through `AGENT_TEAM_DISCORD_WEBHOOK` from the environment.
            """,
        )
        failures = validate(root)
        if len(failures) != 1 or "AGENT_TEAM_DISCORD_WEBHOOK" not in failures[0]:
            raise AssertionError(f"expected missing webhook failure, got {failures!r}")

        write(
            root / "template" / "instances.toml.tmpl",
            """
            [instances.comms]
            agent = "comms"
            env_allow = ["PATH", "AGENT_TEAM_DISCORD_WEBHOOK"]
            """,
        )
        failures = validate(root)
        if failures:
            raise AssertionError(f"expected explicit allowlist to pass, got {failures!r}")

        write(
            root / "template" / "instances.toml.tmpl",
            """
            [instances.comms]
            agent = "comms"
            env_allow = ["PATH"]
            """,
        )
        write(
            root / "template" / "agents" / "comms" / "agent.md",
            """
            ---
            description: Comms
            ---
            Post through the comms helper.
            """,
        )
        write(
            root / "template" / "agents" / "comms" / "config.toml",
            """
            [skills]
            extra = ["post"]
            """,
        )
        write(
            root / "template" / "skills" / "post" / "SKILL.md",
            """
            ---
            description: Post
            ---
            """,
        )
        write(
            root / "template" / "skills" / "post" / "scripts" / "post.sh",
            """
            #!/usr/bin/env bash
            curl -d hi "${AGENT_TEAM_DISCORD_WEBHOOK:-}"
            """,
        )
        failures = validate(root)
        if len(failures) != 1 or "AGENT_TEAM_DISCORD_WEBHOOK" not in failures[0]:
            raise AssertionError(f"expected script env failure, got {failures!r}")

        def expect_script_env_failure(body: str, label: str) -> None:
            write(root / "template" / "skills" / "post" / "scripts" / "post.sh", body)
            script_failures = validate(root)
            if (
                len(script_failures) != 1
                or "AGENT_TEAM_DISCORD_WEBHOOK" not in script_failures[0]
            ):
                raise AssertionError(
                    f"expected {label} script env failure, got {script_failures!r}"
                )

        expect_script_env_failure(
            """
            #!/usr/bin/env bash
            printenv AGENT_TEAM_DISCORD_WEBHOOK >/dev/null
            """,
            "printenv",
        )
        expect_script_env_failure(
            """
            #!/usr/bin/env bash
            python3 - <<'PY'
            import os
            print(os.getenv('AGENT_TEAM_DISCORD_WEBHOOK'))
            PY
            """,
            "os.getenv single-quoted",
        )
        expect_script_env_failure(
            """
            #!/usr/bin/env bash
            python3 - <<'PY'
            import os
            print(os.getenv("AGENT_TEAM_DISCORD_WEBHOOK"))
            PY
            """,
            "os.getenv double-quoted",
        )
        expect_script_env_failure(
            """
            #!/usr/bin/env bash
            env|grep AGENT_TEAM_DISCORD_WEBHOOK >/dev/null
            """,
            "env grep",
        )

        write(
            root / "template" / "agents" / "comms" / "config.toml",
            """
            [skills]
            extra = ["github"]
            """,
        )
        write(
            root / "template" / "skills" / "github" / "SKILL.md",
            """
            ---
            description: GitHub
            ---
            """,
        )
        write(
            root / "template" / "skills" / "github" / "scripts" / "github-api.sh",
            """
            #!/usr/bin/env bash
            if [ -n "${GITHUB_TOKEN:-}" ]; then
                :
            fi
            if token="$(read_env_value "$PWD/.env" GITHUB_TOKEN GH_TOKEN)"; then
                :
            fi
            """,
        )
        failures = validate(root)
        if failures:
            raise AssertionError(f"expected line-safe .env helper to pass, got {failures!r}")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, default=REPO_ROOT)
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()

    if args.self_test:
        run_self_test()
        print("OK  env_allow validator self-test")
        return 0

    root = args.root.resolve()
    failures = validate(root)
    if failures:
        print("env_allow validation failed:", file=sys.stderr)
        for failure in failures:
            print(f"  - {failure}", file=sys.stderr)
        return 1

    for path in default_instance_files(root):
        print(f"OK  {rel(path, root)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
