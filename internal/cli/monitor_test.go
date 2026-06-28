package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
)

func TestMonitorCommandJSONDoesNotExitUnhealthy(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"monitor", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("monitor --json should not fail on unhealthy fleet: %v", err)
	}

	var body monitorSnapshot
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode monitor json: %v\nbody=%s", err, stdout.String())
	}
	if body.Health == nil || body.Health.Healthy {
		t.Fatalf("monitor health should report unhealthy daemon-down state: %+v", body.Health)
	}
	if body.Plan != nil {
		t.Fatalf("plan should be omitted unless --plan is set: %+v", body.Plan)
	}
	if body.Stats == nil {
		t.Fatalf("stats should encode as an empty array, not null")
	}
	if body.StatsError != "" {
		t.Fatalf("stats_error = %q, want empty local fallback error", body.StatsError)
	}
	var raw map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw monitor json: %v\nbody=%s", err, stdout.String())
	}
	health, ok := raw["health"].(map[string]any)
	if !ok {
		t.Fatalf("raw health = %#v, want object", raw["health"])
	}
	queue, ok := health["queue"].(map[string]any)
	if !ok {
		t.Fatalf("raw health.queue = %#v, want object", health["queue"])
	}
	if _, ok := queue["instances"].(map[string]any); !ok {
		t.Fatalf("raw health.queue.instances = %#v, want empty object", queue["instances"])
	}
	if _, ok := queue["events"].(map[string]any); !ok {
		t.Fatalf("raw health.queue.events = %#v, want empty object", queue["events"])
	}
}

func TestMonitorLastMessageRewritesRuntimeHealthActions(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:  "runtime-stale",
		Agent:     "worker",
		Job:       "SQU-88",
		Runtime:   "codex",
		Status:    daemon.StatusRunning,
		PID:       99999999,
		Workspace: tmp,
		StartedAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("write runtime metadata: %v", err)
	}

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--target", tmp, "--last-message", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("monitor last-message json: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot monitorSnapshot
	if err := json.Unmarshal(stdout.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode monitor last-message json: %v\nbody=%s", err, stdout.String())
	}
	assertHealthIssueAction(t, snapshot.Health, "runtime_stale", "agent-team job resume-plan squ-88 --runtime-stale --last-message")

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"monitor", "--target", tmp, "--last-message"})
	if err := text.Execute(); err != nil {
		t.Fatalf("monitor last-message text: %v\nstderr=%s", err, textErr.String())
	}
	if !strings.Contains(textOut.String(), "agent-team job resume-plan squ-88 --runtime-stale --last-message") {
		t.Fatalf("monitor text missing last-message action:\n%s", textOut.String())
	}

	summary := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(summaryOut)
	summary.SetErr(summaryErr)
	summary.SetArgs([]string{"monitor", "--target", tmp, "--summary", "--last-message", "--json"})
	if err := summary.Execute(); err != nil {
		t.Fatalf("monitor summary last-message json: %v\nstderr=%s", err, summaryErr.String())
	}
	var health healthResult
	if err := json.Unmarshal(summaryOut.Bytes(), &health); err != nil {
		t.Fatalf("decode monitor summary health json: %v\nbody=%s", err, summaryOut.String())
	}
	assertHealthIssueAction(t, &health, "runtime_stale", "agent-team job resume-plan squ-88 --runtime-stale --last-message")
}

func assertHealthIssueAction(t *testing.T, health *healthResult, code, action string) {
	t.Helper()
	if health == nil {
		t.Fatalf("health is nil, want issue %s action %q", code, action)
	}
	for _, issue := range health.Issues {
		if issue.Code == code && containsString(issue.Actions, action) {
			return
		}
	}
	t.Fatalf("health issue action %q missing for code %s: %+v", action, code, health.Issues)
}

