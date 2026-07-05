package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/job"
)

func TestApprovalRequestApproveLinksManualGateAndNotifiesRequester(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)
	teamDir := filepath.Join(root, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:         "squ-47",
		Ticket:     "SQU-47",
		Target:     "worker",
		Pipeline:   "ticket_to_pr",
		Status:     job.StatusBlocked,
		LastEvent:  "step_blocked",
		LastStatus: "waiting for plan approval",
		CreatedAt:  now,
		UpdatedAt:  now,
		Steps: []job.Step{{
			ID:               "review",
			Target:           "manager",
			Status:           job.StatusBlocked,
			Gate:             job.StepGateManual,
			ApprovalRequired: true,
		}},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	bodyPath := filepath.Join(root, "plan.md")
	if err := os.WriteFile(bodyPath, []byte("Approve this implementation plan."), 0o644); err != nil {
		t.Fatalf("write body: %v", err)
	}
	t.Setenv("AGENT_TEAM_INSTANCE", "manager")

	request := NewRootCmd()
	requestOut, requestErr := &bytes.Buffer{}, &bytes.Buffer{}
	request.SetOut(requestOut)
	request.SetErr(requestErr)
	request.SetArgs([]string{
		"approval", "request",
		"--repo", root,
		"--job", "squ-47",
		"--id", "plan",
		"--title", "Plan approval",
		"--body-file", bodyPath,
		"--step", "review",
		"--json",
	})
	if err := request.Execute(); err != nil {
		t.Fatalf("approval request: %v\nstderr=%s", err, requestErr.String())
	}
	var requested job.Approval
	if err := json.Unmarshal(requestOut.Bytes(), &requested); err != nil {
		t.Fatalf("decode request: %v\nbody=%s", err, requestOut.String())
	}
	if requested.ID != "plan" || requested.Status != job.ApprovalStatusPending || requested.RequestingInstance != "manager" || requested.StepID != "review" {
		t.Fatalf("requested approval = %+v", requested)
	}
	linked, err := job.Read(teamDir, "squ-47")
	if err != nil {
		t.Fatalf("read linked job: %v", err)
	}
	if linked.Steps[0].ApprovalID != "plan" || linked.Steps[0].ApprovalStatus != job.ApprovalStatusPending {
		t.Fatalf("linked step = %+v", linked.Steps[0])
	}

	bypass := NewRootCmd()
	bypassErr := &bytes.Buffer{}
	bypass.SetErr(bypassErr)
	bypass.SetArgs([]string{"job", "approve", "squ-47", "--repo", root, "--step", "review"})
	if err := bypass.Execute(); err == nil {
		t.Fatalf("job approve bypass succeeded unexpectedly")
	}
	if !strings.Contains(bypassErr.String(), "requires approval") {
		t.Fatalf("job approve bypass stderr = %q", bypassErr.String())
	}

	approve := NewRootCmd()
	approveOut, approveErr := &bytes.Buffer{}, &bytes.Buffer{}
	approve.SetOut(approveOut)
	approve.SetErr(approveErr)
	approve.SetArgs([]string{
		"approval", "approve", "plan",
		"--repo", root,
		"--job", "squ-47",
		"--actor", "supervisor",
		"--notes", "plan is acceptable",
		"--json",
	})
	if err := approve.Execute(); err != nil {
		t.Fatalf("approval approve: %v\nstderr=%s", err, approveErr.String())
	}
	var approved job.Approval
	if err := json.Unmarshal(approveOut.Bytes(), &approved); err != nil {
		t.Fatalf("decode approve: %v\nbody=%s", err, approveOut.String())
	}
	if approved.Status != job.ApprovalStatusApproved || approved.Decision == nil || approved.Decision.Actor != "supervisor" || approved.Decision.Notes != "plan is acceptable" {
		t.Fatalf("approved = %+v", approved)
	}
	updated, err := job.Read(teamDir, "squ-47")
	if err != nil {
		t.Fatalf("read updated job: %v", err)
	}
	if updated.Status != job.StatusQueued || updated.Steps[0].Status != job.StatusQueued || updated.Steps[0].ApprovalStatus != job.ApprovalStatusApproved {
		t.Fatalf("updated job = %+v step=%+v", updated, updated.Steps[0])
	}
	events, err := job.ListEvents(teamDir, "squ-47")
	if err != nil {
		t.Fatalf("job events: %v", err)
	}
	if len(events) != 2 || events[0].Type != "approval.requested" || events[1].Type != "approval.decided" {
		t.Fatalf("events = %+v", events)
	}
	messages, err := daemon.ReadMessages(daemon.DaemonRoot(teamDir), "manager")
	if err != nil {
		t.Fatalf("read mailbox: %v", err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0].Body, `"approval_id":"plan"`) || !strings.Contains(messages[0].Body, `"status":"approved"`) {
		t.Fatalf("messages = %+v", messages)
	}
}
