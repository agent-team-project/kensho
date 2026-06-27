package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/runtimebin"
	"github.com/spf13/cobra"
)

// newLogsCmd builds `agent-team logs <instance> [--follow]`.
//
// When the daemon is down, daemon-managed logs can still be read from the
// persisted `.agent_team/daemon/<instance>/child.log` files. If there is no
// daemon metadata for the requested instance, the command surfaces that
// distinction with a clear daemon-start hint.
func newLogsCmd() *cobra.Command {
	var (
		target           string
		follow           bool
		all              bool
		daemonLog        bool
		latest           bool
		last             int
		lastMsg          bool
		list             bool
		jsonOut          bool
		noPrefix         bool
		clean            bool
		statuses         []string
		runtimes         []string
		agents           []string
		phases           []string
		staleOnly        bool
		runtimeStaleOnly bool
		unhealthy        bool
		tail             string
		since            string
		grep             string
		format           string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "logs [<instance>]",
		Short: "Show an instance's daemon-captured log.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && !list {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --format requires --list.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --format cannot be combined with --json.")
				return exitErr(2)
			}
			formatTemplate, err := parseLogListFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team logs: %v\n", err)
				return exitErr(2)
			}
			tailLines, err := parseLogTail(tail)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team logs: %v\n", err)
				return exitErr(2)
			}
			sinceCutoff, err := parseLogSince(since, time.Now)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team logs: %v\n", err)
				return exitErr(2)
			}
			grepPattern, err := parseLogGrep(grep)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team logs: %v\n", err)
				return exitErr(2)
			}
			return runLogs(cmd, target, args, logsOptions{
				All:            all,
				Daemon:         daemonLog,
				Follow:         follow,
				Latest:         latest,
				Limit:          last,
				LastMessage:    lastMsg,
				List:           list,
				JSON:           jsonOut,
				NoPrefix:       noPrefix,
				Clean:          clean,
				StatusFilters:  statuses,
				RuntimeFilters: runtimes,
				AgentFilters:   agents,
				PhaseFilters:   phases,
				Stale:          staleOnly,
				RuntimeStale:   runtimeStaleOnly,
				Unhealthy:      unhealthy,
				Tail:           tailLines,
				TailSet:        cmd.Flags().Changed("tail"),
				Since:          sinceCutoff,
				Grep:           grepPattern,
				Format:         formatTemplate,
			})
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Tail the log; print new bytes as they appear.")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Show logs for every daemon-known instance, prefixed by instance name.")
	cmd.Flags().BoolVar(&daemonLog, "daemon", false, "Show the agent-teamd daemon log instead of instance logs.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Show logs for the most recently started instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Show logs for the N most recently started instances after other filters (0 = all).")
	cmd.Flags().BoolVar(&lastMsg, "last-message", false, "Show the clean final Codex response sidecar instead of the raw runtime log.")
	cmd.Flags().BoolVar(&list, "list", false, "List daemon-known instance log streams instead of printing log content.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON with --list.")
	cmd.Flags().BoolVar(&noPrefix, "no-prefix", false, "Do not prefix lines when streaming multiple instance logs.")
	cmd.Flags().BoolVar(&clean, "clean", false, "Hide known Codex runtime diagnostic noise when printing raw logs.")
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "Only show logs for lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimes, "runtime", nil, "Only show logs for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&agents, "agent", nil, "Only show logs for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phases, "phase", nil, "Only show logs for instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only show logs for instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&runtimeStaleOnly, "runtime-stale", false, "Only show logs for running instances whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthy, "unhealthy", false, "Only show logs for crashed, status-stale, or runtime-stale instances.")
	cmd.Flags().StringVar(&tail, "tail", "0", "Show only the last N lines before returning or following (0 or all = all).")
	cmd.Flags().StringVar(&since, "since", "", "Only include log streams modified since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.")
	cmd.Flags().StringVar(&grep, "grep", "", "Only print log lines matching this regular expression. One-shot reads only.")
	cmd.Flags().StringVar(&format, "format", "", "With --list, render each log stream with a Go template, e.g. '{{.Instance}} {{.LogPath}}'.")
	return cmd
}

type logsOptions struct {
	All            bool
	Daemon         bool
	Follow         bool
	Latest         bool
	Limit          int
	LastMessage    bool
	List           bool
	JSON           bool
	NoPrefix       bool
	Clean          bool
	StatusFilters  []string
	RuntimeFilters []string
	AgentFilters   []string
	PhaseFilters   []string
	Stale          bool
	RuntimeStale   bool
	Unhealthy      bool
	Tail           int
	TailSet        bool
	Since          *time.Time
	Grep           *regexp.Regexp
	Format         *template.Template
}

func parseLogTail(raw string) (int, error) {
	value := strings.TrimSpace(raw)
	if value == "" || strings.EqualFold(value, "all") {
		return 0, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("--tail must be >= 0 or \"all\"")
	}
	return n, nil
}

func parseLogSince(raw string, now func() time.Time) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	since, err := parseEventSince(raw, now)
	if err != nil {
		return nil, err
	}
	return &since, nil
}

func parseLogGrep(raw string) (*regexp.Regexp, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	re, err := regexp.Compile(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid --grep pattern: %w", err)
	}
	return re, nil
}

