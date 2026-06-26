package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/runtimebin"
)

func mustNewJob(t *testing.T, ticket, target string) *job.Job {
	t.Helper()
	j, err := job.New(ticket, target, "test kickoff", time.Now().UTC())
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	return j
}

func queueQuarantineItemIDs(items []queueQuarantineItem) string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.ID)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
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
		!strings.Contains(showOut.String(), "implement the status monitor") ||
		!strings.Contains(showOut.String(), "Actions:") ||
		!strings.Contains(showOut.String(), "agent-team job dispatch squ-42") {
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

	showEvents := NewRootCmd()
	showEventsOut, showEventsErr := &bytes.Buffer{}, &bytes.Buffer{}
	showEvents.SetOut(showEventsOut)
	showEvents.SetErr(showEventsErr)
	showEvents.SetArgs([]string{"job", "show", "SQU-42", "--repo", tmp, "--events", "1"})
	if err := showEvents.Execute(); err != nil {
		t.Fatalf("job show events: %v\nstderr=%s", err, showEventsErr.String())
	}
	for _, want := range []string{"Recent Events:", "TIME", "closed", "done"} {
		if !strings.Contains(showEventsOut.String(), want) {
			t.Fatalf("job show events missing %q:\n%s", want, showEventsOut.String())
		}
	}

	showEventsJSON := NewRootCmd()
	showEventsJSONOut, showEventsJSONErr := &bytes.Buffer{}, &bytes.Buffer{}
	showEventsJSON.SetOut(showEventsJSONOut)
	showEventsJSON.SetErr(showEventsJSONErr)
	showEventsJSON.SetArgs([]string{"job", "show", "SQU-42", "--repo", tmp, "--events", "all", "--json"})
	if err := showEventsJSON.Execute(); err != nil {
		t.Fatalf("job show events json: %v\nstderr=%s", err, showEventsJSONErr.String())
	}
	var showEventsBody struct {
		Job    job.Job     `json:"job"`
		Events []job.Event `json:"events"`
	}
	if err := json.Unmarshal(showEventsJSONOut.Bytes(), &showEventsBody); err != nil {
		t.Fatalf("decode show events json: %v\nbody=%s", err, showEventsJSONOut.String())
	}
	if showEventsBody.Job.ID != "squ-42" || len(showEventsBody.Events) != 2 {
		t.Fatalf("show events json = %+v", showEventsBody)
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

func TestJobCreateKickoffFileFromStdin(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	oldInput := sendMessageInput
	sendMessageInput = strings.NewReader("implement from stdin\n")
	defer func() { sendMessageInput = oldInput }()

	create := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	create.SetOut(out)
	create.SetErr(stderr)
	create.SetArgs([]string{"job", "create", "SQU-99", "--repo", tmp, "--kickoff-file", "-", "--json"})
	if err := create.Execute(); err != nil {
		t.Fatalf("job create kickoff stdin: %v\nstderr=%s", err, stderr.String())
	}
	var created job.Job
	if err := json.Unmarshal(out.Bytes(), &created); err != nil {
		t.Fatalf("decode job create kickoff stdin: %v\nbody=%s", err, out.String())
	}
	if created.ID != "squ-99" || created.Kickoff != "SQU-99: implement from stdin" {
		t.Fatalf("created job = %+v", created)
	}
	persisted, err := job.Read(filepath.Join(tmp, ".agent_team"), "squ-99")
	if err != nil {
		t.Fatalf("read persisted job: %v", err)
	}
	if persisted.Kickoff != "SQU-99: implement from stdin" {
		t.Fatalf("persisted kickoff = %q", persisted.Kickoff)
	}
}

func TestJobCloseRecordsMessage(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-70",
		Ticket:    "SQU-70",
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
	cmd.SetArgs([]string{"job", "close", "squ-70", "--repo", tmp, "--status", "failed", "--message", "superseded by SQU-71", "--actor", "github", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job close message: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var closed job.Job
	if err := json.Unmarshal(out.Bytes(), &closed); err != nil {
		t.Fatalf("decode close json: %v\nbody=%s", err, out.String())
	}
	if closed.Status != job.StatusFailed || closed.LastEvent != "closed" || closed.LastStatus != "superseded by SQU-71" {
		t.Fatalf("closed = %+v", closed)
	}
	events, err := job.ListEvents(teamDir, "squ-70")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 1 || events[0].Type != "closed" || events[0].Actor != "github" || events[0].Message != "superseded by SQU-71" || events[0].Data["status"] != "failed" {
		t.Fatalf("events = %+v", events)
	}
}

func TestJobCloseDryRunDoesNotMutateJob(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	original := &job.Job{
		ID:        "squ-72",
		Ticket:    "SQU-72",
		Target:    "worker",
		Status:    job.StatusRunning,
		LastEvent: "dispatched",
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
	cmd.SetArgs([]string{"job", "close", "squ-72", "--repo", tmp, "--status", "failed", "--message", "preview only", "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job close --dry-run: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var preview jobActionPreview
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil {
		t.Fatalf("decode close preview: %v\nbody=%s", err, out.String())
	}
	if !preview.DryRun || preview.Job == nil || preview.Job.Status != job.StatusFailed || preview.Job.LastEvent != "closed" || preview.Job.LastStatus != "preview only" {
		t.Fatalf("preview = %+v", preview)
	}
	updated, err := job.Read(teamDir, "squ-72")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.Status != job.StatusRunning || updated.LastEvent != "dispatched" || updated.LastStatus != "" || !updated.UpdatedAt.Equal(original.UpdatedAt) {
		t.Fatalf("dry-run mutated job = %+v", updated)
	}
	events, err := job.ListEvents(teamDir, "squ-72")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("dry-run wrote events = %+v", events)
	}
}

func TestJobCancelDryRunDoesNotMutateJob(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	original := &job.Job{
		ID:        "squ-64",
		Ticket:    "SQU-64",
		Target:    "worker",
		Instance:  "worker-squ-64",
		Status:    job.StatusRunning,
		Held:      true,
		HoldUntil: now.Add(time.Hour),
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
	cmd.SetArgs([]string{"job", "cancel", "squ-64", "not needed", "--repo", tmp, "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job cancel --dry-run: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var preview jobCancelResult
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil {
		t.Fatalf("decode cancel preview: %v\nbody=%s", err, out.String())
	}
	if !preview.DryRun || preview.Job == nil || preview.Job.Status != job.StatusFailed || preview.Job.LastEvent != "cancelled" || preview.Job.LastStatus != "not needed" || preview.Job.Held {
		t.Fatalf("preview = %+v", preview)
	}
	updated, err := job.Read(teamDir, "squ-64")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.Status != job.StatusRunning || updated.LastEvent != "" || !updated.Held || !updated.UpdatedAt.Equal(original.UpdatedAt) {
		t.Fatalf("dry-run mutated job = %+v", updated)
	}
}

func TestJobCancelStopsOwningInstanceAndFailsJob(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-job-cancel-")
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
	if _, err := mgr.Dispatch(daemon.DispatchInput{Agent: "worker", Name: "worker-squ-65", Workspace: tmp}); err != nil {
		t.Fatalf("dispatch worker: %v", err)
	}
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-65",
		Ticket:    "SQU-65",
		Target:    "worker",
		Instance:  "worker-squ-65",
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
	cmd.SetArgs([]string{"job", "cancel", "squ-65", "--repo", tmp, "--message", "operator cancelled", "--actor", "ops", "--stop", "--wait", "--timeout", "2s", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job cancel --stop: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var result jobCancelResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode cancel result: %v\nbody=%s", err, out.String())
	}
	if result.Job == nil || result.Job.Status != job.StatusFailed || result.Job.LastEvent != "cancelled" || result.Job.LastStatus != "operator cancelled" {
		t.Fatalf("cancel result job = %+v", result.Job)
	}
	if len(result.InstanceActions) != 1 || result.InstanceActions[0].Action != "stop" || result.InstanceActions[0].Instance != "worker-squ-65" || result.InstanceActions[0].Status != "stopped" {
		t.Fatalf("instance actions = %+v", result.InstanceActions)
	}
	updated, err := job.Read(teamDir, "squ-65")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.Status != job.StatusFailed || updated.LastEvent != "cancelled" || updated.LastStatus != "operator cancelled" {
		t.Fatalf("updated job = %+v", updated)
	}
	events, err := job.ListEvents(teamDir, "squ-65")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 1 || events[0].Type != "cancelled" || events[0].Actor != "ops" || events[0].Data["instance_action"] != "stop" {
		t.Fatalf("events = %+v", events)
	}
}

func TestJobAdoptUsesJobDefaultsAndUpdatesJob(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	worktree := filepath.Join(tmp, "worktrees", "squ-68")
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-68",
		Ticket:    "SQU-68",
		Target:    "worker",
		Status:    job.StatusQueued,
		Branch:    "squ-68-existing",
		Worktree:  worktree,
		PR:        "https://github.com/example/repo/pull/68",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "adopt", "squ-68", "--repo", tmp, "--pid", strconv.Itoa(os.Getpid()), "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job adopt: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var result daemonAdoptResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode job adopt result: %v\nbody=%s", err, out.String())
	}
	if result.Metadata == nil || result.Metadata.Instance != "worker-squ-68" || result.Metadata.Agent != "worker" || result.Metadata.Job != "squ-68" || result.Metadata.Workspace != worktree || result.Metadata.Branch != "squ-68-existing" || result.Metadata.PR != "https://github.com/example/repo/pull/68" {
		t.Fatalf("metadata = %+v", result.Metadata)
	}
	if result.Job == nil || !result.JobChanged || result.Job.Status != job.StatusRunning || result.Job.Instance != "worker-squ-68" || result.Job.LastEvent != "adopted" {
		t.Fatalf("job adopt result = %+v", result)
	}
	updated, err := job.Read(teamDir, "squ-68")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.Status != job.StatusRunning || updated.Instance != "worker-squ-68" || updated.Branch != "squ-68-existing" || updated.PR != "https://github.com/example/repo/pull/68" || updated.LastEvent != "adopted" {
		t.Fatalf("updated job = %+v", updated)
	}
}

func TestJobAdoptDryRunDoesNotMutateJobOrMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-69",
		Ticket:    "SQU-69",
		Target:    "worker",
		Status:    job.StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}
	pidPath := filepath.Join(tmp, "worker.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "adopt", "squ-69", "--repo", tmp, "--pid-file", pidPath, "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job adopt --dry-run: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var result daemonAdoptResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode job adopt dry-run result: %v\nbody=%s", err, out.String())
	}
	if !result.DryRun || result.Job == nil || !result.JobChanged || result.Job.Status != job.StatusRunning || result.Job.Instance != "worker-squ-69" {
		t.Fatalf("dry-run result = %+v", result)
	}
	if result.Metadata == nil || result.Metadata.PID != os.Getpid() {
		t.Fatalf("dry-run metadata = %+v, want pid %d", result.Metadata, os.Getpid())
	}
	unchanged, err := job.Read(teamDir, "squ-69")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if unchanged.Status != job.StatusQueued || unchanged.Instance != "" || unchanged.LastEvent != "" {
		t.Fatalf("dry-run mutated job = %+v", unchanged)
	}
	if _, err := daemon.ReadMetadata(daemon.DaemonRoot(teamDir), "worker-squ-69"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("metadata should not exist after dry-run: %v", err)
	}
}

func TestJobShowDisplaysRuntimeMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-45",
		Ticket:    "SQU-45",
		Target:    "worker",
		Instance:  "worker-squ-45",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Instance: "worker-squ-45", Status: job.StatusRunning},
			{ID: "review", Target: "manager", Instance: "manager-review", Status: job.StatusBlocked, After: []string{"implement"}},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	for _, meta := range []*daemon.Metadata{
		{Instance: "worker-squ-45", Agent: "worker", Runtime: string(runtimebin.KindCodex), RuntimeBinary: "codex-dev", Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: tmp, StartedAt: now},
		{Instance: "manager-review", Agent: "manager", Runtime: string(runtimebin.KindClaude), RuntimeBinary: "claude-code", Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: tmp, StartedAt: now},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "show", "squ-45", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job show runtime metadata: %v\nstderr=%s", err, stderr.String())
	}
	for _, want := range []string{
		"Instance:    worker-squ-45",
		"Runtime:     codex",
		"Runtime Bin: codex-dev",
		"implement  target=worker status=running instance=worker-squ-45 after=- runtime=codex runtime_bin=codex-dev",
		"review  target=manager status=blocked instance=manager-review after=implement runtime=claude runtime_bin=claude-code",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("job show missing %q:\n%s", want, out.String())
		}
	}
}

func TestJobShowSuggestsRuntimeResumePlanForCrashedMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC().Truncate(time.Second)
	j := mustNewJob(t, "SQU-46", "worker")
	j.Status = job.StatusRunning
	j.UpdatedAt = now
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	meta := &daemon.Metadata{
		Instance:  "worker-squ-46",
		Agent:     "worker",
		Job:       "squ-46",
		Runtime:   string(runtimebin.KindCodex),
		Status:    daemon.StatusCrashed,
		Workspace: tmp,
		StartedAt: now.Add(-time.Hour),
		ExitedAt:  now,
	}
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "show", "squ-46", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job show crashed runtime: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(out.String(), "agent-team job resume-plan squ-46 --status crashed") {
		t.Fatalf("job show missing runtime resume action:\n%s", out.String())
	}
}

func TestJobShowIncludesStatusPreview(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC().Truncate(time.Second)
	j := mustNewJob(t, "SQU-208", "worker")
	j.Status = job.StatusQueued
	j.UpdatedAt = now
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write queued job: %v", err)
	}
	writeStatus(t, filepath.Join(teamDir, "state", "worker-squ-208"), `[status]
phase = "blocked"
description = "needs token"
since = "2026-06-18T12:00:00Z"

[work]
job = "squ-208"
ticket = "SQU-208"
branch = "worker-squ-208"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "show", "SQU-208", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job show: %v\nstderr=%s", err, stderr.String())
	}
	for _, want := range []string{
		"Status Preview:",
		"worker-squ-208",
		"phase=blocked",
		"before=queued",
		"after=blocked",
		"action=would_update",
		"needs token",
		"Actions:",
		"agent-team job unblock squ-208 <answer...>",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("job show missing %q:\n%s", want, out.String())
		}
	}
	updated, err := job.Read(teamDir, "squ-208")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.Status != job.StatusQueued {
		t.Fatalf("job show should not mutate job status: %+v", updated)
	}
}

func TestJobShowIncludesCleanupActionForDoneOwnedWorktree(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-209",
		Ticket:    "SQU-209",
		Target:    "worker",
		Status:    job.StatusDone,
		Branch:    "worktree-worker-squ-209",
		Worktree:  filepath.Join(tmp, ".claude", "worktrees", "worker-squ-209"),
		PR:        "https://github.com/acme/repo/pull/209",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write cleanup-ready job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "show", "squ-209", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job show cleanup-ready: %v\nstderr=%s", err, stderr.String())
	}
	for _, want := range []string{
		"Worktree:",
		"worktree-worker-squ-209",
		"Actions:",
		"agent-team job cleanup squ-209 --dry-run",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("job show cleanup-ready missing %q:\n%s", want, out.String())
		}
	}
}

func TestJobShowIncludesLastMessageActionWhenSidecarExists(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-210",
		Ticket:    "SQU-210",
		Target:    "worker",
		Status:    job.StatusDone,
		Instance:  "worker-squ-210",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	writeLastMessageForTest(t, teamDir, "worker-squ-210", "clean final")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "show", "squ-210", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job show last-message action: %v\nstderr=%s", err, stderr.String())
	}
	for _, want := range []string{
		"Actions:",
		"agent-team job logs squ-210 --last-message",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("job show missing %q:\n%s", want, out.String())
		}
	}
}

func TestJobCreateDryRunDoesNotWrite(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"job", "create", "SQU-43",
		"--target", "worker",
		"--kickoff", "preview this job",
		"--repo", tmp,
		"--dry-run",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job create dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var preview jobCreatePreview
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil {
		t.Fatalf("decode job create dry-run json: %v\nbody=%s", err, out.String())
	}
	if !preview.DryRun || preview.Job == nil || preview.Job.ID != "squ-43" || preview.Job.Kickoff != "SQU-43: preview this job" {
		t.Fatalf("preview = %+v", preview)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".agent_team", "jobs", "squ-43.toml")); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote job file, err=%v", err)
	}

	textCmd := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	textCmd.SetOut(textOut)
	textCmd.SetErr(textErr)
	textCmd.SetArgs([]string{"job", "create", "SQU-44", "--target", "worker", "--repo", tmp, "--dry-run"})
	if err := textCmd.Execute(); err != nil {
		t.Fatalf("job create dry-run text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"Dry run: true", "ID:          squ-44", "Status:      queued"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("dry-run text missing %q:\n%s", want, textOut.String())
		}
	}

	dispatchCmd := NewRootCmd()
	dispatchOut, dispatchErr := &bytes.Buffer{}, &bytes.Buffer{}
	dispatchCmd.SetOut(dispatchOut)
	dispatchCmd.SetErr(dispatchErr)
	dispatchCmd.SetArgs([]string{"job", "create", "SQU-45", "--repo", tmp, "--dry-run", "--dispatch", "--json"})
	if err := dispatchCmd.Execute(); err != nil {
		t.Fatalf("job create --dry-run --dispatch: %v\nstderr=%s", err, dispatchErr.String())
	}
	var dispatchPreview jobDispatchPreview
	if err := json.Unmarshal(dispatchOut.Bytes(), &dispatchPreview); err != nil {
		t.Fatalf("decode create dispatch dry-run json: %v\nbody=%s", err, dispatchOut.String())
	}
	if !dispatchPreview.DryRun || dispatchPreview.Job == nil || dispatchPreview.Job.ID != "squ-45" {
		t.Fatalf("dispatch preview = %+v", dispatchPreview)
	}
	if dispatchPreview.Dispatch == nil || dispatchPreview.Dispatch.RequestedName != "worker-squ-45" {
		t.Fatalf("dispatch route preview = %+v", dispatchPreview.Dispatch)
	}
	payload := dispatchPreview.Dispatch.Preview.Payload
	if payload["job_id"] != "squ-45" || payload["workspace"] != "worktree" {
		t.Fatalf("dispatch payload = %+v", payload)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".agent_team", "jobs", "squ-45.toml")); !os.IsNotExist(err) {
		t.Fatalf("dispatch dry-run wrote job file, err=%v", err)
	}

	pipelineCmd := NewRootCmd()
	pipelineOut, pipelineErr := &bytes.Buffer{}, &bytes.Buffer{}
	pipelineCmd.SetOut(pipelineOut)
	pipelineCmd.SetErr(pipelineErr)
	pipelineCmd.SetArgs([]string{"job", "create", "SQU-46", "--repo", tmp, "--pipeline", "ticket_to_pr", "--dry-run", "--dispatch", "--json", "--runtime", "codex", "--runtime-bin", "codex-dev"})
	if err := pipelineCmd.Execute(); err != nil {
		t.Fatalf("pipeline job create --dry-run --dispatch: %v\nstderr=%s", err, pipelineErr.String())
	}
	var advancePreview jobAdvancePreview
	if err := json.Unmarshal(pipelineOut.Bytes(), &advancePreview); err != nil {
		t.Fatalf("decode pipeline dispatch dry-run json: %v\nbody=%s", err, pipelineOut.String())
	}
	if !advancePreview.DryRun || advancePreview.Job == nil || advancePreview.Job.ID != "squ-46" || advancePreview.Step == nil || advancePreview.Step.ID != "implement" {
		t.Fatalf("advance preview = %+v", advancePreview)
	}
	if advancePreview.Dispatch == nil || advancePreview.Dispatch.RequestedName != "worker-squ-46-implement" {
		t.Fatalf("advance route preview = %+v", advancePreview.Dispatch)
	}
	advancePayload := advancePreview.Dispatch.Preview.Payload
	if advancePayload["pipeline"] != "ticket_to_pr" ||
		advancePayload["pipeline_step"] != "implement" ||
		advancePayload["job_id"] != "squ-46" ||
		advancePayload["workspace"] != "worktree" ||
		advancePayload["runtime"] != "codex" ||
		advancePayload["runtime_binary"] != "codex-dev" {
		t.Fatalf("advance payload = %+v", advancePayload)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".agent_team", "jobs", "squ-46.toml")); !os.IsNotExist(err) {
		t.Fatalf("pipeline dispatch dry-run wrote job file, err=%v", err)
	}
}

func TestJobDispatchDryRunDoesNotMutate(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	j := mustNewJob(t, "SQU-244", "worker")
	j.Kickoff = "SQU-244: preview dispatch"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "dispatch", "squ-244", "--repo", tmp, "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job dispatch dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var preview jobDispatchPreview
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil {
		t.Fatalf("decode job dispatch dry-run json: %v\nbody=%s", err, out.String())
	}
	if !preview.DryRun || preview.Job == nil || preview.Job.ID != "squ-244" {
		t.Fatalf("preview = %+v", preview)
	}
	if preview.Dispatch == nil || preview.Dispatch.RequestedName != "worker-squ-244" || preview.Dispatch.Target != "worker" {
		t.Fatalf("dispatch preview = %+v", preview.Dispatch)
	}
	if preview.Dispatch.Preview == nil || len(preview.Dispatch.Preview.Matched) != 1 || preview.Dispatch.Preview.Matched[0] != "worker" {
		t.Fatalf("route preview = %+v", preview.Dispatch.Preview)
	}
	payload := preview.Dispatch.Preview.Payload
	if payload["job_id"] != "squ-244" || payload["job"] != "squ-244" || payload["workspace"] != "worktree" {
		t.Fatalf("payload = %+v", payload)
	}
	unchanged, err := job.Read(teamDir, "squ-244")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if unchanged.Status != job.StatusQueued || unchanged.Instance != "" || unchanged.LastEvent != "" {
		t.Fatalf("dry-run mutated job = %+v", unchanged)
	}
	events, err := job.ListEvents(teamDir, "squ-244")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("dry-run wrote job events = %+v", events)
	}

	textCmd := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	textCmd.SetOut(textOut)
	textCmd.SetErr(textErr)
	textCmd.SetArgs([]string{"job", "dispatch", "squ-244", "--repo", tmp, "--dry-run"})
	if err := textCmd.Execute(); err != nil {
		t.Fatalf("job dispatch dry-run text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"Job: squ-244 dry-run dispatch", "instance=worker-squ-244", "Matched: worker"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("dry-run text missing %q:\n%s", want, textOut.String())
		}
	}
}

func TestJobCreateFromTicketURLUsesTicketSlugID(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	ticketURL := "https://linear.app/squirtlesquad/issue/SQU-82/create-from-url"

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "create", ticketURL, "--repo", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job create url: %v\nstderr=%s", err, stderr.String())
	}
	var created job.Job
	if err := json.Unmarshal(out.Bytes(), &created); err != nil {
		t.Fatalf("decode create url json: %v\nbody=%s", err, out.String())
	}
	if created.ID != "squ-82" || created.Ticket != "SQU-82" || created.TicketURL != ticketURL {
		t.Fatalf("created = %+v", created)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".agent_team", "jobs", "squ-82.toml")); err != nil {
		t.Fatalf("expected slug job file: %v", err)
	}
	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"job", "show", ticketURL, "--repo", tmp, "--json"})
	if err := show.Execute(); err != nil {
		t.Fatalf("job show url: %v\nstderr=%s", err, showErr.String())
	}
	var shown job.Job
	if err := json.Unmarshal(showOut.Bytes(), &shown); err != nil {
		t.Fatalf("decode show url json: %v\nbody=%s", err, showOut.String())
	}
	if shown.ID != "squ-82" || shown.TicketURL != ticketURL {
		t.Fatalf("shown = %+v", shown)
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
	stamp := "20260619T010203.000000000Z"
	quarantinePath := filepath.Join("quarantine", stamp, daemon.QueueStatePending, "q-job-show-quarantined.json")
	writeQuarantinedQueueItem(t, teamDir, stamp, daemon.QueueStatePending, &daemon.QueueItem{
		ID:         "q-job-show-quarantined",
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-109",
		Payload: map[string]any{
			"job_id": "squ-109",
			"ticket": "SQU-109",
			"target": "worker",
		},
		QueuedAt:  now.Add(-2 * time.Hour),
		UpdatedAt: now.Add(-2 * time.Hour),
	})
	writeQuarantinedQueueItem(t, teamDir, stamp, daemon.QueueStatePending, &daemon.QueueItem{
		ID:         "q-job-show-other",
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-999",
		Payload: map[string]any{
			"job_id": "squ-999",
			"ticket": "SQU-999",
			"target": "worker",
		},
		QueuedAt:  now.Add(-30 * time.Minute),
		UpdatedAt: now.Add(-30 * time.Minute),
	})

	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"job", "show", "SQU-109", "--repo", tmp})
	if err := show.Execute(); err != nil {
		t.Fatalf("job show: %v\nstderr=%s", err, showErr.String())
	}
	for _, want := range []string{
		"Queue:",
		"q-job-show",
		"state=dead",
		"instance_id=worker-squ-109",
		"Queue Quarantine:",
		quarantinePath,
		"q-job-show-quarantined",
		"restorable=yes",
		"Actions:",
		"agent-team job queue retry squ-109 q-job-show",
		"agent-team job queue quarantine squ-109",
		fmt.Sprintf("agent-team job queue quarantine restore squ-109 %s --dry-run", quarantinePath),
	} {
		if !strings.Contains(showOut.String(), want) {
			t.Fatalf("job show missing %q:\n%s", want, showOut.String())
		}
	}
	if strings.Contains(showOut.String(), "q-job-show-other") {
		t.Fatalf("job show leaked unrelated quarantined item:\n%s", showOut.String())
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

func TestJobQueueListsOwnedItems(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	j, err := job.New("SQU-120", "worker", "inspect queued work", time.Now())
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.Instance = "worker-squ-120"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-job-ready",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-120",
			Payload: map[string]any{
				"runtime": "codex",
				"target":  "worker",
			},
			QueuedAt:  now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-2 * time.Hour),
		},
		{
			ID:         "q-job-delayed",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-120-delayed",
			Payload: map[string]any{
				"job_id":  "squ-120",
				"runtime": "claude",
				"target":  "worker",
			},
			NextRetry: now.Add(time.Hour),
			QueuedAt:  now.Add(-time.Hour),
			UpdatedAt: now.Add(-time.Hour),
		},
		{
			ID:         "q-other",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-121",
			Payload: map[string]any{
				"job_id":  "squ-121",
				"runtime": "codex",
				"target":  "worker",
			},
			QueuedAt:  now.Add(-30 * time.Minute),
			UpdatedAt: now.Add(-30 * time.Minute),
		},
		{
			ID:         "q-job-dead",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-120",
			Payload: map[string]any{
				"job_id":  "squ-120",
				"runtime": "codex",
				"ticket":  "SQU-120",
				"target":  "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-3 * time.Hour),
			DeadLetteredAt: now.Add(-3 * time.Hour),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("WriteQueueItem %s: %v", item.ID, err)
		}
	}

	list := NewRootCmd()
	listOut, listErr := &bytes.Buffer{}, &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetErr(listErr)
	list.SetArgs([]string{"job", "queue", "SQU-120", "--repo", tmp, "--json"})
	if err := list.Execute(); err != nil {
		t.Fatalf("job queue json: %v\nstderr=%s", err, listErr.String())
	}
	var items []daemon.QueueItem
	if err := json.Unmarshal(listOut.Bytes(), &items); err != nil {
		t.Fatalf("decode job queue json: %v\nbody=%s", err, listOut.String())
	}
	if got := strings.Join(queueItemIDs(items), ","); got != "q-job-dead,q-job-ready,q-job-delayed" {
		t.Fatalf("job queue ids = %s", got)
	}

	sorted := NewRootCmd()
	sortedOut, sortedErr := &bytes.Buffer{}, &bytes.Buffer{}
	sorted.SetOut(sortedOut)
	sorted.SetErr(sortedErr)
	sorted.SetArgs([]string{"job", "queue", "SQU-120", "--repo", tmp, "--sort", "attempts", "--limit", "1", "--format", "{{.ID}}"})
	if err := sorted.Execute(); err != nil {
		t.Fatalf("job queue sort/limit: %v\nstderr=%s", err, sortedErr.String())
	}
	if got := strings.TrimSpace(sortedOut.String()); got != "q-job-dead" {
		t.Fatalf("job queue sort/limit output = %q", sortedOut.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var watchOut bytes.Buffer
	if err := runJobQueueListWatch(ctx, &watchOut, teamDir, j, queueListFilters{}, queueListOptions{Sort: "attempts", Limit: 1}, false, nil, time.Millisecond, false); err != nil {
		t.Fatalf("runJobQueueListWatch: %v", err)
	}
	if got := watchOut.String(); !strings.Contains(got, "q-job-dead") || strings.Contains(got, "q-job-ready") || strings.Contains(got, watchClearSequence) {
		t.Fatalf("job queue watch output = %q", got)
	}

	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	var summaryWatchOut bytes.Buffer
	if err := runJobQueueSummaryWatch(ctx, &summaryWatchOut, teamDir, j, queueListFilters{}, false, time.Millisecond, false); err != nil {
		t.Fatalf("runJobQueueSummaryWatch: %v", err)
	}
	if got := summaryWatchOut.String(); !strings.Contains(got, "queue: total=3 pending=2 dead=1") || strings.Contains(got, watchClearSequence) {
		t.Fatalf("job queue summary watch output = %q", got)
	}

	textList := NewRootCmd()
	textListOut, textListErr := &bytes.Buffer{}, &bytes.Buffer{}
	textList.SetOut(textListOut)
	textList.SetErr(textListErr)
	textList.SetArgs([]string{"job", "queue", "SQU-120", "--repo", tmp})
	if err := textList.Execute(); err != nil {
		t.Fatalf("job queue text: %v\nstderr=%s", err, textListErr.String())
	}
	for _, want := range []string{
		"agent-team job queue retry squ-120 q-job-dead; agent-team job queue drop squ-120 q-job-dead",
		"agent-team queue drain; agent-team job queue drop squ-120 q-job-ready",
		"agent-team job queue show squ-120 q-job-delayed; agent-team job queue drop squ-120 q-job-delayed",
	} {
		if !strings.Contains(textListOut.String(), want) {
			t.Fatalf("job queue text missing %q:\n%s", want, textListOut.String())
		}
	}

	runtimeList := NewRootCmd()
	runtimeListOut, runtimeListErr := &bytes.Buffer{}, &bytes.Buffer{}
	runtimeList.SetOut(runtimeListOut)
	runtimeList.SetErr(runtimeListErr)
	runtimeList.SetArgs([]string{"job", "queue", "SQU-120", "--repo", tmp, "--runtime", "codex", "--json"})
	if err := runtimeList.Execute(); err != nil {
		t.Fatalf("job queue runtime json: %v\nstderr=%s", err, runtimeListErr.String())
	}
	var runtimeItems []daemon.QueueItem
	if err := json.Unmarshal(runtimeListOut.Bytes(), &runtimeItems); err != nil {
		t.Fatalf("decode job queue runtime json: %v\nbody=%s", err, runtimeListOut.String())
	}
	if got := strings.Join(queueItemIDs(runtimeItems), ","); got != "q-job-dead,q-job-ready" {
		t.Fatalf("job queue runtime ids = %s", got)
	}

	showText := NewRootCmd()
	showTextOut, showTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	showText.SetOut(showTextOut)
	showText.SetErr(showTextErr)
	showText.SetArgs([]string{"job", "queue", "show", "SQU-120", "q-job-dead", "--repo", tmp})
	if err := showText.Execute(); err != nil {
		t.Fatalf("job queue show text: %v\nstderr=%s", err, showTextErr.String())
	}
	for _, want := range []string{"Runtime:     codex", "agent-team job queue retry squ-120 q-job-dead", "agent-team job queue drop squ-120 q-job-dead"} {
		if !strings.Contains(showTextOut.String(), want) {
			t.Fatalf("job queue show missing %q:\n%s", want, showTextOut.String())
		}
	}

	showReady := NewRootCmd()
	showReadyOut, showReadyErr := &bytes.Buffer{}, &bytes.Buffer{}
	showReady.SetOut(showReadyOut)
	showReady.SetErr(showReadyErr)
	showReady.SetArgs([]string{"job", "queue", "show", "SQU-120", "q-job-ready", "--repo", tmp})
	if err := showReady.Execute(); err != nil {
		t.Fatalf("job queue show ready text: %v\nstderr=%s", err, showReadyErr.String())
	}
	for _, want := range []string{"Runtime:     codex", "agent-team queue drain", "agent-team job queue drop squ-120 q-job-ready"} {
		if !strings.Contains(showReadyOut.String(), want) {
			t.Fatalf("job queue show ready missing %q:\n%s", want, showReadyOut.String())
		}
	}

	showOther := NewRootCmd()
	showOtherOut, showOtherErr := &bytes.Buffer{}, &bytes.Buffer{}
	showOther.SetOut(showOtherOut)
	showOther.SetErr(showOtherErr)
	showOther.SetArgs([]string{"job", "queue", "show", "SQU-120", "q-other", "--repo", tmp, "--json"})
	if err := showOther.Execute(); err == nil {
		t.Fatalf("job queue show unrelated item unexpectedly succeeded: stdout=%s", showOtherOut.String())
	}
	if !strings.Contains(showOtherErr.String(), "not owned by job") {
		t.Fatalf("job queue show unrelated stderr = %q", showOtherErr.String())
	}

	ready := NewRootCmd()
	readyOut, readyErr := &bytes.Buffer{}, &bytes.Buffer{}
	ready.SetOut(readyOut)
	ready.SetErr(readyErr)
	ready.SetArgs([]string{"job", "queue", "SQU-120", "--repo", tmp, "--ready", "--format", "{{.ID}} {{.State}}"})
	if err := ready.Execute(); err != nil {
		t.Fatalf("job queue ready format: %v\nstderr=%s", err, readyErr.String())
	}
	if got, want := strings.TrimSpace(readyOut.String()), "q-job-ready pending"; got != want {
		t.Fatalf("job queue ready output = %q, want %q", got, want)
	}

	summary := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(summaryOut)
	summary.SetErr(summaryErr)
	summary.SetArgs([]string{"job", "queue", "SQU-120", "--repo", tmp, "--summary", "--json"})
	if err := summary.Execute(); err != nil {
		t.Fatalf("job queue summary json: %v\nstderr=%s", err, summaryErr.String())
	}
	var summaryResult queueSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &summaryResult); err != nil {
		t.Fatalf("decode job queue summary: %v\nbody=%s", err, summaryOut.String())
	}
	if summaryResult.Total != 3 || summaryResult.Pending != 2 || summaryResult.Dead != 1 || summaryResult.Delayed != 1 || summaryResult.Attempts != daemon.MaxQueueAttempts || summaryResult.Runtimes["codex"] != 2 || summaryResult.Runtimes["claude"] != 1 {
		t.Fatalf("job queue summary = %+v", summaryResult)
	}

	runtimeSummaryCmd := NewRootCmd()
	runtimeSummaryOut, runtimeSummaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	runtimeSummaryCmd.SetOut(runtimeSummaryOut)
	runtimeSummaryCmd.SetErr(runtimeSummaryErr)
	runtimeSummaryCmd.SetArgs([]string{"job", "queue", "SQU-120", "--repo", tmp, "--summary", "--runtime", "codex", "--json"})
	if err := runtimeSummaryCmd.Execute(); err != nil {
		t.Fatalf("job queue runtime summary json: %v\nstderr=%s", err, runtimeSummaryErr.String())
	}
	var runtimeSummary queueSummary
	if err := json.Unmarshal(runtimeSummaryOut.Bytes(), &runtimeSummary); err != nil {
		t.Fatalf("decode job queue runtime summary: %v\nbody=%s", err, runtimeSummaryOut.String())
	}
	if runtimeSummary.Total != 2 || runtimeSummary.Pending != 1 || runtimeSummary.Dead != 1 || runtimeSummary.Delayed != 0 || runtimeSummary.Runtimes["codex"] != 2 {
		t.Fatalf("job queue runtime summary = %+v", runtimeSummary)
	}
}

func TestJobQueueRejectsNegativeWatchInterval(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	j := mustNewJob(t, "SQU-122", "worker")
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "queue", "SQU-122", "--repo", tmp, "--watch", "--interval", "-1s"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("job queue negative interval succeeded")
	}
	if !strings.Contains(stderr.String(), "--interval must be >= 0") {
		t.Fatalf("negative interval stderr = %q", stderr.String())
	}
}

func TestJobQueueRetryDropScopesOwnedItems(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	j, err := job.New("SQU-121", "worker", "recover queued work", time.Now())
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.Instance = "worker-squ-121"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-job-pending",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-121",
			Payload: map[string]any{
				"job_id":  "squ-121",
				"runtime": "codex",
				"target":  "worker",
			},
			QueuedAt:  now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-2 * time.Hour),
		},
		{
			ID:         "q-job-pending-claude",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-121-claude",
			Payload: map[string]any{
				"job_id":  "squ-121",
				"runtime": "claude",
				"target":  "worker",
			},
			QueuedAt:  now.Add(-90 * time.Minute),
			UpdatedAt: now.Add(-90 * time.Minute),
		},
		{
			ID:         "q-job-dead",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-121",
			Payload: map[string]any{
				"job_id":  "squ-121",
				"runtime": "codex",
				"ticket":  "SQU-121",
				"target":  "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-3 * time.Hour),
			DeadLetteredAt: now.Add(-3 * time.Hour),
		},
		{
			ID:         "q-job-dead-claude",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-121-claude",
			Payload: map[string]any{
				"job_id":  "squ-121",
				"runtime": "claude",
				"ticket":  "SQU-121",
				"target":  "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-4 * time.Hour),
			UpdatedAt:      now.Add(-4 * time.Hour),
			DeadLetteredAt: now.Add(-4 * time.Hour),
		},
		{
			ID:         "q-other-dead",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-122",
			Payload: map[string]any{
				"job_id":  "squ-122",
				"runtime": "codex",
				"ticket":  "SQU-122",
				"target":  "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-time.Hour),
			UpdatedAt:      now.Add(-time.Hour),
			DeadLetteredAt: now.Add(-time.Hour),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("WriteQueueItem %s: %v", item.ID, err)
		}
	}

	retryDry := NewRootCmd()
	retryDryOut, retryDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	retryDry.SetOut(retryDryOut)
	retryDry.SetErr(retryDryErr)
	retryDry.SetArgs([]string{"job", "queue", "retry", "SQU-121", "--repo", tmp, "--all", "--runtime", "codex", "--dry-run", "--json"})
	if err := retryDry.Execute(); err != nil {
		t.Fatalf("job queue retry --all dry-run: %v\nstderr=%s", err, retryDryErr.String())
	}
	var retryDryResults []queueRetryResult
	if err := json.Unmarshal(retryDryOut.Bytes(), &retryDryResults); err != nil {
		t.Fatalf("decode retry dry-run: %v\nbody=%s", err, retryDryOut.String())
	}
	if len(retryDryResults) != 1 || retryDryResults[0].ID != "q-job-dead" || retryDryResults[0].Action != "would_retry" || !retryDryResults[0].DryRun {
		t.Fatalf("retry dry-run results = %+v", retryDryResults)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-dead"); err != nil || item.State != daemon.QueueStateDead {
		t.Fatalf("retry dry-run changed item=%+v err=%v", item, err)
	}

	dropDryAll := NewRootCmd()
	dropDryAllOut, dropDryAllErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropDryAll.SetOut(dropDryAllOut)
	dropDryAll.SetErr(dropDryAllErr)
	dropDryAll.SetArgs([]string{"job", "queue", "drop", "SQU-121", "--repo", tmp, "--all", "--state", "pending", "--runtime", "codex", "--dry-run", "--json"})
	if err := dropDryAll.Execute(); err != nil {
		t.Fatalf("job queue drop --all runtime dry-run: %v\nstderr=%s", err, dropDryAllErr.String())
	}
	var dropDryAllResults []queueDropResult
	if err := json.Unmarshal(dropDryAllOut.Bytes(), &dropDryAllResults); err != nil {
		t.Fatalf("decode drop all dry-run: %v\nbody=%s", err, dropDryAllOut.String())
	}
	if len(dropDryAllResults) != 1 || dropDryAllResults[0].ID != "q-job-pending" || dropDryAllResults[0].Action != "would_drop" || !dropDryAllResults[0].DryRun {
		t.Fatalf("drop all dry-run results = %+v", dropDryAllResults)
	}

	retry := NewRootCmd()
	retryOut, retryErr := &bytes.Buffer{}, &bytes.Buffer{}
	retry.SetOut(retryOut)
	retry.SetErr(retryErr)
	retry.SetArgs([]string{"job", "queue", "retry", "SQU-121", "q-job-dead", "--repo", tmp, "--format", "{{.ID}} {{.Action}} {{.State}}"})
	if err := retry.Execute(); err != nil {
		t.Fatalf("job queue retry single: %v\nstderr=%s", err, retryErr.String())
	}
	if got, want := strings.TrimSpace(retryOut.String()), "q-job-dead reset dead"; got != want {
		t.Fatalf("retry output = %q, want %q", got, want)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-dead"); err != nil || item.State != daemon.QueueStatePending || item.LastError != "" || !item.DeadLetteredAt.IsZero() {
		t.Fatalf("retried item=%+v err=%v", item, err)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-other-dead"); err != nil || item.State != daemon.QueueStateDead {
		t.Fatalf("unrelated retry item changed=%+v err=%v", item, err)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-dead-claude"); err != nil || item.State != daemon.QueueStateDead {
		t.Fatalf("claude retry item changed=%+v err=%v", item, err)
	}

	dropOther := NewRootCmd()
	dropOtherOut, dropOtherErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropOther.SetOut(dropOtherOut)
	dropOther.SetErr(dropOtherErr)
	dropOther.SetArgs([]string{"job", "queue", "drop", "SQU-121", "q-other-dead", "--repo", tmp, "--dry-run"})
	if err := dropOther.Execute(); err == nil {
		t.Fatalf("job queue drop unrelated item unexpectedly succeeded: stdout=%s", dropOtherOut.String())
	}
	if !strings.Contains(dropOtherErr.String(), "not owned by job") {
		t.Fatalf("drop unrelated stderr = %q", dropOtherErr.String())
	}

	drop := NewRootCmd()
	dropOut, dropErr := &bytes.Buffer{}, &bytes.Buffer{}
	drop.SetOut(dropOut)
	drop.SetErr(dropErr)
	drop.SetArgs([]string{"job", "queue", "drop", "SQU-121", "q-job-pending", "--repo", tmp, "--json"})
	if err := drop.Execute(); err != nil {
		t.Fatalf("job queue drop single: %v\nstderr=%s", err, dropErr.String())
	}
	var dropResults []queueDropResult
	if err := json.Unmarshal(dropOut.Bytes(), &dropResults); err != nil {
		t.Fatalf("decode drop result: %v\nbody=%s", err, dropOut.String())
	}
	if len(dropResults) != 1 || dropResults[0].ID != "q-job-pending" || dropResults[0].Action != "dropped" {
		t.Fatalf("drop results = %+v", dropResults)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-pending"); !os.IsNotExist(err) {
		t.Fatalf("dropped item err=%v, want not exist", err)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-other-dead"); err != nil || item.State != daemon.QueueStateDead {
		t.Fatalf("unrelated drop item changed=%+v err=%v", item, err)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-pending-claude"); err != nil || item.State != daemon.QueueStatePending {
		t.Fatalf("claude drop item changed=%+v err=%v", item, err)
	}
}

func TestJobQueueDropAllSortsBeforeLimit(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	j, err := job.New("SQU-127", "worker", "sort limited queue drops", time.Now())
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.Instance = "worker-squ-127"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	now := time.Now().UTC()
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-job-low-attempts",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-127-low",
			Payload: map[string]any{
				"job_id": "squ-127",
				"ticket": "SQU-127",
				"target": "worker",
			},
			Attempts:       1,
			LastError:      "first failure",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-2 * time.Hour),
			DeadLetteredAt: now.Add(-2 * time.Hour),
		},
		{
			ID:         "q-job-high-attempts",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-127-high",
			Payload: map[string]any{
				"job_id": "squ-127",
				"ticket": "SQU-127",
				"target": "worker",
			},
			Attempts:       9,
			LastError:      "repeated failure",
			QueuedAt:       now.Add(-time.Hour),
			UpdatedAt:      now.Add(-30 * time.Minute),
			DeadLetteredAt: now.Add(-30 * time.Minute),
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
	cmd.SetArgs([]string{"job", "queue", "drop", "SQU-127", "--repo", tmp, "--all", "--sort", "attempts", "--limit", "1", "--dry-run", "--format", "{{.ID}} {{.Action}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job queue drop sort/limit: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := strings.TrimSpace(out.String()), "q-job-high-attempts would_drop"; got != want {
		t.Fatalf("job queue drop sort/limit output = %q, want %q", got, want)
	}
}

func TestJobQueueQuarantineScopesOwnedFiles(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	j, err := job.New("SQU-123", "worker", "recover quarantined work", time.Now())
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.Instance = "worker-squ-123"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	stamp := "20260619T010203.000000000Z"
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-job-quarantined-restore",
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-123",
			Payload:    map[string]any{"job_id": "squ-123", "ticket": "SQU-123", "target": "worker"},
			QueuedAt:   now.Add(-2 * time.Hour),
			UpdatedAt:  now.Add(-2 * time.Hour),
		},
		{
			ID:             "q-job-quarantined-drop",
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     "worker-squ-123",
			Payload:        map[string]any{"job_id": "squ-123", "ticket": "SQU-123", "target": "worker"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-3 * time.Hour),
			DeadLetteredAt: now.Add(-3 * time.Hour),
		},
		{
			ID:             "q-other-quarantined",
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     "worker-squ-124",
			Payload:        map[string]any{"job_id": "squ-124", "ticket": "SQU-124", "target": "worker"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-time.Hour),
			UpdatedAt:      now.Add(-time.Hour),
			DeadLetteredAt: now.Add(-time.Hour),
		},
	} {
		state := daemon.QueueStateDead
		if item.ID == "q-job-quarantined-restore" {
			state = daemon.QueueStatePending
		}
		writeQuarantinedQueueItem(t, teamDir, stamp, state, item)
	}
	restorePath := filepath.Join("quarantine", stamp, daemon.QueueStatePending, "q-job-quarantined-restore.json")
	dropPath := filepath.Join("quarantine", stamp, daemon.QueueStateDead, "q-job-quarantined-drop.json")
	otherPath := filepath.Join("quarantine", stamp, daemon.QueueStateDead, "q-other-quarantined.json")
	queueRoot := daemon.QueueRoot(daemon.DaemonRoot(teamDir))

	list := NewRootCmd()
	listOut, listErr := &bytes.Buffer{}, &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetErr(listErr)
	list.SetArgs([]string{"job", "queue", "quarantine", "SQU-123", "--repo", tmp, "--json"})
	if err := list.Execute(); err != nil {
		t.Fatalf("job queue quarantine list: %v\nstderr=%s", err, listErr.String())
	}
	var listed []queueQuarantineItem
	if err := json.Unmarshal(listOut.Bytes(), &listed); err != nil {
		t.Fatalf("decode job quarantine list: %v\nbody=%s", err, listOut.String())
	}
	if len(listed) != 2 || queueQuarantineItemIDs(listed) != "q-job-quarantined-drop,q-job-quarantined-restore" {
		t.Fatalf("listed job quarantined items = %+v", listed)
	}

	listSorted := NewRootCmd()
	listSortedOut, listSortedErr := &bytes.Buffer{}, &bytes.Buffer{}
	listSorted.SetOut(listSortedOut)
	listSorted.SetErr(listSortedErr)
	listSorted.SetArgs([]string{"job", "queue", "quarantine", "SQU-123", "--repo", tmp, "--sort", "attempts", "--limit", "1", "--format", "{{.ID}}"})
	if err := listSorted.Execute(); err != nil {
		t.Fatalf("job queue quarantine sorted limit list: %v\nstderr=%s", err, listSortedErr.String())
	}
	if got, want := listSortedOut.String(), "q-job-quarantined-drop\n"; got != want {
		t.Fatalf("job queue quarantine sorted limit list = %q, want %q", got, want)
	}

	restoreLimit := NewRootCmd()
	restoreLimitOut, restoreLimitErr := &bytes.Buffer{}, &bytes.Buffer{}
	restoreLimit.SetOut(restoreLimitOut)
	restoreLimit.SetErr(restoreLimitErr)
	restoreLimit.SetArgs([]string{"job", "queue", "quarantine", "restore", "SQU-123", "--repo", tmp, "--all", "--sort", "attempts", "--limit", "1", "--dry-run", "--format", "{{.ID}}"})
	if err := restoreLimit.Execute(); err != nil {
		t.Fatalf("job queue quarantine restore --all limit: %v\nstderr=%s", err, restoreLimitErr.String())
	}
	if got, want := restoreLimitOut.String(), "q-job-quarantined-drop\n"; got != want {
		t.Fatalf("job restore --limit output = %q, want %q", got, want)
	}

	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"job", "queue", "quarantine", "show", "SQU-123", restorePath, "--repo", tmp})
	if err := show.Execute(); err != nil {
		t.Fatalf("job queue quarantine show: %v\nstderr=%s", err, showErr.String())
	}
	for _, want := range []string{"q-job-quarantined-restore", "Actions:", "agent-team job queue quarantine restore squ-123", "Payload:", "SQU-123"} {
		if !strings.Contains(showOut.String(), want) {
			t.Fatalf("show output missing %q:\n%s", want, showOut.String())
		}
	}

	restoreAllDry := NewRootCmd()
	restoreAllDryOut, restoreAllDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	restoreAllDry.SetOut(restoreAllDryOut)
	restoreAllDry.SetErr(restoreAllDryErr)
	restoreAllDry.SetArgs([]string{"job", "queue", "quarantine", "restore", "SQU-123", "--repo", tmp, "--all", "--state", "pending", "--dry-run", "--format", "{{.ID}} {{.Action}} {{.DryRun}}"})
	if err := restoreAllDry.Execute(); err != nil {
		t.Fatalf("job queue quarantine restore --all dry-run: %v\nstderr=%s", err, restoreAllDryErr.String())
	}
	if got, want := restoreAllDryOut.String(), "q-job-quarantined-restore would_restore true\n"; got != want {
		t.Fatalf("restore --all format = %q, want %q", got, want)
	}

	restore := NewRootCmd()
	restoreOut, restoreErr := &bytes.Buffer{}, &bytes.Buffer{}
	restore.SetOut(restoreOut)
	restore.SetErr(restoreErr)
	restore.SetArgs([]string{"job", "queue", "quarantine", "restore", "SQU-123", restorePath, "--repo", tmp, "--json"})
	if err := restore.Execute(); err != nil {
		t.Fatalf("job queue quarantine restore: %v\nstderr=%s", err, restoreErr.String())
	}
	var restored queueQuarantineRestoreResult
	if err := json.Unmarshal(restoreOut.Bytes(), &restored); err != nil {
		t.Fatalf("decode restore: %v\nbody=%s", err, restoreOut.String())
	}
	if restored.ID != "q-job-quarantined-restore" || restored.Action != "restored" {
		t.Fatalf("restored = %+v", restored)
	}
	if _, err := os.Stat(filepath.Join(queueRoot, restorePath)); !os.IsNotExist(err) {
		t.Fatalf("restore source still exists: %v", err)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-quarantined-restore"); err != nil {
		t.Fatalf("restored active item missing: %v", err)
	}

	dropOther := NewRootCmd()
	dropOtherOut, dropOtherErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropOther.SetOut(dropOtherOut)
	dropOther.SetErr(dropOtherErr)
	dropOther.SetArgs([]string{"job", "queue", "quarantine", "drop", "SQU-123", otherPath, "--repo", tmp, "--dry-run"})
	if err := dropOther.Execute(); err == nil {
		t.Fatalf("job queue quarantine drop unrelated file unexpectedly succeeded: stdout=%s", dropOtherOut.String())
	}
	if !strings.Contains(dropOtherErr.String(), "not owned by job") {
		t.Fatalf("drop unrelated stderr = %q", dropOtherErr.String())
	}

	drop := NewRootCmd()
	dropOut, dropErr := &bytes.Buffer{}, &bytes.Buffer{}
	drop.SetOut(dropOut)
	drop.SetErr(dropErr)
	drop.SetArgs([]string{"job", "queue", "quarantine", "drop", "SQU-123", dropPath, "--repo", tmp, "--json"})
	if err := drop.Execute(); err != nil {
		t.Fatalf("job queue quarantine drop: %v\nstderr=%s", err, dropErr.String())
	}
	var dropped []queueQuarantineDropResult
	if err := json.Unmarshal(dropOut.Bytes(), &dropped); err != nil {
		t.Fatalf("decode drop: %v\nbody=%s", err, dropOut.String())
	}
	if len(dropped) != 1 || dropped[0].ID != "q-job-quarantined-drop" || dropped[0].Action != "dropped" {
		t.Fatalf("dropped = %+v", dropped)
	}
	if _, err := os.Stat(filepath.Join(queueRoot, dropPath)); !os.IsNotExist(err) {
		t.Fatalf("drop source still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(queueRoot, otherPath)); err != nil {
		t.Fatalf("unrelated quarantine file changed: %v", err)
	}
}

func TestJobQueuePruneScopesOwnedItems(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	j, err := job.New("SQU-122", "worker", "prune queued work", time.Now())
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.Instance = "worker-squ-122"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-job-old-dead",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-122",
			Payload: map[string]any{
				"job_id": "squ-122",
				"ticket": "SQU-122",
				"target": "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:      now.Add(-47 * time.Hour),
			DeadLetteredAt: now.Add(-47 * time.Hour),
		},
		{
			ID:         "q-job-new-dead",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-122",
			Payload: map[string]any{
				"job_id": "squ-122",
				"ticket": "SQU-122",
				"target": "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-time.Hour),
			UpdatedAt:      now.Add(-time.Hour),
			DeadLetteredAt: now.Add(-time.Hour),
		},
		{
			ID:         "q-job-old-pending",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-122",
			Payload: map[string]any{
				"job_id": "squ-122",
				"target": "worker",
			},
			QueuedAt:  now.Add(-48 * time.Hour),
			UpdatedAt: now.Add(-48 * time.Hour),
		},
		{
			ID:         "q-other-old-dead",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-123",
			Payload: map[string]any{
				"job_id": "squ-123",
				"ticket": "SQU-123",
				"target": "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:      now.Add(-47 * time.Hour),
			DeadLetteredAt: now.Add(-47 * time.Hour),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("WriteQueueItem %s: %v", item.ID, err)
		}
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"job", "queue", "prune", "SQU-122", "--repo", tmp, "--older-than", "24h", "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("job queue prune dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryResults []queuePruneResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryResults); err != nil {
		t.Fatalf("decode prune dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(dryResults) != 1 || dryResults[0].ID != "q-job-old-dead" || !dryResults[0].DryRun || dryResults[0].Dropped {
		t.Fatalf("prune dry-run results = %+v", dryResults)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-old-dead"); err != nil {
		t.Fatalf("dry-run removed queue item: %v", err)
	}

	prune := NewRootCmd()
	pruneOut, pruneErr := &bytes.Buffer{}, &bytes.Buffer{}
	prune.SetOut(pruneOut)
	prune.SetErr(pruneErr)
	prune.SetArgs([]string{"job", "queue", "prune", "SQU-122", "--repo", tmp, "--older-than", "24h", "--format", "{{.ID}} {{.State}} {{.Dropped}}"})
	if err := prune.Execute(); err != nil {
		t.Fatalf("job queue prune: %v\nstderr=%s", err, pruneErr.String())
	}
	if got, want := strings.TrimSpace(pruneOut.String()), "q-job-old-dead dead true"; got != want {
		t.Fatalf("prune output = %q, want %q", got, want)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-old-dead"); !os.IsNotExist(err) {
		t.Fatalf("old dead item err=%v, want not exist", err)
	}
	for _, id := range []string{"q-job-new-dead", "q-job-old-pending", "q-other-old-dead"} {
		if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), id); err != nil {
			t.Fatalf("queue item %s should remain: %v", id, err)
		}
	}

	pruneAll := NewRootCmd()
	allOut, allErr := &bytes.Buffer{}, &bytes.Buffer{}
	pruneAll.SetOut(allOut)
	pruneAll.SetErr(allErr)
	pruneAll.SetArgs([]string{"job", "queue", "prune", "SQU-122", "--repo", tmp, "--state", "all", "--older-than", "24h", "--dry-run", "--format", "{{.ID}} {{.DryRun}}"})
	if err := pruneAll.Execute(); err != nil {
		t.Fatalf("job queue prune all dry-run: %v\nstderr=%s", err, allErr.String())
	}
	if got, want := strings.TrimSpace(allOut.String()), "q-job-old-pending true"; got != want {
		t.Fatalf("prune all output = %q, want %q", got, want)
	}
}

func TestJobQueuePruneRuntimeFiltersItems(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	j, err := job.New("SQU-124", "worker", "runtime scoped cleanup", time.Now())
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.Instance = "worker-squ-124"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	now := time.Now().UTC()
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-job-old-codex",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-124",
			Payload: map[string]any{
				"job_id":  "squ-124",
				"runtime": "codex",
				"ticket":  "SQU-124",
				"target":  "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:      now.Add(-47 * time.Hour),
			DeadLetteredAt: now.Add(-47 * time.Hour),
		},
		{
			ID:         "q-job-old-claude",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-124-claude",
			Payload: map[string]any{
				"job_id":  "squ-124",
				"runtime": "claude",
				"ticket":  "SQU-124",
				"target":  "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:      now.Add(-47 * time.Hour),
			DeadLetteredAt: now.Add(-47 * time.Hour),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("WriteQueueItem %s: %v", item.ID, err)
		}
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"job", "queue", "prune", "SQU-124", "--repo", tmp, "--older-than", "24h", "--runtime", "codex", "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("job queue prune runtime dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryResults []queuePruneResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryResults); err != nil {
		t.Fatalf("decode job runtime prune dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(dryResults) != 1 || dryResults[0].ID != "q-job-old-codex" || !dryResults[0].DryRun || dryResults[0].Dropped {
		t.Fatalf("job runtime dry-run results = %+v", dryResults)
	}

	prune := NewRootCmd()
	pruneOut, pruneErr := &bytes.Buffer{}, &bytes.Buffer{}
	prune.SetOut(pruneOut)
	prune.SetErr(pruneErr)
	prune.SetArgs([]string{"job", "queue", "prune", "SQU-124", "--repo", tmp, "--older-than", "24h", "--runtime", "codex", "--json"})
	if err := prune.Execute(); err != nil {
		t.Fatalf("job queue prune runtime: %v\nstderr=%s", err, pruneErr.String())
	}
	var pruneResults []queuePruneResult
	if err := json.Unmarshal(pruneOut.Bytes(), &pruneResults); err != nil {
		t.Fatalf("decode job runtime prune: %v\nbody=%s", err, pruneOut.String())
	}
	if len(pruneResults) != 1 || pruneResults[0].ID != "q-job-old-codex" || !pruneResults[0].Dropped {
		t.Fatalf("job runtime prune results = %+v", pruneResults)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-old-codex"); !os.IsNotExist(err) {
		t.Fatalf("codex job item err=%v, want not exist", err)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-old-claude"); err != nil {
		t.Fatalf("claude job item should remain: %v", err)
	}
}

func TestJobQueuePruneEventTypeFiltersItems(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	j, err := job.New("SQU-125", "worker", "event scoped cleanup", time.Now())
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.Instance = "worker-squ-125"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	now := time.Now().UTC()
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-job-dispatch",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-125",
			Payload: map[string]any{
				"job_id": "squ-125",
				"ticket": "SQU-125",
				"target": "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:      now.Add(-47 * time.Hour),
			DeadLetteredAt: now.Add(-47 * time.Hour),
		},
		{
			ID:         "q-job-schedule",
			State:      daemon.QueueStateDead,
			EventType:  "schedule.fire",
			Instance:   "worker",
			InstanceID: "worker-squ-125-schedule",
			Payload: map[string]any{
				"job_id": "squ-125",
				"ticket": "SQU-125",
				"target": "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:      now.Add(-47 * time.Hour),
			DeadLetteredAt: now.Add(-47 * time.Hour),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("WriteQueueItem %s: %v", item.ID, err)
		}
	}

	prune := NewRootCmd()
	pruneOut, pruneErr := &bytes.Buffer{}, &bytes.Buffer{}
	prune.SetOut(pruneOut)
	prune.SetErr(pruneErr)
	prune.SetArgs([]string{"job", "queue", "prune", "SQU-125", "--repo", tmp, "--event-type", "agent.dispatch", "--format", "{{.ID}} {{.Dropped}}"})
	if err := prune.Execute(); err != nil {
		t.Fatalf("job queue prune event filter: %v\nstderr=%s", err, pruneErr.String())
	}
	if got, want := strings.TrimSpace(pruneOut.String()), "q-job-dispatch true"; got != want {
		t.Fatalf("event filtered prune output = %q, want %q", got, want)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-dispatch"); !os.IsNotExist(err) {
		t.Fatalf("dispatch item err=%v, want not exist", err)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-schedule"); err != nil {
		t.Fatalf("schedule item should remain: %v", err)
	}
}

func TestJobQueuePruneReadyDefaultsToPendingDueItems(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	j, err := job.New("SQU-126", "worker", "ready scoped cleanup", time.Now())
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.Instance = "worker-squ-126"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	now := time.Now().UTC()
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-job-ready",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-126",
			Payload: map[string]any{
				"job_id": "squ-126",
				"ticket": "SQU-126",
				"target": "worker",
			},
			NextRetry: now.Add(-time.Minute),
			QueuedAt:  now.Add(-time.Hour),
			UpdatedAt: now.Add(-time.Hour),
		},
		{
			ID:         "q-job-delayed",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-126",
			Payload: map[string]any{
				"job_id": "squ-126",
				"ticket": "SQU-126",
				"target": "worker",
			},
			NextRetry: now.Add(time.Hour),
			QueuedAt:  now.Add(-time.Hour),
			UpdatedAt: now.Add(-time.Hour),
		},
		{
			ID:         "q-job-dead",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-126",
			Payload: map[string]any{
				"job_id": "squ-126",
				"ticket": "SQU-126",
				"target": "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:      now.Add(-47 * time.Hour),
			DeadLetteredAt: now.Add(-47 * time.Hour),
		},
		{
			ID:         "q-other-ready",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-127",
			Payload: map[string]any{
				"job_id": "squ-127",
				"ticket": "SQU-127",
				"target": "worker",
			},
			NextRetry: now.Add(-time.Minute),
			QueuedAt:  now.Add(-time.Hour),
			UpdatedAt: now.Add(-time.Hour),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("WriteQueueItem %s: %v", item.ID, err)
		}
	}

	prune := NewRootCmd()
	pruneOut, pruneErr := &bytes.Buffer{}, &bytes.Buffer{}
	prune.SetOut(pruneOut)
	prune.SetErr(pruneErr)
	prune.SetArgs([]string{"job", "queue", "prune", "SQU-126", "--repo", tmp, "--ready", "--format", "{{.ID}} {{.State}} {{.Dropped}}"})
	if err := prune.Execute(); err != nil {
		t.Fatalf("job queue prune ready: %v\nstderr=%s", err, pruneErr.String())
	}
	if got, want := strings.TrimSpace(pruneOut.String()), "q-job-ready pending true"; got != want {
		t.Fatalf("ready prune output = %q, want %q", got, want)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-ready"); !os.IsNotExist(err) {
		t.Fatalf("ready item err=%v, want not exist", err)
	}
	for _, id := range []string{"q-job-delayed", "q-job-dead", "q-other-ready"} {
		if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), id); err != nil {
			t.Fatalf("queue item %s should remain: %v", id, err)
		}
	}
}

func TestJobQueueRejectsFormatCombinations(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "format with json",
			args: []string{"job", "queue", "SQU-120", "--format", "{{.ID}}", "--json"},
			want: "--format cannot be combined with --json",
		},
		{
			name: "format with summary",
			args: []string{"job", "queue", "SQU-120", "--format", "{{.ID}}", "--summary"},
			want: "--format cannot be combined with --summary",
		},
		{
			name: "invalid format",
			args: []string{"job", "queue", "SQU-120", "--format", "{{"},
			want: "invalid --format template",
		},
		{
			name: "invalid state",
			args: []string{"job", "queue", "SQU-120", "--state", "stuck"},
			want: "--state must be pending or dead",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("job queue validation succeeded: stdout=%s", out.String())
			}
			var code ExitCode
			if !errors.As(err, &code) || int(code) != 2 {
				t.Fatalf("job queue err = %v, want exit code 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestJobQueuePruneRejectsFormatCombinations(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "format with json",
			args: []string{"job", "queue", "prune", "SQU-122", "--format", "{{.ID}}", "--json"},
			want: "--format cannot be combined with --json",
		},
		{
			name: "invalid format",
			args: []string{"job", "queue", "prune", "SQU-122", "--format", "{{"},
			want: "invalid --format template",
		},
		{
			name: "negative older than",
			args: []string{"job", "queue", "prune", "SQU-122", "--older-than", "-1s"},
			want: "--older-than must be >= 0",
		},
		{
			name: "negative limit",
			args: []string{"job", "queue", "prune", "SQU-122", "--limit", "-1"},
			want: "--limit must be >= 0",
		},
		{
			name: "invalid state",
			args: []string{"job", "queue", "prune", "SQU-122", "--state", "stuck"},
			want: "--state must be dead, pending, or all",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("job queue prune validation succeeded: stdout=%s", out.String())
			}
			var code ExitCode
			if !errors.As(err, &code) || int(code) != 2 {
				t.Fatalf("job queue prune err = %v, want exit code 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestJobQueueRetryDropRejectsFormatCombinations(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "retry format with json",
			args: []string{"job", "queue", "retry", "SQU-121", "--format", "{{.ID}}", "--json"},
			want: "--format cannot be combined with --json",
		},
		{
			name: "retry invalid format",
			args: []string{"job", "queue", "retry", "SQU-121", "--format", "{{"},
			want: "invalid --format template",
		},
		{
			name: "retry negative limit",
			args: []string{"job", "queue", "retry", "SQU-121", "--all", "--limit", "-1"},
			want: "--limit must be >= 0",
		},
		{
			name: "retry filter without all",
			args: []string{"job", "queue", "retry", "SQU-121", "q-job-dead", "--state", "dead"},
			want: "--state, --event-type, --runtime, --ready, --sort, and --limit require --all",
		},
		{
			name: "retry runtime without all",
			args: []string{"job", "queue", "retry", "SQU-121", "q-job-dead", "--runtime", "codex"},
			want: "--state, --event-type, --runtime, --ready, --sort, and --limit require --all",
		},
		{
			name: "drop format with json",
			args: []string{"job", "queue", "drop", "SQU-121", "--format", "{{.ID}}", "--json"},
			want: "--format cannot be combined with --json",
		},
		{
			name: "drop invalid format",
			args: []string{"job", "queue", "drop", "SQU-121", "--format", "{{"},
			want: "invalid --format template",
		},
		{
			name: "drop negative limit",
			args: []string{"job", "queue", "drop", "SQU-121", "--all", "--limit", "-1"},
			want: "--limit must be >= 0",
		},
		{
			name: "drop filter without all",
			args: []string{"job", "queue", "drop", "SQU-121", "q-job-dead", "--ready"},
			want: "--state, --event-type, --runtime, --ready, --sort, and --limit require --all",
		},
		{
			name: "drop runtime without all",
			args: []string{"job", "queue", "drop", "SQU-121", "q-job-dead", "--runtime", "codex"},
			want: "--state, --event-type, --runtime, --ready, --sort, and --limit require --all",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("job queue control validation succeeded: stdout=%s", out.String())
			}
			var code ExitCode
			if !errors.As(err, &code) || int(code) != 2 {
				t.Fatalf("job queue control err = %v, want exit code 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
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

	queuedReady := mustNewJob(t, "SQU-208", "worker")
	queuedReady.Pipeline = "ticket_to_pr"
	queuedReady.Steps = []job.Step{
		{ID: "implement", Target: "worker", Status: job.StatusQueued},
	}
	if err := job.Write(teamDir, queuedReady); err != nil {
		t.Fatalf("write queued ready job: %v", err)
	}

	cleanupReady := mustNewJob(t, "SQU-206", "worker")
	cleanupReady.Status = job.StatusDone
	cleanupReady.Branch = "worktree-worker-squ-206"
	cleanupReady.Worktree = filepath.Join(tmp, ".claude", "worktrees", "worker-squ-206")
	cleanupReady.PR = "https://github.com/acme/repo/pull/206"
	if err := job.Write(teamDir, cleanupReady); err != nil {
		t.Fatalf("write cleanup-ready job: %v", err)
	}

	quarantined := mustNewJob(t, "SQU-207", "worker")
	quarantined.Instance = "worker-squ-207"
	if err := job.Write(teamDir, quarantined); err != nil {
		t.Fatalf("write quarantined job: %v", err)
	}
	quarantineStamp := "20260619T020304.000000000Z"
	quarantinePath := filepath.Join("quarantine", quarantineStamp, daemon.QueueStatePending, "q-triage-quarantined.json")
	writeQuarantinedQueueItem(t, teamDir, quarantineStamp, daemon.QueueStatePending, &daemon.QueueItem{
		ID:         "q-triage-quarantined",
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-207",
		Payload: map[string]any{
			"job_id": "squ-207",
			"ticket": "SQU-207",
			"target": "worker",
		},
		QueuedAt:  now.Add(-2 * time.Hour),
		UpdatedAt: now.Add(-2 * time.Hour),
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "triage", "--repo", tmp, "--stale-after", "24h"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job triage: %v\nstderr=%s", err, stderr.String())
	}
	for _, want := range []string{
		"jobs: total=8",
		"queue: total=1 pending=0 dead=1",
		"quarantined=1 restorable=1 unrestorable=0",
		"Attention:",
		"squ-201",
		"failed",
		"squ-202",
		"stale_running",
		"running_without_instance",
		"agent-team job reconcile status",
		"agent-team job timeout squ-202 --dry-run",
		"agent-team job adopt squ-202 --pid <pid> --dry-run",
		"squ-203",
		"stale_queued",
		"agent-team job dispatch squ-203",
		"squ-204",
		"queue_dead",
		"agent-team job queue retry squ-204 q-triage-dead",
		"agent-team job retry squ-201 --dispatch",
		"Ready pipeline steps:",
		"squ-205",
		"implement",
		"agent-team job advance squ-205",
		"squ-208",
		"agent-team job advance squ-208",
		"squ-206",
		"cleanup_ready",
		"agent-team job cleanup squ-206 --dry-run",
		"squ-207",
		"queue_quarantined",
		"agent-team job queue quarantine squ-207",
		fmt.Sprintf("agent-team job queue quarantine restore squ-207 %s --dry-run", quarantinePath),
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
	if snapshot.Summary.Total != 8 || snapshot.Queue.Dead != 1 || snapshot.Queue.Quarantined != 1 || snapshot.Queue.QuarantineRestorable != 1 || len(snapshot.Attention) != 6 || len(snapshot.ReadySteps) != 2 {
		t.Fatalf("triage snapshot = %+v", snapshot)
	}
	reasons := map[string][]string{}
	actions := map[string][]string{}
	for _, item := range snapshot.Attention {
		reasons[item.JobID] = item.Reasons
		actions[item.JobID] = item.Actions
	}
	if !containsString(reasons["squ-204"], "queue_dead") {
		t.Fatalf("squ-204 reasons = %v", reasons["squ-204"])
	}
	if !containsString(actions["squ-204"], "agent-team job queue retry squ-204 q-triage-dead") {
		t.Fatalf("squ-204 actions = %v", actions["squ-204"])
	}
	if !containsString(actions["squ-201"], "agent-team job retry squ-201 --dispatch") {
		t.Fatalf("squ-201 actions = %v", actions["squ-201"])
	}
	if !containsString(actions["squ-202"], "agent-team job adopt squ-202 --pid <pid> --dry-run") {
		t.Fatalf("squ-202 actions = %v", actions["squ-202"])
	}
	if !containsString(actions["squ-202"], "agent-team job timeout squ-202 --dry-run") {
		t.Fatalf("squ-202 actions = %v", actions["squ-202"])
	}
	if !containsString(reasons["squ-206"], "cleanup_ready") {
		t.Fatalf("squ-206 reasons = %v", reasons["squ-206"])
	}
	if !containsString(actions["squ-206"], "agent-team job cleanup squ-206 --dry-run") {
		t.Fatalf("squ-206 actions = %v", actions["squ-206"])
	}
	if !containsString(reasons["squ-207"], "queue_quarantined") {
		t.Fatalf("squ-207 reasons = %v", reasons["squ-207"])
	}
	if !containsString(actions["squ-207"], "agent-team job queue quarantine squ-207") || !containsString(actions["squ-207"], fmt.Sprintf("agent-team job queue quarantine restore squ-207 %s --dry-run", quarantinePath)) {
		t.Fatalf("squ-207 actions = %v", actions["squ-207"])
	}
	readyByID := map[string]jobReadyRow{}
	for _, row := range snapshot.ReadySteps {
		readyByID[row.JobID] = row
	}
	if readyByID["squ-205"].StepID != "implement" || readyByID["squ-208"].StepID != "implement" {
		t.Fatalf("ready steps = %+v", snapshot.ReadySteps)
	}
	if !containsString(readyByID["squ-205"].Actions, "agent-team job advance squ-205") ||
		!containsString(readyByID["squ-208"].Actions, "agent-team job advance squ-208") {
		t.Fatalf("ready step actions = %+v", snapshot.ReadySteps[0].Actions)
	}

	formatCmd := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatCmd.SetOut(formatOut)
	formatCmd.SetErr(formatErr)
	formatCmd.SetArgs([]string{"job", "triage", "--repo", tmp, "--stale-after", "24h", "--format", "{{.Summary.Total}} {{.Queue.Dead}} {{len .Attention}} {{len .ReadySteps}}"})
	if err := formatCmd.Execute(); err != nil {
		t.Fatalf("job triage format: %v\nstderr=%s", err, formatErr.String())
	}
	if got, want := formatOut.String(), "8 1 6 2\n"; got != want {
		t.Fatalf("job triage format = %q, want %q", got, want)
	}

	criticalCmd := NewRootCmd()
	criticalOut, criticalErr := &bytes.Buffer{}, &bytes.Buffer{}
	criticalCmd.SetOut(criticalOut)
	criticalCmd.SetErr(criticalErr)
	criticalCmd.SetArgs([]string{"job", "triage", "--repo", tmp, "--stale-after", "24h", "--min-severity", "critical"})
	if err := criticalCmd.Execute(); err != nil {
		t.Fatalf("job triage critical: %v\nstderr=%s", err, criticalErr.String())
	}
	if !strings.Contains(criticalOut.String(), "squ-201") || !strings.Contains(criticalOut.String(), "squ-204") || strings.Contains(criticalOut.String(), "squ-202") || strings.Contains(criticalOut.String(), "squ-203") {
		t.Fatalf("critical triage output =\n%s", criticalOut.String())
	}

	reasonCmd := NewRootCmd()
	reasonOut, reasonErr := &bytes.Buffer{}, &bytes.Buffer{}
	reasonCmd.SetOut(reasonOut)
	reasonCmd.SetErr(reasonErr)
	reasonCmd.SetArgs([]string{"job", "triage", "--repo", tmp, "--stale-after", "24h", "--reason", "queue_dead", "--json"})
	if err := reasonCmd.Execute(); err != nil {
		t.Fatalf("job triage reason: %v\nstderr=%s", err, reasonErr.String())
	}
	var reasonSnapshot jobTriageSnapshot
	if err := json.Unmarshal(reasonOut.Bytes(), &reasonSnapshot); err != nil {
		t.Fatalf("decode reason triage json: %v\nbody=%s", err, reasonOut.String())
	}
	if len(reasonSnapshot.Attention) != 1 || reasonSnapshot.Attention[0].JobID != "squ-204" {
		t.Fatalf("reason triage attention = %+v", reasonSnapshot.Attention)
	}

	quarantineReasonCmd := NewRootCmd()
	quarantineReasonOut, quarantineReasonErr := &bytes.Buffer{}, &bytes.Buffer{}
	quarantineReasonCmd.SetOut(quarantineReasonOut)
	quarantineReasonCmd.SetErr(quarantineReasonErr)
	quarantineReasonCmd.SetArgs([]string{"job", "triage", "--repo", tmp, "--stale-after", "24h", "--reason", "queue_quarantined", "--json"})
	if err := quarantineReasonCmd.Execute(); err != nil {
		t.Fatalf("job triage quarantine reason: %v\nstderr=%s", err, quarantineReasonErr.String())
	}
	var quarantineReasonSnapshot jobTriageSnapshot
	if err := json.Unmarshal(quarantineReasonOut.Bytes(), &quarantineReasonSnapshot); err != nil {
		t.Fatalf("decode quarantine reason triage json: %v\nbody=%s", err, quarantineReasonOut.String())
	}
	if len(quarantineReasonSnapshot.Attention) != 1 || quarantineReasonSnapshot.Attention[0].JobID != "squ-207" {
		t.Fatalf("quarantine reason triage attention = %+v", quarantineReasonSnapshot.Attention)
	}

	cleanupReasonCmd := NewRootCmd()
	cleanupReasonOut, cleanupReasonErr := &bytes.Buffer{}, &bytes.Buffer{}
	cleanupReasonCmd.SetOut(cleanupReasonOut)
	cleanupReasonCmd.SetErr(cleanupReasonErr)
	cleanupReasonCmd.SetArgs([]string{"job", "triage", "--repo", tmp, "--reason", "cleanup_ready", "--json"})
	if err := cleanupReasonCmd.Execute(); err != nil {
		t.Fatalf("job triage cleanup reason: %v\nstderr=%s", err, cleanupReasonErr.String())
	}
	var cleanupReasonSnapshot jobTriageSnapshot
	if err := json.Unmarshal(cleanupReasonOut.Bytes(), &cleanupReasonSnapshot); err != nil {
		t.Fatalf("decode cleanup reason triage json: %v\nbody=%s", err, cleanupReasonOut.String())
	}
	if len(cleanupReasonSnapshot.Attention) != 1 || cleanupReasonSnapshot.Attention[0].JobID != "squ-206" {
		t.Fatalf("cleanup reason triage attention = %+v", cleanupReasonSnapshot.Attention)
	}
}

func TestJobTimeoutMarksStaleRunningStepsAndJobs(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	stepJob := &job.Job{
		ID:        "squ-840",
		Ticket:    "SQU-840",
		Target:    "worker",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		CreatedAt: now.Add(-2 * time.Hour),
		UpdatedAt: now.Add(-90 * time.Minute),
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-840", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
		},
	}
	if err := job.Write(teamDir, stepJob); err != nil {
		t.Fatalf("write step job: %v", err)
	}
	lifecycleJob := &job.Job{
		ID:        "squ-841",
		Ticket:    "SQU-841",
		Target:    "worker",
		Instance:  "worker-squ-841",
		Status:    job.StatusRunning,
		CreatedAt: now.Add(-48 * time.Hour),
		UpdatedAt: now.Add(-48 * time.Hour),
	}
	if err := job.Write(teamDir, lifecycleJob); err != nil {
		t.Fatalf("write lifecycle job: %v", err)
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"job", "timeout", "squ-840", "--repo", root, "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("job timeout dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryRows []pipelineTimeoutResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryRows); err != nil {
		t.Fatalf("decode dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(dryRows) != 1 || dryRows[0].JobID != "squ-840" || dryRows[0].StepID != "implement" || dryRows[0].Action != "would_fail" || dryRows[0].StepStatus != job.StatusRunning {
		t.Fatalf("dry rows = %+v", dryRows)
	}
	unchanged, err := job.Read(teamDir, "squ-840")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.Status != job.StatusRunning || unchanged.Steps[0].Status != job.StatusRunning || unchanged.Steps[0].Instance != "worker-squ-840" {
		t.Fatalf("dry-run mutated job = %+v", unchanged)
	}

	applyStep := NewRootCmd()
	applyStepOut, applyStepErr := &bytes.Buffer{}, &bytes.Buffer{}
	applyStep.SetOut(applyStepOut)
	applyStep.SetErr(applyStepErr)
	stepTimeoutFile := filepath.Join(root, "job-timeout-message.txt")
	if err := os.WriteFile(stepTimeoutFile, []byte("job step timed out from file\n"), 0o644); err != nil {
		t.Fatalf("write timeout message: %v", err)
	}
	applyStep.SetArgs([]string{"job", "timeout", "squ-840", "--repo", root, "--message-file", stepTimeoutFile, "--json"})
	if err := applyStep.Execute(); err != nil {
		t.Fatalf("job timeout step apply: %v\nstderr=%s", err, applyStepErr.String())
	}
	var stepRows []pipelineTimeoutResult
	if err := json.Unmarshal(applyStepOut.Bytes(), &stepRows); err != nil {
		t.Fatalf("decode step apply: %v\nbody=%s", err, applyStepOut.String())
	}
	if len(stepRows) != 1 || stepRows[0].Action != "failed" || stepRows[0].StepStatus != job.StatusFailed || stepRows[0].Instance != "" {
		t.Fatalf("step rows = %+v", stepRows)
	}
	timedOutStep, err := job.Read(teamDir, "squ-840")
	if err != nil {
		t.Fatalf("read timed out step job: %v", err)
	}
	if timedOutStep.Status != job.StatusFailed || timedOutStep.Steps[0].Status != job.StatusFailed || timedOutStep.Steps[0].Instance != "" || timedOutStep.LastStatus != "job step timed out from file" {
		t.Fatalf("timed out step job = %+v", timedOutStep)
	}

	applyLifecycle := NewRootCmd()
	lifecycleOut, lifecycleErr := &bytes.Buffer{}, &bytes.Buffer{}
	applyLifecycle.SetOut(lifecycleOut)
	applyLifecycle.SetErr(lifecycleErr)
	applyLifecycle.SetArgs([]string{"job", "timeout", "squ-841", "--repo", root, "--message", "job lifecycle timed out", "--json"})
	if err := applyLifecycle.Execute(); err != nil {
		t.Fatalf("job timeout lifecycle apply: %v\nstderr=%s", err, lifecycleErr.String())
	}
	var lifecycleRows []pipelineTimeoutResult
	if err := json.Unmarshal(lifecycleOut.Bytes(), &lifecycleRows); err != nil {
		t.Fatalf("decode lifecycle apply: %v\nbody=%s", err, lifecycleOut.String())
	}
	if len(lifecycleRows) != 1 || lifecycleRows[0].Action != "failed" || lifecycleRows[0].StepID != "" || lifecycleRows[0].StepStatus != job.StatusFailed {
		t.Fatalf("lifecycle rows = %+v", lifecycleRows)
	}
	timedOutJob, err := job.Read(teamDir, "squ-841")
	if err != nil {
		t.Fatalf("read timed out lifecycle job: %v", err)
	}
	if timedOutJob.Status != job.StatusFailed || timedOutJob.LastEvent != "job_timeout" || timedOutJob.LastStatus != "job lifecycle timed out" || timedOutJob.Instance != "worker-squ-841" {
		t.Fatalf("timed out lifecycle job = %+v", timedOutJob)
	}
	events, err := job.ListEvents(teamDir, "squ-841")
	if err != nil {
		t.Fatalf("list lifecycle events: %v", err)
	}
	if len(events) != 1 || events[0].Type != "job_timeout" || events[0].Message != "job lifecycle timed out" {
		t.Fatalf("lifecycle events = %+v", events)
	}
}

func TestJobTimeoutAllMarksStaleRunningWork(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-842",
			Ticket:    "SQU-842",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-842", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
			},
		},
		{
			ID:        "squ-843",
			Ticket:    "SQU-843",
			Target:    "worker",
			Instance:  "worker-squ-843",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-48 * time.Hour),
			UpdatedAt: now.Add(-48 * time.Hour),
		},
		{
			ID:        "squ-844",
			Ticket:    "SQU-844",
			Target:    "worker",
			Instance:  "worker-squ-844",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-30 * time.Minute),
			UpdatedAt: now.Add(-30 * time.Minute),
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	limited := NewRootCmd()
	limitedOut, limitedErr := &bytes.Buffer{}, &bytes.Buffer{}
	limited.SetOut(limitedOut)
	limited.SetErr(limitedErr)
	limited.SetArgs([]string{"job", "timeout", "--all", "--repo", root, "--dry-run", "--limit", "1", "--json"})
	if err := limited.Execute(); err != nil {
		t.Fatalf("job timeout --all limited dry-run: %v\nstderr=%s", err, limitedErr.String())
	}
	var limitedRows []pipelineTimeoutResult
	if err := json.Unmarshal(limitedOut.Bytes(), &limitedRows); err != nil {
		t.Fatalf("decode limited dry-run: %v\nbody=%s", err, limitedOut.String())
	}
	if len(limitedRows) != 1 || limitedRows[0].Action != "would_fail" {
		t.Fatalf("limited rows = %+v", limitedRows)
	}

	apply := NewRootCmd()
	applyOut, applyErr := &bytes.Buffer{}, &bytes.Buffer{}
	apply.SetOut(applyOut)
	apply.SetErr(applyErr)
	timeoutFile := filepath.Join(root, "batch-timeout-message.txt")
	if err := os.WriteFile(timeoutFile, []byte("batch timeout from file\n"), 0o644); err != nil {
		t.Fatalf("write batch timeout message: %v", err)
	}
	apply.SetArgs([]string{"job", "timeout", "--all", "--repo", root, "--message-file", timeoutFile, "--json"})
	if err := apply.Execute(); err != nil {
		t.Fatalf("job timeout --all apply: %v\nstderr=%s", err, applyErr.String())
	}
	var rows []pipelineTimeoutResult
	if err := json.Unmarshal(applyOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode apply: %v\nbody=%s", err, applyOut.String())
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	stepJob, err := job.Read(teamDir, "squ-842")
	if err != nil {
		t.Fatalf("read step job: %v", err)
	}
	if stepJob.Status != job.StatusFailed || stepJob.Steps[0].Status != job.StatusFailed || stepJob.Steps[0].Instance != "" || stepJob.LastStatus != "batch timeout from file" {
		t.Fatalf("step job = %+v", stepJob)
	}
	lifecycleJob, err := job.Read(teamDir, "squ-843")
	if err != nil {
		t.Fatalf("read lifecycle job: %v", err)
	}
	if lifecycleJob.Status != job.StatusFailed || lifecycleJob.Instance != "worker-squ-843" || lifecycleJob.LastEvent != "job_timeout" || lifecycleJob.LastStatus != "batch timeout from file" {
		t.Fatalf("lifecycle job = %+v", lifecycleJob)
	}
	fresh, err := job.Read(teamDir, "squ-844")
	if err != nil {
		t.Fatalf("read fresh job: %v", err)
	}
	if fresh.Status != job.StatusRunning || fresh.Instance != "worker-squ-844" {
		t.Fatalf("fresh job changed = %+v", fresh)
	}
}

func TestJobTimeoutAllFiltersByPipelineAndTargetAgent(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-845",
			Ticket:    "SQU-845",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-845", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
			},
		},
		{
			ID:        "squ-846",
			Ticket:    "SQU-846",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "review", Target: "manager", Status: job.StatusRunning, Instance: "manager-squ-846", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
			},
		},
		{
			ID:        "squ-847",
			Ticket:    "SQU-847",
			Target:    "worker",
			Pipeline:  "other",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-847", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
			},
		},
		{
			ID:        "squ-848",
			Ticket:    "SQU-848",
			Target:    "worker",
			Instance:  "worker-squ-848",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-48 * time.Hour),
			UpdatedAt: now.Add(-48 * time.Hour),
		},
		{
			ID:        "squ-849",
			Ticket:    "SQU-849",
			Target:    "manager",
			Instance:  "manager-squ-849",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-48 * time.Hour),
			UpdatedAt: now.Add(-48 * time.Hour),
		},
		{
			ID:        "squ-850",
			Ticket:    "SQU-850",
			Target:    "worker",
			Pipeline:  "other",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-850", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
				{ID: "review", Target: "manager", Status: job.StatusDone, FinishedAt: now.Add(-80 * time.Minute)},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	pipelineDry := NewRootCmd()
	pipelineOut, pipelineErr := &bytes.Buffer{}, &bytes.Buffer{}
	pipelineDry.SetOut(pipelineOut)
	pipelineDry.SetErr(pipelineErr)
	pipelineDry.SetArgs([]string{"job", "timeout", "--all", "--repo", root, "--pipeline", "ticket_to_pr", "--dry-run", "--json"})
	if err := pipelineDry.Execute(); err != nil {
		t.Fatalf("job timeout --all --pipeline dry-run: %v\nstderr=%s", err, pipelineErr.String())
	}
	var pipelineRows []pipelineTimeoutResult
	if err := json.Unmarshal(pipelineOut.Bytes(), &pipelineRows); err != nil {
		t.Fatalf("decode pipeline dry-run: %v\nbody=%s", err, pipelineOut.String())
	}
	if len(pipelineRows) != 2 || pipelineRows[0].JobID != "squ-845" || pipelineRows[1].JobID != "squ-846" {
		t.Fatalf("pipeline rows = %+v", pipelineRows)
	}

	apply := NewRootCmd()
	applyOut, applyErr := &bytes.Buffer{}, &bytes.Buffer{}
	apply.SetOut(applyOut)
	apply.SetErr(applyErr)
	apply.SetArgs([]string{"job", "timeout", "--all", "--repo", root, "--target-agent", "manager", "--message", "manager timeout sweep", "--json"})
	if err := apply.Execute(); err != nil {
		t.Fatalf("job timeout --all --target-agent apply: %v\nstderr=%s", err, applyErr.String())
	}
	var rows []pipelineTimeoutResult
	if err := json.Unmarshal(applyOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode target-agent apply: %v\nbody=%s", err, applyOut.String())
	}
	if len(rows) != 2 || rows[0].JobID != "squ-846" || rows[1].JobID != "squ-849" {
		t.Fatalf("target-agent rows = %+v", rows)
	}
	review, err := job.Read(teamDir, "squ-846")
	if err != nil {
		t.Fatalf("read review job: %v", err)
	}
	if review.Status != job.StatusFailed || review.Steps[0].Status != job.StatusFailed || review.Steps[0].Instance != "" || review.LastStatus != "manager timeout sweep" {
		t.Fatalf("review job = %+v", review)
	}
	standalone, err := job.Read(teamDir, "squ-849")
	if err != nil {
		t.Fatalf("read standalone manager job: %v", err)
	}
	if standalone.Status != job.StatusFailed || standalone.LastEvent != "job_timeout" || standalone.LastStatus != "manager timeout sweep" {
		t.Fatalf("standalone job = %+v", standalone)
	}
	for _, id := range []string{"squ-845", "squ-847", "squ-848", "squ-850"} {
		unchanged, err := job.Read(teamDir, id)
		if err != nil {
			t.Fatalf("read unchanged %s: %v", id, err)
		}
		if unchanged.Status != job.StatusRunning {
			t.Fatalf("%s changed = %+v", id, unchanged)
		}
		if id == "squ-850" && unchanged.Steps[0].Status != job.StatusRunning {
			t.Fatalf("%s worker step changed = %+v", id, unchanged)
		}
	}
}

func TestJobTimeoutRejectsInvalidArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing target",
			args: []string{"job", "timeout"},
			want: "pass a job id or --all",
		},
		{
			name: "all with job id",
			args: []string{"job", "timeout", "squ-1", "--all"},
			want: "--all cannot be combined with a job id",
		},
		{
			name: "negative limit",
			args: []string{"job", "timeout", "--all", "--limit", "-1"},
			want: "--limit must be >= 0",
		},
		{
			name: "format with json",
			args: []string{"job", "timeout", "squ-1", "--json", "--format", "{{.JobID}}"},
			want: "--format cannot be combined with --json",
		},
		{
			name: "pipeline without all",
			args: []string{"job", "timeout", "squ-1", "--pipeline", "ticket_to_pr"},
			want: "--pipeline and --target-agent require --all",
		},
		{
			name: "target agent without all",
			args: []string{"job", "timeout", "squ-1", "--target-agent", "worker"},
			want: "--pipeline and --target-agent require --all",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("job timeout invalid args succeeded: stdout=%s", out.String())
			}
			var code ExitCode
			if !errors.As(err, &code) || int(code) != 2 {
				t.Fatalf("job timeout err = %v, want exit code 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestJobTriageUsesRunningPipelineStepInstance(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-48 * time.Hour)
	j := mustNewJob(t, "SQU-209", "manager")
	j.Status = job.StatusRunning
	j.Pipeline = "ticket_triage"
	j.UpdatedAt = old
	j.Steps = []job.Step{
		{ID: "triage", Target: "ticket-manager", Status: job.StatusDone, StartedAt: old, FinishedAt: old.Add(time.Hour)},
		{ID: "review", Target: "manager", Instance: "manager-review", Status: job.StatusRunning, After: []string{"triage"}, StartedAt: old.Add(time.Hour)},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "triage", "--repo", tmp, "--stale-after", "24h", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job triage: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot jobTriageSnapshot
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode triage json: %v\nbody=%s", err, out.String())
	}
	if len(snapshot.Attention) != 1 {
		t.Fatalf("attention = %+v", snapshot.Attention)
	}
	item := snapshot.Attention[0]
	if item.JobID != "squ-209" || item.Instance != "manager-review" || item.StepID != "review" {
		t.Fatalf("triage item = %+v", item)
	}
	if containsString(item.Reasons, "running_without_instance") {
		t.Fatalf("triage reasons included ownerless warning: %+v", item.Reasons)
	}
	if !containsString(item.Reasons, "stale_running") {
		t.Fatalf("triage reasons = %+v", item.Reasons)
	}
	if containsString(item.Actions, "agent-team job adopt squ-209 --pid <pid> --dry-run") {
		t.Fatalf("triage actions included adoption hint: %+v", item.Actions)
	}
}

func TestJobTriageIncludesBlockedStatusPreview(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC().Truncate(time.Second)
	j := mustNewJob(t, "SQU-207", "worker")
	j.Status = job.StatusQueued
	j.UpdatedAt = now
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write queued job: %v", err)
	}
	writeStatus(t, filepath.Join(teamDir, "state", "worker-squ-207"), `[status]
phase = "blocked"
description = "needs token"
since = "2026-06-18T12:00:00Z"

[work]
job = "squ-207"
ticket = "SQU-207"
branch = "worker-squ-207"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "triage", "--repo", tmp, "--stale-after", "24h"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job triage: %v\nstderr=%s", err, stderr.String())
	}
	for _, want := range []string{
		"job status: previews=1 changes=1 blocked=1",
		"Attention:",
		"squ-207",
		"status_file_blocked",
		"agent-team job unblock squ-207 <answer...>",
		"needs token",
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
	if len(snapshot.StatusPreviews) != 1 || snapshot.StatusPreviews[0].JobID != "squ-207" || snapshot.StatusPreviews[0].After != job.StatusBlocked {
		t.Fatalf("status previews = %+v", snapshot.StatusPreviews)
	}
	if len(snapshot.Attention) != 1 || snapshot.Attention[0].JobID != "squ-207" || !containsString(snapshot.Attention[0].Reasons, "status_file_blocked") {
		t.Fatalf("attention = %+v", snapshot.Attention)
	}
	if snapshot.Attention[0].Severity != "warning" || !containsString(snapshot.Attention[0].Actions, "agent-team job unblock squ-207 <answer...>") {
		t.Fatalf("attention action/severity = %+v", snapshot.Attention[0])
	}
	updated, err := job.Read(teamDir, "squ-207")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.Status != job.StatusQueued {
		t.Fatalf("triage should not mutate job status: %+v", updated)
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
	if err := runJobTriageWatch(ctx, &out, teamDir, 24*time.Hour, jobTriageFilters{}, false, time.Millisecond, false); err != nil {
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

	badSeverity := NewRootCmd()
	badOut, badErr := &bytes.Buffer{}, &bytes.Buffer{}
	badSeverity.SetOut(badOut)
	badSeverity.SetErr(badErr)
	badSeverity.SetArgs([]string{"job", "triage", "--repo", tmp, "--min-severity", "urgent"})
	if err := badSeverity.Execute(); err == nil {
		t.Fatalf("job triage bad severity succeeded")
	}
	if !strings.Contains(badErr.String(), "--min-severity must be critical, warning, or info") {
		t.Fatalf("bad severity stderr = %q", badErr.String())
	}

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "format with json",
			args: []string{"job", "triage", "--repo", tmp, "--format", "{{.Summary.Total}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "format with watch",
			args: []string{"job", "triage", "--repo", tmp, "--format", "{{.Summary.Total}}", "--watch"},
			want: "--format cannot be combined",
		},
		{
			name: "invalid format",
			args: []string{"job", "triage", "--repo", tmp, "--format", "{{"},
			want: "invalid --format template",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("job triage invalid format succeeded: stdout=%s", out.String())
			}
			var code ExitCode
			if !errors.As(err, &code) || int(code) != 2 {
				t.Fatalf("job triage err = %v, want exit code 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
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

func TestJobReconcileStatusUpdatesOwningJob(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-120",
		Ticket:    "SQU-120",
		Target:    "worker",
		Status:    job.StatusQueued,
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now.Add(-time.Hour),
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	writeStatus(t, filepath.Join(teamDir, "state", "worker-squ-120"), `[status]
phase = "implementing"
description = "writing status reconcile"
since = "2026-06-18T12:00:00Z"

[work]
job = "squ-120"
ticket = "SQU-120"
pr = "https://github.com/acme/repo/pull/120"
branch = "worker-squ-120"
`, now)

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"job", "reconcile", "status", "--repo", tmp, "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("job reconcile status dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview []jobStatusReconcileResult
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode dry status reconcile json: %v\nbody=%s", err, dryOut.String())
	}
	if len(preview) != 1 || preview[0].JobID != "squ-120" || preview[0].After != job.StatusRunning || !preview[0].Changed || !preview[0].DryRun {
		t.Fatalf("preview = %+v", preview)
	}
	unchanged, err := job.Read(teamDir, "squ-120")
	if err != nil {
		t.Fatalf("read dry-run job: %v", err)
	}
	if unchanged.Status != job.StatusQueued || unchanged.Instance != "" {
		t.Fatalf("dry-run changed job = %+v", unchanged)
	}

	apply := NewRootCmd()
	applyOut, applyErr := &bytes.Buffer{}, &bytes.Buffer{}
	apply.SetOut(applyOut)
	apply.SetErr(applyErr)
	apply.SetArgs([]string{"job", "reconcile", "status", "--repo", tmp, "--json"})
	if err := apply.Execute(); err != nil {
		t.Fatalf("job reconcile status: %v\nstderr=%s", err, applyErr.String())
	}
	var applied []jobStatusReconcileResult
	if err := json.Unmarshal(applyOut.Bytes(), &applied); err != nil {
		t.Fatalf("decode status reconcile json: %v\nbody=%s", err, applyOut.String())
	}
	if len(applied) != 1 || applied[0].JobID != "squ-120" || applied[0].After != job.StatusRunning || !applied[0].Changed || applied[0].DryRun {
		t.Fatalf("applied = %+v", applied)
	}
	updated, err := job.Read(teamDir, "squ-120")
	if err != nil {
		t.Fatalf("read updated job: %v", err)
	}
	if updated.Status != job.StatusRunning || updated.Instance != "worker-squ-120" || updated.Branch != "worker-squ-120" || updated.PR == "" {
		t.Fatalf("updated job = %+v", updated)
	}
	if updated.LastEvent != "status_reconcile" || updated.LastStatus != "writing status reconcile" {
		t.Fatalf("updated status fields = %+v", updated)
	}
	events, err := job.ListEvents(teamDir, "squ-120")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != "status_reconcile" || events[0].Data["phase"] != "implementing" {
		t.Fatalf("events = %+v", events)
	}
}

func TestJobReconcileEventsFromTerminalMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-130",
		Ticket:    "SQU-130",
		Target:    "worker",
		Instance:  "worker-squ-130",
		Status:    job.StatusRunning,
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now.Add(-time.Hour),
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	exitCode := 0
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:  "worker-squ-130",
		Agent:     "worker",
		Job:       "squ-130",
		Ticket:    "SQU-130",
		Branch:    "worker-squ-130",
		PR:        "https://github.com/acme/repo/pull/130",
		Workspace: tmp,
		Status:    daemon.StatusExited,
		ExitCode:  &exitCode,
		StartedAt: now.Add(-time.Minute),
		ExitedAt:  now,
	}); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"job", "reconcile", "events", "--repo", tmp, "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("job reconcile events dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview []jobEventReconcileResult
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode dry events reconcile json: %v\nbody=%s", err, dryOut.String())
	}
	if len(preview) != 1 || preview[0].JobID != "squ-130" || preview[0].After != job.StatusDone || preview[0].Event != "instance_exited" || !preview[0].Changed || !preview[0].DryRun {
		t.Fatalf("preview = %+v", preview)
	}
	unchanged, err := job.Read(teamDir, "squ-130")
	if err != nil {
		t.Fatalf("read dry-run job: %v", err)
	}
	if unchanged.Status != job.StatusRunning || unchanged.Branch != "" || unchanged.PR != "" {
		t.Fatalf("dry-run changed job = %+v", unchanged)
	}

	apply := NewRootCmd()
	applyOut, applyErr := &bytes.Buffer{}, &bytes.Buffer{}
	apply.SetOut(applyOut)
	apply.SetErr(applyErr)
	apply.SetArgs([]string{"job", "reconcile", "events", "--repo", tmp, "--json"})
	if err := apply.Execute(); err != nil {
		t.Fatalf("job reconcile events: %v\nstderr=%s", err, applyErr.String())
	}
	var applied []jobEventReconcileResult
	if err := json.Unmarshal(applyOut.Bytes(), &applied); err != nil {
		t.Fatalf("decode events reconcile json: %v\nbody=%s", err, applyOut.String())
	}
	if len(applied) != 1 || applied[0].JobID != "squ-130" || applied[0].After != job.StatusDone || !applied[0].Changed || applied[0].DryRun {
		t.Fatalf("applied = %+v", applied)
	}
	updated, err := job.Read(teamDir, "squ-130")
	if err != nil {
		t.Fatalf("read updated job: %v", err)
	}
	if updated.Status != job.StatusDone || updated.Instance != "worker-squ-130" || updated.Branch != "worker-squ-130" || updated.PR == "" {
		t.Fatalf("updated job = %+v", updated)
	}
	if updated.LastEvent != "instance_exited" || updated.LastStatus != "instance exited successfully" {
		t.Fatalf("updated status fields = %+v", updated)
	}
	events, err := job.ListEvents(teamDir, "squ-130")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != "instance_exited" || events[0].Actor != "cli" || events[0].Data["source"] != "daemon_metadata" || events[0].Data["matched_by"] != "job" {
		t.Fatalf("events = %+v", events)
	}
}

func TestJobReconcileEventsFromLifecycleEventAfterMetadataRemoved(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-133",
		Ticket:    "SQU-133",
		Target:    "worker",
		Instance:  "worker-squ-133",
		Status:    job.StatusRunning,
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now.Add(-time.Hour),
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	exitCode := 0
	if err := daemon.AppendLifecycleEvent(daemon.DaemonRoot(teamDir), &daemon.LifecycleEvent{
		ID:       "event-worker-squ-133-exit",
		TS:       now,
		Action:   "exit",
		Instance: "worker-squ-133",
		Agent:    "worker",
		Job:      "squ-133",
		Ticket:   "SQU-133",
		Branch:   "worker-squ-133",
		PR:       "https://github.com/acme/repo/pull/133",
		Status:   daemon.StatusExited,
		ExitCode: &exitCode,
		Message:  "instance process exited",
	}); err != nil {
		t.Fatalf("AppendLifecycleEvent: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "reconcile", "events", "--repo", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job reconcile events lifecycle: %v\nstderr=%s", err, stderr.String())
	}
	var result []jobEventReconcileResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode lifecycle events reconcile json: %v\nbody=%s", err, out.String())
	}
	if len(result) != 1 || result[0].JobID != "squ-133" || result[0].After != job.StatusDone || result[0].Event != "instance_exited" || !result[0].Changed || result[0].MatchedBy != "job" {
		t.Fatalf("result = %+v", result)
	}
	updated, err := job.Read(teamDir, "squ-133")
	if err != nil {
		t.Fatalf("read updated job: %v", err)
	}
	if updated.Status != job.StatusDone || updated.Instance != "worker-squ-133" || updated.Branch != "worker-squ-133" || updated.PR == "" {
		t.Fatalf("updated job = %+v", updated)
	}
	if updated.LastEvent != "instance_exited" || updated.LastStatus != "instance exited successfully" {
		t.Fatalf("updated status fields = %+v", updated)
	}
	events, err := job.ListEvents(teamDir, "squ-133")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != "instance_exited" || events[0].Data["source"] != "lifecycle_event" || events[0].Data["lifecycle_event_id"] != "event-worker-squ-133-exit" {
		t.Fatalf("events = %+v", events)
	}
}

func TestJobReconcileEventsMarksCrashedMetadataFailed(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-131",
		Ticket:    "SQU-131",
		Target:    "worker",
		Instance:  "worker-squ-131",
		Status:    job.StatusRunning,
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now.Add(-time.Hour),
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	exitCode := 2
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:  "worker-squ-131",
		Agent:     "worker",
		Job:       "squ-131",
		Ticket:    "SQU-131",
		Workspace: tmp,
		Status:    daemon.StatusCrashed,
		ExitCode:  &exitCode,
		StartedAt: now.Add(-time.Minute),
		ExitedAt:  now,
	}); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "reconcile", "events", "--repo", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job reconcile events crashed: %v\nstderr=%s", err, stderr.String())
	}
	var result []jobEventReconcileResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode crashed events reconcile json: %v\nbody=%s", err, out.String())
	}
	if len(result) != 1 || result[0].After != job.StatusFailed || result[0].Event != "instance_crashed" || result[0].Message != "instance exited with code 2" {
		t.Fatalf("result = %+v", result)
	}
	updated, err := job.Read(teamDir, "squ-131")
	if err != nil {
		t.Fatalf("read updated job: %v", err)
	}
	if updated.Status != job.StatusFailed || updated.LastEvent != "instance_crashed" || updated.LastStatus != "instance exited with code 2" {
		t.Fatalf("updated job = %+v", updated)
	}
}

