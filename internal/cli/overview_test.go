package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/job"
)

func TestOverviewReportsAttentionAndActions(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview json: %v\nstderr=%s", err, stderr.String())
	}

	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview: %v\nbody=%s", err, out.String())
	}
	if overview.OK || overview.State != "attention" {
		t.Fatalf("overview state = ok:%v state:%q", overview.OK, overview.State)
	}
	if overview.Health.DaemonRunning || overview.Health.Issues == 0 {
		t.Fatalf("health = %+v, want daemon down with issues", overview.Health)
	}
	if overview.Topology == nil || overview.Topology.Instances != 2 || overview.Topology.Pipelines != 1 {
		t.Fatalf("topology = %+v", overview.Topology)
	}
	if overview.Jobs.Summary.Total != 1 || overview.Jobs.Summary.Active != 1 || overview.Jobs.Summary.Terminal != 0 || overview.Jobs.Summary.Blocked != 1 || overview.Jobs.Attention != 1 {
		t.Fatalf("jobs = %+v", overview.Jobs)
	}
	if overview.Queue.Total != 1 || overview.Queue.Dead != 1 || overview.Queue.Attempts != daemon.MaxQueueAttempts {
		t.Fatalf("queue = %+v", overview.Queue)
	}
	if overview.Pipelines.Total != 1 || overview.Pipelines.ReadySteps != 1 || overview.Pipelines.BlockedSteps != 0 {
		t.Fatalf("pipelines = %+v", overview.Pipelines)
	}
	if overview.Schedules.Declared != 1 || overview.Schedules.Due != 1 {
		t.Fatalf("schedules = %+v", overview.Schedules)
	}
	for _, want := range []string{
		"agent-team repair --dry-run --jobs",
		"agent-team daemon start",
		"agent-team sync --dry-run",
		"agent-team job queue retry squ-700 --all --sort attempts --limit 10 --dry-run",
		"agent-team job queue quarantine squ-700",
		"agent-team job triage",
		"agent-team schedule fire --dry-run --preview-triggers",
		"agent-team drain",
	} {
		if !stringSliceContains(overview.Actions, want) {
			t.Fatalf("actions missing %q: %+v", want, overview.Actions)
		}
	}
	if len(overview.ActionDetails) != len(overview.Actions) {
		t.Fatalf("action details = %+v, want one detail per action", overview.ActionDetails)
	}
	if detail, ok := findOperatorActionHint(overview.ActionDetails, "agent-team job queue retry squ-700 --all --sort attempts --limit 10 --dry-run"); !ok || detail.Source != "queue" || detail.Reason != "queue_dead_letter" {
		t.Fatalf("queue retry detail = %+v, ok=%v", detail, ok)
	}
	if detail, ok := findOperatorActionHint(overview.ActionDetails, "agent-team drain"); !ok || detail.Source != "overview" || !strings.HasPrefix(detail.Reason, "drainable_work=") {
		t.Fatalf("drain detail = %+v, ok=%v", detail, ok)
	}
	if stringSliceContains(overview.Actions, "agent-team queue quarantine ls") {
		t.Fatalf("actions should use job-scoped queue quarantine: %+v", overview.Actions)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"overview", "--repo", root, "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("overview --commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommands := strings.Join(scopedOperatorActions(commandActionsOnly(overview.Actions), operatorCommandScope{Repo: root, Set: true}), "\n") + "\n"
	if got := commandsOut.String(); got != wantCommands {
		t.Fatalf("overview --commands = %q, want %q", got, wantCommands)
	}
}

func TestScopedOperatorActionsPreserveCommandTails(t *testing.T) {
	scope := operatorCommandScope{Repo: "/tmp/repo with spaces", Set: true}
	actions := []string{
		"agent-team repair --dry-run",
		"agent-team --repo /already/scoped repair --dry-run",
		"agent-team job note squ-1 --message 'hello world'",
		"git status --short",
	}

	got := scopedOperatorActions(actions, scope)
	want := []string{
		"agent-team --repo '/tmp/repo with spaces' repair --dry-run",
		"agent-team --repo /already/scoped repair --dry-run",
		"agent-team --repo '/tmp/repo with spaces' job note squ-1 --message 'hello world'",
		"git status --short",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("scoped actions = %#v, want %#v", got, want)
	}
}

func TestOverviewActionSelectionFlags(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root, "--source", "schedules", "--reason", "due", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview filtered json: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode filtered overview: %v\nbody=%s", err, out.String())
	}
	if len(overview.Actions) != 1 || overview.Actions[0] != "agent-team schedule fire --dry-run --preview-triggers" {
		t.Fatalf("filtered overview actions = %+v", overview.Actions)
	}
	if len(overview.ActionDetails) != 1 || overview.ActionDetails[0].Source != "schedules" || overview.ActionDetails[0].Reason != "due=1" {
		t.Fatalf("filtered overview details = %+v", overview.ActionDetails)
	}
	if overview.Queue.Dead != 1 || overview.Schedules.Due != 1 {
		t.Fatalf("overview summaries should remain unfiltered: queue=%+v schedules=%+v", overview.Queue, overview.Schedules)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"overview", "--repo", root, "--source", "queue", "--reason", "queue_dead_letter", "--sort", "command", "--limit", "1", "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("overview filtered commands: %v\nstderr=%s", err, commandsErr.String())
	}
	want := scopedOperatorAction("agent-team job queue retry squ-700 --all --sort attempts --limit 10 --dry-run", operatorCommandScope{Repo: root, Set: true}) + "\n"
	if got := commandsOut.String(); got != want {
		t.Fatalf("overview filtered commands = %q, want %q", got, want)
	}
}

func TestTeamOverviewActionSelectionFlags(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "overview", "delivery", "--repo", root, "--source", "queue", "--reason", "queue_dead_letter", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team overview filtered json: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode filtered team overview: %v\nbody=%s", err, out.String())
	}
	if overview.Team == nil || overview.Team.Name != "delivery" {
		t.Fatalf("team overview team = %+v", overview.Team)
	}
	if len(overview.Actions) != 1 || overview.Actions[0] != "agent-team team queue retry delivery --all --job squ-700 --sort attempts --limit 10 --dry-run" {
		t.Fatalf("filtered team overview actions = %+v", overview.Actions)
	}
	for _, detail := range overview.ActionDetails {
		if detail.Team != "delivery" || detail.Source != "queue" || detail.Reason != "queue_dead_letter" {
			t.Fatalf("filtered team detail = %+v, want delivery queue queue_dead_letter", detail)
		}
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"team", "overview", "delivery", "--repo", root, "--source", "schedules", "--reason", "due", "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("team overview filtered commands: %v\nstderr=%s", err, commandsErr.String())
	}
	want := scopedOperatorAction("agent-team team tick delivery --dry-run --skip-drain --skip-advance", operatorCommandScope{Repo: root, Set: true}) + "\n"
	if got := commandsOut.String(); got != want {
		t.Fatalf("team overview filtered commands = %q, want %q", got, want)
	}
}

