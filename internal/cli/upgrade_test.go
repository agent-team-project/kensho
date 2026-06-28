package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradeCheck_BundledUpToDate(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"upgrade", "--check", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade --check: %v\nstderr: %s", err, errOut.String())
	}
	body := out.String()
	for _, want := range []string{
		"Locked ref: bundled",
		"Target ref: bundled",
		"Locked hash: sha256:",
		"Target hash: sha256:",
		"already up to date",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("upgrade output missing %q\nfull:\n%s", want, body)
		}
	}
}

func TestUpgradeCheck_DetectsDifferentTarget(t *testing.T) {
	tmplDir := t.TempDir()
	writeTinyTemplate(t, tmplDir, "tiny", "0.0.1", "hello")

	target := t.TempDir()
	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"init", tmplDir, "--target", target})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init local template: %v", err)
	}

	nextDir := t.TempDir()
	writeTinyTemplate(t, nextDir, "tiny", "0.0.2", "hello again")

	cmd2 := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd2.SetOut(out)
	cmd2.SetErr(errOut)
	cmd2.SetArgs([]string{"upgrade", "--check", "--target", target, "--to", nextDir})
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("upgrade --check --to: %v\nstderr: %s", err, errOut.String())
	}
	body := out.String()
	if !strings.Contains(body, "Target template: tiny v0.0.2") {
		t.Errorf("missing target version in output: %s", body)
	}
	if !strings.Contains(body, "template differs") {
		t.Errorf("missing differs result: %s", body)
	}
}

func TestUpgradeCheckJSONAndStrict(t *testing.T) {
	tmplDir := t.TempDir()
	writeTinyTemplate(t, tmplDir, "tiny", "0.0.1", "hello")

	target := t.TempDir()
	initCmd := NewRootCmd()
	initCmd.SetOut(&bytes.Buffer{})
	initCmd.SetErr(&bytes.Buffer{})
	initCmd.SetArgs([]string{"init", tmplDir, "--target", target})
	if err := initCmd.Execute(); err != nil {
		t.Fatalf("init local template: %v", err)
	}

	nextDir := t.TempDir()
	writeTinyTemplate(t, nextDir, "tiny", "0.0.2", "hello again")

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"upgrade", "--check", "--json", "--strict", "--target", target, "--to", nextDir})
	err := cmd.Execute()
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("strict drift err = %v, want exit 1\nstderr=%s", err, errOut.String())
	}
	var result upgradeCheckResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode upgrade json: %v\nbody=%s", err, out.String())
	}
	if !result.Differs || result.UpToDate || result.TargetTemplate != "tiny" || result.TargetVersion != "0.0.2" || !result.ApplyImplemented {
		t.Fatalf("upgrade json result = %+v", result)
	}
	if result.LockedHash == "" || result.TargetHash == "" || result.LockedHash == result.TargetHash {
		t.Fatalf("upgrade hashes = %+v", result)
	}
	if errOut.Len() != 0 {
		t.Fatalf("strict json stderr = %q", errOut.String())
	}
}

func TestUpgradeCheckFormatAndStrict(t *testing.T) {
	tmplDir := t.TempDir()
	writeTinyTemplate(t, tmplDir, "tiny", "0.0.1", "hello")

	target := t.TempDir()
	initCmd := NewRootCmd()
	initCmd.SetOut(&bytes.Buffer{})
	initCmd.SetErr(&bytes.Buffer{})
	initCmd.SetArgs([]string{"init", tmplDir, "--target", target})
	if err := initCmd.Execute(); err != nil {
		t.Fatalf("init local template: %v", err)
	}

	nextDir := t.TempDir()
	writeTinyTemplate(t, nextDir, "tiny", "0.0.2", "hello again")

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{
		"upgrade", "--check", "--strict",
		"--format", "{{.Differs}} {{.TargetVersion}} {{.ApplyImplemented}}",
		"--target", target,
		"--to", nextDir,
	})
	err := cmd.Execute()
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("strict format err = %v, want exit 1\nstderr=%s", err, errOut.String())
	}
	if got, want := out.String(), "true 0.0.2 true\n"; got != want {
		t.Fatalf("upgrade --format output = %q, want %q", got, want)
	}
	if errOut.Len() != 0 {
		t.Fatalf("strict format stderr = %q", errOut.String())
	}
}

