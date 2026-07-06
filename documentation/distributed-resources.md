# Distributed Resources

Status: design draft for SQU-128 phase 1. This document is audit and design
only. It intentionally proposes no product-code changes.

## Problem

`agent-team` started as a per-repo, same-machine control plane. That model is
still valuable: a vendored `.agent_team/` tree, a local daemon, file-backed
state, and worktree-isolated workers are simple to inspect and repair.

The next step is distribution. A manager should be able to address another
deployment, a worker should be schedulable inside an L2 container, and a
sandboxed runtime should talk to the daemon without reaching through a Unix
socket path. Today, many records and helpers identify resources by host-local
paths: `.agent_team`, `.claude/worktrees/...`, `daemon.sock`, `.env`, and
template cache directories. Those paths are valid implementation details inside
one machine, but they cannot be durable cross-deployment identity.

The resource model below keeps local paths where they are operationally useful
and adds stable identifiers where the value crosses a process, host, container,
or deployment boundary.

## Design Inputs

- [security-model.md](./security-model.md): L2 containers are the distribution
  substrate. A worker becomes "image + workspace mount + daemon endpoint +
  budget + capability token". The Codex sandbox probe also showed that AF_UNIX
  sockets are blocked on macOS, so the daemon needs a TCP-loopback plus token
  API path before sandboxed workers can rely on brokered verbs.
- [orchestrator.md](./orchestrator.md): the daemon is the owner of lifecycle,
  mailbox, queue, job, status, and runtime state. The current transport is
  Unix-socket HTTP, with an optional loopback HTTP listener already present.
- SQU-127: routes should address named deployments through a registry such as
  `~/.agent-team/deployments.toml`, rather than embedding `root = "/path"`.
  Path-shaped route references can remain as local shorthand, similar to Git
  remote URLs.

## Terms

- Deployment: one agent-team control plane. Today this is one `.agent_team/`
  directory plus one daemon. Later it may be a local daemon with container
  workers, or a remote daemon.
- Deployment name: a local operator alias, such as `agent-team`, resolved by the
  deployment registry. Names are not globally stable.
- Deployment id: the stable `[project].id` already stored in
  `.agent_team/config.toml`. It identifies the source deployment in provenance
  and resource URIs.
- Resource: a durable control-plane object, such as a job, instance, mailbox,
  channel, workspace lease, template, route, or secret handle.
- Materialization: a local file path, socket path, cache path, worktree path, or
  mount path where a resource is implemented for one placement.

## URI Shape

Canonical resource URIs use the deployment id as the authority:

```text
agt://<deployment-id>/<kind>/<resource-id>[#fragment]
```

Rules:

- `<deployment-id>` is `[project].id`, not the local registry alias.
- `<kind>` is a stable noun from the daemon API surface.
- `<resource-id>` uses existing safe ids when they exist: job id, instance name,
  channel name, route name, or template digest.
- URI path segments are percent-encoded. For example, channel `#blocked` is
  `agt://<deployment-id>/channel/%23blocked`.
- Fragments may identify subresources that are not separately persisted. For
  example, `agt://<deployment-id>/job/squ-128#step=implement`.

Initial kinds:

| Kind | Example | Notes |
| --- | --- | --- |
| `project` | `agt://<deployment-id>/project/<deployment-id>` | Self resource for the deployment. |
| `instance` | `agt://<deployment-id>/instance/platform-worker-squ-128` | Named runtime instance. |
| `job` | `agt://<deployment-id>/job/squ-128` | Durable work item. |
| `workspace` | `agt://<deployment-id>/workspace/ws_01j...` | Lease for a repo checkout, worktree, or container mount. |
| `state` | `agt://<deployment-id>/state/platform-worker-squ-128` | Instance-private writable state. |
| `mailbox` | `agt://<deployment-id>/mailbox/platform-worker-squ-128` | Mailbox is addressed through daemon API, not file paths. |
| `channel` | `agt://<deployment-id>/channel/%23blocked` | Broadcast channel ledger. |
| `route` | `agt://<deployment-id>/route/upstream` | Feedback or dispatch route. |
| `template` | `agt://<deployment-id>/template/<source-digest>` | Template identity; cache paths are materializations. |
| `secret` | `agt://<deployment-id>/secret/linear-api-key` | Brokered secret handle, never the secret value. |

