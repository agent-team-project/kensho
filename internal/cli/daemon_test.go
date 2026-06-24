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
