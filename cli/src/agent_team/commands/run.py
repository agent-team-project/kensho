"""`agent-team run <agent>` — launch a Claude Code session as the named agent.

The launched claude session IS the named agent: its prompt becomes the system
prompt (via --append-system-prompt-file). All other agents stay registered as
subagents so the spawned agent can dispatch them via the Task tool.

Per-instance state at .agent_team/state/<instance>/. Defaults the instance
name to the agent name; pass --name to give it a unique identifier.
"""

from __future__ import annotations

import json
import os
import subprocess
import tempfile
from pathlib import Path
from typing import Annotated, Optional

import typer

from agent_team.loader import (
    AgentLoadError,
    TEAM_DIR_NAME,
    load_all_agents,
    union_skills,
)


def register(app: typer.Typer) -> None:
    @app.command(
        name="run",
        help=(
            "Launch a Claude Code session as the named agent. The agent's prompt becomes "
            "the system prompt; all other agents are still registered as subagents so this "
            "agent can dispatch them. Pass `--name` to give the instance a unique identifier "
            "(state dir: .agent_team/state/<name>/). Forward extra args to claude after `--`."
        ),
        context_settings={"allow_extra_args": True, "ignore_unknown_options": True},
    )
    def run(
        ctx: typer.Context,
        agent: Annotated[str, typer.Argument(help="Agent name — directory under .agent_team/agents/.")],
        name: Annotated[Optional[str], typer.Option("--name", "-n",
            help="Instance name (defaults to the agent name). State dir: .agent_team/state/<name>/.")] = None,
        prompt: Annotated[Optional[str], typer.Option("--prompt", "-p",
            help="Kickoff message. With this, claude runs in one-shot mode; without, interactive.")] = None,
        target: Annotated[Path, typer.Option(help="Repo root.")] = Path.cwd(),
    ) -> None:
        target = target.resolve()
        team_dir = target / TEAM_DIR_NAME
        if not team_dir.is_dir():
            typer.echo(f"agent-team: {team_dir} not found — run `agent-team init` first.", err=True)
            raise typer.Exit(2)

        try:
            agents = load_all_agents(team_dir)
        except AgentLoadError as e:
            typer.echo(f"agent-team: {e}", err=True)
            raise typer.Exit(1)

        chosen = next((a for a in agents if a.name == agent), None)
        if chosen is None:
            available = ", ".join(a.name for a in agents) or "(none)"
            typer.echo(f"agent-team: agent `{agent}` not found. Available: {available}", err=True)
            raise typer.Exit(2)

        try:
            skill_paths = union_skills(agents)
        except AgentLoadError as e:
            typer.echo(f"agent-team: {e}", err=True)
            raise typer.Exit(1)

        instance = name or agent
        state_dir = team_dir / "state" / instance
        state_dir.mkdir(parents=True, exist_ok=True)

        agents_json = {a.name: {"description": a.description, "prompt": a.prompt} for a in agents}

        forwarded = list(ctx.args)
        if forwarded and forwarded[0] == "--":
            forwarded = forwarded[1:]

        with tempfile.TemporaryDirectory(prefix="agent-team-") as tmpdir_str:
            tmpdir = Path(tmpdir_str)

            skills_root = tmpdir / ".claude" / "skills"
            skills_root.mkdir(parents=True)
            for sname, spath in skill_paths.items():
                (skills_root / sname).symlink_to(spath)

            kickoff = (
                f"You are the `{instance}` instance of the `{agent}` agent.\n"
                f"Your state dir is `{state_dir.relative_to(target)}` "
                f"(absolute: `{state_dir}`).\n\n"
                f"--- agent prompt ---\n\n"
                f"{chosen.prompt}"
            )
            prompt_file = tmpdir / "system_prompt.md"
            prompt_file.write_text(kickoff)

            env = {
                **os.environ,
                "AGENT_TEAM_ROOT": str(team_dir),
                "AGENT_TEAM_INSTANCE": instance,
                "AGENT_TEAM_STATE_DIR": str(state_dir),
            }

            cmd = [
                "claude",
                "--agents", json.dumps(agents_json, separators=(",", ":")),
                "--add-dir", str(tmpdir),
                "--append-system-prompt-file", str(prompt_file),
            ]
            if prompt:
                cmd += ["-p", prompt]
            cmd.extend(forwarded)

            try:
                rc = subprocess.run(cmd, env=env, cwd=str(target)).returncode
            except FileNotFoundError:
                typer.echo("agent-team: `claude` CLI not found in PATH. Install Claude Code first.", err=True)
                raise typer.Exit(127)
            if rc != 0:
                raise typer.Exit(rc)
