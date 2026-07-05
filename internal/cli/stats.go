package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/runtimebin"
	"github.com/spf13/cobra"
)

func newStatsCmd() *cobra.Command {
	var (
		target           string
		all              bool
		latest           bool
		last             int
		watch            bool
		jsonOut          bool
		summary          bool
		noClear          bool
		format           string
		sortBy           string
		interval         time.Duration
		statusFilters    []string
		runtimeFilters   []string
		agentFilters     []string
		phaseFilters     []string
		instanceFilters  []string
		staleOnly        bool
		runtimeStaleOnly bool
		unhealthyOnly    bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "stats [<instance>...]",
		Aliases: []string{"top"},
		Short:   "Show CPU and memory usage for daemon-managed instances.",
		Long: "Show a one-shot or watchable resource snapshot for daemon-managed instances. " +
			"With no names, only running instances are shown. Use --all to include stopped, exited, and crashed instances.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team stats: --all cannot be combined with instance names.")
				return exitErr(2)
			}
			if latest && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team stats: --latest cannot be combined with instance names.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team stats: --last must be >= 0.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team stats: choose one of --latest or --last.")
				return exitErr(2)
			}
			if last > 0 && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team stats: --last cannot be combined with instance names.")
				return exitErr(2)
			}
			if len(args) > 0 && (len(statusFilters) > 0 || len(runtimeFilters) > 0 || len(agentFilters) > 0 || len(phaseFilters) > 0 || len(instanceFilters) > 0 || staleOnly || runtimeStaleOnly || unhealthyOnly) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team stats: --status, --runtime, --agent, --phase, --instance, --stale, --runtime-stale, and --unhealthy cannot be combined with instance names.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team stats: --interval must be >= 0.")
				return exitErr(2)
			}
			if format != "" && (jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team stats: --format cannot be combined with --json or --summary.")
				return exitErr(2)
			}
			formatTemplate, err := parseStatsFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team stats: %v\n", err)
				return exitErr(2)
			}
			opts, err := newStatsOptionsWithRuntimeInstancesPhasesAndUnhealthy(all, statusFilters, runtimeFilters, agentFilters, phaseFilters, instanceFilters, unhealthyOnly)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team stats: %v\n", err)
				return exitErr(2)
			}
			sortMode, err := parseStatsSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team stats: %v\n", err)
				return exitErr(2)
			}
			opts.Sort = sortMode
			opts.SortSet = cmd.Flags().Changed("sort")
			opts.Latest = latest
			opts.Limit = last
			opts.Stale = staleOnly
			opts.RuntimeStale = runtimeStaleOnly
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			opts.phaseByInstance = statsPhaseByInstance(teamDir, time.Now())
			opts.staleByInstance = staleInstanceSet(teamDir, time.Now())
			var lister instanceLister
			lister, err = newDaemonClient(teamDir)
			if err != nil {
				if errors.Is(err, errDaemonNotRunning) {
					lister = localInstanceLister{daemonRoot: daemon.DaemonRoot(teamDir)}
				} else {
					return err
				}
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				clear := !noClear && !jsonOut
				if summary {
					return runStatsSummaryWatchWithClear(ctx, cmd.OutOrStdout(), lister, args, opts, interval, time.Now, readProcessStats, jsonOut, clear)
				}
				if formatTemplate != nil {
					return runStatsFormatWatch(ctx, cmd.OutOrStdout(), lister, args, opts, interval, time.Now, readProcessStats, formatTemplate)
				}
				return runStatsWatchWithClear(ctx, cmd.OutOrStdout(), lister, args, opts, interval, time.Now, readProcessStats, jsonOut, clear)
			}
			if summary && jsonOut {
				return runStatsSummaryJSON(cmd.OutOrStdout(), lister, args, opts, time.Now(), readProcessStats)
			}
			if summary {
				return runStatsSummary(cmd.OutOrStdout(), lister, args, opts, time.Now(), readProcessStats)
			}
			if jsonOut {
				return runStatsJSON(cmd.OutOrStdout(), lister, args, opts, time.Now(), readProcessStats)
			}
			if formatTemplate != nil {
				return runStatsFormat(cmd.OutOrStdout(), lister, args, opts, time.Now(), readProcessStats, formatTemplate)
			}
			return runStats(cmd.OutOrStdout(), lister, args, opts, time.Now(), readProcessStats)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Include stopped, exited, and crashed daemon-managed instances.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Show stats for the most recently started instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Show stats for the N most recently started instances after other filters (0 = all).")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh stats until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit JSON. With --watch, writes one JSON array per refresh.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate CPU, memory, and RSS totals instead of instance rows.")
	cmd.Flags().StringVar(&format, "format", "", "Render each row with a Go template, e.g. '{{.Instance}} {{.CPUPercent}} {{.RSS}}'.")
	cmd.Flags().StringVar(&sortBy, "sort", "name", "Sort rows by name, cpu, mem, rss, status, agent, phase, stale, runtime-stale, or unhealthy.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only show lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only show instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&agentFilters, "agent", nil, "Only show instances for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only show instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&instanceFilters, "instance", nil, "Only show instances with this name. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only show instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&runtimeStaleOnly, "runtime-stale", false, "Only show running instances whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only show crashed, status-stale, or runtime-stale instances.")
	return cmd
}

