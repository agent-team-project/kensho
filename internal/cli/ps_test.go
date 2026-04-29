package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if !strings.Contains(out, "INSTANCE") || !strings.Contains(out, "STATUS") {
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
	// No daemon → STATUS column shows the placeholder.
	if !strings.Contains(out, "—") {
		t.Errorf("STATUS column should be `—` without a daemon: %q", out)
	}
}
