package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/topology"
)

// newTopologyCmd registers the `topology` group: read-only inspection of the
// declared topology plus an explicit `reload`. Uses the daemon's
// /v1/topology endpoints when running; falls back to local file parsing so
// `agent-team topology` is useful even before the daemon is started.
func newTopologyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "topology",
		Short: "Show declared instances and triggers (reads .agent_team/instances.toml).",
	}
	cmd.AddCommand(newTopologyShowCmd())
	cmd.AddCommand(newTopologyGraphCmd())
	cmd.AddCommand(newTopologySummaryCmd())
	cmd.AddCommand(newTopologyReloadCmd())
	return cmd
}

func newTopologyShowCmd() *cobra.Command {
	var (
		target string
		asJSON bool
	)
	cwd, _ := os.Getwd()
	c := &cobra.Command{
		Use:   "show",
		Short: "Print the resolved topology (declared instances + triggers).",
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			return runTopologyShow(cmd, teamDir, asJSON)
		},
	}
	c.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	c.Flags().BoolVar(&asJSON, "json", false, "Emit raw JSON.")
	return c
}

func newTopologyGraphCmd() *cobra.Command {
	var (
		target        string
		graphFormat   string
		includeRoutes bool
		jsonOut       bool
		jobID         string
		commands      bool
	)
	cwd, _ := os.Getwd()
	c := &cobra.Command{
		Use:   "graph",
		Short: "Render a full topology graph.",
		Long:  "Render a read-only graph of declared teams, instances, pipelines, schedules, and dispatch wiring.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOut && cmd.Flags().Changed("format") {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team topology graph: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team topology graph: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && cmd.Flags().Changed("format") {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team topology graph: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			format, err := parsePipelineGraphFormat(graphFormat)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team topology graph: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			graph, err := collectTopologyGraph(teamDir, includeRoutes, jobID)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team topology graph: %v\n", err)
				return exitErr(1)
			}
			if commands {
				return renderTopologyGraphCommands(cmd.OutOrStdout(), graph, operatorCommandScopeFromCommand(cmd, target, "target"))
			}
			return renderTopologyGraph(cmd.OutOrStdout(), graph, format, jsonOut)
		},
	}
	c.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	c.Flags().StringVar(&graphFormat, "format", "text", "Graph output format: text, mermaid, or dot.")
	c.Flags().BoolVar(&includeRoutes, "routes", false, "Annotate pipeline steps with matching agent.dispatch routes.")
	c.Flags().BoolVar(&jsonOut, "json", false, "Emit graph nodes and edges as JSON.")
	c.Flags().StringVar(&jobID, "job", "", "Overlay durable job step state on its declared pipeline graph.")
	c.Flags().BoolVar(&commands, "commands", false, "Print recommended commands from graph action hints, one per line. agent-team follow-ups preserve the selected repo scope.")
	return c
}

func newTopologySummaryCmd() *cobra.Command {
	var (
		target string
		asJSON bool
	)
	cwd, _ := os.Getwd()
	c := &cobra.Command{
		Use:   "summary",
		Short: "Summarize declared topology and workflow health.",
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			summary, err := collectTopologySummary(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team topology summary: %v\n", err)
				return exitErr(1)
			}
			return renderTopologySummary(cmd.OutOrStdout(), summary, asJSON)
		},
	}
	c.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	c.Flags().BoolVar(&asJSON, "json", false, "Emit topology summary as JSON.")
	return c
}

func newTopologyReloadCmd() *cobra.Command {
	var (
		target  string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	c := &cobra.Command{
		Use:   "reload",
		Short: "Re-read instances.toml from disk (daemon must be running).",
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team topology reload: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseReloadFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team topology reload: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			dc, err := newDaemonClient(teamDir)
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team topology reload: daemon is not running — start it first with `agent-team daemon start`.")
				return exitErr(2)
			}
			res, err := dc.TopologyReload()
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team topology reload: %v\n", err)
				return exitErr(1)
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(res)
			}
			if tmpl != nil {
				if err := tmpl.Execute(cmd.OutOrStdout(), res); err != nil {
					return err
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout())
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Reloaded — %d instance(s) declared.\n", len(res.Instances))
			return nil
		},
	}
	c.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	c.Flags().BoolVar(&jsonOut, "json", false, "Emit reloaded topology as JSON.")
	c.Flags().StringVar(&format, "format", "", "Render reload result with a Go template, e.g. '{{len .Instances}}'.")
	return c
}