Human-facing config may still use local aliases:

```toml
[feedback.routes.agent-team]
type = "deployment"
deployment = "agent-team"
```

The local registry resolves `agent-team` to a transport endpoint and records the
canonical deployment id after the first successful handshake:

```toml
[deployments.agent-team]
id = "6a9e5a16-5bde-4f36-8a17-6ea69bde2470"
transport = "http"
url = "http://127.0.0.1:53117"
token_file = "/Users/me/project/.agent_team/state/manager/capability.token"

# Compatibility-only local materialization. Do not use as durable identity.
root = "/Users/me/project"
socket = "/Users/me/project/.agent_team/daemon.sock"
```

## Transport And Capabilities

Resource identity and transport are separate.

- A resource URI says what is being addressed.
- A deployment registry entry says how to reach the daemon that owns it.
- A capability token says which verbs the caller may perform on which resources.

Transport priority for local deployments should be:

1. Loopback HTTP with a bearer capability token.
2. Unix-socket HTTP for trusted local shells and compatibility.
3. Direct file access only for daemon-owned storage or explicitly local
   fallbacks.

For sandboxed Codex workers, loopback HTTP is not optional. The security model
notes that macOS Seatbelt blocks AF_UNIX, so `AGENT_TEAM_DAEMON_SOCKET` cannot be
the only brokered-verb path. `AGENT_TEAM_DAEMON_URL` may expose the endpoint, but
the token should be passed as a file or runtime secret mount, not as a raw env
value. A future remote transport can replace loopback HTTP with mTLS while
preserving the same resource URIs and capability claims.

Suggested capability shape:

```json
{
  "subject": "agt://<deployment-id>/instance/platform-worker-squ-128",
  "audience": "agt://<deployment-id>/project/<deployment-id>",
  "verbs": ["job.gate.set", "mailbox.read", "mailbox.ack", "message.send"],
  "resources": [
    "agt://<deployment-id>/job/squ-128",
    "agt://<deployment-id>/mailbox/platform-worker-squ-128",
    "agt://<deployment-id>/state/platform-worker-squ-128"
  ],
  "expires_at": "2026-07-05T18:00:00Z"
}
```

## What Stays A Path

Paths remain valid for materialization inside one placement boundary:

- The daemon may store its local database under `.agent_team/daemon/`,
  `.agent_team/jobs/`, `.agent_team/outbox/`, and related directories.
- A runtime may receive a local workspace path, state path, cache path, and temp
  path for the process it is about to run.
- Git still needs a host path for `git -C`, worktree creation, branch cleanup,
  and live-process checks.
- Template pull still needs a local cache directory.
- Snapshot output still takes a user-chosen local path.
- Debug records may keep local paths as redacted `local_path` fields.

Paths should not be the only durable identity when the value is written into a
job record, metadata record, snapshot, PM comment footer, route, or cross-process
dispatch payload. In those cases, persist both:

```toml
workspace_uri = "agt://<deployment-id>/workspace/ws_01j..."
workspace_path = "/Users/me/project/.claude/worktrees/worker-squ-128-abc123"
workspace_path_scope = "host-local"
```

Consumers must treat `*_path` fields as hints. The daemon that owns the URI is
the authority for whether the resource still exists and how to operate on it.

## Migration Plan

1. Land SQU-127 deployment registry and route v2. Keep `root` route syntax as a
   compatibility shorthand, but resolve it to a deployment name plus transport.
2. Extend the loopback bearer-token transport into a capability registry. Keep
   Unix sockets for trusted local compatibility.
3. Add URI fields next to path fields in metadata, jobs, snapshots, usage
   records, and dispatch payloads. Backfill URIs from `[project].id`, existing
   instance names, job ids, and generated workspace ids.
4. Move direct agent/skill reads of `.agent_team/daemon`, mailbox files,
   channels, jobs, and gates behind daemon API verbs. File reads remain as
   local-only fallback for no-daemon mode.
5. Replace `.env` path scanning in skills with daemon-brokered secret handles.
   A local `.env` import can remain a developer convenience outside sandboxed or
   remote placements.
6. Introduce workspace leases. A lease records URI, kind (`repo`, `worktree`,
   `container`), local mount path, branch, cleanup policy, and owning job.
