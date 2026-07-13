package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/archive"
	budgetcalc "github.com/agent-team-project/agent-team/internal/budget"
	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/topology"
)

func TestExtendCommandUpdatesRuntimeMetadataAndRenderers(t *testing.T) {
	env := newAttachTestEnv(t)
	meta, err := env.dmn.Manager().Dispatch(daemon.DispatchInput{
		Agent: "worker", Name: "worker-squ-69", Job: "squ-69", Workspace: env.target,
		Budget: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	t.Cleanup(func() { stopAndWaitForTest(t, env.dmn.Manager(), "worker-squ-69") })
	now := time.Now().UTC()
	if err := job.Write(env.teamDir, &job.Job{
		ID:        "squ-69",
		Ticket:    "SQU-69",
		Target:    "worker",
		Instance:  "worker-squ-69",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"extend", "worker-squ-69", "--by", "5s", "--repo", env.target, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("extend: %v\nstderr=%s", err, stderr.String())
	}
	var row extendCommandResult
	if err := json.Unmarshal(out.Bytes(), &row); err != nil {
		t.Fatalf("decode extend json: %v\nbody=%s", err, out.String())
	}
	if row.Instance != "worker-squ-69" || row.By != "5s" || row.RuntimeBudget != "15s" || row.Status != string(daemon.StatusRunning) {
		t.Fatalf("extend row = %+v", row)
	}
	if row.PreviousDeadline != meta.RuntimeDeadline.Format(time.RFC3339) || row.NewDeadline == "" || row.RuntimeRemaining == "" {
		t.Fatalf("extend deadlines/remaining = %+v, previous meta deadline %s", row, meta.RuntimeDeadline)
	}

	var psBuf bytes.Buffer
	if err := runPsJSON(&psBuf, env.teamDir, time.Now().UTC()); err != nil {
		t.Fatalf("run ps json: %v", err)
	}
	var rows []psJSONRow
	if err := json.Unmarshal(psBuf.Bytes(), &rows); err != nil {
		t.Fatalf("decode ps json: %v\nbody=%s", err, psBuf.String())
	}
	if len(rows) != 1 || rows[0].RuntimeBudget != "15s" || rows[0].RuntimeRemaining == "" {
		t.Fatalf("ps rows = %+v, want extended runtime budget/remaining", rows)
	}

	inspect := NewRootCmd()
	inspectOut, inspectErr := &bytes.Buffer{}, &bytes.Buffer{}
	inspect.SetOut(inspectOut)
	inspect.SetErr(inspectErr)
	inspect.SetArgs([]string{"inspect", "worker-squ-69", "--repo", env.target, "--json"})
	if err := inspect.Execute(); err != nil {
		t.Fatalf("inspect: %v\nstderr=%s", err, inspectErr.String())
	}
	var body inspectJSON
	if err := json.Unmarshal(inspectOut.Bytes(), &body); err != nil {
		t.Fatalf("decode inspect json: %v\nbody=%s", err, inspectOut.String())
	}
	if body.Runtime == nil || body.Runtime.RuntimeBudget != "15s" || body.Runtime.RuntimeRemaining == "" {
		t.Fatalf("inspect runtime = %+v, want extended budget/remaining", body.Runtime)
	}
	events, err := job.ListEvents(env.teamDir, "squ-69")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 || events[0].Type != "extended" || events[0].Actor != "cli" || events[0].Data["amount"] != "5s" {
		t.Fatalf("events = %+v, want top-level extend audit", events)
	}
}

func TestJobExtendRecordsAuditEvent(t *testing.T) {
	env := newAttachTestEnv(t)
	if _, err := env.dmn.Manager().Dispatch(daemon.DispatchInput{
		Agent: "worker", Name: "worker-squ-69", Job: "squ-69", Workspace: env.target,
		Budget: 10 * time.Second,
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	t.Cleanup(func() { stopAndWaitForTest(t, env.dmn.Manager(), "worker-squ-69") })

	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-69",
		Ticket:    "SQU-69",
		Target:    "worker",
		Instance:  "worker-squ-69",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(env.teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "extend", "squ-69", "--by", "5s", "--actor", "ops", "--repo", filepath.Dir(env.teamDir), "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job extend: %v\nstderr=%s", err, stderr.String())
	}
	var result jobExtendResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode job extend json: %v\nbody=%s", err, out.String())
	}
	if result.Job == nil || result.Job.LastEvent != "extended" || result.Job.LastStatus != "extended worker-squ-69 by 5s" {
		t.Fatalf("job result = %+v", result.Job)
	}
	if result.Extension.RuntimeBudget != "15s" {
		t.Fatalf("extension = %+v, want 15s budget", result.Extension)
	}

	events, err := job.ListEvents(env.teamDir, "squ-69")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v, want one audit event", events)
	}
	ev := events[0]
	if ev.Type != "extended" || ev.Actor != "ops" || ev.Message != "extended worker-squ-69 by 5s" {
		t.Fatalf("event = %+v, want extended audit", ev)
	}
	if ev.Data["amount"] != "5s" || ev.Data["instance"] != "worker-squ-69" || ev.Data["new_deadline"] == "" {
		t.Fatalf("event data = %+v, want amount/instance/deadline", ev.Data)
	}
}

func TestJobExtendAddsTokenAllowanceWithoutRuntimeExtension(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-104",
		Ticket:    "SQU-104",
		Target:    "worker",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{
				ID:                 "implement",
				Target:             "worker",
				Status:             job.StatusRunning,
				TokenBudget:        100,
				TokenBudgetNotices: []int{50, 80},
			},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "extend", "squ-104", "--step", "implement", "--tokens", "50", "--actor", "ops", "--repo", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job extend tokens: %v\nstderr=%s", err, stderr.String())
	}
	var result jobExtendResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode token extend json: %v\nbody=%s", err, out.String())
	}
	if result.TokensAdded != 50 || result.TokenBudget != 150 || result.StepID != "implement" {
		t.Fatalf("token result = %+v", result)
	}
	if result.Job == nil || len(result.Job.Steps) != 1 || result.Job.Steps[0].TokenBudget != 150 || len(result.Job.Steps[0].TokenBudgetNotices) != 0 {
		t.Fatalf("updated job = %+v", result.Job)
	}
	events, err := job.ListEvents(teamDir, "squ-104")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 || events[0].Type != "budget_extended" || events[0].Actor != "ops" || events[0].Data["tokens_added"] != "50" || events[0].Data["token_budget"] != "150" {
		t.Fatalf("events = %+v", events)
	}
}

