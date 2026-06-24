# Runtime Profiles

`agent-team` can launch more than one LLM runtime, but not every runtime supports the same lifecycle contract.

Use this page when choosing a runtime, configuring `.agent_team/config.toml`, or debugging why a command is marked unsupported.

## Inspect the Active Runtime

```sh
agent-team runtime
agent-team runtime --json
agent-team runtime --format '{{.Runtime}} {{.Available}} {{.DirectResume}} {{.ManagedResume}} {{.Subagents}}'
agent-team runtime --runtime codex --runtime-bin /opt/bin/codex-wrapper
```

The command reports:

- selected runtime kind
- binary name and resolved path
- repo config source
- environment overrides
- direct-run, daemon-dispatch, direct-resume, managed-resume, and subagent capabilities
- adapter notes and missing-binary warnings

## Probe Runtime Health

Use `runtime probe` when you need a dispatch preflight rather than a static profile:

```sh
agent-team runtime probe
agent-team runtime probe --runtime codex
agent-team runtime probe --runtime codex --json
agent-team runtime probe --runtime codex --skip-doctor
```

The probe combines the selected runtime profile, daemon readiness, daemon socket
path, and action hints. For Codex it also runs `codex doctor --json` with a
timeout, so provider reachability, auth, MCP, and sandbox failures are visible
before jobs or pipelines queue work against a runtime that cannot start.

## Selection Order

Runtime selection is deterministic:

1. `--runtime` on commands that inspect, launch, or dispatch a runtime
2. `AGENT_TEAM_RUNTIME`
3. `.agent_team/config.toml` `[runtime].kind`
4. built-in default, `claude`

Binary selection is:

1. `--runtime-bin` on commands that inspect, launch, or dispatch a runtime
2. when `--runtime` is set without `--runtime-bin`, the built-in default for that runtime
3. `AGENT_TEAM_RUNTIME_BIN`
4. `.agent_team/config.toml` `[runtime].binary` or `[runtime].bin`, but only when `AGENT_TEAM_RUNTIME` is not set
5. built-in default for the selected runtime

Example repo default:

```toml
[runtime]
kind = "codex"
binary = "codex"
```

One-off shell override:

```sh
AGENT_TEAM_RUNTIME=codex agent-team runtime
AGENT_TEAM_RUNTIME=codex AGENT_TEAM_RUNTIME_BIN=/opt/bin/codex-wrapper agent-team run worker --prompt "check status"
```

One-off command override:

```sh
agent-team runtime --runtime codex
agent-team run worker --runtime codex --prompt "check status" --last-message
agent-team run worker --runtime codex --runtime-bin /opt/bin/codex-wrapper --prompt "check status" --detach
agent-team template run bundled manager --runtime codex --prompt "check status" --last-message
agent-team dispatch worker SQU-42 --runtime codex --kickoff "implement the ticket"
agent-team job dispatch squ-42 --runtime codex --runtime-bin /opt/bin/codex-wrapper
agent-team pipeline advance ticket_to_pr --runtime codex --dry-run --preview-routes
agent-team team advance delivery --runtime codex --dry-run --preview-routes
```

## Capability Matrix

| Capability | Claude profile | Codex profile |
| --- | --- | --- |
| Direct interactive `run` | yes | yes |
| Daemon-managed one-shot `run --prompt` | yes | yes |
| Direct clean one-shot `run --prompt --last-message` | no | yes |
| Direct CLI resume outside daemon ownership | yes | yes |
| Native subagent registry | yes | no |
| Managed resume/start | yes | no |
| Interactive daemon `attach` resume flow | yes | no; use logs or direct Codex resume outside daemon ownership |
| `logs --last-message` sidecar | no | yes for `codex exec` |
| Worker status, mailbox, channel scripts | yes | yes, through `AGENT_TEAM_*` shell environment policy |

## Claude Profile

The Claude-compatible profile is the default and the fullest lifecycle target.

It launches roughly as:

```sh
claude --agents '<json>' --add-dir <tmpdir> --append-system-prompt-file <kickoff> <forwarded-args>
```

