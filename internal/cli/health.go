package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newHealthCmd() *cobra.Command {
	var (
		target           string
		jsonOut          bool
		quiet            bool
		watch            bool
		wait             bool
		noClear          bool
		format           string
		commands         bool
		lastMessage      bool
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
		strictTopology   bool
		includeJobs      bool
		interval         time.Duration
		timeout          time.Duration
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Check daemon, instance, queue, job, and outbox health.",
		Long: "Check the daemon, declared persistent instances, crashed instances, and stale status files. " +
			"Queue, job-file quarantine, outbox quarantine, intake, and optional durable job checks are included in the same health result. " +
			"One-shot checks exit 0 when healthy and 1 when unhealthy.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team health: --interval must be >= 0.")
				return exitErr(2)
			}
			if timeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team health: --timeout must be >= 0.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team health: --last must be >= 0.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team health: choose one of --latest or --last.")
				return exitErr(2)
			}
			if watch && wait {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team health: choose one of --watch or --wait.")
				return exitErr(2)
			}
			if quiet && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team health: choose one of --quiet or --json.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team health: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team health: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if commands && quiet {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team health: --commands cannot be combined with --quiet.")
				return exitErr(2)
			}
			if commands && watch {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team health: --commands cannot be combined with --watch.")
				return exitErr(2)
			}
			if commands && wait {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team health: --commands cannot be combined with --wait.")
				return exitErr(2)
			}
			if format != "" && (quiet || jsonOut) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team health: --format cannot be combined with --quiet or --json.")
				return exitErr(2)
			}
			if quiet && watch {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team health: --quiet cannot be combined with --watch.")
				return exitErr(2)
			}
			formatTemplate, err := parseHealthFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team health: %v\n", err)
				return exitErr(2)
			}
			opts, err := newHealthOptionsWithRuntimeInstancesAndUnhealthy(statusFilters, runtimeFilters, agentFilters, phaseFilters, instanceFilters, staleOnly, unhealthyOnly)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team health: %v\n", err)
				return exitErr(2)
			}
			opts.filters.runtimeStale = runtimeStaleOnly
			opts.filters.Limit = last
			if latest {
				opts.filters.Limit = 1
			}
			opts.strictTopology = strictTopology
			opts.includeJobs = includeJobs
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			if wait {
				ctx := cmd.Context()
				cancel := func() {}
				if timeout > 0 {
					ctx, cancel = context.WithTimeout(ctx, timeout)
				}
				defer cancel()
				result, timedOut, err := runHealthWaitWithOutcome(ctx, teamDir, interval, time.Now, opts)
				if err != nil {
					return err
				}
				result = healthResultWithLastMessageActions(result, lastMessage)
				if !quiet {
					if err := writeHealthResultWithFormat(cmd.OutOrStdout(), result, jsonOut, formatTemplate); err != nil {
						return err
					}
					if timedOut && !result.Healthy {
						fmt.Fprintln(cmd.ErrOrStderr(), "agent-team health: wait timed out before the fleet became healthy.")
					}
				}
				if !result.Healthy {
					return exitErr(1)
				}
				return nil
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				if formatTemplate != nil {
					return runHealthFormatWatchWithLastMessage(ctx, cmd.OutOrStdout(), teamDir, interval, time.Now, opts, formatTemplate, lastMessage)
				}
				clear := !noClear && !jsonOut
				return runHealthWatchWithClearAndLastMessage(ctx, cmd.OutOrStdout(), teamDir, interval, time.Now, jsonOut, opts, clear, lastMessage)
			}
			result, err := collectHealthWithOptions(teamDir, time.Now(), opts)
			if err != nil {
				return err
			}
			result = healthResultWithLastMessageActions(result, lastMessage)
			if !quiet {
				scope := operatorCommandScopeFromCommand(cmd, target, "target")
				if err := writeHealthResultWithFormatAndCommands(cmd.OutOrStdout(), result, jsonOut, formatTemplate, commands, scope); err != nil {
					return err
				}
			}
			if !result.Healthy {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress output and use only the exit code.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh health until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&wait, "wait", false, "Poll until the fleet is healthy, then exit.")
	cmd.Flags().StringVar(&format, "format", "", "Render the health result with a Go template, e.g. '{{.Healthy}} {{.Summary.Running}}'.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print issue remediation commands, one per line. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&lastMessage, "last-message", false, "When runtime recovery actions use resume-plan log fallbacks, prefer clean Codex final-message commands.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Only check the most recently started instance after other filters. Daemon health remains global.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Only check the N most recently started instances after other filters (0 = all). Daemon health remains global.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only check instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only check daemon-known instances for this runtime: claude or codex. Daemon health remains global. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&agentFilters, "agent", nil, "Only check declared and daemon-known instances for this agent. Daemon health remains global. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only check instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&instanceFilters, "instance", nil, "Only check instances with this name. Daemon health remains global. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only check instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&runtimeStaleOnly, "runtime-stale", false, "Only check running instances whose recorded runtime PID is no longer live. Daemon health remains global.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only check crashed, status-stale, or runtime-stale instances. Daemon health remains global.")
	cmd.Flags().BoolVar(&strictTopology, "strict-topology", false, "Treat running daemon-known instances not declared in instances.toml as unhealthy.")
	cmd.Flags().BoolVar(&includeJobs, "jobs", false, "Include durable job triage and status-file previews; treat jobs needing attention as unhealthy.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch or --wait.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	return cmd
}

type healthOptions struct {
	filters        psOptions
	strictTopology bool
	includeJobs    bool
}

func newHealthOptions(statusFilters, agentFilters, phaseFilters []string, staleOnly bool) (healthOptions, error) {
	return newHealthOptionsWithInstances(statusFilters, agentFilters, phaseFilters, nil, staleOnly)
}

func newHealthOptionsWithInstances(statusFilters, agentFilters, phaseFilters, instanceFilters []string, staleOnly bool) (healthOptions, error) {
	return newHealthOptionsWithInstancesAndUnhealthy(statusFilters, agentFilters, phaseFilters, instanceFilters, staleOnly, false)
}

