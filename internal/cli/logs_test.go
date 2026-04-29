package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestRun_NoDaemonFlagBypassesDaemonProbe lives here alongside the other
// daemon-aware CLI tests rather than in run_test.go.
func TestRun_NoDaemonFlagBypassesDaemonProbe(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cap, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run", "manager", "--target", tmp, "--prompt", "go", "--no-daemon"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(cap.args) == 0 {
		t.Errorf("execClaude was not invoked; --no-daemon should have routed direct")
	}
}

func TestLogs_NoDaemonReturnsClearError(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "any-instance", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected exit error")
	}
	combined := stderr.String() + out.String()
	if !strings.Contains(combined, "no daemon running") {
		t.Errorf("missing hint: %q", combined)
	}
	if !strings.Contains(combined, "agent-team daemon start") {
		t.Errorf("missing daemon start hint: %q", combined)
	}
}
