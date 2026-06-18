package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newPipelineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pipeline",
		Short: "Inspect declared pipeline workflows.",
		Long:  "Inspect pipeline declarations loaded from .agent_team/instances.toml.",
	}
	cmd.AddCommand(newPipelineLsCmd())
	cmd.AddCommand(newPipelineShowCmd())
	cmd.AddCommand(newPipelineJobsCmd())
	cmd.AddCommand(newPipelineReadyCmd())
	cmd.AddCommand(newPipelineAdvanceCmd())
	cmd.AddCommand(newPipelineRunCmd())
	return cmd
}

func newPipelineLsCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List declared pipelines.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			pipelines, err := loadPipelineInfos(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline ls: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineList(cmd.OutOrStdout(), pipelines, jsonOut)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit pipelines as JSON.")
	return cmd
}

func newPipelineShowCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <pipeline>",
		Short: "Show one declared pipeline.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			info, err := loadPipelineInfo(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline show: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineDetail(cmd.OutOrStdout(), info, jsonOut)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the pipeline as JSON.")
	return cmd
}

func newPipelineJobsCmd() *cobra.Command {
	var (
		repo    string
		status  string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "jobs <pipeline>",
		Short: "List jobs for one pipeline.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline jobs: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline jobs: %v\n", err)
				return exitErr(2)
			}
			filters, err := newJobListFilters(status, "", "", args[0], "", "", "")
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline jobs: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			return runJobList(cmd.OutOrStdout(), teamDir, filters, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&status, "status", "", "Filter by job status: queued, running, blocked, done, or failed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit jobs as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func newPipelineReadyCmd() *cobra.Command {
	var (
		repo    string
		states  []string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ready <pipeline>",
		Short: "List ready jobs for one pipeline.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ready: --format cannot be combined with --json.")
				return exitErr(2)
			}
			stateFilter, err := parseJobNextStateFilter(states, !cmd.Flags().Changed("state"))
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline ready: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parseJobReadyFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline ready: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			return runJobReady(cmd.OutOrStdout(), teamDir, strings.TrimSpace(args[0]), stateFilter, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringSliceVar(&states, "state", nil, "Next-step state to include: ready, queued, running, blocked, failed, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit ready rows as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each row with a Go template, e.g. '{{.JobID}} {{.State}} {{.StepID}}'.")
	return cmd
}

func newPipelineAdvanceCmd() *cobra.Command {
	var (
		repo      string
		workspace string
		limit     int
		dryRun    bool
		jsonOut   bool
		format    string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "advance <pipeline>",
		Short: "Dispatch ready steps for one pipeline.",
		Long:  "Dispatch ready next steps for jobs in one pipeline using the same path as `agent-team job advance`.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline advance: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline advance: --limit must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineAdvanceFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline advance: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			pipelineName := strings.TrimSpace(args[0])
			if pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline advance: pipeline name is required.")
				return exitErr(2)
			}
			results, err := advanceReadyPipelineJobs(cmd, teamDir, pipelineName, workspace, limit, dryRun)
			if err != nil {
				return err
			}
			return renderPipelineAdvanceResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for advanced steps: auto, worktree, or repo.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Advance at most this many ready jobs; 0 means no limit.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview ready steps without dispatching them.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit advance results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.")
	return cmd
}