func TestOperatorActionWithLastMessageOnlyTouchesResumePlan(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "top-level resume plan",
			in:   "agent-team resume-plan --status crashed",
			want: "agent-team resume-plan --status crashed --last-message",
		},
		{
			name: "runtime resume plan",
			in:   "agent-team runtime resume-plan worker --action logs",
			want: "agent-team runtime resume-plan worker --action logs --last-message",
		},
		{
			name: "job resume plan",
			in:   "agent-team job resume-plan squ-42 --step implement",
			want: "agent-team job resume-plan squ-42 --step implement --last-message",
		},
		{
			name: "pipeline resume plan",
			in:   "agent-team pipeline resume-plan ticket_to_pr --runtime-stale",
			want: "agent-team pipeline resume-plan ticket_to_pr --runtime-stale --last-message",
		},
		{
			name: "team resume plan",
			in:   "agent-team team resume-plan delivery --status crashed",
			want: "agent-team team resume-plan delivery --status crashed --last-message",
		},
		{
			name: "team runtime resume plan",
			in:   "agent-team team runtime resume-plan delivery --status crashed",
			want: "agent-team team runtime resume-plan delivery --status crashed --last-message",
		},
		{
			name: "already present",
			in:   "agent-team resume-plan --status crashed --last-message",
			want: "agent-team resume-plan --status crashed --last-message",
		},
		{
			name: "repair untouched",
			in:   "agent-team repair --dry-run",
			want: "agent-team repair --dry-run",
		},
		{
			name: "foreign command untouched",
			in:   "git status --short",
			want: "git status --short",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := operatorActionWithLastMessage(tc.in); got != tc.want {
				t.Fatalf("operatorActionWithLastMessage(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestOperatorActionWithResumePlanFallbacks(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		lastMessage bool
		fallbacks   bool
		want        string
	}{
		{
			name:      "top-level fallbacks",
			in:        "agent-team resume-plan --status crashed",
			fallbacks: true,
			want:      "agent-team resume-plan --status crashed --commands --fallbacks",
		},
		{
			name:        "last message and fallbacks",
			in:          "agent-team job resume-plan squ-42 --runtime-stale",
			lastMessage: true,
			fallbacks:   true,
			want:        "agent-team job resume-plan squ-42 --runtime-stale --last-message --commands --fallbacks",
		},
		{
			name:      "preserves existing commands flag",
			in:        "agent-team pipeline resume-plan ticket_to_pr --commands",
			fallbacks: true,
			want:      "agent-team pipeline resume-plan ticket_to_pr --commands --fallbacks",
		},
		{
			name:        "does not duplicate flags",
			in:          "agent-team team resume-plan delivery --last-message --commands --fallbacks",
			lastMessage: true,
			fallbacks:   true,
			want:        "agent-team team resume-plan delivery --last-message --commands --fallbacks",
		},
		{
			name:      "non resume plan untouched",
			in:        "agent-team repair --dry-run",
			fallbacks: true,
			want:      "agent-team repair --dry-run",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := operatorActionWithResumePlanOptions(tc.in, tc.lastMessage, tc.fallbacks); got != tc.want {
				t.Fatalf("operatorActionWithResumePlanOptions(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestOverviewReportsUnreadInboxActions(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)
	teamDir := filepath.Join(root, ".agent_team")
	daemonRoot := daemon.DaemonRoot(teamDir)
	if err := daemon.AppendMessage(daemonRoot, "manager", &daemon.Message{
		ID:   "msg-overview",
		From: "tester",
		Body: "please check in",
	}); err != nil {
		t.Fatalf("append message: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview unread inbox json: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview unread inbox: %v\nbody=%s", err, out.String())
	}
	if overview.Inbox.Total != 1 || overview.Inbox.Unread != 1 || overview.Inbox.UnreadInstances != 1 || !stringSliceContains(overview.Inbox.UnreadNames, "manager") {
		t.Fatalf("inbox summary = %+v", overview.Inbox)
	}
	if !stringSliceContains(overview.Actions, "agent-team inbox ls --unread") {
		t.Fatalf("actions missing inbox unread command: %+v", overview.Actions)
	}
	if detail, ok := findOperatorActionHint(overview.ActionDetails, "agent-team inbox ls --unread"); !ok || detail.Source != "inbox" || detail.Reason != "unread=1" {
		t.Fatalf("inbox detail = %+v, ok=%v", detail, ok)
	}

	next := NewRootCmd()
	nextOut, nextErr := &bytes.Buffer{}, &bytes.Buffer{}
	next.SetOut(nextOut)
	next.SetErr(nextErr)
	next.SetArgs([]string{"next", "--repo", root, "--source", "inbox", "--json"})
	if err := next.Execute(); err != nil {
		t.Fatalf("next unread inbox json: %v\nstderr=%s", err, nextErr.String())
	}
	var nextResult nextActionResult
	if err := json.Unmarshal(nextOut.Bytes(), &nextResult); err != nil {
		t.Fatalf("decode next unread inbox: %v\nbody=%s", err, nextOut.String())
	}
	if len(nextResult.Actions) != 1 || nextResult.Actions[0] != "agent-team inbox ls --unread" {
		t.Fatalf("next inbox actions = %+v", nextResult)
	}
}

func TestOverviewReportsOutboxActions(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)
	teamDir := filepath.Join(root, ".agent_team")
	now := time.Now().UTC()
	items := []*daemon.OutboxItem{
		{
			ID:        "outbox-pending",
			State:     daemon.OutboxStatePending,
			Type:      "agent.dispatch",
			Payload:   map[string]any{"job_id": "squ-810", "target": "worker"},
			Source:    "manager",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "outbox-failed",
			State:     daemon.OutboxStateFailed,
			Type:      "agent.dispatch",
			Payload:   map[string]any{"job_id": "squ-811", "target": "worker"},
			Source:    "manager",
			LastError: "topology not configured",
			CreatedAt: now.Add(time.Minute),
			UpdatedAt: now.Add(time.Minute),
		},
	}
	for _, item := range items {
		if err := daemon.WriteOutboxItem(teamDir, item); err != nil {
			t.Fatalf("write outbox item %s: %v", item.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview outbox json: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview outbox: %v\nbody=%s", err, out.String())
	}
	if overview.OK || overview.State != "attention" {
		t.Fatalf("overview state = ok:%v state:%q", overview.OK, overview.State)
	}
	if overview.Outbox.Total != 2 || overview.Outbox.Pending != 1 || overview.Outbox.Failed != 1 {
		t.Fatalf("outbox summary = %+v", overview.Outbox)
	}
	for _, want := range []string{
		"agent-team outbox ls --state failed",
		"agent-team outbox drain --dry-run",
		"agent-team drain",
	} {
		if !stringSliceContains(overview.Actions, want) {
			t.Fatalf("actions missing %q: %+v", want, overview.Actions)
		}
	}
	if detail, ok := findOperatorActionHint(overview.ActionDetails, "agent-team outbox ls --state failed"); !ok || detail.Source != "outbox" || detail.Reason != "failed=1" {
		t.Fatalf("failed outbox detail = %+v, ok=%v", detail, ok)
	}
	if detail, ok := findOperatorActionHint(overview.ActionDetails, "agent-team outbox drain --dry-run"); !ok || detail.Source != "outbox" || detail.Reason != "pending=1" {
		t.Fatalf("pending outbox detail = %+v, ok=%v", detail, ok)
	}

	next := NewRootCmd()
	nextOut, nextErr := &bytes.Buffer{}, &bytes.Buffer{}
	next.SetOut(nextOut)
	next.SetErr(nextErr)
	next.SetArgs([]string{"next", "--repo", root, "--source", "outbox", "--json"})
	if err := next.Execute(); err != nil {
		t.Fatalf("next outbox json: %v\nstderr=%s", err, nextErr.String())
	}
	var nextResult nextActionResult
	if err := json.Unmarshal(nextOut.Bytes(), &nextResult); err != nil {
		t.Fatalf("decode next outbox: %v\nbody=%s", err, nextOut.String())
	}
	if len(nextResult.Actions) != 2 {
		t.Fatalf("next outbox actions = %+v", nextResult)
	}
	for _, want := range []string{"agent-team outbox ls --state failed", "agent-team outbox drain --dry-run"} {
		if !stringSliceContains(nextResult.Actions, want) {
			t.Fatalf("next outbox actions missing %q: %+v", want, nextResult.Actions)
		}
	}
	for _, detail := range nextResult.ActionDetails {
		if detail.Source != "outbox" {
			t.Fatalf("next outbox detail = %+v, want source outbox", detail)
		}
	}
}

func TestOverviewPrefersJobScopedOutboxActions(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)
	teamDir := filepath.Join(root, ".agent_team")
	now := time.Date(2026, 6, 27, 18, 0, 0, 0, time.UTC)
	j := &job.Job{
		ID:        "squ-812",
		Ticket:    "SQU-812",
		Target:    "worker",
		Instance:  "worker-squ-812",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	for _, item := range []*daemon.OutboxItem{
		{
			ID:        "outbox-job-pending",
			State:     daemon.OutboxStatePending,
			Type:      "agent.dispatch",
			Payload:   map[string]any{"job_id": "squ-812", "target": "worker"},
			Source:    "manager",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "outbox-job-failed",
			State:     daemon.OutboxStateFailed,
			Type:      "agent.dispatch",
			Payload:   map[string]any{"name": "worker-squ-812", "target": "worker"},
			Source:    "manager",
			LastError: "topology not configured",
			CreatedAt: now.Add(time.Minute),
			UpdatedAt: now.Add(time.Minute),
		},
	} {
		if err := daemon.WriteOutboxItem(teamDir, item); err != nil {
			t.Fatalf("write outbox item %s: %v", item.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview job outbox json: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview job outbox: %v\nbody=%s", err, out.String())
	}
	if overview.OutboxOwner == nil || overview.OutboxOwner.PendingJob != "squ-812" || overview.OutboxOwner.FailedJob != "squ-812" {
		t.Fatalf("overview outbox owner = %+v", overview.OutboxOwner)
	}
	for _, want := range []string{
		"agent-team job outbox squ-812 --state failed",
		"agent-team job outbox squ-812 --state pending",
	} {
		if !stringSliceContains(overview.Actions, want) {
			t.Fatalf("overview actions missing %q: %+v", want, overview.Actions)
		}
	}
	for _, broad := range []string{"agent-team outbox ls --state failed", "agent-team outbox drain --dry-run"} {
		if stringSliceContains(overview.Actions, broad) {
			t.Fatalf("overview should prefer job-scoped action over %q: %+v", broad, overview.Actions)
		}
	}

	next := NewRootCmd()
	nextOut, nextErr := &bytes.Buffer{}, &bytes.Buffer{}
	next.SetOut(nextOut)
	next.SetErr(nextErr)
	next.SetArgs([]string{"next", "--repo", root, "--source", "outbox", "--json"})
	if err := next.Execute(); err != nil {
		t.Fatalf("next job outbox json: %v\nstderr=%s", err, nextErr.String())
	}
	var nextResult nextActionResult
	if err := json.Unmarshal(nextOut.Bytes(), &nextResult); err != nil {
		t.Fatalf("decode next job outbox: %v\nbody=%s", err, nextOut.String())
	}
	if len(nextResult.Actions) != 2 {
		t.Fatalf("next job outbox actions = %+v", nextResult)
	}
	for _, want := range []string{"agent-team job outbox squ-812 --state failed", "agent-team job outbox squ-812 --state pending"} {
		if !stringSliceContains(nextResult.Actions, want) {
			t.Fatalf("next job outbox actions missing %q: %+v", want, nextResult.Actions)
		}
	}
}

func TestOverviewReportsOutboxQuarantineActions(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)
	teamDir := filepath.Join(root, ".agent_team")
	now := time.Date(2026, 6, 27, 19, 0, 0, 0, time.UTC)
	j := &job.Job{
		ID:        "squ-813",
		Ticket:    "SQU-813",
		Target:    "worker",
		Instance:  "worker-squ-813",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	writeQuarantinedOutboxFile(t, teamDir, "20260627T190000.000000000Z", daemon.OutboxStatePending, &daemon.OutboxItem{
		ID:        "outbox-quarantine-overview",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Source:    "manager",
		Payload:   map[string]any{"job_id": "squ-813", "target": "worker"},
		CreatedAt: now,
		UpdatedAt: now,
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview outbox quarantine json: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview outbox quarantine: %v\nbody=%s", err, out.String())
	}
	if overview.OK || overview.State != "attention" {
		t.Fatalf("overview state = ok:%v state:%q", overview.OK, overview.State)
	}
	if overview.OutboxQuarantine.Quarantined != 1 || overview.OutboxQuarantine.Restorable != 1 || overview.OutboxQuarantineOwner != "squ-813" {
		t.Fatalf("outbox quarantine = %+v owner=%q", overview.OutboxQuarantine, overview.OutboxQuarantineOwner)
	}
	if !stringSliceContains(overview.Actions, "agent-team job outbox quarantine squ-813") {
		t.Fatalf("actions missing job-scoped quarantine command: %+v", overview.Actions)
	}
	if stringSliceContains(overview.Actions, "agent-team outbox quarantine ls") {
		t.Fatalf("overview should prefer job-scoped quarantine action: %+v", overview.Actions)
	}
	if detail, ok := findOperatorActionHint(overview.ActionDetails, "agent-team job outbox quarantine squ-813"); !ok || detail.Source != "outbox" || detail.Reason != "quarantined=1" {
		t.Fatalf("outbox quarantine detail = %+v, ok=%v", detail, ok)
	}

	next := NewRootCmd()
	nextOut, nextErr := &bytes.Buffer{}, &bytes.Buffer{}
	next.SetOut(nextOut)
	next.SetErr(nextErr)
	next.SetArgs([]string{"next", "--repo", root, "--source", "outbox", "--reason", "quarantined", "--json"})
	if err := next.Execute(); err != nil {
		t.Fatalf("next outbox quarantine json: %v\nstderr=%s", err, nextErr.String())
	}
	var nextResult nextActionResult
	if err := json.Unmarshal(nextOut.Bytes(), &nextResult); err != nil {
		t.Fatalf("decode next outbox quarantine: %v\nbody=%s", err, nextOut.String())
	}
	for _, want := range []string{
		"agent-team job outbox quarantine squ-813",
		"agent-team job outbox quarantine squ-813 --restorable",
		"agent-team job snapshot squ-813 --json",
	} {
		if !stringSliceContains(nextResult.Actions, want) {
			t.Fatalf("next outbox quarantine actions missing %q: %+v", want, nextResult)
		}
	}
	if nextResult.TotalActions != len(nextResult.Actions) || nextResult.TotalActions != 3 {
		t.Fatalf("next outbox quarantine actions = %+v", nextResult)
	}

	nextAlias := NewRootCmd()
	nextAliasOut, nextAliasErr := &bytes.Buffer{}, &bytes.Buffer{}
	nextAlias.SetOut(nextAliasOut)
	nextAlias.SetErr(nextAliasErr)
	nextAlias.SetArgs([]string{"next", "--repo", root, "--reason", "outbox_quarantined", "--json"})
	if err := nextAlias.Execute(); err != nil {
		t.Fatalf("next outbox quarantine alias json: %v\nstderr=%s", err, nextAliasErr.String())
	}
	var nextAliasResult nextActionResult
	if err := json.Unmarshal(nextAliasOut.Bytes(), &nextAliasResult); err != nil {
		t.Fatalf("decode next outbox quarantine alias: %v\nbody=%s", err, nextAliasOut.String())
	}
	for _, want := range []string{
		"agent-team job outbox quarantine squ-813",
		"agent-team job outbox quarantine squ-813 --restorable",
		"agent-team job snapshot squ-813 --json",
	} {
		if !stringSliceContains(nextAliasResult.Actions, want) {
			t.Fatalf("next outbox quarantine alias actions missing %q: %+v", want, nextAliasResult)
		}
	}
	if nextAliasResult.TotalActions != len(nextAliasResult.Actions) || nextAliasResult.TotalActions != 3 {
		t.Fatalf("next outbox quarantine alias actions = %+v", nextAliasResult)
	}
	if len(nextAliasResult.ActionDetails) != len(nextAliasResult.Actions) {
		t.Fatalf("next outbox quarantine alias details = %+v", nextAliasResult.ActionDetails)
	}
	for _, detail := range nextAliasResult.ActionDetails {
		if detail.Source != "outbox" {
			t.Fatalf("next outbox quarantine alias detail = %+v", detail)
		}
	}

	queueAlias := NewRootCmd()
	queueAliasOut, queueAliasErr := &bytes.Buffer{}, &bytes.Buffer{}
	queueAlias.SetOut(queueAliasOut)
	queueAlias.SetErr(queueAliasErr)
	queueAlias.SetArgs([]string{"next", "--repo", root, "--reason", "queue_quarantined", "--json"})
	if err := queueAlias.Execute(); err != nil {
		t.Fatalf("next queue quarantine alias on outbox fixture: %v\nstderr=%s", err, queueAliasErr.String())
	}
	var queueAliasResult nextActionResult
	if err := json.Unmarshal(queueAliasOut.Bytes(), &queueAliasResult); err != nil {
		t.Fatalf("decode next queue quarantine alias on outbox fixture: %v\nbody=%s", err, queueAliasOut.String())
	}
	if len(queueAliasResult.Actions) != 0 || queueAliasResult.TotalActions != 0 {
		t.Fatalf("queue quarantine alias should not match outbox actions: %+v", queueAliasResult)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"overview", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("overview outbox quarantine text: %v\nstderr=%s", err, textErr.String())
	}
	if !strings.Contains(textOut.String(), "outbox quarantine: quarantined=1 restorable=1 unrestorable=0") {
		t.Fatalf("overview text missing outbox quarantine summary:\n%s", textOut.String())
	}
}

func TestTeamOverviewReportsScopedOutboxActions(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"

[instances.worker]
agent = "worker"

[instances.other]
agent = "other"

[teams.delivery]
instances = ["manager", "worker"]

[teams.platform]
instances = ["other"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 27, 15, 0, 0, 0, time.UTC)
	for _, item := range []*daemon.OutboxItem{
		{
			ID:        "outbox-team-pending",
			State:     daemon.OutboxStatePending,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"target": "worker", "ticket": "SQU-910"},
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "outbox-team-failed",
			State:     daemon.OutboxStateFailed,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"target": "worker", "ticket": "SQU-911"},
			LastError: "route missing",
			CreatedAt: now.Add(time.Minute),
			UpdatedAt: now.Add(time.Minute),
		},
		{
			ID:        "outbox-platform-pending",
			State:     daemon.OutboxStatePending,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"target": "other", "ticket": "OTH-910"},
			CreatedAt: now.Add(2 * time.Minute),
			UpdatedAt: now.Add(2 * time.Minute),
		},
	} {
		if err := daemon.WriteOutboxItem(teamDir, item); err != nil {
			t.Fatalf("write outbox item %s: %v", item.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "overview", "delivery", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team overview outbox json: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode team overview outbox: %v\nbody=%s", err, out.String())
	}
	if overview.Outbox.Total != 2 || overview.Outbox.Pending != 1 || overview.Outbox.Failed != 1 {
		t.Fatalf("team overview outbox = %+v", overview.Outbox)
	}
	for _, want := range []string{
		"agent-team team outbox delivery --state failed",
		"agent-team team outbox delivery --state pending",
	} {
		if !stringSliceContains(overview.Actions, want) {
			t.Fatalf("team overview actions missing %q: %+v", want, overview.Actions)
		}
	}
	if stringSliceContains(overview.Actions, "agent-team team drain delivery") {
		t.Fatalf("team overview should not suggest team drain for outbox-only work: %+v", overview.Actions)
	}
	if detail, ok := findOperatorActionHint(overview.ActionDetails, "agent-team team outbox delivery --state failed"); !ok || detail.Team != "delivery" || detail.Source != "outbox" || detail.Reason != "failed=1" {
		t.Fatalf("team failed outbox detail = %+v, ok=%v", detail, ok)
	}
	if detail, ok := findOperatorActionHint(overview.ActionDetails, "agent-team team outbox delivery --state pending"); !ok || detail.Team != "delivery" || detail.Source != "outbox" || detail.Reason != "pending=1" {
		t.Fatalf("team pending outbox detail = %+v, ok=%v", detail, ok)
	}

	next := NewRootCmd()
	nextOut, nextErr := &bytes.Buffer{}, &bytes.Buffer{}
	next.SetOut(nextOut)
	next.SetErr(nextErr)
	next.SetArgs([]string{"team", "next", "delivery", "--repo", root, "--source", "outbox", "--json"})
	if err := next.Execute(); err != nil {
		t.Fatalf("team next outbox json: %v\nstderr=%s", err, nextErr.String())
	}
	var nextResult nextActionResult
	if err := json.Unmarshal(nextOut.Bytes(), &nextResult); err != nil {
		t.Fatalf("decode team next outbox: %v\nbody=%s", err, nextOut.String())
	}
	if len(nextResult.Actions) != 2 {
		t.Fatalf("team next outbox actions = %+v", nextResult)
	}
	for _, want := range []string{"agent-team team outbox delivery --state failed", "agent-team team outbox delivery --state pending"} {
		if !stringSliceContains(nextResult.Actions, want) {
			t.Fatalf("team next outbox actions missing %q: %+v", want, nextResult.Actions)
		}
	}
	for _, detail := range nextResult.ActionDetails {
		if detail.Team != "delivery" || detail.Source != "outbox" {
			t.Fatalf("team next outbox detail = %+v", detail)
		}
	}
}

func TestTeamOverviewReportsScopedOutboxQuarantineActions(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"

[instances.worker]
agent = "worker"

[instances.other]
agent = "other"

[teams.delivery]
instances = ["manager", "worker"]

[teams.platform]
instances = ["other"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 27, 20, 0, 0, 0, time.UTC)
	writeQuarantinedOutboxFile(t, teamDir, "20260627T200000.000000000Z", daemon.OutboxStatePending, &daemon.OutboxItem{
		ID:        "outbox-delivery-quarantine-overview",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Source:    "manager",
		Payload:   map[string]any{"target": "worker", "ticket": "SQU-914"},
		CreatedAt: now,
		UpdatedAt: now,
	})
	writeQuarantinedOutboxFile(t, teamDir, "20260627T200000.000000000Z", daemon.OutboxStatePending, &daemon.OutboxItem{
		ID:        "outbox-platform-quarantine-overview",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Source:    "manager",
		Payload:   map[string]any{"target": "other", "ticket": "OTH-914"},
		CreatedAt: now,
		UpdatedAt: now,
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "overview", "delivery", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team overview outbox quarantine json: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode team overview outbox quarantine: %v\nbody=%s", err, out.String())
	}
	if overview.OutboxQuarantine.Quarantined != 1 || overview.OutboxQuarantine.Restorable != 1 {
		t.Fatalf("team outbox quarantine = %+v", overview.OutboxQuarantine)
	}
	if !stringSliceContains(overview.Actions, "agent-team team outbox quarantine delivery") {
		t.Fatalf("team actions missing quarantine command: %+v", overview.Actions)
	}
	if detail, ok := findOperatorActionHint(overview.ActionDetails, "agent-team team outbox quarantine delivery"); !ok || detail.Team != "delivery" || detail.Source != "outbox" || detail.Reason != "quarantined=1" {
		t.Fatalf("team outbox quarantine detail = %+v, ok=%v", detail, ok)
	}
	if strings.Contains(out.String(), "outbox-platform-quarantine-overview") {
		t.Fatalf("team overview leaked platform quarantine:\n%s", out.String())
	}

	next := NewRootCmd()
	nextOut, nextErr := &bytes.Buffer{}, &bytes.Buffer{}
	next.SetOut(nextOut)
	next.SetErr(nextErr)
	next.SetArgs([]string{"team", "next", "delivery", "--repo", root, "--source", "outbox", "--reason", "quarantined", "--json"})
	if err := next.Execute(); err != nil {
		t.Fatalf("team next outbox quarantine json: %v\nstderr=%s", err, nextErr.String())
	}
	var nextResult nextActionResult
	if err := json.Unmarshal(nextOut.Bytes(), &nextResult); err != nil {
		t.Fatalf("decode team next outbox quarantine: %v\nbody=%s", err, nextOut.String())
	}
	for _, want := range []string{
		"agent-team team outbox quarantine delivery",
		"agent-team team outbox quarantine delivery --restorable",
		"agent-team team snapshot delivery --json",
	} {
		if !stringSliceContains(nextResult.Actions, want) {
			t.Fatalf("team next outbox quarantine actions missing %q: %+v", want, nextResult)
		}
	}
	if nextResult.TotalActions != len(nextResult.Actions) || nextResult.TotalActions != 3 {
		t.Fatalf("team next outbox quarantine actions = %+v", nextResult)
	}
}

func TestTeamOverviewScopesUnreadInboxActions(t *testing.T) {
	root := writeOverviewAttentionFixture(t)
	teamDir := filepath.Join(root, ".agent_team")
	daemonRoot := daemon.DaemonRoot(teamDir)
	for _, target := range []string{"manager", "outsider"} {
		if err := daemon.AppendMessage(daemonRoot, target, &daemon.Message{
			ID:   "msg-" + target,
			From: "tester",
			Body: "hello " + target,
		}); err != nil {
			t.Fatalf("append %s: %v", target, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "overview", "delivery", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team overview unread inbox: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode team overview unread inbox: %v\nbody=%s", err, out.String())
	}
	if overview.Inbox.Total != 1 || overview.Inbox.Unread != 1 || !stringSliceContains(overview.Inbox.UnreadNames, "manager") || stringSliceContains(overview.Inbox.UnreadNames, "outsider") {
		t.Fatalf("team inbox summary = %+v", overview.Inbox)
	}
	action := "agent-team inbox ls --team delivery --unread"
	if !stringSliceContains(overview.Actions, action) {
		t.Fatalf("team actions missing inbox unread command: %+v", overview.Actions)
	}
	if detail, ok := findOperatorActionHint(overview.ActionDetails, action); !ok || detail.Team != "delivery" || detail.Source != "inbox" || detail.Reason != "unread=1" {
		t.Fatalf("team inbox detail = %+v, ok=%v", detail, ok)
	}
}

func TestOverviewRecommendsParallelReadyFanout(t *testing.T) {
	root := writeOverviewParallelReadyFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview parallel ready: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview parallel ready: %v\nbody=%s", err, out.String())
	}
	if overview.Pipelines.ParallelReadySteps != 2 || !stringSliceContains(overview.Actions, "agent-team tick --all-ready-steps --dry-run --preview-routes") {
		t.Fatalf("overview parallel actions = %+v pipelines=%+v", overview.Actions, overview.Pipelines)
	}
	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"overview", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("overview parallel text: %v\nstderr=%s", err, textErr.String())
	}
	if !strings.Contains(textOut.String(), "pipelines: total=1 jobs=1 ready_steps=1 parallel_ready_steps=2") {
		t.Fatalf("overview parallel text missing fanout count:\n%s", textOut.String())
	}

	team := NewRootCmd()
	teamOut, teamErr := &bytes.Buffer{}, &bytes.Buffer{}
	team.SetOut(teamOut)
	team.SetErr(teamErr)
	team.SetArgs([]string{"team", "overview", "delivery", "--repo", root, "--json"})
	if err := team.Execute(); err != nil {
		t.Fatalf("team overview parallel ready: %v\nstderr=%s", err, teamErr.String())
	}
	var teamOverview overviewResult
	if err := json.Unmarshal(teamOut.Bytes(), &teamOverview); err != nil {
		t.Fatalf("decode team overview parallel ready: %v\nbody=%s", err, teamOut.String())
	}
	if teamOverview.Pipelines.ParallelReadySteps != 2 || !stringSliceContains(teamOverview.Actions, "agent-team team tick delivery --all-ready-steps --dry-run --preview-routes") {
		t.Fatalf("team overview parallel actions = %+v pipelines=%+v", teamOverview.Actions, teamOverview.Pipelines)
	}
}

func TestOverviewRecommendsStaleRunningJobTimeoutRepair(t *testing.T) {
	root := writeOverviewStaleRunningFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview stale running json: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview stale running: %v\nbody=%s", err, out.String())
	}
	if overview.Jobs.StaleRunning != 1 || !stringSliceContains(overview.Actions, "agent-team repair --timeout-jobs --dry-run") {
		t.Fatalf("overview stale running jobs = %+v actions=%+v", overview.Jobs, overview.Actions)
	}
	if detail, ok := findOperatorActionHint(overview.ActionDetails, "agent-team repair --timeout-jobs --dry-run"); !ok || detail.Source != "jobs" || detail.Reason != "stale_running=1" {
		t.Fatalf("stale running action detail = %+v ok=%v", detail, ok)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"overview", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("overview stale running text: %v\nstderr=%s", err, textErr.String())
	}
	if !strings.Contains(textOut.String(), "stale_running=1") {
		t.Fatalf("overview text missing stale running count:\n%s", textOut.String())
	}

	team := NewRootCmd()
	teamOut, teamErr := &bytes.Buffer{}, &bytes.Buffer{}
	team.SetOut(teamOut)
	team.SetErr(teamErr)
	team.SetArgs([]string{"team", "overview", "delivery", "--repo", root, "--json"})
	if err := team.Execute(); err != nil {
		t.Fatalf("team overview stale running json: %v\nstderr=%s", err, teamErr.String())
	}
	var teamOverview overviewResult
	if err := json.Unmarshal(teamOut.Bytes(), &teamOverview); err != nil {
		t.Fatalf("decode team overview stale running: %v\nbody=%s", err, teamOut.String())
	}
	if teamOverview.Jobs.StaleRunning != 1 || !stringSliceContains(teamOverview.Actions, "agent-team team repair delivery --timeout-jobs --dry-run") {
		t.Fatalf("team overview stale running jobs = %+v actions=%+v", teamOverview.Jobs, teamOverview.Actions)
	}
}

func TestOverviewReportsRuntimeResumePlanActions(t *testing.T) {
	root := writeOverviewRuntimeFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview runtime json: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview runtime: %v\nbody=%s", err, out.String())
	}
	if overview.Runtime.Total != 4 || overview.Runtime.Crashed != 3 || overview.Runtime.Exited != 1 {
		t.Fatalf("runtime summary = %+v", overview.Runtime)
	}
	if overview.Runtime.Running != 0 || overview.Runtime.Stalled != 0 || overview.Runtime.QueuedOnCapacity != 0 {
		t.Fatalf("runtime live/queued summary = %+v", overview.Runtime)
	}
	if overview.Runtime.ManagedResume != 4 || overview.Runtime.CanManagedResume != 2 || overview.Runtime.DirectResume != 2 {
		t.Fatalf("runtime resume capability summary = %+v", overview.Runtime)
	}
	if !stringSliceContains(overview.Actions, "agent-team resume-plan --status crashed --sort action --limit 10") {
		t.Fatalf("actions missing runtime resume plan: %+v", overview.Actions)
	}
	if detail, ok := findOperatorActionHint(overview.ActionDetails, "agent-team resume-plan --status crashed --sort action --limit 10"); !ok || detail.Source != "runtime" || detail.Reason != "crashed=3" {
		t.Fatalf("runtime action detail = %+v ok=%v", detail, ok)
	}

	lastMessage := NewRootCmd()
	lastMessageOut, lastMessageErr := &bytes.Buffer{}, &bytes.Buffer{}
	lastMessage.SetOut(lastMessageOut)
	lastMessage.SetErr(lastMessageErr)
	lastMessage.SetArgs([]string{"overview", "--repo", root, "--last-message", "--json"})
	if err := lastMessage.Execute(); err != nil {
		t.Fatalf("overview runtime last-message json: %v\nstderr=%s", err, lastMessageErr.String())
	}
	var lastMessageOverview overviewResult
	if err := json.Unmarshal(lastMessageOut.Bytes(), &lastMessageOverview); err != nil {
		t.Fatalf("decode overview runtime last-message: %v\nbody=%s", err, lastMessageOut.String())
	}
	lastAction := "agent-team resume-plan --status crashed --sort action --limit 10 --last-message"
	if !stringSliceContains(lastMessageOverview.Actions, lastAction) {
		t.Fatalf("last-message actions missing runtime resume plan: %+v", lastMessageOverview.Actions)
	}
	if detail, ok := findOperatorActionHint(lastMessageOverview.ActionDetails, lastAction); !ok || detail.Source != "runtime" || detail.Reason != "crashed=3" {
		t.Fatalf("last-message runtime action detail = %+v ok=%v", detail, ok)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"overview", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("overview runtime text: %v\nstderr=%s", err, textErr.String())
	}
	if !strings.Contains(textOut.String(), "runtime: total=4 running=0 stopped=0 exited=1 crashed=3 unknown=0") {
		t.Fatalf("overview runtime text missing summary:\n%s", textOut.String())
	}
	if !strings.Contains(textOut.String(), "managed_resume=4 can_managed_resume=2 direct_resume=2") {
		t.Fatalf("overview runtime text missing resume capability summary:\n%s", textOut.String())
	}

	team := NewRootCmd()
	teamOut, teamErr := &bytes.Buffer{}, &bytes.Buffer{}
	team.SetOut(teamOut)
	team.SetErr(teamErr)
	team.SetArgs([]string{"team", "overview", "delivery", "--repo", root, "--json"})
	if err := team.Execute(); err != nil {
		t.Fatalf("team overview runtime json: %v\nstderr=%s", err, teamErr.String())
	}
	var teamOverview overviewResult
	if err := json.Unmarshal(teamOut.Bytes(), &teamOverview); err != nil {
		t.Fatalf("decode team overview runtime: %v\nbody=%s", err, teamOut.String())
	}
	if teamOverview.Runtime.Total != 3 || teamOverview.Runtime.Crashed != 2 || teamOverview.Runtime.Exited != 1 {
		t.Fatalf("team runtime summary = %+v", teamOverview.Runtime)
	}
	if teamOverview.Runtime.ManagedResume != 3 || teamOverview.Runtime.CanManagedResume != 2 || teamOverview.Runtime.DirectResume != 2 {
		t.Fatalf("team runtime resume capability summary = %+v", teamOverview.Runtime)
	}
	if !stringSliceContains(teamOverview.Actions, "agent-team team resume-plan delivery --status crashed --sort action --limit 10") {
		t.Fatalf("team actions missing scoped runtime resume plan: %+v", teamOverview.Actions)
	}

	teamLastMessage := NewRootCmd()
	teamLastMessageOut, teamLastMessageErr := &bytes.Buffer{}, &bytes.Buffer{}
	teamLastMessage.SetOut(teamLastMessageOut)
	teamLastMessage.SetErr(teamLastMessageErr)
	teamLastMessage.SetArgs([]string{"team", "overview", "delivery", "--repo", root, "--last-message", "--json"})
	if err := teamLastMessage.Execute(); err != nil {
		t.Fatalf("team overview runtime last-message json: %v\nstderr=%s", err, teamLastMessageErr.String())
	}
	var teamLastMessageOverview overviewResult
	if err := json.Unmarshal(teamLastMessageOut.Bytes(), &teamLastMessageOverview); err != nil {
		t.Fatalf("decode team overview runtime last-message: %v\nbody=%s", err, teamLastMessageOut.String())
	}
	teamLastAction := "agent-team team resume-plan delivery --status crashed --sort action --limit 10 --last-message"
	if !stringSliceContains(teamLastMessageOverview.Actions, teamLastAction) {
		t.Fatalf("team last-message actions missing scoped runtime resume plan: %+v", teamLastMessageOverview.Actions)
	}
	if detail, ok := findOperatorActionHint(teamLastMessageOverview.ActionDetails, teamLastAction); !ok || detail.Team != "delivery" || detail.Source != "runtime" || detail.Reason != "crashed=2" {
		t.Fatalf("team last-message runtime action detail = %+v ok=%v", detail, ok)
	}
}

func TestOverviewReportsPipelineRuntimeResumePlanActions(t *testing.T) {
	root := writeOverviewPipelineRuntimeFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview pipeline runtime json: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview pipeline runtime: %v\nbody=%s", err, out.String())
	}
	if overview.Runtime.Crashed != 2 || strings.Join(overview.Runtime.CrashedPipelines, ",") != "ops_review,ticket_to_pr" {
		t.Fatalf("pipeline runtime summary = %+v", overview.Runtime)
	}
	if !stringSliceContains(overview.Actions, "agent-team pipeline resume-plan --all --status crashed --sort action --limit 10") {
		t.Fatalf("actions missing all-pipeline runtime resume plan: %+v", overview.Actions)
	}
	if stringSliceContains(overview.Actions, "agent-team resume-plan --status crashed") {
		t.Fatalf("actions should prefer pipeline runtime resume plan: %+v", overview.Actions)
	}
	if detail, ok := findOperatorActionHint(overview.ActionDetails, "agent-team pipeline resume-plan --all --status crashed --sort action --limit 10"); !ok || detail.Source != "runtime" || detail.Reason != "crashed=2" {
		t.Fatalf("pipeline runtime action detail = %+v ok=%v", detail, ok)
	}

	next := NewRootCmd()
	nextOut, nextErr := &bytes.Buffer{}, &bytes.Buffer{}
	next.SetOut(nextOut)
	next.SetErr(nextErr)
	next.SetArgs([]string{"next", "--repo", root, "--source", "runtime", "--json"})
	if err := next.Execute(); err != nil {
		t.Fatalf("next pipeline runtime json: %v\nstderr=%s", err, nextErr.String())
	}
	var result nextActionResult
	if err := json.Unmarshal(nextOut.Bytes(), &result); err != nil {
		t.Fatalf("decode next pipeline runtime: %v\nbody=%s", err, nextOut.String())
	}
	if len(result.Actions) != 1 || result.Actions[0] != "agent-team pipeline resume-plan --all --status crashed --sort action --limit 10" {
		t.Fatalf("pipeline runtime next = %+v", result)
	}
}

func TestOverviewReportsStaleRuntimeResumePlanActions(t *testing.T) {
	root := writeOverviewRuntimeFixture(t)
	teamDir := filepath.Join(root, ".agent_team")
	restorePIDLiveCheck := daemon.SetPidLiveCheckForTest(func(pid int) bool {
		return pid != 4242
	})
	t.Cleanup(restorePIDLiveCheck)
	now := time.Now().UTC()
	for _, meta := range []*daemon.Metadata{
		{Instance: "worker-squ-902", Agent: "worker", Status: daemon.StatusRunning, Runtime: "claude", RuntimeBinary: "claude", PID: 4242, SessionID: "team-stale-session", StartedAt: now.Add(-15 * time.Minute)},
		{Instance: "support-stale", Agent: "support", Status: daemon.StatusRunning, Runtime: "claude", RuntimeBinary: "claude", PID: 4242, SessionID: "foreign-stale-session", StartedAt: now.Add(-10 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview stale runtime json: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview stale runtime: %v\nbody=%s", err, out.String())
	}
	if overview.Runtime.StaleRunning != 2 || !stringSliceContains(overview.Runtime.StaleInstances, "support-stale") || !stringSliceContains(overview.Runtime.StaleInstances, "worker-squ-902") {
		t.Fatalf("runtime stale summary = %+v", overview.Runtime)
	}
	if overview.Runtime.Running != 0 || overview.Runtime.Stalled != 2 || !stringSliceContains(overview.Runtime.StalledInstances, "support-stale") || !stringSliceContains(overview.Runtime.StalledInstances, "worker-squ-902") {
		t.Fatalf("runtime stalled summary = %+v", overview.Runtime)
	}
	if !stringSliceContains(overview.Actions, "agent-team resume-plan --runtime-stale --sort stale --limit 10") {
		t.Fatalf("actions missing stale runtime resume plan: %+v", overview.Actions)
	}
	if detail, ok := findOperatorActionHint(overview.ActionDetails, "agent-team resume-plan --runtime-stale --sort stale --limit 10"); !ok || detail.Source != "runtime" || detail.Reason != "stale=2" {
		t.Fatalf("stale runtime action detail = %+v ok=%v", detail, ok)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"overview", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("overview stale runtime text: %v\nstderr=%s", err, textErr.String())
	}
	if !strings.Contains(textOut.String(), "stale_running=2") {
		t.Fatalf("overview stale runtime text missing count:\n%s", textOut.String())
	}
	if !strings.Contains(textOut.String(), "runtime stalled: 2 (marked running, process gone)") {
		t.Fatalf("overview stale runtime text missing stalled line:\n%s", textOut.String())
	}

	team := NewRootCmd()
	teamOut, teamErr := &bytes.Buffer{}, &bytes.Buffer{}
	team.SetOut(teamOut)
	team.SetErr(teamErr)
	team.SetArgs([]string{"team", "overview", "delivery", "--repo", root, "--json"})
	if err := team.Execute(); err != nil {
		t.Fatalf("team overview stale runtime json: %v\nstderr=%s", err, teamErr.String())
	}
	var teamOverview overviewResult
	if err := json.Unmarshal(teamOut.Bytes(), &teamOverview); err != nil {
		t.Fatalf("decode team overview stale runtime: %v\nbody=%s", err, teamOut.String())
	}
	if teamOverview.Runtime.StaleRunning != 1 || !stringSliceContains(teamOverview.Actions, "agent-team team resume-plan delivery --runtime-stale --sort stale --limit 10") {
		t.Fatalf("team stale runtime summary = %+v actions=%+v", teamOverview.Runtime, teamOverview.Actions)
	}
	if teamOverview.Runtime.Running != 0 || teamOverview.Runtime.Stalled != 1 {
		t.Fatalf("team stalled runtime summary = %+v", teamOverview.Runtime)
	}
	if detail, ok := findOperatorActionHint(teamOverview.ActionDetails, "agent-team team resume-plan delivery --runtime-stale --sort stale --limit 10"); !ok || detail.Team != "delivery" || detail.Source != "runtime" || detail.Reason != "stale=1" {
		t.Fatalf("team stale runtime action detail = %+v ok=%v", detail, ok)
	}
}

func TestOverviewReportsQueuedOnCapacityRuntime(t *testing.T) {
	root := writeOverviewQueuedRuntimeFixture(t, 3)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview queued runtime json: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview queued runtime: %v\nbody=%s", err, out.String())
	}
	if overview.Runtime.Total != 0 || overview.Runtime.Running != 0 || overview.Runtime.Stalled != 0 || overview.Runtime.QueuedOnCapacity != 3 {
		t.Fatalf("queued runtime summary = %+v", overview.Runtime)
	}
	if got := overview.Runtime.QueuedOnCapacityByInstance["reviewer"]; got != 3 {
		t.Fatalf("queued by instance = %+v, want reviewer=3", overview.Runtime.QueuedOnCapacityByInstance)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"overview", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("overview queued runtime text: %v\nstderr=%s", err, textErr.String())
	}
	body := textOut.String()
	if !strings.Contains(body, "runtime: total=0 running=0 stopped=0 exited=0 crashed=0 unknown=0 queued_on_capacity=3 stalled=0") {
		t.Fatalf("overview queued runtime text missing machine counts:\n%s", body)
	}
	if !strings.Contains(body, "runtime capacity: 0 running, 3 queued (waiting on reviewer slot)") {
		t.Fatalf("overview queued runtime text missing reviewer-slot line:\n%s", body)
	}
	if strings.Contains(body, "runtime stalled:") {
		t.Fatalf("queued runtime should not render as stalled:\n%s", body)
	}
}

func TestOverviewRuntimeIdleZeroQueuedIsDistinct(t *testing.T) {
	root := writeOverviewQueuedRuntimeFixture(t, 0)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview idle runtime text: %v\nstderr=%s", err, stderr.String())
	}
	body := out.String()
	if !strings.Contains(body, "runtime capacity: 0 running, 0 queued") {
		t.Fatalf("overview idle runtime text missing zero-queued line:\n%s", body)
	}
	if strings.Contains(body, "waiting on reviewer slot") || strings.Contains(body, "runtime stalled:") {
		t.Fatalf("idle runtime should be distinct from queued or stalled:\n%s", body)
	}
}

func TestOverviewReportsPipelineStaleRuntimeResumePlanActions(t *testing.T) {
	root := writeOverviewPipelineStaleRuntimeFixture(t)
	restorePIDLiveCheck := daemon.SetPidLiveCheckForTest(func(pid int) bool {
		return pid != 4242
	})
	t.Cleanup(restorePIDLiveCheck)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview pipeline stale runtime json: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview pipeline stale runtime: %v\nbody=%s", err, out.String())
	}
	if overview.Runtime.StaleRunning != 2 || strings.Join(overview.Runtime.StalePipelines, ",") != "ticket_to_pr" {
		t.Fatalf("pipeline stale runtime summary = %+v", overview.Runtime)
	}
	if !stringSliceContains(overview.Actions, "agent-team pipeline resume-plan ticket_to_pr --runtime-stale --sort stale --limit 10") {
		t.Fatalf("actions missing pipeline stale runtime resume plan: %+v", overview.Actions)
	}
	if stringSliceContains(overview.Actions, "agent-team resume-plan --runtime-stale") {
		t.Fatalf("actions should prefer pipeline stale runtime resume plan: %+v", overview.Actions)
	}

	next := NewRootCmd()
	nextOut, nextErr := &bytes.Buffer{}, &bytes.Buffer{}
	next.SetOut(nextOut)
	next.SetErr(nextErr)
	next.SetArgs([]string{"next", "--repo", root, "--source", "runtime", "--reason", "stale", "--json"})
	if err := next.Execute(); err != nil {
		t.Fatalf("next pipeline stale runtime json: %v\nstderr=%s", err, nextErr.String())
	}
	var result nextActionResult
	if err := json.Unmarshal(nextOut.Bytes(), &result); err != nil {
		t.Fatalf("decode next pipeline stale runtime: %v\nbody=%s", err, nextOut.String())
	}
	if len(result.Actions) != 1 || result.Actions[0] != "agent-team pipeline resume-plan ticket_to_pr --runtime-stale --sort stale --limit 10" {
		t.Fatalf("pipeline stale runtime next = %+v", result)
	}
}

func TestOverviewTextRendersOperatorSummary(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview text: %v\nstderr=%s", err, stderr.String())
	}

	for _, want := range []string{
		"overview: attention",
		"health: unhealthy daemon=down",
		"topology: instances=2 persistent=1 ephemeral=1",
		"jobs: active=1 (queued=0 running=0 blocked=1) terminal=0 (done=0 failed=0) total=1 attention=1",
		"queue: total=1 pending=0 dead=1",
		"pipelines: total=1 jobs=1 ready_steps=1 parallel_ready_steps=0 stale_running_steps=0 blocked_steps=0 failed_steps=0",
		"schedules: declared=1 due=1 upcoming=1",
		"actions:",
		"agent-team repair --dry-run --jobs",
		"agent-team job queue quarantine squ-700",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("overview text missing %q:\n%s", want, out.String())
		}
	}
}

func TestOverviewJobSummarySeparatesActiveAndTerminalJobs(t *testing.T) {
	root := writeOverviewJobSummaryFixture(t, map[string]job.Status{
		"SQU-720": job.StatusQueued,
		"SQU-721": job.StatusRunning,
		"SQU-722": job.StatusBlocked,
		"SQU-723": job.StatusDone,
		"SQU-724": job.StatusFailed,
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview text: %v\nstderr=%s", err, stderr.String())
	}
	wantLine := "jobs: active=3 (queued=1 running=1 blocked=1) terminal=2 (done=1 failed=1) total=5"
	if !strings.Contains(out.String(), wantLine) {
		t.Fatalf("overview text missing %q:\n%s", wantLine, out.String())
	}
	if strings.Contains(out.String(), "jobs: total=5") {
		t.Fatalf("overview text still leads with total:\n%s", out.String())
	}

	jsonCmd := NewRootCmd()
	jsonOut, jsonErr := &bytes.Buffer{}, &bytes.Buffer{}
	jsonCmd.SetOut(jsonOut)
	jsonCmd.SetErr(jsonErr)
	jsonCmd.SetArgs([]string{"overview", "--repo", root, "--json"})
	if err := jsonCmd.Execute(); err != nil {
		t.Fatalf("overview json: %v\nstderr=%s", err, jsonErr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(jsonOut.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview json: %v\nbody=%s", err, jsonOut.String())
	}
	if overview.Jobs.Summary.Total != 5 || overview.Jobs.Summary.Active != 3 || overview.Jobs.Summary.Terminal != 2 {
		t.Fatalf("overview job summary = %+v", overview.Jobs.Summary)
	}
}

func TestOverviewJobSummaryRendersTerminalOnlyAsZeroActive(t *testing.T) {
	root := writeOverviewJobSummaryFixture(t, map[string]job.Status{
		"SQU-725": job.StatusDone,
		"SQU-726": job.StatusFailed,
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview text: %v\nstderr=%s", err, stderr.String())
	}
	wantLine := "jobs: active=0 (queued=0 running=0 blocked=0) terminal=2 (done=1 failed=1) total=2"
	if !strings.Contains(out.String(), wantLine) {
		t.Fatalf("overview terminal-only text missing %q:\n%s", wantLine, out.String())
	}
	if strings.Contains(out.String(), "jobs: total=2") {
		t.Fatalf("overview terminal-only text still leads with total:\n%s", out.String())
	}

	jsonCmd := NewRootCmd()
	jsonOut, jsonErr := &bytes.Buffer{}, &bytes.Buffer{}
	jsonCmd.SetOut(jsonOut)
	jsonCmd.SetErr(jsonErr)
	jsonCmd.SetArgs([]string{"overview", "--repo", root, "--json"})
	if err := jsonCmd.Execute(); err != nil {
		t.Fatalf("overview terminal-only json: %v\nstderr=%s", err, jsonErr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(jsonOut.Bytes(), &overview); err != nil {
		t.Fatalf("decode terminal-only overview json: %v\nbody=%s", err, jsonOut.String())
	}
	if overview.Jobs.Summary.Total != 2 || overview.Jobs.Summary.Active != 0 || overview.Jobs.Summary.Terminal != 2 {
		t.Fatalf("terminal-only overview job summary = %+v", overview.Jobs.Summary)
	}
}

func TestOverviewCommandFormat(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root, "--format", "{{.State}} {{.Jobs.Summary.Total}} {{.Queue.Dead}} {{len .Actions}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview format: %v\nstderr=%s", err, stderr.String())
	}
	if got := out.String(); !strings.HasPrefix(got, "attention 1 1 ") {
		t.Fatalf("overview format output = %q", got)
	}
}

func TestOverviewReportsIntakeErrors(t *testing.T) {
	root := writeIntakeErrorFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview intake json: %v\nstderr=%s", err, stderr.String())
	}

	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview intake: %v\nbody=%s", err, out.String())
	}
	if overview.OK || overview.State != "attention" {
		t.Fatalf("overview state = ok:%v state:%q", overview.OK, overview.State)
	}
	if overview.Intake.Deliveries != 1 || overview.Intake.Errors != 1 || overview.Intake.Replayable != 1 || overview.Intake.LatestErrorID != "intake-failed" {
		t.Fatalf("intake summary = %+v", overview.Intake)
	}
	for _, want := range []string{
		"agent-team intake summary",
		"agent-team intake deliveries --status error",
		"agent-team intake replay --all --dedupe-request-id --dry-run --preview-triggers",
	} {
		if !stringSliceContains(overview.Actions, want) {
			t.Fatalf("actions missing %q: %+v", want, overview.Actions)
		}
	}
}

func TestOverviewReportsIntakeDuplicateRequestIDs(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, delivery := range []intakeDelivery{
		{
			ID:         "first",
			Time:       time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC),
			Provider:   "github",
			RequestID:  "github-delivery-1",
			Status:     intakeDeliveryStatusOK,
			HTTPStatus: 200,
			EventType:  "pr.opened",
		},
		{
			ID:         "second",
			Time:       time.Date(2026, 6, 19, 12, 1, 0, 0, time.UTC),
			Provider:   "github",
			RequestID:  "github-delivery-1",
			Status:     intakeDeliveryStatusOK,
			HTTPStatus: 200,
			EventType:  "pr.opened",
		},
	} {
		if err := appendIntakeDelivery(teamDir, delivery); err != nil {
			t.Fatalf("append %s: %v", delivery.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview duplicate intake json: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview duplicate intake: %v\nbody=%s", err, out.String())
	}
	if overview.OK || overview.State != "attention" || overview.Intake.Errors != 0 || overview.Intake.DuplicateRequestIDs != 1 {
		t.Fatalf("overview duplicate state/intake = ok:%v state:%q intake:%+v", overview.OK, overview.State, overview.Intake)
	}
	if !stringSliceContains(overview.Actions, "agent-team intake duplicates") {
		t.Fatalf("actions missing intake duplicates: %+v", overview.Actions)
	}
	var sawDetail bool
	for _, detail := range overview.ActionDetails {
		if detail.Command == "agent-team intake duplicates" && detail.Source == "intake" && detail.Reason == "duplicate_request_ids=1" {
			sawDetail = true
			break
		}
	}
	if !sawDetail {
		t.Fatalf("action details missing duplicate intake detail: %+v", overview.ActionDetails)
	}
}

func TestOverviewRecommendsBatchCleanupReadyJobs(t *testing.T) {
	root := writeOverviewCleanupFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview cleanup json: %v\nstderr=%s", err, stderr.String())
	}

	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview cleanup: %v\nbody=%s", err, out.String())
	}
	if overview.Jobs.Summary.Total != 1 || overview.Jobs.Summary.Active != 0 || overview.Jobs.Summary.Terminal != 1 || overview.Jobs.Summary.Done != 1 || overview.Jobs.Attention != 1 || overview.Jobs.CleanupReady != 1 {
		t.Fatalf("jobs = %+v", overview.Jobs)
	}
	for _, want := range []string{
		"agent-team job triage",
		"agent-team job cleanup --all --dry-run",
	} {
		if !stringSliceContains(overview.Actions, want) {
			t.Fatalf("actions missing %q: %+v", want, overview.Actions)
		}
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"overview", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("overview cleanup text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{
		"cleanup_ready=1",
		"agent-team job cleanup --all --dry-run",
	} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("overview cleanup text missing %q:\n%s", want, textOut.String())
		}
	}
}

func TestOverviewRecommendsExpiredHoldRelease(t *testing.T) {
	root := writeOverviewExpiredHoldFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview expired holds: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview expired holds: %v\nbody=%s", err, out.String())
	}
	if overview.Jobs.ExpiredHolds != 1 || overview.Jobs.Summary.ExpiredHeld != 1 {
		t.Fatalf("overview jobs = %+v", overview.Jobs)
	}
	if !stringSliceContains(overview.Actions, "agent-team job release --all --expired --dry-run") {
		t.Fatalf("actions missing expired release: %+v", overview.Actions)
	}
	if detail, ok := findOperatorActionHint(overview.ActionDetails, "agent-team job release --all --expired --dry-run"); !ok || detail.Source != "jobs" || detail.Reason != "expired_holds=1" {
		t.Fatalf("expired release detail = %+v ok=%v", detail, ok)
	}

	next := NewRootCmd()
	nextOut, nextErr := &bytes.Buffer{}, &bytes.Buffer{}
	next.SetOut(nextOut)
	next.SetErr(nextErr)
	next.SetArgs([]string{"next", "--repo", root, "--reason", "expired_holds", "--json"})
	if err := next.Execute(); err != nil {
		t.Fatalf("next expired holds: %v\nstderr=%s", err, nextErr.String())
	}
	var nextResult nextActionResult
	if err := json.Unmarshal(nextOut.Bytes(), &nextResult); err != nil {
		t.Fatalf("decode next expired holds: %v\nbody=%s", err, nextOut.String())
	}
	if len(nextResult.Actions) != 1 || nextResult.Actions[0] != "agent-team job release --all --expired --dry-run" {
		t.Fatalf("next expired holds = %+v", nextResult)
	}

	team := NewRootCmd()
	teamOut, teamErr := &bytes.Buffer{}, &bytes.Buffer{}
	team.SetOut(teamOut)
	team.SetErr(teamErr)
	team.SetArgs([]string{"team", "overview", "delivery", "--repo", root, "--json"})
	if err := team.Execute(); err != nil {
		t.Fatalf("team overview expired holds: %v\nstderr=%s", err, teamErr.String())
	}
	var teamOverview overviewResult
	if err := json.Unmarshal(teamOut.Bytes(), &teamOverview); err != nil {
		t.Fatalf("decode team overview expired holds: %v\nbody=%s", err, teamOut.String())
	}
	if teamOverview.Jobs.ExpiredHolds != 1 || !stringSliceContains(teamOverview.Actions, "agent-team team release delivery --expired --dry-run") {
		t.Fatalf("team overview expired holds = %+v actions=%+v", teamOverview.Jobs, teamOverview.Actions)
	}
}

func TestOverviewRecommendsIntakeDoctorForLedgerParseErrors(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(filepath.Join(teamDir, "daemon"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(intakeDeliveryLogPath(teamDir), []byte("{\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview corrupt intake json: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview corrupt intake: %v\nbody=%s", err, out.String())
	}
	if overview.OK || overview.SectionErrors["intake"] == "" {
		t.Fatalf("overview = %+v", overview)
	}
	if !stringSliceContains(overview.Actions, "agent-team intake doctor") {
		t.Fatalf("actions missing intake doctor: %+v", overview.Actions)
	}
}

func TestOverviewRecommendsQueueDoctorForQueueParseErrors(t *testing.T) {
	root := writeOverviewCorruptQueueFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview corrupt queue json: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview corrupt queue: %v\nbody=%s", err, out.String())
	}
	if overview.OK || !overviewHasQueueSectionError(&overview) {
		t.Fatalf("overview = %+v", overview)
	}
	for _, want := range []string{
		"agent-team queue doctor",
		"agent-team snapshot --json",
	} {
		if !stringSliceContains(overview.Actions, want) {
			t.Fatalf("actions missing %q: %+v", want, overview.Actions)
		}
	}
}

func TestOverviewReportsQueueQuarantineInventory(t *testing.T) {
	root := writeOverviewQuarantineFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview quarantine json: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview quarantine: %v\nbody=%s", err, out.String())
	}
	if overview.OK || overview.State != "attention" || overview.Queue.Quarantined != 1 || overview.Queue.QuarantineRestorable != 0 || overview.Queue.QuarantineUnrestorable != 1 {
		t.Fatalf("overview = %+v", overview)
	}
	if !stringSliceContains(overview.Actions, "agent-team queue quarantine ls") {
		t.Fatalf("actions missing quarantine ls: %+v", overview.Actions)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"overview", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("overview quarantine text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{
		"quarantined=1 restorable=0 unrestorable=1",
		"agent-team queue quarantine ls",
	} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("overview quarantine text missing %q:\n%s", want, textOut.String())
		}
	}
}

func TestOverviewReportsJobQuarantineInventory(t *testing.T) {
	root := writeNextJobQuarantineFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview job quarantine json: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview job quarantine: %v\nbody=%s", err, out.String())
	}
	if overview.OK || overview.State != "attention" || overview.JobQuarantine.Quarantined != 2 || overview.JobQuarantine.Restorable != 1 || overview.JobQuarantine.Unrestorable != 1 {
		t.Fatalf("overview = %+v", overview)
	}
	if !stringSliceContains(overview.Actions, "agent-team job quarantine") {
		t.Fatalf("actions missing job quarantine: %+v", overview.Actions)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"overview", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("overview job quarantine text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{
		"job quarantine: quarantined=2 restorable=1 unrestorable=1",
		"agent-team job quarantine",
	} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("overview job quarantine text missing %q:\n%s", want, textOut.String())
		}
	}
}

