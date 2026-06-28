package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	srv := httptest.NewServer(daemon.Handler(mgr, store, nil, ""))
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

func TestChannelCommandsUseLocalStoreWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if err := runChannelPublish(out, stderr, teamDir, "#ops", "tester", "offline broadcast", channelPublishOptions{}); err != nil {
		t.Fatalf("publish local channel: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(out.String(), "published seq=1") {
		t.Fatalf("publish output = %q, want seq=1", out.String())
	}

	out.Reset()
	if err := runChannelLs(out, stderr, teamDir, channelListOptions{}); err != nil {
		t.Fatalf("list local channels: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(out.String(), "#ops") || !strings.Contains(out.String(), "1") {
		t.Fatalf("list output = %q, want #ops with one message", out.String())
	}

	out.Reset()
	if err := runChannelShow(out, stderr, teamDir, "#ops", channelShowOptions{Tail: 10}); err != nil {
		t.Fatalf("show local channel: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(out.String(), "offline broadcast") || !strings.Contains(out.String(), "messages:      1") {
		t.Fatalf("show output = %q, want local message", out.String())
	}

	out.Reset()
	if _, err := runChannelRm(out, stderr, teamDir, "#ops", channelRmOptions{}); err != nil {
		t.Fatalf("rm local channel: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(out.String(), "removed #ops") {
		t.Fatalf("rm output = %q, want removal", out.String())
	}

	out.Reset()
	if err := runChannelLs(out, stderr, teamDir, channelListOptions{}); err != nil {
		t.Fatalf("list after rm: %v\nstderr=%s", err, stderr.String())
	}
	if strings.TrimSpace(out.String()) != "(no channels)" {
		t.Fatalf("list after rm = %q, want no channels", out.String())
	}
}

func TestChannelLsSortLimitFormatAndJSON(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	for _, item := range []struct {
		channel string
		bodies  []string
	}{
		{channel: "#alpha", bodies: []string{"a"}},
		{channel: "#beta", bodies: []string{"b1", "b2", "b3"}},
		{channel: "#gamma", bodies: []string{"g1", "g2"}},
	} {
		for _, body := range item.bodies {
			if err := runChannelPublish(&bytes.Buffer{}, &bytes.Buffer{}, teamDir, item.channel, "tester", body, channelPublishOptions{}); err != nil {
				t.Fatalf("publish %s/%s: %v", item.channel, body, err)
			}
		}
	}

	stdout, stderr, err := executeChannelCommand("channel", "ls", "--target", tmp, "--sort", "messages", "--limit", "2", "--format", "{{.Name}} {{.MessageCount}}")
	if err != nil {
		t.Fatalf("channel ls format: %v\nstderr=%s", err, stderr)
	}
	if got, want := strings.TrimSpace(stdout), "#beta 3\n#gamma 2"; got != want {
		t.Fatalf("channel ls format = %q, want %q", got, want)
	}

	stdout, stderr, err = executeChannelCommand("channels", "--target", tmp, "--sort", "messages", "--limit", "1", "--json")
	if err != nil {
		t.Fatalf("channels json: %v\nstderr=%s", err, stderr)
	}
	var infos []channelInfo
	if err := json.Unmarshal([]byte(stdout), &infos); err != nil {
		t.Fatalf("decode channels json: %v\nbody=%s", err, stdout)
	}
	if len(infos) != 1 || infos[0].Name != "#beta" || infos[0].MessageCount != 3 {
		t.Fatalf("channels json = %+v, want #beta only", infos)
	}
}

func TestChannelShowTailJSONAndFormat(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	for _, body := range []string{"first", "second", "third"} {
		if err := runChannelPublish(&bytes.Buffer{}, &bytes.Buffer{}, teamDir, "#ops", "tester", body, channelPublishOptions{}); err != nil {
			t.Fatalf("publish %s: %v", body, err)
		}
	}

	stdout, stderr, err := executeChannelCommand("channel", "show", "#ops", "--target", tmp, "--tail", "2", "--json")
	if err != nil {
		t.Fatalf("channel show json: %v\nstderr=%s", err, stderr)
	}
	if strings.Contains(stdout, "channel:") {
		t.Fatalf("channel show json included text summary: %q", stdout)
	}
	var result channelShowResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode channel show json: %v\nbody=%s", err, stdout)
	}
	if result.Channel == nil || result.Channel.Name != "#ops" || len(result.Messages) != 2 || result.Messages[0].Body != "second" || result.Messages[1].Body != "third" {
		t.Fatalf("channel show json = %+v, want tail second/third", result)
	}

	stdout, stderr, err = executeChannelCommand("channel", "show", "#ops", "--target", tmp, "--tail", "1", "--format", "{{.Channel.Name}} {{len .Messages}} {{(index .Messages 0).Body}}")
	if err != nil {
		t.Fatalf("channel show format: %v\nstderr=%s", err, stderr)
	}
	if got, want := strings.TrimSpace(stdout), "#ops 1 third"; got != want {
		t.Fatalf("channel show format = %q, want %q", got, want)
	}
}

func TestChannelPublishAndRmMachineOutput(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	stdout, stderr, err := executeChannelCommand("channel", "publish", "#ops", "--target", tmp, "--sender", "tester", "--message", "deploy ready", "--json")
	if err != nil {
		t.Fatalf("channel publish json: %v\nstderr=%s", err, stderr)
	}
	var published channelPublishResult
	if err := json.Unmarshal([]byte(stdout), &published); err != nil {
		t.Fatalf("decode publish json: %v\nbody=%s", err, stdout)
	}
	if published.Channel != "#ops" || published.Sender != "tester" || published.Body != "deploy ready" || published.Seq != 1 {
		t.Fatalf("publish json = %+v, want #ops/tester/seq1", published)
	}

	stdout, stderr, err = executeChannelCommand("channel", "publish", "#ops", "--target", tmp, "--message", "second", "--format", "{{.Channel}} {{.Seq}} {{.Body}}")
	if err != nil {
		t.Fatalf("channel publish format: %v\nstderr=%s", err, stderr)
	}
	if got, want := strings.TrimSpace(stdout), "#ops 2 second"; got != want {
		t.Fatalf("publish format = %q, want %q", got, want)
	}

	stdout, stderr, err = executeChannelCommand("channel", "rm", "#ops", "--target", tmp, "--dry-run", "--format", "{{.Name}} {{.Action}} {{.DryRun}}")
	if err != nil {
		t.Fatalf("channel rm dry-run format: %v\nstderr=%s", err, stderr)
	}
	if got, want := strings.TrimSpace(stdout), "#ops would-remove true"; got != want {
		t.Fatalf("rm dry-run format = %q, want %q", got, want)
	}
	stdout, stderr, err = executeChannelCommand("channel", "show", "#ops", "--target", tmp, "--tail", "0", "--format", "{{.Channel.MessageCount}}")
	if err != nil {
		t.Fatalf("channel show after dry-run rm: %v\nstderr=%s", err, stderr)
	}
	if got, want := strings.TrimSpace(stdout), "2"; got != want {
		t.Fatalf("channel after dry-run rm count = %q, want %q", got, want)
	}

	stdout, stderr, err = executeChannelCommand("--repo", tmp, "channel", "rm", "#ops", "--dry-run", "--commands")
	if err != nil {
		t.Fatalf("channel rm commands: %v\nstderr=%s", err, stderr)
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "channel", "rm", "#ops", "--repo", tmp, "--force"}), " ")
	if got := strings.TrimSpace(stdout); got != wantCommand {
		t.Fatalf("rm commands = %q, want %q", got, wantCommand)
	}

	stdout, stderr, err = executeChannelCommand("channel", "rm", "#ops", "--target", tmp, "--force", "--json")
	if err != nil {
		t.Fatalf("channel rm json: %v\nstderr=%s", err, stderr)
	}
	var removed channelRmResult
	if err := json.Unmarshal([]byte(stdout), &removed); err != nil {
		t.Fatalf("decode rm json: %v\nbody=%s", err, stdout)
	}
	if removed.Name != "#ops" || !removed.Removed || removed.Action != "removed" || removed.DryRun {
		t.Fatalf("rm json = %+v, want removed #ops", removed)
	}
}

