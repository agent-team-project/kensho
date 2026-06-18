package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/spf13/cobra"
)

// newPsCmd builds `agent-team ps`. It's the daemon-aware single-source view:
// when the daemon is running, every running/stopped/exited instance the
// daemon knows about is listed; entries with a status.toml on disk are
// folded in, so an instance that emitted status without ever being dispatched
// via the daemon (the SQU-25 path) still appears.
//
// When the daemon is not running, this command degrades to the on-disk state
// walk plus persisted daemon metadata from `.agent_team/daemon/<instance>/`.
func newPsCmd() *cobra.Command {
	var (
		target          string
		watch           bool
		jsonOut         bool
		quiet           bool
		summary         bool
		all             bool
		staleOnly       bool
		unhealthyOnly   bool
		latest          bool
		last            int
		noClear         bool
		format          string
		sortBy          string
		interval        time.Duration
		statusFilters   []string
		agentFilters    []string
		phaseFilters    []string
		instanceFilters []string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "ps",
		Aliases: []string{"ls"},
		Short:   "List instances (daemon-aware: merges live daemon state with on-disk status).",
		Long: "Daemon-aware single-source view of instances. With the daemon " +
			"running, lifecycle status (running/stopped/exited/crashed) comes " +
			"from /v1/instances; phase / summary come from each instance's " +
			"on-disk status.toml. Without a daemon, it merges on-disk status " +
			"files with persisted runtime metadata from .agent_team/daemon. " +
			"Unlike Docker, this command already shows every visible instance " +
			"by default; --all is accepted for Docker-compatible muscle memory.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team ps: --interval must be >= 0.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team ps: --last must be >= 0.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team ps: choose one of --latest or --last.")
				return exitErr(2)
			}
			opts, err := newPsOptionsWithInstancesAndUnhealthy(statusFilters, agentFilters, phaseFilters, instanceFilters, staleOnly, unhealthyOnly)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team ps: %v\n", err)
				return exitErr(2)
			}
			sortMode, err := parsePsSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team ps: %v\n", err)
				return exitErr(2)
			}
			opts.Sort = sortMode
			opts.SortSet = cmd.Flags().Changed("sort")
			opts.Limit = last
			if latest {
				opts.Limit = 1
			}
			if quiet && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team ps: choose one of --quiet or --json.")
				return exitErr(2)
			}
			if quiet && watch {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team ps: --quiet cannot be combined with --watch.")
				return exitErr(2)
			}
			if quiet && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team ps: --quiet cannot be combined with --summary.")
				return exitErr(2)
			}
			if format != "" && (quiet || jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team ps: --format cannot be combined with --quiet, --json, or --summary.")
				return exitErr(2)
			}
			formatTemplate, err := parsePsFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team ps: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			if quiet {
				return runPsQuiet(cmd.OutOrStdout(), teamDir, time.Now(), opts)
			}
			if summary && !watch && jsonOut {
				return runPsSummaryJSON(cmd.OutOrStdout(), teamDir, time.Now(), opts)
			}
			if summary && !watch {
				return runPsSummary(cmd.OutOrStdout(), teamDir, time.Now(), opts)
			}
			if !watch && jsonOut {
				return runPsJSONWithOptions(cmd.OutOrStdout(), teamDir, time.Now(), opts)
			}
			if !watch && formatTemplate != nil {
				return runPsFormatWithOptions(cmd.OutOrStdout(), teamDir, time.Now(), opts, formatTemplate)
			}
			if !watch {
				return runPsWithOptions(cmd.OutOrStdout(), teamDir, time.Now(), opts)
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			clear := !noClear && !jsonOut
			if summary {
				return runPsSummaryWatchWithClear(ctx, cmd.OutOrStdout(), teamDir, interval, time.Now, jsonOut, opts, clear)
			}
			if formatTemplate != nil {
				return runPsFormatWatch(ctx, cmd.OutOrStdout(), teamDir, interval, time.Now, opts, formatTemplate)
			}
			return runPsWatchFiltered(ctx, cmd.OutOrStdout(), teamDir, interval, time.Now, jsonOut, opts, clear)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the process table until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit JSON. With --watch, writes one JSON array per refresh.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Only print matching instance names.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show lifecycle counts instead of instance rows.")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Show all visible instances. Accepted for Docker compatibility; this is already the default.")
	cmd.Flags().BoolVarP(&latest, "latest", "l", false, "Show only the most recently started instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Show only the N most recently started instances after other filters (0 = all).")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only show instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only show crashed or stale instances.")
	cmd.Flags().StringVar(&format, "format", "", "Render each row with a Go template, e.g. '{{.Instance}} {{.Status}}'.")
	cmd.Flags().StringVar(&sortBy, "sort", "name", "Sort rows by name, status, agent, phase, stale, unhealthy, started, stopped, or exited.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only show lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&agentFilters, "agent", nil, "Only show instances for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only show work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&instanceFilters, "instance", nil, "Only show instances with this name. Can repeat or comma-separate.")
	return cmd
}