func TestOverviewIgnoresRecoveredIntakeErrors(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	replayedAt := time.Date(2026, 6, 19, 12, 5, 0, 0, time.UTC)
	if err := appendIntakeDelivery(teamDir, intakeDelivery{
		ID:           "intake-recovered",
		Time:         time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC),
		Provider:     "linear",
		Status:       intakeDeliveryStatusError,
		HTTPStatus:   503,
		EventType:    "ticket.created",
		Payload:      map[string]any{"source": "linear", "ticket": "SQU-801", "title": "Recovered intake"},
		Ticket:       "SQU-801",
		Error:        "daemon is not running",
		ReplayStatus: intakeDeliveryReplayStatusOK,
		ReplayedAt:   &replayedAt,
	}); err != nil {
		t.Fatalf("append recovered intake delivery: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview recovered intake json: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview recovered intake: %v\nbody=%s", err, out.String())
	}
	if overview.Intake.Deliveries != 1 || overview.Intake.Errors != 0 || overview.Intake.Recovered != 1 || overview.Intake.Replayable != 0 || overview.Intake.LatestErrorID != "" {
		t.Fatalf("intake summary = %+v", overview.Intake)
	}
	for _, unwanted := range []string{
		"agent-team intake summary",
		"agent-team intake deliveries --status error",
		"agent-team intake replay --all --dedupe-request-id --dry-run --preview-triggers",
	} {
		if stringSliceContains(overview.Actions, unwanted) {
			t.Fatalf("actions should not contain %q: %+v", unwanted, overview.Actions)
		}
	}
}

