package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newPlanCmd() *cobra.Command {
	var (
		target          string
		jsonOut         bool
		summary         bool
		stopExtras      bool
		statusFilters   []string
		runtimeFilters  []string
		agentFilters    []string
		phaseFilters    []string
		instanceFilters []string
		actionFilters   []string
		commands        bool
		format          string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Preview desired agent instance state from topology and daemon metadata.",
		Long: "Compare instances.toml with daemon metadata and show the lifecycle actions " +
			"agent-team would normally take: start missing persistent instances, resume stopped " +
			"ones when supported by the runtime, keep running ones, and leave ephemeral declarations on-demand. With --stop-extras, " +
			"running daemon-known instances not declared in topology are previewed as stop actions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team plan: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team plan: --commands cannot be combined with --summary.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team plan: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if format != "" && (jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team plan: --format cannot be combined with --json or --summary.")
				return exitErr(2)
			}
			formatTemplate, err := parsePlanFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team plan: %v\n", err)
				return exitErr(2)
			}
			opts, err := newPsOptionsWithRuntimeInstancesAndUnhealthy(statusFilters, runtimeFilters, agentFilters, phaseFilters, instanceFilters, false, false)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team plan: %v\n", err)
				return exitErr(2)
			}
			actions, err := planActionFilterSet(actionFilters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team plan: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			result, err := collectPlan(teamDir)
			if err != nil {
				return err
			}
			if stopExtras {
				markPlanStopExtras(result)
			}
			result.Instances = filterPlanRowsWithActions(result.Instances, opts, actions)
			result.Summary = summarizePlanRows(result.Instances)
			if commands {
				return renderPlanCommands(cmd.OutOrStdout(), result.Instances, planCommandOptions{
					BaseArgs:        []string{"agent-team", "sync"},
					TargetFlag:      "--target",
					Target:          target,
					TargetSet:       cmd.Flags().Changed("target"),
					StopExtras:      stopExtras,
					StatusFilters:   statusFilters,
					RuntimeFilters:  runtimeFilters,
					AgentFilters:    agentFilters,
					PhaseFilters:    phaseFilters,
					InstanceFilters: instanceFilters,
					ActionFilters:   actionFilters,
				})
			}
			if jsonOut {
				if summary {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(lifecycleActionSummaryResult{
						Summary: summarizeLifecycleActions(planRowsToLifecycleActionResults(result.Instances, true), true),
					})
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			if formatTemplate != nil {
				return renderPlanFormat(cmd.OutOrStdout(), result.Instances, formatTemplate)
			}
			if summary {
				renderLifecycleActionSummary(cmd.OutOrStdout(), summarizeLifecycleActions(planRowsToLifecycleActionResults(result.Instances, true), true))
				return nil
			}
			renderPlan(cmd.OutOrStdout(), result)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate action counts instead of per-instance rows.")
	cmd.Flags().BoolVar(&stopExtras, "stop-extras", false, "Preview running topology extras as stop actions, matching sync --stop-extras.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only show lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only show daemon-known plan rows for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&agentFilters, "agent", nil, "Only show plan rows for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only show plan rows in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&instanceFilters, "instance", nil, "Only show plan rows with this name. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&actionFilters, "action", nil, "Only show plan rows with this action: start, resume, keep, unsupported, on-demand, stop, or extra. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print the matching dry-run sync command when the plan has actionable work.")
	cmd.Flags().StringVar(&format, "format", "", "Render each plan row with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return cmd
}

type planResult struct {
	Daemon    planDaemon  `json:"daemon"`
	Summary   planSummary `json:"summary"`
	Instances []planRow   `json:"instances"`
}

type planDaemon struct {
	Running bool `json:"running"`
	PID     int  `json:"pid,omitempty"`
}

type planSummary struct {
	Total       int `json:"total"`
	Start       int `json:"start"`
	Resume      int `json:"resume"`
	Keep        int `json:"keep"`
	Unsupported int `json:"unsupported,omitempty"`
	OnDemand    int `json:"on_demand"`
	Stop        int `json:"stop,omitempty"`
	Extra       int `json:"extra"`
}

type planRow struct {
	Instance string `json:"instance"`
	Agent    string `json:"agent,omitempty"`
	Runtime  string `json:"-"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	Phase    string `json:"phase"`
	Action   string `json:"action"`
	Detail   string `json:"detail,omitempty"`
	PID      int    `json:"pid,omitempty"`
}

func collectPlan(teamDir string) (*planResult, error) {
	pid, daemonRunning := daemonAlive(teamDir)
	metas, err := planMetadata(teamDir, daemonRunning)
	if err != nil {
		return nil, err
	}
	topo, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return nil, err
	}
	rows := buildPlanRows(topo, metas, daemonRunning)
	annotatePlanPhases(rows, statusPhaseByInstance(teamDir, time.Now()))
	result := &planResult{
		Daemon: planDaemon{
			Running: daemonRunning,
			PID:     pid,
		},
		Instances: rows,
	}
	if !daemonRunning {
		result.Daemon.PID = 0
	}
	result.Summary = summarizePlanRows(rows)
	return result, nil
}

func planMetadata(teamDir string, daemonRunning bool) ([]*daemon.Metadata, error) {
	if daemonRunning {
		dc, err := newDaemonClient(teamDir)
		if err != nil {
			return nil, err
		}
		return dc.Instances()
	}
	return daemon.ListMetadata(daemon.DaemonRoot(teamDir))
}

func buildPlanRows(topo *topology.Topology, metas []*daemon.Metadata, daemonRunning bool) []planRow {
	metaByName := lifecycleMetadataByName(metas)
	seen := map[string]bool{}
	var rows []planRow
	if topo != nil {
		for _, inst := range topo.SortedInstances() {
			meta := metaByName[inst.Name]
			rows = append(rows, planDeclaredRow(inst, meta, daemonRunning))
			seen[inst.Name] = true
		}
	}
	extras := make([]*daemon.Metadata, 0, len(metas))
	for _, meta := range metas {
		if !seen[meta.Instance] {
			extras = append(extras, meta)
		}
	}
	sort.Slice(extras, func(i, j int) bool { return extras[i].Instance < extras[j].Instance })
	for _, meta := range extras {
		if owner, ok := declaredEphemeralOwner(topo, meta.Instance, meta.Agent); ok {
			rows = append(rows, planEphemeralChildRow(owner, meta, daemonRunning))
			continue
		}
		rows = append(rows, planExtraRow(meta, daemonRunning))
	}
	return rows
}

func annotatePlanPhases(rows []planRow, phaseByInstance map[string]string) {
	for i := range rows {
		rows[i].Phase = planPhaseKey(phaseByInstance[rows[i].Instance])
	}
}

func planDeclaredRow(inst *topology.Instance, meta *daemon.Metadata, daemonRunning bool) planRow {
	kind := "persistent"
	if inst.Ephemeral {
		kind = "ephemeral"
	}
	row := planRow{
		Instance: inst.Name,
		Agent:    inst.Agent,
		Kind:     kind,
		Status:   "unknown",
	}
	if meta != nil {
		row.Status = planStatus(meta)
		row.Runtime = metadataRuntimeKey(meta)
		row.PID = meta.PID
		if row.Agent == "" {
			row.Agent = meta.Agent
		}
	}
	if inst.Ephemeral {
		row.Action = "on-demand"
		row.Detail = "ephemeral declaration starts from triggers or agent-team run"
		if meta != nil && meta.Status == daemon.StatusRunning {
			row.Action = "keep"
			row.Detail = "ephemeral instance is already running"
		}
		return row
	}
	if meta == nil {
		row.Action = "start"
		row.Detail = "declared persistent instance has no daemon metadata"
		return row
	}
	if meta.Status == daemon.StatusRunning {
		row.Action = "keep"
		row.Detail = "already running"
		if !daemonRunning && (meta.PID == 0 || !daemon.PidLiveCheck(meta.PID)) {
			if lifecycleMetadataSupportsManagedResume(meta) {
				row.Action = "resume"
				row.Detail = "recorded running pid is not live; daemon start should reconcile"
			} else {
				row.Action = lifecycleActionUnsupported
				row.Detail = lifecycleStaleUnsupportedResumeDetailForInstance(meta, inst.Name)
			}
		}
		return row
	}
	if !lifecycleMetadataSupportsManagedResume(meta) {
		row.Action = lifecycleActionUnsupported
		row.Detail = lifecycleUnsupportedResumeDetailForInstance(meta, inst.Name)
		return row
	}
	row.Action = "resume"
	row.Detail = "daemon metadata can be resumed"
	return row
}

func planEphemeralChildRow(owner *topology.Instance, meta *daemon.Metadata, daemonRunning bool) planRow {
	agent := meta.Agent
	if agent == "" && owner != nil {
		agent = owner.Agent
	}
	status := planStatus(meta)
	row := planRow{
		Instance: meta.Instance,
		Agent:    agent,
		Runtime:  metadataRuntimeKey(meta),
		Kind:     "ephemeral",
		Status:   status,
		Action:   "keep",
		Detail:   fmt.Sprintf("ephemeral child of %q", owner.Name),
		PID:      meta.PID,
	}
	if meta.Status != daemon.StatusRunning {
		row.Action = "extra"
		row.Detail = fmt.Sprintf("finished ephemeral child of %q should be pruned", owner.Name)
		return row
	}
	if !daemonRunning && (meta.PID == 0 || !daemon.PidLiveCheck(meta.PID)) {
		row.Action = "extra"
		row.Detail = fmt.Sprintf("ephemeral child of %q has a non-live recorded pid", owner.Name)
	}
	return row
}

func planExtraRow(meta *daemon.Metadata, daemonRunning bool) planRow {
	row := planRow{
		Instance: meta.Instance,
		Agent:    meta.Agent,
		Runtime:  metadataRuntimeKey(meta),
		Kind:     "extra",
		Status:   planStatus(meta),
		Action:   "extra",
		Detail:   "daemon-known instance is not declared in topology",
		PID:      meta.PID,
	}
	if !daemonRunning && meta.Status == daemon.StatusRunning && (meta.PID == 0 || !daemon.PidLiveCheck(meta.PID)) {
		row.Detail = "daemon-known instance is not declared; recorded running pid is not live"
	}
	return row
}

func planStatus(meta *daemon.Metadata) string {
	if meta == nil || meta.Status == "" {
		return "unknown"
	}
	return string(meta.Status)
}

func planPhaseKey(raw string) string {
	return psPhaseKey(instanceRow{Phase: raw})
}

func planActionFilterSet(actionFilters []string) (map[string]bool, error) {
	if len(actionFilters) == 0 {
		return nil, nil
	}
	actions := map[string]bool{}
	for _, raw := range splitFilterValues(actionFilters) {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		action, ok := normalizePlanAction(raw)
		if !ok {
			return nil, fmt.Errorf("unknown --action %q (want start, resume, keep, unsupported, on-demand, stop, or extra)", raw)
		}
		actions[action] = true
	}
	if len(actions) == 0 {
		return nil, errors.New("--action requires at least one non-empty action")
	}
	return actions, nil
}

func normalizePlanAction(raw string) (string, bool) {
	action := strings.ToLower(strings.TrimSpace(raw))
	switch action {
	case "":
		return "", false
	case "start", "resume", "keep", lifecycleActionUnsupported, "stop", "extra":
		return action, true
	case "on-demand", "on_demand", "ondemand":
		return "on-demand", true
	default:
		return "", false
	}
}

func summarizePlanRows(rows []planRow) planSummary {
	var summary planSummary
	summary.Total = len(rows)
	for _, row := range rows {
		switch row.Action {
		case "start":
			summary.Start++
		case "resume":
			summary.Resume++
		case "keep":
			summary.Keep++
		case lifecycleActionUnsupported:
			summary.Unsupported++
		case "on-demand":
			summary.OnDemand++
		case "stop":
			summary.Stop++
		case "extra":
			summary.Extra++
		}
	}
	return summary
}

func renderPlan(w fmtWriter, result *planResult) {
	if result.Daemon.Running {
		fmt.Fprintf(w, "daemon: running (pid=%d)\n", result.Daemon.PID)
	} else {
		fmt.Fprintln(w, "daemon: not running")
	}
	fmt.Fprintln(w)
	renderPlanTable(w, result)
}

func renderPlanTable(w fmtWriter, result *planResult) {
	if len(result.Instances) == 0 {
		fmt.Fprintln(w, "(no planned instances)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "INSTANCE\tAGENT\tKIND\tSTATUS\tPHASE\tACTION\tDETAIL")
	for _, row := range result.Instances {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", row.Instance, row.Agent, row.Kind, row.Status, row.Phase, row.Action, row.Detail)
	}
	_ = tw.Flush()
	fmt.Fprintf(w, "\nsummary: total=%d start=%d resume=%d keep=%d on-demand=%d",
		result.Summary.Total,
		result.Summary.Start,
		result.Summary.Resume,
		result.Summary.Keep,
		result.Summary.OnDemand,
	)
	if result.Summary.Stop > 0 {
		fmt.Fprintf(w, " stop=%d", result.Summary.Stop)
	}
	if result.Summary.Unsupported > 0 {
		fmt.Fprintf(w, " unsupported=%d", result.Summary.Unsupported)
	}
	fmt.Fprintf(w, " extra=%d\n", result.Summary.Extra)
}

func parsePlanFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("plan-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderPlanFormat(w fmtWriter, rows []planRow, tmpl *template.Template) error {
	for _, row := range rows {
		if err := tmpl.Execute(w, row); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}

type planCommandOptions struct {
	BaseArgs        []string
	TargetFlag      string
	Target          string
	TargetSet       bool
	StopExtras      bool
	StatusFilters   []string
	RuntimeFilters  []string
	AgentFilters    []string
	PhaseFilters    []string
	InstanceFilters []string
	ActionFilters   []string
}

func renderPlanCommands(w fmtWriter, rows []planRow, opts planCommandOptions) error {
	if !planHasActionableSyncRows(rows) {
		return nil
	}
	args := append([]string{}, opts.BaseArgs...)
	if opts.TargetSet && strings.TrimSpace(opts.Target) != "" {
		args = append(args, opts.TargetFlag, opts.Target)
	}
	args = append(args, "--dry-run")
	if opts.StopExtras {
		args = append(args, "--stop-extras")
	}
	args = appendPlanCommandFilterArgs(args, "--status", opts.StatusFilters)
	args = appendPlanCommandFilterArgs(args, "--runtime", opts.RuntimeFilters)
	args = appendPlanCommandFilterArgs(args, "--agent", opts.AgentFilters)
	args = appendPlanCommandFilterArgs(args, "--phase", opts.PhaseFilters)
	args = appendPlanCommandFilterArgs(args, "--instance", opts.InstanceFilters)
	args = appendPlanCommandFilterArgs(args, "--action", opts.ActionFilters)
	_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(args), " "))
	return err
}

func planHasActionableSyncRows(rows []planRow) bool {
	for _, row := range rows {
		switch row.Action {
		case "start", "resume", "stop":
			return true
		}
	}
	return false
}

func appendPlanCommandFilterArgs(args []string, flag string, values []string) []string {
	for _, value := range splitFilterValues(values) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		args = append(args, flag, value)
	}
	return args
}
