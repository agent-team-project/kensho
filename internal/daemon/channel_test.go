package daemon

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestChannel_PublishAssignsMonotonicSeq(t *testing.T) {
	root := t.TempDir()
	cs := NewChannelStore(root)

	for i, body := range []string{"a", "b", "c"} {
		res, err := cs.Publish("#test", "manager", body)
		if err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
		if res.Seq != int64(i+1) {
			t.Errorf("publish %d seq: got %d want %d", i, res.Seq, i+1)
		}
		if res.TS.IsZero() {
			t.Errorf("publish %d: TS unset", i)
		}
	}

	msgs, err := readChannelMessagesSince(root, "#test", 0)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("messages: got %d want 3", len(msgs))
	}
	for i, m := range msgs {
		if m.Seq != int64(i+1) {
			t.Errorf("msg %d seq: got %d want %d", i, m.Seq, i+1)
		}
	}
}

func TestChannel_SubscribeIdempotent(t *testing.T) {
	root := t.TempDir()
	cs := NewChannelStore(root)

	if _, err := cs.Publish("#room", "x", "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Publish("#room", "x", "second"); err != nil {
		t.Fatal(err)
	}

	cursor1, fresh1, err := cs.Subscribe("#room", "alice")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if !fresh1 {
		t.Errorf("first subscribe: fresh=false")
	}
	if cursor1 != 2 {
		t.Errorf("first subscribe cursor: got %d want 2", cursor1)
	}

	cursor2, fresh2, err := cs.Subscribe("#room", "alice")
	if err != nil {
		t.Fatalf("re-subscribe: %v", err)
	}
	if fresh2 {
		t.Errorf("re-subscribe: fresh=true (should be no-op)")
	}
	if cursor2 != cursor1 {
		t.Errorf("re-subscribe cursor: got %d want %d", cursor2, cursor1)
	}
}

