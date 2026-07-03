package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/runtimebin"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newStartCmd() *cobra.Command {
	var (
		target         string
		prompt         string
		promptFile     string
		all            bool
		latest         bool
		last           int
		agents         []string
		runtimeFilters []string
		statusFilters  []string
		phaseFilters   []string
		staleOnly      bool
		runtimeStale   bool
		unhealthyOnly  bool
		wait           bool
		timeout        time.Duration
		readyTimeout   time.Duration
		dryRun         bool
		commands       bool
		summary        bool
		attach         bool
		tail           string
		quiet          bool
		jsonOut        bool
		format         string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "start [<instance>...]",
		Aliases: []string{"up"},
		Short:   "Start agent-teamd if needed, then start or resume instances.",
		Long: "Docker-like convenience command for the common lifecycle path. It starts the per-repo " +
			"daemon in the background when needed. With no args, it brings up declared persistent instances from " +
			"instances.toml; explicit names may also resume daemon-known ad-hoc instances.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --all cannot be combined with instance names.")
				return exitErr(2)
			}
			if latest && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --latest cannot be combined with instance names.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --last must be >= 0.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --latest or --last.")
				return exitErr(2)
			}
			if last > 0 && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --last cannot be combined with instance names.")
				return exitErr(2)
			}
			if len(agents) > 0 && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --agent cannot be combined with instance names.")
				return exitErr(2)
			}
			if len(runtimeFilters) > 0 && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --runtime cannot be combined with instance names.")
				return exitErr(2)
			}
			if len(statusFilters) > 0 && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --status cannot be combined with instance names.")
				return exitErr(2)
			}
			if len(phaseFilters) > 0 && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --phase cannot be combined with instance names.")
				return exitErr(2)
			}
			if staleOnly && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --stale cannot be combined with instance names.")
				return exitErr(2)
			}
			if runtimeStale && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --runtime-stale cannot be combined with instance names.")
				return exitErr(2)
			}
			if unhealthyOnly && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --unhealthy cannot be combined with instance names.")
				return exitErr(2)
			}
			if timeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --timeout must be >= 0.")
				return exitErr(2)
			}
			if readyTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --ready-timeout must be >= 0.")
				return exitErr(2)
			}
			if dryRun && wait {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --dry-run cannot be combined with --wait.")
				return exitErr(2)
			}
			if err := validateLifecycleCommandsFlag(cmd, dryRun, commands, jsonOut, summary, quiet, format, attach); err != nil {
				return err
			}
			if attach && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --attach cannot be combined with --json.")
				return exitErr(2)
			}
			if quiet && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --quiet or --json.")
				return exitErr(2)
			}
			if quiet && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --quiet or --summary.")
				return exitErr(2)
			}
			if summary && attach {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --summary cannot be combined with --attach.")
				return exitErr(2)
			}
			if format != "" && (quiet || jsonOut || attach || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format cannot be combined with --quiet, --json, --attach, or --summary.")
				return exitErr(2)
			}
			if quiet && attach {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --quiet cannot be combined with --attach.")
				return exitErr(2)
			}
			if attach && dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --attach cannot be combined with --dry-run.")
				return exitErr(2)
			}
			if attach && wait {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --attach or --wait.")
				return exitErr(2)
			}
			if !attach && cmd.Flags().Changed("tail") {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --tail requires --attach.")
				return exitErr(2)
			}
			tailLines, err := parseLogTail(tail)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			formatTemplate, err := parseLifecycleActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			if _, err := lifecycleStatusFilterSet(statusFilters); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			if _, err := lifecycleRuntimeFilterSet(runtimeFilters); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			if _, err := lifecyclePhaseFilterSet(phaseFilters); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			resolvedPrompt, err := promptTextWithFile(prompt, promptFile)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			if !dryRun {
				if err := ensureDaemonReadyWithTimeout(cmd, target, jsonOut || quiet || summary || formatTemplate != nil, readyTimeout); err != nil {
					return err
				}
			}
			return runInstanceUpWithOptions(cmd, target, resolvedPrompt, args, instanceUpOptions{
				All:            all,
				Latest:         latest,
				Limit:          last,
				AgentFilters:   agents,
				RuntimeFilters: runtimeFilters,
				StatusFilters:  statusFilters,
				PhaseFilters:   phaseFilters,
				Stale:          staleOnly,
				RuntimeStale:   runtimeStale,
				Unhealthy:      unhealthyOnly,
				Wait:           wait,
				Timeout:        timeout,
				DryRun:         dryRun,
				Summary:        summary,
				Attach:         attach,
				AttachTail:     tailLines,
				AttachTailSet:  cmd.Flags().Changed("tail"),
				Quiet:          quiet,
				JSON:           jsonOut,
				Format:         formatTemplate,
				Commands:       commands,
				Command: scopedLifecycleCommandOptions(cmd, target, lifecycleCommandOptions{
					BaseArgs:        []string{"agent-team", "start"},
					Names:           args,
					All:             all,
					Latest:          latest,
					Limit:           last,
					AgentFilters:    agents,
					RuntimeFilters:  runtimeFilters,
					StatusFilters:   statusFilters,
					PhaseFilters:    phaseFilters,
					Stale:           staleOnly,
					RuntimeStale:    runtimeStale,
					Unhealthy:       unhealthyOnly,
					Prompt:          prompt,
					PromptSet:       cmd.Flags().Changed("prompt"),
					PromptFile:      promptFile,
					PromptFileSet:   cmd.Flags().Changed("prompt-file"),
					Timeout:         timeout,
					TimeoutSet:      cmd.Flags().Changed("timeout"),
					ReadyTimeout:    readyTimeout,
					ReadyTimeoutSet: cmd.Flags().Changed("ready-timeout"),
				}),
			})
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&prompt, "prompt", "", "Override the default kickoff prompt.")
	cmd.Flags().StringVar(&promptFile, "prompt-file", "", "Read kickoff prompt from a file, or '-' for stdin.")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Start or resume every declared persistent and daemon-known instance.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Start or resume the most recently started instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Start or resume the N most recently started instances after other filters (0 = all).")
	cmd.Flags().StringSliceVar(&agents, "agent", nil, "Start or resume every declared persistent and daemon-known instance for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only start or resume daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only start or resume instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only start or resume instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only start or resume instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&runtimeStale, "runtime-stale", false, "Only start or resume running instances whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only start or resume instances that are crashed, status-stale, or runtime-stale.")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for selected instances to become healthy after starting. With no scoped selection, waits for the fleet.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&readyTimeout, "ready-timeout", defaultDaemonReadyTimeout, "Maximum time to wait for implicit daemon readiness (0 = no timeout).")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview planned start/resume actions without changing daemon state.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate action counts instead of per-instance rows.")
	cmd.Flags().BoolVar(&attach, "attach", false, "Follow the selected instance log after starting or resuming. Requires exactly one selected instance.")
	cmd.Flags().StringVar(&tail, "tail", "50", "With --attach, show only the last N lines before following (0 or all = all).")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return cmd
}

func newStopCmd() *cobra.Command {
	var (
		target         string
		all            bool
		latest         bool
		last           int
		agents         []string
		runtimeFilters []string
		statusFilters  []string
		phaseFilters   []string
		staleOnly      bool
		runtimeStale   bool
		unhealthyOnly  bool
		force          bool
		wait           bool
		timeout        time.Duration
		waitTimeout    time.Duration
		dryRun         bool
		commands       bool
		remove         bool
		summary        bool
		quiet          bool
		jsonOut        bool
		format         string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "stop [<instance>...]",
		Aliases: []string{"down"},
		Short:   "Stop running instances.",
		Long: "Docker-like convenience alias for `agent-team instance down`. With no args, stops running declared " +
			"persistent instances. Use --all to stop every daemon-managed running instance, including ad-hoc and ephemeral work.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateLifecycleCommandsFlag(cmd, dryRun, commands, jsonOut, summary, quiet, format, false); err != nil {
				return err
			}
			if quiet && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --quiet or --json.")
				return exitErr(2)
			}
			if quiet && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --quiet or --summary.")
				return exitErr(2)
			}
			if format != "" && (quiet || jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format cannot be combined with --quiet, --json, or --summary.")
				return exitErr(2)
			}
			formatTemplate, err := parseLifecycleActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			return runInstanceDownWithOptions(cmd, target, args, instanceDownOptions{
				All:            all,
				Latest:         latest,
				Limit:          last,
				AgentFilters:   agents,
				RuntimeFilters: runtimeFilters,
				StatusFilters:  statusFilters,
				PhaseFilters:   phaseFilters,
				Stale:          staleOnly,
				RuntimeStale:   runtimeStale,
				Unhealthy:      unhealthyOnly,
				Force:          force,
				Wait:           wait,
				Timeout:        timeout,
				WaitTimeout:    waitTimeout,
				WaitTimeoutSet: cmd.Flags().Changed("wait-timeout"),
				DryRun:         dryRun,
				Remove:         remove,
				Summary:        summary,
				Quiet:          quiet,
				JSON:           jsonOut,
				Format:         formatTemplate,
				Commands:       commands,
				Command: scopedLifecycleCommandOptions(cmd, target, lifecycleCommandOptions{
					BaseArgs:       []string{"agent-team", "stop"},
					Names:          args,
					All:            all,
					Latest:         latest,
					Limit:          last,
					AgentFilters:   agents,
					RuntimeFilters: runtimeFilters,
					StatusFilters:  statusFilters,
					PhaseFilters:   phaseFilters,
					Stale:          staleOnly,
					RuntimeStale:   runtimeStale,
					Unhealthy:      unhealthyOnly,
					Force:          force,
					Remove:         remove,
					Timeout:        timeout,
					TimeoutSet:     cmd.Flags().Changed("timeout"),
				}),
			})
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Stop every daemon-managed running instance.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Stop the most recently started running instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Stop the N most recently started running instances after other filters (0 = all).")
	cmd.Flags().StringSliceVar(&agents, "agent", nil, "Stop every running instance for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only stop running daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Stop daemon-known instances currently in this lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Stop daemon-known instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only stop instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&runtimeStale, "runtime-stale", false, "Only stop running instances whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only stop instances that are crashed, status-stale, or runtime-stale.")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Escalate to SIGKILL if an instance does not stop within --timeout.")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for stopped instances to reach a terminal state.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Grace before --force kills. With --wait and no --wait-timeout, also used as the wait deadline (0 = no wait deadline; force defaults to 10s).")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait for terminal state with --wait. Defaults to --timeout when unset; set 0 explicitly for no wait timeout.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview planned stop actions without changing daemon state.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&remove, "rm", false, "Remove selected instance state and daemon metadata after stopping.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate action counts instead of per-instance rows.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return cmd
}

func newKillCmd() *cobra.Command {
	var (
		target         string
		all            bool
		latest         bool
		last           int
		agents         []string
		runtimeFilters []string
		statusFilters  []string
		phaseFilters   []string
		staleOnly      bool
		runtimeStale   bool
		unhealthyOnly  bool
		timeout        time.Duration
		wait           bool
		waitTimeout    time.Duration
		dryRun         bool
		commands       bool
		remove         bool
		summary        bool
		quiet          bool
		jsonOut        bool
		format         string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "kill [<instance>...]",
		Short: "Force-stop running instances.",
		Long: "Docker-like forced stop. Sends the daemon stop request with force escalation enabled. " +
			"With no args, targets running declared persistent instances; use --all for every daemon-managed running instance.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateLifecycleCommandsFlag(cmd, dryRun, commands, jsonOut, summary, quiet, format, false); err != nil {
				return err
			}
			if quiet && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --quiet or --json.")
				return exitErr(2)
			}
			if quiet && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --quiet or --summary.")
				return exitErr(2)
			}
			if format != "" && (quiet || jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format cannot be combined with --quiet, --json, or --summary.")
				return exitErr(2)
			}
			formatTemplate, err := parseLifecycleActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			return runInstanceDownWithOptions(cmd, target, args, instanceDownOptions{
				All:            all,
				Latest:         latest,
				Limit:          last,
				AgentFilters:   agents,
				RuntimeFilters: runtimeFilters,
				StatusFilters:  statusFilters,
				PhaseFilters:   phaseFilters,
				Stale:          staleOnly,
				RuntimeStale:   runtimeStale,
				Unhealthy:      unhealthyOnly,
				Force:          true,
				Wait:           wait,
				Timeout:        timeout,
				WaitTimeout:    waitTimeout,
				WaitTimeoutSet: cmd.Flags().Changed("wait-timeout"),
				DryRun:         dryRun,
				Remove:         remove,
				Summary:        summary,
				Quiet:          quiet,
				Action:         "kill",
				JSON:           jsonOut,
				Format:         formatTemplate,
				Commands:       commands,
				Command: scopedLifecycleCommandOptions(cmd, target, lifecycleCommandOptions{
					BaseArgs:       []string{"agent-team", "kill"},
					Names:          args,
					All:            all,
					Latest:         latest,
					Limit:          last,
					AgentFilters:   agents,
					RuntimeFilters: runtimeFilters,
					StatusFilters:  statusFilters,
					PhaseFilters:   phaseFilters,
					Stale:          staleOnly,
					RuntimeStale:   runtimeStale,
					Unhealthy:      unhealthyOnly,
					Remove:         remove,
					Timeout:        timeout,
					TimeoutSet:     cmd.Flags().Changed("timeout"),
				}),
			})
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Force-stop every daemon-managed running instance.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Force-stop the most recently started running instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Force-stop the N most recently started running instances after other filters (0 = all).")
	cmd.Flags().StringSliceVar(&agents, "agent", nil, "Force-stop every running instance for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only force-stop running daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Force-stop daemon-known instances currently in this lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Force-stop daemon-known instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only force-stop instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&runtimeStale, "runtime-stale", false, "Only force-stop running instances whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only force-stop instances that are crashed, status-stale, or runtime-stale.")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Second, "Grace before SIGKILL escalation.")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for killed instances to reach a terminal state.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait for terminal state with --wait. Defaults to --timeout when unset; set 0 explicitly for no wait timeout.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview planned kill actions without changing daemon state.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&remove, "rm", false, "Remove selected instance state and daemon metadata after killing.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate action counts instead of per-instance rows.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return cmd
}

