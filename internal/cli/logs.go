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
		target    string
		follow    bool
		all       bool
		daemonLog bool
		latest    bool
		last      int
		list      bool
		jsonOut   bool
		noPrefix  bool
		statuses  []string
		agents    []string
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
				All:           all,
				Daemon:        daemonLog,
				Follow:        follow,
				Latest:        latest,
				Limit:         last,
				List:          list,
				JSON:          jsonOut,
				NoPrefix:      noPrefix,
				StatusFilters: statuses,
				AgentFilters:  agents,
				PhaseFilters:  phases,
				Stale:         staleOnly,
				Unhealthy:     unhealthy,
				Tail:          tailLines,
				TailSet:       cmd.Flags().Changed("tail"),
				Since:         sinceCutoff,
				Grep:          grepPattern,
				Format:        formatTemplate,
			})
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Tail the log; print new bytes as they appear.")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Show logs for every daemon-known instance, prefixed by instance name.")
	cmd.Flags().BoolVar(&daemonLog, "daemon", false, "Show the agent-teamd daemon log instead of instance logs.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Show logs for the most recently started instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Show logs for the N most recently started instances after other filters (0 = all).")
	cmd.Flags().BoolVar(&list, "list", false, "List daemon-known instance log streams instead of printing log content.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON with --list.")
	cmd.Flags().BoolVar(&noPrefix, "no-prefix", false, "Do not prefix lines when streaming multiple instance logs.")
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "Only show logs for lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&agents, "agent", nil, "Only show logs for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phases, "phase", nil, "Only show logs for instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only show logs for instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&unhealthy, "unhealthy", false, "Only show logs for crashed or stale instances.")
	cmd.Flags().StringVar(&tail, "tail", "0", "Show only the last N lines before returning or following (0 or all = all).")
	cmd.Flags().StringVar(&since, "since", "", "Only include log streams modified since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.")
	cmd.Flags().StringVar(&grep, "grep", "", "Only print log lines matching this regular expression. One-shot reads only.")
	cmd.Flags().StringVar(&format, "format", "", "With --list, render each log stream with a Go template, e.g. '{{.Instance}} {{.LogPath}}'.")
	return cmd
}

func newLogAttachCmd() *cobra.Command {
	var (
		target    string
		all       bool
		latest    bool
		last      int
		noFollow  bool
		statuses  []string
		agents    []string
		phases    []string
		staleOnly bool
		unhealthy bool
		tail      string
		since     string
		grep      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "attach [<instance>]",
		Short: "Follow a daemon-managed instance's captured output.",
		Long: "Attach to an instance's daemon-captured stdout/stderr stream. " +
			"This is a focused shortcut for following selected `agent-team logs` streams.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: --last must be >= 0.")
				return exitErr(2)
			}
			hasFilters := len(statuses) > 0 || len(agents) > 0 || len(phases) > 0 || staleOnly || unhealthy
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: --all cannot be combined with an instance name.")
				return exitErr(2)
			}
			if latest && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: --latest cannot be combined with an instance name.")
				return exitErr(2)
			}
			if last > 0 && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: --last cannot be combined with an instance name.")
				return exitErr(2)
			}
			if hasFilters && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: --status, --agent, --phase, --stale, and --unhealthy cannot be combined with an instance name.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: choose one of --latest or --last.")
				return exitErr(2)
			}
			if latest && all {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: --latest cannot be combined with --all.")
				return exitErr(2)
			}
			if last > 0 && all {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: --last cannot be combined with --all.")
				return exitErr(2)
			}
			if hasFilters {
				if _, err := newLogListOptionsWithUnhealthy(statuses, agents, phases, staleOnly, unhealthy); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team attach: %v\n", err)
					return exitErr(2)
				}
			}
			if !latest && last == 0 && !all && !hasFilters && len(args) != 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: instance is required unless --all, --latest, --last, --status, --agent, --phase, --stale, or --unhealthy is set.")
				return exitErr(2)
			}
			tailLines, err := parseLogTail(tail)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team attach: %v\n", err)
				return exitErr(2)
			}
			sinceCutoff, err := parseLogSince(since, time.Now)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team attach: %v\n", err)
				return exitErr(2)
			}
			grepPattern, err := parseLogGrep(grep)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team attach: %v\n", err)
				return exitErr(2)
			}
			if sinceCutoff != nil && !noFollow {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: --since requires --no-follow because captured logs are not timestamped.")
				return exitErr(2)
			}
			if grepPattern != nil && !noFollow {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: --grep requires --no-follow.")
				return exitErr(2)
			}
			return runLogs(cmd, target, args, logsOptions{
				All:           all,
				Follow:        !noFollow,
				Latest:        latest,
				Limit:         last,
				StatusFilters: statuses,
				AgentFilters:  agents,
				PhaseFilters:  phases,
				Stale:         staleOnly,
				Unhealthy:     unhealthy,
				Tail:          tailLines,
				TailSet:       cmd.Flags().Changed("tail"),
				Since:         sinceCutoff,
				Grep:          grepPattern,
			})
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Attach to every daemon-known instance, prefixed by instance name.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Attach to the most recently started instance.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Attach to the N most recently started instances (0 = disabled).")
	cmd.Flags().BoolVar(&noFollow, "no-follow", false, "Print the selected log tail and exit instead of following.")
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "Only attach to instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&agents, "agent", nil, "Only attach to instances for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phases, "phase", nil, "Only attach to instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only attach to instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&unhealthy, "unhealthy", false, "Only attach to crashed or stale instances.")
	cmd.Flags().StringVar(&tail, "tail", "50", "Show only the last N lines before following (0 or all = all).")
	cmd.Flags().StringVar(&since, "since", "", "With --no-follow, only print the log if it was modified since this duration ago (for example 10m, 24h) or RFC3339 timestamp.")
	cmd.Flags().StringVar(&grep, "grep", "", "With --no-follow, only print log lines matching this regular expression.")
	return cmd
}

