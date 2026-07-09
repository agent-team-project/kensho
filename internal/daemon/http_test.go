package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/agent-team-project/agent-team/internal/feedback"
	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/outcomes"
	"github.com/agent-team-project/agent-team/internal/resource"
)

func TestHTTP_Dispatch_StopList(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)

	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	// POST /v1/dispatch
	if err := AppendMessage(root, "w-1", &Message{ID: "direct-mail", From: "manager", Body: "do not append"}); err != nil {
		t.Fatalf("append mailbox: %v", err)
	}
	body := `{"agent":"worker","name":"w-1","prompt":"hi","workspace":"` + t.TempDir() + `"}`
	resp := mustPost(t, srv.URL+"/v1/dispatch", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dispatch status: got %d, body=%s", resp.StatusCode, readBody(t, resp))
	}
	var dispBody struct {
		InstanceID string    `json:"instance_id"`
		StartedAt  time.Time `json:"started_at"`
		PID        int       `json:"pid"`
		SessionID  string    `json:"session_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&dispBody); err != nil {
		t.Fatalf("dispatch body: %v", err)
	}
	if dispBody.InstanceID != "w-1" {
		t.Errorf("instance_id: got %s", dispBody.InstanceID)
	}
	if dispBody.PID == 0 || dispBody.SessionID == "" {
		t.Errorf("missing pid/session: %+v", dispBody)
	}
	if prompt, ok := argValue(fake.lastCall(), "-p"); !ok || prompt != "hi" {
		t.Fatalf("direct dispatch prompt = %q, %v; want caller prompt only in %#v", prompt, ok, fake.lastCall())
	}
	unread, err := ReadUnacked(root, "w-1")
	if err != nil {
		t.Fatalf("ReadUnacked: %v", err)
	}
	if len(unread) != 1 || unread[0].Body != "do not append" {
		t.Fatalf("direct dispatch should not advance mailbox, got %+v", unread)
	}

	// GET /v1/instances
	resp = mustGet(t, srv.URL+"/v1/instances")
	var list []*Metadata
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("instances body: %v", err)
	}
	if len(list) != 1 || list[0].Instance != "w-1" {
		t.Errorf("instances: got %+v", list)
	}
	if list[0].Status != StatusRunning {
		t.Errorf("status: got %s want running", list[0].Status)
	}

	// POST /v1/stop
	resp = mustPost(t, srv.URL+"/v1/stop", `{"instance":"w-1"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stop status: got %d, body=%s", resp.StatusCode, readBody(t, resp))
	}
	waitForStatusNot(t, m, "w-1", StatusRunning)

	resp = mustPost(t, srv.URL+"/v1/stop", `{"instance":"w-1","timeout_ms":-1}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("negative timeout status: got %d, body=%s", resp.StatusCode, readBody(t, resp))
	}
}

func TestHTTP_JobsList(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	writeProjectConfig(t, teamDir, "dep")
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	j, err := jobstore.New("SQU-144", "worker", "kickoff", now)
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	j.Status = jobstore.StatusRunning
	j.Instance = "worker-squ-144"
	j.Epic = "agent-team-project/kensho#153"
	j.Pipeline = "ticket_to_pr"
	j.TokenBudget = 40_000_000
	j.TimeBudget = "45m0s"
	j.HardBudget = true
	j.HardMultiplier = 1.25
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	m := NewInstanceManager(root, nil)
	srv := httptest.NewServer(Handler(m, nil, nil, teamDir))
	defer srv.Close()

	resp := mustGet(t, srv.URL+"/v1/jobs")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("jobs status: got %d, body=%s", resp.StatusCode, readBody(t, resp))
	}
	var jobs []struct {
		ID                  string          `json:"id"`
		URI                 string          `json:"uri"`
		DeploymentURI       string          `json:"deployment_uri"`
		DeploymentParentURI string          `json:"deployment_parent_uri"`
		Epic                string          `json:"epic"`
		Instance            string          `json:"instance"`
		InstanceURI         string          `json:"instance_uri"`
		WorkspaceURI        string          `json:"workspace_uri"`
		Pipeline            string          `json:"pipeline"`
		Status              jobstore.Status `json:"status"`
		TokenBudget         int64           `json:"token_budget"`
		TimeBudget          string          `json:"time_budget"`
		Hard                bool            `json:"hard"`
		HardMultiplier      float64         `json:"hard_multiplier"`
		Kickoff             string          `json:"kickoff"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
		t.Fatalf("decode jobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != "squ-144" || jobs[0].Instance != "worker-squ-144" || jobs[0].Status != jobstore.StatusRunning {
		t.Fatalf("jobs = %+v", jobs)
	}
	if jobs[0].URI != resource.JobURI("dep", "squ-144") || jobs[0].DeploymentURI != resource.DeploymentURI("dep") || jobs[0].InstanceURI != resource.InstanceURI("dep", "worker-squ-144") || jobs[0].WorkspaceURI == "" {
		t.Fatalf("job resource fields = %+v", jobs[0])
	}
	if jobs[0].Epic != "agent-team-project/kensho#153" || jobs[0].Pipeline != "ticket_to_pr" || jobs[0].TokenBudget != 40_000_000 || jobs[0].TimeBudget != "45m0s" || !jobs[0].Hard || jobs[0].HardMultiplier != 1.25 {
		t.Fatalf("job budget fields = %+v", jobs[0])
	}
	if jobs[0].Kickoff != "" {
		t.Fatalf("jobs response leaked kickoff text: %+v", jobs[0])
	}

	resp = mustPost(t, srv.URL+"/v1/jobs", `{}`)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("post jobs status: got %d, body=%s", resp.StatusCode, readBody(t, resp))
	}
}

func TestHTTP_ResourceReadJobAndStep(t *testing.T) {
	teamDir := fixtureTeamDir(t)
	writeProjectConfig(t, teamDir, "dep")
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	j, err := jobstore.New("SQU-124", "worker", "build resource reads", now)
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	j.Steps = []jobstore.Step{{
		ID:     "implement",
		Target: "worker",
		Status: jobstore.StatusRunning,
	}}
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	if err := outcomes.WriteRecord(teamDir, &outcomes.Record{
		Version:       1,
		JobID:         "squ-124",
		Status:        "done",
		Runtime:       "codex",
		Model:         "gpt-5",
		Tier:          "T1",
		BounceClasses: map[string]int{"capability": 2},
		StepRuns: []outcomes.StepRunRecord{{
			ID:      "implement",
			Target:  "worker",
			Runtime: "codex",
			Model:   "gpt-5",
			Tier:    "T1",
		}},
		RecordedAt: now,
	}); err != nil {
		t.Fatalf("write outcome: %v", err)
	}
	m := NewInstanceManager(DaemonRoot(teamDir), nil)
	srv := httptest.NewServer(Handler(m, nil, nil, teamDir))
	defer srv.Close()

	resp := mustGet(t, srv.URL+"/v1/jobs")
	var jobs []struct {
		ID         string `json:"id"`
		OutcomeURI string `json:"outcome_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
		t.Fatalf("decode jobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != "squ-124" || jobs[0].OutcomeURI != resource.OutcomeURI("dep", "squ-124") {
		t.Fatalf("jobs outcome URI = %+v", jobs)
	}

	resp = mustGet(t, srv.URL+"/v1/resources?uri="+url.QueryEscape(resource.JobURI("dep", "squ-124")))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("job read status: got %d, body=%s", resp.StatusCode, readBody(t, resp))
	}
	var jobBody struct {
		URI  string         `json:"uri"`
		Kind string         `json:"kind"`
		ID   string         `json:"id"`
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jobBody); err != nil {
		t.Fatalf("decode job read: %v", err)
	}
	if jobBody.URI != resource.JobURI("dep", "squ-124") || jobBody.Kind != resource.KindJob || jobBody.ID != "squ-124" || jobBody.Data["id"] != "squ-124" {
		t.Fatalf("job resource = %+v", jobBody)
	}
	if jobBody.Data["kickoff"] != "build resource reads" {
		t.Fatalf("job data missing full read-through payload: %+v", jobBody.Data)
	}

	resp = mustGet(t, srv.URL+"/v1/resources?uri="+url.QueryEscape(resource.StepURI("dep", "squ-124", "implement")))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("step read status: got %d, body=%s", resp.StatusCode, readBody(t, resp))
	}
	var stepBody struct {
		Fragment string         `json:"fragment"`
		Data     map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&stepBody); err != nil {
		t.Fatalf("decode step read: %v", err)
	}
	if stepBody.Fragment != "step=implement" || stepBody.Data["id"] != "implement" || stepBody.Data["target"] != "worker" {
		t.Fatalf("step resource = %+v", stepBody)
	}

	resp = mustGet(t, srv.URL+"/v1/resources?uri="+url.QueryEscape(resource.OutcomeURI("dep", "squ-124")))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("outcome read status: got %d, body=%s", resp.StatusCode, readBody(t, resp))
	}
	var outcomeBody struct {
		URI  string `json:"uri"`
		Kind string `json:"kind"`
		ID   string `json:"id"`
		Data struct {
			JobID         string         `json:"job_id"`
			Model         string         `json:"model"`
			Tier          string         `json:"tier"`
			BounceClasses map[string]int `json:"bounce_classes"`
			StepRuns      []struct {
				ID    string `json:"id"`
				Model string `json:"model"`
				Tier  string `json:"tier"`
			} `json:"step_runs"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&outcomeBody); err != nil {
		t.Fatalf("decode outcome read: %v", err)
	}
	if outcomeBody.URI != resource.OutcomeURI("dep", "squ-124") || outcomeBody.Kind != resource.KindOutcome || outcomeBody.ID != "squ-124" {
		t.Fatalf("outcome resource identity = %+v", outcomeBody)
	}
	if outcomeBody.Data.JobID != "squ-124" || outcomeBody.Data.Model != "gpt-5" || outcomeBody.Data.Tier != "T1" || outcomeBody.Data.BounceClasses["capability"] != 2 || len(outcomeBody.Data.StepRuns) != 1 {
		t.Fatalf("outcome resource data = %+v", outcomeBody.Data)
	}
}

