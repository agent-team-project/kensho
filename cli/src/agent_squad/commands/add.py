"""`agent-squad add` — scaffold new squad members.

Today: only `add manager <slug>` is wired. Workers are spawned by ticket-manager
on demand; they aren't files you add. Skills can be added by hand in .agent_squad/skills/.
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

MANAGER_TEMPLATE = """\
# {title}

You are a **{slug}** manager — a persistent agent owning the {slug} domain.

Workers are ephemeral; you are not. You hold context across sessions, track
progress against a goal, dispatch workers within your scope, and are the
single point of contact for anything in your domain.

## Scope

(Describe what this manager owns. A feature area, an ongoing initiative, a
recurring responsibility — anything that benefits from continuity.)

## Working memory

Persist anything you'd want a future you to know in this directory:
- `journal.md` — running narrative of decisions and context
- `goals.md` — the durable objective(s) this manager is tracking
- `progress.md` — what's done, what's next

## Dispatching workers

When work fits a worker's shape (one ticket, one PR), invoke `agent-squad:assign-worker`.
Pass the ticket identifier and any scope-specific context the worker should know.

## When to escalate

If a request falls outside your scope, hand back to the human or to ticket-manager
rather than expanding scope silently.
"""


def register(sub: argparse._SubParsersAction) -> None:
    p = sub.add_parser(
        "add",
        help="Add a new squad member (currently: managers).",
        description="Scaffold new squad members. v0.1 supports `add manager <slug>`.",
    )
    inner = p.add_subparsers(dest="kind", required=True, metavar="<kind>")

    mgr = inner.add_parser("manager", help="Scaffold a new manager scope under .agent_squad/managers/<slug>/.")
    mgr.add_argument("slug", help="kebab-case identifier for the manager scope (e.g. `release-quality`).")
    mgr.add_argument("--target", type=Path, default=Path.cwd())
    mgr.set_defaults(func=run_manager)


def run_manager(args: argparse.Namespace) -> int:
    target: Path = args.target.resolve()
    slug: str = args.slug
    if not slug.replace("-", "").isalnum():
        print(f"agent-squad: slug must be kebab-case alnum: {slug!r}", file=sys.stderr)
        return 2

    managers_dir = target / ".agent_squad" / "managers"
    if not managers_dir.is_dir():
        print(f"agent-squad: {managers_dir} not found — run `agent-squad init` first.", file=sys.stderr)
        return 2

    scope_dir = managers_dir / slug
    if scope_dir.exists():
        print(f"agent-squad: manager scope already exists: {scope_dir}", file=sys.stderr)
        return 1

    scope_dir.mkdir(parents=True)
    title = slug.replace("-", " ").title() + " Manager"
    (scope_dir / "CLAUDE.md").write_text(MANAGER_TEMPLATE.format(title=title, slug=slug))
    print(f"  + {scope_dir.relative_to(target)}/CLAUDE.md")
    print(f"\nManager scope `{slug}` ready. Edit {scope_dir / 'CLAUDE.md'} to define the scope.")
    return 0
