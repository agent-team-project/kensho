package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
)

func TestOutboxListShowRetryDrop(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	writeCLIOutboxItem(t, teamDir, &daemon.OutboxItem{
		ID:        "outbox-a",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Payload:   map[string]any{"job_id": "squ-501", "ticket": "SQU-501", "target": "worker"},
		Source:    "manager",
		CreatedAt: now,
		UpdatedAt: now,
	})
	writeCLIOutboxItem(t, teamDir, &daemon.OutboxItem{
		ID:        "outbox-b",
		State:     daemon.OutboxStateFailed,
		Type:      "agent.dispatch",
		Payload:   map[string]any{"job_id": "squ-502", "ticket": "SQU-502", "target": "worker"},
		Source:    "manager",
		LastError: "no route",
		CreatedAt: now.Add(time.Minute),
		UpdatedAt: now.Add(time.Minute),
	})

	out := runRootForOutboxTest(t, "outbox", "ls", "--target", target, "--json")
	var listed []*daemon.OutboxItem
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil {
		t.Fatalf("decode outbox ls: %v\n%s", err, out.String())
	}
	if len(listed) != 2 || listed[0].ID != "outbox-a" || listed[1].ID != "outbox-b" {
		t.Fatalf("outbox list = %+v, want outbox-a/outbox-b", listed)
	}

	filtered := runRootForOutboxTest(t, "outbox", "ls", "--target", target, "--state", "pending", "--job", "SQU-501", "--format", "{{.ID}} {{.State}}")
	if strings.TrimSpace(filtered.String()) != "outbox-a pending" {
		t.Fatalf("filtered output = %q", filtered.String())
	}

	shown := runRootForOutboxTest(t, "outbox", "show", "--target", target, "outbox-b", "--json")
	var shownItem daemon.OutboxItem
	if err := json.Unmarshal(shown.Bytes(), &shownItem); err != nil {
		t.Fatalf("decode outbox show: %v\n%s", err, shown.String())
	}
	if shownItem.ID != "outbox-b" || shownItem.State != daemon.OutboxStateFailed || shownItem.LastError != "no route" {
		t.Fatalf("shown item = %+v", shownItem)
	}

	retry := runRootForOutboxTest(t, "outbox", "retry", "--target", target, "outbox-b", "--json")
	var retryRows []outboxActionResult
	if err := json.Unmarshal(retry.Bytes(), &retryRows); err != nil {
		t.Fatalf("decode retry: %v\n%s", err, retry.String())
	}
	if len(retryRows) != 1 || retryRows[0].Action != "retried" || retryRows[0].State != daemon.OutboxStatePending {
		t.Fatalf("retry rows = %+v", retryRows)
	}
	retried, err := daemon.ReadOutboxItem(teamDir, "outbox-b")
	if err != nil {
		t.Fatalf("read retried item: %v", err)
	}
	if retried.State != daemon.OutboxStatePending || retried.LastError != "" {
		t.Fatalf("retried item = %+v, want pending with cleared error", retried)
	}

	drop := runRootForOutboxTest(t, "outbox", "drop", "--target", target, "outbox-a", "--json")
	var dropRows []outboxActionResult
	if err := json.Unmarshal(drop.Bytes(), &dropRows); err != nil {
		t.Fatalf("decode drop: %v\n%s", err, drop.String())
	}
	if len(dropRows) != 1 || dropRows[0].Action != "dropped" {
		t.Fatalf("drop rows = %+v", dropRows)
	}
	if _, err := daemon.ReadOutboxItem(teamDir, "outbox-a"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outbox-a should be removed, err=%v", err)
	}

	writeCLIOutboxItem(t, teamDir, &daemon.OutboxItem{
		ID:        "outbox-c",
		State:     daemon.OutboxStateFailed,
		Type:      "agent.dispatch",
		Payload:   map[string]any{"job_id": "squ-503", "ticket": "SQU-503", "target": "worker"},
		Source:    "manager",
		LastError: "missing worker route",
		CreatedAt: now.Add(2 * time.Minute),
		UpdatedAt: now.Add(2 * time.Minute),
	})
	writeCLIOutboxItem(t, teamDir, &daemon.OutboxItem{
		ID:        "outbox-d",
		State:     daemon.OutboxStateFailed,
		Type:      "agent.dispatch",
		Payload:   map[string]any{"job_id": "squ-504", "ticket": "SQU-504", "target": "worker"},
		Source:    "manager",
		LastError: "stale event",
		CreatedAt: now.Add(3 * time.Minute),
		UpdatedAt: now.Add(3 * time.Minute),
	})
	writeCLIOutboxItem(t, teamDir, &daemon.OutboxItem{
		ID:        "outbox-e",
		State:     daemon.OutboxStateProcessed,
		Type:      "agent.dispatch",
		Payload:   map[string]any{"job_id": "squ-505", "ticket": "SQU-505", "target": "worker"},
		Source:    "worker",
		CreatedAt: now.Add(4 * time.Minute),
		UpdatedAt: now.Add(4 * time.Minute),
	})

	retryAll := runRootForOutboxTest(t, "outbox", "retry", "--target", target, "--all", "--source", "manager", "--sort", "id", "--limit", "1", "--json")
	var retryAllRows []outboxActionResult
	if err := json.Unmarshal(retryAll.Bytes(), &retryAllRows); err != nil {
		t.Fatalf("decode retry all: %v\n%s", err, retryAll.String())
	}
	if len(retryAllRows) != 1 || retryAllRows[0].ID != "outbox-c" || retryAllRows[0].Action != "retried" {
		t.Fatalf("retry all rows = %+v", retryAllRows)
	}
	retriedAll, err := daemon.ReadOutboxItem(teamDir, "outbox-c")
	if err != nil || retriedAll.State != daemon.OutboxStatePending || retriedAll.LastError != "" {
		t.Fatalf("retry all item=%+v err=%v", retriedAll, err)
	}
	processed, err := daemon.ReadOutboxItem(teamDir, "outbox-e")
	if err != nil || processed.State != daemon.OutboxStateProcessed {
		t.Fatalf("retry all changed processed item=%+v err=%v", processed, err)
	}

	dropAllDryRun := runRootForOutboxTest(t, "outbox", "drop", "--target", target, "--all", "--state", "failed", "--job", "SQU-504", "--sort", "id", "--dry-run", "--json")
	var dropAllDryRunRows []outboxActionResult
	if err := json.Unmarshal(dropAllDryRun.Bytes(), &dropAllDryRunRows); err != nil {
		t.Fatalf("decode drop all dry-run: %v\n%s", err, dropAllDryRun.String())
	}
	if len(dropAllDryRunRows) != 1 || dropAllDryRunRows[0].ID != "outbox-d" || dropAllDryRunRows[0].Action != "would_drop" || !dropAllDryRunRows[0].DryRun {
		t.Fatalf("drop all dry-run rows = %+v", dropAllDryRunRows)
	}
	if _, err := daemon.ReadOutboxItem(teamDir, "outbox-d"); err != nil {
		t.Fatalf("dry-run removed outbox-d: %v", err)
	}

	dropAll := runRootForOutboxTest(t, "outbox", "drop", "--target", target, "--all", "--state", "failed", "--job", "SQU-504", "--sort", "id", "--json")
	var dropAllRows []outboxActionResult
	if err := json.Unmarshal(dropAll.Bytes(), &dropAllRows); err != nil {
		t.Fatalf("decode drop all: %v\n%s", err, dropAll.String())
	}
	if len(dropAllRows) != 1 || dropAllRows[0].ID != "outbox-d" || dropAllRows[0].Action != "dropped" {
		t.Fatalf("drop all rows = %+v", dropAllRows)
	}
	if _, err := daemon.ReadOutboxItem(teamDir, "outbox-d"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outbox-d should be removed, err=%v", err)
	}
}

