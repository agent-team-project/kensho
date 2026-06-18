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
		target          string
		jsonOut         bool
		quiet           bool
		watch           bool
		wait            bool
		noClear         bool
		format          string
		latest          bool
		last            int
		statusFilters   []string
		agentFilters    []string
		phaseFilters    []string
		instanceFilters []string
		staleOnly       bool
		unhealthyOnly   bool
		strictTopology  bool
		includeJobs     bool
		interval        time.Duration
		timeout         time.Duration
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Check daemon and instance fleet health.",
		Long: "Check the daemon, declared persistent instances, crashed instances, and stale status files. " +
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
			opts, err := newHealthOptionsWithInstancesAndUnhealthy(statusFilters, agentFilters, phaseFilters, instanceFilters, staleOnly, unhealthyOnly)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team health: %v\n", err)
				return exitErr(2)
			}
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
					return runHealthFormatWatch(ctx, cmd.OutOrStdout(), teamDir, interval, time.Now, opts, formatTemplate)
				}
				clear := !noClear && !jsonOut
				return runHealthWatchWithClear(ctx, cmd.OutOrStdout(), teamDir, interval, time.Now, jsonOut, opts, clear)
			}
			result, err := collectHealthWithOptions(teamDir, time.Now(), opts)
			if err != nil {
				return err
			}
			if !quiet {
				if err := writeHealthResultWithFormat(cmd.OutOrStdout(), result, jsonOut, formatTemplate); err != nil {
					return err
				}
			}
			if !result.Healthy {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress output and use only the exit code.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh health until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&wait, "wait", false, "Poll until the fleet is healthy, then exit.")
	cmd.Flags().StringVar(&format, "format", "", "Render the health result with a Go template, e.g. '{{.Healthy}} {{.Summary.Running}}'.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Only check the most recently started instance after other filters. Daemon health remains global.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Only check the N most recently started instances after other filters (0 = all). Daemon health remains global.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only check instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&agentFilters, "agent", nil, "Only check declared and daemon-known instances for this agent. Daemon health remains global. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only check instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&instanceFilters, "instance", nil, "Only check instances with this name. Daemon health remains global. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only check instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only check crashed or stale instances. Daemon health remains global.")
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
	filters, err := newPsOptionsWithInstancesAndUnhealthy(statusFilters, agentFilters, phaseFilters, instanceFilters, staleOnly, unhealthyOnly)
	if err != nil {
		return healthOptions{}, err
	}
	return healthOptions{filters: filters}, nil
}

type healthResult struct {
	Healthy   bool                       `json:"healthy"`
	Daemon    healthDaemon               `json:"daemon"`
	Summary   psSummaryJSON              `json:"summary"`
	Queue     queueSummary               `json:"queue"`
	Jobs      *jobTriageSnapshot         `json:"jobs,omitempty"`
	JobStatus []jobStatusReconcileResult `json:"job_status_preview,omitempty"`
	Declared  healthDeclared             `json:"declared"`
	Issues    []healthIssue              `json:"issues"`
	CheckedAt string                     `json:"checked_at"`
	Instances []healthInstance           `json:"instances,omitempty"`
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
	Instance string `json:"instance"`
	Agent    string `json:"agent"`
	Status   string `json:"status"`
	Phase    string `json:"phase"`
	Stale    bool   `json:"stale"`
	PID      int    `json:"pid,omitempty"`
}

func runHealthWatch(ctx context.Context, w io.Writer, teamDir string, interval time.Duration, now func() time.Time, jsonOut bool, opts healthOptions) error {
	return runHealthWatchWithClear(ctx, w, teamDir, interval, now, jsonOut, opts, false)
}

func runHealthWatchWithClear(ctx context.Context, w io.Writer, teamDir string, interval time.Duration, now func() time.Time, jsonOut bool, opts healthOptions, clear bool) error {
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

func runHealthFormatWatch(ctx context.Context, w io.Writer, teamDir string, interval time.Duration, now func() time.Time, opts healthOptions, tmpl *template.Template) error {
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
		if err := renderHealthFormat(w, result, tmpl); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
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
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
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
	if err := addQueueHealth(result, teamDir, now); err != nil {
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
			Instance: row.Instance,
			Agent:    row.Agent,
			Status:   psStatusKey(row),
			Phase:    psPhaseKey(row),
			Stale:    row.Stale,
			PID:      row.PID,
		})
		if row.Lifecycle == string(daemon.StatusCrashed) {
			result.addIssue("instance_crashed", row.Instance, string(daemon.StatusCrashed), psPhaseKey(row), fmt.Sprintf("instance %q crashed", row.Instance))
		}
		if row.Stale {
			result.addIssue("status_stale", row.Instance, psStatusKey(row), psPhaseKey(row), fmt.Sprintf("instance %q status is stale", row.Instance))
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
				result.addIssue("declared_missing", inst.Name, "unknown", "unknown", fmt.Sprintf("declared persistent instance %q has not been started", inst.Name))
				continue
			}
			if row.Lifecycle == string(daemon.StatusRunning) {
				result.Declared.Running++
				continue
			}
			status := psStatusKey(row)
			result.addIssue("declared_not_running", inst.Name, status, psPhaseKey(row), fmt.Sprintf("declared persistent instance %q is %s", inst.Name, status))
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
	if result.Queue.Dead > 0 {
		result.addIssue(
			"queue_dead_letter",
			"",
			"",
			"",
			fmt.Sprintf("queue has %d dead-letter item(s)", result.Queue.Dead),
		)
	}
	return nil
}

func addJobHealth(result *healthResult, teamDir string, now time.Time) error {
	if result == nil {
		return nil
	}
	snapshot, err := collectJobTriage(teamDir, now.UTC(), defaultJobTriageStaleAfter)
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
	if filters.unhealthy && !psRowUnhealthy(row) {
		return false
	}
	if len(filters.statuses) > 0 && !filters.statuses[psStatusKey(row)] {
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
	fmt.Fprintf(w, "instances: %d total, %d running, %d stopped, %d exited, %d crashed, %d stale\n",
		result.Summary.Total,
		result.Summary.Running,
		result.Summary.Stopped,
		result.Summary.Exited,
		result.Summary.Crashed,
		result.Summary.Stale,
	)
	if result.Queue.Total > 0 {
		fmt.Fprintf(w, "queue: total=%d pending=%d dead=%d delayed=%d attempts=%d\n",
			result.Queue.Total,
			result.Queue.Pending,
			result.Queue.Dead,
			result.Queue.Delayed,
			result.Queue.Attempts,
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