func runLogs(cmd *cobra.Command, target string, args []string, opts logsOptions) error {
	if opts.Tail < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --tail must be >= 0.")
		return exitErr(2)
	}
	if opts.Limit < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --last must be >= 0.")
		return exitErr(2)
	}
	if opts.Since != nil && opts.Follow {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --since cannot be combined with --follow because captured logs are not timestamped.")
		return exitErr(2)
	}
	if opts.Grep != nil && opts.Follow {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --grep cannot be combined with --follow.")
		return exitErr(2)
	}
	if opts.All && len(args) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --all cannot be combined with an instance name.")
		return exitErr(2)
	}
	if opts.Latest && len(args) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --latest cannot be combined with an instance name.")
		return exitErr(2)
	}
	if opts.Limit > 0 && len(args) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --last cannot be combined with an instance name.")
		return exitErr(2)
	}
	if opts.Latest && opts.Limit > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: choose one of --latest or --last.")
		return exitErr(2)
	}
	if opts.Latest && opts.All {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --latest cannot be combined with --all.")
		return exitErr(2)
	}
	if opts.Limit > 0 && opts.All {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --last cannot be combined with --all.")
		return exitErr(2)
	}
	if opts.Latest && opts.Daemon {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --latest cannot be combined with --daemon.")
		return exitErr(2)
	}
	if opts.Limit > 0 && opts.Daemon {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --last cannot be combined with --daemon.")
		return exitErr(2)
	}
	if opts.Latest && opts.List {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --latest cannot be combined with --list.")
		return exitErr(2)
	}
	if opts.Daemon && opts.All {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --daemon cannot be combined with --all.")
		return exitErr(2)
	}
	if opts.Daemon && len(args) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --daemon cannot be combined with an instance name.")
		return exitErr(2)
	}
	if opts.JSON && !opts.List {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --json requires --list.")
		return exitErr(2)
	}
	if opts.Format != nil && !opts.List {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --format requires --list.")
		return exitErr(2)
	}
	if opts.Format != nil && opts.JSON {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --format cannot be combined with --json.")
		return exitErr(2)
	}
	if opts.Grep != nil && opts.List {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --grep cannot be combined with --list.")
		return exitErr(2)
	}
	if opts.NoPrefix && (opts.List || opts.Daemon) {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --no-prefix cannot be combined with --list or --daemon.")
		return exitErr(2)
	}
	if opts.Clean && (opts.List || opts.Daemon) {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --clean cannot be combined with --list or --daemon.")
		return exitErr(2)
	}
	if opts.LastMessage {
		switch {
		case opts.Daemon:
			fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --last-message cannot be combined with --daemon.")
			return exitErr(2)
		case opts.List:
			fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --last-message cannot be combined with --list.")
			return exitErr(2)
		case opts.JSON:
			fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --last-message cannot be combined with --json.")
			return exitErr(2)
		case opts.Format != nil:
			fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --last-message cannot be combined with --format.")
			return exitErr(2)
		case opts.Follow:
			fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --last-message cannot be combined with --follow.")
			return exitErr(2)
		case opts.TailSet:
			fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --last-message cannot be combined with --tail.")
			return exitErr(2)
		case opts.Since != nil:
			fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --last-message cannot be combined with --since.")
			return exitErr(2)
		case opts.Grep != nil:
			fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --last-message cannot be combined with --grep.")
			return exitErr(2)
		case opts.Clean:
			fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --last-message cannot be combined with --clean.")
			return exitErr(2)
		}
	}
	hasFilters := len(opts.StatusFilters) > 0 || len(opts.RuntimeFilters) > 0 || len(opts.AgentFilters) > 0 || len(opts.PhaseFilters) > 0 || opts.Stale || opts.RuntimeStale || opts.Unhealthy
	if opts.NoPrefix && !opts.All && !hasFilters && !opts.Latest && opts.Limit == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --no-prefix requires --all, --latest, --last, --status, --runtime, --agent, --phase, --stale, --runtime-stale, or --unhealthy.")
		return exitErr(2)
	}
	if hasFilters && len(args) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --status, --runtime, --agent, --phase, --stale, --runtime-stale, and --unhealthy cannot be combined with an instance name.")
		return exitErr(2)
	}
	if hasFilters && opts.Daemon {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --daemon cannot be combined with --status, --runtime, --agent, --phase, --stale, --runtime-stale, or --unhealthy.")
		return exitErr(2)
	}
	listOpts := logListOptions{}
	if opts.List || hasFilters || opts.Limit > 0 {
		var err error
		listOpts, err = newLogListOptionsWithRuntimeAndUnhealthy(opts.StatusFilters, opts.RuntimeFilters, opts.AgentFilters, opts.PhaseFilters, opts.Stale, opts.Unhealthy)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team logs: %v\n", err)
			return exitErr(2)
		}
		listOpts.runtimeStale = opts.RuntimeStale
	}
	if opts.List {
		if len(args) > 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --list cannot be combined with an instance name.")
			return exitErr(2)
		}
		if opts.All || opts.Daemon || opts.Follow || opts.TailSet {
			fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --list cannot be combined with --all, --daemon, --follow, or --tail.")
			return exitErr(2)
		}
	}
	if !opts.All && !hasFilters && len(args) != 1 && !opts.Latest && opts.Limit == 0 {
		if opts.List {
			return runLogsList(cmd, target, opts.JSON, opts.Format, listOpts, opts.Since, opts.Limit)
		}
		if opts.Daemon {
			return runDaemonLog(cmd, target, opts)
		}
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: instance is required unless --all, --latest, --last, --status, --runtime, --agent, --phase, --stale, --runtime-stale, or --unhealthy is set.")
		return exitErr(2)
	}
	if opts.List {
		return runLogsList(cmd, target, opts.JSON, opts.Format, listOpts, opts.Since, opts.Limit)
	}
	if opts.LastMessage {
		return runLogsLastMessage(cmd, target, args, opts, listOpts, hasFilters)
	}

	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	client, err := newDaemonClient(teamDir)
	if err != nil {
		if errors.Is(err, errDaemonNotRunning) {
			return runLogsLocal(cmd, teamDir, args, opts, listOpts, hasFilters)
		}
		return err
	}

	ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if opts.Latest {
		return runLatestLogWithClient(ctx, cmd, teamDir, client, opts, listOpts)
	}
	if !opts.All && !hasFilters && opts.Limit == 0 {
		if opts.Since != nil || opts.Grep != nil || opts.Clean {
			rows, err := collectLogListRows(teamDir, client)
			if err != nil {
				return err
			}
			row, ok := findLogListRow(rows, args[0])
			if !ok {
				return &logNotFoundError{Instance: args[0]}
			}
			if !row.Exists {
				return &logNotFoundError{Instance: args[0]}
			}
			return streamLogRowOnce(ctx, cmd.OutOrStdout(), row, opts)
		}
		return client.LogsStream(ctx, cmd.OutOrStdout(), args[0], opts.Follow, opts.Tail)
	}

	names, err := logInstanceNamesWithOptions(teamDir, client, listOpts, opts.Limit)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(no instances)")
		return nil
	}
	if opts.Since != nil || opts.Grep != nil || opts.Clean {
		rows, err := collectLogListRows(teamDir, client)
		if err != nil {
			return err
		}
		if opts.All || hasFilters {
			rows = filterLogListRows(rows, listOpts)
		}
		if opts.Since != nil {
			rows = filterLogListRowsSince(rows, opts.Since)
		}
		rows = latestLogListRowsLimit(rows, opts.Limit)
		if len(rows) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "(no matching logs)")
			return nil
		}
		return streamLocalLogRowsOnce(ctx, cmd.OutOrStdout(), rows, opts.Tail, !opts.NoPrefix, opts.Grep, opts.Clean)
	}
	if opts.Follow {
		return streamAllLogsFollow(ctx, cmd.OutOrStdout(), client, names, opts.Tail, !opts.NoPrefix)
	}
	return streamAllLogsOnce(ctx, cmd.OutOrStdout(), client, names, opts.Tail, !opts.NoPrefix)
}

