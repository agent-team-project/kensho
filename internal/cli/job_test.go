package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
)

func mustNewJob(t *testing.T, ticket, target string) *job.Job {
	t.Helper()
	j, err := job.New(ticket, target, "test kickoff", time.Now().UTC())
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	return j
}

func TestJobCreateListShowClose(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	create := NewRootCmd()
	createOut, createErr := &bytes.Buffer{}, &bytes.Buffer{}
	create.SetOut(createOut)
	create.SetErr(createErr)
	create.SetArgs([]string{
		"job", "create", "SQU-42",
		"--target", "worker",
		"--ticket-url", "https://linear.app/squirtlesquad/issue/SQU-42/status-monitor",
		"--kickoff", "implement the status monitor",
		"--repo", tmp,
		"--json",
	})
	if err := create.Execute(); err != nil {
		t.Fatalf("job create: %v\nstderr=%s", err, createErr.String())
	}
	var created job.Job
	if err := json.Unmarshal(createOut.Bytes(), &created); err != nil {
		t.Fatalf("decode create json: %v\nbody=%s", err, createOut.String())
	}
	if created.ID != "squ-42" || created.Status != job.StatusQueued || created.Target != "worker" {
		t.Fatalf("created = %+v", created)
	}
	if created.TicketURL != "https://linear.app/squirtlesquad/issue/SQU-42/status-monitor" {
		t.Fatalf("created ticket_url = %q", created.TicketURL)
	}

	if _, err := os.Stat(filepath.Join(tmp, ".agent_team", "jobs", "squ-42.toml")); err != nil {
		t.Fatalf("job file missing: %v", err)
	}

	list := NewRootCmd()
	listOut, listErr := &bytes.Buffer{}, &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetErr(listErr)
	list.SetArgs([]string{"job", "ls", "--status", "queued", "--repo", tmp})
	if err := list.Execute(); err != nil {
		t.Fatalf("job ls: %v\nstderr=%s", err, listErr.String())
	}
	for _, want := range []string{"squ-42", "queued", "worker", "SQU-42"} {
		if !strings.Contains(listOut.String(), want) {
			t.Fatalf("job ls missing %q:\n%s", want, listOut.String())
		}
	}

	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"job", "show", "SQU-42", "--repo", tmp})
	if err := show.Execute(); err != nil {
		t.Fatalf("job show: %v\nstderr=%s", err, showErr.String())
	}
	if !strings.Contains(showOut.String(), "Ticket URL:") ||
		!strings.Contains(showOut.String(), "https://linear.app/squirtlesquad/issue/SQU-42/status-monitor") ||
		!strings.Contains(showOut.String(), "Kickoff:") ||
		!strings.Contains(showOut.String(), "implement the status monitor") {
		t.Fatalf("job show missing kickoff:\n%s", showOut.String())
	}

	closeCmd := NewRootCmd()
	closeOut, closeErr := &bytes.Buffer{}, &bytes.Buffer{}
	closeCmd.SetOut(closeOut)
	closeCmd.SetErr(closeErr)
	closeCmd.SetArgs([]string{"job", "close", "squ-42", "--status", "done", "--repo", tmp, "--json"})
	if err := closeCmd.Execute(); err != nil {
		t.Fatalf("job close: %v\nstderr=%s", err, closeErr.String())
	}
	var closed job.Job
	if err := json.Unmarshal(closeOut.Bytes(), &closed); err != nil {
		t.Fatalf("decode close json: %v\nbody=%s", err, closeOut.String())
	}
	if closed.Status != job.StatusDone || closed.LastEvent != "closed" {
		t.Fatalf("closed = %+v", closed)
	}

	eventsCmd := NewRootCmd()
	eventsOut, eventsErr := &bytes.Buffer{}, &bytes.Buffer{}
	eventsCmd.SetOut(eventsOut)
	eventsCmd.SetErr(eventsErr)
	eventsCmd.SetArgs([]string{"job", "events", "SQU-42", "--repo", tmp, "--json"})
	if err := eventsCmd.Execute(); err != nil {
		t.Fatalf("job events: %v\nstderr=%s", err, eventsErr.String())
	}
	var events []job.Event
	if err := json.Unmarshal(eventsOut.Bytes(), &events); err != nil {
		t.Fatalf("decode events json: %v\nbody=%s", err, eventsOut.String())
	}
	if len(events) != 2 || events[0].Type != "created" || events[1].Type != "closed" {
		t.Fatalf("events = %+v", events)
	}
	if events[0].Data["ticket_url"] != "https://linear.app/squirtlesquad/issue/SQU-42/status-monitor" {
		t.Fatalf("created event data = %+v", events[0].Data)
	}

	tailCmd := NewRootCmd()
	tailOut, tailErr := &bytes.Buffer{}, &bytes.Buffer{}
	tailCmd.SetOut(tailOut)
	tailCmd.SetErr(tailErr)
	tailCmd.SetArgs([]string{"job", "events", "SQU-42", "--repo", tmp, "--tail", "1", "--format", "{{.Type}} {{.Status}}"})
	if err := tailCmd.Execute(); err != nil {
		t.Fatalf("job events tail: %v\nstderr=%s", err, tailErr.String())
	}
	if got := strings.TrimSpace(tailOut.String()); got != "closed done" {
		t.Fatalf("tail output = %q", got)
	}
}

func TestJobShowIncludesQueueItems(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	j, err := job.New("SQU-109", "worker", "implement queued work", time.Now())
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.Instance = "worker-squ-109"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	now := time.Now().UTC()
	item := &daemon.QueueItem{
		ID:         "q-job-show",
		State:      daemon.QueueStateDead,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-109",
		Payload: map[string]any{
			"job_id":  "squ-109",
			"ticket":  "SQU-109",
			"target":  "worker",
			"kickoff": "implement queued work",
		},
		Attempts:       daemon.MaxQueueAttempts,
		LastError:      "spawn failed",
		QueuedAt:       now.Add(-time.Hour),
		UpdatedAt:      now,
		DeadLetteredAt: now,
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
		t.Fatalf("WriteQueueItem: %v", err)
	}

	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"job", "show", "SQU-109", "--repo", tmp})
	if err := show.Execute(); err != nil {
		t.Fatalf("job show: %v\nstderr=%s", err, showErr.String())
	}
	for _, want := range []string{"Queue:", "q-job-show", "state=dead", "instance_id=worker-squ-109"} {
		if !strings.Contains(showOut.String(), want) {
			t.Fatalf("job show missing %q:\n%s", want, showOut.String())
		}
	}

	showJSON := NewRootCmd()
	jsonOut, jsonErr := &bytes.Buffer{}, &bytes.Buffer{}
	showJSON.SetOut(jsonOut)
	showJSON.SetErr(jsonErr)
	showJSON.SetArgs([]string{"job", "show", "SQU-109", "--repo", tmp, "--json"})
	if err := showJSON.Execute(); err != nil {
		t.Fatalf("job show json: %v\nstderr=%s", err, jsonErr.String())
	}
	var body job.Job
	if err := json.Unmarshal(jsonOut.Bytes(), &body); err != nil {
		t.Fatalf("decode job show json: %v\nbody=%s", err, jsonOut.String())
	}
	if body.ID != "squ-109" {
		t.Fatalf("json body = %+v", body)
	}
}