func newHealthOptionsWithInstancesAndUnhealthy(statusFilters, agentFilters, phaseFilters, instanceFilters []string, staleOnly, unhealthyOnly bool) (healthOptions, error) {
	return newHealthOptionsWithRuntimeInstancesAndUnhealthy(statusFilters, nil, agentFilters, phaseFilters, instanceFilters, staleOnly, unhealthyOnly)
}

func newHealthOptionsWithRuntimeInstancesAndUnhealthy(statusFilters, runtimeFilters, agentFilters, phaseFilters, instanceFilters []string, staleOnly, unhealthyOnly bool) (healthOptions, error) {
	filters, err := newPsOptionsWithRuntimeInstancesAndUnhealthy(statusFilters, runtimeFilters, agentFilters, phaseFilters, instanceFilters, staleOnly, unhealthyOnly)
	if err != nil {
		return healthOptions{}, err
	}
	return healthOptions{filters: filters}, nil
}

type healthResult struct {
	Healthy          bool                       `json:"healthy"`
	Daemon           healthDaemon               `json:"daemon"`
	Summary          psSummaryJSON              `json:"summary"`
	Queue            queueSummary               `json:"queue"`
	JobQuarantine    jobQuarantineSummary       `json:"job_quarantine"`
	OutboxQuarantine outboxQuarantineSummary    `json:"outbox_quarantine"`
	Intake           overviewIntakeSummary      `json:"intake"`
	Jobs             *jobTriageSnapshot         `json:"jobs,omitempty"`
	JobStatus        []jobStatusReconcileResult `json:"job_status_preview,omitempty"`
	PipelineStatus   []pipelineStatusRow        `json:"pipeline_status,omitempty"`
	PipelineDoctor   *pipelineDoctorResult      `json:"pipeline_doctor,omitempty"`
	Declared         healthDeclared             `json:"declared"`
	Issues           []healthIssue              `json:"issues"`
	CheckedAt        string                     `json:"checked_at"`
	Instances        []healthInstance           `json:"instances,omitempty"`
	Actions          []string                   `json:"actions,omitempty"`
}

type healthDaemon struct {
	Running bool   `json:"running"`
	Ready   bool   `json:"ready"`
	PID     int    `json:"pid,omitempty"`
	Error   string `json:"error,omitempty"`
}

type healthDeclared struct {
	Persistent int `json:"persistent"`
	Running    int `json:"running"`
	Missing    int `json:"missing"`
}

type healthIssue struct {
	Code     string   `json:"code"`
	Severity string   `json:"severity"`
	Instance string   `json:"instance,omitempty"`
	Job      string   `json:"job,omitempty"`
	Actions  []string `json:"actions,omitempty"`
	Message  string   `json:"message"`
	Status   string   `json:"status,omitempty"`
	Phase    string   `json:"phase,omitempty"`
}

type healthInstance struct {
	Instance     string `json:"instance"`
	Agent        string `json:"agent"`
	Status       string `json:"status"`
	Phase        string `json:"phase"`
	Stale        bool   `json:"stale"`
	RuntimeStale bool   `json:"runtime_stale,omitempty"`
	Unhealthy    bool   `json:"unhealthy,omitempty"`
	PID          int    `json:"pid,omitempty"`
}

func runHealthWatch(ctx context.Context, w io.Writer, teamDir string, interval time.Duration, now func() time.Time, jsonOut bool, opts healthOptions) error {
	return runHealthWatchWithClear(ctx, w, teamDir, interval, now, jsonOut, opts, false)
}

func runHealthWatchWithClear(ctx context.Context, w io.Writer, teamDir string, interval time.Duration, now func() time.Time, jsonOut bool, opts healthOptions, clear bool) error {
	return runHealthWatchWithClearAndLastMessage(ctx, w, teamDir, interval, now, jsonOut, opts, clear, false)
}

func runHealthWatchWithClearAndLastMessage(ctx context.Context, w io.Writer, teamDir string, interval time.Duration, now func() time.Time, jsonOut bool, opts healthOptions, clear bool, lastMessage bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		result, err := collectHealthWithOptions(teamDir, now(), opts)
		if err != nil {
			return err
		}
		result = healthResultWithLastMessageActions(result, lastMessage)
		result = healthResultWithActions(result)
		if jsonOut {
			if err := json.NewEncoder(w).Encode(result); err != nil {
				return err
			}
		} else {
			if err := writeWatchClear(w, clear); err != nil {
				return err
			}
			renderHealth(w, result)
		}
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
		if !jsonOut && !clear {
			fmt.Fprintln(w)
		}
	}
}

func runHealthFormatWatch(ctx context.Context, w io.Writer, teamDir string, interval time.Duration, now func() time.Time, opts healthOptions, tmpl *template.Template) error {
	return runHealthFormatWatchWithLastMessage(ctx, w, teamDir, interval, now, opts, tmpl, false)
}

func runHealthFormatWatchWithLastMessage(ctx context.Context, w io.Writer, teamDir string, interval time.Duration, now func() time.Time, opts healthOptions, tmpl *template.Template, lastMessage bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		result, err := collectHealthWithOptions(teamDir, now(), opts)
		if err != nil {
			return err
		}
		result = healthResultWithLastMessageActions(result, lastMessage)
		result = healthResultWithActions(result)
		if err := renderHealthFormat(w, result, tmpl); err != nil {
			return err
		}
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
	}
}

func runHealthWait(ctx context.Context, teamDir string, interval time.Duration, now func() time.Time, opts healthOptions) (*healthResult, error) {
	result, _, err := runHealthWaitWithOutcome(ctx, teamDir, interval, now, opts)
	return result, err
}

func runHealthWaitWithOutcome(ctx context.Context, teamDir string, interval time.Duration, now func() time.Time, opts healthOptions) (*healthResult, bool, error) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var last *healthResult
	for {
		result, err := collectHealthWithOptions(teamDir, now(), opts)
		if err != nil {
			return nil, false, err
		}
		last = result
		if result.Healthy {
			return result, false, nil
		}
		select {
		case <-ctx.Done():
			return last, true, nil
		case <-ticker.C:
		}
	}
}

