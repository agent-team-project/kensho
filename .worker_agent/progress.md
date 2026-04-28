# SQU-21 progress

## Done

- `go.mod` (Go 1.22, Cobra + BurntSushi/toml)
- `cmd/agent-team/main.go` — entrypoint that unwraps `cli.ExitCode` errors
- `embed.go` at module root — `//go:embed all:cli/src/agent_team/template`
- `internal/cli/root.go` — Cobra root, `ExitCode` sentinel
- `internal/cli/init.go` — `init` subcommand mirrors Python init.py byte-for-byte
- `internal/loader/frontmatter.go` — port of `_parse_yaml_subset` (no PyYAML/yaml.v3 dep)
- `internal/loader/loader.go` — `Agent`, `LoadAgent`, `LoadAllAgents`, `ResolveSkills`, `UnionSkills`
- Tests: `internal/loader/{frontmatter,loader}_test.go`, `internal/cli/init_test.go` (21 tests, all passing)
- `scripts/ci/smoke_init_go.py` — narrow Go-init smoke
- `.github/workflows/ci.yml` — added `go` job (vet + test + build + smoke)
- `.gitignore` — `bin/`

## Verification

- `go vet ./...` clean
- `go test ./...` 21 tests pass
- `go build -o bin/agent-team-go ./cmd/agent-team` succeeds (5.5MB binary)
- `python3 scripts/ci/smoke_init_go.py bin/agent-team-go` → OK
- `python3 scripts/ci/smoke_init.py` (Python smoke) still OK
- Validators (TOML, frontmatter) clean
- Byte-equivalence: `diff -r /tmp/parity-py /tmp/parity-go` exits 0; stdout differs only in target path

## Remaining

- Commit, push, open PR, monitor CI.