7. Place L2 container workers by mounting only the workspace lease and state
   resource, plus a token file and daemon endpoint. The container sees paths, but
   the control plane stores URIs.

## Audit Classification

- `trivial`: the path is an internal local materialization or can move behind an
  existing helper without changing the model.
- `needs-transport`: the code assumes local Unix socket or local filesystem
  communication between processes. It needs the deployment registry, loopback
  HTTP, token auth, or a daemon API verb.
- `needs-resource-id`: the code persists, exports, or routes using a path as
  identity. It needs a URI/stable id alongside any local path hint.

## Audit Appendix

| Area | Evidence | Class | Same-machine/path assumption | Migration note |
| --- | --- | --- | --- | --- |
| Daemon target | `cmd/agent-teamd/main.go:33`, `cmd/agent-teamd/main.go:44`, `cmd/agent-teamd/main.go:48` | needs-resource-id | The daemon starts from a repo-root path and derives `.agent_team`. | Registry entries should resolve a deployment id to endpoint and optional root path. |
| Daemon config | `internal/daemon/daemon.go:33`, `internal/daemon/daemon.go:34`, `internal/daemon/daemon.go:37` | needs-resource-id | `TeamDir` is the root identity for socket, metadata, and instance dirs. | Keep `TeamDir` as local materialization; add deployment id/URI to daemon config and responses. |
| Unix socket path | `internal/daemon/daemon.go:58`, `internal/daemon/daemon.go:62`, `internal/daemon/daemon.go:68` | needs-transport | The daemon address is a filesystem path under `.agent_team` or `/tmp`. | Resolve through deployment registry; prefer HTTP+token in sandboxed workers. |
| Daemon local store | `internal/daemon/daemon.go:83`, `internal/daemon/daemon.go:88`, `internal/daemon/daemon.go:93` | trivial | Daemon metadata, logs, and HTTP address are stored under `TeamDir`. | This can remain daemon-private storage if external readers use API/URIs. |
| Unix listener | `internal/daemon/daemon.go:234`, `internal/daemon/daemon.go:242`, `internal/daemon/daemon.go:254` | needs-transport | The primary API listener is `net.Listen("unix", socket)`. | Keep for local compatibility; do not require it for sandbox/container workers. |
| TCP listener | `internal/daemon/daemon.go:257`, `internal/daemon/daemon.go:259`, `internal/daemon/daemon.go:269` | needs-transport | Loopback HTTP exists but is address-only and file-advertised through `http.addr`. | Add capability-token auth and registry registration. |
| CLI daemon client | `internal/cli/client.go:37`, `internal/cli/client.go:52`, `internal/cli/client.go:57`, `internal/cli/client.go:64` | needs-transport | CLI probes pidfile/socket and dials Unix directly. | Client construction should resolve a deployment endpoint and attach credentials. |
| Daemon CLI docs | `internal/cli/daemon.go:24`, `internal/cli/daemon.go:35` | needs-transport | User-facing daemon contract centers `.agent_team/daemon.sock`. | Update once deployment registry and tokenized HTTP are first-class. |
| Repo root resolution | `internal/cli/instance.go:3710`, `internal/cli/instance.go:3718`, `internal/cli/instance.go:3728` | needs-resource-id | Repo-scoped commands discover the control plane from cwd or `AGENT_TEAM_ROOT`. | Accept deployment names/URIs in addition to local roots. |
| Team dir normalization | `internal/cli/instance.go:3731`, `internal/cli/instance.go:3746`, `internal/cli/instance.go:3788`, `internal/cli/instance.go:3855` | trivial | Local commands canonicalize `.agent_team` with abs/eval-symlink paths. | Keep as local path hygiene behind a resolver. |
| Git primary repo detection | `internal/cli/instance.go:3800`, `internal/cli/instance.go:3852` | needs-resource-id | Commands infer primary repo root from linked worktree `.git` internals. | Workspace leases should record primary repo URI and local materialization. |
| Run state dir | `internal/cli/run.go:253`, `internal/cli/run.go:260` | needs-resource-id | Instance state is `TeamDir/state/<instance>` and exported as a path. | Persist `state` URI; pass path only to the local process. |
| Run runtime dir | `internal/cli/run.go:292`, `internal/cli/run.go:306`, `internal/cli/run.go:331` | trivial | Runtime prompt and staged skills live in a temp/local dir. | Local materialization can remain process-scoped. |
| Run env | `internal/cli/run.go:345`, `internal/cli/run.go:349`, `internal/cli/run.go:352` | needs-transport | Launched runtimes receive `AGENT_TEAM_ROOT`, `AGENT_TEAM_STATE_DIR`, and socket/URL env. | Add resource URIs and token-file env; keep paths as placement-local. |
| Run OTel/worktree context | `internal/cli/run.go:359`, `internal/cli/run.go:370` | needs-resource-id | Runtime context records branch/worktree from env or target path. | OTel resource attrs should include workspace URI and optional redacted path. |
| Runtime dispatch payload | `internal/cli/run.go:431`, `internal/cli/run.go:446` | needs-resource-id | `/v1/dispatch` payload carries a workspace path. | Dispatch should accept workspace URI or lease request, with path as local hint. |
| Daemon spawn workspace | `internal/daemon/event.go:1651`, `internal/daemon/event.go:1656`, `internal/daemon/event.go:1660` | needs-resource-id | Event dispatch defaults to repo root or creates a local worktree path. | Create a workspace lease and store `workspace_uri` before launch. |
| Dispatch env | `internal/daemon/event.go:2264`, `internal/daemon/event.go:2293`, `internal/daemon/event.go:2297` | needs-resource-id | Child env exports branch and host worktree path. | Also export workspace URI; make path a placement-local convenience. |
| Ephemeral state/config | `internal/daemon/event.go:2313`, `internal/daemon/event.go:2317`, `internal/daemon/event.go:2330` | needs-resource-id | Runtime config is rendered into a path under `.agent_team/state`. | State URI should identify ownership; daemon keeps path private or mounted. |
| Ephemeral daemon env | `internal/daemon/event.go:2340`, `internal/daemon/event.go:2344`, `internal/daemon/event.go:2347` | needs-transport | Worker receives root, state path, Unix socket, and optional URL. | URL+token should be sufficient; socket/root become compatibility values. |
| Template rerender | `internal/daemon/event.go:2357`, `internal/daemon/event.go:2363`, `internal/daemon/event.go:2377`, `internal/daemon/event.go:2386` | trivial | `.tmpl` files are walked and rendered from local team dir. | Keep as daemon-local template materialization. |
| Skill staging | `internal/daemon/event.go:2447`, `internal/daemon/event.go:2451`, `internal/daemon/event.go:2456` | trivial | Skills are staged with local symlinks into runtime dir. | OK inside one placement; remote runners should receive a packaged runtime bundle. |
| Kickoff state path | `internal/daemon/event.go:2478`, `internal/daemon/event.go:2485`, `internal/daemon/event.go:2487` | needs-resource-id | Prompt includes relative and absolute state-dir paths. | Include state URI; absolute path should be local-process detail. |
| Codex cwd | `internal/daemon/event.go:2523`, `internal/daemon/event.go:2541` | needs-resource-id | Codex is launched with `-C <workspace path>`. | Runtime launch needs local mount path, but job identity should be workspace URI. |
| Worktree creation | `internal/daemon/event.go:2610`, `internal/daemon/event.go:2617`, `internal/daemon/event.go:2621` | needs-resource-id | Worktrees always live under `<repoRoot>/.claude/worktrees`. | Workspace lease should own path, branch, cleanup, and placement. |
| Worktree scratch exclude | `internal/daemon/event.go:2644`, `internal/daemon/event.go:2656` | trivial | Worker scratch is excluded through the linked worktree git dir. | Local git hygiene can stay path-based. |
| Repo parent helper | `internal/daemon/event.go:2688`, `internal/daemon/event.go:2697` | needs-resource-id | Repo root is inferred as parent of `.agent_team`. | Deployment metadata should carry project/workspace identity separately. |
| Job record | `internal/job/job.go:50`, `internal/job/job.go:64`, `internal/job/job.go:65` | needs-resource-id | Durable jobs store branch and worktree path directly. | Add branch/workspace URI fields; keep path as local cleanup hint. |
| Job step snapshot | `internal/job/job.go:105`, `internal/job/job.go:112`, `internal/job/job.go:116` | needs-resource-id | Pipeline step stores workspace string and instance name without deployment URI. | Store instance/job/workspace URIs for cross-deployment pipelines. |
| Job files | `internal/job/job.go:162`, `internal/job/job.go:168` | trivial | Job TOML path is under `TeamDir/jobs`. | Daemon-private storage can remain path-backed. |
| Gate ledger | `internal/job/gates.go:65`, `internal/job/gates.go:67` | trivial | Gates append to `.agent_team/jobs/<id>.gates.jsonl`. | Keep local ledger; expose through daemon API/resource URI. |
| Job events | `internal/job/events.go:33`, `internal/job/events.go:62`, `internal/job/events.go:64` | trivial | Job events append to `.agent_team/jobs/<id>.events.jsonl`. | Keep local ledger; API readers should not require the path. |
| Approvals | `internal/job/approvals.go:48`, `internal/job/approvals.go:54` | trivial | Approvals are JSON files below `.agent_team/jobs/<job>/approvals`. | Keep as local store behind approval resource APIs. |
| Metadata record | `internal/daemon/metadata.go:27`, `internal/daemon/metadata.go:36`, `internal/daemon/metadata.go:37` | needs-resource-id | Instance metadata persists workspace and log paths. | Add instance/workspace/log resource URIs; mark paths host-local. |
| Metadata path | `internal/daemon/metadata.go:61`, `internal/daemon/metadata.go:69`, `internal/daemon/metadata.go:71` | trivial | Metadata JSON files live under daemon root by instance. | Local store is fine once API is authoritative. |
| Launch env snapshot | `internal/daemon/launchenv.go:17`, `internal/daemon/launchenv.go:29`, `internal/daemon/launchenv.go:41`, `internal/daemon/launchenv.go:55` | needs-resource-id | Launch env records binary, args, cwd, env, and per-instance path files. | Preserve for diagnostics but add deployment/runtime URI and stronger redaction. |
| Launch env secrets | `internal/daemon/launchenv.go:127`, `internal/daemon/launchenv.go:153`, `internal/daemon/launchenv.go:177`, `internal/daemon/launchenv.go:184` | needs-transport | Env snapshots strip a small secret set and write files directly. | Token/secret broker reduces secret env; redaction should be policy-driven. |
| Mailbox store | `internal/daemon/mailbox.go:19`, `internal/daemon/mailbox.go:20`, `internal/daemon/mailbox.go:197`, `internal/daemon/mailbox.go:203` | needs-transport | Mailbox is a JSONL file under daemon root and cursors are files. | Daemon API should be the normal read/ack surface; file path is private. |
| Queue store | `internal/daemon/queue.go:57`, `internal/daemon/queue.go:69`, `internal/daemon/queue.go:80`, `internal/daemon/queue.go:147` | trivial | Queue items are JSON files under daemon root. | Daemon-private local implementation. |
| Outbox store | `internal/daemon/outbox.go:57`, `internal/daemon/outbox.go:73`, `internal/daemon/outbox.go:88`, `internal/daemon/outbox.go:207` | needs-transport | Agents can write outbox files when transport is unavailable. | Replace with tokenized HTTP or an explicit local-only spool API. |
| Lock ledger | `internal/daemon/locks.go:52`, `internal/daemon/locks.go:60`, `internal/daemon/locks.go:64`, `internal/daemon/locks.go:123` | trivial | Lock leases are local JSON files under daemon root. | Keep local store; lock names may later become resource URIs for machine/team scopes. |
| Schedule state | `internal/daemon/schedule_state.go:21`, `internal/daemon/schedule_state.go:25`, `internal/daemon/schedule_state.go:29`, `internal/daemon/schedule_state.go:82` | trivial | Schedule clocks are files under daemon root. | Local daemon store; route schedule operations through API if remote. |
| Channels | `internal/daemon/channel.go:107`, `internal/daemon/channel.go:109`, `internal/daemon/channel.go:471`, `internal/daemon/channel.go:501` | needs-transport | Channel ledgers and subscriptions are daemon-root files. | Agents should use channel API over deployment transport. |
| Budget allocations | `internal/budget/allocations.go:88`, `internal/budget/allocations.go:92`, `internal/budget/allocations.go:96`, `internal/budget/allocations.go:130` | trivial | Budget allocation records and locks live below team dir. | Local store is fine; capability checks should gate mutation APIs. |
| Status writer | `template/skills/status/scripts/status.sh:33`, `template/skills/status/scripts/status.sh:41`, `template/skills/status/scripts/status.sh:116` | needs-resource-id | Status skill writes/reads `$AGENT_TEAM_STATE_DIR/status.toml` directly. | State path can remain local; status resource URI should identify owner. |
| Status reader | `internal/cli/instance_status.go:93`, `internal/cli/instance_status.go:128`, `internal/cli/instance_status.go:142` | needs-transport | `instance ps` walks `.agent_team/state/*/status.toml`. | Local CLI can keep fallback; remote status should come from daemon API. |
| Snapshot root paths | `internal/cli/snapshot.go:79`, `internal/cli/snapshot.go:83`, `internal/cli/snapshot.go:225`, `internal/cli/snapshot.go:226` | needs-resource-id | Snapshot records repo and team dir as slashified paths. | Add deployment URI/id; redact or mark paths host-local. |
| Snapshot provenance | `internal/cli/snapshot.go:507`, `internal/cli/snapshot.go:515`, `internal/cli/snapshot.go:519` | needs-resource-id | Snapshot provenance captures command/scope/subject but not resource URI. | Add subject URI when available. |
| Snapshot git info | `internal/cli/snapshot.go:682`, `internal/cli/snapshot.go:729` | trivial | Git info is collected with `git -C <repoRoot>`. | Local diagnostic action; URI belongs in snapshot header. |
| Snapshot output | `internal/cli/snapshot.go:929`, `internal/cli/snapshot.go:941`, `internal/cli/snapshot.go:945` | trivial | User-selected output file is a local path. | Keep path local; not resource identity. |
| Usage source | `internal/usage/usage.go:56`, `internal/usage/usage.go:62`, `internal/usage/usage.go:83`, `internal/usage/usage.go:107` | needs-resource-id | Usage record source is runtime log path. | Add runtime/log URI and keep path as diagnostic hint. |
| Origin envelope | `internal/origin/origin.go:21`, `internal/origin/origin.go:23`, `internal/origin/origin.go:27`, `internal/origin/origin.go:80` | needs-resource-id | Provenance has project/team/instance/job fields but no URI or deployment alias. | Keep fields; add canonical resource URI or deployment id for cross-system joins. |
| Project id | `internal/origin/origin.go:261`, `internal/origin/origin.go:268`, `internal/origin/origin.go:285`, `internal/origin/origin.go:302` | trivial | Stable project id already lives in local config. | Use this as deployment id authority in URIs. |
| Linear write-back config | `internal/pmprovider/linear.go:275`, `internal/pmprovider/linear.go:277`, `internal/pmprovider/linear.go:456`, `internal/pmprovider/linear.go:482` | needs-transport | PM provider reads config and `.env` candidates by local path. | Config can be daemon local; secrets should come from brokered secret handles. |
| Linear worktree `.env` | `internal/pmprovider/linear.go:496`, `internal/pmprovider/linear.go:498`, `internal/pmprovider/linear.go:505` | needs-resource-id | Helper discovers main worktree path to find `.env`. | Replace with secret broker; worktree path should not be credential location. |
| GitHub write-back config | `internal/pmprovider/github.go:222`, `internal/pmprovider/github.go:224` | needs-transport | GitHub provider reads `.agent_team/config.toml` by teamDir path. | OK for daemon-local code; remote workers should call provider through daemon or broker. |
| Runtime mailbox hook | `internal/runtimehooks/mailbox.go:135`, `internal/runtimehooks/mailbox.go:140`, `internal/runtimehooks/mailbox.go:141`, `internal/runtimehooks/mailbox.go:148` | needs-transport | Hook reads mailbox files directly through `AGENT_TEAM_ROOT`. | Hook should call daemon API with capability token; file fallback local only. |
| Inbox skill transport | `template/skills/inbox/scripts/inbox.sh:26`, `template/skills/inbox/scripts/inbox.sh:40`, `template/skills/inbox/scripts/inbox.sh:48`, `template/skills/inbox/scripts/inbox.sh:100` | needs-transport | Inbox prefers URL, else Unix socket, else root-derived socket. | Make URL+token primary; avoid socket/root in sandboxed runtimes. |
| Inbox direct read | `template/skills/inbox/scripts/_inbox_read.py:2`, `template/skills/inbox/scripts/_inbox_read.py:28`, `template/skills/inbox/scripts/_inbox_read.py:32` | needs-transport | `check` and `ack` read/write mailbox files under `$AGENT_TEAM_ROOT/daemon`. | Replace with API; keep direct file only for no-daemon local mode. |
| Channel skill transport | `template/skills/channel/scripts/channel.sh:30`, `template/skills/channel/scripts/channel.sh:44`, `template/skills/channel/scripts/channel.sh:52`, `template/skills/channel/scripts/channel.sh:80` | needs-transport | Channel helper requires `AGENT_TEAM_ROOT` and URL/socket transport. | Tokenized deployment endpoint should be sufficient. |
| Channel receive helper | `template/skills/channel/scripts/_channel_recv.py:32`, `template/skills/channel/scripts/_channel_recv.py:47`, `template/skills/channel/scripts/_channel_recv.py:51` | needs-transport | Polling helper derives socket path from root when URL absent. | Same as channel transport. |
| Assign-worker helper | `template/agents/manager/skills/assign-worker/scripts/assign_worker.sh:20`, `template/agents/manager/skills/assign-worker/scripts/assign_worker.sh:27`, `template/agents/manager/skills/assign-worker/scripts/assign_worker.sh:51`, `template/agents/manager/skills/assign-worker/scripts/assign_worker.sh:65` | needs-transport | Dispatch helper needs root, socket/URL, or writes an outbox file. | Dispatch should target a deployment name/URI with tokenized API. |
| Assign-worker payload | `template/agents/manager/skills/assign-worker/scripts/assign_worker.sh:227`, `template/agents/manager/skills/assign-worker/scripts/assign_worker.sh:234`, `template/agents/manager/skills/assign-worker/scripts/assign_worker.sh:240` | needs-resource-id | Payload uses target/name/job/ticket/workspace strings without deployment/resource URIs. | Add target deployment URI and workspace lease request. |
| Linear skill config | `template/skills/linear/scripts/linear-graphql.sh:96`, `template/skills/linear/scripts/linear-graphql.sh:104`, `template/skills/linear/scripts/linear-graphql.sh:130`, `template/skills/linear/scripts/linear-graphql.sh:186` | needs-transport | Skill reads `.agent_team/config.toml` and `$PWD/.env`. | Config should come from state/config resource; secrets from broker. |
| Linear skill main `.env` | `template/skills/linear/scripts/linear-graphql.sh:189`, `template/skills/linear/scripts/linear-graphql.sh:190`, `template/skills/linear/scripts/linear-graphql.sh:207` | needs-resource-id | Skill discovers main worktree path for `.env`. | Remove worktree coupling from credential lookup. |
| Linear origin footer | `template/skills/linear/scripts/linear-graphql.sh:230`, `template/skills/linear/scripts/linear-graphql.sh:240`, `template/skills/linear/scripts/linear-graphql.sh:246` | needs-resource-id | Footer builds provenance from config path and env fields. | Include canonical resource URI/deployment id in footer. |
| GitHub skill config | `template/skills/github/scripts/github-api.sh:30`, `template/skills/github/scripts/github-api.sh:38`, `template/skills/github/scripts/github-api.sh:110`, `template/skills/github/scripts/github-api.sh:134` | needs-transport | Skill reads config and token from local `.env`. | Same broker/config-resource migration as Linear. |
| GitHub skill main `.env` | `template/skills/github/scripts/github-api.sh:113`, `template/skills/github/scripts/github-api.sh:114`, `template/skills/github/scripts/github-api.sh:115` | needs-resource-id | Skill uses main worktree path to locate credentials. | Replace with secret broker. |
| Worker prompt worktree | `template/agents/worker/agent.md:78`, `template/agents/worker/agent.md:82`, `template/agents/worker/agent.md:87`, `template/agents/worker/agent.md:89` | needs-resource-id | Worker instructions assume `.claude/worktrees` layout and parent `.env` path. | Prompt should discuss workspace lease/state URI and brokered secrets. |
| Worker prompt gates | `template/agents/worker/agent.md:119`, `template/agents/worker/agent.md:144`, `template/agents/worker/agent.md:150` | needs-resource-id | Worker derives main repo from `git worktree list` and reports path to job step. | Job step update should accept repo/workspace URIs. |
| Worker cleanup | `template/agents/worker/agent.md:202`, `template/agents/worker/agent.md:207`, `template/agents/worker/agent.md:211` | needs-resource-id | Cleanup removes a path and branch directly from inferred main repo. | Cleanup should operate on workspace lease URI. |
| Worktree cleanup | `internal/worktreecleanup/cleanup.go:25`, `internal/worktreecleanup/cleanup.go:37`, `internal/worktreecleanup/cleanup.go:45`, `internal/worktreecleanup/cleanup.go:75` | needs-resource-id | Cleanup trusts job `Worktree` path and validates under `.claude/worktrees`. | Lease-owned cleanup can keep path validation locally, keyed by workspace URI. |
| Worktree live check | `internal/worktreecleanup/cleanup.go:119`, `internal/worktreecleanup/cleanup.go:177` | trivial | Live-process checks inspect local paths through `/proc` or `lsof`. | Local host safety check remains path-based. |
| Feedback route schema | `internal/feedback/routes.go:13`, `internal/feedback/routes.go:28`, `internal/feedback/routes.go:31`, `internal/feedback/routes.go:52` | needs-resource-id | Route identity uses `type=local` plus root path. | SQU-127 route v2 should use deployment names and endpoint registry. |
| Feedback route resolution | `internal/feedback/routes.go:57`, `internal/feedback/routes.go:60`, `internal/feedback/routes.go:64` | needs-resource-id | Relative roots are resolved against the current repo path and cleaned. | Local root can remain shorthand that registers/resolves a deployment. |
| Feedback delivery | `internal/cli/feedback.go:89`, `internal/cli/feedback.go:97`, `internal/cli/feedback.go:98` | needs-transport | Delivery joins route root with `.agent_team` and opens daemon client there. | Deliver to deployment endpoint with token/capability. |
| Feedback config example | `template/config.toml.example:70`, `template/config.toml.example:71`, `template/config.toml.example:75` | needs-resource-id | Example says a route dials `<root>/.agent_team/daemon.sock`. | Update example after SQU-127. |
| Template cache root | `internal/cli/template.go:20`, `internal/cli/template.go:23`, `internal/cli/template.go:24`, `internal/cli/template.go:35` | needs-resource-id | Pulled templates live under user-home cache path. | Template URI should be source+resolved digest; cache path is materialization. |
| Template resolver | `internal/template/ref.go:57`, `internal/template/ref.go:62`, `internal/template/ref.go:78`, `internal/template/ref.go:87` | needs-resource-id | Non-local refs resolve by joining cache root and ref. | Persist resolved template identity separate from local cache location. |
| Local template refs | `internal/template/ref.go:103`, `internal/template/ref.go:108`, `internal/template/ref.go:118` | trivial | Local path refs are canonicalized and loaded from disk. | Keep for developer workflow; mark local-only/non-portable in locks. |
| Git template cache | `internal/cli/template_git.go:325`, `internal/cli/template_git.go:340`, `internal/cli/template_git.go:363`, `internal/cli/template_git.go:373` | needs-resource-id | Cache records return local paths as pulled template location. | Result should include template URI/digest and optional cache path. |
| Git template fetch | `internal/cli/template_git.go:506`, `internal/cli/template_git.go:510`, `internal/cli/template_git.go:546` | trivial | Git fetch needs a temp/cache path and `git -C`. | Local implementation path. |
| Template run cache | `internal/cli/template_run.go:17`, `internal/cli/template_run.go:21`, `internal/cli/template_run.go:25`, `internal/cli/template_run.go:27` | trivial | Temporary template runs use XDG/home/temp paths. | Local materialization only. |
| Topology route docs | `documentation/topology.md:781`, `documentation/topology.md:788` | needs-resource-id | Topology dispatch examples describe worktree path/env and socket export. | Update docs after URI/workspace lease fields exist. |
| Orchestrator API docs | `documentation/orchestrator.md:100`, `documentation/orchestrator.md:322`, `documentation/orchestrator.md:334` | needs-transport | Current architecture docs center Unix socket and path-exported runtime state. | Update docs after tokenized transport and resource URI model land. |
| Security model known gap | `documentation/security-model.md:94` | needs-transport | Security model records AF_UNIX denial and need for TCP-loopback+token. | This is the required transport foundation before distributed workers. |
