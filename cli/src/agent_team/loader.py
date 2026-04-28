"""Load agents and resolve skills from a `.agent_team/` tree.

Pure logic — no I/O beyond reading from disk; no Typer or subprocess. Used by
the `run`, `doctor`, and `agent` commands.
"""

from __future__ import annotations

import tomllib
from pathlib import Path

TEAM_DIR_NAME = ".agent_team"


class AgentLoadError(RuntimeError):
    pass


class Agent:
    __slots__ = ("name", "description", "prompt", "skills")

    def __init__(self, name: str, description: str, prompt: str, skills: dict[str, Path]):
        self.name = name
        self.description = description
        self.prompt = prompt
        self.skills = skills


def load_agent(agent_dir: Path, team_dir: Path) -> Agent:
    md_path = agent_dir / "agent.md"
    if not md_path.is_file():
        raise AgentLoadError(f"{md_path} missing — every agent dir needs an agent.md")
    fm, body = parse_frontmatter(md_path.read_text())
    description = fm.get("description", "").strip()
    if not description:
        raise AgentLoadError(f"{md_path} has no `description` in frontmatter")
    skills = resolve_skills(agent_dir, team_dir)
    return Agent(name=agent_dir.name, description=description, prompt=body, skills=skills)


def load_all_agents(team_dir: Path) -> list[Agent]:
    agents_dir = team_dir / "agents"
    if not agents_dir.is_dir():
        raise AgentLoadError(f"{agents_dir} not found")
    return [load_agent(d, team_dir)
            for d in sorted(p for p in agents_dir.iterdir() if p.is_dir())]


def resolve_skills(agent_dir: Path, team_dir: Path) -> dict[str, Path]:
    """{skill_name: absolute_path}. Local skills auto-included; `extra` pulls in shared/path-referenced."""
    skills: dict[str, Path] = {}

    local_root = agent_dir / "skills"
    if local_root.is_dir():
        for child in sorted(local_root.iterdir()):
            if child.is_dir() and (child / "SKILL.md").is_file():
                skills[child.name] = child.resolve()

    cfg_path = agent_dir / "config.toml"
    extra: list[str] = []
    disable: list[str] = []
    if cfg_path.is_file():
        cfg = tomllib.loads(cfg_path.read_text())
        skills_cfg = cfg.get("skills", {})
        extra = list(skills_cfg.get("extra", []))
        disable = list(skills_cfg.get("disable", []))

    shared_root = team_dir / "skills"
    for spec in extra:
        if "/" in spec or spec.startswith("."):
            path = (agent_dir / spec).resolve()
        else:
            path = (shared_root / spec).resolve()
        if not path.is_dir() or not (path / "SKILL.md").is_file():
            raise AgentLoadError(
                f"{agent_dir.name}: skill `{spec}` not found at {path} (no SKILL.md)"
            )
        name = path.name
        if name in skills and skills[name] != path:
            raise AgentLoadError(
                f"{agent_dir.name}: skill name `{name}` is already a local skill at "
                f"{skills[name]}; can't also import a different `{spec}`"
            )
        skills[name] = path

    for name in disable:
        skills.pop(name, None)

    return skills


def union_skills(agents: list[Agent]) -> dict[str, Path]:
    """Combine all agents' skills; error on name collision across agents."""
    union: dict[str, Path] = {}
    for agent in agents:
        for name, path in agent.skills.items():
            existing = union.get(name)
            if existing is not None and existing != path:
                raise AgentLoadError(
                    f"skill name `{name}` resolves to two different paths "
                    f"({existing} vs {path}); rename one."
                )
            union[name] = path
    return union


def parse_frontmatter(text: str) -> tuple[dict[str, str], str]:
    """Split a markdown file with `---`-delimited YAML frontmatter into (fm_dict, body).

    Supports the subset of YAML actually used in agent frontmatter: scalar values
    and block scalars (`key: |`). Lists and nested mappings are skipped.
    """
    if not text.startswith("---\n"):
        return {}, text
    end_idx = text.find("\n---\n", 4)
    if end_idx == -1:
        if text.endswith("\n---"):
            return _parse_yaml_subset(text[4:-4]), ""
        return {}, text
    fm_text = text[4:end_idx]
    body = text[end_idx + 5:]
    return _parse_yaml_subset(fm_text), body


def _parse_yaml_subset(text: str) -> dict[str, str]:
    result: dict[str, str] = {}
    lines = text.split("\n")
    i = 0
    while i < len(lines):
        line = lines[i]
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            i += 1
            continue
        if line.startswith((" ", "\t", "-")):
            i += 1
            continue
        if ":" not in line:
            i += 1
            continue
        key, _, val = line.partition(":")
        key = key.strip()
        val = val.strip()
        if val == "|":
            i += 1
            block_lines: list[str] = []
            base_indent: int | None = None
            while i < len(lines):
                ln = lines[i]
                if not ln.strip():
                    block_lines.append("")
                    i += 1
                    continue
                indent = len(ln) - len(ln.lstrip(" "))
                if base_indent is None:
                    if indent == 0:
                        break
                    base_indent = indent
                if indent < base_indent:
                    break
                block_lines.append(ln[base_indent:])
                i += 1
            result[key] = "\n".join(block_lines).rstrip("\n")
            continue
        if (val.startswith('"') and val.endswith('"')) or (val.startswith("'") and val.endswith("'")):
            val = val[1:-1]
        result[key] = val
        i += 1
    return result
