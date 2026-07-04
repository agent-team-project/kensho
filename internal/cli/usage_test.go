package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/origin"
	"github.com/jamesaud/agent-team/internal/usage"
)

func TestUsageCommandRollsUpByRuntimeAndFiltersSince(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC().Truncate(time.Second)
	writeUsageJobForTest(t, teamDir, "SQU-73", usage.Record{
		Instance:          "worker-squ-73",
		Agent:             "worker",
		Runtime:           "codex",
		TokensAvailable:   true,
		InputTokens:       100,
		CachedInputTokens: 90,
		OutputTokens:      10,
		Turns:             1,
		DurationMS:        2000,
		StartedAt:         now.Add(-time.Hour),
		EndedAt:           now.Add(-50 * time.Minute),
	})
	writeUsageJobForTest(t, teamDir, "SQU-72", usage.Record{
		Instance:          "worker-squ-72",
		Agent:             "worker",
		Runtime:           "codex",
		TokensAvailable:   true,
		InputTokens:       900,
		CachedInputTokens: 850,
		OutputTokens:      40,
		Turns:             1,
		DurationMS:        3000,
		StartedAt:         now.Add(-10 * 24 * time.Hour),
		EndedAt:           now.Add(-10*24*time.Hour + time.Minute),
	})

	root := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance:  "worker-squ-73",
		Agent:     "worker",
		Job:       "squ-73",
		Ticket:    "SQU-73",
		Runtime:   "codex",
		Workspace: tmp,
		Status:    daemon.StatusExited,
		StartedAt: now.Add(-time.Hour),
		ExitedAt:  now.Add(-50 * time.Minute),
		Usage: &usage.Record{
			Instance:          "worker-squ-73",
			Agent:             "worker",
			Runtime:           "codex",
			TokensAvailable:   true,
			InputTokens:       100,
			CachedInputTokens: 90,
			OutputTokens:      10,
			Turns:             1,
			DurationMS:        2000,
			StartedAt:         now.Add(-time.Hour),
			EndedAt:           now.Add(-50 * time.Minute),
		},
	}); err != nil {
		t.Fatalf("WriteMetadata duplicate: %v", err)
	}
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance:  "manager-sidecar",
		Agent:     "manager",
		Runtime:   "claude",
		Workspace: tmp,
		Status:    daemon.StatusExited,
		StartedAt: now.Add(-30 * time.Minute),
		ExitedAt:  now.Add(-25 * time.Minute),
		Usage: &usage.Record{
			Instance:        "manager-sidecar",
			Agent:           "manager",
			Runtime:         "claude",
			TokensAvailable: false,
			Turns:           1,
			DurationMS:      int64((5 * time.Minute).Milliseconds()),
			StartedAt:       now.Add(-30 * time.Minute),
			EndedAt:         now.Add(-25 * time.Minute),
		},
	}); err != nil {
		t.Fatalf("WriteMetadata sidecar: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"usage", "--target", tmp, "--by", "runtime", "--since", "7d", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("usage: %v\nstderr=%s", err, stderr.String())
	}
	var rows []usageRollupRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode usage rows: %v\nbody=%s", err, out.String())
	}
	byRuntime := map[string]usage.Summary{}
	for _, row := range rows {
		byRuntime[row.Key] = row.Usage
	}
	if byRuntime["codex"].InputTokens != 100 || byRuntime["codex"].Runs != 1 {
		t.Fatalf("codex rollup = %+v, rows=%+v", byRuntime["codex"], rows)
	}
	if byRuntime["claude"].TokenUnavailableRuns != 1 || byRuntime["claude"].DurationMS != int64((5*time.Minute).Milliseconds()) {
		t.Fatalf("claude rollup = %+v, rows=%+v", byRuntime["claude"], rows)
	}
}

