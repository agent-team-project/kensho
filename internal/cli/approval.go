package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/job"
	"github.com/spf13/cobra"
)

func newApprovalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "approval",
		Aliases: []string{"approvals"},
		Short:   "Manage durable job approval requests.",
		Long:    "Manage durable approval requests under `.agent_team/jobs/<job-id>/approvals/`.",
	}
	cmd.AddCommand(newApprovalRequestCmd())
	cmd.AddCommand(newApprovalLsCmd())
	cmd.AddCommand(newApprovalShowCmd())
	cmd.AddCommand(newApprovalApproveCmd())
	cmd.AddCommand(newApprovalRejectCmd())
	return cmd
}

func newApprovalRequestCmd() *cobra.Command {
	var (
		repo               string
		jobID              string
		approvalID         string
		title              string
		bodyFile           string
		stepID             string
		actor              string
		requestingInstance string
		notify             string
		jsonOut            bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "request",
		Short: "Create a pending approval request for a job.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(jobID) == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team approval request: --job is required.")
				return exitErr(2)
			}
			if strings.TrimSpace(title) == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team approval request: --title is required.")
				return exitErr(2)
			}
			if strings.TrimSpace(bodyFile) == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team approval request: --body-file is required.")
				return exitErr(2)
			}
			body, err := readMessageFile(bodyFile, "--body-file")
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team approval request: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, jobID)
			if err != nil {
				return err
			}
			requester := defaultJobGateActor(actor)
			if strings.TrimSpace(requestingInstance) == "" {
				requestingInstance = strings.TrimSpace(os.Getenv("AGENT_TEAM_INSTANCE"))
			}
			now := time.Now().UTC()
			approval, err := job.NewApproval(approvalID, j.ID, title, string(body), requester, requestingInstance, stepID, now)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team approval request: %v\n", err)
				return exitErr(2)
			}
			if _, err := os.Stat(job.ApprovalPath(teamDir, j.ID, approval.ID)); err == nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team approval request: approval %q already exists for job %s\n", approval.ID, j.ID)
				return exitErr(2)
			} else if !os.IsNotExist(err) {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team approval request: %v\n", err)
				return exitErr(1)
			}
			if approval.StepID != "" {
				if err := linkApprovalToJobStep(j, approval); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team approval request: %v\n", err)
					return exitErr(2)
				}
			}
			if err := job.WriteApproval(teamDir, approval); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team approval request: %v\n", err)
				return exitErr(1)
			}
			j.UpdatedAt = now
			j.LastEvent = "approval.requested"
			j.LastStatus = fmt.Sprintf("approval %s requested", approval.ID)
			if err := writeJobWithAudit(teamDir, j, "approval.requested", requester, j.LastStatus, approvalEventData(approval)); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team approval request: %v\n", err)
				return exitErr(1)
			}
			if strings.TrimSpace(notify) != "" {
				if _, err := notifyApprovalRequest(teamDir, approval, requester, notify); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team approval request: %v\n", err)
					return exitErr(1)
				}
			}
			return renderApprovalCommandResult(cmd.OutOrStdout(), approval, jsonOut, "requested")
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&jobID, "job", "", "Job id to attach the approval request to.")
	cmd.Flags().StringVar(&approvalID, "id", "", "Approval id; defaults to a timestamped title slug.")
	cmd.Flags().StringVar(&title, "title", "", "Approval request title.")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "Read approval request body from a file, or '-' for stdin.")
	cmd.Flags().StringVar(&stepID, "step", "", "Approval-required manual gate step to link to this approval.")
	cmd.Flags().StringVar(&actor, "actor", "", "Actor recorded on the approval request; defaults to AGENT_TEAM_INSTANCE or cli.")
	cmd.Flags().StringVar(&requestingInstance, "requesting-instance", "", "Instance to notify when the approval is decided; defaults to AGENT_TEAM_INSTANCE.")
	cmd.Flags().StringVar(&notify, "notify", "", "Optional instance to notify when this approval is requested.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the approval as JSON.")
	return cmd
}

func newApprovalLsCmd() *cobra.Command {
	var (
		repo      string
		jobID     string
		statusRaw string
		jsonOut   bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List approval requests for a job.",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(jobID) == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team approval ls: --job is required.")
				return exitErr(2)
			}
			var status job.ApprovalStatus
			if strings.TrimSpace(statusRaw) != "" {
				var err error
				status, err = job.ParseApprovalStatus(statusRaw)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team approval ls: %v\n", err)
					return exitErr(2)
				}
			}
			teamDir, _, err := readJobAndTeamDir(cmd, repo, jobID)
			if err != nil {
				return err
			}
			approvals, err := job.ListApprovals(teamDir, jobID)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team approval ls: %v\n", err)
				return exitErr(1)
			}
			approvals = filterApprovalsByStatus(approvals, status)
			return renderApprovalList(cmd.OutOrStdout(), approvals, jsonOut)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&jobID, "job", "", "Job id whose approval requests should be listed.")
	cmd.Flags().StringVar(&statusRaw, "status", "", "Filter by approval status: pending, approved, or rejected.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit approvals as JSON.")
	return cmd
}