func newPipelineRunCmd() *cobra.Command {
	var (
		repo        string
		id          string
		kickoff     string
		kickoffFile string
		dispatchNow bool
		workspace   string
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "run <pipeline> <ticket> [kickoff...]",
		Short: "Create a durable job from a pipeline declaration.",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline run: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline run: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			pipelineDef, err := loadJobCreatePipeline(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline run: %v\n", err)
				return exitErr(2)
			}
			ticket := args[1]
			kickoffText, err := dispatchKickoff(ticket, kickoff, kickoffFile, args[2:])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline run: %v\n", err)
				return exitErr(2)
			}
			j, err := job.New(ticket, pipelineDef.Steps[0].Target, kickoffText, time.Now())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline run: %v\n", err)
				return exitErr(2)
			}
			if strings.TrimSpace(id) != "" {
				normalized := job.NormalizeID(id)
				if normalized == "" {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline run: --id %q produced an empty normalized id.\n", id)
					return exitErr(2)
				}
				j.ID = normalized
			}
			j.Pipeline = pipelineDef.Name
			j.Steps = jobStepsFromPipeline(pipelineDef)
			j.LastEvent = "created"
			j.LastStatus = "created"
			if _, err := os.Stat(job.Path(teamDir, j.ID)); err == nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline run: job %q already exists.\n", j.ID)
				return exitErr(2)
			}
			if err := writeJobWithAudit(teamDir, j, "created", "cli", "created "+j.Ticket, map[string]string{
				"ticket":   j.Ticket,
				"target":   j.Target,
				"pipeline": j.Pipeline,
			}); err != nil {
				return err
			}
			if dispatchNow {
				res, err := advanceJob(cmd, teamDir, j, workspace)
				if err != nil {
					return err
				}
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(res)
				}
				if tmpl != nil {
					return renderJobTemplate(cmd.OutOrStdout(), res.Job, tmpl)
				}
				return renderJobAdvanceResult(cmd.OutOrStdout(), res)
			}
			return renderJobResult(cmd.OutOrStdout(), j, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&id, "id", "", "Override the normalized job id (default: ticket slug).")
	cmd.Flags().StringVar(&kickoff, "kickoff", "", "Kickoff text for the first pipeline step.")
	cmd.Flags().StringVar(&kickoffFile, "kickoff-file", "", "Read kickoff text from a file.")
	cmd.Flags().BoolVar(&dispatchNow, "dispatch", false, "Dispatch the first ready pipeline step immediately using the running daemon.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for --dispatch: auto, worktree, or repo.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the created job or advance result as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the created or advanced job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

type pipelineInfo struct {
	Name    string             `json:"name"`
	Trigger map[string]any     `json:"trigger"`
	Steps   []pipelineStepInfo `json:"steps"`
}

type pipelineStepInfo struct {
	ID     string   `json:"id"`
	Target string   `json:"target"`
	After  []string `json:"after,omitempty"`
}

type pipelineAdvanceResult struct {
	JobID      string         `json:"job_id"`
	Ticket     string         `json:"ticket"`
	Pipeline   string         `json:"pipeline"`
	StepID     string         `json:"step_id,omitempty"`
	Target     string         `json:"target,omitempty"`
	StepStatus job.Status     `json:"step_status,omitempty"`
	Instance   string         `json:"instance,omitempty"`
	Action     string         `json:"action"`
	DryRun     bool           `json:"dry_run,omitempty"`
	Message    string         `json:"message,omitempty"`
	Job        *job.Job       `json:"job,omitempty"`
	Step       *job.Step      `json:"step,omitempty"`
	Event      *eventResponse `json:"event,omitempty"`
}

func loadPipelineInfos(teamDir string) ([]pipelineInfo, error) {
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return nil, err
	}
	if top == nil {
		return nil, nil
	}
	infos := make([]pipelineInfo, 0, len(top.Pipelines))
	for _, p := range top.SortedPipelines() {
		infos = append(infos, pipelineInfoFromTopology(p))
	}
	return infos, nil
}

func loadPipelineInfo(teamDir, name string) (pipelineInfo, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return pipelineInfo{}, fmt.Errorf("pipeline name is required")
	}
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return pipelineInfo{}, err
	}
	if top == nil || top.Pipelines[name] == nil {
		return pipelineInfo{}, fmt.Errorf("pipeline %q not found", name)
	}
	return pipelineInfoFromTopology(top.Pipelines[name]), nil
}

func pipelineInfoFromTopology(p *topology.Pipeline) pipelineInfo {
	steps := make([]pipelineStepInfo, 0, len(p.Steps))
	for _, step := range p.Steps {
		steps = append(steps, pipelineStepInfo{
			ID:     step.ID,
			Target: step.Target,
			After:  append([]string(nil), step.After...),
		})
	}
	return pipelineInfo{
		Name:    p.Name,
		Trigger: triggerAsMap(p.Trigger),
		Steps:   steps,
	}
}

