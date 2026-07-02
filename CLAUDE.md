# CLAUDE.md

Contributor orientation for `agent-team`. `README.md` is user-facing; this file is for anyone working *on* the CLI.

## What it is

A Go CLI that:

1. Embeds a "default" template (a starter set of agents + skills + a `template.toml` manifest).
2. On `agent-team init [<ref>] [--set k=v]…`, resolves a template ref (bundled, local path, or cached pull) against supplied parameters and renders it into the consumer's repo at `.agent_team/`.
3. On `agent-team run <agent>`, launches Claude Code with the vendored team registered for the session, via Claude Code's `--agents` and `--add-dir` flags.

Everything is per-repo and file-based. There is no plugin install, no marketplace, no global state. The bundled default template (a software-engineering team — `ticket-manager`, `manager`, `worker`, plus `linear` / `pull-request` / `assign-worker` skills) is one template among many possible. Users are expected to edit, replace, point `init` at a different template ref, or wholly rewrite their own.

## Vocabulary

- **template** — a versioned, parameterized directory tree with a `template.toml` manifest. The default template is `go:embed`'d in the binary; others come from local paths or the on-disk cache (`~/.agent-team/cache/`).
- **agent** — a definition at `.agent_team/agents/<name>/`. Authored, static.
- **instance** — a named runtime spawn of an agent (`--name=` at spawn time). Has its own state at `.agent_team/state/<instance-name>/`. One agent can have many instances.
- **workspace** — an instance's working directory. For ephemeral code-writing agents: a fresh worktree per spawn (Claude Code's `Agent` tool with `isolation: "worktree"`). For others: the repo root.

## Forward-looking architecture

Two design sketches capture where the project is going. Read the relevant one if you're touching code in its area.

- [`documentation/templates.md`](./documentation/templates.md) — full templates-as-images model: parameter declarations, layered config resolution, `upgrade` semantics, worked example. The `template` resource verb (SQU-22), the `init <ref>` flow, and the bundled starter's role as the "default template" all live here. Read before touching `init`, the `template` verb, the loader, or `config.toml` shape.
- [`documentation/orchestrator.md`](./documentation/orchestrator.md) — v1.1+ Go daemon (`agent-teamd`) that owns instance lifecycle, replaces Claude Code's in-session dispatch primitives with an orchestrator-mediated model, and unblocks runtime-agnostic execution. Read before touching the dispatch path or thinking about persistent / restartable instances.
- [`documentation/topology.md`](./documentation/topology.md) — declarative topology (`instances.toml`): which named instances exist, how each is configured, what events trigger each. Schema, layered config, and the event-resolution daemon endpoints landed in SQU-27; read before touching `topology` / `event` / `instance up/down` or extending event types.
- [`documentation/recoverable-managers.md`](./documentation/recoverable-managers.md) — daemon-owned recoverable managers (SQU-44): restart policy, generated catch-up briefs, per-instance launch-env snapshots, Codex session capture + managed resume, transition notifications. Read before touching reconcile, `start`/resume paths, or the managed-resume capability gate.
- [`documentation/operating-model.md`](./documentation/operating-model.md) — field-tested operating defaults (SQU-42/SQU-48): two-plane model, small-jobs safety unit, gate discipline, pipeline shape, reviewer checklists, capacity planning, auth-mode fidelity. Read before changing bundled agent/reviewer prompts or pipeline defaults.

## Repo layout

- `cmd/agent-team/` — binary entrypoint (`main.go`).
- `internal/cli/` — Cobra commands. One file per top-level command (`init`, `run`, `doctor`, `template`, `instance`, `topology`, `event`) plus `root.go`.
- `internal/loader/` — pure logic: parse YAML frontmatter, load agents, resolve skills.
- `internal/template/` — manifest parsing, parameter resolution, `.tmpl` rendering, ref resolver.
- `internal/topology/` — `instances.toml` schema: declared instances, per-instance config overrides, the event-trigger match DSL.
- `internal/daemon/` — `agent-teamd` (orchestrator daemon): instance lifecycle, channel store, mailbox, event resolver.
- `template/` — bundled "default" template content. `go:embed`'d into the binary at module root via `embed.go`.
- `embed.go` — `//go:embed all:template` directive + accessor. Lives at the module root because `go:embed` patterns can't escape the directory of the source file holding the directive.
- `.agent_team/` (this repo) — our own team, since we self-dogfood. `agents/` and `skills/` are symlinks into `template/`.
- `scripts/ci/` — CI validators (frontmatter, TOML) and the init smoke harness. Stdlib Python (3.11+) — no `pyproject.toml`, no installed deps; `pyyaml` is `pip install`'d in CI for the frontmatter validator and is the lone third-party dep.
- `.github/workflows/ci.yml` — single Go job running validators, `go vet`, `go test`, `go build`, smoke, and shellcheck.
- `go.mod` — Go ≥ 1.22. Runtime deps: `cobra` (CLI framework), `BurntSushi/toml` (TOML codec). Resist further runtime deps.

## CLI dev loop

From repo root:

```sh
go run ./cmd/agent-team --help
go build -o bin/agent-team ./cmd/agent-team
go test ./...
```

Smoke-test against a tmp dir (after a build):

