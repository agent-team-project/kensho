package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/topology"
)

type fakeSendClient struct {
	metas              []*daemon.Metadata
	instanceCalls      int
	sentTo             string
	sentFrom           string
	sentReplyTo        string
	sentBody           string
	interruptedTo      string
	interruptedFrom    string
	interruptedReplyTo string
	interruptedBody    string
	interruptedForce   bool
	sends              []sendCall
	messageResp        *messageResponse
	messageSendErr     error
	interruptResp      *messageResponse
	interruptErr       error
}

type sendCall struct {
	to      string
	from    string
	replyTo string
	body    string
}

func sendTestTopology(names ...string) *topology.Topology {
	topo := &topology.Topology{Instances: map[string]*topology.Instance{}}
	for _, name := range names {
		topo.Instances[name] = &topology.Instance{Name: name, Agent: "manager"}
	}
	return topo
}

func writeSendAdvisorTopology(t *testing.T, root string) {
	t.Helper()
	body := `[instances.advisor]
agent = "advisor"

[instances.manager]
agent = "manager"
`
	if err := os.WriteFile(filepath.Join(root, ".agent_team", "instances.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write advisor topology: %v", err)
	}
}

func (f *fakeSendClient) Instances() ([]*daemon.Metadata, error) {
	f.instanceCalls++
	return f.metas, nil
}

func (f *fakeSendClient) SendMessage(to, from, body, replyTo string) (*messageResponse, error) {
	f.sentTo = to
	f.sentFrom = from
	f.sentReplyTo = replyTo
	f.sentBody = body
	f.sends = append(f.sends, sendCall{to: to, from: from, replyTo: replyTo, body: body})
	if f.messageSendErr != nil {
		return nil, f.messageSendErr
	}
	if f.messageResp != nil {
		return f.messageResp, nil
	}
	return &messageResponse{Delivered: true, ID: "msg-1", TS: time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)}, nil
}

func (f *fakeSendClient) InterruptMessage(to, from, body, replyTo string, force bool) (*messageResponse, error) {
	f.interruptedTo = to
	f.interruptedFrom = from
	f.interruptedReplyTo = replyTo
	f.interruptedBody = body
	f.interruptedForce = force
	if f.interruptErr != nil {
		return nil, f.interruptErr
	}
	if f.interruptResp != nil {
		return f.interruptResp, nil
	}
	return &messageResponse{Delivered: true, Interrupted: true, ID: "interrupt-1", TS: time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)}, nil
}

func TestSendRequiresKnownInstanceByDefault(t *testing.T) {
	client := &fakeSendClient{}
	stderr := &bytes.Buffer{}
	err := runSendWithClient(&bytes.Buffer{}, stderr, client, "missing", "hello", sendOptions{})
	var code ExitCode
	if !errors.As(err, &code) || code != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if !strings.Contains(stderr.String(), "not known to the daemon") {
		t.Fatalf("stderr = %q, want unknown-instance hint", stderr.String())
	}
	if client.sentTo != "" {
		t.Fatalf("message should not have been sent: %+v", client)
	}
}

func TestSendKnownInstance(t *testing.T) {
	client := &fakeSendClient{metas: []*daemon.Metadata{{Instance: "manager"}}}
	stdout := &bytes.Buffer{}
	if err := runSendWithClient(stdout, &bytes.Buffer{}, client, "manager", "hello there", sendOptions{From: "user"}); err != nil {
		t.Fatalf("runSendWithClient: %v", err)
	}
	if client.sentTo != "manager" || client.sentFrom != "user" || client.sentBody != "hello there" {
		t.Fatalf("sent = to:%q from:%q body:%q", client.sentTo, client.sentFrom, client.sentBody)
	}
	if !strings.Contains(stdout.String(), "sent") || !strings.Contains(stdout.String(), "msg-1") {
		t.Fatalf("stdout = %q, want sent confirmation", stdout.String())
	}
}

func TestSendDeclaredStoppedInstanceQueuesByDefault(t *testing.T) {
	client := &fakeSendClient{}
	stdout := &bytes.Buffer{}
	if err := runSendWithClient(stdout, &bytes.Buffer{}, client, "manager", "hello there", sendOptions{
		From:     "user",
		JSON:     true,
		Topology: sendTestTopology("manager"),
	}); err != nil {
		t.Fatalf("runSendWithClient declared: %v", err)
	}
	if client.sentTo != "manager" || client.sentFrom != "user" || client.sentBody != "hello there" {
		t.Fatalf("sent = to:%q from:%q body:%q", client.sentTo, client.sentFrom, client.sentBody)
	}
	var body sendJSON
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode send json: %v\nbody=%s", err, stdout.String())
	}
	if !body.Delivered || body.Note != daemon.MailboxDeclaredQueuedNote {
		t.Fatalf("send json = %+v, want declared queue note", body)
	}
}

func TestSendAdvisorDefaultCLISenderRequiresDurableReply(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	writeSendAdvisorTopology(t, tmp)

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	stderr := &bytes.Buffer{}
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"send", "advisor", "--repo", tmp, "--message", "which path should we take"})
	err := cmd.Execute()
	var code ExitCode
	if !errors.As(err, &code) || code != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if !strings.Contains(stderr.String(), "advisor consults need a durable reply mailbox") ||
		!strings.Contains(stderr.String(), "--reply-to manager") {
		t.Fatalf("stderr = %q, want durable reply instruction", stderr.String())
	}
	messages, err := daemon.ReadMessages(daemon.DaemonRoot(filepath.Join(tmp, ".agent_team")), "advisor")
	if err != nil {
		t.Fatalf("read advisor messages: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("advisor messages = %+v, want none", messages)
	}
}

