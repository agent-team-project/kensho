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
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newTeamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "team",
		Short: "Inspect declared agent teams.",
		Long:  "Inspect team declarations loaded from .agent_team/instances.toml.",
	}
	cmd.AddCommand(newTeamLsCmd())
	cmd.AddCommand(newTeamShowCmd())
	cmd.AddCommand(newTeamUpCmd())
	cmd.AddCommand(newTeamDownCmd())
	cmd.AddCommand(newTeamRestartCmd())
	cmd.AddCommand(newTeamPlanCmd())
	cmd.AddCommand(newTeamPsCmd())
	cmd.AddCommand(newTeamJobsCmd())
	cmd.AddCommand(newTeamQueueCmd())
	cmd.AddCommand(newTeamLogsCmd())
	cmd.AddCommand(newTeamPipelinesCmd())
	cmd.AddCommand(newTeamSchedulesCmd())
	cmd.AddCommand(newTeamHealthCmd())
	cmd.AddCommand(newTeamStatusCmd())
	return cmd
}

func newTeamLsCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List declared teams.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			teams, err := loadTeamInfos(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team ls: %v\n", err)
				return exitErr(1)
			}
			return renderTeamList(cmd.OutOrStdout(), teams, jsonOut)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit teams as JSON.")
	return cmd
}

func newTeamShowCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <team>",
		Short: "Show one declared team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			info, err := loadTeamInfo(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team show: %v\n", err)
				return exitErr(1)
			}
			return renderTeamDetail(cmd.OutOrStdout(), info, jsonOut)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the team as JSON.")
	return cmd
}

func newTeamPsCmd() *cobra.Command {
	var (
		repo     string
		watch    bool
		noClear  bool
		interval time.Duration
		jsonOut  bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "ps <team>",
		Aliases: []string{"instances"},
		Short:   "List instances owned by one team.",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team ps: --interval must be >= 0.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				clear := !noClear && !jsonOut
				return runTeamPsWatch(ctx, cmd.OutOrStdout(), teamDir, args[0], interval, jsonOut, clear)
			}
			rows, err := collectTeamPsRows(teamDir, args[0], time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team ps: %v\n", err)
				return exitErr(1)
			}
			return renderTeamPs(cmd.OutOrStdout(), rows, jsonOut)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh team instances until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team instances as JSON.")
	return cmd
}

func newTeamJobsCmd() *cobra.Command {
	var (
		repo    string
		status  string
		sortBy  string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "jobs <team>",
		Short: "List jobs owned by one team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team jobs: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team jobs: %v\n", err)
				return exitErr(2)
			}
			sortMode, err := parseJobSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team jobs: %v\n", err)
				return exitErr(2)
			}
			var statusFilter job.Status
			if strings.TrimSpace(status) != "" {
				statusFilter, err = job.ParseStatus(status)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team jobs: %v\n", err)
					return exitErr(2)
				}
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			jobs, err := collectTeamJobs(teamDir, args[0], statusFilter, sortMode)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team jobs: %v\n", err)
				return exitErr(1)
			}
			return renderTeamJobs(cmd.OutOrStdout(), jobs, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&status, "status", "", "Filter by job status: queued, running, blocked, done, or failed.")
	cmd.Flags().StringVar(&sortBy, "sort", "id", "Sort jobs by id, status, target, ticket, created, updated, instance, branch, or pr.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team jobs as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func newTeamQueueCmd() *cobra.Command {
	var (
		repo        string
		stateFilter string
		eventTypes  []string
		readyOnly   bool
		watch       bool
		noClear     bool
		summary     bool
		jsonOut     bool
		format      string
		interval    time.Duration
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "queue <team>",
		Short: "List queue items scoped to one team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if format != "" && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue: --format cannot be combined with --summary.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue: --interval must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parseQueueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue: %v\n", err)
				return exitErr(2)
			}
			filters, err := parseQueueListFilters(stateFilter, nil, eventTypes, nil, readyOnly, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				if summary {
					return runTeamQueueSummaryWatch(ctx, cmd.OutOrStdout(), teamDir, args[0], filters, jsonOut, interval, !noClear && !jsonOut)
				}
				return runTeamQueueListWatch(ctx, cmd.OutOrStdout(), teamDir, args[0], filters, jsonOut, tmpl, interval, !noClear && !jsonOut)
			}
			if summary {
				return runTeamQueueSummary(cmd.OutOrStdout(), teamDir, args[0], filters, jsonOut)
			}
			return runTeamQueueList(cmd.OutOrStdout(), teamDir, args[0], filters, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "Filter by queue state: pending or dead.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "Filter by event type; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "Only show pending queue items whose next retry is due now.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the team queue table until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate queue counts instead of queue rows.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team queue rows as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each queue item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	return cmd
}

func newTeamLogsCmd() *cobra.Command {
	var (
		repo      string
		follow    bool
		latest    bool
		last      int
		list      bool
		jsonOut   bool
		noPrefix  bool
		statuses  []string
		phases    []string
		staleOnly bool
		unhealthy bool
		tail      string
		since     string
		grep      string
		format    string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "logs <team>",
		Short: "Show daemon-captured logs for one team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: choose one of --latest or --last.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --last must be >= 0.")
				return exitErr(2)
			}
			if jsonOut && !list {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --json requires --list.")
				return exitErr(2)
			}
			if format != "" && !list {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --format requires --list.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if list && (follow || cmd.Flags().Changed("tail")) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --list cannot be combined with --follow or --tail.")
				return exitErr(2)
			}
			formatTemplate, err := parseLogListFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team logs: %v\n", err)
				return exitErr(2)
			}
			tailLines, err := parseLogTail(tail)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team logs: %v\n", err)
				return exitErr(2)
			}
			sinceCutoff, err := parseLogSince(since, time.Now)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team logs: %v\n", err)
				return exitErr(2)
			}
			grepPattern, err := parseLogGrep(grep)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team logs: %v\n", err)
				return exitErr(2)
			}
			if sinceCutoff != nil && follow {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --since cannot be combined with --follow because captured logs are not timestamped.")
				return exitErr(2)
			}
			if grepPattern != nil && follow {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --grep cannot be combined with --follow.")
				return exitErr(2)
			}
			if grepPattern != nil && list {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --grep cannot be combined with --list.")
				return exitErr(2)
			}
			listOpts, err := newLogListOptionsWithUnhealthy(statuses, nil, phases, staleOnly, unhealthy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team logs: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			opts := logsOptions{
				Follow:    follow,
				Latest:    latest,
				Limit:     last,
				List:      list,
				JSON:      jsonOut,
				NoPrefix:  noPrefix,
				Tail:      tailLines,
				TailSet:   cmd.Flags().Changed("tail"),
				Since:     sinceCutoff,
				Grep:      grepPattern,
				Format:    formatTemplate,
				Unhealthy: unhealthy,
			}
			return runTeamLogs(cmd, teamDir, args[0], opts, listOpts)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Tail selected team logs.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Show the most recently started team instance log.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Show logs for the N most recently started team instances (0 = all).")
	cmd.Flags().BoolVar(&list, "list", false, "List team log streams instead of printing log content.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON with --list.")
	cmd.Flags().BoolVar(&noPrefix, "no-prefix", false, "Do not prefix lines when streaming multiple team logs.")
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "Only show logs for lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phases, "phase", nil, "Only show logs for work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only show logs for team instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&unhealthy, "unhealthy", false, "Only show logs for crashed or stale team instances.")
	cmd.Flags().StringVar(&tail, "tail", "0", "Show only the last N lines before returning or following (0 or all = all).")
	cmd.Flags().StringVar(&since, "since", "", "Only include log streams modified since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.")
	cmd.Flags().StringVar(&grep, "grep", "", "Only print log lines matching this regular expression. One-shot reads only.")
	cmd.Flags().StringVar(&format, "format", "", "With --list, render each log stream with a Go template, e.g. '{{.Instance}} {{.LogPath}}'.")
	return cmd
}

func newTeamPipelinesCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "pipelines <team>",
		Short: "List pipeline status for one team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team pipelines: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineStatusFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team pipelines: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			rows, err := collectTeamPipelineStatus(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team pipelines: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineStatusRows(cmd.OutOrStdout(), rows, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team pipeline status as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each pipeline with a Go template, e.g. '{{.Pipeline}} {{.ReadySteps}}'.")
	return cmd
}

func newTeamSchedulesCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "schedules <team>",
		Short: "List schedules owned by one team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team schedules: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseScheduleDueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team schedules: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			schedules, err := collectTeamSchedules(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team schedules: %v\n", err)
				return exitErr(1)
			}
			return renderTeamSchedules(cmd.OutOrStdout(), schedules, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team schedules as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each schedule with a Go template, e.g. '{{.Name}} {{.Every}}'.")
	return cmd
}

func newTeamUpCmd() *cobra.Command {
	var (
		repo         string
		prompt       string
		wait         bool
		timeout      time.Duration
		readyTimeout time.Duration
		dryRun       bool
		summary      bool
		attach       bool
		tail         string
		quiet        bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "up <team>",
		Aliases: []string{"start"},
		Short:   "Start or resume a team's declared persistent instances.",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tailLines, formatTemplate, err := validateTeamUpOptions(cmd, "agent-team team up", teamLifecycleUpOptions{
				Wait:          wait,
				Timeout:       timeout,
				ReadyTimeout:  readyTimeout,
				DryRun:        dryRun,
				Summary:       summary,
				Attach:        attach,
				AttachTailSet: cmd.Flags().Changed("tail"),
				Tail:          tail,
				Quiet:         quiet,
				JSON:          jsonOut,
				Format:        format,
			})
			if err != nil {
				return err
			}
			_, names, err := loadTeamPersistentLifecycleInstances(cmd, repo, args[0])
			if err != nil {
				return reportTeamLifecycleLoadError(cmd, "agent-team team up", err)
			}
			if len(names) == 0 {
				return writeEmptyTeamLifecycleStart(cmd, args[0], "up", dryRun, wait, summary, quiet, jsonOut, formatTemplate)
			}
			if !dryRun {
				if err := ensureDaemonReadyWithTimeout(cmd, repo, jsonOut || quiet, readyTimeout); err != nil {
					return err
				}
			}
			return runInstanceUpWithOptions(cmd, repo, prompt, names, instanceUpOptions{
				Wait:          wait,
				Timeout:       timeout,
				DryRun:        dryRun,
				Summary:       summary,
				Attach:        attach,
				AttachTail:    tailLines,
				AttachTailSet: cmd.Flags().Changed("tail"),
				Quiet:         quiet,
				JSON:          jsonOut,
				Format:        formatTemplate,
				Health:        teamLifecycleHealthOptions(names),
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&prompt, "prompt", "", "Override the default kickoff prompt.")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for selected instances to become healthy after starting.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&readyTimeout, "ready-timeout", defaultDaemonReadyTimeout, "Maximum time to wait for implicit daemon readiness (0 = no timeout).")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview planned start/resume actions without changing daemon state.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate action counts instead of per-instance rows.")
	cmd.Flags().BoolVar(&attach, "attach", false, "Follow the selected instance log after starting or resuming. Requires exactly one selected instance.")
	cmd.Flags().StringVar(&tail, "tail", "50", "With --attach, show only the last N lines before following (0 or all = all).")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return cmd
}

func newTeamDownCmd() *cobra.Command {
	var (
		repo        string
		force       bool
		wait        bool
		timeout     time.Duration
		waitTimeout time.Duration
		dryRun      bool
		remove      bool
		summary     bool
		quiet       bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "down <team>",
		Aliases: []string{"stop"},
		Short:   "Stop a team's persistent instances and active ephemeral children.",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			formatTemplate, err := validateTeamDownOptions(cmd, "agent-team team down", teamLifecycleDownOptions{
				Wait:        wait,
				Timeout:     timeout,
				WaitTimeout: waitTimeout,
				DryRun:      dryRun,
				Summary:     summary,
				Quiet:       quiet,
				JSON:        jsonOut,
				Format:      format,
			})
			if err != nil {
				return err
			}
			_, names, err := loadTeamStopLifecycleInstances(cmd, repo, args[0])
			if err != nil {
				return reportTeamLifecycleLoadError(cmd, "agent-team team down", err)
			}
			if len(names) == 0 {
				return writeEmptyTeamLifecycleDown(cmd, args[0], "stop", dryRun, summary, quiet, jsonOut, formatTemplate)
			}
			return runInstanceDownWithOptions(cmd, repo, names, instanceDownOptions{
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
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Escalate to SIGKILL if an instance does not stop within --timeout.")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for stopped instances to reach a terminal state.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Grace before --force kills. With --wait and no --wait-timeout, also used as the wait deadline (0 = no wait deadline; force defaults to 10s).")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait for terminal state with --wait. Defaults to --timeout when unset; set 0 explicitly for no wait timeout.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview planned stop actions without changing daemon state.")
	cmd.Flags().BoolVar(&remove, "rm", false, "Remove selected instance state and daemon metadata after stopping.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate action counts instead of per-instance rows.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return cmd
}

func newTeamRestartCmd() *cobra.Command {
	var (
		repo         string
		prompt       string
		timeout      time.Duration
		readyTimeout time.Duration
		wait         bool
		waitTimeout  time.Duration
		force        bool
		dryRun       bool
		summary      bool
		attach       bool
		tail         string
		quiet        bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "restart <team>",
		Short: "Restart or resume a team's declared persistent instances.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tailLines, formatTemplate, err := validateTeamRestartOptions(cmd, "agent-team team restart", teamLifecycleRestartOptions{
				Timeout:       timeout,
				ReadyTimeout:  readyTimeout,
				Wait:          wait,
				WaitTimeout:   waitTimeout,
				DryRun:        dryRun,
				Summary:       summary,
				Attach:        attach,
				AttachTailSet: cmd.Flags().Changed("tail"),
				Tail:          tail,
				Quiet:         quiet,
				JSON:          jsonOut,
				Format:        format,
			})
			if err != nil {
				return err
			}
			_, names, err := loadTeamPersistentLifecycleInstances(cmd, repo, args[0])
			if err != nil {
				return reportTeamLifecycleLoadError(cmd, "agent-team team restart", err)
			}
			if len(names) == 0 {
				return writeEmptyTeamLifecycleStart(cmd, args[0], "restart", dryRun, wait, summary, quiet, jsonOut, formatTemplate)
			}
			if !dryRun {
				if err := ensureDaemonReadyWithTimeout(cmd, repo, jsonOut || quiet, readyTimeout); err != nil {
					return err
				}
			}
			return runInstanceRestart(cmd, repo, prompt, names, instanceRestartOptions{
				Timeout:       timeout,
				Wait:          wait,
				WaitTimeout:   waitTimeout,
				Force:         force,
				DryRun:        dryRun,
				Summary:       summary,
				Attach:        attach,
				AttachTail:    tailLines,
				AttachTailSet: cmd.Flags().Changed("tail"),
				Quiet:         quiet,
				JSON:          jsonOut,
				Format:        formatTemplate,
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&prompt, "prompt", "", "Override the default kickoff prompt for instances started fresh.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Maximum time to wait for each running instance to stop before resuming (0 = daemon default).")
	cmd.Flags().DurationVar(&readyTimeout, "ready-timeout", defaultDaemonReadyTimeout, "Maximum time to wait for implicit daemon readiness (0 = no timeout).")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for selected instances to become healthy after restarting.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait for health with --wait (0 = no timeout).")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Escalate to SIGKILL if a running instance does not stop within --timeout before restarting.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview planned restart/resume actions without changing daemon state.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate action counts instead of per-instance rows.")
	cmd.Flags().BoolVar(&attach, "attach", false, "Follow the selected instance log after restarting or resuming. Requires exactly one selected instance.")
	cmd.Flags().StringVar(&tail, "tail", "50", "With --attach, show only the last N lines before following (0 or all = all).")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return cmd
}

func newTeamPlanCmd() *cobra.Command {
	var (
		repo          string
		jsonOut       bool
		summary       bool
		stopExtras    bool
		actionFilters []string
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "plan <team>",
		Short: "Preview desired lifecycle state for one team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && (jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team plan: --format cannot be combined with --json or --summary.")
				return exitErr(2)
			}
			formatTemplate, err := parsePlanFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team plan: %v\n", err)
				return exitErr(2)
			}
			actions, err := planActionFilterSet(actionFilters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team plan: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			snapshot, err := collectTeamPlan(teamDir, args[0], stopExtras, actions)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team plan: %v\n", err)
				return exitErr(1)
			}
			if jsonOut {
				if summary {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(lifecycleActionSummaryResult{
						Summary: summarizeLifecycleActions(planRowsToLifecycleActionResults(snapshot.Plan.Instances, true), true),
					})
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(snapshot)
			}
			if formatTemplate != nil {
				return renderPlanFormat(cmd.OutOrStdout(), snapshot.Plan.Instances, formatTemplate)
			}
			if summary {
				renderLifecycleActionSummary(cmd.OutOrStdout(), summarizeLifecycleActions(planRowsToLifecycleActionResults(snapshot.Plan.Instances, true), true))
				return nil
			}
			renderTeamPlan(cmd.OutOrStdout(), snapshot)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team plan as JSON.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate action counts instead of per-instance rows.")
	cmd.Flags().BoolVar(&stopExtras, "stop-extras", false, "Preview running team-agent topology extras as stop actions.")
	cmd.Flags().StringSliceVar(&actionFilters, "action", nil, "Only show plan rows with this action: start, resume, keep, on-demand, stop, or extra. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&format, "format", "", "Render each plan row with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return cmd
}

func newTeamHealthCmd() *cobra.Command {
	var (
		repo        string
		includeJobs bool
		quiet       bool
		jsonOut     bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "health <team>",
		Short: "Check health for one declared team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if quiet && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team health: choose one of --quiet or --json.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			snapshot, err := collectTeamHealth(teamDir, args[0], time.Now().UTC(), includeJobs)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team health: %v\n", err)
				return exitErr(1)
			}
			if !quiet {
				if err := renderTeamHealth(cmd.OutOrStdout(), snapshot, jsonOut); err != nil {
					return err
				}
			}
			if snapshot.Health != nil && !snapshot.Health.Healthy {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&includeJobs, "jobs", false, "Include team-owned job and pipeline health.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team health as JSON.")
	return cmd
}

func newTeamStatusCmd() *cobra.Command {
	var (
		repo     string
		watch    bool
		noClear  bool
		interval time.Duration
		jsonOut  bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "status <team>",
		Short: "Summarize one team's instances, jobs, and pipelines.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team status: --interval must be >= 0.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				clear := !noClear && !jsonOut
				return runTeamStatusWatch(ctx, cmd.OutOrStdout(), teamDir, args[0], interval, jsonOut, clear)
			}
			snapshot, err := collectTeamStatus(teamDir, args[0], time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team status: %v\n", err)
				return exitErr(1)
			}
			return renderTeamStatus(cmd.OutOrStdout(), snapshot, jsonOut)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh team status until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team status as JSON.")
	return cmd
}

type teamInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Instances   []string `json:"instances,omitempty"`
	Pipelines   []string `json:"pipelines,omitempty"`
	Schedules   []string `json:"schedules,omitempty"`
}

type teamStatusSnapshot struct {
	Team            teamInfo            `json:"team"`
	CheckedAt       string              `json:"checked_at"`
	InstanceSummary psSummaryJSON       `json:"instance_summary"`
	Instances       []psJSONRow         `json:"instances,omitempty"`
	JobSummary      jobSummary          `json:"job_summary"`
	PipelineStatus  []pipelineStatusRow `json:"pipeline_status,omitempty"`
	Schedules       []scheduleInfo      `json:"schedules,omitempty"`
	Actions         []string            `json:"actions,omitempty"`
}

type teamPlanSnapshot struct {
	Team teamInfo    `json:"team"`
	Plan *planResult `json:"plan"`
}

type teamHealthSnapshot struct {
	Team   teamInfo      `json:"team"`
	Health *healthResult `json:"health"`
}

func loadTeamInfos(teamDir string) ([]teamInfo, error) {
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return nil, err
	}
	if top == nil {
		return nil, nil
	}
	infos := make([]teamInfo, 0, len(top.Teams))
	for _, team := range top.SortedTeams() {
		infos = append(infos, teamInfoFromTopology(team))
	}
	return infos, nil
}