func TestUpgradeApplyDryRunAndApplyCleanChanges(t *testing.T) {
	tmplDir := t.TempDir()
	writeTinyTemplateFiles(t, tmplDir, "tiny", "0.0.1", map[string]string{
		"skills/tiny/SKILL.md":     "hello\n",
		"skills/obsolete/SKILL.md": "remove me\n",
	})

	target := t.TempDir()
	initCmd := NewRootCmd()
	initCmd.SetOut(&bytes.Buffer{})
	initCmd.SetErr(&bytes.Buffer{})
	initCmd.SetArgs([]string{"init", tmplDir, "--target", target})
	if err := initCmd.Execute(); err != nil {
		t.Fatalf("init local template: %v", err)
	}

	nextDir := t.TempDir()
	writeTinyTemplateFiles(t, nextDir, "tiny", "0.0.2", map[string]string{
		"skills/tiny/SKILL.md": "hello again\n",
		"skills/new/SKILL.md":  "new file\n",
	})

	dryCmd := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dryCmd.SetOut(dryOut)
	dryCmd.SetErr(dryErr)
	dryCmd.SetArgs([]string{"upgrade", "--apply", "--dry-run", "--json", "--target", target, "--to", nextDir})
	if err := dryCmd.Execute(); err != nil {
		t.Fatalf("upgrade --apply --dry-run: %v\nstderr: %s", err, dryErr.String())
	}
	var dryResult upgradeApplyResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryResult); err != nil {
		t.Fatalf("decode dry-run json: %v\nbody=%s", err, dryOut.String())
	}
	if !dryResult.DryRun || dryResult.Applied || dryResult.Added != 1 || dryResult.Updated != 1 || dryResult.Removed != 1 || dryResult.Conflicts != 0 {
		t.Fatalf("dry-run result = %+v", dryResult)
	}
	assertFileBody(t, filepath.Join(target, ".agent_team", "skills", "tiny", "SKILL.md"), "hello\n")
	assertFileBody(t, filepath.Join(target, ".agent_team", "skills", "obsolete", "SKILL.md"), "remove me\n")
	if _, err := os.Stat(filepath.Join(target, ".agent_team", "skills", "new", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create new file, err=%v", err)
	}

	commandsCmd := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commandsCmd.SetOut(commandsOut)
	commandsCmd.SetErr(commandsErr)
	commandsCmd.SetArgs([]string{"upgrade", "--apply", "--dry-run", "--commands", "--target", target, "--to", nextDir})
	if err := commandsCmd.Execute(); err != nil {
		t.Fatalf("upgrade --apply --dry-run --commands: %v\nstderr: %s", err, commandsErr.String())
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "upgrade", "--apply", "--repo", target, "--to", nextDir}), " ") + "\n"
	if got := commandsOut.String(); got != wantCommand {
		t.Fatalf("upgrade --commands = %q, want %q", got, wantCommand)
	}

	rootScopedCommands := NewRootCmd()
	rootScopedOut, rootScopedErr := &bytes.Buffer{}, &bytes.Buffer{}
	rootScopedCommands.SetOut(rootScopedOut)
	rootScopedCommands.SetErr(rootScopedErr)
	rootScopedCommands.SetArgs([]string{"--repo", target, "upgrade", "--apply", "--dry-run", "--commands", "--to", nextDir})
	if err := rootScopedCommands.Execute(); err != nil {
		t.Fatalf("upgrade root --repo --dry-run --commands: %v\nstderr: %s", err, rootScopedErr.String())
	}
	if got := rootScopedOut.String(); got != wantCommand {
		t.Fatalf("upgrade root --commands = %q, want %q", got, wantCommand)
	}

	applyCmd := NewRootCmd()
	applyOut, applyErr := &bytes.Buffer{}, &bytes.Buffer{}
	applyCmd.SetOut(applyOut)
	applyCmd.SetErr(applyErr)
	applyCmd.SetArgs([]string{"upgrade", "--apply", "--json", "--target", target, "--to", nextDir})
	if err := applyCmd.Execute(); err != nil {
		t.Fatalf("upgrade --apply: %v\nstderr: %s", err, applyErr.String())
	}
	var applyResult upgradeApplyResult
	if err := json.Unmarshal(applyOut.Bytes(), &applyResult); err != nil {
		t.Fatalf("decode apply json: %v\nbody=%s", err, applyOut.String())
	}
	if applyResult.DryRun || !applyResult.Applied || applyResult.Added != 1 || applyResult.Updated != 1 || applyResult.Removed != 1 || applyResult.Conflicts != 0 {
		t.Fatalf("apply result = %+v", applyResult)
	}
	assertFileBody(t, filepath.Join(target, ".agent_team", "skills", "tiny", "SKILL.md"), "hello again\n")
	assertFileBody(t, filepath.Join(target, ".agent_team", "skills", "new", "SKILL.md"), "new file\n")
	if _, err := os.Stat(filepath.Join(target, ".agent_team", "skills", "obsolete", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("obsolete file should be removed, err=%v", err)
	}
	lockBody := readUpgradeTestFile(t, filepath.Join(target, ".agent_team", ".template.lock"))
	if !strings.Contains(lockBody, `version = "0.0.2"`) || !strings.Contains(lockBody, `ref = "`+nextDir+`"`) {
		t.Fatalf("lock was not updated to target template:\n%s", lockBody)
	}
}

