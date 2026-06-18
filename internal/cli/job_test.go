package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