func TestSendAdvisorReplyToDurableMailbox(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	writeSendAdvisorTopology(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"send", "advisor", "--repo", tmp, "--reply-to", "manager", "--message", "which path should we take", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("send advisor --reply-to: %v\nstderr=%s", err, stderr.String())
	}
	var body sendJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode send json: %v\nbody=%s", err, out.String())
	}
	if !body.Delivered || body.To != "advisor" || body.From != "(cli)" || body.ReplyTo != "manager" || body.ID == "" {
		t.Fatalf("send json = %+v, want durable reply target", body)
	}
	messages, err := daemon.ReadMessages(daemon.DaemonRoot(teamDir), "advisor")
	if err != nil {
		t.Fatalf("read advisor mailbox: %v", err)
	}
	if len(messages) != 1 || messages[0].From != "(cli)" || messages[0].ReplyTo != "manager" || messages[0].Body != "which path should we take" {
		t.Fatalf("messages = %+v, want operator message with durable reply target", messages)
	}
}

func TestSendUnknownUndeclaredSuggestsDeclaredNames(t *testing.T) {
	client := &fakeSendClient{}
	stderr := &bytes.Buffer{}
	err := runSendWithClient(&bytes.Buffer{}, stderr, client, "manger", "hello", sendOptions{
		Topology: sendTestTopology("manager", "ticket-manager"),
	})
	var code ExitCode
	if !errors.As(err, &code) || code != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if !strings.Contains(stderr.String(), `did you mean "manager"?`) {
		t.Fatalf("stderr = %q, want manager suggestion", stderr.String())
	}
	if client.sentTo != "" {
		t.Fatalf("message should not have been sent: %+v", client)
	}
}

func TestSendInterruptKnownInstance(t *testing.T) {
	client := &fakeSendClient{metas: []*daemon.Metadata{{Instance: "manager"}}}
	stdout := &bytes.Buffer{}
	if err := runSendWithClient(stdout, &bytes.Buffer{}, client, "manager", "stop and read this", sendOptions{From: "user", Interrupt: true, Force: true}); err != nil {
		t.Fatalf("runSendWithClient interrupt: %v", err)
	}
	if client.interruptedTo != "manager" || client.interruptedFrom != "user" || client.interruptedBody != "stop and read this" || !client.interruptedForce {
		t.Fatalf("interrupted = to:%q from:%q body:%q force:%v", client.interruptedTo, client.interruptedFrom, client.interruptedBody, client.interruptedForce)
	}
	if client.sentTo != "" {
		t.Fatalf("normal send should not have been used: %+v", client)
	}
	if !strings.Contains(stdout.String(), "interrupted") || !strings.Contains(stdout.String(), "interrupt-1") {
		t.Fatalf("stdout = %q, want interrupt confirmation", stdout.String())
	}
}

func TestSendInterruptRequiresRunningDaemon(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusRunning,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	stderr := &bytes.Buffer{}
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"send", "manager", "wake up", "--interrupt", "--repo", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("send --interrupt without daemon succeeded")
	}
	var code ExitCode
	if !errors.As(err, &code) || int(code) != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if !strings.Contains(stderr.String(), "--interrupt requires a running daemon") {
		t.Fatalf("stderr = %q, want daemon requirement", stderr.String())
	}
}

func TestSendInterruptDryRunCommands(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusRunning,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"send", "manager", "--repo", tmp, "--from", "ops", "--message", "wake up", "--interrupt", "--force", "--dry-run", "--commands"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("send --interrupt --dry-run --commands: %v\nstderr=%s", err, stderr.String())
	}
	want := strings.Join(shellQuoteArgs([]string{"agent-team", "send", "manager", "--repo", tmp, "--from", "ops", "--message", "wake up", "--interrupt", "--force"}), " ")
	if got := strings.TrimSpace(out.String()); got != want {
		t.Fatalf("send interrupt dry-run commands = %q, want %q", got, want)
	}
}

func TestSendCommandReadsMessageFile(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
	}); err != nil {
		t.Fatal(err)
	}
	messageFile := filepath.Join(tmp, "message.txt")
	message := "\nline one\n$(printf 'false FAIL') ; * ? [x]\n`uname` \\\"quoted\\\" $HOME | & < >\n"
	if err := os.WriteFile(messageFile, []byte(message), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"send", "manager", "--repo", tmp, "--message-file", messageFile, "--format", "{{.To}} {{.Delivered}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("send --message-file: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "manager true\n"; got != want {
		t.Fatalf("send --message-file output = %q, want %q", got, want)
	}
	previousInput := sendMessageInput
	sendMessageInput = strings.NewReader(message)
	t.Cleanup(func() { sendMessageInput = previousInput })
	stdinCmd := NewRootCmd()
	stdinOut, stdinErr := &bytes.Buffer{}, &bytes.Buffer{}
	stdinCmd.SetOut(stdinOut)
	stdinCmd.SetErr(stdinErr)
	stdinCmd.SetArgs([]string{"send", "manager", "--repo", tmp, "--message-file", "-", "--format", "{{.To}} {{.Delivered}}"})
	if err := stdinCmd.Execute(); err != nil {
		t.Fatalf("send --message-file -: %v\nstderr=%s", err, stdinErr.String())
	}
	if got, want := stdinOut.String(), "manager true\n"; got != want {
		t.Fatalf("send --message-file - output = %q, want %q", got, want)
	}
	messages, err := daemon.ReadMessages(daemon.DaemonRoot(teamDir), "manager")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(messages))
	}
	for i, got := range messages {
		if got.Body != message {
			t.Fatalf("message %d body = %q, want %q", i, got.Body, message)
		}
	}
}

func TestSendDryRunSingleValidatesButDoesNotAppend(t *testing.T) {
	client := &fakeSendClient{metas: []*daemon.Metadata{{Instance: "manager"}}}
	stdout := &bytes.Buffer{}
	if err := runSendWithClient(stdout, &bytes.Buffer{}, client, "manager", "hello there", sendOptions{From: "user", DryRun: true}); err != nil {
		t.Fatalf("runSendWithClient dry-run: %v", err)
	}
	if client.instanceCalls != 1 {
		t.Fatalf("Instances called %d times, want target validation", client.instanceCalls)
	}
	if len(client.sends) != 0 {
		t.Fatalf("dry-run should not send messages: %+v", client.sends)
	}
	if !strings.Contains(stdout.String(), "would-send") || !strings.Contains(stdout.String(), "manager") {
		t.Fatalf("stdout = %q, want dry-run recipient", stdout.String())
	}
}

