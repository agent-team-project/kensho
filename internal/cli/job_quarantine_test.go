package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jamesaud/agent-team/internal/job"
)

func writeQuarantinedJobFile(t *testing.T, teamDir, stamp, name string, body []byte) string {
	t.Helper()
	dir := filepath.Join(job.Directory(teamDir), "quarantine", stamp)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join("quarantine", stamp, name)
}

func TestJobQuarantineListShowRestoreDrop(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	restorableRel := writeQuarantinedJobFile(t, teamDir, "20260627T120000.000000000Z", "squ-402-restored.toml", []byte(`id = "squ-402"
ticket = "SQU-402"
target = "worker"
kickoff = "restore this job"
status = "queued"
created_at = 2026-06-27T12:00:00Z
updated_at = 2026-06-27T12:00:00Z
`))
	brokenRel := writeQuarantinedJobFile(t, teamDir, "20260627T120000.000000000Z", "broken.toml", []byte("id = [\n"))
	restorablePath := filepath.Join(job.Directory(teamDir), restorableRel)
	brokenPath := filepath.Join(job.Directory(teamDir), brokenRel)

	list := NewRootCmd()
	listOut, listErr := &bytes.Buffer{}, &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetErr(listErr)
	list.SetArgs([]string{"job", "quarantine", "--repo", tmp, "--sort", "restorable", "--json"})
	if err := list.Execute(); err != nil {
		t.Fatalf("job quarantine list: %v\nstderr=%s", err, listErr.String())
	}
	var listed []jobQuarantineItem
	if err := json.Unmarshal(listOut.Bytes(), &listed); err != nil {
		t.Fatalf("decode job quarantine list: %v\nbody=%s", err, listOut.String())
	}
	if len(listed) != 2 || listed[0].ID != "squ-402" || !listed[0].Restorable || listed[1].Restorable {
		t.Fatalf("listed job quarantine items = %+v", listed)
	}

	filtered := NewRootCmd()
	filteredOut, filteredErr := &bytes.Buffer{}, &bytes.Buffer{}
	filtered.SetOut(filteredOut)
	filtered.SetErr(filteredErr)
	filtered.SetArgs([]string{"job", "quarantine", "--repo", tmp, "--unrestorable", "--format", "{{.Path}} {{.Restorable}}"})
	if err := filtered.Execute(); err != nil {
		t.Fatalf("job quarantine unrestorable format: %v\nstderr=%s", err, filteredErr.String())
	}
	if got, want := filteredOut.String(), brokenRel+" false\n"; got != want {
		t.Fatalf("job quarantine unrestorable format = %q, want %q", got, want)
	}

	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"job", "quarantine", "show", brokenRel, "--repo", tmp})
	if err := show.Execute(); err != nil {
		t.Fatalf("job quarantine show: %v\nstderr=%s", err, showErr.String())
	}
	for _, want := range []string{"broken", "Restorable: no", "Problem:", "agent-team job quarantine drop"} {
		if !strings.Contains(showOut.String(), want) {
			t.Fatalf("job quarantine show missing %q:\n%s", want, showOut.String())
		}
	}
	if strings.Contains(showOut.String(), "agent-team job quarantine restore") {
		t.Fatalf("unrestorable job quarantine show included restore action:\n%s", showOut.String())
	}

	restoreDry := NewRootCmd()
	restoreDryOut, restoreDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	restoreDry.SetOut(restoreDryOut)
	restoreDry.SetErr(restoreDryErr)
	restoreDry.SetArgs([]string{"job", "quarantine", "restore", restorableRel, "--repo", tmp, "--dry-run", "--format", "{{.ID}} {{.Action}} {{.Destination}} {{.DryRun}}"})
	if err := restoreDry.Execute(); err != nil {
		t.Fatalf("job quarantine restore dry-run: %v\nstderr=%s", err, restoreDryErr.String())
	}
	if got, want := restoreDryOut.String(), "squ-402 would_restore squ-402.toml true\n"; got != want {
		t.Fatalf("job quarantine restore dry-run = %q, want %q", got, want)
	}
	if _, err := os.Stat(restorablePath); err != nil {
		t.Fatalf("dry-run should leave quarantined file in place: %v", err)
	}

	restore := NewRootCmd()
	restoreOut, restoreErr := &bytes.Buffer{}, &bytes.Buffer{}
	restore.SetOut(restoreOut)
	restore.SetErr(restoreErr)
	restore.SetArgs([]string{"job", "quarantine", "restore", restorableRel, "--repo", tmp, "--json"})
	if err := restore.Execute(); err != nil {
		t.Fatalf("job quarantine restore: %v\nstderr=%s", err, restoreErr.String())
	}
	var restored jobQuarantineRestoreResult
	if err := json.Unmarshal(restoreOut.Bytes(), &restored); err != nil {
		t.Fatalf("decode job quarantine restore: %v\nbody=%s", err, restoreOut.String())
	}
	if restored.ID != "squ-402" || restored.Action != "restored" || restored.Destination != "squ-402.toml" {
		t.Fatalf("restore result = %+v", restored)
	}
	if _, err := os.Stat(restorablePath); !os.IsNotExist(err) {
		t.Fatalf("quarantined restorable file still exists: %v", err)
	}
	if restoredJob, err := job.Read(teamDir, "SQU-402"); err != nil || restoredJob.ID != "squ-402" {
		t.Fatalf("restored active job = %+v err=%v", restoredJob, err)
	}

	dropDry := NewRootCmd()
	dropDryOut, dropDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropDry.SetOut(dropDryOut)
	dropDry.SetErr(dropDryErr)
	dropDry.SetArgs([]string{"job", "quarantine", "drop", brokenRel, "--repo", tmp, "--dry-run", "--format", "{{.Path}} {{.Action}} {{.DryRun}}"})
	if err := dropDry.Execute(); err != nil {
		t.Fatalf("job quarantine drop dry-run: %v\nstderr=%s", err, dropDryErr.String())
	}
	if got, want := dropDryOut.String(), brokenRel+" would_drop true\n"; got != want {
		t.Fatalf("job quarantine drop dry-run = %q, want %q", got, want)
	}
	if _, err := os.Stat(brokenPath); err != nil {
		t.Fatalf("dry-run should leave broken file in place: %v", err)
	}

	drop := NewRootCmd()
	dropOut, dropErr := &bytes.Buffer{}, &bytes.Buffer{}
	drop.SetOut(dropOut)
	drop.SetErr(dropErr)
	drop.SetArgs([]string{"job", "quarantine", "drop", brokenRel, "--repo", tmp, "--json"})
	if err := drop.Execute(); err != nil {
		t.Fatalf("job quarantine drop: %v\nstderr=%s", err, dropErr.String())
	}
	var dropped jobQuarantineDropResult
	if err := json.Unmarshal(dropOut.Bytes(), &dropped); err != nil {
		t.Fatalf("decode job quarantine drop: %v\nbody=%s", err, dropOut.String())
	}
	if dropped.Path != brokenRel || dropped.Action != "dropped" || !dropped.Dropped {
		t.Fatalf("drop result = %+v", dropped)
	}
	if _, err := os.Stat(brokenPath); !os.IsNotExist(err) {
		t.Fatalf("broken quarantined file still exists: %v", err)
	}

	doctor := NewRootCmd()
	doctorOut, doctorErr := &bytes.Buffer{}, &bytes.Buffer{}
	doctor.SetOut(doctorOut)
	doctor.SetErr(doctorErr)
	doctor.SetArgs([]string{"job", "doctor", "--repo", tmp, "--format", "{{.OK}} {{.Summary.Valid}} {{.Summary.Invalid}}"})
	if err := doctor.Execute(); err != nil {
		t.Fatalf("job doctor after quarantine restore/drop: %v\nstderr=%s", err, doctorErr.String())
	}
	if got, want := doctorOut.String(), "true 1 0\n"; got != want {
		t.Fatalf("job doctor after quarantine restore/drop = %q, want %q", got, want)
	}
}
