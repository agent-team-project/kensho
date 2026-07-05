package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/runtimebin"
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

func setChildLogModTimeForTest(t *testing.T, daemonRoot, instance string, modTime time.Time) {
	t.Helper()
	path := filepath.Join(daemonRoot, instance, "child.log")
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("chtimes %s: %v", instance, err)
	}
}

func writeLastMessageForTest(t *testing.T, teamDir, instance, body string) {
	t.Helper()
	path := filepath.Join(teamDir, "state", instance, runtimebin.CodexLastMessageFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir last message dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write last message: %v", err)
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

func TestLogsSingleInstanceUsesLocalLogWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	writeChildLogForTest(t, root, "manager", "first\nlast\n")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "manager", "--tail", "1", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs local: %v\nstderr=%s", err, stderr.String())
	}
	if got := out.String(); got != "last\n" {
		t.Fatalf("logs output = %q, want last line", got)
	}
}

func TestLogsCleanFiltersCodexNoiseBeforeTail(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "worker",
		Agent:    "worker",
		Runtime:  string(runtimebin.KindCodex),
		Status:   daemon.StatusExited,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	writeChildLogForTest(t, root, "worker", strings.Join([]string{
		"Reading additional input from stdin...",
		"2026-06-24T10:10:44Z  WARN codex_core_plugins::manager: failed to refresh remote installed plugins cache",
		"first useful line",
		"ERROR: Reconnecting... 1/5",
		"final useful line",
		"",
	}, "\n"))

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "worker", "--clean", "--tail", "1", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs clean: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "final useful line\n"; got != want {
		t.Fatalf("logs clean output = %q, want %q", got, want)
	}
}

func TestLogsRendersCodexJSONLByDefault(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "worker",
		Agent:    "worker",
		Runtime:  string(runtimebin.KindCodex),
		Status:   daemon.StatusExited,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	writeChildLogForTest(t, root, "worker", strings.Join([]string{
		`{"type":"thread.started","thread_id":"thread-1"}`,
		`{"type":"turn.started","turn_id":"turn-1"}`,
		`{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"ready"}}`,
		`{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"echo hi","exit_code":null,"status":"in_progress"}}`,
		`{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"echo hi","aggregated_output":"hello\n","exit_code":7,"status":"completed"}}`,
		"",
	}, "\n"))

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "worker", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs render: %v\nstderr=%s", err, stderr.String())
	}
	want := strings.Join([]string{
		ansiDim + "thread.started thread-1" + ansiReset,
		ansiDim + "turn.started turn-1" + ansiReset,
		"ready",
		"$ echo hi",
		"hello",
		ansiDim + "exit 7" + ansiReset,
		"",
	}, "\n")
	if got := out.String(); got != want {
		t.Fatalf("logs render = %q, want %q", got, want)
	}
}

func TestLogsRawPreservesCodexJSONL(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "worker",
		Agent:    "worker",
		Runtime:  string(runtimebin.KindCodex),
		Status:   daemon.StatusExited,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	raw := `{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"ready"}}` + "\n"
	writeChildLogForTest(t, root, "worker", raw)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "worker", "--raw", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs raw: %v\nstderr=%s", err, stderr.String())
	}
	if got := out.String(); got != raw {
		t.Fatalf("logs raw = %q, want raw JSONL %q", got, raw)
	}
}

func TestStreamDaemonLogRendersCodexJSONL(t *testing.T) {
	root := t.TempDir()
	m := daemon.NewInstanceManager(root, nil)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "worker",
		Agent:    "worker",
		Runtime:  string(runtimebin.KindCodex),
		Status:   daemon.StatusStopped,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	if err := m.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	writeChildLogForTest(t, root, "worker", `{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"daemon hello"}}`+"\n")
	c, cleanup := newTestClient(t, daemon.Handler(m, nil, nil, ""))
	defer cleanup()

	var buf bytes.Buffer
	if err := streamDaemonLog(context.Background(), &buf, c, "worker", false, 0, false); err != nil {
		t.Fatalf("stream daemon log: %v", err)
	}
	if got, want := buf.String(), "daemon hello\n"; got != want {
		t.Fatalf("daemon log render = %q, want %q", got, want)
	}
}

func TestCodexLogWriterRendersCompleteLinesIncrementally(t *testing.T) {
	var buf bytes.Buffer
	w := newCodexLogWriter(&buf)
	line := `{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"chunked"}}` + "\n"
	if _, err := w.Write([]byte(line[:20])); err != nil {
		t.Fatalf("write first chunk: %v", err)
	}
	if got := buf.String(); got != "" {
		t.Fatalf("partial line rendered early: %q", got)
	}
	if _, err := w.Write([]byte(line[20:])); err != nil {
		t.Fatalf("write second chunk: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got, want := buf.String(), "chunked\n"; got != want {
		t.Fatalf("chunked render = %q, want %q", got, want)
	}
}

func TestLogsLastMessageUsesStateSidecarWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusExited,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	writeChildLogForTest(t, root, "manager", "codex diagnostic noise\nfinal answer buried\n")
	writeLastMessageForTest(t, teamDir, "manager", "clean final answer")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "manager", "--last-message", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs last-message: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "clean final answer\n"; got != want {
		t.Fatalf("last-message output = %q, want %q", got, want)
	}
}

func TestLogsLastMessageLatestAndLastUseMetadataOrdering(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusExited, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusExited, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "mid", Agent: "worker", Status: daemon.StatusExited, StartedAt: now.Add(-30 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		writeLastMessageForTest(t, teamDir, meta.Instance, meta.Instance+" final")
	}

	latest := NewRootCmd()
	latestOut, latestErr := &bytes.Buffer{}, &bytes.Buffer{}
	latest.SetOut(latestOut)
	latest.SetErr(latestErr)
	latest.SetArgs([]string{"logs", "--latest", "--last-message", "--target", tmp})
	if err := latest.Execute(); err != nil {
		t.Fatalf("logs latest last-message: %v\nstderr=%s", err, latestErr.String())
	}
	if got, want := latestOut.String(), "new final\n"; got != want {
		t.Fatalf("latest last-message = %q, want %q", got, want)
	}

	last := NewRootCmd()
	lastOut, lastErr := &bytes.Buffer{}, &bytes.Buffer{}
	last.SetOut(lastOut)
	last.SetErr(lastErr)
	last.SetArgs([]string{"logs", "--last", "2", "--last-message", "--target", tmp})
	if err := last.Execute(); err != nil {
		t.Fatalf("logs last 2 last-message: %v\nstderr=%s", err, lastErr.String())
	}
	if got, want := lastOut.String(), "new                  | new final\nmid                  | mid final\n"; got != want {
		t.Fatalf("last 2 last-message = %q, want %q", got, want)
	}
}

func TestLogsLatestUsesLocalNewestLogWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "missing-start", Agent: "worker", Status: daemon.StatusStopped},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		writeChildLogForTest(t, root, meta.Instance, meta.Instance+" old\n"+meta.Instance+" last\n")
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "--latest", "--tail", "1", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs --latest local: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "new last\n"; got != want {
		t.Fatalf("logs --latest output = %q, want %q", got, want)
	}
}

