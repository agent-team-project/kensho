package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
)

func TestDaemonStatus_NotRunning(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "status", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out.String(), "not running") {
		t.Errorf("status output: got %q want 'not running'", out.String())
	}
}

func TestDaemonStatus_StalePidfile(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	// Pid 999_999_999 is dead — same trick as reconcile tests.
	if err := os.WriteFile(daemon.PidPath(teamDir), []byte("999999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "status", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out.String(), "not running") {
		t.Errorf("stale pidfile: got %q want 'not running'", out.String())
	}
}

func TestDaemonStatus_Running(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	// Write our own PID; the test process is guaranteed alive.
	pid := os.Getpid()
	if err := os.MkdirAll(daemon.DaemonRoot(teamDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon.PidPath(teamDir),
		[]byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "status", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out.String(), "running") || strings.Contains(out.String(), "not running") {
		t.Errorf("status output: got %q want 'running (pid=...)'", out.String())
	}
	wantPid := "pid=" + strconv.Itoa(pid)
	if !strings.Contains(out.String(), wantPid) {
		t.Errorf("status output missing %s: %q", wantPid, out.String())
	}
	if !strings.Contains(out.String(), "ready: no") || !strings.Contains(out.String(), "daemon socket not found") {
		t.Errorf("status output missing readiness detail: %q", out.String())
	}
}

func TestDaemonStatus_ReadyWithInstanceCount(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-daemon-status-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	mgr := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, time.Second))
	if _, err := mgr.Dispatch(daemon.DispatchInput{Agent: "manager", Name: "manager", Workspace: tmp}); err != nil {
		t.Fatal(err)
	}
	defer stopAndWaitForTest(t, mgr, "manager")
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "status", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, want := range []string{"agent-teamd: running", "ready: yes", "instances: 1", "daemon.sock"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("status output missing %q:\n%s", want, out.String())
		}
	}
}

func TestDaemonStatusJSON_NotRunning(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "status", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --json: %v", err)
	}
	var body daemonStatusJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode daemon status json: %v\nbody=%s", err, out.String())
	}
	if body.Running || body.PID != 0 {
		t.Fatalf("status json should report daemon down: %+v", body)
	}
	if body.Ready || body.SocketExists || body.StalePidfile || body.Instances != 0 {
		t.Fatalf("status json should report daemon down without readiness: %+v", body)
	}
	if body.Socket != daemon.SocketPath(body.TeamDir) {
		t.Fatalf("socket = %q, want %q", body.Socket, daemon.SocketPath(body.TeamDir))
	}
	for _, want := range []string{".agent_team", "daemon.pid", "agent-teamd.log"} {
		joined := body.TeamDir + body.Pidfile + body.Log
		if !strings.Contains(joined, want) {
			t.Fatalf("status json missing %q: %+v", want, body)
		}
	}
}

func TestDaemonStatusJSON_Running(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	pid := os.Getpid()
	if err := os.MkdirAll(daemon.DaemonRoot(teamDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon.PidPath(teamDir), []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "status", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --json: %v", err)
	}
	var body daemonStatusJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode daemon status json: %v\nbody=%s", err, out.String())
	}
	if !body.Running || body.Ready || body.PID != pid || body.SocketExists {
		t.Fatalf("status json should report daemon running pid %d: %+v", pid, body)
	}
	if !strings.Contains(body.Error, "socket") {
		t.Fatalf("status json should report missing socket: %+v", body)
	}
}

func TestDaemonStatusJSON_ReadyWithInstanceCount(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-daemon-status-json-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	mgr := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, time.Second))
	if _, err := mgr.Dispatch(daemon.DispatchInput{Agent: "manager", Name: "manager", Workspace: tmp}); err != nil {
		t.Fatal(err)
	}
	defer stopAndWaitForTest(t, mgr, "manager")
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "status", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --json: %v", err)
	}
	var body daemonStatusJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode daemon status json: %v\nbody=%s", err, out.String())
	}
	if !body.Running || !body.Ready || !body.SocketExists || body.PID != os.Getpid() || body.Instances != 1 || body.Error != "" {
		t.Fatalf("status json should report ready daemon with one instance: %+v", body)
	}
}

