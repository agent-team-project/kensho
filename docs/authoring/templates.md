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
key = "linear.team_id"
type = "string"
required = true
description = "Linear team UUID."

[[parameter]]
key = "linear.ticket_prefix"
type = "string"
required = true
description = "Ticket prefix such as SQU."
```

Supported parameter types are intentionally small:

- `string`
- `int`
- `bool`
- `list<string>`

The renderer writes resolved values to `.agent_team/config.toml`.

## Rendering

Files ending in `.tmpl` are rendered with Go `text/template` and copied without the suffix.

Files without `.tmpl` are copied byte-for-byte.

This opt-in rule avoids accidental rendering of prompts, shell scripts, or Markdown examples that contain `&#123;&#123; ... &#125;&#125;` for other reasons.

## Init Flow

```sh
agent-team init [<ref>] --set linear.team_id=... --set linear.ticket_prefix=SQU
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

## Template Refs

Supported ref sources:

| Ref | Behavior |
| --- | --- |
| omitted, `bundled`, or `default` | Use the embedded default template |
| local path | Copy from the local filesystem |
| cached ref | Resolve from `~/.agent-team/cache/` |

Template pull/cache support exists for local and git-oriented flows. Full registry semantics are intentionally deferred.

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
6. Run:

```sh
go build -o bin/agent-team ./cmd/agent-team
bin/agent-team template smoke ./template \
  --set linear.team_id=00000000-0000-0000-0000-000000000000 \
  --set linear.ticket_prefix=SMK
```

Use `--commands` when the smoke reports warnings or failures and you want the
rendered temp repo plus scoped follow-up doctor commands.

## Code Areas

Template behavior lives mostly in:

- `internal/template/manifest.go`
- `internal/template/config.go`
- `internal/template/render.go`
- `internal/template/ref.go`
- `internal/template/provenance.go`
- `internal/cli/init.go`
- `internal/cli/template.go`
- `internal/cli/template_run.go`
