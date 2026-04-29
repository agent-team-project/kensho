package daemon

import (
	"sync"
	"testing"
)

func TestMailbox_AppendAndRead(t *testing.T) {
	root := t.TempDir()
	for i, body := range []string{"first", "second", "third"} {
		msg := &Message{From: "manager", Body: body}
		if err := AppendMessage(root, "worker-1", msg); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		if msg.ID == "" {
			t.Errorf("append %d did not assign ID", i)
		}
		if msg.To != "worker-1" {
			t.Errorf("append %d did not set To: got %q", i, msg.To)
		}
	}
	got, err := ReadMessages(root, "worker-1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("messages: got %d want 3", len(got))
	}
	wantBodies := []string{"first", "second", "third"}
	for i, m := range got {
		if m.Body != wantBodies[i] {
			t.Errorf("msg %d body: got %q want %q", i, m.Body, wantBodies[i])
		}
		if m.From != "manager" {
			t.Errorf("msg %d from: got %q", i, m.From)
		}
		if m.TS.IsZero() {
			t.Errorf("msg %d TS unset", i)
		}
	}
}

func TestMailbox_ReadEmptyReturnsNil(t *testing.T) {
	root := t.TempDir()
	got, err := ReadMessages(root, "nobody")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty, got %d", len(got))
	}
}

func TestMailbox_CursorAdvancesUnacked(t *testing.T) {
	root := t.TempDir()
	for _, body := range []string{"a", "b", "c", "d"} {
		if err := AppendMessage(root, "w", &Message{From: "x", Body: body}); err != nil {
			t.Fatal(err)
		}
	}
	all, _ := ReadMessages(root, "w")
	if len(all) != 4 {
		t.Fatalf("seed: got %d", len(all))
	}

	// No cursor → all unacked.
	un, err := ReadUnacked(root, "w")
	if err != nil {
		t.Fatalf("unacked: %v", err)
	}
	if len(un) != 4 {
		t.Errorf("unacked: got %d want 4", len(un))
	}

	// Ack the second message → unacked starts after it.
	if err := WriteCursor(root, "w", all[1].ID); err != nil {
		t.Fatalf("cursor: %v", err)
	}
	un, err = ReadUnacked(root, "w")
	if err != nil {
		t.Fatalf("unacked after cursor: %v", err)
	}
	if len(un) != 2 || un[0].Body != "c" || un[1].Body != "d" {
		t.Errorf("unacked after cursor=b: got %+v", un)
	}

	// Cursor at last message → unacked empty.
	if err := WriteCursor(root, "w", all[3].ID); err != nil {
		t.Fatalf("cursor: %v", err)
	}
	un, err = ReadUnacked(root, "w")
	if err != nil {
		t.Fatalf("unacked: %v", err)
	}
	if len(un) != 0 {
		t.Errorf("unacked at last: got %d want 0", len(un))
	}

	got, err := ReadCursor(root, "w")
	if err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if got != all[3].ID {
		t.Errorf("cursor: got %q want %q", got, all[3].ID)
	}
}

func TestMailbox_CursorMissingIDFallsBackToAll(t *testing.T) {
	root := t.TempDir()
	if err := AppendMessage(root, "w", &Message{From: "x", Body: "only"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteCursor(root, "w", "no-such-id"); err != nil {
		t.Fatal(err)
	}
	un, err := ReadUnacked(root, "w")
	if err != nil {
		t.Fatalf("unacked: %v", err)
	}
	if len(un) != 1 {
		t.Errorf("unacked: got %d want 1", len(un))
	}
}

func TestMailbox_ConcurrentAppendIsSerialised(t *testing.T) {
	// Race-detector + many writers proves the mutex actually serialises;
	// without it, JSONL would interleave bytes from concurrent writes.
	root := t.TempDir()
	const writers = 50
	const perWriter = 20
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		i := i
		go func() {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				_ = AppendMessage(root, "w", &Message{
					From: "sender",
					Body: "msg",
				})
				_ = i
			}
		}()
	}
	wg.Wait()
	got, err := ReadMessages(root, "w")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != writers*perWriter {
		t.Errorf("messages: got %d want %d", len(got), writers*perWriter)
	}
	// Every line must be a valid object, every ID must be unique.
	seen := make(map[string]bool, len(got))
	for _, m := range got {
		if seen[m.ID] {
			t.Errorf("duplicate id: %s", m.ID)
		}
		seen[m.ID] = true
	}
}

func TestMailbox_AppendValidation(t *testing.T) {
	root := t.TempDir()
	if err := AppendMessage(root, "", &Message{Body: "x"}); err == nil {
		t.Errorf("want error on empty instance")
	}
	if err := AppendMessage(root, "w", nil); err == nil {
		t.Errorf("want error on nil message")
	}
}