func TestJobTriageShowsAttentionAndReadySteps(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-48 * time.Hour)

	failed := mustNewJob(t, "SQU-201", "worker")
	failed.Status = job.StatusFailed
	failed.LastStatus = "tests failed"
	failed.UpdatedAt = old
	if err := job.Write(teamDir, failed); err != nil {
		t.Fatalf("write failed job: %v", err)
	}

	staleRunning := mustNewJob(t, "SQU-202", "worker")
	staleRunning.Status = job.StatusRunning
	staleRunning.UpdatedAt = old
	if err := job.Write(teamDir, staleRunning); err != nil {
		t.Fatalf("write stale running job: %v", err)
	}

	staleQueued := mustNewJob(t, "SQU-203", "worker")
	staleQueued.Status = job.StatusQueued
	staleQueued.UpdatedAt = old
	if err := job.Write(teamDir, staleQueued); err != nil {
		t.Fatalf("write stale queued job: %v", err)
	}

	queuedDead := mustNewJob(t, "SQU-204", "worker")
	queuedDead.Instance = "worker-squ-204"
	if err := job.Write(teamDir, queuedDead); err != nil {
		t.Fatalf("write queued dead job: %v", err)
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), &daemon.QueueItem{
		ID:         "q-triage-dead",
		State:      daemon.QueueStateDead,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-204",
		Payload: map[string]any{
			"job_id": "squ-204",
			"ticket": "SQU-204",
			"target": "worker",
		},
		Attempts:       daemon.MaxQueueAttempts,
		LastError:      "spawn failed",
		QueuedAt:       now.Add(-time.Hour),
		UpdatedAt:      now,
		DeadLetteredAt: now,
	}); err != nil {
		t.Fatalf("WriteQueueItem: %v", err)
	}

	ready := mustNewJob(t, "SQU-205", "manager")
	ready.Pipeline = "ticket_to_pr"
	ready.Steps = []job.Step{
		{ID: "triage", Target: "ticket-manager", Status: job.StatusDone, StartedAt: old, FinishedAt: old.Add(time.Hour)},
		{ID: "implement", Target: "worker", Status: job.StatusBlocked, After: []string{"triage"}},
	}
	if err := job.Write(teamDir, ready); err != nil {
		t.Fatalf("write ready job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "triage", "--repo", tmp, "--stale-after", "24h"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job triage: %v\nstderr=%s", err, stderr.String())
	}
	for _, want := range []string{
		"jobs: total=5",
		"queue: total=1 pending=0 dead=1",
		"Attention:",
		"squ-201",
		"failed",
		"squ-202",
		"stale_running",
		"running_without_instance",
		"squ-203",
		"stale_queued",
		"squ-204",
		"queue_dead",
		"Ready pipeline steps:",
		"squ-205",
		"implement",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("job triage missing %q:\n%s", want, out.String())
		}
	}

	jsonCmd := NewRootCmd()
	jsonOut, jsonErr := &bytes.Buffer{}, &bytes.Buffer{}
	jsonCmd.SetOut(jsonOut)
	jsonCmd.SetErr(jsonErr)
	jsonCmd.SetArgs([]string{"job", "triage", "--repo", tmp, "--stale-after", "24h", "--json"})
	if err := jsonCmd.Execute(); err != nil {
		t.Fatalf("job triage json: %v\nstderr=%s", err, jsonErr.String())
	}
	var snapshot jobTriageSnapshot
	if err := json.Unmarshal(jsonOut.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode triage json: %v\nbody=%s", err, jsonOut.String())
	}
	if snapshot.Summary.Total != 5 || snapshot.Queue.Dead != 1 || len(snapshot.Attention) != 4 || len(snapshot.ReadySteps) != 1 {
		t.Fatalf("triage snapshot = %+v", snapshot)
	}
	reasons := map[string][]string{}
	for _, item := range snapshot.Attention {
		reasons[item.JobID] = item.Reasons
	}
	if !containsString(reasons["squ-204"], "queue_dead") {
		t.Fatalf("squ-204 reasons = %v", reasons["squ-204"])
	}
	if snapshot.ReadySteps[0].JobID != "squ-205" || snapshot.ReadySteps[0].StepID != "implement" {
		t.Fatalf("ready steps = %+v", snapshot.ReadySteps)
	}
}

func TestJobTriageWatchRendersOnceWhenContextCancelled(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	j := mustNewJob(t, "SQU-206", "worker")
	j.Status = job.StatusFailed
	j.LastStatus = "needs attention"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if err := runJobTriageWatch(ctx, &out, teamDir, 24*time.Hour, false, time.Millisecond, false); err != nil {
		t.Fatalf("triage watch: %v", err)
	}
	if strings.Contains(out.String(), watchClearSequence) {
		t.Fatalf("watch with clear=false wrote clear sequence: %q", out.String())
	}
	for _, want := range []string{"jobs: total=1", "Attention:", "squ-206", "failed"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("triage watch missing %q:\n%s", want, out.String())
		}
	}
}

func TestJobTriageRejectsNegativeInterval(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "triage", "--repo", tmp, "--watch", "--interval", "-1s"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("job triage negative interval succeeded")
	}
	if !strings.Contains(stderr.String(), "--interval must be >= 0") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestJobReconcileQueueUpdatesDeadJob(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	j, err := job.New("SQU-110", "worker", "reconcile queue state", time.Now())
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	now := time.Now().UTC()
	item := &daemon.QueueItem{
		ID:         "q-job-reconcile",
		State:      daemon.QueueStateDead,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-110",
		Payload: map[string]any{
			"job_id": "squ-110",
			"ticket": "SQU-110",
			"target": "worker",
		},
		Attempts:       daemon.MaxQueueAttempts,
		LastError:      "spawn failed",
		QueuedAt:       now.Add(-time.Hour),
		UpdatedAt:      now,
		DeadLetteredAt: now,
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
		t.Fatalf("WriteQueueItem: %v", err)
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"job", "reconcile", "queue", "--repo", tmp, "--state", "dead", "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("job reconcile queue dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryResults []jobQueueReconcileResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryResults); err != nil {
		t.Fatalf("decode dry reconcile json: %v\nbody=%s", err, dryOut.String())
	}
	if len(dryResults) != 1 || dryResults[0].JobID != "squ-110" || dryResults[0].After != job.StatusFailed || !dryResults[0].Changed || !dryResults[0].DryRun {
		t.Fatalf("dry results = %+v", dryResults)
	}
	unchanged, err := job.Read(teamDir, "squ-110")
	if err != nil {
		t.Fatalf("read dry-run job: %v", err)
	}
	if unchanged.Status != job.StatusQueued {
		t.Fatalf("dry-run changed job = %+v", unchanged)
	}

	apply := NewRootCmd()
	applyOut, applyErr := &bytes.Buffer{}, &bytes.Buffer{}
	apply.SetOut(applyOut)
	apply.SetErr(applyErr)
	apply.SetArgs([]string{"job", "reconcile", "queue", "--repo", tmp, "--state", "dead", "--json"})
	if err := apply.Execute(); err != nil {
		t.Fatalf("job reconcile queue apply: %v\nstderr=%s", err, applyErr.String())
	}
	var applied []jobQueueReconcileResult
	if err := json.Unmarshal(applyOut.Bytes(), &applied); err != nil {
		t.Fatalf("decode applied reconcile json: %v\nbody=%s", err, applyOut.String())
	}
	if len(applied) != 1 || applied[0].JobID != "squ-110" || applied[0].After != job.StatusFailed || !applied[0].Changed || applied[0].DryRun {
		t.Fatalf("applied = %+v", applied)
	}
	updated, err := job.Read(teamDir, "squ-110")
	if err != nil {
		t.Fatalf("read updated job: %v", err)
	}
	if updated.Status != job.StatusFailed || updated.LastEvent != "queue_dead" || updated.LastStatus != "spawn failed" || updated.Instance != "worker-squ-110" {
		t.Fatalf("updated job = %+v", updated)
	}
	events, err := job.ListEvents(teamDir, "squ-110")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != "queue_reconcile" || events[0].Data["queue_id"] != "q-job-reconcile" {
		t.Fatalf("events = %+v", events)
	}
}

func TestJobCreateFromPipeline(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	create := NewRootCmd()
	createOut, createErr := &bytes.Buffer{}, &bytes.Buffer{}
	create.SetOut(createOut)
	create.SetErr(createErr)
	create.SetArgs([]string{
		"job", "create", "SQU-214",
		"--repo", tmp,
		"--pipeline", "ticket_to_pr",
		"--kickoff", "manual pipeline kickoff",
		"--json",
	})
	if err := create.Execute(); err != nil {
		t.Fatalf("job create pipeline: %v\nstderr=%s", err, createErr.String())
	}
	var created job.Job
	if err := json.Unmarshal(createOut.Bytes(), &created); err != nil {
		t.Fatalf("decode pipeline job json: %v\nbody=%s", err, createOut.String())
	}
	if created.Pipeline != "ticket_to_pr" || created.Target != "worker" || len(created.Steps) != 2 {
		t.Fatalf("created pipeline job = %+v", created)
	}
	if created.Steps[0].ID != "implement" || created.Steps[0].Status != job.StatusQueued {
		t.Fatalf("first step = %+v", created.Steps[0])
	}
	if created.Steps[1].ID != "review" || created.Steps[1].Status != job.StatusBlocked || strings.Join(created.Steps[1].After, ",") != "implement" {
		t.Fatalf("second step = %+v", created.Steps[1])
	}
	events, err := job.ListEvents(teamDir, "squ-214")
	if err != nil {
		t.Fatalf("list pipeline create events: %v", err)
	}
	if len(events) != 1 || events[0].Data["pipeline"] != "ticket_to_pr" {
		t.Fatalf("create events = %+v", events)
	}

	next := NewRootCmd()
	nextOut, nextErr := &bytes.Buffer{}, &bytes.Buffer{}
	next.SetOut(nextOut)
	next.SetErr(nextErr)
	next.SetArgs([]string{"job", "next", "squ-214", "--repo", tmp, "--format", "{{.State}} {{.Step.ID}}"})
	if err := next.Execute(); err != nil {
		t.Fatalf("job next pipeline create: %v\nstderr=%s", err, nextErr.String())
	}
	if got := strings.TrimSpace(nextOut.String()); got != "queued implement" {
		t.Fatalf("next output = %q", got)
	}

	mismatch := NewRootCmd()
	mismatchOut, mismatchErr := &bytes.Buffer{}, &bytes.Buffer{}
	mismatch.SetOut(mismatchOut)
	mismatch.SetErr(mismatchErr)
	mismatch.SetArgs([]string{"job", "create", "SQU-215", "--repo", tmp, "--pipeline", "ticket_to_pr", "--target", "manager"})
	if err := mismatch.Execute(); err == nil {
		t.Fatalf("job create pipeline target mismatch succeeded")
	}
	if !strings.Contains(mismatchErr.String(), "does not match first step target") {
		t.Fatalf("mismatch stderr = %q", mismatchErr.String())
	}
}