func TestJobExtendTokensIsolatesUnrelatedUnknownKindRecord(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	appendBudgetFixture(t, teamDir, `
[budgets.delivery]
tokens_per_day = 1000
allocation = "reserve"
`)
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	target := &job.Job{
		ID:        "squ-374-target",
		Ticket:    "SQU-374-TARGET",
		Target:    "worker",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		Origin:    origin.Envelope{Team: "delivery"},
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{{
			ID:          "implement",
			Target:      "worker",
			Status:      job.StatusRunning,
			Instance:    "worker-squ-374-target",
			TokenBudget: 100,
		}},
	}
	if err := job.Write(teamDir, target); err != nil {
		t.Fatalf("write target job: %v", err)
	}

	stalePath := filepath.Join(job.Directory(teamDir), "stale-report.toml")
	staleBody := []byte(`id = "stale-report"
ticket = "STALE-REPORT"
target = "worker"
kind = "unknown-profile-report"
status = "done"
created_at = 2026-06-01T00:00:00Z
updated_at = 2026-06-01T00:00:00Z
`)
	if err := os.WriteFile(stalePath, staleBody, 0o644); err != nil {
		t.Fatalf("write unrelated stale report: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "extend", target.ID, "--step", "implement", "--tokens", "50", "--actor", "ops", "--repo", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job extend tokens: %v\nstderr=%s", err, stderr.String())
	}

	persisted, err := job.Read(teamDir, target.ID)
	if err != nil {
		t.Fatalf("read target job: %v", err)
	}
	if len(persisted.Steps) != 1 || persisted.Steps[0].TokenBudget != 150 {
		t.Fatalf("target job = %+v, want only target token budget increased to 150", persisted)
	}
	targetEvents, err := job.ListEvents(teamDir, target.ID)
	if err != nil {
		t.Fatalf("list target events: %v", err)
	}
	if len(targetEvents) != 1 || targetEvents[0].Type != "budget_extended" || targetEvents[0].Actor != "ops" || targetEvents[0].Data["tokens_added"] != "50" || targetEvents[0].Data["token_budget"] != "150" {
		t.Fatalf("target events = %+v, want one auditable token extension", targetEvents)
	}
	if got, err := os.ReadFile(stalePath); err != nil {
		t.Fatalf("read unrelated stale report: %v", err)
	} else if !bytes.Equal(got, staleBody) {
		t.Fatalf("unrelated stale report changed:\n%s", got)
	}
	staleEvents, err := job.ListEvents(teamDir, "stale-report")
	if err != nil {
		t.Fatalf("list unrelated events: %v", err)
	}
	if len(staleEvents) != 0 {
		t.Fatalf("unrelated events = %+v, want none", staleEvents)
	}
	allocations, err := budgetcalc.ListAllocations(teamDir)
	if err != nil {
		t.Fatalf("list allocations: %v", err)
	}
	if len(allocations) != 1 || allocations[0].JobID != target.ID || allocations[0].StepID != "implement" || allocations[0].Tokens != 50 {
		t.Fatalf("allocations = %+v, want only target extension allocation", allocations)
	}
	for _, want := range []string{"stale-report.toml", `unknown job kind "unknown-profile-report"`, "agent-team job doctor --json"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want diagnostic containing %q", stderr.String(), want)
		}
	}
}

