package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/topology"
)

const topoFixture = `
[instances.manager]
agent = "manager"

[[instances.manager.triggers]]
event = "user_invocation"

[instances.worker]
agent     = "worker"
ephemeral = true
replicas  = 2

[[instances.worker.triggers]]
event        = "agent.dispatch"
match.target = "worker"
`

// topoTestEnv stands up an in-process daemon Handler with a topology loaded
// from `instances.toml` written to teamDir, plus a daemonClient pointed at
// it. Mirrors channelTestEnv's shape.
type topoTestEnv struct {
	client  *daemonClient
	srv     *httptest.Server
	teamDir string
	mgr     *daemon.InstanceManager
}

func newTopoTestEnv(t *testing.T) *topoTestEnv {
	t.Helper()
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		t.Fatalf("load topology: %v", err)
	}
	mgr := daemon.NewInstanceManager(t.TempDir(), nil)
	resolver := daemon.NewEventResolver(mgr, teamDir, top)
	srv := httptest.NewServer(daemon.Handler(mgr, nil, resolver, teamDir))
	c := &daemonClient{
		hc:      &http.Client{Timeout: 0},
		baseURL: srv.URL,
		teamDir: teamDir,
	}
	t.Cleanup(srv.Close)
	return &topoTestEnv{client: c, srv: srv, teamDir: teamDir, mgr: mgr}
}

func TestClient_Topology(t *testing.T) {
	env := newTopoTestEnv(t)
	res, err := env.client.Topology()
	if err != nil {
		t.Fatalf("topology: %v", err)
	}
	if len(res.Instances) != 2 {
		t.Errorf("instances: %v", res.Instances)
	}
	for _, i := range res.Instances {
		if i.Name == "worker" {
			if !i.Ephemeral || i.Replicas != 2 {
				t.Errorf("worker: %+v", i)
			}
		}
	}
}

