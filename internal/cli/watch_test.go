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

	"github.com/agent-team-project/agent-team/internal/daemon"
)

func TestWatchCommandJSONEmitsMonitorSnapshots(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	writePlanShapeTopologyFixture(t, tmp)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetContext(ctx)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"watch", "--json", "--plan", "--agent", "manager", "--action", "start", "--interval", "1ms", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("watch --json: %v\nstderr: %s", err, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("watch output empty")
	}

	var snapshot monitorSnapshot
	if err := json.Unmarshal([]byte(lines[0]), &snapshot); err != nil {
		t.Fatalf("decode first watch snapshot: %v\nbody=%s", err, lines[0])
	}
	if snapshot.Health == nil || snapshot.Health.Daemon.Running {
		t.Fatalf("watch snapshot should include daemon-down health: %+v", snapshot.Health)
	}
	if snapshot.Plan == nil || snapshot.Plan.Summary.Total != 1 || snapshot.Plan.Summary.Start != 1 {
		t.Fatalf("watch snapshot should include filtered manager plan: %+v", snapshot.Plan)
	}
	if len(snapshot.Plan.Instances) != 1 || snapshot.Plan.Instances[0].Instance != "manager" {
		t.Fatalf("watch plan rows = %+v, want only manager", snapshot.Plan.Instances)
	}
}