func newRestartCmd() *cobra.Command {
	var (
		target         string
		prompt         string
		promptFile     string
		all            bool
		latest         bool
		last           int
		agents         []string
		runtimeFilters []string
		statusFilters  []string
		phaseFilters   []string
		staleOnly      bool
		runtimeStale   bool
		unhealthyOnly  bool
		timeout        time.Duration
		readyTimeout   time.Duration
		wait           bool
		waitTimeout    time.Duration
		force          bool
		dryRun         bool
		commands       bool
		summary        bool
		attach         bool
		tail           string
		quiet          bool
		jsonOut        bool
		format         string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "restart [<instance>...]",
		Short: "Restart or resume instances.",
		Long: "Restart declared persistent instances through the daemon. Running instances are stopped " +
			"and resumed; stopped instances are resumed; instances with no daemon metadata are started fresh. " +
			"Explicit names may also target daemon-known ad-hoc instances. Runtimes without managed " +
			"resume support are reported as unsupported and left untouched.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --all cannot be combined with instance names.")
				return exitErr(2)
			}
			if latest && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --latest cannot be combined with instance names.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --last must be >= 0.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --latest or --last.")
				return exitErr(2)
			}
			if last > 0 && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --last cannot be combined with instance names.")
				return exitErr(2)
			}
			if len(agents) > 0 && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --agent cannot be combined with instance names.")
				return exitErr(2)
			}
			if len(runtimeFilters) > 0 && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --runtime cannot be combined with instance names.")
				return exitErr(2)
			}
			if len(statusFilters) > 0 && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --status cannot be combined with instance names.")
				return exitErr(2)
			}
			if len(phaseFilters) > 0 && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --phase cannot be combined with instance names.")
				return exitErr(2)
			}
			if staleOnly && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --stale cannot be combined with instance names.")
				return exitErr(2)
			}
			if runtimeStale && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --runtime-stale cannot be combined with instance names.")
				return exitErr(2)
			}
			if unhealthyOnly && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --unhealthy cannot be combined with instance names.")
				return exitErr(2)
			}
			if timeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --timeout must be >= 0.")
				return exitErr(2)
			}
			if readyTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --ready-timeout must be >= 0.")
				return exitErr(2)
			}
			if waitTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --wait-timeout must be >= 0.")
				return exitErr(2)
			}
			if dryRun && wait {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --dry-run cannot be combined with --wait.")
				return exitErr(2)
			}
			if err := validateLifecycleCommandsFlag(cmd, dryRun, commands, jsonOut, summary, quiet, format, attach); err != nil {
				return err
			}
			if attach && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --attach cannot be combined with --json.")
				return exitErr(2)
			}
			if quiet && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --quiet or --json.")
				return exitErr(2)
			}
			if quiet && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --quiet or --summary.")
				return exitErr(2)
			}
			if summary && attach {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --summary cannot be combined with --attach.")
				return exitErr(2)
			}
			if format != "" && (quiet || jsonOut || attach || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format cannot be combined with --quiet, --json, --attach, or --summary.")
				return exitErr(2)
			}
			if quiet && attach {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --quiet cannot be combined with --attach.")
				return exitErr(2)
			}
			if attach && dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --attach cannot be combined with --dry-run.")
				return exitErr(2)
			}
			if attach && wait {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --attach or --wait.")
				return exitErr(2)
			}
			if !attach && cmd.Flags().Changed("tail") {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --tail requires --attach.")
				return exitErr(2)
			}
			tailLines, err := parseLogTail(tail)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			formatTemplate, err := parseLifecycleActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			if _, err := lifecycleStatusFilterSet(statusFilters); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			if _, err := lifecycleRuntimeFilterSet(runtimeFilters); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			if _, err := lifecyclePhaseFilterSet(phaseFilters); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			resolvedPrompt, err := promptTextWithFile(prompt, promptFile)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			if !dryRun {
				if err := ensureDaemonReadyWithTimeout(cmd, target, jsonOut || quiet || summary || formatTemplate != nil, readyTimeout); err != nil {
					return err
				}
			}
			return runInstanceRestart(cmd, target, resolvedPrompt, args, instanceRestartOptions{
				All:            all,
				Latest:         latest,
				Limit:          last,
				AgentFilters:   agents,
				RuntimeFilters: runtimeFilters,
				StatusFilters:  statusFilters,
				PhaseFilters:   phaseFilters,
				Stale:          staleOnly,
				RuntimeStale:   runtimeStale,
				Unhealthy:      unhealthyOnly,
				Timeout:        timeout,
				Wait:           wait,
				WaitTimeout:    waitTimeout,
				Force:          force,
				DryRun:         dryRun,
				Summary:        summary,
				Attach:         attach,
				AttachTail:     tailLines,
				AttachTailSet:  cmd.Flags().Changed("tail"),
				Quiet:          quiet,
				JSON:           jsonOut,
				Format:         formatTemplate,
				Commands:       commands,
				Command: scopedLifecycleCommandOptions(cmd, target, lifecycleCommandOptions{
					BaseArgs:        []string{"agent-team", "restart"},
					Names:           args,
					All:             all,
					Latest:          latest,
					Limit:           last,
					AgentFilters:    agents,
					RuntimeFilters:  runtimeFilters,
					StatusFilters:   statusFilters,
					PhaseFilters:    phaseFilters,
					Stale:           staleOnly,
					RuntimeStale:    runtimeStale,
					Unhealthy:       unhealthyOnly,
					Prompt:          prompt,
					PromptSet:       cmd.Flags().Changed("prompt"),
					PromptFile:      promptFile,
					PromptFileSet:   cmd.Flags().Changed("prompt-file"),
					Force:           force,
					Timeout:         timeout,
					TimeoutSet:      cmd.Flags().Changed("timeout"),
					ReadyTimeout:    readyTimeout,
					ReadyTimeoutSet: cmd.Flags().Changed("ready-timeout"),
				}),
			})
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&prompt, "prompt", "", "Override the default kickoff prompt for instances started fresh.")
	cmd.Flags().StringVar(&promptFile, "prompt-file", "", "Read kickoff prompt from a file, or '-' for stdin.")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Restart or resume every declared persistent and daemon-known instance.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Restart or resume the most recently started instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Restart or resume the N most recently started instances after other filters (0 = all).")
	cmd.Flags().StringSliceVar(&agents, "agent", nil, "Restart or resume every declared persistent and daemon-known instance for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only restart or resume daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only restart or resume instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only restart or resume instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only restart or resume instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&runtimeStale, "runtime-stale", false, "Only restart or resume running instances whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only restart or resume instances that are crashed, status-stale, or runtime-stale.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Maximum time to wait for each running instance to stop before resuming (0 = daemon default).")
	cmd.Flags().DurationVar(&readyTimeout, "ready-timeout", defaultDaemonReadyTimeout, "Maximum time to wait for implicit daemon readiness (0 = no timeout).")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for selected instances to become healthy after restarting. With no scoped selection, waits for the fleet.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait for health with --wait (0 = no timeout).")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Escalate to SIGKILL if a running instance does not stop within --timeout before restarting.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview planned restart/resume actions without changing daemon state.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate action counts instead of per-instance rows.")
	cmd.Flags().BoolVar(&attach, "attach", false, "Follow the selected instance log after restarting or resuming. Requires exactly one selected instance.")
	cmd.Flags().StringVar(&tail, "tail", "50", "With --attach, show only the last N lines before following (0 or all = all).")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return cmd
}

func validateLifecycleCommandsFlag(cmd *cobra.Command, dryRun, commands, jsonOut, summary, quiet bool, format string, attach bool) error {
	if !commands {
		return nil
	}
	if !dryRun {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --commands requires --dry-run.")
		return exitErr(2)
	}
	if jsonOut {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --commands cannot be combined with --json.")
		return exitErr(2)
	}
	if summary {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --commands cannot be combined with --summary.")
		return exitErr(2)
	}
	if quiet {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --commands cannot be combined with --quiet.")
		return exitErr(2)
	}
	if format != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --commands cannot be combined with --format.")
		return exitErr(2)
	}
	if attach {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --commands cannot be combined with --attach.")
		return exitErr(2)
	}
	return nil
}

func scopedLifecycleCommandOptions(cmd *cobra.Command, target string, opts lifecycleCommandOptions) lifecycleCommandOptions {
	scope := operatorCommandScopeFromCommand(cmd, target, "target")
	opts.TargetFlag = "--repo"
	opts.Target = scope.Repo
	opts.TargetSet = scope.Set
	return opts
}