func runPsWatch(ctx context.Context, w io.Writer, teamDir string, interval time.Duration, now func() time.Time) error {
	return runPsWatchWithOptions(ctx, w, teamDir, interval, now, false)
}

func runPsWatchWithOptions(ctx context.Context, w io.Writer, teamDir string, interval time.Duration, now func() time.Time, jsonOut bool) error {
	return runPsWatchFiltered(ctx, w, teamDir, interval, now, jsonOut, psOptions{}, false)
}

func runPsWatchFiltered(ctx context.Context, w io.Writer, teamDir string, interval time.Duration, now func() time.Time, jsonOut bool, opts psOptions, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if jsonOut {
			if err := runPsJSONWithOptions(w, teamDir, now(), opts); err != nil {
				return err
			}
		} else {
			if err := writeWatchClear(w, clear); err != nil {
				return err
			}
			if err := runPsWithOptions(w, teamDir, now(), opts); err != nil {
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

func runPs(w io.Writer, teamDir string, now time.Time) error {
	return runPsWithOptions(w, teamDir, now, psOptions{})
}

func runPsWithOptions(w io.Writer, teamDir string, now time.Time, opts psOptions) error {
	rows, err := collectFilteredPsRows(teamDir, now, opts)
	if err != nil {
		return err
	}
	return renderPsTable(w, rows)
}

func parsePsFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("ps-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func runPsFormatWithOptions(w io.Writer, teamDir string, now time.Time, opts psOptions, tmpl *template.Template) error {
	rows, err := collectFilteredPsRows(teamDir, now, opts)
	if err != nil {
		return err
	}
	return renderPsFormat(w, rows, tmpl)
}

func renderPsFormat(w io.Writer, rows []instanceRow, tmpl *template.Template) error {
	for _, row := range psJSONRows(rows) {
		if err := tmpl.Execute(w, row); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}

func runPsJSON(w io.Writer, teamDir string, now time.Time) error {
	return runPsJSONWithOptions(w, teamDir, now, psOptions{})
}

func runPsJSONWithOptions(w io.Writer, teamDir string, now time.Time, opts psOptions) error {
	rows, err := collectFilteredPsRows(teamDir, now, opts)
	if err != nil {
		return err
	}
	return json.NewEncoder(w).Encode(psJSONRows(rows))
}

func runPsQuiet(w io.Writer, teamDir string, now time.Time, opts psOptions) error {
	rows, err := collectFilteredPsRows(teamDir, now, opts)
	if err != nil {
		return err
	}
	for _, r := range rows {
		fmt.Fprintln(w, r.Instance)
	}
	return nil
}

func runPsSummary(w io.Writer, teamDir string, now time.Time, opts psOptions) error {
	rows, err := collectFilteredPsRows(teamDir, now, opts)
	if err != nil {
		return err
	}
	return renderPsSummary(w, psSummaryRows(rows))
}

func runPsSummaryJSON(w io.Writer, teamDir string, now time.Time, opts psOptions) error {
	rows, err := collectFilteredPsRows(teamDir, now, opts)
	if err != nil {
		return err
	}
	return json.NewEncoder(w).Encode(psSummaryRows(rows))
}

func runPsSummaryWatch(ctx context.Context, w io.Writer, teamDir string, interval time.Duration, now func() time.Time, jsonOut bool, opts psOptions) error {
	return runPsSummaryWatchWithClear(ctx, w, teamDir, interval, now, jsonOut, opts, false)
}

func runPsSummaryWatchWithClear(ctx context.Context, w io.Writer, teamDir string, interval time.Duration, now func() time.Time, jsonOut bool, opts psOptions, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if jsonOut {
			if err := runPsSummaryJSON(w, teamDir, now(), opts); err != nil {
				return err
			}
		} else {
			if err := writeWatchClear(w, clear); err != nil {
				return err
			}
			if err := runPsSummary(w, teamDir, now(), opts); err != nil {
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

func runPsFormatWatch(ctx context.Context, w io.Writer, teamDir string, interval time.Duration, now func() time.Time, opts psOptions, tmpl *template.Template) error {
	return runPsFormatWatchWithClear(ctx, w, teamDir, interval, now, opts, tmpl, false)
}

func runPsFormatWatchWithClear(ctx context.Context, w io.Writer, teamDir string, interval time.Duration, now func() time.Time, opts psOptions, tmpl *template.Template, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := writeWatchClear(w, clear); err != nil {
			return err
		}
		if err := runPsFormatWithOptions(w, teamDir, now(), opts, tmpl); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

type psSummaryJSON struct {
	Total     int            `json:"total"`
	Running   int            `json:"running"`
	Stopped   int            `json:"stopped"`
	Exited    int            `json:"exited"`
	Crashed   int            `json:"crashed"`
	Unknown   int            `json:"unknown"`
	Stale     int            `json:"stale"`
	HasStatus int            `json:"has_status"`
	Phases    map[string]int `json:"phases"`
}

func psSummaryRows(rows []instanceRow) psSummaryJSON {
	out := psSummaryJSON{Phases: map[string]int{}}
	out.Total = len(rows)
	for _, row := range rows {
		switch psStatusKey(row) {
		case string(daemon.StatusRunning):
			out.Running++
		case string(daemon.StatusStopped):
			out.Stopped++
		case string(daemon.StatusExited):
			out.Exited++
		case string(daemon.StatusCrashed):
			out.Crashed++
		default:
			out.Unknown++
		}
		if row.Stale {
			out.Stale++
		}
		if row.HasFile {
			out.HasStatus++
		}
		out.Phases[psPhaseKey(row)]++
	}
	return out
}

func renderPsSummary(w io.Writer, summary psSummaryJSON) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tCOUNT")
	fmt.Fprintf(tw, "running\t%d\n", summary.Running)
	fmt.Fprintf(tw, "stopped\t%d\n", summary.Stopped)
	fmt.Fprintf(tw, "exited\t%d\n", summary.Exited)
	fmt.Fprintf(tw, "crashed\t%d\n", summary.Crashed)
	fmt.Fprintf(tw, "unknown\t%d\n", summary.Unknown)
	fmt.Fprintf(tw, "stale\t%d\n", summary.Stale)
	fmt.Fprintf(tw, "has_status\t%d\n", summary.HasStatus)
	fmt.Fprintf(tw, "total\t%d\n", summary.Total)
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(w)
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PHASE\tCOUNT")
	for _, phase := range psSummaryPhaseOrder() {
		fmt.Fprintf(tw, "%s\t%d\n", phase, summary.Phases[phase])
	}
	return tw.Flush()
}

func psSummaryPhaseOrder() []string {
	return lifecyclePhaseSummaryOrder()
}

func lifecyclePhaseSummaryOrder() []string {
	return []string{"planning", "implementing", "awaiting_review", "blocked", "idle", "done", "unknown"}
}

type psOptions struct {
	Sort      psSortMode
	SortSet   bool
	Limit     int
	statuses  map[string]bool
	agents    map[string]bool
	phases    map[string]bool
	instances map[string]bool
	stale     bool
	unhealthy bool
}

type psSortMode string

const (
	psSortName      psSortMode = "name"
	psSortStatus    psSortMode = "status"
	psSortAgent     psSortMode = "agent"
	psSortPhase     psSortMode = "phase"
	psSortStale     psSortMode = "stale"
	psSortUnhealthy psSortMode = "unhealthy"
	psSortStarted   psSortMode = "started"
	psSortStopped   psSortMode = "stopped"
	psSortExited    psSortMode = "exited"
)

func parsePsSort(raw string) (psSortMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "name", "instance":
		return psSortName, nil
	case "status":
		return psSortStatus, nil
	case "agent":
		return psSortAgent, nil
	case "phase":
		return psSortPhase, nil
	case "stale":
		return psSortStale, nil
	case "unhealthy", "health":
		return psSortUnhealthy, nil
	case "started", "start", "created":
		return psSortStarted, nil
	case "stopped", "stop":
		return psSortStopped, nil
	case "exited", "exit":
		return psSortExited, nil
	default:
		return "", fmt.Errorf("unknown --sort %q (want name, status, agent, phase, stale, unhealthy, started, stopped, or exited)", raw)
	}
}

func newPsOptions(statusFilters, agentFilters, phaseFilters []string, staleOnly bool) (psOptions, error) {
	return newPsOptionsWithInstances(statusFilters, agentFilters, phaseFilters, nil, staleOnly)
}

func newPsOptionsWithInstances(statusFilters, agentFilters, phaseFilters, instanceFilters []string, staleOnly bool) (psOptions, error) {
	return newPsOptionsWithInstancesAndUnhealthy(statusFilters, agentFilters, phaseFilters, instanceFilters, staleOnly, false)
}

func newPsOptionsWithInstancesAndUnhealthy(statusFilters, agentFilters, phaseFilters, instanceFilters []string, staleOnly, unhealthyOnly bool) (psOptions, error) {
	opts := psOptions{stale: staleOnly, unhealthy: unhealthyOnly}
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
			return opts, errors.New("--status requires at least one non-empty status")
		}
	}
	if len(phaseFilters) > 0 {
		phases, err := lifecyclePhaseFilterSet(phaseFilters)
		if err != nil {
			return opts, err
		}
		opts.phases = phases
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
			return opts, errors.New("--agent requires at least one non-empty agent")
		}
	}
	if len(instanceFilters) > 0 {
		opts.instances = map[string]bool{}
		for _, raw := range splitFilterValues(instanceFilters) {
			instance := strings.TrimSpace(raw)
			if instance != "" {
				opts.instances[instance] = true
			}
		}
		if len(opts.instances) == 0 {
			return opts, errors.New("--instance requires at least one non-empty instance")
		}
	}
	return opts, nil
}

func lifecyclePhaseFilterSet(phaseFilters []string) (map[string]bool, error) {
	if len(phaseFilters) == 0 {
		return nil, nil
	}
	return lifecyclePhaseFilterSetForFlag("--phase", phaseFilters)
}

func lifecyclePhaseFilterSetForFlag(flag string, phaseFilters []string) (map[string]bool, error) {
	phases := map[string]bool{}
	for _, raw := range splitFilterValues(phaseFilters) {
		phase := strings.ToLower(strings.TrimSpace(raw))
		if phase == "" {
			continue
		}
		switch phase {
		case "planning", "implementing", "awaiting_review", "blocked", "idle", "done", "unknown":
			phases[phase] = true
		default:
			return nil, fmt.Errorf("unknown %s %q (want planning, implementing, awaiting_review, blocked, idle, done, or unknown)", flag, raw)
		}
	}
	if len(phases) == 0 {
		return nil, fmt.Errorf("%s requires at least one non-empty phase", flag)
	}
	return phases, nil
}

func splitFilterValues(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		for _, part := range strings.Split(item, ",") {
			out = append(out, part)
		}
	}
	return out
}

func filterPsRows(rows []instanceRow, opts psOptions) []instanceRow {
	if len(opts.statuses) == 0 && len(opts.agents) == 0 && len(opts.phases) == 0 && len(opts.instances) == 0 && !opts.stale && !opts.unhealthy {
		return rows
	}
	out := make([]instanceRow, 0, len(rows))
	for _, r := range rows {
		if opts.stale && !r.Stale {
			continue
		}
		if opts.unhealthy && !psRowUnhealthy(r) {
			continue
		}
		if len(opts.instances) > 0 && !opts.instances[r.Instance] {
			continue
		}
		if len(opts.statuses) > 0 && !opts.statuses[psStatusKey(r)] {
			continue
		}
		if len(opts.agents) > 0 && !opts.agents[r.Agent] {
			continue
		}
		if len(opts.phases) > 0 && !opts.phases[psPhaseKey(r)] {
			continue
		}
		out = append(out, r)
	}
	return out
}

func psRowUnhealthy(row instanceRow) bool {
	return psStatusKey(row) == string(daemon.StatusCrashed) || row.Stale
}

func collectFilteredPsRows(teamDir string, now time.Time, opts psOptions) ([]instanceRow, error) {
	rows, err := collectPsRows(teamDir, now)
	if err != nil {
		return nil, err
	}
	return filterLimitSortPsRows(rows, opts), nil
}

func filterLimitSortPsRows(rows []instanceRow, opts psOptions) []instanceRow {
	rows = filterPsRows(rows, opts)
	rows = limitPsRowsByLatestStarted(rows, opts.Limit)
	sortMode := opts.Sort
	if opts.Limit > 0 && !opts.SortSet {
		sortMode = psSortStarted
	}
	sortPsRows(rows, sortMode)
	return rows
}

func limitPsRowsByLatestStarted(rows []instanceRow, limit int) []instanceRow {
	if limit <= 0 || len(rows) <= limit {
		return rows
	}
	out := append([]instanceRow(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.StartedAt.Equal(b.StartedAt) {
			return psTimeAfter(a.StartedAt, b.StartedAt)
		}
		return a.Instance < b.Instance
	})
	return out[:limit]
}

func sortPsRows(rows []instanceRow, mode psSortMode) {
	if mode == "" {
		mode = psSortName
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch mode {
		case psSortStatus:
			if psStatusKey(a) != psStatusKey(b) {
				return psStatusKey(a) < psStatusKey(b)
			}
		case psSortAgent:
			if a.Agent != b.Agent {
				return a.Agent < b.Agent
			}
		case psSortPhase:
			if psPhaseKey(a) != psPhaseKey(b) {
				return psPhaseKey(a) < psPhaseKey(b)
			}
		case psSortStale:
			if a.Stale != b.Stale {
				return a.Stale
			}
		case psSortUnhealthy:
			if psRowUnhealthy(a) != psRowUnhealthy(b) {
				return psRowUnhealthy(a)
			}
		case psSortStarted:
			if !a.StartedAt.Equal(b.StartedAt) {
				return psTimeAfter(a.StartedAt, b.StartedAt)
			}
		case psSortStopped:
			if !a.StoppedAt.Equal(b.StoppedAt) {
				return psTimeAfter(a.StoppedAt, b.StoppedAt)
			}
		case psSortExited:
			if !a.ExitedAt.Equal(b.ExitedAt) {
				return psTimeAfter(a.ExitedAt, b.ExitedAt)
			}
		}
		return a.Instance < b.Instance
	})
}

func psTimeAfter(a, b time.Time) bool {
	if a.IsZero() {
		return false
	}
	if b.IsZero() {
		return true
	}
	return a.After(b)
}

func psStatusKey(r instanceRow) string {
	if r.Lifecycle == "" {
		return "unknown"
	}
	return r.Lifecycle
}

func psPhaseKey(r instanceRow) string {
	phase := strings.ToLower(strings.TrimSpace(r.Phase))
	switch phase {
	case "", "—", "?":
		return "unknown"
	default:
		return phase
	}
}

func collectPsRows(teamDir string, now time.Time) ([]instanceRow, error) {
	agentNames := loadAgentNames(teamDir)
	rows := loadInstanceRows(teamDir, agentNames, now)

	// Try the daemon. errDaemonNotRunning → fall back silently to the
	// persisted metadata view. Other errors are surfaced (something is broken
	// with the daemon — better to know than to hide).
	client, err := newDaemonClient(teamDir)
	switch {
	case err == nil:
		insts, err := client.Instances()
		if err != nil {
			return nil, err
		}
		rows = mergeDaemonRows(rows, insts, agentNames)
	case errors.Is(err, errDaemonNotRunning):
		insts, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
		if err != nil {
			return nil, err
		}
		rows = mergeDaemonRows(rows, insts, agentNames)
	default:
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Instance < rows[j].Instance })
	return rows, nil
}

