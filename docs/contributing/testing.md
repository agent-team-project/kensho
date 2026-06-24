# Testing Strategy

`agent-team` has a large CLI surface and significant file-backed behavior. Tests should prove both structured output and operator-facing text where relevant.

## Test Layers

| Layer | What to test |
| --- | --- |
| Unit | Pure parsers, normalizers, selectors, state transitions |
| CLI | Cobra commands, validation, text output, JSON output |
| Daemon | HTTP endpoints, queue behavior, scheduler, lifecycle metadata |
| Integration-style | Fake spawners, temp repos, socket tests |
| Smoke | Init, doctor, daemon start/stop, end-to-end CLI flows |

## Standard Validation

```sh
go test -count=1 -p 1 ./...
go vet ./...
git diff --check
go build -o bin/agent-team ./cmd/agent-team
go build -o bin/agent-teamd ./cmd/agent-teamd
python3 scripts/ci/smoke_init.py bin/agent-team
npm run docs:build
```

Use `-p 1` for the full suite when socket or temp filesystem tests may contend.

## CLI Test Pattern

Most CLI tests use:

1. temp repo
2. `initInto(t, tmp)`
3. write fixture files
4. execute `NewRootCmd()`
5. assert stdout/stderr/exit code

Test both JSON and text output when:

- action hints are user-facing
- scripts may consume the output
- command behavior differs by flag

## Daemon Tests

Daemon tests should cover:

- metadata persistence
- process lifecycle
- queue persistence
- queue retry and dead-letter behavior
- scheduler state
- event resolution
- mailbox and logs
- API validation

Use fake spawners where possible to avoid relying on an actual LLM runtime.

## Queue Tests

Queue features need strong tests because they are recovery-critical.

Cover:

- pending/dead state
- retries and attempts
- dead-letter movement
- daemon restart recovery
- corrupt file handling
- quarantine list/show/restore/drop
- job-scoped ownership
- team-scoped ownership
- dry-run behavior

## Job Tests

Job tests should cover:

- id normalization
- TOML read/write
- events
- create/list/show
- dispatch payloads
- status-file reconciliation
- blocked/unblock flow
- queue ownership
- quarantine ownership
- cleanup previews
- pipeline step state
- triage reasons and actions

## Docs Tests

Docs must build:

```sh
npm run docs:build
```

The build catches dead internal links, invalid frontmatter, and broken VitePress config.

## Smoke Scenarios Worth Keeping

When adding significant orchestration behavior, prefer a direct smoke in a temp repo:

```sh
TMP=$(mktemp -d)
bin/agent-team init --target "$TMP" \
  --set linear.team_id=00000000-0000-0000-0000-000000000000 \
  --set linear.ticket_prefix=SMK

bin/agent-team --repo "$TMP" daemon start
bin/agent-team --repo "$TMP" overview
bin/agent-team --repo "$TMP" daemon stop
```

For job/queue behavior, inject representative queue files and assert CLI output.

## When to Broaden Coverage

Broaden tests when touching:

- shared selectors
- topology matching
- queue ownership
- job status transitions
- repair loops
- team scoping
- daemon metadata
- output schemas

Keep tests focused for narrow display-only changes.
