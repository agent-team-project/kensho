package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	budgetcalc "github.com/jamesaud/agent-team/internal/budget"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/origin"
	"github.com/jamesaud/agent-team/internal/usage"
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
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
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
	initInto(t, tmp)

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