func TestChannel_DrainAfterSubscribe(t *testing.T) {
	root := t.TempDir()
	cs := NewChannelStore(root)

	// Pre-existing messages: alice subscribes after them, shouldn't see them.
	cs.Publish("#room", "x", "before-alice-1")
	cs.Publish("#room", "x", "before-alice-2")
	if _, _, err := cs.Subscribe("#room", "alice"); err != nil {
		t.Fatal(err)
	}

	// Drain right after subscribing → empty.
	res, err := cs.Drain(context.Background(), "#room", "alice", nil, 0)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(res.Messages) != 0 {
		t.Errorf("immediate drain: got %d want 0", len(res.Messages))
	}
	if res.Cursor != 2 {
		t.Errorf("cursor: got %d want 2", res.Cursor)
	}

	// Two new messages → drain returns both.
	cs.Publish("#room", "x", "after-1")
	cs.Publish("#room", "x", "after-2")
	res, err = cs.Drain(context.Background(), "#room", "alice", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 2 || res.Messages[0].Body != "after-1" || res.Messages[1].Body != "after-2" {
		t.Errorf("drain: got %+v", res.Messages)
	}
	if res.Cursor != 4 {
		t.Errorf("cursor: got %d want 4", res.Cursor)
	}

	// Cursor doesn't auto-advance — re-drain returns the same.
	res2, _ := cs.Drain(context.Background(), "#room", "alice", nil, 0)
	if len(res2.Messages) != 2 {
		t.Errorf("re-drain (no ack): got %d want 2", len(res2.Messages))
	}

	// After ack, drain is empty.
	if err := cs.Ack("#room", "alice", res.Cursor); err != nil {
		t.Fatalf("ack: %v", err)
	}
	res3, _ := cs.Drain(context.Background(), "#room", "alice", nil, 0)
	if len(res3.Messages) != 0 {
		t.Errorf("post-ack drain: got %d want 0", len(res3.Messages))
	}
}

func TestChannel_DrainSinceOverridesCursor(t *testing.T) {
	root := t.TempDir()
	cs := NewChannelStore(root)
	cs.Publish("#x", "s", "a")
	cs.Publish("#x", "s", "b")
	cs.Publish("#x", "s", "c")

	if _, _, err := cs.Subscribe("#x", "bob"); err != nil {
		t.Fatal(err)
	}
	// bob's cursor is 3 (head). With since=1, drain returns msgs 2 and 3.
	since := int64(1)
	res, err := cs.Drain(context.Background(), "#x", "bob", &since, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 2 || res.Messages[0].Seq != 2 || res.Messages[1].Seq != 3 {
		t.Errorf("since drain: got %+v", res.Messages)
	}
}

func TestChannel_AckCursorMonotonic(t *testing.T) {
	root := t.TempDir()
	cs := NewChannelStore(root)
	cs.Publish("#x", "s", "a")
	cs.Publish("#x", "s", "b")
	cs.Publish("#x", "s", "c")
	if _, _, err := cs.Subscribe("#x", "bob"); err != nil {
		t.Fatal(err)
	}

	// Pre-subscribe head cursor was 3 → bob got cursor=3. Ack with 2 should error.
	if err := cs.Ack("#x", "bob", 2); err == nil {
		t.Errorf("ack below cursor should error")
	}
	// Equal is a no-op (no error).
	if err := cs.Ack("#x", "bob", 3); err != nil {
		t.Errorf("ack at current cursor: %v", err)
	}
}

func TestChannel_Unsubscribe(t *testing.T) {
	root := t.TempDir()
	cs := NewChannelStore(root)
	if _, _, err := cs.Subscribe("#room", "alice"); err != nil {
		t.Fatal(err)
	}
	removed, err := cs.Unsubscribe("#room", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Errorf("first unsubscribe: removed=false")
	}
	// Second time → no-op.
	removed, _ = cs.Unsubscribe("#room", "alice")
	if removed {
		t.Errorf("second unsubscribe: removed=true")
	}
	// Drain on a non-subscribed instance errors.
	if _, err := cs.Drain(context.Background(), "#room", "alice", nil, 0); err == nil {
		t.Errorf("drain on non-subscriber should error")
	}
}

func TestChannel_LongPollWakesOnPublish(t *testing.T) {
	root := t.TempDir()
	cs := NewChannelStore(root)
	if _, _, err := cs.Subscribe("#room", "alice"); err != nil {
		t.Fatal(err)
	}

	done := make(chan *DrainResult, 1)
	errc := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		res, err := cs.Drain(ctx, "#room", "alice", nil, 5*time.Second)
		if err != nil {
			errc <- err
			return
		}
		done <- res
	}()

	// Give the goroutine a moment to enter the wait, then publish.
	time.Sleep(50 * time.Millisecond)
	if _, err := cs.Publish("#room", "manager", "wake up"); err != nil {
		t.Fatal(err)
	}

	select {
	case res := <-done:
		if len(res.Messages) != 1 || res.Messages[0].Body != "wake up" {
			t.Errorf("woken drain: got %+v", res.Messages)
		}
	case err := <-errc:
		t.Fatalf("drain err: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("drain did not wake within 2s of publish")
	}
}

func TestChannel_LongPollDeadlineWithoutMessage(t *testing.T) {
	root := t.TempDir()
	cs := NewChannelStore(root)
	if _, _, err := cs.Subscribe("#room", "alice"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res, err := cs.Drain(context.Background(), "#room", "alice", nil, 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	dur := time.Since(start)
	if len(res.Messages) != 0 {
		t.Errorf("expected no messages, got %d", len(res.Messages))
	}
	if dur < 150*time.Millisecond {
		t.Errorf("returned too fast: %s", dur)
	}
}

func TestChannel_ListSummary(t *testing.T) {
	root := t.TempDir()
	cs := NewChannelStore(root)
	cs.Publish("#a", "s", "one")
	cs.Publish("#a", "s", "two")
	cs.Publish("#b", "s", "single")
	cs.Subscribe("#a", "alice")
	cs.Subscribe("#a", "bob")
	cs.Subscribe("#b", "carol")

	infos, err := cs.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("channels: got %d want 2", len(infos))
	}
	if infos[0].Name != "#a" || infos[1].Name != "#b" {
		t.Errorf("sort order: %v", infos)
	}
	if infos[0].Subscribers != 2 || infos[1].Subscribers != 1 {
		t.Errorf("subscriber counts: %+v", infos)
	}
	if infos[0].MessageCount != 2 || infos[1].MessageCount != 1 {
		t.Errorf("message counts: %+v", infos)
	}
	if infos[0].LastMessageTS.IsZero() || infos[1].LastMessageTS.IsZero() {
		t.Errorf("missing last_message_ts")
	}
}

func TestChannel_DeleteThenPublishStartsFresh(t *testing.T) {
	root := t.TempDir()
	cs := NewChannelStore(root)
	cs.Publish("#x", "s", "a")
	cs.Publish("#x", "s", "b")

	removed, err := cs.Delete("#x")
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Errorf("delete: removed=false on existing channel")
	}
	// Re-publish; seq starts at 1.
	res, err := cs.Publish("#x", "s", "after-rm")
	if err != nil {
		t.Fatal(err)
	}
	if res.Seq != 1 {
		t.Errorf("post-rm seq: got %d want 1", res.Seq)
	}

	// Delete on a non-existent channel returns (false, nil).
	removed, _ = cs.Delete("#notexist")
	if removed {
		t.Errorf("delete missing: removed=true")
	}
}

func TestChannel_NameValidation(t *testing.T) {
	root := t.TempDir()
	cs := NewChannelStore(root)
	cases := []struct {
		name string
		ok   bool
	}{
		{"#valid", true},
		{"#with-dash", true},
		{"#a", true},
		{"no-hash", false},
		{"#UPPER", false},
		{"#-leading-dash", false},
		{"#has space", false},
		{"#has/slash", false},
		{"#", false},
		{"", false},
	}
	for _, c := range cases {
		_, err := cs.Publish(c.name, "s", "body")
		gotOk := err == nil
		if gotOk != c.ok {
			t.Errorf("%q: got ok=%v want %v (err=%v)", c.name, gotOk, c.ok, err)
		}
	}
}

// TestChannel_ConcurrentPublishAndDrain hammers a channel from many writers
// while readers drain in parallel. With -race -count=10, this surfaces any
// torn-line write or seq collision.
func TestChannel_ConcurrentPublishAndDrain(t *testing.T) {
	root := t.TempDir()
	cs := NewChannelStore(root)
	if _, _, err := cs.Subscribe("#stress", "reader"); err != nil {
		t.Fatal(err)
	}

	const writers = 20
	const perWriter = 25
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				_, _ = cs.Publish("#stress", "w", "msg")
			}
		}()
	}

	var drained int64
	var rwg sync.WaitGroup
	rwg.Add(2)
	for k := 0; k < 2; k++ {
		go func() {
			defer rwg.Done()
			for atomic.LoadInt64(&drained) < int64(writers*perWriter) {
				res, err := cs.Drain(context.Background(), "#stress", "reader", nil, 50*time.Millisecond)
				if err != nil {
					t.Errorf("drain err: %v", err)
					return
				}
				if len(res.Messages) > 0 {
					atomic.AddInt64(&drained, int64(len(res.Messages)))
					_ = cs.Ack("#stress", "reader", res.Cursor)
				}
			}
		}()
	}

	wg.Wait()
	rwg.Wait()

	all, err := readChannelMessagesSince(root, "#stress", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != writers*perWriter {
		t.Errorf("messages on disk: got %d want %d", len(all), writers*perWriter)
	}
	// Every seq from 1..N must be present exactly once.
	seen := make(map[int64]bool, len(all))
	for _, m := range all {
		if seen[m.Seq] {
			t.Errorf("duplicate seq: %d", m.Seq)
		}
		seen[m.Seq] = true
	}
	for i := int64(1); i <= int64(writers*perWriter); i++ {
		if !seen[i] {
			t.Errorf("missing seq: %d", i)
		}
	}
}