func healthWaitTimedOutUnhealthy(timedOut bool, health *healthResult) bool {
	return timedOut && health != nil && !health.Healthy
}

func writeHealthResult(w io.Writer, result *healthResult, jsonOut bool) error {
	return writeHealthResultWithFormat(w, result, jsonOut, nil)
}

func writeHealthResultWithFormat(w io.Writer, result *healthResult, jsonOut bool, tmpl *template.Template) error {
	return writeHealthResultWithFormatAndCommands(w, result, jsonOut, tmpl, false, operatorCommandScope{})
}

func writeHealthResultWithFormatAndCommands(w io.Writer, result *healthResult, jsonOut bool, tmpl *template.Template, commands bool, scope operatorCommandScope) error {
	result = healthResultWithActions(result)
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if commands {
		return renderHealthCommands(w, result, scope)
	}
	if tmpl != nil {
		return renderHealthFormat(w, result, tmpl)
	}
	renderHealth(w, result)
	return nil
}

func parseHealthFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("health-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderHealthFormat(w io.Writer, result *healthResult, tmpl *template.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func renderHealthCommands(w io.Writer, result *healthResult, scope operatorCommandScope) error {
	if result == nil {
		return nil
	}
	actions := result.Actions
	if len(actions) == 0 {
		actions = healthIssueActions(result)
	}
	return renderActionCommands(w, scopedOperatorActions(actions, scope))
}

func healthResultWithActions(result *healthResult) *healthResult {
	if result == nil {
		return nil
	}
	result.Actions = healthIssueActions(result)
	return result
}

func healthIssueActions(result *healthResult) []string {
	if result == nil {
		return nil
	}
	seen := map[string]bool{}
	var actions []string
	for _, issue := range result.Issues {
		for _, action := range issue.Actions {
			action = strings.TrimSpace(action)
			if action == "" || seen[action] {
				continue
			}
			seen[action] = true
			actions = append(actions, action)
		}
	}
	return actions
}

func healthResultWithLastMessageActions(result *healthResult, lastMessage bool) *healthResult {
	if result == nil || !lastMessage {
		return result
	}
	for i := range result.Issues {
		for j := range result.Issues[i].Actions {
			result.Issues[i].Actions[j] = operatorActionWithLastMessage(result.Issues[i].Actions[j])
		}
	}
	return result
}

func collectHealth(teamDir string, now time.Time) (*healthResult, error) {
	return collectHealthWithOptions(teamDir, now, healthOptions{})
}

