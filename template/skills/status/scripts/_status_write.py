#!/usr/bin/env python3
"""Write `$AGENT_TEAM_STATE_DIR/status.toml` atomically.

Helper for the `status` skill. The bash dispatcher (`status.sh`) parses
arguments and re-invokes this script with state passed via environment
variables — that keeps the bash side stdlib-only-ish (no `jq`/`tomljq`
dependencies) and gives this script a clean stdlib-only Python surface.

Reads the existing file (if any), merges in the new fields, and writes via
`tmp + rename` so a concurrent reader (`agent-team instance ps`) never sees
a partially-written file.

The schema is documented in `documentation/orchestrator.md` § "Instance
status / observability".
"""

from __future__ import annotations

import datetime as dt
import os
import sys
import tomllib
from pathlib import Path


VALID_PHASES = {"planning", "implementing", "awaiting_review", "blocked", "idle", "done"}


def main() -> int:
    state_dir = Path(os.environ["AGENT_TEAM_STATE_DIR"])
    state_dir.mkdir(parents=True, exist_ok=True)
    target = state_dir / "status.toml"
    tmp = state_dir / "status.toml.tmp"

    existing = _load(target)
    verb = os.environ["STATUS_VERB"]

    if verb == "set":
        new = _apply_set(existing)
    elif verb == "block":
        new = _apply_block(existing)
    elif verb == "clear-block":
        new = _apply_clear_block(existing)
    else:
        print(f"_status_write.py: unknown verb: {verb}", file=sys.stderr)
        return 2

    body = _serialize(new)
    tmp.write_text(body, encoding="utf-8")
    os.replace(tmp, target)
    return 0


def _load(path: Path) -> dict:
    if not path.exists():
        return {}
    try:
        with path.open("rb") as fh:
            return tomllib.load(fh)
    except tomllib.TOMLDecodeError:
        # Corrupt file — start fresh rather than propagating; this skill is
        # the only writer, so a malformed file means someone hand-edited or
        # an earlier crash truncated it. Either way, overwrite.
        return {}


def _now() -> str:
    # ISO-8601 UTC, second precision, with `Z` suffix.
    return dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def _apply_set(existing: dict) -> dict:
    phase = os.environ["STATUS_PHASE"]
    if phase not in VALID_PHASES or phase == "blocked":
        # Bash already validates, but defence in depth.
        print(f"_status_write.py: invalid phase for `set`: {phase}", file=sys.stderr)
        sys.exit(2)

    out = dict(existing)
    status = dict(out.get("status", {}))
    work = dict(out.get("work", {}))

    prior_phase = status.get("phase")
    status["phase"] = phase
    if prior_phase != phase or "since" not in status:
        status["since"] = _now()

    desc = os.environ.get("STATUS_DESC", "")
    if desc:
        status["description"] = desc
    last_action = os.environ.get("STATUS_LAST_ACTION", "")
    if last_action:
        status["last_action"] = last_action

    for envk, tomlk in (
        ("STATUS_TICKET", "ticket"),
        ("STATUS_PR", "pr"),
        ("STATUS_BRANCH", "branch"),
    ):
        v = os.environ.get(envk, "")
        if v:
            work[tomlk] = v

    out["status"] = status
    if work:
        out["work"] = work
    # Leaving an existing [blocking] section in place when entering a non-
    # blocked phase would lie to readers. `set` clears it.
    out.pop("blocking", None)
    return out


def _apply_block(existing: dict) -> dict:
    out = dict(existing)
    status = dict(out.get("status", {}))

    prior_phase = status.get("phase")
    if prior_phase != "blocked":
        # Stash the prior phase so `clear-block` can restore it.
        status["resume_phase"] = prior_phase or "idle"
        status["since"] = _now()
    status["phase"] = "blocked"

    out["status"] = status
    out["blocking"] = {
        "reason": os.environ["STATUS_REASON"],
        "ask_to": os.environ["STATUS_ASK"],
    }
    return out


def _apply_clear_block(existing: dict) -> dict:
    out = dict(existing)
    status = dict(out.get("status", {}))
    resume = status.pop("resume_phase", None) or "idle"
    if status.get("phase") == "blocked":
        status["phase"] = resume
        status["since"] = _now()
    out["status"] = status
    out.pop("blocking", None)
    return out


def _serialize(data: dict) -> str:
    """Hand-write TOML. The schema is small and fixed-shape, so we don't pull
    in a third-party encoder. `tomllib` (Python ≥ 3.11) reads but does not
    write; `tomli_w` would be the dep otherwise."""
    sections = []

    status = data.get("status") or {}
    if status:
        # Stable key order so diffs are clean.
        order = ["phase", "description", "since", "last_action", "resume_phase"]
        sections.append(_format_section("status", status, order))

    work = data.get("work") or {}
    if work:
        order = ["ticket", "pr", "branch"]
        sections.append(_format_section("work", work, order))

    blocking = data.get("blocking") or {}
    if blocking:
        order = ["reason", "ask_to"]
        sections.append(_format_section("blocking", blocking, order))

    return "\n\n".join(sections) + "\n"


def _format_section(name: str, body: dict, order: list[str]) -> str:
    lines = [f"[{name}]"]
    keys = [k for k in order if k in body] + [k for k in body if k not in order]
    for k in keys:
        lines.append(f"{k} = {_format_value(body[k])}")
    return "\n".join(lines)


def _format_value(v) -> str:
    if isinstance(v, str):
        # TOML basic strings: backslash and double-quote need escaping.
        escaped = v.replace("\\", "\\\\").replace('"', '\\"')
        # Drop control chars that would need \uXXXX escapes — the schema's
        # values are all human descriptions, not arbitrary blobs.
        escaped = "".join(c for c in escaped if c == "\t" or ord(c) >= 0x20)
        return f'"{escaped}"'
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, (int, float)):
        return str(v)
    raise TypeError(f"unexpected value type for status.toml: {type(v).__name__}")


if __name__ == "__main__":
    sys.exit(main())
