"""`agent-squad doctor` — sanity-check a vendored squad.

v0.1: tomllib-only validation. Confirms required keys are present and the
plugin manifests exist where they should. Does not validate IDs against
Linear/etc. — that's a runtime concern of the skills.
"""

from __future__ import annotations

import argparse
import sys
import tomllib
from pathlib import Path

REQUIRED_LINEAR_KEYS = ("team_id", "ticket_prefix")
REQUIRED_SQUAD_KEYS = ("pm_tool",)


def register(sub: argparse._SubParsersAction) -> None:
    p = sub.add_parser(
        "doctor",
        help="Sanity-check the vendored squad in this repo.",
        description="Verifies .agent_squad/config.toml has required keys and the plugin manifests are present.",
    )
    p.add_argument("--target", type=Path, default=Path.cwd())
    p.set_defaults(func=run)


def run(args: argparse.Namespace) -> int:
    target: Path = args.target.resolve()
    squad_dir = target / ".agent_squad"
    problems: list[str] = []

    if not squad_dir.is_dir():
        problems.append(f"{squad_dir} not found — run `agent-squad init` first.")
        return _report(problems)

    cfg_path = squad_dir / "config.toml"
    if not cfg_path.is_file():
        problems.append(f"{cfg_path} missing — copy config.toml.example and fill it in.")
    else:
        try:
            cfg = tomllib.loads(cfg_path.read_text())
        except tomllib.TOMLDecodeError as e:
            problems.append(f"{cfg_path} is not valid TOML: {e}")
            cfg = {}

        squad = cfg.get("squad", {})
        for k in REQUIRED_SQUAD_KEYS:
            if k not in squad:
                problems.append(f"[squad].{k} missing in {cfg_path}")

        linear = cfg.get("linear", {})
        if squad.get("pm_tool") == "linear":
            for k in REQUIRED_LINEAR_KEYS:
                if not linear.get(k):
                    problems.append(f"[linear].{k} missing/empty in {cfg_path}")

    for path in [
        squad_dir / "agents",
        squad_dir / "skills",
        squad_dir / "managers",
        squad_dir / ".claude-plugin" / "plugin.json",
        target / ".claude-plugin" / "marketplace.json",
    ]:
        if not path.exists():
            problems.append(f"{path.relative_to(target)} missing — re-run `agent-squad init`.")

    return _report(problems)


def _report(problems: list[str]) -> int:
    if not problems:
        print("agent-squad doctor: OK")
        return 0
    print("agent-squad doctor: problems found:", file=sys.stderr)
    for p in problems:
        print(f"  - {p}", file=sys.stderr)
    return 1
