package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	budgetcalc "github.com/agent-team-project/agent-team/internal/budget"
	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/topology"
	"github.com/agent-team-project/agent-team/internal/usage"
)

func TestBudgetStatusCommandReportsConfiguredTeam(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	appendBudgetFixture(t, teamDir, `
[budgets.delivery]
tokens_per_day = 200
jobs_in_flight = 2
`)
	now := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	used := writeUsageJobForTest(t, teamDir, "SQU-103", usage.Record{
		Instance:        "worker-squ-103",
		TokensAvailable: true,
		InputTokens:     100,
		OutputTokens:    10,
		StartedAt:       now.Add(-time.Hour),
		EndedAt:         now,
	})
	used.Origin = origin.Envelope{Team: "delivery"}
	used.Usage.Records[0].Origin = used.Origin
	if err := job.Write(teamDir, used); err != nil {
		t.Fatalf("job.Write used: %v", err)
	}
	running, err := job.New("SQU-104", "worker", "running", now)
	if err != nil {
		t.Fatalf("job.New running: %v", err)
	}
	running.Status = job.StatusRunning
	running.Origin = origin.Envelope{Team: "delivery"}
	if err := job.Write(teamDir, running); err != nil {
		t.Fatalf("job.Write running: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"budget", "status", "--target", tmp, "--team", "delivery", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("budget status: %v\nstderr=%s", err, stderr.String())
	}
	var rows []budgetcalc.TeamStatus
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode rows: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Team != "delivery" || rows[0].TokensUsed != 110 || rows[0].TokensPerDay != 200 || rows[0].JobsInFlight != 1 || rows[0].JobsInFlightCap != 2 {
		t.Fatalf("row = %+v", rows[0])
	}
}

func TestBudgetStatusNoBudgetsIsNoop(t *testing.T) {
	tmp := t.TempDir()
	initCmd := NewRootCmd()
	initCmd.SetOut(&bytes.Buffer{})
	initCmd.SetErr(&bytes.Buffer{})
	initCmd.SetArgs([]string{"init", "--target", tmp, "--no-input"})
	if err := initCmd.Execute(); err != nil {
		t.Fatalf("slim init: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"budget", "status", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("budget status: %v\nstderr=%s", err, stderr.String())
	}
	if got := strings.TrimSpace(out.String()); got != "(no budgets configured)" {
		t.Fatalf("text output = %q", got)
	}

	jsonCmd := NewRootCmd()
	jsonOut, jsonErr := &bytes.Buffer{}, &bytes.Buffer{}
	jsonCmd.SetOut(jsonOut)
	jsonCmd.SetErr(jsonErr)
	jsonCmd.SetArgs([]string{"budget", "status", "--target", tmp, "--json"})
	if err := jsonCmd.Execute(); err != nil {
		t.Fatalf("budget status json: %v\nstderr=%s", err, jsonErr.String())
	}
	if got := strings.TrimSpace(jsonOut.String()); got != "[]" {
		t.Fatalf("json output = %q", got)
	}
}

func TestBudgetStatusCommandRendersAllocatedColumn(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	appendBudgetFixture(t, teamDir, `
[budgets.delivery]
tokens_per_day = 100
allocation = "oversubscribe"
`)
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		t.Fatalf("LoadFromTeamDir: %v", err)
	}
	if _, err := budgetcalc.GrantTokens(teamDir, top, budgetcalc.GrantRequest{
		Team:     "delivery",
		JobID:    "squ-104",
		Instance: "worker-squ-104",
		Tokens:   125,
		Now:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("GrantTokens: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"budget", "status", "--target", tmp, "--team", "delivery"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("budget status: %v\nstderr=%s", err, stderr.String())
	}
	text := out.String()
	if !strings.Contains(text, "ALLOCATED") || !strings.Contains(text, "125") {
		t.Fatalf("text output = %q, want allocated column with 125", text)
	}
}

func TestBudgetStatusCommandReportsJobAllowanceFromLiveCodexLog(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j, err := job.New("SQU-104", "worker", "running", now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.Status = job.StatusRunning
	j.TokenBudget = 100
	j.TimeBudget = "10m"
	j.ReminderLevels = []int{50, 80, 100}
	j.TokenBudgetNotices = []int{50}
	j.Instance = "worker-squ-104"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	logPath := filepath.Join(tmp, "codex.jsonl")
	if err := os.WriteFile(logPath, []byte(`{"type":"turn.completed","usage":{"input_tokens":70,"output_tokens":15}}`+"\n"), 0o644); err != nil {
		t.Fatalf("write codex log: %v", err)
	}
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:  "worker-squ-104",
		Agent:     "worker",
		Job:       "squ-104",
		Runtime:   "codex",
		Workspace: tmp,
		Status:    daemon.StatusRunning,
		StartedAt: now.Add(-2 * time.Minute),
		LogPath:   logPath,
	}); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"budget", "status", "--target", tmp, "--job", "squ-104", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("budget status job: %v\nstderr=%s", err, stderr.String())
	}
	var row jobBudgetStatus
	if err := json.Unmarshal(out.Bytes(), &row); err != nil {
		t.Fatalf("decode row: %v\nbody=%s", err, out.String())
	}
	if row.JobID != "squ-104" || row.Instance != "worker-squ-104" || row.Runtime != "codex" {
		t.Fatalf("row identity = %+v", row)
	}
	if !row.TokensAvailable || row.TokensUsed != 85 || row.TokenBudget != 100 || row.TokensRemaining != 15 {
		t.Fatalf("row token budget = %+v", row)
	}
	if row.TimeBudget != "10m0s" || row.TimeElapsed == "" || row.TimeRemaining == "" {
		t.Fatalf("row time budget = %+v", row)
	}
	if len(row.TokenNoticeLevels) != 1 || row.TokenNoticeLevels[0] != 50 {
		t.Fatalf("row notices = %+v", row.TokenNoticeLevels)
	}
}

func appendBudgetFixture(t *testing.T, teamDir, body string) {
	t.Helper()
	path := filepath.Join(teamDir, "instances.toml")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open instances.toml: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString("\n" + body); err != nil {
		t.Fatalf("append instances.toml: %v", err)
	}
}