// runTopologyShow prints either the daemon's view (if running, includes
// runtime running/queued counts) or a file-only view.
func runTopologyShow(cmd *cobra.Command, teamDir string, asJSON bool) error {
	// Prefer daemon-sourced topology — it includes per-instance running
	// counters. Fall back to parsing instances.toml ourselves so the command
	// is useful before the daemon is started.
	if dc, err := newDaemonClient(teamDir); err == nil {
		res, err := dc.Topology()
		if err == nil {
			if asJSON {
				body, _ := json.MarshalIndent(res, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(body))
				return nil
			}
			printDaemonTopology(cmd.OutOrStdout(), res)
			return nil
		}
	}
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(1)
	}
	if top == nil || len(top.Instances) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(no instances declared — add .agent_team/instances.toml)")
		return nil
	}
	if asJSON {
		// Mirror the daemon shape so consumers don't branch.
		body, _ := json.MarshalIndent(toResponseLike(top), "", "  ")
		fmt.Fprintln(cmd.OutOrStdout(), string(body))
		return nil
	}
	printLocalTopology(cmd.OutOrStdout(), top)
	return nil
}

type topologySummary struct {
	OK               bool `json:"ok"`
	Instances        int  `json:"instances"`
	Persistent       int  `json:"persistent"`
	Ephemeral        int  `json:"ephemeral"`
	Triggers         int  `json:"triggers"`
	Pipelines        int  `json:"pipelines"`
	PipelineSteps    int  `json:"pipeline_steps"`
	PipelineProblems int  `json:"pipeline_problems"`
	PipelineWarnings int  `json:"pipeline_warnings"`
	Schedules        int  `json:"schedules"`
	Teams            int  `json:"teams"`
	TeamProblems     int  `json:"team_problems"`
	TeamWarnings     int  `json:"team_warnings"`
}

type topologyGraph struct {
	Instances []teamGraphInstance `json:"instances,omitempty"`
	Pipelines []pipelineGraph     `json:"pipelines,omitempty"`
	Schedules []teamGraphSchedule `json:"schedules,omitempty"`
	Teams     []teamInfo          `json:"teams,omitempty"`
	Edges     []teamGraphEdge     `json:"edges,omitempty"`
}

const topologyGraphRootNode = "topology"