func TestSendDryRunCommands(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusRunning,
	}); err != nil {
		t.Fatalf("write manager metadata: %v", err)
	}
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "worker",
		Agent:    "worker",
		Status:   daemon.StatusStopped,
	}); err != nil {
		t.Fatalf("write worker metadata: %v", err)
	}

	direct := NewRootCmd()
	directOut, directErr := &bytes.Buffer{}, &bytes.Buffer{}
	direct.SetOut(directOut)
	direct.SetErr(directErr)
	direct.SetArgs([]string{"send", "manager", "--repo", tmp, "--from", "ops", "--message", "hello", "--dry-run", "--commands"})
	if err := direct.Execute(); err != nil {
		t.Fatalf("send direct --dry-run --commands: %v\nstderr=%s", err, directErr.String())
	}
	wantDirect := strings.Join(shellQuoteArgs([]string{"agent-team", "send", "manager", "--repo", tmp, "--from", "ops", "--message", "hello"}), " ")
	if got := strings.TrimSpace(directOut.String()); got != wantDirect {
		t.Fatalf("send direct --dry-run --commands = %q, want %q", got, wantDirect)
	}

	rootScopedDirect := NewRootCmd()
	rootScopedDirectOut, rootScopedDirectErr := &bytes.Buffer{}, &bytes.Buffer{}
	rootScopedDirect.SetOut(rootScopedDirectOut)
	rootScopedDirect.SetErr(rootScopedDirectErr)
	rootScopedDirect.SetArgs([]string{"--repo", tmp, "send", "manager", "--from", "ops", "--message", "hello", "--dry-run", "--commands"})
	if err := rootScopedDirect.Execute(); err != nil {
		t.Fatalf("send direct root --repo --dry-run --commands: %v\nstderr=%s", err, rootScopedDirectErr.String())
	}
	if got := strings.TrimSpace(rootScopedDirectOut.String()); got != wantDirect {
		t.Fatalf("send direct root --repo --dry-run --commands = %q, want %q", got, wantDirect)
	}
	if messages, err := daemon.ReadMessages(root, "manager"); err != nil && !os.IsNotExist(err) {
		t.Fatalf("read manager messages: %v", err)
	} else if len(messages) != 0 {
		t.Fatalf("dry-run wrote manager messages: %+v", messages)
	}

	emptyMessageFlag := NewRootCmd()
	emptyMessageOut, emptyMessageErr := &bytes.Buffer{}, &bytes.Buffer{}
	emptyMessageFlag.SetOut(emptyMessageOut)
	emptyMessageFlag.SetErr(emptyMessageErr)
	emptyMessageFlag.SetArgs([]string{"send", "manager", "--repo", tmp, "--message", "", "--dry-run", "--commands", "fallback", "text"})
	if err := emptyMessageFlag.Execute(); err != nil {
		t.Fatalf("send empty --message --dry-run --commands: %v\nstderr=%s", err, emptyMessageErr.String())
	}
	wantEmptyMessage := strings.Join(shellQuoteArgs([]string{"agent-team", "send", "manager", "--repo", tmp, "fallback", "text"}), " ")
	if got := strings.TrimSpace(emptyMessageOut.String()); got != wantEmptyMessage {
		t.Fatalf("send empty --message --dry-run --commands = %q, want %q", got, wantEmptyMessage)
	}

	selection := NewRootCmd()
	selectionOut, selectionErr := &bytes.Buffer{}, &bytes.Buffer{}
	selection.SetOut(selectionOut)
	selection.SetErr(selectionErr)
	selection.SetArgs([]string{"send", "--repo", tmp, "--agent", "manager", "--status", "running", "--message", "hello", "--dry-run", "--commands"})
	if err := selection.Execute(); err != nil {
		t.Fatalf("send selection --dry-run --commands: %v\nstderr=%s", err, selectionErr.String())
	}
	wantSelection := strings.Join(shellQuoteArgs([]string{"agent-team", "send", "--repo", tmp, "--message", "hello", "--agent", "manager", "--status", "running"}), " ")
	if got := strings.TrimSpace(selectionOut.String()); got != wantSelection {
		t.Fatalf("send selection --dry-run --commands = %q, want %q", got, wantSelection)
	}

	rootScopedSelection := NewRootCmd()
	rootScopedSelectionOut, rootScopedSelectionErr := &bytes.Buffer{}, &bytes.Buffer{}
	rootScopedSelection.SetOut(rootScopedSelectionOut)
	rootScopedSelection.SetErr(rootScopedSelectionErr)
	rootScopedSelection.SetArgs([]string{"--repo", tmp, "send", "--agent", "manager", "--status", "running", "--message", "hello", "--dry-run", "--commands"})
	if err := rootScopedSelection.Execute(); err != nil {
		t.Fatalf("send selection root --repo --dry-run --commands: %v\nstderr=%s", err, rootScopedSelectionErr.String())
	}
	if got := strings.TrimSpace(rootScopedSelectionOut.String()); got != wantSelection {
		t.Fatalf("send selection root --repo --dry-run --commands = %q, want %q", got, wantSelection)
	}

	noRecipients := NewRootCmd()
	noRecipientsOut, noRecipientsErr := &bytes.Buffer{}, &bytes.Buffer{}
	noRecipients.SetOut(noRecipientsOut)
	noRecipients.SetErr(noRecipientsErr)
	noRecipients.SetArgs([]string{"send", "--repo", tmp, "--agent", "reviewer", "--message", "hello", "--dry-run", "--commands"})
	if err := noRecipients.Execute(); err != nil {
		t.Fatalf("send no-recipient --dry-run --commands: %v\nstderr=%s", err, noRecipientsErr.String())
	}
	if got := strings.TrimSpace(noRecipientsOut.String()); got != "" {
		t.Fatalf("send no-recipient --dry-run --commands = %q, want empty", got)
	}

	missing := NewRootCmd()
	missing.SetOut(&bytes.Buffer{})
	missingErr := &bytes.Buffer{}
	missing.SetErr(missingErr)
	missing.SetArgs([]string{"send", "future", "--repo", tmp, "--dry-run", "--commands", "queued"})
	err := missing.Execute()
	if err == nil {
		t.Fatalf("send missing --dry-run --commands succeeded")
	}
	var code ExitCode
	if !errors.As(err, &code) || int(code) != 2 {
		t.Fatalf("missing err = %v, want exit 2", err)
	}
	if !strings.Contains(missingErr.String(), "not known to the daemon") {
		t.Fatalf("missing stderr = %q, want unknown-instance hint", missingErr.String())
	}

}

func TestSendUsesLocalMailboxWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"send", "manager", "offline hello", "--json", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("send local mailbox: %v\nstderr=%s", err, stderr.String())
	}

	var body sendJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode send json: %v\nbody=%s", err, out.String())
	}
	if !body.Delivered || body.To != "manager" || body.From != "(cli)" || body.ID == "" {
		t.Fatalf("send json = %+v", body)
	}
	messages, err := daemon.ReadMessages(root, "manager")
	if err != nil {
		t.Fatalf("read mailbox: %v", err)
	}
	if len(messages) != 1 || messages[0].ID != body.ID || messages[0].Body != "offline hello" || messages[0].From != "(cli)" {
		t.Fatalf("messages = %+v, want appended local mailbox message", messages)
	}
}

func TestSendLatestUsesLocalNewestMailboxWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-5 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"send", "--latest", "offline", "hello", "--json", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("send --latest local mailbox: %v\nstderr=%s", err, stderr.String())
	}

	var body []sendJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode send --latest json: %v\nbody=%s", err, out.String())
	}
	if len(body) != 1 || !body[0].Delivered || body[0].To != "new" {
		t.Fatalf("send --latest json = %+v, want delivery to new", body)
	}
	messages, err := daemon.ReadMessages(root, "new")
	if err != nil {
		t.Fatalf("read newest mailbox: %v", err)
	}
	if len(messages) != 1 || messages[0].Body != "offline hello" {
		t.Fatalf("new messages = %+v, want offline hello", messages)
	}
	oldMessages, err := daemon.ReadMessages(root, "old")
	if err != nil {
		t.Fatalf("read old mailbox: %v", err)
	}
	if len(oldMessages) != 0 {
		t.Fatalf("old messages = %+v, want none", oldMessages)
	}
}

func TestSendDeclaredUsesLocalMailboxWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"send", "ticket-manager", "queued offline", "--json", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("send declared local mailbox: %v\nstderr=%s", err, stderr.String())
	}

	var body sendJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode send json: %v\nbody=%s", err, out.String())
	}
	if !body.Delivered || body.To != "ticket-manager" || body.ID == "" || body.Note != daemon.MailboxDeclaredQueuedNote {
		t.Fatalf("send json = %+v", body)
	}
	messages, err := daemon.ReadMessages(root, "ticket-manager")
	if err != nil {
		t.Fatalf("read declared mailbox: %v", err)
	}
	if len(messages) != 1 || messages[0].ID != body.ID || messages[0].Body != "queued offline" {
		t.Fatalf("messages = %+v, want queued local message", messages)
	}
}

func TestSendJSON(t *testing.T) {
	ts := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	client := &fakeSendClient{
		metas:       []*daemon.Metadata{{Instance: "worker"}},
		messageResp: &messageResponse{Delivered: true, ID: "msg-json", TS: ts},
	}
	stdout := &bytes.Buffer{}
	if err := runSendWithClient(stdout, &bytes.Buffer{}, client, "worker", "ship it", sendOptions{From: "manager", JSON: true}); err != nil {
		t.Fatalf("runSendWithClient: %v", err)
	}
	var body sendJSON
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode send json: %v\nbody=%s", err, stdout.String())
	}
	if !body.Delivered || body.To != "worker" || body.From != "manager" || body.ID != "msg-json" || !body.TS.Equal(ts) {
		t.Fatalf("send json = %+v", body)
	}
}

func TestSendFormat(t *testing.T) {
	ts := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	client := &fakeSendClient{
		metas:       []*daemon.Metadata{{Instance: "worker"}},
		messageResp: &messageResponse{Delivered: true, ID: "msg-format", TS: ts},
	}
	tmpl, err := parseSendFormat("{{.To}}:{{.From}}:{{.Delivered}}:{{.ID}}")
	if err != nil {
		t.Fatalf("parse send format: %v", err)
	}
	stdout := &bytes.Buffer{}
	if err := runSendWithClient(stdout, &bytes.Buffer{}, client, "worker", "ship it", sendOptions{From: "manager", Format: tmpl}); err != nil {
		t.Fatalf("runSendWithClient: %v", err)
	}
	if got, want := stdout.String(), "worker:manager:true:msg-format\n"; got != want {
		t.Fatalf("send format output = %q, want %q", got, want)
	}
}

func TestSendSelectionByAgentAndStatus(t *testing.T) {
	client := &fakeSendClient{
		metas: []*daemon.Metadata{
			{Instance: "adhoc", Agent: "manager", Status: daemon.StatusRunning},
			{Instance: "ticket-manager", Agent: "ticket-manager", Status: daemon.StatusRunning},
			{Instance: "manager", Agent: "manager", Status: daemon.StatusStopped},
		},
	}
	stdout := &bytes.Buffer{}
	err := runSendSelectionWithClient(stdout, &bytes.Buffer{}, client, "focus up", sendOptions{
		From:          "smoke",
		AgentFilters:  []string{"manager"},
		StatusFilters: []string{"running"},
	})
	if err != nil {
		t.Fatalf("runSendSelectionWithClient: %v", err)
	}
	if len(client.sends) != 1 || client.sends[0].to != "adhoc" || client.sends[0].from != "smoke" || client.sends[0].body != "focus up" {
		t.Fatalf("sends = %+v, want one send to adhoc", client.sends)
	}
	if !strings.Contains(stdout.String(), "adhoc") {
		t.Fatalf("stdout = %q, want adhoc send confirmation", stdout.String())
	}
}