func TestTeamMonitorLastMessageScopesRuntimeHealthActions(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(filepath.Join(teamDir, "agents", "worker"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.worker]
agent = "worker"
ephemeral = true

[instances.build-worker]
agent = "worker"
ephemeral = true

[teams.delivery]
instances = ["worker"]

[teams.platform]
instances = ["build-worker"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, meta := range []*daemon.Metadata{
		{Instance: "worker-squ-902", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999999, Workspace: root, StartedAt: now},
		{Instance: "build-worker-1", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999999, Workspace: root, StartedAt: now},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "monitor", "delivery", "--repo", root, "--runtime-stale", "--last-message", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team monitor runtime-stale last-message: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot monitorSnapshot
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode team monitor last-message: %v\nbody=%s", err, out.String())
	}
	wantAction := "agent-team team resume-plan delivery --runtime-stale --sort stale --limit 10 --last-message"
	assertHealthIssueAction(t, snapshot.Health, "runtime_stale", wantAction)
	for _, issue := range snapshot.Health.Issues {
		for _, action := range issue.Actions {
			if strings.Contains(action, "build-worker-1") || strings.Contains(action, "agent-team resume-plan worker-squ-902 --runtime-stale") {
				t.Fatalf("team monitor leaked unscoped action %q in %+v", action, snapshot.Health.Issues)
			}
		}
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"team", "monitor", "delivery", "--repo", root, "--runtime-stale", "--last-message"})
	if err := text.Execute(); err != nil {
		t.Fatalf("team monitor text last-message: %v\nstderr=%s", err, textErr.String())
	}
	if !strings.Contains(textOut.String(), wantAction) {
		t.Fatalf("team monitor text missing %q:\n%s", wantAction, textOut.String())
	}
	if strings.Contains(textOut.String(), "build-worker-1") || strings.Contains(textOut.String(), "agent-team resume-plan worker-squ-902 --runtime-stale") {
		t.Fatalf("team monitor text leaked unscoped or unrelated runtime:\n%s", textOut.String())
	}
}

func TestMonitorCommandsPrintsVisibleSectionActions(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"

[instances.worker]
agent = "worker"
ephemeral = true
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:  "runtime-stale",
		Agent:     "worker",
		Job:       "SQU-88",
		Runtime:   "codex",
		Status:    daemon.StatusRunning,
		PID:       99999999,
		Workspace: tmp,
		StartedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("write runtime metadata: %v", err)
	}
	failed := mustNewJob(t, "SQU-701", "worker")
	failed.Status = job.StatusFailed
	failed.UpdatedAt = now.Add(-2 * time.Hour)
	if err := job.Write(teamDir, failed); err != nil {
		t.Fatalf("write failed job: %v", err)
	}
	ready := mustNewJob(t, "SQU-702", "manager")
	ready.Pipeline = "ticket_to_pr"
	ready.Steps = []job.Step{
		{ID: "triage", Target: "ticket-manager", Status: job.StatusDone, StartedAt: now.Add(-2 * time.Hour), FinishedAt: now.Add(-time.Hour)},
		{ID: "implement", Target: "worker", Status: job.StatusBlocked, After: []string{"triage"}},
	}
	if err := job.Write(teamDir, ready); err != nil {
		t.Fatalf("write ready job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--target", tmp, "--last-message", "--plan", "--jobs", "--commands"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("monitor --commands: %v\nstderr=%s", err, stderr.String())
	}
	body := out.String()
	scope := operatorCommandScope{Repo: tmp, Set: true}
	wantCommands := []string{
		scopedOperatorAction("agent-team job resume-plan squ-88 --runtime-stale --last-message", scope),
		scopedOperatorAction("agent-team sync --dry-run", scope),
		scopedOperatorAction("agent-team job retry squ-701 --dispatch", scope),
		scopedOperatorAction("agent-team job advance squ-702", scope),
	}
	for _, want := range wantCommands {
		if !strings.Contains(body, want) {
			t.Fatalf("monitor --commands missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "HEALTH") || strings.Contains(body, "INSTANCES") || strings.Contains(body, "Ready pipeline steps:") {
		t.Fatalf("monitor --commands included dashboard text:\n%s", body)
	}
}

func TestTeamMonitorCommandsPrintsScopedSectionActions(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
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
		{Instance: "worker-squ-902", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999999, Workspace: root, StartedAt: now},
		{Instance: "build-worker-1", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999998, Workspace: root, StartedAt: now},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	failed := mustNewJob(t, "SQU-901", "worker")
	failed.Status = job.StatusFailed
	failed.UpdatedAt = now.Add(-2 * time.Hour)
	if err := job.Write(teamDir, failed); err != nil {
		t.Fatalf("write failed job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "monitor", "delivery", "--repo", root, "--last-message", "--plan", "--jobs", "--commands"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team monitor --commands: %v\nstderr=%s", err, stderr.String())
	}
	body := out.String()
	scope := operatorCommandScope{Repo: root, Set: true}
	wantCommands := []string{
		scopedOperatorAction("agent-team team resume-plan delivery --runtime-stale --sort stale --limit 10 --last-message", scope),
		scopedOperatorAction("agent-team team sync delivery --dry-run", scope),
		scopedOperatorAction("agent-team job retry squ-901 --dispatch", scope),
	}
	for _, want := range wantCommands {
		if !strings.Contains(body, want) {
			t.Fatalf("team monitor --commands missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "build-worker-1") || strings.Contains(body, "agent-team resume-plan worker-squ-902 --runtime-stale") {
		t.Fatalf("team monitor --commands leaked unscoped or unrelated runtime:\n%s", body)
	}
}

func TestMonitorCommandsRejectsIncompatibleOutputModes(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "json", args: []string{"monitor", "--target", tmp, "--commands", "--json"}, want: "--commands cannot be combined with --json"},
		{name: "summary", args: []string{"monitor", "--target", tmp, "--commands", "--summary"}, want: "--commands cannot be combined with --summary"},
		{name: "watch", args: []string{"monitor", "--target", tmp, "--commands", "--watch"}, want: "--commands cannot be combined with --watch"},
		{name: "team watch", args: []string{"team", "watch", "delivery", "--repo", tmp, "--commands"}, want: "--commands cannot be combined with --watch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(stdout)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil {
				t.Fatalf("%s succeeded; stdout=%s", strings.Join(tc.args, " "), stdout.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestMonitorReportsUnreadInboxSummary(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.AppendMessage(daemon.DaemonRoot(teamDir), "manager", &daemon.Message{
		ID:   "msg-monitor-inbox",
		From: "operator",
		Body: "please check diagnostics",
	}); err != nil {
		t.Fatalf("append message: %v", err)
	}
	if err := daemon.AppendMessage(daemon.DaemonRoot(teamDir), "worker", &daemon.Message{
		ID:   "msg-monitor-worker",
		From: "operator",
		Body: "worker-only diagnostics",
	}); err != nil {
		t.Fatalf("append worker message: %v", err)
	}

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("monitor --json unread inbox: %v\nstderr=%s", err, stderr.String())
	}
	var body monitorSnapshot
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode monitor inbox json: %v\nbody=%s", err, stdout.String())
	}
	if body.Inbox.Total != 2 || body.Inbox.Unread != 2 || body.Inbox.UnreadInstances != 2 || !stringSliceContains(body.Inbox.UnreadNames, "manager") || !stringSliceContains(body.Inbox.UnreadNames, "worker") {
		t.Fatalf("inbox summary = %+v", body.Inbox)
	}
	if strings.Contains(stdout.String(), "please check diagnostics") || strings.Contains(stdout.String(), "worker-only diagnostics") {
		t.Fatalf("monitor json should not include inbox bodies:\n%s", stdout.String())
	}

	filtered := NewRootCmd()
	filteredOut, filteredErr := &bytes.Buffer{}, &bytes.Buffer{}
	filtered.SetOut(filteredOut)
	filtered.SetErr(filteredErr)
	filtered.SetArgs([]string{"monitor", "--json", "--instance", "manager", "--target", tmp})
	if err := filtered.Execute(); err != nil {
		t.Fatalf("monitor --instance manager inbox: %v\nstderr=%s", err, filteredErr.String())
	}
	var filteredBody monitorSnapshot
	if err := json.Unmarshal(filteredOut.Bytes(), &filteredBody); err != nil {
		t.Fatalf("decode filtered monitor inbox json: %v\nbody=%s", err, filteredOut.String())
	}
	if filteredBody.Inbox.Total != 1 || filteredBody.Inbox.Unread != 1 || !stringSliceContains(filteredBody.Inbox.UnreadNames, "manager") || stringSliceContains(filteredBody.Inbox.UnreadNames, "worker") {
		t.Fatalf("filtered inbox summary = %+v", filteredBody.Inbox)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"monitor", "--target", tmp})
	if err := text.Execute(); err != nil {
		t.Fatalf("monitor text unread inbox: %v\nstderr=%s", err, textErr.String())
	}
	if !strings.Contains(textOut.String(), "inbox: instances=2 total=2 unread=2 unread_instances=2") {
		t.Fatalf("monitor text missing inbox summary:\n%s", textOut.String())
	}
	if strings.Contains(textOut.String(), "please check diagnostics") || strings.Contains(textOut.String(), "worker-only diagnostics") {
		t.Fatalf("monitor text should not include inbox bodies:\n%s", textOut.String())
	}
}

func TestMonitorReportsQueueAndOutboxHealth(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Date(2026, 6, 27, 21, 0, 0, 0, time.UTC)
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), &daemon.QueueItem{
		ID:             "q-monitor-dead",
		State:          daemon.QueueStateDead,
		EventType:      "agent.dispatch",
		Instance:       "worker",
		InstanceID:     "worker-squ-920",
		Payload:        map[string]any{"target": "worker", "ticket": "SQU-920"},
		Attempts:       daemon.MaxQueueAttempts,
		LastError:      "spawn failed",
		QueuedAt:       now.Add(-time.Hour),
		UpdatedAt:      now,
		DeadLetteredAt: now,
	}); err != nil {
		t.Fatalf("write queue item: %v", err)
	}
	writeQuarantinedOutboxFile(t, teamDir, "20260627T210000.000000000Z", daemon.OutboxStatePending, &daemon.OutboxItem{
		ID:        "outbox-monitor-quarantined",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Source:    "manager",
		Payload:   map[string]any{"target": "worker", "ticket": "SQU-920"},
		CreatedAt: now.Add(-time.Minute),
		UpdatedAt: now,
	})

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("monitor --json queue/outbox health: %v\nstderr=%s", err, stderr.String())
	}
	var body monitorSnapshot
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode monitor queue/outbox json: %v\nbody=%s", err, stdout.String())
	}
	if body.Health == nil {
		t.Fatalf("health missing: %+v", body)
	}
	if body.Health.Queue.Total != 1 || body.Health.Queue.Dead != 1 {
		t.Fatalf("queue = %+v, want one dead item", body.Health.Queue)
	}
	if body.Health.OutboxQuarantine.Quarantined != 1 || body.Health.OutboxQuarantine.Restorable != 1 || body.Health.OutboxQuarantine.Unrestorable != 0 {
		t.Fatalf("outbox quarantine = %+v, want one restorable item", body.Health.OutboxQuarantine)
	}
	codes := map[string]bool{}
	for _, issue := range body.Health.Issues {
		codes[issue.Code] = true
	}
	for _, want := range []string{"queue_dead_letter", "outbox_quarantined"} {
		if !codes[want] {
			t.Fatalf("issues = %+v, missing %s", body.Health.Issues, want)
		}
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"monitor", "--target", tmp})
	if err := text.Execute(); err != nil {
		t.Fatalf("monitor text queue/outbox health: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"queue: total=1", "dead=1", "outbox quarantine: quarantined=1 restorable=1 unrestorable=0", "queue_dead_letter", "outbox_quarantined"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("monitor text missing %q:\n%s", want, textOut.String())
		}
	}
}

func TestTeamMonitorReportsScopedOutboxQuarantineHealth(t *testing.T) {
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

[instances.other]
agent = "other"

[teams.delivery]
instances = ["manager", "worker"]

[teams.platform]
instances = ["other"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 27, 21, 30, 0, 0, time.UTC)
	writeQuarantinedOutboxFile(t, teamDir, "20260627T213000.000000000Z", daemon.OutboxStatePending, &daemon.OutboxItem{
		ID:        "outbox-delivery-monitor-quarantined",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Source:    "manager",
		Payload:   map[string]any{"target": "worker", "ticket": "SQU-921"},
		CreatedAt: now,
		UpdatedAt: now,
	})
	writeQuarantinedOutboxFile(t, teamDir, "20260627T213000.000000000Z", daemon.OutboxStatePending, &daemon.OutboxItem{
		ID:        "outbox-platform-monitor-quarantined",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Source:    "manager",
		Payload:   map[string]any{"target": "other", "ticket": "OTH-921"},
		CreatedAt: now,
		UpdatedAt: now,
	})

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "monitor", "delivery", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team monitor --json outbox quarantine: %v\nstderr=%s", err, stderr.String())
	}
	var body monitorSnapshot
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode team monitor outbox quarantine json: %v\nbody=%s", err, stdout.String())
	}
	if body.Team == nil || body.Team.Name != "delivery" || body.Health == nil {
		t.Fatalf("team monitor snapshot = %+v", body)
	}
	if body.Health.OutboxQuarantine.Quarantined != 1 || body.Health.OutboxQuarantine.Restorable != 1 || body.Health.OutboxQuarantine.Unrestorable != 0 {
		t.Fatalf("team outbox quarantine = %+v", body.Health.OutboxQuarantine)
	}
	var sawScopedAction bool
	for _, issue := range body.Health.Issues {
		if issue.Code == "outbox_quarantined" && containsString(issue.Actions, "agent-team team outbox quarantine delivery") && containsString(issue.Actions, "agent-team team outbox quarantine delivery --restorable") {
			sawScopedAction = true
		}
	}
	if !sawScopedAction {
		t.Fatalf("issues = %+v, missing scoped outbox quarantine action", body.Health.Issues)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"team", "monitor", "delivery", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("team monitor text outbox quarantine: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"Team: delivery", "outbox quarantine: quarantined=1 restorable=1 unrestorable=0", "outbox_quarantined", "agent-team team outbox quarantine delivery --restorable"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("team monitor text missing %q:\n%s", want, textOut.String())
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	watch := NewRootCmd()
	watch.SetContext(ctx)
	watchOut, watchErr := &bytes.Buffer{}, &bytes.Buffer{}
	watch.SetOut(watchOut)
	watch.SetErr(watchErr)
	watch.SetArgs([]string{"team", "watch", "delivery", "--repo", root, "--json", "--interval", "1ms"})
	if err := watch.Execute(); err != nil {
		t.Fatalf("team watch json outbox quarantine: %v\nstderr=%s", err, watchErr.String())
	}
	watchBody := strings.TrimSpace(watchOut.String())
	if watchBody == "" {
		t.Fatalf("team watch emitted no snapshot")
	}
	var watched monitorSnapshot
	if err := json.Unmarshal([]byte(strings.Split(watchBody, "\n")[0]), &watched); err != nil {
		t.Fatalf("decode team watch snapshot: %v\nbody=%s", err, watchOut.String())
	}
	if watched.Team == nil || watched.Team.Name != "delivery" || watched.Health == nil || watched.Health.OutboxQuarantine.Quarantined != 1 {
		t.Fatalf("team watch snapshot = %+v", watched)
	}
}

func TestMonitorSummaryJSONUsesHealthSnapshot(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--summary", "--json", "--agent", "manager", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("monitor --summary --json should not fail on unhealthy fleet: %v\nstderr: %s", err, stderr.String())
	}

	var body healthResult
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode monitor summary json: %v\nbody=%s", err, stdout.String())
	}
	if body.Healthy || body.Daemon.Running {
		t.Fatalf("monitor summary should report daemon-down health: %+v", body)
	}
	if body.Summary.Total != 0 {
		t.Fatalf("summary total = %d, want zero matching runtime rows", body.Summary.Total)
	}
	if body.Declared.Persistent != 1 || body.Declared.Missing != 1 {
		t.Fatalf("declared summary = %+v, want one missing manager declaration", body.Declared)
	}
}