func TestUpgradeApplyReportsConflictForLocalEdit(t *testing.T) {
	tmplDir := t.TempDir()
	writeTinyTemplate(t, tmplDir, "tiny", "0.0.1", "hello\n")

	target := t.TempDir()
	initCmd := NewRootCmd()
	initCmd.SetOut(&bytes.Buffer{})
	initCmd.SetErr(&bytes.Buffer{})
	initCmd.SetArgs([]string{"init", tmplDir, "--target", target})
	if err := initCmd.Execute(); err != nil {
		t.Fatalf("init local template: %v", err)
	}

	currentPath := filepath.Join(target, ".agent_team", "skills", "tiny", "SKILL.md")
	if err := os.WriteFile(currentPath, []byte("local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	nextDir := t.TempDir()
	writeTinyTemplate(t, nextDir, "tiny", "0.0.2", "target edit\n")

	commandsCmd := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commandsCmd.SetOut(commandsOut)
	commandsCmd.SetErr(commandsErr)
	commandsCmd.SetArgs([]string{"upgrade", "--apply", "--dry-run", "--commands", "--target", target, "--to", nextDir})
	if err := commandsCmd.Execute(); err != nil {
		t.Fatalf("conflict upgrade --commands: %v\nstderr=%s", err, commandsErr.String())
	}
	if commandsOut.Len() != 0 {
		t.Fatalf("conflict upgrade --commands should not emit apply command: %q", commandsOut.String())
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"upgrade", "--apply", "--json", "--target", target, "--to", nextDir})
	err := cmd.Execute()
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("conflict err = %v, want exit 1\nstderr=%s", err, errOut.String())
	}
	var result upgradeApplyResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode conflict json: %v\nbody=%s", err, out.String())
	}
	if result.Applied || result.Conflicts != 1 || len(result.Actions) != 1 || result.Actions[0].Action != "conflict" {
		t.Fatalf("conflict result = %+v", result)
	}
	assertFileBody(t, currentPath, "local edit\n")
}

func TestUpgradeOutputValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{
			args: []string{"upgrade", "--check", "--format", "{{.Differs}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			args: []string{"upgrade", "--check", "--format", "{{"},
			want: "invalid --format template",
		},
		{
			args: []string{"upgrade", "--apply", "--commands"},
			want: "--commands requires --apply --dry-run",
		},
		{
			args: []string{"upgrade", "--check", "--commands"},
			want: "--commands requires --apply --dry-run",
		},
		{
			args: []string{"upgrade", "--apply", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			args: []string{"upgrade", "--apply", "--dry-run", "--commands", "--format", "{{.Differs}}"},
			want: "--commands cannot be combined with --format",
		},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(errOut)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		var ec ExitCode
		if !errors.As(err, &ec) || int(ec) != 2 {
			t.Fatalf("%v: err = %v, want exit 2", tc.args, err)
		}
		if !strings.Contains(errOut.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, errOut.String(), tc.want)
		}
		if out.Len() != 0 {
			t.Fatalf("%v: validation should not write stdout: %q", tc.args, out.String())
		}
	}
}

func TestUpgradeRequiresMode(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"upgrade", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected upgrade without a mode to fail")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
	if !strings.Contains(errOut.String(), "pass --check") || !strings.Contains(errOut.String(), "--apply") {
		t.Errorf("missing mode guidance: %s", errOut.String())
	}
}

func TestUpgradeCheck_FailsWithoutLock(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	if err := os.Remove(filepath.Join(tmp, ".agent_team", ".template.lock")); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"upgrade", "--check", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected missing lock to fail")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
	if !strings.Contains(errOut.String(), ".template.lock") {
		t.Errorf("missing lock path in error: %s", errOut.String())
	}
}

func writeTinyTemplate(t *testing.T, dir, name, version, body string) {
	t.Helper()
	writeTinyTemplateFiles(t, dir, name, version, map[string]string{
		"skills/tiny/SKILL.md": body,
	})
}

func writeTinyTemplateFiles(t *testing.T, dir, name, version string, files map[string]string) {
	t.Helper()
	manifest := `[template]
name = "` + name + `"
version = "` + version + `"
`
	if err := os.WriteFile(filepath.Join(dir, "template.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for rel, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readUpgradeTestFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func assertFileBody(t *testing.T, path, want string) {
	t.Helper()
	if got := readUpgradeTestFile(t, path); got != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}