func TestSendSelectionByPhase(t *testing.T) {
	client := &fakeSendClient{
		metas: []*daemon.Metadata{
			{Instance: "manager-blocked", Agent: "manager", Status: daemon.StatusRunning},
			{Instance: "manager-idle", Agent: "manager", Status: daemon.StatusRunning},
			{Instance: "worker-blocked", Agent: "worker", Status: daemon.StatusRunning},
		},
	}
	stdout := &bytes.Buffer{}
	err := runSendSelectionWithClient(stdout, &bytes.Buffer{}, client, "please unblock", sendOptions{
		AgentFilters: []string{"manager"},
		PhaseFilters: []string{"blocked"},
		PhaseByInstance: map[string]string{
			"manager-blocked": "blocked",
			"manager-idle":    "idle",
			"worker-blocked":  "blocked",
		},
	})
	if err != nil {
		t.Fatalf("runSendSelectionWithClient: %v", err)
	}
	if len(client.sends) != 1 || client.sends[0].to != "manager-blocked" || client.sends[0].body != "please unblock" {
		t.Fatalf("sends = %+v, want one send to manager-blocked", client.sends)
	}
	if !strings.Contains(stdout.String(), "manager-blocked") {
		t.Fatalf("stdout = %q, want manager-blocked send confirmation", stdout.String())
	}
}

func TestSendSelectionByStale(t *testing.T) {
	client := &fakeSendClient{
		metas: []*daemon.Metadata{
			{Instance: "manager-stale", Agent: "manager", Status: daemon.StatusRunning},
			{Instance: "manager-fresh", Agent: "manager", Status: daemon.StatusRunning},
			{Instance: "worker-stale", Agent: "worker", Status: daemon.StatusRunning},
		},
	}
	stdout := &bytes.Buffer{}
	err := runSendSelectionWithClient(stdout, &bytes.Buffer{}, client, "please report", sendOptions{
		AgentFilters: []string{"manager"},
		Stale:        true,
		StaleByInstance: map[string]bool{
			"manager-stale": true,
			"worker-stale":  true,
		},
	})
	if err != nil {
		t.Fatalf("runSendSelectionWithClient: %v", err)
	}
	if len(client.sends) != 1 || client.sends[0].to != "manager-stale" || client.sends[0].body != "please report" {
		t.Fatalf("sends = %+v, want one send to manager-stale", client.sends)
	}
	if !strings.Contains(stdout.String(), "manager-stale") {
		t.Fatalf("stdout = %q, want manager-stale send confirmation", stdout.String())
	}
}

func TestSendSelectionByUnhealthy(t *testing.T) {
	client := &fakeSendClient{
		metas: []*daemon.Metadata{
			{Instance: "manager-crashed", Agent: "manager", Status: daemon.StatusCrashed},
			{Instance: "manager-stale", Agent: "manager", Status: daemon.StatusRunning},
			{Instance: "manager-fresh", Agent: "manager", Status: daemon.StatusRunning},
			{Instance: "worker-stale", Agent: "worker", Status: daemon.StatusRunning},
		},
	}
	stdout := &bytes.Buffer{}
	err := runSendSelectionWithClient(stdout, &bytes.Buffer{}, client, "please report", sendOptions{
		AgentFilters: []string{"manager"},
		Unhealthy:    true,
		StaleByInstance: map[string]bool{
			"manager-stale": true,
			"worker-stale":  true,
		},
	})
	if err != nil {
		t.Fatalf("runSendSelectionWithClient: %v", err)
	}
	if len(client.sends) != 2 || client.sends[0].to != "manager-crashed" || client.sends[1].to != "manager-stale" {
		t.Fatalf("sends = %+v, want sends to crashed and stale managers", client.sends)
	}
	if !strings.Contains(stdout.String(), "manager-crashed") || !strings.Contains(stdout.String(), "manager-stale") {
		t.Fatalf("stdout = %q, want unhealthy recipients", stdout.String())
	}
}

func TestSendPhaseFilterUsesLocalMailboxWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusRunning},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "blocked"
description = "needs input"
`, time.Time{})
	writeStatus(t, filepath.Join(teamDir, "state", "worker"), `[status]
phase = "idle"
description = "waiting"
`, time.Time{})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"send", "--phase", "blocked", "offline phase hello", "--json", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("send --phase local mailbox: %v\nstderr=%s", err, stderr.String())
	}

	var rows []sendJSON
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode send --phase json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || !rows[0].Delivered || rows[0].To != "manager" {
		t.Fatalf("send --phase json = %+v, want delivery to manager", rows)
	}
	messages, err := daemon.ReadMessages(root, "manager")
	if err != nil {
		t.Fatalf("read manager mailbox: %v", err)
	}
	if len(messages) != 1 || messages[0].Body != "offline phase hello" {
		t.Fatalf("manager messages = %+v, want phase-filtered message", messages)
	}
	workerMessages, err := daemon.ReadMessages(root, "worker")
	if err != nil {
		t.Fatalf("read worker mailbox: %v", err)
	}
	if len(workerMessages) != 0 {
		t.Fatalf("worker messages = %+v, want none", workerMessages)
	}
}

func TestSendStaleFilterUsesLocalMailboxWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusRunning},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "implementing"
description = "stuck"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "worker"), `[status]
phase = "implementing"
description = "fresh"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"send", "--stale", "offline stale hello", "--json", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("send --stale local mailbox: %v\nstderr=%s", err, stderr.String())
	}

	var rows []sendJSON
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode send --stale json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || !rows[0].Delivered || rows[0].To != "manager" {
		t.Fatalf("send --stale json = %+v, want delivery to manager", rows)
	}
	messages, err := daemon.ReadMessages(root, "manager")
	if err != nil {
		t.Fatalf("read manager mailbox: %v", err)
	}
	if len(messages) != 1 || messages[0].Body != "offline stale hello" {
		t.Fatalf("manager messages = %+v, want stale-filtered message", messages)
	}
	workerMessages, err := daemon.ReadMessages(root, "worker")
	if err != nil {
		t.Fatalf("read worker mailbox: %v", err)
	}
	if len(workerMessages) != 0 {
		t.Fatalf("worker messages = %+v, want none", workerMessages)
	}
}