func collectHealthWithOptions(teamDir string, now time.Time, opts healthOptions) (*healthResult, error) {
	daemonStatus := collectDaemonStatus(teamDir)
	rows, err := collectPsRows(teamDir, now)
	if err != nil {
		return nil, err
	}
	topo, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return nil, err
	}
	result := buildHealthWithDaemonStatus(daemonStatus, rows, topo, now, opts)
	if err := addPipelineWorkflowHealth(result, teamDir); err != nil {
		return nil, err
	}
	if err := addQueueHealth(result, teamDir, now); err != nil {
		return nil, err
	}
	if err := addJobQuarantineHealth(result, teamDir); err != nil {
		return nil, err
	}
	if err := addOutboxQuarantineHealth(result, teamDir); err != nil {
		return nil, err
	}
	if err := addIntakeHealth(result, teamDir); err != nil {
		return nil, err
	}
	if opts.includeJobs {
		if err := addJobHealth(result, teamDir, now); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func buildHealth(daemonRunning bool, daemonPID int, rows []instanceRow, topo *topology.Topology, now time.Time) *healthResult {
	return buildHealthWithOptions(daemonRunning, daemonPID, rows, topo, now, healthOptions{})
}

func buildHealthWithOptions(daemonRunning bool, daemonPID int, rows []instanceRow, topo *topology.Topology, now time.Time, opts healthOptions) *healthResult {
	return buildHealthWithDaemonStatus(daemonStatusJSON{
		Running: daemonRunning,
		Ready:   daemonRunning,
		PID:     daemonPID,
	}, rows, topo, now, opts)
}

func buildHealthWithDaemonStatus(daemonStatus daemonStatusJSON, rows []instanceRow, topo *topology.Topology, now time.Time, opts healthOptions) *healthResult {
	allRows := rows
	opts = healthOptionsWithLatestInstances(allRows, opts)
	rows = filterHealthRows(rows, opts)
	result := &healthResult{
		Healthy: true,
		Daemon: healthDaemon{
			Running: daemonStatus.Running,
			Ready:   daemonStatus.Ready,
			PID:     daemonStatus.PID,
			Error:   daemonStatus.Error,
		},
		Summary:   psSummaryRows(rows),
		CheckedAt: now.UTC().Format(time.RFC3339),
	}
	if !daemonStatus.Running {
		result.Daemon.PID = 0
		result.addIssue("daemon_not_running", "", "", "", "daemon is not running")
	} else if !daemonStatus.Ready {
		msg := "daemon API is not ready"
		if daemonStatus.Error != "" {
			msg += ": " + daemonStatus.Error
		}
		result.addIssue("daemon_not_ready", "", "", "", msg)
	}

	allRowByName := map[string]instanceRow{}
	for _, row := range allRows {
		allRowByName[row.Instance] = row
	}
	for _, row := range rows {
		result.Instances = append(result.Instances, healthInstance{
			Instance:     row.Instance,
			Agent:        row.Agent,
			Status:       psStatusKey(row),
			Phase:        psPhaseKey(row),
			Stale:        row.Stale,
			RuntimeStale: row.RuntimeStale,
			Unhealthy:    psRowUnhealthy(row),
			PID:          row.PID,
		})
		if row.Lifecycle == string(daemon.StatusCrashed) {
			result.addIssueWithSeverityAndActions(
				"instance_crashed",
				"error",
				row.Instance,
				job.NormalizeID(row.Job),
				string(daemon.StatusCrashed),
				psPhaseKey(row),
				fmt.Sprintf("instance %q crashed", row.Instance),
				crashedInstanceHealthActions(row),
			)
		}
		if row.Stale {
			result.addIssue("status_stale", row.Instance, psStatusKey(row), psPhaseKey(row), fmt.Sprintf("instance %q status is stale", row.Instance))
		}
		if row.RuntimeStale {
			result.addIssueWithSeverityAndActions(
				"runtime_stale",
				"error",
				row.Instance,
				job.NormalizeID(row.Job),
				psStatusKey(row),
				psPhaseKey(row),
				fmt.Sprintf("instance %q metadata says running but PID %d is not live", row.Instance, row.PID),
				runtimeStaleHealthActions(row),
			)
		}
	}

	if topo != nil {
		declaredNames := make(map[string]bool, len(topo.Instances))
		for _, inst := range topo.SortedInstances() {
			declaredNames[inst.Name] = true
			if inst.Ephemeral {
				continue
			}
			row, ok := allRowByName[inst.Name]
			if !healthDeclaredMatchesFilters(inst, row, ok, opts) {
				continue
			}
			result.Declared.Persistent++
			if !ok {
				result.Declared.Missing++
				result.addIssueWithSeverityAndActions("declared_missing", "error", inst.Name, "", "unknown", "unknown", fmt.Sprintf("declared persistent instance %q has not been started", inst.Name), []string{"agent-team sync --dry-run"})
				continue
			}
			if row.Lifecycle == string(daemon.StatusRunning) {
				result.Declared.Running++
				continue
			}
			status := psStatusKey(row)
			result.addIssueWithSeverityAndActions("declared_not_running", "error", inst.Name, "", status, psPhaseKey(row), fmt.Sprintf("declared persistent instance %q is %s", inst.Name, status), []string{"agent-team sync --dry-run"})
		}
		if opts.strictTopology {
			for _, row := range rows {
				if declaredNames[row.Instance] || row.Lifecycle != string(daemon.StatusRunning) {
					continue
				}
				if _, ok := declaredEphemeralOwner(topo, row.Instance, row.Agent); ok {
					continue
				}
				result.addIssue(
					"topology_extra_running",
					row.Instance,
					psStatusKey(row),
					psPhaseKey(row),
					fmt.Sprintf("daemon-known instance %q is running but not declared in instances.toml", row.Instance),
				)
			}
		}
	}
	return result
}

func addQueueHealth(result *healthResult, teamDir string, now time.Time) error {
	if result == nil {
		return nil
	}
	items, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return err
	}
	result.Queue = summarizeQueueItems(items, now.UTC())
	quarantine, err := listQueueQuarantine(teamDir)
	if err != nil {
		return err
	}
	applyQueueQuarantineSummary(&result.Queue, quarantine)
	if result.Queue.Dead > 0 {
		result.addIssueWithSeverityAndActions(
			"queue_dead_letter",
			"error",
			"",
			"",
			"",
			"",
			fmt.Sprintf("queue has %d dead-letter item(s)", result.Queue.Dead),
			queueDeadLetterHealthActions(teamDir, items),
		)
	}
	if result.Queue.Quarantined > 0 {
		jobID := singleQueueQuarantineJobID(teamDir, quarantine)
		pipelineName := ""
		if jobID == "" {
			pipelineName = singleQueueQuarantinePipeline(teamDir, quarantine)
		}
		result.addIssueWithSeverityAndActions(
			"queue_quarantined",
			"warning",
			"",
			"",
			"",
			"",
			fmt.Sprintf("queue has %d quarantined file(s) (%d restorable, %d unrestorable)", result.Queue.Quarantined, result.Queue.QuarantineRestorable, result.Queue.QuarantineUnrestorable),
			queueQuarantineHealthActions(result.Queue, "", pipelineName, jobID),
		)
	}
	return nil
}

func queueDeadLetterHealthActions(teamDir string, items []*daemon.QueueItem) []string {
	retry := globalQueueRetryAllRecoveryAction(false)
	if id := singleDeadQueueJobID(teamDir, items); id != "" {
		retry = jobQueueRetryAllRecoveryAction(id, false)
	} else if pipelineName := singleDeadQueuePipeline(teamDir, items); pipelineName != "" {
		retry = pipelineQueueRetryAllRecoveryAction(pipelineName, false)
	}
	return []string{retry, "agent-team repair --skip-tick"}
}

func addJobQuarantineHealth(result *healthResult, teamDir string) error {
	if result == nil {
		return nil
	}
	items, err := listJobQuarantine(teamDir)
	if err != nil {
		return err
	}
	result.JobQuarantine = summarizeJobQuarantineItems(items)
	if result.JobQuarantine.Quarantined == 0 {
		return nil
	}
	result.addIssueWithSeverityAndActions(
		"job_quarantined",
		"warning",
		"",
		"",
		"",
		"",
		fmt.Sprintf("jobs have %d quarantined file(s) (%d restorable, %d unrestorable)", result.JobQuarantine.Quarantined, result.JobQuarantine.Restorable, result.JobQuarantine.Unrestorable),
		jobQuarantineHealthActions(result.JobQuarantine),
	)
	return nil
}

func jobQuarantineHealthActions(summary jobQuarantineSummary) []string {
	actions := []string{"agent-team job quarantine"}
	if summary.Unrestorable > 0 {
		actions = append(actions, "agent-team job quarantine --unrestorable")
	}
	if summary.Restorable > 0 {
		actions = append(actions, "agent-team job quarantine --restorable")
	}
	actions = append(actions, "agent-team snapshot --json")
	return actions
}

func singleDeadQueueJobID(teamDir string, items []*daemon.QueueItem) string {
	jobs, err := job.List(teamDir)
	if err != nil {
		return ""
	}
	return singleDeadQueueJobIDForJobs(items, jobs)
}