func newStatusCmd() *cobra.Command {
	var (
		target           string
		jsonOut          bool
		commands         bool
		watch            bool
		summary          bool
		resources        bool
		plan             bool
		stopExtras       bool
		noClear          bool
		latest           bool
		last             int
		eventTail        int
		eventSince       string
		interval         time.Duration
		statusFilters    []string
		runtimeFilters   []string
		agentFilters     []string
		phaseFilters     []string
		instanceFilters  []string
		eventActions     []string
		actionFilters    []string
		staleOnly        bool
		runtimeStaleOnly bool
		unhealthyOnly    bool
		strictTopology   bool
		format           string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show daemon health and the current instance table.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team status: --interval must be >= 0.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team status: --last must be >= 0.")
				return exitErr(2)
			}
			if eventTail < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team status: --events must be >= 0.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team status: choose one of --latest or --last.")
				return exitErr(2)
			}
			if eventTail > 0 && !summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team status: --events requires --summary.")
				return exitErr(2)
			}
			if resources && !summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team status: --resources requires --summary.")
				return exitErr(2)
			}
			if plan && !summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team status: --plan requires --summary.")
				return exitErr(2)
			}
			if stopExtras && !plan {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team status: --stop-extras requires --plan.")
				return exitErr(2)
			}
			if len(actionFilters) > 0 && !plan {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team status: --action requires --plan.")
				return exitErr(2)
			}
			if strings.TrimSpace(eventSince) != "" && eventTail == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team status: --since requires --events.")
				return exitErr(2)
			}
			if len(eventActions) > 0 && eventTail == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team status: --event-action requires --events.")
				return exitErr(2)
			}
			if format != "" && (jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team status: --format cannot be combined with --json or --summary.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team status: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team status: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if commands && watch {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team status: --commands cannot be combined with --watch.")
				return exitErr(2)
			}
			if strictTopology && !summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team status: --strict-topology requires --summary.")
				return exitErr(2)
			}
			formatTemplate, err := parsePsFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team status: %v\n", err)
				return exitErr(2)
			}
			opts, err := newPsOptionsWithRuntimeInstancesAndUnhealthy(statusFilters, runtimeFilters, agentFilters, phaseFilters, instanceFilters, staleOnly, unhealthyOnly)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team status: %v\n", err)
				return exitErr(2)
			}
			opts.runtimeStale = runtimeStaleOnly
			opts.Limit = last
			if latest {
				opts.Limit = 1
			}
			eventFilters, err := newMonitorEventFilters(eventActions, eventSince, time.Now)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team status: %v\n", err)
				return exitErr(2)
			}
			planActions, err := planActionFilterSet(actionFilters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team status: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				clear := !noClear && !jsonOut
				if summary {
					return runStatusSummaryWatchWithOptions(ctx, out, teamDir, interval, time.Now, jsonOut, statusSummaryOptions{
						Health: healthOptions{
							filters:        opts,
							strictTopology: strictTopology,
						},
						IncludeResources: resources,
						IncludePlan:      plan,
						StopExtras:       stopExtras,
						PlanActions:      planActions,
						EventTail:        eventTail,
						EventFilters:     eventFilters,
					}, clear)
				}
				if formatTemplate != nil {
					return runPsFormatWatch(ctx, out, teamDir, interval, time.Now, opts, formatTemplate)
				}
				return runStatusWatchWithClear(ctx, out, teamDir, interval, time.Now, jsonOut, opts, clear)
			}
			if summary {
				summaryOpts := statusSummaryOptions{
					Health: healthOptions{
						filters:        opts,
						strictTopology: strictTopology,
					},
					IncludeResources: resources,
					IncludePlan:      plan,
					StopExtras:       stopExtras,
					PlanActions:      planActions,
					EventTail:        eventTail,
					EventFilters:     eventFilters,
				}
				if commands {
					scope := operatorCommandScopeFromCommand(cmd, target, "target")
					return runStatusSummaryCommands(out, teamDir, time.Now(), summaryOpts, statusSummaryCommandOptions{
						Scope: scope,
						Plan: planCommandOptions{
							BaseArgs:        []string{"agent-team", "sync"},
							TargetFlag:      "--repo",
							Target:          scope.Repo,
							TargetSet:       scope.Set,
							DryRun:          true,
							StopExtras:      stopExtras,
							StatusFilters:   statusFilters,
							RuntimeFilters:  runtimeFilters,
							AgentFilters:    agentFilters,
							PhaseFilters:    phaseFilters,
							InstanceFilters: instanceFilters,
							ActionFilters:   actionFilters,
						},
					})
				}
				return runStatusSummaryWithOptions(out, teamDir, time.Now(), jsonOut, summaryOpts)
			}
			if commands {
				return runStatusCommands(out, teamDir, time.Now(), opts, operatorCommandScopeFromCommand(cmd, target, "target"))
			}
			if jsonOut {
				return runStatusJSONWithOptions(out, teamDir, time.Now(), opts)
			}
			if formatTemplate != nil {
				return runPsFormatWithOptions(out, teamDir, time.Now(), opts, formatTemplate)
			}
			return runStatusWithOptions(out, teamDir, time.Now(), opts)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print daemon and health remediation commands, one per line. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show a compact non-failing fleet health summary instead of the full instance table.")
	cmd.Flags().BoolVar(&resources, "resources", false, "With --summary, include aggregate CPU, memory, and RSS totals.")
	cmd.Flags().BoolVar(&plan, "plan", false, "With --summary, include desired-state action counts from instances.toml and daemon metadata.")
	cmd.Flags().BoolVar(&stopExtras, "stop-extras", false, "With --plan, preview running topology extras as stop actions.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh daemon health and instance table until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Show only the most recently started instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Show only the N most recently started instances after other filters (0 = all).")
	cmd.Flags().IntVar(&eventTail, "events", 0, "With --summary, include a summary of the last N matching daemon lifecycle events (0 = omit).")
	cmd.Flags().StringSliceVar(&eventActions, "event-action", nil, "With --events, only include lifecycle events with this action. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&eventSince, "since", "", "With --events, only include lifecycle events since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.")
	cmd.Flags().StringSliceVar(&actionFilters, "action", nil, "With --plan, only include plan rows with this action: start, resume, keep, unsupported, on-demand, stop, or extra. Can repeat or comma-separate.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only show lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only show instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&agentFilters, "agent", nil, "Only show instances for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only show work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&instanceFilters, "instance", nil, "Only show instances with this name. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only show instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&runtimeStaleOnly, "runtime-stale", false, "Only show running instances whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only show crashed, status-stale, or runtime-stale instances.")
	cmd.Flags().BoolVar(&strictTopology, "strict-topology", false, "With --summary, treat running daemon-known instances not declared in instances.toml as unhealthy.")
	cmd.Flags().StringVar(&format, "format", "", "Render each instance row with a Go template, e.g. '{{.Instance}} {{.Status}}'.")
	return cmd
}

type statusJSON struct {
	Daemon       statusDaemonJSON       `json:"daemon"`
	Runtime      overviewRuntimeSummary `json:"runtime"`
	RuntimeError string                 `json:"runtime_error,omitempty"`
	Instances    []psJSONRow            `json:"instances"`
	Actions      []string               `json:"actions,omitempty"`
}

type statusDaemonJSON struct {
	Running bool     `json:"running"`
	Ready   bool     `json:"ready"`
	PID     int      `json:"pid,omitempty"`
	Error   string   `json:"error,omitempty"`
	Actions []string `json:"actions,omitempty"`
}

func runStatusJSON(w fmtWriter, teamDir string, now time.Time) error {
	return runStatusJSONWithOptions(w, teamDir, now, psOptions{})
}

func runStatusJSONWithOptions(w fmtWriter, teamDir string, now time.Time, opts psOptions) error {
	daemonStatus := collectDaemonStatus(teamDir)
	rows, err := collectFilteredPsRows(teamDir, now, opts)
	if err != nil {
		return err
	}
	runtime, runtimeErr := collectMonitorRuntimeSummary(teamDir, monitorRuntimeSelectedInstanceSet(rows, opts))
	daemonActions := daemonStatusRemediationActions(daemonStatus)
	actions := append([]string{}, daemonActions...)
	health, err := collectHealthWithOptions(teamDir, now, healthOptions{filters: opts})
	if err != nil {
		return err
	}
	for _, issue := range health.Issues {
		actions = append(actions, issue.Actions...)
	}
	body := statusJSON{
		Daemon: statusDaemonJSON{
			Running: daemonStatus.Running,
			Ready:   daemonStatus.Ready,
			PID:     daemonStatus.PID,
			Error:   daemonStatus.Error,
			Actions: daemonActions,
		},
		Runtime:   runtime,
		Instances: psJSONRows(rows),
		Actions:   commandActionsOnly(actions),
	}
	if runtimeErr != nil {
		body.RuntimeError = runtimeErr.Error()
	}
	if !daemonStatus.Running {
		body.Daemon.PID = 0
	}
	return json.NewEncoder(w).Encode(body)
}

func runStatus(w fmtWriter, teamDir string, now time.Time) error {
	return runStatusWithOptions(w, teamDir, now, psOptions{})
}

func runStatusWithOptions(w fmtWriter, teamDir string, now time.Time, opts psOptions) error {
	daemonStatus := collectDaemonStatus(teamDir)
	if daemonStatus.Running {
		fmt.Fprintf(w, "daemon: running (pid=%d, ready=%s)\n", daemonStatus.PID, yesNo(daemonStatus.Ready))
		if daemonStatus.Error != "" {
			fmt.Fprintf(w, "daemon error: %s\n", daemonStatus.Error)
		}
	} else {
		fmt.Fprintln(w, "daemon: not running")
	}
	fmt.Fprintln(w)
	runtimeSelectedInstances, err := monitorSummaryRuntimeSelectedInstanceSet(teamDir, now, opts)
	if err != nil {
		renderMonitorRuntimeSummary(w, overviewRuntimeSummary{}, err.Error())
	} else if runtime, err := collectMonitorRuntimeSummary(teamDir, runtimeSelectedInstances); err != nil {
		renderMonitorRuntimeSummary(w, overviewRuntimeSummary{}, err.Error())
	} else {
		renderMonitorRuntimeSummary(w, runtime, "")
	}
	fmt.Fprintln(w)
	return runPsWithOptions(w, teamDir, now, opts)
}

func runStatusCommands(w io.Writer, teamDir string, now time.Time, opts psOptions, scope operatorCommandScope) error {
	daemonStatus := collectDaemonStatus(teamDir)
	health, err := collectHealthWithOptions(teamDir, now, healthOptions{filters: opts})
	if err != nil {
		return err
	}
	actions := append([]string{}, daemonStatusRemediationActions(daemonStatus)...)
	for _, issue := range health.Issues {
		actions = append(actions, issue.Actions...)
	}
	return renderOperatorActionCommands(w, actions, scope)
}

func daemonStatusRemediationActions(status daemonStatusJSON) []string {
	if !status.Running {
		return []string{"agent-team daemon start"}
	}
	if !status.Ready {
		return []string{
			"agent-team daemon restart",
			"agent-team daemon logs --tail 80",
		}
	}
	if len(daemonBuildMismatchWarnings(status)) > 0 {
		return []string{"agent-team daemon restart"}
	}
	return nil
}

func runStatusSummary(w io.Writer, teamDir string, now time.Time, jsonOut bool, opts psOptions) error {
	return runStatusSummaryWithHealthOptions(w, teamDir, now, jsonOut, healthOptions{filters: opts})
}

func runStatusSummaryWithHealthOptions(w io.Writer, teamDir string, now time.Time, jsonOut bool, opts healthOptions) error {
	return runStatusSummaryWithOptions(w, teamDir, now, jsonOut, statusSummaryOptions{Health: opts})
}

type statusSummaryOptions struct {
	Health           healthOptions
	IncludeResources bool
	IncludePlan      bool
	StopExtras       bool
	PlanActions      map[string]bool
	EventTail        int
	EventFilters     eventFilters
}

type statusSummarySnapshot struct {
	Health         *healthResult                 `json:"health"`
	Runtime        overviewRuntimeSummary        `json:"runtime"`
	RuntimeError   string                        `json:"runtime_error,omitempty"`
	Resources      *statsSummaryJSON             `json:"resources,omitempty"`
	ResourcesError string                        `json:"resources_error,omitempty"`
	Plan           *lifecycleActionSummaryResult `json:"plan,omitempty"`
	Events         *eventSummaryJSON             `json:"events,omitempty"`
	EventsError    string                        `json:"events_error,omitempty"`
	planRows       []planRow
}

type statusSummaryCommandOptions struct {
	Scope operatorCommandScope
	Plan  planCommandOptions
}

func runStatusSummaryWithOptions(w io.Writer, teamDir string, now time.Time, jsonOut bool, opts statusSummaryOptions) error {
	if !opts.IncludeResources && !opts.IncludePlan && opts.EventTail <= 0 {
		if jsonOut {
			result, err := collectHealthWithOptions(teamDir, now, opts.Health)
			if err != nil {
				return err
			}
			return writeHealthResult(w, result, true)
		}
	}
	snapshot, err := collectStatusSummarySnapshot(teamDir, now, opts)
	if err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(w).Encode(snapshot)
	}
	renderStatusSummarySnapshot(w, snapshot)
	return nil
}

func runStatusSummaryCommands(w io.Writer, teamDir string, now time.Time, opts statusSummaryOptions, commandOpts statusSummaryCommandOptions) error {
	snapshot, err := collectStatusSummarySnapshot(teamDir, now, opts)
	if err != nil {
		return err
	}
	daemonStatus := collectDaemonStatus(teamDir)
	actions := append([]string{}, daemonStatusRemediationActions(daemonStatus)...)
	if snapshot.Health != nil {
		for _, issue := range snapshot.Health.Issues {
			actions = append(actions, issue.Actions...)
		}
	}
	if opts.IncludePlan {
		var planCommands strings.Builder
		if err := renderPlanCommands(&planCommands, snapshot.planRows, commandOpts.Plan); err != nil {
			return err
		}
		actions = append(actions, splitCommandLines(planCommands.String())...)
	}
	return renderOperatorActionCommands(w, actions, commandOpts.Scope)
}

func collectStatusSummarySnapshot(teamDir string, now time.Time, opts statusSummaryOptions) (*statusSummarySnapshot, error) {
	health, err := collectHealthWithOptions(teamDir, now, opts.Health)
	if err != nil {
		return nil, err
	}
	snapshot := &statusSummarySnapshot{Health: health}
	runtimeSelectedInstances, err := monitorSummaryRuntimeSelectedInstanceSet(teamDir, now, opts.Health.filters)
	if err != nil {
		return nil, err
	}
	if runtime, err := collectMonitorRuntimeSummary(teamDir, runtimeSelectedInstances); err != nil {
		snapshot.RuntimeError = err.Error()
	} else {
		snapshot.Runtime = runtime
	}
	var selectedInstances map[string]bool
	if opts.IncludeResources || opts.IncludePlan || opts.EventTail > 0 {
		selectedInstances, err = monitorSummarySelectedInstanceSet(teamDir, now, opts.Health.filters)
		if err != nil {
			return nil, err
		}
	}
	if opts.IncludeResources {
		resources, err := collectStatusSummaryResources(teamDir, now, opts, selectedInstances)
		if err != nil {
			snapshot.ResourcesError = err.Error()
		} else {
			snapshot.Resources = resources
		}
	}
	if opts.IncludePlan {
		plan, err := collectPlan(teamDir)
		if err != nil {
			return nil, err
		}
		if opts.StopExtras {
			markPlanStopExtras(plan)
		}
		planOpts := opts.Health.filters
		if selectedInstances != nil {
			planOpts.instances = selectedInstances
		}
		plan.Instances = filterPlanRowsWithActions(plan.Instances, planOpts, opts.PlanActions)
		summary := summarizeLifecycleActions(planRowsToLifecycleActionResults(plan.Instances, true), true)
		snapshot.Plan = &lifecycleActionSummaryResult{Summary: summary}
		snapshot.planRows = plan.Instances
	}
	if opts.EventTail <= 0 {
		return snapshot, nil
	}
	eventInstanceFilter := opts.Health.filters.instances
	if selectedInstances != nil {
		eventInstanceFilter = selectedInstances
	}
	events, err := collectMonitorSummaryEvents(teamDir, opts.EventTail, statusSummaryEventFilters(opts, eventInstanceFilter), "oldest")
	if err != nil {
		snapshot.EventsError = err.Error()
	} else {
		snapshot.Events = events
	}
	return snapshot, nil
}

func collectStatusSummaryResources(teamDir string, now time.Time, opts statusSummaryOptions, selectedInstances map[string]bool) (*statsSummaryJSON, error) {
	statsOpts := statsOptions{
		All:       true,
		Limit:     opts.Health.filters.Limit,
		Stale:     opts.Health.filters.stale,
		statuses:  opts.Health.filters.statuses,
		agents:    opts.Health.filters.agents,
		phases:    opts.Health.filters.phases,
		instances: opts.Health.filters.instances,
	}
	return collectMonitorResourceSummary(teamDir, now, monitorOptions{
		PS:    opts.Health.filters,
		Stats: statsOpts,
	}, selectedInstances)
}

func statusSummaryEventFilters(opts statusSummaryOptions, instances map[string]bool) eventFilters {
	filters := opts.EventFilters
	filters.agents = opts.Health.filters.agents
	filters.instances = instances
	filters.statuses = opts.Health.filters.statuses
	return filters
}

func renderStatusSummarySnapshot(w io.Writer, snapshot *statusSummarySnapshot) {
	renderHealth(w, snapshot.Health)
	fmt.Fprintln(w)
	renderMonitorRuntimeSummary(w, snapshot.Runtime, snapshot.RuntimeError)
	if snapshot.ResourcesError == "" && snapshot.Resources == nil && snapshot.Plan == nil && snapshot.EventsError == "" && snapshot.Events == nil {
		return
	}
	if snapshot.ResourcesError != "" || snapshot.Resources != nil {
		fmt.Fprintln(w)
		if snapshot.ResourcesError != "" {
			fmt.Fprintf(w, "resources: unavailable: %s\n", snapshot.ResourcesError)
		} else {
			fmt.Fprintln(w, "resources:")
			_ = renderStatsSummary(w, *snapshot.Resources)
		}
	}
	if snapshot.Plan != nil {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "plan:")
		renderLifecycleActionSummary(w, snapshot.Plan.Summary)
	}
	if snapshot.EventsError != "" || snapshot.Events != nil {
		fmt.Fprintln(w)
	}
	if snapshot.EventsError != "" {
		fmt.Fprintf(w, "events: unavailable: %s\n", snapshot.EventsError)
		return
	}
	if snapshot.Events != nil {
		_ = renderEventSummaryResult(w, *snapshot.Events, false)
	}
}