func TestTeamOverviewScopesCountsAndActions(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "overview", "delivery", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team overview json: %v\nstderr=%s", err, stderr.String())
	}

	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode team overview: %v\nbody=%s", err, out.String())
	}
	if overview.Team == nil || overview.Team.Name != "delivery" {
		t.Fatalf("team = %+v", overview.Team)
	}
	if overview.OK || overview.State != "attention" {
		t.Fatalf("overview state = ok:%v state:%q", overview.OK, overview.State)
	}
	if overview.Topology == nil || overview.Topology.Instances != 2 || overview.Topology.Teams != 1 || overview.Topology.Pipelines != 1 || overview.Topology.Schedules != 1 {
		t.Fatalf("topology = %+v", overview.Topology)
	}
	if overview.Jobs.Summary.Total != 1 || overview.Jobs.Summary.Active != 1 || overview.Jobs.Summary.Terminal != 0 || overview.Jobs.Attention != 1 || overview.Queue.Dead != 1 || overview.Queue.Quarantined != 1 || overview.Queue.QuarantineRestorable != 1 || overview.Queue.QuarantineUnrestorable != 0 || overview.Pipelines.ReadySteps != 1 || overview.Schedules.Due != 1 {
		t.Fatalf("overview = %+v", overview)
	}
	for _, want := range []string{
		"agent-team team repair delivery --dry-run --jobs",
		"agent-team team sync delivery --dry-run",
		"agent-team team queue retry delivery --all --job squ-700 --sort attempts --limit 10 --dry-run",
		"agent-team team queue quarantine delivery",
		"agent-team team triage delivery",
		"agent-team team tick delivery --dry-run --preview-routes",
		"agent-team team tick delivery --dry-run --skip-drain --skip-advance",
		"agent-team team drain delivery",
	} {
		if !stringSliceContains(overview.Actions, want) {
			t.Fatalf("actions missing %q: %+v", want, overview.Actions)
		}
	}
	if detail, ok := findOperatorActionHint(overview.ActionDetails, "agent-team team tick delivery --dry-run --preview-routes"); !ok || detail.Team != "delivery" || detail.Source != "pipelines" || detail.Reason == "" {
		t.Fatalf("team advance detail = %+v, ok=%v", detail, ok)
	}
	if stringSliceContains(overview.Actions, "agent-team team queue retry delivery --all --sort attempts --limit 10 --dry-run") {
		t.Fatalf("team actions should prefer job-filtered retry: %+v", overview.Actions)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"team", "overview", "delivery", "--repo", root, "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("team overview --commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommands := strings.Join(scopedOperatorActions(commandActionsOnly(overview.Actions), operatorCommandScope{Repo: root, Set: true}), "\n") + "\n"
	if got := commandsOut.String(); got != wantCommands {
		t.Fatalf("team overview --commands = %q, want %q", got, wantCommands)
	}
}

