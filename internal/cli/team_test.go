package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/runtimebin"
)

func TestTeamCommandsListShowAndStatus(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.ticket-manager]
agent = "ticket-manager"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
after = ["implement"]

[schedules.nightly]
every = "24h"

[teams.delivery]
description = "Default delivery team."
instances = ["manager", "ticket-manager", "worker"]
pipelines = ["ticket_to_pr"]
schedules = ["nightly"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "idle"
description = "ready"
since = "2026-06-18T12:00:00Z"
`, now)
	pipelineJob := &job.Job{
		ID:        "squ-801",
		Ticket:    "SQU-801",
		Target:    "worker",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusDone},
			{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}},
		},
	}
	if err := job.Write(teamDir, pipelineJob); err != nil {
		t.Fatalf("write job: %v", err)
	}
	outsideJob := &job.Job{
		ID:        "oth-801",
		Ticket:    "OTH-801",
		Target:    "platform",
		Pipeline:  "platform_ops",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "implement", Target: "platform-worker", Status: job.StatusDone},
			{ID: "review", Target: "platform-manager", Status: job.StatusBlocked, After: []string{"implement"}},
		},
	}
	if err := job.Write(teamDir, outsideJob); err != nil {
		t.Fatalf("write outside job: %v", err)
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), &daemon.QueueItem{
		ID:             "q-status-team",
		State:          daemon.QueueStateDead,
		EventType:      "agent.dispatch",
		Instance:       "worker",
		InstanceID:     "worker-squ-801",
		Payload:        map[string]any{"job_id": "squ-801", "target": "worker"},
		Attempts:       daemon.MaxQueueAttempts,
		LastError:      "spawn failed",
		QueuedAt:       now.Add(-time.Hour),
		UpdatedAt:      now,
		DeadLetteredAt: now,
	}); err != nil {
		t.Fatalf("write queue item: %v", err)
	}
	writeQuarantinedQueueItem(t, teamDir, "20260619T000000.000000000Z", daemon.QueueStateDead, &daemon.QueueItem{
		ID:         "q-status-team-quarantined",
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-801",
		Payload:    map[string]any{"job_id": "squ-801", "target": "worker"},
		QueuedAt:   now.Add(-2 * time.Hour),
		UpdatedAt:  now.Add(-2 * time.Hour),
	})
	writeQuarantinedQueueItem(t, teamDir, "20260619T000000.000000000Z", daemon.QueueStateDead, &daemon.QueueItem{
		ID:         "q-status-other-quarantined",
		EventType:  "agent.dispatch",
		Instance:   "platform",
		InstanceID: "platform-oth-801",
		Payload:    map[string]any{"job_id": "oth-801", "target": "platform"},
		QueuedAt:   now.Add(-2 * time.Hour),
		UpdatedAt:  now.Add(-2 * time.Hour),
	})

	list := NewRootCmd()
	listOut, listErr := &bytes.Buffer{}, &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetErr(listErr)
	list.SetArgs([]string{"team", "ls", "--repo", root})
	if err := list.Execute(); err != nil {
		t.Fatalf("team ls: %v\nstderr=%s", err, listErr.String())
	}
	for _, want := range []string{"TEAM", "delivery", "Default delivery team.", "3", "1"} {
		if !strings.Contains(listOut.String(), want) {
			t.Fatalf("team ls missing %q:\n%s", want, listOut.String())
		}
	}

	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"team", "show", "delivery", "--repo", root, "--json"})
	if err := show.Execute(); err != nil {
		t.Fatalf("team show: %v\nstderr=%s", err, showErr.String())
	}
	var info teamInfo
	if err := json.Unmarshal(showOut.Bytes(), &info); err != nil {
		t.Fatalf("decode team show: %v\nbody=%s", err, showOut.String())
	}
	if info.Name != "delivery" || len(info.Instances) != 3 || len(info.Pipelines) != 1 || len(info.Schedules) != 1 {
		t.Fatalf("team info = %+v", info)
	}

	graphCmd := NewRootCmd()
	graphOut, graphErr := &bytes.Buffer{}, &bytes.Buffer{}
	graphCmd.SetOut(graphOut)
	graphCmd.SetErr(graphErr)
	graphCmd.SetArgs([]string{"team", "graph", "delivery", "--repo", root, "--routes", "--json"})
	if err := graphCmd.Execute(); err != nil {
		t.Fatalf("team graph json: %v\nstderr=%s", err, graphErr.String())
	}
	var graph teamGraph
	if err := json.Unmarshal(graphOut.Bytes(), &graph); err != nil {
		t.Fatalf("decode team graph: %v\nbody=%s", err, graphOut.String())
	}
	if graph.Team.Name != "delivery" || len(graph.Instances) != 3 || len(graph.Pipelines) != 1 || len(graph.Schedules) != 1 {
		t.Fatalf("team graph summary = %+v", graph)
	}
	if len(graph.Pipelines[0].Nodes) != 2 || graph.Pipelines[0].Nodes[0].Routes[0] != "worker" {
		t.Fatalf("team graph pipeline nodes = %+v", graph.Pipelines[0].Nodes)
	}
	foundDispatchEdge := false
	for _, edge := range graph.Edges {
		if edge.From == "pipeline:ticket_to_pr:step:implement" && edge.To == "instance:worker" && edge.Kind == "dispatches_to" {
			foundDispatchEdge = true
		}
	}
	if !foundDispatchEdge {
		t.Fatalf("team graph edges missing dispatch edge: %+v", graph.Edges)
	}

	graphText := NewRootCmd()
	graphTextOut, graphTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	graphText.SetOut(graphTextOut)
	graphText.SetErr(graphTextErr)
	graphText.SetArgs([]string{"team", "graph", "delivery", "--repo", root, "--routes"})
	if err := graphText.Execute(); err != nil {
		t.Fatalf("team graph text: %v\nstderr=%s", err, graphTextErr.String())
	}
	for _, want := range []string{"Team: delivery", "Instances:", "Pipelines:", "implement target=worker after=- routes=worker", "Edges:", "dispatches_to"} {
		if !strings.Contains(graphTextOut.String(), want) {
			t.Fatalf("team graph text missing %q:\n%s", want, graphTextOut.String())
		}
	}

	graphMermaid := NewRootCmd()
	graphMermaidOut, graphMermaidErr := &bytes.Buffer{}, &bytes.Buffer{}
	graphMermaid.SetOut(graphMermaidOut)
	graphMermaid.SetErr(graphMermaidErr)
	graphMermaid.SetArgs([]string{"team", "graph", "delivery", "--repo", root, "--format", "mermaid"})
	if err := graphMermaid.Execute(); err != nil {
		t.Fatalf("team graph mermaid: %v\nstderr=%s", err, graphMermaidErr.String())
	}
	if !strings.Contains(graphMermaidOut.String(), "flowchart TD") || !strings.Contains(graphMermaidOut.String(), "team_delivery") {
		t.Fatalf("team graph mermaid output:\n%s", graphMermaidOut.String())
	}

	graphDOT := NewRootCmd()
	graphDOTOut, graphDOTErr := &bytes.Buffer{}, &bytes.Buffer{}
	graphDOT.SetOut(graphDOTOut)
	graphDOT.SetErr(graphDOTErr)
	graphDOT.SetArgs([]string{"team", "graph", "delivery", "--repo", root, "--format", "dot"})
	if err := graphDOT.Execute(); err != nil {
		t.Fatalf("team graph dot: %v\nstderr=%s", err, graphDOTErr.String())
	}
	if !strings.Contains(graphDOTOut.String(), "digraph \"delivery\"") || !strings.Contains(graphDOTOut.String(), "\"team:delivery\" -> \"pipeline:ticket_to_pr\"") {
		t.Fatalf("team graph dot output:\n%s", graphDOTOut.String())
	}

	ps := NewRootCmd()
	psOut, psErr := &bytes.Buffer{}, &bytes.Buffer{}
	ps.SetOut(psOut)
	ps.SetErr(psErr)
	ps.SetArgs([]string{"team", "ps", "delivery", "--repo", root, "--json"})
	if err := ps.Execute(); err != nil {
		t.Fatalf("team ps: %v\nstderr=%s", err, psErr.String())
	}
	var instanceRows []psJSONRow
	if err := json.Unmarshal(psOut.Bytes(), &instanceRows); err != nil {
		t.Fatalf("decode team ps: %v\nbody=%s", err, psOut.String())
	}
	if len(instanceRows) != 3 {
		t.Fatalf("team ps rows = %+v", instanceRows)
	}
	instances := map[string]psJSONRow{}
	for _, row := range instanceRows {
		instances[row.Instance] = row
	}
	if instances["manager"].Phase != "idle" || instances["ticket-manager"].Agent != "ticket-manager" || instances["worker"].Agent != "worker" {
		t.Fatalf("team ps instances = %+v", instances)
	}

	psAlias := NewRootCmd()
	psAliasOut, psAliasErr := &bytes.Buffer{}, &bytes.Buffer{}
	psAlias.SetOut(psAliasOut)
	psAlias.SetErr(psAliasErr)
	psAlias.SetArgs([]string{"team", "instances", "delivery", "--repo", root})
	if err := psAlias.Execute(); err != nil {
		t.Fatalf("team instances alias: %v\nstderr=%s", err, psAliasErr.String())
	}
	for _, want := range []string{"INSTANCE", "manager", "ticket-manager", "worker"} {
		if !strings.Contains(psAliasOut.String(), want) {
			t.Fatalf("team ps text missing %q:\n%s", want, psAliasOut.String())
		}
	}

	psFormat := NewRootCmd()
	psFormatOut, psFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	psFormat.SetOut(psFormatOut)
	psFormat.SetErr(psFormatErr)
	psFormat.SetArgs([]string{"team", "ps", "delivery", "--repo", root, "--format", "{{.Instance}} {{.Phase}}"})
	if err := psFormat.Execute(); err != nil {
		t.Fatalf("team ps format: %v\nstderr=%s", err, psFormatErr.String())
	}
	for _, want := range []string{"manager idle", "ticket-manager unknown", "worker unknown"} {
		if !strings.Contains(psFormatOut.String(), want) {
			t.Fatalf("team ps format missing %q:\n%s", want, psFormatOut.String())
		}
	}

	jobs := NewRootCmd()
	jobsOut, jobsErr := &bytes.Buffer{}, &bytes.Buffer{}
	jobs.SetOut(jobsOut)
	jobs.SetErr(jobsErr)
	jobs.SetArgs([]string{"team", "jobs", "delivery", "--repo", root, "--status", "running", "--json"})
	if err := jobs.Execute(); err != nil {
		t.Fatalf("team jobs: %v\nstderr=%s", err, jobsErr.String())
	}
	var ownedJobs []job.Job
	if err := json.Unmarshal(jobsOut.Bytes(), &ownedJobs); err != nil {
		t.Fatalf("decode team jobs: %v\nbody=%s", err, jobsOut.String())
	}
	if len(ownedJobs) != 1 || ownedJobs[0].ID != "squ-801" {
		t.Fatalf("owned jobs = %+v", ownedJobs)
	}

	format := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	format.SetOut(formatOut)
	format.SetErr(formatErr)
	format.SetArgs([]string{"team", "jobs", "delivery", "--repo", root, "--format", "{{.ID}} {{.Pipeline}}"})
	if err := format.Execute(); err != nil {
		t.Fatalf("team jobs format: %v\nstderr=%s", err, formatErr.String())
	}
	if got := strings.TrimSpace(formatOut.String()); got != "squ-801 ticket_to_pr" {
		t.Fatalf("team jobs format = %q", got)
	}

	ready := NewRootCmd()
	readyOut, readyErr := &bytes.Buffer{}, &bytes.Buffer{}
	ready.SetOut(readyOut)
	ready.SetErr(readyErr)
	ready.SetArgs([]string{"team", "ready", "delivery", "--repo", root, "--json"})
	if err := ready.Execute(); err != nil {
		t.Fatalf("team ready: %v\nstderr=%s", err, readyErr.String())
	}
	var readyRows []jobReadyRow
	if err := json.Unmarshal(readyOut.Bytes(), &readyRows); err != nil {
		t.Fatalf("decode team ready: %v\nbody=%s", err, readyOut.String())
	}
	if len(readyRows) != 1 || readyRows[0].JobID != "squ-801" || readyRows[0].State != "ready" || readyRows[0].StepID != "review" {
		t.Fatalf("team ready rows = %+v", readyRows)
	}

	readyFormat := NewRootCmd()
	readyFormatOut, readyFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	readyFormat.SetOut(readyFormatOut)
	readyFormat.SetErr(readyFormatErr)
	readyFormat.SetArgs([]string{"team", "ready", "delivery", "--repo", root, "--state", "all", "--step", "review", "--sort", "updated", "--limit", "1", "--format", "{{.JobID}} {{.State}} {{.StepID}}"})
	if err := readyFormat.Execute(); err != nil {
		t.Fatalf("team ready format: %v\nstderr=%s", err, readyFormatErr.String())
	}
	if got := strings.TrimSpace(readyFormatOut.String()); got != "squ-801 ready review" {
		t.Fatalf("team ready format = %q", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	readyWatch := NewRootCmd()
	readyWatchOut, readyWatchErr := &bytes.Buffer{}, &bytes.Buffer{}
	readyWatch.SetContext(ctx)
	readyWatch.SetOut(readyWatchOut)
	readyWatch.SetErr(readyWatchErr)
	readyWatch.SetArgs([]string{"team", "ready", "delivery", "--repo", root, "--state", "all", "--step", "review", "--sort", "updated", "--limit", "1", "--watch", "--no-clear", "--interval", "1ms", "--format", "{{.JobID}} {{.State}} {{.StepID}}"})
	if err := readyWatch.Execute(); err != nil {
		t.Fatalf("team ready watch: %v\nstderr=%s", err, readyWatchErr.String())
	}
	if got := strings.TrimSpace(readyWatchOut.String()); got != "squ-801 ready review" || strings.Contains(readyWatchOut.String(), watchClearSequence) {
		t.Fatalf("team ready watch = %q", readyWatchOut.String())
	}

	readyInterval := NewRootCmd()
	readyIntervalOut, readyIntervalErr := &bytes.Buffer{}, &bytes.Buffer{}
	readyInterval.SetOut(readyIntervalOut)
	readyInterval.SetErr(readyIntervalErr)
	readyInterval.SetArgs([]string{"team", "ready", "delivery", "--repo", root, "--watch", "--interval", "-1s"})
	if err := readyInterval.Execute(); err == nil {
		t.Fatalf("team ready negative interval succeeded")
	}
	if !strings.Contains(readyIntervalErr.String(), "--interval must be >= 0") {
		t.Fatalf("team ready negative interval stderr = %q", readyIntervalErr.String())
	}

	advance := NewRootCmd()
	advanceOut, advanceErr := &bytes.Buffer{}, &bytes.Buffer{}
	advance.SetOut(advanceOut)
	advance.SetErr(advanceErr)
	advance.SetArgs([]string{"team", "advance", "delivery", "--repo", root, "--dry-run", "--preview-routes", "--json", "--runtime", "codex", "--runtime-bin", "codex-dev"})
	if err := advance.Execute(); err != nil {
		t.Fatalf("team advance dry-run: %v\nstderr=%s", err, advanceErr.String())
	}
	var advanceRows []pipelineAdvanceResult
	if err := json.Unmarshal(advanceOut.Bytes(), &advanceRows); err != nil {
		t.Fatalf("decode team advance: %v\nbody=%s", err, advanceOut.String())
	}
	if len(advanceRows) != 1 || advanceRows[0].JobID != "squ-801" || advanceRows[0].Action != "would_advance" || !advanceRows[0].DryRun || advanceRows[0].StepID != "review" {
		t.Fatalf("team advance rows = %+v", advanceRows)
	}
	if advanceRows[0].Preview == nil || advanceRows[0].Preview.Dispatch == nil || advanceRows[0].Preview.Dispatch.Preview == nil {
		t.Fatalf("team advance preview missing route payload = %+v", advanceRows[0].Preview)
	}
	advancePayload := advanceRows[0].Preview.Dispatch.Preview.Payload
	if advancePayload["runtime"] != "codex" || advancePayload["runtime_binary"] != "codex-dev" {
		t.Fatalf("team advance payload = %+v", advancePayload)
	}

	advanceFormat := NewRootCmd()
	advanceFormatOut, advanceFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	advanceFormat.SetOut(advanceFormatOut)
	advanceFormat.SetErr(advanceFormatErr)
	advanceFormat.SetArgs([]string{"team", "advance", "delivery", "--repo", root, "--dry-run", "--format", "{{.JobID}} {{.Action}} {{.StepID}}"})
	if err := advanceFormat.Execute(); err != nil {
		t.Fatalf("team advance format: %v\nstderr=%s", err, advanceFormatErr.String())
	}
	if got := strings.TrimSpace(advanceFormatOut.String()); got != "squ-801 would_advance review" {
		t.Fatalf("team advance format = %q", got)
	}

	invalidAdvance := NewRootCmd()
	invalidAdvanceOut, invalidAdvanceErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidAdvance.SetOut(invalidAdvanceOut)
	invalidAdvance.SetErr(invalidAdvanceErr)
	invalidAdvance.SetArgs([]string{"team", "advance", "delivery", "--repo", root, "--preview-routes"})
	if err := invalidAdvance.Execute(); err == nil {
		t.Fatal("team advance --preview-routes without --dry-run succeeded")
	}
	if !strings.Contains(invalidAdvanceErr.String(), "--preview-routes requires --dry-run") {
		t.Fatalf("team advance invalid stderr = %q", invalidAdvanceErr.String())
	}

	triage := NewRootCmd()
	triageOut, triageErr := &bytes.Buffer{}, &bytes.Buffer{}
	triage.SetOut(triageOut)
	triage.SetErr(triageErr)
	triage.SetArgs([]string{"team", "triage", "delivery", "--repo", root, "--json"})
	if err := triage.Execute(); err != nil {
		t.Fatalf("team triage: %v\nstderr=%s", err, triageErr.String())
	}
	var triageSnapshot jobTriageSnapshot
	if err := json.Unmarshal(triageOut.Bytes(), &triageSnapshot); err != nil {
		t.Fatalf("decode team triage: %v\nbody=%s", err, triageOut.String())
	}
	if triageSnapshot.Summary.Total != 1 || triageSnapshot.Queue.Dead != 1 || len(triageSnapshot.Attention) != 1 || triageSnapshot.Attention[0].JobID != "squ-801" {
		t.Fatalf("team triage snapshot = %+v", triageSnapshot)
	}
	if len(triageSnapshot.ReadySteps) != 1 || triageSnapshot.ReadySteps[0].JobID != "squ-801" {
		t.Fatalf("team triage ready steps = %+v", triageSnapshot.ReadySteps)
	}

	triageText := NewRootCmd()
	triageTextOut, triageTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	triageText.SetOut(triageTextOut)
	triageText.SetErr(triageTextErr)
	triageText.SetArgs([]string{"team", "triage", "delivery", "--repo", root, "--reason", "queue_dead"})
	if err := triageText.Execute(); err != nil {
		t.Fatalf("team triage text: %v\nstderr=%s", err, triageTextErr.String())
	}
	if !strings.Contains(triageTextOut.String(), "squ-801") || strings.Contains(triageTextOut.String(), "oth-801") {
		t.Fatalf("team triage text = %q", triageTextOut.String())
	}

	triageFormat := NewRootCmd()
	triageFormatOut, triageFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	triageFormat.SetOut(triageFormatOut)
	triageFormat.SetErr(triageFormatErr)
	triageFormat.SetArgs([]string{"team", "triage", "delivery", "--repo", root, "--format", "{{.Summary.Total}} {{.Queue.Dead}} {{len .Attention}} {{len .ReadySteps}}"})
	if err := triageFormat.Execute(); err != nil {
		t.Fatalf("team triage format: %v\nstderr=%s", err, triageFormatErr.String())
	}
	if got, want := triageFormatOut.String(), "1 1 1 1\n"; got != want {
		t.Fatalf("team triage format = %q, want %q", got, want)
	}

	pipelines := NewRootCmd()
	pipelinesOut, pipelinesErr := &bytes.Buffer{}, &bytes.Buffer{}
	pipelines.SetOut(pipelinesOut)
	pipelines.SetErr(pipelinesErr)
	pipelines.SetArgs([]string{"team", "pipelines", "delivery", "--repo", root, "--json"})
	if err := pipelines.Execute(); err != nil {
		t.Fatalf("team pipelines: %v\nstderr=%s", err, pipelinesErr.String())
	}
	var pipelineRows []pipelineStatusRow
	if err := json.Unmarshal(pipelinesOut.Bytes(), &pipelineRows); err != nil {
		t.Fatalf("decode team pipelines: %v\nbody=%s", err, pipelinesOut.String())
	}
	if len(pipelineRows) != 1 || pipelineRows[0].Pipeline != "ticket_to_pr" || pipelineRows[0].ReadySteps != 1 || pipelineRows[0].QueueDead != 1 || pipelineRows[0].QueueQuarantined != 1 || pipelineRows[0].QueueRestorable != 1 {
		t.Fatalf("team pipeline rows = %+v", pipelineRows)
	}
	for _, want := range []string{
		"agent-team team queue delivery --state dead --summary",
		"agent-team team queue retry delivery --all --dry-run",
		"agent-team team queue quarantine delivery",
		"agent-team team queue quarantine delivery --restorable",
		"agent-team team snapshot delivery --json",
	} {
		if !containsString(pipelineRows[0].Actions, want) {
			t.Fatalf("team pipeline actions missing %q: %+v", want, pipelineRows[0].Actions)
		}
	}
	for _, action := range pipelineRows[0].Actions {
		if strings.Contains(action, "agent-team pipeline queue") {
			t.Fatalf("team pipeline action leaked pipeline queue namespace: %+v", pipelineRows[0].Actions)
		}
	}

	pipelinesFormat := NewRootCmd()
	pipelinesFormatOut, pipelinesFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	pipelinesFormat.SetOut(pipelinesFormatOut)
	pipelinesFormat.SetErr(pipelinesFormatErr)
	pipelinesFormat.SetArgs([]string{"team", "pipelines", "delivery", "--repo", root, "--format", "{{.Pipeline}} {{.ReadySteps}}"})
	if err := pipelinesFormat.Execute(); err != nil {
		t.Fatalf("team pipelines format: %v\nstderr=%s", err, pipelinesFormatErr.String())
	}
	if got := strings.TrimSpace(pipelinesFormatOut.String()); got != "ticket_to_pr 1" {
		t.Fatalf("team pipelines format = %q", got)
	}

	pipelinesLimited := NewRootCmd()
	pipelinesLimitedOut, pipelinesLimitedErr := &bytes.Buffer{}, &bytes.Buffer{}
	pipelinesLimited.SetOut(pipelinesLimitedOut)
	pipelinesLimited.SetErr(pipelinesLimitedErr)
	pipelinesLimited.SetArgs([]string{"team", "pipelines", "delivery", "--repo", root, "--sort", "ready", "--limit", "1", "--format", "{{.Pipeline}} {{.ReadySteps}}"})
	if err := pipelinesLimited.Execute(); err != nil {
		t.Fatalf("team pipelines sort/limit: %v\nstderr=%s", err, pipelinesLimitedErr.String())
	}
	if got := strings.TrimSpace(pipelinesLimitedOut.String()); got != "ticket_to_pr 1" {
		t.Fatalf("team pipelines sort/limit = %q", got)
	}

	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	pipelinesWatch := NewRootCmd()
	pipelinesWatchOut, pipelinesWatchErr := &bytes.Buffer{}, &bytes.Buffer{}
	pipelinesWatch.SetContext(ctx)
	pipelinesWatch.SetOut(pipelinesWatchOut)
	pipelinesWatch.SetErr(pipelinesWatchErr)
	pipelinesWatch.SetArgs([]string{"team", "pipelines", "delivery", "--repo", root, "--watch", "--no-clear", "--interval", "1h", "--format", "{{.Pipeline}} {{.ReadySteps}}"})
	if err := pipelinesWatch.Execute(); err != nil {
		t.Fatalf("team pipelines watch: %v\nstderr=%s", err, pipelinesWatchErr.String())
	}
	if got := strings.TrimSpace(pipelinesWatchOut.String()); got != "ticket_to_pr 1" || strings.Contains(pipelinesWatchOut.String(), watchClearSequence) {
		t.Fatalf("team pipelines watch = %q", pipelinesWatchOut.String())
	}

	pipelinesInterval := NewRootCmd()
	pipelinesIntervalOut, pipelinesIntervalErr := &bytes.Buffer{}, &bytes.Buffer{}
	pipelinesInterval.SetOut(pipelinesIntervalOut)
	pipelinesInterval.SetErr(pipelinesIntervalErr)
	pipelinesInterval.SetArgs([]string{"team", "pipelines", "delivery", "--repo", root, "--watch", "--interval", "-1s"})
	if err := pipelinesInterval.Execute(); err == nil {
		t.Fatalf("team pipelines negative interval succeeded")
	}
	if !strings.Contains(pipelinesIntervalErr.String(), "--interval must be >= 0") {
		t.Fatalf("team pipelines negative interval stderr = %q", pipelinesIntervalErr.String())
	}

	pipelinesLimit := NewRootCmd()
	pipelinesLimitOut, pipelinesLimitErr := &bytes.Buffer{}, &bytes.Buffer{}
	pipelinesLimit.SetOut(pipelinesLimitOut)
	pipelinesLimit.SetErr(pipelinesLimitErr)
	pipelinesLimit.SetArgs([]string{"team", "pipelines", "delivery", "--repo", root, "--limit", "-1"})
	if err := pipelinesLimit.Execute(); err == nil {
		t.Fatalf("team pipelines negative limit succeeded")
	}
	if !strings.Contains(pipelinesLimitErr.String(), "--limit must be >= 0") {
		t.Fatalf("team pipelines negative limit stderr = %q", pipelinesLimitErr.String())
	}

	pipelinesSort := NewRootCmd()
	pipelinesSortOut, pipelinesSortErr := &bytes.Buffer{}, &bytes.Buffer{}
	pipelinesSort.SetOut(pipelinesSortOut)
	pipelinesSort.SetErr(pipelinesSortErr)
	pipelinesSort.SetArgs([]string{"team", "pipelines", "delivery", "--repo", root, "--sort", "age"})
	if err := pipelinesSort.Execute(); err == nil {
		t.Fatalf("team pipelines invalid sort succeeded")
	}
	if !strings.Contains(pipelinesSortErr.String(), "--sort must be declared") {
		t.Fatalf("team pipelines invalid sort stderr = %q", pipelinesSortErr.String())
	}

	explain := NewRootCmd()
	explainOut, explainErr := &bytes.Buffer{}, &bytes.Buffer{}
	explain.SetOut(explainOut)
	explain.SetErr(explainErr)
	explain.SetArgs([]string{"team", "explain", "delivery", "--repo", root, "--json"})
	if err := explain.Execute(); err != nil {
		t.Fatalf("team explain: %v\nstderr=%s", err, explainErr.String())
	}
	var explainRows []pipelineExplainRow
	if err := json.Unmarshal(explainOut.Bytes(), &explainRows); err != nil {
		t.Fatalf("decode team explain: %v\nbody=%s", err, explainOut.String())
	}
	if len(explainRows) != 1 || explainRows[0].Pipeline != "ticket_to_pr" || explainRows[0].TotalJobs != 1 || len(explainRows[0].Jobs) != 1 || explainRows[0].Jobs[0].JobID != "squ-801" {
		t.Fatalf("team explain rows = %+v", explainRows)
	}
	if containsString(explainRows[0].Actions, "agent-team pipeline advance ticket_to_pr --dry-run --preview-routes") ||
		!containsString(explainRows[0].Actions, "agent-team team advance delivery --dry-run --preview-routes") ||
		!containsString(explainRows[0].Jobs[0].Actions, "agent-team job advance squ-801") {
		t.Fatalf("team explain actions = %+v job actions=%+v", explainRows[0].Actions, explainRows[0].Jobs[0].Actions)
	}

	explainText := NewRootCmd()
	explainTextOut, explainTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	explainText.SetOut(explainTextOut)
	explainText.SetErr(explainTextErr)
	explainText.SetArgs([]string{"team", "explain", "delivery", "--repo", root})
	if err := explainText.Execute(); err != nil {
		t.Fatalf("team explain text: %v\nstderr=%s", err, explainTextErr.String())
	}
	for _, want := range []string{"Pipeline: ticket_to_pr", "squ-801", "agent-team team advance delivery --dry-run --preview-routes", "agent-team job advance squ-801"} {
		if !strings.Contains(explainTextOut.String(), want) {
			t.Fatalf("team explain text missing %q:\n%s", want, explainTextOut.String())
		}
	}
	if strings.Contains(explainTextOut.String(), "oth-801") {
		t.Fatalf("team explain text included outside job:\n%s", explainTextOut.String())
	}

	explainFormat := NewRootCmd()
	explainFormatOut, explainFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	explainFormat.SetOut(explainFormatOut)
	explainFormat.SetErr(explainFormatErr)
	explainFormat.SetArgs([]string{"team", "explain", "delivery", "--repo", root, "--format", "{{.Pipeline}} {{.TotalJobs}} {{.ExplainedJobs}}"})
	if err := explainFormat.Execute(); err != nil {
		t.Fatalf("team explain format: %v\nstderr=%s", err, explainFormatErr.String())
	}
	if got := strings.TrimSpace(explainFormatOut.String()); got != "ticket_to_pr 1 1" {
		t.Fatalf("team explain format = %q", got)
	}

	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	explainWatch := NewRootCmd()
	explainWatchOut, explainWatchErr := &bytes.Buffer{}, &bytes.Buffer{}
	explainWatch.SetContext(ctx)
	explainWatch.SetOut(explainWatchOut)
	explainWatch.SetErr(explainWatchErr)
	explainWatch.SetArgs([]string{"team", "explain", "delivery", "--repo", root, "--watch", "--no-clear", "--interval", "1h", "--format", "{{.Pipeline}} {{.TotalJobs}} {{.ExplainedJobs}}"})
	if err := explainWatch.Execute(); err != nil {
		t.Fatalf("team explain watch: %v\nstderr=%s", err, explainWatchErr.String())
	}
	if got := strings.TrimSpace(explainWatchOut.String()); got != "ticket_to_pr 1 1" || strings.Contains(explainWatchOut.String(), watchClearSequence) {
		t.Fatalf("team explain watch = %q", explainWatchOut.String())
	}

	explainInterval := NewRootCmd()
	explainIntervalOut, explainIntervalErr := &bytes.Buffer{}, &bytes.Buffer{}
	explainInterval.SetOut(explainIntervalOut)
	explainInterval.SetErr(explainIntervalErr)
	explainInterval.SetArgs([]string{"team", "explain", "delivery", "--repo", root, "--watch", "--interval", "-1s"})
	if err := explainInterval.Execute(); err == nil {
		t.Fatalf("team explain negative interval succeeded")
	}
	if !strings.Contains(explainIntervalErr.String(), "--interval must be >= 0") {
		t.Fatalf("team explain negative interval stderr = %q", explainIntervalErr.String())
	}

	explainReady := NewRootCmd()
	explainReadyOut, explainReadyErr := &bytes.Buffer{}, &bytes.Buffer{}
	explainReady.SetOut(explainReadyOut)
	explainReady.SetErr(explainReadyErr)
	explainReady.SetArgs([]string{"team", "explain", "delivery", "--repo", root, "--state", "ready", "--json"})
	if err := explainReady.Execute(); err != nil {
		t.Fatalf("team explain ready filter: %v\nstderr=%s", err, explainReadyErr.String())
	}
	var readyExplainRows []pipelineExplainRow
	if err := json.Unmarshal(explainReadyOut.Bytes(), &readyExplainRows); err != nil {
		t.Fatalf("decode team ready explain: %v\nbody=%s", err, explainReadyOut.String())
	}
	if len(readyExplainRows) != 1 || readyExplainRows[0].TotalJobs != 1 || readyExplainRows[0].ExplainedJobs != 1 || len(readyExplainRows[0].Jobs) != 1 || readyExplainRows[0].Jobs[0].JobID != "squ-801" {
		t.Fatalf("team ready explain rows = %+v", readyExplainRows)
	}

	explainStep := NewRootCmd()
	explainStepOut, explainStepErr := &bytes.Buffer{}, &bytes.Buffer{}
	explainStep.SetOut(explainStepOut)
	explainStep.SetErr(explainStepErr)
	explainStep.SetArgs([]string{"team", "explain", "delivery", "--repo", root, "--step", "review", "--json"})
	if err := explainStep.Execute(); err != nil {
		t.Fatalf("team explain step filter: %v\nstderr=%s", err, explainStepErr.String())
	}
	var stepExplainRows []pipelineExplainRow
	if err := json.Unmarshal(explainStepOut.Bytes(), &stepExplainRows); err != nil {
		t.Fatalf("decode team step explain: %v\nbody=%s", err, explainStepOut.String())
	}
	if len(stepExplainRows) != 1 || stepExplainRows[0].TotalJobs != 1 || stepExplainRows[0].ExplainedJobs != 1 || len(stepExplainRows[0].Jobs) != 1 || len(stepExplainRows[0].Jobs[0].Steps) != 1 || stepExplainRows[0].Jobs[0].Steps[0].ID != "review" {
		t.Fatalf("team step explain rows = %+v", stepExplainRows)
	}

	explainFailed := NewRootCmd()
	explainFailedOut, explainFailedErr := &bytes.Buffer{}, &bytes.Buffer{}
	explainFailed.SetOut(explainFailedOut)
	explainFailed.SetErr(explainFailedErr)
	explainFailed.SetArgs([]string{"team", "explain", "delivery", "--repo", root, "--state", "failed", "--json"})
	if err := explainFailed.Execute(); err != nil {
		t.Fatalf("team explain failed filter: %v\nstderr=%s", err, explainFailedErr.String())
	}
	var failedExplainRows []pipelineExplainRow
	if err := json.Unmarshal(explainFailedOut.Bytes(), &failedExplainRows); err != nil {
		t.Fatalf("decode team failed explain: %v\nbody=%s", err, explainFailedOut.String())
	}
	if len(failedExplainRows) != 1 || failedExplainRows[0].TotalJobs != 1 || failedExplainRows[0].ExplainedJobs != 0 || len(failedExplainRows[0].Jobs) != 0 {
		t.Fatalf("team failed explain rows = %+v", failedExplainRows)
	}

	explainFailedText := NewRootCmd()
	explainFailedTextOut, explainFailedTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	explainFailedText.SetOut(explainFailedTextOut)
	explainFailedText.SetErr(explainFailedTextErr)
	explainFailedText.SetArgs([]string{"team", "explain", "delivery", "--repo", root, "--state", "failed"})
	if err := explainFailedText.Execute(); err != nil {
		t.Fatalf("team explain failed text: %v\nstderr=%s", err, explainFailedTextErr.String())
	}
	if !strings.Contains(explainFailedTextOut.String(), "Jobs: none selected") {
		t.Fatalf("team explain failed text = %q", explainFailedTextOut.String())
	}

	schedules := NewRootCmd()
	schedulesOut, schedulesErr := &bytes.Buffer{}, &bytes.Buffer{}
	schedules.SetOut(schedulesOut)
	schedules.SetErr(schedulesErr)
	schedules.SetArgs([]string{"team", "schedules", "delivery", "--repo", root, "--json"})
	if err := schedules.Execute(); err != nil {
		t.Fatalf("team schedules: %v\nstderr=%s", err, schedulesErr.String())
	}
	var scheduleRows []scheduleInfo
	if err := json.Unmarshal(schedulesOut.Bytes(), &scheduleRows); err != nil {
		t.Fatalf("decode team schedules: %v\nbody=%s", err, schedulesOut.String())
	}
	if len(scheduleRows) != 1 || scheduleRows[0].Name != "nightly" || scheduleRows[0].Every != "24h0m0s" {
		t.Fatalf("team schedule rows = %+v", scheduleRows)
	}

	schedulesText := NewRootCmd()
	schedulesTextOut, schedulesTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	schedulesText.SetOut(schedulesTextOut)
	schedulesText.SetErr(schedulesTextErr)
	schedulesText.SetArgs([]string{"team", "schedules", "delivery", "--repo", root})
	if err := schedulesText.Execute(); err != nil {
		t.Fatalf("team schedules text: %v\nstderr=%s", err, schedulesTextErr.String())
	}
	for _, want := range []string{"SCHEDULE", "nightly", "24h0m0s"} {
		if !strings.Contains(schedulesTextOut.String(), want) {
			t.Fatalf("team schedules text missing %q:\n%s", want, schedulesTextOut.String())
		}
	}

	schedulesFormat := NewRootCmd()
	schedulesFormatOut, schedulesFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	schedulesFormat.SetOut(schedulesFormatOut)
	schedulesFormat.SetErr(schedulesFormatErr)
	schedulesFormat.SetArgs([]string{"team", "schedules", "delivery", "--repo", root, "--format", "{{.Name}} {{.Every}}"})
	if err := schedulesFormat.Execute(); err != nil {
		t.Fatalf("team schedules format: %v\nstderr=%s", err, schedulesFormatErr.String())
	}
	if got := strings.TrimSpace(schedulesFormatOut.String()); got != "nightly 24h0m0s" {
		t.Fatalf("team schedules format = %q", got)
	}

	status := NewRootCmd()
	statusOut, statusErr := &bytes.Buffer{}, &bytes.Buffer{}
	status.SetOut(statusOut)
	status.SetErr(statusErr)
	status.SetArgs([]string{"team", "status", "delivery", "--repo", root, "--json"})
	if err := status.Execute(); err != nil {
		t.Fatalf("team status: %v\nstderr=%s", err, statusErr.String())
	}
	var snapshot teamStatusSnapshot
	if err := json.Unmarshal(statusOut.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode team status: %v\nbody=%s", err, statusOut.String())
	}
	if snapshot.Team.Name != "delivery" || snapshot.InstanceSummary.Total != 3 || snapshot.JobSummary.Total != 1 {
		t.Fatalf("team status summary = %+v", snapshot)
	}
	if snapshot.Queue.Total != 1 || snapshot.Queue.Dead != 1 || snapshot.Queue.Pending != 0 || snapshot.Queue.Quarantined != 1 || snapshot.Queue.QuarantineRestorable != 1 || snapshot.Queue.QuarantineUnrestorable != 0 {
		t.Fatalf("team status queue = %+v", snapshot.Queue)
	}
	if len(snapshot.PipelineStatus) != 1 || snapshot.PipelineStatus[0].Pipeline != "ticket_to_pr" || snapshot.PipelineStatus[0].ReadySteps != 1 {
		t.Fatalf("pipeline status = %+v", snapshot.PipelineStatus)
	}
	if !containsString(snapshot.Actions, "agent-team team sync delivery --wait") {
		t.Fatalf("actions missing team sync hint: %+v", snapshot.Actions)
	}
	if !containsString(snapshot.Actions, "agent-team team queue retry delivery --all --job squ-801 --dry-run") {
		t.Fatalf("actions missing team queue retry hint: %+v", snapshot.Actions)
	}
	if containsString(snapshot.Actions, "agent-team team queue retry delivery --all") {
		t.Fatalf("actions should prefer job-filtered dry-run retry: %+v", snapshot.Actions)
	}
	if !containsString(snapshot.Actions, "agent-team team queue quarantine delivery") || !containsString(snapshot.Actions, "agent-team team snapshot delivery --json") {
		t.Fatalf("actions missing quarantine hints: %+v", snapshot.Actions)
	}
	if containsString(snapshot.Actions, "agent-team start worker") {
		t.Fatalf("actions should not start ephemeral worker: %+v", snapshot.Actions)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"team", "status", "delivery", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("team status text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"Team: delivery", "instances: total=3", "jobs: total=1", "queue: total=1 pending=0 dead=1 delayed=0 attempts=3 quarantined=1 restorable=1 unrestorable=0", "pipeline status: pipelines=1 jobs=1 ready_steps=1", "Actions:", "agent-team team sync delivery --wait", "agent-team team queue retry delivery --all --job squ-801 --dry-run", "agent-team team queue quarantine delivery", "agent-team team queue quarantine delivery --restorable", "agent-team team advance delivery --dry-run --preview-routes"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("team status text missing %q:\n%s", want, textOut.String())
		}
	}
	if strings.Contains(textOut.String(), "q-status-other-quarantined") {
		t.Fatalf("team status text leaked unrelated quarantine:\n%s", textOut.String())
	}

	statusFormat := NewRootCmd()
	statusFormatOut, statusFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	statusFormat.SetOut(statusFormatOut)
	statusFormat.SetErr(statusFormatErr)
	statusFormat.SetArgs([]string{"team", "status", "delivery", "--repo", root, "--format", "{{.Team.Name}} {{.InstanceSummary.Total}} {{.Queue.Dead}}"})
	if err := statusFormat.Execute(); err != nil {
		t.Fatalf("team status format: %v\nstderr=%s", err, statusFormatErr.String())
	}
	if got, want := statusFormatOut.String(), "delivery 3 1\n"; got != want {
		t.Fatalf("team status format = %q, want %q", got, want)
	}
}

func TestTeamAdvanceAllReadySteps(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.parallel_checks]
trigger.event = "ticket.created"

[[pipelines.parallel_checks.steps]]
id = "lint"
target = "worker"

[[pipelines.parallel_checks.steps]]
id = "test"
target = "worker"

[[pipelines.parallel_checks.steps]]
id = "review"
target = "manager"
after = ["lint", "test"]

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["parallel_checks"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	create := NewRootCmd()
	createOut, createErr := &bytes.Buffer{}, &bytes.Buffer{}
	create.SetOut(createOut)
	create.SetErr(createErr)
	create.SetArgs([]string{"team", "run", "delivery", "SQU-812", "--repo", root, "--json"})
	if err := create.Execute(); err != nil {
		t.Fatalf("team run: %v\nstderr=%s", err, createErr.String())
	}

	ready := NewRootCmd()
	readyOut, readyErr := &bytes.Buffer{}, &bytes.Buffer{}
	ready.SetOut(readyOut)
	ready.SetErr(readyErr)
	ready.SetArgs([]string{"team", "ready", "delivery", "--repo", root, "--json"})
	if err := ready.Execute(); err != nil {
		t.Fatalf("team ready: %v\nstderr=%s", err, readyErr.String())
	}
	var readyRows []jobReadyRow
	if err := json.Unmarshal(readyOut.Bytes(), &readyRows); err != nil {
		t.Fatalf("decode team ready rows: %v\nbody=%s", err, readyOut.String())
	}
	if len(readyRows) != 1 || readyRows[0].ParallelReadySteps != 2 || !containsString(readyRows[0].Actions, "agent-team team advance delivery --all-ready-steps --dry-run --preview-routes") {
		t.Fatalf("team ready rows = %+v, want scoped all-ready action", readyRows)
	}

	status := NewRootCmd()
	statusOut, statusErr := &bytes.Buffer{}, &bytes.Buffer{}
	status.SetOut(statusOut)
	status.SetErr(statusErr)
	status.SetArgs([]string{"team", "pipelines", "delivery", "--repo", root, "--json"})
	if err := status.Execute(); err != nil {
		t.Fatalf("team pipelines: %v\nstderr=%s", err, statusErr.String())
	}
	var statusRows []pipelineStatusRow
	if err := json.Unmarshal(statusOut.Bytes(), &statusRows); err != nil {
		t.Fatalf("decode team pipeline status: %v\nbody=%s", err, statusOut.String())
	}
	if len(statusRows) != 1 || statusRows[0].ParallelReadySteps != 2 || !containsString(statusRows[0].Actions, "agent-team team advance delivery --all-ready-steps --dry-run --preview-routes") {
		t.Fatalf("team pipeline status = %+v, want scoped all-ready action", statusRows)
	}

	all := NewRootCmd()
	allOut, allErr := &bytes.Buffer{}, &bytes.Buffer{}
	all.SetOut(allOut)
	all.SetErr(allErr)
	all.SetArgs([]string{"team", "advance", "delivery", "--repo", root, "--dry-run", "--all-ready-steps", "--json"})
	if err := all.Execute(); err != nil {
		t.Fatalf("team advance all-ready: %v\nstderr=%s", err, allErr.String())
	}
	var allRows []pipelineAdvanceResult
	if err := json.Unmarshal(allOut.Bytes(), &allRows); err != nil {
		t.Fatalf("decode team advance all-ready: %v\nbody=%s", err, allOut.String())
	}
	if len(allRows) != 2 || allRows[0].JobID != "squ-812" || allRows[0].StepID != "lint" || allRows[0].StepStatus != job.StatusQueued || allRows[1].StepID != "test" {
		t.Fatalf("team all-ready rows = %+v, want queued lint then ready test", allRows)
	}

	limited := NewRootCmd()
	limitedOut, limitedErr := &bytes.Buffer{}, &bytes.Buffer{}
	limited.SetOut(limitedOut)
	limited.SetErr(limitedErr)
	limited.SetArgs([]string{"team", "advance", "delivery", "--repo", root, "--dry-run", "--all-ready-steps", "--limit", "1", "--json"})
	if err := limited.Execute(); err != nil {
		t.Fatalf("team advance all-ready limited: %v\nstderr=%s", err, limitedErr.String())
	}
	var limitedRows []pipelineAdvanceResult
	if err := json.Unmarshal(limitedOut.Bytes(), &limitedRows); err != nil {
		t.Fatalf("decode limited team advance: %v\nbody=%s", err, limitedOut.String())
	}
	if len(limitedRows) != 1 || limitedRows[0].StepID != "lint" {
		t.Fatalf("limited team rows = %+v, want queued first step", limitedRows)
	}
}

func TestTeamJobsFiltersByRuntime(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.worker]
agent = "worker"

[instances.manager]
agent = "manager"

[instances.platform-worker]
agent = "platform-worker"

[teams.delivery]
instances = ["worker", "manager"]

[teams.platform]
instances = ["platform-worker"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-901",
			Ticket:    "SQU-901",
			Target:    "worker",
			Instance:  "worker-squ-901",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "plt-901",
			Ticket:    "PLT-901",
			Target:    "platform-worker",
			Instance:  "platform-plt-901",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}
	for _, meta := range []*daemon.Metadata{
		{Instance: "worker-squ-901", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
		{Instance: "platform-plt-901", Agent: "platform-worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
		{Instance: "manager", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "jobs", "delivery", "--repo", root, "--runtime", "codex", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team jobs --runtime codex: %v\nstderr=%s", err, stderr.String())
	}
	var jobs []job.Job
	if err := json.Unmarshal(out.Bytes(), &jobs); err != nil {
		t.Fatalf("decode team jobs runtime: %v\nbody=%s", err, out.String())
	}
	if len(jobs) != 1 || jobs[0].ID != "squ-901" {
		t.Fatalf("team jobs runtime = %+v", jobs)
	}

	summaryCmd := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summaryCmd.SetOut(summaryOut)
	summaryCmd.SetErr(summaryErr)
	summaryCmd.SetArgs([]string{"team", "jobs", "delivery", "--repo", root, "--runtime", "codex", "--summary", "--json"})
	if err := summaryCmd.Execute(); err != nil {
		t.Fatalf("team jobs summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var summary jobSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &summary); err != nil {
		t.Fatalf("decode team jobs summary: %v\nbody=%s", err, summaryOut.String())
	}
	if summary.Total != 1 || summary.Runtimes["codex"] != 1 || summary.Targets["worker"] != 1 {
		t.Fatalf("team jobs summary = %+v", summary)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"team", "jobs", "delivery", "--repo", root, "--runtime", "codex"})
	if err := text.Execute(); err != nil {
		t.Fatalf("team jobs text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"RUNTIME", "squ-901", "codex"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("team jobs text missing %q:\n%s", want, textOut.String())
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	watch := NewRootCmd()
	watchOut, watchErr := &bytes.Buffer{}, &bytes.Buffer{}
	watch.SetContext(ctx)
	watch.SetOut(watchOut)
	watch.SetErr(watchErr)
	watch.SetArgs([]string{"team", "jobs", "delivery", "--repo", root, "--runtime", "codex", "--summary", "--watch", "--no-clear", "--interval", "1h"})
	if err := watch.Execute(); err != nil {
		t.Fatalf("team jobs summary watch: %v\nstderr=%s", err, watchErr.String())
	}
	if !strings.Contains(watchOut.String(), "jobs: total=1") || strings.Contains(watchOut.String(), watchClearSequence) {
		t.Fatalf("team jobs summary watch = %q", watchOut.String())
	}

	claude := NewRootCmd()
	claudeOut, claudeErr := &bytes.Buffer{}, &bytes.Buffer{}
	claude.SetOut(claudeOut)
	claude.SetErr(claudeErr)
	claude.SetArgs([]string{"team", "jobs", "delivery", "--repo", root, "--runtime", "claude", "--json"})
	if err := claude.Execute(); err != nil {
		t.Fatalf("team jobs --runtime claude: %v\nstderr=%s", err, claudeErr.String())
	}
	jobs = nil
	if err := json.Unmarshal(claudeOut.Bytes(), &jobs); err != nil {
		t.Fatalf("decode team jobs claude runtime: %v\nbody=%s", err, claudeOut.String())
	}
	if len(jobs) != 0 {
		t.Fatalf("team jobs claude runtime = %+v", jobs)
	}

	invalid := NewRootCmd()
	invalidOut, invalidErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalid.SetOut(invalidOut)
	invalid.SetErr(invalidErr)
	invalid.SetArgs([]string{"team", "jobs", "delivery", "--repo", root, "--runtime", "bad"})
	if err := invalid.Execute(); err == nil {
		t.Fatalf("team jobs invalid runtime succeeded")
	}
	if !strings.Contains(invalidErr.String(), "unknown --runtime") {
		t.Fatalf("invalid runtime stderr = %q", invalidErr.String())
	}

	badFormat := NewRootCmd()
	badFormatOut, badFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	badFormat.SetOut(badFormatOut)
	badFormat.SetErr(badFormatErr)
	badFormat.SetArgs([]string{"team", "jobs", "delivery", "--repo", root, "--summary", "--format", "{{.ID}}"})
	if err := badFormat.Execute(); err == nil {
		t.Fatalf("team jobs accepted --summary with --format")
	}
	if !strings.Contains(badFormatErr.String(), "--format cannot be combined with --summary") {
		t.Fatalf("summary format stderr = %q", badFormatErr.String())
	}

	invalidInterval := NewRootCmd()
	invalidIntervalOut, invalidIntervalErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidInterval.SetOut(invalidIntervalOut)
	invalidInterval.SetErr(invalidIntervalErr)
	invalidInterval.SetArgs([]string{"team", "jobs", "delivery", "--repo", root, "--watch", "--interval", "-1s"})
	if err := invalidInterval.Execute(); err == nil {
		t.Fatalf("team jobs negative interval succeeded")
	}
	if !strings.Contains(invalidIntervalErr.String(), "--interval must be >= 0") {
		t.Fatalf("negative interval stderr = %q", invalidIntervalErr.String())
	}

	invalidLimit := NewRootCmd()
	invalidLimitOut, invalidLimitErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidLimit.SetOut(invalidLimitOut)
	invalidLimit.SetErr(invalidLimitErr)
	invalidLimit.SetArgs([]string{"team", "jobs", "delivery", "--repo", root, "--limit", "-1"})
	if err := invalidLimit.Execute(); err == nil {
		t.Fatalf("team jobs negative limit succeeded")
	}
	if !strings.Contains(invalidLimitErr.String(), "--limit must be >= 0") {
		t.Fatalf("negative limit stderr = %q", invalidLimitErr.String())
	}

	summaryLimit := NewRootCmd()
	summaryLimitOut, summaryLimitErr := &bytes.Buffer{}, &bytes.Buffer{}
	summaryLimit.SetOut(summaryLimitOut)
	summaryLimit.SetErr(summaryLimitErr)
	summaryLimit.SetArgs([]string{"team", "jobs", "delivery", "--repo", root, "--summary", "--limit", "1"})
	if err := summaryLimit.Execute(); err == nil {
		t.Fatalf("team jobs summary limit succeeded")
	}
	if !strings.Contains(summaryLimitErr.String(), "--limit cannot be combined with --summary") {
		t.Fatalf("summary limit stderr = %q", summaryLimitErr.String())
	}
}

func TestTeamStatusIncludesJobRuntimeSummary(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.worker]
agent = "worker"

[teams.delivery]
instances = ["worker"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-902",
		Ticket:    "SQU-902",
		Target:    "worker",
		Instance:  "worker",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:      "worker",
		Agent:         "worker",
		Runtime:       string(runtimebin.KindCodex),
		RuntimeBinary: "codex-dev",
		Status:        daemon.StatusRunning,
		PID:           os.Getpid(),
		Workspace:     root,
		StartedAt:     now,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "status", "delivery", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team status: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot teamStatusSnapshot
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode team status: %v\nbody=%s", err, out.String())
	}
	if snapshot.JobSummary.Runtimes["codex"] != 1 {
		t.Fatalf("team status job runtimes = %+v", snapshot.JobSummary.Runtimes)
	}
}

func TestTeamPsFiltersByRuntime(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"

[instances.worker]
agent = "worker"
ephemeral = true

[instances.build-worker]
agent = "worker"
ephemeral = true

[teams.delivery]
instances = ["manager", "worker"]

[teams.platform]
instances = ["build-worker"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, Workspace: root, StartedAt: now.Add(-3 * time.Minute)},
		{Instance: "worker-squ-101", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, Workspace: root, StartedAt: now.Add(-2 * time.Minute)},
		{Instance: "build-worker-1", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, Workspace: root, StartedAt: now.Add(-time.Minute)},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "ps", "delivery", "--repo", root, "--runtime", "codex", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team ps --runtime: %v\nstderr=%s", err, stderr.String())
	}
	var rows []psJSONRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode team ps runtime: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "worker-squ-101" || rows[0].Runtime != "codex" {
		t.Fatalf("team ps runtime rows = %+v, want only delivery Codex worker", rows)
	}

	badRuntime := NewRootCmd()
	badRuntime.SetOut(&bytes.Buffer{})
	badRuntimeErr := &bytes.Buffer{}
	badRuntime.SetErr(badRuntimeErr)
	badRuntime.SetArgs([]string{"team", "ps", "delivery", "--repo", root, "--runtime", "llama"})
	if err := badRuntime.Execute(); err == nil {
		t.Fatal("team ps accepted unknown runtime")
	}
	if !strings.Contains(badRuntimeErr.String(), "unknown --runtime") {
		t.Fatalf("bad runtime stderr = %q", badRuntimeErr.String())
	}
}

func TestTeamRetryScopesPipelineFailures(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.platform-worker]
agent = "worker"
ephemeral = true

[[instances.platform-worker.triggers]]
event = "agent.dispatch"
match.target = "platform-worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[pipelines.platform_ops]
trigger.event = "ticket.created"
trigger.match.team = "platform"

[[pipelines.platform_ops.steps]]
id = "implement"
target = "platform-worker"

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]

[teams.platform]
instances = ["platform-worker"]
pipelines = ["platform_ops"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:         "squ-901",
			Ticket:     "SQU-901",
			Target:     "worker",
			Kickoff:    "delivery retry",
			Pipeline:   "ticket_to_pr",
			Status:     job.StatusFailed,
			LastEvent:  "step_failed",
			LastStatus: "implement failed",
			CreatedAt:  now,
			UpdatedAt:  now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusFailed, Instance: "worker-old", StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
			},
		},
		{
			ID:         "oth-901",
			Ticket:     "OTH-901",
			Target:     "platform-worker",
			Kickoff:    "platform retry",
			Pipeline:   "platform_ops",
			Status:     job.StatusFailed,
			LastEvent:  "step_failed",
			LastStatus: "implement failed",
			CreatedAt:  now,
			UpdatedAt:  now,
			Steps: []job.Step{
				{ID: "implement", Target: "platform-worker", Status: job.StatusFailed, Instance: "platform-old", StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"team", "retry", "delivery", "--repo", root, "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("team retry dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryRows []pipelineRetryResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryRows); err != nil {
		t.Fatalf("decode team retry dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(dryRows) != 1 || dryRows[0].JobID != "squ-901" || dryRows[0].Pipeline != "ticket_to_pr" || dryRows[0].Action != "would_retry" || dryRows[0].StepStatus != job.StatusBlocked {
		t.Fatalf("dry rows = %+v", dryRows)
	}
	if strings.Contains(dryOut.String(), "oth-901") || strings.Contains(dryOut.String(), "platform_ops") {
		t.Fatalf("team retry dry-run leaked platform job:\n%s", dryOut.String())
	}

	preview := NewRootCmd()
	previewOut, previewErr := &bytes.Buffer{}, &bytes.Buffer{}
	preview.SetOut(previewOut)
	preview.SetErr(previewErr)
	preview.SetArgs([]string{"team", "retry", "delivery", "--repo", root, "--dispatch", "--workspace", "repo", "--dry-run", "--preview-routes", "--json", "--runtime", "codex", "--runtime-bin", "codex-dev"})
	if err := preview.Execute(); err != nil {
		t.Fatalf("team retry preview: %v\nstderr=%s", err, previewErr.String())
	}
	var previewRows []pipelineRetryResult
	if err := json.Unmarshal(previewOut.Bytes(), &previewRows); err != nil {
		t.Fatalf("decode team retry preview: %v\nbody=%s", err, previewOut.String())
	}
	if len(previewRows) != 1 || previewRows[0].Action != "would_dispatch" || previewRows[0].Preview == nil || previewRows[0].Preview.Dispatch == nil || previewRows[0].Preview.Dispatch.Preview == nil {
		t.Fatalf("preview rows = %+v", previewRows)
	}
	if !containsString(previewRows[0].Preview.Dispatch.Preview.Matched, "worker") {
		t.Fatalf("preview routes = %+v", previewRows[0].Preview.Dispatch.Preview)
	}
	retryPayload := previewRows[0].Preview.Dispatch.Preview.Payload
	if retryPayload["runtime"] != "codex" || retryPayload["runtime_binary"] != "codex-dev" {
		t.Fatalf("team retry payload = %+v", retryPayload)
	}

	format := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	format.SetOut(formatOut)
	format.SetErr(formatErr)
	format.SetArgs([]string{"team", "retry", "delivery", "--repo", root, "--dry-run", "--format", "{{.JobID}} {{.Action}} {{.StepID}}"})
	if err := format.Execute(); err != nil {
		t.Fatalf("team retry format: %v\nstderr=%s", err, formatErr.String())
	}
	if got := strings.TrimSpace(formatOut.String()); got != "squ-901 would_retry implement" {
		t.Fatalf("team retry format = %q", got)
	}
	retryFile := filepath.Join(root, "team-retry-message.txt")
	if err := os.WriteFile(retryFile, []byte("delivery retry approved from file\n"), 0o644); err != nil {
		t.Fatalf("write team retry message file: %v", err)
	}

	run := NewRootCmd()
	runOut, runErr := &bytes.Buffer{}, &bytes.Buffer{}
	run.SetOut(runOut)
	run.SetErr(runErr)
	run.SetArgs([]string{"team", "retry", "delivery", "--repo", root, "--message-file", retryFile, "--json"})
	if err := run.Execute(); err != nil {
		t.Fatalf("team retry: %v\nstderr=%s", err, runErr.String())
	}
	var runRows []pipelineRetryResult
	if err := json.Unmarshal(runOut.Bytes(), &runRows); err != nil {
		t.Fatalf("decode team retry: %v\nbody=%s", err, runOut.String())
	}
	if len(runRows) != 1 || runRows[0].Action != "retried" || runRows[0].StepStatus != job.StatusBlocked || runRows[0].Message != "delivery retry approved from file" {
		t.Fatalf("run rows = %+v", runRows)
	}
	delivery, err := job.Read(teamDir, "squ-901")
	if err != nil {
		t.Fatalf("read delivery: %v", err)
	}
	platform, err := job.Read(teamDir, "oth-901")
	if err != nil {
		t.Fatalf("read platform: %v", err)
	}
	if delivery.Status != job.StatusQueued || delivery.LastStatus != "delivery retry approved from file" || delivery.Steps[0].Status != job.StatusBlocked || delivery.Steps[0].Instance != "" {
		t.Fatalf("delivery job = %+v", delivery)
	}
	if platform.Status != job.StatusFailed || platform.Steps[0].Status != job.StatusFailed || platform.Steps[0].Instance != "platform-old" {
		t.Fatalf("platform job changed = %+v", platform)
	}

	capped := &job.Job{
		ID:         "squ-902",
		Ticket:     "SQU-902",
		Target:     "worker",
		Kickoff:    "delivery force retry",
		Pipeline:   "ticket_to_pr",
		Status:     job.StatusFailed,
		LastEvent:  "step_failed",
		LastStatus: "implement failed",
		CreatedAt:  now,
		UpdatedAt:  now,
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusFailed, Instance: "worker-capped", Attempts: 1, MaxAttempts: 1, StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
		},
	}
	if err := job.Write(teamDir, capped); err != nil {
		t.Fatalf("write capped team job: %v", err)
	}
	force := NewRootCmd()
	forceOut, forceErr := &bytes.Buffer{}, &bytes.Buffer{}
	force.SetOut(forceOut)
	force.SetErr(forceErr)
	force.SetArgs([]string{"team", "retry", "delivery", "--repo", root, "--force", "--message", "team override", "--json"})
	if err := force.Execute(); err != nil {
		t.Fatalf("team retry --force: %v\nstderr=%s", err, forceErr.String())
	}
	var forceRows []pipelineRetryResult
	if err := json.Unmarshal(forceOut.Bytes(), &forceRows); err != nil {
		t.Fatalf("decode team retry --force: %v\nbody=%s", err, forceOut.String())
	}
	if len(forceRows) != 1 || forceRows[0].JobID != "squ-902" || forceRows[0].Action != "retried" || forceRows[0].StepStatus != job.StatusBlocked || forceRows[0].Attempts != 1 || forceRows[0].MaxAttempts != 1 || forceRows[0].Message != "team override" {
		t.Fatalf("team force rows = %+v", forceRows)
	}
	forced, err := job.Read(teamDir, "squ-902")
	if err != nil {
		t.Fatalf("read forced team job: %v", err)
	}
	if forced.Status != job.StatusQueued || forced.Steps[0].Status != job.StatusBlocked || forced.Steps[0].Instance != "" || forced.LastStatus != "team override" {
		t.Fatalf("forced team job = %+v", forced)
	}
}

func TestTeamRetryStepFilter(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:         "squ-911",
			Ticket:     "SQU-911",
			Target:     "worker",
			Kickoff:    "implement failed",
			Pipeline:   "ticket_to_pr",
			Status:     job.StatusFailed,
			LastEvent:  "step_failed",
			LastStatus: "implement failed",
			CreatedAt:  now,
			UpdatedAt:  now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusFailed, Instance: "worker-implement", StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
			},
		},
		{
			ID:         "squ-912",
			Ticket:     "SQU-912",
			Target:     "manager",
			Kickoff:    "review failed",
			Pipeline:   "ticket_to_pr",
			Status:     job.StatusFailed,
			LastEvent:  "step_failed",
			LastStatus: "review failed",
			CreatedAt:  now,
			UpdatedAt:  now,
			Steps: []job.Step{
				{ID: "review", Target: "manager", Status: job.StatusFailed, Instance: "manager-review", StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "retry", "delivery", "--repo", root, "--step", "review", "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team retry --step dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var rows []pipelineRetryResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode team retry --step: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].JobID != "squ-912" || rows[0].StepID != "review" || rows[0].Action != "would_retry" {
		t.Fatalf("rows = %+v", rows)
	}
	if strings.Contains(out.String(), "squ-911") {
		t.Fatalf("team retry --step leaked nonmatching step:\n%s", out.String())
	}
}

func TestTeamTimeoutMarksOwnedStaleRunningSteps(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
timeout = "1h"

[pipelines.other]
trigger.event = "ticket.created"
trigger.match.project = "Other"

[[pipelines.other.steps]]
id = "implement"
target = "worker"
timeout = "1h"

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-800",
			Ticket:    "SQU-800",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-800", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
			},
		},
		{
			ID:        "oth-800",
			Ticket:    "OTH-800",
			Target:    "worker",
			Pipeline:  "other",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-oth-800", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"team", "timeout", "delivery", "--repo", root, "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("team timeout dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryRows []pipelineTimeoutResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryRows); err != nil {
		t.Fatalf("decode dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(dryRows) != 1 || dryRows[0].JobID != "squ-800" || dryRows[0].Action != "would_fail" {
		t.Fatalf("dry rows = %+v", dryRows)
	}
	if strings.Contains(dryOut.String(), "oth-800") {
		t.Fatalf("team timeout dry-run leaked unrelated job:\n%s", dryOut.String())
	}

	apply := NewRootCmd()
	applyOut, applyErr := &bytes.Buffer{}, &bytes.Buffer{}
	apply.SetOut(applyOut)
	apply.SetErr(applyErr)
	timeoutFile := filepath.Join(root, "team-timeout-message.txt")
	if err := os.WriteFile(timeoutFile, []byte("team timed out stale step from file\n"), 0o644); err != nil {
		t.Fatalf("write timeout message: %v", err)
	}
	apply.SetArgs([]string{"team", "timeout", "delivery", "--repo", root, "--message-file", timeoutFile, "--json"})
	if err := apply.Execute(); err != nil {
		t.Fatalf("team timeout apply: %v\nstderr=%s", err, applyErr.String())
	}
	var applied []pipelineTimeoutResult
	if err := json.Unmarshal(applyOut.Bytes(), &applied); err != nil {
		t.Fatalf("decode apply: %v\nbody=%s", err, applyOut.String())
	}
	if len(applied) != 1 || applied[0].JobID != "squ-800" || applied[0].Action != "failed" {
		t.Fatalf("applied rows = %+v", applied)
	}
	owned, err := job.Read(teamDir, "squ-800")
	if err != nil {
		t.Fatalf("read owned job: %v", err)
	}
	if owned.Status != job.StatusFailed || owned.Steps[0].Status != job.StatusFailed || owned.Steps[0].Instance != "" || owned.LastStatus != "team timed out stale step from file" {
		t.Fatalf("owned job = %+v", owned)
	}
	unowned, err := job.Read(teamDir, "oth-800")
	if err != nil {
		t.Fatalf("read unowned job: %v", err)
	}
	if unowned.Status != job.StatusRunning || unowned.Steps[0].Status != job.StatusRunning || unowned.Steps[0].Instance != "worker-oth-800" {
		t.Fatalf("unowned job changed: %+v", unowned)
	}

	retry := NewRootCmd()
	retryOut, retryErr := &bytes.Buffer{}, &bytes.Buffer{}
	retry.SetOut(retryOut)
	retry.SetErr(retryErr)
	retry.SetArgs([]string{"team", "retry", "delivery", "--repo", root, "--dry-run", "--json"})
	if err := retry.Execute(); err != nil {
		t.Fatalf("team retry after timeout: %v\nstderr=%s", err, retryErr.String())
	}
	var retryRows []pipelineRetryResult
	if err := json.Unmarshal(retryOut.Bytes(), &retryRows); err != nil {
		t.Fatalf("decode retry: %v\nbody=%s", err, retryOut.String())
	}
	if len(retryRows) != 1 || retryRows[0].JobID != "squ-800" || retryRows[0].Action != "would_retry" {
		t.Fatalf("retry rows = %+v", retryRows)
	}
}

func TestTeamTimeoutJobsIncludesSteplessWork(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.ops]
agent = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
timeout = "1h"

[pipelines.other]
trigger.event = "ticket.created"

[[pipelines.other.steps]]
id = "implement"
target = "worker"
timeout = "1h"

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-801",
			Ticket:    "SQU-801",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-801", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
			},
		},
		{
			ID:        "squ-802",
			Ticket:    "SQU-802",
			Target:    "worker",
			Instance:  "worker-squ-802",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-48 * time.Hour),
			UpdatedAt: now.Add(-48 * time.Hour),
		},
		{
			ID:        "oth-801",
			Ticket:    "OTH-801",
			Target:    "worker",
			Pipeline:  "other",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-oth-801", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
			},
		},
		{
			ID:        "ops-802",
			Ticket:    "OPS-802",
			Target:    "ops",
			Instance:  "ops-802",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-48 * time.Hour),
			UpdatedAt: now.Add(-48 * time.Hour),
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"team", "timeout", "delivery", "--repo", root, "--jobs", "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("team timeout --jobs dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryRows []pipelineTimeoutResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryRows); err != nil {
		t.Fatalf("decode dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(dryRows) != 2 {
		t.Fatalf("dry rows = %+v", dryRows)
	}
	dryJobs := map[string]bool{}
	for _, row := range dryRows {
		dryJobs[row.JobID] = true
	}
	if !dryJobs["squ-801"] || !dryJobs["squ-802"] || dryJobs["oth-801"] || dryJobs["ops-802"] {
		t.Fatalf("dry jobs = %+v", dryJobs)
	}

	apply := NewRootCmd()
	applyOut, applyErr := &bytes.Buffer{}, &bytes.Buffer{}
	apply.SetOut(applyOut)
	apply.SetErr(applyErr)
	timeoutFile := filepath.Join(root, "team-timeout-sweep.txt")
	if err := os.WriteFile(timeoutFile, []byte("team timeout sweep from file\n"), 0o644); err != nil {
		t.Fatalf("write timeout sweep message: %v", err)
	}
	apply.SetArgs([]string{"team", "timeout", "delivery", "--repo", root, "--jobs", "--message-file", timeoutFile, "--json"})
	if err := apply.Execute(); err != nil {
		t.Fatalf("team timeout --jobs apply: %v\nstderr=%s", err, applyErr.String())
	}
	var rows []pipelineTimeoutResult
	if err := json.Unmarshal(applyOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode apply: %v\nbody=%s", err, applyOut.String())
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	ownedPipeline, err := job.Read(teamDir, "squ-801")
	if err != nil {
		t.Fatalf("read owned pipeline job: %v", err)
	}
	if ownedPipeline.Status != job.StatusFailed || ownedPipeline.Steps[0].Instance != "" || ownedPipeline.LastStatus != "team timeout sweep from file" {
		t.Fatalf("owned pipeline job = %+v", ownedPipeline)
	}
	ownedStandalone, err := job.Read(teamDir, "squ-802")
	if err != nil {
		t.Fatalf("read owned standalone job: %v", err)
	}
	if ownedStandalone.Status != job.StatusFailed || ownedStandalone.Instance != "worker-squ-802" || ownedStandalone.LastEvent != "job_timeout" || ownedStandalone.LastStatus != "team timeout sweep from file" {
		t.Fatalf("owned standalone job = %+v", ownedStandalone)
	}
	unownedPipeline, err := job.Read(teamDir, "oth-801")
	if err != nil {
		t.Fatalf("read unowned pipeline job: %v", err)
	}
	if unownedPipeline.Status != job.StatusRunning || unownedPipeline.Steps[0].Instance != "worker-oth-801" {
		t.Fatalf("unowned pipeline job changed = %+v", unownedPipeline)
	}
	unownedStandalone, err := job.Read(teamDir, "ops-802")
	if err != nil {
		t.Fatalf("read unowned standalone job: %v", err)
	}
	if unownedStandalone.Status != job.StatusRunning || unownedStandalone.Instance != "ops-802" {
		t.Fatalf("unowned standalone job changed = %+v", unownedStandalone)
	}
}

func TestTeamTimeoutFiltersByTargetAgent(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
timeout = "1h"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
timeout = "1h"

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-803",
			Ticket:    "SQU-803",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-803", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
			},
		},
		{
			ID:        "squ-804",
			Ticket:    "SQU-804",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "review", Target: "manager", Status: job.StatusRunning, Instance: "manager-squ-804", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
			},
		},
		{
			ID:        "squ-805",
			Ticket:    "SQU-805",
			Target:    "manager",
			Instance:  "manager-squ-805",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-48 * time.Hour),
			UpdatedAt: now.Add(-48 * time.Hour),
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "timeout", "delivery", "--repo", root, "--jobs", "--target-agent", "manager", "--message", "manager team timeout", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team timeout --target-agent: %v\nstderr=%s", err, stderr.String())
	}
	var rows []pipelineTimeoutResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode team timeout target rows: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 2 || rows[0].JobID != "squ-804" || rows[1].JobID != "squ-805" {
		t.Fatalf("rows = %+v", rows)
	}
	worker, err := job.Read(teamDir, "squ-803")
	if err != nil {
		t.Fatalf("read worker job: %v", err)
	}
	if worker.Status != job.StatusRunning || worker.Steps[0].Status != job.StatusRunning || worker.Steps[0].Instance != "worker-squ-803" {
		t.Fatalf("worker job changed = %+v", worker)
	}
	managerStep, err := job.Read(teamDir, "squ-804")
	if err != nil {
		t.Fatalf("read manager step job: %v", err)
	}
	if managerStep.Status != job.StatusFailed || managerStep.Steps[0].Status != job.StatusFailed || managerStep.Steps[0].Instance != "" || managerStep.LastStatus != "manager team timeout" {
		t.Fatalf("manager step job = %+v", managerStep)
	}
	managerLifecycle, err := job.Read(teamDir, "squ-805")
	if err != nil {
		t.Fatalf("read manager lifecycle job: %v", err)
	}
	if managerLifecycle.Status != job.StatusFailed || managerLifecycle.LastEvent != "job_timeout" || managerLifecycle.LastStatus != "manager team timeout" {
		t.Fatalf("manager lifecycle job = %+v", managerLifecycle)
	}
}

func TestTeamRepairTimeoutPipelinesScopesToTeam(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
timeout = "1h"

[pipelines.other]
trigger.event = "ticket.created"
trigger.match.project = "Other"

[[pipelines.other.steps]]
id = "implement"
target = "worker"
timeout = "1h"

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-830",
			Ticket:    "SQU-830",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-830", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
			},
		},
		{
			ID:        "oth-830",
			Ticket:    "OTH-830",
			Target:    "worker",
			Pipeline:  "other",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-oth-830", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	timeoutMessageFile := filepath.Join(root, "team-repair-timeout-message.txt")
	if err := os.WriteFile(timeoutMessageFile, []byte("team repair timeout approved from file\n"), 0o644); err != nil {
		t.Fatalf("write timeout message: %v", err)
	}
	cmd.SetArgs([]string{
		"team", "repair", "delivery",
		"--repo", root,
		"--timeout-pipelines",
		"--timeout-pipeline", "ticket_to_pr",
		"--timeout-target-agent", "worker",
		"--timeout-message-file", timeoutMessageFile,
		"--skip-daemon",
		"--skip-queue",
		"--skip-tick",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team repair timeout: %v\nstderr=%s", err, stderr.String())
	}
	var result teamRepairResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode team repair timeout: %v\nbody=%s", err, out.String())
	}
	if result.PipelineTimeout.Action != "timed_out" || len(result.PipelineTimeout.Results) != 1 || result.PipelineTimeout.Results[0].JobID != "squ-830" {
		t.Fatalf("team repair timeout = %+v", result.PipelineTimeout)
	}
	owned, err := job.Read(teamDir, "squ-830")
	if err != nil {
		t.Fatalf("read owned job: %v", err)
	}
	if owned.Status != job.StatusFailed || owned.LastStatus != "team repair timeout approved from file" || owned.Steps[0].Instance != "" {
		t.Fatalf("owned job = %+v", owned)
	}
	unowned, err := job.Read(teamDir, "oth-830")
	if err != nil {
		t.Fatalf("read unowned job: %v", err)
	}
	if unowned.Status != job.StatusRunning || unowned.Steps[0].Status != job.StatusRunning || unowned.Steps[0].Instance != "worker-oth-830" {
		t.Fatalf("unowned job changed: %+v", unowned)
	}
}

func TestTeamRepairTimeoutJobsScopesToTeam(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.ops]
agent = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
timeout = "1h"

[pipelines.other]
trigger.event = "ticket.created"
trigger.match.project = "Other"

[[pipelines.other.steps]]
id = "implement"
target = "worker"
timeout = "1h"

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-831",
			Ticket:    "SQU-831",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-831", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
			},
		},
		{
			ID:        "oth-831",
			Ticket:    "OTH-831",
			Target:    "worker",
			Pipeline:  "other",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-oth-831", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
			},
		},
		{
			ID:        "squ-832",
			Ticket:    "SQU-832",
			Target:    "worker",
			Instance:  "worker-squ-832",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-48 * time.Hour),
			UpdatedAt: now.Add(-48 * time.Hour),
		},
		{
			ID:        "ops-832",
			Ticket:    "OPS-832",
			Target:    "ops",
			Instance:  "ops-832",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-48 * time.Hour),
			UpdatedAt: now.Add(-48 * time.Hour),
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{
		"team", "repair", "delivery",
		"--repo", root,
		"--dry-run",
		"--timeout-jobs",
		"--timeout-pipeline", "ticket_to_pr",
		"--skip-daemon",
		"--skip-queue",
		"--skip-tick",
		"--json",
	})
	if err := dry.Execute(); err != nil {
		t.Fatalf("team repair timeout jobs pipeline dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryResult teamRepairResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryResult); err != nil {
		t.Fatalf("decode team repair timeout jobs dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if dryResult.JobTimeout.Action != "would_fail" || len(dryResult.JobTimeout.Results) != 1 || dryResult.JobTimeout.Results[0].JobID != "squ-831" {
		t.Fatalf("team repair timeout jobs dry-run = %+v", dryResult.JobTimeout)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"team", "repair", "delivery",
		"--repo", root,
		"--timeout-jobs",
		"--timeout-message", "team repair timed out job work",
		"--skip-daemon",
		"--skip-queue",
		"--skip-tick",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team repair timeout jobs: %v\nstderr=%s", err, stderr.String())
	}
	var result teamRepairResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode team repair timeout jobs: %v\nbody=%s", err, out.String())
	}
	if result.JobTimeout.Action != "timed_out" || len(result.JobTimeout.Results) != 2 {
		t.Fatalf("team repair job timeout = %+v", result.JobTimeout)
	}
	timedOut := map[string]bool{}
	for _, row := range result.JobTimeout.Results {
		timedOut[row.JobID] = true
	}
	if !timedOut["squ-831"] || !timedOut["squ-832"] || timedOut["oth-831"] || timedOut["ops-832"] {
		t.Fatalf("timed out jobs = %+v", timedOut)
	}
	ownedPipeline, err := job.Read(teamDir, "squ-831")
	if err != nil {
		t.Fatalf("read owned pipeline job: %v", err)
	}
	if ownedPipeline.Status != job.StatusFailed || ownedPipeline.Steps[0].Instance != "" || ownedPipeline.LastStatus != "team repair timed out job work" {
		t.Fatalf("owned pipeline job = %+v", ownedPipeline)
	}
	ownedLifecycle, err := job.Read(teamDir, "squ-832")
	if err != nil {
		t.Fatalf("read owned lifecycle job: %v", err)
	}
	if ownedLifecycle.Status != job.StatusFailed || ownedLifecycle.Instance != "worker-squ-832" || ownedLifecycle.LastEvent != "job_timeout" {
		t.Fatalf("owned lifecycle job = %+v", ownedLifecycle)
	}
	unownedPipeline, err := job.Read(teamDir, "oth-831")
	if err != nil {
		t.Fatalf("read unowned pipeline job: %v", err)
	}
	if unownedPipeline.Status != job.StatusRunning || unownedPipeline.Steps[0].Instance != "worker-oth-831" {
		t.Fatalf("unowned pipeline job changed = %+v", unownedPipeline)
	}
	unownedLifecycle, err := job.Read(teamDir, "ops-832")
	if err != nil {
		t.Fatalf("read unowned lifecycle job: %v", err)
	}
	if unownedLifecycle.Status != job.StatusRunning || unownedLifecycle.Instance != "ops-832" {
		t.Fatalf("unowned lifecycle job changed = %+v", unownedLifecycle)
	}
}

func TestTeamApproveManualGateScopesToTeam(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[[instances.manager.triggers]]
event        = "agent.dispatch"
match.target = "manager"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
after = ["implement"]
gate = "manual"

[pipelines.ops_review]
trigger.event = "ticket.created"

[[pipelines.ops_review.steps]]
id = "implement"
target = "worker"

[[pipelines.ops_review.steps]]
id = "review"
target = "manager"
after = ["implement"]
gate = "manual"

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-930",
			Ticket:    "SQU-930",
			Target:    "worker",
			Kickoff:   "delivery review",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusBlocked,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusDone, StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
				{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}, Gate: job.StepGateManual},
			},
		},
		{
			ID:        "squ-931",
			Ticket:    "SQU-931",
			Target:    "worker",
			Kickoff:   "ops review",
			Pipeline:  "ops_review",
			Status:    job.StatusBlocked,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusDone, StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
				{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}, Gate: job.StepGateManual},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	dryRun := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dryRun.SetOut(dryOut)
	dryRun.SetErr(dryErr)
	dryRun.SetArgs([]string{"team", "approve", "delivery", "--repo", root, "--dry-run", "--dispatch", "--preview-routes", "--json", "--runtime", "codex", "--runtime-bin", "codex-dev"})
	if err := dryRun.Execute(); err != nil {
		t.Fatalf("team approve dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview []pipelineApproveResult
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode team approve dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(preview) != 1 || preview[0].JobID != "squ-930" || preview[0].Action != "would_dispatch" || preview[0].Preview == nil || preview[0].Preview.Dispatch == nil || preview[0].Preview.Dispatch.Preview == nil {
		t.Fatalf("team approve preview = %+v", preview)
	}
	approvePayload := preview[0].Preview.Dispatch.Preview.Payload
	if approvePayload["runtime"] != "codex" || approvePayload["runtime_binary"] != "codex-dev" {
		t.Fatalf("team approve payload = %+v", approvePayload)
	}
	if strings.Contains(dryOut.String(), "squ-931") {
		t.Fatalf("team approve leaked foreign job:\n%s", dryOut.String())
	}

	status := NewRootCmd()
	statusOut, statusErr := &bytes.Buffer{}, &bytes.Buffer{}
	status.SetOut(statusOut)
	status.SetErr(statusErr)
	status.SetArgs([]string{"team", "pipelines", "delivery", "--repo", root, "--json"})
	if err := status.Execute(); err != nil {
		t.Fatalf("team pipelines: %v\nstderr=%s", err, statusErr.String())
	}
	var statusRows []pipelineStatusRow
	if err := json.Unmarshal(statusOut.Bytes(), &statusRows); err != nil {
		t.Fatalf("decode team pipelines: %v\nbody=%s", err, statusOut.String())
	}
	if len(statusRows) != 1 || statusRows[0].Pipeline != "ticket_to_pr" || statusRows[0].ManualGates != 1 || !containsString(statusRows[0].Actions, "agent-team team approve delivery --dry-run --dispatch --preview-routes") {
		t.Fatalf("team pipeline status = %+v", statusRows)
	}
	approvalFile := filepath.Join(root, "team-approval.txt")
	if err := os.WriteFile(approvalFile, []byte("delivery manual approval from file\n"), 0o644); err != nil {
		t.Fatalf("write team approval file: %v", err)
	}

	run := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	run.SetOut(out)
	run.SetErr(stderr)
	run.SetArgs([]string{"team", "approve", "delivery", "--repo", root, "--message-file", approvalFile, "--json"})
	if err := run.Execute(); err != nil {
		t.Fatalf("team approve: %v\nstderr=%s", err, stderr.String())
	}
	var rows []pipelineApproveResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode team approve: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].JobID != "squ-930" || rows[0].Action != "approved" || rows[0].Message != "delivery manual approval from file" {
		t.Fatalf("team approve rows = %+v", rows)
	}
	delivery, err := job.Read(teamDir, "squ-930")
	if err != nil {
		t.Fatalf("read delivery job: %v", err)
	}
	if delivery.Status != job.StatusQueued || delivery.Steps[1].Status != job.StatusQueued {
		t.Fatalf("delivery job = %+v", delivery)
	}
	foreign, err := job.Read(teamDir, "squ-931")
	if err != nil {
		t.Fatalf("read foreign job: %v", err)
	}
	if foreign.Status != job.StatusBlocked || foreign.Steps[1].Status != job.StatusBlocked {
		t.Fatalf("foreign job changed = %+v", foreign)
	}
}

func TestTeamRejectManualGateScopesToTeam(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[[instances.manager.triggers]]
event        = "agent.dispatch"
match.target = "manager"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
after = ["implement"]
gate = "manual"

[pipelines.ops_review]
trigger.event = "ticket.created"

[[pipelines.ops_review.steps]]
id = "implement"
target = "worker"

[[pipelines.ops_review.steps]]
id = "review"
target = "manager"
after = ["implement"]
gate = "manual"

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-932",
			Ticket:    "SQU-932",
			Target:    "worker",
			Kickoff:   "delivery rejection",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusBlocked,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusDone, StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
				{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}, Gate: job.StepGateManual},
			},
		},
		{
			ID:        "squ-933",
			Ticket:    "SQU-933",
			Target:    "worker",
			Kickoff:   "ops rejection",
			Pipeline:  "ops_review",
			Status:    job.StatusBlocked,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusDone, StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
				{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}, Gate: job.StepGateManual},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	dryRun := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dryRun.SetOut(dryOut)
	dryRun.SetErr(dryErr)
	dryRun.SetArgs([]string{"team", "reject", "delivery", "--repo", root, "--dry-run", "--json"})
	if err := dryRun.Execute(); err != nil {
		t.Fatalf("team reject dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview []pipelineApproveResult
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode team reject dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(preview) != 1 || preview[0].JobID != "squ-932" || preview[0].Action != "would_reject" || preview[0].StepStatus != job.StatusFailed {
		t.Fatalf("team reject preview = %+v", preview)
	}
	if strings.Contains(dryOut.String(), "squ-933") {
		t.Fatalf("team reject leaked foreign job:\n%s", dryOut.String())
	}
	unchanged, err := job.Read(teamDir, "squ-932")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.Status != job.StatusBlocked || unchanged.Steps[1].Status != job.StatusBlocked {
		t.Fatalf("dry-run mutated delivery job = %+v", unchanged)
	}
	rejectionFile := filepath.Join(root, "team-rejection.txt")
	if err := os.WriteFile(rejectionFile, []byte("delivery manual rejection from file\n"), 0o644); err != nil {
		t.Fatalf("write team rejection file: %v", err)
	}

	run := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	run.SetOut(out)
	run.SetErr(stderr)
	run.SetArgs([]string{"team", "reject", "delivery", "--repo", root, "--message-file", rejectionFile, "--json"})
	if err := run.Execute(); err != nil {
		t.Fatalf("team reject: %v\nstderr=%s", err, stderr.String())
	}
	var rows []pipelineApproveResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode team reject: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].JobID != "squ-932" || rows[0].Action != "rejected" || rows[0].Message != "delivery manual rejection from file" {
		t.Fatalf("team reject rows = %+v", rows)
	}
	delivery, err := job.Read(teamDir, "squ-932")
	if err != nil {
		t.Fatalf("read delivery job: %v", err)
	}
	if delivery.Status != job.StatusFailed || delivery.Steps[1].Status != job.StatusFailed || delivery.LastEvent != "manual_gate_rejected" {
		t.Fatalf("delivery job = %+v", delivery)
	}
	foreign, err := job.Read(teamDir, "squ-933")
	if err != nil {
		t.Fatalf("read foreign job: %v", err)
	}
	if foreign.Status != job.StatusBlocked || foreign.Steps[1].Status != job.StatusBlocked {
		t.Fatalf("foreign job changed = %+v", foreign)
	}
}

func TestTeamSkipStepScopesToTeam(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
after = ["implement"]

[pipelines.ops_review]
trigger.event = "ticket.created"

[[pipelines.ops_review.steps]]
id = "implement"
target = "worker"

[[pipelines.ops_review.steps]]
id = "review"
target = "manager"
after = ["implement"]

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-934",
			Ticket:    "SQU-934",
			Target:    "worker",
			Kickoff:   "delivery skip",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusBlocked,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusDone, StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
				{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}},
			},
		},
		{
			ID:        "squ-935",
			Ticket:    "SQU-935",
			Target:    "worker",
			Kickoff:   "ops skip",
			Pipeline:  "ops_review",
			Status:    job.StatusBlocked,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusDone, StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
				{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	missingStep := NewRootCmd()
	missingStepOut, missingStepErr := &bytes.Buffer{}, &bytes.Buffer{}
	missingStep.SetOut(missingStepOut)
	missingStep.SetErr(missingStepErr)
	missingStep.SetArgs([]string{"team", "skip", "delivery", "--repo", root})
	if err := missingStep.Execute(); err == nil {
		t.Fatal("team skip without --step succeeded")
	}

	dryRun := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dryRun.SetOut(dryOut)
	dryRun.SetErr(dryErr)
	dryRun.SetArgs([]string{"team", "skip", "delivery", "--repo", root, "--step", "review", "--message", "delivery review bypassed", "--dry-run", "--json"})
	if err := dryRun.Execute(); err != nil {
		t.Fatalf("team skip dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview []pipelineSkipResult
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode team skip dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(preview) != 1 || preview[0].JobID != "squ-934" || preview[0].Action != "would_skip" || preview[0].StepStatus != job.StatusDone || !preview[0].Skipped {
		t.Fatalf("team skip preview = %+v", preview)
	}
	if strings.Contains(dryOut.String(), "squ-935") {
		t.Fatalf("team skip leaked foreign job:\n%s", dryOut.String())
	}
	unchanged, err := job.Read(teamDir, "squ-934")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.Status != job.StatusBlocked || unchanged.Steps[1].Status != job.StatusBlocked || unchanged.Steps[1].Skipped {
		t.Fatalf("dry-run mutated delivery job = %+v", unchanged)
	}
	skipFile := filepath.Join(root, "team-skip-reason.txt")
	if err := os.WriteFile(skipFile, []byte("delivery review bypassed from file\n"), 0o644); err != nil {
		t.Fatalf("write team skip reason file: %v", err)
	}

	run := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	run.SetOut(out)
	run.SetErr(stderr)
	run.SetArgs([]string{"team", "skip", "delivery", "--repo", root, "--step", "review", "--message-file", skipFile, "--json"})
	if err := run.Execute(); err != nil {
		t.Fatalf("team skip: %v\nstderr=%s", err, stderr.String())
	}
	var rows []pipelineSkipResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode team skip: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].JobID != "squ-934" || rows[0].Action != "skipped" || !rows[0].Skipped || rows[0].SkipReason != "delivery review bypassed from file" {
		t.Fatalf("team skip rows = %+v", rows)
	}
	delivery, err := job.Read(teamDir, "squ-934")
	if err != nil {
		t.Fatalf("read delivery job: %v", err)
	}
	if delivery.Status != job.StatusDone || delivery.Steps[1].Status != job.StatusDone || !delivery.Steps[1].Skipped {
		t.Fatalf("delivery job = %+v", delivery)
	}
	foreign, err := job.Read(teamDir, "squ-935")
	if err != nil {
		t.Fatalf("read foreign job: %v", err)
	}
	if foreign.Status != job.StatusBlocked || foreign.Steps[1].Status != job.StatusBlocked || foreign.Steps[1].Skipped {
		t.Fatalf("foreign job changed = %+v", foreign)
	}
}

func TestTeamCancelScopesToTeam(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[pipelines.ops_review]
trigger.event = "ticket.created"

[[pipelines.ops_review.steps]]
id = "implement"
target = "worker"

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-936",
			Ticket:    "SQU-936",
			Target:    "worker",
			Kickoff:   "delivery cancel",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusQueued},
			},
		},
		{
			ID:        "squ-937",
			Ticket:    "SQU-937",
			Target:    "worker",
			Kickoff:   "ops cancel",
			Pipeline:  "ops_review",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusQueued},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	dryRun := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dryRun.SetOut(dryOut)
	dryRun.SetErr(dryErr)
	dryRun.SetArgs([]string{"team", "cancel", "delivery", "--repo", root, "--message", "superseded", "--dry-run", "--json"})
	if err := dryRun.Execute(); err != nil {
		t.Fatalf("team cancel dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview []pipelineCancelResult
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode team cancel dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(preview) != 1 || preview[0].JobID != "squ-936" || preview[0].Action != "would_cancel" || preview[0].StatusAfter != job.StatusFailed {
		t.Fatalf("team cancel preview = %+v", preview)
	}
	if strings.Contains(dryOut.String(), "squ-937") {
		t.Fatalf("team cancel leaked foreign job:\n%s", dryOut.String())
	}
	unchanged, err := job.Read(teamDir, "squ-936")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.Status != job.StatusQueued || unchanged.LastEvent == "cancelled" {
		t.Fatalf("dry-run mutated delivery job = %+v", unchanged)
	}
	cancelFile := filepath.Join(root, "team-cancel-reason.txt")
	if err := os.WriteFile(cancelFile, []byte("superseded from file\n"), 0o644); err != nil {
		t.Fatalf("write team cancel reason file: %v", err)
	}

	run := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	run.SetOut(out)
	run.SetErr(stderr)
	run.SetArgs([]string{"team", "cancel", "delivery", "--repo", root, "--message-file", cancelFile, "--json"})
	if err := run.Execute(); err != nil {
		t.Fatalf("team cancel: %v\nstderr=%s", err, stderr.String())
	}
	var rows []pipelineCancelResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode team cancel: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].JobID != "squ-936" || rows[0].Action != "cancelled" || rows[0].Message != "superseded from file" {
		t.Fatalf("team cancel rows = %+v", rows)
	}
	delivery, err := job.Read(teamDir, "squ-936")
	if err != nil {
		t.Fatalf("read delivery job: %v", err)
	}
	if delivery.Status != job.StatusFailed || delivery.LastEvent != "cancelled" || delivery.LastStatus != "superseded from file" {
		t.Fatalf("delivery job = %+v", delivery)
	}
	foreign, err := job.Read(teamDir, "squ-937")
	if err != nil {
		t.Fatalf("read foreign job: %v", err)
	}
	if foreign.Status != job.StatusQueued || foreign.LastEvent == "cancelled" {
		t.Fatalf("foreign job changed = %+v", foreign)
	}
}

func TestTeamRepairRetryPipelinesStepFilter(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "worker"

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:         "squ-913",
			Ticket:     "SQU-913",
			Target:     "worker",
			Kickoff:    "implement failed",
			Pipeline:   "ticket_to_pr",
			Status:     job.StatusFailed,
			LastEvent:  "step_failed",
			LastStatus: "implement failed",
			CreatedAt:  now,
			UpdatedAt:  now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusFailed, Instance: "worker-implement", StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
			},
		},
		{
			ID:         "squ-914",
			Ticket:     "SQU-914",
			Target:     "worker",
			Kickoff:    "review failed",
			Pipeline:   "ticket_to_pr",
			Status:     job.StatusFailed,
			LastEvent:  "step_failed",
			LastStatus: "review failed",
			CreatedAt:  now,
			UpdatedAt:  now,
			Steps: []job.Step{
				{ID: "review", Target: "worker", Status: job.StatusFailed, Instance: "worker-review", StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"team", "repair", "delivery",
		"--repo", root,
		"--dry-run",
		"--retry-pipelines",
		"--retry-step", "review",
		"--skip-daemon",
		"--skip-queue",
		"--skip-tick",
		"--workspace", "repo",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team repair retry --retry-step: %v\nstderr=%s", err, stderr.String())
	}
	var result teamRepairResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode team repair retry step: %v\nbody=%s", err, out.String())
	}
	if result.PipelineRetry.Action != "would_dispatch" || len(result.PipelineRetry.Results) != 1 {
		t.Fatalf("pipeline retry = %+v", result.PipelineRetry)
	}
	row := result.PipelineRetry.Results[0]
	if row.JobID != "squ-914" || row.StepID != "review" || row.Action != "would_dispatch" {
		t.Fatalf("retry row = %+v", row)
	}
	if strings.Contains(out.String(), "squ-913") {
		t.Fatalf("team repair retry step leaked nonmatching job:\n%s", out.String())
	}
}

func TestTeamRepairAllReadyStepsDryRun(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.parallel_checks]
trigger.event = "ticket.created"

[[pipelines.parallel_checks.steps]]
id = "lint"
target = "worker"

[[pipelines.parallel_checks.steps]]
id = "test"
target = "worker"

[[pipelines.parallel_checks.steps]]
id = "review"
target = "manager"
after = ["lint", "test"]

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["parallel_checks"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	create := NewRootCmd()
	createOut, createErr := &bytes.Buffer{}, &bytes.Buffer{}
	create.SetOut(createOut)
	create.SetErr(createErr)
	create.SetArgs([]string{"team", "run", "delivery", "SQU-814", "--repo", root, "--json"})
	if err := create.Execute(); err != nil {
		t.Fatalf("team run: %v\nstderr=%s", err, createErr.String())
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "repair", "delivery", "--repo", root, "--dry-run", "--skip-daemon", "--skip-queue", "--all-ready-steps", "--preview-routes", "--runtime", "codex", "--runtime-bin", "codex-dev", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team repair all-ready: %v\nstderr=%s", err, stderr.String())
	}
	var result teamRepairResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode team repair all-ready: %v\nbody=%s", err, out.String())
	}
	if result.Tick.Result == nil || len(result.Tick.Result.Tick.Advance) != 2 || result.Tick.Result.Tick.Advance[0].StepID != "lint" || result.Tick.Result.Tick.Advance[0].StepStatus != job.StatusQueued || result.Tick.Result.Tick.Advance[1].StepID != "test" {
		t.Fatalf("team repair all-ready advance = %+v, want queued lint then ready test", result.Tick.Result)
	}
	if result.Tick.Result.Tick.Advance[0].Preview == nil || result.Tick.Result.Tick.Advance[0].Preview.Dispatch == nil || result.Tick.Result.Tick.Advance[0].Preview.Dispatch.Preview == nil {
		t.Fatalf("team repair all-ready preview missing route payload = %+v", result.Tick.Result.Tick.Advance[0].Preview)
	}
	payload := result.Tick.Result.Tick.Advance[0].Preview.Dispatch.Preview.Payload
	if payload["runtime"] != "codex" || payload["runtime_binary"] != "codex-dev" {
		t.Fatalf("team repair all-ready payload = %+v", payload)
	}
}

func TestTeamRetryValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"team", "retry", "delivery", "--limit", "-1"}, "--limit must be >= 0"},
		{[]string{"team", "retry", "delivery", "--preview-routes", "--dry-run"}, "--preview-routes requires --dry-run and --dispatch"},
		{[]string{"team", "retry", "delivery", "--format", "{{.JobID}}", "--json"}, "--format cannot be combined with --json"},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
	}
}

func TestTeamCleanupScopesDoneJobOwnership(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.worker]
agent = "worker"
ephemeral = true

[instances.platform]
agent = "platform"
ephemeral = true

[teams.delivery]
instances = ["worker"]

[teams.platform]
instances = ["platform"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	initGitRepoForJobTest(t, root)
	makeMergedBranch := func(branch string) {
		t.Helper()
		runGitForJobTest(t, root, "checkout", "-b", branch)
		runGitForJobTest(t, root, "checkout", "main")
	}
	deliveryBranch := "worktree-worker-squ-720"
	platformBranch := "worktree-platform-ops-720"
	makeMergedBranch(deliveryBranch)
	makeMergedBranch(platformBranch)
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-720",
			Ticket:    "SQU-720",
			Target:    "worker",
			Status:    job.StatusDone,
			Branch:    deliveryBranch,
			PR:        "https://github.com/acme/repo/pull/720",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "ops-720",
			Ticket:    "OPS-720",
			Target:    "platform",
			Status:    job.StatusDone,
			Branch:    platformBranch,
			PR:        "https://github.com/acme/repo/pull/721",
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}

	preview := NewRootCmd()
	previewOut, previewErr := &bytes.Buffer{}, &bytes.Buffer{}
	preview.SetOut(previewOut)
	preview.SetErr(previewErr)
	preview.SetArgs([]string{"team", "cleanup", "delivery", "--repo", root, "--dry-run", "--json"})
	if err := preview.Execute(); err != nil {
		t.Fatalf("team cleanup dry-run: %v\nstderr=%s", err, previewErr.String())
	}
	var previewResult jobCleanupBatchResult
	if err := json.Unmarshal(previewOut.Bytes(), &previewResult); err != nil {
		t.Fatalf("decode team cleanup preview: %v\nbody=%s", err, previewOut.String())
	}
	if previewResult.Team != "delivery" || !previewResult.DryRun || previewResult.Total != 1 || len(previewResult.Items) != 1 || previewResult.Items[0].JobID != "squ-720" {
		t.Fatalf("team cleanup preview = %+v", previewResult)
	}
	if !branchExists(t, root, deliveryBranch) || !branchExists(t, root, platformBranch) {
		t.Fatalf("dry-run removed a branch")
	}

	previewFormat := NewRootCmd()
	previewFormatOut, previewFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	previewFormat.SetOut(previewFormatOut)
	previewFormat.SetErr(previewFormatErr)
	previewFormat.SetArgs([]string{"team", "cleanup", "delivery", "--repo", root, "--dry-run", "--format", "{{.Team}} {{.Total}} {{.Previewed}} {{len .Items}}"})
	if err := previewFormat.Execute(); err != nil {
		t.Fatalf("team cleanup dry-run format: %v\nstderr=%s", err, previewFormatErr.String())
	}
	if got, want := previewFormatOut.String(), "delivery 1 1 1\n"; got != want {
		t.Fatalf("team cleanup dry-run format = %q, want %q", got, want)
	}

	apply := NewRootCmd()
	applyOut, applyErr := &bytes.Buffer{}, &bytes.Buffer{}
	apply.SetOut(applyOut)
	apply.SetErr(applyErr)
	apply.SetArgs([]string{"team", "cleanup", "delivery", "--repo", root, "--merged", "--json"})
	if err := apply.Execute(); err != nil {
		t.Fatalf("team cleanup apply: %v\nstderr=%s", err, applyErr.String())
	}
	var applied jobCleanupBatchResult
	if err := json.Unmarshal(applyOut.Bytes(), &applied); err != nil {
		t.Fatalf("decode team cleanup apply: %v\nbody=%s", err, applyOut.String())
	}
	if applied.Team != "delivery" || applied.Total != 1 || applied.Cleaned != 1 || len(applied.Items) != 1 || applied.Items[0].JobID != "squ-720" {
		t.Fatalf("team cleanup applied = %+v", applied)
	}
	cleaned, err := job.Read(teamDir, "squ-720")
	if err != nil {
		t.Fatalf("read cleaned job: %v", err)
	}
	untouched, err := job.Read(teamDir, "ops-720")
	if err != nil {
		t.Fatalf("read untouched job: %v", err)
	}
	if cleaned.Branch != "" || cleaned.LastEvent != "cleanup" {
		t.Fatalf("cleaned job = %+v", cleaned)
	}
	if untouched.Branch != platformBranch || untouched.LastEvent == "cleanup" {
		t.Fatalf("outside team job mutated = %+v", untouched)
	}
	if branchExists(t, root, deliveryBranch) {
		t.Fatalf("delivery branch still exists")
	}
	if !branchExists(t, root, platformBranch) {
		t.Fatalf("platform branch was removed")
	}
}

func TestTeamCleanupRejectsFormatCombinations(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "format with json",
			args: []string{"team", "cleanup", "delivery", "--dry-run", "--format", "{{.Team}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "invalid format",
			args: []string{"team", "cleanup", "delivery", "--dry-run", "--format", "{{"},
			want: "invalid --format template",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("team cleanup validation succeeded: stdout=%s", out.String())
			}
			var code ExitCode
			if !errors.As(err, &code) || int(code) != 2 {
				t.Fatalf("team cleanup err = %v, want exit code 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestTeamShowMissingFails(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "show", "missing", "--repo", root})
	if err := cmd.Execute(); err == nil {
		t.Fatal("team show missing succeeded")
	}
	if !strings.Contains(stderr.String(), `team "missing" not found`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestTeamDoctorReportsScopedTopologyHealth(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "doctor", "delivery", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team doctor default: %v\nstderr=%s", err, stderr.String())
	}
	var result teamDoctorResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode team doctor: %v\nbody=%s", err, out.String())
	}
	if !result.OK || result.Team.Name != "delivery" || len(result.Problems) != 0 {
		t.Fatalf("doctor result = %+v", result)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"team", "doctor", "delivery", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("team doctor text: %v\nstderr=%s", err, textErr.String())
	}
	if !strings.Contains(textOut.String(), "agent-team team doctor: OK (delivery)") {
		t.Fatalf("team doctor text = %q", textOut.String())
	}

	format := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	format.SetOut(formatOut)
	format.SetErr(formatErr)
	format.SetArgs([]string{"team", "doctor", "delivery", "--repo", root, "--format", "{{.Team.Name}} {{.OK}} {{len .Problems}}"})
	if err := format.Execute(); err != nil {
		t.Fatalf("team doctor format: %v\nstderr=%s", err, formatErr.String())
	}
	if got, want := formatOut.String(), "delivery true 0\n"; got != want {
		t.Fatalf("team doctor format output = %q, want %q", got, want)
	}
}

func TestTeamDoctorFindsTopologyLeaks(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.worker]
agent = "worker"

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[instances.other]
agent = "other"

[[instances.other.triggers]]
event = "agent.dispatch"
match.target = "other"

[[instances.other.triggers]]
event = "schedule"
match.name = "nightly"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "other"

[pipelines.platform_schedule]
trigger.event = "schedule"
trigger.match.name = "nightly"

[[pipelines.platform_schedule.steps]]
id = "run"
target = "other"

[schedules.nightly]
every = "24h"

[teams.delivery]
instances = ["worker"]
pipelines = ["ticket_to_pr"]
schedules = ["nightly"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "doctor", "delivery", "--repo", root, "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("team doctor unexpectedly succeeded")
	}
	var code ExitCode
	if !errors.As(err, &code) || int(code) != 1 {
		t.Fatalf("err = %v, want exit 1\nstderr=%s", err, stderr.String())
	}
	var result teamDoctorResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode team doctor leak: %v\nbody=%s", err, out.String())
	}
	if result.OK || len(result.Problems) != 1 || result.Problems[0].Code != "pipeline_target_outside_team" || result.Problems[0].Target != "other" {
		t.Fatalf("problems = %+v", result.Problems)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Code != "schedule_routes_outside_team" {
		t.Fatalf("warnings = %+v", result.Warnings)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"team", "doctor", "delivery", "--repo", root})
	if err := text.Execute(); err == nil {
		t.Fatal("team doctor text unexpectedly succeeded")
	}
	if !strings.Contains(textErr.String(), `pipeline "ticket_to_pr" step "implement" targets "other"`) || !strings.Contains(textErr.String(), `schedule "nightly" also matches`) {
		t.Fatalf("team doctor stderr = %q", textErr.String())
	}

	format := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	format.SetOut(formatOut)
	format.SetErr(formatErr)
	format.SetArgs([]string{"team", "doctor", "delivery", "--repo", root, "--format", "{{.Team.Name}} {{.OK}} {{len .Problems}} {{len .Warnings}}"})
	if err := format.Execute(); err == nil {
		t.Fatal("team doctor format unexpectedly succeeded")
	}
	if got, want := formatOut.String(), "delivery false 1 1\n"; got != want {
		t.Fatalf("team doctor failure format output = %q, want %q", got, want)
	}
	if formatErr.Len() != 0 {
		t.Fatalf("team doctor failure format stderr = %q", formatErr.String())
	}
}

func TestTeamDoctorAllValidatesEveryTeam(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.worker]
agent = "worker"

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[instances.other]
agent = "other"

[[instances.other.triggers]]
event = "agent.dispatch"
match.target = "other"

[pipelines.delivery]
trigger.event = "ticket.created"

[[pipelines.delivery.steps]]
id = "implement"
target = "worker"

[pipelines.platform]
trigger.event = "ticket.created"

[[pipelines.platform.steps]]
id = "implement"
target = "other"

[teams.delivery]
instances = ["worker"]
pipelines = ["delivery"]

[teams.platform]
instances = ["worker"]
pipelines = ["platform"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "doctor", "--all", "--repo", root, "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("team doctor --all unexpectedly succeeded")
	}
	var result allTeamDoctorResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode team doctor all: %v\nbody=%s stderr=%s", err, out.String(), stderr.String())
	}
	if result.OK || len(result.Teams) != 2 || !hasTeamDoctorFindingForTeam(result.Problems, "platform", "pipeline_target_outside_team") {
		t.Fatalf("team doctor all result = %+v", result)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"team", "doctor", "--all", "--repo", root})
	if err := text.Execute(); err == nil {
		t.Fatal("team doctor --all text unexpectedly succeeded")
	}
	if textOut.Len() != 0 || !strings.Contains(textErr.String(), `team "platform"`) || !strings.Contains(textErr.String(), `targets "other"`) {
		t.Fatalf("team doctor all text stdout=%q stderr=%q", textOut.String(), textErr.String())
	}

	format := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	format.SetOut(formatOut)
	format.SetErr(formatErr)
	format.SetArgs([]string{"team", "doctor", "--all", "--repo", root, "--format", "{{.OK}} {{len .Teams}} {{len .Problems}}"})
	if err := format.Execute(); err == nil {
		t.Fatal("team doctor --all format unexpectedly succeeded")
	}
	if got, want := formatOut.String(), "false 2 1\n"; got != want {
		t.Fatalf("team doctor --all format output = %q, want %q", got, want)
	}
	if formatErr.Len() != 0 {
		t.Fatalf("team doctor --all format stderr = %q", formatErr.String())
	}

	invalid := NewRootCmd()
	invalidOut, invalidErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalid.SetOut(invalidOut)
	invalid.SetErr(invalidErr)
	invalid.SetArgs([]string{"team", "doctor", "delivery", "--all", "--repo", root})
	if err := invalid.Execute(); err == nil {
		t.Fatal("team doctor <team> --all succeeded")
	}
	if !strings.Contains(invalidErr.String(), "--all cannot be combined") {
		t.Fatalf("invalid stderr = %q", invalidErr.String())
	}

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"team", "doctor", "delivery", "--format", "{{.OK}}", "--json", "--repo", root}, "--format cannot be combined"},
		{[]string{"team", "doctor", "delivery", "--format", "{{", "--repo", root}, "invalid --format template"},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		var code ExitCode
		if !errors.As(err, &code) || int(code) != 2 {
			t.Fatalf("%v: err = %v, want exit 2", tc.args, err)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
		if out.Len() != 0 {
			t.Fatalf("%v: validation wrote stdout: %q", tc.args, out.String())
		}
	}
}

func TestTeamDoctorIncludesPipelineWorkflowFindings(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.worker]
agent = "worker"

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
after = ["review"]

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "worker"
after = ["implement"]

[teams.delivery]
instances = ["worker"]
pipelines = ["ticket_to_pr"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "doctor", "delivery", "--repo", root, "--json"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("team doctor unexpectedly succeeded")
	}
	var result teamDoctorResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode team doctor workflow: %v\nbody=%s", err, out.String())
	}
	if result.OK || !hasTeamDoctorFinding(result.Problems, "dependency_cycle") {
		t.Fatalf("team doctor problems = %+v", result.Problems)
	}
	if !hasTeamDoctorFinding(result.Warnings, "first_step_has_dependencies") {
		t.Fatalf("team doctor warnings = %+v", result.Warnings)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"team", "doctor", "delivery", "--repo", root})
	if err := text.Execute(); err == nil {
		t.Fatal("team doctor text unexpectedly succeeded")
	}
	if textOut.Len() != 0 || !strings.Contains(textErr.String(), "dependency cycle") {
		t.Fatalf("team doctor text stdout=%q stderr=%q", textOut.String(), textErr.String())
	}
}

func TestTeamRunCreatesPipelineJob(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)
	teamDir := filepath.Join(root, ".agent_team")

	previewCmd := NewRootCmd()
	previewOut, previewErr := &bytes.Buffer{}, &bytes.Buffer{}
	previewCmd.SetOut(previewOut)
	previewCmd.SetErr(previewErr)
	oldInput := sendMessageInput
	sendMessageInput = strings.NewReader("ship it from stdin\n")
	defer func() { sendMessageInput = oldInput }()
	previewCmd.SetArgs([]string{"team", "run", "delivery", "SQU-811", "--repo", root, "--kickoff-file", "-", "--dry-run", "--json"})
	if err := previewCmd.Execute(); err != nil {
		t.Fatalf("team run dry-run: %v\nstderr=%s", err, previewErr.String())
	}
	var preview jobCreatePreview
	if err := json.Unmarshal(previewOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode team run preview: %v\nbody=%s", err, previewOut.String())
	}
	if !preview.DryRun || preview.Job == nil || preview.Job.ID != "squ-811" || preview.Job.Pipeline != "ticket_to_pr" || preview.Job.Target != "worker" {
		t.Fatalf("preview = %+v", preview)
	}
	if preview.Job.Kickoff != "SQU-811: ship it from stdin" {
		t.Fatalf("preview kickoff = %q", preview.Job.Kickoff)
	}
	if len(preview.Job.Steps) != 2 || preview.Job.Steps[0].ID != "implement" || preview.Job.Steps[1].ID != "review" {
		t.Fatalf("preview steps = %+v", preview.Job.Steps)
	}
	if _, err := os.Stat(filepath.Join(teamDir, "jobs", "squ-811.toml")); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote team run job file, err=%v", err)
	}

	dispatchPreviewCmd := NewRootCmd()
	dispatchPreviewOut, dispatchPreviewErr := &bytes.Buffer{}, &bytes.Buffer{}
	dispatchPreviewCmd.SetOut(dispatchPreviewOut)
	dispatchPreviewCmd.SetErr(dispatchPreviewErr)
	dispatchPreviewCmd.SetArgs([]string{"team", "run", "delivery", "SQU-812", "--repo", root, "--kickoff", "ship it", "--dispatch", "--dry-run", "--json", "--runtime", "codex", "--runtime-bin", "codex-dev"})
	if err := dispatchPreviewCmd.Execute(); err != nil {
		t.Fatalf("team run dispatch dry-run: %v\nstderr=%s", err, dispatchPreviewErr.String())
	}
	var dispatchPreview jobAdvancePreview
	if err := json.Unmarshal(dispatchPreviewOut.Bytes(), &dispatchPreview); err != nil {
		t.Fatalf("decode team run dispatch preview: %v\nbody=%s", err, dispatchPreviewOut.String())
	}
	if !dispatchPreview.DryRun || dispatchPreview.Job == nil || dispatchPreview.Job.ID != "squ-812" || dispatchPreview.Dispatch == nil || dispatchPreview.Dispatch.Preview == nil {
		t.Fatalf("dispatch preview = %+v", dispatchPreview)
	}
	dispatchPayload := dispatchPreview.Dispatch.Preview.Payload
	if dispatchPayload["runtime"] != "codex" || dispatchPayload["runtime_binary"] != "codex-dev" {
		t.Fatalf("team run dispatch payload = %+v", dispatchPayload)
	}
	if _, err := os.Stat(filepath.Join(teamDir, "jobs", "squ-812.toml")); !os.IsNotExist(err) {
		t.Fatalf("dispatch dry-run wrote team run job file, err=%v", err)
	}

	createCmd := NewRootCmd()
	createOut, createErr := &bytes.Buffer{}, &bytes.Buffer{}
	createCmd.SetOut(createOut)
	createCmd.SetErr(createErr)
	createCmd.SetArgs([]string{"team", "run", "delivery", "SQU-812", "--repo", root, "--ticket-url", "https://linear.app/squirtlesquad/issue/SQU-812/team-run", "--format", "{{.ID}} {{.Pipeline}}"})
	if err := createCmd.Execute(); err != nil {
		t.Fatalf("team run create: %v\nstderr=%s", err, createErr.String())
	}
	if strings.TrimSpace(createOut.String()) != "squ-812 ticket_to_pr" {
		t.Fatalf("team run format = %q", createOut.String())
	}
	created, err := job.Read(teamDir, "squ-812")
	if err != nil {
		t.Fatalf("read created team run job: %v", err)
	}
	if created.Pipeline != "ticket_to_pr" || created.Target != "worker" || created.TicketURL != "https://linear.app/squirtlesquad/issue/SQU-812/team-run" {
		t.Fatalf("created job = %+v", created)
	}
}

func TestTeamRunSelectsPipelineForMultiPipelineTeam(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"

[pipelines.triage]
trigger.event = "ticket.created"

[[pipelines.triage.steps]]
id = "triage"
target = "manager"

[pipelines.review]
trigger.event = "ticket.created"

[[pipelines.review.steps]]
id = "review"
target = "manager"

[teams.ops]
instances = ["manager"]
pipelines = ["triage", "review"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ambiguous := NewRootCmd()
	ambiguousOut, ambiguousErr := &bytes.Buffer{}, &bytes.Buffer{}
	ambiguous.SetOut(ambiguousOut)
	ambiguous.SetErr(ambiguousErr)
	ambiguous.SetArgs([]string{"team", "run", "ops", "SQU-813", "--repo", root, "--dry-run"})
	if err := ambiguous.Execute(); err == nil {
		t.Fatal("team run without --pipeline succeeded for multi-pipeline team")
	}
	if !strings.Contains(ambiguousErr.String(), `choose one with --pipeline`) {
		t.Fatalf("ambiguous stderr = %q", ambiguousErr.String())
	}

	selected := NewRootCmd()
	selectedOut, selectedErr := &bytes.Buffer{}, &bytes.Buffer{}
	selected.SetOut(selectedOut)
	selected.SetErr(selectedErr)
	selected.SetArgs([]string{"team", "run", "ops", "SQU-814", "--repo", root, "--pipeline", "review", "--dry-run", "--json"})
	if err := selected.Execute(); err != nil {
		t.Fatalf("team run selected pipeline: %v\nstderr=%s", err, selectedErr.String())
	}
	var preview jobCreatePreview
	if err := json.Unmarshal(selectedOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode selected team run preview: %v\nbody=%s", err, selectedOut.String())
	}
	if preview.Job == nil || preview.Job.Pipeline != "review" || len(preview.Job.Steps) != 1 || preview.Job.Steps[0].ID != "review" {
		t.Fatalf("selected preview = %+v", preview)
	}

	foreign := NewRootCmd()
	foreignOut, foreignErr := &bytes.Buffer{}, &bytes.Buffer{}
	foreign.SetOut(foreignOut)
	foreign.SetErr(foreignErr)
	foreign.SetArgs([]string{"team", "run", "ops", "SQU-815", "--repo", root, "--pipeline", "missing", "--dry-run"})
	if err := foreign.Execute(); err == nil {
		t.Fatal("team run foreign pipeline succeeded")
	}
	if !strings.Contains(foreignErr.String(), `pipeline "missing" is not declared on team "ops"`) {
		t.Fatalf("foreign stderr = %q", foreignErr.String())
	}
}

func TestTeamStatusWatchRendersSnapshot(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"

[teams.delivery]
instances = ["manager"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if err := runTeamStatusWatch(ctx, &out, teamDir, "delivery", time.Millisecond, false, false); err != nil {
		t.Fatalf("team status watch: %v", err)
	}
	if !strings.Contains(out.String(), "Team: delivery") || !strings.Contains(out.String(), "instances: total=1") {
		t.Fatalf("watch output missing team snapshot:\n%s", out.String())
	}

	jsonCtx, jsonCancel := context.WithCancel(context.Background())
	jsonCancel()
	var jsonOut bytes.Buffer
	if err := runTeamStatusWatch(jsonCtx, &jsonOut, teamDir, "delivery", time.Millisecond, true, false); err != nil {
		t.Fatalf("team status watch json: %v", err)
	}
	var snapshot teamStatusSnapshot
	if err := json.Unmarshal(bytes.TrimSpace(jsonOut.Bytes()), &snapshot); err != nil {
		t.Fatalf("decode watch json: %v\nbody=%s", err, jsonOut.String())
	}
	if snapshot.Team.Name != "delivery" {
		t.Fatalf("watch json snapshot = %+v", snapshot)
	}
}

func TestTeamPsWatchRendersSnapshot(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"

[teams.delivery]
instances = ["manager"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if err := runTeamPsWatch(ctx, &out, teamDir, "delivery", time.Millisecond, false, false); err != nil {
		t.Fatalf("team ps watch: %v", err)
	}
	if !strings.Contains(out.String(), "INSTANCE") || !strings.Contains(out.String(), "manager") {
		t.Fatalf("watch output missing instance rows:\n%s", out.String())
	}

	jsonCtx, jsonCancel := context.WithCancel(context.Background())
	jsonCancel()
	var jsonOut bytes.Buffer
	if err := runTeamPsWatch(jsonCtx, &jsonOut, teamDir, "delivery", time.Millisecond, true, false); err != nil {
		t.Fatalf("team ps watch json: %v", err)
	}
	var rows []psJSONRow
	if err := json.Unmarshal(bytes.TrimSpace(jsonOut.Bytes()), &rows); err != nil {
		t.Fatalf("decode watch json: %v\nbody=%s", err, jsonOut.String())
	}
	if len(rows) != 1 || rows[0].Instance != "manager" {
		t.Fatalf("watch json rows = %+v", rows)
	}
}

func TestTeamLifecycleDryRunScopesInstances(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.ticket-manager]
agent = "ticket-manager"

[instances.build-worker]
agent = "worker"
ephemeral = true

[instances.other]
agent = "other"

[teams.delivery]
instances = ["manager", "ticket-manager", "worker"]

[teams.platform]
instances = ["other", "build-worker"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
		{Instance: "ticket-manager", Agent: "ticket-manager", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
		{Instance: "worker-squ-101", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
		{Instance: "build-worker-1", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
		{Instance: "other", Agent: "other", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	up := NewRootCmd()
	upOut, upErr := &bytes.Buffer{}, &bytes.Buffer{}
	up.SetOut(upOut)
	up.SetErr(upErr)
	up.SetArgs([]string{"team", "up", "delivery", "--repo", root, "--dry-run", "--json"})
	if err := up.Execute(); err != nil {
		t.Fatalf("team up dry-run: %v\nstderr=%s", err, upErr.String())
	}
	var upRows []lifecycleActionResult
	if err := json.Unmarshal(upOut.Bytes(), &upRows); err != nil {
		t.Fatalf("decode team up: %v\nbody=%s", err, upOut.String())
	}
	if got := lifecycleResultInstances(upRows); strings.Join(got, ",") != "manager,ticket-manager" {
		t.Fatalf("team up instances = %v", got)
	}

	upRuntime := NewRootCmd()
	upRuntimeOut, upRuntimeErr := &bytes.Buffer{}, &bytes.Buffer{}
	upRuntime.SetOut(upRuntimeOut)
	upRuntime.SetErr(upRuntimeErr)
	upRuntime.SetArgs([]string{"team", "up", "delivery", "--repo", root, "--runtime", "codex", "--dry-run", "--json"})
	if err := upRuntime.Execute(); err != nil {
		t.Fatalf("team up runtime dry-run: %v\nstderr=%s", err, upRuntimeErr.String())
	}
	var upRuntimeRows []lifecycleActionResult
	if err := json.Unmarshal(upRuntimeOut.Bytes(), &upRuntimeRows); err != nil {
		t.Fatalf("decode team up runtime: %v\nbody=%s", err, upRuntimeOut.String())
	}
	if got := lifecycleResultInstances(upRuntimeRows); strings.Join(got, ",") != "ticket-manager" {
		t.Fatalf("team up runtime instances = %v", got)
	}

	down := NewRootCmd()
	downOut, downErr := &bytes.Buffer{}, &bytes.Buffer{}
	down.SetOut(downOut)
	down.SetErr(downErr)
	down.SetArgs([]string{"team", "down", "delivery", "--repo", root, "--dry-run", "--json"})
	if err := down.Execute(); err != nil {
		t.Fatalf("team down dry-run: %v\nstderr=%s", err, downErr.String())
	}
	var downRows []instanceDownResult
	if err := json.Unmarshal(downOut.Bytes(), &downRows); err != nil {
		t.Fatalf("decode team down: %v\nbody=%s", err, downOut.String())
	}
	downNames := instanceDownResultNames(downRows)
	for _, want := range []string{"manager", "ticket-manager", "worker-squ-101"} {
		if !stringInSlice(want, downNames) {
			t.Fatalf("team down instances = %v, missing %s", downNames, want)
		}
	}
	for _, unwanted := range []string{"worker", "build-worker-1", "other"} {
		if stringInSlice(unwanted, downNames) {
			t.Fatalf("team down instances = %v, included %s", downNames, unwanted)
		}
	}

	downRuntime := NewRootCmd()
	downRuntimeOut, downRuntimeErr := &bytes.Buffer{}, &bytes.Buffer{}
	downRuntime.SetOut(downRuntimeOut)
	downRuntime.SetErr(downRuntimeErr)
	downRuntime.SetArgs([]string{"team", "down", "delivery", "--repo", root, "--runtime", "codex", "--dry-run", "--json"})
	if err := downRuntime.Execute(); err != nil {
		t.Fatalf("team down runtime dry-run: %v\nstderr=%s", err, downRuntimeErr.String())
	}
	var downRuntimeRows []instanceDownResult
	if err := json.Unmarshal(downRuntimeOut.Bytes(), &downRuntimeRows); err != nil {
		t.Fatalf("decode team down runtime: %v\nbody=%s", err, downRuntimeOut.String())
	}
	downRuntimeNames := instanceDownResultNames(downRuntimeRows)
	for _, want := range []string{"ticket-manager", "worker-squ-101"} {
		if !stringInSlice(want, downRuntimeNames) {
			t.Fatalf("team down runtime instances = %v, missing %s", downRuntimeNames, want)
		}
	}
	for _, unwanted := range []string{"manager", "worker", "build-worker-1", "other"} {
		if stringInSlice(unwanted, downRuntimeNames) {
			t.Fatalf("team down runtime instances = %v, included %s", downRuntimeNames, unwanted)
		}
	}

	restart := NewRootCmd()
	restartOut, restartErr := &bytes.Buffer{}, &bytes.Buffer{}
	restart.SetOut(restartOut)
	restart.SetErr(restartErr)
	restart.SetArgs([]string{"team", "restart", "delivery", "--repo", root, "--dry-run", "--json"})
	if err := restart.Execute(); err != nil {
		t.Fatalf("team restart dry-run: %v\nstderr=%s", err, restartErr.String())
	}
	var restartRows []lifecycleActionResult
	if err := json.Unmarshal(restartOut.Bytes(), &restartRows); err != nil {
		t.Fatalf("decode team restart: %v\nbody=%s", err, restartOut.String())
	}
	if got := lifecycleResultInstances(restartRows); strings.Join(got, ",") != "manager,ticket-manager" {
		t.Fatalf("team restart instances = %v", got)
	}

	restartRuntime := NewRootCmd()
	restartRuntimeOut, restartRuntimeErr := &bytes.Buffer{}, &bytes.Buffer{}
	restartRuntime.SetOut(restartRuntimeOut)
	restartRuntime.SetErr(restartRuntimeErr)
	restartRuntime.SetArgs([]string{"team", "restart", "delivery", "--repo", root, "--runtime", "codex", "--dry-run", "--json"})
	if err := restartRuntime.Execute(); err != nil {
		t.Fatalf("team restart runtime dry-run: %v\nstderr=%s", err, restartRuntimeErr.String())
	}
	var restartRuntimeRows []lifecycleActionResult
	if err := json.Unmarshal(restartRuntimeOut.Bytes(), &restartRuntimeRows); err != nil {
		t.Fatalf("decode team restart runtime: %v\nbody=%s", err, restartRuntimeOut.String())
	}
	if got := lifecycleResultInstances(restartRuntimeRows); strings.Join(got, ",") != "ticket-manager" {
		t.Fatalf("team restart runtime instances = %v", got)
	}

	invalid := NewRootCmd()
	invalidOut, invalidErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalid.SetOut(invalidOut)
	invalid.SetErr(invalidErr)
	invalid.SetArgs([]string{"team", "up", "delivery", "--repo", root, "--runtime", "llama", "--dry-run", "--json"})
	if err := invalid.Execute(); err == nil {
		t.Fatalf("team up invalid runtime succeeded\nstdout=%s\nstderr=%s", invalidOut.String(), invalidErr.String())
	}
	if !strings.Contains(invalidErr.String(), "unknown --runtime") {
		t.Fatalf("invalid runtime stderr = %q", invalidErr.String())
	}
}

func TestTeamWaitScopesSelection(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"

[instances.ticket-manager]
agent = "ticket-manager"

[instances.worker]
agent = "worker"
ephemeral = true

[instances.build-worker]
agent = "worker"
ephemeral = true

[instances.other]
agent = "other"

[teams.delivery]
instances = ["manager", "ticket-manager", "worker"]

[teams.platform]
instances = ["other", "build-worker"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "idle"
description = "ready"
since = "2026-06-18T12:00:00Z"
	`, now)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(-3 * time.Minute)},
		{Instance: "worker-squ-101", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(-2 * time.Minute)},
		{Instance: "build-worker-1", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(-time.Minute)},
		{Instance: "other", Agent: "other", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"team", "wait", "delivery", "--repo", root, "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("team wait dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var rows []waitResult
	if err := json.Unmarshal(dryOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode team wait: %v\nbody=%s", err, dryOut.String())
	}
	byInstance := map[string]waitResult{}
	for _, row := range rows {
		byInstance[row.Instance] = row
	}
	for _, want := range []string{"manager", "ticket-manager", "worker-squ-101"} {
		if _, ok := byInstance[want]; !ok {
			t.Fatalf("team wait rows = %+v, missing %s", rows, want)
		}
	}
	for _, unwanted := range []string{"build-worker-1", "other", "worker"} {
		if _, ok := byInstance[unwanted]; ok {
			t.Fatalf("team wait rows = %+v, included %s", rows, unwanted)
		}
	}
	if byInstance["ticket-manager"].Status != "unknown" || byInstance["manager"].Status != "running" {
		t.Fatalf("team wait statuses = %+v", byInstance)
	}

	unknown := NewRootCmd()
	unknownOut, unknownErr := &bytes.Buffer{}, &bytes.Buffer{}
	unknown.SetOut(unknownOut)
	unknown.SetErr(unknownErr)
	unknown.SetArgs([]string{"team", "wait", "delivery", "--repo", root, "--status", "unknown", "--dry-run", "--json"})
	if err := unknown.Execute(); err != nil {
		t.Fatalf("team wait unknown: %v\nstderr=%s", err, unknownErr.String())
	}
	rows = nil
	if err := json.Unmarshal(unknownOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode team wait unknown: %v\nbody=%s", err, unknownOut.String())
	}
	if len(rows) != 1 || rows[0].Instance != "ticket-manager" || rows[0].Status != "unknown" {
		t.Fatalf("team wait unknown rows = %+v", rows)
	}

	codex := NewRootCmd()
	codexOut, codexErr := &bytes.Buffer{}, &bytes.Buffer{}
	codex.SetOut(codexOut)
	codex.SetErr(codexErr)
	codex.SetArgs([]string{"team", "wait", "delivery", "--repo", root, "--runtime", "codex", "--dry-run", "--json"})
	if err := codex.Execute(); err != nil {
		t.Fatalf("team wait codex: %v\nstderr=%s", err, codexErr.String())
	}
	rows = nil
	if err := json.Unmarshal(codexOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode team wait codex: %v\nbody=%s", err, codexOut.String())
	}
	if len(rows) != 1 || rows[0].Instance != "worker-squ-101" || rows[0].Status != "running" {
		t.Fatalf("team wait codex rows = %+v", rows)
	}

	running := NewRootCmd()
	runningOut, runningErr := &bytes.Buffer{}, &bytes.Buffer{}
	running.SetOut(runningOut)
	running.SetErr(runningErr)
	running.SetArgs([]string{"team", "wait", "delivery", "manager", "--repo", root, "--json"})
	if err := running.Execute(); err != nil {
		t.Fatalf("team wait running: %v\nstderr=%s", err, runningErr.String())
	}
	rows = nil
	if err := json.Unmarshal(runningOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode team wait running: %v\nbody=%s", err, runningOut.String())
	}
	if len(rows) != 1 || rows[0].Instance != "manager" || rows[0].Status != "running" {
		t.Fatalf("team wait running rows = %+v", rows)
	}

	foreign := NewRootCmd()
	foreign.SetOut(&bytes.Buffer{})
	foreignErr := &bytes.Buffer{}
	foreign.SetErr(foreignErr)
	foreign.SetArgs([]string{"team", "wait", "delivery", "other", "--repo", root, "--dry-run"})
	if err := foreign.Execute(); err == nil {
		t.Fatal("team wait accepted non-team instance")
	}
	if !strings.Contains(foreignErr.String(), `instance "other" is not known to team "delivery"`) {
		t.Fatalf("foreign stderr = %q", foreignErr.String())
	}

	badRuntime := NewRootCmd()
	badRuntime.SetOut(&bytes.Buffer{})
	badRuntimeErr := &bytes.Buffer{}
	badRuntime.SetErr(badRuntimeErr)
	badRuntime.SetArgs([]string{"team", "wait", "delivery", "--repo", root, "--runtime", "llama"})
	if err := badRuntime.Execute(); err == nil {
		t.Fatal("team wait accepted unknown runtime")
	}
	if !strings.Contains(badRuntimeErr.String(), "unknown --runtime") {
		t.Fatalf("bad runtime stderr = %q", badRuntimeErr.String())
	}
}

