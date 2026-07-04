# agent-team CLI Reference

Generated from the live Cobra command tree. Run `agent-team docs cli --output docs/reference/cli.generated.md` after changing commands or flags.

## `agent-team`

Declare and launch a custom set of LLM agents and skills, vendored into any repo.

agent-team — declare and launch LLM agents and skills, vendored into any repo.

Docker-like shortcuts:
  agent-team up    = agent-team start
  agent-team down  = agent-team stop
  agent-team ls    = agent-team ps
  agent-team top   = agent-team stats
  agent-team exec  = agent-team attach

```text
agent-team [flags]
```

Persistent Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team adopt` - Adopt a live external runtime process.
- `agent-team agent` - List and inspect runnable agent definitions.
- `agent-team approval` - Manage durable job approval requests.
- `agent-team attach` - Open an interactive runtime session against a daemon-managed persistent instance.
- `agent-team channel` - Manage daemon-managed pub/sub channels.
- `agent-team channels` - List all pub/sub channels (alias for `channel ls`).
- `agent-team completion` - Generate the autocompletion script for the specified shell
- `agent-team daemon` - Manage the agent-teamd orchestrator daemon for this repo.
- `agent-team dispatch` - Dispatch an agent through daemon topology.
- `agent-team docs` - Generate developer documentation from the command tree.
- `agent-team doctor` - Sanity-check the vendored team.
- `agent-team drain` - Run maintenance cycles until idle.
- `agent-team event` - Publish manual topology events to the daemon (for testing trigger matching).
- `agent-team events` - Show daemon lifecycle events.
- `agent-team extend` - Extend a running instance watchdog deadline.
- `agent-team feedback` - Record and inspect local agent feedback.
- `agent-team graph` - Render the automation graph.
- `agent-team health` - Check daemon, instance, queue, job, and outbox health.
- `agent-team help` - Help about any command
- `agent-team inbox` - Inspect and acknowledge daemon mailbox messages.
- `agent-team init` - Vendor a starter team template into the current repo (creates .agent_team/).
- `agent-team inspect` - Show an instance&#39;s runtime, state, and topology.
- `agent-team instance` - Manage agent instance state (.agent_team/state/&lt;instance&gt;/).
- `agent-team intake` - Normalize external events into topology events.
- `agent-team job` - Manage durable work units.
- `agent-team kill` - Force-stop running instances.
- `agent-team locks` - Inspect declared dispatch lock utilization.
- `agent-team logs` - Show an instance&#39;s daemon-captured log.
- `agent-team monitor` - Show a combined health, recovery, inbox, instance, and resource snapshot.
- `agent-team next` - Print recommended next operator actions.
- `agent-team outbox` - Inspect and control sandboxed agent outbox events.
- `agent-team overview` - Show a concise operator overview across health, jobs, queue, pipelines, and schedules.
- `agent-team pipeline` - Inspect declared pipeline workflows.
- `agent-team plan` - Preview desired agent instance state from topology and daemon metadata.
- `agent-team prune` - Remove finished daemon-managed instances.
- `agent-team ps` - List instances (daemon-aware: merges live daemon state with on-disk status).
- `agent-team queue` - Inspect and control persisted daemon event queue items.
- `agent-team reload` - Reload daemon topology and reconcile runtime metadata.
- `agent-team repair` - Recover common unhealthy orchestration state.
- `agent-team restart` - Restart or resume instances.
- `agent-team resume-plan` - Show runtime resume and fallback commands for daemon metadata.
- `agent-team rm` - Remove instance state and daemon metadata.
- `agent-team run` - Launch an LLM runtime session as the named agent.
- `agent-team runtime` - Inspect the selected LLM runtime profile.
- `agent-team schedule` - Inspect and run declared schedule events.
- `agent-team send` - Send a mailbox message to a daemon-managed instance.
- `agent-team shortcuts` - List command aliases and Docker-like shortcuts.
- `agent-team signatures` - Inspect pipeline infra signatures.
- `agent-team snapshot` - Capture a read-only orchestration diagnostic report.
- `agent-team start` - Start agent-teamd if needed, then start or resume instances.
- `agent-team stats` - Show CPU and memory usage for daemon-managed instances.
- `agent-team status` - Show daemon health and the current instance table.
- `agent-team stop` - Stop running instances.
- `agent-team sync` - Apply topology&#39;s desired persistent instance state.
- `agent-team team` - Inspect declared agent teams.
- `agent-team template` - Manage templates (bundled + cached) used by `agent-team init`.
- `agent-team tick` - Run one orchestration maintenance cycle.
- `agent-team topology` - Show declared instances and triggers (reads .agent_team/instances.toml).
- `agent-team upgrade` - Check or apply a template upgrade using the repo&#39;s template lock.
- `agent-team usage` - Show runtime token usage rollups.
- `agent-team wait` - Wait for daemon-managed instances to reach a lifecycle condition.
- `agent-team watch` - Watch the combined health, recovery, inbox, instance, and resource monitor.

## `agent-team adopt`

Adopt a live external runtime process.

Adopt a live external runtime process by writing daemon runtime metadata for it. Adopted processes become visible to ps, inspect, monitor, stop, and reconcile. This is a shorter alias for `agent-team runtime adopt`.

```text
agent-team adopt <instance> [flags]
```

Flags:

```text
      --agent string         Agent name for the adopted instance. Inferred from instances.toml when omitted.
      --branch string        Branch name to record on the adopted metadata.
      --commands             Print only follow-up commands, one per line, after adoption planning or apply.
      --dry-run              Preview adoption without writing metadata.
      --force                Replace existing live metadata for the instance.
      --format string        Render the adoption result with a Go template, e.g. '{{.Metadata.Instance}} {{.Metadata.PID}}'.
      --job string           Owning job id to record on the adopted metadata.
      --json                 Emit machine-readable JSON.
      --log-path string      Runtime log path, if the external process already writes to one.
      --pid int              Live process PID to adopt.
      --pid-file string      Read the live process PID to adopt from this file. Cannot be combined with --pid.
      --pr string            PR URL to record on the adopted metadata.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --runtime string       Runtime profile for the adopted process (claude or codex). Defaults to repo/env selection.
      --runtime-bin string   Runtime binary or wrapper used by the adopted process.
      --session-id string    Runtime session id, when known and resumable.
      --started-at string    Process start time as RFC3339. Defaults to now, or existing metadata for the same PID.
      --step string          Pipeline step id to mark as owned by the adopted process. Requires --job.
      --ticket string        Ticket id to record on the adopted metadata.
      --workspace string     Workspace path for the adopted process. Defaults to the repo root.
```

## `agent-team agent`

List and inspect runnable agent definitions.

List and inspect runnable agent definitions loaded from .agent_team/agents.

```text
agent-team agent
```

Aliases: `agents`

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team agent doctor` - Validate installed agent definitions.
- `agent-team agent ls` - List runnable agent definitions.
- `agent-team agent show` - Show one runnable agent definition.

## `agent-team agent doctor`

Validate installed agent definitions.

```text
agent-team agent doctor [agent] [flags]
```

Flags:

```text
      --all              Validate all installed agents. This is the default when no agent is passed.
      --commands         Print recommended follow-up commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string    Render the agent doctor result with a Go template, e.g. '{{.OK}} {{len .Problems}}'.
      --json             Emit agent doctor findings as JSON.
      --repo string      Repo root containing .agent_team. (default "<repo>")
      --strict           Fail on all strict agent doctor checks. Currently aliases --strict-runtime.
      --strict-runtime   Fail when an agent runtime default cannot be resolved or is not discoverable.
```

## `agent-team agent ls`

List runnable agent definitions.

```text
agent-team agent ls [flags]
```

Flags:

```text
      --format string   Render each agent with a Go template, e.g. '{{.Name}} {{len .Skills}}'.
      --json            Emit agents as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team agent show`

Show one runnable agent definition.

```text
agent-team agent show <agent> [flags]
```

Flags:

```text
      --format string   Render the agent with a Go template, e.g. '{{.Name}} {{.Summary}}'.
      --json            Emit the agent as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team approval`

Manage durable job approval requests.

Manage durable approval requests under `.agent_team/jobs/&lt;job-id&gt;/approvals/`.

```text
agent-team approval
```

Aliases: `approvals`

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team approval approve` - Approve one approval request.
- `agent-team approval ls` - List approval requests for a job.
- `agent-team approval reject` - Reject one approval request.
- `agent-team approval request` - Create a pending approval request for a job.
- `agent-team approval show` - Show one approval request.

## `agent-team approval approve`

Approve one approval request.

```text
agent-team approval approve <approval-id> [notes...] [flags]
```

Flags:

```text
      --actor string        Actor recorded on the decision; defaults to AGENT_TEAM_INSTANCE or cli.
      --job string          Job id that owns the approval request.
      --json                Emit the approval as JSON.
      --notes string        Decision notes recorded on the approval.
      --notes-file string   Read decision notes from a file, or '-' for stdin.
      --repo string         Repo root containing .agent_team. (default "<repo>")
```

## `agent-team approval ls`

List approval requests for a job.

```text
agent-team approval ls [flags]
```

Aliases: `list`

Flags:

```text
      --job string      Job id whose approval requests should be listed.
      --json            Emit approvals as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
      --status string   Filter by approval status: pending, approved, or rejected.
```

## `agent-team approval reject`

Reject one approval request.

```text
agent-team approval reject <approval-id> [notes...] [flags]
```

Flags:

```text
      --actor string        Actor recorded on the decision; defaults to AGENT_TEAM_INSTANCE or cli.
      --job string          Job id that owns the approval request.
      --json                Emit the approval as JSON.
      --notes string        Decision notes recorded on the approval.
      --notes-file string   Read decision notes from a file, or '-' for stdin.
      --repo string         Repo root containing .agent_team. (default "<repo>")
```

## `agent-team approval request`

Create a pending approval request for a job.

```text
agent-team approval request [flags]
```

Flags:

```text
      --actor string                 Actor recorded on the approval request; defaults to AGENT_TEAM_INSTANCE or cli.
      --body-file string             Read approval request body from a file, or '-' for stdin.
      --id string                    Approval id; defaults to a timestamped title slug.
      --job string                   Job id to attach the approval request to.
      --json                         Emit the approval as JSON.
      --notify string                Optional instance to notify when this approval is requested.
      --repo string                  Repo root containing .agent_team. (default "<repo>")
      --requesting-instance string   Instance to notify when the approval is decided; defaults to AGENT_TEAM_INSTANCE.
      --step string                  Approval-required manual gate step to link to this approval.
      --title string                 Approval request title.
```

## `agent-team approval show`

Show one approval request.

```text
agent-team approval show <approval-id> [flags]
```

Flags:

```text
      --job string    Job id that owns the approval request.
      --json          Emit the approval as JSON.
      --repo string   Repo root containing .agent_team. (default "<repo>")
```

## `agent-team attach`

Open an interactive runtime session against a daemon-managed persistent instance.

Stop the daemon-managed child for &lt;instance&gt;, then exec `&lt;runtime&gt; --resume &lt;session-id&gt;` in your terminal so the conversation continues interactively. On exit, the daemon resumes supervision automatically — pass --no-resume to leave the instance stopped.

There is brief downtime during the handoff (Shape A): the daemon child is killed before the runtime resume command reattaches. Channel cursors and mailbox state survive the transfer.

Compatibility: log-oriented invocations such as --no-follow, --tail, --latest, --last, --all, or status/agent/phase filters follow the daemon-captured log stream, matching the older attach shortcut. `agent-team logs` is the preferred explicit command for log streaming. Dry-runs also print unmanaged resume and log commands for runtimes that do not support daemon-managed resume.

```text
agent-team attach <instance> [flags]
```

Aliases: `exec`

Flags:

```text
      --agent strings     Log compatibility mode: only attach to instances for this agent. Can repeat or comma-separate.
  -a, --all               Log compatibility mode: attach to every daemon-known instance, prefixed by instance name.
      --commands          With --dry-run, print the matching attach or unmanaged fallback commands. agent-team follow-ups preserve the selected repo scope.
      --dry-run           Preview the interactive handoff without stopping or resuming the daemon child.
      --grep string       Log compatibility mode with --no-follow: only print log lines matching this regular expression.
  -n, --last int          Log compatibility mode: attach to the N most recently started instances (0 = disabled).
      --latest            Log compatibility mode: attach to the most recently started instance.
      --no-follow         Log compatibility mode: print the selected log tail and exit instead of following.
      --no-resume         Leave the instance in stopped state when the runtime exits (default: re-dispatch via the daemon).
      --phase strings     Log compatibility mode: only attach to instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --runtime strings   Log compatibility mode: only attach to instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale     Log compatibility mode: only attach to instances whose recorded runtime PID is no longer live.
      --since string      Log compatibility mode with --no-follow: only print the log if it was modified since this duration ago (for example 10m, 24h) or RFC3339 timestamp.
      --stale             Log compatibility mode: only attach to instances whose status.toml is stale.
      --status strings    Log compatibility mode: only attach to instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --tail string       Log compatibility mode: show only the last N lines before following (0 or all = all). (default "50")
      --target string     Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --unhealthy         Log compatibility mode: only attach to crashed, status-stale, or runtime-stale instances.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team channel`

Manage daemon-managed pub/sub channels.

```text
agent-team channel
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team channel ls` - List all channels: subscriber count, message count, last activity.
- `agent-team channel publish` - Publish a message to a channel from the CLI (creates the channel if missing).
- `agent-team channel rm` - Delete a channel and all of its on-disk state.
- `agent-team channel show` - Show one channel&#39;s summary plus its tail of recent messages.

## `agent-team channel ls`

List all channels: subscriber count, message count, last activity.

```text
agent-team channel ls [flags]
```

Flags:

```text
      --format string   Render each channel with a Go template, e.g. '{{.Name}} {{.MessageCount}}'.
      --json            Emit machine-readable JSON.
      --limit int       Limit channels after sorting; 0 means no limit.
      --sort string     Sort channels by name, subscribers, messages, or last. (default "name")
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team channel publish`

Publish a message to a channel from the CLI (creates the channel if missing).

```text
agent-team channel publish <name> [body...] [flags]
```

Flags:

```text
      --format string         Render the publish result with a Go template, e.g. '{{.Channel}} {{.Seq}}'.
      --json                  Emit machine-readable JSON.
      --message string        Message text to publish.
      --message-file string   Read message text from a file, or '-' for stdin.
      --sender string         Sender label recorded with the message. (default "(cli)")
      --target string         Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team channel rm`

Delete a channel and all of its on-disk state.

```text
agent-team channel rm <name> [flags]
```

Flags:

```text
      --commands        With --dry-run, print the matching channel rm apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run         Preview channel removal without deleting it.
  -f, --force           Skip confirmation.
      --format string   Render the removal result with a Go template, e.g. '{{.Name}} {{.Action}}'.
      --json            Emit machine-readable JSON.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team channel show`

Show one channel&#39;s summary plus its tail of recent messages.

```text
agent-team channel show <name> [flags]
```

Flags:

```text
      --format string   Render the channel summary and messages with a Go template, e.g. '{{.Channel.Name}} {{len .Messages}}'.
      --json            Emit machine-readable JSON.
      --tail int        Show at most this many recent messages; 0 means all messages. (default 10)
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team channels`

List all pub/sub channels (alias for `channel ls`).

```text
agent-team channels [flags]
```

Flags:

```text
      --format string   Render each channel with a Go template, e.g. '{{.Name}} {{.MessageCount}}'.
      --json            Emit machine-readable JSON.
      --limit int       Limit channels after sorting; 0 means no limit.
      --sort string     Sort channels by name, subscribers, messages, or last. (default "name")
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team completion`

Generate the autocompletion script for the specified shell

Generate the autocompletion script for agent-team for the specified shell.
See each sub-command&#39;s help for details on how to use the generated script.

```text
agent-team completion
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team completion bash` - Generate the autocompletion script for bash
- `agent-team completion fish` - Generate the autocompletion script for fish
- `agent-team completion powershell` - Generate the autocompletion script for powershell
- `agent-team completion zsh` - Generate the autocompletion script for zsh

## `agent-team completion bash`

Generate the autocompletion script for bash

Generate the autocompletion script for the bash shell.

This script depends on the &#39;bash-completion&#39; package.
If it is not installed already, you can install it via your OS&#39;s package manager.

To load completions in your current shell session:

	source &lt;(agent-team completion bash)

To load completions for every new session, execute once:

#### Linux:

	agent-team completion bash &gt; /etc/bash_completion.d/agent-team

#### macOS:

	agent-team completion bash &gt; $(brew --prefix)/etc/bash_completion.d/agent-team

You will need to start a new shell for this setup to take effect.

```text
agent-team completion bash
```

Flags:

```text
      --no-descriptions   disable completion descriptions
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team completion fish`

Generate the autocompletion script for fish

Generate the autocompletion script for the fish shell.

To load completions in your current shell session:

	agent-team completion fish | source

To load completions for every new session, execute once:

	agent-team completion fish &gt; ~/.config/fish/completions/agent-team.fish

You will need to start a new shell for this setup to take effect.

```text
agent-team completion fish [flags]
```

Flags:

```text
      --no-descriptions   disable completion descriptions
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team completion powershell`

Generate the autocompletion script for powershell

Generate the autocompletion script for powershell.

To load completions in your current shell session:

	agent-team completion powershell | Out-String | Invoke-Expression

To load completions for every new session, add the output of the above command
to your powershell profile.

```text
agent-team completion powershell [flags]
```

Flags:

```text
      --no-descriptions   disable completion descriptions
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team completion zsh`

Generate the autocompletion script for zsh

Generate the autocompletion script for the zsh shell.

If shell completion is not already enabled in your environment you will need
to enable it.  You can execute the following once:

	echo &#34;autoload -U compinit; compinit&#34; &gt;&gt; ~/.zshrc

To load completions in your current shell session:

	source &lt;(agent-team completion zsh)

To load completions for every new session, execute once:

#### Linux:

	agent-team completion zsh &gt; &#34;${fpath[1]}/_agent-team&#34;

#### macOS:

	agent-team completion zsh &gt; $(brew --prefix)/share/zsh/site-functions/_agent-team

You will need to start a new shell for this setup to take effect.

```text
agent-team completion zsh [flags]
```

Flags:

```text
      --no-descriptions   disable completion descriptions
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team daemon`

Manage the agent-teamd orchestrator daemon for this repo.

Manage the agent-teamd orchestrator daemon for this repo.

agent-teamd is the per-repo daemon that owns runtime subprocess lifecycle (spawn / track / stop / resume) and serves a small JSON API over .agent_team/daemon.sock. It is a separate binary; this command group manages it.

```text
agent-team daemon
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team daemon adopt` - Adopt a live external process into daemon metadata.
- `agent-team daemon env` - Show the daemon launch environment snapshot with secrets redacted.
- `agent-team daemon logs` - Show the agent-teamd daemon log.
- `agent-team daemon reconcile` - Refresh daemon instance metadata against the live process table.
- `agent-team daemon restart` - Restart agent-teamd, reconciling existing instance metadata on boot.
- `agent-team daemon start` - Boot agent-teamd in this repo (detached by default; foreground with --detach=false).
- `agent-team daemon status` - Print whether agent-teamd is running in this repo, and its pid if so.
- `agent-team daemon stop` - Gracefully stop the running agent-teamd (SIGTERM, then SIGKILL after timeout).

## `agent-team daemon adopt`

Adopt a live external process into daemon metadata.

Adopt a live external process by writing daemon runtime metadata for it. Adopted processes become visible to ps, inspect, monitor, stop, and reconcile. The daemon cannot wait on an adopted process it did not spawn, so later exits are observed by daemon reconcile.

```text
agent-team daemon adopt <instance> [flags]
```

Flags:

```text
      --agent string         Agent name for the adopted instance. Inferred from instances.toml when omitted.
      --branch string        Branch name to record on the adopted metadata.
      --commands             Print only follow-up commands, one per line, after adoption planning or apply.
      --dry-run              Preview adoption without writing metadata.
      --force                Replace existing live metadata for the instance.
      --format string        Render the adoption result with a Go template, e.g. '{{.Metadata.Instance}} {{.Metadata.PID}}'.
      --job string           Owning job id to record on the adopted metadata.
      --json                 Emit machine-readable JSON.
      --log-path string      Runtime log path, if the external process already writes to one.
      --pid int              Live process PID to adopt.
      --pid-file string      Read the live process PID to adopt from this file. Cannot be combined with --pid.
      --pr string            PR URL to record on the adopted metadata.
      --runtime string       Runtime profile for the adopted process (claude or codex). Defaults to repo/env selection.
      --runtime-bin string   Runtime binary or wrapper used by the adopted process.
      --session-id string    Runtime session id, when known and resumable.
      --started-at string    Process start time as RFC3339. Defaults to now, or existing metadata for the same PID.
      --step string          Pipeline step id to mark as owned by the adopted process. Requires --job.
      --target string        Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --ticket string        Ticket id to record on the adopted metadata.
      --workspace string     Workspace path for the adopted process. Defaults to the repo root.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team daemon env`

Show the daemon launch environment snapshot with secrets redacted.

```text
agent-team daemon env [flags]
```

Flags:

```text
      --json            Emit machine-readable JSON with secret values redacted.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team daemon logs`

Show the agent-teamd daemon log.

Show or follow the local agent-teamd daemon log at .agent_team/daemon/agent-teamd.log. This is a discoverable alias for `agent-team logs --daemon`.

```text
agent-team daemon logs [flags]
```

Flags:

```text
  -f, --follow          Tail the daemon log; print new bytes as they appear.
      --grep string     Only print daemon log lines matching this regular expression. One-shot reads only.
      --since string    Only show the daemon log if it was modified since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --tail string     Show only the last N lines before returning or following (0 or all = all). (default "0")
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team daemon reconcile`

Refresh daemon instance metadata against the live process table.

Run the daemon&#39;s crash-only reconciliation pass without restarting agent-teamd. Running records whose PIDs are gone are marked exited; live adopted records stay running.

```text
agent-team daemon reconcile [flags]
```

Flags:

```text
      --format string   Render reconcile result with a Go template, e.g. '{{.Changed}} {{len .Instances}}'.
      --json            Emit machine-readable JSON.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team daemon restart`

Restart agent-teamd, reconciling existing instance metadata on boot.

Stop agent-teamd if it is running, then start it again. By default the restarted daemon is detached so the command returns after the socket is ready. Pass --detach=false to restart in the foreground for debugging.

```text
agent-team daemon restart [flags]
```

Flags:

```text
      --detach                   Background the restarted daemon (writes log to .agent_team/daemon/agent-teamd.log). (default true)
      --format string            Render daemon restart result with a Go template, e.g. '{{.Action}} {{.Changed}} {{.Status.Ready}}'. Requires detached mode.
      --http-addr string         Also expose the restarted daemon API on this loopback HTTP address, e.g. 127.0.0.1:0. Empty disables HTTP.
      --json                     Emit machine-readable JSON. Requires detached mode.
  -q, --quiet                    Suppress output and use only the exit code. Requires detached mode.
      --ready-timeout duration   Maximum time to wait for restarted detached daemon readiness (0 = no timeout). (default 3s)
      --target string            Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --timeout duration         Grace period for stopping the old daemon before SIGKILL escalation (0 = force immediately). (default 5s)
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team daemon start`

Boot agent-teamd in this repo (detached by default; foreground with --detach=false).

Boot agent-teamd in this repo. By default the daemon is detached so the command returns after the socket is ready. Pass --detach=false to run in the foreground for debugging.

```text
agent-team daemon start [flags]
```

Flags:

```text
      --detach                   Background the daemon (writes log to .agent_team/daemon/agent-teamd.log). (default true)
      --format string            Render daemon start result with a Go template, e.g. '{{.Action}} {{.PID}}'. Requires detached mode.
      --http-addr string         Also expose the daemon API on this loopback HTTP address, e.g. 127.0.0.1:0. Empty disables HTTP.
      --json                     Emit machine-readable JSON. Requires detached mode.
  -q, --quiet                    Suppress output and use only the exit code. Requires detached mode.
      --ready-timeout duration   Maximum time to wait for detached daemon readiness (0 = no timeout). (default 3s)
      --target string            Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team daemon status`

Print whether agent-teamd is running in this repo, and its pid if so.

```text
agent-team daemon status [flags]
```

Flags:

```text
      --commands            Print only recommended follow-up commands for the daemon state. agent-team follow-ups preserve the selected repo scope.
      --down                With --wait, wait until agent-teamd is not running.
      --format string       Render daemon status with a Go template, e.g. '{{.Ready}} {{.PID}}'.
      --interval duration   Polling interval for --wait. (default 200ms)
      --json                Emit machine-readable JSON.
  -q, --quiet               Suppress output and use the exit code as a readiness probe.
      --target string       Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --timeout duration    Maximum time to wait with --wait (0 = no timeout). (default 30s)
      --wait                Wait until agent-teamd is running and ready.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team daemon stop`

Gracefully stop the running agent-teamd (SIGTERM, then SIGKILL after timeout).

```text
agent-team daemon stop [flags]
```

Flags:

```text
      --format string      Render daemon stop result with a Go template, e.g. '{{.Action}} {{.Changed}}'.
      --json               Emit machine-readable JSON.
  -q, --quiet              Suppress output and use only the exit code.
      --target string      Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --timeout duration   Grace period before SIGKILL escalation (0 = force immediately). (default 5s)
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team dispatch`

Dispatch an agent through daemon topology.

Dispatch an agent through daemon topology by publishing an `agent.dispatch` event. This is the human-friendly wrapper for the common manager-to-worker path.

```text
agent-team dispatch <target> <ticket> [kickoff...] [flags]
```

Flags:

```text
      --commands              With --dry-run, print the matching dispatch apply command when the preview has actionable routes. agent-team follow-ups preserve the selected repo scope.
      --dry-run               Preview topology matches without publishing to the daemon.
      --format string         Render the event outcome or dry-run preview with a Go template.
      --json                  Emit the daemon event outcome as JSON.
      --kickoff string        Kickoff text for the dispatched agent.
      --kickoff-file string   Read kickoff text from a file, or '-' for stdin.
      --name string           Requested instance name (default: <target>-<ticket-slug>).
      --runtime string        Runtime profile for the dispatched instance (claude or codex). Overrides env and repo config.
      --runtime-bin string    Runtime binary for the dispatched instance. Overrides env and repo config.
      --source string         Source instance for the dispatch event (default: AGENT_TEAM_INSTANCE or cli).
      --target string         Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --workspace string      Workspace mode for spawned children: auto, worktree, or repo. (default "auto")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team docs`

Generate developer documentation from the command tree.

```text
agent-team docs [flags]
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team docs cli` - Generate a markdown CLI reference.
- `agent-team docs site` - Show developer docs website commands.

## `agent-team docs cli`

Generate a markdown CLI reference.

Generate a markdown CLI reference from the live Cobra command tree. Use this to refresh docs after adding or changing commands and flags.

```text
agent-team docs cli [flags]
```

Flags:

```text
      --check string     Exit non-zero if this markdown file does not match generated output.
  -h, --help             help for cli
      --include-hidden   Include hidden commands.
  -o, --output string    Write markdown to this path instead of stdout. Use '-' for stdout.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team docs site`

Show developer docs website commands.

Show the local VitePress developer docs website commands and paths for this source checkout.

```text
agent-team docs site [flags]
```

Flags:

```text
      --commands   Print only shell commands for dev, build, and preview.
      --json       Emit docs site paths and commands as JSON.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team doctor`

Sanity-check the vendored team.

Sanity-check the vendored team: .agent_team/ layout, config.toml validity, template provenance, each agent&#39;s frontmatter, skill resolution across all agents, durable job files, pipeline workflow wiring, the selected runtime binary, whether the companion agent-teamd binary is available for daemon-backed lifecycle commands, and the daemon&#39;s running/readiness state when the repo is otherwise valid.

```text
agent-team doctor [agent] [flags]
```

Flags:

```text
      --canary                    Dispatch a throwaway daemon-backed runtime canary and verify it exits cleanly.
      --canary-timeout duration   Maximum time to wait for the daemon-backed canary to exit. (default 30s)
      --commands                  Print recommended follow-up commands, one per line.
      --format string             Render the doctor result with a Go template, e.g. '{{.OK}} {{len .Problems}}'.
      --json                      Emit machine-readable JSON.
      --runtime string            Runtime profile to validate for this invocation (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary to validate for this invocation. Overrides env and repo config.
      --strict                    Fail on daemon binary, selected/runtime-default binary, and template provenance warnings.
      --strict-daemon             Fail when the companion agent-teamd binary is not discoverable.
      --strict-runtime            Fail when the selected LLM runtime binary or pipeline/team step and agent runtime defaults are not discoverable.
      --strict-template           Fail when .template.lock no longer matches its resolved template ref.
      --target string             Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team drain`

Run maintenance cycles until idle.

Run orchestration maintenance cycles until no immediate job-status, schedule, outbox, queue, or pipeline work remains. This is the script-friendly shortcut for `agent-team tick --until-idle`.

```text
agent-team drain [flags]
```

Flags:

```text
      --all-ready-steps           Advance every currently ready independent pipeline step in each drain cycle.
      --fail-on-failed            With --wait, exit 1 if any drain-advanced job resolves to failed.
      --format string             Render the drain result with a Go template, e.g. '{{.CyclesRun}} {{.Idle}}'.
      --interval duration         Delay between drain cycles. (default 2s)
      --json                      Emit machine-readable JSON.
      --limit int                 Advance at most this many ready pipeline jobs per cycle, or ready steps with --all-ready-steps; 0 means no limit.
      --max-cycles int            Stop after this many cycles if work keeps appearing. (default 20)
      --runtime string            Runtime profile for advanced step dispatches (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for advanced step dispatches. Overrides env and repo config.
      --skip-advance              Skip pipeline advancement.
      --skip-drain                Skip outbox and queue draining.
      --skip-reconcile            Skip daemon metadata and job status reconciliation.
      --skip-schedules            Skip firing due schedules.
      --target string             Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --wait                      After drain reaches idle, wait for jobs advanced during drain cycles to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step for every drain-advanced job.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
      --workspace string          Workspace mode for advanced pipeline steps: auto, worktree, or repo. (default "auto")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team event`

Publish manual topology events to the daemon (for testing trigger matching).

```text
agent-team event
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team event publish` - Publish an event of the given type. The daemon resolves it against declared triggers.
- `agent-team event trace` - Dry-run an event against local topology and explain trigger decisions.

## `agent-team event publish`

Publish an event of the given type. The daemon resolves it against declared triggers.

```text
agent-team event publish <type> [flags]
```

Flags:

```text
      --commands              With --dry-run, print the apply command, one per line.
      --dry-run               Preview matching triggers without publishing to the daemon.
      --format string         Render the event outcome or dry-run preview with a Go template, e.g. '{{len .Matched}} {{len .Dispatched}}'.
      --json                  Emit the daemon event outcome as JSON.
      --payload string        JSON object passed as the event payload (e.g. '{"target":"worker"}').
      --payload-file string   Read event payload JSON from a file, or '-' for stdin.
      --target string         Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --trace                 Include per-trigger match and rejection trace output.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team event trace`

Dry-run an event against local topology and explain trigger decisions.

```text
agent-team event trace <type> [flags]
```

Flags:

```text
      --json                  Emit the event trace as JSON.
      --payload stringArray   Payload predicate value as key=value; may be repeated.
      --target string         Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team events`

Show daemon lifecycle events.

Show or follow the daemon lifecycle event stream: dispatches, starts, stops, exits, crashes, and removals.

```text
agent-team events [flags]
```

Flags:

```text
      --action strings     Only show events with this action. Can repeat or comma-separate.
      --agent strings      Only show events for this agent. Can repeat or comma-separate.
  -f, --follow             Keep streaming new lifecycle events.
      --format string      Render each event with a Go template, e.g. '{{.Job}} {{.Action}} {{.Instance}} {{.Status}}'.
      --instance strings   Only show events for this instance. Can repeat or comma-separate.
      --job strings        Only show events for this job id or ticket. Can repeat or comma-separate.
      --json               Emit raw JSONL events.
  -n, --last int           Show events for the N most recently started daemon-known instances after other filters (0 = all).
      --latest             Show events for the most recently started daemon-known instance after other filters.
      --phase strings      Only show events for instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --runtime strings    Only show events for daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale      Only show events for instances whose recorded runtime PID is currently no longer live.
      --since string       Only show events since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --sort string        Sort returned events by oldest or newest. Follow mode always streams oldest first. (default "oldest")
      --stale              Only show events for instances whose status.toml is currently stale.
      --status strings     Only show events with this lifecycle status. Can repeat or comma-separate.
      --step string        Only show events for instances recorded on this pipeline step id.
      --summary            Summarize matching events by action, status, agent, and instance.
      --tail int           Show only the last N events before returning or following (0 = all). With non-following filters, N applies after filtering.
      --target string      Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --unhealthy          Only show events for instances that are currently crashed, status-stale, or runtime-stale.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team extend`

Extend a running instance watchdog deadline.

Extend the armed watchdog deadline for one running daemon-managed instance. The command refuses instances that are not running or do not have an armed watchdog.

```text
agent-team extend <instance> [flags]
```

Flags:

```text
      --actor string    Actor label recorded in lifecycle/audit events. (default "cli")
      --by duration     Amount to add to the running watchdog deadline, for example 30m.
      --format string   Render the extension result with a Go template, e.g. '{{.Instance}} {{.NewDeadline}}'.
      --json            Emit machine-readable JSON.
  -q, --quiet           Suppress non-error output and use only the exit code.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team feedback`

Record and inspect local agent feedback.

Record and inspect local agent feedback under `.agent_team/feedback/items/`. Feedback is local and file-backed; it does not contact Linear or require the daemon.

```text
agent-team feedback
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team feedback ls` - List local feedback items.
- `agent-team feedback resolve` - Resolve a local feedback item.
- `agent-team feedback show` - Show one local feedback item.
- `agent-team feedback submit` - Submit one local feedback item.

## `agent-team feedback ls`

List local feedback items.

```text
agent-team feedback ls [flags]
```

Aliases: `list`

Flags:

```text
      --group           Collapse rows by fingerprint and show count plus first/last seen.
      --repo string     Repo root containing .agent_team. (default "<repo>")
      --status string   Filter by status: new, ticketed, dismissed, or all. (default "new")
```

## `agent-team feedback resolve`

Resolve a local feedback item.

Resolve a local feedback item as ticketed or dismissed. Exactly one of --ticket or --dismiss is required.

```text
agent-team feedback resolve <id> [flags]
```

Flags:

```text
      --dismiss string   Mark feedback dismissed with a reason.
      --repo string      Repo root containing .agent_team. (default "<repo>")
      --ticket string    Mark feedback ticketed with a ticket id or URL.
```

## `agent-team feedback show`

Show one local feedback item.

```text
agent-team feedback show <id> [flags]
```

Flags:

```text
      --repo string   Repo root containing .agent_team. (default "<repo>")
```

## `agent-team feedback submit`

Submit one local feedback item.

Submit one local feedback item. The body is the only required input; context is captured automatically from the agent-team environment and local metadata.

```text
agent-team feedback submit <text> [flags]
```

Flags:

```text
      --category string   Feedback category: friction, bug, idea, or docs. (default "friction")
      --repo string       Repo root containing .agent_team. (default "<repo>")
```

## `agent-team graph`

Render the automation graph.

Render a read-only graph of the repo automation model. By default this shows the full topology; pass a team or pipeline name, or use --team or --pipeline, to narrow to one declared workflow owner.

```text
agent-team graph [team-or-pipeline] [flags]
```

Flags:

```text
      --commands          Print recommended commands from graph action hints, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string     Graph output format: text, mermaid, or dot. (default "text")
      --job string        Overlay durable job step state on declared pipeline graphs.
      --json              Emit graph nodes and edges as JSON.
      --pipeline string   Render one declared pipeline graph instead of the full topology graph.
      --repo string       Repo root containing .agent_team. (default "<repo>")
      --routes            Annotate pipeline steps with matching agent.dispatch route instances.
      --team string       Render one declared team graph instead of the full topology graph.
```

## `agent-team health`

Check daemon, instance, queue, job, and outbox health.

Check the daemon, declared persistent instances, crashed instances, and stale status files. Queue, job-file quarantine, outbox quarantine, intake, and optional durable job checks are included in the same health result. One-shot checks exit 0 when healthy and 1 when unhealthy.

```text
agent-team health [flags]
```

Flags:

```text
      --agent strings       Only check declared and daemon-known instances for this agent. Daemon health remains global. Can repeat or comma-separate.
      --commands            Print issue remediation commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --fallbacks           When runtime recovery actions use resume-plan, recommend command-mode fallback expansion.
      --format string       Render the health result with a Go template, e.g. '{{.Healthy}} {{.Summary.Running}}'.
      --instance strings    Only check instances with this name. Daemon health remains global. Can repeat or comma-separate.
      --interval duration   Refresh interval for --watch or --wait. (default 2s)
      --jobs                Include durable job triage and status-file previews; treat jobs needing attention as unhealthy.
      --json                Emit machine-readable JSON.
  -n, --last int            Only check the N most recently started instances after other filters (0 = all). Daemon health remains global.
      --last-message        When runtime recovery actions use resume-plan log fallbacks, prefer clean Codex final-message commands.
      --latest              Only check the most recently started instance after other filters. Daemon health remains global.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --phase strings       Only check instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
  -q, --quiet               Suppress output and use only the exit code.
      --runtime strings     Only check daemon-known instances for this runtime: claude or codex. Daemon health remains global. Can repeat or comma-separate.
      --runtime-stale       Only check running instances whose recorded runtime PID is no longer live. Daemon health remains global.
      --stale               Only check instances whose status.toml is stale.
      --status strings      Only check instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --strict-topology     Treat running daemon-known instances not declared in instances.toml as unhealthy.
      --target string       Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --timeout duration    Maximum time to wait with --wait (0 = no timeout).
      --unhealthy           Only check crashed, status-stale, or runtime-stale instances. Daemon health remains global.
      --wait                Poll until the fleet is healthy, then exit.
  -w, --watch               Refresh health until interrupted.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team help`

Help about any command

Help provides help for any command in the application.
Simply type agent-team help [path to command] for full details.

```text
agent-team help [command]
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team inbox`

Inspect and acknowledge daemon mailbox messages.

Inspect daemon mailbox messages stored under .agent_team/daemon. The inbox commands read local files directly, so they work even when agent-teamd is not running.

```text
agent-team inbox
```

Aliases: `mailbox`

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team inbox ack` - Advance an instance inbox cursor.
- `agent-team inbox ls` - List inbox summaries by instance.
- `agent-team inbox prune` - Prune acknowledged inbox messages.
- `agent-team inbox show` - Show messages for one instance inbox.

## `agent-team inbox ack`

Advance an instance inbox cursor.

```text
agent-team inbox ack <instance> <message-id>|--all [flags]
```

Flags:

```text
      --all             Acknowledge every current message in the inbox.
      --commands        With --dry-run, print the matching inbox ack apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run         Preview the cursor update without writing it.
      --format string   Render the ack result with a Go template, e.g. '{{.Instance}} {{.Acked}}'.
      --json            Emit machine-readable JSON.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team inbox ls`

List inbox summaries by instance.

```text
agent-team inbox ls [flags]
```

Flags:

```text
      --commands        Print inbox show commands for inboxes with unread messages. agent-team follow-ups preserve the selected repo scope.
      --format string   Render each inbox summary with a Go template, e.g. '{{.Instance}} {{.Unread}}'.
      --json            Emit machine-readable JSON.
      --limit int       Limit inbox summaries after filtering and sorting; 0 means no limit.
      --sort string     Sort inboxes by instance, unread, latest, or total. (default "instance")
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --team string     Only list inboxes owned by this declared team.
      --unread          Show only inboxes with unread messages.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team inbox prune`

Prune acknowledged inbox messages.

Prune acknowledged inbox messages while preserving the cursor anchor message. Unread messages are never removed.

```text
agent-team inbox prune <instance>...|--all [flags]
```

Flags:

```text
      --all                   Prune every current inbox.
      --commands              With --dry-run, print the matching inbox prune apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run               Preview inbox compaction without rewriting mailbox files.
      --format string         Render each prune result with a Go template, e.g. '{{.Instance}} {{.Dropped}}'.
      --json                  Emit machine-readable JSON.
      --limit int             Prune at most this many acknowledged messages per inbox; 0 means no limit.
      --older-than duration   Only prune acknowledged messages older than this duration.
      --target string         Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --team string           With --all, only prune inboxes owned by this declared team.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team inbox show`

Show messages for one instance inbox.

```text
agent-team inbox show <instance> [flags]
```

Flags:

```text
      --commands        Print an inbox ack command for the latest displayed unread message. agent-team follow-ups preserve the selected repo scope.
      --format string   Render each message with a Go template, e.g. '{{.ID}} {{.Unread}} {{.Body}}'.
      --json            Emit machine-readable JSON.
      --tail int        Show only the N most recent matching messages (0 = all).
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --unread          Show only messages after the inbox cursor.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team init`

Vendor a starter team template into the current repo (creates .agent_team/).

Vendor a template into the current repo (creates .agent_team/). With no ref, the bundled
default template is used (a software-engineering team — manager + worker + ticket-manager,
plus linear / pull-request / assign-worker skills). Refs can be local paths, cached refs,
or git refs such as github.com/acme/eng-team@v1.0.0. Pass `--template empty` for a scaffold-
only init. `--set k=v` supplies template parameters; `--no-input` fails (rather than prompting)
when required parameters have no value.

```text
agent-team init [<ref>] [flags]
```

Flags:

```text
      --commands           With --dry-run, print the matching init apply command.
      --dry-run            Preview init without writing .agent_team/.
      --force              Overwrite existing .agent_team/ files (config.toml is never overwritten).
      --format string      Render the init result with a Go template, e.g. '{{.TeamDir}} {{.Kind}}'.
      --json               Emit machine-readable JSON on success.
      --no-input           Fail with a clear error if required parameters are missing instead of prompting.
      --set stringArray    Set a template parameter, e.g. --set linear.team_id=<uuid>. Repeatable.
      --target string      Target repo root. (default "<repo>")
      --template default   default (uses the supplied/bundled template ref) or `empty` (scaffold only, no manifest). (default "default")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team inspect`

Show an instance&#39;s runtime, state, and topology.

Docker-like convenience alias for `agent-team instance show &lt;instance&gt;`.

```text
agent-team inspect [<instance>...] [flags]
```

Flags:

```text
      --agent strings      Only inspect instances for this agent. Can repeat or comma-separate.
  -a, --all                Inspect every visible instance.
      --format string      Render each instance with a Go template, e.g. '{{.Instance}} {{if .Runtime}}{{.Runtime.Lifecycle}}{{end}}'.
      --instance strings   Only inspect instances with this name. Can repeat or comma-separate.
      --json               Emit machine-readable JSON.
  -n, --last int           Inspect the N most recently started visible instances after other filters (0 = all).
      --latest             Inspect the most recently started visible instance after other filters.
      --phase strings      Only inspect instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --runtime strings    Only inspect instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale      Only inspect running instances whose recorded runtime PID is no longer live.
      --stale              Only inspect instances whose status.toml is stale.
      --status strings     Only inspect instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --target string      Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --unhealthy          Only inspect crashed, status-stale, or runtime-stale instances.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team instance`

Manage agent instance state (.agent_team/state/&lt;instance&gt;/).

```text
agent-team instance
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team instance brief` - Generate a recoverable catch-up brief for an instance.
- `agent-team instance down` - Stop declared persistent instances. With no args, stops all running.
- `agent-team instance ls` - List instances (state dirs).
- `agent-team instance ps` - List instances with their current status (Docker-ps style).
- `agent-team instance rm` - Remove an instance&#39;s state.
- `agent-team instance show` - Show an instance&#39;s state files.
- `agent-team instance up` - Start or resume instances (idempotent). Requires the daemon.

## `agent-team instance brief`

Generate a recoverable catch-up brief for an instance.

Generate a recoverable catch-up brief for an instance from daemon-owned state: identity, jobs, mailbox, channel cursors, lifecycle events, and fleet rows. The brief is written to .agent_team/state/&lt;name&gt;/brief.md and printed.

```text
agent-team instance brief <name> [flags]
```

Flags:

```text
      --json            Emit the generated brief as structured JSON.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team instance down`

Stop declared persistent instances. With no args, stops all running.

```text
agent-team instance down [<name>...] [flags]
```

Flags:

```text
      --agent strings           Stop every running instance for this agent. Can repeat or comma-separate.
      --commands                With --dry-run, print the matching instance down apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run                 Preview planned stop actions without changing daemon state.
  -f, --force                   Escalate to SIGKILL if an instance does not stop within --timeout.
      --format string           Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                    Emit machine-readable JSON.
  -n, --last int                Stop the N most recently started running instances after other filters (0 = all).
      --latest                  Stop the most recently started running instance after other filters.
      --phase strings           Stop daemon-known instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --rm                      Remove selected instance state and daemon metadata after stopping.
      --runtime-stale           Only stop running instances whose recorded runtime PID is no longer live.
      --stale                   Only stop instances whose status.toml is stale.
      --status strings          Stop daemon-known instances currently in this lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary                 Show aggregate action counts instead of per-instance rows.
      --target string           Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --timeout duration        Grace before --force kills. With --wait and no --wait-timeout, also used as the wait deadline (0 = no wait deadline; force defaults to 10s).
      --unhealthy               Only stop instances that are crashed, status-stale, or runtime-stale.
      --wait                    Wait for stopped instances to reach a terminal state.
      --wait-timeout duration   Maximum time to wait for terminal state with --wait. Defaults to --timeout when unset; set 0 explicitly for no wait timeout.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team instance ls`

List instances (state dirs).

```text
agent-team instance ls [flags]
```

Flags:

```text
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team instance ps`

List instances with their current status (Docker-ps style).

Walks .agent_team/state/*/status.toml and renders one row per instance. Instances with a state dir but no status.toml render with `—` placeholders so they remain visible. Non-idle/non-done rows whose status.toml is older than the configured health policy threshold are flagged `(stale)`.

```text
agent-team instance ps [flags]
```

Flags:

```text
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team instance rm`

Remove an instance&#39;s state.

```text
agent-team instance rm [<name>...] [flags]
```

Flags:

```text
      --agent strings     With --all, --finished, --latest, --last, --runtime, --status, --phase, --stale, --runtime-stale, or --unhealthy, only remove daemon-known instances for this agent. Can repeat or comma-separate.
  -a, --all               Remove every daemon-known instance. Can combine with --agent, --runtime, --status, --phase, --stale, --runtime-stale, or --unhealthy.
      --commands          With --dry-run, print the matching instance rm apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run           Preview matching removals without deleting state or daemon metadata.
      --finished          Remove every daemon-known exited or crashed instance.
  -f, --force             Skip confirmation; if the daemon is running, stop a running instance before removal.
      --format string     Render each removal result with a Go template, e.g. '{{.Instance}} {{.Path}}'. Requires --force unless --dry-run is set.
      --json              Emit machine-readable JSON. Requires --force unless --dry-run is set.
  -n, --last int          Remove the N most recently started daemon-known instances after other filters (0 = all).
      --latest            Remove the most recently started daemon-known instance after other filters.
      --phase strings     Remove daemon-known instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --runtime strings   Remove daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale     Remove only daemon-known running instances whose recorded runtime PID is no longer live.
      --stale             Remove only daemon-known instances whose non-idle work phase has stale status telemetry.
      --status strings    Remove daemon-known instances currently in this lifecycle status: stopped, exited, crashed, running, or unknown. Can repeat or comma-separate.
      --summary           Show aggregate removal counts instead of per-instance rows.
      --target string     Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --unhealthy         Remove only daemon-known instances that are crashed, status-stale, or runtime-stale.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team instance show`

Show an instance&#39;s state files.

```text
agent-team instance show <name> [flags]
```

Flags:

```text
      --json            Emit machine-readable JSON.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team instance up`

Start or resume instances (idempotent). Requires the daemon.

```text
agent-team instance up [<name>...] [flags]
```

Flags:

```text
      --agent strings        Start or resume every declared persistent and daemon-known instance for this agent. Can repeat or comma-separate.
  -a, --all                  Start or resume every declared persistent and daemon-known instance.
      --attach               Follow the selected instance log after starting or resuming. Requires exactly one selected instance.
      --commands             With --dry-run, print the matching instance up apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run              Preview planned start/resume actions without changing daemon state.
      --format string        Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                 Emit machine-readable JSON.
  -n, --last int             Start or resume the N most recently started instances after other filters (0 = all).
      --latest               Start or resume the most recently started instance after other filters.
      --phase strings        Only start or resume instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --prompt string        Override the default kickoff prompt.
      --prompt-file string   Read kickoff prompt from a file, or '-' for stdin.
      --runtime-stale        Only start or resume running instances whose recorded runtime PID is no longer live.
      --stale                Only start or resume instances whose status.toml is stale.
      --status strings       Only start or resume instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary              Show aggregate action counts instead of per-instance rows.
      --tail string          With --attach, show only the last N lines before following (0 or all = all). (default "50")
      --target string        Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --timeout duration     Maximum time to wait with --wait (0 = no timeout).
      --unhealthy            Only start or resume instances that are crashed, status-stale, or runtime-stale.
      --wait                 Wait for selected instances to become healthy after starting. With no scoped selection, waits for the fleet.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team intake`

Normalize external events into topology events.

Normalize external events such as Linear/GitHub webhooks and schedules into topology events handled by the daemon.

```text
agent-team intake
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team intake deliveries` - List recent intake server deliveries.
- `agent-team intake doctor` - Validate the recorded intake delivery ledger.
- `agent-team intake duplicates` - List duplicate provider request ids in the delivery ledger.
- `agent-team intake github` - Normalize a github webhook payload and publish it.
- `agent-team intake linear` - Normalize a linear webhook payload and publish it.
- `agent-team intake prune` - Prune recorded intake deliveries.
- `agent-team intake replay` - Replay a recorded normalized intake delivery.
- `agent-team intake schedule` - Publish a named schedule event.
- `agent-team intake serve` - Run a local HTTP listener for external webhook intake.
- `agent-team intake service` - Print service or deployment config for intake serve.
- `agent-team intake summary` - Summarize recorded intake deliveries.

## `agent-team intake deliveries`

List recent intake server deliveries.

```text
agent-team intake deliveries [flags]
```

Flags:

```text
      --commands               Print recommended follow-up commands, one per line.
      --format string          Render each delivery with a Go template, e.g. '{{.Provider}} {{.Status}} {{.EventType}}'.
      --json                   Emit deliveries as JSON.
      --provider string        Only show deliveries for a provider: linear or github.
      --replay-status string   Only show deliveries with replay status: ok, error, none, or any.
      --request-id string      Only show deliveries with this provider request id, such as X-GitHub-Delivery.
      --status string          Only show deliveries with a status: ok or error.
      --tail string            Show only the last N deliveries (0 or all = all). (default "20")
      --target string          Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --unresolved             Only show failed deliveries that still need replay.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team intake doctor`

Validate the recorded intake delivery ledger.

```text
agent-team intake doctor [flags]
```

Flags:

```text
      --commands        Print recommended follow-up commands, one per line.
      --format string   Render the intake doctor result with a Go template, e.g. '{{.OK}} {{len .Problems}}'.
      --json            Emit ledger doctor findings as JSON.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team intake duplicates`

List duplicate provider request ids in the delivery ledger.

```text
agent-team intake duplicates [flags]
```

Flags:

```text
      --commands            Print recommended follow-up commands, one per line.
      --format string       Render each duplicate group with a Go template, e.g. '{{.Provider}} {{.RequestID}} {{.Count}}'.
      --json                Emit duplicate request id groups as JSON.
      --provider string     Only show duplicate request ids for a provider: linear or github.
      --request-id string   Only show this provider request id.
      --target string       Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team intake github`

Normalize a github webhook payload and publish it.

```text
agent-team intake github [flags]
```

Flags:

```text
      --advance                   With --reconcile-job, dispatch the next ready pipeline step after PR metadata is reconciled.
      --cleanup-merged            With --reconcile-job, remove the job-owned worktree and branch after a merged PR event.
      --commands                  With --dry-run, print the apply command, one per line. agent-team follow-ups preserve the selected repo scope.
      --dry-run                   Normalize and print the event without publishing to the daemon.
      --fail-on-failed            With --wait, exit 1 if the reconciled job resolves to failed.
      --format string             Render the intake result with a Go template, e.g. '{{.Event.Type}}'.
      --json                      Emit normalized event and daemon outcome as JSON.
      --payload string            Webhook JSON object.
      --payload-file string       Read webhook JSON from a file, or '-' for stdin.
      --preview-triggers          With --dry-run, include local topology instance and pipeline matches.
      --reconcile-job             Also reconcile the normalized PR event into the owning durable job.
      --runtime string            Runtime profile for --advance dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for --advance dispatch. Overrides env and repo config.
      --target string             Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --verify-pr                 With --cleanup-merged, verify the recorded GitHub PR is merged with gh before cleanup.
      --wait                      With --advance, wait for the reconciled job to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
      --workspace string          Workspace mode for --advance dispatch: auto, worktree, or repo. (default "auto")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team intake linear`

Normalize a linear webhook payload and publish it.

```text
agent-team intake linear [flags]
```

Flags:

```text
      --commands              With --dry-run, print the apply command, one per line. agent-team follow-ups preserve the selected repo scope.
      --dry-run               Normalize and print the event without publishing to the daemon.
      --format string         Render the intake result with a Go template, e.g. '{{.Event.Type}}'.
      --json                  Emit normalized event and daemon outcome as JSON.
      --payload string        Webhook JSON object.
      --payload-file string   Read webhook JSON from a file, or '-' for stdin.
      --preview-triggers      With --dry-run, include local topology instance and pipeline matches.
      --target string         Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team intake prune`

Prune recorded intake deliveries.

Prune recorded intake deliveries. By default this removes successful deliveries and keeps failures for recovery.

```text
agent-team intake prune [flags]
```

Flags:

```text
      --commands               With --dry-run, print the apply command, one per line. agent-team follow-ups preserve the selected repo scope.
      --dry-run                Preview deliveries that would be pruned without rewriting the ledger.
      --format string          Render each prune result with a Go template, e.g. '{{.ID}} {{.Status}} {{.Dropped}}'.
      --json                   Emit prune results as JSON.
      --older-than duration    Only prune deliveries older than this duration.
      --replay-status string   Only prune deliveries with replay status: ok, error, none, or any. Defaults --status to all when set.
      --status string          Delivery status to prune: ok, error, or all. (default "ok")
      --target string          Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team intake replay`

Replay a recorded normalized intake delivery.

```text
agent-team intake replay [delivery-id] [flags]
```

Flags:

```text
      --all                 Replay all matching recorded deliveries.
      --commands            With --dry-run, print the apply command, one per line. agent-team follow-ups preserve the selected repo scope.
      --dedupe-request-id   With --all, skip later deliveries with the same provider request id.
      --dry-run             Preview the normalized delivery without publishing it.
      --format string       Render the replay result with a Go template, e.g. '{{.Event.Type}}'.
      --json                Emit replay result as JSON.
      --limit int           With --all, replay at most this many matching deliveries (0 = all).
      --preview-triggers    With --dry-run, include local topology instance and pipeline matches.
      --provider string     With --all, only replay deliveries for a provider: linear or github.
      --status string       With --all, delivery status to replay: ok, error, or all. error skips recovered replays. (default "error")
      --target string       Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team intake schedule`

Publish a named schedule event.

```text
agent-team intake schedule <name> [flags]
```

Flags:

```text
      --commands                  With --dry-run, print the apply command, one per line. agent-team follow-ups preserve the selected repo scope.
      --dry-run                   Normalize and print the event without publishing to the daemon.
      --fail-on-failed            With --wait, exit 1 if any schedule-created job resolves to failed.
      --format string             Render the intake result with a Go template, e.g. '{{.Event.Type}}'.
      --json                      Emit normalized event and daemon outcome as JSON.
      --payload string            Additional JSON object merged into the schedule payload.
      --payload-file string       Read additional schedule payload JSON from a file, or '-' for stdin.
      --preview-triggers          With --dry-run, include local topology instance and pipeline matches.
      --target string             Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --wait                      After the schedule publishes pipeline jobs, wait for those jobs to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. pipeline_step, advance_dispatched, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step for every schedule-created job.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team intake serve`

Run a local HTTP listener for external webhook intake.

```text
agent-team intake serve [flags]
```

Flags:

```text
      --addr string                           Address for the webhook listener. (default "127.0.0.1:8787")
      --commands                              With --dry-run, print the matching intake serve command without starting the listener. agent-team follow-ups preserve the selected repo scope.
      --dry-run                               Normalize requests and return previews without publishing to the daemon.
      --github-advance-job                    With --github-reconcile-job, dispatch the next ready pipeline step after PR metadata is reconciled.
      --github-cleanup-merged                 With --github-reconcile-job, remove the job-owned worktree and branch after a merged PR event.
      --github-reconcile-job                  For GitHub PR events, also reconcile the owning durable job.
      --github-replay-window duration         Reject signed GitHub delivery IDs already seen within this duration. Use 0 to disable. (default 24h0m0s)
      --github-secret string                  GitHub webhook secret. Defaults to GITHUB_WEBHOOK_SECRET when set.
      --github-verify-pr                      With --github-cleanup-merged, verify recorded GitHub PRs are merged with gh before cleanup.
      --linear-max-age duration               Maximum accepted Linear webhook age after signature verification. (default 1m0s)
      --linear-secret string                  Linear webhook signing secret. Defaults to LINEAR_WEBHOOK_SECRET when set.
      --max-body-bytes int                    Maximum webhook request body size accepted by the intake server. (default 1048576)
      --preview-triggers                      With --dry-run, include local topology instance and pipeline matches.
      --prune-ok-older-than duration          Prune successful delivery history older than this duration after each request. Use 0 to disable. (default 168h0m0s)
      --prune-recovered-older-than duration   Prune recovered failed delivery history older than this duration after each request. Use 0 to disable. (default 168h0m0s)
      --require-github-secret                 Fail startup unless --github-secret or GITHUB_WEBHOOK_SECRET is set.
      --require-linear-secret                 Fail startup unless --linear-secret or LINEAR_WEBHOOK_SECRET is set.
      --target string                         Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team intake service`

Print service or deployment config for intake serve.

Print a read-only service or deployment configuration for running `agent-team intake serve` against this repo. The command does not install, apply, or write the generated file.

```text
agent-team intake service systemd|launchd|compose|kubernetes [flags]
```

Flags:

```text
      --addr string                           Address for the webhook listener. (default "127.0.0.1:8787")
      --bin string                            agent-team binary path used in the service. (default "agent-team")
      --container-workdir string              Container working directory used by compose service generation. (default "/workspace")
      --description string                    Service description. (default "agent-team intake server")
      --env-file string                       Secret environment file for systemd EnvironmentFile or compose env_file; launchd does not support this.
      --github-advance-job                    Include --github-advance-job in ExecStart; requires --github-reconcile-job.
      --github-cleanup-merged                 Include --github-cleanup-merged in ExecStart; requires --github-reconcile-job.
      --github-reconcile-job                  Include --github-reconcile-job in ExecStart.
      --github-replay-window duration         Reject signed GitHub delivery IDs already seen within this duration. Use 0 to disable. (default 24h0m0s)
      --github-secret-env string              Environment variable name containing the GitHub webhook secret; empty omits it. (default "GITHUB_WEBHOOK_SECRET")
      --github-verify-pr                      Include --github-verify-pr in ExecStart; requires --github-cleanup-merged.
      --image string                          Container image used by compose service generation. (default "agent-team:local")
      --ingress-class string                  Kubernetes IngressClass name for --ingress-host; kubernetes output only.
      --ingress-host string                   Kubernetes Ingress host to expose the generated Service; kubernetes output only.
      --linear-max-age duration               Maximum accepted Linear webhook age after signature verification. (default 1m0s)
      --linear-secret-env string              Environment variable name containing the Linear webhook secret; empty omits it. (default "LINEAR_WEBHOOK_SECRET")
      --max-body-bytes int                    Maximum webhook request body size accepted by intake serve. (default 1048576)
      --name string                           Service unit name/comment stem. (default "agent-team-intake")
      --prune-ok-older-than duration          Prune successful delivery history older than this duration after each request. Use 0 to disable. (default 168h0m0s)
      --prune-recovered-older-than duration   Prune recovered failed delivery history older than this duration after each request. Use 0 to disable. (default 168h0m0s)
      --publish string                        Compose port publication host:container mapping; empty omits ports. (default "127.0.0.1:8787:8787")
      --require-github-secret                 Include --require-github-secret in ExecStart.
      --require-linear-secret                 Include --require-linear-secret in ExecStart.
      --secret-name string                    Kubernetes Secret name used by kubernetes service generation; defaults to <name>-secrets.
      --target string                         Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --tls-secret string                     Kubernetes TLS Secret name for --ingress-host; kubernetes output only.
      --workspace-claim string                Kubernetes PersistentVolumeClaim name mounted at --container-workdir; defaults to <name>-workspace.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team intake summary`

Summarize recorded intake deliveries.

```text
agent-team intake summary [flags]
```

Flags:

```text
      --commands               Print recommended follow-up commands, one per line.
      --format string          Render the summary with a Go template, e.g. '{{.Unresolved}} {{.Replayable}}'.
      --json                   Emit summary as JSON.
      --provider string        Only summarize deliveries for a provider: linear or github.
      --replay-status string   Only summarize deliveries with replay status: ok, error, none, or any.
      --request-id string      Only summarize deliveries with this provider request id, such as X-GitHub-Delivery.
      --status string          Only summarize deliveries with a status: ok or error.
      --target string          Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --unresolved             Only summarize failed deliveries that still need replay.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team job`

Manage durable work units.

Manage durable work units backed by `.agent_team/jobs/&lt;job-id&gt;.toml`. Jobs track ticket ownership, target agent, lifecycle state, instance, branch, worktree, and PR metadata.

```text
agent-team job
```

Aliases: `jobs`

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team job adopt` - Adopt a live external process as a job&#39;s owning instance.
- `agent-team job advance` - Dispatch the next ready step in a pipeline job.
- `agent-team job approve` - Approve a blocked manual pipeline gate.
- `agent-team job attach` - Attach to a job&#39;s owning instance.
- `agent-team job block` - Mark a job blocked with an operator reason.
- `agent-team job bounce` - Re-queue a pipeline step with review findings.
- `agent-team job cancel` - Cancel a job as failed.
- `agent-team job cleanup` - Remove a done job&#39;s owned worker worktree and branch after merge.
- `agent-team job close` - Close a job as done or failed.
- `agent-team job create` - Create a durable job for a ticket.
- `agent-team job dispatch` - Dispatch a job to its target agent.
- `agent-team job doctor` - Validate durable job files.
- `agent-team job events` - Show a job&#39;s durable event history.
- `agent-team job explain` - Explain pipeline step readiness for one job.
- `agent-team job extend` - Extend a job&#39;s running watchdog deadline.
- `agent-team job gate` - Write durable per-job gate results.
- `agent-team job gates` - Show latest gate results for one job.
- `agent-team job graph` - Render one job&#39;s pipeline graph with step state.
- `agent-team job hold` - Hold a job so pipeline automation will not advance it.
- `agent-team job keep-worktree` - Disable automatic worktree cleanup for a job.
- `agent-team job kill` - Force-stop a job&#39;s owning instance.
- `agent-team job logs` - Show a job&#39;s owning instance log.
- `agent-team job ls` - List durable jobs.
- `agent-team job merge` - Apply a pipeline job&#39;s declared merge strategy.
- `agent-team job next` - Show the next pipeline step for a job without dispatching it.
- `agent-team job note` - Append an operator note to a job&#39;s audit history.
- `agent-team job outbox` - List or control outbox events owned by one job.
- `agent-team job prune` - Remove terminal job files and their event logs.
- `agent-team job ps` - List instances owned by one job.
- `agent-team job quarantine` - Inspect, restore, and drop quarantined job files.
- `agent-team job queue` - List queue items owned by one job.
- `agent-team job ready` - List pipeline jobs with ready or selected next-step states.
- `agent-team job reconcile` - Reconcile external runtime state back into jobs.
- `agent-team job reject` - Reject a blocked manual pipeline gate.
- `agent-team job release` - Release a held job so pipeline automation can advance it.
- `agent-team job reopen` - Reopen a durable job for another attempt.
- `agent-team job resume-plan` - Show runtime resume and fallback commands for one job.
- `agent-team job rm` - Remove job files and their event logs.
- `agent-team job runtime` - Inspect job-owned runtime metadata.
- `agent-team job send` - Send a mailbox message to a job&#39;s owning instance.
- `agent-team job show` - Show one durable job.
- `agent-team job snapshot` - Capture a job-scoped diagnostic snapshot.
- `agent-team job start` - Start or resume a job&#39;s owning instance.
- `agent-team job stats` - Show CPU and memory usage for a job&#39;s instances.
- `agent-team job step` - Update a pipeline job step status.
- `agent-team job stop` - Stop a job&#39;s owning instance.
- `agent-team job timeline` - Show a combined job audit and lifecycle timeline.
- `agent-team job timeout` - Mark stale running job work failed.
- `agent-team job triage` - Show jobs that need operator attention.
- `agent-team job unblock` - Answer a blocked job and mark it ready to continue.
- `agent-team job update` - Update job metadata.
- `agent-team job wait` - Wait for a job to reach a lifecycle status, event, or next step.

## `agent-team job adopt`

Adopt a live external process as a job&#39;s owning instance.

Adopt a live external process into daemon metadata and sync the durable job ownership fields. Defaults such as agent, workspace, branch, PR, and ticket come from the job file when present.

```text
agent-team job adopt <job-id> [flags]
```

Flags:

```text
      --agent string         Agent name for the adopted instance. Defaults to the job target.
      --branch string        Branch name to record. Defaults to the job branch.
      --commands             Print only follow-up commands, one per line, after adoption planning or apply.
      --dry-run              Preview adoption without writing metadata or job state.
      --force                Replace existing live metadata for the instance.
      --format string        Render the adoption result with a Go template, e.g. '{{.Job.ID}} {{.Metadata.Instance}}'.
      --instance string      Instance name that should own the job. Defaults to selected or active step ownership, then job ownership.
      --json                 Emit machine-readable JSON.
      --log-path string      Runtime log path, if the external process already writes to one.
      --pid int              Live process PID to adopt.
      --pid-file string      Read the live process PID to adopt from this file. Cannot be combined with --pid.
      --pr string            PR URL to record. Defaults to the job PR.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --runtime string       Runtime profile for the adopted process (claude or codex). Defaults to repo/env selection.
      --runtime-bin string   Runtime binary or wrapper used by the adopted process.
      --session-id string    Runtime session id, when known and resumable.
      --started-at string    Process start time as RFC3339. Defaults to now, or existing metadata for the same PID.
      --step string          Pipeline step id to mark as owned by the adopted process.
      --workspace string     Workspace path for the adopted process. Defaults to the job worktree, then repo root.
```

## `agent-team job advance`

Dispatch the next ready step in a pipeline job.

```text
agent-team job advance <job-id> [flags]
```

Flags:

```text
      --commands                  With --dry-run, print the matching job advance apply command when the preview has actionable work.
      --dry-run                   Preview the next ready step dispatch without changing daemon or job state.
      --fail-on-failed            With --wait, exit 1 if the job resolves to failed.
      --format string             Render the advance preview or result with a Go template, e.g. '{{.Job.ID}} {{.Step.ID}}'.
      --json                      Emit the updated job and daemon event outcome as JSON.
      --repo string               Repo root containing .agent_team. (default "<repo>")
      --runtime string            Runtime profile for the advanced step dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for the advanced step dispatch. Overrides env and repo config.
      --wait                      After advancing, wait for the job to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
      --workspace string          Workspace mode for the advanced step: auto, worktree, or repo. (default "auto")
```

## `agent-team job approve`

Approve a blocked manual pipeline gate.

Approve a blocked manual pipeline gate by marking it queued. By default this selects the next blocked manual gate for the job; pass --step to approve a specific gate.

```text
agent-team job approve <job-id> [message...] [flags]
```

Flags:

```text
      --advance                   After approval, dispatch the newly ready step.
      --commands                  With --dry-run, print the matching job approve apply command when the preview has actionable work.
      --dry-run                   Preview approval and optional advance dispatch without writing job or daemon state.
      --fail-on-failed            With --wait, exit 1 if the job resolves to failed.
      --format string             Render the updated job or advance result with a Go template, e.g. '{{.ID}} {{.Status}}' or '{{.Job.ID}} {{.Step.ID}}'.
      --json                      Emit the updated job or advance result as JSON.
      --message string            Approval message recorded on the job.
      --message-file string       Read approval message from a file, or '-' for stdin.
      --repo string               Repo root containing .agent_team. (default "<repo>")
      --runtime string            Runtime profile for --advance dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for --advance dispatch. Overrides env and repo config.
      --step string               Manual gate step id to approve. Defaults to the next blocked manual gate.
      --wait                      With --advance, wait for the job to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
      --workspace string          Workspace mode for an advanced step: auto, worktree, or repo. (default "auto")
```

## `agent-team job attach`

Attach to a job&#39;s owning instance.

Attach to the instance recorded on a durable job. By default this opens the owning instance with the normal interactive attach flow. Passing log options such as --tail, --no-follow, --since, or --grep follows the daemon-captured log stream instead.

```text
agent-team job attach <job-id> [flags]
```

Aliases: `exec`

Flags:

```text
      --commands       With --dry-run, print the matching job attach or unmanaged fallback commands.
      --dry-run        Preview the owning instance handoff without stopping or resuming the daemon child.
      --grep string    Log mode with --no-follow: only print log lines matching this regular expression.
      --no-follow      Log mode: print the selected log tail and exit instead of following.
      --no-resume      Leave the owning instance in stopped state when the runtime exits.
      --repo string    Repo root containing .agent_team. (default "<repo>")
      --since string   Log mode with --no-follow: only print the log if it was modified since this duration ago (for example 10m, 24h) or RFC3339 timestamp.
      --step string    Use this pipeline step's owning instance.
      --tail string    Log mode: show only the last N lines before following (0 or all = all). (default "50")
```

## `agent-team job block`

Mark a job blocked with an operator reason.

Mark a durable job blocked and record an operator reason in the job audit history. Use `job hold` instead when work should keep its lifecycle status but automation should stop advancing it.

```text
agent-team job block <job-id> [reason...] [flags]
```

Flags:

```text
      --actor string          Actor label recorded in the blocked audit event. (default "cli")
      --commands              With --dry-run, print the matching job block apply command when the preview has actionable work.
      --dry-run               Preview the blocked job without changing job state or writing an audit event.
      --format string         Render the updated job or dry-run preview with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --json                  Emit the updated job or dry-run preview as JSON.
      --message string        Blocked reason recorded on the job.
      --message-file string   Read blocked reason from a file, or '-' for stdin.
      --repo string           Repo root containing .agent_team. (default "<repo>")
```

## `agent-team job bounce`

Re-queue a pipeline step with review findings.

Re-queue a pipeline step with review findings appended to the job kickoff. By default this selects the common review-bounce target: the single completed step with an owning instance. Pass --step to target a specific step.

```text
agent-team job bounce <job-id> [flags]
```

Flags:

```text
      --advance                   After recording the bounce, dispatch the re-queued step.
      --commands                  With --dry-run, print the matching job bounce apply command when the preview has actionable work.
      --dry-run                   Preview the bounce and optional advance dispatch without writing job or daemon state.
      --fail-on-failed            With --wait, exit 1 if the job resolves to failed.
      --findings string           Review findings to append to the job kickoff.
      --findings-file string      Read review findings from a file, or '-' for stdin.
      --format string             Render the updated job or advance result with a Go template, e.g. '{{.ID}} {{.Status}}' or '{{.Job.ID}} {{.Step.ID}}'.
      --json                      Emit the updated job or advance result as JSON.
      --repo string               Repo root containing .agent_team. (default "<repo>")
      --runtime string            Runtime profile for --advance dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for --advance dispatch. Overrides env and repo config.
      --step string               Pipeline step to re-queue. Defaults to the single completed step with an owning instance.
      --wait                      With --advance, wait for the job to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
      --workspace string          Workspace mode for --advance: auto, worktree, or repo. (default "auto")
```

## `agent-team job cancel`

Cancel a job as failed.

Cancel a durable job by marking it failed with a cancelled audit event. By default this only updates the job file; pass --stop or --kill to also stop the owning instance.

```text
agent-team job cancel <job-id> [reason...] [flags]
```

Flags:

```text
      --actor string            Actor label recorded in the cancellation audit event. (default "cli")
      --commands                With --dry-run, print the matching job cancel apply command when the preview has actionable work.
      --dry-run                 Preview the cancellation without changing daemon or job state.
      --format string           Render the cancellation result with a Go template, e.g. '{{.Job.ID}} {{.Job.Status}}'.
      --json                    Emit the cancellation result as JSON.
      --kill                    Force-stop the owning instance before recording the cancellation.
      --message string          Cancellation reason recorded on the job.
      --message-file string     Read cancellation reason from a file, or '-' for stdin.
      --repo string             Repo root containing .agent_team. (default "<repo>")
      --rm                      Remove selected instance state and daemon metadata after stopping or killing.
      --step string             Use this pipeline step's owning instance when combined with --stop or --kill.
      --stop                    Gracefully stop the owning instance before recording the cancellation.
      --timeout duration        Grace before --kill escalation, or wait deadline when used with --wait and no --wait-timeout.
      --wait                    Wait for the owning instance to reach a terminal state when --stop or --kill is set.
      --wait-timeout duration   Maximum time to wait for terminal state with --wait.
```

## `agent-team job cleanup`

Remove a done job&#39;s owned worker worktree and branch after merge.

Preview or remove job-owned worktrees and branches. Applying cleanup requires jobs marked done plus --merged after confirming the matching PR has merged.

```text
agent-team job cleanup <job-id>|--all [flags]
```

Flags:

```text
      --all             Clean all done jobs that still own a recorded worktree or branch.
      --commands        With --dry-run, print the matching cleanup apply command when the preview has actionable work.
      --dry-run         Preview the job-owned worktree and branch cleanup without removing anything.
      --force-branch    With --merged, delete the job branch with git branch -D if it is not locally merged.
      --format string   Render the cleanup result with a Go template, e.g. '{{.ID}} {{.LastStatus}}' or '{{.Total}} {{.Cleaned}}'.
      --json            Emit the updated job as JSON.
      --merged          Confirm the job's PR has merged before removing a done job's worktree and branch.
      --repo string     Repo root containing .agent_team. (default "<repo>")
      --verify-pr       Verify the recorded GitHub PR is merged with gh before cleanup.
```

## `agent-team job close`

Close a job as done or failed.

```text
agent-team job close <job-id> [message...] [flags]
```

Flags:

```text
      --actor string          Actor label recorded in the close audit event. (default "cli")
      --commands              With --dry-run, print the matching job close apply command when the preview has actionable work.
      --dry-run               Preview the close without changing job state or writing an audit event.
      --format string         Render the updated job with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --json                  Emit the updated job as JSON.
      --message string        Close message recorded on the job.
      --message-file string   Read close message from a file, or '-' for stdin.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --status string         Close status: done or failed. (default "done")
```

## `agent-team job create`

Create a durable job for a ticket.

```text
agent-team job create <ticket> [kickoff...] [flags]
```

Flags:

```text
      --commands                  With --dry-run, print the matching job create apply command.
      --dispatch                  Dispatch the created job immediately using the running daemon.
      --dry-run                   Preview the job that would be created without writing it.
      --fail-on-failed            With --wait, exit 1 if the job resolves to failed.
      --format string             Render the job with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --id string                 Override the normalized job id (default: ticket slug).
      --instance string           Instance name that owns the job (default set during dispatch).
      --json                      Emit the job as JSON.
      --kickoff string            Kickoff text for the target agent.
      --kickoff-file string       Read kickoff text from a file, or '-' for stdin.
      --pipeline string           Create this job from a declared pipeline in instances.toml.
      --repo string               Repo root containing .agent_team. (default "<repo>")
      --runtime string            Runtime profile for --dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for --dispatch. Overrides env and repo config.
      --target string             Target agent that should own this job. (default "worker")
      --ticket-url string         Canonical ticket URL to store on the job.
      --wait                      After creating or dispatching, wait for the job to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. dispatched, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
      --workspace string          Workspace mode for --dispatch: auto, worktree, or repo. (default "auto")
```

## `agent-team job dispatch`

Dispatch a job to its target agent.

```text
agent-team job dispatch <job-id> [flags]
```

Flags:

```text
      --commands                  With --dry-run, print the matching job dispatch apply command when the preview has actionable work.
      --dry-run                   Preview topology matches without publishing to the daemon or updating the job.
      --fail-on-failed            With --wait, exit 1 if the job resolves to failed.
      --format string             Render the updated job or dry-run preview with a Go template.
      --json                      Emit the updated job and daemon event outcome as JSON.
      --repo string               Repo root containing .agent_team. (default "<repo>")
      --runtime string            Runtime profile for the dispatched instance (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for the dispatched instance. Overrides env and repo config.
      --source string             Source instance for the dispatch event (default: AGENT_TEAM_INSTANCE or cli).
      --wait                      After dispatching, wait for the job to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. dispatched, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
      --workspace string          Workspace mode for spawned children: auto, worktree, or repo. (default "auto")
```

## `agent-team job doctor`

Validate durable job files.

Validate durable job TOML files under `.agent_team/jobs/` without relying on normal job listing paths.

```text
agent-team job doctor [flags]
```

Flags:

```text
      --commands        Print recommended follow-up commands, or with --quarantine --dry-run print the matching quarantine apply command.
      --dry-run         With --quarantine, preview files that would be moved.
      --format string   Render the job doctor result with a Go template, e.g. '{{.OK}} {{.Summary.Valid}}'.
      --json            Emit durable job doctor findings as JSON.
      --quarantine      Move job files with doctor problems out of the active jobs directory.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team job events`

Show a job&#39;s durable event history.

```text
agent-team job events [<job-id>|--all] [flags]
```

Flags:

```text
      --actor strings       Only show job events from this actor. Can repeat or comma-separate.
      --all                 Show durable events across all jobs.
  -f, --follow              Poll and print new job events until interrupted.
      --format string       Render each event with a Go template, e.g. '{{.TS}} {{.Type}} {{.Message}}'.
      --instance strings    Only show job events for this owning instance. Can repeat or comma-separate.
      --interval duration   Polling interval for --follow. (default 1s)
      --json                Emit machine-readable JSON. With --follow, emit one JSON object per line.
      --repo string         Repo root containing .agent_team. (default "<repo>")
      --since string        Only show job events since this duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --sort string         Sort returned events by oldest or newest. Follow mode always streams oldest first. (default "oldest")
      --status strings      Only show job events with this status: queued, running, blocked, done, or failed. Can repeat or comma-separate.
      --summary             Summarize matching job events by type, status, actor, and instance.
      --tail string         Show only the last N events before returning or following (0 or all = all). (default "0")
      --type strings        Only show job events with this type. Can repeat or comma-separate.
```

## `agent-team job explain`

Explain pipeline step readiness for one job.

Explain one job&#39;s pipeline state from the durable job file, including every step, dependency blockers, gates, ready/running/failed state, and suggested next actions.

```text
agent-team job explain <job-id> [flags]
```

Aliases: `watch`

Flags:

```text
      --commands            Print recommended commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string       Render the pipeline explanation with a Go template, e.g. '{{.State}} {{len .Steps}}'.
      --interval duration   Refresh interval for --watch. (default 2s)
      --json                Emit the pipeline explanation as JSON.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --repo string         Repo root containing .agent_team. (default "<repo>")
      --state strings       Only render when the job's next-step state matches: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --step string         Only include details for this pipeline step id.
  -w, --watch               Refresh the job pipeline explanation until interrupted.
```

## `agent-team job extend`

Extend a job&#39;s running watchdog deadline.

Extend the armed watchdog deadline for a job&#39;s running owning instance. Use --step for pipeline jobs when the target step is ambiguous.

```text
agent-team job extend <job-id> [flags]
```

Flags:

```text
      --actor string    Actor label recorded in the job audit event. (default "cli")
      --by duration     Amount to add to the running watchdog deadline, for example 30m.
      --format string   Render the extension result with a Go template, e.g. '{{.Job.ID}} {{.Extension.NewDeadline}}'.
      --json            Emit machine-readable JSON.
  -q, --quiet           Suppress non-error output and use only the exit code.
      --repo string     Repo root containing .agent_team. (default "<repo>")
      --step string     Use this pipeline step's owning instance.
```

## `agent-team job gate`

Write durable per-job gate results.

Write durable per-job gate results to the append-only gate log under `.agent_team/jobs/`.

```text
agent-team job gate
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team job gate set` - Append one gate result to a job.

## `agent-team job gate set`

Append one gate result to a job.

```text
agent-team job gate set <job-id> <gate-name> [flags]
```

Flags:

```text
      --actor string       Actor recorded on the gate result; defaults to AGENT_TEAM_INSTANCE or cli.
      --json               Emit the recorded gate result as JSON.
      --log-ref string     Path or URL with supporting gate output.
      --repo string        Repo root containing .agent_team. (default "<repo>")
      --signature string   Failure signature or short failure text used for infra/content classification.
      --status string      Gate status: pass or fail.
```

## `agent-team job gates`

Show latest gate results for one job.

Show latest per-name gate results from a job&#39;s append-only gate log. Failed gates are classified as infra or content using the job pipeline&#39;s infra_signatures.

```text
agent-team job gates <job-id> [flags]
```

Flags:

```text
      --json          Emit gate results as JSON.
      --repo string   Repo root containing .agent_team. (default "<repo>")
```

## `agent-team job graph`

Render one job&#39;s pipeline graph with step state.

Render the declared pipeline graph for one durable job, overlaying the job&#39;s current step status, blockers, instance ownership, attempts, and action hints.

```text
agent-team job graph <job-id> [flags]
```

Flags:

```text
      --commands        Print recommended commands from graph action hints, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string   Graph output format: text, mermaid, or dot. (default "text")
      --json            Emit graph nodes and edges as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
      --routes          Annotate step targets with matching agent.dispatch route instances.
```

## `agent-team job hold`

Hold a job so pipeline automation will not advance it.

Hold a durable job without changing its lifecycle status. Held jobs remain visible in status views, but next-step readiness reports held and automatic advance loops skip them until release. Use --all to hold matching jobs in a batch.

```text
agent-team job hold <job-id>|--all [reason...] [flags]
```

Aliases: `pause`

Flags:

```text
      --all                   Hold all matching jobs instead of one job.
      --commands              With --dry-run, print the matching hold apply command when the preview has actionable work.
      --dry-run               Preview the hold without writing job state.
      --for duration          Hold for this duration, for example 30m or 2h.
      --format string         Render the updated job or batch row with a Go template, e.g. '{{.ID}} {{.Held}} {{.HoldReason}}' or '{{.JobID}} {{.Action}}'.
      --json                  Emit the updated job as JSON.
      --limit int             With --all, hold at most this many matching jobs; 0 means no limit.
      --message string        Hold reason recorded on the job.
      --message-file string   Read hold reason from a file, or '-' for stdin.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --state strings         With --all, next-step state to hold: ready, queued, running, blocked, failed, held, done, none, or all. Defaults to active non-held, non-done jobs.
      --until string          Hold until this RFC3339 timestamp.
```

## `agent-team job keep-worktree`

Disable automatic worktree cleanup for a job.

Set a job&#39;s reap_worktree policy to never so its recorded worktree and branch are preserved when the job closes or its PR merges.

```text
agent-team job keep-worktree <job-id> [reason...] [flags]
```

Flags:

```text
      --actor string    Actor label recorded in the keep-worktree audit event. (default "cli")
      --format string   Render the updated job with a Go template, e.g. '{{.ID}} {{.ReapWorktree}}'.
      --json            Emit the updated job as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team job kill`

Force-stop a job&#39;s owning instance.

```text
agent-team job kill <job-id> [flags]
```

Flags:

```text
      --commands                With --dry-run, print the matching job kill command when the preview has actionable work.
      --dry-run                 Preview the kill action without changing daemon or job state.
      --format string           Render the lifecycle action with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                    Emit machine-readable lifecycle action JSON.
  -q, --quiet                   Suppress non-error output and use only the exit code.
      --repo string             Repo root containing .agent_team. (default "<repo>")
      --rm                      Remove selected instance state and daemon metadata after killing.
      --step string             Use this pipeline step's owning instance.
      --timeout duration        Grace before SIGKILL escalation. (default 2s)
      --wait                    Wait for the owning instance to reach a terminal state.
      --wait-timeout duration   Maximum time to wait for terminal state with --wait.
```

## `agent-team job logs`

Show a job&#39;s owning instance log.

```text
agent-team job logs <job-id> [flags]
```

Flags:

```text
      --clean          Hide known Codex runtime diagnostic noise before printing the owning instance log.
  -f, --follow         Tail the owning instance log; print new bytes as they appear.
      --grep string    Only print log lines matching this regular expression. One-shot reads only.
      --last-message   Show the clean final Codex response sidecar for the owning instance.
      --raw            Print the unprocessed owning instance log without Codex JSONL rendering.
      --repo string    Repo root containing .agent_team. (default "<repo>")
      --since string   Only print the log if it was modified since a duration ago (for example 10m, 24h) or RFC3339 timestamp.
      --step string    Use this pipeline step's owning instance.
      --tail string    Show only the last N lines before returning or following (0 or all = all). (default "0")
```

## `agent-team job ls`

List durable jobs.

```text
agent-team job ls [flags]
```

Flags:

```text
      --active-hold           Only show held jobs whose hold is still active or has no deadline.
      --branch string         Filter by branch.
      --commands              Print recommended follow-up commands from the visible job rows. agent-team follow-ups preserve the selected repo scope.
      --expired-hold          Only show held jobs whose hold_until has passed.
      --format string         Render each job with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --held                  Only show held jobs.
      --instance string       Filter by owning instance.
      --interval duration     Refresh interval for --watch. (default 2s)
      --json                  Emit machine-readable JSON.
      --limit int             Limit rows after filtering and sorting; 0 means no limit.
      --no-clear              With --watch, append snapshots instead of redrawing the terminal.
      --pipeline string       Filter by pipeline name.
      --pr string             Filter by PR URL or number substring.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --runtime strings       Filter by owning instance runtime: claude or codex. Can repeat or comma-separate.
      --sort string           Sort rows by id, status, target, ticket, created, updated, instance, runtime, branch, or pr. (default "id")
      --status string         Filter by status: queued, running, blocked, done, or failed.
      --summary               Show aggregate job counts instead of job rows.
      --target-agent string   Filter by target agent.
      --ticket string         Filter by ticket id or URL substring.
      --unheld                Only show jobs that are not held.
  -w, --watch                 Refresh the job table until interrupted.
```

## `agent-team job merge`

Apply a pipeline job&#39;s declared merge strategy.

Apply the merge mechanics declared by `[pipelines.&lt;name&gt;.merge]` for a durable job. The command does not dispatch agents or rerun gates; it only performs the selected squash, rebase, or script merge action.

```text
agent-team job merge <job-id> [flags]
```

Flags:

```text
      --base string       Base branch passed to merge mechanics. (default "main")
      --branch string     Head branch to merge. Required when the job has no recorded PR.
      --dry-run           Preview the merge strategy and drift classification without mutating git or job state.
      --json              Emit the merge result as JSON.
      --land string       Override final PR landing mode: squash, merge, or rebase.
      --repo string       Repo root containing .agent_team. (default "<repo>")
      --worktree string   Worktree path for local or script merge mechanics. Defaults to the job worktree, then the repo root.
```

## `agent-team job next`

Show the next pipeline step for a job without dispatching it.

```text
agent-team job next <job-id> [flags]
```

Flags:

```text
      --commands        Print only recommended commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string   Render the next-step state with a Go template, e.g. '{{.State}} {{.Step.ID}}'.
      --json            Emit the next-step state as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
      --state strings   Only render when the next-step state matches: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --step string     Only render when this pipeline step is the next step.
```

## `agent-team job note`

Append an operator note to a job&#39;s audit history.

Append an operator note to a durable job without sending a mailbox message or changing ownership. The note updates last_event and last_status, then records a durable job event.

```text
agent-team job note <job-id> [message...] [flags]
```

Flags:

```text
      --actor string          Actor label recorded in the note audit event. (default "cli")
      --commands              With --dry-run, print the matching job note apply command when the preview has actionable work.
      --dry-run               Preview the note without changing job state or writing an audit event.
      --format string         Render the updated job or dry-run preview with a Go template, e.g. '{{.ID}} {{.LastEvent}}'.
      --json                  Emit the updated job or dry-run preview as JSON.
      --message string        Note text recorded on the job.
      --message-file string   Read note text from a file, or '-' for stdin.
      --repo string           Repo root containing .agent_team. (default "<repo>")
```

## `agent-team job outbox`

List or control outbox events owned by one job.

List sandboxed agent outbox events owned by one durable job.

```text
agent-team job outbox <job-id> [flags]
```

Flags:

```text
      --commands            Print recommended commands from the visible job-owned outbox rows, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string       Render each job-owned outbox item with a Go template, e.g. '{{.ID}} {{.State}}'.
      --interval duration   Refresh interval for --watch. (default 2s)
      --json                Emit job-owned outbox rows as JSON.
      --limit int           Limit rows after filtering and sorting; 0 means no limit.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --repo string         Repo root containing .agent_team. (default "<repo>")
      --sort string         Sort rows by state, id, type, source, job, created, updated, or error. (default "state")
      --source strings      Filter by source agent/instance; repeat or comma-separate values.
      --state string        Filter by outbox state: pending, processed, or failed.
      --summary             Show aggregate outbox counts instead of rows.
      --type strings        Filter by event type; repeat or comma-separate values.
  -w, --watch               Refresh the job outbox table until interrupted.
```

Subcommands:

- `agent-team job outbox drop` - Drop outbox events owned by one job.
- `agent-team job outbox prune` - Prune old outbox events owned by one job.
- `agent-team job outbox quarantine` - List quarantined outbox files owned by one job.
- `agent-team job outbox retry` - Retry outbox events owned by one job.
- `agent-team job outbox show` - Show one outbox event owned by one job.

## `agent-team job outbox drop`

Drop outbox events owned by one job.

Remove one job-owned outbox event by id, or drop a filtered job-owned batch with --all. Batch drops default to failed events.

```text
agent-team job outbox drop <job-id> [id] [flags]
```

Flags:

```text
      --all              Drop all matching job-owned outbox events instead of one id.
      --commands         With --dry-run, print the matching job outbox drop apply command when the preview has actionable work.
      --dry-run          Preview the drop without removing the event.
      --format string    Render the drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --json             Emit machine-readable JSON.
      --limit int        With --all, drop at most this many matching outbox events; 0 means no limit.
      --repo string      Repo root containing .agent_team. (default "<repo>")
      --sort string      With --all, sort matching outbox events before limiting: state, id, type, source, job, created, updated, or error. (default "state")
      --source strings   With --all, filter by source agent/instance; repeat or comma-separate values.
      --state string     With --all, filter by outbox state: pending, processed, or failed. Defaults to failed.
      --type strings     With --all, filter by event type; repeat or comma-separate values.
```

## `agent-team job outbox prune`

Prune old outbox events owned by one job.

Prune old sandboxed agent outbox events owned by one durable job. By default this removes processed events; pass --state failed, pending, or all for explicit cleanup.

```text
agent-team job outbox prune <job-id> [flags]
```

Flags:

```text
      --commands              With --dry-run, print the matching job outbox prune apply command when the preview has actionable work.
      --dry-run               Preview job-owned outbox events that would be pruned without dropping them.
      --format string         Render each prune result with a Go template, e.g. '{{.ID}} {{.Dropped}}'.
      --json                  Emit prune results as JSON.
      --limit int             Prune at most this many matching job-owned outbox events; 0 means no limit.
      --older-than duration   Only prune items older than this duration based on processed/failed/update/create time.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --source strings        Filter by source agent/instance before pruning; repeat or comma-separate values.
      --state string          Outbox state to prune: processed, failed, pending, or all. (default "processed")
      --type strings          Filter by event type before pruning; repeat or comma-separate values.
```

## `agent-team job outbox quarantine`

List quarantined outbox files owned by one job.

List quarantined sandboxed agent outbox files owned by one durable job.

```text
agent-team job outbox quarantine <job-id> [flags]
```

Flags:

```text
      --commands         Print recommended commands from the visible job-owned quarantined outbox files, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string    Render each quarantined outbox file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.
      --json             Emit quarantined outbox files as JSON.
      --limit int        Limit rows after filtering and sorting; 0 means no limit.
      --repo string      Repo root containing .agent_team. (default "<repo>")
      --restorable       Only show quarantined files that can be restored.
      --sort string      Sort rows by path, state, id, type, source, job, created, updated, modified, restorable, or size. (default "path")
      --source strings   Filter by source agent/instance; repeat or comma-separate values.
      --state string     Filter by outbox state: pending, processed, or failed.
      --summary          Show aggregate job-owned quarantined outbox-file counts instead of rows.
      --type strings     Filter by event type; repeat or comma-separate values.
      --unrestorable     Only show quarantined files that cannot be restored.
```

Subcommands:

- `agent-team job outbox quarantine drop` - Drop job-owned quarantined outbox files after inspection.
- `agent-team job outbox quarantine restore` - Restore job-owned quarantined outbox files.
- `agent-team job outbox quarantine show` - Show one job-owned quarantined outbox file.

## `agent-team job outbox quarantine drop`

Drop job-owned quarantined outbox files after inspection.

Drop one job-owned quarantined outbox file by path, or drop a filtered job-owned batch with --all.

```text
agent-team job outbox quarantine drop <job-id> [quarantine-path] [flags]
```

Flags:

```text
      --all                   Drop all matching job-owned quarantined files instead of one path.
      --commands              With --dry-run, print the matching job outbox quarantine drop apply command when the preview has actionable work.
      --dry-run               Preview quarantined files that would be dropped.
      --format string         Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --json                  Emit drop results as JSON.
      --limit int             With --all, drop at most this many matching job-owned quarantined files; 0 means no limit.
      --older-than duration   With --all, only drop files older than this duration based on file mtime.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --restorable            With --all, only drop quarantined files that can be restored.
      --sort string           With --all, sort matching job-owned quarantined files before limiting: path, state, id, type, source, job, created, updated, modified, restorable, or size. (default "path")
      --source strings        With --all, filter by source agent/instance; repeat or comma-separate values.
      --state string          With --all, filter by outbox state: pending, processed, or failed.
      --type strings          With --all, filter by event type; repeat or comma-separate values.
      --unrestorable          With --all, only drop quarantined files that cannot be restored.
```

## `agent-team job outbox quarantine restore`

Restore job-owned quarantined outbox files.

Restore one job-owned quarantined outbox file by path, or restore a filtered batch of job-owned restorable files with --all.

```text
agent-team job outbox quarantine restore <job-id> [quarantine-path] [flags]
```

Flags:

```text
      --all              Restore all matching job-owned restorable quarantined files instead of one path.
      --commands         With --dry-run, print the matching job outbox quarantine restore apply command when the preview has actionable work.
      --dry-run          Preview the restore without moving files.
      --force            Overwrite an existing active outbox file with the same restore path.
      --format string    Render each restore result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --json             Emit restore result as JSON.
      --limit int        With --all, restore at most this many matching job-owned quarantined files; 0 means no limit.
      --repo string      Repo root containing .agent_team. (default "<repo>")
      --sort string      With --all, sort matching job-owned quarantined files before limiting: path, state, id, type, source, job, created, updated, modified, restorable, or size. (default "path")
      --source strings   With --all, filter by source agent/instance; repeat or comma-separate values.
      --state string     With --all, filter by outbox state: pending, processed, or failed.
      --type strings     With --all, filter by event type; repeat or comma-separate values.
```

## `agent-team job outbox quarantine show`

Show one job-owned quarantined outbox file.

```text
agent-team job outbox quarantine show <job-id> <quarantine-path> [flags]
```

Flags:

```text
      --commands        Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.
      --format string   Render the quarantined outbox file with a Go template, e.g. '{{.ID}} {{.State}}'.
      --json            Emit the quarantined outbox file as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team job outbox retry`

Retry outbox events owned by one job.

Move one job-owned processed or failed outbox event back to pending by id, or retry a filtered job-owned batch with --all. Batch retries default to failed events.

```text
agent-team job outbox retry <job-id> [id] [flags]
```

Aliases: `requeue`

Flags:

```text
      --all              Retry all matching job-owned outbox events instead of one id.
      --commands         With --dry-run, print the matching job outbox retry apply command when the preview has actionable work.
      --dry-run          Preview the retry without moving the event.
      --format string    Render the retry result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --json             Emit machine-readable JSON.
      --limit int        With --all, retry at most this many matching outbox events; 0 means no limit.
      --repo string      Repo root containing .agent_team. (default "<repo>")
      --sort string      With --all, sort matching outbox events before limiting: state, id, type, source, job, created, updated, or error. (default "state")
      --source strings   With --all, filter by source agent/instance; repeat or comma-separate values.
      --state string     With --all, filter by outbox state: pending, processed, or failed. Defaults to failed.
      --type strings     With --all, filter by event type; repeat or comma-separate values.
```

## `agent-team job outbox show`

Show one outbox event owned by one job.

```text
agent-team job outbox show <job-id> <id> [flags]
```

Flags:

```text
      --commands        Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.
      --format string   Render the job-owned outbox item with a Go template, e.g. '{{.ID}} {{.State}}'.
      --json            Emit the job-owned outbox item as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team job prune`

Remove terminal job files and their event logs.

Remove jobs in terminal statuses. By default, this removes done and failed jobs.

```text
agent-team job prune [flags]
```

Flags:

```text
      --commands         With --dry-run, print the matching job prune apply command when the preview has actionable work.
      --dry-run          Preview removals without deleting files.
      --format string    Render each result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --json             Emit removal results as JSON.
      --repo string      Repo root containing .agent_team. (default "<repo>")
      --status strings   Terminal status to prune: done, failed, or terminal. Can repeat or comma-separate.
```

## `agent-team job ps`

List instances owned by one job.

List daemon-aware instance rows owned by one durable job. Pipeline jobs can own several stage instances; pass --step to focus one stage.

```text
agent-team job ps <job-id> [flags]
```

Flags:

```text
      --agent strings       Only show job-owned instances for this agent. Can repeat or comma-separate.
  -a, --all                 Show all visible job-owned instances. Accepted for Docker compatibility; this is already the default.
      --format string       Render each row with a Go template, e.g. '{{.Instance}} {{.Status}}'.
      --interval duration   Refresh interval for --watch. (default 2s)
      --json                Emit JSON. With --watch, writes one JSON array per refresh.
  -n, --last int            Show only the N most recently started job-owned instances after other filters (0 = all).
  -l, --latest              Show only the most recently started job-owned instance after other filters.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --phase strings       Only show job-owned work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
  -q, --quiet               Only print matching job-owned instance names.
      --repo string         Repo root containing .agent_team. (default "<repo>")
      --runtime strings     Only show job-owned instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale       Only show job-owned running instances whose recorded runtime PID is no longer live.
      --sort string         Sort rows by name, status, agent, phase, stale, runtime-stale, unhealthy, started, stopped, or exited. (default "name")
      --stale               Only show job-owned instances whose status.toml is stale.
      --status strings      Only show job-owned lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --step string         List this pipeline step's owning instance.
      --summary             Show lifecycle counts instead of job instance rows.
      --unhealthy           Only show crashed, status-stale, or runtime-stale job-owned instances.
  -w, --watch               Refresh job instance rows until interrupted.
```

## `agent-team job quarantine`

Inspect, restore, and drop quarantined job files.

Inspect durable job TOML files moved under `.agent_team/jobs/quarantine/`, restore validated files to the active jobs directory, or explicitly drop preserved files.

```text
agent-team job quarantine [flags]
```

Flags:

```text
      --commands        Print recommended commands from the visible quarantined job files, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string   Render each quarantined job file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.
      --json            Emit quarantined job files as JSON.
      --limit int       Limit rows after filtering and sorting; 0 means no limit.
      --repo string     Repo root containing .agent_team. (default "<repo>")
      --restorable      Only show quarantined job files that can be restored.
      --sort string     Sort rows by path, id, ticket, target, status, modified, restorable, or size. (default "path")
      --summary         Show aggregate quarantined job-file counts instead of rows.
      --unrestorable    Only show quarantined job files that cannot be restored.
```

Subcommands:

- `agent-team job quarantine drop` - Drop one quarantined job file after inspection.
- `agent-team job quarantine restore` - Restore one validated quarantined job file.
- `agent-team job quarantine show` - Show one quarantined job file.

## `agent-team job quarantine drop`

Drop one quarantined job file after inspection.

```text
agent-team job quarantine drop <quarantine-path> [flags]
```

Flags:

```text
      --commands        With --dry-run, print the matching job quarantine drop apply command when the preview has actionable work.
      --dry-run         Preview the quarantined job file drop without deleting it.
      --format string   Render the drop result with a Go template, e.g. '{{.Path}} {{.Action}}'.
      --json            Emit the drop result as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team job quarantine restore`

Restore one validated quarantined job file.

```text
agent-team job quarantine restore <quarantine-path> [flags]
```

Flags:

```text
      --commands        With --dry-run, print the matching job quarantine restore apply command when the preview has actionable work.
      --dry-run         Preview the job file restore without moving it.
      --force           Overwrite an existing active job file with the same id.
      --format string   Render the restore result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --json            Emit the restore result as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team job quarantine show`

Show one quarantined job file.

```text
agent-team job quarantine show <quarantine-path> [flags]
```

Flags:

```text
      --commands        Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.
      --format string   Render the quarantined job file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.
      --json            Emit the quarantined job file as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team job queue`

List queue items owned by one job.

List persisted daemon queue items owned by one durable job.

```text
agent-team job queue <job-id> [flags]
```

Flags:

```text
      --commands             Print recommended commands from the visible job queue rows, one per line. agent-team follow-ups preserve the selected repo scope.
      --event-type strings   Filter by event type; repeat or comma-separate values.
      --format string        Render each queue item with a Go template, e.g. '{{.ID}} {{.State}}'.
      --interval duration    Refresh interval for --watch. (default 2s)
      --json                 Emit machine-readable JSON.
      --limit int            Limit rows after filtering and sorting; 0 means no limit.
      --no-clear             With --watch, append snapshots instead of redrawing the terminal.
      --ready                Only show pending queue items whose next retry is due now.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --runtime strings      Filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.
      --sort string          Sort rows by state, id, event, instance, job, runtime, queued, updated, next-retry, or attempts. (default "state")
      --state string         Filter by queue state: pending or dead.
      --summary              Show aggregate queue counts instead of queue rows.
  -w, --watch                Refresh the job queue table until interrupted.
```

Subcommands:

- `agent-team job queue drop` - Drop queue items owned by one job.
- `agent-team job queue prune` - Prune queue items owned by one job.
- `agent-team job queue quarantine` - List quarantined queue files owned by one job.
- `agent-team job queue retry` - Retry queue items owned by one job.
- `agent-team job queue show` - Show one queue item owned by one job.

## `agent-team job queue drop`

Drop queue items owned by one job.

Drop one job-owned queue item by id, or drop a filtered job-owned batch with --all. Batch drops default to dead-letter items.

```text
agent-team job queue drop <job-id> [id] [flags]
```

Flags:

```text
      --all                  Drop all matching job-owned queue items instead of one id.
      --commands             With --dry-run, print the matching job queue drop command when the preview has actionable work.
      --dry-run              Preview matching job-owned queue items without dropping them.
      --event-type strings   With --all, filter by event type; repeat or comma-separate values.
      --format string        Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --json                 Emit machine-readable JSON.
      --limit int            With --all, drop at most this many matching queue items; 0 means no limit.
      --ready                With --all, only drop pending queue items whose next retry is due now.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --runtime strings      With --all, filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.
      --sort string          With --all, sort matching queue items before limiting: state, id, event, instance, job, runtime, queued, updated, next-retry, or attempts. (default "state")
      --state string         With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.
```

## `agent-team job queue prune`

Prune queue items owned by one job.

Prune queue items owned by one durable job. By default this removes dead-letter items.

```text
agent-team job queue prune <job-id> [flags]
```

Flags:

```text
      --commands              With --dry-run, print the matching job queue prune command when the preview has actionable work.
      --dry-run               Preview job-owned queue items that would be pruned without dropping them.
      --event-type strings    Filter by event type before pruning; repeat or comma-separate values.
      --format string         Render each prune result with a Go template, e.g. '{{.ID}} {{.State}}'.
      --json                  Emit prune results as JSON.
      --limit int             Prune at most this many matching job-owned queue items; 0 means no limit.
      --older-than duration   Only prune job-owned items older than this duration based on retry/dead-letter/update time.
      --ready                 Only prune pending queue items whose next retry is due now. Defaults --state to pending when --state is omitted.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --runtime strings       Filter by queued dispatch runtime before pruning: claude or codex. Can repeat or comma-separate.
      --state string          Queue state to prune: dead, pending, or all. (default "dead")
```

## `agent-team job queue quarantine`

List quarantined queue files owned by one job.

List quarantined queue files owned by one durable job.

```text
agent-team job queue quarantine <job-id> [flags]
```

Flags:

```text
      --commands             Print recommended commands from the visible job-owned quarantined queue files, one per line. agent-team follow-ups preserve the selected repo scope.
      --event-type strings   Filter by event type; repeat or comma-separate values.
      --format string        Render each quarantined queue file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.
      --json                 Emit quarantined queue files as JSON.
      --limit int            Limit rows after filtering and sorting; 0 means no limit.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --restorable           Only show quarantined files that can be restored.
      --sort string          Sort rows by path, state, id, event, instance, job, queued, updated, modified, attempts, restorable, or size. (default "path")
      --state string         Filter by queue state: pending or dead.
      --summary              Show aggregate job-owned quarantined queue-file counts instead of rows.
      --unrestorable         Only show quarantined files that cannot be restored.
```

Subcommands:

- `agent-team job queue quarantine drop` - Drop job-owned quarantined queue files after inspection.
- `agent-team job queue quarantine restore` - Restore job-owned quarantined queue files.
- `agent-team job queue quarantine show` - Show one job-owned quarantined queue file.

## `agent-team job queue quarantine drop`

Drop job-owned quarantined queue files after inspection.

Drop one job-owned quarantined queue file by path, or drop a filtered job-owned batch with --all.

```text
agent-team job queue quarantine drop <job-id> [quarantine-path] [flags]
```

Flags:

```text
      --all                   Drop all matching job-owned quarantined files instead of one path.
      --commands              With --dry-run, print the matching job queue quarantine drop apply command when the preview has actionable work.
      --dry-run               Preview quarantined files that would be dropped.
      --event-type strings    With --all, filter by event type; repeat or comma-separate values.
      --format string         Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --json                  Emit drop results as JSON.
      --limit int             With --all, drop at most this many matching job-owned quarantined files; 0 means no limit.
      --older-than duration   With --all, only drop files older than this duration based on file mtime.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --restorable            With --all, only drop quarantined files that can be restored.
      --sort string           With --all, sort matching job-owned quarantined files before limiting: path, state, id, event, instance, job, queued, updated, modified, attempts, restorable, or size. (default "path")
      --state string          With --all, filter by queue state: pending or dead.
      --unrestorable          With --all, only drop quarantined files that cannot be restored.
```

## `agent-team job queue quarantine restore`

Restore job-owned quarantined queue files.

Restore one job-owned quarantined queue file by path, or restore a filtered batch of job-owned restorable files with --all.

```text
agent-team job queue quarantine restore <job-id> [quarantine-path] [flags]
```

Flags:

```text
      --all                  Restore all matching job-owned restorable quarantined files instead of one path.
      --commands             With --dry-run, print the matching job queue quarantine restore apply command when the preview has actionable work.
      --dry-run              Preview the restore without moving files.
      --event-type strings   With --all, filter by event type; repeat or comma-separate values.
      --force                Overwrite an existing active queue file with the same restore path.
      --format string        Render each restore result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --json                 Emit restore result as JSON.
      --limit int            With --all, restore at most this many matching job-owned quarantined files; 0 means no limit.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --sort string          With --all, sort matching job-owned quarantined files before limiting: path, state, id, event, instance, job, queued, updated, modified, attempts, restorable, or size. (default "path")
      --state string         With --all, filter by queue state: pending or dead.
```

## `agent-team job queue quarantine show`

Show one job-owned quarantined queue file.

```text
agent-team job queue quarantine show <job-id> <quarantine-path> [flags]
```

Flags:

```text
      --commands        Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.
      --format string   Render the quarantined queue file with a Go template, e.g. '{{.ID}} {{.State}}'.
      --json            Emit the quarantined queue file as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team job queue retry`

Retry queue items owned by one job.

Retry one job-owned queue item by id, or retry a filtered job-owned batch with --all. Batch retries default to dead-letter items.

```text
agent-team job queue retry <job-id> [id] [flags]
```

Flags:

```text
      --all                  Retry all matching job-owned queue items instead of one id.
      --commands             With --dry-run, print the matching job queue retry command when the preview has actionable work.
      --dry-run              Preview matching job-owned queue items without retrying them.
      --event-type strings   With --all, filter by event type; repeat or comma-separate values.
      --format string        Render each retry result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --json                 Emit machine-readable JSON.
      --limit int            With --all, retry at most this many matching queue items; 0 means no limit.
      --ready                With --all, only retry pending queue items whose next retry is due now.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --runtime strings      With --all, filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.
      --sort string          With --all, sort matching queue items before limiting: state, id, event, instance, job, runtime, queued, updated, next-retry, or attempts. (default "state")
      --state string         With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.
```

## `agent-team job queue show`

Show one queue item owned by one job.

```text
agent-team job queue show <job-id> <id> [flags]
```

Flags:

```text
      --commands        Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.
      --format string   Render the queue item with a Go template, e.g. '{{.ID}} {{.State}}'.
      --json            Emit the queue item as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team job ready`

List pipeline jobs with ready or selected next-step states.

```text
agent-team job ready [flags]
```

Flags:

```text
      --commands            Print only recommended commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string       Render each row with a Go template, e.g. '{{.JobID}} {{.State}} {{.StepID}}'.
      --interval duration   Refresh interval for --watch. (default 2s)
      --json                Emit ready rows as JSON.
      --limit int           Limit rows after filtering and sorting; 0 means no limit.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --pipeline string     Filter by pipeline name.
      --repo string         Repo root containing .agent_team. (default "<repo>")
      --sort string         Sort rows by job, state, step, target, pipeline, updated, ticket, instance, or label. (default "job")
      --state strings       Next-step state to include: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --step string         Only include rows whose next step has this id.
  -w, --watch               Refresh the ready-step table until interrupted.
```

## `agent-team job reconcile`

Reconcile external runtime state back into jobs.

```text
agent-team job reconcile
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team job reconcile events` - Reconcile terminal daemon instance metadata back into owning jobs.
- `agent-team job reconcile github` - Reconcile a GitHub PR webhook payload with its owning job.
- `agent-team job reconcile queue` - Reconcile persisted queue state back into owning jobs.
- `agent-team job reconcile status` - Reconcile instance status.toml files back into owning jobs.

## `agent-team job reconcile events`

Reconcile terminal daemon instance metadata back into owning jobs.

```text
agent-team job reconcile events [flags]
```

Flags:

```text
      --commands              With --dry-run, print the matching job reconcile events apply command when the preview has actionable work.
      --dry-run               Preview job updates without writing them.
      --format string         Render each result with a Go template, e.g. '{{.JobID}} {{.After}}'.
      --json                  Emit machine-readable JSON.
      --pipeline string       Only reconcile jobs owned by this pipeline.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --target-agent string   Only reconcile jobs targeting this agent.
```

## `agent-team job reconcile github`

Reconcile a GitHub PR webhook payload with its owning job.

```text
agent-team job reconcile github [flags]
```

Flags:

```text
      --advance                   After reconciling PR metadata, dispatch the next ready pipeline step.
      --cleanup-merged            After a merged PR event, remove the job-owned worktree and branch.
      --commands                  With --dry-run, print the matching job reconcile github apply command.
      --dry-run                   Preview the owning job update without writing it.
      --fail-on-failed            With --wait, exit 1 if the job resolves to failed.
      --format string             Render the reconciled job with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --json                      Emit the normalized event and reconciled job as JSON.
      --payload string            GitHub webhook JSON object.
      --payload-file string       Read GitHub webhook JSON from a file, or '-' for stdin.
      --repo string               Repo root containing .agent_team. (default "<repo>")
      --runtime string            Runtime profile for --advance dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for --advance dispatch. Overrides env and repo config.
      --verify-pr                 With --cleanup-merged, verify the recorded GitHub PR is merged with gh before cleanup.
      --wait                      With --advance, wait for the job to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
      --workspace string          Workspace mode for --advance dispatch: auto, worktree, or repo. (default "auto")
```

## `agent-team job reconcile queue`

Reconcile persisted queue state back into owning jobs.

```text
agent-team job reconcile queue [flags]
```

Flags:

```text
      --commands              With --dry-run, print the matching job reconcile queue apply command when the preview has actionable work.
      --dry-run               Preview job updates without writing them.
      --format string         Render each result with a Go template, e.g. '{{.JobID}} {{.After}}'.
      --json                  Emit machine-readable JSON.
      --pipeline string       Only reconcile jobs owned by this pipeline.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --state string          Queue state to reconcile: pending, dead, or all. (default "all")
      --target-agent string   Only reconcile jobs targeting this agent.
```

## `agent-team job reconcile status`

Reconcile instance status.toml files back into owning jobs.

```text
agent-team job reconcile status [flags]
```

Flags:

```text
      --commands              With --dry-run, print the matching job reconcile status apply command when the preview has actionable work.
      --dry-run               Preview job updates without writing them.
      --format string         Render each result with a Go template, e.g. '{{.JobID}} {{.After}}'.
      --json                  Emit machine-readable JSON.
      --pipeline string       Only reconcile jobs owned by this pipeline.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --target-agent string   Only reconcile jobs targeting this agent.
```

## `agent-team job reject`

Reject a blocked manual pipeline gate.

Reject a blocked manual pipeline gate by marking the gate step failed. By default this selects the next blocked manual gate for the job; pass --step to reject a specific gate.

```text
agent-team job reject <job-id> [reason...] [flags]
```

Flags:

```text
      --commands              With --dry-run, print the matching job reject apply command when the preview has actionable work.
      --dry-run               Preview rejection without writing job state.
      --format string         Render the updated job with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --json                  Emit the updated job as JSON.
      --message string        Rejection reason recorded on the job.
      --message-file string   Read rejection reason from a file, or '-' for stdin.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --step string           Manual gate step id to reject. Defaults to the next blocked manual gate.
```

## `agent-team job release`

Release a held job so pipeline automation can advance it.

Release a held durable job without changing its lifecycle status. After release, ready and advance commands evaluate the job&#39;s pipeline steps normally. Use --all to release matching held jobs in a batch.

```text
agent-team job release <job-id>|--all [message...] [flags]
```

Aliases: `resume`, `unpause`

Flags:

```text
      --all                   Release all matching held jobs instead of one job.
      --commands              With --dry-run, print the matching release apply command when the preview has actionable work.
      --dry-run               Preview the release without writing job state.
      --expired               With --all, only release held jobs whose hold_until has passed.
      --format string         Render the updated job or batch row with a Go template, e.g. '{{.ID}} {{.Held}} {{.LastStatus}}' or '{{.JobID}} {{.Action}}'.
      --json                  Emit the updated job as JSON.
      --limit int             With --all, release at most this many held jobs; 0 means no limit.
      --message string        Release message recorded on the job.
      --message-file string   Read release message from a file, or '-' for stdin.
      --repo string           Repo root containing .agent_team. (default "<repo>")
```

## `agent-team job reopen`

Reopen a durable job for another attempt.

Reopen a durable job by resetting its lifecycle status to queued or blocked. Running jobs are refused unless --force is set. Pass --dispatch to immediately send the reopened job to its target, and --wait to block until the retried job reaches a status or event.

```text
agent-team job reopen <job-id> [flags]
```

Aliases: `retry`

Flags:

```text
      --commands                  With --dry-run, print the matching job reopen/retry apply command when the preview has actionable work.
      --dispatch                  Dispatch the reopened job immediately using the running daemon.
      --dry-run                   Preview the reopened job and optional dispatch without writing job or daemon state.
      --fail-on-failed            With --wait, exit 1 if the job resolves to failed.
  -f, --force                     Allow reopening a job currently marked running.
      --format string             Render the updated job or dry-run preview with a Go template.
      --json                      Emit the updated job or dry-run preview as JSON.
      --message string            Status message recorded on the job.
      --repo string               Repo root containing .agent_team. (default "<repo>")
      --runtime string            Runtime profile for --dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for --dispatch. Overrides env and repo config.
      --source string             Source instance for --dispatch (default: AGENT_TEAM_INSTANCE or cli).
      --status string             Reopened status: queued or blocked. (default "queued")
      --wait                      After reopening or dispatching, wait for the job to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
      --workspace string          Workspace mode for --dispatch: auto, worktree, or repo. (default "auto")
```

## `agent-team job resume-plan`

Show runtime resume and fallback commands for one job.

Show runtime resume and fallback commands for daemon metadata owned by one durable job. This is the job-scoped form of `agent-team runtime resume-plan --job &lt;job-id&gt;`.

```text
agent-team job resume-plan <job-id> [flags]
```

Flags:

```text
      --action strings    Only include plans whose recommended action is start, attach, resume, or logs. Can repeat or comma-separate.
      --can-managed       Only include runtimes with enough session metadata for daemon-managed resume.
      --commands          Print only recommended commands, one per line, after filtering, sorting, and limiting. agent-team follow-ups preserve the selected repo scope.
      --direct            Only include runtimes with a direct runtime resume command.
      --fallbacks         With --commands, print all viable start, attach, log, last-message, and direct resume commands per plan.
      --format string     Render each plan with a Go template, e.g. '{{.Instance}} {{.RecommendedAction}} {{.RecommendedCommand}}'.
      --json              Emit machine-readable JSON.
      --last-message      For Codex log fallbacks, recommend the clean last-message sidecar instead of following raw logs.
      --limit int         Limit plans after filtering and sorting; 0 means no limit.
      --managed           Only include runtimes whose adapter supports daemon-managed resume.
      --repo string       Repo root containing .agent_team. (default "<repo>")
      --runtime strings   Only include metadata for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale     Only include running metadata whose recorded runtime PID is no longer live.
      --sort string       Sort plans before rendering by instance, action, runtime, status, stale, job, pipeline, step, or agent. (default "instance")
      --stale             Only include running metadata whose recorded runtime PID is no longer live. Compatibility alias for --runtime-stale.
      --status strings    Only include metadata with this status: running, stopped, exited, or crashed. Can repeat or comma-separate.
      --step string       Only include plans for this pipeline step id.
      --summary           Summarize matching resume plans by recommended action, runtime, and status.
      --unhealthy         Only include crashed or stale running metadata.
```

## `agent-team job rm`

Remove job files and their event logs.

Remove durable job TOML files and their sibling event logs. Queued, running, and blocked jobs are refused unless --force is set.

```text
agent-team job rm <job-id> [<job-id>...] [flags]
```

Aliases: `remove`

Flags:

```text
      --commands        With --dry-run, print the matching job rm apply command when the preview has actionable work.
      --dry-run         Preview removals without deleting files.
  -f, --force           Allow removing queued, running, or blocked jobs.
      --format string   Render each result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --json            Emit removal results as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team job runtime`

Inspect job-owned runtime metadata.

Inspect raw daemon runtime metadata owned by one durable job.

```text
agent-team job runtime
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team job runtime ls` - List daemon runtime metadata owned by one job.

## `agent-team job runtime ls`

List daemon runtime metadata owned by one job.

List raw daemon runtime metadata owned by one durable job. Pipeline jobs can own several stage instances; pass --step to focus one stage.

```text
agent-team job runtime ls <job-id> [flags]
```

Aliases: `list`, `ps`

Flags:

```text
      --agent strings      Only show job-owned metadata for this agent. Can repeat or comma-separate.
      --format string      Render each job runtime row with a Go template, e.g. '{{.Instance}} {{.Runtime}} {{.Status}}'.
      --instance strings   Only show job-owned metadata with this instance name. Can repeat or comma-separate.
      --json               Emit job runtime metadata as JSON.
  -n, --last int           Show only the N most recently started job-owned runtime records after other filters (0 = all).
  -l, --latest             Show only the most recently started job-owned runtime record after other filters.
      --repo string        Repo root containing .agent_team. (default "<repo>")
      --runtime strings    Only show job-owned metadata for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale      Only show job-owned running metadata whose recorded runtime PID is no longer live.
      --sort string        Sort job runtime rows by instance, status, runtime, agent, stale, unhealthy, job, started, stopped, or exited. (default "instance")
      --status strings     Only show job-owned runtime status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --step string        Only show metadata for this pipeline step's owning instance.
      --summary            Summarize matching job-owned runtime metadata by status, runtime, and agent.
      --unhealthy          Only show crashed or runtime-stale job-owned metadata.
```

## `agent-team job send`

Send a mailbox message to a job&#39;s owning instance.

```text
agent-team job send <job-id> [message...] [flags]
```

Flags:

```text
      --allow-missing         Allow queueing a message for an instance the daemon does not know yet.
      --commands              With --dry-run, print the matching job send apply command when the preview has actionable work.
      --dry-run               Preview the send without appending a mailbox message or updating the job.
      --format string         Render the updated job or dry-run preview with a Go template, e.g. '{{.ID}} {{.LastEvent}}'.
      --from string           Sender label recorded with the message. (default "(cli)")
      --json                  Emit the updated job or batch rows as JSON.
      --message string        Message text to send.
      --message-file string   Read message text from a file, or '-' for stdin.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --step string           Use this pipeline step's owning instance.
```

## `agent-team job show`

Show one durable job.

```text
agent-team job show <job-id> [flags]
```

Aliases: `inspect`

Flags:

```text
      --commands             Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.
      --events string        Include the last N job events in the detail output, or all. (default "5")
      --events-sort string   Sort included job events by oldest or newest after applying --events. (default "oldest")
      --format string        Render the job with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --json                 Emit the job as JSON.
      --repo string          Repo root containing .agent_team. (default "<repo>")
```

## `agent-team job snapshot`

Capture a job-scoped diagnostic snapshot.

Capture a read-only diagnostic snapshot for one durable job, including job state, audit events, daemon lifecycle rows, combined audit/lifecycle timeline rows, queue/outbox ownership, inbox summaries, runtime metadata, state files, optional log tail content, and command provenance.

```text
agent-team job snapshot <job-id> [flags]
```

Flags:

```text
      --commands             Print snapshot follow-up commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --events int           Recent job and lifecycle events to include. Use -1 for all events or 0 to skip events. (default 20)
      --events-sort string   Sort included job and lifecycle events by oldest or newest after applying --events. (default "oldest")
      --format string        Render the job snapshot with a Go template, e.g. '{{.Job.ID}} {{.Job.Status}}'.
      --json                 Emit the full job snapshot JSON to stdout.
      --no-redact            Include raw queue/outbox payload values and latest inbox bodies instead of redacting them.
  -o, --output string        Write the full JSON snapshot to this file. Use '-' for stdout.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --tail int             Include the last N log lines in JSON output. Use -1 for the full log or 0 to omit log content.
```

## `agent-team job start`

Start or resume a job&#39;s owning instance.

```text
agent-team job start <job-id> [flags]
```

Flags:

```text
      --commands                 With --dry-run, print the matching job start command when the preview has actionable work.
      --dry-run                  Preview the start/resume action without changing daemon or job state.
      --format string            Render the lifecycle action with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                     Emit machine-readable lifecycle action JSON.
  -q, --quiet                    Suppress non-error output and use only the exit code.
      --ready-timeout duration   Maximum time to wait for implicit daemon readiness (0 = no timeout). (default 3s)
      --repo string              Repo root containing .agent_team. (default "<repo>")
      --step string              Use this pipeline step's owning instance.
      --timeout duration         Maximum time to wait with --wait (0 = no timeout).
      --wait                     Wait for the owning instance to become healthy after starting or resuming.
```

## `agent-team job stats`

Show CPU and memory usage for a job&#39;s instances.

Show a one-shot or watchable resource snapshot for daemon-known instances owned by one durable job. Pipeline jobs can own several stage instances; pass --step to focus one stage. With no filters, only running job-owned instances are shown.

```text
agent-team job stats <job-id> [flags]
```

Aliases: `top`

Flags:

```text
      --agent strings       Only show job-owned instances for this agent. Can repeat or comma-separate.
  -a, --all                 Include stopped, exited, and crashed job-owned instances.
      --format string       Render each row with a Go template, e.g. '{{.Instance}} {{.CPUPercent}} {{.RSS}}'.
      --interval duration   Refresh interval for --watch. (default 2s)
      --json                Emit JSON. With --watch, writes one JSON array per refresh.
  -n, --last int            Show stats for the N most recently started job-owned instances after other filters (0 = all).
      --latest              Show stats for the most recently started job-owned instance after other filters.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --phase strings       Only show job-owned instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --repo string         Repo root containing .agent_team. (default "<repo>")
      --runtime strings     Only show job-owned instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale       Only show job-owned running instances whose recorded runtime PID is no longer live.
      --sort string         Sort rows by name, cpu, mem, rss, status, agent, phase, stale, runtime-stale, or unhealthy. (default "name")
      --stale               Only show job-owned instances whose status.toml is stale.
      --status strings      Only show job-owned lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --step string         Show stats for this pipeline step's owning instance.
      --summary             Show aggregate CPU, memory, and RSS totals instead of job instance rows.
      --unhealthy           Only show crashed, status-stale, or runtime-stale job-owned instances.
  -w, --watch               Refresh job stats until interrupted.
```

## `agent-team job step`

Update a pipeline job step status.

```text
agent-team job step <job-id> <step-id> [flags]
```

Flags:

```text
      --advance                   After marking the step done, dispatch the next ready step.
      --branch string             Branch name to record on the job.
      --commands                  With --dry-run, print the matching job step apply command when the preview has actionable work.
      --dry-run                   Preview the step update and optional advance dispatch without writing job or daemon state.
      --fail-on-failed            With --wait, exit 1 if the job resolves to failed.
  -f, --force                     Allow marking a step running without an owning instance.
      --format string             Render the updated job or advance result with a Go template, e.g. '{{.ID}} {{.Status}}' or '{{.Job.ID}} {{.Step.ID}}'.
      --instance string           Instance that owns or completed this step.
      --json                      Emit the updated job or advance result as JSON.
      --message string            Status message recorded on the job.
      --pr string                 PR URL to record on the job.
      --repo string               Repo root containing .agent_team. (default "<repo>")
      --runtime string            Runtime profile for --advance dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for --advance dispatch. Overrides env and repo config.
      --skip                      Mark this step as intentionally skipped; stored as done so dependent steps can continue.
      --status string             Step status: queued, running, blocked, done, or failed. (default "done")
      --wait                      With --advance, wait for the job to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
      --workspace string          Workspace mode for an advanced step: auto, worktree, or repo. (default "auto")
      --worktree string           Worktree path to record on the job.
```

## `agent-team job stop`

Stop a job&#39;s owning instance.

```text
agent-team job stop <job-id> [flags]
```

Flags:

```text
      --commands                With --dry-run, print the matching job stop command when the preview has actionable work.
      --dry-run                 Preview the stop action without changing daemon or job state.
  -f, --force                   Escalate to SIGKILL if the owning instance does not stop within --timeout.
      --format string           Render the lifecycle action with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                    Emit machine-readable lifecycle action JSON.
  -q, --quiet                   Suppress non-error output and use only the exit code.
      --repo string             Repo root containing .agent_team. (default "<repo>")
      --rm                      Remove selected instance state and daemon metadata after stopping.
      --step string             Use this pipeline step's owning instance.
      --timeout duration        Grace before --force kills. With --wait and no --wait-timeout, also used as the wait deadline.
      --wait                    Wait for the owning instance to reach a terminal state.
      --wait-timeout duration   Maximum time to wait for terminal state with --wait.
```

## `agent-team job timeline`

Show a combined job audit and lifecycle timeline.

Show one durable job&#39;s audit events together with matching daemon lifecycle events, or show a combined timeline across every durable job. Timeline rows are read-only and sorted across both sources by event time.

```text
agent-team job timeline [<job-id>|--all] [flags]
```

Flags:

```text
      --actor strings      Only show job-audit timeline rows from this actor. Can repeat or comma-separate.
      --agent strings      Only show lifecycle timeline rows for this agent. Can repeat or comma-separate.
      --all                Show timelines across all durable jobs.
      --format string      Render each timeline row with a Go template, e.g. '{{.TS}} {{.Source}} {{.Kind}} {{.Message}}'.
      --instance strings   Only show timeline rows for this owning instance. Can repeat or comma-separate.
      --job strings        Only show timeline rows for this job id. Can repeat or comma-separate.
      --json               Emit machine-readable JSON.
      --kind strings       Only show timeline rows with this kind/action. Can repeat or comma-separate.
      --repo string        Repo root containing .agent_team. (default "<repo>")
      --since string       Only show timeline rows since this duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --sort string        Sort returned timeline rows by oldest or newest after applying --tail. (default "oldest")
      --source string      Timeline source to include: all, job, or lifecycle. (default "all")
      --status strings     Only show timeline rows with this status. Can repeat or comma-separate.
      --summary            Summarize matching timeline rows by source, kind, status, actor, instance, and agent.
      --tail string        Show only the last N combined events before sorting for display (0 or all = all). (default "0")
```

## `agent-team job timeout`

Mark stale running job work failed.

Mark or preview stale running work for one durable job, or across all jobs with --all. Pipeline steps use their step timeout first, then [health].job_stale_after. A step-less running job uses [health].job_stale_after.

```text
agent-team job timeout <job-id>|--all [flags]
```

Flags:

```text
      --all                   Mark stale running work across all jobs.
      --commands              With --dry-run, print the matching timeout apply command when the preview has actionable work.
      --dry-run               Preview stale-work failure without writing job state.
      --format string         Render each result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.
      --json                  Emit timeout results as JSON.
      --limit int             With --all, mark at most this many stale running jobs or steps failed; 0 means no limit.
      --message string        Status message recorded on the timed-out job.
      --message-file string   Read timeout message from a file, or '-' for stdin.
      --pipeline string       With --all, mark only stale work owned by this pipeline.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --step string           Mark only a stale running step with this id failed.
      --target-agent string   With --all, mark only stale work targeting this agent.
```

## `agent-team job triage`

Show jobs that need operator attention.

Show a compact work queue triage view from durable jobs, persisted daemon queue items, status-file update previews, and ready pipeline steps.

```text
agent-team job triage [flags]
```

Flags:

```text
      --commands               Print only recommended commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --content-only           Only show attention rows with failed gates classified as content.
      --format string          Render the triage snapshot with a Go template, e.g. '{{.Summary.Total}} {{len .Attention}}'.
      --infra-only             Only show attention rows with failed gates classified as infra.
      --interval duration      Refresh interval for --watch. (default 2s)
      --json                   Emit triage snapshot as JSON.
      --min-severity string    Only show attention rows at least this severe: critical, warning, or info.
      --no-clear               With --watch, append snapshots instead of redrawing the terminal.
      --reason strings         Only show attention rows with this reason. Can repeat or comma-separate.
      --repo string            Repo root containing .agent_team. (default "<repo>")
      --stale-after duration   Flag queued or running jobs with no update after this duration (default: [health].job_stale_after or 24h; 0 disables stale checks). (default 24h0m0s)
  -w, --watch                  Refresh the triage view until interrupted.
```

## `agent-team job unblock`

Answer a blocked job and mark it ready to continue.

Send an answer to a blocked job&#39;s owning instance, then mark the durable job running or queued. Use this when a worker reported blocked and the operator has supplied the missing input.

```text
agent-team job unblock <job-id> [message...] [flags]
```

Flags:

```text
      --allow-missing         Allow queueing a message for an owning instance the daemon does not know yet.
      --commands              With --dry-run, print the matching job unblock apply command when the preview has actionable work.
      --dry-run               Preview the unblock without sending a mailbox message or updating the job.
  -f, --force                 Allow unblocking a job not currently marked blocked.
      --format string         Render the updated job or dry-run preview with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --from string           Sender label recorded with the unblock message. (default "(cli)")
      --json                  Emit the updated job or dry-run preview as JSON.
      --message string        Message text to send.
      --message-file string   Read message text from a file, or '-' for stdin.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --status string         Status after unblocking: running or queued. (default "running")
      --step string           Use this pipeline step's owning instance.
```

## `agent-team job update`

Update job metadata.

Update durable job metadata such as status, owner instance, branch, worktree, PR URL, and landing mode.

```text
agent-team job update <job-id> [flags]
```

Flags:

```text
      --advance                   After updating metadata, dispatch the next ready pipeline step.
      --branch string             Set branch.
      --clear strings             Clear metadata fields: ticket-url, instance, branch, worktree, pr, pipeline, or land. Can repeat or comma-separate.
      --commands                  With --dry-run, print the matching job update apply command when the preview has actionable work.
      --dry-run                   Preview metadata updates and optional advance dispatch without writing job or daemon state.
      --fail-on-failed            With --wait, exit 1 if the job resolves to failed.
      --format string             Render the updated job or advance result with a Go template, e.g. '{{.ID}} {{.Status}}' or '{{.Job.ID}} {{.Step.ID}}'.
      --instance string           Set owning instance.
      --json                      Emit the updated job or advance result as JSON.
      --kickoff string            Set kickoff text for future dispatches.
      --kickoff-file string       Read kickoff text from a file, or '-' for stdin.
      --land string               Set final PR landing mode: squash, merge, or rebase.
      --message string            Status message recorded on the job.
      --pr string                 Set PR URL or number.
      --repo string               Repo root containing .agent_team. (default "<repo>")
      --runtime string            Runtime profile for --advance dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for --advance dispatch. Overrides env and repo config.
      --status string             Set lifecycle status: queued, running, blocked, done, or failed.
      --target string             Set target agent.
      --ticket-url string         Set ticket URL.
      --wait                      With --advance, wait for the job to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
      --workspace string          Workspace mode for --advance: auto, worktree, or repo. (default "auto")
      --worktree string           Set worktree path.
```

## `agent-team job wait`

Wait for a job to reach a lifecycle status, event, or next step.

Wait for a durable job to reach one of the requested lifecycle statuses, last events, or pipeline next-step states. By default this waits for a terminal status: done or failed. When --event, --next-state, or --step is set without --status, any lifecycle status is accepted.

```text
agent-team job wait <job-id> [flags]
```

Flags:

```text
      --event strings        Last event to wait for, e.g. closed, adopted, or pipeline_done. Can repeat or comma-separate.
      --fail-on-failed       Exit 1 if the job resolves to failed.
      --format string        Render the final job with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --interval duration    Polling interval. (default 500ms)
      --json                 Emit the final job as JSON.
      --next-state strings   Next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
  -q, --quiet                Suppress output and use only the exit code.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --status strings       Status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --step string          Pipeline step id that must be the current next step.
      --timeout duration     Maximum time to wait (0 = no timeout).
```

## `agent-team kill`

Force-stop running instances.

Docker-like forced stop. Sends the daemon stop request with force escalation enabled. With no args, targets running declared persistent instances; use --all for every daemon-managed running instance.

```text
agent-team kill [<instance>...] [flags]
```

Flags:

```text
      --agent strings           Force-stop every running instance for this agent. Can repeat or comma-separate.
  -a, --all                     Force-stop every daemon-managed running instance.
      --commands                With --dry-run, print the matching apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run                 Preview planned kill actions without changing daemon state.
      --format string           Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                    Emit machine-readable JSON.
  -n, --last int                Force-stop the N most recently started running instances after other filters (0 = all).
      --latest                  Force-stop the most recently started running instance after other filters.
      --phase strings           Force-stop daemon-known instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
  -q, --quiet                   Suppress non-error output and use only the exit code.
      --rm                      Remove selected instance state and daemon metadata after killing.
      --runtime strings         Only force-stop running daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale           Only force-stop running instances whose recorded runtime PID is no longer live.
      --stale                   Only force-stop instances whose status.toml is stale.
      --status strings          Force-stop daemon-known instances currently in this lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary                 Show aggregate action counts instead of per-instance rows.
      --target string           Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --timeout duration        Grace before SIGKILL escalation. (default 2s)
      --unhealthy               Only force-stop instances that are crashed, status-stale, or runtime-stale.
      --wait                    Wait for killed instances to reach a terminal state.
      --wait-timeout duration   Maximum time to wait for terminal state with --wait. Defaults to --timeout when unset; set 0 explicitly for no wait timeout.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team locks`

Inspect declared dispatch lock utilization.

Inspect named dispatch locks declared in .agent_team/instances.toml and their active daemon ledger holders.

```text
agent-team locks [flags]
```

Aliases: `lock`

Flags:

```text
      --format string   Render each lock with a Go template, e.g. '{{.Name}} {{.Used}}/{{.Slots}}'.
      --json            Emit lock snapshots as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team logs`

Show an instance&#39;s daemon-captured log.

```text
agent-team logs [<instance>] [flags]
```

Flags:

```text
      --agent strings     Only show logs for this agent. Can repeat or comma-separate.
  -a, --all               Show logs for every daemon-known instance, prefixed by instance name.
      --clean             Hide known Codex runtime diagnostic noise before printing logs.
      --daemon            Show the agent-teamd daemon log instead of instance logs.
  -f, --follow            Tail the log; print new bytes as they appear.
      --format string     With --list, render each log stream with a Go template, e.g. '{{.Instance}} {{.LogPath}}'.
      --grep string       Only print log lines matching this regular expression. One-shot reads only.
      --job strings       Only show logs for this job id or ticket. Can repeat or comma-separate.
      --json              Emit machine-readable JSON with --list.
  -n, --last int          Show logs for the N most recently started instances after other filters (0 = all).
      --last-message      Show the clean final Codex response sidecar instead of the raw runtime log.
      --latest            Show logs for the most recently started instance after other filters.
      --list              List daemon-known instance log streams instead of printing log content.
      --no-prefix         Do not prefix lines when streaming multiple instance logs.
      --phase strings     Only show logs for instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --raw               Print the unprocessed runtime log stream without Codex JSONL rendering.
      --runtime strings   Only show logs for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale     Only show logs for running instances whose recorded runtime PID is no longer live.
      --since string      Only include log streams modified since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --stale             Only show logs for instances whose status.toml is stale.
      --status strings    Only show logs for lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --tail string       Show only the last N lines before returning or following (0 or all = all). (default "0")
      --target string     Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --unhealthy         Only show logs for crashed, status-stale, or runtime-stale instances.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team monitor`

Show a combined health, recovery, inbox, instance, and resource snapshot.

Show a Docker-style operator snapshot combining fleet health, inbox state, job, queue, and outbox recovery signals, the instance table, and daemon-managed process stats. With --watch, refresh until interrupted.

```text
agent-team monitor [flags]
```

Flags:

```text
      --action strings         With --plan, only show plan rows with this action: start, resume, keep, unsupported, on-demand, stop, or extra. Can repeat or comma-separate.
      --agent strings          Only show instances, stats, and plan rows for this agent. Can repeat or comma-separate.
  -a, --all                    Include stopped, exited, and crashed daemon-managed instances in the stats section.
      --commands               Print recovery and apply commands from the visible monitor sections, one per line. agent-team follow-ups preserve the selected repo scope.
      --event-action strings   With --events, only show lifecycle events with this action. Can repeat or comma-separate.
      --events int             Include the last N matching daemon lifecycle events in the full monitor (0 = omit).
      --events-sort string     Sort the visible --events section by oldest or newest. (default "oldest")
      --fallbacks              When runtime recovery actions use resume-plan, recommend command-mode fallback expansion.
      --format string          Render monitor snapshots with a Go template, e.g. '{{.Health.Healthy}} {{len .Instances}}'.
      --instance strings       Only show instances, stats, and plan rows with this name. Can repeat or comma-separate.
      --interval duration      Refresh interval for --watch. (default 2s)
      --jobs                   Include durable job summary, attention, ready-step state, and status-file previews.
      --json                   Emit JSON. With --watch, writes one JSON object per refresh.
  -n, --last int               Show only the N most recently started instances after other filters (0 = all).
      --last-message           When runtime recovery actions use resume-plan log fallbacks, prefer clean Codex final-message commands.
      --latest                 Show only the most recently started instance after other filters.
      --no-clear               With --watch, append snapshots instead of redrawing the terminal.
      --phase strings          Only show instances and stats in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --plan                   Include desired-state actions from instances.toml and daemon metadata.
      --resources              With --summary, include aggregate CPU, memory, and RSS totals.
      --runtime strings        Only show instances and stats for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale          Only show running instances whose recorded runtime PID is no longer live.
      --schedules              Include due and upcoming declared schedule state.
      --since string           With --events, only show lifecycle events since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --sort string            Sort instance rows by name, status, agent, phase, stale, runtime-stale, unhealthy, started, stopped, or exited. (default "name")
      --stale                  Only show instances whose status.toml is stale.
      --stats-sort string      Sort stats rows by name, cpu, mem, rss, status, agent, phase, stale, runtime-stale, or unhealthy. (default "name")
      --status strings         Only show lifecycle status in instance, stats, and plan sections: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --stop-extras            With --plan, preview running topology extras as stop actions.
      --strict-topology        Treat running daemon-known instances not declared in instances.toml as unhealthy.
      --summary                Show compact non-failing fleet health and optional plan summaries instead of the full monitor.
      --target string          Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --unhealthy              Only show crashed, status-stale, or runtime-stale instances.
  -w, --watch                  Refresh the monitor snapshot until interrupted.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team next`

Print recommended next operator actions.

Print recommended next operator actions from the read-only overview. Use --team to scope recommendations to one declared team.

```text
agent-team next [flags]
```

Flags:

```text
      --commands             Print only recommended commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --details              Include source and reason metadata in text output.
      --fallbacks            When runtime recovery actions use resume-plan, recommend command-mode fallback expansion.
      --format string        Render the next-action result with a Go template, e.g. '{{.State}} {{len .Actions}}'.
      --interval duration    Refresh interval for --watch. (default 2s)
      --json                 Emit recommended actions as JSON.
      --last-message         When runtime recovery actions use resume-plan log fallbacks, prefer clean Codex final-message commands.
      --limit int            Show at most this many actions; 0 means all.
      --no-clear             With --watch, append snapshots instead of redrawing the terminal.
      --reason strings       Only show actions with this reason. Values match exactly, or as prefixes before '='. Queue/job/outbox quarantine aliases are supported. Can repeat or comma-separate.
      --schedule-limit int   Upcoming schedules to inspect while building recommendations; 0 means all. (default 5)
      --sort string          Sort actions before applying --limit by default, source, reason, or command. (default "default")
      --source strings       Only show actions from this source: health, topology, runtime, inbox, outbox, queue, jobs, pipelines, schedules, intake, section_errors, or overview. Can repeat or comma-separate.
      --target string        Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --team string          Scope recommendations to this declared team.
  -w, --watch                Refresh recommended actions until interrupted.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team outbox`

Inspect and control sandboxed agent outbox events.

Inspect and control sandboxed agent outbox events under `.agent_team/outbox/`.

Agents write outbox events when daemon socket or loopback HTTP transport is unavailable. `agent-team tick`, `agent-team drain`, and `agent-team outbox drain` publish pending events through the daemon resolver.

```text
agent-team outbox
```

Aliases: `outboxes`

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team outbox doctor` - Validate sandboxed agent outbox files.
- `agent-team outbox drain` - Ask the running daemon to publish pending outbox events.
- `agent-team outbox drop` - Drop one or more outbox events.
- `agent-team outbox ls` - List sandboxed agent outbox events.
- `agent-team outbox prune` - Prune old sandboxed agent outbox events.
- `agent-team outbox quarantine` - Inspect, restore, and drop quarantined outbox files.
- `agent-team outbox retry` - Retry one or more processed or failed outbox events.
- `agent-team outbox show` - Show one outbox event.

## `agent-team outbox doctor`

Validate sandboxed agent outbox files.

Validate sandboxed agent outbox JSON files without relying on normal outbox listing paths.

```text
agent-team outbox doctor [flags]
```

Flags:

```text
      --commands        Print recommended follow-up commands, or with --quarantine --dry-run print the matching quarantine apply command.
      --dry-run         With --quarantine, preview files that would be moved.
      --format string   Render the outbox doctor result with a Go template, e.g. '{{.OK}} {{.Summary.Invalid}}'.
      --json            Emit outbox doctor findings as JSON.
      --quarantine      Move outbox files with doctor problems out of the active outbox.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team outbox drain`

Ask the running daemon to publish pending outbox events.

```text
agent-team outbox drain [flags]
```

Flags:

```text
      --commands        With --dry-run, print the matching drain command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run         Preview pending outbox events without publishing them.
      --format string   Render the drain result with a Go template, e.g. '{{.WouldPublish}} {{.Pending}}'.
      --json            Emit machine-readable JSON.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team outbox drop`

Drop one or more outbox events.

Remove one outbox event by id, or drop a filtered batch with --all. Batch drops default to failed events.

```text
agent-team outbox drop [id] [flags]
```

Flags:

```text
      --all              Drop all matching outbox events instead of one id.
      --commands         With --dry-run, print the matching drop apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run          Preview the drop without removing the event.
      --format string    Render the drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings      With --all, filter by job id or ticket; repeat or comma-separate values.
      --json             Emit machine-readable JSON.
      --limit int        With --all, drop at most this many matching outbox events; 0 means no limit.
      --sort string      With --all, sort matching outbox events before limiting: state, id, type, source, job, created, updated, or error. (default "state")
      --source strings   With --all, filter by source agent/instance; repeat or comma-separate values.
      --state string     With --all, filter by outbox state: pending, processed, or failed. Defaults to failed.
      --target string    Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --type strings     With --all, filter by event type; repeat or comma-separate values.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team outbox ls`

List sandboxed agent outbox events.

```text
agent-team outbox ls [flags]
```

Aliases: `watch`

Flags:

```text
      --commands            Print recommended commands from the visible outbox rows, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string       Render each outbox item with a Go template, e.g. '{{.ID}} {{.State}}'.
      --interval duration   Refresh interval for --watch. (default 2s)
      --job strings         Filter by job id or ticket; repeat or comma-separate values.
      --json                Emit machine-readable JSON.
      --limit int           Limit rows after filtering and sorting; 0 means no limit.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --sort string         Sort rows by state, id, type, source, job, created, updated, or error. (default "state")
      --source strings      Filter by source agent/instance; repeat or comma-separate values.
      --state string        Filter by outbox state: pending, processed, or failed.
      --summary             Show aggregate outbox counts instead of rows.
      --target string       Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --type strings        Filter by event type; repeat or comma-separate values.
  -w, --watch               Refresh the outbox table until interrupted.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team outbox prune`

Prune old sandboxed agent outbox events.

Prune old sandboxed agent outbox events. By default this removes processed events; pass --state failed, pending, or all for explicit cleanup.

```text
agent-team outbox prune [flags]
```

Flags:

```text
      --commands              With --dry-run, print the matching prune apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run               Preview outbox events that would be pruned without dropping them.
      --format string         Render each prune result with a Go template, e.g. '{{.ID}} {{.Dropped}}'.
      --job strings           Filter by job id or ticket before pruning; repeat or comma-separate values.
      --json                  Emit prune results as JSON.
      --limit int             Prune at most this many matching outbox events; 0 means no limit.
      --older-than duration   Only prune items older than this duration based on processed/failed/update/create time.
      --source strings        Filter by source agent/instance before pruning; repeat or comma-separate values.
      --state string          Outbox state to prune: processed, failed, pending, or all. (default "processed")
      --target string         Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --type strings          Filter by event type before pruning; repeat or comma-separate values.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team outbox quarantine`

Inspect, restore, and drop quarantined outbox files.

Inspect outbox files moved under `.agent_team/outbox/quarantine/`, restore validated entries to the active outbox, or explicitly drop preserved files.

```text
agent-team outbox quarantine
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team outbox quarantine drop` - Drop quarantined outbox files after inspection.
- `agent-team outbox quarantine ls` - List quarantined outbox files.
- `agent-team outbox quarantine restore` - Restore validated quarantined outbox files.
- `agent-team outbox quarantine show` - Show one quarantined outbox file.

## `agent-team outbox quarantine drop`

Drop quarantined outbox files after inspection.

Drop one quarantined outbox file by path, or drop a filtered batch with --all.

```text
agent-team outbox quarantine drop [quarantine-path] [flags]
```

Flags:

```text
      --all                   Drop all matching quarantined files instead of one path.
      --commands              With --dry-run, print the matching drop apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run               Preview quarantined files that would be dropped.
      --format string         Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings           With --all, filter by job id or ticket; repeat or comma-separate values.
      --json                  Emit drop results as JSON.
      --limit int             With --all, drop at most this many matching quarantined files; 0 means no limit.
      --older-than duration   With --all, only drop files older than this duration based on file mtime.
      --restorable            With --all, only drop quarantined files that can be restored.
      --sort string           With --all, sort matching quarantined files before limiting: path, state, id, type, source, job, created, updated, modified, restorable, or size. (default "path")
      --source strings        With --all, filter by source agent/instance; repeat or comma-separate values.
      --state string          With --all, filter by outbox state: pending, processed, or failed.
      --target string         Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --type strings          With --all, filter by event type; repeat or comma-separate values.
      --unrestorable          With --all, only drop quarantined files that cannot be restored.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team outbox quarantine ls`

List quarantined outbox files.

```text
agent-team outbox quarantine ls [flags]
```

Flags:

```text
      --commands         Print recommended commands from the visible quarantined outbox files, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string    Render each quarantined outbox file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.
      --job strings      Filter by job id or ticket; repeat or comma-separate values.
      --json             Emit quarantined outbox files as JSON.
      --limit int        Limit rows after filtering and sorting; 0 means no limit.
      --restorable       Only show quarantined files that can be restored.
      --sort string      Sort rows by path, state, id, type, source, job, created, updated, modified, restorable, or size. (default "path")
      --source strings   Filter by source agent/instance; repeat or comma-separate values.
      --state string     Filter by outbox state: pending, processed, or failed.
      --summary          Show aggregate quarantined outbox-file counts instead of rows.
      --target string    Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --type strings     Filter by event type; repeat or comma-separate values.
      --unrestorable     Only show quarantined files that cannot be restored.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team outbox quarantine restore`

Restore validated quarantined outbox files.

Restore one validated quarantined outbox file by path, or restore a filtered batch of restorable files with --all.

```text
agent-team outbox quarantine restore [quarantine-path] [flags]
```

Flags:

```text
      --all              Restore all matching restorable quarantined files instead of one path.
      --commands         With --dry-run, print the matching restore apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run          Preview the restore without moving files.
      --force            Overwrite an existing active outbox file with the same restore path.
      --format string    Render each restore result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings      With --all, filter by job id or ticket; repeat or comma-separate values.
      --json             Emit restore result as JSON.
      --limit int        With --all, restore at most this many matching quarantined files; 0 means no limit.
      --sort string      With --all, sort matching quarantined files before limiting: path, state, id, type, source, job, created, updated, modified, restorable, or size. (default "path")
      --source strings   With --all, filter by source agent/instance; repeat or comma-separate values.
      --state string     With --all, filter by outbox state: pending, processed, or failed.
      --target string    Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --type strings     With --all, filter by event type; repeat or comma-separate values.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team outbox quarantine show`

Show one quarantined outbox file.

```text
agent-team outbox quarantine show <quarantine-path> [flags]
```

Flags:

```text
      --commands        Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.
      --format string   Render the quarantined outbox file with a Go template, e.g. '{{.ID}} {{.State}}'.
      --json            Emit the quarantined outbox file as JSON.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team outbox retry`

Retry one or more processed or failed outbox events.

Move one processed or failed outbox event back to pending by id, or retry a filtered batch with --all. Batch retries default to failed events.

```text
agent-team outbox retry [id] [flags]
```

Aliases: `requeue`

Flags:

```text
      --all              Retry all matching outbox events instead of one id.
      --commands         With --dry-run, print the matching retry apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run          Preview the retry without moving the event.
      --format string    Render the retry result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings      With --all, filter by job id or ticket; repeat or comma-separate values.
      --json             Emit machine-readable JSON.
      --limit int        With --all, retry at most this many matching outbox events; 0 means no limit.
      --sort string      With --all, sort matching outbox events before limiting: state, id, type, source, job, created, updated, or error. (default "state")
      --source strings   With --all, filter by source agent/instance; repeat or comma-separate values.
      --state string     With --all, filter by outbox state: pending, processed, or failed. Defaults to failed.
      --target string    Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --type strings     With --all, filter by event type; repeat or comma-separate values.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team outbox show`

Show one outbox event.

```text
agent-team outbox show <id> [flags]
```

Flags:

```text
      --commands        Print recommended follow-up commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string   Render the outbox item with a Go template, e.g. '{{.ID}} {{.State}}'.
      --json            Emit the outbox item as JSON.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team overview`

Show a concise operator overview across health, jobs, queue, pipelines, and schedules.

Show a read-only operator overview with health, topology, job, queue, pipeline, schedule, and recommended next-action summaries.

```text
agent-team overview [flags]
```

Flags:

```text
      --commands             Print recommended actions, one per line. agent-team follow-ups preserve the selected repo scope.
      --fallbacks            When runtime recovery actions use resume-plan, recommend command-mode fallback expansion.
      --format string        Render the overview result with a Go template, e.g. '{{.State}} {{len .Actions}}'.
      --interval duration    Refresh interval for --watch. (default 2s)
      --json                 Emit overview as JSON.
      --last-message         When runtime recovery actions use resume-plan log fallbacks, prefer clean Codex final-message commands.
      --limit int            Show at most this many action recommendations after filtering and sorting; 0 means all.
      --no-clear             With --watch, append snapshots instead of redrawing the terminal.
      --reason strings       Only keep action recommendations with this reason. Values match exactly, or as prefixes before '='. Queue/job/outbox quarantine aliases are supported. Can repeat or comma-separate.
      --schedule-limit int   Upcoming schedules to inspect after ordering; 0 means all. (default 5)
      --sort string          Sort action recommendations before applying --limit by default, source, reason, or command. (default "default")
      --source strings       Only keep action recommendations from this source: health, topology, runtime, inbox, outbox, queue, jobs, pipelines, schedules, intake, section_errors, or overview. Can repeat or comma-separate.
      --target string        Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
  -w, --watch                Refresh overview until interrupted.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team pipeline`

Inspect declared pipeline workflows.

Inspect pipeline declarations loaded from .agent_team/instances.toml.

```text
agent-team pipeline
```

Aliases: `pipelines`

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team pipeline adopt` - Adopt a live external process for a pipeline-owned job.
- `agent-team pipeline advance` - Dispatch ready pipeline steps.
- `agent-team pipeline approve` - Approve blocked manual pipeline gates.
- `agent-team pipeline cancel` - Cancel non-terminal pipeline jobs.
- `agent-team pipeline cleanup` - Clean up done jobs owned by one pipeline.
- `agent-team pipeline doctor` - Validate pipeline workflow wiring.
- `agent-team pipeline drain` - Run one pipeline&#39;s maintenance loop until idle.
- `agent-team pipeline events` - Show lifecycle events scoped to pipeline-owned jobs.
- `agent-team pipeline explain` - Explain pipeline jobs and step blockers.
- `agent-team pipeline graph` - Render a declared pipeline step graph.
- `agent-team pipeline hold` - Hold pipeline jobs so automation will not advance them.
- `agent-team pipeline job-events` - Show durable job events for pipeline-owned jobs.
- `agent-team pipeline jobs` - List pipeline jobs.
- `agent-team pipeline logs` - Show daemon-captured logs for pipeline-owned jobs.
- `agent-team pipeline ls` - List declared pipelines.
- `agent-team pipeline next` - Print recommended next actions for pipeline jobs.
- `agent-team pipeline outbox` - List or control pipeline-owned outbox events.
- `agent-team pipeline ps` - List pipeline-owned instances.
- `agent-team pipeline queue` - List or control pipeline-owned queue items.
- `agent-team pipeline ready` - List ready pipeline jobs.
- `agent-team pipeline reject` - Reject blocked manual pipeline gates.
- `agent-team pipeline release` - Release held pipeline jobs so automation can advance them.
- `agent-team pipeline repair` - Recover unhealthy orchestration state for one pipeline.
- `agent-team pipeline resume-plan` - Show runtime resume and fallback commands for pipeline-owned jobs.
- `agent-team pipeline retry` - Reset failed pipeline steps for another attempt.
- `agent-team pipeline run` - Create a durable job from a pipeline declaration.
- `agent-team pipeline runtime` - Inspect pipeline-owned runtime metadata.
- `agent-team pipeline send` - Send a mailbox message to pipeline-owned instances.
- `agent-team pipeline show` - Show one declared pipeline.
- `agent-team pipeline skip` - Mark matching pipeline steps intentionally skipped.
- `agent-team pipeline snapshot` - Capture a read-only diagnostic snapshot for one pipeline.
- `agent-team pipeline stats` - Show CPU and memory usage for pipeline-owned instances.
- `agent-team pipeline status` - Summarize pipeline jobs and next steps.
- `agent-team pipeline tick` - Run one pipeline&#39;s orchestration maintenance work.
- `agent-team pipeline timeline` - Show combined job audit and lifecycle timelines for pipeline-owned jobs.
- `agent-team pipeline timeout` - Mark stale running pipeline steps failed.
- `agent-team pipeline triage` - Show pipeline-owned jobs that need operator attention.
- `agent-team pipeline unblock` - Answer blocked pipeline workers.
- `agent-team pipeline wait` - Wait for pipeline jobs to reach a lifecycle status, event, or next step.

## `agent-team pipeline adopt`

Adopt a live external process for a pipeline-owned job.

Adopt a live external process into daemon metadata and sync the durable job ownership fields, after verifying the job belongs to the named pipeline.

```text
agent-team pipeline adopt <pipeline> <job-id> [flags]
```

Flags:

```text
      --agent string         Agent name for the adopted instance. Defaults to the selected step target or job target.
      --branch string        Branch name to record. Defaults to the job branch.
      --commands             Print only follow-up commands, one per line, after adoption planning or apply.
      --dry-run              Preview adoption without writing metadata or job state.
      --force                Replace existing live metadata for the instance.
      --format string        Render the adoption result with a Go template, e.g. '{{.Job.ID}} {{.Metadata.Instance}}'.
      --instance string      Instance name that should own the job. Defaults to selected or active step ownership, then job ownership.
      --json                 Emit machine-readable JSON.
      --log-path string      Runtime log path, if the external process already writes to one.
      --pid int              Live process PID to adopt.
      --pid-file string      Read the live process PID to adopt from this file. Cannot be combined with --pid.
      --pr string            PR URL to record. Defaults to the job PR.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --runtime string       Runtime profile for the adopted process (claude or codex). Defaults to repo/env selection.
      --runtime-bin string   Runtime binary or wrapper used by the adopted process.
      --session-id string    Runtime session id, when known and resumable.
      --started-at string    Process start time as RFC3339. Defaults to now, or existing metadata for the same PID.
      --step string          Pipeline step id to mark as owned by the adopted process.
      --workspace string     Workspace path for the adopted process. Defaults to the job worktree, then repo root.
```

## `agent-team pipeline advance`

Dispatch ready pipeline steps.

Dispatch ready next steps for jobs in one pipeline, or across all pipelines with --all, using the same path as `agent-team job advance`.

```text
agent-team pipeline advance <pipeline>|--all [flags]
```

Flags:

```text
      --all                       Advance ready steps across all pipelines.
      --all-ready-steps           Advance every currently ready independent step for each selected job.
      --commands                  With --dry-run, print the matching advance apply command when the preview has actionable work.
      --dry-run                   Preview ready steps without dispatching them.
      --fail-on-failed            With --wait, exit 1 if any advanced job resolves to failed.
      --format string             Render each result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.
      --json                      Emit advance results as JSON.
      --limit int                 Advance at most this many ready jobs, or ready steps with --all-ready-steps; 0 means no limit.
      --preview-routes            With --dry-run, include local topology route and dispatch payload previews.
      --repo string               Repo root containing .agent_team. (default "<repo>")
      --runtime string            Runtime profile for advanced step dispatches (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for advanced step dispatches. Overrides env and repo config.
      --wait                      After advancing, wait for advanced jobs to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step for every advanced job.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
      --workspace string          Workspace mode for advanced steps: auto, worktree, or repo. (default "auto")
```

## `agent-team pipeline approve`

Approve blocked manual pipeline gates.

Approve blocked manual-gate steps for jobs in one pipeline, or all pipelines with --all. By default this marks matching manual gates queued; pass --step to target one stage, or --dispatch to immediately dispatch each approved step.

```text
agent-team pipeline approve <pipeline>|--all [flags]
```

Flags:

```text
      --all                       Approve manual gates across all pipelines.
      --commands                  With --dry-run, print the matching approve apply command when the preview has actionable work.
      --dispatch                  Dispatch each approved manual gate immediately.
      --dry-run                   Preview manual gate approvals and optional dispatches without writing job or daemon state.
      --fail-on-failed            With --wait, exit 1 if any approved job resolves to failed.
      --format string             Render each approval result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.
      --json                      Emit approval results as JSON.
      --limit int                 Maximum manual gates to approve (0 = no limit).
      --message string            Status message recorded on each approved job.
      --message-file string       Read approval message from a file, or '-' for stdin.
      --preview-routes            With --dry-run --dispatch, include route and payload previews.
      --repo string               Repo root containing .agent_team. (default "<repo>")
      --runtime string            Runtime profile for --dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for --dispatch. Overrides env and repo config.
      --step string               Approve only manual gates whose next blocked step has this id.
      --wait                      After approving or dispatching, wait for approved jobs to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step for every approved job.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
      --workspace string          Workspace mode for --dispatch: auto, worktree, or repo. (default "auto")
```

## `agent-team pipeline cancel`

Cancel non-terminal pipeline jobs.

Cancel queued, running, or blocked jobs in one pipeline, or all pipelines with --all, by marking the durable job failed with a cancelled audit event. Batch cancellation only updates job files; use job cancel --stop or --kill when an owning instance should also be stopped.

```text
agent-team pipeline cancel <pipeline>|--all [flags]
```

Flags:

```text
      --actor string          Actor label recorded in cancellation audit events. (default "cli")
      --all                   Cancel non-terminal jobs across all pipelines.
      --commands              With --dry-run, print the matching cancel apply command when the preview has actionable work.
      --dry-run               Preview cancellations without writing job state.
      --format string         Render each cancellation result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StatusAfter}}'.
      --json                  Emit cancellation results as JSON.
      --limit int             Maximum matching jobs to cancel (0 = no limit).
      --message string        Cancellation reason recorded on each cancelled job.
      --message-file string   Read cancellation reason from a file, or '-' for stdin.
      --repo string           Repo root containing .agent_team. (default "<repo>")
```

## `agent-team pipeline cleanup`

Clean up done jobs owned by one pipeline.

Preview or remove job-owned worktrees and branches for done jobs owned by one declared pipeline. Applying cleanup requires --merged after confirming the matching PRs have merged.

```text
agent-team pipeline cleanup <pipeline> [flags]
```

Flags:

```text
      --commands        With --dry-run, print the matching pipeline cleanup apply command when the preview has actionable work.
      --dry-run         Preview done pipeline-owned job cleanup without removing worktrees or branches.
      --force-branch    Delete recorded branches even when git does not consider them merged.
      --format string   Render the cleanup result with a Go template, e.g. '{{.Pipeline}} {{.Cleaned}} {{.Failed}}'.
      --json            Emit pipeline cleanup result as JSON.
      --merged          Confirm matching done pipeline jobs' PRs are merged and apply cleanup.
      --repo string     Repo root containing .agent_team. (default "<repo>")
      --verify-pr       Use gh to verify each recorded PR is merged before cleanup.
```

## `agent-team pipeline doctor`

Validate pipeline workflow wiring.

Validate declared pipeline workflow wiring: dependency graphs must be acyclic, step targets should resolve through agent.dispatch topology routes, and schedule-triggered pipelines should have a matching schedule source.

```text
agent-team pipeline doctor [<pipeline>|--all] [flags]
```

Flags:

```text
      --all              Validate all pipelines. This is the default when no pipeline is passed.
      --commands         Print recommended follow-up commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string    Render the doctor result with a Go template, e.g. '{{.OK}} {{len .Problems}}'.
      --json             Emit pipeline doctor findings as JSON.
      --repo string      Repo root containing .agent_team. (default "<repo>")
      --strict           Fail on all strict pipeline doctor checks. Currently aliases --strict-runtime.
      --strict-runtime   Fail when a step-declared or target-agent runtime default cannot be resolved or is not discoverable.
      --target string    Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

## `agent-team pipeline drain`

Run one pipeline&#39;s maintenance loop until idle.

Run scoped pipeline maintenance cycles until no immediate pipeline-owned queue or ready-step work remains. Use pipeline repair for dead-letter retry, stale-work timeout, or failed-step retry.

```text
agent-team pipeline drain <pipeline> [flags]
```

Flags:

```text
      --all-ready-steps           Advance every currently ready independent pipeline step in each drain cycle.
      --fail-on-failed            With --wait, exit 1 if any pipeline drain-advanced job resolves to failed.
      --format string             Render the pipeline drain result with a Go template, e.g. '{{.Pipeline}} {{.CyclesRun}} {{.Idle}}'.
      --interval duration         Delay between drain cycles. (default 2s)
      --json                      Emit machine-readable JSON.
      --limit int                 Advance at most this many ready pipeline jobs per cycle, or ready steps with --all-ready-steps; 0 means no limit.
      --max-cycles int            Stop after this many cycles if work keeps appearing. (default 20)
      --repo string               Repo root containing .agent_team. (default "<repo>")
      --runtime string            Runtime profile for advanced step dispatches (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for advanced step dispatches. Overrides env and repo config.
      --skip-advance              Skip pipeline advancement work.
      --skip-drain                Skip pipeline-owned queue drain work.
      --wait                      After pipeline drain reaches idle, wait for jobs advanced during drain cycles to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step for every drain-advanced job.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
      --workspace string          Workspace mode for advanced pipeline steps: auto, worktree, or repo. (default "auto")
```

## `agent-team pipeline events`

Show lifecycle events scoped to pipeline-owned jobs.

Show or follow daemon lifecycle events for daemon-known instances owned by jobs in one declared pipeline, or omit the pipeline/pass --all to inspect every pipeline-owned job.

```text
agent-team pipeline events [<pipeline>|--all] [flags]
```

Flags:

```text
      --action strings    Only show events with this action. Can repeat or comma-separate.
      --all               Show events across all pipelines. This is the default when no pipeline is passed.
  -f, --follow            Keep streaming new lifecycle events.
      --format string     Render each event with a Go template, e.g. '{{.Job}} {{.Action}} {{.Instance}} {{.Status}}'.
      --job strings       Only show events for this pipeline-owned job id or ticket. Can repeat or comma-separate.
      --json              Emit raw JSONL events.
      --phase strings     Only show pipeline events for instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --repo string       Repo root containing .agent_team. (default "<repo>")
      --runtime strings   Only show pipeline events for daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale     Only show pipeline events for instances whose recorded runtime PID is currently no longer live.
      --since string      Only show events since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --sort string       Sort returned events by oldest or newest. Follow mode always streams oldest first. (default "oldest")
      --stale             Only show pipeline events for instances whose status.toml is currently stale or missing.
      --status strings    Only show events with this lifecycle status. Can repeat or comma-separate.
      --step string       Only show events for instances recorded on this pipeline step id.
      --summary           Summarize matching pipeline events by action, status, agent, and instance.
      --tail int          Show only the last N matching pipeline events before returning or following (0 = all).
      --unhealthy         Only show pipeline events for instances that are currently crashed, status-stale, or runtime-stale.
```

## `agent-team pipeline explain`

Explain pipeline jobs and step blockers.

Explain pipeline state from durable jobs, expanding each matching job with step readiness, dependency blockers, gates, active instances, and suggested next actions.

```text
agent-team pipeline explain [<pipeline>|--all] [flags]
```

Flags:

```text
      --all                 Explain all pipelines. This is the default when no pipeline is passed.
      --commands            Print recommended commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string       Render each pipeline explanation with a Go template, e.g. '{{.Pipeline}} {{len .Jobs}}'.
      --interval duration   Refresh interval for --watch. (default 2s)
      --json                Emit pipeline explanations as JSON.
      --limit int           Limit job explanations per pipeline; 0 means no limit.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --repo string         Repo root containing .agent_team. (default "<repo>")
      --sort string         Sort job explanations before applying --limit by job, state, step, target, updated, created, ticket, instance, or label. (default "updated")
      --state strings       Only explain jobs whose next-step state matches: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --step string         Only include jobs and step details for this pipeline step id.
  -w, --watch               Refresh pipeline explanations until interrupted.
```

## `agent-team pipeline graph`

Render a declared pipeline step graph.

Render a read-only graph of one declared pipeline in text, Mermaid, DOT, or JSON form.

```text
agent-team pipeline graph <pipeline> [flags]
```

Flags:

```text
      --commands        Print recommended commands from graph action hints, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string   Graph output format: text, mermaid, or dot. (default "text")
      --job string      Overlay durable job step state on the declared pipeline graph.
      --json            Emit graph nodes and edges as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
      --routes          Annotate step targets with matching agent.dispatch route instances.
```

## `agent-team pipeline hold`

Hold pipeline jobs so automation will not advance them.

Hold jobs in one pipeline, or all pipelines with --all, without changing their lifecycle status. Held jobs report next-step state held until released.

```text
agent-team pipeline hold <pipeline>|--all [reason...] [flags]
```

Aliases: `pause`

Flags:

```text
      --all                   Hold jobs across all pipelines.
      --commands              With --dry-run, print the matching hold apply command when the preview has actionable work.
      --dry-run               Preview holds without writing job state.
      --for duration          Hold for this duration, for example 30m or 2h.
      --format string         Render each hold result with a Go template, e.g. '{{.JobID}} {{.Action}}'.
      --json                  Emit hold results as JSON.
      --limit int             Hold at most this many matching jobs; 0 means no limit.
      --message string        Hold reason recorded on each job.
      --message-file string   Read hold reason from a file, or '-' for stdin.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --state strings         Next-step state to hold: ready, queued, running, blocked, failed, held, done, none, or all. Defaults to active non-held, non-done jobs.
      --until string          Hold until this RFC3339 timestamp.
```

## `agent-team pipeline job-events`

Show durable job events for pipeline-owned jobs.

Show durable job audit events for one pipeline. With no pipeline, all pipeline-owned job events are shown.

```text
agent-team pipeline job-events [<pipeline>|--all] [flags]
```

Flags:

```text
      --actor strings       Only show job events from this actor. Can repeat or comma-separate.
      --all                 Show job events across all pipelines. This is the default when no pipeline is passed.
  -f, --follow              Poll and print new pipeline job events until interrupted.
      --format string       Render each job event with a Go template, e.g. '{{.JobID}} {{.Type}} {{.Status}}'.
      --instance strings    Only show job events for this owning instance. Can repeat or comma-separate.
      --interval duration   Polling interval for --follow. (default 1s)
      --json                Emit matching job events as JSON.
      --repo string         Repo root containing .agent_team. (default "<repo>")
      --since string        Only show job events since this duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --sort string         Sort returned events by oldest or newest. Follow mode always streams oldest first. (default "oldest")
      --status strings      Only show job events with this status: queued, running, blocked, done, or failed. Can repeat or comma-separate.
      --summary             Summarize matching job events by job, type, status, actor, and instance.
      --tail string         Show only the last N matching events after combining pipeline jobs (0 or all = all). (default "0")
      --type strings        Only show job events with this type. Can repeat or comma-separate.
```

## `agent-team pipeline jobs`

List pipeline jobs.

List durable jobs for one pipeline. With no pipeline, all pipeline-owned jobs are listed.

```text
agent-team pipeline jobs [<pipeline>|--all] [flags]
```

Flags:

```text
      --active-hold           Only show held jobs whose hold is still active or has no deadline.
      --all                   List jobs across all pipelines. This is the default when no pipeline is passed.
      --branch string         Only show jobs owning this branch.
      --commands              Print recommended follow-up commands from the visible pipeline job rows. agent-team follow-ups preserve the selected repo scope.
      --expired-hold          Only show held jobs whose hold_until has passed.
      --format string         Render each job with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --held                  Only show held jobs.
      --instance string       Only show jobs owned by this instance.
      --interval duration     Refresh interval for --watch. (default 2s)
      --json                  Emit jobs as JSON.
      --limit int             Limit rows after filtering and sorting; 0 means no limit.
      --no-clear              With --watch, append snapshots instead of redrawing the terminal.
      --pr string             Only show jobs whose PR URL contains this value.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --runtime strings       Only show jobs whose instance metadata has this runtime: claude or codex. Can repeat or comma-separate.
      --sort string           Sort jobs by id, status, target, ticket, created, updated, instance, runtime, branch, or pr. (default "id")
      --status string         Filter by job status: queued, running, blocked, done, or failed.
      --summary               Show aggregate pipeline job counts instead of job rows.
      --target-agent string   Only show jobs targeting this agent.
      --ticket string         Only show jobs whose ticket id or URL contains this value.
      --unheld                Only show jobs that are not held.
  -w, --watch                 Refresh pipeline jobs until interrupted.
```

## `agent-team pipeline logs`

Show daemon-captured logs for pipeline-owned jobs.

Show daemon-captured logs for jobs in one declared pipeline, or omit the pipeline/pass --all to inspect every pipeline-owned job.

```text
agent-team pipeline logs [<pipeline>|--all] [flags]
```

Flags:

```text
      --all               Show logs across all pipelines. This is the default when no pipeline is passed.
      --clean             Hide known Codex runtime diagnostic noise before printing pipeline logs.
  -f, --follow            Tail selected pipeline logs.
      --format string     With --list, render each log stream with a Go template, e.g. '{{.Instance}} {{.LogPath}}'.
      --grep string       Only print log lines matching this regular expression. One-shot reads only.
      --job strings       Only show logs for this pipeline-owned job id or ticket. Can repeat or comma-separate.
      --json              Emit machine-readable JSON with --list.
  -n, --last int          Show logs for the N most recently started pipeline instances (0 = all).
      --last-message      Show clean final Codex response sidecars instead of raw runtime logs.
      --latest            Show the most recently started pipeline instance log.
      --list              List pipeline log streams instead of printing log content.
      --no-prefix         Do not prefix lines when streaming multiple pipeline logs.
      --phase strings     Only show logs for work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --raw               Print unprocessed pipeline logs without Codex JSONL rendering.
      --repo string       Repo root containing .agent_team. (default "<repo>")
      --runtime strings   Only show logs for pipeline-owned instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale     Only show logs for pipeline instances whose recorded runtime PID is no longer live.
      --since string      Only include log streams modified since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --stale             Only show logs for pipeline instances whose status.toml is stale.
      --status strings    Only show logs for lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --step string       Only show logs for instances recorded on this pipeline step id.
      --tail string       Show only the last N lines before returning or following (0 or all = all). (default "0")
      --unhealthy         Only show logs for crashed, status-stale, or runtime-stale pipeline instances.
```

## `agent-team pipeline ls`

List declared pipelines.

```text
agent-team pipeline ls [flags]
```

Flags:

```text
      --format string   Render each pipeline with a Go template, e.g. '{{.Name}} {{len .Steps}}'.
      --json            Emit pipelines as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team pipeline next`

Print recommended next actions for pipeline jobs.

Print read-only recommended next actions from pipeline status rows. With no pipeline, all declared pipelines are considered.

```text
agent-team pipeline next [<pipeline>|--all] [flags]
```

Flags:

```text
      --all                 Consider all pipelines. This is the default when no pipeline is passed.
      --commands            Print only recommended commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string       Render each action with a Go template, e.g. '{{.Pipeline}} {{.Action}}'.
      --interval duration   Refresh interval for --watch. (default 2s)
      --json                Emit recommended actions as JSON.
      --limit int           Maximum number of actions to print (0 = no limit).
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --reason strings      Only show actions with this reason. Values match exactly, or as prefixes before '='. Can repeat or comma-separate.
      --repo string         Repo root containing .agent_team. (default "<repo>")
      --sort string         Sort pipelines before selecting actions by declared, pipeline, steps, jobs, queued, running, blocked, done, failed, ready, stale, manual, held, none, queue, queue-pending, queue-dead, queue-quarantined, quarantined, outbox, outbox-pending, outbox-failed, outbox-processed, or outbox-quarantined. (default "declared")
      --team string         Only consider pipelines owned by this declared team; actions are rendered with team-scoped commands.
  -w, --watch               Refresh recommended pipeline actions until interrupted.
```

## `agent-team pipeline outbox`

List or control pipeline-owned outbox events.

List sandboxed agent outbox events owned by one pipeline. With no pipeline, all pipeline-owned outbox events are listed. Outbox subcommands still require an explicit pipeline.

```text
agent-team pipeline outbox [<pipeline>|--all] [flags]
```

Flags:

```text
      --all                 List outbox events across all pipelines. This is the default when no pipeline is passed.
      --commands            Print recommended commands from the visible pipeline-owned outbox rows, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string       Render each pipeline-owned outbox item with a Go template, e.g. '{{.ID}} {{.State}}'.
      --interval duration   Refresh interval for --watch. (default 2s)
      --job strings         Filter by job id or ticket; repeat or comma-separate values.
      --json                Emit pipeline-owned outbox rows as JSON.
      --limit int           Limit rows after filtering and sorting; 0 means no limit.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --repo string         Repo root containing .agent_team. (default "<repo>")
      --sort string         Sort rows by state, id, type, source, job, created, updated, or error. (default "state")
      --source strings      Filter by source agent/instance; repeat or comma-separate values.
      --state string        Filter by outbox state: pending, processed, or failed.
      --summary             Show aggregate outbox counts instead of rows.
      --type strings        Filter by event type; repeat or comma-separate values.
  -w, --watch               Refresh the pipeline outbox table until interrupted.
```

Subcommands:

- `agent-team pipeline outbox drop` - Drop outbox events owned by one pipeline.
- `agent-team pipeline outbox prune` - Prune old outbox events owned by one pipeline.
- `agent-team pipeline outbox quarantine` - List pipeline-owned quarantined outbox files.
- `agent-team pipeline outbox retry` - Retry outbox events owned by one pipeline.
- `agent-team pipeline outbox show` - Show one outbox event owned by one pipeline.

## `agent-team pipeline outbox drop`

Drop outbox events owned by one pipeline.

Remove one pipeline-owned outbox event by id, or drop a filtered pipeline-owned batch with --all. Batch drops default to failed events.

```text
agent-team pipeline outbox drop <pipeline> [id] [flags]
```

Flags:

```text
      --all              Drop all matching pipeline-owned outbox events instead of one id.
      --commands         With --dry-run, print the matching pipeline outbox drop apply command when the preview has actionable work.
      --dry-run          Preview the drop without removing the event.
      --format string    Render the drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings      With --all, filter by job id or ticket; repeat or comma-separate values.
      --json             Emit machine-readable JSON.
      --limit int        With --all, drop at most this many matching outbox events; 0 means no limit.
      --repo string      Repo root containing .agent_team. (default "<repo>")
      --sort string      With --all, sort matching outbox events before limiting: state, id, type, source, job, created, updated, or error. (default "state")
      --source strings   With --all, filter by source agent/instance; repeat or comma-separate values.
      --state string     With --all, filter by outbox state: pending, processed, or failed. Defaults to failed.
      --type strings     With --all, filter by event type; repeat or comma-separate values.
```

## `agent-team pipeline outbox prune`

Prune old outbox events owned by one pipeline.

Prune old sandboxed agent outbox events owned by one pipeline. By default this removes processed events; pass --state failed, pending, or all for explicit cleanup.

```text
agent-team pipeline outbox prune <pipeline> [flags]
```

Flags:

```text
      --commands              With --dry-run, print the matching pipeline outbox prune apply command when the preview has actionable work.
      --dry-run               Preview pipeline-owned outbox events that would be pruned without dropping them.
      --format string         Render each prune result with a Go template, e.g. '{{.ID}} {{.Dropped}}'.
      --job strings           Filter by job id or ticket before pruning; repeat or comma-separate values.
      --json                  Emit prune results as JSON.
      --limit int             Prune at most this many matching pipeline-owned outbox events; 0 means no limit.
      --older-than duration   Only prune items older than this duration based on processed/failed/update/create time.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --source strings        Filter by source agent/instance before pruning; repeat or comma-separate values.
      --state string          Outbox state to prune: processed, failed, pending, or all. (default "processed")
      --type strings          Filter by event type before pruning; repeat or comma-separate values.
```

## `agent-team pipeline outbox quarantine`

List pipeline-owned quarantined outbox files.

List quarantined outbox files owned by one pipeline. With no pipeline, all pipeline-owned quarantined outbox files are listed. Show, restore, and drop still require an explicit pipeline.

```text
agent-team pipeline outbox quarantine [<pipeline>|--all] [flags]
```

Flags:

```text
      --all              List quarantined outbox files across all pipelines. This is the default when no pipeline is passed.
      --commands         Print recommended commands from the visible pipeline-owned quarantined outbox files, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string    Render each pipeline-owned quarantined outbox file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.
      --job strings      Filter by job id or ticket; repeat or comma-separate values.
      --json             Emit pipeline-owned quarantined outbox files as JSON.
      --limit int        Limit rows after filtering and sorting; 0 means no limit.
      --repo string      Repo root containing .agent_team. (default "<repo>")
      --restorable       Only show quarantined files that can be restored.
      --sort string      Sort rows by path, state, id, type, source, job, created, updated, modified, restorable, or size. (default "path")
      --source strings   Filter by source agent/instance; repeat or comma-separate values.
      --state string     Filter by outbox state: pending, processed, or failed.
      --summary          Show aggregate pipeline-owned quarantined outbox-file counts instead of rows.
      --type strings     Filter by event type; repeat or comma-separate values.
      --unrestorable     Only show quarantined files that cannot be restored.
```

Subcommands:

- `agent-team pipeline outbox quarantine drop` - Drop pipeline-owned quarantined outbox files after inspection.
- `agent-team pipeline outbox quarantine restore` - Restore pipeline-owned quarantined outbox files.
- `agent-team pipeline outbox quarantine show` - Show one pipeline-owned quarantined outbox file.

## `agent-team pipeline outbox quarantine drop`

Drop pipeline-owned quarantined outbox files after inspection.

Drop one pipeline-owned quarantined outbox file by path, or drop a filtered pipeline-owned batch with --all.

```text
agent-team pipeline outbox quarantine drop <pipeline> [quarantine-path] [flags]
```

Flags:

```text
      --all                   Drop all matching pipeline-owned quarantined files instead of one path.
      --commands              With --dry-run, print the matching pipeline outbox quarantine drop apply command when the preview has actionable work.
      --dry-run               Preview quarantined files that would be dropped.
      --format string         Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings           With --all, filter by job id or ticket; repeat or comma-separate values.
      --json                  Emit drop results as JSON.
      --limit int             With --all, drop at most this many matching pipeline-owned quarantined files; 0 means no limit.
      --older-than duration   With --all, only drop files older than this duration based on file mtime.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --restorable            With --all, only drop quarantined files that can be restored.
      --sort string           With --all, sort matching pipeline-owned quarantined files before limiting: path, state, id, type, source, job, created, updated, modified, restorable, or size. (default "path")
      --source strings        With --all, filter by source agent/instance; repeat or comma-separate values.
      --state string          With --all, filter by outbox state: pending, processed, or failed.
      --type strings          With --all, filter by event type; repeat or comma-separate values.
      --unrestorable          With --all, only drop quarantined files that cannot be restored.
```

## `agent-team pipeline outbox quarantine restore`

Restore pipeline-owned quarantined outbox files.

Restore one pipeline-owned quarantined outbox file by path, or restore a filtered pipeline-owned batch of restorable files with --all.

```text
agent-team pipeline outbox quarantine restore <pipeline> [quarantine-path] [flags]
```

Flags:

```text
      --all              Restore all matching pipeline-owned restorable quarantined files instead of one path.
      --commands         With --dry-run, print the matching pipeline outbox quarantine restore apply command when the preview has actionable work.
      --dry-run          Preview the restore without moving files.
      --force            Overwrite an existing active outbox file with the same restore path.
      --format string    Render each restore result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings      With --all, filter by job id or ticket; repeat or comma-separate values.
      --json             Emit restore result as JSON.
      --limit int        With --all, restore at most this many matching pipeline-owned quarantined files; 0 means no limit.
      --repo string      Repo root containing .agent_team. (default "<repo>")
      --sort string      With --all, sort matching pipeline-owned quarantined files before limiting: path, state, id, type, source, job, created, updated, modified, restorable, or size. (default "path")
      --source strings   With --all, filter by source agent/instance; repeat or comma-separate values.
      --state string     With --all, filter by outbox state: pending, processed, or failed.
      --type strings     With --all, filter by event type; repeat or comma-separate values.
```

## `agent-team pipeline outbox quarantine show`

Show one pipeline-owned quarantined outbox file.

```text
agent-team pipeline outbox quarantine show <pipeline> <quarantine-path> [flags]
```

Flags:

```text
      --commands        Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.
      --format string   Render the pipeline-owned quarantined outbox file with a Go template, e.g. '{{.ID}} {{.State}}'.
      --json            Emit the pipeline-owned quarantined outbox file as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team pipeline outbox retry`

Retry outbox events owned by one pipeline.

Move one pipeline-owned processed or failed outbox event back to pending by id, or retry a filtered pipeline-owned batch with --all. Batch retries default to failed events.

```text
agent-team pipeline outbox retry <pipeline> [id] [flags]
```

Aliases: `requeue`

Flags:

```text
      --all              Retry all matching pipeline-owned outbox events instead of one id.
      --commands         With --dry-run, print the matching pipeline outbox retry apply command when the preview has actionable work.
      --dry-run          Preview the retry without moving the event.
      --format string    Render the retry result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings      With --all, filter by job id or ticket; repeat or comma-separate values.
      --json             Emit machine-readable JSON.
      --limit int        With --all, retry at most this many matching outbox events; 0 means no limit.
      --repo string      Repo root containing .agent_team. (default "<repo>")
      --sort string      With --all, sort matching outbox events before limiting: state, id, type, source, job, created, updated, or error. (default "state")
      --source strings   With --all, filter by source agent/instance; repeat or comma-separate values.
      --state string     With --all, filter by outbox state: pending, processed, or failed. Defaults to failed.
      --type strings     With --all, filter by event type; repeat or comma-separate values.
```

## `agent-team pipeline outbox show`

Show one outbox event owned by one pipeline.

```text
agent-team pipeline outbox show <pipeline> <id> [flags]
```

Flags:

```text
      --commands        Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.
      --format string   Render the pipeline-owned outbox item with a Go template, e.g. '{{.ID}} {{.State}}'.
      --json            Emit the pipeline-owned outbox item as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team pipeline ps`

List pipeline-owned instances.

List daemon-aware instance rows owned by jobs in one declared pipeline. Omit the pipeline or pass --all to inspect every pipeline-owned job while excluding ad hoc instances.

```text
agent-team pipeline ps [<pipeline>|--all] [flags]
```

Flags:

```text
      --agent strings       Only show pipeline-owned instances for this agent. Can repeat or comma-separate.
      --all                 List instances across all pipelines. This is the default when no pipeline is passed.
      --format string       Render each row with a Go template, e.g. '{{.Instance}} {{.Status}}'.
      --interval duration   Refresh interval for --watch. (default 2s)
      --json                Emit JSON. With --watch, writes one JSON array per refresh.
  -n, --last int            Show only the N most recently started pipeline-owned instances after other filters (0 = all).
  -l, --latest              Show only the most recently started pipeline-owned instance after other filters.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --phase strings       Only show pipeline-owned work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
  -q, --quiet               Only print matching pipeline-owned instance names.
      --repo string         Repo root containing .agent_team. (default "<repo>")
      --runtime strings     Only show pipeline-owned instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale       Only show pipeline-owned running instances whose recorded runtime PID is no longer live.
      --sort string         Sort rows by name, status, agent, phase, stale, runtime-stale, unhealthy, started, stopped, or exited. (default "name")
      --stale               Only show pipeline-owned instances whose status.toml is stale.
      --status strings      Only show pipeline-owned lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary             Show lifecycle counts instead of pipeline instance rows.
      --unhealthy           Only show crashed, status-stale, or runtime-stale pipeline-owned instances.
  -w, --watch               Refresh pipeline instance rows until interrupted.
```

## `agent-team pipeline queue`

List or control pipeline-owned queue items.

List active queue items owned by one pipeline. With no pipeline, all pipeline-owned queue items are listed. Queue subcommands still require an explicit pipeline.

```text
agent-team pipeline queue [<pipeline>|--all] [flags]
```

Flags:

```text
      --all                  List queue items across all pipelines. This is the default when no pipeline is passed.
      --commands             Print recommended commands from the visible pipeline queue rows, one per line. agent-team follow-ups preserve the selected repo scope.
      --event-type strings   Filter by event type; repeat or comma-separate values.
      --format string        Render each queue item with a Go template, e.g. '{{.ID}} {{.State}}'.
      --interval duration    Refresh interval for --watch. (default 2s)
      --job strings          Filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit pipeline queue rows as JSON.
      --limit int            Limit rows after filtering and sorting; 0 means no limit.
      --no-clear             With --watch, append snapshots instead of redrawing the terminal.
      --ready                Only show pending queue items whose next retry is due now.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --runtime strings      Filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.
      --sort string          Sort rows by state, id, event, instance, job, runtime, queued, updated, next-retry, or attempts. (default "state")
      --state string         Filter by queue state: pending or dead.
      --summary              Show aggregate queue counts instead of queue rows.
  -w, --watch                Refresh the pipeline queue table until interrupted.
```

Subcommands:

- `agent-team pipeline queue drop` - Drop pipeline-owned queue items.
- `agent-team pipeline queue prune` - Prune pipeline-owned queue items.
- `agent-team pipeline queue quarantine` - List pipeline-owned quarantined queue files.
- `agent-team pipeline queue retry` - Retry pipeline-owned queue items.
- `agent-team pipeline queue show` - Show one queue item owned by one pipeline.

## `agent-team pipeline queue drop`

Drop pipeline-owned queue items.

Drop one pipeline-owned queue item by id, or drop a filtered pipeline-owned batch with --all. Batch drops default to dead-letter items.

```text
agent-team pipeline queue drop <pipeline> [id] [flags]
```

Flags:

```text
      --all                  Drop all matching pipeline-owned queue items instead of one id.
      --commands             With --dry-run, print the matching pipeline queue drop command when the preview has actionable work.
      --dry-run              Preview matching pipeline-owned queue items without dropping them.
      --event-type strings   With --all, filter by event type; repeat or comma-separate values.
      --format string        Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings          With --all, filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit machine-readable JSON.
      --limit int            With --all, drop at most this many matching queue items; 0 means no limit.
      --ready                With --all, only drop pending queue items whose next retry is due now.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --runtime strings      With --all, filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.
      --sort string          With --all, sort matching queue items before limiting: state, id, event, instance, job, runtime, queued, updated, next-retry, or attempts. (default "state")
      --state string         With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.
```

## `agent-team pipeline queue prune`

Prune pipeline-owned queue items.

Prune pipeline-owned queue items. By default this removes dead-letter items owned by the selected pipeline.

```text
agent-team pipeline queue prune <pipeline> [flags]
```

Flags:

```text
      --commands              With --dry-run, print the matching pipeline queue prune command when the preview has actionable work.
      --dry-run               Preview pipeline-owned queue items that would be pruned without dropping them.
      --event-type strings    Filter by event type before pruning; repeat or comma-separate values.
      --format string         Render each prune result with a Go template, e.g. '{{.ID}} {{.State}}'.
      --job strings           Filter by job id or ticket before pruning; repeat or comma-separate values.
      --json                  Emit prune results as JSON.
      --limit int             Prune at most this many matching pipeline-owned queue items; 0 means no limit.
      --older-than duration   Only prune pipeline-owned items older than this duration based on retry/dead-letter/update time.
      --ready                 Only prune pending queue items whose next retry is due now. Defaults --state to pending when --state is omitted.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --runtime strings       Filter by queued dispatch runtime before pruning: claude or codex. Can repeat or comma-separate.
      --state string          Queue state to prune: dead, pending, or all. (default "dead")
```

## `agent-team pipeline queue quarantine`

List pipeline-owned quarantined queue files.

List quarantined queue files owned by one pipeline. With no pipeline, all pipeline-owned quarantined queue files are listed. Show, restore, and drop still require an explicit pipeline.

```text
agent-team pipeline queue quarantine [<pipeline>|--all] [flags]
```

Flags:

```text
      --all                  List quarantined queue files across all pipelines. This is the default when no pipeline is passed.
      --commands             Print recommended commands from the visible pipeline-owned quarantined queue files, one per line. agent-team follow-ups preserve the selected repo scope.
      --event-type strings   Filter by event type; repeat or comma-separate values.
      --format string        Render each pipeline-owned quarantined queue file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.
      --job strings          Filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit pipeline-owned quarantined queue files as JSON.
      --limit int            Limit rows after filtering and sorting; 0 means no limit.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --restorable           Only show quarantined files that can be restored.
      --sort string          Sort rows by path, state, id, event, instance, job, queued, updated, modified, attempts, restorable, or size. (default "path")
      --state string         Filter by queue state: pending or dead.
      --summary              Show aggregate pipeline-owned quarantined queue-file counts instead of rows.
      --unrestorable         Only show quarantined files that cannot be restored.
```

Subcommands:

- `agent-team pipeline queue quarantine drop` - Drop pipeline-owned quarantined queue files after inspection.
- `agent-team pipeline queue quarantine restore` - Restore pipeline-owned quarantined queue files.
- `agent-team pipeline queue quarantine show` - Show one pipeline-owned quarantined queue file.

## `agent-team pipeline queue quarantine drop`

Drop pipeline-owned quarantined queue files after inspection.

Drop one pipeline-owned quarantined queue file by path, or drop a filtered pipeline-owned batch with --all.

```text
agent-team pipeline queue quarantine drop <pipeline> [quarantine-path] [flags]
```

Flags:

```text
      --all                   Drop all matching pipeline-owned quarantined files instead of one path.
      --commands              With --dry-run, print the matching pipeline queue quarantine drop apply command when the preview has actionable work.
      --dry-run               Preview quarantined files that would be dropped.
      --event-type strings    With --all, filter by event type; repeat or comma-separate values.
      --format string         Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings           With --all, filter by job id or ticket; repeat or comma-separate values.
      --json                  Emit drop results as JSON.
      --limit int             With --all, drop at most this many matching pipeline-owned quarantined files; 0 means no limit.
      --older-than duration   With --all, only drop files older than this duration based on file mtime.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --restorable            With --all, only drop quarantined files that can be restored.
      --sort string           With --all, sort matching pipeline-owned quarantined files before limiting: path, state, id, event, instance, job, queued, updated, modified, attempts, restorable, or size. (default "path")
      --state string          With --all, filter by queue state: pending or dead.
      --unrestorable          With --all, only drop quarantined files that cannot be restored.
```

## `agent-team pipeline queue quarantine restore`

Restore pipeline-owned quarantined queue files.

Restore one pipeline-owned quarantined queue file by path, or restore a filtered pipeline-owned batch of restorable files with --all.

```text
agent-team pipeline queue quarantine restore <pipeline> [quarantine-path] [flags]
```

Flags:

```text
      --all                  Restore all matching pipeline-owned restorable quarantined files instead of one path.
      --commands             With --dry-run, print the matching pipeline queue quarantine restore apply command when the preview has actionable work.
      --dry-run              Preview the restore without moving files.
      --event-type strings   With --all, filter by event type; repeat or comma-separate values.
      --force                Overwrite an existing active queue file with the same restore path.
      --format string        Render each restore result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings          With --all, filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit restore result as JSON.
      --limit int            With --all, restore at most this many matching pipeline-owned quarantined files; 0 means no limit.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --sort string          With --all, sort matching pipeline-owned quarantined files before limiting: path, state, id, event, instance, job, queued, updated, modified, attempts, restorable, or size. (default "path")
      --state string         With --all, filter by queue state: pending or dead.
```

## `agent-team pipeline queue quarantine show`

Show one pipeline-owned quarantined queue file.

```text
agent-team pipeline queue quarantine show <pipeline> <quarantine-path> [flags]
```

Flags:

```text
      --commands        Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.
      --format string   Render the pipeline-owned quarantined queue file with a Go template, e.g. '{{.Pipeline}} {{.ID}}'.
      --json            Emit the pipeline-owned quarantined queue file as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team pipeline queue retry`

Retry pipeline-owned queue items.

Retry one pipeline-owned queue item by id, or retry a filtered pipeline-owned batch with --all. Batch retries default to dead-letter items.

```text
agent-team pipeline queue retry <pipeline> [id] [flags]
```

Flags:

```text
      --all                  Retry all matching pipeline-owned queue items instead of one id.
      --commands             With --dry-run, print the matching pipeline queue retry command when the preview has actionable work.
      --dry-run              Preview matching pipeline-owned queue items without retrying them.
      --event-type strings   With --all, filter by event type; repeat or comma-separate values.
      --format string        Render each retry result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings          With --all, filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit machine-readable JSON.
      --limit int            With --all, retry at most this many matching queue items; 0 means no limit.
      --ready                With --all, only retry pending queue items whose next retry is due now.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --runtime strings      With --all, filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.
      --sort string          With --all, sort matching queue items before limiting: state, id, event, instance, job, runtime, queued, updated, next-retry, or attempts. (default "state")
      --state string         With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.
```

## `agent-team pipeline queue show`

Show one queue item owned by one pipeline.

```text
agent-team pipeline queue show <pipeline> <id> [flags]
```

Flags:

```text
      --commands        Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.
      --format string   Render the queue item with a Go template, e.g. '{{.ID}} {{.State}}'.
      --json            Emit the queue item as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team pipeline ready`

List ready pipeline jobs.

```text
agent-team pipeline ready [<pipeline>|--all] [flags]
```

Flags:

```text
      --all                 List ready jobs across all pipelines. This is the default when no pipeline is passed.
      --commands            Print only recommended commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string       Render each row with a Go template, e.g. '{{.JobID}} {{.State}} {{.StepID}}'.
      --interval duration   Refresh interval for --watch. (default 2s)
      --json                Emit ready rows as JSON.
      --limit int           Limit rows after filtering and sorting; 0 means no limit.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --repo string         Repo root containing .agent_team. (default "<repo>")
      --sort string         Sort rows by job, state, step, target, pipeline, updated, ticket, instance, or label. (default "job")
      --state strings       Next-step state to include: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --step string         Only include rows whose next step has this id.
  -w, --watch               Refresh the ready-step table until interrupted.
```

## `agent-team pipeline reject`

Reject blocked manual pipeline gates.

Reject blocked manual-gate steps for jobs in one pipeline, or all pipelines with --all. Rejected gates are marked failed and record a manual_gate_rejected audit event.

```text
agent-team pipeline reject <pipeline>|--all [flags]
```

Flags:

```text
      --all                   Reject manual gates across all pipelines.
      --commands              With --dry-run, print the matching reject apply command when the preview has actionable work.
      --dry-run               Preview manual gate rejections without writing job state.
      --format string         Render each rejection result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.
      --json                  Emit rejection results as JSON.
      --limit int             Maximum manual gates to reject (0 = no limit).
      --message string        Status message recorded on each rejected job.
      --message-file string   Read rejection reason from a file, or '-' for stdin.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --step string           Reject only manual gates whose next blocked step has this id.
```

## `agent-team pipeline release`

Release held pipeline jobs so automation can advance them.

Release held jobs in one pipeline, or all pipelines with --all, without changing their lifecycle status.

```text
agent-team pipeline release <pipeline>|--all [message...] [flags]
```

Aliases: `resume`, `unpause`

Flags:

```text
      --all                   Release held jobs across all pipelines.
      --commands              With --dry-run, print the matching release apply command when the preview has actionable work.
      --dry-run               Preview releases without writing job state.
      --expired               Only release held jobs whose hold_until has passed.
      --format string         Render each release result with a Go template, e.g. '{{.JobID}} {{.Action}}'.
      --json                  Emit release results as JSON.
      --limit int             Release at most this many held jobs; 0 means no limit.
      --message string        Release message recorded on each job.
      --message-file string   Read release message from a file, or '-' for stdin.
      --repo string           Repo root containing .agent_team. (default "<repo>")
```

## `agent-team pipeline repair`

Recover unhealthy orchestration state for one pipeline.

Recover unhealthy orchestration state scoped to one pipeline: ensure the daemon is ready, retry pipeline-owned dead-letter queue items, optionally time out stale work, retry failed steps, and advance ready steps. Use --dry-run to preview.

```text
agent-team pipeline repair <pipeline> [flags]
```

Flags:

```text
      --all-ready-steps               Advance every currently ready independent pipeline step during the scoped repair advance.
      --commands                      With --dry-run, print the matching pipeline repair apply command when the preview has actionable work.
      --dry-run                       Preview pipeline repair actions without mutating state or starting the daemon.
      --fail-on-failed                With --wait, exit 1 if any repaired job resolves to failed.
      --format string                 Render the pipeline repair result with a Go template, e.g. '{{.Pipeline}} {{.Queue.Action}}'.
      --json                          Emit machine-readable JSON.
      --limit int                     Retry at most this many pipeline-owned dead-letter queue items or failed pipeline jobs, and advance at most this many ready jobs or ready steps with --all-ready-steps; 0 means no limit.
      --preview-routes                With --dry-run, include route and dispatch payload previews for retried or ready pipeline steps.
      --ready-timeout duration        Maximum time to wait for implicit daemon readiness (0 = no timeout). (default 3s)
      --repo string                   Repo root containing .agent_team. (default "<repo>")
      --retry-force                   With --retry-pipelines, ignore step max_attempts caps for explicit pipeline repair retry.
      --retry-message string          Audit message to record when --retry-pipelines resets failed pipeline steps.
      --retry-message-file string     Read pipeline retry repair audit message from a file, or '-' for stdin.
      --retry-pipelines               Reset failed pipeline steps and dispatch them before the scoped advance.
      --retry-step string             With --retry-pipelines, retry only failed jobs whose next failed step has this id.
      --runtime string                Runtime profile for retried or advanced step dispatches (claude or codex). Overrides env and repo config.
      --runtime-bin string            Runtime binary for retried or advanced step dispatches. Overrides env and repo config.
      --skip-advance                  Do not advance ready pipeline steps after repair.
      --skip-daemon                   Do not start or reconcile the daemon.
      --skip-queue                    Do not retry pipeline-owned dead-letter queue items.
      --timeout-jobs                  Mark stale running pipeline job work failed before retrying failed steps.
      --timeout-message string        Audit message to record when pipeline timeout repair marks stale work failed.
      --timeout-message-file string   Read pipeline timeout repair audit message from a file, or '-' for stdin.
      --timeout-pipelines             Mark stale running pipeline steps failed before retrying failed steps.
      --timeout-step string           With --timeout-jobs or --timeout-pipelines, mark only stale running steps with this id failed.
      --timeout-target-agent string   With --timeout-jobs or --timeout-pipelines, mark only stale work targeting this agent.
      --wait                          After repair dispatches retried or ready steps, wait for those jobs to reach a lifecycle status, event, or next-step state.
      --wait-event strings            With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration        Polling interval with --wait. (default 500ms)
      --wait-next-state strings       With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings           With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string              With --wait, pipeline step id that must be the current next step for every repaired job.
      --wait-timeout duration         Maximum time to wait with --wait (0 = no timeout).
      --workspace string              Workspace mode for retried or advanced pipeline steps: auto, worktree, or repo. (default "auto")
```

## `agent-team pipeline resume-plan`

Show runtime resume and fallback commands for pipeline-owned jobs.

Show runtime resume and fallback commands for daemon metadata owned by jobs in one declared pipeline, or omit the pipeline/pass --all to inspect every pipeline-owned job. This is the pipeline-scoped form of `agent-team runtime resume-plan`.

```text
agent-team pipeline resume-plan [<pipeline>|--all] [flags]
```

Flags:

```text
      --action strings    Only include plans whose recommended action is start, attach, resume, or logs. Can repeat or comma-separate.
      --all               Plan runtime recovery across all pipelines. This is the default when no pipeline is passed.
      --can-managed       Only include runtimes with enough session metadata for daemon-managed resume.
      --commands          Print only recommended commands, one per line, after filtering, sorting, and limiting. agent-team follow-ups preserve the selected repo scope.
      --direct            Only include runtimes with a direct runtime resume command.
      --fallbacks         With --commands, print all viable start, attach, log, last-message, and direct resume commands per plan.
      --format string     Render each plan with a Go template, e.g. '{{.Instance}} {{.RecommendedAction}} {{.RecommendedCommand}}'.
      --json              Emit machine-readable JSON.
      --last-message      For Codex log fallbacks, recommend the clean last-message sidecar instead of following raw logs.
      --limit int         Limit plans after filtering and sorting; 0 means no limit.
      --managed           Only include runtimes whose adapter supports daemon-managed resume.
      --repo string       Repo root containing .agent_team. (default "<repo>")
      --runtime strings   Only include metadata for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale     Only include running metadata whose recorded runtime PID is no longer live.
      --sort string       Sort plans before rendering by instance, action, runtime, status, stale, job, pipeline, step, or agent. (default "instance")
      --stale             Only include running metadata whose recorded runtime PID is no longer live. Compatibility alias for --runtime-stale.
      --status strings    Only include metadata with this status: running, stopped, exited, or crashed. Can repeat or comma-separate.
      --step string       Only include plans for this pipeline step id.
      --summary           Summarize matching pipeline resume plans by recommended action, runtime, and status.
      --unhealthy         Only include crashed or stale running metadata.
```

## `agent-team pipeline retry`

Reset failed pipeline steps for another attempt.

Reset failed pipeline steps for jobs in one pipeline, or all pipelines with --all. By default this makes failed steps ready for the next pipeline advance; pass --step to target one stage, or --dispatch to immediately dispatch each retry.

```text
agent-team pipeline retry <pipeline>|--all [flags]
```

Flags:

```text
      --all                       Retry failed steps across all pipelines.
      --commands                  With --dry-run, print the matching retry apply command when the preview has actionable work.
      --dispatch                  Dispatch each reset failed step immediately.
      --dry-run                   Preview failed-step resets and optional dispatches without writing job or daemon state.
      --fail-on-failed            With --wait, exit 1 if any retried job resolves to failed.
      --force                     Ignore step max_attempts caps for this explicit retry.
      --format string             Render each retry result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.
      --json                      Emit retry results as JSON.
      --limit int                 Maximum failed jobs to retry (0 = no limit).
      --message string            Status message recorded on each retried job.
      --message-file string       Read retry message from a file, or '-' for stdin.
      --preview-routes            With --dry-run --dispatch, include route and payload previews.
      --repo string               Repo root containing .agent_team. (default "<repo>")
      --runtime string            Runtime profile for --dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for --dispatch. Overrides env and repo config.
      --step string               Retry only failed jobs whose next failed step has this id.
      --wait                      After retrying or dispatching, wait for retried jobs to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step for every retried job.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
      --workspace string          Workspace mode for --dispatch: auto, worktree, or repo. (default "auto")
```

## `agent-team pipeline run`

Create a durable job from a pipeline declaration.

```text
agent-team pipeline run <pipeline> <ticket> [kickoff...] [flags]
```

Flags:

```text
      --commands                  With --dry-run, print the matching pipeline run apply command. agent-team follow-ups preserve the selected repo scope.
      --dispatch                  Dispatch the first ready pipeline step immediately using the running daemon.
      --dry-run                   Preview the pipeline job that would be created without writing it.
      --fail-on-failed            With --wait, exit 1 if the job resolves to failed.
      --format string             Render the created or advanced job with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --id string                 Override the normalized job id (default: ticket slug).
      --json                      Emit the created job or advance result as JSON.
      --kickoff string            Kickoff text for the first pipeline step.
      --kickoff-file string       Read kickoff text from a file, or '-' for stdin.
      --repo string               Repo root containing .agent_team. (default "<repo>")
      --runtime string            Runtime profile for --dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for --dispatch. Overrides env and repo config.
      --ticket-url string         Canonical ticket URL to store on the job.
      --wait                      After creating or dispatching, wait for the job to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. advance_dispatched, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
      --workspace string          Workspace mode for --dispatch: auto, worktree, or repo. (default "auto")
```

## `agent-team pipeline runtime`

Inspect pipeline-owned runtime metadata.

Inspect daemon runtime metadata owned by jobs in one declared pipeline, or across every pipeline-owned job by default.

```text
agent-team pipeline runtime
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team pipeline runtime ls` - List daemon runtime metadata owned by pipeline jobs.

## `agent-team pipeline runtime ls`

List daemon runtime metadata owned by pipeline jobs.

List daemon-known runtime metadata owned by jobs in one declared pipeline. Omit the pipeline or pass --all to inspect every pipeline-owned job while excluding ad hoc instances.

```text
agent-team pipeline runtime ls [<pipeline>|--all] [flags]
```

Aliases: `list`, `ps`

Flags:

```text
      --agent strings      Only show pipeline-owned metadata for this agent. Can repeat or comma-separate.
      --all                List runtime metadata across all pipelines. This is the default when no pipeline is passed.
      --format string      Render each pipeline runtime row with a Go template, e.g. '{{.Instance}} {{.Runtime}} {{.Status}}'.
      --instance strings   Only show pipeline-owned metadata with this instance name. Can repeat or comma-separate.
      --json               Emit pipeline runtime metadata as JSON.
  -n, --last int           Show only the N most recently started pipeline-owned runtime records after other filters (0 = all).
  -l, --latest             Show only the most recently started pipeline-owned runtime record after other filters.
      --repo string        Repo root containing .agent_team. (default "<repo>")
      --runtime strings    Only show pipeline-owned metadata for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale      Only show pipeline-owned running metadata whose recorded runtime PID is no longer live.
      --sort string        Sort pipeline runtime rows by instance, status, runtime, agent, stale, unhealthy, job, started, stopped, or exited. (default "instance")
      --status strings     Only show pipeline-owned runtime status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary            Summarize matching pipeline-owned runtime metadata by status, runtime, and agent.
      --unhealthy          Only show crashed or runtime-stale pipeline-owned metadata.
```

## `agent-team pipeline send`

Send a mailbox message to pipeline-owned instances.

Send a mailbox message to daemon-known instances owned by jobs in one declared pipeline. Use --all to include every lifecycle status, or combine selectors such as --status, --runtime, --phase, --latest, --last, --stale, --runtime-stale, and --unhealthy.

```text
agent-team pipeline send <pipeline> [message...] [flags]
```

Flags:

```text
      --all                   Send to every daemon-known pipeline instance regardless of lifecycle status.
      --commands              With --dry-run, print the matching pipeline send apply command when the preview has actionable recipients.
      --dry-run               Preview matching recipients without appending mailbox messages.
      --format string         Render each send result with a Go template, e.g. '{{.To}} {{.ID}}'.
      --from string           Sender label recorded with the message. (default "(cli)")
      --json                  Emit machine-readable JSON.
  -n, --last int              Send to the N most recently started pipeline-owned daemon-known instances after other filters (0 = all).
      --latest                Send to the most recently started pipeline-owned daemon-known instance after other filters.
      --message string        Message text to send.
      --message-file string   Read message text from a file, or '-' for stdin.
      --phase strings         Send to pipeline-owned instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --runtime strings       Send to pipeline-owned instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale         Send to pipeline-owned running instances whose recorded runtime PID is no longer live.
      --stale                 Send to pipeline-owned instances whose status.toml is stale.
      --status strings        Send to pipeline-owned instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --unhealthy             Send to pipeline-owned instances that are crashed, status-stale, or runtime-stale.
```

## `agent-team pipeline show`

Show one declared pipeline.

```text
agent-team pipeline show <pipeline> [flags]
```

Aliases: `inspect`

Flags:

```text
      --format string   Render the pipeline with a Go template, e.g. '{{.Name}} {{len .Steps}}'.
      --json            Emit the pipeline as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team pipeline skip`

Mark matching pipeline steps intentionally skipped.

Mark matching non-running pipeline steps as done with skipped metadata for jobs in one pipeline, or all pipelines with --all. The step id is required to prevent accidental broad bypasses.

```text
agent-team pipeline skip <pipeline>|--all --step <id> [flags]
```

Flags:

```text
      --all                   Skip matching steps across all pipelines.
      --commands              With --dry-run, print the matching skip apply command when the preview has actionable work.
      --dry-run               Preview skipped steps without writing job state.
      --format string         Render each skip result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.
      --json                  Emit skip results as JSON.
      --limit int             Maximum matching steps to skip or report (0 = no limit).
      --message string        Skip reason recorded on each updated job.
      --message-file string   Read skip reason from a file, or '-' for stdin.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --step string           Required pipeline step id to mark skipped.
```

## `agent-team pipeline snapshot`

Capture a read-only diagnostic snapshot for one pipeline.

Capture a compact read-only diagnostic artifact for one pipeline, including status, step explanations, owned jobs, inbox summaries, queue ownership, dry-run advance previews, and command provenance.

```text
agent-team pipeline snapshot <pipeline> [flags]
```

Flags:

```text
      --commands               Print snapshot follow-up commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string          Render the pipeline snapshot with a Go template, e.g. '{{.Pipeline}} {{len .Jobs}}'.
      --json                   Emit the full pipeline snapshot JSON to stdout.
      --no-redact              Include raw payload values and latest inbox bodies instead of redacting them.
  -o, --output string          Write the full JSON pipeline snapshot to this file. Use '-' for stdout.
      --repo string            Repo root containing .agent_team. (default "<repo>")
      --timeline string        Include the last N combined audit/lifecycle timeline rows in the snapshot (0 or all = all). (default "50")
      --timeline-sort string   Sort included combined audit/lifecycle timeline rows by oldest or newest after applying --timeline. (default "oldest")
```

## `agent-team pipeline stats`

Show CPU and memory usage for pipeline-owned instances.

Show a one-shot or watchable resource snapshot for daemon-known instances owned by durable jobs in one declared pipeline. Omit the pipeline or pass --all to inspect every pipeline-owned job. With no filters, only running pipeline-owned instances are shown; use --status or --unhealthy to include inactive rows.

```text
agent-team pipeline stats [<pipeline>|--all] [flags]
```

Aliases: `top`

Flags:

```text
      --all                 Show stats across all pipelines. This is the default when no pipeline is passed.
      --format string       Render each row with a Go template, e.g. '{{.Instance}} {{.CPUPercent}} {{.RSS}}'.
      --interval duration   Refresh interval for --watch. (default 2s)
      --json                Emit JSON. With --watch, writes one JSON array per refresh.
  -n, --last int            Show stats for the N most recently started pipeline-owned instances after other filters (0 = all).
      --latest              Show stats for the most recently started pipeline-owned instance after other filters.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --phase strings       Only show pipeline-owned instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --repo string         Repo root containing .agent_team. (default "<repo>")
      --runtime strings     Only show pipeline-owned instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale       Only show pipeline-owned running instances whose recorded runtime PID is no longer live.
      --sort string         Sort rows by name, cpu, mem, rss, status, agent, phase, stale, runtime-stale, or unhealthy. (default "name")
      --stale               Only show pipeline-owned instances whose status.toml is stale.
      --status strings      Only show pipeline-owned lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary             Show aggregate CPU, memory, and RSS totals instead of pipeline instance rows.
      --unhealthy           Only show crashed, status-stale, or runtime-stale pipeline-owned instances.
  -w, --watch               Refresh pipeline stats until interrupted.
```

## `agent-team pipeline status`

Summarize pipeline jobs and next steps.

```text
agent-team pipeline status [<pipeline>|--all] [flags]
```

Aliases: `watch`

Flags:

```text
      --all                 Summarize all pipelines. This is the default when no pipeline is passed.
      --commands            Print recommended actions, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string       Render each row with a Go template, e.g. '{{.Pipeline}} {{.Jobs}} {{.ReadySteps}}'.
      --interval duration   Refresh interval for --watch. (default 2s)
      --json                Emit pipeline status rows as JSON.
      --limit int           Limit rows after sorting; 0 means no limit.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --repo string         Repo root containing .agent_team. (default "<repo>")
      --sort string         Sort rows by declared, pipeline, steps, jobs, queued, running, blocked, done, failed, ready, stale, manual, held, none, queue, queue-pending, queue-dead, queue-quarantined, quarantined, outbox, outbox-pending, outbox-failed, outbox-processed, or outbox-quarantined. (default "declared")
  -w, --watch               Refresh the pipeline status table until interrupted.
```

## `agent-team pipeline tick`

Run one pipeline&#39;s orchestration maintenance work.

Run or preview one pipeline&#39;s drainable queue items and ready steps.

```text
agent-team pipeline tick <pipeline> [flags]
```

Flags:

```text
      --all-ready-steps           Advance every currently ready independent pipeline step in this tick.
      --commands                  With --dry-run, print the matching pipeline tick apply command when the preview has actionable work.
      --dry-run                   Preview pipeline-owned maintenance work without mutating state.
      --fail-on-failed            With --wait, exit 1 if any advanced pipeline job resolves to failed.
      --format string             Render the pipeline tick result with a Go template, e.g. '{{.Pipeline}} {{.Tick.Queue.WouldDispatch}} {{len .Tick.Advance}}'.
      --json                      Emit machine-readable JSON.
      --limit int                 Advance at most this many ready pipeline jobs, or ready steps with --all-ready-steps; 0 means no limit.
      --preview-routes            With --dry-run, include route and dispatch payload previews for ready pipeline steps.
      --repo string               Repo root containing .agent_team. (default "<repo>")
      --runtime string            Runtime profile for advanced step dispatches (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for advanced step dispatches. Overrides env and repo config.
      --skip-advance              Skip pipeline advancement work.
      --skip-drain                Skip pipeline-owned queue drain work.
      --wait                      After one pipeline tick, wait for advanced pipeline jobs to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step for every advanced job.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
      --workspace string          Workspace mode for advanced pipeline steps: auto, worktree, or repo. (default "auto")
```

## `agent-team pipeline timeline`

Show combined job audit and lifecycle timelines for pipeline-owned jobs.

Show durable job audit events together with matching daemon lifecycle events for one pipeline, or for all pipeline-owned jobs.

```text
agent-team pipeline timeline [<pipeline>|--all] [flags]
```

Flags:

```text
      --actor strings      Only show job-audit timeline rows from this actor. Can repeat or comma-separate.
      --agent strings      Only show lifecycle timeline rows for this agent. Can repeat or comma-separate.
      --all                Show timelines across all pipeline-owned jobs. This is the default when no pipeline is passed.
      --format string      Render each timeline row with a Go template, e.g. '{{.JobID}} {{.Source}} {{.Kind}}'.
      --instance strings   Only show timeline rows for this owning instance. Can repeat or comma-separate.
      --job strings        Only show timeline rows for this job id. Can repeat or comma-separate.
      --json               Emit machine-readable JSON.
      --kind strings       Only show timeline rows with this kind/action. Can repeat or comma-separate.
      --repo string        Repo root containing .agent_team. (default "<repo>")
      --since string       Only show timeline rows since this duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --sort string        Sort returned timeline rows by oldest or newest after applying --tail. (default "oldest")
      --source string      Timeline source to include: all, job, or lifecycle. (default "all")
      --status strings     Only show timeline rows with this status. Can repeat or comma-separate.
      --summary            Summarize matching timeline rows by job, source, kind, status, actor, instance, and agent.
      --tail string        Show only the last N combined events before sorting for display (0 or all = all). (default "0")
```

## `agent-team pipeline timeout`

Mark stale running pipeline steps failed.

Mark stale running pipeline steps failed so they can be retried through the normal retry flow. A running step is stale when it exceeds its step timeout, or [health].job_stale_after when no step timeout is declared.

```text
agent-team pipeline timeout <pipeline>|--all [flags]
```

Flags:

```text
      --all                   Mark stale running steps failed across all pipelines.
      --commands              With --dry-run, print the matching timeout apply command when the preview has actionable work.
      --dry-run               Preview stale-step failures without writing job state.
      --format string         Render each timeout result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.
      --json                  Emit timeout results as JSON.
      --limit int             Maximum stale running steps to mark failed (0 = no limit).
      --message string        Status message recorded on each timed-out job.
      --message-file string   Read timeout message from a file, or '-' for stdin.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --step string           Mark only stale running steps with this id.
      --target-agent string   Mark only stale running steps targeting this agent.
```

## `agent-team pipeline triage`

Show pipeline-owned jobs that need operator attention.

Show a compact pipeline-scoped work queue triage view from durable jobs, persisted daemon queue items, status-file update previews, and ready pipeline steps. With no pipeline, all pipeline-owned jobs are considered.

```text
agent-team pipeline triage [<pipeline>|--all] [flags]
```

Flags:

```text
      --all                    Triage all pipeline-owned jobs. This is the default when no pipeline is passed.
      --commands               Print only recommended commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string          Render the pipeline triage snapshot with a Go template, e.g. '{{.Summary.Total}} {{len .Attention}}'.
      --interval duration      Refresh interval for --watch. (default 2s)
      --json                   Emit pipeline triage snapshot as JSON.
      --min-severity string    Only show attention rows at least this severe: critical, warning, or info.
      --no-clear               With --watch, append snapshots instead of redrawing the terminal.
      --reason strings         Only show attention rows with this reason. Can repeat or comma-separate.
      --repo string            Repo root containing .agent_team. (default "<repo>")
      --stale-after duration   Flag queued or running jobs with no update after this duration (default: [health].job_stale_after or 24h; 0 disables stale checks). (default 24h0m0s)
  -w, --watch                  Refresh the pipeline triage view until interrupted.
```

## `agent-team pipeline unblock`

Answer blocked pipeline workers.

Send the same operator answer to blocked pipeline step owners for jobs in one pipeline, or all pipelines with --all. By default a job is selected when it has a single blocked step owner; pass --step to target one stage explicitly.

```text
agent-team pipeline unblock <pipeline>|--all [message...] [flags]
```

Flags:

```text
      --all                   Unblock matching jobs across all pipelines.
      --allow-missing         Allow queueing messages for owning instances the daemon does not know yet.
      --commands              With --dry-run, print the matching unblock apply command when the preview has actionable work.
      --dry-run               Preview matching unblocks without writing job state or mailbox messages.
      --format string         Render each unblock result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}} {{.Instance}}'.
      --from string           Sender label recorded with each unblock message. (default "(cli)")
      --json                  Emit unblock results as JSON.
      --limit int             Maximum blocked jobs to unblock or report (0 = no limit).
      --message string        Message text to send.
      --message-file string   Read message text from a file, or '-' for stdin.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --status string         Status after unblocking: running or queued. (default "running")
      --step string           Unblock only blocked jobs whose selected step has this id.
```

## `agent-team pipeline wait`

Wait for pipeline jobs to reach a lifecycle status, event, or next step.

Wait for every selected pipeline-owned job to reach one of the requested lifecycle statuses, last events, and/or pipeline next-step states. By default this waits for terminal statuses: done or failed. When --event, --next-state, or --step is set without --status, any status is accepted.

```text
agent-team pipeline wait [<pipeline>|--all] [flags]
```

Flags:

```text
      --all                  Wait for jobs across all pipelines. This is the default when no pipeline is passed.
      --event strings        Last event to wait for, e.g. closed, adopted, or pipeline_done. Can repeat or comma-separate.
      --fail-on-failed       Exit 1 if any selected job resolves to failed.
      --format string        Render each final job with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --interval duration    Polling interval. (default 500ms)
      --job strings          Only wait for these pipeline-owned job ids. Can repeat or comma-separate.
      --json                 Emit final pipeline jobs as JSON.
      --next-state strings   Next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
  -q, --quiet                Suppress output and use only the exit code.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --status strings       Status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --step string          Pipeline step id that must be the current next step for every selected job.
      --timeout duration     Maximum time to wait (0 = no timeout).
```

## `agent-team plan`

Preview desired agent instance state from topology and daemon metadata.

Compare instances.toml with daemon metadata and show the lifecycle actions agent-team would normally take: start missing persistent instances, resume stopped ones when supported by the runtime, keep running ones, and leave ephemeral declarations on-demand. With --stop-extras, running daemon-known instances not declared in topology are previewed as stop actions.

```text
agent-team plan [flags]
```

Flags:

```text
      --action strings     Only show plan rows with this action: start, resume, restart, keep, unsupported, on-demand, stop, or extra. Can repeat or comma-separate.
      --agent strings      Only show plan rows for this agent. Can repeat or comma-separate.
      --commands           Print the matching dry-run sync command when the plan has actionable work. agent-team follow-ups preserve the selected repo scope.
      --format string      Render each plan row with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --instance strings   Only show plan rows with this name. Can repeat or comma-separate.
      --json               Emit machine-readable JSON.
      --phase strings      Only show plan rows in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --runtime strings    Only show daemon-known plan rows for this runtime: claude or codex. Can repeat or comma-separate.
      --status strings     Only show lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --stop-extras        Preview running topology extras as stop actions, matching sync --stop-extras.
      --summary            Show aggregate action counts instead of per-instance rows.
      --target string      Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team prune`

Remove finished daemon-managed instances.

Remove daemon-known exited or crashed instances and their state. Running and stopped instances are intentionally left alone unless selected by --runtime-stale or --unhealthy.

```text
agent-team prune [flags]
```

Flags:

```text
      --agent strings         Only remove matching instances for this agent. Can repeat or comma-separate.
      --commands              With --dry-run, print the matching prune apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run               Preview matching instances that would be pruned without deleting state or daemon metadata.
      --format string         Render each removal result with a Go template, e.g. '{{.Instance}} {{.Path}}'.
      --json                  Emit machine-readable JSON.
      --older-than duration   Only prune finished instances whose terminal timestamp is older than this duration (for example 24h).
      --phase strings         Only remove finished instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
  -q, --quiet                 Suppress non-error output and use only the exit code.
      --runtime strings       Only remove matching instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale         Also remove daemon-known running instances whose recorded runtime PID is no longer live.
      --stale                 Only remove finished instances whose non-idle work phase has stale status telemetry.
      --status strings        Only remove finished instances in this lifecycle status: exited or crashed. Can repeat or comma-separate.
      --summary               Show aggregate removal counts instead of per-instance rows.
      --target string         Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --unhealthy             Only remove crashed finished instances, finished status-stale instances, or runtime-stale running instances.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team ps`

List instances (daemon-aware: merges live daemon state with on-disk status).

Daemon-aware single-source view of instances. With the daemon running, lifecycle status (running/stopped/exited/crashed) comes from /v1/instances; phase / summary come from each instance&#39;s on-disk status.toml. Without a daemon, it merges on-disk status files with persisted runtime metadata from .agent_team/daemon. Unlike Docker, this command already shows every visible instance by default; --all is accepted for Docker-compatible muscle memory.

```text
agent-team ps [flags]
```

Aliases: `ls`

Flags:

```text
      --agent strings       Only show instances for this agent. Can repeat or comma-separate.
  -a, --all                 Show all visible instances. Accepted for Docker compatibility; this is already the default.
      --format string       Render each row with a Go template, e.g. '{{.Instance}} {{.Status}}'.
      --instance strings    Only show instances with this name. Can repeat or comma-separate.
      --interval duration   Refresh interval for --watch. (default 2s)
      --json                Emit JSON. With --watch, writes one JSON array per refresh.
  -n, --last int            Show only the N most recently started instances after other filters (0 = all).
  -l, --latest              Show only the most recently started instance after other filters.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --phase strings       Only show work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
  -q, --quiet               Only print matching instance names.
      --runtime strings     Only show instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale       Only show running instances whose recorded runtime PID is no longer live.
      --sort string         Sort rows by name, status, agent, phase, stale, runtime-stale, unhealthy, started, stopped, or exited. (default "name")
      --stale               Only show instances whose status.toml is stale.
      --status strings      Only show lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary             Show lifecycle counts instead of instance rows.
      --target string       Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --unhealthy           Only show crashed, status-stale, or runtime-stale instances.
  -w, --watch               Refresh the process table until interrupted.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team queue`

Inspect and control persisted daemon event queue items.

Inspect and control persisted daemon event queue items under `.agent_team/daemon/queue/`.

```text
agent-team queue
```

Aliases: `queues`

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team queue doctor` - Validate persisted queue files.
- `agent-team queue drain` - Ask the running daemon to dispatch ready pending queue items.
- `agent-team queue drop` - Drop pending or dead-letter queue items.
- `agent-team queue ls` - List persisted queue items.
- `agent-team queue prune` - Prune persisted queue items.
- `agent-team queue quarantine` - Inspect, restore, and drop quarantined queue files.
- `agent-team queue retry` - Retry pending or dead-letter queue items.
- `agent-team queue show` - Show one persisted queue item.

## `agent-team queue doctor`

Validate persisted queue files.

Validate persisted daemon queue files without relying on normal queue listing paths.

```text
agent-team queue doctor [flags]
```

Flags:

```text
      --commands        Print recommended follow-up commands, or with --quarantine --dry-run print the matching quarantine apply command.
      --dry-run         With --quarantine, preview files that would be moved.
      --format string   Render the queue doctor result with a Go template, e.g. '{{.OK}} {{.Summary.Invalid}}'.
      --json            Emit queue doctor findings as JSON.
      --quarantine      Move queue files with doctor problems out of the active queue.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team queue drain`

Ask the running daemon to dispatch ready pending queue items.

```text
agent-team queue drain [flags]
```

Flags:

```text
      --commands        With --dry-run, print the matching drain command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run         Preview ready queue items without dispatching them.
      --format string   Render the drain result with a Go template, e.g. '{{.Dispatched}} {{.Pending}}'.
      --json            Emit machine-readable JSON.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team queue drop`

Drop pending or dead-letter queue items.

Drop one queue item by id, or drop a filtered batch with --all. Batch drops default to dead-letter items.

```text
agent-team queue drop <id> [flags]
```

Flags:

```text
      --all                  Drop all matching queue items instead of one id.
      --commands             With --dry-run, print the matching drop command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run              Preview matching queue items without dropping them.
      --event-type strings   With --all, filter by event type; repeat or comma-separate values.
      --format string        Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --instance strings     With --all, filter by target instance name; repeat or comma-separate values.
      --job strings          With --all, filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit machine-readable JSON.
      --limit int            With --all, drop at most this many matching queue items; 0 means no limit.
      --ready                With --all, only drop pending queue items whose next retry is due now.
      --runtime strings      With --all, filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.
      --sort string          With --all, sort matching queue items before limiting: state, id, event, instance, job, runtime, queued, updated, next-retry, or attempts. (default "state")
      --state string         With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.
      --target string        Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team queue ls`

List persisted queue items.

```text
agent-team queue ls [flags]
```

Aliases: `watch`

Flags:

```text
      --commands             Print recommended commands from the visible queue rows, one per line. agent-team follow-ups preserve the selected repo scope.
      --event-type strings   Filter by event type; repeat or comma-separate values.
      --format string        Render each queue item with a Go template, e.g. '{{.ID}} {{.State}}'.
      --instance strings     Filter by target instance name; repeat or comma-separate values.
      --interval duration    Refresh interval for --watch. (default 2s)
      --job strings          Filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit machine-readable JSON.
      --limit int            Limit rows after filtering and sorting; 0 means no limit.
      --no-clear             With --watch, append snapshots instead of redrawing the terminal.
      --ready                Only show pending queue items whose next retry is due now.
      --reason strings       Filter by queue reason, such as lock_held. Can repeat or comma-separate.
      --runtime strings      Filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.
      --sort string          Sort rows by state, id, event, instance, job, runtime, queued, updated, next-retry, or attempts. (default "state")
      --state string         Filter by queue state: pending or dead.
      --summary              Show aggregate queue counts instead of queue rows.
      --target string        Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
  -w, --watch                Refresh the queue table until interrupted.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team queue prune`

Prune persisted queue items.

Prune persisted queue items. By default this removes dead-letter items.

```text
agent-team queue prune [flags]
```

Flags:

```text
      --commands              With --dry-run, print the matching prune command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run               Preview queue items that would be pruned without dropping them.
      --event-type strings    Filter by event type before pruning; repeat or comma-separate values.
      --format string         Render each result with a Go template, e.g. '{{.ID}} {{.State}}'.
      --instance strings      Filter by target instance name before pruning; repeat or comma-separate values.
      --job strings           Filter by job id or ticket before pruning; repeat or comma-separate values.
      --json                  Emit prune results as JSON.
      --limit int             Prune at most this many matching queue items; 0 means no limit.
      --older-than duration   Only prune items older than this duration based on retry/dead-letter/update time.
      --ready                 Only prune pending queue items whose next retry is due now. Defaults --state to pending when --state is omitted.
      --runtime strings       Filter by queued dispatch runtime before pruning: claude or codex. Can repeat or comma-separate.
      --state string          Queue state to prune: dead, pending, or all. (default "dead")
      --target string         Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team queue quarantine`

Inspect, restore, and drop quarantined queue files.

Inspect queue files moved under `.agent_team/daemon/queue/quarantine/`, restore validated entries to the active queue, or explicitly drop preserved files.

```text
agent-team queue quarantine
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team queue quarantine drop` - Drop quarantined queue files after inspection.
- `agent-team queue quarantine ls` - List quarantined queue files.
- `agent-team queue quarantine restore` - Restore validated quarantined queue files.
- `agent-team queue quarantine show` - Show one quarantined queue file.

## `agent-team queue quarantine drop`

Drop quarantined queue files after inspection.

Drop one quarantined queue file by path, or drop a filtered batch with --all.

```text
agent-team queue quarantine drop [quarantine-path] [flags]
```

Flags:

```text
      --all                   Drop all matching quarantined files instead of one path.
      --commands              With --dry-run, print the matching drop apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run               Preview quarantined files that would be dropped.
      --event-type strings    With --all, filter by event type; repeat or comma-separate values.
      --format string         Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --instance strings      With --all, filter by target instance name; repeat or comma-separate values.
      --job strings           With --all, filter by job id or ticket; repeat or comma-separate values.
      --json                  Emit drop results as JSON.
      --limit int             With --all, drop at most this many matching quarantined files; 0 means no limit.
      --older-than duration   With --all, only drop files older than this duration based on file mtime.
      --restorable            With --all, only drop quarantined files that can be restored.
      --sort string           With --all, sort matching quarantined files before limiting: path, state, id, event, instance, job, queued, updated, modified, attempts, restorable, or size. (default "path")
      --state string          With --all, filter by queue state: pending or dead.
      --target string         Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --unrestorable          With --all, only drop quarantined files that cannot be restored.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team queue quarantine ls`

List quarantined queue files.

```text
agent-team queue quarantine ls [flags]
```

Flags:

```text
      --commands             Print recommended commands from the visible quarantined queue files, one per line. agent-team follow-ups preserve the selected repo scope.
      --event-type strings   Filter by event type; repeat or comma-separate values.
      --format string        Render each quarantined queue file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.
      --instance strings     Filter by target instance name; repeat or comma-separate values.
      --job strings          Filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit quarantined queue files as JSON.
      --limit int            Limit rows after filtering and sorting; 0 means no limit.
      --restorable           Only show quarantined files that can be restored.
      --sort string          Sort rows by path, state, id, event, instance, job, queued, updated, modified, attempts, restorable, or size. (default "path")
      --state string         Filter by queue state: pending or dead.
      --summary              Show aggregate quarantined queue-file counts instead of rows.
      --target string        Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --unrestorable         Only show quarantined files that cannot be restored.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team queue quarantine restore`

Restore validated quarantined queue files.

Restore one validated quarantined queue file by path, or restore a filtered batch of restorable files with --all.

```text
agent-team queue quarantine restore [quarantine-path] [flags]
```

Flags:

```text
      --all                  Restore all matching restorable quarantined files instead of one path.
      --commands             With --dry-run, print the matching restore apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run              Preview the restore without moving files.
      --event-type strings   With --all, filter by event type; repeat or comma-separate values.
      --force                Overwrite an existing active queue file with the same restore path.
      --format string        Render each restore result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --instance strings     With --all, filter by target instance name; repeat or comma-separate values.
      --job strings          With --all, filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit restore result as JSON.
      --limit int            With --all, restore at most this many matching quarantined files; 0 means no limit.
      --sort string          With --all, sort matching quarantined files before limiting: path, state, id, event, instance, job, queued, updated, modified, attempts, restorable, or size. (default "path")
      --state string         With --all, filter by queue state: pending or dead.
      --target string        Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team queue quarantine show`

Show one quarantined queue file.

```text
agent-team queue quarantine show <quarantine-path> [flags]
```

Flags:

```text
      --commands        Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.
      --format string   Render the quarantined queue file with a Go template, e.g. '{{.ID}} {{.State}}'.
      --json            Emit the quarantined queue file as JSON.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team queue retry`

Retry pending or dead-letter queue items.

Retry one queue item by id, or retry a filtered batch with --all. Batch retries default to dead-letter items.

```text
agent-team queue retry <id> [flags]
```

Flags:

```text
      --all                  Retry all matching queue items instead of one id.
      --commands             With --dry-run, print the matching retry command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run              Preview matching queue items without retrying them.
      --event-type strings   With --all, filter by event type; repeat or comma-separate values.
      --format string        Render each retry result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --instance strings     With --all, filter by target instance name; repeat or comma-separate values.
      --job strings          With --all, filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit machine-readable JSON.
      --limit int            With --all, retry at most this many matching queue items; 0 means no limit.
      --ready                With --all, only retry pending queue items whose next retry is due now.
      --runtime strings      With --all, filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.
      --sort string          With --all, sort matching queue items before limiting: state, id, event, instance, job, runtime, queued, updated, next-retry, or attempts. (default "state")
      --state string         With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.
      --target string        Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team queue show`

Show one persisted queue item.

```text
agent-team queue show <id> [flags]
```

Flags:

```text
      --commands        Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.
      --format string   Render the queue item with a Go template, e.g. '{{.ID}} {{.State}}'.
      --json            Emit the queue item as JSON.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team reload`

Reload daemon topology and reconcile runtime metadata.

Re-read .agent_team/instances.toml in the running daemon and then reconcile daemon metadata against the live process table. This is the operator path after editing declarations when you do not want to restart agent-teamd.

```text
agent-team reload [flags]
```

Flags:

```text
      --format string   Render reload result with a Go template, e.g. '{{len .Topology.Instances}} {{.Reconcile.Changed}}'.
      --json            Emit machine-readable JSON.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team repair`

Recover common unhealthy orchestration state.

Recover common unhealthy orchestration state: ensure the daemon is ready, retry dead-letter queue items, optionally time out stale job work, optionally retry failed pipeline steps, and run a maintenance tick to drain ready work and advance pipelines. Use --dry-run to preview.

```text
agent-team repair [flags]
```

Flags:

```text
      --all-ready-steps               Advance every currently ready independent pipeline step during the repair tick.
      --commands                      With --dry-run, print the matching repair apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run                       Preview repair actions without mutating state or starting the daemon.
      --fail-on-failed                With --wait, exit 1 if any repaired job resolves to failed.
      --fallbacks                     When repair health snapshots include runtime recovery actions, recommend command-mode fallback expansion.
      --format string                 Render the repair result with a Go template, e.g. '{{.DryRun}} {{.Queue.Action}}'.
      --interval duration             Delay between --until-idle maintenance cycles. (default 2s)
      --jobs                          Include durable job triage and status-file previews in health snapshots.
      --json                          Emit machine-readable JSON.
      --last-message                  When repair health snapshots include runtime recovery actions, prefer clean Codex final-message commands.
      --limit int                     Retry at most this many dead-letter queue items or failed pipeline jobs, and advance at most this many ready pipeline jobs or ready steps with --all-ready-steps; 0 means no limit.
      --max-cycles int                With --until-idle, stop after this many cycles if work keeps appearing. (default 20)
      --preview-routes                With --dry-run, include route and dispatch payload previews for retried or ready pipeline steps.
      --ready-timeout duration        Maximum time to wait for implicit daemon readiness (0 = no timeout). (default 3s)
      --retry-force                   With --retry-pipelines, ignore step max_attempts caps for explicit repair retry.
      --retry-message string          Audit message to record when --retry-pipelines resets failed steps.
      --retry-message-file string     Read retry repair audit message from a file, or '-' for stdin.
      --retry-pipeline string         With --retry-pipelines, retry only failed jobs owned by this pipeline.
      --retry-pipelines               Reset failed pipeline steps and dispatch them before the maintenance tick.
      --retry-step string             With --retry-pipelines, retry only failed jobs whose next failed step has this id.
      --runtime string                Runtime profile for retried or advanced step dispatches (claude or codex). Overrides env and repo config.
      --runtime-bin string            Runtime binary for retried or advanced step dispatches. Overrides env and repo config.
      --skip-daemon                   Do not start or reconcile the daemon.
      --skip-queue                    Do not retry dead-letter queue items.
      --skip-tick                     Do not run a maintenance tick after queue retry.
      --target string                 Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --timeout-jobs                  Mark stale running durable job work failed before retrying failed pipeline steps.
      --timeout-message string        Audit message to record when timeout repair marks stale work failed.
      --timeout-message-file string   Read timeout repair audit message from a file, or '-' for stdin.
      --timeout-pipeline string       With --timeout-jobs or --timeout-pipelines, mark only stale work owned by this pipeline.
      --timeout-pipelines             Mark stale running pipeline steps failed before retrying failed pipeline steps.
      --timeout-step string           With --timeout-jobs or --timeout-pipelines, mark only stale running steps with this id failed.
      --timeout-target-agent string   With --timeout-jobs or --timeout-pipelines, mark only stale work targeting this agent.
      --until-idle                    Run maintenance ticks until no immediate queue, schedule, or pipeline work remains.
      --wait                          After repair dispatches retried or ready steps, wait for those jobs to reach a lifecycle status, event, or next-step state.
      --wait-event strings            With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration        Polling interval with --wait. (default 500ms)
      --wait-next-state strings       With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings           With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string              With --wait, pipeline step id that must be the current next step for every repaired job.
      --wait-timeout duration         Maximum time to wait with --wait (0 = no timeout).
      --workspace string              Workspace mode for retried or advanced pipeline steps: auto, worktree, or repo. (default "auto")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team restart`

Restart or resume instances.

Restart declared persistent instances through the daemon. Running instances are stopped and resumed; stopped instances are resumed; instances with no daemon metadata are started fresh. Explicit names may also target daemon-known ad-hoc instances. Runtimes without managed resume support are reported as unsupported and left untouched.

```text
agent-team restart [<instance>...] [flags]
```

Flags:

```text
      --agent strings            Restart or resume every declared persistent and daemon-known instance for this agent. Can repeat or comma-separate.
  -a, --all                      Restart or resume every declared persistent and daemon-known instance.
      --attach                   Follow the selected instance log after restarting or resuming. Requires exactly one selected instance.
      --commands                 With --dry-run, print the matching apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run                  Preview planned restart/resume actions without changing daemon state.
  -f, --force                    Escalate to SIGKILL if a running instance does not stop within --timeout before restarting.
      --format string            Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                     Emit machine-readable JSON.
  -n, --last int                 Restart or resume the N most recently started instances after other filters (0 = all).
      --latest                   Restart or resume the most recently started instance after other filters.
      --phase strings            Only restart or resume instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --prompt string            Override the default kickoff prompt for instances started fresh.
      --prompt-file string       Read kickoff prompt from a file, or '-' for stdin.
  -q, --quiet                    Suppress non-error output and use only the exit code.
      --ready-timeout duration   Maximum time to wait for implicit daemon readiness (0 = no timeout). (default 3s)
      --runtime strings          Only restart or resume daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale            Only restart or resume running instances whose recorded runtime PID is no longer live.
      --stale                    Only restart or resume instances whose status.toml is stale.
      --status strings           Only restart or resume instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary                  Show aggregate action counts instead of per-instance rows.
      --tail string              With --attach, show only the last N lines before following (0 or all = all). (default "50")
      --target string            Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --timeout duration         Maximum time to wait for each running instance to stop before resuming (0 = daemon default).
      --unhealthy                Only restart or resume instances that are crashed, status-stale, or runtime-stale.
      --wait                     Wait for selected instances to become healthy after restarting. With no scoped selection, waits for the fleet.
      --wait-timeout duration    Maximum time to wait for health with --wait (0 = no timeout).
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team resume-plan`

Show runtime resume and fallback commands for daemon metadata.

Show runtime resume and fallback commands for daemon metadata without contacting the daemon. This is a shorter alias for `agent-team runtime resume-plan`.

```text
agent-team resume-plan [<instance>...] [flags]
```

Flags:

```text
      --action strings    Only include plans whose recommended action is start, attach, resume, or logs. Can repeat or comma-separate.
      --can-managed       Only include runtimes with enough session metadata for daemon-managed resume.
      --commands          Print only recommended commands, one per line, after filtering, sorting, and limiting. agent-team follow-ups preserve the selected repo scope.
      --direct            Only include runtimes with a direct runtime resume command.
      --fallbacks         With --commands, print all viable start, attach, log, last-message, and direct resume commands per plan.
      --format string     Render each plan with a Go template, e.g. '{{.Instance}} {{.RecommendedAction}} {{.RecommendedCommand}}'.
      --job string        Select the instance recorded on or associated with this job id.
      --json              Emit machine-readable JSON.
      --last-message      For Codex log fallbacks, recommend the clean last-message sidecar instead of following raw logs.
      --limit int         Limit plans after filtering and sorting; 0 means no limit.
      --managed           Only include runtimes whose adapter supports daemon-managed resume.
      --repo string       Repo root containing .agent_team. (default "<repo>")
      --runtime strings   Only include metadata for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale     Only include running metadata whose recorded runtime PID is no longer live.
      --sort string       Sort plans before rendering by instance, action, runtime, status, stale, job, pipeline, step, or agent. (default "instance")
      --stale             Only include running metadata whose recorded runtime PID is no longer live. Compatibility alias for --runtime-stale.
      --status strings    Only include metadata with this status: running, stopped, exited, or crashed. Can repeat or comma-separate.
      --step string       Only include plans for this pipeline step id.
      --summary           Summarize matching resume plans by recommended action, runtime, and status.
      --unhealthy         Only include crashed or stale running metadata.
```

## `agent-team rm`

Remove instance state and daemon metadata.

Docker-like convenience alias for `agent-team instance rm`. When the daemon is running, also removes daemon metadata; use --force to stop and remove a running instance.

```text
agent-team rm [<instance>...] [flags]
```

Flags:

```text
      --agent strings     With --all, --finished, --latest, --last, --runtime, --status, --phase, --stale, --runtime-stale, or --unhealthy, only remove daemon-known instances for this agent. Can repeat or comma-separate.
  -a, --all               Remove every daemon-known instance. Can combine with --agent, --runtime, --status, --phase, --stale, --runtime-stale, or --unhealthy.
      --commands          With --dry-run, print the matching remove command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run           Preview matching removals without deleting state or daemon metadata.
      --finished          Remove every daemon-known exited or crashed instance.
  -f, --force             Skip confirmation; if the daemon is running, stop a running instance before removal.
      --format string     Render each removal result with a Go template, e.g. '{{.Instance}} {{.Path}}'. Requires --force unless --dry-run is set.
      --json              Emit machine-readable JSON. Requires --force unless --dry-run is set.
  -n, --last int          Remove the N most recently started daemon-known instances after other filters (0 = all).
      --latest            Remove the most recently started daemon-known instance after other filters.
      --phase strings     Remove daemon-known instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
  -q, --quiet             Suppress non-error output. Requires --force unless --dry-run is set.
      --runtime strings   Remove daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale     Remove only daemon-known running instances whose recorded runtime PID is no longer live.
      --stale             Remove only daemon-known instances whose non-idle work phase has stale status telemetry.
      --status strings    Remove daemon-known instances currently in this lifecycle status: stopped, exited, crashed, running, or unknown. Can repeat or comma-separate.
      --summary           Show aggregate removal counts instead of per-instance rows.
      --target string     Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --unhealthy         Remove only daemon-known instances that are crashed, status-stale, or runtime-stale.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team run`

Launch an LLM runtime session as the named agent.

Launch an LLM runtime session as the named agent. With the default Claude-compatible runtime, the agent&#39;s prompt becomes the system prompt and all other agents are registered as subagents. Pass `--name` to give the instance a unique identifier (state dir: .agent_team/state/&lt;name&gt;/). Forward extra runtime args after `--`.

```text
agent-team run <agent> [-- <runtime-args>...] [flags]
```

Flags:

```text
      --attach                   Dispatch through agent-teamd and follow the captured instance log.
  -d, --detach                   Dispatch through agent-teamd and return immediately instead of attaching to the runtime.
      --format string            Render daemon dispatch metadata with a Go template, e.g. '{{.Instance}} {{.PID}}'. Requires --prompt or --detach.
      --instance-config string   Path to a per-instance TOML config that layers on top of repo config (below --set).
      --json                     Emit daemon dispatch metadata as JSON. Requires --prompt or --detach.
      --last-message             With Codex --prompt runs, bypass the daemon and print only the clean final response sidecar.
  -n, --name string              Instance name (defaults to the agent name). State dir: .agent_team/state/<name>/.
      --no-daemon                Bypass the daemon: exec the runtime directly even if the daemon is running. Useful for debugging.
  -p, --prompt string            Kickoff message. With this, the runtime runs in one-shot mode when supported; without, interactive.
      --prompt-file string       Read kickoff message from a file, or '-' for stdin.
      --ready-timeout duration   Maximum time to wait for daemon readiness with --detach or --attach (0 = no timeout). (default 3s)
      --runtime string           Runtime profile for this invocation (claude or codex). Overrides env and repo config.
      --runtime-bin string       Runtime binary for this invocation. Overrides env and repo config.
      --set stringArray          Override a config value for this spawn, e.g. --set linear.team_id=<x>. Repeatable.
      --tail string              With --attach, show only the last N lines before following (0 or all = all). (default "50")
      --target string            Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team runtime`

Inspect the selected LLM runtime profile.

Inspect the selected LLM runtime profile, binary resolution, and whether the runtime supports direct runs, daemon dispatch, direct resume, managed resume, and native subagents.

```text
agent-team runtime [flags]
```

Flags:

```text
      --format string        Render runtime info with a Go template, e.g. '{{.Runtime}} {{.Available}}'.
      --json                 Emit machine-readable JSON.
      --runtime string       Runtime profile to inspect for this invocation (claude or codex). Overrides env and repo config.
      --runtime-bin string   Runtime binary to inspect for this invocation. Overrides env and repo config.
      --target string        Repo root or any path under a repo. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team runtime adopt` - Adopt a live external runtime process.
- `agent-team runtime ls` - List supported runtime profiles.
- `agent-team runtime metadata` - Inspect persisted daemon runtime metadata.
- `agent-team runtime probe` - Probe runtime, daemon, and Codex environment health.
- `agent-team runtime profile` - Show the selected runtime profile.
- `agent-team runtime resume-plan` - Show runtime resume and fallback commands for daemon metadata.
- `agent-team runtime set` - Set the repo default runtime profile.
- `agent-team runtime unset` - Remove the repo default runtime profile.

## `agent-team runtime adopt`

Adopt a live external runtime process.

Adopt a live external runtime process by writing daemon runtime metadata for it. Adopted processes become visible to ps, inspect, monitor, stop, and reconcile. Use this when a Claude or Codex process was started outside agent-team but should be tracked by the repo daemon.

```text
agent-team runtime adopt <instance> [flags]
```

Flags:

```text
      --agent string         Agent name for the adopted instance. Inferred from instances.toml when omitted.
      --branch string        Branch name to record on the adopted metadata.
      --commands             Print only follow-up commands, one per line, after adoption planning or apply.
      --dry-run              Preview adoption without writing metadata.
      --force                Replace existing live metadata for the instance.
      --format string        Render the adoption result with a Go template, e.g. '{{.Metadata.Instance}} {{.Metadata.PID}}'.
      --job string           Owning job id to record on the adopted metadata.
      --json                 Emit machine-readable JSON.
      --log-path string      Runtime log path, if the external process already writes to one.
      --pid int              Live process PID to adopt.
      --pid-file string      Read the live process PID to adopt from this file. Cannot be combined with --pid.
      --pr string            PR URL to record on the adopted metadata.
      --runtime string       Runtime profile for the adopted process (claude or codex). Defaults to repo/env selection.
      --runtime-bin string   Runtime binary or wrapper used by the adopted process.
      --session-id string    Runtime session id, when known and resumable.
      --started-at string    Process start time as RFC3339. Defaults to now, or existing metadata for the same PID.
      --step string          Pipeline step id to mark as owned by the adopted process. Requires --job.
      --target string        Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --ticket string        Ticket id to record on the adopted metadata.
      --workspace string     Workspace path for the adopted process. Defaults to the repo root.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team runtime ls`

List supported runtime profiles.

List supported runtime profiles, binary resolution, availability, and runtime capabilities. The selected row is the profile the current environment or repo config would use by default.

```text
agent-team runtime ls [flags]
```

Aliases: `list`

Flags:

```text
      --commands        Print runtime probe commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string   Render each runtime row with a Go template, e.g. '{{.Runtime}} {{.Available}}'.
      --json            Emit machine-readable JSON.
      --target string   Repo root or any path under a repo. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team runtime metadata`

Inspect persisted daemon runtime metadata.

Inspect raw daemon runtime metadata persisted under .agent_team/daemon without adding declared-but-not-started placeholders.

```text
agent-team runtime metadata
```

Aliases: `meta`

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team runtime metadata ls` - List persisted daemon runtime metadata.
- `agent-team runtime metadata show` - Show one persisted daemon runtime metadata record.

## `agent-team runtime metadata ls`

List persisted daemon runtime metadata.

List raw daemon runtime metadata persisted for this repo without adding declared-but-not-started placeholders.

```text
agent-team runtime metadata ls [<instance>...] [flags]
```

Aliases: `list`, `ps`

Flags:

```text
      --agent strings      Only show metadata for this agent. Can repeat or comma-separate.
      --commands           Print one runtime metadata show command per matching row. agent-team follow-ups preserve the selected repo scope.
      --format string      Render each runtime metadata row with a Go template, e.g. '{{.Instance}} {{.Runtime}} {{.Status}}'.
      --instance strings   Only show metadata with this instance name. Can repeat or comma-separate.
      --json               Emit runtime metadata as JSON.
  -n, --last int           Show only the N most recently started runtime metadata records after other filters (0 = all).
  -l, --latest             Show only the most recently started runtime metadata record after other filters.
      --runtime strings    Only show metadata for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale      Only show running metadata whose recorded runtime PID is no longer live.
      --sort string        Sort runtime metadata rows by instance, status, runtime, agent, stale, unhealthy, job, started, stopped, or exited. (default "instance")
      --status strings     Only show metadata with this status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary            Summarize matching runtime metadata by status, runtime, and agent.
      --target string      Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --unhealthy          Only show crashed or runtime-stale metadata.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team runtime metadata show`

Show one persisted daemon runtime metadata record.

Show one raw daemon runtime metadata record persisted for this repo, enriching job ownership fields from durable job files when possible.

```text
agent-team runtime metadata show <instance> [flags]
```

Aliases: `get`, `inspect`

Flags:

```text
      --commands        Print follow-up inspect, logs, resume-plan, and job show commands. agent-team follow-ups preserve the selected repo scope.
      --format string   Render the runtime metadata row with a Go template, e.g. '{{.Instance}} {{.Runtime}} {{.Status}}'.
      --json            Emit runtime metadata as JSON.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team runtime probe`

Probe runtime, daemon, and Codex environment health.

Probe the selected runtime and repo daemon health. For the Codex runtime, the probe also runs `codex doctor --json` so provider reachability, auth, and sandbox issues are captured before dispatching work. Pass --exec to also run a minimal real Codex `exec -` one-shot and verify last-message capture.

```text
agent-team runtime probe [flags]
```

Aliases: `check`, `doctor`

Flags:

```text
      --codex-daemon-check         Run the recommended Codex daemon reachability probe: start agent-teamd with loopback HTTP and run --exec-http-check. Implies --runtime codex.
      --commands                   Print recommended follow-up commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --daemon-http-addr string    With --start-daemon, also expose agent-teamd on this loopback HTTP address, e.g. 127.0.0.1:0.
      --daemon-interval duration   Polling interval for --wait-daemon. (default 200ms)
      --exec                       Run a minimal runtime-native execution probe. Currently supports Codex one-shot execution.
      --exec-http-check            Run a Codex exec probe that verifies daemon loopback HTTP access through AGENT_TEAM_DAEMON_URL. Implies --exec and --require-daemon.
      --exec-prompt string         Prompt sent to the runtime when --exec is set. (default "Reply exactly with: agent-team runtime probe ok")
      --exec-prompt-file string    Read --exec probe prompt from a file, or '-' for stdin.
      --exec-socket-check          Run a Codex exec probe that verifies daemon Unix-socket access from inside the runtime sandbox. Implies --exec and --require-daemon.
      --format string              Render the probe result with a Go template, e.g. '{{.OK}} {{len .Issues}}'.
      --json                       Emit machine-readable JSON.
      --output string              Write the full probe result as pretty JSON to this file.
      --require-daemon             Fail when the repo daemon is not running and ready.
      --runtime string             Runtime profile to probe for this invocation (claude or codex). Overrides env and repo config.
      --runtime-bin string         Runtime binary to probe for this invocation. Overrides env and repo config.
      --skip-doctor                Skip runtime-native diagnostics such as codex doctor --json.
      --start-daemon               Start the detached repo daemon before reporting daemon health when it is not ready.
      --target string              Repo root or any path under a repo. (default "<repo>")
      --timeout duration           Maximum time for daemon wait and external runtime diagnostics such as codex doctor --json. (default 20s)
      --wait-daemon                Wait for the repo daemon to become ready before reporting daemon health.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team runtime profile`

Show the selected runtime profile.

Show the selected LLM runtime profile, binary resolution, and whether the runtime supports direct runs, daemon dispatch, direct resume, managed resume, and native subagents.

```text
agent-team runtime profile [flags]
```

Aliases: `show`

Flags:

```text
      --format string        Render runtime info with a Go template, e.g. '{{.Runtime}} {{.Available}}'.
      --json                 Emit machine-readable JSON.
      --runtime string       Runtime profile to inspect for this invocation (claude or codex). Overrides env and repo config.
      --runtime-bin string   Runtime binary to inspect for this invocation. Overrides env and repo config.
      --target string        Repo root or any path under a repo. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team runtime resume-plan`

Show runtime resume and fallback commands for daemon metadata.

Show runtime resume and fallback commands for daemon metadata without contacting the daemon. This explains whether an instance can be resumed through agent-team, which direct runtime command is available, and which log commands are safest for runtimes without managed resume.

```text
agent-team runtime resume-plan [<instance>...] [flags]
```

Flags:

```text
      --action strings    Only include plans whose recommended action is start, attach, resume, or logs. Can repeat or comma-separate.
      --can-managed       Only include runtimes with enough session metadata for daemon-managed resume.
      --commands          Print only recommended commands, one per line, after filtering, sorting, and limiting. agent-team follow-ups preserve the selected repo scope.
      --direct            Only include runtimes with a direct runtime resume command.
      --fallbacks         With --commands, print all viable start, attach, log, last-message, and direct resume commands per plan.
      --format string     Render each plan with a Go template, e.g. '{{.Instance}} {{.RecommendedAction}} {{.RecommendedCommand}}'.
      --job string        Select the instance recorded on or associated with this job id.
      --json              Emit machine-readable JSON.
      --last-message      For Codex log fallbacks, recommend the clean last-message sidecar instead of following raw logs.
      --limit int         Limit plans after filtering and sorting; 0 means no limit.
      --managed           Only include runtimes whose adapter supports daemon-managed resume.
      --runtime strings   Only include metadata for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale     Only include running metadata whose recorded runtime PID is no longer live.
      --sort string       Sort plans before rendering by instance, action, runtime, status, stale, job, pipeline, step, or agent. (default "instance")
      --stale             Only include running metadata whose recorded runtime PID is no longer live. Compatibility alias for --runtime-stale.
      --status strings    Only include metadata with this status: running, stopped, exited, or crashed. Can repeat or comma-separate.
      --step string       Only include plans for this pipeline step id.
      --summary           Summarize matching resume plans by recommended action, runtime, and status.
      --target string     Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --unhealthy         Only include crashed or stale running metadata.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team runtime set`

Set the repo default runtime profile.

Set the repo default LLM runtime profile in .agent_team/config.toml. Command flags and AGENT_TEAM_RUNTIME / AGENT_TEAM_RUNTIME_BIN still override this repo default at runtime.

```text
agent-team runtime set <runtime> [flags]
```

Aliases: `configure`, `use`

Flags:

```text
      --binary string        Alias for --runtime-bin.
      --commands             With --dry-run, print the apply command. agent-team follow-ups preserve the selected repo scope.
      --dry-run              Preview the config change without writing.
      --format string        Render the set result with a Go template, e.g. '{{.Runtime}} {{.Binary}}'.
      --json                 Emit machine-readable JSON.
      --runtime-bin string   Runtime binary or wrapper to store. Defaults to the runtime's built-in binary.
      --target string        Repo root or any path under a repo. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team runtime unset`

Remove the repo default runtime profile.

Remove [runtime].kind, [runtime].binary, and [runtime].bin from .agent_team/config.toml so the repo falls back to environment variables or built-in runtime defaults.

```text
agent-team runtime unset [flags]
```

Aliases: `clear`, `reset`

Flags:

```text
      --commands        With --dry-run, print the apply command. agent-team follow-ups preserve the selected repo scope.
      --dry-run         Preview the config change without writing.
      --format string   Render the unset result with a Go template, e.g. '{{.Changed}} {{.DryRun}}'.
      --json            Emit machine-readable JSON.
      --target string   Repo root or any path under a repo. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team schedule`

Inspect and run declared schedule events.

Inspect schedules declared in .agent_team/instances.toml and manually publish their schedule events.

```text
agent-team schedule
```

Aliases: `schedules`

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team schedule due` - List schedules due now.
- `agent-team schedule fire` - Publish every schedule due now through the daemon.
- `agent-team schedule ls` - List declared schedules.
- `agent-team schedule next` - List declared schedules ordered by next run.
- `agent-team schedule run` - Publish one declared schedule event now.
- `agent-team schedule show` - Show one declared schedule.

## `agent-team schedule due`

List schedules due now.

```text
agent-team schedule due [flags]
```

Flags:

```text
      --commands        Print only the due schedule preview command, one per line.
      --format string   Render each due schedule with a Go template, e.g. '{{.Name}} {{.DueReason}}'.
      --json            Emit due schedules as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team schedule fire`

Publish every schedule due now through the daemon.

```text
agent-team schedule fire [flags]
```

Flags:

```text
      --commands                  With --dry-run, print the matching schedule fire apply command when schedules are due.
      --dry-run                   Preview due schedules without publishing events or writing schedule clocks.
      --fail-on-failed            With --wait, exit 1 if any schedule-created job resolves to failed.
      --format string             Render the fire result with a Go template, e.g. '{{.Fired}} {{len .Schedules}}'.
      --json                      Emit fire results as JSON.
      --preview-triggers          With --dry-run, include local topology instance and pipeline matches.
      --repo string               Repo root containing .agent_team. (default "<repo>")
      --wait                      After schedules publish pipeline jobs, wait for those jobs to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. pipeline_step, advance_dispatched, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step for every schedule-created job.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
```

## `agent-team schedule ls`

List declared schedules.

```text
agent-team schedule ls [flags]
```

Flags:

```text
      --format string   Render each schedule with a Go template, e.g. '{{.Name}} {{.Every}}'.
      --json            Emit schedules as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team schedule next`

List declared schedules ordered by next run.

```text
agent-team schedule next [flags]
```

Flags:

```text
      --commands        Print only due schedule preview commands, one per line.
      --format string   Render each forecast row with a Go template, e.g. '{{.Name}} {{.Due}} {{.NextRun}}'.
      --json            Emit schedule forecast rows as JSON.
      --limit int       Show at most this many schedules after ordering; 0 means all.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team schedule run`

Publish one declared schedule event now.

```text
agent-team schedule run <schedule> [flags]
```

Flags:

```text
      --commands                  With --dry-run, print the matching schedule run apply command.
      --dry-run                   Preview the schedule event without publishing it.
      --fail-on-failed            With --wait, exit 1 if any schedule-created job resolves to failed.
      --format string             Render the event result with a Go template, e.g. '{{.Event.Type}} {{.DryRun}}'.
      --json                      Emit the event and outcome as JSON.
      --payload string            Additional JSON object merged into the declared schedule payload.
      --payload-file string       Read additional schedule payload JSON from a file, or '-' for stdin.
      --preview-triggers          With --dry-run, include local topology instance and pipeline matches.
      --repo string               Repo root containing .agent_team. (default "<repo>")
      --wait                      After the schedule publishes pipeline jobs, wait for those jobs to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. pipeline_step, advance_dispatched, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step for every schedule-created job.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
```

## `agent-team schedule show`

Show one declared schedule.

```text
agent-team schedule show <schedule> [flags]
```

Flags:

```text
      --format string   Render the schedule with a Go template, e.g. '{{.Name}} {{.Every}}'.
      --json            Emit the schedule as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team send`

Send a mailbox message to a daemon-managed instance.

Send a direct message through the daemon mailbox. By default the target must already be known to the daemon, which catches typos. Use --allow-missing to intentionally queue a message for a future instance. Use --all, --latest, --last, --agent, --runtime, --status, --phase, --stale, --runtime-stale, or --unhealthy to send the same message to a selected set of daemon-known instances.

```text
agent-team send [<instance>] <message...> [flags]
```

Flags:

```text
      --agent strings         Send to daemon-known instances for this agent. Can repeat or comma-separate.
  -a, --all                   Send to every daemon-known instance.
      --allow-missing         Allow queueing a message for an instance the daemon does not know yet.
      --commands              With --dry-run, print the matching send apply command when the preview has actionable recipients. agent-team follow-ups preserve the selected repo scope.
      --dry-run               Preview matching recipients without appending mailbox messages.
      --force                 With --interrupt, allow fresh fallback when no captured session can be resumed.
      --format string         Render each send result with a Go template, e.g. '{{.To}} {{.ID}}'.
      --from string           Sender label recorded with the message. (default "(cli)")
      --interrupt             Deliver the message, gracefully stop the instance, and managed-resume the same captured session.
      --json                  Emit machine-readable JSON.
  -n, --last int              Send to the N most recently started daemon-known instances after other filters (0 = all).
      --latest                Send to the most recently started daemon-known instance after other filters.
      --message string        Message text to send.
      --message-file string   Read message text from a file, or '-' for stdin.
      --phase strings         Send to daemon-known instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --runtime strings       Send to daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale         Send to daemon-known running instances whose recorded runtime PID is no longer live.
      --stale                 Send to daemon-known instances whose status.toml is stale.
      --status strings        Send to daemon-known instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --target string         Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --unhealthy             Send to daemon-known instances that are crashed, status-stale, or runtime-stale.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team shortcuts`

List command aliases and Docker-like shortcuts.

List command aliases and Docker-like shortcuts from the live command tree. By default this shows top-level shortcuts; use --all to include nested command-group aliases.

```text
agent-team shortcuts [flags]
```

Flags:

```text
      --all             Include nested aliases under command groups.
      --format string   Render each shortcut with a Go template, e.g. '{{.Alias}} -> {{.Command}}'.
      --json            Emit shortcuts as JSON.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team signatures`

Inspect pipeline infra signatures.

Inspect pipeline infra_signatures without writing job state.

```text
agent-team signatures
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team signatures test` - Dry-run a pipeline&#39;s infra signatures against a log file.

## `agent-team signatures test`

Dry-run a pipeline&#39;s infra signatures against a log file.

Dry-run a pipeline&#39;s infra_signatures against a log file. Each signature is reported as match or no-match, and matches include the matched excerpt.

```text
agent-team signatures test <pipeline> [flags]
```

Flags:

```text
      --against string   Log file to test against the pipeline infra signatures.
      --json             Emit signature test results as JSON.
      --repo string      Repo root containing .agent_team. (default "<repo>")
```

## `agent-team snapshot`

Capture a read-only orchestration diagnostic report.

Capture a read-only diagnostic report with health, plan, instance, job, job quarantine, job status preview, outbox, queue, inbox, schedule, runtime, recent lifecycle event state, and command provenance. Use --json for stdout or --output to write a JSON file.

```text
agent-team snapshot [flags]
```

Flags:

```text
      --commands                Print snapshot next-action commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --events int              Recent lifecycle events to include. Use -1 for all events or 0 to skip events. (default 50)
      --events-sort string      Sort included lifecycle events by oldest or newest after applying --events. (default "oldest")
      --format string           Render the snapshot with a Go template, e.g. '{{.Repo}} {{len .Jobs}}'.
      --intake-deliveries int   Recent intake deliveries to include. Use -1 for all deliveries or 0 to skip deliveries. (default 50)
      --json                    Emit the full snapshot JSON to stdout.
      --no-redact               Include raw payload values instead of redacting sensitive keys.
  -o, --output string           Write the full JSON snapshot to this file. Use '-' for stdout.
      --schedule-limit int      Upcoming schedules to include after ordering; 0 means all. (default 10)
      --target string           Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team snapshot diff` - Compare two saved diagnostic snapshots.

## `agent-team snapshot diff`

Compare two saved diagnostic snapshots.

Compare two saved global, team, pipeline, or job diagnostic snapshot JSON files and summarize provenance, git, runtime, health, plan, triage, next-action, follow-up action, instance, job, inbox, outbox, queue, schedule, intake, event, timeline, pipeline, ready-advance, and section-error changes. Use --current-after or --current-before to compare one saved snapshot against the current repo state for the saved snapshot scope.

```text
agent-team snapshot diff <before.json> <after.json> | <snapshot.json> (--current-after|--current-before) [flags]
```

Flags:

```text
      --action strings          Only compare change actions: added, removed, or changed. Can repeat or comma-separate.
      --commands                Print selected added or changed follow-up commands from next/actions diff rows, one per line. agent-team follow-ups preserve the selected repo scope.
      --current-after           Compare the saved snapshot argument against the current repo state for the saved snapshot scope as the after snapshot.
      --current-before          Compare the current repo state for the saved snapshot scope as the before snapshot against the saved snapshot argument.
      --events int              With --current-after/--current-before, recent lifecycle events to include. Use -1 for all events or 0 to skip events. (default 50)
      --events-sort string      With --current-after/--current-before, sort included lifecycle events by oldest or newest after applying --events. (default "oldest")
      --exit-code               Exit with status 1 when snapshots differ.
      --format string           Render the diff result with a Go template, e.g. '{{.Summary.TotalChanges}} {{len .Changes}}'.
      --intake-deliveries int   With --current-after/--current-before, recent intake deliveries to include. Use -1 for all deliveries or 0 to skip deliveries. (default 50)
      --json                    Emit snapshot diff as JSON.
      --limit int               Limit emitted change detail rows after summarizing all changes; 0 means all.
      --no-redact               With --current-after/--current-before, include raw payload values instead of redacting sensitive keys.
  -o, --output string           Write the JSON snapshot diff to this file. Use '-' for stdout.
      --schedule-limit int      With --current-after/--current-before, upcoming schedules to include after ordering; 0 means all. (default 10)
      --section strings         Only compare sections: provenance, git, runtime, health, plan, triage, next, actions, instances, jobs, job_quarantine, pipelines, inbox, outbox, outbox_quarantine, queue, queue_quarantine, schedules, intake, events, timeline, advance, section_errors, quarantine, timelines, commands, pipeline_metrics, ready_advance, or all. Can repeat or comma-separate.
      --sort string             Sort emitted change detail rows by section, action, or id before applying --limit. (default "section")
      --summary                 Only emit metadata and summary counters; suppress change detail rows.
      --timeline string         With --current-after/--current-before on pipeline snapshots, include the last N combined audit/lifecycle timeline rows (0 or all = all). (default "50")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team start`

Start agent-teamd if needed, then start or resume instances.

Docker-like convenience command for the common lifecycle path. It starts the per-repo daemon in the background when needed. With no args, it brings up declared persistent instances from instances.toml; explicit names may also resume daemon-known ad-hoc instances.

```text
agent-team start [<instance>...] [flags]
```

Aliases: `up`

Flags:

```text
      --agent strings            Start or resume every declared persistent and daemon-known instance for this agent. Can repeat or comma-separate.
  -a, --all                      Start or resume every declared persistent and daemon-known instance.
      --attach                   Follow the selected instance log after starting or resuming. Requires exactly one selected instance.
      --commands                 With --dry-run, print the matching apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run                  Preview planned start/resume actions without changing daemon state.
      --format string            Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                     Emit machine-readable JSON.
  -n, --last int                 Start or resume the N most recently started instances after other filters (0 = all).
      --latest                   Start or resume the most recently started instance after other filters.
      --phase strings            Only start or resume instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --prompt string            Override the default kickoff prompt.
      --prompt-file string       Read kickoff prompt from a file, or '-' for stdin.
  -q, --quiet                    Suppress non-error output and use only the exit code.
      --ready-timeout duration   Maximum time to wait for implicit daemon readiness (0 = no timeout). (default 3s)
      --runtime strings          Only start or resume daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale            Only start or resume running instances whose recorded runtime PID is no longer live.
      --stale                    Only start or resume instances whose status.toml is stale.
      --status strings           Only start or resume instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary                  Show aggregate action counts instead of per-instance rows.
      --tail string              With --attach, show only the last N lines before following (0 or all = all). (default "50")
      --target string            Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --timeout duration         Maximum time to wait with --wait (0 = no timeout).
      --unhealthy                Only start or resume instances that are crashed, status-stale, or runtime-stale.
      --wait                     Wait for selected instances to become healthy after starting. With no scoped selection, waits for the fleet.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team stats`

Show CPU and memory usage for daemon-managed instances.

Show a one-shot or watchable resource snapshot for daemon-managed instances. With no names, only running instances are shown. Use --all to include stopped, exited, and crashed instances.

```text
agent-team stats [<instance>...] [flags]
```

Aliases: `top`

Flags:

```text
      --agent strings       Only show instances for this agent. Can repeat or comma-separate.
  -a, --all                 Include stopped, exited, and crashed daemon-managed instances.
      --format string       Render each row with a Go template, e.g. '{{.Instance}} {{.CPUPercent}} {{.RSS}}'.
      --instance strings    Only show instances with this name. Can repeat or comma-separate.
      --interval duration   Refresh interval for --watch. (default 2s)
      --json                Emit JSON. With --watch, writes one JSON array per refresh.
  -n, --last int            Show stats for the N most recently started instances after other filters (0 = all).
      --latest              Show stats for the most recently started instance after other filters.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --phase strings       Only show instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --runtime strings     Only show instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale       Only show running instances whose recorded runtime PID is no longer live.
      --sort string         Sort rows by name, cpu, mem, rss, status, agent, phase, stale, runtime-stale, or unhealthy. (default "name")
      --stale               Only show instances whose status.toml is stale.
      --status strings      Only show lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary             Show aggregate CPU, memory, and RSS totals instead of instance rows.
      --target string       Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --unhealthy           Only show crashed, status-stale, or runtime-stale instances.
  -w, --watch               Refresh stats until interrupted.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team status`

Show daemon health and the current instance table.

```text
agent-team status [flags]
```

Flags:

```text
      --action strings         With --plan, only include plan rows with this action: start, resume, keep, unsupported, on-demand, stop, or extra. Can repeat or comma-separate.
      --agent strings          Only show instances for this agent. Can repeat or comma-separate.
      --commands               Print daemon and health remediation commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --event-action strings   With --events, only include lifecycle events with this action. Can repeat or comma-separate.
      --events int             With --summary, include a summary of the last N matching daemon lifecycle events (0 = omit).
      --format string          Render each instance row with a Go template, e.g. '{{.Instance}} {{.Status}}'.
      --instance strings       Only show instances with this name. Can repeat or comma-separate.
      --interval duration      Refresh interval for --watch. (default 2s)
      --json                   Emit machine-readable JSON.
  -n, --last int               Show only the N most recently started instances after other filters (0 = all).
      --latest                 Show only the most recently started instance after other filters.
      --no-clear               With --watch, append snapshots instead of redrawing the terminal.
      --phase strings          Only show work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --plan                   With --summary, include desired-state action counts from instances.toml and daemon metadata.
      --resources              With --summary, include aggregate CPU, memory, and RSS totals.
      --runtime strings        Only show instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale          Only show running instances whose recorded runtime PID is no longer live.
      --since string           With --events, only include lifecycle events since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --stale                  Only show instances whose status.toml is stale.
      --status strings         Only show lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --stop-extras            With --plan, preview running topology extras as stop actions.
      --strict-topology        With --summary, treat running daemon-known instances not declared in instances.toml as unhealthy.
      --summary                Show a compact non-failing fleet health summary instead of the full instance table.
      --target string          Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --unhealthy              Only show crashed, status-stale, or runtime-stale instances.
  -w, --watch                  Refresh daemon health and instance table until interrupted.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team stop`

Stop running instances.

Docker-like convenience alias for `agent-team instance down`. With no args, stops running declared persistent instances. Use --all to stop every daemon-managed running instance, including ad-hoc and ephemeral work.

```text
agent-team stop [<instance>...] [flags]
```

Aliases: `down`

Flags:

```text
      --agent strings           Stop every running instance for this agent. Can repeat or comma-separate.
  -a, --all                     Stop every daemon-managed running instance.
      --commands                With --dry-run, print the matching apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run                 Preview planned stop actions without changing daemon state.
  -f, --force                   Escalate to SIGKILL if an instance does not stop within --timeout.
      --format string           Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                    Emit machine-readable JSON.
  -n, --last int                Stop the N most recently started running instances after other filters (0 = all).
      --latest                  Stop the most recently started running instance after other filters.
      --phase strings           Stop daemon-known instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
  -q, --quiet                   Suppress non-error output and use only the exit code.
      --rm                      Remove selected instance state and daemon metadata after stopping.
      --runtime strings         Only stop running daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale           Only stop running instances whose recorded runtime PID is no longer live.
      --stale                   Only stop instances whose status.toml is stale.
      --status strings          Stop daemon-known instances currently in this lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary                 Show aggregate action counts instead of per-instance rows.
      --target string           Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --timeout duration        Grace before --force kills. With --wait and no --wait-timeout, also used as the wait deadline (0 = no wait deadline; force defaults to 10s).
      --unhealthy               Only stop instances that are crashed, status-stale, or runtime-stale.
      --wait                    Wait for stopped instances to reach a terminal state.
      --wait-timeout duration   Maximum time to wait for terminal state with --wait. Defaults to --timeout when unset; set 0 explicitly for no wait timeout.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team sync`

Apply topology&#39;s desired persistent instance state.

Reload daemon topology, reconcile runtime metadata, then start or resume declared persistent instances when supported by the runtime. Sync is intentionally non-destructive: daemon-known instances that are not declared in topology are reported by plan but are not stopped or removed unless --stop-extras is set.

```text
agent-team sync [flags]
```

Flags:

```text
      --action strings           Only sync plan rows with this action: start, resume, restart, keep, unsupported, on-demand, stop, or extra. Can repeat or comma-separate.
      --agent strings            Only sync plan rows for this agent. Can repeat or comma-separate.
      --commands                 With --dry-run, print the matching apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run                  Preview topology convergence without starting the daemon or instances.
      --format string            Render each sync action with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --instance strings         Only sync plan rows with this name. Can repeat or comma-separate.
      --json                     Emit machine-readable JSON.
      --phase strings            Only sync plan rows in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
  -q, --quiet                    Suppress non-error output and use only the exit code.
      --ready-timeout duration   Maximum time to wait for implicit daemon readiness (0 = no timeout). (default 3s)
      --runtime strings          Only sync daemon-known plan rows for this runtime: claude or codex. Can repeat or comma-separate.
      --status strings           Only sync plan rows with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --stop-extras              Also stop running daemon-known instances not declared in instances.toml.
      --summary                  Show aggregate action counts instead of per-instance rows.
      --target string            Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --timeout duration         Maximum time to wait with --wait (0 = no timeout).
      --wait                     Wait for selected instances to become healthy after syncing. With no filters, waits for the fleet.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team team`

Inspect declared agent teams.

Inspect team declarations loaded from .agent_team/instances.toml.

```text
agent-team team
```

Aliases: `teams`

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team team adopt` - Adopt a live external process for a team-owned job.
- `agent-team team advance` - Dispatch ready pipeline steps owned by one team.
- `agent-team team approve` - Approve manual pipeline gates owned by one team.
- `agent-team team cancel` - Cancel non-terminal pipeline jobs owned by one team.
- `agent-team team cleanup` - Clean up done jobs owned by one team.
- `agent-team team doctor` - Validate one team&#39;s topology wiring.
- `agent-team team down` - Stop a team&#39;s persistent instances and active ephemeral children.
- `agent-team team drain` - Run one team&#39;s maintenance loop until idle.
- `agent-team team events` - Show lifecycle events scoped to one team.
- `agent-team team explain` - Explain pipeline jobs owned by one team.
- `agent-team team graph` - Render a declared team graph.
- `agent-team team health` - Check health for one declared team.
- `agent-team team hold` - Hold pipeline jobs owned by one team.
- `agent-team team job-events` - Show durable job events for team-owned jobs.
- `agent-team team jobs` - List jobs owned by one team.
- `agent-team team logs` - Show daemon-captured logs for one team.
- `agent-team team ls` - List declared teams.
- `agent-team team monitor` - Show a combined operator snapshot for one team.
- `agent-team team next` - Print recommended next actions scoped to one team.
- `agent-team team outbox` - List or control outbox events scoped to one team.
- `agent-team team overview` - Show a concise operator overview for one declared team.
- `agent-team team pipelines` - List pipeline status for one team.
- `agent-team team plan` - Preview desired lifecycle state for one team.
- `agent-team team prune` - Remove finished team-owned instances.
- `agent-team team ps` - List instances owned by one team.
- `agent-team team queue` - List or control queue items scoped to one team.
- `agent-team team ready` - List ready pipeline jobs owned by one team.
- `agent-team team reject` - Reject manual pipeline gates owned by one team.
- `agent-team team release` - Release held pipeline jobs owned by one team.
- `agent-team team repair` - Recover unhealthy orchestration state for one team.
- `agent-team team restart` - Restart or resume a team&#39;s declared persistent instances.
- `agent-team team resume-plan` - Show runtime resume and fallback commands for one team.
- `agent-team team retry` - Reset failed pipeline steps owned by one team.
- `agent-team team run` - Create a durable job through a team&#39;s pipeline.
- `agent-team team runtime` - Inspect team-owned runtime metadata.
- `agent-team team schedules` - List schedules owned by one team.
- `agent-team team send` - Send a mailbox message to team-owned instances.
- `agent-team team show` - Show one declared team.
- `agent-team team skip` - Mark matching team pipeline steps intentionally skipped.
- `agent-team team snapshot` - Capture a team-scoped diagnostic report.
- `agent-team team stats` - Show CPU and memory usage for team-owned instances.
- `agent-team team status` - Summarize one team&#39;s instances, jobs, and pipelines.
- `agent-team team sync` - Sync one team&#39;s declared persistent instances.
- `agent-team team tick` - Run one team&#39;s orchestration maintenance work.
- `agent-team team timeline` - Show combined job audit and lifecycle timelines for team-owned jobs.
- `agent-team team timeout` - Mark stale running work owned by one team failed.
- `agent-team team triage` - Show team-owned jobs that need operator attention.
- `agent-team team unblock` - Answer blocked pipeline workers owned by one team.
- `agent-team team up` - Start or resume a team&#39;s declared persistent instances.
- `agent-team team wait` - Wait for team-owned instances to reach a lifecycle condition.
- `agent-team team wait-jobs` - Wait for team-owned jobs to reach a lifecycle status, event, or next step.

## `agent-team team adopt`

Adopt a live external process for a team-owned job.

Adopt a live external process into daemon metadata and sync the durable job ownership fields, after verifying the job belongs to the named team.

```text
agent-team team adopt <team> <job-id> [flags]
```

Flags:

```text
      --agent string         Agent name for the adopted instance. Defaults to the selected step target or job target.
      --branch string        Branch name to record. Defaults to the job branch.
      --commands             Print only follow-up commands, one per line, after adoption planning or apply.
      --dry-run              Preview adoption without writing metadata or job state.
      --force                Replace existing live metadata for the instance.
      --format string        Render the adoption result with a Go template, e.g. '{{.Job.ID}} {{.Metadata.Instance}}'.
      --instance string      Instance name that should own the job. Defaults to selected or active step ownership, then job ownership.
      --json                 Emit machine-readable JSON.
      --log-path string      Runtime log path, if the external process already writes to one.
      --pid int              Live process PID to adopt.
      --pid-file string      Read the live process PID to adopt from this file. Cannot be combined with --pid.
      --pr string            PR URL to record. Defaults to the job PR.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --runtime string       Runtime profile for the adopted process (claude or codex). Defaults to repo/env selection.
      --runtime-bin string   Runtime binary or wrapper used by the adopted process.
      --session-id string    Runtime session id, when known and resumable.
      --started-at string    Process start time as RFC3339. Defaults to now, or existing metadata for the same PID.
      --step string          Pipeline step id to mark as owned by the adopted process.
      --workspace string     Workspace path for the adopted process. Defaults to the job worktree, then repo root.
```

## `agent-team team advance`

Dispatch ready pipeline steps owned by one team.

Dispatch or preview ready next steps for jobs in one team&#39;s declared pipelines.

```text
agent-team team advance <team> [flags]
```

Flags:

```text
      --all-ready-steps           Advance every currently ready independent step for each selected team job.
      --commands                  With --dry-run, print the matching team advance apply command when the preview has actionable work.
      --dry-run                   Preview ready steps without dispatching them.
      --fail-on-failed            With --wait, exit 1 if any advanced job resolves to failed.
      --format string             Render each result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.
      --json                      Emit advance results as JSON.
      --limit int                 Advance at most this many ready team jobs, or ready steps with --all-ready-steps; 0 means no limit.
      --preview-routes            With --dry-run, include local topology route and dispatch payload previews.
      --repo string               Repo root containing .agent_team. (default "<repo>")
      --runtime string            Runtime profile for advanced step dispatches (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for advanced step dispatches. Overrides env and repo config.
      --wait                      After advancing, wait for advanced jobs to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step for every advanced job.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
      --workspace string          Workspace mode for advanced steps: auto, worktree, or repo. (default "auto")
```

## `agent-team team approve`

Approve manual pipeline gates owned by one team.

Approve or preview blocked manual-gate steps for jobs in one team&#39;s declared pipelines. Pass --step to target one stage, or --dispatch to immediately dispatch each approved step.

```text
agent-team team approve <team> [flags]
```

Flags:

```text
      --commands                  With --dry-run, print the matching team approve apply command when the preview has actionable work.
      --dispatch                  Dispatch each approved manual gate immediately.
      --dry-run                   Preview manual gate approvals and optional dispatches without writing job or daemon state.
      --fail-on-failed            With --wait, exit 1 if any approved job resolves to failed.
      --format string             Render each result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.
      --json                      Emit approval results as JSON.
      --limit int                 Approve at most this many manual gates; 0 means no limit.
      --message string            Status message recorded on each approved team job.
      --message-file string       Read approval message from a file, or '-' for stdin.
      --preview-routes            With --dry-run --dispatch, include local topology route and dispatch payload previews.
      --repo string               Repo root containing .agent_team. (default "<repo>")
      --runtime string            Runtime profile for --dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for --dispatch. Overrides env and repo config.
      --step string               Approve only manual gates whose next blocked step has this id.
      --wait                      After approving or dispatching, wait for approved jobs to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step for every approved job.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
      --workspace string          Workspace mode for approved dispatches: auto, worktree, or repo. (default "auto")
```

## `agent-team team cancel`

Cancel non-terminal pipeline jobs owned by one team.

Cancel queued, running, or blocked jobs in one team&#39;s declared pipelines by marking the durable job failed with a cancelled audit event. Batch cancellation only updates job files; use job cancel --stop or --kill when an owning instance should also be stopped.

```text
agent-team team cancel <team> [flags]
```

Flags:

```text
      --actor string          Actor label recorded in cancellation audit events. (default "cli")
      --commands              With --dry-run, print the matching team cancel apply command when the preview has actionable work.
      --dry-run               Preview team cancellations without writing job state.
      --format string         Render each result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StatusAfter}}'.
      --json                  Emit cancellation results as JSON.
      --limit int             Cancel at most this many non-terminal team jobs; 0 means no limit.
      --message string        Cancellation reason recorded on each cancelled team job.
      --message-file string   Read cancellation reason from a file, or '-' for stdin.
      --repo string           Repo root containing .agent_team. (default "<repo>")
```

## `agent-team team cleanup`

Clean up done jobs owned by one team.

Preview or remove job-owned worktrees and branches for done jobs owned by one declared team. Applying cleanup requires --merged after confirming the matching PRs have merged.

```text
agent-team team cleanup <team> [flags]
```

Flags:

```text
      --commands        With --dry-run, print the matching team cleanup apply command when the preview has actionable work.
      --dry-run         Preview done team-owned job cleanup without removing anything.
      --force-branch    With --merged, delete job branches with git branch -D if they are not locally merged.
      --format string   Render the cleanup batch with a Go template, e.g. '{{.Team}} {{.Cleaned}} {{.Failed}}'.
      --json            Emit the cleanup batch as JSON.
      --merged          Confirm the team's matching done-job PRs have merged before removing worktrees and branches.
      --repo string     Repo root containing .agent_team. (default "<repo>")
      --verify-pr       Verify recorded GitHub PRs are merged with gh before cleanup.
```

## `agent-team team doctor`

Validate one team&#39;s topology wiring.

Validate a declared team&#39;s topology wiring: team-owned pipeline workflows must be runnable, pipeline step targets must be owned by the team, and team schedules should route back to team-owned instances or pipelines.

```text
agent-team team doctor <team>|--all [flags]
```

Flags:

```text
      --all              Validate all declared teams.
      --commands         Print recommended follow-up commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string    Render the team doctor result with a Go template, e.g. '{{.OK}} {{len .Problems}}'.
      --json             Emit team doctor findings as JSON.
      --repo string      Repo root containing .agent_team. (default "<repo>")
      --strict           Fail on all strict team doctor checks. Currently aliases --strict-runtime.
      --strict-runtime   Fail when a team-owned step or target-agent runtime default cannot be resolved or is not discoverable.
      --target string    Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

## `agent-team team down`

Stop a team&#39;s persistent instances and active ephemeral children.

```text
agent-team team down <team> [flags]
```

Aliases: `stop`

Flags:

```text
      --commands                With --dry-run, print the matching team down apply command when the preview has actionable work.
      --dry-run                 Preview planned stop actions without changing daemon state.
  -f, --force                   Escalate to SIGKILL if an instance does not stop within --timeout.
      --format string           Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                    Emit machine-readable JSON.
  -q, --quiet                   Suppress non-error output and use only the exit code.
      --repo string             Repo root containing .agent_team. (default "<repo>")
      --rm                      Remove selected instance state and daemon metadata after stopping.
      --runtime strings         Only target team-owned daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.
      --summary                 Show aggregate action counts instead of per-instance rows.
      --timeout duration        Grace before --force kills. With --wait and no --wait-timeout, also used as the wait deadline (0 = no wait deadline; force defaults to 10s).
      --wait                    Wait for stopped instances to reach a terminal state.
      --wait-timeout duration   Maximum time to wait for terminal state with --wait. Defaults to --timeout when unset; set 0 explicitly for no wait timeout.
```

## `agent-team team drain`

Run one team&#39;s maintenance loop until idle.

Run scoped team ticks until no immediate team schedule, queue, or pipeline work remains.

```text
agent-team team drain <team> [flags]
```

Flags:

```text
      --all-ready-steps           Advance every currently ready independent team pipeline step in each drain cycle.
      --fail-on-failed            With --wait, exit 1 if any team drain-advanced job resolves to failed.
      --format string             Render the drain result with a Go template, e.g. '{{.Team.Name}} {{.CyclesRun}} {{.Idle}}'.
      --interval duration         Delay between drain cycles. (default 2s)
      --json                      Emit machine-readable JSON.
      --limit int                 Advance at most this many ready pipeline jobs per cycle, or ready steps with --all-ready-steps; 0 means no limit.
      --max-cycles int            Stop after this many cycles if work keeps appearing. (default 20)
      --repo string               Repo root containing .agent_team. (default "<repo>")
      --runtime string            Runtime profile for advanced step dispatches (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for advanced step dispatches. Overrides env and repo config.
      --skip-advance              Skip pipeline advancement work.
      --skip-drain                Skip queue drain work.
      --skip-schedules            Skip due schedule work.
      --wait                      After team drain reaches idle, wait for jobs advanced during team drain cycles to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step for every drain-advanced job.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
      --workspace string          Workspace mode for advanced pipeline steps: auto, worktree, or repo. (default "auto")
```

## `agent-team team events`

Show lifecycle events scoped to one team.

Show or follow daemon lifecycle events for one declared team, including ephemeral children owned by that team.

```text
agent-team team events <team> [flags]
```

Flags:

```text
      --action strings    Only show events with this action. Can repeat or comma-separate.
  -f, --follow            Keep streaming new lifecycle events.
      --format string     Render each event with a Go template, e.g. '{{.Job}} {{.Action}} {{.Instance}} {{.Status}}'.
      --job strings       Only show events for this team-owned job id or ticket. Can repeat or comma-separate.
      --json              Emit raw JSONL events.
      --phase strings     Only show team events for instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --repo string       Repo root containing .agent_team. (default "<repo>")
      --runtime strings   Only show team events for daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale     Only show team events for instances whose recorded runtime PID is currently no longer live.
      --since string      Only show events since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --sort string       Sort returned events by oldest or newest. Follow mode always streams oldest first. (default "oldest")
      --stale             Only show team events for instances whose status.toml is currently stale or missing.
      --status strings    Only show events with this lifecycle status. Can repeat or comma-separate.
      --step string       Only show events for instances recorded on this pipeline step id.
      --summary           Summarize matching team events by action, status, agent, and instance.
      --tail int          Show only the last N matching team events before returning or following (0 = all).
      --unhealthy         Only show team events for instances that are currently crashed, status-stale, or runtime-stale.
```

## `agent-team team explain`

Explain pipeline jobs owned by one team.

Explain team-owned pipeline state from durable jobs, expanding each matching job with step readiness, dependency blockers, gates, active instances, and suggested next actions.

```text
agent-team team explain <team> [flags]
```

Flags:

```text
      --commands            Print recommended commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string       Render each pipeline explanation with a Go template, e.g. '{{.Pipeline}} {{len .Jobs}}'.
      --interval duration   Refresh interval for --watch. (default 2s)
      --json                Emit team pipeline explanations as JSON.
      --limit int           Limit job explanations per team-owned pipeline; 0 means no limit.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --repo string         Repo root containing .agent_team. (default "<repo>")
      --sort string         Sort job explanations before applying --limit by job, state, step, target, updated, created, ticket, instance, or label. (default "updated")
      --state strings       Only explain jobs whose next-step state matches: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --step string         Only include jobs and step details for this pipeline step id.
  -w, --watch               Refresh team pipeline explanations until interrupted.
```

## `agent-team team graph`

Render a declared team graph.

Render a read-only graph of one declared team&#39;s instances, pipelines, schedules, and step dispatch wiring.

```text
agent-team team graph <team> [flags]
```

Flags:

```text
      --commands        Print recommended commands from graph action hints, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string   Graph output format: text, mermaid, or dot. (default "text")
      --job string      Overlay durable job step state on a team-owned pipeline graph.
      --json            Emit graph nodes and edges as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
      --routes          Annotate pipeline steps with matching agent.dispatch routes.
```

## `agent-team team health`

Check health for one declared team.

```text
agent-team team health <team> [flags]
```

Flags:

```text
      --commands          Print issue remediation commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --fallbacks         When runtime recovery actions use resume-plan, recommend command-mode fallback expansion.
      --format string     Render team health with a Go template, e.g. '{{.Team.Name}} {{.Health.Healthy}}'.
      --jobs              Include team-owned job and pipeline health.
      --json              Emit team health as JSON.
      --last-message      When runtime recovery actions use resume-plan log fallbacks, prefer clean Codex final-message commands.
  -q, --quiet             Suppress output and use only the exit code.
      --repo string       Repo root containing .agent_team. (default "<repo>")
      --runtime strings   Only check team-owned daemon-known instances for this runtime: claude or codex. Daemon, queue, and job health remain team-scoped. Can repeat or comma-separate.
      --runtime-stale     Only check team-owned running instances whose recorded runtime PID is no longer live. Daemon, queue, and job health remain team-scoped.
```

## `agent-team team hold`

Hold pipeline jobs owned by one team.

Hold matching jobs in pipelines declared on one team without changing their lifecycle status.

```text
agent-team team hold <team> [reason...] [flags]
```

Aliases: `pause`

Flags:

```text
      --commands              With --dry-run, print the matching hold apply command when the preview has actionable work.
      --dry-run               Preview holds without writing job state.
      --for duration          Hold for this duration, for example 30m or 2h.
      --format string         Render each hold result with a Go template, e.g. '{{.JobID}} {{.Action}}'.
      --json                  Emit hold results as JSON.
      --limit int             Hold at most this many matching team jobs; 0 means no limit.
      --message string        Hold reason recorded on each team job.
      --message-file string   Read hold reason from a file, or '-' for stdin.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --state strings         Next-step state to hold: ready, queued, running, blocked, failed, held, done, none, or all. Defaults to active non-held, non-done jobs.
      --until string          Hold until this RFC3339 timestamp.
```

## `agent-team team job-events`

Show durable job events for team-owned jobs.

Show durable job audit events for jobs owned by one declared team.

```text
agent-team team job-events <team> [flags]
```

Flags:

```text
      --actor strings       Only show job events from this actor. Can repeat or comma-separate.
  -f, --follow              Poll and print new team job events until interrupted.
      --format string       Render each job event with a Go template, e.g. '{{.JobID}} {{.Type}} {{.Status}}'.
      --instance strings    Only show job events for this owning instance. Can repeat or comma-separate.
      --interval duration   Polling interval for --follow. (default 1s)
      --json                Emit matching job events as JSON.
      --repo string         Repo root containing .agent_team. (default "<repo>")
      --since string        Only show job events since this duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --sort string         Sort returned events by oldest or newest. Follow mode always streams oldest first. (default "oldest")
      --status strings      Only show job events with this status: queued, running, blocked, done, or failed. Can repeat or comma-separate.
      --summary             Summarize matching job events by job, type, status, actor, and instance.
      --tail string         Show only the last N matching events after combining team jobs (0 or all = all). (default "0")
      --type strings        Only show job events with this type. Can repeat or comma-separate.
```

## `agent-team team jobs`

List jobs owned by one team.

```text
agent-team team jobs <team> [flags]
```

Flags:

```text
      --active-hold           Only show held jobs whose hold is still active or has no deadline.
      --branch string         Only show jobs owning this branch.
      --commands              Print recommended follow-up commands from the visible team job rows. agent-team follow-ups preserve the selected repo scope.
      --expired-hold          Only show held jobs whose hold_until has passed.
      --format string         Render each job with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --held                  Only show held jobs.
      --instance string       Only show jobs owned by this instance.
      --interval duration     Refresh interval for --watch. (default 2s)
      --json                  Emit team jobs as JSON.
      --limit int             Limit rows after filtering and sorting; 0 means no limit.
      --no-clear              With --watch, append snapshots instead of redrawing the terminal.
      --pr string             Only show jobs whose PR URL contains this value.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --runtime strings       Only show team-owned jobs whose instance metadata has this runtime: claude or codex. Can repeat or comma-separate.
      --sort string           Sort jobs by id, status, target, ticket, created, updated, instance, runtime, branch, or pr. (default "id")
      --status string         Filter by job status: queued, running, blocked, done, or failed.
      --summary               Show aggregate team job counts instead of job rows.
      --target-agent string   Only show jobs targeting this agent.
      --ticket string         Only show jobs whose ticket id or URL contains this value.
      --unheld                Only show jobs that are not held.
  -w, --watch                 Refresh team jobs until interrupted.
```

## `agent-team team logs`

Show daemon-captured logs for one team.

```text
agent-team team logs <team> [flags]
```

Flags:

```text
      --clean             Hide known Codex runtime diagnostic noise before printing team logs.
  -f, --follow            Tail selected team logs.
      --format string     With --list, render each log stream with a Go template, e.g. '{{.Instance}} {{.LogPath}}'.
      --grep string       Only print log lines matching this regular expression. One-shot reads only.
      --job strings       Only show logs for this team-owned job id or ticket. Can repeat or comma-separate.
      --json              Emit machine-readable JSON with --list.
  -n, --last int          Show logs for the N most recently started team instances (0 = all).
      --last-message      Show clean final Codex response sidecars instead of raw runtime logs.
      --latest            Show the most recently started team instance log.
      --list              List team log streams instead of printing log content.
      --no-prefix         Do not prefix lines when streaming multiple team logs.
      --phase strings     Only show logs for work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --raw               Print unprocessed team logs without Codex JSONL rendering.
      --repo string       Repo root containing .agent_team. (default "<repo>")
      --runtime strings   Only show logs for team-owned instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale     Only show logs for team instances whose recorded runtime PID is no longer live.
      --since string      Only include log streams modified since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --stale             Only show logs for team instances whose status.toml is stale.
      --status strings    Only show logs for lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --step string       Only show logs for instances recorded on this pipeline step id.
      --tail string       Show only the last N lines before returning or following (0 or all = all). (default "0")
      --unhealthy         Only show logs for crashed, status-stale, or runtime-stale team instances.
```

## `agent-team team ls`

List declared teams.

```text
agent-team team ls [flags]
```

Flags:

```text
      --json          Emit teams as JSON.
      --repo string   Repo root containing .agent_team. (default "<repo>")
```

## `agent-team team monitor`

Show a combined operator snapshot for one team.

Show a Docker-style operator snapshot scoped to one declared team, combining team health, team-owned queue and outbox recovery signals, inbox state, instance rows, daemon-managed process stats, and optional plan, job, schedule, and lifecycle event sections.

```text
agent-team team monitor <team> [flags]
```

Aliases: `watch`

Flags:

```text
      --action strings         With --plan, only show plan rows with this action: start, resume, keep, unsupported, on-demand, stop, or extra. Can repeat or comma-separate.
      --agent strings          Only show team-owned instances, stats, and plan rows for this agent. Can repeat or comma-separate.
  -a, --all                    Include stopped, exited, crashed, and missing team-owned instances in the stats section.
      --commands               Print recovery and apply commands from the visible team monitor sections, one per line. agent-team follow-ups preserve the selected repo scope.
      --event-action strings   With --events, only show lifecycle events with this action. Can repeat or comma-separate.
      --events int             Include the last N matching team lifecycle events in the full monitor (0 = omit).
      --events-sort string     Sort the visible --events section by oldest or newest. (default "oldest")
      --fallbacks              When runtime recovery actions use resume-plan, recommend command-mode fallback expansion.
      --format string          Render team monitor snapshots with a Go template, e.g. '{{.Team.Name}} {{len .Instances}}'.
      --instance strings       Only show team-owned instances with this name. Can repeat or comma-separate.
      --interval duration      Refresh interval for --watch. (default 2s)
      --jobs                   Include team-owned durable job summary, attention, ready-step state, and status-file previews.
      --json                   Emit JSON. With --watch, writes one JSON object per refresh.
  -n, --last int               Show only the N most recently started team-owned instances after other filters (0 = all).
      --last-message           When runtime recovery actions use resume-plan log fallbacks, prefer clean Codex final-message commands.
      --latest                 Show only the most recently started team-owned instance after other filters.
      --no-clear               With --watch, append snapshots instead of redrawing the terminal.
      --phase strings          Only show team-owned instances and stats in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --plan                   Include team-scoped desired-state actions from instances.toml and daemon metadata.
      --repo string            Repo root containing .agent_team. (default "<repo>")
      --resources              With --summary, include aggregate CPU, memory, and RSS totals for team-owned instances.
      --runtime strings        Only show team-owned instances for this runtime in instance, stats, and plan sections: claude or codex. Can repeat or comma-separate.
      --runtime-stale          Only show team-owned running instances whose recorded runtime PID is no longer live.
      --schedules              Include due and upcoming team schedules.
      --since string           With --events, only show lifecycle events since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --sort string            Sort instance rows by name, status, agent, phase, stale, runtime-stale, unhealthy, started, stopped, or exited. (default "name")
      --stale                  Only show team-owned instances whose status.toml is stale.
      --stats-sort string      Sort stats rows by name, cpu, mem, rss, status, agent, phase, stale, runtime-stale, or unhealthy. (default "name")
      --status strings         Only show team-owned lifecycle status in instance, stats, and plan sections: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --stop-extras            With --plan, preview running team-agent extras as stop actions.
      --summary                Show compact team health and optional plan summaries instead of the full team monitor.
      --unhealthy              Only show crashed, status-stale, or runtime-stale team-owned instances.
  -w, --watch                  Refresh the team monitor snapshot until interrupted.
```

## `agent-team team next`

Print recommended next actions scoped to one team.

Print recommended next operator actions from the read-only team overview.

```text
agent-team team next <team> [flags]
```

Flags:

```text
      --commands             Print only recommended commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --details              Include source and reason metadata in text output.
      --fallbacks            When runtime recovery actions use resume-plan, recommend command-mode fallback expansion.
      --format string        Render the next-action result with a Go template, e.g. '{{.Team.Name}} {{len .Actions}}'.
      --interval duration    Refresh interval for --watch. (default 2s)
      --json                 Emit recommended actions as JSON.
      --last-message         When runtime recovery actions use resume-plan log fallbacks, prefer clean Codex final-message commands.
      --limit int            Show at most this many actions; 0 means all.
      --no-clear             With --watch, append snapshots instead of redrawing the terminal.
      --reason strings       Only show actions with this reason. Values match exactly, or as prefixes before '='. Queue/job/outbox quarantine aliases are supported. Can repeat or comma-separate.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --schedule-limit int   Upcoming schedules to inspect while building recommendations; 0 means all. (default 5)
      --sort string          Sort actions before applying --limit by default, source, reason, or command. (default "default")
      --source strings       Only show actions from this source: health, topology, runtime, inbox, outbox, queue, jobs, pipelines, schedules, intake, section_errors, or overview. Can repeat or comma-separate.
  -w, --watch                Refresh recommended actions until interrupted.
```

## `agent-team team outbox`

List or control outbox events scoped to one team.

```text
agent-team team outbox <team> [flags]
```

Flags:

```text
      --commands            Print recommended commands from the visible team-owned outbox rows, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string       Render each team-owned outbox item with a Go template, e.g. '{{.ID}} {{.State}}'.
      --interval duration   Refresh interval for --watch. (default 2s)
      --job strings         Filter by job id or ticket; repeat or comma-separate values.
      --json                Emit team-owned outbox rows as JSON.
      --limit int           Limit rows after filtering and sorting; 0 means no limit.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --repo string         Repo root containing .agent_team. (default "<repo>")
      --sort string         Sort rows by state, id, type, source, job, created, updated, or error. (default "state")
      --source strings      Filter by source agent/instance; repeat or comma-separate values.
      --state string        Filter by outbox state: pending, processed, or failed.
      --summary             Show aggregate outbox counts instead of rows.
      --type strings        Filter by event type; repeat or comma-separate values.
  -w, --watch               Refresh the team outbox table until interrupted.
```

Subcommands:

- `agent-team team outbox drop` - Drop outbox events owned by one team.
- `agent-team team outbox prune` - Prune old outbox events owned by one team.
- `agent-team team outbox quarantine` - List team-owned quarantined outbox files.
- `agent-team team outbox retry` - Retry outbox events owned by one team.
- `agent-team team outbox show` - Show one outbox event owned by one team.

## `agent-team team outbox drop`

Drop outbox events owned by one team.

Remove one team-owned outbox event by id, or drop a filtered team-owned batch with --all. Batch drops default to failed events.

```text
agent-team team outbox drop <team> [id] [flags]
```

Flags:

```text
      --all              Drop all matching team-owned outbox events instead of one id.
      --commands         With --dry-run, print the matching team outbox drop apply command when the preview has actionable work.
      --dry-run          Preview the drop without removing the event.
      --format string    Render the drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings      With --all, filter by job id or ticket; repeat or comma-separate values.
      --json             Emit machine-readable JSON.
      --limit int        With --all, drop at most this many matching outbox events; 0 means no limit.
      --repo string      Repo root containing .agent_team. (default "<repo>")
      --sort string      With --all, sort matching outbox events before limiting: state, id, type, source, job, created, updated, or error. (default "state")
      --source strings   With --all, filter by source agent/instance; repeat or comma-separate values.
      --state string     With --all, filter by outbox state: pending, processed, or failed. Defaults to failed.
      --type strings     With --all, filter by event type; repeat or comma-separate values.
```

## `agent-team team outbox prune`

Prune old outbox events owned by one team.

Prune old sandboxed agent outbox events owned by one team. By default this removes processed events; pass --state failed, pending, or all for explicit cleanup.

```text
agent-team team outbox prune <team> [flags]
```

Flags:

```text
      --commands              With --dry-run, print the matching team outbox prune apply command when the preview has actionable work.
      --dry-run               Preview team-owned outbox events that would be pruned without dropping them.
      --format string         Render each prune result with a Go template, e.g. '{{.ID}} {{.Dropped}}'.
      --job strings           Filter by job id or ticket before pruning; repeat or comma-separate values.
      --json                  Emit prune results as JSON.
      --limit int             Prune at most this many matching team-owned outbox events; 0 means no limit.
      --older-than duration   Only prune items older than this duration based on processed/failed/update/create time.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --source strings        Filter by source agent/instance before pruning; repeat or comma-separate values.
      --state string          Outbox state to prune: processed, failed, pending, or all. (default "processed")
      --type strings          Filter by event type before pruning; repeat or comma-separate values.
```

## `agent-team team outbox quarantine`

List team-owned quarantined outbox files.

List quarantined sandboxed agent outbox files owned by one declared team.

```text
agent-team team outbox quarantine <team> [flags]
```

Flags:

```text
      --commands         Print recommended commands from the visible team-owned quarantined outbox files, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string    Render each team-owned quarantined outbox file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.
      --job strings      Filter by job id or ticket; repeat or comma-separate values.
      --json             Emit team-owned quarantined outbox files as JSON.
      --limit int        Limit rows after filtering and sorting; 0 means no limit.
      --repo string      Repo root containing .agent_team. (default "<repo>")
      --restorable       Only show quarantined files that can be restored.
      --sort string      Sort rows by path, state, id, type, source, job, created, updated, modified, restorable, or size. (default "path")
      --source strings   Filter by source agent/instance; repeat or comma-separate values.
      --state string     Filter by outbox state: pending, processed, or failed.
      --summary          Show aggregate team-owned quarantined outbox-file counts instead of rows.
      --type strings     Filter by event type; repeat or comma-separate values.
      --unrestorable     Only show quarantined files that cannot be restored.
```

Subcommands:

- `agent-team team outbox quarantine drop` - Drop team-owned quarantined outbox files after inspection.
- `agent-team team outbox quarantine restore` - Restore team-owned quarantined outbox files.
- `agent-team team outbox quarantine show` - Show one team-owned quarantined outbox file.

## `agent-team team outbox quarantine drop`

Drop team-owned quarantined outbox files after inspection.

Drop one team-owned quarantined outbox file by path, or drop a filtered team-owned batch with --all.

```text
agent-team team outbox quarantine drop <team> [quarantine-path] [flags]
```

Flags:

```text
      --all                   Drop all matching team-owned quarantined files instead of one path.
      --commands              With --dry-run, print the matching team outbox quarantine drop apply command when the preview has actionable work.
      --dry-run               Preview quarantined files that would be dropped.
      --format string         Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings           With --all, filter by job id or ticket; repeat or comma-separate values.
      --json                  Emit drop results as JSON.
      --limit int             With --all, drop at most this many matching team-owned quarantined files; 0 means no limit.
      --older-than duration   With --all, only drop files older than this duration based on file mtime.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --restorable            With --all, only drop quarantined files that can be restored.
      --sort string           With --all, sort matching team-owned quarantined files before limiting: path, state, id, type, source, job, created, updated, modified, restorable, or size. (default "path")
      --source strings        With --all, filter by source agent/instance; repeat or comma-separate values.
      --state string          With --all, filter by outbox state: pending, processed, or failed.
      --type strings          With --all, filter by event type; repeat or comma-separate values.
      --unrestorable          With --all, only drop quarantined files that cannot be restored.
```

## `agent-team team outbox quarantine restore`

Restore team-owned quarantined outbox files.

Restore one team-owned quarantined outbox file by path, or restore a filtered team-owned batch of restorable files with --all.

```text
agent-team team outbox quarantine restore <team> [quarantine-path] [flags]
```

Flags:

```text
      --all              Restore all matching team-owned restorable quarantined files instead of one path.
      --commands         With --dry-run, print the matching team outbox quarantine restore apply command when the preview has actionable work.
      --dry-run          Preview the restore without moving files.
      --force            Overwrite an existing active outbox file with the same restore path.
      --format string    Render each restore result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings      With --all, filter by job id or ticket; repeat or comma-separate values.
      --json             Emit restore result as JSON.
      --limit int        With --all, restore at most this many matching team-owned quarantined files; 0 means no limit.
      --repo string      Repo root containing .agent_team. (default "<repo>")
      --sort string      With --all, sort matching team-owned quarantined files before limiting: path, state, id, type, source, job, created, updated, modified, restorable, or size. (default "path")
      --source strings   With --all, filter by source agent/instance; repeat or comma-separate values.
      --state string     With --all, filter by outbox state: pending, processed, or failed.
      --type strings     With --all, filter by event type; repeat or comma-separate values.
```

## `agent-team team outbox quarantine show`

Show one team-owned quarantined outbox file.

```text
agent-team team outbox quarantine show <team> <quarantine-path> [flags]
```

Flags:

```text
      --commands        Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.
      --format string   Render the team-owned quarantined outbox file with a Go template, e.g. '{{.ID}} {{.State}}'.
      --json            Emit the team-owned quarantined outbox file as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team team outbox retry`

Retry outbox events owned by one team.

Move one team-owned processed or failed outbox event back to pending by id, or retry a filtered team-owned batch with --all. Batch retries default to failed events.

```text
agent-team team outbox retry <team> [id] [flags]
```

Aliases: `requeue`

Flags:

```text
      --all              Retry all matching team-owned outbox events instead of one id.
      --commands         With --dry-run, print the matching team outbox retry apply command when the preview has actionable work.
      --dry-run          Preview the retry without moving the event.
      --format string    Render the retry result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings      With --all, filter by job id or ticket; repeat or comma-separate values.
      --json             Emit machine-readable JSON.
      --limit int        With --all, retry at most this many matching outbox events; 0 means no limit.
      --repo string      Repo root containing .agent_team. (default "<repo>")
      --sort string      With --all, sort matching outbox events before limiting: state, id, type, source, job, created, updated, or error. (default "state")
      --source strings   With --all, filter by source agent/instance; repeat or comma-separate values.
      --state string     With --all, filter by outbox state: pending, processed, or failed. Defaults to failed.
      --type strings     With --all, filter by event type; repeat or comma-separate values.
```

## `agent-team team outbox show`

Show one outbox event owned by one team.

```text
agent-team team outbox show <team> <id> [flags]
```

Flags:

```text
      --commands        Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.
      --format string   Render the team-owned outbox item with a Go template, e.g. '{{.ID}} {{.State}}'.
      --json            Emit the team-owned outbox item as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team team overview`

Show a concise operator overview for one declared team.

Show a read-only operator overview scoped to one declared team with health, topology, job, queue, pipeline, schedule, and recommended next-action summaries.

```text
agent-team team overview <team> [flags]
```

Flags:

```text
      --commands             Print recommended team actions, one per line. agent-team follow-ups preserve the selected repo scope.
      --fallbacks            When runtime recovery actions use resume-plan, recommend command-mode fallback expansion.
      --format string        Render the team overview result with a Go template, e.g. '{{.Team.Name}} {{.State}}'.
      --interval duration    Refresh interval for --watch. (default 2s)
      --json                 Emit team overview as JSON.
      --last-message         When runtime recovery actions use resume-plan log fallbacks, prefer clean Codex final-message commands.
      --limit int            Show at most this many action recommendations after filtering and sorting; 0 means all.
      --no-clear             With --watch, append snapshots instead of redrawing the terminal.
      --reason strings       Only keep action recommendations with this reason. Values match exactly, or as prefixes before '='. Queue/job/outbox quarantine aliases are supported. Can repeat or comma-separate.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --schedule-limit int   Upcoming team schedules to inspect after ordering; 0 means all. (default 5)
      --sort string          Sort action recommendations before applying --limit by default, source, reason, or command. (default "default")
      --source strings       Only keep action recommendations from this source: health, topology, runtime, inbox, outbox, queue, jobs, pipelines, schedules, intake, section_errors, or overview. Can repeat or comma-separate.
  -w, --watch                Refresh team overview until interrupted.
```

## `agent-team team pipelines`

List pipeline status for one team.

```text
agent-team team pipelines <team> [flags]
```

Flags:

```text
      --commands            Print recommended actions, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string       Render each pipeline with a Go template, e.g. '{{.Pipeline}} {{.ReadySteps}}'.
      --interval duration   Refresh interval for --watch. (default 2s)
      --json                Emit team pipeline status as JSON.
      --limit int           Limit rows after sorting; 0 means no limit.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --repo string         Repo root containing .agent_team. (default "<repo>")
      --sort string         Sort rows by declared, pipeline, steps, jobs, queued, running, blocked, done, failed, ready, stale, manual, held, none, queue, queue-pending, queue-dead, queue-quarantined, quarantined, outbox, outbox-pending, outbox-failed, outbox-processed, or outbox-quarantined. (default "declared")
  -w, --watch               Refresh the team pipeline status table until interrupted.
```

## `agent-team team plan`

Preview desired lifecycle state for one team.

```text
agent-team team plan <team> [flags]
```

Flags:

```text
      --action strings    Only show plan rows with this action: start, resume, keep, unsupported, on-demand, stop, or extra. Can repeat or comma-separate.
      --commands          Print the matching dry-run team sync command when the plan has actionable work.
      --format string     Render each plan row with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json              Emit team plan as JSON.
      --repo string       Repo root containing .agent_team. (default "<repo>")
      --runtime strings   Only show team-owned daemon-known plan rows for this runtime: claude or codex. Can repeat or comma-separate.
      --stop-extras       Preview running team-agent topology extras as stop actions.
      --summary           Show aggregate action counts instead of per-instance rows.
```

## `agent-team team prune`

Remove finished team-owned instances.

Remove daemon-known exited or crashed instances owned by one declared team. Running and stopped instances are intentionally left alone unless selected by --runtime-stale or --unhealthy.

```text
agent-team team prune <team> [flags]
```

Flags:

```text
      --commands              With --dry-run, print the matching team prune apply command when the preview has actionable work.
      --dry-run               Preview matching team-owned instances that would be pruned without deleting state or daemon metadata.
      --format string         Render each removal result with a Go template, e.g. '{{.Instance}} {{.Path}}'.
      --json                  Emit machine-readable JSON.
      --older-than duration   Only prune finished team-owned instances whose terminal timestamp is older than this duration (for example 24h).
      --phase strings         Only remove finished team-owned instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
  -q, --quiet                 Suppress non-error output and use only the exit code.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --runtime strings       Only remove matching team-owned instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale         Also remove team-owned running instances whose recorded runtime PID is no longer live.
      --stale                 Only remove finished team-owned instances whose non-idle work phase has stale status telemetry.
      --status strings        Only remove finished team-owned instances in this lifecycle status: exited or crashed. Can repeat or comma-separate.
      --summary               Show aggregate removal counts instead of per-instance rows.
      --unhealthy             Only remove crashed finished team-owned instances, finished status-stale instances, or runtime-stale running instances.
```

## `agent-team team ps`

List instances owned by one team.

```text
agent-team team ps <team> [flags]
```

Aliases: `instances`

Flags:

```text
      --agent strings       Only show team-owned instances for this agent. Can repeat or comma-separate.
      --format string       Render each team instance with a Go template, e.g. '{{.Instance}} {{.Status}}'.
      --instance strings    Only show team-owned instances with this name. Can repeat or comma-separate.
      --interval duration   Refresh interval for --watch. (default 2s)
      --json                Emit team instances as JSON.
  -n, --last int            Show only the N most recently started team-owned instances after other filters (0 = all).
  -l, --latest              Show only the most recently started team-owned instance after other filters.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --phase strings       Only show team-owned work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --repo string         Repo root containing .agent_team. (default "<repo>")
      --runtime strings     Only show team-owned instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale       Only show team-owned running instances whose recorded runtime PID is no longer live.
      --sort string         Sort team instance rows by name, status, agent, phase, stale, runtime-stale, unhealthy, started, stopped, or exited. (default "name")
      --stale               Only show team-owned instances whose status.toml is stale.
      --status strings      Only show team-owned lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary             Show lifecycle counts instead of team instance rows.
      --unhealthy           Only show crashed, status-stale, or runtime-stale team-owned instances.
  -w, --watch               Refresh team instances until interrupted.
```

## `agent-team team queue`

List or control queue items scoped to one team.

```text
agent-team team queue <team> [flags]
```

Flags:

```text
      --commands             Print recommended commands from the visible team queue rows, one per line. agent-team follow-ups preserve the selected repo scope.
      --event-type strings   Filter by event type; repeat or comma-separate values.
      --format string        Render each queue item with a Go template, e.g. '{{.ID}} {{.State}}'.
      --interval duration    Refresh interval for --watch. (default 2s)
      --job strings          Filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit team queue rows as JSON.
      --limit int            Limit rows after filtering and sorting; 0 means no limit.
      --no-clear             With --watch, append snapshots instead of redrawing the terminal.
      --ready                Only show pending queue items whose next retry is due now.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --runtime strings      Filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.
      --sort string          Sort rows by state, id, event, instance, job, runtime, queued, updated, next-retry, or attempts. (default "state")
      --state string         Filter by queue state: pending or dead.
      --summary              Show aggregate queue counts instead of queue rows.
  -w, --watch                Refresh the team queue table until interrupted.
```

Subcommands:

- `agent-team team queue drop` - Drop team-owned queue items.
- `agent-team team queue prune` - Prune team-owned queue items.
- `agent-team team queue quarantine` - List quarantined queue files scoped to one team.
- `agent-team team queue retry` - Retry team-owned queue items.
- `agent-team team queue show` - Show one queue item owned by one team.

## `agent-team team queue drop`

Drop team-owned queue items.

Drop one team-owned queue item by id, or drop a filtered team-owned batch with --all. Batch drops default to dead-letter items.

```text
agent-team team queue drop <team> [id] [flags]
```

Flags:

```text
      --all                  Drop all matching team-owned queue items instead of one id.
      --commands             With --dry-run, print the matching team queue drop command when the preview has actionable work.
      --dry-run              Preview matching team-owned queue items without dropping them.
      --event-type strings   With --all, filter by event type; repeat or comma-separate values.
      --format string        Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings          With --all, filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit machine-readable JSON.
      --limit int            With --all, drop at most this many matching queue items; 0 means no limit.
      --ready                With --all, only drop pending queue items whose next retry is due now.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --runtime strings      With --all, filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.
      --sort string          With --all, sort matching queue items before limiting: state, id, event, instance, job, runtime, queued, updated, next-retry, or attempts. (default "state")
      --state string         With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.
```

## `agent-team team queue prune`

Prune team-owned queue items.

Prune team-owned queue items. By default this removes dead-letter items owned by the selected team.

```text
agent-team team queue prune <team> [flags]
```

Flags:

```text
      --commands              With --dry-run, print the matching team queue prune command when the preview has actionable work.
      --dry-run               Preview team-owned queue items that would be pruned without dropping them.
      --event-type strings    Filter by event type before pruning; repeat or comma-separate values.
      --format string         Render each prune result with a Go template, e.g. '{{.ID}} {{.State}}'.
      --job strings           Filter by job id or ticket before pruning; repeat or comma-separate values.
      --json                  Emit prune results as JSON.
      --limit int             Prune at most this many matching team-owned queue items; 0 means no limit.
      --older-than duration   Only prune team-owned items older than this duration based on retry/dead-letter/update time.
      --ready                 Only prune pending queue items whose next retry is due now. Defaults --state to pending when --state is omitted.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --runtime strings       Filter by queued dispatch runtime before pruning: claude or codex. Can repeat or comma-separate.
      --state string          Queue state to prune: dead, pending, or all. (default "dead")
```

## `agent-team team queue quarantine`

List quarantined queue files scoped to one team.

```text
agent-team team queue quarantine <team> [flags]
```

Flags:

```text
      --commands             Print recommended commands from the visible team-owned quarantined queue files, one per line. agent-team follow-ups preserve the selected repo scope.
      --event-type strings   Filter by event type; repeat or comma-separate values.
      --format string        Render each team-owned quarantined queue file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.
      --job strings          Filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit team-owned quarantined queue files as JSON.
      --limit int            Limit rows after filtering and sorting; 0 means no limit.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --restorable           Only show quarantined files that can be restored.
      --sort string          Sort rows by path, state, id, event, instance, job, queued, updated, modified, attempts, restorable, or size. (default "path")
      --state string         Filter by queue state: pending or dead.
      --summary              Show aggregate team-owned quarantined queue-file counts instead of rows.
      --unrestorable         Only show quarantined files that cannot be restored.
```

Subcommands:

- `agent-team team queue quarantine drop` - Drop team-owned quarantined queue files after inspection.
- `agent-team team queue quarantine restore` - Restore team-owned quarantined queue files.
- `agent-team team queue quarantine show` - Show one team-owned quarantined queue file.

## `agent-team team queue quarantine drop`

Drop team-owned quarantined queue files after inspection.

Drop one team-owned quarantined queue file by path, or drop a filtered team-owned batch with --all.

```text
agent-team team queue quarantine drop <team> [quarantine-path] [flags]
```

Flags:

```text
      --all                   Drop all matching team-owned quarantined files instead of one path.
      --commands              With --dry-run, print the matching team queue quarantine drop apply command when the preview has actionable work.
      --dry-run               Preview quarantined files that would be dropped.
      --event-type strings    With --all, filter by event type; repeat or comma-separate values.
      --format string         Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings           With --all, filter by job id or ticket; repeat or comma-separate values.
      --json                  Emit drop results as JSON.
      --limit int             With --all, drop at most this many matching team-owned quarantined files; 0 means no limit.
      --older-than duration   With --all, only drop files older than this duration based on file mtime.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --restorable            With --all, only drop quarantined files that can be restored.
      --sort string           With --all, sort matching team-owned quarantined files before limiting: path, state, id, event, instance, job, queued, updated, modified, attempts, restorable, or size. (default "path")
      --state string          With --all, filter by queue state: pending or dead.
      --unrestorable          With --all, only drop quarantined files that cannot be restored.
```

## `agent-team team queue quarantine restore`

Restore team-owned quarantined queue files.

Restore one team-owned quarantined queue file by path, or restore a filtered team-owned batch of restorable files with --all.

```text
agent-team team queue quarantine restore <team> [quarantine-path] [flags]
```

Flags:

```text
      --all                  Restore all matching team-owned restorable quarantined files instead of one path.
      --commands             With --dry-run, print the matching team queue quarantine restore apply command when the preview has actionable work.
      --dry-run              Preview the restore without moving files.
      --event-type strings   With --all, filter by event type; repeat or comma-separate values.
      --force                Overwrite an existing active queue file with the same restore path.
      --format string        Render each restore result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings          With --all, filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit restore result as JSON.
      --limit int            With --all, restore at most this many matching team-owned quarantined files; 0 means no limit.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --sort string          With --all, sort matching team-owned quarantined files before limiting: path, state, id, event, instance, job, queued, updated, modified, attempts, restorable, or size. (default "path")
      --state string         With --all, filter by queue state: pending or dead.
```

## `agent-team team queue quarantine show`

Show one team-owned quarantined queue file.

```text
agent-team team queue quarantine show <team> <quarantine-path> [flags]
```

Flags:

```text
      --commands        Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.
      --format string   Render the team-owned quarantined queue file with a Go template, e.g. '{{.Team}} {{.ID}}'.
      --json            Emit the team-owned quarantined queue file as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team team queue retry`

Retry team-owned queue items.

Retry one team-owned queue item by id, or retry a filtered team-owned batch with --all. Batch retries default to dead-letter items.

```text
agent-team team queue retry <team> [id] [flags]
```

Flags:

```text
      --all                  Retry all matching team-owned queue items instead of one id.
      --commands             With --dry-run, print the matching team queue retry command when the preview has actionable work.
      --dry-run              Preview matching team-owned queue items without retrying them.
      --event-type strings   With --all, filter by event type; repeat or comma-separate values.
      --format string        Render each retry result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings          With --all, filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit machine-readable JSON.
      --limit int            With --all, retry at most this many matching queue items; 0 means no limit.
      --ready                With --all, only retry pending queue items whose next retry is due now.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --runtime strings      With --all, filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.
      --sort string          With --all, sort matching queue items before limiting: state, id, event, instance, job, runtime, queued, updated, next-retry, or attempts. (default "state")
      --state string         With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.
```

## `agent-team team queue show`

Show one queue item owned by one team.

```text
agent-team team queue show <team> <id> [flags]
```

Flags:

```text
      --commands        Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.
      --format string   Render the queue item with a Go template, e.g. '{{.ID}} {{.State}}'.
      --json            Emit the queue item as JSON.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team team ready`

List ready pipeline jobs owned by one team.

```text
agent-team team ready <team> [flags]
```

Flags:

```text
      --commands            Print only recommended commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string       Render each row with a Go template, e.g. '{{.JobID}} {{.State}} {{.StepID}}'.
      --interval duration   Refresh interval for --watch. (default 2s)
      --json                Emit team ready rows as JSON.
      --limit int           Limit rows after filtering and sorting; 0 means no limit.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --repo string         Repo root containing .agent_team. (default "<repo>")
      --sort string         Sort rows by job, state, step, target, pipeline, updated, ticket, instance, or label. (default "job")
      --state strings       Next-step state to include: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --step string         Only include rows whose next step has this id.
  -w, --watch               Refresh the team ready-step table until interrupted.
```

## `agent-team team reject`

Reject manual pipeline gates owned by one team.

Reject or preview blocked manual-gate steps for jobs in one team&#39;s declared pipelines. Rejected gates are marked failed and record a manual_gate_rejected audit event.

```text
agent-team team reject <team> [flags]
```

Flags:

```text
      --commands              With --dry-run, print the matching team reject apply command when the preview has actionable work.
      --dry-run               Preview manual gate rejections without writing job state.
      --format string         Render each result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.
      --json                  Emit rejection results as JSON.
      --limit int             Reject at most this many manual gates; 0 means no limit.
      --message string        Status message recorded on each rejected team job.
      --message-file string   Read rejection reason from a file, or '-' for stdin.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --step string           Reject only manual gates whose next blocked step has this id.
```

## `agent-team team release`

Release held pipeline jobs owned by one team.

Release held jobs in pipelines declared on one team without changing their lifecycle status.

```text
agent-team team release <team> [message...] [flags]
```

Aliases: `resume`, `unpause`

Flags:

```text
      --commands              With --dry-run, print the matching release apply command when the preview has actionable work.
      --dry-run               Preview releases without writing job state.
      --expired               Only release held jobs whose hold_until has passed.
      --format string         Render each release result with a Go template, e.g. '{{.JobID}} {{.Action}}'.
      --json                  Emit release results as JSON.
      --limit int             Release at most this many held team jobs; 0 means no limit.
      --message string        Release message recorded on each team job.
      --message-file string   Read release message from a file, or '-' for stdin.
      --repo string           Repo root containing .agent_team. (default "<repo>")
```

## `agent-team team repair`

Recover unhealthy orchestration state for one team.

Recover unhealthy orchestration state scoped to one team: ensure the daemon is ready, retry team-owned dead-letter queue items, optionally time out stale team work, retry failed team pipeline steps, and run a scoped team tick. Use --dry-run to preview.

```text
agent-team team repair <team> [flags]
```

Flags:

```text
      --all-ready-steps               Advance every currently ready independent team pipeline step during the scoped repair tick.
      --commands                      With --dry-run, print the matching team repair apply command when the preview has actionable work.
      --dry-run                       Preview team repair actions without mutating state or starting the daemon.
      --fail-on-failed                With --wait, exit 1 if any team-repaired job resolves to failed.
      --fallbacks                     When team repair health snapshots include runtime recovery actions, recommend command-mode fallback expansion.
      --format string                 Render the team repair result with a Go template, e.g. '{{.Team.Name}} {{.Queue.Action}}'.
      --interval duration             Delay between --until-idle scoped team tick cycles. (default 2s)
      --jobs                          Include team-owned durable job and pipeline health.
      --json                          Emit machine-readable JSON.
      --last-message                  When team repair health snapshots include runtime recovery actions, prefer clean Codex final-message commands.
      --limit int                     Retry at most this many team dead-letter queue items or failed team pipeline jobs, and advance at most this many ready team pipeline jobs or ready steps with --all-ready-steps; 0 means no limit.
      --max-cycles int                With --until-idle, stop after this many cycles if work keeps appearing. (default 20)
      --preview-routes                With --dry-run, include route and dispatch payload previews for retried or ready team pipeline steps.
      --ready-timeout duration        Maximum time to wait for implicit daemon readiness (0 = no timeout). (default 3s)
      --repo string                   Repo root containing .agent_team. (default "<repo>")
      --retry-force                   With --retry-pipelines, ignore step max_attempts caps for explicit team repair retry.
      --retry-message string          Audit message to record when --retry-pipelines resets failed team steps.
      --retry-message-file string     Read team retry repair audit message from a file, or '-' for stdin.
      --retry-pipeline string         With --retry-pipelines, retry only failed team jobs owned by this pipeline.
      --retry-pipelines               Reset failed team pipeline steps and dispatch them before the scoped team tick.
      --retry-step string             With --retry-pipelines, retry only failed team jobs whose next failed step has this id.
      --runtime string                Runtime profile for retried or advanced team step dispatches (claude or codex). Overrides env and repo config.
      --runtime-bin string            Runtime binary for retried or advanced team step dispatches. Overrides env and repo config.
      --skip-daemon                   Do not start or reconcile the daemon.
      --skip-queue                    Do not retry team-owned dead-letter queue items.
      --skip-tick                     Do not run a scoped team tick after queue retry.
      --timeout-jobs                  Mark stale running team job work failed before retrying failed pipeline steps.
      --timeout-message string        Audit message to record when team timeout repair marks stale work failed.
      --timeout-message-file string   Read team timeout repair audit message from a file, or '-' for stdin.
      --timeout-pipeline string       With --timeout-jobs or --timeout-pipelines, mark only stale team work owned by this pipeline.
      --timeout-pipelines             Mark stale running team pipeline steps failed before retrying failed pipeline steps.
      --timeout-step string           With --timeout-jobs or --timeout-pipelines, mark only stale running team steps with this id failed.
      --timeout-target-agent string   With --timeout-jobs or --timeout-pipelines, mark only stale team work targeting this agent.
      --until-idle                    Run scoped team ticks until no immediate team queue, schedule, or pipeline work remains.
      --wait                          After team repair dispatches retried or ready steps, wait for those jobs to reach a lifecycle status, event, or next-step state.
      --wait-event strings            With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration        Polling interval with --wait. (default 500ms)
      --wait-next-state strings       With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings           With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string              With --wait, pipeline step id that must be the current next step for every repaired job.
      --wait-timeout duration         Maximum time to wait with --wait (0 = no timeout).
      --workspace string              Workspace mode for retried or advanced team pipeline steps: auto, worktree, or repo. (default "auto")
```

## `agent-team team restart`

Restart or resume a team&#39;s declared persistent instances.

```text
agent-team team restart <team> [flags]
```

Flags:

```text
      --attach                   Follow the selected instance log after restarting or resuming. Requires exactly one selected instance.
      --commands                 With --dry-run, print the matching team restart apply command when the preview has actionable work.
      --dry-run                  Preview planned restart/resume actions without changing daemon state.
  -f, --force                    Escalate to SIGKILL if a running instance does not stop within --timeout before restarting.
      --format string            Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                     Emit machine-readable JSON.
      --prompt string            Override the default kickoff prompt for instances started fresh.
      --prompt-file string       Read kickoff prompt from a file, or '-' for stdin.
  -q, --quiet                    Suppress non-error output and use only the exit code.
      --ready-timeout duration   Maximum time to wait for implicit daemon readiness (0 = no timeout). (default 3s)
      --repo string              Repo root containing .agent_team. (default "<repo>")
      --runtime strings          Only target team-owned daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.
      --summary                  Show aggregate action counts instead of per-instance rows.
      --tail string              With --attach, show only the last N lines before following (0 or all = all). (default "50")
      --timeout duration         Maximum time to wait for each running instance to stop before resuming (0 = daemon default).
      --wait                     Wait for selected instances to become healthy after restarting.
      --wait-timeout duration    Maximum time to wait for health with --wait (0 = no timeout).
```

## `agent-team team resume-plan`

Show runtime resume and fallback commands for one team.

Show runtime resume and fallback commands for daemon metadata owned by one declared team. This is a shorter alias for `agent-team team runtime resume-plan`.

```text
agent-team team resume-plan <team> [flags]
```

Flags:

```text
      --action strings    Only include plans whose recommended action is start, attach, resume, or logs. Can repeat or comma-separate.
      --can-managed       Only include runtimes with enough session metadata for daemon-managed resume.
      --commands          Print only recommended commands, one per line, after filtering, sorting, and limiting. agent-team follow-ups preserve the selected repo scope.
      --direct            Only include runtimes with a direct runtime resume command.
      --fallbacks         With --commands, print all viable start, attach, log, last-message, and direct resume commands per plan.
      --format string     Render each plan with a Go template, e.g. '{{.Instance}} {{.RecommendedAction}} {{.RecommendedCommand}}'.
      --json              Emit machine-readable JSON.
      --last-message      For Codex log fallbacks, recommend the clean last-message sidecar instead of following raw logs.
      --limit int         Limit plans after filtering and sorting; 0 means no limit.
      --managed           Only include runtimes whose adapter supports daemon-managed resume.
      --repo string       Repo root containing .agent_team. (default "<repo>")
      --runtime strings   Only include metadata for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale     Only include running metadata whose recorded runtime PID is no longer live.
      --sort string       Sort plans before rendering by instance, action, runtime, status, stale, job, pipeline, step, or agent. (default "instance")
      --stale             Only include running metadata whose recorded runtime PID is no longer live. Compatibility alias for --runtime-stale.
      --status strings    Only include metadata with this status: running, stopped, exited, or crashed. Can repeat or comma-separate.
      --step string       Only include plans for this pipeline step id.
      --summary           Summarize matching team resume plans by recommended action, runtime, and status.
      --unhealthy         Only include crashed or stale running metadata.
```

## `agent-team team retry`

Reset failed pipeline steps owned by one team.

Reset or preview failed-step retries for jobs in one team&#39;s declared pipelines. Pass --step to target one stage, or --dispatch to immediately dispatch each reset retry.

```text
agent-team team retry <team> [flags]
```

Flags:

```text
      --commands                  With --dry-run, print the matching team retry apply command when the preview has actionable work.
      --dispatch                  Dispatch each reset failed step immediately.
      --dry-run                   Preview failed-step resets and optional dispatches without writing job or daemon state.
      --fail-on-failed            With --wait, exit 1 if any retried job resolves to failed.
      --force                     Ignore step max_attempts caps for this explicit team retry.
      --format string             Render each result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.
      --json                      Emit retry results as JSON.
      --limit int                 Retry at most this many failed team jobs; 0 means no limit.
      --message string            Status message recorded on each retried team job.
      --message-file string       Read retry message from a file, or '-' for stdin.
      --preview-routes            With --dry-run --dispatch, include local topology route and dispatch payload previews.
      --repo string               Repo root containing .agent_team. (default "<repo>")
      --runtime string            Runtime profile for --dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for --dispatch. Overrides env and repo config.
      --step string               Retry only failed team jobs whose next failed step has this id.
      --wait                      After retrying or dispatching, wait for retried jobs to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step for every retried job.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
      --workspace string          Workspace mode for retried dispatches: auto, worktree, or repo. (default "auto")
```

## `agent-team team run`

Create a durable job through a team&#39;s pipeline.

Create a durable job using one of the team&#39;s declared pipelines. If the team declares exactly one pipeline, it is selected automatically; otherwise pass --pipeline.

```text
agent-team team run <team> <ticket> [kickoff...] [flags]
```

Flags:

```text
      --commands                  With --dry-run, print the matching team run apply command. agent-team follow-ups preserve the selected repo scope.
      --dispatch                  Dispatch the first ready pipeline step immediately using the running daemon.
      --dry-run                   Preview the pipeline job that would be created without writing it.
      --fail-on-failed            With --wait, exit 1 if the job resolves to failed.
      --format string             Render the created or advanced job with a Go template, e.g. '{{.ID}} {{.Pipeline}}'.
      --id string                 Override the normalized job id (default: ticket slug).
      --json                      Emit the created job or advance result as JSON.
      --kickoff string            Kickoff text for the first pipeline step.
      --kickoff-file string       Read kickoff text from a file, or '-' for stdin.
      --pipeline string           Team pipeline to use when the team declares more than one.
      --repo string               Repo root containing .agent_team. (default "<repo>")
      --runtime string            Runtime profile for --dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for --dispatch. Overrides env and repo config.
      --ticket-url string         Canonical ticket URL to store on the job.
      --wait                      After creating or dispatching, wait for the job to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. advance_dispatched, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
      --workspace string          Workspace mode for --dispatch: auto, worktree, or repo. (default "auto")
```

## `agent-team team runtime`

Inspect team-owned runtime metadata.

Inspect runtime metadata for daemon-known instances owned by one declared team.

```text
agent-team team runtime
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team team runtime ls` - List daemon runtime metadata owned by one team.
- `agent-team team runtime resume-plan` - Show runtime resume and fallback commands for one team.

## `agent-team team runtime ls`

List daemon runtime metadata owned by one team.

List daemon-known runtime metadata owned by one declared team, including persistent members and live ephemeral children.

```text
agent-team team runtime ls <team> [flags]
```

Aliases: `list`, `ps`

Flags:

```text
      --agent strings      Only show team-owned metadata for this agent. Can repeat or comma-separate.
      --format string      Render each team runtime row with a Go template, e.g. '{{.Instance}} {{.Runtime}} {{.Status}}'.
      --instance strings   Only show team-owned metadata with this instance name. Can repeat or comma-separate.
      --json               Emit team runtime metadata as JSON.
  -n, --last int           Show only the N most recently started team-owned runtime records after other filters (0 = all).
  -l, --latest             Show only the most recently started team-owned runtime record after other filters.
      --repo string        Repo root containing .agent_team. (default "<repo>")
      --runtime strings    Only show team-owned metadata for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale      Only show team-owned running metadata whose recorded runtime PID is no longer live.
      --sort string        Sort team runtime rows by instance, status, runtime, agent, stale, unhealthy, job, started, stopped, or exited. (default "instance")
      --status strings     Only show team-owned runtime status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary            Summarize matching team-owned runtime metadata by status, runtime, and agent.
      --unhealthy          Only show crashed or runtime-stale team-owned metadata.
```

## `agent-team team runtime resume-plan`

Show runtime resume and fallback commands for one team.

Show runtime resume and fallback commands for daemon metadata owned by one declared team. This is the team-scoped form of `agent-team runtime resume-plan`.

```text
agent-team team runtime resume-plan <team> [flags]
```

Flags:

```text
      --action strings    Only include plans whose recommended action is start, attach, resume, or logs. Can repeat or comma-separate.
      --can-managed       Only include runtimes with enough session metadata for daemon-managed resume.
      --commands          Print only recommended commands, one per line, after filtering, sorting, and limiting. agent-team follow-ups preserve the selected repo scope.
      --direct            Only include runtimes with a direct runtime resume command.
      --fallbacks         With --commands, print all viable start, attach, log, last-message, and direct resume commands per plan.
      --format string     Render each plan with a Go template, e.g. '{{.Instance}} {{.RecommendedAction}} {{.RecommendedCommand}}'.
      --json              Emit machine-readable JSON.
      --last-message      For Codex log fallbacks, recommend the clean last-message sidecar instead of following raw logs.
      --limit int         Limit plans after filtering and sorting; 0 means no limit.
      --managed           Only include runtimes whose adapter supports daemon-managed resume.
      --repo string       Repo root containing .agent_team. (default "<repo>")
      --runtime strings   Only include metadata for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale     Only include running metadata whose recorded runtime PID is no longer live.
      --sort string       Sort plans before rendering by instance, action, runtime, status, stale, job, pipeline, step, or agent. (default "instance")
      --stale             Only include running metadata whose recorded runtime PID is no longer live. Compatibility alias for --runtime-stale.
      --status strings    Only include metadata with this status: running, stopped, exited, or crashed. Can repeat or comma-separate.
      --step string       Only include plans for this pipeline step id.
      --summary           Summarize matching team resume plans by recommended action, runtime, and status.
      --unhealthy         Only include crashed or stale running metadata.
```

## `agent-team team schedules`

List schedules owned by one team.

```text
agent-team team schedules <team> [flags]
```

Flags:

```text
      --commands        Print the due team schedule preview command, one per line.
      --due             Only show team schedules due now, including the due reason.
      --format string   Render each schedule with a Go template, e.g. '{{.Name}} {{.Every}}'.
      --json            Emit team schedules as JSON.
      --limit int       Show at most this many schedules after filtering and ordering; 0 means all.
      --next            Order team schedules by due state and next run, including due metadata.
      --repo string     Repo root containing .agent_team. (default "<repo>")
```

## `agent-team team send`

Send a mailbox message to team-owned instances.

Send a mailbox message to running daemon-known instances owned by one declared team. Use --all to include every lifecycle status, or combine selectors such as --status, --runtime, --phase, --latest, --last, --stale, --runtime-stale, and --unhealthy.

```text
agent-team team send <team> [message...] [flags]
```

Flags:

```text
      --all                   Send to every daemon-known team instance regardless of lifecycle status.
      --commands              With --dry-run, print the matching team send apply command when the preview has actionable recipients.
      --dry-run               Preview matching recipients without appending mailbox messages.
      --format string         Render each send result with a Go template, e.g. '{{.To}} {{.ID}}'.
      --from string           Sender label recorded with the message. (default "(cli)")
      --json                  Emit machine-readable JSON.
  -n, --last int              Send to the N most recently started team-owned daemon-known instances after other filters (0 = all).
      --latest                Send to the most recently started team-owned daemon-known instance after other filters.
      --message string        Message text to send.
      --message-file string   Read message text from a file, or '-' for stdin.
      --phase strings         Send to team-owned instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --runtime strings       Send to team-owned instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale         Send to team-owned running instances whose recorded runtime PID is no longer live.
      --stale                 Send to team-owned instances whose status.toml is stale.
      --status strings        Send to team-owned instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --unhealthy             Send to team-owned instances that are crashed, status-stale, or runtime-stale.
```

## `agent-team team show`

Show one declared team.

```text
agent-team team show <team> [flags]
```

Aliases: `inspect`

Flags:

```text
      --json          Emit the team as JSON.
      --repo string   Repo root containing .agent_team. (default "<repo>")
```

## `agent-team team skip`

Mark matching team pipeline steps intentionally skipped.

Mark matching non-running pipeline steps as done with skipped metadata for jobs in one team&#39;s declared pipelines. The step id is required to prevent accidental broad bypasses.

```text
agent-team team skip <team> --step <id> [flags]
```

Flags:

```text
      --commands              With --dry-run, print the matching team skip apply command when the preview has actionable work.
      --dry-run               Preview skipped team steps without writing job state.
      --format string         Render each result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.
      --json                  Emit skip results as JSON.
      --limit int             Skip or report at most this many matching team steps; 0 means no limit.
      --message string        Skip reason recorded on each updated team job.
      --message-file string   Read skip reason from a file, or '-' for stdin.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --step string           Required pipeline step id to mark skipped.
```

## `agent-team team snapshot`

Capture a team-scoped diagnostic report.

Capture a read-only diagnostic report scoped to one declared team. It includes team health, plan, instances, jobs, job status preview, queue, inbox, schedule, runtime, lifecycle event state, and command provenance.

```text
agent-team team snapshot <team> [flags]
```

Flags:

```text
      --commands             Print snapshot next-action commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --events int           Recent matching team lifecycle events to include. Use -1 for all matching events or 0 to skip events. (default 50)
      --events-sort string   Sort included team lifecycle events by oldest or newest after applying --events. (default "oldest")
      --format string        Render the team snapshot with a Go template, e.g. '{{.Team.Name}} {{len .Jobs}}'.
      --json                 Emit the full snapshot JSON to stdout.
      --no-redact            Include raw payload values instead of redacting sensitive keys.
  -o, --output string        Write the full JSON snapshot to this file. Use '-' for stdout.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --schedule-limit int   Upcoming team schedules to include after ordering; 0 means all. (default 10)
```

## `agent-team team stats`

Show CPU and memory usage for team-owned instances.

Show a one-shot or watchable resource snapshot for instances owned by one declared team. With no names, only running team-owned instances are shown. Use --all to include stopped, exited, crashed, and missing persistent team members.

```text
agent-team team stats <team> [<instance>...] [flags]
```

Aliases: `top`

Flags:

```text
  -a, --all                 Include stopped, exited, crashed, and missing persistent team-owned instances.
      --format string       Render each row with a Go template, e.g. '{{.Instance}} {{.CPUPercent}} {{.RSS}}'.
      --interval duration   Refresh interval for --watch. (default 2s)
      --json                Emit JSON. With --watch, writes one JSON array per refresh.
  -n, --last int            Show stats for the N most recently started team-owned instances after other filters (0 = all).
      --latest              Show stats for the most recently started team-owned instance after other filters.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --phase strings       Only show team-owned instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --repo string         Repo root containing .agent_team. (default "<repo>")
      --runtime strings     Only show team-owned instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale       Only show team-owned running instances whose recorded runtime PID is no longer live.
      --sort string         Sort rows by name, cpu, mem, rss, status, agent, phase, stale, runtime-stale, or unhealthy. (default "name")
      --stale               Only show team-owned instances whose status.toml is stale.
      --status strings      Only show team-owned lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary             Show aggregate CPU, memory, and RSS totals instead of team instance rows.
      --unhealthy           Only show crashed, status-stale, or runtime-stale team-owned instances.
  -w, --watch               Refresh team stats until interrupted.
```

## `agent-team team status`

Summarize one team&#39;s instances, jobs, and pipelines.

```text
agent-team team status <team> [flags]
```

Flags:

```text
      --commands            Print recommended actions, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string       Render team status with a Go template, e.g. '{{.Team.Name}} {{.InstanceSummary.Total}}'.
      --interval duration   Refresh interval for --watch. (default 2s)
      --json                Emit team status as JSON.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --repo string         Repo root containing .agent_team. (default "<repo>")
      --runtime strings     Only summarize team-owned instances for this runtime: claude or codex. Jobs, queue, pipelines, and schedules remain team-scoped. Can repeat or comma-separate.
      --runtime-stale       Only summarize team-owned running instances whose recorded runtime PID is no longer live. Jobs, queue, pipelines, and schedules remain team-scoped.
  -w, --watch               Refresh team status until interrupted.
```

## `agent-team team sync`

Sync one team&#39;s declared persistent instances.

Reload topology, reconcile daemon metadata, then start or resume the selected team&#39;s declared persistent instances. With --stop-extras, running daemon-known extras for the team&#39;s agents are stopped.

```text
agent-team team sync <team> [flags]
```

Flags:

```text
      --action strings           Only sync plan rows with this action: start, resume, keep, unsupported, on-demand, stop, or extra. Can repeat or comma-separate.
      --commands                 With --dry-run, print the matching team sync apply command when the preview has actionable work.
      --dry-run                  Preview team topology convergence without starting the daemon or instances.
      --format string            Render each sync action with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                     Emit machine-readable JSON.
  -q, --quiet                    Suppress non-error output and use only the exit code.
      --ready-timeout duration   Maximum time to wait for implicit daemon readiness (0 = no timeout). (default 3s)
      --repo string              Repo root containing .agent_team. (default "<repo>")
      --runtime strings          Only sync team-owned daemon-known plan rows for this runtime: claude or codex. Can repeat or comma-separate.
      --stop-extras              Also stop running daemon-known extras for this team's agents.
      --summary                  Show aggregate action counts instead of per-instance rows.
      --timeout duration         Maximum time to wait with --wait (0 = no timeout).
      --wait                     Wait for selected team instances to become healthy after syncing.
```

## `agent-team team tick`

Run one team&#39;s orchestration maintenance work.

Run or preview one team&#39;s due schedules, drainable queue items, and ready pipeline steps.

```text
agent-team team tick <team> [flags]
```

Flags:

```text
      --all-ready-steps           Advance every currently ready independent team pipeline step in this tick.
      --commands                  With --dry-run, print the matching team tick apply command when the preview has actionable work.
      --dry-run                   Preview team-owned maintenance work without mutating state.
      --fail-on-failed            With --wait, exit 1 if any advanced team pipeline job resolves to failed.
      --format string             Render the team tick result with a Go template, e.g. '{{.Team.Name}} {{.Tick.Queue.WouldDispatch}}'.
      --interval duration         Refresh interval for --watch, or delay between --until-idle cycles. (default 2s)
      --json                      Emit machine-readable JSON.
      --limit int                 Advance at most this many ready pipeline jobs, or ready steps with --all-ready-steps; 0 means no limit.
      --max-cycles int            With --until-idle, stop after this many cycles if work keeps appearing. (default 20)
      --preview-routes            With --dry-run, include route and dispatch payload previews for ready pipeline steps.
      --repo string               Repo root containing .agent_team. (default "<repo>")
      --runtime string            Runtime profile for advanced step dispatches (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for advanced step dispatches. Overrides env and repo config.
      --skip-advance              Skip pipeline advancement work.
      --skip-drain                Skip queue drain work.
      --skip-schedules            Skip due schedule work.
      --until-idle                Run team tick cycles until no immediate team schedule, queue, or pipeline work remains.
      --wait                      After one team tick, wait for advanced team pipeline jobs to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step for every advanced job.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
  -w, --watch                     Run the team tick repeatedly until interrupted.
      --workspace string          Workspace mode for advanced pipeline steps: auto, worktree, or repo. (default "auto")
```

## `agent-team team timeline`

Show combined job audit and lifecycle timelines for team-owned jobs.

Show durable job audit events together with matching daemon lifecycle events for jobs owned by one declared team.

```text
agent-team team timeline <team> [flags]
```

Flags:

```text
      --actor strings      Only show job-audit timeline rows from this actor. Can repeat or comma-separate.
      --agent strings      Only show lifecycle timeline rows for this agent. Can repeat or comma-separate.
      --format string      Render each timeline row with a Go template, e.g. '{{.JobID}} {{.Source}} {{.Kind}}'.
      --instance strings   Only show timeline rows for this owning instance. Can repeat or comma-separate.
      --job strings        Only show timeline rows for this job id. Can repeat or comma-separate.
      --json               Emit machine-readable JSON.
      --kind strings       Only show timeline rows with this kind/action. Can repeat or comma-separate.
      --repo string        Repo root containing .agent_team. (default "<repo>")
      --since string       Only show timeline rows since this duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --sort string        Sort returned timeline rows by oldest or newest after applying --tail. (default "oldest")
      --source string      Timeline source to include: all, job, or lifecycle. (default "all")
      --status strings     Only show timeline rows with this status. Can repeat or comma-separate.
      --summary            Summarize matching timeline rows by job, source, kind, status, actor, instance, and agent.
      --tail string        Show only the last N combined events before sorting for display (0 or all = all). (default "0")
```

## `agent-team team timeout`

Mark stale running work owned by one team failed.

Mark or preview stale running steps for jobs in one team&#39;s declared pipelines. Add --jobs to include stale step-less jobs whose target instance belongs to the team. Timed-out work becomes failed so the normal team retry flow can reopen it.

```text
agent-team team timeout <team> [flags]
```

Flags:

```text
      --commands              With --dry-run, print the matching timeout apply command when the preview has actionable work.
      --dry-run               Preview stale-work failures without writing job state.
      --format string         Render each result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.
      --jobs                  Include stale step-less jobs whose target instance belongs to the team.
      --json                  Emit timeout results as JSON.
      --limit int             Mark at most this many stale running team jobs or steps failed; 0 means no limit.
      --message string        Status message recorded on each timed-out team job.
      --message-file string   Read timeout message from a file, or '-' for stdin.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --step string           Mark only stale running team steps with this id.
      --target-agent string   Mark only stale running team work targeting this agent.
```

## `agent-team team triage`

Show team-owned jobs that need operator attention.

Show a compact team-scoped work queue triage view from durable jobs, persisted daemon queue items, status-file update previews, and ready pipeline steps.

```text
agent-team team triage <team> [flags]
```

Flags:

```text
      --commands               Print only recommended commands, one per line. agent-team follow-ups preserve the selected repo scope.
      --content-only           Only show attention rows with failed gates classified as content.
      --format string          Render the team triage snapshot with a Go template, e.g. '{{.Summary.Total}} {{len .Attention}}'.
      --infra-only             Only show attention rows with failed gates classified as infra.
      --interval duration      Refresh interval for --watch. (default 2s)
      --json                   Emit team triage snapshot as JSON.
      --min-severity string    Only show attention rows at least this severe: critical, warning, or info.
      --no-clear               With --watch, append snapshots instead of redrawing the terminal.
      --reason strings         Only show attention rows with this reason. Can repeat or comma-separate.
      --repo string            Repo root containing .agent_team. (default "<repo>")
      --stale-after duration   Flag queued or running jobs with no update after this duration (default: [health].job_stale_after or 24h; 0 disables stale checks). (default 24h0m0s)
  -w, --watch                  Refresh the team triage view until interrupted.
```

## `agent-team team unblock`

Answer blocked pipeline workers owned by one team.

Send the same operator answer to blocked pipeline step owners for jobs in one team&#39;s declared pipelines. By default a job is selected when it has a single blocked step owner; pass --step to target one stage explicitly.

```text
agent-team team unblock <team> [message...] [flags]
```

Flags:

```text
      --allow-missing         Allow queueing messages for owning instances the daemon does not know yet.
      --commands              With --dry-run, print the matching team unblock apply command when the preview has actionable work.
      --dry-run               Preview team unblocks without writing job state or mailbox messages.
      --format string         Render each result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}} {{.Instance}}'.
      --from string           Sender label recorded with each unblock message. (default "(cli)")
      --json                  Emit unblock results as JSON.
      --limit int             Unblock at most this many blocked team jobs; 0 means no limit.
      --message string        Message text to send.
      --message-file string   Read message text from a file, or '-' for stdin.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --status string         Status after unblocking: running or queued. (default "running")
      --step string           Unblock only blocked jobs whose selected step has this id.
```

## `agent-team team up`

Start or resume a team&#39;s declared persistent instances.

```text
agent-team team up <team> [flags]
```

Aliases: `start`

Flags:

```text
      --attach                   Follow the selected instance log after starting or resuming. Requires exactly one selected instance.
      --commands                 With --dry-run, print the matching team up apply command when the preview has actionable work.
      --dry-run                  Preview planned start/resume actions without changing daemon state.
      --format string            Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                     Emit machine-readable JSON.
      --prompt string            Override the default kickoff prompt.
      --prompt-file string       Read kickoff prompt from a file, or '-' for stdin.
  -q, --quiet                    Suppress non-error output and use only the exit code.
      --ready-timeout duration   Maximum time to wait for implicit daemon readiness (0 = no timeout). (default 3s)
      --repo string              Repo root containing .agent_team. (default "<repo>")
      --runtime strings          Only target team-owned daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.
      --summary                  Show aggregate action counts instead of per-instance rows.
      --tail string              With --attach, show only the last N lines before following (0 or all = all). (default "50")
      --timeout duration         Maximum time to wait with --wait (0 = no timeout).
      --wait                     Wait for selected instances to become healthy after starting.
```

## `agent-team team wait`

Wait for team-owned instances to reach a lifecycle condition.

Wait until each selected team-owned instance reaches a lifecycle condition. With no instance names or selectors, this waits for declared persistent team members and live team-owned ephemeral children to be running. Use --until to wait for terminal, stopped, exited, crashed, removed, or running. Use --until-phase to wait for a reported work phase such as idle, blocked, or done; when combined with --until, both conditions must match.

```text
agent-team team wait <team> [<instance>...] [flags]
```

Flags:

```text
      --commands              With --dry-run, print the matching team wait command for the selected instances. agent-team follow-ups preserve the selected repo scope.
      --dry-run               Preview selected team instances and current state without waiting.
      --fail-on-crash         Exit 1 if any selected instance resolves to crashed.
      --format string         Render each wait result with a Go template, e.g. '{{.Instance}} {{.Status}} {{.Phase}}'.
      --interval duration     Polling interval. (default 500ms)
      --json                  Emit machine-readable JSON.
  -n, --last int              Wait for the N most recently started team-owned instances after other filters (0 = all).
      --latest                Wait for the most recently started team-owned instance after other filters.
      --phase strings         Wait for team-owned instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
  -q, --quiet                 Suppress output and use only the exit code.
      --repo string           Repo root containing .agent_team. (default "<repo>")
      --runtime strings       Wait for team-owned instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale         Wait for team-owned running instances whose recorded runtime PID is no longer live.
      --stale                 Wait for team-owned instances whose status.toml is stale.
      --status strings        Wait for team-owned instances currently in this lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary               Show aggregate final status and phase counts instead of per-instance rows.
      --timeout duration      Maximum time to wait (0 = no timeout).
      --unhealthy             Wait for team-owned instances that are crashed, status-stale, or runtime-stale.
      --until string          Lifecycle condition to wait for: running, terminal, stopped, exited, crashed, or removed. (default "running")
      --until-phase strings   Work phase condition to wait for: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
```

## `agent-team team wait-jobs`

Wait for team-owned jobs to reach a lifecycle status, event, or next step.

Wait for every selected job owned by one team to reach one of the requested lifecycle statuses, last events, and/or pipeline next-step states. By default this waits for terminal statuses: done or failed. When --event, --next-state, or --step is set without --status, any status is accepted. Use `team wait` for team-owned instance lifecycle waits.

```text
agent-team team wait-jobs <team> [flags]
```

Flags:

```text
      --event strings        Last event to wait for, e.g. closed, adopted, pipeline_done, or pipeline_failed. Can repeat or comma-separate.
      --fail-on-failed       Exit 1 if any selected job resolves to failed.
      --format string        Render each final job with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --interval duration    Polling interval. (default 500ms)
      --job strings          Only wait for these team-owned job ids. Can repeat or comma-separate.
      --json                 Emit final team-owned jobs as JSON.
      --next-state strings   Next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
  -q, --quiet                Suppress output and use only the exit code.
      --repo string          Repo root containing .agent_team. (default "<repo>")
      --status strings       Status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --step string          Pipeline step id that must be the current next step for every selected job.
      --timeout duration     Maximum time to wait (0 = no timeout).
```

## `agent-team template`

Manage templates (bundled + cached) used by `agent-team init`.

Manage templates: list, inspect, pull, and remove. A template is a parameterised directory tree with a `template.toml` manifest. The default template is embedded in the binary and can be referenced as `bundled` or `default`; additional templates can come from local paths, cached refs, or git refs pulled into a local cache.

```text
agent-team template
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team template ls` - List bundled and cached templates.
- `agent-team template pull` - Fetch a template into the cache so it can be referenced later.
- `agent-team template rm` - Remove a template from the cache.
- `agent-team template run` - One-shot: instantiate a template into a tempdir and spawn an agent.
- `agent-team template show` - Print a template&#39;s manifest. Default ref: bundled (alias: default).
- `agent-team template smoke` - Render a template into a temp repo and validate it.

## `agent-team template ls`

List bundled and cached templates.

```text
agent-team template ls [flags]
```

Flags:

```text
      --format string   Render each template row with a Go template, e.g. '{{.Ref}} {{.Version}}'.
      --json            Emit machine-readable JSON.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team template pull`

Fetch a template into the cache so it can be referenced later.

Pull a template into ~/.agent-team/cache/. Local directory refs are copied. Git refs such as github.com/acme/eng-team@v1.0.0 or https://github.com/acme/eng-team.git@v1.0.0 are shallow-fetched at the requested revision and cached under the resolved commit SHA. Bundled templates need no pull because they are embedded in the binary.

```text
agent-team template pull <ref> [flags]
```

Flags:

```text
      --as string       Cache key to store under (defaults to <name>@<version> from manifest, or basename).
      --commands        With --dry-run, print the matching template pull apply command when the preview has actionable work.
      --dry-run         Preview template pull without copying or cloning into the cache.
      --format string   Render the pull result with a Go template, e.g. '{{.CacheKey}} {{.Action}}'.
      --json            Emit machine-readable JSON.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team template rm`

Remove a template from the cache.

```text
agent-team template rm <ref> [flags]
```

Flags:

```text
      --commands        With --dry-run, print the matching template rm apply command when the preview has actionable work.
      --dry-run         Preview template removal without deleting it.
      --format string   Render the removal result with a Go template, e.g. '{{.Ref}} {{.Action}}'.
      --json            Emit machine-readable JSON.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team template run`

One-shot: instantiate a template into a tempdir and spawn an agent.

Instantiate a template (bundled, local path, cached ref, or git ref) into a target directory and immediately spawn the named agent against it. Returns when the selected runtime session exits. Without --target, a tempdir under $XDG_CACHE_HOME/agent-team/runs (or ~/.agent-team/runs) is created and removed on exit unless --keep is passed. With --target, the directory is preserved.

This is for ephemeral try-out / CI / sandbox use cases. The daemon is bypassed; the selected runtime is exec&#39;d directly. For long-lived setups, use `init` + `run` separately.

```text
agent-team template run <ref> <agent> [-- <runtime-args>...] [flags]
```

Flags:

```text
      --force                Overwrite an existing .agent_team/ at --target.
      --keep                 Keep the auto-created tempdir on exit (no-op when --target is set).
      --last-message         With Codex --prompt runs, suppress runtime diagnostics and print the clean final response sidecar.
  -n, --name string          Instance name (defaults to the agent name).
      --no-input             Fail if required parameters are missing instead of prompting.
  -p, --prompt string        Kickoff message for the agent (one-shot mode if set, interactive otherwise).
      --prompt-file string   Read kickoff message from a file, or '-' for stdin.
      --runtime string       Runtime profile for this invocation (claude or codex). Overrides env and rendered repo config.
      --runtime-bin string   Runtime binary for this invocation. Overrides env and rendered repo config.
      --set stringArray      Set a template parameter, e.g. --set linear.team_id=<uuid>. Repeatable.
      --target string        Target directory (must not already contain .agent_team/ unless --force). Defaults to a tempdir.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team template show`

Print a template&#39;s manifest. Default ref: bundled (alias: default).

```text
agent-team template show [ref] [flags]
```

Flags:

```text
      --format string   Render the template summary with a Go template, e.g. '{{.Ref}} {{.ContentHash}}'.
      --json            Emit machine-readable JSON.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team template smoke`

Render a template into a temp repo and validate it.

Render a template into a temporary repo with init --no-input semantics, then run doctor, agent doctor, pipeline doctor, and team doctor. Pass --set for required parameters.

```text
agent-team template smoke [ref] [flags]
```

Flags:

```text
      --commands          Print recommended follow-up commands for smoke findings. Keeps the rendered temp repo so commands remain usable.
      --format string     Render the smoke result with a Go template, e.g. '{{.OK}} {{len .Steps}}'.
      --json              Emit smoke results as JSON.
      --keep              Keep the temporary rendered repo for inspection.
      --set stringArray   Set a template parameter, e.g. --set linear.team_id=<uuid>. Repeatable.
      --strict            Fail on daemon binary, selected/runtime-default binary, and template provenance warnings.
      --strict-daemon     Fail doctor when the companion agent-teamd binary is not discoverable.
      --strict-runtime    Fail doctor when the selected LLM runtime binary or pipeline/team step and agent runtime defaults are not discoverable.
      --strict-template   Fail doctor when rendered template provenance does not resolve cleanly.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team tick`

Run one orchestration maintenance cycle.

Run one orchestration maintenance cycle against the running daemon: reconcile process metadata and job status files, fire due schedules, drain agent outbox and ready queue items, then advance ready pipeline jobs.

```text
agent-team tick [flags]
```

Flags:

```text
      --all-ready-steps           Advance every currently ready independent pipeline step in this tick.
      --commands                  With --dry-run, print the matching tick apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.
      --dry-run                   Preview job status reconciliation, schedule firing, outbox/queue drains, and pipeline advancement without mutating state.
      --fail-on-failed            With --wait, exit 1 if any advanced job resolves to failed.
      --format string             Render the tick result or until-idle aggregate with a Go template, e.g. '{{.Queue.Dispatched}} {{len .Advance}}'.
      --interval duration         Refresh interval for --watch, or delay between --until-idle cycles. (default 2s)
      --json                      Emit machine-readable JSON.
      --limit int                 Advance at most this many ready pipeline jobs, or ready steps with --all-ready-steps; 0 means no limit.
      --max-cycles int            With --until-idle, stop after this many cycles if work keeps appearing. (default 20)
      --preview-routes            With --dry-run, include route and dispatch payload previews for ready pipeline steps.
      --runtime string            Runtime profile for advanced step dispatches (claude or codex). Overrides env and repo config.
      --runtime-bin string        Runtime binary for advanced step dispatches. Overrides env and repo config.
      --skip-advance              Skip pipeline advancement.
      --skip-drain                Skip outbox and queue draining.
      --skip-reconcile            Skip daemon metadata and job status reconciliation.
      --skip-schedules            Skip firing due schedules.
      --target string             Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --until-idle                Run tick cycles until no immediate schedule, outbox, queue, or pipeline work remains.
      --wait                      After one tick, wait for advanced pipeline jobs to reach a lifecycle status, event, or next-step state.
      --wait-event strings        With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.
      --wait-interval duration    Polling interval with --wait. (default 500ms)
      --wait-next-state strings   With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.
      --wait-status strings       With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --wait-step string          With --wait, pipeline step id that must be the current next step for every advanced job.
      --wait-timeout duration     Maximum time to wait with --wait (0 = no timeout).
  -w, --watch                     Run tick repeatedly until interrupted.
      --workspace string          Workspace mode for advanced pipeline steps: auto, worktree, or repo. (default "auto")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team topology`

Show declared instances and triggers (reads .agent_team/instances.toml).

```text
agent-team topology
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team topology graph` - Render a full topology graph.
- `agent-team topology reload` - Re-read instances.toml from disk (daemon must be running).
- `agent-team topology show` - Print the resolved topology (declared instances + triggers).
- `agent-team topology summary` - Summarize declared topology and workflow health.

## `agent-team topology graph`

Render a full topology graph.

Render a read-only graph of declared teams, instances, pipelines, schedules, and dispatch wiring.

```text
agent-team topology graph [flags]
```

Flags:

```text
      --commands        Print recommended commands from graph action hints, one per line. agent-team follow-ups preserve the selected repo scope.
      --format string   Graph output format: text, mermaid, or dot. (default "text")
      --job string      Overlay durable job step state on its declared pipeline graph.
      --json            Emit graph nodes and edges as JSON.
      --routes          Annotate pipeline steps with matching agent.dispatch routes.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team topology reload`

Re-read instances.toml from disk (daemon must be running).

```text
agent-team topology reload [flags]
```

Flags:

```text
      --format string   Render reload result with a Go template, e.g. '{{len .Instances}}'.
      --json            Emit reloaded topology as JSON.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team topology show`

Print the resolved topology (declared instances + triggers).

```text
agent-team topology show [flags]
```

Flags:

```text
      --json            Emit raw JSON.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team topology summary`

Summarize declared topology and workflow health.

```text
agent-team topology summary [flags]
```

Flags:

```text
      --json            Emit topology summary as JSON.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team upgrade`

Check or apply a template upgrade using the repo&#39;s template lock.

Compare .agent_team/.template.lock against the locked template ref, or --to &lt;ref&gt; when supplied. With --apply, agent-team renders the locked and target templates with the current repo config and applies only clean three-way changes; local edits are reported as conflicts and left untouched.

```text
agent-team upgrade (--check|--apply) [--to <ref>] [flags]
```

Flags:

```text
      --apply           Apply clean template changes and update .template.lock; refuses to run when local conflicts are detected.
      --check           Compare current template lock against a resolved template ref without writing files.
      --commands        With --apply --dry-run, print the matching apply command when the preview has actionable work.
      --dry-run         With --apply, preview the clean/conflicting file actions without writing files.
      --format string   Render the upgrade check result with a Go template, e.g. '{{.Differs}} {{.TargetVersion}}'.
      --json            Emit the upgrade check result as JSON.
      --strict          With --check, exit 1 when the target template differs from the lock.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --to string       Template ref to compare against (defaults to the ref in .template.lock).
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team usage`

Show runtime token usage rollups.

Show runtime usage rollups captured from finalized daemon-managed instances and persisted onto durable jobs.

```text
agent-team usage [flags]
```

Flags:

```text
      --by string       Group usage by job, instance, agent, or runtime. (default "job")
      --json            Emit usage rollups as JSON.
      --since string    Only include usage captured since a duration ago (for example 7d, 24h) or an RFC3339 timestamp.
      --target string   Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team wait`

Wait for daemon-managed instances to reach a lifecycle condition.

Wait until each selected daemon-managed instance reaches a lifecycle condition. By default this waits for a terminal state (actually stopped, exited, crashed, or removed), matching Docker-style completion waits. Use --until to wait for running, stopped, exited, crashed, removed, or terminal. Use --until-phase to wait for a reported work phase such as idle, blocked, or done; when combined with --until, both conditions must match.

```text
agent-team wait [<instance>...] [flags]
```

Flags:

```text
      --agent strings         Wait for every daemon-known instance for this agent. Can repeat or comma-separate.
  -a, --all                   Wait for every daemon-known instance.
      --commands              With --dry-run, print the matching wait command for the selected instances. agent-team follow-ups preserve the selected repo scope.
      --dry-run               Preview selected instances and current state without waiting.
      --fail-on-crash         Exit 1 if any selected instance resolves to crashed.
      --format string         Render each wait result with a Go template, e.g. '{{.Instance}} {{.Status}} {{.Phase}}'.
      --interval duration     Polling interval. (default 500ms)
      --json                  Emit machine-readable JSON.
  -n, --last int              Wait for the N most recently started daemon-known instances after other filters (0 = all).
      --latest                Wait for the most recently started daemon-known instance after other filters.
      --phase strings         Wait for daemon-known instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
  -q, --quiet                 Suppress output and use only the exit code.
      --runtime strings       Wait for daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale         Wait for daemon-known running instances whose recorded runtime PID is no longer live.
      --stale                 Wait for daemon-known instances whose status.toml is stale.
      --status strings        Wait for daemon-known instances currently in this lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary               Show aggregate final status and phase counts instead of per-instance rows.
      --target string         Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --timeout duration      Maximum time to wait (0 = no timeout).
      --unhealthy             Wait for daemon-known instances that are crashed, status-stale, or runtime-stale.
      --until string          Lifecycle condition to wait for: terminal, running, stopped, exited, crashed, or removed. (default "terminal")
      --until-phase strings   Work phase condition to wait for: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

## `agent-team watch`

Watch the combined health, recovery, inbox, instance, and resource monitor.

Watch the Docker-style operator monitor, refreshing fleet health, job, queue, and outbox recovery signals, inbox state, instance state, and daemon-managed process stats until interrupted.

```text
agent-team watch [flags]
```

Flags:

```text
      --action strings         With --plan, only show plan rows with this action: start, resume, keep, unsupported, on-demand, stop, or extra. Can repeat or comma-separate.
      --agent strings          Only show instances, stats, and plan rows for this agent. Can repeat or comma-separate.
  -a, --all                    Include stopped, exited, and crashed daemon-managed instances in the stats section.
      --event-action strings   With --events, only show lifecycle events with this action. Can repeat or comma-separate.
      --events int             Include the last N matching daemon lifecycle events in the full monitor (0 = omit).
      --events-sort string     Sort the visible --events section by oldest or newest. (default "oldest")
      --fallbacks              When runtime recovery actions use resume-plan, recommend command-mode fallback expansion.
      --format string          Render each monitor snapshot with a Go template, e.g. '{{.Health.Healthy}} {{len .Instances}}'.
      --instance strings       Only show instances, stats, and plan rows with this name. Can repeat or comma-separate.
      --interval duration      Refresh interval. (default 2s)
      --jobs                   Include durable job summary, attention, and ready-step state.
      --json                   Emit one JSON object per refresh.
  -n, --last int               Show only the N most recently started instances after other filters (0 = all).
      --last-message           When runtime recovery actions use resume-plan log fallbacks, prefer clean Codex final-message commands.
      --latest                 Show only the most recently started instance after other filters.
      --no-clear               Append snapshots instead of redrawing the terminal.
      --phase strings          Only show instances and stats in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --plan                   Include desired-state actions from instances.toml and daemon metadata.
      --resources              With --summary, include aggregate CPU, memory, and RSS totals.
      --runtime strings        Only show instances and stats for this runtime: claude or codex. Can repeat or comma-separate.
      --runtime-stale          Only show running instances whose recorded runtime PID is no longer live.
      --schedules              Include due and upcoming declared schedule state.
      --since string           With --events, only show lifecycle events since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --sort string            Sort instance rows by name, status, agent, phase, stale, runtime-stale, unhealthy, started, stopped, or exited. (default "name")
      --stale                  Only show instances whose status.toml is stale.
      --stats-sort string      Sort stats rows by name, cpu, mem, rss, status, agent, phase, stale, runtime-stale, or unhealthy. (default "name")
      --status strings         Only show lifecycle status in instance, stats, and plan sections: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --stop-extras            With --plan, preview running topology extras as stop actions.
      --strict-topology        Treat running daemon-known instances not declared in instances.toml as unhealthy.
      --summary                Watch compact non-failing fleet health and optional plan summaries instead of the full monitor.
      --target string          Repo root containing .agent_team (legacy; prefer global --repo). (default "<repo>")
      --unhealthy              Only show crashed, status-stale, or runtime-stale instances.
```

Inherited Flags:

```text
      --repo string   Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.
```