func mergeDaemonRows(rows []instanceRow, insts []*daemon.Metadata, agentNames map[string]bool) []instanceRow {
	rowByInstance := map[string]int{}
	for i := range rows {
		rowByInstance[rows[i].Instance] = i
	}
	for _, m := range insts {
		idx, ok := rowByInstance[m.Instance]
		if !ok {
			newRow := newRowFromMeta(m, agentNames)
			rows = append(rows, newRow)
			rowByInstance[newRow.Instance] = len(rows) - 1
			continue
		}
		if m.Agent != "" {
			rows[idx].Agent = m.Agent
		}
		rows[idx].Lifecycle = metadataStatusKey(m)
		rows[idx].Job = firstNonEmpty(rows[idx].Job, m.Job)
		rows[idx].Ticket = firstNonEmpty(rows[idx].Ticket, m.Ticket)
		rows[idx].Branch = firstNonEmpty(rows[idx].Branch, m.Branch)
		rows[idx].PR = firstNonEmpty(rows[idx].PR, m.PR)
		rows[idx].Workspace = m.Workspace
		rows[idx].PID = m.PID
		rows[idx].StartedAt = m.StartedAt
		rows[idx].StoppedAt = m.StoppedAt
		rows[idx].ExitedAt = m.ExitedAt
	}
	return rows
}