type logListRow struct {
	Instance      string `json:"instance"`
	Agent         string `json:"agent,omitempty"`
	Runtime       string `json:"runtime,omitempty"`
	RuntimeBinary string `json:"runtime_binary,omitempty"`
	Status        string `json:"status,omitempty"`
	Phase         string `json:"phase,omitempty"`
	Stale         bool   `json:"stale,omitempty"`
	RuntimeStale  bool   `json:"runtime_stale,omitempty"`
	Unhealthy     bool   `json:"unhealthy,omitempty"`
	PID           int    `json:"pid,omitempty"`
	StartedAt     string `json:"started_at,omitempty"`
	LogPath       string `json:"log_path"`
	Exists        bool   `json:"exists"`
	SizeBytes     int64  `json:"size_bytes,omitempty"`
	ModifiedAt    string `json:"modified_at,omitempty"`

	path       string
	startedAt  time.Time
	modifiedAt time.Time
}

type logListOptions struct {
	statuses     map[string]bool
	runtimes     map[string]bool
	agents       map[string]bool
	phases       map[string]bool
	stale        bool
	runtimeStale bool
	unhealthy    bool
}

func newLogListOptions(statusFilters, agentFilters, phaseFilters []string, staleOnly bool) (logListOptions, error) {
	return newLogListOptionsWithRuntimeAndUnhealthy(statusFilters, nil, agentFilters, phaseFilters, staleOnly, false)
}

func newLogListOptionsWithUnhealthy(statusFilters, agentFilters, phaseFilters []string, staleOnly, unhealthyOnly bool) (logListOptions, error) {
	return newLogListOptionsWithRuntimeAndUnhealthy(statusFilters, nil, agentFilters, phaseFilters, staleOnly, unhealthyOnly)
}

func newLogListOptionsWithRuntimeAndUnhealthy(statusFilters, runtimeFilters, agentFilters, phaseFilters []string, staleOnly, unhealthyOnly bool) (logListOptions, error) {
	opts := logListOptions{stale: staleOnly, unhealthy: unhealthyOnly}
	if len(statusFilters) > 0 {
		opts.statuses = map[string]bool{}
		for _, raw := range splitFilterValues(statusFilters) {
			status := strings.ToLower(strings.TrimSpace(raw))
			if status == "" {
				continue
			}
			switch status {
			case string(daemon.StatusRunning), string(daemon.StatusStopped), string(daemon.StatusExited), string(daemon.StatusCrashed), "unknown":
				opts.statuses[status] = true
			default:
				return opts, fmt.Errorf("unknown --status %q (want running, stopped, exited, crashed, or unknown)", raw)
			}
		}
		if len(opts.statuses) == 0 {
			return opts, fmt.Errorf("--status requires at least one non-empty status")
		}
	}
	if len(runtimeFilters) > 0 {
		opts.runtimes = map[string]bool{}
		for _, raw := range splitFilterValues(runtimeFilters) {
			if strings.TrimSpace(raw) == "" {
				continue
			}
			kind, err := runtimebin.ParseKind(raw)
			if err != nil {
				return opts, fmt.Errorf("unknown --runtime %q (want claude or codex)", raw)
			}
			opts.runtimes[string(kind)] = true
		}
		if len(opts.runtimes) == 0 {
			return opts, fmt.Errorf("--runtime requires at least one non-empty runtime")
		}
	}
	if len(agentFilters) > 0 {
		opts.agents = map[string]bool{}
		for _, raw := range splitFilterValues(agentFilters) {
			agent := strings.TrimSpace(raw)
			if agent != "" {
				opts.agents[agent] = true
			}
		}
		if len(opts.agents) == 0 {
			return opts, fmt.Errorf("--agent requires at least one non-empty agent")
		}
	}
	if len(phaseFilters) > 0 {
		phases, err := lifecyclePhaseFilterSet(phaseFilters)
		if err != nil {
			return opts, err
		}
		opts.phases = phases
	}
	return opts, nil
}