```sh
mkdir -p /tmp/team-smoke
bin/agent-team init --target /tmp/team-smoke \
    --set linear.team_id=00000000-0000-0000-0000-000000000000 \
    --set linear.ticket_prefix=SMK
```

The `scripts/ci/smoke_init.py` harness exercises the same path end-to-end:

```sh
go build -o bin/agent-team ./cmd/agent-team
python3 scripts/ci/smoke_init.py bin/agent-team
```

## How `agent-team run <agent>` works

For each `.agent_team/agents/<name>/agent.md`:
1. Split YAML frontmatter from the body. `internal/loader/frontmatter.go` is a stdlib-only mini-parser that handles scalar and block-scalar values (no `gopkg.in/yaml.v3` at runtime).
2. `description` from frontmatter becomes the agent's description; body becomes the agent's prompt.
3. Directory name becomes the agent's name (e.g. `agents/worker/` → subagent `worker`).
4. Skills are resolved: every `<agent>/skills/<name>/SKILL.md` is auto-included; `[skills].extra = ["..."]` in `<agent>/config.toml` pulls in shared skills (looked up under `.agent_team/skills/<name>/`) or arbitrary paths.

The CLI assembles `{name: {description, prompt}, …}` as JSON, builds a tmpdir with `.claude/skills/<name>` symlinks for the union of all referenced skills, writes the chosen agent's prompt + a kickoff preamble (instance name, state dir) to a temp file, creates `.agent_team/state/<instance>/` if missing, and exec's:

```sh
claude --agents '<json>' --add-dir <tmpdir> --append-system-prompt-file <kickoff> <forwarded-args>
```

The launched session IS the named agent (its prompt is the system prompt) AND has every other agent registered as a subagent (so e.g. a spawned `manager` can dispatch a `worker` via the Task tool).

The launcher exports into claude's env:
- `AGENT_TEAM_ROOT` — absolute path to `.agent_team/`
- `AGENT_TEAM_INSTANCE` — the instance name
- `AGENT_TEAM_STATE_DIR` — absolute path to `.agent_team/state/<instance>/`

Skills are picked up by Claude Code's `--add-dir` discovery — see [Skills docs](https://code.claude.com/docs/en/skills) for the directory shape `--add-dir` expects.

`.agent_team/config.toml` is read by skill bash via `python3 -c 'import tomllib; …'`. The CLI does not substitute prompt templates at run time — values flow through the filesystem. (`.tmpl` substitution happens once at `init` time per the templates-as-images model.)

## Self-dogfooding

This repo's `.agent_team/agents` and `.agent_team/skills` are symlinks into `template/`, so edits to template content are immediately live for the next `agent-team run`. If you've broken the wiring, recreate the symlinks by hand or wipe `.agent_team/{agents,skills}` and re-link.

## Releasing

`.goreleaser.yaml` builds both binaries for `darwin/{arm64,amd64}` + `linux/{amd64,arm64}`; `.github/workflows/release.yml` runs goreleaser on `v*` tag push. Dry-run before tagging (produces `dist/`, uploads nothing): `goreleaser release --snapshot --clean --skip=publish`. Cut a real release: `git tag v0.x.0 && git push --tags`. `--version` output is wired to `internal/cli.Version`, which goreleaser overrides via `-ldflags`.

Homebrew publishing is deferred — the tap repo doesn't exist yet. To enable: create `jamesaud/homebrew-agent-team`, mint a PAT with `repo` scope on it, save it as the `HOMEBREW_TAP_GITHUB_TOKEN` secret on this repo, then uncomment the `brews:` block in `.goreleaser.yaml`.

## Contribution rules

### Branches

One branch per ticket, prefixed meaningfully (e.g. `squ-23-decommission-python`). When the bundled `worker` agent runs in a worktree, it follows the same convention.

### Tickets

Tickets for this repo use the `SQU` prefix and live in the `squirtlesquad` Linear workspace. Routing is handled by `ticket-manager` reading `.agent_team/config.toml`.

### Commits

Match the existing history (`git log --oneline`). Conventions:

- Tag with a category or milestone: `docs: …`, `fix(cli): …`, `chore: …`, or a milestone tag if one applies.
- Include the ticket identifier when the commit closes or substantially advances one.
- Trailer: `Co-authored-by: Claude Opus 4.7 (1M context) <noreply@anthropic.com>` on any commit an agent helped author.

### PR body

Lead with a short summary of what changed and why. Link the ticket via `Closes https://linear.app/squirtlesquad/issue/SQU-<n>/<slug>`. End with the standard Claude Code footer.

### Quality bar

- Minimal surface area. One responsibility per component.
- No half-finished code paths. No dead code, no commented-out blocks.
- Strong layer boundaries: CLI ↔ template ↔ vendored copy ↔ consumer extensions.
- If a value would be hardcoded in a template file (UUID, label, path, ticket prefix), it goes in `.agent_team/config.toml` instead — declared as a `[[parameter]]` in the bundled `template/template.toml` and substituted via a `.tmpl` file. Extend the schema rather than embedding.
- Runtime Go deps stay minimal — currently `cobra` + `BurntSushi/toml`.

If a PR doesn't meet this bar, it doesn't land.

Keep this file short. When it grows past ~150 lines, prune.