func TestOutboxPruneLocal(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Now().UTC().Truncate(time.Second)
	for _, item := range []*daemon.OutboxItem{
		{
			ID:          "outbox-processed-old",
			State:       daemon.OutboxStateProcessed,
			Type:        "agent.dispatch",
			Payload:     map[string]any{"job_id": "squ-601", "ticket": "SQU-601", "target": "worker"},
			Source:      "manager",
			CreatedAt:   now.Add(-72 * time.Hour),
			UpdatedAt:   now.Add(-72 * time.Hour),
			ProcessedAt: now.Add(-72 * time.Hour),
		},
		{
			ID:          "outbox-processed-mid",
			State:       daemon.OutboxStateProcessed,
			Type:        "agent.dispatch",
			Payload:     map[string]any{"job_id": "squ-602", "ticket": "SQU-602", "target": "worker"},
			Source:      "manager",
			CreatedAt:   now.Add(-48 * time.Hour),
			UpdatedAt:   now.Add(-48 * time.Hour),
			ProcessedAt: now.Add(-48 * time.Hour),
		},
		{
			ID:          "outbox-processed-new",
			State:       daemon.OutboxStateProcessed,
			Type:        "agent.dispatch",
			Payload:     map[string]any{"job_id": "squ-603", "ticket": "SQU-603", "target": "worker"},
			Source:      "manager",
			CreatedAt:   now.Add(-time.Hour),
			UpdatedAt:   now.Add(-time.Hour),
			ProcessedAt: now.Add(-time.Hour),
		},
		{
			ID:        "outbox-failed-old",
			State:     daemon.OutboxStateFailed,
			Type:      "agent.dispatch",
			Payload:   map[string]any{"job_id": "squ-604", "ticket": "SQU-604", "target": "worker"},
			Source:    "manager",
			CreatedAt: now.Add(-48 * time.Hour),
			UpdatedAt: now.Add(-48 * time.Hour),
			FailedAt:  now.Add(-48 * time.Hour),
		},
		{
			ID:        "outbox-pending-old",
			State:     daemon.OutboxStatePending,
			Type:      "agent.dispatch",
			Payload:   map[string]any{"job_id": "squ-605", "ticket": "SQU-605", "target": "worker"},
			Source:    "manager",
			CreatedAt: now.Add(-72 * time.Hour),
			UpdatedAt: now.Add(-72 * time.Hour),
		},
	} {
		writeCLIOutboxItem(t, teamDir, item)
	}

	dry := runRootForOutboxTest(t, "outbox", "prune", "--target", target, "--older-than", "24h", "--limit", "1", "--dry-run", "--json")
	var dryRows []outboxPruneResult
	if err := json.Unmarshal(dry.Bytes(), &dryRows); err != nil {
		t.Fatalf("decode prune dry-run: %v\n%s", err, dry.String())
	}
	if len(dryRows) != 1 || dryRows[0].ID != "outbox-processed-old" || !dryRows[0].DryRun || dryRows[0].Dropped {
		t.Fatalf("prune dry-run rows = %+v", dryRows)
	}
	if _, err := daemon.ReadOutboxItem(teamDir, "outbox-processed-old"); err != nil {
		t.Fatalf("dry-run removed processed old: %v", err)
	}

	pruned := runRootForOutboxTest(t, "outbox", "prune", "--target", target, "--older-than", "24h", "--format", "{{.ID}} {{.State}} {{.Dropped}}")
	if got, want := strings.TrimSpace(pruned.String()), "outbox-processed-old processed true\noutbox-processed-mid processed true"; got != want {
		t.Fatalf("prune output = %q, want %q", got, want)
	}
	for _, id := range []string{"outbox-processed-old", "outbox-processed-mid"} {
		if _, err := daemon.ReadOutboxItem(teamDir, id); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s should be removed, err=%v", id, err)
		}
	}
	for _, id := range []string{"outbox-processed-new", "outbox-failed-old", "outbox-pending-old"} {
		if _, err := daemon.ReadOutboxItem(teamDir, id); err != nil {
			t.Fatalf("%s should remain after default prune: %v", id, err)
		}
	}

	failed := runRootForOutboxTest(t, "outbox", "prune", "--target", target, "--state", "failed", "--older-than", "24h", "--source", "manager", "--json")
	var failedRows []outboxPruneResult
	if err := json.Unmarshal(failed.Bytes(), &failedRows); err != nil {
		t.Fatalf("decode failed prune: %v\n%s", err, failed.String())
	}
	if len(failedRows) != 1 || failedRows[0].ID != "outbox-failed-old" || !failedRows[0].Dropped {
		t.Fatalf("failed prune rows = %+v", failedRows)
	}
	if _, err := daemon.ReadOutboxItem(teamDir, "outbox-failed-old"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outbox-failed-old should be removed, err=%v", err)
	}
	if _, err := daemon.ReadOutboxItem(teamDir, "outbox-pending-old"); err != nil {
		t.Fatalf("pending item should remain after failed prune: %v", err)
	}
}

