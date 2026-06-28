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
	"github.com/jamesaud/agent-team/internal/job"
)

func TestJobSnapshotCapturesPostMortemRuntimeState(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	j := &job.Job{
		ID:        "squ-160",
		Ticket:    "SQU-160",
		Target:    "worker",
		Instance:  "worker-squ-160",
		Status:    job.StatusDone,
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now,
		LastEvent: "instance_exited",
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	if err := job.AppendEvent(teamDir, &job.Event{
		TS:       now.Add(-time.Hour),
		JobID:    j.ID,
		Type:     "created",
		Status:   job.StatusRunning,
		Instance: j.Instance,
		Message:  "created",
		Actor:    "test",
	}); err != nil {
		t.Fatalf("append created event: %v", err)
	}
	if err := job.AppendEvent(teamDir, &job.Event{
		TS:       now,
		JobID:    j.ID,
		Type:     "instance_exited",
		Status:   job.StatusDone,
		Instance: j.Instance,
		Message:  "done",
		Actor:    "daemon",
		Data:     map[string]string{"api_key": "timeline-secret", "instance": j.Instance},
	}); err != nil {
		t.Fatalf("append exit event: %v", err)
	}

	stateDir := filepath.Join(teamDir, "state", j.Instance)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}
	writeStatus(t, stateDir, `[status]
phase = "done"
description = "complete"
`, now)
	writeLastMessageForTest(t, teamDir, j.Instance, "clean final")

	root := daemon.DaemonRoot(teamDir)
	exitCode := 0
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance:      j.Instance,
		Agent:         "worker",
		Job:           j.ID,
		Ticket:        j.Ticket,
		Runtime:       "codex",
		RuntimeBinary: "codex-dev",
		Workspace:     tmp,
		Status:        daemon.StatusExited,
		StartedAt:     now.Add(-30 * time.Minute),
		ExitedAt:      now,
		ExitCode:      &exitCode,
		LogPath:       filepath.Join(root, j.Instance, "child.log"),
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	writeChildLogForTest(t, root, j.Instance, "first\nsecond\nthird\n")
	if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{
		ID:       "dispatch-160",
		TS:       now.Add(-30 * time.Minute),
		Action:   "dispatch",
		Instance: j.Instance,
		Agent:    "worker",
		Job:      j.ID,
		Ticket:   j.Ticket,
		Status:   daemon.StatusRunning,
	}); err != nil {
		t.Fatalf("append dispatch lifecycle: %v", err)
	}
	if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{
		ID:       "exit-160",
		TS:       now,
		Action:   "exit",
		Instance: j.Instance,
		Agent:    "worker",
		Job:      j.ID,
		Ticket:   j.Ticket,
		Status:   daemon.StatusExited,
		ExitCode: &exitCode,
	}); err != nil {
		t.Fatalf("append exit lifecycle: %v", err)
	}
	if err := daemon.WriteQueueItem(root, &daemon.QueueItem{
		ID:         "q-160",
		State:      daemon.QueueStateDead,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: j.Instance,
		Payload:    map[string]any{"job_id": j.ID, "target": "worker", "api_key": "secret-key"},
		QueuedAt:   now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("write queue: %v", err)
	}
	writeCLIOutboxItem(t, teamDir, &daemon.OutboxItem{
		ID:        "outbox-160",
		State:     daemon.OutboxStateFailed,
		Type:      "agent.dispatch",
		Source:    "manager",
		Payload:   map[string]any{"job_id": j.ID, "target": "worker", "api_key": "secret-outbox"},
		LastError: "socket unavailable",
		CreatedAt: now,
		UpdatedAt: now,
	})
	writeCLIOutboxItem(t, teamDir, &daemon.OutboxItem{
		ID:        "outbox-other-160",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Source:    "manager",
		Payload:   map[string]any{"job_id": "squ-other", "target": "worker", "api_key": "other-secret"},
		CreatedAt: now,
		UpdatedAt: now,
	})
	writeQuarantinedOutboxFile(t, teamDir, "20260623T120000.000000000Z", daemon.OutboxStateFailed, &daemon.OutboxItem{
		ID:        "outbox-quarantined-160",
		State:     daemon.OutboxStateFailed,
		Type:      "agent.dispatch",
		Source:    "manager",
		Payload:   map[string]any{"job_id": j.ID, "target": "worker", "api_key": "quarantined-secret"},
		LastError: "invalid state file",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err := daemon.AppendMessage(root, j.Instance, &daemon.Message{
		ID:   "msg-160",
		From: "manager",
		Body: "please confirm the final state",
		TS:   now.Add(-5 * time.Minute),
	}); err != nil {
		t.Fatalf("append inbox message: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "snapshot", "SQU-160", "--repo", tmp, "--events", "-1", "--events-sort", "newest", "--tail", "2", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job snapshot: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot jobSnapshotResult
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v\nbody=%s", err, out.String())
	}
	if snapshot.Job == nil || snapshot.Job.ID != j.ID || snapshot.Instance != j.Instance {
		t.Fatalf("job snapshot identity = %+v", snapshot)
	}
	if snapshot.Provenance == nil || snapshot.Provenance.Command != "agent-team job snapshot" || snapshot.Provenance.Scope != "job" || snapshot.Provenance.Subject != "squ-160" || snapshot.Provenance.Options.Events == nil || *snapshot.Provenance.Options.Events != -1 || snapshot.Provenance.Options.EventSort != "newest" || snapshot.Provenance.Options.Tail == nil || *snapshot.Provenance.Options.Tail != 2 || !snapshot.Provenance.Options.Redacted {
		t.Fatalf("job snapshot provenance = %+v", snapshot.Provenance)
	}
	if snapshot.Runtime == nil || snapshot.Runtime.Lifecycle != "exited" || snapshot.Runtime.Runtime != "codex" || snapshot.Runtime.ExitCode == nil || *snapshot.Runtime.ExitCode != 0 {
		t.Fatalf("runtime = %+v", snapshot.Runtime)
	}
	if snapshot.State == nil || !snapshot.State.Exists || snapshot.Status == nil || snapshot.Status.Phase != "done" {
		t.Fatalf("state/status = %+v / %+v", snapshot.State, snapshot.Status)
	}
	if snapshot.Log == nil || !snapshot.Log.Exists || snapshot.Log.Tail != "second\nthird\n" {
		t.Fatalf("log = %+v", snapshot.Log)
	}
	if snapshot.LastMessage == nil || !snapshot.LastMessage.Exists || snapshot.LastMessage.Tail != "clean final" {
		t.Fatalf("last message = %+v", snapshot.LastMessage)
	}
	if len(snapshot.JobEvents) != 2 || len(snapshot.LifecycleEvents) != 2 {
		t.Fatalf("events: job=%d lifecycle=%d", len(snapshot.JobEvents), len(snapshot.LifecycleEvents))
	}
	if snapshot.JobEvents[0].Type != "instance_exited" || snapshot.JobEvents[1].Type != "created" {
		t.Fatalf("job events order = %+v", snapshot.JobEvents)
	}
	if snapshot.LifecycleEvents[0].Action != "exit" || snapshot.LifecycleEvents[1].Action != "dispatch" {
		t.Fatalf("lifecycle events order = %+v", snapshot.LifecycleEvents)
	}
	if len(snapshot.Timeline) != 4 {
		t.Fatalf("timeline = %+v", snapshot.Timeline)
	}
	jobRows, lifecycleRows := countJobTimelineSources(snapshot.Timeline)
	if jobRows != 2 || lifecycleRows != 2 {
		t.Fatalf("timeline source counts: job=%d lifecycle=%d entries=%+v", jobRows, lifecycleRows, snapshot.Timeline)
	}
	for _, entry := range snapshot.Timeline {
		if entry.JobID != j.ID {
			t.Fatalf("timeline entry not scoped to job: %+v", entry)
		}
		if entry.Source == "job" && entry.Data["api_key"] != "" && entry.Data["api_key"] != snapshotRedactedValue {
			t.Fatalf("timeline data not redacted: %+v", entry)
		}
	}
	if len(snapshot.Queue) != 1 || snapshot.Queue[0].Payload["api_key"] != snapshotRedactedValue {
		t.Fatalf("queue not redacted: %+v", snapshot.Queue)
	}
	if len(snapshot.Outbox) != 1 || snapshot.Outbox[0].ID != "outbox-160" || snapshot.Outbox[0].Payload["api_key"] != snapshotRedactedValue {
		t.Fatalf("outbox not scoped/redacted: %+v", snapshot.Outbox)
	}
	if snapshot.OutboxSummary == nil || snapshot.OutboxSummary.Total != 1 || snapshot.OutboxSummary.Failed != 1 {
		t.Fatalf("outbox summary = %+v", snapshot.OutboxSummary)
	}
	if len(snapshot.OutboxQuarantine) != 1 || snapshot.OutboxQuarantine[0].ID != "outbox-quarantined-160" || snapshot.OutboxQuarantine[0].Job != j.ID || snapshot.OutboxQuarantineSummary == nil || snapshot.OutboxQuarantineSummary.Quarantined != 1 || snapshot.OutboxQuarantineSummary.Restorable != 1 {
		t.Fatalf("outbox quarantine = %+v summary=%+v", snapshot.OutboxQuarantine, snapshot.OutboxQuarantineSummary)
	}
	if snapshot.InboxSummary == nil || snapshot.InboxSummary.Total != 1 || snapshot.InboxSummary.Unread != 1 || snapshot.InboxSummary.UnreadInstances != 1 {
		t.Fatalf("inbox summary = %+v", snapshot.InboxSummary)
	}
	if len(snapshot.Inbox) != 1 || snapshot.Inbox[0].Instance != j.Instance || snapshot.Inbox[0].LatestID != "msg-160" || snapshot.Inbox[0].LatestBody != snapshotRedactedValue {
		t.Fatalf("inbox rows = %+v", snapshot.Inbox)
	}
	for _, want := range []string{
		"agent-team inspect worker-squ-160",
		"agent-team inbox show worker-squ-160 --unread",
		"agent-team job logs squ-160 --tail 100",
		"agent-team job logs squ-160 --last-message",
		"agent-team job queue squ-160 --summary",
		"agent-team job outbox squ-160 --summary",
		"agent-team job outbox quarantine squ-160",
	} {
		if !containsString(snapshot.Actions, want) {
			t.Fatalf("actions missing %q: %+v", want, snapshot.Actions)
		}
	}

	raw := NewRootCmd()
	rawOut, rawErr := &bytes.Buffer{}, &bytes.Buffer{}
	raw.SetOut(rawOut)
	raw.SetErr(rawErr)
	raw.SetArgs([]string{"job", "snapshot", "SQU-160", "--repo", tmp, "--events", "0", "--no-redact", "--json"})
	if err := raw.Execute(); err != nil {
		t.Fatalf("job snapshot no-redact: %v\nstderr=%s", err, rawErr.String())
	}
	var rawSnapshot jobSnapshotResult
	if err := json.Unmarshal(rawOut.Bytes(), &rawSnapshot); err != nil {
		t.Fatalf("decode raw snapshot: %v\nbody=%s", err, rawOut.String())
	}
	if len(rawSnapshot.Outbox) != 1 || rawSnapshot.Outbox[0].Payload["api_key"] != "secret-outbox" {
		t.Fatalf("raw outbox rows = %+v", rawSnapshot.Outbox)
	}
	if len(rawSnapshot.OutboxQuarantine) != 1 || rawSnapshot.OutboxQuarantine[0].ID != "outbox-quarantined-160" {
		t.Fatalf("raw outbox quarantine rows = %+v", rawSnapshot.OutboxQuarantine)
	}
	if rawSnapshot.Timeline != nil {
		t.Fatalf("raw snapshot with --events 0 included timeline: %+v", rawSnapshot.Timeline)
	}
}

func TestJobSnapshotHumanSummaryAndOutputFile(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-161",
		Ticket:    "SQU-161",
		Target:    "worker",
		Status:    job.StatusFailed,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	writeCLIOutboxItem(t, teamDir, &daemon.OutboxItem{
		ID:        "outbox-161",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Source:    "manager",
		Payload:   map[string]any{"job_id": j.ID, "target": "worker"},
		CreatedAt: now,
		UpdatedAt: now,
	})

	summary := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(out)
	summary.SetErr(stderr)
	summary.SetArgs([]string{"job", "snapshot", "squ-161", "--repo", tmp})
	if err := summary.Execute(); err != nil {
		t.Fatalf("job snapshot summary: %v\nstderr=%s", err, stderr.String())
	}
	for _, want := range []string{"job snapshot:", "command: agent-team job snapshot scope=job subject=squ-161", "job: squ-161", "events: job=0 lifecycle=0", "timeline: events=0 job=0 lifecycle=0", "outbox: total=1 pending=1 failed=0 processed=0", "actions:"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("summary missing %q:\n%s", want, out.String())
		}
	}

	outputPath := filepath.Join(tmp, "snapshots", "job.json")
	fileCmd := NewRootCmd()
	fileOut, fileErr := &bytes.Buffer{}, &bytes.Buffer{}
	fileCmd.SetOut(fileOut)
	fileCmd.SetErr(fileErr)
	fileCmd.SetArgs([]string{"job", "snapshot", "squ-161", "--repo", tmp, "--output", outputPath})
	if err := fileCmd.Execute(); err != nil {
		t.Fatalf("job snapshot output: %v\nstderr=%s", err, fileErr.String())
	}
	if !strings.Contains(fileOut.String(), "Wrote job snapshot to ") {
		t.Fatalf("output message = %q", fileOut.String())
	}
	body, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var snapshot jobSnapshotResult
	if err := json.Unmarshal(body, &snapshot); err != nil {
		t.Fatalf("decode output: %v\nbody=%s", err, string(body))
	}
	if snapshot.Job == nil || snapshot.Job.ID != "squ-161" {
		t.Fatalf("output snapshot = %+v", snapshot)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"job", "snapshot", "squ-161", "--repo", tmp, "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("job snapshot --commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommands := strings.Join(scopedOperatorActions([]string{
		"agent-team job outbox squ-161 --summary",
		"agent-team job show squ-161 --events all",
		"agent-team job timeline squ-161 --tail 50 --sort newest",
	}, operatorCommandScope{Repo: tmp, Set: true}), "\n") + "\n"
	if got := commandsOut.String(); got != wantCommands {
		t.Fatalf("job snapshot --commands = %q, want %q", got, wantCommands)
	}

	commandsConflict := NewRootCmd()
	commandsConflictOut, commandsConflictErr := &bytes.Buffer{}, &bytes.Buffer{}
	commandsConflict.SetOut(commandsConflictOut)
	commandsConflict.SetErr(commandsConflictErr)
	commandsConflict.SetArgs([]string{"job", "snapshot", "squ-161", "--repo", tmp, "--commands", "--json"})
	if err := commandsConflict.Execute(); err == nil {
		t.Fatalf("job snapshot --commands --json succeeded")
	}
	if !strings.Contains(commandsConflictErr.String(), "--commands cannot be combined with --json, --output, or --format") {
		t.Fatalf("commands conflict stderr = %q", commandsConflictErr.String())
	}

	invalid := NewRootCmd()
	invalidOut, invalidErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalid.SetOut(invalidOut)
	invalid.SetErr(invalidErr)
	invalid.SetArgs([]string{"job", "snapshot", "squ-161", "--repo", tmp, "--events-sort", "sideways"})
	if err := invalid.Execute(); err == nil {
		t.Fatalf("job snapshot invalid events sort succeeded")
	}
	if !strings.Contains(invalidErr.String(), "--events-sort must be oldest or newest") {
		t.Fatalf("invalid events sort stderr = %q", invalidErr.String())
	}
}

func TestJobSnapshotIncludesPipelineStepInbox(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Date(2026, 6, 24, 9, 0, 0, 0, time.UTC)
	j := &job.Job{
		ID:        "squ-162",
		Ticket:    "SQU-162",
		Target:    "worker",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{{
			ID:       "implement",
			Target:   "worker",
			Instance: "worker-squ-162-implement",
			Status:   job.StatusBlocked,
		}},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.AppendMessage(root, "worker-squ-162-implement", &daemon.Message{
		ID:   "msg-step-162",
		From: "manager",
		Body: "operator answer for blocked worker",
		TS:   now,
	}); err != nil {
		t.Fatalf("append step inbox message: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "snapshot", "squ-162", "--repo", tmp, "--events", "0", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job snapshot step inbox: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot jobSnapshotResult
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v\nbody=%s", err, out.String())
	}
	if snapshot.InboxSummary == nil || snapshot.InboxSummary.Instances != 1 || snapshot.InboxSummary.Total != 1 || snapshot.InboxSummary.Unread != 1 {
		t.Fatalf("inbox summary = %+v", snapshot.InboxSummary)
	}
	if len(snapshot.Inbox) != 1 || snapshot.Inbox[0].Instance != "worker-squ-162-implement" || snapshot.Inbox[0].LatestBody != snapshotRedactedValue {
		t.Fatalf("inbox rows = %+v", snapshot.Inbox)
	}
	if !containsString(snapshot.Actions, "agent-team inbox show worker-squ-162-implement --unread") {
		t.Fatalf("actions missing step inbox hint: %+v", snapshot.Actions)
	}

	raw := NewRootCmd()
	rawOut, rawErr := &bytes.Buffer{}, &bytes.Buffer{}
	raw.SetOut(rawOut)
	raw.SetErr(rawErr)
	raw.SetArgs([]string{"job", "snapshot", "squ-162", "--repo", tmp, "--events", "0", "--no-redact", "--json"})
	if err := raw.Execute(); err != nil {
		t.Fatalf("job snapshot step inbox no-redact: %v\nstderr=%s", err, rawErr.String())
	}
	var rawSnapshot jobSnapshotResult
	if err := json.Unmarshal(rawOut.Bytes(), &rawSnapshot); err != nil {
		t.Fatalf("decode raw snapshot: %v\nbody=%s", err, rawOut.String())
	}
	if len(rawSnapshot.Inbox) != 1 || rawSnapshot.Inbox[0].LatestBody != "operator answer for blocked worker" {
		t.Fatalf("raw inbox rows = %+v", rawSnapshot.Inbox)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"job", "snapshot", "squ-162", "--repo", tmp, "--events", "0"})
	if err := text.Execute(); err != nil {
		t.Fatalf("job snapshot step inbox text: %v\nstderr=%s", err, textErr.String())
	}
	if !strings.Contains(textOut.String(), "inbox: instances=1 total=1 unread=1 unread_instances=1") {
		t.Fatalf("text summary missing inbox:\n%s", textOut.String())
	}
	if strings.Contains(textOut.String(), "operator answer for blocked worker") {
		t.Fatalf("text summary leaked inbox body:\n%s", textOut.String())
	}
}