func singleDeadQueueJobIDForJobs(items []*daemon.QueueItem, jobs []*job.Job) string {
	var found string
	var dead int
	for _, item := range items {
		if item == nil || item.State != daemon.QueueStateDead {
			continue
		}
		dead++
		matches := map[string]bool{}
		for _, j := range jobs {
			if j == nil || strings.TrimSpace(j.ID) == "" {
				continue
			}
			if queueItemMatchesJob(item, j) {
				matches[j.ID] = true
			}
		}
		if len(matches) != 1 {
			return ""
		}
		var id string
		for match := range matches {
			id = match
		}
		if found == "" {
			found = id
			continue
		}
		if found != id {
			return ""
		}
	}
	if dead == 0 {
		return ""
	}
	return found
}

func singleDeadQueuePipeline(teamDir string, items []*daemon.QueueItem) string {
	jobs, err := job.List(teamDir)
	if err != nil {
		return ""
	}
	pipelineName := singleDeadQueuePipelineForJobs(items, jobs)
	if pipelineName == "" {
		return ""
	}
	if _, err := loadPipelineInfo(teamDir, pipelineName); err != nil {
		return ""
	}
	return pipelineName
}

func singleDeadQueuePipelineForJobs(items []*daemon.QueueItem, jobs []*job.Job) string {
	var found string
	var dead int
	for _, item := range items {
		if item == nil || item.State != daemon.QueueStateDead {
			continue
		}
		dead++
		matches := map[string]bool{}
		for _, j := range jobs {
			if j == nil || strings.TrimSpace(j.Pipeline) == "" {
				continue
			}
			if queueItemMatchesJob(item, j) {
				matches[j.Pipeline] = true
			}
		}
		if len(matches) != 1 {
			return ""
		}
		var pipelineName string
		for match := range matches {
			pipelineName = match
		}
		if found == "" {
			found = pipelineName
			continue
		}
		if found != pipelineName {
			return ""
		}
	}
	if dead == 0 {
		return ""
	}
	return found
}

func singleQueueQuarantineJobID(teamDir string, items []queueQuarantineItem) string {
	jobs, err := job.List(teamDir)
	if err != nil {
		return ""
	}
	return singleQueueQuarantineJobIDForJobs(items, jobs)
}

func singleQueueQuarantineJobIDForJobs(items []queueQuarantineItem, jobs []*job.Job) string {
	var found string
	if len(items) == 0 {
		return ""
	}
	for _, item := range items {
		matches := map[string]bool{}
		for _, j := range jobs {
			if j == nil || strings.TrimSpace(j.ID) == "" {
				continue
			}
			if queueQuarantineItemMatchesJob(item, j) {
				matches[j.ID] = true
			}
		}
		if len(matches) != 1 {
			return ""
		}
		var id string
		for match := range matches {
			id = match
		}
		if found == "" {
			found = id
			continue
		}
		if found != id {
			return ""
		}
	}
	return found
}

func singleQueueQuarantinePipeline(teamDir string, items []queueQuarantineItem) string {
	jobs, err := job.List(teamDir)
	if err != nil {
		return ""
	}
	pipelineName := singleQueueQuarantinePipelineForJobs(items, jobs)
	if pipelineName == "" {
		return ""
	}
	if _, err := loadPipelineInfo(teamDir, pipelineName); err != nil {
		return ""
	}
	return pipelineName
}

func singleQueueQuarantinePipelineForJobs(items []queueQuarantineItem, jobs []*job.Job) string {
	var found string
	if len(items) == 0 {
		return ""
	}
	for _, item := range items {
		matches := map[string]bool{}
		for _, j := range jobs {
			if j == nil || strings.TrimSpace(j.Pipeline) == "" {
				continue
			}
			if queueQuarantineItemMatchesJob(item, j) {
				matches[j.Pipeline] = true
			}
		}
		if len(matches) != 1 {
			return ""
		}
		var pipelineName string
		for match := range matches {
			pipelineName = match
		}
		if found == "" {
			found = pipelineName
			continue
		}
		if found != pipelineName {
			return ""
		}
	}
	return found
}

func queueQuarantineHealthActions(summary queueSummary, teamName, pipelineName, jobID string) []string {
	var listAction, detailAction, restorableAction, unrestorableAction string
	switch {
	case teamName != "":
		listAction = fmt.Sprintf("agent-team team queue quarantine %s", teamName)
		detailAction = fmt.Sprintf("agent-team team snapshot %s --json", teamName)
		restorableAction = fmt.Sprintf("agent-team team queue quarantine %s --restorable", teamName)
		unrestorableAction = fmt.Sprintf("agent-team team queue quarantine %s --unrestorable", teamName)
	case jobID != "":
		listAction = fmt.Sprintf("agent-team job queue quarantine %s", jobID)
		detailAction = fmt.Sprintf("agent-team job show %s", jobID)
		restorableAction = fmt.Sprintf("agent-team job queue quarantine %s --restorable", jobID)
		unrestorableAction = fmt.Sprintf("agent-team job queue quarantine %s --unrestorable", jobID)
	case pipelineName != "":
		listAction = fmt.Sprintf("agent-team pipeline queue quarantine %s", pipelineName)
		detailAction = fmt.Sprintf("agent-team pipeline snapshot %s --json", pipelineName)
		restorableAction = fmt.Sprintf("agent-team pipeline queue quarantine %s --restorable", pipelineName)
		unrestorableAction = fmt.Sprintf("agent-team pipeline queue quarantine %s --unrestorable", pipelineName)
	default:
		listAction = "agent-team queue quarantine ls"
		detailAction = "agent-team snapshot --json"
		restorableAction = "agent-team queue quarantine ls --restorable"
		unrestorableAction = "agent-team queue quarantine ls --unrestorable"
	}
	actions := []string{listAction}
	if summary.QuarantineUnrestorable > 0 {
		actions = append(actions, unrestorableAction)
	}
	if summary.QuarantineRestorable > 0 {
		actions = append(actions, restorableAction)
	}
	actions = append(actions, detailAction)
	return actions
}