func runLogsList(cmd *cobra.Command, target string, jsonOut bool, format *template.Template, opts logListOptions, since *time.Time, limit int) error {
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	client, err := newDaemonClient(teamDir)
	if err != nil {
		if errors.Is(err, errDaemonNotRunning) {
			rows, err := collectLocalLogListRows(teamDir)
			if err != nil {
				return err
			}
			rows = filterLogListRows(rows, opts)
			rows = filterLogListRowsSince(rows, since)
			rows = latestLogListRowsLimit(rows, limit)
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(rows)
			}
			if format != nil {
				return renderLogListFormat(cmd.OutOrStdout(), rows, format)
			}
			renderLogList(cmd.OutOrStdout(), rows)
			return nil
		}
		return err
	}
	rows, err := collectLogListRows(teamDir, client)
	if err != nil {
		return err
	}
	rows = filterLogListRows(rows, opts)
	rows = filterLogListRowsSince(rows, since)
	rows = latestLogListRowsLimit(rows, limit)
	if jsonOut {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(rows)
	}
	if format != nil {
		return renderLogListFormat(cmd.OutOrStdout(), rows, format)
	}
	renderLogList(cmd.OutOrStdout(), rows)
	return nil
}

func runLogsLocal(cmd *cobra.Command, teamDir string, args []string, opts logsOptions, listOpts logListOptions, hasFilters bool) error {
	ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	rows, err := collectLocalLogListRows(teamDir)
	if err != nil {
		return err
	}
	if opts.Latest {
		rows = filterLogListRows(rows, listOpts)
		row, ok := latestLogListRow(rows)
		if !ok {
			fmt.Fprintln(cmd.OutOrStdout(), "(no instances)")
			return nil
		}
		return streamSelectedLocalLogRow(ctx, cmd, row, opts)
	}
	if !opts.All && !hasFilters && opts.Limit == 0 {
		row, ok := findLogListRow(rows, args[0])
		if !ok {
			return noDaemonLogsError(cmd)
		}
		if opts.Since != nil {
			if !row.Exists {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team logs: log not found at %s.\n", row.LogPath)
				return exitErr(1)
			}
			return streamLogRowOnce(ctx, cmd.OutOrStdout(), row, opts)
		}
		if opts.Grep != nil {
			if !row.Exists {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team logs: log not found at %s.\n", row.LogPath)
				return exitErr(1)
			}
			return streamLogRowOnce(ctx, cmd.OutOrStdout(), row, opts)
		}
		if err := streamLocalLog(ctx, cmd.OutOrStdout(), row.path, opts.Follow, opts.Tail, nil, opts.Clean); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team logs: log not found at %s.\n", row.LogPath)
				return exitErr(1)
			}
			return err
		}
		return nil
	}

	rows = filterLogListRows(rows, listOpts)
	if opts.Since != nil {
		rows = filterLogListRowsSince(rows, opts.Since)
	}
	rows = latestLogListRowsLimit(rows, opts.Limit)
	if len(rows) == 0 {
		if opts.Since != nil {
			fmt.Fprintln(cmd.OutOrStdout(), "(no matching logs)")
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "(no instances)")
		}
		return nil
	}
	if opts.Follow {
		return streamLocalLogRowsFollow(ctx, cmd.OutOrStdout(), rows, opts.Tail, !opts.NoPrefix, opts.Clean)
	}
	return streamLocalLogRowsOnce(ctx, cmd.OutOrStdout(), rows, opts.Tail, !opts.NoPrefix, opts.Grep, opts.Clean)
}

func runLogsLastMessage(cmd *cobra.Command, target string, args []string, opts logsOptions, listOpts logListOptions, hasFilters bool) error {
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	rows, err := collectLocalLogListRows(teamDir)
	if err != nil {
		return err
	}
	if opts.Latest {
		rows = filterLogListRows(rows, listOpts)
		row, ok := latestLogListRow(rows)
		if !ok {
			fmt.Fprintln(cmd.OutOrStdout(), "(no instances)")
			return nil
		}
		return streamSelectedLastMessage(cmd, teamDir, row)
	}
	if !opts.All && !hasFilters && opts.Limit == 0 {
		row, ok := findLogListRow(rows, args[0])
		if !ok {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team logs: no daemon metadata for instance %q.\n", args[0])
			fmt.Fprintln(cmd.ErrOrStderr(), "  Run a daemon-managed Codex one-shot first, then retry with `agent-team logs --last-message`.")
			return exitErr(1)
		}
		return streamSelectedLastMessage(cmd, teamDir, row)
	}
	rows = filterLogListRows(rows, listOpts)
	rows = latestLogListRowsLimit(rows, opts.Limit)
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(no instances)")
		return nil
	}
	return streamLastMessageRows(cmd.OutOrStdout(), teamDir, rows, !opts.NoPrefix)
}