func TestSendUnhealthyFilterUsesLocalMailboxWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "crashed", Agent: "manager", Status: daemon.StatusCrashed},
		{Instance: "stale", Agent: "manager", Status: daemon.StatusRunning},
		{Instance: "fresh", Agent: "manager", Status: daemon.StatusRunning},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "crashed"), `[status]
phase = "idle"
description = "crashed"
`, now)
	writeStatus(t, filepath.Join(teamDir, "state", "stale"), `[status]
phase = "implementing"
description = "stuck"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "fresh"), `[status]
phase = "implementing"
description = "fresh"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"send", "--unhealthy", "offline health hello", "--json", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("send --unhealthy local mailbox: %v\nstderr=%s", err, stderr.String())
	}

	var rows []sendJSON
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode send --unhealthy json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 2 || !rows[0].Delivered || !rows[1].Delivered || rows[0].To != "crashed" || rows[1].To != "stale" {
		t.Fatalf("send --unhealthy json = %+v, want delivery to crashed and stale", rows)
	}
	for _, name := range []string{"crashed", "stale"} {
		messages, err := daemon.ReadMessages(root, name)
		if err != nil {
			t.Fatalf("read %s mailbox: %v", name, err)
		}
		if len(messages) != 1 || messages[0].Body != "offline health hello" {
			t.Fatalf("%s messages = %+v, want unhealthy-filtered message", name, messages)
		}
	}
	freshMessages, err := daemon.ReadMessages(root, "fresh")
	if err != nil {
		t.Fatalf("read fresh mailbox: %v", err)
	}
	if len(freshMessages) != 0 {
		t.Fatalf("fresh messages = %+v, want none", freshMessages)
	}
}

func TestSendRuntimeStaleFilterUsesLocalMailboxWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "crashed", Agent: "manager", Runtime: "codex", Status: daemon.StatusCrashed, PID: 0, StartedAt: old},
		{Instance: "runtime-stale", Agent: "manager", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999999, StartedAt: old},
		{Instance: "status-stale", Agent: "manager", Runtime: "codex", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: old},
		{Instance: "fresh", Agent: "manager", Runtime: "codex", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "status-stale"), `[status]
phase = "implementing"
description = "old status"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "fresh"), `[status]
phase = "implementing"
description = "fresh"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"send", "--runtime-stale", "runtime stale hello", "--json", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("send --runtime-stale local mailbox: %v\nstderr=%s", err, stderr.String())
	}

	var rows []sendJSON
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode send --runtime-stale json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || !rows[0].Delivered || rows[0].To != "runtime-stale" {
		t.Fatalf("send --runtime-stale json = %+v, want delivery to runtime-stale only", rows)
	}
	messages, err := daemon.ReadMessages(root, "runtime-stale")
	if err != nil {
		t.Fatalf("read runtime-stale mailbox: %v", err)
	}
	if len(messages) != 1 || messages[0].Body != "runtime stale hello" {
		t.Fatalf("runtime-stale messages = %+v, want runtime-stale-filtered message", messages)
	}
	for _, name := range []string{"crashed", "status-stale", "fresh"} {
		messages, err := daemon.ReadMessages(root, name)
		if err != nil {
			t.Fatalf("read %s mailbox: %v", name, err)
		}
		if len(messages) != 0 {
			t.Fatalf("%s messages = %+v, want none", name, messages)
		}
	}
}

func TestSendRuntimeFilterUsesLocalMailboxWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, meta := range []*daemon.Metadata{
		{Instance: "codex-worker", Agent: "worker", Runtime: "codex", RuntimeBinary: "codex-dev", Status: daemon.StatusRunning},
		{Instance: "claude-manager", Agent: "manager", Runtime: "claude", RuntimeBinary: "claude-code", Status: daemon.StatusRunning},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"send", "--runtime", "codex", "runtime hello", "--json", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("send --runtime local mailbox: %v\nstderr=%s", err, stderr.String())
	}

	var rows []sendJSON
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode send --runtime json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || !rows[0].Delivered || rows[0].To != "codex-worker" {
		t.Fatalf("send --runtime json = %+v, want delivery to codex worker", rows)
	}
	messages, err := daemon.ReadMessages(root, "codex-worker")
	if err != nil {
		t.Fatalf("read codex mailbox: %v", err)
	}
	if len(messages) != 1 || messages[0].Body != "runtime hello" {
		t.Fatalf("codex messages = %+v, want runtime-filtered message", messages)
	}
	claudeMessages, err := daemon.ReadMessages(root, "claude-manager")
	if err != nil {
		t.Fatalf("read claude mailbox: %v", err)
	}
	if len(claudeMessages) != 0 {
		t.Fatalf("claude messages = %+v, want none", claudeMessages)
	}
}

