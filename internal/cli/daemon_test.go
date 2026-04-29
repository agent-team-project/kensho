package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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
	for _, want := range []string{"start", "stop", "status"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("daemon help missing %q: %s", want, out.String())
		}
	}
}

