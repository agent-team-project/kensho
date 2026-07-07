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

func TestPs_NoInstancesNoDaemon(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	var buf bytes.Buffer
	if err := runPs(&buf, filepath.Join(tmp, ".agent_team"), time.Now()); err != nil {
		t.Fatalf("runPs: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "(no instances)") {
		t.Errorf("output: got %q", got)
	}
}

func TestPs_OnDiskOnlyShowsPhaseFromStatusToml(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	stateDir := filepath.Join(teamDir, "state", "worker-1")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	statusBody := `[status]
phase = "implementing"
description = "porting tests"
since = "2026-04-29T10:00:00Z"

[work]
ticket = "SQU-29"
`
	if err := os.WriteFile(filepath.Join(stateDir, "status.toml"), []byte(statusBody), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := runPs(&buf, teamDir, time.Now()); err != nil {
		t.Fatalf("runPs: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "INSTANCE") || !strings.Contains(out, "STATUS") || !strings.Contains(out, "PID") {
		t.Errorf("missing header: %q", out)
	}
	if !strings.Contains(out, "worker-1") {
		t.Errorf("missing row: %q", out)
	}
	if !strings.Contains(out, "implementing") {
		t.Errorf("missing phase: %q", out)
	}
	if !strings.Contains(out, "porting tests") {
		t.Errorf("missing description: %q", out)
	}
	// No daemon/runtime metadata → STATUS and PID columns show placeholders.
	if !strings.Contains(out, "—") {
		t.Errorf("STATUS/PID columns should include `—` placeholders without a daemon: %q", out)
	}
}

func TestPsTextShowsPIDFromDaemonMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	started := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)
	deadline := started.Add(2 * time.Minute)
	now := started.Add(30 * time.Second)
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:        "manager",
		Agent:           "manager",
		Status:          daemon.StatusRunning,
		PID:             12345,
		StartedAt:       started,
		RuntimeBudget:   "2m0s",
		RuntimeDeadline: deadline,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	var buf bytes.Buffer
	if err := runPs(&buf, teamDir, now); err != nil {
		t.Fatalf("runPs: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "PID") || !strings.Contains(out, "12345") {
		t.Fatalf("ps output missing PID column/value: %q", out)
	}
	if !strings.Contains(out, "BUDGET") ||
		!strings.Contains(out, "budget=2m0s") ||
		!strings.Contains(out, "elapsed=30s") ||
		!strings.Contains(out, "remaining=1m30s") {
		t.Fatalf("ps output missing runtime budget details: %q", out)
	}
}

func TestPsJSON_OnDiskOnly(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	stateDir := filepath.Join(teamDir, "state", "worker-1")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	statusBody := `[status]
phase = "implementing"
description = "porting tests"
since = "2026-04-29T10:00:00Z"
`
	if err := os.WriteFile(filepath.Join(stateDir, "status.toml"), []byte(statusBody), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := runPsJSON(&buf, teamDir, time.Now()); err != nil {
		t.Fatalf("runPsJSON: %v", err)
	}
	var rows []psJSONRow
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("decode json: %v\nbody=%s", err, buf.String())
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one", rows)
	}
	got := rows[0]
	if got.Instance != "worker-1" || got.Phase != "implementing" || got.Summary != "porting tests" {
		t.Fatalf("row = %+v", got)
	}
	if got.Status != "unknown" {
		t.Fatalf("status without daemon = %q, want unknown", got.Status)
	}
	if !got.HasStatus {
		t.Fatalf("has_status should be true: %+v", got)
	}
}

func TestPsJSON_NormalizesUnknownStatusAndPhase(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.MkdirAll(filepath.Join(teamDir, "state", "empty-one"), 0o755); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := runPsJSON(&buf, teamDir, time.Now()); err != nil {
		t.Fatalf("runPsJSON: %v", err)
	}
	var rows []psJSONRow
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("decode json: %v\nbody=%s", err, buf.String())
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one", rows)
	}
	if rows[0].Status != "unknown" || rows[0].Phase != "unknown" {
		t.Fatalf("row = %+v, want semantic unknown status and phase", rows[0])
	}
}

func TestPsMergesLocalDaemonMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	started := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)
	deadline := started.Add(45 * time.Minute)
	now := started.Add(5 * time.Minute)
	lastActivity := started.Add(4 * time.Minute)
	logPath := filepath.Join(daemon.DaemonRoot(teamDir), "adhoc", "child.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("mkdir log dir: %v", err)
	}
	if err := os.WriteFile(logPath, []byte("working\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	if err := os.Chtimes(logPath, lastActivity, lastActivity); err != nil {
		t.Fatalf("chtimes log: %v", err)
	}
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:        "adhoc",
		Agent:           "manager",
		Status:          daemon.StatusStopped,
		PID:             123,
		StartedAt:       started,
		RuntimeBudget:   "45m0s",
		RuntimeDeadline: deadline,
		ResumeCount:     2,
		FreshFallback:   true,
		FreshFallbacks:  1,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	var buf bytes.Buffer
	if err := runPsJSON(&buf, teamDir, now); err != nil {
		t.Fatalf("runPsJSON: %v", err)
	}
	var rows []psJSONRow
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("decode json: %v\nbody=%s", err, buf.String())
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one daemon metadata row", rows)
	}
	got := rows[0]
	if got.Instance != "adhoc" || got.Agent != "manager" || got.Status != "stopped" || got.PID != 123 {
		t.Fatalf("row = %+v, want stopped manager metadata", got)
	}
	if got.RuntimeBudget != "45m0s" ||
		got.RuntimeDeadline != deadline.Format(time.RFC3339) ||
		got.RuntimeElapsed != "5m0s" ||
		got.RuntimeRemaining != "" {
		t.Fatalf("row = %+v, want persisted runtime budget metadata", got)
	}
	if got.ResumeCount != 2 || !got.FreshFallback || got.FreshFallbacks != 1 ||
		got.LastActivityAt != lastActivity.Format(time.RFC3339) ||
		got.Activity != "quiet 1m" {
		t.Fatalf("row = %+v, want resume/activity metadata", got)
	}
	if got.HasStatus {
		t.Fatalf("daemon-only row should not report status.toml: %+v", got)
	}
}

