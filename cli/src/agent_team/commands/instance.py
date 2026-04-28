"""`agent-team instance ...` — manage agent instance state at .agent_team/state/<instance>/."""

from __future__ import annotations

import shutil
from pathlib import Path
from typing import Annotated

import typer

from agent_team.loader import TEAM_DIR_NAME

app = typer.Typer(
    help="Manage agent instance state (.agent_team/state/<instance>/).",
    no_args_is_help=True,
)


def _resolve_team_dir(target: Path) -> Path:
    team_dir = target.resolve() / TEAM_DIR_NAME
    if not team_dir.is_dir():
        typer.echo(f"agent-team: {team_dir} not found — run `agent-team init` first.", err=True)
        raise typer.Exit(2)
    return team_dir


@app.command(name="ls", help="List instances (state dirs).")
def list_instances(
    target: Annotated[Path, typer.Option(help="Repo root.")] = Path.cwd(),
) -> None:
    team_dir = _resolve_team_dir(target)
    state_root = team_dir / "state"
    if not state_root.is_dir():
        typer.echo("(no instances)")
        return
    names = sorted(p.name for p in state_root.iterdir() if p.is_dir())
    if not names:
        typer.echo("(no instances)")
        return
    for n in names:
        typer.echo(n)


@app.command(help="Show an instance's state files.")
def show(
    name: Annotated[str, typer.Argument(help="Instance name.")],
    target: Annotated[Path, typer.Option(help="Repo root.")] = Path.cwd(),
) -> None:
    team_dir = _resolve_team_dir(target)
    state_dir = team_dir / "state" / name
    if not state_dir.is_dir():
        typer.echo(f"agent-team: instance not found: {state_dir}", err=True)
        raise typer.Exit(2)
    typer.echo(f"instance: {name}")
    typer.echo(f"path:     {state_dir.relative_to(team_dir.parent)}/")
    typer.echo("")
    files = sorted(state_dir.iterdir())
    if not files:
        typer.echo("(empty)")
        return
    typer.echo("files:")
    for f in files:
        if f.is_file():
            typer.echo(f"  {f.name}  ({f.stat().st_size} bytes)")
        elif f.is_dir():
            typer.echo(f"  {f.name}/  (dir)")


@app.command(help="Remove an instance's state.")
def rm(
    name: Annotated[str, typer.Argument(help="Instance name.")],
    target: Annotated[Path, typer.Option(help="Repo root.")] = Path.cwd(),
    force: Annotated[bool, typer.Option("--force", "-f", help="Skip confirmation.")] = False,
) -> None:
    team_dir = _resolve_team_dir(target)
    state_dir = team_dir / "state" / name
    if not state_dir.is_dir():
        typer.echo(f"agent-team: instance not found: {state_dir}", err=True)
        raise typer.Exit(2)

    if not force and not typer.confirm(f"Remove {state_dir}?"):
        typer.echo("(aborted)")
        return

    shutil.rmtree(state_dir)
    typer.echo(f"  removed {state_dir.relative_to(team_dir.parent)}")
