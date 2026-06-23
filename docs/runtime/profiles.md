# Runtime Profiles

`agent-team` can launch more than one LLM runtime, but not every runtime supports the same lifecycle contract.

Use this page when choosing a runtime, configuring `.agent_team/config.toml`, or debugging why a command is marked unsupported.

## Inspect the Active Runtime

```sh
agent-team runtime
agent-team runtime --json
agent-team runtime --format '{{.Runtime}} {{.Available}} {{.DirectResume}} {{.ManagedResume}} {{.Subagents}}'
```

The command reports:

- selected runtime kind
- binary name and resolved path
- repo config source
- environment overrides
- direct-run, daemon-dispatch, direct-resume, managed-resume, and subagent capabilities
- adapter notes and missing-binary warnings

## Selection Order

Runtime selection is deterministic:

1. `AGENT_TEAM_RUNTIME`
2. `.agent_team/config.toml` `[runtime].kind`
3. built-in default, `claude`

Binary selection is:

1. `AGENT_TEAM_RUNTIME_BIN`
2. `.agent_team/config.toml` `[runtime].binary` or `[runtime].bin`, but only when `AGENT_TEAM_RUNTIME` is not set
3. built-in default for the selected runtime

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

## Capability Matrix

| Capability | Claude profile | Codex profile |
| --- | --- | --- |
| Direct interactive `run` | yes | yes |
| Daemon-managed one-shot `run --prompt` | yes | yes |
| Direct CLI resume outside daemon ownership | yes | yes |
| Native subagent registry | yes | no |
| Managed resume/start | yes | no |
| `attach` resume flow | yes | limited to direct process attachment |
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

One-shot daemon run:

```sh
AGENT_TEAM_RUNTIME=codex agent-team run worker \
  --prompt "summarize the current job status" \
  --detach \
  --json
```

For one-shot runs, the adapter uses `codex exec -` and sends the assembled agent prompt over stdin. This avoids placing large prompts in argv.

Codex daemon runs also capture:

```text
.agent_team/state/<instance>/last-message.txt
```

Read the clean final answer with:

```sh
agent-team logs <instance> --last-message
```

The Codex adapter sets `AGENT_TEAM_*` variables through Codex shell-environment policy options, so status, inbox, and channel scripts can find the repo team root and state directory without broadly inheriting the parent process environment.

## Codex Limitations

Codex does not expose the same `--agents` and `--session-id` contract as the Claude profile.

That means:

- native runtime subagents are not registered
- direct `codex resume` is available only outside agent-team managed instance ownership
- stopped Codex metadata cannot be resumed with `start`
- `plan` and `sync` report stopped Codex instances as `unsupported` instead of trying to resume them
- daemon dispatch requires `--prompt`, because Codex one-shot work needs an explicit task for `codex exec`

Use jobs, queue, and pipeline commands for orchestration around Codex runs instead of relying on in-session subagent dispatch.

## Troubleshooting

| Symptom | Likely cause | First check |
| --- | --- | --- |
| `available: no` | Runtime binary is not in `PATH` | `agent-team runtime`, then `which codex` or `which claude` |
| Config binary ignored | `AGENT_TEAM_RUNTIME` is set, so config binary is skipped | Set `AGENT_TEAM_RUNTIME_BIN` too, or unset `AGENT_TEAM_RUNTIME` |
| `codex daemon dispatch requires --prompt` | Codex daemon runs need an explicit one-shot task | Add `--prompt "..."` |
| `runtime "codex" does not support managed resume` | Stopped Codex metadata cannot be resumed | Re-run with a fresh `--prompt`, or remove stale metadata after inspection |
| Tool scripts cannot find state | Missing `AGENT_TEAM_*` environment in runtime shell | Check `agent-team runtime` and inspect the daemon child log |

## Adapter Design Notes

New runtime profiles should preserve the repo-local contract:

- `.agent_team/` remains the source of truth
- `AGENT_TEAM_ROOT`, `AGENT_TEAM_INSTANCE`, and `AGENT_TEAM_STATE_DIR` are available to tool scripts
- read-only inspection works from local files when the daemon is down
- daemon-managed work writes logs and metadata under `.agent_team/daemon/`
- unsupported lifecycle actions report explicit `unsupported` results instead of silently doing nothing
