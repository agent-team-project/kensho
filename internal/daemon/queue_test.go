package daemon

import (
	"errors"
	"io/fs"
	"testing"
	"time"
)

func TestQueueItemReadWriteListAndRemove(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC)
	item := &QueueItem{
		ID:         "q1",
		State:      QueueStatePending,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-1",
		Payload:    map[string]any{"target": "worker", "ticket": "SQU-1"},
		QueuedAt:   now,
		UpdatedAt:  now,
	}
	if err := WriteQueueItem(root, item); err != nil {
		t.Fatalf("WriteQueueItem: %v", err)
	}
	got, err := ReadQueueItem(root, "q1")
	if err != nil {
		t.Fatalf("ReadQueueItem: %v", err)
	}
	if got.ID != "q1" || got.State != QueueStatePending || got.Payload["ticket"] != "SQU-1" {
		t.Fatalf("got = %+v", got)
	}
	items, err := ListQueueItems(root)
	if err != nil {
		t.Fatalf("ListQueueItems: %v", err)
	}
	if len(items) != 1 || items[0].ID != "q1" {
		t.Fatalf("items = %+v", items)
	}
	if err := RemoveQueueItem(root, "q1"); err != nil {
		t.Fatalf("RemoveQueueItem: %v", err)
	}
	if _, err := ReadQueueItem(root, "q1"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Read removed err=%v, want fs.ErrNotExist", err)
	}
}

func TestQueueItemDeadLetterAndRetryReset(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	item := &QueueItem{
		ID:         "q2",
		State:      QueueStatePending,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-2",
		Payload:    map[string]any{"target": "worker"},
		Attempts:   MaxQueueAttempts,
		LastError:  "spawn failed",
		QueuedAt:   now,
		UpdatedAt:  now,
	}
	if err := MoveQueueItemToDead(root, item); err != nil {
		t.Fatalf("MoveQueueItemToDead: %v", err)
	}
	dead, err := ReadQueueItem(root, "q2")
	if err != nil {
		t.Fatalf("Read dead: %v", err)
	}
	if dead.State != QueueStateDead || dead.DeadLetteredAt.IsZero() {
		t.Fatalf("dead = %+v", dead)
	}
	if err := ResetQueueItemForRetry(root, dead); err != nil {
		t.Fatalf("ResetQueueItemForRetry: %v", err)
	}
	pending, err := ReadQueueItem(root, "q2")
	if err != nil {
		t.Fatalf("Read pending: %v", err)
	}
	if pending.State != QueueStatePending || pending.LastError != "" || !pending.DeadLetteredAt.IsZero() {
		t.Fatalf("pending = %+v", pending)
	}
}
