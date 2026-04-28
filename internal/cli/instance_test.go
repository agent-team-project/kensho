package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