func addOutboxQuarantineHealth(result *healthResult, teamDir string) error {
	if result == nil {
		return nil
	}
	items, err := listOutboxQuarantine(teamDir)
	if err != nil {
		return err
	}
	result.OutboxQuarantine = summarizeOutboxQuarantineItems(items)
	if result.OutboxQuarantine.Quarantined == 0 {
		return nil
	}
	jobID := singleOutboxQuarantineJobID(teamDir, items)
	pipelineName := ""
	if jobID == "" {
		pipelineName = singleOutboxQuarantinePipeline(teamDir, items)
	}
	result.addIssueWithSeverityAndActions(
		"outbox_quarantined",
		"warning",
		"",
		"",
		"",
		"",
		fmt.Sprintf("outbox has %d quarantined file(s) (%d restorable, %d unrestorable)", result.OutboxQuarantine.Quarantined, result.OutboxQuarantine.Restorable, result.OutboxQuarantine.Unrestorable),
		outboxQuarantineHealthActions(result.OutboxQuarantine, "", pipelineName, jobID),
	)
	return nil
}

func singleOutboxQuarantineJobID(teamDir string, items []outboxQuarantineItem) string {
	jobs, err := job.List(teamDir)
	if err != nil {
		return ""
	}
	return singleOutboxQuarantineJobIDForJobs(items, jobs)
}

func singleOutboxQuarantineJobIDForJobs(items []outboxQuarantineItem, jobs []*job.Job) string {
	var found string
	if len(items) == 0 {
		return ""
	}
	for _, item := range items {
		matches := map[string]bool{}
		for _, j := range jobs {
			if j == nil || strings.TrimSpace(j.ID) == "" {
				continue
			}
			if outboxQuarantineItemMatchesJob(item, j) {
				matches[j.ID] = true
			}
		}
		if len(matches) != 1 {
			return ""
		}
		var id string
		for match := range matches {
			id = match
		}
		if found == "" {
			found = id
			continue
		}
		if found != id {
			return ""
		}
	}
	return found
}

func singleOutboxQuarantinePipeline(teamDir string, items []outboxQuarantineItem) string {
	jobs, err := job.List(teamDir)
	if err != nil {
		return ""
	}
	pipelineName := singleOutboxQuarantinePipelineForJobs(items, jobs)
	if pipelineName == "" {
		return ""
	}
	if _, err := loadPipelineInfo(teamDir, pipelineName); err != nil {
		return ""
	}
	return pipelineName
}

func singleOutboxQuarantinePipelineForJobs(items []outboxQuarantineItem, jobs []*job.Job) string {
	var found string
	if len(items) == 0 {
		return ""
	}
	for _, item := range items {
		matches := map[string]bool{}
		for _, j := range jobs {
			if j == nil || strings.TrimSpace(j.Pipeline) == "" {
				continue
			}
			if outboxQuarantineItemMatchesJob(item, j) {
				matches[j.Pipeline] = true
			}
		}
		if len(matches) != 1 {
			return ""
		}
		var pipelineName string
		for match := range matches {
			pipelineName = match
		}
		if found == "" {
			found = pipelineName
			continue
		}
		if found != pipelineName {
			return ""
		}
	}
	return found
}

func outboxQuarantineHealthActions(summary outboxQuarantineSummary, teamName, pipelineName, jobID string) []string {
	var listAction, detailAction, restorableAction, unrestorableAction string
	switch {
	case teamName != "":
		listAction = fmt.Sprintf("agent-team team outbox quarantine %s", teamName)
		detailAction = fmt.Sprintf("agent-team team snapshot %s --json", teamName)
		restorableAction = fmt.Sprintf("agent-team team outbox quarantine %s --restorable", teamName)
		unrestorableAction = fmt.Sprintf("agent-team team outbox quarantine %s --unrestorable", teamName)
	case jobID != "":
		listAction = fmt.Sprintf("agent-team job outbox quarantine %s", jobID)
		detailAction = fmt.Sprintf("agent-team job snapshot %s --json", jobID)
		restorableAction = fmt.Sprintf("agent-team job outbox quarantine %s --restorable", jobID)
		unrestorableAction = fmt.Sprintf("agent-team job outbox quarantine %s --unrestorable", jobID)
	case pipelineName != "":
		listAction = fmt.Sprintf("agent-team pipeline outbox quarantine %s", pipelineName)
		detailAction = fmt.Sprintf("agent-team pipeline snapshot %s --json", pipelineName)
		restorableAction = fmt.Sprintf("agent-team pipeline outbox quarantine %s --restorable", pipelineName)
		unrestorableAction = fmt.Sprintf("agent-team pipeline outbox quarantine %s --unrestorable", pipelineName)
	default:
		listAction = "agent-team outbox quarantine ls"
		detailAction = "agent-team snapshot --json"
		restorableAction = "agent-team outbox quarantine ls --restorable"
		unrestorableAction = "agent-team outbox quarantine ls --unrestorable"
	}
	actions := []string{listAction}
	if summary.Unrestorable > 0 {
		actions = append(actions, unrestorableAction)
	}
	if summary.Restorable > 0 {
		actions = append(actions, restorableAction)
	}
	actions = append(actions, detailAction)
	return actions
}

func addIntakeHealth(result *healthResult, teamDir string) error {
	if result == nil {
		return nil
	}
	deliveries, err := listIntakeDeliveries(teamDir)
	if err != nil {
		return err
	}
	result.Intake = overviewIntakeFromDeliveries(deliveries)
	if result.Intake.Errors == 0 {
		return nil
	}
	actions := []string{"agent-team intake deliveries --unresolved"}
	if result.Intake.Replayable > 0 {
		actions = append(actions, intakeReplayAllDryRunAction())
	}
	result.addIssueWithSeverityAndActions(
		"intake_unresolved",
		"error",
		"",
		"",
		"",
		"",
		fmt.Sprintf("intake has %d unresolved delivery failure(s)", result.Intake.Errors),
		actions,
	)
	return nil
}