func loadTeamInfo(teamDir, name string) (teamInfo, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return teamInfo{}, err
	}
	if top == nil {
		return teamInfo{}, fmt.Errorf("team %q not found", strings.TrimSpace(name))
	}
	return teamInfoFromTopology(team), nil
}

func loadTopologyTeam(teamDir, name string) (*topology.Topology, *topology.Team, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil, fmt.Errorf("team name is required")
	}
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return nil, nil, err
	}
	if top == nil || top.Teams[name] == nil {
		return top, nil, fmt.Errorf("team %q not found", name)
	}
	return top, top.Teams[name], nil
}

func teamInfoFromTopology(team *topology.Team) teamInfo {
	if team == nil {
		return teamInfo{}
	}
	return teamInfo{
		Name:        team.Name,
		Description: team.Description,
		Instances:   append([]string(nil), team.Instances...),
		Pipelines:   append([]string(nil), team.Pipelines...),
		Schedules:   append([]string(nil), team.Schedules...),
	}
}

func collectTeamStatus(teamDir, name string, now time.Time) (*teamStatusSnapshot, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	rows, err := collectPsRows(teamDir, now)
	if err != nil {
		return nil, err
	}
	instanceRows := teamInstanceRows(top, team, rows)
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	pipelineStatus, err := collectPipelineStatusRows(teamDir, "")
	if err != nil {
		return nil, err
	}
	schedules, err := loadScheduleInfos(teamDir)
	if err != nil {
		return nil, err
	}
	snapshot := &teamStatusSnapshot{
		Team:            teamInfoFromTopology(team),
		CheckedAt:       now.UTC().Format(time.RFC3339),
		InstanceSummary: psSummaryRows(instanceRows),
		Instances:       psJSONRows(instanceRows),
		JobSummary:      summarizeJobs(teamJobs(top, team, jobs)),
		PipelineStatus:  teamPipelineStatus(team, pipelineStatus),
		Schedules:       teamSchedules(team, schedules),
	}
	snapshot.Actions = teamStatusActions(top, team, snapshot)
	return snapshot, nil
}