func TestJobCreateDispatchesImmediately(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-job-create-dispatch-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	mgr := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, 2*time.Second))
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	create := NewRootCmd()
	createOut, createErr := &bytes.Buffer{}, &bytes.Buffer{}
	create.SetOut(createOut)
	create.SetErr(createErr)
	create.SetArgs([]string{"job", "create", "SQU-216", "--repo", tmp, "--dispatch", "--workspace", "repo", "--json"})
	if err := create.Execute(); err != nil {
		t.Fatalf("job create --dispatch: %v\nstderr=%s", err, createErr.String())
	}
	var dispatched jobDispatchResult
	if err := json.Unmarshal(createOut.Bytes(), &dispatched); err != nil {
		t.Fatalf("decode dispatched json: %v\nbody=%s", err, createOut.String())
	}
	if dispatched.Job == nil || dispatched.Job.Status != job.StatusRunning || dispatched.Job.Instance != "worker-squ-216" || dispatched.Job.LastEvent != "dispatched" {
		t.Fatalf("dispatched result = %+v", dispatched)
	}

	pipeline := NewRootCmd()
	pipelineOut, pipelineErr := &bytes.Buffer{}, &bytes.Buffer{}
	pipeline.SetOut(pipelineOut)
	pipeline.SetErr(pipelineErr)
	pipeline.SetArgs([]string{"job", "create", "SQU-217", "--repo", tmp, "--pipeline", "ticket_to_pr", "--dispatch", "--workspace", "repo", "--json"})
	if err := pipeline.Execute(); err != nil {
		t.Fatalf("job create pipeline --dispatch: %v\nstderr=%s", err, pipelineErr.String())
	}
	var advanced jobAdvanceResult
	if err := json.Unmarshal(pipelineOut.Bytes(), &advanced); err != nil {
		t.Fatalf("decode pipeline dispatch json: %v\nbody=%s", err, pipelineOut.String())
	}
	if advanced.Job == nil || advanced.Step == nil {
		t.Fatalf("advanced result missing job/step = %+v", advanced)
	}
	if advanced.Job.Status != job.StatusRunning || advanced.Job.Pipeline != "ticket_to_pr" {
		t.Fatalf("advanced job = %+v", advanced.Job)
	}
	if advanced.Step.ID != "implement" || advanced.Step.Status != job.StatusRunning || advanced.Step.Instance != "worker-squ-217-implement" {
		t.Fatalf("advanced step = %+v", advanced.Step)
	}
}

func TestJobCreateDispatchMarksMessagedPersistentInstanceRunning(t *testing.T) {
	target, _, cleanup := setupIntakePipelineRepo(t)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "create", "SQU-218", "--repo", target, "--target", "manager", "--dispatch", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job create persistent --dispatch: %v\nstderr=%s", err, stderr.String())
	}
	var result jobDispatchResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode persistent dispatch json: %v\nbody=%s", err, out.String())
	}
	if result.Job == nil || result.Job.Status != job.StatusRunning || result.Job.Instance != "manager" || result.Job.LastEvent != "messaged" {
		t.Fatalf("persistent dispatch result = %+v", result)
	}
	updated, err := job.Read(filepath.Join(target, ".agent_team"), "squ-218")
	if err != nil {
		t.Fatalf("read persistent dispatch job: %v", err)
	}
	if updated.Status != job.StatusRunning || updated.Instance != "manager" || updated.LastEvent != "messaged" {
		t.Fatalf("updated persistent job = %+v", updated)
	}
}

func TestJobEventsFilters(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	base := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	j := &job.Job{
		ID:        "squ-75",
		Ticket:    "SQU-75",
		Target:    "worker",
		Status:    job.StatusRunning,
		CreatedAt: base.Add(-2 * time.Hour),
		UpdatedAt: base,
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	for _, ev := range []job.Event{
		{
			TS:      base.Add(-2 * time.Hour),
			JobID:   j.ID,
			Type:    "created",
			Status:  job.StatusQueued,
			Actor:   "cli",
			Message: "created",
		},
		{
			TS:       base.Add(-30 * time.Minute),
			JobID:    j.ID,
			Type:     "updated",
			Status:   job.StatusRunning,
			Instance: "worker-squ-75",
			Actor:    "daemon",
			Message:  "started",
		},
		{
			TS:       base.Add(-10 * time.Minute),
			JobID:    j.ID,
			Type:     "closed",
			Status:   job.StatusDone,
			Instance: "worker-squ-75",
			Actor:    "cli",
			Message:  "closed",
		},
	} {
		ev := ev
		if err := job.AppendEvent(teamDir, &ev); err != nil {
			t.Fatalf("append event %s: %v", ev.Type, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"job", "events", "squ-75",
		"--repo", tmp,
		"--type", "closed",
		"--actor", "cli",
		"--since", base.Add(-time.Hour).Format(time.RFC3339),
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job events filters: %v\nstderr=%s", err, stderr.String())
	}
	var got []job.Event
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode filtered events json: %v\nbody=%s", err, out.String())
	}
	if len(got) != 1 || got[0].Type != "closed" || got[0].Actor != "cli" {
		t.Fatalf("filtered events = %+v", got)
	}

	cmd = NewRootCmd()
	out, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"job", "events", "squ-75",
		"--repo", tmp,
		"--type", "created",
		"--since", base.Add(-time.Hour).Format(time.RFC3339),
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job events filtered empty: %v\nstderr=%s", err, stderr.String())
	}
	got = nil
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode empty filtered events json: %v\nbody=%s", err, out.String())
	}
	if len(got) != 0 {
		t.Fatalf("expected no events, got %+v", got)
	}

	cmd = NewRootCmd()
	out, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "events", "squ-75", "--repo", tmp, "--type", ","})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("job events empty type filter succeeded")
	}
	if !strings.Contains(stderr.String(), "--type requires at least one non-empty event type") {
		t.Fatalf("missing empty filter error:\n%s", stderr.String())
	}
}

func TestJobListFilters(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	first := &job.Job{
		ID:        "squ-50",
		Ticket:    "SQU-50",
		Target:    "worker",
		Instance:  "worker-squ-50",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		Branch:    "worktree-worker-squ-50",
		PR:        "https://github.com/acme/repo/pull/50",
		CreatedAt: now,
		UpdatedAt: now,
	}
	second := &job.Job{
		ID:        "squ-51",
		Ticket:    "SQU-51",
		Target:    "manager",
		Instance:  "manager",
		Status:    job.StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, first); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if err := job.Write(teamDir, second); err != nil {
		t.Fatalf("write second: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"job", "ls",
		"--repo", tmp,
		"--target-agent", "worker",
		"--pipeline", "ticket_to_pr",
		"--instance", "worker-squ-50",
		"--branch", "worktree-worker-squ-50",
		"--pr", "50",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job ls filters: %v\nstderr=%s", err, stderr.String())
	}
	var got []job.Job
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode job ls json: %v\nbody=%s", err, out.String())
	}
	if len(got) != 1 || got[0].ID != "squ-50" {
		t.Fatalf("filtered jobs = %+v", got)
	}
}