func runStatusSummaryWatch(ctx context.Context, w io.Writer, teamDir string, interval time.Duration, now func() time.Time, jsonOut bool, opts psOptions) error {
	return runStatusSummaryWatchWithClear(ctx, w, teamDir, interval, now, jsonOut, opts, false)
}

func runStatusSummaryWatchWithClear(ctx context.Context, w io.Writer, teamDir string, interval time.Duration, now func() time.Time, jsonOut bool, opts psOptions, clear bool) error {
	return runStatusSummaryHealthWatchWithClear(ctx, w, teamDir, interval, now, jsonOut, healthOptions{filters: opts}, clear)
}

func runStatusSummaryHealthWatchWithClear(ctx context.Context, w io.Writer, teamDir string, interval time.Duration, now func() time.Time, jsonOut bool, opts healthOptions, clear bool) error {
	return runStatusSummaryWatchWithOptions(ctx, w, teamDir, interval, now, jsonOut, statusSummaryOptions{Health: opts}, clear)
}

func runStatusSummaryWatchWithOptions(ctx context.Context, w io.Writer, teamDir string, interval time.Duration, now func() time.Time, jsonOut bool, opts statusSummaryOptions, clear bool) error {
	if !opts.IncludeResources && !opts.IncludePlan && opts.EventTail <= 0 && jsonOut {
		return runHealthWatchWithClear(ctx, w, teamDir, interval, now, jsonOut, opts.Health, clear)
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		snapshot, err := collectStatusSummarySnapshot(teamDir, now(), opts)
		if err != nil {
			return err
		}
		if jsonOut {
			if err := json.NewEncoder(w).Encode(snapshot); err != nil {
				return err
			}
		} else {
			if err := writeWatchClear(w, clear); err != nil {
				return err
			}
			renderStatusSummarySnapshot(w, snapshot)
		}
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
		if !jsonOut && !clear {
			fmt.Fprintln(w)
		}
	}
}

func runStatusWatch(ctx context.Context, w fmtWriter, teamDir string, interval time.Duration, now func() time.Time, jsonOut bool, opts psOptions) error {
	return runStatusWatchWithClear(ctx, w, teamDir, interval, now, jsonOut, opts, false)
}

func runStatusWatchWithClear(ctx context.Context, w fmtWriter, teamDir string, interval time.Duration, now func() time.Time, jsonOut bool, opts psOptions, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if jsonOut {
			if err := runStatusJSONWithOptions(w, teamDir, now(), opts); err != nil {
				return err
			}
		} else {
			if err := writeWatchClear(w, clear); err != nil {
				return err
			}
			if err := runStatusWithOptions(w, teamDir, now(), opts); err != nil {
				return err
			}
		}
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
		if !jsonOut && !clear {
			fmt.Fprintln(w)
		}
	}
}

func newInspectCmd() *cobra.Command {
	var (
		target           string
		jsonOut          bool
		all              bool
		latest           bool
		last             int
		statusFilters    []string
		runtimeFilters   []string
		agentFilters     []string
		phaseFilters     []string
		instanceFilters  []string
		staleOnly        bool
		runtimeStaleOnly bool
		unhealthyOnly    bool
		format           string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "inspect [<instance>...]",
		Short: "Show an instance's runtime, state, and topology.",
		Long:  "Docker-like convenience alias for `agent-team instance show <instance>`.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inspect: --format cannot be combined with --json.")
				return exitErr(2)
			}
			formatTemplate, err := parseInspectFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team inspect: %v\n", err)
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inspect: --last must be >= 0.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inspect: choose one of --latest or --last.")
				return exitErr(2)
			}
			filtersActive := len(statusFilters) > 0 || len(runtimeFilters) > 0 || len(agentFilters) > 0 || len(phaseFilters) > 0 || len(instanceFilters) > 0 || staleOnly || runtimeStaleOnly || unhealthyOnly
			filterOpts, err := newPsOptionsWithRuntimeInstancesAndUnhealthy(statusFilters, runtimeFilters, agentFilters, phaseFilters, instanceFilters, staleOnly, unhealthyOnly)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team inspect: %v\n", err)
				return exitErr(2)
			}
			filterOpts.runtimeStale = runtimeStaleOnly
			if latest {
				filterOpts.Limit = 1
			} else if last > 0 {
				filterOpts.Limit = last
			}
			return runInspect(cmd, target, args, inspectOptions{
				All:           all,
				Latest:        latest,
				Limit:         last,
				JSON:          jsonOut,
				Format:        formatTemplate,
				FiltersActive: filtersActive,
				Filters:       filterOpts,
			})
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Inspect every visible instance.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Inspect the most recently started visible instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Inspect the N most recently started visible instances after other filters (0 = all).")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only inspect instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only inspect instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&agentFilters, "agent", nil, "Only inspect instances for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only inspect instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&instanceFilters, "instance", nil, "Only inspect instances with this name. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only inspect instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&runtimeStaleOnly, "runtime-stale", false, "Only inspect running instances whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only inspect crashed, status-stale, or runtime-stale instances.")
	cmd.Flags().StringVar(&format, "format", "", "Render each instance with a Go template, e.g. '{{.Instance}} {{if .Runtime}}{{.Runtime.Lifecycle}}{{end}}'.")
	return cmd
}

type inspectOptions struct {
	All           bool
	Latest        bool
	Limit         int
	JSON          bool
	Format        *template.Template
	FiltersActive bool
	Filters       psOptions
}

func runInspect(cmd *cobra.Command, target string, names []string, opts inspectOptions) error {
	if opts.All && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --all cannot be combined with instance names.")
		return exitErr(2)
	}
	if opts.Latest && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --latest cannot be combined with instance names.")
		return exitErr(2)
	}
	if opts.Limit > 0 && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --last cannot be combined with instance names.")
		return exitErr(2)
	}
	if opts.FiltersActive && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: inspect filters cannot be combined with instance names.")
		return exitErr(2)
	}
	if !opts.All && !opts.FiltersActive && !opts.Latest && opts.Limit == 0 && len(names) == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: instance is required unless --all, --latest, --last, or a filter is set.")
		return exitErr(2)
	}
	if !opts.All && !opts.FiltersActive && !opts.Latest && opts.Limit == 0 && len(names) == 1 && opts.Format == nil {
		return runInstanceShow(cmd, target, names[0], opts.JSON)
	}

	inspectNames := names
	if opts.All || opts.FiltersActive || opts.Latest || opts.Limit > 0 {
		teamDir, err := resolveTeamDir(cmd, target)
		if err != nil {
			return err
		}
		inspectNames, err = inspectInstanceNamesWithOptions(teamDir, time.Now(), opts.Filters)
		if err != nil {
			return err
		}
		if len(inspectNames) == 0 {
			if opts.JSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode([]inspectJSON{})
			}
			if opts.Format != nil {
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), "(no instances)")
			return nil
		}
	}

	infos := make([]*inspectJSON, 0, len(inspectNames))
	for _, name := range inspectNames {
		info, err := collectInstanceInspect(cmd, target, name, time.Now())
		if err != nil {
			return err
		}
		infos = append(infos, info)
	}
	if opts.JSON {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(infos)
	}
	if opts.Format != nil {
		return renderInspectFormat(cmd.OutOrStdout(), infos, opts.Format)
	}
	out := cmd.OutOrStdout()
	for i, info := range infos {
		if i > 0 {
			fmt.Fprintln(out)
		}
		renderInstanceInspect(out, info)
	}
	return nil
}

func parseInspectFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("inspect-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderInspectFormat(w io.Writer, infos []*inspectJSON, tmpl *template.Template) error {
	for _, info := range infos {
		if err := tmpl.Execute(w, info); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}

func inspectAllInstanceNames(teamDir string, now time.Time) ([]string, error) {
	return inspectInstanceNamesWithOptions(teamDir, now, psOptions{})
}

func inspectInstanceNamesWithOptions(teamDir string, now time.Time, opts psOptions) ([]string, error) {
	rows, err := collectFilteredPsRows(teamDir, now, opts)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Instance)
	}
	return names, nil
}

func newRmCmd() *cobra.Command {
	var (
		target         string
		all            bool
		force          bool
		dryRun         bool
		commands       bool
		finished       bool
		latest         bool
		last           int
		staleOnly      bool
		runtimeStale   bool
		unhealthyOnly  bool
		agents         []string
		runtimeFilters []string
		statusFilters  []string
		phaseFilters   []string
		quiet          bool
		jsonOut        bool
		summary        bool
		format         string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "rm [<instance>...]",
		Short: "Remove instance state and daemon metadata.",
		Long: "Docker-like convenience alias for `agent-team instance rm`. When the daemon is running, also " +
			"removes daemon metadata; use --force to stop and remove a running instance.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if quiet && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --quiet or --summary.")
				return exitErr(2)
			}
			if format != "" && (quiet || jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format cannot be combined with --quiet, --json, or --summary.")
				return exitErr(2)
			}
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --commands cannot be combined with --summary.")
				return exitErr(2)
			}
			if commands && quiet {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --commands cannot be combined with --quiet.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			formatTemplate, err := parseRmFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			return runInstanceRmWithOptions(cmd, target, args, instanceRmOptions{
				All:            all,
				Force:          force,
				DryRun:         dryRun,
				Commands:       commands,
				Finished:       finished,
				Latest:         latest,
				Limit:          last,
				Stale:          staleOnly,
				RuntimeStale:   runtimeStale,
				Unhealthy:      unhealthyOnly,
				AgentFilters:   agents,
				RuntimeFilters: runtimeFilters,
				StatusFilters:  statusFilters,
				PhaseFilters:   phaseFilters,
				Quiet:          quiet,
				JSON:           jsonOut,
				Summary:        summary,
				Format:         formatTemplate,
				Command: scopedLifecycleCommandOptions(cmd, target, lifecycleCommandOptions{
					BaseArgs:       []string{"agent-team", "rm"},
					Names:          args,
					All:            all,
					Finished:       finished,
					Latest:         latest,
					Limit:          last,
					AgentFilters:   agents,
					RuntimeFilters: runtimeFilters,
					StatusFilters:  statusFilters,
					PhaseFilters:   phaseFilters,
					Stale:          staleOnly,
					RuntimeStale:   runtimeStale,
					Unhealthy:      unhealthyOnly,
					Force:          force,
				}),
			})
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Remove every daemon-known instance. Can combine with --agent, --runtime, --status, --phase, --stale, --runtime-stale, or --unhealthy.")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Skip confirmation; if the daemon is running, stop a running instance before removal.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview matching removals without deleting state or daemon metadata.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching remove command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&finished, "finished", false, "Remove every daemon-known exited or crashed instance.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Remove the most recently started daemon-known instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Remove the N most recently started daemon-known instances after other filters (0 = all).")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Remove only daemon-known instances whose non-idle work phase has stale status telemetry.")
	cmd.Flags().BoolVar(&runtimeStale, "runtime-stale", false, "Remove only daemon-known running instances whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Remove only daemon-known instances that are crashed, status-stale, or runtime-stale.")
	cmd.Flags().StringSliceVar(&agents, "agent", nil, "With --all, --finished, --latest, --last, --runtime, --status, --phase, --stale, --runtime-stale, or --unhealthy, only remove daemon-known instances for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Remove daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Remove daemon-known instances currently in this lifecycle status: stopped, exited, crashed, running, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Remove daemon-known instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output. Requires --force unless --dry-run is set.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON. Requires --force unless --dry-run is set.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate removal counts instead of per-instance rows.")
	cmd.Flags().StringVar(&format, "format", "", "Render each removal result with a Go template, e.g. '{{.Instance}} {{.Path}}'. Requires --force unless --dry-run is set.")
	return cmd
}