func TestPsTextWarnsWhenDaemonUnreachable(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:  "worker-squ-165",
		Agent:     "worker",
		Status:    daemon.StatusRunning,
		PID:       90535,
		StartedAt: time.Date(2026, 7, 7, 0, 40, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	if err := os.WriteFile(daemon.HTTPAddrPath(teamDir), []byte("127.0.0.1:1\n"), 0o644); err != nil {
		t.Fatalf("write stale http addr: %v", err)
	}

	var buf bytes.Buffer
	if err := runPs(&buf, teamDir, time.Date(2026, 7, 7, 0, 45, 0, 0, time.UTC)); err != nil {
		t.Fatalf("runPs: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"warning: daemon unreachable", "last-known", "not live", "worker-squ-165"} {
		if !strings.Contains(out, want) {
			t.Fatalf("ps output missing %q:\n%s", want, out)
		}
	}
}

func TestPsFreezesRuntimeBudgetElapsedForTerminalMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	started := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)
	exited := started.Add(8 * time.Minute)
	deadline := started.Add(30 * time.Minute)
	now := started.Add(time.Hour + 17*time.Minute)
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:        "reviewer-squ-75",
		Agent:           "worker",
		Status:          daemon.StatusExited,
		StartedAt:       started,
		ExitedAt:        exited,
		RuntimeBudget:   "30m0s",
		RuntimeDeadline: deadline,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	var jsonBuf bytes.Buffer
	if err := runPsJSON(&jsonBuf, teamDir, now); err != nil {
		t.Fatalf("runPsJSON: %v", err)
	}
	var rows []psJSONRow
	if err := json.Unmarshal(jsonBuf.Bytes(), &rows); err != nil {
		t.Fatalf("decode json: %v\nbody=%s", err, jsonBuf.String())
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one terminal daemon metadata row", rows)
	}
	if rows[0].RuntimeElapsed != "8m0s" || rows[0].RuntimeRemaining != "" {
		t.Fatalf("row = %+v, want elapsed frozen at exit and no remaining budget", rows[0])
	}

	var textBuf bytes.Buffer
	if err := runPs(&textBuf, teamDir, now); err != nil {
		t.Fatalf("runPs: %v", err)
	}
	text := textBuf.String()
	if !strings.Contains(text, "elapsed=8m0s") {
		t.Fatalf("ps output missing frozen elapsed:\n%s", text)
	}
	if strings.Contains(text, "remaining=") {
		t.Fatalf("ps output should omit remaining for terminal rows:\n%s", text)
	}
}

