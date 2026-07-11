package jobwrite

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/outcomes"
	"github.com/agent-team-project/agent-team/internal/pmprovider"
)

func TestWriteWithAuditFailureAttentionOnceForFailedTransitions(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")
	t.Setenv("LINEAR_USER_API_KEY", "")

	cases := []struct {
		name      string
		eventType string
		actor     string
		message   string
	}{
		{name: "dispatch rejection", eventType: "dispatch_failed", actor: "daemon", message: "no matching declared instance"},
		{name: "job kill", eventType: "instance_killed", actor: "cli", message: "kill worker-squ-68"},
		{name: "manual gate reject", eventType: "manual_gate_rejected", actor: "cli", message: "review rejected"},
		{name: "pipeline reject", eventType: "manual_gate_rejected", actor: "cli", message: "pipeline rejected"},
		{name: "auto advance rejection", eventType: "pipeline_advanced", actor: "daemon", message: "auto-advance dispatch rejected"},
		{name: "pipeline cancel", eventType: "cancelled", actor: "cli", message: "superseded"},
		{name: "reconcile crash", eventType: "instance_crashed", actor: "cli", message: "instance exited with code 2"},
		{name: "dead queue reconcile", eventType: "queue_reconcile", actor: "cli", message: "queue item dead-lettered"},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			teamDir := testTeamDir(t)
			now := time.Date(2026, 7, 3, 12, 0, i, 0, time.UTC)
			j, err := job.New(fmt.Sprintf("SQU-%d", 680+i), "worker", "kickoff", now)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			j.Status = job.StatusRunning
			j.LastEvent = "dispatch"
			j.LastStatus = "running"
			if err := job.Write(teamDir, j); err != nil {
				t.Fatalf("seed Write: %v", err)
			}

			j.Status = job.StatusFailed
			j.LastEvent = tc.eventType
			j.LastStatus = tc.message
			j.UpdatedAt = now.Add(time.Minute)
			if err := WriteWithAudit(teamDir, j, Options{
				EventType: tc.eventType,
				Actor:     tc.actor,
				Message:   tc.message,
				Data:      map[string]string{"case": tc.name},
			}); err != nil {
				t.Fatalf("WriteWithAudit failed transition: %v", err)
			}
			assertFailureAttentionAuditCount(t, teamDir, j.ID, 1)
			persisted, err := job.Read(teamDir, j.ID)
			if err != nil {
				t.Fatalf("Read persisted: %v", err)
			}
			if !persisted.LinearAttentionWritten {
				t.Fatalf("LinearAttentionWritten = false, want true")
			}

			persisted.LastStatus = tc.message + " repair pass"
			persisted.UpdatedAt = now.Add(2 * time.Minute)
			if err := WriteWithAudit(teamDir, persisted, Options{
				EventType: "repair_pass",
				Actor:     tc.actor,
				Message:   persisted.LastStatus,
			}); err != nil {
				t.Fatalf("WriteWithAudit repeated failed write: %v", err)
			}
			assertFailureAttentionAuditCount(t, teamDir, j.ID, 1)
		})
	}
}

func TestReconcilePRMarksMergedJobDone(t *testing.T) {
	teamDir := testTeamDir(t)
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	j, err := job.New("SQU-77", "worker", "ship the change", now)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j.Status = job.StatusRunning
	j.PR = "https://github.com/acme/repo/pull/77"
	j.Branch = "worktree-worker-squ-77"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("Write: %v", err)
	}
	merged := true
	result, err := ReconcilePR(teamDir, job.ReconcileInput{
		EventType: "pr.merged",
		Source:    "github",
		Action:    "closed",
		PR:        "77",
		PRURL:     "https://github.com/acme/repo/pull/77/",
		Branch:    "worktree-worker-squ-77",
		Merged:    &merged,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ReconcilePR: %v", err)
	}
	if result.MatchedBy != "pr_url" || result.Job.Status != job.StatusDone || result.Job.LastEvent != "pr.merged" {
		t.Fatalf("result = %+v", result)
	}
	updated, err := job.Read(teamDir, "squ-77")
	if err != nil {
		t.Fatalf("Read updated: %v", err)
	}
	if updated.Status != job.StatusDone || updated.LastStatus != "pull request merged" {
		t.Fatalf("updated = %+v", updated)
	}
	events, err := job.ListEvents(teamDir, "squ-77")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != "pr.merged" || events[0].Actor != "github" || events[0].Data["matched_by"] != "pr_url" || events[0].Data["source"] != "github" {
		t.Fatalf("events = %+v", events)
	}
}