func TestHTTP_ResourceReadInstanceAndErrors(t *testing.T) {
	teamDir := fixtureTeamDir(t)
	writeProjectConfig(t, teamDir, "dep")
	daemonRoot := DaemonRoot(teamDir)
	if err := WriteMetadata(daemonRoot, &Metadata{
		Instance:  "worker-squ-124",
		Agent:     "worker",
		Workspace: t.TempDir(),
		Status:    StatusRunning,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	m := NewInstanceManager(daemonRoot, nil)
	if err := m.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	srv := httptest.NewServer(Handler(m, nil, nil, teamDir))
	defer srv.Close()

	resp := mustGet(t, srv.URL+"/v1/resources?uri="+url.QueryEscape(resource.InstanceURI("dep", "worker-squ-124")))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("instance read status: got %d, body=%s", resp.StatusCode, readBody(t, resp))
	}
	var body struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode instance read: %v", err)
	}
	if body.Data["instance"] != "worker-squ-124" || body.Data["uri"] != resource.InstanceURI("dep", "worker-squ-124") {
		t.Fatalf("instance resource = %+v", body.Data)
	}

	resp = mustGet(t, srv.URL+"/v1/resources?uri="+url.QueryEscape(resource.InstanceURI("other", "worker-squ-124")))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("wrong deployment status: got %d, body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = mustGet(t, srv.URL+"/v1/resources")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing uri status: got %d, body=%s", resp.StatusCode, readBody(t, resp))
	}
}

func writeProjectConfig(t *testing.T, teamDir, id string) {
	t.Helper()
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatalf("mkdir team dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte("[project]\nid = \""+id+"\"\n"), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}
}

