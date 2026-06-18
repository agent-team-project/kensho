package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newMonitorCmd() *cobra.Command {
	var (
		target          string
		all             bool
		watch           bool
		plan            bool
		jobs            bool
		schedules       bool
		stopExtras      bool
		summary         bool
		resources       bool
		jsonOut         bool
		noClear         bool
		latest          bool
		last            int
		format          string
		sortBy          string
		statsSortBy     string
		staleOnly       bool
		unhealthyOnly   bool
		eventTail       int
		eventSince      string
		interval        time.Duration
		statusFilters   []string
		agentFilters    []string
		phaseFilters    []string
		instanceFilters []string
		actionFilters   []string
		eventActions    []string
		strictTopology  bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "monitor",
		Short: "Show a combined health, instance, and resource snapshot.",
		Long: "Show a Docker-style operator snapshot combining fleet health, the instance table, " +
			"and daemon-managed process stats. With --watch, refresh until interrupted.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team monitor: --interval must be >= 0.")
				return exitErr(2)
			}
			if eventTail < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team monitor: --events must be >= 0.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team monitor: --last must be >= 0.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team monitor: choose one of --latest or --last.")
				return exitErr(2)
			}
			if stopExtras && !plan {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team monitor: --stop-extras requires --plan.")
				return exitErr(2)
			}
			if resources && !summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team monitor: --resources requires --summary.")
				return exitErr(2)
			}
			if strings.TrimSpace(eventSince) != "" && eventTail == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team monitor: --since requires --events.")
				return exitErr(2)
			}
			if len(eventActions) > 0 && eventTail == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team monitor: --event-action requires --events.")
				return exitErr(2)
			}
			if len(actionFilters) > 0 && !plan {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team monitor: --action requires --plan.")
				return exitErr(2)
			}
			if format != "" && (jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team monitor: --format cannot be combined with --json or --summary.")
				return exitErr(2)
			}
			formatTemplate, err := parseMonitorFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team monitor: %v\n", err)
				return exitErr(2)
			}
			opts, err := newMonitorOptionsWithInstancesPhasesStaleAndUnhealthy(all, statusFilters, agentFilters, phaseFilters, instanceFilters, staleOnly, unhealthyOnly)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team monitor: %v\n", err)
				return exitErr(2)
			}
			sortMode, err := parsePsSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team monitor: %v\n", err)
				return exitErr(2)
			}
			statsSortMode, err := parseStatsSortFlag(statsSortBy, "--stats-sort")
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team monitor: %v\n", err)
				return exitErr(2)
			}
			planActions, err := planActionFilterSet(actionFilters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team monitor: %v\n", err)
				return exitErr(2)
			}
			eventFilters, err := newMonitorEventFilters(eventActions, eventSince, time.Now)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team monitor: %v\n", err)
				return exitErr(2)
			}
			opts.PS.Sort = sortMode
			opts.PS.SortSet = cmd.Flags().Changed("sort")
			opts.Stats.Sort = statsSortMode
			opts.Stats.SortSet = cmd.Flags().Changed("stats-sort")
			opts.PS.Limit = last
			opts.Stats.Limit = last
			if latest {
				opts.PS.Limit = 1
				opts.Stats.Limit = 1
			}
			opts.IncludePlan = plan
			opts.IncludeJobs = jobs
			opts.IncludeSchedules = schedules
			opts.IncludeResources = resources
			opts.StopExtras = stopExtras
			opts.PlanActions = planActions
			opts.EventTail = eventTail
			opts.EventFilters = eventFilters
			opts.StrictTopology = strictTopology
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				if formatTemplate != nil {
					return runMonitorFormatWatch(ctx, cmd.OutOrStdout(), teamDir, interval, time.Now, readProcessStats, opts, formatTemplate)
				}
				clear := !noClear && !jsonOut
				if summary {
					return runMonitorSummaryWatch(ctx, cmd.OutOrStdout(), teamDir, interval, time.Now, jsonOut, opts, clear)
				}
				return runMonitorWatch(ctx, cmd.OutOrStdout(), teamDir, interval, time.Now, readProcessStats, jsonOut, opts, clear)
			}
			if summary {
				return runMonitorSummary(cmd.OutOrStdout(), teamDir, time.Now(), jsonOut, opts)
			}
			snapshot, err := collectMonitorSnapshot(teamDir, time.Now(), readProcessStats, opts)
			if err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(snapshot)
			}
			if formatTemplate != nil {
				return renderMonitorFormat(cmd.OutOrStdout(), snapshot, formatTemplate)
			}
			return renderMonitor(cmd.OutOrStdout(), snapshot)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Include stopped, exited, and crashed daemon-managed instances in the stats section.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the monitor snapshot until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&plan, "plan", false, "Include desired-state actions from instances.toml and daemon metadata.")
	cmd.Flags().BoolVar(&jobs, "jobs", false, "Include durable job summary, attention, ready-step state, and status-file previews.")
	cmd.Flags().BoolVar(&schedules, "schedules", false, "Include due and upcoming declared schedule state.")
	cmd.Flags().BoolVar(&stopExtras, "stop-extras", false, "With --plan, preview running topology extras as stop actions.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show compact non-failing fleet health and optional plan summaries instead of the full monitor.")
	cmd.Flags().BoolVar(&resources, "resources", false, "With --summary, include aggregate CPU, memory, and RSS totals.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit JSON. With --watch, writes one JSON object per refresh.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Show only the most recently started instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Show only the N most recently started instances after other filters (0 = all).")
	cmd.Flags().StringVar(&format, "format", "", "Render monitor snapshots with a Go template, e.g. '{{.Health.Healthy}} {{len .Instances}}'.")
	cmd.Flags().StringVar(&sortBy, "sort", "name", "Sort instance rows by name, status, agent, phase, stale, unhealthy, started, stopped, or exited.")
	cmd.Flags().StringVar(&statsSortBy, "stats-sort", "name", "Sort stats rows by name, cpu, mem, rss, status, agent, phase, stale, or unhealthy.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only show instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only show crashed or stale instances.")
	cmd.Flags().IntVar(&eventTail, "events", 0, "Include the last N matching daemon lifecycle events in the full monitor (0 = omit).")
	cmd.Flags().StringSliceVar(&eventActions, "event-action", nil, "With --events, only show lifecycle events with this action. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&eventSince, "since", "", "With --events, only show lifecycle events since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only show lifecycle status in instance, stats, and plan sections: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&agentFilters, "agent", nil, "Only show instances, stats, and plan rows for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only show instances and stats in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&instanceFilters, "instance", nil, "Only show instances, stats, and plan rows with this name. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&actionFilters, "action", nil, "With --plan, only show plan rows with this action: start, resume, keep, on-demand, stop, or extra. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&strictTopology, "strict-topology", false, "Treat running daemon-known instances not declared in instances.toml as unhealthy.")
	return cmd
}

