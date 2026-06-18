package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
)

func TestIntakeLinearCreatesPipelineJob(t *testing.T) {
	target, mgr, cleanup := setupIntakePipelineRepo(t)
	defer cleanup()

	payload := `{"action":"Issue created","data":{"identifier":"SQU-101","title":"Add intake"}}`
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "linear", "--payload", payload, "--target", target, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("intake linear: %v\nstderr=%s", err, stderr.String())
	}
	var result intakePublishResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode intake json: %v\nbody=%s", err, out.String())
	}
	if result.Event.Type != "ticket.created" {
		t.Fatalf("event = %+v", result.Event)
	}
	if len(result.Outcome.Messaged) != 1 || result.Outcome.Messaged[0] != "manager" {
		t.Fatalf("outcome = %+v", result.Outcome)
	}
	j, err := job.Read(filepath.Join(target, ".agent_team"), "squ-101")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if j.Pipeline != "ticket_triage" || len(j.Steps) != 1 || j.Steps[0].Target != "manager" {
		t.Fatalf("job = %+v", j)
	}
	messages, err := daemon.ReadMessages(daemon.DaemonRoot(filepath.Join(target, ".agent_team")), "manager")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %+v, want one", messages)
	}
	_ = mgr
}

func TestIntakeLinearDryRunNormalizesWithoutDaemon(t *testing.T) {
	payload := `{"action":"Issue created","data":{"identifier":"SQU-102","title":"Dry run intake","team":{"key":"SQU"}}}`
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "linear", "--payload", payload, "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("intake linear dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var result intakePublishResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode dry-run json: %v\nbody=%s", err, out.String())
	}
	if !result.DryRun || result.Outcome != nil {
		t.Fatalf("dry-run result = %+v", result)
	}
	if result.Event.Type != "ticket.created" || result.Event.Payload["ticket"] != "SQU-102" || result.Event.Payload["team"] != "SQU" {
		t.Fatalf("event = %+v", result.Event)
	}
}

func TestIntakeGitHubReconcilesOwningJob(t *testing.T) {
	target, _, cleanup := setupIntakePipelineRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	j := mustNewJob(t, "SQU-106", "worker")
	j.Status = job.StatusRunning
	j.PR = "https://github.com/acme/repo/pull/106"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	payload := `{"action":"closed","repository":{"full_name":"acme/repo"},"pull_request":{"number":106,"merged":true,"html_url":"https://github.com/acme/repo/pull/106","head":{"ref":"worker-squ-106"}}}`
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "github", "--payload", payload, "--target", target, "--reconcile-job", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("intake github reconcile: %v\nstderr=%s", err, stderr.String())
	}
	var result intakePublishResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode intake github json: %v\nbody=%s", err, out.String())
	}
	if result.Event == nil || result.Event.Type != "pr.merged" {
		t.Fatalf("event = %+v", result.Event)
	}
	if result.Reconcile == nil || result.Reconcile.Job == nil {
		t.Fatalf("missing reconcile result: %+v", result)
	}
	if result.Reconcile.Job.ID != "squ-106" || result.Reconcile.Job.Status != job.StatusDone || result.Reconcile.MatchedBy != "pr_url" {
		t.Fatalf("reconcile = %+v", result.Reconcile)
	}
	updated, err := job.Read(teamDir, "squ-106")
	if err != nil {
		t.Fatalf("read reconciled job: %v", err)
	}
	if updated.Status != job.StatusDone || updated.LastEvent != "pr.merged" || updated.Branch != "worker-squ-106" {
		t.Fatalf("updated job = %+v", updated)
	}
}

func TestIntakeGitHubCleanupMergedRequiresReconcileJob(t *testing.T) {
	payload := `{"action":"closed","pull_request":{"number":1,"merged":true}}`
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "github", "--payload", payload, "--cleanup-merged", "--dry-run"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("intake github --cleanup-merged without --reconcile-job succeeded")
	}
	if !strings.Contains(stderr.String(), "--cleanup-merged requires --reconcile-job") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestIntakeDryRunFormat(t *testing.T) {
	payload := `{"action":"Issue created","data":{"identifier":"SQU-103","title":"Formatted intake"}}`
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"intake", "linear",
		"--payload", payload,
		"--dry-run",
		"--format", `{{.Event.Type}} {{index .Event.Payload "ticket"}} {{.DryRun}}`,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("intake dry-run format: %v\nstderr=%s", err, stderr.String())
	}
	if got := strings.TrimSpace(out.String()); got != "ticket.created SQU-103 true" {
		t.Fatalf("formatted dry-run = %q", got)
	}

	invalid := NewRootCmd()
	invalidOut, invalidErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalid.SetOut(invalidOut)
	invalid.SetErr(invalidErr)
	invalid.SetArgs([]string{"intake", "linear", "--payload", payload, "--dry-run", "--format", "{{.Event.Type}}", "--json"})
	if err := invalid.Execute(); err == nil {
		t.Fatalf("intake dry-run format+json succeeded")
	}
	if !strings.Contains(invalidErr.String(), "--format cannot be combined with --json") {
		t.Fatalf("format+json stderr = %q", invalidErr.String())
	}
}

func TestIntakeSchedulePublishesScheduleEvent(t *testing.T) {
	target, _, cleanup := setupIntakePipelineRepo(t)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "schedule", "nightly", "--payload", `{"workspace":"repo"}`, "--target", target, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("intake schedule: %v\nstderr=%s", err, stderr.String())
	}
	var result intakePublishResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode intake json: %v\nbody=%s", err, out.String())
	}
	if result.Event.Type != "schedule" || result.Event.Payload["name"] != "nightly" {
		t.Fatalf("event = %+v", result.Event)
	}
}

func TestIntakeScheduleDryRunText(t *testing.T) {
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "schedule", "nightly", "--payload", `{"workspace":"repo"}`, "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("intake schedule dry-run: %v\nstderr=%s", err, stderr.String())
	}
	for _, want := range []string{"Event: schedule", "KEY", "name", "nightly", "source", "schedule", "workspace", "repo"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run text missing %q:\n%s", want, out.String())
		}
	}
}

func setupIntakePipelineRepo(t *testing.T) (target string, mgr *daemon.InstanceManager, cleanup func()) {
	t.Helper()
	target, err := os.MkdirTemp("/tmp", "agent-team-intake-")
	if err != nil {
		t.Fatal(err)
	}
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(filepath.Join(teamDir, "agents", "manager"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "agents", "manager", "agent.md"), []byte("---\ndescription: manager\n---\n\nmanager\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"

[[instances.manager.triggers]]
event = "agent.dispatch"
match.target = "manager"

[pipelines.ticket_triage]
trigger.event = "ticket.created"

[[pipelines.ticket_triage.steps]]
id = "triage"
target = "manager"

[pipelines.nightly]
trigger.event = "schedule"
trigger.match.name = "nightly"

[[pipelines.nightly.steps]]
id = "triage"
target = "manager"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr = daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), fakeSpawnerForTest(t, 2*time.Second))
	cleanupDaemon := startRunTestDaemon(t, teamDir, mgr)
	return target, mgr, func() {
		cleanupDaemon()
		_ = os.RemoveAll(target)
	}
}