func TestTeamWaitRuntimeStaleScopesInstances(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.build-worker]
agent = "worker"
ephemeral = true

[teams.delivery]
instances = ["manager", "worker"]

[teams.platform]
instances = ["build-worker"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, meta := range []*daemon.Metadata{
		{Instance: "worker-squ-101", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: 99999999, StartedAt: now.Add(-2 * time.Minute), Workspace: root},
		{Instance: "worker-squ-102", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(-time.Minute), Workspace: root},
		{Instance: "build-worker-1", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: 99999999, StartedAt: now, Workspace: root},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "wait", "delivery", "--repo", root, "--runtime-stale", "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team wait --runtime-stale dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var rows []waitResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode team wait --runtime-stale: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "worker-squ-101" || rows[0].Status != string(daemon.StatusRunning) {
		t.Fatalf("team wait --runtime-stale rows = %+v, want delivery runtime-stale worker only", rows)
	}
}

func TestTeamPruneScopesFinishedInstances(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"

[instances.worker]
agent = "worker"
ephemeral = true

[instances.build-worker]
agent = "worker"
ephemeral = true

[instances.other]
agent = "other"

[teams.delivery]
instances = ["manager", "worker"]

[teams.platform]
instances = ["other", "build-worker"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, name := range []string{"manager", "worker-squ-101", "build-worker-1", "other"} {
		if err := os.MkdirAll(filepath.Join(teamDir, "state", name), 0o755); err != nil {
			t.Fatalf("mkdir state %s: %v", name, err)
		}
	}
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusExited, Workspace: root, StartedAt: now.Add(-4 * time.Hour), ExitedAt: now.Add(-3 * time.Hour)},
		{Instance: "worker-squ-101", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusCrashed, Workspace: root, StartedAt: now.Add(-3 * time.Hour), ExitedAt: now.Add(-2 * time.Hour)},
		{Instance: "build-worker-1", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusCrashed, Workspace: root, StartedAt: now.Add(-3 * time.Hour), ExitedAt: now.Add(-2 * time.Hour)},
		{Instance: "other", Agent: "other", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusExited, Workspace: root, StartedAt: now.Add(-2 * time.Hour), ExitedAt: now.Add(-time.Hour)},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"team", "prune", "delivery", "--repo", root, "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("team prune dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview []instanceRmResult
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode team prune dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if got := instanceRmResultNames(preview); strings.Join(got, ",") != "manager,worker-squ-101" {
		t.Fatalf("team prune preview names = %v", got)
	}
	for _, row := range preview {
		if !row.DryRun || !row.StateRemoved || !row.DaemonRemoved {
			t.Fatalf("team prune preview row = %+v", row)
		}
	}
	if _, err := daemon.ReadMetadata(daemon.DaemonRoot(teamDir), "worker-squ-101"); err != nil {
		t.Fatalf("dry-run removed worker metadata: %v", err)
	}

	codexDry := NewRootCmd()
	codexDryOut, codexDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	codexDry.SetOut(codexDryOut)
	codexDry.SetErr(codexDryErr)
	codexDry.SetArgs([]string{"team", "prune", "delivery", "--repo", root, "--runtime", "codex", "--dry-run", "--json"})
	if err := codexDry.Execute(); err != nil {
		t.Fatalf("team prune runtime dry-run: %v\nstderr=%s", err, codexDryErr.String())
	}
	preview = nil
	if err := json.Unmarshal(codexDryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode team prune runtime dry-run: %v\nbody=%s", err, codexDryOut.String())
	}
	if got := instanceRmResultNames(preview); strings.Join(got, ",") != "worker-squ-101" {
		t.Fatalf("team prune runtime preview names = %v", got)
	}

	badRuntime := NewRootCmd()
	badRuntime.SetOut(&bytes.Buffer{})
	badRuntimeErr := &bytes.Buffer{}
	badRuntime.SetErr(badRuntimeErr)
	badRuntime.SetArgs([]string{"team", "prune", "delivery", "--repo", root, "--runtime", "llama", "--dry-run"})
	if err := badRuntime.Execute(); err == nil {
		t.Fatal("team prune accepted unknown runtime")
	}
	if !strings.Contains(badRuntimeErr.String(), "unknown --runtime") {
		t.Fatalf("bad runtime stderr = %q", badRuntimeErr.String())
	}

	prune := NewRootCmd()
	pruneOut, pruneErr := &bytes.Buffer{}, &bytes.Buffer{}
	prune.SetOut(pruneOut)
	prune.SetErr(pruneErr)
	prune.SetArgs([]string{"team", "prune", "delivery", "--repo", root, "--status", "crashed", "--format", "{{.Instance}} {{.Removed}}"})
	if err := prune.Execute(); err != nil {
		t.Fatalf("team prune crashed: %v\nstderr=%s", err, pruneErr.String())
	}
	if got := strings.TrimSpace(pruneOut.String()); got != "worker-squ-101 true" {
		t.Fatalf("team prune crashed output = %q", got)
	}
	if _, err := daemon.ReadMetadata(daemon.DaemonRoot(teamDir), "worker-squ-101"); err == nil {
		t.Fatal("worker metadata still exists after team prune")
	}
	if _, err := os.Stat(filepath.Join(teamDir, "state", "worker-squ-101")); !os.IsNotExist(err) {
		t.Fatalf("worker state still exists or unexpected err=%v", err)
	}
	for _, name := range []string{"manager", "build-worker-1", "other"} {
		if _, err := daemon.ReadMetadata(daemon.DaemonRoot(teamDir), name); err != nil {
			t.Fatalf("metadata %s should remain: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(teamDir, "state", name)); err != nil {
			t.Fatalf("state %s should remain: %v", name, err)
		}
	}
}

func TestTeamRuntimeResumePlanScopesMetadata(t *testing.T) {
	root := writeOverviewRuntimeFixture(t)
	teamDir := filepath.Join(root, ".agent_team")
	oldPIDLiveCheck := daemon.PidLiveCheck
	daemon.PidLiveCheck = func(pid int) bool {
		return pid != 4242
	}
	t.Cleanup(func() {
		daemon.PidLiveCheck = oldPIDLiveCheck
	})
	now := time.Now().UTC()
	for _, meta := range []*daemon.Metadata{
		{Instance: "worker-squ-902", Agent: "worker", Status: daemon.StatusRunning, Runtime: "claude", RuntimeBinary: "claude", PID: 4242, SessionID: "team-stale-session", StartedAt: now.Add(-15 * time.Minute)},
		{Instance: "support-stale", Agent: "support", Status: daemon.StatusRunning, Runtime: "claude", RuntimeBinary: "claude", PID: 4242, SessionID: "foreign-stale-session", StartedAt: now.Add(-10 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "runtime", "resume-plan", "delivery", "--repo", root, "--status", "crashed", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team runtime resume-plan json: %v\nstderr=%s", err, stderr.String())
	}
	var plans []runtimeResumePlan
	if err := json.Unmarshal(out.Bytes(), &plans); err != nil {
		t.Fatalf("decode team runtime resume-plan: %v\nbody=%s", err, out.String())
	}
	if len(plans) != 2 || plans[0].Instance != "manager" || plans[1].Instance != "worker-squ-900" {
		t.Fatalf("plans = %+v, want manager and worker-squ-900 only", plans)
	}

	format := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	format.SetOut(formatOut)
	format.SetErr(formatErr)
	format.SetArgs([]string{"team", "runtime", "resume-plan", "delivery", "--repo", root, "--status", "crashed", "--runtime", "codex", "--action", "logs", "--format", "{{.Instance}} {{.Runtime}} {{.RecommendedAction}}"})
	if err := format.Execute(); err != nil {
		t.Fatalf("team runtime resume-plan format: %v\nstderr=%s", err, formatErr.String())
	}
	if got, want := strings.TrimSpace(formatOut.String()), "worker-squ-900 codex logs"; got != want {
		t.Fatalf("formatted team runtime resume-plan = %q, want %q", got, want)
	}

	summary := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(summaryOut)
	summary.SetErr(summaryErr)
	summary.SetArgs([]string{"team", "runtime", "resume-plan", "delivery", "--repo", root, "--status", "crashed", "--summary", "--json"})
	if err := summary.Execute(); err != nil {
		t.Fatalf("team runtime resume-plan summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var counts runtimeResumeSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &counts); err != nil {
		t.Fatalf("decode team runtime resume-plan summary: %v\nbody=%s", err, summaryOut.String())
	}
	if counts.Total != 2 || counts.Actions["logs"] != 2 || counts.Runtimes["claude"] != 1 || counts.Runtimes["codex"] != 1 || counts.Statuses["crashed"] != 2 || counts.ManagedResume != 1 || counts.CanManagedResume != 0 || counts.DirectResume != 0 {
		t.Fatalf("team resume-plan summary = %+v", counts)
	}

	stale := NewRootCmd()
	staleOut, staleErr := &bytes.Buffer{}, &bytes.Buffer{}
	stale.SetOut(staleOut)
	stale.SetErr(staleErr)
	stale.SetArgs([]string{"team", "runtime", "resume-plan", "delivery", "--repo", root, "--runtime-stale", "--format", "{{.Instance}} {{.Stale}} {{.RecommendedAction}}"})
	if err := stale.Execute(); err != nil {
		t.Fatalf("team runtime resume-plan runtime-stale filter: %v\nstderr=%s", err, staleErr.String())
	}
	if got, want := strings.TrimSpace(staleOut.String()), "worker-squ-902 true start"; got != want {
		t.Fatalf("team stale resume-plan = %q, want %q", got, want)
	}

	unhealthy := NewRootCmd()
	unhealthyOut, unhealthyErr := &bytes.Buffer{}, &bytes.Buffer{}
	unhealthy.SetOut(unhealthyOut)
	unhealthy.SetErr(unhealthyErr)
	unhealthy.SetArgs([]string{"team", "runtime", "resume-plan", "delivery", "--repo", root, "--unhealthy", "--format", "{{.Instance}} {{.RecommendedAction}} {{.Stale}}"})
	if err := unhealthy.Execute(); err != nil {
		t.Fatalf("team runtime resume-plan unhealthy filter: %v\nstderr=%s", err, unhealthyErr.String())
	}
	if got, want := strings.TrimSpace(unhealthyOut.String()), strings.Join([]string{
		"manager logs false",
		"worker-squ-900 logs false",
		"worker-squ-902 start true",
	}, "\n"); got != want {
		t.Fatalf("team unhealthy resume-plan = %q, want %q", got, want)
	}
}

func TestTeamStatusFiltersByRuntime(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"manager", "worker"} {
		if err := os.MkdirAll(filepath.Join(teamDir, "agents", name), 0o755); err != nil {
			t.Fatalf("mkdir agent %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"

[instances.worker]
agent = "worker"
ephemeral = true

[instances.build-worker]
agent = "worker"
ephemeral = true

[teams.delivery]
instances = ["manager", "worker"]

[teams.platform]
instances = ["build-worker"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "worker-squ-301", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
		{Instance: "build-worker-1", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(-time.Hour)},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "status", "delivery", "--repo", root, "--runtime", "codex", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team status runtime: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot teamStatusSnapshot
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode team status runtime: %v\nbody=%s", err, out.String())
	}
	if snapshot.InstanceSummary.Total != 1 || snapshot.InstanceSummary.Running != 1 {
		t.Fatalf("team status runtime summary = %+v", snapshot.InstanceSummary)
	}
	if got := psJSONRowNames(snapshot.Instances); strings.Join(got, ",") != "worker-squ-301" {
		t.Fatalf("team status runtime instances = %v", got)
	}
	if snapshot.Instances[0].Runtime != "codex" {
		t.Fatalf("team status runtime instance = %+v", snapshot.Instances[0])
	}
	if strings.Contains(out.String(), "build-worker-1") {
		t.Fatalf("team status runtime leaked unrelated instance:\n%s", out.String())
	}
	if containsString(snapshot.Actions, "agent-team team sync delivery --wait") {
		t.Fatalf("team status runtime should not suggest sync for filtered manager: %+v", snapshot.Actions)
	}

	badRuntime := NewRootCmd()
	badRuntime.SetOut(&bytes.Buffer{})
	badRuntimeErr := &bytes.Buffer{}
	badRuntime.SetErr(badRuntimeErr)
	badRuntime.SetArgs([]string{"team", "status", "delivery", "--repo", root, "--runtime", "llama"})
	if err := badRuntime.Execute(); err == nil {
		t.Fatal("team status accepted unknown runtime")
	}
	if !strings.Contains(badRuntimeErr.String(), "unknown --runtime") {
		t.Fatalf("bad runtime stderr = %q", badRuntimeErr.String())
	}
}

func TestTeamStatsScopesRows(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"

[instances.ticket-manager]
agent = "ticket-manager"

[instances.worker]
agent = "worker"
ephemeral = true

[instances.build-worker]
agent = "worker"
ephemeral = true

[instances.other]
agent = "other"

[teams.delivery]
instances = ["manager", "ticket-manager", "worker"]

[teams.platform]
instances = ["other", "build-worker"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(-3 * time.Minute)},
		{Instance: "worker-squ-101", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(-2 * time.Minute)},
		{Instance: "build-worker-1", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(-time.Minute)},
		{Instance: "other", Agent: "other", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusExited, Workspace: root, StartedAt: now.Add(-time.Minute), ExitedAt: now},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	running := NewRootCmd()
	runningOut, runningErr := &bytes.Buffer{}, &bytes.Buffer{}
	running.SetOut(runningOut)
	running.SetErr(runningErr)
	running.SetArgs([]string{"team", "stats", "delivery", "--repo", root, "--json"})
	if err := running.Execute(); err != nil {
		t.Fatalf("team stats running: %v\nstderr=%s", err, runningErr.String())
	}
	var rows []statsJSONRow
	if err := json.Unmarshal(runningOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode team stats running: %v\nbody=%s", err, runningOut.String())
	}
	if got := statsJSONRowNames(rows); strings.Join(got, ",") != "manager,worker-squ-101" {
		t.Fatalf("team stats running names = %v", got)
	}

	codex := NewRootCmd()
	codexOut, codexErr := &bytes.Buffer{}, &bytes.Buffer{}
	codex.SetOut(codexOut)
	codex.SetErr(codexErr)
	codex.SetArgs([]string{"team", "stats", "delivery", "--repo", root, "--runtime", "codex", "--json"})
	if err := codex.Execute(); err != nil {
		t.Fatalf("team stats runtime: %v\nstderr=%s", err, codexErr.String())
	}
	rows = nil
	if err := json.Unmarshal(codexOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode team stats runtime: %v\nbody=%s", err, codexOut.String())
	}
	if got := statsJSONRowNames(rows); strings.Join(got, ",") != "worker-squ-101" {
		t.Fatalf("team stats runtime names = %v", got)
	}
	if rows[0].Runtime != "codex" {
		t.Fatalf("team stats runtime row = %+v", rows[0])
	}

	badRuntime := NewRootCmd()
	badRuntime.SetOut(&bytes.Buffer{})
	badRuntimeErr := &bytes.Buffer{}
	badRuntime.SetErr(badRuntimeErr)
	badRuntime.SetArgs([]string{"team", "stats", "delivery", "--repo", root, "--runtime", "llama"})
	if err := badRuntime.Execute(); err == nil {
		t.Fatal("team stats accepted unknown runtime")
	}
	if !strings.Contains(badRuntimeErr.String(), "unknown --runtime") {
		t.Fatalf("bad runtime stderr = %q", badRuntimeErr.String())
	}

	all := NewRootCmd()
	allOut, allErr := &bytes.Buffer{}, &bytes.Buffer{}
	all.SetOut(allOut)
	all.SetErr(allErr)
	all.SetArgs([]string{"team", "stats", "delivery", "--repo", root, "--all", "--json"})
	if err := all.Execute(); err != nil {
		t.Fatalf("team stats all: %v\nstderr=%s", err, allErr.String())
	}
	rows = nil
	if err := json.Unmarshal(allOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode team stats all: %v\nbody=%s", err, allOut.String())
	}
	byInstance := map[string]statsJSONRow{}
	for _, row := range rows {
		byInstance[row.Instance] = row
	}
	for _, want := range []string{"manager", "ticket-manager", "worker-squ-101"} {
		if _, ok := byInstance[want]; !ok {
			t.Fatalf("team stats all rows = %+v, missing %s", rows, want)
		}
	}
	if byInstance["ticket-manager"].Status != "unknown" {
		t.Fatalf("ticket-manager row = %+v, want unknown", byInstance["ticket-manager"])
	}
	for _, unwanted := range []string{"build-worker-1", "other"} {
		if _, ok := byInstance[unwanted]; ok {
			t.Fatalf("team stats all rows = %+v, included %s", rows, unwanted)
		}
	}
}

func TestTeamLifecycleOutputFlagConflicts(t *testing.T) {
	for _, args := range [][]string{
		{"team", "up", "delivery", "--quiet", "--json"},
		{"team", "up", "delivery", "--tail", "10", "--dry-run"},
		{"team", "down", "delivery", "--quiet", "--json"},
		{"team", "restart", "delivery", "--quiet", "--json"},
		{"team", "sync", "delivery", "--quiet", "--json"},
		{"team", "sync", "delivery", "--format", "{{.Instance}}", "--json"},
		{"team", "sync", "delivery", "--dry-run", "--wait"},
		{"team", "plan", "delivery", "--format", "{{.Instance}}", "--json"},
		{"team", "queue", "delivery", "--format", "{{.ID}}", "--json"},
		{"team", "logs", "delivery", "--json"},
		{"team", "events", "delivery", "--format", "{{.Instance}}", "--json"},
		{"team", "events", "delivery", "--summary", "--follow"},
		{"team", "send", "delivery", "hello", "--format", "{{.To}}", "--json"},
		{"team", "send", "delivery", "hello", "--latest", "--last", "1"},
		{"team", "send", "delivery", "hello", "--last", "-1"},
		{"team", "wait", "delivery", "--quiet", "--json"},
		{"team", "wait", "delivery", "--summary", "--quiet"},
		{"team", "wait", "delivery", "--format", "{{.Instance}}", "--json"},
		{"team", "wait", "delivery", "manager", "--status", "running"},
		{"team", "wait", "delivery", "--latest", "--last", "1"},
		{"team", "wait", "delivery", "--last", "-1"},
		{"team", "prune", "delivery", "--quiet", "--summary"},
		{"team", "prune", "delivery", "--format", "{{.Instance}}", "--json"},
		{"team", "prune", "delivery", "--older-than=-1s"},
		{"team", "stats", "delivery", "--format", "{{.Instance}}", "--json"},
		{"team", "stats", "delivery", "--latest", "--last", "1"},
		{"team", "stats", "delivery", "manager", "--status", "running"},
		{"team", "stats", "delivery", "--last", "-1"},
		{"team", "snapshot", "delivery", "--json", "--output", "snapshot.json"},
		{"team", "snapshot", "delivery", "--events", "-2"},
		{"team", "snapshot", "delivery", "--schedule-limit", "-1"},
		{"team", "monitor", "delivery", "--format", "{{.Team.Name}}", "--json"},
		{"team", "monitor", "delivery", "--events", "-1"},
		{"team", "monitor", "delivery", "--since", "10m"},
		{"team", "monitor", "delivery", "--event-action", "dispatch"},
		{"team", "monitor", "delivery", "--stop-extras"},
		{"team", "monitor", "delivery", "--action", "start"},
		{"team", "monitor", "delivery", "--latest", "--last", "1"},
		{"team", "monitor", "delivery", "--last", "-1"},
		{"team", "monitor", "delivery", "--watch", "--interval", "-1s"},
		{"team", "run", "delivery", "SQU-CONFLICT", "--format", "{{.ID}}", "--json"},
	} {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("%v succeeded", args)
		}
		if strings.TrimSpace(stderr.String()) == "" {
			t.Fatalf("%v produced empty stderr", args)
		}
	}
}

func TestTeamSendScopesRecipients(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.build-worker]
agent = "worker"
ephemeral = true

[instances.other]
agent = "other"

[teams.delivery]
instances = ["manager", "worker"]

[teams.platform]
instances = ["other", "build-worker"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(time.Minute), Workspace: root},
		{Instance: "worker-squ-101", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(2 * time.Minute), Workspace: root},
		{Instance: "worker-squ-100", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusStopped, PID: os.Getpid(), StartedAt: now.Add(3 * time.Minute), Workspace: root},
		{Instance: "build-worker-1", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(4 * time.Minute), Workspace: root},
		{Instance: "other", Agent: "other", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(5 * time.Minute), Workspace: root},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"team", "send", "delivery", "--repo", root, "--dry-run", "--json", "hello", "team"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("team send dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryRows []sendJSON
	if err := json.Unmarshal(dryOut.Bytes(), &dryRows); err != nil {
		t.Fatalf("decode team send dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if got := sendTargets(dryRows); strings.Join(got, ",") != "manager,worker-squ-101" {
		t.Fatalf("team send dry-run targets = %v", got)
	}

	codex := NewRootCmd()
	codexOut, codexErr := &bytes.Buffer{}, &bytes.Buffer{}
	codex.SetOut(codexOut)
	codex.SetErr(codexErr)
	codex.SetArgs([]string{"team", "send", "delivery", "--repo", root, "--runtime", "codex", "--dry-run", "--json", "hello"})
	if err := codex.Execute(); err != nil {
		t.Fatalf("team send --runtime dry-run: %v\nstderr=%s", err, codexErr.String())
	}
	var codexRows []sendJSON
	if err := json.Unmarshal(codexOut.Bytes(), &codexRows); err != nil {
		t.Fatalf("decode team send --runtime: %v\nbody=%s", err, codexOut.String())
	}
	if got := sendTargets(codexRows); strings.Join(got, ",") != "worker-squ-101" {
		t.Fatalf("team send --runtime targets = %v", got)
	}

	badRuntime := NewRootCmd()
	badRuntime.SetOut(&bytes.Buffer{})
	badRuntimeErr := &bytes.Buffer{}
	badRuntime.SetErr(badRuntimeErr)
	badRuntime.SetArgs([]string{"team", "send", "delivery", "--repo", root, "--runtime", "llama", "hello"})
	if err := badRuntime.Execute(); err == nil {
		t.Fatal("team send accepted unknown runtime")
	}
	if !strings.Contains(badRuntimeErr.String(), "unknown --runtime") {
		t.Fatalf("bad runtime stderr = %q", badRuntimeErr.String())
	}

	allStatuses := NewRootCmd()
	allOut, allErr := &bytes.Buffer{}, &bytes.Buffer{}
	allStatuses.SetOut(allOut)
	allStatuses.SetErr(allErr)
	allStatuses.SetArgs([]string{"team", "send", "delivery", "--repo", root, "--all", "--dry-run", "--json", "hello"})
	if err := allStatuses.Execute(); err != nil {
		t.Fatalf("team send --all dry-run: %v\nstderr=%s", err, allErr.String())
	}
	var allRows []sendJSON
	if err := json.Unmarshal(allOut.Bytes(), &allRows); err != nil {
		t.Fatalf("decode team send --all: %v\nbody=%s", err, allOut.String())
	}
	if got := sendTargets(allRows); strings.Join(got, ",") != "manager,worker-squ-100,worker-squ-101" {
		t.Fatalf("team send --all targets = %v", got)
	}

	latest := NewRootCmd()
	latestOut, latestErr := &bytes.Buffer{}, &bytes.Buffer{}
	latest.SetOut(latestOut)
	latest.SetErr(latestErr)
	latest.SetArgs([]string{"team", "send", "delivery", "--repo", root, "--latest", "--dry-run", "--format", "{{.To}}", "ping"})
	if err := latest.Execute(); err != nil {
		t.Fatalf("team send latest: %v\nstderr=%s", err, latestErr.String())
	}
	if got := strings.TrimSpace(latestOut.String()); got != "worker-squ-101" {
		t.Fatalf("team send latest = %q", got)
	}

	send := NewRootCmd()
	sendOut, sendErr := &bytes.Buffer{}, &bytes.Buffer{}
	send.SetOut(sendOut)
	send.SetErr(sendErr)
	send.SetArgs([]string{"team", "send", "delivery", "--repo", root, "--from", "operator", "please", "sync"})
	if err := send.Execute(); err != nil {
		t.Fatalf("team send: %v\nstderr=%s", err, sendErr.String())
	}
	for _, instance := range []string{"manager", "worker-squ-101"} {
		messages, err := daemon.ReadMessages(daemon.DaemonRoot(teamDir), instance)
		if err != nil {
			t.Fatalf("read messages %s: %v", instance, err)
		}
		if len(messages) != 1 || messages[0].From != "operator" || messages[0].Body != "please sync" {
			t.Fatalf("messages %s = %+v", instance, messages)
		}
	}
	for _, instance := range []string{"worker-squ-100", "build-worker-1", "other"} {
		messages, err := daemon.ReadMessages(daemon.DaemonRoot(teamDir), instance)
		if err != nil {
			t.Fatalf("read messages %s: %v", instance, err)
		}
		if len(messages) != 0 {
			t.Fatalf("unexpected messages %s = %+v", instance, messages)
		}
	}

	messageFile := filepath.Join(root, "team-message.txt")
	if err := os.WriteFile(messageFile, []byte("file\nsync\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sendFile := NewRootCmd()
	sendFileOut, sendFileErr := &bytes.Buffer{}, &bytes.Buffer{}
	sendFile.SetOut(sendFileOut)
	sendFile.SetErr(sendFileErr)
	sendFile.SetArgs([]string{"team", "send", "delivery", "--repo", root, "--message-file", messageFile, "--format", "{{.To}}"})
	if err := sendFile.Execute(); err != nil {
		t.Fatalf("team send --message-file: %v\nstderr=%s", err, sendFileErr.String())
	}
	if got := strings.Split(strings.TrimSpace(sendFileOut.String()), "\n"); strings.Join(got, ",") != "manager,worker-squ-101" {
		t.Fatalf("team send --message-file targets = %q", sendFileOut.String())
	}
	for _, instance := range []string{"manager", "worker-squ-101"} {
		messages, err := daemon.ReadMessages(daemon.DaemonRoot(teamDir), instance)
		if err != nil {
			t.Fatalf("read messages %s: %v", instance, err)
		}
		if len(messages) != 2 || messages[1].Body != "file\nsync" {
			t.Fatalf("messages after file send %s = %+v", instance, messages)
		}
	}
}

func TestTeamSendRuntimeStaleScopesRecipients(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.build-worker]
agent = "worker"
ephemeral = true

[teams.delivery]
instances = ["manager", "worker"]

[teams.platform]
instances = ["build-worker"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, meta := range []*daemon.Metadata{
		{Instance: "worker-squ-101", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: 99999999, StartedAt: now.Add(-2 * time.Minute), Workspace: root},
		{Instance: "worker-squ-102", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(-time.Minute), Workspace: root},
		{Instance: "build-worker-1", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: 99999999, StartedAt: now, Workspace: root},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "send", "delivery", "--repo", root, "--runtime-stale", "--dry-run", "--json", "hello"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team send --runtime-stale dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var rows []sendJSON
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode team send --runtime-stale: %v\nbody=%s", err, out.String())
	}
	if got := sendTargets(rows); strings.Join(got, ",") != "worker-squ-101" {
		t.Fatalf("team send --runtime-stale targets = %v", got)
	}
}

func TestTeamEventsScopesLifecycleEvents(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.build-worker]
agent = "worker"
ephemeral = true

[instances.other]
agent = "other"

[teams.delivery]
instances = ["manager", "worker"]

[teams.platform]
instances = ["other", "build-worker"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	daemonRoot := daemon.DaemonRoot(teamDir)
	base := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: base},
		{Instance: "worker-squ-501", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusStopped, PID: os.Getpid(), Workspace: root, StartedAt: base.Add(2 * time.Minute), StoppedAt: base.Add(4 * time.Minute)},
		{Instance: "build-worker-1", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusStopped, PID: os.Getpid(), Workspace: root, StartedAt: base.Add(time.Minute), StoppedAt: base.Add(time.Minute)},
		{Instance: "other", Agent: "other", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusStopped, PID: os.Getpid(), Workspace: root, StartedAt: base.Add(3 * time.Minute), StoppedAt: base.Add(3 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(daemonRoot, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	for _, ev := range []*daemon.LifecycleEvent{
		{TS: base, Action: "start", Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, Message: "manager up"},
		{TS: base.Add(time.Minute), Action: "stop", Instance: "build-worker-1", Agent: "worker", Status: daemon.StatusStopped, Message: "platform stop"},
		{TS: base.Add(2 * time.Minute), Action: "dispatch", Instance: "worker-squ-501", Agent: "worker", Status: daemon.StatusRunning, Message: "delivery worker"},
		{TS: base.Add(3 * time.Minute), Action: "stop", Instance: "other", Agent: "other", Status: daemon.StatusStopped, Message: "other stop"},
		{TS: base.Add(4 * time.Minute), Action: "stop", Instance: "worker-squ-501", Agent: "worker", Status: daemon.StatusStopped, Message: "delivery done"},
	} {
		if err := daemon.AppendLifecycleEvent(daemonRoot, ev); err != nil {
			t.Fatalf("append event: %v", err)
		}
	}

	list := NewRootCmd()
	listOut, listErr := &bytes.Buffer{}, &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetErr(listErr)
	list.SetArgs([]string{"team", "events", "delivery", "--repo", root, "--json"})
	if err := list.Execute(); err != nil {
		t.Fatalf("team events json: %v\nstderr=%s", err, listErr.String())
	}
	events := decodeLifecycleEventJSONL(t, listOut.String())
	if got := lifecycleEventInstances(events); strings.Join(got, ",") != "manager,worker-squ-501,worker-squ-501" {
		t.Fatalf("team events instances = %v\nbody=%s", got, listOut.String())
	}

	formatted := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatted.SetOut(formatOut)
	formatted.SetErr(formatErr)
	formatted.SetArgs([]string{"team", "events", "delivery", "--repo", root, "--tail", "1", "--format", "{{.Instance}} {{.Action}}"})
	if err := formatted.Execute(); err != nil {
		t.Fatalf("team events format: %v\nstderr=%s", err, formatErr.String())
	}
	if got := strings.TrimSpace(formatOut.String()); got != "worker-squ-501 stop" {
		t.Fatalf("team events tail format = %q", got)
	}

	summary := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(summaryOut)
	summary.SetErr(summaryErr)
	summary.SetArgs([]string{"team", "events", "delivery", "--repo", root, "--summary", "--action", "stop", "--json"})
	if err := summary.Execute(); err != nil {
		t.Fatalf("team events summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var eventSummary eventSummaryJSON
	if err := json.Unmarshal(summaryOut.Bytes(), &eventSummary); err != nil {
		t.Fatalf("decode team events summary: %v\nbody=%s", err, summaryOut.String())
	}
	if eventSummary.Total != 1 || eventSummary.Actions["stop"] != 1 || eventSummary.Instances["worker-squ-501"] != 1 {
		t.Fatalf("team events summary = %+v", eventSummary)
	}

	codex := NewRootCmd()
	codexOut, codexErr := &bytes.Buffer{}, &bytes.Buffer{}
	codex.SetOut(codexOut)
	codex.SetErr(codexErr)
	codex.SetArgs([]string{"team", "events", "delivery", "--repo", root, "--runtime", "codex", "--json"})
	if err := codex.Execute(); err != nil {
		t.Fatalf("team events runtime: %v\nstderr=%s", err, codexErr.String())
	}
	events = decodeLifecycleEventJSONL(t, codexOut.String())
	if got := lifecycleEventInstances(events); strings.Join(got, ",") != "worker-squ-501,worker-squ-501" {
		t.Fatalf("team events runtime instances = %v\nbody=%s", got, codexOut.String())
	}
	if strings.Contains(codexOut.String(), "manager up") || strings.Contains(codexOut.String(), "build-worker-1") || strings.Contains(codexOut.String(), "other stop") {
		t.Fatalf("team events runtime leaked unrelated event:\n%s", codexOut.String())
	}

	badRuntime := NewRootCmd()
	badRuntime.SetOut(&bytes.Buffer{})
	badRuntimeErr := &bytes.Buffer{}
	badRuntime.SetErr(badRuntimeErr)
	badRuntime.SetArgs([]string{"team", "events", "delivery", "--repo", root, "--runtime", "llama"})
	if err := badRuntime.Execute(); err == nil {
		t.Fatal("team events accepted unknown runtime")
	}
	if !strings.Contains(badRuntimeErr.String(), "unknown --runtime") {
		t.Fatalf("bad runtime stderr = %q", badRuntimeErr.String())
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"team", "events", "delivery", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("team events text: %v\nstderr=%s", err, textErr.String())
	}
	if strings.Contains(textOut.String(), "build-worker-1") || strings.Contains(textOut.String(), "other stop") {
		t.Fatalf("team events text leaked unrelated event:\n%s", textOut.String())
	}
}

func TestTeamEventsCurrentStateFiltersScopeEphemeralChildren(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.build-worker]
agent = "worker"
ephemeral = true

[instances.other]
agent = "other"

[teams.delivery]
instances = ["manager", "worker"]

[teams.platform]
instances = ["other", "build-worker"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	daemonRoot := daemon.DaemonRoot(teamDir)
	now := time.Now().UTC()
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(-4 * time.Minute)},
		{Instance: "worker-squ-777", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: 99999999, Workspace: root, StartedAt: now.Add(-3 * time.Minute)},
		{Instance: "worker-squ-778", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(-2 * time.Minute)},
		{Instance: "build-worker-1", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: 99999999, Workspace: root, StartedAt: now.Add(-time.Minute)},
	} {
		if err := daemon.WriteMetadata(daemonRoot, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		if err := daemon.AppendLifecycleEvent(daemonRoot, &daemon.LifecycleEvent{
			TS:       meta.StartedAt,
			Action:   "dispatch",
			Instance: meta.Instance,
			Agent:    meta.Agent,
			Status:   meta.Status,
			Message:  meta.Instance,
		}); err != nil {
			t.Fatalf("append event %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `
[status]
phase = "idle"
description = "manager idle"
`, now)
	writeStatus(t, filepath.Join(teamDir, "state", "worker-squ-777"), `
[status]
phase = "blocked"
description = "blocked worker"
`, now)
	writeStatus(t, filepath.Join(teamDir, "state", "worker-squ-778"), `
[status]
phase = "idle"
description = "idle worker"
`, now)
	writeStatus(t, filepath.Join(teamDir, "state", "build-worker-1"), `
[status]
phase = "blocked"
description = "other team worker"
`, now)

	phase := NewRootCmd()
	phaseOut, phaseErr := &bytes.Buffer{}, &bytes.Buffer{}
	phase.SetOut(phaseOut)
	phase.SetErr(phaseErr)
	phase.SetArgs([]string{"team", "events", "delivery", "--repo", root, "--phase", "blocked", "--format", "{{.Instance}}"})
	if err := phase.Execute(); err != nil {
		t.Fatalf("team events phase filter: %v\nstderr=%s", err, phaseErr.String())
	}
	if got, want := phaseOut.String(), "worker-squ-777\n"; got != want {
		t.Fatalf("team events phase output = %q, want %q", got, want)
	}

	runtimeStale := NewRootCmd()
	runtimeStaleOut, runtimeStaleErr := &bytes.Buffer{}, &bytes.Buffer{}
	runtimeStale.SetOut(runtimeStaleOut)
	runtimeStale.SetErr(runtimeStaleErr)
	runtimeStale.SetArgs([]string{"team", "events", "delivery", "--repo", root, "--runtime-stale", "--format", "{{.Instance}}"})
	if err := runtimeStale.Execute(); err != nil {
		t.Fatalf("team events runtime-stale filter: %v\nstderr=%s", err, runtimeStaleErr.String())
	}
	if got, want := runtimeStaleOut.String(), "worker-squ-777\n"; got != want {
		t.Fatalf("team events runtime-stale output = %q, want %q", got, want)
	}
}

func TestTeamLogsScopesRowsAndStreams(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.build-worker]
agent = "worker"
ephemeral = true

[instances.other]
agent = "other"

[teams.delivery]
instances = ["manager", "worker"]

[teams.platform]
instances = ["other", "build-worker"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	daemonRoot := daemon.DaemonRoot(teamDir)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "worker-squ-201", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
		{Instance: "build-worker-1", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(-time.Hour)},
		{Instance: "other", Agent: "other", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(-time.Hour)},
	} {
		if err := daemon.WriteMetadata(daemonRoot, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeChildLogForTest(t, daemonRoot, "manager", "manager first\nmanager second\n")
	writeChildLogForTest(t, daemonRoot, "worker-squ-201", "worker first\nworker latest\n")
	writeChildLogForTest(t, daemonRoot, "build-worker-1", "build worker log\n")
	writeChildLogForTest(t, daemonRoot, "other", "other log\n")
	writeLastMessageForTest(t, teamDir, "manager", "manager final")
	writeLastMessageForTest(t, teamDir, "worker-squ-201", "worker final")
	writeLastMessageForTest(t, teamDir, "build-worker-1", "build worker final")
	writeLastMessageForTest(t, teamDir, "other", "other final")

	list := NewRootCmd()
	listOut, listErr := &bytes.Buffer{}, &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetErr(listErr)
	list.SetArgs([]string{"team", "logs", "delivery", "--repo", root, "--list", "--json"})
	if err := list.Execute(); err != nil {
		t.Fatalf("team logs list: %v\nstderr=%s", err, listErr.String())
	}
	var rows []logListRow
	if err := json.Unmarshal(listOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode team logs list: %v\nbody=%s", err, listOut.String())
	}
	if got := logRowInstances(rows); strings.Join(got, ",") != "manager,worker-squ-201" {
		t.Fatalf("team log rows = %v", got)
	}

	codexList := NewRootCmd()
	codexListOut, codexListErr := &bytes.Buffer{}, &bytes.Buffer{}
	codexList.SetOut(codexListOut)
	codexList.SetErr(codexListErr)
	codexList.SetArgs([]string{"team", "logs", "delivery", "--repo", root, "--runtime", "codex", "--list", "--json"})
	if err := codexList.Execute(); err != nil {
		t.Fatalf("team logs runtime list: %v\nstderr=%s", err, codexListErr.String())
	}
	rows = nil
	if err := json.Unmarshal(codexListOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode team logs runtime list: %v\nbody=%s", err, codexListOut.String())
	}
	if got := logRowInstances(rows); strings.Join(got, ",") != "worker-squ-201" {
		t.Fatalf("team runtime log rows = %v", got)
	}
	if rows[0].Runtime != "codex" {
		t.Fatalf("team runtime log row = %+v", rows[0])
	}

	formatted := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatted.SetOut(formatOut)
	formatted.SetErr(formatErr)
	formatted.SetArgs([]string{"team", "logs", "delivery", "--repo", root, "--list", "--format", "{{.Instance}} {{.Size}}"})
	if err := formatted.Execute(); err != nil {
		t.Fatalf("team logs format: %v\nstderr=%s", err, formatErr.String())
	}
	formatBody := formatOut.String()
	for _, want := range []string{"manager ", "worker-squ-201 "} {
		if !strings.Contains(formatBody, want) {
			t.Fatalf("team logs format missing %q:\n%s", want, formatBody)
		}
	}
	if strings.Contains(formatBody, "build-worker") || strings.Contains(formatBody, "other") {
		t.Fatalf("team logs format leaked unrelated rows:\n%s", formatBody)
	}

	logs := NewRootCmd()
	logsOut, logsErr := &bytes.Buffer{}, &bytes.Buffer{}
	logs.SetOut(logsOut)
	logs.SetErr(logsErr)
	logs.SetArgs([]string{"team", "logs", "delivery", "--repo", root, "--tail", "1"})
	if err := logs.Execute(); err != nil {
		t.Fatalf("team logs: %v\nstderr=%s", err, logsErr.String())
	}
	body := logsOut.String()
	for _, want := range []string{"manager              | manager second", "worker-squ-201       | worker latest"} {
		if !strings.Contains(body, want) {
			t.Fatalf("team logs missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "build worker") || strings.Contains(body, "other log") {
		t.Fatalf("team logs leaked unrelated content:\n%s", body)
	}

	runtimeLogs := NewRootCmd()
	runtimeLogsOut, runtimeLogsErr := &bytes.Buffer{}, &bytes.Buffer{}
	runtimeLogs.SetOut(runtimeLogsOut)
	runtimeLogs.SetErr(runtimeLogsErr)
	runtimeLogs.SetArgs([]string{"team", "logs", "delivery", "--repo", root, "--runtime", "codex", "--tail", "1"})
	if err := runtimeLogs.Execute(); err != nil {
		t.Fatalf("team logs runtime: %v\nstderr=%s", err, runtimeLogsErr.String())
	}
	if got := runtimeLogsOut.String(); got != "worker latest\n" {
		t.Fatalf("team logs runtime = %q", got)
	}

	latest := NewRootCmd()
	latestOut, latestErr := &bytes.Buffer{}, &bytes.Buffer{}
	latest.SetOut(latestOut)
	latest.SetErr(latestErr)
	latest.SetArgs([]string{"team", "logs", "delivery", "--repo", root, "--latest", "--tail", "1"})
	if err := latest.Execute(); err != nil {
		t.Fatalf("team logs latest: %v\nstderr=%s", err, latestErr.String())
	}
	if got := latestOut.String(); got != "worker latest\n" {
		t.Fatalf("team logs latest = %q", got)
	}

	lastMessages := NewRootCmd()
	lastOut, lastErr := &bytes.Buffer{}, &bytes.Buffer{}
	lastMessages.SetOut(lastOut)
	lastMessages.SetErr(lastErr)
	lastMessages.SetArgs([]string{"team", "logs", "delivery", "--repo", root, "--last-message"})
	if err := lastMessages.Execute(); err != nil {
		t.Fatalf("team logs last-message: %v\nstderr=%s", err, lastErr.String())
	}
	lastBody := lastOut.String()
	for _, want := range []string{"manager              | manager final", "worker-squ-201       | worker final"} {
		if !strings.Contains(lastBody, want) {
			t.Fatalf("team last-message missing %q:\n%s", want, lastBody)
		}
	}
	if strings.Contains(lastBody, "build worker final") || strings.Contains(lastBody, "other final") {
		t.Fatalf("team last-message leaked unrelated content:\n%s", lastBody)
	}

	runtimeLast := NewRootCmd()
	runtimeLastOut, runtimeLastErr := &bytes.Buffer{}, &bytes.Buffer{}
	runtimeLast.SetOut(runtimeLastOut)
	runtimeLast.SetErr(runtimeLastErr)
	runtimeLast.SetArgs([]string{"team", "logs", "delivery", "--repo", root, "--runtime", "codex", "--last-message"})
	if err := runtimeLast.Execute(); err != nil {
		t.Fatalf("team logs runtime last-message: %v\nstderr=%s", err, runtimeLastErr.String())
	}
	if got := runtimeLastOut.String(); got != "worker final\n" {
		t.Fatalf("team logs runtime last-message = %q", got)
	}

	latestLast := NewRootCmd()
	latestLastOut, latestLastErr := &bytes.Buffer{}, &bytes.Buffer{}
	latestLast.SetOut(latestLastOut)
	latestLast.SetErr(latestLastErr)
	latestLast.SetArgs([]string{"team", "logs", "delivery", "--repo", root, "--latest", "--last-message"})
	if err := latestLast.Execute(); err != nil {
		t.Fatalf("team logs latest last-message: %v\nstderr=%s", err, latestLastErr.String())
	}
	if got := latestLastOut.String(); got != "worker final\n" {
		t.Fatalf("team logs latest last-message = %q", got)
	}

	badRuntime := NewRootCmd()
	badRuntime.SetOut(&bytes.Buffer{})
	badRuntimeErr := &bytes.Buffer{}
	badRuntime.SetErr(badRuntimeErr)
	badRuntime.SetArgs([]string{"team", "logs", "delivery", "--repo", root, "--runtime", "llama", "--list"})
	if err := badRuntime.Execute(); err == nil {
		t.Fatal("team logs accepted unknown runtime")
	}
	if !strings.Contains(badRuntimeErr.String(), "unknown --runtime") {
		t.Fatalf("bad runtime stderr = %q", badRuntimeErr.String())
	}

	conflict := NewRootCmd()
	conflictErr := &bytes.Buffer{}
	conflict.SetOut(&bytes.Buffer{})
	conflict.SetErr(conflictErr)
	conflict.SetArgs([]string{"team", "logs", "delivery", "--repo", root, "--last-message", "--grep", "final"})
	err := conflict.Execute()
	if err == nil {
		t.Fatalf("team logs last-message with grep succeeded")
	}
	var code ExitCode
	if !errors.As(err, &code) || code != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if !strings.Contains(conflictErr.String(), "--last-message cannot be combined with --grep") {
		t.Fatalf("stderr = %q, want grep validation", conflictErr.String())
	}
}

func TestTeamQueueScopesItems(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.other]
agent = "other"

[teams.delivery]
instances = ["manager", "worker"]

[teams.platform]
instances = ["other"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	teamJob := &job.Job{
		ID:        "squ-501",
		Ticket:    "SQU-501",
		Target:    "worker",
		Status:    job.StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, teamJob); err != nil {
		t.Fatalf("write team job: %v", err)
	}
	otherJob := &job.Job{
		ID:        "oth-1",
		Ticket:    "OTH-1",
		Target:    "other",
		Status:    job.StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, otherJob); err != nil {
		t.Fatalf("write other job: %v", err)
	}
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-team-job",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-501",
			Payload:    map[string]any{"job_id": "squ-501", "target": "worker", "runtime": "codex"},
			Attempts:   daemon.MaxQueueAttempts,
			LastError:  "spawn failed",
			QueuedAt:   now.Add(-time.Hour),
			UpdatedAt:  now,
		},
		{
			ID:         "q-team-claude",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-claude",
			Payload:    map[string]any{"target": "worker", "runtime": "claude"},
			Attempts:   daemon.MaxQueueAttempts,
			LastError:  "spawn failed",
			QueuedAt:   now.Add(-30 * time.Minute),
			UpdatedAt:  now,
		},
		{
			ID:         "q-team-target",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-direct",
			Payload:    map[string]any{"target": "worker", "runtime": "codex"},
			QueuedAt:   now,
			UpdatedAt:  now,
		},
		{
			ID:         "q-other-job",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "other",
			InstanceID: "other-oth-1",
			Payload:    map[string]any{"job_id": "oth-1", "target": "other", "runtime": "codex"},
			QueuedAt:   now,
			UpdatedAt:  now,
		},
		{
			ID:         "q-other-target",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "other",
			InstanceID: "other-direct",
			Payload:    map[string]any{"target": "other", "runtime": "codex"},
			QueuedAt:   now,
			UpdatedAt:  now,
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("write queue item %s: %v", item.ID, err)
		}
	}
	writeQuarantinedQueueItem(t, teamDir, "20260619T010000.000000000Z", daemon.QueueStateDead, &daemon.QueueItem{
		ID:         "q-team-quarantined",
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-501",
		Payload:    map[string]any{"job_id": "squ-501", "target": "worker"},
		QueuedAt:   now.Add(-2 * time.Hour),
		UpdatedAt:  now.Add(-2 * time.Hour),
	})
	writeQuarantinedQueueItem(t, teamDir, "20260619T010000.000000000Z", daemon.QueueStateDead, &daemon.QueueItem{
		ID:         "q-team-unrestorable",
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-501",
		Payload:    map[string]any{"job_id": "squ-501", "target": "worker"},
		UpdatedAt:  now.Add(-2 * time.Hour),
	})
	writeQuarantinedQueueItem(t, teamDir, "20260619T010000.000000000Z", daemon.QueueStateDead, &daemon.QueueItem{
		ID:         "q-other-quarantined",
		EventType:  "agent.dispatch",
		Instance:   "other",
		InstanceID: "other-oth-1",
		Payload:    map[string]any{"job_id": "oth-1", "target": "other"},
		QueuedAt:   now.Add(-2 * time.Hour),
		UpdatedAt:  now.Add(-2 * time.Hour),
	})

	list := NewRootCmd()
	listOut, listErr := &bytes.Buffer{}, &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetErr(listErr)
	list.SetArgs([]string{"team", "queue", "delivery", "--repo", root, "--json"})
	if err := list.Execute(); err != nil {
		t.Fatalf("team queue: %v\nstderr=%s", err, listErr.String())
	}
	var items []daemon.QueueItem
	if err := json.Unmarshal(listOut.Bytes(), &items); err != nil {
		t.Fatalf("decode team queue: %v\nbody=%s", err, listOut.String())
	}
	if got := queueItemIDs(items); strings.Join(got, ",") != "q-team-job,q-team-claude,q-team-target" {
		t.Fatalf("team queue ids = %v", got)
	}

	sorted := NewRootCmd()
	sortedOut, sortedErr := &bytes.Buffer{}, &bytes.Buffer{}
	sorted.SetOut(sortedOut)
	sorted.SetErr(sortedErr)
	sorted.SetArgs([]string{"team", "queue", "delivery", "--repo", root, "--sort", "runtime", "--limit", "1", "--format", "{{.ID}}"})
	if err := sorted.Execute(); err != nil {
		t.Fatalf("team queue sort/limit: %v\nstderr=%s", err, sortedErr.String())
	}
	if got := strings.TrimSpace(sortedOut.String()); got != "q-team-claude" {
		t.Fatalf("team queue sort/limit output = %q", sortedOut.String())
	}

	textList := NewRootCmd()
	textListOut, textListErr := &bytes.Buffer{}, &bytes.Buffer{}
	textList.SetOut(textListOut)
	textList.SetErr(textListErr)
	textList.SetArgs([]string{"team", "queue", "delivery", "--repo", root})
	if err := textList.Execute(); err != nil {
		t.Fatalf("team queue text: %v\nstderr=%s", err, textListErr.String())
	}
	for _, want := range []string{
		"agent-team job queue retry squ-501 q-team-job; agent-team job queue drop squ-501 q-team-job",
		"agent-team team queue retry delivery q-team-claude; agent-team team queue drop delivery q-team-claude",
		"agent-team team drain delivery; agent-team team queue drop delivery q-team-target",
	} {
		if !strings.Contains(textListOut.String(), want) {
			t.Fatalf("team queue text missing %q:\n%s", want, textListOut.String())
		}
	}

	runtimeList := NewRootCmd()
	runtimeListOut, runtimeListErr := &bytes.Buffer{}, &bytes.Buffer{}
	runtimeList.SetOut(runtimeListOut)
	runtimeList.SetErr(runtimeListErr)
	runtimeList.SetArgs([]string{"team", "queue", "delivery", "--repo", root, "--runtime", "codex", "--json"})
	if err := runtimeList.Execute(); err != nil {
		t.Fatalf("team queue runtime filter: %v\nstderr=%s", err, runtimeListErr.String())
	}
	var runtimeItems []daemon.QueueItem
	if err := json.Unmarshal(runtimeListOut.Bytes(), &runtimeItems); err != nil {
		t.Fatalf("decode team queue runtime filter: %v\nbody=%s", err, runtimeListOut.String())
	}
	if got := queueItemIDs(runtimeItems); strings.Join(got, ",") != "q-team-job,q-team-target" {
		t.Fatalf("team queue runtime-filtered ids = %v", got)
	}

	showText := NewRootCmd()
	showTextOut, showTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	showText.SetOut(showTextOut)
	showText.SetErr(showTextErr)
	showText.SetArgs([]string{"team", "queue", "show", "delivery", "q-team-claude", "--repo", root})
	if err := showText.Execute(); err != nil {
		t.Fatalf("team queue show text: %v\nstderr=%s", err, showTextErr.String())
	}
	for _, want := range []string{"Runtime:     claude", "agent-team team queue retry delivery q-team-claude", "agent-team team queue drop delivery q-team-claude"} {
		if !strings.Contains(showTextOut.String(), want) {
			t.Fatalf("team queue show missing %q:\n%s", want, showTextOut.String())
		}
	}

	showOther := NewRootCmd()
	showOtherOut, showOtherErr := &bytes.Buffer{}, &bytes.Buffer{}
	showOther.SetOut(showOtherOut)
	showOther.SetErr(showOtherErr)
	showOther.SetArgs([]string{"team", "queue", "show", "delivery", "q-other-target", "--repo", root, "--json"})
	if err := showOther.Execute(); err == nil {
		t.Fatalf("team queue show unrelated item unexpectedly succeeded: stdout=%s", showOtherOut.String())
	}
	if !strings.Contains(showOtherErr.String(), "not owned by team") {
		t.Fatalf("team queue show unrelated stderr = %q", showOtherErr.String())
	}

	summary := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(summaryOut)
	summary.SetErr(summaryErr)
	summary.SetArgs([]string{"team", "queue", "delivery", "--repo", root, "--state", "dead", "--summary", "--json"})
	if err := summary.Execute(); err != nil {
		t.Fatalf("team queue summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var queueSummaryResult queueSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &queueSummaryResult); err != nil {
		t.Fatalf("decode queue summary: %v\nbody=%s", err, summaryOut.String())
	}
	if queueSummaryResult.Total != 2 || queueSummaryResult.Dead != 2 || queueSummaryResult.Quarantined != 2 || queueSummaryResult.QuarantineRestorable != 1 || queueSummaryResult.QuarantineUnrestorable != 1 || queueSummaryResult.Instances["worker"] != 2 || queueSummaryResult.Runtimes["codex"] != 1 || queueSummaryResult.Runtimes["claude"] != 1 {
		t.Fatalf("queue summary = %+v", queueSummaryResult)
	}

	runtimeSummaryCmd := NewRootCmd()
	runtimeSummaryOut, runtimeSummaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	runtimeSummaryCmd.SetOut(runtimeSummaryOut)
	runtimeSummaryCmd.SetErr(runtimeSummaryErr)
	runtimeSummaryCmd.SetArgs([]string{"team", "queue", "delivery", "--repo", root, "--state", "dead", "--runtime", "codex", "--summary", "--json"})
	if err := runtimeSummaryCmd.Execute(); err != nil {
		t.Fatalf("team queue runtime summary: %v\nstderr=%s", err, runtimeSummaryErr.String())
	}
	var runtimeSummary queueSummary
	if err := json.Unmarshal(runtimeSummaryOut.Bytes(), &runtimeSummary); err != nil {
		t.Fatalf("decode team queue runtime summary: %v\nbody=%s", err, runtimeSummaryOut.String())
	}
	if runtimeSummary.Total != 1 || runtimeSummary.Dead != 1 || runtimeSummary.Quarantined != 0 || runtimeSummary.Runtimes["codex"] != 1 {
		t.Fatalf("runtime queue summary = %+v", runtimeSummary)
	}

	quarantine := NewRootCmd()
	quarantineOut, quarantineErr := &bytes.Buffer{}, &bytes.Buffer{}
	quarantine.SetOut(quarantineOut)
	quarantine.SetErr(quarantineErr)
	quarantine.SetArgs([]string{"team", "queue", "quarantine", "delivery", "--repo", root, "--state", "dead", "--job", "SQU-501", "--restorable", "--json"})
	if err := quarantine.Execute(); err != nil {
		t.Fatalf("team queue quarantine: %v\nstderr=%s", err, quarantineErr.String())
	}
	var quarantineItems []queueQuarantineItem
	if err := json.Unmarshal(quarantineOut.Bytes(), &quarantineItems); err != nil {
		t.Fatalf("decode team queue quarantine: %v\nbody=%s", err, quarantineOut.String())
	}
	if len(quarantineItems) != 1 || quarantineItems[0].ID != "q-team-quarantined" || quarantineItems[0].Job != "squ-501" {
		t.Fatalf("team queue quarantine items = %+v", quarantineItems)
	}
	teamQuarantinePath := quarantineItems[0].Path
	otherQuarantinePath := filepath.Join("quarantine", "20260619T010000.000000000Z", daemon.QueueStateDead, "q-other-quarantined.json")

	quarantineFormat := NewRootCmd()
	quarantineFormatOut, quarantineFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	quarantineFormat.SetOut(quarantineFormatOut)
	quarantineFormat.SetErr(quarantineFormatErr)
	quarantineFormat.SetArgs([]string{"team", "queue", "quarantine", "delivery", "--repo", root, "--format", "{{.ID}} {{.State}} {{.Restorable}}"})
	if err := quarantineFormat.Execute(); err != nil {
		t.Fatalf("team queue quarantine format: %v\nstderr=%s", err, quarantineFormatErr.String())
	}
	if !strings.Contains(quarantineFormatOut.String(), "q-team-quarantined dead true") || !strings.Contains(quarantineFormatOut.String(), "q-team-unrestorable dead false") || strings.Contains(quarantineFormatOut.String(), "q-other-quarantined") {
		t.Fatalf("team queue quarantine format =\n%s", quarantineFormatOut.String())
	}

	unrestorable := NewRootCmd()
	unrestorableOut, unrestorableErr := &bytes.Buffer{}, &bytes.Buffer{}
	unrestorable.SetOut(unrestorableOut)
	unrestorable.SetErr(unrestorableErr)
	unrestorable.SetArgs([]string{"team", "queue", "quarantine", "delivery", "--repo", root, "--unrestorable", "--json"})
	if err := unrestorable.Execute(); err != nil {
		t.Fatalf("team queue quarantine unrestorable: %v\nstderr=%s", err, unrestorableErr.String())
	}
	var unrestorableItems []queueQuarantineItem
	if err := json.Unmarshal(unrestorableOut.Bytes(), &unrestorableItems); err != nil {
		t.Fatalf("decode team queue quarantine unrestorable: %v\nbody=%s", err, unrestorableOut.String())
	}
	if len(unrestorableItems) != 1 || unrestorableItems[0].ID != "q-team-unrestorable" || unrestorableItems[0].Restorable {
		t.Fatalf("team unrestorable quarantine items = %+v", unrestorableItems)
	}

	quarantineText := NewRootCmd()
	quarantineTextOut, quarantineTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	quarantineText.SetOut(quarantineTextOut)
	quarantineText.SetErr(quarantineTextErr)
	quarantineText.SetArgs([]string{"team", "queue", "quarantine", "delivery", "--repo", root})
	if err := quarantineText.Execute(); err != nil {
		t.Fatalf("team queue quarantine text: %v\nstderr=%s", err, quarantineTextErr.String())
	}
	if !strings.Contains(quarantineTextOut.String(), "q-team-quarantined") || !strings.Contains(quarantineTextOut.String(), "q-team-unrestorable") || strings.Contains(quarantineTextOut.String(), "q-other-quarantined") {
		t.Fatalf("team queue quarantine text =\n%s", quarantineTextOut.String())
	}

	showQuarantine := NewRootCmd()
	showQuarantineOut, showQuarantineErr := &bytes.Buffer{}, &bytes.Buffer{}
	showQuarantine.SetOut(showQuarantineOut)
	showQuarantine.SetErr(showQuarantineErr)
	showQuarantine.SetArgs([]string{"team", "queue", "quarantine", "show", "delivery", teamQuarantinePath, "--repo", root, "--json"})
	if err := showQuarantine.Execute(); err != nil {
		t.Fatalf("team queue quarantine show: %v\nstderr=%s", err, showQuarantineErr.String())
	}
	var shownQuarantine queueQuarantineShowResult
	if err := json.Unmarshal(showQuarantineOut.Bytes(), &shownQuarantine); err != nil {
		t.Fatalf("decode team queue quarantine show: %v\nbody=%s", err, showQuarantineOut.String())
	}
	if shownQuarantine.Team != "delivery" || shownQuarantine.ID != "q-team-quarantined" || shownQuarantine.QueueItem == nil {
		t.Fatalf("shown team queue quarantine = %+v", shownQuarantine)
	}

	showQuarantineFormat := NewRootCmd()
	showQuarantineFormatOut, showQuarantineFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	showQuarantineFormat.SetOut(showQuarantineFormatOut)
	showQuarantineFormat.SetErr(showQuarantineFormatErr)
	showQuarantineFormat.SetArgs([]string{"team", "queue", "quarantine", "show", "delivery", teamQuarantinePath, "--repo", root, "--format", "{{.Team}} {{.ID}} {{.QueueItem.Instance}}"})
	if err := showQuarantineFormat.Execute(); err != nil {
		t.Fatalf("team queue quarantine show format: %v\nstderr=%s", err, showQuarantineFormatErr.String())
	}
	if showQuarantineFormatOut.String() != "delivery q-team-quarantined worker\n" {
		t.Fatalf("team queue quarantine show format = %q", showQuarantineFormatOut.String())
	}

	showQuarantineText := NewRootCmd()
	showQuarantineTextOut, showQuarantineTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	showQuarantineText.SetOut(showQuarantineTextOut)
	showQuarantineText.SetErr(showQuarantineTextErr)
	showQuarantineText.SetArgs([]string{"team", "queue", "quarantine", "show", "delivery", teamQuarantinePath, "--repo", root})
	if err := showQuarantineText.Execute(); err != nil {
		t.Fatalf("team queue quarantine show text: %v\nstderr=%s", err, showQuarantineTextErr.String())
	}
	if !strings.Contains(showQuarantineTextOut.String(), "agent-team team queue quarantine restore delivery") || strings.Contains(showQuarantineTextOut.String(), "q-other-quarantined") {
		t.Fatalf("team queue quarantine show text =\n%s", showQuarantineTextOut.String())
	}

	restoreAllDry := NewRootCmd()
	restoreAllDryOut, restoreAllDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	restoreAllDry.SetOut(restoreAllDryOut)
	restoreAllDry.SetErr(restoreAllDryErr)
	restoreAllDry.SetArgs([]string{"team", "queue", "quarantine", "restore", "delivery", "--repo", root, "--all", "--job", "SQU-501", "--state", "dead", "--dry-run", "--json"})
	if err := restoreAllDry.Execute(); err != nil {
		t.Fatalf("team queue quarantine restore --all dry-run: %v\nstderr=%s", err, restoreAllDryErr.String())
	}
	var restoreAllResults []queueQuarantineRestoreResult
	if err := json.Unmarshal(restoreAllDryOut.Bytes(), &restoreAllResults); err != nil {
		t.Fatalf("decode team queue quarantine restore --all dry-run: %v\nbody=%s", err, restoreAllDryOut.String())
	}
	if len(restoreAllResults) != 1 || restoreAllResults[0].ID != "q-team-quarantined" || restoreAllResults[0].Action != "would_restore" || !restoreAllResults[0].DryRun {
		t.Fatalf("team restore --all dry-run results = %+v", restoreAllResults)
	}

	restoreAllFormat := NewRootCmd()
	restoreAllFormatOut, restoreAllFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	restoreAllFormat.SetOut(restoreAllFormatOut)
	restoreAllFormat.SetErr(restoreAllFormatErr)
	restoreAllFormat.SetArgs([]string{"team", "queue", "quarantine", "restore", "delivery", "--repo", root, "--all", "--job", "SQU-501", "--dry-run", "--format", "{{.ID}} {{.Action}} {{.DryRun}}"})
	if err := restoreAllFormat.Execute(); err != nil {
		t.Fatalf("team queue quarantine restore --all format: %v\nstderr=%s", err, restoreAllFormatErr.String())
	}
	if restoreAllFormatOut.String() != "q-team-quarantined would_restore true\n" {
		t.Fatalf("team queue quarantine restore --all format = %q", restoreAllFormatOut.String())
	}

	restoreFilterPath := NewRootCmd()
	restoreFilterPathOut, restoreFilterPathErr := &bytes.Buffer{}, &bytes.Buffer{}
	restoreFilterPath.SetOut(restoreFilterPathOut)
	restoreFilterPath.SetErr(restoreFilterPathErr)
	restoreFilterPath.SetArgs([]string{"team", "queue", "quarantine", "restore", "delivery", teamQuarantinePath, "--repo", root, "--job", "SQU-501", "--dry-run"})
	if err := restoreFilterPath.Execute(); err == nil {
		t.Fatalf("team queue quarantine restore path with filter succeeded: stdout=%s", restoreFilterPathOut.String())
	}
	if !strings.Contains(restoreFilterPathErr.String(), "filters require --all") {
		t.Fatalf("restore path filter stderr = %q", restoreFilterPathErr.String())
	}

	restoreDry := NewRootCmd()
	restoreDryOut, restoreDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	restoreDry.SetOut(restoreDryOut)
	restoreDry.SetErr(restoreDryErr)
	restoreDry.SetArgs([]string{"team", "queue", "quarantine", "restore", "delivery", teamQuarantinePath, "--repo", root, "--dry-run", "--json"})
	if err := restoreDry.Execute(); err != nil {
		t.Fatalf("team queue quarantine restore dry-run: %v\nstderr=%s", err, restoreDryErr.String())
	}
	var restoreResult queueQuarantineRestoreResult
	if err := json.Unmarshal(restoreDryOut.Bytes(), &restoreResult); err != nil {
		t.Fatalf("decode team queue quarantine restore dry-run: %v\nbody=%s", err, restoreDryOut.String())
	}
	if restoreResult.ID != "q-team-quarantined" || restoreResult.Action != "would_restore" || !restoreResult.DryRun {
		t.Fatalf("restore result = %+v", restoreResult)
	}

	restoreOther := NewRootCmd()
	restoreOtherOut, restoreOtherErr := &bytes.Buffer{}, &bytes.Buffer{}
	restoreOther.SetOut(restoreOtherOut)
	restoreOther.SetErr(restoreOtherErr)
	restoreOther.SetArgs([]string{"team", "queue", "quarantine", "restore", "delivery", otherQuarantinePath, "--repo", root, "--dry-run"})
	if err := restoreOther.Execute(); err == nil {
		t.Fatal("team queue quarantine restore unrelated item unexpectedly succeeded")
	}
	if !strings.Contains(restoreOtherErr.String(), "not owned by team") {
		t.Fatalf("restore unrelated stderr = %q stdout=%q", restoreOtherErr.String(), restoreOtherOut.String())
	}

	dropDry := NewRootCmd()
	dropDryOut, dropDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropDry.SetOut(dropDryOut)
	dropDry.SetErr(dropDryErr)
	dropDry.SetArgs([]string{"team", "queue", "quarantine", "drop", "delivery", teamQuarantinePath, "--repo", root, "--dry-run", "--json"})
	if err := dropDry.Execute(); err != nil {
		t.Fatalf("team queue quarantine drop dry-run: %v\nstderr=%s", err, dropDryErr.String())
	}
	var dropResults []queueQuarantineDropResult
	if err := json.Unmarshal(dropDryOut.Bytes(), &dropResults); err != nil {
		t.Fatalf("decode team queue quarantine drop dry-run: %v\nbody=%s", err, dropDryOut.String())
	}
	if len(dropResults) != 1 || dropResults[0].ID != "q-team-quarantined" || dropResults[0].Action != "would_drop" || !dropResults[0].DryRun {
		t.Fatalf("drop dry-run results = %+v", dropResults)
	}

	dropFormat := NewRootCmd()
	dropFormatOut, dropFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropFormat.SetOut(dropFormatOut)
	dropFormat.SetErr(dropFormatErr)
	dropFormat.SetArgs([]string{"team", "queue", "quarantine", "drop", "delivery", teamQuarantinePath, "--repo", root, "--dry-run", "--format", "{{.ID}} {{.Action}} {{.DryRun}}"})
	if err := dropFormat.Execute(); err != nil {
		t.Fatalf("team queue quarantine drop format: %v\nstderr=%s", err, dropFormatErr.String())
	}
	if dropFormatOut.String() != "q-team-quarantined would_drop true\n" {
		t.Fatalf("team queue quarantine drop format = %q", dropFormatOut.String())
	}

	filterDropDry := NewRootCmd()
	filterDropDryOut, filterDropDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	filterDropDry.SetOut(filterDropDryOut)
	filterDropDry.SetErr(filterDropDryErr)
	filterDropDry.SetArgs([]string{"team", "queue", "quarantine", "drop", "delivery", "--repo", root, "--all", "--job", "SQU-501", "--state", "dead", "--unrestorable", "--dry-run", "--json"})
	if err := filterDropDry.Execute(); err != nil {
		t.Fatalf("team queue quarantine drop filtered --all dry-run: %v\nstderr=%s", err, filterDropDryErr.String())
	}
	var filterDropResults []queueQuarantineDropResult
	if err := json.Unmarshal(filterDropDryOut.Bytes(), &filterDropResults); err != nil {
		t.Fatalf("decode filtered team queue quarantine drop --all dry-run: %v\nbody=%s", err, filterDropDryOut.String())
	}
	if len(filterDropResults) != 1 || filterDropResults[0].ID != "q-team-unrestorable" || filterDropResults[0].Restorable {
		t.Fatalf("filtered drop --all dry-run results = %+v", filterDropResults)
	}

	filterPathDrop := NewRootCmd()
	filterPathDropOut, filterPathDropErr := &bytes.Buffer{}, &bytes.Buffer{}
	filterPathDrop.SetOut(filterPathDropOut)
	filterPathDrop.SetErr(filterPathDropErr)
	filterPathDrop.SetArgs([]string{"team", "queue", "quarantine", "drop", "delivery", teamQuarantinePath, "--repo", root, "--job", "SQU-501", "--dry-run"})
	if err := filterPathDrop.Execute(); err == nil {
		t.Fatalf("team queue quarantine drop path with filter succeeded: stdout=%s", filterPathDropOut.String())
	}
	if !strings.Contains(filterPathDropErr.String(), "filters require --all") {
		t.Fatalf("path filter stderr = %q", filterPathDropErr.String())
	}

	dropAllDry := NewRootCmd()
	dropAllDryOut, dropAllDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropAllDry.SetOut(dropAllDryOut)
	dropAllDry.SetErr(dropAllDryErr)
	dropAllDry.SetArgs([]string{"team", "queue", "quarantine", "drop", "delivery", "--repo", root, "--all", "--dry-run", "--json"})
	if err := dropAllDry.Execute(); err != nil {
		t.Fatalf("team queue quarantine drop --all dry-run: %v\nstderr=%s", err, dropAllDryErr.String())
	}
	var dropAllResults []queueQuarantineDropResult
	if err := json.Unmarshal(dropAllDryOut.Bytes(), &dropAllResults); err != nil {
		t.Fatalf("decode team queue quarantine drop --all dry-run: %v\nbody=%s", err, dropAllDryOut.String())
	}
	if len(dropAllResults) != 2 || queueQuarantineDropIDs(dropAllResults) != "q-team-quarantined,q-team-unrestorable" {
		t.Fatalf("drop --all dry-run results = %+v", dropAllResults)
	}

	jobFiltered := NewRootCmd()
	jobOut, jobErr := &bytes.Buffer{}, &bytes.Buffer{}
	jobFiltered.SetOut(jobOut)
	jobFiltered.SetErr(jobErr)
	jobFiltered.SetArgs([]string{"team", "queue", "delivery", "--repo", root, "--job", "SQU-501", "--json"})
	if err := jobFiltered.Execute(); err != nil {
		t.Fatalf("team queue job filter: %v\nstderr=%s", err, jobErr.String())
	}
	var jobItems []daemon.QueueItem
	if err := json.Unmarshal(jobOut.Bytes(), &jobItems); err != nil {
		t.Fatalf("decode team queue job filter: %v\nbody=%s", err, jobOut.String())
	}
	if got := queueItemIDs(jobItems); strings.Join(got, ",") != "q-team-job" {
		t.Fatalf("team queue job-filtered ids = %v", got)
	}

	formatted := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatted.SetOut(formatOut)
	formatted.SetErr(formatErr)
	formatted.SetArgs([]string{"team", "queue", "delivery", "--repo", root, "--format", "{{.ID}} {{.State}}"})
	if err := formatted.Execute(); err != nil {
		t.Fatalf("team queue format: %v\nstderr=%s", err, formatErr.String())
	}
	formatBody := formatOut.String()
	for _, want := range []string{"q-team-job dead", "q-team-target pending"} {
		if !strings.Contains(formatBody, want) {
			t.Fatalf("team queue format missing %q:\n%s", want, formatBody)
		}
	}
	if strings.Contains(formatBody, "q-other") {
		t.Fatalf("team queue format leaked unrelated item:\n%s", formatBody)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"team", "queue", "delivery", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("team queue text: %v\nstderr=%s", err, textErr.String())
	}
	if !strings.Contains(textOut.String(), "q-team-job") || strings.Contains(textOut.String(), "q-other") {
		t.Fatalf("team queue text =\n%s", textOut.String())
	}

	retryDry := NewRootCmd()
	retryDryOut, retryDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	retryDry.SetOut(retryDryOut)
	retryDry.SetErr(retryDryErr)
	retryDry.SetArgs([]string{"team", "queue", "retry", "delivery", "--repo", root, "--all", "--job", "SQU-501", "--runtime", "codex", "--dry-run", "--json"})
	if err := retryDry.Execute(); err != nil {
		t.Fatalf("team queue retry --all dry-run: %v\nstderr=%s", err, retryDryErr.String())
	}
	var retryDryResults []queueRetryResult
	if err := json.Unmarshal(retryDryOut.Bytes(), &retryDryResults); err != nil {
		t.Fatalf("decode team queue retry dry-run: %v\nbody=%s", err, retryDryOut.String())
	}
	if len(retryDryResults) != 1 || retryDryResults[0].ID != "q-team-job" || retryDryResults[0].Action != "would_retry" || !retryDryResults[0].DryRun {
		t.Fatalf("retry dry-run results = %+v", retryDryResults)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-team-job"); err != nil || item.State != daemon.QueueStateDead {
		t.Fatalf("retry dry-run changed item=%+v err=%v", item, err)
	}

	retryRuntimeDry := NewRootCmd()
	retryRuntimeDryOut, retryRuntimeDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	retryRuntimeDry.SetOut(retryRuntimeDryOut)
	retryRuntimeDry.SetErr(retryRuntimeDryErr)
	retryRuntimeDry.SetArgs([]string{"team", "queue", "retry", "delivery", "--repo", root, "--all", "--runtime", "codex", "--dry-run", "--json"})
	if err := retryRuntimeDry.Execute(); err != nil {
		t.Fatalf("team queue retry --all runtime dry-run: %v\nstderr=%s", err, retryRuntimeDryErr.String())
	}
	var retryRuntimeResults []queueRetryResult
	if err := json.Unmarshal(retryRuntimeDryOut.Bytes(), &retryRuntimeResults); err != nil {
		t.Fatalf("decode team queue retry runtime dry-run: %v\nbody=%s", err, retryRuntimeDryOut.String())
	}
	if len(retryRuntimeResults) != 1 || retryRuntimeResults[0].ID != "q-team-job" {
		t.Fatalf("retry runtime dry-run results = %+v", retryRuntimeResults)
	}

	retryDryFormat := NewRootCmd()
	retryDryFormatOut, retryDryFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	retryDryFormat.SetOut(retryDryFormatOut)
	retryDryFormat.SetErr(retryDryFormatErr)
	retryDryFormat.SetArgs([]string{"team", "queue", "retry", "delivery", "--repo", root, "--all", "--job", "SQU-501", "--runtime", "codex", "--dry-run", "--format", "{{.ID}} {{.Action}} {{.DryRun}}"})
	if err := retryDryFormat.Execute(); err != nil {
		t.Fatalf("team queue retry --all dry-run format: %v\nstderr=%s", err, retryDryFormatErr.String())
	}
	if got, want := retryDryFormatOut.String(), "q-team-job would_retry true\n"; got != want {
		t.Fatalf("team queue retry dry-run format = %q, want %q", got, want)
	}

	otherRetry := NewRootCmd()
	otherRetryOut, otherRetryErr := &bytes.Buffer{}, &bytes.Buffer{}
	otherRetry.SetOut(otherRetryOut)
	otherRetry.SetErr(otherRetryErr)
	otherRetry.SetArgs([]string{"team", "queue", "retry", "delivery", "--repo", root, "q-other-job", "--dry-run", "--json"})
	if err := otherRetry.Execute(); err == nil {
		t.Fatal("team queue retry unrelated item unexpectedly succeeded")
	}
	if !strings.Contains(otherRetryErr.String(), "not owned by team") {
		t.Fatalf("team queue retry unrelated stderr = %q stdout=%q", otherRetryErr.String(), otherRetryOut.String())
	}

	retryApply := NewRootCmd()
	retryApplyOut, retryApplyErr := &bytes.Buffer{}, &bytes.Buffer{}
	retryApply.SetOut(retryApplyOut)
	retryApply.SetErr(retryApplyErr)
	retryApply.SetArgs([]string{"team", "queue", "retry", "delivery", "--repo", root, "q-team-job", "--json"})
	if err := retryApply.Execute(); err != nil {
		t.Fatalf("team queue retry single: %v\nstderr=%s", err, retryApplyErr.String())
	}
	var retried daemon.QueueItem
	if err := json.Unmarshal(retryApplyOut.Bytes(), &retried); err != nil {
		t.Fatalf("decode team queue retry single: %v\nbody=%s", err, retryApplyOut.String())
	}
	if retried.ID != "q-team-job" || retried.State != daemon.QueueStatePending || retried.LastError != "" {
		t.Fatalf("retried item = %+v", retried)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-other-job"); err != nil || item.State != daemon.QueueStateDead {
		t.Fatalf("unrelated retry item changed=%+v err=%v", item, err)
	}

	dropReady := NewRootCmd()
	dropReadyOut, dropReadyErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropReady.SetOut(dropReadyOut)
	dropReady.SetErr(dropReadyErr)
	dropReady.SetArgs([]string{"team", "queue", "drop", "delivery", "--repo", root, "--all", "--ready", "--runtime", "codex", "--dry-run", "--json"})
	if err := dropReady.Execute(); err != nil {
		t.Fatalf("team queue drop --all ready dry-run: %v\nstderr=%s", err, dropReadyErr.String())
	}
	var dropReadyResults []queueDropResult
	if err := json.Unmarshal(dropReadyOut.Bytes(), &dropReadyResults); err != nil {
		t.Fatalf("decode team queue drop ready dry-run: %v\nbody=%s", err, dropReadyOut.String())
	}
	dropReadyIDs := map[string]bool{}
	for _, result := range dropReadyResults {
		dropReadyIDs[result.ID] = true
		if result.Action != "would_drop" || !result.DryRun {
			t.Fatalf("drop ready result = %+v, want dry-run would_drop", result)
		}
	}
	if !dropReadyIDs["q-team-job"] || !dropReadyIDs["q-team-target"] || dropReadyIDs["q-other-target"] {
		t.Fatalf("drop ready results = %+v", dropReadyResults)
	}

	dropReadyFormat := NewRootCmd()
	dropReadyFormatOut, dropReadyFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropReadyFormat.SetOut(dropReadyFormatOut)
	dropReadyFormat.SetErr(dropReadyFormatErr)
	dropReadyFormat.SetArgs([]string{"team", "queue", "drop", "delivery", "--repo", root, "--all", "--ready", "--runtime", "codex", "--dry-run", "--format", "{{.ID}} {{.Action}} {{.DryRun}}"})
	if err := dropReadyFormat.Execute(); err != nil {
		t.Fatalf("team queue drop --all ready dry-run format: %v\nstderr=%s", err, dropReadyFormatErr.String())
	}
	dropFormatLines := strings.Split(strings.TrimSpace(dropReadyFormatOut.String()), "\n")
	if got := strings.Join(dropFormatLines, ","); got != "q-team-job would_drop true,q-team-target would_drop true" {
		t.Fatalf("team queue drop ready dry-run format = %q", dropReadyFormatOut.String())
	}

	dropApply := NewRootCmd()
	dropApplyOut, dropApplyErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropApply.SetOut(dropApplyOut)
	dropApply.SetErr(dropApplyErr)
	dropApply.SetArgs([]string{"team", "queue", "drop", "delivery", "--repo", root, "q-team-target", "--json"})
	if err := dropApply.Execute(); err != nil {
		t.Fatalf("team queue drop single: %v\nstderr=%s", err, dropApplyErr.String())
	}
	var dropped map[string]any
	if err := json.Unmarshal(dropApplyOut.Bytes(), &dropped); err != nil {
		t.Fatalf("decode team queue drop single: %v\nbody=%s", err, dropApplyOut.String())
	}
	if dropped["dropped"] != true || dropped["id"] != "q-team-target" || dropped["team"] != "delivery" {
		t.Fatalf("dropped result = %+v", dropped)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-team-target"); !os.IsNotExist(err) {
		t.Fatalf("team queue target still exists or unexpected err=%v", err)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-other-target"); err != nil || item.State != daemon.QueueStatePending {
		t.Fatalf("unrelated drop item changed=%+v err=%v", item, err)
	}
}

func TestTeamQueuePruneScopesItems(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.other]
agent = "other"

[teams.delivery]
instances = ["manager", "worker"]

[teams.platform]
instances = ["other"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, item := range []*daemon.QueueItem{
		{
			ID:             "q-team-old",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     "worker-squ-700",
			Payload:        map[string]any{"target": "worker", "ticket": "SQU-700"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:      now.Add(-47 * time.Hour),
			DeadLetteredAt: now.Add(-47 * time.Hour),
		},
		{
			ID:             "q-team-new",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     "worker-squ-701",
			Payload:        map[string]any{"target": "worker", "ticket": "SQU-701"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-time.Hour),
			UpdatedAt:      now.Add(-time.Hour),
			DeadLetteredAt: now.Add(-time.Hour),
		},
		{
			ID:         "q-team-pending",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-702",
			Payload:    map[string]any{"target": "worker", "ticket": "SQU-702"},
			QueuedAt:   now.Add(-48 * time.Hour),
			UpdatedAt:  now.Add(-48 * time.Hour),
		},
		{
			ID:             "q-other-old",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "other",
			InstanceID:     "other-oth-700",
			Payload:        map[string]any{"target": "other", "ticket": "OTH-700"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:      now.Add(-47 * time.Hour),
			DeadLetteredAt: now.Add(-47 * time.Hour),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("write queue item %s: %v", item.ID, err)
		}
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"team", "queue", "prune", "delivery", "--repo", root, "--older-than", "24h", "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("team queue prune dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryResults []queuePruneResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryResults); err != nil {
		t.Fatalf("decode team queue prune dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(dryResults) != 1 || dryResults[0].ID != "q-team-old" || !dryResults[0].DryRun || dryResults[0].Dropped {
		t.Fatalf("team queue prune dry-run results = %+v", dryResults)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-team-old"); err != nil {
		t.Fatalf("dry-run removed team queue item: %v", err)
	}

	prune := NewRootCmd()
	pruneOut, pruneErr := &bytes.Buffer{}, &bytes.Buffer{}
	prune.SetOut(pruneOut)
	prune.SetErr(pruneErr)
	prune.SetArgs([]string{"team", "queue", "prune", "delivery", "--repo", root, "--older-than", "24h", "--format", "{{.ID}} {{.State}} {{.Dropped}}"})
	if err := prune.Execute(); err != nil {
		t.Fatalf("team queue prune: %v\nstderr=%s", err, pruneErr.String())
	}
	if got, want := strings.TrimSpace(pruneOut.String()), "q-team-old dead true"; got != want {
		t.Fatalf("team queue prune output = %q, want %q", got, want)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-team-old"); !os.IsNotExist(err) {
		t.Fatalf("old team queue item err=%v, want not exist", err)
	}
	for _, id := range []string{"q-team-new", "q-team-pending", "q-other-old"} {
		if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), id); err != nil {
			t.Fatalf("queue item %s should remain: %v", id, err)
		}
	}

	pruneAll := NewRootCmd()
	allOut, allErr := &bytes.Buffer{}, &bytes.Buffer{}
	pruneAll.SetOut(allOut)
	pruneAll.SetErr(allErr)
	pruneAll.SetArgs([]string{"team", "queue", "prune", "delivery", "--repo", root, "--state", "all", "--older-than", "24h", "--dry-run", "--format", "{{.ID}} {{.DryRun}}"})
	if err := pruneAll.Execute(); err != nil {
		t.Fatalf("team queue prune state all dry-run: %v\nstderr=%s", err, allErr.String())
	}
	if got, want := strings.TrimSpace(allOut.String()), "q-team-pending true"; got != want {
		t.Fatalf("team queue prune all output = %q, want %q", got, want)
	}
}

func TestTeamQueuePruneRuntimeFiltersItems(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.other]
agent = "other"

[teams.delivery]
instances = ["manager", "worker"]

[teams.platform]
instances = ["other"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-team-codex",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-710",
			Payload: map[string]any{
				"runtime": "codex",
				"target":  "worker",
				"ticket":  "SQU-710",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:      now.Add(-47 * time.Hour),
			DeadLetteredAt: now.Add(-47 * time.Hour),
		},
		{
			ID:         "q-team-claude",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-711",
			Payload: map[string]any{
				"runtime": "claude",
				"target":  "worker",
				"ticket":  "SQU-711",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:      now.Add(-47 * time.Hour),
			DeadLetteredAt: now.Add(-47 * time.Hour),
		},
		{
			ID:         "q-other-codex",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "other",
			InstanceID: "other-oth-710",
			Payload: map[string]any{
				"runtime": "codex",
				"target":  "other",
				"ticket":  "OTH-710",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:      now.Add(-47 * time.Hour),
			DeadLetteredAt: now.Add(-47 * time.Hour),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("write queue item %s: %v", item.ID, err)
		}
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"team", "queue", "prune", "delivery", "--repo", root, "--older-than", "24h", "--runtime", "codex", "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("team queue prune runtime dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryResults []queuePruneResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryResults); err != nil {
		t.Fatalf("decode team runtime prune dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(dryResults) != 1 || dryResults[0].ID != "q-team-codex" || !dryResults[0].DryRun || dryResults[0].Dropped {
		t.Fatalf("team runtime dry-run results = %+v", dryResults)
	}

	prune := NewRootCmd()
	pruneOut, pruneErr := &bytes.Buffer{}, &bytes.Buffer{}
	prune.SetOut(pruneOut)
	prune.SetErr(pruneErr)
	prune.SetArgs([]string{"team", "queue", "prune", "delivery", "--repo", root, "--older-than", "24h", "--runtime", "codex", "--json"})
	if err := prune.Execute(); err != nil {
		t.Fatalf("team queue prune runtime: %v\nstderr=%s", err, pruneErr.String())
	}
	var pruneResults []queuePruneResult
	if err := json.Unmarshal(pruneOut.Bytes(), &pruneResults); err != nil {
		t.Fatalf("decode team runtime prune: %v\nbody=%s", err, pruneOut.String())
	}
	if len(pruneResults) != 1 || pruneResults[0].ID != "q-team-codex" || !pruneResults[0].Dropped {
		t.Fatalf("team runtime prune results = %+v", pruneResults)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-team-codex"); !os.IsNotExist(err) {
		t.Fatalf("codex team item err=%v, want not exist", err)
	}
	for _, id := range []string{"q-team-claude", "q-other-codex"} {
		if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), id); err != nil {
			t.Fatalf("queue item %s should remain: %v", id, err)
		}
	}
}

func TestTeamQueueRetryDropRejectsFormatCombinations(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "retry format with json",
			args: []string{"team", "queue", "retry", "delivery", "--format", "{{.ID}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "retry invalid format",
			args: []string{"team", "queue", "retry", "delivery", "--format", "{{"},
			want: "invalid --format template",
		},
		{
			name: "drop format with json",
			args: []string{"team", "queue", "drop", "delivery", "--format", "{{.ID}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "drop invalid format",
			args: []string{"team", "queue", "drop", "delivery", "--format", "{{"},
			want: "invalid --format template",
		},
		{
			name: "quarantine format with json",
			args: []string{"team", "queue", "quarantine", "delivery", "--format", "{{.ID}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "quarantine invalid format",
			args: []string{"team", "queue", "quarantine", "delivery", "--format", "{{"},
			want: "invalid --format template",
		},
		{
			name: "quarantine show format with json",
			args: []string{"team", "queue", "quarantine", "show", "delivery", "quarantine/20260619T000000.000000000Z/dead/q.json", "--format", "{{.ID}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "quarantine restore invalid format",
			args: []string{"team", "queue", "quarantine", "restore", "delivery", "quarantine/20260619T000000.000000000Z/dead/q.json", "--format", "{{"},
			want: "invalid --format template",
		},
		{
			name: "quarantine drop format with json",
			args: []string{"team", "queue", "quarantine", "drop", "delivery", "quarantine/20260619T000000.000000000Z/dead/q.json", "--format", "{{.ID}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "prune format with json",
			args: []string{"team", "queue", "prune", "delivery", "--format", "{{.ID}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "prune invalid format",
			args: []string{"team", "queue", "prune", "delivery", "--format", "{{"},
			want: "invalid --format template",
		},
		{
			name: "prune negative older than",
			args: []string{"team", "queue", "prune", "delivery", "--older-than", "-1s"},
			want: "--older-than must be >= 0",
		},
		{
			name: "prune invalid state",
			args: []string{"team", "queue", "prune", "delivery", "--state", "active"},
			want: "--state must be dead, pending, or all",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("team queue format validation succeeded: stdout=%s", out.String())
			}
			var code ExitCode
			if !errors.As(err, &code) || int(code) != 2 {
				t.Fatalf("team queue err = %v, want exit code 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestTeamSnapshotScopesDiagnostics(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.other]
agent = "other"
ephemeral = true
replicas = 1

[[instances.other.triggers]]
event = "agent.dispatch"
match.target = "other"

[instances.build-worker]
agent = "worker"
ephemeral = true

[[instances.build-worker.triggers]]
event = "agent.dispatch"
match.target = "build-worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[pipelines.platform_work]
trigger.event = "ticket.created"
trigger.match.team = "platform"

[[pipelines.platform_work.steps]]
id = "implement"
target = "other"

[schedules.delivery_due]
every = "24h"
payload.target = "worker"
payload.access_token = "delivery-secret"

[schedules.platform_due]
every = "24h"
payload.target = "other"
payload.access_token = "platform-secret"

[teams.delivery]
description = "Delivery team"
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
schedules = ["delivery_due"]

[teams.platform]
instances = ["other", "build-worker"]
pipelines = ["platform_work"]
schedules = ["platform_due"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-701",
			Ticket:    "SQU-701",
			Target:    "worker",
			Kickoff:   "SQU-701: implement",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusBlocked},
			},
		},
		{
			ID:        "oth-701",
			Ticket:    "OTH-701",
			Target:    "other",
			Kickoff:   "OTH-701: implement",
			Pipeline:  "platform_work",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "other", Status: job.StatusBlocked},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "worker-squ-701"), `[status]
phase = "blocked"
description = "waiting on review"
since = "2026-06-18T12:00:00Z"

[work]
job = "squ-701"
ticket = "SQU-701"
branch = "worker-squ-701"
`, now)
	writeStatus(t, filepath.Join(teamDir, "state", "other-oth-701"), `[status]
phase = "blocked"
description = "unrelated"
since = "2026-06-18T12:00:00Z"

[work]
job = "oth-701"
ticket = "OTH-701"
branch = "other-oth-701"
`, now)
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-delivery-snapshot",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-701",
			Payload: map[string]any{
				"job_id":       "squ-701",
				"target":       "worker",
				"ticket":       "SQU-701",
				"access_token": "queue-secret",
			},
			QueuedAt:  now.Add(-time.Minute),
			UpdatedAt: now.Add(-time.Minute),
		},
		{
			ID:         "q-platform-snapshot",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "other",
			InstanceID: "other-oth-701",
			Payload:    map[string]any{"job_id": "oth-701", "target": "other", "ticket": "OTH-701"},
			QueuedAt:   now.Add(-time.Minute),
			UpdatedAt:  now.Add(-time.Minute),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("write queue item %s: %v", item.ID, err)
		}
	}
	writeQuarantinedQueueItem(t, teamDir, "20260619T000000.000000000Z", daemon.QueueStatePending, &daemon.QueueItem{
		ID:         "q-delivery-quarantined",
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-701",
		Payload:    map[string]any{"job_id": "squ-701", "target": "worker", "ticket": "SQU-701"},
		QueuedAt:   now.Add(-2 * time.Minute),
		UpdatedAt:  now.Add(-2 * time.Minute),
	})
	writeQuarantinedQueueItem(t, teamDir, "20260619T000000.000000000Z", daemon.QueueStatePending, &daemon.QueueItem{
		ID:         "q-platform-quarantined",
		EventType:  "agent.dispatch",
		Instance:   "other",
		InstanceID: "other-oth-701",
		Payload:    map[string]any{"job_id": "oth-701", "target": "other", "ticket": "OTH-701"},
		QueuedAt:   now.Add(-2 * time.Minute),
		UpdatedAt:  now.Add(-2 * time.Minute),
	})
	daemonRoot := daemon.DaemonRoot(teamDir)
	for _, ev := range []*daemon.LifecycleEvent{
		{TS: now.Add(-3 * time.Minute), Action: "start", Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, Message: "manager up"},
		{TS: now.Add(-2 * time.Minute), Action: "dispatch", Instance: "worker-squ-701", Agent: "worker", Status: daemon.StatusRunning, Message: "delivery worker"},
		{TS: now.Add(-time.Minute), Action: "dispatch", Instance: "other-oth-701", Agent: "other", Status: daemon.StatusRunning, Message: "platform worker"},
	} {
		if err := daemon.AppendLifecycleEvent(daemonRoot, ev); err != nil {
			t.Fatalf("append event: %v", err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "snapshot", "delivery", "--repo", root, "--events", "-1", "--schedule-limit", "0", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team snapshot json: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot snapshotResult
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode team snapshot: %v\nbody=%s", err, out.String())
	}
	if snapshot.Team == nil || snapshot.Team.Name != "delivery" {
		t.Fatalf("team metadata = %+v", snapshot.Team)
	}
	if snapshot.Overview == nil || snapshot.Overview.Team == nil || snapshot.Overview.Team.Name != "delivery" || snapshot.Next == nil || snapshot.Next.Team == nil || snapshot.Next.Team.Name != "delivery" {
		t.Fatalf("team overview/next missing: overview=%+v next=%+v", snapshot.Overview, snapshot.Next)
	}
	if len(snapshot.Next.ActionDetails) == 0 {
		t.Fatalf("team next action details missing: %+v", snapshot.Next)
	}
	for _, detail := range snapshot.Next.ActionDetails {
		if detail.Team != "delivery" {
			t.Fatalf("team snapshot next detail is not scoped: %+v", detail)
		}
	}
	if !snapshot.Redacted {
		t.Fatalf("snapshot should redact by default")
	}
	if len(snapshot.Jobs) != 1 || snapshot.Jobs[0].ID != "squ-701" {
		t.Fatalf("snapshot jobs = %+v", snapshot.Jobs)
	}
	if len(snapshot.Queue) != 1 || snapshot.Queue[0].ID != "q-delivery-snapshot" || snapshot.QueueSummary == nil || snapshot.QueueSummary.Total != 1 || snapshot.QueueSummary.Quarantined != 1 || snapshot.QueueSummary.QuarantineRestorable != 1 || snapshot.QueueSummary.QuarantineUnrestorable != 0 {
		t.Fatalf("snapshot queue = %+v summary=%+v", snapshot.Queue, snapshot.QueueSummary)
	}
	if len(snapshot.QueueQuarantine) != 1 || snapshot.QueueQuarantine[0].ID != "q-delivery-quarantined" || snapshot.QueueQuarantine[0].Job != "squ-701" {
		t.Fatalf("snapshot queue quarantine = %+v", snapshot.QueueQuarantine)
	}
	if snapshot.Queue[0].Payload["access_token"] != snapshotRedactedValue {
		t.Fatalf("queue payload not redacted: %+v", snapshot.Queue[0].Payload)
	}
	if len(snapshot.Schedules) != 1 || snapshot.Schedules[0].Name != "delivery_due" || snapshot.Schedules[0].Payload["access_token"] != snapshotRedactedValue {
		t.Fatalf("snapshot schedules = %+v", snapshot.Schedules)
	}
	if len(snapshot.ScheduleNext) != 1 || snapshot.ScheduleNext[0].Name != "delivery_due" {
		t.Fatalf("snapshot schedule next = %+v", snapshot.ScheduleNext)
	}
	if len(snapshot.PipelineStatus) != 1 || snapshot.PipelineStatus[0].Pipeline != "ticket_to_pr" || snapshot.PipelineStatus[0].ReadySteps != 1 {
		t.Fatalf("snapshot pipeline status = %+v", snapshot.PipelineStatus)
	}
	if len(snapshot.PipelineExplain) != 1 || snapshot.PipelineExplain[0].Pipeline != "ticket_to_pr" || snapshot.PipelineExplain[0].ExplainedJobs != 1 || len(snapshot.PipelineExplain[0].Jobs) != 1 || snapshot.PipelineExplain[0].Jobs[0].JobID != "squ-701" {
		t.Fatalf("snapshot pipeline explain = %+v", snapshot.PipelineExplain)
	}
	if len(snapshot.PipelineAdvance) != 1 || snapshot.PipelineAdvance[0].JobID != "squ-701" || snapshot.PipelineAdvance[0].Pipeline != "ticket_to_pr" {
		t.Fatalf("snapshot pipeline advance = %+v", snapshot.PipelineAdvance)
	}
	if snapshot.TeamDoctor == nil || !snapshot.TeamDoctor.OK || snapshot.TeamDoctor.Team.Name != "delivery" {
		t.Fatalf("snapshot team doctor = %+v", snapshot.TeamDoctor)
	}
	if snapshot.JobTriage == nil || snapshot.JobTriage.Summary.Total != 1 || len(snapshot.JobTriage.ReadySteps) != 1 {
		t.Fatalf("snapshot job triage = %+v", snapshot.JobTriage)
	}
	if len(snapshot.JobStatus) != 1 || snapshot.JobStatus[0].JobID != "squ-701" || !snapshot.JobStatus[0].Changed {
		t.Fatalf("snapshot job status = %+v", snapshot.JobStatus)
	}
	if got := lifecycleEventInstances(snapshot.Events); strings.Join(got, ",") != "manager,worker-squ-701" {
		t.Fatalf("snapshot events = %v\nbody=%s", got, out.String())
	}
	body := out.String()
	for _, leak := range []string{"platform_due", "platform_work", "oth-701", "q-platform-snapshot", "q-platform-quarantined", "platform worker", "platform-secret"} {
		if strings.Contains(body, leak) {
			t.Fatalf("team snapshot json leaked %q:\n%s", leak, body)
		}
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"team", "snapshot", "delivery", "--repo", root, "--events", "0"})
	if err := text.Execute(); err != nil {
		t.Fatalf("team snapshot text: %v\nstderr=%s", err, textErr.String())
	}
	textBody := textOut.String()
	for _, want := range []string{"team: delivery", "next: state=", "jobs: total=1", "queue: total=1 pending=1 dead=0 delayed=0 attempts=0 quarantined=1 restorable=1 unrestorable=0", "pipeline status: pipelines=1", "pipeline explain: pipelines=1 jobs=1 steps=1", "team doctor: problems=0 warnings=1", "events: 0"} {
		if !strings.Contains(textBody, want) {
			t.Fatalf("team snapshot text missing %q:\n%s", want, textBody)
		}
	}
	for _, leak := range []string{"platform_due", "platform_work", "oth-701", "q-platform-snapshot", "q-platform-quarantined"} {
		if strings.Contains(textBody, leak) {
			t.Fatalf("team snapshot text leaked %q:\n%s", leak, textBody)
		}
	}
}

func TestTeamMonitorScopesDiagnostics(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.other]
agent = "other"
ephemeral = true
replicas = 1

[[instances.other.triggers]]
event = "agent.dispatch"
match.target = "other"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[pipelines.platform_work]
trigger.event = "ticket.created"
trigger.match.team = "platform"

[[pipelines.platform_work.steps]]
id = "implement"
target = "other"

[schedules.delivery_due]
every = "24h"
payload.target = "worker"

[schedules.platform_due]
every = "24h"
payload.target = "other"

[teams.delivery]
description = "Delivery team"
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
schedules = ["delivery_due"]

[teams.platform]
instances = ["other"]
pipelines = ["platform_work"]
schedules = ["platform_due"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-702",
			Ticket:    "SQU-702",
			Target:    "worker",
			Kickoff:   "SQU-702: implement",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusBlocked},
			},
		},
		{
			ID:        "oth-702",
			Ticket:    "OTH-702",
			Target:    "other",
			Kickoff:   "OTH-702: implement",
			Pipeline:  "platform_work",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "other", Status: job.StatusBlocked},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "worker-squ-702"), `[status]
phase = "blocked"
description = "waiting on review"
since = "2026-06-18T12:00:00Z"

[work]
job = "squ-702"
ticket = "SQU-702"
branch = "worker-squ-702"
`, now)
	writeStatus(t, filepath.Join(teamDir, "state", "other-oth-702"), `[status]
phase = "blocked"
description = "unrelated"
since = "2026-06-18T12:00:00Z"

[work]
job = "oth-702"
ticket = "OTH-702"
branch = "other-oth-702"
`, now)
	writeStatus(t, filepath.Join(teamDir, "state", "build-worker-1"), `[status]
phase = "implementing"
description = "platform build"
since = "2026-06-18T12:00:00Z"
`, now)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(-3 * time.Minute)},
		{Instance: "worker-squ-702", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(-2 * time.Minute)},
		{Instance: "other-oth-702", Agent: "other", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(-time.Minute)},
		{Instance: "build-worker-1", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(-time.Minute)},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-delivery-monitor",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-702",
			Payload:    map[string]any{"job_id": "squ-702", "target": "worker", "ticket": "SQU-702"},
			QueuedAt:   now.Add(-time.Minute),
			UpdatedAt:  now.Add(-time.Minute),
		},
		{
			ID:         "q-platform-monitor",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "other",
			InstanceID: "other-oth-702",
			Payload:    map[string]any{"job_id": "oth-702", "target": "other", "ticket": "OTH-702"},
			QueuedAt:   now.Add(-time.Minute),
			UpdatedAt:  now.Add(-time.Minute),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("write queue item %s: %v", item.ID, err)
		}
	}
	daemonRoot := daemon.DaemonRoot(teamDir)
	for _, ev := range []*daemon.LifecycleEvent{
		{TS: now.Add(-3 * time.Minute), Action: "start", Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, Message: "manager up"},
		{TS: now.Add(-2 * time.Minute), Action: "dispatch", Instance: "worker-squ-702", Agent: "worker", Status: daemon.StatusRunning, Message: "delivery worker"},
		{TS: now.Add(-time.Minute), Action: "dispatch", Instance: "other-oth-702", Agent: "other", Status: daemon.StatusRunning, Message: "platform worker"},
	} {
		if err := daemon.AppendLifecycleEvent(daemonRoot, ev); err != nil {
			t.Fatalf("append event: %v", err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "monitor", "delivery", "--repo", root, "--all", "--plan", "--jobs", "--schedules", "--events", "10", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team monitor json: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot monitorSnapshot
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode team monitor: %v\nbody=%s", err, out.String())
	}
	if snapshot.Team == nil || snapshot.Team.Name != "delivery" {
		t.Fatalf("team metadata = %+v", snapshot.Team)
	}
	if snapshot.Health == nil || snapshot.Health.Jobs != nil || snapshot.Health.Queue.Total != 1 {
		t.Fatalf("health = %+v", snapshot.Health)
	}
	if len(snapshot.Instances) != 2 || snapshot.Instances[0].Instance == "other-oth-702" || snapshot.Instances[1].Instance == "other-oth-702" {
		t.Fatalf("instances = %+v", snapshot.Instances)
	}
	if snapshot.Plan == nil || snapshot.Plan.Summary.Total == 0 {
		t.Fatalf("plan = %+v", snapshot.Plan)
	}
	if snapshot.Jobs == nil || snapshot.Jobs.Summary.Total != 1 || len(snapshot.Jobs.ReadySteps) != 1 {
		t.Fatalf("jobs = %+v", snapshot.Jobs)
	}
	if len(snapshot.JobStatus) != 1 || snapshot.JobStatus[0].JobID != "squ-702" {
		t.Fatalf("job status = %+v", snapshot.JobStatus)
	}
	if len(snapshot.PipelineStatus) != 1 || snapshot.PipelineStatus[0].Pipeline != "ticket_to_pr" || snapshot.PipelineStatus[0].ReadySteps != 1 {
		t.Fatalf("pipeline status = %+v", snapshot.PipelineStatus)
	}
	if snapshot.Schedules == nil || len(snapshot.Schedules.Rows) != 1 || snapshot.Schedules.Rows[0].Name != "delivery_due" {
		t.Fatalf("schedules = %+v", snapshot.Schedules)
	}
	if got := lifecycleEventInstances(snapshot.Events); strings.Join(got, ",") != "manager,worker-squ-702" {
		t.Fatalf("events = %v\nbody=%s", got, out.String())
	}
	body := out.String()
	for _, leak := range []string{"platform_due", "platform_work", "oth-702", "q-platform-monitor", "platform worker", "build-worker-1"} {
		if strings.Contains(body, leak) {
			t.Fatalf("team monitor json leaked %q:\n%s", leak, body)
		}
	}

	codex := NewRootCmd()
	codexOut, codexErr := &bytes.Buffer{}, &bytes.Buffer{}
	codex.SetOut(codexOut)
	codex.SetErr(codexErr)
	codex.SetArgs([]string{"team", "monitor", "delivery", "--repo", root, "--runtime", "codex", "--events", "10", "--json"})
	if err := codex.Execute(); err != nil {
		t.Fatalf("team monitor runtime: %v\nstderr=%s", err, codexErr.String())
	}
	var runtimeSnapshot monitorSnapshot
	if err := json.Unmarshal(codexOut.Bytes(), &runtimeSnapshot); err != nil {
		t.Fatalf("decode team monitor runtime: %v\nbody=%s", err, codexOut.String())
	}
	if got := psJSONRowNames(runtimeSnapshot.Instances); strings.Join(got, ",") != "worker-squ-702" {
		t.Fatalf("team monitor runtime instances = %v", got)
	}
	if runtimeSnapshot.Instances[0].Runtime != "codex" {
		t.Fatalf("team monitor runtime instance = %+v", runtimeSnapshot.Instances[0])
	}
	if got := statsJSONRowNames(runtimeSnapshot.Stats); strings.Join(got, ",") != "worker-squ-702" {
		t.Fatalf("team monitor runtime stats = %v", got)
	}
	if got := lifecycleEventInstances(runtimeSnapshot.Events); strings.Join(got, ",") != "worker-squ-702" {
		t.Fatalf("team monitor runtime events = %v\nbody=%s", got, codexOut.String())
	}
	for _, leak := range []string{"manager up", "platform worker", "other-oth-702", "build-worker-1"} {
		if strings.Contains(codexOut.String(), leak) {
			t.Fatalf("team monitor runtime leaked %q:\n%s", leak, codexOut.String())
		}
	}

	badRuntime := NewRootCmd()
	badRuntime.SetOut(&bytes.Buffer{})
	badRuntimeErr := &bytes.Buffer{}
	badRuntime.SetErr(badRuntimeErr)
	badRuntime.SetArgs([]string{"team", "monitor", "delivery", "--repo", root, "--runtime", "llama"})
	if err := badRuntime.Execute(); err == nil {
		t.Fatal("team monitor accepted unknown runtime")
	}
	if !strings.Contains(badRuntimeErr.String(), "unknown --runtime") {
		t.Fatalf("bad runtime stderr = %q", badRuntimeErr.String())
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"team", "monitor", "delivery", "--repo", root, "--all", "--jobs", "--schedules", "--events", "10"})
	if err := text.Execute(); err != nil {
		t.Fatalf("team monitor text: %v\nstderr=%s", err, textErr.String())
	}
	textBody := textOut.String()
	for _, want := range []string{"Team: delivery", "jobs:", "schedules:", "instances:", "events:", "stats:"} {
		if !strings.Contains(textBody, want) {
			t.Fatalf("team monitor text missing %q:\n%s", want, textBody)
		}
	}
	for _, leak := range []string{"platform_due", "platform_work", "oth-702", "q-platform-monitor"} {
		if strings.Contains(textBody, leak) {
			t.Fatalf("team monitor text leaked %q:\n%s", leak, textBody)
		}
	}

	formatted := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatted.SetOut(formatOut)
	formatted.SetErr(formatErr)
	formatted.SetArgs([]string{"team", "monitor", "delivery", "--repo", root, "--format", "{{.Team.Name}} {{len .Instances}}"})
	if err := formatted.Execute(); err != nil {
		t.Fatalf("team monitor format: %v\nstderr=%s", err, formatErr.String())
	}
	if strings.TrimSpace(formatOut.String()) != "delivery 2" {
		t.Fatalf("team monitor format = %q", formatOut.String())
	}
}

func TestTeamTickDryRunScopesMaintenance(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.other]
agent = "other"
ephemeral = true
replicas = 1

[[instances.other.triggers]]
event = "agent.dispatch"
match.target = "other"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[pipelines.platform_work]
trigger.event = "ticket.created"
trigger.match.team = "platform"

[[pipelines.platform_work.steps]]
id = "implement"
target = "other"

[schedules.delivery_due]
every = "24h"
run_on_start = true
payload.target = "worker"

[schedules.platform_due]
every = "24h"
run_on_start = true
payload.target = "other"

[teams.delivery]
description = "Delivery team"
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
schedules = ["delivery_due"]

[teams.platform]
instances = ["other"]
pipelines = ["platform_work"]
schedules = ["platform_due"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-100",
			Ticket:    "SQU-100",
			Target:    "worker",
			Kickoff:   "SQU-100: implement",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusBlocked},
			},
		},
		{
			ID:        "oth-100",
			Ticket:    "OTH-100",
			Target:    "other",
			Kickoff:   "OTH-100: implement",
			Pipeline:  "platform_work",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "other", Status: job.StatusBlocked},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-delivery-ready",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-100",
			Payload:    map[string]any{"job_id": "squ-100", "target": "worker", "ticket": "SQU-100"},
			QueuedAt:   now.Add(-time.Minute),
			UpdatedAt:  now.Add(-time.Minute),
		},
		{
			ID:         "q-platform-ready",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "other",
			InstanceID: "other-oth-100",
			Payload:    map[string]any{"job_id": "oth-100", "target": "other", "ticket": "OTH-100"},
			QueuedAt:   now.Add(-time.Minute),
			UpdatedAt:  now.Add(-time.Minute),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("write queue item %s: %v", item.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "tick", "delivery", "--repo", root, "--workspace", "repo", "--dry-run", "--preview-routes", "--runtime", "codex", "--runtime-bin", "codex-dev", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team tick dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var result teamTickResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode team tick: %v\nbody=%s", err, out.String())
	}
	if result.Team.Name != "delivery" || !result.Tick.DryRun {
		t.Fatalf("team tick result = %+v", result)
	}
	if result.Tick.Schedule == nil || result.Tick.Schedule.WouldFire != 1 || len(result.Tick.Schedule.Schedules) != 1 || result.Tick.Schedule.Schedules[0].Name != "delivery_due" {
		t.Fatalf("team tick schedules = %+v", result.Tick.Schedule)
	}
	if result.Tick.Queue == nil || result.Tick.Queue.WouldDispatch != 1 || result.Tick.Queue.Pending != 1 || len(result.Tick.Queue.Outcomes) != 1 || result.Tick.Queue.Outcomes[0].Instance != "worker" {
		t.Fatalf("team tick queue = %+v", result.Tick.Queue)
	}
	if len(result.Tick.Advance) != 1 || result.Tick.Advance[0].JobID != "squ-100" || result.Tick.Advance[0].Pipeline != "ticket_to_pr" || result.Tick.Advance[0].Preview == nil {
		t.Fatalf("team tick advance = %+v", result.Tick.Advance)
	}
	dispatch := result.Tick.Advance[0].Preview.Dispatch
	if dispatch == nil || dispatch.Preview == nil {
		t.Fatalf("team tick dispatch preview = %+v", result.Tick.Advance[0].Preview)
	}
	payload := dispatch.Preview.Payload
	if payload["runtime"] != "codex" || payload["runtime_binary"] != "codex-dev" {
		t.Fatalf("team tick payload = %+v", payload)
	}
	body := out.String()
	for _, leak := range []string{"platform_due", "platform_work", "oth-100", "q-platform-ready"} {
		if strings.Contains(body, leak) {
			t.Fatalf("team tick json leaked %q:\n%s", leak, body)
		}
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"team", "tick", "delivery", "--repo", root, "--workspace", "repo", "--dry-run", "--preview-routes"})
	if err := text.Execute(); err != nil {
		t.Fatalf("team tick text: %v\nstderr=%s", err, textErr.String())
	}
	textBody := textOut.String()
	for _, want := range []string{"Team: delivery", "Schedules:", "delivery_due", "Queue:", "would_dispatch", "Pipeline advance:", "squ-100", "Matched: worker"} {
		if !strings.Contains(textBody, want) {
			t.Fatalf("team tick text missing %q:\n%s", want, textBody)
		}
	}
	for _, leak := range []string{"platform_due", "platform_work", "oth-100", "q-platform-ready"} {
		if strings.Contains(textBody, leak) {
			t.Fatalf("team tick text leaked %q:\n%s", leak, textBody)
		}
	}

	formatted := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatted.SetOut(formatOut)
	formatted.SetErr(formatErr)
	formatted.SetArgs([]string{"team", "tick", "delivery", "--repo", root, "--dry-run", "--format", "{{.Team.Name}} {{.Tick.Queue.WouldDispatch}} {{len .Tick.Advance}}"})
	if err := formatted.Execute(); err != nil {
		t.Fatalf("team tick format: %v\nstderr=%s", err, formatErr.String())
	}
	if strings.TrimSpace(formatOut.String()) != "delivery 1 1" {
		t.Fatalf("team tick format = %q", formatOut.String())
	}

	invalid := NewRootCmd()
	invalidOut, invalidErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalid.SetOut(invalidOut)
	invalid.SetErr(invalidErr)
	invalid.SetArgs([]string{"team", "tick", "delivery", "--repo", root})
	if err := invalid.Execute(); err == nil {
		t.Fatal("team tick without --dry-run succeeded")
	}
	if !strings.Contains(invalidErr.String(), "daemon is not running") || !strings.Contains(invalidErr.String(), "use --dry-run") {
		t.Fatalf("team tick invalid stderr = %q stdout=%q", invalidErr.String(), invalidOut.String())
	}
}

func TestTeamTickAllReadyStepsDryRun(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.parallel_checks]
trigger.event = "ticket.created"

[[pipelines.parallel_checks.steps]]
id = "lint"
target = "worker"

[[pipelines.parallel_checks.steps]]
id = "test"
target = "worker"

[[pipelines.parallel_checks.steps]]
id = "review"
target = "manager"
after = ["lint", "test"]

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["parallel_checks"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	create := NewRootCmd()
	createOut, createErr := &bytes.Buffer{}, &bytes.Buffer{}
	create.SetOut(createOut)
	create.SetErr(createErr)
	create.SetArgs([]string{"team", "run", "delivery", "SQU-813", "--repo", root, "--json"})
	if err := create.Execute(); err != nil {
		t.Fatalf("team run: %v\nstderr=%s", err, createErr.String())
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "tick", "delivery", "--repo", root, "--dry-run", "--skip-schedules", "--skip-drain", "--all-ready-steps", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team tick all-ready: %v\nstderr=%s", err, stderr.String())
	}
	var result teamTickResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode team tick all-ready: %v\nbody=%s", err, out.String())
	}
	if len(result.Tick.Advance) != 2 || result.Tick.Advance[0].JobID != "squ-813" || result.Tick.Advance[0].StepID != "lint" || result.Tick.Advance[0].StepStatus != job.StatusQueued || result.Tick.Advance[1].StepID != "test" {
		t.Fatalf("team tick all-ready advance = %+v, want queued lint then ready test", result.Tick.Advance)
	}

	limited := NewRootCmd()
	limitedOut, limitedErr := &bytes.Buffer{}, &bytes.Buffer{}
	limited.SetOut(limitedOut)
	limited.SetErr(limitedErr)
	limited.SetArgs([]string{"team", "tick", "delivery", "--repo", root, "--dry-run", "--skip-schedules", "--skip-drain", "--all-ready-steps", "--limit", "1", "--json"})
	if err := limited.Execute(); err != nil {
		t.Fatalf("team tick all-ready limited: %v\nstderr=%s", err, limitedErr.String())
	}
	var limitedResult teamTickResult
	if err := json.Unmarshal(limitedOut.Bytes(), &limitedResult); err != nil {
		t.Fatalf("decode limited team tick all-ready: %v\nbody=%s", err, limitedOut.String())
	}
	if len(limitedResult.Tick.Advance) != 1 || limitedResult.Tick.Advance[0].StepID != "lint" {
		t.Fatalf("limited team tick advance = %+v, want queued first step", limitedResult.Tick.Advance)
	}
}

func TestTeamTickRunsScopedMaintenance(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "agent-team-team-tick-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	teamDir := filepath.Join(root, ".agent_team")
	for _, agent := range []string{"worker", "other"} {
		agentDir := filepath.Join(teamDir, "agents", agent)
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\ndescription: test " + agent + "\n---\n\nYou are a test " + agent + ".\n"
		if err := os.WriteFile(filepath.Join(agentDir, "agent.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[[instances.manager.triggers]]
event = "schedule"

[instances.other]
agent = "other"
ephemeral = true
replicas = 1

[[instances.other.triggers]]
event = "agent.dispatch"
match.target = "other"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[pipelines.platform_work]
trigger.event = "ticket.created"
trigger.match.team = "platform"

[[pipelines.platform_work.steps]]
id = "implement"
target = "other"

[schedules.delivery_due]
every = "24h"
run_on_start = true
payload.target = "worker"

[schedules.platform_due]
every = "24h"
run_on_start = true
payload.target = "other"

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
schedules = ["delivery_due"]

[teams.platform]
instances = ["other"]
pipelines = ["platform_work"]
schedules = ["platform_due"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-200",
			Ticket:    "SQU-200",
			Target:    "worker",
			Kickoff:   "SQU-200: implement",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusBlocked},
			},
		},
		{
			ID:        "oth-200",
			Ticket:    "OTH-200",
			Target:    "other",
			Kickoff:   "OTH-200: implement",
			Pipeline:  "platform_work",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "other", Status: job.StatusBlocked},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-delivery-run",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-200",
			Payload:    map[string]any{"job_id": "squ-200", "target": "worker", "name": "worker-squ-200", "ticket": "SQU-200"},
			QueuedAt:   now.Add(-time.Minute),
			UpdatedAt:  now.Add(-time.Minute),
		},
		{
			ID:         "q-platform-run",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "other",
			InstanceID: "other-oth-200",
			Payload:    map[string]any{"job_id": "oth-200", "target": "other", "name": "other-oth-200", "ticket": "OTH-200"},
			QueuedAt:   now.Add(-time.Minute),
			UpdatedAt:  now.Add(-time.Minute),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("write queue item %s: %v", item.ID, err)
		}
	}
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), fakeSpawnerForTest(t, 2*time.Second))
	cleanupDaemon := startRunTestDaemon(t, teamDir, mgr)
	defer cleanupDaemon()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "tick", "delivery", "--repo", root, "--workspace", "repo", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team tick: %v\nstderr=%s", err, stderr.String())
	}
	var result teamTickResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode team tick: %v\nbody=%s", err, out.String())
	}
	if result.Tick.DryRun || result.Tick.Schedule == nil || result.Tick.Schedule.Fired != 1 || len(result.Tick.Schedule.Schedules) != 1 || result.Tick.Schedule.Schedules[0].Name != "delivery_due" {
		t.Fatalf("team tick schedule = %+v", result.Tick.Schedule)
	}
	if result.Tick.Queue == nil || result.Tick.Queue.Dispatched != 1 || result.Tick.Queue.Pending != 0 || len(result.Tick.Queue.Outcomes) != 1 || result.Tick.Queue.Outcomes[0].InstanceID != "worker-squ-200" {
		t.Fatalf("team tick queue = %+v", result.Tick.Queue)
	}
	if len(result.Tick.Advance) != 1 || result.Tick.Advance[0].JobID != "squ-200" || result.Tick.Advance[0].Action != "advanced" || result.Tick.Advance[0].StepStatus != job.StatusRunning {
		t.Fatalf("team tick advance = %+v", result.Tick.Advance)
	}
	if _, err := daemon.ReadScheduleState(daemon.DaemonRoot(teamDir), "delivery_due"); err != nil {
		t.Fatalf("delivery schedule state missing: %v", err)
	}
	if _, err := daemon.ReadScheduleState(daemon.DaemonRoot(teamDir), "platform_due"); !os.IsNotExist(err) {
		t.Fatalf("platform schedule state changed, err=%v", err)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-delivery-run"); !os.IsNotExist(err) {
		t.Fatalf("delivery queue item still exists or unexpected err=%v", err)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-platform-run"); err != nil {
		t.Fatalf("platform queue item changed: %v", err)
	}
	teamJob, err := job.Read(teamDir, "squ-200")
	if err != nil {
		t.Fatalf("read team job: %v", err)
	}
	if len(teamJob.Steps) != 1 || teamJob.Steps[0].Status != job.StatusRunning || teamJob.Steps[0].Instance == "" {
		t.Fatalf("team job after tick = %+v", teamJob)
	}
	otherJob, err := job.Read(teamDir, "oth-200")
	if err != nil {
		t.Fatalf("read other job: %v", err)
	}
	if len(otherJob.Steps) != 1 || otherJob.Steps[0].Status != job.StatusBlocked {
		t.Fatalf("other job changed = %+v", otherJob)
	}
	messages, err := daemon.ReadMessages(daemon.DaemonRoot(teamDir), "manager")
	if err != nil {
		t.Fatalf("read manager messages: %v", err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0].Body, "delivery_due") || strings.Contains(messages[0].Body, "platform_due") {
		t.Fatalf("manager messages = %+v", messages)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-200")
	stopAndWaitForTest(t, mgr, teamJob.Steps[0].Instance)
}

func TestTeamTickUntilIdleScopesQueueWork(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "agent-team-team-tick-idle-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	teamDir := filepath.Join(root, ".agent_team")
	for _, agent := range []string{"worker", "other"} {
		agentDir := filepath.Join(teamDir, "agents", agent)
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\ndescription: test " + agent + "\n---\n\nYou are a test " + agent + ".\n"
		if err := os.WriteFile(filepath.Join(agentDir, "agent.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.other]
agent = "other"
ephemeral = true
replicas = 1

[[instances.other.triggers]]
event = "agent.dispatch"
match.target = "other"

[teams.delivery]
instances = ["worker"]

[teams.platform]
instances = ["other"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-idle-delivery",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-idle-delivery",
			Payload:    map[string]any{"target": "worker", "name": "worker-idle-delivery", "ticket": "SQU-IDLE"},
			QueuedAt:   now.Add(-time.Minute),
			UpdatedAt:  now.Add(-time.Minute),
		},
		{
			ID:         "q-idle-platform",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "other",
			InstanceID: "other-idle-platform",
			Payload:    map[string]any{"target": "other", "name": "other-idle-platform", "ticket": "OTH-IDLE"},
			QueuedAt:   now.Add(-time.Minute),
			UpdatedAt:  now.Add(-time.Minute),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("write queue item %s: %v", item.ID, err)
		}
	}
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), fakeSpawnerForTest(t, 2*time.Second))
	cleanupDaemon := startRunTestDaemon(t, teamDir, mgr)
	defer cleanupDaemon()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "tick", "delivery", "--repo", root, "--skip-schedules", "--skip-advance", "--until-idle", "--interval", "0s", "--max-cycles", "3", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team tick until-idle: %v\nstderr=%s", err, stderr.String())
	}
	var result teamTickUntilIdleResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode team tick until-idle: %v\nbody=%s", err, out.String())
	}
	if result.Team.Name != "delivery" || !result.Idle || result.CyclesRun != 2 || len(result.Cycles) != 2 {
		t.Fatalf("until-idle result = %+v", result)
	}
	if result.Cycles[0].Tick.Queue == nil || result.Cycles[0].Tick.Queue.Dispatched != 1 {
		t.Fatalf("first cycle queue = %+v", result.Cycles[0].Tick.Queue)
	}
	if result.Cycles[1].Tick.Queue == nil || result.Cycles[1].Tick.Queue.Dispatched != 0 || result.Cycles[1].Tick.Queue.Pending != 0 {
		t.Fatalf("second cycle queue = %+v", result.Cycles[1].Tick.Queue)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-idle-delivery"); !os.IsNotExist(err) {
		t.Fatalf("delivery queue item still exists or unexpected err=%v", err)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-idle-platform"); err != nil {
		t.Fatalf("platform queue item changed: %v", err)
	}
	stopAndWaitForTest(t, mgr, "worker-idle-delivery")

	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), &daemon.QueueItem{
		ID:         "q-drain-delivery",
		State:      daemon.QueueStatePending,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-drain-delivery",
		Payload:    map[string]any{"target": "worker", "name": "worker-drain-delivery", "ticket": "SQU-DRAIN"},
		QueuedAt:   now.Add(-time.Minute),
		UpdatedAt:  now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("write drain queue item: %v", err)
	}

	drain := NewRootCmd()
	drainOut, drainErr := &bytes.Buffer{}, &bytes.Buffer{}
	drain.SetOut(drainOut)
	drain.SetErr(drainErr)
	drain.SetArgs([]string{"team", "drain", "delivery", "--repo", root, "--skip-schedules", "--skip-advance", "--interval", "0s", "--max-cycles", "3", "--json"})
	if err := drain.Execute(); err != nil {
		t.Fatalf("team drain: %v\nstderr=%s", err, drainErr.String())
	}
	var drainResult teamTickUntilIdleResult
	if err := json.Unmarshal(drainOut.Bytes(), &drainResult); err != nil {
		t.Fatalf("decode team drain: %v\nbody=%s", err, drainOut.String())
	}
	if drainResult.Team.Name != "delivery" || !drainResult.Idle || drainResult.CyclesRun != 2 || len(drainResult.Cycles) != 2 {
		t.Fatalf("drain result = %+v", drainResult)
	}
	if drainResult.Cycles[0].Tick.Queue == nil || drainResult.Cycles[0].Tick.Queue.Dispatched != 1 {
		t.Fatalf("first drain cycle queue = %+v", drainResult.Cycles[0].Tick.Queue)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-drain-delivery"); !os.IsNotExist(err) {
		t.Fatalf("drain delivery queue item still exists or unexpected err=%v", err)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-idle-platform"); err != nil {
		t.Fatalf("platform queue item changed after drain: %v", err)
	}
	stopAndWaitForTest(t, mgr, "worker-drain-delivery")
}

func TestTeamTickRejectsInvalidLoopFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "watch until idle",
			args: []string{"team", "tick", "delivery", "--watch", "--until-idle"},
			want: "choose one of --watch or --until-idle",
		},
		{
			name: "dry until idle",
			args: []string{"team", "tick", "delivery", "--until-idle", "--dry-run"},
			want: "--until-idle cannot be combined with --dry-run",
		},
		{
			name: "max cycles without until idle",
			args: []string{"team", "tick", "delivery", "--max-cycles", "2"},
			want: "--max-cycles requires --until-idle",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			stderr := &bytes.Buffer{}
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil {
				t.Fatalf("team tick %s succeeded", tc.name)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestTeamRepairScopesQueueAndHealth(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.other]
agent = "other"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

	[[pipelines.ticket_to_pr.steps]]
	id = "implement"
	target = "worker"

	[pipelines.release_review]
	trigger.event = "release.created"

	[[pipelines.release_review.steps]]
	id = "implement"
	target = "worker"

	[teams.delivery]
	instances = ["manager", "worker"]
	pipelines = ["ticket_to_pr", "release_review"]

[teams.platform]
instances = ["other"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	teamJob := &job.Job{
		ID:         "squ-300",
		Ticket:     "SQU-300",
		Target:     "worker",
		Pipeline:   "ticket_to_pr",
		Status:     job.StatusFailed,
		LastStatus: "worker failed",
		CreatedAt:  now,
		UpdatedAt:  now,
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusFailed},
		},
	}
	if err := job.Write(teamDir, teamJob); err != nil {
		t.Fatalf("write team job: %v", err)
	}
	releaseJob := &job.Job{
		ID:         "rel-300",
		Ticket:     "REL-300",
		Target:     "worker",
		Pipeline:   "release_review",
		Status:     job.StatusFailed,
		LastStatus: "release review failed",
		CreatedAt:  now,
		UpdatedAt:  now,
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusFailed},
		},
	}
	if err := job.Write(teamDir, releaseJob); err != nil {
		t.Fatalf("write release job: %v", err)
	}
	otherJob := &job.Job{
		ID:         "oth-300",
		Ticket:     "OTH-300",
		Target:     "other",
		Status:     job.StatusFailed,
		LastStatus: "other failed",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := job.Write(teamDir, otherJob); err != nil {
		t.Fatalf("write other job: %v", err)
	}
	for _, item := range []*daemon.QueueItem{
		{
			ID:             "q-team-repair",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     "worker-squ-300",
			Payload:        map[string]any{"job_id": "squ-300", "target": "worker", "ticket": "SQU-300"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-time.Hour),
			UpdatedAt:      now,
			DeadLetteredAt: now,
		},
		{
			ID:             "q-other-repair",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "other",
			InstanceID:     "other-oth-300",
			Payload:        map[string]any{"job_id": "oth-300", "target": "other", "ticket": "OTH-300"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-time.Hour),
			UpdatedAt:      now,
			DeadLetteredAt: now,
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("write queue item %s: %v", item.ID, err)
		}
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"team", "repair", "delivery", "--repo", root, "--dry-run", "--skip-daemon", "--skip-tick", "--jobs", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("team repair dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview teamRepairResult
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode team repair dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if preview.Team.Name != "delivery" || !preview.DryRun || preview.Daemon.Action != "skipped" || preview.Queue.Action != "would_retry" {
		t.Fatalf("team repair preview = %+v", preview)
	}
	if preview.HealthBefore == nil || preview.HealthBefore.Queue.Dead != 1 || preview.HealthBefore.Jobs == nil || preview.HealthBefore.Jobs.Summary.Total != 2 {
		t.Fatalf("team repair health before = %+v", preview.HealthBefore)
	}
	if len(preview.Queue.Results) != 1 || preview.Queue.Results[0].ID != "q-team-repair" || preview.Queue.Results[0].Action != "would_retry" {
		t.Fatalf("team repair queue preview = %+v", preview.Queue.Results)
	}
	if strings.Contains(dryOut.String(), "q-other-repair") || strings.Contains(dryOut.String(), "oth-300") {
		t.Fatalf("team repair dry-run leaked unrelated work:\n%s", dryOut.String())
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-team-repair"); err != nil || item.State != daemon.QueueStateDead {
		t.Fatalf("dry-run changed team queue item=%+v err=%v", item, err)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"team", "repair", "delivery", "--repo", root, "--dry-run", "--skip-daemon", "--skip-tick", "--jobs"})
	if err := text.Execute(); err != nil {
		t.Fatalf("team repair text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"Team: delivery", "Health before:", "q-team-repair", "pipeline_failed_step"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("team repair text missing %q:\n%s", want, textOut.String())
		}
	}
	if strings.Contains(textOut.String(), "q-other-repair") || strings.Contains(textOut.String(), "oth-300") {
		t.Fatalf("team repair text leaked unrelated work:\n%s", textOut.String())
	}

	formatted := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatted.SetOut(formatOut)
	formatted.SetErr(formatErr)
	formatted.SetArgs([]string{"team", "repair", "delivery", "--repo", root, "--dry-run", "--skip-daemon", "--skip-tick", "--jobs", "--format", "{{.Team.Name}} {{.DryRun}} {{.Daemon.Action}} {{.Queue.Action}} {{len .Queue.Results}}"})
	if err := formatted.Execute(); err != nil {
		t.Fatalf("team repair format: %v\nstderr=%s", err, formatErr.String())
	}
	if formatErr.Len() != 0 {
		t.Fatalf("team repair format stderr = %q", formatErr.String())
	}
	if got, want := formatOut.String(), "delivery true skipped would_retry 1\n"; got != want {
		t.Fatalf("team repair format output = %q, want %q", got, want)
	}

	retryPreview := NewRootCmd()
	retryPreviewOut, retryPreviewErr := &bytes.Buffer{}, &bytes.Buffer{}
	retryPreview.SetOut(retryPreviewOut)
	retryPreview.SetErr(retryPreviewErr)
	retryMessageFile := filepath.Join(root, "team-repair-retry-message.txt")
	if err := os.WriteFile(retryMessageFile, []byte("team repair retry from file\n"), 0o644); err != nil {
		t.Fatalf("write retry message: %v", err)
	}
	retryPreview.SetArgs([]string{
		"team", "repair", "delivery",
		"--repo", root,
		"--dry-run",
		"--retry-pipelines",
		"--retry-pipeline", "ticket_to_pr",
		"--retry-message-file", retryMessageFile,
		"--preview-routes",
		"--skip-daemon",
		"--skip-queue",
		"--skip-tick",
		"--workspace", "repo",
		"--runtime", "codex",
		"--runtime-bin", "codex-dev",
		"--json",
	})
	if err := retryPreview.Execute(); err != nil {
		t.Fatalf("team repair retry dry-run: %v\nstderr=%s", err, retryPreviewErr.String())
	}
	var retryDry teamRepairResult
	if err := json.Unmarshal(retryPreviewOut.Bytes(), &retryDry); err != nil {
		t.Fatalf("decode team repair retry dry-run: %v\nbody=%s", err, retryPreviewOut.String())
	}
	if retryDry.PipelineRetry.Action != "would_dispatch" || len(retryDry.PipelineRetry.Results) != 1 {
		t.Fatalf("team repair pipeline retry = %+v", retryDry.PipelineRetry)
	}
	retryRow := retryDry.PipelineRetry.Results[0]
	if retryRow.JobID != "squ-300" || retryRow.Action != "would_dispatch" || retryRow.StepID != "implement" || retryRow.Target != "worker" || retryRow.Preview == nil || retryRow.Preview.Dispatch == nil {
		t.Fatalf("team repair retry row = %+v", retryRow)
	}
	if retryRow.Preview.Dispatch.RequestedName != "worker-squ-300-implement" {
		t.Fatalf("team repair retry requested name = %q", retryRow.Preview.Dispatch.RequestedName)
	}
	retryPayload := retryRow.Preview.Dispatch.Preview.Payload
	if retryPayload["runtime"] != "codex" || retryPayload["runtime_binary"] != "codex-dev" {
		t.Fatalf("team repair retry payload = %+v", retryPayload)
	}
	if strings.Contains(retryPreviewOut.String(), "oth-300") || strings.Contains(retryPreviewOut.String(), "q-other-repair") || strings.Contains(retryPreviewOut.String(), "rel-300") {
		t.Fatalf("team repair retry dry-run leaked unrelated work:\n%s", retryPreviewOut.String())
	}
	unchangedJob, err := job.Read(teamDir, "squ-300")
	if err != nil {
		t.Fatalf("read unchanged team job: %v", err)
	}
	if unchangedJob.Status != job.StatusFailed || unchangedJob.Steps[0].Status != job.StatusFailed {
		t.Fatalf("team repair retry dry-run mutated job = %+v", unchangedJob)
	}
	unchangedRelease, err := job.Read(teamDir, "rel-300")
	if err != nil {
		t.Fatalf("read unchanged release job: %v", err)
	}
	if unchangedRelease.Status != job.StatusFailed || unchangedRelease.Steps[0].Status != job.StatusFailed {
		t.Fatalf("team repair retry dry-run mutated release job = %+v", unchangedRelease)
	}

	run := NewRootCmd()
	runOut, runErr := &bytes.Buffer{}, &bytes.Buffer{}
	run.SetOut(runOut)
	run.SetErr(runErr)
	run.SetArgs([]string{"team", "repair", "delivery", "--repo", root, "--skip-daemon", "--skip-tick", "--json"})
	if err := run.Execute(); err != nil {
		t.Fatalf("team repair retry: %v\nstderr=%s", err, runErr.String())
	}
	var repaired teamRepairResult
	if err := json.Unmarshal(runOut.Bytes(), &repaired); err != nil {
		t.Fatalf("decode team repair retry: %v\nbody=%s", err, runOut.String())
	}
	if repaired.DryRun || repaired.Queue.Action != "retried" || len(repaired.Queue.Results) != 1 || repaired.Queue.Results[0].ID != "q-team-repair" {
		t.Fatalf("team repair retry result = %+v", repaired)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-team-repair"); err != nil || item.State != daemon.QueueStatePending || item.LastError != "" {
		t.Fatalf("team queue item not retried=%+v err=%v", item, err)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-other-repair"); err != nil || item.State != daemon.QueueStateDead {
		t.Fatalf("unrelated queue item changed=%+v err=%v", item, err)
	}
}

func TestTeamRepairRejectsInvalidFormatFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "format with json",
			args: []string{"team", "repair", "delivery", "--format", "{{.Team.Name}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "invalid format",
			args: []string{"team", "repair", "delivery", "--format", "{{"},
			want: "invalid --format template",
		},
		{
			name: "retry pipelines without daemon",
			args: []string{"team", "repair", "delivery", "--retry-pipelines", "--skip-daemon"},
			want: "--retry-pipelines requires daemon access",
		},
		{
			name: "retry message without retry pipelines",
			args: []string{"team", "repair", "delivery", "--retry-message", "incident"},
			want: "--retry-message requires --retry-pipelines",
		},
		{
			name: "retry message file without retry pipelines",
			args: []string{"team", "repair", "delivery", "--retry-message-file", "incident.txt"},
			want: "--retry-message-file requires --retry-pipelines",
		},
		{
			name: "retry step without retry pipelines",
			args: []string{"team", "repair", "delivery", "--retry-step", "review"},
			want: "--retry-step requires --retry-pipelines",
		},
		{
			name: "retry pipeline without retry pipelines",
			args: []string{"team", "repair", "delivery", "--retry-pipeline", "ticket_to_pr"},
			want: "--retry-pipeline requires --retry-pipelines",
		},
		{
			name: "retry force without retry pipelines",
			args: []string{"team", "repair", "delivery", "--retry-force"},
			want: "--retry-force requires --retry-pipelines",
		},
		{
			name: "timeout jobs with timeout pipelines",
			args: []string{"team", "repair", "delivery", "--timeout-jobs", "--timeout-pipelines"},
			want: "--timeout-jobs cannot be combined with --timeout-pipelines",
		},
		{
			name: "timeout pipeline without timeout mode",
			args: []string{"team", "repair", "delivery", "--timeout-pipeline", "ticket_to_pr"},
			want: "--timeout-pipeline requires --timeout-pipelines or --timeout-jobs",
		},
		{
			name: "timeout message file without timeout mode",
			args: []string{"team", "repair", "delivery", "--timeout-message-file", "incident.txt"},
			want: "--timeout-message-file requires --timeout-pipelines or --timeout-jobs",
		},
		{
			name: "timeout target without timeout mode",
			args: []string{"team", "repair", "delivery", "--timeout-target-agent", "worker"},
			want: "--timeout-target-agent requires --timeout-pipelines or --timeout-jobs",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("team repair invalid flags succeeded: stdout=%s", out.String())
			}
			var code ExitCode
			if !errors.As(err, &code) || int(code) != 2 {
				t.Fatalf("team repair err = %v, want exit code 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func setupTeamScopedPlanFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.ticket-manager]
agent = "ticket-manager"

[instances.build-worker]
agent = "worker"
ephemeral = true

[instances.other]
agent = "other"

[teams.delivery]
description = "Delivery team"
instances = ["manager", "ticket-manager", "worker"]

[teams.platform]
instances = ["other", "build-worker"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
		{Instance: "worker-squ-101", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
		{Instance: "build-worker-1", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
		{Instance: "adhoc-worker", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
		{Instance: "other", Agent: "other", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	return root
}

func TestTeamPlanScopesRowsAndStopExtras(t *testing.T) {
	root := setupTeamScopedPlanFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "plan", "delivery", "--repo", root, "--stop-extras", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team plan: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot teamPlanSnapshot
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode team plan: %v\nbody=%s", err, out.String())
	}
	if snapshot.Team.Name != "delivery" || snapshot.Plan == nil {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	rows := planRowsByInstance(snapshot.Plan.Instances)
	for _, want := range []string{"manager", "ticket-manager", "worker", "worker-squ-101", "adhoc-worker"} {
		if _, ok := rows[want]; !ok {
			t.Fatalf("team plan rows = %+v, missing %s", snapshot.Plan.Instances, want)
		}
	}
	for _, unwanted := range []string{"build-worker", "build-worker-1", "other"} {
		if _, ok := rows[unwanted]; ok {
			t.Fatalf("team plan rows = %+v, included %s", snapshot.Plan.Instances, unwanted)
		}
	}
	if rows["adhoc-worker"].Action != "stop" || rows["adhoc-worker"].Kind != "extra" {
		t.Fatalf("adhoc-worker row = %+v, want stop extra", rows["adhoc-worker"])
	}

	noExtras := NewRootCmd()
	noExtrasOut, noExtrasErr := &bytes.Buffer{}, &bytes.Buffer{}
	noExtras.SetOut(noExtrasOut)
	noExtras.SetErr(noExtrasErr)
	noExtras.SetArgs([]string{"team", "plan", "delivery", "--repo", root, "--json"})
	if err := noExtras.Execute(); err != nil {
		t.Fatalf("team plan without extras: %v\nstderr=%s", err, noExtrasErr.String())
	}
	var noExtrasSnapshot teamPlanSnapshot
	if err := json.Unmarshal(noExtrasOut.Bytes(), &noExtrasSnapshot); err != nil {
		t.Fatalf("decode team plan without extras: %v\nbody=%s", err, noExtrasOut.String())
	}
	if _, ok := planRowsByInstance(noExtrasSnapshot.Plan.Instances)["adhoc-worker"]; ok {
		t.Fatalf("team plan without --stop-extras included adhoc-worker: %+v", noExtrasSnapshot.Plan.Instances)
	}

	runtimeOnly := NewRootCmd()
	runtimeOut, runtimeErr := &bytes.Buffer{}, &bytes.Buffer{}
	runtimeOnly.SetOut(runtimeOut)
	runtimeOnly.SetErr(runtimeErr)
	runtimeOnly.SetArgs([]string{"team", "plan", "delivery", "--repo", root, "--runtime", "codex", "--stop-extras", "--json"})
	if err := runtimeOnly.Execute(); err != nil {
		t.Fatalf("team plan runtime: %v\nstderr=%s", err, runtimeErr.String())
	}
	var runtimeSnapshot teamPlanSnapshot
	if err := json.Unmarshal(runtimeOut.Bytes(), &runtimeSnapshot); err != nil {
		t.Fatalf("decode team plan runtime: %v\nbody=%s", err, runtimeOut.String())
	}
	runtimeRows := planRowsByInstance(runtimeSnapshot.Plan.Instances)
	for _, want := range []string{"worker-squ-101", "adhoc-worker"} {
		if _, ok := runtimeRows[want]; !ok {
			t.Fatalf("team plan runtime rows = %+v, missing %s", runtimeSnapshot.Plan.Instances, want)
		}
	}
	for _, unwanted := range []string{"manager", "ticket-manager", "worker", "build-worker-1", "other"} {
		if _, ok := runtimeRows[unwanted]; ok {
			t.Fatalf("team plan runtime rows = %+v, included %s", runtimeSnapshot.Plan.Instances, unwanted)
		}
	}

	startOnly := NewRootCmd()
	startOut, startErr := &bytes.Buffer{}, &bytes.Buffer{}
	startOnly.SetOut(startOut)
	startOnly.SetErr(startErr)
	startOnly.SetArgs([]string{"team", "plan", "delivery", "--repo", root, "--action", "start", "--json"})
	if err := startOnly.Execute(); err != nil {
		t.Fatalf("team plan action start: %v\nstderr=%s", err, startErr.String())
	}
	var startSnapshot teamPlanSnapshot
	if err := json.Unmarshal(startOut.Bytes(), &startSnapshot); err != nil {
		t.Fatalf("decode team plan action start: %v\nbody=%s", err, startOut.String())
	}
	if startSnapshot.Plan.Summary.Total != 1 || startSnapshot.Plan.Summary.Start != 1 || startSnapshot.Plan.Instances[0].Instance != "ticket-manager" {
		t.Fatalf("start-filtered plan = %+v", startSnapshot.Plan)
	}

	formatted := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatted.SetOut(formatOut)
	formatted.SetErr(formatErr)
	formatted.SetArgs([]string{"team", "plan", "delivery", "--repo", root, "--format", "{{.Instance}} {{.Action}}"})
	if err := formatted.Execute(); err != nil {
		t.Fatalf("team plan format: %v\nstderr=%s", err, formatErr.String())
	}
	formatBody := formatOut.String()
	for _, want := range []string{"manager keep", "ticket-manager start", "worker on-demand", "worker-squ-101 keep"} {
		if !strings.Contains(formatBody, want) {
			t.Fatalf("formatted team plan missing %q:\n%s", want, formatBody)
		}
	}
	if strings.Contains(formatBody, "adhoc-worker") || strings.Contains(formatBody, "build-worker") {
		t.Fatalf("formatted team plan included unrelated/extra rows:\n%s", formatBody)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"team", "plan", "delivery", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("team plan text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"Team: delivery", "daemon:", "INSTANCE", "summary:"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("team plan text missing %q:\n%s", want, textOut.String())
		}
	}
}

func TestTeamSyncDryRunScopesRowsAndFilters(t *testing.T) {
	root := setupTeamScopedPlanFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "sync", "delivery", "--repo", root, "--dry-run", "--stop-extras", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team sync dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot teamPlanSnapshot
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode team sync dry-run: %v\nbody=%s", err, out.String())
	}
	if snapshot.Team.Name != "delivery" || snapshot.Plan == nil {
		t.Fatalf("sync snapshot = %+v", snapshot)
	}
	rows := planRowsByInstance(snapshot.Plan.Instances)
	for _, want := range []string{"manager", "ticket-manager", "worker", "worker-squ-101", "adhoc-worker"} {
		if _, ok := rows[want]; !ok {
			t.Fatalf("team sync rows = %+v, missing %s", snapshot.Plan.Instances, want)
		}
	}
	for _, unwanted := range []string{"build-worker", "build-worker-1", "other"} {
		if _, ok := rows[unwanted]; ok {
			t.Fatalf("team sync rows = %+v, included %s", snapshot.Plan.Instances, unwanted)
		}
	}
	if rows["adhoc-worker"].Action != "stop" || rows["adhoc-worker"].Kind != "extra" {
		t.Fatalf("adhoc-worker row = %+v, want stop extra", rows["adhoc-worker"])
	}

	runtimeOnly := NewRootCmd()
	runtimeOut, runtimeErr := &bytes.Buffer{}, &bytes.Buffer{}
	runtimeOnly.SetOut(runtimeOut)
	runtimeOnly.SetErr(runtimeErr)
	runtimeOnly.SetArgs([]string{"team", "sync", "delivery", "--repo", root, "--dry-run", "--stop-extras", "--runtime", "codex", "--json"})
	if err := runtimeOnly.Execute(); err != nil {
		t.Fatalf("team sync runtime dry-run: %v\nstderr=%s", err, runtimeErr.String())
	}
	var runtimeSnapshot teamPlanSnapshot
	if err := json.Unmarshal(runtimeOut.Bytes(), &runtimeSnapshot); err != nil {
		t.Fatalf("decode team sync runtime dry-run: %v\nbody=%s", err, runtimeOut.String())
	}
	runtimeRows := planRowsByInstance(runtimeSnapshot.Plan.Instances)
	for _, want := range []string{"worker-squ-101", "adhoc-worker"} {
		if _, ok := runtimeRows[want]; !ok {
			t.Fatalf("team sync runtime rows = %+v, missing %s", runtimeSnapshot.Plan.Instances, want)
		}
	}
	for _, unwanted := range []string{"manager", "ticket-manager", "worker", "build-worker-1", "other"} {
		if _, ok := runtimeRows[unwanted]; ok {
			t.Fatalf("team sync runtime rows = %+v, included %s", runtimeSnapshot.Plan.Instances, unwanted)
		}
	}

	startOnly := NewRootCmd()
	startOut, startErr := &bytes.Buffer{}, &bytes.Buffer{}
	startOnly.SetOut(startOut)
	startOnly.SetErr(startErr)
	startOnly.SetArgs([]string{"team", "sync", "delivery", "--repo", root, "--dry-run", "--action", "start", "--json"})
	if err := startOnly.Execute(); err != nil {
		t.Fatalf("team sync action start: %v\nstderr=%s", err, startErr.String())
	}
	var startSnapshot teamPlanSnapshot
	if err := json.Unmarshal(startOut.Bytes(), &startSnapshot); err != nil {
		t.Fatalf("decode team sync action start: %v\nbody=%s", err, startOut.String())
	}
	if startSnapshot.Plan.Summary.Total != 1 || startSnapshot.Plan.Summary.Start != 1 || startSnapshot.Plan.Instances[0].Instance != "ticket-manager" {
		t.Fatalf("start-filtered sync = %+v", startSnapshot.Plan)
	}

	formatted := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatted.SetOut(formatOut)
	formatted.SetErr(formatErr)
	formatted.SetArgs([]string{"team", "sync", "delivery", "--repo", root, "--dry-run", "--format", "{{.Instance}} {{.Action}}"})
	if err := formatted.Execute(); err != nil {
		t.Fatalf("team sync format: %v\nstderr=%s", err, formatErr.String())
	}
	formatBody := formatOut.String()
	for _, want := range []string{"manager keep", "ticket-manager start", "worker on-demand", "worker-squ-101 keep"} {
		if !strings.Contains(formatBody, want) {
			t.Fatalf("formatted team sync missing %q:\n%s", want, formatBody)
		}
	}
	if strings.Contains(formatBody, "adhoc-worker") || strings.Contains(formatBody, "build-worker") {
		t.Fatalf("formatted team sync included unrelated/extra rows:\n%s", formatBody)
	}
}

func lifecycleResultInstances(rows []lifecycleActionResult) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Instance)
	}
	return out
}

func instanceDownResultNames(rows []instanceDownResult) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Instance)
	}
	return out
}

func instanceRmResultNames(rows []instanceRmResult) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Instance)
	}
	return out
}

func statsJSONRowNames(rows []statsJSONRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Instance)
	}
	return out
}

func psJSONRowNames(rows []psJSONRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Instance)
	}
	return out
}

func planRowsByInstance(rows []planRow) map[string]planRow {
	out := map[string]planRow{}
	for _, row := range rows {
		out[row.Instance] = row
	}
	return out
}

func queueItemIDs(items []daemon.QueueItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.ID)
	}
	return out
}

func queueQuarantineDropIDs(items []queueQuarantineDropResult) string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.ID)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func logRowInstances(rows []logListRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Instance)
	}
	return out
}

func decodeLifecycleEventJSONL(t *testing.T, body string) []daemon.LifecycleEvent {
	t.Helper()
	var events []daemon.LifecycleEvent
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev daemon.LifecycleEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("decode lifecycle event %q: %v\nbody=%s", line, err, body)
		}
		events = append(events, ev)
	}
	return events
}

func lifecycleEventInstances(events []daemon.LifecycleEvent) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Instance)
	}
	return out
}

func sendTargets(rows []sendJSON) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.To)
	}
	return out
}

func stringInSlice(needle string, haystack []string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

func TestTeamHealthJobsAreTeamScoped(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.other]
agent = "other"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	teamJob := &job.Job{
		ID:         "squ-901",
		Ticket:     "SQU-901",
		Target:     "worker",
		Pipeline:   "ticket_to_pr",
		Status:     job.StatusFailed,
		LastStatus: "tests failed",
		CreatedAt:  now,
		UpdatedAt:  now,
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusFailed},
		},
	}
	if err := job.Write(teamDir, teamJob); err != nil {
		t.Fatalf("write team job: %v", err)
	}
	unrelated := &job.Job{
		ID:         "oth-1",
		Ticket:     "OTH-1",
		Target:     "other",
		Status:     job.StatusFailed,
		LastStatus: "unrelated failed",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := job.Write(teamDir, unrelated); err != nil {
		t.Fatalf("write unrelated job: %v", err)
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), &daemon.QueueItem{
		ID:             "q-team-dead",
		State:          daemon.QueueStateDead,
		EventType:      "agent.dispatch",
		Instance:       "worker",
		InstanceID:     "worker-squ-901",
		Payload:        map[string]any{"job_id": "squ-901", "target": "worker", "ticket": "SQU-901"},
		Attempts:       daemon.MaxQueueAttempts,
		LastError:      "spawn failed",
		QueuedAt:       now.Add(-time.Hour),
		UpdatedAt:      now,
		DeadLetteredAt: now,
	}); err != nil {
		t.Fatalf("write team queue item: %v", err)
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), &daemon.QueueItem{
		ID:             "q-other-dead",
		State:          daemon.QueueStateDead,
		EventType:      "agent.dispatch",
		Instance:       "other",
		InstanceID:     "other-oth-1",
		Payload:        map[string]any{"job_id": "oth-1", "target": "other", "ticket": "OTH-1"},
		Attempts:       daemon.MaxQueueAttempts,
		LastError:      "spawn failed",
		QueuedAt:       now.Add(-time.Hour),
		UpdatedAt:      now,
		DeadLetteredAt: now,
	}); err != nil {
		t.Fatalf("write unrelated queue item: %v", err)
	}
	writeQuarantinedQueueItem(t, teamDir, "20260619T020000.000000000Z", daemon.QueueStateDead, &daemon.QueueItem{
		ID:         "q-team-health-quarantined",
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-901",
		Payload:    map[string]any{"job_id": "squ-901", "target": "worker", "ticket": "SQU-901"},
		QueuedAt:   now.Add(-2 * time.Hour),
		UpdatedAt:  now.Add(-2 * time.Hour),
	})
	writeQuarantinedQueueItem(t, teamDir, "20260619T020000.000000000Z", daemon.QueueStateDead, &daemon.QueueItem{
		ID:         "q-other-health-quarantined",
		EventType:  "agent.dispatch",
		Instance:   "other",
		InstanceID: "other-oth-1",
		Payload:    map[string]any{"job_id": "oth-1", "target": "other", "ticket": "OTH-1"},
		QueuedAt:   now.Add(-2 * time.Hour),
		UpdatedAt:  now.Add(-2 * time.Hour),
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "health", "delivery", "--repo", root, "--jobs", "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("team health unexpectedly succeeded")
	}
	var code ExitCode
	if !errors.As(err, &code) || int(code) != 1 {
		t.Fatalf("err = %v, want exit 1\nstderr=%s", err, stderr.String())
	}
	var snapshot teamHealthSnapshot
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode team health: %v\nbody=%s", err, out.String())
	}
	if snapshot.Team.Name != "delivery" || snapshot.Health == nil || snapshot.Health.Healthy {
		t.Fatalf("team health snapshot = %+v", snapshot)
	}
	if snapshot.Health.Jobs == nil || snapshot.Health.Jobs.Summary.Total != 1 || snapshot.Health.Jobs.Summary.Failed != 1 {
		t.Fatalf("team job summary = %+v", snapshot.Health.Jobs)
	}
	if snapshot.Health.Queue.Dead != 1 || snapshot.Health.Queue.Quarantined != 1 || snapshot.Health.Queue.QuarantineRestorable != 1 || snapshot.Health.Queue.QuarantineUnrestorable != 0 {
		t.Fatalf("team queue summary = %+v", snapshot.Health.Queue)
	}
	if len(snapshot.Health.PipelineStatus) != 1 || snapshot.Health.PipelineStatus[0].Pipeline != "ticket_to_pr" || snapshot.Health.PipelineStatus[0].FailedSteps != 1 {
		t.Fatalf("pipeline status = %+v", snapshot.Health.PipelineStatus)
	}
	for _, issue := range snapshot.Health.Issues {
		if issue.Job == "oth-1" || strings.Contains(issue.Message, "OTH-1") {
			t.Fatalf("unrelated issue leaked into team health: %+v", snapshot.Health.Issues)
		}
	}
	codes := map[string]bool{}
	var sawTeamJob bool
	var sawScopedQueueAction bool
	var sawQuarantineAction bool
	var sawScopedPipelineAction bool
	for _, issue := range snapshot.Health.Issues {
		codes[issue.Code] = true
		if issue.Code == "job_attention" && issue.Job == "squ-901" {
			sawTeamJob = true
		}
		if issue.Code == "pipeline_failed_step" &&
			containsString(issue.Actions, "agent-team team retry delivery --dry-run --dispatch --preview-routes") &&
			containsString(issue.Actions, "agent-team team repair delivery --retry-pipelines --dry-run --preview-routes") {
			sawScopedPipelineAction = true
		}
		if issue.Code == "queue_dead_letter" && containsString(issue.Actions, "agent-team team queue retry delivery --all --job squ-901") {
			sawScopedQueueAction = true
		}
		if issue.Code == "queue_quarantined" && containsString(issue.Actions, "agent-team team queue quarantine delivery") && containsString(issue.Actions, "agent-team team queue quarantine delivery --restorable") && containsString(issue.Actions, "agent-team team snapshot delivery --json") {
			sawQuarantineAction = true
		}
	}
	for _, want := range []string{"daemon_not_running", "queue_dead_letter", "queue_quarantined", "job_attention", "pipeline_failed_step"} {
		if !codes[want] {
			t.Fatalf("issues = %+v, missing %s", snapshot.Health.Issues, want)
		}
	}
	if !sawTeamJob {
		t.Fatalf("issues = %+v, missing team job_attention", snapshot.Health.Issues)
	}
	if !sawScopedQueueAction {
		t.Fatalf("issues = %+v, missing scoped team queue retry action", snapshot.Health.Issues)
	}
	if !sawScopedPipelineAction {
		t.Fatalf("issues = %+v, missing scoped team pipeline retry action", snapshot.Health.Issues)
	}
	if !sawQuarantineAction {
		t.Fatalf("issues = %+v, missing scoped quarantine action", snapshot.Health.Issues)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"team", "health", "delivery", "--repo", root, "--jobs"})
	if err := text.Execute(); err == nil {
		t.Fatal("team health text unexpectedly succeeded")
	}
	for _, want := range []string{"Team: delivery", "health: unhealthy", "jobs: total=1", "quarantined=1 restorable=1 unrestorable=0", "pipeline_failed_step", "queue_dead_letter", "queue_quarantined", "agent-team team retry delivery --dry-run --dispatch --preview-routes", "agent-team team repair delivery --retry-pipelines --dry-run --preview-routes", "agent-team team queue quarantine delivery --restorable"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("team health text missing %q:\n%s", want, textOut.String())
		}
	}
	if strings.Contains(textOut.String(), "oth-1") || strings.Contains(textOut.String(), "OTH-1") {
		t.Fatalf("team health text included unrelated job:\n%s", textOut.String())
	}

	defaultHealth := NewRootCmd()
	defaultOut, defaultErr := &bytes.Buffer{}, &bytes.Buffer{}
	defaultHealth.SetOut(defaultOut)
	defaultHealth.SetErr(defaultErr)
	defaultHealth.SetArgs([]string{"team", "health", "delivery", "--repo", root, "--json"})
	if err := defaultHealth.Execute(); err == nil {
		t.Fatal("team health default unexpectedly succeeded")
	}
	var defaultSnapshot teamHealthSnapshot
	if err := json.Unmarshal(defaultOut.Bytes(), &defaultSnapshot); err != nil {
		t.Fatalf("decode default team health: %v\nbody=%s\nstderr=%s", err, defaultOut.String(), defaultErr.String())
	}
	if defaultSnapshot.Health == nil || defaultSnapshot.Health.Queue.Quarantined != 1 || defaultSnapshot.Health.Queue.QuarantineRestorable != 1 || defaultSnapshot.Health.Queue.QuarantineUnrestorable != 0 || defaultSnapshot.Health.Jobs != nil {
		t.Fatalf("default team health = %+v", defaultSnapshot.Health)
	}

	formatted := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatted.SetOut(formatOut)
	formatted.SetErr(formatErr)
	formatted.SetArgs([]string{"team", "health", "delivery", "--repo", root, "--jobs", "--format", "{{.Team.Name}} {{.Health.Healthy}} {{.Health.Jobs.Summary.Failed}} {{.Health.Queue.Dead}} {{.Health.Queue.Quarantined}}"})
	if err := formatted.Execute(); err == nil {
		t.Fatal("team health format unexpectedly succeeded")
	} else if !errors.As(err, &code) || int(code) != 1 {
		t.Fatalf("team health format err = %v, want exit 1\nstderr=%s", err, formatErr.String())
	}
	if got, want := formatOut.String(), "delivery false 1 1 1\n"; got != want {
		t.Fatalf("team health format output = %q, want %q", got, want)
	}
}

func TestScopeTeamHealthIssueActions(t *testing.T) {
	result := &healthResult{Issues: []healthIssue{
		{Code: "declared_missing", Actions: []string{"agent-team sync --dry-run", "agent-team daemon start"}},
		{Code: "queue_dead_letter", Actions: []string{"agent-team queue retry --all --dry-run"}},
		{Code: "instance_crashed", Actions: []string{"agent-team runtime resume-plan worker-squ-1 --status crashed"}},
		{Code: "instance_crashed", Actions: []string{"agent-team job resume-plan squ-1 --status crashed"}},
	}}
	scopeTeamHealthIssueActions(result, "delivery")
	if got := result.Issues[0].Actions; !containsString(got, "agent-team team sync delivery --dry-run") || containsString(got, "agent-team sync --dry-run") || !containsString(got, "agent-team daemon start") {
		t.Fatalf("declared actions = %+v", got)
	}
	if got := result.Issues[1].Actions; !containsString(got, "agent-team queue retry --all --dry-run") {
		t.Fatalf("queue actions changed unexpectedly: %+v", got)
	}
	if got := result.Issues[2].Actions; !containsString(got, "agent-team team runtime resume-plan delivery --status crashed") || containsString(got, "agent-team runtime resume-plan worker-squ-1 --status crashed") {
		t.Fatalf("runtime actions = %+v", got)
	}
	if got := result.Issues[3].Actions; !containsString(got, "agent-team job resume-plan squ-1 --status crashed") {
		t.Fatalf("job runtime action should remain job-scoped: %+v", got)
	}
}

func TestTeamHealthFiltersByRuntime(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"manager", "worker"} {
		if err := os.MkdirAll(filepath.Join(teamDir, "agents", name), 0o755); err != nil {
			t.Fatalf("mkdir agent %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"

[instances.worker]
agent = "worker"
ephemeral = true

[instances.build-worker]
agent = "worker"
ephemeral = true

[teams.delivery]
instances = ["manager", "worker"]

[teams.platform]
instances = ["build-worker"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "worker-squ-901", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
		{Instance: "build-worker-1", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(-time.Hour)},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "health", "delivery", "--repo", root, "--runtime", "codex", "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("team health runtime unexpectedly succeeded")
	}
	var code ExitCode
	if !errors.As(err, &code) || int(code) != 1 {
		t.Fatalf("err = %v, want exit 1\nstderr=%s", err, stderr.String())
	}
	var snapshot teamHealthSnapshot
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode team health runtime: %v\nbody=%s", err, out.String())
	}
	if snapshot.Health == nil || snapshot.Health.Summary.Total != 1 || snapshot.Health.Summary.Running != 1 {
		t.Fatalf("team health runtime summary = %+v", snapshot.Health)
	}
	if got := healthInstanceNames(snapshot.Health.Instances); strings.Join(got, ",") != "worker-squ-901" {
		t.Fatalf("team health runtime instances = %v", got)
	}
	if strings.Contains(out.String(), "build-worker-1") {
		t.Fatalf("team health runtime leaked unrelated instance:\n%s", out.String())
	}

	badRuntime := NewRootCmd()
	badRuntime.SetOut(&bytes.Buffer{})
	badRuntimeErr := &bytes.Buffer{}
	badRuntime.SetErr(badRuntimeErr)
	badRuntime.SetArgs([]string{"team", "health", "delivery", "--repo", root, "--runtime", "llama"})
	if err := badRuntime.Execute(); err == nil {
		t.Fatal("team health accepted unknown runtime")
	}
	if !strings.Contains(badRuntimeErr.String(), "unknown --runtime") {
		t.Fatalf("bad runtime stderr = %q", badRuntimeErr.String())
	}
}

func TestTeamHealthOutputValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"team", "health", "delivery", "--quiet", "--json"}, "choose one of --quiet or --json"},
		{[]string{"team", "health", "delivery", "--format", "{{.Team.Name}}", "--json"}, "--format cannot be combined"},
		{[]string{"team", "health", "delivery", "--format", "{{.Team.Name}}", "--quiet"}, "--format cannot be combined"},
		{[]string{"team", "health", "delivery", "--format", "{{"}, "invalid --format template"},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		var code ExitCode
		if !errors.As(err, &code) || int(code) != 2 {
			t.Fatalf("%v: err = %v, want exit 2", tc.args, err)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
		if out.Len() != 0 {
			t.Fatalf("%v: validation wrote stdout: %q", tc.args, out.String())
		}
	}
}

func TestTeamStatusRejectsNegativeInterval(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "status", "delivery", "--watch", "--interval", "-1s"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("team status negative interval succeeded")
	}
	if !strings.Contains(stderr.String(), "--interval must be >= 0") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestTeamTriageRejectsFormatCombinations(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "format with json",
			args: []string{"team", "triage", "delivery", "--format", "{{.Summary.Total}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "format with watch",
			args: []string{"team", "triage", "delivery", "--format", "{{.Summary.Total}}", "--watch"},
			want: "--format cannot be combined",
		},
		{
			name: "invalid format",
			args: []string{"team", "triage", "delivery", "--format", "{{"},
			want: "invalid --format template",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("team triage invalid format succeeded: stdout=%s", out.String())
			}
			var code ExitCode
			if !errors.As(err, &code) || int(code) != 2 {
				t.Fatalf("team triage err = %v, want exit code 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestTeamStatusRejectsFormatCombinations(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "format with json",
			args: []string{"team", "status", "delivery", "--format", "{{.Team.Name}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "format with watch",
			args: []string{"team", "status", "delivery", "--format", "{{.Team.Name}}", "--watch"},
			want: "--format cannot be combined",
		},
		{
			name: "invalid format",
			args: []string{"team", "status", "delivery", "--format", "{{"},
			want: "invalid --format template",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("team status invalid format succeeded: stdout=%s", out.String())
			}
			var code ExitCode
			if !errors.As(err, &code) || int(code) != 2 {
				t.Fatalf("team status err = %v, want exit code 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestTeamPsRejectsNegativeInterval(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "ps", "delivery", "--watch", "--interval", "-1s"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("team ps negative interval succeeded")
	}
	if !strings.Contains(stderr.String(), "--interval must be >= 0") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestTeamPsRejectsFormatCombinations(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "format with json",
			args: []string{"team", "ps", "delivery", "--format", "{{.Instance}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "format with watch",
			args: []string{"team", "ps", "delivery", "--format", "{{.Instance}}", "--watch"},
			want: "--format cannot be combined",
		},
		{
			name: "invalid format",
			args: []string{"team", "ps", "delivery", "--format", "{{"},
			want: "invalid --format template",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("team ps invalid format succeeded: stdout=%s", out.String())
			}
			var code ExitCode
			if !errors.As(err, &code) || int(code) != 2 {
				t.Fatalf("team ps err = %v, want exit code 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func hasTeamDoctorFinding(findings []teamDoctorFinding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func hasTeamDoctorFindingForTeam(findings []teamDoctorFinding, teamName, code string) bool {
	for _, finding := range findings {
		if finding.Team == teamName && finding.Code == code {
			return true
		}
	}
	return false
}