type monitorOptions struct {
	PS               psOptions
	Stats            statsOptions
	IncludePlan      bool
	IncludeJobs      bool
	IncludeSchedules bool
	IncludeResources bool
	StopExtras       bool
	PlanActions      map[string]bool
	EventTail        int
	EventFilters     eventFilters
	StrictTopology   bool
}

func newMonitorOptions(all bool, statusFilters, agentFilters []string) (monitorOptions, error) {
	return newMonitorOptionsWithInstances(all, statusFilters, agentFilters, nil)
}

func newMonitorOptionsWithInstances(all bool, statusFilters, agentFilters, instanceFilters []string) (monitorOptions, error) {
	return newMonitorOptionsWithInstancesAndPhases(all, statusFilters, agentFilters, nil, instanceFilters)
}

func newMonitorOptionsWithInstancesAndPhases(all bool, statusFilters, agentFilters, phaseFilters, instanceFilters []string) (monitorOptions, error) {
	return newMonitorOptionsWithInstancesPhasesAndStale(all, statusFilters, agentFilters, phaseFilters, instanceFilters, false)
}

func newMonitorOptionsWithInstancesPhasesAndStale(all bool, statusFilters, agentFilters, phaseFilters, instanceFilters []string, staleOnly bool) (monitorOptions, error) {
	return newMonitorOptionsWithInstancesPhasesStaleAndUnhealthy(all, statusFilters, agentFilters, phaseFilters, instanceFilters, staleOnly, false)
}