type statsOptions struct {
	All          bool
	Latest       bool
	Limit        int
	Stale        bool
	RuntimeStale bool
	Unhealthy    bool
	Sort         statsSortMode
	SortSet      bool
	statuses     map[string]bool
	runtimes     map[string]bool
	agents       map[string]bool
	phases       map[string]bool
	instances    map[string]bool

	phaseByInstance map[string]string
	staleByInstance map[string]bool
}

type statsSortMode string

const (
	statsSortName         statsSortMode = "name"
	statsSortCPU          statsSortMode = "cpu"
	statsSortMemory       statsSortMode = "mem"
	statsSortRSS          statsSortMode = "rss"
	statsSortStatus       statsSortMode = "status"
	statsSortAgent        statsSortMode = "agent"
	statsSortPhase        statsSortMode = "phase"
	statsSortStale        statsSortMode = "stale"
	statsSortRuntimeStale statsSortMode = "runtime-stale"
	statsSortUnhealthy    statsSortMode = "unhealthy"
)

type processStats struct {
	CPUPercent    float64
	MemoryPercent float64
	RSSKiB        int64
}

type processStatsProbe func(pid int) (processStats, error)

type statsRow struct {
	Instance       string
	Agent          string
	Runtime        string
	RuntimeBinary  string
	Status         string
	Phase          string
	Stale          bool
	RuntimeStale   bool
	PID            int
	Age            string
	CPUPercent     float64
	MemoryPercent  float64
	RSSKiB         int64
	StatsAvailable bool
	Error          string
}

type statsJSONRow struct {
	Instance      string   `json:"instance"`
	Agent         string   `json:"agent"`
	Runtime       string   `json:"runtime,omitempty"`
	RuntimeBinary string   `json:"runtime_binary,omitempty"`
	Status        string   `json:"status"`
	Phase         string   `json:"phase,omitempty"`
	Stale         bool     `json:"stale,omitempty"`
	RuntimeStale  bool     `json:"runtime_stale,omitempty"`
	Unhealthy     bool     `json:"unhealthy,omitempty"`
	PID           int      `json:"pid,omitempty"`
	Age           string   `json:"age,omitempty"`
	CPUPercent    *float64 `json:"cpu_percent,omitempty"`
	MemoryPercent *float64 `json:"memory_percent,omitempty"`
	RSSBytes      int64    `json:"rss_bytes,omitempty"`
	RSS           string   `json:"rss,omitempty"`
	Error         string   `json:"error,omitempty"`
}