func TestMonitorSummaryLatestJSONScopesHealthRows(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-5 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--summary", "--latest", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("monitor --summary --latest --json should not fail: %v\nstderr: %s", err, stderr.String())
	}
	var body healthResult
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode monitor summary latest json: %v\nbody=%s", err, stdout.String())
	}
	if body.Summary.Total != 1 || body.Summary.Stopped != 1 {
		t.Fatalf("summary = %+v, want one stopped latest row", body.Summary)
	}
	if len(body.Instances) != 1 || body.Instances[0].Instance != "new" {
		t.Fatalf("instances = %+v, want only newest row", body.Instances)
	}
}

func TestMonitorStrictTopologyUsesHealthOption(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance: "adhoc",
		Agent:    "worker",
		Status:   daemon.StatusRunning,
		PID:      123,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	defaultSnapshot, err := collectMonitorSnapshot(teamDir, time.Now(), nil, monitorOptions{})
	if err != nil {
		t.Fatalf("collect default monitor: %v", err)
	}
	for _, issue := range defaultSnapshot.Health.Issues {
		if issue.Code == "topology_extra_running" {
			t.Fatalf("default monitor should not flag topology extras: %+v", defaultSnapshot.Health.Issues)
		}
	}

	strictSnapshot, err := collectMonitorSnapshot(teamDir, time.Now(), nil, monitorOptions{StrictTopology: true})
	if err != nil {
		t.Fatalf("collect strict monitor: %v", err)
	}
	var found bool
	for _, issue := range strictSnapshot.Health.Issues {
		if issue.Code == "topology_extra_running" && issue.Instance == "adhoc" {
			found = true
		}
	}
	if !found {
		t.Fatalf("strict monitor health issues = %+v, want adhoc topology_extra_running", strictSnapshot.Health.Issues)
	}
}

func TestMonitorSummaryPlanJSONIncludesPlanSummary(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--summary", "--plan", "--json", "--agent", "manager", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("monitor --summary --plan --json: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	var body monitorSummarySnapshot
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode monitor summary plan json: %v\nbody=%s", err, stdout.String())
	}
	if body.Health == nil || body.Health.Healthy {
		t.Fatalf("health = %+v, want daemon-down health", body.Health)
	}
	if body.Plan == nil {
		t.Fatalf("plan summary missing: %+v", body)
	}
	if body.Plan.Summary.Total != 1 || body.Plan.Summary.Actions["start"] != 1 || !body.Plan.Summary.DryRun {
		t.Fatalf("plan summary = %+v, want one dry-run manager start", body.Plan.Summary)
	}
}

