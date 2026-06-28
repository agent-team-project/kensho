package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
)

func TestSnapshotCommandJSONCollectsRepoState(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()

	j, err := job.New("SQU-501", "worker", "capture diagnostics", now)
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	j.Status = job.StatusRunning
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	writeStatus(t, filepath.Join(teamDir, "state", "worker-squ-501"), `[status]
phase = "blocked"
description = "waiting on queue failure"
since = "2026-06-18T12:00:00Z"

[work]
job = "squ-501"
ticket = "SQU-501"
branch = "worker-squ-501"
`, now)
	queue := &daemon.QueueItem{
		ID:         "q-snapshot",
		State:      daemon.QueueStateDead,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-501",
		Payload: map[string]any{
			"target":       "worker",
			"ticket":       "SQU-501",
			"access_token": "secret-token",
			"headers": map[string]any{
				"authorization": "Bearer secret",
				"safe":          "visible",
			},
		},
		Attempts:       daemon.MaxQueueAttempts,
		LastError:      "spawn failed",
		QueuedAt:       now.Add(-time.Hour),
		UpdatedAt:      now.Add(-30 * time.Minute),
		DeadLetteredAt: now.Add(-30 * time.Minute),
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), queue); err != nil {
		t.Fatalf("write queue: %v", err)
	}
	if err := daemon.WriteOutboxItem(teamDir, &daemon.OutboxItem{
		ID:     "outbox-snapshot",
		State:  daemon.OutboxStatePending,
		Type:   "agent.dispatch",
		Source: "manager",
		Payload: map[string]any{
			"job_id":       "squ-501",
			"target":       "worker",
			"access_token": "outbox-secret",
			"headers": map[string]any{
				"authorization": "Bearer outbox-secret",
				"safe":          "visible",
			},
		},
		CreatedAt: now.Add(-50 * time.Minute),
		UpdatedAt: now.Add(-49 * time.Minute),
	}); err != nil {
		t.Fatalf("write outbox: %v", err)
	}
	writeQuarantinedQueueItem(t, teamDir, "20260619T000000.000000000Z", daemon.QueueStatePending, &daemon.QueueItem{
		ID:         "q-snapshot-quarantined",
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-501",
		Payload:    map[string]any{"job_id": "squ-501", "target": "worker", "ticket": "SQU-501"},
		QueuedAt:   now.Add(-45 * time.Minute),
		UpdatedAt:  now.Add(-40 * time.Minute),
	})
	writeQuarantinedOutboxFile(t, teamDir, "20260619T010000.000000000Z", daemon.OutboxStatePending, &daemon.OutboxItem{
		ID:     "outbox-snapshot-quarantined",
		State:  daemon.OutboxStatePending,
		Type:   "agent.dispatch",
		Source: "manager",
		Payload: map[string]any{
			"job_id": "squ-501",
			"target": "worker",
			"ticket": "SQU-501",
		},
		CreatedAt: now.Add(-44 * time.Minute),
		UpdatedAt: now.Add(-39 * time.Minute),
	})
	writeQuarantinedJobFile(t, teamDir, "20260619T020000.000000000Z", "squ-502.toml", []byte(`id = "squ-502"
ticket = "SQU-502"
target = "worker"
status = "queued"
created_at = 2026-06-19T02:00:00Z
updated_at = 2026-06-19T02:00:00Z
`))
	if err := appendIntakeDelivery(teamDir, intakeDelivery{
		ID:         "intake-snapshot",
		Time:       now.Add(-20 * time.Minute),
		Provider:   "linear",
		Status:     intakeDeliveryStatusError,
		HTTPStatus: 503,
		EventType:  "ticket.created",
		Payload: map[string]any{
			"source":       "linear",
			"ticket":       "SQU-501",
			"access_token": "intake-secret",
			"headers": map[string]any{
				"authorization": "Bearer intake-secret",
				"safe":          "visible",
			},
		},
		Ticket: "SQU-501",
		Error:  "daemon is not running",
	}); err != nil {
		t.Fatalf("append intake delivery: %v", err)
	}
	if err := daemon.AppendMessage(daemon.DaemonRoot(teamDir), "manager", &daemon.Message{
		ID:   "msg-snapshot",
		From: "tester",
		Body: "snapshot inbox secret",
		TS:   now.Add(-10 * time.Minute),
	}); err != nil {
		t.Fatalf("append inbox message: %v", err)
	}
	if err := daemon.AppendLifecycleEvent(daemon.DaemonRoot(teamDir), &daemon.LifecycleEvent{
		TS:       now,
		Action:   "dispatch",
		Instance: "worker-squ-501",
		Agent:    "worker",
		Status:   daemon.StatusRunning,
		Message:  "started worker",
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"snapshot", "--target", tmp, "--events", "5", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("snapshot json: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot snapshotResult
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v\nbody=%s", err, out.String())
	}
	if snapshot.Version == "" || snapshot.CapturedAt == "" || snapshot.Repo == "" || snapshot.TeamDir == "" {
		t.Fatalf("snapshot metadata missing: %+v", snapshot)
	}
	if snapshot.Provenance == nil || snapshot.Provenance.Command != "agent-team snapshot" || snapshot.Provenance.Scope != "global" || snapshot.Provenance.Subject != "" || snapshot.Provenance.Options.Events == nil || *snapshot.Provenance.Options.Events != 5 || snapshot.Provenance.Options.IntakeDeliveries == nil || *snapshot.Provenance.Options.IntakeDeliveries != 50 || !snapshot.Provenance.Options.Redacted {
		t.Fatalf("snapshot provenance = %+v", snapshot.Provenance)
	}
	if snapshot.Health == nil || snapshot.Health.Queue.Dead != 1 || snapshot.Health.JobQuarantine.Quarantined != 1 {
		t.Fatalf("health = %+v", snapshot.Health)
	}
	if snapshot.Overview == nil || snapshot.Next == nil || len(snapshot.Next.ActionDetails) == 0 {
		t.Fatalf("overview/next missing: overview=%+v next=%+v", snapshot.Overview, snapshot.Next)
	}
	if detail, ok := findOperatorActionHint(snapshot.Next.ActionDetails, "agent-team repair --dry-run --jobs"); !ok || detail.Source != "health" || detail.Reason != "unhealthy" {
		t.Fatalf("snapshot next repair detail = %+v, ok=%v", detail, ok)
	}
	if snapshot.Plan == nil || snapshot.Plan.Summary.Total == 0 {
		t.Fatalf("plan = %+v", snapshot.Plan)
	}
	if len(snapshot.Jobs) != 1 || snapshot.Jobs[0].ID != "squ-501" || snapshot.Jobs[0].Status != job.StatusRunning {
		t.Fatalf("jobs = %+v", snapshot.Jobs)
	}
	if snapshot.JobTriage == nil || snapshot.JobTriage.Summary.Total != 1 || len(snapshot.JobTriage.Attention) == 0 {
		t.Fatalf("job triage = %+v", snapshot.JobTriage)
	}
	if len(snapshot.JobStatus) != 1 || snapshot.JobStatus[0].JobID != "squ-501" || snapshot.JobStatus[0].After != job.StatusBlocked || !snapshot.JobStatus[0].Changed || !snapshot.JobStatus[0].DryRun {
		t.Fatalf("job status preview = %+v", snapshot.JobStatus)
	}
	if !snapshot.Redacted {
		t.Fatalf("snapshot should redact by default: %+v", snapshot)
	}
	if len(snapshot.Queue) != 1 || snapshot.Queue[0].ID != "q-snapshot" || snapshot.QueueSummary == nil || snapshot.QueueSummary.Dead != 1 || snapshot.QueueSummary.Quarantined != 1 || snapshot.QueueSummary.QuarantineRestorable != 1 || snapshot.QueueSummary.QuarantineUnrestorable != 0 {
		t.Fatalf("queue = %+v summary=%+v", snapshot.Queue, snapshot.QueueSummary)
	}
	if len(snapshot.Outbox) != 1 || snapshot.Outbox[0].ID != "outbox-snapshot" || snapshot.OutboxSummary == nil || snapshot.OutboxSummary.Total != 1 || snapshot.OutboxSummary.Pending != 1 {
		t.Fatalf("outbox = %+v summary=%+v", snapshot.Outbox, snapshot.OutboxSummary)
	}
	if len(snapshot.OutboxQuarantine) != 1 || snapshot.OutboxQuarantine[0].ID != "outbox-snapshot-quarantined" || !snapshot.OutboxQuarantine[0].Restorable || snapshot.OutboxQuarantine[0].Job != "squ-501" || snapshot.OutboxQuarantineSummary == nil || snapshot.OutboxQuarantineSummary.Quarantined != 1 || snapshot.OutboxQuarantineSummary.Restorable != 1 || snapshot.OutboxQuarantineSummary.Unrestorable != 0 {
		t.Fatalf("outbox quarantine = %+v summary=%+v", snapshot.OutboxQuarantine, snapshot.OutboxQuarantineSummary)
	}
	if snapshot.Overview == nil || snapshot.Overview.Outbox.Pending != 1 {
		t.Fatalf("overview outbox = %+v", snapshot.Overview)
	}
	if len(snapshot.QueueQuarantine) != 1 || snapshot.QueueQuarantine[0].ID != "q-snapshot-quarantined" || !snapshot.QueueQuarantine[0].Restorable || snapshot.QueueQuarantine[0].Job != "squ-501" {
		t.Fatalf("queue quarantine = %+v", snapshot.QueueQuarantine)
	}
	if len(snapshot.JobQuarantine) != 1 || snapshot.JobQuarantine[0].ID != "squ-502" || !snapshot.JobQuarantine[0].Restorable || snapshot.JobQuarantineSummary == nil || snapshot.JobQuarantineSummary.Quarantined != 1 || snapshot.JobQuarantineSummary.Restorable != 1 {
		t.Fatalf("job quarantine = %+v summary=%+v", snapshot.JobQuarantine, snapshot.JobQuarantineSummary)
	}
	if snapshot.InboxSummary == nil || snapshot.InboxSummary.Total != 1 || snapshot.InboxSummary.Unread != 1 || snapshot.InboxSummary.UnreadInstances != 1 {
		t.Fatalf("inbox summary = %+v", snapshot.InboxSummary)
	}
	if snapshot.Overview == nil || snapshot.Overview.Inbox.Unread != 1 {
		t.Fatalf("overview inbox = %+v", snapshot.Overview)
	}
	if len(snapshot.Inbox) != 1 || snapshot.Inbox[0].Instance != "manager" || snapshot.Inbox[0].LatestID != "msg-snapshot" || snapshot.Inbox[0].LatestBody != snapshotRedactedValue {
		t.Fatalf("snapshot inbox = %+v", snapshot.Inbox)
	}
	if snapshot.Queue[0].Payload["target"] != "worker" || snapshot.Queue[0].Payload["access_token"] != snapshotRedactedValue {
		t.Fatalf("redacted payload = %+v", snapshot.Queue[0].Payload)
	}
	headers, ok := snapshot.Queue[0].Payload["headers"].(map[string]any)
	if !ok || headers["authorization"] != snapshotRedactedValue || headers["safe"] != "visible" {
		t.Fatalf("nested redacted payload = %+v", snapshot.Queue[0].Payload["headers"])
	}
	if snapshot.Outbox[0].Payload["target"] != "worker" || snapshot.Outbox[0].Payload["access_token"] != snapshotRedactedValue {
		t.Fatalf("redacted outbox payload = %+v", snapshot.Outbox[0].Payload)
	}
	outboxHeaders, ok := snapshot.Outbox[0].Payload["headers"].(map[string]any)
	if !ok || outboxHeaders["authorization"] != snapshotRedactedValue || outboxHeaders["safe"] != "visible" {
		t.Fatalf("nested redacted outbox payload = %+v", snapshot.Outbox[0].Payload["headers"])
	}
	if len(snapshot.Events) != 1 || snapshot.Events[0].Instance != "worker-squ-501" {
		t.Fatalf("events = %+v", snapshot.Events)
	}
	if len(snapshot.Intake) != 1 || snapshot.Intake[0].ID != "intake-snapshot" || snapshot.IntakeSummary == nil || snapshot.IntakeSummary.Errors != 1 || snapshot.IntakeSummary.Replayable != 1 {
		t.Fatalf("intake = %+v summary=%+v", snapshot.Intake, snapshot.IntakeSummary)
	}
	if snapshot.Intake[0].Payload["ticket"] != "SQU-501" || snapshot.Intake[0].Payload["access_token"] != snapshotRedactedValue {
		t.Fatalf("redacted intake payload = %+v", snapshot.Intake[0].Payload)
	}
	intakeHeaders, ok := snapshot.Intake[0].Payload["headers"].(map[string]any)
	if !ok || intakeHeaders["authorization"] != snapshotRedactedValue || intakeHeaders["safe"] != "visible" {
		t.Fatalf("nested redacted intake payload = %+v", snapshot.Intake[0].Payload["headers"])
	}
	if len(snapshot.Intake[0].Actions) == 0 || !strings.Contains(snapshot.Intake[0].Actions[0], "agent-team intake replay intake-snapshot") {
		t.Fatalf("intake actions = %+v", snapshot.Intake[0].Actions)
	}
	if snapshot.Runtime == nil || snapshot.Runtime.Runtime == "" {
		t.Fatalf("runtime = %+v", snapshot.Runtime)
	}
	if snapshot.TeamsDoctor == nil || !snapshot.TeamsDoctor.OK || len(snapshot.TeamsDoctor.Teams) != 1 {
		t.Fatalf("teams doctor = %+v", snapshot.TeamsDoctor)
	}
}

func TestSnapshotEventsSortNewestTailsAfterLimit(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	for _, ev := range []*daemon.LifecycleEvent{
		{TS: now.Add(-3 * time.Minute), Action: "start", Instance: "old-worker", Agent: "worker", Status: daemon.StatusRunning, Message: "old"},
		{TS: now.Add(-2 * time.Minute), Action: "dispatch", Instance: "middle-worker", Agent: "worker", Status: daemon.StatusRunning, Message: "middle"},
		{TS: now.Add(-time.Minute), Action: "stop", Instance: "new-worker", Agent: "worker", Status: daemon.StatusStopped, Message: "new"},
	} {
		if err := daemon.AppendLifecycleEvent(daemon.DaemonRoot(teamDir), ev); err != nil {
			t.Fatalf("append event: %v", err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"snapshot", "--target", tmp, "--events", "2", "--events-sort", "newest", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("snapshot events newest: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot snapshotResult
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v\nbody=%s", err, out.String())
	}
	if got := lifecycleEventInstances(snapshot.Events); strings.Join(got, ",") != "new-worker,middle-worker" {
		t.Fatalf("snapshot events = %v\nbody=%s", got, out.String())
	}
	if snapshot.Provenance == nil || snapshot.Provenance.Options.EventSort != "newest" {
		t.Fatalf("snapshot provenance = %+v", snapshot.Provenance)
	}
}

func TestTeamSnapshotEventsSortNewestTailsAfterScopedLimit(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.other]
agent = "other"

[teams.delivery]
instances = ["manager", "worker"]
`), 0o644); err != nil {
		t.Fatalf("write team topology: %v", err)
	}
	now := time.Now().UTC()
	for _, ev := range []*daemon.LifecycleEvent{
		{TS: now.Add(-4 * time.Minute), Action: "start", Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, Message: "manager"},
		{TS: now.Add(-3 * time.Minute), Action: "dispatch", Instance: "worker", Agent: "worker", Status: daemon.StatusRunning, Message: "worker"},
		{TS: now.Add(-2 * time.Minute), Action: "dispatch", Instance: "other", Agent: "other", Status: daemon.StatusRunning, Message: "other"},
		{TS: now.Add(-time.Minute), Action: "stop", Instance: "manager", Agent: "manager", Status: daemon.StatusStopped, Message: "manager stopped"},
	} {
		if err := daemon.AppendLifecycleEvent(daemon.DaemonRoot(teamDir), ev); err != nil {
			t.Fatalf("append event: %v", err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "snapshot", "delivery", "--repo", root, "--events", "2", "--events-sort", "newest", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team snapshot events newest: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot snapshotResult
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode team snapshot: %v\nbody=%s", err, out.String())
	}
	if got := lifecycleEventInstances(snapshot.Events); strings.Join(got, ",") != "manager,worker" {
		t.Fatalf("team snapshot events = %v\nbody=%s", got, out.String())
	}
	if snapshot.Provenance == nil || snapshot.Provenance.Options.EventSort != "newest" {
		t.Fatalf("team snapshot provenance = %+v", snapshot.Provenance)
	}
	if strings.Contains(out.String(), "other") {
		t.Fatalf("team snapshot leaked non-team event:\n%s", out.String())
	}
}

func TestSnapshotIncludesPipelineAdvancePreview(t *testing.T) {
	target, _, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-503",
		Ticket:    "SQU-503",
		Target:    "worker",
		Kickoff:   "SQU-503: implement",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "triage", Target: "manager", Status: job.StatusDone, Instance: "manager", StartedAt: now, FinishedAt: now},
			{ID: "implement", Target: "worker", Status: job.StatusBlocked, After: []string{"triage"}},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write ready job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"snapshot", "--target", target, "--events", "0", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("snapshot pipeline advance: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot snapshotResult
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v\nbody=%s", err, out.String())
	}
	if len(snapshot.PipelineAdvance) != 1 || snapshot.PipelineAdvance[0].JobID != "squ-503" || snapshot.PipelineAdvance[0].Action != "would_advance" || !snapshot.PipelineAdvance[0].DryRun {
		t.Fatalf("pipeline advance preview = %+v", snapshot.PipelineAdvance)
	}
	if len(snapshot.PipelineStatus) != 1 || snapshot.PipelineStatus[0].Pipeline != "ticket_to_pr" || snapshot.PipelineStatus[0].Declared || snapshot.PipelineStatus[0].Jobs != 1 || snapshot.PipelineStatus[0].ReadySteps != 1 {
		t.Fatalf("pipeline status = %+v", snapshot.PipelineStatus)
	}
	if len(snapshot.PipelineExplain) != 1 || snapshot.PipelineExplain[0].Pipeline != "ticket_to_pr" || snapshot.PipelineExplain[0].ExplainedJobs != 1 || len(snapshot.PipelineExplain[0].Jobs) != 1 || snapshot.PipelineExplain[0].Jobs[0].JobID != "squ-503" || len(snapshot.PipelineExplain[0].Jobs[0].Steps) != 2 {
		t.Fatalf("pipeline explain = %+v", snapshot.PipelineExplain)
	}
	preview := snapshot.PipelineAdvance[0].Preview
	if preview == nil || preview.Step == nil || preview.Step.ID != "implement" || preview.Dispatch == nil || preview.Dispatch.RequestedName != "worker-squ-503-implement" {
		t.Fatalf("pipeline advance route preview = %+v", preview)
	}
	if preview.Dispatch.Preview == nil || len(preview.Dispatch.Preview.Matched) != 1 || preview.Dispatch.Preview.Matched[0] != "worker" {
		t.Fatalf("pipeline advance dispatch preview = %+v", preview.Dispatch.Preview)
	}
	payload := preview.Dispatch.Preview.Payload
	if payload["job_id"] != "squ-503" || payload["pipeline"] != "ticket_to_pr" || payload["pipeline_step"] != "implement" || payload["workspace"] != "worktree" {
		t.Fatalf("pipeline advance payload = %+v", payload)
	}
	unchanged, err := job.Read(teamDir, "squ-503")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.Steps[1].Status != job.StatusBlocked {
		t.Fatalf("snapshot mutated job = %+v", unchanged)
	}
}

func TestSnapshotFormatCommands(t *testing.T) {
	target, _, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
`), 0o644); err != nil {
		t.Fatalf("write team topology: %v", err)
	}
	now := time.Now().UTC()
	j, err := job.New("SQU-287", "worker", "format snapshot commands", now)
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	j.Pipeline = "ticket_to_pr"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "global",
			args: []string{"--repo", target, "snapshot", "--events", "0", "--format", "{{len .Jobs}}:{{.Redacted}}"},
			want: "1:true\n",
		},
		{
			name: "job",
			args: []string{"job", "snapshot", "squ-287", "--repo", target, "--format", "{{.Job.ID}}:{{.Redacted}}"},
			want: "squ-287:true\n",
		},
		{
			name: "pipeline",
			args: []string{"pipeline", "snapshot", "ticket_to_pr", "--repo", target, "--format", "{{.Pipeline}}:{{len .Jobs}}:{{.Redacted}}"},
			want: "ticket_to_pr:1:true\n",
		},
		{
			name: "team",
			args: []string{"team", "snapshot", "delivery", "--repo", target, "--events", "0", "--format", "{{.Team.Name}}:{{len .Jobs}}:{{.Redacted}}"},
			want: "delivery:1:true\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("%s snapshot --format: %v\nstderr=%s", tc.name, err, stderr.String())
			}
			if got := out.String(); got != tc.want {
				t.Fatalf("%s snapshot --format output = %q, want %q", tc.name, got, tc.want)
			}
		})
	}

	conflict := NewRootCmd()
	conflictOut, conflictErr := &bytes.Buffer{}, &bytes.Buffer{}
	conflict.SetOut(conflictOut)
	conflict.SetErr(conflictErr)
	conflict.SetArgs([]string{"--repo", target, "snapshot", "--format", "{{.Repo}}", "--json"})
	if err := conflict.Execute(); err == nil {
		t.Fatalf("snapshot --format --json succeeded")
	}
	if !strings.Contains(conflictErr.String(), "--format cannot be combined with --json or --output") {
		t.Fatalf("format/json stderr = %q", conflictErr.String())
	}

	jobConflict := NewRootCmd()
	jobConflictOut, jobConflictErr := &bytes.Buffer{}, &bytes.Buffer{}
	jobConflict.SetOut(jobConflictOut)
	jobConflict.SetErr(jobConflictErr)
	jobConflict.SetArgs([]string{"job", "snapshot", "squ-287", "--repo", target, "--format", "{{.Job.ID}}", "--output", filepath.Join(target, "job-snapshot.json")})
	if err := jobConflict.Execute(); err == nil {
		t.Fatalf("job snapshot --format --output succeeded")
	}
	if !strings.Contains(jobConflictErr.String(), "--format cannot be combined with --json or --output") {
		t.Fatalf("job format/output stderr = %q", jobConflictErr.String())
	}

	invalid := NewRootCmd()
	invalidOut, invalidErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalid.SetOut(invalidOut)
	invalid.SetErr(invalidErr)
	invalid.SetArgs([]string{"pipeline", "snapshot", "ticket_to_pr", "--repo", target, "--format", "{{"})
	if err := invalid.Execute(); err == nil {
		t.Fatalf("pipeline snapshot invalid format succeeded")
	}
	if !strings.Contains(invalidErr.String(), "invalid --format template") {
		t.Fatalf("invalid format stderr = %q", invalidErr.String())
	}

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "global events sort",
			args: []string{"snapshot", "--target", target, "--events-sort", "sideways"},
			want: "agent-team snapshot: --events-sort must be oldest or newest.",
		},
		{
			name: "team events sort",
			args: []string{"team", "snapshot", "delivery", "--repo", target, "--events-sort", "sideways"},
			want: "agent-team team snapshot: --events-sort must be oldest or newest.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil {
				t.Fatalf("%s succeeded", tc.name)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("%s stderr = %q, want %q", tc.name, stderr.String(), tc.want)
			}
		})
	}
}

