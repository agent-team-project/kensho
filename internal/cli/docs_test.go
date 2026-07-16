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

func TestDocsCLIGeneratesMarkdown(t *testing.T) {
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"docs", "cli"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("docs cli: %v\nstderr=%s", err, stderr.String())
	}
	body := out.String()
	for _, want := range []string{
		"# agent-team CLI Reference",
		"## `agent-team run`",
		"Launch an LLM runtime session as the named agent.",
		"-p, --prompt string",
		"## `agent-team job cleanup`",
		"## `agent-team docs cli`",
		"--repo string",
		`(default "<repo>")`,
		"--verify-pr",
		"GH-N (case-insensitive), #N, N, owner/repo#N, owner/repo/issues/N",
		"https://github.com/owner/repo/issues/N",
		"https://api.github.com/repos/owner/repo/issues/N",
		"configured default owner/repo",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("generated docs missing %q", want)
		}
	}
	if cwd, err := os.Getwd(); err == nil && strings.Contains(body, cwd) {
		t.Fatalf("generated docs leaked cwd %q", cwd)
	}
	if stderr.Len() != 0 {
		t.Fatalf("docs cli wrote stderr: %s", stderr.String())
	}
}

func TestDocsCLIWritesOutputFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reference", "cli.generated.md")
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"docs", "cli", "--output", path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("docs cli --output: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(out.String(), "Wrote CLI reference to ") {
		t.Fatalf("stdout = %q", out.String())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !strings.Contains(string(body), "## `agent-team pipeline advance`") {
		t.Fatalf("output file missing pipeline advance docs")
	}
	if stderr.Len() != 0 {
		t.Fatalf("docs cli --output wrote stderr: %s", stderr.String())
	}
}

func TestDocsCLIChecksOutputFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.generated.md")
	write := NewRootCmd()
	write.SetOut(&bytes.Buffer{})
	write.SetErr(&bytes.Buffer{})
	write.SetArgs([]string{"docs", "cli", "--output", path})
	if err := write.Execute(); err != nil {
		t.Fatalf("write docs fixture: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"docs", "cli", "--check", path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("docs cli --check: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(out.String(), "CLI reference is up to date:") {
		t.Fatalf("check stdout = %q", out.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("docs cli --check wrote stderr: %s", stderr.String())
	}
}

func TestDocsCLICheckFailsWhenStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.generated.md")
	if err := os.WriteFile(path, []byte("stale\n"), 0o644); err != nil {
		t.Fatalf("write stale docs: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"docs", "cli", "--check", path})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("docs cli --check succeeded for stale file")
	}
	var code ExitCode
	if !errors.As(err, &code) || int(code) != 1 {
		t.Fatalf("error = %v, want exit 1", err)
	}
	if !strings.Contains(stderr.String(), "is stale") || !strings.Contains(stderr.String(), "agent-team docs cli --output") {
		t.Fatalf("check stderr = %q", stderr.String())
	}
	if out.Len() != 0 {
		t.Fatalf("stale check wrote stdout: %s", out.String())
	}
}

func TestDocsCLIRejectsOutputAndCheck(t *testing.T) {
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"docs", "cli", "--output", "out.md", "--check", "out.md"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("docs cli accepted --output with --check")
	}
	var code ExitCode
	if !errors.As(err, &code) || int(code) != 2 {
		t.Fatalf("error = %v, want exit 2", err)
	}
	if !strings.Contains(stderr.String(), "--output cannot be combined with --check") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if out.Len() != 0 {
		t.Fatalf("validation wrote stdout: %s", out.String())
	}
}

func TestDocsSiteShowsLocalCommands(t *testing.T) {
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"docs", "site"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("docs site: %v\nstderr=%s", err, stderr.String())
	}
	body := out.String()
	for _, want := range []string{
		"Developer docs site",
		"available:     true",
		"docs/.vitepress/config.mts",
		"docs/reference/cli.generated.md",
		"http://localhost:5173/",
		"npm run docs:dev",
		"npm run docs:build",
		"npm run docs:preview",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("docs site output missing %q\nbody:\n%s", want, body)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("docs site wrote stderr: %s", stderr.String())
	}
}

func TestDocsSiteCommandsOnly(t *testing.T) {
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"docs", "site", "--commands"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("docs site --commands: %v\nstderr=%s", err, stderr.String())
	}
	body := strings.TrimSpace(out.String())
	lines := strings.Split(body, "\n")
	if len(lines) != 3 {
		t.Fatalf("commands = %q, want three lines", body)
	}
	for _, want := range []string{"npm run docs:dev", "npm run docs:build", "npm run docs:preview"} {
		if !strings.Contains(body, want) {
			t.Fatalf("docs site commands missing %q\nbody:\n%s", want, body)
		}
	}
	for _, unwanted := range []string{"Developer docs site", "dev_url:"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("docs site commands included %q\nbody:\n%s", unwanted, body)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("docs site --commands wrote stderr: %s", stderr.String())
	}
}

func TestDocsSiteJSON(t *testing.T) {
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"docs", "site", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("docs site --json: %v\nstderr=%s", err, stderr.String())
	}
	var info docsSiteInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("decode docs site json: %v\nbody=%s", err, out.String())
	}
	if !info.Available || info.DevURL != "http://localhost:5173/" || len(info.Commands) != 3 {
		t.Fatalf("docs site json = %+v", info)
	}
	if !strings.HasSuffix(info.Config, filepath.Join("docs", ".vitepress", "config.mts")) {
		t.Fatalf("docs site config = %q", info.Config)
	}
	if stderr.Len() != 0 {
		t.Fatalf("docs site --json wrote stderr: %s", stderr.String())
	}
}

func TestDocsSiteRejectsCommandsAndJSON(t *testing.T) {
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"docs", "site", "--commands", "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("docs site accepted --commands with --json")
	}
	var code ExitCode
	if !errors.As(err, &code) || int(code) != 2 {
		t.Fatalf("error = %v, want exit 2", err)
	}
	if !strings.Contains(stderr.String(), wantCommandsModeConflict("--json")) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if out.Len() != 0 {
		t.Fatalf("validation wrote stdout: %s", out.String())
	}
}