// TestChannel_RestartReplaysFromCursor verifies the persistent-instance
// resume scenario: after subscribe + publish + ack, a fresh ChannelStore
// instance reads the same cursor from disk and drain returns nothing for
// already-acked messages, but newly-published messages do come through.
func TestChannel_RestartReplaysFromCursor(t *testing.T) {
	root := t.TempDir()

	cs1 := NewChannelStore(root)
	if _, _, err := cs1.Subscribe("#deploys", "manager"); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"v1", "v2", "v3"} {
		if _, err := cs1.Publish("#deploys", "ci", b); err != nil {
			t.Fatal(err)
		}
	}
	res, err := cs1.Drain(context.Background(), "#deploys", "manager", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 3 {
		t.Fatalf("first drain: got %d want 3", len(res.Messages))
	}
	if err := cs1.Ack("#deploys", "manager", res.Cursor); err != nil {
		t.Fatal(err)
	}

	// Simulate daemon restart: brand-new ChannelStore against the same root.
	cs2 := NewChannelStore(root)

	// Publish more messages that arrive "while the daemon was down" — except
	// for testability we publish via cs2; the on-disk effect is identical.
	if _, err := cs2.Publish("#deploys", "ci", "v4"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs2.Publish("#deploys", "ci", "v5"); err != nil {
		t.Fatal(err)
	}

	res2, err := cs2.Drain(context.Background(), "#deploys", "manager", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Messages) != 2 || res2.Messages[0].Body != "v4" || res2.Messages[1].Body != "v5" {
		t.Errorf("post-restart drain: got %+v", res2.Messages)
	}
}

func TestChannel_SplitChannelPath(t *testing.T) {
	cases := []struct {
		path     string
		wantName string
		wantVerb string
		wantOK   bool
	}{
		{"/v1/channel/%23room/publish", "#room", "publish", true},
		{"/v1/channel/%23room/messages", "#room", "messages", true},
		{"/v1/channel/%23room", "#room", "", true},
		{"/v1/channel/%23room/", "#room", "", true},
		{"/v1/channel/", "", "", false},
		{"/v1/channel", "", "", false},
		{"/v1/something-else", "", "", false},
	}
	for _, c := range cases {
		gotName, gotVerb, gotOK := splitChannelPath(c.path)
		if gotName != c.wantName || gotVerb != c.wantVerb || gotOK != c.wantOK {
			t.Errorf("%q: got (%q,%q,%v) want (%q,%q,%v)",
				c.path, gotName, gotVerb, gotOK, c.wantName, c.wantVerb, c.wantOK)
		}
	}
}
