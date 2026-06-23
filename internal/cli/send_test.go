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

	"github.com/jamesaud/agent-team/internal/daemon"
)

type fakeSendClient struct {
	metas          []*daemon.Metadata
	instanceCalls  int
	sentTo         string
	sentFrom       string
	sentBody       string
	sends          []sendCall
	messageResp    *messageResponse
	messageSendErr error
}

type sendCall struct {
	to   string
	from string
	body string
}

func (f *fakeSendClient) Instances() ([]*daemon.Metadata, error) {
	f.instanceCalls++
	return f.metas, nil
}

func (f *fakeSendClient) SendMessage(to, from, body string) (*messageResponse, error) {
	f.sentTo = to
	f.sentFrom = from
	f.sentBody = body
	f.sends = append(f.sends, sendCall{to: to, from: from, body: body})
	if f.messageSendErr != nil {
		return nil, f.messageSendErr
	}
	if f.messageResp != nil {
		return f.messageResp, nil
	}
	return &messageResponse{Delivered: true, ID: "msg-1", TS: time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)}, nil
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
	if err := os.WriteFile(messageFile, []byte("line one\nline two\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"send", "manager", "--target", tmp, "--message-file", messageFile, "--format", "{{.To}} {{.Delivered}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("send --message-file: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "manager true\n"; got != want {
		t.Fatalf("send --message-file output = %q, want %q", got, want)
	}
	messages, err := daemon.ReadMessages(daemon.DaemonRoot(teamDir), "manager")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 1 || messages[0].Body != "line one\nline two" {
		t.Fatalf("messages = %+v", messages)
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

func TestSendAllowMissingQueuesWithoutKnownCheck(t *testing.T) {
	client := &fakeSendClient{}
	if err := runSendWithClient(&bytes.Buffer{}, &bytes.Buffer{}, client, "future", "queued", sendOptions{AllowMissing: true}); err != nil {
		t.Fatalf("runSendWithClient: %v", err)
	}
	if client.instanceCalls != 0 {
		t.Fatalf("Instances called %d times, want none with --allow-missing", client.instanceCalls)
	}
	if client.sentTo != "future" || client.sentFrom != "(cli)" {
		t.Fatalf("sent = to:%q from:%q", client.sentTo, client.sentFrom)
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
	cmd.SetArgs([]string{"send", "manager", "offline hello", "--json", "--target", tmp})
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
	cmd.SetArgs([]string{"send", "--latest", "offline", "hello", "--json", "--target", tmp})
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

func TestSendAllowMissingUsesLocalMailboxWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"send", "future-worker", "queued offline", "--allow-missing", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("send --allow-missing local mailbox: %v\nstderr=%s", err, stderr.String())
	}

	var body sendJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode send json: %v\nbody=%s", err, out.String())
	}
	if !body.Delivered || body.To != "future-worker" || body.ID == "" {
		t.Fatalf("send json = %+v", body)
	}
	messages, err := daemon.ReadMessages(root, "future-worker")
	if err != nil {
		t.Fatalf("read future mailbox: %v", err)
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
	cmd.SetArgs([]string{"send", "--phase", "blocked", "offline phase hello", "--json", "--target", tmp})
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
	cmd.SetArgs([]string{"send", "--stale", "offline stale hello", "--json", "--target", tmp})
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
	cmd.SetArgs([]string{"send", "--unhealthy", "offline health hello", "--json", "--target", tmp})
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
	cmd.SetArgs([]string{"send", "--runtime", "codex", "runtime hello", "--json", "--target", tmp})
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
		{[]string{"send"}, "instance and message body are required unless --all, --latest, --last, --agent, --runtime, --status, --phase, --stale, or --unhealthy"},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(append(tc.args, "--target", tmp))
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
			name: "allow missing",
			body: "hello",
			opts: sendOptions{All: true, AllowMissing: true},
			want: "--allow-missing cannot be combined",
		},
		{
			name: "allow missing latest",
			body: "hello",
			opts: sendOptions{Latest: true, AllowMissing: true},
			want: "--allow-missing cannot be combined",
		},
		{
			name: "allow missing stale",
			body: "hello",
			opts: sendOptions{Stale: true, AllowMissing: true},
			want: "--allow-missing cannot be combined",
		},
		{
			name: "allow missing unhealthy",
			body: "hello",
			opts: sendOptions{Unhealthy: true, AllowMissing: true},
			want: "--allow-missing cannot be combined",
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