func TestHTTP_TopologyIncludesTeamsBudgetsAndStepBudgets(t *testing.T) {
	teamDir := fixtureTeamDir(t)
	top := mustParseCustomTopo(t, `
[instances.worker]
agent = "worker"
ephemeral = true

[instances.reviewer]
agent = "reviewer"
ephemeral = true

[pipelines.ticket_to_pr]
trigger.event = "ticket.status_changed"
auto_advance = true

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
workspace = "worktree"
timeout = "45m"
token_budget = "40M"
time_budget = "45m"
hard = true
hard_multiplier = 1.25
reminder_levels = [50, 80]
max_attempts = 1

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "reviewer"
after = ["implement"]
optional = true

[schedules.nightly]
every = "1h"
run_on_start = true
scope = "team"

[schedules.nightly.payload]
kind = "self_exam"

[teams.delivery]
description = "Delivery team"
instances = ["worker", "reviewer"]
pipelines = ["ticket_to_pr"]
schedules = ["nightly"]

[budgets]
reminder_levels = [25, 75, 100]

[budgets.delivery]
tokens_per_day = 200_000_000
jobs_in_flight = 4
allocation = "reserve"
load_weight = 2.5
`)
	m := NewInstanceManager(t.TempDir(), nil)
	resolver := NewEventResolver(m, teamDir, top)
	lastSeen := time.Date(2026, 7, 7, 13, 0, 0, 0, time.UTC)
	lastFired := time.Date(2026, 7, 7, 12, 30, 0, 0, time.UTC)
	if err := WriteScheduleState(m.daemonRoot, &ScheduleState{Name: "team.delivery.nightly", LastSeenAt: lastSeen, LastFiredAt: lastFired}); err != nil {
		t.Fatalf("write schedule state: %v", err)
	}
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustGet(t, srv.URL+"/v1/topology")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("topology status: got %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var body struct {
		Pipelines []struct {
			Name        string `json:"name"`
			AutoAdvance bool   `json:"auto_advance"`
			Steps       []struct {
				ID             string  `json:"id"`
				TokenBudget    int64   `json:"token_budget"`
				TimeBudget     string  `json:"time_budget"`
				Hard           bool    `json:"hard"`
				HardMultiplier float64 `json:"hard_multiplier"`
				ReminderLevels []int   `json:"reminder_levels"`
				MaxAttempts    int     `json:"max_attempts"`
			} `json:"steps"`
		} `json:"pipelines"`
		Teams []struct {
			Name      string   `json:"name"`
			Pipelines []string `json:"pipelines"`
			Schedules []string `json:"schedules"`
		} `json:"teams"`
		Budgets []struct {
			Team         string  `json:"team"`
			TokensPerDay int64   `json:"tokens_per_day"`
			JobsInFlight int     `json:"jobs_in_flight"`
			Allocation   string  `json:"allocation"`
			LoadWeight   float64 `json:"load_weight"`
		} `json:"budgets"`
		Schedules []struct {
			Name        string    `json:"name"`
			StateName   string    `json:"state_name"`
			Every       string    `json:"every"`
			RunOnStart  bool      `json:"run_on_start"`
			Team        string    `json:"team"`
			LastSeenAt  time.Time `json:"last_seen_at"`
			LastFiredAt time.Time `json:"last_fired_at"`
		} `json:"schedules"`
		BudgetReminderLevels []int `json:"budget_reminder_levels"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode topology: %v", err)
	}
	if len(body.Pipelines) != 1 || body.Pipelines[0].Name != "ticket_to_pr" || !body.Pipelines[0].AutoAdvance {
		t.Fatalf("pipelines = %+v", body.Pipelines)
	}
	if len(body.Pipelines[0].Steps) != 2 {
		t.Fatalf("pipeline steps = %+v", body.Pipelines[0].Steps)
	}
	step := body.Pipelines[0].Steps[0]
	if step.ID != "implement" || step.TokenBudget != 40_000_000 || step.TimeBudget != "45m0s" || !step.Hard || step.HardMultiplier != 1.25 || step.MaxAttempts != 1 {
		t.Fatalf("implement step = %+v", step)
	}
	if len(step.ReminderLevels) != 2 || step.ReminderLevels[0] != 50 || step.ReminderLevels[1] != 80 {
		t.Fatalf("reminder levels = %+v", step.ReminderLevels)
	}
	if len(body.Teams) != 1 || body.Teams[0].Name != "delivery" || len(body.Teams[0].Pipelines) != 1 || body.Teams[0].Pipelines[0] != "ticket_to_pr" || len(body.Teams[0].Schedules) != 1 || body.Teams[0].Schedules[0] != "nightly" {
		t.Fatalf("teams = %+v", body.Teams)
	}
	if len(body.Budgets) != 1 || body.Budgets[0].Team != "delivery" || body.Budgets[0].TokensPerDay != 200_000_000 || body.Budgets[0].JobsInFlight != 4 || body.Budgets[0].Allocation != "reserve" || body.Budgets[0].LoadWeight != 2.5 {
		t.Fatalf("budgets = %+v", body.Budgets)
	}
	if len(body.Schedules) != 1 || body.Schedules[0].Name != "nightly" || body.Schedules[0].StateName != "team.delivery.nightly" || body.Schedules[0].Every != "1h0m0s" || !body.Schedules[0].RunOnStart || body.Schedules[0].Team != "delivery" {
		t.Fatalf("schedules = %+v", body.Schedules)
	}
	if !body.Schedules[0].LastSeenAt.Equal(lastSeen) || !body.Schedules[0].LastFiredAt.Equal(lastFired) {
		t.Fatalf("schedule clock = %+v, want seen=%s fired=%s", body.Schedules[0], lastSeen, lastFired)
	}
	if len(body.BudgetReminderLevels) != 3 || body.BudgetReminderLevels[0] != 25 || body.BudgetReminderLevels[2] != 100 {
		t.Fatalf("budget reminder levels = %+v", body.BudgetReminderLevels)
	}
}

func TestHTTP_ExtendRuntimeBudget(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	meta, err := m.Dispatch(DispatchInput{
		Agent: "worker", Name: "w-extend", Workspace: t.TempDir(),
		Budget: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	resp := mustPost(t, srv.URL+"/v1/extend", `{"instance":"w-extend","by_ms":500,"actor":"ops"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("extend status: got %d, body=%s", resp.StatusCode, readBody(t, resp))
	}
	var body struct {
		InstanceID       string    `json:"instance_id"`
		ByMillis         int64     `json:"by_ms"`
		PreviousDeadline time.Time `json:"previous_deadline"`
		NewDeadline      time.Time `json:"new_deadline"`
		Actor            string    `json:"actor"`
		Metadata         Metadata  `json:"metadata"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode extend: %v", err)
	}
	if body.InstanceID != "w-extend" || body.ByMillis != 500 || body.Actor != "ops" {
		t.Fatalf("extend body = %+v", body)
	}
	if !body.PreviousDeadline.Equal(meta.RuntimeDeadline) || !body.NewDeadline.Equal(meta.RuntimeDeadline.Add(500*time.Millisecond)) {
		t.Fatalf("deadlines = %s -> %s, want %s -> %s", body.PreviousDeadline, body.NewDeadline, meta.RuntimeDeadline, meta.RuntimeDeadline.Add(500*time.Millisecond))
	}
	if body.Metadata.RuntimeBudget != "2.5s" || !body.Metadata.RuntimeDeadline.Equal(body.NewDeadline) {
		t.Fatalf("metadata = %+v, want extended budget/deadline", body.Metadata)
	}

	resp = mustPost(t, srv.URL+"/v1/extend", `{"instance":"w-extend","by_ms":0}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("zero extend status: got %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	mustPost(t, srv.URL+"/v1/stop", `{"instance":"w-extend","force":true,"timeout_ms":25}`)
	waitForStatusNot(t, m, "w-extend", StatusRunning)
}

func TestHTTP_DispatchValidation(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, newFakeSpawner(time.Second).spawn)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	cases := []struct {
		name string
		body string
	}{
		{"missing agent", `{"name":"x","workspace":"/tmp"}`},
		{"missing name", `{"agent":"w","workspace":"/tmp"}`},
		{"missing workspace", `{"agent":"w","name":"x"}`},
		{"bad json", `{not-json}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := mustPost(t, srv.URL+"/v1/dispatch", c.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status: got %d want 400 for %s", resp.StatusCode, c.name)
			}
		})
	}

	// Unknown fields are tolerated on the wire: a newer CLI's additive field
	// must not brick an older daemon (SQU-55).
	t.Run("unknown field tolerated", func(t *testing.T) {
		resp := mustPost(t, srv.URL+"/v1/dispatch", `{"agent":"w","name":"x-tolerant","workspace":"/tmp","extra":1}`)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status: got %d want 200, body=%s", resp.StatusCode, readBody(t, resp))
		}
	})
}

func TestHTTP_DispatchPassesStdin(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	body := `{"agent":"worker","name":"w-stdin","workspace":"` + t.TempDir() + `","runtime":"codex","runtime_binary":"codex","args":["exec","-"],"stdin":"hello via http"}`
	resp := mustPost(t, srv.URL+"/v1/dispatch", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dispatch status: got %d, body=%s", resp.StatusCode, readBody(t, resp))
	}
	if got := fake.lastStdin(); got != "hello via http" {
		t.Fatalf("stdin = %q, want request body stdin", got)
	}
	mustPost(t, srv.URL+"/v1/stop", `{"instance":"w-stdin"}`)
	waitForStatusNot(t, m, "w-stdin", StatusRunning)
}

func TestHTTP_StartResumesSession(t *testing.T) {
	claudeConfigDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfigDir)
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	workspace := t.TempDir()
	resp := mustPost(t, srv.URL+"/v1/dispatch",
		`{"agent":"manager","name":"mgr","workspace":"`+workspace+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dispatch: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var disp struct {
		SessionID string `json:"session_id"`
	}
	json.NewDecoder(resp.Body).Decode(&disp)
	writeClaudeSession(t, claudeConfigDir, workspace, disp.SessionID)

	// Stop and wait for finalisation.
	mustPost(t, srv.URL+"/v1/stop", `{"instance":"mgr"}`)
	waitForStatusNot(t, m, "mgr", StatusRunning)

	// Start.
	resp = mustPost(t, srv.URL+"/v1/start", `{"instance":"mgr"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("start: %d %s", resp.StatusCode, readBody(t, resp))
	}

	args := fake.lastCall()
	foundResume := false
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--resume" && args[i+1] == disp.SessionID {
			foundResume = true
		}
	}
	if !foundResume {
		t.Errorf("expected --resume %s, got: %v", disp.SessionID, args)
	}

	mustPost(t, srv.URL+"/v1/stop", `{"instance":"mgr"}`)
	waitForStatusNot(t, m, "mgr", StatusRunning)
}

func TestHTTP_StartFreshBypassesResume(t *testing.T) {
	t.Setenv("AGENT_TEAM_RUNTIME", "claude")
	teamDir := fixtureTeamDir(t)
	writeFixtureAgent(t, teamDir, "manager")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.mgr]
agent = "manager"
runtime = "claude"
description = "Recoverable Claude manager."
`), 0o644); err != nil {
		t.Fatal(err)
	}
	root := DaemonRoot(teamDir)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	now := time.Now().UTC()
	if err := WriteMetadata(root, &Metadata{
		Instance:      "mgr",
		Agent:         "manager",
		Runtime:       "claude",
		RuntimeBinary: "claude",
		Workspace:     t.TempDir(),
		PID:           123,
		SessionID:     "resume-session",
		StartedAt:     now,
		StoppedAt:     now,
		Status:        StatusStopped,
	}); err != nil {
		t.Fatal(err)
	}

	resp := mustPost(t, srv.URL+"/v1/start", `{"instance":"mgr","fresh":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("start fresh: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var body struct {
		SessionResumed bool `json:"session_resumed"`
		FreshFallback  bool `json:"fresh_fallback"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode start fresh response: %v", err)
	}
	if body.SessionResumed || !body.FreshFallback {
		t.Fatalf("start fresh response = %+v, want fresh fallback without resume", body)
	}
	args := fake.lastCall()
	if containsString(args, "--resume") {
		t.Fatalf("fresh start should not use --resume: %v", args)
	}
	promptFile := filepath.Join(teamDir, "state", "mgr", "runtime", "system_prompt.md")
	if got, ok := argValue(args, "--append-system-prompt-file"); !ok || filepath.Clean(got) != filepath.Clean(promptFile) {
		t.Fatalf("fresh prompt arg = %q, %v; want %s in args %v", got, ok, promptFile, args)
	}

	mustPost(t, srv.URL+"/v1/stop", `{"instance":"mgr"}`)
	waitForStatusNot(t, m, "mgr", StatusRunning)
}

func TestHTTP_StartFreshForceRestartsRunning(t *testing.T) {
	t.Setenv("AGENT_TEAM_RUNTIME", "claude")
	teamDir := fixtureTeamDir(t)
	writeFixtureAgent(t, teamDir, "manager")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.mgr]
agent = "manager"
runtime = "claude"
description = "Recoverable Claude manager."
`), 0o644); err != nil {
		t.Fatal(err)
	}
	root := DaemonRoot(teamDir)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	initial, err := m.Dispatch(DispatchInput{
		Agent:         "manager",
		Name:          "mgr",
		Workspace:     t.TempDir(),
		Runtime:       "claude",
		RuntimeBinary: "claude",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	resp := mustPost(t, srv.URL+"/v1/start", `{"instance":"mgr","fresh":true,"force":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("forced start fresh: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var body struct {
		SessionResumed bool `json:"session_resumed"`
		FreshFallback  bool `json:"fresh_fallback"`
		PID            int  `json:"pid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode forced start fresh response: %v", err)
	}
	if body.SessionResumed || !body.FreshFallback || body.PID == initial.PID {
		t.Fatalf("forced start fresh response = %+v, initial pid=%d", body, initial.PID)
	}
	if got := fake.callCount(); got != 2 {
		t.Fatalf("spawn calls after forced fresh = %d, want 2", got)
	}
	args := fake.lastCall()
	if containsString(args, "--resume") {
		t.Fatalf("forced fresh should not use --resume: %v", args)
	}
	promptFile := filepath.Join(teamDir, "state", "mgr", "runtime", "system_prompt.md")
	if got, ok := argValue(args, "--append-system-prompt-file"); !ok || filepath.Clean(got) != filepath.Clean(promptFile) {
		t.Fatalf("forced fresh prompt arg = %q, %v; want %s in args %v", got, ok, promptFile, args)
	}

	mustPost(t, srv.URL+"/v1/stop", `{"instance":"mgr"}`)
	waitForStatusNot(t, m, "mgr", StatusRunning)
}

func TestHTTP_RestartResumesSession(t *testing.T) {
	claudeConfigDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfigDir)
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	workspace := t.TempDir()
	resp := mustPost(t, srv.URL+"/v1/dispatch",
		`{"agent":"manager","name":"mgr","workspace":"`+workspace+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dispatch: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var disp struct {
		SessionID string `json:"session_id"`
	}
	json.NewDecoder(resp.Body).Decode(&disp)
	writeClaudeSession(t, claudeConfigDir, workspace, disp.SessionID)

	resp = mustPost(t, srv.URL+"/v1/restart", `{"instance":"mgr","timeout_ms":10000}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("restart: %d %s", resp.StatusCode, readBody(t, resp))
	}
	args := fake.lastCall()
	foundResume := false
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--resume" && args[i+1] == disp.SessionID {
			foundResume = true
		}
	}
	if !foundResume {
		t.Errorf("expected --resume %s, got: %v", disp.SessionID, args)
	}

	resp = mustPost(t, srv.URL+"/v1/restart", `{"instance":"mgr","timeout_ms":-1}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("negative restart timeout: got %d body=%s", resp.StatusCode, readBody(t, resp))
	}

	mustPost(t, srv.URL+"/v1/stop", `{"instance":"mgr"}`)
	waitForStatusNot(t, m, "mgr", StatusRunning)
}

func TestHTTP_InterruptResumesSession(t *testing.T) {
	claudeConfigDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfigDir)
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	workspace := t.TempDir()
	resp := mustPost(t, srv.URL+"/v1/dispatch",
		`{"agent":"manager","name":"mgr","workspace":"`+workspace+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dispatch: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var disp struct {
		SessionID string `json:"session_id"`
	}
	json.NewDecoder(resp.Body).Decode(&disp)
	writeClaudeSession(t, claudeConfigDir, workspace, disp.SessionID)

	resp = mustPost(t, srv.URL+"/v1/interrupt", `{"to":"mgr","from":"ops","body":"hard steer"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("interrupt: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var out struct {
		Delivered   bool   `json:"delivered"`
		Interrupted bool   `json:"interrupted"`
		ID          string `json:"id"`
		SessionID   string `json:"session_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode interrupt: %v", err)
	}
	if !out.Delivered || !out.Interrupted || out.ID == "" || out.SessionID != disp.SessionID {
		t.Fatalf("interrupt response = %+v, want delivered interrupted same session %s", out, disp.SessionID)
	}
	args := fake.lastCall()
	foundResume := false
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--resume" && args[i+1] == disp.SessionID {
			foundResume = true
		}
	}
	if !foundResume {
		t.Errorf("expected --resume %s, got: %v", disp.SessionID, args)
	}
	resumePrompt, ok := argValue(args, "-p")
	if !ok {
		t.Fatalf("resume args missing -p mailbox prompt: %v", args)
	}
	for _, want := range []string{kickoffMailboxHeading, "From: ops", "hard steer"} {
		if !strings.Contains(resumePrompt, want) {
			t.Fatalf("resume prompt missing %q: %q", want, resumePrompt)
		}
	}
	messages, err := ReadUnacked(root, "mgr")
	if err != nil {
		t.Fatalf("read mailbox: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("mailbox = %+v, want interrupt message delivered to resume prompt", messages)
	}
	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if !lifecycleEventsContain(events, "interrupted", "mgr") {
		t.Fatalf("events missing interrupted: %+v", events)
	}

	mustPost(t, srv.URL+"/v1/stop", `{"instance":"mgr"}`)
	waitForStatusNot(t, m, "mgr", StatusRunning)
}

func TestHTTP_RemoveRequiresForceForRunning(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/dispatch",
		`{"agent":"manager","name":"mgr","workspace":"`+t.TempDir()+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dispatch: %d %s", resp.StatusCode, readBody(t, resp))
	}

	resp = mustPost(t, srv.URL+"/v1/remove", `{"instance":"mgr"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("remove running without force: got %d body=%s", resp.StatusCode, readBody(t, resp))
	}

	resp = mustPost(t, srv.URL+"/v1/remove", `{"instance":"mgr","force":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("force remove: %d %s", resp.StatusCode, readBody(t, resp))
	}
	listResp := mustGet(t, srv.URL+"/v1/instances")
	var list []*Metadata
	if err := json.NewDecoder(listResp.Body).Decode(&list); err != nil {
		t.Fatalf("instances body: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("instances after remove = %+v, want empty", list)
	}
}

func TestHTTP_MethodGuards(t *testing.T) {
	m := NewInstanceManager(t.TempDir(), newFakeSpawner(time.Second).spawn)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	// GET on a POST endpoint
	resp := mustGet(t, srv.URL+"/v1/dispatch")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("dispatch GET: got %d want 405", resp.StatusCode)
	}
	// POST on a GET endpoint
	resp = mustPost(t, srv.URL+"/v1/instances", `{}`)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("instances POST: got %d want 405", resp.StatusCode)
	}
	resp = mustGet(t, srv.URL+"/v1/reconcile")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("reconcile GET: got %d want 405", resp.StatusCode)
	}
}

