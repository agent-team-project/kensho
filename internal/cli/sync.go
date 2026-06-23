package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	var (
		target       string
		dryRun       bool
		wait         bool
		stopExtras   bool
		timeout      time.Duration
		readyTimeout time.Duration
		summary      bool
		quiet        bool
		jsonOut      bool
		format       string
		statuses     []string
		agents       []string
		phases       []string
		instances    []string
		actions      []string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Apply topology's desired persistent instance state.",
		Long: "Reload daemon topology, reconcile runtime metadata, then start or resume declared " +
			"persistent instances when supported by the runtime. Sync is intentionally non-destructive: daemon-known instances " +
			"that are not declared in topology are reported by plan but are not stopped or removed " +
			"unless --stop-extras is set.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if timeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team sync: --timeout must be >= 0.")
				return exitErr(2)
			}
			if readyTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team sync: --ready-timeout must be >= 0.")
				return exitErr(2)
			}
			if dryRun && wait {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team sync: --dry-run cannot be combined with --wait.")
				return exitErr(2)
			}
			if quiet && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team sync: choose one of --quiet or --json.")
				return exitErr(2)
			}
			if quiet && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team sync: choose one of --quiet or --summary.")
				return exitErr(2)
			}
			if format != "" && (quiet || jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team sync: --format cannot be combined with --quiet, --json, or --summary.")
				return exitErr(2)
			}
			filters, err := newPsOptionsWithInstances(statuses, agents, phases, instances, false)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team sync: %v\n", err)
				return exitErr(2)
			}
			actionFilters, err := planActionFilterSet(actions)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team sync: %v\n", err)
				return exitErr(2)
			}
			formatTemplate, err := parseLifecycleActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team sync: %v\n", err)
				return exitErr(2)
			}
			return runSync(cmd, target, syncOptions{
				DryRun:       dryRun,
				Wait:         wait,
				StopExtras:   stopExtras,
				Timeout:      timeout,
				ReadyTimeout: readyTimeout,
				Summary:      summary,
				Quiet:        quiet,
				JSON:         jsonOut,
				Format:       formatTemplate,
				Filters:      filters,
				Actions:      actionFilters,
			})
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview topology convergence without starting the daemon or instances.")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for selected instances to become healthy after syncing. With no filters, waits for the fleet.")
	cmd.Flags().BoolVar(&stopExtras, "stop-extras", false, "Also stop running daemon-known instances not declared in instances.toml.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&readyTimeout, "ready-timeout", defaultDaemonReadyTimeout, "Maximum time to wait for implicit daemon readiness (0 = no timeout).")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate action counts instead of per-instance rows.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each sync action with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "Only sync plan rows with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&agents, "agent", nil, "Only sync plan rows for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phases, "phase", nil, "Only sync plan rows in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&instances, "instance", nil, "Only sync plan rows with this name. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&actions, "action", nil, "Only sync plan rows with this action: start, resume, keep, unsupported, on-demand, stop, or extra. Can repeat or comma-separate.")
	return cmd
}

type syncOptions struct {
	DryRun       bool
	Wait         bool
	StopExtras   bool
	Timeout      time.Duration
	ReadyTimeout time.Duration
	Summary      bool
	Quiet        bool
	JSON         bool
	Format       *template.Template
	Filters      psOptions
	Actions      map[string]bool
}

func runSync(cmd *cobra.Command, target string, opts syncOptions) error {
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	if opts.DryRun {
		result, err := collectPlan(teamDir)
		if err != nil {
			return err
		}
		if opts.StopExtras {
			markPlanStopExtras(result)
		}
		filterSyncPlan(result, opts.Filters, opts.Actions)
		if opts.JSON {
			if opts.Summary {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(lifecycleActionSummaryResult{
					Summary: summarizeLifecycleActions(planRowsToLifecycleActionResults(result.Instances, true), true),
				})
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		}
		if opts.Quiet {
			return nil
		}
		if opts.Format != nil {
			return renderPlanFormat(cmd.OutOrStdout(), result.Instances, opts.Format)
		}
		if opts.Summary {
			renderLifecycleActionSummary(cmd.OutOrStdout(), summarizeLifecycleActions(planRowsToLifecycleActionResults(result.Instances, true), true))
			return nil
		}
		renderPlan(cmd.OutOrStdout(), result)
		return nil
	}
	if err := ensureDaemonReadyWithTimeout(cmd, target, opts.JSON || opts.Quiet, opts.ReadyTimeout); err != nil {
		return err
	}
	dc, err := newDaemonClient(teamDir)
	if err != nil {
		if errors.Is(err, errDaemonNotRunning) {
			fmt.Fprintln(cmd.ErrOrStderr(), "agent-team sync: daemon is not running — start it with `agent-team start`.")
			return exitErr(1)
		}
		return err
	}
	if _, err := dc.TopologyReload(); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team sync: reload: %v\n", err)
		return exitErr(1)
	}
	if _, err := dc.Reconcile(); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team sync: reconcile: %v\n", err)
		return exitErr(1)
	}
	if opts.StopExtras {
		topo, err := topology.LoadFromTeamDir(teamDir)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team sync: %v\n", err)
			return exitErr(1)
		}
		return runSyncWithStopExtras(cmd, target, teamDir, dc, topo, opts)
	}
	names, filtered, err := syncTargetNamesFromCurrentPlan(teamDir, opts.Filters, opts.Actions)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team sync: %v\n", err)
		return exitErr(1)
	}
	if filtered && len(names) == 0 {
		return renderSyncNoActions(cmd.OutOrStdout(), opts)
	}
	return runInstanceUpWithOptions(cmd, target, "", names, instanceUpOptions{
		Wait:    opts.Wait,
		Timeout: opts.Timeout,
		Summary: opts.Summary,
		Quiet:   opts.Quiet,
		JSON:    opts.JSON,
		Format:  opts.Format,
		Health:  syncWaitHealthOptionsForNames(opts.Filters, opts.Actions, names),
	})
}