func TestPsFormatRendersRows(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	stateDir := filepath.Join(teamDir, "state", "worker-1")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "status.toml"), []byte(`[status]
phase = "implementing"
description = "porting tests"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	tmpl, err := parsePsFormat(`{{.Instance}} {{.Status}} {{.Phase}} {{.Summary}}`)
	if err != nil {
		t.Fatalf("parsePsFormat: %v", err)
	}

	var buf bytes.Buffer
	if err := runPsFormatWithOptions(&buf, teamDir, time.Now(), psOptions{}, tmpl); err != nil {
		t.Fatalf("runPsFormatWithOptions: %v", err)
	}
	if got, want := buf.String(), "worker-1 unknown implementing porting tests\n"; got != want {
		t.Fatalf("formatted ps = %q, want %q", got, want)
	}
}

func TestPsFormatRejectsInvalidTemplate(t *testing.T) {
	_, err := parsePsFormat(`{{.Instance`)
	if err == nil || !strings.Contains(err.Error(), "invalid --format template") {
		t.Fatalf("err = %v, want invalid template", err)
	}
}

func TestPsFormatRejectsConflictingOutputModes(t *testing.T) {
	for _, args := range [][]string{
		{"ps", "--format", "{{.Instance}}", "--json"},
		{"ps", "--format", "{{.Instance}}", "--quiet"},
		{"ps", "--format", "{{.Instance}}", "--summary"},
	} {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", args)
		}
		if !strings.Contains(stderr.String(), "--format cannot be combined") {
			t.Fatalf("%v: stderr = %q, want format conflict", args, stderr.String())
		}
	}
}

func TestPsAllFlagAcceptedForCompatibility(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	stateDir := filepath.Join(teamDir, "state", "worker-1")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "status.toml"), []byte(`[status]
phase = "idle"
description = "waiting"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"ps", "--all", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("ps --all: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "worker-1") || !strings.Contains(got, "waiting") {
		t.Fatalf("ps --all output missing visible instance: %q", got)
	}
}

func TestPsLastJSONShowsMostRecentlyStartedInstances(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	metas := []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-3 * time.Hour)},
		{Instance: "mid", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-1 * time.Hour)},
		{Instance: "missing", Agent: "worker", Status: daemon.StatusRunning},
	}
	for _, meta := range metas {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata: %v", err)
		}
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"ps", "--last", "2", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("ps --last --json: %v", err)
	}
	var rows []psJSONRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode json: %v\nbody=%s", err, out.String())
	}
	got := rowInstances(rows)
	want := []string{"new", "mid"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("instances = %v, want %v", got, want)
	}
}

func TestPsLatestJSONShowsMostRecentlyStartedInstance(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-5 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata: %v", err)
		}
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"ps", "--latest", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("ps --latest --json: %v", err)
	}
	var rows []psJSONRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "new" {
		t.Fatalf("rows = %+v, want newest instance only", rows)
	}
}

func TestPsLastHonorsExplicitSortAfterSelection(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "z-new", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "a-mid", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-30 * time.Minute)},
		{Instance: "m-old", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-2 * time.Hour)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata: %v", err)
		}
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"ps", "--last", "2", "--sort", "name", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("ps --last --sort name --json: %v", err)
	}
	var rows []psJSONRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode json: %v\nbody=%s", err, out.String())
	}
	got := rowInstances(rows)
	want := []string{"a-mid", "z-new"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("instances = %v, want %v", got, want)
	}
}

func TestPsLatestLastValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "negative last", args: []string{"ps", "--last", "-1"}, want: "--last must be >= 0"},
		{name: "latest and last", args: []string{"ps", "--latest", "--last", "2"}, want: "choose one of --latest or --last"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			stderr := &bytes.Buffer{}
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected validation error")
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestPsFiltersRowsByStatusAndAgent(t *testing.T) {
	opts, err := newPsOptions([]string{"running", "unknown"}, []string{"worker"}, nil, false)
	if err != nil {
		t.Fatalf("newPsOptions: %v", err)
	}
	rows := []instanceRow{
		{Instance: "manager", Agent: "manager", Lifecycle: "running"},
		{Instance: "worker-a", Agent: "worker", Lifecycle: "running"},
		{Instance: "worker-b", Agent: "worker", Lifecycle: "stopped"},
		{Instance: "worker-c", Agent: "worker"},
	}
	got := filterPsRows(rows, opts)
	if len(got) != 2 {
		t.Fatalf("rows = %+v, want two", got)
	}
	if got[0].Instance != "worker-a" || got[1].Instance != "worker-c" {
		t.Fatalf("rows = %+v, want worker-a and worker-c", got)
	}
}

func TestPsFiltersRowsByPhaseAndStale(t *testing.T) {
	opts, err := newPsOptions(nil, nil, []string{"blocked", "unknown"}, true)
	if err != nil {
		t.Fatalf("newPsOptions: %v", err)
	}
	rows := []instanceRow{
		{Instance: "manager", Phase: "blocked", Stale: true},
		{Instance: "worker-a", Phase: "implementing", Stale: true},
		{Instance: "worker-b", Phase: "—", Stale: true},
		{Instance: "worker-c", Phase: "blocked", Stale: false},
	}
	got := filterPsRows(rows, opts)
	if len(got) != 2 {
		t.Fatalf("rows = %+v, want two", got)
	}
	if got[0].Instance != "manager" || got[1].Instance != "worker-b" {
		t.Fatalf("rows = %+v, want blocked manager and unknown worker-b", got)
	}
}

func TestPsFiltersRowsByUnhealthy(t *testing.T) {
	opts, err := newPsOptionsWithInstancesAndUnhealthy(nil, nil, nil, nil, false, true)
	if err != nil {
		t.Fatalf("newPsOptionsWithInstancesAndUnhealthy: %v", err)
	}
	rows := []instanceRow{
		{Instance: "crashed", Agent: "worker", Lifecycle: string(daemon.StatusCrashed)},
		{Instance: "stale", Agent: "manager", Lifecycle: string(daemon.StatusRunning), Stale: true},
		{Instance: "healthy", Agent: "worker", Lifecycle: string(daemon.StatusRunning)},
		{Instance: "unknown", Agent: "worker"},
	}
	got := filterPsRows(rows, opts)
	if len(got) != 2 {
		t.Fatalf("rows = %+v, want crashed and stale", got)
	}
	if got[0].Instance != "crashed" || got[1].Instance != "stale" {
		t.Fatalf("rows = %+v, want crashed and stale", got)
	}
}

func TestPsUnhealthyJSONShowsCrashedAndStaleRows(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "crashed", Agent: "worker", Status: daemon.StatusCrashed, StartedAt: old},
		{Instance: "fresh", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now},
		{Instance: "stale", Agent: "manager", Status: daemon.StatusRunning, StartedAt: old},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "stale"), `[status]
phase = "implementing"
description = "stuck"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "fresh"), `[status]
phase = "idle"
description = "waiting"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"ps", "--unhealthy", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("ps --unhealthy --json: %v\nstderr=%s", err, stderr.String())
	}
	var rows []psJSONRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode ps unhealthy json: %v\nbody=%s", err, out.String())
	}
	if got := strings.Join(rowInstances(rows), ","); got != "crashed,stale" {
		t.Fatalf("instances = %v, want crashed,stale", rowInstances(rows))
	}
	if rows[0].Status != "crashed" || !rows[1].Stale {
		t.Fatalf("rows = %+v, want crashed row and stale row", rows)
	}
}

