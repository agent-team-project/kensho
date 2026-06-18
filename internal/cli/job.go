package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/job"
	"github.com/spf13/cobra"
)

func newJobCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Manage durable work units.",
		Long: "Manage durable work units backed by `.agent_team/jobs/<job-id>.toml`. " +
			"Jobs track ticket ownership, target agent, lifecycle state, instance, branch, worktree, and PR metadata.",
	}
	cmd.AddCommand(newJobCreateCmd())
	cmd.AddCommand(newJobLsCmd())
	cmd.AddCommand(newJobShowCmd())
	cmd.AddCommand(newJobDispatchCmd())
	cmd.AddCommand(newJobSendCmd())
	cmd.AddCommand(newJobCloseCmd())
	cmd.AddCommand(newJobCleanupCmd())
	cmd.AddCommand(newJobStepCmd())
	cmd.AddCommand(newJobAdvanceCmd())
	return cmd
}

func newJobCreateCmd() *cobra.Command {
	var (
		repo        string
		targetAgent string
		id          string
		kickoff     string
		kickoffFile string
		instance    string
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "create <ticket> [kickoff...]",
		Short: "Create a durable job for a ticket.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job create: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			ticket := args[0]
			kickoffText, err := dispatchKickoff(ticket, kickoff, kickoffFile, args[1:])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: %v\n", err)
				return exitErr(2)
			}
			j, err := job.New(ticket, targetAgent, kickoffText, time.Now())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: %v\n", err)
				return exitErr(2)
			}
			if strings.TrimSpace(id) != "" {
				normalized := job.NormalizeID(id)
				if normalized == "" {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: --id %q produced an empty normalized id.\n", id)
					return exitErr(2)
				}
				j.ID = normalized
			}
			if strings.TrimSpace(instance) != "" {
				j.Instance = strings.TrimSpace(instance)
			}
			if _, err := os.Stat(job.Path(teamDir, j.ID)); err == nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: job %q already exists.\n", j.ID)
				return exitErr(2)
			}
			if err := job.Write(teamDir, j); err != nil {
				return err
			}
			return renderJobResult(cmd.OutOrStdout(), j, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&targetAgent, "target", "worker", "Target agent that should own this job.")
	cmd.Flags().StringVar(&id, "id", "", "Override the normalized job id (default: ticket slug).")
	cmd.Flags().StringVar(&kickoff, "kickoff", "", "Kickoff text for the target agent.")
	cmd.Flags().StringVar(&kickoffFile, "kickoff-file", "", "Read kickoff text from a file.")
	cmd.Flags().StringVar(&instance, "instance", "", "Instance name that owns the job (default set during dispatch).")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func newJobLsCmd() *cobra.Command {
	var (
		repo         string
		statusFilter string
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List durable jobs.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ls: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job ls: %v\n", err)
				return exitErr(2)
			}
			var want job.Status
			if strings.TrimSpace(statusFilter) != "" {
				want, err = job.ParseStatus(statusFilter)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job ls: %v\n", err)
					return exitErr(2)
				}
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			jobs, err := job.List(teamDir)
			if err != nil {
				return err
			}
			filtered := make([]*job.Job, 0, len(jobs))
			for _, j := range jobs {
				if want != "" && j.Status != want {
					continue
				}
				filtered = append(filtered, j)
			}
			out := cmd.OutOrStdout()
			if jsonOut {
				return json.NewEncoder(out).Encode(filtered)
			}
			if tmpl != nil {
				for _, j := range filtered {
					if err := renderJobTemplate(out, j, tmpl); err != nil {
						return err
					}
				}
				return nil
			}
			renderJobTable(out, filtered)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&statusFilter, "status", "", "Filter by status: queued, running, blocked, done, or failed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func newJobShowCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <job-id>",
		Short: "Show one durable job.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job show: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job show: %v\n", err)
				return exitErr(2)
			}
			j, err := readJobFromRepo(cmd, repo, args[0])
			if err != nil {
				return err
			}
			return renderJobResult(cmd.OutOrStdout(), j, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func newJobDispatchCmd() *cobra.Command {
	var (
		repo      string
		source    string
		workspace string
		jsonOut   bool
		format    string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "dispatch <job-id>",
		Short: "Dispatch a job to its target agent.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job dispatch: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job dispatch: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			payload, requestedName, err := buildDispatchEventPayload(j.Target, j.Ticket, j.Kickoff, j.Instance, source, workspace)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job dispatch: %v\n", err)
				return exitErr(2)
			}
			payload["job_id"] = j.ID
			payload["job"] = j.ID
			dc, err := newDaemonClient(teamDir)
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job dispatch: daemon is not running — start it with `agent-team start`.")
				return exitErr(2)
			}
			res, err := dc.PublishEvent("agent.dispatch", payload)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job dispatch: %v\n", err)
				return exitErr(1)
			}
			if latest, err := job.Read(teamDir, j.ID); err == nil {
				j = latest
			}
			applyDispatchResponseToJob(j, requestedName, res)
			if err := job.Write(teamDir, j); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if jsonOut {
				return json.NewEncoder(out).Encode(struct {
					Job   *job.Job       `json:"job"`
					Event *eventResponse `json:"event"`
				}{Job: j, Event: res})
			}
			if tmpl != nil {
				return renderJobTemplate(out, j, tmpl)
			}
			renderDispatchOutcome(out, j.Target, requestedName, res)
			fmt.Fprintf(out, "Job: %s status=%s instance=%s\n", j.ID, j.Status, j.Instance)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&source, "source", "", "Source instance for the dispatch event (default: AGENT_TEAM_INSTANCE or cli).")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for spawned children: auto, worktree, or repo.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job and daemon event outcome as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func newJobSendCmd() *cobra.Command {
	var (
		repo         string
		from         string
		allowMissing bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "send <job-id> <message...>",
		Short: "Send a mailbox message to a job's owning instance.",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job send: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job send: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			if strings.TrimSpace(j.Instance) == "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job send: job %q has no owning instance; dispatch it first.\n", j.ID)
				return exitErr(2)
			}
			client, err := sendClientForTeamDir(teamDir)
			if err != nil {
				return err
			}
			body := strings.Join(args[1:], " ")
			if err := runSendWithClient(io.Discard, cmd.ErrOrStderr(), client, j.Instance, body, sendOptions{
				From:         from,
				AllowMissing: allowMissing,
			}); err != nil {
				return err
			}
			j.LastEvent = "message_sent"
			j.LastStatus = strings.TrimSpace(body)
			j.UpdatedAt = time.Now().UTC()
			if err := job.Write(teamDir, j); err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(j)
			}
			if tmpl != nil {
				return renderJobTemplate(cmd.OutOrStdout(), j, tmpl)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  sent   %-20s job=%s\n", j.Instance, j.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&from, "from", "(cli)", "Sender label recorded with the message.")
	cmd.Flags().BoolVar(&allowMissing, "allow-missing", false, "Allow queueing a message for an instance the daemon does not know yet.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job with a Go template, e.g. '{{.ID}} {{.LastEvent}}'.")
	return cmd
}