func collectTopologyGraph(teamDir string, includeRoutes bool, jobID string) (topologyGraph, error) {
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return topologyGraph{}, err
	}
	graph := topologyGraph{}
	if top == nil {
		return graph, nil
	}
	overlayJob, err := readTopologyGraphOverlayJob(teamDir, top, jobID)
	if err != nil {
		return topologyGraph{}, err
	}
	for _, inst := range top.SortedInstances() {
		if inst == nil {
			continue
		}
		graph.Instances = append(graph.Instances, teamGraphInstance{
			Name:      inst.Name,
			Agent:     strings.TrimSpace(inst.Agent),
			Ephemeral: inst.Ephemeral,
		})
		graph.Edges = append(graph.Edges, teamGraphEdge{From: topologyGraphRootNode, To: "instance:" + inst.Name, Kind: "declares_instance"})
	}
	for _, pipeline := range top.SortedPipelines() {
		if pipeline == nil {
			continue
		}
		pg := pipelineGraphFromTopology(top, pipeline, includeRoutes)
		if overlayJob != nil && strings.TrimSpace(overlayJob.Pipeline) == pipeline.Name {
			pg = pipelineGraphWithJobState(pg, overlayJob)
		}
		graph.Pipelines = append(graph.Pipelines, pg)
		graph.Edges = append(graph.Edges, teamGraphEdge{From: topologyGraphRootNode, To: "pipeline:" + pipeline.Name, Kind: "declares_pipeline"})
		graph.Edges = append(graph.Edges, teamGraphEdge{From: "pipeline:" + pipeline.Name, To: "pipeline:" + pipeline.Name + ":trigger", Kind: "has_trigger"})
		for _, edge := range pg.Edges {
			graph.Edges = append(graph.Edges, teamGraphEdge{
				From: namespacedPipelineGraphNode(pipeline.Name, edge.From),
				To:   namespacedPipelineGraphNode(pipeline.Name, edge.To),
				Kind: "pipeline_dependency",
			})
		}
		for _, node := range pg.Nodes {
			if node.Missing {
				continue
			}
			targets := node.Routes
			if len(targets) == 0 && strings.TrimSpace(node.Target) != "" {
				targets = []string{strings.TrimSpace(node.Target)}
			}
			for _, target := range targets {
				target = strings.TrimSpace(target)
				if target == "" {
					continue
				}
				graph.Edges = append(graph.Edges, teamGraphEdge{
					From: "pipeline:" + pipeline.Name + ":step:" + node.ID,
					To:   "instance:" + target,
					Kind: "dispatches_to",
				})
			}
		}
	}
	for _, schedule := range top.SortedSchedules() {
		if schedule == nil {
			continue
		}
		graph.Schedules = append(graph.Schedules, teamGraphSchedule{
			Name:       schedule.Name,
			Every:      schedule.Every.String(),
			RunOnStart: schedule.RunOnStart,
		})
		graph.Edges = append(graph.Edges, teamGraphEdge{From: topologyGraphRootNode, To: "schedule:" + schedule.Name, Kind: "declares_schedule"})
		payload := schedule.EventPayload()
		for _, pipeline := range top.SortedPipelines() {
			if pipeline == nil || pipeline.Trigger == nil || pipeline.Trigger.Event != topology.EventSchedule || !pipeline.Trigger.Matches(payload) {
				continue
			}
			graph.Edges = append(graph.Edges, teamGraphEdge{From: "schedule:" + schedule.Name, To: "pipeline:" + pipeline.Name, Kind: "triggers_pipeline"})
		}
	}
	for _, team := range top.SortedTeams() {
		if team == nil {
			continue
		}
		graph.Teams = append(graph.Teams, teamInfoFromTopology(team))
		teamNode := "team:" + team.Name
		graph.Edges = append(graph.Edges, teamGraphEdge{From: topologyGraphRootNode, To: teamNode, Kind: "declares_team"})
		for _, name := range team.Instances {
			name = strings.TrimSpace(name)
			if name != "" {
				graph.Edges = append(graph.Edges, teamGraphEdge{From: teamNode, To: "instance:" + name, Kind: "owns_instance"})
			}
		}
		for _, name := range team.Pipelines {
			name = strings.TrimSpace(name)
			if name != "" {
				graph.Edges = append(graph.Edges, teamGraphEdge{From: teamNode, To: "pipeline:" + name, Kind: "owns_pipeline"})
			}
		}
		for _, name := range team.Schedules {
			name = strings.TrimSpace(name)
			if name != "" {
				graph.Edges = append(graph.Edges, teamGraphEdge{From: teamNode, To: "schedule:" + name, Kind: "owns_schedule"})
			}
		}
	}
	return graph, nil
}

func readTopologyGraphOverlayJob(teamDir string, top *topology.Topology, id string) (*job.Job, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	if top == nil {
		return nil, fmt.Errorf("topology is required")
	}
	j, err := job.Read(teamDir, id)
	if err != nil {
		return nil, err
	}
	pipeline := strings.TrimSpace(j.Pipeline)
	if pipeline == "" {
		return nil, fmt.Errorf("job %q is not a pipeline job", j.ID)
	}
	if top.Pipelines[pipeline] == nil {
		return nil, fmt.Errorf("job %q belongs to pipeline %q, not a declared pipeline", j.ID, pipeline)
	}
	return j, nil
}