func TestTeamOverviewCommandFormat(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "overview", "delivery", "--repo", root, "--format", "{{.Team.Name}} {{.State}} {{.Queue.Quarantined}} {{len .Actions}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team overview format: %v\nstderr=%s", err, stderr.String())
	}
	if got := out.String(); !strings.HasPrefix(got, "delivery attention 1 ") {
		t.Fatalf("team overview format output = %q", got)
	}
}

func TestTeamOverviewRecommendsScopedCleanupCommand(t *testing.T) {
	root := writeOverviewCleanupFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "overview", "delivery", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team overview cleanup json: %v\nstderr=%s", err, stderr.String())
	}

	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode team overview cleanup: %v\nbody=%s", err, out.String())
	}
	if overview.Team == nil || overview.Team.Name != "delivery" || overview.Jobs.CleanupReady != 1 {
		t.Fatalf("team overview = %+v", overview)
	}
	if !stringSliceContains(overview.Actions, "agent-team team cleanup delivery --dry-run") {
		t.Fatalf("actions missing scoped cleanup command: %+v", overview.Actions)
	}
	if stringSliceContains(overview.Actions, "agent-team job cleanup --all --dry-run") {
		t.Fatalf("team actions should not include unscoped batch cleanup: %+v", overview.Actions)
	}
}