func addPipelineWorkflowHealth(result *healthResult, teamDir string) error {
	if result == nil {
		return nil
	}
	doctor, err := collectPipelineDoctor(teamDir, "")
	if err != nil {
		return err
	}
	if !pipelineDoctorHasHealthContent(doctor) {
		return nil
	}
	result.PipelineDoctor = doctor
	for _, problem := range doctor.Problems {
		code := "pipeline_workflow_problem"
		if strings.TrimSpace(problem.Code) != "" {
			code = "pipeline_" + problem.Code
		}
		result.addIssueWithSeverityAndActions(
			code,
			"error",
			"",
			"",
			"",
			"",
			problem.Message,
			pipelineDoctorActions(problem),
		)
	}
	return nil
}

func pipelineDoctorHasHealthContent(result *pipelineDoctorResult) bool {
	if result == nil {
		return false
	}
	if len(result.Pipelines) > 0 || len(result.Problems) > 0 {
		return true
	}
	for _, warning := range result.Warnings {
		if warning.Code != "no_pipelines" {
			return true
		}
	}
	return false
}

func countPipelineDoctorWarnings(result *pipelineDoctorResult) int {
	if result == nil {
		return 0
	}
	count := 0
	for _, warning := range result.Warnings {
		if warning.Code == "no_pipelines" {
			continue
		}
		count++
	}
	return count
}

func pipelineDoctorActions(finding pipelineDoctorFinding) []string {
	pipeline := strings.TrimSpace(finding.Pipeline)
	if pipeline == "" {
		return []string{"agent-team pipeline doctor --all"}
	}
	return []string{fmt.Sprintf("agent-team pipeline doctor %s", pipeline)}
}

