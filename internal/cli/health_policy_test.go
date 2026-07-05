package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/job"
)

func TestHealthPolicyDefaultsAndConfig(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatalf("mkdir team dir: %v", err)
	}

	defaults, err := loadHealthPolicy(teamDir)
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	if defaults.StatusStaleAfter != defaultStatusStaleAfter || defaults.JobStaleAfter != defaultJobTriageStaleAfter || defaults.TerminalRetention != 0 || defaults.BounceAttentionAfter != defaultBounceAttentionAfter {
		t.Fatalf("defaults = %+v", defaults)
	}

	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(`[health]
status_stale_after = "45m"
job_stale_after = "72h"
terminal_retention = "14d"
bounce_attention_after = 3
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	got, err := loadHealthPolicy(teamDir)
	if err != nil {
		t.Fatalf("load configured policy: %v", err)
	}
	if got.StatusStaleAfter != 45*time.Minute || got.JobStaleAfter != 72*time.Hour || got.TerminalRetention != 14*24*time.Hour || got.BounceAttentionAfter != 3 {
		t.Fatalf("policy = %+v", got)
	}
}

func TestHealthPolicyRejectsInvalidDuration(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatalf("mkdir team dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(`[health]
status_stale_after = "soon"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := loadHealthPolicy(teamDir)
	if err == nil || !strings.Contains(err.Error(), "health.status_stale_after") {
		t.Fatalf("err = %v, want health.status_stale_after duration error", err)
	}
}

func TestHealthPolicyDurationAllowsZero(t *testing.T) {
	got, err := parseHealthPolicyDuration("health.status_stale_after", "0")
	if err != nil {
		t.Fatalf("parse zero: %v", err)
	}
	if got != 0 {
		t.Fatalf("zero duration = %v", got)
	}
}

func TestCollectPsRowsUsesConfiguredStatusStaleAfter(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now()
	old := now.Add(-15 * time.Minute)
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:  "manager",
		Agent:     "manager",
		Status:    daemon.StatusRunning,
		StartedAt: old,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "implementing"
description = "work"
`, old)

	writeHealthPolicyConfig(t, teamDir, "1h", "24h")
	rows, err := collectPsRows(teamDir, now)
	if err != nil {
		t.Fatalf("collect rows: %v", err)
	}
	if len(rows) != 1 || rows[0].Stale {
		t.Fatalf("rows = %+v, want manager not stale under 1h policy", rows)
	}

	writeHealthPolicyConfig(t, teamDir, "5m", "24h")
	rows, err = collectPsRows(teamDir, now)
	if err != nil {
		t.Fatalf("collect rows: %v", err)
	}
	if len(rows) != 1 || !rows[0].Stale {
		t.Fatalf("rows = %+v, want manager stale under 5m policy", rows)
	}
}

func TestJobTriageUsesConfiguredStaleAfterUnlessFlagOverrides(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	old := now.Add(-25 * time.Hour)
	writeHealthPolicyConfig(t, teamDir, "10m", "72h")

	j, err := job.New("SQU-501", "worker", "stale job", old)
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	j.Status = job.StatusRunning
	j.Instance = "worker-squ-501"
	j.UpdatedAt = old
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	configured := runJobTriageJSON(t, tmp)
	if len(configured.Attention) != 0 {
		t.Fatalf("attention with configured 72h policy = %+v, want none", configured.Attention)
	}

	override := runJobTriageJSON(t, tmp, "--stale-after", "1h")
	if len(override.Attention) != 1 || !containsString(override.Attention[0].Reasons, "stale_running") {
		t.Fatalf("attention with CLI override = %+v, want stale_running", override.Attention)
	}
}

func writeHealthPolicyConfig(t *testing.T, teamDir, statusStaleAfter, jobStaleAfter string) {
	t.Helper()
	body := "[health]\n" +
		"status_stale_after = " + strconvQuote(statusStaleAfter) + "\n" +
		"job_stale_after = " + strconvQuote(jobStaleAfter) + "\n"
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write health config: %v", err)
	}
}

func runJobTriageJSON(t *testing.T, repo string, extra ...string) jobTriageSnapshot {
	t.Helper()
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	args := append([]string{"job", "triage", "--repo", repo, "--json"}, extra...)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		var code ExitCode
		if !errors.As(err, &code) {
			t.Fatalf("job triage: %v\nstderr=%s", err, stderr.String())
		}
		t.Fatalf("job triage exit %d\nstderr=%s", code, stderr.String())
	}
	var snapshot jobTriageSnapshot
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode job triage: %v\nbody=%s", err, out.String())
	}
	return snapshot
}

func strconvQuote(value string) string {
	body, _ := json.Marshal(value)
	return string(body)
}