func TestSendLatestSelectsNewestMatchingTarget(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	client := &fakeSendClient{
		metas: []*daemon.Metadata{
			{Instance: "worker-new", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-5 * time.Minute)},
			{Instance: "manager-old", Agent: "manager", Status: daemon.StatusRunning, StartedAt: now.Add(-2 * time.Hour)},
			{Instance: "manager-new", Agent: "manager", Status: daemon.StatusRunning, StartedAt: now.Add(-30 * time.Minute)},
		},
	}
	stdout := &bytes.Buffer{}
	err := runSendSelectionWithClient(stdout, &bytes.Buffer{}, client, "focus up", sendOptions{
		Latest:       true,
		AgentFilters: []string{"manager"},
	})
	if err != nil {
		t.Fatalf("runSendSelectionWithClient: %v", err)
	}
	if len(client.sends) != 1 || client.sends[0].to != "manager-new" {
		t.Fatalf("sends = %+v, want one send to newest matching manager", client.sends)
	}
	if !strings.Contains(stdout.String(), "manager-new") {
		t.Fatalf("stdout = %q, want manager-new send confirmation", stdout.String())
	}
}

func TestSendLastDryRunJSONSelectsNewestTargets(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	client := &fakeSendClient{
		metas: []*daemon.Metadata{
			{Instance: "old", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-2 * time.Hour)},
			{Instance: "new", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-5 * time.Minute)},
			{Instance: "mid", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-30 * time.Minute)},
		},
	}
	stdout := &bytes.Buffer{}
	err := runSendSelectionWithClient(stdout, &bytes.Buffer{}, client, "broadcast", sendOptions{
		Limit:  2,
		DryRun: true,
		JSON:   true,
	})
	if err != nil {
		t.Fatalf("runSendSelectionWithClient: %v", err)
	}
	if len(client.sends) != 0 {
		t.Fatalf("dry-run should not send messages: %+v", client.sends)
	}
	var rows []sendJSON
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("decode send --last json: %v\nbody=%s", err, stdout.String())
	}
	if len(rows) != 2 || rows[0].To != "new" || rows[1].To != "mid" {
		t.Fatalf("rows = %+v, want newest two new,mid", rows)
	}
}

func TestSendSelectionSplitsCommaSeparatedAgentFilters(t *testing.T) {
	client := &fakeSendClient{
		metas: []*daemon.Metadata{
			{Instance: "adhoc", Agent: "manager", Status: daemon.StatusRunning},
			{Instance: "ticket-manager", Agent: "ticket-manager", Status: daemon.StatusRunning},
			{Instance: "worker", Agent: "worker", Status: daemon.StatusRunning},
		},
	}
	err := runSendSelectionWithClient(&bytes.Buffer{}, &bytes.Buffer{}, client, "focus up", sendOptions{
		AgentFilters:  []string{"manager,ticket-manager"},
		StatusFilters: []string{"running"},
	})
	if err != nil {
		t.Fatalf("runSendSelectionWithClient: %v", err)
	}
	got := make([]string, 0, len(client.sends))
	for _, send := range client.sends {
		got = append(got, send.to)
	}
	if strings.Join(got, ",") != "adhoc,ticket-manager" {
		t.Fatalf("sends = %+v, want adhoc,ticket-manager", client.sends)
	}
}

func TestSendSelectionAllJSON(t *testing.T) {
	ts := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	client := &fakeSendClient{
		metas: []*daemon.Metadata{
			{Instance: "z-worker", Agent: "worker", Status: daemon.StatusRunning},
			{Instance: "a-manager", Agent: "manager", Status: daemon.StatusStopped},
		},
		messageResp: &messageResponse{Delivered: true, ID: "msg-json", TS: ts},
	}
	stdout := &bytes.Buffer{}
	err := runSendSelectionWithClient(stdout, &bytes.Buffer{}, client, "broadcast", sendOptions{All: true, JSON: true})
	if err != nil {
		t.Fatalf("runSendSelectionWithClient: %v", err)
	}
	var rows []sendJSON
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("decode send json: %v\nbody=%s", err, stdout.String())
	}
	if len(rows) != 2 || rows[0].To != "a-manager" || rows[1].To != "z-worker" {
		t.Fatalf("send rows = %+v, want sorted all targets", rows)
	}
}

func TestSendSelectionDryRunJSONDoesNotAppend(t *testing.T) {
	client := &fakeSendClient{
		metas: []*daemon.Metadata{
			{Instance: "z-worker", Agent: "worker", Status: daemon.StatusRunning},
			{Instance: "a-manager", Agent: "manager", Status: daemon.StatusRunning},
		},
	}
	stdout := &bytes.Buffer{}
	err := runSendSelectionWithClient(stdout, &bytes.Buffer{}, client, "broadcast", sendOptions{All: true, From: "operator", DryRun: true, JSON: true})
	if err != nil {
		t.Fatalf("runSendSelectionWithClient dry-run: %v", err)
	}
	if len(client.sends) != 0 {
		t.Fatalf("dry-run should not send messages: %+v", client.sends)
	}
	var rows []sendJSON
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("decode send dry-run json: %v\nbody=%s", err, stdout.String())
	}
	if len(rows) != 2 || rows[0].To != "a-manager" || rows[1].To != "z-worker" {
		t.Fatalf("send rows = %+v, want sorted dry-run targets", rows)
	}
	for _, row := range rows {
		if !row.DryRun || row.Delivered || row.From != "operator" || row.ID != "" {
			t.Fatalf("dry-run row = %+v, want dry_run true and no delivery id", row)
		}
	}
}

func TestSelectSendTargetsLatestLimitUsesNewestStartedAt(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	client := &fakeSendClient{
		metas: []*daemon.Metadata{
			{Instance: "old", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-2 * time.Hour)},
			{Instance: "missing", Agent: "worker", Status: daemon.StatusRunning},
			{Instance: "new", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-5 * time.Minute)},
			{Instance: "mid", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-30 * time.Minute)},
		},
	}
	targets, err := selectSendTargets(client, sendOptions{Limit: 2})
	if err != nil {
		t.Fatalf("selectSendTargets: %v", err)
	}
	if strings.Join(targets, ",") != "new,mid" {
		t.Fatalf("targets = %v, want new,mid", targets)
	}
}