func newMonitorOptionsWithInstancesPhasesStaleAndUnhealthy(all bool, statusFilters, agentFilters, phaseFilters, instanceFilters []string, staleOnly, unhealthyOnly bool) (monitorOptions, error) {
	psOpts, err := newPsOptionsWithInstancesAndUnhealthy(statusFilters, agentFilters, phaseFilters, instanceFilters, staleOnly, unhealthyOnly)
	if err != nil {
		return monitorOptions{}, err
	}
	statsOpts, err := newStatsOptionsWithInstancesAndPhases(all, statusFilters, agentFilters, phaseFilters, instanceFilters)
	if err != nil {
		return monitorOptions{}, err
	}
	statsOpts.Stale = staleOnly
	return monitorOptions{PS: psOpts, Stats: statsOpts}, nil
}

type monitorSnapshot struct {
	Health      *healthResult              `json:"health"`
	Plan        *planResult                `json:"plan,omitempty"`
	Jobs        *jobTriageSnapshot         `json:"jobs,omitempty"`
	JobStatus   []jobStatusReconcileResult `json:"job_status_preview,omitempty"`
	Schedules   *scheduleForecast          `json:"schedules,omitempty"`
	Instances   []psJSONRow                `json:"instances"`
	Stats       []statsJSONRow             `json:"stats"`
	StatsError  string                     `json:"stats_error,omitempty"`
	Events      []daemon.LifecycleEvent    `json:"events,omitempty"`
	EventsError string                     `json:"events_error,omitempty"`

	instanceRows []instanceRow
	statsRows    []statsRow
	eventRows    []daemon.LifecycleEvent
	statsEmpty   string
}

type monitorSummarySnapshot struct {
	Health         *healthResult                 `json:"health"`
	Resources      *statsSummaryJSON             `json:"resources,omitempty"`
	ResourcesError string                        `json:"resources_error,omitempty"`
	Plan           *lifecycleActionSummaryResult `json:"plan,omitempty"`
	Jobs           *jobTriageSnapshot            `json:"jobs,omitempty"`
	JobStatus      []jobStatusReconcileResult    `json:"job_status_preview,omitempty"`
	Schedules      *scheduleForecast             `json:"schedules,omitempty"`
	Events         *eventSummaryJSON             `json:"events,omitempty"`
	EventsError    string                        `json:"events_error,omitempty"`
}

type scheduleForecast struct {
	Total    int            `json:"total"`
	Due      int            `json:"due"`
	Upcoming int            `json:"upcoming"`
	Unseen   int            `json:"unseen"`
	Rows     []scheduleInfo `json:"rows"`
}

const watchClearSequence = "\x1b[H\x1b[2J"

func writeWatchClear(w io.Writer, clear bool) error {
	if !clear {
		return nil
	}
	_, err := io.WriteString(w, watchClearSequence)
	return err
}