func newJobCloseCmd() *cobra.Command {
	var (
		repo    string
		status  string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "close <job-id>",
		Short: "Close a job as done or failed.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job close: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if status != string(job.StatusDone) && status != string(job.StatusFailed) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job close: --status must be done or failed.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job close: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			j.Status = job.Status(status)
			j.LastEvent = "closed"
			j.LastStatus = status
			j.UpdatedAt = time.Now().UTC()
			if err := job.Write(teamDir, j); err != nil {
				return err
			}
			return renderJobResult(cmd.OutOrStdout(), j, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&status, "status", string(job.StatusDone), "Close status: done or failed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func newJobCleanupCmd() *cobra.Command {
	var (
		repo    string
		merged  bool
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "cleanup <job-id>",
		Short: "Remove a job-owned worker worktree and branch after merge.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job cleanup: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if !merged {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job cleanup: pass --merged after confirming the job's PR has merged.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job cleanup: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			repoRoot := filepath.Dir(teamDir)
			summary, err := cleanupJobOwnedWorktree(repoRoot, j)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job cleanup: %v\n", err)
				return exitErr(1)
			}
			j.Worktree = ""
			j.Branch = ""
			j.LastEvent = "cleanup"
			j.LastStatus = summary
			j.UpdatedAt = time.Now().UTC()
			if err := job.Write(teamDir, j); err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(j)
			}
			if tmpl != nil {
				return renderJobTemplate(cmd.OutOrStdout(), j, tmpl)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Job: %s cleanup complete (%s)\n", j.ID, summary)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&merged, "merged", false, "Confirm the job's PR has merged before removing its worktree and branch.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job with a Go template, e.g. '{{.ID}} {{.LastStatus}}'.")
	return cmd
}

func newJobStepCmd() *cobra.Command {
	var (
		repo      string
		status    string
		message   string
		instance  string
		pr        string
		branch    string
		worktree  string
		advance   bool
		workspace string
		jsonOut   bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "step <job-id> <step-id>",
		Short: "Update a pipeline job step status.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			stepStatus, err := job.ParseStatus(status)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job step: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			if err := updateJobStep(j, args[1], stepStatus, jobStepUpdate{
				Message:  message,
				Instance: instance,
				PR:       pr,
				Branch:   branch,
				Worktree: worktree,
			}); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job step: %v\n", err)
				return exitErr(2)
			}
			if err := job.Write(teamDir, j); err != nil {
				return err
			}
			if advance && stepStatus == job.StatusDone {
				res, err := advanceJob(cmd, teamDir, j, workspace)
				if err != nil {
					return err
				}
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(res)
				}
				return renderJobAdvanceResult(cmd.OutOrStdout(), res)
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(j)
			}
			renderJobDetail(cmd.OutOrStdout(), j)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&status, "status", string(job.StatusDone), "Step status: queued, running, blocked, done, or failed.")
	cmd.Flags().StringVar(&message, "message", "", "Status message recorded on the job.")
	cmd.Flags().StringVar(&instance, "instance", "", "Instance that owns or completed this step.")
	cmd.Flags().StringVar(&pr, "pr", "", "PR URL to record on the job.")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch name to record on the job.")
	cmd.Flags().StringVar(&worktree, "worktree", "", "Worktree path to record on the job.")
	cmd.Flags().BoolVar(&advance, "advance", false, "After marking the step done, dispatch the next ready step.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for an advanced step: auto, worktree, or repo.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job or advance result as JSON.")
	return cmd
}

