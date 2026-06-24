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
agent-team runtime probe --runtime codex --require-daemon
agent-team runtime probe --runtime codex --require-daemon --wait-daemon --timeout 10s
agent-team runtime probe --runtime codex --start-daemon --require-daemon
agent-team runtime probe --runtime codex --format '{{.OK}} {{len .Issues}}'
agent-team runtime probe --runtime codex --exec --timeout 2m
agent-team runtime probe --runtime codex --exec --timeout 2m --output runtime-probe.json
```

The probe combines the selected runtime profile, daemon readiness, daemon socket
path, and action hints. For Codex it also runs `codex doctor --json` with a
timeout, so provider reachability, auth, MCP, and sandbox failures are visible
before jobs or pipelines queue work against a runtime that cannot start. By
default, a stopped daemon is a warning because direct runs can still work; add
`--require-daemon` when the preflight is for daemon-backed dispatch, mailbox, or
channel flows and should fail until `agent-teamd` is ready. Add `--wait-daemon`
to poll readiness first; `--timeout` bounds both that wait and runtime-native
diagnostics. Add `--start-daemon` when the preflight should start the detached
repo daemon if it is not ready; without that flag the probe remains read-only.
`--exec` is opt-in because it spends a real runtime call: for Codex it runs
`codex exec -`, sends a short prompt over stdin, and verifies that
`--output-last-message` produced a sidecar. Add `--output <file>` to write the
full structured probe result as pretty JSON while still printing the normal
text or `--json` response. Exec probe failures are classified into actionable
IDs such as `provider_unreachable`, `auth_failed`, `sandbox_blocked`,
`exec_timeout`, and last-message sidecar failures.

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

When you need raw Codex logs for debugging but do not want startup/plugin/MCP
reconnect diagnostics mixed into the useful output, add `--clean`:

```sh
agent-team logs <instance> --clean --tail 200
agent-team job logs <job-id> --clean --grep "error"
agent-team team logs <team> --clean
```

Use attach dry-runs to discover the direct resume command without transferring
daemon ownership:

```sh
agent-team attach <instance> --dry-run
```

For Codex metadata, the preview includes the unmanaged `codex resume <session>`
command plus `logs --follow` and `logs --last-message` fallbacks. Non-dry-run
`attach` still refuses Codex managed handoff so it does not stop a child it
cannot later supervise with the same session contract.

Use `runtime resume-plan` when you want the same guidance without contacting
the daemon:

```sh
agent-team runtime resume-plan
agent-team runtime resume-plan worker-squ-42
agent-team runtime resume-plan --job squ-42
agent-team runtime resume-plan --runtime codex --status exited
agent-team runtime resume-plan --action resume --format '{{.Instance}} {{.RecommendedAction}} {{.RecommendedCommand}}'
agent-team runtime resume-plan --json
agent-team team runtime resume-plan delivery --status crashed
agent-team team runtime resume-plan delivery --action logs
```

`agent-team overview` also summarizes runtime metadata and links crashed
instances to `runtime resume-plan`; `agent-team team overview <team>` and
`agent-team team next <team> --source runtime` use
`agent-team team runtime resume-plan <team>` for team-scoped recovery.

The command reads `.agent_team/daemon/*/meta.json` directly and prints the
recommended action plus managed start, attach dry-run, unmanaged runtime resume,
log follow, and Codex last-message commands. Job-linked metadata also includes
`job attach` and `job logs` variants so recovery can stay scoped to the durable
work unit. Use `--action start|attach|resume|logs` when scripts or operators
only need one recovery class.

The Codex adapter sets `AGENT_TEAM_*` variables through Codex shell-environment policy options, so status, inbox, and channel scripts can find the repo team root and state directory without broadly inheriting the parent process environment.

## Codex Limitations

Codex does not expose the same `--agents` and `--session-id` contract as the Claude profile.

That means:

- native runtime subagents are not registered
- direct `codex resume` is available only outside agent-team managed instance ownership; use `agent-team attach <instance> --dry-run` to print the exact command
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
| `runtime "codex" does not support managed resume` | Codex metadata cannot be started or restarted through managed daemon resume | Run `agent-team attach <instance> --dry-run` to print unmanaged resume/log commands, or re-run with a fresh `--prompt` |
| Tool scripts cannot find state | Missing `AGENT_TEAM_*` environment in runtime shell | Check `agent-team runtime` and inspect the daemon child log |
| Codex exits before running any task | Codex auth, provider reachability, sandbox setup, stdin handling, or last-message capture is broken | `agent-team runtime probe --runtime codex --json`, then `agent-team runtime probe --runtime codex --exec --timeout 2m` |

## Observed Probe Findings

2026-06-24 local probe:

```sh
agent-team runtime probe --runtime codex --exec --timeout 2m --json
```

Result:

- `runtime.available = true` for `/opt/homebrew/bin/codex`
- `codex_doctor` failed `network.provider_reachability` for
  `https://chatgpt.com/backend-api/`
- WebSocket reachability warned with DNS lookup failure for `chatgpt.com`
- Codex plugin/update sync also hit DNS failures for `github.com` and
  `api.github.com`
- `exec_probe` failed with `provider_unreachable` and did not produce a
  last-message sidecar
- daemon readiness was a warning only because the probe was run without
  `--start-daemon` / `--require-daemon`

Action: fix DNS/proxy/VPN/provider reachability first, then rerun the same
probe. Use `--start-daemon --require-daemon` when validating daemon-backed
dispatch readiness in the same pass.

## Adapter Design Notes

New runtime profiles should preserve the repo-local contract:

- `.agent_team/` remains the source of truth
- `AGENT_TEAM_ROOT`, `AGENT_TEAM_INSTANCE`, and `AGENT_TEAM_STATE_DIR` are available to tool scripts
- read-only inspection works from local files when the daemon is down
- daemon-managed work writes logs and metadata under `.agent_team/daemon/`
- unsupported lifecycle actions report explicit `unsupported` results instead of silently doing nothing
