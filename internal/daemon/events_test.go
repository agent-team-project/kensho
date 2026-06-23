package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAppendAndStreamLifecycleEvents(t *testing.T) {
	root := t.TempDir()
	ts := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	exitCode := 0
	if err := AppendLifecycleEvent(root, &LifecycleEvent{
		ID:       "event-1",
		TS:       ts,
		Action:   "dispatch",
		Instance: "manager",
		Agent:    "manager",
		Job:      "squ-1",
		Ticket:   "SQU-1",
		Branch:   "worker-squ-1",
		PR:       "https://github.com/acme/repo/pull/1",
		Status:   StatusRunning,
		PID:      42,
		ExitCode: &exitCode,
		Message:  "started",
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	var buf bytes.Buffer
	if err := StreamLifecycleEvents(context.Background(), &buf, root, false, 0); err != nil {
		t.Fatalf("stream events: %v", err)
	}
	var got LifecycleEvent
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &got); err != nil {
		t.Fatalf("decode event: %v\nbody=%s", err, buf.String())
	}
	if got.ID != "event-1" || got.Action != "dispatch" || got.Instance != "manager" || got.PID != 42 || !got.TS.Equal(ts) {
		t.Fatalf("event = %+v", got)
	}
	if got.Job != "squ-1" || got.Ticket != "SQU-1" || got.Branch != "worker-squ-1" || got.PR == "" || got.ExitCode == nil || *got.ExitCode != 0 {
		t.Fatalf("ownership fields = %+v", got)
	}
}

func TestListLifecycleEvents(t *testing.T) {
	root := t.TempDir()
	exitCode := 2
	if err := AppendLifecycleEvent(root, &LifecycleEvent{Action: "dispatch", Instance: "worker-squ-2", Job: "squ-2"}); err != nil {
		t.Fatalf("append dispatch: %v", err)
	}
	if err := AppendLifecycleEvent(root, &LifecycleEvent{
		ID:       "event-2",
		Action:   "crash",
		Instance: "worker-squ-2",
		Agent:    "worker",
		Job:      "squ-2",
		Ticket:   "SQU-2",
		Branch:   "worker-squ-2",
		Status:   StatusCrashed,
		ExitCode: &exitCode,
	}); err != nil {
		t.Fatalf("append crash: %v", err)
	}

	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("ListLifecycleEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	got := events[1]
	if got.ID != "event-2" || got.Action != "crash" || got.Status != StatusCrashed || got.Job != "squ-2" || got.ExitCode == nil || *got.ExitCode != 2 {
		t.Fatalf("event = %+v", got)
	}
}

func TestListLifecycleEventsMissingFileIsEmpty(t *testing.T) {
	events, err := ListLifecycleEvents(t.TempDir())
	if err != nil {
		t.Fatalf("ListLifecycleEvents missing: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want empty", events)
	}
}

func TestStreamLifecycleEventsTail(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		if err := AppendLifecycleEvent(root, &LifecycleEvent{Action: "dispatch", Instance: name}); err != nil {
			t.Fatalf("append %s: %v", name, err)
		}
	}
	var buf bytes.Buffer
	if err := StreamLifecycleEvents(context.Background(), &buf, root, false, 2); err != nil {
		t.Fatalf("stream tail: %v", err)
	}
	body := buf.String()
	if strings.Contains(body, `"instance":"a"`) || !strings.Contains(body, `"instance":"b"`) || !strings.Contains(body, `"instance":"c"`) {
		t.Fatalf("tail body = %s", body)
	}
}

func TestStreamLifecycleEventsMissingFileIsEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := StreamLifecycleEvents(context.Background(), &buf, t.TempDir(), false, 0); err != nil {
		t.Fatalf("stream missing events: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("body = %q, want empty", buf.String())
	}
}