func TestMonitorSummaryResourcesJSONIncludesStatsSummary(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:  "manager",
		Agent:     "manager",
		Status:    daemon.StatusStopped,
		PID:       321,
		StartedAt: now.Add(-5 * time.Minute),
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "done"
description = "complete"
`, now)

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--summary", "--resources", "--all", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("monitor --summary --resources --json: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	var body monitorSummarySnapshot
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode monitor summary resources json: %v\nbody=%s", err, stdout.String())
	}
	if body.Health == nil {
		t.Fatalf("health missing: %+v", body)
	}
	if body.Resources == nil {
		t.Fatalf("resources summary missing: %+v", body)
	}
	if body.Resources.Total != 1 || body.Resources.Stopped != 1 || body.Resources.Measured != 0 || body.Resources.Phases["done"] != 1 {
		t.Fatalf("resources summary = %+v, want one stopped done manager", body.Resources)
	}
	if body.Plan != nil || body.Events != nil {
		t.Fatalf("plan/events should be omitted unless requested: plan=%+v events=%+v", body.Plan, body.Events)
	}
}

func TestMonitorJobsJSONIncludesJobTriage(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	j := mustNewJob(t, "SQU-701", "worker")
	j.Status = job.StatusFailed
	j.LastStatus = "needs review"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	blocked := mustNewJob(t, "SQU-702", "worker")
	if err := job.Write(teamDir, blocked); err != nil {
		t.Fatalf("write blocked preview job: %v", err)
	}
	now := time.Now().UTC()
	pipelineJob := &job.Job{
		ID:        "squ-703",
		Ticket:    "SQU-703",
		Target:    "worker",
		Instance:  "worker-squ-703",
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
		t.Fatalf("write pipeline job: %v", err)
	}
	writeStatus(t, filepath.Join(teamDir, "state", "worker-squ-702"), `[status]
phase = "blocked"
description = "needs credentials"
since = "2026-06-18T12:00:00Z"

[work]
job = "squ-702"
ticket = "SQU-702"
branch = "worker-squ-702"
`, time.Now().UTC())

	summary := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(summaryOut)
	summary.SetErr(summaryErr)
	summary.SetArgs([]string{"monitor", "--summary", "--jobs", "--json", "--target", tmp})
	if err := summary.Execute(); err != nil {
		t.Fatalf("monitor --summary --jobs --json: %v\nstdout=%s\nstderr=%s", err, summaryOut.String(), summaryErr.String())
	}
	var summaryBody monitorSummarySnapshot
	if err := json.Unmarshal(summaryOut.Bytes(), &summaryBody); err != nil {
		t.Fatalf("decode monitor summary jobs json: %v\nbody=%s", err, summaryOut.String())
	}
	if summaryBody.Jobs == nil || summaryBody.Jobs.Summary.Failed != 1 || len(summaryBody.Jobs.Attention) != 2 {
		t.Fatalf("summary jobs = %+v", summaryBody.Jobs)
	}
	summaryAttention := map[string]bool{}
	for _, item := range summaryBody.Jobs.Attention {
		summaryAttention[item.JobID] = true
	}
	if !summaryAttention["squ-701"] || !summaryAttention["squ-702"] {
		t.Fatalf("summary attention = %+v", summaryBody.Jobs.Attention)
	}
	if len(summaryBody.JobStatus) != 1 || summaryBody.JobStatus[0].JobID != "squ-702" || summaryBody.JobStatus[0].After != job.StatusBlocked || !summaryBody.JobStatus[0].Changed {
		t.Fatalf("summary job status = %+v", summaryBody.JobStatus)
	}
	if len(summaryBody.PipelineStatus) == 0 || summaryBody.PipelineStatus[0].Pipeline != "ticket_to_pr" || summaryBody.PipelineStatus[0].ReadySteps != 1 {
		t.Fatalf("summary pipeline status = %+v", summaryBody.PipelineStatus)
	}

	summaryText := NewRootCmd()
	summaryTextOut, summaryTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	summaryText.SetOut(summaryTextOut)
	summaryText.SetErr(summaryTextErr)
	summaryText.SetArgs([]string{"monitor", "--summary", "--jobs", "--target", tmp})
	if err := summaryText.Execute(); err != nil {
		t.Fatalf("monitor --summary --jobs text: %v\nstdout=%s\nstderr=%s", err, summaryTextOut.String(), summaryTextErr.String())
	}
	if !strings.Contains(summaryTextOut.String(), "job status: previews=1 changes=1 blocked=1") || !strings.Contains(summaryTextOut.String(), "pipeline status: pipelines=") {
		t.Fatalf("summary text missing job status:\n%s", summaryTextOut.String())
	}

	full := NewRootCmd()
	fullOut, fullErr := &bytes.Buffer{}, &bytes.Buffer{}
	full.SetOut(fullOut)
	full.SetErr(fullErr)
	full.SetArgs([]string{"monitor", "--jobs", "--json", "--target", tmp})
	if err := full.Execute(); err != nil {
		t.Fatalf("monitor --jobs --json: %v\nstdout=%s\nstderr=%s", err, fullOut.String(), fullErr.String())
	}
	var fullBody monitorSnapshot
	if err := json.Unmarshal(fullOut.Bytes(), &fullBody); err != nil {
		t.Fatalf("decode monitor jobs json: %v\nbody=%s", err, fullOut.String())
	}
	if fullBody.Jobs == nil || len(fullBody.Jobs.Attention) != 2 {
		t.Fatalf("full jobs = %+v", fullBody.Jobs)
	}
	fullAttention := map[string]bool{}
	for _, item := range fullBody.Jobs.Attention {
		fullAttention[item.JobID] = true
	}
	if !fullAttention["squ-701"] || !fullAttention["squ-702"] {
		t.Fatalf("full attention = %+v", fullBody.Jobs.Attention)
	}
	if len(fullBody.JobStatus) != 1 || fullBody.JobStatus[0].JobID != "squ-702" || fullBody.JobStatus[0].After != job.StatusBlocked {
		t.Fatalf("full job status = %+v", fullBody.JobStatus)
	}
	if len(fullBody.PipelineStatus) == 0 || fullBody.PipelineStatus[0].ReadySteps != 1 {
		t.Fatalf("full pipeline status = %+v", fullBody.PipelineStatus)
	}

	fullText := NewRootCmd()
	fullTextOut, fullTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	fullText.SetOut(fullTextOut)
	fullText.SetErr(fullTextErr)
	fullText.SetArgs([]string{"monitor", "--jobs", "--target", tmp})
	if err := fullText.Execute(); err != nil {
		t.Fatalf("monitor --jobs text: %v\nstdout=%s\nstderr=%s", err, fullTextOut.String(), fullTextErr.String())
	}
	if !strings.Contains(fullTextOut.String(), "pipeline status:") || !strings.Contains(fullTextOut.String(), "ticket_to_pr") {
		t.Fatalf("full text missing pipeline status:\n%s", fullTextOut.String())
	}
}

func TestMonitorSchedulesJSONIncludesForecast(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	writeScheduleTopology(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	if err := daemon.WriteScheduleState(daemon.DaemonRoot(teamDir), &daemon.ScheduleState{
		Name:       "hourly",
		LastSeenAt: now.Add(-30 * time.Minute),
	}); err != nil {
		t.Fatalf("WriteScheduleState hourly: %v", err)
	}

	summary := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(summaryOut)
	summary.SetErr(summaryErr)
	summary.SetArgs([]string{"monitor", "--summary", "--schedules", "--json", "--target", tmp})
	if err := summary.Execute(); err != nil {
		t.Fatalf("monitor --summary --schedules --json: %v\nstdout=%s\nstderr=%s", err, summaryOut.String(), summaryErr.String())
	}
	var summaryBody monitorSummarySnapshot
	if err := json.Unmarshal(summaryOut.Bytes(), &summaryBody); err != nil {
		t.Fatalf("decode monitor summary schedules json: %v\nbody=%s", err, summaryOut.String())
	}
	if summaryBody.Schedules == nil || summaryBody.Schedules.Total != 2 || summaryBody.Schedules.Due != 1 || summaryBody.Schedules.Upcoming != 1 {
		t.Fatalf("summary schedules = %+v", summaryBody.Schedules)
	}
	if summaryBody.Schedules.Rows[0].Name != "nightly" || summaryBody.Schedules.Rows[0].DueReason != "run_on_start" {
		t.Fatalf("summary schedule rows = %+v", summaryBody.Schedules.Rows)
	}

	full := NewRootCmd()
	fullOut, fullErr := &bytes.Buffer{}, &bytes.Buffer{}
	full.SetOut(fullOut)
	full.SetErr(fullErr)
	full.SetArgs([]string{"monitor", "--schedules", "--json", "--target", tmp})
	if err := full.Execute(); err != nil {
		t.Fatalf("monitor --schedules --json: %v\nstdout=%s\nstderr=%s", err, fullOut.String(), fullErr.String())
	}
	var fullBody monitorSnapshot
	if err := json.Unmarshal(fullOut.Bytes(), &fullBody); err != nil {
		t.Fatalf("decode monitor schedules json: %v\nbody=%s", err, fullOut.String())
	}
	if fullBody.Schedules == nil || fullBody.Schedules.Due != 1 || len(fullBody.Schedules.Rows) != 2 {
		t.Fatalf("full schedules = %+v", fullBody.Schedules)
	}
}

func TestMonitorSummaryResourcesHonorsStaleFilter(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	old := time.Now().Add(-staleAfter - time.Minute)
	fresh := time.Now()
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusStopped, PID: 321, StartedAt: old},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusStopped, PID: 654, StartedAt: fresh},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "implementing"
description = "stale work"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "worker"), `[status]
phase = "implementing"
description = "fresh work"
`, fresh)

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--summary", "--resources", "--stale", "--all", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("monitor --summary --resources --stale --json: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	var body monitorSummarySnapshot
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode monitor stale resources json: %v\nbody=%s", err, stdout.String())
	}
	if body.Health == nil || body.Health.Summary.Total != 1 || body.Health.Summary.Stale != 1 {
		t.Fatalf("health summary = %+v, want one stale instance", body.Health)
	}
	if body.Resources == nil {
		t.Fatalf("resources summary missing: %+v", body)
	}
	if body.Resources.Total != 1 || body.Resources.Stopped != 1 || body.Resources.Phases["implementing"] != 1 {
		t.Fatalf("resources summary = %+v, want stale manager only", body.Resources)
	}
}

func TestMonitorSummaryResourcesHonorsUnhealthyFilter(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "crashed", Agent: "worker", Status: daemon.StatusCrashed, PID: 123, StartedAt: old},
		{Instance: "fresh", Agent: "worker", Status: daemon.StatusStopped, PID: 456, StartedAt: now},
		{Instance: "stale", Agent: "manager", Status: daemon.StatusStopped, PID: 789, StartedAt: old},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "stale"), `[status]
phase = "implementing"
description = "stale work"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "fresh"), `[status]
phase = "idle"
description = "fresh work"
`, now)

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--summary", "--resources", "--unhealthy", "--all", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("monitor --summary --resources --unhealthy --json: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	var body monitorSummarySnapshot
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode monitor unhealthy resources json: %v\nbody=%s", err, stdout.String())
	}
	if body.Health == nil || body.Health.Summary.Total != 2 || body.Health.Summary.Crashed != 1 || body.Health.Summary.Stale != 1 {
		t.Fatalf("health summary = %+v, want crashed and stale instances only", body.Health)
	}
	if body.Resources == nil {
		t.Fatalf("resources summary missing: %+v", body)
	}
	if body.Resources.Total != 2 || body.Resources.Crashed != 1 || body.Resources.Stale != 1 {
		t.Fatalf("resources summary = %+v, want crashed and stale instances only", body.Resources)
	}
}

func TestMonitorRejectsInvalidLatestLastOptions(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "negative-last",
			args: []string{"monitor", "--last", "-1"},
			want: "--last must be >= 0",
		},
		{
			name: "latest-and-last",
			args: []string{"monitor", "--latest", "--last", "2"},
			want: "choose one of --latest or --last",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewRootCmd()
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(stdout)
			cmd.SetErr(stderr)
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected validation error; stdout=%s stderr=%s", stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tt.want)
			}
		})
	}
}

func TestMonitorPlanJSONUsesFilters(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--json", "--plan", "--agent", "manager", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("monitor --json --plan: %v\nstderr: %s", err, stderr.String())
	}

	var body monitorSnapshot
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode monitor json: %v\nbody=%s", err, stdout.String())
	}
	if body.Plan == nil {
		t.Fatalf("monitor --plan should include plan: %+v", body)
	}
	if body.Plan.Summary.Total != 1 || body.Plan.Summary.Start != 1 {
		t.Fatalf("plan summary = %+v, want one manager start", body.Plan.Summary)
	}
	if len(body.Plan.Instances) != 1 || body.Plan.Instances[0].Instance != "manager" {
		t.Fatalf("plan rows = %+v, want only manager", body.Plan.Instances)
	}
}

func TestMonitorPlanJSONFiltersByAction(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--json", "--plan", "--action", "on_demand", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("monitor --json --plan --action on_demand: %v\nstderr: %s", err, stderr.String())
	}

	var body monitorSnapshot
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode monitor json: %v\nbody=%s", err, stdout.String())
	}
	if body.Plan == nil {
		t.Fatalf("monitor --plan should include plan: %+v", body)
	}
	if body.Plan.Summary.Total != 1 || body.Plan.Summary.OnDemand != 1 {
		t.Fatalf("plan summary = %+v, want one on-demand row", body.Plan.Summary)
	}
	if len(body.Plan.Instances) != 1 || body.Plan.Instances[0].Instance != "worker" || body.Plan.Instances[0].Action != "on-demand" {
		t.Fatalf("plan rows = %+v, want worker on-demand only", body.Plan.Instances)
	}
}

func TestMonitorCommandLastJSONLimitsRowsByLatestStarted(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "manager", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(-3 * time.Hour)},
		{Instance: "mid", Agent: "manager", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "manager", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(-1 * time.Hour)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--json", "--last", "2", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("monitor --json --last 2: %v\nstderr: %s", err, stderr.String())
	}

	var body monitorSnapshot
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode monitor json: %v\nbody=%s", err, stdout.String())
	}
	if got, want := rowInstances(body.Instances), []string{"new", "mid"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("instances = %v, want %v", got, want)
	}
}

