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

```text
agent-team [flags]
```

Persistent Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team attach` - Open an interactive runtime session against a daemon-managed persistent instance.
- `agent-team channel` - Manage daemon-managed pub/sub channels.
- `agent-team channels` - List all pub/sub channels (alias for `channel ls`).
- `agent-team completion` - Generate the autocompletion script for the specified shell
- `agent-team daemon` - Manage the agent-teamd orchestrator daemon for this repo.
- `agent-team dispatch` - Dispatch an agent through daemon topology.
- `agent-team docs` - Generate developer documentation from the command tree.
- `agent-team doctor` - Sanity-check the vendored team.
- `agent-team event` - Publish manual topology events to the daemon (for testing trigger matching).
- `agent-team events` - Show daemon lifecycle events.
- `agent-team health` - Check daemon and instance fleet health.
- `agent-team help` - Help about any command
- `agent-team init` - Vendor a starter team template into the current repo (creates .agent_team/).
- `agent-team inspect` - Show an instance&#39;s runtime, state, and topology.
- `agent-team instance` - Manage agent instance state (.agent_team/state/&lt;instance&gt;/).
- `agent-team intake` - Normalize external events into topology events.
- `agent-team job` - Manage durable work units.
- `agent-team kill` - Force-stop running instances.
- `agent-team logs` - Show an instance&#39;s daemon-captured log.
- `agent-team monitor` - Show a combined health, instance, and resource snapshot.
- `agent-team next` - Print recommended next operator actions.
- `agent-team overview` - Show a concise operator overview across health, jobs, queue, pipelines, and schedules.
- `agent-team pipeline` - Inspect declared pipeline workflows.
- `agent-team plan` - Preview desired agent instance state from topology and daemon metadata.
- `agent-team prune` - Remove finished daemon-managed instances.
- `agent-team ps` - List instances (daemon-aware: merges live daemon state with on-disk status).
- `agent-team queue` - Inspect and control persisted daemon event queue items.
- `agent-team reload` - Reload daemon topology and reconcile runtime metadata.
- `agent-team repair` - Recover common unhealthy orchestration state.
- `agent-team restart` - Restart or resume instances.
- `agent-team rm` - Remove instance state and daemon metadata.
- `agent-team run` - Launch an LLM runtime session as the named agent.
- `agent-team runtime` - Inspect the selected LLM runtime profile.
- `agent-team schedule` - Inspect and run declared schedule events.
- `agent-team send` - Send a mailbox message to a daemon-managed instance.
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
- `agent-team wait` - Wait for daemon-managed instances to reach a lifecycle condition.
- `agent-team watch` - Watch the combined health, instance, and resource monitor.

## `agent-team attach`

Open an interactive runtime session against a daemon-managed persistent instance.

Stop the daemon-managed child for &lt;instance&gt;, then exec `&lt;runtime&gt; --resume &lt;session-id&gt;` in your terminal so the conversation continues interactively. On exit, the daemon resumes supervision automatically — pass --no-resume to leave the instance stopped.

There is brief downtime during the handoff (Shape A): the daemon child is killed before the runtime resume command reattaches. Channel cursors and mailbox state survive the transfer.

Compatibility: log-oriented invocations such as --no-follow, --tail, --latest, --last, --all, or status/agent/phase filters follow the daemon-captured log stream, matching the older attach shortcut. `agent-team logs` is the preferred explicit command for log streaming.

```text
agent-team attach <instance> [flags]
```

Flags:

```text
      --agent strings     Log compatibility mode: only attach to instances for this agent. Can repeat or comma-separate.
  -a, --all               Log compatibility mode: attach to every daemon-known instance, prefixed by instance name.
      --dry-run           Preview the interactive handoff without stopping or resuming the daemon child.
      --grep string       Log compatibility mode with --no-follow: only print log lines matching this regular expression.
  -n, --last int          Log compatibility mode: attach to the N most recently started instances (0 = disabled).
      --latest            Log compatibility mode: attach to the most recently started instance.
      --no-follow         Log compatibility mode: print the selected log tail and exit instead of following.
      --no-resume         Leave the instance in stopped state when the runtime exits (default: re-dispatch via the daemon).
      --phase strings     Log compatibility mode: only attach to instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --runtime strings   Log compatibility mode: only attach to instances for this runtime: claude or codex. Can repeat or comma-separate.
      --since string      Log compatibility mode with --no-follow: only print the log if it was modified since this duration ago (for example 10m, 24h) or RFC3339 timestamp.
      --stale             Log compatibility mode: only attach to instances whose status.toml is stale.
      --status strings    Log compatibility mode: only attach to instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --tail string       Log compatibility mode: show only the last N lines before following (0 or all = all). (default "50")
      --target string     Repo root. (default "<repo>")
      --unhealthy         Log compatibility mode: only attach to crashed or stale instances.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team channel`

Manage daemon-managed pub/sub channels.

```text
agent-team channel
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --target string   Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team channel publish`

Publish a message to a channel from the CLI (creates the channel if missing).

```text
agent-team channel publish <name> <body...> [flags]
```

Flags:

```text
      --sender string   Sender label recorded with the message. (default "(cli)")
      --target string   Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team channel rm`

Delete a channel and all of its on-disk state.

```text
agent-team channel rm <name> [flags]
```

Flags:

```text
  -f, --force           Skip confirmation.
      --target string   Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team channel show`

Show one channel&#39;s summary plus its tail of recent messages.

```text
agent-team channel show <name> [flags]
```

Flags:

```text
      --target string   Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team channels`

List all pub/sub channels (alias for `channel ls`).

```text
agent-team channels [flags]
```

Flags:

```text
      --target string   Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team daemon logs` - Show the agent-teamd daemon log.
- `agent-team daemon reconcile` - Refresh daemon instance metadata against the live process table.
- `agent-team daemon restart` - Restart agent-teamd, reconciling existing instance metadata on boot.
- `agent-team daemon start` - Boot agent-teamd in this repo (detached by default; foreground with --detach=false).
- `agent-team daemon status` - Print whether agent-teamd is running in this repo, and its pid if so.
- `agent-team daemon stop` - Gracefully stop the running agent-teamd (SIGTERM, then SIGKILL after timeout).

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
      --target string   Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --target string   Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --json                     Emit machine-readable JSON. Requires detached mode.
      --ready-timeout duration   Maximum time to wait for restarted detached daemon readiness (0 = no timeout). (default 3s)
      --target string            Repo root. (default "<repo>")
      --timeout duration         Grace period for stopping the old daemon before SIGKILL escalation (0 = force immediately). (default 5s)
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --json                     Emit machine-readable JSON. Requires detached mode.
      --ready-timeout duration   Maximum time to wait for detached daemon readiness (0 = no timeout). (default 3s)
      --target string            Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team daemon status`

Print whether agent-teamd is running in this repo, and its pid if so.

```text
agent-team daemon status [flags]
```

Flags:

```text
      --down                With --wait, wait until agent-teamd is not running.
      --format string       Render daemon status with a Go template, e.g. '{{.Ready}} {{.PID}}'.
      --interval duration   Polling interval for --wait. (default 200ms)
      --json                Emit machine-readable JSON.
  -q, --quiet               Suppress output and use the exit code as a readiness probe.
      --target string       Repo root. (default "<repo>")
      --timeout duration    Maximum time to wait with --wait (0 = no timeout). (default 30s)
      --wait                Wait until agent-teamd is running and ready.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --target string      Repo root. (default "<repo>")
      --timeout duration   Grace period before SIGKILL escalation (0 = force immediately). (default 5s)
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team dispatch`

Dispatch an agent through daemon topology.

Dispatch an agent through daemon topology by publishing an `agent.dispatch` event. This is the human-friendly wrapper for the common manager-to-worker path.

```text
agent-team dispatch <target> <ticket> [kickoff...] [flags]
```

Flags:

```text
      --dry-run               Preview topology matches without publishing to the daemon.
      --format string         Render the event outcome or dry-run preview with a Go template.
      --json                  Emit the daemon event outcome as JSON.
      --kickoff string        Kickoff text for the dispatched agent.
      --kickoff-file string   Read kickoff text from a file.
      --name string           Requested instance name (default: <target>-<ticket-slug>).
      --runtime string        Runtime profile for the dispatched instance (claude or codex). Overrides env and repo config.
      --runtime-bin string    Runtime binary for the dispatched instance. Overrides env and repo config.
      --source string         Source instance for the dispatch event (default: AGENT_TEAM_INSTANCE or cli).
      --target string         Repo root. (default "<repo>")
      --workspace string      Workspace mode for spawned children: auto, worktree, or repo. (default "auto")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team docs`

Generate developer documentation from the command tree.

```text
agent-team docs [flags]
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team docs cli` - Generate a markdown CLI reference.

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
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team doctor`

Sanity-check the vendored team.

Sanity-check the vendored team: .agent_team/ layout, config.toml validity, template provenance, each agent&#39;s frontmatter, skill resolution across all agents, pipeline workflow wiring, the selected runtime binary, and whether the companion agent-teamd binary is available for daemon-backed lifecycle commands.

```text
agent-team doctor [flags]
```

Flags:

```text
      --format string     Render the doctor result with a Go template, e.g. '{{.OK}} {{len .Problems}}'.
      --json              Emit machine-readable JSON.
      --strict-daemon     Fail when the companion agent-teamd binary is not discoverable.
      --strict-runtime    Fail when the selected LLM runtime binary is not discoverable.
      --strict-template   Fail when .template.lock no longer matches its resolved template ref.
      --target string     Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team event`

Publish manual topology events to the daemon (for testing trigger matching).

```text
agent-team event
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team event publish` - Publish an event of the given type. The daemon resolves it against declared triggers.

## `agent-team event publish`

Publish an event of the given type. The daemon resolves it against declared triggers.

```text
agent-team event publish <type> [flags]
```

Flags:

```text
      --dry-run               Preview matching triggers without publishing to the daemon.
      --format string         Render the event outcome or dry-run preview with a Go template, e.g. '{{len .Matched}} {{len .Dispatched}}'.
      --json                  Emit the daemon event outcome as JSON.
      --payload string        JSON object passed as the event payload (e.g. '{"target":"worker"}').
      --payload-file string   Read event payload JSON from a file, or '-' for stdin.
      --target string         Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --format string      Render each event with a Go template, e.g. '{{.Action}} {{.Instance}} {{.Status}}'.
      --instance strings   Only show events for this instance. Can repeat or comma-separate.
      --json               Emit raw JSONL events.
  -n, --last int           Show events for the N most recently started daemon-known instances after other filters (0 = all).
      --latest             Show events for the most recently started daemon-known instance after other filters.
      --phase strings      Only show events for instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --since string       Only show events since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --stale              Only show events for instances whose status.toml is currently stale.
      --status strings     Only show events with this lifecycle status. Can repeat or comma-separate.
      --summary            Summarize matching events by action, status, agent, and instance.
      --tail int           Show only the last N events before returning or following (0 = all). With non-following filters, N applies after filtering.
      --target string      Repo root. (default "<repo>")
      --unhealthy          Only show events for instances that are currently crashed or stale.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team health`

Check daemon and instance fleet health.

Check the daemon, declared persistent instances, crashed instances, and stale status files. One-shot checks exit 0 when healthy and 1 when unhealthy.

```text
agent-team health [flags]
```

Flags:

```text
      --agent strings       Only check declared and daemon-known instances for this agent. Daemon health remains global. Can repeat or comma-separate.
      --format string       Render the health result with a Go template, e.g. '{{.Healthy}} {{.Summary.Running}}'.
      --instance strings    Only check instances with this name. Daemon health remains global. Can repeat or comma-separate.
      --interval duration   Refresh interval for --watch or --wait. (default 2s)
      --jobs                Include durable job triage and status-file previews; treat jobs needing attention as unhealthy.
      --json                Emit machine-readable JSON.
  -n, --last int            Only check the N most recently started instances after other filters (0 = all). Daemon health remains global.
      --latest              Only check the most recently started instance after other filters. Daemon health remains global.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --phase strings       Only check instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
  -q, --quiet               Suppress output and use only the exit code.
      --stale               Only check instances whose status.toml is stale.
      --status strings      Only check instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --strict-topology     Treat running daemon-known instances not declared in instances.toml as unhealthy.
      --target string       Repo root. (default "<repo>")
      --timeout duration    Maximum time to wait with --wait (0 = no timeout).
      --unhealthy           Only check crashed or stale instances. Daemon health remains global.
      --wait                Poll until the fleet is healthy, then exit.
  -w, --watch               Refresh health until interrupted.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team init`

Vendor a starter team template into the current repo (creates .agent_team/).

Vendor a template into the current repo (creates .agent_team/). With no ref, the bundled
default template is used (a software-engineering team — manager + worker + ticket-manager,
plus linear / pull-request / assign-worker skills). Pass `--template empty` for a scaffold-
only init. `--set k=v` supplies template parameters; `--no-input` fails (rather than prompting)
when required parameters have no value.

```text
agent-team init [<ref>] [flags]
```

Flags:

```text
      --force              Overwrite existing .agent_team/ files (config.toml is never overwritten).
      --no-input           Fail with a clear error if required parameters are missing instead of prompting.
      --set stringArray    Set a template parameter, e.g. --set linear.team_id=<uuid>. Repeatable.
      --target string      Target repo root. (default "<repo>")
      --template default   default (uses the supplied/bundled template ref) or `empty` (scaffold only, no manifest). (default "default")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --stale              Only inspect instances whose status.toml is stale.
      --status strings     Only inspect instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --target string      Repo root. (default "<repo>")
      --unhealthy          Only inspect crashed or stale instances.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team instance`

Manage agent instance state (.agent_team/state/&lt;instance&gt;/).

```text
agent-team instance
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team instance down` - Stop declared persistent instances. With no args, stops all running.
- `agent-team instance ls` - List instances (state dirs).
- `agent-team instance ps` - List instances with their current status (Docker-ps style).
- `agent-team instance rm` - Remove an instance&#39;s state.
- `agent-team instance show` - Show an instance&#39;s state files.
- `agent-team instance up` - Start or resume instances (idempotent). Requires the daemon.

## `agent-team instance down`

Stop declared persistent instances. With no args, stops all running.

```text
agent-team instance down [<name>...] [flags]
```

Flags:

```text
      --agent strings           Stop every running instance for this agent. Can repeat or comma-separate.
      --dry-run                 Preview planned stop actions without changing daemon state.
  -f, --force                   Escalate to SIGKILL if an instance does not stop within --timeout.
      --format string           Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                    Emit machine-readable JSON.
  -n, --last int                Stop the N most recently started running instances after other filters (0 = all).
      --latest                  Stop the most recently started running instance after other filters.
      --phase strings           Stop daemon-known instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --rm                      Remove selected instance state and daemon metadata after stopping.
      --stale                   Only stop instances whose status.toml is stale.
      --status strings          Stop daemon-known instances currently in this lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary                 Show aggregate action counts instead of per-instance rows.
      --target string           Repo root. (default "<repo>")
      --timeout duration        Grace before --force kills. With --wait and no --wait-timeout, also used as the wait deadline (0 = no wait deadline; force defaults to 10s).
      --unhealthy               Only stop instances that are crashed or stale.
      --wait                    Wait for stopped instances to reach a terminal state.
      --wait-timeout duration   Maximum time to wait for terminal state with --wait. Defaults to --timeout when unset; set 0 explicitly for no wait timeout.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team instance ls`

List instances (state dirs).

```text
agent-team instance ls [flags]
```

Flags:

```text
      --target string   Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team instance ps`

List instances with their current status (Docker-ps style).

Walks .agent_team/state/*/status.toml and renders one row per instance. Instances with a state dir but no status.toml render with `—` placeholders so they remain visible. Non-idle/non-done rows whose status.toml is older than the configured health policy threshold are flagged `(stale)`.

```text
agent-team instance ps [flags]
```

Flags:

```text
      --target string   Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team instance rm`

Remove an instance&#39;s state.

```text
agent-team instance rm [<name>...] [flags]
```

Flags:

```text
      --agent strings    With --all, --finished, --latest, --last, --status, --phase, --stale, or --unhealthy, only remove daemon-known instances for this agent. Can repeat or comma-separate.
  -a, --all              Remove every daemon-known instance. Can combine with --agent, --status, --phase, --stale, or --unhealthy.
      --dry-run          Preview matching removals without deleting state or daemon metadata.
      --finished         Remove every daemon-known exited or crashed instance.
  -f, --force            Skip confirmation; if the daemon is running, stop a running instance before removal.
      --format string    Render each removal result with a Go template, e.g. '{{.Instance}} {{.Path}}'. Requires --force unless --dry-run is set.
      --json             Emit machine-readable JSON. Requires --force unless --dry-run is set.
  -n, --last int         Remove the N most recently started daemon-known instances after other filters (0 = all).
      --latest           Remove the most recently started daemon-known instance after other filters.
      --phase strings    Remove daemon-known instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --stale            Remove only daemon-known instances whose non-idle work phase has stale status telemetry.
      --status strings   Remove daemon-known instances currently in this lifecycle status: stopped, exited, crashed, running, or unknown. Can repeat or comma-separate.
      --summary          Show aggregate removal counts instead of per-instance rows.
      --target string    Repo root. (default "<repo>")
      --unhealthy        Remove only daemon-known instances that are crashed or stale.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team instance show`

Show an instance&#39;s state files.

```text
agent-team instance show <name> [flags]
```

Flags:

```text
      --json            Emit machine-readable JSON.
      --target string   Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team instance up`

Start or resume instances (idempotent). Requires the daemon.

```text
agent-team instance up [<name>...] [flags]
```

Flags:

```text
      --agent strings      Start or resume every declared persistent and daemon-known instance for this agent. Can repeat or comma-separate.
  -a, --all                Start or resume every declared persistent and daemon-known instance.
      --attach             Follow the selected instance log after starting or resuming. Requires exactly one selected instance.
      --dry-run            Preview planned start/resume actions without changing daemon state.
      --format string      Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json               Emit machine-readable JSON.
  -n, --last int           Start or resume the N most recently started instances after other filters (0 = all).
      --latest             Start or resume the most recently started instance after other filters.
      --phase strings      Only start or resume instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --prompt string      Override the default kickoff prompt.
      --stale              Only start or resume instances whose status.toml is stale.
      --status strings     Only start or resume instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary            Show aggregate action counts instead of per-instance rows.
      --tail string        With --attach, show only the last N lines before following (0 or all = all). (default "50")
      --target string      Repo root. (default "<repo>")
      --timeout duration   Maximum time to wait with --wait (0 = no timeout).
      --unhealthy          Only start or resume instances that are crashed or stale.
      --wait               Wait for selected instances to become healthy after starting. With no scoped selection, waits for the fleet.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team intake`

Normalize external events into topology events.

Normalize external events such as Linear/GitHub webhooks and schedules into topology events handled by the daemon.

```text
agent-team intake
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team intake deliveries` - List recent intake server deliveries.
- `agent-team intake doctor` - Validate the recorded intake delivery ledger.
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
      --format string          Render each delivery with a Go template, e.g. '{{.Provider}} {{.Status}} {{.EventType}}'.
      --json                   Emit deliveries as JSON.
      --provider string        Only show deliveries for a provider: linear or github.
      --replay-status string   Only show deliveries with replay status: ok, error, none, or any.
      --request-id string      Only show deliveries with this provider request id, such as X-GitHub-Delivery.
      --status string          Only show deliveries with a status: ok or error.
      --tail string            Show only the last N deliveries (0 or all = all). (default "20")
      --target string          Repo root. (default "<repo>")
      --unresolved             Only show failed deliveries that still need replay.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team intake doctor`