func collectTopologySummary(teamDir string) (*topologySummary, error) {
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return nil, err
	}
	summary := &topologySummary{OK: true}
	if top == nil {
		return summary, nil
	}
	summary.Instances = len(top.Instances)
	for _, inst := range top.SortedInstances() {
		if inst == nil {
			continue
		}
		if inst.Ephemeral {
			summary.Ephemeral++
		} else {
			summary.Persistent++
		}
		summary.Triggers += len(inst.Triggers)
	}
	summary.Pipelines = len(top.Pipelines)
	for _, pipeline := range top.SortedPipelines() {
		if pipeline == nil {
			continue
		}
		summary.PipelineSteps += len(pipeline.Steps)
	}
	summary.Schedules = len(top.Schedules)
	summary.Teams = len(top.Teams)
	if pipelineDoctor, err := collectPipelineDoctor(teamDir, ""); err != nil {
		return nil, err
	} else if pipelineDoctor != nil {
		summary.PipelineProblems = len(pipelineDoctor.Problems)
		summary.PipelineWarnings = countPipelineDoctorWarnings(pipelineDoctor)
	}
	if teamDoctor, err := collectAllTeamDoctor(teamDir); err != nil {
		return nil, err
	} else if teamDoctor != nil {
		summary.TeamProblems = len(teamDoctor.Problems)
		summary.TeamWarnings = countSnapshotTeamDoctorWarnings(teamDoctor.Warnings)
	}
	summary.OK = summary.PipelineProblems == 0 && summary.TeamProblems == 0
	return summary, nil
}

func renderTopologySummary(w io.Writer, summary *topologySummary, asJSON bool) error {
	if summary == nil {
		summary = &topologySummary{OK: true}
	}
	if asJSON {
		return json.NewEncoder(w).Encode(summary)
	}
	state := "ok"
	if !summary.OK {
		state = "attention"
	}
	fmt.Fprintf(w, "topology: %s\n", state)
	fmt.Fprintf(w, "instances: total=%d persistent=%d ephemeral=%d triggers=%d\n",
		summary.Instances,
		summary.Persistent,
		summary.Ephemeral,
		summary.Triggers)
	fmt.Fprintf(w, "pipelines: total=%d steps=%d problems=%d warnings=%d\n",
		summary.Pipelines,
		summary.PipelineSteps,
		summary.PipelineProblems,
		summary.PipelineWarnings)
	fmt.Fprintf(w, "schedules: total=%d\n", summary.Schedules)
	fmt.Fprintf(w, "teams: total=%d problems=%d warnings=%d\n",
		summary.Teams,
		summary.TeamProblems,
		summary.TeamWarnings)
	return nil
}

func renderTopologyGraph(w io.Writer, graph topologyGraph, format pipelineGraphFormat, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(graph)
	}
	switch format {
	case pipelineGraphMermaid:
		renderTopologyGraphMermaid(w, graph)
	case pipelineGraphDOT:
		renderTopologyGraphDOT(w, graph)
	default:
		renderTopologyGraphText(w, graph)
	}
	return nil
}

func renderTopologyGraphCommands(w io.Writer, graph topologyGraph, scope operatorCommandScope) error {
	var actions []string
	for _, pipeline := range graph.Pipelines {
		actions = append(actions, pipelineGraphActionCommands(pipeline)...)
	}
	return renderActionCommands(w, scopedOperatorActions(actions, scope))
}