func newApprovalShowCmd() *cobra.Command {
	var (
		repo    string
		jobID   string
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <approval-id>",
		Short: "Show one approval request.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(jobID) == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team approval show: --job is required.")
				return exitErr(2)
			}
			teamDir, _, err := readJobAndTeamDir(cmd, repo, jobID)
			if err != nil {
				return err
			}
			approval, err := job.ReadApproval(teamDir, jobID, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team approval show: %v\n", err)
				return exitErr(1)
			}
			return renderApprovalDetail(cmd.OutOrStdout(), approval, jsonOut)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&jobID, "job", "", "Job id that owns the approval request.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the approval as JSON.")
	return cmd
}

func newApprovalApproveCmd() *cobra.Command {
	return newApprovalDecisionCmd("approve", job.ApprovalStatusApproved)
}

func newApprovalRejectCmd() *cobra.Command {
	return newApprovalDecisionCmd("reject", job.ApprovalStatusRejected)
}

func newApprovalDecisionCmd(name string, status job.ApprovalStatus) *cobra.Command {
	var (
		repo      string
		jobID     string
		actor     string
		notes     string
		notesFile string
		jsonOut   bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   name + " <approval-id> [notes...]",
		Short: approvalDecisionVerb(status) + " one approval request.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(jobID) == "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team approval %s: --job is required.\n", name)
				return exitErr(2)
			}
			decisionNotes, err := optionalMessageBodyWithFlagNames(notes, notesFile, args[1:], "--notes", "--notes-file")
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team approval %s: %v\n", name, err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, jobID)
			if err != nil {
				return err
			}
			approval, err := job.ReadApproval(teamDir, j.ID, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team approval %s: %v\n", name, err)
				return exitErr(1)
			}
			decisionActor := defaultJobGateActor(actor)
			now := time.Now().UTC()
			if err := job.DecideApproval(approval, status, decisionActor, decisionNotes, now); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team approval %s: %v\n", name, err)
				return exitErr(2)
			}
			if err := applyApprovalDecisionToJobStep(j, approval); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team approval %s: %v\n", name, err)
				return exitErr(2)
			}
			if err := job.WriteApproval(teamDir, approval); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team approval %s: %v\n", name, err)
				return exitErr(1)
			}
			j.UpdatedAt = now
			j.LastEvent = "approval.decided"
			j.LastStatus = fmt.Sprintf("approval %s %s", approval.ID, approval.Status)
			if err := writeJobWithAudit(teamDir, j, "approval.decided", decisionActor, j.LastStatus, approvalEventData(approval)); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team approval %s: %v\n", name, err)
				return exitErr(1)
			}
			if _, err := notifyApprovalDecision(teamDir, approval, decisionActor); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team approval %s: %v\n", name, err)
				return exitErr(1)
			}
			return renderApprovalCommandResult(cmd.OutOrStdout(), approval, jsonOut, string(status))
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&jobID, "job", "", "Job id that owns the approval request.")
	cmd.Flags().StringVar(&actor, "actor", "", "Actor recorded on the decision; defaults to AGENT_TEAM_INSTANCE or cli.")
	cmd.Flags().StringVar(&notes, "notes", "", "Decision notes recorded on the approval.")
	cmd.Flags().StringVar(&notesFile, "notes-file", "", "Read decision notes from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the approval as JSON.")
	return cmd
}

func approvalDecisionVerb(status job.ApprovalStatus) string {
	switch status {
	case job.ApprovalStatusApproved:
		return "Approve"
	case job.ApprovalStatusRejected:
		return "Reject"
	default:
		return "Decide"
	}
}

func linkApprovalToJobStep(j *job.Job, approval *job.Approval) error {
	idx := jobStepIndex(j, approval.StepID)
	if idx == -1 {
		return fmt.Errorf("step %q not found", approval.StepID)
	}
	step := &j.Steps[idx]
	if step.Gate != job.StepGateManual {
		return fmt.Errorf("step %q is not a manual gate", step.ID)
	}
	if !step.ApprovalRequired {
		return fmt.Errorf("step %q is not configured with approval_required", step.ID)
	}
	if strings.TrimSpace(step.ApprovalID) != "" {
		return fmt.Errorf("step %q already references approval %q", step.ID, step.ApprovalID)
	}
	step.ApprovalID = approval.ID
	step.ApprovalStatus = approval.Status
	return nil
}

func applyApprovalDecisionToJobStep(j *job.Job, approval *job.Approval) error {
	if strings.TrimSpace(approval.StepID) == "" {
		return nil
	}
	idx := jobStepIndex(j, approval.StepID)
	if idx == -1 {
		return fmt.Errorf("step %q not found", approval.StepID)
	}
	step := &j.Steps[idx]
	if step.ApprovalID != approval.ID {
		return fmt.Errorf("step %q references approval %q, not %q", step.ID, step.ApprovalID, approval.ID)
	}
	step.ApprovalStatus = approval.Status
	message := fmt.Sprintf("approval %s %s", approval.ID, approval.Status)
	switch approval.Status {
	case job.ApprovalStatusApproved:
		if step.Status == job.StatusBlocked {
			return updateJobStep(j, step.ID, job.StatusQueued, jobStepUpdate{Message: message})
		}
	case job.ApprovalStatusRejected:
		if step.Status != job.StatusDone && step.Status != job.StatusFailed {
			return updateJobStep(j, step.ID, job.StatusFailed, jobStepUpdate{Message: message})
		}
	}
	return nil
}

