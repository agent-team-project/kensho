package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/topology"
)

func TestInstanceLs_Empty(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"instance", "ls", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("instance ls: %v", err)
	}
	if !strings.Contains(out.String(), "(no instances)") {
		t.Errorf("expected (no instances), got: %s", out.String())
	}
}

func TestInstanceLs_ListsCreatedDirs(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	for _, n := range []string{"manager", "worker-squ-99"} {
		if err := os.MkdirAll(filepath.Join(teamDir, "state", n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"instance", "ls", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("instance ls: %v", err)
	}
	got := strings.TrimSpace(out.String())
	want := "manager\nworker-squ-99"
	if got != want {
		t.Errorf("instance ls output = %q, want %q", got, want)
	}
}

func TestInstanceShow_PrintsFiles(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	stateDir := filepath.Join(tmp, ".agent_team", "state", "manager")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "journal.md"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"instance", "show", "manager", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("instance show: %v", err)
	}
	o := out.String()
	if !strings.Contains(o, "instance: manager") {
		t.Errorf("missing header: %s", o)
	}
	if !strings.Contains(o, "journal.md  (11 bytes)") {
		t.Errorf("missing journal entry: %s", o)
	}
	if !strings.Contains(o, "subdir/  (dir)") {
		t.Errorf("missing subdir entry: %s", o)
	}
}

func TestInspectAlias_PrintsFiles(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	stateDir := filepath.Join(tmp, ".agent_team", "state", "manager")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "journal.md"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"inspect", "manager", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	o := out.String()
	if !strings.Contains(o, "instance: manager") {
		t.Errorf("missing header: %s", o)
	}
	if !strings.Contains(o, "journal.md  (11 bytes)") {
		t.Errorf("missing journal entry: %s", o)
	}
}

func TestPrintRuntimeMetadata_PrintsDaemonFields(t *testing.T) {
	tmp := t.TempDir()
	teamDir := filepath.Join(tmp, ".agent_team")
	started := time.Date(2026, 6, 17, 12, 30, 0, 0, time.UTC)
	deadline := started.Add(45 * time.Minute)
	meta := &daemon.Metadata{
		Instance:        "adhoc",
		Agent:           "manager",
		Status:          daemon.StatusRunning,
		Runtime:         "codex",
		RuntimeBinary:   "codex-dev",
		PID:             12345,
		Workspace:       tmp,
		SessionID:       "session-1",
		StartedAt:       started,
		RuntimeBudget:   "45m0s",
		RuntimeDeadline: deadline,
		LogPath:         filepath.Join(teamDir, "daemon", "adhoc", "child.log"),
	}

	var out bytes.Buffer
	printRuntimeMetadata(&out, inspectRuntimeJSONFromMeta(teamDir, meta))
	body := out.String()
	for _, want := range []string{
		"runtime:",
		"lifecycle:   running",
		"agent:       manager",
		"runtime:     codex",
		"binary:      codex-dev",
		"pid:         12345",
		"workspace:   " + filepath.ToSlash(tmp),
		"session_id:  session-1",
		"started_at:  2026-06-17T12:30:00Z",
		"budget:      45m0s",
		"deadline:    2026-06-17T13:15:00Z",
		"log:         .agent_team/daemon/adhoc/child.log",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("runtime output missing %q:\n%s", want, body)
		}
	}
}

func TestInspectRuntimeJSONFromMetaNormalizesMissingStatus(t *testing.T) {
	tmp := t.TempDir()
	teamDir := filepath.Join(tmp, ".agent_team")
	meta := &daemon.Metadata{
		Instance: "adhoc",
		Agent:    "manager",
	}

	got := inspectRuntimeJSONFromMeta(teamDir, meta)
	if got == nil || got.Lifecycle != "unknown" {
		t.Fatalf("runtime = %+v, want unknown lifecycle", got)
	}
}

func TestInspectUsesLocalDaemonMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	started := time.Date(2026, 6, 17, 12, 30, 0, 0, time.UTC)
	deadline := started.Add(45 * time.Minute)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance:        "adhoc",
		Agent:           "manager",
		Status:          daemon.StatusStopped,
		Runtime:         "codex",
		RuntimeBinary:   "codex-dev",
		Workspace:       tmp,
		SessionID:       "session-1",
		StartedAt:       started,
		RuntimeBudget:   "45m0s",
		RuntimeDeadline: deadline,
		Adopted:         true,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"inspect", "adhoc", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("inspect local metadata: %v\nstderr=%s", err, stderr.String())
	}
	var body inspectJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode inspect json: %v\nbody=%s", err, out.String())
	}
	if body.State.Exists {
		t.Fatalf("state should be missing for daemon-only metadata: %+v", body.State)
	}
	if body.Runtime == nil ||
		body.Runtime.Lifecycle != "stopped" ||
		body.Runtime.Agent != "manager" ||
		body.Runtime.Runtime != "codex" ||
		body.Runtime.RuntimeBinary != "codex-dev" ||
		body.Runtime.SessionID != "session-1" ||
		body.Runtime.RuntimeBudget != "45m0s" ||
		body.Runtime.RuntimeDeadline != deadline.Format(time.RFC3339) ||
		!body.Runtime.Adopted {
		t.Fatalf("runtime = %+v, want stopped manager session", body.Runtime)
	}
	if body.Runtime.LogPath != ".agent_team/daemon/adhoc/child.log" {
		t.Fatalf("runtime log path = %q", body.Runtime.LogPath)
	}
}

func TestInspectJSON_PrintsStructuredData(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	stateDir := filepath.Join(tmp, ".agent_team", "state", "manager")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "journal.md"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeStatus(t, stateDir, `
[status]
phase = "idle"
description = "Waiting"
since = "2026-06-17T12:00:00Z"
`, time.Now())

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"inspect", "manager", "--target", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("inspect --json: %v", err)
	}

	var body inspectJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode inspect json: %v\nbody=%s", err, out.String())
	}
	if body.Instance != "manager" {
		t.Fatalf("instance = %q", body.Instance)
	}
	if !body.State.Exists || body.State.Path != ".agent_team/state/manager" {
		t.Fatalf("state = %+v", body.State)
	}
	if body.Status == nil || body.Status.Phase != "idle" || body.Status.Description != "Waiting" {
		t.Fatalf("status = %+v", body.Status)
	}
	if body.Topology == nil || body.Topology.Agent != "manager" {
		t.Fatalf("topology = %+v", body.Topology)
	}
	if len(body.Files) == 0 {
		t.Fatalf("files should not be empty: %+v", body.Files)
	}
}

func TestInspectFormatPrintsStructuredFields(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	stateDir := filepath.Join(tmp, ".agent_team", "state", "manager")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeStatus(t, stateDir, `
[status]
phase = "idle"
description = "Waiting"
`, time.Now())

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"inspect", "manager", "--format", "{{.Instance}}:{{.State.Path}}:{{.Status.Phase}}:{{.Topology.Agent}}", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("inspect --format: %v", err)
	}
	want := "manager:.agent_team/state/manager:idle:manager\n"
	if out.String() != want {
		t.Fatalf("formatted inspect = %q, want %q", out.String(), want)
	}
}

func TestInspectFormatRejectsJSONAndInvalidTemplate(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"inspect", "manager", "--format", "{{.Instance}}", "--json"}, "--format cannot be combined with --json"},
		{[]string{"inspect", "manager", "--format", "{{"}, "invalid --format template"},
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

func TestInspectJSONMultipleEmitsArray(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	for _, name := range []string{"manager", "worker-1"} {
		stateDir := filepath.Join(tmp, ".agent_team", "state", name)
		if err := os.MkdirAll(stateDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"inspect", "manager", "worker-1", "--target", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("inspect multiple --json: %v", err)
	}
	var body []inspectJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode inspect json array: %v\nbody=%s", err, out.String())
	}
	if len(body) != 2 || body[0].Instance != "manager" || body[1].Instance != "worker-1" {
		t.Fatalf("body = %+v", body)
	}
}

func TestInspectAllJSONUsesVisibleInstances(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	for _, name := range []string{"manager", "worker-1"} {
		stateDir := filepath.Join(tmp, ".agent_team", "state", name)
		if err := os.MkdirAll(stateDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"inspect", "--all", "--target", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("inspect --all --json: %v", err)
	}
	var body []inspectJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode inspect all json: %v\nbody=%s", err, out.String())
	}
	var names []string
	for _, info := range body {
		names = append(names, info.Instance)
	}
	if strings.Join(names, ",") != "manager,worker-1" {
		t.Fatalf("instances = %v, want manager,worker-1", names)
	}
}

func TestInspectLatestJSONUsesNewestVisibleInstance(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "manager", Status: daemon.StatusStopped, StartedAt: now.Add(-5 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"inspect", "--latest", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("inspect --latest: %v\nstderr=%s", err, stderr.String())
	}
	var rows []inspectJSON
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode inspect json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "new" {
		t.Fatalf("rows = %+v, want newest visible instance", rows)
	}
}

func TestInspectLastJSONUsesNewestVisibleInstances(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-3 * time.Hour)},
		{Instance: "mid", Agent: "manager", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "manager", Status: daemon.StatusRunning, StartedAt: now.Add(-1 * time.Hour)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"inspect", "--last", "2", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("inspect --last 2: %v\nstderr=%s", err, stderr.String())
	}
	var rows []inspectJSON
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode inspect json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 2 || rows[0].Instance != "new" || rows[1].Instance != "mid" {
		t.Fatalf("rows = %+v, want newest two visible instances", rows)
	}
}

func TestInspectLatestHonorsFilters(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old-running", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new-stopped", Agent: "manager", Status: daemon.StatusStopped, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "newer-running", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-30 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"inspect", "--latest", "--status", "running", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("inspect --latest --status: %v\nstderr=%s", err, stderr.String())
	}
	var rows []inspectJSON
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode inspect json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "newer-running" {
		t.Fatalf("rows = %+v, want newest running instance", rows)
	}
}

func TestInspectFilteredJSONUsesVisibleInstances(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	managerDir := filepath.Join(tmp, ".agent_team", "state", "manager")
	workerDir := filepath.Join(tmp, ".agent_team", "state", "worker-1")
	if err := os.MkdirAll(managerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeStatus(t, managerDir, `
[status]
phase = "idle"
description = "Waiting"
`, time.Now())
	writeStatus(t, workerDir, `
[status]
phase = "blocked"
description = "Blocked"
`, time.Now())

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"inspect", "--agent", "manager", "--phase", "idle", "--target", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("inspect filtered --json: %v", err)
	}
	var body []inspectJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode filtered inspect json: %v\nbody=%s", err, out.String())
	}
	if len(body) != 1 || body[0].Instance != "manager" {
		t.Fatalf("body = %+v, want manager only", body)
	}
	if body[0].Status == nil || body[0].Status.Phase != "idle" {
		t.Fatalf("status = %+v, want idle", body[0].Status)
	}
}

func TestInspectRuntimeFilterUsesVisibleInstances(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "codex-worker", Agent: "worker", Runtime: "codex", RuntimeBinary: "codex-dev", Status: daemon.StatusRunning, PID: 10, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "claude-manager", Agent: "manager", Runtime: "claude", RuntimeBinary: "claude-code", Status: daemon.StatusRunning, PID: 11, StartedAt: now.Add(-3 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"inspect", "--runtime", "codex", "--target", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("inspect --runtime --json: %v\nstderr=%s", err, stderr.String())
	}
	var body []inspectJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode runtime inspect json: %v\nbody=%s", err, out.String())
	}
	if len(body) != 1 || body[0].Instance != "codex-worker" {
		t.Fatalf("body = %+v, want codex worker only", body)
	}
	if body[0].Runtime == nil || body[0].Runtime.Runtime != "codex" || body[0].Runtime.RuntimeBinary != "codex-dev" {
		t.Fatalf("runtime = %+v, want codex metadata", body[0].Runtime)
	}
}