func renderTopologyGraphText(w io.Writer, graph topologyGraph) {
	fmt.Fprintln(w, "Topology")
	if len(graph.Instances) == 0 {
		fmt.Fprintln(w, "Instances: -")
	} else {
		fmt.Fprintln(w, "Instances:")
		for _, inst := range graph.Instances {
			fmt.Fprintf(w, "  %s agent=%s ephemeral=%t\n", inst.Name, emptyDash(inst.Agent), inst.Ephemeral)
		}
	}
	if len(graph.Teams) == 0 {
		fmt.Fprintln(w, "Teams: -")
	} else {
		fmt.Fprintln(w, "Teams:")
		for _, team := range graph.Teams {
			fmt.Fprintf(w, "  %s instances=%d pipelines=%d schedules=%d\n", team.Name, len(team.Instances), len(team.Pipelines), len(team.Schedules))
		}
	}
	if len(graph.Pipelines) == 0 {
		fmt.Fprintln(w, "Pipelines: -")
	} else {
		fmt.Fprintln(w, "Pipelines:")
		for _, pipeline := range graph.Pipelines {
			jobInfo := ""
			if pipeline.JobID != "" {
				jobInfo = fmt.Sprintf(" job=%s ticket=%s status=%s state=%s message=%q", pipeline.JobID, emptyDash(pipeline.Ticket), pipeline.JobStatus, emptyDash(pipeline.JobState), pipeline.Message)
			}
			fmt.Fprintf(w, "  %s trigger=%s steps=%d%s\n", pipeline.Name, emptyDash(pipeline.Summary), len(pipeline.Nodes), jobInfo)
			for _, node := range pipeline.Nodes {
				after := "-"
				if len(node.After) > 0 {
					after = strings.Join(node.After, ",")
				}
				fmt.Fprintf(w, "    %s target=%s after=%s%s\n", node.ID, emptyDash(node.Target), after, pipelineGraphNodeTopologyText(node))
			}
		}
	}
	if len(graph.Schedules) == 0 {
		fmt.Fprintln(w, "Schedules: -")
	} else {
		fmt.Fprintln(w, "Schedules:")
		for _, schedule := range graph.Schedules {
			fmt.Fprintf(w, "  %s every=%s run_on_start=%t\n", schedule.Name, emptyDash(schedule.Every), schedule.RunOnStart)
		}
	}
	if len(graph.Edges) == 0 {
		return
	}
	fmt.Fprintln(w, "Edges:")
	for _, edge := range graph.Edges {
		kind := ""
		if edge.Kind != "" {
			kind = " kind=" + edge.Kind
		}
		fmt.Fprintf(w, "  %s -> %s%s\n", edge.From, edge.To, kind)
	}
}

func renderTopologyGraphMermaid(w io.Writer, graph topologyGraph) {
	fmt.Fprintln(w, "flowchart TD")
	for _, node := range topologyGraphNodeLabels(graph) {
		label := strings.ReplaceAll(node.Label, "\n", "<br/>")
		fmt.Fprintf(w, "  %s[%q]\n", pipelineGraphMermaidID(node.ID), pipelineMermaidLabel(label))
	}
	for _, edge := range graph.Edges {
		fmt.Fprintf(w, "  %s --> %s\n", pipelineGraphMermaidID(edge.From), pipelineGraphMermaidID(edge.To))
	}
}

func renderTopologyGraphDOT(w io.Writer, graph topologyGraph) {
	fmt.Fprintln(w, `digraph "topology" {`)
	fmt.Fprintln(w, "  rankdir=TB;")
	for _, node := range topologyGraphNodeLabels(graph) {
		fmt.Fprintf(w, "  %q [label=%q];\n", node.ID, node.Label)
	}
	for _, edge := range graph.Edges {
		fmt.Fprintf(w, "  %q -> %q", edge.From, edge.To)
		if edge.Kind != "" {
			fmt.Fprintf(w, " [label=%q]", edge.Kind)
		}
		fmt.Fprintln(w, ";")
	}
	fmt.Fprintln(w, "}")
}