func TestOutboxDoctorFindsAndQuarantinesProblems(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	writeCLIOutboxItem(t, teamDir, &daemon.OutboxItem{
		ID:        "outbox-valid",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Payload:   map[string]any{"job_id": "squ-610", "target": "worker"},
		Source:    "manager",
		CreatedAt: now,
		UpdatedAt: now,
	})
	writeRawOutboxFile(t, teamDir, daemon.OutboxStateProcessed, "bad-json.json", "{\n")
	writeRawOutboxFile(t, teamDir, daemon.OutboxStateFailed, "missing-type.json", `{
  "id": "missing-type",
  "state": "failed",
  "payload": {"job_id": "squ-611", "target": "worker"},
  "created_at": "2026-06-27T12:00:00Z",
  "updated_at": "2026-06-27T12:00:00Z",
  "failed_at": "2026-06-27T12:00:00Z"
}
`)
	writeRawOutboxFile(t, teamDir, daemon.OutboxStatePending, "path-id.json", `{
  "id": "stored-id",
  "state": "pending",
  "type": "agent.dispatch",
  "payload": {"job_id": "squ-612", "target": "worker"},
  "created_at": "2026-06-27T12:00:00Z",
  "updated_at": "2026-06-27T12:00:00Z"
}
`)
	writeRawOutboxFile(t, teamDir, daemon.OutboxStatePending, "README.txt", "operator note\n")

	out, stderr, err := runRootForOutboxTestErr(t, "outbox", "doctor", "--target", target, "--json")
	if err == nil {
		t.Fatalf("outbox doctor succeeded with corrupt outbox files")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("outbox doctor err = %v, want exit 1", err)
	}
	var result outboxDoctorResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode outbox doctor: %v\nstdout=%s stderr=%s", err, out.String(), stderr.String())
	}
	if result.OK || result.Summary.Files != 4 || result.Summary.Valid != 1 || result.Summary.Invalid != 3 || result.Summary.Ignored != 1 {
		t.Fatalf("outbox doctor result = %+v", result)
	}
	for _, code := range []string{"invalid_json", "missing_type", "id_path_mismatch"} {
		if !outboxDoctorHasCode(result.Problems, code) {
			t.Fatalf("outbox doctor missing problem code %q: %+v", code, result.Problems)
		}
	}
	if !outboxDoctorHasCode(result.Warnings, "unexpected_file") {
		t.Fatalf("outbox doctor missing unexpected file warning: %+v", result.Warnings)
	}

	dryOut, dryErrOut, err := runRootForOutboxTestErr(t, "outbox", "doctor", "--target", target, "--quarantine", "--dry-run", "--json")
	if err != nil {
		t.Fatalf("outbox doctor quarantine dry-run: %v\nstderr=%s", err, dryErrOut.String())
	}
	var dryResult outboxDoctorResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryResult); err != nil {
		t.Fatalf("decode outbox doctor dry-run: %v\nstdout=%s", err, dryOut.String())
	}
	if dryResult.Quarantine == nil || !dryResult.Quarantine.DryRun || dryResult.Quarantine.Candidates != 3 || dryResult.Quarantine.Moved != 0 {
		t.Fatalf("outbox doctor dry-run quarantine = %+v", dryResult.Quarantine)
	}
	for _, rel := range []string{
		filepath.Join(daemon.OutboxStateProcessed, "bad-json.json"),
		filepath.Join(daemon.OutboxStateFailed, "missing-type.json"),
		filepath.Join(daemon.OutboxStatePending, "path-id.json"),
	} {
		if _, err := os.Stat(filepath.Join(daemon.OutboxRoot(teamDir), rel)); err != nil {
			t.Fatalf("dry-run moved %s: %v", rel, err)
		}
	}

	quarantineOut, quarantineErrOut, err := runRootForOutboxTestErr(t, "outbox", "doctor", "--target", target, "--quarantine", "--json")
	if err != nil {
		t.Fatalf("outbox doctor quarantine: %v\nstderr=%s", err, quarantineErrOut.String())
	}
	var quarantineResult outboxDoctorResult
	if err := json.Unmarshal(quarantineOut.Bytes(), &quarantineResult); err != nil {
		t.Fatalf("decode outbox doctor quarantine: %v\nstdout=%s", err, quarantineOut.String())
	}
	if !quarantineResult.OK || quarantineResult.Quarantine == nil || quarantineResult.Quarantine.Candidates != 3 || quarantineResult.Quarantine.Moved != 3 {
		t.Fatalf("outbox doctor quarantine result = %+v", quarantineResult)
	}
	for _, rel := range []string{
		filepath.Join(daemon.OutboxStateProcessed, "bad-json.json"),
		filepath.Join(daemon.OutboxStateFailed, "missing-type.json"),
		filepath.Join(daemon.OutboxStatePending, "path-id.json"),
	} {
		if _, err := os.Stat(filepath.Join(daemon.OutboxRoot(teamDir), rel)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s should be quarantined, err=%v", rel, err)
		}
	}
	listOut := runRootForOutboxTest(t, "outbox", "ls", "--target", target, "--json")
	var listed []*daemon.OutboxItem
	if err := json.Unmarshal(listOut.Bytes(), &listed); err != nil {
		t.Fatalf("decode outbox list after quarantine: %v\n%s", err, listOut.String())
	}
	if len(listed) != 1 || listed[0].ID != "outbox-valid" {
		t.Fatalf("outbox list after quarantine = %+v", listed)
	}
}

func TestOutboxQuarantineListShowRestoreDrop(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	stamp := "20260627T120000.000000000Z"
	writeQuarantinedOutboxFile(t, teamDir, stamp, daemon.OutboxStatePending, &daemon.OutboxItem{
		ID:        "outbox-restorable",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Payload:   map[string]any{"job_id": "SQU-710", "target": "worker"},
		Source:    "manager",
		CreatedAt: time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 6, 27, 12, 1, 0, 0, time.UTC),
	})
	writeQuarantinedOutboxFile(t, teamDir, stamp, daemon.OutboxStateFailed, &daemon.OutboxItem{
		ID:        "outbox-drop",
		State:     daemon.OutboxStateFailed,
		Type:      "agent.dispatch",
		Payload:   map[string]any{"job_id": "SQU-711", "target": "worker"},
		Source:    "manager",
		CreatedAt: time.Date(2026, 6, 27, 12, 2, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 6, 27, 12, 3, 0, 0, time.UTC),
		FailedAt:  time.Date(2026, 6, 27, 12, 3, 0, 0, time.UTC),
	})
	writeRawOutboxFile(t, teamDir, filepath.Join(outboxQuarantineDir, stamp, daemon.OutboxStateProcessed), "outbox-invalid.json", "{\n")
	restorablePath := filepath.Join(outboxQuarantineDir, stamp, daemon.OutboxStatePending, "outbox-restorable.json")
	dropPath := filepath.Join(outboxQuarantineDir, stamp, daemon.OutboxStateFailed, "outbox-drop.json")
	invalidPath := filepath.Join(outboxQuarantineDir, stamp, daemon.OutboxStateProcessed, "outbox-invalid.json")

	ls := runRootForOutboxTest(t, "outbox", "quarantine", "ls", "--target", target, "--json")
	var listed []outboxQuarantineItem
	if err := json.Unmarshal(ls.Bytes(), &listed); err != nil {
		t.Fatalf("decode outbox quarantine ls: %v\n%s", err, ls.String())
	}
	wantPaths := []string{dropPath, invalidPath, restorablePath}
	sort.Strings(wantPaths)
	if len(listed) != 3 || outboxQuarantinePaths(listed) != strings.Join(wantPaths, ",") {
		t.Fatalf("outbox quarantine list = %+v", listed)
	}

	filtered := runRootForOutboxTest(t, "outbox", "quarantine", "ls", "--target", target, "--job", "SQU-710", "--restorable", "--format", "{{.ID}} {{.Restorable}}")
	if got, want := strings.TrimSpace(filtered.String()), "outbox-restorable true"; got != want {
		t.Fatalf("outbox quarantine filtered output = %q, want %q", got, want)
	}

	show := runRootForOutboxTest(t, "outbox", "quarantine", "show", "--target", target, restorablePath, "--json")
	var shown outboxQuarantineShowResult
	if err := json.Unmarshal(show.Bytes(), &shown); err != nil {
		t.Fatalf("decode outbox quarantine show: %v\n%s", err, show.String())
	}
	if shown.ID != "outbox-restorable" || !shown.Restorable || shown.OutboxItem == nil || shown.OutboxItem.Payload["job_id"] != "SQU-710" {
		t.Fatalf("shown outbox quarantine item = %+v", shown)
	}
	showText := runRootForOutboxTest(t, "outbox", "quarantine", "show", "--target", target, restorablePath)
	for _, want := range []string{"Path:", "outbox-restorable", "Actions:", "agent-team outbox quarantine restore", "Payload:", "SQU-710"} {
		if !strings.Contains(showText.String(), want) {
			t.Fatalf("outbox quarantine show text missing %q:\n%s", want, showText.String())
		}
	}

	restoreAllDry := runRootForOutboxTest(t, "outbox", "quarantine", "restore", "--target", target, "--all", "--job", "SQU-710", "--dry-run", "--json")
	var restoreAllRows []outboxQuarantineRestoreResult
	if err := json.Unmarshal(restoreAllDry.Bytes(), &restoreAllRows); err != nil {
		t.Fatalf("decode outbox quarantine restore --all dry-run: %v\n%s", err, restoreAllDry.String())
	}
	if len(restoreAllRows) != 1 || restoreAllRows[0].ID != "outbox-restorable" || restoreAllRows[0].Action != "would_restore" || !restoreAllRows[0].DryRun {
		t.Fatalf("restore all dry-run rows = %+v", restoreAllRows)
	}

	dropAllDry := runRootForOutboxTest(t, "outbox", "quarantine", "drop", "--target", target, "--all", "--unrestorable", "--dry-run", "--json")
	var dropAllRows []outboxQuarantineDropResult
	if err := json.Unmarshal(dropAllDry.Bytes(), &dropAllRows); err != nil {
		t.Fatalf("decode outbox quarantine drop --all dry-run: %v\n%s", err, dropAllDry.String())
	}
	if len(dropAllRows) != 1 || dropAllRows[0].Path != invalidPath || dropAllRows[0].Action != "would_drop" {
		t.Fatalf("drop all dry-run rows = %+v", dropAllRows)
	}

	restore := runRootForOutboxTest(t, "outbox", "quarantine", "restore", "--target", target, restorablePath, "--json")
	var restoreRow outboxQuarantineRestoreResult
	if err := json.Unmarshal(restore.Bytes(), &restoreRow); err != nil {
		t.Fatalf("decode outbox quarantine restore: %v\n%s", err, restore.String())
	}
	if restoreRow.ID != "outbox-restorable" || restoreRow.Action != "restored" {
		t.Fatalf("restore row = %+v", restoreRow)
	}
	if _, err := daemon.ReadOutboxItem(teamDir, "outbox-restorable"); err != nil {
		t.Fatalf("restored outbox item is not readable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(daemon.OutboxRoot(teamDir), restorablePath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restored quarantine file should be gone, err=%v", err)
	}

	dropDry := runRootForOutboxTest(t, "outbox", "quarantine", "drop", "--target", target, dropPath, "--dry-run", "--format", "{{.ID}} {{.Action}} {{.DryRun}}")
	if got, want := strings.TrimSpace(dropDry.String()), "outbox-drop would_drop true"; got != want {
		t.Fatalf("outbox quarantine drop dry-run = %q, want %q", got, want)
	}
	drop := runRootForOutboxTest(t, "outbox", "quarantine", "drop", "--target", target, dropPath, "--json")
	var dropRows []outboxQuarantineDropResult
	if err := json.Unmarshal(drop.Bytes(), &dropRows); err != nil {
		t.Fatalf("decode outbox quarantine drop: %v\n%s", err, drop.String())
	}
	if len(dropRows) != 1 || dropRows[0].ID != "outbox-drop" || !dropRows[0].Dropped {
		t.Fatalf("drop rows = %+v", dropRows)
	}
	if _, err := os.Stat(filepath.Join(daemon.OutboxRoot(teamDir), dropPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dropped quarantine file should be gone, err=%v", err)
	}
}

