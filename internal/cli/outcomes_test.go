package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/outcomes"
)

func TestOutcomesReportCommandRendersJSONAndTable(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	config := `[pm]
provider = "none"

[outcomes.epic_allocations]
resource-governance = "200"
`
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(config), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	if err := outcomes.WriteRecord(teamDir, &outcomes.Record{
		Version:     1,
		JobID:       "squ-135",
		Ticket:      "SQU-135",
		Epic:        "resource-governance",
		Status:      "done",
		Week:        "2026-W28",
		Team:        "delivery",
		Agent:       "worker",
		CreatedAt:   now.Add(-2 * time.Hour),
		FinalizedAt: now,
		WorkUnits: []outcomes.WorkUnitRecord{{
			ID:         "implement",
			Target:     "worker",
			StartedAt:  now.Add(-2 * time.Hour),
			FinishedAt: now.Add(-time.Hour),
		}},
		ReviewRounds:     2,
		BounceCount:      1,
		TokenBudget:      200,
		TokensConsumed:   150,
		TimeToMergeMS:    int64((2 * time.Hour).Milliseconds()),
		TimeToTerminalMS: int64((2 * time.Hour).Milliseconds()),
		RecordedAt:       now,
	}); err != nil {
		t.Fatalf("WriteRecord: %v", err)
	}

	jsonCmd := NewRootCmd()
	jsonOut, jsonErr := &bytes.Buffer{}, &bytes.Buffer{}
	jsonCmd.SetOut(jsonOut)
	jsonCmd.SetErr(jsonErr)
	jsonCmd.SetArgs([]string{"outcomes", "report", "--target", tmp, "--json"})
	if err := jsonCmd.Execute(); err != nil {
		t.Fatalf("outcomes report json: %v\nstderr=%s", err, jsonErr.String())
	}
	var report outcomes.Report
	if err := json.Unmarshal(jsonOut.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\nbody=%s", err, jsonOut.String())
	}
	if len(report.Rows) != 1 || report.Rows[0].Jobs != 1 || report.Rows[0].AverageBounces != 1 {
		t.Fatalf("report = %+v", report)
	}
	if report.Rows[0].EffectiveConcurrency != 1 || report.Rows[0].PeakConcurrentWorkUnits != 1 || report.Rows[0].DeclaredReplicaCapacity != 4 {
		t.Fatalf("report concurrency = %+v", report.Rows[0])
	}

	tableCmd := NewRootCmd()
	tableOut, tableErr := &bytes.Buffer{}, &bytes.Buffer{}
	tableCmd.SetOut(tableOut)
	tableCmd.SetErr(tableErr)
	tableCmd.SetArgs([]string{"outcomes", "report", "--target", tmp})
	if err := tableCmd.Execute(); err != nil {
		t.Fatalf("outcomes report table: %v\nstderr=%s", err, tableErr.String())
	}
	text := tableOut.String()
	if !strings.Contains(text, "EFF_CONC") || !strings.Contains(text, "CAPACITY") || !strings.Contains(text, "2026-W28") || !strings.Contains(text, "150/200") {
		t.Fatalf("table output = %q", text)
	}

	epicCmd := NewRootCmd()
	epicOut, epicErr := &bytes.Buffer{}, &bytes.Buffer{}
	epicCmd.SetOut(epicOut)
	epicCmd.SetErr(epicErr)
	epicCmd.SetArgs([]string{"outcomes", "report", "--target", tmp, "--by-epic"})
	if err := epicCmd.Execute(); err != nil {
		t.Fatalf("outcomes report by epic: %v\nstderr=%s", err, epicErr.String())
	}
	epicText := epicOut.String()
	if !strings.Contains(epicText, "EPIC_ALLOC") || !strings.Contains(epicText, "resource-governance") || !strings.Contains(epicText, "150/200") {
		t.Fatalf("by-epic table output = %q", epicText)
	}
}