func TestJobListSummary(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	jobs := []*job.Job{
		{ID: "squ-65", Ticket: "SQU-65", Target: "worker", Status: job.StatusQueued, CreatedAt: now, UpdatedAt: now},
		{
			ID:        "squ-66",
			Ticket:    "SQU-66",
			Target:    "worker",
			Instance:  "worker-squ-66",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			Branch:    "worktree-worker-squ-66",
			Worktree:  filepath.Join(tmp, ".claude", "worktrees", "worker-squ-66"),
			PR:        "https://github.com/acme/repo/pull/66",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{ID: "squ-67", Ticket: "SQU-67", Target: "manager", Status: job.StatusDone, CreatedAt: now, UpdatedAt: now},
	}
	for _, j := range jobs {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "ls", "--repo", tmp, "--target-agent", "worker", "--summary", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job ls summary json: %v\nstderr=%s", err, stderr.String())
	}
	var summary jobSummary
	if err := json.Unmarshal(out.Bytes(), &summary); err != nil {
		t.Fatalf("decode summary json: %v\nbody=%s", err, out.String())
	}
	if summary.Total != 2 || summary.Queued != 1 || summary.Running != 1 || summary.Done != 0 {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.Targets["worker"] != 2 || summary.Pipelines["ticket_to_pr"] != 1 {
		t.Fatalf("summary maps = %+v", summary)
	}
	if summary.WithInstance != 1 || summary.WithBranch != 1 || summary.WithWorktree != 1 || summary.WithPR != 1 {
		t.Fatalf("summary ownership = %+v", summary)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"job", "ls", "--repo", tmp, "--status", "done", "--summary"})
	if err := text.Execute(); err != nil {
		t.Fatalf("job ls summary text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{
		"jobs: total=1 queued=0 running=0 blocked=0 done=1 failed=0",
		"targets: manager=1",
		"ownership: instance=0 branch=0 worktree=0 pr=0",
	} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("summary text missing %q:\n%s", want, textOut.String())
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var watchOut bytes.Buffer
	if err := runJobSummaryWatch(ctx, &watchOut, teamDir, jobListFilters{}, false, time.Millisecond, false); err != nil {
		t.Fatalf("runJobSummaryWatch: %v", err)
	}
	if !strings.Contains(watchOut.String(), "jobs: total=3") || strings.Contains(watchOut.String(), watchClearSequence) {
		t.Fatalf("watch summary output = %q", watchOut.String())
	}
}

func TestJobListSortsRows(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	base := time.Now().UTC()
	jobs := []*job.Job{
		{ID: "squ-72", Ticket: "SQU-72", Target: "worker", Status: job.StatusDone, CreatedAt: base, UpdatedAt: base.Add(time.Minute)},
		{ID: "squ-73", Ticket: "SQU-73", Target: "manager", Status: job.StatusQueued, CreatedAt: base, UpdatedAt: base.Add(3 * time.Minute)},
		{ID: "squ-74", Ticket: "SQU-74", Target: "worker", Status: job.StatusBlocked, CreatedAt: base, UpdatedAt: base.Add(2 * time.Minute)},
	}
	for _, j := range jobs {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	updated := NewRootCmd()
	updatedOut, updatedErr := &bytes.Buffer{}, &bytes.Buffer{}
	updated.SetOut(updatedOut)
	updated.SetErr(updatedErr)
	updated.SetArgs([]string{"job", "ls", "--repo", tmp, "--sort", "updated", "--json"})
	if err := updated.Execute(); err != nil {
		t.Fatalf("job ls sort updated: %v\nstderr=%s", err, updatedErr.String())
	}
	var got []job.Job
	if err := json.Unmarshal(updatedOut.Bytes(), &got); err != nil {
		t.Fatalf("decode sorted jobs: %v\nbody=%s", err, updatedOut.String())
	}
	if len(got) != 3 || got[0].ID != "squ-73" || got[1].ID != "squ-74" || got[2].ID != "squ-72" {
		t.Fatalf("updated sort = %+v", got)
	}

	status := NewRootCmd()
	statusOut, statusErr := &bytes.Buffer{}, &bytes.Buffer{}
	status.SetOut(statusOut)
	status.SetErr(statusErr)
	status.SetArgs([]string{"job", "ls", "--repo", tmp, "--sort", "status", "--format", "{{.ID}}"})
	if err := status.Execute(); err != nil {
		t.Fatalf("job ls sort status: %v\nstderr=%s", err, statusErr.String())
	}
	if got := strings.Split(strings.TrimSpace(statusOut.String()), "\n"); strings.Join(got, ",") != "squ-73,squ-74,squ-72" {
		t.Fatalf("status sort output = %q", statusOut.String())
	}

	invalid := NewRootCmd()
	invalidOut, invalidErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalid.SetOut(invalidOut)
	invalid.SetErr(invalidErr)
	invalid.SetArgs([]string{"job", "ls", "--repo", tmp, "--sort", "age"})
	if err := invalid.Execute(); err == nil {
		t.Fatalf("job ls invalid sort succeeded unexpectedly")
	}
	if !strings.Contains(invalidErr.String(), "--sort must be") {
		t.Fatalf("invalid sort stderr = %q", invalidErr.String())
	}
}

func TestJobRmRequiresForceForActiveAndRemovesEvents(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	create := NewRootCmd()
	createOut, createErr := &bytes.Buffer{}, &bytes.Buffer{}
	create.SetOut(createOut)
	create.SetErr(createErr)
	create.SetArgs([]string{"job", "create", "SQU-61", "--repo", tmp, "--kickoff", "remove smoke"})
	if err := create.Execute(); err != nil {
		t.Fatalf("job create: %v\nstderr=%s", err, createErr.String())
	}

	rmActive := NewRootCmd()
	rmActiveOut, rmActiveErr := &bytes.Buffer{}, &bytes.Buffer{}
	rmActive.SetOut(rmActiveOut)
	rmActive.SetErr(rmActiveErr)
	rmActive.SetArgs([]string{"job", "rm", "squ-61", "--repo", tmp})
	if err := rmActive.Execute(); err == nil {
		t.Fatalf("job rm active succeeded unexpectedly")
	}
	if !strings.Contains(rmActiveErr.String(), "refusing to remove active job") {
		t.Fatalf("rm active stderr = %q", rmActiveErr.String())
	}

	closeCmd := NewRootCmd()
	closeOut, closeErr := &bytes.Buffer{}, &bytes.Buffer{}
	closeCmd.SetOut(closeOut)
	closeCmd.SetErr(closeErr)
	closeCmd.SetArgs([]string{"job", "close", "squ-61", "--repo", tmp, "--status", "done"})
	if err := closeCmd.Execute(); err != nil {
		t.Fatalf("job close: %v\nstderr=%s", err, closeErr.String())
	}

	rm := NewRootCmd()
	rmOut, rmErr := &bytes.Buffer{}, &bytes.Buffer{}
	rm.SetOut(rmOut)
	rm.SetErr(rmErr)
	rm.SetArgs([]string{"job", "rm", "SQU-61", "--repo", tmp, "--json"})
	if err := rm.Execute(); err != nil {
		t.Fatalf("job rm: %v\nstderr=%s", err, rmErr.String())
	}
	var removed []jobRemoveResult
	if err := json.Unmarshal(rmOut.Bytes(), &removed); err != nil {
		t.Fatalf("decode rm json: %v\nbody=%s", err, rmOut.String())
	}
	if len(removed) != 1 || !removed[0].Removed || !removed[0].JobFile || !removed[0].EventLog || !removed[0].EventsRemoved {
		t.Fatalf("removed = %+v", removed)
	}
	if _, err := os.Stat(job.Path(filepath.Join(tmp, ".agent_team"), "squ-61")); !os.IsNotExist(err) {
		t.Fatalf("job file err=%v, want not exist", err)
	}
	if _, err := os.Stat(job.EventPath(filepath.Join(tmp, ".agent_team"), "squ-61")); !os.IsNotExist(err) {
		t.Fatalf("event file err=%v, want not exist", err)
	}
}

func TestJobPruneRemovesOnlySelectedTerminalJobs(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{ID: "squ-62", Ticket: "SQU-62", Target: "worker", Status: job.StatusDone, CreatedAt: now, UpdatedAt: now},
		{ID: "squ-63", Ticket: "SQU-63", Target: "worker", Status: job.StatusFailed, CreatedAt: now, UpdatedAt: now},
		{ID: "squ-64", Ticket: "SQU-64", Target: "worker", Status: job.StatusQueued, CreatedAt: now, UpdatedAt: now},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
		if err := job.AppendSnapshotEvent(teamDir, j, "seeded", "test", "", nil); err != nil {
			t.Fatalf("append event %s: %v", j.ID, err)
		}
	}

	dryRun := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dryRun.SetOut(dryOut)
	dryRun.SetErr(dryErr)
	dryRun.SetArgs([]string{"job", "prune", "--repo", tmp, "--dry-run", "--json"})
	if err := dryRun.Execute(); err != nil {
		t.Fatalf("job prune dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dry []jobRemoveResult
	if err := json.Unmarshal(dryOut.Bytes(), &dry); err != nil {
		t.Fatalf("decode dry-run json: %v\nbody=%s", err, dryOut.String())
	}
	if len(dry) != 2 || !dry[0].DryRun || dry[0].Removed {
		t.Fatalf("dry-run results = %+v", dry)
	}
	if _, err := os.Stat(job.Path(teamDir, "squ-62")); err != nil {
		t.Fatalf("dry-run removed job unexpectedly: %v", err)
	}

	pruneDone := NewRootCmd()
	pruneDoneOut, pruneDoneErr := &bytes.Buffer{}, &bytes.Buffer{}
	pruneDone.SetOut(pruneDoneOut)
	pruneDone.SetErr(pruneDoneErr)
	pruneDone.SetArgs([]string{"job", "prune", "--repo", tmp, "--status", "done", "--format", "{{.ID}} {{.Action}} {{.Status}}"})
	if err := pruneDone.Execute(); err != nil {
		t.Fatalf("job prune done: %v\nstderr=%s", err, pruneDoneErr.String())
	}
	if got := strings.TrimSpace(pruneDoneOut.String()); got != "squ-62 removed done" {
		t.Fatalf("prune done output = %q", got)
	}
	if _, err := os.Stat(job.Path(teamDir, "squ-62")); !os.IsNotExist(err) {
		t.Fatalf("done job file err=%v, want not exist", err)
	}
	if _, err := os.Stat(job.EventPath(teamDir, "squ-62")); !os.IsNotExist(err) {
		t.Fatalf("done event file err=%v, want not exist", err)
	}
	if _, err := os.Stat(job.Path(teamDir, "squ-63")); err != nil {
		t.Fatalf("failed job removed unexpectedly: %v", err)
	}
	if _, err := os.Stat(job.Path(teamDir, "squ-64")); err != nil {
		t.Fatalf("queued job removed unexpectedly: %v", err)
	}
}

func TestJobReopenResetsStatusAndAudits(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	failed := &job.Job{
		ID:        "squ-68",
		Ticket:    "SQU-68",
		Target:    "worker",
		Status:    job.StatusFailed,
		LastEvent: "dispatch_failed",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, failed); err != nil {
		t.Fatalf("write failed job: %v", err)
	}

	reopen := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	reopen.SetOut(out)
	reopen.SetErr(stderr)
	reopen.SetArgs([]string{"job", "reopen", "SQU-68", "--repo", tmp, "--message", "retry after fix", "--json"})
	if err := reopen.Execute(); err != nil {
		t.Fatalf("job reopen: %v\nstderr=%s", err, stderr.String())
	}
	var reopened job.Job
	if err := json.Unmarshal(out.Bytes(), &reopened); err != nil {
		t.Fatalf("decode reopen json: %v\nbody=%s", err, out.String())
	}
	if reopened.Status != job.StatusQueued || reopened.LastEvent != "reopened" || reopened.LastStatus != "retry after fix" {
		t.Fatalf("reopened = %+v", reopened)
	}
	events, err := job.ListEvents(teamDir, "squ-68")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != "reopened" || events[0].Status != job.StatusQueued || events[0].Message != "retry after fix" {
		t.Fatalf("events = %+v", events)
	}
}

func TestJobRetryDispatchesReopenedJob(t *testing.T) {
	target, mgr, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Now().UTC()
	failed := &job.Job{
		ID:         "squ-80",
		Ticket:     "SQU-80",
		Target:     "worker",
		Kickoff:    "retry failed worker",
		Status:     job.StatusFailed,
		LastEvent:  "dispatch_failed",
		LastStatus: "spawn failed",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := job.Write(teamDir, failed); err != nil {
		t.Fatalf("write failed job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "retry", "SQU-80", "--repo", target, "--dispatch", "--workspace", "repo", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job retry --dispatch: %v\nstderr=%s", err, stderr.String())
	}
	var result jobDispatchResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode retry dispatch json: %v\nbody=%s", err, out.String())
	}
	if result.Job == nil || result.Job.Status != job.StatusRunning || result.Job.Instance != "worker-squ-80" || result.Job.LastEvent != "dispatched" {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Event.Dispatched) != 1 {
		t.Fatalf("event = %+v, want one dispatch", result.Event)
	}
	events, err := job.ListEvents(teamDir, "squ-80")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 3 ||
		events[0].Type != "reopened" ||
		events[1].Type != "dispatched" ||
		events[1].Actor != "daemon" ||
		events[2].Type != "dispatched" ||
		events[2].Actor != "cli" {
		t.Fatalf("events = %+v", events)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-80")
}

func TestJobReopenRefusesRunningUnlessForced(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	running := &job.Job{
		ID:        "squ-69",
		Ticket:    "SQU-69",
		Target:    "worker",
		Status:    job.StatusRunning,
		Instance:  "worker-squ-69",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, running); err != nil {
		t.Fatalf("write running job: %v", err)
	}

	refused := NewRootCmd()
	refusedOut, refusedErr := &bytes.Buffer{}, &bytes.Buffer{}
	refused.SetOut(refusedOut)
	refused.SetErr(refusedErr)
	refused.SetArgs([]string{"job", "reopen", "squ-69", "--repo", tmp})
	if err := refused.Execute(); err == nil {
		t.Fatalf("job reopen running succeeded unexpectedly")
	}
	if !strings.Contains(refusedErr.String(), "refusing to reopen running job") {
		t.Fatalf("stderr = %q", refusedErr.String())
	}

	forced := NewRootCmd()
	forcedOut, forcedErr := &bytes.Buffer{}, &bytes.Buffer{}
	forced.SetOut(forcedOut)
	forced.SetErr(forcedErr)
	forced.SetArgs([]string{"job", "retry", "squ-69", "--repo", tmp, "--force", "--status", "blocked", "--format", "{{.ID}} {{.Status}} {{.LastEvent}}"})
	if err := forced.Execute(); err != nil {
		t.Fatalf("job retry force: %v\nstderr=%s", err, forcedErr.String())
	}
	if got := strings.TrimSpace(forcedOut.String()); got != "squ-69 blocked reopened" {
		t.Fatalf("forced output = %q", got)
	}
}

func TestJobUpdateSetsClearsAndAuditsMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-70",
		Ticket:    "SQU-70",
		Target:    "worker",
		Status:    job.StatusQueued,
		Branch:    "old-branch",
		Worktree:  "/tmp/old-worktree",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	update := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	update.SetOut(out)
	update.SetErr(stderr)
	update.SetArgs([]string{
		"job", "update", "SQU-70",
		"--repo", tmp,
		"--status", "running",
		"--instance", "worker-squ-70",
		"--pr", "https://github.com/acme/repo/pull/70",
		"--message", "metadata repaired",
		"--json",
	})
	if err := update.Execute(); err != nil {
		t.Fatalf("job update: %v\nstderr=%s", err, stderr.String())
	}
	var updated job.Job
	if err := json.Unmarshal(out.Bytes(), &updated); err != nil {
		t.Fatalf("decode update json: %v\nbody=%s", err, out.String())
	}
	if updated.Status != job.StatusRunning || updated.Instance != "worker-squ-70" || updated.PR == "" || updated.LastStatus != "metadata repaired" {
		t.Fatalf("updated = %+v", updated)
	}
	events, err := job.ListEvents(teamDir, "squ-70")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != "updated" || events[0].Data["status"] != "running" || events[0].Data["instance"] != "worker-squ-70" {
		t.Fatalf("events = %+v", events)
	}

	clear := NewRootCmd()
	clearOut, clearErr := &bytes.Buffer{}, &bytes.Buffer{}
	clear.SetOut(clearOut)
	clear.SetErr(clearErr)
	clear.SetArgs([]string{"job", "update", "squ-70", "--repo", tmp, "--clear", "branch,worktree,pr", "--format", "{{.Branch}}|{{.Worktree}}|{{.PR}}|{{.LastStatus}}"})
	if err := clear.Execute(); err != nil {
		t.Fatalf("job update clear: %v\nstderr=%s", err, clearErr.String())
	}
	if got := strings.TrimSpace(clearOut.String()); got != "|||updated branch,pr,worktree" {
		t.Fatalf("clear output = %q", got)
	}
}

func TestJobUpdateRequiresChange(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-71",
		Ticket:    "SQU-71",
		Target:    "worker",
		Status:    job.StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "update", "squ-71", "--repo", tmp})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("job update without changes succeeded unexpectedly")
	}
	if !strings.Contains(stderr.String(), "pass at least one update flag") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestJobListWatchRendersSnapshot(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-52",
		Ticket:    "SQU-52",
		Target:    "worker",
		Status:    job.StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if err := runJobListWatch(ctx, &out, teamDir, jobListFilters{}, false, nil, time.Millisecond, false); err != nil {
		t.Fatalf("runJobListWatch: %v", err)
	}
	if !strings.Contains(out.String(), "squ-52") || strings.Contains(out.String(), watchClearSequence) {
		t.Fatalf("watch output = %q", out.String())
	}
}

func TestJobWaitPollsUntilTerminalStatus(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-58",
		Ticket:    "SQU-58",
		Target:    "worker",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(25 * time.Millisecond)
		updated, err := job.Read(teamDir, "squ-58")
		if err != nil {
			t.Errorf("read job in updater: %v", err)
			return
		}
		updated.Status = job.StatusDone
		updated.LastEvent = "test_done"
		updated.UpdatedAt = time.Now().UTC()
		if err := job.Write(teamDir, updated); err != nil {
			t.Errorf("write done job: %v", err)
		}
	}()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "wait", "SQU-58", "--repo", tmp, "--timeout", "2s", "--interval", "10ms", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job wait: %v\nstderr=%s", err, stderr.String())
	}
	<-done
	var got job.Job
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode wait json: %v\nbody=%s", err, out.String())
	}
	if got.Status != job.StatusDone || got.LastEvent != "test_done" {
		t.Fatalf("wait result = %+v", got)
	}
}

