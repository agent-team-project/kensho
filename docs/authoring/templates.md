# Templates

Templates are the distribution unit for agent teams.

They answer: "What agents, skills, topology defaults, and parameter declarations should a repo receive?"

## Template Directory Shape

```text
template/
├── template.toml
├── config.toml.example
├── instances.toml
├── agents/
│   ├── manager/
│   │   ├── agent.md
│   │   └── config.toml
│   ├── ticket-manager/
│   │   ├── agent.md
│   │   └── config.toml
│   ├── reviewer/
│   │   ├── agent.md
│   │   └── config.toml
│   └── worker/
│       ├── agent.md
│       └── config.toml
└── skills/
    ├── linear/
    ├── pull-request/
    ├── status/
    ├── inbox/
    └── channel/
```

The bundled default template uses this exact shape.

## Manifest

`template.toml` declares template identity and parameters.

```toml
[template]
name = "software-engineering-team"
version = "0.1.0"
description = "Manager, ticket manager, and worker agents for software delivery."

[[parameter]]
key = "team.pm_tool"
type = "string"
default = "none"
pattern = "^(none|linear)$"
description = "Which PM tool the team talks to."

[[parameter]]
key = "linear.team_id"
type = "string"
default = ""
required_when_key = "team.pm_tool"
required_when_value = "linear"
description = "Linear team UUID."
```

Supported parameter types are intentionally small:

- `string`
- `int`
- `bool`
- `list<string>`

The renderer writes resolved values to `.agent_team/config.toml`.

Use `required = true` for values every repo must provide. Use
`required_when_key` plus `required_when_value` for conditional requirements,
such as Linear fields that are required only when `team.pm_tool = "linear"`.

## Rendering

Files ending in `.tmpl` are rendered with Go `text/template` and copied without the suffix.

Files without `.tmpl` are copied byte-for-byte.

This opt-in rule avoids accidental rendering of prompts, shell scripts, or Markdown examples that contain `&#123;&#123; ... &#125;&#125;` for other reasons.

## Init Flow

```sh
agent-team init [<ref>]
```

`init` performs:

1. Resolve the template ref.
2. Load `template.toml`.
3. Resolve parameter values from defaults, flags, and prompts.
4. Render `.tmpl` files.
5. Copy the resolved tree into `.agent_team/`.
6. Write `.agent_team/config.toml`.
7. Write `.agent_team/.template.lock`.

`--no-input` makes missing required parameters fail fast, which is useful in CI.
The bundled template has no unconditional required parameters, so zero-flag
init succeeds with `[team].pm_tool = "none"`. To enable Linear, pass
`--set team.pm_tool=linear --set linear.team_id=... --set linear.ticket_prefix=SQU`.

## Template Refs

Supported ref sources:

| Ref | Behavior |
| --- | --- |
| omitted, `bundled`, or `default` | Use the embedded default template |
| local path | Copy from the local filesystem |
| cached ref | Resolve from `~/.agent-team/cache/` |
| `github.com/org/repo@v1.2.3` | Shallow-fetch the git ref, cache it by resolved commit SHA, then render it |
| `https://...git@ref`, `ssh://...@ref`, `git@host:org/repo.git@ref`, `file:///...@ref` | Generic git URL forms; tags, branches, and commit SHAs are accepted |
| `github.com/org/repo` | Resolve the latest git tag; if no tags exist, fall back to `HEAD` with a warning |

Git cache entries live under `~/.agent-team/cache/<source>@<resolved-sha>/`
and include cache metadata that is ignored by rendering and content hashing.
Pinned tags and commit SHAs are reused from cache on later `init` / `show` /
`pull` calls. Branch refs are allowed, but they are mutable and produce a
warning.

There is no central registry or marketplace. Any git repo with `template.toml`
at its root is installable.

## Trust Model

`init` renders files only. It reads `template.toml`, copies files, renders
opt-in `.tmpl` files with Go `text/template`, writes `.agent_team/config.toml`,
and records `.template.lock`. It does not execute hooks, scripts, package
managers, or template-provided commands.

This render-only boundary is the security posture for installable templates.
Users should still inspect remote template content like any other source they
vendor into a repo, but installing a template does not run code from that
template.

## Template Lock

`.agent_team/.template.lock` records source provenance.

It lets `upgrade --check` compare the current repo against a target template ref and detect drift. `upgrade --apply --dry-run` renders the locked and target templates with the current repo config, previews add/update/remove actions, and reports conflicts where local edits overlap target changes. `upgrade --apply` applies only a clean plan and then updates `.template.lock`.

## Template Authoring Checklist

When authoring a template:

1. Declare all user-specific values in `template.toml`.
2. Keep hardcoded IDs out of prompts and scripts.
3. Use `.tmpl` only where values must be rendered.
4. Keep agents reusable; put repo-specific behavior in config.
5. Include a starter `instances.toml` when daemon workflows should work after init.
6. Tag published releases, for example `v1.2.3`, so consumers can pin refs.
7. Run:

```sh
go build -o bin/agent-team ./cmd/agent-team
bin/agent-team template smoke ./template \
  --set team.pm_tool=linear \
  --set linear.team_id=00000000-0000-0000-0000-000000000000 \
  --set linear.ticket_prefix=SMK
```

Use `--commands` when the smoke reports warnings or failures and you want the
rendered temp repo plus scoped follow-up doctor commands. Use `--strict` in CI
when daemon, runtime, and template provenance warnings should fail the smoke.

## Code Areas

Template behavior lives mostly in:

- `internal/template/manifest.go`
- `internal/template/config.go`
- `internal/template/render.go`
- `internal/template/ref.go`
- `internal/template/provenance.go`
- `internal/cli/init.go`
- `internal/cli/template.go`
- `internal/cli/template_git.go`
- `internal/cli/template_run.go`
