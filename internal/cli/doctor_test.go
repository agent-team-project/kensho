package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctor_FailsOnEmptyLinearKeys(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	// Wipe the resolved Linear keys to simulate a freshly-init'd repo where
	// the user hasn't yet supplied real values.
	cfgPath := filepath.Join(tmp, ".agent_team", "config.toml")
	if err := os.WriteFile(cfgPath, []byte(`[team]
pm_tool = "linear"

[linear]
team_id = ""
ticket_prefix = ""
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error: empty Linear team_id/ticket_prefix")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Errorf("expected exit 1, got %v", err)
	}
	if !strings.Contains(errOut.String(), "[linear].team_id missing/empty") {
		t.Errorf("missing team_id complaint: %s", errOut.String())
	}
}

func TestDoctor_PassesWithFilledLinearKeys(t *testing.T) {
	tmp := t.TempDir()
	// initInto supplies linear.team_id and linear.ticket_prefix via --set, so
	// doctor should be happy out of the box.
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("doctor failed unexpectedly: %v\nstderr: %s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "agent-team doctor: OK") {
		t.Errorf("expected OK output, got: %s", out.String())
	}
}

func TestDoctor_NoTeamDir(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when .agent_team/ missing")
	}
	if !strings.Contains(errOut.String(), "not found — run `agent-team init` first") {
		t.Errorf("missing init hint: %s", errOut.String())
	}
}

func TestDoctor_BadTOML(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cfgPath := filepath.Join(tmp, ".agent_team", "config.toml")
	if err := os.WriteFile(cfgPath, []byte("not = valid = toml ===="), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error on invalid TOML")
	}
	if !strings.Contains(errOut.String(), "is not valid TOML") {
		t.Errorf("missing toml-error message: %s", errOut.String())
	}
}