func TestTeamOverviewTextRendersTeamSummary(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "overview", "delivery", "--repo", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team overview text: %v\nstderr=%s", err, stderr.String())
	}

	for _, want := range []string{
		"overview: attention",
		"team: delivery",
		"topology: instances=2 persistent=1 ephemeral=1",
		"jobs: active=1 (queued=0 running=0 blocked=1) terminal=0 (done=0 failed=0) total=1 attention=1",
		"queue: total=1 pending=0 dead=1 delayed=0 attempts=3 quarantined=1 restorable=1 unrestorable=0",
		"schedules: declared=1 due=1 upcoming=1",
		"agent-team team repair delivery --dry-run --jobs",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("team overview text missing %q:\n%s", want, out.String())
		}
	}
}

func TestOverviewFormatValidation(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "overview-json-conflict",
			args: []string{"overview", "--format", "{{.State}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "overview-invalid-template",
			args: []string{"overview", "--format", "{{"},
			want: "invalid --format template",
		},
		{
			name: "overview-commands-json-conflict",
			args: []string{"overview", "--commands", "--json"},
			want: wantCommandsModeConflict("--json"),
		},
		{
			name: "overview-commands-format-conflict",
			args: []string{"overview", "--commands", "--format", "{{.State}}"},
			want: wantCommandsModeConflict("--format"),
		},
		{
			name: "overview-commands-watch-conflict",
			args: []string{"overview", "--commands", "--watch"},
			want: wantCommandsModeConflict("--watch"),
		},
		{
			name: "overview-invalid-limit",
			args: []string{"overview", "--limit", "-1"},
			want: "--limit must be >= 0",
		},
		{
			name: "overview-invalid-source",
			args: []string{"overview", "--source", "missing"},
			want: "unknown --source",
		},
		{
			name: "team-overview-json-conflict",
			args: []string{"team", "overview", "delivery", "--format", "{{.State}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "team-overview-invalid-template",
			args: []string{"team", "overview", "delivery", "--format", "{{"},
			want: "invalid --format template",
		},
		{
			name: "team-overview-commands-json-conflict",
			args: []string{"team", "overview", "delivery", "--commands", "--json"},
			want: wantCommandsModeConflict("--json"),
		},
		{
			name: "team-overview-commands-format-conflict",
			args: []string{"team", "overview", "delivery", "--commands", "--format", "{{.State}}"},
			want: wantCommandsModeConflict("--format"),
		},
		{
			name: "team-overview-commands-watch-conflict",
			args: []string{"team", "overview", "delivery", "--commands", "--watch"},
			want: wantCommandsModeConflict("--watch"),
		},
		{
			name: "team-overview-invalid-limit",
			args: []string{"team", "overview", "delivery", "--limit", "-1"},
			want: "--limit must be >= 0",
		},
		{
			name: "team-overview-invalid-source",
			args: []string{"team", "overview", "delivery", "--source", "missing"},
			want: "unknown --source",
		},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%s: expected validation error", tc.name)
		}
		var code ExitCode
		if !errors.As(err, &code) || int(code) != 2 {
			t.Fatalf("%s: err=%v, want exit 2", tc.name, err)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%s: stderr=%q, want %q", tc.name, stderr.String(), tc.want)
		}
		if out.Len() != 0 {
			t.Fatalf("%s: validation wrote stdout: %q", tc.name, out.String())
		}
	}
}