func newJobAdvanceCmd() *cobra.Command {
	var (
		repo      string
		workspace string
		jsonOut   bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "advance <job-id>",
		Short: "Dispatch the next ready step in a pipeline job.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			res, err := advanceJob(cmd, teamDir, j, workspace)
			if err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(res)
			}
			return renderJobAdvanceResult(cmd.OutOrStdout(), res)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for the advanced step: auto, worktree, or repo.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job and daemon event outcome as JSON.")
	return cmd
}

func readJobFromRepo(cmd *cobra.Command, repo, id string) (*job.Job, error) {
	_, j, err := readJobAndTeamDir(cmd, repo, id)
	return j, err
}

func readJobAndTeamDir(cmd *cobra.Command, repo, id string) (string, *job.Job, error) {
	teamDir, err := resolveTeamDir(cmd, repo)
	if err != nil {
		return "", nil, err
	}
	j, err := job.Read(teamDir, id)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job: %v\n", err)
		return "", nil, exitErr(1)
	}
	return teamDir, j, nil
}

func applyDispatchResponseToJob(j *job.Job, requestedName string, res *eventResponse) {
	now := time.Now().UTC()
	j.UpdatedAt = now
	if strings.TrimSpace(j.Instance) == "" {
		j.Instance = requestedName
	}
	for _, d := range res.Dispatched {
		if id, _ := d["instance_id"].(string); strings.TrimSpace(id) != "" {
			j.Instance = id
			j.Status = job.StatusRunning
			j.LastEvent = "dispatched"
			j.LastStatus = "running"
			return
		}
	}
	if len(res.Queued) > 0 {
		if strings.TrimSpace(j.Instance) == "" {
			j.Instance = requestedName
		}
		j.Status = job.StatusQueued
		j.LastEvent = "queued"
		j.LastStatus = "queued"
		return
	}
	for _, r := range res.Rejected {
		reason, _ := r["reason"].(string)
		if id, _ := r["instance_id"].(string); strings.TrimSpace(id) != "" {
			j.Instance = id
		}
		if strings.Contains(reason, "already running") {
			j.Status = job.StatusRunning
			j.LastEvent = "already_running"
			j.LastStatus = reason
			return
		}
		if strings.Contains(reason, "already queued") {
			j.Status = job.StatusQueued
			j.LastEvent = "already_queued"
			j.LastStatus = reason
			return
		}
		j.Status = job.StatusFailed
		j.LastEvent = "dispatch_rejected"
		j.LastStatus = reason
		return
	}
	if len(res.Matched) == 0 {
		j.Status = job.StatusFailed
		j.LastEvent = "dispatch_no_match"
		j.LastStatus = "no triggers matched"
	}
}

