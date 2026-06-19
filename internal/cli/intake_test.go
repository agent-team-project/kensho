package cli

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

	payload := `{"action":"Issue created","data":{"identifier":"SQU-101","url":"https://linear.app/squirtlesquad/issue/SQU-101/add-intake","title":"Add intake"}}`
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
	if j.Pipeline != "ticket_triage" || len(j.Steps) != 1 || j.Steps[0].Target != "manager" || j.TicketURL != "https://linear.app/squirtlesquad/issue/SQU-101/add-intake" {
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

func TestIntakePayloadFileDashReadsStdin(t *testing.T) {
	prev := intakeInput
	intakeInput = strings.NewReader(`{"action":"Issue created","data":{"identifier":"SQU-104","title":"Pipe payload"}}`)
	t.Cleanup(func() { intakeInput = prev })

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "linear", "--payload-file", "-", "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("intake linear stdin dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var result intakePublishResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode stdin dry-run json: %v\nbody=%s", err, out.String())
	}
	if !result.DryRun || result.Event == nil || result.Event.Type != "ticket.created" || result.Event.Payload["ticket"] != "SQU-104" {
		t.Fatalf("stdin dry-run result = %+v", result)
	}
}

func TestIntakeLinearDryRunPreviewTriggers(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"

[[instances.manager.triggers]]
event = "ticket.created"

[pipelines.ticket_triage]
trigger.event = "ticket.created"

[[pipelines.ticket_triage.steps]]
id = "triage"
target = "manager"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	payload := `{"action":"Issue created","data":{"identifier":"SQU-105","title":"Preview routing"}}`
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "linear", "--payload", payload, "--target", target, "--dry-run", "--preview-triggers", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("intake linear dry-run preview: %v\nstderr=%s", err, stderr.String())
	}
	var result intakePublishResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode dry-run preview json: %v\nbody=%s", err, out.String())
	}
	if !result.DryRun || result.Event == nil || result.Event.Type != "ticket.created" {
		t.Fatalf("dry-run preview result = %+v", result)
	}
	if result.Preview == nil || !result.Preview.DryRun || len(result.Preview.Matched) != 1 || result.Preview.Matched[0] != "manager" {
		t.Fatalf("trigger preview = %+v", result.Preview)
	}
	if len(result.Preview.Pipelines) != 1 || result.Preview.Pipelines[0] != "ticket_triage" {
		t.Fatalf("pipeline preview = %+v", result.Preview)
	}
	if len(result.Preview.PipelineJobs) != 1 {
		t.Fatalf("pipeline job preview = %+v", result.Preview)
	}
	pipelineJob := result.Preview.PipelineJobs[0]
	if pipelineJob.Action != "would_create" || pipelineJob.JobID != "squ-105" || pipelineJob.Ticket != "SQU-105" || pipelineJob.Pipeline != "ticket_triage" || pipelineJob.Target != "manager" {
		t.Fatalf("pipeline job preview = %+v", pipelineJob)
	}
	if len(pipelineJob.Steps) != 1 || pipelineJob.Steps[0].ID != "triage" || pipelineJob.Steps[0].Target != "manager" || pipelineJob.Steps[0].Status != job.StatusQueued {
		t.Fatalf("pipeline job steps preview = %+v", pipelineJob.Steps)
	}
	textCmd := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	textCmd.SetOut(textOut)
	textCmd.SetErr(textErr)
	textCmd.SetArgs([]string{"intake", "linear", "--payload", payload, "--target", target, "--dry-run", "--preview-triggers"})
	if err := textCmd.Execute(); err != nil {
		t.Fatalf("intake linear dry-run preview text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"Matched: manager", "Pipelines: ticket_triage", "Jobs:", "squ-105", "would_create", "target=manager", "steps=triage"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("preview text missing %q:\n%s", want, textOut.String())
		}
	}
	if _, err := job.Read(teamDir, "squ-105"); !os.IsNotExist(err) {
		t.Fatalf("dry-run preview wrote job, err=%v", err)
	}
}

