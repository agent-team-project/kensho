package cli

import (
	"bytes"
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
		"--verify-pr",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("generated docs missing %q", want)
		}
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