func TestInspectUnhealthyJSONUsesVisibleInstances(t *testing.T) {
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
	writeStatus(t, filepath.Join(teamDir, "state", "stale"), `
[status]
phase = "implementing"
description = "stuck"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "fresh"), `
[status]
phase = "idle"
description = "fresh"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"inspect", "--unhealthy", "--target", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("inspect --unhealthy --json: %v\nstderr=%s", err, stderr.String())
	}
	var body []inspectJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode unhealthy inspect json: %v\nbody=%s", err, out.String())
	}
	if len(body) != 2 || body[0].Instance != "crashed" || body[1].Instance != "stale" {
		t.Fatalf("body = %+v, want crashed and stale only", body)
	}
	if body[0].Runtime == nil || body[0].Runtime.Lifecycle != string(daemon.StatusCrashed) {
		t.Fatalf("crashed runtime = %+v, want crashed", body[0].Runtime)
	}
	if body[1].Status == nil || body[1].Status.Phase != "implementing" {
		t.Fatalf("stale status = %+v, want implementing status", body[1].Status)
	}
}

func TestInspectAllRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"inspect", "--all", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--all cannot be combined") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestInspectLatestRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"inspect", "manager", "--latest"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--latest cannot be combined with instance names") {
		t.Fatalf("stderr = %q, want latest/name validation", stderr.String())
	}
}

func TestInspectRejectsInvalidLatestLastOptions(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "negative-last",
			args: []string{"inspect", "--last", "-1"},
			want: "--last must be >= 0",
		},
		{
			name: "latest-and-last",
			args: []string{"inspect", "--latest", "--last", "2"},
			want: "choose one of --latest or --last",
		},
		{
			name: "last-and-name",
			args: []string{"inspect", "manager", "--last", "2"},
			want: "--last cannot be combined with instance names",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewRootCmd()
			stderr := &bytes.Buffer{}
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(stderr)
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected validation error")
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tt.want)
			}
		})
	}
}

func TestInspectFiltersRejectExplicitNames(t *testing.T) {
	for _, args := range [][]string{
		{"inspect", "manager", "--agent", "manager"},
		{"inspect", "manager", "--runtime", "codex"},
		{"inspect", "manager", "--unhealthy"},
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
		if !strings.Contains(stderr.String(), "filters cannot be combined") {
			t.Fatalf("%v: stderr = %q", args, stderr.String())
		}
	}
}

func TestInspectFiltersRejectUnknownRuntime(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"inspect", "--runtime", "llama"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "unknown --runtime") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestInspectFiltersRejectEmptyAgent(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"inspect", "--agent", "  "})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "non-empty agent") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestInspectRequiresNamesUnlessAll(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"inspect"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "instance is required unless --all, --latest, --last, or a filter") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestInstanceShow_NotFound(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"instance", "show", "ghost", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing instance")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

func TestSelectDownTargets_AllIncludesAdhoc(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	running := map[string]bool{
		"adhoc":          true,
		"manager":        true,
		"ticket-manager": true,
		"worker":         true,
	}
	metas := []*daemon.Metadata{
		{Instance: "adhoc", Agent: "manager", Status: daemon.StatusRunning},
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning},
		{Instance: "ticket-manager", Agent: "ticket-manager", Status: daemon.StatusRunning},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusRunning},
	}

	defaultTargets, err := selectDownTargets(teamDir, running, metas, nil, false, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("selectDownTargets default: %v", err)
	}
	if strings.Join(defaultTargets, ",") != "manager,ticket-manager" {
		t.Fatalf("default targets = %v, want persistent declarations only", defaultTargets)
	}

	allTargets, err := selectDownTargets(teamDir, running, metas, nil, true, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("selectDownTargets all: %v", err)
	}
	if strings.Join(allTargets, ",") != "adhoc,manager,ticket-manager,worker" {
		t.Fatalf("all targets = %v, want every running daemon instance", allTargets)
	}
}

func TestSelectDownTargets_AgentFilter(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	metas := []*daemon.Metadata{
		{Instance: "adhoc", Agent: "manager", Status: daemon.StatusRunning},
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning},
		{Instance: "worker-stopped", Agent: "worker", Status: daemon.StatusStopped},
		{Instance: "worker-running", Agent: "worker", Status: daemon.StatusRunning},
	}
	running := runningInstanceSetFromMetas(metas)

	targets, err := selectDownTargets(teamDir, running, metas, nil, false, []string{"manager"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("selectDownTargets agent: %v", err)
	}
	if strings.Join(targets, ",") != "adhoc,manager" {
		t.Fatalf("manager targets = %v, want adhoc,manager", targets)
	}

	targets, err = selectDownTargets(teamDir, running, metas, nil, true, []string{"worker"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("selectDownTargets all+agent: %v", err)
	}
	if strings.Join(targets, ",") != "worker-running" {
		t.Fatalf("worker targets = %v, want worker-running", targets)
	}

	if _, err := selectDownTargets(teamDir, running, metas, []string{"manager"}, false, []string{"manager"}, nil, nil, nil); err == nil {
		t.Fatalf("expected explicit names plus agent filter to fail")
	}

	_, err = selectDownTargets(teamDir, running, metas, nil, false, []string{"  "}, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "non-empty agent") {
		t.Fatalf("err = %v, want non-empty agent validation", err)
	}
}

func TestSelectDownTargets_StatusFilter(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	metas := []*daemon.Metadata{
		{Instance: "manager-running", Agent: "manager", Status: daemon.StatusRunning},
		{Instance: "manager-stopped", Agent: "manager", Status: daemon.StatusStopped},
		{Instance: "worker-running", Agent: "worker", Status: daemon.StatusRunning},
		{Instance: "worker-stopped", Agent: "worker", Status: daemon.StatusStopped},
		{Instance: "ghost", Agent: "worker"},
	}
	running := runningInstanceSetFromMetas(metas)
	statuses, err := lifecycleStatusFilterSet([]string{"stopped,unknown"})
	if err != nil {
		t.Fatalf("lifecycleStatusFilterSet: %v", err)
	}

	targets, err := selectDownTargets(teamDir, running, metas, nil, false, nil, statuses, nil, nil)
	if err != nil {
		t.Fatalf("selectDownTargets status: %v", err)
	}
	if strings.Join(targets, ",") != "ghost,manager-stopped,worker-stopped" {
		t.Fatalf("status targets = %v, want stopped/unknown targets", targets)
	}

	targets, err = selectDownTargets(teamDir, running, metas, nil, true, []string{"worker"}, statuses, nil, nil)
	if err != nil {
		t.Fatalf("selectDownTargets status+agent: %v", err)
	}
	if strings.Join(targets, ",") != "ghost,worker-stopped" {
		t.Fatalf("status+agent targets = %v, want ghost,worker-stopped", targets)
	}

	_, err = selectDownTargets(teamDir, running, metas, []string{"manager"}, false, nil, statuses, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("err = %v, want status/name validation", err)
	}

	_, err = selectDownTargets(teamDir, running, metas, nil, false, []string{"  "}, statuses, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "non-empty agent") {
		t.Fatalf("err = %v, want non-empty agent validation", err)
	}
}

func TestSelectDownTargets_PhaseFilter(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	metas := []*daemon.Metadata{
		{Instance: "manager-blocked", Agent: "manager", Status: daemon.StatusRunning},
		{Instance: "manager-idle", Agent: "manager", Status: daemon.StatusRunning},
		{Instance: "worker-blocked", Agent: "worker", Status: daemon.StatusRunning},
		{Instance: "worker-stopped", Agent: "worker", Status: daemon.StatusStopped},
	}
	running := runningInstanceSetFromMetas(metas)
	phases, err := lifecyclePhaseFilterSet([]string{"blocked"})
	if err != nil {
		t.Fatalf("lifecyclePhaseFilterSet: %v", err)
	}
	phaseByInstance := map[string]string{
		"manager-blocked": "blocked",
		"manager-idle":    "idle",
		"worker-blocked":  "blocked",
		"worker-stopped":  "blocked",
	}

	targets, err := selectDownTargets(teamDir, running, metas, nil, false, []string{"manager"}, nil, phases, phaseByInstance)
	if err != nil {
		t.Fatalf("selectDownTargets phase: %v", err)
	}
	if strings.Join(targets, ",") != "manager-blocked" {
		t.Fatalf("phase targets = %v, want manager-blocked", targets)
	}

	statuses, err := lifecycleStatusFilterSet([]string{"stopped"})
	if err != nil {
		t.Fatalf("lifecycleStatusFilterSet: %v", err)
	}
	targets, err = selectDownTargets(teamDir, running, metas, nil, false, nil, statuses, phases, phaseByInstance)
	if err != nil {
		t.Fatalf("selectDownTargets status+phase: %v", err)
	}
	if strings.Join(targets, ",") != "worker-stopped" {
		t.Fatalf("status+phase targets = %v, want worker-stopped", targets)
	}

	_, err = selectDownTargets(teamDir, running, metas, []string{"manager"}, false, nil, nil, phases, phaseByInstance)
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("err = %v, want phase/name validation", err)
	}
}

func TestSelectLifecycleTargets_ExplicitDaemonKnownAdhoc(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	topo, err := topology.LoadFromTeamDir(filepath.Join(tmp, ".agent_team"))
	if err != nil {
		t.Fatalf("load topology: %v", err)
	}
	metas := []*daemon.Metadata{
		{Instance: "adhoc", Agent: "manager", Status: daemon.StatusStopped},
	}

	targets, err := selectLifecycleTargets(topo, metas, []string{"adhoc"})
	if err != nil {
		t.Fatalf("select explicit adhoc: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("targets = %+v, want one", targets)
	}
	if targets[0].name != "adhoc" || targets[0].agent != "manager" || targets[0].meta == nil {
		t.Fatalf("target = %+v, want daemon-backed adhoc manager", targets[0])
	}
}

func TestSelectLifecycleTargets_DefaultIgnoresAdhocMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	topo, err := topology.LoadFromTeamDir(filepath.Join(tmp, ".agent_team"))
	if err != nil {
		t.Fatalf("load topology: %v", err)
	}
	metas := []*daemon.Metadata{
		{Instance: "adhoc", Agent: "manager", Status: daemon.StatusStopped},
	}

	targets, err := selectLifecycleTargets(topo, metas, nil)
	if err != nil {
		t.Fatalf("select default: %v", err)
	}
	var names []string
	for _, target := range targets {
		names = append(names, target.name)
	}
	if strings.Join(names, ",") != "manager,ticket-manager" {
		t.Fatalf("default targets = %v, want declared persistent only", names)
	}
}

func TestSelectAllLifecycleTargetsIncludesDaemonKnownExtras(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	topo, err := topology.LoadFromTeamDir(filepath.Join(tmp, ".agent_team"))
	if err != nil {
		t.Fatalf("load topology: %v", err)
	}
	metas := []*daemon.Metadata{
		{Instance: "adhoc-b", Agent: "manager", Status: daemon.StatusStopped},
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning},
		{Instance: "adhoc-a", Agent: "worker", Status: daemon.StatusExited},
	}

	targets, err := selectAllLifecycleTargets(topo, metas)
	if err != nil {
		t.Fatalf("select all: %v", err)
	}
	var names []string
	for _, target := range targets {
		names = append(names, target.name)
	}
	if strings.Join(names, ",") != "manager,ticket-manager,adhoc-a,adhoc-b" {
		t.Fatalf("all lifecycle targets = %v", names)
	}
	if !targets[0].running() {
		t.Fatalf("manager target should carry running daemon metadata: %+v", targets[0])
	}
}