func newPruneCmd() *cobra.Command {
	var (
		target         string
		agents         []string
		runtimeFilters []string
		statusFilters  []string
		phaseFilters   []string
		staleOnly      bool
		runtimeStale   bool
		unhealthyOnly  bool
		dryRun         bool
		commands       bool
		olderThan      time.Duration
		quiet          bool
		jsonOut        bool
		summary        bool
		format         string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove finished daemon-managed instances.",
		Long: "Remove daemon-known exited or crashed instances and their state. " +
			"Running and stopped instances are intentionally left alone unless selected by --runtime-stale or --unhealthy.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if quiet && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --quiet or --summary.")
				return exitErr(2)
			}
			if format != "" && (quiet || jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format cannot be combined with --quiet, --json, or --summary.")
				return exitErr(2)
			}
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --commands cannot be combined with --summary.")
				return exitErr(2)
			}
			if commands && quiet {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --commands cannot be combined with --quiet.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if cmd.Flags().Changed("older-than") && olderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --older-than must be >= 0.")
				return exitErr(2)
			}
			if err := validatePruneStatusFilters(statusFilters); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			formatTemplate, err := parseRmFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			return runInstanceRmWithOptions(cmd, target, nil, instanceRmOptions{
				Force:          true,
				DryRun:         dryRun,
				Commands:       commands,
				Finished:       true,
				Stale:          staleOnly,
				RuntimeStale:   runtimeStale,
				Unhealthy:      unhealthyOnly,
				OlderThan:      olderThan,
				OlderThanSet:   cmd.Flags().Changed("older-than"),
				AgentFilters:   agents,
				RuntimeFilters: runtimeFilters,
				StatusFilters:  statusFilters,
				PhaseFilters:   phaseFilters,
				Quiet:          quiet,
				JSON:           jsonOut,
				Summary:        summary,
				Format:         formatTemplate,
				Command: scopedLifecycleCommandOptions(cmd, target, lifecycleCommandOptions{
					BaseArgs:       []string{"agent-team", "prune"},
					AgentFilters:   agents,
					RuntimeFilters: runtimeFilters,
					StatusFilters:  statusFilters,
					PhaseFilters:   phaseFilters,
					Stale:          staleOnly,
					RuntimeStale:   runtimeStale,
					Unhealthy:      unhealthyOnly,
					OlderThan:      olderThan,
					OlderThanSet:   cmd.Flags().Changed("older-than"),
				}),
			})
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringSliceVar(&agents, "agent", nil, "Only remove matching instances for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only remove matching instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only remove finished instances in this lifecycle status: exited or crashed. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only remove finished instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only remove finished instances whose non-idle work phase has stale status telemetry.")
	cmd.Flags().BoolVar(&runtimeStale, "runtime-stale", false, "Also remove daemon-known running instances whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only remove crashed finished instances, finished status-stale instances, or runtime-stale running instances.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview matching instances that would be pruned without deleting state or daemon metadata.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching prune apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "Only prune finished instances whose terminal timestamp is older than this duration (for example 24h).")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate removal counts instead of per-instance rows.")
	cmd.Flags().StringVar(&format, "format", "", "Render each removal result with a Go template, e.g. '{{.Instance}} {{.Path}}'.")
	return cmd
}

func validatePruneStatusFilters(statusFilters []string) error {
	statuses, err := lifecycleStatusFilterSet(statusFilters)
	if err != nil {
		return err
	}
	for status := range statuses {
		switch status {
		case string(daemon.StatusExited), string(daemon.StatusCrashed):
		default:
			return fmt.Errorf("--status for prune accepts only exited or crashed, got %q", status)
		}
	}
	return nil
}

func newWaitCmd() *cobra.Command {
	var (
		target         string
		all            bool
		latest         bool
		last           int
		agents         []string
		statusFilters  []string
		runtimeFilters []string
		phaseFilters   []string
		staleOnly      bool
		runtimeStale   bool
		unhealthyOnly  bool
		untilPhases    []string
		untilRaw       string
		timeout        time.Duration
		interval       time.Duration
		dryRun         bool
		commands       bool
		failOnCrash    bool
		jsonOut        bool
		quiet          bool
		summary        bool
		format         string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "wait [<instance>...]",
		Short: "Wait for daemon-managed instances to reach a lifecycle condition.",
		Long: "Wait until each selected daemon-managed instance reaches a lifecycle condition. " +
			"By default this waits for a terminal state (actually stopped, exited, crashed, or removed), matching Docker-style completion waits. " +
			"Use --until to wait for running, stopped, exited, crashed, removed, or terminal. " +
			"Use --until-phase to wait for a reported work phase such as idle, blocked, or done; when combined with --until, both conditions must match.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --all cannot be combined with instance names.")
				return exitErr(2)
			}
			if latest && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --latest cannot be combined with instance names.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --last must be >= 0.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --latest or --last.")
				return exitErr(2)
			}
			if last > 0 && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --last cannot be combined with instance names.")
				return exitErr(2)
			}
			if len(agents) > 0 && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --agent cannot be combined with instance names.")
				return exitErr(2)
			}
			if len(statusFilters) > 0 && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --status cannot be combined with instance names.")
				return exitErr(2)
			}
			if len(runtimeFilters) > 0 && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --runtime cannot be combined with instance names.")
				return exitErr(2)
			}
			if len(phaseFilters) > 0 && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --phase cannot be combined with instance names.")
				return exitErr(2)
			}
			if staleOnly && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --stale cannot be combined with instance names.")
				return exitErr(2)
			}
			if runtimeStale && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --runtime-stale cannot be combined with instance names.")
				return exitErr(2)
			}
			if unhealthyOnly && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --unhealthy cannot be combined with instance names.")
				return exitErr(2)
			}
			if !all && !latest && last == 0 && len(agents) == 0 && len(statusFilters) == 0 && len(runtimeFilters) == 0 && len(phaseFilters) == 0 && !staleOnly && !runtimeStale && !unhealthyOnly && len(args) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: instance is required unless --all, --latest, --last, --agent, --status, --runtime, --phase, --stale, --runtime-stale, or --unhealthy is set.")
				return exitErr(2)
			}
			if timeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --timeout must be >= 0.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --interval must be >= 0.")
				return exitErr(2)
			}
			if quiet && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --quiet or --json.")
				return exitErr(2)
			}
			if quiet && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --quiet or --summary.")
				return exitErr(2)
			}
			if err := validateWaitCommandsFlag(cmd, "agent-team wait", dryRun, commands, jsonOut, summary, quiet, format); err != nil {
				return err
			}
			if format != "" && (quiet || jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format cannot be combined with --quiet, --json, or --summary.")
				return exitErr(2)
			}
			formatTemplate, err := parseWaitFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			until, err := parseWaitUntil(untilRaw)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			if _, err := lifecycleStatusFilterSet(statusFilters); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			if _, err := lifecycleRuntimeFilterSet(runtimeFilters); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			if _, err := lifecyclePhaseFilterSet(phaseFilters); len(phaseFilters) > 0 && err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			untilPhaseSet := map[string]bool(nil)
			if len(untilPhases) > 0 {
				untilPhaseSet, err = lifecyclePhaseFilterSetForFlag("--until-phase", untilPhases)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
					return exitErr(2)
				}
				if !cmd.Flags().Changed("until") {
					until = waitUntilAny
				}
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			var phaseByInstance map[string]string
			var staleInstances map[string]bool
			if len(phaseFilters) > 0 || staleOnly || unhealthyOnly {
				now := time.Now()
				if len(phaseFilters) > 0 {
					phaseByInstance = waitPhaseByInstance(teamDir, now)
				}
				if staleOnly || unhealthyOnly {
					staleInstances = staleInstanceSet(teamDir, now)
				}
			}
			var lister instanceLister
			lister, err = newDaemonClient(teamDir)
			if err != nil {
				if errors.Is(err, errDaemonNotRunning) {
					lister = localInstanceLister{daemonRoot: daemon.DaemonRoot(teamDir)}
				} else {
					return err
				}
			}
			names := args
			if latest {
				names, err = waitLatestInstanceNamesWithPhasesStaleRuntimeAndUnhealthy(lister, agents, statusFilters, runtimeFilters, phaseFilters, phaseByInstance, staleInstances, staleOnly, runtimeStale, unhealthyOnly)
				if err != nil {
					return err
				}
			} else if last > 0 {
				names, err = waitLatestInstanceNamesLimitWithPhasesStaleRuntimeAndUnhealthy(lister, agents, statusFilters, runtimeFilters, phaseFilters, phaseByInstance, staleInstances, staleOnly, runtimeStale, unhealthyOnly, last)
				if err != nil {
					return err
				}
			} else if len(agents) > 0 || len(statusFilters) > 0 || len(runtimeFilters) > 0 || len(phaseFilters) > 0 || staleOnly || runtimeStale || unhealthyOnly {
				names, err = waitFilteredInstanceNamesWithPhasesStaleRuntimeAndUnhealthy(lister, agents, statusFilters, runtimeFilters, phaseFilters, phaseByInstance, staleInstances, staleOnly, runtimeStale, unhealthyOnly)
				if err != nil {
					return err
				}
			} else if all {
				names, err = waitAllInstanceNames(lister)
				if err != nil {
					return err
				}
			}
			if len(args) == 0 && len(names) == 0 {
				if commands {
					return nil
				}
				if summary {
					body := waitSummaryResult{Summary: summarizeWaitResults(nil, waitConditionString(until, untilPhaseSet))}
					if jsonOut {
						return json.NewEncoder(cmd.OutOrStdout()).Encode(body)
					}
					renderWaitSummary(cmd.OutOrStdout(), body.Summary)
					return nil
				}
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode([]waitResult{})
				}
				if !quiet && formatTemplate == nil {
					fmt.Fprintln(cmd.OutOrStdout(), "(no instances)")
				}
				return nil
			}
			var phaseSource waitPhaseSource
			if len(untilPhaseSet) > 0 || len(phaseFilters) > 0 || staleOnly || runtimeStale || unhealthyOnly || summary || dryRun {
				phaseSource = func() map[string]string {
					return waitPhaseByInstance(teamDir, time.Now())
				}
			}
			if dryRun {
				results, err := waitSnapshotForInstances(lister, phaseSource, names)
				if err != nil {
					var unknownErr *waitUnknownError
					if errors.As(err, &unknownErr) {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: instance %q is not known to the daemon.\n", unknownErr.Instance)
						return exitErr(2)
					}
					return err
				}
				if commands {
					return renderWaitCommands(cmd.OutOrStdout(), results, waitCommandOptions{
						BaseArgs:    []string{"agent-team", "wait"},
						Scope:       operatorCommandScopeFromCommand(cmd, target, "target"),
						Until:       until,
						UntilSet:    cmd.Flags().Changed("until"),
						UntilPhases: untilPhases,
						Timeout:     timeout,
						TimeoutSet:  cmd.Flags().Changed("timeout"),
						Interval:    interval,
						IntervalSet: cmd.Flags().Changed("interval"),
						FailOnCrash: failOnCrash,
					})
				}
				if err := renderWaitCommandResults(cmd, results, summary, jsonOut, quiet, formatTemplate, waitConditionString(until, untilPhaseSet), len(untilPhaseSet) > 0); err != nil {
					return err
				}
				if failOnCrash && waitResultsHaveStatus(results, string(daemon.StatusCrashed)) {
					return exitErr(1)
				}
				return nil
			}
			ctx := cmd.Context()
			cancel := func() {}
			if timeout > 0 {
				ctx, cancel = context.WithTimeout(ctx, timeout)
			}
			defer cancel()
			results, err := waitForInstancesUntilWithPhases(ctx, lister, phaseSource, names, interval, until, untilPhaseSet)
			if err != nil {
				var timeoutErr *waitTimeoutError
				if errors.As(err, &timeoutErr) {
					if !quiet {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: wait timed out waiting for %s: %s\n", waitConditionString(until, untilPhaseSet), strings.Join(timeoutErr.PendingNames(), ", "))
					}
					return exitErr(1)
				}
				var unknownErr *waitUnknownError
				if errors.As(err, &unknownErr) {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: instance %q is not known to the daemon.\n", unknownErr.Instance)
					return exitErr(2)
				}
				return err
			}
			if err := renderWaitCommandResults(cmd, results, summary, jsonOut, quiet, formatTemplate, waitConditionString(until, untilPhaseSet), len(untilPhaseSet) > 0); err != nil {
				return err
			}
			if failOnCrash && waitResultsHaveStatus(results, string(daemon.StatusCrashed)) {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Wait for every daemon-known instance.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Wait for the most recently started daemon-known instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Wait for the N most recently started daemon-known instances after other filters (0 = all).")
	cmd.Flags().StringSliceVar(&agents, "agent", nil, "Wait for every daemon-known instance for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Wait for daemon-known instances currently in this lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Wait for daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Wait for daemon-known instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Wait for daemon-known instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&runtimeStale, "runtime-stale", false, "Wait for daemon-known running instances whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Wait for daemon-known instances that are crashed, status-stale, or runtime-stale.")
	cmd.Flags().StringVar(&untilRaw, "until", string(waitUntilTerminal), "Lifecycle condition to wait for: terminal, running, stopped, exited, crashed, or removed.")
	cmd.Flags().StringSliceVar(&untilPhases, "until-phase", nil, "Work phase condition to wait for: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Maximum time to wait (0 = no timeout).")
	cmd.Flags().DurationVar(&interval, "interval", 500*time.Millisecond, "Polling interval.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview selected instances and current state without waiting.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching wait command for the selected instances. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&failOnCrash, "fail-on-crash", false, "Exit 1 if any selected instance resolves to crashed.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress output and use only the exit code.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate final status and phase counts instead of per-instance rows.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each wait result with a Go template, e.g. '{{.Instance}} {{.Status}} {{.Phase}}'.")
	return cmd
}

type instanceLister interface {
	Instances() ([]*daemon.Metadata, error)
}

type waitResult struct {
	Instance string `json:"instance"`
	Status   string `json:"status"`
	Phase    string `json:"phase,omitempty"`
	PID      int    `json:"pid,omitempty"`
}

type waitSummaryResult struct {
	Summary waitSummary `json:"summary"`
}

type waitSummary struct {
	Total     int            `json:"total"`
	Condition string         `json:"condition,omitempty"`
	Statuses  map[string]int `json:"statuses"`
	Phases    map[string]int `json:"phases"`
}

type waitCommandOptions struct {
	BaseArgs    []string
	Scope       operatorCommandScope
	Names       []string
	Until       waitUntil
	UntilSet    bool
	UntilPhases []string
	Timeout     time.Duration
	TimeoutSet  bool
	Interval    time.Duration
	IntervalSet bool
	FailOnCrash bool
}

func validateWaitCommandsFlag(cmd *cobra.Command, prefix string, dryRun, commands, jsonOut, summary, quiet bool, format string) error {
	if !commands {
		return nil
	}
	if !dryRun {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: --commands requires --dry-run.\n", prefix)
		return exitErr(2)
	}
	if jsonOut {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: --commands cannot be combined with --json.\n", prefix)
		return exitErr(2)
	}
	if summary {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: --commands cannot be combined with --summary.\n", prefix)
		return exitErr(2)
	}
	if quiet {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: --commands cannot be combined with --quiet.\n", prefix)
		return exitErr(2)
	}
	if format != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: --commands cannot be combined with --format.\n", prefix)
		return exitErr(2)
	}
	return nil
}

func parseWaitFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("wait-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderWaitFormat(w io.Writer, rows []waitResult, tmpl *template.Template) error {
	for _, row := range rows {
		if err := tmpl.Execute(w, row); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}

func summarizeWaitResults(rows []waitResult, condition string) waitSummary {
	summary := waitSummary{
		Total:     len(rows),
		Condition: condition,
		Statuses:  map[string]int{},
		Phases:    map[string]int{},
	}
	for _, row := range rows {
		status := strings.TrimSpace(row.Status)
		if status == "" {
			status = "unknown"
		}
		summary.Statuses[status]++
		phase := waitResultPhaseKey(row)
		if phase != "" {
			summary.Phases[phase]++
		}
	}
	return summary
}

func renderWaitSummary(w io.Writer, summary waitSummary) {
	fmt.Fprintf(w, "summary: total=%d", summary.Total)
	if summary.Condition != "" {
		fmt.Fprintf(w, " condition=%q", summary.Condition)
	}
	fmt.Fprintln(w)
	if len(summary.Statuses) > 0 {
		fmt.Fprint(w, "statuses:")
		for _, key := range sortedCountKeys(summary.Statuses) {
			fmt.Fprintf(w, " %s=%d", key, summary.Statuses[key])
		}
		fmt.Fprintln(w)
	}
	if len(summary.Phases) > 0 {
		fmt.Fprint(w, "phases:")
		for _, phase := range lifecyclePhaseSummaryOrder() {
			if summary.Phases[phase] > 0 {
				fmt.Fprintf(w, " %s=%d", phase, summary.Phases[phase])
			}
		}
		for _, phase := range sortedCountKeys(summary.Phases) {
			if !knownLifecyclePhase(phase) {
				fmt.Fprintf(w, " %s=%d", phase, summary.Phases[phase])
			}
		}
		fmt.Fprintln(w)
	}
}

func renderWaitCommandResults(cmd *cobra.Command, results []waitResult, summary, jsonOut, quiet bool, formatTemplate *template.Template, condition string, includePhase bool) error {
	out := cmd.OutOrStdout()
	if summary {
		body := waitSummaryResult{Summary: summarizeWaitResults(results, condition)}
		if jsonOut {
			return json.NewEncoder(out).Encode(body)
		}
		renderWaitSummary(out, body.Summary)
		return nil
	}
	if jsonOut {
		return json.NewEncoder(out).Encode(results)
	}
	if quiet {
		return nil
	}
	if formatTemplate != nil {
		return renderWaitFormat(out, results, formatTemplate)
	}
	for _, result := range results {
		if includePhase {
			fmt.Fprintf(out, "  wait   %-20s %s %s\n", result.Instance, result.Status, waitResultPhaseKey(result))
			continue
		}
		fmt.Fprintf(out, "  wait   %-20s %s\n", result.Instance, result.Status)
	}
	return nil
}

func renderWaitCommands(w io.Writer, results []waitResult, opts waitCommandOptions) error {
	names := waitResultNames(results)
	if len(names) == 0 {
		return nil
	}
	opts.Names = names
	_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(waitCommandArgs(opts)), " "))
	return err
}

