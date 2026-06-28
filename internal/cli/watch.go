package cli

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newWatchCmd() *cobra.Command {
	var (
		target           string
		all              bool
		plan             bool
		jobs             bool
		schedules        bool
		stopExtras       bool
		summary          bool
		resources        bool
		lastMessage      bool
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
		Use:   "watch",
		Short: "Watch the combined health, recovery, inbox, instance, and resource monitor.",
		Long: "Watch the Docker-style operator monitor, refreshing fleet health, " +
			"job, queue, and outbox recovery signals, inbox state, instance state, and daemon-managed process stats until interrupted.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team watch: --interval must be >= 0.")
				return exitErr(2)
			}
			if eventTail < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team watch: --events must be >= 0.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team watch: --last must be >= 0.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team watch: choose one of --latest or --last.")
				return exitErr(2)
			}
			if stopExtras && !plan {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team watch: --stop-extras requires --plan.")
				return exitErr(2)
			}
			if resources && !summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team watch: --resources requires --summary.")
				return exitErr(2)
			}
			if strings.TrimSpace(eventSince) != "" && eventTail == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team watch: --since requires --events.")
				return exitErr(2)
			}
			if len(eventActions) > 0 && eventTail == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team watch: --event-action requires --events.")
				return exitErr(2)
			}
			if strings.TrimSpace(eventSortBy) != "" && cmd.Flags().Changed("events-sort") && eventTail == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team watch: --events-sort requires --events.")
				return exitErr(2)
			}
			if len(actionFilters) > 0 && !plan {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team watch: --action requires --plan.")
				return exitErr(2)
			}
			if format != "" && (jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team watch: --format cannot be combined with --json or --summary.")
				return exitErr(2)
			}
			formatTemplate, err := parseMonitorFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team watch: %v\n", err)
				return exitErr(2)
			}
			opts, err := newMonitorOptionsWithRuntimeInstancesPhasesStaleAndUnhealthy(all, statusFilters, runtimeFilters, agentFilters, phaseFilters, instanceFilters, staleOnly, unhealthyOnly)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team watch: %v\n", err)
				return exitErr(2)
			}
			opts.PS.runtimeStale = runtimeStaleOnly
			opts.Stats.RuntimeStale = runtimeStaleOnly
			sortMode, err := parsePsSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team watch: %v\n", err)
				return exitErr(2)
			}
			statsSortMode, err := parseStatsSortFlag(statsSortBy, "--stats-sort")
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team watch: %v\n", err)
				return exitErr(2)
			}
			planActions, err := planActionFilterSet(actionFilters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team watch: %v\n", err)
				return exitErr(2)
			}
			eventFilters, err := newMonitorEventFilters(eventActions, eventSince, time.Now)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team watch: %v\n", err)
				return exitErr(2)
			}
			eventSortMode, err := parseEventSort(eventSortBy)
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team watch: --events-sort must be oldest or newest.")
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
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
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
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Include stopped, exited, and crashed daemon-managed instances in the stats section.")
	cmd.Flags().BoolVar(&plan, "plan", false, "Include desired-state actions from instances.toml and daemon metadata.")
	cmd.Flags().BoolVar(&jobs, "jobs", false, "Include durable job summary, attention, and ready-step state.")
	cmd.Flags().BoolVar(&schedules, "schedules", false, "Include due and upcoming declared schedule state.")
	cmd.Flags().BoolVar(&stopExtras, "stop-extras", false, "With --plan, preview running topology extras as stop actions.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Watch compact non-failing fleet health and optional plan summaries instead of the full monitor.")
	cmd.Flags().BoolVar(&resources, "resources", false, "With --summary, include aggregate CPU, memory, and RSS totals.")
	cmd.Flags().BoolVar(&lastMessage, "last-message", false, "When runtime recovery actions use resume-plan log fallbacks, prefer clean Codex final-message commands.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit one JSON object per refresh.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "Append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Show only the most recently started instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Show only the N most recently started instances after other filters (0 = all).")
	cmd.Flags().StringVar(&format, "format", "", "Render each monitor snapshot with a Go template, e.g. '{{.Health.Healthy}} {{len .Instances}}'.")
	cmd.Flags().StringVar(&sortBy, "sort", "name", "Sort instance rows by name, status, agent, phase, stale, runtime-stale, unhealthy, started, stopped, or exited.")
	cmd.Flags().StringVar(&statsSortBy, "stats-sort", "name", "Sort stats rows by name, cpu, mem, rss, status, agent, phase, stale, runtime-stale, or unhealthy.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only show instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&runtimeStaleOnly, "runtime-stale", false, "Only show running instances whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only show crashed, status-stale, or runtime-stale instances.")
	cmd.Flags().IntVar(&eventTail, "events", 0, "Include the last N matching daemon lifecycle events in the full monitor (0 = omit).")
	cmd.Flags().StringVar(&eventSortBy, "events-sort", "oldest", "Sort the visible --events section by oldest or newest.")
	cmd.Flags().StringSliceVar(&eventActions, "event-action", nil, "With --events, only show lifecycle events with this action. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&eventSince, "since", "", "With --events, only show lifecycle events since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only show lifecycle status in instance, stats, and plan sections: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only show instances and stats for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&agentFilters, "agent", nil, "Only show instances, stats, and plan rows for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only show instances and stats in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&instanceFilters, "instance", nil, "Only show instances, stats, and plan rows with this name. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&actionFilters, "action", nil, "With --plan, only show plan rows with this action: start, resume, keep, unsupported, on-demand, stop, or extra. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&strictTopology, "strict-topology", false, "Treat running daemon-known instances not declared in instances.toml as unhealthy.")
	return cmd
}