func TestChannelMachineOutputValidation(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "ls rejects json format",
			args: []string{"channel", "ls", "--target", tmp, "--json", "--format", "{{.Name}}"},
			want: "--format cannot be combined with --json",
		},
		{
			name: "ls rejects negative limit",
			args: []string{"channel", "ls", "--target", tmp, "--limit", "-1"},
			want: "--limit must be >= 0",
		},
		{
			name: "ls rejects bad sort",
			args: []string{"channel", "ls", "--target", tmp, "--sort", "created"},
			want: "--sort must be name, subscribers, messages, or last",
		},
		{
			name: "show rejects json format",
			args: []string{"channel", "show", "#ops", "--target", tmp, "--json", "--format", "{{.Channel.Name}}"},
			want: "--format cannot be combined with --json",
		},
		{
			name: "show rejects negative tail",
			args: []string{"channel", "show", "#ops", "--target", tmp, "--tail", "-1"},
			want: "--tail must be >= 0",
		},
		{
			name: "publish rejects json format",
			args: []string{"channel", "publish", "#ops", "--target", tmp, "--message", "body", "--json", "--format", "{{.Seq}}"},
			want: "--format cannot be combined with --json",
		},
		{
			name: "rm rejects commands without dry run",
			args: []string{"channel", "rm", "#ops", "--target", tmp, "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "rm rejects commands json",
			args: []string{"channel", "rm", "#ops", "--target", tmp, "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "rm rejects commands format",
			args: []string{"channel", "rm", "#ops", "--target", tmp, "--dry-run", "--commands", "--format", "{{.Name}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "rm rejects json format",
			args: []string{"channel", "rm", "#ops", "--target", tmp, "--json", "--format", "{{.Name}}"},
			want: "--format cannot be combined with --json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, err := executeChannelCommand(tt.args...)
			var code ExitCode
			if !errors.As(err, &code) || code != 2 {
				t.Fatalf("err = %v, want exit 2", err)
			}
			if !strings.Contains(stderr, tt.want) {
				t.Fatalf("stderr = %q, want %q", stderr, tt.want)
			}
		})
	}
}