func waitCommandArgs(opts waitCommandOptions) []string {
	args := append([]string{}, opts.BaseArgs...)
	if opts.Scope.Set && strings.TrimSpace(opts.Scope.Repo) != "" {
		args = append(args, "--repo", opts.Scope.Repo)
	}
	args = append(args, opts.Names...)
	if opts.UntilSet && opts.Until != waitUntilAny {
		args = append(args, "--until", string(opts.Until))
	}
	args = appendPlanCommandFilterArgs(args, "--until-phase", opts.UntilPhases)
	if opts.TimeoutSet {
		args = append(args, "--timeout", opts.Timeout.String())
	}
	if opts.IntervalSet {
		args = append(args, "--interval", opts.Interval.String())
	}
	if opts.FailOnCrash {
		args = append(args, "--fail-on-crash")
	}
	return args
}

func waitResultNames(results []waitResult) []string {
	names := make([]string, 0, len(results))
	seen := map[string]bool{}
	for _, result := range results {
		name := strings.TrimSpace(result.Instance)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

func waitResultsHaveStatus(rows []waitResult, status string) bool {
	for _, row := range rows {
		if row.Status == status {
			return true
		}
	}
	return false
}

func knownLifecyclePhase(phase string) bool {
	for _, known := range lifecyclePhaseSummaryOrder() {
		if phase == known {
			return true
		}
	}
	return false
}

type waitUntil string

const (
	waitUntilAny      waitUntil = "any"
	waitUntilTerminal waitUntil = "terminal"
	waitUntilRunning  waitUntil = "running"
	waitUntilStopped  waitUntil = "stopped"
	waitUntilExited   waitUntil = "exited"
	waitUntilCrashed  waitUntil = "crashed"
	waitUntilRemoved  waitUntil = "removed"
)

func parseWaitUntil(raw string) (waitUntil, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "", "terminal", "finished":
		return waitUntilTerminal, nil
	case "running":
		return waitUntilRunning, nil
	case "stopped":
		return waitUntilStopped, nil
	case "exited":
		return waitUntilExited, nil
	case "crashed":
		return waitUntilCrashed, nil
	case "removed":
		return waitUntilRemoved, nil
	default:
		return "", fmt.Errorf("unknown --until %q (want terminal, running, stopped, exited, crashed, or removed)", raw)
	}
}

type waitTimeoutError struct {
	Pending []waitResult
}

func (e *waitTimeoutError) Error() string {
	return "wait timed out"
}

func (e *waitTimeoutError) PendingNames() []string {
	names := make([]string, 0, len(e.Pending))
	for _, result := range e.Pending {
		names = append(names, result.Instance)
	}
	return names
}

type waitUnknownError struct {
	Instance string
}

func (e *waitUnknownError) Error() string {
	return fmt.Sprintf("unknown instance %q", e.Instance)
}

func waitAllInstanceNames(lister instanceLister) ([]string, error) {
	metas, err := lister.Instances()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(metas))
	for _, meta := range metas {
		names = append(names, meta.Instance)
	}
	sort.Strings(names)
	return names, nil
}

func waitAgentInstanceNames(lister instanceLister, filters []string) ([]string, error) {
	return waitFilteredInstanceNames(lister, filters, nil)
}

func waitFilteredInstanceNames(lister instanceLister, agentFilters, statusFilters []string) ([]string, error) {
	return waitFilteredInstanceNamesWithPhases(lister, agentFilters, statusFilters, nil, nil)
}

func waitFilteredInstanceNamesWithPhases(lister instanceLister, agentFilters, statusFilters, phaseFilters []string, phaseByInstance map[string]string) ([]string, error) {
	return waitFilteredInstanceNamesWithPhasesAndStale(lister, agentFilters, statusFilters, phaseFilters, phaseByInstance, nil)
}

func waitFilteredInstanceNamesWithPhasesAndStale(lister instanceLister, agentFilters, statusFilters, phaseFilters []string, phaseByInstance map[string]string, staleInstances map[string]bool) ([]string, error) {
	return waitFilteredInstanceNamesWithPhasesStaleAndUnhealthy(lister, agentFilters, statusFilters, phaseFilters, phaseByInstance, staleInstances, staleInstances != nil, false)
}

func waitFilteredInstanceNamesWithPhasesStaleAndUnhealthy(lister instanceLister, agentFilters, statusFilters, phaseFilters []string, phaseByInstance map[string]string, staleInstances map[string]bool, staleOnly bool, unhealthyOnly bool) ([]string, error) {
	return waitFilteredInstanceNamesWithPhasesStaleRuntimeAndUnhealthy(lister, agentFilters, statusFilters, nil, phaseFilters, phaseByInstance, staleInstances, staleOnly, false, unhealthyOnly)
}

func waitFilteredInstanceNamesWithPhasesStaleRuntimeAndUnhealthy(lister instanceLister, agentFilters, statusFilters, runtimeFilters, phaseFilters []string, phaseByInstance map[string]string, staleInstances map[string]bool, staleOnly bool, runtimeStaleOnly bool, unhealthyOnly bool) ([]string, error) {
	agents := lifecycleAgentFilterSet(agentFilters)
	if len(agentFilters) > 0 && len(agents) == 0 {
		return nil, errors.New("--agent requires at least one non-empty agent")
	}
	statuses, err := lifecycleStatusFilterSet(statusFilters)
	if err != nil {
		return nil, err
	}
	runtimes, err := lifecycleRuntimeFilterSet(runtimeFilters)
	if err != nil {
		return nil, err
	}
	var phases map[string]bool
	if len(phaseFilters) > 0 {
		phases, err = lifecyclePhaseFilterSet(phaseFilters)
		if err != nil {
			return nil, err
		}
	}
	metas, err := lister.Instances()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(metas))
	for _, meta := range metas {
		if !waitMetaMatchesFilters(meta, agents, statuses, runtimes, phases, phaseByInstance, staleInstances, staleOnly, runtimeStaleOnly, unhealthyOnly) {
			continue
		}
		names = append(names, meta.Instance)
	}
	sort.Strings(names)
	return names, nil
}

func waitLatestInstanceNames(lister instanceLister, agentFilters, statusFilters []string) ([]string, error) {
	return waitLatestInstanceNamesLimit(lister, agentFilters, statusFilters, 1)
}

func waitLatestInstanceNamesLimit(lister instanceLister, agentFilters, statusFilters []string, limit int) ([]string, error) {
	return waitLatestInstanceNamesLimitWithPhases(lister, agentFilters, statusFilters, nil, nil, limit)
}

func waitLatestInstanceNamesWithPhases(lister instanceLister, agentFilters, statusFilters, phaseFilters []string, phaseByInstance map[string]string) ([]string, error) {
	return waitLatestInstanceNamesLimitWithPhases(lister, agentFilters, statusFilters, phaseFilters, phaseByInstance, 1)
}

func waitLatestInstanceNamesLimitWithPhases(lister instanceLister, agentFilters, statusFilters, phaseFilters []string, phaseByInstance map[string]string, limit int) ([]string, error) {
	return waitLatestInstanceNamesLimitWithPhasesAndStale(lister, agentFilters, statusFilters, phaseFilters, phaseByInstance, nil, limit)
}

func waitLatestInstanceNamesWithPhasesAndStale(lister instanceLister, agentFilters, statusFilters, phaseFilters []string, phaseByInstance map[string]string, staleInstances map[string]bool) ([]string, error) {
	return waitLatestInstanceNamesLimitWithPhasesAndStale(lister, agentFilters, statusFilters, phaseFilters, phaseByInstance, staleInstances, 1)
}

func waitLatestInstanceNamesLimitWithPhasesAndStale(lister instanceLister, agentFilters, statusFilters, phaseFilters []string, phaseByInstance map[string]string, staleInstances map[string]bool, limit int) ([]string, error) {
	return waitLatestInstanceNamesLimitWithPhasesStaleAndUnhealthy(lister, agentFilters, statusFilters, phaseFilters, phaseByInstance, staleInstances, staleInstances != nil, false, limit)
}

func waitLatestInstanceNamesWithPhasesStaleAndUnhealthy(lister instanceLister, agentFilters, statusFilters, phaseFilters []string, phaseByInstance map[string]string, staleInstances map[string]bool, staleOnly bool, unhealthyOnly bool) ([]string, error) {
	return waitLatestInstanceNamesLimitWithPhasesStaleAndUnhealthy(lister, agentFilters, statusFilters, phaseFilters, phaseByInstance, staleInstances, staleOnly, unhealthyOnly, 1)
}

func waitLatestInstanceNamesLimitWithPhasesStaleAndUnhealthy(lister instanceLister, agentFilters, statusFilters, phaseFilters []string, phaseByInstance map[string]string, staleInstances map[string]bool, staleOnly bool, unhealthyOnly bool, limit int) ([]string, error) {
	return waitLatestInstanceNamesLimitWithPhasesStaleRuntimeAndUnhealthy(lister, agentFilters, statusFilters, nil, phaseFilters, phaseByInstance, staleInstances, staleOnly, false, unhealthyOnly, limit)
}

func waitLatestInstanceNamesWithPhasesStaleRuntimeAndUnhealthy(lister instanceLister, agentFilters, statusFilters, runtimeFilters, phaseFilters []string, phaseByInstance map[string]string, staleInstances map[string]bool, staleOnly bool, runtimeStaleOnly bool, unhealthyOnly bool) ([]string, error) {
	return waitLatestInstanceNamesLimitWithPhasesStaleRuntimeAndUnhealthy(lister, agentFilters, statusFilters, runtimeFilters, phaseFilters, phaseByInstance, staleInstances, staleOnly, runtimeStaleOnly, unhealthyOnly, 1)
}