func topologyGraphNodeLabels(graph topologyGraph) []teamGraphLabel {
	labels := []teamGraphLabel{{ID: topologyGraphRootNode, Label: "topology"}}
	for _, inst := range graph.Instances {
		parts := []string{"instance: " + inst.Name}
		if inst.Agent != "" {
			parts = append(parts, "agent: "+inst.Agent)
		}
		if inst.Ephemeral {
			parts = append(parts, "ephemeral")
		}
		labels = append(labels, teamGraphLabel{ID: "instance:" + inst.Name, Label: strings.Join(parts, "\n")})
	}
	for _, team := range graph.Teams {
		parts := []string{"team: " + team.Name}
		if team.Description != "" {
			parts = append(parts, team.Description)
		}
		labels = append(labels, teamGraphLabel{ID: "team:" + team.Name, Label: strings.Join(parts, "\n")})
	}
	for _, pipeline := range graph.Pipelines {
		labels = append(labels, teamGraphLabel{ID: "pipeline:" + pipeline.Name, Label: pipelineGraphPipelineLabel(pipeline)})
		labels = append(labels, teamGraphLabel{ID: "pipeline:" + pipeline.Name + ":trigger", Label: "trigger: " + emptyDash(pipeline.Summary)})
		for _, node := range pipeline.Nodes {
			labels = append(labels, teamGraphLabel{
				ID:    "pipeline:" + pipeline.Name + ":step:" + node.ID,
				Label: pipelineGraphNodeLabel(node, "\n"),
			})
		}
	}
	for _, schedule := range graph.Schedules {
		parts := []string{"schedule: " + schedule.Name}
		if schedule.Every != "" {
			parts = append(parts, "every: "+schedule.Every)
		}
		if schedule.RunOnStart {
			parts = append(parts, "run_on_start")
		}
		labels = append(labels, teamGraphLabel{ID: "schedule:" + schedule.Name, Label: strings.Join(parts, "\n")})
	}
	return labels
}

func toResponseLike(top *topology.Topology) map[string]any {
	out := make([]map[string]any, 0, len(top.Instances))
	for _, inst := range top.SortedInstances() {
		out = append(out, map[string]any{
			"name":          inst.Name,
			"agent":         inst.Agent,
			"ephemeral":     inst.Ephemeral,
			"description":   inst.Description,
			"replicas":      inst.Replicas,
			"reap_worktree": inst.ReapWorktree,
			"config":        map[string]any(inst.Config),
			"triggers":      triggersAsMaps(inst.Triggers),
		})
	}
	pipelines := make([]map[string]any, 0, len(top.Pipelines))
	for _, pipeline := range top.SortedPipelines() {
		pipelines = append(pipelines, map[string]any{
			"name":          pipeline.Name,
			"trigger":       triggerAsMap(pipeline.Trigger),
			"steps":         pipelineStepsAsMaps(pipeline.Steps),
			"reap_worktree": pipeline.ReapWorktree,
		})
	}
	schedules := make([]map[string]any, 0, len(top.Schedules))
	for _, schedule := range top.SortedSchedules() {
		schedules = append(schedules, map[string]any{
			"name":         schedule.Name,
			"every":        schedule.Every.String(),
			"run_on_start": schedule.RunOnStart,
			"payload":      schedule.Payload,
		})
	}
	return map[string]any{"instances": out, "pipelines": pipelines, "schedules": schedules}
}

func triggersAsMaps(triggers []*topology.Trigger) []map[string]any {
	out := make([]map[string]any, 0, len(triggers))
	for _, t := range triggers {
		match := map[string]any{}
		for k, mv := range t.Match {
			if mv.Single != "" {
				match[k] = mv.Single
			} else if len(mv.List) > 0 {
				match[k] = mv.List
			}
		}
		out = append(out, map[string]any{"event": t.Event, "match": match})
	}
	return out
}

func triggerAsMap(t *topology.Trigger) map[string]any {
	if t == nil {
		return nil
	}
	match := map[string]any{}
	for k, mv := range t.Match {
		if mv.Single != "" {
			match[k] = mv.Single
		} else if len(mv.List) > 0 {
			match[k] = mv.List
		}
	}
	return map[string]any{"event": t.Event, "match": match}
}