func TestSnapshotCommands(t *testing.T) {
	target := t.TempDir()
	initInto(t, target)
	scope := operatorCommandScope{Repo: target, Set: true}

	global := NewRootCmd()
	globalOut, globalErr := &bytes.Buffer{}, &bytes.Buffer{}
	global.SetOut(globalOut)
	global.SetErr(globalErr)
	global.SetArgs([]string{"--repo", target, "snapshot", "--events", "0", "--commands"})
	if err := global.Execute(); err != nil {
		t.Fatalf("snapshot --commands: %v\nstderr=%s", err, globalErr.String())
	}
	globalBody := globalOut.String()
	if !strings.Contains(globalBody, scopedOperatorAction("agent-team daemon start", scope)) ||
		!strings.Contains(globalBody, scopedOperatorAction("agent-team sync --dry-run", scope)) ||
		strings.Contains(globalBody, "snapshot:") ||
		strings.Contains(globalBody, "next:") {
		t.Fatalf("snapshot --commands output = %q", globalBody)
	}

	team := NewRootCmd()
	teamOut, teamErr := &bytes.Buffer{}, &bytes.Buffer{}
	team.SetOut(teamOut)
	team.SetErr(teamErr)
	team.SetArgs([]string{"team", "snapshot", "delivery", "--repo", target, "--events", "0", "--commands"})
	if err := team.Execute(); err != nil {
		t.Fatalf("team snapshot --commands: %v\nstderr=%s", err, teamErr.String())
	}
	teamBody := teamOut.String()
	if !strings.Contains(teamBody, scopedOperatorAction("agent-team daemon start", scope)) ||
		!strings.Contains(teamBody, scopedOperatorAction("agent-team team sync delivery --dry-run", scope)) ||
		strings.Contains(teamBody, "snapshot:") ||
		strings.Contains(teamBody, "next:") {
		t.Fatalf("team snapshot --commands output = %q", teamBody)
	}

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "global json conflict",
			args: []string{"--repo", target, "snapshot", "--commands", "--json"},
			want: "agent-team snapshot: --commands cannot be combined with --json, --output, or --format.",
		},
		{
			name: "team format conflict",
			args: []string{"team", "snapshot", "delivery", "--repo", target, "--commands", "--format", "{{.Team.Name}}"},
			want: "agent-team team snapshot: --commands cannot be combined with --json, --output, or --format.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil {
				t.Fatalf("%s succeeded", tc.name)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("%s stderr = %q, want %q", tc.name, stderr.String(), tc.want)
			}
		})
	}
}