Validate the recorded intake delivery ledger.

```text
agent-team intake doctor [flags]
```

Flags:

```text
      --format string   Render the intake doctor result with a Go template, e.g. '{{.OK}} {{len .Problems}}'.
      --json            Emit ledger doctor findings as JSON.
      --target string   Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team intake github`

Normalize a github webhook payload and publish it.

```text
agent-team intake github [flags]
```

Flags:

```text
      --cleanup-merged        With --reconcile-job, remove the job-owned worktree and branch after a merged PR event.
      --dry-run               Normalize and print the event without publishing to the daemon.
      --format string         Render the intake result with a Go template, e.g. '{{.Event.Type}}'.
      --json                  Emit normalized event and daemon outcome as JSON.
      --payload string        Webhook JSON object.
      --payload-file string   Read webhook JSON from a file, or '-' for stdin.
      --preview-triggers      With --dry-run, include local topology instance and pipeline matches.
      --reconcile-job         Also reconcile the normalized PR event into the owning durable job.
      --target string         Repo root. (default "<repo>")
      --verify-pr             With --cleanup-merged, verify the recorded GitHub PR is merged with gh before cleanup.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team intake linear`

Normalize a linear webhook payload and publish it.

```text
agent-team intake linear [flags]
```

Flags:

```text
      --dry-run               Normalize and print the event without publishing to the daemon.
      --format string         Render the intake result with a Go template, e.g. '{{.Event.Type}}'.
      --json                  Emit normalized event and daemon outcome as JSON.
      --payload string        Webhook JSON object.
      --payload-file string   Read webhook JSON from a file, or '-' for stdin.
      --preview-triggers      With --dry-run, include local topology instance and pipeline matches.
      --target string         Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team intake prune`

Prune recorded intake deliveries.

Prune recorded intake deliveries. By default this removes successful deliveries and keeps failures for recovery.

```text
agent-team intake prune [flags]
```

Flags:

```text
      --dry-run                Preview deliveries that would be pruned without rewriting the ledger.
      --format string          Render each prune result with a Go template, e.g. '{{.ID}} {{.Status}} {{.Dropped}}'.
      --json                   Emit prune results as JSON.
      --older-than duration    Only prune deliveries older than this duration.
      --replay-status string   Only prune deliveries with replay status: ok, error, none, or any. Defaults --status to all when set.
      --status string          Delivery status to prune: ok, error, or all. (default "ok")
      --target string          Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team intake replay`

Replay a recorded normalized intake delivery.

```text
agent-team intake replay [delivery-id] [flags]
```

Flags:

```text
      --all                Replay all matching recorded deliveries.
      --dry-run            Preview the normalized delivery without publishing it.
      --format string      Render the replay result with a Go template, e.g. '{{.Event.Type}}'.
      --json               Emit replay result as JSON.
      --limit int          With --all, replay at most this many matching deliveries (0 = all).
      --preview-triggers   With --dry-run, include local topology instance and pipeline matches.
      --provider string    With --all, only replay deliveries for a provider: linear or github.
      --status string      With --all, delivery status to replay: ok, error, or all. error skips recovered replays. (default "error")
      --target string      Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team intake schedule`

Publish a named schedule event.

```text
agent-team intake schedule <name> [flags]
```

Flags:

```text
      --dry-run               Normalize and print the event without publishing to the daemon.
      --format string         Render the intake result with a Go template, e.g. '{{.Event.Type}}'.
      --json                  Emit normalized event and daemon outcome as JSON.
      --payload string        Additional JSON object merged into the schedule payload.
      --payload-file string   Read additional schedule payload JSON from a file, or '-' for stdin.
      --preview-triggers      With --dry-run, include local topology instance and pipeline matches.
      --target string         Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team intake serve`

Run a local HTTP listener for external webhook intake.

```text
agent-team intake serve [flags]
```

Flags:

```text
      --addr string                           Address for the webhook listener. (default "127.0.0.1:8787")
      --dry-run                               Normalize requests and return previews without publishing to the daemon.
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
      --target string                         Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --target string                         Repo root. (default "<repo>")
      --tls-secret string                     Kubernetes TLS Secret name for --ingress-host; kubernetes output only.
      --workspace-claim string                Kubernetes PersistentVolumeClaim name mounted at --container-workdir; defaults to <name>-workspace.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team intake summary`

Summarize recorded intake deliveries.

```text
agent-team intake summary [flags]
```

Flags:

```text
      --format string          Render the summary with a Go template, e.g. '{{.Unresolved}} {{.Replayable}}'.
      --json                   Emit summary as JSON.
      --provider string        Only summarize deliveries for a provider: linear or github.
      --replay-status string   Only summarize deliveries with replay status: ok, error, none, or any.
      --request-id string      Only summarize deliveries with this provider request id, such as X-GitHub-Delivery.
      --status string          Only summarize deliveries with a status: ok or error.
      --target string          Repo root. (default "<repo>")
      --unresolved             Only summarize failed deliveries that still need replay.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team job`

Manage durable work units.

Manage durable work units backed by `.agent_team/jobs/&lt;job-id&gt;.toml`. Jobs track ticket ownership, target agent, lifecycle state, instance, branch, worktree, and PR metadata.

```text
agent-team job
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team job advance` - Dispatch the next ready step in a pipeline job.
- `agent-team job attach` - Attach to a job&#39;s owning instance.
- `agent-team job cleanup` - Remove a job-owned worker worktree and branch after merge.
- `agent-team job close` - Close a job as done or failed.
- `agent-team job create` - Create a durable job for a ticket.
- `agent-team job dispatch` - Dispatch a job to its target agent.
- `agent-team job events` - Show a job&#39;s durable event history.
- `agent-team job kill` - Force-stop a job&#39;s owning instance.
- `agent-team job logs` - Show a job&#39;s owning instance log.
- `agent-team job ls` - List durable jobs.
- `agent-team job next` - Show the next pipeline step for a job without dispatching it.
- `agent-team job prune` - Remove terminal job files and their event logs.
- `agent-team job queue` - List queue items owned by one job.
- `agent-team job ready` - List pipeline jobs with ready or selected next-step states.
- `agent-team job reconcile` - Reconcile external runtime state back into jobs.
- `agent-team job reopen` - Reopen a durable job for another attempt.
- `agent-team job rm` - Remove job files and their event logs.
- `agent-team job send` - Send a mailbox message to a job&#39;s owning instance.
- `agent-team job show` - Show one durable job.
- `agent-team job start` - Start or resume a job&#39;s owning instance.
- `agent-team job step` - Update a pipeline job step status.
- `agent-team job stop` - Stop a job&#39;s owning instance.
- `agent-team job triage` - Show jobs that need operator attention.
- `agent-team job unblock` - Answer a blocked job and mark it ready to continue.
- `agent-team job update` - Update job metadata.
- `agent-team job wait` - Wait for a job to reach a lifecycle status.

## `agent-team job advance`

Dispatch the next ready step in a pipeline job.

```text
agent-team job advance <job-id> [flags]
```

Flags:

```text
      --dry-run              Preview the next ready step dispatch without changing daemon or job state.
      --format string        Render the advance preview or result with a Go template, e.g. '{{.Job.ID}} {{.Step.ID}}'.
      --json                 Emit the updated job and daemon event outcome as JSON.
      --repo string          Repo root. (default "<repo>")
      --runtime string       Runtime profile for the advanced step dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string   Runtime binary for the advanced step dispatch. Overrides env and repo config.
      --workspace string     Workspace mode for the advanced step: auto, worktree, or repo. (default "auto")
```

## `agent-team job attach`

Attach to a job&#39;s owning instance.

Attach to the instance recorded on a durable job. By default this opens the owning instance with the normal interactive attach flow. Passing log options such as --tail, --no-follow, --since, or --grep follows the daemon-captured log stream instead.

```text
agent-team job attach <job-id> [flags]
```

Flags:

```text
      --dry-run        Preview the owning instance handoff without stopping or resuming the daemon child.
      --grep string    Log mode with --no-follow: only print log lines matching this regular expression.
      --no-follow      Log mode: print the selected log tail and exit instead of following.
      --no-resume      Leave the owning instance in stopped state when the runtime exits.
      --repo string    Repo root. (default "<repo>")
      --since string   Log mode with --no-follow: only print the log if it was modified since this duration ago (for example 10m, 24h) or RFC3339 timestamp.
      --tail string    Log mode: show only the last N lines before following (0 or all = all). (default "50")
```

## `agent-team job cleanup`

Remove a job-owned worker worktree and branch after merge.

```text
agent-team job cleanup <job-id>|--all [flags]
```

Flags:

```text
      --all             Clean all done jobs that still own a recorded worktree or branch.
      --dry-run         Preview the job-owned worktree and branch cleanup without removing anything.
      --force-branch    With --merged, delete the job branch with git branch -D if it is not locally merged.
      --format string   Render the cleanup result with a Go template, e.g. '{{.ID}} {{.LastStatus}}' or '{{.Total}} {{.Cleaned}}'.
      --json            Emit the updated job as JSON.
      --merged          Confirm the job's PR has merged before removing its worktree and branch.
      --repo string     Repo root. (default "<repo>")
      --verify-pr       Verify the recorded GitHub PR is merged with gh before cleanup.
```

## `agent-team job close`

Close a job as done or failed.

```text
agent-team job close <job-id> [flags]
```

Flags:

```text
      --format string   Render the updated job with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --json            Emit the updated job as JSON.
      --repo string     Repo root. (default "<repo>")
      --status string   Close status: done or failed. (default "done")
```

## `agent-team job create`

Create a durable job for a ticket.

```text
agent-team job create <ticket> [kickoff...] [flags]
```

Flags:

```text
      --dispatch              Dispatch the created job immediately using the running daemon.
      --dry-run               Preview the job that would be created without writing it.
      --format string         Render the job with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --id string             Override the normalized job id (default: ticket slug).
      --instance string       Instance name that owns the job (default set during dispatch).
      --json                  Emit the job as JSON.
      --kickoff string        Kickoff text for the target agent.
      --kickoff-file string   Read kickoff text from a file.
      --pipeline string       Create this job from a declared pipeline in instances.toml.
      --repo string           Repo root. (default "<repo>")
      --runtime string        Runtime profile for --dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string    Runtime binary for --dispatch. Overrides env and repo config.
      --target string         Target agent that should own this job. (default "worker")
      --ticket-url string     Canonical ticket URL to store on the job.
      --workspace string      Workspace mode for --dispatch: auto, worktree, or repo. (default "auto")
```

## `agent-team job dispatch`

Dispatch a job to its target agent.

```text
agent-team job dispatch <job-id> [flags]
```

Flags:

```text
      --dry-run              Preview topology matches without publishing to the daemon or updating the job.
      --format string        Render the updated job or dry-run preview with a Go template.
      --json                 Emit the updated job and daemon event outcome as JSON.
      --repo string          Repo root. (default "<repo>")
      --runtime string       Runtime profile for the dispatched instance (claude or codex). Overrides env and repo config.
      --runtime-bin string   Runtime binary for the dispatched instance. Overrides env and repo config.
      --source string        Source instance for the dispatch event (default: AGENT_TEAM_INSTANCE or cli).
      --workspace string     Workspace mode for spawned children: auto, worktree, or repo. (default "auto")
```

## `agent-team job events`

Show a job&#39;s durable event history.

```text
agent-team job events <job-id> [flags]
```

Flags:

```text
      --actor strings       Only show job events from this actor. Can repeat or comma-separate.
  -f, --follow              Poll and print new job events until interrupted.
      --format string       Render each event with a Go template, e.g. '{{.TS}} {{.Type}} {{.Message}}'.
      --interval duration   Polling interval for --follow. (default 1s)
      --json                Emit machine-readable JSON. With --follow, emit one JSON object per line.
      --repo string         Repo root. (default "<repo>")
      --since string        Only show job events since this duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --tail string         Show only the last N events before returning or following (0 or all = all). (default "0")
      --type strings        Only show job events with this type. Can repeat or comma-separate.
```

## `agent-team job kill`

Force-stop a job&#39;s owning instance.

```text
agent-team job kill <job-id> [flags]
```

Flags:

```text
      --dry-run                 Preview the kill action without changing daemon or job state.
      --format string           Render the lifecycle action with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                    Emit machine-readable lifecycle action JSON.
  -q, --quiet                   Suppress non-error output and use only the exit code.
      --repo string             Repo root. (default "<repo>")
      --rm                      Remove selected instance state and daemon metadata after killing.
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
  -f, --follow         Tail the owning instance log; print new bytes as they appear.
      --grep string    Only print log lines matching this regular expression. One-shot reads only.
      --last-message   Show the clean final Codex response sidecar for the owning instance.
      --repo string    Repo root. (default "<repo>")
      --since string   Only print the log if it was modified since a duration ago (for example 10m, 24h) or RFC3339 timestamp.
      --tail string    Show only the last N lines before returning or following (0 or all = all). (default "0")
```

## `agent-team job ls`

List durable jobs.

```text
agent-team job ls [flags]
```

Flags:

```text
      --branch string         Filter by branch.
      --format string         Render each job with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --instance string       Filter by owning instance.
      --interval duration     Refresh interval for --watch. (default 2s)
      --json                  Emit machine-readable JSON.
      --no-clear              With --watch, append snapshots instead of redrawing the terminal.
      --pipeline string       Filter by pipeline name.
      --pr string             Filter by PR URL or number substring.
      --repo string           Repo root. (default "<repo>")
      --sort string           Sort rows by id, status, target, ticket, created, updated, instance, branch, or pr. (default "id")
      --status string         Filter by status: queued, running, blocked, done, or failed.
      --summary               Show aggregate job counts instead of job rows.
      --target-agent string   Filter by target agent.
      --ticket string         Filter by ticket id or URL substring.
  -w, --watch                 Refresh the job table until interrupted.
```

## `agent-team job next`

Show the next pipeline step for a job without dispatching it.

```text
agent-team job next <job-id> [flags]
```

Flags:

```text
      --format string   Render the next-step state with a Go template, e.g. '{{.State}} {{.Step.ID}}'.
      --json            Emit the next-step state as JSON.
      --repo string     Repo root. (default "<repo>")
```

## `agent-team job prune`

Remove terminal job files and their event logs.

Remove jobs in terminal statuses. By default, this removes done and failed jobs.

```text
agent-team job prune [flags]
```

Flags:

```text
      --dry-run          Preview removals without deleting files.
      --format string    Render each result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --json             Emit removal results as JSON.
      --repo string      Repo root. (default "<repo>")
      --status strings   Terminal status to prune: done, failed, or terminal. Can repeat or comma-separate.
```

## `agent-team job queue`

List queue items owned by one job.

List persisted daemon queue items owned by one durable job.

```text
agent-team job queue <job-id> [flags]
```

Flags:

```text
      --event-type strings   Filter by event type; repeat or comma-separate values.
      --format string        Render each queue item with a Go template, e.g. '{{.ID}} {{.State}}'.
      --json                 Emit machine-readable JSON.
      --ready                Only show pending queue items whose next retry is due now.
      --repo string          Repo root. (default "<repo>")
      --state string         Filter by queue state: pending or dead.
      --summary              Show aggregate queue counts instead of queue rows.
```

Subcommands:

- `agent-team job queue drop` - Drop queue items owned by one job.
- `agent-team job queue prune` - Prune queue items owned by one job.
- `agent-team job queue quarantine` - List quarantined queue files owned by one job.
- `agent-team job queue retry` - Retry queue items owned by one job.

## `agent-team job queue drop`

Drop queue items owned by one job.

Drop one job-owned queue item by id, or drop a filtered job-owned batch with --all. Batch drops default to dead-letter items.

```text
agent-team job queue drop <job-id> [id] [flags]
```

Flags:

```text
      --all                  Drop all matching job-owned queue items instead of one id.
      --dry-run              Preview matching job-owned queue items without dropping them.
      --event-type strings   With --all, filter by event type; repeat or comma-separate values.
      --format string        Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --json                 Emit machine-readable JSON.
      --limit int            With --all, drop at most this many matching queue items; 0 means no limit.
      --ready                With --all, only drop pending queue items whose next retry is due now.
      --repo string          Repo root. (default "<repo>")
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
      --dry-run               Preview job-owned queue items that would be pruned without dropping them.
      --format string         Render each prune result with a Go template, e.g. '{{.ID}} {{.State}}'.
      --json                  Emit prune results as JSON.
      --older-than duration   Only prune job-owned items older than this duration based on retry/dead-letter/update time.
      --repo string           Repo root. (default "<repo>")
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
      --event-type strings   Filter by event type; repeat or comma-separate values.
      --format string        Render each quarantined queue file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.
      --json                 Emit quarantined queue files as JSON.
      --repo string          Repo root. (default "<repo>")
      --restorable           Only show quarantined files that can be restored.
      --state string         Filter by queue state: pending or dead.
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
      --dry-run               Preview quarantined files that would be dropped.
      --event-type strings    With --all, filter by event type; repeat or comma-separate values.
      --format string         Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --json                  Emit drop results as JSON.
      --older-than duration   With --all, only drop files older than this duration based on file mtime.
      --repo string           Repo root. (default "<repo>")
      --restorable            With --all, only drop quarantined files that can be restored.
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
      --dry-run              Preview the restore without moving files.
      --event-type strings   With --all, filter by event type; repeat or comma-separate values.
      --force                Overwrite an existing active queue file with the same restore path.
      --format string        Render each restore result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --json                 Emit restore result as JSON.
      --repo string          Repo root. (default "<repo>")
      --state string         With --all, filter by queue state: pending or dead.
```

## `agent-team job queue quarantine show`

Show one job-owned quarantined queue file.

```text
agent-team job queue quarantine show <job-id> <quarantine-path> [flags]
```

Flags:

```text
      --format string   Render the quarantined queue file with a Go template, e.g. '{{.ID}} {{.State}}'.
      --json            Emit the quarantined queue file as JSON.
      --repo string     Repo root. (default "<repo>")
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
      --dry-run              Preview matching job-owned queue items without retrying them.
      --event-type strings   With --all, filter by event type; repeat or comma-separate values.
      --format string        Render each retry result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --json                 Emit machine-readable JSON.
      --limit int            With --all, retry at most this many matching queue items; 0 means no limit.
      --ready                With --all, only retry pending queue items whose next retry is due now.
      --repo string          Repo root. (default "<repo>")
      --state string         With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.
```

## `agent-team job ready`

List pipeline jobs with ready or selected next-step states.

```text
agent-team job ready [flags]
```

Flags:

```text
      --format string     Render each row with a Go template, e.g. '{{.JobID}} {{.State}} {{.StepID}}'.
      --json              Emit ready rows as JSON.
      --pipeline string   Filter by pipeline name.
      --repo string       Repo root. (default "<repo>")
      --state strings     Next-step state to include: ready, queued, running, blocked, failed, done, none, or all. Can repeat or comma-separate.
```

## `agent-team job reconcile`

Reconcile external runtime state back into jobs.

```text
agent-team job reconcile
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --dry-run         Preview job updates without writing them.
      --format string   Render each result with a Go template, e.g. '{{.JobID}} {{.After}}'.
      --json            Emit machine-readable JSON.
      --repo string     Repo root. (default "<repo>")
```

## `agent-team job reconcile github`

Reconcile a GitHub PR webhook payload with its owning job.

```text
agent-team job reconcile github [flags]
```

Flags:

```text
      --cleanup-merged        After a merged PR event, remove the job-owned worktree and branch.
      --dry-run               Preview the owning job update without writing it.
      --format string         Render the reconciled job with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --json                  Emit the normalized event and reconciled job as JSON.
      --payload string        GitHub webhook JSON object.
      --payload-file string   Read GitHub webhook JSON from a file, or '-' for stdin.
      --repo string           Repo root. (default "<repo>")
      --verify-pr             With --cleanup-merged, verify the recorded GitHub PR is merged with gh before cleanup.
```

## `agent-team job reconcile queue`

Reconcile persisted queue state back into owning jobs.

```text
agent-team job reconcile queue [flags]
```

Flags:

```text
      --dry-run         Preview job updates without writing them.
      --format string   Render each result with a Go template, e.g. '{{.JobID}} {{.After}}'.
      --json            Emit machine-readable JSON.
      --repo string     Repo root. (default "<repo>")
      --state string    Queue state to reconcile: pending, dead, or all. (default "all")
```

## `agent-team job reconcile status`

Reconcile instance status.toml files back into owning jobs.

```text
agent-team job reconcile status [flags]
```

Flags:

```text
      --dry-run         Preview job updates without writing them.
      --format string   Render each result with a Go template, e.g. '{{.JobID}} {{.After}}'.
      --json            Emit machine-readable JSON.
      --repo string     Repo root. (default "<repo>")
```

## `agent-team job reopen`

Reopen a durable job for another attempt.

Reopen a durable job by resetting its lifecycle status to queued or blocked. Running jobs are refused unless --force is set. Pass --dispatch to immediately send the reopened job to its target.

```text
agent-team job reopen <job-id> [flags]
```

Aliases: `retry`

Flags:

```text
      --dispatch             Dispatch the reopened job immediately using the running daemon.
      --dry-run              Preview the reopened job and optional dispatch without writing job or daemon state.
  -f, --force                Allow reopening a job currently marked running.
      --format string        Render the updated job or dry-run preview with a Go template.
      --json                 Emit the updated job or dry-run preview as JSON.
      --message string       Status message recorded on the job.
      --repo string          Repo root. (default "<repo>")
      --runtime string       Runtime profile for --dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string   Runtime binary for --dispatch. Overrides env and repo config.
      --source string        Source instance for --dispatch (default: AGENT_TEAM_INSTANCE or cli).
      --status string        Reopened status: queued or blocked. (default "queued")
      --workspace string     Workspace mode for --dispatch: auto, worktree, or repo. (default "auto")
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
      --dry-run         Preview removals without deleting files.
  -f, --force           Allow removing queued, running, or blocked jobs.
      --format string   Render each result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --json            Emit removal results as JSON.
      --repo string     Repo root. (default "<repo>")
```

## `agent-team job send`

Send a mailbox message to a job&#39;s owning instance.

```text
agent-team job send <job-id> [message...] [flags]
```

Flags:

```text
      --allow-missing         Allow queueing a message for an instance the daemon does not know yet.
      --format string         Render the updated job with a Go template, e.g. '{{.ID}} {{.LastEvent}}'.
      --from string           Sender label recorded with the message. (default "(cli)")
      --json                  Emit the updated job as JSON.
      --message string        Message text to send.
      --message-file string   Read message text from a file, or '-' for stdin.
      --repo string           Repo root. (default "<repo>")
```

## `agent-team job show`

Show one durable job.

```text
agent-team job show <job-id> [flags]
```

Flags:

```text
      --events string   Include the last N job events in the detail output, or all. (default "5")
      --format string   Render the job with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --json            Emit the job as JSON.
      --repo string     Repo root. (default "<repo>")
```

## `agent-team job start`

Start or resume a job&#39;s owning instance.

```text
agent-team job start <job-id> [flags]
```

Flags:

```text
      --dry-run                  Preview the start/resume action without changing daemon or job state.
      --format string            Render the lifecycle action with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                     Emit machine-readable lifecycle action JSON.
  -q, --quiet                    Suppress non-error output and use only the exit code.
      --ready-timeout duration   Maximum time to wait for implicit daemon readiness (0 = no timeout). (default 3s)
      --repo string              Repo root. (default "<repo>")
      --timeout duration         Maximum time to wait with --wait (0 = no timeout).
      --wait                     Wait for the owning instance to become healthy after starting or resuming.
```

## `agent-team job step`

Update a pipeline job step status.

```text
agent-team job step <job-id> <step-id> [flags]
```

Flags:

```text
      --advance              After marking the step done, dispatch the next ready step.
      --branch string        Branch name to record on the job.
      --dry-run              Preview the step update and optional advance dispatch without writing job or daemon state.
      --format string        Render the updated job or advance result with a Go template, e.g. '{{.ID}} {{.Status}}' or '{{.Job.ID}} {{.Step.ID}}'.
      --instance string      Instance that owns or completed this step.
      --json                 Emit the updated job or advance result as JSON.
      --message string       Status message recorded on the job.
      --pr string            PR URL to record on the job.
      --repo string          Repo root. (default "<repo>")
      --runtime string       Runtime profile for --advance dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string   Runtime binary for --advance dispatch. Overrides env and repo config.
      --skip                 Mark this step as intentionally skipped; stored as done so dependent steps can continue.
      --status string        Step status: queued, running, blocked, done, or failed. (default "done")
      --workspace string     Workspace mode for an advanced step: auto, worktree, or repo. (default "auto")
      --worktree string      Worktree path to record on the job.
```

## `agent-team job stop`

Stop a job&#39;s owning instance.

```text
agent-team job stop <job-id> [flags]
```

Flags:

```text
      --dry-run                 Preview the stop action without changing daemon or job state.
  -f, --force                   Escalate to SIGKILL if the owning instance does not stop within --timeout.
      --format string           Render the lifecycle action with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                    Emit machine-readable lifecycle action JSON.
  -q, --quiet                   Suppress non-error output and use only the exit code.
      --repo string             Repo root. (default "<repo>")
      --rm                      Remove selected instance state and daemon metadata after stopping.
      --timeout duration        Grace before --force kills. With --wait and no --wait-timeout, also used as the wait deadline.
      --wait                    Wait for the owning instance to reach a terminal state.
      --wait-timeout duration   Maximum time to wait for terminal state with --wait.
```

## `agent-team job triage`

Show jobs that need operator attention.

Show a compact work queue triage view from durable jobs, persisted daemon queue items, status-file update previews, and ready pipeline steps.

```text
agent-team job triage [flags]
```

Flags:

```text
      --format string          Render the triage snapshot with a Go template, e.g. '{{.Summary.Total}} {{len .Attention}}'.
      --interval duration      Refresh interval for --watch. (default 2s)
      --json                   Emit triage snapshot as JSON.
      --min-severity string    Only show attention rows at least this severe: critical, warning, or info.
      --no-clear               With --watch, append snapshots instead of redrawing the terminal.
      --reason strings         Only show attention rows with this reason. Can repeat or comma-separate.
      --repo string            Repo root. (default "<repo>")
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
      --dry-run               Preview the unblock without sending a mailbox message or updating the job.
  -f, --force                 Allow unblocking a job not currently marked blocked.
      --format string         Render the updated job with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --from string           Sender label recorded with the unblock message. (default "(cli)")
      --json                  Emit the updated job as JSON.
      --message string        Message text to send.
      --message-file string   Read message text from a file, or '-' for stdin.
      --repo string           Repo root. (default "<repo>")
      --status string         Status after unblocking: running or queued. (default "running")
```

## `agent-team job update`

Update job metadata.

Update durable job metadata such as status, owner instance, branch, worktree, and PR URL.

```text
agent-team job update <job-id> [flags]
```

Flags:

```text
      --branch string       Set branch.
      --clear strings       Clear metadata fields: ticket-url, instance, branch, worktree, pr, or pipeline. Can repeat or comma-separate.
      --format string       Render the updated job with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --instance string     Set owning instance.
      --json                Emit the updated job as JSON.
      --message string      Status message recorded on the job.
      --pr string           Set PR URL or number.
      --repo string         Repo root. (default "<repo>")
      --status string       Set lifecycle status: queued, running, blocked, done, or failed.
      --target string       Set target agent.
      --ticket-url string   Set ticket URL.
      --worktree string     Set worktree path.
```

## `agent-team job wait`

Wait for a job to reach a lifecycle status.

Wait for a durable job to reach one of the requested lifecycle statuses. By default this waits for a terminal status: done or failed.

```text
agent-team job wait <job-id> [flags]
```

Flags:

```text
      --fail-on-failed      Exit 1 if the job resolves to failed.
      --format string       Render the final job with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --interval duration   Polling interval. (default 500ms)
      --json                Emit the final job as JSON.
  -q, --quiet               Suppress output and use only the exit code.
      --repo string         Repo root. (default "<repo>")
      --status strings      Status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.
      --timeout duration    Maximum time to wait (0 = no timeout).
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
      --dry-run                 Preview planned kill actions without changing daemon state.
      --format string           Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                    Emit machine-readable JSON.
  -n, --last int                Force-stop the N most recently started running instances after other filters (0 = all).
      --latest                  Force-stop the most recently started running instance after other filters.
      --phase strings           Force-stop daemon-known instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
  -q, --quiet                   Suppress non-error output and use only the exit code.
      --rm                      Remove selected instance state and daemon metadata after killing.
      --stale                   Only force-stop instances whose status.toml is stale.
      --status strings          Force-stop daemon-known instances currently in this lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary                 Show aggregate action counts instead of per-instance rows.
      --target string           Repo root. (default "<repo>")
      --timeout duration        Grace before SIGKILL escalation. (default 2s)
      --unhealthy               Only force-stop instances that are crashed or stale.
      --wait                    Wait for killed instances to reach a terminal state.
      --wait-timeout duration   Maximum time to wait for terminal state with --wait. Defaults to --timeout when unset; set 0 explicitly for no wait timeout.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --daemon            Show the agent-teamd daemon log instead of instance logs.
  -f, --follow            Tail the log; print new bytes as they appear.
      --format string     With --list, render each log stream with a Go template, e.g. '{{.Instance}} {{.LogPath}}'.
      --grep string       Only print log lines matching this regular expression. One-shot reads only.
      --json              Emit machine-readable JSON with --list.
  -n, --last int          Show logs for the N most recently started instances after other filters (0 = all).
      --last-message      Show the clean final Codex response sidecar instead of the raw runtime log.
      --latest            Show logs for the most recently started instance after other filters.
      --list              List daemon-known instance log streams instead of printing log content.
      --no-prefix         Do not prefix lines when streaming multiple instance logs.
      --phase strings     Only show logs for instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --runtime strings   Only show logs for this runtime: claude or codex. Can repeat or comma-separate.
      --since string      Only include log streams modified since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --stale             Only show logs for instances whose status.toml is stale.
      --status strings    Only show logs for lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --tail string       Show only the last N lines before returning or following (0 or all = all). (default "0")
      --target string     Repo root. (default "<repo>")
      --unhealthy         Only show logs for crashed or stale instances.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team monitor`

Show a combined health, instance, and resource snapshot.

Show a Docker-style operator snapshot combining fleet health, the instance table, and daemon-managed process stats. With --watch, refresh until interrupted.

```text
agent-team monitor [flags]
```

Flags:

```text
      --action strings         With --plan, only show plan rows with this action: start, resume, keep, unsupported, on-demand, stop, or extra. Can repeat or comma-separate.
      --agent strings          Only show instances, stats, and plan rows for this agent. Can repeat or comma-separate.
  -a, --all                    Include stopped, exited, and crashed daemon-managed instances in the stats section.
      --event-action strings   With --events, only show lifecycle events with this action. Can repeat or comma-separate.
      --events int             Include the last N matching daemon lifecycle events in the full monitor (0 = omit).
      --format string          Render monitor snapshots with a Go template, e.g. '{{.Health.Healthy}} {{len .Instances}}'.
      --instance strings       Only show instances, stats, and plan rows with this name. Can repeat or comma-separate.
      --interval duration      Refresh interval for --watch. (default 2s)
      --jobs                   Include durable job summary, attention, ready-step state, and status-file previews.
      --json                   Emit JSON. With --watch, writes one JSON object per refresh.
  -n, --last int               Show only the N most recently started instances after other filters (0 = all).
      --latest                 Show only the most recently started instance after other filters.
      --no-clear               With --watch, append snapshots instead of redrawing the terminal.
      --phase strings          Only show instances and stats in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --plan                   Include desired-state actions from instances.toml and daemon metadata.
      --resources              With --summary, include aggregate CPU, memory, and RSS totals.
      --schedules              Include due and upcoming declared schedule state.
      --since string           With --events, only show lifecycle events since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --sort string            Sort instance rows by name, status, agent, phase, stale, unhealthy, started, stopped, or exited. (default "name")
      --stale                  Only show instances whose status.toml is stale.
      --stats-sort string      Sort stats rows by name, cpu, mem, rss, status, agent, phase, stale, or unhealthy. (default "name")
      --status strings         Only show lifecycle status in instance, stats, and plan sections: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --stop-extras            With --plan, preview running topology extras as stop actions.
      --strict-topology        Treat running daemon-known instances not declared in instances.toml as unhealthy.
      --summary                Show compact non-failing fleet health and optional plan summaries instead of the full monitor.
      --target string          Repo root. (default "<repo>")
      --unhealthy              Only show crashed or stale instances.
  -w, --watch                  Refresh the monitor snapshot until interrupted.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team next`

Print recommended next operator actions.

Print recommended next operator actions from the read-only overview. Use --team to scope recommendations to one declared team.

```text
agent-team next [flags]
```

Flags:

```text
      --format string        Render the next-action result with a Go template, e.g. '{{.State}} {{len .Actions}}'.
      --interval duration    Refresh interval for --watch. (default 2s)
      --json                 Emit recommended actions as JSON.
      --limit int            Show at most this many actions; 0 means all.
      --no-clear             With --watch, append snapshots instead of redrawing the terminal.
      --schedule-limit int   Upcoming schedules to inspect while building recommendations; 0 means all. (default 5)
      --target string        Repo root. (default "<repo>")
      --team string          Scope recommendations to this declared team.
  -w, --watch                Refresh recommended actions until interrupted.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team overview`

Show a concise operator overview across health, jobs, queue, pipelines, and schedules.

Show a read-only operator overview with health, topology, job, queue, pipeline, schedule, and recommended next-action summaries.

```text
agent-team overview [flags]
```

Flags:

```text
      --format string        Render the overview result with a Go template, e.g. '{{.State}} {{len .Actions}}'.
      --interval duration    Refresh interval for --watch. (default 2s)
      --json                 Emit overview as JSON.
      --no-clear             With --watch, append snapshots instead of redrawing the terminal.
      --schedule-limit int   Upcoming schedules to inspect after ordering; 0 means all. (default 5)
      --target string        Repo root. (default "<repo>")
  -w, --watch                Refresh overview until interrupted.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team pipeline`

Inspect declared pipeline workflows.

Inspect pipeline declarations loaded from .agent_team/instances.toml.

```text
agent-team pipeline
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team pipeline advance` - Dispatch ready pipeline steps.
- `agent-team pipeline approve` - Approve blocked manual pipeline gates.
- `agent-team pipeline doctor` - Validate pipeline workflow wiring.
- `agent-team pipeline graph` - Render a declared pipeline step graph.
- `agent-team pipeline jobs` - List jobs for one pipeline.
- `agent-team pipeline ls` - List declared pipelines.
- `agent-team pipeline ready` - List ready pipeline jobs.
- `agent-team pipeline retry` - Reset failed pipeline steps for another attempt.
- `agent-team pipeline run` - Create a durable job from a pipeline declaration.
- `agent-team pipeline show` - Show one declared pipeline.
- `agent-team pipeline status` - Summarize pipeline jobs and next steps.

## `agent-team pipeline advance`

Dispatch ready pipeline steps.

Dispatch ready next steps for jobs in one pipeline, or across all pipelines with --all, using the same path as `agent-team job advance`.

```text
agent-team pipeline advance <pipeline>|--all [flags]
```

Flags:

```text
      --all                  Advance ready steps across all pipelines.
      --dry-run              Preview ready steps without dispatching them.
      --format string        Render each result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.
      --json                 Emit advance results as JSON.
      --limit int            Advance at most this many ready jobs; 0 means no limit.
      --preview-routes       With --dry-run, include local topology route and dispatch payload previews.
      --repo string          Repo root. (default "<repo>")
      --runtime string       Runtime profile for advanced step dispatches (claude or codex). Overrides env and repo config.
      --runtime-bin string   Runtime binary for advanced step dispatches. Overrides env and repo config.
      --workspace string     Workspace mode for advanced steps: auto, worktree, or repo. (default "auto")
```

## `agent-team pipeline approve`

Approve blocked manual pipeline gates.

Approve blocked manual-gate steps for jobs in one pipeline, or all pipelines with --all. By default this marks matching manual gates queued; pass --step to target one stage, or --dispatch to immediately dispatch each approved step.

```text
agent-team pipeline approve <pipeline>|--all [flags]
```

Flags:

```text
      --all                  Approve manual gates across all pipelines.
      --dispatch             Dispatch each approved manual gate immediately.
      --dry-run              Preview manual gate approvals and optional dispatches without writing job or daemon state.
      --format string        Render each approval result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.
      --json                 Emit approval results as JSON.
      --limit int            Maximum manual gates to approve (0 = no limit).
      --message string       Status message recorded on each approved job.
      --preview-routes       With --dry-run --dispatch, include route and payload previews.
      --repo string          Repo root. (default "<repo>")
      --runtime string       Runtime profile for --dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string   Runtime binary for --dispatch. Overrides env and repo config.
      --step string          Approve only manual gates whose next blocked step has this id.
      --workspace string     Workspace mode for --dispatch: auto, worktree, or repo. (default "auto")
```

## `agent-team pipeline doctor`

Validate pipeline workflow wiring.

Validate declared pipeline workflow wiring: dependency graphs must be acyclic, step targets should resolve through agent.dispatch topology routes, and schedule-triggered pipelines should have a matching schedule source.

```text
agent-team pipeline doctor [<pipeline>|--all] [flags]
```

Flags:

```text
      --all             Validate all pipelines. This is the default when no pipeline is passed.
      --format string   Render the doctor result with a Go template, e.g. '{{.OK}} {{len .Problems}}'.
      --json            Emit pipeline doctor findings as JSON.
      --repo string     Repo root. (default "<repo>")
```

## `agent-team pipeline graph`

Render a declared pipeline step graph.

Render a read-only graph of one declared pipeline in text, Mermaid, DOT, or JSON form.

```text
agent-team pipeline graph <pipeline> [flags]
```

Flags:

```text
      --format string   Graph output format: text, mermaid, or dot. (default "text")
      --json            Emit graph nodes and edges as JSON.
      --repo string     Repo root. (default "<repo>")
      --routes          Annotate step targets with matching agent.dispatch route instances.
```

## `agent-team pipeline jobs`

List jobs for one pipeline.

```text
agent-team pipeline jobs <pipeline> [flags]
```

Flags:

```text
      --format string   Render each job with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --json            Emit jobs as JSON.
      --repo string     Repo root. (default "<repo>")
      --status string   Filter by job status: queued, running, blocked, done, or failed.
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
      --repo string     Repo root. (default "<repo>")
```

## `agent-team pipeline ready`

List ready pipeline jobs.

```text
agent-team pipeline ready <pipeline>|--all [flags]
```

Flags:

```text
      --all             List ready jobs across all pipelines.
      --format string   Render each row with a Go template, e.g. '{{.JobID}} {{.State}} {{.StepID}}'.
      --json            Emit ready rows as JSON.
      --repo string     Repo root. (default "<repo>")
      --state strings   Next-step state to include: ready, queued, running, blocked, failed, done, none, or all. Can repeat or comma-separate.
```

## `agent-team pipeline retry`

Reset failed pipeline steps for another attempt.

Reset failed pipeline steps for jobs in one pipeline, or all pipelines with --all. By default this makes failed steps ready for the next pipeline advance; pass --step to target one stage, or --dispatch to immediately dispatch each retry.

```text
agent-team pipeline retry <pipeline>|--all [flags]
```

Flags:

```text
      --all                  Retry failed steps across all pipelines.
      --dispatch             Dispatch each reset failed step immediately.
      --dry-run              Preview failed-step resets and optional dispatches without writing job or daemon state.
      --format string        Render each retry result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.
      --json                 Emit retry results as JSON.
      --limit int            Maximum failed jobs to retry (0 = no limit).
      --message string       Status message recorded on each retried job.
      --preview-routes       With --dry-run --dispatch, include route and payload previews.
      --repo string          Repo root. (default "<repo>")
      --runtime string       Runtime profile for --dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string   Runtime binary for --dispatch. Overrides env and repo config.
      --step string          Retry only failed jobs whose next failed step has this id.
      --workspace string     Workspace mode for --dispatch: auto, worktree, or repo. (default "auto")
```

## `agent-team pipeline run`

Create a durable job from a pipeline declaration.

```text
agent-team pipeline run <pipeline> <ticket> [kickoff...] [flags]
```

Flags:

```text
      --dispatch              Dispatch the first ready pipeline step immediately using the running daemon.
      --dry-run               Preview the pipeline job that would be created without writing it.
      --format string         Render the created or advanced job with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --id string             Override the normalized job id (default: ticket slug).
      --json                  Emit the created job or advance result as JSON.
      --kickoff string        Kickoff text for the first pipeline step.
      --kickoff-file string   Read kickoff text from a file.
      --repo string           Repo root. (default "<repo>")
      --runtime string        Runtime profile for --dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string    Runtime binary for --dispatch. Overrides env and repo config.
      --ticket-url string     Canonical ticket URL to store on the job.
      --workspace string      Workspace mode for --dispatch: auto, worktree, or repo. (default "auto")
```

## `agent-team pipeline show`

Show one declared pipeline.

```text
agent-team pipeline show <pipeline> [flags]
```

Flags:

```text
      --format string   Render the pipeline with a Go template, e.g. '{{.Name}} {{len .Steps}}'.
      --json            Emit the pipeline as JSON.
      --repo string     Repo root. (default "<repo>")
```

## `agent-team pipeline status`

Summarize pipeline jobs and next steps.

```text
agent-team pipeline status [<pipeline>|--all] [flags]
```

Flags:

```text
      --all             Summarize all pipelines. This is the default when no pipeline is passed.
      --format string   Render each row with a Go template, e.g. '{{.Pipeline}} {{.Jobs}} {{.ReadySteps}}'.
      --json            Emit pipeline status rows as JSON.
      --repo string     Repo root. (default "<repo>")
```

## `agent-team plan`

Preview desired agent instance state from topology and daemon metadata.

Compare instances.toml with daemon metadata and show the lifecycle actions agent-team would normally take: start missing persistent instances, resume stopped ones when supported by the runtime, keep running ones, and leave ephemeral declarations on-demand. With --stop-extras, running daemon-known instances not declared in topology are previewed as stop actions.

```text
agent-team plan [flags]
```

Flags:

```text
      --action strings     Only show plan rows with this action: start, resume, keep, unsupported, on-demand, stop, or extra. Can repeat or comma-separate.
      --agent strings      Only show plan rows for this agent. Can repeat or comma-separate.
      --format string      Render each plan row with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --instance strings   Only show plan rows with this name. Can repeat or comma-separate.
      --json               Emit machine-readable JSON.
      --phase strings      Only show plan rows in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --status strings     Only show lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --stop-extras        Preview running topology extras as stop actions, matching sync --stop-extras.
      --summary            Show aggregate action counts instead of per-instance rows.
      --target string      Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team prune`

Remove finished daemon-managed instances.

Remove daemon-known exited or crashed instances and their state. Running and stopped instances are intentionally left alone.

```text
agent-team prune [flags]
```

Flags:

```text
      --agent strings         Only remove finished instances for this agent. Can repeat or comma-separate.
      --dry-run               Preview finished instances that would be pruned without deleting state or daemon metadata.
      --format string         Render each removal result with a Go template, e.g. '{{.Instance}} {{.Path}}'.
      --json                  Emit machine-readable JSON.
      --older-than duration   Only prune finished instances whose terminal timestamp is older than this duration (for example 24h).
      --phase strings         Only remove finished instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
  -q, --quiet                 Suppress non-error output and use only the exit code.
      --stale                 Only remove finished instances whose non-idle work phase has stale status telemetry.
      --status strings        Only remove finished instances in this lifecycle status: exited or crashed. Can repeat or comma-separate.
      --summary               Show aggregate removal counts instead of per-instance rows.
      --target string         Repo root. (default "<repo>")
      --unhealthy             Only remove finished instances that are crashed or stale.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --sort string         Sort rows by name, status, agent, phase, stale, unhealthy, started, stopped, or exited. (default "name")
      --stale               Only show instances whose status.toml is stale.
      --status strings      Only show lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary             Show lifecycle counts instead of instance rows.
      --target string       Repo root. (default "<repo>")
      --unhealthy           Only show crashed or stale instances.
  -w, --watch               Refresh the process table until interrupted.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team queue`

Inspect and control persisted daemon event queue items.

Inspect and control persisted daemon event queue items under `.agent_team/daemon/queue/`.

```text
agent-team queue
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --dry-run         With --quarantine, preview files that would be moved.
      --format string   Render the queue doctor result with a Go template, e.g. '{{.OK}} {{.Summary.Invalid}}'.
      --json            Emit queue doctor findings as JSON.
      --quarantine      Move queue files with doctor problems out of the active queue.
      --target string   Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team queue drain`

Ask the running daemon to dispatch ready pending queue items.

```text
agent-team queue drain [flags]
```

Flags:

```text
      --dry-run         Preview ready queue items without dispatching them.
      --format string   Render the drain result with a Go template, e.g. '{{.Dispatched}} {{.Pending}}'.
      --json            Emit machine-readable JSON.
      --target string   Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --dry-run              Preview matching queue items without dropping them.
      --event-type strings   With --all, filter by event type; repeat or comma-separate values.
      --format string        Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --instance strings     With --all, filter by target instance name; repeat or comma-separate values.
      --job strings          With --all, filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit machine-readable JSON.
      --limit int            With --all, drop at most this many matching queue items; 0 means no limit.
      --ready                With --all, only drop pending queue items whose next retry is due now.
      --state string         With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.
      --target string        Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team queue ls`

List persisted queue items.

```text
agent-team queue ls [flags]
```

Flags:

```text
      --event-type strings   Filter by event type; repeat or comma-separate values.
      --format string        Render each queue item with a Go template, e.g. '{{.ID}} {{.State}}'.
      --instance strings     Filter by target instance name; repeat or comma-separate values.
      --interval duration    Refresh interval for --watch. (default 2s)
      --job strings          Filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit machine-readable JSON.
      --no-clear             With --watch, append snapshots instead of redrawing the terminal.
      --ready                Only show pending queue items whose next retry is due now.
      --state string         Filter by queue state: pending or dead.
      --summary              Show aggregate queue counts instead of queue rows.
      --target string        Repo root. (default "<repo>")
  -w, --watch                Refresh the queue table until interrupted.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team queue prune`

Prune persisted queue items.

Prune persisted queue items. By default this removes dead-letter items.

```text
agent-team queue prune [flags]
```

Flags:

```text
      --dry-run               Preview queue items that would be pruned without dropping them.
      --format string         Render each result with a Go template, e.g. '{{.ID}} {{.State}}'.
      --json                  Emit prune results as JSON.
      --older-than duration   Only prune items older than this duration based on retry/dead-letter/update time.
      --state string          Queue state to prune: dead, pending, or all. (default "dead")
      --target string         Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team queue quarantine`

Inspect, restore, and drop quarantined queue files.

Inspect queue files moved under `.agent_team/daemon/queue/quarantine/`, restore validated entries to the active queue, or explicitly drop preserved files.

```text
agent-team queue quarantine
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
agent-team queue quarantine drop <quarantine-path> [flags]
```

Flags:

```text
      --all                   Drop all matching quarantined files instead of one path.
      --dry-run               Preview quarantined files that would be dropped.
      --event-type strings    With --all, filter by event type; repeat or comma-separate values.
      --format string         Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --instance strings      With --all, filter by target instance name; repeat or comma-separate values.
      --job strings           With --all, filter by job id or ticket; repeat or comma-separate values.
      --json                  Emit drop results as JSON.
      --older-than duration   With --all, only drop files older than this duration based on file mtime.
      --restorable            With --all, only drop quarantined files that can be restored.
      --state string          With --all, filter by queue state: pending or dead.
      --target string         Repo root. (default "<repo>")
      --unrestorable          With --all, only drop quarantined files that cannot be restored.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team queue quarantine ls`

List quarantined queue files.

```text
agent-team queue quarantine ls [flags]
```

Flags:

```text
      --event-type strings   Filter by event type; repeat or comma-separate values.
      --format string        Render each quarantined queue file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.
      --instance strings     Filter by target instance name; repeat or comma-separate values.
      --job strings          Filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit quarantined queue files as JSON.
      --restorable           Only show quarantined files that can be restored.
      --state string         Filter by queue state: pending or dead.
      --target string        Repo root. (default "<repo>")
      --unrestorable         Only show quarantined files that cannot be restored.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team queue quarantine restore`

Restore validated quarantined queue files.

Restore one validated quarantined queue file by path, or restore a filtered batch of restorable files with --all.

```text
agent-team queue quarantine restore <quarantine-path> [flags]
```

Flags:

```text
      --all                  Restore all matching restorable quarantined files instead of one path.
      --dry-run              Preview the restore without moving files.
      --event-type strings   With --all, filter by event type; repeat or comma-separate values.
      --force                Overwrite an existing active queue file with the same restore path.
      --format string        Render each restore result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --instance strings     With --all, filter by target instance name; repeat or comma-separate values.
      --job strings          With --all, filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit restore result as JSON.
      --state string         With --all, filter by queue state: pending or dead.
      --target string        Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team queue quarantine show`

Show one quarantined queue file.

```text
agent-team queue quarantine show <quarantine-path> [flags]
```

Flags:

```text
      --format string   Render the quarantined queue file with a Go template, e.g. '{{.ID}} {{.State}}'.
      --json            Emit the quarantined queue file as JSON.
      --target string   Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --dry-run              Preview matching queue items without retrying them.
      --event-type strings   With --all, filter by event type; repeat or comma-separate values.
      --format string        Render each retry result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --instance strings     With --all, filter by target instance name; repeat or comma-separate values.
      --job strings          With --all, filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit machine-readable JSON.
      --limit int            With --all, retry at most this many matching queue items; 0 means no limit.
      --ready                With --all, only retry pending queue items whose next retry is due now.
      --state string         With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.
      --target string        Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team queue show`

Show one persisted queue item.

```text
agent-team queue show <id> [flags]
```

Flags:

```text
      --format string   Render the queue item with a Go template, e.g. '{{.ID}} {{.State}}'.
      --json            Emit the queue item as JSON.
      --target string   Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --target string   Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team repair`

Recover common unhealthy orchestration state.

Recover common unhealthy orchestration state: ensure the daemon is ready, retry dead-letter queue items, optionally retry failed pipeline steps, and run a maintenance tick to drain ready work and advance pipelines. Use --dry-run to preview.

```text
agent-team repair [flags]
```

Flags:

```text
      --dry-run                  Preview repair actions without mutating state or starting the daemon.
      --format string            Render the repair result with a Go template, e.g. '{{.DryRun}} {{.Queue.Action}}'.
      --interval duration        Delay between --until-idle maintenance cycles. (default 2s)
      --jobs                     Include durable job triage and status-file previews in health snapshots.
      --json                     Emit machine-readable JSON.
      --limit int                Retry at most this many dead-letter queue items or failed pipeline jobs, and advance at most this many ready pipeline jobs; 0 means no limit.
      --max-cycles int           With --until-idle, stop after this many cycles if work keeps appearing. (default 20)
      --preview-routes           With --dry-run, include route and dispatch payload previews for retried or ready pipeline steps.
      --ready-timeout duration   Maximum time to wait for implicit daemon readiness (0 = no timeout). (default 3s)
      --retry-message string     Audit message to record when --retry-pipelines resets failed steps.
      --retry-pipelines          Reset failed pipeline steps and dispatch them before the maintenance tick.
      --retry-step string        With --retry-pipelines, retry only failed jobs whose next failed step has this id.
      --skip-daemon              Do not start or reconcile the daemon.
      --skip-queue               Do not retry dead-letter queue items.
      --skip-tick                Do not run a maintenance tick after queue retry.
      --target string            Repo root. (default "<repo>")
      --until-idle               Run maintenance ticks until no immediate queue, schedule, or pipeline work remains.
      --workspace string         Workspace mode for retried or advanced pipeline steps: auto, worktree, or repo. (default "auto")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team restart`

Restart or resume instances.

Restart declared persistent instances through the daemon. Running instances are stopped and resumed; stopped instances are resumed; instances with no daemon metadata are started fresh. Explicit names may also target daemon-known ad-hoc instances.

```text
agent-team restart [<instance>...] [flags]
```

Flags:

```text
      --agent strings            Restart or resume every declared persistent and daemon-known instance for this agent. Can repeat or comma-separate.
  -a, --all                      Restart or resume every declared persistent and daemon-known instance.
      --attach                   Follow the selected instance log after restarting or resuming. Requires exactly one selected instance.
      --dry-run                  Preview planned restart/resume actions without changing daemon state.
  -f, --force                    Escalate to SIGKILL if a running instance does not stop within --timeout before restarting.
      --format string            Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                     Emit machine-readable JSON.
  -n, --last int                 Restart or resume the N most recently started instances after other filters (0 = all).
      --latest                   Restart or resume the most recently started instance after other filters.
      --phase strings            Only restart or resume instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --prompt string            Override the default kickoff prompt for instances started fresh.
  -q, --quiet                    Suppress non-error output and use only the exit code.
      --ready-timeout duration   Maximum time to wait for implicit daemon readiness (0 = no timeout). (default 3s)
      --stale                    Only restart or resume instances whose status.toml is stale.
      --status strings           Only restart or resume instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary                  Show aggregate action counts instead of per-instance rows.
      --tail string              With --attach, show only the last N lines before following (0 or all = all). (default "50")
      --target string            Repo root. (default "<repo>")
      --timeout duration         Maximum time to wait for each running instance to stop before resuming (0 = daemon default).
      --unhealthy                Only restart or resume instances that are crashed or stale.
      --wait                     Wait for selected instances to become healthy after restarting. With no scoped selection, waits for the fleet.
      --wait-timeout duration    Maximum time to wait for health with --wait (0 = no timeout).
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team rm`

Remove instance state and daemon metadata.

Docker-like convenience alias for `agent-team instance rm`. When the daemon is running, also removes daemon metadata; use --force to stop and remove a running instance.

```text
agent-team rm [<instance>...] [flags]
```

Flags:

```text
      --agent strings    With --all, --finished, --latest, --last, --status, --phase, --stale, or --unhealthy, only remove daemon-known instances for this agent. Can repeat or comma-separate.
  -a, --all              Remove every daemon-known instance. Can combine with --agent, --status, --phase, --stale, or --unhealthy.
      --dry-run          Preview matching removals without deleting state or daemon metadata.
      --finished         Remove every daemon-known exited or crashed instance.
  -f, --force            Skip confirmation; if the daemon is running, stop a running instance before removal.
      --format string    Render each removal result with a Go template, e.g. '{{.Instance}} {{.Path}}'. Requires --force unless --dry-run is set.
      --json             Emit machine-readable JSON. Requires --force unless --dry-run is set.
  -n, --last int         Remove the N most recently started daemon-known instances after other filters (0 = all).
      --latest           Remove the most recently started daemon-known instance after other filters.
      --phase strings    Remove daemon-known instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
  -q, --quiet            Suppress non-error output. Requires --force unless --dry-run is set.
      --stale            Remove only daemon-known instances whose non-idle work phase has stale status telemetry.
      --status strings   Remove daemon-known instances currently in this lifecycle status: stopped, exited, crashed, running, or unknown. Can repeat or comma-separate.
      --summary          Show aggregate removal counts instead of per-instance rows.
      --target string    Repo root. (default "<repo>")
      --unhealthy        Remove only daemon-known instances that are crashed or stale.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --ready-timeout duration   Maximum time to wait for daemon readiness with --detach or --attach (0 = no timeout). (default 3s)
      --runtime string           Runtime profile for this invocation (claude or codex). Overrides env and repo config.
      --runtime-bin string       Runtime binary for this invocation. Overrides env and repo config.
      --set stringArray          Override a config value for this spawn, e.g. --set linear.team_id=<x>. Repeatable.
      --tail string              With --attach, show only the last N lines before following (0 or all = all). (default "50")
      --target string            Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team schedule`

Inspect and run declared schedule events.

Inspect schedules declared in .agent_team/instances.toml and manually publish their schedule events.

```text
agent-team schedule
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --format string   Render each due schedule with a Go template, e.g. '{{.Name}} {{.DueReason}}'.
      --json            Emit due schedules as JSON.
      --repo string     Repo root. (default "<repo>")
```

## `agent-team schedule fire`

Publish every schedule due now through the daemon.

```text
agent-team schedule fire [flags]
```

Flags:

```text
      --dry-run            Preview due schedules without publishing events or writing schedule clocks.
      --format string      Render the fire result with a Go template, e.g. '{{.Fired}} {{len .Schedules}}'.
      --json               Emit fire results as JSON.
      --preview-triggers   With --dry-run, include local topology instance and pipeline matches.
      --repo string        Repo root. (default "<repo>")
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
      --repo string     Repo root. (default "<repo>")
```

## `agent-team schedule next`

List declared schedules ordered by next run.

```text
agent-team schedule next [flags]
```

Flags:

```text
      --format string   Render each forecast row with a Go template, e.g. '{{.Name}} {{.Due}} {{.NextRun}}'.
      --json            Emit schedule forecast rows as JSON.
      --limit int       Show at most this many schedules after ordering; 0 means all.
      --repo string     Repo root. (default "<repo>")
```

## `agent-team schedule run`

Publish one declared schedule event now.

```text
agent-team schedule run <schedule> [flags]
```

Flags:

```text
      --dry-run               Preview the schedule event without publishing it.
      --format string         Render the event result with a Go template, e.g. '{{.Event.Type}} {{.DryRun}}'.
      --json                  Emit the event and outcome as JSON.
      --payload string        Additional JSON object merged into the declared schedule payload.
      --payload-file string   Read additional schedule payload JSON from a file, or '-' for stdin.
      --preview-triggers      With --dry-run, include local topology instance and pipeline matches.
      --repo string           Repo root. (default "<repo>")
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
      --repo string     Repo root. (default "<repo>")
```

## `agent-team send`

Send a mailbox message to a daemon-managed instance.

Send a direct message through the daemon mailbox. By default the target must already be known to the daemon, which catches typos. Use --allow-missing to intentionally queue a message for a future instance. Use --all, --latest, --last, --agent, --status, --phase, --stale, or --unhealthy to send the same message to a selected set of daemon-known instances.

```text
agent-team send [<instance>] <message...> [flags]
```

Flags:

```text
      --agent strings         Send to daemon-known instances for this agent. Can repeat or comma-separate.
  -a, --all                   Send to every daemon-known instance.
      --allow-missing         Allow queueing a message for an instance the daemon does not know yet.
      --dry-run               Preview matching recipients without appending mailbox messages.
      --format string         Render each send result with a Go template, e.g. '{{.To}} {{.ID}}'.
      --from string           Sender label recorded with the message. (default "(cli)")
      --json                  Emit machine-readable JSON.
  -n, --last int              Send to the N most recently started daemon-known instances after other filters (0 = all).
      --latest                Send to the most recently started daemon-known instance after other filters.
      --message string        Message text to send.
      --message-file string   Read message text from a file, or '-' for stdin.
      --phase strings         Send to daemon-known instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --stale                 Send to daemon-known instances whose status.toml is stale.
      --status strings        Send to daemon-known instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --target string         Repo root. (default "<repo>")
      --unhealthy             Send to daemon-known instances that are crashed or stale.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team snapshot`

