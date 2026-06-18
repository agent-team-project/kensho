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