func TestScopedOutboxPruneRespectsOwnership(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.other]
agent = "other"

[[instances.other.triggers]]
event = "agent.dispatch"
match.target = "other"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[pipelines.ops_review]
trigger.event = "ops.created"

[[pipelines.ops_review.steps]]
id = "implement"
target = "other"

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]

[teams.platform]
instances = ["other"]
pipelines = ["ops_review"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-72 * time.Hour)
	for _, j := range []*job.Job{
		{ID: "squ-905", Ticket: "SQU-905", Target: "worker", Pipeline: "ticket_to_pr", Status: job.StatusRunning, CreatedAt: now, UpdatedAt: now},
		{ID: "squ-906", Ticket: "SQU-906", Target: "worker", Pipeline: "ticket_to_pr", Status: job.StatusRunning, CreatedAt: now, UpdatedAt: now},
		{ID: "squ-907", Ticket: "SQU-907", Target: "worker", Status: job.StatusRunning, CreatedAt: now, UpdatedAt: now},
		{ID: "ops-905", Ticket: "OPS-905", Target: "other", Pipeline: "ops_review", Status: job.StatusRunning, CreatedAt: now, UpdatedAt: now},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}
	for _, item := range []*daemon.OutboxItem{
		{
			ID:          "outbox-job-prune",
			State:       daemon.OutboxStateProcessed,
			Type:        "agent.dispatch",
			Source:      "manager",
			Payload:     map[string]any{"job_id": "squ-905", "ticket": "SQU-905", "target": "worker"},
			CreatedAt:   old,
			UpdatedAt:   old,
			ProcessedAt: old,
		},
		{
			ID:          "outbox-pipeline-prune",
			State:       daemon.OutboxStateProcessed,
			Type:        "agent.dispatch",
			Source:      "manager",
			Payload:     map[string]any{"job_id": "squ-906", "ticket": "SQU-906", "target": "worker"},
			CreatedAt:   old,
			UpdatedAt:   old,
			ProcessedAt: old,
		},
		{
			ID:          "outbox-team-prune",
			State:       daemon.OutboxStateProcessed,
			Type:        "agent.dispatch",
			Source:      "manager",
			Payload:     map[string]any{"job_id": "squ-907", "ticket": "SQU-907", "target": "worker"},
			CreatedAt:   old,
			UpdatedAt:   old,
			ProcessedAt: old,
		},
		{
			ID:          "outbox-owned-new",
			State:       daemon.OutboxStateProcessed,
			Type:        "agent.dispatch",
			Source:      "manager",
			Payload:     map[string]any{"job_id": "squ-905", "ticket": "SQU-905", "target": "worker"},
			CreatedAt:   now.Add(-time.Hour),
			UpdatedAt:   now.Add(-time.Hour),
			ProcessedAt: now.Add(-time.Hour),
		},
		{
			ID:        "outbox-owned-failed",
			State:     daemon.OutboxStateFailed,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "squ-905", "ticket": "SQU-905", "target": "worker"},
			CreatedAt: old,
			UpdatedAt: old,
			FailedAt:  old,
		},
		{
			ID:          "outbox-unowned-prune",
			State:       daemon.OutboxStateProcessed,
			Type:        "agent.dispatch",
			Source:      "manager",
			Payload:     map[string]any{"job_id": "ops-905", "ticket": "OPS-905", "target": "other"},
			CreatedAt:   old,
			UpdatedAt:   old,
			ProcessedAt: old,
		},
	} {
		writeCLIOutboxItem(t, teamDir, item)
	}

	dryJob := runRootForOutboxTest(t, "job", "outbox", "prune", "squ-905", "--repo", root, "--older-than", "24h", "--dry-run", "--json")
	var dryJobRows []outboxPruneResult
	if err := json.Unmarshal(dryJob.Bytes(), &dryJobRows); err != nil {
		t.Fatalf("decode job prune dry-run: %v\n%s", err, dryJob.String())
	}
	if len(dryJobRows) != 1 || dryJobRows[0].ID != "outbox-job-prune" || !dryJobRows[0].DryRun || dryJobRows[0].Dropped {
		t.Fatalf("job prune dry-run rows = %+v", dryJobRows)
	}

	jobPrune := runRootForOutboxTest(t, "job", "outbox", "prune", "squ-905", "--repo", root, "--older-than", "24h", "--format", "{{.ID}} {{.Dropped}}")
	if got, want := strings.TrimSpace(jobPrune.String()), "outbox-job-prune true"; got != want {
		t.Fatalf("job prune output = %q, want %q", got, want)
	}
	pipelinePrune := runRootForOutboxTest(t, "pipeline", "outbox", "prune", "ticket_to_pr", "--repo", root, "--older-than", "24h", "--job", "SQU-906", "--json")
	var pipelineRows []outboxPruneResult
	if err := json.Unmarshal(pipelinePrune.Bytes(), &pipelineRows); err != nil {
		t.Fatalf("decode pipeline prune: %v\n%s", err, pipelinePrune.String())
	}
	if len(pipelineRows) != 1 || pipelineRows[0].ID != "outbox-pipeline-prune" || !pipelineRows[0].Dropped {
		t.Fatalf("pipeline prune rows = %+v", pipelineRows)
	}
	teamPrune := runRootForOutboxTest(t, "team", "outbox", "prune", "delivery", "--repo", root, "--older-than", "24h", "--job", "SQU-907", "--json")
	var teamRows []outboxPruneResult
	if err := json.Unmarshal(teamPrune.Bytes(), &teamRows); err != nil {
		t.Fatalf("decode team prune: %v\n%s", err, teamPrune.String())
	}
	if len(teamRows) != 1 || teamRows[0].ID != "outbox-team-prune" || !teamRows[0].Dropped {
		t.Fatalf("team prune rows = %+v", teamRows)
	}

	for _, id := range []string{"outbox-job-prune", "outbox-pipeline-prune", "outbox-team-prune"} {
		if _, err := daemon.ReadOutboxItem(teamDir, id); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s should be removed, err=%v", id, err)
		}
	}
	for _, id := range []string{"outbox-owned-new", "outbox-owned-failed", "outbox-unowned-prune"} {
		if _, err := daemon.ReadOutboxItem(teamDir, id); err != nil {
			t.Fatalf("%s should remain after scoped prune: %v", id, err)
		}
	}
}