type jobStepUpdate struct {
	Message  string
	Instance string
	PR       string
	Branch   string
	Worktree string
}

type jobAdvanceResult struct {
	Job     *job.Job       `json:"job"`
	Step    *job.Step      `json:"step,omitempty"`
	Event   *eventResponse `json:"event,omitempty"`
	Message string         `json:"message,omitempty"`
}

func updateJobStep(j *job.Job, stepID string, status job.Status, update jobStepUpdate) error {
	idx := jobStepIndex(j, stepID)
	if idx == -1 {
		return fmt.Errorf("step %q not found", stepID)
	}
	now := time.Now().UTC()
	step := &j.Steps[idx]
	step.Status = status
	if strings.TrimSpace(update.Instance) != "" {
		step.Instance = strings.TrimSpace(update.Instance)
	}
	if (status == job.StatusRunning || status == job.StatusQueued) && step.StartedAt.IsZero() {
		step.StartedAt = now
	}
	if status == job.StatusDone || status == job.StatusFailed {
		if step.StartedAt.IsZero() {
			step.StartedAt = now
		}
		step.FinishedAt = now
	}
	if update.PR != "" {
		j.PR = update.PR
	}
	if update.Branch != "" {
		j.Branch = update.Branch
	}
	if update.Worktree != "" {
		j.Worktree = update.Worktree
	}
	j.LastEvent = "step_" + string(status)
	if strings.TrimSpace(update.Message) != "" {
		j.LastStatus = strings.TrimSpace(update.Message)
	} else {
		j.LastStatus = stepID + " " + string(status)
	}
	j.UpdatedAt = now
	switch status {
	case job.StatusFailed:
		j.Status = job.StatusFailed
	case job.StatusBlocked:
		j.Status = job.StatusBlocked
	case job.StatusDone:
		if allJobStepsDone(j) {
			j.Status = job.StatusDone
			j.LastEvent = "pipeline_done"
			j.LastStatus = "all steps done"
		} else {
			j.Status = job.StatusRunning
		}
	default:
		j.Status = status
	}
	return nil
}

func advanceJob(cmd *cobra.Command, teamDir string, j *job.Job, workspace string) (*jobAdvanceResult, error) {
	step := nextReadyJobStep(j)
	if step == nil {
		now := time.Now().UTC()
		if allJobStepsDone(j) {
			j.Status = job.StatusDone
			j.LastEvent = "pipeline_done"
			j.LastStatus = "all steps done"
			j.UpdatedAt = now
			if err := job.Write(teamDir, j); err != nil {
				return nil, err
			}
			return &jobAdvanceResult{Job: j, Message: "all steps done"}, nil
		}
		j.Status = job.StatusBlocked
		j.LastEvent = "advance_blocked"
		j.LastStatus = "no ready steps"
		j.UpdatedAt = now
		if err := job.Write(teamDir, j); err != nil {
			return nil, err
		}
		return &jobAdvanceResult{Job: j, Message: "no ready steps"}, nil
	}
	name := step.Instance
	if strings.TrimSpace(name) == "" {
		name = step.Target + "-" + j.ID + "-" + job.NormalizeID(step.ID)
	}
	payload, requestedName, err := buildDispatchEventPayload(step.Target, j.Ticket, j.Kickoff, name, "job:"+j.ID, workspace)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job advance: %v\n", err)
		return nil, exitErr(2)
	}
	payload["job_id"] = j.ID
	payload["job"] = j.ID
	if j.Pipeline != "" {
		payload["pipeline"] = j.Pipeline
	}
	payload["pipeline_step"] = step.ID
	dc, err := newDaemonClient(teamDir)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job advance: daemon is not running — start it with `agent-team start`.")
		return nil, exitErr(2)
	}
	res, err := dc.PublishEvent("agent.dispatch", payload)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job advance: %v\n", err)
		return nil, exitErr(1)
	}
	if latest, err := job.Read(teamDir, j.ID); err == nil {
		j = latest
	}
	applyAdvanceResponseToJobStep(j, step.ID, requestedName, res)
	if err := job.Write(teamDir, j); err != nil {
		return nil, err
	}
	if idx := jobStepIndex(j, step.ID); idx >= 0 {
		return &jobAdvanceResult{Job: j, Step: &j.Steps[idx], Event: res}, nil
	}
	return &jobAdvanceResult{Job: j, Event: res}, nil
}