func TestPsUnhealthyJSONIncludesRuntimeStaleRows(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	for _, meta := range []*daemon.Metadata{
		{Instance: "fresh", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now},
		{Instance: "runtime-stale", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999999, StartedAt: now.Add(-time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"ps", "--unhealthy", "--summary", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("ps --unhealthy --summary --json: %v\nstderr=%s", err, stderr.String())
	}
	var summary psSummaryJSON
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("decode ps summary json: %v\nbody=%s", err, stdout.String())
	}
	if summary.Total != 1 || summary.RuntimeStale != 1 || summary.Unhealthy != 1 || summary.Stale != 0 {
		t.Fatalf("summary = %+v, want one runtime-stale unhealthy row", summary)
	}

	cmd = NewRootCmd()
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"ps", "--unhealthy", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("ps --unhealthy --json: %v\nstderr=%s", err, stderr.String())
	}
	var rows []psJSONRow
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("decode ps rows json: %v\nbody=%s", err, stdout.String())
	}
	if len(rows) != 1 || rows[0].Instance != "runtime-stale" || !rows[0].RuntimeStale || !rows[0].Unhealthy || rows[0].Stale {
		t.Fatalf("rows = %+v, want one runtime-stale unhealthy row", rows)
	}
}

func TestPsRuntimeStaleJSONFiltersOnlyRuntimeStaleRows(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "crashed", Agent: "worker", Runtime: "codex", Status: daemon.StatusCrashed, PID: 99999998, StartedAt: now.Add(-2 * time.Minute)},
		{Instance: "runtime-stale", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999999, StartedAt: now.Add(-time.Minute)},
		{Instance: "fresh", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "status-stale"), `[status]
phase = "implementing"
description = "stale status only"
`, old)

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"ps", "--runtime-stale", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("ps --runtime-stale --json: %v\nstderr=%s", err, stderr.String())
	}
	var rows []psJSONRow
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("decode ps runtime-stale json: %v\nbody=%s", err, stdout.String())
	}
	if len(rows) != 1 || rows[0].Instance != "runtime-stale" || !rows[0].RuntimeStale || !rows[0].Unhealthy || rows[0].Stale {
		t.Fatalf("rows = %+v, want one runtime-stale row only", rows)
	}
}

func TestPsFiltersRowsByInstanceAndCommaSeparatedValues(t *testing.T) {
	opts, err := newPsOptionsWithInstances([]string{"running, unknown"}, nil, nil, []string{"manager,worker-c"}, false)
	if err != nil {
		t.Fatalf("newPsOptionsWithInstances: %v", err)
	}
	rows := []instanceRow{
		{Instance: "manager", Agent: "manager", Lifecycle: "running"},
		{Instance: "ticket-manager", Agent: "ticket-manager", Lifecycle: "running"},
		{Instance: "worker-a", Agent: "worker", Lifecycle: "running"},
		{Instance: "worker-c", Agent: "worker"},
	}
	got := filterPsRows(rows, opts)
	if len(got) != 2 {
		t.Fatalf("rows = %+v, want two", got)
	}
	if got[0].Instance != "manager" || got[1].Instance != "worker-c" {
		t.Fatalf("rows = %+v, want manager and worker-c", got)
	}
}

