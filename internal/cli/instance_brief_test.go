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
	jobstore "github.com/jamesaud/agent-team/internal/job"
)

func TestInstanceBrief_EmptyStateRendersAndWrites(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"
description = "Owns delivery."
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"instance", "brief", "manager", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("instance brief: %v\nstderr=%s", err, stderr.String())
	}
	body := out.String()
	for _, want := range []string{
		"# Instance brief: manager",
		"Agent: manager",
		"Role: Owns delivery.",
		"Owned Jobs",
		"(none)",
		"Fleet Snapshot",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("brief output missing %q:\n%s", want, body)
		}
	}
	path := filepath.Join(teamDir, "state", "manager", "brief.md")
	if fileBody, err := os.ReadFile(path); err != nil {
		t.Fatalf("read brief.md: %v", err)
	} else if string(fileBody) != body {
		t.Fatalf("brief.md mismatch:\nfile=%s\nstdout=%s", string(fileBody), body)
	}
}

func TestInstanceBrief_JSONIncludesOwnedState(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"
description = "Delivery manager."

[instances.worker]
agent = "worker"
ephemeral = true

[teams.delivery]
instances = ["manager", "worker"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance:  "manager",
		Agent:     "manager",
		Status:    daemon.StatusStopped,
		Runtime:   "claude",
		Workspace: tmp,
		SessionID: "session-1",
		Job:       "squ-52",
		Ticket:    "SQU-52",
		StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	j := &jobstore.Job{
		ID:         "squ-52",
		Ticket:     "SQU-52",
		Target:     "manager",
		Instance:   "manager",
		Pipeline:   "ticket_to_pr",
		Status:     jobstore.StatusRunning,
		Branch:     "squ-52",
		PR:         "https://github.example/pr/1",
		LastStatus: "running implement",
		CreatedAt:  now,
		UpdatedAt:  now,
		Steps: []jobstore.Step{{
			ID:       "implement",
			Target:   "manager",
			Instance: "manager",
			Status:   jobstore.StatusRunning,
		}},
	}
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatal(err)
	}
	if err := daemon.AppendMessage(root, "manager", &daemon.Message{ID: "msg-1", From: "worker", Body: "please review", TS: now}); err != nil {
		t.Fatal(err)
	}
	channels := daemon.NewChannelStore(root)
	if _, _, err := channels.Subscribe("#review", "manager"); err != nil {
		t.Fatal(err)
	}
	if _, err := channels.Publish("#review", "worker", "review update"); err != nil {
		t.Fatal(err)
	}
	if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{ID: "ev-1", TS: now, Action: "start", Instance: "manager", Job: "squ-52", Status: daemon.StatusRunning, Message: "instance resumed"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(teamDir, "state", "manager"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "state", "manager", "status.toml"), []byte(`
[status]
phase = "implementing"
description = "Working SQU-52"

[work]
job = "squ-52"
ticket = "SQU-52"
branch = "squ-52"
pr = "https://github.example/pr/1"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"instance", "brief", "manager", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("instance brief --json: %v\nstderr=%s", err, stderr.String())
	}
	var brief daemon.InstanceBrief
	if err := json.Unmarshal(out.Bytes(), &brief); err != nil {
		t.Fatalf("decode brief json: %v\nbody=%s", err, out.String())
	}
	if brief.Instance != "manager" || brief.Agent != "manager" || brief.Runtime == nil || brief.Runtime.SessionID != "session-1" {
		t.Fatalf("identity/runtime = %+v runtime=%+v", brief, brief.Runtime)
	}
	if len(brief.Jobs) != 1 || brief.Jobs[0].ID != "squ-52" || brief.Jobs[0].Branch != "squ-52" {
		t.Fatalf("jobs = %+v", brief.Jobs)
	}
	if len(brief.Pipelines) != 1 || brief.Pipelines[0].Name != "ticket_to_pr" || brief.Pipelines[0].Running != 1 {
		t.Fatalf("pipelines = %+v", brief.Pipelines)
	}
	if len(brief.Mailbox) != 1 || brief.Mailbox[0].ID != "msg-1" {
		t.Fatalf("mailbox = %+v", brief.Mailbox)
	}
	if len(brief.Channels) != 1 || brief.Channels[0].Name != "#review" || brief.Channels[0].Unread != 1 {
		t.Fatalf("channels = %+v", brief.Channels)
	}
	if len(brief.Events) != 1 || brief.Events[0].ID != "ev-1" {
		t.Fatalf("events = %+v", brief.Events)
	}
	if len(brief.Fleet) == 0 || brief.Fleet[0].Instance != "manager" {
		t.Fatalf("fleet = %+v", brief.Fleet)
	}
	if _, err := os.Stat(filepath.Join(teamDir, "state", "manager", "brief.md")); err != nil {
		t.Fatalf("brief.md not written: %v", err)
	}
}