func waitLatestInstanceNamesLimitWithPhasesStaleRuntimeAndUnhealthy(lister instanceLister, agentFilters, statusFilters, runtimeFilters, phaseFilters []string, phaseByInstance map[string]string, staleInstances map[string]bool, staleOnly bool, runtimeStaleOnly bool, unhealthyOnly bool, limit int) ([]string, error) {
	agents := lifecycleAgentFilterSet(agentFilters)
	if len(agentFilters) > 0 && len(agents) == 0 {
		return nil, errors.New("--agent requires at least one non-empty agent")
	}
	statuses, err := lifecycleStatusFilterSet(statusFilters)
	if err != nil {
		return nil, err
	}
	runtimes, err := lifecycleRuntimeFilterSet(runtimeFilters)
	if err != nil {
		return nil, err
	}
	var phases map[string]bool
	if len(phaseFilters) > 0 {
		phases, err = lifecyclePhaseFilterSet(phaseFilters)
		if err != nil {
			return nil, err
		}
	}
	metas, err := lister.Instances()
	if err != nil {
		return nil, err
	}
	filtered := make([]*daemon.Metadata, 0, len(metas))
	for _, meta := range metas {
		if !waitMetaMatchesFilters(meta, agents, statuses, runtimes, phases, phaseByInstance, staleInstances, staleOnly, runtimeStaleOnly, unhealthyOnly) {
			continue
		}
		filtered = append(filtered, meta)
	}
	filtered = latestMetadataByStartedLimit(filtered, limit)
	names := make([]string, 0, len(filtered))
	for _, meta := range filtered {
		names = append(names, meta.Instance)
	}
	return names, nil
}

func waitMetaMatchesFilters(meta *daemon.Metadata, agents, statuses, runtimes, phases map[string]bool, phaseByInstance map[string]string, staleInstances map[string]bool, staleOnly bool, runtimeStaleOnly bool, unhealthyOnly bool) bool {
	if len(agents) > 0 && !agents[meta.Agent] {
		return false
	}
	if len(statuses) > 0 && !statuses[metadataStatusKey(meta)] {
		return false
	}
	if len(runtimes) > 0 && !runtimes[metadataRuntimeKey(meta)] {
		return false
	}
	if len(phases) > 0 && !phases[waitPhaseForInstance(phaseByInstance, meta.Instance)] {
		return false
	}
	if staleOnly && !staleInstances[meta.Instance] {
		return false
	}
	if runtimeStaleOnly && !runtimeResumeMetadataIsStale(meta) {
		return false
	}
	if unhealthyOnly && metadataStatusKey(meta) != string(daemon.StatusCrashed) && !staleInstances[meta.Instance] && !runtimeResumeMetadataIsStale(meta) {
		return false
	}
	return true
}

func latestMetadataByStarted(metas []*daemon.Metadata) *daemon.Metadata {
	if len(metas) == 0 {
		return nil
	}
	return latestMetadataByStartedLimit(metas, 1)[0]
}

func latestMetadataByStartedLimit(metas []*daemon.Metadata, limit int) []*daemon.Metadata {
	if limit <= 0 || len(metas) == 0 {
		return nil
	}
	out := append([]*daemon.Metadata(nil), metas...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.StartedAt.Equal(b.StartedAt) {
			return psTimeAfter(a.StartedAt, b.StartedAt)
		}
		return a.Instance < b.Instance
	})
	if limit < len(out) {
		out = out[:limit]
	}
	return out
}

func metadataStatusKey(meta *daemon.Metadata) string {
	if meta == nil || meta.Status == "" {
		return "unknown"
	}
	return string(meta.Status)
}

func lifecycleRuntimeFilterSet(filters []string) (map[string]bool, error) {
	if len(filters) == 0 {
		return nil, nil
	}
	out := map[string]bool{}
	for _, raw := range splitFilterValues(filters) {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		kind, err := runtimebin.ParseKind(raw)
		if err != nil {
			return nil, fmt.Errorf("unknown --runtime %q (want claude or codex)", raw)
		}
		out[string(kind)] = true
	}
	if len(out) == 0 {
		return nil, errors.New("--runtime requires at least one non-empty runtime")
	}
	return out, nil
}

func metadataRuntimeKey(meta *daemon.Metadata) string {
	if meta == nil {
		return "unknown"
	}
	runtime := strings.ToLower(strings.TrimSpace(meta.Runtime))
	if runtime == "" {
		return "unknown"
	}
	return runtime
}

func waitForInstances(ctx context.Context, lister instanceLister, names []string, interval time.Duration) ([]waitResult, error) {
	return waitForInstancesUntil(ctx, lister, names, interval, waitUntilTerminal)
}

func waitForInstancesUntil(ctx context.Context, lister instanceLister, names []string, interval time.Duration, until waitUntil) ([]waitResult, error) {
	return waitForInstancesUntilWithPhases(ctx, lister, nil, names, interval, until, nil)
}

type waitPhaseSource func() map[string]string

func waitForInstancesUntilWithPhases(ctx context.Context, lister instanceLister, phaseSource waitPhaseSource, names []string, interval time.Duration, until waitUntil, untilPhases map[string]bool) ([]waitResult, error) {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	pending := map[string]bool{}
	order := make([]string, 0, len(names))
	for _, name := range names {
		if pending[name] {
			continue
		}
		pending[name] = true
		order = append(order, name)
	}
	results := make([]waitResult, 0, len(order))
	lastSeen := map[string]waitResult{}
	seenAny := map[string]bool{}

	for {
		list, err := lister.Instances()
		if err != nil {
			return nil, err
		}
		var phaseByInstance map[string]string
		if phaseSource != nil {
			phaseByInstance = phaseSource()
		}
		byName := map[string]*daemon.Metadata{}
		for _, meta := range list {
			byName[meta.Instance] = meta
		}
		for _, name := range order {
			if !pending[name] {
				continue
			}
			meta, ok := byName[name]
			if !ok {
				if seenAny[name] {
					result := waitResult{Instance: name, Status: "removed"}
					if previous, ok := lastSeen[name]; ok {
						result.Phase = previous.Phase
					}
					lastSeen[name] = result
					if waitUntilSatisfied(until, result, nil, untilPhases) {
						results = append(results, result)
						delete(pending, name)
					}
					continue
				}
				return nil, &waitUnknownError{Instance: name}
			}
			seenAny[name] = true
			result := waitResultFromMeta(meta)
			if phaseSource != nil {
				result.Phase = waitPhaseForInstance(phaseByInstance, name)
			}
			lastSeen[name] = result
			if waitUntilSatisfied(until, result, meta, untilPhases) {
				results = append(results, result)
				delete(pending, name)
			}
		}
		if len(pending) == 0 {
			return results, nil
		}
		select {
		case <-ctx.Done():
			stillPending := make([]waitResult, 0, len(pending))
			for _, name := range order {
				if !pending[name] {
					continue
				}
				result, ok := lastSeen[name]
				if !ok {
					result = waitResult{Instance: name, Status: "unknown"}
					if phaseSource != nil {
						result.Phase = waitPhaseForInstance(phaseByInstance, name)
					}
				}
				stillPending = append(stillPending, result)
			}
			return results, &waitTimeoutError{Pending: stillPending}
		case <-time.After(interval):
		}
	}
}

func waitSnapshotForInstances(lister instanceLister, phaseSource waitPhaseSource, names []string) ([]waitResult, error) {
	seen := map[string]bool{}
	order := make([]string, 0, len(names))
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		order = append(order, name)
	}
	list, err := lister.Instances()
	if err != nil {
		return nil, err
	}
	byName := map[string]*daemon.Metadata{}
	for _, meta := range list {
		byName[meta.Instance] = meta
	}
	var phaseByInstance map[string]string
	if phaseSource != nil {
		phaseByInstance = phaseSource()
	}
	results := make([]waitResult, 0, len(order))
	for _, name := range order {
		meta, ok := byName[name]
		if !ok {
			return nil, &waitUnknownError{Instance: name}
		}
		result := waitResultFromMeta(meta)
		if phaseSource != nil {
			result.Phase = waitPhaseForInstance(phaseByInstance, name)
		}
		results = append(results, result)
	}
	return results, nil
}

func waitResultFromMeta(meta *daemon.Metadata) waitResult {
	result := waitResult{Instance: meta.Instance, Status: string(meta.Status), PID: meta.PID}
	if result.Status == "" {
		result.Status = "unknown"
	}
	return result
}

func waitPhaseByInstance(teamDir string, now time.Time) map[string]string {
	return statusPhaseByInstance(teamDir, now)
}

func waitPhaseForInstance(phaseByInstance map[string]string, instance string) string {
	if phaseByInstance == nil {
		return ""
	}
	return waitPhaseKey(phaseByInstance[instance])
}

func waitPhaseKey(raw string) string {
	return psPhaseKey(instanceRow{Phase: raw})
}

func waitResultPhaseKey(result waitResult) string {
	return waitPhaseKey(result.Phase)
}

func waitUntilSatisfied(until waitUntil, result waitResult, meta *daemon.Metadata, untilPhases map[string]bool) bool {
	if !waitLifecycleUntilSatisfied(until, result, meta) {
		return false
	}
	if len(untilPhases) == 0 {
		return true
	}
	return untilPhases[waitResultPhaseKey(result)]
}

func waitLifecycleUntilSatisfied(until waitUntil, result waitResult, meta *daemon.Metadata) bool {
	switch until {
	case waitUntilAny:
		return true
	case waitUntilTerminal:
		if result.Status == "removed" {
			return true
		}
		return waitTerminalMeta(meta)
	case waitUntilRunning:
		return meta != nil && meta.Status == daemon.StatusRunning
	case waitUntilStopped:
		return meta != nil && meta.Status == daemon.StatusStopped
	case waitUntilExited:
		return meta != nil && meta.Status == daemon.StatusExited
	case waitUntilCrashed:
		return meta != nil && meta.Status == daemon.StatusCrashed
	case waitUntilRemoved:
		return result.Status == "removed"
	default:
		return false
	}
}

func waitConditionString(until waitUntil, phases map[string]bool) string {
	lifecycle := string(until)
	if until == waitUntilAny {
		lifecycle = ""
	}
	if len(phases) == 0 {
		if lifecycle == "" {
			return "any"
		}
		return lifecycle
	}
	phaseNames := make([]string, 0, len(phases))
	for phase := range phases {
		phaseNames = append(phaseNames, phase)
	}
	sort.Strings(phaseNames)
	phaseCondition := "phase " + strings.Join(phaseNames, "|")
	if lifecycle == "" {
		return phaseCondition
	}
	return lifecycle + " and " + phaseCondition
}

func waitTerminalStatus(status daemon.Status) bool {
	switch status {
	case daemon.StatusExited, daemon.StatusCrashed:
		return true
	default:
		return false
	}
}

func waitTerminalMeta(meta *daemon.Metadata) bool {
	if meta == nil {
		return false
	}
	if waitTerminalStatus(meta.Status) {
		return true
	}
	if meta.Status != daemon.StatusStopped {
		return false
	}
	if !meta.ExitedAt.IsZero() {
		return true
	}
	return !daemon.PidLiveCheck(meta.PID)
}

type instanceRestartOptions struct {
	All            bool
	Latest         bool
	Limit          int
	AgentFilters   []string
	RuntimeFilters []string
	StatusFilters  []string
	PhaseFilters   []string
	Stale          bool
	RuntimeStale   bool
	Unhealthy      bool
	Timeout        time.Duration
	Wait           bool
	WaitTimeout    time.Duration
	Force          bool
	DryRun         bool
	Summary        bool
	Attach         bool
	AttachTail     int
	AttachTailSet  bool
	Quiet          bool
	JSON           bool
	Format         *template.Template
	Commands       bool
	Command        lifecycleCommandOptions
}

