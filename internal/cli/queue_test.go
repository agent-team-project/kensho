package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
)

func TestQueueCommandListShowDropLocal(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	item := &daemon.QueueItem{
		ID:         "q-local",
		State:      daemon.QueueStateDead,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-90",
		Payload:    map[string]any{"target": "worker", "ticket": "SQU-90"},
		Attempts:   daemon.MaxQueueAttempts,
		LastError:  "spawn failed",
		QueuedAt:   now,
		UpdatedAt:  now,
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
		t.Fatalf("WriteQueueItem: %v", err)
	}

	ls := NewRootCmd()
	lsOut, lsErr := &bytes.Buffer{}, &bytes.Buffer{}
	ls.SetOut(lsOut)
	ls.SetErr(lsErr)
	ls.SetArgs([]string{"queue", "ls", "--target", tmp})
	if err := ls.Execute(); err != nil {
		t.Fatalf("queue ls: %v\nstderr=%s", err, lsErr.String())
	}
	for _, want := range []string{"q-local", "dead", "worker-squ-90", "agent-team queue retry q-local", "agent-team queue drop q-local"} {
		if !strings.Contains(lsOut.String(), want) {
			t.Fatalf("queue ls missing %q:\n%s", want, lsOut.String())
		}
	}

	showText := NewRootCmd()
	showTextOut, showTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	showText.SetOut(showTextOut)
	showText.SetErr(showTextErr)
	showText.SetArgs([]string{"queue", "show", "q-local", "--target", tmp})
	if err := showText.Execute(); err != nil {
		t.Fatalf("queue show text: %v\nstderr=%s", err, showTextErr.String())
	}
	for _, want := range []string{"Actions:", "agent-team queue retry q-local", "agent-team queue drop q-local"} {
		if !strings.Contains(showTextOut.String(), want) {
			t.Fatalf("queue show text missing %q:\n%s", want, showTextOut.String())
		}
	}

	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"queue", "show", "q-local", "--target", tmp, "--json"})
	if err := show.Execute(); err != nil {
		t.Fatalf("queue show: %v\nstderr=%s", err, showErr.String())
	}
	var shown daemon.QueueItem
	if err := json.Unmarshal(showOut.Bytes(), &shown); err != nil {
		t.Fatalf("decode show: %v\nbody=%s", err, showOut.String())
	}
	if shown.ID != "q-local" || shown.Payload["ticket"] != "SQU-90" {
		t.Fatalf("shown = %+v", shown)
	}

	dryDrop := NewRootCmd()
	dryDropOut, dryDropErr := &bytes.Buffer{}, &bytes.Buffer{}
	dryDrop.SetOut(dryDropOut)
	dryDrop.SetErr(dryDropErr)
	dryDrop.SetArgs([]string{"queue", "drop", "q-local", "--target", tmp, "--dry-run", "--json"})
	if err := dryDrop.Execute(); err != nil {
		t.Fatalf("queue drop dry-run: %v\nstderr=%s", err, dryDropErr.String())
	}
	var dryDropResults []queueDropResult
	if err := json.Unmarshal(dryDropOut.Bytes(), &dryDropResults); err != nil {
		t.Fatalf("decode drop dry-run: %v\nbody=%s", err, dryDropOut.String())
	}
	if len(dryDropResults) != 1 || dryDropResults[0].ID != "q-local" || dryDropResults[0].Action != "would_drop" || !dryDropResults[0].DryRun {
		t.Fatalf("dry drop results = %+v", dryDropResults)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-local"); err != nil {
		t.Fatalf("drop dry-run removed queue item: %v", err)
	}

	drop := NewRootCmd()
	dropOut, dropErr := &bytes.Buffer{}, &bytes.Buffer{}
	drop.SetOut(dropOut)
	drop.SetErr(dropErr)
	drop.SetArgs([]string{"queue", "drop", "q-local", "--target", tmp, "--json"})
	if err := drop.Execute(); err != nil {
		t.Fatalf("queue drop: %v\nstderr=%s", err, dropErr.String())
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-local"); !os.IsNotExist(err) {
		t.Fatalf("queue item still exists or unexpected err=%v", err)
	}
}

func TestQueueDoctorReportsPersistedQueueProblems(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	queueRoot := daemon.QueueRoot(daemon.DaemonRoot(teamDir))
	now := time.Now().UTC().Truncate(time.Second)
	valid := &daemon.QueueItem{
		ID:         "q-valid",
		State:      daemon.QueueStatePending,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-120",
		Payload:    map[string]any{"target": "worker", "ticket": "SQU-120"},
		QueuedAt:   now,
		UpdatedAt:  now,
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), valid); err != nil {
		t.Fatalf("WriteQueueItem valid: %v", err)
	}
	duplicate := *valid
	duplicate.State = daemon.QueueStateDead
	duplicate.DeadLetteredAt = now
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), &duplicate); err != nil {
		t.Fatalf("WriteQueueItem duplicate: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(queueRoot, daemon.QueueStatePending), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(queueRoot, daemon.QueueStateDead), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(queueRoot, daemon.QueueStatePending, "bad-json.json"), []byte("{\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(queueRoot, daemon.QueueStatePending, "missing-id.json"), []byte(fmt.Sprintf(`{
  "state": "pending",
  "event_type": "agent.dispatch",
  "instance": "worker",
  "instance_id": "worker-squ-121",
  "payload": {},
  "queued_at": %q,
  "updated_at": %q
}`, now.Format(time.RFC3339), now.Format(time.RFC3339))), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(queueRoot, daemon.QueueStateDead, "mismatch.json"), []byte(fmt.Sprintf(`{
  "id": "stored-id",
  "state": "pending",
  "event_type": "",
  "instance": "worker",
  "instance_id": "worker-squ-122",
  "payload": {},
  "queued_at": %q,
  "updated_at": %q
}`, now.Format(time.RFC3339), now.Format(time.RFC3339))), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(queueRoot, daemon.QueueStatePending, "notes.txt"), []byte("not a queue item\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"queue", "doctor", "--target", tmp, "--json"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("queue doctor succeeded unexpectedly")
	}
	if stderr.Len() != 0 {
		t.Fatalf("queue doctor json wrote stderr: %s", stderr.String())
	}
	var result queueDoctorResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode queue doctor json: %v\nbody=%s", err, out.String())
	}
	if result.OK || result.Summary.Files != 5 || result.Summary.Items != 4 || result.Summary.Valid != 2 || result.Summary.Invalid != 3 || result.Summary.Ignored != 1 || result.Summary.Duplicates != 1 {
		t.Fatalf("queue doctor result = %+v", result)
	}
	codes := map[string]bool{}
	for _, problem := range result.Problems {
		codes[problem.Code] = true
	}
	for _, want := range []string{"invalid_json", "duplicate_id", "id_path_mismatch", "state_path_mismatch", "missing_event_type"} {
		if !codes[want] {
			t.Fatalf("queue doctor problems missing %q: %+v", want, result.Problems)
		}
	}
	warningCodes := map[string]bool{}
	for _, warning := range result.Warnings {
		warningCodes[warning.Code] = true
	}
	for _, want := range []string{"missing_id", "unexpected_file"} {
		if !warningCodes[want] {
			t.Fatalf("queue doctor warnings missing %q: %+v", want, result.Warnings)
		}
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"queue", "doctor", "--target", tmp})
	if err := text.Execute(); err == nil {
		t.Fatal("queue doctor text succeeded unexpectedly")
	}
	for _, want := range []string{"agent-team queue doctor: problems found", "bad-json.json is not valid JSON", "duplicates queue id"} {
		if !strings.Contains(textErr.String(), want) {
			t.Fatalf("queue doctor text missing %q:\nstdout=%s\nstderr=%s", want, textOut.String(), textErr.String())
		}
	}
}

