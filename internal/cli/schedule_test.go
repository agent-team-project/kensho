package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
)

func TestScheduleListShowAndDryRun(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	writeScheduleTopology(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	lastSeen := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	lastFired := lastSeen.Add(-24 * time.Hour)
	if err := daemon.WriteScheduleState(daemon.DaemonRoot(teamDir), &daemon.ScheduleState{
		Name:        "nightly",
		LastSeenAt:  lastSeen,
		LastFiredAt: lastFired,
	}); err != nil {
		t.Fatalf("WriteScheduleState: %v", err)
	}

	list := NewRootCmd()
	listOut, listErr := &bytes.Buffer{}, &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetErr(listErr)
	list.SetArgs([]string{"schedule", "ls", "--repo", tmp, "--json"})
	if err := list.Execute(); err != nil {
		t.Fatalf("schedule ls: %v\nstderr=%s", err, listErr.String())
	}
	var schedules []scheduleInfo
	if err := json.Unmarshal(listOut.Bytes(), &schedules); err != nil {
		t.Fatalf("decode schedule ls json: %v\nbody=%s", err, listOut.String())
	}
	if len(schedules) != 2 || schedules[0].Name != "hourly" || schedules[1].Name != "nightly" {
		t.Fatalf("schedules = %+v", schedules)
	}
	if schedules[1].Every != "24h0m0s" || !schedules[1].RunOnStart || schedules[1].Payload["workspace"] != "repo" {
		t.Fatalf("nightly schedule = %+v", schedules[1])
	}
	if schedules[1].LastSeenAt == nil || !schedules[1].LastSeenAt.Equal(lastSeen) {
		t.Fatalf("nightly last_seen = %v, want %s", schedules[1].LastSeenAt, lastSeen)
	}
	if schedules[1].LastFiredAt == nil || !schedules[1].LastFiredAt.Equal(lastFired) {
		t.Fatalf("nightly last_fired = %v, want %s", schedules[1].LastFiredAt, lastFired)
	}
	if schedules[1].NextRun == nil || !schedules[1].NextRun.Equal(lastSeen.Add(24*time.Hour)) {
		t.Fatalf("nightly next_run = %v", schedules[1].NextRun)
	}

	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"schedule", "show", "nightly", "--repo", tmp})
	if err := show.Execute(); err != nil {
		t.Fatalf("schedule show: %v\nstderr=%s", err, showErr.String())
	}
	for _, want := range []string{"Schedule:     nightly", "Event:        schedule", "Every:        24h0m0s", "Last Seen:    2026-06-18T12:00:00Z", "Last Fired:   2026-06-17T12:00:00Z", "Next Run:     2026-06-19T12:00:00Z", "workspace=repo"} {
		if !strings.Contains(showOut.String(), want) {
			t.Fatalf("schedule show missing %q:\n%s", want, showOut.String())
		}
	}

	run := NewRootCmd()
	runOut, runErr := &bytes.Buffer{}, &bytes.Buffer{}
	run.SetOut(runOut)
	run.SetErr(runErr)
	run.SetArgs([]string{"schedule", "run", "nightly", "--repo", tmp, "--dry-run", "--json"})
	if err := run.Execute(); err != nil {
		t.Fatalf("schedule run dry-run: %v\nstderr=%s", err, runErr.String())
	}
	var result intakePublishResult
	if err := json.Unmarshal(runOut.Bytes(), &result); err != nil {
		t.Fatalf("decode schedule dry-run json: %v\nbody=%s", err, runOut.String())
	}
	if !result.DryRun || result.Event == nil || result.Event.Type != "schedule" {
		t.Fatalf("dry-run result = %+v", result)
	}
	if result.Event.Payload["name"] != "nightly" || result.Event.Payload["source"] != "schedule" || result.Event.Payload["workspace"] != "repo" {
		t.Fatalf("dry-run payload = %+v", result.Event.Payload)
	}

	override := NewRootCmd()
	overrideOut, overrideErr := &bytes.Buffer{}, &bytes.Buffer{}
	override.SetOut(overrideOut)
	override.SetErr(overrideErr)
	override.SetArgs([]string{
		"schedule", "run", "nightly",
		"--repo", tmp,
		"--payload", `{"workspace":"scratch","extra":true,"name":"ignored"}`,
		"--dry-run",
		"--json",
	})
	if err := override.Execute(); err != nil {
		t.Fatalf("schedule run override dry-run: %v\nstderr=%s", err, overrideErr.String())
	}
	var overridden intakePublishResult
	if err := json.Unmarshal(overrideOut.Bytes(), &overridden); err != nil {
		t.Fatalf("decode schedule override json: %v\nbody=%s", err, overrideOut.String())
	}
	if overridden.Event.Payload["workspace"] != "scratch" || overridden.Event.Payload["extra"] != true {
		t.Fatalf("override payload = %+v", overridden.Event.Payload)
	}
	if overridden.Event.Payload["name"] != "nightly" || overridden.Event.Payload["source"] != "schedule" {
		t.Fatalf("identity fields should be preserved: %+v", overridden.Event.Payload)
	}
}

func TestScheduleShowMissing(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	writeScheduleTopology(t, tmp)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"schedule", "show", "missing", "--repo", tmp})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("schedule show missing succeeded: stdout=%s", out.String())
	}
	if !strings.Contains(stderr.String(), `schedule "missing" not found`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestScheduleRunRejectsInvalidPayload(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	writeScheduleTopology(t, tmp)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"schedule", "run", "nightly", "--repo", tmp, "--payload", "{", "--dry-run"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("schedule run invalid payload succeeded: stdout=%s", out.String())
	}
	if !strings.Contains(stderr.String(), "--payload is not valid JSON") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func writeScheduleTopology(t *testing.T, repo string) {
	t.Helper()
	body := `
[instances.manager]
agent = "manager"

[schedules.nightly]
every = "24h"
run_on_start = true
payload.workspace = "repo"
payload.kind = "nightly"

[schedules.hourly]
every = "1h"
payload.workspace = "repo"
payload.kind = "hourly"
`
	if err := os.WriteFile(filepath.Join(repo, ".agent_team", "instances.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write instances.toml: %v", err)
	}
}
