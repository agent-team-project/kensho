# Use Case: Template Authoring

This scenario is for developers creating a reusable agent team.

## Goal

Create a template that can be instantiated into many repos with different parameters.

## Start With a Template Tree

```text
my-template/
├── template.toml
├── instances.toml
├── agents/
│   ├── manager/
│   └── worker/
└── skills/
    └── status/
```

## Declare Parameters

```toml
[template]
name = "my-engineering-team"
version = "0.1.0"

[[parameter]]
key = "tracker.project"
type = "string"
required = true
description = "Project key in the issue tracker."
```

## Render Only Intentional Files

Use `.tmpl` when the file needs substitution:

```md
Issues for this repo use `&#123;&#123; .tracker.project &#125;&#125;`.
```

Avoid rendering files that do not need parameters.

## Add Topology Defaults

```toml
[instances.manager]
agent = "manager"
ephemeral = false

[instances.worker]
agent = "worker"
ephemeral = true
replicas = 2

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"
```

## Test Locally

```sh
go build -o bin/agent-team ./cmd/agent-team
bin/agent-team init ./my-template \
  --target /tmp/my-template-smoke \
  --set tracker.project=DEMO

bin/agent-team doctor --target /tmp/my-template-smoke
```

## Authoring Checklist

- Manifest has name, version, and descriptions.
- Required parameters have clear descriptions.
- Defaults are safe and generic.
- No repo-specific UUIDs are hardcoded in prompts.
- Agents have simple frontmatter.
- Shared skills are referenced by agent config.
- Topology validates with `doctor`.
- Init works in a temp directory.
- One-shot `template run` works when relevant.

## Promotion Path

1. Use a local path while iterating.
2. Commit the template to a git repo.
3. Pull/cache by ref.
4. Initialize consumer repos from the pinned ref.
5. Use `upgrade --check` to detect drift.