func collectTeamPlan(teamDir, name string, stopExtras bool, actions map[string]bool) (*teamPlanSnapshot, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	result, err := collectPlan(teamDir)
	if err != nil {
		return nil, err
	}
	if stopExtras {
		markPlanStopExtras(result)
	}
	result.Instances = teamPlanRows(top, team, result.Instances, stopExtras)
	result.Instances = filterPlanRowsWithActions(result.Instances, psOptions{}, actions)
	result.Summary = summarizePlanRows(result.Instances)
	return &teamPlanSnapshot{
		Team: teamInfoFromTopology(team),
		Plan: result,
	}, nil
}

func collectTeamHealth(teamDir, name string, now time.Time, includeJobs bool) (*teamHealthSnapshot, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	rows, err := collectPsRows(teamDir, now)
	if err != nil {
		return nil, err
	}
	healthRows := teamRuntimeRows(top, team, rows)
	scoped := teamScopedTopology(top, team)
	result := buildHealthWithDaemonStatus(collectDaemonStatus(teamDir), healthRows, scoped, now, healthOptions{})
	if includeJobs {
		if err := addTeamJobHealth(result, teamDir, top, team, now); err != nil {
			return nil, err
		}
	}
	return &teamHealthSnapshot{Team: teamInfoFromTopology(team), Health: result}, nil
}

func collectTeamPsRows(teamDir, name string, now time.Time) ([]instanceRow, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	rows, err := collectPsRows(teamDir, now)
	if err != nil {
		return nil, err
	}
	return teamInstanceRows(top, team, rows), nil
}

type teamLifecycleUpOptions struct {
	Wait          bool
	Timeout       time.Duration
	ReadyTimeout  time.Duration
	DryRun        bool
	Summary       bool
	Attach        bool
	AttachTailSet bool
	Tail          string
	Quiet         bool
	JSON          bool
	Format        string
}

type teamLifecycleDownOptions struct {
	Wait        bool
	Timeout     time.Duration
	WaitTimeout time.Duration
	DryRun      bool
	Summary     bool
	Quiet       bool
	JSON        bool
	Format      string
}

type teamLifecycleRestartOptions struct {
	Timeout       time.Duration
	ReadyTimeout  time.Duration
	Wait          bool
	WaitTimeout   time.Duration
	DryRun        bool
	Summary       bool
	Attach        bool
	AttachTailSet bool
	Tail          string
	Quiet         bool
	JSON          bool
	Format        string
}

func loadTeamPersistentLifecycleInstances(cmd *cobra.Command, repo, name string) (string, []string, error) {
	teamDir, err := resolveTeamDir(cmd, repo)
	if err != nil {
		return "", nil, err
	}
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return "", nil, err
	}
	return teamDir, teamPersistentLifecycleInstanceNames(top, team), nil
}

func loadTeamStopLifecycleInstances(cmd *cobra.Command, repo, name string) (string, []string, error) {
	teamDir, err := resolveTeamDir(cmd, repo)
	if err != nil {
		return "", nil, err
	}
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return "", nil, err
	}
	names := teamPersistentLifecycleInstanceNames(top, team)
	metas, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
	if err != nil {
		return "", nil, err
	}
	names = append(names, teamEphemeralChildLifecycleInstanceNames(top, team, metas)...)
	return teamDir, names, nil
}

func teamPersistentLifecycleInstanceNames(top *topology.Topology, team *topology.Team) []string {
	if top == nil || team == nil {
		return nil
	}
	seen := map[string]bool{}
	names := make([]string, 0, len(team.Instances))
	for _, name := range team.Instances {
		if seen[name] {
			continue
		}
		inst := top.Instances[name]
		if inst == nil || inst.Ephemeral {
			continue
		}
		names = append(names, name)
		seen[name] = true
	}
	return names
}

func teamEphemeralChildLifecycleInstanceNames(top *topology.Topology, team *topology.Team, metas []*daemon.Metadata) []string {
	if top == nil || team == nil {
		return nil
	}
	owners := map[string]bool{}
	for _, name := range team.Instances {
		if inst := top.Instances[name]; inst != nil && inst.Ephemeral {
			owners[inst.Name] = true
		}
	}
	if len(owners) == 0 {
		return nil
	}
	var names []string
	seen := map[string]bool{}
	for _, meta := range metas {
		if meta == nil || seen[meta.Instance] {
			continue
		}
		owner, ok := declaredEphemeralOwner(top, meta.Instance, meta.Agent)
		if !ok || !owners[owner.Name] {
			continue
		}
		names = append(names, meta.Instance)
		seen[meta.Instance] = true
	}
	sort.Strings(names)
	return names
}

func reportTeamLifecycleLoadError(cmd *cobra.Command, prefix string, err error) error {
	var code ExitCode
	if errors.As(err, &code) {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", prefix, err)
	return exitErr(1)
}

func validateTeamUpOptions(cmd *cobra.Command, prefix string, opts teamLifecycleUpOptions) (int, *template.Template, error) {
	if opts.Timeout < 0 {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--timeout must be >= 0")
	}
	if opts.ReadyTimeout < 0 {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--ready-timeout must be >= 0")
	}
	if opts.DryRun && opts.Wait {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--dry-run cannot be combined with --wait")
	}
	if opts.Attach && opts.JSON {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--attach cannot be combined with --json")
	}
	if opts.Quiet && opts.JSON {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "choose one of --quiet or --json")
	}
	if opts.Quiet && opts.Summary {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "choose one of --quiet or --summary")
	}
	if opts.Summary && opts.Attach {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--summary cannot be combined with --attach")
	}
	if opts.Format != "" && (opts.Quiet || opts.JSON || opts.Attach || opts.Summary) {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--format cannot be combined with --quiet, --json, --attach, or --summary")
	}
	if opts.Quiet && opts.Attach {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--quiet cannot be combined with --attach")
	}
	if opts.Attach && opts.DryRun {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--attach cannot be combined with --dry-run")
	}
	if opts.Attach && opts.Wait {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "choose one of --attach or --wait")
	}
	if !opts.Attach && opts.AttachTailSet {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--tail requires --attach")
	}
	tailLines, err := parseLogTail(opts.Tail)
	if err != nil {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, err.Error())
	}
	formatTemplate, err := parseLifecycleActionFormat(opts.Format)
	if err != nil {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, err.Error())
	}
	return tailLines, formatTemplate, nil
}

func validateTeamDownOptions(cmd *cobra.Command, prefix string, opts teamLifecycleDownOptions) (*template.Template, error) {
	if opts.Timeout < 0 {
		return nil, teamLifecycleUsageError(cmd, prefix, "--timeout must be >= 0")
	}
	if opts.WaitTimeout < 0 {
		return nil, teamLifecycleUsageError(cmd, prefix, "--wait-timeout must be >= 0")
	}
	if opts.DryRun && opts.Wait {
		return nil, teamLifecycleUsageError(cmd, prefix, "--dry-run cannot be combined with --wait")
	}
	if opts.Quiet && opts.JSON {
		return nil, teamLifecycleUsageError(cmd, prefix, "choose one of --quiet or --json")
	}
	if opts.Quiet && opts.Summary {
		return nil, teamLifecycleUsageError(cmd, prefix, "choose one of --quiet or --summary")
	}
	if opts.Format != "" && (opts.Quiet || opts.JSON || opts.Summary) {
		return nil, teamLifecycleUsageError(cmd, prefix, "--format cannot be combined with --quiet, --json, or --summary")
	}
	formatTemplate, err := parseLifecycleActionFormat(opts.Format)
	if err != nil {
		return nil, teamLifecycleUsageError(cmd, prefix, err.Error())
	}
	return formatTemplate, nil
}