func TestHTTP_InstancesEmptyArray(t *testing.T) {
	m := NewInstanceManager(t.TempDir(), nil)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()
	resp := mustGet(t, srv.URL+"/v1/instances")
	body := readBody(t, resp)
	if !strings.HasPrefix(strings.TrimSpace(body), "[") {
		t.Errorf("expected JSON array, got %q", body)
	}
}

func TestHTTP_StatusIncludesBuildIdentity(t *testing.T) {
	root := t.TempDir()
	teamDir := t.TempDir()
	build := buildinfo.Info{
		Version:  "0.1.0",
		Revision: "deadbeefcafebabefeedface1234567890abcdef",
		Time:     "2026-07-02T12:34:56Z",
	}
	m := NewInstanceManager(root, newFakeSpawner(time.Second).spawn)
	if _, err := m.Dispatch(DispatchInput{Agent: "manager", Name: "manager", Workspace: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = m.Stop("manager")
		waitForStatusNot(t, m, "manager", StatusRunning)
	}()
	srv := httptest.NewServer(Handler(m, nil, nil, teamDir, build))
	defer srv.Close()

	resp := mustGet(t, srv.URL+"/v1/status")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var body struct {
		Ready     bool           `json:"ready"`
		PID       int            `json:"pid"`
		Instances int            `json:"instances"`
		TeamDir   string         `json:"team_dir"`
		Build     buildinfo.Info `json:"build"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !body.Ready || body.PID == 0 || body.Instances != 1 || body.TeamDir != teamDir {
		t.Fatalf("status body = %+v", body)
	}
	if body.Build.Revision != build.Revision || body.Build.Time != build.Time || body.Build.Version != build.Version {
		t.Fatalf("status build = %+v, want %+v", body.Build, build)
	}

	resp = mustPost(t, srv.URL+"/v1/status", `{}`)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status POST: got %d body=%s", resp.StatusCode, readBody(t, resp))
	}
}

func TestHTTP_BuildHandshakeMatchedIdentityLogsNothing(t *testing.T) {
	root := t.TempDir()
	build := buildinfo.Info{
		Version:  "0.1.0",
		Revision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Time:     "2026-07-03T00:00:00Z",
	}
	logs := &bytes.Buffer{}
	m := NewInstanceManager(root, newFakeSpawner(time.Second).spawn)
	srv := httptest.NewServer(HandlerWithLog(m, nil, nil, "", logs, build))
	defer srv.Close()

	resp := mustGetWithBuild(t, srv.URL+"/v1/status", build)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	if logs.Len() != 0 {
		t.Fatalf("logs = %q, want no build skew warning", logs.String())
	}
}

func TestHTTP_BuildHandshakeSkewLogsOncePerClientIdentity(t *testing.T) {
	root := t.TempDir()
	daemonBuild := buildinfo.Info{
		Version:  "0.1.0",
		Revision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Time:     "2026-07-03T00:00:00Z",
	}
	clientBuild := buildinfo.Info{
		Version:  "0.1.0",
		Revision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Time:     "2026-07-03T00:05:00Z",
	}
	logs := &bytes.Buffer{}
	m := NewInstanceManager(root, newFakeSpawner(time.Second).spawn)
	srv := httptest.NewServer(HandlerWithLog(m, nil, nil, "", logs, daemonBuild))
	defer srv.Close()

	for i := 0; i < 3; i++ {
		resp := mustGetWithBuild(t, srv.URL+"/v1/status", clientBuild)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d: got %d body=%s", i, resp.StatusCode, readBody(t, resp))
		}
		resp.Body.Close()
	}
	if count := strings.Count(logs.String(), "daemon build skew"); count != 1 {
		t.Fatalf("skew warning count = %d, want 1; logs=%q", count, logs.String())
	}
	if !strings.Contains(logs.String(), clientBuild.Display()) || !strings.Contains(logs.String(), daemonBuild.Display()) {
		t.Fatalf("logs = %q, want client and daemon identities", logs.String())
	}

	otherClientBuild := buildinfo.Info{
		Version:  "0.1.0",
		Revision: "cccccccccccccccccccccccccccccccccccccccc",
		Time:     "2026-07-03T00:10:00Z",
	}
	resp := mustGetWithBuild(t, srv.URL+"/v1/status", otherClientBuild)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second client status: got %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	if count := strings.Count(logs.String(), "daemon build skew"); count != 2 {
		t.Fatalf("skew warning count after second identity = %d, want 2; logs=%q", count, logs.String())
	}
}

func TestHTTP_ErrorBodyIncludesDaemonBuild(t *testing.T) {
	root := t.TempDir()
	build := buildinfo.Info{
		Version:  "0.1.0",
		Revision: "dddddddddddddddddddddddddddddddddddddddd",
		Time:     "2026-07-03T00:15:00Z",
		Modified: true,
	}
	m := NewInstanceManager(root, newFakeSpawner(time.Second).spawn)
	srv := httptest.NewServer(HandlerWithLog(m, nil, nil, "", io.Discard, build))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event", `{}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("event status: got %d want 503", resp.StatusCode)
	}
	var body struct {
		Error       string         `json:"error"`
		DaemonBuild buildinfo.Info `json:"daemon_build"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error != "topology not configured" {
		t.Fatalf("error = %q, want topology not configured", body.Error)
	}
	if body.DaemonBuild != build {
		t.Fatalf("daemon_build = %+v, want %+v", body.DaemonBuild, build)
	}
}

func TestHTTP_OutboxDrain(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	now := time.Date(2026, 6, 27, 10, 30, 0, 0, time.UTC)
	if err := WriteOutboxItem(teamDir, &OutboxItem{
		ID:        "outbox-http",
		State:     OutboxStatePending,
		Type:      "agent.dispatch",
		Payload:   map[string]any{"target": "worker", "name": "worker-squ-402", "ticket": "SQU-402", "workspace": "repo"},
		Source:    "manager",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("WriteOutboxItem: %v", err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustGet(t, srv.URL+"/v1/outbox")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("outbox list: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var items []*OutboxItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatalf("outbox list decode: %v", err)
	}
	if len(items) != 1 || items[0].ID != "outbox-http" {
		t.Fatalf("outbox items = %+v, want outbox-http", items)
	}

	resp = mustPost(t, srv.URL+"/v1/outbox/drain?dry_run=true", `{}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("outbox dry drain: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var preview OutboxDrainResult
	if err := json.NewDecoder(resp.Body).Decode(&preview); err != nil {
		t.Fatalf("preview decode: %v", err)
	}
	if preview.WouldPublish != 1 || preview.Pending != 1 {
		t.Fatalf("preview = %+v, want would_publish=1 pending=1", preview)
	}
	if fake.callCount() != 0 {
		t.Fatalf("dry-run spawned %d processes", fake.callCount())
	}

	resp = mustPost(t, srv.URL+"/v1/outbox/drain", `{}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("outbox drain: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var result OutboxDrainResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("result decode: %v", err)
	}
	if result.Published != 1 || result.Pending != 0 || result.Processed != 1 {
		t.Fatalf("result = %+v, want published=1 pending=0 processed=1", result)
	}
	if fake.callCount() != 1 {
		t.Fatalf("spawn calls=%d, want 1", fake.callCount())
	}
}

func TestHTTP_ReconcileMarksDeadRunningProcessExited(t *testing.T) {
	root := t.TempDir()
	restorePIDLiveCheck := SetPidLiveCheckForTest(func(pid int) bool { return false })
	defer restorePIDLiveCheck()

	if err := WriteMetadata(root, &Metadata{
		Instance:  "orphan",
		Agent:     "manager",
		Status:    StatusRunning,
		PID:       999999,
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	m := NewInstanceManager(root, nil)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/reconcile", `{}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reconcile status: got %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var body reconcileResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode reconcile body: %v", err)
	}
	if !body.Reconciled || body.Changed != 1 {
		t.Fatalf("reconcile body = %+v, want one change", body)
	}
	if len(body.Instances) != 1 || body.Instances[0].Status != StatusExited {
		t.Fatalf("instances = %+v, want orphan exited", body.Instances)
	}
	if len(body.Changes) != 1 || body.Changes[0].Before != StatusRunning || body.Changes[0].After != StatusExited {
		t.Fatalf("changes = %+v, want running -> exited", body.Changes)
	}
	list := m.List()
	if len(list) != 1 || list[0].Status != StatusExited {
		t.Fatalf("manager list = %+v, want reconciled exited metadata", list)
	}
}

func TestHTTP_Message_AppendsToMailbox(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top := mustParseCustomTopo(t, `
[instances."worker-1"]
agent = "worker"
`)
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/message", `{"to":"worker-1","from":"manager","body":"hello"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var rb struct {
		Delivered bool   `json:"delivered"`
		ID        string `json:"id"`
		Note      string `json:"note"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rb); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !rb.Delivered {
		t.Errorf("delivered=false")
	}
	if rb.ID == "" {
		t.Errorf("missing id")
	}
	if rb.Note != MailboxDeclaredQueuedNote {
		t.Fatalf("note = %q, want declared queue note", rb.Note)
	}

	got, err := ReadMessages(root, "worker-1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("messages: got %d want 1", len(got))
	}
	if got[0].Body != "hello" || got[0].From != "manager" || got[0].To != "worker-1" {
		t.Errorf("message: %+v", got[0])
	}
}

func TestHTTP_DirectDispatchOTelDisabledStripsInheritedEnv(t *testing.T) {
	// SQU-74 round-3 finding: the /v1/dispatch path must derive the OTel
	// strip decision from the repo config; with [otel] enabled=false, stale
	// telemetry env inherited by the daemon must not reach the child.
	t.Setenv("CLAUDE_CODE_ENABLE_TELEMETRY", "1")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://stale")
	t.Setenv("TRACEPARENT", "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01")
	t.Setenv("AGENTTEAM_OTEL_HEADER_0", "stale-secret")

	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	writeFixtureOTelConfig(t, teamDir, false)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	srv := httptest.NewServer(Handler(m, nil, nil, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/dispatch",
		`{"agent":"worker","name":"direct-otel-disabled","workspace":"/tmp","prompt":"noop"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dispatch: %d %s", resp.StatusCode, readBody(t, resp))
	}
	t.Cleanup(func() {
		_, _ = m.Stop("direct-otel-disabled")
		_ = m.WaitForReaper("direct-otel-disabled", 5*time.Second)
	})
	env := fake.lastEnv()
	for _, forbidden := range []string{
		"CLAUDE_CODE_ENABLE_TELEMETRY=",
		"OTEL_EXPORTER_OTLP_ENDPOINT=",
		"TRACEPARENT=",
		"AGENTTEAM_OTEL_HEADER_",
	} {
		if containsEnvPrefix(env, forbidden) {
			t.Fatalf("direct dispatch with disabled otel leaked %q: %#v", forbidden, env)
		}
	}
}

func TestHTTP_Message_Validation(t *testing.T) {
	teamDir := fixtureTeamDir(t)
	top := mustParseCustomTopo(t, `
[instances.x]
agent = "worker"

[instances.manager]
agent = "manager"
`)
	m := NewInstanceManager(t.TempDir(), nil)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	cases := []struct {
		name string
		body string
	}{
		{"missing to", `{"body":"hi"}`},
		{"missing body", `{"to":"x"}`},
		{"bad json", `{not-json}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := mustPost(t, srv.URL+"/v1/message", c.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status: %d want 400", resp.StatusCode)
			}
		})
	}

	// Unknown fields are tolerated on the wire (SQU-55).
	t.Run("unknown field tolerated", func(t *testing.T) {
		resp := mustPost(t, srv.URL+"/v1/message", `{"to":"x","body":"y","foo":1}`)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status: got %d want 200, body=%s", resp.StatusCode, readBody(t, resp))
		}
	})

	t.Run("unknown undeclared target rejected with suggestion", func(t *testing.T) {
		resp := mustPost(t, srv.URL+"/v1/message", `{"to":"manger","body":"y"}`)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status: got %d want 400, body=%s", resp.StatusCode, readBody(t, resp))
		}
		if body := readBody(t, resp); !strings.Contains(body, `did you mean \"manager\"?`) {
			t.Fatalf("body = %s, want manager suggestion", body)
		}
	})
}

func TestHTTP_Message_MethodGuard(t *testing.T) {
	m := NewInstanceManager(t.TempDir(), nil)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()
	resp := mustGet(t, srv.URL+"/v1/message")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status: %d want 405", resp.StatusCode)
	}
}

func TestHTTP_AuthorityViolationAuditOnly(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	j, err := jobstore.New("SQU-92", "worker", "kickoff", now)
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	top := mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"

[authority.agents.manager]
allow = ["inbox.send"]
`)
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/message", bytes.NewReader([]byte(`{"from":"worker-squ-92","to":"manager","body":"hello"}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(origin.HeaderName, origin.HeaderValue(origin.Envelope{
		Team:     "platform",
		Agent:    "worker",
		Instance: "worker-squ-92",
		Job:      "squ-92",
	}))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("message: %d %s", resp.StatusCode, readBody(t, resp))
	}
	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("ListLifecycleEvents: %v", err)
	}
	if len(events) != 1 || events[0].Action != authorityViolationAction || events[0].Origin.Agent != "worker" {
		t.Fatalf("lifecycle events = %+v", events)
	}
	jobEvents, err := jobstore.ListEvents(teamDir, "squ-92")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(jobEvents) != 1 || jobEvents[0].Type != authorityViolationAction || jobEvents[0].Data["verb"] != "inbox.send" {
		t.Fatalf("job events = %+v", jobEvents)
	}
}

