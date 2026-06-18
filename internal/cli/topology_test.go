package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if len(updatePreview.PipelineJobs) != 1 || updatePreview.PipelineJobs[0].Action != "would_update" || !updatePreview.PipelineJobs[0].Existing {
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
