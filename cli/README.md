# agent-squad CLI

The `agent-squad` Python package — distributes the squad as a CLI.

This directory is package internals. User-facing docs live in the [repo root README](../README.md) and [`documentation/`](../documentation/).

## Layout

```
cli/
├── pyproject.toml
└── src/agent_squad/
    ├── cli.py                 # argparse entrypoint (`agent-squad`)
    ├── commands/              # init, sync, add, doctor
    └── template/              # bundled — copied into <consumer>/.agent_squad/ on init
        ├── agents/            # ticket-manager.md, worker.md, manager.md
        ├── skills/            # linear, pull-request, assign-worker
        ├── scripts/           # linear-graphql.sh
        ├── managers/          # convention dir for per-manager scopes
        └── config.toml.example
```

## Local dev

From this directory:

```sh
uv run --with-editable . agent-squad --help
```

Or install editably and run:

```sh
uv pip install -e .
agent-squad --help
```