func renderPsTable(w io.Writer, rows []instanceRow) error {
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no instances)")
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "INSTANCE\tAGENT\tJOB\tSTATUS\tPHASE\tPID\tAGE\tSUMMARY")
	for _, r := range rows {
		phase := r.Phase
		if r.Stale {
			phase = phase + " (stale)"
		}
		life := r.Lifecycle
		if life == "" {
			life = "—"
		}
		pid := "—"
		if r.PID > 0 {
			pid = strconv.Itoa(r.PID)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Instance, r.Agent, emptyDash(r.Job), life, phase, pid, r.Age, r.Summary)
	}
	return tw.Flush()
}

type psJSONRow struct {
	Instance  string `json:"instance"`
	Agent     string `json:"agent"`
	Status    string `json:"status"`
	Phase     string `json:"phase"`
	Age       string `json:"age"`
	Summary   string `json:"summary,omitempty"`
	Job       string `json:"job,omitempty"`
	Ticket    string `json:"ticket,omitempty"`
	Branch    string `json:"branch,omitempty"`
	PR        string `json:"pr,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Stale     bool   `json:"stale"`
	HasStatus bool   `json:"has_status"`
	PID       int    `json:"pid,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
	StoppedAt string `json:"stopped_at,omitempty"`
	ExitedAt  string `json:"exited_at,omitempty"`
}