func TestSelectAgentLifecycleTargetsIncludesDeclaredAndDaemonKnown(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	topo, err := topology.LoadFromTeamDir(filepath.Join(tmp, ".agent_team"))
	if err != nil {
		t.Fatalf("load topology: %v", err)
	}
	metas := []*daemon.Metadata{
		{Instance: "adhoc-b", Agent: "manager", Status: daemon.StatusStopped},
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning},
		{Instance: "adhoc-a", Agent: "worker", Status: daemon.StatusExited},
	}

	targets, err := selectAgentLifecycleTargets(topo, metas, []string{"manager"})
	if err != nil {
		t.Fatalf("select manager targets: %v", err)
	}
	var names []string
	for _, target := range targets {
		names = append(names, target.name)
	}
	if strings.Join(names, ",") != "manager,adhoc-b" {
		t.Fatalf("manager lifecycle targets = %v, want declared manager then daemon-known adhoc-b", names)
	}
	if !targets[0].running() {
		t.Fatalf("declared manager target should carry running metadata: %+v", targets[0])
	}

	targets, err = selectAgentLifecycleTargets(topo, metas, []string{"worker"})
	if err != nil {
		t.Fatalf("select worker targets: %v", err)
	}
	names = names[:0]
	for _, target := range targets {
		names = append(names, target.name)
	}
	if strings.Join(names, ",") != "adhoc-a" {
		t.Fatalf("worker lifecycle targets = %v, want daemon-known worker only because declared worker is ephemeral", names)
	}
}

func TestSelectAgentLifecycleTargetsUnknownAgent(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	topo, err := topology.LoadFromTeamDir(filepath.Join(tmp, ".agent_team"))
	if err != nil {
		t.Fatalf("load topology: %v", err)
	}

	_, err = selectAgentLifecycleTargets(topo, nil, []string{"ghost"})
	if err == nil || !strings.Contains(err.Error(), "match --agent") {
		t.Fatalf("expected no-match agent error, got %v", err)
	}

	_, err = selectAgentLifecycleTargets(topo, nil, []string{"  "})
	if err == nil || !strings.Contains(err.Error(), "non-empty agent") {
		t.Fatalf("expected empty agent error, got %v", err)
	}
}

func TestSelectAgentLifecycleTargetsWithoutTopologyUsesDaemonKnown(t *testing.T) {
	metas := []*daemon.Metadata{
		{Instance: "adhoc-b", Agent: "manager", Status: daemon.StatusStopped},
		{Instance: "adhoc-a", Agent: "worker", Status: daemon.StatusExited},
	}

	targets, err := selectAgentLifecycleTargets(nil, metas, []string{"manager"})
	if err != nil {
		t.Fatalf("select manager targets without topology: %v", err)
	}
	if len(targets) != 1 || targets[0].name != "adhoc-b" || targets[0].agent != "manager" || targets[0].meta == nil {
		t.Fatalf("targets = %+v, want daemon-known adhoc-b manager", targets)
	}
}

func TestLifecycleStatusFilterSet(t *testing.T) {
	statuses, err := lifecycleStatusFilterSet([]string{"running, stopped", "unknown"})
	if err != nil {
		t.Fatalf("lifecycleStatusFilterSet: %v", err)
	}
	for _, want := range []string{"running", "stopped", "unknown"} {
		if !statuses[want] {
			t.Fatalf("statuses = %v, missing %q", statuses, want)
		}
	}

	if _, err := lifecycleStatusFilterSet([]string{"paused"}); err == nil || !strings.Contains(err.Error(), "unknown --status") {
		t.Fatalf("err = %v, want unknown status", err)
	}
	if _, err := lifecycleStatusFilterSet([]string{" , "}); err == nil || !strings.Contains(err.Error(), "non-empty status") {
		t.Fatalf("err = %v, want non-empty status", err)
	}
}

func TestFilterLifecycleTargetsByStatus(t *testing.T) {
	targets := []lifecycleTarget{
		{name: "declared", agent: "manager"},
		{name: "running", agent: "manager", meta: &daemon.Metadata{Status: daemon.StatusRunning}},
		{name: "stopped", agent: "manager", meta: &daemon.Metadata{Status: daemon.StatusStopped}},
	}
	filtered := filterLifecycleTargetsByStatus(targets, map[string]bool{
		"stopped": true,
		"unknown": true,
	})
	var names []string
	for _, target := range filtered {
		names = append(names, target.name)
	}
	if strings.Join(names, ",") != "declared,stopped" {
		t.Fatalf("filtered = %v, want declared,stopped", names)
	}
}

func TestFilterLifecycleTargetsByPhase(t *testing.T) {
	targets := []lifecycleTarget{
		{name: "blocked", agent: "manager"},
		{name: "idle", agent: "manager"},
		{name: "unknown", agent: "manager"},
	}
	filtered := filterLifecycleTargetsByPhase(targets, map[string]bool{
		"blocked": true,
		"unknown": true,
	}, map[string]string{
		"blocked": "blocked",
		"idle":    "idle",
	})
	var names []string
	for _, target := range filtered {
		names = append(names, target.name)
	}
	if strings.Join(names, ",") != "blocked,unknown" {
		t.Fatalf("filtered = %v, want blocked,unknown", names)
	}
}

func TestRunInstanceUpAllRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "--all", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --all plus name validation error")
	}
	if !strings.Contains(stderr.String(), "--all cannot be combined") {
		t.Fatalf("stderr = %q, want --all validation", stderr.String())
	}
}

func TestRunInstanceUpAgentRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "--agent", "manager", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --agent plus name validation error")
	}
	if !strings.Contains(stderr.String(), "--agent cannot be combined") {
		t.Fatalf("stderr = %q, want --agent validation", stderr.String())
	}
}

func TestRunInstanceUpStatusRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "--status", "stopped", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --status plus name validation error")
	}
	if !strings.Contains(stderr.String(), "--status cannot be combined") {
		t.Fatalf("stderr = %q, want --status validation", stderr.String())
	}
}

func TestInstanceUpStatusRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"instance", "up", "--status", "stopped", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --status plus name validation error")
	}
	if !strings.Contains(stderr.String(), "--status cannot be combined") {
		t.Fatalf("stderr = %q, want --status validation", stderr.String())
	}
}

func TestInstanceUpLastDryRunSelectsNewestStoppedMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-3 * time.Hour)},
		{Instance: "mid", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-1 * time.Hour)},
		{Instance: "running-newer", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-30 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"instance", "up", "--last", "2", "--status", "stopped", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("instance up --last dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var rows []lifecycleActionResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode dry-run json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 2 || rows[0].Instance != "new" || rows[1].Instance != "mid" {
		t.Fatalf("rows = %+v, want newest two stopped metadata resumes", rows)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"instance", "up", "--last", "2", "--status", "stopped", "--dry-run", "--commands", "--target", tmp})
	if err := commands.Execute(); err != nil {
		t.Fatalf("instance up --dry-run --commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{
		"agent-team",
		"instance",
		"up",
		"--repo",
		tmp,
		"--last",
		"2",
		"--status",
		"stopped",
	}), " ")
	if got := strings.TrimSpace(commandsOut.String()); got != wantCommand {
		t.Fatalf("instance up --dry-run --commands = %q, want %q", got, wantCommand)
	}

	rootScoped := NewRootCmd()
	rootScopedOut, rootScopedErr := &bytes.Buffer{}, &bytes.Buffer{}
	rootScoped.SetOut(rootScopedOut)
	rootScoped.SetErr(rootScopedErr)
	rootScoped.SetArgs([]string{"--repo", tmp, "instance", "up", "--last", "2", "--status", "stopped", "--dry-run", "--commands"})
	if err := rootScoped.Execute(); err != nil {
		t.Fatalf("instance up root --repo --dry-run --commands: %v\nstderr=%s", err, rootScopedErr.String())
	}
	if got := strings.TrimSpace(rootScopedOut.String()); got != wantCommand {
		t.Fatalf("instance up root --repo --dry-run --commands = %q, want %q", got, wantCommand)
	}
}

func TestInstanceUpWaitJSONHonorsAgentFilterHealth(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-instance-up-filter-wait-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusRunning,
		PID:      os.Getpid(),
	}); err != nil {
		t.Fatalf("write manager metadata: %v", err)
	}
	mgr := daemon.NewInstanceManager(root, nil)
	if err := mgr.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	cleanupDaemon := startRunTestDaemon(t, teamDir, mgr)
	defer cleanupDaemon()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"instance", "up",
		"--agent", "manager",
		"--wait",
		"--timeout", "50ms",
		"--json",
		"--target", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("instance up --agent manager --wait --json: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var body lifecycleHealthResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode instance up filtered wait json: %v\nbody=%s", err, out.String())
	}
	if body.Health == nil || !body.Health.Healthy {
		t.Fatalf("filtered instance up wait health = %+v, want healthy manager-only health", body.Health)
	}
	if len(body.Actions) != 1 || body.Actions[0].Instance != "manager" || body.Actions[0].Action != "skip" {
		t.Fatalf("filtered instance up wait actions = %+v, want manager skip only", body.Actions)
	}
	if body.Health.Declared.Persistent != 1 || body.Health.Declared.Running != 1 || body.Health.Declared.Missing != 0 {
		t.Fatalf("filtered instance up wait declared health = %+v, want only manager declared", body.Health.Declared)
	}
}

func TestInstanceDownLastDryRunSelectsNewestRunningMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(-3 * time.Hour)},
		{Instance: "mid", Agent: "worker", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(-1 * time.Hour)},
		{Instance: "stopped-newer", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-30 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"instance", "down", "--last", "2", "--status", "running", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("instance down --last dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var rows []instanceDownResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode dry-run json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 2 || rows[0].Instance != "new" || rows[1].Instance != "mid" {
		t.Fatalf("rows = %+v, want newest two running metadata targets", rows)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"instance", "down", "--last", "2", "--status", "running", "--dry-run", "--commands", "--rm", "--timeout", "10s", "--target", tmp})
	if err := commands.Execute(); err != nil {
		t.Fatalf("instance down --dry-run --commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{
		"agent-team",
		"instance",
		"down",
		"--repo",
		tmp,
		"--last",
		"2",
		"--status",
		"running",
		"--rm",
		"--timeout",
		"10s",
	}), " ")
	if got := strings.TrimSpace(commandsOut.String()); got != wantCommand {
		t.Fatalf("instance down --dry-run --commands = %q, want %q", got, wantCommand)
	}

	rootScoped := NewRootCmd()
	rootScopedOut, rootScopedErr := &bytes.Buffer{}, &bytes.Buffer{}
	rootScoped.SetOut(rootScopedOut)
	rootScoped.SetErr(rootScopedErr)
	rootScoped.SetArgs([]string{"--repo", tmp, "instance", "down", "--last", "2", "--status", "running", "--dry-run", "--commands", "--rm", "--timeout", "10s"})
	if err := rootScoped.Execute(); err != nil {
		t.Fatalf("instance down root --repo --dry-run --commands: %v\nstderr=%s", err, rootScopedErr.String())
	}
	if got := strings.TrimSpace(rootScopedOut.String()); got != wantCommand {
		t.Fatalf("instance down root --repo --dry-run --commands = %q, want %q", got, wantCommand)
	}
}