func TestSendSelectionFormat(t *testing.T) {
	client := &fakeSendClient{
		metas: []*daemon.Metadata{
			{Instance: "z-worker", Agent: "worker", Status: daemon.StatusRunning},
			{Instance: "a-manager", Agent: "manager", Status: daemon.StatusRunning},
		},
		messageResp: &messageResponse{Delivered: true, ID: "msg-format", TS: time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)},
	}
	tmpl, err := parseSendFormat("{{.To}}:{{.ID}}")
	if err != nil {
		t.Fatalf("parse send format: %v", err)
	}
	stdout := &bytes.Buffer{}
	err = runSendSelectionWithClient(stdout, &bytes.Buffer{}, client, "broadcast", sendOptions{All: true, Format: tmpl})
	if err != nil {
		t.Fatalf("runSendSelectionWithClient: %v", err)
	}
	if got, want := stdout.String(), "a-manager:msg-format\nz-worker:msg-format\n"; got != want {
		t.Fatalf("send selection format = %q, want %q", got, want)
	}
}

func TestSendSelectionEmptyMatch(t *testing.T) {
	client := &fakeSendClient{metas: []*daemon.Metadata{{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning}}}
	stdout := &bytes.Buffer{}
	err := runSendSelectionWithClient(stdout, &bytes.Buffer{}, client, "hello", sendOptions{AgentFilters: []string{"worker"}})
	if err != nil {
		t.Fatalf("runSendSelectionWithClient: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "(no instances)" {
		t.Fatalf("stdout = %q, want no instances", stdout.String())
	}
	if len(client.sends) != 0 {
		t.Fatalf("sends = %+v, want none", client.sends)
	}
}

func TestSendSelectionEmptyMatchFormatSuppressesText(t *testing.T) {
	client := &fakeSendClient{metas: []*daemon.Metadata{{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning}}}
	tmpl, err := parseSendFormat("{{.To}}")
	if err != nil {
		t.Fatalf("parse send format: %v", err)
	}
	stdout := &bytes.Buffer{}
	err = runSendSelectionWithClient(stdout, &bytes.Buffer{}, client, "hello", sendOptions{AgentFilters: []string{"worker"}, Format: tmpl})
	if err != nil {
		t.Fatalf("runSendSelectionWithClient: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("formatted empty match should not write text, got %q", stdout.String())
	}
	if len(client.sends) != 0 {
		t.Fatalf("sends = %+v, want none", client.sends)
	}
}

func TestSendFormatRejectsConflictingModes(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"send", "manager", "hello", "--format", "{{.To}}", "--json"}, "--format cannot be combined with --json"},
		{[]string{"send", "manager", "hello", "--format", "{{"}, "invalid --format template"},
		{[]string{"send", "manager", "hello", "--message", "also"}, "provide message text using only one"},
		{[]string{"send", "manager", "--message-file", filepath.Join(t.TempDir(), "missing.txt")}, "--message-file:"},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
	}
}

func TestSendCommandsValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"send", "manager", "hello", "--commands"}, wantCommandCommandsModeRequiresDryRun("send")},
		{[]string{"send", "manager", "hello", "--dry-run", "--commands", "--json"}, wantCommandCommandsModeConflict("send", "--json")},
		{[]string{"send", "manager", "hello", "--dry-run", "--commands", "--format", "{{.To}}"}, wantCommandCommandsModeConflict("send", "--format")},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		var code ExitCode
		if !errors.As(err, &code) || int(code) != 2 {
			t.Fatalf("%v: err = %v, want exit 2", tc.args, err)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
		if out.Len() != 0 {
			t.Fatalf("%v: validation wrote stdout: %q", tc.args, out.String())
		}
	}
}

func TestSendLatestLastValidation(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"send", "--last", "-1", "hello"}, "--last must be >= 0"},
		{[]string{"send", "--latest", "--last", "2", "hello"}, "choose one of --latest or --last"},
		{[]string{"send", "--latest"}, "message body is required"},
		{[]string{"send"}, "instance and message body are required unless --all, --latest, --last, --agent, --runtime, --status, --phase, --stale, --runtime-stale, or --unhealthy"},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(append(tc.args, "--repo", tmp))
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
	}
}

func TestSendSelectionValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
		opts sendOptions
		want string
	}{
		{
			name: "empty body",
			body: " ",
			opts: sendOptions{All: true},
			want: "message body is required",
		},
		{
			name: "negative last",
			body: "hello",
			opts: sendOptions{Limit: -1},
			want: "--last must be >= 0",
		},
		{
			name: "latest and last",
			body: "hello",
			opts: sendOptions{Latest: true, Limit: 2},
			want: "choose one of --latest or --last",
		},
		{
			name: "empty agent",
			body: "hello",
			opts: sendOptions{AgentFilters: []string{"  "}},
			want: "non-empty agent",
		},
		{
			name: "bad runtime",
			body: "hello",
			opts: sendOptions{RuntimeFilters: []string{"llama"}},
			want: "unknown --runtime",
		},
		{
			name: "empty runtime",
			body: "hello",
			opts: sendOptions{RuntimeFilters: []string{"  "}},
			want: "non-empty runtime",
		},
		{
			name: "bad status",
			body: "hello",
			opts: sendOptions{StatusFilters: []string{"paused"}},
			want: "unknown --status",
		},
		{
			name: "bad phase",
			body: "hello",
			opts: sendOptions{PhaseFilters: []string{"reviewing"}},
			want: "unknown --phase",
		},
		{
			name: "empty phase",
			body: "hello",
			opts: sendOptions{PhaseFilters: []string{"  "}},
			want: "non-empty phase",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stderr := &bytes.Buffer{}
			err := runSendSelectionWithClient(&bytes.Buffer{}, stderr, &fakeSendClient{}, tc.body, tc.opts)
			var code ExitCode
			if !errors.As(err, &code) || code != 2 {
				t.Fatalf("err = %v, want exit 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}