Capture a read-only orchestration diagnostic report.

Capture a read-only diagnostic report with health, plan, instance, job, job status preview, queue, schedule, runtime, and recent lifecycle event state. Use --json for stdout or --output to write a JSON file.

```text
agent-team snapshot [flags]
```

Flags:

```text
      --events int              Recent lifecycle events to include. Use -1 for all events or 0 to skip events. (default 50)
      --intake-deliveries int   Recent intake deliveries to include. Use -1 for all deliveries or 0 to skip deliveries. (default 50)
      --json                    Emit the full snapshot JSON to stdout.
      --no-redact               Include raw payload values instead of redacting sensitive keys.
  -o, --output string           Write the full JSON snapshot to this file. Use '-' for stdout.
      --schedule-limit int      Upcoming schedules to include after ordering; 0 means all. (default 10)
      --target string           Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --dry-run                  Preview planned start/resume actions without changing daemon state.
      --format string            Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                     Emit machine-readable JSON.
  -n, --last int                 Start or resume the N most recently started instances after other filters (0 = all).
      --latest                   Start or resume the most recently started instance after other filters.
      --phase strings            Only start or resume instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --prompt string            Override the default kickoff prompt.
  -q, --quiet                    Suppress non-error output and use only the exit code.
      --ready-timeout duration   Maximum time to wait for implicit daemon readiness (0 = no timeout). (default 3s)
      --stale                    Only start or resume instances whose status.toml is stale.
      --status strings           Only start or resume instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary                  Show aggregate action counts instead of per-instance rows.
      --tail string              With --attach, show only the last N lines before following (0 or all = all). (default "50")
      --target string            Repo root. (default "<repo>")
      --timeout duration         Maximum time to wait with --wait (0 = no timeout).
      --unhealthy                Only start or resume instances that are crashed or stale.
      --wait                     Wait for selected instances to become healthy after starting. With no scoped selection, waits for the fleet.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --sort string         Sort rows by name, cpu, mem, rss, status, agent, phase, stale, or unhealthy. (default "name")
      --stale               Only show instances whose status.toml is stale.
      --status strings      Only show lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary             Show aggregate CPU, memory, and RSS totals instead of instance rows.
      --target string       Repo root. (default "<repo>")
      --unhealthy           Only show crashed or stale instances.
  -w, --watch               Refresh stats until interrupted.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --since string           With --events, only include lifecycle events since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --stale                  Only show instances whose status.toml is stale.
      --status strings         Only show lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --stop-extras            With --plan, preview running topology extras as stop actions.
      --strict-topology        With --summary, treat running daemon-known instances not declared in instances.toml as unhealthy.
      --summary                Show a compact non-failing fleet health summary instead of the full instance table.
      --target string          Repo root. (default "<repo>")
      --unhealthy              Only show crashed or stale instances.
  -w, --watch                  Refresh daemon health and instance table until interrupted.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --dry-run                 Preview planned stop actions without changing daemon state.
  -f, --force                   Escalate to SIGKILL if an instance does not stop within --timeout.
      --format string           Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                    Emit machine-readable JSON.
  -n, --last int                Stop the N most recently started running instances after other filters (0 = all).
      --latest                  Stop the most recently started running instance after other filters.
      --phase strings           Stop daemon-known instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
  -q, --quiet                   Suppress non-error output and use only the exit code.
      --rm                      Remove selected instance state and daemon metadata after stopping.
      --stale                   Only stop instances whose status.toml is stale.
      --status strings          Stop daemon-known instances currently in this lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary                 Show aggregate action counts instead of per-instance rows.
      --target string           Repo root. (default "<repo>")
      --timeout duration        Grace before --force kills. With --wait and no --wait-timeout, also used as the wait deadline (0 = no wait deadline; force defaults to 10s).
      --unhealthy               Only stop instances that are crashed or stale.
      --wait                    Wait for stopped instances to reach a terminal state.
      --wait-timeout duration   Maximum time to wait for terminal state with --wait. Defaults to --timeout when unset; set 0 explicitly for no wait timeout.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team sync`

Apply topology&#39;s desired persistent instance state.

Reload daemon topology, reconcile runtime metadata, then start or resume declared persistent instances when supported by the runtime. Sync is intentionally non-destructive: daemon-known instances that are not declared in topology are reported by plan but are not stopped or removed unless --stop-extras is set.

```text
agent-team sync [flags]
```

Flags:

```text
      --action strings           Only sync plan rows with this action: start, resume, keep, unsupported, on-demand, stop, or extra. Can repeat or comma-separate.
      --agent strings            Only sync plan rows for this agent. Can repeat or comma-separate.
      --dry-run                  Preview topology convergence without starting the daemon or instances.
      --format string            Render each sync action with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --instance strings         Only sync plan rows with this name. Can repeat or comma-separate.
      --json                     Emit machine-readable JSON.
      --phase strings            Only sync plan rows in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
  -q, --quiet                    Suppress non-error output and use only the exit code.
      --ready-timeout duration   Maximum time to wait for implicit daemon readiness (0 = no timeout). (default 3s)
      --status strings           Only sync plan rows with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --stop-extras              Also stop running daemon-known instances not declared in instances.toml.
      --summary                  Show aggregate action counts instead of per-instance rows.
      --target string            Repo root. (default "<repo>")
      --timeout duration         Maximum time to wait with --wait (0 = no timeout).
      --wait                     Wait for selected instances to become healthy after syncing. With no filters, waits for the fleet.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team team`

Inspect declared agent teams.

Inspect team declarations loaded from .agent_team/instances.toml.

```text
agent-team team
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team team advance` - Dispatch ready pipeline steps owned by one team.
- `agent-team team approve` - Approve manual pipeline gates owned by one team.
- `agent-team team cleanup` - Clean up done jobs owned by one team.
- `agent-team team doctor` - Validate one team&#39;s topology wiring.
- `agent-team team down` - Stop a team&#39;s persistent instances and active ephemeral children.
- `agent-team team drain` - Run one team&#39;s maintenance loop until idle.
- `agent-team team events` - Show lifecycle events scoped to one team.
- `agent-team team health` - Check health for one declared team.
- `agent-team team jobs` - List jobs owned by one team.
- `agent-team team logs` - Show daemon-captured logs for one team.
- `agent-team team ls` - List declared teams.
- `agent-team team monitor` - Show a combined operator snapshot for one team.
- `agent-team team next` - Print recommended next actions scoped to one team.
- `agent-team team overview` - Show a concise operator overview for one declared team.
- `agent-team team pipelines` - List pipeline status for one team.
- `agent-team team plan` - Preview desired lifecycle state for one team.
- `agent-team team prune` - Remove finished team-owned instances.
- `agent-team team ps` - List instances owned by one team.
- `agent-team team queue` - List or control queue items scoped to one team.
- `agent-team team ready` - List ready pipeline jobs owned by one team.
- `agent-team team repair` - Recover unhealthy orchestration state for one team.
- `agent-team team restart` - Restart or resume a team&#39;s declared persistent instances.
- `agent-team team retry` - Reset failed pipeline steps owned by one team.
- `agent-team team run` - Create a durable job through a team&#39;s pipeline.
- `agent-team team schedules` - List schedules owned by one team.
- `agent-team team send` - Send a mailbox message to team-owned instances.
- `agent-team team show` - Show one declared team.
- `agent-team team snapshot` - Capture a team-scoped diagnostic report.
- `agent-team team stats` - Show CPU and memory usage for team-owned instances.
- `agent-team team status` - Summarize one team&#39;s instances, jobs, and pipelines.
- `agent-team team sync` - Sync one team&#39;s declared persistent instances.
- `agent-team team tick` - Run one team&#39;s orchestration maintenance work.
- `agent-team team triage` - Show team-owned jobs that need operator attention.
- `agent-team team up` - Start or resume a team&#39;s declared persistent instances.
- `agent-team team wait` - Wait for team-owned instances to reach a lifecycle condition.

## `agent-team team advance`

Dispatch ready pipeline steps owned by one team.

Dispatch or preview ready next steps for jobs in one team&#39;s declared pipelines.

```text
agent-team team advance <team> [flags]
```

Flags:

```text
      --dry-run              Preview ready steps without dispatching them.
      --format string        Render each result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.
      --json                 Emit advance results as JSON.
      --limit int            Advance at most this many ready team jobs; 0 means no limit.
      --preview-routes       With --dry-run, include local topology route and dispatch payload previews.
      --repo string          Repo root. (default "<repo>")
      --runtime string       Runtime profile for advanced step dispatches (claude or codex). Overrides env and repo config.
      --runtime-bin string   Runtime binary for advanced step dispatches. Overrides env and repo config.
      --workspace string     Workspace mode for advanced steps: auto, worktree, or repo. (default "auto")
```

## `agent-team team approve`

Approve manual pipeline gates owned by one team.

Approve or preview blocked manual-gate steps for jobs in one team&#39;s declared pipelines. Pass --step to target one stage, or --dispatch to immediately dispatch each approved step.

```text
agent-team team approve <team> [flags]
```

Flags:

```text
      --dispatch             Dispatch each approved manual gate immediately.
      --dry-run              Preview manual gate approvals and optional dispatches without writing job or daemon state.
      --format string        Render each result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.
      --json                 Emit approval results as JSON.
      --limit int            Approve at most this many manual gates; 0 means no limit.
      --message string       Status message recorded on each approved team job.
      --preview-routes       With --dry-run --dispatch, include local topology route and dispatch payload previews.
      --repo string          Repo root. (default "<repo>")
      --runtime string       Runtime profile for --dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string   Runtime binary for --dispatch. Overrides env and repo config.
      --step string          Approve only manual gates whose next blocked step has this id.
      --workspace string     Workspace mode for approved dispatches: auto, worktree, or repo. (default "auto")
```

## `agent-team team cleanup`

Clean up done jobs owned by one team.

Preview or remove job-owned worktrees and branches for done jobs owned by one declared team.

```text
agent-team team cleanup <team> [flags]
```

Flags:

```text
      --dry-run         Preview team-owned job cleanup without removing anything.
      --force-branch    With --merged, delete job branches with git branch -D if they are not locally merged.
      --format string   Render the cleanup batch with a Go template, e.g. '{{.Team}} {{.Cleaned}} {{.Failed}}'.
      --json            Emit the cleanup batch as JSON.
      --merged          Confirm the team's matching PRs have merged before removing worktrees and branches.
      --repo string     Repo root. (default "<repo>")
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
      --all             Validate all declared teams.
      --format string   Render the team doctor result with a Go template, e.g. '{{.OK}} {{len .Problems}}'.
      --json            Emit team doctor findings as JSON.
      --repo string     Repo root. (default "<repo>")
```

## `agent-team team down`

Stop a team&#39;s persistent instances and active ephemeral children.

```text
agent-team team down <team> [flags]
```

Aliases: `stop`

Flags:

```text
      --dry-run                 Preview planned stop actions without changing daemon state.
  -f, --force                   Escalate to SIGKILL if an instance does not stop within --timeout.
      --format string           Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                    Emit machine-readable JSON.
  -q, --quiet                   Suppress non-error output and use only the exit code.
      --repo string             Repo root. (default "<repo>")
      --rm                      Remove selected instance state and daemon metadata after stopping.
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
      --format string       Render the drain result with a Go template, e.g. '{{.Team.Name}} {{.CyclesRun}} {{.Idle}}'.
      --interval duration   Delay between drain cycles. (default 2s)
      --json                Emit machine-readable JSON.
      --limit int           Advance at most this many ready pipeline jobs per cycle; 0 means no limit.
      --max-cycles int      Stop after this many cycles if work keeps appearing. (default 20)
      --repo string         Repo root. (default "<repo>")
      --skip-advance        Skip pipeline advancement work.
      --skip-drain          Skip queue drain work.
      --skip-schedules      Skip due schedule work.
      --workspace string    Workspace mode for advanced pipeline steps: auto, worktree, or repo. (default "auto")
```

## `agent-team team events`

Show lifecycle events scoped to one team.

Show or follow daemon lifecycle events for one declared team, including ephemeral children owned by that team.

```text
agent-team team events <team> [flags]
```

Flags:

```text
      --action strings   Only show events with this action. Can repeat or comma-separate.
  -f, --follow           Keep streaming new lifecycle events.
      --format string    Render each event with a Go template, e.g. '{{.Action}} {{.Instance}} {{.Status}}'.
      --json             Emit raw JSONL events.
      --repo string      Repo root. (default "<repo>")
      --since string     Only show events since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --status strings   Only show events with this lifecycle status. Can repeat or comma-separate.
      --summary          Summarize matching team events by action, status, agent, and instance.
      --tail int         Show only the last N matching team events before returning or following (0 = all).
```

## `agent-team team health`

Check health for one declared team.

```text
agent-team team health <team> [flags]
```

Flags:

```text
      --format string   Render team health with a Go template, e.g. '{{.Team.Name}} {{.Health.Healthy}}'.
      --jobs            Include team-owned job and pipeline health.
      --json            Emit team health as JSON.
  -q, --quiet           Suppress output and use only the exit code.
      --repo string     Repo root. (default "<repo>")
```

## `agent-team team jobs`

List jobs owned by one team.

```text
agent-team team jobs <team> [flags]
```

Flags:

```text
      --format string   Render each job with a Go template, e.g. '{{.ID}} {{.Status}}'.
      --json            Emit team jobs as JSON.
      --repo string     Repo root. (default "<repo>")
      --sort string     Sort jobs by id, status, target, ticket, created, updated, instance, branch, or pr. (default "id")
      --status string   Filter by job status: queued, running, blocked, done, or failed.
```

## `agent-team team logs`

Show daemon-captured logs for one team.

```text
agent-team team logs <team> [flags]
```

Flags:

```text
  -f, --follow           Tail selected team logs.
      --format string    With --list, render each log stream with a Go template, e.g. '{{.Instance}} {{.LogPath}}'.
      --grep string      Only print log lines matching this regular expression. One-shot reads only.
      --json             Emit machine-readable JSON with --list.
  -n, --last int         Show logs for the N most recently started team instances (0 = all).
      --last-message     Show clean final Codex response sidecars instead of raw runtime logs.
      --latest           Show the most recently started team instance log.
      --list             List team log streams instead of printing log content.
      --no-prefix        Do not prefix lines when streaming multiple team logs.
      --phase strings    Only show logs for work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --repo string      Repo root. (default "<repo>")
      --since string     Only include log streams modified since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --stale            Only show logs for team instances whose status.toml is stale.
      --status strings   Only show logs for lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --tail string      Show only the last N lines before returning or following (0 or all = all). (default "0")
      --unhealthy        Only show logs for crashed or stale team instances.
```

## `agent-team team ls`

List declared teams.

```text
agent-team team ls [flags]
```

Flags:

```text
      --json          Emit teams as JSON.
      --repo string   Repo root. (default "<repo>")
```

## `agent-team team monitor`

Show a combined operator snapshot for one team.

Show a Docker-style operator snapshot scoped to one declared team, combining team health, instance rows, daemon-managed process stats, and optional plan, job, schedule, and lifecycle event sections.

```text
agent-team team monitor <team> [flags]
```

Flags:

```text
      --action strings         With --plan, only show plan rows with this action: start, resume, keep, unsupported, on-demand, stop, or extra. Can repeat or comma-separate.
      --agent strings          Only show team-owned instances, stats, and plan rows for this agent. Can repeat or comma-separate.
  -a, --all                    Include stopped, exited, crashed, and missing team-owned instances in the stats section.
      --event-action strings   With --events, only show lifecycle events with this action. Can repeat or comma-separate.
      --events int             Include the last N matching team lifecycle events in the full monitor (0 = omit).
      --format string          Render team monitor snapshots with a Go template, e.g. '{{.Team.Name}} {{len .Instances}}'.
      --instance strings       Only show team-owned instances with this name. Can repeat or comma-separate.
      --interval duration      Refresh interval for --watch. (default 2s)
      --jobs                   Include team-owned durable job summary, attention, ready-step state, and status-file previews.
      --json                   Emit JSON. With --watch, writes one JSON object per refresh.
  -n, --last int               Show only the N most recently started team-owned instances after other filters (0 = all).
      --latest                 Show only the most recently started team-owned instance after other filters.
      --no-clear               With --watch, append snapshots instead of redrawing the terminal.
      --phase strings          Only show team-owned instances and stats in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --plan                   Include team-scoped desired-state actions from instances.toml and daemon metadata.
      --repo string            Repo root. (default "<repo>")
      --schedules              Include due and upcoming team schedules.
      --since string           With --events, only show lifecycle events since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --sort string            Sort instance rows by name, status, agent, phase, stale, unhealthy, started, stopped, or exited. (default "name")
      --stale                  Only show team-owned instances whose status.toml is stale.
      --stats-sort string      Sort stats rows by name, cpu, mem, rss, status, agent, phase, stale, or unhealthy. (default "name")
      --status strings         Only show team-owned lifecycle status in instance, stats, and plan sections: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --stop-extras            With --plan, preview running team-agent extras as stop actions.
      --unhealthy              Only show crashed or stale team-owned instances.
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
      --format string        Render the next-action result with a Go template, e.g. '{{.Team.Name}} {{len .Actions}}'.
      --interval duration    Refresh interval for --watch. (default 2s)
      --json                 Emit recommended actions as JSON.
      --limit int            Show at most this many actions; 0 means all.
      --no-clear             With --watch, append snapshots instead of redrawing the terminal.
      --repo string          Repo root. (default "<repo>")
      --schedule-limit int   Upcoming schedules to inspect while building recommendations; 0 means all. (default 5)
  -w, --watch                Refresh recommended actions until interrupted.
```

## `agent-team team overview`

Show a concise operator overview for one declared team.

Show a read-only operator overview scoped to one declared team with health, topology, job, queue, pipeline, schedule, and recommended next-action summaries.

```text
agent-team team overview <team> [flags]
```

Flags:

```text
      --format string        Render the team overview result with a Go template, e.g. '{{.Team.Name}} {{.State}}'.
      --interval duration    Refresh interval for --watch. (default 2s)
      --json                 Emit team overview as JSON.
      --no-clear             With --watch, append snapshots instead of redrawing the terminal.
      --repo string          Repo root. (default "<repo>")
      --schedule-limit int   Upcoming team schedules to inspect after ordering; 0 means all. (default 5)
  -w, --watch                Refresh team overview until interrupted.
```

## `agent-team team pipelines`

List pipeline status for one team.

```text
agent-team team pipelines <team> [flags]
```

Flags:

```text
      --format string   Render each pipeline with a Go template, e.g. '{{.Pipeline}} {{.ReadySteps}}'.
      --json            Emit team pipeline status as JSON.
      --repo string     Repo root. (default "<repo>")
```

## `agent-team team plan`

Preview desired lifecycle state for one team.

```text
agent-team team plan <team> [flags]
```

Flags:

```text
      --action strings   Only show plan rows with this action: start, resume, keep, unsupported, on-demand, stop, or extra. Can repeat or comma-separate.
      --format string    Render each plan row with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json             Emit team plan as JSON.
      --repo string      Repo root. (default "<repo>")
      --stop-extras      Preview running team-agent topology extras as stop actions.
      --summary          Show aggregate action counts instead of per-instance rows.
```

## `agent-team team prune`

Remove finished team-owned instances.

Remove daemon-known exited or crashed instances owned by one declared team. Running and stopped instances are intentionally left alone.

```text
agent-team team prune <team> [flags]
```

Flags:

```text
      --dry-run               Preview finished team-owned instances that would be pruned without deleting state or daemon metadata.
      --format string         Render each removal result with a Go template, e.g. '{{.Instance}} {{.Path}}'.
      --json                  Emit machine-readable JSON.
      --older-than duration   Only prune finished team-owned instances whose terminal timestamp is older than this duration (for example 24h).
      --phase strings         Only remove finished team-owned instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
  -q, --quiet                 Suppress non-error output and use only the exit code.
      --repo string           Repo root. (default "<repo>")
      --stale                 Only remove finished team-owned instances whose non-idle work phase has stale status telemetry.
      --status strings        Only remove finished team-owned instances in this lifecycle status: exited or crashed. Can repeat or comma-separate.
      --summary               Show aggregate removal counts instead of per-instance rows.
      --unhealthy             Only remove finished team-owned instances that are crashed or stale.
```

## `agent-team team ps`

List instances owned by one team.

```text
agent-team team ps <team> [flags]
```

Aliases: `instances`

Flags:

```text
      --format string       Render each team instance with a Go template, e.g. '{{.Instance}} {{.Status}}'.
      --interval duration   Refresh interval for --watch. (default 2s)
      --json                Emit team instances as JSON.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --repo string         Repo root. (default "<repo>")
  -w, --watch               Refresh team instances until interrupted.
```

## `agent-team team queue`

List or control queue items scoped to one team.

```text
agent-team team queue <team> [flags]
```

Flags:

```text
      --event-type strings   Filter by event type; repeat or comma-separate values.
      --format string        Render each queue item with a Go template, e.g. '{{.ID}} {{.State}}'.
      --interval duration    Refresh interval for --watch. (default 2s)
      --job strings          Filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit team queue rows as JSON.
      --no-clear             With --watch, append snapshots instead of redrawing the terminal.
      --ready                Only show pending queue items whose next retry is due now.
      --repo string          Repo root. (default "<repo>")
      --state string         Filter by queue state: pending or dead.
      --summary              Show aggregate queue counts instead of queue rows.
  -w, --watch                Refresh the team queue table until interrupted.
```

Subcommands:

- `agent-team team queue drop` - Drop team-owned queue items.
- `agent-team team queue prune` - Prune team-owned queue items.
- `agent-team team queue quarantine` - List quarantined queue files scoped to one team.
- `agent-team team queue retry` - Retry team-owned queue items.

## `agent-team team queue drop`

Drop team-owned queue items.

Drop one team-owned queue item by id, or drop a filtered team-owned batch with --all. Batch drops default to dead-letter items.

```text
agent-team team queue drop <team> [id] [flags]
```

Flags:

```text
      --all                  Drop all matching team-owned queue items instead of one id.
      --dry-run              Preview matching team-owned queue items without dropping them.
      --event-type strings   With --all, filter by event type; repeat or comma-separate values.
      --format string        Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings          With --all, filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit machine-readable JSON.
      --limit int            With --all, drop at most this many matching queue items; 0 means no limit.
      --ready                With --all, only drop pending queue items whose next retry is due now.
      --repo string          Repo root. (default "<repo>")
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
      --dry-run               Preview team-owned queue items that would be pruned without dropping them.
      --format string         Render each prune result with a Go template, e.g. '{{.ID}} {{.State}}'.
      --json                  Emit prune results as JSON.
      --older-than duration   Only prune team-owned items older than this duration based on retry/dead-letter/update time.
      --repo string           Repo root. (default "<repo>")
      --state string          Queue state to prune: dead, pending, or all. (default "dead")
```

## `agent-team team queue quarantine`

List quarantined queue files scoped to one team.

```text
agent-team team queue quarantine <team> [flags]
```

Flags:

```text
      --event-type strings   Filter by event type; repeat or comma-separate values.
      --format string        Render each team-owned quarantined queue file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.
      --job strings          Filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit team-owned quarantined queue files as JSON.
      --repo string          Repo root. (default "<repo>")
      --restorable           Only show quarantined files that can be restored.
      --state string         Filter by queue state: pending or dead.
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
      --dry-run               Preview quarantined files that would be dropped.
      --event-type strings    With --all, filter by event type; repeat or comma-separate values.
      --format string         Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings           With --all, filter by job id or ticket; repeat or comma-separate values.
      --json                  Emit drop results as JSON.
      --older-than duration   With --all, only drop files older than this duration based on file mtime.
      --repo string           Repo root. (default "<repo>")
      --restorable            With --all, only drop quarantined files that can be restored.
      --state string          With --all, filter by queue state: pending or dead.
      --unrestorable          With --all, only drop quarantined files that cannot be restored.
```

## `agent-team team queue quarantine restore`

Restore team-owned quarantined queue files.

Restore one team-owned quarantined queue file by path, or restore a filtered team-owned batch of restorable files with --all.

```text
agent-team team queue quarantine restore <team> <quarantine-path> [flags]
```

Flags:

```text
      --all                  Restore all matching team-owned restorable quarantined files instead of one path.
      --dry-run              Preview the restore without moving files.
      --event-type strings   With --all, filter by event type; repeat or comma-separate values.
      --force                Overwrite an existing active queue file with the same restore path.
      --format string        Render each restore result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings          With --all, filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit restore result as JSON.
      --repo string          Repo root. (default "<repo>")
      --state string         With --all, filter by queue state: pending or dead.
```

## `agent-team team queue quarantine show`

Show one team-owned quarantined queue file.

```text
agent-team team queue quarantine show <team> <quarantine-path> [flags]
```

Flags:

```text
      --format string   Render the team-owned quarantined queue file with a Go template, e.g. '{{.Team}} {{.ID}}'.
      --json            Emit the team-owned quarantined queue file as JSON.
      --repo string     Repo root. (default "<repo>")
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
      --dry-run              Preview matching team-owned queue items without retrying them.
      --event-type strings   With --all, filter by event type; repeat or comma-separate values.
      --format string        Render each retry result with a Go template, e.g. '{{.ID}} {{.Action}}'.
      --job strings          With --all, filter by job id or ticket; repeat or comma-separate values.
      --json                 Emit machine-readable JSON.
      --limit int            With --all, retry at most this many matching queue items; 0 means no limit.
      --ready                With --all, only retry pending queue items whose next retry is due now.
      --repo string          Repo root. (default "<repo>")
      --state string         With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.
```

## `agent-team team ready`

List ready pipeline jobs owned by one team.

```text
agent-team team ready <team> [flags]
```

Flags:

```text
      --format string   Render each row with a Go template, e.g. '{{.JobID}} {{.State}} {{.StepID}}'.
      --json            Emit team ready rows as JSON.
      --repo string     Repo root. (default "<repo>")
      --state strings   Next-step state to include: ready, queued, running, blocked, failed, done, none, or all. Can repeat or comma-separate.
```

## `agent-team team repair`

Recover unhealthy orchestration state for one team.

Recover unhealthy orchestration state scoped to one team: ensure the daemon is ready, retry team-owned dead-letter queue items, optionally retry failed team pipeline steps, and run a scoped team tick. Use --dry-run to preview.

```text
agent-team team repair <team> [flags]
```

Flags:

```text
      --dry-run                  Preview team repair actions without mutating state or starting the daemon.
      --format string            Render the team repair result with a Go template, e.g. '{{.Team.Name}} {{.Queue.Action}}'.
      --interval duration        Delay between --until-idle scoped team tick cycles. (default 2s)
      --jobs                     Include team-owned durable job and pipeline health.
      --json                     Emit machine-readable JSON.
      --limit int                Retry at most this many team dead-letter queue items or failed team pipeline jobs, and advance at most this many ready team pipeline jobs; 0 means no limit.
      --max-cycles int           With --until-idle, stop after this many cycles if work keeps appearing. (default 20)
      --preview-routes           With --dry-run, include route and dispatch payload previews for retried or ready team pipeline steps.
      --ready-timeout duration   Maximum time to wait for implicit daemon readiness (0 = no timeout). (default 3s)
      --repo string              Repo root. (default "<repo>")
      --retry-message string     Audit message to record when --retry-pipelines resets failed team steps.
      --retry-pipelines          Reset failed team pipeline steps and dispatch them before the scoped team tick.
      --retry-step string        With --retry-pipelines, retry only failed team jobs whose next failed step has this id.
      --skip-daemon              Do not start or reconcile the daemon.
      --skip-queue               Do not retry team-owned dead-letter queue items.
      --skip-tick                Do not run a scoped team tick after queue retry.
      --until-idle               Run scoped team ticks until no immediate team queue, schedule, or pipeline work remains.
      --workspace string         Workspace mode for retried or advanced team pipeline steps: auto, worktree, or repo. (default "auto")
```

## `agent-team team restart`

Restart or resume a team&#39;s declared persistent instances.

```text
agent-team team restart <team> [flags]
```

Flags:

```text
      --attach                   Follow the selected instance log after restarting or resuming. Requires exactly one selected instance.
      --dry-run                  Preview planned restart/resume actions without changing daemon state.
  -f, --force                    Escalate to SIGKILL if a running instance does not stop within --timeout before restarting.
      --format string            Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                     Emit machine-readable JSON.
      --prompt string            Override the default kickoff prompt for instances started fresh.
  -q, --quiet                    Suppress non-error output and use only the exit code.
      --ready-timeout duration   Maximum time to wait for implicit daemon readiness (0 = no timeout). (default 3s)
      --repo string              Repo root. (default "<repo>")
      --summary                  Show aggregate action counts instead of per-instance rows.
      --tail string              With --attach, show only the last N lines before following (0 or all = all). (default "50")
      --timeout duration         Maximum time to wait for each running instance to stop before resuming (0 = daemon default).
      --wait                     Wait for selected instances to become healthy after restarting.
      --wait-timeout duration    Maximum time to wait for health with --wait (0 = no timeout).
```

## `agent-team team retry`

Reset failed pipeline steps owned by one team.

Reset or preview failed-step retries for jobs in one team&#39;s declared pipelines. Pass --step to target one stage, or --dispatch to immediately dispatch each reset retry.

```text
agent-team team retry <team> [flags]
```

Flags:

```text
      --dispatch             Dispatch each reset failed step immediately.
      --dry-run              Preview failed-step resets and optional dispatches without writing job or daemon state.
      --format string        Render each result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.
      --json                 Emit retry results as JSON.
      --limit int            Retry at most this many failed team jobs; 0 means no limit.
      --message string       Status message recorded on each retried team job.
      --preview-routes       With --dry-run --dispatch, include local topology route and dispatch payload previews.
      --repo string          Repo root. (default "<repo>")
      --runtime string       Runtime profile for --dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string   Runtime binary for --dispatch. Overrides env and repo config.
      --step string          Retry only failed team jobs whose next failed step has this id.
      --workspace string     Workspace mode for retried dispatches: auto, worktree, or repo. (default "auto")
```

## `agent-team team run`

Create a durable job through a team&#39;s pipeline.

Create a durable job using one of the team&#39;s declared pipelines. If the team declares exactly one pipeline, it is selected automatically; otherwise pass --pipeline.

```text
agent-team team run <team> <ticket> [kickoff...] [flags]
```

Flags:

```text
      --dispatch              Dispatch the first ready pipeline step immediately using the running daemon.
      --dry-run               Preview the pipeline job that would be created without writing it.
      --format string         Render the created or advanced job with a Go template, e.g. '{{.ID}} {{.Pipeline}}'.
      --id string             Override the normalized job id (default: ticket slug).
      --json                  Emit the created job or advance result as JSON.
      --kickoff string        Kickoff text for the first pipeline step.
      --kickoff-file string   Read kickoff text from a file.
      --pipeline string       Team pipeline to use when the team declares more than one.
      --repo string           Repo root. (default "<repo>")
      --runtime string        Runtime profile for --dispatch (claude or codex). Overrides env and repo config.
      --runtime-bin string    Runtime binary for --dispatch. Overrides env and repo config.
      --ticket-url string     Canonical ticket URL to store on the job.
      --workspace string      Workspace mode for --dispatch: auto, worktree, or repo. (default "auto")
```

## `agent-team team schedules`

List schedules owned by one team.

```text
agent-team team schedules <team> [flags]
```

Flags:

```text
      --format string   Render each schedule with a Go template, e.g. '{{.Name}} {{.Every}}'.
      --json            Emit team schedules as JSON.
      --repo string     Repo root. (default "<repo>")
```

## `agent-team team send`

Send a mailbox message to team-owned instances.

Send a mailbox message to running daemon-known instances owned by one declared team. Use --all to include every lifecycle status, or combine selectors such as --status, --phase, --latest, --last, --stale, and --unhealthy.

```text
agent-team team send <team> [message...] [flags]
```

Flags:

```text
      --all                   Send to every daemon-known team instance regardless of lifecycle status.
      --dry-run               Preview matching recipients without appending mailbox messages.
      --format string         Render each send result with a Go template, e.g. '{{.To}} {{.ID}}'.
      --from string           Sender label recorded with the message. (default "(cli)")
      --json                  Emit machine-readable JSON.
  -n, --last int              Send to the N most recently started team-owned daemon-known instances after other filters (0 = all).
      --latest                Send to the most recently started team-owned daemon-known instance after other filters.
      --message string        Message text to send.
      --message-file string   Read message text from a file, or '-' for stdin.
      --phase strings         Send to team-owned instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --repo string           Repo root. (default "<repo>")
      --stale                 Send to team-owned instances whose status.toml is stale.
      --status strings        Send to team-owned instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --unhealthy             Send to team-owned instances that are crashed or stale.
```

## `agent-team team show`

Show one declared team.

```text
agent-team team show <team> [flags]
```

Flags:

```text
      --json          Emit the team as JSON.
      --repo string   Repo root. (default "<repo>")
```

## `agent-team team snapshot`

Capture a team-scoped diagnostic report.

Capture a read-only diagnostic report scoped to one declared team. It includes team health, plan, instances, jobs, job status preview, queue, schedule, runtime, and lifecycle event state.

```text
agent-team team snapshot <team> [flags]
```

Flags:

```text
      --events int           Recent matching team lifecycle events to include. Use -1 for all matching events or 0 to skip events. (default 50)
      --json                 Emit the full snapshot JSON to stdout.
      --no-redact            Include raw payload values instead of redacting sensitive keys.
  -o, --output string        Write the full JSON snapshot to this file. Use '-' for stdout.
      --repo string          Repo root. (default "<repo>")
      --schedule-limit int   Upcoming team schedules to include after ordering; 0 means all. (default 10)
```

## `agent-team team stats`

Show CPU and memory usage for team-owned instances.

Show a one-shot or watchable resource snapshot for instances owned by one declared team. With no names, only running team-owned instances are shown. Use --all to include stopped, exited, crashed, and missing persistent team members.

```text
agent-team team stats <team> [<instance>...] [flags]
```

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
      --repo string         Repo root. (default "<repo>")
      --sort string         Sort rows by name, cpu, mem, rss, status, agent, phase, stale, or unhealthy. (default "name")
      --stale               Only show team-owned instances whose status.toml is stale.
      --status strings      Only show team-owned lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary             Show aggregate CPU, memory, and RSS totals instead of team instance rows.
      --unhealthy           Only show crashed or stale team-owned instances.
  -w, --watch               Refresh team stats until interrupted.
```

## `agent-team team status`

Summarize one team&#39;s instances, jobs, and pipelines.

```text
agent-team team status <team> [flags]
```

Flags:

```text
      --format string       Render team status with a Go template, e.g. '{{.Team.Name}} {{.InstanceSummary.Total}}'.
      --interval duration   Refresh interval for --watch. (default 2s)
      --json                Emit team status as JSON.
      --no-clear            With --watch, append snapshots instead of redrawing the terminal.
      --repo string         Repo root. (default "<repo>")
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
      --dry-run                  Preview team topology convergence without starting the daemon or instances.
      --format string            Render each sync action with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                     Emit machine-readable JSON.
  -q, --quiet                    Suppress non-error output and use only the exit code.
      --ready-timeout duration   Maximum time to wait for implicit daemon readiness (0 = no timeout). (default 3s)
      --repo string              Repo root. (default "<repo>")
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
      --dry-run             Preview team-owned maintenance work without mutating state.
      --format string       Render the team tick result with a Go template, e.g. '{{.Team.Name}} {{.Tick.Queue.WouldDispatch}}'.
      --interval duration   Refresh interval for --watch, or delay between --until-idle cycles. (default 2s)
      --json                Emit machine-readable JSON.
      --limit int           Advance at most this many ready pipeline jobs; 0 means no limit.
      --max-cycles int      With --until-idle, stop after this many cycles if work keeps appearing. (default 20)
      --preview-routes      With --dry-run, include route and dispatch payload previews for ready pipeline steps.
      --repo string         Repo root. (default "<repo>")
      --skip-advance        Skip pipeline advancement work.
      --skip-drain          Skip queue drain work.
      --skip-schedules      Skip due schedule work.
      --until-idle          Run team tick cycles until no immediate team schedule, queue, or pipeline work remains.
  -w, --watch               Run the team tick repeatedly until interrupted.
      --workspace string    Workspace mode for advanced pipeline steps: auto, worktree, or repo. (default "auto")
```

## `agent-team team triage`

Show team-owned jobs that need operator attention.

Show a compact team-scoped work queue triage view from durable jobs, persisted daemon queue items, status-file update previews, and ready pipeline steps.

```text
agent-team team triage <team> [flags]
```

Flags:

```text
      --format string          Render the team triage snapshot with a Go template, e.g. '{{.Summary.Total}} {{len .Attention}}'.
      --interval duration      Refresh interval for --watch. (default 2s)
      --json                   Emit team triage snapshot as JSON.
      --min-severity string    Only show attention rows at least this severe: critical, warning, or info.
      --no-clear               With --watch, append snapshots instead of redrawing the terminal.
      --reason strings         Only show attention rows with this reason. Can repeat or comma-separate.
      --repo string            Repo root. (default "<repo>")
      --stale-after duration   Flag queued or running jobs with no update after this duration (default: [health].job_stale_after or 24h; 0 disables stale checks). (default 24h0m0s)
  -w, --watch                  Refresh the team triage view until interrupted.
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
      --dry-run                  Preview planned start/resume actions without changing daemon state.
      --format string            Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.
      --json                     Emit machine-readable JSON.
      --prompt string            Override the default kickoff prompt.
  -q, --quiet                    Suppress non-error output and use only the exit code.
      --ready-timeout duration   Maximum time to wait for implicit daemon readiness (0 = no timeout). (default 3s)
      --repo string              Repo root. (default "<repo>")
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
      --dry-run               Preview selected team instances and current state without waiting.
      --fail-on-crash         Exit 1 if any selected instance resolves to crashed.
      --format string         Render each wait result with a Go template, e.g. '{{.Instance}} {{.Status}} {{.Phase}}'.
      --interval duration     Polling interval. (default 500ms)
      --json                  Emit machine-readable JSON.
  -n, --last int              Wait for the N most recently started team-owned instances after other filters (0 = all).
      --latest                Wait for the most recently started team-owned instance after other filters.
      --phase strings         Wait for team-owned instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
  -q, --quiet                 Suppress output and use only the exit code.
      --repo string           Repo root. (default "<repo>")
      --stale                 Wait for team-owned instances whose status.toml is stale.
      --status strings        Wait for team-owned instances currently in this lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary               Show aggregate final status and phase counts instead of per-instance rows.
      --timeout duration      Maximum time to wait (0 = no timeout).
      --unhealthy             Wait for team-owned instances that are crashed or stale.
      --until string          Lifecycle condition to wait for: running, terminal, stopped, exited, crashed, or removed. (default "running")
      --until-phase strings   Work phase condition to wait for: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
```

## `agent-team template`

Manage templates (bundled + cached) used by `agent-team init`.

Manage templates: list, inspect, pull, and remove. A template is a parameterised directory tree with a `template.toml` manifest. The default template is embedded in the binary and can be referenced as `bundled` or `default`; additional templates are pulled from local paths into a local cache.

```text
agent-team template
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
agent-team template ls
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team template pull`

Fetch a template into the cache so it can be referenced later.

Pull a template into ~/.agent-team/cache/&lt;ref&gt;. Local directory refs are copied. Git refs such as github.com/acme/eng-team@v1.0.0 or https://github.com/acme/eng-team.git@v1.0.0 are cloned at the requested revision. Bundled templates need no pull because they are embedded in the binary.

```text
agent-team template pull <ref> [flags]
```

Flags:

```text
      --as string   Cache key to store under (defaults to <name>@<version> from manifest, or basename).
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team template rm`

Remove a template from the cache.

```text
agent-team template rm <ref>
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team template run`

One-shot: instantiate a template into a tempdir and spawn an agent.

Instantiate a template (bundled, local path, or cached ref) into a target directory and immediately spawn the named agent against it. Returns when the selected runtime session exits. Without --target, a tempdir under $XDG_CACHE_HOME/agent-team/runs (or ~/.agent-team/runs) is created and removed on exit unless --keep is passed. With --target, the directory is preserved.

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
      --runtime string       Runtime profile for this invocation (claude or codex). Overrides env and rendered repo config.
      --runtime-bin string   Runtime binary for this invocation. Overrides env and rendered repo config.
      --set stringArray      Set a template parameter, e.g. --set linear.team_id=<uuid>. Repeatable.
      --target string        Target directory (must not already contain .agent_team/ unless --force). Defaults to a tempdir.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team template show`

Print a template&#39;s manifest. Default ref: bundled (alias: default).

```text
agent-team template show [ref]
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team template smoke`

Render a template into a temp repo and validate it.

Render a template into a temporary repo with init --no-input semantics, then run doctor, pipeline doctor, and team doctor. Pass --set for required parameters.

```text
agent-team template smoke [ref] [flags]
```

Flags:

```text
      --json              Emit smoke results as JSON.
      --keep              Keep the temporary rendered repo for inspection.
      --set stringArray   Set a template parameter, e.g. --set linear.team_id=<uuid>. Repeatable.
      --strict-daemon     Fail doctor when the companion agent-teamd binary is not discoverable.
      --strict-runtime    Fail doctor when the selected LLM runtime binary is not discoverable.
      --strict-template   Fail doctor when rendered template provenance does not resolve cleanly.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team tick`

Run one orchestration maintenance cycle.

Run one orchestration maintenance cycle against the running daemon: reconcile process metadata and job status files, fire due schedules, drain ready queue items, then advance ready pipeline jobs.

```text
agent-team tick [flags]
```

Flags:

```text
      --dry-run             Preview job status reconciliation, schedule firing, queue drain, and pipeline advancement without mutating state.
      --format string       Render the tick result or until-idle aggregate with a Go template, e.g. '{{.Queue.Dispatched}} {{len .Advance}}'.
      --interval duration   Refresh interval for --watch, or delay between --until-idle cycles. (default 2s)
      --json                Emit machine-readable JSON.
      --limit int           Advance at most this many ready pipeline jobs; 0 means no limit.
      --max-cycles int      With --until-idle, stop after this many cycles if work keeps appearing. (default 20)
      --preview-routes      With --dry-run, include route and dispatch payload previews for ready pipeline steps.
      --skip-advance        Skip pipeline advancement.
      --skip-drain          Skip queue draining.
      --skip-reconcile      Skip daemon metadata and job status reconciliation.
      --skip-schedules      Skip firing due schedules.
      --target string       Repo root. (default "<repo>")
      --until-idle          Run tick cycles until no immediate schedule, queue, or pipeline work remains.
  -w, --watch               Run tick repeatedly until interrupted.
      --workspace string    Workspace mode for advanced pipeline steps: auto, worktree, or repo. (default "auto")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team topology`

Show declared instances and triggers (reads .agent_team/instances.toml).

```text
agent-team topology
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

Subcommands:

- `agent-team topology reload` - Re-read instances.toml from disk (daemon must be running).
- `agent-team topology show` - Print the resolved topology (declared instances + triggers).
- `agent-team topology summary` - Summarize declared topology and workflow health.

## `agent-team topology reload`

Re-read instances.toml from disk (daemon must be running).

```text
agent-team topology reload [flags]
```

Flags:

```text
      --target string   Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team topology show`

Print the resolved topology (declared instances + triggers).

```text
agent-team topology show [flags]
```

Flags:

```text
      --json            Emit raw JSON.
      --target string   Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team topology summary`

Summarize declared topology and workflow health.

```text
agent-team topology summary [flags]
```

Flags:

```text
      --json            Emit topology summary as JSON.
      --target string   Repo root. (default "<repo>")
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --dry-run         With --apply, preview the clean/conflicting file actions without writing files.
      --format string   Render the upgrade check result with a Go template, e.g. '{{.Differs}} {{.TargetVersion}}'.
      --json            Emit the upgrade check result as JSON.
      --strict          With --check, exit 1 when the target template differs from the lock.
      --target string   Repo root. (default "<repo>")
      --to string       Template ref to compare against (defaults to the ref in .template.lock).
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
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
      --dry-run               Preview selected instances and current state without waiting.
      --fail-on-crash         Exit 1 if any selected instance resolves to crashed.
      --format string         Render each wait result with a Go template, e.g. '{{.Instance}} {{.Status}} {{.Phase}}'.
      --interval duration     Polling interval. (default 500ms)
      --json                  Emit machine-readable JSON.
  -n, --last int              Wait for the N most recently started daemon-known instances after other filters (0 = all).
      --latest                Wait for the most recently started daemon-known instance after other filters.
      --phase strings         Wait for daemon-known instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
  -q, --quiet                 Suppress output and use only the exit code.
      --stale                 Wait for daemon-known instances whose status.toml is stale.
      --status strings        Wait for daemon-known instances currently in this lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --summary               Show aggregate final status and phase counts instead of per-instance rows.
      --target string         Repo root. (default "<repo>")
      --timeout duration      Maximum time to wait (0 = no timeout).
      --unhealthy             Wait for daemon-known instances that are crashed or stale.
      --until string          Lifecycle condition to wait for: terminal, running, stopped, exited, crashed, or removed. (default "terminal")
      --until-phase strings   Work phase condition to wait for: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

## `agent-team watch`

Watch the combined health, instance, and resource monitor.

Watch the Docker-style operator monitor, refreshing fleet health, instance state, and daemon-managed process stats until interrupted.

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
      --format string          Render each monitor snapshot with a Go template, e.g. '{{.Health.Healthy}} {{len .Instances}}'.
      --instance strings       Only show instances, stats, and plan rows with this name. Can repeat or comma-separate.
      --interval duration      Refresh interval. (default 2s)
      --jobs                   Include durable job summary, attention, and ready-step state.
      --json                   Emit one JSON object per refresh.
  -n, --last int               Show only the N most recently started instances after other filters (0 = all).
      --latest                 Show only the most recently started instance after other filters.
      --no-clear               Append snapshots instead of redrawing the terminal.
      --phase strings          Only show instances and stats in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.
      --plan                   Include desired-state actions from instances.toml and daemon metadata.
      --resources              With --summary, include aggregate CPU, memory, and RSS totals.
      --schedules              Include due and upcoming declared schedule state.
      --since string           With --events, only show lifecycle events since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.
      --sort string            Sort instance rows by name, status, agent, phase, stale, unhealthy, started, stopped, or exited. (default "name")
      --stale                  Only show instances whose status.toml is stale.
      --stats-sort string      Sort stats rows by name, cpu, mem, rss, status, agent, phase, stale, or unhealthy. (default "name")
      --status strings         Only show lifecycle status in instance, stats, and plan sections: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.
      --stop-extras            With --plan, preview running topology extras as stop actions.
      --strict-topology        Treat running daemon-known instances not declared in instances.toml as unhealthy.
      --summary                Watch compact non-failing fleet health and optional plan summaries instead of the full monitor.
      --target string          Repo root. (default "<repo>")
      --unhealthy              Only show crashed or stale instances.
```

Inherited Flags:

```text
      --repo string   Repo root for commands that read .agent_team; overrides legacy repo-root --target flags.
```