func validateTeamRestartOptions(cmd *cobra.Command, prefix string, opts teamLifecycleRestartOptions) (int, *template.Template, error) {
	if opts.Timeout < 0 {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--timeout must be >= 0")
	}
	if opts.ReadyTimeout < 0 {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--ready-timeout must be >= 0")
	}
	if opts.WaitTimeout < 0 {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--wait-timeout must be >= 0")
	}
	if opts.DryRun && opts.Wait {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--dry-run cannot be combined with --wait")
	}
	if opts.Attach && opts.JSON {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--attach cannot be combined with --json")
	}
	if opts.Quiet && opts.JSON {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "choose one of --quiet or --json")
	}
	if opts.Quiet && opts.Summary {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "choose one of --quiet or --summary")
	}
	if opts.Summary && opts.Attach {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--summary cannot be combined with --attach")
	}
	if opts.Format != "" && (opts.Quiet || opts.JSON || opts.Attach || opts.Summary) {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--format cannot be combined with --quiet, --json, --attach, or --summary")
	}
	if opts.Quiet && opts.Attach {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--quiet cannot be combined with --attach")
	}
	if opts.Attach && opts.DryRun {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--attach cannot be combined with --dry-run")
	}
	if opts.Attach && opts.Wait {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "choose one of --attach or --wait")
	}
	if !opts.Attach && opts.AttachTailSet {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--tail requires --attach")
	}
	tailLines, err := parseLogTail(opts.Tail)
	if err != nil {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, err.Error())
	}
	formatTemplate, err := parseLifecycleActionFormat(opts.Format)
	if err != nil {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, err.Error())
	}
	return tailLines, formatTemplate, nil
}

func teamLifecycleUsageError(cmd *cobra.Command, prefix, message string) error {
	fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s.\n", prefix, strings.TrimSuffix(message, "."))
	return exitErr(2)
}

func teamLifecycleHealthOptions(names []string) healthOptions {
	instances := map[string]bool{}
	for _, name := range names {
		if strings.TrimSpace(name) != "" {
			instances[name] = true
		}
	}
	if len(instances) == 0 {
		return healthOptions{}
	}
	return healthOptions{filters: psOptions{instances: instances}}
}

func writeEmptyTeamLifecycleStart(cmd *cobra.Command, teamName, verb string, dryRun, wait, summary, quiet, jsonOut bool, formatTemplate *template.Template) error {
	out := cmd.OutOrStdout()
	if jsonOut {
		if summary {
			return json.NewEncoder(out).Encode(lifecycleActionSummaryResult{
				Summary: summarizeLifecycleActions(nil, dryRun),
			})
		}
		if wait {
			return json.NewEncoder(out).Encode(lifecycleHealthResult{Actions: []lifecycleActionResult{}})
		}
		return json.NewEncoder(out).Encode([]lifecycleActionResult{})
	}
	if quiet || formatTemplate != nil {
		return nil
	}
	if summary {
		renderLifecycleActionSummary(out, summarizeLifecycleActions(nil, dryRun))
		return nil
	}
	fmt.Fprintf(out, "(no persistent instances to %s for team %q)\n", verb, strings.TrimSpace(teamName))
	return nil
}

func writeEmptyTeamLifecycleDown(cmd *cobra.Command, teamName, verb string, dryRun, summary, quiet, jsonOut bool, formatTemplate *template.Template) error {
	out := cmd.OutOrStdout()
	if jsonOut {
		if summary {
			return json.NewEncoder(out).Encode(lifecycleActionSummaryResult{
				Summary: summarizeInstanceDownActions(nil, dryRun),
			})
		}
		return json.NewEncoder(out).Encode([]instanceDownResult{})
	}
	if quiet || formatTemplate != nil {
		return nil
	}
	if summary {
		renderLifecycleActionSummary(out, summarizeInstanceDownActions(nil, dryRun))
		return nil
	}
	fmt.Fprintf(out, "(nothing to %s for team %q)\n", verb, strings.TrimSpace(teamName))
	return nil
}

func addTeamJobHealth(result *healthResult, teamDir string, top *topology.Topology, team *topology.Team, now time.Time) error {
	if result == nil {
		return nil
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return err
	}
	ownedJobs := teamJobs(top, team, jobs)
	ownedIDs := jobIDSet(ownedJobs)
	queueItems, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return err
	}
	teamQueue := teamQueueItems(top, team, ownedJobs, queueItems)
	result.Queue = summarizeQueueItems(teamQueue, now.UTC())
	if result.Queue.Dead > 0 {
		result.addIssueWithSeverityAndActions(
			"queue_dead_letter",
			"error",
			"",
			"",
			"",
			"",
			fmt.Sprintf("team %q queue has %d dead-letter item(s)", team.Name, result.Queue.Dead),
			teamQueueActions(ownedJobs, teamQueue),
		)
	}
	triage, err := collectJobTriage(teamDir, now.UTC(), defaultJobTriageStaleAfter)
	if err != nil {
		return err
	}
	triage.Summary = summarizeJobs(ownedJobs)
	triage.Queue = result.Queue
	triage.Attention = filterJobTriageItemsByJobIDs(triage.Attention, ownedIDs)
	triage.ReadySteps = filterJobReadyRowsByJobIDs(triage.ReadySteps, ownedIDs)
	triage.StatusPreviews = filterJobStatusPreviewsByJobIDs(triage.StatusPreviews, ownedIDs)
	result.Jobs = &triage
	result.JobStatus = triage.StatusPreviews
	for _, item := range triage.Attention {
		result.addJobIssue(item)
	}
	for _, preview := range triage.StatusPreviews {
		if !preview.Changed || preview.After != job.StatusBlocked {
			continue
		}
		message := fmt.Sprintf("job %q status file reports blocked", preview.JobID)
		if strings.TrimSpace(preview.Message) != "" {
			message += ": " + strings.TrimSpace(preview.Message)
		}
		result.addIssueWithSeverityAndActions("job_status_blocked", "error", preview.Instance, preview.JobID, string(preview.After), preview.Phase, message, []string{
			fmt.Sprintf("agent-team job unblock %s <answer...>", preview.JobID),
		})
	}
	pipelineStatus, err := collectTeamPipelineStatus(teamDir, team.Name)
	if err != nil {
		return err
	}
	result.PipelineStatus = pipelineStatus
	for _, row := range pipelineStatus {
		if row.FailedSteps > 0 {
			result.addIssueWithSeverityAndActions(
				"pipeline_failed_step",
				"error",
				"",
				"",
				"",
				"",
				fmt.Sprintf("pipeline %q has %d failed step(s)", row.Pipeline, row.FailedSteps),
				row.Actions,
			)
		}
		if row.BlockedSteps > 0 {
			result.addIssueWithSeverityAndActions(
				"pipeline_blocked_step",
				"warning",
				"",
				"",
				"",
				"",
				fmt.Sprintf("pipeline %q has %d blocked step(s)", row.Pipeline, row.BlockedSteps),
				row.Actions,
			)
		}
	}
	return nil
}