func TestLogsLatestUsesDaemonNewestLog(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-logs-latest-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	m := daemon.NewInstanceManager(root, nil)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-5 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		writeChildLogForTest(t, root, meta.Instance, meta.Instance+" old\n"+meta.Instance+" last\n")
	}
	if err := m.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	cleanup := startRunTestDaemon(t, teamDir, m)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "--latest", "--tail", "1", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs --latest daemon: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "new last\n"; got != want {
		t.Fatalf("logs --latest daemon output = %q, want %q", got, want)
	}
}

func TestLogsLatestWithAgentFilterUsesLocalNewestMatchingLog(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "worker-old", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "manager-new", Agent: "manager", Status: daemon.StatusStopped, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "worker-mid", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-30 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		writeChildLogForTest(t, root, meta.Instance, meta.Instance+" log\n")
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "--latest", "--agent", "worker", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs --latest --agent local: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "worker-mid log\n"; got != want {
		t.Fatalf("logs --latest --agent output = %q, want %q", got, want)
	}
}

func TestLogsPhaseFilterUsesLocalStatusWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusStopped},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusStopped},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		writeChildLogForTest(t, root, meta.Instance, meta.Instance+" old\n"+meta.Instance+" last\n")
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `
[status]
phase = "idle"
description = "waiting"
`, now)
	writeStatus(t, filepath.Join(teamDir, "state", "worker"), `
[status]
phase = "blocked"
description = "needs input"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "--phase", "blocked", "--tail", "1", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs --phase local: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "worker               | worker last\n"; got != want {
		t.Fatalf("logs --phase output = %q, want %q", got, want)
	}
}

func TestLogsStaleFilterUsesLocalStatusWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusStopped},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusStopped},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		writeChildLogForTest(t, root, meta.Instance, meta.Instance+" old\n"+meta.Instance+" last\n")
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `
[status]
phase = "implementing"
description = "stuck"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "worker"), `
[status]
phase = "implementing"
description = "fresh"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "--stale", "--tail", "1", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs --stale local: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "manager              | manager last\n"; got != want {
		t.Fatalf("logs --stale output = %q, want %q", got, want)
	}
}

func TestLogsUnhealthyFilterUsesLocalStatusWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "crashed", Agent: "worker", Status: daemon.StatusCrashed, StartedAt: old},
		{Instance: "fresh", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now},
		{Instance: "stale", Agent: "manager", Status: daemon.StatusStopped, StartedAt: old},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		writeChildLogForTest(t, root, meta.Instance, meta.Instance+" old\n"+meta.Instance+" last\n")
	}
	writeStatus(t, filepath.Join(teamDir, "state", "stale"), `
[status]
phase = "implementing"
description = "stuck"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "fresh"), `
[status]
phase = "idle"
description = "fresh"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "--unhealthy", "--tail", "1", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs --unhealthy local: %v\nstderr=%s", err, stderr.String())
	}
	got := out.String()
	if !strings.Contains(got, "crashed last") || !strings.Contains(got, "stale last") {
		t.Fatalf("logs --unhealthy output = %q, want crashed and stale logs", got)
	}
	if strings.Contains(got, "fresh") {
		t.Fatalf("logs --unhealthy output = %q, want fresh log filtered out", got)
	}
}

func TestLogsSingleGrepUsesLocalLogWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	writeChildLogForTest(t, root, "manager", "info boot\nerror failed\ninfo done\n")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "manager", "--grep", "error", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs local grep: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "error failed\n"; got != want {
		t.Fatalf("logs grep output = %q, want %q", got, want)
	}
}

func TestAttachNoFollowUsesLocalLogWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	writeChildLogForTest(t, root, "manager", "first\nlast\n")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"attach", "manager", "--no-follow", "--tail", "all", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("attach local: %v\nstderr=%s", err, stderr.String())
	}
	if got := out.String(); got != "first\nlast\n" {
		t.Fatalf("attach output = %q, want full log", got)
	}
}

func TestExecAliasNoFollowUsesAttachLogMode(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	writeChildLogForTest(t, root, "manager", "exec first\nexec last\n")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"exec", "manager", "--no-follow", "--tail", "all", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("exec alias local: %v\nstderr=%s", err, stderr.String())
	}
	if got := out.String(); got != "exec first\nexec last\n" {
		t.Fatalf("exec alias output = %q, want full log", got)
	}
}

func TestAttachLatestNoFollowUsesLocalNewestLogWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-5 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		writeChildLogForTest(t, root, meta.Instance, meta.Instance+" first\n"+meta.Instance+" last\n")
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"attach", "--latest", "--no-follow", "--tail", "all", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("attach --latest local: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "new first\nnew last\n"; got != want {
		t.Fatalf("attach --latest output = %q, want %q", got, want)
	}
}

func TestAttachLastNoFollowUsesLocalNewestLogsWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "mid", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-30 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		writeChildLogForTest(t, root, meta.Instance, meta.Instance+" old\n"+meta.Instance+" last\n")
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"attach", "--last", "2", "--no-follow", "--tail", "1", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("attach --last local: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "new                  | new last\nmid                  | mid last\n"; got != want {
		t.Fatalf("attach --last output = %q, want %q", got, want)
	}
}

func TestAttachFiltersNoFollowUseLocalMatchingLogsWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusStopped},
		{Instance: "worker-running", Agent: "worker", Status: daemon.StatusRunning},
		{Instance: "worker-stopped", Agent: "worker", Status: daemon.StatusStopped},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		writeChildLogForTest(t, root, meta.Instance, meta.Instance+" old\n"+meta.Instance+" last\n")
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"attach", "--agent", "worker", "--status", "stopped", "--no-follow", "--tail", "1", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("attach filters local: %v\nstderr=%s", err, stderr.String())
	}
	got := out.String()
	if !strings.Contains(got, "worker-stopped") || !strings.Contains(got, "worker-stopped last") {
		t.Fatalf("attach filtered output = %q, want stopped worker log", got)
	}
	if strings.Contains(got, "worker-running") || strings.Contains(got, "manager") {
		t.Fatalf("attach filtered output = %q, want only stopped worker log", got)
	}
}

func TestAttachRuntimeFilterNoFollowUsesLocalMatchingLogsWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, meta := range []*daemon.Metadata{
		{Instance: "codex-worker", Agent: "worker", Runtime: "codex", Status: daemon.StatusStopped},
		{Instance: "claude-manager", Agent: "manager", Runtime: "claude", Status: daemon.StatusStopped},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		writeChildLogForTest(t, root, meta.Instance, meta.Instance+" old\n"+meta.Instance+" last\n")
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"attach", "--runtime", "codex", "--no-follow", "--tail", "all", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("attach --runtime local: %v\nstderr=%s", err, stderr.String())
	}
	got := out.String()
	if !strings.Contains(got, "codex-worker last") {
		t.Fatalf("attach runtime output = %q, want codex worker log", got)
	}
	if strings.Contains(got, "claude-manager") {
		t.Fatalf("attach runtime output = %q, want only codex log", got)
	}
}

func TestAttachPhaseFilterNoFollowUsesLocalStatusWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusStopped},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusStopped},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		writeChildLogForTest(t, root, meta.Instance, meta.Instance+" old\n"+meta.Instance+" last\n")
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `
[status]
phase = "idle"
description = "waiting"
`, now)
	writeStatus(t, filepath.Join(teamDir, "state", "worker"), `