func filterApprovalsByStatus(approvals []*job.Approval, status job.ApprovalStatus) []*job.Approval {
	if status == "" {
		return approvals
	}
	out := make([]*job.Approval, 0, len(approvals))
	for _, approval := range approvals {
		if approval.Status == status {
			out = append(out, approval)
		}
	}
	return out
}

func approvalEventData(approval *job.Approval) map[string]string {
	data := map[string]string{}
	if approval == nil {
		return data
	}
	data["approval_id"] = approval.ID
	data["approval_status"] = string(approval.Status)
	if strings.TrimSpace(approval.StepID) != "" {
		data["step"] = approval.StepID
	}
	return data
}

func notifyApprovalRequest(teamDir string, approval *job.Approval, actor, to string) (*daemon.Message, error) {
	to = strings.TrimSpace(to)
	if to == "" {
		return nil, nil
	}
	return appendApprovalMessage(teamDir, to, actor, map[string]string{
		"event":       "approval.requested",
		"job_id":      approval.JobID,
		"approval_id": approval.ID,
		"status":      string(approval.Status),
		"title":       approval.Title,
		"step":        approval.StepID,
	})
}

func notifyApprovalDecision(teamDir string, approval *job.Approval, actor string) (*daemon.Message, error) {
	to := strings.TrimSpace(approval.RequestingInstance)
	if to == "" {
		return nil, nil
	}
	body := map[string]string{
		"event":       "approval.decided",
		"job_id":      approval.JobID,
		"approval_id": approval.ID,
		"status":      string(approval.Status),
		"title":       approval.Title,
		"step":        approval.StepID,
	}
	if approval.Decision != nil {
		body["actor"] = approval.Decision.Actor
		body["notes"] = approval.Decision.Notes
	}
	return appendApprovalMessage(teamDir, to, actor, body)
}

func appendApprovalMessage(teamDir, to, from string, body map[string]string) (*daemon.Message, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	msg := &daemon.Message{From: strings.TrimSpace(from), Body: string(raw)}
	if err := daemon.AppendMessage(daemon.DaemonRoot(teamDir), to, msg); err != nil {
		return nil, err
	}
	return msg, nil
}

func renderApprovalCommandResult(w io.Writer, approval *job.Approval, jsonOut bool, action string) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(approval)
	}
	fmt.Fprintf(w, "%s approval %s for %s: %s\n", action, approval.ID, approval.JobID, approval.Status)
	return nil
}

func renderApprovalList(w io.Writer, approvals []*job.Approval, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(approvals)
	}
	if len(approvals) == 0 {
		fmt.Fprintln(w, "(no approvals)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tTITLE\tSTEP\tREQUESTED_BY\tDECISION_BY\tUPDATED")
	for _, approval := range approvals {
		decisionBy := "-"
		if approval.Decision != nil {
			decisionBy = approval.Decision.Actor
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			approval.ID,
			approval.Status,
			approval.Title,
			emptyDash(approval.StepID),
			emptyDash(approval.RequestedBy),
			decisionBy,
			approvalUpdatedAt(approval).Format(time.RFC3339),
		)
	}
	return tw.Flush()
}

func renderApprovalDetail(w io.Writer, approval *job.Approval, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(approval)
	}
	fmt.Fprintf(w, "ID:                  %s\n", approval.ID)
	fmt.Fprintf(w, "Job:                 %s\n", approval.JobID)
	fmt.Fprintf(w, "Status:              %s\n", approval.Status)
	fmt.Fprintf(w, "Title:               %s\n", approval.Title)
	fmt.Fprintf(w, "Requested At:        %s\n", approval.RequestedAt.Format(time.RFC3339))
	fmt.Fprintf(w, "Requested By:        %s\n", emptyDash(approval.RequestedBy))
	fmt.Fprintf(w, "Requesting Instance: %s\n", emptyDash(approval.RequestingInstance))
	if strings.TrimSpace(approval.StepID) != "" {
		fmt.Fprintf(w, "Step:                %s\n", approval.StepID)
	}
	if approval.Decision != nil {
		fmt.Fprintf(w, "Decision At:         %s\n", approval.Decision.TS.Format(time.RFC3339))
		fmt.Fprintf(w, "Decision Actor:      %s\n", approval.Decision.Actor)
		if strings.TrimSpace(approval.Decision.Notes) != "" {
			fmt.Fprintf(w, "Decision Notes:      %s\n", approval.Decision.Notes)
		}
	}
	fmt.Fprintln(w, "Body:")
	fmt.Fprintln(w, approval.Body)
	return nil
}

func approvalUpdatedAt(approval *job.Approval) time.Time {
	if approval != nil && approval.Decision != nil {
		return approval.Decision.TS
	}
	if approval == nil {
		return time.Time{}
	}
	return approval.RequestedAt
}