func TestOutboxDrainDryRunOffline(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Date(2026, 6, 27, 12, 30, 0, 0, time.UTC)
	writeCLIOutboxItem(t, teamDir, &daemon.OutboxItem{
		ID:        "outbox-offline",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Payload:   map[string]any{"job_id": "squ-503", "target": "worker"},
		Source:    "manager",
		CreatedAt: now,
		UpdatedAt: now,
	})

	out := runRootForOutboxTest(t, "outbox", "drain", "--target", target, "--dry-run", "--json")
	var result daemon.OutboxDrainResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode drain dry-run: %v\n%s", err, out.String())
	}
	if !result.DryRun || result.WouldPublish != 1 || result.Pending != 1 || result.Published != 0 {
		t.Fatalf("dry-run result = %+v", result)
	}
	if _, err := daemon.ReadOutboxItem(teamDir, "outbox-offline"); err != nil {
		t.Fatalf("dry-run removed outbox item: %v", err)
	}
}

func TestOutboxDrainThroughDaemon(t *testing.T) {
	target, mgr, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Date(2026, 6, 27, 13, 0, 0, 0, time.UTC)
	writeCLIOutboxItem(t, teamDir, &daemon.OutboxItem{
		ID:        "outbox-daemon",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Payload:   map[string]any{"target": "worker", "name": "worker-squ-504", "ticket": "SQU-504", "workspace": "repo"},
		Source:    "manager",
		CreatedAt: now,
		UpdatedAt: now,
	})

	out := runRootForOutboxTest(t, "outbox", "drain", "--target", target, "--json")
	var result daemon.OutboxDrainResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode outbox drain: %v\n%s", err, out.String())
	}
	if result.Published != 1 || result.Pending != 0 || result.Processed != 1 {
		t.Fatalf("drain result = %+v", result)
	}
	processed, err := daemon.ReadOutboxItem(teamDir, "outbox-daemon")
	if err != nil {
		t.Fatalf("read processed item: %v", err)
	}
	if processed.State != daemon.OutboxStateProcessed {
		t.Fatalf("processed state = %s, want processed", processed.State)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-504")
}

func TestTeamOutboxScopesItemsAndActions(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.other]
agent = "other"

[[instances.other.triggers]]
event = "agent.dispatch"
match.target = "other"

[teams.delivery]
instances = ["manager", "worker"]