func markPlanStopExtras(result *planResult) {
	if result == nil {
		return
	}
	for i := range result.Instances {
		row := &result.Instances[i]
		if row.Kind != "extra" || row.Status != string(daemon.StatusRunning) {
			continue
		}
		if !result.Daemon.Running && (row.PID == 0 || !daemon.PidLiveCheck(row.PID)) {
			continue
		}
		row.Action = "stop"
		row.Detail = "running topology extra would be stopped by --stop-extras"
	}
	result.Summary = summarizePlanRows(result.Instances)
}

func filterSyncPlan(result *planResult, filters psOptions, actions map[string]bool) {
	if result == nil {
		return
	}
	result.Instances = filterPlanRowsWithActions(result.Instances, filters, actions)
	result.Summary = summarizePlanRows(result.Instances)
}

func syncFiltersSet(filters psOptions, actions map[string]bool) bool {
	return len(filters.statuses) > 0 ||
		len(filters.agents) > 0 ||
		len(filters.phases) > 0 ||
		len(filters.instances) > 0 ||
		len(actions) > 0
}

func syncTargetNamesFromCurrentPlan(teamDir string, filters psOptions, actions map[string]bool) ([]string, bool, error) {
	if !syncFiltersSet(filters, actions) {
		return nil, false, nil
	}
	result, err := collectPlan(teamDir)
	if err != nil {
		return nil, true, err
	}
	filterSyncPlan(result, filters, actions)
	names := make([]string, 0, len(result.Instances))
	for _, row := range result.Instances {
		switch row.Action {
		case "start", "resume", "keep", lifecycleActionUnsupported:
			if row.Kind == "persistent" {
				names = append(names, row.Instance)
			}
		}
	}
	return names, true, nil
}

func renderSyncNoActions(w io.Writer, opts syncOptions) error {
	if opts.JSON {
		if opts.Summary {
			return json.NewEncoder(w).Encode(lifecycleActionSummaryResult{
				Summary: summarizeLifecycleActions(nil, opts.DryRun),
			})
		}
		if opts.Wait {
			return json.NewEncoder(w).Encode(lifecycleHealthResult{Actions: []lifecycleActionResult{}})
		}
		return json.NewEncoder(w).Encode([]lifecycleActionResult{})
	}
	if opts.Quiet || opts.Format != nil {
		return nil
	}
	if opts.Summary {
		renderLifecycleActionSummary(w, summarizeLifecycleActions(nil, opts.DryRun))
		return nil
	}
	fmt.Fprintln(w, "(no instances)")
	return nil
}

func planRowsToLifecycleActionResults(rows []planRow, dryRun bool) []lifecycleActionResult {
	results := make([]lifecycleActionResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, lifecycleActionResult{
			Action:   row.Action,
			Instance: row.Instance,
			Agent:    row.Agent,
			Status:   row.Status,
			PID:      row.PID,
			Detail:   row.Detail,
			DryRun:   dryRun,
		})
	}
	return results
}

