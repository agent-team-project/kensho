package jobwrite

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/linearwriteback"
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

func testTeamDir(t *testing.T) string {
	t.Helper()
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatalf("mkdir team dir: %v", err)
	}
	config := `[team]
pm_tool = "linear"

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
		if ev.Data["action"] == string(linearwriteback.ActionFailureAttention) {
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