func TestReconcilePRClosedFailureWritesLinearAttention(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")
	t.Setenv("LINEAR_USER_API_KEY", "")
	teamDir := testTeamDir(t)
	now := time.Date(2026, 7, 3, 12, 30, 0, 0, time.UTC)
	j, err := job.New("SQU-88", "worker", "ship the change", now)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j.Status = job.StatusRunning
	j.PR = "https://github.com/acme/repo/pull/88"
	j.Branch = "worker-squ-88"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("Write: %v", err)
	}
	merged := false
	result, err := ReconcilePR(teamDir, job.ReconcileInput{
		EventType: "pr.closed",
		Source:    "github",
		Action:    "closed",
		PR:        "88",
		PRURL:     "https://github.com/acme/repo/pull/88",
		Branch:    "worker-squ-88",
		Merged:    &merged,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ReconcilePR: %v", err)
	}
	if result.Job.Status != job.StatusFailed || result.Job.LastEvent != "pr.closed" {
		t.Fatalf("result = %+v, want failed pr.closed", result)
	}
	updated, err := job.Read(teamDir, "squ-88")
	if err != nil {
		t.Fatalf("Read updated: %v", err)
	}
	if updated.Status != job.StatusFailed || !updated.LinearAttentionWritten {
		t.Fatalf("updated = %+v, want failed with LinearAttentionWritten", updated)
	}
	assertFailureAttentionAuditCount(t, teamDir, "squ-88", 1)
	events, err := job.ListEvents(teamDir, "squ-88")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if events[0].Type != "pr.closed" || events[0].Actor != "github" || events[0].Data["matched_by"] != "pr_url" {
		t.Fatalf("first event = %+v, want pr.closed snapshot", events[0])
	}
}

func TestWriteWithAuditStampsOrigin(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatalf("mkdir team dir: %v", err)
	}
	config := `[project]
id = "project-1"

[pm]
provider = "none"
`
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(config), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	topology := `[instances.platform-worker]
agent = "worker"
ephemeral = true

[teams.platform]
instances = ["platform-worker"]
`
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topology), 0o644); err != nil {
		t.Fatalf("write instances: %v", err)
	}
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	j, err := job.New("SQU-90", "worker", "kickoff", now)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j.Instance = "platform-worker-squ-90"
	j.Status = job.StatusRunning
	j.LastEvent = "dispatch"
	j.LastStatus = "running"

	if err := WriteWithAudit(teamDir, j, Options{
		EventType: "dispatch",
		Actor:     "daemon",
		Data:      map[string]string{"trigger": "schedule:feedback-triage"},
	}); err != nil {
		t.Fatalf("WriteWithAudit: %v", err)
	}
	persisted, err := job.Read(teamDir, j.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if persisted.Origin.Project != "project-1" ||
		persisted.Origin.Team != "platform" ||
		persisted.Origin.Instance != "platform-worker-squ-90" ||
		persisted.Origin.Agent != "worker" ||
		persisted.Origin.Job != "squ-90" ||
		persisted.Origin.Trigger != "schedule:feedback-triage" {
		t.Fatalf("origin = %+v", persisted.Origin)
	}
	if persisted.Epic != "project-1" {
		t.Fatalf("epic = %q, want origin project", persisted.Epic)
	}
	events, err := job.ListEvents(teamDir, j.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1", len(events))
	}
	if events[0].Origin.Team != "platform" || events[0].Origin.Project != "project-1" {
		t.Fatalf("event origin = %+v", events[0].Origin)
	}
	if events[0].Data["epic"] != "project-1" {
		t.Fatalf("event data = %+v, want epic project-1", events[0].Data)
	}
}

func TestWriteWithAuditRecordsTerminalOutcome(t *testing.T) {
	teamDir := testTeamDir(t)
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	j, err := job.New("SQU-135", "worker", "ship outcome ledger", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j.Status = job.StatusRunning
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("seed Write: %v", err)
	}

	j.Status = job.StatusDone
	j.LastEvent = "closed"
	j.LastStatus = "done"
	j.UpdatedAt = now
	if err := WriteWithAudit(teamDir, j, Options{
		EventType: "closed",
		Actor:     "cli",
		Message:   "done",
	}); err != nil {
		t.Fatalf("WriteWithAudit terminal: %v", err)
	}

	rec, err := outcomes.ReadRecord(teamDir, j.ID)
	if err != nil {
		t.Fatalf("ReadRecord: %v", err)
	}
	if rec.JobID != "squ-135" || rec.Status != "done" || rec.TerminalEvent != "closed" {
		t.Fatalf("record = %+v", rec)
	}
}

func testTeamDir(t *testing.T) string {
	t.Helper()
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatalf("mkdir team dir: %v", err)
	}
	config := `[pm]
provider = "linear"

[linear]
team_id = "team-1"
ticket_prefix = "SQU"
in_progress_state = "In Progress"
attention_state = "Todo"
`
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(config), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return teamDir
}

func assertFailureAttentionAuditCount(t *testing.T, teamDir, jobID string, want int) {
	t.Helper()
	events, err := job.ListEvents(teamDir, jobID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	got := 0
	for _, ev := range events {
		if ev.Data["action"] == string(pmprovider.ActionFailureAttention) {
			got++
			if ev.Type != "linear_writeback_skipped" {
				t.Fatalf("failure attention event type = %q, want missing-key skip", ev.Type)
			}
			if ev.Message != "no Linear API key found" {
				t.Fatalf("failure attention message = %q, want missing-key reason", ev.Message)
			}
		}
	}
	if got != want {
		t.Fatalf("failure attention audit events = %d, want %d; events = %+v", got, want, events)
	}
}