func TestJobWaitTimesOut(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-59",
		Ticket:    "SQU-59",
		Target:    "worker",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "wait", "squ-59", "--repo", tmp, "--timeout", "1ms", "--interval", "10ms"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("job wait succeeded unexpectedly")
	}
	if !strings.Contains(stderr.String(), "timed out") || !strings.Contains(stderr.String(), "current=running") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestJobWaitFailOnFailed(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-60",
		Ticket:    "SQU-60",
		Target:    "worker",
		Status:    job.StatusFailed,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "wait", "squ-60", "--repo", tmp, "--quiet", "--fail-on-failed"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("job wait succeeded unexpectedly")
	}
	if out.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("quiet wait produced stdout=%q stderr=%q", out.String(), stderr.String())
	}
}

func TestJobLogsReadsOwningInstanceLog(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-53",
		Ticket:    "SQU-53",
		Target:    "worker",
		Instance:  "worker-squ-53",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance:  "worker-squ-53",
		Agent:     "worker",
		Status:    daemon.StatusStopped,
		StartedAt: now,
		Job:       "squ-53",
		Ticket:    "SQU-53",
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	writeChildLogForTest(t, root, "worker-squ-53", "first\nmiddle\nlast\n")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "logs", "SQU-53", "--repo", tmp, "--tail", "1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job logs: %v\nstderr=%s", err, stderr.String())
	}
	if got := out.String(); got != "last\n" {
		t.Fatalf("job logs output = %q, want last line", got)
	}
}