func TestMonitorPlanStopExtrasJSON(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance: "adhoc",
		Agent:    "manager",
		Status:   daemon.StatusRunning,
		PID:      os.Getpid(),
	}); err != nil {
		t.Fatalf("write adhoc metadata: %v", err)
	}

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--json", "--plan", "--stop-extras", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("monitor --json --plan --stop-extras: %v\nstderr: %s", err, stderr.String())
	}

	var body monitorSnapshot
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode monitor json: %v\nbody=%s", err, stdout.String())
	}
	if body.Plan == nil {
		t.Fatalf("monitor --plan should include plan: %+v", body)
	}
	if body.Plan.Summary.Stop != 1 || body.Plan.Summary.Extra != 0 {
		t.Fatalf("plan summary = %+v, want one stop preview and no remaining extras", body.Plan.Summary)
	}
	var found bool
	for _, row := range body.Plan.Instances {
		if row.Instance == "adhoc" {
			found = true
			if row.Kind != "extra" || row.Status != "running" || row.Action != "stop" {
				t.Fatalf("adhoc row = %+v, want extra/running/stop", row)
			}
		}
	}
	if !found {
		t.Fatalf("plan rows missing adhoc: %+v", body.Plan.Instances)
	}
}

func TestMonitorStopExtrasRequiresPlan(t *testing.T) {
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--stop-extras"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("monitor --stop-extras succeeded unexpectedly; stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "--stop-extras requires --plan") {
		t.Fatalf("stderr missing stop-extras/plan validation:\n%s", stderr.String())
	}
}

func TestMonitorActionRequiresPlan(t *testing.T) {
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--action", "start"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("monitor --action succeeded unexpectedly; stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "--action requires --plan") {
		t.Fatalf("stderr missing action/plan validation:\n%s", stderr.String())
	}
}

func TestMonitorRejectsUnknownAction(t *testing.T) {
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--plan", "--action", "pause"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("monitor --plan --action pause succeeded unexpectedly; stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "unknown --action") {
		t.Fatalf("stderr missing action validation:\n%s", stderr.String())
	}
}

func TestMonitorFormatRendersSnapshot(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"monitor",
		"--plan",
		"--instance", "manager",
		"--format", "{{.Health.Healthy}}:{{len .Instances}}:{{.Health.Declared.Missing}}:{{.Plan.Summary.Total}}:{{.StatsError}}",
		"--target", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("monitor --format: %v\nstderr: %s", err, stderr.String())
	}
	want := "false:0:1:1:\n"
	if got := stdout.String(); got != want {
		t.Fatalf("monitor --format output = %q, want %q", got, want)
	}
}

func TestMonitorFormatRejectsConflictingModes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "json",
			args: []string{"monitor", "--format", "{{.Health.Healthy}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "summary",
			args: []string{"monitor", "--format", "{{.Health.Healthy}}", "--summary"},
			want: "--format cannot be combined",
		},
		{
			name: "invalid-template",
			args: []string{"monitor", "--format", "{{"},
			want: "invalid --format template",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewRootCmd()
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(stdout)
			cmd.SetErr(stderr)
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected monitor --format validation failure, stdout=%s", stdout.String())
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tt.want)
			}
		})
	}
}

