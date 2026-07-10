package agentteam

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

const docsMarkerChecker = "template/skills/sentinel/scripts/docs_marker_check.py"

func TestSentinelDocsMarkerCheckerIgnoresExampleBlocks(t *testing.T) {
	requirePython3(t)

	cmd := exec.Command(
		"python3",
		docsMarkerChecker,
		"https://docs.example.invalid/getting-started.html",
		"scripts/skills/sentinel/fixtures/docs_marker_code_blocks.html",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected marker checker to pass for code/example blocks: %v\n%s", err, output)
	}
}

func TestSentinelDocsMarkerCheckerReportsProseLeaks(t *testing.T) {
	requirePython3(t)

	const url = "https://docs.example.invalid/getting-started.html"
	cmd := exec.Command(
		"python3",
		docsMarkerChecker,
		url,
		"scripts/skills/sentinel/fixtures/docs_marker_prose_leak.html",
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected marker checker to fail for prose leak, got success:\n%s", output)
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("expected marker checker exit 1, got %v:\n%s", err, output)
	}

	got := string(output)
	for _, want := range []string{
		url,
		"outside code/example block",
		"Team UUID: {{ .linear.team_id }}",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected marker checker output to contain %q, got:\n%s", want, got)
		}
	}
}

func requirePython3(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is required for sentinel docs marker checks")
	}
}
