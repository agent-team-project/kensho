"""`agent-team doctor` — sanity-check a vendored team."""

from __future__ import annotations

import tomllib
from pathlib import Path
from typing import Annotated

import typer

from agent_team.loader import AgentLoadError, TEAM_DIR_NAME, load_agent, union_skills


def register(app: typer.Typer) -> None:
    @app.command(
        help=(
            "Sanity-check the vendored team: .agent_team/ layout, config.toml validity, "
            "each agent's frontmatter, and skill resolution across all agents."
        ),
    )
    def doctor(
        target: Annotated[Path, typer.Option(help="Repo root.")] = Path.cwd(),
    ) -> None:
        target = target.resolve()
        team_dir = target / TEAM_DIR_NAME
        problems: list[str] = []

        if not team_dir.is_dir():
            problems.append(f"{team_dir} not found — run `agent-team init` first.")
            _exit(problems)
            return

        cfg_path = team_dir / "config.toml"
        if not cfg_path.is_file():
            problems.append(f"{cfg_path} missing — copy config.toml.example and fill it in.")
        else:
            try:
                cfg = tomllib.loads(cfg_path.read_text())
            except tomllib.TOMLDecodeError as e:
                problems.append(f"{cfg_path} is not valid TOML: {e}")
                cfg = {}
            team = cfg.get("team", {})
            if team.get("pm_tool") == "linear":
                linear = cfg.get("linear", {})
                for k in ("team_id", "ticket_prefix"):
                    if not linear.get(k):
                        problems.append(f"[linear].{k} missing/empty in {cfg_path}")

        agents_dir = team_dir / "agents"
        if not agents_dir.is_dir():
            problems.append(f"{agents_dir} missing — re-run `agent-team init`.")
        else:
            agent_dirs = [d for d in agents_dir.iterdir() if d.is_dir()]
            if not agent_dirs:
                problems.append(f"no agents under {agents_dir} — `agent-team agent create <name>` to scaffold one.")
            else:
                loaded = []
                for d in sorted(agent_dirs):
                    try:
                        loaded.append(load_agent(d, team_dir))
                    except AgentLoadError as e:
                        problems.append(str(e))
                if loaded:
                    try:
                        union_skills(loaded)
                    except AgentLoadError as e:
                        problems.append(str(e))

        _exit(problems)


def _exit(problems: list[str]) -> None:
    if not problems:
        typer.echo("agent-team doctor: OK")
        return
    typer.echo("agent-team doctor: problems found:", err=True)
    for p in problems:
        typer.echo(f"  - {p}", err=True)
    raise typer.Exit(1)
