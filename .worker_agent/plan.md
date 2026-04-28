# SQU-21 plan — Go skeleton + loader + init + CI

## Scope (A only)

Port `agent-team init` and `loader.py` to Go, alongside the existing Python CLI. No `run`, no `doctor`, no resource verbs, no `template` verb. Python untouched.

## Repo additions

```
go.mod
go.sum
cmd/agent-team/main.go              # entrypoint
internal/cli/root.go                # Cobra root command + version
internal/cli/init.go                # `init` subcommand
internal/loader/loader.go           # Agent type, load_agent, load_all_agents, resolve_skills, union_skills
internal/loader/frontmatter.go      # YAML frontmatter mini-parser (scalar + block scalar)
internal/loader/loader_test.go
internal/loader/frontmatter_test.go
internal/template/embed.go          # //go:embed bundled template
scripts/ci/smoke_init_go.py         # narrow Go-init smoke (mirrors Python init checks only)
```

CI extension: add a Go job to `.github/workflows/ci.yml` running `go vet`, `go test ./...`, `go build -o bin/agent-team-go ./cmd/agent-team`, then `python3 scripts/ci/smoke_init_go.py` against the built binary.

## Dependencies

- `github.com/spf13/cobra` — command framework. Approved.
- `github.com/BurntSushi/toml` — TOML parsing for `config.toml` skill resolution.
- `gopkg.in/yaml.v3` — listed in ticket. **But:** the existing Python `loader.py` uses a stdlib mini-parser, not PyYAML. Frontmatter only needs scalars + block scalars. Pulling `yaml.v3` for that is overkill. **Decision: port the Python mini-parser to Go (no `yaml.v3` dep).** Keeps minimal-deps policy intact and matches the Python behavior bit-for-bit. If the manager wants `yaml.v3` for future-proofing I'll add it, but the ticket just listed it as a candidate, and parity is the spec.
- Go ≥1.22 (for `//go:embed`-of-symlinks-aware behavior + range-over-int; 1.21 also fine).

Lock minimum to 1.22.

## Loader port — semantic mapping

Python → Go:

| Python | Go |
|---|---|
| `class Agent` (slots: name, description, prompt, skills) | `type Agent struct { Name, Description, Prompt string; Skills map[string]string }` |
| `class AgentLoadError(RuntimeError)` | sentinel/wrapped errors via `fmt.Errorf("%w", ...)`; tests assert on string content for parity with Python's runtime errors |
| `load_agent(agent_dir, team_dir)` | `LoadAgent(agentDir, teamDir string) (*Agent, error)` |
| `load_all_agents(team_dir)` | `LoadAllAgents(teamDir string) ([]*Agent, error)` — sorted by `agentDir.Name()` |
| `resolve_skills(agent_dir, team_dir)` | `ResolveSkills(agentDir, teamDir string) (map[string]string, error)` |
| `union_skills(agents)` | `UnionSkills(agents []*Agent) (map[string]string, error)` |
| `parse_frontmatter(text)` | `ParseFrontmatter(text string) (map[string]string, string)` |

Skill paths: Python uses `pathlib.Path.resolve()` (canonical absolute, follows symlinks). Go equivalent: `filepath.EvalSymlinks(filepath.Clean(absPath))`. Need to match because we compare paths for collisions.

Sort: Python's `sorted(p for p in agents_dir.iterdir())` sorts by `Path` repr, which is the absolute path string. Go: `sort.Slice` by name. Python on a single dir will sort by basename (Path comparisons compare component-wise; siblings differ only in basename). Go sorting by basename matches.

Local skills iteration (`local_root.iterdir()`): Python sorts. Mirror in Go.

`config.toml` `[skills].extra` / `disable`: read with BurntSushi's `toml.DecodeFile` into a struct with `Skills.Extra []string` / `Disable []string`. Robust to extra keys.

Skill ref resolution rule (verbatim from Python):

```python
if "/" in spec or spec.startswith("."):
    path = (agent_dir / spec).resolve()
else:
    path = (shared_root / spec).resolve()
```