func TestHTTP_AuthorityEnforcementDeniesUnauthorizedRemove(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	if err := WriteMetadata(root, &Metadata{
		Instance:  "worker-squ-122",
		Agent:     "worker",
		Origin:    origin.Envelope{Team: "delivery", Agent: "worker", Instance: "worker-squ-122", Job: "SQU-122"},
		Workspace: t.TempDir(),
		StartedAt: now,
		Status:    StatusStopped,
	}); err != nil {
		t.Fatalf("WriteMetadata actor: %v", err)
	}
	if err := WriteMetadata(root, &Metadata{
		Instance:  "victim",
		Agent:     "worker",
		Workspace: t.TempDir(),
		StartedAt: now,
		Status:    StatusStopped,
	}); err != nil {
		t.Fatalf("WriteMetadata victim: %v", err)
	}
	top := mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"

[instances.worker]
agent = "worker"
ephemeral = true

[teams.delivery]
instances = ["manager", "worker"]

[authority]
enforcement = "enforce"

[authority.instances.manager]
allow = ["instance.remove"]

[authority.agents.worker]
allow = ["inbox.send"]
`)
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/remove", bytes.NewReader([]byte(`{"instance":"victim"}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(origin.HeaderName, origin.HeaderValue(origin.Envelope{Instance: "worker-squ-122", Agent: "manager", Team: "delivery", Job: "SQU-122"}))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("remove status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if _, err := ReadMetadata(root, "victim"); err != nil {
		t.Fatalf("victim metadata should remain: %v", err)
	}
	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("ListLifecycleEvents: %v", err)
	}
	if len(events) != 1 || events[0].Action != authorityViolationAction || events[0].Origin.Agent != "worker" {
		t.Fatalf("lifecycle events = %+v", events)
	}
	if !strings.Contains(events[0].Message, "verb=instance.remove") || !strings.Contains(events[0].Message, "allowlist_source=authority.agents.worker") {
		t.Fatalf("violation message = %q", events[0].Message)
	}
}