type statsFormatRow struct {
	Instance      string
	Agent         string
	Runtime       string
	RuntimeBinary string
	Status        string
	Phase         string
	Stale         bool
	RuntimeStale  bool
	Unhealthy     bool
	PID           int
	Age           string
	CPUPercent    string
	MemoryPercent string
	RSSBytes      int64
	RSS           string
	Error         string
	Measured      bool
}

type statsSummaryJSON struct {
	Total         int            `json:"total"`
	Running       int            `json:"running"`
	Stopped       int            `json:"stopped"`
	Exited        int            `json:"exited"`
	Crashed       int            `json:"crashed"`
	Unknown       int            `json:"unknown"`
	Measured      int            `json:"measured"`
	Errors        int            `json:"errors"`
	Stale         int            `json:"stale"`
	RuntimeStale  int            `json:"runtime_stale"`
	Unhealthy     int            `json:"unhealthy"`
	CPUPercent    float64        `json:"cpu_percent"`
	MemoryPercent float64        `json:"memory_percent"`
	RSSBytes      int64          `json:"rss_bytes"`
	RSS           string         `json:"rss"`
	Phases        map[string]int `json:"phases"`
}

type statsUnknownError struct {
	Instance string
}

func (e *statsUnknownError) Error() string {
	return fmt.Sprintf("unknown instance %q", e.Instance)
}

type localInstanceLister struct {
	daemonRoot string
}

func (l localInstanceLister) Instances() ([]*daemon.Metadata, error) {
	return daemon.ListMetadata(l.daemonRoot)
}

func newStatsOptions(all bool, statusFilters, agentFilters []string) (statsOptions, error) {
	return newStatsOptionsWithInstances(all, statusFilters, agentFilters, nil)
}

func newStatsOptionsWithInstances(all bool, statusFilters, agentFilters, instanceFilters []string) (statsOptions, error) {
	return newStatsOptionsWithInstancesAndPhases(all, statusFilters, agentFilters, nil, instanceFilters)
}

func newStatsOptionsWithInstancesAndPhases(all bool, statusFilters, agentFilters, phaseFilters, instanceFilters []string) (statsOptions, error) {
	return newStatsOptionsWithInstancesPhasesAndUnhealthy(all, statusFilters, agentFilters, phaseFilters, instanceFilters, false)
}

func newStatsOptionsWithInstancesPhasesAndUnhealthy(all bool, statusFilters, agentFilters, phaseFilters, instanceFilters []string, unhealthyOnly bool) (statsOptions, error) {
	return newStatsOptionsWithRuntimeInstancesPhasesAndUnhealthy(all, statusFilters, nil, agentFilters, phaseFilters, instanceFilters, unhealthyOnly)
}

func newStatsOptionsWithRuntimeInstancesPhasesAndUnhealthy(all bool, statusFilters, runtimeFilters, agentFilters, phaseFilters, instanceFilters []string, unhealthyOnly bool) (statsOptions, error) {
	opts := statsOptions{All: all, Unhealthy: unhealthyOnly, Sort: statsSortName}
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
			return opts, errors.New("--runtime requires at least one non-empty runtime")
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
			return opts, errors.New("--agent requires at least one non-empty agent")
		}
	}
	if len(phaseFilters) > 0 {
		phases, err := lifecyclePhaseFilterSet(phaseFilters)
		if err != nil {
			return opts, err
		}
		opts.phases = phases
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

func parseStatsSort(raw string) (statsSortMode, error) {
	return parseStatsSortFlag(raw, "--sort")
}

func parseStatsSortFlag(raw, flagName string) (statsSortMode, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "", "name", "instance":
		return statsSortName, nil
	case "cpu":
		return statsSortCPU, nil
	case "mem", "memory":
		return statsSortMemory, nil
	case "rss":
		return statsSortRSS, nil
	case "status":
		return statsSortStatus, nil
	case "agent":
		return statsSortAgent, nil
	case "phase":
		return statsSortPhase, nil
	case "stale":
		return statsSortStale, nil
	case "runtime-stale", "runtime_stale", "runtimestale":
		return statsSortRuntimeStale, nil
	case "unhealthy", "health":
		return statsSortUnhealthy, nil
	default:
		return "", fmt.Errorf("unknown %s %q (want name, cpu, mem, rss, status, agent, phase, stale, runtime-stale, or unhealthy)", flagName, raw)
	}
}