func runMonitorWatch(ctx context.Context, w io.Writer, teamDir string, interval time.Duration, now func() time.Time, probe processStatsProbe, jsonOut bool, opts monitorOptions, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		snapshot, err := collectMonitorSnapshot(teamDir, now(), probe, opts)
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
			if err := renderMonitor(w, snapshot); err != nil {
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

func runMonitorFormatWatch(ctx context.Context, w io.Writer, teamDir string, interval time.Duration, now func() time.Time, probe processStatsProbe, opts monitorOptions, tmpl *template.Template) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		snapshot, err := collectMonitorSnapshot(teamDir, now(), probe, opts)
		if err != nil {
			return err
		}
		if err := renderMonitorFormat(w, snapshot, tmpl); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func runMonitorSummary(w io.Writer, teamDir string, now time.Time, jsonOut bool, opts monitorOptions) error {
	snapshot, err := collectMonitorSummarySnapshot(teamDir, now, opts)
	if err != nil {
		return err
	}
	if jsonOut {
		if !monitorSummaryJSONUsesSnapshot(opts) {
			return json.NewEncoder(w).Encode(snapshot.Health)
		}
		return json.NewEncoder(w).Encode(snapshot)
	}
	renderMonitorSummarySnapshot(w, snapshot)
	return nil
}

func runMonitorSummaryWatch(ctx context.Context, w io.Writer, teamDir string, interval time.Duration, now func() time.Time, jsonOut bool, opts monitorOptions, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		snapshot, err := collectMonitorSummarySnapshot(teamDir, now(), opts)
		if err != nil {
			return err
		}
		if jsonOut {
			if !monitorSummaryJSONUsesSnapshot(opts) {
				if err := json.NewEncoder(w).Encode(snapshot.Health); err != nil {
					return err
				}
			} else {
				if err := json.NewEncoder(w).Encode(snapshot); err != nil {
					return err
				}
			}
		} else {
			if err := writeWatchClear(w, clear); err != nil {
				return err
			}
			renderMonitorSummarySnapshot(w, snapshot)
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

func monitorSummaryJSONUsesSnapshot(opts monitorOptions) bool {
	return opts.IncludeResources || opts.IncludePlan || opts.IncludeJobs || opts.IncludeSchedules || opts.EventTail > 0
}

func collectMonitorSummarySnapshot(teamDir string, now time.Time, opts monitorOptions) (*monitorSummarySnapshot, error) {
	health, err := collectHealthWithOptions(teamDir, now, healthOptions{
		filters:        opts.PS,
		strictTopology: opts.StrictTopology,
	})
	if err != nil {
		return nil, err
	}
	snapshot := &monitorSummarySnapshot{Health: health}
	var selectedInstances map[string]bool
	if opts.IncludeResources || opts.IncludePlan || opts.EventTail > 0 {
		selectedInstances, err = monitorSummarySelectedInstanceSet(teamDir, now, opts.PS)
		if err != nil {
			return nil, err
		}
	}
	if opts.IncludeResources {
		resources, err := collectMonitorResourceSummary(teamDir, now, opts, selectedInstances)
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
		planOpts := opts.PS
		if selectedInstances != nil {
			planOpts.instances = selectedInstances
		}
		plan.Instances = filterPlanRowsWithActions(plan.Instances, planOpts, opts.PlanActions)
		summary := summarizeLifecycleActions(planRowsToLifecycleActionResults(plan.Instances, true), true)
		snapshot.Plan = &lifecycleActionSummaryResult{Summary: summary}
	}
	if opts.IncludeJobs {
		jobs, err := collectJobTriage(teamDir, now, defaultJobTriageStaleAfter)
		if err != nil {
			return nil, err
		}
		snapshot.Jobs = &jobs
		status, err := reconcileJobsFromStatus(teamDir, true, now)
		if err != nil {
			return nil, err
		}
		snapshot.JobStatus = status
	}
	if opts.IncludeSchedules {
		schedules, err := collectScheduleForecast(teamDir, now)
		if err != nil {
			return nil, err
		}
		snapshot.Schedules = schedules
	}
	if opts.EventTail > 0 {
		eventInstanceFilter := opts.PS.instances
		if selectedInstances != nil {
			eventInstanceFilter = selectedInstances
		}
		events, err := collectMonitorSummaryEvents(teamDir, opts.EventTail, monitorEventFilters(opts, eventInstanceFilter))
		if err != nil {
			snapshot.EventsError = err.Error()
		} else {
			snapshot.Events = events
		}
	}
	return snapshot, nil
}

func collectScheduleForecast(teamDir string, now time.Time) (*scheduleForecast, error) {
	schedules, err := loadScheduleInfos(teamDir)
	if err != nil {
		return nil, err
	}
	rows := nextScheduleRows(schedules, now, 0)
	forecast := &scheduleForecast{Total: len(rows), Rows: rows}
	for _, row := range rows {
		switch {
		case row.Due:
			forecast.Due++
		case row.NextRun != nil:
			forecast.Upcoming++
		default:
			forecast.Unseen++
		}
	}
	return forecast, nil
}

func collectMonitorResourceSummary(teamDir string, now time.Time, opts monitorOptions, selectedInstances map[string]bool) (*statsSummaryJSON, error) {
	statsOpts := opts.Stats
	statsOpts.phaseByInstance = statsPhaseByInstance(teamDir, now)
	statsOpts.staleByInstance = staleInstanceSet(teamDir, now)
	if selectedInstances != nil {
		statsOpts.instances = selectedInstances
	}
	var lister instanceLister
	client, err := newDaemonClient(teamDir)
	switch {
	case err == nil:
		lister = client
	case errors.Is(err, errDaemonNotRunning):
		lister = localInstanceLister{daemonRoot: daemon.DaemonRoot(teamDir)}
	default:
		return nil, err
	}
	rows, err := collectStatsRows(lister, nil, statsOpts, now, readProcessStats)
	if err != nil {
		return nil, err
	}
	summary := summarizeStatsRows(rows)
	return &summary, nil
}

func monitorEventFilters(opts monitorOptions, instances map[string]bool) eventFilters {
	filters := opts.EventFilters
	filters.agents = opts.PS.agents
	filters.instances = instances
	filters.statuses = opts.PS.statuses
	return filters
}

func newMonitorEventFilters(actions []string, sinceRaw string, now func() time.Time) (eventFilters, error) {
	var filters eventFilters
	var err error
	if filters.actions, err = stringSetFilter(actions, "--event-action", "action"); err != nil {
		return filters, err
	}
	sinceRaw = strings.TrimSpace(sinceRaw)
	if sinceRaw == "" {
		return filters, nil
	}
	since, err := parseEventSince(sinceRaw, now)
	if err != nil {
		return filters, err
	}
	filters.since = &since
	return filters, nil
}

func collectMonitorSummaryEvents(teamDir string, tail int, filters eventFilters) (*eventSummaryJSON, error) {
	var client eventsClient
	daemonClient, err := newDaemonClient(teamDir)
	switch {
	case err == nil:
		client = daemonClient
	case errors.Is(err, errDaemonNotRunning):
		client = localEventsClient{daemonRoot: daemon.DaemonRoot(teamDir)}
	default:
		return nil, err
	}
	events, err := collectMonitorEvents(context.Background(), client, tail, filters)
	if err != nil {
		return nil, err
	}
	summary := summarizeMonitorEvents(events)
	return &summary, nil
}

func summarizeMonitorEvents(events []daemon.LifecycleEvent) eventSummaryJSON {
	summary := eventSummaryJSON{}
	for _, ev := range events {
		summary.add(ev)
	}
	summary.finalize()
	return summary
}

func monitorSummarySelectedInstanceSet(teamDir string, now time.Time, opts psOptions) (map[string]bool, error) {
	if opts.Limit <= 0 && len(opts.phases) == 0 && !opts.stale && !opts.unhealthy {
		return nil, nil
	}
	rows, err := collectPsRows(teamDir, now)
	if err != nil {
		return nil, err
	}
	displayRows := filterLimitSortPsRows(rows, opts)
	return monitorSelectedInstanceSet(displayRows, opts), nil
}

func renderMonitorSummarySnapshot(w io.Writer, snapshot *monitorSummarySnapshot) {
	renderHealth(w, snapshot.Health)
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
	if snapshot.Jobs != nil {
		fmt.Fprintln(w)
		renderMonitorJobsSummary(w, snapshot.Jobs)
		if snapshot.JobStatus != nil {
			renderMonitorJobStatusSummary(w, snapshot.JobStatus)
		}
	}
	if snapshot.Schedules != nil {
		fmt.Fprintln(w)
		renderMonitorSchedulesSummary(w, snapshot.Schedules)
	}
	if snapshot.EventsError != "" || snapshot.Events != nil {
		fmt.Fprintln(w)
		if snapshot.EventsError != "" {
			fmt.Fprintf(w, "events: unavailable: %s\n", snapshot.EventsError)
		} else {
			_ = renderEventSummaryResult(w, *snapshot.Events, false)
		}
	}
}

func renderMonitorJobsSummary(w io.Writer, snapshot *jobTriageSnapshot) {
	fmt.Fprintln(w, "job triage:")
	renderJobSummary(w, snapshot.Summary)
	renderQueueSummary(w, snapshot.Queue)
	fmt.Fprintf(w, "triage: attention=%d ready_steps=%d\n", len(snapshot.Attention), len(snapshot.ReadySteps))
}

func renderMonitorJobStatusSummary(w io.Writer, results []jobStatusReconcileResult) {
	fmt.Fprintf(w, "job status: previews=%d changes=%d blocked=%d\n",
		len(results),
		countChangedJobStatusPreviews(results),
		countJobStatusPreviewsByAfter(results, "blocked"),
	)
}

func renderMonitorJobs(w io.Writer, snapshot *jobTriageSnapshot) {
	fmt.Fprintln(w, "job triage:")
	renderJobSummary(w, snapshot.Summary)
	renderQueueSummary(w, snapshot.Queue)
	fmt.Fprintln(w)
	renderJobTriageAttention(w, snapshot.Attention)
	if len(snapshot.ReadySteps) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Ready pipeline steps:")
		renderJobReadyTable(w, snapshot.ReadySteps)
	}
}

func renderMonitorSchedulesSummary(w io.Writer, snapshot *scheduleForecast) {
	fmt.Fprintf(w, "schedules: total=%d due=%d upcoming=%d unseen=%d\n", snapshot.Total, snapshot.Due, snapshot.Upcoming, snapshot.Unseen)
}

func renderMonitorSchedules(w io.Writer, snapshot *scheduleForecast) {
	fmt.Fprintln(w, "schedules:")
	renderMonitorSchedulesSummary(w, snapshot)
	if len(snapshot.Rows) == 0 {
		fmt.Fprintln(w, "(no schedules declared)")
		return
	}
	fmt.Fprintln(w)
	_ = renderScheduleNextRows(w, snapshot.Rows, false, nil)
}

func collectMonitorSnapshot(teamDir string, now time.Time, probe processStatsProbe, opts monitorOptions) (*monitorSnapshot, error) {
	pid, alive := daemonAlive(teamDir)
	rows, err := collectPsRows(teamDir, now)
	if err != nil {
		return nil, err
	}
	opts.Stats.phaseByInstance = statsPhaseByRows(rows)
	opts.Stats.staleByInstance = statsStaleByRows(rows)
	topo, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return nil, err
	}
	health := buildHealthWithOptions(alive, pid, rows, topo, now, healthOptions{
		filters:        opts.PS,
		strictTopology: opts.StrictTopology,
	})
	displayRows := filterLimitSortPsRows(rows, opts.PS)
	selectedInstances := monitorSelectedInstanceSet(displayRows, opts.PS)
	snapshot := &monitorSnapshot{
		Health:       health,
		Instances:    psJSONRows(displayRows),
		Stats:        []statsJSONRow{},
		instanceRows: displayRows,
		statsEmpty:   "(no running instances; use --all to include stopped/exited instances)",
	}
	if opts.Stats.All {
		snapshot.statsEmpty = "(no daemon-managed instances)"
	}
	if statsOptionsHasFilters(opts.Stats) || opts.PS.stale || opts.PS.unhealthy {
		snapshot.statsEmpty = "(no matching instances)"
	}
	if opts.IncludePlan {
		plan, err := collectPlan(teamDir)
		if err != nil {
			return nil, err
		}
		if opts.StopExtras {
			markPlanStopExtras(plan)
		}
		planOpts := opts.PS
		if selectedInstances != nil {
			planOpts.instances = selectedInstances
		}
		plan.Instances = filterPlanRowsWithActions(plan.Instances, planOpts, opts.PlanActions)
		plan.Summary = summarizePlanRows(plan.Instances)
		snapshot.Plan = plan
	}
	if opts.IncludeJobs {
		jobs, err := collectJobTriage(teamDir, now, defaultJobTriageStaleAfter)
		if err != nil {
			return nil, err
		}
		snapshot.Jobs = &jobs
		status, err := reconcileJobsFromStatus(teamDir, true, now)
		if err != nil {
			return nil, err
		}
		snapshot.JobStatus = status
	}
	if opts.IncludeSchedules {
		schedules, err := collectScheduleForecast(teamDir, now)
		if err != nil {
			return nil, err
		}
		snapshot.Schedules = schedules
	}
	eventInstanceFilter := opts.PS.instances
	if selectedInstances != nil {
		eventInstanceFilter = selectedInstances
	}

	client, err := newDaemonClient(teamDir)
	switch {
	case err == nil:
		statsRows, err := collectStatsRows(client, nil, opts.Stats, now, probe)
		if err != nil {
			snapshot.StatsError = err.Error()
		} else {
			if opts.PS.stale || opts.PS.unhealthy {
				statsRows = filterStatsRowsToInstanceOrder(statsRows, displayRows)
			}
			snapshot.statsRows = statsRows
			snapshot.Stats = statsJSONRows(statsRows)
		}
		if opts.EventTail > 0 {
			events, err := collectMonitorEvents(context.Background(), client, opts.EventTail, monitorEventFilters(opts, eventInstanceFilter))
			if err != nil {
				snapshot.EventsError = err.Error()
			} else {
				snapshot.eventRows = events
				snapshot.Events = events
			}
		}
	case errors.Is(err, errDaemonNotRunning):
		statsRows, err := collectStatsRows(localInstanceLister{daemonRoot: daemon.DaemonRoot(teamDir)}, nil, opts.Stats, now, probe)
		if err != nil {
			snapshot.StatsError = err.Error()
		} else {
			if opts.PS.stale || opts.PS.unhealthy {
				statsRows = filterStatsRowsToInstanceOrder(statsRows, displayRows)
			}
			snapshot.statsRows = statsRows
			snapshot.Stats = statsJSONRows(statsRows)
		}
		if opts.EventTail > 0 {
			events, err := collectMonitorEvents(context.Background(), localEventsClient{daemonRoot: daemon.DaemonRoot(teamDir)}, opts.EventTail, monitorEventFilters(opts, eventInstanceFilter))
			if err != nil {
				snapshot.EventsError = err.Error()
			} else {
				snapshot.eventRows = events
				snapshot.Events = events
			}
		}
	default:
		return nil, err
	}
	return snapshot, nil
}

func monitorSelectedInstanceSet(rows []instanceRow, opts psOptions) map[string]bool {
	if opts.Limit <= 0 && len(opts.phases) == 0 && !opts.stale && !opts.unhealthy {
		return nil
	}
	out := make(map[string]bool, len(rows))
	for _, row := range rows {
		out[row.Instance] = true
	}
	if len(out) == 0 {
		out[""] = false
	}
	return out
}

func statsPhaseByRows(rows []instanceRow) map[string]string {
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Instance] = psPhaseKey(row)
	}
	return out
}