func TestQueueDoctorQuarantineDryRunAndApply(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	queueRoot := daemon.QueueRoot(daemon.DaemonRoot(teamDir))
	pendingDir := filepath.Join(queueRoot, daemon.QueueStatePending)
	now := time.Now().UTC().Truncate(time.Second)
	if err := os.MkdirAll(pendingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	good := &daemon.QueueItem{
		ID:         "q-good",
		State:      daemon.QueueStatePending,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-130",
		Payload:    map[string]any{"target": "worker", "ticket": "SQU-130"},
		QueuedAt:   now,
		UpdatedAt:  now,
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), good); err != nil {
		t.Fatalf("WriteQueueItem good: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pendingDir, "bad-json.json"), []byte("{\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pendingDir, "missing-event.json"), []byte(fmt.Sprintf(`{
  "id": "missing-event",
  "state": "pending",
  "instance": "worker",
  "instance_id": "worker-squ-131",
  "payload": {},
  "queued_at": %q,
  "updated_at": %q
}`, now.Format(time.RFC3339), now.Format(time.RFC3339))), 0o644); err != nil {
		t.Fatal(err)
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"queue", "doctor", "--target", tmp, "--quarantine", "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("queue doctor quarantine dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryResult queueDoctorResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryResult); err != nil {
		t.Fatalf("decode quarantine dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if dryResult.OK || dryResult.Quarantine == nil || !dryResult.Quarantine.DryRun || dryResult.Quarantine.Candidates != 2 || dryResult.Quarantine.Moved != 0 {
		t.Fatalf("dry quarantine result = %+v", dryResult)
	}
	for _, path := range []string{
		filepath.Join(pendingDir, "bad-json.json"),
		filepath.Join(pendingDir, "missing-event.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("dry-run moved %s: %v", path, err)
		}
	}

	apply := NewRootCmd()
	applyOut, applyErr := &bytes.Buffer{}, &bytes.Buffer{}
	apply.SetOut(applyOut)
	apply.SetErr(applyErr)
	apply.SetArgs([]string{"queue", "doctor", "--target", tmp, "--quarantine", "--json"})
	if err := apply.Execute(); err != nil {
		t.Fatalf("queue doctor quarantine apply: %v\nstderr=%s\nstdout=%s", err, applyErr.String(), applyOut.String())
	}
	var applied queueDoctorResult
	if err := json.Unmarshal(applyOut.Bytes(), &applied); err != nil {
		t.Fatalf("decode quarantine apply: %v\nbody=%s", err, applyOut.String())
	}
	if !applied.OK || applied.Quarantine == nil || applied.Quarantine.DryRun || applied.Quarantine.Candidates != 2 || applied.Quarantine.Moved != 2 {
		t.Fatalf("applied quarantine result = %+v", applied)
	}
	for _, item := range applied.Quarantine.Items {
		if item.Action != "quarantined" {
			t.Fatalf("quarantine item action = %+v", item)
		}
		if _, err := os.Stat(filepath.Join(queueRoot, item.Destination)); err != nil {
			t.Fatalf("quarantined destination %s: %v", item.Destination, err)
		}
	}
	for _, path := range []string{
		filepath.Join(pendingDir, "bad-json.json"),
		filepath.Join(pendingDir, "missing-event.json"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("active problem file still exists or stat failed %s: %v", path, err)
		}
	}

	ls := NewRootCmd()
	lsOut, lsErr := &bytes.Buffer{}, &bytes.Buffer{}
	ls.SetOut(lsOut)
	ls.SetErr(lsErr)
	ls.SetArgs([]string{"queue", "ls", "--target", tmp, "--json"})
	if err := ls.Execute(); err != nil {
		t.Fatalf("queue ls after quarantine: %v\nstderr=%s", err, lsErr.String())
	}
	var items []daemon.QueueItem
	if err := json.Unmarshal(lsOut.Bytes(), &items); err != nil {
		t.Fatalf("decode queue ls after quarantine: %v\nbody=%s", err, lsOut.String())
	}
	if len(items) != 1 || items[0].ID != "q-good" {
		t.Fatalf("queue items after quarantine = %+v", items)
	}
}

func TestQueueQuarantineListAndRestore(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	queueRoot := daemon.QueueRoot(daemon.DaemonRoot(teamDir))
	pendingDir := filepath.Join(queueRoot, daemon.QueueStatePending)
	now := time.Now().UTC().Truncate(time.Second)
	if err := os.MkdirAll(pendingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pendingDir, "mismatch.json"), []byte(fmt.Sprintf(`{
  "id": "stored-id",
  "state": "pending",
  "event_type": "agent.dispatch",
  "instance": "worker",
  "instance_id": "worker-squ-132",
  "payload": {"ticket": "SQU-132"},
  "queued_at": %q,
  "updated_at": %q
}`, now.Format(time.RFC3339), now.Format(time.RFC3339))), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pendingDir, "bad-json.json"), []byte("{\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	doctor := NewRootCmd()
	doctorOut, doctorErr := &bytes.Buffer{}, &bytes.Buffer{}
	doctor.SetOut(doctorOut)
	doctor.SetErr(doctorErr)
	doctor.SetArgs([]string{"queue", "doctor", "--target", tmp, "--quarantine", "--json"})
	if err := doctor.Execute(); err != nil {
		t.Fatalf("queue doctor quarantine: %v\nstderr=%s\nstdout=%s", err, doctorErr.String(), doctorOut.String())
	}

	ls := NewRootCmd()
	lsOut, lsErr := &bytes.Buffer{}, &bytes.Buffer{}
	ls.SetOut(lsOut)
	ls.SetErr(lsErr)
	ls.SetArgs([]string{"queue", "quarantine", "ls", "--target", tmp, "--json"})
	if err := ls.Execute(); err != nil {
		t.Fatalf("queue quarantine ls: %v\nstderr=%s", err, lsErr.String())
	}
	var quarantined []queueQuarantineItem
	if err := json.Unmarshal(lsOut.Bytes(), &quarantined); err != nil {
		t.Fatalf("decode quarantine ls: %v\nbody=%s", err, lsOut.String())
	}
	if len(quarantined) != 2 {
		t.Fatalf("quarantined items = %+v", quarantined)
	}
	var restorable, invalid queueQuarantineItem
	for _, item := range quarantined {
		switch {
		case item.ID == "stored-id":
			restorable = item
		case strings.Contains(item.Path, "bad-json.json"):
			invalid = item
		}
	}
	if !restorable.Restorable || restorable.RestorePath != filepath.Join(daemon.QueueStatePending, "mismatch.json") || restorable.Job != "squ-132" {
		t.Fatalf("restorable item = %+v", restorable)
	}
	if invalid.Restorable || !strings.Contains(invalid.Problem, "invalid JSON") {
		t.Fatalf("invalid item = %+v", invalid)
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"queue", "quarantine", "restore", "--target", tmp, "--dry-run", "--json", restorable.Path})
	if err := dry.Execute(); err != nil {
		t.Fatalf("queue quarantine restore dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryResult queueQuarantineRestoreResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryResult); err != nil {
		t.Fatalf("decode restore dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if dryResult.Action != "would_restore" || !dryResult.DryRun || dryResult.Destination != restorable.RestorePath {
		t.Fatalf("restore dry-run result = %+v", dryResult)
	}
	if _, err := os.Stat(filepath.Join(pendingDir, "mismatch.json")); !os.IsNotExist(err) {
		t.Fatalf("dry-run restored active file unexpectedly: %v", err)
	}

	restore := NewRootCmd()
	restoreOut, restoreErr := &bytes.Buffer{}, &bytes.Buffer{}
	restore.SetOut(restoreOut)
	restore.SetErr(restoreErr)
	restore.SetArgs([]string{"queue", "quarantine", "restore", "--target", tmp, "--json", restorable.Path})
	if err := restore.Execute(); err != nil {
		t.Fatalf("queue quarantine restore: %v\nstderr=%s", err, restoreErr.String())
	}
	var restored queueQuarantineRestoreResult
	if err := json.Unmarshal(restoreOut.Bytes(), &restored); err != nil {
		t.Fatalf("decode restore: %v\nbody=%s", err, restoreOut.String())
	}
	if restored.Action != "restored" || restored.DryRun || restored.Destination != restorable.RestorePath {
		t.Fatalf("restore result = %+v", restored)
	}
	if _, err := os.Stat(filepath.Join(pendingDir, "mismatch.json")); err != nil {
		t.Fatalf("active restored file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(queueRoot, restorable.Path)); !os.IsNotExist(err) {
		t.Fatalf("quarantine source still exists: %v", err)
	}

	restoreBad := NewRootCmd()
	restoreBadOut, restoreBadErr := &bytes.Buffer{}, &bytes.Buffer{}
	restoreBad.SetOut(restoreBadOut)
	restoreBad.SetErr(restoreBadErr)
	restoreBad.SetArgs([]string{"queue", "quarantine", "restore", "--target", tmp, "--json", invalid.Path})
	if err := restoreBad.Execute(); err == nil {
		t.Fatalf("restored invalid quarantine unexpectedly: stdout=%s", restoreBadOut.String())
	}
	if _, err := os.Stat(filepath.Join(queueRoot, invalid.Path)); err != nil {
		t.Fatalf("invalid quarantine source moved: %v", err)
	}
}

func TestQueueQuarantineDropExplicitAndBatch(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	queueRoot := daemon.QueueRoot(daemon.DaemonRoot(teamDir))
	now := time.Now().UTC().Truncate(time.Second)
	writeQuarantinedQueueItem(t, teamDir, "20260619T000000.000000000Z", daemon.QueueStatePending, &daemon.QueueItem{
		ID:         "q-restorable",
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-133",
		Payload:    map[string]any{"ticket": "SQU-133", "target": "worker"},
		QueuedAt:   now,
		UpdatedAt:  now,
	})
	invalidDir := filepath.Join(queueRoot, "quarantine", "20260619T000000.000000000Z", daemon.QueueStatePending)
	if err := os.WriteFile(filepath.Join(invalidDir, "bad-one.json"), []byte("{\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(invalidDir, "bad-two.json"), []byte("{\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	items, err := listQueueQuarantine(teamDir)
	if err != nil {
		t.Fatalf("list quarantine: %v", err)
	}
	var explicitPath string
	for _, item := range items {
		if strings.Contains(item.Path, "bad-one.json") {
			explicitPath = item.Path
			break
		}
	}
	if explicitPath == "" {
		t.Fatalf("missing explicit bad item: %+v", items)
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"queue", "quarantine", "drop", "--target", tmp, "--dry-run", "--json", explicitPath})
	if err := dry.Execute(); err != nil {
		t.Fatalf("queue quarantine drop dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryResults []queueQuarantineDropResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryResults); err != nil {
		t.Fatalf("decode explicit drop dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(dryResults) != 1 || dryResults[0].Action != "would_drop" || !dryResults[0].DryRun {
		t.Fatalf("explicit dry-run results = %+v", dryResults)
	}
	if _, err := os.Stat(filepath.Join(queueRoot, explicitPath)); err != nil {
		t.Fatalf("dry-run removed explicit quarantine: %v", err)
	}

	drop := NewRootCmd()
	dropOut, dropErr := &bytes.Buffer{}, &bytes.Buffer{}
	drop.SetOut(dropOut)
	drop.SetErr(dropErr)
	drop.SetArgs([]string{"queue", "quarantine", "drop", "--target", tmp, "--json", explicitPath})
	if err := drop.Execute(); err != nil {
		t.Fatalf("queue quarantine drop explicit: %v\nstderr=%s", err, dropErr.String())
	}
	var dropped []queueQuarantineDropResult
	if err := json.Unmarshal(dropOut.Bytes(), &dropped); err != nil {
		t.Fatalf("decode explicit drop: %v\nbody=%s", err, dropOut.String())
	}
	if len(dropped) != 1 || dropped[0].Action != "dropped" || !dropped[0].Dropped {
		t.Fatalf("explicit drop results = %+v", dropped)
	}
	if _, err := os.Stat(filepath.Join(queueRoot, explicitPath)); !os.IsNotExist(err) {
		t.Fatalf("explicit quarantine still exists or stat failed: %v", err)
	}

	batchDry := NewRootCmd()
	batchDryOut, batchDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	batchDry.SetOut(batchDryOut)
	batchDry.SetErr(batchDryErr)
	batchDry.SetArgs([]string{"queue", "quarantine", "drop", "--target", tmp, "--all", "--unrestorable", "--dry-run", "--json"})
	if err := batchDry.Execute(); err != nil {
		t.Fatalf("queue quarantine drop batch dry-run: %v\nstderr=%s", err, batchDryErr.String())
	}
	var batchDryResults []queueQuarantineDropResult
	if err := json.Unmarshal(batchDryOut.Bytes(), &batchDryResults); err != nil {
		t.Fatalf("decode batch drop dry-run: %v\nbody=%s", err, batchDryOut.String())
	}
	if len(batchDryResults) != 1 || !strings.Contains(batchDryResults[0].Path, "bad-two.json") || batchDryResults[0].Restorable {
		t.Fatalf("batch dry-run results = %+v", batchDryResults)
	}

	batch := NewRootCmd()
	batchOut, batchErr := &bytes.Buffer{}, &bytes.Buffer{}
	batch.SetOut(batchOut)
	batch.SetErr(batchErr)
	batch.SetArgs([]string{"queue", "quarantine", "drop", "--target", tmp, "--all", "--unrestorable", "--json"})
	if err := batch.Execute(); err != nil {
		t.Fatalf("queue quarantine drop batch: %v\nstderr=%s", err, batchErr.String())
	}
	var batchResults []queueQuarantineDropResult
	if err := json.Unmarshal(batchOut.Bytes(), &batchResults); err != nil {
		t.Fatalf("decode batch drop: %v\nbody=%s", err, batchOut.String())
	}
	if len(batchResults) != 1 || !batchResults[0].Dropped || batchResults[0].Restorable {
		t.Fatalf("batch drop results = %+v", batchResults)
	}
	remaining, err := listQueueQuarantine(teamDir)
	if err != nil {
		t.Fatalf("list remaining quarantine: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != "q-restorable" || !remaining[0].Restorable {
		t.Fatalf("remaining quarantine = %+v", remaining)
	}
}

func TestQueueDoctorOKWithWarnings(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	queueRoot := daemon.QueueRoot(daemon.DaemonRoot(teamDir))
	now := time.Now().UTC().Truncate(time.Second)
	if err := os.MkdirAll(filepath.Join(queueRoot, daemon.QueueStatePending), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(queueRoot, daemon.QueueStatePending, "missing-id.json"), []byte(fmt.Sprintf(`{
  "state": "pending",
  "event_type": "agent.dispatch",
  "instance": "worker",
  "instance_id": "worker-squ-123",
  "payload": {},
  "queued_at": %q,
  "updated_at": %q
}`, now.Format(time.RFC3339), now.Format(time.RFC3339))), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"queue", "doctor", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("queue doctor warning-only failed: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(out.String(), "agent-team queue doctor: OK") || !strings.Contains(out.String(), "valid=1") {
		t.Fatalf("queue doctor stdout = %q", out.String())
	}
	if !strings.Contains(stderr.String(), "warning:") || !strings.Contains(stderr.String(), "no id field") {
		t.Fatalf("queue doctor stderr = %q", stderr.String())
	}
}

func TestQueueListWatchRendersSnapshot(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	item := &daemon.QueueItem{
		ID:         "q-watch",
		State:      daemon.QueueStatePending,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-92",
		Payload:    map[string]any{"target": "worker", "ticket": "SQU-92"},
		QueuedAt:   now,
		UpdatedAt:  now,
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
		t.Fatalf("WriteQueueItem: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if err := runQueueListWatch(ctx, &out, teamDir, queueListFilters{state: daemon.QueueStatePending}, false, nil, time.Millisecond, false); err != nil {
		t.Fatalf("runQueueListWatch: %v", err)
	}
	if !strings.Contains(out.String(), "q-watch") || strings.Contains(out.String(), watchClearSequence) {
		t.Fatalf("watch output = %q", out.String())
	}

	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	out.Reset()
	if err := runQueueSummaryWatch(ctx, &out, teamDir, queueListFilters{}, false, time.Millisecond, false); err != nil {
		t.Fatalf("runQueueSummaryWatch: %v", err)
	}
	if !strings.Contains(out.String(), "queue: total=1 pending=1 dead=0") || strings.Contains(out.String(), watchClearSequence) {
		t.Fatalf("summary watch output = %q", out.String())
	}
}

func TestQueueListFilters(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	items := []*daemon.QueueItem{
		{
			ID:         "q-ready",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-96",
			Payload:    map[string]any{"target": "worker", "ticket": "SQU-96"},
			Attempts:   1,
			NextRetry:  now.Add(-time.Minute),
			QueuedAt:   now.Add(-2 * time.Hour),
			UpdatedAt:  now.Add(-time.Minute),
		},
		{
			ID:         "q-delayed",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-97",
			Payload:    map[string]any{"target": "worker", "ticket": "SQU-97"},
			Attempts:   2,
			NextRetry:  now.Add(time.Hour),
			QueuedAt:   now.Add(-time.Hour),
			UpdatedAt:  now,
		},
		{
			ID:         "q-manager",
			State:      daemon.QueueStatePending,
			EventType:  "ticket.created",
			Instance:   "manager",
			InstanceID: "manager-squ-98",
			Payload:    map[string]any{"target": "manager", "ticket": "SQU-98"},
			QueuedAt:   now.Add(-30 * time.Minute),
			UpdatedAt:  now,
		},
		{
			ID:             "q-dead-worker",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     "worker-squ-99",
			Payload:        map[string]any{"target": "worker", "ticket": "SQU-99"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-2 * time.Hour),
			DeadLetteredAt: now.Add(-2 * time.Hour),
		},
	}
	for _, item := range items {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("WriteQueueItem %s: %v", item.ID, err)
		}
	}

	list := NewRootCmd()
	listOut, listErr := &bytes.Buffer{}, &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetErr(listErr)
	list.SetArgs([]string{
		"queue", "ls",
		"--target", tmp,
		"--instance", "worker,manager",
		"--event-type", "agent.dispatch",
		"--job", "SQU-96",
		"--ready",
		"--json",
	})
	if err := list.Execute(); err != nil {
		t.Fatalf("queue ls filters: %v\nstderr=%s", err, listErr.String())
	}
	var listed []daemon.QueueItem
	if err := json.Unmarshal(listOut.Bytes(), &listed); err != nil {
		t.Fatalf("decode list json: %v\nbody=%s", err, listOut.String())
	}
	if len(listed) != 1 || listed[0].ID != "q-ready" {
		t.Fatalf("listed = %+v", listed)
	}

	textList := NewRootCmd()
	textListOut, textListErr := &bytes.Buffer{}, &bytes.Buffer{}
	textList.SetOut(textListOut)
	textList.SetErr(textListErr)
	textList.SetArgs([]string{"queue", "ls", "--target", tmp, "--instance", "worker", "--event-type", "agent.dispatch"})
	if err := textList.Execute(); err != nil {
		t.Fatalf("queue ls text: %v\nstderr=%s", err, textListErr.String())
	}
	for _, want := range []string{
		"q-ready",
		"agent-team queue drain; agent-team queue drop q-ready",
		"q-delayed",
		"agent-team queue show q-delayed; agent-team queue drop q-delayed",
		"q-dead-worker",
		"agent-team queue retry q-dead-worker; agent-team queue drop q-dead-worker",
	} {
		if !strings.Contains(textListOut.String(), want) {
			t.Fatalf("queue ls text missing %q:\n%s", want, textListOut.String())
		}
	}

	summaryCmd := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summaryCmd.SetOut(summaryOut)
	summaryCmd.SetErr(summaryErr)
	summaryCmd.SetArgs([]string{
		"queue", "ls",
		"--target", tmp,
		"--summary",
		"--instance", "worker",
		"--event-type", "agent.dispatch",
		"--json",
	})
	if err := summaryCmd.Execute(); err != nil {
		t.Fatalf("queue ls filtered summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var summary queueSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &summary); err != nil {
		t.Fatalf("decode summary json: %v\nbody=%s", err, summaryOut.String())
	}
	if summary.Total != 3 || summary.Pending != 2 || summary.Dead != 1 || summary.Delayed != 1 || summary.Attempts != daemon.MaxQueueAttempts+3 {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.Instances["worker"] != 3 || summary.Events["agent.dispatch"] != 3 {
		t.Fatalf("summary maps = %+v", summary)
	}

	bad := NewRootCmd()
	badOut, badErr := &bytes.Buffer{}, &bytes.Buffer{}
	bad.SetOut(badOut)
	bad.SetErr(badErr)
	bad.SetArgs([]string{"queue", "ls", "--target", tmp, "--instance", ","})
	if err := bad.Execute(); err == nil {
		t.Fatalf("queue ls empty instance succeeded; stdout=%s", badOut.String())
	}
	if !strings.Contains(badErr.String(), "--instance requires at least one non-empty instance") {
		t.Fatalf("bad stderr = %q", badErr.String())
	}
}

func TestQueueDropAllLocal(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	items := []*daemon.QueueItem{
		{
			ID:             "q-drop-worker",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     "worker-squ-104",
			Payload:        map[string]any{"target": "worker", "ticket": "SQU-104"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-2 * time.Hour),
			DeadLetteredAt: now.Add(-2 * time.Hour),
		},
		{
			ID:             "q-drop-manager",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "manager",
			InstanceID:     "manager-squ-105",
			Payload:        map[string]any{"target": "manager", "ticket": "SQU-105"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-2 * time.Hour),
			DeadLetteredAt: now.Add(-2 * time.Hour),
		},
		{
			ID:         "q-drop-ready",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-106",
			Payload:    map[string]any{"target": "worker", "ticket": "SQU-106"},
			NextRetry:  now.Add(-time.Minute),
			QueuedAt:   now.Add(-time.Hour),
			UpdatedAt:  now,
		},
		{
			ID:         "q-drop-delayed",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-107",
			Payload:    map[string]any{"target": "worker", "ticket": "SQU-107"},
			NextRetry:  now.Add(time.Hour),
			QueuedAt:   now.Add(-time.Hour),
			UpdatedAt:  now,
		},
	}
	for _, item := range items {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("WriteQueueItem %s: %v", item.ID, err)
		}
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{
		"queue", "drop",
		"--target", tmp,
		"--all",
		"--instance", "worker",
		"--event-type", "agent.dispatch",
		"--job", "SQU-104",
		"--dry-run",
		"--json",
	})
	if err := dry.Execute(); err != nil {
		t.Fatalf("queue drop --all dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryResults []queueDropResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryResults); err != nil {
		t.Fatalf("decode dry drop json: %v\nbody=%s", err, dryOut.String())
	}
	if len(dryResults) != 1 || dryResults[0].ID != "q-drop-worker" || dryResults[0].Action != "would_drop" || !dryResults[0].DryRun {
		t.Fatalf("dry results = %+v", dryResults)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-drop-worker"); err != nil {
		t.Fatalf("dry-run removed worker item: %v", err)
	}

	ready := NewRootCmd()
	readyOut, readyErr := &bytes.Buffer{}, &bytes.Buffer{}
	ready.SetOut(readyOut)
	ready.SetErr(readyErr)
	ready.SetArgs([]string{"queue", "drop", "--target", tmp, "--all", "--ready", "--dry-run", "--json"})
	if err := ready.Execute(); err != nil {
		t.Fatalf("queue drop --all ready dry-run: %v\nstderr=%s", err, readyErr.String())
	}
	var readyResults []queueDropResult
	if err := json.Unmarshal(readyOut.Bytes(), &readyResults); err != nil {
		t.Fatalf("decode ready drop json: %v\nbody=%s", err, readyOut.String())
	}
	if len(readyResults) != 1 || readyResults[0].ID != "q-drop-ready" {
		t.Fatalf("ready results = %+v", readyResults)
	}

	apply := NewRootCmd()
	applyOut, applyErr := &bytes.Buffer{}, &bytes.Buffer{}
	apply.SetOut(applyOut)
	apply.SetErr(applyErr)
	apply.SetArgs([]string{
		"queue", "drop",
		"--target", tmp,
		"--all",
		"--instance", "worker",
		"--event-type", "agent.dispatch",
		"--job", "SQU-104",
		"--json",
	})
	if err := apply.Execute(); err != nil {
		t.Fatalf("queue drop --all apply: %v\nstderr=%s", err, applyErr.String())
	}
	var applied []queueDropResult
	if err := json.Unmarshal(applyOut.Bytes(), &applied); err != nil {
		t.Fatalf("decode applied drop json: %v\nbody=%s", err, applyOut.String())
	}
	if len(applied) != 1 || applied[0].ID != "q-drop-worker" || applied[0].Action != "dropped" {
		t.Fatalf("applied = %+v", applied)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-drop-worker"); !os.IsNotExist(err) {
		t.Fatalf("worker item still exists or unexpected err=%v", err)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-drop-manager"); err != nil || item.State != daemon.QueueStateDead {
		t.Fatalf("manager item=%+v err=%v", item, err)
	}
}

func TestQueueRetryAllLocal(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	items := []*daemon.QueueItem{
		{
			ID:             "q-retry-worker",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     "worker-squ-100",
			Payload:        map[string]any{"target": "worker", "ticket": "SQU-100"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-2 * time.Hour),
			DeadLetteredAt: now.Add(-2 * time.Hour),
		},
		{
			ID:             "q-retry-manager",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "manager",
			InstanceID:     "manager-squ-101",
			Payload:        map[string]any{"target": "manager", "ticket": "SQU-101"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-2 * time.Hour),
			DeadLetteredAt: now.Add(-2 * time.Hour),
		},
		{
			ID:         "q-ready-pending",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-102",
			Payload:    map[string]any{"target": "worker", "ticket": "SQU-102"},
			NextRetry:  now.Add(-time.Minute),
			QueuedAt:   now.Add(-time.Hour),
			UpdatedAt:  now,
		},
		{
			ID:         "q-delayed-pending",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-103",
			Payload:    map[string]any{"target": "worker", "ticket": "SQU-103"},
			NextRetry:  now.Add(time.Hour),
			QueuedAt:   now.Add(-time.Hour),
			UpdatedAt:  now,
		},
	}
	for _, item := range items {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("WriteQueueItem %s: %v", item.ID, err)
		}
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{
		"queue", "retry",
		"--target", tmp,
		"--all",
		"--instance", "worker",
		"--event-type", "agent.dispatch",
		"--job", "SQU-100",
		"--dry-run",
		"--json",
	})
	if err := dry.Execute(); err != nil {
		t.Fatalf("queue retry --all dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryResults []queueRetryResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryResults); err != nil {
		t.Fatalf("decode dry retry json: %v\nbody=%s", err, dryOut.String())
	}
	if len(dryResults) != 1 || dryResults[0].ID != "q-retry-worker" || dryResults[0].Action != "would_retry" || !dryResults[0].DryRun {
		t.Fatalf("dry results = %+v", dryResults)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-retry-worker"); err != nil || item.State != daemon.QueueStateDead {
		t.Fatalf("dry-run changed item=%+v err=%v", item, err)
	}

	ready := NewRootCmd()
	readyOut, readyErr := &bytes.Buffer{}, &bytes.Buffer{}
	ready.SetOut(readyOut)
	ready.SetErr(readyErr)
	ready.SetArgs([]string{"queue", "retry", "--target", tmp, "--all", "--ready", "--dry-run", "--json"})
	if err := ready.Execute(); err != nil {
		t.Fatalf("queue retry --all ready dry-run: %v\nstderr=%s", err, readyErr.String())
	}
	var readyResults []queueRetryResult
	if err := json.Unmarshal(readyOut.Bytes(), &readyResults); err != nil {
		t.Fatalf("decode ready retry json: %v\nbody=%s", err, readyOut.String())
	}
	if len(readyResults) != 1 || readyResults[0].ID != "q-ready-pending" {
		t.Fatalf("ready results = %+v", readyResults)
	}

	apply := NewRootCmd()
	applyOut, applyErr := &bytes.Buffer{}, &bytes.Buffer{}
	apply.SetOut(applyOut)
	apply.SetErr(applyErr)
	apply.SetArgs([]string{
		"queue", "retry",
		"--target", tmp,
		"--all",
		"--instance", "worker",
		"--event-type", "agent.dispatch",
		"--job", "SQU-100",
		"--json",
	})
	if err := apply.Execute(); err != nil {
		t.Fatalf("queue retry --all apply: %v\nstderr=%s", err, applyErr.String())
	}
	var applied []queueRetryResult
	if err := json.Unmarshal(applyOut.Bytes(), &applied); err != nil {
		t.Fatalf("decode applied retry json: %v\nbody=%s", err, applyOut.String())
	}
	if len(applied) != 1 || applied[0].ID != "q-retry-worker" || applied[0].Action != "reset" {
		t.Fatalf("applied = %+v", applied)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-retry-worker"); err != nil || item.State != daemon.QueueStatePending || item.LastError != "" || !item.DeadLetteredAt.IsZero() {
		t.Fatalf("retried worker item=%+v err=%v", item, err)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-retry-manager"); err != nil || item.State != daemon.QueueStateDead {
		t.Fatalf("manager item=%+v err=%v", item, err)
	}
}

func TestQueuePruneLocal(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	items := []*daemon.QueueItem{
		{
			ID:         "q-pending",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-93",
			Payload:    map[string]any{"target": "worker", "ticket": "SQU-93"},
			QueuedAt:   now.Add(-48 * time.Hour),
			UpdatedAt:  now.Add(-48 * time.Hour),
		},
		{
			ID:             "q-dead-old",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     "worker-squ-94",
			Payload:        map[string]any{"target": "worker", "ticket": "SQU-94"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:      now.Add(-47 * time.Hour),
			DeadLetteredAt: now.Add(-47 * time.Hour),
		},
		{
			ID:             "q-dead-new",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     "worker-squ-95",
			Payload:        map[string]any{"target": "worker", "ticket": "SQU-95"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-time.Hour),
			UpdatedAt:      now.Add(-time.Hour),
			DeadLetteredAt: now.Add(-time.Hour),
		},
	}
	for _, item := range items {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("WriteQueueItem %s: %v", item.ID, err)
		}
	}
	writeQuarantinedQueueItem(t, teamDir, "20260619T040000.000000000Z", daemon.QueueStateDead, &daemon.QueueItem{
		ID:         "q-prune-quarantined",
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-96",
		Payload:    map[string]any{"target": "worker", "ticket": "SQU-96"},
		QueuedAt:   now.Add(-2 * time.Hour),
		UpdatedAt:  now.Add(-2 * time.Hour),
	})

	summaryCmd := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summaryCmd.SetOut(summaryOut)
	summaryCmd.SetErr(summaryErr)
	summaryCmd.SetArgs([]string{"queue", "ls", "--target", tmp, "--summary", "--json"})
	if err := summaryCmd.Execute(); err != nil {
		t.Fatalf("queue ls summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var summary queueSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &summary); err != nil {
		t.Fatalf("decode queue summary json: %v\nbody=%s", err, summaryOut.String())
	}
	if summary.Total != 3 || summary.Pending != 1 || summary.Dead != 2 || summary.Attempts != daemon.MaxQueueAttempts*2 || summary.Quarantined != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.Instances["worker"] != 3 || summary.Events["agent.dispatch"] != 3 {
		t.Fatalf("summary maps = %+v", summary)
	}

	dryRun := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dryRun.SetOut(dryOut)
	dryRun.SetErr(dryErr)
	dryRun.SetArgs([]string{"queue", "prune", "--target", tmp, "--older-than", "24h", "--dry-run", "--json"})
	if err := dryRun.Execute(); err != nil {
		t.Fatalf("queue prune dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dry []queuePruneResult
	if err := json.Unmarshal(dryOut.Bytes(), &dry); err != nil {
		t.Fatalf("decode dry prune json: %v\nbody=%s", err, dryOut.String())
	}
	if len(dry) != 1 || dry[0].ID != "q-dead-old" || !dry[0].DryRun || dry[0].Dropped {
		t.Fatalf("dry results = %+v", dry)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-dead-old"); err != nil {
		t.Fatalf("dry-run removed item: %v", err)
	}

	prune := NewRootCmd()
	pruneOut, pruneErr := &bytes.Buffer{}, &bytes.Buffer{}
	prune.SetOut(pruneOut)
	prune.SetErr(pruneErr)
	prune.SetArgs([]string{"queue", "prune", "--target", tmp, "--older-than", "24h", "--format", "{{.ID}} {{.State}} {{.Dropped}}"})
	if err := prune.Execute(); err != nil {
		t.Fatalf("queue prune: %v\nstderr=%s", err, pruneErr.String())
	}
	if got := strings.TrimSpace(pruneOut.String()); got != "q-dead-old dead true" {
		t.Fatalf("prune output = %q", got)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-dead-old"); !os.IsNotExist(err) {
		t.Fatalf("dead old item err=%v, want not exist", err)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-pending"); err != nil {
		t.Fatalf("pending should remain: %v", err)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-dead-new"); err != nil {
		t.Fatalf("new dead should remain: %v", err)
	}

	pruneAll := NewRootCmd()
	allOut, allErr := &bytes.Buffer{}, &bytes.Buffer{}
	pruneAll.SetOut(allOut)
	pruneAll.SetErr(allErr)
	pruneAll.SetArgs([]string{"queue", "prune", "--target", tmp, "--state", "all", "--json"})
	if err := pruneAll.Execute(); err != nil {
		t.Fatalf("queue prune all: %v\nstderr=%s", err, allErr.String())
	}
	var all []queuePruneResult
	if err := json.Unmarshal(allOut.Bytes(), &all); err != nil {
		t.Fatalf("decode all prune json: %v\nbody=%s", err, allOut.String())
	}
	if len(all) != 2 || !all[0].Dropped || !all[1].Dropped {
		t.Fatalf("all results = %+v", all)
	}
}

func TestQueueRetryDryRunSingleDoesNotRequireDaemon(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	item := &daemon.QueueItem{
		ID:         "q-retry-one",
		State:      daemon.QueueStateDead,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-96",
		Payload:    map[string]any{"target": "worker", "ticket": "SQU-96"},
		Attempts:   daemon.MaxQueueAttempts,
		LastError:  "spawn failed",
		QueuedAt:   now.Add(-time.Hour),
		UpdatedAt:  now.Add(-time.Hour),
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
		t.Fatalf("WriteQueueItem: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"queue", "retry", "q-retry-one", "--target", tmp, "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("queue retry dry-run single: %v\nstderr=%s", err, stderr.String())
	}
	var results []queueRetryResult
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		t.Fatalf("decode retry dry-run: %v\nbody=%s", err, out.String())
	}
	if len(results) != 1 || results[0].ID != "q-retry-one" || results[0].Action != "would_retry" || !results[0].DryRun {
		t.Fatalf("retry dry-run results = %+v", results)
	}
	unchanged, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-retry-one")
	if err != nil {
		t.Fatalf("retry dry-run removed item: %v", err)
	}
	if unchanged.State != daemon.QueueStateDead || unchanged.LastError != "spawn failed" || unchanged.Attempts != daemon.MaxQueueAttempts {
		t.Fatalf("retry dry-run changed item = %+v", unchanged)
	}

	textCmd := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	textCmd.SetOut(textOut)
	textCmd.SetErr(textErr)
	textCmd.SetArgs([]string{"queue", "retry", "q-retry-one", "--target", tmp, "--dry-run"})
	if err := textCmd.Execute(); err != nil {
		t.Fatalf("queue retry dry-run single text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"q-retry-one", "would_retry", "worker-squ-96"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("retry dry-run text missing %q:\n%s", want, textOut.String())
		}
	}
}

func TestQueueRetryThroughDaemon(t *testing.T) {
	target, mgr, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Now().UTC()
	item := &daemon.QueueItem{
		ID:         "q-retry",
		State:      daemon.QueueStateDead,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-91",
		Payload:    map[string]any{"target": "worker", "name": "worker-squ-91", "ticket": "SQU-91"},
		Attempts:   daemon.MaxQueueAttempts,
		LastError:  "spawn failed",
		QueuedAt:   now,
		UpdatedAt:  now,
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
		t.Fatalf("WriteQueueItem: %v", err)
	}

	retry := NewRootCmd()
	retryOut, retryErr := &bytes.Buffer{}, &bytes.Buffer{}
	retry.SetOut(retryOut)
	retry.SetErr(retryErr)
	retry.SetArgs([]string{"queue", "retry", "q-retry", "--target", target, "--json"})
	if err := retry.Execute(); err != nil {
		t.Fatalf("queue retry: %v\nstderr=%s", err, retryErr.String())
	}
	var outcome daemon.EventOutcome
	if err := json.Unmarshal(retryOut.Bytes(), &outcome); err != nil {
		t.Fatalf("decode retry: %v\nbody=%s", err, retryOut.String())
	}
	if outcome.Action != "dispatched" || outcome.InstanceID != "worker-squ-91" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-retry"); !os.IsNotExist(err) {
		t.Fatalf("queue item should be removed after retry dispatch, err=%v", err)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-91")
}

func TestQueueDrainThroughDaemon(t *testing.T) {
	target, mgr, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Now().UTC()
	item := &daemon.QueueItem{
		ID:         "q-drain",
		State:      daemon.QueueStatePending,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-92",
		Payload:    map[string]any{"target": "worker", "name": "worker-squ-92", "ticket": "SQU-92"},
		QueuedAt:   now,
		UpdatedAt:  now,
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
		t.Fatalf("WriteQueueItem: %v", err)
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"queue", "drain", "--target", target, "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("queue drain dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview daemon.QueueDrainResult
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode drain dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if !preview.DryRun || preview.WouldDispatch != 1 || preview.Dispatched != 0 || preview.Pending != 1 {
		t.Fatalf("drain preview = %+v", preview)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-drain"); err != nil {
		t.Fatalf("dry-run removed queue item: %v", err)
	}

	drain := NewRootCmd()
	drainOut, drainErr := &bytes.Buffer{}, &bytes.Buffer{}
	drain.SetOut(drainOut)
	drain.SetErr(drainErr)
	drain.SetArgs([]string{"queue", "drain", "--target", target, "--json"})
	if err := drain.Execute(); err != nil {
		t.Fatalf("queue drain: %v\nstderr=%s", err, drainErr.String())
	}
	var result daemon.QueueDrainResult
	if err := json.Unmarshal(drainOut.Bytes(), &result); err != nil {
		t.Fatalf("decode drain: %v\nbody=%s", err, drainOut.String())
	}
	if result.Attempted != 1 || result.Dispatched != 1 || result.Pending != 0 || result.Dead != 0 {
		t.Fatalf("drain result = %+v", result)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" || result.Outcomes[0].InstanceID != "worker-squ-92" {
		t.Fatalf("drain outcomes = %+v", result.Outcomes)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-drain"); !os.IsNotExist(err) {
		t.Fatalf("queue item should be removed after drain, err=%v", err)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-92")
}

func TestQueueDrainDryRunDoesNotRequireDaemon(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-ready",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-93",
			Payload:    map[string]any{"target": "worker", "name": "worker-squ-93", "ticket": "SQU-93"},
			QueuedAt:   now.Add(-2 * time.Minute),
			UpdatedAt:  now.Add(-2 * time.Minute),
		},
		{
			ID:         "q-delayed",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-94",
			Payload:    map[string]any{"target": "worker", "name": "worker-squ-94", "ticket": "SQU-94"},
			NextRetry:  now.Add(time.Hour),
			QueuedAt:   now.Add(-time.Minute),
			UpdatedAt:  now.Add(-time.Minute),
		},
		{
			ID:         "q-dead",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-95",
			Payload:    map[string]any{"target": "worker", "name": "worker-squ-95", "ticket": "SQU-95"},
			QueuedAt:   now.Add(-3 * time.Minute),
			UpdatedAt:  now.Add(-3 * time.Minute),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("WriteQueueItem %s: %v", item.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"queue", "drain", "--target", tmp, "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("queue drain dry-run offline: %v\nstderr=%s", err, stderr.String())
	}
	var result daemon.QueueDrainResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode offline drain dry-run: %v\nbody=%s", err, out.String())
	}
	if !result.DryRun || result.WouldDispatch != 1 || result.Pending != 2 || result.Dead != 1 || result.Dispatched != 0 {
		t.Fatalf("offline drain preview = %+v", result)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "would_dispatch" || result.Outcomes[0].InstanceID != "worker-squ-93" {
		t.Fatalf("offline drain outcomes = %+v", result.Outcomes)
	}
	for _, id := range []string{"q-ready", "q-delayed", "q-dead"} {
		if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), id); err != nil {
			t.Fatalf("dry-run removed queue item %s: %v", id, err)
		}
	}

	textCmd := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	textCmd.SetOut(textOut)
	textCmd.SetErr(textErr)
	textCmd.SetArgs([]string{"queue", "drain", "--target", tmp, "--dry-run"})
	if err := textCmd.Execute(); err != nil {
		t.Fatalf("queue drain dry-run offline text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"queue drain dry-run: would_dispatch=1 pending=2 dead=1", "worker-squ-93", "would_dispatch"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("offline drain text missing %q:\n%s", want, textOut.String())
		}
	}
}