[teams.platform]
instances = ["other"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 27, 14, 0, 0, 0, time.UTC)
	for _, j := range []*job.Job{
		{
			ID:        "squ-901",
			Ticket:    "SQU-901",
			Target:    "worker",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "oth-901",
			Ticket:    "OTH-901",
			Target:    "other",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}
	for _, item := range []*daemon.OutboxItem{
		{
			ID:        "outbox-delivery-pending",
			State:     daemon.OutboxStatePending,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "squ-901", "ticket": "SQU-901", "target": "worker"},
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "outbox-delivery-failed",
			State:     daemon.OutboxStateFailed,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "squ-901", "ticket": "SQU-901", "target": "worker"},
			LastError: "route missing",
			CreatedAt: now.Add(time.Minute),
			UpdatedAt: now.Add(time.Minute),
		},
		{
			ID:        "outbox-platform-pending",
			State:     daemon.OutboxStatePending,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "oth-901", "ticket": "OTH-901", "target": "other"},
			CreatedAt: now.Add(2 * time.Minute),
			UpdatedAt: now.Add(2 * time.Minute),
		},
	} {
		writeCLIOutboxItem(t, teamDir, item)
	}

	out := runRootForOutboxTest(t, "team", "outbox", "delivery", "--repo", root, "--sort", "id", "--json")
	var listed []*daemon.OutboxItem
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil {
		t.Fatalf("decode team outbox list: %v\n%s", err, out.String())
	}
	if len(listed) != 2 || listed[0].ID != "outbox-delivery-failed" || listed[1].ID != "outbox-delivery-pending" {
		t.Fatalf("team outbox list = %+v", listed)
	}

	summaryOut := runRootForOutboxTest(t, "team", "outbox", "delivery", "--repo", root, "--summary", "--json")
	var summary outboxSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &summary); err != nil {
		t.Fatalf("decode team outbox summary: %v\n%s", err, summaryOut.String())
	}
	if summary.Total != 2 || summary.Pending != 1 || summary.Failed != 1 || summary.Filtered != 2 {
		t.Fatalf("team outbox summary = %+v", summary)
	}

	shown := runRootForOutboxTest(t, "team", "outbox", "show", "delivery", "outbox-delivery-failed", "--repo", root, "--json")
	var shownItem daemon.OutboxItem
	if err := json.Unmarshal(shown.Bytes(), &shownItem); err != nil {
		t.Fatalf("decode team outbox show: %v\n%s", err, shown.String())
	}
	if shownItem.ID != "outbox-delivery-failed" || shownItem.LastError != "route missing" {
		t.Fatalf("shown team outbox item = %+v", shownItem)
	}

	retry := runRootForOutboxTest(t, "team", "outbox", "retry", "delivery", "outbox-delivery-failed", "--repo", root, "--dry-run", "--json")
	var retryRows []outboxActionResult
	if err := json.Unmarshal(retry.Bytes(), &retryRows); err != nil {
		t.Fatalf("decode team outbox retry: %v\n%s", err, retry.String())
	}
	if len(retryRows) != 1 || retryRows[0].Action != "would_retry" || !retryRows[0].DryRun {
		t.Fatalf("retry rows = %+v", retryRows)
	}
	stillFailed, err := daemon.ReadOutboxItem(teamDir, "outbox-delivery-failed")
	if err != nil || stillFailed.State != daemon.OutboxStateFailed {
		t.Fatalf("dry-run retry changed item=%+v err=%v", stillFailed, err)
	}

	drop := runRootForOutboxTest(t, "team", "outbox", "drop", "delivery", "outbox-delivery-pending", "--repo", root, "--dry-run", "--json")
	var dropRows []outboxActionResult
	if err := json.Unmarshal(drop.Bytes(), &dropRows); err != nil {
		t.Fatalf("decode team outbox drop: %v\n%s", err, drop.String())
	}
	if len(dropRows) != 1 || dropRows[0].Action != "would_drop" || !dropRows[0].DryRun {
		t.Fatalf("drop rows = %+v", dropRows)
	}
	if _, err := daemon.ReadOutboxItem(teamDir, "outbox-delivery-pending"); err != nil {
		t.Fatalf("dry-run drop removed item: %v", err)
	}

	retryAll := runRootForOutboxTest(t, "team", "outbox", "retry", "delivery", "--repo", root, "--all", "--sort", "id", "--json")
	var retryAllRows []outboxActionResult
	if err := json.Unmarshal(retryAll.Bytes(), &retryAllRows); err != nil {
		t.Fatalf("decode team outbox retry all: %v\n%s", err, retryAll.String())
	}
	if len(retryAllRows) != 1 || retryAllRows[0].ID != "outbox-delivery-failed" || retryAllRows[0].Action != "retried" {
		t.Fatalf("team retry all rows = %+v", retryAllRows)
	}
	retriedTeamItem, err := daemon.ReadOutboxItem(teamDir, "outbox-delivery-failed")
	if err != nil || retriedTeamItem.State != daemon.OutboxStatePending {
		t.Fatalf("team retry all changed item=%+v err=%v", retriedTeamItem, err)
	}

	dropAll := runRootForOutboxTest(t, "team", "outbox", "drop", "delivery", "--repo", root, "--all", "--state", "pending", "--job", "SQU-901", "--sort", "id", "--limit", "1", "--dry-run", "--json")
	var dropAllRows []outboxActionResult
	if err := json.Unmarshal(dropAll.Bytes(), &dropAllRows); err != nil {
		t.Fatalf("decode team outbox drop all dry-run: %v\n%s", err, dropAll.String())
	}
	if len(dropAllRows) != 1 || dropAllRows[0].ID != "outbox-delivery-failed" || dropAllRows[0].Action != "would_drop" || !dropAllRows[0].DryRun {
		t.Fatalf("team drop all rows = %+v", dropAllRows)
	}

	_, stderr, err := runRootForOutboxTestErr(t, "team", "outbox", "show", "delivery", "outbox-platform-pending", "--repo", root)
	if err == nil {
		t.Fatalf("out-of-team show succeeded")
	}
	if !strings.Contains(stderr.String(), `outbox item "outbox-platform-pending" is not owned by team "delivery"`) {
		t.Fatalf("out-of-team error = %q", stderr.String())
	}
}

func TestPipelineOutboxScopesItemsAndActions(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[pipelines.ops_review]
trigger.event = "ops.created"

[[pipelines.ops_review.steps]]
id = "implement"
target = "worker"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 27, 16, 0, 0, 0, time.UTC)
	for _, j := range []*job.Job{
		{
			ID:        "squ-902",
			Ticket:    "SQU-902",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "ops-902",
			Ticket:    "OPS-902",
			Target:    "worker",
			Pipeline:  "ops_review",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}
	for _, item := range []*daemon.OutboxItem{
		{
			ID:        "outbox-ticket-pending",
			State:     daemon.OutboxStatePending,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "squ-902", "ticket": "SQU-902", "target": "worker"},
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "outbox-ticket-failed",
			State:     daemon.OutboxStateFailed,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "squ-902", "ticket": "SQU-902", "target": "worker"},
			LastError: "route missing",
			CreatedAt: now.Add(time.Minute),
			UpdatedAt: now.Add(time.Minute),
		},
		{
			ID:        "outbox-ops-pending",
			State:     daemon.OutboxStatePending,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "ops-902", "ticket": "OPS-902", "target": "worker"},
			CreatedAt: now.Add(2 * time.Minute),
			UpdatedAt: now.Add(2 * time.Minute),
		},
	} {
		writeCLIOutboxItem(t, teamDir, item)
	}

	out := runRootForOutboxTest(t, "pipeline", "outbox", "ticket_to_pr", "--repo", root, "--sort", "id", "--json")
	var listed []*daemon.OutboxItem
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil {
		t.Fatalf("decode pipeline outbox list: %v\n%s", err, out.String())
	}
	if len(listed) != 2 || listed[0].ID != "outbox-ticket-failed" || listed[1].ID != "outbox-ticket-pending" {
		t.Fatalf("pipeline outbox list = %+v", listed)
	}

	allOut := runRootForOutboxTest(t, "pipeline", "outbox", "--repo", root, "--sort", "id", "--json")
	var allListed []*daemon.OutboxItem
	if err := json.Unmarshal(allOut.Bytes(), &allListed); err != nil {
		t.Fatalf("decode all pipeline outbox list: %v\n%s", err, allOut.String())
	}
	if len(allListed) != 3 {
		t.Fatalf("all pipeline outbox list = %+v", allListed)
	}

	summaryOut := runRootForOutboxTest(t, "pipeline", "outbox", "ticket_to_pr", "--repo", root, "--summary", "--json")
	var summary outboxSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &summary); err != nil {
		t.Fatalf("decode pipeline outbox summary: %v\n%s", err, summaryOut.String())
	}
	if summary.Total != 2 || summary.Pending != 1 || summary.Failed != 1 || summary.Filtered != 2 {
		t.Fatalf("pipeline outbox summary = %+v", summary)
	}

	shown := runRootForOutboxTest(t, "pipeline", "outbox", "show", "ticket_to_pr", "outbox-ticket-failed", "--repo", root, "--json")
	var shownItem daemon.OutboxItem
	if err := json.Unmarshal(shown.Bytes(), &shownItem); err != nil {
		t.Fatalf("decode pipeline outbox show: %v\n%s", err, shown.String())
	}
	if shownItem.ID != "outbox-ticket-failed" || shownItem.LastError != "route missing" {
		t.Fatalf("shown pipeline outbox item = %+v", shownItem)
	}

	retry := runRootForOutboxTest(t, "pipeline", "outbox", "retry", "ticket_to_pr", "outbox-ticket-failed", "--repo", root, "--dry-run", "--json")
	var retryRows []outboxActionResult
	if err := json.Unmarshal(retry.Bytes(), &retryRows); err != nil {
		t.Fatalf("decode pipeline outbox retry: %v\n%s", err, retry.String())
	}
	if len(retryRows) != 1 || retryRows[0].Action != "would_retry" || !retryRows[0].DryRun {
		t.Fatalf("retry rows = %+v", retryRows)
	}
	stillFailed, err := daemon.ReadOutboxItem(teamDir, "outbox-ticket-failed")
	if err != nil || stillFailed.State != daemon.OutboxStateFailed {
		t.Fatalf("dry-run retry changed item=%+v err=%v", stillFailed, err)
	}

	drop := runRootForOutboxTest(t, "pipeline", "outbox", "drop", "ticket_to_pr", "outbox-ticket-pending", "--repo", root, "--dry-run", "--json")
	var dropRows []outboxActionResult
	if err := json.Unmarshal(drop.Bytes(), &dropRows); err != nil {
		t.Fatalf("decode pipeline outbox drop: %v\n%s", err, drop.String())
	}
	if len(dropRows) != 1 || dropRows[0].Action != "would_drop" || !dropRows[0].DryRun {
		t.Fatalf("drop rows = %+v", dropRows)
	}
	if _, err := daemon.ReadOutboxItem(teamDir, "outbox-ticket-pending"); err != nil {
		t.Fatalf("dry-run drop removed item: %v", err)
	}

	retryAll := runRootForOutboxTest(t, "pipeline", "outbox", "retry", "ticket_to_pr", "--repo", root, "--all", "--sort", "id", "--json")
	var retryAllRows []outboxActionResult
	if err := json.Unmarshal(retryAll.Bytes(), &retryAllRows); err != nil {
		t.Fatalf("decode pipeline outbox retry all: %v\n%s", err, retryAll.String())
	}
	if len(retryAllRows) != 1 || retryAllRows[0].ID != "outbox-ticket-failed" || retryAllRows[0].Action != "retried" {
		t.Fatalf("pipeline retry all rows = %+v", retryAllRows)
	}
	retriedPipelineItem, err := daemon.ReadOutboxItem(teamDir, "outbox-ticket-failed")
	if err != nil || retriedPipelineItem.State != daemon.OutboxStatePending {
		t.Fatalf("pipeline retry all changed item=%+v err=%v", retriedPipelineItem, err)
	}

	dropAll := runRootForOutboxTest(t, "pipeline", "outbox", "drop", "ticket_to_pr", "--repo", root, "--all", "--state", "pending", "--job", "SQU-902", "--sort", "id", "--limit", "1", "--dry-run", "--json")
	var dropAllRows []outboxActionResult
	if err := json.Unmarshal(dropAll.Bytes(), &dropAllRows); err != nil {
		t.Fatalf("decode pipeline outbox drop all dry-run: %v\n%s", err, dropAll.String())
	}
	if len(dropAllRows) != 1 || dropAllRows[0].ID != "outbox-ticket-failed" || dropAllRows[0].Action != "would_drop" || !dropAllRows[0].DryRun {
		t.Fatalf("pipeline drop all rows = %+v", dropAllRows)
	}

	_, stderr, err := runRootForOutboxTestErr(t, "pipeline", "outbox", "show", "ticket_to_pr", "outbox-ops-pending", "--repo", root)
	if err == nil {
		t.Fatalf("out-of-pipeline show succeeded")
	}
	if !strings.Contains(stderr.String(), `outbox item "outbox-ops-pending" is not owned by pipeline "ticket_to_pr"`) {
		t.Fatalf("out-of-pipeline error = %q", stderr.String())
	}
}

