package cli

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	if len(result.Outcome.Queued) != 1 || result.Outcome.Queued[0] != "manager" || len(result.Outcome.Messaged) != 0 {
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
	if result.Outcome == nil || len(result.Outcome.Queued) != 1 || result.Outcome.Queued[0] != "manager" || len(result.Outcome.Messaged) != 0 {
		t.Fatalf("outcome = %+v", result.Outcome)
	}
	j, err := job.Read(teamDir, "squ-202")
	if err != nil {
		t.Fatalf("read server-created job: %v", err)
	}
	if j.Pipeline != "ticket_triage" || j.TicketURL != "https://linear.app/squirtlesquad/issue/SQU-202/server-intake" {
		t.Fatalf("server-created job = %+v", j)
	}
	deliveries, err := listIntakeDeliveries(teamDir)
	if err != nil {
		t.Fatalf("list intake deliveries: %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].Provider != "linear" || deliveries[0].Status != intakeDeliveryStatusOK || deliveries[0].EventType != "ticket.created" || deliveries[0].Ticket != "SQU-202" || deliveries[0].Payload["ticket"] != "SQU-202" {
		t.Fatalf("deliveries = %+v", deliveries)
	}
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "deliveries", "--target", target, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("intake deliveries: %v\nstderr=%s", err, stderr.String())
	}
	var rows []intakeDelivery
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode deliveries json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Ticket != "SQU-202" {
		t.Fatalf("delivery rows = %+v", rows)
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

func TestIntakeServePrunesRetainedDeliveries(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	replayedAt := now.Add(-25 * time.Hour)
	for _, delivery := range []intakeDelivery{
		{
			ID:         "old-ok",
			Time:       now.Add(-48 * time.Hour),
			Provider:   "linear",
			Status:     intakeDeliveryStatusOK,
			HTTPStatus: http.StatusOK,
			EventType:  "ticket.created",
			Ticket:     "SQU-214",
		},
		{
			ID:           "old-recovered",
			Time:         now.Add(-48 * time.Hour),
			Provider:     "linear",
			Status:       intakeDeliveryStatusError,
			ReplayStatus: intakeDeliveryReplayStatusOK,
			ReplayedAt:   &replayedAt,
			HTTPStatus:   http.StatusServiceUnavailable,
			EventType:    "ticket.created",
			Ticket:       "SQU-215",
		},
		{
			ID:         "old-unresolved",
			Time:       now.Add(-48 * time.Hour),
			Provider:   "linear",
			Status:     intakeDeliveryStatusError,
			HTTPStatus: http.StatusServiceUnavailable,
			EventType:  "ticket.created",
			Payload:    map[string]any{"source": "linear", "ticket": "SQU-216"},
			Ticket:     "SQU-216",
			Error:      "daemon is not running",
		},
	} {
		if err := appendIntakeDelivery(teamDir, delivery); err != nil {
			t.Fatalf("append %s: %v", delivery.ID, err)
		}
	}

	body := `{"action":"Issue created","data":{"identifier":"SQU-217","title":"Fresh dry-run"}}`
	req := httptest.NewRequest(http.MethodPost, "/linear", strings.NewReader(body))
	rec := httptest.NewRecorder()
	newIntakeServeHandler(teamDir, intakeServeOptions{
		DryRun:                  true,
		Now:                     func() time.Time { return now },
		PruneOKOlderThan:        24 * time.Hour,
		PruneRecoveredOlderThan: 24 * time.Hour,
	}).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	deliveries, err := listIntakeDeliveries(teamDir)
	if err != nil {
		t.Fatalf("list after serve retention: %v", err)
	}
	ids := deliveryIDs(deliveries)
	if strings.Contains(ids, "old-ok") || strings.Contains(ids, "old-recovered") {
		t.Fatalf("serve retention kept pruned rows: %+v", deliveries)
	}
	if !strings.Contains(ids, "old-unresolved") || len(deliveries) != 2 {
		t.Fatalf("serve retention deliveries = %+v", deliveries)
	}
	if deliveries[1].Ticket != "SQU-217" || deliveries[1].Status != intakeDeliveryStatusOK || !deliveries[1].DryRun {
		t.Fatalf("fresh delivery = %+v", deliveries[1])
	}
}

func TestIntakeServeGitHubVerifyPRDryRunPreview(t *testing.T) {
	target := t.TempDir()
	initInto(t, target)
	initGitRepoForJobTest(t, target)
	installFakeGHForJobTest(t, `{"merged":true,"state":"MERGED","mergeCommit":{"oid":"fedcba"}}`, 0)

	teamDir := filepath.Join(target, ".agent_team")
	branch := "worktree-worker-squ-218"
	runGitForJobTest(t, target, "checkout", "-b", branch)
	runGitForJobTest(t, target, "checkout", "main")
	j := mustNewJob(t, "SQU-218", "worker")
	j.Status = job.StatusRunning
	j.Branch = branch
	j.PR = "https://github.com/acme/repo/pull/218"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	body := `{"action":"closed","repository":{"full_name":"acme/repo"},"pull_request":{"number":218,"merged":true,"html_url":"https://github.com/acme/repo/pull/218","head":{"ref":"worktree-worker-squ-218"}}}`
	req := httptest.NewRequest(http.MethodPost, "/github", strings.NewReader(body))
	rec := httptest.NewRecorder()
	newIntakeServeHandler(teamDir, intakeServeOptions{
		DryRun:              true,
		GitHubReconcileJob:  true,
		GitHubCleanupMerged: true,
		GitHubVerifyPR:      true,
	}).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var result intakePublishResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode serve dry-run json: %v\nbody=%s", err, rec.Body.String())
	}
	if !result.DryRun || result.CleanupPreview == nil || !result.CleanupPreview.VerifyPR || result.CleanupPreview.PRVerification == nil || result.CleanupPreview.PRVerification.MergeCommit != "fedcba" {
		t.Fatalf("serve verify preview = %+v", result.CleanupPreview)
	}
	if !branchExists(t, target, branch) {
		t.Fatalf("dry-run removed branch %s", branch)
	}
}

func TestIntakeServeGitHubVerifyPRRequiresCleanupMerged(t *testing.T) {
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "serve", "--github-verify-pr"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("intake serve --github-verify-pr succeeded without cleanup: stdout=%s", out.String())
	}
	var code ExitCode
	if !errors.As(err, &code) || int(code) != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if !strings.Contains(stderr.String(), "--github-verify-pr requires --github-cleanup-merged") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestIntakeDeliveriesFiltersAndFormat(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	if err := appendIntakeDelivery(teamDir, intakeDelivery{
		ID:         "linear-ok",
		Time:       now,
		Provider:   "linear",
		Status:     intakeDeliveryStatusOK,
		HTTPStatus: http.StatusOK,
		EventType:  "ticket.created",
		Ticket:     "SQU-205",
	}); err != nil {
		t.Fatalf("append linear delivery: %v", err)
	}
	if err := appendIntakeDelivery(teamDir, intakeDelivery{
		ID:         "github-error",
		Time:       now.Add(time.Second),
		Provider:   "github",
		Status:     intakeDeliveryStatusError,
		HTTPStatus: http.StatusUnauthorized,
		EventType:  "pr.opened",
		Payload:    map[string]any{"source": "github", "pr": "205", "repository": "acme/repo"},
		Error:      "missing X-Hub-Signature-256 header",
	}); err != nil {
		t.Fatalf("append github delivery: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "deliveries", "--target", target, "--provider", "github", "--status", "error", "--format", "{{.Provider}} {{.Status}} {{.HTTPStatus}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("intake deliveries format: %v\nstderr=%s", err, stderr.String())
	}
	if got := strings.TrimSpace(out.String()); got != "github error 401" {
		t.Fatalf("formatted deliveries = %q", got)
	}

	jsonCmd := NewRootCmd()
	jsonOut, jsonErr := &bytes.Buffer{}, &bytes.Buffer{}
	jsonCmd.SetOut(jsonOut)
	jsonCmd.SetErr(jsonErr)
	jsonCmd.SetArgs([]string{"intake", "deliveries", "--target", target, "--tail", "1", "--json"})
	if err := jsonCmd.Execute(); err != nil {
		t.Fatalf("intake deliveries tail json: %v\nstderr=%s", err, jsonErr.String())
	}
	var rows []intakeDelivery
	if err := json.Unmarshal(jsonOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode tail deliveries: %v\nbody=%s", err, jsonOut.String())
	}
	if len(rows) != 1 || rows[0].ID != "github-error" {
		t.Fatalf("tail deliveries = %+v", rows)
	}
	if len(rows[0].Actions) != 2 || !strings.Contains(rows[0].Actions[0], "agent-team intake replay github-error --dry-run --preview-triggers") {
		t.Fatalf("tail delivery actions = %+v", rows[0].Actions)
	}
}

func TestIntakeDeliveriesReplayFilters(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	replayedAt := now.Add(5 * time.Minute)
	for _, delivery := range []intakeDelivery{
		{
			ID:           "recovered",
			Time:         now,
			Provider:     "linear",
			Status:       intakeDeliveryStatusError,
			ReplayStatus: intakeDeliveryReplayStatusOK,
			ReplayedAt:   &replayedAt,
			HTTPStatus:   http.StatusServiceUnavailable,
			EventType:    "ticket.created",
			Payload:      map[string]any{"source": "linear", "ticket": "SQU-212"},
			Ticket:       "SQU-212",
			Error:        "daemon is not running",
		},
		{
			ID:         "unresolved",
			Time:       now.Add(time.Second),
			Provider:   "linear",
			Status:     intakeDeliveryStatusError,
			HTTPStatus: http.StatusServiceUnavailable,
			EventType:  "ticket.created",
			Payload:    map[string]any{"source": "linear", "ticket": "SQU-213"},
			Ticket:     "SQU-213",
			Error:      "daemon is not running",
		},
		{
			ID:           "replay-failed",
			Time:         now.Add(2 * time.Second),
			Provider:     "github",
			Status:       intakeDeliveryStatusError,
			ReplayStatus: intakeDeliveryReplayStatusError,
			ReplayedAt:   &replayedAt,
			ReplayError:  "daemon: event: refused",
			HTTPStatus:   http.StatusServiceUnavailable,
			EventType:    "pr.opened",
			Payload:      map[string]any{"source": "github", "pr_url": "https://github.com/acme/repo/pull/213"},
			Error:        "daemon is not running",
		},
	} {
		if err := appendIntakeDelivery(teamDir, delivery); err != nil {
			t.Fatalf("append %s: %v", delivery.ID, err)
		}
	}

	recovered := NewRootCmd()
	recoveredOut, recoveredErr := &bytes.Buffer{}, &bytes.Buffer{}
	recovered.SetOut(recoveredOut)
	recovered.SetErr(recoveredErr)
	recovered.SetArgs([]string{"intake", "deliveries", "--target", target, "--replay-status", "ok", "--json"})
	if err := recovered.Execute(); err != nil {
		t.Fatalf("intake deliveries replay-status ok: %v\nstderr=%s", err, recoveredErr.String())
	}
	var recoveredRows []intakeDelivery
	if err := json.Unmarshal(recoveredOut.Bytes(), &recoveredRows); err != nil {
		t.Fatalf("decode recovered deliveries: %v\nbody=%s", err, recoveredOut.String())
	}
	if deliveryIDs(recoveredRows) != "recovered" || len(recoveredRows[0].Actions) != 0 {
		t.Fatalf("recovered rows = %+v", recoveredRows)
	}

	unresolved := NewRootCmd()
	unresolvedOut, unresolvedErr := &bytes.Buffer{}, &bytes.Buffer{}
	unresolved.SetOut(unresolvedOut)
	unresolved.SetErr(unresolvedErr)
	unresolved.SetArgs([]string{"intake", "deliveries", "--target", target, "--unresolved", "--json"})
	if err := unresolved.Execute(); err != nil {
		t.Fatalf("intake deliveries unresolved: %v\nstderr=%s", err, unresolvedErr.String())
	}
	var unresolvedRows []intakeDelivery
	if err := json.Unmarshal(unresolvedOut.Bytes(), &unresolvedRows); err != nil {
		t.Fatalf("decode unresolved deliveries: %v\nbody=%s", err, unresolvedOut.String())
	}
	if deliveryIDs(unresolvedRows) != "unresolved,replay-failed" {
		t.Fatalf("unresolved rows = %+v", unresolvedRows)
	}
	for _, row := range unresolvedRows {
		if len(row.Actions) == 0 {
			t.Fatalf("unresolved row missing actions = %+v", row)
		}
	}
}

func TestIntakeSummaryReportsRecoveryState(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	replayedAt := now.Add(-time.Hour)
	for _, delivery := range []intakeDelivery{
		{
			ID:         "linear-ok",
			Time:       now.Add(-4 * time.Minute),
			Provider:   "linear",
			Status:     intakeDeliveryStatusOK,
			HTTPStatus: http.StatusOK,
			EventType:  "ticket.created",
			Ticket:     "SQU-218",
		},
		{
			ID:           "linear-recovered",
			Time:         now.Add(-3 * time.Minute),
			Provider:     "linear",
			Status:       intakeDeliveryStatusError,
			ReplayStatus: intakeDeliveryReplayStatusOK,
			ReplayedAt:   &replayedAt,
			HTTPStatus:   http.StatusServiceUnavailable,
			EventType:    "ticket.created",
			Payload:      map[string]any{"source": "linear", "ticket": "SQU-219"},
			Ticket:       "SQU-219",
			Error:        "daemon is not running",
		},
		{
			ID:         "github-unresolved",
			Time:       now.Add(-2 * time.Minute),
			Provider:   "github",
			Status:     intakeDeliveryStatusError,
			HTTPStatus: http.StatusServiceUnavailable,
			EventType:  "pr.opened",
			Payload:    map[string]any{"source": "github", "pr_url": "https://github.com/acme/repo/pull/220"},
			PR:         "https://github.com/acme/repo/pull/220",
			Error:      "daemon is not running",
		},
		{
			ID:           "github-replay-failed",
			Time:         now.Add(-time.Minute),
			Provider:     "github",
			Status:       intakeDeliveryStatusError,
			ReplayStatus: intakeDeliveryReplayStatusError,
			ReplayedAt:   &replayedAt,
			ReplayError:  "daemon: refused",
			HTTPStatus:   http.StatusServiceUnavailable,
			EventType:    "pr.opened",
			Payload:      map[string]any{"source": "github", "pr_url": "https://github.com/acme/repo/pull/221"},
			PR:           "https://github.com/acme/repo/pull/221",
			Error:        "daemon is not running",
		},
	} {
		if err := appendIntakeDelivery(teamDir, delivery); err != nil {
			t.Fatalf("append %s: %v", delivery.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "summary", "--target", target, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("intake summary json: %v\nstderr=%s", err, stderr.String())
	}
	var summary intakeSummaryResult
	if err := json.Unmarshal(out.Bytes(), &summary); err != nil {
		t.Fatalf("decode intake summary: %v\nbody=%s", err, out.String())
	}
	if summary.Deliveries != 4 || summary.OK != 1 || summary.Failed != 3 || summary.Unresolved != 2 || summary.Recovered != 1 || summary.Replayable != 2 || summary.ReplayFailed != 1 || summary.LatestErrorID != "github-replay-failed" {
		t.Fatalf("summary = %+v", summary)
	}
	if len(summary.Providers) != 2 || summary.Providers[0].Provider != "github" || summary.Providers[0].Deliveries != 2 || summary.Providers[0].ReplayFailed != 1 || summary.Providers[1].Provider != "linear" || summary.Providers[1].Recovered != 1 {
		t.Fatalf("provider summaries = %+v", summary.Providers)
	}
	for _, want := range []string{
		"agent-team intake deliveries --unresolved",
		"agent-team intake replay --all --dry-run --preview-triggers",
		"agent-team intake replay --all",
		"agent-team intake prune --replay-status ok --dry-run",
	} {
		if !containsString(summary.Actions, want) {
			t.Fatalf("summary actions missing %q: %+v", want, summary.Actions)
		}
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"intake", "summary", "--target", target})
	if err := text.Execute(); err != nil {
		t.Fatalf("intake summary text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"intake: deliveries=4 ok=1 failed=3 unresolved=2 recovered=1 replayable=2 replay_failed=1 latest_error=github-replay-failed", "github", "linear", "agent-team intake replay --all"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("summary text missing %q:\n%s", want, textOut.String())
		}
	}

	filtered := NewRootCmd()
	filteredOut, filteredErr := &bytes.Buffer{}, &bytes.Buffer{}
	filtered.SetOut(filteredOut)
	filtered.SetErr(filteredErr)
	filtered.SetArgs([]string{"intake", "summary", "--target", target, "--provider", "github", "--replay-status", "error", "--format", "{{.Deliveries}} {{.ReplayFailed}} {{.LatestErrorID}}"})
	if err := filtered.Execute(); err != nil {
		t.Fatalf("intake summary format: %v\nstderr=%s", err, filteredErr.String())
	}
	if got := strings.TrimSpace(filteredOut.String()); got != "1 1 github-replay-failed" {
		t.Fatalf("filtered summary = %q", got)
	}
}

func TestIntakeDoctorReportsLedgerFindings(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(filepath.Join(teamDir, "daemon"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		`{"id":"ok","time":"2026-06-19T12:00:00Z","provider":"linear","status":"ok","http_status":200}`,
		`{`,
		`{"id":"ok","time":"2026-06-19T12:01:00Z","provider":"linear","status":"ok","http_status":200}`,
		`{"id":"bad-status","time":"2026-06-19T12:02:00Z","provider":"github","status":"weird","http_status":500}`,
		`{"id":"missing-payload","time":"2026-06-19T12:03:00Z","provider":"github","status":"error","http_status":503}`,
	}, "\n") + "\n"
	if err := os.WriteFile(intakeDeliveryLogPath(teamDir), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "doctor", "--target", target, "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected intake doctor to fail on ledger problems")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("expected exit 1, got %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("json doctor should not write stderr: %s", stderr.String())
	}
	var result intakeDoctorResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode intake doctor: %v\nbody=%s", err, out.String())
	}
	if result.OK || !result.Exists || result.Deliveries != 4 || result.Summary.Deliveries != 4 || result.Summary.Unresolved != 1 {
		t.Fatalf("doctor result = %+v", result)
	}
	for _, code := range []string{"invalid_json", "duplicate_id", "unknown_status"} {
		if !hasIntakeDoctorFinding(result.Problems, code) {
			t.Fatalf("problems missing %s: %+v", code, result.Problems)
		}
	}
	if !hasIntakeDoctorFinding(result.Warnings, "not_replayable") {
		t.Fatalf("warnings missing not_replayable: %+v", result.Warnings)
	}

	format := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	format.SetOut(formatOut)
	format.SetErr(formatErr)
	format.SetArgs([]string{"intake", "doctor", "--target", target, "--format", "{{.OK}} {{.Deliveries}} {{len .Problems}} {{len .Warnings}}"})
	if err := format.Execute(); err == nil {
		t.Fatal("expected intake doctor format to fail on ledger problems")
	}
	if got, want := formatOut.String(), "false 4 3 1\n"; got != want {
		t.Fatalf("intake doctor format output = %q, want %q", got, want)
	}
	if formatErr.Len() != 0 {
		t.Fatalf("intake doctor format stderr = %q", formatErr.String())
	}
}

func TestIntakeDoctorOKWithWarnings(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(filepath.Join(teamDir, "daemon"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"missing-payload","time":"2026-06-19T12:03:00Z","provider":"github","status":"error","http_status":503}` + "\n"
	if err := os.WriteFile(intakeDeliveryLogPath(teamDir), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "doctor", "--target", target})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("intake doctor warnings should not fail: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(out.String(), "agent-team intake doctor: OK") || !strings.Contains(out.String(), "unresolved=1") {
		t.Fatalf("doctor stdout = %q", out.String())
	}
	if !strings.Contains(stderr.String(), "cannot be replayed") {
		t.Fatalf("doctor stderr missing warning: %q", stderr.String())
	}

	format := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	format.SetOut(formatOut)
	format.SetErr(formatErr)
	format.SetArgs([]string{"intake", "doctor", "--target", target, "--format", "{{.OK}} {{.Summary.Unresolved}} {{len .Warnings}}"})
	if err := format.Execute(); err != nil {
		t.Fatalf("intake doctor format warnings should not fail: %v\nstderr=%s", err, formatErr.String())
	}
	if got, want := formatOut.String(), "true 1 1\n"; got != want {
		t.Fatalf("intake doctor warning format output = %q, want %q", got, want)
	}
	if formatErr.Len() != 0 {
		t.Fatalf("intake doctor warning format stderr = %q", formatErr.String())
	}
}

func TestIntakeDoctorFormatValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"intake", "doctor", "--format", "{{.OK}}", "--json"}, "--format cannot be combined"},
		{[]string{"intake", "doctor", "--format", "{{"}, "invalid --format template"},
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

func TestIntakePruneFiltersAndRewritesLedger(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, delivery := range []intakeDelivery{
		{
			ID:         "ok-old",
			Time:       now.Add(-48 * time.Hour),
			Provider:   "linear",
			Status:     intakeDeliveryStatusOK,
			HTTPStatus: http.StatusOK,
			EventType:  "ticket.created",
			Ticket:     "SQU-301",
		},
		{
			ID:         "ok-new",
			Time:       now,
			Provider:   "linear",
			Status:     intakeDeliveryStatusOK,
			HTTPStatus: http.StatusOK,
			EventType:  "ticket.created",
			Ticket:     "SQU-302",
		},
		{
			ID:         "error-old",
			Time:       now.Add(-48 * time.Hour),
			Provider:   "github",
			Status:     intakeDeliveryStatusError,
			HTTPStatus: http.StatusServiceUnavailable,
			EventType:  "pr.opened",
			Error:      "daemon is not running",
		},
	} {
		if err := appendIntakeDelivery(teamDir, delivery); err != nil {
			t.Fatalf("append %s: %v", delivery.ID, err)
		}
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"intake", "prune", "--target", target, "--older-than", "24h", "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("intake prune dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryResults []intakePruneResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryResults); err != nil {
		t.Fatalf("decode dry prune: %v\nbody=%s", err, dryOut.String())
	}
	if len(dryResults) != 1 || dryResults[0].ID != "ok-old" || !dryResults[0].DryRun || dryResults[0].Dropped {
		t.Fatalf("dry prune results = %+v", dryResults)
	}
	deliveries, err := listIntakeDeliveries(teamDir)
	if err != nil {
		t.Fatalf("list after dry-run: %v", err)
	}
	if len(deliveries) != 3 {
		t.Fatalf("dry-run rewrote ledger: %+v", deliveries)
	}

	prune := NewRootCmd()
	pruneOut, pruneErr := &bytes.Buffer{}, &bytes.Buffer{}
	prune.SetOut(pruneOut)
	prune.SetErr(pruneErr)
	prune.SetArgs([]string{"intake", "prune", "--target", target, "--older-than", "24h", "--format", "{{.ID}} {{.Status}} {{.Dropped}}"})
	if err := prune.Execute(); err != nil {
		t.Fatalf("intake prune: %v\nstderr=%s", err, pruneErr.String())
	}
	if got := strings.TrimSpace(pruneOut.String()); got != "ok-old ok true" {
		t.Fatalf("prune output = %q", got)
	}
	deliveries, err = listIntakeDeliveries(teamDir)
	if err != nil {
		t.Fatalf("list after prune: %v", err)
	}
	if deliveryIDs(deliveries) != "error-old,ok-new" {
		t.Fatalf("deliveries after prune = %+v", deliveries)
	}

	errorPrune := NewRootCmd()
	errorOut, errorErr := &bytes.Buffer{}, &bytes.Buffer{}
	errorPrune.SetOut(errorOut)
	errorPrune.SetErr(errorErr)
	errorPrune.SetArgs([]string{"intake", "prune", "--target", target, "--status", "error", "--older-than", "24h", "--json"})
	if err := errorPrune.Execute(); err != nil {
		t.Fatalf("intake prune error: %v\nstderr=%s", err, errorErr.String())
	}
	var errorResults []intakePruneResult
	if err := json.Unmarshal(errorOut.Bytes(), &errorResults); err != nil {
		t.Fatalf("decode error prune: %v\nbody=%s", err, errorOut.String())
	}
	if len(errorResults) != 1 || errorResults[0].ID != "error-old" || !errorResults[0].Dropped {
		t.Fatalf("error prune results = %+v", errorResults)
	}
	deliveries, err = listIntakeDeliveries(teamDir)
	if err != nil {
		t.Fatalf("list after error prune: %v", err)
	}
	if deliveryIDs(deliveries) != "ok-new" {
		t.Fatalf("deliveries after error prune = %+v", deliveries)
	}
}

func TestIntakePruneReplayStatus(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	replayedAt := now.Add(-time.Hour)
	for _, delivery := range []intakeDelivery{
		{
			ID:           "recovered-error",
			Time:         now.Add(-2 * time.Hour),
			Provider:     "linear",
			Status:       intakeDeliveryStatusError,
			ReplayStatus: intakeDeliveryReplayStatusOK,
			ReplayedAt:   &replayedAt,
			HTTPStatus:   http.StatusServiceUnavailable,
			EventType:    "ticket.created",
			Ticket:       "SQU-303",
		},
		{
			ID:         "unresolved-error",
			Time:       now.Add(-2 * time.Hour),
			Provider:   "linear",
			Status:     intakeDeliveryStatusError,
			HTTPStatus: http.StatusServiceUnavailable,
			EventType:  "ticket.created",
			Ticket:     "SQU-304",
		},
		{
			ID:         "ok-delivery",
			Time:       now.Add(-2 * time.Hour),
			Provider:   "linear",
			Status:     intakeDeliveryStatusOK,
			HTTPStatus: http.StatusOK,
			EventType:  "ticket.created",
			Ticket:     "SQU-305",
		},
	} {
		if err := appendIntakeDelivery(teamDir, delivery); err != nil {
			t.Fatalf("append %s: %v", delivery.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "prune", "--target", target, "--replay-status", "ok", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("intake prune replay-status: %v\nstderr=%s", err, stderr.String())
	}
	var results []intakePruneResult
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		t.Fatalf("decode prune replay-status: %v\nbody=%s", err, out.String())
	}
	if len(results) != 1 || results[0].ID != "recovered-error" || results[0].ReplayStatus != intakeDeliveryReplayStatusOK || !results[0].Dropped {
		t.Fatalf("prune replay-status results = %+v", results)
	}
	deliveries, err := listIntakeDeliveries(teamDir)
	if err != nil {
		t.Fatalf("list after replay-status prune: %v", err)
	}
	if deliveryIDs(deliveries) != "unresolved-error,ok-delivery" {
		t.Fatalf("deliveries after replay-status prune = %+v", deliveries)
	}
}

func TestIntakeReplayDryRunPreview(t *testing.T) {
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
	if err := appendIntakeDelivery(teamDir, intakeDelivery{
		ID:         "replay-preview",
		Time:       time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC),
		Provider:   "linear",
		Status:     intakeDeliveryStatusError,
		HTTPStatus: http.StatusServiceUnavailable,
		EventType:  "ticket.created",
		Payload:    map[string]any{"source": "linear", "ticket": "SQU-206", "title": "Replay preview"},
		Ticket:     "SQU-206",
		Error:      "daemon is not running",
	}); err != nil {
		t.Fatalf("append delivery: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "replay", "replay-preview", "--target", target, "--dry-run", "--preview-triggers", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("intake replay dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var result intakePublishResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode replay dry-run: %v\nbody=%s", err, out.String())
	}
	if !result.DryRun || result.Event == nil || result.Event.Type != "ticket.created" || result.Event.Payload["ticket"] != "SQU-206" {
		t.Fatalf("replay dry-run result = %+v", result)
	}
	if result.Preview == nil || len(result.Preview.Matched) != 1 || result.Preview.Matched[0] != "manager" || len(result.Preview.Pipelines) != 1 || result.Preview.Pipelines[0] != "ticket_triage" {
		t.Fatalf("replay preview = %+v", result.Preview)
	}

	format := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	format.SetOut(formatOut)
	format.SetErr(formatErr)
	format.SetArgs([]string{"intake", "replay", "replay-preview", "--target", target, "--dry-run", "--preview-triggers", "--format", `{{.Event.Type}} {{index .Event.Payload "ticket"}} {{.DryRun}} {{len .Preview.Matched}}`})
	if err := format.Execute(); err != nil {
		t.Fatalf("intake replay dry-run format: %v\nstderr=%s", err, formatErr.String())
	}
	if got, want := formatOut.String(), "ticket.created SQU-206 true 1\n"; got != want {
		t.Fatalf("intake replay dry-run format = %q, want %q", got, want)
	}
}

func TestIntakeReplayPublishesDelivery(t *testing.T) {
	target, _, cleanup := setupIntakePipelineRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	if err := appendIntakeDelivery(teamDir, intakeDelivery{
		ID:         "replay-publish",
		Time:       time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC),
		Provider:   "linear",
		Status:     intakeDeliveryStatusError,
		HTTPStatus: http.StatusServiceUnavailable,
		EventType:  "ticket.created",
		Payload:    map[string]any{"source": "linear", "ticket": "SQU-207", "ticket_url": "https://linear.app/squirtlesquad/issue/SQU-207/replay", "title": "Replay publish"},
		Ticket:     "SQU-207",
		Error:      "daemon is not running",
	}); err != nil {
		t.Fatalf("append delivery: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "replay", "replay-publish", "--target", target, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("intake replay publish: %v\nstderr=%s", err, stderr.String())
	}
	var result intakePublishResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode replay publish: %v\nbody=%s", err, out.String())
	}
	if result.Outcome == nil || len(result.Outcome.Queued) != 1 || result.Outcome.Queued[0] != "manager" || len(result.Outcome.Messaged) != 0 {
		t.Fatalf("replay outcome = %+v", result.Outcome)
	}
	j, err := job.Read(teamDir, "squ-207")
	if err != nil {
		t.Fatalf("read replay job: %v", err)
	}
	if j.Pipeline != "ticket_triage" || j.TicketURL != "https://linear.app/squirtlesquad/issue/SQU-207/replay" {
		t.Fatalf("replay job = %+v", j)
	}
	deliveries, err := listIntakeDeliveries(teamDir)
	if err != nil {
		t.Fatalf("list replay deliveries: %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].ReplayStatus != intakeDeliveryReplayStatusOK || deliveries[0].ReplayedAt == nil || deliveries[0].ReplayError != "" {
		t.Fatalf("replay delivery marker = %+v", deliveries)
	}
}

func TestIntakeReplayAllDryRunPreviewFilters(t *testing.T) {
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
	deliveries := []intakeDelivery{
		{
			ID:         "linear-first",
			Time:       time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC),
			Provider:   "linear",
			Status:     intakeDeliveryStatusError,
			HTTPStatus: http.StatusServiceUnavailable,
			EventType:  "ticket.created",
			Payload:    map[string]any{"source": "linear", "ticket": "SQU-208", "title": "Replay first"},
			Ticket:     "SQU-208",
			Error:      "daemon is not running",
		},
		{
			ID:         "github-skipped",
			Time:       time.Date(2026, 6, 19, 12, 1, 0, 0, time.UTC),
			Provider:   "github",
			Status:     intakeDeliveryStatusError,
			HTTPStatus: http.StatusServiceUnavailable,
			EventType:  "pr.opened",
			Payload:    map[string]any{"source": "github", "pr_url": "https://github.com/acme/repo/pull/208"},
			PR:         "https://github.com/acme/repo/pull/208",
			Error:      "daemon is not running",
		},
		{
			ID:         "linear-ok-skipped",
			Time:       time.Date(2026, 6, 19, 12, 2, 0, 0, time.UTC),
			Provider:   "linear",
			Status:     intakeDeliveryStatusOK,
			HTTPStatus: http.StatusOK,
			EventType:  "ticket.created",
			Payload:    map[string]any{"source": "linear", "ticket": "SQU-209", "title": "Already ok"},
			Ticket:     "SQU-209",
		},
	}
	for _, delivery := range deliveries {
		if err := appendIntakeDelivery(teamDir, delivery); err != nil {
			t.Fatalf("append %s: %v", delivery.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "replay", "--all", "--target", target, "--provider", "linear", "--limit", "1", "--dry-run", "--preview-triggers", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("intake replay all dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var batch intakeReplayBatchResult
	if err := json.Unmarshal(out.Bytes(), &batch); err != nil {
		t.Fatalf("decode replay all dry-run: %v\nbody=%s", err, out.String())
	}
	if !batch.DryRun || batch.Total != 1 || batch.Succeeded != 1 || batch.Failed != 0 || len(batch.Results) != 1 {
		t.Fatalf("batch = %+v", batch)
	}
	result := batch.Results[0]
	if result.DeliveryID != "linear-first" || !result.OK || result.Event == nil || result.Event.Payload["ticket"] != "SQU-208" {
		t.Fatalf("result = %+v", result)
	}
	if result.Preview == nil || len(result.Preview.Matched) != 1 || result.Preview.Matched[0] != "manager" || len(result.Preview.Pipelines) != 1 || result.Preview.Pipelines[0] != "ticket_triage" {
		t.Fatalf("preview = %+v", result.Preview)
	}

	format := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	format.SetOut(formatOut)
	format.SetErr(formatErr)
	format.SetArgs([]string{"intake", "replay", "--all", "--target", target, "--provider", "linear", "--limit", "1", "--dry-run", "--preview-triggers", "--format", "{{.DeliveryID}} {{.OK}} {{.DryRun}} {{len .Preview.Pipelines}}"})
	if err := format.Execute(); err != nil {
		t.Fatalf("intake replay all dry-run format: %v\nstderr=%s", err, formatErr.String())
	}
	if got, want := formatOut.String(), "linear-first true true 1\n"; got != want {
		t.Fatalf("intake replay all dry-run format = %q, want %q", got, want)
	}
}

func TestIntakeReplayAllPublishesDeliveries(t *testing.T) {
	target, _, cleanup := setupIntakePipelineRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	for _, delivery := range []intakeDelivery{
		{
			ID:         "replay-all-one",
			Time:       time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC),
			Provider:   "linear",
			Status:     intakeDeliveryStatusError,
			HTTPStatus: http.StatusServiceUnavailable,
			EventType:  "ticket.created",
			Payload:    map[string]any{"source": "linear", "ticket": "SQU-210", "ticket_url": "https://linear.app/squirtlesquad/issue/SQU-210/replay-all-one", "title": "Replay all one"},
			Ticket:     "SQU-210",
			Error:      "daemon is not running",
		},
		{
			ID:         "replay-all-two",
			Time:       time.Date(2026, 6, 19, 12, 1, 0, 0, time.UTC),
			Provider:   "linear",
			Status:     intakeDeliveryStatusError,
			HTTPStatus: http.StatusServiceUnavailable,
			EventType:  "ticket.created",
			Payload:    map[string]any{"source": "linear", "ticket": "SQU-211", "ticket_url": "https://linear.app/squirtlesquad/issue/SQU-211/replay-all-two", "title": "Replay all two"},
			Ticket:     "SQU-211",
			Error:      "daemon is not running",
		},
	} {
		if err := appendIntakeDelivery(teamDir, delivery); err != nil {
			t.Fatalf("append %s: %v", delivery.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "replay", "--all", "--target", target, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("intake replay all publish: %v\nstderr=%s", err, stderr.String())
	}
	var batch intakeReplayBatchResult
	if err := json.Unmarshal(out.Bytes(), &batch); err != nil {
		t.Fatalf("decode replay all publish: %v\nbody=%s", err, out.String())
	}
	if batch.DryRun || batch.Total != 2 || batch.Succeeded != 2 || batch.Failed != 0 || len(batch.Results) != 2 {
		t.Fatalf("batch = %+v", batch)
	}
	for _, result := range batch.Results {
		if !result.OK || result.Outcome == nil || len(result.Outcome.Queued) != 1 || result.Outcome.Queued[0] != "manager" || len(result.Outcome.Messaged) != 0 {
			t.Fatalf("result = %+v", result)
		}
	}
	for _, id := range []string{"squ-210", "squ-211"} {
		j, err := job.Read(teamDir, id)
		if err != nil {
			t.Fatalf("read replay all job %s: %v", id, err)
		}
		if j.Pipeline != "ticket_triage" {
			t.Fatalf("job %s = %+v", id, j)
		}
	}
	deliveries, err := listIntakeDeliveries(teamDir)
	if err != nil {
		t.Fatalf("list replay all deliveries: %v", err)
	}
	if len(deliveries) != 2 {
		t.Fatalf("replay all deliveries = %+v", deliveries)
	}
	for _, delivery := range deliveries {
		if delivery.ReplayStatus != intakeDeliveryReplayStatusOK || delivery.ReplayedAt == nil || delivery.ReplayError != "" {
			t.Fatalf("delivery replay marker = %+v", delivery)
		}
	}

	replayAgain := NewRootCmd()
	againOut, againErr := &bytes.Buffer{}, &bytes.Buffer{}
	replayAgain.SetOut(againOut)
	replayAgain.SetErr(againErr)
	replayAgain.SetArgs([]string{"intake", "replay", "--all", "--target", target, "--dry-run", "--json"})
	if err := replayAgain.Execute(); err != nil {
		t.Fatalf("intake replay all after recovery: %v\nstderr=%s", err, againErr.String())
	}
	var again intakeReplayBatchResult
	if err := json.Unmarshal(againOut.Bytes(), &again); err != nil {
		t.Fatalf("decode replay all after recovery: %v\nbody=%s", err, againOut.String())
	}
	if again.Total != 0 || again.Succeeded != 0 || again.Failed != 0 || len(again.Results) != 0 {
		t.Fatalf("replay all after recovery = %+v", again)
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

func deliveryIDs(deliveries []intakeDelivery) string {
	ids := make([]string, 0, len(deliveries))
	for _, delivery := range deliveries {
		ids = append(ids, delivery.ID)
	}
	return strings.Join(ids, ",")
}

func hasIntakeDoctorFinding(findings []intakeDoctorFinding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
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
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "cleanup without reconcile",
			args: []string{"intake", "github", "--payload", payload, "--cleanup-merged", "--dry-run"},
			want: "--cleanup-merged requires --reconcile-job",
		},
		{
			name: "verify without cleanup",
			args: []string{"intake", "github", "--payload", payload, "--reconcile-job", "--verify-pr", "--dry-run"},
			want: "--verify-pr requires --cleanup-merged",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil {
				t.Fatalf("intake github validation succeeded: stdout=%s", out.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestIntakeGitHubVerifyPRDryRunPreview(t *testing.T) {
	target := t.TempDir()
	initInto(t, target)
	initGitRepoForJobTest(t, target)
	installFakeGHForJobTest(t, `{"merged":true,"state":"MERGED","mergeCommit":{"oid":"def456"}}`, 0)

	teamDir := filepath.Join(target, ".agent_team")
	branch := "worktree-worker-squ-109"
	runGitForJobTest(t, target, "checkout", "-b", branch)
	runGitForJobTest(t, target, "checkout", "main")
	j := mustNewJob(t, "SQU-109", "worker")
	j.Status = job.StatusRunning
	j.Branch = branch
	j.PR = "https://github.com/acme/repo/pull/109"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	payload := `{"action":"closed","repository":{"full_name":"acme/repo"},"pull_request":{"number":109,"merged":true,"html_url":"https://github.com/acme/repo/pull/109","head":{"ref":"worktree-worker-squ-109"}}}`
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "github", "--payload", payload, "--target", target, "--reconcile-job", "--cleanup-merged", "--verify-pr", "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("intake github verify dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var result intakePublishResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode intake dry-run json: %v\nbody=%s", err, out.String())
	}
	if !result.DryRun || result.CleanupPreview == nil || !result.CleanupPreview.VerifyPR || result.CleanupPreview.PRVerification == nil || !result.CleanupPreview.PRVerification.Verified {
		t.Fatalf("intake verify preview = %+v", result.CleanupPreview)
	}
	if !branchExists(t, target, branch) {
		t.Fatalf("dry-run removed branch %s", branch)
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

func TestIntakeScheduleAcceptsPayloadFileAndRejectsConflict(t *testing.T) {
	tmp := t.TempDir()
	payloadFile := filepath.Join(tmp, "schedule-payload.json")
	if err := os.WriteFile(payloadFile, []byte(`{"workspace":"file","from_file":true,"name":"ignored"}`), 0o644); err != nil {
		t.Fatalf("write payload file: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"intake", "schedule", "nightly", "--payload-file", payloadFile, "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("intake schedule payload-file dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var result intakePublishResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode intake schedule payload-file json: %v\nbody=%s", err, out.String())
	}
	if !result.DryRun || result.Event == nil || result.Event.Type != "schedule" {
		t.Fatalf("result = %+v", result)
	}
	if result.Event.Payload["workspace"] != "file" || result.Event.Payload["from_file"] != true {
		t.Fatalf("payload-file result = %+v", result.Event.Payload)
	}
	if result.Event.Payload["name"] != "nightly" || result.Event.Payload["source"] != "schedule" {
		t.Fatalf("identity fields should be preserved: %+v", result.Event.Payload)
	}

	conflict := NewRootCmd()
	conflictOut, conflictErr := &bytes.Buffer{}, &bytes.Buffer{}
	conflict.SetOut(conflictOut)
	conflict.SetErr(conflictErr)
	conflict.SetArgs([]string{"intake", "schedule", "nightly", "--payload", `{}`, "--payload-file", payloadFile, "--dry-run"})
	if err := conflict.Execute(); err == nil {
		t.Fatalf("intake schedule payload conflict succeeded: stdout=%s", conflictOut.String())
	}
	if !strings.Contains(conflictErr.String(), "choose one of --payload or --payload-file") {
		t.Fatalf("conflict stderr = %q", conflictErr.String())
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
