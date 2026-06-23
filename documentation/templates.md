# Templates as Images (design sketch)

**Status**: design sketch, not yet built. Captures the v1.2+ model that lands in SQU-22 (`template` verb consolidation) and beyond. Companion to [`orchestrator.md`](./orchestrator.md).

## What it is

The same conceptual duality Docker uses for images vs. containers, applied here:

| Docker | agent-team |
|---|---|
| **Image** — versioned, distributable artifact, parameterized via env vars at run time | **Template** — versioned, distributable directory of agents + skills + manifest, parameterized via config |
| **Container** — running instance of an image with concrete config | **Repo** — `.agent_team/` tree instantiated from a template with concrete parameters |

A consumer fetches a template, supplies parameters (Linear team ID, ticket prefix, defaults), and instantiates it into their repo. Multiple consumers reuse the same template with different parameters; one consumer can host multiple instances of the same agent with per-instance overrides.

## Why this model

The current single-bundled-starter model assumes one fixed `.agent_team/` shape. Two real use cases break that:

1. **Different repos, different teams.** A user with five projects across three Linear teams shouldn't hand-edit five `config.toml` files. The template author declares the parameters; each consumer fills them in once at `init`.
2. **One repo, multiple instances of the same agent with different settings.** "I want a `ticket-manager-platform` routing to Linear project A and a `ticket-manager-mobile` routing to project B." Single repo-global `config.toml` doesn't support this — instances need per-instance overrides on top of repo defaults.

Templates-as-images plus a layered config resolution chain covers both.

## Concepts

### Template

A directory tree containing:

- `template.toml` — manifest (name, version, parameter declarations).
- `agents/<name>/agent.md`, `skills/<name>/SKILL.md`, etc. — same shape as today's `.agent_team/`.
- `*.tmpl` files — files that need parameter substitution at instantiation. Files without the `.tmpl` suffix are copied verbatim.