func TestJobExtendTokensIsolatesUnrelatedInvalidArchivedRecords(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	appendBudgetFixture(t, teamDir, `
[budgets.delivery]
tokens_per_day = 1000
allocation = "reserve"
`)
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	target := &job.Job{
		ID:        "squ-374-target",
		Ticket:    "SQU-374-TARGET",
		Target:    "worker",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		Origin:    origin.Envelope{Team: "delivery"},
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{{
			ID:          "implement",
			Target:      "worker",
			Status:      job.StatusRunning,
			Instance:    "worker-squ-374-target",
			TokenBudget: 100,
		}},
	}
	if err := job.Write(teamDir, target); err != nil {
		t.Fatalf("write target job: %v", err)
	}

	archivePath, err := archive.AppendJSON(teamDir, now.Add(-24*time.Hour), map[string]any{
		"type":        "job",
		"archived_at": now.Add(-23 * time.Hour),
		"id":          "archived-stale-report",
		"terminal_at": now.Add(-24 * time.Hour),
		"job": &job.Job{
			ID:        "archived-stale-report",
			Ticket:    "ARCHIVED-STALE-REPORT",
			Target:    "worker",
			Kind:      "unknown-profile-report",
			Status:    job.StatusDone,
			CreatedAt: now.Add(-48 * time.Hour),
			UpdatedAt: now.Add(-24 * time.Hour),
		},
	})
	if err != nil {
		t.Fatalf("append invalid archived job: %v", err)
	}
	f, err := os.OpenFile(archivePath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	if _, err := f.WriteString("not-json\n"); err != nil {
		_ = f.Close()
		t.Fatalf("append malformed archive line: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}
	archiveBefore, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read archive before extension: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "extend", target.ID, "--step", "implement", "--tokens", "50", "--actor", "ops", "--repo", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job extend tokens: %v\nstderr=%s", err, stderr.String())
	}

	persisted, err := job.Read(teamDir, target.ID)
	if err != nil {
		t.Fatalf("read target job: %v", err)
	}
	if len(persisted.Steps) != 1 || persisted.Steps[0].TokenBudget != 150 {
		t.Fatalf("target job = %+v, want only target token budget increased to 150", persisted)
	}
	targetEvents, err := job.ListEvents(teamDir, target.ID)
	if err != nil {
		t.Fatalf("list target events: %v", err)
	}
	if len(targetEvents) != 1 || targetEvents[0].Type != "budget_extended" || targetEvents[0].Actor != "ops" || targetEvents[0].Data["tokens_added"] != "50" || targetEvents[0].Data["token_budget"] != "150" {
		t.Fatalf("target events = %+v, want one auditable token extension", targetEvents)
	}
	if archiveAfter, err := os.ReadFile(archivePath); err != nil {
		t.Fatalf("read archive after extension: %v", err)
	} else if !bytes.Equal(archiveAfter, archiveBefore) {
		t.Fatalf("unrelated archive changed:\n%s", archiveAfter)
	}
	if _, err := os.Stat(job.EventPath(teamDir, "archived-stale-report")); !os.IsNotExist(err) {
		t.Fatalf("unrelated live event path err = %v, want not exist", err)
	}
	allocations, err := budgetcalc.ListAllocations(teamDir)
	if err != nil {
		t.Fatalf("list allocations: %v", err)
	}
	if len(allocations) != 1 || allocations[0].JobID != target.ID || allocations[0].StepID != "implement" || allocations[0].Tokens != 50 {
		t.Fatalf("allocations = %+v, want only target extension allocation", allocations)
	}
	for _, want := range []string{filepath.Base(archivePath), "line 1", "archived-stale-report", `unknown job kind "unknown-profile-report"`, "line 2", "invalid character", "agent-team job doctor --json"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want diagnostic containing %q", stderr.String(), want)
		}
	}
}

func TestJobExtendTokensRejectsPartiallyDecodedTargetArchive(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	appendBudgetFixture(t, teamDir, `
[budgets.delivery]
tokens_per_day = 1000
allocation = "reserve"
`)
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	target := &job.Job{
		ID:        "squ-374-target",
		Ticket:    "SQU-374-TARGET",
		Target:    "worker",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		Origin:    origin.Envelope{Team: "delivery"},
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{{
			ID:          "implement",
			Target:      "worker",
			Status:      job.StatusRunning,
			Instance:    "worker-squ-374-target",
			TokenBudget: 100,
		}},
	}
	if err := job.Write(teamDir, target); err != nil {
		t.Fatalf("write target job: %v", err)
	}
	if _, err := archive.AppendJSON(teamDir, now, map[string]any{
		"type":        "job",
		"archived_at": now,
		"id":          target.ID,
		"terminal_at": now,
		"job":         "malformed-target-job",
	}); err != nil {
		t.Fatalf("append malformed target archive: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "extend", target.ID, "--step", "implement", "--tokens", "50", "--actor", "ops", "--repo", tmp, "--json"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("job extend unexpectedly succeeded: stdout=%s stderr=%s", out.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "cannot unmarshal string") {
		t.Fatalf("stderr = %q, want malformed target archive error", stderr.String())
	}

	persisted, err := job.Read(teamDir, target.ID)
	if err != nil {
		t.Fatalf("read target job: %v", err)
	}
	if len(persisted.Steps) != 1 || persisted.Steps[0].TokenBudget != 100 {
		t.Fatalf("target job = %+v, want token budget unchanged at 100", persisted)
	}
	if _, err := os.Stat(job.EventPath(teamDir, target.ID)); !os.IsNotExist(err) {
		t.Fatalf("target event path err = %v, want no audit event", err)
	}
	allocations, err := budgetcalc.ListAllocations(teamDir)
	if err != nil {
		t.Fatalf("list allocations: %v", err)
	}
	if len(allocations) != 0 {
		t.Fatalf("allocations = %+v, want none", allocations)
	}
}

func TestJobExtendQuietSuppressesIsolationWarning(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	appendBudgetFixture(t, teamDir, `
[budgets.delivery]
tokens_per_day = 1000
allocation = "reserve"
`)
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	target := &job.Job{
		ID:        "squ-374-quiet-target",
		Ticket:    "SQU-374-QUIET-TARGET",
		Target:    "worker",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		Origin:    origin.Envelope{Team: "delivery"},
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{{
			ID:          "implement",
			Target:      "worker",
			Status:      job.StatusRunning,
			Instance:    "worker-squ-374-quiet-target",
			TokenBudget: 100,
		}},
	}
	if err := job.Write(teamDir, target); err != nil {
		t.Fatalf("write target job: %v", err)
	}
	if err := os.WriteFile(filepath.Join(job.Directory(teamDir), "stale-report.toml"), []byte(`id = "stale-report"
ticket = "STALE-REPORT"
target = "worker"
kind = "unknown-profile-report"
status = "done"
created_at = 2026-06-01T00:00:00Z
updated_at = 2026-06-01T00:00:00Z
`), 0o644); err != nil {
		t.Fatalf("write unrelated stale report: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "extend", target.ID, "--step", "implement", "--tokens", "50", "--actor", "ops", "--repo", tmp, "--quiet"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job extend tokens: %v\nstderr=%s", err, stderr.String())
	}
	if out.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("quiet output: stdout=%q stderr=%q, want both empty", out.String(), stderr.String())
	}
	persisted, err := job.Read(teamDir, target.ID)
	if err != nil {
		t.Fatalf("read target job: %v", err)
	}
	if len(persisted.Steps) != 1 || persisted.Steps[0].TokenBudget != 150 {
		t.Fatalf("target job = %+v, want successful token extension", persisted)
	}
	events, err := job.ListEvents(teamDir, target.ID)
	if err != nil {
		t.Fatalf("list target events: %v", err)
	}
	if len(events) != 1 || events[0].Type != "budget_extended" {
		t.Fatalf("target events = %+v, want one auditable token extension", events)
	}
}

func TestJobExtendTokensReserveModeRequiresHeadroom(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	appendBudgetFixture(t, teamDir, `
[budgets.delivery]
tokens_per_day = 100
allocation = "reserve"
`)
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		t.Fatalf("LoadFromTeamDir: %v", err)
	}
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-106",
		Ticket:    "SQU-106",
		Target:    "worker",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		Origin:    origin.Envelope{Team: "delivery"},
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{
				ID:          "implement",
				Target:      "worker",
				Status:      job.StatusRunning,
				Instance:    "worker-squ-106",
				TokenBudget: 80,
			},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	if _, err := budgetcalc.GrantTokens(teamDir, top, budgetcalc.GrantRequest{
		Team:     "delivery",
		JobID:    "squ-106",
		StepID:   "implement",
		Instance: "worker-squ-106",
		Tokens:   80,
		Now:      now,
		Origin:   j.Origin,
	}); err != nil {
		t.Fatalf("GrantTokens: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "extend", "squ-106", "--step", "implement", "--tokens", "30", "--actor", "ops", "--repo", tmp, "--json"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("job extend unexpectedly succeeded; out=%s stderr=%s", out.String(), stderr.String())
	}
	persisted, err := job.Read(teamDir, "squ-106")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if persisted.Steps[0].TokenBudget != 80 {
		t.Fatalf("token budget = %d, want unchanged 80", persisted.Steps[0].TokenBudget)
	}
}