func TestHTTP_AuthorityEnforcementAllowsManagerRemove(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	if err := WriteMetadata(root, &Metadata{
		Instance:  "manager",
		Agent:     "manager",
		Origin:    origin.Envelope{Team: "delivery", Agent: "manager", Instance: "manager"},
		Workspace: t.TempDir(),
		StartedAt: now,
		Status:    StatusStopped,
	}); err != nil {
		t.Fatalf("WriteMetadata manager: %v", err)
	}
	if err := WriteMetadata(root, &Metadata{
		Instance:  "victim",
		Agent:     "worker",
		Workspace: t.TempDir(),
		StartedAt: now,
		Status:    StatusStopped,
	}); err != nil {
		t.Fatalf("WriteMetadata victim: %v", err)
	}
	top := mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"

[instances.worker]
agent = "worker"
ephemeral = true

[teams.delivery]
instances = ["manager", "worker"]

[authority]
enforcement = "enforce"

[authority.instances.manager]
allow = ["instance.remove"]
`)
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/remove", bytes.NewReader([]byte(`{"instance":"victim"}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(origin.HeaderName, origin.HeaderValue(origin.Envelope{Instance: "manager"}))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remove status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if _, err := ReadMetadata(root, "victim"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("victim metadata err = %v, want not exist", err)
	}
	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("ListLifecycleEvents: %v", err)
	}
	for _, ev := range events {
		if ev.Action == authorityViolationAction {
			t.Fatalf("unexpected authority violation: %+v", ev)
		}
	}
}

func TestHTTP_LoopbackOperatorTokenAllowsAuthorityEnforcedInboxSend(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	tokenPath, err := EnsureOperatorToken(teamDir)
	if err != nil {
		t.Fatalf("EnsureOperatorToken: %v", err)
	}
	token, err := ReadTokenFile(tokenPath)
	if err != nil {
		t.Fatalf("ReadTokenFile: %v", err)
	}
	top := mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"

[authority]
enforcement = "enforce"

[authority.instances.manager]
allow = ["*"]
`)
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, teamDir, top)
	handler := loopbackAuthHandler(Handler(m, nil, resolver, teamDir), teamDir, m, buildinfo.Current("test"))

	req, err := http.NewRequest(http.MethodPost, "http://daemon/v1/message", bytes.NewReader([]byte(`{"from":"(cli)","to":"manager","body":"hello from operator"}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req = req.WithContext(context.WithValue(req.Context(), daemonTransportContextKey{}, daemonTransportTCP))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("message status = %d body=%s", rr.Code, rr.Body.String())
	}
	messages, err := ReadMessages(root, "manager")
	if err != nil {
		t.Fatalf("ReadMessages: %v", err)
	}
	if len(messages) != 1 || messages[0].From != "(cli)" || messages[0].Body != "hello from operator" {
		t.Fatalf("messages = %+v", messages)
	}
	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("ListLifecycleEvents: %v", err)
	}
	for _, ev := range events {
		if ev.Action == authorityViolationAction {
			t.Fatalf("unexpected authority violation: %+v", ev)
		}
	}
}