[status]
phase = "blocked"
description = "needs input"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"attach", "--phase", "blocked", "--no-follow", "--tail", "1", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("attach --phase local: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "worker               | worker last\n"; got != want {
		t.Fatalf("attach --phase output = %q, want %q", got, want)
	}
}

func TestAttachStaleFilterNoFollowUsesLocalStatusWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusStopped},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusStopped},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		writeChildLogForTest(t, root, meta.Instance, meta.Instance+" old\n"+meta.Instance+" last\n")
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `
[status]
phase = "blocked"
description = "stuck"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "worker"), `
[status]
phase = "blocked"
description = "fresh"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"attach", "--stale", "--no-follow", "--tail", "1", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("attach --stale local: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "manager              | manager last\n"; got != want {
		t.Fatalf("attach --stale output = %q, want %q", got, want)
	}
}

func TestAttachUnhealthyFilterNoFollowUsesLocalStatusWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "crashed", Agent: "worker", Status: daemon.StatusCrashed, StartedAt: old},
		{Instance: "fresh", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now},
		{Instance: "stale", Agent: "manager", Status: daemon.StatusStopped, StartedAt: old},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		writeChildLogForTest(t, root, meta.Instance, meta.Instance+" old\n"+meta.Instance+" last\n")
	}
	writeStatus(t, filepath.Join(teamDir, "state", "stale"), `
[status]
phase = "blocked"
description = "stuck"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "fresh"), `
[status]
phase = "idle"
description = "fresh"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"attach", "--unhealthy", "--no-follow", "--tail", "1", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("attach --unhealthy local: %v\nstderr=%s", err, stderr.String())
	}
	got := out.String()
	if !strings.Contains(got, "crashed last") || !strings.Contains(got, "stale last") {
		t.Fatalf("attach --unhealthy output = %q, want crashed and stale logs", got)
	}
	if strings.Contains(got, "fresh") {
		t.Fatalf("attach --unhealthy output = %q, want fresh log filtered out", got)
	}
}

func TestAttachRuntimeStaleFilterNoFollowUsesLocalMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	for _, meta := range []*daemon.Metadata{
		{Instance: "crashed", Agent: "worker", Runtime: "codex", Status: daemon.StatusCrashed, PID: 99999998, StartedAt: now.Add(-2 * time.Minute)},
		{Instance: "runtime-stale", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999999, StartedAt: now.Add(-time.Minute)},
		{Instance: "fresh", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		writeChildLogForTest(t, root, meta.Instance, meta.Instance+" old\n"+meta.Instance+" last\n")
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"attach", "--runtime-stale", "--no-follow", "--tail", "1", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("attach --runtime-stale local: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "runtime-stale        | runtime-stale last\n"; got != want {
		t.Fatalf("attach --runtime-stale output = %q, want %q", got, want)
	}
}

func TestAttachNoFollowGrepUsesLocalLogWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	writeChildLogForTest(t, root, "manager", "info boot\nerror failed\ninfo done\n")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"attach", "manager", "--no-follow", "--grep", "error", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("attach local grep: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "error failed\n"; got != want {
		t.Fatalf("attach grep output = %q, want %q", got, want)
	}
}

func TestAttachNoFollowSinceUsesLocalLogModifiedTime(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	writeChildLogForTest(t, root, "manager", "old line\n")
	setChildLogModTimeForTest(t, root, "manager", time.Now().Add(-2*time.Hour))

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"attach", "manager", "--no-follow", "--since", "1h", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("attach local since: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "(no matching logs)\n"; got != want {
		t.Fatalf("attach since output = %q, want %q", got, want)
	}
}

func TestLogsAllUsesLocalLogsWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, name := range []string{"worker", "manager"} {
		if err := daemon.WriteMetadata(root, &daemon.Metadata{
			Instance: name,
			Agent:    name,
			Status:   daemon.StatusStopped,
		}); err != nil {
			t.Fatalf("write metadata %s: %v", name, err)
		}
		writeChildLogForTest(t, root, name, name+" old\n"+name+" last\n")
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "--all", "--tail", "1", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs --all local: %v\nstderr=%s", err, stderr.String())
	}
	want := "manager              | manager last\nworker               | worker last\n"
	if got := out.String(); got != want {
		t.Fatalf("logs --all output = %q, want %q", got, want)
	}
}

func TestLogsLastUsesLocalNewestLogsWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "mid", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-30 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		writeChildLogForTest(t, root, meta.Instance, meta.Instance+" old\n"+meta.Instance+" last\n")
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "--last", "2", "--tail", "1", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs --last local: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "new                  | new last\nmid                  | mid last\n"; got != want {
		t.Fatalf("logs --last output = %q, want %q", got, want)
	}
}

func TestLogsAllGrepPrefixesMatchingLines(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "worker", body: "worker ok\nworker error\n"},
		{name: "manager", body: "manager error\nmanager ok\n"},
	} {
		if err := daemon.WriteMetadata(root, &daemon.Metadata{
			Instance: tc.name,
			Agent:    tc.name,
			Status:   daemon.StatusStopped,
		}); err != nil {
			t.Fatalf("write metadata %s: %v", tc.name, err)
		}
		writeChildLogForTest(t, root, tc.name, tc.body)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "--all", "--grep", "error", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs --all grep local: %v\nstderr=%s", err, stderr.String())
	}
	want := "manager              | manager error\nworker               | worker error\n"
	if got := out.String(); got != want {
		t.Fatalf("logs --all grep output = %q, want %q", got, want)
	}
}

func TestLogsAllSinceUsesLocalModifiedTimeWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	oldAt := time.Now().Add(-2 * time.Hour)
	for _, tc := range []struct {
		name    string
		agent   string
		content string
		modTime time.Time
	}{
		{name: "old", agent: "worker", content: "old line\n", modTime: oldAt},
		{name: "recent", agent: "manager", content: "recent line\n", modTime: time.Now()},
	} {
		if err := daemon.WriteMetadata(root, &daemon.Metadata{
			Instance: tc.name,
			Agent:    tc.agent,
			Status:   daemon.StatusStopped,
		}); err != nil {
			t.Fatalf("write metadata %s: %v", tc.name, err)
		}
		writeChildLogForTest(t, root, tc.name, tc.content)
		setChildLogModTimeForTest(t, root, tc.name, tc.modTime)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "--all", "--since", "1h", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs --all --since local: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "recent               | recent line\n"; got != want {
		t.Fatalf("logs --all --since output = %q, want %q", got, want)
	}
}

func TestLogsSingleSinceOlderLogPrintsNoMatching(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	writeChildLogForTest(t, root, "manager", "old line\n")
	setChildLogModTimeForTest(t, root, "manager", time.Now().Add(-2*time.Hour))

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "manager", "--since", "1h", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs manager --since local: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "(no matching logs)\n"; got != want {
		t.Fatalf("logs manager --since output = %q, want %q", got, want)
	}
}

func TestLogsListUsesLocalMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	writeChildLogForTest(t, root, "manager", "alpha\n")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "--list", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs --list local: %v\nstderr=%s", err, stderr.String())
	}
	var rows []logListRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode logs list: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "manager" || rows[0].Status != "stopped" || !rows[0].Exists {
		t.Fatalf("rows = %+v, want stopped manager with log", rows)
	}
}

