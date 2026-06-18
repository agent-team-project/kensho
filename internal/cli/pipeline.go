package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
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
	cmd.AddCommand(newPipelineStatusCmd())
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

func newPipelineStatusCmd() *cobra.Command {
	var (
		repo    string
		all     bool
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "status [<pipeline>|--all]",
		Short: "Summarize pipeline jobs and next steps.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline status: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline status: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline status: pass at most one pipeline name.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineStatusFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline status: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			pipelineName := ""
			if len(args) == 1 && !all {
				pipelineName = strings.TrimSpace(args[0])
			}
			if len(args) == 1 && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline status: pipeline name is required.")
				return exitErr(2)
			}
			rows, err := collectPipelineStatusRows(teamDir, pipelineName)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline status: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineStatusRows(cmd.OutOrStdout(), rows, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&all, "all", false, "Summarize all pipelines. This is the default when no pipeline is passed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit pipeline status rows as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each row with a Go template, e.g. '{{.Pipeline}} {{.Jobs}} {{.ReadySteps}}'.")
	return cmd
}

func newPipelineReadyCmd() *cobra.Command {
	var (
		repo    string
		states  []string
		all     bool
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ready <pipeline>|--all",
		Short: "List ready pipeline jobs.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ready: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ready: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if !all && len(args) != 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ready: pass a pipeline name or --all.")
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
			pipelineName := ""
			if !all {
				pipelineName = strings.TrimSpace(args[0])
			}
			if !all && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ready: pipeline name is required.")
				return exitErr(2)
			}
			return runJobReady(cmd.OutOrStdout(), teamDir, pipelineName, stateFilter, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringSliceVar(&states, "state", nil, "Next-step state to include: ready, queued, running, blocked, failed, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&all, "all", false, "List ready jobs across all pipelines.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit ready rows as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each row with a Go template, e.g. '{{.JobID}} {{.State}} {{.StepID}}'.")
	return cmd
}

func newPipelineAdvanceCmd() *cobra.Command {
	var (
		repo          string
		workspace     string
		limit         int
		all           bool
		dryRun        bool
		previewRoutes bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "advance <pipeline>|--all",
		Short: "Dispatch ready pipeline steps.",
		Long:  "Dispatch ready next steps for jobs in one pipeline, or across all pipelines with --all, using the same path as `agent-team job advance`.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline advance: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline advance: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if !all && len(args) != 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline advance: pass a pipeline name or --all.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline advance: --limit must be >= 0.")
				return exitErr(2)
			}
			if previewRoutes && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline advance: --preview-routes requires --dry-run.")
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
			pipelineName := ""
			if !all {
				pipelineName = strings.TrimSpace(args[0])
			}
			if !all && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline advance: pipeline name is required.")
				return exitErr(2)
			}
			results, err := advanceReadyPipelineJobs(cmd, teamDir, pipelineName, workspace, limit, dryRun, previewRoutes)
			if err != nil {
				return err
			}
			return renderPipelineAdvanceResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for advanced steps: auto, worktree, or repo.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Advance at most this many ready jobs; 0 means no limit.")
	cmd.Flags().BoolVar(&all, "all", false, "Advance ready steps across all pipelines.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview ready steps without dispatching them.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-routes", false, "With --dry-run, include local topology route and dispatch payload previews.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit advance results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.")
	return cmd
}