type logsOptions struct {
	All           bool
	Daemon        bool
	Follow        bool
	Latest        bool
	Limit         int
	List          bool
	JSON          bool
	NoPrefix      bool
	StatusFilters []string
	AgentFilters  []string
	PhaseFilters  []string
	Stale         bool
	Unhealthy     bool
	Tail          int
	TailSet       bool
	Since         *time.Time
	Grep          *regexp.Regexp
	Format        *template.Template
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
	hasFilters := len(opts.StatusFilters) > 0 || len(opts.AgentFilters) > 0 || len(opts.PhaseFilters) > 0 || opts.Stale || opts.Unhealthy
	if opts.NoPrefix && !opts.All && !hasFilters && !opts.Latest && opts.Limit == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --no-prefix requires --all, --latest, --last, --status, --agent, --phase, --stale, or --unhealthy.")
		return exitErr(2)
	}
	if hasFilters && len(args) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --status, --agent, --phase, --stale, and --unhealthy cannot be combined with an instance name.")
		return exitErr(2)
	}
	if hasFilters && opts.Daemon {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: --daemon cannot be combined with --status, --agent, --phase, --stale, or --unhealthy.")
		return exitErr(2)
	}
	listOpts := logListOptions{}
	if opts.List || hasFilters || opts.Limit > 0 {
		var err error
		listOpts, err = newLogListOptionsWithUnhealthy(opts.StatusFilters, opts.AgentFilters, opts.PhaseFilters, opts.Stale, opts.Unhealthy)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team logs: %v\n", err)
			return exitErr(2)
		}
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
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team logs: instance is required unless --all, --latest, --last, --status, --agent, --phase, --stale, or --unhealthy is set.")
		return exitErr(2)
	}
	if opts.List {
		return runLogsList(cmd, target, opts.JSON, opts.Format, listOpts, opts.Since, opts.Limit)
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
		if opts.Since != nil || opts.Grep != nil {
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
	if opts.Since != nil || opts.Grep != nil {
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
		return streamLocalLogRowsOnce(ctx, cmd.OutOrStdout(), rows, opts.Tail, !opts.NoPrefix, opts.Grep)
	}
	if opts.Follow {
		return streamAllLogsFollow(ctx, cmd.OutOrStdout(), client, names, opts.Tail, !opts.NoPrefix)
	}
	return streamAllLogsOnce(ctx, cmd.OutOrStdout(), client, names, opts.Tail, !opts.NoPrefix)
}

type logListRow struct {
	Instance   string `json:"instance"`
	Agent      string `json:"agent,omitempty"`
	Status     string `json:"status,omitempty"`
	Phase      string `json:"phase,omitempty"`
	Stale      bool   `json:"stale,omitempty"`
	PID        int    `json:"pid,omitempty"`
	StartedAt  string `json:"started_at,omitempty"`
	LogPath    string `json:"log_path"`
	Exists     bool   `json:"exists"`
	SizeBytes  int64  `json:"size_bytes,omitempty"`
	ModifiedAt string `json:"modified_at,omitempty"`

	path       string
	startedAt  time.Time
	modifiedAt time.Time
}

type logListOptions struct {
	statuses  map[string]bool
	agents    map[string]bool
	phases    map[string]bool
	stale     bool
	unhealthy bool
}

func newLogListOptions(statusFilters, agentFilters, phaseFilters []string, staleOnly bool) (logListOptions, error) {
	return newLogListOptionsWithUnhealthy(statusFilters, agentFilters, phaseFilters, staleOnly, false)
}

func newLogListOptionsWithUnhealthy(statusFilters, agentFilters, phaseFilters []string, staleOnly, unhealthyOnly bool) (logListOptions, error) {
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
		if err := streamLocalLog(ctx, cmd.OutOrStdout(), row.path, opts.Follow, opts.Tail, nil); err != nil {
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
		return streamLocalLogRowsFollow(ctx, cmd.OutOrStdout(), rows, opts.Tail, !opts.NoPrefix)
	}
	return streamLocalLogRowsOnce(ctx, cmd.OutOrStdout(), rows, opts.Tail, !opts.NoPrefix, opts.Grep)
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
	if opts.Since != nil || opts.Grep != nil {
		return streamSelectedLocalLogRow(ctx, cmd, row, opts)
	}
	return client.LogsStream(ctx, cmd.OutOrStdout(), row.Instance, opts.Follow, opts.Tail)
}

func streamSelectedLocalLogRow(ctx context.Context, cmd *cobra.Command, row logListRow, opts logsOptions) error {
	if opts.Since != nil || opts.Grep != nil {
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
	if err := streamLocalLog(ctx, cmd.OutOrStdout(), row.path, opts.Follow, opts.Tail, nil); err != nil {
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
		row := logListRow{
			Instance:  meta.Instance,
			Agent:     meta.Agent,
			Status:    metadataStatusKey(meta),
			Phase:     statsPhaseKey(phaseByInstance[meta.Instance]),
			Stale:     staleInstances[meta.Instance],
			PID:       meta.PID,
			LogPath:   displayPathFromTeamDir(teamDir, logPath),
			path:      logPath,
			startedAt: meta.StartedAt,
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
		if len(opts.agents) > 0 && !opts.agents[row.Agent] {
			continue
		}
		if len(opts.phases) > 0 && !opts.phases[logRowPhaseKey(row)] {
			continue
		}
		if opts.stale && !row.Stale {
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
	return len(opts.statuses) > 0 || len(opts.agents) > 0 || len(opts.phases) > 0 || opts.stale || opts.unhealthy
}

func logRowPhaseKey(row logListRow) string {
	return statsPhaseKey(row.Phase)
}

func logRowUnhealthy(row logListRow) bool {
	return row.Status == string(daemon.StatusCrashed) || row.Stale
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
	fmt.Fprintln(tw, "INSTANCE\tAGENT\tSTATUS\tPHASE\tSTALE\tSIZE\tLOG")
	for _, row := range rows {
		size := "-"
		if row.Exists {
			size = formatLogSize(row.SizeBytes)
		}
		stale := "—"
		if row.Stale {
			stale = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", row.Instance, row.Agent, row.Status, logRowPhaseKey(row), stale, size, row.LogPath)
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
	Instance   string
	Agent      string
	Status     string
	Phase      string
	Stale      bool
	PID        int
	LogPath    string
	Exists     bool
	SizeBytes  int64
	Size       string
	ModifiedAt string
	StartedAt  string
}

func renderLogListFormat(w io.Writer, rows []logListRow, tmpl *template.Template) error {
	for _, row := range rows {
		formatRow := logListFormatRow{
			Instance:   row.Instance,
			Agent:      row.Agent,
			Status:     row.Status,
			Phase:      row.Phase,
			Stale:      row.Stale,
			PID:        row.PID,
			LogPath:    row.LogPath,
			Exists:     row.Exists,
			SizeBytes:  row.SizeBytes,
			Size:       "-",
			ModifiedAt: row.ModifiedAt,
			StartedAt:  row.StartedAt,
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
	if err := streamLocalLog(ctx, cmd.OutOrStdout(), logPath, opts.Follow, opts.Tail, opts.Grep); err != nil {
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

func streamLocalLogRowsOnce(ctx context.Context, w io.Writer, rows []logListRow, tail int, prefix bool, grep *regexp.Regexp) error {
	var mu sync.Mutex
	for _, row := range rows {
		pw := multiLogWriter(w, row.Instance, &mu, prefix)
		if err := streamLocalLog(ctx, pw, row.path, false, tail, grep); err != nil {
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
	return streamLocalLog(ctx, w, row.path, false, opts.Tail, opts.Grep)
}

func streamLogRowOnceSince(ctx context.Context, w io.Writer, row logListRow, opts logsOptions) error {
	return streamLogRowOnce(ctx, w, row, opts)
}

func streamLocalLogRowsFollow(ctx context.Context, w io.Writer, rows []logListRow, tail int, prefix bool) error {
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
			if err := streamLocalLog(ctx, pw, row.path, true, tail, nil); err != nil {
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

func streamLocalLog(ctx context.Context, w io.Writer, path string, follow bool, tail int, grep *regexp.Regexp) error {
	if grep != nil && follow {
		return errors.New("logs: grep cannot be combined with follow")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if grep != nil {
		if err := copyGrepLinesLocal(w, f, tail, grep); err != nil {
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

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	buf := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
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

func copyGrepLinesLocal(w io.Writer, f *os.File, tail int, grep *regexp.Regexp) error {
	var r io.Reader = f
	if tail > 0 {
		body, err := readTailLinesLocal(f, tail)
		if err != nil {
			return err
		}
		r = bytes.NewReader(body)
	}
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadString('\n')
		if line != "" && grep.MatchString(lineForGrep(line)) {
			if _, werr := io.WriteString(w, line); werr != nil {
				return werr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
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