func TestLogsJobFilterUsesLocalJobOwnershipWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-201",
			Ticket:    "SQU-201",
			Target:    "worker",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-201",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "squ-202",
			Ticket:    "SQU-202",
			Target:    "worker",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-202",
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}
	for _, meta := range []*daemon.Metadata{
		{Instance: "worker-squ-201", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, StartedAt: now.Add(time.Minute)},
		{Instance: "worker-squ-202", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, StartedAt: now.Add(2 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeChildLogForTest(t, root, "worker-squ-201", "owned first\nowned latest\n")
	writeChildLogForTest(t, root, "worker-squ-202", "foreign first\nforeign latest\n")
	writeLastMessageForTest(t, teamDir, "worker-squ-201", "owned final")
	writeLastMessageForTest(t, teamDir, "worker-squ-202", "foreign final")

	list := NewRootCmd()
	listOut, listErr := &bytes.Buffer{}, &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetErr(listErr)
	list.SetArgs([]string{"logs", "--list", "--json", "--job", "SQU-201", "--target", tmp})
	if err := list.Execute(); err != nil {
		t.Fatalf("logs --list --job: %v\nstderr=%s", err, listErr.String())
	}
	var rows []logListRow
	if err := json.Unmarshal(listOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode logs list job: %v\nbody=%s", err, listOut.String())
	}
	if len(rows) != 1 || rows[0].Instance != "worker-squ-201" || rows[0].JobID != "squ-201" || rows[0].Ticket != "SQU-201" {
		t.Fatalf("job-filtered rows = %+v", rows)
	}

	raw := NewRootCmd()
	rawOut, rawErr := &bytes.Buffer{}, &bytes.Buffer{}
	raw.SetOut(rawOut)
	raw.SetErr(rawErr)
	raw.SetArgs([]string{"logs", "--job", "squ-201", "--latest", "--tail", "1", "--target", tmp})
	if err := raw.Execute(); err != nil {
		t.Fatalf("logs --job latest: %v\nstderr=%s", err, rawErr.String())
	}
	if got := rawOut.String(); got != "owned latest\n" {
		t.Fatalf("job-filtered latest log = %q", got)
	}

	last := NewRootCmd()
	lastOut, lastErr := &bytes.Buffer{}, &bytes.Buffer{}
	last.SetOut(lastOut)
	last.SetErr(lastErr)
	last.SetArgs([]string{"logs", "--job", "squ-201", "--latest", "--last-message", "--target", tmp})
	if err := last.Execute(); err != nil {
		t.Fatalf("logs --job last-message: %v\nstderr=%s", err, lastErr.String())
	}
	if got := lastOut.String(); got != "owned final\n" {
		t.Fatalf("job-filtered last message = %q", got)
	}
}

func TestLogsListJSONIncludesStale(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusStopped},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusStopped},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		writeChildLogForTest(t, root, meta.Instance, meta.Instance+"\n")
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `
[status]
phase = "implementing"
description = "stuck"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "worker"), `
[status]
phase = "idle"
description = "waiting"
`, old)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "--list", "--stale", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs --list --stale --json: %v\nstderr=%s", err, stderr.String())
	}
	var rows []logListRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode logs list stale: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "manager" || !rows[0].Stale {
		t.Fatalf("rows = %+v, want stale manager only", rows)
	}
}

func TestLogsListJSONUnhealthyIncludesCrashedAndStale(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "crashed", Agent: "worker", Status: daemon.StatusCrashed, StartedAt: old},
		{Instance: "fresh", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now},
		{Instance: "stale", Agent: "manager", Status: daemon.StatusStopped, StartedAt: old},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		writeChildLogForTest(t, root, meta.Instance, meta.Instance+"\n")
	}
	writeStatus(t, filepath.Join(teamDir, "state", "stale"), `
[status]
phase = "implementing"
description = "stuck"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "fresh"), `
[status]
phase = "idle"
description = "fresh"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "--list", "--unhealthy", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs --list --unhealthy --json: %v\nstderr=%s", err, stderr.String())
	}
	var rows []logListRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode logs list unhealthy: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 2 || rows[0].Instance != "crashed" || rows[1].Instance != "stale" {
		t.Fatalf("rows = %+v, want crashed and stale rows", rows)
	}
	if rows[0].Status != string(daemon.StatusCrashed) || !rows[1].Stale {
		t.Fatalf("rows = %+v, want crashed row and stale row", rows)
	}
}

func TestLogsListJSONUnhealthyIncludesRuntimeStale(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	for _, meta := range []*daemon.Metadata{
		{Instance: "fresh", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now},
		{Instance: "runtime-stale", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999999, StartedAt: now.Add(-time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		writeChildLogForTest(t, root, meta.Instance, meta.Instance+"\n")
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "--list", "--unhealthy", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs --list --unhealthy --json: %v\nstderr=%s", err, stderr.String())
	}
	var rows []logListRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode logs list unhealthy: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "runtime-stale" || !rows[0].RuntimeStale || !rows[0].Unhealthy || rows[0].Stale {
		t.Fatalf("rows = %+v, want one runtime-stale unhealthy row", rows)
	}
}

func TestLogsListJSONRuntimeStaleFiltersOnlyRuntimeStale(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "crashed", Agent: "worker", Runtime: "codex", Status: daemon.StatusCrashed, PID: 99999998, StartedAt: now.Add(-2 * time.Minute)},
		{Instance: "runtime-stale", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999999, StartedAt: now.Add(-time.Minute)},
		{Instance: "fresh", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		writeChildLogForTest(t, root, meta.Instance, meta.Instance+"\n")
	}
	writeStatus(t, filepath.Join(teamDir, "state", "status-stale"), `[status]