func TestInstanceUpDownRejectInvalidLatestLastOptions(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"instance", "up", "--dry-run", "--last", "-1"}, "--last must be >= 0"},
		{[]string{"instance", "up", "--dry-run", "--latest", "--last", "2"}, "choose one of --latest or --last"},
		{[]string{"instance", "up", "manager", "--dry-run", "--last", "2"}, "--last cannot be combined with instance names"},
		{[]string{"instance", "up", "--timeout", "-1s"}, "--timeout must be >= 0"},
		{[]string{"instance", "up", "--dry-run", "--wait"}, "--dry-run cannot be combined with --wait"},
		{[]string{"instance", "up", "--commands"}, "--commands requires --dry-run"},
		{[]string{"instance", "up", "--dry-run", "--commands", "--json"}, "--commands cannot be combined with --json"},
		{[]string{"instance", "up", "--dry-run", "--commands", "--summary"}, "--commands cannot be combined with --summary"},
		{[]string{"instance", "up", "--dry-run", "--commands", "--format", "{{.Instance}}"}, "--commands cannot be combined with --format"},
		{[]string{"instance", "up", "--dry-run", "--commands", "--attach", "manager"}, "--commands cannot be combined with --attach"},
		{[]string{"instance", "down", "--last", "-1"}, "--last must be >= 0"},
		{[]string{"instance", "down", "--latest", "--last", "2"}, "choose one of --latest or --last"},
		{[]string{"instance", "down", "manager", "--last", "2"}, "--last cannot be combined with instance names"},
		{[]string{"instance", "down", "--commands"}, "--commands requires --dry-run"},
		{[]string{"instance", "down", "--dry-run", "--commands", "--json"}, "--commands cannot be combined with --json"},
		{[]string{"instance", "down", "--dry-run", "--commands", "--summary"}, "--commands cannot be combined with --summary"},
		{[]string{"instance", "down", "--dry-run", "--commands", "--format", "{{.Instance}}"}, "--commands cannot be combined with --format"},
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

func TestSelectLifecycleTargets_UnknownExplicitName(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	topo, err := topology.LoadFromTeamDir(filepath.Join(tmp, ".agent_team"))
	if err != nil {
		t.Fatalf("load topology: %v", err)
	}

	_, err = selectLifecycleTargets(topo, nil, []string{"ghost"})
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("expected unknown-name error, got %v", err)
	}
}

func TestInstanceRm_Force(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	stateDir := filepath.Join(tmp, ".agent_team", "state", "ephemeral")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"instance", "rm", "ephemeral", "--target", tmp, "--force"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("instance rm --force: %v", err)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Errorf("state dir should be gone, got err=%v", err)
	}
	if !strings.Contains(out.String(), "removed") {
		t.Errorf("missing 'removed' message: %s", out.String())
	}
}

func TestRmTopLevel_Force(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	stateDir := filepath.Join(tmp, ".agent_team", "state", "ephemeral")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"rm", "ephemeral", "--target", tmp, "--force"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rm --force: %v", err)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Errorf("state dir should be gone, got err=%v", err)
	}
	if !strings.Contains(out.String(), "removed") {
		t.Errorf("missing 'removed' message: %s", out.String())
	}
}

func TestRmTopLevelDryRunCommands(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	stateDir := filepath.Join(tmp, ".agent_team", "state", "ephemeral")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm", "ephemeral", "--target", tmp, "--force", "--dry-run", "--commands"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rm --dry-run --commands: %v\nstderr=%s", err, stderr.String())
	}
	want := strings.Join(shellQuoteArgs([]string{"agent-team", "rm", "--repo", tmp, "ephemeral", "--force"}), " ")
	if got := strings.TrimSpace(out.String()); got != want {
		t.Fatalf("rm --dry-run --commands = %q, want %q", got, want)
	}

	rootScoped := NewRootCmd()
	rootScopedOut, rootScopedErr := &bytes.Buffer{}, &bytes.Buffer{}
	rootScoped.SetOut(rootScopedOut)
	rootScoped.SetErr(rootScopedErr)
	rootScoped.SetArgs([]string{"--repo", tmp, "rm", "ephemeral", "--force", "--dry-run", "--commands"})
	if err := rootScoped.Execute(); err != nil {
		t.Fatalf("rm root --repo --dry-run --commands: %v\nstderr=%s", err, rootScopedErr.String())
	}
	if got := strings.TrimSpace(rootScopedOut.String()); got != want {
		t.Fatalf("rm root --repo --dry-run --commands = %q, want %q", got, want)
	}
	if _, err := os.Stat(stateDir); err != nil {
		t.Fatalf("state should remain after dry-run: %v", err)
	}
}

func TestInstanceRmDryRunCommands(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	stateDir := filepath.Join(tmp, ".agent_team", "state", "ephemeral")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"instance", "rm", "ephemeral", "--target", tmp, "--force", "--dry-run", "--commands"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("instance rm --dry-run --commands: %v\nstderr=%s", err, stderr.String())
	}
	want := strings.Join(shellQuoteArgs([]string{"agent-team", "instance", "rm", "--repo", tmp, "ephemeral", "--force"}), " ")
	if got := strings.TrimSpace(out.String()); got != want {
		t.Fatalf("instance rm --dry-run --commands = %q, want %q", got, want)
	}
	if _, err := os.Stat(stateDir); err != nil {
		t.Fatalf("state should remain after dry-run: %v", err)
	}

	rootScoped := NewRootCmd()
	rootScopedOut, rootScopedErr := &bytes.Buffer{}, &bytes.Buffer{}
	rootScoped.SetOut(rootScopedOut)
	rootScoped.SetErr(rootScopedErr)
	rootScoped.SetArgs([]string{"--repo", tmp, "instance", "rm", "ephemeral", "--force", "--dry-run", "--commands"})
	if err := rootScoped.Execute(); err != nil {
		t.Fatalf("instance rm root --repo --dry-run --commands: %v\nstderr=%s", err, rootScopedErr.String())
	}
	if got := strings.TrimSpace(rootScopedOut.String()); got != want {
		t.Fatalf("instance rm root --repo --dry-run --commands = %q, want %q", got, want)
	}

	noAction := NewRootCmd()
	noActionOut, noActionErr := &bytes.Buffer{}, &bytes.Buffer{}
	noAction.SetOut(noActionOut)
	noAction.SetErr(noActionErr)
	noAction.SetArgs([]string{"instance", "rm", "--all", "--target", tmp, "--runtime", "codex", "--dry-run", "--commands"})
	if err := noAction.Execute(); err != nil {
		t.Fatalf("instance rm --dry-run --commands no action: %v\nstderr=%s", err, noActionErr.String())
	}
	if got := strings.TrimSpace(noActionOut.String()); got != "" {
		t.Fatalf("instance rm no-action commands = %q, want empty", got)
	}
}

func TestRmTopLevelJSONForce(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	stateDir := filepath.Join(tmp, ".agent_team", "state", "ephemeral")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"rm", "ephemeral", "--target", tmp, "--force", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rm --json --force: %v", err)
	}
	var rows []instanceRmResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode rm json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "ephemeral" || !rows[0].Removed || !rows[0].StateRemoved {
		t.Fatalf("rm json rows = %+v, want removed ephemeral state", rows)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Errorf("state dir should be gone, got err=%v", err)
	}
}

func TestRmTopLevelFormatForce(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	stateDir := filepath.Join(tmp, ".agent_team", "state", "ephemeral")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"rm", "ephemeral", "--target", tmp, "--force", "--format", "{{.Instance}}:{{.Removed}}:{{.StateRemoved}}:{{.Path}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rm --format --force: %v", err)
	}
	if got, want := out.String(), "ephemeral:true:true:.agent_team/state/ephemeral\n"; got != want {
		t.Fatalf("rm --format output = %q, want %q", got, want)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Errorf("state dir should be gone, got err=%v", err)
	}
}

func TestRmDryRunFormatDoesNotRemoveStateOrMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	stateDir := filepath.Join(teamDir, "state", "ephemeral")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "ephemeral",
		Agent:    "worker",
		Status:   daemon.StatusRunning,
		PID:      os.Getpid(),
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"rm", "ephemeral",
		"--target", tmp,
		"--dry-run",
		"--format", "{{.Instance}}:{{.Action}}:{{.DryRun}}:{{.StateRemoved}}:{{.DaemonRemoved}}:{{.Removed}}:{{.Path}}",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rm --dry-run --format: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "ephemeral:remove:true:true:true:false:.agent_team/state/ephemeral\n"; got != want {
		t.Fatalf("rm dry-run format = %q, want %q", got, want)
	}
	if _, err := os.Stat(stateDir); err != nil {
		t.Fatalf("state dir should remain after dry-run, stat err=%v", err)
	}
	if _, err := daemon.ReadMetadata(root, "ephemeral"); err != nil {
		t.Fatalf("metadata should remain after dry-run: %v", err)
	}
}

func TestRmLastDryRunSelectsNewestLocalMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "mid", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-30 * time.Minute)},
	} {
		if err := os.MkdirAll(filepath.Join(teamDir, "state", meta.Instance), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm", "--last", "2", "--status", "stopped", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rm --last --dry-run --json: %v\nstderr=%s", err, stderr.String())
	}
	var rows []instanceRmResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode rm --last json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 2 || rows[0].Instance != "new" || rows[1].Instance != "mid" {
		t.Fatalf("rows = %+v, want newest two new,mid", rows)
	}
	for _, name := range []string{"old", "new", "mid"} {
		if _, err := daemon.ReadMetadata(root, name); err != nil {
			t.Fatalf("metadata %s should remain after dry-run: %v", name, err)
		}
	}
}

func TestInstanceRmLatestDryRunSelectsNewestMatchingAgent(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "worker-new", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "manager-old", Agent: "manager", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "manager-new", Agent: "manager", Status: daemon.StatusStopped, StartedAt: now.Add(-30 * time.Minute)},
	} {
		if err := os.MkdirAll(filepath.Join(teamDir, "state", meta.Instance), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"instance", "rm", "--latest", "--agent", "manager", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("instance rm --latest --agent --dry-run --json: %v\nstderr=%s", err, stderr.String())
	}
	var rows []instanceRmResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode instance rm --latest json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "manager-new" {
		t.Fatalf("rows = %+v, want newest manager", rows)
	}
}

func TestRmTopLevelQuietForceSuppressesOutput(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	stateDir := filepath.Join(tmp, ".agent_team", "state", "ephemeral")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm", "ephemeral", "--target", tmp, "--force", "--quiet"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rm --force --quiet: %v\nstderr: %s", err, stderr.String())
	}
	if out.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("quiet rm should not write output, stdout=%q stderr=%q", out.String(), stderr.String())
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Errorf("state dir should be gone, got err=%v", err)
	}
}