func TestSnapshotDiffCommandReportsChanges(t *testing.T) {
	tmp := t.TempDir()
	beforePath := filepath.Join(tmp, "before.json")
	afterPath := filepath.Join(tmp, "after.json")
	exitCode := 1
	before := snapshotDiffInput{
		CapturedAt: "2026-06-18T12:00:00Z",
		Pipeline:   "ticket_to_pr",
		Git:        &snapshotGitInfo{Branch: "main", Commit: "1111111", Upstream: "origin/main", Behind: 1},
		Runtime: &runtimeInfo{
			Runtime:        "claude",
			Binary:         "claude",
			Path:           "/usr/local/bin/claude",
			Available:      true,
			DirectRun:      true,
			DaemonDispatch: true,
			DirectResume:   true,
			ManagedResume:  true,
			Resume:         true,
			Subagents:      true,
			ConfigPath:     "/repo/.agent_team/config.toml",
		},
		Health: &healthResult{
			Healthy: false,
			Daemon:  healthDaemon{Running: false, Ready: false},
			Summary: psSummaryJSON{
				Total:     1,
				Crashed:   1,
				Unhealthy: 1,
			},
			Queue:    queueSummary{Total: 1, Dead: 1},
			Intake:   overviewIntakeSummary{Deliveries: 1, Errors: 1},
			Declared: healthDeclared{Persistent: 1, Missing: 1},
			Issues: []healthIssue{
				{Code: "daemon_down", Severity: "error"},
			},
		},
		Plan: &planResult{
			Daemon: planDaemon{Running: false},
			Summary: planSummary{
				Total:       2,
				Start:       1,
				Unsupported: 1,
			},
			Instances: []planRow{
				{Instance: "manager", Agent: "manager", Kind: "persistent", Status: "missing", Phase: "unknown", Action: "start"},
				{Instance: "worker", Agent: "worker", Kind: "on_demand", Status: "stopped", Phase: "idle", Action: "unsupported"},
			},
		},
		Next: &nextActionResult{
			OK:      false,
			State:   "attention",
			Actions: []string{"agent-team health --jobs", "agent-team sync --dry-run"},
			ActionDetails: []operatorActionHint{
				{Command: "agent-team health --jobs", Source: "health", Reason: "issues"},
				{Command: "agent-team sync --dry-run", Source: "topology", Reason: "missing"},
			},
			TotalActions: 2,
		},
		Provenance: newSnapshotProvenance("agent-team snapshot", "global", "", snapshotProvenanceOptions{
			Events:           intValuePtr(20),
			IntakeDeliveries: intValuePtr(20),
			ScheduleLimit:    intValuePtr(10),
			Redacted:         true,
		}),
		Instances: []snapshotDiffInstance{
			{Instance: "manager", Agent: "manager", Status: "running", Phase: "idle"},
			{Instance: "worker-squ-801", Agent: "worker", Status: "running", Phase: "blocked", Runtime: "codex", Job: "squ-801"},
		},
		Jobs: []snapshotDiffJob{
			{ID: "squ-801", Status: "running", Pipeline: "ticket_to_pr", Target: "worker"},
			{ID: "squ-802", Status: "blocked", Pipeline: "ticket_to_pr", Target: "worker"},
		},
		JobQuarantine: []jobQuarantineItem{
			{Path: "quarantine/20260618T120000.000000000Z/squ-802.toml", ID: "squ-802", Ticket: "SQU-802", Target: "worker", Status: job.StatusBlocked, Restorable: false, Problem: "unknown job status"},
		},
		Inbox: []snapshotDiffInbox{
			{Instance: "manager", Agent: "manager", Status: "running", Total: 1, Unread: 1, LatestID: "msg-1", LatestFrom: "tester", LatestTS: "2026-06-18T11:59:00Z"},
		},
		Outbox: []snapshotDiffOutboxItem{
			{ID: "outbox-1", State: "pending", Type: "agent.dispatch", Source: "manager", Payload: map[string]any{"job_id": "squ-801", "target": "worker"}},
		},
		OutboxQuarantine: []snapshotDiffOutboxQuarantine{
			{Path: "pending/outbox-quarantine-old.json", State: "pending", ID: "outbox-quarantine-old", Type: "agent.dispatch", Source: "manager", Job: "squ-802", Target: "worker", Restorable: false, Problem: "invalid json"},
		},
		Queue: []snapshotDiffQueueItem{
			{ID: "q-1", State: "pending"},
		},
		QueueQuarantine: []snapshotDiffQuarantine{
			{Path: "dead/q-dead.json", State: "dead", ID: "q-dead", EventType: "ticket.created", Instance: "worker", Job: "squ-802", Restorable: false, Problem: "invalid json"},
		},
		Schedules: []snapshotDiffSchedule{
			{Name: "delivery_due", Event: "schedule", Every: "1h", RunOnStart: true, NextRun: "2026-06-18T13:00:00Z"},
		},
		ScheduleNext: []snapshotDiffSchedule{
			{Name: "delivery_due", Event: "schedule", Every: "1h", RunOnStart: true, Due: true, DueReason: "run_on_start"},
		},
		Intake: []snapshotDiffIntake{
			{ID: "delivery-1", Provider: "linear", Status: "error", HTTPStatus: 500, ReplayStatus: "error", EventType: "ticket.created", Ticket: "SQU-801", JobID: "squ-801"},
		},
		IntakeDuplicates: []snapshotDiffIntakeDuplicate{
			{Provider: "github", RequestID: "github-delivery-1", Count: 2, IDs: []string{"delivery-1", "delivery-old"}},
		},
		Events: []snapshotDiffEvent{
			{ID: "ev-1", Action: "start", Instance: "worker-squ-801", Agent: "worker", Job: "squ-801", Status: "running"},
		},
		Timeline: []snapshotDiffTimelineEntry{
			{TS: "2026-06-18T12:01:00Z", Source: "job", JobID: "squ-801", Kind: "note", Status: "running", Actor: "cli", Message: "started"},
		},
		Status: &snapshotDiffStatus{
			Pipeline:     "ticket_to_pr",
			Jobs:         2,
			ReadySteps:   1,
			FailedSteps:  0,
			BlockedSteps: 1,
		},
		AdvancePreview: []snapshotDiffAdvance{
			{JobID: "squ-801", Pipeline: "ticket_to_pr", StepID: "implement", Target: "worker", Action: "would_advance"},
		},
		SectionErrors: map[string]string{"queue": "parse failed"},
	}
	after := snapshotDiffInput{
		CapturedAt: "2026-06-18T12:05:00Z",
		Pipeline:   "ticket_to_pr",
		Git:        &snapshotGitInfo{Branch: "feature/squ-801", Commit: "2222222", Upstream: "origin/feature/squ-801", Dirty: true, Changes: 3, Ahead: 2},
		Runtime: &runtimeInfo{
			Runtime:        "codex",
			Binary:         "codex",
			Path:           "/usr/local/bin/codex",
			Available:      true,
			DirectRun:      true,
			DaemonDispatch: true,
			DirectResume:   true,
			EnvRuntime:     "codex",
			ConfigPath:     "/repo/.agent_team/config.toml",
		},
		Health: &healthResult{
			Healthy: true,
			Daemon:  healthDaemon{Running: true, Ready: true},
			Summary: psSummaryJSON{
				Total:   1,
				Running: 1,
			},
			Intake:   overviewIntakeSummary{Deliveries: 1, Recovered: 1},
			Declared: healthDeclared{Persistent: 1, Running: 1},
		},
		Plan: &planResult{
			Daemon: planDaemon{Running: true},
			Summary: planSummary{
				Total:    2,
				Keep:     1,
				OnDemand: 1,
			},
			Instances: []planRow{
				{Instance: "manager", Agent: "manager", Kind: "persistent", Status: "running", Phase: "idle", Action: "keep"},
				{Instance: "worker", Agent: "worker", Kind: "on_demand", Status: "stopped", Phase: "idle", Action: "on-demand"},
			},
		},
		Next: &nextActionResult{
			OK:      true,
			State:   "ok",
			Actions: []string{"agent-team snapshot --output diagnostics.json"},
			ActionDetails: []operatorActionHint{
				{Command: "agent-team snapshot --output diagnostics.json", Source: "snapshot", Reason: "handoff"},
			},
			TotalActions: 1,
		},
		Provenance: newSnapshotProvenance("agent-team team snapshot", "team", "delivery", snapshotProvenanceOptions{
			Events:        intValuePtr(5),
			ScheduleLimit: intValuePtr(0),
			Redacted:      false,
		}),
		Instances: []snapshotDiffInstance{
			{Instance: "manager", Agent: "manager", Status: "running", Phase: "idle"},
			{Instance: "reviewer-squ-803", Agent: "manager", Status: "running", Phase: "working", Runtime: "claude", Job: "squ-803"},
			{Instance: "worker-squ-801", Agent: "worker", Status: "exited", Phase: "done", Runtime: "codex", Job: "squ-801"},
		},
		Jobs: []snapshotDiffJob{
			{ID: "squ-801", Status: "done", Pipeline: "ticket_to_pr", Target: "worker"},
			{ID: "squ-803", Status: "queued", Pipeline: "ticket_to_pr", Target: "manager"},
		},
		JobQuarantine: []jobQuarantineItem{
			{Path: "quarantine/20260618T120000.000000000Z/squ-802.toml", ID: "squ-802", Ticket: "SQU-802", Target: "worker", Status: job.StatusBlocked, Restorable: true},
			{Path: "quarantine/20260618T120500.000000000Z/squ-803.toml", ID: "squ-803", Ticket: "SQU-803", Target: "manager", Status: job.StatusQueued, Restorable: true},
		},
		Inbox: []snapshotDiffInbox{
			{Instance: "manager", Agent: "manager", Status: "running", Total: 2, Unread: 0, Cursor: "msg-2", LatestID: "msg-2", LatestFrom: "worker", LatestTS: "2026-06-18T12:04:00Z"},
			{Instance: "worker-squ-803", Agent: "worker", Status: "running", Total: 1, Unread: 1, LatestID: "msg-3", LatestFrom: "manager", LatestTS: "2026-06-18T12:05:00Z"},
		},
		Outbox: []snapshotDiffOutboxItem{
			{ID: "outbox-1", State: "failed", Type: "agent.dispatch", Source: "manager", Payload: map[string]any{"job_id": "squ-801", "target": "worker"}, LastError: "route missing"},
			{ID: "outbox-2", State: "pending", Type: "agent.dispatch", Source: "manager", Payload: map[string]any{"job_id": "squ-803", "target": "manager"}},
		},
		OutboxQuarantine: []snapshotDiffOutboxQuarantine{
			{Path: "pending/outbox-quarantine-old.json", State: "pending", ID: "outbox-quarantine-old", Type: "agent.dispatch", Source: "manager", Job: "squ-802", Target: "worker", Restorable: true},
			{Path: "failed/outbox-quarantine-new.json", State: "failed", ID: "outbox-quarantine-new", Type: "agent.dispatch", Source: "manager", Job: "squ-803", Target: "manager", Restorable: true},
		},
		Queue: []snapshotDiffQueueItem{
			{ID: "q-1", State: "dead"},
			{ID: "q-2", State: "pending"},
		},
		QueueQuarantine: []snapshotDiffQuarantine{
			{Path: "dead/q-dead.json", State: "dead", ID: "q-dead", EventType: "ticket.created", Instance: "worker", Job: "squ-802", Restorable: true},
			{Path: "pending/q-new.json", State: "pending", ID: "q-new", EventType: "ticket.created", Instance: "worker", Job: "squ-803", Restorable: true},
		},
		Schedules: []snapshotDiffSchedule{
			{Name: "delivery_due", Event: "schedule", Every: "2h", RunOnStart: true, NextRun: "2026-06-18T14:00:00Z"},
		},
		ScheduleNext: []snapshotDiffSchedule{
			{Name: "delivery_due", Event: "schedule", Every: "2h", RunOnStart: true, Due: false, NextRun: "2026-06-18T14:00:00Z"},
		},
		Intake: []snapshotDiffIntake{
			{ID: "delivery-1", Provider: "linear", Status: "ok", HTTPStatus: 200, ReplayStatus: "ok", EventType: "ticket.created", Ticket: "SQU-801", JobID: "squ-801"},
			{ID: "delivery-2", Provider: "github", Status: "ok", HTTPStatus: 202, EventType: "pr.merged", PR: "https://github.test/pr/1", JobID: "squ-801"},
		},
		IntakeDuplicates: []snapshotDiffIntakeDuplicate{
			{Provider: "github", RequestID: "github-delivery-1", Count: 3, IDs: []string{"delivery-1", "delivery-2", "delivery-old"}},
		},
		Events: []snapshotDiffEvent{
			{ID: "ev-1", Action: "exit", Instance: "worker-squ-801", Agent: "worker", Job: "squ-801", Status: "exited", ExitCode: &exitCode},
			{ID: "ev-2", Action: "queued", Instance: "manager", Agent: "manager", Job: "squ-803", Status: "running"},
		},
		Timeline: []snapshotDiffTimelineEntry{
			{TS: "2026-06-18T12:01:00Z", Source: "job", JobID: "squ-801", Kind: "note", Status: "done", Actor: "cli", Message: "completed"},
			{TS: "2026-06-18T12:04:00Z", Source: "lifecycle", JobID: "squ-803", Kind: "dispatch", Status: "running", Instance: "reviewer-squ-803", Agent: "manager"},
		},
		Status: &snapshotDiffStatus{
			Pipeline:     "ticket_to_pr",
			Jobs:         2,
			ReadySteps:   0,
			FailedSteps:  1,
			BlockedSteps: 0,
		},
		AdvancePreview: []snapshotDiffAdvance{
			{JobID: "squ-803", Pipeline: "ticket_to_pr", StepID: "review", Target: "manager", Action: "would_advance"},
		},
		SectionErrors: map[string]string{"advance_preview": "route missing"},
	}
	writeSnapshotDiffInput(t, beforePath, before)
	writeSnapshotDiffInput(t, afterPath, after)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("snapshot diff json: %v\nstderr=%s", err, stderr.String())
	}
	var result snapshotDiffResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode snapshot diff json: %v\nbody=%s", err, out.String())
	}
	if result.Before.Kind != "pipeline" || result.Before.Scope != "ticket_to_pr" || result.After.Scope != "ticket_to_pr" {
		t.Fatalf("snapshot diff metadata = %+v -> %+v", result.Before, result.After)
	}
	if result.Summary.Provenance.Added != 1 || result.Summary.Provenance.Removed != 1 || result.Summary.Provenance.Changed != 5 {
		t.Fatalf("provenance counters = %+v", result.Summary.Provenance)
	}
	if result.Summary.Git.Changed != 7 {
		t.Fatalf("git counters = %+v", result.Summary.Git)
	}
	if result.Summary.Runtime.Added != 1 || result.Summary.Runtime.Changed != 6 {
		t.Fatalf("runtime counters = %+v", result.Summary.Runtime)
	}
	if result.Summary.Health.Removed != 2 || result.Summary.Health.Changed != 12 {
		t.Fatalf("health counters = %+v", result.Summary.Health)
	}
	if result.Summary.Plan.Changed != 7 {
		t.Fatalf("plan counters = %+v", result.Summary.Plan)
	}
	if result.Summary.Next.Added != 1 || result.Summary.Next.Removed != 2 || result.Summary.Next.Changed != 3 {
		t.Fatalf("next counters = %+v", result.Summary.Next)
	}
	if result.Summary.Jobs.Added != 1 || result.Summary.Jobs.Removed != 1 || result.Summary.Jobs.Changed != 1 {
		t.Fatalf("job counters = %+v", result.Summary.Jobs)
	}
	if result.Summary.JobQuarantine.Added != 1 || result.Summary.JobQuarantine.Changed != 1 {
		t.Fatalf("job quarantine counters = %+v", result.Summary.JobQuarantine)
	}
	if result.Summary.Instances.Added != 1 || result.Summary.Instances.Changed != 1 {
		t.Fatalf("instance counters = %+v", result.Summary.Instances)
	}
	if result.Summary.Inbox.Added != 1 || result.Summary.Inbox.Changed != 1 {
		t.Fatalf("inbox counters = %+v", result.Summary.Inbox)
	}
	if result.Summary.Outbox.Added != 1 || result.Summary.Outbox.Changed != 1 {
		t.Fatalf("outbox counters = %+v", result.Summary.Outbox)
	}
	if result.Summary.OutboxQuarantine.Added != 1 || result.Summary.OutboxQuarantine.Changed != 1 {
		t.Fatalf("outbox quarantine counters = %+v", result.Summary.OutboxQuarantine)
	}
	if result.Summary.Queue.Added != 1 || result.Summary.Queue.Changed != 1 {
		t.Fatalf("queue counters = %+v", result.Summary.Queue)
	}
	if result.Summary.QueueQuarantine.Added != 1 || result.Summary.QueueQuarantine.Changed != 1 {
		t.Fatalf("queue quarantine counters = %+v", result.Summary.QueueQuarantine)
	}
	if result.Summary.Schedules.Changed != 2 {
		t.Fatalf("schedule counters = %+v", result.Summary.Schedules)
	}
	if result.Summary.Intake.Added != 1 || result.Summary.Intake.Changed != 2 {
		t.Fatalf("intake counters = %+v", result.Summary.Intake)
	}
	if result.Summary.Events.Added != 1 || result.Summary.Events.Changed != 1 {
		t.Fatalf("event counters = %+v", result.Summary.Events)
	}
	if result.Summary.Timeline.Added != 1 || result.Summary.Timeline.Changed != 1 {
		t.Fatalf("timeline counters = %+v", result.Summary.Timeline)
	}
	if result.Summary.Advance.Added != 1 || result.Summary.Advance.Removed != 1 {
		t.Fatalf("advance counters = %+v", result.Summary.Advance)
	}
	if result.Summary.SectionErrors.Added != 1 || result.Summary.SectionErrors.Removed != 1 {
		t.Fatalf("section error counters = %+v", result.Summary.SectionErrors)
	}
	if !hasSnapshotDiffChange(result.Changes, "provenance", "command", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "provenance", "subject", "added") ||
		!hasSnapshotDiffChange(result.Changes, "provenance", "intake_deliveries", "removed") ||
		!hasSnapshotDiffChange(result.Changes, "git", "branch", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "git", "dirty", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "runtime", "runtime", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "runtime", "env_runtime", "added") ||
		!hasSnapshotDiffChange(result.Changes, "runtime", "subagents", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "health", "healthy", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "health", "issues.code.daemon_down", "removed") ||
		!hasSnapshotDiffChange(result.Changes, "plan", "daemon.running", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "plan", "instance.manager", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "next", "ok", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "next", "action/agent-team health --jobs", "removed") ||
		!hasSnapshotDiffChange(result.Changes, "next", "action/agent-team snapshot --output diagnostics.json", "added") ||
		!hasSnapshotDiffChange(result.Changes, "jobs", "squ-801", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "jobs", "squ-802", "removed") ||
		!hasSnapshotDiffChange(result.Changes, "jobs", "squ-803", "added") ||
		!hasSnapshotDiffChange(result.Changes, "job_quarantine", "quarantine/20260618T120000.000000000Z/squ-802.toml", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "job_quarantine", "quarantine/20260618T120500.000000000Z/squ-803.toml", "added") ||
		!hasSnapshotDiffChange(result.Changes, "instances", "reviewer-squ-803", "added") ||
		!hasSnapshotDiffChange(result.Changes, "instances", "worker-squ-801", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "inbox", "manager", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "inbox", "worker-squ-803", "added") ||
		!hasSnapshotDiffChange(result.Changes, "outbox", "outbox-1", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "outbox", "outbox-2", "added") ||
		!hasSnapshotDiffChange(result.Changes, "outbox_quarantine", "pending/outbox-quarantine-old.json", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "outbox_quarantine", "failed/outbox-quarantine-new.json", "added") ||
		!hasSnapshotDiffChange(result.Changes, "queue_quarantine", "dead/q-dead.json", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "schedules", "declared/delivery_due", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "intake", "duplicate/github/github-delivery-1", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "intake", "delivery-1", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "events", "ev-1", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "timeline", "job|squ-801|2026-06-18T12:01:00Z|note", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "timeline", "lifecycle|squ-803|2026-06-18T12:04:00Z|dispatch|reviewer-squ-803", "added") ||
		!hasSnapshotDiffChange(result.Changes, "pipelines", "ticket_to_pr.ready_steps", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "advance", "squ-803:review", "added") {
		t.Fatalf("missing expected changes: %+v", result.Changes)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"snapshot", "diff", beforePath, afterPath})
	if err := text.Execute(); err != nil {
		t.Fatalf("snapshot diff text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{
		"snapshot diff:",
		"provenance: added=1 removed=1 changed=5",
		"git: added=0 removed=0 changed=7",
		"runtime: added=1 removed=0 changed=6",
		"health: added=0 removed=2 changed=12",
		"plan: added=0 removed=0 changed=7",
		"next: added=1 removed=2 changed=3",
		"instances: added=1 removed=0 changed=1",
		"jobs: added=1 removed=1 changed=1",
		"job_quarantine: added=1 removed=0 changed=1",
		"pipelines:",
		"inbox: added=1 removed=0 changed=1",
		"outbox: added=1 removed=0 changed=1",
		"outbox_quarantine: added=1 removed=0 changed=1",
		"queue: added=1 removed=0 changed=1",
		"queue_quarantine: added=1 removed=0 changed=1",
		"schedules: added=0 removed=0 changed=2",
		"intake: added=1 removed=0 changed=2",
		"events: added=1 removed=0 changed=1",
		"timeline: added=1 removed=0 changed=1",
		"advance: added=1 removed=1 changed=0",
		"section_errors: added=1 removed=1 changed=0",
		"squ-801",
		"ticket_to_pr.ready_steps",
	} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("snapshot diff text missing %q:\n%s", want, textOut.String())
		}
	}

	formatted := NewRootCmd()
	formattedOut, formattedErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatted.SetOut(formattedOut)
	formatted.SetErr(formattedErr)
	formatted.SetArgs([]string{
		"snapshot", "diff", beforePath, afterPath,
		"--format", "{{.Summary.Jobs.Added}}:{{.Summary.Queue.Changed}}:{{(index .Changes 0).Section}}:{{(index .Changes 0).ID}}",
	})
	if err := formatted.Execute(); err != nil {
		t.Fatalf("snapshot diff --format: %v\nstderr=%s", err, formattedErr.String())
	}
	if got, want := formattedOut.String(), "1:1:provenance:command\n"; got != want {
		t.Fatalf("snapshot diff --format output = %q, want %q", got, want)
	}

	summaryJSON := NewRootCmd()
	summaryJSONOut, summaryJSONErr := &bytes.Buffer{}, &bytes.Buffer{}
	summaryJSON.SetOut(summaryJSONOut)
	summaryJSON.SetErr(summaryJSONErr)
	summaryJSON.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--summary", "--json"})
	if err := summaryJSON.Execute(); err != nil {
		t.Fatalf("snapshot diff --summary json: %v\nstderr=%s", err, summaryJSONErr.String())
	}
	var summaryResult snapshotDiffResult
	if err := json.Unmarshal(summaryJSONOut.Bytes(), &summaryResult); err != nil {
		t.Fatalf("decode summary snapshot diff json: %v\nbody=%s", err, summaryJSONOut.String())
	}
	if len(summaryResult.Changes) != 0 || !summaryResult.Summary.SummaryOnly || summaryResult.Summary.OmittedChanges != result.Summary.TotalChanges || summaryResult.Summary.TotalChanges != result.Summary.TotalChanges {
		t.Fatalf("summary diff result = %+v changes=%d total=%d", summaryResult.Summary, len(summaryResult.Changes), result.Summary.TotalChanges)
	}
	if summaryResult.Summary.Jobs.Added != result.Summary.Jobs.Added || summaryResult.Summary.Queue.Changed != result.Summary.Queue.Changed {
		t.Fatalf("summary diff counters changed: %+v vs %+v", summaryResult.Summary, result.Summary)
	}

	addedOnly := NewRootCmd()
	addedOnlyOut, addedOnlyErr := &bytes.Buffer{}, &bytes.Buffer{}
	addedOnly.SetOut(addedOnlyOut)
	addedOnly.SetErr(addedOnlyErr)
	addedOnly.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--action", "added", "--json"})
	if err := addedOnly.Execute(); err != nil {
		t.Fatalf("snapshot diff --action added json: %v\nstderr=%s", err, addedOnlyErr.String())
	}
	var addedOnlyResult snapshotDiffResult
	if err := json.Unmarshal(addedOnlyOut.Bytes(), &addedOnlyResult); err != nil {
		t.Fatalf("decode added-only snapshot diff json: %v\nbody=%s", err, addedOnlyOut.String())
	}
	addedTotal := 0
	for _, change := range result.Changes {
		if change.Action == "added" {
			addedTotal++
		}
	}
	if addedOnlyResult.Summary.TotalChanges != addedTotal || len(addedOnlyResult.Changes) != addedTotal || strings.Join(addedOnlyResult.Summary.ActionFilter, ",") != "added" {
		t.Fatalf("added-only diff summary = %+v changes=%d want added=%d", addedOnlyResult.Summary, len(addedOnlyResult.Changes), addedTotal)
	}
	if addedOnlyResult.Summary.Jobs.Added != result.Summary.Jobs.Added || addedOnlyResult.Summary.Jobs.Removed != 0 || addedOnlyResult.Summary.Jobs.Changed != 0 || addedOnlyResult.Summary.Advance.Removed != 0 {
		t.Fatalf("added-only counters = %+v", addedOnlyResult.Summary)
	}
	for _, change := range addedOnlyResult.Changes {
		if change.Action != "added" {
			t.Fatalf("added-only diff included %q change: %+v", change.Action, addedOnlyResult.Changes)
		}
	}

	actionText := NewRootCmd()
	actionTextOut, actionTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	actionText.SetOut(actionTextOut)
	actionText.SetErr(actionTextErr)
	actionText.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--action", "removed", "--limit", "1"})
	if err := actionText.Execute(); err != nil {
		t.Fatalf("snapshot diff --action removed text: %v\nstderr=%s", err, actionTextErr.String())
	}
	if !strings.Contains(actionTextOut.String(), "filter: actions=removed") || !strings.Contains(actionTextOut.String(), "details: showing=1 omitted=") || strings.Contains(actionTextOut.String(), "\tadded\t") {
		t.Fatalf("action text output unexpected:\n%s", actionTextOut.String())
	}

	actionFormat := NewRootCmd()
	actionFormatOut, actionFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	actionFormat.SetOut(actionFormatOut)
	actionFormat.SetErr(actionFormatErr)
	actionFormat.SetArgs([]string{
		"snapshot", "diff", beforePath, afterPath,
		"--action", "added,changed",
		"--format", "{{.Summary.TotalChanges}}:{{.Summary.Jobs.Added}}:{{.Summary.Jobs.Changed}}:{{len .Changes}}:{{index .Summary.ActionFilter 0}},{{index .Summary.ActionFilter 1}}",
	})
	if err := actionFormat.Execute(); err != nil {
		t.Fatalf("snapshot diff --action --format: %v\nstderr=%s", err, actionFormatErr.String())
	}
	actionFilteredTotal := 0
	for _, change := range result.Changes {
		if change.Action == "added" || change.Action == "changed" {
			actionFilteredTotal++
		}
	}
	if got, want := strings.TrimSpace(actionFormatOut.String()), fmt.Sprintf("%d:1:1:%d:added,changed", actionFilteredTotal, actionFilteredTotal); got != want {
		t.Fatalf("snapshot diff --action --format output = %q, want %q", got, want)
	}

	noRemovedQueueExit := NewRootCmd()
	noRemovedQueueExitOut, noRemovedQueueExitErr := &bytes.Buffer{}, &bytes.Buffer{}
	noRemovedQueueExit.SetOut(noRemovedQueueExitOut)
	noRemovedQueueExit.SetErr(noRemovedQueueExitErr)
	noRemovedQueueExit.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "queue", "--action", "removed", "--exit-code"})
	if err := noRemovedQueueExit.Execute(); err != nil {
		t.Fatalf("snapshot diff --action removed --exit-code with no filtered changes: %v\nstderr=%s", err, noRemovedQueueExitErr.String())
	}
	if !strings.Contains(noRemovedQueueExitOut.String(), "changes: total=0") {
		t.Fatalf("no removed queue diff output = %s", noRemovedQueueExitOut.String())
	}

	outputPath := filepath.Join(tmp, "diffs", "jobs-added.json")
	outputFile := NewRootCmd()
	outputFileOut, outputFileErr := &bytes.Buffer{}, &bytes.Buffer{}
	outputFile.SetOut(outputFileOut)
	outputFile.SetErr(outputFileErr)
	outputFile.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "jobs", "--action", "added", "--output", outputPath})
	if err := outputFile.Execute(); err != nil {
		t.Fatalf("snapshot diff --output: %v\nstderr=%s", err, outputFileErr.String())
	}
	if !strings.Contains(outputFileOut.String(), "Wrote snapshot diff to") {
		t.Fatalf("snapshot diff --output stdout = %q", outputFileOut.String())
	}
	outputBody, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read diff output: %v", err)
	}
	var outputResult snapshotDiffResult
	if err := json.Unmarshal(outputBody, &outputResult); err != nil {
		t.Fatalf("decode diff output file: %v\nbody=%s", err, string(outputBody))
	}
	if outputResult.Summary.TotalChanges != 1 || outputResult.Summary.Jobs.Added != 1 || strings.Join(outputResult.Summary.ActionFilter, ",") != "added" || len(outputResult.Changes) != 1 || outputResult.Changes[0].Action != "added" {
		t.Fatalf("diff output result = %+v changes=%+v", outputResult.Summary, outputResult.Changes)
	}

	outputStdout := NewRootCmd()
	outputStdoutOut, outputStdoutErr := &bytes.Buffer{}, &bytes.Buffer{}
	outputStdout.SetOut(outputStdoutOut)
	outputStdout.SetErr(outputStdoutErr)
	outputStdout.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "queue", "--summary", "--output", "-"})
	if err := outputStdout.Execute(); err != nil {
		t.Fatalf("snapshot diff --output -: %v\nstderr=%s", err, outputStdoutErr.String())
	}
	var outputStdoutResult snapshotDiffResult
	if err := json.Unmarshal(outputStdoutOut.Bytes(), &outputStdoutResult); err != nil {
		t.Fatalf("decode diff output stdout: %v\nbody=%s", err, outputStdoutOut.String())
	}
	if !outputStdoutResult.Summary.SummaryOnly || len(outputStdoutResult.Changes) != 0 || outputStdoutResult.Summary.Queue.Added != 1 || outputStdoutResult.Summary.Queue.Changed != 1 {
		t.Fatalf("diff output stdout summary = %+v changes=%+v", outputStdoutResult.Summary, outputStdoutResult.Changes)
	}

	summaryText := NewRootCmd()
	summaryTextOut, summaryTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	summaryText.SetOut(summaryTextOut)
	summaryText.SetErr(summaryTextErr)
	summaryText.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--summary"})
	if err := summaryText.Execute(); err != nil {
		t.Fatalf("snapshot diff --summary text: %v\nstderr=%s", err, summaryTextErr.String())
	}
	if !strings.Contains(summaryTextOut.String(), "details: summary only (omitted=") || strings.Contains(summaryTextOut.String(), "SECTION\tID") || strings.Contains(summaryTextOut.String(), "squ-801") {
		t.Fatalf("summary text output unexpected:\n%s", summaryTextOut.String())
	}

	limitedJSON := NewRootCmd()
	limitedJSONOut, limitedJSONErr := &bytes.Buffer{}, &bytes.Buffer{}
	limitedJSON.SetOut(limitedJSONOut)
	limitedJSON.SetErr(limitedJSONErr)
	limitedJSON.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--limit", "3", "--json"})
	if err := limitedJSON.Execute(); err != nil {
		t.Fatalf("snapshot diff --limit json: %v\nstderr=%s", err, limitedJSONErr.String())
	}
	var limitedResult snapshotDiffResult
	if err := json.Unmarshal(limitedJSONOut.Bytes(), &limitedResult); err != nil {
		t.Fatalf("decode limited snapshot diff json: %v\nbody=%s", err, limitedJSONOut.String())
	}
	if len(limitedResult.Changes) != 3 || limitedResult.Summary.DetailLimit != 3 || limitedResult.Summary.ShownChanges != 3 || limitedResult.Summary.OmittedChanges != result.Summary.TotalChanges-3 || limitedResult.Summary.TotalChanges != result.Summary.TotalChanges {
		t.Fatalf("limited diff summary = %+v changes=%d total=%d", limitedResult.Summary, len(limitedResult.Changes), result.Summary.TotalChanges)
	}
	if limitedResult.Summary.Jobs.Added != result.Summary.Jobs.Added || limitedResult.Summary.Pipelines.Changed != result.Summary.Pipelines.Changed {
		t.Fatalf("limited diff counters changed: %+v vs %+v", limitedResult.Summary, result.Summary)
	}

	limitedText := NewRootCmd()
	limitedTextOut, limitedTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	limitedText.SetOut(limitedTextOut)
	limitedText.SetErr(limitedTextErr)
	limitedText.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--limit", "2"})
	if err := limitedText.Execute(); err != nil {
		t.Fatalf("snapshot diff --limit text: %v\nstderr=%s", err, limitedTextErr.String())
	}
	if !strings.Contains(limitedTextOut.String(), "details: showing=2 omitted=") || strings.Contains(limitedTextOut.String(), "ticket_to_pr.ready_steps") {
		t.Fatalf("limited text output unexpected:\n%s", limitedTextOut.String())
	}

	limitedFormat := NewRootCmd()
	limitedFormatOut, limitedFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	limitedFormat.SetOut(limitedFormatOut)
	limitedFormat.SetErr(limitedFormatErr)
	limitedFormat.SetArgs([]string{
		"snapshot", "diff", beforePath, afterPath,
		"--limit", "2",
		"--format", "{{.Summary.DetailLimit}}:{{len .Changes}}:{{gt .Summary.OmittedChanges 0}}",
	})
	if err := limitedFormat.Execute(); err != nil {
		t.Fatalf("snapshot diff --limit --format: %v\nstderr=%s", err, limitedFormatErr.String())
	}
	if got, want := limitedFormatOut.String(), "2:2:true\n"; got != want {
		t.Fatalf("snapshot diff --limit --format output = %q, want %q", got, want)
	}

	sortedLimited := NewRootCmd()
	sortedLimitedOut, sortedLimitedErr := &bytes.Buffer{}, &bytes.Buffer{}
	sortedLimited.SetOut(sortedLimitedOut)
	sortedLimited.SetErr(sortedLimitedErr)
	sortedLimited.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--sort", "action", "--limit", "4", "--json"})
	if err := sortedLimited.Execute(); err != nil {
		t.Fatalf("snapshot diff --sort action --limit json: %v\nstderr=%s", err, sortedLimitedErr.String())
	}
	var sortedLimitedResult snapshotDiffResult
	if err := json.Unmarshal(sortedLimitedOut.Bytes(), &sortedLimitedResult); err != nil {
		t.Fatalf("decode sorted limited snapshot diff json: %v\nbody=%s", err, sortedLimitedOut.String())
	}
	if sortedLimitedResult.Summary.DetailSort != "action" || sortedLimitedResult.Summary.DetailLimit != 4 || len(sortedLimitedResult.Changes) != 4 || sortedLimitedResult.Summary.TotalChanges != result.Summary.TotalChanges {
		t.Fatalf("sorted limited summary = %+v changes=%d total=%d", sortedLimitedResult.Summary, len(sortedLimitedResult.Changes), result.Summary.TotalChanges)
	}
	for _, change := range sortedLimitedResult.Changes {
		if change.Action != "added" {
			t.Fatalf("sorted limited change action = %q, want added: %+v", change.Action, sortedLimitedResult.Changes)
		}
	}

	sortedText := NewRootCmd()
	sortedTextOut, sortedTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	sortedText.SetOut(sortedTextOut)
	sortedText.SetErr(sortedTextErr)
	sortedText.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--sort", "action", "--limit", "2"})
	if err := sortedText.Execute(); err != nil {
		t.Fatalf("snapshot diff --sort action --limit text: %v\nstderr=%s", err, sortedTextErr.String())
	}
	if !strings.Contains(sortedTextOut.String(), "details: sort=action showing=2 omitted=") {
		t.Fatalf("sorted text output unexpected:\n%s", sortedTextOut.String())
	}

	changedExit := NewRootCmd()
	changedExitOut, changedExitErr := &bytes.Buffer{}, &bytes.Buffer{}
	changedExit.SetOut(changedExitOut)
	changedExit.SetErr(changedExitErr)
	changedExit.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--json", "--exit-code"})
	if err := changedExit.Execute(); err == nil {
		t.Fatalf("snapshot diff --exit-code with changes succeeded")
	}
	var changedExitResult snapshotDiffResult
	if err := json.Unmarshal(changedExitOut.Bytes(), &changedExitResult); err != nil {
		t.Fatalf("decode changed exit-code diff json: %v\nbody=%s\nstderr=%s", err, changedExitOut.String(), changedExitErr.String())
	}
	if changedExitResult.Summary.TotalChanges == 0 {
		t.Fatalf("changed exit-code diff result has no changes: %+v", changedExitResult)
	}

	sameExit := NewRootCmd()
	sameExitOut, sameExitErr := &bytes.Buffer{}, &bytes.Buffer{}
	sameExit.SetOut(sameExitOut)
	sameExit.SetErr(sameExitErr)
	sameExit.SetArgs([]string{"snapshot", "diff", beforePath, beforePath, "--exit-code"})
	if err := sameExit.Execute(); err != nil {
		t.Fatalf("snapshot diff --exit-code identical: %v\nstderr=%s", err, sameExitErr.String())
	}
	if !strings.Contains(sameExitOut.String(), "changes: total=0") || !strings.Contains(sameExitOut.String(), "details: none") {
		t.Fatalf("identical snapshot diff text = %s", sameExitOut.String())
	}

	provenanceOnly := NewRootCmd()
	provenanceOnlyOut, provenanceOnlyErr := &bytes.Buffer{}, &bytes.Buffer{}
	provenanceOnly.SetOut(provenanceOnlyOut)
	provenanceOnly.SetErr(provenanceOnlyErr)
	provenanceOnly.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "provenance", "--json"})
	if err := provenanceOnly.Execute(); err != nil {
		t.Fatalf("snapshot diff provenance section: %v\nstderr=%s", err, provenanceOnlyErr.String())
	}
	var provenanceOnlyResult snapshotDiffResult
	if err := json.Unmarshal(provenanceOnlyOut.Bytes(), &provenanceOnlyResult); err != nil {
		t.Fatalf("decode provenance-only snapshot diff: %v\nbody=%s", err, provenanceOnlyOut.String())
	}
	if provenanceOnlyResult.Summary.TotalChanges != 7 || provenanceOnlyResult.Summary.Provenance.Added != 1 || provenanceOnlyResult.Summary.Provenance.Removed != 1 || provenanceOnlyResult.Summary.Provenance.Changed != 5 || provenanceOnlyResult.Summary.Queue.Added != 0 {
		t.Fatalf("provenance-only diff summary = %+v", provenanceOnlyResult.Summary)
	}
	for _, change := range provenanceOnlyResult.Changes {
		if change.Section != "provenance" {
			t.Fatalf("provenance-only diff included %q change: %+v", change.Section, provenanceOnlyResult.Changes)
		}
	}

	gitOnly := NewRootCmd()
	gitOnlyOut, gitOnlyErr := &bytes.Buffer{}, &bytes.Buffer{}
	gitOnly.SetOut(gitOnlyOut)
	gitOnly.SetErr(gitOnlyErr)
	gitOnly.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "git", "--json"})
	if err := gitOnly.Execute(); err != nil {
		t.Fatalf("snapshot diff git section: %v\nstderr=%s", err, gitOnlyErr.String())
	}
	var gitOnlyResult snapshotDiffResult
	if err := json.Unmarshal(gitOnlyOut.Bytes(), &gitOnlyResult); err != nil {
		t.Fatalf("decode git-only snapshot diff: %v\nbody=%s", err, gitOnlyOut.String())
	}
	if gitOnlyResult.Summary.TotalChanges != 7 || gitOnlyResult.Summary.Git.Changed != 7 || gitOnlyResult.Summary.Provenance.Changed != 0 {
		t.Fatalf("git-only diff summary = %+v", gitOnlyResult.Summary)
	}
	for _, change := range gitOnlyResult.Changes {
		if change.Section != "git" {
			t.Fatalf("git-only diff included %q change: %+v", change.Section, gitOnlyResult.Changes)
		}
	}

	runtimeOnly := NewRootCmd()
	runtimeOnlyOut, runtimeOnlyErr := &bytes.Buffer{}, &bytes.Buffer{}
	runtimeOnly.SetOut(runtimeOnlyOut)
	runtimeOnly.SetErr(runtimeOnlyErr)
	runtimeOnly.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "runtime", "--json"})
	if err := runtimeOnly.Execute(); err != nil {
		t.Fatalf("snapshot diff runtime section: %v\nstderr=%s", err, runtimeOnlyErr.String())
	}
	var runtimeOnlyResult snapshotDiffResult
	if err := json.Unmarshal(runtimeOnlyOut.Bytes(), &runtimeOnlyResult); err != nil {
		t.Fatalf("decode runtime-only snapshot diff: %v\nbody=%s", err, runtimeOnlyOut.String())
	}
	if runtimeOnlyResult.Summary.TotalChanges != 7 || runtimeOnlyResult.Summary.Runtime.Added != 1 || runtimeOnlyResult.Summary.Runtime.Changed != 6 || runtimeOnlyResult.Summary.Git.Changed != 0 {
		t.Fatalf("runtime-only diff summary = %+v", runtimeOnlyResult.Summary)
	}
	for _, change := range runtimeOnlyResult.Changes {
		if change.Section != "runtime" {
			t.Fatalf("runtime-only diff included %q change: %+v", change.Section, runtimeOnlyResult.Changes)
		}
	}

	healthOnly := NewRootCmd()
	healthOnlyOut, healthOnlyErr := &bytes.Buffer{}, &bytes.Buffer{}
	healthOnly.SetOut(healthOnlyOut)
	healthOnly.SetErr(healthOnlyErr)
	healthOnly.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "health", "--json"})
	if err := healthOnly.Execute(); err != nil {
		t.Fatalf("snapshot diff health section: %v\nstderr=%s", err, healthOnlyErr.String())
	}
	var healthOnlyResult snapshotDiffResult
	if err := json.Unmarshal(healthOnlyOut.Bytes(), &healthOnlyResult); err != nil {
		t.Fatalf("decode health-only snapshot diff: %v\nbody=%s", err, healthOnlyOut.String())
	}
	if healthOnlyResult.Summary.TotalChanges != 14 || healthOnlyResult.Summary.Health.Removed != 2 || healthOnlyResult.Summary.Health.Changed != 12 || healthOnlyResult.Summary.Plan.Changed != 0 {
		t.Fatalf("health-only diff summary = %+v", healthOnlyResult.Summary)
	}
	for _, change := range healthOnlyResult.Changes {
		if change.Section != "health" {
			t.Fatalf("health-only diff included %q change: %+v", change.Section, healthOnlyResult.Changes)
		}
	}

	planOnly := NewRootCmd()
	planOnlyOut, planOnlyErr := &bytes.Buffer{}, &bytes.Buffer{}
	planOnly.SetOut(planOnlyOut)
	planOnly.SetErr(planOnlyErr)
	planOnly.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "plan", "--json"})
	if err := planOnly.Execute(); err != nil {
		t.Fatalf("snapshot diff plan section: %v\nstderr=%s", err, planOnlyErr.String())
	}
	var planOnlyResult snapshotDiffResult
	if err := json.Unmarshal(planOnlyOut.Bytes(), &planOnlyResult); err != nil {
		t.Fatalf("decode plan-only snapshot diff: %v\nbody=%s", err, planOnlyOut.String())
	}
	if planOnlyResult.Summary.TotalChanges != 7 || planOnlyResult.Summary.Plan.Changed != 7 || planOnlyResult.Summary.Health.Changed != 0 {
		t.Fatalf("plan-only diff summary = %+v", planOnlyResult.Summary)
	}
	for _, change := range planOnlyResult.Changes {
		if change.Section != "plan" {
			t.Fatalf("plan-only diff included %q change: %+v", change.Section, planOnlyResult.Changes)
		}
	}

	nextOnly := NewRootCmd()
	nextOnlyOut, nextOnlyErr := &bytes.Buffer{}, &bytes.Buffer{}
	nextOnly.SetOut(nextOnlyOut)
	nextOnly.SetErr(nextOnlyErr)
	nextOnly.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "next", "--json"})
	if err := nextOnly.Execute(); err != nil {
		t.Fatalf("snapshot diff next section: %v\nstderr=%s", err, nextOnlyErr.String())
	}
	var nextOnlyResult snapshotDiffResult
	if err := json.Unmarshal(nextOnlyOut.Bytes(), &nextOnlyResult); err != nil {
		t.Fatalf("decode next-only snapshot diff: %v\nbody=%s", err, nextOnlyOut.String())
	}
	if nextOnlyResult.Summary.TotalChanges != 6 || nextOnlyResult.Summary.Next.Added != 1 || nextOnlyResult.Summary.Next.Removed != 2 || nextOnlyResult.Summary.Next.Changed != 3 || nextOnlyResult.Summary.Plan.Changed != 0 {
		t.Fatalf("next-only diff summary = %+v", nextOnlyResult.Summary)
	}
	for _, change := range nextOnlyResult.Changes {
		if change.Section != "next" {
			t.Fatalf("next-only diff included %q change: %+v", change.Section, nextOnlyResult.Changes)
		}
	}

	queueOnly := NewRootCmd()
	queueOnlyOut, queueOnlyErr := &bytes.Buffer{}, &bytes.Buffer{}
	queueOnly.SetOut(queueOnlyOut)
	queueOnly.SetErr(queueOnlyErr)
	queueOnly.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "queue", "--json"})
	if err := queueOnly.Execute(); err != nil {
		t.Fatalf("snapshot diff queue section: %v\nstderr=%s", err, queueOnlyErr.String())
	}
	var queueOnlyResult snapshotDiffResult
	if err := json.Unmarshal(queueOnlyOut.Bytes(), &queueOnlyResult); err != nil {
		t.Fatalf("decode queue-only snapshot diff: %v\nbody=%s", err, queueOnlyOut.String())
	}
	if queueOnlyResult.Summary.TotalChanges != 2 || queueOnlyResult.Summary.Queue.Added != 1 || queueOnlyResult.Summary.Queue.Changed != 1 || queueOnlyResult.Summary.Jobs.Added != 0 {
		t.Fatalf("queue-only diff summary = %+v", queueOnlyResult.Summary)
	}
	for _, change := range queueOnlyResult.Changes {
		if change.Section != "queue" {
			t.Fatalf("queue-only diff included %q change: %+v", change.Section, queueOnlyResult.Changes)
		}
	}

	jobQuarantineOnly := NewRootCmd()
	jobQuarantineOnlyOut, jobQuarantineOnlyErr := &bytes.Buffer{}, &bytes.Buffer{}
	jobQuarantineOnly.SetOut(jobQuarantineOnlyOut)
	jobQuarantineOnly.SetErr(jobQuarantineOnlyErr)
	jobQuarantineOnly.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "job_quarantine", "--json"})
	if err := jobQuarantineOnly.Execute(); err != nil {
		t.Fatalf("snapshot diff job quarantine section: %v\nstderr=%s", err, jobQuarantineOnlyErr.String())
	}
	var jobQuarantineOnlyResult snapshotDiffResult
	if err := json.Unmarshal(jobQuarantineOnlyOut.Bytes(), &jobQuarantineOnlyResult); err != nil {
		t.Fatalf("decode job-quarantine-only snapshot diff: %v\nbody=%s", err, jobQuarantineOnlyOut.String())
	}
	if jobQuarantineOnlyResult.Summary.TotalChanges != 2 || jobQuarantineOnlyResult.Summary.JobQuarantine.Added != 1 || jobQuarantineOnlyResult.Summary.JobQuarantine.Changed != 1 || jobQuarantineOnlyResult.Summary.Queue.Added != 0 {
		t.Fatalf("job-quarantine-only diff summary = %+v", jobQuarantineOnlyResult.Summary)
	}
	for _, change := range jobQuarantineOnlyResult.Changes {
		if change.Section != "job_quarantine" {
			t.Fatalf("job-quarantine-only diff included %q change: %+v", change.Section, jobQuarantineOnlyResult.Changes)
		}
	}

	quarantineAlias := NewRootCmd()
	quarantineAliasOut, quarantineAliasErr := &bytes.Buffer{}, &bytes.Buffer{}
	quarantineAlias.SetOut(quarantineAliasOut)
	quarantineAlias.SetErr(quarantineAliasErr)
	quarantineAlias.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "quarantine", "--json"})
	if err := quarantineAlias.Execute(); err != nil {
		t.Fatalf("snapshot diff quarantine alias section: %v\nstderr=%s", err, quarantineAliasErr.String())
	}
	var quarantineAliasResult snapshotDiffResult
	if err := json.Unmarshal(quarantineAliasOut.Bytes(), &quarantineAliasResult); err != nil {
		t.Fatalf("decode quarantine-alias snapshot diff: %v\nbody=%s", err, quarantineAliasOut.String())
	}
	if quarantineAliasResult.Summary.TotalChanges != 6 ||
		quarantineAliasResult.Summary.JobQuarantine.Added != 1 ||
		quarantineAliasResult.Summary.JobQuarantine.Changed != 1 ||
		quarantineAliasResult.Summary.OutboxQuarantine.Added != 1 ||
		quarantineAliasResult.Summary.OutboxQuarantine.Changed != 1 ||
		quarantineAliasResult.Summary.QueueQuarantine.Added != 1 ||
		quarantineAliasResult.Summary.QueueQuarantine.Changed != 1 ||
		quarantineAliasResult.Summary.Queue.Added != 0 {
		t.Fatalf("quarantine-alias diff summary = %+v", quarantineAliasResult.Summary)
	}
	for _, change := range quarantineAliasResult.Changes {
		switch change.Section {
		case "job_quarantine", "outbox_quarantine", "queue_quarantine":
		default:
			t.Fatalf("quarantine-alias diff included %q change: %+v", change.Section, quarantineAliasResult.Changes)
		}
	}

	inboxOnly := NewRootCmd()
	inboxOnlyOut, inboxOnlyErr := &bytes.Buffer{}, &bytes.Buffer{}
	inboxOnly.SetOut(inboxOnlyOut)
	inboxOnly.SetErr(inboxOnlyErr)
	inboxOnly.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "inbox", "--json"})
	if err := inboxOnly.Execute(); err != nil {
		t.Fatalf("snapshot diff inbox section: %v\nstderr=%s", err, inboxOnlyErr.String())
	}
	var inboxOnlyResult snapshotDiffResult
	if err := json.Unmarshal(inboxOnlyOut.Bytes(), &inboxOnlyResult); err != nil {
		t.Fatalf("decode inbox-only snapshot diff: %v\nbody=%s", err, inboxOnlyOut.String())
	}
	if inboxOnlyResult.Summary.TotalChanges != 2 || inboxOnlyResult.Summary.Inbox.Added != 1 || inboxOnlyResult.Summary.Inbox.Changed != 1 || inboxOnlyResult.Summary.Queue.Added != 0 {
		t.Fatalf("inbox-only diff summary = %+v", inboxOnlyResult.Summary)
	}
	for _, change := range inboxOnlyResult.Changes {
		if change.Section != "inbox" {
			t.Fatalf("inbox-only diff included %q change: %+v", change.Section, inboxOnlyResult.Changes)
		}
	}

	timelineOnly := NewRootCmd()
	timelineOnlyOut, timelineOnlyErr := &bytes.Buffer{}, &bytes.Buffer{}
	timelineOnly.SetOut(timelineOnlyOut)
	timelineOnly.SetErr(timelineOnlyErr)
	timelineOnly.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "timeline", "--json"})
	if err := timelineOnly.Execute(); err != nil {
		t.Fatalf("snapshot diff timeline section: %v\nstderr=%s", err, timelineOnlyErr.String())
	}
	var timelineOnlyResult snapshotDiffResult
	if err := json.Unmarshal(timelineOnlyOut.Bytes(), &timelineOnlyResult); err != nil {
		t.Fatalf("decode timeline-only snapshot diff: %v\nbody=%s", err, timelineOnlyOut.String())
	}
	if timelineOnlyResult.Summary.TotalChanges != 2 || timelineOnlyResult.Summary.Timeline.Added != 1 || timelineOnlyResult.Summary.Timeline.Changed != 1 || timelineOnlyResult.Summary.Events.Added != 0 {
		t.Fatalf("timeline-only diff summary = %+v", timelineOnlyResult.Summary)
	}
	for _, change := range timelineOnlyResult.Changes {
		if change.Section != "timeline" {
			t.Fatalf("timeline-only diff included %q change: %+v", change.Section, timelineOnlyResult.Changes)
		}
	}

	invalidSection := NewRootCmd()
	invalidSectionOut, invalidSectionErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidSection.SetOut(invalidSectionOut)
	invalidSection.SetErr(invalidSectionErr)
	invalidSection.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "telemetry"})
	if err := invalidSection.Execute(); err == nil {
		t.Fatalf("snapshot diff invalid section succeeded")
	}
	if !strings.Contains(invalidSectionErr.String(), "--section must be provenance, git, runtime, health, plan, triage, next") {
		t.Fatalf("invalid section stderr = %q", invalidSectionErr.String())
	}

	formatWithJSON := NewRootCmd()
	formatWithJSONOut, formatWithJSONErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatWithJSON.SetOut(formatWithJSONOut)
	formatWithJSON.SetErr(formatWithJSONErr)
	formatWithJSON.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--format", "{{.Summary.TotalChanges}}", "--json"})
	if err := formatWithJSON.Execute(); err == nil {
		t.Fatalf("snapshot diff --format --json succeeded")
	}
	if !strings.Contains(formatWithJSONErr.String(), "--format cannot be combined with --json") {
		t.Fatalf("format/json stderr = %q", formatWithJSONErr.String())
	}

	jsonWithOutput := NewRootCmd()
	jsonWithOutputOut, jsonWithOutputErr := &bytes.Buffer{}, &bytes.Buffer{}
	jsonWithOutput.SetOut(jsonWithOutputOut)
	jsonWithOutput.SetErr(jsonWithOutputErr)
	jsonWithOutput.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--json", "--output", "diff.json"})
	if err := jsonWithOutput.Execute(); err == nil {
		t.Fatalf("snapshot diff --json --output succeeded")
	}
	if !strings.Contains(jsonWithOutputErr.String(), "choose one of --json or --output") {
		t.Fatalf("json/output stderr = %q", jsonWithOutputErr.String())
	}

	formatWithOutput := NewRootCmd()
	formatWithOutputOut, formatWithOutputErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatWithOutput.SetOut(formatWithOutputOut)
	formatWithOutput.SetErr(formatWithOutputErr)
	formatWithOutput.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--format", "{{.Summary.TotalChanges}}", "--output", "diff.json"})
	if err := formatWithOutput.Execute(); err == nil {
		t.Fatalf("snapshot diff --format --output succeeded")
	}
	if !strings.Contains(formatWithOutputErr.String(), "--format cannot be combined with --output") {
		t.Fatalf("format/output stderr = %q", formatWithOutputErr.String())
	}

	invalidFormat := NewRootCmd()
	invalidFormatOut, invalidFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidFormat.SetOut(invalidFormatOut)
	invalidFormat.SetErr(invalidFormatErr)
	invalidFormat.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--format", "{{"})
	if err := invalidFormat.Execute(); err == nil {
		t.Fatalf("snapshot diff invalid format succeeded")
	}
	if !strings.Contains(invalidFormatErr.String(), "invalid --format template") {
		t.Fatalf("invalid format stderr = %q", invalidFormatErr.String())
	}

	invalidLimit := NewRootCmd()
	invalidLimitOut, invalidLimitErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidLimit.SetOut(invalidLimitOut)
	invalidLimit.SetErr(invalidLimitErr)
	invalidLimit.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--limit", "-1"})
	if err := invalidLimit.Execute(); err == nil {
		t.Fatalf("snapshot diff invalid limit succeeded")
	}
	if !strings.Contains(invalidLimitErr.String(), "--limit must be >= 0") {
		t.Fatalf("invalid limit stderr = %q", invalidLimitErr.String())
	}

	invalidSort := NewRootCmd()
	invalidSortOut, invalidSortErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidSort.SetOut(invalidSortOut)
	invalidSort.SetErr(invalidSortErr)
	invalidSort.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--sort", "age"})
	if err := invalidSort.Execute(); err == nil {
		t.Fatalf("snapshot diff invalid sort succeeded")
	}
	if !strings.Contains(invalidSortErr.String(), "--sort must be section, action, or id") {
		t.Fatalf("invalid sort stderr = %q", invalidSortErr.String())
	}

	invalidAction := NewRootCmd()
	invalidActionOut, invalidActionErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidAction.SetOut(invalidActionOut)
	invalidAction.SetErr(invalidActionErr)
	invalidAction.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--action", "updated"})
	if err := invalidAction.Execute(); err == nil {
		t.Fatalf("snapshot diff invalid action succeeded")
	}
	if !strings.Contains(invalidActionErr.String(), "--action must be added, removed, changed, or all") {
		t.Fatalf("invalid action stderr = %q", invalidActionErr.String())
	}

	invalidSummaryLimit := NewRootCmd()
	invalidSummaryLimitOut, invalidSummaryLimitErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidSummaryLimit.SetOut(invalidSummaryLimitOut)
	invalidSummaryLimit.SetErr(invalidSummaryLimitErr)
	invalidSummaryLimit.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--summary", "--limit", "1"})
	if err := invalidSummaryLimit.Execute(); err == nil {
		t.Fatalf("snapshot diff summary with limit succeeded")
	}
	if !strings.Contains(invalidSummaryLimitErr.String(), "--summary cannot be combined with --limit") {
		t.Fatalf("invalid summary/limit stderr = %q", invalidSummaryLimitErr.String())
	}

	invalidSummarySort := NewRootCmd()
	invalidSummarySortOut, invalidSummarySortErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidSummarySort.SetOut(invalidSummarySortOut)
	invalidSummarySort.SetErr(invalidSummarySortErr)
	invalidSummarySort.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--summary", "--sort", "action"})
	if err := invalidSummarySort.Execute(); err == nil {
		t.Fatalf("snapshot diff summary with sort succeeded")
	}
	if !strings.Contains(invalidSummarySortErr.String(), "--summary cannot be combined with --sort") {
		t.Fatalf("invalid summary/sort stderr = %q", invalidSummarySortErr.String())
	}

	invalidSummaryFormat := NewRootCmd()
	invalidSummaryFormatOut, invalidSummaryFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidSummaryFormat.SetOut(invalidSummaryFormatOut)
	invalidSummaryFormat.SetErr(invalidSummaryFormatErr)
	invalidSummaryFormat.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--summary", "--format", "{{.Summary.TotalChanges}}"})
	if err := invalidSummaryFormat.Execute(); err == nil {
		t.Fatalf("snapshot diff summary with format succeeded")
	}
	if !strings.Contains(invalidSummaryFormatErr.String(), "--format cannot be combined with --summary") {
		t.Fatalf("invalid summary/format stderr = %q", invalidSummaryFormatErr.String())
	}
}

func TestSnapshotDiffCommandComparesCurrentSnapshot(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	beforePath := filepath.Join(tmp, "before.json")
	afterPath := filepath.Join(tmp, "after.json")
	empty := snapshotDiffInput{
		CapturedAt: "2026-06-25T12:00:00Z",
		Repo:       tmp,
		Provenance: newSnapshotProvenance("agent-team snapshot", "global", "", snapshotProvenanceOptions{
			Events:        intValuePtr(0),
			ScheduleLimit: intValuePtr(0),
			Redacted:      true,
		}),
	}
	writeSnapshotDiffInput(t, beforePath, empty)
	writeSnapshotDiffInput(t, afterPath, empty)

	j, err := job.New("SQU-260", "worker", "compare current snapshot", time.Now().UTC())
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	j.Pipeline = "ticket_to_pr"
	j.Instance = "worker-squ-260"
	j.Status = job.StatusQueued
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	timelineBase := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	for _, ev := range []job.Event{
		{
			TS:       timelineBase.Add(time.Minute),
			JobID:    j.ID,
			Type:     "created",
			Status:   job.StatusQueued,
			Instance: j.Instance,
			Actor:    "test",
			Message:  "queued",
		},
		{
			TS:       timelineBase.Add(2 * time.Minute),
			JobID:    j.ID,
			Type:     "note",
			Status:   job.StatusQueued,
			Instance: j.Instance,
			Actor:    "test",
			Message:  "newer",
		},
	} {
		if err := job.AppendEvent(teamDir, &ev); err != nil {
			t.Fatalf("append timeline event: %v", err)
		}
	}

	currentAfter := NewRootCmd()
	currentAfterOut, currentAfterErr := &bytes.Buffer{}, &bytes.Buffer{}
	currentAfter.SetOut(currentAfterOut)
	currentAfter.SetErr(currentAfterErr)
	currentAfter.SetArgs([]string{"--repo", tmp, "snapshot", "diff", beforePath, "--current-after", "--section", "jobs", "--events", "0", "--intake-deliveries", "0", "--schedule-limit", "0", "--json"})
	if err := currentAfter.Execute(); err != nil {
		t.Fatalf("snapshot diff --current-after: %v\nstderr=%s", err, currentAfterErr.String())
	}
	var afterResult snapshotDiffResult
	if err := json.Unmarshal(currentAfterOut.Bytes(), &afterResult); err != nil {
		t.Fatalf("decode current-after diff: %v\nbody=%s", err, currentAfterOut.String())
	}
	if afterResult.Before.Path != beforePath || afterResult.After.Path != "<current>" || afterResult.After.Kind != "repo" {
		t.Fatalf("current-after metadata = %+v -> %+v", afterResult.Before, afterResult.After)
	}
	if afterResult.Summary.TotalChanges != 1 || afterResult.Summary.Jobs.Added != 1 || !hasSnapshotDiffChange(afterResult.Changes, "jobs", "squ-260", "added") {
		t.Fatalf("current-after result = %+v changes=%+v", afterResult.Summary, afterResult.Changes)
	}

	currentBefore := NewRootCmd()
	currentBeforeOut, currentBeforeErr := &bytes.Buffer{}, &bytes.Buffer{}
	currentBefore.SetOut(currentBeforeOut)
	currentBefore.SetErr(currentBeforeErr)
	currentBefore.SetArgs([]string{"--repo", tmp, "snapshot", "diff", afterPath, "--current-before", "--section", "jobs", "--events", "0", "--intake-deliveries", "0", "--schedule-limit", "0", "--json"})
	if err := currentBefore.Execute(); err != nil {
		t.Fatalf("snapshot diff --current-before: %v\nstderr=%s", err, currentBeforeErr.String())
	}
	var beforeResult snapshotDiffResult
	if err := json.Unmarshal(currentBeforeOut.Bytes(), &beforeResult); err != nil {
		t.Fatalf("decode current-before diff: %v\nbody=%s", err, currentBeforeOut.String())
	}
	if beforeResult.Before.Path != "<current>" || beforeResult.After.Path != afterPath {
		t.Fatalf("current-before metadata = %+v -> %+v", beforeResult.Before, beforeResult.After)
	}
	if beforeResult.Summary.TotalChanges != 1 || beforeResult.Summary.Jobs.Removed != 1 || !hasSnapshotDiffChange(beforeResult.Changes, "jobs", "squ-260", "removed") {
		t.Fatalf("current-before result = %+v changes=%+v", beforeResult.Summary, beforeResult.Changes)
	}

	other, err := job.New("OPS-260", "worker", "outside pipeline scope", time.Now().UTC())
	if err != nil {
		t.Fatalf("new other job: %v", err)
	}
	other.Pipeline = "platform_work"
	other.Status = job.StatusQueued
	if err := job.Write(teamDir, other); err != nil {
		t.Fatalf("write other job: %v", err)
	}
	pipelineBeforePath := filepath.Join(tmp, "pipeline-before.json")
	writeSnapshotDiffInput(t, pipelineBeforePath, snapshotDiffInput{
		CapturedAt: "2026-06-25T12:00:00Z",
		Repo:       tmp,
		Pipeline:   "ticket_to_pr",
		Provenance: newSnapshotProvenance("agent-team pipeline snapshot", "pipeline", "ticket_to_pr", snapshotProvenanceOptions{
			Redacted: true,
		}),
	})
	pipelineCurrent := NewRootCmd()
	pipelineCurrentOut, pipelineCurrentErr := &bytes.Buffer{}, &bytes.Buffer{}
	pipelineCurrent.SetOut(pipelineCurrentOut)
	pipelineCurrent.SetErr(pipelineCurrentErr)
	pipelineCurrent.SetArgs([]string{"--repo", tmp, "snapshot", "diff", pipelineBeforePath, "--current-after", "--section", "jobs", "--events", "0", "--intake-deliveries", "0", "--schedule-limit", "0", "--json"})
	if err := pipelineCurrent.Execute(); err != nil {
		t.Fatalf("pipeline snapshot diff --current-after: %v\nstderr=%s", err, pipelineCurrentErr.String())
	}
	var pipelineResult snapshotDiffResult
	if err := json.Unmarshal(pipelineCurrentOut.Bytes(), &pipelineResult); err != nil {
		t.Fatalf("decode pipeline current diff: %v\nbody=%s", err, pipelineCurrentOut.String())
	}
	if pipelineResult.Before.Kind != "pipeline" || pipelineResult.Before.Scope != "ticket_to_pr" || pipelineResult.After.Kind != "pipeline" || pipelineResult.After.Scope != "ticket_to_pr" {
		t.Fatalf("pipeline current metadata = %+v -> %+v", pipelineResult.Before, pipelineResult.After)
	}
	if pipelineResult.Summary.TotalChanges != 1 || pipelineResult.Summary.Jobs.Added != 1 || !hasSnapshotDiffChange(pipelineResult.Changes, "jobs", "squ-260", "added") || hasSnapshotDiffChange(pipelineResult.Changes, "jobs", "ops-260", "added") {
		t.Fatalf("pipeline current result = %+v changes=%+v", pipelineResult.Summary, pipelineResult.Changes)
	}

	pipelineTimeline := NewRootCmd()
	pipelineTimelineOut, pipelineTimelineErr := &bytes.Buffer{}, &bytes.Buffer{}
	pipelineTimeline.SetOut(pipelineTimelineOut)
	pipelineTimeline.SetErr(pipelineTimelineErr)
	pipelineTimeline.SetArgs([]string{"--repo", tmp, "snapshot", "diff", pipelineBeforePath, "--current-after", "--section", "timeline", "--timeline", "1", "--json"})
	if err := pipelineTimeline.Execute(); err != nil {
		t.Fatalf("pipeline snapshot diff --current-after --timeline: %v\nstderr=%s", err, pipelineTimelineErr.String())
	}
	var pipelineTimelineResult snapshotDiffResult
	if err := json.Unmarshal(pipelineTimelineOut.Bytes(), &pipelineTimelineResult); err != nil {
		t.Fatalf("decode pipeline timeline current diff: %v\nbody=%s", err, pipelineTimelineOut.String())
	}
	newerTimelineID := "job|squ-260|2026-06-25T12:02:00Z|note|worker-squ-260"
	olderTimelineID := "job|squ-260|2026-06-25T12:01:00Z|created|worker-squ-260"
	if pipelineTimelineResult.Summary.TotalChanges != 1 || pipelineTimelineResult.Summary.Timeline.Added != 1 || !hasSnapshotDiffChange(pipelineTimelineResult.Changes, "timeline", newerTimelineID, "added") || hasSnapshotDiffChange(pipelineTimelineResult.Changes, "timeline", olderTimelineID, "added") {
		t.Fatalf("pipeline current timeline result = %+v changes=%+v", pipelineTimelineResult.Summary, pipelineTimelineResult.Changes)
	}

	both := NewRootCmd()
	bothOut, bothErr := &bytes.Buffer{}, &bytes.Buffer{}
	both.SetOut(bothOut)
	both.SetErr(bothErr)
	both.SetArgs([]string{"snapshot", "diff", beforePath, "--current-before", "--current-after"})
	if err := both.Execute(); err == nil {
		t.Fatalf("snapshot diff both current flags succeeded")
	}
	if !strings.Contains(bothErr.String(), "choose one of --current-after or --current-before") {
		t.Fatalf("both current flags stderr = %q", bothErr.String())
	}

	wrongArgs := NewRootCmd()
	wrongArgsOut, wrongArgsErr := &bytes.Buffer{}, &bytes.Buffer{}
	wrongArgs.SetOut(wrongArgsOut)
	wrongArgs.SetErr(wrongArgsErr)
	wrongArgs.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--current-after"})
	if err := wrongArgs.Execute(); err == nil {
		t.Fatalf("snapshot diff current-after with two files succeeded")
	}
	if !strings.Contains(wrongArgsErr.String(), "pass exactly one snapshot file") {
		t.Fatalf("wrong arg count stderr = %q", wrongArgsErr.String())
	}

	unusedCurrentOption := NewRootCmd()
	unusedCurrentOptionOut, unusedCurrentOptionErr := &bytes.Buffer{}, &bytes.Buffer{}
	unusedCurrentOption.SetOut(unusedCurrentOptionOut)
	unusedCurrentOption.SetErr(unusedCurrentOptionErr)
	unusedCurrentOption.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--events", "0"})
	if err := unusedCurrentOption.Execute(); err == nil {
		t.Fatalf("snapshot diff unused current option succeeded")
	}
	if !strings.Contains(unusedCurrentOptionErr.String(), "current snapshot options require") {
		t.Fatalf("unused current option stderr = %q", unusedCurrentOptionErr.String())
	}

	unusedTimelineOption := NewRootCmd()
	unusedTimelineOptionOut, unusedTimelineOptionErr := &bytes.Buffer{}, &bytes.Buffer{}
	unusedTimelineOption.SetOut(unusedTimelineOptionOut)
	unusedTimelineOption.SetErr(unusedTimelineOptionErr)
	unusedTimelineOption.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--timeline", "1"})
	if err := unusedTimelineOption.Execute(); err == nil {
		t.Fatalf("snapshot diff unused timeline option succeeded")
	}
	if !strings.Contains(unusedTimelineOptionErr.String(), "current snapshot options require") {
		t.Fatalf("unused timeline option stderr = %q", unusedTimelineOptionErr.String())
	}
}

func TestSnapshotDiffCommandReportsTriageChanges(t *testing.T) {
	tmp := t.TempDir()
	beforePath := filepath.Join(tmp, "triage-before.json")
	afterPath := filepath.Join(tmp, "triage-after.json")
	now := time.Date(2026, 6, 25, 9, 0, 0, 0, time.UTC)
	before := snapshotDiffInput{
		CapturedAt: now.Format(time.RFC3339),
		Repo:       "/repo",
		JobTriage: &jobTriageSnapshot{
			Summary: jobSummary{Total: 3, Running: 2, Blocked: 1},
			Queue:   queueSummary{Total: 1, Dead: 1},
			Attention: []jobTriageItem{
				{
					JobID:     "squ-201",
					Ticket:    "SQU-201",
					Status:    job.StatusRunning,
					Severity:  "warning",
					Reasons:   []string{"queue_dead"},
					Actions:   []string{"agent-team job queue retry squ-201 --all --dry-run"},
					Target:    "worker",
					Instance:  "worker-squ-201",
					Pipeline:  "ticket_to_pr",
					QueueDead: 1,
					QueueIDs:  []string{"q-201"},
				},
				{
					JobID:     "squ-202",
					Ticket:    "SQU-202",
					Status:    job.StatusBlocked,
					Severity:  "critical",
					Reasons:   []string{"blocked_status"},
					StepID:    "review",
					StepState: "blocked",
					Pipeline:  "ticket_to_pr",
					Message:   "operator input required",
				},
			},
			ReadySteps: []jobReadyRow{{
				JobID:      "squ-203",
				Ticket:     "SQU-203",
				Pipeline:   "ticket_to_pr",
				JobStatus:  job.StatusQueued,
				State:      "ready",
				StepID:     "implement",
				Target:     "worker",
				StepStatus: job.StatusQueued,
				Actions:    []string{"agent-team job advance squ-203 --dry-run"},
			}},
			StatusPreviews: []jobStatusReconcileResult{{
				JobID:     "squ-202",
				Instance:  "worker-squ-202",
				Phase:     "blocked",
				MatchedBy: "status",
				Before:    job.StatusRunning,
				After:     job.StatusBlocked,
				Message:   "blocked",
				Changed:   true,
				DryRun:    true,
			}},
		},
	}
	after := snapshotDiffInput{
		CapturedAt: now.Add(5 * time.Minute).Format(time.RFC3339),
		Repo:       "/repo",
		JobTriage: &jobTriageSnapshot{
			Summary: jobSummary{Total: 3, Running: 1, Blocked: 1, Done: 1},
			Queue:   queueSummary{},
			Attention: []jobTriageItem{
				{
					JobID:     "squ-202",
					Ticket:    "SQU-202",
					Status:    job.StatusBlocked,
					Severity:  "warning",
					Reasons:   []string{"blocked_status"},
					StepID:    "review",
					StepState: "blocked",
					Pipeline:  "ticket_to_pr",
					Message:   "still waiting",
				},
				{
					JobID:    "squ-204",
					Ticket:   "SQU-204",
					Status:   job.StatusRunning,
					Severity: "warning",
					Reasons:  []string{"stale_running"},
					Target:   "worker",
					Instance: "worker-squ-204",
					Actions:  []string{"agent-team job timeout squ-204 --dry-run"},
				},
			},
			ReadySteps: []jobReadyRow{{
				JobID:      "squ-205",
				Ticket:     "SQU-205",
				Pipeline:   "ticket_to_pr",
				JobStatus:  job.StatusQueued,
				State:      "ready",
				StepID:     "review",
				Target:     "manager",
				StepStatus: job.StatusQueued,
				Actions:    []string{"agent-team job advance squ-205 --dry-run"},
			}},
		},
	}
	writeSnapshotDiffInput(t, beforePath, before)
	writeSnapshotDiffInput(t, afterPath, after)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "triage", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("snapshot diff triage section: %v\nstderr=%s", err, stderr.String())
	}
	var result snapshotDiffResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode triage snapshot diff: %v\nbody=%s", err, out.String())
	}
	if result.Summary.TotalChanges == 0 || result.Summary.Triage.Added == 0 || result.Summary.Triage.Removed == 0 || result.Summary.Triage.Changed == 0 {
		t.Fatalf("triage diff summary = %+v", result.Summary)
	}
	if result.Summary.Jobs.Added != 0 || result.Summary.Next.Added != 0 {
		t.Fatalf("triage-only diff leaked other sections: %+v", result.Summary)
	}
	for _, change := range result.Changes {
		if change.Section != "triage" {
			t.Fatalf("triage-only diff included %q change: %+v", change.Section, result.Changes)
		}
	}
	for _, want := range []struct {
		id     string
		action string
	}{
		{"attention/squ-201", "removed"},
		{"attention/squ-202/step/review", "changed"},
		{"attention/squ-204", "added"},
		{"ready/squ-203/step/implement", "removed"},
		{"ready/squ-205/step/review", "added"},
		{"status_preview/squ-202/worker-squ-202", "removed"},
		{"attention.reason.queue_dead", "removed"},
		{"attention.reason.stale_running", "added"},
	} {
		if !hasSnapshotDiffChange(result.Changes, "triage", want.id, want.action) {
			t.Fatalf("missing triage change %s %s: %+v", want.id, want.action, result.Changes)
		}
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "triage"})
	if err := text.Execute(); err != nil {
		t.Fatalf("snapshot diff triage text: %v\nstderr=%s", err, textErr.String())
	}
	if !strings.Contains(textOut.String(), "triage: added=") || !strings.Contains(textOut.String(), "attention/squ-204") {
		t.Fatalf("triage text missing expected details:\n%s", textOut.String())
	}
}