func TestOverviewStateReportsActiveForReadyWork(t *testing.T) {
	overview := &overviewResult{
		OK:    true,
		State: "ok",
		Queue: queueSummary{
			Pending: 1,
			Delayed: 0,
		},
	}
	overview.Actions = overviewActions(overview, nil)
	overview.OK = overviewOK(overview, nil)
	overview.State = overviewState(overview)

	if overview.OK || overview.State != "active" {
		t.Fatalf("overview state = ok:%v state:%q", overview.OK, overview.State)
	}
	if !stringSliceContains(overview.Actions, "agent-team queue drain --dry-run") {
		t.Fatalf("actions = %+v", overview.Actions)
	}
	if !stringSliceContains(overview.Actions, "agent-team drain") {
		t.Fatalf("actions missing drain: %+v", overview.Actions)
	}
	details := overviewActionHints(overview, nil)
	if detail, ok := findOperatorActionHint(details, "agent-team drain"); !ok || detail.Source != "overview" || detail.Reason != "drainable_work=1" {
		t.Fatalf("drain detail = %+v, ok=%v", detail, ok)
	}
}

func TestOverviewStateReportsAttentionForFailures(t *testing.T) {
	overview := &overviewResult{
		OK: true,
		Queue: queueSummary{
			Dead: 1,
		},
	}
	overview.Actions = overviewActions(overview, nil)
	overview.OK = overviewOK(overview, nil)
	overview.State = overviewState(overview)

	if overview.OK || overview.State != "attention" {
		t.Fatalf("overview state = ok:%v state:%q", overview.OK, overview.State)
	}
	if !stringSliceContains(overview.Actions, "agent-team queue retry --all --sort attempts --limit 10 --dry-run") {
		t.Fatalf("actions = %+v", overview.Actions)
	}
}

func TestOverviewNormalizesRetryIssueActionsToDryRun(t *testing.T) {
	overview := &overviewResult{
		OK: true,
		Queue: queueSummary{
			Dead: 1,
		},
	}
	health := &healthResult{
		Healthy: false,
		Issues: []healthIssue{{
			Code: "queue_dead_letter",
			Actions: []string{
				"agent-team job queue retry squ-1 q-1",
				"agent-team repair --skip-tick",
			},
		}},
	}

	actions := overviewActions(overview, health)
	for _, want := range []string{
		"agent-team job queue retry squ-1 q-1 --dry-run",
		"agent-team repair --skip-tick --dry-run",
	} {
		if !stringSliceContains(actions, want) {
			t.Fatalf("actions missing %q: %+v", want, actions)
		}
	}
	for _, unwanted := range []string{
		"agent-team job queue retry squ-1 q-1",
		"agent-team repair --skip-tick",
	} {
		if stringSliceContains(actions, unwanted) {
			t.Fatalf("actions should normalize %q to dry-run: %+v", unwanted, actions)
		}
	}
}

func TestOverviewWatchRendersUntilContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &bytes.Buffer{}
	calls := 0

	err := runOverviewWatch(ctx, out, func(now time.Time) (*overviewResult, error) {
		calls++
		cancel()
		return &overviewResult{
			OK:         true,
			State:      "ok",
			CapturedAt: now.UTC().Format(time.RFC3339),
			Health: overviewHealthSummary{
				Healthy: true,
			},
		}, nil
	}, false, nil, time.Millisecond, false)
	if err != nil {
		t.Fatalf("runOverviewWatch: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if !strings.Contains(out.String(), "overview: ok") || !strings.Contains(out.String(), "actions: none") {
		t.Fatalf("watch output:\n%s", out.String())
	}
}

func writeOverviewAttentionFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"manager", "support", "worker"} {
		if err := os.MkdirAll(filepath.Join(teamDir, "agents", name), 0o755); err != nil {
			t.Fatalf("mkdir agent %s: %v", name, err)
		}
	}
	instances := `
[instances.manager]
agent = "manager"

[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[schedules.nightly]
every = "1h"
run_on_start = true
payload.kind = "nightly"

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
schedules = ["nightly"]
`
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(instances), 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	j, err := job.New("SQU-700", "worker", "test kickoff", now)
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.Status = job.StatusBlocked
	j.Pipeline = "ticket_to_pr"
	j.Steps = []job.Step{{
		ID:        "implement",
		Target:    "worker",
		Status:    job.StatusBlocked,
		StartedAt: now.Add(-time.Hour),
	}}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}

	item := &daemon.QueueItem{
		ID:             "q-overview",
		State:          daemon.QueueStateDead,
		EventType:      "agent.dispatch",
		Instance:       "worker",
		InstanceID:     "worker-squ-700",
		Payload:        map[string]any{"target": "worker", "ticket": "SQU-700", "job_id": "squ-700"},
		Attempts:       daemon.MaxQueueAttempts,
		LastError:      "spawn failed",
		QueuedAt:       now,
		UpdatedAt:      now,
		DeadLetteredAt: now.Add(time.Minute),
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
		t.Fatalf("WriteQueueItem: %v", err)
	}
	writeQuarantinedQueueItem(t, teamDir, "20260619T030000.000000000Z", daemon.QueueStateDead, &daemon.QueueItem{
		ID:         "q-overview-quarantined",
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-700",
		Payload:    map[string]any{"target": "worker", "ticket": "SQU-700", "job_id": "squ-700"},
		QueuedAt:   now.Add(-2 * time.Hour),
		UpdatedAt:  now.Add(-2 * time.Hour),
	})
	return root
}

func writeOverviewParallelReadyFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	instances := `
[instances.manager]
agent = "manager"

[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.parallel_checks]
trigger.event = "ticket.created"

[[pipelines.parallel_checks.steps]]
id = "lint"
target = "worker"

[[pipelines.parallel_checks.steps]]
id = "test"
target = "worker"

[[pipelines.parallel_checks.steps]]
id = "review"
target = "manager"
after = ["lint", "test"]

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["parallel_checks"]
`
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(instances), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	j := &job.Job{
		ID:        "squ-740",
		Ticket:    "SQU-740",
		Target:    "worker",
		Kickoff:   "parallel checks",
		Pipeline:  "parallel_checks",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "lint", Target: "worker", Status: job.StatusQueued},
			{ID: "test", Target: "worker", Status: job.StatusBlocked},
			{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"lint", "test"}},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	return root
}

func writeOverviewStaleRunningFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	instances := `
[instances.manager]
agent = "manager"

[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[teams.delivery]
instances = ["manager", "worker"]
`
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(instances), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-720",
		Ticket:    "SQU-720",
		Target:    "worker",
		Instance:  "worker-squ-720",
		Status:    job.StatusRunning,
		CreatedAt: now.Add(-48 * time.Hour),
		UpdatedAt: now.Add(-48 * time.Hour),
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	return root
}

func writeOverviewRuntimeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	instances := `
[instances.manager]
agent = "manager"

[instances.worker]
agent = "worker"
ephemeral = true

[teams.delivery]
instances = ["manager", "worker"]
`
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(instances), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusCrashed, Runtime: "claude", SessionID: "manager-session", StartedAt: now.Add(-2 * time.Hour), ExitedAt: now.Add(-time.Hour)},
		{Instance: "worker-squ-900", Agent: "worker", Status: daemon.StatusCrashed, Runtime: "codex", StartedAt: now.Add(-90 * time.Minute), ExitedAt: now.Add(-30 * time.Minute)},
		{Instance: "worker-squ-901", Agent: "worker", Status: daemon.StatusExited, Runtime: "codex", SessionID: "worker-session", StartedAt: now.Add(-80 * time.Minute), ExitedAt: now.Add(-20 * time.Minute)},
		{Instance: "support", Agent: "support", Status: daemon.StatusCrashed, Runtime: "claude", StartedAt: now.Add(-70 * time.Minute), ExitedAt: now.Add(-10 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("WriteMetadata %s: %v", meta.Instance, err)
		}
	}
	return root
}

func writeOverviewQueuedRuntimeFixture(t *testing.T, queued int) string {
	t.Helper()
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	instances := `
[instances.reviewer]
agent = "manager"
ephemeral = true
replicas = 1

[teams.delivery]
instances = ["reviewer"]
`
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(instances), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	for i := 0; i < queued; i++ {
		item := &daemon.QueueItem{
			ID:         fmt.Sprintf("queued-reviewer-%d", i),
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "reviewer",
			InstanceID: fmt.Sprintf("reviewer-squ-71%d", i),
			Payload:    map[string]any{"target": "reviewer"},
			QueuedAt:   now.Add(time.Duration(i) * time.Minute),
			UpdatedAt:  now.Add(time.Duration(i) * time.Minute),
		}
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("WriteQueueItem %s: %v", item.ID, err)
		}
	}
	if queued > 0 {
		delayed := &daemon.QueueItem{
			ID:         "queued-reviewer-delayed",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "reviewer",
			InstanceID: "reviewer-delayed",
			Payload:    map[string]any{"target": "reviewer"},
			NextRetry:  time.Now().UTC().Add(24 * time.Hour),
			QueuedAt:   now,
			UpdatedAt:  now,
		}
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), delayed); err != nil {
			t.Fatalf("WriteQueueItem %s: %v", delayed.ID, err)
		}
	}
	return root
}

func writeOverviewPipelineRuntimeFixture(t *testing.T) string {
	t.Helper()
	root := writeOverviewPipelineRuntimeBase(t)
	teamDir := filepath.Join(root, ".agent_team")
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "worker-squ-910", Job: "squ-910", Agent: "worker", Status: daemon.StatusCrashed, Runtime: "codex", StartedAt: now.Add(-2 * time.Hour), ExitedAt: now.Add(-time.Hour)},
		{Instance: "worker-ops-910", Job: "ops-910", Agent: "worker", Status: daemon.StatusCrashed, Runtime: "codex", StartedAt: now.Add(-90 * time.Minute), ExitedAt: now.Add(-30 * time.Minute)},
		{Instance: "adhoc-runtime", Agent: "worker", Status: daemon.StatusExited, Runtime: "codex", StartedAt: now.Add(-80 * time.Minute), ExitedAt: now.Add(-20 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("WriteMetadata %s: %v", meta.Instance, err)
		}
	}
	return root
}

func writeOverviewPipelineStaleRuntimeFixture(t *testing.T) string {
	t.Helper()
	root := writeOverviewPipelineRuntimeBase(t)
	teamDir := filepath.Join(root, ".agent_team")
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "worker-squ-910", Job: "squ-910", Agent: "worker", Status: daemon.StatusRunning, Runtime: "codex", RuntimeBinary: "codex", PID: 4242, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "reviewer-squ-911", Job: "squ-911", Agent: "manager", Status: daemon.StatusRunning, Runtime: "claude", RuntimeBinary: "claude", PID: 4242, StartedAt: now.Add(-90 * time.Minute)},
		{Instance: "adhoc-runtime", Agent: "worker", Status: daemon.StatusExited, Runtime: "codex", StartedAt: now.Add(-80 * time.Minute), ExitedAt: now.Add(-20 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("WriteMetadata %s: %v", meta.Instance, err)
		}
	}
	return root
}

func writeOverviewPipelineRuntimeBase(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	instances := `
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
after = ["implement"]

[pipelines.ops_review]
trigger.event = "ops.created"

[[pipelines.ops_review.steps]]
id = "audit"
target = "worker"
`
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(instances), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	for _, j := range []*job.Job{
		{
			ID:        "squ-910",
			Ticket:    "SQU-910",
			Target:    "worker",
			Kickoff:   "pipeline runtime",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-910",
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-910", StartedAt: now.Add(-2 * time.Hour)},
			},
		},
		{
			ID:        "squ-911",
			Ticket:    "SQU-911",
			Target:    "worker",
			Kickoff:   "pipeline review runtime",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-911",
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "review", Target: "manager", Status: job.StatusRunning, Instance: "reviewer-squ-911", StartedAt: now.Add(-90 * time.Minute)},
			},
		},
		{
			ID:        "ops-910",
			Ticket:    "OPS-910",
			Target:    "worker",
			Kickoff:   "ops pipeline runtime",
			Pipeline:  "ops_review",
			Status:    job.StatusRunning,
			Instance:  "worker-ops-910",
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "audit", Target: "worker", Status: job.StatusRunning, Instance: "worker-ops-910", StartedAt: now.Add(-90 * time.Minute)},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}
	return root
}

func writeOverviewJobSummaryFixture(t *testing.T, statuses map[string]job.Status) string {
	t.Helper()
	root := t.TempDir()
	initInto(t, root)
	teamDir := filepath.Join(root, ".agent_team")
	now := time.Now().UTC()
	tickets := make([]string, 0, len(statuses))
	for ticket := range statuses {
		tickets = append(tickets, ticket)
	}
	sort.Strings(tickets)
	for _, ticket := range tickets {
		j, err := job.New(ticket, "worker", "test kickoff", now)
		if err != nil {
			t.Fatalf("job.New %s: %v", ticket, err)
		}
		j.Status = statuses[ticket]
		j.UpdatedAt = now
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", ticket, err)
		}
	}
	return root
}

func writeOverviewCleanupFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	instances := `
[instances.worker]
agent = "worker"
ephemeral = true

[teams.delivery]
instances = ["worker"]
`
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(instances), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	j := &job.Job{
		ID:        "squ-710",
		Ticket:    "SQU-710",
		Target:    "worker",
		Status:    job.StatusDone,
		Branch:    "worktree-worker-squ-710",
		Worktree:  filepath.Join(root, ".claude", "worktrees", "worker-squ-710"),
		PR:        "https://github.com/acme/repo/pull/710",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	return root
}

func writeOverviewExpiredHoldFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	instances := `
[instances.worker]
agent = "worker"
ephemeral = true

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[teams.delivery]
instances = ["worker"]
pipelines = ["ticket_to_pr"]
`
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(instances), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	j := &job.Job{
		ID:         "squ-711",
		Ticket:     "SQU-711",
		Target:     "worker",
		Pipeline:   "ticket_to_pr",
		Status:     job.StatusQueued,
		Held:       true,
		HoldReason: "maintenance window elapsed",
		HoldUntil:  now.Add(-time.Minute),
		CreatedAt:  now.Add(-time.Hour),
		UpdatedAt:  now,
		Steps: []job.Step{{
			ID:     "implement",
			Target: "worker",
			Status: job.StatusQueued,
		}},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	return root
}

func writeOverviewCorruptQueueFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	initInto(t, root)
	teamDir := filepath.Join(root, ".agent_team")
	queueDir := filepath.Join(daemon.QueueRoot(daemon.DaemonRoot(teamDir)), daemon.QueueStatePending)
	if err := os.MkdirAll(queueDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(queueDir, "bad.json"), []byte("{\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeOverviewQuarantineFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	initInto(t, root)
	teamDir := filepath.Join(root, ".agent_team")
	queueDir := filepath.Join(daemon.QueueRoot(daemon.DaemonRoot(teamDir)), "quarantine", "20260619T000000.000000000Z", daemon.QueueStatePending)
	if err := os.MkdirAll(queueDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(queueDir, "bad.json"), []byte("{\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeIntakeErrorFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := appendIntakeDelivery(teamDir, intakeDelivery{
		ID:         "intake-failed",
		Time:       time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC),
		Provider:   "linear",
		Status:     intakeDeliveryStatusError,
		HTTPStatus: 503,
		EventType:  "ticket.created",
		Payload:    map[string]any{"source": "linear", "ticket": "SQU-800", "title": "Failed intake"},
		Ticket:     "SQU-800",
		Error:      "daemon is not running",
	}); err != nil {
		t.Fatalf("append intake delivery: %v", err)
	}
	return root
}