func collectTeamJobs(teamDir, name string, status job.Status, sortMode string) ([]*job.Job, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	owned := teamJobs(top, team, jobs)
	if status != "" {
		filtered := owned[:0]
		for _, j := range owned {
			if j.Status == status {
				filtered = append(filtered, j)
			}
		}
		owned = filtered
	}
	sortJobs(owned, sortMode)
	return owned, nil
}

func collectTeamQueueItems(teamDir, name string, filters queueListFilters, now time.Time) ([]*daemon.QueueItem, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	items, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	owned := teamQueueItems(top, team, teamJobs(top, team, jobs), items)
	return filterQueueItems(owned, filters.withNow(now)), nil
}

func runTeamQueueList(w io.Writer, teamDir, name string, filters queueListFilters, jsonOut bool, tmpl *template.Template) error {
	items, err := collectTeamQueueItems(teamDir, name, filters, time.Now().UTC())
	if err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(w).Encode(items)
	}
	if tmpl != nil {
		return renderQueueItemsFormat(w, items, tmpl)
	}
	renderQueueTable(w, items)
	return nil
}

func runTeamQueueSummary(w io.Writer, teamDir, name string, filters queueListFilters, jsonOut bool) error {
	now := time.Now().UTC()
	items, err := collectTeamQueueItems(teamDir, name, filters, now)
	if err != nil {
		return err
	}
	summary := summarizeQueueItems(items, now)
	if jsonOut {
		return json.NewEncoder(w).Encode(summary)
	}
	renderQueueSummary(w, summary)
	return nil
}

func runTeamQueueListWatch(ctx context.Context, w io.Writer, teamDir, name string, filters queueListFilters, jsonOut bool, tmpl *template.Template, interval time.Duration, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if !jsonOut {
			if err := writeWatchClear(w, clear); err != nil {
				return err
			}
		}
		if err := runTeamQueueList(w, teamDir, name, filters, jsonOut, tmpl); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if !jsonOut && !clear {
				fmt.Fprintln(w)
			}
		}
	}
}

func runTeamQueueSummaryWatch(ctx context.Context, w io.Writer, teamDir, name string, filters queueListFilters, jsonOut bool, interval time.Duration, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if !jsonOut {
			if err := writeWatchClear(w, clear); err != nil {
				return err
			}
		}
		if err := runTeamQueueSummary(w, teamDir, name, filters, jsonOut); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if !jsonOut && !clear {
				fmt.Fprintln(w)
			}
		}
	}
}

func runTeamLogs(cmd *cobra.Command, teamDir, name string, opts logsOptions, listOpts logListOptions) error {
	rows, err := collectTeamLogRows(teamDir, name, listOpts, opts.Since, opts.Limit)
	if err != nil {
		return err
	}
	if opts.Latest {
		rows = latestLogListRowsLimit(rows, 1)
	}
	if opts.List {
		if opts.JSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(rows)
		}
		if opts.Format != nil {
			return renderLogListFormat(cmd.OutOrStdout(), rows, opts.Format)
		}
		renderLogList(cmd.OutOrStdout(), rows)
		return nil
	}
	if len(rows) == 0 {
		if opts.Since != nil || opts.Grep != nil {
			fmt.Fprintln(cmd.OutOrStdout(), "(no matching logs)")
			return nil
		}
		fmt.Fprintln(cmd.OutOrStdout(), "(no instances)")
		return nil
	}
	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer cancel()
	if len(rows) == 1 {
		if opts.Follow {
			if err := streamLocalLog(ctx, cmd.OutOrStdout(), rows[0].path, true, opts.Tail, nil); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team logs: log not found at %s.\n", rows[0].LogPath)
					return exitErr(1)
				}
				return err
			}
			return nil
		}
		if err := streamLogRowOnce(ctx, cmd.OutOrStdout(), rows[0], opts); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team logs: log not found at %s.\n", rows[0].LogPath)
				return exitErr(1)
			}
			return err
		}
		return nil
	}
	if opts.Follow {
		return streamLocalLogRowsFollow(ctx, cmd.OutOrStdout(), rows, opts.Tail, !opts.NoPrefix)
	}
	return streamLocalLogRowsOnce(ctx, cmd.OutOrStdout(), rows, opts.Tail, !opts.NoPrefix, opts.Grep)
}

func collectTeamLogRows(teamDir, name string, opts logListOptions, since *time.Time, limit int) ([]logListRow, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	rows, err := collectLocalLogListRows(teamDir)
	if err != nil {
		return nil, err
	}
	rows = teamLogRows(top, team, rows)
	rows = filterLogListRows(rows, opts)
	rows = filterLogListRowsSince(rows, since)
	rows = latestLogListRowsLimit(rows, limit)
	if rows == nil {
		return []logListRow{}, nil
	}
	return rows, nil
}

func collectTeamPipelineStatus(teamDir, name string) ([]pipelineStatusRow, error) {
	_, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	rows, err := collectPipelineStatusRows(teamDir, "")
	if err != nil {
		return nil, err
	}
	return teamPipelineStatus(team, rows), nil
}

func collectTeamSchedules(teamDir, name string) ([]scheduleInfo, error) {
	_, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	schedules, err := loadScheduleInfos(teamDir)
	if err != nil {
		return nil, err
	}
	return teamSchedules(team, schedules), nil
}

func runTeamPsWatch(ctx context.Context, w io.Writer, teamDir, name string, interval time.Duration, jsonOut bool, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		rows, err := collectTeamPsRows(teamDir, name, time.Now().UTC())
		if err != nil {
			return err
		}
		if err := renderTeamPsWithClear(w, rows, jsonOut, clear); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if !jsonOut && !clear {
				fmt.Fprintln(w)
			}
		}
	}
}

func runTeamStatusWatch(ctx context.Context, w io.Writer, teamDir, name string, interval time.Duration, jsonOut bool, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		snapshot, err := collectTeamStatus(teamDir, name, time.Now().UTC())
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
			if err := renderTeamStatus(w, snapshot, false); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if !jsonOut && !clear {
				fmt.Fprintln(w)
			}
		}
	}
}

func teamInstanceRows(top *topology.Topology, team *topology.Team, rows []instanceRow) []instanceRow {
	if team == nil {
		return nil
	}
	rowByName := map[string]instanceRow{}
	rowsByAgent := map[string][]instanceRow{}
	for _, row := range rows {
		rowByName[row.Instance] = row
		rowsByAgent[row.Agent] = append(rowsByAgent[row.Agent], row)
	}
	var out []instanceRow
	seen := map[string]bool{}
	for _, name := range team.Instances {
		inst := top.Instances[name]
		if inst == nil {
			continue
		}
		if inst.Ephemeral {
			addedLive := false
			for _, row := range rowsByAgent[inst.Agent] {
				if seen[row.Instance] {
					continue
				}
				out = append(out, row)
				seen[row.Instance] = true
				addedLive = true
			}
			if !addedLive && !seen[name] {
				out = append(out, declaredTeamInstanceRow(name, inst.Agent))
				seen[name] = true
			}
			continue
		}
		if row, ok := rowByName[name]; ok {
			out = append(out, row)
		} else {
			out = append(out, declaredTeamInstanceRow(name, inst.Agent))
		}
		seen[name] = true
	}
	sortPsRows(out, psSortName)
	return out
}