func TestRmAllForceJSONRemovesMatchingAgent(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-rm-all-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	mgr := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, 2*time.Second))
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()
	defer func() {
		for _, meta := range mgr.List() {
			if meta.Status == daemon.StatusRunning {
				stopAndWaitForTest(t, mgr, meta.Instance)
			}
		}
	}()

	for _, spec := range []struct {
		name  string
		agent string
	}{
		{name: "manager-one", agent: "manager"},
		{name: "worker-one", agent: "worker"},
	} {
		if err := os.MkdirAll(filepath.Join(teamDir, "state", spec.name), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := mgr.Dispatch(daemon.DispatchInput{Agent: spec.agent, Name: spec.name, Workspace: tmp}); err != nil {
			t.Fatalf("dispatch %s: %v", spec.name, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm", "--all", "--agent", "manager", "--force", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rm --all --agent --force --json: %v\nstderr: %s", err, stderr.String())
	}
	var rows []instanceRmResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode rm --all json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "manager-one" || !rows[0].Removed || !rows[0].DaemonRemoved || !rows[0].StateRemoved {
		t.Fatalf("rm --all rows = %+v, want manager-one fully removed", rows)
	}
	if _, err := os.Stat(filepath.Join(teamDir, "state", "manager-one")); !os.IsNotExist(err) {
		t.Fatalf("manager-one state should be gone, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(teamDir, "state", "worker-one")); err != nil {
		t.Fatalf("worker-one state should remain, stat err=%v", err)
	}
	for _, meta := range mgr.List() {
		if meta.Instance == "manager-one" {
			t.Fatalf("daemon metadata still includes removed manager-one: %+v", meta)
		}
	}
}

func TestRmRuntimeDryRunJSONUsesLocalMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now().UTC()
	for _, item := range []struct {
		name    string
		agent   string
		runtime string
		status  daemon.Status
	}{
		{name: "codex-running", agent: "worker", runtime: "codex", status: daemon.StatusRunning},
		{name: "codex-stopped", agent: "manager", runtime: "codex", status: daemon.StatusStopped},
		{name: "claude-stopped", agent: "manager", runtime: "claude", status: daemon.StatusStopped},
	} {
		if err := os.MkdirAll(filepath.Join(teamDir, "state", item.name), 0o755); err != nil {
			t.Fatalf("mkdir state %s: %v", item.name, err)
		}
		if err := daemon.WriteMetadata(root, &daemon.Metadata{
			Instance:  item.name,
			Agent:     item.agent,
			Runtime:   item.runtime,
			Status:    item.status,
			StartedAt: now,
		}); err != nil {
			t.Fatalf("write metadata %s: %v", item.name, err)
		}
	}

	for _, args := range [][]string{
		{"rm", "--runtime", "codex", "--dry-run", "--json", "--target", tmp},
		{"instance", "rm", "--runtime", "codex", "--dry-run", "--json", "--target", tmp},
	} {
		cmd := NewRootCmd()
		out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(stderr)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%v: %v\nstderr=%s", args, err, stderr.String())
		}
		var rows []instanceRmResult
		if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
			t.Fatalf("%v: decode json: %v\nbody=%s", args, err, out.String())
		}
		if got := instanceRmResultNames(rows); strings.Join(got, ",") != "codex-running,codex-stopped" {
			t.Fatalf("%v: rows = %+v, want codex-running,codex-stopped", args, rows)
		}
		for _, row := range rows {
			if !row.DryRun || !row.DaemonRemoved || !row.StateRemoved {
				t.Fatalf("%v: row = %+v, want dry-run state and metadata removal preview", args, row)
			}
		}
	}

	for _, name := range []string{"codex-running", "codex-stopped", "claude-stopped"} {
		if _, err := os.Stat(filepath.Join(teamDir, "state", name)); err != nil {
			t.Fatalf("%s state should remain after dry-run: %v", name, err)
		}
		if _, err := daemon.ReadMetadata(root, name); err != nil {
			t.Fatalf("%s metadata should remain after dry-run: %v", name, err)
		}
	}

	bad := NewRootCmd()
	bad.SetOut(&bytes.Buffer{})
	var badErr bytes.Buffer
	bad.SetErr(&badErr)
	bad.SetArgs([]string{"rm", "--runtime", "llama", "--dry-run", "--target", tmp})
	if err := bad.Execute(); err == nil {
		t.Fatal("rm accepted unknown runtime")
	}
	if !strings.Contains(badErr.String(), "unknown --runtime") {
		t.Fatalf("bad runtime stderr = %q", badErr.String())
	}
}

func TestRmSummaryJSONDryRun(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	stateDir := filepath.Join(teamDir, "state", "ephemeral")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "ephemeral",
		Agent:    "worker",
		Status:   daemon.StatusStopped,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm", "--all", "--dry-run", "--summary", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rm --summary --json --dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var body lifecycleActionSummaryResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode rm summary json: %v\nbody=%s", err, out.String())
	}
	if body.Summary.Total != 1 || body.Summary.Actions["remove"] != 1 || !body.Summary.DryRun || body.Summary.StateRemoved != 1 || body.Summary.DaemonRemoved != 1 || body.Summary.Removed != 0 {
		t.Fatalf("summary = %+v, want one dry-run removal preview", body.Summary)
	}
	if _, err := os.Stat(stateDir); err != nil {
		t.Fatalf("state dir should remain after dry-run: %v", err)
	}
	if _, err := daemon.ReadMetadata(root, "ephemeral"); err != nil {
		t.Fatalf("metadata should remain after dry-run: %v", err)
	}
}

func TestInstanceRmSummaryTextSuppressesRows(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	stateDir := filepath.Join(teamDir, "state", "ephemeral")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "ephemeral",
		Agent:    "worker",
		Status:   daemon.StatusStopped,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"instance", "rm", "--all", "--dry-run", "--summary", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("instance rm --summary --dry-run: %v\nstderr=%s", err, stderr.String())
	}
	body := out.String()
	if !strings.Contains(body, "summary: total=1 dry_run=true remove=1") || !strings.Contains(body, "removed: total=0 state=1 daemon=1") {
		t.Fatalf("summary output = %q, want aggregate removal counts", body)
	}
	if strings.Contains(body, "would remove") {
		t.Fatalf("summary output should suppress per-row dry-run text: %q", body)
	}
	if _, err := os.Stat(stateDir); err != nil {
		t.Fatalf("state dir should remain after dry-run: %v", err)
	}
}

func TestRmJSONRequiresForce(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm", "ephemeral", "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --json without --force to fail")
	}
	if !strings.Contains(stderr.String(), "--json requires --force") {
		t.Fatalf("stderr = %q, want --json requires --force", stderr.String())
	}
}

func TestRmQuietRequiresForceAndRejectsJSON(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"rm", "ephemeral", "--quiet"}, "--quiet requires --force"},
		{[]string{"rm", "ephemeral", "--quiet", "--force", "--json"}, "choose one of --quiet or --json"},
		{[]string{"rm", "ephemeral", "--quiet", "--force", "--summary"}, "choose one of --quiet or --summary"},
		{[]string{"prune", "--quiet", "--summary"}, "choose one of --quiet or --summary"},
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

func TestRmFormatRequiresForceAndRejectsStructuredModes(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"rm", "ephemeral", "--format", "{{.Instance}}"}, "--format requires --force"},
		{[]string{"rm", "ephemeral", "--format", "{{.Instance}}", "--force", "--json"}, "--format cannot be combined"},
		{[]string{"rm", "ephemeral", "--format", "{{.Instance}}", "--force", "--quiet"}, "--format cannot be combined"},
		{[]string{"rm", "ephemeral", "--format", "{{.Instance}}", "--force", "--summary"}, "--format cannot be combined"},
		{[]string{"instance", "rm", "ephemeral", "--format", "{{.Instance}}", "--force", "--summary"}, "--format cannot be combined"},
		{[]string{"prune", "--format", "{{.Instance}}", "--summary"}, "--format cannot be combined"},
		{[]string{"rm", "ephemeral", "--format", "{{", "--force"}, "invalid --format template"},
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

func TestPruneQuietSuppressesOutput(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-prune-quiet-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	stateDir := filepath.Join(teamDir, "state", "finished")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "finished",
		Agent:    "manager",
		Status:   daemon.StatusExited,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	mgr := daemon.NewInstanceManager(root, nil)
	if err := mgr.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"prune", "--quiet", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("prune --quiet: %v\nstderr: %s", err, stderr.String())
	}
	if out.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("quiet prune should not write output, stdout=%q stderr=%q", out.String(), stderr.String())
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Errorf("state dir should be gone, got err=%v", err)
	}
	for _, meta := range mgr.List() {
		if meta.Instance == "finished" {
			t.Fatalf("daemon metadata still includes finished: %+v", meta)
		}
	}
}

func TestPruneFormatPrintsRemovalRows(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-prune-format-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	stateDir := filepath.Join(teamDir, "state", "finished")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "finished",
		Agent:    "manager",
		Status:   daemon.StatusExited,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	mgr := daemon.NewInstanceManager(root, nil)
	if err := mgr.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"prune", "--format", "{{.Instance}}:{{.Path}}", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("prune --format: %v\nstderr: %s", err, stderr.String())
	}
	if got, want := out.String(), "finished:.agent_team/state/finished\n"; got != want {
		t.Fatalf("prune --format output = %q, want %q", got, want)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Errorf("state dir should be gone, got err=%v", err)
	}
	for _, meta := range mgr.List() {
		if meta.Instance == "finished" {
			t.Fatalf("daemon metadata still includes finished: %+v", meta)
		}
	}
}

func TestPruneDryRunJSONDoesNotRemoveFinished(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	stateDir := filepath.Join(teamDir, "state", "finished")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "finished",
		Agent:    "manager",
		Status:   daemon.StatusExited,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"prune", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("prune --dry-run --json: %v\nstderr=%s", err, stderr.String())
	}
	var rows []instanceRmResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode prune dry-run json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "finished" || !rows[0].DryRun || rows[0].Removed || !rows[0].StateRemoved || !rows[0].DaemonRemoved {
		t.Fatalf("rows = %+v, want dry-run finished removal preview", rows)
	}
	if _, err := os.Stat(stateDir); err != nil {
		t.Fatalf("state dir should remain after dry-run, stat err=%v", err)
	}
	if _, err := daemon.ReadMetadata(root, "finished"); err != nil {
		t.Fatalf("metadata should remain after dry-run: %v", err)
	}
}

func TestPruneOlderThanDryRunJSONOnlyMatchesOldFinished(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now().UTC()
	records := []struct {
		name     string
		status   daemon.Status
		exitedAt time.Time
	}{
		{name: "old", status: daemon.StatusExited, exitedAt: now.Add(-48 * time.Hour)},
		{name: "recent", status: daemon.StatusCrashed, exitedAt: now.Add(-1 * time.Hour)},
		{name: "unknown-time", status: daemon.StatusExited},
		{name: "stopped", status: daemon.StatusStopped, exitedAt: now.Add(-72 * time.Hour)},
	}
	for _, record := range records {
		stateDir := filepath.Join(teamDir, "state", record.name)
		if err := os.MkdirAll(stateDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := daemon.WriteMetadata(root, &daemon.Metadata{
			Instance: record.name,
			Agent:    "manager",
			Status:   record.status,
			ExitedAt: record.exitedAt,
		}); err != nil {
			t.Fatalf("write metadata for %s: %v", record.name, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"prune", "--dry-run", "--json", "--older-than", "24h", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("prune --older-than dry-run json: %v\nstderr=%s", err, stderr.String())
	}
	var rows []instanceRmResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode prune older-than json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "old" || !rows[0].DryRun {
		t.Fatalf("rows = %+v, want only old dry-run removal preview", rows)
	}
	for _, record := range records {
		if _, err := os.Stat(filepath.Join(teamDir, "state", record.name)); err != nil {
			t.Fatalf("%s state should remain after dry-run: %v", record.name, err)
		}
		if _, err := daemon.ReadMetadata(root, record.name); err != nil {
			t.Fatalf("%s metadata should remain after dry-run: %v", record.name, err)
		}
	}
}

func TestPruneOlderThanRejectsNegativeDuration(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"prune", "--older-than=-1s"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--older-than must be >= 0") {
		t.Fatalf("stderr = %q, want --older-than validation", stderr.String())
	}
}

func TestPruneUsesLocalMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	stateDir := filepath.Join(teamDir, "state", "finished")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "finished",
		Agent:    "manager",
		Status:   daemon.StatusExited,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"prune", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("prune local metadata: %v\nstderr=%s", err, stderr.String())
	}
	var rows []instanceRmResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode prune json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "finished" || !rows[0].Removed || !rows[0].DaemonRemoved || !rows[0].StateRemoved {
		t.Fatalf("rows = %+v, want finished fully removed", rows)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("state dir should be gone, stat err=%v", err)
	}
	if _, err := daemon.ReadMetadata(root, "finished"); !os.IsNotExist(err) {
		t.Fatalf("metadata should be gone, err=%v", err)
	}
}

func TestPruneSummaryJSONUsesLocalMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	stateDir := filepath.Join(teamDir, "state", "finished")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "finished",
		Agent:    "manager",
		Status:   daemon.StatusExited,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"prune", "--summary", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("prune --summary --json: %v\nstderr=%s", err, stderr.String())
	}
	var body lifecycleActionSummaryResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode prune summary json: %v\nbody=%s", err, out.String())
	}
	if body.Summary.Total != 1 || body.Summary.Actions["remove"] != 1 || body.Summary.Removed != 1 || body.Summary.StateRemoved != 1 || body.Summary.DaemonRemoved != 1 {
		t.Fatalf("summary = %+v, want one finished removal", body.Summary)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("state dir should be gone, stat err=%v", err)
	}
	if _, err := daemon.ReadMetadata(root, "finished"); !os.IsNotExist(err) {
		t.Fatalf("metadata should be gone, err=%v", err)
	}
}

func TestPrunePhaseUsesLocalStatusWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, item := range []struct {
		name  string
		phase string
	}{
		{name: "done", phase: "done"},
		{name: "idle", phase: "idle"},
	} {
		stateDir := filepath.Join(teamDir, "state", item.name)
		writeStatus(t, stateDir, "[status]\nphase = \""+item.phase+"\"\ndescription = \"fixture\"\n", time.Time{})
		if err := daemon.WriteMetadata(root, &daemon.Metadata{
			Instance: item.name,
			Agent:    "manager",
			Status:   daemon.StatusExited,
		}); err != nil {
			t.Fatalf("write metadata %s: %v", item.name, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"prune", "--phase", "done", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("prune --phase done local metadata: %v\nstderr=%s", err, stderr.String())
	}
	var rows []instanceRmResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode prune --phase json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "done" || !rows[0].Removed || !rows[0].DaemonRemoved || !rows[0].StateRemoved {
		t.Fatalf("rows = %+v, want done fully removed", rows)
	}
	if _, err := os.Stat(filepath.Join(teamDir, "state", "done")); !os.IsNotExist(err) {
		t.Fatalf("done state dir should be gone, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(teamDir, "state", "idle")); err != nil {
		t.Fatalf("idle state dir should remain: %v", err)
	}
	if _, err := daemon.ReadMetadata(root, "done"); !os.IsNotExist(err) {
		t.Fatalf("done metadata should be gone, err=%v", err)
	}
	if _, err := daemon.ReadMetadata(root, "idle"); err != nil {
		t.Fatalf("idle metadata should remain: %v", err)
	}
}

func TestPruneStatusNarrowsFinishedInstances(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, item := range []struct {
		name   string
		status daemon.Status
	}{
		{name: "crashed", status: daemon.StatusCrashed},
		{name: "exited", status: daemon.StatusExited},
		{name: "stopped", status: daemon.StatusStopped},
	} {
		stateDir := filepath.Join(teamDir, "state", item.name)
		if err := os.MkdirAll(stateDir, 0o755); err != nil {
			t.Fatalf("mkdir state %s: %v", item.name, err)
		}
		if err := daemon.WriteMetadata(root, &daemon.Metadata{
			Instance: item.name,
			Agent:    "manager",
			Status:   item.status,
		}); err != nil {
			t.Fatalf("write metadata %s: %v", item.name, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"prune", "--status", "crashed", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("prune --status crashed: %v\nstderr=%s", err, stderr.String())
	}
	var rows []instanceRmResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode prune --status json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "crashed" || !rows[0].Removed {
		t.Fatalf("rows = %+v, want only crashed removed", rows)
	}
	for _, name := range []string{"exited", "stopped"} {
		if _, err := os.Stat(filepath.Join(teamDir, "state", name)); err != nil {
			t.Fatalf("%s state should remain: %v", name, err)
		}
		if _, err := daemon.ReadMetadata(root, name); err != nil {
			t.Fatalf("%s metadata should remain: %v", name, err)
		}
	}
}

func TestPruneRuntimeNarrowsFinishedInstances(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, item := range []struct {
		name    string
		runtime string
		status  daemon.Status
	}{
		{name: "codex-crashed", runtime: "codex", status: daemon.StatusCrashed},
		{name: "codex-exited", runtime: "codex", status: daemon.StatusExited},
		{name: "codex-stopped", runtime: "codex", status: daemon.StatusStopped},
		{name: "claude-exited", runtime: "claude", status: daemon.StatusExited},
	} {
		stateDir := filepath.Join(teamDir, "state", item.name)
		if err := os.MkdirAll(stateDir, 0o755); err != nil {
			t.Fatalf("mkdir state %s: %v", item.name, err)
		}
		if err := daemon.WriteMetadata(root, &daemon.Metadata{
			Instance: item.name,
			Agent:    "worker",
			Runtime:  item.runtime,
			Status:   item.status,
		}); err != nil {
			t.Fatalf("write metadata %s: %v", item.name, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"prune", "--runtime", "codex", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("prune --runtime codex: %v\nstderr=%s", err, stderr.String())
	}
	var rows []instanceRmResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode prune --runtime json: %v\nbody=%s", err, out.String())
	}
	if got := instanceRmResultNames(rows); strings.Join(got, ",") != "codex-crashed,codex-exited" {
		t.Fatalf("rows = %+v, want codex-crashed,codex-exited", rows)
	}
	for _, row := range rows {
		if !row.DryRun || !row.DaemonRemoved || !row.StateRemoved {
			t.Fatalf("row = %+v, want dry-run state and metadata removal preview", row)
		}
	}
	for _, name := range []string{"codex-crashed", "codex-exited", "codex-stopped", "claude-exited"} {
		if _, err := os.Stat(filepath.Join(teamDir, "state", name)); err != nil {
			t.Fatalf("%s state should remain after dry-run: %v", name, err)
		}
		if _, err := daemon.ReadMetadata(root, name); err != nil {
			t.Fatalf("%s metadata should remain after dry-run: %v", name, err)
		}
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"prune", "--runtime", "codex", "--status", "crashed", "--dry-run", "--commands", "--target", tmp})
	if err := commands.Execute(); err != nil {
		t.Fatalf("prune --dry-run --commands: %v\nstderr=%s", err, commandsErr.String())
	}
	want := strings.Join(shellQuoteArgs([]string{"agent-team", "prune", "--repo", tmp, "--runtime", "codex", "--status", "crashed"}), " ")
	if got := strings.TrimSpace(commandsOut.String()); got != want {
		t.Fatalf("prune --dry-run --commands = %q, want %q", got, want)
	}

	rootScopedCommands := NewRootCmd()
	rootScopedCommandsOut, rootScopedCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	rootScopedCommands.SetOut(rootScopedCommandsOut)
	rootScopedCommands.SetErr(rootScopedCommandsErr)
	rootScopedCommands.SetArgs([]string{"--repo", tmp, "prune", "--runtime", "codex", "--status", "crashed", "--dry-run", "--commands"})
	if err := rootScopedCommands.Execute(); err != nil {
		t.Fatalf("prune root --repo --dry-run --commands: %v\nstderr=%s", err, rootScopedCommandsErr.String())
	}
	if got := strings.TrimSpace(rootScopedCommandsOut.String()); got != want {
		t.Fatalf("prune root --repo --dry-run --commands = %q, want %q", got, want)
	}
}

func TestPruneDryRunCommandsNoActionIsSilent(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"prune", "--target", tmp, "--dry-run", "--commands"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("prune --dry-run --commands no action: %v\nstderr=%s", err, stderr.String())
	}
	if got := strings.TrimSpace(out.String()); got != "" {
		t.Fatalf("prune --dry-run --commands no action = %q, want empty", got)
	}
}

func TestRemoveCommandsRejectInvalidRenderModes(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "rm requires dry run",
			args: []string{"rm", "ephemeral", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "rm json",
			args: []string{"rm", "ephemeral", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "instance rm requires dry run",
			args: []string{"instance", "rm", "ephemeral", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "instance rm json",
			args: []string{"instance", "rm", "ephemeral", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "instance rm summary",
			args: []string{"instance", "rm", "ephemeral", "--dry-run", "--commands", "--summary"},
			want: "--commands cannot be combined with --summary",
		},
		{
			name: "instance rm format",
			args: []string{"instance", "rm", "ephemeral", "--dry-run", "--commands", "--format", "{{.Instance}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "prune summary",
			args: []string{"prune", "--dry-run", "--commands", "--summary"},
			want: "--commands cannot be combined with --summary",
		},
		{
			name: "prune quiet",
			args: []string{"prune", "--dry-run", "--commands", "--quiet"},
			want: "--commands cannot be combined with --quiet",
		},
		{
			name: "prune format",
			args: []string{"prune", "--dry-run", "--commands", "--format", "{{.Instance}}"},
			want: "--commands cannot be combined with --format",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil {
				t.Fatalf("command succeeded\nstdout=%s\nstderr=%s", out.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestPruneStaleNarrowsFinishedInstances(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	old := time.Now().Add(-staleAfter - time.Minute)
	for _, item := range []struct {
		name   string
		status daemon.Status
		phase  string
		mtime  time.Time
	}{
		{name: "stale-exited", status: daemon.StatusExited, phase: "implementing", mtime: old},
		{name: "fresh-exited", status: daemon.StatusExited, phase: "implementing", mtime: time.Now()},
		{name: "idle-old", status: daemon.StatusExited, phase: "idle", mtime: old},
		{name: "running-stale", status: daemon.StatusRunning, phase: "implementing", mtime: old},
	} {
		stateDir := filepath.Join(teamDir, "state", item.name)
		writeStatus(t, stateDir, "[status]\nphase = \""+item.phase+"\"\ndescription = \"fixture\"\n", item.mtime)
		if err := daemon.WriteMetadata(root, &daemon.Metadata{
			Instance: item.name,
			Agent:    "manager",
			Status:   item.status,
		}); err != nil {
			t.Fatalf("write metadata %s: %v", item.name, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"prune", "--stale", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("prune --stale: %v\nstderr=%s", err, stderr.String())
	}
	var rows []instanceRmResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode prune --stale json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "stale-exited" || !rows[0].Removed {
		t.Fatalf("rows = %+v, want only stale-exited removed", rows)
	}
	for _, name := range []string{"fresh-exited", "idle-old", "running-stale"} {
		if _, err := os.Stat(filepath.Join(teamDir, "state", name)); err != nil {
			t.Fatalf("%s state should remain: %v", name, err)
		}
		if _, err := daemon.ReadMetadata(root, name); err != nil {
			t.Fatalf("%s metadata should remain: %v", name, err)
		}
	}
}

func TestPruneUnhealthyNarrowsFinishedInstances(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	old := time.Now().Add(-staleAfter - time.Minute)
	for _, item := range []struct {
		name   string
		status daemon.Status
		phase  string
		mtime  time.Time
	}{
		{name: "crashed", status: daemon.StatusCrashed, phase: "idle", mtime: time.Now()},
		{name: "stale-exited", status: daemon.StatusExited, phase: "blocked", mtime: old},
		{name: "fresh-exited", status: daemon.StatusExited, phase: "done", mtime: old},
		{name: "stopped-stale", status: daemon.StatusStopped, phase: "implementing", mtime: old},
	} {
		stateDir := filepath.Join(teamDir, "state", item.name)
		writeStatus(t, stateDir, "[status]\nphase = \""+item.phase+"\"\ndescription = \"fixture\"\n", item.mtime)
		if err := daemon.WriteMetadata(root, &daemon.Metadata{
			Instance: item.name,
			Agent:    "manager",
			Status:   item.status,
		}); err != nil {
			t.Fatalf("write metadata %s: %v", item.name, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"prune", "--unhealthy", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("prune --unhealthy: %v\nstderr=%s", err, stderr.String())
	}
	var rows []instanceRmResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode prune --unhealthy json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 2 || rows[0].Instance != "crashed" || rows[1].Instance != "stale-exited" {
		t.Fatalf("rows = %+v, want crashed and stale-exited removed", rows)
	}
	for _, row := range rows {
		if !row.Removed || !row.StateRemoved || !row.DaemonRemoved {
			t.Fatalf("row = %+v, want full removal", row)
		}
	}
	for _, name := range []string{"fresh-exited", "stopped-stale"} {
		if _, err := os.Stat(filepath.Join(teamDir, "state", name)); err != nil {
			t.Fatalf("%s state should remain: %v", name, err)
		}
		if _, err := daemon.ReadMetadata(root, name); err != nil {
			t.Fatalf("%s metadata should remain: %v", name, err)
		}
	}
}

func TestPruneStatusRejectsNonFinishedStatus(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"prune", "--status", "stopped"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected prune --status stopped validation error")
	}
	if !strings.Contains(stderr.String(), "accepts only exited or crashed") {
		t.Fatalf("stderr = %q, want prune status validation", stderr.String())
	}
}

func TestRmFinishedSelectsOnlyExitedAndCrashed(t *testing.T) {
	targets := selectFinishedRmTargets(map[string]daemonInstanceInfo{
		"running": {status: string(daemon.StatusRunning)},
		"stopped": {status: string(daemon.StatusStopped)},
		"exited":  {status: string(daemon.StatusExited)},
		"crashed": {status: string(daemon.StatusCrashed)},
	}, nil)
	if strings.Join(targets, ",") != "crashed,exited" {
		t.Fatalf("targets = %v, want crashed,exited", targets)
	}
}

func TestRmFinishedSelectsOnlyMatchingAgent(t *testing.T) {
	targets := selectFinishedRmTargets(map[string]daemonInstanceInfo{
		"finished-manager": {status: string(daemon.StatusExited), agent: "manager"},
		"finished-worker":  {status: string(daemon.StatusCrashed), agent: "worker"},
		"stopped-manager":  {status: string(daemon.StatusStopped), agent: "manager"},
	}, []string{"manager"})
	if strings.Join(targets, ",") != "finished-manager" {
		t.Fatalf("targets = %v, want finished-manager", targets)
	}
}

func TestRmStatusSelectsMatchingStatuses(t *testing.T) {
	statuses, err := lifecycleStatusFilterSet([]string{"stopped,exited"})
	if err != nil {
		t.Fatalf("lifecycleStatusFilterSet: %v", err)
	}
	targets := selectRmTargets(map[string]daemonInstanceInfo{
		"running": {status: string(daemon.StatusRunning), agent: "manager"},
		"stopped": {status: string(daemon.StatusStopped), agent: "manager"},
		"exited":  {status: string(daemon.StatusExited), agent: "worker"},
		"crashed": {status: string(daemon.StatusCrashed), agent: "worker"},
	}, nil, statuses, nil, nil, false, false, nil)
	if strings.Join(targets, ",") != "exited,stopped" {
		t.Fatalf("targets = %v, want exited,stopped", targets)
	}
}

func TestRmStatusSelectsMatchingAgent(t *testing.T) {
	statuses, err := lifecycleStatusFilterSet([]string{"stopped,exited"})
	if err != nil {
		t.Fatalf("lifecycleStatusFilterSet: %v", err)
	}
	targets := selectRmTargets(map[string]daemonInstanceInfo{
		"stopped-manager": {status: string(daemon.StatusStopped), agent: "manager"},
		"exited-manager":  {status: string(daemon.StatusExited), agent: "manager"},
		"stopped-worker":  {status: string(daemon.StatusStopped), agent: "worker"},
	}, []string{"manager"}, statuses, nil, nil, false, false, nil)
	if strings.Join(targets, ",") != "exited-manager,stopped-manager" {
		t.Fatalf("targets = %v, want manager stopped/exited targets", targets)
	}
}

func TestRmFinishedAndStatusIntersect(t *testing.T) {
	statuses, err := lifecycleStatusFilterSet([]string{"exited"})
	if err != nil {
		t.Fatalf("lifecycleStatusFilterSet: %v", err)
	}
	targets := selectRmTargets(map[string]daemonInstanceInfo{
		"exited":  {status: string(daemon.StatusExited), agent: "manager"},
		"crashed": {status: string(daemon.StatusCrashed), agent: "manager"},
		"stopped": {status: string(daemon.StatusStopped), agent: "manager"},
	}, nil, statuses, nil, nil, true, false, nil)
	if strings.Join(targets, ",") != "exited" {
		t.Fatalf("targets = %v, want exited only", targets)
	}
}

func TestRmPhaseSelectsMatchingTargets(t *testing.T) {
	phases, err := lifecyclePhaseFilterSet([]string{"done,blocked"})
	if err != nil {
		t.Fatalf("lifecyclePhaseFilterSet: %v", err)
	}
	targets := selectRmTargets(map[string]daemonInstanceInfo{
		"done-manager":    {status: string(daemon.StatusExited), agent: "manager"},
		"blocked-manager": {status: string(daemon.StatusStopped), agent: "manager"},
		"idle-manager":    {status: string(daemon.StatusStopped), agent: "manager"},
		"done-worker":     {status: string(daemon.StatusExited), agent: "worker"},
	}, []string{"manager"}, nil, phases, map[string]string{
		"done-manager":    "done",
		"blocked-manager": "blocked",
		"idle-manager":    "idle",
		"done-worker":     "done",
	}, false, false, nil)
	if strings.Join(targets, ",") != "blocked-manager,done-manager" {
		t.Fatalf("targets = %v, want blocked-manager,done-manager", targets)
	}
}

func TestRmFinishedAndPhaseIntersect(t *testing.T) {
	phases, err := lifecyclePhaseFilterSet([]string{"done"})
	if err != nil {
		t.Fatalf("lifecyclePhaseFilterSet: %v", err)
	}
	targets := selectRmTargets(map[string]daemonInstanceInfo{
		"done-exited":  {status: string(daemon.StatusExited), agent: "manager"},
		"done-stopped": {status: string(daemon.StatusStopped), agent: "manager"},
		"idle-crashed": {status: string(daemon.StatusCrashed), agent: "manager"},
	}, nil, nil, phases, map[string]string{
		"done-exited":  "done",
		"done-stopped": "done",
		"idle-crashed": "idle",
	}, true, false, nil)
	if strings.Join(targets, ",") != "done-exited" {
		t.Fatalf("targets = %v, want done-exited only", targets)
	}
}

func TestRmStaleSelectsMatchingTargets(t *testing.T) {
	targets := selectRmTargets(map[string]daemonInstanceInfo{
		"stale-manager":  {status: string(daemon.StatusStopped), agent: "manager"},
		"fresh-manager":  {status: string(daemon.StatusStopped), agent: "manager"},
		"stale-worker":   {status: string(daemon.StatusStopped), agent: "worker"},
		"running-worker": {status: string(daemon.StatusRunning), agent: "worker"},
	}, []string{"manager"}, nil, nil, nil, false, true, map[string]bool{
		"stale-manager":  true,
		"stale-worker":   true,
		"running-worker": true,
	})
	if strings.Join(targets, ",") != "stale-manager" {
		t.Fatalf("targets = %v, want stale-manager only", targets)
	}
}

func TestPruneRuntimeStaleNarrowsDeadRecordedPIDs(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	for _, meta := range []*daemon.Metadata{
		{Instance: "fresh", Agent: "manager", Runtime: "codex", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now},
		{Instance: "runtime-stale", Agent: "manager", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999999, StartedAt: now.Add(-time.Minute)},
		{Instance: "exited", Agent: "manager", Runtime: "codex", Status: daemon.StatusExited, PID: 0, StartedAt: now.Add(-2 * time.Minute), ExitedAt: now.Add(-time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"prune", "--runtime-stale", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("prune --runtime-stale: %v\nstderr=%s", err, stderr.String())
	}
	var rows []instanceRmResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode prune --runtime-stale json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "runtime-stale" || !rows[0].DryRun || rows[0].StateRemoved || !rows[0].DaemonRemoved {
		t.Fatalf("rows = %+v, want runtime-stale daemon metadata dry-run removal preview", rows)
	}
}

func TestRmUnhealthySelectsCrashedAndStaleTargets(t *testing.T) {
	targets := selectRmTargetsWithUnhealthy(map[string]daemonInstanceInfo{
		"crashed-manager": {status: string(daemon.StatusCrashed), agent: "manager"},
		"fresh-manager":   {status: string(daemon.StatusRunning), agent: "manager"},
		"runtime-stale":   {status: string(daemon.StatusRunning), agent: "manager", runtimeStale: true},
		"stale-manager":   {status: string(daemon.StatusRunning), agent: "manager"},
		"stale-worker":    {status: string(daemon.StatusRunning), agent: "worker"},
	}, []string{"manager"}, nil, nil, nil, false, false, false, true, map[string]bool{
		"stale-manager": true,
		"stale-worker":  true,
	})
	if strings.Join(targets, ",") != "crashed-manager,runtime-stale,stale-manager" {
		t.Fatalf("targets = %v, want crashed-manager,runtime-stale,stale-manager", targets)
	}
}

func TestRmRuntimeStaleSelectsDeadRecordedPIDs(t *testing.T) {
	targets := selectRmTargetsWithUnhealthy(map[string]daemonInstanceInfo{
		"crashed-manager": {status: string(daemon.StatusCrashed), agent: "manager"},
		"fresh-manager":   {status: string(daemon.StatusRunning), agent: "manager"},
		"runtime-stale":   {status: string(daemon.StatusRunning), agent: "manager", runtimeStale: true},
		"stale-manager":   {status: string(daemon.StatusRunning), agent: "manager"},
	}, []string{"manager"}, nil, nil, nil, false, false, true, false, map[string]bool{
		"stale-manager": true,
	})
	if strings.Join(targets, ",") != "runtime-stale" {
		t.Fatalf("targets = %v, want runtime-stale", targets)
	}
}

func TestRmOlderThanFiltersByTerminalTime(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	targets := filterRmTargetsOlderThan(
		[]string{"future", "old-exited", "old-stopped", "recent", "unknown"},
		map[string]daemonInstanceInfo{
			"future":      {finishedAt: now.Add(1 * time.Minute)},
			"old-exited":  {finishedAt: now.Add(-48 * time.Hour)},
			"old-stopped": {finishedAt: now.Add(-25 * time.Hour)},
			"recent":      {finishedAt: now.Add(-1 * time.Hour)},
			"unknown":     {},
		},
		24*time.Hour,
		now,
	)
	if strings.Join(targets, ",") != "old-exited,old-stopped" {
		t.Fatalf("targets = %v, want old-exited,old-stopped", targets)
	}
}

func TestRmAllSelectsMatchingAgent(t *testing.T) {
	targets := selectRmTargets(map[string]daemonInstanceInfo{
		"manager-running": {status: string(daemon.StatusRunning), agent: "manager"},
		"manager-stopped": {status: string(daemon.StatusStopped), agent: "manager"},
		"worker-running":  {status: string(daemon.StatusRunning), agent: "worker"},
	}, []string{"manager"}, nil, nil, nil, false, false, nil)
	if strings.Join(targets, ",") != "manager-running,manager-stopped" {
		t.Fatalf("targets = %v, want all manager targets", targets)
	}
}

func TestLatestRmTargetsLimitUsesNewestStartedAt(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	targets := latestRmTargetsLimit([]string{"old", "new", "missing", "mid"}, map[string]daemonInstanceInfo{
		"old": {startedAt: now.Add(-2 * time.Hour)},
		"new": {startedAt: now.Add(-5 * time.Minute)},
		"mid": {startedAt: now.Add(-30 * time.Minute)},
	}, 2)
	if strings.Join(targets, ",") != "new,mid" {
		t.Fatalf("targets = %v, want newest two", targets)
	}
}

func TestRmFinishedRejectsEmptyAgentFilter(t *testing.T) {
	targets := selectFinishedRmTargets(map[string]daemonInstanceInfo{
		"finished-manager": {status: string(daemon.StatusExited), agent: "manager"},
	}, []string{"  "})
	if len(targets) != 0 {
		t.Fatalf("targets = %v, want no targets for empty --agent filter", targets)
	}
}

func TestRmRequiresNamesUnlessFinished(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "instance is required unless --all, --finished, --latest, --last, --runtime, --status, --phase, --stale, --runtime-stale, or --unhealthy") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRmAllRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm", "--all", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--all cannot be combined") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRmAllRejectsFinished(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm", "--all", "--finished"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "choose one of --all or --finished") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRmFinishedRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm", "--finished", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--finished cannot be combined") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRmStaleRejectsExplicitNames(t *testing.T) {
	for _, args := range [][]string{
		{"rm", "--stale", "manager"},
		{"instance", "rm", "--stale", "manager"},
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
		if !strings.Contains(stderr.String(), "--stale cannot be combined with instance names") {
			t.Fatalf("%v: stderr = %q", args, stderr.String())
		}
	}
}

func TestRmUnhealthyRejectsExplicitNames(t *testing.T) {
	for _, args := range [][]string{
		{"rm", "--unhealthy", "manager"},
		{"instance", "rm", "--unhealthy", "manager"},
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
		if !strings.Contains(stderr.String(), "--unhealthy cannot be combined with instance names") {
			t.Fatalf("%v: stderr = %q", args, stderr.String())
		}
	}
}

func TestRmLatestLastValidation(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"rm", "manager", "--latest"}, "--latest cannot be combined with instance names"},
		{[]string{"rm", "manager", "--last", "2"}, "--last cannot be combined with instance names"},
		{[]string{"rm", "--last", "-1"}, "--last must be >= 0"},
		{[]string{"rm", "--latest", "--last", "2"}, "choose one of --latest or --last"},
		{[]string{"instance", "rm", "manager", "--latest"}, "--latest cannot be combined with instance names"},
		{[]string{"instance", "rm", "manager", "--last", "2"}, "--last cannot be combined with instance names"},
		{[]string{"instance", "rm", "--last", "-1"}, "--last must be >= 0"},
		{[]string{"instance", "rm", "--latest", "--last", "2"}, "choose one of --latest or --last"},
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

func TestRmAgentRequiresFinished(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm", "--agent", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--agent requires --all, --finished, --latest, --last, --runtime, --status, --phase, --stale, --runtime-stale, or --unhealthy") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRmAgentRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm", "--agent", "manager", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--agent cannot be combined") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRmAgentRequiresNonEmptyFilter(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm", "--finished", "--agent", "  "})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "non-empty agent") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRmStatusRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm", "--status", "stopped", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--status cannot be combined") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRmPhaseRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm", "--phase", "done", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--phase cannot be combined") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRmRejectsUnknownStatus(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm", "--status", "paused", "--force"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "unknown --status") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRmRejectsUnknownPhase(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm", "--phase", "reviewing", "--force"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "unknown --phase") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRmFinishedNoLocalMetadataNoops(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm", "--finished", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rm --finished: %v\nstderr=%s", err, stderr.String())
	}
	if got := strings.TrimSpace(out.String()); got != "(nothing to remove)" {
		t.Fatalf("stdout = %q, want nothing to remove", out.String())
	}
}

func TestRmAllNoLocalMetadataNoops(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm", "--all", "--force", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rm --all: %v\nstderr=%s", err, stderr.String())
	}
	if got := strings.TrimSpace(out.String()); got != "[]" {
		t.Fatalf("stdout = %q, want empty JSON array", out.String())
	}
}

func TestRmStatusUsesLocalMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	stateDir := filepath.Join(teamDir, "state", "manager")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm", "--status", "stopped", "--force", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rm --status stopped local metadata: %v\nstderr=%s", err, stderr.String())
	}
	var rows []instanceRmResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode rm json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "manager" || !rows[0].Removed || !rows[0].DaemonRemoved || !rows[0].StateRemoved {
		t.Fatalf("rows = %+v, want manager fully removed", rows)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("state dir should be gone, stat err=%v", err)
	}
	if _, err := daemon.ReadMetadata(root, "manager"); !os.IsNotExist(err) {
		t.Fatalf("metadata should be gone, err=%v", err)
	}
}

func TestRmPhaseUsesLocalStatusWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, item := range []struct {
		name  string
		phase string
	}{
		{name: "done", phase: "done"},
		{name: "idle", phase: "idle"},
	} {
		stateDir := filepath.Join(teamDir, "state", item.name)
		writeStatus(t, stateDir, "[status]\nphase = \""+item.phase+"\"\ndescription = \"fixture\"\n", time.Time{})
		if err := daemon.WriteMetadata(root, &daemon.Metadata{
			Instance: item.name,
			Agent:    "manager",
			Status:   daemon.StatusStopped,
		}); err != nil {
			t.Fatalf("write metadata %s: %v", item.name, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm", "--phase", "done", "--force", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rm --phase done local metadata: %v\nstderr=%s", err, stderr.String())
	}
	var rows []instanceRmResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode rm --phase json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "done" || !rows[0].Removed || !rows[0].DaemonRemoved || !rows[0].StateRemoved {
		t.Fatalf("rows = %+v, want done fully removed", rows)
	}
	if _, err := os.Stat(filepath.Join(teamDir, "state", "done")); !os.IsNotExist(err) {
		t.Fatalf("done state dir should be gone, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(teamDir, "state", "idle")); err != nil {
		t.Fatalf("idle state dir should remain: %v", err)
	}
	if _, err := daemon.ReadMetadata(root, "done"); !os.IsNotExist(err) {
		t.Fatalf("done metadata should be gone, err=%v", err)
	}
	if _, err := daemon.ReadMetadata(root, "idle"); err != nil {
		t.Fatalf("idle metadata should remain: %v", err)
	}
}

func TestRmStaleUsesLocalStatusWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	old := time.Now().Add(-staleAfter - time.Minute)
	recent := time.Now()

	for _, item := range []struct {
		name  string
		agent string
		phase string
		mtime time.Time
	}{
		{name: "stale-manager", agent: "manager", phase: "implementing", mtime: old},
		{name: "recent-manager", agent: "manager", phase: "implementing", mtime: recent},
		{name: "idle-manager", agent: "manager", phase: "idle", mtime: old},
		{name: "stale-worker", agent: "worker", phase: "implementing", mtime: old},
	} {
		stateDir := filepath.Join(teamDir, "state", item.name)
		writeStatus(t, stateDir, "[status]\nphase = \""+item.phase+"\"\ndescription = \"fixture\"\n", item.mtime)
		if err := daemon.WriteMetadata(root, &daemon.Metadata{
			Instance: item.name,
			Agent:    item.agent,
			Status:   daemon.StatusStopped,
		}); err != nil {
			t.Fatalf("write metadata %s: %v", item.name, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm", "--stale", "--agent", "manager", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rm --stale --agent manager dry-run json: %v\nstderr=%s", err, stderr.String())
	}
	var rows []instanceRmResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode rm --stale json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "stale-manager" || !rows[0].DryRun || !rows[0].DaemonRemoved || !rows[0].StateRemoved {
		t.Fatalf("rows = %+v, want stale-manager dry-run removal preview", rows)
	}
	for _, name := range []string{"stale-manager", "recent-manager", "idle-manager", "stale-worker"} {
		if _, err := os.Stat(filepath.Join(teamDir, "state", name)); err != nil {
			t.Fatalf("%s state should remain after dry-run: %v", name, err)
		}
		if _, err := daemon.ReadMetadata(root, name); err != nil {
			t.Fatalf("%s metadata should remain after dry-run: %v", name, err)
		}
	}
}

func TestRmUnhealthyUsesLocalStatusWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	old := time.Now().Add(-staleAfter - time.Minute)
	recent := time.Now()

	for _, item := range []struct {
		name   string
		agent  string
		status daemon.Status
		phase  string
		mtime  time.Time
	}{
		{name: "crashed-manager", agent: "manager", status: daemon.StatusCrashed, phase: "idle", mtime: recent},
		{name: "stale-manager", agent: "manager", status: daemon.StatusRunning, phase: "implementing", mtime: old},
		{name: "fresh-manager", agent: "manager", status: daemon.StatusRunning, phase: "implementing", mtime: recent},
		{name: "stale-worker", agent: "worker", status: daemon.StatusRunning, phase: "implementing", mtime: old},
	} {
		stateDir := filepath.Join(teamDir, "state", item.name)
		writeStatus(t, stateDir, "[status]\nphase = \""+item.phase+"\"\ndescription = \"fixture\"\n", item.mtime)
		if err := daemon.WriteMetadata(root, &daemon.Metadata{
			Instance: item.name,
			Agent:    item.agent,
			Status:   item.status,
		}); err != nil {
			t.Fatalf("write metadata %s: %v", item.name, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm", "--unhealthy", "--agent", "manager", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rm --unhealthy --agent manager dry-run json: %v\nstderr=%s", err, stderr.String())
	}
	var rows []instanceRmResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode rm --unhealthy json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 2 || rows[0].Instance != "crashed-manager" || rows[1].Instance != "stale-manager" {
		t.Fatalf("rows = %+v, want crashed-manager and stale-manager", rows)
	}
	for _, row := range rows {
		if !row.DryRun || !row.DaemonRemoved || !row.StateRemoved {
			t.Fatalf("row = %+v, want dry-run state and metadata removal preview", row)
		}
	}
	for _, name := range []string{"crashed-manager", "stale-manager", "fresh-manager", "stale-worker"} {
		if _, err := os.Stat(filepath.Join(teamDir, "state", name)); err != nil {
			t.Fatalf("%s state should remain after dry-run: %v", name, err)
		}
		if _, err := daemon.ReadMetadata(root, name); err != nil {
			t.Fatalf("%s metadata should remain after dry-run: %v", name, err)
		}
	}
}

func TestRmLocalMetadataRefusesLiveRunningPID(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusRunning,
		PID:      os.Getpid(),
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"rm", "--all", "--force", "--json", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected live running local metadata removal to fail")
	}
	if !strings.Contains(stderr.String(), "appears to still be running") {
		t.Fatalf("stderr = %q, want live running refusal", stderr.String())
	}
}

func TestInstanceRm_AbortedWithoutConfirm(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	stateDir := filepath.Join(tmp, ".agent_team", "state", "keep")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(bytes.NewBufferString("\n")) // empty answer → abort
	cmd.SetArgs([]string{"instance", "rm", "keep", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("instance rm: %v", err)
	}
	if _, err := os.Stat(stateDir); err != nil {
		t.Errorf("state dir should still exist, got err=%v", err)
	}
	if !strings.Contains(out.String(), "(aborted)") {
		t.Errorf("missing (aborted): %s", out.String())
	}
}