func runSyncWithStopExtras(cmd *cobra.Command, target, teamDir string, dc *daemonClient, topo *topology.Topology, opts syncOptions) error {
	metas, err := dc.Instances()
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team sync: %v\n", err)
		return exitErr(1)
	}
	out := cmd.OutOrStdout()
	results := syncStopExtraResults(out, dc, topo, metas, statusPhaseByInstance(teamDir, time.Now()), opts)
	metas, err = dc.Instances()
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team sync: %v\n", err)
		return exitErr(1)
	}
	names, filtered, err := syncTargetNamesFromCurrentPlan(teamDir, opts.Filters, opts.Actions)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team sync: %v\n", err)
		return exitErr(1)
	}
	if filtered && len(names) == 0 {
		return renderSyncActionResults(cmd, teamDir, dc, results, opts)
	}
	targets, err := selectLifecycleTargets(topo, metas, names)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team sync: %v\n", err)
		return exitErr(2)
	}
	for _, lt := range targets {
		if lt.running() {
			result := lifecycleActionResult{
				Action:   "skip",
				Instance: lt.name,
				Agent:    lt.agent,
				Status:   string(daemon.StatusRunning),
				Detail:   "already running",
			}
			if lt.meta != nil {
				result.PID = lt.meta.PID
			}
			results = append(results, result)
			if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
				fmt.Fprintf(out, "  skip   %-20s already running\n", lt.name)
			}
			continue
		}
		if lt.meta != nil {
			if !lifecycleMetadataSupportsManagedResume(lt.meta) {
				result := lifecycleTargetUnsupportedResumeResult(lt)
				results = append(results, result)
				if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
					fmt.Fprintf(out, "  %-7s %-20s %s\n", result.Action, lt.name, result.Detail)
				}
				continue
			}
			if err := dc.StartInstance(lt.name); err != nil {
				results = append(results, lifecycleActionResult{Action: "error", Instance: lt.name, Agent: lt.agent, Status: "error", Error: err.Error()})
				if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
					fmt.Fprintf(out, "  error  %-20s %v\n", lt.name, err)
				}
				continue
			}
			results = append(results, lifecycleActionResult{Action: "resume", Instance: lt.name, Agent: lt.agent})
			if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
				fmt.Fprintf(out, "  resume %-20s %s\n", lt.name, lt.agent)
			}
			continue
		}
		kickoff := fmt.Sprintf("Topology bring-up: you are %q, an instance of %q.", lt.name, lt.agent)
		runErr := runMaybeSuppressStdout(cmd, opts.JSON || opts.Quiet || opts.Format != nil || opts.Summary, func() error {
			return upOne(cmd, target, lt.declared, kickoff)
		})
		if runErr != nil {
			results = append(results, lifecycleActionResult{Action: "error", Instance: lt.name, Agent: lt.agent, Status: "error", Error: runErr.Error()})
			if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
				fmt.Fprintf(out, "  error  %-20s %v\n", lt.name, runErr)
			}
			continue
		}
		results = append(results, lifecycleActionResult{Action: "start", Instance: lt.name, Agent: lt.agent})
		if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
			fmt.Fprintf(out, "  start  %-20s %s\n", lt.name, lt.agent)
		}
	}
	return renderSyncActionResults(cmd, teamDir, dc, results, opts)
}

func renderSyncActionResults(cmd *cobra.Command, teamDir string, dc *daemonClient, results []lifecycleActionResult, opts syncOptions) error {
	out := cmd.OutOrStdout()
	enriched := enrichLifecycleResults(dc, results)
	var health *healthResult
	healthWaitTimedOut := false
	var err error
	if opts.Wait {
		ctx := cmd.Context()
		cancel := func() {}
		if opts.Timeout > 0 {
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		}
		defer cancel()
		health, healthWaitTimedOut, err = runHealthWaitWithOutcome(ctx, teamDir, 500*time.Millisecond, time.Now, syncWaitHealthOptionsForResults(opts.Filters, opts.Actions, enriched))
		if err != nil {
			return err
		}
	}
	if opts.JSON {
		if opts.Summary {
			body := lifecycleActionSummaryResult{Summary: summarizeLifecycleActions(enriched, false)}
			if opts.Wait {
				body.Health = health
			}
			if err := json.NewEncoder(out).Encode(body); err != nil {
				return err
			}
			if lifecycleActionResultsHaveErrors(enriched) {
				return exitErr(1)
			}
			if health != nil && !health.Healthy {
				reportLifecycleHealthWaitTimeout(cmd, opts.Quiet, healthWaitTimedOut, health)
				return exitErr(1)
			}
			return nil
		}
		if opts.Wait {
			if err := json.NewEncoder(out).Encode(lifecycleHealthResult{Actions: enriched, Health: health}); err != nil {
				return err
			}
		} else if err := json.NewEncoder(out).Encode(enriched); err != nil {
			return err
		}
		if lifecycleActionResultsHaveErrors(enriched) {
			return exitErr(1)
		}
		if health != nil && !health.Healthy {
			reportLifecycleHealthWaitTimeout(cmd, opts.Quiet, healthWaitTimedOut, health)
			return exitErr(1)
		}
		return nil
	}
	if opts.Format != nil {
		if err := renderLifecycleActionFormat(out, enriched, opts.Format); err != nil {
			return err
		}
		if lifecycleActionResultsHaveErrors(enriched) {
			return exitErr(1)
		}
		if opts.Wait && health != nil && !health.Healthy {
			reportLifecycleHealthWaitTimeout(cmd, opts.Quiet, healthWaitTimedOut, health)
			return exitErr(1)
		}
		return nil
	}
	if opts.Summary {
		renderLifecycleActionSummary(out, summarizeLifecycleActions(enriched, false))
		if opts.Wait && !opts.Quiet {
			fmt.Fprintln(out)
			renderHealth(out, health)
		}
		if lifecycleActionResultsHaveErrors(enriched) {
			return exitErr(1)
		}
		if health != nil && !health.Healthy {
			reportLifecycleHealthWaitTimeout(cmd, opts.Quiet, healthWaitTimedOut, health)
			return exitErr(1)
		}
		return nil
	}
	if opts.Wait {
		if !opts.Quiet {
			fmt.Fprintln(out)
			renderHealth(out, health)
		}
		if health != nil && !health.Healthy {
			reportLifecycleHealthWaitTimeout(cmd, opts.Quiet, healthWaitTimedOut, health)
			return exitErr(1)
		}
	}
	if lifecycleActionResultsHaveErrors(enriched) {
		return exitErr(1)
	}
	return nil
}