func TestUsageCommandRollsUpByTeam(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	platform := writeUsageJobForTest(t, teamDir, "SQU-90", usage.Record{
		Instance:        "platform-worker-squ-90",
		Agent:           "worker",
		Runtime:         "codex",
		TokensAvailable: true,
		InputTokens:     100,
		OutputTokens:    20,
		Turns:           1,
		StartedAt:       now.Add(-time.Hour),
		EndedAt:         now,
	})
	platform.Origin = origin.Envelope{Team: "platform"}
	platform.Usage.Records[0].Origin = platform.Origin
	if err := job.Write(teamDir, platform); err != nil {
		t.Fatalf("job.Write platform: %v", err)
	}
	delivery := writeUsageJobForTest(t, teamDir, "SQU-91", usage.Record{
		Instance:        "worker-squ-91",
		Agent:           "worker",
		Runtime:         "codex",
		TokensAvailable: true,
		InputTokens:     7,
		OutputTokens:    3,
		Turns:           1,
		StartedAt:       now.Add(-time.Hour),
		EndedAt:         now,
	})
	delivery.Origin = origin.Envelope{Team: "delivery"}
	delivery.Usage.Records[0].Origin = delivery.Origin
	if err := job.Write(teamDir, delivery); err != nil {
		t.Fatalf("job.Write delivery: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"usage", "--target", tmp, "--by", "team", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("usage: %v\nstderr=%s", err, stderr.String())
	}
	var rows []usageRollupRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode usage rows: %v\nbody=%s", err, out.String())
	}
	byTeam := map[string]usage.Summary{}
	for _, row := range rows {
		byTeam[row.Key] = row.Usage
	}
	if byTeam["platform"].InputTokens != 100 || byTeam["delivery"].InputTokens != 7 {
		t.Fatalf("team rollups = %+v rows=%+v", byTeam, rows)
	}
}

func TestJobShowAndSnapshotIncludeUsageSummary(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	writeUsageJobForTest(t, teamDir, "SQU-73", usage.Record{
		Instance:        "worker-squ-73",
		Agent:           "worker",
		Runtime:         "codex",
		TokensAvailable: true,
		InputTokens:     123,
		OutputTokens:    9,
		Turns:           1,
		DurationMS:      1500,
		StartedAt:       now.Add(-time.Hour),
		EndedAt:         now,
	})

	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"job", "show", "squ-73", "--repo", tmp})
	if err := show.Execute(); err != nil {
		t.Fatalf("job show: %v\nstderr=%s", err, showErr.String())
	}
	if !strings.Contains(showOut.String(), "Usage:") || !strings.Contains(showOut.String(), "input=123") {
		t.Fatalf("job show output missing usage:\n%s", showOut.String())
	}

	snapshot := NewRootCmd()
	snapshotOut, snapshotErr := &bytes.Buffer{}, &bytes.Buffer{}
	snapshot.SetOut(snapshotOut)
	snapshot.SetErr(snapshotErr)
	snapshot.SetArgs([]string{"job", "snapshot", "squ-73", "--repo", tmp})
	if err := snapshot.Execute(); err != nil {
		t.Fatalf("job snapshot: %v\nstderr=%s", err, snapshotErr.String())
	}
	if !strings.Contains(snapshotOut.String(), "usage:") || !strings.Contains(snapshotOut.String(), "input=123") {
		t.Fatalf("job snapshot output missing usage:\n%s", snapshotOut.String())
	}
}

func writeUsageJobForTest(t *testing.T, teamDir, ticket string, rec usage.Record) *job.Job {
	t.Helper()
	j, err := job.New(ticket, "worker", "usage test", rec.StartedAt)
	if err != nil {
		t.Fatalf("job.New %s: %v", ticket, err)
	}
	j.Status = job.StatusDone
	j.Instance = rec.Instance
	j.UpdatedAt = rec.EndedAt
	if j.UpdatedAt.IsZero() {
		j.UpdatedAt = rec.StartedAt
	}
	j.Usage, _ = usage.MergeRecord(nil, rec)
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write %s: %v", ticket, err)
	}
	return j
}
