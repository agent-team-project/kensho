# Runtime hooks for turn-boundary mailbox injection

SQU-66 investigated whether agent-team can soft-push daemon mailbox messages into an already-running agent without interrupting the runtime. The desired behavior is: a manager sends a message while a worker is running, and the worker receives it as model-visible context at the next prompt/tool boundary.

## Summary

Both supported runtimes now have a viable hook surface for this.

| Runtime | Probe | Context injection support | agent-team behavior |
| --- | --- | --- | --- |
| Claude Code 2.1.199 | `claude --help`; official hooks reference | Yes. `UserPromptSubmit` and `PreToolUse` hooks can return `hookSpecificOutput.additionalContext`. | Generate a per-instance `--settings` JSON that runs the mailbox hook on `UserPromptSubmit` and `PreToolUse`. |
| Codex CLI 0.142.2 | `codex --version`, `codex --help`, `codex exec --help`, `codex features list`, `~/.codex/config.toml`; official Codex hooks docs | Yes. Codex 0.142.2 has stable `hooks`; `UserPromptSubmit` and `PreToolUse` accept `hookSpecificOutput.additionalContext`. | Pass inline `-c hooks.*` config for the generated hook and add `--dangerously-bypass-hook-trust` because the launcher generated and vetted the hook command for this invocation. |

The implementation is intentionally a soft push. It does not preempt an in-flight model response. It drains unread mailbox messages when the runtime reaches a hook boundary, injects them as additional context, then advances the daemon mailbox cursor.

## Claude Code

Claude Code hooks are configured through settings JSON. Its hook reference lists lifecycle events including `UserPromptSubmit` once per turn and `PreToolUse` on every tool call inside the agent loop. The same reference documents `hookSpecificOutput.additionalContext` as model-visible context: Claude Code wraps the returned string as a system reminder and adds it at the hook point.

Relevant source: <https://code.claude.com/docs/en/hooks>

Local probe:

```sh
claude --version
# 2.1.199 (Claude Code)

claude --help
# includes --settings <file-or-json>
```

agent-team uses `--settings <generated-json>` instead of editing the repo's `.claude/settings.json`. The generated settings run the same command hook for:

- `UserPromptSubmit`, so user-submitted or resumed turns get pending mailbox messages before model processing.
- `PreToolUse`, so a message that arrives during an agentic loop is delivered before the next tool call proceeds.

## Codex

Codex 0.142.2 exposes hooks as a stable feature flag and documents hook configuration through `hooks.json` or inline `[hooks]` tables in `config.toml`. Its hook docs list `UserPromptSubmit` and `PreToolUse` as turn-scoped events, and both support `hookSpecificOutput.additionalContext`.

Relevant sources:

- <https://developers.openai.com/codex/hooks>
- <https://developers.openai.com/codex/cli/reference>

Local probes:

```sh
codex --version
# codex-cli 0.142.2

codex features list
# hooks stable true

codex --help
codex exec --help
# both include --dangerously-bypass-hook-trust

sed -n '1,260p' ~/.codex/config.toml
# local user config uses notify = [..., "turn-ended"], but no hook tables
```

Codex requires trust review before non-managed command hooks run. agent-team uses `--dangerously-bypass-hook-trust` only for the generated mailbox hook configuration it passes to the runtime invocation. This avoids requiring persisted trust for a per-instance hook script whose path changes per launch.

## Opt-out

Mailbox hook injection defaults on. Disable it in any resolved config layer:

```toml
[runtime.hooks]
mailbox_injection = false
```

Because `agent-team run` resolves repo config, topology instance config, per-instance state config, and `--set` before launch, the opt-out can be scoped per repo, declared instance, or one invocation:

```sh
agent-team run worker --set runtime.hooks.mailbox_injection=false
```

## Hook payload

The generated hook script reads `AGENT_TEAM_ROOT` and `AGENT_TEAM_INSTANCE`, loads unread messages from `.agent_team/daemon/<instance>/mailbox.jsonl`, formats them under `## New daemon mailbox messages`, writes `.agent_team/daemon/<instance>/mailbox-cursor.txt` to the last delivered id, and prints:

```json
{
  "hookSpecificOutput": {
    "hookEventName": "UserPromptSubmit",
    "additionalContext": "## New daemon mailbox messages\n..."
  }
}
```

The script preserves the hook event name from stdin (`hook_event_name` or `hookEventName`) so the same command can be used for `UserPromptSubmit` and `PreToolUse`.