func TestDaemonAdoptInfersDeclaredAgentAndWritesMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"
`), 0o644); err != nil {
		t.Fatalf("write instances.toml: %v", err)
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "adopt", "manager", "--target", tmp, "--pid", strconv.Itoa(os.Getpid()), "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("daemon adopt: %v", err)
	}
	var result daemonAdoptResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode daemon adopt json: %v\nbody=%s", err, out.String())
	}
	if !result.Changed || result.Reconciled || result.Metadata == nil {
		t.Fatalf("adopt result = %+v", result)
	}
	if result.Metadata.Instance != "manager" || result.Metadata.Agent != "manager" || result.Metadata.Status != daemon.StatusRunning || !result.Metadata.Adopted {
		t.Fatalf("adopt metadata = %+v", result.Metadata)
	}
	for _, want := range []string{
		"agent-team inspect manager",
		"agent-team logs manager --follow",
		"agent-team resume-plan manager",
	} {
		if !containsString(result.Actions, want) {
			t.Fatalf("adopt actions = %+v, missing %q", result.Actions, want)
		}
	}
	absTmp, err := filepath.Abs(tmp)
	if err != nil {
		t.Fatalf("Abs(tmp): %v", err)
	}
	expectedWorkspace := absTmp
	if eval, err := filepath.EvalSymlinks(absTmp); err == nil {
		expectedWorkspace = eval
	}
	if result.Metadata.Workspace != expectedWorkspace || result.Metadata.Runtime != "claude" {
		t.Fatalf("adopt metadata workspace/runtime = %+v", result.Metadata)
	}
	disk, err := daemon.ReadMetadata(daemon.DaemonRoot(teamDir), "manager")
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if !disk.Adopted || disk.PID != os.Getpid() {
		t.Fatalf("disk metadata = %+v", disk)
	}
}

func TestDaemonAdoptDryRunDoesNotWriteMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "adopt", "external-worker", "--target", tmp, "--agent", "worker", "--pid", strconv.Itoa(os.Getpid()), "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("daemon adopt --dry-run: %v", err)
	}
	var result daemonAdoptResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode daemon adopt dry-run json: %v\nbody=%s", err, out.String())
	}
	if !result.DryRun || !result.Changed || result.Metadata == nil || !result.Metadata.Adopted {
		t.Fatalf("dry-run result = %+v", result)
	}
	if _, err := daemon.ReadMetadata(daemon.DaemonRoot(filepath.Join(tmp, ".agent_team")), "external-worker"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("metadata should not exist after dry-run: %v", err)
	}
}

func TestDaemonAdoptDryRunTextPrintsFollowUpActions(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "adopt", "external-worker", "--target", tmp, "--agent", "worker", "--pid", strconv.Itoa(os.Getpid()), "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("daemon adopt --dry-run text: %v", err)
	}
	for _, want := range []string{
		"would adopt external-worker",
		"after apply:",
		"agent-team inspect external-worker",
		"agent-team logs external-worker --follow",
		"agent-team resume-plan external-worker",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("adopt dry-run text missing %q:\n%s", want, out.String())
		}
	}
}

func TestDaemonAdoptCommandsPrintsOnlyFollowUpActions(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "adopt", "external-worker", "--target", tmp, "--agent", "worker", "--pid", strconv.Itoa(os.Getpid()), "--dry-run", "--commands"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("daemon adopt --commands: %v", err)
	}
	want := strings.Join([]string{
		"agent-team inspect external-worker",
		"agent-team logs external-worker --follow",
		"agent-team resume-plan external-worker",
	}, "\n") + "\n"
	if got := out.String(); got != want {
		t.Fatalf("adopt commands output = %q, want %q", got, want)
	}
}

func TestDaemonAdoptRejectsCommandsWithJSONOrFormat(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "json",
			args: []string{"daemon", "adopt", "external-worker", "--target", t.TempDir(), "--agent", "worker", "--pid", strconv.Itoa(os.Getpid()), "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "format",
			args: []string{"daemon", "adopt", "external-worker", "--target", t.TempDir(), "--agent", "worker", "--pid", strconv.Itoa(os.Getpid()), "--dry-run", "--commands", "--format", "{{.Action}}"},
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
				t.Fatalf("daemon adopt accepted %s conflict: stdout=%s", tc.name, out.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestRuntimeAdoptDryRunDoesNotWriteMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"runtime", "adopt", "external-worker", "--target", tmp, "--agent", "worker", "--pid", strconv.Itoa(os.Getpid()), "--runtime", "codex", "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime adopt --dry-run: %v", err)
	}
	var result daemonAdoptResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode runtime adopt dry-run json: %v\nbody=%s", err, out.String())
	}
	if !result.DryRun || !result.Changed || result.Metadata == nil || !result.Metadata.Adopted {
		t.Fatalf("dry-run result = %+v", result)
	}
	if result.Metadata.Instance != "external-worker" || result.Metadata.Agent != "worker" {
		t.Fatalf("runtime adopt metadata = %+v", result.Metadata)
	}
	if result.Metadata.Runtime != "codex" || !containsString(result.Actions, "agent-team logs external-worker --last-message") {
		t.Fatalf("runtime adopt codex actions = %+v metadata=%+v", result.Actions, result.Metadata)
	}
	if _, err := daemon.ReadMetadata(daemon.DaemonRoot(filepath.Join(tmp, ".agent_team")), "external-worker"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("metadata should not exist after dry-run: %v", err)
	}
}

func TestAdoptShortcutDryRunDoesNotWriteMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"adopt", "external-worker", "--repo", tmp, "--agent", "worker", "--pid", strconv.Itoa(os.Getpid()), "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("adopt shortcut --dry-run: %v", err)
	}
	var result daemonAdoptResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode adopt shortcut dry-run json: %v\nbody=%s", err, out.String())
	}
	if !result.DryRun || !result.Changed || result.Metadata == nil || !result.Metadata.Adopted {
		t.Fatalf("dry-run result = %+v", result)
	}
	if result.Metadata.Instance != "external-worker" || result.Metadata.Agent != "worker" {
		t.Fatalf("adopt shortcut metadata = %+v", result.Metadata)
	}
	if _, err := daemon.ReadMetadata(daemon.DaemonRoot(filepath.Join(tmp, ".agent_team")), "external-worker"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("metadata should not exist after dry-run: %v", err)
	}
}

func TestAdoptShortcutReadsPIDFile(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	pidPath := filepath.Join(tmp, "runtime.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"adopt", "external-worker", "--repo", tmp, "--agent", "worker", "--pid-file", pidPath, "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("adopt shortcut --pid-file: %v", err)
	}
	var result daemonAdoptResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode adopt shortcut pid-file json: %v\nbody=%s", err, out.String())
	}
	if result.Metadata == nil || result.Metadata.PID != os.Getpid() {
		t.Fatalf("adopt shortcut metadata = %+v, want pid %d", result.Metadata, os.Getpid())
	}
	if _, err := daemon.ReadMetadata(daemon.DaemonRoot(filepath.Join(tmp, ".agent_team")), "external-worker"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("metadata should not exist after dry-run: %v", err)
	}
}

func TestAdoptShortcutRejectsPIDAndPIDFile(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	pidPath := filepath.Join(tmp, "runtime.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"adopt", "external-worker", "--repo", tmp, "--agent", "worker", "--pid", strconv.Itoa(os.Getpid()), "--pid-file", pidPath, "--dry-run"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("adopt shortcut accepted --pid and --pid-file")
	}
	if !strings.Contains(stderr.String(), "--pid and --pid-file cannot be combined") {
		t.Fatalf("stderr = %q, want pid conflict", stderr.String())
	}
}

func TestDaemonAdoptUpdatesOwningJob(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	worktree := filepath.Join(tmp, "worktrees", "squ-66")
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-66",
		Ticket:    "SQU-66",
		Target:    "worker",
		Status:    job.StatusQueued,
		Branch:    "squ-66-adopted",
		Worktree:  worktree,
		PR:        "https://github.com/example/repo/pull/66",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"daemon", "adopt", "worker-squ-66",
		"--target", tmp,
		"--pid", strconv.Itoa(os.Getpid()),
		"--job", "squ-66",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("daemon adopt job: %v", err)
	}
	var result daemonAdoptResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode daemon adopt job json: %v\nbody=%s", err, out.String())
	}
	if result.Metadata == nil || result.Metadata.Job != "squ-66" || result.Job == nil || !result.JobChanged {
		t.Fatalf("adopt result = %+v", result)
	}
	if result.Metadata.Agent != "worker" || result.Metadata.Ticket != "SQU-66" || result.Metadata.Branch != "squ-66-adopted" || result.Metadata.PR != "https://github.com/example/repo/pull/66" || result.Metadata.Workspace != worktree {
		t.Fatalf("metadata defaults = %+v", result.Metadata)
	}
	if result.Job.Status != job.StatusRunning || result.Job.Instance != "worker-squ-66" || result.Job.Branch != "squ-66-adopted" || result.Job.PR != "https://github.com/example/repo/pull/66" || result.Job.LastEvent != "adopted" {
		t.Fatalf("adopted job result = %+v", result.Job)
	}
	for _, want := range []string{
		"agent-team inspect worker-squ-66",
		"agent-team logs worker-squ-66 --follow",
		"agent-team resume-plan worker-squ-66",
		"agent-team job show squ-66",
		"agent-team job logs squ-66 --follow",
		"agent-team job resume-plan squ-66",
	} {
		if !containsString(result.Actions, want) {
			t.Fatalf("job adopt actions = %+v, missing %q", result.Actions, want)
		}
	}
	updated, err := job.Read(teamDir, "squ-66")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.Status != job.StatusRunning || updated.Instance != "worker-squ-66" || updated.Branch != "squ-66-adopted" || updated.PR != "https://github.com/example/repo/pull/66" || updated.LastEvent != "adopted" {
		t.Fatalf("updated job = %+v", updated)
	}
	events, err := job.ListEvents(teamDir, "squ-66")
	if err != nil {
		t.Fatalf("job events: %v", err)
	}
	if len(events) != 1 || events[0].Type != "adopted" || events[0].Data["instance"] != "worker-squ-66" {
		t.Fatalf("events = %+v", events)
	}
}

func TestDaemonAdoptUpdatesOwningPipelineStep(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-166",
		Ticket:    "SQU-166",
		Target:    "manager",
		Status:    job.StatusRunning,
		Pipeline:  "ticket_to_pr",
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "triage", Target: "ticket-manager", Status: job.StatusDone, StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
			{ID: "implement", Target: "worker", Status: job.StatusRunning, After: []string{"triage"}, StartedAt: now.Add(-30 * time.Minute)},
		},
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"daemon", "adopt", "worker-squ-166-implement",
		"--target", tmp,
		"--pid", strconv.Itoa(os.Getpid()),
		"--job", "squ-166",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("daemon adopt pipeline step: %v", err)
	}
	var result daemonAdoptResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode daemon adopt pipeline json: %v\nbody=%s", err, out.String())
	}
	if result.Metadata == nil || result.Metadata.Agent != "worker" || result.Metadata.Instance != "worker-squ-166-implement" {
		t.Fatalf("metadata = %+v", result.Metadata)
	}
	if result.Job == nil || !result.JobChanged || result.Job.Instance != "worker-squ-166-implement" || len(result.Job.Steps) != 2 {
		t.Fatalf("adopt result = %+v", result)
	}
	if result.Job.Steps[1].Status != job.StatusRunning || result.Job.Steps[1].Instance != "worker-squ-166-implement" {
		t.Fatalf("adopted step = %+v", result.Job.Steps[1])
	}
	updated, err := job.Read(teamDir, "squ-166")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.Instance != "worker-squ-166-implement" || updated.Steps[1].Instance != "worker-squ-166-implement" || updated.LastEvent != "adopted" {
		t.Fatalf("updated job = %+v", updated)
	}
	events, err := job.ListEvents(teamDir, "squ-166")
	if err != nil {
		t.Fatalf("job events: %v", err)
	}
	if len(events) != 1 || events[0].Data["step"] != "implement" {
		t.Fatalf("events = %+v", events)
	}
}

func TestDaemonAdoptUsesExplicitPipelineStep(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-167",
		Ticket:    "SQU-167",
		Target:    "worker",
		Status:    job.StatusRunning,
		Pipeline:  "ticket_to_pr",
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusRunning, StartedAt: now.Add(-30 * time.Minute)},
			{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}},
		},
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"daemon", "adopt", "manager-squ-167-review",
		"--target", tmp,
		"--pid", strconv.Itoa(os.Getpid()),
		"--job", "squ-167",
		"--step", "review",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("daemon adopt explicit step: %v", err)
	}
	var result daemonAdoptResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode daemon adopt explicit step json: %v\nbody=%s", err, out.String())
	}
	if result.Metadata == nil || result.Metadata.Agent != "manager" || result.Metadata.Instance != "manager-squ-167-review" {
		t.Fatalf("metadata = %+v", result.Metadata)
	}
	if result.Job == nil || !result.JobChanged || result.Job.Instance != "manager-squ-167-review" || len(result.Job.Steps) != 2 {
		t.Fatalf("adopt result = %+v", result)
	}
	if result.Job.Steps[1].Status != job.StatusRunning || result.Job.Steps[1].Instance != "manager-squ-167-review" {
		t.Fatalf("adopted step = %+v", result.Job.Steps[1])
	}
	events, err := job.ListEvents(teamDir, "squ-167")
	if err != nil {
		t.Fatalf("job events: %v", err)
	}
	if len(events) != 1 || events[0].Data["step"] != "review" {
		t.Fatalf("events = %+v", events)
	}
}

func TestDaemonAdoptDryRunPreviewsOwningJobUpdate(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-67",
		Ticket:    "SQU-67",
		Target:    "worker",
		Status:    job.StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"daemon", "adopt", "worker-squ-67",
		"--target", tmp,
		"--agent", "worker",
		"--pid", strconv.Itoa(os.Getpid()),
		"--job", "squ-67",
		"--dry-run",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("daemon adopt dry-run job: %v", err)
	}
	var result daemonAdoptResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode daemon adopt dry-run job json: %v\nbody=%s", err, out.String())
	}
	if !result.DryRun || result.Job == nil || !result.JobChanged || result.Job.Status != job.StatusRunning || result.Job.Instance != "worker-squ-67" {
		t.Fatalf("dry-run result = %+v", result)
	}
	unchanged, err := job.Read(teamDir, "squ-67")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if unchanged.Status != job.StatusQueued || unchanged.Instance != "" || unchanged.LastEvent != "" {
		t.Fatalf("dry-run mutated job = %+v", unchanged)
	}
	if _, err := daemon.ReadMetadata(daemon.DaemonRoot(teamDir), "worker-squ-67"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("metadata should not exist after dry-run: %v", err)
	}
}

func TestDaemonAdoptFormatPrintsRow(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"daemon", "adopt", "worker-one",
		"--target", tmp,
		"--agent", "worker",
		"--pid", strconv.Itoa(os.Getpid()),
		"--runtime", "codex",
		"--format", "{{.Metadata.Instance}} {{.Metadata.Runtime}} {{.Metadata.PID}} {{.Changed}}",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("daemon adopt --format: %v", err)
	}
	want := "worker-one codex " + strconv.Itoa(os.Getpid()) + " true\n"
	if out.String() != want {
		t.Fatalf("format output = %q, want %q", out.String(), want)
	}
}

func TestDaemonAdoptRequiresAgentForUndeclaredInstance(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	errOut := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"daemon", "adopt", "unknown", "--target", tmp, "--pid", strconv.Itoa(os.Getpid())})
	if err := cmd.Execute(); err == nil {
		t.Fatal("daemon adopt without inferred agent succeeded")
	}
	if !strings.Contains(errOut.String(), "--agent is required") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestDaemonStatusQuietNotReadyExitsOneWithoutOutput(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"daemon", "status", "--quiet", "--target", tmp})
	err := cmd.Execute()
	var code ExitCode
	if err == nil || !errors.As(err, &code) || code != 1 {
		t.Fatalf("err = %v, want quiet status exit 1", err)
	}
	if out.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("quiet status should not write output, stdout=%q stderr=%q", out.String(), stderr.String())
	}
}

func TestDaemonStatusQuietReadyNoOutput(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-daemon-status-quiet-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	mgr := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, time.Second))
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"daemon", "status", "--quiet", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("daemon status --quiet: %v", err)
	}
	if out.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("quiet status should not write output, stdout=%q stderr=%q", out.String(), stderr.String())
	}
}

func TestDaemonStatusQuietRejectsJSON(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"daemon", "status", "--quiet", "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected quiet/json validation error")
	}
	if !strings.Contains(stderr.String(), "choose one of --quiet or --json") {
		t.Fatalf("stderr = %q, want quiet/json validation", stderr.String())
	}
}

func TestDaemonStatusFormatNotRunning(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "status", "--format", "{{.Running}}:{{.Ready}}:{{.Instances}}:{{.SocketExists}}", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --format: %v", err)
	}
	if got, want := out.String(), "false:false:0:false\n"; got != want {
		t.Fatalf("status --format output = %q, want %q", got, want)
	}
}

func TestDaemonStatusWaitFormatTimeout(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "status", "--wait", "--timeout", "5ms", "--interval", "1ms", "--format", "{{.Running}}:{{.Ready}}:{{.Error}}", "--target", tmp})
	err := cmd.Execute()
	var code ExitCode
	if err == nil || !errors.As(err, &code) || code != 1 {
		t.Fatalf("err = %v, want exit 1 timeout", err)
	}
	if got := out.String(); !strings.Contains(got, "false:false:timed out waiting for daemon readiness") {
		t.Fatalf("status --wait --format output = %q, want timeout snapshot", got)
	}
}

func TestDaemonStatusFormatRejectsConflictingModes(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"daemon", "status", "--format", "{{.Ready}}", "--json"}, "--format cannot be combined"},
		{[]string{"daemon", "status", "--format", "{{.Ready}}", "--quiet"}, "--format cannot be combined"},
		{[]string{"daemon", "status", "--format", "{{"}, "invalid --format template"},
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

func TestDaemonStatusWaitJSONReady(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-daemon-status-wait-json-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	mgr := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, time.Second))
	if _, err := mgr.Dispatch(daemon.DispatchInput{Agent: "manager", Name: "manager", Workspace: tmp}); err != nil {
		t.Fatal(err)
	}
	defer stopAndWaitForTest(t, mgr, "manager")
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "status", "--wait", "--timeout", "1s", "--interval", "10ms", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --wait --json: %v", err)
	}
	var body daemonStatusJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode daemon status json: %v\nbody=%s", err, out.String())
	}
	if !body.Running || !body.Ready || body.Instances != 1 || body.Error != "" {
		t.Fatalf("status --wait json should report ready daemon: %+v", body)
	}
}

func TestDaemonStatusWaitJSONTimeout(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "status", "--wait", "--timeout", "5ms", "--interval", "1ms", "--json", "--target", tmp})
	err := cmd.Execute()
	var code ExitCode
	if err == nil || !errors.As(err, &code) || code != 1 {
		t.Fatalf("err = %v, want exit 1 timeout", err)
	}
	var body daemonStatusJSON
	if decErr := json.Unmarshal(out.Bytes(), &body); decErr != nil {
		t.Fatalf("decode daemon status json: %v\nbody=%s", decErr, out.String())
	}
	if body.Ready || body.Running || !strings.Contains(body.Error, "timed out waiting for daemon readiness") {
		t.Fatalf("status --wait timeout json = %+v", body)
	}
}

func TestDaemonStatusWaitDownJSONAlreadyStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "status", "--wait", "--down", "--timeout", "1s", "--interval", "1ms", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --wait --down --json: %v", err)
	}
	var body daemonStatusJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode daemon status json: %v\nbody=%s", err, out.String())
	}
	if body.Running || body.Ready || body.Error != "" {
		t.Fatalf("status --wait --down json should report stopped daemon: %+v", body)
	}
}

func TestDaemonStatusWaitDownJSONTimeout(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	pid := os.Getpid()
	if err := os.MkdirAll(daemon.DaemonRoot(teamDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon.PidPath(teamDir), []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "status", "--wait", "--down", "--timeout", "5ms", "--interval", "1ms", "--json", "--target", tmp})
	err := cmd.Execute()
	var code ExitCode
	if err == nil || !errors.As(err, &code) || code != 1 {
		t.Fatalf("err = %v, want exit 1 timeout", err)
	}
	var body daemonStatusJSON
	if decErr := json.Unmarshal(out.Bytes(), &body); decErr != nil {
		t.Fatalf("decode daemon status json: %v\nbody=%s", decErr, out.String())
	}
	if !body.Running || !strings.Contains(body.Error, "timed out waiting for daemon shutdown") {
		t.Fatalf("status --wait --down timeout json = %+v", body)
	}
}

func TestDaemonStatusWaitRejectsZeroInterval(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"daemon", "status", "--wait", "--interval", "0"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected interval validation error")
	}
	if !strings.Contains(stderr.String(), "--interval must be > 0 with --wait") {
		t.Fatalf("stderr = %q, want interval validation", stderr.String())
	}
}

func TestDaemonStatusDownRequiresWait(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"daemon", "status", "--down"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected down validation error")
	}
	if !strings.Contains(stderr.String(), "--down requires --wait") {
		t.Fatalf("stderr = %q, want down validation", stderr.String())
	}
}

func TestDaemonStartJSONAlreadyRunning(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	pid := os.Getpid()
	if err := os.MkdirAll(daemon.DaemonRoot(teamDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon.PidPath(teamDir), []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "start", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("daemon start --json: %v", err)
	}
	var body daemonLifecycleJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode start json: %v\nbody=%s", err, out.String())
	}
	if body.Action != "start" || body.Changed || !body.AlreadyRunning || body.PID != pid {
		t.Fatalf("start json should report already-running daemon: %+v", body)
	}
	if !body.Status.Running || body.Status.PID != pid {
		t.Fatalf("start json should include running status: %+v", body.Status)
	}
}

func TestDaemonStartFormatAlreadyRunning(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	pid := os.Getpid()
	if err := os.MkdirAll(daemon.DaemonRoot(teamDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon.PidPath(teamDir), []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "start", "--format", "{{.Action}}:{{.Changed}}:{{.AlreadyRunning}}:{{.PID}}", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("daemon start --format: %v", err)
	}
	want := "start:false:true:" + strconv.Itoa(pid) + "\n"
	if got := out.String(); got != want {
		t.Fatalf("daemon start --format output = %q, want %q", got, want)
	}
}

func TestDaemonStartQuietAlreadyRunning(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.MkdirAll(daemon.DaemonRoot(teamDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon.PidPath(teamDir), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"daemon", "start", "--quiet", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("daemon start --quiet: %v\nstderr=%s", err, stderr.String())
	}
	if out.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("quiet start should not write output, stdout=%q stderr=%q", out.String(), stderr.String())
	}
}

func TestDaemonStartJSONRejectsForeground(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"daemon", "start", "--json", "--detach=false"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected foreground json validation error")
	}
	if !strings.Contains(stderr.String(), "--json cannot be combined with --detach=false") {
		t.Fatalf("stderr = %q, want foreground json validation", stderr.String())
	}
}

func TestDaemonLifecycleFormatRejectsConflictingModes(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"daemon", "start", "--quiet", "--detach=false"}, "--quiet cannot be combined with --detach=false"},
		{[]string{"daemon", "start", "--quiet", "--json"}, "choose one of --quiet or --json"},
		{[]string{"daemon", "start", "--format", "{{.Action}}", "--quiet"}, "--format cannot be combined with --quiet"},
		{[]string{"daemon", "start", "--format", "{{.Action}}", "--json"}, "--format cannot be combined with --json"},
		{[]string{"daemon", "start", "--format", "{{.Action}}", "--detach=false"}, "--format cannot be combined with --detach=false"},
		{[]string{"daemon", "start", "--format", "{{"}, "invalid --format template"},
		{[]string{"daemon", "stop", "--quiet", "--json"}, "choose one of --quiet or --json"},
		{[]string{"daemon", "stop", "--format", "{{.Action}}", "--quiet"}, "--format cannot be combined with --quiet"},
		{[]string{"daemon", "stop", "--format", "{{.Action}}", "--json"}, "--format cannot be combined with --json"},
		{[]string{"daemon", "stop", "--format", "{{"}, "invalid --format template"},
		{[]string{"daemon", "restart", "--quiet", "--detach=false"}, "--quiet cannot be combined with --detach=false"},
		{[]string{"daemon", "restart", "--quiet", "--json"}, "choose one of --quiet or --json"},
		{[]string{"daemon", "restart", "--format", "{{.Action}}", "--quiet"}, "--format cannot be combined with --quiet"},
		{[]string{"daemon", "restart", "--format", "{{.Action}}", "--json"}, "--format cannot be combined with --json"},
		{[]string{"daemon", "restart", "--format", "{{.Action}}", "--detach=false"}, "--format cannot be combined with --detach=false"},
		{[]string{"daemon", "restart", "--format", "{{"}, "invalid --format template"},
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

func TestDaemonStartRejectsNegativeReadyTimeout(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"daemon", "start", "--ready-timeout", "-1s"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected ready-timeout validation error")
	}
	if !strings.Contains(stderr.String(), "--ready-timeout must be >= 0") {
		t.Fatalf("stderr = %q, want ready-timeout validation", stderr.String())
	}
}

func TestDaemonStopJSONNotRunning(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "stop", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("daemon stop --json: %v", err)
	}
	var body daemonLifecycleJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode stop json: %v\nbody=%s", err, out.String())
	}
	if body.Action != "stop" || body.Changed || body.Stopped || body.Status.Running || body.Message != "not running" {
		t.Fatalf("stop json should report daemon not running: %+v", body)
	}
}

func TestDaemonStopFormatNotRunning(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "stop", "--format", "{{.Action}}:{{.Changed}}:{{.Message}}:{{.Status.Running}}", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("daemon stop --format: %v", err)
	}
	if got, want := out.String(), "stop:false:not running:false\n"; got != want {
		t.Fatalf("daemon stop --format output = %q, want %q", got, want)
	}
}

func TestDaemonStopJSONStalePidfile(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.MkdirAll(daemon.DaemonRoot(teamDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon.PidPath(teamDir), []byte("999999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "stop", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("daemon stop --json stale pidfile: %v", err)
	}
	var body daemonLifecycleJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode stop json: %v\nbody=%s", err, out.String())
	}
	if body.Action != "stop" || !body.Changed || !body.StalePidfileRemoved || body.PreviousPID != 999999999 {
		t.Fatalf("stop json should report stale pidfile cleanup: %+v", body)
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); err == nil {
		t.Fatalf("stale pidfile still present after stop --json")
	}
}

func TestRenderDaemonRestartFormat(t *testing.T) {
	tmpl, err := parseDaemonRestartFormat("{{.Action}}:{{.Changed}}:{{.Stop.Message}}:{{.Start.Message}}:{{.Status.Ready}}")
	if err != nil {
		t.Fatalf("parse restart format: %v", err)
	}
	var out bytes.Buffer
	err = renderDaemonRestartFormat(&out, daemonRestartJSON{
		Action:  "restart",
		Changed: true,
		Stop:    daemonLifecycleJSON{Action: "stop", Message: "stopped"},
		Start:   daemonLifecycleJSON{Action: "start", Message: "started"},
		Status:  daemonStatusJSON{Ready: true},
	}, tmpl)
	if err != nil {
		t.Fatalf("render restart format: %v", err)
	}
	if got, want := out.String(), "restart:true:stopped:started:true\n"; got != want {
		t.Fatalf("restart format = %q, want %q", got, want)
	}
}

func TestDaemonRestartJSONRejectsForeground(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"daemon", "restart", "--json", "--detach=false"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected restart foreground json validation error")
	}
	if !strings.Contains(stderr.String(), "--json cannot be combined with --detach=false") {
		t.Fatalf("stderr = %q, want foreground json validation", stderr.String())
	}
}

func TestDaemonRestartRejectsNegativeReadyTimeout(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"daemon", "restart", "--ready-timeout", "-1s"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected restart ready-timeout validation error")
	}
	if !strings.Contains(stderr.String(), "--ready-timeout must be >= 0") {
		t.Fatalf("stderr = %q, want ready-timeout validation", stderr.String())
	}
}

func TestDaemonStop_NotRunning(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "stop", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !strings.Contains(out.String(), "not running") {
		t.Errorf("stop output: %q", out.String())
	}
}

func TestDaemonStopQuietNotRunning(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"daemon", "stop", "--quiet", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("daemon stop --quiet: %v\nstderr=%s", err, stderr.String())
	}
	if out.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("quiet stop should not write output, stdout=%q stderr=%q", out.String(), stderr.String())
	}
}

func TestDaemonStop_StalePidfileCleaned(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.MkdirAll(daemon.DaemonRoot(teamDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon.PidPath(teamDir), []byte("999999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "stop", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	// Stale pidfile should be removed by stop's cleanup path.
	if _, err := os.Stat(daemon.PidPath(teamDir)); err == nil {
		t.Errorf("stale pidfile still present after stop")
	}
}

func TestDaemonStopNegativeTimeoutFailsFast(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"daemon", "stop", "--timeout", "-1s"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected timeout validation error")
	}
	if !strings.Contains(stderr.String(), "--timeout must be >= 0") {
		t.Fatalf("stderr = %q, want timeout validation", stderr.String())
	}
}

func TestDaemonRestartNegativeTimeoutFailsFast(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"daemon", "restart", "--timeout", "-1s"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected timeout validation error")
	}
	if !strings.Contains(stderr.String(), "--timeout must be >= 0") {
		t.Fatalf("stderr = %q, want timeout validation", stderr.String())
	}
}

func TestDaemonStartDetachDefault(t *testing.T) {
	cmd := newDaemonStartCmd()
	flag := cmd.Flags().Lookup("detach")
	if flag == nil {
		t.Fatalf("detach flag missing")
	}
	if flag.DefValue != "true" {
		t.Fatalf("detach default = %q, want true", flag.DefValue)
	}
	timeoutFlag := cmd.Flags().Lookup("ready-timeout")
	if timeoutFlag == nil {
		t.Fatalf("ready-timeout flag missing")
	}
	if timeoutFlag.DefValue != defaultDaemonReadyTimeout.String() {
		t.Fatalf("ready-timeout default = %q, want %s", timeoutFlag.DefValue, defaultDaemonReadyTimeout)
	}
}

func TestDaemonReconcileRequiresRunningDaemon(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"daemon", "reconcile", "--target", tmp})
	err := cmd.Execute()
	var code ExitCode
	if err == nil || !strings.Contains(stderr.String(), "daemon is not running") {
		t.Fatalf("err = %v stderr = %q, want daemon not running", err, stderr.String())
	}
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
}

func TestDaemonLogsReadsLocalLogWithoutDaemon(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.MkdirAll(daemon.DaemonRoot(teamDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon.LogPath(teamDir), []byte("first\nlast\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "logs", "--tail", "1", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("daemon logs: %v", err)
	}
	if got := out.String(); got != "last\n" {
		t.Fatalf("daemon logs output = %q, want last line", got)
	}
}

func TestDaemonLogsTailAllReadsWholeLog(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.MkdirAll(daemon.DaemonRoot(teamDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon.LogPath(teamDir), []byte("first\nlast\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "logs", "--tail", "all", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("daemon logs --tail all: %v", err)
	}
	if got := out.String(); got != "first\nlast\n" {
		t.Fatalf("daemon logs output = %q, want whole log", got)
	}
}

func TestDaemonLogsGrep(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.MkdirAll(daemon.DaemonRoot(teamDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon.LogPath(teamDir), []byte("info boot\nerror failed\ninfo done\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "logs", "--grep", "error", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("daemon logs --grep: %v", err)
	}
	if got, want := out.String(), "error failed\n"; got != want {
		t.Fatalf("daemon logs --grep output = %q, want %q", got, want)
	}
}

func TestDaemonLogsSinceFiltersOldLog(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.MkdirAll(daemon.DaemonRoot(teamDir), 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := daemon.LogPath(teamDir)
	if err := os.WriteFile(logPath, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldAt := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(logPath, oldAt, oldAt); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"daemon", "logs", "--since", "1h", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("daemon logs --since: %v", err)
	}
	if got, want := out.String(), "(no matching logs)\n"; got != want {
		t.Fatalf("daemon logs --since output = %q, want %q", got, want)
	}
}

func TestDaemonLogsNegativeTailFailsFast(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"daemon", "logs", "--tail", "-1"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected tail validation error")
	}
	if !strings.Contains(stderr.String(), "--tail must be >= 0") {
		t.Fatalf("stderr = %q, want tail validation", stderr.String())
	}
}

func TestRenderDaemonReconcile(t *testing.T) {
	resp := &daemonReconcileResponse{
		Changed: 1,
		Instances: []*daemon.Metadata{
			{Instance: "manager", Status: daemon.StatusExited},
		},
		Changes: []daemonReconcileChange{
			{Instance: "manager", Agent: "manager", Before: daemon.StatusRunning, After: daemon.StatusExited, PID: 999999},
		},
	}
	out := &bytes.Buffer{}
	if err := renderDaemonReconcile(out, resp); err != nil {
		t.Fatalf("renderDaemonReconcile: %v", err)
	}
	body := out.String()
	for _, want := range []string{"reconciled 1 instances (1 changed)", "manager", "running -> exited", "999999"} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q:\n%s", want, body)
		}
	}
}

func TestRenderDaemonReconcileNoChanges(t *testing.T) {
	out := &bytes.Buffer{}
	if err := renderDaemonReconcile(out, &daemonReconcileResponse{}); err != nil {
		t.Fatalf("renderDaemonReconcile: %v", err)
	}
	if !strings.Contains(out.String(), "no status changes") {
		t.Fatalf("output = %q, want no status changes", out.String())
	}
}

func TestRenderDaemonReconcileFormat(t *testing.T) {
	tmpl, err := parseDaemonReconcileFormat("{{.Reconciled}}:{{.Changed}}:{{len .Instances}}:{{range .Changes}}{{.Instance}}>{{.After}}{{end}}")
	if err != nil {
		t.Fatalf("parse reconcile format: %v", err)
	}
	resp := &daemonReconcileResponse{
		Reconciled: true,
		Changed:    1,
		Instances: []*daemon.Metadata{
			{Instance: "manager", Status: daemon.StatusExited},
		},
		Changes: []daemonReconcileChange{
			{Instance: "manager", Agent: "manager", Before: daemon.StatusRunning, After: daemon.StatusExited, PID: 999999},
		},
	}
	var out bytes.Buffer
	if err := renderDaemonReconcileFormat(&out, resp, tmpl); err != nil {
		t.Fatalf("render reconcile format: %v", err)
	}
	if got, want := out.String(), "true:1:1:manager>exited\n"; got != want {
		t.Fatalf("reconcile format = %q, want %q", got, want)
	}
}

func TestDaemonReconcileFormatRejectsConflictingModes(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"daemon", "reconcile", "--format", "{{.Changed}}", "--json"}, "--format cannot be combined with --json"},
		{[]string{"daemon", "reconcile", "--format", "{{"}, "invalid --format template"},
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

func TestDaemonHelp(t *testing.T) {
	// Smoke: the command tree wires up cleanly.
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"daemon", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("daemon --help: %v", err)
	}
	for _, want := range []string{"start", "stop", "restart", "reconcile", "status", "logs"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("daemon help missing %q: %s", want, out.String())
		}
	}
}