func TestPsSortRowsByStatusThenName(t *testing.T) {
	rows := []instanceRow{
		{Instance: "worker-b", Agent: "worker", Lifecycle: string(daemon.StatusRunning)},
		{Instance: "manager", Agent: "manager", Lifecycle: string(daemon.StatusStopped)},
		{Instance: "worker-a", Agent: "worker", Lifecycle: string(daemon.StatusRunning)},
	}
	sortPsRows(rows, psSortStatus)
	got := []string{rows[0].Instance, rows[1].Instance, rows[2].Instance}
	want := []string{"worker-a", "worker-b", "manager"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("status sort = %v, want %v", got, want)
	}
}

func TestPsSortRowsByStaleFirst(t *testing.T) {
	rows := []instanceRow{
		{Instance: "manager", Agent: "manager"},
		{Instance: "worker-b", Agent: "worker", Stale: true},
		{Instance: "worker-a", Agent: "worker", Stale: true},
	}
	sortPsRows(rows, psSortStale)
	got := []string{rows[0].Instance, rows[1].Instance, rows[2].Instance}
	want := []string{"worker-a", "worker-b", "manager"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("stale sort = %v, want %v", got, want)
	}
}

func TestPsSortRowsByRuntimeStaleFirst(t *testing.T) {
	sortMode, err := parsePsSort("runtime-stale")
	if err != nil {
		t.Fatalf("parsePsSort runtime-stale: %v", err)
	}
	aliasMode, err := parsePsSort("runtime_stale")
	if err != nil {
		t.Fatalf("parsePsSort runtime_stale: %v", err)
	}
	if aliasMode != sortMode {
		t.Fatalf("runtime_stale alias = %q, want %q", aliasMode, sortMode)
	}
	rows := []instanceRow{
		{Instance: "fresh", Agent: "worker", Lifecycle: string(daemon.StatusRunning)},
		{Instance: "status-stale", Agent: "manager", Lifecycle: string(daemon.StatusRunning), Stale: true},
		{Instance: "runtime-b", Agent: "worker", Lifecycle: string(daemon.StatusRunning), RuntimeStale: true},
		{Instance: "runtime-a", Agent: "manager", Lifecycle: string(daemon.StatusRunning), RuntimeStale: true},
	}
	sortPsRows(rows, sortMode)
	got := []string{rows[0].Instance, rows[1].Instance, rows[2].Instance, rows[3].Instance}
	want := []string{"runtime-a", "runtime-b", "fresh", "status-stale"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("runtime-stale sort = %v, want %v", got, want)
	}
}

func TestPsSortRowsByUnhealthyFirst(t *testing.T) {
	sortMode, err := parsePsSort("unhealthy")
	if err != nil {
		t.Fatalf("parsePsSort unhealthy: %v", err)
	}
	rows := []instanceRow{
		{Instance: "healthy", Agent: "worker", Lifecycle: string(daemon.StatusRunning)},
		{Instance: "stale", Agent: "manager", Lifecycle: string(daemon.StatusRunning), Stale: true},
		{Instance: "crashed", Agent: "worker", Lifecycle: string(daemon.StatusCrashed)},
		{Instance: "unknown", Agent: "worker"},
	}
	sortPsRows(rows, sortMode)
	got := []string{rows[0].Instance, rows[1].Instance, rows[2].Instance, rows[3].Instance}
	want := []string{"crashed", "stale", "healthy", "unknown"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("unhealthy sort = %v, want %v", got, want)
	}
}

func TestPsSortRowsByStartedNewestFirst(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	rows := []instanceRow{
		{Instance: "old", Agent: "worker", StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "missing", Agent: "manager"},
		{Instance: "new", Agent: "manager", StartedAt: now.Add(-5 * time.Minute)},
	}
	sortPsRows(rows, psSortStarted)
	got := []string{rows[0].Instance, rows[1].Instance, rows[2].Instance}
	want := []string{"new", "old", "missing"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("started sort = %v, want %v", got, want)
	}
}

func TestLimitPsRowsByLatestStarted(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	rows := []instanceRow{
		{Instance: "old", Agent: "worker", StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "missing", Agent: "manager"},
		{Instance: "new", Agent: "manager", StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "mid", Agent: "worker", StartedAt: now.Add(-30 * time.Minute)},
	}
	gotRows := limitPsRowsByLatestStarted(rows, 2)
	got := []string{gotRows[0].Instance, gotRows[1].Instance}
	want := []string{"new", "mid"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("limited rows = %v, want %v", got, want)
	}
	if len(rows) != 4 {
		t.Fatalf("original rows mutated to len %d, want 4", len(rows))
	}
}

