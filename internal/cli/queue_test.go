package cli

import (
	"bytes"
	"context"
	"encoding/json"
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
		State:      daemon.QueueStatePending,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-90",
		Payload:    map[string]any{"target": "worker", "ticket": "SQU-90"},
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
	for _, want := range []string{"q-local", "pending", "worker-squ-90"} {
		if !strings.Contains(lsOut.String(), want) {
			t.Fatalf("queue ls missing %q:\n%s", want, lsOut.String())
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
	if summary.Total != 3 || summary.Pending != 1 || summary.Dead != 2 || summary.Attempts != daemon.MaxQueueAttempts*2 {
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