func TestCollectMonitorSnapshotFiltersInstanceRows(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	for _, name := range []string{"manager", "worker-1"} {
		stateDir := filepath.Join(teamDir, "state", name)
		if err := os.MkdirAll(stateDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stateDir, "status.toml"), []byte(`[status]
phase = "idle"
description = "waiting"
`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	psOpts, err := newPsOptions(nil, []string{"manager"}, nil, false)
	if err != nil {
		t.Fatalf("newPsOptions: %v", err)
	}
	statsOpts, err := newStatsOptions(false, nil, []string{"manager"})
	if err != nil {
		t.Fatalf("newStatsOptions: %v", err)
	}

	snapshot, err := collectMonitorSnapshot(teamDir, time.Now(), nil, monitorOptions{PS: psOpts, Stats: statsOpts})
	if err != nil {
		t.Fatalf("collectMonitorSnapshot: %v", err)
	}
	if len(snapshot.Instances) != 1 || snapshot.Instances[0].Instance != "manager" {
		t.Fatalf("instances = %+v, want only manager", snapshot.Instances)
	}
	if snapshot.Health == nil || snapshot.Health.Summary.Total != 1 {
		t.Fatalf("health should be filtered to the manager row: %+v", snapshot.Health)
	}
	if snapshot.StatsError != "" {
		t.Fatalf("stats_error = %q, want empty local fallback error", snapshot.StatsError)
	}
}

func TestCollectMonitorSnapshotFiltersRuntimeRows(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "codex-worker", Agent: "worker", Runtime: "codex", RuntimeBinary: "codex-dev", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "claude-manager", Agent: "manager", Runtime: "claude", RuntimeBinary: "claude-code", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(-3 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	opts, err := newMonitorOptionsWithRuntimeInstancesPhasesStaleAndUnhealthy(false, nil, []string{"codex"}, nil, nil, nil, false, false)
	if err != nil {
		t.Fatalf("newMonitorOptionsWithRuntimeInstancesPhasesStaleAndUnhealthy: %v", err)
	}

	snapshot, err := collectMonitorSnapshot(teamDir, now, func(pid int) (processStats, error) {
		return processStats{CPUPercent: 1.0, MemoryPercent: 0.5, RSSKiB: 1024}, nil
	}, opts)
	if err != nil {
		t.Fatalf("collect monitor with runtime filter: %v", err)
	}
	if len(snapshot.Instances) != 1 || snapshot.Instances[0].Instance != "codex-worker" || snapshot.Instances[0].Runtime != "codex" || snapshot.Instances[0].RuntimeBinary != "codex-dev" {
		t.Fatalf("instances = %+v, want codex worker only", snapshot.Instances)
	}
	if snapshot.Health == nil || snapshot.Health.Summary.Total != 1 {
		t.Fatalf("health should be filtered to the codex row: %+v", snapshot.Health)
	}
	if len(snapshot.Stats) != 1 || snapshot.Stats[0].Instance != "codex-worker" || snapshot.Stats[0].Runtime != "codex" || snapshot.Stats[0].RuntimeBinary != "codex-dev" {
		t.Fatalf("stats = %+v, want codex worker only", snapshot.Stats)
	}
}

func TestCollectMonitorSnapshotSortsInstanceRows(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, meta := range []*daemon.Metadata{
		{Instance: "a-stopped", Agent: "manager", Status: daemon.StatusStopped},
		{Instance: "b-running", Agent: "manager", Status: daemon.StatusRunning, PID: 42},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	snapshot, err := collectMonitorSnapshot(teamDir, time.Now(), nil, monitorOptions{PS: psOptions{Sort: psSortStatus}})
	if err != nil {
		t.Fatalf("collectMonitorSnapshot: %v", err)
	}
	if len(snapshot.Instances) != 2 {
		t.Fatalf("instances = %+v, want two rows", snapshot.Instances)
	}
	if got := snapshot.Instances[0].Instance; got != "b-running" {
		t.Fatalf("first instance = %q, want running row first under status sort; rows=%+v", got, snapshot.Instances)
	}
}

func TestCollectMonitorSnapshotSortsInstanceRowsByStarted(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "manager", Status: daemon.StatusRunning, PID: 42, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "manager", Status: daemon.StatusRunning, PID: 43, StartedAt: now.Add(-5 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	snapshot, err := collectMonitorSnapshot(teamDir, now, nil, monitorOptions{PS: psOptions{Sort: psSortStarted}})
	if err != nil {
		t.Fatalf("collectMonitorSnapshot: %v", err)
	}
	if len(snapshot.Instances) != 2 {
		t.Fatalf("instances = %+v, want two rows", snapshot.Instances)
	}
	if got := snapshot.Instances[0].Instance; got != "new" {
		t.Fatalf("first instance = %q, want newest started row first; rows=%+v", got, snapshot.Instances)
	}
}

func TestCollectMonitorSnapshotLimitsRowsByLatestStarted(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "manager", Status: daemon.StatusRunning, PID: 101, StartedAt: now.Add(-3 * time.Hour)},
		{Instance: "mid", Agent: "manager", Status: daemon.StatusRunning, PID: 202, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "manager", Status: daemon.StatusRunning, PID: 303, StartedAt: now.Add(-1 * time.Hour)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	probe := func(pid int) (processStats, error) {
		return processStats{CPUPercent: float64(pid), MemoryPercent: 0.1, RSSKiB: int64(pid)}, nil
	}

	snapshot, err := collectMonitorSnapshot(teamDir, now, probe, monitorOptions{
		PS:    psOptions{Limit: 2},
		Stats: statsOptions{Limit: 2},
	})
	if err != nil {
		t.Fatalf("collectMonitorSnapshot: %v", err)
	}
	if got, want := rowInstances(snapshot.Instances), []string{"new", "mid"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("instances = %v, want %v", got, want)
	}
	if got, want := statsRowInstances(snapshot.Stats), []string{"new", "mid"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("stats = %v, want %v", got, want)
	}
}

func TestCollectMonitorSnapshotSortsStatsRows(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, meta := range []*daemon.Metadata{
		{Instance: "a-low", Agent: "manager", Status: daemon.StatusRunning, PID: 101},
		{Instance: "b-high", Agent: "manager", Status: daemon.StatusRunning, PID: 202},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	probe := func(pid int) (processStats, error) {
		return processStats{CPUPercent: float64(pid), MemoryPercent: 0.1, RSSKiB: int64(pid)}, nil
	}

	snapshot, err := collectMonitorSnapshot(teamDir, time.Now(), probe, monitorOptions{Stats: statsOptions{Sort: statsSortCPU}})
	if err != nil {
		t.Fatalf("collectMonitorSnapshot: %v", err)
	}
	if len(snapshot.Stats) != 2 {
		t.Fatalf("stats = %+v, want two rows", snapshot.Stats)
	}
	if got := snapshot.Stats[0].Instance; got != "b-high" {
		t.Fatalf("first stats row = %q, want highest CPU first; rows=%+v", got, snapshot.Stats)
	}
}

func TestMonitorInstanceFilterJSONScopesPlanAndHealth(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--json", "--plan", "--instance", "manager", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("monitor --json --plan --instance: %v\nstderr: %s", err, stderr.String())
	}

	var body monitorSnapshot
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode monitor json: %v\nbody=%s", err, stdout.String())
	}
	if body.Health == nil || body.Health.Declared.Persistent != 1 || body.Health.Declared.Missing != 1 {
		t.Fatalf("health declared = %+v, want only missing manager declaration", body.Health)
	}
	if body.Plan == nil || len(body.Plan.Instances) != 1 || body.Plan.Instances[0].Instance != "manager" {
		t.Fatalf("plan rows = %+v, want manager only", body.Plan)
	}
	if len(body.Instances) != 0 {
		t.Fatalf("runtime instances = %+v, want none before start", body.Instances)
	}
}

func TestMonitorAllIncludesStoppedStats(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-monitor-all-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), fakeSpawnerForTest(t, 2*time.Second))
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()
	defer func() {
		for _, meta := range mgr.List() {
			if meta.Instance == "running" && meta.Status == daemon.StatusRunning {
				stopAndWaitForTest(t, mgr, "running")
				return
			}
		}
	}()

	if _, err := mgr.Dispatch(daemon.DispatchInput{Agent: "manager", Name: "running", Workspace: tmp}); err != nil {
		t.Fatalf("dispatch running: %v", err)
	}
	if _, err := mgr.Dispatch(daemon.DispatchInput{Agent: "manager", Name: "stopped", Workspace: tmp}); err != nil {
		t.Fatalf("dispatch stopped: %v", err)
	}
	stopAndWaitForTest(t, mgr, "stopped")

	probe := func(pid int) (processStats, error) {
		return processStats{CPUPercent: 1.0, MemoryPercent: 0.5, RSSKiB: 1024}, nil
	}
	defaultSnapshot, err := collectMonitorSnapshot(teamDir, time.Now(), probe, monitorOptions{})
	if err != nil {
		t.Fatalf("collect default monitor: %v", err)
	}
	if statsJSONContains(defaultSnapshot.Stats, "stopped") {
		t.Fatalf("default monitor stats should exclude stopped rows: %+v", defaultSnapshot.Stats)
	}

	opts, err := newMonitorOptions(true, nil, nil)
	if err != nil {
		t.Fatalf("newMonitorOptions: %v", err)
	}
	allSnapshot, err := collectMonitorSnapshot(teamDir, time.Now(), probe, opts)
	if err != nil {
		t.Fatalf("collect --all monitor: %v", err)
	}
	if !statsJSONContains(allSnapshot.Stats, "running") || !statsJSONContains(allSnapshot.Stats, "stopped") {
		t.Fatalf("--all monitor stats missing rows: %+v", allSnapshot.Stats)
	}
	for _, row := range allSnapshot.Stats {
		if row.Instance == "stopped" && row.CPUPercent != nil {
			t.Fatalf("stopped row should not include process metrics: %+v", row)
		}
	}

	instanceOpts, err := newMonitorOptionsWithInstances(true, nil, nil, []string{"stopped"})
	if err != nil {
		t.Fatalf("newMonitorOptionsWithInstances: %v", err)
	}
	filteredSnapshot, err := collectMonitorSnapshot(teamDir, time.Now(), probe, instanceOpts)
	if err != nil {
		t.Fatalf("collect --instance monitor: %v", err)
	}
	if len(filteredSnapshot.Stats) != 1 || filteredSnapshot.Stats[0].Instance != "stopped" {
		t.Fatalf("--instance monitor stats = %+v, want stopped only", filteredSnapshot.Stats)
	}
}

func TestMonitorEventsJSONUsesFilters(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-monitor-events-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), fakeSpawnerForTest(t, 2*time.Second))
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()
	defer func() {
		for _, meta := range mgr.List() {
			if meta.Status == daemon.StatusRunning {
				stopAndWaitForTest(t, mgr, meta.Instance)
			}
		}
	}()

	if _, err := mgr.Dispatch(daemon.DispatchInput{Agent: "manager", Name: "manager", Workspace: tmp}); err != nil {
		t.Fatalf("dispatch manager: %v", err)
	}
	if _, err := mgr.Dispatch(daemon.DispatchInput{Agent: "worker", Name: "worker", Workspace: tmp}); err != nil {
		t.Fatalf("dispatch worker: %v", err)
	}

	opts, err := newMonitorOptionsWithInstances(true, nil, []string{"manager"}, nil)
	if err != nil {
		t.Fatalf("newMonitorOptionsWithInstances: %v", err)
	}
	opts.EventTail = 10
	probe := func(pid int) (processStats, error) {
		return processStats{CPUPercent: 1.0, MemoryPercent: 0.5, RSSKiB: 1024}, nil
	}
	snapshot, err := collectMonitorSnapshot(teamDir, time.Now(), probe, opts)
	if err != nil {
		t.Fatalf("collect monitor with events: %v", err)
	}
	if snapshot.EventsError != "" {
		t.Fatalf("events_error = %q", snapshot.EventsError)
	}
	if len(snapshot.Events) != 1 || snapshot.Events[0].Instance != "manager" || snapshot.Events[0].Agent != "manager" {
		t.Fatalf("events = %+v, want only manager event", snapshot.Events)
	}
}

func TestMonitorEventsUseLocalLogWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, ev := range []daemon.LifecycleEvent{
		{
			TS:       time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC),
			Action:   "dispatch",
			Instance: "manager",
			Agent:    "manager",
			Status:   daemon.StatusRunning,
		},
		{
			TS:       time.Date(2026, 6, 17, 12, 1, 0, 0, time.UTC),
			Action:   "dispatch",
			Instance: "worker",
			Agent:    "worker",
			Status:   daemon.StatusRunning,
		},
	} {
		ev := ev
		if err := daemon.AppendLifecycleEvent(root, &ev); err != nil {
			t.Fatalf("append event: %v", err)
		}
	}

	opts, err := newMonitorOptionsWithInstances(true, nil, []string{"manager"}, nil)
	if err != nil {
		t.Fatalf("newMonitorOptionsWithInstances: %v", err)
	}
	opts.EventTail = 10
	snapshot, err := collectMonitorSnapshot(teamDir, time.Now(), nil, opts)
	if err != nil {
		t.Fatalf("collect monitor with local events: %v", err)
	}
	if snapshot.StatsError != "" {
		t.Fatalf("stats_error = %q, want empty local fallback error", snapshot.StatsError)
	}
	if snapshot.EventsError != "" {
		t.Fatalf("events_error = %q, want empty", snapshot.EventsError)
	}
	if len(snapshot.Events) != 1 || snapshot.Events[0].Instance != "manager" || snapshot.Events[0].Agent != "manager" {
		t.Fatalf("events = %+v, want only manager event", snapshot.Events)
	}
}