func streamSelectedLastMessage(cmd *cobra.Command, teamDir string, row logListRow) error {
	return streamSelectedLastMessageWithPrefix(cmd, teamDir, row, "agent-team logs")
}

func streamSelectedLastMessageWithPrefix(cmd *cobra.Command, teamDir string, row logListRow, prefix string) error {
	path := lastMessagePathForInstance(teamDir, row.Instance)
	if err := writeLastMessageFile(cmd.OutOrStdout(), path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: last message not found at %s.\n", prefix, displayPathFromTeamDir(teamDir, path))
			fmt.Fprintln(cmd.ErrOrStderr(), "  Codex last-message capture is available for one-shot runs launched after this feature was added.")
			return exitErr(1)
		}
		return err
	}
	return nil
}

func streamLastMessageRows(w io.Writer, teamDir string, rows []logListRow, prefix bool) error {
	var mu sync.Mutex
	wrote := false
	for _, row := range rows {
		path := lastMessagePathForInstance(teamDir, row.Instance)
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		pw := multiLogWriter(w, row.Instance, &mu, prefix)
		if err := writeLastMessageFile(pw, path); err != nil {
			return err
		}
		wrote = true
	}
	if !wrote {
		fmt.Fprintln(w, "(no matching last messages)")
	}
	return nil
}

func writeLastMessageFile(w io.Writer, path string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		return err
	}
	if len(body) == 0 || body[len(body)-1] != '\n' {
		_, err = io.WriteString(w, "\n")
	}
	return err
}

func lastMessagePathForInstance(teamDir, instance string) string {
	return filepath.Join(teamDir, "state", instance, runtimebin.CodexLastMessageFile)
}

func runLatestLogWithClient(ctx context.Context, cmd *cobra.Command, teamDir string, client *daemonClient, opts logsOptions, listOpts logListOptions) error {
	rows, err := collectLogListRows(teamDir, client)
	if err != nil {
		return err
	}
	rows = filterLogListRows(rows, listOpts)
	row, ok := latestLogListRow(rows)
	if !ok {
		fmt.Fprintln(cmd.OutOrStdout(), "(no instances)")
		return nil
	}
	if opts.Since != nil || opts.Grep != nil || opts.Clean {
		return streamSelectedLocalLogRow(ctx, cmd, row, opts)
	}
	return client.LogsStream(ctx, cmd.OutOrStdout(), row.Instance, opts.Follow, opts.Tail)
}

func streamSelectedLocalLogRow(ctx context.Context, cmd *cobra.Command, row logListRow, opts logsOptions) error {
	if opts.Since != nil || opts.Grep != nil || opts.Clean {
		if !row.Exists {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team logs: log not found at %s.\n", row.LogPath)
			return exitErr(1)
		}
		if err := streamLogRowOnce(ctx, cmd.OutOrStdout(), row, opts); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team logs: log not found at %s.\n", row.LogPath)
				return exitErr(1)
			}
			return err
		}
		return nil
	}
	if err := streamLocalLog(ctx, cmd.OutOrStdout(), row.path, opts.Follow, opts.Tail, nil, opts.Clean); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team logs: log not found at %s.\n", row.LogPath)
			return exitErr(1)
		}
		return err
	}
	return nil
}

func noDaemonLogsError(cmd *cobra.Command) error {
	fmt.Fprintln(cmd.ErrOrStderr(),
		"agent-team logs: no daemon running — start it with `agent-team daemon start`.")
	fmt.Fprintln(cmd.ErrOrStderr(),
		"  (without daemon-managed metadata, there are no captured logs to read.)")
	return exitErr(1)
}

func findLogListRow(rows []logListRow, instance string) (logListRow, bool) {
	for _, row := range rows {
		if row.Instance == instance {
			return row, true
		}
	}
	return logListRow{}, false
}

func collectLogListRows(teamDir string, client *daemonClient) ([]logListRow, error) {
	metas, err := client.Instances()
	if err != nil {
		return nil, err
	}
	return logListRowsFromMetadata(teamDir, metas)
}

func collectLocalLogListRows(teamDir string) ([]logListRow, error) {
	metas, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	return logListRowsFromMetadata(teamDir, metas)
}

