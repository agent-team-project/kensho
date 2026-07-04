# Publishing Templates

Templates are git-native. There is no marketplace requirement: any repository
with `template.toml` at its root can be installed.

## Repository Shape

```text
my-agent-team/
├── template.toml
├── agents/
│   ├── manager/
│   │   └── agent.md
│   └── worker/
│       └── agent.md
├── skills/
│   └── status/
│       └── SKILL.md
└── instances.toml
```

Only `template.toml` is required. The rest of the tree follows the same
`.agent_team/` shape that `init` writes into consumer repos.

Use `.tmpl` suffixes only for files that need parameter substitution. Files
without `.tmpl` are copied byte-for-byte.

## Manifest Requirements

`template.toml` needs template identity and any parameter declarations:

```toml
[template]
name = "my-agent-team"
version = "1.0.0"
description = "Reusable manager and worker team."

[[parameter]]
key = "tracker.project"
type = "string"
required = true
description = "Project key used by tracker skills."
```

Keep user-specific values out of prompts and scripts. Declare them as
parameters and render them into config or docs only where needed.

## Versioning

Publish stable releases with git tags:

```sh
git tag v1.0.0
git push origin v1.0.0
```

Consumers can then install a pinned release:

```sh
agent-team init github.com/acme/my-agent-team@v1.0.0
```

Tags and commit SHAs are cached by resolved commit SHA and reused on later
installs. Branch refs such as `@main` work, but they are mutable and produce a
warning.

If the ref is omitted, `agent-team` selects the latest git tag. If the repo has
no tags, it falls back to `HEAD` and warns that the ref is mutable.

## Trust Posture

Installing a template is render-only. `agent-team init`:

- reads `template.toml`
- resolves parameters
- copies files
- renders opt-in `.tmpl` files
- writes `.agent_team/config.toml` and `.agent_team/.template.lock`

It does not execute scripts, hooks, package managers, or template-provided
commands. The security boundary is the rendered file tree.

## Local Validation

Before tagging a template, smoke it locally:

```sh
go build -o bin/agent-team ./cmd/agent-team
bin/agent-team template smoke ./my-agent-team \
  --set tracker.project=DEMO
```

Use `template show ./my-agent-team` to inspect the manifest and `template pull
./my-agent-team` to test cache behavior before publishing.
