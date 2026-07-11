package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

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