func TestMonitorEventsFilterByActionAndSince(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, ev := range []daemon.LifecycleEvent{
		{
			TS:       time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC),
			Action:   "stop",
			Instance: "old-stop",
			Agent:    "manager",
			Status:   daemon.StatusStopped,
		},
		{
			TS:       time.Date(2026, 6, 17, 12, 1, 0, 0, time.UTC),
			Action:   "dispatch",
			Instance: "new-dispatch",
			Agent:    "manager",
			Status:   daemon.StatusRunning,
		},
		{
			TS:       time.Date(2026, 6, 17, 12, 2, 0, 0, time.UTC),
			Action:   "stop",
			Instance: "new-stop",
			Agent:    "manager",
			Status:   daemon.StatusStopped,
		},
	} {
		ev := ev
		if err := daemon.AppendLifecycleEvent(root, &ev); err != nil {
			t.Fatalf("append event: %v", err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"monitor",
		"--events", "10",
		"--event-action", "stop",
		"--since", "2026-06-17T12:01:00Z",
		"--json",
		"--target", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("monitor event filters: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot monitorSnapshot
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode monitor json: %v\nbody=%s", err, out.String())
	}
	if snapshot.EventsError != "" {
		t.Fatalf("events_error = %q, want empty", snapshot.EventsError)
	}
	if len(snapshot.Events) != 1 || snapshot.Events[0].Instance != "new-stop" {
		t.Fatalf("events = %+v, want only recent stop", snapshot.Events)
	}
}

func TestMonitorEventsTailAppliesAfterFilters(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, ev := range []daemon.LifecycleEvent{
		{
			TS:       time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC),
			Action:   "dispatch",
			Instance: "manager-old",
			Agent:    "manager",
			Status:   daemon.StatusRunning,
		},
		{
			TS:       time.Date(2026, 6, 17, 12, 1, 0, 0, time.UTC),
			Action:   "dispatch",
			Instance: "worker-newer",
			Agent:    "worker",
			Status:   daemon.StatusRunning,
		},
		{
			TS:       time.Date(2026, 6, 17, 12, 2, 0, 0, time.UTC),
			Action:   "stop",
			Instance: "manager-new",
			Agent:    "manager",
			Status:   daemon.StatusStopped,
		},
		{
			TS:       time.Date(2026, 6, 17, 12, 3, 0, 0, time.UTC),
			Action:   "dispatch",
			Instance: "worker-newest",
			Agent:    "worker",
			Status:   daemon.StatusRunning,
		},
	} {
		ev := ev
		if err := daemon.AppendLifecycleEvent(root, &ev); err != nil {
			t.Fatalf("append event: %v", err)
		}
	}

	opts, err := newMonitorOptionsWithInstances(true, nil, []string{"manager"}, nil)
	if err != nil {
		t.Fatalf("newMonitorOptionsWithInstances: %v", err)
	}
	opts.EventTail = 1
	snapshot, err := collectMonitorSnapshot(teamDir, time.Now(), nil, opts)
	if err != nil {
		t.Fatalf("collect monitor with local events: %v", err)
	}
	if snapshot.EventsError != "" {
		t.Fatalf("events_error = %q, want empty", snapshot.EventsError)
	}
	if len(snapshot.Events) != 1 || snapshot.Events[0].Instance != "manager-new" {
		t.Fatalf("events = %+v, want last matching manager event", snapshot.Events)
	}

	opts.EventTail = 2
	opts.EventSort = "newest"
	snapshot, err = collectMonitorSnapshot(teamDir, time.Now(), nil, opts)
	if err != nil {
		t.Fatalf("collect monitor with newest events: %v", err)
	}
	if got := lifecycleEventInstances(snapshot.Events); strings.Join(got, ",") != "manager-new,manager-old" {
		t.Fatalf("newest events = %v, want manager-new,manager-old", got)
	}
}

func TestMonitorStatsUseLocalMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
		PID:      123,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	opts, err := newMonitorOptions(true, nil, nil)
	if err != nil {
		t.Fatalf("newMonitorOptions: %v", err)
	}
	snapshot, err := collectMonitorSnapshot(teamDir, time.Now(), nil, opts)
	if err != nil {
		t.Fatalf("collect monitor with local stats: %v", err)
	}
	if snapshot.StatsError != "" {
		t.Fatalf("stats_error = %q, want empty", snapshot.StatsError)
	}
	if len(snapshot.Stats) != 1 || snapshot.Stats[0].Instance != "manager" || snapshot.Stats[0].Status != "stopped" || snapshot.Stats[0].CPUPercent != nil {
		t.Fatalf("stats = %+v, want stopped manager without metrics", snapshot.Stats)
	}
}

func TestMonitorPhaseFilterScopesInstancesAndStats(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(-3 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `
[status]
phase = "idle"
description = "waiting"
`, now)
	writeStatus(t, filepath.Join(teamDir, "state", "worker"), `
[status]
phase = "blocked"
description = "needs input"
`, now)

	opts, err := newMonitorOptionsWithInstancesAndPhases(false, nil, nil, []string{"blocked"}, nil)
	if err != nil {
		t.Fatalf("newMonitorOptionsWithInstancesAndPhases: %v", err)
	}
	snapshot, err := collectMonitorSnapshot(teamDir, now, func(pid int) (processStats, error) {
		return processStats{CPUPercent: 1.0, MemoryPercent: 0.5, RSSKiB: 1024}, nil
	}, opts)
	if err != nil {
		t.Fatalf("collect monitor with phase filter: %v", err)
	}
	if len(snapshot.Instances) != 1 || snapshot.Instances[0].Instance != "worker" || snapshot.Instances[0].Phase != "blocked" {
		t.Fatalf("instances = %+v, want blocked worker only", snapshot.Instances)
	}
	if len(snapshot.Stats) != 1 || snapshot.Stats[0].Instance != "worker" || snapshot.Stats[0].Phase != "blocked" {
		t.Fatalf("stats = %+v, want blocked worker only", snapshot.Stats)
	}
}

func TestMonitorStaleFilterScopesInstancesAndStats(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusStopped, PID: 123, StartedAt: old},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusStopped, PID: 456, StartedAt: now},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "implementing"
description = "stale work"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "worker"), `[status]
phase = "implementing"
description = "fresh work"
`, now)

	opts, err := newMonitorOptionsWithInstancesPhasesAndStale(true, nil, nil, nil, nil, true)
	if err != nil {
		t.Fatalf("newMonitorOptionsWithInstancesPhasesAndStale: %v", err)
	}
	snapshot, err := collectMonitorSnapshot(teamDir, now, nil, opts)
	if err != nil {
		t.Fatalf("collect monitor with stale filter: %v", err)
	}
	if len(snapshot.Instances) != 1 || snapshot.Instances[0].Instance != "manager" || !snapshot.Instances[0].Stale {
		t.Fatalf("instances = %+v, want stale manager only", snapshot.Instances)
	}
	if len(snapshot.Stats) != 1 || snapshot.Stats[0].Instance != "manager" || snapshot.Stats[0].Phase != "implementing" {
		t.Fatalf("stats = %+v, want stale manager only", snapshot.Stats)
	}
}

func TestMonitorStaleFilterWithNoMatchesScopesStatsToNone(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now()
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:  "manager",
		Agent:     "manager",
		Status:    daemon.StatusStopped,
		PID:       123,
		StartedAt: now,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "implementing"
description = "fresh work"
`, now)

	opts, err := newMonitorOptionsWithInstancesPhasesAndStale(true, nil, nil, nil, nil, true)
	if err != nil {
		t.Fatalf("newMonitorOptionsWithInstancesPhasesAndStale: %v", err)
	}
	snapshot, err := collectMonitorSnapshot(teamDir, now, nil, opts)
	if err != nil {
		t.Fatalf("collect monitor with stale filter: %v", err)
	}
	if len(snapshot.Instances) != 0 {
		t.Fatalf("instances = %+v, want no stale rows", snapshot.Instances)
	}
	if len(snapshot.Stats) != 0 {
		t.Fatalf("stats = %+v, want no stale rows", snapshot.Stats)
	}
}

func TestMonitorUnhealthyFilterScopesInstancesAndStats(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "crashed", Agent: "worker", Status: daemon.StatusCrashed, PID: 123, StartedAt: old},
		{Instance: "fresh", Agent: "worker", Status: daemon.StatusStopped, PID: 456, StartedAt: now},
		{Instance: "stale", Agent: "manager", Status: daemon.StatusStopped, PID: 789, StartedAt: old},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "stale"), `[status]
phase = "implementing"
description = "stale work"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "fresh"), `[status]
phase = "idle"
description = "fresh work"
`, now)

	opts, err := newMonitorOptionsWithInstancesPhasesStaleAndUnhealthy(true, nil, nil, nil, nil, false, true)
	if err != nil {
		t.Fatalf("newMonitorOptionsWithInstancesPhasesStaleAndUnhealthy: %v", err)
	}
	snapshot, err := collectMonitorSnapshot(teamDir, now, nil, opts)
	if err != nil {
		t.Fatalf("collect monitor with unhealthy filter: %v", err)
	}
	if got := strings.Join(rowInstances(snapshot.Instances), ","); got != "crashed,stale" {
		t.Fatalf("instances = %+v, want crashed and stale rows", snapshot.Instances)
	}
	if got := strings.Join(statsRowInstances(snapshot.Stats), ","); got != "crashed,stale" {
		t.Fatalf("stats = %+v, want crashed and stale rows", snapshot.Stats)
	}
	if snapshot.Instances[0].Status != "crashed" || !snapshot.Instances[1].Stale {
		t.Fatalf("instances = %+v, want crashed row and stale row", snapshot.Instances)
	}
}

func statsJSONContains(rows []statsJSONRow, instance string) bool {
	for _, row := range rows {
		if row.Instance == instance {
			return true
		}
	}
	return false
}

func statsRowInstances(rows []statsJSONRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Instance)
	}
	return out
}