func TestChannelPublishCommandMessageFile(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	messageFile := filepath.Join(tmp, "broadcast.txt")
	if err := os.WriteFile(messageFile, []byte("file broadcast\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	publishFile := NewRootCmd()
	fileOut, fileErr := &bytes.Buffer{}, &bytes.Buffer{}
	publishFile.SetOut(fileOut)
	publishFile.SetErr(fileErr)
	publishFile.SetArgs([]string{"channel", "publish", "#ops", "--target", tmp, "--sender", "tester", "--message-file", messageFile})
	if err := publishFile.Execute(); err != nil {
		t.Fatalf("channel publish --message-file: %v\nstderr=%s", err, fileErr.String())
	}
	if !strings.Contains(fileOut.String(), "published seq=1") {
		t.Fatalf("publish file output = %q, want seq=1", fileOut.String())
	}

	oldInput := sendMessageInput
	sendMessageInput = strings.NewReader("stdin broadcast\n")
	defer func() { sendMessageInput = oldInput }()
	publishStdin := NewRootCmd()
	stdinOut, stdinErr := &bytes.Buffer{}, &bytes.Buffer{}
	publishStdin.SetOut(stdinOut)
	publishStdin.SetErr(stdinErr)
	publishStdin.SetArgs([]string{"channel", "publish", "#ops", "--target", tmp, "--message-file", "-"})
	if err := publishStdin.Execute(); err != nil {
		t.Fatalf("channel publish stdin: %v\nstderr=%s", err, stdinErr.String())
	}
	if !strings.Contains(stdinOut.String(), "published seq=2") {
		t.Fatalf("publish stdin output = %q, want seq=2", stdinOut.String())
	}

	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	if err := runChannelShow(showOut, showErr, teamDir, "#ops", channelShowOptions{Tail: 10}); err != nil {
		t.Fatalf("show channel after publishes: %v\nstderr=%s", err, showErr.String())
	}
	if !strings.Contains(showOut.String(), "file broadcast") || !strings.Contains(showOut.String(), "stdin broadcast") {
		t.Fatalf("show output = %q, want both published bodies", showOut.String())
	}

	conflict := NewRootCmd()
	conflictOut, conflictErr := &bytes.Buffer{}, &bytes.Buffer{}
	conflict.SetOut(conflictOut)
	conflict.SetErr(conflictErr)
	conflict.SetArgs([]string{"channel", "publish", "#ops", "body", "--target", tmp, "--message", "flag"})
	if err := conflict.Execute(); err == nil {
		t.Fatal("channel publish conflicting message sources succeeded")
	}
	if !strings.Contains(conflictErr.String(), "only one of positional args, --message, or --message-file") {
		t.Fatalf("conflict stderr = %q", conflictErr.String())
	}
}

func TestChannelCommandsRoundTripHashNameThroughDaemon(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agt-channel-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), nil)
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	run := func(args ...string) (string, string, error) {
		cmd := NewRootCmd()
		out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(stderr)
		cmd.SetArgs(args)
		err := cmd.Execute()
		return out.String(), stderr.String(), err
	}

	out, stderr, err := run("channel", "publish", "--target", tmp, "#standup", "Codex docs validation")
	if err != nil {
		t.Fatalf("publish daemon channel: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(out, "published seq=1") {
		t.Fatalf("publish output = %q, want seq=1", out)
	}

	out, stderr, err = run("channels", "--target", tmp)
	if err != nil {
		t.Fatalf("channels: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(out, "#standup") || !strings.Contains(out, "1") {
		t.Fatalf("channels output = %q, want #standup with one message", out)
	}

	out, stderr, err = run("channel", "show", "--target", tmp, "#standup")
	if err != nil {
		t.Fatalf("show daemon channel: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(out, "channel:       #standup") || !strings.Contains(out, "Codex docs validation") {
		t.Fatalf("show output = %q, want #standup message", out)
	}

	out, stderr, err = run("channel", "rm", "--target", tmp, "--force", "#standup")
	if err != nil {
		t.Fatalf("rm daemon channel: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(out, "removed #standup") {
		t.Fatalf("rm output = %q, want #standup removal", out)
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

func executeChannelCommand(args ...string) (string, string, error) {
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), stderr.String(), err
}