func TestJobLogsRequiresOwningInstance(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-54",
		Ticket:    "SQU-54",
		Target:    "worker",
		Status:    job.StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "logs", "squ-54", "--repo", tmp})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("job logs succeeded unexpectedly")
	}
	if !strings.Contains(stderr.String(), "has no owning instance") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestJobAttachResolvesOwningInstance(t *testing.T) {
	env := newAttachTestEnv(t)
	meta := env.dispatchOne(t, "worker-squ-55")
	now := time.Now().UTC()
	if err := job.Write(env.teamDir, &job.Job{
		ID:        "squ-55",
		Ticket:    "SQU-55",
		Target:    "worker",
		Instance:  "worker-squ-55",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cap, restore := captureAttachExec(t, nil)
	defer restore()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "attach", "SQU-55", "--repo", env.target, "--no-resume"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job attach: %v\nstderr=%s", err, stderr.String())
	}
	if !cap.called {
		t.Fatal("execClaudeAttach was not called")
	}
	if len(cap.args) != 2 || cap.args[0] != "--resume" || cap.args[1] != meta.SessionID {
		t.Fatalf("attach args = %v, want resume session %s", cap.args, meta.SessionID)
	}
	wantCWD := env.target
	if eval, err := filepath.EvalSymlinks(env.target); err == nil {
		wantCWD = eval
	}
	if cap.cwd != wantCWD {
		t.Fatalf("attach cwd = %q, want %q", cap.cwd, wantCWD)
	}
	if err := env.dmn.Manager().WaitForReaper("worker-squ-55", 5*time.Second); err != nil {
		t.Fatalf("wait stop reaper: %v", err)
	}
}

func TestJobAttachLogModeReadsOwningInstanceLog(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-56",
		Ticket:    "SQU-56",
		Target:    "worker",
		Instance:  "worker-squ-56",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance:  "worker-squ-56",
		Agent:     "worker",
		Status:    daemon.StatusStopped,
		StartedAt: now,
		Job:       "squ-56",
		Ticket:    "SQU-56",
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	writeChildLogForTest(t, root, "worker-squ-56", "first\nlast\n")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "attach", "squ-56", "--repo", tmp, "--no-follow", "--tail", "1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job attach log mode: %v\nstderr=%s", err, stderr.String())
	}
	if got := out.String(); got != "last\n" {
		t.Fatalf("job attach output = %q, want last line", got)
	}
}

func TestJobAttachRequiresOwningInstance(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-57",
		Ticket:    "SQU-57",
		Target:    "worker",
		Status:    job.StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "attach", "squ-57", "--repo", tmp})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("job attach succeeded unexpectedly")
	}
	if !strings.Contains(stderr.String(), "has no owning instance") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestJobStartResumesOwningInstanceAndMarksJobRunning(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-job-start-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	mgr := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, 2*time.Second))
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()
	if _, err := mgr.Dispatch(daemon.DispatchInput{Agent: "worker", Name: "worker-squ-63", Workspace: tmp}); err != nil {
		t.Fatalf("dispatch worker: %v", err)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-63")
	t.Cleanup(func() {
		meta, err := daemon.ReadMetadata(root, "worker-squ-63")
		if err == nil && meta.Status == daemon.StatusRunning {
			stopAndWaitForTest(t, mgr, "worker-squ-63")
		}
	})
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-63",
		Ticket:    "SQU-63",
		Target:    "worker",
		Instance:  "worker-squ-63",
		Status:    job.StatusBlocked,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "start", "squ-63", "--repo", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job start: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var rows []lifecycleActionResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode start json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Action != "resume" || rows[0].Instance != "worker-squ-63" || rows[0].Status != string(daemon.StatusRunning) {
		t.Fatalf("rows = %+v", rows)
	}
	updated, err := job.Read(teamDir, "squ-63")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.Status != job.StatusRunning || updated.LastEvent != "instance_start" || updated.LastStatus != "start worker-squ-63" {
		t.Fatalf("updated job = %+v", updated)
	}
}

func TestJobStopStopsOwningInstanceAndBlocksJob(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-job-stop-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	mgr := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, 2*time.Second))
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()
	if _, err := mgr.Dispatch(daemon.DispatchInput{Agent: "worker", Name: "worker-squ-61", Workspace: tmp}); err != nil {
		t.Fatalf("dispatch worker: %v", err)
	}
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-61",
		Ticket:    "SQU-61",
		Target:    "worker",
		Instance:  "worker-squ-61",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "stop", "squ-61", "--repo", tmp, "--wait", "--timeout", "2s", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job stop: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var rows []instanceDownResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode stop json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Action != "stop" || rows[0].Instance != "worker-squ-61" || rows[0].Status != "stopped" {
		t.Fatalf("rows = %+v", rows)
	}
	updated, err := job.Read(teamDir, "squ-61")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.Status != job.StatusBlocked || updated.LastEvent != "instance_stop" || updated.LastStatus != "stop worker-squ-61" {
		t.Fatalf("updated job = %+v", updated)
	}
}

func TestJobKillDryRunDoesNotMutateJob(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-job-kill-dry-run-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	mgr := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, 2*time.Second))
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()
	if _, err := mgr.Dispatch(daemon.DispatchInput{Agent: "worker", Name: "worker-squ-62", Workspace: tmp}); err != nil {
		t.Fatalf("dispatch worker: %v", err)
	}
	defer stopAndWaitForTest(t, mgr, "worker-squ-62")
	now := time.Now().UTC()
	original := &job.Job{
		ID:        "squ-62",
		Ticket:    "SQU-62",
		Target:    "worker",
		Instance:  "worker-squ-62",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, original); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "kill", "squ-62", "--repo", tmp, "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job kill --dry-run: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var rows []instanceDownResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode kill dry-run json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Action != "kill" || rows[0].Instance != "worker-squ-62" || !rows[0].DryRun {
		t.Fatalf("rows = %+v", rows)
	}
	updated, err := job.Read(teamDir, "squ-62")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.Status != job.StatusRunning || updated.LastEvent != "" || !updated.UpdatedAt.Equal(original.UpdatedAt) {
		t.Fatalf("dry-run mutated job = %+v", updated)
	}
}