func psJSONRows(rows []instanceRow) []psJSONRow {
	out := make([]psJSONRow, 0, len(rows))
	for _, r := range rows {
		row := psJSONRow{
			Instance:  r.Instance,
			Agent:     r.Agent,
			Status:    psStatusKey(r),
			Phase:     psPhaseKey(r),
			Age:       r.Age,
			Summary:   r.Summary,
			Job:       r.Job,
			Ticket:    r.Ticket,
			Branch:    r.Branch,
			PR:        r.PR,
			Workspace: filepath.ToSlash(r.Workspace),
			Stale:     r.Stale,
			HasStatus: r.HasFile,
			PID:       r.PID,
		}
		if !r.StartedAt.IsZero() {
			row.StartedAt = r.StartedAt.UTC().Format(time.RFC3339)
		}
		if !r.StoppedAt.IsZero() {
			row.StoppedAt = r.StoppedAt.UTC().Format(time.RFC3339)
		}
		if !r.ExitedAt.IsZero() {
			row.ExitedAt = r.ExitedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, row)
	}
	return out
}

// newRowFromMeta builds a row for an instance the daemon knows about but
// which has no state dir / status.toml on disk yet. Phase shows `—` until
// the instance starts emitting status.
func newRowFromMeta(m *daemon.Metadata, agentNames map[string]bool) instanceRow {
	agent := m.Agent
	if !agentNames[agent] {
		// Best-effort: if the agent name isn't recognised, fall back to "—".
		agent = guessAgentName(m.Instance, agentNames)
	}
	return instanceRow{
		Instance:  m.Instance,
		Agent:     agent,
		Phase:     "—",
		Age:       "—",
		Lifecycle: metadataStatusKey(m),
		Job:       m.Job,
		Ticket:    m.Ticket,
		Branch:    m.Branch,
		PR:        m.PR,
		Workspace: m.Workspace,
		PID:       m.PID,
		StartedAt: m.StartedAt,
		StoppedAt: m.StoppedAt,
		ExitedAt:  m.ExitedAt,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