func syncStopExtraResults(w io.Writer, dc *daemonClient, topo *topology.Topology, metas []*daemon.Metadata, phaseByInstance map[string]string, opts syncOptions) []lifecycleActionResult {
	if len(opts.Actions) > 0 && !opts.Actions["stop"] {
		return nil
	}
	declared := map[string]bool{}
	if topo != nil {
		for _, inst := range topo.SortedInstances() {
			declared[inst.Name] = true
		}
	}
	extras := make([]*daemon.Metadata, 0, len(metas))
	for _, meta := range metas {
		if meta.Status != daemon.StatusRunning || declared[meta.Instance] {
			continue
		}
		if _, ok := declaredEphemeralOwner(topo, meta.Instance, meta.Agent); ok {
			continue
		}
		if !syncMetadataMatchesFilters(meta, opts.Filters, phaseByInstance) {
			continue
		}
		extras = append(extras, meta)
	}
	sort.Slice(extras, func(i, j int) bool { return extras[i].Instance < extras[j].Instance })
	results := make([]lifecycleActionResult, 0, len(extras))
	for _, meta := range extras {
		result := lifecycleActionResult{
			Action:   "stop",
			Instance: meta.Instance,
			Agent:    meta.Agent,
			Status:   string(daemon.StatusStopped),
			PID:      meta.PID,
			Detail:   "topology extra",
		}
		if err := dc.StopInstanceWithOptions(meta.Instance, false, 0); err != nil {
			result.Action = "error"
			result.Status = "error"
			result.Error = err.Error()
			if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
				fmt.Fprintf(w, "  error  %-20s %v\n", meta.Instance, err)
			}
		} else if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
			fmt.Fprintf(w, "  stop   %-20s topology extra\n", meta.Instance)
		}
		results = append(results, result)
	}
	return results
}

func syncMetadataMatchesFilters(meta *daemon.Metadata, filters psOptions, phaseByInstance map[string]string) bool {
	if meta == nil {
		return false
	}
	if len(filters.instances) > 0 && !filters.instances[meta.Instance] {
		return false
	}
	if len(filters.agents) > 0 && !filters.agents[meta.Agent] {
		return false
	}
	status := metadataStatusKey(meta)
	if len(filters.statuses) > 0 && !filters.statuses[status] {
		return false
	}
	if len(filters.phases) > 0 && !filters.phases[planPhaseKey(phaseByInstance[meta.Instance])] {
		return false
	}
	return true
}

func syncWaitHealthOptionsForNames(filters psOptions, actions map[string]bool, names []string) healthOptions {
	if !syncFiltersSet(filters, actions) {
		return healthOptions{}
	}
	instances := make(map[string]bool, len(names))
	for _, name := range names {
		if name != "" {
			instances[name] = true
		}
	}
	if len(instances) == 0 {
		return healthOptions{filters: filters}
	}
	return healthOptions{filters: psOptions{instances: instances}}
}

func syncWaitHealthOptionsForResults(filters psOptions, actions map[string]bool, results []lifecycleActionResult) healthOptions {
	if !syncFiltersSet(filters, actions) {
		return healthOptions{}
	}
	instances := make(map[string]bool, len(results))
	for _, result := range results {
		if result.Instance != "" {
			instances[result.Instance] = true
		}
	}
	if len(instances) == 0 {
		return healthOptions{filters: filters}
	}
	return healthOptions{filters: psOptions{instances: instances}}
}