func runInstanceRestart(cmd *cobra.Command, target, prompt string, names []string, opts ...instanceRestartOptions) error {
	cfg := instanceRestartOptions{}
	if len(opts) > 0 {
		cfg = opts[0]
	}
	if cfg.All && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --all cannot be combined with instance names.")
		return exitErr(2)
	}
	if cfg.Latest && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --latest cannot be combined with instance names.")
		return exitErr(2)
	}
	if cfg.Limit < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --last must be >= 0.")
		return exitErr(2)
	}
	if cfg.Latest && cfg.Limit > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --latest or --last.")
		return exitErr(2)
	}
	if cfg.Limit > 0 && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --last cannot be combined with instance names.")
		return exitErr(2)
	}
	if len(cfg.AgentFilters) > 0 && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --agent cannot be combined with instance names.")
		return exitErr(2)
	}
	if len(cfg.RuntimeFilters) > 0 && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --runtime cannot be combined with instance names.")
		return exitErr(2)
	}
	if len(cfg.StatusFilters) > 0 && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --status cannot be combined with instance names.")
		return exitErr(2)
	}
	if len(cfg.PhaseFilters) > 0 && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --phase cannot be combined with instance names.")
		return exitErr(2)
	}
	if cfg.Stale && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --stale cannot be combined with instance names.")
		return exitErr(2)
	}
	if cfg.RuntimeStale && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --runtime-stale cannot be combined with instance names.")
		return exitErr(2)
	}
	if cfg.Unhealthy && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --unhealthy cannot be combined with instance names.")
		return exitErr(2)
	}
	if cfg.Timeout < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --timeout must be >= 0.")
		return exitErr(2)
	}
	if cfg.WaitTimeout < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --wait-timeout must be >= 0.")
		return exitErr(2)
	}
	if cfg.DryRun && cfg.Wait {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --dry-run cannot be combined with --wait.")
		return exitErr(2)
	}
	if cfg.Commands && !cfg.DryRun {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --commands requires --dry-run.")
		return exitErr(2)
	}
	if cfg.Commands && cfg.JSON {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --commands cannot be combined with --json.")
		return exitErr(2)
	}
	if cfg.Commands && cfg.Summary {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --commands cannot be combined with --summary.")
		return exitErr(2)
	}
	if cfg.Commands && cfg.Quiet {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --commands cannot be combined with --quiet.")
		return exitErr(2)
	}
	if cfg.Commands && cfg.Format != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --commands cannot be combined with --format.")
		return exitErr(2)
	}
	if cfg.Commands && cfg.Attach {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --commands cannot be combined with --attach.")
		return exitErr(2)
	}
	if cfg.Attach && cfg.JSON {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --attach cannot be combined with --json.")
		return exitErr(2)
	}
	if cfg.Quiet && cfg.JSON {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --quiet or --json.")
		return exitErr(2)
	}
	if cfg.Quiet && cfg.Summary {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --quiet or --summary.")
		return exitErr(2)
	}
	if cfg.Format != nil && cfg.JSON {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format cannot be combined with --json.")
		return exitErr(2)
	}
	if cfg.Format != nil && cfg.Quiet {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format cannot be combined with --quiet.")
		return exitErr(2)
	}
	if cfg.Format != nil && cfg.Summary {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format cannot be combined with --summary.")
		return exitErr(2)
	}
	if cfg.Quiet && cfg.Attach {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --quiet cannot be combined with --attach.")
		return exitErr(2)
	}
	if cfg.Summary && cfg.Attach {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --summary cannot be combined with --attach.")
		return exitErr(2)
	}
	if cfg.Format != nil && cfg.Attach {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format cannot be combined with --attach.")
		return exitErr(2)
	}
	if cfg.Attach && cfg.DryRun {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --attach cannot be combined with --dry-run.")
		return exitErr(2)
	}
	if cfg.Attach && cfg.Wait {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --attach or --wait.")
		return exitErr(2)
	}
	if !cfg.Attach && cfg.AttachTailSet {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --tail requires --attach.")
		return exitErr(2)
	}
	runtimes, err := lifecycleRuntimeFilterSet(cfg.RuntimeFilters)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(2)
	}
	statuses, err := lifecycleStatusFilterSet(cfg.StatusFilters)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(2)
	}
	phases, err := lifecyclePhaseFilterSet(cfg.PhaseFilters)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(2)
	}
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	var phaseByInstance map[string]string
	var staleInstances map[string]bool
	if len(phases) > 0 || cfg.Stale || cfg.Unhealthy {
		now := time.Now()
		if len(phases) > 0 {
			phaseByInstance = waitPhaseByInstance(teamDir, now)
		}
		if cfg.Stale || cfg.Unhealthy {
			staleInstances = staleInstanceSet(teamDir, now)
		}
	}
	topo, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(1)
	}
	if topo == nil && len(names) == 0 && !cfg.All && !cfg.Latest && cfg.Limit == 0 && len(cfg.AgentFilters) == 0 && len(runtimes) == 0 && len(statuses) == 0 && len(phases) == 0 && !cfg.Stale && !cfg.RuntimeStale && !cfg.Unhealthy {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: no instances.toml — nothing to restart.")
		return exitErr(2)
	}
	dc, err := newDaemonClient(teamDir)
	if err != nil && !(cfg.DryRun && errors.Is(err, errDaemonNotRunning)) {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: daemon is not running.")
		return exitErr(2)
	}
	var metas []*daemon.Metadata
	if dc != nil {
		metas, err = dc.Instances()
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
			return exitErr(1)
		}
	} else if cfg.DryRun {
		metas, err = daemon.ListMetadata(daemon.DaemonRoot(teamDir))
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
			return exitErr(1)
		}
	}
	var targets []lifecycleTarget
	if len(cfg.AgentFilters) > 0 {
		targets, err = selectAgentLifecycleTargets(topo, metas, cfg.AgentFilters)
	} else if cfg.All || cfg.Latest || cfg.Limit > 0 || len(runtimes) > 0 || len(statuses) > 0 || len(phases) > 0 || cfg.Stale || cfg.RuntimeStale || cfg.Unhealthy {
		targets, err = selectAllLifecycleTargets(topo, metas)
	} else {
		targets, err = selectLifecycleTargets(topo, metas, names)
	}
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(2)
	}
	targets = filterLifecycleTargetsByRuntime(targets, runtimes)
	targets = filterLifecycleTargetsByStatus(targets, statuses)
	targets = filterLifecycleTargetsByPhase(targets, phases, phaseByInstance)
	targets = filterLifecycleTargetsByStale(targets, cfg.Stale, staleInstances)
	targets = filterLifecycleTargetsByRuntimeStale(targets, cfg.RuntimeStale)
	targets = filterLifecycleTargetsByUnhealthy(targets, cfg.Unhealthy, staleInstances)
	if cfg.Latest {
		targets = latestLifecycleTargetsLimit(targets, 1)
	} else if cfg.Limit > 0 {
		targets = latestLifecycleTargetsLimit(targets, cfg.Limit)
	}
	if cfg.Attach && len(targets) != 1 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --attach requires exactly one selected instance.")
		return exitErr(2)
	}
	waitHealth := healthOptions{}
	if cfg.Wait && lifecycleSelectionScoped(names, cfg.AgentFilters, cfg.RuntimeFilters, cfg.StatusFilters, cfg.PhaseFilters, cfg.Latest, cfg.Limit, cfg.Stale, cfg.RuntimeStale, cfg.Unhealthy) {
		waitHealth = lifecycleWaitHealthOptionsForTargets(targets)
	}
	if len(targets) == 0 {
		if cfg.Commands {
			return nil
		}
		if cfg.JSON {
			if cfg.Summary {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(lifecycleActionSummaryResult{
					Summary: summarizeLifecycleActions(nil, cfg.DryRun),
				})
			}
			if cfg.Wait {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(lifecycleHealthResult{Actions: []lifecycleActionResult{}})
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode([]lifecycleActionResult{})
		}
		if cfg.Quiet || cfg.Format != nil {
			return nil
		}
		if cfg.Summary {
			renderLifecycleActionSummary(cmd.OutOrStdout(), summarizeLifecycleActions(nil, cfg.DryRun))
			return nil
		}
		fmt.Fprintln(cmd.OutOrStdout(), "(no instances)")
		return nil
	}
	out := cmd.OutOrStdout()
	results := make([]lifecycleActionResult, 0, len(targets))
	for _, lt := range targets {
		if cfg.DryRun {
			result := dryRunRestartResult(lt)
			results = append(results, result)
			if !cfg.JSON && !cfg.Quiet && cfg.Format == nil && !cfg.Summary && !cfg.Commands {
				renderLifecycleDryRun(out, result)
			}
			continue
		}
		if lt.meta != nil {
			if !lifecycleMetadataCanManagedResume(lt.meta) {
				result := lifecycleTargetUnsupportedResumeResult(lt)
				results = append(results, result)
				if !cfg.JSON && !cfg.Quiet && cfg.Format == nil && !cfg.Summary {
					fmt.Fprintf(out, "  %-7s %-20s %s\n", result.Action, lt.name, result.Detail)
				}
				continue
			}
			if err := dc.RestartInstanceWithOptions(lt.name, cfg.Force, cfg.Timeout); err != nil {
				results = append(results, lifecycleActionResult{Action: "error", Instance: lt.name, Agent: lt.agent, Status: "error", Error: err.Error()})
				if !cfg.JSON && !cfg.Quiet && cfg.Format == nil && !cfg.Summary {
					fmt.Fprintf(out, "  error   %-20s %v\n", lt.name, err)
				}
				continue
			}
			results = append(results, lifecycleActionResult{Action: "restart", Instance: lt.name, Agent: lt.agent})
			if !cfg.JSON && !cfg.Quiet && cfg.Format == nil && !cfg.Summary {
				fmt.Fprintf(out, "  restart %-20s %s\n", lt.name, lt.agent)
			}
			continue
		}
		kickoff := prompt
		if kickoff == "" {
			kickoff = fmt.Sprintf("Topology bring-up: you are %q, an instance of %q.", lt.name, lt.agent)
		}
		runErr := runMaybeSuppressStdout(cmd, cfg.JSON || cfg.Quiet || cfg.Format != nil || cfg.Summary, func() error {
			return upOne(cmd, target, lt.declared, kickoff)
		})
		if runErr != nil {
			results = append(results, lifecycleActionResult{Action: "error", Instance: lt.name, Agent: lt.agent, Status: "error", Error: runErr.Error()})
			if !cfg.JSON && !cfg.Quiet && cfg.Format == nil && !cfg.Summary {
				fmt.Fprintf(out, "  error   %-20s %v\n", lt.name, runErr)
			}
			continue
		}
		results = append(results, lifecycleActionResult{Action: "start", Instance: lt.name, Agent: lt.agent})
		if !cfg.JSON && !cfg.Quiet && cfg.Format == nil && !cfg.Summary {
			fmt.Fprintf(out, "  start   %-20s %s\n", lt.name, lt.agent)
		}
	}
	if cfg.DryRun {
		if cfg.Commands {
			return renderLifecycleActionCommands(out, results, cfg.Command)
		}
		if cfg.JSON {
			if cfg.Summary {
				return json.NewEncoder(out).Encode(lifecycleActionSummaryResult{
					Summary: summarizeLifecycleActions(results, true),
				})
			}
			return json.NewEncoder(out).Encode(results)
		}
		if cfg.Format != nil {
			return renderLifecycleActionFormat(out, results, cfg.Format)
		}
		if cfg.Summary {
			renderLifecycleActionSummary(out, summarizeLifecycleActions(results, true))
		}
		return nil
	}
	enriched := enrichLifecycleResults(dc, results)
	var health *healthResult
	healthWaitTimedOut := false
	if cfg.Wait {
		ctx := cmd.Context()
		cancel := func() {}
		if cfg.WaitTimeout > 0 {
			ctx, cancel = context.WithTimeout(ctx, cfg.WaitTimeout)
		}
		defer cancel()
		health, healthWaitTimedOut, err = runHealthWaitWithOutcome(ctx, teamDir, 500*time.Millisecond, time.Now, waitHealth)
		if err != nil {
			return err
		}
	}
	if cfg.Attach {
		if lifecycleActionResultsHaveErrors(enriched) {
			return exitErr(1)
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		fmt.Fprintf(out, "\nattaching to %s (Ctrl-C to detach)\n", targets[0].name)
		return followLifecycleLog(ctx, out, dc, targets[0].name, cfg.AttachTail)
	}
	if cfg.JSON {
		if cfg.Summary {
			body := lifecycleActionSummaryResult{Summary: summarizeLifecycleActions(enriched, false)}
			if cfg.Wait {
				body.Health = health
			}
			if err := json.NewEncoder(out).Encode(body); err != nil {
				return err
			}
			if lifecycleActionResultsHaveErrors(enriched) {
				return exitErr(1)
			}
			if cfg.Wait && health != nil && !health.Healthy {
				reportLifecycleHealthWaitTimeout(cmd, cfg.Quiet, healthWaitTimedOut, health)
				return exitErr(1)
			}
			return nil
		}
		if cfg.Wait {
			if err := json.NewEncoder(out).Encode(lifecycleHealthResult{Actions: enriched, Health: health}); err != nil {
				return err
			}
			if lifecycleActionResultsHaveErrors(enriched) {
				return exitErr(1)
			}
			if health != nil && !health.Healthy {
				reportLifecycleHealthWaitTimeout(cmd, cfg.Quiet, healthWaitTimedOut, health)
				return exitErr(1)
			}
			return nil
		}
		if err := json.NewEncoder(out).Encode(enriched); err != nil {
			return err
		}
		if lifecycleActionResultsHaveErrors(enriched) {
			return exitErr(1)
		}
		if cfg.Wait && health != nil && !health.Healthy {
			reportLifecycleHealthWaitTimeout(cmd, cfg.Quiet, healthWaitTimedOut, health)
			return exitErr(1)
		}
		return nil
	}
	if cfg.Format != nil {
		if err := renderLifecycleActionFormat(out, enriched, cfg.Format); err != nil {
			return err
		}
		if lifecycleActionResultsHaveErrors(enriched) {
			return exitErr(1)
		}
		return nil
	}
	if cfg.Summary {
		renderLifecycleActionSummary(out, summarizeLifecycleActions(enriched, false))
		if cfg.Wait && !cfg.Quiet {
			fmt.Fprintln(out)
			renderHealth(out, health)
		}
		if lifecycleActionResultsHaveErrors(enriched) {
			return exitErr(1)
		}
		if cfg.Wait && health != nil && !health.Healthy {
			reportLifecycleHealthWaitTimeout(cmd, cfg.Quiet, healthWaitTimedOut, health)
			return exitErr(1)
		}
		return nil
	}
	if cfg.Wait {
		if !cfg.Quiet {
			fmt.Fprintln(out)
			renderHealth(out, health)
		}
		if lifecycleActionResultsHaveErrors(enriched) {
			return exitErr(1)
		}
		if health != nil && !health.Healthy {
			reportLifecycleHealthWaitTimeout(cmd, cfg.Quiet, healthWaitTimedOut, health)
			return exitErr(1)
		}
	}
	if lifecycleActionResultsHaveErrors(enriched) {
		return exitErr(1)
	}
	return nil
}

func ensureDaemonReady(cmd *cobra.Command, target string, quiet bool) error {
	return ensureDaemonReadyWithTimeout(cmd, target, quiet, defaultDaemonReadyTimeout)
}

func ensureDaemonReadyWithTimeout(cmd *cobra.Command, target string, quiet bool, readyTimeout time.Duration) error {
	if readyTimeout < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon start: --ready-timeout must be >= 0.")
		return exitErr(2)
	}
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	if _, err := newDaemonClient(teamDir); err == nil {
		return nil
	} else if !errors.Is(err, errDaemonNotRunning) {
		return err
	}

	start := func() error {
		return runDaemonStartWithJSON(cmd, target, true, readyTimeout, "", false, false)
	}
	if quiet {
		start = func() error {
			return runDaemonStartWithJSON(cmd, target, true, readyTimeout, "", false, true)
		}
	}
	return start()
}