Use it when you need:

- native subagent dispatch inside the runtime session
- managed resume through runtime session IDs
- long-running persistent instances
- the broadest compatibility with existing lifecycle commands

Useful checks:

```sh
AGENT_TEAM_RUNTIME=claude agent-team runtime
agent-team run manager
agent-team run worker --prompt "implement SQU-42" --detach --json
agent-team start manager --wait
```

## Codex Profile

The Codex profile is designed for direct launches and daemon-managed one-shot work.

Interactive direct run:

```sh
AGENT_TEAM_RUNTIME=codex agent-team run manager --no-daemon
```

Clean direct one-shot run:

```sh
AGENT_TEAM_RUNTIME=codex agent-team run manager \
  --prompt "summarize the current job status" \
  --last-message
```

One-shot daemon run:

```sh
AGENT_TEAM_RUNTIME=codex agent-team run worker \
  --prompt "summarize the current job status" \
  --detach \
  --json
```

For one-shot runs, the adapter uses `codex exec -` and sends the assembled agent prompt over stdin. This avoids placing large prompts in argv. `run --prompt --last-message` bypasses the daemon, waits for Codex to exit, suppresses raw Codex stdout/stderr on success, and prints only the captured final response. If Codex exits nonzero, raw stdout/stderr are replayed for diagnosis.

Codex daemon runs also capture:

```text
.agent_team/state/<instance>/last-message.txt
```

Read the clean final answer with:

```sh
agent-team logs <instance> --last-message
agent-team job logs <job-id> --last-message
agent-team team logs <team> --last-message
```

The Codex adapter sets `AGENT_TEAM_*` variables through Codex shell-environment policy options, so status, inbox, and channel scripts can find the repo team root and state directory without broadly inheriting the parent process environment.

## Codex Limitations

Codex does not expose the same `--agents` and `--session-id` contract as the Claude profile.

That means:

- native runtime subagents are not registered
- direct `codex resume` is available only outside agent-team managed instance ownership
- stopped Codex metadata cannot be resumed with `start`
- Codex metadata cannot be restarted through managed daemon resume; `restart` reports `unsupported` and leaves running Codex children untouched
- `plan` and `sync` report stopped Codex instances as `unsupported` instead of trying to resume them
- daemon dispatch requires `--prompt`, because Codex one-shot work needs an explicit task for `codex exec`

Use jobs, queue, and pipeline commands for orchestration around Codex runs instead of relying on in-session subagent dispatch.

## Troubleshooting

| Symptom | Likely cause | First check |
| --- | --- | --- |
| `available: no` | Runtime binary is not in `PATH` | `agent-team runtime`, then `which codex` or `which claude` |
| Config binary ignored | `--runtime`, `AGENT_TEAM_RUNTIME`, or `AGENT_TEAM_RUNTIME_BIN` is taking precedence | Check `agent-team runtime --json`, then unset the env override or pass `--runtime-bin` |
| `codex daemon dispatch requires --prompt` | Codex daemon runs need an explicit one-shot task | Add `--prompt "..."` |
| `runtime "codex" does not support managed resume` | Codex metadata cannot be started or restarted through managed daemon resume | Inspect logs or last message, then re-run with a fresh `--prompt` when more work is needed |
| Tool scripts cannot find state | Missing `AGENT_TEAM_*` environment in runtime shell | Check `agent-team runtime` and inspect the daemon child log |
| Codex exits before running any task | Codex auth, provider reachability, or sandbox setup is broken | `agent-team runtime probe --runtime codex --json`, then `codex doctor --summary` |

## Adapter Design Notes

New runtime profiles should preserve the repo-local contract:

- `.agent_team/` remains the source of truth
- `AGENT_TEAM_ROOT`, `AGENT_TEAM_INSTANCE`, and `AGENT_TEAM_STATE_DIR` are available to tool scripts
- read-only inspection works from local files when the daemon is down
- daemon-managed work writes logs and metadata under `.agent_team/daemon/`
- unsupported lifecycle actions report explicit `unsupported` results instead of silently doing nothing