func newPipelineRunCmd() *cobra.Command {
	var (
		repo        string
		id          string
		ticketURL   string
		kickoff     string
		kickoffFile string
		dispatchNow bool
		workspace   string
		dryRun      bool
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
			if strings.TrimSpace(ticketURL) != "" {
				j.TicketURL = strings.TrimSpace(ticketURL)
			}
			j.Pipeline = pipelineDef.Name
			j.Steps = jobStepsFromPipeline(pipelineDef)
			j.LastEvent = "created"
			j.LastStatus = "created"
			if _, err := os.Stat(job.Path(teamDir, j.ID)); err == nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline run: job %q already exists.\n", j.ID)
				return exitErr(2)
			}
			if dryRun {
				if dispatchNow {
					preview, err := previewJobAdvanceDispatch(teamDir, j, workspace)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline run: %v\n", err)
						return exitErr(1)
					}
					return renderJobAdvancePreview(cmd.OutOrStdout(), preview, jsonOut, tmpl)
				}
				return renderJobCreatePreview(cmd.OutOrStdout(), j, jsonOut, tmpl)
			}
			data := map[string]string{
				"ticket":   j.Ticket,
				"target":   j.Target,
				"pipeline": j.Pipeline,
			}
			if j.TicketURL != "" {
				data["ticket_url"] = j.TicketURL
			}
			if err := writeJobWithAudit(teamDir, j, "created", "cli", "created "+j.Ticket, data); err != nil {
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
	cmd.Flags().StringVar(&ticketURL, "ticket-url", "", "Canonical ticket URL to store on the job.")
	cmd.Flags().StringVar(&kickoff, "kickoff", "", "Kickoff text for the first pipeline step.")
	cmd.Flags().StringVar(&kickoffFile, "kickoff-file", "", "Read kickoff text from a file.")
	cmd.Flags().BoolVar(&dispatchNow, "dispatch", false, "Dispatch the first ready pipeline step immediately using the running daemon.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for --dispatch: auto, worktree, or repo.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the pipeline job that would be created without writing it.")
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

type pipelineStatusRow struct {
	Pipeline     string   `json:"pipeline"`
	Declared     bool     `json:"declared"`
	Steps        int      `json:"steps"`
	Jobs         int      `json:"jobs"`
	Queued       int      `json:"queued"`
	Running      int      `json:"running"`
	Blocked      int      `json:"blocked"`
	Done         int      `json:"done"`
	Failed       int      `json:"failed"`
	ReadySteps   int      `json:"ready_steps"`
	QueuedSteps  int      `json:"queued_steps"`
	RunningSteps int      `json:"running_steps"`
	BlockedSteps int      `json:"blocked_steps"`
	FailedSteps  int      `json:"failed_steps"`
	DoneSteps    int      `json:"done_steps"`
	NoStep       int      `json:"no_step"`
	Actions      []string `json:"actions,omitempty"`
}

type pipelineAdvanceResult struct {
	JobID      string             `json:"job_id"`
	Ticket     string             `json:"ticket"`
	Pipeline   string             `json:"pipeline"`
	StepID     string             `json:"step_id,omitempty"`
	Target     string             `json:"target,omitempty"`
	StepStatus job.Status         `json:"step_status,omitempty"`
	Instance   string             `json:"instance,omitempty"`
	Action     string             `json:"action"`
	DryRun     bool               `json:"dry_run,omitempty"`
	Message    string             `json:"message,omitempty"`
	Job        *job.Job           `json:"job,omitempty"`
	Step       *job.Step          `json:"step,omitempty"`
	Event      *eventResponse     `json:"event,omitempty"`
	Preview    *jobAdvancePreview `json:"preview,omitempty"`
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

func collectPipelineStatusRows(teamDir, pipeline string) ([]pipelineStatusRow, error) {
	pipeline = strings.TrimSpace(pipeline)
	infos, err := loadPipelineInfos(teamDir)
	if err != nil {
		return nil, err
	}
	rows := map[string]*pipelineStatusRow{}
	declaredOrder := []string{}
	declared := map[string]bool{}
	rowFor := func(name string) *pipelineStatusRow {
		if row := rows[name]; row != nil {
			return row
		}
		row := &pipelineStatusRow{Pipeline: name}
		rows[name] = row
		return row
	}
	for _, info := range infos {
		if pipeline != "" && info.Name != pipeline {
			continue
		}
		row := rowFor(info.Name)
		row.Declared = true
		row.Steps = len(info.Steps)
		declared[info.Name] = true
		declaredOrder = append(declaredOrder, info.Name)
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		if j == nil {
			continue
		}
		name := strings.TrimSpace(j.Pipeline)
		if name == "" {
			continue
		}
		if pipeline != "" && name != pipeline {
			continue
		}
		applyPipelineStatusJob(rowFor(name), j)
	}
	if pipeline != "" {
		row := rows[pipeline]
		if row == nil {
			return nil, fmt.Errorf("pipeline %q not found", pipeline)
		}
		finalizePipelineStatusRow(row)
		return []pipelineStatusRow{*row}, nil
	}
	extras := make([]string, 0, len(rows))
	for name := range rows {
		if !declared[name] {
			extras = append(extras, name)
		}
	}
	sort.Strings(extras)
	out := make([]pipelineStatusRow, 0, len(rows))
	for _, name := range declaredOrder {
		if row := rows[name]; row != nil {
			finalizePipelineStatusRow(row)
			out = append(out, *row)
		}
	}
	for _, name := range extras {
		row := rows[name]
		finalizePipelineStatusRow(row)
		out = append(out, *row)
	}
	return out, nil
}

func applyPipelineStatusJob(row *pipelineStatusRow, j *job.Job) {
	if row == nil || j == nil {
		return
	}
	row.Jobs++
	switch j.Status {
	case job.StatusQueued:
		row.Queued++
	case job.StatusRunning:
		row.Running++
	case job.StatusBlocked:
		row.Blocked++
	case job.StatusDone:
		row.Done++
	case job.StatusFailed:
		row.Failed++
	}
	next := inspectNextJobStep(j)
	switch next.State {
	case "ready":
		row.ReadySteps++
	case "queued":
		row.QueuedSteps++
	case "running":
		row.RunningSteps++
	case "blocked":
		row.BlockedSteps++
	case "failed":
		row.FailedSteps++
	case "done":
		row.DoneSteps++
	case "none":
		row.NoStep++
	}
}

func finalizePipelineStatusRow(row *pipelineStatusRow) {
	if row == nil {
		return
	}
	actions := []string{}
	if row.ReadySteps > 0 {
		actions = append(actions, fmt.Sprintf("agent-team pipeline advance %s --dry-run --preview-routes", row.Pipeline))
	}
	if row.FailedSteps > 0 {
		actions = append(actions, fmt.Sprintf("agent-team pipeline ready %s --state failed", row.Pipeline))
	}
	if row.BlockedSteps > 0 {
		actions = append(actions, fmt.Sprintf("agent-team pipeline ready %s --state blocked", row.Pipeline))
	}
	if row.QueuedSteps > 0 {
		actions = append(actions, "agent-team tick")
	}
	row.Actions = actions
}

func advanceReadyPipelineJobs(cmd *cobra.Command, teamDir, pipeline, workspace string, limit int, dryRun bool, previewRoutes bool) ([]pipelineAdvanceResult, error) {
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
			if previewRoutes {
				j, err := job.Read(teamDir, row.JobID)
				if err != nil {
					return nil, err
				}
				preview, err := previewJobAdvanceDispatch(teamDir, j, workspace)
				if err != nil {
					return nil, err
				}
				result.Preview = preview
				result.Message = preview.Message
				if preview.Step != nil {
					result.StepID = preview.Step.ID
					result.Target = preview.Step.Target
					result.StepStatus = preview.Step.Status
					result.Instance = preview.Step.Instance
				}
			}
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

func parsePipelineStatusFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("pipeline-status-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderPipelineStatusRows(w io.Writer, rows []pipelineStatusRow, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(rows)
	}
	if tmpl != nil {
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
	renderPipelineStatusTable(w, rows)
	return nil
}

func renderPipelineStatusTable(w io.Writer, rows []pipelineStatusRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no pipelines)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PIPELINE\tDECLARED\tSTEPS\tJOBS\tJOB_STATUS\tREADY\tQUEUED\tRUNNING\tBLOCKED\tFAILED\tDONE\tNONE\tACTION")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n",
			row.Pipeline,
			yesNo(row.Declared),
			row.Steps,
			row.Jobs,
			pipelineStatusJobSummary(row),
			row.ReadySteps,
			row.QueuedSteps,
			row.RunningSteps,
			row.BlockedSteps,
			row.FailedSteps,
			row.DoneSteps,
			row.NoStep,
			emptyDash(strings.Join(row.Actions, "; ")),
		)
	}
	_ = tw.Flush()
}