func advanceReadyPipelineJobs(cmd *cobra.Command, teamDir, pipeline, workspace string, limit int, dryRun bool) ([]pipelineAdvanceResult, error) {
	rows, err := collectJobReadyRows(teamDir, pipeline, map[string]bool{"ready": true})
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	results := make([]pipelineAdvanceResult, 0, len(rows))
	for _, row := range rows {
		result := pipelineAdvanceResult{
			JobID:      row.JobID,
			Ticket:     row.Ticket,
			Pipeline:   row.Pipeline,
			StepID:     row.StepID,
			Target:     row.Target,
			StepStatus: row.StepStatus,
			Instance:   row.Instance,
			Action:     "would_advance",
			DryRun:     dryRun,
			Message:    row.Message,
		}
		if dryRun {
			results = append(results, result)
			continue
		}
		j, err := job.Read(teamDir, row.JobID)
		if err != nil {
			return nil, err
		}
		advanced, err := advanceJob(cmd, teamDir, j, workspace)
		if err != nil {
			return nil, err
		}
		result.Action = pipelineAdvanceAction(advanced)
		result.DryRun = false
		result.Job = advanced.Job
		result.Step = advanced.Step
		result.Event = advanced.Event
		result.Message = advanced.Message
		if advanced.Job != nil {
			result.Ticket = advanced.Job.Ticket
			result.Pipeline = advanced.Job.Pipeline
		}
		if advanced.Step != nil {
			result.StepID = advanced.Step.ID
			result.Target = advanced.Step.Target
			result.StepStatus = advanced.Step.Status
			result.Instance = advanced.Step.Instance
		}
		results = append(results, result)
	}
	return results, nil
}

func pipelineAdvanceAction(result *jobAdvanceResult) string {
	if result == nil {
		return "skipped"
	}
	if strings.TrimSpace(result.Message) != "" && result.Step == nil && result.Event == nil {
		return "skipped"
	}
	return "advanced"
}

func renderPipelineList(w io.Writer, pipelines []pipelineInfo, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(pipelines)
	}
	if len(pipelines) == 0 {
		fmt.Fprintln(w, "(no pipelines declared)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "PIPELINE\tTRIGGER\tSTEPS")
	for _, info := range pipelines {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", info.Name, summariseTriggerMap(info.Trigger), summarisePipelineInfoSteps(info.Steps))
	}
	_ = tw.Flush()
	return nil
}

func renderPipelineDetail(w io.Writer, info pipelineInfo, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(info)
	}
	fmt.Fprintf(w, "Pipeline: %s\n", info.Name)
	fmt.Fprintf(w, "Trigger:  %s\n", summariseTriggerMap(info.Trigger))
	if len(info.Steps) == 0 {
		fmt.Fprintln(w, "Steps:    -")
		return nil
	}
	fmt.Fprintln(w, "Steps:")
	for _, step := range info.Steps {
		after := "-"
		if len(step.After) > 0 {
			after = strings.Join(step.After, ",")
		}
		fmt.Fprintf(w, "  %s target=%s after=%s\n", step.ID, step.Target, after)
	}
	return nil
}

func summarisePipelineInfoSteps(steps []pipelineStepInfo) string {
	if len(steps) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(steps))
	for _, step := range steps {
		if len(step.After) > 0 {
			parts = append(parts, fmt.Sprintf("%s:%s after=%s", step.ID, step.Target, strings.Join(step.After, ",")))
		} else {
			parts = append(parts, fmt.Sprintf("%s:%s", step.ID, step.Target))
		}
	}
	return strings.Join(parts, " -> ")
}

func parsePipelineAdvanceFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("pipeline-advance-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderPipelineAdvanceResults(w io.Writer, results []pipelineAdvanceResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(results)
	}
	if tmpl != nil {
		for _, result := range results {
			if err := tmpl.Execute(w, result); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	renderPipelineAdvanceTable(w, results)
	return nil
}

func renderPipelineAdvanceTable(w io.Writer, results []pipelineAdvanceResult) {
	if len(results) == 0 {
		fmt.Fprintln(w, "(no ready pipeline jobs)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB\tSTEP\tTARGET\tACTION\tSTATUS\tINSTANCE\tMESSAGE")
	for _, result := range results {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			result.JobID,
			emptyDash(result.StepID),
			emptyDash(result.Target),
			result.Action,
			emptyDash(string(result.StepStatus)),
			emptyDash(result.Instance),
			emptyDash(result.Message),
		)
	}
	_ = tw.Flush()
}