func pipelineStepsAsMaps(steps []*topology.PipelineStep) []map[string]any {
	out := make([]map[string]any, 0, len(steps))
	for _, step := range steps {
		row := map[string]any{"id": step.ID, "target": step.Target, "after": step.After}
		if step.Label != "" {
			row["label"] = step.Label
		}
		if step.Description != "" {
			row["description"] = step.Description
		}
		if step.Instructions != "" {
			row["instructions"] = step.Instructions
		}
		if step.Gate != "" {
			row["gate"] = step.Gate
		}
		if step.Optional {
			row["optional"] = true
		}
		if step.Timeout > 0 {
			row["timeout"] = step.Timeout.String()
		}
		if step.MaxAttempts > 0 {
			row["max_attempts"] = step.MaxAttempts
		}
		out = append(out, row)
	}
	return out
}

func printDaemonTopology(w io.Writer, res *topologyResponse) {
	if len(res.Instances) == 0 && len(res.Pipelines) == 0 && len(res.Schedules) == 0 {
		fmt.Fprintln(w, "(no topology declared)")
		return
	}
	if len(res.Instances) > 0 {
		tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tAGENT\tEPHEMERAL\tREPLICAS\tTRIGGERS\tRUNNING\tQUEUED")
		for _, i := range res.Instances {
			eph := "no"
			if i.Ephemeral {
				eph = "yes"
			}
			trigSummary := summariseTriggers(i.Triggers)
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%d\t%d\n",
				i.Name, i.Agent, eph, i.Replicas, trigSummary, i.Running, i.Queued)
		}
		_ = tw.Flush()
	}
	if len(res.Pipelines) > 0 {
		if len(res.Instances) > 0 {
			fmt.Fprintln(w)
		}
		printDaemonPipelines(w, res.Pipelines)
	}
	if len(res.Schedules) > 0 {
		if len(res.Instances) > 0 || len(res.Pipelines) > 0 {
			fmt.Fprintln(w)
		}
		printDaemonSchedules(w, res.Schedules)
	}
}

func printLocalTopology(w io.Writer, top *topology.Topology) {
	if len(top.Instances) > 0 {
		tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tAGENT\tEPHEMERAL\tREPLICAS\tTRIGGERS")
		for _, inst := range top.SortedInstances() {
			eph := "no"
			if inst.Ephemeral {
				eph = "yes"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n",
				inst.Name, inst.Agent, eph, inst.Replicas, summariseLocalTriggers(inst.Triggers))
		}
		_ = tw.Flush()
	}
	if len(top.Pipelines) > 0 {
		if len(top.Instances) > 0 {
			fmt.Fprintln(w)
		}
		printLocalPipelines(w, top.SortedPipelines())
	}
	if len(top.Schedules) > 0 {
		if len(top.Instances) > 0 || len(top.Pipelines) > 0 {
			fmt.Fprintln(w)
		}
		printLocalSchedules(w, top.SortedSchedules())
	}
}

func printDaemonPipelines(w io.Writer, pipelines []topologyPipeline) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "PIPELINE\tTRIGGER\tSTEPS")
	for _, pipeline := range pipelines {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", pipeline.Name, summariseTriggerMap(pipeline.Trigger), summarisePipelineStepMaps(pipeline.Steps))
	}
	_ = tw.Flush()
}

func printLocalPipelines(w io.Writer, pipelines []*topology.Pipeline) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "PIPELINE\tTRIGGER\tSTEPS")
	for _, pipeline := range pipelines {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", pipeline.Name, summariseLocalTrigger(pipeline.Trigger), summariseLocalPipelineSteps(pipeline.Steps))
	}
	_ = tw.Flush()
}

func printDaemonSchedules(w io.Writer, schedules []topologySchedule) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SCHEDULE\tEVERY\tRUN_ON_START\tPAYLOAD")
	for _, schedule := range schedules {
		fmt.Fprintf(tw, "%s\t%s\t%t\t%s\n", schedule.Name, schedule.Every, schedule.RunOnStart, summarisePayloadMap(schedule.Payload))
	}
	_ = tw.Flush()
}

func printLocalSchedules(w io.Writer, schedules []*topology.Schedule) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SCHEDULE\tEVERY\tRUN_ON_START\tPAYLOAD")
	for _, schedule := range schedules {
		fmt.Fprintf(tw, "%s\t%s\t%t\t%s\n", schedule.Name, schedule.Every, schedule.RunOnStart, summariseAnyPayloadMap(schedule.Payload))
	}
	_ = tw.Flush()
}