func TestRenderMonitorShowsHealthInstancesAndStats(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	instanceRows := []instanceRow{
		{Instance: "manager", Agent: "manager", Lifecycle: string(daemon.StatusRunning), Phase: "idle", Age: "5m", Summary: "waiting", PID: 10},
	}
	statsRows := []statsRow{
		{
			Instance:       "manager",
			Agent:          "manager",
			Status:         string(daemon.StatusRunning),
			PID:            10,
			Age:            "5m",
			CPUPercent:     2.5,
			MemoryPercent:  1.2,
			RSSKiB:         12_800,
			StatsAvailable: true,
		},
	}
	snapshot := &monitorSnapshot{
		Health:    buildHealth(true, 123, instanceRows, nil, now),
		Instances: psJSONRows(instanceRows),
		Stats:     statsJSONRows(statsRows),
		Events: []daemon.LifecycleEvent{{
			TS:       now,
			Action:   "dispatch",
			Instance: "manager",
			Agent:    "manager",
			Status:   daemon.StatusRunning,
			PID:      10,
		}},
		instanceRows: instanceRows,
		statsRows:    statsRows,
		eventRows: []daemon.LifecycleEvent{{
			TS:       now,
			Action:   "dispatch",
			Instance: "manager",
			Agent:    "manager",
			Status:   daemon.StatusRunning,
			PID:      10,
		}},
	}

	var buf bytes.Buffer
	if err := renderMonitor(&buf, snapshot); err != nil {
		t.Fatalf("renderMonitor: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"health: healthy", "instances:", "events:", "dispatch", "manager", "stats:", "2.5", "12.5MiB"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered monitor missing %q:\n%s", want, out)
		}
	}
}

func TestRenderMonitorIncludesPlanWhenPresent(t *testing.T) {
	snapshot := &monitorSnapshot{
		Health: buildHealth(false, 0, nil, nil, time.Now()),
		Plan: &planResult{
			Summary: planSummary{Total: 1, Start: 1},
			Instances: []planRow{{
				Instance: "manager",
				Agent:    "manager",
				Kind:     "persistent",
				Status:   "unknown",
				Action:   "start",
				Detail:   "declared persistent instance has no daemon metadata",
			}},
		},
		Stats:      []statsJSONRow{},
		statsEmpty: "(no running instances)",
	}

	var buf bytes.Buffer
	if err := renderMonitor(&buf, snapshot); err != nil {
		t.Fatalf("renderMonitor: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"plan:", "INSTANCE", "manager", "summary: total=1 start=1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered monitor missing %q:\n%s", want, out)
		}
	}
}

func TestMonitorWatchJSONEmitsSnapshots(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := tmp + "/.agent_team"

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runMonitorWatch(ctx, &buf, teamDir, time.Millisecond, time.Now, nil, true, monitorOptions{}, false); err != nil {
		t.Fatalf("runMonitorWatch json: %v", err)
	}
	body := strings.TrimSpace(buf.String())
	if body == "" {
		t.Fatalf("watch monitor json output empty")
	}
	first := strings.Split(body, "\n")[0]
	var snapshot monitorSnapshot
	if err := json.Unmarshal([]byte(first), &snapshot); err != nil {
		t.Fatalf("first monitor snapshot is not json: %v\nbody=%s", err, body)
	}
	if snapshot.Health == nil || snapshot.Health.Daemon.Running {
		t.Fatalf("snapshot should include daemon-down health: %+v", snapshot.Health)
	}
}

func TestMonitorWatchTextClearsWhenRequested(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := tmp + "/.agent_team"

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runMonitorWatch(ctx, &buf, teamDir, time.Millisecond, time.Now, nil, false, monitorOptions{}, true); err != nil {
		t.Fatalf("runMonitorWatch text clear: %v", err)
	}
	body := buf.String()
	if !strings.HasPrefix(body, watchClearSequence) {
		t.Fatalf("monitor watch should start with clear sequence, got %q", body[:min(len(body), len(watchClearSequence)+20)])
	}
	if !strings.Contains(body, "health: unhealthy") || !strings.Contains(body, "instances:") {
		t.Fatalf("monitor watch text missing snapshot content:\n%s", body)
	}
}

func TestMonitorFormatWatchEmitsRows(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := tmp + "/.agent_team"
	tmpl, err := parseMonitorFormat("{{.Health.Healthy}}:{{len .Instances}}:{{.StatsError}}")
	if err != nil {
		t.Fatalf("parseMonitorFormat: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runMonitorFormatWatch(ctx, &buf, teamDir, time.Millisecond, time.Now, nil, monitorOptions{}, tmpl); err != nil {
		t.Fatalf("runMonitorFormatWatch: %v", err)
	}
	first := strings.Split(strings.TrimSpace(buf.String()), "\n")[0]
	if first != "false:0:" {
		t.Fatalf("first monitor format watch row = %q, want daemon-down snapshot\nbody=%s", first, buf.String())
	}
	if strings.Contains(buf.String(), watchClearSequence) {
		t.Fatalf("monitor format watch should not emit clear sequence: %q", buf.String())
	}
}

func TestMonitorSummaryWatchTextClearsWhenRequested(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := tmp + "/.agent_team"

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runMonitorSummaryWatch(ctx, &buf, teamDir, time.Millisecond, time.Now, false, monitorOptions{}, true); err != nil {
		t.Fatalf("runMonitorSummaryWatch text clear: %v", err)
	}
	body := buf.String()
	if !strings.HasPrefix(body, watchClearSequence) {
		t.Fatalf("summary watch should start with clear sequence, got %q", body[:min(len(body), len(watchClearSequence)+20)])
	}
	if !strings.Contains(body, "health: unhealthy") {
		t.Fatalf("summary watch text missing health snapshot:\n%s", body)
	}
}

func TestMonitorNegativeIntervalFailsFast(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--interval", "-1s"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected interval validation error")
	}
	if !strings.Contains(stderr.String(), "--interval must be >= 0") {
		t.Fatalf("stderr = %q, want interval validation", stderr.String())
	}
}

func TestMonitorRejectsNegativeEvents(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--events", "-1"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected events validation error")
	}
	if !strings.Contains(stderr.String(), "--events must be >= 0") {
		t.Fatalf("stderr = %q, want events validation", stderr.String())
	}
}

func TestMonitorResourcesRequireSummary(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--resources"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected resources validation error")
	}
	if !strings.Contains(stderr.String(), "--resources requires --summary") {
		t.Fatalf("stderr = %q, want resources validation", stderr.String())
	}
}

func TestMonitorEventFiltersRequireEvents(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"monitor", "--since", "10m"}, want: "--since requires --events"},
		{args: []string{"monitor", "--event-action", "stop"}, want: "--event-action requires --events"},
		{args: []string{"monitor", "--events-sort", "newest"}, want: "--events-sort requires --events"},
		{args: []string{"monitor", "--events", "5", "--events-sort", "sideways"}, want: "--events-sort must be oldest or newest"},
		{args: []string{"watch", "--events-sort", "newest"}, want: "--events-sort requires --events"},
		{args: []string{"watch", "--events", "5", "--events-sort", "sideways"}, want: "--events-sort must be oldest or newest"},
		{args: []string{"monitor", "--events", "5", "--since", "recently"}, want: "--since must be a duration"},
		{args: []string{"monitor", "--events", "5", "--event-action", ","}, want: "--event-action requires at least one non-empty action"},
	} {
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

func TestMonitorSummaryEventsJSONIncludesEventSummary(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, ev := range []daemon.LifecycleEvent{
		{
			TS:       time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC),
			Action:   "dispatch",
			Instance: "manager",
			Agent:    "manager",
			Status:   daemon.StatusRunning,
		},
		{
			TS:       time.Date(2026, 6, 17, 12, 1, 0, 0, time.UTC),
			Action:   "dispatch",
			Instance: "worker",
			Agent:    "worker",
			Status:   daemon.StatusRunning,
		},
		{
			TS:       time.Date(2026, 6, 17, 12, 2, 0, 0, time.UTC),
			Action:   "stop",
			Instance: "manager",
			Agent:    "manager",
			Status:   daemon.StatusStopped,
		},
	} {
		ev := ev
		if err := daemon.AppendLifecycleEvent(root, &ev); err != nil {
			t.Fatalf("append event: %v", err)
		}
	}

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--summary", "--events", "5", "--event-action", "stop", "--since", "2026-06-17T12:01:00Z", "--json", "--agent", "manager", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("monitor --summary --events --json: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	var body monitorSummarySnapshot
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode monitor summary events json: %v\nbody=%s", err, stdout.String())
	}
	if body.Health == nil || body.Health.Healthy {
		t.Fatalf("health = %+v, want daemon-down health", body.Health)
	}
	if body.Plan != nil {
		t.Fatalf("plan summary should be omitted unless --plan is set: %+v", body.Plan)
	}
	if body.Events == nil {
		t.Fatalf("events summary missing: %+v", body)
	}
	if body.Events.Total != 1 || body.Events.Actions["stop"] != 1 || body.Events.Statuses["stopped"] != 1 || body.Events.Agents["manager"] != 1 || body.Events.Instances["manager"] != 1 {
		t.Fatalf("events summary = %+v, want one recent manager stop", body.Events)
	}
}

func TestMonitorRejectsUnknownStatus(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--status", "paused"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected status validation error")
	}
	if !strings.Contains(stderr.String(), "unknown --status") {
		t.Fatalf("stderr = %q, want status validation", stderr.String())
	}
}

func TestMonitorRejectsUnknownRuntime(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"monitor", "--runtime", "llama"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected runtime validation error")
	}
	if !strings.Contains(stderr.String(), "unknown --runtime") {
		t.Fatalf("stderr = %q, want runtime validation", stderr.String())
	}
}

func TestMonitorRejectsUnknownSorts(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"monitor", "--sort", "cpu"}, want: "unknown --sort"},
		{args: []string{"monitor", "--stats-sort", "latency"}, want: "unknown --stats-sort"},
	} {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected sort validation error", tc.args)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
	}
}