The bundled software-engineering team (today's starter) becomes the **default template**: same content, plus a `template.toml` declaring its parameters (`linear.team_id`, `linear.ticket_prefix`, etc.).

### Repo

A consumer's `.agent_team/` directory after instantiation. Contains:

- Rendered agent and skill files (`.tmpl` suffixes resolved and stripped).
- `config.toml` — resolved parameter values, repo-wide.
- `.template.lock` — pinned ref + content hash of the source template for reproducibility.

### Instance

A running spawn of an agent within a repo. Has its own state dir at `.agent_team/state/<instance>/`. Today's instances share the repo-wide `config.toml`; the new model allows per-instance overrides.

## Manifest format (`template.toml`)

```toml
[template]
name        = "software-engineering-team"
version     = "1.0.0"
description = "Manager + worker + ticket-manager team for Linear-tracked engineering work."

[[parameter]]
key         = "linear.team_id"
type        = "string"
required    = true
description = "Linear team UUID. Find at linear.app/<workspace>/team/<TEAM>/all → URL contains the UUID."

[[parameter]]
key         = "linear.ticket_prefix"
type        = "string"
required    = true
pattern     = "^[A-Z]{2,5}$"
description = "Linear ticket prefix, e.g. SQU."

[[parameter]]
key         = "linear.initiative_id"
type        = "string"
required    = false
description = "Optional initiative UUID to attach new tickets to."

[[parameter]]
key         = "linear.labels"
type        = "list<string>"
default     = []
description = "Labels to apply to all created tickets."
```

**Parameter fields**:
- `key` — dotted path the value occupies in the resolved config tree. Mirrors `config.toml` structure.
- `type` — one of `string`, `int`, `bool`, `list<string>`. Future: `enum`, nested objects.
- `required` — boolean; default `false`.
- `default` — required if `required = false`. Value of declared type.
- `pattern` — optional regex validation for string types.
- `description` — shown to the user during interactive prompting.

## Substitution

**Opt-in via `.tmpl` suffix.** `agents/ticket-manager/skills/linear/SKILL.md.tmpl` becomes `.../SKILL.md` after rendering, with parameters substituted. Files without `.tmpl` are copied byte-for-byte.

**Syntax: Go `text/template`.** A file's content is rendered against the resolved config tree:

```markdown
# Linear access

Tickets for this repo use the `{{ .linear.ticket_prefix }}` prefix. Team UUID:
{{ .linear.team_id }}.

{{ if .linear.initiative_id -}}
Tickets are filed under initiative `{{ .linear.initiative_id }}`.
{{- end }}
```

**Why Go templates over a custom DSL**:
- Stdlib in the language we're rewriting in — zero extra dependency.
- Well-understood, documented, debuggable.
- Conditionals + iteration (`{{ range .linear.labels }}`) come for free.

**Why `.tmpl` suffix opt-in over render-everything**:
- Many files (markdown bodies, bash scripts) legitimately contain `{{ }}` for unrelated reasons — Claude Code prompts, mustache snippets in user docs, etc. Rendering everything forces escape rules everywhere.
- Authors mark intent explicitly. A reader of the template tree sees `agent.md` (verbatim) vs `agent.md.tmpl` (rendered) and immediately knows which is which.

## Resolution chain

Parameters resolve from layered sources, in order of precedence (highest wins):

```
1. CLI flags                       (--set linear.project_id=<x>)
2. Per-instance config             (.agent_team/state/<instance>/config.toml, optional)
3. Repo config                     (.agent_team/config.toml)
4. Template defaults               (template.toml [[parameter]] default values)
```

### At `init` time

```
template defaults (template.toml)
  ←  CLI flags / interactive prompts (for required params without defaults)
=  resolved repo config (written to .agent_team/config.toml)
   used for substitution into .tmpl files
```

### At `run` time

```
repo config (.agent_team/config.toml)
  ←  per-instance config (.agent_team/state/<instance>/config.toml, if present)
  ←  CLI --set flags
=  resolved instance config (written to .agent_team/state/<instance>/config.toml by the launcher)
   exposed to the agent via $AGENT_TEAM_STATE_DIR/config.toml
```

The agent itself never sees the layering — it reads one resolved config from a known path. Layering happens in the launcher.

## CLI surface

```
agent-team init [<ref>] [--set k=v]... [--no-input]
    Instantiate a template into the current repo.
    <ref> defaults to the bundled "default" template.
    Required parameters with no default → prompt interactively, or read from --set, or fail under --no-input.

agent-team upgrade --check [--to <ref>]
    Read-only preflight available today: compare .agent_team/.template.lock
    against the locked ref or an explicit target ref.

agent-team upgrade [--to <ref>] [--set k=v]...
    Future apply mode: re-resolve the current repo's template against a
    (newer) version. Three-way merges template-default → user-current →
    new-template-default. User edits to authored files are preserved when not
    in conflict.

agent-team template ls
    List bundled + cached templates.

agent-team template pull <ref>
    Fetch template to local cache (~/.agent-team/cache/...).

agent-team template show <ref>
    Print manifest: name, version, declared parameters.

agent-team template rm <ref>
    Remove a template from the local cache.

agent-team template run <ref> <agent> [--target <dir>] [--keep] [--force] [--set k=v]... [-p "..."]
    One-shot: instantiate a template into a (temp)dir and spawn the named agent
    against it. Tempdir is removed on exit unless --target or --keep is given.
    The daemon is bypassed (see § Worked example step 8 for the rationale).

agent-team run <agent> [--set k=v]... [--instance-config <path>]
    Spawn an instance. CLI --set flags and --instance-config layer on top of repo config.
```

## Refs

A template ref identifies *what to instantiate*:

- **Bundled** (no ref): `agent-team init` → uses the default template embedded in the binary.
- **Local path**: `agent-team init ./path/to/template` → useful for template authoring / development.
- **Git URL**: `agent-team template pull github.com/foo/bar@v0.1.0` → fetches via `git clone`, caches under `~/.agent-team/cache/<host>/<owner>/<repo>@<version>/`; after that `agent-team init github.com/foo/bar@v0.1.0` resolves from the cache. Pinned refs (`@v0.1.0`) are preferred; mutable refs such as `@main` warn on pull.
- **OCI / registry** (later): defer until git refs prove insufficient.

## Versioning

- Templates declare `version` in `template.toml` (semver).
- Refs can pin (`@v1.2.0`), float to a branch (`@main`), or omit (defaults to latest tagged).
- `.template.lock` records the resolved ref, manifest identity, and source content hash, enabling reproducible re-instantiation and future upgrade planning.

## Open questions

1. **Interactive vs. flags-only as default.** Recommended: prompt by default, `--no-input` for CI / scripted use (must supply all required params via `--set` or fail). Aligns with `cookiecutter`, `create-react-app`. Not aligned with `helm install` (flags-only). Verify with users once shipped.

2. **`upgrade` semantics on user-edited files.** Three-way merge is the obvious model but has corner cases (deleted file, renamed file, schema-incompatible parameter change). For v1.2 launch: support upgrade only between same-major-version templates; bail with a diagnostic if the major version changes. Refine later.

3. **Multi-template repos.** Can one `.agent_team/` host multiple templates? Probably no for v1.2 — keeps the resolution chain simple. Power users with two unrelated agent groups should split into two repos or a sub-directory model. Revisit if pain emerges.

4. **Parameter scope** — template-wide vs. per-agent. Recommended: template-wide for v1.2 (all params declared in `template.toml`, available to any `.tmpl` in the tree). Per-agent scoping adds complexity for marginal benefit; templates that need it can split into separate templates.

5. **`run --set` overrides on resolved values that came from `.tmpl` substitution.** A `.tmpl` file is rendered at `init` time and committed (or just present) in the repo. If `run --set linear.project_id=X` overrides a value that's already baked into a rendered file, the agent reads the rendered file but the `--set` is also exposed via `$AGENT_TEAM_STATE_DIR/config.toml`. There's a divergence — the rendered file says one thing, the resolved config says another. Two options: (a) re-render `.tmpl` files into the state dir at every `run` (always-fresh, costs a tmp tree per spawn), (b) resolve at runtime by passing the merged config and ignoring rendered files (then `.tmpl` rendering at init is only for human readability — feels wasteful). Decide before SQU-22 lands. Tentative: (a) — render at every `run` into the instance's state dir, treat `.tmpl`-rendered files in `.agent_team/` as a checked-in default that's overridden by the per-spawn render.

## Worked example

Concrete trace of the full lifecycle, focused on the git-URL ref path. Takes a hypothetical published template `github.com/acme/eng-team@v1.0.0` and instantiates it into a consumer repo.

### 1. Author's side: publishing a template (one-time)

The author has a git repo `github.com/acme/eng-team`. Its layout:

```
eng-team/
├── template.toml
├── agents/
│   ├── manager/
│   │   └── agent.md
│   ├── worker/
│   │   └── agent.md
│   └── ticket-manager/
│       ├── agent.md
│       └── skills/
│           └── linear/
│               ├── SKILL.md
│               └── config.toml.tmpl       ← parameterized
└── skills/
    ├── pull-request/
    │   └── SKILL.md
    └── linear/
        └── SKILL.md
```

`template.toml`:

```toml
[template]
name        = "eng-team"
version     = "1.0.0"
description = "Manager + worker + ticket-manager for Linear-tracked engineering work."

[[parameter]]
key         = "linear.team_id"
type        = "string"
required    = true
description = "Linear team UUID. Find at linear.app/<workspace>/team/<TEAM>/all (UUID is in the URL)."

[[parameter]]
key         = "linear.ticket_prefix"
type        = "string"
required    = true
pattern     = "^[A-Z]{2,5}$"
description = "Linear ticket prefix, e.g. ENG."

[[parameter]]
key         = "linear.initiative_id"
type        = "string"
required    = false
description = "Optional initiative UUID to attach new tickets to."

[[parameter]]
key         = "linear.labels"
type        = "list<string>"
default     = []
description = "Labels applied to all created tickets."
```

`agents/ticket-manager/skills/linear/config.toml.tmpl` (the only `.tmpl` file; everything else is verbatim):

```toml
[linear]
team_id       = "{{ .linear.team_id }}"
ticket_prefix = "{{ .linear.ticket_prefix }}"
{{- if .linear.initiative_id }}
initiative_id = "{{ .linear.initiative_id }}"
{{- end }}
labels        = [{{ range $i, $l := .linear.labels }}{{ if $i }}, {{ end }}"{{ $l }}"{{ end }}]
```

Author tags the release: `git tag v1.0.0 && git push --tags`. The template is now consumable.

### 2. Consumer: pull

```sh
$ agent-team template pull github.com/acme/eng-team@v1.0.0
Pulling github.com/acme/eng-team@v1.0.0 ...
✓ Cloned into ~/.agent-team/cache/github.com/acme/eng-team@v1.0.0/
✓ Validated template.toml (4 parameters declared)
✓ Content hash: sha256:a1b2c3...
```

Cache layout after pull:

```
~/.agent-team/cache/
└── github.com/
    └── acme/
        └── eng-team@v1.0.0/
            ├── template.toml
            ├── agents/...
            ├── skills/...
            └── .agent-team-meta.json    ← {ref, content_hash, pulled_at}
```

### 3. Consumer: inspect

```sh
$ agent-team template show github.com/acme/eng-team@v1.0.0
Template: eng-team v1.0.0
Description: Manager + worker + ticket-manager for Linear-tracked engineering work.

Parameters:
  linear.team_id        string   (required)        Linear team UUID. Find at linear.app/...
  linear.ticket_prefix  string   (required, ^[A-Z]{2,5}$)  Linear ticket prefix, e.g. ENG.
  linear.initiative_id  string   (optional)        Optional initiative UUID to attach new tickets to.
  linear.labels         list<string> (default: []) Labels applied to all created tickets.

Agents in this template: manager, worker, ticket-manager
Skills in this template: pull-request, linear
```

### 4. Consumer: instantiate

```sh
$ cd ~/projects/my-app
$ agent-team init github.com/acme/eng-team@v1.0.0
Resolving template github.com/acme/eng-team@v1.0.0 ... ✓ (cached)

This template requires the following parameters:

  linear.team_id (required): 72986798-f8b4-4f57-afe2-c76d4868db0f
  linear.ticket_prefix (required) [matches ^[A-Z]{2,5}$]: APP
  linear.initiative_id (optional, leave blank to skip):
  linear.labels (optional, comma-separated, default: []): needs-triage, platform

Resolved configuration:
  linear.team_id       = "72986798-f8b4-4f57-afe2-c76d4868db0f"
  linear.ticket_prefix = "APP"
  linear.labels        = ["needs-triage", "platform"]

Writing .agent_team/ ...
✓ agents/manager/agent.md (verbatim)
✓ agents/worker/agent.md (verbatim)
✓ agents/ticket-manager/agent.md (verbatim)
✓ agents/ticket-manager/skills/linear/SKILL.md (verbatim)
✓ agents/ticket-manager/skills/linear/config.toml (rendered from .tmpl)
✓ skills/pull-request/SKILL.md (verbatim)
✓ skills/linear/SKILL.md (verbatim)
✓ config.toml (resolved parameters)
✓ .template.lock (github.com/acme/eng-team@v1.0.0, sha256:a1b2c3...)

Done. Run `agent-team run manager` to start.
```

Resulting consumer repo state:

```
my-app/
└── .agent_team/
    ├── config.toml                  ← resolved values, repo-wide
    ├── .template.lock                ← pinned ref + hash
    ├── agents/
    │   ├── manager/
    │   ├── worker/
    │   └── ticket-manager/
    │       └── skills/linear/
    │           ├── SKILL.md
    │           └── config.toml      ← rendered from .tmpl
    └── skills/
        ├── pull-request/
        └── linear/
```

`.agent_team/config.toml`:

```toml
[linear]
team_id       = "72986798-f8b4-4f57-afe2-c76d4868db0f"
ticket_prefix = "APP"
labels        = ["needs-triage", "platform"]
```

`.agent_team/agents/ticket-manager/skills/linear/config.toml` (rendered):

```toml
[linear]
team_id       = "72986798-f8b4-4f57-afe2-c76d4868db0f"
ticket_prefix = "APP"
labels        = ["needs-triage", "platform"]
```

### 5. Consumer: run an instance (default config)

```sh
$ agent-team run ticket-manager
```

Launcher steps:
1. Reads `.agent_team/config.toml`.
2. No per-instance config dir; no `--set` flags. Resolved config = repo config.
3. Creates `.agent_team/state/ticket-manager/`, writes the resolved `config.toml` there.
4. Sets `AGENT_TEAM_STATE_DIR=.../state/ticket-manager`.
5. Exec's `claude --agents '{"ticket-manager": ...}' --add-dir <skills-tmpdir> ...`.

Inside the claude session, the `linear` skill reads `$AGENT_TEAM_STATE_DIR/config.toml` and routes tickets to `team_id = 72986798-...`, prefix `APP`.

### 6. Consumer: multiple ticket-managers with different routing (the motivating case)

The user runs two services in one repo and wants tickets routed to two different Linear projects. Today's `config.toml` is repo-global — so they use per-instance overrides:

```sh
# Platform tickets → Linear project A
$ agent-team run ticket-manager --name=tm-platform \
    --set linear.project_id=3d07030a-a372-41a2-b01e-1b4116d0f151

# Mobile tickets → Linear project B
$ agent-team run ticket-manager --name=tm-mobile \
    --set linear.project_id=50b6cd55-5760-4fd3-9bbe-acb17e544aa2
```

For each, the launcher merges:

```
template defaults  →  repo config  →  per-instance --set  =  resolved instance config
```

State dirs after both spawns:

```
.agent_team/state/
├── tm-platform/
│   └── config.toml         ← linear.project_id = 3d07030a-...
└── tm-mobile/
    └── config.toml         ← linear.project_id = 50b6cd55-...
```

Both instances share `linear.team_id` and `linear.ticket_prefix` from the repo config; they diverge only on `linear.project_id`. Either can be re-spawned with the same `--name` and the per-instance overrides persist via the state dir.

Alternative — the user can commit a per-instance config to skip retyping `--set` every time:

```toml
# .agent_team/state/tm-platform/config.toml
[linear]
project_id = "3d07030a-a372-41a2-b01e-1b4116d0f151"
```

Then `agent-team run ticket-manager --name=tm-platform` picks it up automatically.

### 7. Consumer: upgrade

A new version drops upstream:

```sh
$ agent-team upgrade --to github.com/acme/eng-team@v1.1.0
Pulling github.com/acme/eng-team@v1.1.0 ... ✓
Resolving v1.0.0 → v1.1.0 ...

Changes:
  template.toml          modified (1 parameter added: linear.priority_default)
  agents/manager/agent.md modified upstream, unchanged locally  → auto-update
  agents/worker/agent.md  modified upstream, modified locally   → 3-way merge needed

New parameters required:
  linear.priority_default (string, default: "medium"):  [Enter to accept default]

Three-way merge required for: agents/worker/agent.md
  Run `agent-team upgrade --resolve` after editing or pass --strategy=ours / --strategy=theirs.

✗ Upgrade paused. Resolve conflicts and re-run.
```

If the user accepts the default and there's no conflict:

```sh
✓ agents/manager/agent.md updated
✓ template.toml-derived config.toml updated (linear.priority_default = "medium")
✓ .template.lock bumped to v1.1.0
```

User edits to authored files are preserved when the upstream didn't touch the same regions; conflicts surface explicitly rather than silently overwriting.

### 8. Consumer: one-shot ephemeral run

For try-out / CI / fresh-sandbox use cases, the two-step `init` + `run` collapse into a single command:

```sh
$ agent-team template run github.com/acme/eng-team@v1.0.0 manager \
    --set linear.team_id=72986798-f8b4-4f57-afe2-c76d4868db0f \
    --set linear.ticket_prefix=APP \
    -p "kick off the platform-q3 initiative"
Resolving template github.com/acme/eng-team@v1.0.0 ... ✓ (cached)
Using tempdir /home/alice/.agent-team/runs/20260427T143052-manager-abc123 (removed on exit; pass --keep to preserve)
Vendoring team into /home/alice/.agent-team/runs/20260427T143052-manager-abc123/.agent_team
  + .agent_team/agents/manager
  + .agent_team/agents/worker
  + .agent_team/agents/ticket-manager
  ...
  + .agent_team/config.toml (resolved)
[runtime session runs, attached to terminal]
[on exit: tempdir is removed]
```

Surface (full flag set):

```
agent-team template run <ref> <agent> [--target <dir>] [--keep] [--force] \
    [--set k=v]... [--no-input] [-n <instance>] [-p "<kickoff>"] \
    [-- <runtime-args>...]
```

Resolution flow:

1. **Ref**: same resolver as `init` — `bundled`, local path, or cached pull.
2. **Target dir**:
   - `--target <dir>` given: use it; pre-existing `.agent_team/` is rejected unless `--force`.
   - else: auto-create `<runsRoot>/<timestamp>-<agent>-<random>/`, where `<runsRoot>` is `$XDG_CACHE_HOME/agent-team/runs` (Linux with XDG set) or `~/.agent-team/runs` (macOS / fallback). Predictable so users who pass `--keep` can find their preserved runs.
3. **Parameter resolution**: same as `init` — `--set` flags + interactive prompts for required params, `--no-input` fails fast in CI.
4. **Render**: `.agent_team/` is written into the target dir.
5. **Spawn**: same as `run`, with the daemon explicitly bypassed (see below).
6. **Cleanup**: when the agent's runtime session exits, if the dir was auto-created and `--keep` is unset, the dir is removed. `--target` directories are always preserved. SIGINT / SIGTERM trigger best-effort cleanup before re-raising the signal so the parent shell sees the conventional exit status.

#### Why the daemon is bypassed

`template run` is for one-shot ephemeral spawns. Bringing up a tempdir-scoped daemon to dispatch a single instance and then tearing it down adds lifecycle complexity for no gain in this use case — the user has nothing to follow up with via `instance ps` or `logs --follow` because the tempdir is about to vanish. So `template run` always execs the selected runtime directly.

The tradeoff: a `template run` instance is invisible from another terminal (no `instance ps`, no `logs --follow`). Acceptable for ephemeral spawns. For long-lived setups where multi-terminal observability matters, use `init` + `run` separately — the daemon-aware path is preserved there.

A future `--daemon` flag could opt into spinning up a tempdir-scoped daemon for the run's duration, but this is deferred until concrete demand surfaces.

### Summary of the model in one paragraph

The template is a parameterized image hosted somewhere (git URL today). `template pull` fetches it to a local cache. `init <ref>` resolves required parameters (interactive prompt or `--set` flags), writes the resolved `config.toml` plus rendered `.tmpl` files into the consumer's `.agent_team/`, and pins the version in `.template.lock`. `run` reads `config.toml`, optionally layers a per-instance config and `--set` flags, and exposes the resolved tree to the spawned agent at `$AGENT_TEAM_STATE_DIR/config.toml`. `upgrade` re-resolves against a newer template version with a three-way merge.

## Relationship to orchestrator and topology

Three forward-looking docs partition the design space:

- **This doc (`templates.md`)** — authoring/distribution: parameterized templates with manifest, substitution, refs, four-layer resolution chain.
- [`orchestrator.md`](./orchestrator.md) — runtime: daemon-managed lifecycle, message routing, instance state.
- [`topology.md`](./topology.md) — declaration: `instances.toml` (which named instances exist, configured how, triggered by what events). Topology adds a fifth resolution layer (per-instance declared overrides) between repo `config.toml` and per-instance state files. Consumers who don't need topology see today's UX — declaring instances is opt-in.

## What this doesn't change

- Agent definitions stay file-based and human-authored. Templates are just a packaging layer.
- The bundled team (manager + worker + ticket-manager) becomes the default template — same files, plus a `template.toml`. Existing consumers see no behavior change unless they opt into parameters.
- The orchestrator design ([`orchestrator.md`](./orchestrator.md)) is independent. Templates address authoring/distribution; the orchestrator addresses runtime lifecycle. They compose: a daemon-managed instance is still configured by parameters resolved through the chain above.