func applyAdvanceResponseToJobStep(j *job.Job, stepID, requestedName string, res *eventResponse) {
	status := job.StatusFailed
	instance := requestedName
	lastEvent := "advance_rejected"
	lastStatus := "dispatch rejected"
	for _, d := range res.Dispatched {
		if id, _ := d["instance_id"].(string); strings.TrimSpace(id) != "" {
			status = job.StatusRunning
			instance = id
			lastEvent = "advance_dispatched"
			lastStatus = "running " + stepID
			goto done
		}
	}
	if len(res.Queued) > 0 {
		status = job.StatusQueued
		lastEvent = "advance_queued"
		lastStatus = "queued " + stepID
		goto done
	}
	if len(res.Messaged) > 0 {
		status = job.StatusRunning
		instance = res.Messaged[0]
		lastEvent = "advance_messaged"
		lastStatus = "running " + stepID
		goto done
	}
	for _, r := range res.Rejected {
		reason, _ := r["reason"].(string)
		if id, _ := r["instance_id"].(string); strings.TrimSpace(id) != "" {
			instance = id
		}
		if strings.Contains(reason, "already running") {
			status = job.StatusRunning
			lastEvent = "advance_already_running"
			lastStatus = reason
			goto done
		}
		if strings.Contains(reason, "already queued") {
			status = job.StatusQueued
			lastEvent = "advance_already_queued"
			lastStatus = reason
			goto done
		}
		lastStatus = reason
		break
	}
	if len(res.Matched) == 0 {
		lastEvent = "advance_no_match"
		lastStatus = "no triggers matched"
	}
done:
	_ = updateJobStep(j, stepID, status, jobStepUpdate{Instance: instance, Message: lastStatus})
	j.LastEvent = lastEvent
	j.LastStatus = lastStatus
}

func nextReadyJobStep(j *job.Job) *job.Step {
	done := map[string]bool{}
	for _, step := range j.Steps {
		if step.Status == job.StatusDone {
			done[step.ID] = true
		}
	}
	for i := range j.Steps {
		step := &j.Steps[i]
		if step.Status == job.StatusDone || step.Status == job.StatusFailed || step.Status == job.StatusRunning || step.Status == job.StatusQueued {
			continue
		}
		ready := true
		for _, dep := range step.After {
			if !done[dep] {
				ready = false
				break
			}
		}
		if ready {
			return step
		}
	}
	for i := range j.Steps {
		step := &j.Steps[i]
		if step.Status != job.StatusQueued {
			continue
		}
		ready := true
		for _, dep := range step.After {
			if !done[dep] {
				ready = false
				break
			}
		}
		if ready {
			return step
		}
	}
	return nil
}

func allJobStepsDone(j *job.Job) bool {
	if len(j.Steps) == 0 {
		return false
	}
	for _, step := range j.Steps {
		if step.Status != job.StatusDone {
			return false
		}
	}
	return true
}

func jobStepIndex(j *job.Job, stepID string) int {
	for i, step := range j.Steps {
		if step.ID == stepID {
			return i
		}
	}
	return -1
}

func renderJobAdvanceResult(w io.Writer, res *jobAdvanceResult) error {
	if res.Message != "" {
		fmt.Fprintf(w, "Job: %s %s\n", res.Job.ID, res.Message)
		return nil
	}
	if res.Step != nil {
		fmt.Fprintf(w, "Job: %s advanced step=%s status=%s instance=%s\n",
			res.Job.ID, res.Step.ID, res.Step.Status, emptyDash(res.Step.Instance))
	}
	if res.Event != nil {
		renderDispatchOutcome(w, "", "", res.Event)
	}
	return nil
}