func TestWatchJSONIncludesFilteredInboxSummary(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	for _, instance := range []string{"manager", "worker"} {
		if err := daemon.AppendMessage(daemon.DaemonRoot(teamDir), instance, &daemon.Message{
			ID:   "msg-watch-" + instance,
			From: "operator",
			Body: "watch inbox " + instance,
		}); err != nil {
			t.Fatalf("append message %s: %v", instance, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetContext(ctx)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"watch", "--json", "--instance", "manager", "--interval", "1ms", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("watch --json inbox: %v\nstderr: %s", err, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("watch inbox output empty")
	}

	var snapshot monitorSnapshot
	if err := json.Unmarshal([]byte(lines[0]), &snapshot); err != nil {
		t.Fatalf("decode first watch inbox snapshot: %v\nbody=%s", err, lines[0])
	}
	if snapshot.Inbox.Total != 1 || snapshot.Inbox.Unread != 1 || snapshot.Inbox.UnreadInstances != 1 || !stringSliceContains(snapshot.Inbox.UnreadNames, "manager") || stringSliceContains(snapshot.Inbox.UnreadNames, "worker") {
		t.Fatalf("watch inbox summary = %+v", snapshot.Inbox)
	}
	if strings.Contains(lines[0], "watch inbox manager") || strings.Contains(lines[0], "watch inbox worker") {
		t.Fatalf("watch json should not include inbox bodies:\n%s", lines[0])
	}
}

func TestWatchFallbacksRewriteRuntimeHealthActions(t *testing.T) {
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

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetContext(ctx)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"watch", "--json", "--fallbacks", "--interval", "1ms", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("watch --fallbacks --json: %v\nstderr=%s", err, stderr.String())
	}
	first := strings.Split(strings.TrimSpace(stdout.String()), "\n")[0]
	if first == "" {
		t.Fatalf("watch fallback output empty")
	}
	var snapshot monitorSnapshot
	if err := json.Unmarshal([]byte(first), &snapshot); err != nil {
		t.Fatalf("decode first watch fallback snapshot: %v\nbody=%s", err, first)
	}
	assertHealthIssueAction(t, snapshot.Health, "runtime_stale", "agent-team job resume-plan squ-88 --runtime-stale --commands --fallbacks")
}

func TestWatchSummaryJSONEmitsHealthSnapshots(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	writePlanShapeTopologyFixture(t, tmp)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetContext(ctx)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"watch", "--summary", "--json", "--agent", "manager", "--interval", "1ms", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("watch --summary --json: %v\nstderr: %s", err, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("watch summary output empty")
	}

	var snapshot healthResult
	if err := json.Unmarshal([]byte(lines[0]), &snapshot); err != nil {
		t.Fatalf("decode first watch summary snapshot: %v\nbody=%s", err, lines[0])
	}
	if snapshot.Healthy || snapshot.Daemon.Running {
		t.Fatalf("watch summary should include daemon-down health: %+v", snapshot)
	}
	if snapshot.Declared.Persistent != 1 || snapshot.Declared.Missing != 1 {
		t.Fatalf("manager-filtered declared counts = %+v, want one missing manager", snapshot.Declared)
	}
	if snapshot.Summary.Total != 0 {
		t.Fatalf("summary total = %d, want zero matching runtime rows", snapshot.Summary.Total)
	}
}

func TestWatchSummaryLatestJSONScopesHealthRows(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetContext(ctx)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"watch", "--summary", "--latest", "--json", "--interval", "1ms", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("watch --summary --latest --json: %v\nstderr: %s", err, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("watch summary latest output empty")
	}
	var snapshot healthResult
	if err := json.Unmarshal([]byte(lines[0]), &snapshot); err != nil {
		t.Fatalf("decode first watch summary latest snapshot: %v\nbody=%s", err, lines[0])
	}
	if snapshot.Summary.Total != 1 || snapshot.Summary.Stopped != 1 {
		t.Fatalf("summary = %+v, want one stopped latest row", snapshot.Summary)
	}
	if len(snapshot.Instances) != 1 || snapshot.Instances[0].Instance != "new" {
		t.Fatalf("instances = %+v, want only newest row", snapshot.Instances)
	}
}

func TestWatchEventsJSONEmitsMonitorSnapshots(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.AppendLifecycleEvent(daemon.DaemonRoot(teamDir), &daemon.LifecycleEvent{
		TS:       time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC),
		Action:   "dispatch",
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusRunning,
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetContext(ctx)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"watch", "--json", "--events", "5", "--interval", "1ms", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("watch --json --events: %v\nstderr: %s", err, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("watch events output empty")
	}

	var snapshot monitorSnapshot
	if err := json.Unmarshal([]byte(lines[0]), &snapshot); err != nil {
		t.Fatalf("decode first watch events snapshot: %v\nbody=%s", err, lines[0])
	}
	if snapshot.EventsError != "" {
		t.Fatalf("events_error = %q, want empty", snapshot.EventsError)
	}
	if len(snapshot.Events) != 1 || snapshot.Events[0].Instance != "manager" {
		t.Fatalf("events = %+v, want manager dispatch", snapshot.Events)
	}
}

func TestWatchPhaseFilterScopesInstancesAndStats(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetContext(ctx)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"watch", "--json", "--phase", "blocked", "--interval", "1ms", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("watch --phase blocked --json: %v\nstderr: %s", err, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("watch --phase output empty")
	}

	var snapshot monitorSnapshot
	if err := json.Unmarshal([]byte(lines[0]), &snapshot); err != nil {
		t.Fatalf("decode first watch phase snapshot: %v\nbody=%s", err, lines[0])
	}
	if len(snapshot.Instances) != 1 || snapshot.Instances[0].Instance != "worker" || snapshot.Instances[0].Phase != "blocked" {
		t.Fatalf("instances = %+v, want blocked worker only", snapshot.Instances)
	}
	if len(snapshot.Stats) != 1 || snapshot.Stats[0].Instance != "worker" || snapshot.Stats[0].Phase != "blocked" {
		t.Fatalf("stats = %+v, want blocked worker only", snapshot.Stats)
	}
}

func TestWatchStaleFilterScopesInstancesAndStats(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetContext(ctx)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"watch", "--stale", "--all", "--json", "--interval", "1ms", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("watch --stale --json: %v\nstderr: %s", err, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("watch --stale output empty")
	}

	var snapshot monitorSnapshot
	if err := json.Unmarshal([]byte(lines[0]), &snapshot); err != nil {
		t.Fatalf("decode first watch stale snapshot: %v\nbody=%s", err, lines[0])
	}
	if len(snapshot.Instances) != 1 || snapshot.Instances[0].Instance != "manager" || !snapshot.Instances[0].Stale {
		t.Fatalf("instances = %+v, want stale manager only", snapshot.Instances)
	}
	if len(snapshot.Stats) != 1 || snapshot.Stats[0].Instance != "manager" || snapshot.Stats[0].Phase != "implementing" {
		t.Fatalf("stats = %+v, want stale manager only", snapshot.Stats)
	}
}

func TestWatchUnhealthyFilterScopesInstancesAndStats(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetContext(ctx)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"watch", "--unhealthy", "--all", "--json", "--interval", "1ms", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("watch --unhealthy --json: %v\nstderr: %s", err, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("watch --unhealthy output empty")
	}

	var snapshot monitorSnapshot
	if err := json.Unmarshal([]byte(lines[0]), &snapshot); err != nil {
		t.Fatalf("decode first watch unhealthy snapshot: %v\nbody=%s", err, lines[0])
	}
	if got := strings.Join(rowInstances(snapshot.Instances), ","); got != "crashed,stale" {
		t.Fatalf("instances = %+v, want crashed and stale rows", snapshot.Instances)
	}
	if got := strings.Join(statsRowInstances(snapshot.Stats), ","); got != "crashed,stale" {
		t.Fatalf("stats = %+v, want crashed and stale rows", snapshot.Stats)
	}
}

func TestWatchCommandFormatEmitsMonitorRows(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetContext(ctx)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"watch", "--format", "{{.Health.Healthy}}:{{len .Instances}}:{{.StatsError}}", "--interval", "1ms", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("watch --format: %v\nstderr: %s", err, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("watch --format output empty")
	}
	if lines[0] != "false:0:" {
		t.Fatalf("first watch --format row = %q, want daemon-down snapshot\nbody=%s", lines[0], stdout.String())
	}
	if strings.Contains(stdout.String(), watchClearSequence) {
		t.Fatalf("watch --format should not emit clear sequence, stdout=%q", stdout.String())
	}
}

func TestWatchCommandTextClearsByDefault(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetContext(ctx)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"watch", "--interval", "1ms", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("watch text: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), watchClearSequence) {
		t.Fatalf("watch text should redraw by default, stdout=%q", stdout.String())
	}
}

func TestWatchCommandNoClearAppendsTextSnapshots(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetContext(ctx)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"watch", "--no-clear", "--interval", "1ms", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("watch --no-clear: %v\nstderr: %s", err, stderr.String())
	}
	if strings.Contains(stdout.String(), watchClearSequence) {
		t.Fatalf("watch --no-clear should not emit clear sequence, stdout=%q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "health: unhealthy") {
		t.Fatalf("watch --no-clear missing monitor content, stdout=%q", stdout.String())
	}
}

func TestWatchFormatRejectsConflictingModes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "json",
			args: []string{"watch", "--format", "{{.Health.Healthy}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "summary",
			args: []string{"watch", "--format", "{{.Health.Healthy}}", "--summary"},
			want: "--format cannot be combined",
		},
		{
			name: "invalid-template",
			args: []string{"watch", "--format", "{{"},
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
				t.Fatalf("expected watch --format validation failure, stdout=%s", stdout.String())
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tt.want)
			}
		})
	}
}

