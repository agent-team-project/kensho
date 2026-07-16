# Resource Model

`agent-team` is still local and repo-scoped by default, but runtime state now
has stable resource identities. The goal is to let operators and agents name
the thing they mean before deciding whether it lives in a path, a daemon
socket, or a routed deployment.

## Canonical URIs

Resource identifiers use:

```text
agt://<deployment-id>/<kind>/<id>[#fragment]
```

The local deployment id comes from `[project].id` in `.agent_team/config.toml`.
`agent-team init` writes that id for initialized repos. The deployment self
resource is modeled as the project resource:

```text
agt://<deployment-id>/project/<deployment-id>
```

Current daemon reads support these resource kinds:

| Kind | What It Names |
| --- | --- |
| `project` | The local deployment and daemon state root |
| `instance` | Runtime metadata for one instance |
| `job` | A durable job; `#step=<id>` reads one pipeline step |
| `workspace` | The repo workspace or a job/instance worktree |
| `state` | One instance state directory and status summary |
| `log` | One instance child log metadata |
| `usage` | Runtime usage records |
| `mailbox` | One instance mailbox |
| `channel` | One daemon channel and subscriptions |
| `queue` | One active queue item |
| `outbox` | One agent outbox item |
| `lock` | One declared lock's snapshots and leases |
| `topology` | The current loaded topology |

The read envelope contains the canonical `uri`, `kind`, `id`, optional
`fragment`, and a resource-specific JSON `data` value.

## Deployment Names

`agent-team deployments` projects a read-only deployment registry view from the
current repo and any configured local routes. It does not create or update a
registry file.

```sh
agent-team deployments ls
agent-team deployments resolve self
agent-team deployments resolve local --json
```

Every initialized repo with `[project].id` has a `self` entry, with aliases
`local`, `.`, and the deployment id. A configured `[project].parent_uri` appears
as `parent`. Local feedback routes can also appear as named route entries when
they point at another initialized `.agent_team/` directory.

Entries may include a local transport endpoint when one is available:

- `http` for the daemon's loopback HTTP listener.
- `unix` for the daemon socket.

## Reading Resources

Use `agent-team read <agt-uri>` for daemon-mediated reads:

```sh
agent-team read agt://<deployment-id>/topology/current
agent-team read agt://<deployment-id>/job/squ-42
agent-team read agt://<deployment-id>/job/squ-42#step=review --json
```

The command resolves the URI's deployment id through `agent-team deployments`
and then asks that deployment's daemon for `/v1/resources?uri=...`. It never
falls back to opening `.agent_team/` files directly. Without `--json`, it prints
the resource `data` body; with `--json`, it prints the full envelope.

If the URI belongs to an unknown deployment, has a mismatched deployment id, or
names an unsupported resource kind, the command fails instead of guessing a
path.

## Authority

Topology authority allowlists are written in terms of CLI verbs, not paths.
When `[authority].enforcement = "audit"`, disallowed audited mutations append
`authority_violation` events but still run. When
`[authority].enforcement = "enforce"`, the same violation is recorded and the
mutation is denied.

Runtime launch also installs an `agent-team` shim under enforcement. The shim
resolves each invocation through the real Cobra command tree to a dotted verb
such as `job.merge`, `job.gate.set`, `read`, or `deployments.resolve`. Unknown
verbs are denied before wildcard allowlists are considered. The allowlist is
baked into the generated shim, so an agent cannot widen it by editing
environment variables.

Managed CLI resolution has one precedence rule: an explicit native CLI, then
the launching CLI or its sibling pair, then native `agent-team` executables in
`PATH` order. Generated per-instance shims are never eligible targets. When the
daemon identity is known, a source-comparable candidate wins over an earlier
stale native executable; if none is comparable, activation reports the first
native candidate only as a diagnostic and does not claim coherence.

Every generated shim exposes `agent-team --build-attestation [--json|--header]`
before Cobra or authority resolution. The read-only result is baked at launch
and separates CLI source identity, daemon comparison (`coherent`, `mismatch`,
missing provenance, or not checked), activation assets, and the registered
skill fingerprint. Bundled inbox/channel/dispatch helpers accept a build
header only from a generated shim or native managed CLI whose immutable
identity matches the active daemon. Activation-sensitive writes fail with a
fresh-start action when no comparable surface exists.

Because URI reads are ordinary CLI verbs, grant `read` when an enforced agent
should read daemon resources by `agt://` URI. Scoped grants such as
`job.gate.*:own` are evaluated by the real CLI or daemon using the caller's
trusted origin metadata; the shim only decides whether a known verb reaches
that implementation.

## API Surface

The daemon serves the same model over its local API:

```http
GET /v1/resources?uri=<url-encoded-agt-uri>
```

The API is local to one repo/deployment and is still versioned under `/v1`.
The CLI remains the supported integration surface for scripts.