func teamRuntimeRows(top *topology.Topology, team *topology.Team, rows []instanceRow) []instanceRow {
	if team == nil {
		return nil
	}
	instanceNames := stringSliceSet(team.Instances)
	ephemeralAgents := map[string]bool{}
	for _, name := range team.Instances {
		if inst := top.Instances[name]; inst != nil && inst.Ephemeral {
			ephemeralAgents[inst.Agent] = true
		}
	}
	out := make([]instanceRow, 0, len(rows))
	for _, row := range rows {
		if instanceNames[row.Instance] || ephemeralAgents[row.Agent] {
			out = append(out, row)
		}
	}
	sortPsRows(out, psSortName)
	return out
}

func teamPlanRows(top *topology.Topology, team *topology.Team, rows []planRow, includeExtras bool) []planRow {
	if top == nil || team == nil {
		return nil
	}
	instances := stringSliceSet(team.Instances)
	agents := map[string]bool{}
	ephemeralOwners := map[string]bool{}
	for _, name := range team.Instances {
		inst := top.Instances[name]
		if inst == nil {
			continue
		}
		if strings.TrimSpace(inst.Agent) != "" {
			agents[inst.Agent] = true
		}
		if inst.Ephemeral {
			ephemeralOwners[inst.Name] = true
		}
	}
	out := make([]planRow, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		if seen[row.Instance] {
			continue
		}
		if instances[row.Instance] {
			out = append(out, row)
			seen[row.Instance] = true
			continue
		}
		if owner, ok := declaredEphemeralOwner(top, row.Instance, row.Agent); ok && ephemeralOwners[owner.Name] {
			out = append(out, row)
			seen[row.Instance] = true
			continue
		}
		if includeExtras && row.Kind == "extra" && agents[row.Agent] {
			out = append(out, row)
			seen[row.Instance] = true
		}
	}
	return out
}

func teamLogRows(top *topology.Topology, team *topology.Team, rows []logListRow) []logListRow {
	if top == nil || team == nil {
		return nil
	}
	instanceNames := stringSliceSet(team.Instances)
	ephemeralOwners := map[string]bool{}
	for _, name := range team.Instances {
		if inst := top.Instances[name]; inst != nil && inst.Ephemeral {
			ephemeralOwners[inst.Name] = true
		}
	}
	out := make([]logListRow, 0, len(rows))
	for _, row := range rows {
		if instanceNames[row.Instance] {
			out = append(out, row)
			continue
		}
		if owner, ok := declaredEphemeralOwner(top, row.Instance, row.Agent); ok && ephemeralOwners[owner.Name] {
			out = append(out, row)
		}
	}
	return out
}

func teamScopedTopology(top *topology.Topology, team *topology.Team) *topology.Topology {
	scoped := &topology.Topology{
		Instances: map[string]*topology.Instance{},
		Pipelines: map[string]*topology.Pipeline{},
		Schedules: map[string]*topology.Schedule{},
		Teams:     map[string]*topology.Team{},
	}
	if top == nil || team == nil {
		return scoped
	}
	for _, name := range team.Instances {
		if inst := top.Instances[name]; inst != nil {
			scoped.Instances[name] = inst
		}
	}
	for _, name := range team.Pipelines {
		if pipeline := top.Pipelines[name]; pipeline != nil {
			scoped.Pipelines[name] = pipeline
		}
	}
	for _, name := range team.Schedules {
		if schedule := top.Schedules[name]; schedule != nil {
			scoped.Schedules[name] = schedule
		}
	}
	scoped.Teams[team.Name] = team
	return scoped
}

func declaredTeamInstanceRow(name, agent string) instanceRow {
	return instanceRow{
		Instance: name,
		Agent:    agent,
		Phase:    "—",
		Age:      "—",
	}
}

func teamJobs(top *topology.Topology, team *topology.Team, jobs []*job.Job) []*job.Job {
	if team == nil {
		return nil
	}
	pipelines := stringSliceSet(team.Pipelines)
	targets := stringSliceSet(team.Instances)
	for _, name := range team.Instances {
		if inst := top.Instances[name]; inst != nil {
			targets[inst.Agent] = true
		}
	}
	out := make([]*job.Job, 0, len(jobs))
	for _, j := range jobs {
		if j == nil {
			continue
		}
		if pipelines[j.Pipeline] || targets[j.Target] {
			out = append(out, j)
		}
	}
	return out
}

func teamQueueItems(top *topology.Topology, team *topology.Team, jobs []*job.Job, items []*daemon.QueueItem) []*daemon.QueueItem {
	if team == nil {
		return nil
	}
	instanceNames := stringSliceSet(team.Instances)
	agents := map[string]bool{}
	for _, name := range team.Instances {
		if inst := top.Instances[name]; inst != nil && strings.TrimSpace(inst.Agent) != "" {
			agents[inst.Agent] = true
		}
	}
	out := make([]*daemon.QueueItem, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		if queueItemMatchesAnyJob(item, jobs) || queueItemMatchesTeamTarget(item, instanceNames, agents) {
			out = append(out, item)
		}
	}
	return out
}

func queueItemMatchesAnyJob(item *daemon.QueueItem, jobs []*job.Job) bool {
	for _, j := range jobs {
		if queueItemMatchesJob(item, j) {
			return true
		}
	}
	return false
}

func queueItemMatchesTeamTarget(item *daemon.QueueItem, instances, agents map[string]bool) bool {
	if item == nil {
		return false
	}
	for _, value := range []string{
		item.Instance,
		queuePayloadString(item.Payload, "target"),
		queuePayloadString(item.Payload, "instance"),
		queuePayloadString(item.Payload, "agent"),
	} {
		value = strings.TrimSpace(value)
		if value != "" && (instances[value] || agents[value]) {
			return true
		}
	}
	return false
}

func jobIDSet(jobs []*job.Job) map[string]bool {
	out := make(map[string]bool, len(jobs))
	for _, j := range jobs {
		if j != nil {
			out[j.ID] = true
		}
	}
	return out
}

func filterJobTriageItemsByJobIDs(items []jobTriageItem, ids map[string]bool) []jobTriageItem {
	if len(ids) == 0 {
		return nil
	}
	out := make([]jobTriageItem, 0, len(items))
	for _, item := range items {
		if ids[item.JobID] {
			out = append(out, item)
		}
	}
	return out
}

func filterJobReadyRowsByJobIDs(rows []jobReadyRow, ids map[string]bool) []jobReadyRow {
	if len(ids) == 0 {
		return nil
	}
	out := make([]jobReadyRow, 0, len(rows))
	for _, row := range rows {
		if ids[row.JobID] {
			out = append(out, row)
		}
	}
	return out
}

func filterJobStatusPreviewsByJobIDs(previews []jobStatusReconcileResult, ids map[string]bool) []jobStatusReconcileResult {
	if len(ids) == 0 {
		return nil
	}
	out := make([]jobStatusReconcileResult, 0, len(previews))
	for _, preview := range previews {
		if ids[preview.JobID] {
			out = append(out, preview)
		}
	}
	return out
}

func queueItemsForJobs(items []*daemon.QueueItem, jobs []*job.Job) []*daemon.QueueItem {
	if len(items) == 0 || len(jobs) == 0 {
		return nil
	}
	out := make([]*daemon.QueueItem, 0, len(items))
	for _, item := range items {
		for _, j := range jobs {
			if queueItemMatchesJob(item, j) {
				out = append(out, item)
				break
			}
		}
	}
	return out
}