Implement identically.

## Frontmatter parser — semantic mapping

Direct port of `_parse_yaml_subset`:

- Lines starting with space, tab, or `-` → skip.
- Empty / `#` comment lines → skip.
- No `:` → skip.
- Key: value (strip both).
- `value == "|"` → block scalar: collect indented lines, strip first non-empty line's indent as base, dedent, rstrip newlines.
- Quoted (single or double) values: strip quotes.

Tests cover: scalar only, block scalar only, both mixed, missing close `\n---\n`, no opening `---\n`, blank lines inside block scalar.

## Init command — semantic mapping

Python `init.py`:

1. Validate `--template` ∈ {default, empty}; else exit 2.
2. Resolve target. If not dir, exit 2.
3. Print `Vendoring team into <team_dir>`.
4. If empty: write `agents/`, `skills/` empty dirs (echo `+ .agent_team/agents/`, `+ .agent_team/skills/`).
5. Else copy template root → `<team_dir>`, **skipping** `config.toml.example`. For each child:
   - If target exists and not `--force`: echo `skip <rel> (already exists; --force to overwrite)`, continue.
   - If dir: `rmtree` if exists, then `copytree`.
   - If file: `copy2`.
   - Echo `+ <rel-from-team-parent>` (relative path includes `.agent_team/`).
6. Write config: if `<team_dir>/config.toml` exists, echo `keep <rel> (untouched)`; else copy `config.toml.example` → both `config.toml.example` and `config.toml` (default), or write `EMPTY_CONFIG` literal (empty).
7. Echo blank line + Done. Next steps... (same wording).

Go `go:embed` reads the embedded FS, walks it, writes to disk preserving the relative tree. The relative-path printing must yield e.g. `.agent_team/agents/` exactly — use `filepath.Rel(team_dir.parent, target_path)` style.

The Python `Path.relative_to(team_dir.parent)` produces e.g. `.agent_team/agents`. Note this requires `team_dir.parent` to exist as a real ancestor — the Python output uses POSIX `/` separators on Linux/Mac. Go's `filepath.Rel` uses OS separators; for cross-platform CI parity we'll use `/` in echoed paths (`filepath.ToSlash`).

For `_write_empty`: Python prints `+ .agent_team/agents/` and `+ .agent_team/skills/` — note the trailing `/` and use of `dst.name`. Mirror exactly.

## CI changes

`.github/workflows/ci.yml`:

- Add a `go-build-and-test` job, parallel to `validate`, with `actions/setup-go@v5` (`go-version: "1.22"`).
- Steps: `go vet ./...`, `go test ./...`, `go build -o bin/agent-team-go ./cmd/agent-team`, `python3 scripts/ci/smoke_init_go.py bin/agent-team-go`.
- Existing Python jobs untouched.

`scripts/ci/smoke_init_go.py`: takes the Go binary path as `sys.argv[1]`, runs `init --target <tmp>`, asserts the same `EXPECTED_AFTER_INIT` paths exist, validates `config.toml` parses as TOML. **Does not** test `agent create`, `skill create`, `doctor` — those are Python-only for now.

## Byte-equivalence verification

Acceptance criterion: `bin/agent-team-go init --target /tmp/foo` produces a tree byte-equivalent to `agent-team init --target /tmp/foo`. Will run both locally before opening PR using `diff -r` (and `find` checksums). Differences allowed: file mtimes, permissions (Python `shutil.copy2` preserves mtimes; Go `embed.FS` does not — so atimes/mtimes will differ; test only file *content* equality + tree shape).

## Out of scope (do not bleed)

- `run`, `doctor`, `agent`, `skill`, `instance` subcommands.
- The `template` verb / templates-as-images / parameter substitution. Not happening here.
- Renaming `bin/agent-team-go` → `bin/agent-team`. Ticket C.
- Deleting Python.

## Open question I'm answering myself

> "Decide whether `go:embed` reads from `cli/src/agent_team/template/` directly, or relocates."

Per manager: stay at `cli/src/agent_team/template/`. Relocation deferred to SQU-23.