func cleanupJobOwnedWorktree(repoRoot string, j *job.Job) (string, error) {
	if strings.TrimSpace(j.Worktree) == "" && strings.TrimSpace(j.Branch) == "" {
		return "nothing to clean", nil
	}
	removed := make([]string, 0, 2)
	if strings.TrimSpace(j.Worktree) != "" {
		if err := validateJobOwnedWorktree(repoRoot, j.Worktree); err != nil {
			return "", err
		}
		if _, err := os.Stat(j.Worktree); err == nil {
			if out, err := exec.Command("git", "-C", repoRoot, "worktree", "remove", "--force", j.Worktree).CombinedOutput(); err != nil {
				return "", fmt.Errorf("remove worktree %s: %w: %s", j.Worktree, err, strings.TrimSpace(string(out)))
			}
			removed = append(removed, "worktree")
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	if strings.TrimSpace(j.Branch) != "" {
		exists, err := gitBranchExists(repoRoot, j.Branch)
		if err != nil {
			return "", err
		}
		if exists {
			if out, err := exec.Command("git", "-C", repoRoot, "branch", "-d", j.Branch).CombinedOutput(); err != nil {
				return "", fmt.Errorf("remove branch %s: %w: %s", j.Branch, err, strings.TrimSpace(string(out)))
			}
			removed = append(removed, "branch")
		}
	}
	if len(removed) == 0 {
		return "nothing to clean", nil
	}
	return "removed " + strings.Join(removed, " and "), nil
}

func validateJobOwnedWorktree(repoRoot, worktreePath string) error {
	root, err := filepath.Abs(filepath.Join(repoRoot, ".claude", "worktrees"))
	if err != nil {
		return err
	}
	if resolvedRoot, err := filepath.EvalSymlinks(root); err == nil {
		root = resolvedRoot
	}
	path, err := filepath.Abs(worktreePath)
	if err != nil {
		return err
	}
	if resolvedPath, err := filepath.EvalSymlinks(path); err == nil {
		path = resolvedPath
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("refusing to remove worktree outside %s: %s", root, path)
	}
	return nil
}

func gitBranchExists(repoRoot, branch string) (bool, error) {
	out, err := exec.Command("git", "-C", repoRoot, "branch", "--list", branch, "--format", "%(refname:short)").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("list branch %s: %w: %s", branch, err, strings.TrimSpace(string(out)))
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == branch {
			return true, nil
		}
	}
	return false, nil
}

func parseJobFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderJobResult(w io.Writer, j *job.Job, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(j)
	}
	if tmpl != nil {
		return renderJobTemplate(w, j, tmpl)
	}
	renderJobDetail(w, j)
	return nil
}

func renderJobTemplate(w io.Writer, j *job.Job, tmpl *template.Template) error {
	if err := tmpl.Execute(w, j); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func renderJobTable(w io.Writer, jobs []*job.Job) {
	if len(jobs) == 0 {
		fmt.Fprintln(w, "(no jobs)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tTARGET\tINSTANCE\tTICKET\tUPDATED")
	for _, j := range jobs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			j.ID, j.Status, j.Target, emptyDash(j.Instance), j.Ticket, j.UpdatedAt.Format(time.RFC3339))
	}
	_ = tw.Flush()
}

func renderJobDetail(w io.Writer, j *job.Job) {
	fmt.Fprintf(w, "ID:          %s\n", j.ID)
	fmt.Fprintf(w, "Status:      %s\n", j.Status)
	fmt.Fprintf(w, "Ticket:      %s\n", j.Ticket)
	if j.TicketURL != "" {
		fmt.Fprintf(w, "Ticket URL:  %s\n", j.TicketURL)
	}
	fmt.Fprintf(w, "Target:      %s\n", j.Target)
	if j.Instance != "" {
		fmt.Fprintf(w, "Instance:    %s\n", j.Instance)
	}
	if j.Pipeline != "" {
		fmt.Fprintf(w, "Pipeline:    %s\n", j.Pipeline)
	}
	if j.Branch != "" {
		fmt.Fprintf(w, "Branch:      %s\n", j.Branch)
	}
	if j.Worktree != "" {
		fmt.Fprintf(w, "Worktree:    %s\n", j.Worktree)
	}
	if j.PR != "" {
		fmt.Fprintf(w, "PR:          %s\n", j.PR)
	}
	if j.LastEvent != "" {
		fmt.Fprintf(w, "Last Event:  %s\n", j.LastEvent)
	}
	if j.LastStatus != "" {
		fmt.Fprintf(w, "Last Status: %s\n", j.LastStatus)
	}
	if j.Kickoff != "" {
		fmt.Fprintf(w, "Kickoff:     %s\n", j.Kickoff)
	}
	if len(j.Steps) > 0 {
		fmt.Fprintln(w, "Steps:")
		for _, step := range j.Steps {
			instance := step.Instance
			if instance == "" {
				instance = "-"
			}
			after := "-"
			if len(step.After) > 0 {
				after = strings.Join(step.After, ",")
			}
			fmt.Fprintf(w, "  %s  target=%s status=%s instance=%s after=%s\n",
				step.ID, step.Target, step.Status, instance, after)
		}
	}
	fmt.Fprintf(w, "Created:     %s\n", j.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(w, "Updated:     %s\n", j.UpdatedAt.Format(time.RFC3339))
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