func teamQueueActions(jobs []*job.Job, items []*daemon.QueueItem) []string {
	ids := map[string]bool{}
	for _, item := range items {
		if item == nil || item.State != daemon.QueueStateDead {
			continue
		}
		for _, j := range jobs {
			if queueItemMatchesJob(item, j) {
				ids[j.ID] = true
			}
		}
	}
	if len(ids) == 1 {
		for id := range ids {
			return []string{fmt.Sprintf("agent-team queue retry --all --job %s", id)}
		}
	}
	return []string{"agent-team queue retry --all"}
}

func teamPipelineStatus(team *topology.Team, rows []pipelineStatusRow) []pipelineStatusRow {
	if team == nil || len(team.Pipelines) == 0 {
		return nil
	}
	pipelines := stringSliceSet(team.Pipelines)
	out := make([]pipelineStatusRow, 0, len(rows))
	for _, row := range rows {
		if pipelines[row.Pipeline] {
			out = append(out, row)
		}
	}
	return out
}

func teamSchedules(team *topology.Team, schedules []scheduleInfo) []scheduleInfo {
	if team == nil || len(team.Schedules) == 0 {
		return nil
	}
	names := stringSliceSet(team.Schedules)
	out := make([]scheduleInfo, 0, len(schedules))
	for _, schedule := range schedules {
		if names[schedule.Name] {
			out = append(out, schedule)
		}
	}
	return out
}

func teamStatusActions(top *topology.Topology, team *topology.Team, snapshot *teamStatusSnapshot) []string {
	if top == nil || team == nil || snapshot == nil {
		return nil
	}
	actions := []string{}
	add := func(action string) {
		action = strings.TrimSpace(action)
		if action == "" {
			return
		}
		for _, existing := range actions {
			if existing == action {
				return
			}
		}
		actions = append(actions, action)
	}
	rowsByName := map[string]psJSONRow{}
	for _, row := range snapshot.Instances {
		rowsByName[row.Instance] = row
	}
	var missingPersistent []string
	for _, name := range team.Instances {
		inst := top.Instances[name]
		if inst == nil || inst.Ephemeral {
			continue
		}
		if rowsByName[name].Status != "running" {
			missingPersistent = append(missingPersistent, name)
		}
	}
	if len(missingPersistent) > 0 {
		sort.Strings(missingPersistent)
		add("agent-team start " + strings.Join(missingPersistent, " "))
	}
	for _, row := range snapshot.PipelineStatus {
		add("agent-team pipeline status " + row.Pipeline)
		for _, action := range row.Actions {
			add(action)
		}
	}
	return actions
}

func stringSliceSet(items []string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			out[item] = true
		}
	}
	return out
}

func renderTeamList(w io.Writer, teams []teamInfo, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(teams)
	}
	if len(teams) == 0 {
		fmt.Fprintln(w, "(no teams declared)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "TEAM\tINSTANCES\tPIPELINES\tSCHEDULES\tDESCRIPTION")
	for _, team := range teams {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%s\n",
			team.Name,
			len(team.Instances),
			len(team.Pipelines),
			len(team.Schedules),
			emptyDash(team.Description),
		)
	}
	return tw.Flush()
}

func renderTeamDetail(w io.Writer, team teamInfo, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(team)
	}
	fmt.Fprintf(w, "Team:        %s\n", team.Name)
	fmt.Fprintf(w, "Description: %s\n", emptyDash(team.Description))
	fmt.Fprintf(w, "Instances:   %s\n", emptyDash(strings.Join(team.Instances, ", ")))
	fmt.Fprintf(w, "Pipelines:   %s\n", emptyDash(strings.Join(team.Pipelines, ", ")))
	fmt.Fprintf(w, "Schedules:   %s\n", emptyDash(strings.Join(team.Schedules, ", ")))
	return nil
}

func renderTeamStatus(w io.Writer, snapshot *teamStatusSnapshot, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(snapshot)
	}
	fmt.Fprintf(w, "Team: %s\n", snapshot.Team.Name)
	if snapshot.Team.Description != "" {
		fmt.Fprintf(w, "Description: %s\n", snapshot.Team.Description)
	}
	fmt.Fprintf(w, "instances: total=%d running=%d stopped=%d exited=%d crashed=%d unknown=%d stale=%d\n",
		snapshot.InstanceSummary.Total,
		snapshot.InstanceSummary.Running,
		snapshot.InstanceSummary.Stopped,
		snapshot.InstanceSummary.Exited,
		snapshot.InstanceSummary.Crashed,
		snapshot.InstanceSummary.Unknown,
		snapshot.InstanceSummary.Stale,
	)
	renderJobSummary(w, snapshot.JobSummary)
	if snapshot.PipelineStatus != nil {
		fmt.Fprintf(w, "pipeline status: pipelines=%d jobs=%d ready_steps=%d failed_steps=%d\n",
			len(snapshot.PipelineStatus),
			countPipelineStatusJobs(snapshot.PipelineStatus),
			countPipelineStatusReadySteps(snapshot.PipelineStatus),
			countPipelineStatusFailedSteps(snapshot.PipelineStatus),
		)
	}
	if len(snapshot.Schedules) > 0 {
		fmt.Fprintf(w, "schedules: %d\n", len(snapshot.Schedules))
	}
	if len(snapshot.Actions) == 0 {
		return nil
	}
	fmt.Fprintln(w, "Actions:")
	for _, action := range snapshot.Actions {
		fmt.Fprintf(w, "  %s\n", action)
	}
	return nil
}

func renderTeamHealth(w io.Writer, snapshot *teamHealthSnapshot, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(snapshot)
	}
	fmt.Fprintf(w, "Team: %s\n", snapshot.Team.Name)
	if snapshot.Team.Description != "" {
		fmt.Fprintf(w, "Description: %s\n", snapshot.Team.Description)
	}
	renderHealth(w, snapshot.Health)
	return nil
}

func renderTeamPlan(w io.Writer, snapshot *teamPlanSnapshot) {
	if snapshot == nil || snapshot.Plan == nil {
		fmt.Fprintln(w, "(no plan)")
		return
	}
	fmt.Fprintf(w, "Team: %s\n", snapshot.Team.Name)
	if snapshot.Team.Description != "" {
		fmt.Fprintf(w, "Description: %s\n", snapshot.Team.Description)
	}
	renderPlan(w, snapshot.Plan)
}

func renderTeamPs(w io.Writer, rows []instanceRow, jsonOut bool) error {
	return renderTeamPsWithClear(w, rows, jsonOut, false)
}

func renderTeamPsWithClear(w io.Writer, rows []instanceRow, jsonOut bool, clear bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(psJSONRows(rows))
	}
	if err := writeWatchClear(w, clear); err != nil {
		return err
	}
	return renderPsTable(w, rows)
}

func renderTeamJobs(w io.Writer, jobs []*job.Job, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(jobs)
	}
	if tmpl != nil {
		for _, j := range jobs {
			if err := renderJobTemplate(w, j, tmpl); err != nil {
				return err
			}
		}
		return nil
	}
	renderJobTable(w, jobs)
	return nil
}

func renderTeamSchedules(w io.Writer, schedules []scheduleInfo, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(schedules)
	}
	if tmpl != nil {
		for _, schedule := range schedules {
			if err := tmpl.Execute(w, schedule); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	return renderScheduleList(w, schedules, false)
}
