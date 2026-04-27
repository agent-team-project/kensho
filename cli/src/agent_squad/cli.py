"""agent-squad — vendor a squad of agents and skills into any repo."""

from __future__ import annotations

import argparse
import sys

from agent_squad import __version__
from agent_squad.commands import add, doctor, init, sync


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="agent-squad",
        description="Vendor a software-engineering agent squad (ticket-manager + managers + workers) into any repo.",
    )
    parser.add_argument("--version", action="version", version=f"agent-squad {__version__}")

    sub = parser.add_subparsers(dest="command", required=True, metavar="<command>")

    init.register(sub)
    sync.register(sub)
    add.register(sub)
    doctor.register(sub)

    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
