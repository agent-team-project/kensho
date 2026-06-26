package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
)

func TestOverviewReportsAttentionAndActions(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--target", root, "--json"})
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
	if overview.Jobs.Summary.Total != 1 || overview.Jobs.Summary.Blocked != 1 || overview.Jobs.Attention != 1 {
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
	cmd.SetArgs([]string{"overview", "--target", root, "--json"})
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
	next.SetArgs([]string{"next", "--target", root, "--source", "inbox", "--json"})
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
	cmd.SetArgs([]string{"overview", "--target", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview parallel ready: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview parallel ready: %v\nbody=%s", err, out.String())
	}
	if overview.Pipelines.ParallelReadySteps != 2 || !stringSliceContains(overview.Actions, "agent-team pipeline advance --all --all-ready-steps --dry-run --preview-routes") {
		t.Fatalf("overview parallel actions = %+v pipelines=%+v", overview.Actions, overview.Pipelines)
	}
	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"overview", "--target", root})
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
	if teamOverview.Pipelines.ParallelReadySteps != 2 || !stringSliceContains(teamOverview.Actions, "agent-team team advance delivery --all-ready-steps --dry-run --preview-routes") {
		t.Fatalf("team overview parallel actions = %+v pipelines=%+v", teamOverview.Actions, teamOverview.Pipelines)
	}
}