func runStats(w io.Writer, lister instanceLister, names []string, opts statsOptions, now time.Time, probe processStatsProbe) error {
	rows, err := collectStatsRows(lister, names, opts, now, probe)
	if err != nil {
		return err
	}
	empty := "(no running instances; use --all to include stopped/exited instances)"
	if opts.All || len(names) > 0 {
		empty = "(no instances)"
	}
	if statsOptionsHasFilters(opts) {
		empty = "(no matching instances)"
	}
	return renderStatsTable(w, rows, empty)
}

func runStatsJSON(w io.Writer, lister instanceLister, names []string, opts statsOptions, now time.Time, probe processStatsProbe) error {
	rows, err := collectStatsRows(lister, names, opts, now, probe)
	if err != nil {
		return err
	}
	return json.NewEncoder(w).Encode(statsJSONRows(rows))
}

func parseStatsFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("stats-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func runStatsFormat(w io.Writer, lister instanceLister, names []string, opts statsOptions, now time.Time, probe processStatsProbe, tmpl *template.Template) error {
	rows, err := collectStatsRows(lister, names, opts, now, probe)
	if err != nil {
		return err
	}
	return renderStatsFormat(w, rows, tmpl)
}

func runStatsSummary(w io.Writer, lister instanceLister, names []string, opts statsOptions, now time.Time, probe processStatsProbe) error {
	rows, err := collectStatsRows(lister, names, opts, now, probe)
	if err != nil {
		return err
	}
	return renderStatsSummary(w, summarizeStatsRows(rows))
}

func runStatsSummaryJSON(w io.Writer, lister instanceLister, names []string, opts statsOptions, now time.Time, probe processStatsProbe) error {
	rows, err := collectStatsRows(lister, names, opts, now, probe)
	if err != nil {
		return err
	}
	return json.NewEncoder(w).Encode(summarizeStatsRows(rows))
}

