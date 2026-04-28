"""`agent-team agent ...` — manage agent definitions."""

from __future__ import annotations

import shutil
from pathlib import Path
from typing import Annotated

import typer

from agent_team.loader import AgentLoadError, TEAM_DIR_NAME, load_agent

AGENT_TEMPLATE = """\
---
description: |
  TODO — what this agent does and when to invoke it. This becomes the agent's
  description in the --agents JSON Claude Code uses for routing.
---

# {title}

You are the `{name}` agent. TODO: describe your role, your scope, your
critical rules, and your workflow.

## Skills you have

This agent's skills are declared in ./config.toml under [skills].extra plus
any local skills under ./skills/.
"""

AGENT_CONFIG_TEMPLATE = """\
# Skills available to the `{name}` agent at runtime.
#
# Local skills under ./skills/ are auto-included.
# Pull in shared skills (under ../../skills/) by name, or anywhere by path:
[skills]
extra = []
# disable = []   # opt-out from local defaults if needed
"""

app = typer.Typer(
    help="Manage agent definitions (.agent_team/agents/<name>/).",
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


@app.command(help="Create a new agent definition at .agent_team/agents/<name>/.")
def create(
    name: Annotated[str, typer.Argument(help="kebab-case identifier (e.g. `reviewer`).")],
    target: Annotated[Path, typer.Option(help="Repo root.")] = Path.cwd(),
) -> None:
    _check_kebab(name, "agent name")
    team_dir = _resolve_team_dir(target)

    agent_dir = team_dir / "agents" / name
    if agent_dir.exists():
        typer.echo(f"agent-team: agent already exists: {agent_dir}", err=True)
        raise typer.Exit(1)
    agent_dir.mkdir(parents=True)
    title = name.replace("-", " ").title()
    (agent_dir / "agent.md").write_text(AGENT_TEMPLATE.format(name=name, title=title))
    (agent_dir / "config.toml").write_text(AGENT_CONFIG_TEMPLATE.format(name=name))
    typer.echo(f"  + {(agent_dir / 'agent.md').relative_to(team_dir.parent)}")
    typer.echo(f"  + {(agent_dir / 'config.toml').relative_to(team_dir.parent)}")
    typer.echo(f"\nAgent `{name}` scaffolded. Edit {agent_dir / 'agent.md'} to write its prompt.")


@app.command(name="ls", help="List all agents.")
def list_agents(
    target: Annotated[Path, typer.Option(help="Repo root.")] = Path.cwd(),
) -> None:
    team_dir = _resolve_team_dir(target)
    agents_dir = team_dir / "agents"
    if not agents_dir.is_dir():
        typer.echo("(no agents)")
        return

    rows: list[tuple[str, str, str]] = []
    for d in sorted(p for p in agents_dir.iterdir() if p.is_dir()):
        try:
            agent = load_agent(d, team_dir)
            desc = agent.description.split("\n", 1)[0]
            if len(desc) > 60:
                desc = desc[:57] + "..."
            skills_str = ", ".join(sorted(agent.skills.keys())) or "(none)"
            rows.append((agent.name, skills_str, desc))
        except AgentLoadError as e:
            rows.append((d.name, "ERROR", str(e)))

    if not rows:
        typer.echo("(no agents — run `agent-team agent create <name>`)")
        return

    name_w = max(len(r[0]) for r in rows + [("NAME", "", "")])
    skills_w = max(len(r[1]) for r in rows + [("", "SKILLS", "")])
    typer.echo(f"{'NAME':<{name_w}}  {'SKILLS':<{skills_w}}  DESCRIPTION")
    for n, s, d in rows:
        typer.echo(f"{n:<{name_w}}  {s:<{skills_w}}  {d}")


@app.command(help="Show details about one agent.")
def show(
    name: Annotated[str, typer.Argument(help="Agent name.")],
    target: Annotated[Path, typer.Option(help="Repo root.")] = Path.cwd(),
) -> None:
    team_dir = _resolve_team_dir(target)
    agent_dir = team_dir / "agents" / name
    if not agent_dir.is_dir():
        typer.echo(f"agent-team: agent not found: {agent_dir}", err=True)
        raise typer.Exit(2)
    try:
        agent = load_agent(agent_dir, team_dir)
    except AgentLoadError as e:
        typer.echo(f"agent-team: {e}", err=True)
        raise typer.Exit(1)

    typer.echo(f"agent:    {agent.name}")
    typer.echo(f"path:     {agent_dir.relative_to(team_dir.parent)}/")
    typer.echo("")
    typer.echo("description:")
    for line in agent.description.split("\n"):
        typer.echo(f"  {line}")
    typer.echo("")
    typer.echo("skills:")
    if agent.skills:
        for sname, spath in sorted(agent.skills.items()):
            try:
                rel = spath.relative_to(team_dir.parent)
                typer.echo(f"  - {sname}  →  {rel}")
            except ValueError:
                typer.echo(f"  - {sname}  →  {spath}")
    else:
        typer.echo("  (none)")
    typer.echo("")
    typer.echo(f"prompt:   {len(agent.prompt.splitlines())} lines, {len(agent.prompt)} chars")


@app.command(help="Remove an agent definition.")
def rm(
    name: Annotated[str, typer.Argument(help="Agent name.")],
    target: Annotated[Path, typer.Option(help="Repo root.")] = Path.cwd(),
    force: Annotated[bool, typer.Option("--force", "-f", help="Skip confirmation.")] = False,
) -> None:
    team_dir = _resolve_team_dir(target)
    agent_dir = team_dir / "agents" / name
    if not agent_dir.is_dir():
        typer.echo(f"agent-team: agent not found: {agent_dir}", err=True)
        raise typer.Exit(2)

    if not force and not typer.confirm(f"Remove {agent_dir}?"):
        typer.echo("(aborted)")
        return

    shutil.rmtree(agent_dir)
    typer.echo(f"  removed {agent_dir.relative_to(team_dir.parent)}")