func TestJobDispatchAndSend(t *testing.T) {
	target, mgr, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()

	create := NewRootCmd()
	createOut, createErr := &bytes.Buffer{}, &bytes.Buffer{}
	create.SetOut(createOut)
	create.SetErr(createErr)
	create.SetArgs([]string{
		"job", "create", "SQU-43",
		"--target", "worker",
		"--kickoff", "implement queue persistence",
		"--repo", target,
	})
	if err := create.Execute(); err != nil {
		t.Fatalf("job create: %v\nstderr=%s", err, createErr.String())
	}

	dispatch := NewRootCmd()
	dispatchOut, dispatchErr := &bytes.Buffer{}, &bytes.Buffer{}
	dispatch.SetOut(dispatchOut)
	dispatch.SetErr(dispatchErr)
	dispatch.SetArgs([]string{"job", "dispatch", "squ-43", "--workspace", "repo", "--repo", target, "--json"})
	if err := dispatch.Execute(); err != nil {
		t.Fatalf("job dispatch: %v\nstderr=%s", err, dispatchErr.String())
	}
	var dispatched struct {
		Job   job.Job       `json:"job"`
		Event eventResponse `json:"event"`
	}
	if err := json.Unmarshal(dispatchOut.Bytes(), &dispatched); err != nil {
		t.Fatalf("decode dispatch json: %v\nbody=%s", err, dispatchOut.String())
	}
	if dispatched.Job.Status != job.StatusRunning || dispatched.Job.Instance != "worker-squ-43" {
		t.Fatalf("dispatched job = %+v", dispatched.Job)
	}
	if len(dispatched.Event.Dispatched) != 1 {
		t.Fatalf("event = %+v, want one dispatch", dispatched.Event)
	}

	send := NewRootCmd()
	sendOut, sendErr := &bytes.Buffer{}, &bytes.Buffer{}
	send.SetOut(sendOut)
	send.SetErr(sendErr)
	send.SetArgs([]string{"job", "send", "SQU-43", "please post a status update", "--repo", target})
	if err := send.Execute(); err != nil {
		t.Fatalf("job send: %v\nstderr=%s", err, sendErr.String())
	}
	if !strings.Contains(sendOut.String(), "job=squ-43") {
		t.Fatalf("send output = %q", sendOut.String())
	}
	messages, err := daemon.ReadMessages(daemon.DaemonRoot(filepath.Join(target, ".agent_team")), "worker-squ-43")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 1 || messages[0].Body != "please post a status update" {
		t.Fatalf("messages = %+v", messages)
	}

	updated, err := job.Read(filepath.Join(target, ".agent_team"), "squ-43")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.LastEvent != "message_sent" || updated.LastStatus != "please post a status update" {
		t.Fatalf("updated job = %+v", updated)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-43")
}

func TestJobDispatchRecordsWorktreeAndCleanup(t *testing.T) {
	target, mgr, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	initGitRepoForJobTest(t, target)
	teamDir := filepath.Join(target, ".agent_team")

	create := NewRootCmd()
	create.SetOut(&bytes.Buffer{})
	createErr := &bytes.Buffer{}
	create.SetErr(createErr)
	create.SetArgs([]string{
		"job", "create", "SQU-44",
		"--target", "worker",
		"--kickoff", "implement worktree ownership",
		"--repo", target,
	})
	if err := create.Execute(); err != nil {
		t.Fatalf("job create: %v\nstderr=%s", err, createErr.String())
	}

	dispatch := NewRootCmd()
	dispatchOut, dispatchErr := &bytes.Buffer{}, &bytes.Buffer{}
	dispatch.SetOut(dispatchOut)
	dispatch.SetErr(dispatchErr)
	dispatch.SetArgs([]string{"job", "dispatch", "squ-44", "--workspace", "worktree", "--repo", target, "--json"})
	if err := dispatch.Execute(); err != nil {
		t.Fatalf("job dispatch: %v\nstderr=%s", err, dispatchErr.String())
	}
	dispatched, err := job.Read(teamDir, "squ-44")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if dispatched.Instance != "worker-squ-44" || dispatched.Branch == "" || dispatched.Worktree == "" {
		t.Fatalf("dispatched job missing ownership metadata: %+v", dispatched)
	}
	if st, err := os.Stat(dispatched.Worktree); err != nil || !st.IsDir() {
		t.Fatalf("worktree path = %q stat=%v", dispatched.Worktree, err)
	}
	meta, err := daemon.ReadMetadata(daemon.DaemonRoot(filepath.Join(target, ".agent_team")), "worker-squ-44")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if meta.Job != "squ-44" || meta.Ticket != "SQU-44" || meta.Branch != dispatched.Branch || meta.Workspace != dispatched.Worktree {
		t.Fatalf("metadata = %+v, want job/ticket/branch/worktree ownership", meta)
	}

	ps := NewRootCmd()
	psOut, psErr := &bytes.Buffer{}, &bytes.Buffer{}
	ps.SetOut(psOut)
	ps.SetErr(psErr)
	ps.SetArgs([]string{"ps", "--json", "--target", target})
	if err := ps.Execute(); err != nil {
		t.Fatalf("ps --json: %v\nstderr=%s", err, psErr.String())
	}
	var rows []psJSONRow
	if err := json.Unmarshal(psOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode ps rows: %v\nbody=%s", err, psOut.String())
	}
	if len(rows) != 1 || rows[0].Job != "squ-44" || rows[0].Branch != dispatched.Branch {
		t.Fatalf("ps rows = %+v, want job ownership", rows)
	}
	inspect := NewRootCmd()
	inspectOut, inspectErr := &bytes.Buffer{}, &bytes.Buffer{}
	inspect.SetOut(inspectOut)
	inspect.SetErr(inspectErr)
	inspect.SetArgs([]string{"inspect", "worker-squ-44", "--json", "--target", target})
	if err := inspect.Execute(); err != nil {
		t.Fatalf("inspect --json: %v\nstderr=%s", err, inspectErr.String())
	}
	var info inspectJSON
	if err := json.Unmarshal(inspectOut.Bytes(), &info); err != nil {
		t.Fatalf("decode inspect json: %v\nbody=%s", err, inspectOut.String())
	}
	if info.Runtime == nil || info.Runtime.Job != "squ-44" || info.Runtime.Branch != dispatched.Branch {
		t.Fatalf("inspect runtime = %+v, want job ownership", info.Runtime)
	}

	stopAndWaitForTest(t, mgr, "worker-squ-44")

	previewCmd := NewRootCmd()
	previewOut, previewErr := &bytes.Buffer{}, &bytes.Buffer{}
	previewCmd.SetOut(previewOut)
	previewCmd.SetErr(previewErr)
	previewCmd.SetArgs([]string{"job", "cleanup", "squ-44", "--dry-run", "--repo", target, "--json"})
	if err := previewCmd.Execute(); err != nil {
		t.Fatalf("job cleanup dry-run: %v\nstderr=%s", err, previewErr.String())
	}
	var preview jobCleanupPreview
	if err := json.Unmarshal(previewOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode cleanup dry-run json: %v\nbody=%s", err, previewOut.String())
	}
	if !preview.DryRun || !preview.WouldRemoveWorktree || !preview.WouldRemoveBranch || preview.Summary != "would remove worktree and branch" {
		t.Fatalf("cleanup preview = %+v", preview)
	}
	stillOwned, err := job.Read(teamDir, "squ-44")
	if err != nil {
		t.Fatalf("read dry-run job: %v", err)
	}
	if stillOwned.Worktree != dispatched.Worktree || stillOwned.Branch != dispatched.Branch {
		t.Fatalf("dry-run mutated job = %+v", stillOwned)
	}
	if _, err := os.Stat(dispatched.Worktree); err != nil {
		t.Fatalf("dry-run removed worktree or stat failed: %v", err)
	}
	if !branchExists(t, target, dispatched.Branch) {
		t.Fatalf("dry-run removed branch %s", dispatched.Branch)
	}

	cleanupCmd := NewRootCmd()
	cleanupOut, cleanupErr := &bytes.Buffer{}, &bytes.Buffer{}
	cleanupCmd.SetOut(cleanupOut)
	cleanupCmd.SetErr(cleanupErr)
	cleanupCmd.SetArgs([]string{"job", "cleanup", "squ-44", "--merged", "--repo", target, "--json"})
	if err := cleanupCmd.Execute(); err != nil {
		t.Fatalf("job cleanup: %v\nstderr=%s", err, cleanupErr.String())
	}
	var cleaned job.Job
	if err := json.Unmarshal(cleanupOut.Bytes(), &cleaned); err != nil {
		t.Fatalf("decode cleanup json: %v\nbody=%s", err, cleanupOut.String())
	}
	if cleaned.Worktree != "" || cleaned.Branch != "" || cleaned.LastEvent != "cleanup" {
		t.Fatalf("cleaned job = %+v", cleaned)
	}
	if _, err := os.Stat(dispatched.Worktree); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists or stat error: %v", err)
	}
	if branchExists(t, target, dispatched.Branch) {
		t.Fatalf("branch %s still exists after cleanup", dispatched.Branch)
	}
}