func TestWatchSummaryPlanJSONIncludesPlanSummary(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	writePlanShapeTopologyFixture(t, tmp)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetContext(ctx)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"watch", "--summary", "--plan", "--json", "--agent", "manager", "--interval", "1ms", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("watch --summary --plan --json: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("watch summary plan output empty")
	}
	var snapshot monitorSummarySnapshot
	if err := json.Unmarshal([]byte(lines[0]), &snapshot); err != nil {
		t.Fatalf("decode first watch summary plan snapshot: %v\nbody=%s", err, lines[0])
	}
	if snapshot.Health == nil || snapshot.Health.Healthy {
		t.Fatalf("health = %+v, want daemon-down health", snapshot.Health)
	}
	if snapshot.Plan == nil || snapshot.Plan.Summary.Total != 3 || snapshot.Plan.Summary.Actions["start"] != 1 || snapshot.Plan.Summary.Actions["on-demand"] != 2 {
		t.Fatalf("plan summary = %+v, want one manager start", snapshot.Plan)
	}
}

func TestWatchRejectsInvalidLatestLastOptions(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "negative-last",
			args: []string{"watch", "--last", "-1"},
			want: "--last must be >= 0",
		},
		{
			name: "latest-and-last",
			args: []string{"watch", "--latest", "--last", "2"},
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

func TestWatchStopExtrasRequiresPlan(t *testing.T) {
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"watch", "--stop-extras"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("watch --stop-extras succeeded unexpectedly; stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "--stop-extras requires --plan") {
		t.Fatalf("stderr missing stop-extras/plan validation:\n%s", stderr.String())
	}
}

func TestWatchActionRequiresPlan(t *testing.T) {
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"watch", "--action", "start"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("watch --action succeeded unexpectedly; stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "--action requires --plan") {
		t.Fatalf("stderr missing action/plan validation:\n%s", stderr.String())
	}
}

func TestWatchRejectsUnknownAction(t *testing.T) {
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"watch", "--plan", "--action", "pause"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("watch --plan --action pause succeeded unexpectedly; stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "unknown --action") {
		t.Fatalf("stderr missing action validation:\n%s", stderr.String())
	}
}

func TestWatchSummaryEventsJSONIncludesEventSummary(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.AppendLifecycleEvent(daemon.DaemonRoot(teamDir), &daemon.LifecycleEvent{
		TS:       time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC),
		Action:   "dispatch",
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusRunning,
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}
	if err := daemon.AppendLifecycleEvent(daemon.DaemonRoot(teamDir), &daemon.LifecycleEvent{
		TS:       time.Date(2026, 6, 17, 12, 1, 0, 0, time.UTC),
		Action:   "stop",
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetContext(ctx)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"watch", "--summary", "--events", "5", "--event-action", "stop", "--since", "2026-06-17T12:00:30Z", "--json", "--interval", "1ms", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("watch --summary --events --json: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("watch summary events output empty")
	}
	var snapshot monitorSummarySnapshot
	if err := json.Unmarshal([]byte(lines[0]), &snapshot); err != nil {
		t.Fatalf("decode first watch summary events snapshot: %v\nbody=%s", err, lines[0])
	}
	if snapshot.Health == nil || snapshot.Health.Healthy {
		t.Fatalf("health = %+v, want daemon-down health", snapshot.Health)
	}
	if snapshot.Events == nil {
		t.Fatalf("events summary missing: %+v", snapshot)
	}
	if snapshot.Events.Total != 1 || snapshot.Events.Actions["stop"] != 1 || snapshot.Events.Statuses["stopped"] != 1 || snapshot.Events.Instances["manager"] != 1 {
		t.Fatalf("events summary = %+v, want one recent manager stop", snapshot.Events)
	}
}

func TestWatchSummaryResourcesJSONIncludesStatsSummary(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetContext(ctx)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"watch", "--summary", "--resources", "--all", "--json", "--interval", "1ms", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("watch --summary --resources --json: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("watch summary resources output empty")
	}
	var snapshot monitorSummarySnapshot
	if err := json.Unmarshal([]byte(lines[0]), &snapshot); err != nil {
		t.Fatalf("decode first watch summary resources snapshot: %v\nbody=%s", err, lines[0])
	}
	if snapshot.Health == nil {
		t.Fatalf("health missing: %+v", snapshot)
	}
	if snapshot.Resources == nil {
		t.Fatalf("resources summary missing: %+v", snapshot)
	}
	if snapshot.Resources.Total != 1 || snapshot.Resources.Stopped != 1 || snapshot.Resources.Measured != 0 || snapshot.Resources.Phases["done"] != 1 {
		t.Fatalf("resources summary = %+v, want one stopped done manager", snapshot.Resources)
	}
}

func TestWatchRejectsNegativeEvents(t *testing.T) {
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"watch", "--events", "-1"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("watch --events -1 succeeded unexpectedly; stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "--events must be >= 0") {
		t.Fatalf("stderr missing invalid events message:\n%s", stderr.String())
	}
}

func TestWatchResourcesRequireSummary(t *testing.T) {
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"watch", "--resources"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected resources validation error; stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "--resources requires --summary") {
		t.Fatalf("stderr = %q, want resources validation", stderr.String())
	}
}

func TestWatchEventFiltersRequireEvents(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"watch", "--since", "10m"}, want: "--since requires --events"},
		{args: []string{"watch", "--event-action", "stop"}, want: "--event-action requires --events"},
		{args: []string{"watch", "--events", "5", "--since", "recently"}, want: "--since must be a duration"},
		{args: []string{"watch", "--events", "5", "--event-action", ","}, want: "--event-action requires at least one non-empty action"},
	} {
		cmd := NewRootCmd()
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(stdout)
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error; stdout=%s", tc.args, stdout.String())
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
	}
}

func TestWatchRejectsInvalidFilters(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"watch", "--status", "paused"}, want: `agent-team watch: unknown --status "paused"`},
		{args: []string{"watch", "--runtime", "llama"}, want: `agent-team watch: unknown --runtime "llama"`},
		{args: []string{"watch", "--phase", "reviewing"}, want: `agent-team watch: unknown --phase "reviewing"`},
	} {
		cmd := NewRootCmd()
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(stdout)
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("%v: expected validation error; stdout=%s stderr=%s", tc.args, stdout.String(), stderr.String())
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
	}
}

func TestWatchRejectsUnknownSorts(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"watch", "--sort", "cpu"}, want: "unknown --sort"},
		{args: []string{"watch", "--stats-sort", "latency"}, want: "unknown --stats-sort"},
	} {
		cmd := NewRootCmd()
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(stdout)
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected sort validation error; stdout=%s", tc.args, stdout.String())
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
	}
}