func TestOverviewRecommendsStaleRunningJobTimeoutRepair(t *testing.T) {
	root := writeOverviewStaleRunningFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--target", root, "--json"})
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
	text.SetArgs([]string{"overview", "--target", root})
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
	cmd.SetArgs([]string{"overview", "--target", root, "--json"})
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
	if !stringSliceContains(overview.Actions, "agent-team resume-plan --status crashed") {
		t.Fatalf("actions missing runtime resume plan: %+v", overview.Actions)
	}
	if detail, ok := findOperatorActionHint(overview.ActionDetails, "agent-team resume-plan --status crashed"); !ok || detail.Source != "runtime" || detail.Reason != "crashed=3" {
		t.Fatalf("runtime action detail = %+v ok=%v", detail, ok)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"overview", "--target", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("overview runtime text: %v\nstderr=%s", err, textErr.String())
	}
	if !strings.Contains(textOut.String(), "runtime: total=4 running=0 stopped=0 exited=1 crashed=3 unknown=0") {
		t.Fatalf("overview runtime text missing summary:\n%s", textOut.String())
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
	if !stringSliceContains(teamOverview.Actions, "agent-team team resume-plan delivery --status crashed") {
		t.Fatalf("team actions missing scoped runtime resume plan: %+v", teamOverview.Actions)
	}
}

func TestOverviewReportsStaleRuntimeResumePlanActions(t *testing.T) {
	root := writeOverviewRuntimeFixture(t)
	teamDir := filepath.Join(root, ".agent_team")
	oldPIDLiveCheck := daemon.PidLiveCheck
	daemon.PidLiveCheck = func(pid int) bool {
		return pid != 4242
	}
	t.Cleanup(func() {
		daemon.PidLiveCheck = oldPIDLiveCheck
	})
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
	cmd.SetArgs([]string{"overview", "--target", root, "--json"})
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
	if !stringSliceContains(overview.Actions, "agent-team resume-plan --runtime-stale") {
		t.Fatalf("actions missing stale runtime resume plan: %+v", overview.Actions)
	}
	if detail, ok := findOperatorActionHint(overview.ActionDetails, "agent-team resume-plan --runtime-stale"); !ok || detail.Source != "runtime" || detail.Reason != "stale=2" {
		t.Fatalf("stale runtime action detail = %+v ok=%v", detail, ok)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"overview", "--target", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("overview stale runtime text: %v\nstderr=%s", err, textErr.String())
	}
	if !strings.Contains(textOut.String(), "stale_running=2") {
		t.Fatalf("overview stale runtime text missing count:\n%s", textOut.String())
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
	if teamOverview.Runtime.StaleRunning != 1 || !stringSliceContains(teamOverview.Actions, "agent-team team resume-plan delivery --runtime-stale") {
		t.Fatalf("team stale runtime summary = %+v actions=%+v", teamOverview.Runtime, teamOverview.Actions)
	}
	if detail, ok := findOperatorActionHint(teamOverview.ActionDetails, "agent-team team resume-plan delivery --runtime-stale"); !ok || detail.Team != "delivery" || detail.Source != "runtime" || detail.Reason != "stale=1" {
		t.Fatalf("team stale runtime action detail = %+v ok=%v", detail, ok)
	}
}

func TestOverviewTextRendersOperatorSummary(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--target", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview text: %v\nstderr=%s", err, stderr.String())
	}

	for _, want := range []string{
		"overview: attention",
		"health: unhealthy daemon=down",
		"topology: instances=2 persistent=1 ephemeral=1",
		"jobs: total=1 queued=0 running=0 blocked=1 done=0 failed=0 attention=1",
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

func TestOverviewCommandFormat(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--target", root, "--format", "{{.State}} {{.Jobs.Summary.Total}} {{.Queue.Dead}} {{len .Actions}}"})
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
	cmd.SetArgs([]string{"overview", "--target", root, "--json"})
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
	cmd.SetArgs([]string{"overview", "--target", root, "--json"})
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
	cmd.SetArgs([]string{"overview", "--target", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview cleanup json: %v\nstderr=%s", err, stderr.String())
	}

	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview cleanup: %v\nbody=%s", err, out.String())
	}
	if overview.Jobs.Summary.Total != 1 || overview.Jobs.Summary.Done != 1 || overview.Jobs.Attention != 1 || overview.Jobs.CleanupReady != 1 {
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
	text.SetArgs([]string{"overview", "--target", root})
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
	cmd.SetArgs([]string{"overview", "--target", root, "--json"})
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
	next.SetArgs([]string{"next", "--target", root, "--reason", "expired_holds", "--json"})
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
	cmd.SetArgs([]string{"overview", "--target", root, "--json"})
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
	cmd.SetArgs([]string{"overview", "--target", root, "--json"})
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
	cmd.SetArgs([]string{"overview", "--target", root, "--json"})
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
	text.SetArgs([]string{"overview", "--target", root})
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
	cmd.SetArgs([]string{"overview", "--target", root, "--json"})
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
	if overview.Jobs.Summary.Total != 1 || overview.Jobs.Attention != 1 || overview.Queue.Dead != 1 || overview.Queue.Quarantined != 1 || overview.Queue.QuarantineRestorable != 1 || overview.Queue.QuarantineUnrestorable != 0 || overview.Pipelines.ReadySteps != 1 || overview.Schedules.Due != 1 {
		t.Fatalf("overview = %+v", overview)
	}
	for _, want := range []string{
		"agent-team team repair delivery --dry-run --jobs",
		"agent-team team sync delivery --dry-run",
		"agent-team team queue retry delivery --all --job squ-700 --sort attempts --limit 10 --dry-run",
		"agent-team team queue quarantine delivery",
		"agent-team team triage delivery",
		"agent-team team advance delivery --dry-run --preview-routes",
		"agent-team team tick delivery --dry-run --skip-drain --skip-advance",
		"agent-team team drain delivery",
	} {
		if !stringSliceContains(overview.Actions, want) {
			t.Fatalf("actions missing %q: %+v", want, overview.Actions)
		}
	}
	if detail, ok := findOperatorActionHint(overview.ActionDetails, "agent-team team advance delivery --dry-run --preview-routes"); !ok || detail.Team != "delivery" || detail.Source != "pipelines" || detail.Reason == "" {
		t.Fatalf("team advance detail = %+v, ok=%v", detail, ok)
	}
	if stringSliceContains(overview.Actions, "agent-team team queue retry delivery --all --sort attempts --limit 10 --dry-run") {
		t.Fatalf("team actions should prefer job-filtered retry: %+v", overview.Actions)
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
		"jobs: total=1 queued=0 running=0 blocked=1 done=0 failed=0 attention=1",
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
			name: "team-overview-json-conflict",
			args: []string{"team", "overview", "delivery", "--format", "{{.State}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "team-overview-invalid-template",
			args: []string{"team", "overview", "delivery", "--format", "{{"},
			want: "invalid --format template",
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
		{Instance: "manager", Agent: "manager", Status: daemon.StatusCrashed, Runtime: "claude", StartedAt: now.Add(-2 * time.Hour), ExitedAt: now.Add(-time.Hour)},
		{Instance: "worker-squ-900", Agent: "worker", Status: daemon.StatusCrashed, Runtime: "codex", StartedAt: now.Add(-90 * time.Minute), ExitedAt: now.Add(-30 * time.Minute)},
		{Instance: "worker-squ-901", Agent: "worker", Status: daemon.StatusExited, Runtime: "codex", StartedAt: now.Add(-80 * time.Minute), ExitedAt: now.Add(-20 * time.Minute)},
		{Instance: "support", Agent: "support", Status: daemon.StatusCrashed, Runtime: "claude", StartedAt: now.Add(-70 * time.Minute), ExitedAt: now.Add(-10 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("WriteMetadata %s: %v", meta.Instance, err)
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