phase = "implementing"
description = "stale status only"
`, old)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "--list", "--runtime-stale", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs --list --runtime-stale --json: %v\nstderr=%s", err, stderr.String())
	}
	var rows []logListRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode logs list runtime-stale: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "runtime-stale" || !rows[0].RuntimeStale || !rows[0].Unhealthy || rows[0].Stale {
		t.Fatalf("rows = %+v, want one runtime-stale row only", rows)
	}
}

func TestLogsListTableIncludesPhaseAndStale(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	old := time.Now().Add(-staleAfter - time.Minute)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	writeChildLogForTest(t, root, "manager", "alpha\n")
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `
[status]
phase = "blocked"
description = "stuck"
`, old)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "--list", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs --list table: %v\nstderr=%s", err, stderr.String())
	}
	got := out.String()
	for _, want := range []string{"PHASE", "STALE", "blocked", "yes", "manager"} {
		if !strings.Contains(got, want) {
			t.Fatalf("logs list table missing %q:\n%s", want, got)
		}
	}
}

func TestLogsListSinceFiltersModifiedStreams(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	oldAt := time.Now().Add(-2 * time.Hour)
	for _, tc := range []struct {
		name    string
		modTime time.Time
	}{
		{name: "old", modTime: oldAt},
		{name: "recent", modTime: time.Now()},
	} {
		if err := daemon.WriteMetadata(root, &daemon.Metadata{
			Instance: tc.name,
			Agent:    "manager",
			Status:   daemon.StatusStopped,
		}); err != nil {
			t.Fatalf("write metadata %s: %v", tc.name, err)
		}
		writeChildLogForTest(t, root, tc.name, tc.name+"\n")
		setChildLogModTimeForTest(t, root, tc.name, tc.modTime)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "--list", "--json", "--since", "1h", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs --list --since: %v\nstderr=%s", err, stderr.String())
	}
	var rows []logListRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode logs list since: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "recent" || !rows[0].Exists || rows[0].ModifiedAt == "" {
		t.Fatalf("rows = %+v, want only recent existing row", rows)
	}
}

func TestLogListRowsNormalizeMissingStatus(t *testing.T) {
	tmp := t.TempDir()
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	meta := &daemon.Metadata{
		Instance:      "manager",
		Agent:         "manager",
		Runtime:       "codex",
		RuntimeBinary: "codex-dev",
	}

	rows, err := logListRowsFromMetadata(teamDir, []*daemon.Metadata{meta})
	if err != nil {
		t.Fatalf("logListRowsFromMetadata: %v", err)
	}
	if len(rows) != 1 || rows[0].Status != "unknown" || rows[0].Runtime != "codex" || rows[0].RuntimeBinary != "codex-dev" {
		t.Fatalf("rows = %+v, want unknown status", rows)
	}

	body, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal rows: %v", err)
	}
	if !strings.Contains(string(body), `"status":"unknown"`) {
		t.Fatalf("json rows = %s, want explicit unknown status", body)
	}
	if !strings.Contains(string(body), `"runtime":"codex"`) || !strings.Contains(string(body), `"runtime_binary":"codex-dev"`) {
		t.Fatalf("json rows = %s, want runtime metadata", body)
	}

	tmpl, err := parseLogListFormat(`{{.Instance}}:{{.Status}}`)
	if err != nil {
		t.Fatalf("parseLogListFormat: %v", err)
	}
	var out bytes.Buffer
	if err := renderLogListFormat(&out, rows, tmpl); err != nil {
		t.Fatalf("renderLogListFormat: %v", err)
	}
	if got, want := out.String(), "manager:unknown\n"; got != want {
		t.Fatalf("formatted rows = %q, want %q", got, want)
	}

	if got, want := rows[0].LogPath, displayPathFromTeamDir(teamDir, filepath.Join(root, "manager", "child.log")); got != want {
		t.Fatalf("log path = %q, want %q", got, want)
	}
}

func TestLogsSinceRejectsFollow(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "manager", "--since", "1h", "--follow"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --since --follow validation error")
	}
	if !strings.Contains(stderr.String(), "--since cannot be combined with --follow") {
		t.Fatalf("stderr = %q, want --since follow validation", stderr.String())
	}
}

func TestLogsGrepValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"logs", "manager", "--grep", "error", "--follow"}, "--grep cannot be combined with --follow"},
		{[]string{"logs", "--list", "--grep", "error"}, "--grep cannot be combined with --list"},
		{[]string{"logs", "manager", "--grep", "["}, "invalid --grep pattern"},
		{[]string{"logs", "manager", "--last-message", "--follow"}, "--last-message cannot be combined with --follow"},
		{[]string{"logs", "manager", "--last-message", "--clean"}, "--last-message cannot be combined with --clean"},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
	}
}

func TestAttachFilterValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"attach", "manager", "--grep", "error"}, "--grep requires --no-follow"},
		{[]string{"attach", "manager", "--since", "1h"}, "--since requires --no-follow"},
		{[]string{"attach", "manager", "--no-follow", "--grep", "["}, "invalid --grep pattern"},
		{[]string{"attach", "manager", "--no-follow", "--since", "-1h"}, "--since duration must be >= 0"},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
	}
}

func TestParseLogSince(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	got, err := parseLogSince("30m", func() time.Time { return now })
	if err != nil {
		t.Fatalf("parse duration since: %v", err)
	}
	if got == nil || !got.Equal(now.Add(-30*time.Minute)) {
		t.Fatalf("since = %v, want %v", got, now.Add(-30*time.Minute))
	}
	if _, err := parseLogSince("-1m", func() time.Time { return now }); err == nil || !strings.Contains(err.Error(), "duration must be >= 0") {
		t.Fatalf("negative duration err = %v, want validation", err)
	}
}

func TestLogs_NegativeTailFailsFast(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "any-instance", "--target", tmp, "--tail", "-1"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected exit error")
	}
	if !strings.Contains(stderr.String(), "--tail must be >= 0") {
		t.Errorf("missing tail validation: %q", stderr.String())
	}
}

func TestAttachNegativeTailFailsFast(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"attach", "any-instance", "--target", tmp, "--tail", "-1"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected exit error")
	}
	if !strings.Contains(stderr.String(), "--tail must be >= 0") {
		t.Errorf("missing tail validation: %q", stderr.String())
	}
}

func TestParseLogTail(t *testing.T) {
	cases := []struct {
		raw  string
		want int
		ok   bool
	}{
		{raw: "", want: 0, ok: true},
		{raw: "0", want: 0, ok: true},
		{raw: "all", want: 0, ok: true},
		{raw: "ALL", want: 0, ok: true},
		{raw: " 5 ", want: 5, ok: true},
		{raw: "-1", ok: false},
		{raw: "latest", ok: false},
	}
	for _, tc := range cases {
		got, err := parseLogTail(tc.raw)
		if tc.ok {
			if err != nil || got != tc.want {
				t.Fatalf("parseLogTail(%q) = %d, %v; want %d, nil", tc.raw, got, err, tc.want)
			}
			continue
		}
		if err == nil {
			t.Fatalf("parseLogTail(%q) succeeded unexpectedly with %d", tc.raw, got)
		}
	}
}

func TestLogsJSONRequiresList(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "manager", "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--json requires --list") {
		t.Fatalf("stderr = %q, want --json validation", stderr.String())
	}
}

func TestLogsListRejectsContentFlags(t *testing.T) {
	for _, args := range [][]string{
		{"logs", "manager", "--list"},
		{"logs", "--list", "--all"},
		{"logs", "--list", "--daemon"},
		{"logs", "--list", "--follow"},
		{"logs", "--list", "--tail", "1"},
		{"logs", "--list", "--tail", "all"},
	} {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", args)
		}
		if !strings.Contains(stderr.String(), "--list cannot be combined") {
			t.Fatalf("%v: stderr = %q, want --list validation", args, stderr.String())
		}
	}
}

func TestLogsListFormatPrintsRows(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-logs-format-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	// Resolve symlinks (macOS /tmp -> /private/tmp) so the metadata log path
	// and the team dir share one base and the display path is always
	// repo-relative, matching Linux.
	if resolved, err := filepath.EvalSymlinks(tmp); err == nil {
		tmp = resolved
	}
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	m := daemon.NewInstanceManager(root, nil)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusRunning,
		PID:      123,
		LogPath:  filepath.Join(root, "manager", "child.log"),
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	writeChildLogForTest(t, root, "manager", "alpha\n")
	if err := m.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	cleanup := startRunTestDaemon(t, teamDir, m)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "--list", "--format", "{{.Instance}}:{{.Agent}}:{{.Status}}:{{.Size}}:{{.LogPath}}", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs --list --format: %v\nstderr: %s", err, stderr.String())
	}
	got := out.String()
	if want := "manager:manager:running:6B:.agent_team/daemon/manager/child.log\n"; got != want {
		t.Fatalf("formatted logs = %q, want %q", got, want)
	}
}

func TestLogsListFormatValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"logs", "--format", "{{.Instance}}"}, "--format requires --list"},
		{[]string{"logs", "--list", "--format", "{{.Instance}}", "--json"}, "--format cannot be combined with --json"},
		{[]string{"logs", "--list", "--format", "{{"}, "invalid --format template"},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
	}
}

func TestLogsFiltersRejectSingleInstanceAndDaemonLog(t *testing.T) {
	for _, args := range [][]string{
		{"logs", "manager", "--status", "running"},
		{"logs", "manager", "--agent", "manager"},
		{"logs", "manager", "--phase", "blocked"},
		{"logs", "manager", "--stale"},
		{"logs", "manager", "--runtime-stale"},
		{"logs", "manager", "--unhealthy"},
		{"logs", "--daemon", "--agent", "manager"},
		{"logs", "--daemon", "--phase", "blocked"},
		{"logs", "--daemon", "--stale"},
		{"logs", "--daemon", "--runtime-stale"},
		{"logs", "--daemon", "--unhealthy"},
	} {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", args)
		}
		if !strings.Contains(stderr.String(), "cannot be combined") {
			t.Fatalf("%v: stderr = %q, want conflict validation", args, stderr.String())
		}
	}
}

func TestLogListOptionsValidateStatus(t *testing.T) {
	if _, err := newLogListOptions([]string{"bogus"}, nil, nil, false); err == nil || !strings.Contains(err.Error(), "unknown --status") {
		t.Fatalf("expected unknown status error, got %v", err)
	}
	if _, err := newLogListOptionsWithRuntimeAndUnhealthy(nil, []string{"llama"}, nil, nil, false, false); err == nil || !strings.Contains(err.Error(), "unknown --runtime") {
		t.Fatalf("expected unknown runtime error, got %v", err)
	}
	opts, err := newLogListOptionsWithRuntimeAndUnhealthy([]string{"running, stopped"}, []string{"codex"}, []string{"manager,worker"}, []string{"blocked,unknown"}, true, false)
	if err != nil {
		t.Fatalf("newLogListOptions: %v", err)
	}
	if !opts.statuses["running"] || !opts.statuses["stopped"] || !opts.runtimes["codex"] || !opts.agents["manager"] || !opts.agents["worker"] || !opts.phases["blocked"] || !opts.phases["unknown"] {
		t.Fatalf("opts = %+v, want running/stopped manager/worker filters", opts)
	}
	if !opts.stale || !logListOptionsHasFilters(opts) {
		t.Fatalf("opts = %+v, want stale filter enabled", opts)
	}
	unhealthy, err := newLogListOptionsWithUnhealthy(nil, nil, nil, false, true)
	if err != nil {
		t.Fatalf("newLogListOptionsWithUnhealthy: %v", err)
	}
	if !unhealthy.unhealthy || !logListOptionsHasFilters(unhealthy) {
		t.Fatalf("opts = %+v, want unhealthy filter enabled", unhealthy)
	}
}

func TestLogListOptionsRejectEmptyFilters(t *testing.T) {
	cases := []struct {
		name     string
		statuses []string
		runtimes []string
		agents   []string
		phases   []string
		want     string
	}{
		{name: "status", statuses: []string{"  "}, want: "non-empty status"},
		{name: "runtime", runtimes: []string{"  "}, want: "non-empty runtime"},
		{name: "agent", agents: []string{"  "}, want: "non-empty agent"},
		{name: "phase", phases: []string{"  "}, want: "non-empty phase"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newLogListOptionsWithRuntimeAndUnhealthy(tc.statuses, tc.runtimes, tc.agents, tc.phases, false, false)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestFilterLogListRows(t *testing.T) {
	rows := []logListRow{
		{Instance: "manager", Agent: "manager", Runtime: "codex", Status: "running", Phase: "blocked"},
		{Instance: "worker-1", Agent: "worker", Runtime: "claude", Status: "stopped", Phase: "idle"},
		{Instance: "unknown", Agent: "manager", Runtime: "codex"},
	}
	opts, err := newLogListOptionsWithRuntimeAndUnhealthy([]string{"running, unknown"}, []string{"codex"}, []string{"manager"}, []string{"blocked,unknown"}, false, false)
	if err != nil {
		t.Fatalf("newLogListOptions: %v", err)
	}
	got := filterLogListRows(rows, opts)
	if len(got) != 2 || got[0].Instance != "manager" || got[1].Instance != "unknown" {
		t.Fatalf("filtered rows = %+v, want manager and unknown", got)
	}
}

func TestFilterLogListRowsJob(t *testing.T) {
	rows := []logListRow{
		{Instance: "worker-squ-201", JobID: "squ-201", Ticket: "SQU-201"},
		{Instance: "worker-squ-202", Ticket: "SQU-202"},
		{Instance: "worker-squ-203"},
	}
	got := filterLogListRows(rows, logListOptions{jobs: map[string]bool{"squ-202": true}})
	if len(got) != 1 || got[0].Instance != "worker-squ-202" {
		t.Fatalf("filtered rows = %+v, want squ-202 by ticket", got)
	}
}

func TestFilterLogListRowsUnhealthy(t *testing.T) {
	rows := []logListRow{
		{Instance: "crashed", Agent: "worker", Status: string(daemon.StatusCrashed)},
		{Instance: "fresh", Agent: "worker", Status: "running"},
		{Instance: "stale", Agent: "manager", Status: "stopped", Stale: true},
	}
	opts, err := newLogListOptionsWithUnhealthy(nil, nil, nil, false, true)
	if err != nil {
		t.Fatalf("newLogListOptionsWithUnhealthy: %v", err)
	}
	got := filterLogListRows(rows, opts)
	if len(got) != 2 || got[0].Instance != "crashed" || got[1].Instance != "stale" {
		t.Fatalf("filtered rows = %+v, want crashed and stale", got)
	}
}

func TestLogsDaemonReadsLocalLogWithoutDaemon(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.MkdirAll(daemon.DaemonRoot(teamDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon.LogPath(teamDir), []byte("first\nlast\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"logs", "--daemon", "--tail", "1", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs --daemon: %v", err)
	}
	if got := out.String(); got != "last\n" {
		t.Fatalf("daemon log output = %q, want last line", got)
	}
}

func TestLogsDaemonGrep(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.MkdirAll(daemon.DaemonRoot(teamDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon.LogPath(teamDir), []byte("info boot\nerror failed\ninfo done\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"logs", "--daemon", "--grep", "error", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs --daemon --grep: %v", err)
	}
	if got, want := out.String(), "error failed\n"; got != want {
		t.Fatalf("daemon grep output = %q, want %q", got, want)
	}
}

func TestLogsDaemonTailAllReadsWholeLog(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.MkdirAll(daemon.DaemonRoot(teamDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon.LogPath(teamDir), []byte("first\nlast\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"logs", "--daemon", "--tail", "all", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs --daemon --tail all: %v", err)
	}
	if got := out.String(); got != "first\nlast\n" {
		t.Fatalf("daemon log output = %q, want whole log", got)
	}
}

func TestLogsDaemonMissingLogReturnsHint(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "--daemon", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected missing daemon log to fail")
	}
	if !strings.Contains(stderr.String(), "daemon log not found") || !strings.Contains(stderr.String(), "agent-team start") {
		t.Fatalf("stderr = %q, want daemon log hint", stderr.String())
	}
}

func TestLogsDaemonRejectsAllAndInstance(t *testing.T) {
	for _, args := range [][]string{
		{"logs", "--daemon", "--all"},
		{"logs", "--daemon", "manager"},
	} {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", args)
		}
		if !strings.Contains(stderr.String(), "--daemon cannot be combined") {
			t.Fatalf("%v: stderr = %q, want --daemon validation", args, stderr.String())
		}
	}
}

func TestLogsRequiresInstanceUnlessAll(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "instance is required unless --all, --latest, --last, --status, --runtime, --agent, --phase, --job, --stale, --runtime-stale, or --unhealthy is set") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestCollectLogListRows(t *testing.T) {
	tmp := t.TempDir()
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	m := daemon.NewInstanceManager(root, nil)
	for _, meta := range []*daemon.Metadata{
		{
			Instance: "worker",
			Agent:    "worker",
			Status:   daemon.StatusStopped,
			LogPath:  filepath.Join(root, "worker", "child.log"),
		},
		{
			Instance: "manager",
			Agent:    "manager",
			Status:   daemon.StatusRunning,
			PID:      123,
			LogPath:  filepath.Join(root, "manager", "child.log"),
		},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeChildLogForTest(t, root, "manager", "alpha\n")
	if err := m.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	c, cleanup := newTestClient(t, daemon.Handler(m, nil, nil, ""))
	defer cleanup()

	rows, err := collectLogListRows(teamDir, c)
	if err != nil {
		t.Fatalf("collectLogListRows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want two", rows)
	}
	if rows[0].Instance != "manager" || !rows[0].Exists || rows[0].SizeBytes != int64(len("alpha\n")) || rows[0].PID != 123 {
		t.Fatalf("manager row = %+v", rows[0])
	}
	if rows[1].Instance != "worker" || rows[1].Exists {
		t.Fatalf("worker row = %+v, want missing log", rows[1])
	}
	var buf bytes.Buffer
	renderLogList(&buf, rows)
	for _, want := range []string{"INSTANCE", "manager", "running", "6B", ".agent_team/daemon/manager/child.log"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("rendered log list missing %q:\n%s", want, buf.String())
		}
	}

	tmpl, err := parseLogListFormat("{{.Instance}} {{.Size}} {{.Exists}}")
	if err != nil {
		t.Fatalf("parseLogListFormat: %v", err)
	}
	buf.Reset()
	if err := renderLogListFormat(&buf, rows, tmpl); err != nil {
		t.Fatalf("renderLogListFormat: %v", err)
	}
	if got, want := buf.String(), "manager 6B true\nworker - false\n"; got != want {
		t.Fatalf("formatted log list = %q, want %q", got, want)
	}

	var jsonBuf bytes.Buffer
	if err := json.NewEncoder(&jsonBuf).Encode(rows); err != nil {
		t.Fatalf("encode rows: %v", err)
	}
	if !strings.Contains(jsonBuf.String(), `"exists":true`) {
		t.Fatalf("json rows missing exists=true: %s", jsonBuf.String())
	}
}

func TestLogsListLastUsesNewestRows(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "mid", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-30 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		writeChildLogForTest(t, root, meta.Instance, meta.Instance+"\n")
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "--list", "--last", "2", "--format", "{{.Instance}}", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logs --list --last: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "new\nmid\n"; got != want {
		t.Fatalf("logs --list --last output = %q, want %q", got, want)
	}
}

func TestLogInstanceNamesWithOptionsFiltersRows(t *testing.T) {
	tmp := t.TempDir()
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	m := daemon.NewInstanceManager(root, nil)
	for _, meta := range []*daemon.Metadata{
		{Instance: "worker", Agent: "worker", Status: daemon.StatusRunning},
		{Instance: "adhoc", Agent: "manager", Status: daemon.StatusRunning},
		{Instance: "manager", Agent: "manager", Status: daemon.StatusStopped},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	if err := m.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	c, cleanup := newTestClient(t, daemon.Handler(m, nil, nil, ""))
	defer cleanup()
	opts, err := newLogListOptions([]string{"running"}, []string{"manager"}, nil, false)
	if err != nil {
		t.Fatalf("newLogListOptions: %v", err)
	}

	names, err := logInstanceNamesWithOptions(teamDir, c, opts, 0)
	if err != nil {
		t.Fatalf("logInstanceNamesWithOptions: %v", err)
	}
	if strings.Join(names, ",") != "adhoc" {
		t.Fatalf("names = %v, want only running manager-agent adhoc", names)
	}
}

func TestLogInstanceNamesWithOptionsLimitUsesNewestRows(t *testing.T) {
	tmp := t.TempDir()
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	m := daemon.NewInstanceManager(root, nil)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "mid", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-30 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	if err := m.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	c, cleanup := newTestClient(t, daemon.Handler(m, nil, nil, ""))
	defer cleanup()

	names, err := logInstanceNamesWithOptions(teamDir, c, logListOptions{}, 2)
	if err != nil {
		t.Fatalf("logInstanceNamesWithOptions: %v", err)
	}
	if strings.Join(names, ",") != "new,mid" {
		t.Fatalf("names = %v, want newest two", names)
	}
}

func TestLatestLogListRowUsesNewestStartedAt(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	row, ok := latestLogListRow([]logListRow{
		{Instance: "old", startedAt: now.Add(-2 * time.Hour)},
		{Instance: "missing"},
		{Instance: "new", startedAt: now.Add(-5 * time.Minute)},
		{Instance: "mid", startedAt: now.Add(-30 * time.Minute)},
	})
	if !ok {
		t.Fatalf("latestLogListRow returned no row")
	}
	if row.Instance != "new" {
		t.Fatalf("latest row = %+v, want new", row)
	}
}

func TestLatestLogListRowsLimitUsesNewestStartedAt(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	rows := latestLogListRowsLimit([]logListRow{
		{Instance: "old", startedAt: now.Add(-2 * time.Hour)},
		{Instance: "missing"},
		{Instance: "new", startedAt: now.Add(-5 * time.Minute)},
		{Instance: "mid", startedAt: now.Add(-30 * time.Minute)},
	}, 2)
	if got := rows[0].Instance + "," + rows[1].Instance; got != "new,mid" {
		t.Fatalf("latest rows = %s, want new,mid", got)
	}
}

func TestStreamLocalLogFollowStopsOnContextCancel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-teamd.log")
	if err := os.WriteFile(path, []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := streamLocalLog(ctx, &buf, path, true, 0, nil, false, false); err != nil {
		t.Fatalf("streamLocalLog: %v", err)
	}
	if got := buf.String(); got != "seed\n" {
		t.Fatalf("follow output = %q, want seed", got)
	}
}

func TestLogsAllRejectsInstanceName(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "manager", "--all"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--all cannot be combined") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestLogsLatestValidation(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"logs", "manager", "--latest"}, "--latest cannot be combined with an instance name"},
		{[]string{"logs", "manager", "--last", "2"}, "--last cannot be combined with an instance name"},
		{[]string{"logs", "--last", "-1"}, "--last must be >= 0"},
		{[]string{"logs", "--latest", "--last", "2"}, "choose one of --latest or --last"},
		{[]string{"logs", "--latest", "--all"}, "--latest cannot be combined with --all"},
		{[]string{"logs", "--last", "2", "--all"}, "--last cannot be combined with --all"},
		{[]string{"logs", "--latest", "--daemon"}, "--latest cannot be combined with --daemon"},
		{[]string{"logs", "--last", "2", "--daemon"}, "--last cannot be combined with --daemon"},
		{[]string{"logs", "--latest", "--list"}, "--latest cannot be combined with --list"},
	} {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
	}
}

func TestLogsNoPrefixRequiresMultiInstanceSelection(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"logs", "manager", "--no-prefix"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--no-prefix requires --all, --latest, --last, --status, --runtime, --agent, --phase, --job, --stale, --runtime-stale, or --unhealthy") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestAttachLatestValidation(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"attach"}, "instance is required unless --all, --latest, --last, --status, --runtime, --agent, --phase, --stale, --runtime-stale, or --unhealthy is set"},
		{[]string{"attach", "manager", "--all"}, "--all cannot be combined with an instance name"},
		{[]string{"attach", "manager", "--latest"}, "--latest cannot be combined with an instance name"},
		{[]string{"attach", "manager", "--last", "2"}, "--last cannot be combined with an instance name"},
		{[]string{"attach", "manager", "--runtime", "codex"}, "--status, --runtime, --agent, --phase, --stale, --runtime-stale, and --unhealthy cannot be combined with an instance name"},
		{[]string{"attach", "manager", "--agent", "worker"}, "--status, --runtime, --agent, --phase, --stale, --runtime-stale, and --unhealthy cannot be combined with an instance name"},
		{[]string{"attach", "manager", "--phase", "blocked"}, "--status, --runtime, --agent, --phase, --stale, --runtime-stale, and --unhealthy cannot be combined with an instance name"},
		{[]string{"attach", "manager", "--stale"}, "--status, --runtime, --agent, --phase, --stale, --runtime-stale, and --unhealthy cannot be combined with an instance name"},
		{[]string{"attach", "manager", "--runtime-stale"}, "--status, --runtime, --agent, --phase, --stale, --runtime-stale, and --unhealthy cannot be combined with an instance name"},
		{[]string{"attach", "manager", "--unhealthy"}, "--status, --runtime, --agent, --phase, --stale, --runtime-stale, and --unhealthy cannot be combined with an instance name"},
		{[]string{"attach", "--last", "-1"}, "--last must be >= 0"},
		{[]string{"attach", "--latest", "--last", "2"}, "choose one of --latest or --last"},
		{[]string{"attach", "--latest", "--all"}, "--latest cannot be combined with --all"},
		{[]string{"attach", "--last", "2", "--all"}, "--last cannot be combined with --all"},
		{[]string{"attach", "--status", "bogus"}, "unknown --status"},
		{[]string{"attach", "--runtime", "llama"}, "unknown --runtime"},
		{[]string{"attach", "--phase", "reviewing"}, "unknown --phase"},
	} {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
	}
}

func TestPrefixLineWriterPrefixesEveryLine(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	w := newPrefixLineWriter(&buf, "manager", &mu)
	if _, err := w.Write([]byte("one\ntwo")); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	if _, err := w.Write([]byte("\nthree\n")); err != nil {
		t.Fatalf("write 2: %v", err)
	}
	want := "manager              | one\nmanager              | two\nmanager              | three\n"
	if got := buf.String(); got != want {
		t.Fatalf("prefixed output = %q, want %q", got, want)
	}
}

func TestLogsAllStreamsSortedPrefixedLogs(t *testing.T) {
	root := t.TempDir()
	m := daemon.NewInstanceManager(root, nil)
	for _, instance := range []string{"worker", "manager"} {
		if err := daemon.WriteMetadata(root, &daemon.Metadata{
			Instance: instance,
			Agent:    instance,
			Status:   daemon.StatusStopped,
		}); err != nil {
			t.Fatalf("write metadata %s: %v", instance, err)
		}
	}
	if err := m.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	writeChildLogForTest(t, root, "worker", "worker old\nworker last\n")
	writeChildLogForTest(t, root, "manager", "manager old\nmanager last\n")
	c, cleanup := newTestClient(t, daemon.Handler(m, nil, nil, ""))
	defer cleanup()

	names, err := logInstanceNames(c)
	if err != nil {
		t.Fatalf("logInstanceNames: %v", err)
	}
	var buf bytes.Buffer
	if err := streamAllLogsOnce(context.Background(), &buf, c, names, 1, true, false); err != nil {
		t.Fatalf("streamAllLogsOnce: %v", err)
	}
	want := "manager              | manager last\nworker               | worker last\n"
	if got := buf.String(); got != want {
		t.Fatalf("logs = %q, want %q", got, want)
	}
}

func TestLogsAllNoPrefixStreamsSortedRawLogs(t *testing.T) {
	root := t.TempDir()
	m := daemon.NewInstanceManager(root, nil)
	for _, instance := range []string{"worker", "manager"} {
		if err := daemon.WriteMetadata(root, &daemon.Metadata{
			Instance: instance,
			Agent:    instance,
			Status:   daemon.StatusStopped,
		}); err != nil {
			t.Fatalf("write metadata %s: %v", instance, err)
		}
	}
	if err := m.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	writeChildLogForTest(t, root, "worker", "worker old\nworker last\n")
	writeChildLogForTest(t, root, "manager", "manager old\nmanager last\n")
	c, cleanup := newTestClient(t, daemon.Handler(m, nil, nil, ""))
	defer cleanup()

	names, err := logInstanceNames(c)
	if err != nil {
		t.Fatalf("logInstanceNames: %v", err)
	}
	var buf bytes.Buffer
	if err := streamAllLogsOnce(context.Background(), &buf, c, names, 1, false, false); err != nil {
		t.Fatalf("streamAllLogsOnce: %v", err)
	}
	want := "manager last\nworker last\n"
	if got := buf.String(); got != want {
		t.Fatalf("logs = %q, want %q", got, want)
	}
}

func TestLogsAllFollowStopsOnContextCancel(t *testing.T) {
	root := t.TempDir()
	m := daemon.NewInstanceManager(root, nil)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusRunning,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	if err := m.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	writeChildLogForTest(t, root, "manager", "seed\n")
	c, cleanup := newTestClient(t, daemon.Handler(m, nil, nil, ""))
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := streamAllLogsFollow(ctx, &buf, c, []string{"manager"}, 0, true, false); err != nil {
		t.Fatalf("streamAllLogsFollow: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "manager              | seed\n") {
		t.Fatalf("follow output = %q, want seeded log", got)
	}
}

func TestAttachTailAllReadsWholeInstanceLog(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-attach-tail-all-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	m := daemon.NewInstanceManager(root, nil)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	if err := m.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	writeChildLogForTest(t, root, "manager", "first\nlast\n")
	cleanup := startRunTestDaemon(t, teamDir, m)
	defer cleanup()

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"attach", "manager", "--no-follow", "--tail", "all", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("attach --tail all: %v", err)
	}
	if got := out.String(); got != "first\nlast\n" {
		t.Fatalf("attach output = %q, want whole instance log", got)
	}
}