func TestPsSortRowsByExitedNewestFirst(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	rows := []instanceRow{
		{Instance: "old", Agent: "worker", ExitedAt: now.Add(-2 * time.Hour)},
		{Instance: "missing", Agent: "manager"},
		{Instance: "new", Agent: "manager", ExitedAt: now.Add(-5 * time.Minute)},
	}
	sortPsRows(rows, psSortExited)
	got := []string{rows[0].Instance, rows[1].Instance, rows[2].Instance}
	want := []string{"new", "old", "missing"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("exited sort = %v, want %v", got, want)
	}
}

func TestPsSortRejectsUnknownValue(t *testing.T) {
	_, err := parsePsSort("memory")
	if err == nil || !strings.Contains(err.Error(), "unknown --sort") {
		t.Fatalf("err = %v, want unknown --sort", err)
	}
}

func TestPsOptionsRejectUnknownStatus(t *testing.T) {
	_, err := newPsOptions([]string{"paused"}, nil, nil, false)
	if err == nil || !strings.Contains(err.Error(), "unknown --status") {
		t.Fatalf("err = %v, want unknown status", err)
	}
}

func TestPsOptionsRejectEmptyFilters(t *testing.T) {
	cases := []struct {
		name      string
		statuses  []string
		runtimes  []string
		agents    []string
		phases    []string
		instances []string
		want      string
	}{
		{name: "status", statuses: []string{"  "}, want: "non-empty status"},
		{name: "runtime", runtimes: []string{"  "}, want: "non-empty runtime"},
		{name: "agent", agents: []string{"  "}, want: "non-empty agent"},
		{name: "phase", phases: []string{"  "}, want: "non-empty phase"},
		{name: "instance", instances: []string{"  "}, want: "non-empty instance"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newPsOptionsWithRuntimeInstancesAndUnhealthy(tc.statuses, tc.runtimes, tc.agents, tc.phases, tc.instances, false, false)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestPsOptionsRejectUnknownPhase(t *testing.T) {
	_, err := newPsOptions(nil, nil, []string{"reviewing"}, false)
	if err == nil || !strings.Contains(err.Error(), "unknown --phase") {
		t.Fatalf("err = %v, want unknown phase", err)
	}
}

func TestPsOptionsRejectUnknownRuntime(t *testing.T) {
	_, err := newPsOptionsWithRuntimeInstancesAndUnhealthy(nil, []string{"llama"}, nil, nil, nil, false, false)
	if err == nil || !strings.Contains(err.Error(), "unknown --runtime") {
		t.Fatalf("err = %v, want unknown runtime", err)
	}
}

func TestPsFiltersRowsByRuntime(t *testing.T) {
	opts, err := newPsOptionsWithRuntimeInstancesAndUnhealthy(nil, []string{"codex"}, nil, nil, nil, false, false)
	if err != nil {
		t.Fatalf("newPsOptionsWithRuntimeInstancesAndUnhealthy: %v", err)
	}
	rows := []instanceRow{
		{Instance: "manager", Agent: "manager", Runtime: "claude"},
		{Instance: "worker-squ-42", Agent: "worker", Runtime: "codex"},
		{Instance: "legacy", Agent: "worker"},
	}
	got := filterPsRows(rows, opts)
	if len(got) != 1 || got[0].Instance != "worker-squ-42" {
		t.Fatalf("rows = %+v, want codex worker", got)
	}
}

func TestPsQuietPrintsMatchingInstanceNames(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.MkdirAll(filepath.Join(teamDir, "state", "worker-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(teamDir, "state", "manager"), 0o755); err != nil {
		t.Fatal(err)
	}
	opts, err := newPsOptions([]string{"unknown"}, []string{"worker"}, nil, false)
	if err != nil {
		t.Fatalf("newPsOptions: %v", err)
	}
	var buf bytes.Buffer
	if err := runPsQuiet(&buf, teamDir, time.Now(), opts); err != nil {
		t.Fatalf("runPsQuiet: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "worker-1" {
		t.Fatalf("quiet output = %q, want worker-1", got)
	}
}

func TestPsSummaryRowsCountsLifecycleStates(t *testing.T) {
	rows := []instanceRow{
		{Lifecycle: "running", Runtime: "codex", Phase: "blocked", Stale: true, HasFile: true},
		{Lifecycle: "stopped", Runtime: "claude", Phase: "idle"},
		{Lifecycle: "exited", Runtime: "codex", Phase: "done", HasFile: true},
		{Lifecycle: "crashed", Runtime: "claude", Phase: "implementing"},
		{},
	}
	got := psSummaryRows(rows)
	if got.Total != 5 || got.Running != 1 || got.Stopped != 1 || got.Exited != 1 || got.Crashed != 1 || got.Unknown != 1 {
		t.Fatalf("summary counts = %+v", got)
	}
	if got.Stale != 1 || got.HasStatus != 2 {
		t.Fatalf("summary metadata counts = %+v", got)
	}
	for phase, want := range map[string]int{
		"blocked":      1,
		"idle":         1,
		"done":         1,
		"implementing": 1,
		"unknown":      1,
	} {
		if got.Phases[phase] != want {
			t.Fatalf("phase %s = %d, want %d in %+v", phase, got.Phases[phase], want, got.Phases)
		}
	}
	for runtime, want := range map[string]int{
		"codex":   2,
		"claude":  2,
		"unknown": 1,
	} {
		if got.Runtimes[runtime] != want {
			t.Fatalf("runtime %s = %d, want %d in %+v", runtime, got.Runtimes[runtime], want, got.Runtimes)
		}
	}
}

func TestRenderPsSummaryIncludesPhaseCounts(t *testing.T) {
	var buf bytes.Buffer
	summary := psSummaryJSON{
		Total:    3,
		Phases:   map[string]int{"blocked": 1, "idle": 2},
		Runtimes: map[string]int{"codex": 2, "claude": 1},
	}
	if err := renderPsSummary(&buf, summary); err != nil {
		t.Fatalf("renderPsSummary: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"STATUS", "RUNTIME", "PHASE", "codex", "claude", "blocked", "idle"} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary output missing %q:\n%s", want, out)
		}
	}
}

func TestPsSummaryJSON_OnDiskOnly(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.MkdirAll(filepath.Join(teamDir, "state", "worker-1"), 0o755); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := runPsSummaryJSON(&buf, teamDir, time.Now(), psOptions{}); err != nil {
		t.Fatalf("runPsSummaryJSON: %v", err)
	}
	var body psSummaryJSON
	if err := json.Unmarshal(buf.Bytes(), &body); err != nil {
		t.Fatalf("decode summary json: %v\nbody=%s", err, buf.String())
	}
	if body.Total != 1 || body.Unknown != 1 {
		t.Fatalf("summary = %+v, want one unknown row", body)
	}
	if body.Phases["unknown"] != 1 {
		t.Fatalf("summary phases = %+v, want one unknown phase", body.Phases)
	}
}

func TestPsSummaryWatchJSONEmitsSnapshots(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runPsSummaryWatch(ctx, &buf, teamDir, time.Millisecond, time.Now, true, psOptions{}); err != nil {
		t.Fatalf("runPsSummaryWatch json: %v", err)
	}
	body := strings.TrimSpace(buf.String())
	if body == "" {
		t.Fatalf("watch summary json output empty")
	}
	first := strings.Split(body, "\n")[0]
	var summary psSummaryJSON
	if err := json.Unmarshal([]byte(first), &summary); err != nil {
		t.Fatalf("first summary snapshot is not json: %v\nbody=%s", err, body)
	}
}

func TestPsQuietAndSummaryConflict(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"ps", "--quiet", "--summary"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--quiet cannot be combined with --summary") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestPsNegativeIntervalFailsFast(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"ps", "--watch", "--interval", "-1s"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected interval validation error")
	}
	if !strings.Contains(stderr.String(), "--interval must be >= 0") {
		t.Fatalf("stderr = %q, want interval validation", stderr.String())
	}
}

func TestMergeDaemonRowsUpdatesAgentForExistingStateRow(t *testing.T) {
	started := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)
	stopped := time.Date(2026, 6, 17, 11, 0, 0, 0, time.UTC)
	exited := time.Date(2026, 6, 17, 11, 5, 0, 0, time.UTC)
	rows := []instanceRow{
		{Instance: "adhoc", Agent: "—", Phase: "—", Age: "—"},
	}
	metas := []*daemon.Metadata{
		{Instance: "adhoc", Agent: "manager", Status: daemon.StatusRunning, Runtime: "codex", RuntimeBinary: "codex-dev", PID: 42, StartedAt: started, StoppedAt: stopped, ExitedAt: exited},
	}
	got := mergeDaemonRows(t.TempDir(), rows, metas, map[string]bool{"manager": true}, started.Add(30*time.Minute))
	if got[0].Agent != "manager" || got[0].Lifecycle != "running" || got[0].Runtime != "codex" || got[0].RuntimeBinary != "codex-dev" || got[0].PID != 42 || !got[0].StartedAt.Equal(started) || !got[0].StoppedAt.Equal(stopped) || !got[0].ExitedAt.Equal(exited) {
		t.Fatalf("row = %+v, want daemon agent/status/pid", got[0])
	}
}

func TestPsJSONRowsIncludeLifecycleTimestamps(t *testing.T) {
	started := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)
	stopped := time.Date(2026, 6, 17, 11, 0, 0, 0, time.UTC)
	exited := time.Date(2026, 6, 17, 11, 5, 0, 0, time.UTC)
	rows := psJSONRows([]instanceRow{{
		Instance:      "manager",
		Agent:         "manager",
		Runtime:       "codex",
		RuntimeBinary: "codex-dev",
		StartedAt:     started,
		StoppedAt:     stopped,
		ExitedAt:      exited,
	}})
	if len(rows) != 1 ||
		rows[0].Runtime != "codex" ||
		rows[0].RuntimeBinary != "codex-dev" ||
		rows[0].StartedAt != started.Format(time.RFC3339) ||
		rows[0].StoppedAt != stopped.Format(time.RFC3339) ||
		rows[0].ExitedAt != exited.Format(time.RFC3339) {
		t.Fatalf("rows = %+v, want runtime metadata and RFC3339 lifecycle timestamps", rows)
	}
}