func TestSnapshotDiffCommandReportsJobSnapshotChanges(t *testing.T) {
	tmp := t.TempDir()
	beforePath := filepath.Join(tmp, "job-before.json")
	afterPath := filepath.Join(tmp, "job-after.json")
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	exitCode := 0
	before := jobSnapshotResult{
		Version:    Version,
		CapturedAt: now.Format(time.RFC3339),
		Repo:       "/repo",
		Provenance: newSnapshotProvenance("agent-team job snapshot", "job", "squ-160", snapshotProvenanceOptions{
			Events:   intValuePtr(-1),
			Tail:     intValuePtr(0),
			Redacted: true,
		}),
		Job: &job.Job{
			ID:        "squ-160",
			Ticket:    "SQU-160",
			Target:    "worker",
			Instance:  "worker-squ-160",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-time.Hour),
			UpdatedAt: now,
			Steps: []job.Step{{
				ID:       "implement",
				Target:   "worker",
				Instance: "worker-squ-160",
				Status:   job.StatusRunning,
			}},
		},
		Instance: "worker-squ-160",
		Runtime: &inspectRuntimeJSON{
			Lifecycle: "running",
			Agent:     "worker",
			Runtime:   "codex",
			Job:       "squ-160",
			PID:       1234,
		},
		State:  &jobSnapshotState{Path: "state/worker-squ-160", Exists: true},
		Status: &inspectStatusJSON{Phase: "working", Description: "implementing", Stale: false},
		Queue: []*daemon.QueueItem{{
			ID:        "q-160",
			State:     daemon.QueueStatePending,
			EventType: "agent.dispatch",
		}},
		Inbox: []inboxSummaryRow{{
			Instance: "worker-squ-160",
			Total:    1,
			Unread:   1,
			LatestID: "msg-160",
		}},
		Actions: []string{
			"agent-team inbox show worker-squ-160 --unread",
			"agent-team inspect worker-squ-160",
		},
		JobEvents: []job.Event{{
			TS:     now.Add(-time.Hour),
			JobID:  "squ-160",
			Type:   "created",
			Status: job.StatusQueued,
			Actor:  "test",
		}},
		LifecycleEvents: []daemon.LifecycleEvent{{
			ID:       "dispatch-160",
			TS:       now.Add(-30 * time.Minute),
			Action:   "dispatch",
			Instance: "worker-squ-160",
			Agent:    "worker",
			Job:      "squ-160",
			Status:   daemon.StatusRunning,
		}},
	}
	after := before
	after.CapturedAt = now.Add(5 * time.Minute).Format(time.RFC3339)
	after.Job = &job.Job{
		ID:         "squ-160",
		Ticket:     "SQU-160",
		Target:     "worker",
		Instance:   "worker-squ-160",
		Pipeline:   "ticket_to_pr",
		Status:     job.StatusDone,
		LastEvent:  "instance_exited",
		LastStatus: "done",
		CreatedAt:  now.Add(-time.Hour),
		UpdatedAt:  now.Add(5 * time.Minute),
		Steps: []job.Step{{
			ID:         "implement",
			Target:     "worker",
			Instance:   "worker-squ-160",
			Status:     job.StatusDone,
			FinishedAt: now.Add(5 * time.Minute),
		}},
	}
	after.Runtime = &inspectRuntimeJSON{
		Lifecycle: "exited",
		Agent:     "worker",
		Runtime:   "codex",
		Job:       "squ-160",
		PID:       1234,
		ExitCode:  &exitCode,
	}
	after.Status = &inspectStatusJSON{Phase: "done", Description: "complete", Stale: false}
	after.Queue = []*daemon.QueueItem{{
		ID:        "q-160",
		State:     daemon.QueueStateDead,
		EventType: "agent.dispatch",
	}}
	after.Inbox = nil
	after.Actions = []string{
		"agent-team inspect worker-squ-160",
		"agent-team job logs squ-160 --last-message",
	}
	after.JobEvents = append(after.JobEvents, job.Event{
		TS:      now.Add(5 * time.Minute),
		JobID:   "squ-160",
		Type:    "instance_exited",
		Status:  job.StatusDone,
		Actor:   "daemon",
		Message: "done",
	})
	after.LifecycleEvents = append(after.LifecycleEvents, daemon.LifecycleEvent{
		ID:       "exit-160",
		TS:       now.Add(5 * time.Minute),
		Action:   "exit",
		Instance: "worker-squ-160",
		Agent:    "worker",
		Job:      "squ-160",
		Status:   daemon.StatusExited,
		ExitCode: &exitCode,
	})
	writeSnapshotDiffJSON(t, beforePath, before)
	writeSnapshotDiffJSON(t, afterPath, after)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job snapshot diff: %v\nstderr=%s", err, stderr.String())
	}
	var result snapshotDiffResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode job snapshot diff: %v\nbody=%s", err, out.String())
	}
	if result.Before.Kind != "job" || result.Before.Scope != "squ-160" || result.After.Kind != "job" || result.After.Scope != "squ-160" {
		t.Fatalf("job snapshot diff metadata = %+v -> %+v", result.Before, result.After)
	}
	if result.Summary.Jobs.Changed != 3 {
		t.Fatalf("job snapshot job counters = %+v", result.Summary.Jobs)
	}
	if result.Summary.Runtime.Added != 1 || result.Summary.Runtime.Changed != 1 {
		t.Fatalf("job snapshot runtime counters = %+v", result.Summary.Runtime)
	}
	if result.Summary.Queue.Changed != 1 {
		t.Fatalf("job snapshot queue counters = %+v", result.Summary.Queue)
	}
	if result.Summary.Inbox.Removed != 1 {
		t.Fatalf("job snapshot inbox counters = %+v", result.Summary.Inbox)
	}
	if result.Summary.Next.Added != 1 || result.Summary.Next.Removed != 1 {
		t.Fatalf("job snapshot next counters = %+v", result.Summary.Next)
	}
	if result.Summary.Events.Added != 2 {
		t.Fatalf("job snapshot event counters = %+v", result.Summary.Events)
	}
	if !hasSnapshotDiffChange(result.Changes, "jobs", "squ-160", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "jobs", "squ-160/status", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "runtime", "lifecycle", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "runtime", "exit_code", "added") ||
		!hasSnapshotDiffChange(result.Changes, "queue", "q-160", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "inbox", "worker-squ-160", "removed") ||
		!hasSnapshotDiffChange(result.Changes, "next", "action/agent-team inbox show worker-squ-160 --unread", "removed") ||
		!hasSnapshotDiffChange(result.Changes, "next", "action/agent-team job logs squ-160 --last-message", "added") ||
		!hasSnapshotDiffChange(result.Changes, "events", "lifecycle/exit-160", "added") {
		t.Fatalf("missing expected job snapshot changes: %+v", result.Changes)
	}

	nextOnly := NewRootCmd()
	nextOnlyOut, nextOnlyErr := &bytes.Buffer{}, &bytes.Buffer{}
	nextOnly.SetOut(nextOnlyOut)
	nextOnly.SetErr(nextOnlyErr)
	nextOnly.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "next", "--json"})
	if err := nextOnly.Execute(); err != nil {
		t.Fatalf("job snapshot next diff: %v\nstderr=%s", err, nextOnlyErr.String())
	}
	var nextOnlyResult snapshotDiffResult
	if err := json.Unmarshal(nextOnlyOut.Bytes(), &nextOnlyResult); err != nil {
		t.Fatalf("decode job snapshot next diff: %v\nbody=%s", err, nextOnlyOut.String())
	}
	if nextOnlyResult.Summary.TotalChanges != 2 || nextOnlyResult.Summary.Next.Added != 1 || nextOnlyResult.Summary.Next.Removed != 1 || nextOnlyResult.Summary.Jobs.Changed != 0 {
		t.Fatalf("job snapshot next-only summary = %+v", nextOnlyResult.Summary)
	}
}