func TestJobReconcileGitHubMergedCleansOwnedWorktree(t *testing.T) {
	target, mgr, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	initGitRepoForJobTest(t, target)

	create := NewRootCmd()
	create.SetOut(&bytes.Buffer{})
	createErr := &bytes.Buffer{}
	create.SetErr(createErr)
	create.SetArgs([]string{
		"job", "create", "SQU-45",
		"--target", "worker",
		"--kickoff", "finish pr reconciliation",
		"--repo", target,
	})
	if err := create.Execute(); err != nil {
		t.Fatalf("job create: %v\nstderr=%s", err, createErr.String())
	}
	dispatch := NewRootCmd()
	dispatchOut, dispatchErr := &bytes.Buffer{}, &bytes.Buffer{}
	dispatch.SetOut(dispatchOut)
	dispatch.SetErr(dispatchErr)
	dispatch.SetArgs([]string{"job", "dispatch", "squ-45", "--workspace", "worktree", "--repo", target, "--json"})
	if err := dispatch.Execute(); err != nil {
		t.Fatalf("job dispatch: %v\nstderr=%s", err, dispatchErr.String())
	}
	dispatched, err := job.Read(filepath.Join(target, ".agent_team"), "squ-45")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	dispatched.PR = "https://github.com/acme/repo/pull/45"
	if err := job.Write(filepath.Join(target, ".agent_team"), dispatched); err != nil {
		t.Fatalf("write job pr: %v", err)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-45")

	payload, err := json.Marshal(map[string]any{
		"action": "closed",
		"repository": map[string]any{
			"full_name": "acme/repo",
		},
		"pull_request": map[string]any{
			"number":   45,
			"merged":   true,
			"html_url": "https://github.com/acme/repo/pull/45",
			"head": map[string]any{
				"ref": dispatched.Branch,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	preview := NewRootCmd()
	previewOut, previewErr := &bytes.Buffer{}, &bytes.Buffer{}
	preview.SetOut(previewOut)
	preview.SetErr(previewErr)
	preview.SetArgs([]string{
		"job", "reconcile", "github",
		"--payload", string(payload),
		"--cleanup-merged",
		"--dry-run",
		"--repo", target,
		"--json",
	})
	if err := preview.Execute(); err != nil {
		t.Fatalf("job reconcile github dry-run: %v\nstderr=%s", err, previewErr.String())
	}
	var previewResult struct {
		Result struct {
			Job job.Job `json:"job"`
		} `json:"result"`
		CleanupPreview *jobCleanupPreview `json:"cleanup_preview"`
		DryRun         bool               `json:"dry_run"`
	}
	if err := json.Unmarshal(previewOut.Bytes(), &previewResult); err != nil {
		t.Fatalf("decode reconcile dry-run json: %v\nbody=%s", err, previewOut.String())
	}
	if !previewResult.DryRun || previewResult.Result.Job.Status != job.StatusDone {
		t.Fatalf("dry-run reconcile = %+v", previewResult)
	}
	if previewResult.CleanupPreview == nil || !previewResult.CleanupPreview.WouldRemoveWorktree || !previewResult.CleanupPreview.WouldRemoveBranch {
		t.Fatalf("dry-run cleanup preview = %+v", previewResult.CleanupPreview)
	}
	afterPreview, err := job.Read(filepath.Join(target, ".agent_team"), "squ-45")
	if err != nil {
		t.Fatalf("read job after dry-run: %v", err)
	}
	if afterPreview.Status != job.StatusRunning || afterPreview.Worktree != dispatched.Worktree || afterPreview.Branch != dispatched.Branch {
		t.Fatalf("dry-run mutated job = %+v", afterPreview)
	}
	if _, err := os.Stat(dispatched.Worktree); err != nil {
		t.Fatalf("dry-run removed worktree or stat failed: %v", err)
	}
	if !branchExists(t, target, dispatched.Branch) {
		t.Fatalf("dry-run removed branch %s", dispatched.Branch)
	}

	reconcile := NewRootCmd()
	reconcileOut, reconcileErr := &bytes.Buffer{}, &bytes.Buffer{}
	reconcile.SetOut(reconcileOut)
	reconcile.SetErr(reconcileErr)
	reconcile.SetArgs([]string{
		"job", "reconcile", "github",
		"--payload", string(payload),
		"--cleanup-merged",
		"--repo", target,
		"--json",
	})
	if err := reconcile.Execute(); err != nil {
		t.Fatalf("job reconcile github: %v\nstderr=%s", err, reconcileErr.String())
	}
	var result struct {
		Result struct {
			Job job.Job `json:"job"`
		} `json:"result"`
		Cleanup string `json:"cleanup"`
	}
	if err := json.Unmarshal(reconcileOut.Bytes(), &result); err != nil {
		t.Fatalf("decode reconcile json: %v\nbody=%s", err, reconcileOut.String())
	}
	if result.Result.Job.Status != job.StatusDone || result.Result.Job.Worktree != "" || result.Result.Job.Branch != "" {
		t.Fatalf("reconciled job = %+v", result.Result.Job)
	}
	if !strings.Contains(result.Cleanup, "removed") {
		t.Fatalf("cleanup summary = %q", result.Cleanup)
	}
	if _, err := os.Stat(dispatched.Worktree); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists or stat error: %v", err)
	}
	if branchExists(t, target, dispatched.Branch) {
		t.Fatalf("branch %s still exists after reconcile cleanup", dispatched.Branch)
	}
}

func TestJobNextReportsPipelineState(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-203",
		Ticket:    "SQU-203",
		Target:    "manager",
		Kickoff:   "SQU-203: pipeline state",
		Pipeline:  "ticket_triage",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "triage", Target: "manager", Status: job.StatusDone, Instance: "manager", StartedAt: now, FinishedAt: now},
			{ID: "review", Target: "worker", Status: job.StatusBlocked, After: []string{"triage"}},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write ready job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "next", "squ-203", "--repo", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job next ready: %v\nstderr=%s", err, stderr.String())
	}
	var ready jobNextResult
	if err := json.Unmarshal(out.Bytes(), &ready); err != nil {
		t.Fatalf("decode ready json: %v\nbody=%s", err, out.String())
	}
	if ready.State != "ready" || ready.Step == nil || ready.Step.ID != "review" || len(ready.WaitingFor) != 0 {
		t.Fatalf("ready result = %+v", ready)
	}

	j.Steps[1].Status = job.StatusRunning
	j.Steps[1].Instance = "worker-squ-203-review"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write running job: %v", err)
	}
	cmd = NewRootCmd()
	out, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "next", "squ-203", "--repo", tmp, "--format", "{{.State}} {{.Step.ID}} {{.Step.Instance}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job next format: %v\nstderr=%s", err, stderr.String())
	}
	if got := strings.TrimSpace(out.String()); got != "running review worker-squ-203-review" {
		t.Fatalf("formatted next = %q", got)
	}

	j.Steps[1].Status = job.StatusDone
	j.Steps[1].FinishedAt = now
	j.Status = job.StatusDone
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write done job: %v", err)
	}
	cmd = NewRootCmd()
	out, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "next", "squ-203", "--repo", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job next done: %v\nstderr=%s", err, stderr.String())
	}
	var done jobNextResult
	if err := json.Unmarshal(out.Bytes(), &done); err != nil {
		t.Fatalf("decode done json: %v\nbody=%s", err, out.String())
	}
	if done.State != "done" || done.Step != nil || done.Message != "all steps done" {
		t.Fatalf("done result = %+v", done)
	}

	noSteps := &job.Job{
		ID:        "squ-204",
		Ticket:    "SQU-204",
		Target:    "worker",
		Status:    job.StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, noSteps); err != nil {
		t.Fatalf("write no-step job: %v", err)
	}
	cmd = NewRootCmd()
	out, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "next", "squ-204", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job next no steps: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(out.String(), "state=none") || !strings.Contains(out.String(), "job has no pipeline steps") {
		t.Fatalf("no-step output = %q", out.String())
	}
}

func TestJobReadyListsAdvanceablePipelineJobs(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	jobs := []*job.Job{
		{
			ID:        "squ-210",
			Ticket:    "SQU-210",
			Target:    "manager",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusDone, StartedAt: now, FinishedAt: now},
				{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}},
			},
		},
		{
			ID:        "squ-211",
			Ticket:    "SQU-211",
			Target:    "manager",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now.Add(time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-211"},
				{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}},
			},
		},
		{
			ID:        "squ-212",
			Ticket:    "SQU-212",
			Target:    "manager",
			Pipeline:  "nightly",
			Status:    job.StatusDone,
			CreatedAt: now,
			UpdatedAt: now.Add(2 * time.Minute),
			Steps: []job.Step{
				{ID: "triage", Target: "manager", Status: job.StatusDone, StartedAt: now, FinishedAt: now},
			},
		},
		{
			ID:        "squ-213",
			Ticket:    "SQU-213",
			Target:    "worker",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
	for _, j := range jobs {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "ready", "--repo", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job ready json: %v\nstderr=%s", err, stderr.String())
	}
	var rows []jobReadyRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode ready rows: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].JobID != "squ-210" || rows[0].State != "ready" || rows[0].StepID != "review" {
		t.Fatalf("default ready rows = %+v", rows)
	}

	cmd = NewRootCmd()
	out, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "ready", "--repo", tmp, "--pipeline", "ticket_to_pr", "--state", "all", "--format", "{{.JobID}} {{.State}} {{.StepID}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job ready all format: %v\nstderr=%s", err, stderr.String())
	}
	if got := strings.Split(strings.TrimSpace(out.String()), "\n"); strings.Join(got, ",") != "squ-210 ready review,squ-211 running implement" {
		t.Fatalf("formatted ready rows = %q", out.String())
	}

	cmd = NewRootCmd()
	out, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "ready", "--repo", tmp, "--state", ","})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("job ready empty state succeeded")
	}
	if !strings.Contains(stderr.String(), "--state requires at least one non-empty state") {
		t.Fatalf("missing state error:\n%s", stderr.String())
	}
}

func TestJobAdvanceDispatchesNextReadyStep(t *testing.T) {
	target, _, cleanup := setupIntakePipelineRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-201",
		Ticket:    "SQU-201",
		Target:    "manager",
		Kickoff:   "SQU-201: review implementation",
		Pipeline:  "ticket_triage",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "triage", Target: "manager", Status: job.StatusDone, Instance: "manager", StartedAt: now, FinishedAt: now},
			{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"triage"}},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "advance", "squ-201", "--repo", target, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job advance: %v\nstderr=%s", err, stderr.String())
	}
	var result jobAdvanceResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode advance json: %v\nbody=%s", err, out.String())
	}
	if result.Step == nil || result.Step.ID != "review" || result.Step.Status != job.StatusRunning || result.Step.Instance != "manager" {
		t.Fatalf("result = %+v", result)
	}
	updated, err := job.Read(teamDir, "squ-201")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.Steps[1].Status != job.StatusRunning || updated.LastEvent != "advance_messaged" {
		t.Fatalf("updated = %+v", updated)
	}
	messages, err := daemon.ReadMessages(daemon.DaemonRoot(teamDir), "manager")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0].Body, `"pipeline_step":"review"`) {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestJobStepDoneAdvanceDispatchesNextStep(t *testing.T) {
	target, _, cleanup := setupIntakePipelineRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-202",
		Ticket:    "SQU-202",
		Target:    "manager",
		Kickoff:   "SQU-202: triage",
		Pipeline:  "ticket_triage",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "triage", Target: "manager", Status: job.StatusRunning, Instance: "manager", StartedAt: now},
			{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"triage"}},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "step", "squ-202", "triage", "--status", "done", "--advance", "--repo", target, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job step --advance: %v\nstderr=%s", err, stderr.String())
	}
	var result jobAdvanceResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode step advance json: %v\nbody=%s", err, out.String())
	}
	if result.Step == nil || result.Step.ID != "review" || result.Step.Status != job.StatusRunning {
		t.Fatalf("result = %+v", result)
	}
	updated, err := job.Read(teamDir, "squ-202")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.Steps[0].Status != job.StatusDone || updated.Steps[1].Status != job.StatusRunning {
		t.Fatalf("updated steps = %+v", updated.Steps)
	}
}

func initGitRepoForJobTest(t *testing.T, dir string) {
	t.Helper()
	runGitForJobTest(t, dir, "init", "-b", "main")
	runGitForJobTest(t, dir, "config", "user.email", "test@example.com")
	runGitForJobTest(t, dir, "config", "user.name", "Agent Team Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForJobTest(t, dir, "add", ".")
	runGitForJobTest(t, dir, "commit", "-m", "init")
}

func runGitForJobTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, strings.TrimSpace(string(out)))
	}
	return string(out)
}

func branchExists(t *testing.T, dir, branch string) bool {
	t.Helper()
	out := runGitForJobTest(t, dir, "branch", "--list", branch, "--format", "%(refname:short)")
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == branch {
			return true
		}
	}
	return false
}