func TestHTTP_UnidentifiedCLISenderDeniedByAuthorityEnforcement(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top := mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"

[authority]
enforcement = "enforce"

[authority.instances.manager]
allow = ["*"]
`)
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/message", bytes.NewReader([]byte(`{"from":"(cli)","to":"manager","body":"hello without identity"}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("message status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	messages, err := ReadMessages(root, "manager")
	if err != nil {
		t.Fatalf("ReadMessages: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("messages = %+v, want none", messages)
	}
	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("ListLifecycleEvents: %v", err)
	}
	if len(events) != 1 || events[0].Action != authorityViolationAction {
		t.Fatalf("lifecycle events = %+v", events)
	}
	if !strings.Contains(events[0].Message, "verb=inbox.send") || !strings.Contains(events[0].Message, "allowlist_source=none") {
		t.Fatalf("violation message = %q", events[0].Message)
	}
}

func TestHTTP_LoopbackTokenOriginFeedsAuthorityAudit(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	j, err := jobstore.New("SQU-130", "worker", "kickoff", now)
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	instance := "worker-squ-130"
	tokenPath, err := EnsureInstanceToken(teamDir, instance)
	if err != nil {
		t.Fatalf("EnsureInstanceToken: %v", err)
	}
	token, err := ReadTokenFile(tokenPath)
	if err != nil {
		t.Fatalf("ReadTokenFile: %v", err)
	}
	if err := WriteMetadata(root, &Metadata{
		Instance: instance,
		Agent:    "worker",
		Job:      j.ID,
		Origin: origin.Envelope{
			Team:     "delivery",
			Agent:    "worker",
			Instance: instance,
			Job:      j.ID,
		},
		Workspace: t.TempDir(),
		StartedAt: now,
		Status:    StatusRunning,
	}); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}
	top := mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"

[authority.agents.manager]
allow = ["daemon.reconcile"]
`)
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, teamDir, top)
	handler := loopbackAuthHandler(Handler(m, nil, resolver, teamDir), teamDir, m, buildinfo.Current("test"))

	req, err := http.NewRequest(http.MethodPost, "http://daemon/v1/reconcile", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req = req.WithContext(context.WithValue(req.Context(), daemonTransportContextKey{}, daemonTransportTCP))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("reconcile status = %d body=%s", rr.Code, rr.Body.String())
	}

	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("ListLifecycleEvents: %v", err)
	}
	if len(events) != 1 || events[0].Action != authorityViolationAction || events[0].Origin.Agent != "worker" || events[0].Origin.Instance != instance {
		t.Fatalf("lifecycle events = %+v", events)
	}
	jobEvents, err := jobstore.ListEvents(teamDir, j.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(jobEvents) != 1 || jobEvents[0].Type != authorityViolationAction || jobEvents[0].Data["verb"] != "daemon.reconcile" || jobEvents[0].Data["actor_job"] != j.ID {
		t.Fatalf("job events = %+v", jobEvents)
	}
}

func TestHTTP_LoopbackTokenOriginCannotBeWidenedByHeader(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	instance := "worker-squ-122"
	tokenPath, err := EnsureInstanceToken(teamDir, instance)
	if err != nil {
		t.Fatalf("EnsureInstanceToken: %v", err)
	}
	token, err := ReadTokenFile(tokenPath)
	if err != nil {
		t.Fatalf("ReadTokenFile: %v", err)
	}
	if err := WriteMetadata(root, &Metadata{
		Instance:  instance,
		Agent:     "worker",
		Origin:    origin.Envelope{Team: "delivery", Agent: "worker", Instance: instance, Job: "SQU-122"},
		Workspace: t.TempDir(),
		StartedAt: now,
		Status:    StatusStopped,
	}); err != nil {
		t.Fatalf("WriteMetadata actor: %v", err)
	}
	if err := WriteMetadata(root, &Metadata{
		Instance:  "victim",
		Agent:     "worker",
		Workspace: t.TempDir(),
		StartedAt: now,
		Status:    StatusStopped,
	}); err != nil {
		t.Fatalf("WriteMetadata victim: %v", err)
	}
	top := mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"

[instances.worker]
agent = "worker"
ephemeral = true

[teams.delivery]
instances = ["manager", "worker"]

[authority]
enforcement = "enforce"

[authority.instances.manager]
allow = ["instance.remove"]

[authority.agents.worker]
allow = ["inbox.send"]
`)
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, teamDir, top)
	handler := loopbackAuthHandler(Handler(m, nil, resolver, teamDir), teamDir, m, buildinfo.Current("test"))

	req, err := http.NewRequest(http.MethodPost, "http://daemon/v1/remove", bytes.NewReader([]byte(`{"instance":"victim"}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(origin.HeaderName, origin.HeaderValue(origin.Envelope{Instance: "manager", Agent: "manager", Team: "delivery"}))
	req = req.WithContext(context.WithValue(req.Context(), daemonTransportContextKey{}, daemonTransportTCP))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("remove status = %d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := ReadMetadata(root, "victim"); err != nil {
		t.Fatalf("victim metadata should remain: %v", err)
	}
	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("ListLifecycleEvents: %v", err)
	}
	if len(events) != 1 || events[0].Action != authorityViolationAction || events[0].Origin.Agent != "worker" || events[0].Origin.Instance != instance {
		t.Fatalf("lifecycle events = %+v", events)
	}
}

func TestHTTP_LoopbackTokenOriginCannotBeWidenedByHeaderWithoutMetadata(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	instance := "worker-squ-122"
	tokenPath, err := EnsureInstanceToken(teamDir, instance)
	if err != nil {
		t.Fatalf("EnsureInstanceToken: %v", err)
	}
	token, err := ReadTokenFile(tokenPath)
	if err != nil {
		t.Fatalf("ReadTokenFile: %v", err)
	}
	if err := WriteMetadata(root, &Metadata{
		Instance:  "victim",
		Agent:     "worker",
		Workspace: t.TempDir(),
		StartedAt: now,
		Status:    StatusStopped,
	}); err != nil {
		t.Fatalf("WriteMetadata victim: %v", err)
	}
	top := mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"

[instances.worker]
agent = "worker"
ephemeral = true

[teams.delivery]
instances = ["manager", "worker"]

[authority]
enforcement = "enforce"

[authority.agents.manager]
allow = ["instance.remove"]

[authority.agents.worker]
allow = ["inbox.send"]
`)
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, teamDir, top)
	handler := loopbackAuthHandler(Handler(m, nil, resolver, teamDir), teamDir, m, buildinfo.Current("test"))

	req, err := http.NewRequest(http.MethodPost, "http://daemon/v1/remove", bytes.NewReader([]byte(`{"instance":"victim"}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(origin.HeaderName, origin.HeaderValue(origin.Envelope{Agent: "manager", Team: "delivery"}))
	req = req.WithContext(context.WithValue(req.Context(), daemonTransportContextKey{}, daemonTransportTCP))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("remove status = %d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := ReadMetadata(root, "victim"); err != nil {
		t.Fatalf("victim metadata should remain: %v", err)
	}
	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("ListLifecycleEvents: %v", err)
	}
	if len(events) != 1 || events[0].Action != authorityViolationAction || events[0].Origin.Agent != "worker" || events[0].Origin.Instance != instance {
		t.Fatalf("lifecycle events = %+v", events)
	}
}

func TestHTTP_UIShellServedWithoutTokenDataGated(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	instance := "worker-ui"
	tokenPath, err := EnsureInstanceToken(teamDir, instance)
	if err != nil {
		t.Fatalf("EnsureInstanceToken: %v", err)
	}
	token, err := ReadTokenFile(tokenPath)
	if err != nil {
		t.Fatalf("ReadTokenFile: %v", err)
	}
	m := NewInstanceManager(root, nil)
	handler := loopbackAuthHandler(Handler(m, nil, nil, teamDir), teamDir, m, buildinfo.Current("test"))

	// The static UI shell loads WITHOUT a bearer token so a browser can reach the
	// token field; a token-gated shell is unreachable in a plain navigation.
	req := httptest.NewRequest(http.MethodGet, "http://daemon/ui/", nil)
	req = req.WithContext(context.WithValue(req.Context(), daemonTransportContextKey{}, daemonTransportTCP))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unauthenticated ui status = %d body=%s (shell must load without a token)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "agent-team-ui") {
		t.Fatalf("unauthenticated ui body missing root marker: %s", rr.Body.String())
	}
	// Data endpoints stay gated even though the shell is open.
	req = httptest.NewRequest(http.MethodGet, "http://daemon/v1/instances", nil)
	req = req.WithContext(context.WithValue(req.Context(), daemonTransportContextKey{}, daemonTransportTCP))
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /v1/instances status = %d, want 401 (data stays gated)", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "http://daemon/ui/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req = req.WithContext(context.WithValue(req.Context(), daemonTransportContextKey{}, daemonTransportTCP))
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("authenticated ui status = %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "agent-team-ui") {
		t.Fatalf("ui body missing root marker: %s", rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "http://daemon/ui/app.js", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req = req.WithContext(context.WithValue(req.Context(), daemonTransportContextKey{}, daemonTransportTCP))
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("authenticated app.js status = %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "/v1/instances") || !strings.Contains(rr.Body.String(), "/v1/jobs") || !strings.Contains(rr.Body.String(), "/v1/topology") {
		t.Fatalf("app.js does not call daemon read APIs: %s", rr.Body.String())
	}
}

func TestHTTP_FeedbackDeliverStoresOriginPingsIncidentAndAudits(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top := mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"

[authority.agents.worker]
allow = ["inbox.send"]
`)
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/feedback/deliver", bytes.NewReader([]byte(`{
		"body":"target daemon socket is unreachable",
		"category":"incident",
		"context":{"instance":"worker-squ-126","job":"squ-126","ticket":"SQU-126"}
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(origin.HeaderName, origin.HeaderValue(origin.Envelope{
		Project:  "source-project",
		Team:     "platform",
		Agent:    "worker",
		Instance: "worker-squ-126",
		Job:      "squ-126",
	}))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("feedback deliver: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var out struct {
		Delivered     bool   `json:"delivered"`
		ID            string `json:"id"`
		ManagerPinged bool   `json:"manager_pinged"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !out.Delivered || out.ID == "" || !out.ManagerPinged {
		t.Fatalf("response = %+v", out)
	}
	items, err := feedback.List(teamDir)
	if err != nil {
		t.Fatalf("feedback list: %v", err)
	}
	if len(items) != 1 || items[0].ID != out.ID || items[0].Category != feedback.CategoryIncident {
		t.Fatalf("items = %+v", items)
	}
	if items[0].Origin == nil || items[0].Origin.Project != "source-project" || items[0].Origin.Agent != "worker" {
		t.Fatalf("origin = %+v", items[0].Origin)
	}
	messages, err := ReadMessages(root, "manager")
	if err != nil {
		t.Fatalf("ReadMessages: %v", err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0].Body, out.ID) || !strings.Contains(messages[0].Body, "source-project") {
		t.Fatalf("manager messages = %+v", messages)
	}
	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("ListLifecycleEvents: %v", err)
	}
	if len(events) != 1 || events[0].Action != authorityViolationAction || events[0].Origin.Agent != "worker" {
		t.Fatalf("lifecycle events = %+v", events)
	}
}

func TestHTTP_Channel_PublishSubscribeDrainAck(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, nil)
	cs := NewChannelStore(root)
	srv := httptest.NewServer(Handler(m, cs, nil, ""))
	defer srv.Close()

	// Publish before any subscriber → message is on disk; subscriber comes
	// in after, gets cursor=1 (head), shouldn't see "first".
	resp := mustPost(t, srv.URL+"/v1/channel/%23room/publish",
		`{"sender":"manager","body":"first"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var pubResp struct {
		Seq int64 `json:"seq"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pubResp); err != nil {
		t.Fatalf("publish decode: %v", err)
	}
	if pubResp.Seq != 1 {
		t.Errorf("first seq: got %d", pubResp.Seq)
	}

	// Subscribe alice.
	resp = mustPost(t, srv.URL+"/v1/channel/%23room/subscribe", `{"instance":"alice"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("subscribe: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var subResp struct {
		Cursor     int64 `json:"cursor"`
		Subscribed bool  `json:"subscribed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&subResp); err != nil {
		t.Fatal(err)
	}
	if !subResp.Subscribed {
		t.Errorf("subscribed=false on first subscribe")
	}
	if subResp.Cursor != 1 {
		t.Errorf("cursor: got %d want 1", subResp.Cursor)
	}

	// Re-subscribe is idempotent.
	resp = mustPost(t, srv.URL+"/v1/channel/%23room/subscribe", `{"instance":"alice"}`)
	json.NewDecoder(resp.Body).Decode(&subResp)
	if subResp.Subscribed {
		t.Errorf("subscribed=true on re-subscribe")
	}

	// Drain immediately → empty (cursor at head).
	resp = mustGet(t, srv.URL+"/v1/channel/%23room/messages?instance=alice")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("drain: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var drainResp struct {
		Messages []ChannelMessage `json:"messages"`
		Cursor   int64            `json:"cursor"`
	}
	json.NewDecoder(resp.Body).Decode(&drainResp)
	if len(drainResp.Messages) != 0 {
		t.Errorf("immediate drain: got %d want 0", len(drainResp.Messages))
	}

	// Publish two more.
	mustPost(t, srv.URL+"/v1/channel/%23room/publish", `{"sender":"manager","body":"two"}`)
	mustPost(t, srv.URL+"/v1/channel/%23room/publish", `{"sender":"manager","body":"three"}`)

	resp = mustGet(t, srv.URL+"/v1/channel/%23room/messages?instance=alice")
	json.NewDecoder(resp.Body).Decode(&drainResp)
	if len(drainResp.Messages) != 2 {
		t.Errorf("post-publish drain: got %d want 2", len(drainResp.Messages))
	}
	if drainResp.Cursor != 3 {
		t.Errorf("cursor: got %d want 3", drainResp.Cursor)
	}

	// Ack and re-drain → empty.
	ackBody := `{"instance":"alice","cursor":` + jsonNumber(drainResp.Cursor) + `}`
	resp = mustPost(t, srv.URL+"/v1/channel/%23room/ack", ackBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ack: %d %s", resp.StatusCode, readBody(t, resp))
	}
	resp = mustGet(t, srv.URL+"/v1/channel/%23room/messages?instance=alice")
	json.NewDecoder(resp.Body).Decode(&drainResp)
	if len(drainResp.Messages) != 0 {
		t.Errorf("post-ack drain: got %d want 0", len(drainResp.Messages))
	}
}

func TestHTTP_Channel_TeamScopedDeclarationUsesScopedStorage(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top := mustParseCustomTopo(t, `
[channels.supervisor]
scope = "team"

[teams.platform]
channels = ["supervisor"]
`)
	m := NewInstanceManager(root, nil)
	cs := NewChannelStore(root)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, cs, resolver, teamDir))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/channel/%23supervisor/publish", bytes.NewReader([]byte(`{"sender":"manager","body":"scoped"}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(origin.HeaderName, origin.HeaderValue(origin.Envelope{Team: "platform", Agent: "manager", Instance: "manager"}))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish: %d %s", resp.StatusCode, readBody(t, resp))
	}
	scoped, err := readChannelMessagesSince(root, "#team-platform-supervisor", 0)
	if err != nil {
		t.Fatalf("read scoped channel: %v", err)
	}
	if len(scoped) != 1 || scoped[0].Body != "scoped" {
		t.Fatalf("scoped messages = %+v", scoped)
	}
	unscoped, err := readChannelMessagesSince(root, "#supervisor", 0)
	if err != nil {
		t.Fatalf("read unscoped channel: %v", err)
	}
	if len(unscoped) != 0 {
		t.Fatalf("unscoped messages = %+v, want none", unscoped)
	}
}

func TestHTTP_Channel_TeamScopedReadUsesOwningTeamWhenActorDiffers(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top := mustParseCustomTopo(t, `
[channels.supervisor]
scope = "team"

[teams.platform]
channels = ["supervisor"]
`)
	m := NewInstanceManager(root, nil)
	cs := NewChannelStore(root)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, cs, resolver, teamDir))
	defer srv.Close()

	if _, err := cs.Publish("#team-platform-supervisor", "manager", "owner message"); err != nil {
		t.Fatalf("seed owner channel: %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/channel/%23supervisor/messages?instance=quality-auditor&since=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(origin.HeaderName, origin.HeaderValue(origin.Envelope{Team: "quality", Agent: "auditor", Instance: "quality-auditor"}))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("messages: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var body struct {
		Messages []ChannelMessage `json:"messages"`
		Cursor   int64            `json:"cursor"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	if len(body.Messages) != 1 || body.Messages[0].Body != "owner message" {
		t.Fatalf("messages = %+v, want owner channel message", body.Messages)
	}
	actorScoped, err := readChannelMessagesSince(root, "#team-quality-supervisor", 0)
	if err != nil {
		t.Fatalf("read actor-scoped channel: %v", err)
	}
	if len(actorScoped) != 0 {
		t.Fatalf("actor-scoped messages = %+v, want none", actorScoped)
	}
	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("ListLifecycleEvents: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("read path wrote lifecycle events = %+v", events)
	}
}

func TestHTTP_Channel_TeamScopedWriteUsesOwningTeamAndAuditsActorTeam(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	j, err := jobstore.New("SQU-92", "worker", "kickoff", now)
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	top := mustParseCustomTopo(t, `
[channels.supervisor]
scope = "team"

[teams.platform]
channels = ["supervisor"]

[authority.agents.manager]
allow = ["channel.*"]
`)
	m := NewInstanceManager(root, nil)
	cs := NewChannelStore(root)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, cs, resolver, teamDir))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/channel/%23supervisor/publish", bytes.NewReader([]byte(`{"sender":"quality-auditor","body":"cross write"}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(origin.HeaderName, origin.HeaderValue(origin.Envelope{Team: "quality", Agent: "auditor", Instance: "quality-auditor", Job: j.ID}))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish: %d %s", resp.StatusCode, readBody(t, resp))
	}
	ownerScoped, err := readChannelMessagesSince(root, "#team-platform-supervisor", 0)
	if err != nil {
		t.Fatalf("read owner-scoped channel: %v", err)
	}
	if len(ownerScoped) != 1 || ownerScoped[0].Body != "cross write" {
		t.Fatalf("owner-scoped messages = %+v", ownerScoped)
	}
	actorScoped, err := readChannelMessagesSince(root, "#team-quality-supervisor", 0)
	if err != nil {
		t.Fatalf("read actor-scoped channel: %v", err)
	}
	if len(actorScoped) != 0 {
		t.Fatalf("actor-scoped messages = %+v, want none", actorScoped)
	}
	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("ListLifecycleEvents: %v", err)
	}
	if len(events) != 1 || events[0].Action != authorityViolationAction || events[0].Origin.Team != "quality" || events[0].Origin.Agent != "auditor" {
		t.Fatalf("lifecycle events = %+v", events)
	}
	jobEvents, err := jobstore.ListEvents(teamDir, j.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(jobEvents) != 1 || jobEvents[0].Type != authorityViolationAction || jobEvents[0].Data["verb"] != "channel.publish" || jobEvents[0].Data["resource"] != "channel:#team-platform-supervisor" {
		t.Fatalf("job events = %+v", jobEvents)
	}
}

func TestHTTP_Channel_DrainSinceParam(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, nil)
	cs := NewChannelStore(root)
	srv := httptest.NewServer(Handler(m, cs, nil, ""))
	defer srv.Close()

	mustPost(t, srv.URL+"/v1/channel/%23x/publish", `{"sender":"s","body":"a"}`)
	mustPost(t, srv.URL+"/v1/channel/%23x/publish", `{"sender":"s","body":"b"}`)
	mustPost(t, srv.URL+"/v1/channel/%23x/publish", `{"sender":"s","body":"c"}`)
	mustPost(t, srv.URL+"/v1/channel/%23x/subscribe", `{"instance":"bob"}`)

	// since=0 → all three.
	resp := mustGet(t, srv.URL+"/v1/channel/%23x/messages?instance=bob&since=0")
	var dr struct {
		Messages []ChannelMessage `json:"messages"`
	}
	json.NewDecoder(resp.Body).Decode(&dr)
	if len(dr.Messages) != 3 {
		t.Errorf("since=0: got %d want 3", len(dr.Messages))
	}

	// since=2 → only seq 3.
	resp = mustGet(t, srv.URL+"/v1/channel/%23x/messages?instance=bob&since=2")
	json.NewDecoder(resp.Body).Decode(&dr)
	if len(dr.Messages) != 1 || dr.Messages[0].Seq != 3 {
		t.Errorf("since=2: got %+v", dr.Messages)
	}
}

func TestHTTP_Channel_LongPollWait(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, nil)
	cs := NewChannelStore(root)
	srv := httptest.NewServer(Handler(m, cs, nil, ""))
	defer srv.Close()

	mustPost(t, srv.URL+"/v1/channel/%23live/subscribe", `{"instance":"alice"}`)

	// Issue a wait drain in a goroutine; publish from the main thread; expect
	// the goroutine to wake up before the deadline.
	type result struct {
		body string
		dur  time.Duration
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		resp, _ := http.Get(srv.URL + "/v1/channel/%23live/messages?instance=alice&wait=3s")
		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		resp.Body.Close()
		done <- result{body: buf.String(), dur: time.Since(start)}
	}()

	time.Sleep(100 * time.Millisecond)
	mustPost(t, srv.URL+"/v1/channel/%23live/publish", `{"sender":"x","body":"woke!"}`)

	select {
	case r := <-done:
		if r.dur > 2*time.Second {
			t.Errorf("waited too long: %s — should have woken on publish", r.dur)
		}
		if !strings.Contains(r.body, "woke!") {
			t.Errorf("body=%q", r.body)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("long-poll never returned")
	}
}

func TestHTTP_Channel_List(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, nil)
	cs := NewChannelStore(root)
	srv := httptest.NewServer(Handler(m, cs, nil, ""))
	defer srv.Close()

	mustPost(t, srv.URL+"/v1/channel/%23a/publish", `{"sender":"s","body":"x"}`)
	mustPost(t, srv.URL+"/v1/channel/%23b/subscribe", `{"instance":"alice"}`)

	resp := mustGet(t, srv.URL+"/v1/channels")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	var infos []ChannelInfo
	json.NewDecoder(resp.Body).Decode(&infos)
	if len(infos) != 2 {
		t.Fatalf("infos: got %d want 2 (%+v)", len(infos), infos)
	}
}

func TestHTTP_Channel_Delete(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, nil)
	cs := NewChannelStore(root)
	srv := httptest.NewServer(Handler(m, cs, nil, ""))
	defer srv.Close()

	mustPost(t, srv.URL+"/v1/channel/%23gone/publish", `{"sender":"s","body":"x"}`)

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/v1/channel/%23gone", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("delete: %d %s", resp.StatusCode, readBody(t, resp))
	}

	// Deleting again → 404.
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/v1/channel/%23gone", nil)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("delete-again: got %d want 404", resp.StatusCode)
	}
}

func TestHTTP_Channel_Validation(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, nil)
	cs := NewChannelStore(root)
	srv := httptest.NewServer(Handler(m, cs, nil, ""))
	defer srv.Close()

	// Bad name (uppercase).
	resp := mustPost(t, srv.URL+"/v1/channel/%23BadName/publish", `{"sender":"s","body":"x"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad name: got %d want 400", resp.StatusCode)
	}
	// Missing body.
	resp = mustPost(t, srv.URL+"/v1/channel/%23ok/publish", `{"sender":"s"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing body: got %d want 400", resp.StatusCode)
	}
	// Drain with missing instance.
	resp = mustGet(t, srv.URL+"/v1/channel/%23ok/messages")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing instance: got %d want 400", resp.StatusCode)
	}
	// Unknown verb.
	resp = mustPost(t, srv.URL+"/v1/channel/%23ok/strange-verb", `{}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown verb: got %d want 404", resp.StatusCode)
	}
}

func jsonNumber(n int64) string { return strconv.FormatInt(n, 10) }

func mustPost(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func mustGetWithBuild(t *testing.T, url string, build buildinfo.Info) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	req.Header.Set(buildinfo.HeaderName, build.HeaderValue())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	return buf.String()
}