func TestJobOutboxScopesItemsAndActions(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 27, 17, 0, 0, 0, time.UTC)
	for _, j := range []*job.Job{
		{
			ID:        "squ-903",
			Ticket:    "SQU-903",
			Target:    "worker",
			Instance:  "worker-squ-903",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "ops-903",
			Ticket:    "OPS-903",
			Target:    "worker",
			Instance:  "worker-ops-903",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}
	for _, item := range []*daemon.OutboxItem{
		{
			ID:        "outbox-job-pending",
			State:     daemon.OutboxStatePending,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "squ-903", "ticket": "SQU-903", "target": "worker"},
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "outbox-job-failed",
			State:     daemon.OutboxStateFailed,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"ticket": "SQU-903", "target": "worker"},
			LastError: "route missing",
			CreatedAt: now.Add(time.Minute),
			UpdatedAt: now.Add(time.Minute),
		},
		{
			ID:        "outbox-other-pending",
			State:     daemon.OutboxStatePending,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "ops-903", "ticket": "OPS-903", "target": "worker"},
			CreatedAt: now.Add(2 * time.Minute),
			UpdatedAt: now.Add(2 * time.Minute),
		},
	} {
		writeCLIOutboxItem(t, teamDir, item)
	}

	out := runRootForOutboxTest(t, "job", "outbox", "SQU-903", "--repo", root, "--sort", "id", "--json")
	var listed []*daemon.OutboxItem
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil {
		t.Fatalf("decode job outbox list: %v\n%s", err, out.String())
	}
	if len(listed) != 2 || listed[0].ID != "outbox-job-failed" || listed[1].ID != "outbox-job-pending" {
		t.Fatalf("job outbox list = %+v", listed)
	}

	summaryOut := runRootForOutboxTest(t, "job", "outbox", "squ-903", "--repo", root, "--summary", "--json")
	var summary outboxSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &summary); err != nil {
		t.Fatalf("decode job outbox summary: %v\n%s", err, summaryOut.String())
	}
	if summary.Total != 2 || summary.Pending != 1 || summary.Failed != 1 || summary.Filtered != 2 {
		t.Fatalf("job outbox summary = %+v", summary)
	}

	shown := runRootForOutboxTest(t, "job", "outbox", "show", "squ-903", "outbox-job-failed", "--repo", root, "--json")
	var shownItem daemon.OutboxItem
	if err := json.Unmarshal(shown.Bytes(), &shownItem); err != nil {
		t.Fatalf("decode job outbox show: %v\n%s", err, shown.String())
	}
	if shownItem.ID != "outbox-job-failed" || shownItem.LastError != "route missing" {
		t.Fatalf("shown job outbox item = %+v", shownItem)
	}

	retry := runRootForOutboxTest(t, "job", "outbox", "retry", "squ-903", "outbox-job-failed", "--repo", root, "--dry-run", "--json")
	var retryRows []outboxActionResult
	if err := json.Unmarshal(retry.Bytes(), &retryRows); err != nil {
		t.Fatalf("decode job outbox retry: %v\n%s", err, retry.String())
	}
	if len(retryRows) != 1 || retryRows[0].Action != "would_retry" || !retryRows[0].DryRun {
		t.Fatalf("retry rows = %+v", retryRows)
	}
	stillFailed, err := daemon.ReadOutboxItem(teamDir, "outbox-job-failed")
	if err != nil || stillFailed.State != daemon.OutboxStateFailed {
		t.Fatalf("dry-run retry changed item=%+v err=%v", stillFailed, err)
	}

	drop := runRootForOutboxTest(t, "job", "outbox", "drop", "squ-903", "outbox-job-pending", "--repo", root, "--dry-run", "--json")
	var dropRows []outboxActionResult
	if err := json.Unmarshal(drop.Bytes(), &dropRows); err != nil {
		t.Fatalf("decode job outbox drop: %v\n%s", err, drop.String())
	}
	if len(dropRows) != 1 || dropRows[0].Action != "would_drop" || !dropRows[0].DryRun {
		t.Fatalf("drop rows = %+v", dropRows)
	}
	if _, err := daemon.ReadOutboxItem(teamDir, "outbox-job-pending"); err != nil {
		t.Fatalf("dry-run drop removed item: %v", err)
	}

	_, stderr, err := runRootForOutboxTestErr(t, "job", "outbox", "show", "squ-903", "outbox-other-pending", "--repo", root)
	if err == nil {
		t.Fatalf("out-of-job show succeeded")
	}
	if !strings.Contains(stderr.String(), `outbox item "outbox-other-pending" is not owned by job "squ-903"`) {
		t.Fatalf("out-of-job error = %q", stderr.String())
	}
}