func TestIntakeServeLinearDryRunPreview(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"

[[instances.manager.triggers]]
event = "ticket.created"

[pipelines.ticket_triage]
trigger.event = "ticket.created"

[[pipelines.ticket_triage.steps]]
id = "triage"
target = "manager"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	payload := `{"action":"Issue created","data":{"identifier":"SQU-201","title":"Preview server intake"}}`
	req := httptest.NewRequest(http.MethodPost, "/linear", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	newIntakeServeHandler(teamDir, intakeServeOptions{DryRun: true, PreviewTriggers: true}).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var result intakePublishResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode server dry-run response: %v\nbody=%s", err, rec.Body.String())
	}
	if !result.DryRun || result.Event == nil || result.Event.Type != "ticket.created" || result.Event.Payload["ticket"] != "SQU-201" {
		t.Fatalf("server dry-run result = %+v", result)
	}
	if result.Preview == nil || len(result.Preview.Matched) != 1 || result.Preview.Matched[0] != "manager" {
		t.Fatalf("server trigger preview = %+v", result.Preview)
	}
	if len(result.Preview.Pipelines) != 1 || result.Preview.Pipelines[0] != "ticket_triage" {
		t.Fatalf("server pipeline preview = %+v", result.Preview)
	}
	if _, err := job.Read(teamDir, "squ-201"); !os.IsNotExist(err) {
		t.Fatalf("dry-run server wrote job, err=%v", err)
	}
}

func TestIntakeServeLinearPublishes(t *testing.T) {
	target, _, cleanup := setupIntakePipelineRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")

	payload := `{"action":"Issue created","data":{"identifier":"SQU-202","url":"https://linear.app/squirtlesquad/issue/SQU-202/server-intake","title":"Server intake"}}`
	req := httptest.NewRequest(http.MethodPost, "/linear", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	newIntakeServeHandler(teamDir, intakeServeOptions{}).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var result intakePublishResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode server publish response: %v\nbody=%s", err, rec.Body.String())
	}
	if result.Event == nil || result.Event.Type != "ticket.created" {
		t.Fatalf("event = %+v", result.Event)
	}
	if result.Outcome == nil || len(result.Outcome.Messaged) != 1 || result.Outcome.Messaged[0] != "manager" {
		t.Fatalf("outcome = %+v", result.Outcome)
	}
	j, err := job.Read(teamDir, "squ-202")
	if err != nil {
		t.Fatalf("read server-created job: %v", err)
	}
	if j.Pipeline != "ticket_triage" || j.TicketURL != "https://linear.app/squirtlesquad/issue/SQU-202/server-intake" {
		t.Fatalf("server-created job = %+v", j)
	}
}

func TestIntakeServeErrors(t *testing.T) {
	handler := newIntakeServeHandler(t.TempDir(), intakeServeOptions{DryRun: true})
	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{name: "method", method: http.MethodGet, path: "/linear", status: http.StatusMethodNotAllowed},
		{name: "unknown", method: http.MethodPost, path: "/unknown", status: http.StatusNotFound},
		{name: "payload", method: http.MethodPost, path: "/linear", body: `{`, status: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, tc.status, rec.Body.String())
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error response: %v\nbody=%s", err, rec.Body.String())
			}
			if body["error"] == "" {
				t.Fatalf("missing error body: %+v", body)
			}
		})
	}
}

func TestIntakeServeLinearSignatureAndTimestamp(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	secret := "linear-secret"
	body := []byte(`{"action":"Issue created","webhookTimestamp":` + mustMillisString(now) + `,"data":{"identifier":"SQU-203","title":"Signed Linear"}}`)
	req := httptest.NewRequest(http.MethodPost, "/linear", bytes.NewReader(body))
	req.Header.Set("Linear-Signature", hmacSHA256Hex(secret, body, ""))
	rec := httptest.NewRecorder()
	newIntakeServeHandler(t.TempDir(), intakeServeOptions{
		DryRun:       true,
		LinearSecret: secret,
		Now:          func() time.Time { return now },
	}).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	stale := []byte(`{"action":"Issue created","webhookTimestamp":` + mustMillisString(now.Add(-2*time.Minute)) + `,"data":{"identifier":"SQU-204","title":"Stale Linear"}}`)
	staleReq := httptest.NewRequest(http.MethodPost, "/linear", bytes.NewReader(stale))
	staleReq.Header.Set("Linear-Signature", hmacSHA256Hex(secret, stale, ""))
	staleRec := httptest.NewRecorder()
	newIntakeServeHandler(t.TempDir(), intakeServeOptions{
		DryRun:       true,
		LinearSecret: secret,
		Now:          func() time.Time { return now },
	}).ServeHTTP(staleRec, staleReq)
	if staleRec.Code != http.StatusUnauthorized {
		t.Fatalf("stale status = %d body=%s", staleRec.Code, staleRec.Body.String())
	}

	badReq := httptest.NewRequest(http.MethodPost, "/linear", bytes.NewReader(body))
	badReq.Header.Set("Linear-Signature", "bad")
	badRec := httptest.NewRecorder()
	newIntakeServeHandler(t.TempDir(), intakeServeOptions{
		DryRun:       true,
		LinearSecret: secret,
		Now:          func() time.Time { return now },
	}).ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature status = %d body=%s", badRec.Code, badRec.Body.String())
	}
}