func pipelineStatusJobSummary(row pipelineStatusRow) string {
	parts := []string{}
	if row.Queued > 0 {
		parts = append(parts, fmt.Sprintf("queued=%d", row.Queued))
	}
	if row.Running > 0 {
		parts = append(parts, fmt.Sprintf("running=%d", row.Running))
	}
	if row.Blocked > 0 {
		parts = append(parts, fmt.Sprintf("blocked=%d", row.Blocked))
	}
	if row.Done > 0 {
		parts = append(parts, fmt.Sprintf("done=%d", row.Done))
	}
	if row.Failed > 0 {
		parts = append(parts, fmt.Sprintf("failed=%d", row.Failed))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
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
	return renderPipelineAdvanceRoutePreviews(w, results)
}

func renderPipelineAdvanceTable(w io.Writer, results []pipelineAdvanceResult) {
	if len(results) == 0 {
		fmt.Fprintln(w, "(no ready pipeline jobs)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB\tPIPELINE\tSTEP\tTARGET\tACTION\tSTATUS\tINSTANCE\tMESSAGE")
	for _, result := range results {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			result.JobID,
			emptyDash(result.Pipeline),
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

func renderPipelineAdvanceRoutePreviews(w io.Writer, results []pipelineAdvanceResult) error {
	wroteHeader := false
	for _, result := range results {
		if result.Preview == nil {
			continue
		}
		if !wroteHeader {
			fmt.Fprintln(w, "Routes:")
			wroteHeader = true
		}
		requestedName := ""
		if result.Preview.Dispatch != nil {
			requestedName = result.Preview.Dispatch.RequestedName
		}
		fmt.Fprintf(w, "%s step=%s target=%s instance=%s\n",
			result.JobID,
			emptyDash(result.StepID),
			emptyDash(result.Target),
			emptyDash(requestedName),
		)
		if result.Preview.Dispatch == nil || result.Preview.Dispatch.Preview == nil || !eventPublishPreviewHasRoutes(result.Preview.Dispatch.Preview) {
			fmt.Fprintln(w, "(no triggers matched)")
			continue
		}
		if err := renderEventPublishRoutePreview(w, result.Preview.Dispatch.Preview); err != nil {
			return err
		}
	}
	return nil
}