func statsStaleByRows(rows []instanceRow) map[string]bool {
	out := make(map[string]bool, len(rows))
	for _, row := range rows {
		if row.Stale {
			out[row.Instance] = true
		}
	}
	return out
}

func filterStatsRowsToInstanceOrder(rows []statsRow, order []instanceRow) []statsRow {
	if len(rows) == 0 || len(order) == 0 {
		return nil
	}
	byInstance := make(map[string]statsRow, len(rows))
	for _, row := range rows {
		byInstance[row.Instance] = row
	}
	out := make([]statsRow, 0, len(order))
	for _, row := range order {
		stats, ok := byInstance[row.Instance]
		if ok {
			out = append(out, stats)
		}
	}
	return out
}

func parseMonitorFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("monitor-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderMonitorFormat(w io.Writer, snapshot *monitorSnapshot, tmpl *template.Template) error {
	if err := tmpl.Execute(w, snapshot); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func renderMonitor(w io.Writer, snapshot *monitorSnapshot) error {
	renderHealth(w, snapshot.Health)

	if snapshot.Plan != nil {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "plan:")
		renderPlanTable(w, snapshot.Plan)
	}

	if snapshot.Jobs != nil {
		fmt.Fprintln(w)
		renderMonitorJobs(w, snapshot.Jobs)
		if snapshot.JobStatus != nil {
			fmt.Fprintln(w)
			renderMonitorJobStatusSummary(w, snapshot.JobStatus)
		}
	}

	if snapshot.Schedules != nil {
		fmt.Fprintln(w)
		renderMonitorSchedules(w, snapshot.Schedules)
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "instances:")
	if err := renderPsTable(w, snapshot.instanceRows); err != nil {
		return err
	}

	if snapshot.eventRows != nil || snapshot.EventsError != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "events:")
		if snapshot.EventsError != "" {
			fmt.Fprintf(w, "(events unavailable: %s)\n", snapshot.EventsError)
		} else if len(snapshot.eventRows) == 0 {
			fmt.Fprintln(w, "(no events)")
		} else {
			for _, ev := range snapshot.eventRows {
				renderEventLine(w, ev)
			}
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "stats:")
	if snapshot.StatsError != "" {
		fmt.Fprintf(w, "(stats unavailable: %s)\n", snapshot.StatsError)
		return nil
	}
	empty := snapshot.statsEmpty
	if empty == "" {
		empty = "(no running instances; use --all to include stopped/exited instances)"
	}
	return renderStatsTable(w, snapshot.statsRows, empty)
}

