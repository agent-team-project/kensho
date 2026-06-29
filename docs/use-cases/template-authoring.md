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
bin/agent-team template smoke ./my-template \
  --set tracker.project=DEMO
```

Use `--keep` when you want to inspect the rendered temp repo after the smoke
run. The smoke command runs `init`, `doctor`, `pipeline doctor`, and `team doctor`;
it exits non-zero if any step fails. Add `--strict-runtime` when CI
should fail on unavailable selected runtimes, step-declared runtime defaults, or
agent-level runtime defaults.

## Authoring Checklist

- Manifest has name, version, and descriptions.
- Required parameters have clear descriptions.
- Defaults are safe and generic.
- No repo-specific UUIDs are hardcoded in prompts.
- Agents have simple frontmatter.
- Shared skills are referenced by agent config.
- `template smoke` passes with representative parameters.
- One-shot `template run` works when relevant, including `--runtime codex` when the template should be tested against the Codex adapter.

## Promotion Path

1. Use a local path while iterating.
2. Commit the template to a git repo.
3. Pull/cache by ref.
4. Initialize consumer repos from the pinned ref.
5. Use `upgrade --check` to detect drift and `upgrade --apply --dry-run` before applying clean template changes.
