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
	if !strings.Contains(showOut.String(), "Kickoff:") || !strings.Contains(showOut.String(), "implement the status monitor") {
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
	dispatched, err := job.Read(filepath.Join(target, ".agent_team"), "squ-44")
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