func TestIntakeServeGitHubSignature(t *testing.T) {
	secret := "github-secret"
	body := []byte(`{"action":"opened","repository":{"full_name":"acme/repo"},"pull_request":{"number":203,"merged":false,"html_url":"https://github.com/acme/repo/pull/203","head":{"ref":"worker-squ-203"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", hmacSHA256Hex(secret, body, "sha256="))
	rec := httptest.NewRecorder()
	newIntakeServeHandler(t.TempDir(), intakeServeOptions{DryRun: true, GitHubSecret: secret}).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var result intakePublishResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode signed github response: %v\nbody=%s", err, rec.Body.String())
	}
	if result.Event == nil || result.Event.Type != "pr.opened" {
		t.Fatalf("signed github event = %+v", result.Event)
	}

	missingReq := httptest.NewRequest(http.MethodPost, "/github", bytes.NewReader(body))
	missingRec := httptest.NewRecorder()
	newIntakeServeHandler(t.TempDir(), intakeServeOptions{DryRun: true, GitHubSecret: secret}).ServeHTTP(missingRec, missingReq)
	if missingRec.Code != http.StatusUnauthorized {
		t.Fatalf("missing signature status = %d body=%s", missingRec.Code, missingRec.Body.String())
	}
}

func hmacSHA256Hex(secret string, body []byte, prefix string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return prefix + hex.EncodeToString(mac.Sum(nil))
}

func mustMillisString(t time.Time) string {
	return fmt.Sprintf("%d", t.UnixMilli())
}

func TestIntakePreviewTriggersRequiresDryRun(t *testing.T) {
	payload := `{"action":"Issue created","data":{"identifier":"SQU-106"}}`
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "linear", "--payload", payload, "--preview-triggers"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("intake --preview-triggers without dry-run succeeded: stdout=%s", out.String())
	}
	if !strings.Contains(stderr.String(), "--preview-triggers requires --dry-run") {
		t.Fatalf("stderr = %q", stderr.String())
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

func TestIntakeGitHubDryRunReconcileJobDoesNotMutate(t *testing.T) {
	target := t.TempDir()
	initInto(t, target)
	teamDir := filepath.Join(target, ".agent_team")
	j := mustNewJob(t, "SQU-107", "worker")
	j.Status = job.StatusRunning
	j.PR = "https://github.com/acme/repo/pull/107"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	payload := `{"action":"closed","repository":{"full_name":"acme/repo"},"pull_request":{"number":107,"merged":true,"html_url":"https://github.com/acme/repo/pull/107","head":{"ref":"worker-squ-107"}}}`
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "github", "--payload", payload, "--target", target, "--dry-run", "--reconcile-job", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("intake github dry-run reconcile: %v\nstderr=%s", err, stderr.String())
	}
	var result intakePublishResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode intake github dry-run json: %v\nbody=%s", err, out.String())
	}
	if !result.DryRun || result.Outcome != nil {
		t.Fatalf("dry-run result = %+v", result)
	}
	if result.Reconcile == nil || result.Reconcile.Job == nil || result.Reconcile.Job.Status != job.StatusDone {
		t.Fatalf("preview reconcile = %+v", result.Reconcile)
	}
	unchanged, err := job.Read(teamDir, "squ-107")
	if err != nil {
		t.Fatalf("read dry-run job: %v", err)
	}
	if unchanged.Status != job.StatusRunning || unchanged.LastEvent != "" || unchanged.Branch != "" {
		t.Fatalf("dry-run mutated job = %+v", unchanged)
	}
	events, err := job.ListEvents(teamDir, "squ-107")
	if err != nil {
		t.Fatalf("list dry-run events: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("dry-run wrote events = %+v", events)
	}
}

func TestIntakeGitHubReconcileDoesNotMutateWhenDaemonDown(t *testing.T) {
	target := t.TempDir()
	initInto(t, target)
	teamDir := filepath.Join(target, ".agent_team")
	j := mustNewJob(t, "SQU-108", "worker")
	j.Status = job.StatusRunning
	j.PR = "https://github.com/acme/repo/pull/108"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	payload := `{"action":"closed","repository":{"full_name":"acme/repo"},"pull_request":{"number":108,"merged":true,"html_url":"https://github.com/acme/repo/pull/108","head":{"ref":"worker-squ-108"}}}`
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "github", "--payload", payload, "--target", target, "--reconcile-job", "--json"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("intake github reconcile daemon-down succeeded unexpectedly: stdout=%s", out.String())
	}
	if !strings.Contains(stderr.String(), "daemon is not running") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	unchanged, err := job.Read(teamDir, "squ-108")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if unchanged.Status != job.StatusRunning || unchanged.LastEvent != "" || unchanged.Branch != "" {
		t.Fatalf("daemon-down reconcile mutated job = %+v", unchanged)
	}
	events, err := job.ListEvents(teamDir, "squ-108")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("daemon-down reconcile wrote events = %+v", events)
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

func TestIntakeScheduleDryRunPreviewTriggers(t *testing.T) {
	target, _, cleanup := setupIntakePipelineRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "schedule", "nightly", "--payload", `{"workspace":"repo"}`, "--target", target, "--dry-run", "--preview-triggers", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("intake schedule dry-run preview: %v\nstderr=%s", err, stderr.String())
	}
	var result intakePublishResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode schedule dry-run preview json: %v\nbody=%s", err, out.String())
	}
	if !result.DryRun || result.Event == nil || result.Event.Type != "schedule" {
		t.Fatalf("schedule dry-run preview result = %+v", result)
	}
	if result.Preview == nil || len(result.Preview.Pipelines) != 1 || result.Preview.Pipelines[0] != "nightly" {
		t.Fatalf("schedule trigger preview = %+v", result.Preview)
	}
	if len(result.Preview.PipelineJobs) != 1 {
		t.Fatalf("schedule pipeline job preview = %+v", result.Preview)
	}
	pipelineJob := result.Preview.PipelineJobs[0]
	if pipelineJob.Action != "would_create" || pipelineJob.Pipeline != "nightly" || pipelineJob.Target != "manager" || !pipelineJob.GeneratedTicket || pipelineJob.JobID != "" {
		t.Fatalf("schedule pipeline job preview = %+v", pipelineJob)
	}
	if len(pipelineJob.Steps) != 1 || pipelineJob.Steps[0].ID != "triage" || pipelineJob.Steps[0].Target != "manager" {
		t.Fatalf("schedule pipeline steps = %+v", pipelineJob.Steps)
	}
	textCmd := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	textCmd.SetOut(textOut)
	textCmd.SetErr(textErr)
	textCmd.SetArgs([]string{"intake", "schedule", "nightly", "--payload", `{"workspace":"repo"}`, "--target", target, "--dry-run", "--preview-triggers"})
	if err := textCmd.Execute(); err != nil {
		t.Fatalf("intake schedule dry-run preview text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"Pipelines: nightly", "Jobs:", "pipeline:nightly", "would_create", "target=manager", "ticket=<generated>", "steps=triage"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("schedule preview text missing %q:\n%s", want, textOut.String())
		}
	}
	entries, err := os.ReadDir(job.Directory(teamDir))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read jobs dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("dry-run schedule preview wrote jobs = %+v", entries)
	}
}

func TestIntakeSchedulePreviewTriggersRequiresDryRun(t *testing.T) {
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "schedule", "nightly", "--preview-triggers"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("intake schedule --preview-triggers without dry-run succeeded: stdout=%s", out.String())
	}
	if !strings.Contains(stderr.String(), "--preview-triggers requires --dry-run") {
		t.Fatalf("stderr = %q", stderr.String())
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
