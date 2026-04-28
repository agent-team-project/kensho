"""`agent-team skill ...` — manage skills (shared or agent-private)."""

from __future__ import annotations

import shutil
from pathlib import Path
from typing import Annotated, Optional

import typer

from agent_team.loader import TEAM_DIR_NAME

SKILL_TEMPLATE = """\
---
name: {name}
description: TODO — what this skill does. One sentence.
---

# {title}

TODO — write the skill's instructions, recipes, and bash patterns here.
"""

app = typer.Typer(
    help="Manage skills (shared at .agent_team/skills/<name>/, or agent-private at .agent_team/agents/<a>/skills/<name>/).",
    no_args_is_help=True,
)


def _check_kebab(value: str, what: str) -> None:
    if not value or not value.replace("-", "").isalnum() or value != value.lower():
        typer.echo(f"agent-team: {what} must be kebab-case lowercase alnum: {value!r}", err=True)
        raise typer.Exit(2)


def _resolve_team_dir(target: Path) -> Path:
    team_dir = target.resolve() / TEAM_DIR_NAME
    if not team_dir.is_dir():
        typer.echo(f"agent-team: {team_dir} not found — run `agent-team init` first.", err=True)
        raise typer.Exit(2)
    return team_dir


def _skill_dir(team_dir: Path, name: str, agent: Optional[str]) -> Path:
    if agent is None:
        return team_dir / "skills" / name
    return team_dir / "agents" / agent / "skills" / name


@app.command(help="Create a new skill (shared by default; --agent for agent-private).")
def create(
    name: Annotated[str, typer.Argument(help="kebab-case identifier (e.g. `slack`).")],
    agent: Annotated[Optional[str], typer.Option("--agent", help="Scope under one agent.")] = None,
    target: Annotated[Path, typer.Option(help="Repo root.")] = Path.cwd(),
) -> None:
    _check_kebab(name, "skill name")
    team_dir = _resolve_team_dir(target)

    if agent is not None:
        _check_kebab(agent, "agent name")
        agent_dir = team_dir / "agents" / agent
        if not agent_dir.is_dir():
            typer.echo(f"agent-team: agent dir not found: {agent_dir}", err=True)
            raise typer.Exit(2)

    skill_dir = _skill_dir(team_dir, name, agent)
    if skill_dir.exists():
        typer.echo(f"agent-team: skill already exists: {skill_dir}", err=True)
        raise typer.Exit(1)
    skill_dir.mkdir(parents=True)
    title = name.replace("-", " ").title()
    (skill_dir / "SKILL.md").write_text(SKILL_TEMPLATE.format(name=name, title=title))
    typer.echo(f"  + {(skill_dir / 'SKILL.md').relative_to(team_dir.parent)}")
    if agent is not None:
        typer.echo(f"\nSkill `{name}` scaffolded under agent `{agent}` (auto-included via that agent's local skills/).")
    else:
        typer.echo(f"\nSkill `{name}` scaffolded as a shared skill. Reference it from any agent's config.toml: [skills].extra = ['{name}'].")


@app.command(name="ls", help="List skills (shared by default; --agent for one agent's private skills).")
def list_skills(
    agent: Annotated[Optional[str], typer.Option("--agent", help="List one agent's private skills.")] = None,
    target: Annotated[Path, typer.Option(help="Repo root.")] = Path.cwd(),
) -> None:
    team_dir = _resolve_team_dir(target)
    if agent is None:
        skills_root = team_dir / "skills"
    else:
        skills_root = team_dir / "agents" / agent / "skills"
    if not skills_root.is_dir():
        typer.echo("(no skills)")
        return
    names = sorted(d.name for d in skills_root.iterdir()
                   if d.is_dir() and (d / "SKILL.md").is_file())
    if not names:
        typer.echo("(no skills)")
        return
    for n in names:
        typer.echo(n)


@app.command(help="Remove a skill.")
def rm(
    name: Annotated[str, typer.Argument(help="Skill name.")],
    agent: Annotated[Optional[str], typer.Option("--agent", help="Remove from one agent's private skills.")] = None,
    target: Annotated[Path, typer.Option(help="Repo root.")] = Path.cwd(),
    force: Annotated[bool, typer.Option("--force", "-f", help="Skip confirmation.")] = False,
) -> None:
    team_dir = _resolve_team_dir(target)
    skill_dir = _skill_dir(team_dir, name, agent)
    if not skill_dir.is_dir():
        typer.echo(f"agent-team: skill not found: {skill_dir}", err=True)
        raise typer.Exit(2)

    if not force and not typer.confirm(f"Remove {skill_dir}?"):
        typer.echo("(aborted)")
        return

    shutil.rmtree(skill_dir)
    typer.echo(f"  removed {skill_dir.relative_to(team_dir.parent)}")
