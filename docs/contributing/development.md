# Development Workflow

This page is for contributors working on `agent-team`.

## Prerequisites

- Go 1.22 or newer
- Node.js for docs
- Python 3.11+ for CI scripts
- `shellcheck` for full CI parity
- the selected LLM runtime for runtime smoke work

## Main Commands

```sh
go run ./cmd/agent-team --help
go build -o bin/agent-team ./cmd/agent-team
go build -o bin/agent-teamd ./cmd/agent-teamd
go test ./...
```

Docs:

```sh
npm install
npm run docs:dev
npm run docs:build
```

## Smoke Test

```sh
go build -o bin/agent-team ./cmd/agent-team
python3 scripts/ci/smoke_init.py bin/agent-team
```

The smoke covers init, template rendering, doctor, and daemon behavior.

## Branch and Commit Style

Use meaningful ticket-oriented branches when possible:

```sh
git switch -c codex/squ-42-job-recovery
```

Commit messages generally follow:

```text
feat(job): add scoped queue recovery
fix(cli): handle missing status file
docs: explain daemon lifecycle
chore: update release config
```

Agent-authored commits should include:

```text
Co-authored-by: Codex Opus 4.7 (1M context) <noreply@anthropic.com>
```

## Development Principles

- Keep runtime dependencies minimal.
- Prefer file-backed structured state.
- Preserve CLI dry-runs for mutating workflows.
- Add JSON output for script-facing commands.
- Scope commands by job/team when ownership is known.
- Keep daemon and CLI boundaries explicit.
- Do not add a database without a strong reason.
- Avoid hiding state in global directories.

## Adding a Command

1. Add the Cobra command under `internal/cli`.
2. Keep command creation in the relevant top-level file.
3. Put pure logic in helpers that can be unit-tested.
4. Add text output tests where human output includes action hints.
5. Add JSON tests where scripts may consume output.
6. Add dry-run behavior before destructive mutations.
7. Update docs and README command maps.

## Adding Daemon Behavior

1. Update `internal/daemon`.
2. Add API-level tests if an endpoint changes.
3. Add CLI client tests if user-facing commands depend on it.
4. Ensure metadata survives daemon restart where applicable.
5. Verify local fallback behavior when daemon is down.
6. Add smoke coverage for socket-bound behavior when appropriate.

## Adding File State

When adding a new state file:

- choose TOML for human-authored or human-inspected config
- choose JSON for structured machine records
- choose JSONL for append-only event streams
- add doctor/validation behavior where corruption is likely
- add snapshot coverage if it is useful for handoff
- add prune/drop behavior if it can grow without bound

## Documentation Workflow

Source lives in `docs/` and builds with VitePress.

```sh
npm run docs:build
```

Add documentation when changing:

- command behavior
- file formats
- daemon API
- topology schema
- job/queue semantics
- operator recovery flows
- use cases

Do not rely on README alone for developer-facing architecture changes.