func renderStatsFormat(w io.Writer, rows []statsRow, tmpl *template.Template) error {
	for _, row := range statsFormatRows(rows) {
		if err := tmpl.Execute(w, row); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}

func runStatsWatch(ctx context.Context, w io.Writer, lister instanceLister, names []string, opts statsOptions, interval time.Duration, now func() time.Time, probe processStatsProbe, jsonOut bool) error {
	return runStatsWatchWithClear(ctx, w, lister, names, opts, interval, now, probe, jsonOut, false)
}

func runStatsWatchWithClear(ctx context.Context, w io.Writer, lister instanceLister, names []string, opts statsOptions, interval time.Duration, now func() time.Time, probe processStatsProbe, jsonOut bool, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if jsonOut {
			if err := runStatsJSON(w, lister, names, opts, now(), probe); err != nil {
				return err
			}
		} else {
			if err := writeWatchClear(w, clear); err != nil {
				return err
			}
			if err := runStats(w, lister, names, opts, now(), probe); err != nil {
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

func runStatsFormatWatch(ctx context.Context, w io.Writer, lister instanceLister, names []string, opts statsOptions, interval time.Duration, now func() time.Time, probe processStatsProbe, tmpl *template.Template) error {
	return runStatsFormatWatchWithClear(ctx, w, lister, names, opts, interval, now, probe, tmpl, false)
}

func runStatsFormatWatchWithClear(ctx context.Context, w io.Writer, lister instanceLister, names []string, opts statsOptions, interval time.Duration, now func() time.Time, probe processStatsProbe, tmpl *template.Template, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := writeWatchClear(w, clear); err != nil {
			return err
		}
		if err := runStatsFormat(w, lister, names, opts, now(), probe, tmpl); err != nil {
			return err
		}
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
	}
}

func runStatsSummaryWatch(ctx context.Context, w io.Writer, lister instanceLister, names []string, opts statsOptions, interval time.Duration, now func() time.Time, probe processStatsProbe, jsonOut bool) error {
	return runStatsSummaryWatchWithClear(ctx, w, lister, names, opts, interval, now, probe, jsonOut, false)
}

func runStatsSummaryWatchWithClear(ctx context.Context, w io.Writer, lister instanceLister, names []string, opts statsOptions, interval time.Duration, now func() time.Time, probe processStatsProbe, jsonOut bool, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if jsonOut {
			if err := runStatsSummaryJSON(w, lister, names, opts, now(), probe); err != nil {
				return err
			}
		} else {
			if err := writeWatchClear(w, clear); err != nil {
				return err
			}
			if err := runStatsSummary(w, lister, names, opts, now(), probe); err != nil {
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

func collectStatsRows(lister instanceLister, names []string, opts statsOptions, now time.Time, probe processStatsProbe) ([]statsRow, error) {
	if probe == nil {
		probe = readProcessStats
	}
	metas, err := lister.Instances()
	if err != nil {
		return nil, err
	}
	byName := map[string]*daemon.Metadata{}
	for _, meta := range metas {
		byName[meta.Instance] = meta
	}

	var selected []*daemon.Metadata
	if len(names) > 0 {
		seen := map[string]bool{}
		for _, name := range names {
			if seen[name] {
				continue
			}
			seen[name] = true
			meta, ok := byName[name]
			if !ok {
				return nil, &statsUnknownError{Instance: name}
			}
			selected = append(selected, meta)
		}
	} else {
		for _, meta := range metas {
			if statsOptionsMatchesMeta(opts, meta) {
				selected = append(selected, meta)
			}
		}
		sort.Slice(selected, func(i, j int) bool { return selected[i].Instance < selected[j].Instance })
	}
	if opts.Latest {
		selected = latestStatsMetadataLimit(selected, 1)
	} else if opts.Limit > 0 {
		selected = latestStatsMetadataLimit(selected, opts.Limit)
	}

	rows := make([]statsRow, 0, len(selected))
	for _, meta := range selected {
		row := statsRow{
			Instance:      meta.Instance,
			Agent:         meta.Agent,
			Runtime:       meta.Runtime,
			RuntimeBinary: meta.RuntimeBinary,
			Status:        metadataStatusKey(meta),
			Phase:         statsMetaPhase(opts, meta.Instance),
			Stale:         statsMetaStale(opts, meta.Instance),
			RuntimeStale:  runtimeResumeMetadataIsStale(meta),
			PID:           meta.PID,
			Age:           startedAge(meta.StartedAt, now),
		}
		if meta.Status == daemon.StatusRunning && meta.PID > 0 {
			stats, err := probe(meta.PID)
			if err != nil {
				row.Error = err.Error()
			} else {
				row.CPUPercent = stats.CPUPercent
				row.MemoryPercent = stats.MemoryPercent
				row.RSSKiB = stats.RSSKiB
				row.StatsAvailable = true
			}
		}
		rows = append(rows, row)
	}
	sortMode := opts.Sort
	if sortMode == "" {
		sortMode = statsSortName
	}
	if opts.Limit > 0 && !opts.SortSet && sortMode == statsSortName {
		return rows, nil
	}
	if len(names) > 0 && !opts.SortSet && sortMode == statsSortName {
		return rows, nil
	}
	sortStatsRows(rows, sortMode)
	return rows, nil
}

func latestStatsMetadata(metas []*daemon.Metadata) []*daemon.Metadata {
	return latestStatsMetadataLimit(metas, 1)
}

func latestStatsMetadataLimit(metas []*daemon.Metadata, limit int) []*daemon.Metadata {
	if limit <= 0 || len(metas) <= limit {
		return metas
	}
	out := append([]*daemon.Metadata(nil), metas...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.StartedAt.Equal(b.StartedAt) {
			return psTimeAfter(a.StartedAt, b.StartedAt)
		}
		return a.Instance < b.Instance
	})
	return out[:limit]
}

func sortStatsRows(rows []statsRow, mode statsSortMode) {
	if mode == "" {
		mode = statsSortName
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch mode {
		case statsSortCPU:
			if a.StatsAvailable != b.StatsAvailable {
				return a.StatsAvailable
			}
			if a.CPUPercent != b.CPUPercent {
				return a.CPUPercent > b.CPUPercent
			}
		case statsSortMemory:
			if a.StatsAvailable != b.StatsAvailable {
				return a.StatsAvailable
			}
			if a.MemoryPercent != b.MemoryPercent {
				return a.MemoryPercent > b.MemoryPercent
			}
		case statsSortRSS:
			if a.StatsAvailable != b.StatsAvailable {
				return a.StatsAvailable
			}
			if a.RSSKiB != b.RSSKiB {
				return a.RSSKiB > b.RSSKiB
			}
		case statsSortStatus:
			if a.Status != b.Status {
				return a.Status < b.Status
			}
		case statsSortAgent:
			if a.Agent != b.Agent {
				return a.Agent < b.Agent
			}
		case statsSortPhase:
			if statsPhaseKey(a.Phase) != statsPhaseKey(b.Phase) {
				return statsPhaseKey(a.Phase) < statsPhaseKey(b.Phase)
			}
		case statsSortStale:
			if a.Stale != b.Stale {
				return a.Stale
			}
		case statsSortRuntimeStale:
			if a.RuntimeStale != b.RuntimeStale {
				return a.RuntimeStale
			}
		case statsSortUnhealthy:
			if statsRowUnhealthy(a) != statsRowUnhealthy(b) {
				return statsRowUnhealthy(a)
			}
		}
		return a.Instance < b.Instance
	})
}

func statsRowUnhealthy(row statsRow) bool {
	return row.Status == string(daemon.StatusCrashed) || row.Stale || row.RuntimeStale
}

func statsOptionsMatchesMeta(opts statsOptions, meta *daemon.Metadata) bool {
	if meta == nil {
		return false
	}
	status := string(meta.Status)
	if status == "" {
		status = "unknown"
	}
	if len(opts.statuses) > 0 {
		if !opts.statuses[status] {
			return false
		}
	} else if !opts.All && !opts.Unhealthy && meta.Status != daemon.StatusRunning {
		return false
	}
	if len(opts.agents) > 0 && !opts.agents[meta.Agent] {
		return false
	}
	if len(opts.runtimes) > 0 && !opts.runtimes[statsMetaRuntimeKey(meta)] {
		return false
	}
	if len(opts.instances) > 0 && !opts.instances[meta.Instance] {
		return false
	}
	if len(opts.phases) > 0 && !opts.phases[statsMetaPhase(opts, meta.Instance)] {
		return false
	}
	if opts.Stale && !statsMetaStale(opts, meta.Instance) {
		return false
	}
	if opts.RuntimeStale && !runtimeResumeMetadataIsStale(meta) {
		return false
	}
	if opts.Unhealthy && !statsMetaUnhealthy(opts, meta) {
		return false
	}
	return true
}

func statsOptionsHasFilters(opts statsOptions) bool {
	return len(opts.statuses) > 0 || len(opts.runtimes) > 0 || len(opts.agents) > 0 || len(opts.phases) > 0 || len(opts.instances) > 0 || opts.Stale || opts.RuntimeStale || opts.Unhealthy
}

func statsMetaRuntimeKey(meta *daemon.Metadata) string {
	if meta == nil {
		return "unknown"
	}
	runtime := strings.ToLower(strings.TrimSpace(meta.Runtime))
	if runtime == "" {
		return "unknown"
	}
	return runtime
}

func statsPhaseByInstance(teamDir string, now time.Time) map[string]string {
	return statusPhaseByInstance(teamDir, now)
}

func statsMetaPhase(opts statsOptions, instance string) string {
	if opts.phaseByInstance == nil {
		return ""
	}
	return statsPhaseKey(opts.phaseByInstance[instance])
}

func statsMetaStale(opts statsOptions, instance string) bool {
	if opts.staleByInstance == nil {
		return false
	}
	return opts.staleByInstance[instance]
}

func statsMetaUnhealthy(opts statsOptions, meta *daemon.Metadata) bool {
	return metadataStatusKey(meta) == string(daemon.StatusCrashed) || statsMetaStale(opts, meta.Instance) || runtimeResumeMetadataIsStale(meta)
}

func statsPhaseKey(raw string) string {
	phase := strings.ToLower(strings.TrimSpace(raw))
	switch phase {
	case "", "—", "?":
		return "unknown"
	default:
		return phase
	}
}

func startedAge(startedAt time.Time, now time.Time) string {
	if startedAt.IsZero() {
		return "—"
	}
	return formatAge(now.Sub(startedAt))
}

func renderStatsTable(w io.Writer, rows []statsRow, empty string) error {
	if len(rows) == 0 {
		fmt.Fprintln(w, empty)
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "INSTANCE\tAGENT\tSTATUS\tPHASE\tSTALE\tRUNTIME_STALE\tPID\tCPU%\tMEM%\tRSS\tAGE")
	for _, row := range rows {
		pid := "—"
		if row.PID > 0 {
			pid = strconv.Itoa(row.PID)
		}
		cpu, mem, rss := "—", "—", "—"
		if row.StatsAvailable {
			cpu = fmt.Sprintf("%.1f", row.CPUPercent)
			mem = fmt.Sprintf("%.1f", row.MemoryPercent)
			rss = formatRSS(row.RSSKiB)
		}
		stale := "—"
		if row.Stale {
			stale = "yes"
		}
		runtimeStale := "—"
		if row.RuntimeStale {
			runtimeStale = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.Instance, row.Agent, row.Status, statsPhaseKey(row.Phase), stale, runtimeStale, pid, cpu, mem, rss, row.Age)
	}
	return tw.Flush()
}

func summarizeStatsRows(rows []statsRow) statsSummaryJSON {
	out := statsSummaryJSON{Phases: map[string]int{}}
	out.Total = len(rows)
	for _, row := range rows {
		switch row.Status {
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
		if row.Error != "" {
			out.Errors++
		}
		if row.Stale {
			out.Stale++
		}
		if row.RuntimeStale {
			out.RuntimeStale++
		}
		if statsRowUnhealthy(row) {
			out.Unhealthy++
		}
		if row.StatsAvailable {
			out.Measured++
			out.CPUPercent += row.CPUPercent
			out.MemoryPercent += row.MemoryPercent
			out.RSSBytes += row.RSSKiB * 1024
		}
		out.Phases[statsPhaseKey(row.Phase)]++
	}
	out.RSS = formatRSS(out.RSSBytes / 1024)
	return out
}

func renderStatsSummary(w io.Writer, summary statsSummaryJSON) error {
	fmt.Fprintf(w,
		"instances: total=%d running=%d stopped=%d exited=%d crashed=%d unknown=%d stale=%d runtime_stale=%d unhealthy=%d measured=%d errors=%d cpu=%.1f%% mem=%.1f%% rss=%s\n",
		summary.Total,
		summary.Running,
		summary.Stopped,
		summary.Exited,
		summary.Crashed,
		summary.Unknown,
		summary.Stale,
		summary.RuntimeStale,
		summary.Unhealthy,
		summary.Measured,
		summary.Errors,
		summary.CPUPercent,
		summary.MemoryPercent,
		summary.RSS,
	)
	fmt.Fprint(w, "phases:")
	for _, phase := range lifecyclePhaseSummaryOrder() {
		fmt.Fprintf(w, " %s=%d", phase, summary.Phases[phase])
	}
	fmt.Fprintln(w)
	return nil
}

func statsJSONRows(rows []statsRow) []statsJSONRow {
	out := make([]statsJSONRow, 0, len(rows))
	for _, row := range rows {
		body := statsJSONRow{
			Instance:      row.Instance,
			Agent:         row.Agent,
			Runtime:       row.Runtime,
			RuntimeBinary: row.RuntimeBinary,
			Status:        row.Status,
			Phase:         row.Phase,
			Stale:         row.Stale,
			RuntimeStale:  row.RuntimeStale,
			Unhealthy:     statsRowUnhealthy(row),
			PID:           row.PID,
			Age:           row.Age,
			Error:         row.Error,
		}
		if row.StatsAvailable {
			cpu := row.CPUPercent
			mem := row.MemoryPercent
			body.CPUPercent = &cpu
			body.MemoryPercent = &mem
			body.RSSBytes = row.RSSKiB * 1024
			body.RSS = formatRSS(row.RSSKiB)
		}
		out = append(out, body)
	}
	return out
}

func statsFormatRows(rows []statsRow) []statsFormatRow {
	out := make([]statsFormatRow, 0, len(rows))
	for _, row := range rows {
		body := statsFormatRow{
			Instance:      row.Instance,
			Agent:         row.Agent,
			Runtime:       row.Runtime,
			RuntimeBinary: row.RuntimeBinary,
			Status:        row.Status,
			Phase:         row.Phase,
			Stale:         row.Stale,
			RuntimeStale:  row.RuntimeStale,
			Unhealthy:     statsRowUnhealthy(row),
			PID:           row.PID,
			Age:           row.Age,
			Error:         row.Error,
		}
		if row.StatsAvailable {
			body.CPUPercent = fmt.Sprintf("%.1f", row.CPUPercent)
			body.MemoryPercent = fmt.Sprintf("%.1f", row.MemoryPercent)
			body.RSSBytes = row.RSSKiB * 1024
			body.RSS = formatRSS(row.RSSKiB)
			body.Measured = true
		}
		out = append(out, body)
	}
	return out
}

func formatRSS(kib int64) string {
	if kib <= 0 {
		return "0B"
	}
	mib := float64(kib) / 1024
	if mib < 1024 {
		return fmt.Sprintf("%.1fMiB", mib)
	}
	return fmt.Sprintf("%.1fGiB", mib/1024)
}

func readProcessStats(pid int) (processStats, error) {
	if pid <= 0 {
		return processStats{}, fmt.Errorf("pid must be > 0")
	}
	out, err := exec.Command("ps",
		"-p", strconv.Itoa(pid),
		"-o", "pid=",
		"-o", "pcpu=",
		"-o", "pmem=",
		"-o", "rss=",
	).Output()
	if err != nil {
		return processStats{}, fmt.Errorf("ps pid %d: %w", pid, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) < 4 {
		return processStats{}, fmt.Errorf("ps pid %d: no stats", pid)
	}
	gotPID, err := strconv.Atoi(fields[0])
	if err != nil {
		return processStats{}, fmt.Errorf("ps pid %d: parse pid %q: %w", pid, fields[0], err)
	}
	if gotPID != pid {
		return processStats{}, fmt.Errorf("ps pid %d: got pid %d", pid, gotPID)
	}
	cpu, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return processStats{}, fmt.Errorf("ps pid %d: parse cpu %q: %w", pid, fields[1], err)
	}
	mem, err := strconv.ParseFloat(fields[2], 64)
	if err != nil {
		return processStats{}, fmt.Errorf("ps pid %d: parse mem %q: %w", pid, fields[2], err)
	}
	rss, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil {
		return processStats{}, fmt.Errorf("ps pid %d: parse rss %q: %w", pid, fields[3], err)
	}
	return processStats{CPUPercent: cpu, MemoryPercent: mem, RSSKiB: rss}, nil
}
