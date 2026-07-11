package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeStatus is a tiny helper that hand-emits a status.toml fixture. We
// avoid round-tripping through the bash skill in Go tests so the unit tests
// don't depend on python3 or bash being on PATH for the test runner.
func writeStatus(t *testing.T, stateDir, body string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stateDir, "status.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInstancePs_NoState(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"instance", "ps", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("instance ps: %v", err)
	}
	if !strings.Contains(out.String(), "(no instances)") {
		t.Errorf("expected (no instances), got: %s", out.String())
	}
}

func TestInstancePs_RendersRows(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	stateRoot := filepath.Join(tmp, ".agent_team", "state")

	now := time.Now()

	writeStatus(t, filepath.Join(stateRoot, "manager"), `
[status]
phase = "idle"
description = "Awaiting next request"
since = "2026-04-29T00:00:00Z"
`, now.Add(-2*time.Minute))

	writeStatus(t, filepath.Join(stateRoot, "worker-tst-1"), `
[status]
phase = "implementing"
description = "Porting status emission"
since = "2026-04-29T00:00:00Z"

[work]
job = "tst-1"
ticket = "TST-1"
pr = "https://example/pulls/1"
branch = "tst-1"
`, now.Add(-30*time.Second))

	writeStatus(t, filepath.Join(stateRoot, "ticket-manager"), `
[status]
phase = "blocked"
since = "2026-04-29T00:00:00Z"

[blocking]
reason = "no projects configured"
ask_to = "user"
`, now.Add(-1*time.Minute))

	// Empty state dir — should still render with placeholders.
	if err := os.MkdirAll(filepath.Join(stateRoot, "empty-one"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"instance", "ps", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("instance ps: %v", err)
	}
	got := out.String()

	for _, want := range []string{
		"INSTANCE", "AGENT", "PHASE", "AGE", "SUMMARY",
		"manager", "idle", "Awaiting next request",
		"worker-tst-1", "worker", "implementing", "Porting status emission",
		"ticket-manager", "blocked", "asks user: no projects configured",
		"empty-one", "—",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ps output missing %q\n--- output:\n%s", want, got)
		}
	}
	if strings.Contains(got, "(stale)") {
		t.Errorf("nothing should be stale yet, got:\n%s", got)
	}
}

func TestInstancePs_StaleAnnotation(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	stateRoot := filepath.Join(tmp, ".agent_team", "state")

	now := time.Now()
	// A non-idle phase older than the staleAfter threshold.
	writeStatus(t, filepath.Join(stateRoot, "worker-stuck"), `
[status]
phase = "implementing"
description = "wedged"
`, now.Add(-15*time.Minute))

	// An idle one of the same age — should NOT be flagged.
	writeStatus(t, filepath.Join(stateRoot, "manager"), `
[status]
phase = "idle"
description = "waiting"
`, now.Add(-15*time.Minute))

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"instance", "ps", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("instance ps: %v", err)
	}
	got := out.String()

	// worker-stuck row should carry (stale).
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "worker-stuck") && !strings.Contains(line, "(stale)") {
			t.Errorf("worker-stuck row should be flagged stale: %q", line)
		}
		if strings.HasPrefix(line, "manager") && strings.Contains(line, "(stale)") {
			t.Errorf("manager row (idle) should NOT be flagged stale: %q", line)
		}
	}
}

func TestInstancePs_CorruptStatusDegradedRow(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	stateRoot := filepath.Join(tmp, ".agent_team", "state")
	dir := filepath.Join(stateRoot, "broken")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status.toml"), []byte("not = valid = toml"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Add a healthy row so we can confirm the bad one didn't poison the table.
	writeStatus(t, filepath.Join(stateRoot, "ok"), `
[status]
phase = "idle"
description = "fine"
`, time.Now())

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"instance", "ps", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("instance ps: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "broken") || !strings.Contains(got, "parse error") {
		t.Errorf("broken instance should render with parse-error summary; got:\n%s", got)
	}
	if !strings.Contains(got, "ok") || !strings.Contains(got, "fine") {
		t.Errorf("healthy row should still render; got:\n%s", got)
	}
}

func TestInstanceShow_IncludesStatusPanel(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	stateRoot := filepath.Join(tmp, ".agent_team", "state")
	writeStatus(t, filepath.Join(stateRoot, "worker-x"), `
[status]
phase = "awaiting_review"
description = "PR open"
since = "2026-04-29T00:00:00Z"

[work]
job = "tst-9"
ticket = "TST-9"
pr = "https://example/pulls/9"
branch = "tst-9"
`, time.Now())

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"instance", "show", "worker-x", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("instance show: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"instance: worker-x",
		"status:",
		"phase:        awaiting_review",
		"description:  PR open",
		"work:",
		"job:     tst-9",
		"ticket:  TST-9",
		"pr:      https://example/pulls/9",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("show output missing %q\n--- output:\n%s", want, got)
		}
	}
}

func TestGuessAgentName(t *testing.T) {
	agents := map[string]bool{"manager": true, "worker": true, "ticket-manager": true}
	for _, c := range []struct {
		instance string
		want     string
	}{
		{"manager", "manager"},
		{"worker", "worker"},
		{"worker-squ-25", "worker"},
		{"ticket-manager", "ticket-manager"},
		{"ticket-manager-foo", "ticket-manager"},
		{"random-name", "—"},
	} {
		got := guessAgentName(c.instance, agents)
		if got != c.want {
			t.Errorf("guessAgentName(%q) = %q, want %q", c.instance, got, c.want)
		}
	}
}

func TestFormatAge(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{2 * time.Minute, "2m"},
		{3 * time.Hour, "3h"},
		{49 * time.Hour, "2d"},
	} {
		if got := formatAge(c.d); got != c.want {
			t.Errorf("formatAge(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