func TestClient_TopologyReload(t *testing.T) {
	env := newTopoTestEnv(t)
	// Replace the file and reload.
	if err := os.WriteFile(filepath.Join(env.teamDir, "instances.toml"), []byte(`
[instances.solo]
agent = "manager"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := env.client.TopologyReload()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(res.Instances) != 1 || res.Instances[0].Name != "solo" {
		t.Errorf("after reload: %v", res.Instances)
	}
}

func TestTopologyReloadCommandJSONAndFormat(t *testing.T) {
	root := t.TempDir()
	if eval, err := filepath.EvalSymlinks(root); err == nil {
		root = eval
	}
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	restorePIDLiveCheck := daemon.SetPidLiveCheckForTest(func(pid int) bool { return pid == os.Getpid() })
	t.Cleanup(restorePIDLiveCheck)
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), nil)
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.solo]
agent = "manager"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	jsonCmd := NewRootCmd()
	jsonOut, jsonErr := &bytes.Buffer{}, &bytes.Buffer{}
	jsonCmd.SetOut(jsonOut)
	jsonCmd.SetErr(jsonErr)
	jsonCmd.SetArgs([]string{"topology", "reload", "--target", root, "--json"})
	if err := jsonCmd.Execute(); err != nil {
		t.Fatalf("topology reload --json: %v\nstderr=%s", err, jsonErr.String())
	}
	var body topologyResponse
	if err := json.Unmarshal(jsonOut.Bytes(), &body); err != nil {
		t.Fatalf("decode topology reload json: %v\nbody=%s", err, jsonOut.String())
	}
	if len(body.Instances) != 1 || body.Instances[0].Name != "solo" {
		t.Fatalf("reload json = %+v, want solo", body.Instances)
	}

	formatCmd := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatCmd.SetOut(formatOut)
	formatCmd.SetErr(formatErr)
	formatCmd.SetArgs([]string{"topology", "reload", "--target", root, "--format", "{{len .Instances}} {{(index .Instances 0).Name}}"})
	if err := formatCmd.Execute(); err != nil {
		t.Fatalf("topology reload --format: %v\nstderr=%s", err, formatErr.String())
	}
	if got := strings.TrimSpace(formatOut.String()); got != "1 solo" {
		t.Fatalf("format output = %q, want %q", got, "1 solo")
	}

	badCmd := NewRootCmd()
	badOut, badErr := &bytes.Buffer{}, &bytes.Buffer{}
	badCmd.SetOut(badOut)
	badCmd.SetErr(badErr)
	badCmd.SetArgs([]string{"topology", "reload", "--target", root, "--json", "--format", "{{len .Instances}}"})
	err := badCmd.Execute()
	if err == nil {
		t.Fatalf("topology reload accepted --json with --format; stdout=%s", badOut.String())
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if !strings.Contains(badErr.String(), "--format cannot be combined with --json") {
		t.Fatalf("stderr = %q", badErr.String())
	}
}

func TestClient_PublishEvent_Persistent(t *testing.T) {
	env := newTopoTestEnv(t)
	res, err := env.client.PublishEvent("user_invocation", map[string]any{"name": "manager"})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if len(res.Matched) != 1 || res.Matched[0] != "manager" {
		t.Errorf("matched: %v", res.Matched)
	}
	if len(res.Messaged) != 1 {
		t.Errorf("messaged: %v", res.Messaged)
	}
}

func TestClient_PublishEvent_NoMatch(t *testing.T) {
	env := newTopoTestEnv(t)
	res, err := env.client.PublishEvent("ticket_webhook", map[string]any{"project": "Mobile"})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if len(res.Matched) != 0 {
		t.Errorf("expected no matches, got %v", res.Matched)
	}
}

func TestTopologyShowIncludesPipelines(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := topoFixture + `
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
`
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"topology", "show", "--target", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("topology show: %v\nstderr=%s", err, stderr.String())
	}
	for _, want := range []string{"PIPELINE", "ticket_to_pr", "ticket.created", "implement"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("topology output missing %q:\n%s", want, out.String())
		}
	}
}

func TestTopologyShowIncludesSchedules(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := topoFixture + `
[schedules.nightly]
every = "1h"
payload.workspace = "repo"
`
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"topology", "show", "--target", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("topology show: %v\nstderr=%s", err, stderr.String())
	}
	for _, want := range []string{"SCHEDULE", "nightly", "1h0m0s", "workspace"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("topology output missing %q:\n%s", want, out.String())
		}
	}
}

func TestTopologySummaryReportsInventoryAndDoctorCounts(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := topoFixture + `
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[schedules.nightly]
every = "1h"

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
schedules = ["nightly"]
`
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"topology", "summary", "--target", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("topology summary json: %v\nstderr=%s", err, stderr.String())
	}
	var summary topologySummary
	if err := json.Unmarshal(out.Bytes(), &summary); err != nil {
		t.Fatalf("decode topology summary: %v\nbody=%s", err, out.String())
	}
	if !summary.OK || summary.Instances != 2 || summary.Persistent != 1 || summary.Ephemeral != 1 || summary.Triggers != 2 || summary.Pipelines != 1 || summary.PipelineSteps != 1 || summary.Schedules != 1 || summary.Teams != 1 {
		t.Fatalf("summary = %+v", summary)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"topology", "summary", "--target", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("topology summary text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"topology: ok", "instances: total=2 persistent=1 ephemeral=1 triggers=2", "pipelines: total=1 steps=1 problems=0 warnings=0", "teams: total=1 problems=0 warnings=1"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("summary text missing %q:\n%s", want, textOut.String())
		}
	}
}

func TestTopologyGraphRendersFullTopology(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := topoFixture + `
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
label = "Code review"
description = "Review branch and PR state."
instructions = "Check tests, summarize risks, and decide whether the PR can proceed."
target = "manager"
workspace = "repo"
runtime = "codex"
runtime_bin = "codex-dev"
after = ["implement"]
optional = true
timeout = "45m"
max_attempts = 3

[schedules.nightly]
every = "1h"

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
schedules = ["nightly"]
`
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"topology", "graph", "--target", root, "--routes", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("topology graph json: %v\nstderr=%s", err, stderr.String())
	}
	var graph topologyGraph
	if err := json.Unmarshal(out.Bytes(), &graph); err != nil {
		t.Fatalf("decode topology graph: %v\nbody=%s", err, out.String())
	}
	if len(graph.Instances) != 2 || len(graph.Pipelines) != 1 || len(graph.Schedules) != 1 || len(graph.Teams) != 1 {
		t.Fatalf("topology graph summary = %+v", graph)
	}
	foundDispatchEdge := false
	foundTeamEdge := false
	for _, edge := range graph.Edges {
		if edge.From == "pipeline:ticket_to_pr:step:implement" && edge.To == "instance:worker" && edge.Kind == "dispatches_to" {
			foundDispatchEdge = true
		}
		if edge.From == "team:delivery" && edge.To == "pipeline:ticket_to_pr" && edge.Kind == "owns_pipeline" {
			foundTeamEdge = true
		}
	}
	if !foundDispatchEdge || !foundTeamEdge {
		t.Fatalf("topology graph edges missing dispatch=%t team=%t: %+v", foundDispatchEdge, foundTeamEdge, graph.Edges)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"topology", "graph", "--target", root, "--routes"})
	if err := text.Execute(); err != nil {
		t.Fatalf("topology graph text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{
		"Topology",
		"Teams:",
		"delivery instances=2 pipelines=1 schedules=1",
		"implement target=worker after=- routes=worker",
		`review target=manager after=implement workspace=repo runtime=codex:codex-dev label="Code review" description="Review branch and PR state." instructions="Check tests, summarize risks, and decide whether the PR can proceed." optional=true timeout=45m0s max_attempts=3`,
		"dispatches_to",
	} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("topology graph text missing %q:\n%s", want, textOut.String())
		}
	}

	mermaid := NewRootCmd()
	mermaidOut, mermaidErr := &bytes.Buffer{}, &bytes.Buffer{}
	mermaid.SetOut(mermaidOut)
	mermaid.SetErr(mermaidErr)
	mermaid.SetArgs([]string{"topology", "graph", "--target", root, "--format", "mermaid"})
	if err := mermaid.Execute(); err != nil {
		t.Fatalf("topology graph mermaid: %v\nstderr=%s", err, mermaidErr.String())
	}
	if !strings.Contains(mermaidOut.String(), "flowchart TD") || !strings.Contains(mermaidOut.String(), "team_delivery") {
		t.Fatalf("topology graph mermaid output:\n%s", mermaidOut.String())
	}

	dot := NewRootCmd()
	dotOut, dotErr := &bytes.Buffer{}, &bytes.Buffer{}
	dot.SetOut(dotOut)
	dot.SetErr(dotErr)
	dot.SetArgs([]string{"topology", "graph", "--target", root, "--format", "dot"})
	if err := dot.Execute(); err != nil {
		t.Fatalf("topology graph dot: %v\nstderr=%s", err, dotErr.String())
	}
	if !strings.Contains(dotOut.String(), `digraph "topology"`) || !strings.Contains(dotOut.String(), `"topology" -> "team:delivery"`) {
		t.Fatalf("topology graph dot output:\n%s", dotOut.String())
	}

	pipelineJob := &job.Job{
		ID:        "squ-971",
		Ticket:    "SQU-971",
		Target:    "worker",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusDone},
			{ID: "review", Target: "manager", Status: job.StatusQueued, After: []string{"implement"}},
		},
	}
	if err := job.Write(teamDir, pipelineJob); err != nil {
		t.Fatalf("write topology graph job: %v", err)
	}
	wantAction := "agent-team pipeline tick ticket_to_pr --dry-run --preview-routes"

	jobJSON := NewRootCmd()
	jobJSONOut, jobJSONErr := &bytes.Buffer{}, &bytes.Buffer{}
	jobJSON.SetOut(jobJSONOut)
	jobJSON.SetErr(jobJSONErr)
	jobJSON.SetArgs([]string{"topology", "graph", "--target", root, "--job", "squ-971", "--json"})
	if err := jobJSON.Execute(); err != nil {
		t.Fatalf("topology graph job json: %v\nstderr=%s", err, jobJSONErr.String())
	}
	var jobGraph topologyGraph
	if err := json.Unmarshal(jobJSONOut.Bytes(), &jobGraph); err != nil {
		t.Fatalf("decode topology graph job: %v\nbody=%s", err, jobJSONOut.String())
	}
	if len(jobGraph.Pipelines) != 1 || jobGraph.Pipelines[0].JobID != "squ-971" || jobGraph.Pipelines[0].JobState != "queued" {
		t.Fatalf("topology graph job overlay = %+v", jobGraph.Pipelines)
	}
	jobNodes := map[string]pipelineGraphNode{}
	for _, node := range jobGraph.Pipelines[0].Nodes {
		jobNodes[node.ID] = node
	}
	if jobNodes["review"].State != "ready" || jobNodes["review"].StepStatus != job.StatusQueued || !containsString(jobNodes["review"].Actions, wantAction) {
		t.Fatalf("topology graph review overlay = %+v", jobNodes["review"])
	}

	jobText := NewRootCmd()
	jobTextOut, jobTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	jobText.SetOut(jobTextOut)
	jobText.SetErr(jobTextErr)
	jobText.SetArgs([]string{"topology", "graph", "--target", root, "--job", "squ-971"})
	if err := jobText.Execute(); err != nil {
		t.Fatalf("topology graph job text: %v\nstderr=%s", err, jobTextErr.String())
	}
	for _, want := range []string{
		`ticket_to_pr trigger=ticket.created steps=2 job=squ-971 ticket=SQU-971 status=running state=queued`,
		`review target=manager after=implement workspace=repo runtime=codex:codex-dev label="Code review" description="Review branch and PR state." instructions="Check tests, summarize risks, and decide whether the PR can proceed." optional=true timeout=45m0s max_attempts=3 state=ready step_status=queued ready=true message="ready to advance" actions="agent-team pipeline tick ticket_to_pr --dry-run --preview-routes"`,
	} {
		if !strings.Contains(jobTextOut.String(), want) {
			t.Fatalf("topology graph job text missing %q:\n%s", want, jobTextOut.String())
		}
	}

	jobMermaid := NewRootCmd()
	jobMermaidOut, jobMermaidErr := &bytes.Buffer{}, &bytes.Buffer{}
	jobMermaid.SetOut(jobMermaidOut)
	jobMermaid.SetErr(jobMermaidErr)
	jobMermaid.SetArgs([]string{"topology", "graph", "--target", root, "--job", "squ-971", "--format", "mermaid"})
	if err := jobMermaid.Execute(); err != nil {
		t.Fatalf("topology graph job mermaid: %v\nstderr=%s", err, jobMermaidErr.String())
	}
	if !strings.Contains(jobMermaidOut.String(), "job: squ-971") || !strings.Contains(jobMermaidOut.String(), "ticket: SQU-971") || !strings.Contains(jobMermaidOut.String(), "state: ready") || !strings.Contains(jobMermaidOut.String(), "actions: "+wantAction) {
		t.Fatalf("topology graph job mermaid output:\n%s", jobMermaidOut.String())
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"topology", "graph", "--target", root, "--job", "squ-971", "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("topology graph commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommand := strings.Join(scopedOperatorActions([]string{wantAction}, operatorCommandScope{Repo: root, Set: true}), "\n") + "\n"
	if commandsOut.String() != wantCommand {
		t.Fatalf("topology graph commands = %q, want %q", commandsOut.String(), wantCommand)
	}

	outsideJob := &job.Job{
		ID:        "squ-972",
		Ticket:    "SQU-972",
		Target:    "worker",
		Pipeline:  "missing_pipeline",
		Status:    job.StatusRunning,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := job.Write(teamDir, outsideJob); err != nil {
		t.Fatalf("write topology graph outside job: %v", err)
	}
	mismatch := NewRootCmd()
	mismatchOut, mismatchErr := &bytes.Buffer{}, &bytes.Buffer{}
	mismatch.SetOut(mismatchOut)
	mismatch.SetErr(mismatchErr)
	mismatch.SetArgs([]string{"topology", "graph", "--target", root, "--job", "squ-972"})
	err := mismatch.Execute()
	if err == nil {
		t.Fatal("topology graph accepted a job from an undeclared pipeline")
	}
	var mismatchCode ExitCode
	if !errors.As(err, &mismatchCode) || int(mismatchCode) != 1 {
		t.Fatalf("topology graph mismatch err = %v, want exit 1", err)
	}
	if !strings.Contains(mismatchErr.String(), `job "squ-972" belongs to pipeline "missing_pipeline", not a declared pipeline`) {
		t.Fatalf("topology graph mismatch stderr = %q", mismatchErr.String())
	}

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "commands json",
			args: []string{"topology", "graph", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "commands format",
			args: []string{"topology", "graph", "--commands", "--format", "text"},
			want: "--commands cannot be combined with --format",
		},
	} {
		t.Run("topology-graph-validation-"+tc.name, func(t *testing.T) {
			invalid := NewRootCmd()
			invalidOut, invalidErr := &bytes.Buffer{}, &bytes.Buffer{}
			invalid.SetOut(invalidOut)
			invalid.SetErr(invalidErr)
			invalid.SetArgs(tc.args)
			err := invalid.Execute()
			if err == nil {
				t.Fatalf("topology graph accepted invalid args %v", tc.args)
			}
			var code ExitCode
			if !errors.As(err, &code) || int(code) != 2 {
				t.Fatalf("topology graph invalid args err = %v, want exit 2", err)
			}
			if !strings.Contains(invalidErr.String(), tc.want) {
				t.Fatalf("topology graph invalid args stderr = %q, want %q", invalidErr.String(), tc.want)
			}
			if invalidOut.Len() != 0 {
				t.Fatalf("topology graph invalid args wrote stdout: %q", invalidOut.String())
			}
		})
	}
}

func TestTopologySummaryReportsAttention(t *testing.T) {
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

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "other"

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
	cmd.SetArgs([]string{"topology", "summary", "--target", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("topology summary attention: %v\nstderr=%s", err, stderr.String())
	}
	var summary topologySummary
	if err := json.Unmarshal(out.Bytes(), &summary); err != nil {
		t.Fatalf("decode topology summary attention: %v\nbody=%s", err, out.String())
	}
	if summary.OK || summary.TeamProblems != 1 || summary.PipelineProblems != 0 {
		t.Fatalf("summary = %+v", summary)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"topology", "summary", "--target", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("topology summary attention text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"topology: attention", "teams: total=1 problems=1 warnings=0"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("summary text missing %q:\n%s", want, textOut.String())
		}
	}
}

func TestEventPublishJSON(t *testing.T) {
	target, err := os.MkdirTemp("/tmp", "agent-team-event-json-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(target)
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), nil)
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"event", "publish", "user_invocation", "--payload", `{"name":"manager"}`, "--json", "--target", target})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("event publish --json: %v\nstderr=%s", err, stderr.String())
	}
	var body eventResponse
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode event publish json: %v\nbody=%s", err, out.String())
	}
	if len(body.Matched) != 1 || body.Matched[0] != "manager" || len(body.Messaged) != 1 || body.Messaged[0] != "manager" {
		t.Fatalf("body = %+v, want manager matched and messaged", body)
	}
}

func TestEventPublishPayloadFileDash(t *testing.T) {
	target, err := os.MkdirTemp("/tmp", "agent-team-event-stdin-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(target)
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), nil)
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()
	prev := intakeInput
	intakeInput = strings.NewReader(`{"name":"manager"}`)
	t.Cleanup(func() { intakeInput = prev })

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"event", "publish", "user_invocation", "--payload-file", "-", "--json", "--target", target})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("event publish stdin --json: %v\nstderr=%s", err, stderr.String())
	}
	var body eventResponse
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode event publish stdin json: %v\nbody=%s", err, out.String())
	}
	if len(body.Matched) != 1 || body.Matched[0] != "manager" || len(body.Messaged) != 1 || body.Messaged[0] != "manager" {
		t.Fatalf("body = %+v, want manager matched and messaged", body)
	}

	intakeInput = strings.NewReader(`{"name":"manager"}`)
	commandsCmd := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commandsCmd.SetOut(commandsOut)
	commandsCmd.SetErr(commandsErr)
	commandsCmd.SetArgs([]string{"--repo", target, "event", "publish", "user_invocation", "--payload-file", "-", "--dry-run", "--commands"})
	if err := commandsCmd.Execute(); err != nil {
		t.Fatalf("event publish stdin --commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{
		"agent-team",
		"event",
		"publish",
		"user_invocation",
		"--repo",
		target,
		"--payload",
		`{"name":"manager"}`,
	}), " ")
	if got := strings.TrimSpace(commandsOut.String()); got != wantCommand {
		t.Fatalf("event publish stdin --commands output = %q, want %q", got, wantCommand)
	}
}

func TestEventPublishDryRunUsesLocalTopology(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
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
	cmd.SetArgs([]string{"event", "publish", "user_invocation", "--payload", `{"name":"manager"}`, "--dry-run", "--json", "--target", target})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("event publish dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var preview eventPublishPreview
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil {
		t.Fatalf("decode event publish dry-run json: %v\nbody=%s", err, out.String())
	}
	if !preview.DryRun || preview.Type != "user_invocation" || len(preview.Matched) != 1 || preview.Matched[0] != "manager" {
		t.Fatalf("preview = %+v", preview)
	}

	commandsCmd := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commandsCmd.SetOut(commandsOut)
	commandsCmd.SetErr(commandsErr)
	commandsCmd.SetArgs([]string{"event", "publish", "user_invocation", "--payload", `{"name":"manager"}`, "--dry-run", "--commands", "--target", target})
	if err := commandsCmd.Execute(); err != nil {
		t.Fatalf("event publish dry-run --commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{
		"agent-team",
		"event",
		"publish",
		"user_invocation",
		"--repo",
		target,
		"--payload",
		`{"name":"manager"}`,
	}), " ")
	if got := strings.TrimSpace(commandsOut.String()); got != wantCommand {
		t.Fatalf("event publish dry-run --commands output = %q, want %q", got, wantCommand)
	}

	noRouteCmd := NewRootCmd()
	noRouteOut, noRouteErr := &bytes.Buffer{}, &bytes.Buffer{}
	noRouteCmd.SetOut(noRouteOut)
	noRouteCmd.SetErr(noRouteErr)
	noRouteCmd.SetArgs([]string{"event", "publish", "unknown.event", "--payload", `{"name":"worker"}`, "--dry-run", "--commands", "--target", target})
	if err := noRouteCmd.Execute(); err != nil {
		t.Fatalf("event publish dry-run --commands no route: %v\nstderr=%s", err, noRouteErr.String())
	}
	if got := noRouteOut.String(); got != "" {
		t.Fatalf("event publish dry-run --commands no route output = %q, want empty", got)
	}
}

func TestEventPublishDryRunPreviewsPipelineJob(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := topoFixture + `
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
`
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"event", "publish", "ticket.created", "--payload", `{"ticket":"SQU-130","kickoff":"Implement it"}`, "--dry-run", "--json", "--target", target})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("event publish dry-run pipeline: %v\nstderr=%s", err, stderr.String())
	}
	var preview eventPublishPreview
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil {
		t.Fatalf("decode event publish dry-run pipeline json: %v\nbody=%s", err, out.String())
	}
	if !preview.DryRun || preview.Type != "ticket.created" || len(preview.Pipelines) != 1 || preview.Pipelines[0] != "ticket_to_pr" {
		t.Fatalf("preview = %+v", preview)
	}
	if len(preview.PipelineJobs) != 1 {
		t.Fatalf("pipeline job preview = %+v", preview)
	}
	pipelineJob := preview.PipelineJobs[0]
	if pipelineJob.Action != "would_create" || pipelineJob.JobID != "squ-130" || pipelineJob.Target != "worker" || pipelineJob.Kickoff != "Implement it" {
		t.Fatalf("pipeline job preview = %+v", pipelineJob)
	}
	if len(pipelineJob.Steps) != 1 || pipelineJob.Steps[0].ID != "implement" || pipelineJob.Steps[0].Status != job.StatusQueued {
		t.Fatalf("pipeline job steps = %+v", pipelineJob.Steps)
	}
	if _, err := job.Read(teamDir, "squ-130"); !os.IsNotExist(err) {
		t.Fatalf("dry-run pipeline preview wrote job, err=%v", err)
	}

	existing := mustNewJob(t, "SQU-130", "worker")
	existing.Status = job.StatusBlocked
	if err := job.Write(teamDir, existing); err != nil {
		t.Fatalf("write existing job: %v", err)
	}
	updateCmd := NewRootCmd()
	updateOut, updateErr := &bytes.Buffer{}, &bytes.Buffer{}
	updateCmd.SetOut(updateOut)
	updateCmd.SetErr(updateErr)
	updateCmd.SetArgs([]string{"event", "publish", "ticket.created", "--payload", `{"ticket":"SQU-130","kickoff":"Updated kickoff"}`, "--dry-run", "--json", "--target", target})
	if err := updateCmd.Execute(); err != nil {
		t.Fatalf("event publish dry-run existing pipeline: %v\nstderr=%s", err, updateErr.String())
	}
	var updatePreview eventPublishPreview
	if err := json.Unmarshal(updateOut.Bytes(), &updatePreview); err != nil {
		t.Fatalf("decode event publish dry-run existing pipeline json: %v\nbody=%s", err, updateOut.String())
	}
	if len(updatePreview.PipelineJobs) != 1 || updatePreview.PipelineJobs[0].Action != "would_noop" || !updatePreview.PipelineJobs[0].Existing {
		t.Fatalf("existing pipeline job preview = %+v", updatePreview.PipelineJobs)
	}
	unchanged, err := job.Read(teamDir, "squ-130")
	if err != nil {
		t.Fatalf("read existing job: %v", err)
	}
	if unchanged.Status != job.StatusBlocked || unchanged.Kickoff != "test kickoff" {
		t.Fatalf("dry-run mutated existing job = %+v", unchanged)
	}
}

func TestEventTraceCommandExplainsTriggerDecisions(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := topoFixture + `
[pipelines.cypher]
trigger.event = "ticket.created"
trigger.match.project = "cypher"

[[pipelines.cypher.steps]]
id = "implement"
target = "worker"

[pipelines.graphql]
trigger.event = "ticket.created"
trigger.match.project = "graphql"

[[pipelines.graphql.steps]]
id = "implement"
target = "worker"
`
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"event", "trace", "ticket.created", "--payload", "project=cypher", "--target", target})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("event trace: %v\nstderr=%s", err, stderr.String())
	}
	for _, want := range []string{
		"event: ticket.created payload={\"project\":\"cypher\"}",
		"MATCH pipelines.cypher",
		"MATCH (pipeline first step implement -> worker)",
		"MISS  pipelines.graphql",
		"payload project=cypher != graphql",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("event trace output missing %q:\n%s", want, out.String())
		}
	}

	jsonCmd := NewRootCmd()
	jsonOut, jsonErr := &bytes.Buffer{}, &bytes.Buffer{}
	jsonCmd.SetOut(jsonOut)
	jsonCmd.SetErr(jsonErr)
	jsonCmd.SetArgs([]string{"event", "trace", "ticket.created", "--payload", "project=cypher", "--json", "--target", target})
	if err := jsonCmd.Execute(); err != nil {
		t.Fatalf("event trace --json: %v\nstderr=%s", err, jsonErr.String())
	}
	var trace topology.EventTrace
	if err := json.Unmarshal(jsonOut.Bytes(), &trace); err != nil {
		t.Fatalf("decode trace json: %v\nbody=%s", err, jsonOut.String())
	}
	if trace.MatchedRules != 1 || len(trace.MatchedPipelineNames()) != 1 || trace.MatchedPipelineNames()[0] != "cypher" {
		t.Fatalf("trace = %+v", trace)
	}
	graphql := cliTraceEntryByScope(t, trace, "pipelines.graphql")
	if graphql.Matched || graphql.Reason != "payload project=cypher != graphql" {
		t.Fatalf("graphql trace = %+v", graphql)
	}
}

func TestEventPublishTraceUsesDaemonTrace(t *testing.T) {
	target, err := os.MkdirTemp("/tmp", "agent-team-event-trace-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(target)
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), nil)
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"event", "publish", "agent.dispatch", "--payload", `{"target":"manager"}`, "--trace", "--target", target})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("event publish --trace: %v\nstderr=%s", err, stderr.String())
	}
	for _, want := range []string{
		"event: agent.dispatch payload={\"target\":\"manager\"}",
		"MISS  instances.manager",
		"event type mismatch",
		"MISS  instances.worker",
		"payload target=manager != worker",
		"WARNING: matched 0 rules",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("event publish --trace output missing %q:\n%s", want, out.String())
		}
	}
}

func TestEventPublishFormat(t *testing.T) {
	target, err := os.MkdirTemp("/tmp", "agent-team-event-format-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(target)
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), nil)
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"event", "publish", "user_invocation", "--format", "{{len .Matched}}:{{len .Messaged}}:{{len .Dispatched}}", "--target", target})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("event publish --format: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "1:1:0\n"; got != want {
		t.Fatalf("event publish --format output = %q, want %q", got, want)
	}
}

func TestEventPublishFormatRejectsConflictingModes(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"event", "publish", "user_invocation", "--format", "{{len .Matched}}", "--json"}, "--format cannot be combined"},
		{[]string{"event", "publish", "user_invocation", "--commands"}, "--commands requires --dry-run"},
		{[]string{"event", "publish", "user_invocation", "--dry-run", "--commands", "--json"}, "--commands cannot be combined with --json"},
		{[]string{"event", "publish", "user_invocation", "--dry-run", "--commands", "--format", "{{.Type}}"}, "--commands cannot be combined with --format"},
		{[]string{"event", "publish", "user_invocation", "--format", "{{"}, "invalid --format template"},
		{[]string{"event", "publish", "user_invocation", "--payload", `{}`, "--payload-file", "-"}, "choose one of --payload or --payload-file"},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
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

func TestTopologyShow_LocalFallback(t *testing.T) {
	// No daemon — `topology show` falls back to file parsing.
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	out := &strings.Builder{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"topology", "show", "--target", target})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	for _, want := range []string{"manager", "worker", "agent.dispatch"} {
		if !strings.Contains(got, want) {
			t.Errorf("topology show missing %q in output: %s", want, got)
		}
	}
}

func TestTopologyShow_NoFile(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	out := &strings.Builder{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"topology", "show", "--target", target})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "no instances declared") {
		t.Errorf("expected helpful empty-state message, got: %s", out.String())
	}
}

func cliTraceEntryByScope(t *testing.T, trace topology.EventTrace, scope string) topology.EventTraceEntry {
	t.Helper()
	for _, entry := range trace.Entries {
		if entry.Scope == scope {
			return entry
		}
	}
	t.Fatalf("trace entry %q missing: %+v", scope, trace.Entries)
	return topology.EventTraceEntry{}
}
