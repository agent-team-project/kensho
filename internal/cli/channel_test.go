package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
)

// channelTestEnv stands up a fresh daemon-side ChannelStore and an httptest
// server backed by daemon.Handler, returning a daemonClient pointed at it.
type channelTestEnv struct {
	client *daemonClient
	srv    *httptest.Server
	store  *daemon.ChannelStore
}

func newChannelTestEnv(t *testing.T) *channelTestEnv {
	t.Helper()
	root := t.TempDir()
	mgr := daemon.NewInstanceManager(root, nil)
	store := daemon.NewChannelStore(root)
	srv := httptest.NewServer(daemon.Handler(mgr, store))
	c := &daemonClient{
		hc:      &http.Client{Timeout: 0},
		baseURL: srv.URL,
		teamDir: root,
	}
	t.Cleanup(srv.Close)
	return &channelTestEnv{client: c, srv: srv, store: store}
}

func TestClient_ChannelPublishSubscribeDrainAck(t *testing.T) {
	env := newChannelTestEnv(t)
	c := env.client

	sub, err := c.ChannelSubscribe("#room", "alice")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if !sub.Subscribed || sub.Cursor != 0 {
		t.Errorf("first subscribe: %+v", sub)
	}

	for _, body := range []string{"a", "b", "c"} {
		if _, err := c.ChannelPublish("#room", "manager", body); err != nil {
			t.Fatal(err)
		}
	}

	dr, err := c.ChannelDrain(context.Background(), "#room", "alice", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(dr.Messages) != 3 {
		t.Errorf("drain: got %d want 3", len(dr.Messages))
	}
	if dr.Cursor != 3 {
		t.Errorf("cursor: got %d want 3", dr.Cursor)
	}
	if err := c.ChannelAck("#room", "alice", dr.Cursor); err != nil {
		t.Fatal(err)
	}

	dr2, _ := c.ChannelDrain(context.Background(), "#room", "alice", nil, 0)
	if len(dr2.Messages) != 0 {
		t.Errorf("post-ack drain: got %d want 0", len(dr2.Messages))
	}
}

func TestClient_ChannelDrain_LongPollWakesOnPublish(t *testing.T) {
	env := newChannelTestEnv(t)
	c := env.client
	if _, err := c.ChannelSubscribe("#live", "alice"); err != nil {
		t.Fatal(err)
	}

	type res struct {
		dr  *drainResp
		err error
		dur time.Duration
	}
	done := make(chan res, 1)
	start := time.Now()
	go func() {
		dr, err := c.ChannelDrain(context.Background(), "#live", "alice", nil, 3*time.Second)
		done <- res{dr: dr, err: err, dur: time.Since(start)}
	}()
	time.Sleep(80 * time.Millisecond)
	if _, err := c.ChannelPublish("#live", "manager", "wake"); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("drain err: %v", r.err)
		}
		if r.dur > 2*time.Second {
			t.Errorf("waited too long: %s", r.dur)
		}
		if len(r.dr.Messages) != 1 || r.dr.Messages[0].Body != "wake" {
			t.Errorf("messages: %+v", r.dr.Messages)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("never returned")
	}
}

func TestClient_ChannelDrain_WithSinceOverride(t *testing.T) {
	env := newChannelTestEnv(t)
	c := env.client
	for _, body := range []string{"a", "b", "c"} {
		c.ChannelPublish("#x", "s", body)
	}
	since := int64(0)
	dr, err := c.ChannelDrain(context.Background(), "#x", "(cli)", &since, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(dr.Messages) != 3 {
		t.Errorf("since=0 drain: got %d want 3", len(dr.Messages))
	}

	since = 1
	dr, err = c.ChannelDrain(context.Background(), "#x", "(cli)", &since, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(dr.Messages) != 2 {
		t.Errorf("since=1 drain: got %d want 2", len(dr.Messages))
	}
}

func TestClient_ChannelList(t *testing.T) {
	env := newChannelTestEnv(t)
	c := env.client

	c.ChannelPublish("#a", "s", "1")
	c.ChannelPublish("#a", "s", "2")
	c.ChannelSubscribe("#a", "alice")
	c.ChannelPublish("#b", "s", "1")

	infos, err := c.ChannelList()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("infos: got %d", len(infos))
	}
	// Sorted by name → #a then #b.
	if infos[0].Name != "#a" || infos[1].Name != "#b" {
		t.Errorf("order: %+v", infos)
	}
	if infos[0].Subscribers != 1 || infos[0].MessageCount != 2 {
		t.Errorf("#a info: %+v", infos[0])
	}
}

func TestClient_ChannelDelete(t *testing.T) {
	env := newChannelTestEnv(t)
	c := env.client

	c.ChannelPublish("#gone", "s", "x")
	if err := c.ChannelDelete("#gone"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := c.ChannelDelete("#gone"); err == nil {
		t.Errorf("delete of missing channel did not error")
	} else if !strings.Contains(err.Error(), "no such channel") {
		t.Errorf("err: %v", err)
	}
}

func TestClient_ChannelUnsubscribe_Idempotent(t *testing.T) {
	env := newChannelTestEnv(t)
	c := env.client
	c.ChannelSubscribe("#x", "alice")
	r1, err := c.ChannelUnsubscribe("#x", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !r1.Unsubscribed {
		t.Errorf("first unsubscribe: %+v", r1)
	}
	r2, _ := c.ChannelUnsubscribe("#x", "alice")
	if r2.Unsubscribed {
		t.Errorf("second unsubscribe: %+v", r2)
	}
}

func TestHumanAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{15 * time.Second, "15s"},
		{3 * time.Minute, "3m"},
		{2 * time.Hour, "2h"},
		{36 * time.Hour, "1d"},
	}
	for _, c := range cases {
		if got := humanAge(c.d); got != c.want {
			t.Errorf("humanAge(%s) = %q want %q", c.d, got, c.want)
		}
	}
}
