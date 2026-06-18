package cli

import (
	"bytes"
	"encoding/json"
	"os"
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