func TestMergeDaemonRowsNormalizesMissingStatus(t *testing.T) {
	rows := []instanceRow{
		{Instance: "stateful", Agent: "manager", Phase: "idle", Age: "1m"},
	}
	metas := []*daemon.Metadata{
		{Instance: "stateful", Agent: "manager", PID: 42},
		{Instance: "adhoc", Agent: "manager", PID: 43},
	}

	got := mergeDaemonRows(t.TempDir(), rows, metas, map[string]bool{"manager": true}, time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC))
	if len(got) != 2 {
		t.Fatalf("rows = %+v, want two", got)
	}
	for _, row := range got {
		if row.Lifecycle != "unknown" {
			t.Fatalf("row = %+v, want unknown lifecycle", row)
		}
	}
}

func TestPsWatchStopsOnContextCancel(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runPsWatch(ctx, &buf, teamDir, time.Millisecond, time.Now); err != nil {
		t.Fatalf("runPsWatch: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "(no instances)") {
		t.Errorf("watch output missing ps table: %q", got)
	}
}

func TestPsWatchTextClearsWhenRequested(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runPsWatchFiltered(ctx, &buf, teamDir, time.Millisecond, time.Now, false, psOptions{}, true); err != nil {
		t.Fatalf("runPsWatchFiltered clear: %v", err)
	}
	body := buf.String()
	if !strings.HasPrefix(body, watchClearSequence) {
		t.Fatalf("ps watch should start with clear sequence, got %q", body[:min(len(body), len(watchClearSequence)+20)])
	}
	if !strings.Contains(body, "(no instances)") {
		t.Fatalf("ps watch clear output missing table: %q", body)
	}
}

func TestPsWatchTextNoClearAppendsSnapshots(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runPsWatchFiltered(ctx, &buf, teamDir, time.Millisecond, time.Now, false, psOptions{}, false); err != nil {
		t.Fatalf("runPsWatchFiltered no clear: %v", err)
	}
	body := buf.String()
	if strings.Contains(body, watchClearSequence) {
		t.Fatalf("ps watch no-clear should not emit clear sequence: %q", body)
	}
	if !strings.Contains(body, "(no instances)") {
		t.Fatalf("ps watch no-clear output missing table: %q", body)
	}
}

func TestPsFormatWatchEmitsRowsWithoutClear(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	stateDir := filepath.Join(teamDir, "state", "worker-1")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "status.toml"), []byte(`[status]
phase = "idle"
description = "waiting"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	tmpl, err := parsePsFormat(`{{.Instance}}:{{.Phase}}`)
	if err != nil {
		t.Fatalf("parsePsFormat: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runPsFormatWatch(ctx, &buf, teamDir, time.Millisecond, time.Now, psOptions{}, tmpl); err != nil {
		t.Fatalf("runPsFormatWatch: %v", err)
	}
	first := strings.Split(strings.TrimSpace(buf.String()), "\n")[0]
	if first != "worker-1:idle" {
		t.Fatalf("first ps format watch row = %q, want worker-1:idle\nbody=%s", first, buf.String())
	}
	if strings.Contains(buf.String(), watchClearSequence) {
		t.Fatalf("ps format watch should not emit clear sequence: %q", buf.String())
	}
	if strings.Contains(buf.String(), "\n\n") {
		t.Fatalf("ps format watch should not insert blank snapshot separators: %q", buf.String())
	}
}

func TestPsWatchJSONEmitsSnapshots(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runPsWatchWithOptions(ctx, &buf, teamDir, time.Millisecond, time.Now, true); err != nil {
		t.Fatalf("runPsWatchWithOptions json: %v", err)
	}
	body := strings.TrimSpace(buf.String())
	if body == "" {
		t.Fatalf("watch json output empty")
	}
	first := strings.Split(body, "\n")[0]
	var rows []psJSONRow
	if err := json.Unmarshal([]byte(first), &rows); err != nil {
		t.Fatalf("first snapshot is not json: %v\nbody=%s", err, body)
	}
}

func rowInstances(rows []psJSONRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Instance)
	}
	return out
}