func TestJobOutboxRetryDropAllScopesAndFilters(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 27, 18, 30, 0, 0, time.UTC)
	for _, j := range []*job.Job{
		{
			ID:        "squ-904",
			Ticket:    "SQU-904",
			Target:    "worker",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "ops-904",
			Ticket:    "OPS-904",
			Target:    "worker",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}
	for _, item := range []*daemon.OutboxItem{
		{
			ID:        "outbox-batch-failed-a",
			State:     daemon.OutboxStateFailed,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "squ-904", "target": "worker"},
			LastError: "route missing",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "outbox-batch-failed-b",
			State:     daemon.OutboxStateFailed,
			Type:      "agent.dispatch",
			Source:    "worker",
			Payload:   map[string]any{"ticket": "SQU-904", "target": "worker"},
			LastError: "route missing",
			CreatedAt: now.Add(time.Minute),
			UpdatedAt: now.Add(time.Minute),
		},
		{
			ID:        "outbox-batch-pending",
			State:     daemon.OutboxStatePending,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "squ-904", "target": "worker"},
			CreatedAt: now.Add(2 * time.Minute),
			UpdatedAt: now.Add(2 * time.Minute),
		},
		{
			ID:        "outbox-batch-other",
			State:     daemon.OutboxStateFailed,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "ops-904", "target": "worker"},
			LastError: "route missing",
			CreatedAt: now.Add(3 * time.Minute),
			UpdatedAt: now.Add(3 * time.Minute),
		},
	} {
		writeCLIOutboxItem(t, teamDir, item)
	}

	dryRetry := runRootForOutboxTest(t, "job", "outbox", "retry", "squ-904", "--repo", root, "--all", "--source", "manager", "--sort", "id", "--limit", "1", "--dry-run", "--json")
	var dryRetryRows []outboxActionResult
	if err := json.Unmarshal(dryRetry.Bytes(), &dryRetryRows); err != nil {
		t.Fatalf("decode job outbox retry all dry-run: %v\n%s", err, dryRetry.String())
	}
	if len(dryRetryRows) != 1 || dryRetryRows[0].ID != "outbox-batch-failed-a" || dryRetryRows[0].Action != "would_retry" || !dryRetryRows[0].DryRun {
		t.Fatalf("dry retry rows = %+v", dryRetryRows)
	}
	stillFailed, err := daemon.ReadOutboxItem(teamDir, "outbox-batch-failed-a")
	if err != nil || stillFailed.State != daemon.OutboxStateFailed {
		t.Fatalf("dry-run retry changed item=%+v err=%v", stillFailed, err)
	}

	retry := runRootForOutboxTest(t, "job", "outbox", "retry", "squ-904", "--repo", root, "--all", "--sort", "id", "--json")
	var retryRows []outboxActionResult
	if err := json.Unmarshal(retry.Bytes(), &retryRows); err != nil {
		t.Fatalf("decode job outbox retry all: %v\n%s", err, retry.String())
	}
	if len(retryRows) != 2 || retryRows[0].ID != "outbox-batch-failed-a" || retryRows[1].ID != "outbox-batch-failed-b" {
		t.Fatalf("retry rows = %+v", retryRows)
	}
	for _, id := range []string{"outbox-batch-failed-a", "outbox-batch-failed-b"} {
		item, err := daemon.ReadOutboxItem(teamDir, id)
		if err != nil || item.State != daemon.OutboxStatePending {
			t.Fatalf("retried %s item=%+v err=%v", id, item, err)
		}
	}
	other, err := daemon.ReadOutboxItem(teamDir, "outbox-batch-other")
	if err != nil || other.State != daemon.OutboxStateFailed {
		t.Fatalf("other job item changed=%+v err=%v", other, err)
	}

	drop := runRootForOutboxTest(t, "job", "outbox", "drop", "squ-904", "--repo", root, "--all", "--state", "pending", "--sort", "id", "--limit", "2", "--json")
	var dropRows []outboxActionResult
	if err := json.Unmarshal(drop.Bytes(), &dropRows); err != nil {
		t.Fatalf("decode job outbox drop all: %v\n%s", err, drop.String())
	}
	if len(dropRows) != 2 || dropRows[0].ID != "outbox-batch-failed-a" || dropRows[1].ID != "outbox-batch-failed-b" {
		t.Fatalf("drop rows = %+v", dropRows)
	}
	for _, id := range []string{"outbox-batch-failed-a", "outbox-batch-failed-b"} {
		if _, err := daemon.ReadOutboxItem(teamDir, id); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s should be removed, err=%v", id, err)
		}
	}
	if _, err := daemon.ReadOutboxItem(teamDir, "outbox-batch-pending"); err != nil {
		t.Fatalf("limit should leave pending item: %v", err)
	}

	_, stderr, err := runRootForOutboxTestErr(t, "job", "outbox", "retry", "squ-904", "outbox-batch-pending", "--repo", root, "--state", "failed")
	if err == nil {
		t.Fatalf("retry with filter but no --all succeeded")
	}
	if !strings.Contains(stderr.String(), "--state, --type, --source, --sort, and --limit require --all") {
		t.Fatalf("retry filter error = %q", stderr.String())
	}
}

func writeCLIOutboxItem(t *testing.T, teamDir string, item *daemon.OutboxItem) {
	t.Helper()
	if err := daemon.WriteOutboxItem(teamDir, item); err != nil {
		t.Fatalf("WriteOutboxItem(%s): %v", item.ID, err)
	}
}

func writeRawOutboxFile(t *testing.T, teamDir, state, name, body string) {
	t.Helper()
	dir := filepath.Join(daemon.OutboxRoot(teamDir), state)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeQuarantinedOutboxFile(t *testing.T, teamDir, stamp, state string, item *daemon.OutboxItem) {
	t.Helper()
	if item == nil {
		t.Fatal("nil outbox item")
	}
	body, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeRawOutboxFile(t, teamDir, filepath.Join(outboxQuarantineDir, stamp, state), item.ID+".json", string(append(body, '\n')))
}

func outboxQuarantinePaths(items []outboxQuarantineItem) string {
	paths := make([]string, 0, len(items))
	for _, item := range items {
		paths = append(paths, item.Path)
	}
	sort.Strings(paths)
	return strings.Join(paths, ",")
}

func outboxDoctorHasCode(findings []outboxDoctorFinding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func runRootForOutboxTest(t *testing.T, args ...string) *bytes.Buffer {
	t.Helper()
	out, stderr, err := runRootForOutboxTestErr(t, args...)
	if err != nil {
		t.Fatalf("agent-team %s: %v\nstderr=%s\nstdout=%s", strings.Join(args, " "), err, stderr.String(), out.String())
	}
	return out
}

func runRootForOutboxTestErr(t *testing.T, args ...string) (*bytes.Buffer, *bytes.Buffer, error) {
	t.Helper()
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out, stderr, err
}