func TestJobReconcileEventsCompletesPipelineStepIdempotently(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-132",
		Ticket:    "SQU-132",
		Target:    "worker",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now.Add(-time.Hour),
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-132-implement", StartedAt: now.Add(-30 * time.Minute)},
			{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	exitCode := 0
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:  "worker-squ-132-implement",
		Agent:     "worker",
		Job:       "squ-132",
		Ticket:    "SQU-132",
		Branch:    "worker-squ-132",
		Workspace: tmp,
		Status:    daemon.StatusExited,
		ExitCode:  &exitCode,
		StartedAt: now.Add(-30 * time.Minute),
		ExitedAt:  now,
	}); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "reconcile", "events", "--repo", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job reconcile events pipeline: %v\nstderr=%s", err, stderr.String())
	}
	var result []jobEventReconcileResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode pipeline events reconcile json: %v\nbody=%s", err, out.String())
	}
	if len(result) != 1 || result[0].StepID != "implement" || result[0].After != job.StatusRunning || result[0].Message != "completed pipeline step" || !result[0].Changed {
		t.Fatalf("result = %+v", result)
	}
	updated, err := job.Read(teamDir, "squ-132")
	if err != nil {
		t.Fatalf("read updated job: %v", err)
	}
	if updated.Status != job.StatusRunning || updated.Steps[0].Status != job.StatusDone || updated.Steps[0].FinishedAt.IsZero() || updated.LastEvent != "instance_exited" {
		t.Fatalf("updated job = %+v", updated)
	}

	again := NewRootCmd()
	againOut, againErr := &bytes.Buffer{}, &bytes.Buffer{}
	again.SetOut(againOut)
	again.SetErr(againErr)
	again.SetArgs([]string{"job", "reconcile", "events", "--repo", tmp, "--json"})
	if err := again.Execute(); err != nil {
		t.Fatalf("job reconcile events pipeline again: %v\nstderr=%s", err, againErr.String())
	}
	var second []jobEventReconcileResult
	if err := json.Unmarshal(againOut.Bytes(), &second); err != nil {
		t.Fatalf("decode second pipeline events reconcile json: %v\nbody=%s", err, againOut.String())
	}
	if len(second) != 1 || second[0].After != job.StatusRunning || second[0].Changed {
		t.Fatalf("second result = %+v", second)
	}
	events, err := job.ListEvents(teamDir, "squ-132")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != "instance_exited" {
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

	explain := NewRootCmd()
	explainOut, explainErr := &bytes.Buffer{}, &bytes.Buffer{}
	explain.SetOut(explainOut)
	explain.SetErr(explainErr)
	explain.SetArgs([]string{"job", "explain", "squ-214", "--repo", tmp, "--json"})
	if err := explain.Execute(); err != nil {
		t.Fatalf("job explain pipeline create: %v\nstderr=%s", err, explainErr.String())
	}
	var explained jobExplainResult
	if err := json.Unmarshal(explainOut.Bytes(), &explained); err != nil {
		t.Fatalf("decode explain json: %v\nbody=%s", err, explainOut.String())
	}
	if explained.State != "queued" || len(explained.Steps) != 2 {
		t.Fatalf("explained pipeline = %+v", explained)
	}
	if explained.Steps[0].ID != "implement" || explained.Steps[0].State != "ready" || !explained.Steps[0].Ready {
		t.Fatalf("explain first step = %+v", explained.Steps[0])
	}
	if explained.Steps[1].ID != "review" || explained.Steps[1].State != "waiting" || strings.Join(explained.Steps[1].WaitingFor, ",") != "implement" {
		t.Fatalf("explain second step = %+v", explained.Steps[1])
	}
	if !containsString(explained.Actions, "agent-team job advance squ-214") {
		t.Fatalf("explain actions = %+v", explained.Actions)
	}

	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"job", "show", "squ-214", "--repo", tmp})
	if err := show.Execute(); err != nil {
		t.Fatalf("job show pipeline explain action: %v\nstderr=%s", err, showErr.String())
	}
	if !strings.Contains(showOut.String(), "agent-team job explain squ-214") {
		t.Fatalf("job show missing explain action:\n%s", showOut.String())
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

func TestJobCreateDispatchQueuesStoppedPersistentInstance(t *testing.T) {
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
	if result.Job == nil || result.Job.Status != job.StatusQueued || result.Job.Instance != "manager" || result.Job.LastEvent != "queued" {
		t.Fatalf("persistent dispatch result = %+v", result)
	}
	updated, err := job.Read(filepath.Join(target, ".agent_team"), "squ-218")
	if err != nil {
		t.Fatalf("read persistent dispatch job: %v", err)
	}
	if updated.Status != job.StatusQueued || updated.Instance != "manager" || updated.LastEvent != "queued" {
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
	pipelineStep := &job.Job{
		ID:        "squ-52",
		Ticket:    "SQU-52",
		Target:    "ticket-manager",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "triage", Target: "ticket-manager", Status: job.StatusDone},
			{ID: "review", Target: "manager", Instance: "manager-review", Status: job.StatusRunning},
		},
	}
	if err := job.Write(teamDir, first); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if err := job.Write(teamDir, second); err != nil {
		t.Fatalf("write second: %v", err)
	}
	if err := job.Write(teamDir, pipelineStep); err != nil {
		t.Fatalf("write pipeline step: %v", err)
	}
	daemonRoot := daemon.DaemonRoot(teamDir)
	for _, meta := range []*daemon.Metadata{
		{Instance: "worker-squ-50", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: tmp, StartedAt: now},
		{Instance: "manager", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: tmp, StartedAt: now},
		{Instance: "manager-review", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: tmp, StartedAt: now},
	} {
		if err := daemon.WriteMetadata(daemonRoot, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
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
		"--runtime", "codex",
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

	claude := NewRootCmd()
	claudeOut, claudeErr := &bytes.Buffer{}, &bytes.Buffer{}
	claude.SetOut(claudeOut)
	claude.SetErr(claudeErr)
	claude.SetArgs([]string{"job", "ls", "--repo", tmp, "--runtime", "claude", "--json"})
	if err := claude.Execute(); err != nil {
		t.Fatalf("job ls runtime filter: %v\nstderr=%s", err, claudeErr.String())
	}
	got = nil
	if err := json.Unmarshal(claudeOut.Bytes(), &got); err != nil {
		t.Fatalf("decode claude job ls json: %v\nbody=%s", err, claudeOut.String())
	}
	if len(got) != 2 || got[0].ID != "squ-51" || got[1].ID != "squ-52" {
		t.Fatalf("claude runtime jobs = %+v", got)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"job", "ls", "--repo", tmp, "--status", "running"})
	if err := text.Execute(); err != nil {
		t.Fatalf("job ls text runtime column: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"RUNTIME", "squ-50", "codex", "squ-52", "claude"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("job ls text missing %q:\n%s", want, textOut.String())
		}
	}

	invalid := NewRootCmd()
	invalidOut, invalidErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalid.SetOut(invalidOut)
	invalid.SetErr(invalidErr)
	invalid.SetArgs([]string{"job", "ls", "--repo", tmp, "--runtime", "bad"})
	if err := invalid.Execute(); err == nil {
		t.Fatalf("job ls invalid runtime succeeded")
	}
	if !strings.Contains(invalidErr.String(), "unknown --runtime") {
		t.Fatalf("invalid runtime stderr = %q", invalidErr.String())
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
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:      "worker-squ-66",
		Agent:         "worker",
		Runtime:       string(runtimebin.KindCodex),
		RuntimeBinary: "codex-dev",
		Status:        daemon.StatusRunning,
		PID:           os.Getpid(),
		Workspace:     tmp,
		StartedAt:     now,
	}); err != nil {
		t.Fatalf("write runtime metadata: %v", err)
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
	if summary.Runtimes["codex"] != 1 {
		t.Fatalf("summary runtimes = %+v", summary.Runtimes)
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

	limited := NewRootCmd()
	limitedOut, limitedErr := &bytes.Buffer{}, &bytes.Buffer{}
	limited.SetOut(limitedOut)
	limited.SetErr(limitedErr)
	limited.SetArgs([]string{"job", "ls", "--repo", tmp, "--sort", "updated", "--limit", "2", "--format", "{{.ID}}"})
	if err := limited.Execute(); err != nil {
		t.Fatalf("job ls limit: %v\nstderr=%s", err, limitedErr.String())
	}
	if got := strings.Split(strings.TrimSpace(limitedOut.String()), "\n"); strings.Join(got, ",") != "squ-73,squ-74" {
		t.Fatalf("limited output = %q", limitedOut.String())
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

	invalidLimit := NewRootCmd()
	invalidLimitOut, invalidLimitErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidLimit.SetOut(invalidLimitOut)
	invalidLimit.SetErr(invalidLimitErr)
	invalidLimit.SetArgs([]string{"job", "ls", "--repo", tmp, "--limit", "-1"})
	if err := invalidLimit.Execute(); err == nil {
		t.Fatalf("job ls negative limit succeeded unexpectedly")
	}
	if !strings.Contains(invalidLimitErr.String(), "--limit must be >= 0") {
		t.Fatalf("invalid limit stderr = %q", invalidLimitErr.String())
	}

	summaryLimit := NewRootCmd()
	summaryLimitOut, summaryLimitErr := &bytes.Buffer{}, &bytes.Buffer{}
	summaryLimit.SetOut(summaryLimitOut)
	summaryLimit.SetErr(summaryLimitErr)
	summaryLimit.SetArgs([]string{"job", "ls", "--repo", tmp, "--summary", "--limit", "1"})
	if err := summaryLimit.Execute(); err == nil {
		t.Fatalf("job ls summary limit succeeded unexpectedly")
	}
	if !strings.Contains(summaryLimitErr.String(), "--limit cannot be combined with --summary") {
		t.Fatalf("summary limit stderr = %q", summaryLimitErr.String())
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

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"job", "reopen", "SQU-68", "--repo", tmp, "--message", "retry after fix", "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("job reopen dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview jobReopenPreview
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode reopen dry-run json: %v\nbody=%s", err, dryOut.String())
	}
	if !preview.DryRun || preview.Job == nil || preview.Job.Status != job.StatusQueued || preview.Job.LastEvent != "reopened" || preview.Job.LastStatus != "retry after fix" {
		t.Fatalf("preview = %+v", preview)
	}
	unchanged, err := job.Read(teamDir, "squ-68")
	if err != nil {
		t.Fatalf("read dry-run job: %v", err)
	}
	if unchanged.Status != job.StatusFailed || unchanged.LastEvent != "dispatch_failed" {
		t.Fatalf("dry-run mutated job = %+v", unchanged)
	}
	previewEvents, err := job.ListEvents(teamDir, "squ-68")
	if err != nil {
		t.Fatalf("ListEvents dry-run: %v", err)
	}
	if len(previewEvents) != 0 {
		t.Fatalf("dry-run wrote events = %+v", previewEvents)
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

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"job", "retry", "SQU-80", "--repo", target, "--dispatch", "--workspace", "repo", "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("job retry --dispatch dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview jobDispatchPreview
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode retry dispatch dry-run json: %v\nbody=%s", err, dryOut.String())
	}
	if !preview.DryRun || preview.Job == nil || preview.Job.Status != job.StatusQueued || preview.Job.LastEvent != "reopened" {
		t.Fatalf("preview = %+v", preview)
	}
	if preview.Dispatch == nil || preview.Dispatch.RequestedName != "worker-squ-80" {
		t.Fatalf("dispatch preview = %+v", preview.Dispatch)
	}
	previewPayload := preview.Dispatch.Preview.Payload
	if previewPayload["job_id"] != "squ-80" || previewPayload["workspace"] != "repo" {
		t.Fatalf("payload = %+v", previewPayload)
	}
	unchanged, err := job.Read(teamDir, "squ-80")
	if err != nil {
		t.Fatalf("read dry-run job: %v", err)
	}
	if unchanged.Status != job.StatusFailed || unchanged.LastEvent != "dispatch_failed" {
		t.Fatalf("dry-run mutated job = %+v", unchanged)
	}
	previewEvents, err := job.ListEvents(teamDir, "squ-80")
	if err != nil {
		t.Fatalf("ListEvents dry-run: %v", err)
	}
	if len(previewEvents) != 0 {
		t.Fatalf("dry-run wrote events = %+v", previewEvents)
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

func TestJobRetryDispatchResetsFailedPipelineStep(t *testing.T) {
	target, _, cleanup := setupIntakePipelineRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:         "squ-81",
		Ticket:     "SQU-81",
		Target:     "manager",
		Kickoff:    "retry failed review",
		Pipeline:   "ticket_triage",
		Status:     job.StatusFailed,
		LastEvent:  "step_failed",
		LastStatus: "review failed",
		CreatedAt:  now,
		UpdatedAt:  now,
		Steps: []job.Step{
			{ID: "triage", Target: "manager", Status: job.StatusDone, Instance: "manager", StartedAt: now.Add(-3 * time.Hour), FinishedAt: now.Add(-2 * time.Hour)},
			{ID: "review", Target: "manager", Status: job.StatusFailed, Instance: "manager-old", After: []string{"triage"}, StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write failed pipeline job: %v", err)
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"job", "retry", "squ-81", "--repo", target, "--dispatch", "--workspace", "repo", "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("job retry pipeline dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview jobAdvancePreview
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode retry pipeline dry-run json: %v\nbody=%s", err, dryOut.String())
	}
	if !preview.DryRun || preview.Job == nil || preview.Job.Status != job.StatusQueued || preview.Job.LastEvent != "reopened" || preview.Step == nil || preview.Step.ID != "review" {
		t.Fatalf("preview = %+v", preview)
	}
	if preview.Step.Status != job.StatusBlocked || preview.Step.Instance != "" || !preview.Step.FinishedAt.IsZero() {
		t.Fatalf("preview step = %+v", preview.Step)
	}
	if preview.Dispatch == nil || preview.Dispatch.RequestedName != "manager-squ-81-review" {
		t.Fatalf("dispatch preview = %+v", preview.Dispatch)
	}
	payload := preview.Dispatch.Preview.Payload
	if payload["pipeline"] != "ticket_triage" || payload["pipeline_step"] != "review" || payload["job_id"] != "squ-81" || payload["workspace"] != "repo" {
		t.Fatalf("payload = %+v", payload)
	}
	unchanged, err := job.Read(teamDir, "squ-81")
	if err != nil {
		t.Fatalf("read dry-run job: %v", err)
	}
	if unchanged.Status != job.StatusFailed || unchanged.Steps[1].Status != job.StatusFailed || unchanged.Steps[1].Instance != "manager-old" || unchanged.Steps[1].FinishedAt.IsZero() {
		t.Fatalf("dry-run mutated job = %+v", unchanged)
	}
	previewEvents, err := job.ListEvents(teamDir, "squ-81")
	if err != nil {
		t.Fatalf("ListEvents dry-run: %v", err)
	}
	if len(previewEvents) != 0 {
		t.Fatalf("dry-run wrote events = %+v", previewEvents)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "retry", "squ-81", "--repo", target, "--dispatch", "--workspace", "repo", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job retry pipeline --dispatch: %v\nstderr=%s", err, stderr.String())
	}
	var result jobAdvanceResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode retry pipeline json: %v\nbody=%s", err, out.String())
	}
	if result.Step == nil || result.Step.ID != "review" || result.Step.Status != job.StatusQueued || result.Step.Instance != "manager" {
		t.Fatalf("result = %+v", result)
	}
	updated, err := job.Read(teamDir, "squ-81")
	if err != nil {
		t.Fatalf("read updated job: %v", err)
	}
	if updated.Status != job.StatusQueued || updated.Steps[1].Status != job.StatusQueued || updated.Steps[1].Instance != "manager" {
		t.Fatalf("updated = %+v", updated)
	}
	if updated.Steps[1].FinishedAt.IsZero() == false {
		t.Fatalf("retry should clear old finished_at, got %+v", updated.Steps[1])
	}
	events, err := job.ListEvents(teamDir, "squ-81")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) < 1 || events[0].Type != "reopened" || events[0].Data["step"] != "review" {
		t.Fatalf("events = %+v", events)
	}
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

func TestJobWaitPollsUntilLastEvent(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-62",
		Ticket:    "SQU-62",
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
		updated, err := job.Read(teamDir, "squ-62")
		if err != nil {
			t.Errorf("read job in updater: %v", err)
			return
		}
		updated.LastEvent = "adopted"
		updated.LastStatus = "external process adopted"
		updated.UpdatedAt = time.Now().UTC()
		if err := job.Write(teamDir, updated); err != nil {
			t.Errorf("write adopted job: %v", err)
		}
	}()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "wait", "SQU-62", "--repo", tmp, "--event", "adopted", "--timeout", "2s", "--interval", "10ms", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job wait event: %v\nstderr=%s", err, stderr.String())
	}
	<-done
	var got job.Job
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode wait event json: %v\nbody=%s", err, out.String())
	}
	if got.Status != job.StatusRunning || got.LastEvent != "adopted" {
		t.Fatalf("wait event result = %+v", got)
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

func TestJobWaitEventTimesOut(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-63",
		Ticket:    "SQU-63",
		Target:    "worker",
		Status:    job.StatusRunning,
		LastEvent: "dispatched",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "wait", "squ-63", "--repo", tmp, "--event", "closed", "--timeout", "1ms", "--interval", "10ms"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("job wait event succeeded unexpectedly")
	}
	if !strings.Contains(stderr.String(), "event=closed") || !strings.Contains(stderr.String(), "current=running event=dispatched") {
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

func TestJobLogsLastMessageUsesOwningInstanceSidecar(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-56",
		Ticket:    "SQU-56",
		Target:    "worker",
		Instance:  "worker-squ-56",
		Status:    job.StatusDone,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	writeLastMessageForTest(t, teamDir, "worker-squ-56", "clean job final")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "logs", "SQU-56", "--repo", tmp, "--last-message"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job logs last-message: %v\nstderr=%s", err, stderr.String())
	}
	if got := out.String(); got != "clean job final\n" {
		t.Fatalf("job last-message output = %q, want clean sidecar", got)
	}
}

func TestJobLogsLastMessageRejectsLogFilters(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-57",
		Ticket:    "SQU-57",
		Target:    "worker",
		Instance:  "worker-squ-57",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "logs", "SQU-57", "--repo", tmp, "--last-message", "--tail", "1"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("job logs last-message with tail succeeded")
	}
	var code ExitCode
	if !errors.As(err, &code) || code != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if !strings.Contains(stderr.String(), "--last-message cannot be combined with --tail") {
		t.Fatalf("stderr = %q, want tail validation", stderr.String())
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

func TestJobAttachDryRunUnsupportedCodexShowsJobFallbacks(t *testing.T) {
	env := newAttachTestEnv(t)
	sleep := exec.Command("sleep", "30")
	if err := sleep.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	t.Cleanup(func() {
		_ = sleep.Process.Kill()
		_, _ = sleep.Process.Wait()
	})

	meta := &daemon.Metadata{
		Instance:      "worker-squ-57",
		Agent:         "worker",
		Runtime:       string(runtimebin.KindCodex),
		RuntimeBinary: runtimebin.DefaultBinaryForKind(runtimebin.KindCodex),
		Workspace:     env.target,
		PID:           sleep.Process.Pid,
		SessionID:     "codex-job-session",
		StartedAt:     time.Now().UTC(),
		Status:        daemon.StatusRunning,
	}
	if err := daemon.WriteMetadata(daemon.DaemonRoot(env.teamDir), meta); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	if err := env.dmn.Manager().LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	now := time.Now().UTC()
	if err := job.Write(env.teamDir, &job.Job{
		ID:        "squ-57",
		Ticket:    "SQU-57",
		Target:    "worker",
		Instance:  "worker-squ-57",
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
	cmd.SetArgs([]string{"job", "attach", "SQU-57", "--repo", env.target, "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job attach codex dry-run: %v\nstderr=%s", err, stderr.String())
	}
	if cap.called {
		t.Fatal("execClaudeAttach should not run during unsupported dry-run")
	}
	body := out.String()
	for _, want := range []string{
		"runtime:              codex",
		"managed_resume:       no",
		"command:              codex resume codex-job-session",
		"logs_command:         agent-team logs worker-squ-57 --follow",
		"last_message_command: agent-team logs worker-squ-57 --last-message",
		"job_logs_command:      agent-team job logs squ-57 --follow",
		"job_last_message_command: agent-team job logs squ-57 --last-message",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("job attach dry-run missing %q:\n%s", want, body)
		}
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
	beforeDryRun, err := job.Read(filepath.Join(target, ".agent_team"), "squ-43")
	if err != nil {
		t.Fatalf("read job before dry-run send: %v", err)
	}

	dryRun := NewRootCmd()
	dryRunOut, dryRunErr := &bytes.Buffer{}, &bytes.Buffer{}
	dryRun.SetOut(dryRunOut)
	dryRun.SetErr(dryRunErr)
	dryRun.SetArgs([]string{"job", "send", "SQU-43", "preview status ping", "--repo", target, "--dry-run", "--json"})
	if err := dryRun.Execute(); err != nil {
		t.Fatalf("job send dry-run: %v\nstderr=%s", err, dryRunErr.String())
	}
	var preview struct {
		ID       string  `json:"id"`
		Job      job.Job `json:"job"`
		DryRun   bool    `json:"dry_run"`
		Instance string  `json:"instance"`
		From     string  `json:"from"`
		Message  string  `json:"message"`
	}
	if err := json.Unmarshal(dryRunOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode job send dry-run: %v\nbody=%s", err, dryRunOut.String())
	}
	if !preview.DryRun || preview.ID != "squ-43" || preview.Job.ID != "squ-43" || preview.Instance != "worker-squ-43" || preview.From != "(cli)" || preview.Message != "preview status ping" {
		t.Fatalf("dry-run preview = %+v", preview)
	}
	messages, err = daemon.ReadMessages(daemon.DaemonRoot(filepath.Join(target, ".agent_team")), "worker-squ-43")
	if err != nil {
		t.Fatalf("read messages after dry-run: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("dry-run appended messages = %+v", messages)
	}
	afterDryRun, err := job.Read(filepath.Join(target, ".agent_team"), "squ-43")
	if err != nil {
		t.Fatalf("read job after dry-run send: %v", err)
	}
	if afterDryRun.LastEvent != beforeDryRun.LastEvent || afterDryRun.LastStatus != beforeDryRun.LastStatus || !afterDryRun.UpdatedAt.Equal(beforeDryRun.UpdatedAt) {
		t.Fatalf("dry-run mutated job before=%+v after=%+v", beforeDryRun, afterDryRun)
	}

	dryRunFormat := NewRootCmd()
	dryRunFormatOut, dryRunFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	dryRunFormat.SetOut(dryRunFormatOut)
	dryRunFormat.SetErr(dryRunFormatErr)
	dryRunFormat.SetArgs([]string{"job", "send", "SQU-43", "--message", "formatted preview", "--repo", target, "--dry-run", "--format", "{{.ID}} {{.DryRun}} {{.Instance}}"})
	if err := dryRunFormat.Execute(); err != nil {
		t.Fatalf("job send dry-run format: %v\nstderr=%s", err, dryRunFormatErr.String())
	}
	if got, want := dryRunFormatOut.String(), "squ-43 true worker-squ-43\n"; got != want {
		t.Fatalf("dry-run format output = %q, want %q", got, want)
	}

	messageFile := filepath.Join(target, "handoff.txt")
	if err := os.WriteFile(messageFile, []byte("first line\nsecond line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sendFile := NewRootCmd()
	sendFileOut, sendFileErr := &bytes.Buffer{}, &bytes.Buffer{}
	sendFile.SetOut(sendFileOut)
	sendFile.SetErr(sendFileErr)
	sendFile.SetArgs([]string{"job", "send", "SQU-43", "--message-file", messageFile, "--repo", target, "--format", "{{.ID}} {{.LastEvent}}"})
	if err := sendFile.Execute(); err != nil {
		t.Fatalf("job send file: %v\nstderr=%s", err, sendFileErr.String())
	}
	if got, want := sendFileOut.String(), "squ-43 message_sent\n"; got != want {
		t.Fatalf("send file output = %q, want %q", got, want)
	}
	messages, err = daemon.ReadMessages(daemon.DaemonRoot(filepath.Join(target, ".agent_team")), "worker-squ-43")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 2 || messages[1].Body != "first line\nsecond line" {
		t.Fatalf("messages after file send = %+v", messages)
	}

	prevInput := sendMessageInput
	sendMessageInput = strings.NewReader("stdin handoff\n")
	defer func() { sendMessageInput = prevInput }()
	sendStdin := NewRootCmd()
	sendStdinOut, sendStdinErr := &bytes.Buffer{}, &bytes.Buffer{}
	sendStdin.SetOut(sendStdinOut)
	sendStdin.SetErr(sendStdinErr)
	sendStdin.SetArgs([]string{"job", "send", "SQU-43", "--message-file", "-", "--repo", target, "--json"})
	if err := sendStdin.Execute(); err != nil {
		t.Fatalf("job send stdin: %v\nstderr=%s", err, sendStdinErr.String())
	}
	var sentJob job.Job
	if err := json.Unmarshal(sendStdinOut.Bytes(), &sentJob); err != nil {
		t.Fatalf("decode send stdin: %v\nbody=%s", err, sendStdinOut.String())
	}
	if sentJob.ID != "squ-43" || sentJob.LastStatus != "stdin handoff" {
		t.Fatalf("send stdin job = %+v", sentJob)
	}
	messages, err = daemon.ReadMessages(daemon.DaemonRoot(filepath.Join(target, ".agent_team")), "worker-squ-43")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 3 || messages[2].Body != "stdin handoff" {
		t.Fatalf("messages after stdin send = %+v", messages)
	}

	updated, err := job.Read(filepath.Join(target, ".agent_team"), "squ-43")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.LastEvent != "message_sent" || updated.LastStatus != "stdin handoff" {
		t.Fatalf("updated job = %+v", updated)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-43")
}

func TestJobSendMessageSourceValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"job", "send", "SQU-43", "--repo", t.TempDir()}, "message body is required"},
		{[]string{"job", "send", "SQU-43", "hello", "--message", "also"}, "provide message text using only one"},
		{[]string{"job", "send", "SQU-43", "--message-file", filepath.Join(t.TempDir(), "missing.txt")}, "--message-file:"},
		{[]string{"job", "send", "SQU-43", "--message", "hello", "--json", "--format", "{{.ID}}"}, "--format cannot be combined"},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		var code ExitCode
		if !errors.As(err, &code) || int(code) != 2 {
			t.Fatalf("%v: err = %v, want exit 2", tc.args, err)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
		if out.Len() != 0 {
			t.Fatalf("%v: validation wrote stdout: %q", tc.args, out.String())
		}
	}
}

func TestJobNoteRecordsAuditEvent(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-73",
		Ticket:    "SQU-73",
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
	cmd.SetArgs([]string{"job", "note", "SQU-73", "blocked on staging credentials", "--repo", tmp, "--actor", "ops", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job note: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var noted job.Job
	if err := json.Unmarshal(out.Bytes(), &noted); err != nil {
		t.Fatalf("decode note json: %v\nbody=%s", err, out.String())
	}
	if noted.Status != job.StatusRunning || noted.LastEvent != "note" || noted.LastStatus != "blocked on staging credentials" {
		t.Fatalf("noted job = %+v", noted)
	}
	events, err := job.ListEvents(teamDir, "squ-73")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 1 || events[0].Type != "note" || events[0].Actor != "ops" || events[0].Message != "blocked on staging credentials" || events[0].Status != job.StatusRunning {
		t.Fatalf("events = %+v", events)
	}
}

func TestJobNoteDryRunDoesNotMutateJob(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	original := &job.Job{
		ID:        "squ-74",
		Ticket:    "SQU-74",
		Target:    "worker",
		Status:    job.StatusQueued,
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
	cmd.SetArgs([]string{"job", "note", "squ-74", "--repo", tmp, "--message", "preview note", "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job note dry-run: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var preview jobActionPreview
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil {
		t.Fatalf("decode note preview: %v\nbody=%s", err, out.String())
	}
	if !preview.DryRun || preview.Job == nil || preview.Job.LastEvent != "note" || preview.Job.LastStatus != "preview note" {
		t.Fatalf("preview = %+v", preview)
	}
	updated, err := job.Read(teamDir, "squ-74")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.LastEvent != "" || updated.LastStatus != "" || !updated.UpdatedAt.Equal(original.UpdatedAt) {
		t.Fatalf("dry-run mutated job = %+v", updated)
	}
	events, err := job.ListEvents(teamDir, "squ-74")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("dry-run wrote events = %+v", events)
	}
}

func TestJobBlockMarksBlockedAndRecordsEvent(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-75",
		Ticket:    "SQU-75",
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
	cmd.SetArgs([]string{"job", "block", "SQU-75", "waiting on vendor API", "--repo", tmp, "--actor", "linear", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job block: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var blocked job.Job
	if err := json.Unmarshal(out.Bytes(), &blocked); err != nil {
		t.Fatalf("decode block json: %v\nbody=%s", err, out.String())
	}
	if blocked.Status != job.StatusBlocked || blocked.LastEvent != "blocked" || blocked.LastStatus != "waiting on vendor API" {
		t.Fatalf("blocked job = %+v", blocked)
	}
	events, err := job.ListEvents(teamDir, "squ-75")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 1 || events[0].Type != "blocked" || events[0].Actor != "linear" || events[0].Message != "waiting on vendor API" || events[0].Data["status"] != "blocked" {
		t.Fatalf("events = %+v", events)
	}
}

func TestJobBlockDryRunDoesNotMutateJob(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	original := &job.Job{
		ID:        "squ-76",
		Ticket:    "SQU-76",
		Target:    "worker",
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
	cmd.SetArgs([]string{"job", "block", "squ-76", "--repo", tmp, "--message", "preview block", "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job block dry-run: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var preview jobActionPreview
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil {
		t.Fatalf("decode block preview: %v\nbody=%s", err, out.String())
	}
	if !preview.DryRun || preview.Job == nil || preview.Job.Status != job.StatusBlocked || preview.Job.LastEvent != "blocked" || preview.Job.LastStatus != "preview block" {
		t.Fatalf("preview = %+v", preview)
	}
	updated, err := job.Read(teamDir, "squ-76")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.Status != job.StatusRunning || updated.LastEvent != "" || updated.LastStatus != "" || !updated.UpdatedAt.Equal(original.UpdatedAt) {
		t.Fatalf("dry-run mutated job = %+v", updated)
	}
	events, err := job.ListEvents(teamDir, "squ-76")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("dry-run wrote events = %+v", events)
	}
}

func TestJobUnblockSendsMessageAndMarksRunning(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now().UTC()
	blocked := &job.Job{
		ID:         "squ-82",
		Ticket:     "SQU-82",
		Target:     "worker",
		Instance:   "worker-squ-82",
		Status:     job.StatusBlocked,
		LastEvent:  "status_blocked",
		LastStatus: "needs credentials",
		CreatedAt:  now.Add(-time.Hour),
		UpdatedAt:  now.Add(-30 * time.Minute),
	}
	if err := job.Write(teamDir, blocked); err != nil {
		t.Fatalf("write blocked job: %v", err)
	}
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance:  "worker-squ-82",
		Agent:     "worker",
		Status:    daemon.StatusRunning,
		StartedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	answerFile := filepath.Join(tmp, "answer.txt")
	if err := os.WriteFile(answerFile, []byte("credentials are now configured\ncontinue\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "unblock", "SQU-82", "--message-file", answerFile, "--repo", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job unblock: %v\nstderr=%s", err, stderr.String())
	}
	var updated job.Job
	if err := json.Unmarshal(out.Bytes(), &updated); err != nil {
		t.Fatalf("decode unblock json: %v\nbody=%s", err, out.String())
	}
	if updated.Status != job.StatusRunning || updated.LastEvent != "unblocked" || updated.LastStatus != "credentials are now configured\ncontinue" {
		t.Fatalf("updated = %+v", updated)
	}
	messages, err := daemon.ReadMessages(root, "worker-squ-82")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 1 || messages[0].From != "(cli)" || messages[0].Body != "credentials are now configured\ncontinue" {
		t.Fatalf("messages = %+v", messages)
	}
	events, err := job.ListEvents(teamDir, "squ-82")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != "unblocked" || events[0].Status != job.StatusRunning || events[0].Data["instance"] != "worker-squ-82" {
		t.Fatalf("events = %+v", events)
	}
}

func TestJobUnblockMessageSourceValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"job", "unblock", "SQU-82", "--repo", t.TempDir()}, "message body is required"},
		{[]string{"job", "unblock", "SQU-82", "hello", "--message", "also"}, "provide message text using only one"},
		{[]string{"job", "unblock", "SQU-82", "--message-file", filepath.Join(t.TempDir(), "missing.txt")}, "--message-file:"},
		{[]string{"job", "unblock", "SQU-82", "--message", "hello", "--json", "--format", "{{.ID}}"}, "--format cannot be combined"},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		var code ExitCode
		if !errors.As(err, &code) || int(code) != 2 {
			t.Fatalf("%v: err = %v, want exit 2", tc.args, err)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
		if out.Len() != 0 {
			t.Fatalf("%v: validation wrote stdout: %q", tc.args, out.String())
		}
	}
}

func TestJobUnblockAcceptsBlockedStatusPreview(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now().UTC().Truncate(time.Second)
	queued := &job.Job{
		ID:        "squ-84",
		Ticket:    "SQU-84",
		Target:    "worker",
		Status:    job.StatusQueued,
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now.Add(-30 * time.Minute),
	}
	if err := job.Write(teamDir, queued); err != nil {
		t.Fatalf("write queued job: %v", err)
	}
	writeStatus(t, filepath.Join(teamDir, "state", "worker-squ-84"), `[status]
phase = "blocked"
description = "needs token"
since = "2026-06-18T12:00:00Z"

[work]
job = "squ-84"
ticket = "SQU-84"
branch = "worker-squ-84"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "unblock", "SQU-84", "token is configured", "--repo", tmp, "--allow-missing", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job unblock status preview: %v\nstderr=%s", err, stderr.String())
	}
	var updated job.Job
	if err := json.Unmarshal(out.Bytes(), &updated); err != nil {
		t.Fatalf("decode unblock json: %v\nbody=%s", err, out.String())
	}
	if updated.Status != job.StatusRunning || updated.Instance != "worker-squ-84" || updated.Branch != "worker-squ-84" || updated.LastEvent != "unblocked" {
		t.Fatalf("updated = %+v", updated)
	}
	messages, err := daemon.ReadMessages(root, "worker-squ-84")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 1 || messages[0].Body != "token is configured" {
		t.Fatalf("messages = %+v", messages)
	}
	events, err := job.ListEvents(teamDir, "squ-84")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != "unblocked" || events[0].Data["status_preview"] != "true" || events[0].Data["phase"] != "blocked" {
		t.Fatalf("events = %+v", events)
	}

	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"job", "show", "SQU-84", "--repo", tmp})
	if err := show.Execute(); err != nil {
		t.Fatalf("job show after unblock: %v\nstderr=%s", err, showErr.String())
	}
	if strings.Contains(showOut.String(), "Status Preview:") {
		t.Fatalf("stale blocked status should be superseded after unblock:\n%s", showOut.String())
	}

	triage := NewRootCmd()
	triageOut, triageErr := &bytes.Buffer{}, &bytes.Buffer{}
	triage.SetOut(triageOut)
	triage.SetErr(triageErr)
	triage.SetArgs([]string{"job", "triage", "--repo", tmp, "--json"})
	if err := triage.Execute(); err != nil {
		t.Fatalf("job triage after unblock: %v\nstderr=%s", err, triageErr.String())
	}
	var snapshot jobTriageSnapshot
	if err := json.Unmarshal(triageOut.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode triage json: %v\nbody=%s", err, triageOut.String())
	}
	if len(snapshot.StatusPreviews) != 0 || len(snapshot.Attention) != 0 {
		t.Fatalf("triage should ignore stale blocked status after unblock: %+v", snapshot)
	}
}

func TestJobUnblockDryRunDoesNotMutateOrSend(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now().UTC().Truncate(time.Second)
	queued := &job.Job{
		ID:        "squ-85",
		Ticket:    "SQU-85",
		Target:    "worker",
		Status:    job.StatusQueued,
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now.Add(-30 * time.Minute),
	}
	if err := job.Write(teamDir, queued); err != nil {
		t.Fatalf("write queued job: %v", err)
	}
	writeStatus(t, filepath.Join(teamDir, "state", "worker-squ-85"), `[status]
phase = "blocked"
description = "needs token"
since = "2026-06-18T12:00:00Z"

[work]
job = "squ-85"
ticket = "SQU-85"
branch = "worker-squ-85"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "unblock", "SQU-85", "token is configured", "--repo", tmp, "--allow-missing", "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job unblock dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var preview jobUnblockPreview
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil {
		t.Fatalf("decode unblock preview json: %v\nbody=%s", err, out.String())
	}
	if !preview.DryRun || preview.Job == nil || preview.Job.Status != job.StatusRunning || preview.Job.Instance != "worker-squ-85" || !preview.StatusPreview {
		t.Fatalf("preview = %+v", preview)
	}
	persisted, err := job.Read(teamDir, "squ-85")
	if err != nil {
		t.Fatalf("read persisted job: %v", err)
	}
	if persisted.Status != job.StatusQueued || persisted.Instance != "" || persisted.LastEvent != "" {
		t.Fatalf("dry-run mutated job = %+v", persisted)
	}
	messages, err := daemon.ReadMessages(root, "worker-squ-85")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("dry-run sent messages = %+v", messages)
	}
	events, err := job.ListEvents(teamDir, "squ-85")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("dry-run wrote events = %+v", events)
	}
}

func TestJobUnblockRefusesNonBlockedWithoutForce(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	running := &job.Job{
		ID:        "squ-83",
		Ticket:    "SQU-83",
		Target:    "worker",
		Instance:  "worker-squ-83",
		Status:    job.StatusRunning,
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
	refused.SetArgs([]string{"job", "unblock", "squ-83", "continue", "--repo", tmp})
	if err := refused.Execute(); err == nil {
		t.Fatalf("job unblock running succeeded unexpectedly: stdout=%s", refusedOut.String())
	}
	if !strings.Contains(refusedErr.String(), "pass --force to unblock anyway") {
		t.Fatalf("stderr = %q", refusedErr.String())
	}
	if messages, err := daemon.ReadMessages(daemon.DaemonRoot(teamDir), "worker-squ-83"); err == nil && len(messages) != 0 {
		t.Fatalf("messages should not be sent: %+v", messages)
	}
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

	previewFormat := NewRootCmd()
	previewFormatOut, previewFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	previewFormat.SetOut(previewFormatOut)
	previewFormat.SetErr(previewFormatErr)
	previewFormat.SetArgs([]string{"job", "cleanup", "squ-44", "--dry-run", "--repo", target, "--format", "{{.JobID}} {{.DryRun}} {{.Summary}}"})
	if err := previewFormat.Execute(); err != nil {
		t.Fatalf("job cleanup dry-run format: %v\nstderr=%s", err, previewFormatErr.String())
	}
	if got, want := previewFormatOut.String(), "squ-44 true would remove worktree and branch\n"; got != want {
		t.Fatalf("job cleanup dry-run format = %q, want %q", got, want)
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

	readyForCleanup, err := job.Read(teamDir, "squ-44")
	if err != nil {
		t.Fatalf("read job before cleanup: %v", err)
	}
	readyForCleanup.Status = job.StatusDone
	readyForCleanup.PR = "https://github.com/acme/repo/pull/44"
	readyForCleanup.LastEvent = "pr.merged"
	readyForCleanup.LastStatus = "pull request merged"
	readyForCleanup.UpdatedAt = time.Now().UTC()
	if err := job.Write(teamDir, readyForCleanup); err != nil {
		t.Fatalf("write job before cleanup: %v", err)
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

func TestJobCleanupMergedRejectsRunningJob(t *testing.T) {
	target := t.TempDir()
	initInto(t, target)
	initGitRepoForJobTest(t, target)
	teamDir := filepath.Join(target, ".agent_team")
	branch := "worktree-worker-squ-45"
	runGitForJobTest(t, target, "checkout", "-b", branch)
	runGitForJobTest(t, target, "checkout", "main")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-45",
		Ticket:    "SQU-45",
		Target:    "worker",
		Status:    job.StatusRunning,
		Branch:    branch,
		Worktree:  filepath.Join(target, ".claude", "worktrees", "worker-squ-45"),
		PR:        "https://github.com/acme/repo/pull/45",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write running job: %v", err)
	}

	cleanupCmd := NewRootCmd()
	cleanupOut, cleanupErr := &bytes.Buffer{}, &bytes.Buffer{}
	cleanupCmd.SetOut(cleanupOut)
	cleanupCmd.SetErr(cleanupErr)
	cleanupCmd.SetArgs([]string{"job", "cleanup", "squ-45", "--merged", "--repo", target, "--json"})
	err := cleanupCmd.Execute()
	if err == nil {
		t.Fatalf("running cleanup unexpectedly succeeded: stdout=%s", cleanupOut.String())
	}
	var code ExitCode
	if !errors.As(err, &code) || int(code) != 2 {
		t.Fatalf("cleanup err = %v, want exit 2", err)
	}
	if !strings.Contains(cleanupErr.String(), "close or reconcile it as done before cleanup") {
		t.Fatalf("cleanup stderr = %q", cleanupErr.String())
	}
	stillRunning, err := job.Read(teamDir, "squ-45")
	if err != nil {
		t.Fatalf("read running job: %v", err)
	}
	if stillRunning.Status != job.StatusRunning || stillRunning.Branch != branch || stillRunning.Worktree != j.Worktree {
		t.Fatalf("running cleanup mutated job = %+v", stillRunning)
	}
	if !branchExists(t, target, branch) {
		t.Fatalf("cleanup removed branch %s", branch)
	}
}

func TestJobCleanupCanForceDeleteUnmergedBranch(t *testing.T) {
	target := t.TempDir()
	initInto(t, target)
	initGitRepoForJobTest(t, target)
	teamDir := filepath.Join(target, ".agent_team")
	branch := "worktree-worker-squ-46-force"
	runGitForJobTest(t, target, "checkout", "-b", branch)
	if err := os.WriteFile(filepath.Join(target, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForJobTest(t, target, "add", "feature.txt")
	runGitForJobTest(t, target, "commit", "-m", "feature")
	runGitForJobTest(t, target, "checkout", "main")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-46",
		Ticket:    "SQU-46",
		Target:    "worker",
		Status:    job.StatusDone,
		Branch:    branch,
		PR:        "https://github.com/acme/repo/pull/46",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	preview := NewRootCmd()
	previewOut, previewErr := &bytes.Buffer{}, &bytes.Buffer{}
	preview.SetOut(previewOut)
	preview.SetErr(previewErr)
	preview.SetArgs([]string{"job", "cleanup", "squ-46", "--repo", target, "--dry-run", "--force-branch", "--json"})
	if err := preview.Execute(); err != nil {
		t.Fatalf("job cleanup force preview: %v\nstderr=%s", err, previewErr.String())
	}
	var previewResult jobCleanupPreview
	if err := json.Unmarshal(previewOut.Bytes(), &previewResult); err != nil {
		t.Fatalf("decode cleanup force preview: %v\nbody=%s", err, previewOut.String())
	}
	if !previewResult.ForceBranch || previewResult.BranchDeleteMode != "force_delete" || !previewResult.WouldRemoveBranch || previewResult.Summary != "would remove branch (force)" {
		t.Fatalf("force cleanup preview = %+v", previewResult)
	}

	safe := NewRootCmd()
	safeOut, safeErr := &bytes.Buffer{}, &bytes.Buffer{}
	safe.SetOut(safeOut)
	safe.SetErr(safeErr)
	safe.SetArgs([]string{"job", "cleanup", "squ-46", "--repo", target, "--merged", "--json"})
	if err := safe.Execute(); err == nil {
		t.Fatalf("safe cleanup unexpectedly deleted unmerged branch: stdout=%s", safeOut.String())
	}
	if !branchExists(t, target, branch) {
		t.Fatalf("safe cleanup removed branch %s", branch)
	}
	stillOwned, err := job.Read(teamDir, "squ-46")
	if err != nil {
		t.Fatalf("read job after safe cleanup failure: %v", err)
	}
	if stillOwned.Branch != branch {
		t.Fatalf("safe cleanup mutated job = %+v", stillOwned)
	}

	force := NewRootCmd()
	forceOut, forceErr := &bytes.Buffer{}, &bytes.Buffer{}
	force.SetOut(forceOut)
	force.SetErr(forceErr)
	force.SetArgs([]string{"job", "cleanup", "squ-46", "--repo", target, "--merged", "--force-branch", "--json"})
	if err := force.Execute(); err != nil {
		t.Fatalf("force cleanup: %v\nstderr=%s", err, forceErr.String())
	}
	var cleaned job.Job
	if err := json.Unmarshal(forceOut.Bytes(), &cleaned); err != nil {
		t.Fatalf("decode force cleanup: %v\nbody=%s", err, forceOut.String())
	}
	if cleaned.Branch != "" || !strings.Contains(cleaned.LastStatus, "branch (force)") {
		t.Fatalf("cleaned job = %+v", cleaned)
	}
	if branchExists(t, target, branch) {
		t.Fatalf("branch %s still exists after force cleanup", branch)
	}
}

func TestJobCleanupVerifyPRAllowsForceDeleteAfterGitHubMerge(t *testing.T) {
	target := t.TempDir()
	initInto(t, target)
	initGitRepoForJobTest(t, target)
	installFakeGHForJobTest(t, `{"merged":true,"state":"MERGED","mergeCommit":{"oid":"abc123"}}`, 0)

	teamDir := filepath.Join(target, ".agent_team")
	branch := "worktree-worker-squ-47-verify"
	runGitForJobTest(t, target, "checkout", "-b", branch)
	if err := os.WriteFile(filepath.Join(target, "verified-feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForJobTest(t, target, "add", "verified-feature.txt")
	runGitForJobTest(t, target, "commit", "-m", "verified feature")
	runGitForJobTest(t, target, "checkout", "main")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-47",
		Ticket:    "SQU-47",
		Target:    "worker",
		Status:    job.StatusDone,
		Branch:    branch,
		PR:        "https://github.com/acme/repo/pull/47",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	preview := NewRootCmd()
	previewOut, previewErr := &bytes.Buffer{}, &bytes.Buffer{}
	preview.SetOut(previewOut)
	preview.SetErr(previewErr)
	preview.SetArgs([]string{"job", "cleanup", "squ-47", "--repo", target, "--dry-run", "--force-branch", "--verify-pr", "--json"})
	if err := preview.Execute(); err != nil {
		t.Fatalf("job cleanup verify preview: %v\nstderr=%s", err, previewErr.String())
	}
	var previewResult jobCleanupPreview
	if err := json.Unmarshal(previewOut.Bytes(), &previewResult); err != nil {
		t.Fatalf("decode cleanup verify preview: %v\nbody=%s", err, previewOut.String())
	}
	if !previewResult.VerifyPR || previewResult.PRVerification == nil || !previewResult.PRVerification.Verified || previewResult.PRVerification.State != "MERGED" {
		t.Fatalf("verify cleanup preview = %+v", previewResult)
	}

	force := NewRootCmd()
	forceOut, forceErr := &bytes.Buffer{}, &bytes.Buffer{}
	force.SetOut(forceOut)
	force.SetErr(forceErr)
	force.SetArgs([]string{"job", "cleanup", "squ-47", "--repo", target, "--merged", "--force-branch", "--verify-pr", "--json"})
	if err := force.Execute(); err != nil {
		t.Fatalf("force cleanup with pr verification: %v\nstderr=%s", err, forceErr.String())
	}
	var cleaned job.Job
	if err := json.Unmarshal(forceOut.Bytes(), &cleaned); err != nil {
		t.Fatalf("decode force cleanup with pr verification: %v\nbody=%s", err, forceOut.String())
	}
	if cleaned.Branch != "" || !strings.Contains(cleaned.LastStatus, "branch (force)") {
		t.Fatalf("cleaned job = %+v", cleaned)
	}
	if branchExists(t, target, branch) {
		t.Fatalf("branch %s still exists after verified force cleanup", branch)
	}
}

func TestJobCleanupVerifyPRRejectsOpenPullRequest(t *testing.T) {
	target := t.TempDir()
	initInto(t, target)
	initGitRepoForJobTest(t, target)
	installFakeGHForJobTest(t, `{"merged":false,"state":"OPEN"}`, 0)

	teamDir := filepath.Join(target, ".agent_team")
	branch := "worktree-worker-squ-48-open"
	runGitForJobTest(t, target, "checkout", "-b", branch)
	if err := os.WriteFile(filepath.Join(target, "open-feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForJobTest(t, target, "add", "open-feature.txt")
	runGitForJobTest(t, target, "commit", "-m", "open feature")
	runGitForJobTest(t, target, "checkout", "main")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-48",
		Ticket:    "SQU-48",
		Target:    "worker",
		Status:    job.StatusDone,
		Branch:    branch,
		PR:        "https://github.com/acme/repo/pull/48",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cleanup := NewRootCmd()
	cleanupOut, cleanupErr := &bytes.Buffer{}, &bytes.Buffer{}
	cleanup.SetOut(cleanupOut)
	cleanup.SetErr(cleanupErr)
	cleanup.SetArgs([]string{"job", "cleanup", "squ-48", "--repo", target, "--merged", "--force-branch", "--verify-pr", "--json"})
	err := cleanup.Execute()
	if err == nil {
		t.Fatalf("cleanup with open PR unexpectedly succeeded: stdout=%s", cleanupOut.String())
	}
	var code ExitCode
	if !errors.As(err, &code) || int(code) != 1 {
		t.Fatalf("cleanup err = %v, want exit 1", err)
	}
	if !strings.Contains(cleanupErr.String(), "PR is not merged") {
		t.Fatalf("cleanup stderr = %q", cleanupErr.String())
	}
	stillOwned, err := job.Read(teamDir, "squ-48")
	if err != nil {
		t.Fatalf("read job after verify failure: %v", err)
	}
	if stillOwned.Branch != branch {
		t.Fatalf("verify failure mutated job = %+v", stillOwned)
	}
	if !branchExists(t, target, branch) {
		t.Fatalf("cleanup removed branch %s despite open PR", branch)
	}
}

func TestJobCleanupAllPreviewsAndAppliesDoneOwnership(t *testing.T) {
	target := t.TempDir()
	initInto(t, target)
	initGitRepoForJobTest(t, target)
	teamDir := filepath.Join(target, ".agent_team")
	makeMergedBranch := func(branch string) {
		t.Helper()
		runGitForJobTest(t, target, "checkout", "-b", branch)
		runGitForJobTest(t, target, "checkout", "main")
	}
	branchA := "worktree-worker-squ-301"
	branchB := "worktree-worker-squ-302"
	branchQueued := "worktree-worker-squ-303"
	makeMergedBranch(branchA)
	makeMergedBranch(branchB)
	makeMergedBranch(branchQueued)
	now := time.Now().UTC()
	jobs := []*job.Job{
		{
			ID:        "squ-301",
			Ticket:    "SQU-301",
			Target:    "worker",
			Status:    job.StatusDone,
			Branch:    branchA,
			Worktree:  filepath.Join(target, ".claude", "worktrees", "worker-squ-301"),
			PR:        "https://github.com/acme/repo/pull/301",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "squ-302",
			Ticket:    "SQU-302",
			Target:    "worker",
			Status:    job.StatusDone,
			Branch:    branchB,
			PR:        "https://github.com/acme/repo/pull/302",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "squ-303",
			Ticket:    "SQU-303",
			Target:    "worker",
			Status:    job.StatusQueued,
			Branch:    branchQueued,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "squ-304",
			Ticket:    "SQU-304",
			Target:    "worker",
			Status:    job.StatusDone,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
	for _, j := range jobs {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}

	preview := NewRootCmd()
	previewOut, previewErr := &bytes.Buffer{}, &bytes.Buffer{}
	preview.SetOut(previewOut)
	preview.SetErr(previewErr)
	preview.SetArgs([]string{"job", "cleanup", "--all", "--repo", target, "--dry-run", "--json"})
	if err := preview.Execute(); err != nil {
		t.Fatalf("job cleanup --all dry-run: %v\nstderr=%s", err, previewErr.String())
	}
	var previewResult jobCleanupBatchResult
	if err := json.Unmarshal(previewOut.Bytes(), &previewResult); err != nil {
		t.Fatalf("decode cleanup --all dry-run json: %v\nbody=%s", err, previewOut.String())
	}
	if !previewResult.DryRun || previewResult.Total != 2 || previewResult.Previewed != 2 || previewResult.Failed != 0 || len(previewResult.Items) != 2 {
		t.Fatalf("cleanup --all preview = %+v", previewResult)
	}
	if previewResult.Items[0].JobID != "squ-301" || previewResult.Items[1].JobID != "squ-302" {
		t.Fatalf("cleanup --all preview items = %+v", previewResult.Items)
	}
	if previewResult.Items[0].Preview == nil || !previewResult.Items[0].Preview.BranchExists || previewResult.Items[0].Preview.WorktreeExists {
		t.Fatalf("cleanup --all preview item = %+v", previewResult.Items[0])
	}
	if !branchExists(t, target, branchA) || !branchExists(t, target, branchB) || !branchExists(t, target, branchQueued) {
		t.Fatalf("dry-run removed a branch")
	}

	previewFormat := NewRootCmd()
	previewFormatOut, previewFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	previewFormat.SetOut(previewFormatOut)
	previewFormat.SetErr(previewFormatErr)
	previewFormat.SetArgs([]string{"job", "cleanup", "--all", "--repo", target, "--dry-run", "--format", "{{.Total}} {{.Previewed}} {{.Failed}} {{len .Items}}"})
	if err := previewFormat.Execute(); err != nil {
		t.Fatalf("job cleanup --all dry-run format: %v\nstderr=%s", err, previewFormatErr.String())
	}
	if got, want := previewFormatOut.String(), "2 2 0 2\n"; got != want {
		t.Fatalf("job cleanup --all dry-run format = %q, want %q", got, want)
	}

	apply := NewRootCmd()
	applyOut, applyErr := &bytes.Buffer{}, &bytes.Buffer{}
	apply.SetOut(applyOut)
	apply.SetErr(applyErr)
	apply.SetArgs([]string{"job", "cleanup", "--all", "--repo", target, "--merged", "--json"})
	if err := apply.Execute(); err != nil {
		t.Fatalf("job cleanup --all apply: %v\nstderr=%s", err, applyErr.String())
	}
	var applied jobCleanupBatchResult
	if err := json.Unmarshal(applyOut.Bytes(), &applied); err != nil {
		t.Fatalf("decode cleanup --all apply json: %v\nbody=%s", err, applyOut.String())
	}
	if applied.DryRun || !applied.Merged || applied.Total != 2 || applied.Cleaned != 2 || applied.Failed != 0 || len(applied.Items) != 2 {
		t.Fatalf("cleanup --all applied = %+v", applied)
	}
	cleanedA, err := job.Read(teamDir, "squ-301")
	if err != nil {
		t.Fatalf("read cleaned A: %v", err)
	}
	cleanedB, err := job.Read(teamDir, "squ-302")
	if err != nil {
		t.Fatalf("read cleaned B: %v", err)
	}
	if cleanedA.Branch != "" || cleanedA.Worktree != "" || cleanedA.LastEvent != "cleanup" || cleanedB.Branch != "" || cleanedB.LastEvent != "cleanup" {
		t.Fatalf("cleaned jobs = %+v %+v", cleanedA, cleanedB)
	}
	if branchExists(t, target, branchA) || branchExists(t, target, branchB) {
		t.Fatalf("cleanup --all left cleaned branches behind")
	}
	if !branchExists(t, target, branchQueued) {
		t.Fatalf("cleanup --all removed queued branch %s", branchQueued)
	}
	queued, err := job.Read(teamDir, "squ-303")
	if err != nil {
		t.Fatalf("read queued: %v", err)
	}
	if queued.Branch != branchQueued {
		t.Fatalf("queued job was mutated = %+v", queued)
	}
	events, err := job.ListEvents(teamDir, "squ-301")
	if err != nil {
		t.Fatalf("list cleanup events: %v", err)
	}
	if len(events) != 1 || events[0].Type != "cleanup" {
		t.Fatalf("cleanup events = %+v", events)
	}

	empty := NewRootCmd()
	emptyOut, emptyErr := &bytes.Buffer{}, &bytes.Buffer{}
	empty.SetOut(emptyOut)
	empty.SetErr(emptyErr)
	empty.SetArgs([]string{"job", "cleanup", "--all", "--repo", target, "--dry-run"})
	if err := empty.Execute(); err != nil {
		t.Fatalf("job cleanup --all empty dry-run: %v\nstderr=%s", err, emptyErr.String())
	}
	if !strings.Contains(emptyOut.String(), "No cleanup-ready jobs.") {
		t.Fatalf("empty cleanup output = %q", emptyOut.String())
	}
}

func TestJobCleanupRejectsFormatCombinations(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "format with json",
			args: []string{"job", "cleanup", "squ-1", "--dry-run", "--format", "{{.JobID}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "invalid format",
			args: []string{"job", "cleanup", "squ-1", "--dry-run", "--format", "{{"},
			want: "invalid --format template",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("job cleanup validation succeeded: stdout=%s", out.String())
			}
			var code ExitCode
			if !errors.As(err, &code) || int(code) != 2 {
				t.Fatalf("job cleanup err = %v, want exit code 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestJobReconcileGitHubAdvancePreviewsPRGate(t *testing.T) {
	target := t.TempDir()
	initInto(t, target)
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[[instances.manager.triggers]]
event        = "agent.dispatch"
match.target = "manager"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
after = ["implement"]
gate = "pr"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	create := NewRootCmd()
	createOut, createErr := &bytes.Buffer{}, &bytes.Buffer{}
	create.SetOut(createOut)
	create.SetErr(createErr)
	create.SetArgs([]string{"pipeline", "run", "ticket_to_pr", "SQU-49", "pr gate reconcile", "--repo", target, "--json"})
	if err := create.Execute(); err != nil {
		t.Fatalf("pipeline run: %v\nstderr=%s", err, createErr.String())
	}
	step := NewRootCmd()
	stepOut, stepErr := &bytes.Buffer{}, &bytes.Buffer{}
	step.SetOut(stepOut)
	step.SetErr(stepErr)
	step.SetArgs([]string{"job", "step", "squ-49", "implement", "--status", "done", "--repo", target, "--json"})
	if err := step.Execute(); err != nil {
		t.Fatalf("mark implement done: %v\nstderr=%s", err, stepErr.String())
	}
	j, err := job.Read(teamDir, "squ-49")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	j.Branch = "worker-squ-49"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write branch: %v", err)
	}

	payload := `{"action":"opened","repository":{"full_name":"acme/repo"},"pull_request":{"number":49,"merged":false,"html_url":"https://github.com/acme/repo/pull/49","head":{"ref":"worker-squ-49"}}}`
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "reconcile", "github", "--payload", payload, "--advance", "--dry-run", "--repo", target, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job reconcile github advance dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var result struct {
		Result struct {
			Job job.Job `json:"job"`
		} `json:"result"`
		AdvancePreview *jobAdvancePreview `json:"advance_preview"`
		DryRun         bool               `json:"dry_run"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode reconcile advance: %v\nbody=%s", err, out.String())
	}
	if !result.DryRun || result.Result.Job.PR != "https://github.com/acme/repo/pull/49" {
		t.Fatalf("reconcile result = %+v", result)
	}
	if result.AdvancePreview == nil || result.AdvancePreview.Step == nil || result.AdvancePreview.Step.ID != "review" || result.AdvancePreview.Dispatch == nil || result.AdvancePreview.Dispatch.RequestedName != "manager-squ-49-review" {
		t.Fatalf("advance preview = %+v", result.AdvancePreview)
	}
	unchanged, err := job.Read(teamDir, "squ-49")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.PR != "" {
		t.Fatalf("dry-run wrote PR: %+v", unchanged)
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

func TestJobReconcileGitHubVerifyPRRequiresCleanupMerged(t *testing.T) {
	payload := `{"action":"closed","pull_request":{"number":1,"merged":true}}`
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "reconcile", "github", "--payload", payload, "--verify-pr", "--dry-run"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("job reconcile github --verify-pr succeeded without cleanup: stdout=%s", out.String())
	}
	var code ExitCode
	if !errors.As(err, &code) || int(code) != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if !strings.Contains(stderr.String(), "--verify-pr requires --cleanup-merged") {
		t.Fatalf("stderr = %q", stderr.String())
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
	if len(ready.Actions) != 1 || ready.Actions[0] != "agent-team job advance squ-203" {
		t.Fatalf("ready actions = %+v", ready.Actions)
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
	if len(done.Actions) != 0 {
		t.Fatalf("done actions = %+v, want none", done.Actions)
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

func TestJobStepMetadataAppearsInDiagnostics(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-204",
		Ticket:    "SQU-204",
		Target:    "manager",
		Kickoff:   "Implement SQU-204.",
		Pipeline:  "ticket_triage",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "triage", Label: "Triage", Target: "manager", Status: job.StatusDone, StartedAt: now, FinishedAt: now},
			{ID: "review", Label: "Code review", Description: "Review the worker branch before PR handoff.", Instructions: "Check tests and prepare PR handoff notes.", Target: "worker", Status: job.StatusBlocked, After: []string{"triage"}},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"job", "show", "squ-204", "--repo", tmp})
	if err := show.Execute(); err != nil {
		t.Fatalf("job show metadata: %v\nstderr=%s", err, showErr.String())
	}
	for _, want := range []string{`review  target=worker status=blocked instance=- after=triage label="Code review" description="Review the worker branch before PR handoff." instructions="Check tests and prepare PR handoff notes."`} {
		if !strings.Contains(showOut.String(), want) {
			t.Fatalf("job show missing %q:\n%s", want, showOut.String())
		}
	}

	explain := NewRootCmd()
	explainOut, explainErr := &bytes.Buffer{}, &bytes.Buffer{}
	explain.SetOut(explainOut)
	explain.SetErr(explainErr)
	explain.SetArgs([]string{"job", "explain", "squ-204", "--repo", tmp, "--json"})
	if err := explain.Execute(); err != nil {
		t.Fatalf("job explain metadata: %v\nstderr=%s", err, explainErr.String())
	}
	var explained jobExplainResult
	if err := json.Unmarshal(explainOut.Bytes(), &explained); err != nil {
		t.Fatalf("decode job explain: %v\nbody=%s", err, explainOut.String())
	}
	if explained.Next.StepID != "review" || explained.Next.Label != "Code review" || explained.Next.Description != "Review the worker branch before PR handoff." || explained.Next.Instructions != "Check tests and prepare PR handoff notes." {
		t.Fatalf("explain next = %+v", explained.Next)
	}
	if len(explained.Steps) != 2 || explained.Steps[1].Label != "Code review" || explained.Steps[1].Description != "Review the worker branch before PR handoff." || explained.Steps[1].Instructions != "Check tests and prepare PR handoff notes." {
		t.Fatalf("explain steps = %+v", explained.Steps)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	explainWatch := NewRootCmd()
	explainWatchOut, explainWatchErr := &bytes.Buffer{}, &bytes.Buffer{}
	explainWatch.SetContext(ctx)
	explainWatch.SetOut(explainWatchOut)
	explainWatch.SetErr(explainWatchErr)
	explainWatch.SetArgs([]string{"job", "explain", "squ-204", "--repo", tmp, "--watch", "--no-clear", "--interval", "1h", "--format", "{{.JobID}} {{.State}} {{len .Steps}}"})
	if err := explainWatch.Execute(); err != nil {
		t.Fatalf("job explain watch: %v\nstderr=%s", err, explainWatchErr.String())
	}
	if got := strings.TrimSpace(explainWatchOut.String()); got != "squ-204 ready 2" || strings.Contains(explainWatchOut.String(), watchClearSequence) {
		t.Fatalf("job explain watch output = %q", explainWatchOut.String())
	}

	explainInterval := NewRootCmd()
	explainIntervalOut, explainIntervalErr := &bytes.Buffer{}, &bytes.Buffer{}
	explainInterval.SetOut(explainIntervalOut)
	explainInterval.SetErr(explainIntervalErr)
	explainInterval.SetArgs([]string{"job", "explain", "squ-204", "--repo", tmp, "--watch", "--interval", "-1s"})
	if err := explainInterval.Execute(); err == nil {
		t.Fatalf("job explain negative interval succeeded")
	}
	if !strings.Contains(explainIntervalErr.String(), "--interval must be >= 0") {
		t.Fatalf("job explain negative interval stderr = %q", explainIntervalErr.String())
	}

	ready := NewRootCmd()
	readyOut, readyErr := &bytes.Buffer{}, &bytes.Buffer{}
	ready.SetOut(readyOut)
	ready.SetErr(readyErr)
	ready.SetArgs([]string{"job", "ready", "--repo", tmp, "--json"})
	if err := ready.Execute(); err != nil {
		t.Fatalf("job ready metadata: %v\nstderr=%s", err, readyErr.String())
	}
	var rows []jobReadyRow
	if err := json.Unmarshal(readyOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode ready rows: %v\nbody=%s", err, readyOut.String())
	}
	if len(rows) != 1 || rows[0].StepID != "review" || rows[0].Label != "Code review" || rows[0].Description != "Review the worker branch before PR handoff." || rows[0].Instructions != "Check tests and prepare PR handoff notes." {
		t.Fatalf("ready rows = %+v", rows)
	}

	advance := NewRootCmd()
	advanceOut, advanceErr := &bytes.Buffer{}, &bytes.Buffer{}
	advance.SetOut(advanceOut)
	advance.SetErr(advanceErr)
	advance.SetArgs([]string{"job", "advance", "squ-204", "--repo", tmp, "--dry-run", "--json"})
	if err := advance.Execute(); err != nil {
		t.Fatalf("job advance metadata dry-run: %v\nstderr=%s", err, advanceErr.String())
	}
	var preview jobAdvancePreview
	if err := json.Unmarshal(advanceOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode advance preview: %v\nbody=%s", err, advanceOut.String())
	}
	if preview.Dispatch == nil || preview.Dispatch.Preview == nil {
		t.Fatalf("advance preview missing dispatch: %+v", preview)
	}
	kickoff, _ := preview.Dispatch.Preview.Payload["kickoff"].(string)
	for _, want := range []string{"Implement SQU-204.", "--- pipeline step instructions (review) ---", "Check tests and prepare PR handoff notes."} {
		if !strings.Contains(kickoff, want) {
			t.Fatalf("advance kickoff missing %q in:\n%s", want, kickoff)
		}
	}
}

func TestJobOptionalFailedStepUnblocksDependents(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-231",
		Ticket:    "SQU-231",
		Target:    "manager",
		Kickoff:   "SQU-231: optional validation",
		Pipeline:  "ticket_triage",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "precheck", Target: "manager", Status: job.StatusRunning, Instance: "manager", Optional: true, StartedAt: now},
			{ID: "review", Target: "worker", Status: job.StatusBlocked, After: []string{"precheck"}},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	stepCmd := NewRootCmd()
	stepOut, stepErr := &bytes.Buffer{}, &bytes.Buffer{}
	stepCmd.SetOut(stepOut)
	stepCmd.SetErr(stepErr)
	stepCmd.SetArgs([]string{"job", "step", "squ-231", "precheck", "--status", "failed", "--message", "lint service unavailable", "--repo", tmp, "--json"})
	if err := stepCmd.Execute(); err != nil {
		t.Fatalf("job step optional failed: %v\nstderr=%s", err, stepErr.String())
	}
	var stepResult job.Job
	if err := json.Unmarshal(stepOut.Bytes(), &stepResult); err != nil {
		t.Fatalf("decode step json: %v\nbody=%s", err, stepOut.String())
	}
	if stepResult.Status != job.StatusRunning || stepResult.Steps[0].Status != job.StatusFailed || !stepResult.Steps[0].Optional {
		t.Fatalf("step result = %+v", stepResult)
	}

	nextCmd := NewRootCmd()
	nextOut, nextErr := &bytes.Buffer{}, &bytes.Buffer{}
	nextCmd.SetOut(nextOut)
	nextCmd.SetErr(nextErr)
	nextCmd.SetArgs([]string{"job", "next", "squ-231", "--repo", tmp, "--json"})
	if err := nextCmd.Execute(); err != nil {
		t.Fatalf("job next optional failed: %v\nstderr=%s", err, nextErr.String())
	}
	var next jobNextResult
	if err := json.Unmarshal(nextOut.Bytes(), &next); err != nil {
		t.Fatalf("decode next json: %v\nbody=%s", err, nextOut.String())
	}
	if next.State != "ready" || next.Step == nil || next.Step.ID != "review" || len(next.WaitingFor) != 0 {
		t.Fatalf("next = %+v", next)
	}

	explainCmd := NewRootCmd()
	explainOut, explainErr := &bytes.Buffer{}, &bytes.Buffer{}
	explainCmd.SetOut(explainOut)
	explainCmd.SetErr(explainErr)
	explainCmd.SetArgs([]string{"job", "explain", "squ-231", "--repo", tmp, "--json"})
	if err := explainCmd.Execute(); err != nil {
		t.Fatalf("job explain optional failed: %v\nstderr=%s", err, explainErr.String())
	}
	var explained jobExplainResult
	if err := json.Unmarshal(explainOut.Bytes(), &explained); err != nil {
		t.Fatalf("decode explain json: %v\nbody=%s", err, explainOut.String())
	}
	if len(explained.Steps) != 2 || !explained.Steps[0].Optional || explained.Steps[0].Message != "optional failed" || !explained.Steps[1].Ready {
		t.Fatalf("explained = %+v", explained)
	}

	gated := &job.Job{
		ID:        "squ-232",
		Ticket:    "SQU-232",
		Target:    "manager",
		Kickoff:   "SQU-232: optional validation",
		Pipeline:  "ticket_triage",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "precheck", Target: "manager", Status: job.StatusFailed, Optional: true, StartedAt: now, FinishedAt: now},
			{ID: "approval", Target: "manager", Status: job.StatusBlocked, After: []string{"precheck"}, Gate: job.StepGateManual},
		},
	}
	if err := job.Write(teamDir, gated); err != nil {
		t.Fatalf("write gated job: %v", err)
	}
	gatedCmd := NewRootCmd()
	gatedOut, gatedErr := &bytes.Buffer{}, &bytes.Buffer{}
	gatedCmd.SetOut(gatedOut)
	gatedCmd.SetErr(gatedErr)
	gatedCmd.SetArgs([]string{"job", "next", "squ-232", "--repo", tmp, "--json"})
	if err := gatedCmd.Execute(); err != nil {
		t.Fatalf("job next gated optional failed: %v\nstderr=%s", err, gatedErr.String())
	}
	var gatedNext jobNextResult
	if err := json.Unmarshal(gatedOut.Bytes(), &gatedNext); err != nil {
		t.Fatalf("decode gated next json: %v\nbody=%s", err, gatedOut.String())
	}
	if gatedNext.State != "blocked" || gatedNext.Step == nil || gatedNext.Step.ID != "approval" {
		t.Fatalf("gated next = %+v", gatedNext)
	}

	doneCmd := NewRootCmd()
	doneOut, doneErr := &bytes.Buffer{}, &bytes.Buffer{}
	doneCmd.SetOut(doneOut)
	doneCmd.SetErr(doneErr)
	doneCmd.SetArgs([]string{"job", "step", "squ-231", "review", "--status", "done", "--repo", tmp, "--json"})
	if err := doneCmd.Execute(); err != nil {
		t.Fatalf("job step review done: %v\nstderr=%s", err, doneErr.String())
	}
	var doneJob job.Job
	if err := json.Unmarshal(doneOut.Bytes(), &doneJob); err != nil {
		t.Fatalf("decode done json: %v\nbody=%s", err, doneOut.String())
	}
	if doneJob.Status != job.StatusDone || doneJob.LastEvent != "pipeline_done" || doneJob.LastStatus != "all required steps done" {
		t.Fatalf("done job = %+v", doneJob)
	}
}

func TestJobHoldReleaseStopsReadiness(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-240",
		Ticket:    "SQU-240",
		Target:    "manager",
		Kickoff:   "SQU-240: held pipeline",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusDone, StartedAt: now, FinishedAt: now},
			{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	holdUntil := now.Truncate(time.Second).Add(time.Hour)

	hold := NewRootCmd()
	holdOut, holdErr := &bytes.Buffer{}, &bytes.Buffer{}
	hold.SetOut(holdOut)
	hold.SetErr(holdErr)
	holdMessageFile := filepath.Join(tmp, "hold-message.txt")
	if err := os.WriteFile(holdMessageFile, []byte("waiting for user from file\n"), 0o644); err != nil {
		t.Fatalf("write hold message: %v", err)
	}
	hold.SetArgs([]string{"job", "hold", "squ-240", "--repo", tmp, "--message-file", holdMessageFile, "--until", holdUntil.Format(time.RFC3339), "--json"})
	if err := hold.Execute(); err != nil {
		t.Fatalf("job hold: %v\nstderr=%s", err, holdErr.String())
	}
	var held job.Job
	if err := json.Unmarshal(holdOut.Bytes(), &held); err != nil {
		t.Fatalf("decode hold json: %v\nbody=%s", err, holdOut.String())
	}
	if !held.Held || held.HoldReason != "waiting for user from file" || !held.HoldUntil.Equal(holdUntil) || held.LastEvent != "held" || held.Status != job.StatusRunning {
		t.Fatalf("held job = %+v", held)
	}

	nextCmd := NewRootCmd()
	nextOut, nextErr := &bytes.Buffer{}, &bytes.Buffer{}
	nextCmd.SetOut(nextOut)
	nextCmd.SetErr(nextErr)
	nextCmd.SetArgs([]string{"job", "next", "squ-240", "--repo", tmp, "--json"})
	if err := nextCmd.Execute(); err != nil {
		t.Fatalf("job next held: %v\nstderr=%s", err, nextErr.String())
	}
	var next jobNextResult
	if err := json.Unmarshal(nextOut.Bytes(), &next); err != nil {
		t.Fatalf("decode next json: %v\nbody=%s", err, nextOut.String())
	}
	if next.State != "held" || next.Step != nil || !strings.Contains(next.Message, "waiting for user from file") || !containsString(next.Actions, "agent-team job release squ-240") {
		t.Fatalf("held next = %+v", next)
	}

	readyDefault := NewRootCmd()
	readyDefaultOut, readyDefaultErr := &bytes.Buffer{}, &bytes.Buffer{}
	readyDefault.SetOut(readyDefaultOut)
	readyDefault.SetErr(readyDefaultErr)
	readyDefault.SetArgs([]string{"job", "ready", "--repo", tmp, "--json"})
	if err := readyDefault.Execute(); err != nil {
		t.Fatalf("job ready default: %v\nstderr=%s", err, readyDefaultErr.String())
	}
	var defaultRows []jobReadyRow
	if err := json.Unmarshal(readyDefaultOut.Bytes(), &defaultRows); err != nil {
		t.Fatalf("decode default ready json: %v\nbody=%s", err, readyDefaultOut.String())
	}
	for _, row := range defaultRows {
		if row.JobID == "squ-240" {
			t.Fatalf("held job appeared in default ready rows: %+v", defaultRows)
		}
	}

	readyHeld := NewRootCmd()
	readyHeldOut, readyHeldErr := &bytes.Buffer{}, &bytes.Buffer{}
	readyHeld.SetOut(readyHeldOut)
	readyHeld.SetErr(readyHeldErr)
	readyHeld.SetArgs([]string{"job", "ready", "--repo", tmp, "--state", "held", "--json"})
	if err := readyHeld.Execute(); err != nil {
		t.Fatalf("job ready held: %v\nstderr=%s", err, readyHeldErr.String())
	}
	var heldRows []jobReadyRow
	if err := json.Unmarshal(readyHeldOut.Bytes(), &heldRows); err != nil {
		t.Fatalf("decode held ready json: %v\nbody=%s", err, readyHeldOut.String())
	}
	if len(heldRows) != 1 || heldRows[0].JobID != "squ-240" || heldRows[0].State != "held" || !containsString(heldRows[0].Actions, "agent-team job release squ-240") {
		t.Fatalf("held ready rows = %+v", heldRows)
	}

	advance := NewRootCmd()
	advanceOut, advanceErr := &bytes.Buffer{}, &bytes.Buffer{}
	advance.SetOut(advanceOut)
	advance.SetErr(advanceErr)
	advance.SetArgs([]string{"job", "advance", "squ-240", "--repo", tmp, "--dry-run", "--json"})
	if err := advance.Execute(); err != nil {
		t.Fatalf("job advance held dry-run: %v\nstderr=%s", err, advanceErr.String())
	}
	var advancePreview jobAdvancePreview
	if err := json.Unmarshal(advanceOut.Bytes(), &advancePreview); err != nil {
		t.Fatalf("decode advance preview: %v\nbody=%s", err, advanceOut.String())
	}
	if !strings.Contains(advancePreview.Message, "waiting for user from file") || advancePreview.Step != nil || advancePreview.Dispatch != nil {
		t.Fatalf("advance preview = %+v", advancePreview)
	}

	status := NewRootCmd()
	statusOut, statusErr := &bytes.Buffer{}, &bytes.Buffer{}
	status.SetOut(statusOut)
	status.SetErr(statusErr)
	status.SetArgs([]string{"pipeline", "status", "ticket_to_pr", "--repo", tmp, "--json"})
	if err := status.Execute(); err != nil {
		t.Fatalf("pipeline status held: %v\nstderr=%s", err, statusErr.String())
	}
	var statusRows []pipelineStatusRow
	if err := json.Unmarshal(statusOut.Bytes(), &statusRows); err != nil {
		t.Fatalf("decode pipeline status: %v\nbody=%s", err, statusOut.String())
	}
	if len(statusRows) != 1 || statusRows[0].HeldSteps != 1 ||
		!containsString(statusRows[0].Actions, "agent-team pipeline explain ticket_to_pr --state held") ||
		!containsString(statusRows[0].Actions, "agent-team pipeline ready ticket_to_pr --state held") {
		t.Fatalf("pipeline status rows = %+v", statusRows)
	}

	explain := NewRootCmd()
	explainOut, explainErr := &bytes.Buffer{}, &bytes.Buffer{}
	explain.SetOut(explainOut)
	explain.SetErr(explainErr)
	explain.SetArgs([]string{"pipeline", "explain", "ticket_to_pr", "--repo", tmp, "--state", "held", "--json"})
	if err := explain.Execute(); err != nil {
		t.Fatalf("pipeline explain held: %v\nstderr=%s", err, explainErr.String())
	}
	var explainRows []pipelineExplainRow
	if err := json.Unmarshal(explainOut.Bytes(), &explainRows); err != nil {
		t.Fatalf("decode pipeline explain: %v\nbody=%s", err, explainOut.String())
	}
	if len(explainRows) != 1 || explainRows[0].ExplainedJobs != 1 || len(explainRows[0].Jobs) != 1 || explainRows[0].Jobs[0].State != "held" {
		t.Fatalf("pipeline explain rows = %+v", explainRows)
	}

	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"job", "show", "squ-240", "--repo", tmp})
	if err := show.Execute(); err != nil {
		t.Fatalf("job show held: %v\nstderr=%s", err, showErr.String())
	}
	if !strings.Contains(showOut.String(), "Held:        yes") ||
		!strings.Contains(showOut.String(), "Hold Reason: waiting for user from file") ||
		!strings.Contains(showOut.String(), "Hold Until:  "+holdUntil.Format(time.RFC3339)) ||
		!strings.Contains(showOut.String(), "agent-team job release squ-240") ||
		strings.Contains(showOut.String(), "agent-team job advance squ-240") {
		t.Fatalf("job show held output:\n%s", showOut.String())
	}

	expired := &job.Job{
		ID:         "squ-241",
		Ticket:     "SQU-241",
		Target:     "manager",
		Kickoff:    "SQU-241: expired hold",
		Pipeline:   "ticket_to_pr",
		Status:     job.StatusRunning,
		Held:       true,
		HoldReason: "past deadline",
		HoldUntil:  now.Truncate(time.Second).Add(-time.Hour),
		CreatedAt:  now,
		UpdatedAt:  now,
		Steps:      []job.Step{{ID: "review", Target: "manager", Status: job.StatusBlocked}},
	}
	if err := job.Write(teamDir, expired); err != nil {
		t.Fatalf("write expired job: %v", err)
	}

	activeList := NewRootCmd()
	activeListOut, activeListErr := &bytes.Buffer{}, &bytes.Buffer{}
	activeList.SetOut(activeListOut)
	activeList.SetErr(activeListErr)
	activeList.SetArgs([]string{"job", "ls", "--repo", tmp, "--active-hold", "--json"})
	if err := activeList.Execute(); err != nil {
		t.Fatalf("job ls active hold: %v\nstderr=%s", err, activeListErr.String())
	}
	var activeJobs []job.Job
	if err := json.Unmarshal(activeListOut.Bytes(), &activeJobs); err != nil {
		t.Fatalf("decode active holds: %v\nbody=%s", err, activeListOut.String())
	}
	if len(activeJobs) != 1 || activeJobs[0].ID != "squ-240" {
		t.Fatalf("active holds = %+v", activeJobs)
	}

	expiredList := NewRootCmd()
	expiredListOut, expiredListErr := &bytes.Buffer{}, &bytes.Buffer{}
	expiredList.SetOut(expiredListOut)
	expiredList.SetErr(expiredListErr)
	expiredList.SetArgs([]string{"pipeline", "jobs", "ticket_to_pr", "--repo", tmp, "--expired-hold", "--json"})
	if err := expiredList.Execute(); err != nil {
		t.Fatalf("pipeline jobs expired hold: %v\nstderr=%s", err, expiredListErr.String())
	}
	var expiredJobs []job.Job
	if err := json.Unmarshal(expiredListOut.Bytes(), &expiredJobs); err != nil {
		t.Fatalf("decode expired holds: %v\nbody=%s", err, expiredListOut.String())
	}
	if len(expiredJobs) != 1 || expiredJobs[0].ID != "squ-241" {
		t.Fatalf("expired holds = %+v", expiredJobs)
	}

	summaryCmd := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summaryCmd.SetOut(summaryOut)
	summaryCmd.SetErr(summaryErr)
	summaryCmd.SetArgs([]string{"job", "ls", "--repo", tmp, "--summary", "--json"})
	if err := summaryCmd.Execute(); err != nil {
		t.Fatalf("job summary held deadlines: %v\nstderr=%s", err, summaryErr.String())
	}
	var summary jobSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &summary); err != nil {
		t.Fatalf("decode held summary: %v\nbody=%s", err, summaryOut.String())
	}
	if summary.Held != 2 || summary.ExpiredHeld != 1 {
		t.Fatalf("held summary = %+v", summary)
	}

	triageExpired := NewRootCmd()
	triageExpiredOut, triageExpiredErr := &bytes.Buffer{}, &bytes.Buffer{}
	triageExpired.SetOut(triageExpiredOut)
	triageExpired.SetErr(triageExpiredErr)
	triageExpired.SetArgs([]string{"job", "triage", "--repo", tmp, "--reason", "expired_hold", "--json"})
	if err := triageExpired.Execute(); err != nil {
		t.Fatalf("job triage expired hold: %v\nstderr=%s", err, triageExpiredErr.String())
	}
	var expiredTriage jobTriageSnapshot
	if err := json.Unmarshal(triageExpiredOut.Bytes(), &expiredTriage); err != nil {
		t.Fatalf("decode expired hold triage: %v\nbody=%s", err, triageExpiredOut.String())
	}
	if len(expiredTriage.Attention) != 1 ||
		expiredTriage.Attention[0].JobID != "squ-241" ||
		!containsString(expiredTriage.Attention[0].Reasons, "expired_hold") ||
		!containsString(expiredTriage.Attention[0].Actions, "agent-team job release squ-241") ||
		!strings.Contains(expiredTriage.Attention[0].Message, "expired") {
		t.Fatalf("expired hold triage = %+v", expiredTriage.Attention)
	}

	releaseExpired := NewRootCmd()
	releaseExpiredOut, releaseExpiredErr := &bytes.Buffer{}, &bytes.Buffer{}
	releaseExpired.SetOut(releaseExpiredOut)
	releaseExpired.SetErr(releaseExpiredErr)
	releaseExpired.SetArgs([]string{"job", "release", "--all", "--expired", "--repo", tmp, "--json"})
	if err := releaseExpired.Execute(); err != nil {
		t.Fatalf("job release expired holds: %v\nstderr=%s", err, releaseExpiredErr.String())
	}
	var releaseExpiredRows []pipelineHoldResult
	if err := json.Unmarshal(releaseExpiredOut.Bytes(), &releaseExpiredRows); err != nil {
		t.Fatalf("decode release expired holds: %v\nbody=%s", err, releaseExpiredOut.String())
	}
	if len(releaseExpiredRows) != 1 || releaseExpiredRows[0].JobID != "squ-241" || releaseExpiredRows[0].Action != "released" || releaseExpiredRows[0].HoldUntil == "" {
		t.Fatalf("release expired holds = %+v", releaseExpiredRows)
	}
	releasedExpired, err := job.Read(teamDir, "squ-241")
	if err != nil {
		t.Fatalf("read released expired job: %v", err)
	}
	if releasedExpired.Held || !releasedExpired.HoldUntil.IsZero() {
		t.Fatalf("released expired job = %+v", releasedExpired)
	}

	release := NewRootCmd()
	releaseOut, releaseErr := &bytes.Buffer{}, &bytes.Buffer{}
	release.SetOut(releaseOut)
	release.SetErr(releaseErr)
	releaseMessageFile := filepath.Join(tmp, "release-message.txt")
	if err := os.WriteFile(releaseMessageFile, []byte("resume from file\n"), 0o644); err != nil {
		t.Fatalf("write release message: %v", err)
	}
	release.SetArgs([]string{"job", "release", "squ-240", "--repo", tmp, "--message-file", releaseMessageFile, "--json"})
	if err := release.Execute(); err != nil {
		t.Fatalf("job release: %v\nstderr=%s", err, releaseErr.String())
	}
	var released job.Job
	if err := json.Unmarshal(releaseOut.Bytes(), &released); err != nil {
		t.Fatalf("decode release json: %v\nbody=%s", err, releaseOut.String())
	}
	if released.Held || released.HoldReason != "" || !released.HoldUntil.IsZero() || released.LastEvent != "released" || released.LastStatus != "resume from file" {
		t.Fatalf("released job = %+v", released)
	}

	nextAfter := NewRootCmd()
	nextAfterOut, nextAfterErr := &bytes.Buffer{}, &bytes.Buffer{}
	nextAfter.SetOut(nextAfterOut)
	nextAfter.SetErr(nextAfterErr)
	nextAfter.SetArgs([]string{"job", "next", "squ-240", "--repo", tmp, "--json"})
	if err := nextAfter.Execute(); err != nil {
		t.Fatalf("job next released: %v\nstderr=%s", err, nextAfterErr.String())
	}
	var releasedNext jobNextResult
	if err := json.Unmarshal(nextAfterOut.Bytes(), &releasedNext); err != nil {
		t.Fatalf("decode released next: %v\nbody=%s", err, nextAfterOut.String())
	}
	if releasedNext.State != "ready" || releasedNext.Step == nil || releasedNext.Step.ID != "review" {
		t.Fatalf("released next = %+v", releasedNext)
	}
}

func TestJobHoldAllMatchesActiveJobs(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC().Truncate(time.Second)
	for _, j := range []*job.Job{
		{
			ID:        "squ-250",
			Ticket:    "SQU-250",
			Target:    "manager",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "squ-251",
			Ticket:    "SQU-251",
			Target:    "manager",
			Status:    job.StatusDone,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:         "squ-252",
			Ticket:     "SQU-252",
			Target:     "manager",
			Status:     job.StatusQueued,
			Held:       true,
			HoldReason: "already paused",
			CreatedAt:  now,
			UpdatedAt:  now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	hold := NewRootCmd()
	holdOut, holdErr := &bytes.Buffer{}, &bytes.Buffer{}
	hold.SetOut(holdOut)
	hold.SetErr(holdErr)
	hold.SetArgs([]string{"job", "hold", "--all", "--repo", tmp, "--for", "1h", "--message", "incident freeze", "--json"})
	if err := hold.Execute(); err != nil {
		t.Fatalf("job hold all: %v\nstderr=%s", err, holdErr.String())
	}
	var rows []pipelineHoldResult
	if err := json.Unmarshal(holdOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode hold all rows: %v\nbody=%s", err, holdOut.String())
	}
	if len(rows) != 1 || rows[0].JobID != "squ-250" || rows[0].Action != "held" || rows[0].HoldUntil == "" {
		t.Fatalf("hold all rows = %+v", rows)
	}
	held, err := job.Read(teamDir, "squ-250")
	if err != nil {
		t.Fatalf("read held job: %v", err)
	}
	if !held.Held || held.HoldReason != "incident freeze" || held.HoldUntil.IsZero() {
		t.Fatalf("held job = %+v", held)
	}
	done, err := job.Read(teamDir, "squ-251")
	if err != nil {
		t.Fatalf("read done job: %v", err)
	}
	if done.Held {
		t.Fatalf("done job should not be held by default: %+v", done)
	}
	already, err := job.Read(teamDir, "squ-252")
	if err != nil {
		t.Fatalf("read already held job: %v", err)
	}
	if !already.Held || already.HoldReason != "already paused" {
		t.Fatalf("already held job changed unexpectedly: %+v", already)
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
	cmd.SetArgs([]string{"job", "ready", "--repo", tmp, "--pipeline", "ticket_to_pr", "--state", "all", "--sort", "updated", "--format", "{{.JobID}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job ready sort updated: %v\nstderr=%s", err, stderr.String())
	}
	if got := strings.Split(strings.TrimSpace(out.String()), "\n"); strings.Join(got, ",") != "squ-211,squ-210" {
		t.Fatalf("sorted ready rows = %q", out.String())
	}

	cmd = NewRootCmd()
	out, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "ready", "--repo", tmp, "--pipeline", "ticket_to_pr", "--state", "all", "--sort", "updated", "--limit", "1", "--format", "{{.JobID}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job ready limit: %v\nstderr=%s", err, stderr.String())
	}
	if got := strings.TrimSpace(out.String()); got != "squ-211" {
		t.Fatalf("limited ready rows = %q", out.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd = NewRootCmd()
	out, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetContext(ctx)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "ready", "--repo", tmp, "--pipeline", "ticket_to_pr", "--state", "all", "--sort", "updated", "--limit", "1", "--watch", "--no-clear", "--interval", "1ms", "--format", "{{.JobID}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job ready watch: %v\nstderr=%s", err, stderr.String())
	}
	if got := strings.TrimSpace(out.String()); got != "squ-211" || strings.Contains(out.String(), watchClearSequence) {
		t.Fatalf("watched ready rows = %q", out.String())
	}

	cmd = NewRootCmd()
	out, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "ready", "--repo", tmp, "--pipeline", "ticket_to_pr", "--state", "all", "--step", "implement", "--format", "{{.JobID}} {{.State}} {{.StepID}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job ready step filter: %v\nstderr=%s", err, stderr.String())
	}
	if got := strings.TrimSpace(out.String()); got != "squ-211 running implement" {
		t.Fatalf("step-filtered ready rows = %q", out.String())
	}

	cmd = NewRootCmd()
	out, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "ready", "--repo", tmp, "--sort", "priority"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("job ready invalid sort succeeded")
	}
	if !strings.Contains(stderr.String(), "--sort must be job") {
		t.Fatalf("missing sort error:\n%s", stderr.String())
	}

	cmd = NewRootCmd()
	out, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "ready", "--repo", tmp, "--limit", "-1"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("job ready negative limit succeeded")
	}
	if !strings.Contains(stderr.String(), "--limit must be >= 0") {
		t.Fatalf("missing limit error:\n%s", stderr.String())
	}

	cmd = NewRootCmd()
	out, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "ready", "--repo", tmp, "--watch", "--interval", "-1s"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("job ready negative interval succeeded")
	}
	if !strings.Contains(stderr.String(), "--interval must be >= 0") {
		t.Fatalf("missing interval error:\n%s", stderr.String())
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

func TestJobPipelineControlRejectsFormatCombinations(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "advance format with json",
			args: []string{"job", "advance", "squ-1", "--format", "{{.Job.ID}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "advance invalid format",
			args: []string{"job", "advance", "squ-1", "--format", "{{"},
			want: "invalid --format template",
		},
		{
			name: "step format with json",
			args: []string{"job", "step", "squ-1", "implement", "--format", "{{.ID}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "step invalid format",
			args: []string{"job", "step", "squ-1", "implement", "--format", "{{"},
			want: "invalid --format template",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("job pipeline control validation succeeded: stdout=%s", out.String())
			}
			var code ExitCode
			if !errors.As(err, &code) || int(code) != 2 {
				t.Fatalf("err = %v, want exit 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
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

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"job", "advance", "squ-201", "--repo", target, "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("job advance dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview jobAdvancePreview
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode advance dry-run json: %v\nbody=%s", err, dryOut.String())
	}
	if !preview.DryRun || preview.Job == nil || preview.Job.ID != "squ-201" || preview.Step == nil || preview.Step.ID != "review" {
		t.Fatalf("preview = %+v", preview)
	}
	if preview.Dispatch == nil || preview.Dispatch.RequestedName != "manager-squ-201-review" {
		t.Fatalf("dispatch preview = %+v", preview.Dispatch)
	}
	payload := preview.Dispatch.Preview.Payload
	if payload["pipeline"] != "ticket_triage" || payload["pipeline_step"] != "review" || payload["job_id"] != "squ-201" {
		t.Fatalf("payload = %+v", payload)
	}
	unchanged, err := job.Read(teamDir, "squ-201")
	if err != nil {
		t.Fatalf("read dry-run job: %v", err)
	}
	if unchanged.Steps[1].Status != job.StatusBlocked || unchanged.LastEvent != "" {
		t.Fatalf("dry-run mutated job = %+v", unchanged)
	}
	dryMessages, err := daemon.ReadMessages(daemon.DaemonRoot(teamDir), "manager")
	if err != nil {
		t.Fatalf("read dry-run messages: %v", err)
	}
	if len(dryMessages) != 0 {
		t.Fatalf("dry-run sent messages = %+v", dryMessages)
	}

	dryFormat := NewRootCmd()
	dryFormatOut, dryFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	dryFormat.SetOut(dryFormatOut)
	dryFormat.SetErr(dryFormatErr)
	dryFormat.SetArgs([]string{"job", "advance", "squ-201", "--repo", target, "--dry-run", "--format", "{{.Job.ID}} {{.Step.ID}} {{.Dispatch.RequestedName}} {{.DryRun}}"})
	if err := dryFormat.Execute(); err != nil {
		t.Fatalf("job advance dry-run format: %v\nstderr=%s", err, dryFormatErr.String())
	}
	if got := dryFormatOut.String(); got != "squ-201 review manager-squ-201-review true\n" {
		t.Fatalf("job advance dry-run format = %q", got)
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
	if result.Step == nil || result.Step.ID != "review" || result.Step.Status != job.StatusQueued || result.Step.Instance != "manager" {
		t.Fatalf("result = %+v", result)
	}
	updated, err := job.Read(teamDir, "squ-201")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.Steps[1].Status != job.StatusQueued || updated.LastEvent != "advance_queued" {
		t.Fatalf("updated = %+v", updated)
	}
	messages, err := daemon.ReadMessages(daemon.DaemonRoot(teamDir), "manager")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0].Body, `"pipeline_step":"review"`) {
		t.Fatalf("messages = %+v", messages)
	}

	formattedJob := &job.Job{
		ID:        "squ-201-format",
		Ticket:    "SQU-201-FORMAT",
		Target:    "manager",
		Kickoff:   "SQU-201-FORMAT: review implementation",
		Pipeline:  "ticket_triage",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "triage", Target: "manager", Status: job.StatusDone, Instance: "manager", StartedAt: now, FinishedAt: now},
			{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"triage"}},
		},
	}
	if err := job.Write(teamDir, formattedJob); err != nil {
		t.Fatalf("write formatted job: %v", err)
	}
	formatCmd := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatCmd.SetOut(formatOut)
	formatCmd.SetErr(formatErr)
	formatCmd.SetArgs([]string{"job", "advance", "squ-201-format", "--repo", target, "--format", "{{.Job.ID}} {{.Step.ID}} {{.Step.Status}} {{.Step.Instance}}"})
	if err := formatCmd.Execute(); err != nil {
		t.Fatalf("job advance format: %v\nstderr=%s", err, formatErr.String())
	}
	if got := formatOut.String(); got != "squ-201-format review queued manager\n" {
		t.Fatalf("job advance format = %q", got)
	}
	formattedUpdated, err := job.Read(teamDir, "squ-201-format")
	if err != nil {
		t.Fatalf("read formatted job: %v", err)
	}
	if formattedUpdated.Steps[1].Status != job.StatusQueued || formattedUpdated.LastEvent != "advance_queued" {
		t.Fatalf("formatted updated job = %+v", formattedUpdated)
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

	stepDry := NewRootCmd()
	stepDryOut, stepDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	stepDry.SetOut(stepDryOut)
	stepDry.SetErr(stepDryErr)
	stepDry.SetArgs([]string{"job", "step", "squ-202", "triage", "--status", "blocked", "--message", "needs review notes", "--repo", target, "--dry-run", "--json"})
	if err := stepDry.Execute(); err != nil {
		t.Fatalf("job step dry-run: %v\nstderr=%s", err, stepDryErr.String())
	}
	var stepPreview jobStepPreview
	if err := json.Unmarshal(stepDryOut.Bytes(), &stepPreview); err != nil {
		t.Fatalf("decode step dry-run json: %v\nbody=%s", err, stepDryOut.String())
	}
	if !stepPreview.DryRun || stepPreview.Job == nil || stepPreview.Job.Steps[0].Status != job.StatusBlocked || stepPreview.Job.LastStatus != "needs review notes" {
		t.Fatalf("step preview = %+v", stepPreview)
	}
	unchanged, err := job.Read(teamDir, "squ-202")
	if err != nil {
		t.Fatalf("read dry-run job: %v", err)
	}
	if unchanged.Steps[0].Status != job.StatusRunning || unchanged.LastStatus != "" {
		t.Fatalf("step dry-run mutated job = %+v", unchanged)
	}

	stepDryFormat := NewRootCmd()
	stepDryFormatOut, stepDryFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	stepDryFormat.SetOut(stepDryFormatOut)
	stepDryFormat.SetErr(stepDryFormatErr)
	stepDryFormat.SetArgs([]string{"job", "step", "squ-202", "triage", "--status", "blocked", "--message", "needs review notes", "--repo", target, "--dry-run", "--format", "{{.ID}} {{.Status}} {{.LastStatus}}"})
	if err := stepDryFormat.Execute(); err != nil {
		t.Fatalf("job step dry-run format: %v\nstderr=%s", err, stepDryFormatErr.String())
	}
	if got := stepDryFormatOut.String(); got != "squ-202 blocked needs review notes\n" {
		t.Fatalf("job step dry-run format = %q", got)
	}

	advanceDry := NewRootCmd()
	advanceDryOut, advanceDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	advanceDry.SetOut(advanceDryOut)
	advanceDry.SetErr(advanceDryErr)
	advanceDry.SetArgs([]string{"job", "step", "squ-202", "triage", "--status", "done", "--advance", "--repo", target, "--dry-run", "--json"})
	if err := advanceDry.Execute(); err != nil {
		t.Fatalf("job step --advance dry-run: %v\nstderr=%s", err, advanceDryErr.String())
	}
	var advancePreview jobAdvancePreview
	if err := json.Unmarshal(advanceDryOut.Bytes(), &advancePreview); err != nil {
		t.Fatalf("decode step advance dry-run json: %v\nbody=%s", err, advanceDryOut.String())
	}
	if !advancePreview.DryRun || advancePreview.Job == nil || advancePreview.Job.Steps[0].Status != job.StatusDone || advancePreview.Step == nil || advancePreview.Step.ID != "review" {
		t.Fatalf("advance preview = %+v", advancePreview)
	}
	if advancePreview.Dispatch == nil || advancePreview.Dispatch.RequestedName != "manager-squ-202-review" {
		t.Fatalf("dispatch preview = %+v", advancePreview.Dispatch)
	}
	payload := advancePreview.Dispatch.Preview.Payload
	if payload["pipeline"] != "ticket_triage" || payload["pipeline_step"] != "review" || payload["job_id"] != "squ-202" {
		t.Fatalf("payload = %+v", payload)
	}
	unchanged, err = job.Read(teamDir, "squ-202")
	if err != nil {
		t.Fatalf("read advance dry-run job: %v", err)
	}
	if unchanged.Steps[0].Status != job.StatusRunning || unchanged.Steps[1].Status != job.StatusBlocked {
		t.Fatalf("advance dry-run mutated job = %+v", unchanged)
	}
	dryMessages, err := daemon.ReadMessages(daemon.DaemonRoot(teamDir), "manager")
	if err != nil {
		t.Fatalf("read dry-run messages: %v", err)
	}
	if len(dryMessages) != 0 {
		t.Fatalf("dry-run sent messages = %+v", dryMessages)
	}

	advanceDryFormat := NewRootCmd()
	advanceDryFormatOut, advanceDryFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	advanceDryFormat.SetOut(advanceDryFormatOut)
	advanceDryFormat.SetErr(advanceDryFormatErr)
	advanceDryFormat.SetArgs([]string{"job", "step", "squ-202", "triage", "--status", "done", "--advance", "--repo", target, "--dry-run", "--format", "{{.Job.ID}} {{.Step.ID}} {{.Dispatch.RequestedName}} {{.DryRun}}"})
	if err := advanceDryFormat.Execute(); err != nil {
		t.Fatalf("job step --advance dry-run format: %v\nstderr=%s", err, advanceDryFormatErr.String())
	}
	if got := advanceDryFormatOut.String(); got != "squ-202 review manager-squ-202-review true\n" {
		t.Fatalf("job step --advance dry-run format = %q", got)
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
	if result.Step == nil || result.Step.ID != "review" || result.Step.Status != job.StatusQueued {
		t.Fatalf("result = %+v", result)
	}
	updated, err := job.Read(teamDir, "squ-202")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.Steps[0].Status != job.StatusDone || updated.Steps[1].Status != job.StatusQueued {
		t.Fatalf("updated steps = %+v", updated.Steps)
	}

	stepOnly := &job.Job{
		ID:        "squ-202-step",
		Ticket:    "SQU-202-STEP",
		Target:    "manager",
		Kickoff:   "SQU-202-STEP: triage",
		Pipeline:  "ticket_triage",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "triage", Target: "manager", Status: job.StatusRunning, Instance: "manager", StartedAt: now},
			{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"triage"}},
		},
	}
	if err := job.Write(teamDir, stepOnly); err != nil {
		t.Fatalf("write step-only job: %v", err)
	}
	stepFormat := NewRootCmd()
	stepFormatOut, stepFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	stepFormat.SetOut(stepFormatOut)
	stepFormat.SetErr(stepFormatErr)
	stepFormat.SetArgs([]string{"job", "step", "squ-202-step", "triage", "--status", "blocked", "--message", "waiting", "--repo", target, "--format", "{{.ID}} {{.Status}} {{.LastEvent}} {{.LastStatus}}"})
	if err := stepFormat.Execute(); err != nil {
		t.Fatalf("job step format: %v\nstderr=%s", err, stepFormatErr.String())
	}
	if got := stepFormatOut.String(); got != "squ-202-step blocked step_blocked waiting\n" {
		t.Fatalf("job step format = %q", got)
	}
	stepUpdated, err := job.Read(teamDir, "squ-202-step")
	if err != nil {
		t.Fatalf("read step-only job: %v", err)
	}
	if stepUpdated.Status != job.StatusBlocked || stepUpdated.Steps[0].Status != job.StatusBlocked {
		t.Fatalf("step-only updated = %+v", stepUpdated)
	}
}

func TestJobStepRunningRequiresInstance(t *testing.T) {
	target, _, cleanup := setupIntakePipelineRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-223",
		Ticket:    "SQU-223",
		Target:    "worker",
		Kickoff:   "SQU-223: implement",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusBlocked},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	refused := NewRootCmd()
	refusedOut, refusedErr := &bytes.Buffer{}, &bytes.Buffer{}
	refused.SetOut(refusedOut)
	refused.SetErr(refusedErr)
	refused.SetArgs([]string{"job", "step", "squ-223", "implement", "--status", "running", "--repo", target})
	err := refused.Execute()
	if err == nil {
		t.Fatalf("job step ownerless running succeeded: stdout=%s", refusedOut.String())
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Fatalf("expected exit 2, got %v", err)
	}
	if !strings.Contains(refusedErr.String(), "status running requires --instance") {
		t.Fatalf("refused stderr = %q", refusedErr.String())
	}
	unchanged, err := job.Read(teamDir, "squ-223")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.Steps[0].Status != job.StatusBlocked || unchanged.Steps[0].Instance != "" {
		t.Fatalf("ownerless refusal mutated job = %+v", unchanged.Steps[0])
	}

	withInstance := NewRootCmd()
	withInstanceOut, withInstanceErr := &bytes.Buffer{}, &bytes.Buffer{}
	withInstance.SetOut(withInstanceOut)
	withInstance.SetErr(withInstanceErr)
	withInstance.SetArgs([]string{"job", "step", "squ-223", "implement", "--status", "running", "--instance", "worker-squ-223-implement", "--repo", target, "--json"})
	if err := withInstance.Execute(); err != nil {
		t.Fatalf("job step running with instance: %v\nstderr=%s", err, withInstanceErr.String())
	}
	var updated job.Job
	if err := json.Unmarshal(withInstanceOut.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated job: %v\nbody=%s", err, withInstanceOut.String())
	}
	if updated.Steps[0].Status != job.StatusRunning || updated.Steps[0].Instance != "worker-squ-223-implement" {
		t.Fatalf("updated step = %+v", updated.Steps[0])
	}

	forcedJob := &job.Job{
		ID:        "squ-223-force",
		Ticket:    "SQU-223-FORCE",
		Target:    "worker",
		Kickoff:   "SQU-223-FORCE: implement",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusBlocked},
		},
	}
	if err := job.Write(teamDir, forcedJob); err != nil {
		t.Fatalf("write forced job: %v", err)
	}
	forced := NewRootCmd()
	forcedOut, forcedErr := &bytes.Buffer{}, &bytes.Buffer{}
	forced.SetOut(forcedOut)
	forced.SetErr(forcedErr)
	forced.SetArgs([]string{"job", "step", "squ-223-force", "implement", "--status", "running", "--force", "--repo", target, "--format", "{{.ID}} {{.Status}} {{(index .Steps 0).Status}}"})
	if err := forced.Execute(); err != nil {
		t.Fatalf("job step running forced: %v\nstderr=%s", err, forcedErr.String())
	}
	if got := forcedOut.String(); got != "squ-223-force running running\n" {
		t.Fatalf("forced output = %q", got)
	}
}

func TestJobStepSkipMarksDoneAndUnblocksDependents(t *testing.T) {
	target := t.TempDir()
	initInto(t, target)
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-224",
		Ticket:    "SQU-224",
		Target:    "manager",
		Kickoff:   "SQU-224: skip triage",
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

	dryRun := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dryRun.SetOut(dryOut)
	dryRun.SetErr(dryErr)
	dryRun.SetArgs([]string{"job", "step", "squ-224", "triage", "--skip", "--message", "covered by implementation", "--repo", target, "--dry-run", "--json"})
	if err := dryRun.Execute(); err != nil {
		t.Fatalf("job step --skip dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview jobStepPreview
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode dry-run json: %v\nbody=%s", err, dryOut.String())
	}
	if !preview.DryRun || preview.Job == nil || preview.Job.Steps[0].Status != job.StatusDone || !preview.Job.Steps[0].Skipped || preview.Job.Steps[0].SkipReason != "covered by implementation" {
		t.Fatalf("skip preview = %+v", preview)
	}
	unchanged, err := job.Read(teamDir, "squ-224")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.Steps[0].Status != job.StatusRunning || unchanged.Steps[0].Skipped {
		t.Fatalf("dry-run mutated job = %+v", unchanged.Steps[0])
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "step", "squ-224", "triage", "--skip", "--message", "covered by implementation", "--repo", target})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job step --skip: %v\nstderr=%s", err, stderr.String())
	}
	if body := out.String(); !strings.Contains(body, "skipped=true") || !strings.Contains(body, "skip_reason=covered by implementation") {
		t.Fatalf("job show output missing skip metadata:\n%s", body)
	}
	updated, err := job.Read(teamDir, "squ-224")
	if err != nil {
		t.Fatalf("read updated job: %v", err)
	}
	if updated.Status != job.StatusRunning || updated.LastEvent != "step_skipped" || updated.LastStatus != "covered by implementation" {
		t.Fatalf("updated job = %+v", updated)
	}
	if updated.Steps[0].Status != job.StatusDone || !updated.Steps[0].Skipped || updated.Steps[0].SkipReason != "covered by implementation" {
		t.Fatalf("updated step = %+v", updated.Steps[0])
	}

	next := NewRootCmd()
	nextOut, nextErr := &bytes.Buffer{}, &bytes.Buffer{}
	next.SetOut(nextOut)
	next.SetErr(nextErr)
	next.SetArgs([]string{"job", "next", "squ-224", "--repo", target, "--format", "{{.State}} {{.Step.ID}}"})
	if err := next.Execute(); err != nil {
		t.Fatalf("job next after skip: %v\nstderr=%s", err, nextErr.String())
	}
	if got := nextOut.String(); got != "ready review\n" {
		t.Fatalf("job next after skip = %q", got)
	}

	conflict := NewRootCmd()
	conflictErr := &bytes.Buffer{}
	conflict.SetOut(&bytes.Buffer{})
	conflict.SetErr(conflictErr)
	conflict.SetArgs([]string{"job", "step", "squ-224", "review", "--skip", "--status", "blocked", "--repo", target})
	err = conflict.Execute()
	if err == nil {
		t.Fatal("job step --skip --status blocked succeeded")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Fatalf("expected exit 2, got %v", err)
	}
	if !strings.Contains(conflictErr.String(), "--skip can only be combined with --status done") {
		t.Fatalf("conflict stderr = %q", conflictErr.String())
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

func installFakeGHForJobTest(t *testing.T, stdout string, exitCode int) {
	t.Helper()
	binDir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\ncat <<'EOF'\n%s\nEOF\nexit %d\n", stdout, exitCode)
	path := filepath.Join(binDir, "gh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
