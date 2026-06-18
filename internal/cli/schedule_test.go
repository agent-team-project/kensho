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

func TestScheduleDueListsRunOnStartAndInterval(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	writeScheduleTopology(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	old := time.Now().UTC().Add(-2 * time.Hour)
	if err := daemon.WriteScheduleState(daemon.DaemonRoot(teamDir), &daemon.ScheduleState{
		Name:       "hourly",
		LastSeenAt: old,
	}); err != nil {
		t.Fatalf("WriteScheduleState hourly: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"schedule", "due", "--repo", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("schedule due json: %v\nstderr=%s", err, stderr.String())
	}
	var rows []scheduleInfo
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode schedule due json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 2 || rows[0].Name != "hourly" || rows[0].DueReason != "interval" || rows[1].Name != "nightly" || rows[1].DueReason != "run_on_start" {
		t.Fatalf("due rows = %+v", rows)
	}

	format := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	format.SetOut(formatOut)
	format.SetErr(formatErr)
	format.SetArgs([]string{"schedule", "due", "--repo", tmp, "--format", "{{.Name}} {{.DueReason}}"})
	if err := format.Execute(); err != nil {
		t.Fatalf("schedule due format: %v\nstderr=%s", err, formatErr.String())
	}
	if got := strings.Split(strings.TrimSpace(formatOut.String()), "\n"); strings.Join(got, ",") != "hourly interval,nightly run_on_start" {
		t.Fatalf("formatted due rows = %q", formatOut.String())
	}
}

func TestScheduleNextOrdersDueUpcomingAndLimit(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	writeScheduleTopology(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	if err := daemon.WriteScheduleState(daemon.DaemonRoot(teamDir), &daemon.ScheduleState{
		Name:       "hourly",
		LastSeenAt: now.Add(-30 * time.Minute),
	}); err != nil {
		t.Fatalf("WriteScheduleState hourly: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"schedule", "next", "--repo", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("schedule next json: %v\nstderr=%s", err, stderr.String())
	}
	var rows []scheduleInfo
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode schedule next json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 2 {
		t.Fatalf("next rows = %+v", rows)
	}
	if rows[0].Name != "nightly" || !rows[0].Due || rows[0].DueReason != "run_on_start" {
		t.Fatalf("first next row = %+v, want due nightly", rows[0])
	}
	if rows[1].Name != "hourly" || rows[1].Due || rows[1].NextRun == nil || !rows[1].NextRun.After(now) {
		t.Fatalf("second next row = %+v, want upcoming hourly", rows[1])
	}

	format := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	format.SetOut(formatOut)
	format.SetErr(formatErr)
	format.SetArgs([]string{"schedule", "next", "--repo", tmp, "--limit", "1", "--format", "{{.Name}} {{.DueReason}}"})
	if err := format.Execute(); err != nil {
		t.Fatalf("schedule next format: %v\nstderr=%s", err, formatErr.String())
	}
	if strings.TrimSpace(formatOut.String()) != "nightly run_on_start" {
		t.Fatalf("formatted limited next rows = %q", formatOut.String())
	}
}

func TestScheduleFireUsesDaemonAndPreservesDryRun(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-schedule-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	writeScheduleTopology(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), fakeSpawnerForTest(t, time.Second))
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"schedule", "fire", "--repo", tmp, "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("schedule fire dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview daemon.ScheduleFireResult
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode schedule fire dry-run json: %v\nbody=%s", err, dryOut.String())
	}
	if !preview.DryRun || preview.WouldFire != 1 || preview.Fired != 0 || len(preview.Schedules) != 1 {
		t.Fatalf("preview = %+v", preview)
	}
	if item := preview.Schedules[0]; item.Name != "nightly" || item.Reason != "run_on_start" || item.Payload["kind"] != "nightly" {
		t.Fatalf("preview item = %+v", item)
	}
	if _, err := daemon.ReadScheduleState(daemon.DaemonRoot(teamDir), "nightly"); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote schedule state, err=%v", err)
	}

	fire := NewRootCmd()
	fireOut, fireErr := &bytes.Buffer{}, &bytes.Buffer{}
	fire.SetOut(fireOut)
	fire.SetErr(fireErr)
	fire.SetArgs([]string{"schedule", "fire", "--repo", tmp, "--json"})
	if err := fire.Execute(); err != nil {
		t.Fatalf("schedule fire: %v\nstderr=%s", err, fireErr.String())
	}
	var result daemon.ScheduleFireResult
	if err := json.Unmarshal(fireOut.Bytes(), &result); err != nil {
		t.Fatalf("decode schedule fire json: %v\nbody=%s", err, fireOut.String())
	}
	if result.DryRun || result.Fired != 1 || len(result.Schedules) != 1 {
		t.Fatalf("result = %+v", result)
	}
	if item := result.Schedules[0]; item.Name != "nightly" || len(item.Outcomes) != 1 || item.Outcomes[0].Action != "messaged" || item.Outcomes[0].Instance != "manager" {
		t.Fatalf("result item = %+v", item)
	}
	if _, err := daemon.ReadScheduleState(daemon.DaemonRoot(teamDir), "nightly"); err != nil {
		t.Fatalf("schedule state not written: %v", err)
	}
	messages, err := daemon.ReadMessages(daemon.DaemonRoot(teamDir), "manager")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0].Body, `"event":"schedule"`) || !strings.Contains(messages[0].Body, `"name":"nightly"`) {
		t.Fatalf("messages = %+v", messages)
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

[[instances.manager.triggers]]
event = "schedule"
match.name = "nightly"

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