func addJobHealth(result *healthResult, teamDir string, now time.Time) error {
	if result == nil {
		return nil
	}
	snapshot, err := collectJobTriageWithPolicy(teamDir, now.UTC())
	if err != nil {
		return err
	}
	result.Jobs = &snapshot
	for _, item := range snapshot.Attention {
		result.addJobIssue(item)
	}
	statusPreview, err := reconcileJobsFromStatus(teamDir, true, now.UTC())
	if err != nil {
		return err
	}
	result.JobStatus = statusPreview
	for _, preview := range statusPreview {
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
	pipelineStatus, err := collectPipelineStatusRows(teamDir, "")
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
		if row.StaleRunningSteps > 0 {
			result.addIssueWithSeverityAndActions(
				"pipeline_stale_running_step",
				"warning",
				"",
				"",
				"",
				"",
				fmt.Sprintf("pipeline %q has %d stale running step(s)", row.Pipeline, row.StaleRunningSteps),
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

func filterHealthRows(rows []instanceRow, opts healthOptions) []instanceRow {
	return filterLimitSortPsRows(rows, opts.filters)
}

func healthOptionsWithLatestInstances(rows []instanceRow, opts healthOptions) healthOptions {
	if opts.filters.Limit <= 0 {
		return opts
	}
	limit := opts.filters.Limit
	base := opts.filters
	base.Limit = 0
	candidates := filterPsRows(rows, base)
	candidates = limitPsRowsByLatestStarted(candidates, limit)
	instances := make(map[string]bool, len(candidates))
	for _, row := range candidates {
		instances[row.Instance] = true
	}
	if len(instances) == 0 {
		instances[""] = false
	}
	opts.filters.instances = instances
	opts.filters.Limit = 0
	opts.filters.Sort = psSortStarted
	opts.filters.SortSet = true
	return opts
}

func healthDeclaredMatchesFilters(inst *topology.Instance, row instanceRow, hasRow bool, opts healthOptions) bool {
	if hasRow {
		return healthRowMatchesFilters(row, opts)
	}
	return healthRowMatchesFilters(instanceRow{
		Instance:  inst.Name,
		Agent:     inst.Agent,
		Lifecycle: "unknown",
		Phase:     "unknown",
	}, opts)
}

func healthRowMatchesFilters(row instanceRow, opts healthOptions) bool {
	filters := opts.filters
	if filters.stale && !row.Stale {
		return false
	}
	if filters.runtimeStale && !row.RuntimeStale {
		return false
	}
	if filters.unhealthy && !psRowUnhealthy(row) {
		return false
	}
	if len(filters.statuses) > 0 && !filters.statuses[psStatusKey(row)] {
		return false
	}
	if len(filters.runtimes) > 0 && !filters.runtimes[psRuntimeKey(row)] {
		return false
	}
	if len(filters.agents) > 0 && !filters.agents[row.Agent] {
		return false
	}
	if len(filters.instances) > 0 && !filters.instances[row.Instance] {
		return false
	}
	if len(filters.phases) > 0 && !filters.phases[psPhaseKey(row)] {
		return false
	}
	return true
}

func crashedInstanceHealthActions(row instanceRow) []string {
	if id := job.NormalizeID(row.Job); id != "" {
		return []string{fmt.Sprintf("agent-team job resume-plan %s --status crashed", id)}
	}
	if instance := strings.TrimSpace(row.Instance); instance != "" {
		return []string{fmt.Sprintf("agent-team resume-plan %s --status crashed", instance)}
	}
	return []string{"agent-team resume-plan --status crashed"}
}

func runtimeStaleHealthActions(row instanceRow) []string {
	if id := job.NormalizeID(row.Job); id != "" {
		return []string{fmt.Sprintf("agent-team job resume-plan %s --runtime-stale", id)}
	}
	if instance := strings.TrimSpace(row.Instance); instance != "" {
		return []string{fmt.Sprintf("agent-team resume-plan %s --runtime-stale", instance)}
	}
	return []string{"agent-team resume-plan --runtime-stale"}
}

func (r *healthResult) addIssue(code, instance, status, phase, message string) {
	r.addIssueWithSeverity(code, "error", instance, "", status, phase, message)
}

func (r *healthResult) addJobIssue(item jobTriageItem) {
	severity := item.Severity
	switch severity {
	case "critical":
		severity = "error"
	case "warning", "info":
	default:
		severity = "error"
	}
	reasons := strings.Join(item.Reasons, ",")
	if reasons == "" {
		reasons = "attention"
	}
	message := fmt.Sprintf("job %q needs attention: %s", item.JobID, reasons)
	if strings.TrimSpace(item.Message) != "" {
		message += ": " + strings.TrimSpace(item.Message)
	}
	r.addIssueWithSeverityAndActions("job_attention", severity, item.Instance, item.JobID, string(item.Status), "", message, item.Actions)
}

func (r *healthResult) addIssueWithSeverity(code, severity, instance, jobID, status, phase, message string) {
	r.addIssueWithSeverityAndActions(code, severity, instance, jobID, status, phase, message, nil)
}

func (r *healthResult) addIssueWithSeverityAndActions(code, severity, instance, jobID, status, phase, message string, actions []string) {
	r.Healthy = false
	r.Issues = append(r.Issues, healthIssue{
		Code:     code,
		Severity: severity,
		Instance: instance,
		Job:      jobID,
		Actions:  append([]string(nil), actions...),
		Status:   status,
		Phase:    phase,
		Message:  message,
	})
}

func renderHealth(w io.Writer, result *healthResult) {
	state := "healthy"
	if !result.Healthy {
		state = "unhealthy"
	}
	if result.Daemon.Running {
		fmt.Fprintf(w, "health: %s\n", state)
		fmt.Fprintf(w, "daemon: running (pid=%d, ready=%s)\n", result.Daemon.PID, yesNo(result.Daemon.Ready))
		if result.Daemon.Error != "" {
			fmt.Fprintf(w, "daemon error: %s\n", result.Daemon.Error)
		}
	} else {
		fmt.Fprintf(w, "health: %s\n", state)
		fmt.Fprintln(w, "daemon: not running")
	}
	if result.Declared.Persistent > 0 {
		fmt.Fprintf(w, "declared: %d persistent, %d running, %d missing\n",
			result.Declared.Persistent, result.Declared.Running, result.Declared.Missing)
	}
	fmt.Fprintf(w, "instances: %d total, %d running, %d stopped, %d exited, %d crashed, %d stale, %d runtime_stale, %d unhealthy\n",
		result.Summary.Total,
		result.Summary.Running,
		result.Summary.Stopped,
		result.Summary.Exited,
		result.Summary.Crashed,
		result.Summary.Stale,
		result.Summary.RuntimeStale,
		result.Summary.Unhealthy,
	)
	if result.Queue.Total > 0 || result.Queue.Quarantined > 0 {
		fmt.Fprintln(w, queueSummaryLine(result.Queue))
	}
	if result.JobQuarantine.Quarantined > 0 {
		fmt.Fprintln(w, jobQuarantineSummaryLine(result.JobQuarantine))
	}
	if result.OutboxQuarantine.Quarantined > 0 {
		fmt.Fprintln(w, outboxQuarantineSummaryLine(result.OutboxQuarantine))
	}
	if result.Intake.Deliveries > 0 {
		fmt.Fprintf(w, "intake: deliveries=%d errors=%d recovered=%d replayable=%d duplicate_request_ids=%d\n",
			result.Intake.Deliveries,
			result.Intake.Errors,
			result.Intake.Recovered,
			result.Intake.Replayable,
			result.Intake.DuplicateRequestIDs,
		)
	}
	if result.Jobs != nil {
		fmt.Fprintf(w, "jobs: total=%d queued=%d running=%d blocked=%d done=%d failed=%d attention=%d ready_steps=%d\n",
			result.Jobs.Summary.Total,
			result.Jobs.Summary.Queued,
			result.Jobs.Summary.Running,
			result.Jobs.Summary.Blocked,
			result.Jobs.Summary.Done,
			result.Jobs.Summary.Failed,
			len(result.Jobs.Attention),
			len(result.Jobs.ReadySteps),
		)
	}
	if result.JobStatus != nil {
		fmt.Fprintf(w, "job status: previews=%d changes=%d blocked=%d\n",
			len(result.JobStatus),
			countChangedJobStatusPreviews(result.JobStatus),
			countJobStatusPreviewsByAfter(result.JobStatus, job.StatusBlocked),
		)
	}
	if result.PipelineStatus != nil {
		fmt.Fprintf(w, "pipeline status: pipelines=%d jobs=%d ready_steps=%d manual_gates=%d stale_running_steps=%d failed_steps=%d\n",
			len(result.PipelineStatus),
			countPipelineStatusJobs(result.PipelineStatus),
			countPipelineStatusReadySteps(result.PipelineStatus),
			countPipelineStatusManualGates(result.PipelineStatus),
			countPipelineStatusStaleRunningSteps(result.PipelineStatus),
			countPipelineStatusFailedSteps(result.PipelineStatus),
		)
	}
	if result.PipelineDoctor != nil {
		fmt.Fprintf(w, "pipeline doctor: pipelines=%d problems=%d warnings=%d\n",
			len(result.PipelineDoctor.Pipelines),
			len(result.PipelineDoctor.Problems),
			countPipelineDoctorWarnings(result.PipelineDoctor),
		)
	}
	fmt.Fprint(w, "phases:")
	for _, phase := range lifecyclePhaseSummaryOrder() {
		fmt.Fprintf(w, " %s=%d", phase, result.Summary.Phases[phase])
	}
	fmt.Fprintln(w)
	if len(result.Issues) == 0 {
		return
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ISSUE\tINSTANCE\tDETAIL")
	for _, issue := range result.Issues {
		inst := issue.Instance
		if inst == "" {
			inst = "—"
		}
		detail := issue.Message
		parts := []string{}
		if issue.Job != "" {
			parts = append(parts, "job="+issue.Job)
		}
		if issue.Status != "" {
			parts = append(parts, "status="+issue.Status)
		}
		if issue.Phase != "" {
			parts = append(parts, "phase="+issue.Phase)
		}
		if len(issue.Actions) > 0 {
			parts = append(parts, "action="+strings.Join(issue.Actions, "; "))
		}
		if len(parts) > 0 {
			detail += " (" + strings.Join(parts, ", ") + ")"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", issue.Code, inst, detail)
	}
	_ = tw.Flush()
}

func countJobStatusPreviewsByAfter(results []jobStatusReconcileResult, status job.Status) int {
	count := 0
	for _, result := range results {
		if result.Changed && result.After == status {
			count++
		}
	}
	return count
}
