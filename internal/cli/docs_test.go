package cli

import (
	"bytes"
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