func summariseTriggers(triggers []map[string]interface{}) string {
	if len(triggers) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(triggers))
	for _, t := range triggers {
		ev, _ := t["event"].(string)
		match, _ := t["match"].(map[string]interface{})
		if len(match) > 0 {
			keys := make([]string, 0, len(match))
			for k := range match {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			parts = append(parts, fmt.Sprintf("%s(%s)", ev, strings.Join(keys, ",")))
		} else {
			parts = append(parts, ev)
		}
	}
	return strings.Join(parts, ", ")
}

func summarisePayloadMap(payload map[string]interface{}) string {
	if len(payload) == 0 {
		return "—"
	}
	keys := make([]string, 0, len(payload))
	for key := range payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func summariseAnyPayloadMap(payload map[string]any) string {
	if len(payload) == 0 {
		return "—"
	}
	keys := make([]string, 0, len(payload))
	for key := range payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func summariseLocalTriggers(triggers []*topology.Trigger) string {
	if len(triggers) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(triggers))
	for _, t := range triggers {
		if len(t.Match) > 0 {
			keys := make([]string, 0, len(t.Match))
			for k := range t.Match {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			parts = append(parts, fmt.Sprintf("%s(%s)", t.Event, strings.Join(keys, ",")))
		} else {
			parts = append(parts, t.Event)
		}
	}
	return strings.Join(parts, ", ")
}

func summariseTriggerMap(trigger map[string]interface{}) string {
	if len(trigger) == 0 {
		return "—"
	}
	event, _ := trigger["event"].(string)
	match, _ := trigger["match"].(map[string]interface{})
	if len(match) == 0 {
		return event
	}
	keys := make([]string, 0, len(match))
	for key := range match {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return fmt.Sprintf("%s(%s)", event, strings.Join(keys, ","))
}

func summariseLocalTrigger(trigger *topology.Trigger) string {
	if trigger == nil {
		return "—"
	}
	if len(trigger.Match) == 0 {
		return trigger.Event
	}
	keys := make([]string, 0, len(trigger.Match))
	for key := range trigger.Match {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return fmt.Sprintf("%s(%s)", trigger.Event, strings.Join(keys, ","))
}

func summarisePipelineStepMaps(steps []map[string]interface{}) string {
	if len(steps) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(steps))
	for _, step := range steps {
		id, _ := step["id"].(string)
		target, _ := step["target"].(string)
		suffix := ""
		if gate, _ := step["gate"].(string); gate != "" {
			suffix += " gate=" + gate
		}
		if optional, _ := step["optional"].(bool); optional {
			suffix += " optional=true"
		}
		if timeout, _ := step["timeout"].(string); timeout != "" {
			suffix += " timeout=" + timeout
		}
		if maxAttempts, _ := step["max_attempts"].(int); maxAttempts > 0 {
			suffix += fmt.Sprintf(" max_attempts=%d", maxAttempts)
		}
		if label, _ := step["label"].(string); label != "" {
			suffix = fmt.Sprintf(" label=%q", label) + suffix
		}
		parts = append(parts, id+"→"+target+suffix)
	}
	return strings.Join(parts, ", ")
}

func summariseLocalPipelineSteps(steps []*topology.PipelineStep) string {
	if len(steps) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(steps))
	for _, step := range steps {
		suffix := ""
		if step.Gate != "" {
			suffix += " gate=" + step.Gate
		}
		if step.Optional {
			suffix += " optional=true"
		}
		if step.Timeout > 0 {
			suffix += " timeout=" + step.Timeout.String()
		}
		if step.MaxAttempts > 0 {
			suffix += fmt.Sprintf(" max_attempts=%d", step.MaxAttempts)
		}
		if step.Label != "" {
			suffix = fmt.Sprintf(" label=%q", step.Label) + suffix
		}
		parts = append(parts, step.ID+"→"+step.Target+suffix)
	}
	return strings.Join(parts, ", ")
}