func logListRowsFromMetadata(teamDir string, metas []*daemon.Metadata) ([]logListRow, error) {
	sort.Slice(metas, func(i, j int) bool { return metas[i].Instance < metas[j].Instance })
	phaseByInstance := logPhaseByInstance(teamDir)
	staleInstances := staleInstanceSet(teamDir, time.Now())
	rows := make([]logListRow, 0, len(metas))
	for _, meta := range metas {
		logPath := logPathForMetadata(teamDir, meta)
		runtimeStale := runtimeResumeMetadataIsStale(meta)
		row := logListRow{
			Instance:      meta.Instance,
			Agent:         meta.Agent,
			Runtime:       meta.Runtime,
			RuntimeBinary: meta.RuntimeBinary,
			Status:        metadataStatusKey(meta),
			Phase:         statsPhaseKey(phaseByInstance[meta.Instance]),
			Stale:         staleInstances[meta.Instance],
			RuntimeStale:  runtimeStale,
			Unhealthy:     metadataStatusKey(meta) == string(daemon.StatusCrashed) || staleInstances[meta.Instance] || runtimeStale,
			PID:           meta.PID,
			LogPath:       displayPathFromTeamDir(teamDir, logPath),
			path:          logPath,
			startedAt:     meta.StartedAt,
		}
		if !meta.StartedAt.IsZero() {
			row.StartedAt = meta.StartedAt.UTC().Format(time.RFC3339)
		}
		if st, err := os.Stat(logPath); err == nil {
			row.Exists = true
			row.SizeBytes = st.Size()
			row.modifiedAt = st.ModTime()
			row.ModifiedAt = row.modifiedAt.UTC().Format(time.RFC3339)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func logPhaseByInstance(teamDir string) map[string]string {
	return statusPhaseByInstance(teamDir, time.Now())
}

func latestLogListRow(rows []logListRow) (logListRow, bool) {
	rows = latestLogListRowsLimit(rows, 1)
	if len(rows) == 0 {
		return logListRow{}, false
	}
	return rows[0], true
}

func latestLogListRowsLimit(rows []logListRow, limit int) []logListRow {
	if limit <= 0 || len(rows) == 0 {
		return rows
	}
	out := append([]logListRow(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.startedAt.Equal(b.startedAt) {
			return psTimeAfter(a.startedAt, b.startedAt)
		}
		return a.Instance < b.Instance
	})
	if limit < len(out) {
		out = out[:limit]
	}
	return out
}

func logPathForMetadata(teamDir string, meta *daemon.Metadata) string {
	if meta != nil && meta.LogPath != "" {
		return meta.LogPath
	}
	instance := ""
	if meta != nil {
		instance = meta.Instance
	}
	return filepath.Join(daemon.DaemonRoot(teamDir), instance, "child.log")
}

func filterLogListRows(rows []logListRow, opts logListOptions) []logListRow {
	if !logListOptionsHasFilters(opts) {
		return rows
	}
	out := make([]logListRow, 0, len(rows))
	for _, row := range rows {
		status := row.Status
		if status == "" {
			status = "unknown"
		}
		if len(opts.statuses) > 0 && !opts.statuses[status] {
			continue
		}
		if len(opts.runtimes) > 0 && !opts.runtimes[logRowRuntimeKey(row)] {
			continue
		}
		if len(opts.agents) > 0 && !opts.agents[row.Agent] {
			continue
		}
		if len(opts.phases) > 0 && !opts.phases[logRowPhaseKey(row)] {
			continue
		}
		if opts.stale && !row.Stale {
			continue
		}
		if opts.runtimeStale && !row.RuntimeStale {
			continue
		}
		if opts.unhealthy && !logRowUnhealthy(row) {
			continue
		}
		out = append(out, row)
	}
	return out
}

func logListOptionsHasFilters(opts logListOptions) bool {
	return len(opts.statuses) > 0 || len(opts.runtimes) > 0 || len(opts.agents) > 0 || len(opts.phases) > 0 || opts.stale || opts.runtimeStale || opts.unhealthy
}

func logRowRuntimeKey(row logListRow) string {
	runtime := strings.ToLower(strings.TrimSpace(row.Runtime))
	if runtime == "" {
		return "unknown"
	}
	return runtime
}

func logRowPhaseKey(row logListRow) string {
	return statsPhaseKey(row.Phase)
}

func logRowUnhealthy(row logListRow) bool {
	return row.Status == string(daemon.StatusCrashed) || row.Stale || row.RuntimeStale
}

func filterLogListRowsSince(rows []logListRow, since *time.Time) []logListRow {
	if since == nil {
		return rows
	}
	out := make([]logListRow, 0, len(rows))
	for _, row := range rows {
		if !logRowModifiedSince(row, since) {
			continue
		}
		out = append(out, row)
	}
	return out
}

func logRowModifiedSince(row logListRow, since *time.Time) bool {
	if since == nil {
		return true
	}
	if !row.Exists {
		return false
	}
	modifiedAt := row.modifiedAt
	if modifiedAt.IsZero() && row.ModifiedAt != "" {
		if parsed, err := time.Parse(time.RFC3339, row.ModifiedAt); err == nil {
			modifiedAt = parsed
		}
	}
	if modifiedAt.IsZero() {
		return false
	}
	return !modifiedAt.Before(*since)
}

func renderLogList(w io.Writer, rows []logListRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no instances)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "INSTANCE\tAGENT\tSTATUS\tPHASE\tSTALE\tRUNTIME_STALE\tSIZE\tLOG")
	for _, row := range rows {
		size := "-"
		if row.Exists {
			size = formatLogSize(row.SizeBytes)
		}
		stale := "—"
		if row.Stale {
			stale = "yes"
		}
		runtimeStale := "—"
		if row.RuntimeStale {
			runtimeStale = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", row.Instance, row.Agent, row.Status, logRowPhaseKey(row), stale, runtimeStale, size, row.LogPath)
	}
	_ = tw.Flush()
}

func parseLogListFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("logs-list-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

type logListFormatRow struct {
	Instance     string
	Agent        string
	Status       string
	Phase        string
	Stale        bool
	RuntimeStale bool
	Unhealthy    bool
	PID          int
	LogPath      string
	Exists       bool
	SizeBytes    int64
	Size         string
	ModifiedAt   string
	StartedAt    string
}

func renderLogListFormat(w io.Writer, rows []logListRow, tmpl *template.Template) error {
	for _, row := range rows {
		formatRow := logListFormatRow{
			Instance:     row.Instance,
			Agent:        row.Agent,
			Status:       row.Status,
			Phase:        row.Phase,
			Stale:        row.Stale,
			RuntimeStale: row.RuntimeStale,
			Unhealthy:    logRowUnhealthy(row),
			PID:          row.PID,
			LogPath:      row.LogPath,
			Exists:       row.Exists,
			SizeBytes:    row.SizeBytes,
			Size:         "-",
			ModifiedAt:   row.ModifiedAt,
			StartedAt:    row.StartedAt,
		}
		if row.Exists {
			formatRow.Size = formatLogSize(row.SizeBytes)
		}
		if err := tmpl.Execute(w, formatRow); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}

func formatLogSize(bytes int64) string {
	if bytes <= 0 {
		return "0B"
	}
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	kib := float64(bytes) / 1024
	if kib < 1024 {
		return fmt.Sprintf("%.1fKiB", kib)
	}
	mib := kib / 1024
	if mib < 1024 {
		return fmt.Sprintf("%.1fMiB", mib)
	}
	return fmt.Sprintf("%.1fGiB", mib/1024)
}

func runDaemonLog(cmd *cobra.Command, target string, opts logsOptions) error {
	if opts.Since != nil && opts.Follow {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --since cannot be combined with --follow because captured logs are not timestamped.")
		return exitErr(2)
	}
	if opts.Grep != nil && opts.Follow {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --grep cannot be combined with --follow.")
		return exitErr(2)
	}
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	logPath := daemon.LogPath(teamDir)
	if opts.Since != nil {
		st, err := os.Stat(logPath)
		if err == nil && st.ModTime().Before(*opts.Since) {
			fmt.Fprintln(cmd.OutOrStdout(), "(no matching logs)")
			return nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := streamLocalLog(ctx, cmd.OutOrStdout(), logPath, opts.Follow, opts.Tail, opts.Grep, false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team logs: daemon log not found at %s.\n", daemon.LogPath(teamDir))
			fmt.Fprintln(cmd.ErrOrStderr(), "  Start the daemon with `agent-team start` or `agent-team daemon start --detach` first.")
			return exitErr(1)
		}
		return err
	}
	return nil
}

func logInstanceNames(client *daemonClient) ([]string, error) {
	metas, err := client.Instances()
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

func logInstanceNamesWithOptions(teamDir string, client *daemonClient, opts logListOptions, limit int) ([]string, error) {
	if !logListOptionsHasFilters(opts) {
		if limit <= 0 {
			return logInstanceNames(client)
		}
		rows, err := collectLogListRows(teamDir, client)
		if err != nil {
			return nil, err
		}
		rows = latestLogListRowsLimit(rows, limit)
		names := make([]string, 0, len(rows))
		for _, row := range rows {
			names = append(names, row.Instance)
		}
		return names, nil
	}
	rows, err := collectLogListRows(teamDir, client)
	if err != nil {
		return nil, err
	}
	rows = filterLogListRows(rows, opts)
	rows = latestLogListRowsLimit(rows, limit)
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Instance)
	}
	if limit <= 0 {
		sort.Strings(names)
	}
	return names, nil
}

func streamAllLogsOnce(ctx context.Context, w io.Writer, client *daemonClient, names []string, tail int, prefix bool) error {
	var mu sync.Mutex
	for _, name := range names {
		pw := multiLogWriter(w, name, &mu, prefix)
		if err := client.LogsStream(ctx, pw, name, false, tail); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func streamAllLogsFollow(ctx context.Context, w io.Writer, client *daemonClient, names []string, tail int, prefix bool) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var mu sync.Mutex
	var wg sync.WaitGroup
	errCh := make(chan error, len(names))
	for _, name := range names {
		name := name
		wg.Add(1)
		go func() {
			defer wg.Done()
			pw := multiLogWriter(w, name, &mu, prefix)
			if err := client.LogsStream(ctx, pw, name, true, tail); err != nil {
				errCh <- fmt.Errorf("%s: %w", name, err)
				cancel()
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		<-done
	}
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

func streamLocalLogRowsOnce(ctx context.Context, w io.Writer, rows []logListRow, tail int, prefix bool, grep *regexp.Regexp, clean bool) error {
	var mu sync.Mutex
	for _, row := range rows {
		pw := multiLogWriter(w, row.Instance, &mu, prefix)
		if err := streamLocalLog(ctx, pw, row.path, false, tail, grep, clean); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("%s: log not found at %s", row.Instance, row.LogPath)
			}
			return fmt.Errorf("%s: %w", row.Instance, err)
		}
	}
	return nil
}

func streamLogRowOnce(ctx context.Context, w io.Writer, row logListRow, opts logsOptions) error {
	if !row.Exists {
		return os.ErrNotExist
	}
	if !logRowModifiedSince(row, opts.Since) {
		fmt.Fprintln(w, "(no matching logs)")
		return nil
	}
	return streamLocalLog(ctx, w, row.path, false, opts.Tail, opts.Grep, opts.Clean)
}

func streamLogRowOnceSince(ctx context.Context, w io.Writer, row logListRow, opts logsOptions) error {
	return streamLogRowOnce(ctx, w, row, opts)
}

func streamLocalLogRowsFollow(ctx context.Context, w io.Writer, rows []logListRow, tail int, prefix bool, clean bool) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var mu sync.Mutex
	var wg sync.WaitGroup
	errCh := make(chan error, len(rows))
	for _, row := range rows {
		row := row
		wg.Add(1)
		go func() {
			defer wg.Done()
			pw := multiLogWriter(w, row.Instance, &mu, prefix)
			if err := streamLocalLog(ctx, pw, row.path, true, tail, nil, clean); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					errCh <- fmt.Errorf("%s: log not found at %s", row.Instance, row.LogPath)
				} else {
					errCh <- fmt.Errorf("%s: %w", row.Instance, err)
				}
				cancel()
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		<-done
	}
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

func streamLocalLog(ctx context.Context, w io.Writer, path string, follow bool, tail int, grep *regexp.Regexp, clean bool) error {
	if grep != nil && follow {
		return errors.New("logs: grep cannot be combined with follow")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if clean || grep != nil {
		if err := copyFilteredLinesLocal(w, f, tail, grep, clean); err != nil {
			return err
		}
	} else if tail > 0 {
		if err := copyTailLinesLocal(w, f, tail); err != nil {
			return err
		}
	} else if _, err := io.Copy(w, f); err != nil {
		return err
	}
	if !follow {
		return nil
	}
	if clean {
		return followCleanLogLines(ctx, w, f)
	}

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	buf := make([]byte, 32*1024)
	for {
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
		for {
			n, rerr := f.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return werr
				}
			}
			if rerr != nil {
				if errors.Is(rerr, io.EOF) {
					break
				}
				return rerr
			}
		}
	}
}

func copyTailLinesLocal(w io.Writer, f *os.File, lines int) error {
	body, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	if lines <= 0 {
		_, err := w.Write(body)
		return err
	}
	start := 0
	seen := 0
	for i := len(body) - 1; i >= 0; i-- {
		if body[i] != '\n' {
			continue
		}
		if i == len(body)-1 {
			continue
		}
		seen++
		if seen == lines {
			start = i + 1
			break
		}
	}
	_, err = w.Write(body[start:])
	return err
}

func copyFilteredLinesLocal(w io.Writer, f *os.File, tail int, grep *regexp.Regexp, clean bool) error {
	body, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	lines := filteredLogLines(body, grep, clean)
	if tail > 0 && tail < len(lines) {
		lines = lines[len(lines)-tail:]
	}
	for _, line := range lines {
		if _, err := w.Write(line); err != nil {
			return err
		}
	}
	return nil
}

func filteredLogLines(body []byte, grep *regexp.Regexp, clean bool) [][]byte {
	parts := bytes.SplitAfter(body, []byte("\n"))
	out := make([][]byte, 0, len(parts))
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		line := string(part)
		normalized := lineForGrep(line)
		if clean && isCleanLogNoiseLine(normalized) {
			continue
		}
		if grep != nil && !grep.MatchString(normalized) {
			continue
		}
		out = append(out, part)
	}
	return out
}

func followCleanLogLines(ctx context.Context, w io.Writer, f *os.File) error {
	reader := bufio.NewReader(f)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
		for {
			line, err := reader.ReadString('\n')
			if line != "" && !isCleanLogNoiseLine(lineForGrep(line)) {
				if _, werr := io.WriteString(w, line); werr != nil {
					return werr
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return err
			}
		}
	}
}

func isCleanLogNoiseLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	for _, prefix := range []string{
		"Reading additional input from stdin",
		"OpenAI Codex v",
		"workdir:",
		"model:",
		"provider:",
		"approval:",
		"sandbox:",
		"reasoning effort:",
		"reasoning summaries:",
		"session id:",
		"ERROR: Reconnecting...",
		"warning: Falling back from WebSockets",
	} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	if line == "--------" {
		return true
	}
	for _, marker := range []string{
		" WARN codex_",
		" ERROR codex_",
		" WARN rmcp::",
		" ERROR rmcp::",
		" WARN tokio",
		" ERROR tokio",
	} {
		if strings.Contains(line, marker) {
			return true
		}
	}
	return false
}

func readTailLinesLocal(f *os.File, lines int) ([]byte, error) {
	body, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	if lines <= 0 {
		return body, nil
	}
	start := 0
	seen := 0
	for i := len(body) - 1; i >= 0; i-- {
		if body[i] != '\n' {
			continue
		}
		if i == len(body)-1 {
			continue
		}
		seen++
		if seen == lines {
			start = i + 1
			break
		}
	}
	return body[start:], nil
}

func lineForGrep(line string) string {
	line = strings.TrimSuffix(line, "\n")
	return strings.TrimSuffix(line, "\r")
}

type prefixLineWriter struct {
	w           io.Writer
	prefix      string
	mu          *sync.Mutex
	atLineStart bool
}

func newPrefixLineWriter(w io.Writer, instance string, mu *sync.Mutex) *prefixLineWriter {
	return &prefixLineWriter{
		w:           w,
		prefix:      fmt.Sprintf("%-20s | ", instance),
		mu:          mu,
		atLineStart: true,
	}
}

func multiLogWriter(w io.Writer, instance string, mu *sync.Mutex, prefix bool) io.Writer {
	if prefix {
		return newPrefixLineWriter(w, instance, mu)
	}
	return &lockedWriter{w: w, mu: mu}
}

type lockedWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}

func (w *prefixLineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	written := 0
	for len(p) > 0 {
		if w.atLineStart {
			if _, err := io.WriteString(w.w, w.prefix); err != nil {
				return written, err
			}
			w.atLineStart = false
		}
		idx := -1
		for i, b := range p {
			if b == '\n' {
				idx = i
				break
			}
		}
		if idx == -1 {
			n, err := w.w.Write(p)
			written += n
			return written, err
		}
		n, err := w.w.Write(p[:idx+1])
		written += n
		if err != nil {
			return written, err
		}
		p = p[idx+1:]
		w.atLineStart = true
	}
	return written, nil
}