func collectMonitorEvents(ctx context.Context, client eventsClient, tail int, filters eventFilters) ([]daemon.LifecycleEvent, error) {
	rc, err := client.Events(ctx, false, 0)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	events := []daemon.LifecycleEvent{}
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev daemon.LifecycleEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if !filters.match(ev) {
			continue
		}
		events = append(events, ev)
	}
	if tail > 0 && len(events) > tail {
		events = events[len(events)-tail:]
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return events, nil
		}
		return events, err
	}
	return events, nil
}

func filterPlanRows(rows []planRow, opts psOptions) []planRow {
	if len(opts.statuses) == 0 && len(opts.agents) == 0 && len(opts.phases) == 0 && len(opts.instances) == 0 {
		return rows
	}
	out := make([]planRow, 0, len(rows))
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
		if len(opts.phases) > 0 && !opts.phases[planPhaseKey(row.Phase)] {
			continue
		}
		if len(opts.instances) > 0 && !opts.instances[row.Instance] {
			continue
		}
		out = append(out, row)
	}
	return out
}

func filterPlanRowsWithActions(rows []planRow, opts psOptions, actions map[string]bool) []planRow {
	rows = filterPlanRows(rows, opts)
	if len(actions) == 0 {
		return rows
	}
	out := make([]planRow, 0, len(rows))
	for _, row := range rows {
		if actions[row.Action] {
			out = append(out, row)
		}
	}
	return out
}
