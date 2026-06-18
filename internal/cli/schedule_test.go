package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScheduleListShowAndDryRun(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	writeScheduleTopology(t, tmp)

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

	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"schedule", "show", "nightly", "--repo", tmp})
	if err := show.Execute(); err != nil {
		t.Fatalf("schedule show: %v\nstderr=%s", err, showErr.String())
	}
	for _, want := range []string{"Schedule:     nightly", "Event:        schedule", "Every:        24h0m0s", "workspace=repo"} {
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
