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

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newMonitorCmd() *cobra.Command {
	var (
		target           string
		all              bool
		watch            bool
		plan             bool
		jobs             bool
		schedules        bool
		stopExtras       bool
		summary          bool
		resources        bool
		lastMessage      bool
		fallbacks        bool
		commands         bool
		jsonOut          bool
		noClear          bool
		latest           bool
		last             int
		format           string
		sortBy           string
		statsSortBy      string
		staleOnly        bool
		runtimeStaleOnly bool
		unhealthyOnly    bool
		eventTail        int
		eventSortBy      string
		eventSince       string
		interval         time.Duration
		statusFilters    []string
		runtimeFilters   []string
		agentFilters     []string
		phaseFilters     []string
		instanceFilters  []string
		actionFilters    []string
		eventActions     []string
		strictTopology   bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "monitor",
		Short: "Show a combined health, recovery, inbox, instance, and resource snapshot.",
		Long: "Show a Docker-style operator snapshot combining fleet health, inbox state, " +
			"job, queue, and outbox recovery signals, the instance table, and daemon-managed process stats. " +
			"With --watch, refresh until interrupted.",
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
			if strings.TrimSpace(eventSortBy) != "" && cmd.Flags().Changed("events-sort") && eventTail == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team monitor: --events-sort requires --events.")
				return exitErr(2)
			}
			if len(actionFilters) > 0 && !plan {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team monitor: --action requires --plan.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team monitor: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team monitor: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if commands && watch {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team monitor: --commands cannot be combined with --watch.")
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
			opts, err := newMonitorOptionsWithRuntimeInstancesPhasesStaleAndUnhealthy(all, statusFilters, runtimeFilters, agentFilters, phaseFilters, instanceFilters, staleOnly, unhealthyOnly)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team monitor: %v\n", err)
				return exitErr(2)
			}
			opts.PS.runtimeStale = runtimeStaleOnly
			opts.Stats.RuntimeStale = runtimeStaleOnly
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
			eventSortMode, err := parseEventSort(eventSortBy)
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team monitor: --events-sort must be oldest or newest.")
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
			opts.EventSort = eventSortMode
			opts.EventFilters = eventFilters
			opts.StrictTopology = strictTopology
			opts.LastMessage = lastMessage
			opts.Fallbacks = fallbacks
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
				if commands {
					scope := operatorCommandScopeFromCommand(cmd, target, "target")
					return runMonitorSummaryCommands(cmd.OutOrStdout(), teamDir, time.Now(), opts, monitorCommandOptions{
						Scope: scope,
						Plan: planCommandOptions{
							BaseArgs:        []string{"agent-team", "sync"},
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
				return runMonitorSummary(cmd.OutOrStdout(), teamDir, time.Now(), jsonOut, opts)
			}
			snapshot, err := collectMonitorSnapshot(teamDir, time.Now(), readProcessStats, opts)
			if err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(snapshot)
			}
			if commands {
				scope := operatorCommandScopeFromCommand(cmd, target, "target")
				return renderMonitorCommands(cmd.OutOrStdout(), snapshot, monitorCommandOptions{
					Scope: scope,
					Plan: planCommandOptions{
						BaseArgs:        []string{"agent-team", "sync"},
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
			if formatTemplate != nil {
				return renderMonitorFormat(cmd.OutOrStdout(), snapshot, formatTemplate)
			}
			return renderMonitor(cmd.OutOrStdout(), snapshot)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Include stopped, exited, and crashed daemon-managed instances in the stats section.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the monitor snapshot until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&plan, "plan", false, "Include desired-state actions from instances.toml and daemon metadata.")
	cmd.Flags().BoolVar(&jobs, "jobs", false, "Include durable job summary, attention, ready-step state, and status-file previews.")
	cmd.Flags().BoolVar(&schedules, "schedules", false, "Include due and upcoming declared schedule state.")
	cmd.Flags().BoolVar(&stopExtras, "stop-extras", false, "With --plan, preview running topology extras as stop actions.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show compact non-failing fleet health and optional plan summaries instead of the full monitor.")
	cmd.Flags().BoolVar(&resources, "resources", false, "With --summary, include aggregate CPU, memory, and RSS totals.")
	cmd.Flags().BoolVar(&lastMessage, "last-message", false, "When runtime recovery actions use resume-plan log fallbacks, prefer clean Codex final-message commands.")
	cmd.Flags().BoolVar(&fallbacks, "fallbacks", false, "When runtime recovery actions use resume-plan, recommend command-mode fallback expansion.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print recovery and apply commands from the visible monitor sections, one per line. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit JSON. With --watch, writes one JSON object per refresh.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Show only the most recently started instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Show only the N most recently started instances after other filters (0 = all).")
	cmd.Flags().StringVar(&format, "format", "", "Render monitor snapshots with a Go template, e.g. '{{.Health.Healthy}} {{len .Instances}}'.")
	cmd.Flags().StringVar(&sortBy, "sort", "name", "Sort instance rows by name, status, agent, phase, stale, runtime-stale, unhealthy, started, stopped, or exited.")
	cmd.Flags().StringVar(&statsSortBy, "stats-sort", "name", "Sort stats rows by name, cpu, mem, rss, status, agent, phase, stale, runtime-stale, or unhealthy.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only show instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&runtimeStaleOnly, "runtime-stale", false, "Only show running instances whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only show crashed, status-stale, or runtime-stale instances.")
	cmd.Flags().IntVar(&eventTail, "events", 0, "Include the last N matching daemon lifecycle events in the full monitor (0 = omit).")
	cmd.Flags().StringVar(&eventSortBy, "events-sort", "oldest", "Sort the visible --events section by oldest or newest.")
	cmd.Flags().StringSliceVar(&eventActions, "event-action", nil, "With --events, only show lifecycle events with this action. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&eventSince, "since", "", "With --events, only show lifecycle events since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only show lifecycle status in instance, stats, and plan sections: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only show instances and stats for this runtime: claude, codex, or docker. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&agentFilters, "agent", nil, "Only show instances, stats, and plan rows for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only show instances and stats in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&instanceFilters, "instance", nil, "Only show instances, stats, and plan rows with this name. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&actionFilters, "action", nil, "With --plan, only show plan rows with this action: start, resume, keep, unsupported, on-demand, stop, or extra. Can repeat or comma-separate.")
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
	EventSort        string
	EventFilters     eventFilters
	StrictTopology   bool
	LastMessage      bool
	Fallbacks        bool
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
	return newMonitorOptionsWithRuntimeInstancesPhasesStaleAndUnhealthy(all, statusFilters, nil, agentFilters, phaseFilters, instanceFilters, staleOnly, unhealthyOnly)
}

func newMonitorOptionsWithRuntimeInstancesPhasesStaleAndUnhealthy(all bool, statusFilters, runtimeFilters, agentFilters, phaseFilters, instanceFilters []string, staleOnly, unhealthyOnly bool) (monitorOptions, error) {
	psOpts, err := newPsOptionsWithRuntimeInstancesAndUnhealthy(statusFilters, runtimeFilters, agentFilters, phaseFilters, instanceFilters, staleOnly, unhealthyOnly)
	if err != nil {
		return monitorOptions{}, err
	}
	statsOpts, err := newStatsOptionsWithRuntimeInstancesPhasesAndUnhealthy(all, statusFilters, runtimeFilters, agentFilters, phaseFilters, instanceFilters, false)
	if err != nil {
		return monitorOptions{}, err
	}
	statsOpts.Stale = staleOnly
	return monitorOptions{PS: psOpts, Stats: statsOpts}, nil
}

type monitorSnapshot struct {
	Team           *teamInfo                  `json:"team,omitempty"`
	Health         *healthResult              `json:"health"`
	Plan           *planResult                `json:"plan,omitempty"`
	Jobs           *jobTriageSnapshot         `json:"jobs,omitempty"`
	JobStatus      []jobStatusReconcileResult `json:"job_status_preview,omitempty"`
	PipelineStatus []pipelineStatusRow        `json:"pipeline_status,omitempty"`
	Schedules      *scheduleForecast          `json:"schedules,omitempty"`
	Runtime        overviewRuntimeSummary     `json:"runtime"`
	RuntimeError   string                     `json:"runtime_error,omitempty"`
	Inbox          overviewInboxSummary       `json:"inbox"`
	InboxError     string                     `json:"inbox_error,omitempty"`
	Instances      []psJSONRow                `json:"instances"`
	Stats          []statsJSONRow             `json:"stats"`
	StatsError     string                     `json:"stats_error,omitempty"`
	Events         []daemon.LifecycleEvent    `json:"events,omitempty"`
	EventsError    string                     `json:"events_error,omitempty"`

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
	PipelineStatus []pipelineStatusRow           `json:"pipeline_status,omitempty"`
	Schedules      *scheduleForecast             `json:"schedules,omitempty"`
	Runtime        overviewRuntimeSummary        `json:"runtime"`
	RuntimeError   string                        `json:"runtime_error,omitempty"`
	Inbox          overviewInboxSummary          `json:"inbox"`
	InboxError     string                        `json:"inbox_error,omitempty"`
	Events         *eventSummaryJSON             `json:"events,omitempty"`
	EventsError    string                        `json:"events_error,omitempty"`
	planRows       []planRow
}

type teamMonitorSummarySnapshot struct {
	Team           *teamInfo                     `json:"team,omitempty"`
	Health         *healthResult                 `json:"health"`
	Resources      *statsSummaryJSON             `json:"resources,omitempty"`
	ResourcesError string                        `json:"resources_error,omitempty"`
	Plan           *lifecycleActionSummaryResult `json:"plan,omitempty"`
	Jobs           *jobTriageSnapshot            `json:"jobs,omitempty"`
	JobStatus      []jobStatusReconcileResult    `json:"job_status_preview,omitempty"`
	PipelineStatus []pipelineStatusRow           `json:"pipeline_status,omitempty"`
	Schedules      *scheduleForecast             `json:"schedules,omitempty"`
	Runtime        overviewRuntimeSummary        `json:"runtime"`
	RuntimeError   string                        `json:"runtime_error,omitempty"`
	Inbox          overviewInboxSummary          `json:"inbox"`
	InboxError     string                        `json:"inbox_error,omitempty"`
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

func waitForWatchTick(ctx context.Context, ticks <-chan time.Time) bool {
	select {
	case <-ctx.Done():
		return false
	default:
	}
	select {
	case <-ctx.Done():
		return false
	case <-ticks:
	}
	select {
	case <-ctx.Done():
		return false
	default:
		return true
	}
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
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
		if !jsonOut && !clear {
			fmt.Fprintln(w)
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
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
	}
}

func runTeamMonitorWatch(ctx context.Context, w io.Writer, teamDir, name string, interval time.Duration, now func() time.Time, probe processStatsProbe, jsonOut bool, opts monitorOptions, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		snapshot, err := collectTeamMonitorSnapshot(teamDir, name, now(), probe, opts)
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
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
		if !jsonOut && !clear {
			fmt.Fprintln(w)
		}
	}
}

func runTeamMonitorFormatWatch(ctx context.Context, w io.Writer, teamDir, name string, interval time.Duration, now func() time.Time, probe processStatsProbe, opts monitorOptions, tmpl *template.Template) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		snapshot, err := collectTeamMonitorSnapshot(teamDir, name, now(), probe, opts)
		if err != nil {
			return err
		}
		if err := renderMonitorFormat(w, snapshot, tmpl); err != nil {
			return err
		}
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
	}
}

func runTeamMonitorSummaryWatch(ctx context.Context, w io.Writer, teamDir, name string, interval time.Duration, now func() time.Time, probe processStatsProbe, jsonOut bool, opts monitorOptions, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		snapshot, err := collectTeamMonitorSnapshot(teamDir, name, now(), probe, opts)
		if err != nil {
			return err
		}
		summary := teamMonitorSummaryFromSnapshot(snapshot, opts)
		if jsonOut {
			if err := json.NewEncoder(w).Encode(summary); err != nil {
				return err
			}
		} else {
			if err := writeWatchClear(w, clear); err != nil {
				return err
			}
			renderTeamMonitorSummarySnapshot(w, summary)
		}
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
		if !jsonOut && !clear {
			fmt.Fprintln(w)
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

func runMonitorSummaryCommands(w io.Writer, teamDir string, now time.Time, opts monitorOptions, commandOpts monitorCommandOptions) error {
	snapshot, err := collectMonitorSummarySnapshot(teamDir, now, opts)
	if err != nil {
		return err
	}
	actions := monitorSummaryCommandActions(snapshot)
	if opts.IncludePlan {
		var planCommands strings.Builder
		if err := renderPlanCommands(&planCommands, snapshot.planRows, commandOpts.Plan); err != nil {
			return err
		}
		actions = append(actions, splitCommandLines(planCommands.String())...)
	}
	return renderOperatorActionCommands(w, actions, commandOpts.Scope)
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
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
		if !jsonOut && !clear {
			fmt.Fprintln(w)
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
	health = healthResultWithResumePlanActions(health, opts.LastMessage, opts.Fallbacks)
	snapshot := &monitorSummarySnapshot{Health: health}
	var runtimeSelectedInstances map[string]bool
	if runtimeSelectedInstances, err = monitorSummaryRuntimeSelectedInstanceSet(teamDir, now, opts.PS); err != nil {
		return nil, err
	}
	if runtime, err := collectMonitorRuntimeSummary(teamDir, runtimeSelectedInstances); err != nil {
		snapshot.RuntimeError = err.Error()
	} else {
		snapshot.Runtime = runtime
	}
	if inbox, err := collectMonitorInbox(teamDir, nil, nil, nil); err != nil {
		snapshot.InboxError = err.Error()
	} else {
		snapshot.Inbox = inbox
	}
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
		snapshot.planRows = plan.Instances
	}
	if opts.IncludeJobs {
		jobs, err := collectJobTriageWithPolicy(teamDir, now)
		if err != nil {
			return nil, err
		}
		snapshot.Jobs = &jobs
		status, err := reconcileJobsFromStatus(teamDir, true, now)
		if err != nil {
			return nil, err
		}
		snapshot.JobStatus = status
		pipelines, err := collectPipelineStatusRows(teamDir, "")
		if err != nil {
			return nil, err
		}
		snapshot.PipelineStatus = pipelines
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
		events, err := collectMonitorSummaryEvents(teamDir, opts.EventTail, monitorEventFilters(opts, eventInstanceFilter), opts.EventSort)
		if err != nil {
			snapshot.EventsError = err.Error()
		} else {
			snapshot.Events = events
		}
	}
	return snapshot, nil
}

func teamMonitorSummaryFromSnapshot(snapshot *monitorSnapshot, opts monitorOptions) *teamMonitorSummarySnapshot {
	if snapshot == nil {
		return &teamMonitorSummarySnapshot{}
	}
	summary := &teamMonitorSummarySnapshot{
		Team:           snapshot.Team,
		Health:         snapshot.Health,
		Jobs:           snapshot.Jobs,
		JobStatus:      snapshot.JobStatus,
		PipelineStatus: snapshot.PipelineStatus,
		Schedules:      snapshot.Schedules,
		Runtime:        snapshot.Runtime,
		RuntimeError:   snapshot.RuntimeError,
		Inbox:          snapshot.Inbox,
		InboxError:     snapshot.InboxError,
	}
	if opts.IncludeResources {
		if snapshot.StatsError != "" {
			summary.ResourcesError = snapshot.StatsError
		} else {
			resources := summarizeStatsRows(snapshot.statsRows)
			summary.Resources = &resources
		}
	}
	if snapshot.Plan != nil {
		planSummary := summarizeLifecycleActions(planRowsToLifecycleActionResults(snapshot.Plan.Instances, true), true)
		summary.Plan = &lifecycleActionSummaryResult{Summary: planSummary}
	}
	if snapshot.EventsError != "" {
		summary.EventsError = snapshot.EventsError
	} else if opts.EventTail > 0 {
		events := summarizeMonitorEvents(snapshot.eventRows)
		summary.Events = &events
	}
	return summary
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

func collectMonitorRuntimeSummary(teamDir string, selectedInstances map[string]bool) (overviewRuntimeSummary, error) {
	metas, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
	if err != nil {
		return overviewRuntimeSummary{}, err
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return overviewRuntimeSummary{}, err
	}
	queueItems, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return overviewRuntimeSummary{}, err
	}
	return overviewRuntimeFromMetadataAndQueue(filterMonitorRuntimeMetadata(metas, selectedInstances), jobs, filterMonitorRuntimeQueueItems(queueItems, selectedInstances), time.Now().UTC()), nil
}

func collectTeamMonitorRuntimeSummary(teamDir string, top *topology.Topology, team *topology.Team, selectedInstances map[string]bool) (overviewRuntimeSummary, error) {
	metas, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
	if err != nil {
		return overviewRuntimeSummary{}, err
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return overviewRuntimeSummary{}, err
	}
	queueItems, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return overviewRuntimeSummary{}, err
	}
	metas = teamMetadata(top, team, metas)
	queueItems = teamQueueItems(top, team, jobs, queueItems)
	return overviewRuntimeFromMetadataAndQueue(filterMonitorRuntimeMetadata(metas, selectedInstances), jobs, filterMonitorRuntimeQueueItems(queueItems, selectedInstances), time.Now().UTC()), nil
}

func filterMonitorRuntimeMetadata(metas []*daemon.Metadata, selectedInstances map[string]bool) []*daemon.Metadata {
	if selectedInstances == nil {
		return metas
	}
	out := make([]*daemon.Metadata, 0, len(metas))
	for _, meta := range metas {
		if meta == nil {
			continue
		}
		if selectedInstances[meta.Instance] {
			out = append(out, meta)
		}
	}
	return out
}

func filterMonitorRuntimeQueueItems(items []*daemon.QueueItem, selectedInstances map[string]bool) []*daemon.QueueItem {
	if selectedInstances == nil {
		return items
	}
	out := make([]*daemon.QueueItem, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		if selectedInstances[item.Instance] || selectedInstances[item.InstanceID] {
			out = append(out, item)
		}
	}
	return out
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

func collectMonitorSummaryEvents(teamDir string, tail int, filters eventFilters, sortMode string) (*eventSummaryJSON, error) {
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
	events, err := collectMonitorEvents(context.Background(), client, tail, filters, sortMode)
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
	if opts.Limit <= 0 && len(opts.runtimes) == 0 && len(opts.phases) == 0 && !opts.stale && !opts.unhealthy {
		return nil, nil
	}
	rows, err := collectPsRows(teamDir, now)
	if err != nil {
		return nil, err
	}
	displayRows := filterLimitSortPsRows(rows, opts)
	return monitorSelectedInstanceSet(displayRows, opts), nil
}

func monitorSummaryRuntimeSelectedInstanceSet(teamDir string, now time.Time, opts psOptions) (map[string]bool, error) {
	if !monitorPSUsesVisibleInstanceSet(opts) {
		return nil, nil
	}
	rows, err := collectPsRows(teamDir, now)
	if err != nil {
		return nil, err
	}
	displayRows := filterLimitSortPsRows(rows, opts)
	return monitorRuntimeSelectedInstanceSet(displayRows, opts), nil
}

func renderMonitorSummarySnapshot(w io.Writer, snapshot *monitorSummarySnapshot) {
	renderHealth(w, snapshot.Health)
	fmt.Fprintln(w)
	renderMonitorInboxSummary(w, snapshot.Inbox, snapshot.InboxError)
	renderMonitorRuntimeSummary(w, snapshot.Runtime, snapshot.RuntimeError)
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
		if snapshot.PipelineStatus != nil {
			renderMonitorPipelineStatusSummary(w, snapshot.PipelineStatus)
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

func renderTeamMonitorSummarySnapshot(w io.Writer, snapshot *teamMonitorSummarySnapshot) {
	if snapshot.Team != nil {
		fmt.Fprintf(w, "Team: %s\n", snapshot.Team.Name)
		if snapshot.Team.Description != "" {
			fmt.Fprintf(w, "Description: %s\n", snapshot.Team.Description)
		}
	}
	renderHealth(w, snapshot.Health)
	fmt.Fprintln(w)
	renderMonitorInboxSummary(w, snapshot.Inbox, snapshot.InboxError)
	renderMonitorRuntimeSummary(w, snapshot.Runtime, snapshot.RuntimeError)
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
		if snapshot.PipelineStatus != nil {
			renderMonitorPipelineStatusSummary(w, snapshot.PipelineStatus)
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

func renderMonitorRuntimeSummary(w io.Writer, runtime overviewRuntimeSummary, runtimeErr string) {
	if runtimeErr != "" {
		fmt.Fprintf(w, "runtime: unavailable: %s\n", runtimeErr)
		return
	}
	renderOverviewRuntimeSummary(w, runtime)
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

func renderMonitorPipelineStatusSummary(w io.Writer, rows []pipelineStatusRow) {
	fmt.Fprintf(w, "pipeline status: pipelines=%d jobs=%d ready_steps=%d stale_running_steps=%d failed_steps=%d\n",
		len(rows),
		countPipelineStatusJobs(rows),
		countPipelineStatusReadySteps(rows),
		countPipelineStatusStaleRunningSteps(rows),
		countPipelineStatusFailedSteps(rows))
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

func renderMonitorPipelineStatus(w io.Writer, rows []pipelineStatusRow) {
	fmt.Fprintln(w, "pipeline status:")
	renderPipelineStatusTable(w, rows)
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
	_ = renderScheduleNextRows(w, snapshot.Rows, false, nil, false)
}

type monitorCommandOptions struct {
	Scope operatorCommandScope
	Plan  planCommandOptions
}

func renderMonitorCommands(w io.Writer, snapshot *monitorSnapshot, opts monitorCommandOptions) error {
	if snapshot == nil {
		return nil
	}
	actions := monitorCommandActions(snapshot)
	if snapshot.Plan != nil {
		var planCommands strings.Builder
		if err := renderPlanCommands(&planCommands, snapshot.Plan.Instances, opts.Plan); err != nil {
			return err
		}
		actions = append(actions, splitCommandLines(planCommands.String())...)
	}
	return renderOperatorActionCommands(w, actions, opts.Scope)
}

func monitorCommandActions(snapshot *monitorSnapshot) []string {
	actions := make([]string, 0)
	if snapshot == nil {
		return actions
	}
	if snapshot.Health != nil {
		for _, issue := range snapshot.Health.Issues {
			actions = append(actions, issue.Actions...)
		}
	}
	if snapshot.Jobs != nil {
		for _, item := range snapshot.Jobs.Attention {
			actions = append(actions, item.Actions...)
		}
		for _, row := range snapshot.Jobs.ReadySteps {
			actions = append(actions, row.Actions...)
		}
	}
	return actions
}

func monitorSummaryCommandActions(snapshot *monitorSummarySnapshot) []string {
	actions := make([]string, 0)
	if snapshot == nil {
		return actions
	}
	if snapshot.Health != nil {
		for _, issue := range snapshot.Health.Issues {
			actions = append(actions, issue.Actions...)
		}
	}
	if snapshot.Jobs != nil {
		for _, item := range snapshot.Jobs.Attention {
			actions = append(actions, item.Actions...)
		}
		for _, row := range snapshot.Jobs.ReadySteps {
			actions = append(actions, row.Actions...)
		}
	}
	return actions
}

func splitCommandLines(body string) []string {
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
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
	if err := addPipelineWorkflowHealth(health, teamDir); err != nil {
		return nil, err
	}
	if err := addQueueHealth(health, teamDir, now); err != nil {
		return nil, err
	}
	if err := addOutboxQuarantineHealth(health, teamDir); err != nil {
		return nil, err
	}
	if err := addIntakeHealth(health, teamDir); err != nil {
		return nil, err
	}
	health = healthResultWithResumePlanActions(health, opts.LastMessage, opts.Fallbacks)
	displayRows := filterLimitSortPsRows(rows, opts.PS)
	selectedInstances := monitorSelectedInstanceSet(displayRows, opts.PS)
	runtimeSelectedInstances := monitorRuntimeSelectedInstanceSet(displayRows, opts.PS)
	selectedInboxInstances := monitorInboxSelectedInstanceSet(displayRows, opts.PS)
	snapshot := &monitorSnapshot{
		Health:       health,
		Instances:    psJSONRows(displayRows),
		Stats:        []statsJSONRow{},
		instanceRows: displayRows,
		statsEmpty:   "(no running instances; use --all to include stopped/exited instances)",
	}
	if runtime, err := collectMonitorRuntimeSummary(teamDir, runtimeSelectedInstances); err != nil {
		snapshot.RuntimeError = err.Error()
	} else {
		snapshot.Runtime = runtime
	}
	if inbox, err := collectMonitorInbox(teamDir, nil, nil, selectedInboxInstances); err != nil {
		snapshot.InboxError = err.Error()
	} else {
		snapshot.Inbox = inbox
	}
	if opts.Stats.All {
		snapshot.statsEmpty = "(no daemon-managed instances)"
	}
	if statsOptionsHasFilters(opts.Stats) || opts.PS.stale || opts.PS.runtimeStale || opts.PS.unhealthy {
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
		jobs, err := collectJobTriageWithPolicy(teamDir, now)
		if err != nil {
			return nil, err
		}
		snapshot.Jobs = &jobs
		status, err := reconcileJobsFromStatus(teamDir, true, now)
		if err != nil {
			return nil, err
		}
		snapshot.JobStatus = status
		pipelines, err := collectPipelineStatusRows(teamDir, "")
		if err != nil {
			return nil, err
		}
		snapshot.PipelineStatus = pipelines
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
			if opts.PS.stale || opts.PS.runtimeStale || opts.PS.unhealthy {
				statsRows = filterStatsRowsToInstanceOrder(statsRows, displayRows)
			}
			snapshot.statsRows = statsRows
			snapshot.Stats = statsJSONRows(statsRows)
		}
		if opts.EventTail > 0 {
			events, err := collectMonitorEvents(context.Background(), client, opts.EventTail, monitorEventFilters(opts, eventInstanceFilter), opts.EventSort)
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
			if opts.PS.stale || opts.PS.runtimeStale || opts.PS.unhealthy {
				statsRows = filterStatsRowsToInstanceOrder(statsRows, displayRows)
			}
			snapshot.statsRows = statsRows
			snapshot.Stats = statsJSONRows(statsRows)
		}
		if opts.EventTail > 0 {
			events, err := collectMonitorEvents(context.Background(), localEventsClient{daemonRoot: daemon.DaemonRoot(teamDir)}, opts.EventTail, monitorEventFilters(opts, eventInstanceFilter), opts.EventSort)
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

func collectTeamMonitorSnapshot(teamDir, name string, now time.Time, probe processStatsProbe, opts monitorOptions) (*monitorSnapshot, error) {
	now = now.UTC()
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	rows, err := collectPsRows(teamDir, now)
	if err != nil {
		return nil, err
	}
	teamRows := teamInstanceRows(top, team, rows)
	opts.Stats.phaseByInstance = statsPhaseByRows(teamRows)
	opts.Stats.staleByInstance = statsStaleByRows(teamRows)
	health := buildHealthWithDaemonStatus(collectDaemonStatus(teamDir), teamRuntimeRows(top, team, rows), teamScopedTopology(top, team), now, healthOptions{
		filters: opts.PS,
	})
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	ownedJobs := teamJobs(top, team, jobs)
	if err := addTeamQueueHealth(health, teamDir, top, team, ownedJobs, now); err != nil {
		return nil, err
	}
	if err := addTeamOutboxQuarantineHealth(health, teamDir, top, team, ownedJobs); err != nil {
		return nil, err
	}
	if opts.IncludeJobs {
		if err := addTeamJobHealth(health, teamDir, top, team, ownedJobs, now); err != nil {
			return nil, err
		}
	}
	scopeTeamHealthIssueActions(health, team.Name)
	health = healthResultWithResumePlanActions(health, opts.LastMessage, opts.Fallbacks)
	displayRows := filterLimitSortPsRows(teamRows, opts.PS)
	selectedInstances := monitorSelectedInstanceSet(displayRows, opts.PS)
	runtimeSelectedInstances := monitorRuntimeSelectedInstanceSet(displayRows, opts.PS)
	selectedInboxInstances := monitorInboxSelectedInstanceSet(displayRows, opts.PS)
	info := teamInfoFromTopology(team)
	snapshot := &monitorSnapshot{
		Team:         &info,
		Health:       health,
		Instances:    psJSONRows(displayRows),
		Stats:        []statsJSONRow{},
		instanceRows: displayRows,
		statsEmpty:   "(no running team-owned instances; use --all to include stopped/exited instances)",
	}
	if runtime, err := collectTeamMonitorRuntimeSummary(teamDir, top, team, runtimeSelectedInstances); err != nil {
		snapshot.RuntimeError = err.Error()
	} else {
		snapshot.Runtime = runtime
	}
	if inbox, err := collectMonitorInbox(teamDir, top, team, selectedInboxInstances); err != nil {
		snapshot.InboxError = err.Error()
	} else {
		snapshot.Inbox = inbox
	}
	if opts.Stats.All {
		snapshot.statsEmpty = "(no daemon-managed team-owned instances)"
	}
	if statsOptionsHasFilters(opts.Stats) || opts.PS.stale || opts.PS.runtimeStale || opts.PS.unhealthy {
		snapshot.statsEmpty = "(no matching team-owned instances)"
	}
	if opts.IncludePlan {
		plan, err := collectPlan(teamDir)
		if err != nil {
			return nil, err
		}
		if opts.StopExtras {
			markPlanStopExtras(plan)
		}
		plan.Instances = teamPlanRows(top, team, plan.Instances, opts.StopExtras)
		planOpts := opts.PS
		if selectedInstances != nil {
			planOpts.instances = selectedInstances
		}
		plan.Instances = filterPlanRowsWithActions(plan.Instances, planOpts, opts.PlanActions)
		plan.Summary = summarizePlanRows(plan.Instances)
		snapshot.Plan = plan
	}
	if opts.IncludeJobs {
		jobs := health.Jobs
		jobStatus := health.JobStatus
		pipelineStatus := health.PipelineStatus
		health.Jobs = nil
		health.JobStatus = nil
		health.PipelineStatus = nil
		snapshot.Jobs = jobs
		snapshot.JobStatus = jobStatus
		snapshot.PipelineStatus = pipelineStatus
	}
	if opts.IncludeSchedules {
		schedules, err := collectTeamScheduleForecast(teamDir, team, now)
		if err != nil {
			return nil, err
		}
		snapshot.Schedules = schedules
	}

	var base instanceLister
	client, err := newDaemonClient(teamDir)
	switch {
	case err == nil:
		base = client
	case errors.Is(err, errDaemonNotRunning):
		base = localInstanceLister{daemonRoot: daemon.DaemonRoot(teamDir)}
	default:
		return nil, err
	}
	statsRows, err := collectStatsRows(teamWaitLister{instanceLister: base, top: top, team: team}, nil, opts.Stats, now, probe)
	if err != nil {
		snapshot.StatsError = err.Error()
	} else {
		if opts.PS.stale || opts.PS.runtimeStale || opts.PS.unhealthy {
			statsRows = filterStatsRowsToInstanceOrder(statsRows, displayRows)
		}
		snapshot.statsRows = statsRows
		snapshot.Stats = statsJSONRows(statsRows)
	}
	if opts.EventTail > 0 {
		filters, err := teamMonitorEventFilters(teamDir, name, opts, selectedInstances)
		if err != nil {
			snapshot.EventsError = err.Error()
		} else {
			var eventClient eventsClient = localEventsClient{daemonRoot: daemon.DaemonRoot(teamDir)}
			if client != nil {
				eventClient = client
			}
			events, err := collectMonitorEvents(context.Background(), eventClient, opts.EventTail, filters, opts.EventSort)
			if err != nil {
				snapshot.EventsError = err.Error()
			} else {
				snapshot.eventRows = events
				snapshot.Events = events
			}
		}
	}
	return snapshot, nil
}

func collectTeamScheduleForecast(teamDir string, team *topology.Team, now time.Time) (*scheduleForecast, error) {
	schedules, err := loadScheduleInfos(teamDir)
	if err != nil {
		return nil, err
	}
	rows := nextScheduleRows(teamSchedules(team, schedules), now, 0)
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

func collectMonitorInbox(teamDir string, top *topology.Topology, team *topology.Team, selectedInstances map[string]bool) (overviewInboxSummary, error) {
	daemonRoot := daemon.DaemonRoot(teamDir)
	instances, metaByInstance, err := listInboxInstances(daemonRoot)
	if err != nil {
		return overviewInboxSummary{}, err
	}
	if team != nil {
		instances = filterInboxInstancesForTeam(top, team, instances, metaByInstance)
	}
	if len(selectedInstances) > 0 {
		instances = filterMonitorInboxInstances(instances, selectedInstances)
	}
	rows, err := collectInboxSummaryRows(daemonRoot, instances, metaByInstance, false)
	if err != nil {
		return overviewInboxSummary{}, err
	}
	return overviewInboxFromRows(rows), nil
}

func filterMonitorInboxInstances(instances []string, selected map[string]bool) []string {
	if len(instances) == 0 || len(selected) == 0 {
		return instances
	}
	out := make([]string, 0, len(instances))
	for _, instance := range instances {
		if selected[instance] {
			out = append(out, instance)
		}
	}
	return out
}

func teamMonitorEventFilters(teamDir, name string, opts monitorOptions, selectedInstances map[string]bool) (eventFilters, error) {
	filters, err := teamEventFilters(teamDir, name, nil, nil, "", time.Now)
	if err != nil {
		return filters, err
	}
	filters.actions = opts.EventFilters.actions
	filters.since = opts.EventFilters.since
	filters.agents = opts.PS.agents
	filters.statuses = opts.PS.statuses
	switch {
	case len(opts.PS.instances) > 0:
		filters.instances = opts.PS.instances
		filters.instancePrefixes = nil
	case selectedInstances != nil:
		filters.instances = selectedInstances
		filters.instancePrefixes = nil
	}
	return filters, nil
}

func monitorSelectedInstanceSet(rows []instanceRow, opts psOptions) map[string]bool {
	if opts.Limit <= 0 && len(opts.runtimes) == 0 && len(opts.phases) == 0 && !opts.stale && !opts.runtimeStale && !opts.unhealthy {
		return nil
	}
	return monitorInstanceSetFromRows(rows)
}

func monitorRuntimeSelectedInstanceSet(rows []instanceRow, opts psOptions) map[string]bool {
	if !monitorPSUsesVisibleInstanceSet(opts) {
		return nil
	}
	return monitorInstanceSetFromRows(rows)
}

func monitorPSUsesVisibleInstanceSet(opts psOptions) bool {
	return opts.Limit > 0 ||
		len(opts.statuses) > 0 ||
		len(opts.runtimes) > 0 ||
		len(opts.agents) > 0 ||
		len(opts.phases) > 0 ||
		len(opts.instances) > 0 ||
		opts.stale ||
		opts.runtimeStale ||
		opts.unhealthy
}

func monitorInboxSelectedInstanceSet(rows []instanceRow, opts psOptions) map[string]bool {
	if len(opts.instances) > 0 {
		out := make(map[string]bool, len(opts.instances))
		for instance, ok := range opts.instances {
			if ok {
				out[instance] = true
			}
		}
		if len(out) == 0 {
			out[""] = false
		}
		return out
	}
	if opts.Limit <= 0 &&
		len(opts.statuses) == 0 &&
		len(opts.runtimes) == 0 &&
		len(opts.agents) == 0 &&
		len(opts.phases) == 0 &&
		len(opts.instances) == 0 &&
		!opts.stale &&
		!opts.runtimeStale &&
		!opts.unhealthy {
		return nil
	}
	return monitorInstanceSetFromRows(rows)
}

func monitorInstanceSetFromRows(rows []instanceRow) map[string]bool {
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
	if snapshot.Team != nil {
		fmt.Fprintf(w, "Team: %s\n", snapshot.Team.Name)
		if snapshot.Team.Description != "" {
			fmt.Fprintf(w, "Description: %s\n", snapshot.Team.Description)
		}
	}
	renderHealth(w, snapshot.Health)
	fmt.Fprintln(w)
	renderMonitorInboxSummary(w, snapshot.Inbox, snapshot.InboxError)
	renderMonitorRuntimeSummary(w, snapshot.Runtime, snapshot.RuntimeError)

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
		if snapshot.PipelineStatus != nil {
			fmt.Fprintln(w)
			renderMonitorPipelineStatus(w, snapshot.PipelineStatus)
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

func renderMonitorInboxSummary(w io.Writer, inbox overviewInboxSummary, inboxErr string) {
	if inboxErr != "" {
		fmt.Fprintf(w, "inbox: unavailable: %s\n", inboxErr)
		return
	}
	fmt.Fprintf(w, "inbox: instances=%d total=%d unread=%d unread_instances=%d\n",
		inbox.Instances,
		inbox.Total,
		inbox.Unread,
		inbox.UnreadInstances)
}

func collectMonitorEvents(ctx context.Context, client eventsClient, tail int, filters eventFilters, sortMode string) ([]daemon.LifecycleEvent, error) {
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
	sortLifecycleEventsForDisplay(events, sortMode)
	if err := sc.Err(); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return events, nil
		}
		return events, err
	}
	return events, nil
}

func filterPlanRows(rows []planRow, opts psOptions) []planRow {
	if len(opts.statuses) == 0 && len(opts.runtimes) == 0 && len(opts.agents) == 0 && len(opts.phases) == 0 && len(opts.instances) == 0 {
		return rows
	}
	out := make([]planRow, 0, len(rows))
	for _, row := range rows {
		status := row.Status
		if status == "" {
			status = "unknown"
		}
		runtime := strings.ToLower(strings.TrimSpace(row.Runtime))
		if runtime == "" {
			runtime = "unknown"
		}
		if len(opts.statuses) > 0 && !opts.statuses[status] {
			continue
		}
		if len(opts.runtimes) > 0 && !opts.runtimes[runtime] {
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