func TestSnapshotIncludesGitMetadata(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	initInto(t, tmp)
	runGitForJobTest(t, tmp, "init")
	runGitForJobTest(t, tmp, "config", "user.email", "agent-team@example.test")
	runGitForJobTest(t, tmp, "config", "user.name", "Agent Team")
	runGitForJobTest(t, tmp, "add", ".")
	runGitForJobTest(t, tmp, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(tmp, "uncommitted.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"snapshot", "--target", tmp, "--events", "0", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("snapshot git metadata: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot snapshotResult
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v\nbody=%s", err, out.String())
	}
	if snapshot.Git == nil || snapshot.Git.Branch == "" || snapshot.Git.Commit == "" {
		t.Fatalf("git metadata = %+v, want branch and commit", snapshot.Git)
	}
	if !snapshot.Git.Dirty || snapshot.Git.Changes == 0 {
		t.Fatalf("git metadata = %+v, want dirty working tree", snapshot.Git)
	}
}

func writeSnapshotDiffInput(t *testing.T, path string, input snapshotDiffInput) {
	t.Helper()
	writeSnapshotDiffJSON(t, path, input)
}

func writeSnapshotDiffJSON(t *testing.T, path string, input any) {
	t.Helper()
	body, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		t.Fatalf("marshal snapshot diff input: %v", err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func hasSnapshotDiffChange(changes []snapshotDiffChange, section, id, action string) bool {
	for _, change := range changes {
		if change.Section == section && change.ID == id && change.Action == action {
			return true
		}
	}
	return false
}

func TestSnapshotIntakeSummaryUsesFullLedger(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	for _, delivery := range []intakeDelivery{
		{
			ID:         "older-error",
			Time:       now.Add(-time.Hour),
			Provider:   "linear",
			RequestID:  "linear-delivery-505",
			Status:     intakeDeliveryStatusError,
			HTTPStatus: 503,
			EventType:  "ticket.created",
			Payload:    map[string]any{"source": "linear", "ticket": "SQU-504"},
			Ticket:     "SQU-504",
			Error:      "daemon is not running",
		},
		{
			ID:         "newer-ok",
			Time:       now,
			Provider:   "linear",
			RequestID:  "linear-delivery-505",
			Status:     intakeDeliveryStatusOK,
			HTTPStatus: 200,
			EventType:  "ticket.created",
			Ticket:     "SQU-505",
		},
	} {
		if err := appendIntakeDelivery(teamDir, delivery); err != nil {
			t.Fatalf("append %s: %v", delivery.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"snapshot", "--target", tmp, "--events", "0", "--intake-deliveries", "1", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("snapshot intake limit: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot snapshotResult
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v\nbody=%s", err, out.String())
	}
	if len(snapshot.Intake) != 1 || snapshot.Intake[0].ID != "newer-ok" {
		t.Fatalf("limited intake rows = %+v", snapshot.Intake)
	}
	if snapshot.IntakeSummary == nil || snapshot.IntakeSummary.Deliveries != 2 || snapshot.IntakeSummary.Errors != 1 || snapshot.IntakeSummary.Replayable != 1 || snapshot.IntakeSummary.LatestErrorID != "older-error" {
		t.Fatalf("intake summary = %+v", snapshot.IntakeSummary)
	}
	if len(snapshot.IntakeDuplicates) != 1 || snapshot.IntakeDuplicates[0].RequestID != "linear-delivery-505" || snapshot.IntakeDuplicates[0].Count != 2 {
		t.Fatalf("intake duplicates = %+v", snapshot.IntakeDuplicates)
	}
}

func TestSnapshotSummaryIncludesJobTriage(t *testing.T) {
	now := time.Now().UTC()
	snapshot := &snapshotResult{
		CapturedAt: now.Format(time.RFC3339),
		Repo:       "/repo",
		Provenance: newSnapshotProvenance("agent-team snapshot", "global", "", snapshotProvenanceOptions{
			Redacted: true,
		}),
		Git:      &snapshotGitInfo{Branch: "main", Commit: "abcdef123456", Dirty: true, Changes: 2, Ahead: 1},
		Redacted: true,
		Jobs: []*job.Job{
			{ID: "squ-601", Ticket: "SQU-601", Target: "worker", Status: job.StatusFailed, CreatedAt: now, UpdatedAt: now},
		},
		JobTriage: &jobTriageSnapshot{
			Summary: jobSummary{Total: 1, Failed: 1},
			Attention: []jobTriageItem{{
				JobID:    "squ-601",
				Ticket:   "SQU-601",
				Status:   job.StatusFailed,
				Severity: "critical",
				Reasons:  []string{"failed"},
			}},
		},
		JobStatus: []jobStatusReconcileResult{{
			JobID:   "squ-601",
			Changed: true,
		}},
		PipelineStatus: []pipelineStatusRow{{
			Pipeline:   "ticket_to_pr",
			Jobs:       1,
			ReadySteps: 1,
		}},
		PipelineAdvance: []pipelineAdvanceResult{{
			JobID:  "squ-601",
			Action: "would_advance",
			Preview: &jobAdvancePreview{
				Dispatch: &dispatchRoutePreview{
					Preview: &eventPublishPreview{},
				},
			},
		}},
		TeamsDoctor: &allTeamDoctorResult{
			OK:    false,
			Teams: []teamDoctorResult{{Team: teamInfo{Name: "delivery"}, OK: false}},
			Problems: []teamDoctorFinding{{
				Code:    "pipeline_target_outside_team",
				Team:    "delivery",
				Message: "pipeline target outside team",
			}},
			Warnings: []teamDoctorFinding{{
				Code:    "schedule_routes_outside_team",
				Team:    "delivery",
				Message: "schedule routes outside team",
			}},
		},
		TeamDoctor: &teamDoctorResult{
			Team: teamInfo{Name: "delivery"},
			OK:   false,
			Problems: []teamDoctorFinding{{
				Code:    "pipeline_target_outside_team",
				Message: "pipeline target outside team",
			}},
		},
		QueueSummary:            &queueSummary{Total: 1, Pending: 1, Quarantined: 1, QuarantineRestorable: 1},
		JobQuarantineSummary:    &jobQuarantineSummary{Quarantined: 1, Restorable: 1},
		OutboxQuarantineSummary: &outboxQuarantineSummary{Quarantined: 1, Restorable: 1},
		InboxSummary:            &overviewInboxSummary{Instances: 2, Total: 3, Unread: 1, UnreadInstances: 1},
		IntakeSummary: &overviewIntakeSummary{
			Deliveries: 1,
			Errors:     1,
			Recovered:  0,
			Replayable: 1,
		},
		IntakeDuplicates: []intakeDuplicateRequest{{
			Provider:  "github",
			RequestID: "delivery-1",
			Count:     2,
		}},
	}

	var out bytes.Buffer
	renderSnapshotSummary(&out, snapshot)
	for _, want := range []string{"command: agent-team snapshot scope=global", "git: branch=main commit=abcdef123456 dirty=yes changes=2 ahead=1 behind=0", "jobs: total=1", "job triage: attention=1 ready_steps=0", "job quarantine: quarantined=1 restorable=1 unrestorable=0", "job status: previews=1 changes=1", "pipeline status: pipelines=1 jobs=1 ready_steps=1 manual_gates=0 stale_running_steps=0 failed_steps=0", "pipeline advance: ready=1 route_previews=1", "teams doctor: teams=1 problems=1 warnings=1", "team doctor: problems=1 warnings=0", "queue: total=1 pending=1 dead=0 delayed=0 attempts=0 quarantined=1 restorable=1 unrestorable=0", "outbox quarantine: quarantined=1 restorable=1 unrestorable=0", "inbox: instances=2 total=3 unread=1 unread_instances=1", "intake: deliveries=1 errors=1 recovered=0 replayable=1 duplicate_request_ids=1"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("summary missing %q:\n%s", want, out.String())
		}
	}
}

func TestSnapshotIncludesTeamDoctorFindings(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.worker]
agent = "worker"

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[instances.other]
agent = "other"

[[instances.other.triggers]]
event = "agent.dispatch"
match.target = "other"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "other"

[teams.delivery]
instances = ["worker"]
pipelines = ["ticket_to_pr"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"snapshot", "--target", tmp, "--events", "0", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("snapshot team doctor: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot snapshotResult
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v\nbody=%s", err, out.String())
	}
	if snapshot.TeamsDoctor == nil || snapshot.TeamsDoctor.OK || !hasTeamDoctorFindingForTeam(snapshot.TeamsDoctor.Problems, "delivery", "pipeline_target_outside_team") {
		t.Fatalf("teams doctor = %+v", snapshot.TeamsDoctor)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"snapshot", "--target", tmp, "--events", "0"})
	if err := text.Execute(); err != nil {
		t.Fatalf("snapshot team doctor text: %v\nstderr=%s", err, textErr.String())
	}
	if !strings.Contains(textOut.String(), "teams doctor: teams=1 problems=1 warnings=0") {
		t.Fatalf("snapshot text missing teams doctor summary:\n%s", textOut.String())
	}
}

func TestSnapshotCommandWritesOutputFile(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	outPath := filepath.Join(tmp, "diagnostics", "snapshot.json")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"snapshot", "--target", tmp, "--events", "0", "--output", outPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("snapshot output: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(out.String(), "Wrote snapshot to") {
		t.Fatalf("stdout = %q", out.String())
	}
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var snapshot snapshotResult
	if err := json.Unmarshal(body, &snapshot); err != nil {
		t.Fatalf("decode output file: %v\nbody=%s", err, string(body))
	}
	if len(snapshot.Events) != 0 {
		t.Fatalf("--events 0 should skip events: %+v", snapshot.Events)
	}
}

func TestSnapshotNoRedactPreservesPayloadSecrets(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	item := &daemon.QueueItem{
		ID:         "q-sensitive",
		State:      daemon.QueueStatePending,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-502",
		Payload:    map[string]any{"target": "worker", "api_key": "raw-key"},
		QueuedAt:   now,
		UpdatedAt:  now,
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
		t.Fatalf("write queue: %v", err)
	}
	if err := appendIntakeDelivery(teamDir, intakeDelivery{
		ID:         "intake-raw",
		Time:       now,
		Provider:   "github",
		Status:     intakeDeliveryStatusError,
		HTTPStatus: 503,
		EventType:  "pr.opened",
		Payload:    map[string]any{"source": "github", "api_key": "raw-intake-key"},
		Error:      "daemon is not running",
	}); err != nil {
		t.Fatalf("append intake delivery: %v", err)
	}
	if err := daemon.AppendMessage(daemon.DaemonRoot(teamDir), "manager", &daemon.Message{
		ID:   "msg-raw",
		From: "tester",
		Body: "raw inbox body",
		TS:   now,
	}); err != nil {
		t.Fatalf("append inbox message: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"snapshot", "--target", tmp, "--events", "0", "--no-redact", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("snapshot no-redact: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot snapshotResult
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v\nbody=%s", err, out.String())
	}
	if snapshot.Redacted {
		t.Fatalf("--no-redact should disable redaction: %+v", snapshot)
	}
	if len(snapshot.Queue) != 1 || snapshot.Queue[0].Payload["api_key"] != "raw-key" {
		t.Fatalf("queue payload = %+v", snapshot.Queue)
	}
	if len(snapshot.Intake) != 1 || snapshot.Intake[0].Payload["api_key"] != "raw-intake-key" {
		t.Fatalf("intake payload = %+v", snapshot.Intake)
	}
	if len(snapshot.Inbox) != 1 || snapshot.Inbox[0].LatestBody != "raw inbox body" {
		t.Fatalf("inbox body = %+v", snapshot.Inbox)
	}
}

func TestSnapshotRejectsJSONAndOutputFile(t *testing.T) {
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"snapshot", "--json", "--output", "snapshot.json"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("snapshot invalid flags succeeded: stdout=%s", out.String())
	}
	if !strings.Contains(stderr.String(), "choose one of --json or --output") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestSnapshotRejectsInvalidIntakeLimit(t *testing.T) {
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"snapshot", "--intake-deliveries", "-2"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("snapshot invalid intake limit succeeded: stdout=%s", out.String())
	}
	if !strings.Contains(stderr.String(), "--intake-deliveries must be >= -1") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func writeQuarantinedQueueItem(t *testing.T, teamDir, stamp, state string, item *daemon.QueueItem) {
	t.Helper()
	if item == nil {
		t.Fatal("nil queue item")
	}
	item.State = state
	body, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		t.Fatalf("marshal quarantined queue item: %v", err)
	}
	body = append(body, '\n')
	dir := filepath.Join(daemon.QueueRoot(daemon.DaemonRoot(teamDir)), "quarantine", stamp, state)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir queue quarantine: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, item.ID+".json"), body, 0o644); err != nil {
		t.Fatalf("write quarantined queue item: %v", err)
	}
}
