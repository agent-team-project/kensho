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

func TestJobQueueListsOwnedItems(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	j, err := job.New("SQU-120", "worker", "inspect queued work", time.Now())
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.Instance = "worker-squ-120"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-job-ready",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-120",
			Payload: map[string]any{
				"runtime": "codex",
				"target":  "worker",
			},
			QueuedAt:  now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-2 * time.Hour),
		},
		{
			ID:         "q-job-delayed",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-120-delayed",
			Payload: map[string]any{
				"job_id":  "squ-120",
				"runtime": "claude",
				"target":  "worker",
			},
			NextRetry: now.Add(time.Hour),
			QueuedAt:  now.Add(-time.Hour),
			UpdatedAt: now.Add(-time.Hour),
		},
		{
			ID:         "q-other",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-121",
			Payload: map[string]any{
				"job_id":  "squ-121",
				"runtime": "codex",
				"target":  "worker",
			},
			QueuedAt:  now.Add(-30 * time.Minute),
			UpdatedAt: now.Add(-30 * time.Minute),
		},
		{
			ID:         "q-job-dead",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-120",
			Payload: map[string]any{
				"job_id":  "squ-120",
				"runtime": "codex",
				"ticket":  "SQU-120",
				"target":  "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-3 * time.Hour),
			DeadLetteredAt: now.Add(-3 * time.Hour),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("WriteQueueItem %s: %v", item.ID, err)
		}
	}

	list := NewRootCmd()
	listOut, listErr := &bytes.Buffer{}, &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetErr(listErr)
	list.SetArgs([]string{"job", "queue", "SQU-120", "--repo", tmp, "--json"})
	if err := list.Execute(); err != nil {
		t.Fatalf("job queue json: %v\nstderr=%s", err, listErr.String())
	}
	var items []daemon.QueueItem
	if err := json.Unmarshal(listOut.Bytes(), &items); err != nil {
		t.Fatalf("decode job queue json: %v\nbody=%s", err, listOut.String())
	}
	if got := strings.Join(queueItemIDs(items), ","); got != "q-job-dead,q-job-ready,q-job-delayed" {
		t.Fatalf("job queue ids = %s", got)
	}

	sorted := NewRootCmd()
	sortedOut, sortedErr := &bytes.Buffer{}, &bytes.Buffer{}
	sorted.SetOut(sortedOut)
	sorted.SetErr(sortedErr)
	sorted.SetArgs([]string{"job", "queue", "SQU-120", "--repo", tmp, "--sort", "attempts", "--limit", "1", "--format", "{{.ID}}"})
	if err := sorted.Execute(); err != nil {
		t.Fatalf("job queue sort/limit: %v\nstderr=%s", err, sortedErr.String())
	}
	if got := strings.TrimSpace(sortedOut.String()); got != "q-job-dead" {
		t.Fatalf("job queue sort/limit output = %q", sortedOut.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var watchOut bytes.Buffer
	if err := runJobQueueListWatch(ctx, &watchOut, teamDir, j, queueListFilters{}, queueListOptions{Sort: "attempts", Limit: 1}, false, nil, time.Millisecond, false); err != nil {
		t.Fatalf("runJobQueueListWatch: %v", err)
	}
	if got := watchOut.String(); !strings.Contains(got, "q-job-dead") || strings.Contains(got, "q-job-ready") || strings.Contains(got, watchClearSequence) {
		t.Fatalf("job queue watch output = %q", got)
	}

	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	var summaryWatchOut bytes.Buffer
	if err := runJobQueueSummaryWatch(ctx, &summaryWatchOut, teamDir, j, queueListFilters{}, false, time.Millisecond, false); err != nil {
		t.Fatalf("runJobQueueSummaryWatch: %v", err)
	}
	if got := summaryWatchOut.String(); !strings.Contains(got, "queue: total=3 pending=2 dead=1") || strings.Contains(got, watchClearSequence) {
		t.Fatalf("job queue summary watch output = %q", got)
	}

	textList := NewRootCmd()
	textListOut, textListErr := &bytes.Buffer{}, &bytes.Buffer{}
	textList.SetOut(textListOut)
	textList.SetErr(textListErr)
	textList.SetArgs([]string{"job", "queue", "SQU-120", "--repo", tmp})
	if err := textList.Execute(); err != nil {
		t.Fatalf("job queue text: %v\nstderr=%s", err, textListErr.String())
	}
	for _, want := range []string{
		"agent-team job queue retry squ-120 q-job-dead; agent-team job queue drop squ-120 q-job-dead",
		"agent-team queue drain; agent-team job queue drop squ-120 q-job-ready",
		"agent-team job queue show squ-120 q-job-delayed; agent-team job queue drop squ-120 q-job-delayed",
	} {
		if !strings.Contains(textListOut.String(), want) {
			t.Fatalf("job queue text missing %q:\n%s", want, textListOut.String())
		}
	}

	textListCommands := NewRootCmd()
	textListCommandsOut, textListCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	textListCommands.SetOut(textListCommandsOut)
	textListCommands.SetErr(textListCommandsErr)
	textListCommands.SetArgs([]string{"job", "queue", "SQU-120", "--repo", tmp, "--commands"})
	if err := textListCommands.Execute(); err != nil {
		t.Fatalf("job queue --commands: %v\nstderr=%s", err, textListCommandsErr.String())
	}
	wantListCommands := strings.Join(scopedOperatorActions([]string{
		"agent-team job queue retry squ-120 q-job-dead",
		"agent-team job queue drop squ-120 q-job-dead",
		"agent-team queue drain",
		"agent-team job queue drop squ-120 q-job-ready",
		"agent-team job queue show squ-120 q-job-delayed",
		"agent-team job queue drop squ-120 q-job-delayed",
	}, operatorCommandScope{Repo: tmp, Set: true}), "\n") + "\n"
	if got, want := textListCommandsOut.String(), wantListCommands; got != want {
		t.Fatalf("job queue --commands = %q, want %q", got, want)
	}
	if strings.Contains(textListCommandsOut.String(), "STATE") || strings.Contains(textListCommandsOut.String(), "ACTION") {
		t.Fatalf("job queue --commands included table output:\n%s", textListCommandsOut.String())
	}

	runtimeList := NewRootCmd()
	runtimeListOut, runtimeListErr := &bytes.Buffer{}, &bytes.Buffer{}
	runtimeList.SetOut(runtimeListOut)
	runtimeList.SetErr(runtimeListErr)
	runtimeList.SetArgs([]string{"job", "queue", "SQU-120", "--repo", tmp, "--runtime", "codex", "--json"})
	if err := runtimeList.Execute(); err != nil {
		t.Fatalf("job queue runtime json: %v\nstderr=%s", err, runtimeListErr.String())
	}
	var runtimeItems []daemon.QueueItem
	if err := json.Unmarshal(runtimeListOut.Bytes(), &runtimeItems); err != nil {
		t.Fatalf("decode job queue runtime json: %v\nbody=%s", err, runtimeListOut.String())
	}
	if got := strings.Join(queueItemIDs(runtimeItems), ","); got != "q-job-dead,q-job-ready" {
		t.Fatalf("job queue runtime ids = %s", got)
	}

	showText := NewRootCmd()
	showTextOut, showTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	showText.SetOut(showTextOut)
	showText.SetErr(showTextErr)
	showText.SetArgs([]string{"job", "queue", "show", "SQU-120", "q-job-dead", "--repo", tmp})
	if err := showText.Execute(); err != nil {
		t.Fatalf("job queue show text: %v\nstderr=%s", err, showTextErr.String())
	}
	for _, want := range []string{"Runtime:     codex", "agent-team job queue retry squ-120 q-job-dead", "agent-team job queue drop squ-120 q-job-dead"} {
		if !strings.Contains(showTextOut.String(), want) {
			t.Fatalf("job queue show missing %q:\n%s", want, showTextOut.String())
		}
	}

	showCommands := NewRootCmd()
	showCommandsOut, showCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	showCommands.SetOut(showCommandsOut)
	showCommands.SetErr(showCommandsErr)
	showCommands.SetArgs([]string{"job", "queue", "show", "SQU-120", "q-job-dead", "--repo", tmp, "--commands"})
	if err := showCommands.Execute(); err != nil {
		t.Fatalf("job queue show --commands: %v\nstderr=%s", err, showCommandsErr.String())
	}
	wantCommands := strings.Join(scopedOperatorActions([]string{
		"agent-team job queue retry squ-120 q-job-dead",
		"agent-team job queue drop squ-120 q-job-dead",
	}, operatorCommandScope{Repo: tmp, Set: true}), "\n") + "\n"
	if got, want := showCommandsOut.String(), wantCommands; got != want {
		t.Fatalf("job queue show --commands = %q, want %q", got, want)
	}

	showReady := NewRootCmd()
	showReadyOut, showReadyErr := &bytes.Buffer{}, &bytes.Buffer{}
	showReady.SetOut(showReadyOut)
	showReady.SetErr(showReadyErr)
	showReady.SetArgs([]string{"job", "queue", "show", "SQU-120", "q-job-ready", "--repo", tmp})
	if err := showReady.Execute(); err != nil {
		t.Fatalf("job queue show ready text: %v\nstderr=%s", err, showReadyErr.String())
	}
	for _, want := range []string{"Runtime:     codex", "agent-team queue drain", "agent-team job queue drop squ-120 q-job-ready"} {
		if !strings.Contains(showReadyOut.String(), want) {
			t.Fatalf("job queue show ready missing %q:\n%s", want, showReadyOut.String())
		}
	}

	showReadyCommands := NewRootCmd()
	showReadyCommandsOut, showReadyCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	showReadyCommands.SetOut(showReadyCommandsOut)
	showReadyCommands.SetErr(showReadyCommandsErr)
	showReadyCommands.SetArgs([]string{"job", "queue", "show", "SQU-120", "q-job-ready", "--repo", tmp, "--commands"})
	if err := showReadyCommands.Execute(); err != nil {
		t.Fatalf("job queue show ready --commands: %v\nstderr=%s", err, showReadyCommandsErr.String())
	}
	wantReadyCommands := strings.Join(scopedOperatorActions([]string{
		"agent-team queue drain",
		"agent-team job queue drop squ-120 q-job-ready",
	}, operatorCommandScope{Repo: tmp, Set: true}), "\n") + "\n"
	if got, want := showReadyCommandsOut.String(), wantReadyCommands; got != want {
		t.Fatalf("job queue show ready --commands = %q, want %q", got, want)
	}

	showOther := NewRootCmd()
	showOtherOut, showOtherErr := &bytes.Buffer{}, &bytes.Buffer{}
	showOther.SetOut(showOtherOut)
	showOther.SetErr(showOtherErr)
	showOther.SetArgs([]string{"job", "queue", "show", "SQU-120", "q-other", "--repo", tmp, "--json"})
	if err := showOther.Execute(); err == nil {
		t.Fatalf("job queue show unrelated item unexpectedly succeeded: stdout=%s", showOtherOut.String())
	}
	if !strings.Contains(showOtherErr.String(), "not owned by job") {
		t.Fatalf("job queue show unrelated stderr = %q", showOtherErr.String())
	}

	ready := NewRootCmd()
	readyOut, readyErr := &bytes.Buffer{}, &bytes.Buffer{}
	ready.SetOut(readyOut)
	ready.SetErr(readyErr)
	ready.SetArgs([]string{"job", "queue", "SQU-120", "--repo", tmp, "--ready", "--format", "{{.ID}} {{.State}}"})
	if err := ready.Execute(); err != nil {
		t.Fatalf("job queue ready format: %v\nstderr=%s", err, readyErr.String())
	}
	if got, want := strings.TrimSpace(readyOut.String()), "q-job-ready pending"; got != want {
		t.Fatalf("job queue ready output = %q, want %q", got, want)
	}

	summary := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(summaryOut)
	summary.SetErr(summaryErr)
	summary.SetArgs([]string{"job", "queue", "SQU-120", "--repo", tmp, "--summary", "--json"})
	if err := summary.Execute(); err != nil {
		t.Fatalf("job queue summary json: %v\nstderr=%s", err, summaryErr.String())
	}
	var summaryResult queueSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &summaryResult); err != nil {
		t.Fatalf("decode job queue summary: %v\nbody=%s", err, summaryOut.String())
	}
	if summaryResult.Total != 3 || summaryResult.Pending != 2 || summaryResult.Dead != 1 || summaryResult.Delayed != 1 || summaryResult.Attempts != daemon.MaxQueueAttempts || summaryResult.Runtimes["codex"] != 2 || summaryResult.Runtimes["claude"] != 1 {
		t.Fatalf("job queue summary = %+v", summaryResult)
	}

	runtimeSummaryCmd := NewRootCmd()
	runtimeSummaryOut, runtimeSummaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	runtimeSummaryCmd.SetOut(runtimeSummaryOut)
	runtimeSummaryCmd.SetErr(runtimeSummaryErr)
	runtimeSummaryCmd.SetArgs([]string{"job", "queue", "SQU-120", "--repo", tmp, "--summary", "--runtime", "codex", "--json"})
	if err := runtimeSummaryCmd.Execute(); err != nil {
		t.Fatalf("job queue runtime summary json: %v\nstderr=%s", err, runtimeSummaryErr.String())
	}
	var runtimeSummary queueSummary
	if err := json.Unmarshal(runtimeSummaryOut.Bytes(), &runtimeSummary); err != nil {
		t.Fatalf("decode job queue runtime summary: %v\nbody=%s", err, runtimeSummaryOut.String())
	}
	if runtimeSummary.Total != 2 || runtimeSummary.Pending != 1 || runtimeSummary.Dead != 1 || runtimeSummary.Delayed != 0 || runtimeSummary.Runtimes["codex"] != 2 {
		t.Fatalf("job queue runtime summary = %+v", runtimeSummary)
	}
}

func TestJobQueueRejectsNegativeWatchInterval(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	j := mustNewJob(t, "SQU-122", "worker")
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "queue", "SQU-122", "--repo", tmp, "--watch", "--interval", "-1s"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("job queue negative interval succeeded")
	}
	if !strings.Contains(stderr.String(), "--interval must be >= 0") {
		t.Fatalf("negative interval stderr = %q", stderr.String())
	}
}

func TestJobQueueRetryDropScopesOwnedItems(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	j, err := job.New("SQU-121", "worker", "recover queued work", time.Now())
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.Instance = "worker-squ-121"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-job-pending",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-121",
			Payload: map[string]any{
				"job_id":  "squ-121",
				"runtime": "codex",
				"target":  "worker",
			},
			QueuedAt:  now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-2 * time.Hour),
		},
		{
			ID:         "q-job-pending-claude",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-121-claude",
			Payload: map[string]any{
				"job_id":  "squ-121",
				"runtime": "claude",
				"target":  "worker",
			},
			QueuedAt:  now.Add(-90 * time.Minute),
			UpdatedAt: now.Add(-90 * time.Minute),
		},
		{
			ID:         "q-job-dead",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-121",
			Payload: map[string]any{
				"job_id":  "squ-121",
				"runtime": "codex",
				"ticket":  "SQU-121",
				"target":  "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-3 * time.Hour),
			DeadLetteredAt: now.Add(-3 * time.Hour),
		},
		{
			ID:         "q-job-dead-claude",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-121-claude",
			Payload: map[string]any{
				"job_id":  "squ-121",
				"runtime": "claude",
				"ticket":  "SQU-121",
				"target":  "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-4 * time.Hour),
			UpdatedAt:      now.Add(-4 * time.Hour),
			DeadLetteredAt: now.Add(-4 * time.Hour),
		},
		{
			ID:         "q-other-dead",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-122",
			Payload: map[string]any{
				"job_id":  "squ-122",
				"runtime": "codex",
				"ticket":  "SQU-122",
				"target":  "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-time.Hour),
			UpdatedAt:      now.Add(-time.Hour),
			DeadLetteredAt: now.Add(-time.Hour),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("WriteQueueItem %s: %v", item.ID, err)
		}
	}

	retryDry := NewRootCmd()
	retryDryOut, retryDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	retryDry.SetOut(retryDryOut)
	retryDry.SetErr(retryDryErr)
	retryDry.SetArgs([]string{"job", "queue", "retry", "SQU-121", "--repo", tmp, "--all", "--runtime", "codex", "--dry-run", "--json"})
	if err := retryDry.Execute(); err != nil {
		t.Fatalf("job queue retry --all dry-run: %v\nstderr=%s", err, retryDryErr.String())
	}
	var retryDryResults []queueRetryResult
	if err := json.Unmarshal(retryDryOut.Bytes(), &retryDryResults); err != nil {
		t.Fatalf("decode retry dry-run: %v\nbody=%s", err, retryDryOut.String())
	}
	if len(retryDryResults) != 1 || retryDryResults[0].ID != "q-job-dead" || retryDryResults[0].Action != "would_retry" || !retryDryResults[0].DryRun {
		t.Fatalf("retry dry-run results = %+v", retryDryResults)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-dead"); err != nil || item.State != daemon.QueueStateDead {
		t.Fatalf("retry dry-run changed item=%+v err=%v", item, err)
	}

	retryCommands := NewRootCmd()
	retryCommandsOut, retryCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	retryCommands.SetOut(retryCommandsOut)
	retryCommands.SetErr(retryCommandsErr)
	retryCommands.SetArgs([]string{"job", "queue", "retry", "SQU-121", "--repo", tmp, "--all", "--runtime", "codex", "--limit", "1", "--dry-run", "--commands"})
	if err := retryCommands.Execute(); err != nil {
		t.Fatalf("job queue retry --all commands: %v\nstderr=%s", err, retryCommandsErr.String())
	}
	wantRetryCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "job", "queue", "retry", "SQU-121", "--repo", tmp, "--all", "--runtime", "codex", "--limit", "1"}), " ")
	if got := strings.TrimSpace(retryCommandsOut.String()); got != wantRetryCommand {
		t.Fatalf("job queue retry --all commands = %q, want %q", got, wantRetryCommand)
	}

	dropDryAll := NewRootCmd()
	dropDryAllOut, dropDryAllErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropDryAll.SetOut(dropDryAllOut)
	dropDryAll.SetErr(dropDryAllErr)
	dropDryAll.SetArgs([]string{"job", "queue", "drop", "SQU-121", "--repo", tmp, "--all", "--state", "pending", "--runtime", "codex", "--dry-run", "--json"})
	if err := dropDryAll.Execute(); err != nil {
		t.Fatalf("job queue drop --all runtime dry-run: %v\nstderr=%s", err, dropDryAllErr.String())
	}
	var dropDryAllResults []queueDropResult
	if err := json.Unmarshal(dropDryAllOut.Bytes(), &dropDryAllResults); err != nil {
		t.Fatalf("decode drop all dry-run: %v\nbody=%s", err, dropDryAllOut.String())
	}
	if len(dropDryAllResults) != 1 || dropDryAllResults[0].ID != "q-job-pending" || dropDryAllResults[0].Action != "would_drop" || !dropDryAllResults[0].DryRun {
		t.Fatalf("drop all dry-run results = %+v", dropDryAllResults)
	}

	dropCommands := NewRootCmd()
	dropCommandsOut, dropCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropCommands.SetOut(dropCommandsOut)
	dropCommands.SetErr(dropCommandsErr)
	dropCommands.SetArgs([]string{"job", "queue", "drop", "SQU-121", "--repo", tmp, "--all", "--state", "pending", "--runtime", "codex", "--limit", "1", "--dry-run", "--commands"})
	if err := dropCommands.Execute(); err != nil {
		t.Fatalf("job queue drop --all commands: %v\nstderr=%s", err, dropCommandsErr.String())
	}
	wantDropCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "job", "queue", "drop", "SQU-121", "--repo", tmp, "--all", "--state", "pending", "--runtime", "codex", "--limit", "1"}), " ")
	if got := strings.TrimSpace(dropCommandsOut.String()); got != wantDropCommand {
		t.Fatalf("job queue drop --all commands = %q, want %q", got, wantDropCommand)
	}

	retryOneCommands := NewRootCmd()
	retryOneCommandsOut, retryOneCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	retryOneCommands.SetOut(retryOneCommandsOut)
	retryOneCommands.SetErr(retryOneCommandsErr)
	retryOneCommands.SetArgs([]string{"job", "queue", "retry", "SQU-121", "q-job-dead", "--repo", tmp, "--dry-run", "--commands"})
	if err := retryOneCommands.Execute(); err != nil {
		t.Fatalf("job queue retry single commands: %v\nstderr=%s", err, retryOneCommandsErr.String())
	}
	wantRetryOneCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "job", "queue", "retry", "SQU-121", "q-job-dead", "--repo", tmp}), " ")
	if got := strings.TrimSpace(retryOneCommandsOut.String()); got != wantRetryOneCommand {
		t.Fatalf("job queue retry single commands = %q, want %q", got, wantRetryOneCommand)
	}

	retry := NewRootCmd()
	retryOut, retryErr := &bytes.Buffer{}, &bytes.Buffer{}
	retry.SetOut(retryOut)
	retry.SetErr(retryErr)
	retry.SetArgs([]string{"job", "queue", "retry", "SQU-121", "q-job-dead", "--repo", tmp, "--format", "{{.ID}} {{.Action}} {{.State}}"})
	if err := retry.Execute(); err != nil {
		t.Fatalf("job queue retry single: %v\nstderr=%s", err, retryErr.String())
	}
	if got, want := strings.TrimSpace(retryOut.String()), "q-job-dead reset dead"; got != want {
		t.Fatalf("retry output = %q, want %q", got, want)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-dead"); err != nil || item.State != daemon.QueueStatePending || item.LastError != "" || !item.DeadLetteredAt.IsZero() {
		t.Fatalf("retried item=%+v err=%v", item, err)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-other-dead"); err != nil || item.State != daemon.QueueStateDead {
		t.Fatalf("unrelated retry item changed=%+v err=%v", item, err)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-dead-claude"); err != nil || item.State != daemon.QueueStateDead {
		t.Fatalf("claude retry item changed=%+v err=%v", item, err)
	}

	dropOther := NewRootCmd()
	dropOtherOut, dropOtherErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropOther.SetOut(dropOtherOut)
	dropOther.SetErr(dropOtherErr)
	dropOther.SetArgs([]string{"job", "queue", "drop", "SQU-121", "q-other-dead", "--repo", tmp, "--dry-run"})
	if err := dropOther.Execute(); err == nil {
		t.Fatalf("job queue drop unrelated item unexpectedly succeeded: stdout=%s", dropOtherOut.String())
	}
	if !strings.Contains(dropOtherErr.String(), "not owned by job") {
		t.Fatalf("drop unrelated stderr = %q", dropOtherErr.String())
	}

	dropOneCommands := NewRootCmd()
	dropOneCommandsOut, dropOneCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropOneCommands.SetOut(dropOneCommandsOut)
	dropOneCommands.SetErr(dropOneCommandsErr)
	dropOneCommands.SetArgs([]string{"job", "queue", "drop", "SQU-121", "q-job-pending", "--repo", tmp, "--dry-run", "--commands"})
	if err := dropOneCommands.Execute(); err != nil {
		t.Fatalf("job queue drop single commands: %v\nstderr=%s", err, dropOneCommandsErr.String())
	}
	wantDropOneCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "job", "queue", "drop", "SQU-121", "q-job-pending", "--repo", tmp}), " ")
	if got := strings.TrimSpace(dropOneCommandsOut.String()); got != wantDropOneCommand {
		t.Fatalf("job queue drop single commands = %q, want %q", got, wantDropOneCommand)
	}

	drop := NewRootCmd()
	dropOut, dropErr := &bytes.Buffer{}, &bytes.Buffer{}
	drop.SetOut(dropOut)
	drop.SetErr(dropErr)
	drop.SetArgs([]string{"job", "queue", "drop", "SQU-121", "q-job-pending", "--repo", tmp, "--json"})
	if err := drop.Execute(); err != nil {
		t.Fatalf("job queue drop single: %v\nstderr=%s", err, dropErr.String())
	}
	var dropResults []queueDropResult
	if err := json.Unmarshal(dropOut.Bytes(), &dropResults); err != nil {
		t.Fatalf("decode drop result: %v\nbody=%s", err, dropOut.String())
	}
	if len(dropResults) != 1 || dropResults[0].ID != "q-job-pending" || dropResults[0].Action != "dropped" {
		t.Fatalf("drop results = %+v", dropResults)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-pending"); !os.IsNotExist(err) {
		t.Fatalf("dropped item err=%v, want not exist", err)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-other-dead"); err != nil || item.State != daemon.QueueStateDead {
		t.Fatalf("unrelated drop item changed=%+v err=%v", item, err)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-pending-claude"); err != nil || item.State != daemon.QueueStatePending {
		t.Fatalf("claude drop item changed=%+v err=%v", item, err)
	}
}

func TestJobQueueDropAllSortsBeforeLimit(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	j, err := job.New("SQU-127", "worker", "sort limited queue drops", time.Now())
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.Instance = "worker-squ-127"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	now := time.Now().UTC()
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-job-low-attempts",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-127-low",
			Payload: map[string]any{
				"job_id": "squ-127",
				"ticket": "SQU-127",
				"target": "worker",
			},
			Attempts:       1,
			LastError:      "first failure",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-2 * time.Hour),
			DeadLetteredAt: now.Add(-2 * time.Hour),
		},
		{
			ID:         "q-job-high-attempts",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-127-high",
			Payload: map[string]any{
				"job_id": "squ-127",
				"ticket": "SQU-127",
				"target": "worker",
			},
			Attempts:       9,
			LastError:      "repeated failure",
			QueuedAt:       now.Add(-time.Hour),
			UpdatedAt:      now.Add(-30 * time.Minute),
			DeadLetteredAt: now.Add(-30 * time.Minute),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("WriteQueueItem %s: %v", item.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "queue", "drop", "SQU-127", "--repo", tmp, "--all", "--sort", "attempts", "--limit", "1", "--dry-run", "--format", "{{.ID}} {{.Action}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job queue drop sort/limit: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := strings.TrimSpace(out.String()), "q-job-high-attempts would_drop"; got != want {
		t.Fatalf("job queue drop sort/limit output = %q, want %q", got, want)
	}
}

func TestJobQueueQuarantineScopesOwnedFiles(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	j, err := job.New("SQU-123", "worker", "recover quarantined work", time.Now())
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.Instance = "worker-squ-123"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	stamp := "20260619T010203.000000000Z"
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-job-quarantined-restore",
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-123",
			Payload:    map[string]any{"job_id": "squ-123", "ticket": "SQU-123", "target": "worker"},
			QueuedAt:   now.Add(-2 * time.Hour),
			UpdatedAt:  now.Add(-2 * time.Hour),
		},
		{
			ID:             "q-job-quarantined-drop",
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     "worker-squ-123",
			Payload:        map[string]any{"job_id": "squ-123", "ticket": "SQU-123", "target": "worker"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-3 * time.Hour),
			DeadLetteredAt: now.Add(-3 * time.Hour),
		},
		{
			ID:             "q-other-quarantined",
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     "worker-squ-124",
			Payload:        map[string]any{"job_id": "squ-124", "ticket": "SQU-124", "target": "worker"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-time.Hour),
			UpdatedAt:      now.Add(-time.Hour),
			DeadLetteredAt: now.Add(-time.Hour),
		},
	} {
		state := daemon.QueueStateDead
		if item.ID == "q-job-quarantined-restore" {
			state = daemon.QueueStatePending
		}
		writeQuarantinedQueueItem(t, teamDir, stamp, state, item)
	}
	restorePath := filepath.Join("quarantine", stamp, daemon.QueueStatePending, "q-job-quarantined-restore.json")
	dropPath := filepath.Join("quarantine", stamp, daemon.QueueStateDead, "q-job-quarantined-drop.json")
	otherPath := filepath.Join("quarantine", stamp, daemon.QueueStateDead, "q-other-quarantined.json")
	queueRoot := daemon.QueueRoot(daemon.DaemonRoot(teamDir))

	list := NewRootCmd()
	listOut, listErr := &bytes.Buffer{}, &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetErr(listErr)
	list.SetArgs([]string{"job", "queue", "quarantine", "SQU-123", "--repo", tmp, "--json"})
	if err := list.Execute(); err != nil {
		t.Fatalf("job queue quarantine list: %v\nstderr=%s", err, listErr.String())
	}
	var listed []queueQuarantineItem
	if err := json.Unmarshal(listOut.Bytes(), &listed); err != nil {
		t.Fatalf("decode job quarantine list: %v\nbody=%s", err, listOut.String())
	}
	if len(listed) != 2 || queueQuarantineItemIDs(listed) != "q-job-quarantined-drop,q-job-quarantined-restore" {
		t.Fatalf("listed job quarantined items = %+v", listed)
	}

	listCommands := NewRootCmd()
	listCommandsOut, listCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	listCommands.SetOut(listCommandsOut)
	listCommands.SetErr(listCommandsErr)
	listCommands.SetArgs([]string{"job", "queue", "quarantine", "SQU-123", "--repo", tmp, "--sort", "id", "--commands"})
	if err := listCommands.Execute(); err != nil {
		t.Fatalf("job queue quarantine list --commands: %v\nstderr=%s", err, listCommandsErr.String())
	}
	wantListCommands := strings.Join(scopedOperatorActions([]string{
		"agent-team job queue quarantine restore squ-123 " + dropPath,
		"agent-team job queue quarantine drop squ-123 " + dropPath,
		"agent-team job queue quarantine restore squ-123 " + restorePath,
		"agent-team job queue quarantine drop squ-123 " + restorePath,
	}, operatorCommandScope{Repo: tmp, Set: true}), "\n") + "\n"
	if got := listCommandsOut.String(); got != wantListCommands {
		t.Fatalf("job queue quarantine list --commands = %q, want %q", got, wantListCommands)
	}
	if strings.Contains(listCommandsOut.String(), "PATH") || strings.Contains(listCommandsOut.String(), "RESTORABLE") {
		t.Fatalf("job queue quarantine list --commands included table text:\n%s", listCommandsOut.String())
	}

	summary := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(summaryOut)
	summary.SetErr(summaryErr)
	summary.SetArgs([]string{"job", "queue", "quarantine", "SQU-123", "--repo", tmp, "--summary", "--json"})
	if err := summary.Execute(); err != nil {
		t.Fatalf("job queue quarantine summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var summaryBody queueQuarantineSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &summaryBody); err != nil {
		t.Fatalf("decode job queue quarantine summary: %v\nbody=%s", err, summaryOut.String())
	}
	if summaryBody.Quarantined != 2 || summaryBody.Restorable != 2 || summaryBody.Unrestorable != 0 || summaryBody.States[daemon.QueueStatePending] != 1 || summaryBody.States[daemon.QueueStateDead] != 1 || summaryBody.Jobs["squ-123"] != 2 {
		t.Fatalf("job queue quarantine summary = %+v", summaryBody)
	}

	summaryText := NewRootCmd()
	summaryTextOut, summaryTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	summaryText.SetOut(summaryTextOut)
	summaryText.SetErr(summaryTextErr)
	summaryText.SetArgs([]string{"job", "queue", "quarantine", "SQU-123", "--repo", tmp, "--state", daemon.QueueStatePending, "--summary"})
	if err := summaryText.Execute(); err != nil {
		t.Fatalf("job queue quarantine summary text: %v\nstderr=%s", err, summaryTextErr.String())
	}
	if got, want := summaryTextOut.String(), "queue quarantine: quarantined=1 restorable=1 unrestorable=0\n"; got != want {
		t.Fatalf("job queue quarantine summary text = %q, want %q", got, want)
	}

	invalidSummary := NewRootCmd()
	invalidSummaryOut, invalidSummaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidSummary.SetOut(invalidSummaryOut)
	invalidSummary.SetErr(invalidSummaryErr)
	invalidSummary.SetArgs([]string{"job", "queue", "quarantine", "SQU-123", "--repo", tmp, "--summary", "--sort", "attempts"})
	if err := invalidSummary.Execute(); err == nil {
		t.Fatalf("job queue quarantine summary accepted --sort; stdout=%s stderr=%s", invalidSummaryOut.String(), invalidSummaryErr.String())
	}
	if !strings.Contains(invalidSummaryErr.String(), "--sort and --limit cannot be combined with --summary") {
		t.Fatalf("job queue quarantine summary invalid stderr = %q", invalidSummaryErr.String())
	}

	listSorted := NewRootCmd()
	listSortedOut, listSortedErr := &bytes.Buffer{}, &bytes.Buffer{}
	listSorted.SetOut(listSortedOut)
	listSorted.SetErr(listSortedErr)
	listSorted.SetArgs([]string{"job", "queue", "quarantine", "SQU-123", "--repo", tmp, "--sort", "attempts", "--limit", "1", "--format", "{{.ID}}"})
	if err := listSorted.Execute(); err != nil {
		t.Fatalf("job queue quarantine sorted limit list: %v\nstderr=%s", err, listSortedErr.String())
	}
	if got, want := listSortedOut.String(), "q-job-quarantined-drop\n"; got != want {
		t.Fatalf("job queue quarantine sorted limit list = %q, want %q", got, want)
	}

	restoreLimit := NewRootCmd()
	restoreLimitOut, restoreLimitErr := &bytes.Buffer{}, &bytes.Buffer{}
	restoreLimit.SetOut(restoreLimitOut)
	restoreLimit.SetErr(restoreLimitErr)
	restoreLimit.SetArgs([]string{"job", "queue", "quarantine", "restore", "SQU-123", "--repo", tmp, "--all", "--sort", "attempts", "--limit", "1", "--dry-run", "--format", "{{.ID}}"})
	if err := restoreLimit.Execute(); err != nil {
		t.Fatalf("job queue quarantine restore --all limit: %v\nstderr=%s", err, restoreLimitErr.String())
	}
	if got, want := restoreLimitOut.String(), "q-job-quarantined-drop\n"; got != want {
		t.Fatalf("job restore --limit output = %q, want %q", got, want)
	}

	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"job", "queue", "quarantine", "show", "SQU-123", restorePath, "--repo", tmp})
	if err := show.Execute(); err != nil {
		t.Fatalf("job queue quarantine show: %v\nstderr=%s", err, showErr.String())
	}
	for _, want := range []string{"q-job-quarantined-restore", "Actions:", "agent-team job queue quarantine restore squ-123", "Payload:", "SQU-123"} {
		if !strings.Contains(showOut.String(), want) {
			t.Fatalf("show output missing %q:\n%s", want, showOut.String())
		}
	}

	showCommands := NewRootCmd()
	showCommandsOut, showCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	showCommands.SetOut(showCommandsOut)
	showCommands.SetErr(showCommandsErr)
	showCommands.SetArgs([]string{"job", "queue", "quarantine", "show", "SQU-123", restorePath, "--repo", tmp, "--commands"})
	if err := showCommands.Execute(); err != nil {
		t.Fatalf("job queue quarantine show --commands: %v\nstderr=%s", err, showCommandsErr.String())
	}
	wantCommands := strings.Join(scopedOperatorActions([]string{
		"agent-team job queue quarantine restore squ-123 " + restorePath,
		"agent-team job queue quarantine drop squ-123 " + restorePath,
	}, operatorCommandScope{Repo: tmp, Set: true}), "\n") + "\n"
	if got := showCommandsOut.String(); got != wantCommands {
		t.Fatalf("job queue quarantine show --commands = %q, want %q", got, wantCommands)
	}

	restoreAllDry := NewRootCmd()
	restoreAllDryOut, restoreAllDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	restoreAllDry.SetOut(restoreAllDryOut)
	restoreAllDry.SetErr(restoreAllDryErr)
	restoreAllDry.SetArgs([]string{"job", "queue", "quarantine", "restore", "SQU-123", "--repo", tmp, "--all", "--state", "pending", "--dry-run", "--format", "{{.ID}} {{.Action}} {{.DryRun}}"})
	if err := restoreAllDry.Execute(); err != nil {
		t.Fatalf("job queue quarantine restore --all dry-run: %v\nstderr=%s", err, restoreAllDryErr.String())
	}
	if got, want := restoreAllDryOut.String(), "q-job-quarantined-restore would_restore true\n"; got != want {
		t.Fatalf("restore --all format = %q, want %q", got, want)
	}

	restoreAllCommands := NewRootCmd()
	restoreAllCommandsOut, restoreAllCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	restoreAllCommands.SetOut(restoreAllCommandsOut)
	restoreAllCommands.SetErr(restoreAllCommandsErr)
	restoreAllCommands.SetArgs([]string{"job", "queue", "quarantine", "restore", "SQU-123", "--repo", tmp, "--all", "--state", "pending", "--dry-run", "--commands"})
	if err := restoreAllCommands.Execute(); err != nil {
		t.Fatalf("job queue quarantine restore --all commands: %v\nstderr=%s", err, restoreAllCommandsErr.String())
	}
	wantRestoreAllCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "job", "queue", "quarantine", "restore", "squ-123", "--repo", tmp, "--all", "--state", "pending"}), " ")
	if got := strings.TrimSpace(restoreAllCommandsOut.String()); got != wantRestoreAllCommand {
		t.Fatalf("job queue quarantine restore --all commands = %q, want %q", got, wantRestoreAllCommand)
	}

	restoreCommands := NewRootCmd()
	restoreCommandsOut, restoreCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	restoreCommands.SetOut(restoreCommandsOut)
	restoreCommands.SetErr(restoreCommandsErr)
	restoreCommands.SetArgs([]string{"job", "queue", "quarantine", "restore", "SQU-123", restorePath, "--repo", tmp, "--dry-run", "--commands"})
	if err := restoreCommands.Execute(); err != nil {
		t.Fatalf("job queue quarantine restore commands: %v\nstderr=%s", err, restoreCommandsErr.String())
	}
	wantRestoreCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "job", "queue", "quarantine", "restore", "squ-123", restorePath, "--repo", tmp}), " ")
	if got := strings.TrimSpace(restoreCommandsOut.String()); got != wantRestoreCommand {
		t.Fatalf("job queue quarantine restore commands = %q, want %q", got, wantRestoreCommand)
	}

	dropAllCommands := NewRootCmd()
	dropAllCommandsOut, dropAllCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropAllCommands.SetOut(dropAllCommandsOut)
	dropAllCommands.SetErr(dropAllCommandsErr)
	dropAllCommands.SetArgs([]string{"job", "queue", "quarantine", "drop", "SQU-123", "--repo", tmp, "--all", "--state", "dead", "--restorable", "--dry-run", "--commands"})
	if err := dropAllCommands.Execute(); err != nil {
		t.Fatalf("job queue quarantine drop --all commands: %v\nstderr=%s", err, dropAllCommandsErr.String())
	}
	wantDropAllCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "job", "queue", "quarantine", "drop", "squ-123", "--repo", tmp, "--all", "--state", "dead", "--restorable"}), " ")
	if got := strings.TrimSpace(dropAllCommandsOut.String()); got != wantDropAllCommand {
		t.Fatalf("job queue quarantine drop --all commands = %q, want %q", got, wantDropAllCommand)
	}

	restore := NewRootCmd()
	restoreOut, restoreErr := &bytes.Buffer{}, &bytes.Buffer{}
	restore.SetOut(restoreOut)
	restore.SetErr(restoreErr)
	restore.SetArgs([]string{"job", "queue", "quarantine", "restore", "SQU-123", restorePath, "--repo", tmp, "--json"})
	if err := restore.Execute(); err != nil {
		t.Fatalf("job queue quarantine restore: %v\nstderr=%s", err, restoreErr.String())
	}
	var restored queueQuarantineRestoreResult
	if err := json.Unmarshal(restoreOut.Bytes(), &restored); err != nil {
		t.Fatalf("decode restore: %v\nbody=%s", err, restoreOut.String())
	}
	if restored.ID != "q-job-quarantined-restore" || restored.Action != "restored" {
		t.Fatalf("restored = %+v", restored)
	}
	if _, err := os.Stat(filepath.Join(queueRoot, restorePath)); !os.IsNotExist(err) {
		t.Fatalf("restore source still exists: %v", err)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-quarantined-restore"); err != nil {
		t.Fatalf("restored active item missing: %v", err)
	}

	dropOther := NewRootCmd()
	dropOtherOut, dropOtherErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropOther.SetOut(dropOtherOut)
	dropOther.SetErr(dropOtherErr)
	dropOther.SetArgs([]string{"job", "queue", "quarantine", "drop", "SQU-123", otherPath, "--repo", tmp, "--dry-run"})
	if err := dropOther.Execute(); err == nil {
		t.Fatalf("job queue quarantine drop unrelated file unexpectedly succeeded: stdout=%s", dropOtherOut.String())
	}
	if !strings.Contains(dropOtherErr.String(), "not owned by job") {
		t.Fatalf("drop unrelated stderr = %q", dropOtherErr.String())
	}

	dropCommands := NewRootCmd()
	dropCommandsOut, dropCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropCommands.SetOut(dropCommandsOut)
	dropCommands.SetErr(dropCommandsErr)
	dropCommands.SetArgs([]string{"job", "queue", "quarantine", "drop", "SQU-123", dropPath, "--repo", tmp, "--dry-run", "--commands"})
	if err := dropCommands.Execute(); err != nil {
		t.Fatalf("job queue quarantine drop commands: %v\nstderr=%s", err, dropCommandsErr.String())
	}
	wantDropCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "job", "queue", "quarantine", "drop", "squ-123", dropPath, "--repo", tmp}), " ")
	if got := strings.TrimSpace(dropCommandsOut.String()); got != wantDropCommand {
		t.Fatalf("job queue quarantine drop commands = %q, want %q", got, wantDropCommand)
	}

	drop := NewRootCmd()
	dropOut, dropErr := &bytes.Buffer{}, &bytes.Buffer{}
	drop.SetOut(dropOut)
	drop.SetErr(dropErr)
	drop.SetArgs([]string{"job", "queue", "quarantine", "drop", "SQU-123", dropPath, "--repo", tmp, "--json"})
	if err := drop.Execute(); err != nil {
		t.Fatalf("job queue quarantine drop: %v\nstderr=%s", err, dropErr.String())
	}
	var dropped []queueQuarantineDropResult
	if err := json.Unmarshal(dropOut.Bytes(), &dropped); err != nil {
		t.Fatalf("decode drop: %v\nbody=%s", err, dropOut.String())
	}
	if len(dropped) != 1 || dropped[0].ID != "q-job-quarantined-drop" || dropped[0].Action != "dropped" {
		t.Fatalf("dropped = %+v", dropped)
	}
	if _, err := os.Stat(filepath.Join(queueRoot, dropPath)); !os.IsNotExist(err) {
		t.Fatalf("drop source still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(queueRoot, otherPath)); err != nil {
		t.Fatalf("unrelated quarantine file changed: %v", err)
	}
}

func TestJobQueuePruneScopesOwnedItems(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	j, err := job.New("SQU-122", "worker", "prune queued work", time.Now())
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.Instance = "worker-squ-122"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-job-old-dead",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-122",
			Payload: map[string]any{
				"job_id": "squ-122",
				"ticket": "SQU-122",
				"target": "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:      now.Add(-47 * time.Hour),
			DeadLetteredAt: now.Add(-47 * time.Hour),
		},
		{
			ID:         "q-job-new-dead",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-122",
			Payload: map[string]any{
				"job_id": "squ-122",
				"ticket": "SQU-122",
				"target": "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-time.Hour),
			UpdatedAt:      now.Add(-time.Hour),
			DeadLetteredAt: now.Add(-time.Hour),
		},
		{
			ID:         "q-job-old-pending",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-122",
			Payload: map[string]any{
				"job_id": "squ-122",
				"target": "worker",
			},
			QueuedAt:  now.Add(-48 * time.Hour),
			UpdatedAt: now.Add(-48 * time.Hour),
		},
		{
			ID:         "q-other-old-dead",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-123",
			Payload: map[string]any{
				"job_id": "squ-123",
				"ticket": "SQU-123",
				"target": "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:      now.Add(-47 * time.Hour),
			DeadLetteredAt: now.Add(-47 * time.Hour),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("WriteQueueItem %s: %v", item.ID, err)
		}
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"job", "queue", "prune", "SQU-122", "--repo", tmp, "--older-than", "24h", "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("job queue prune dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryResults []queuePruneResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryResults); err != nil {
		t.Fatalf("decode prune dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(dryResults) != 1 || dryResults[0].ID != "q-job-old-dead" || !dryResults[0].DryRun || dryResults[0].Dropped {
		t.Fatalf("prune dry-run results = %+v", dryResults)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-old-dead"); err != nil {
		t.Fatalf("dry-run removed queue item: %v", err)
	}

	pruneCommands := NewRootCmd()
	pruneCommandsOut, pruneCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	pruneCommands.SetOut(pruneCommandsOut)
	pruneCommands.SetErr(pruneCommandsErr)
	pruneCommands.SetArgs([]string{"job", "queue", "prune", "SQU-122", "--repo", tmp, "--older-than", "24h", "--dry-run", "--commands"})
	if err := pruneCommands.Execute(); err != nil {
		t.Fatalf("job queue prune commands: %v\nstderr=%s", err, pruneCommandsErr.String())
	}
	wantPruneCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "job", "queue", "prune", "SQU-122", "--repo", tmp, "--older-than", "24h0m0s"}), " ")
	if got := strings.TrimSpace(pruneCommandsOut.String()); got != wantPruneCommand {
		t.Fatalf("job queue prune commands = %q, want %q", got, wantPruneCommand)
	}

	prune := NewRootCmd()
	pruneOut, pruneErr := &bytes.Buffer{}, &bytes.Buffer{}
	prune.SetOut(pruneOut)
	prune.SetErr(pruneErr)
	prune.SetArgs([]string{"job", "queue", "prune", "SQU-122", "--repo", tmp, "--older-than", "24h", "--format", "{{.ID}} {{.State}} {{.Dropped}}"})
	if err := prune.Execute(); err != nil {
		t.Fatalf("job queue prune: %v\nstderr=%s", err, pruneErr.String())
	}
	if got, want := strings.TrimSpace(pruneOut.String()), "q-job-old-dead dead true"; got != want {
		t.Fatalf("prune output = %q, want %q", got, want)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-old-dead"); !os.IsNotExist(err) {
		t.Fatalf("old dead item err=%v, want not exist", err)
	}
	for _, id := range []string{"q-job-new-dead", "q-job-old-pending", "q-other-old-dead"} {
		if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), id); err != nil {
			t.Fatalf("queue item %s should remain: %v", id, err)
		}
	}

	pruneAll := NewRootCmd()
	allOut, allErr := &bytes.Buffer{}, &bytes.Buffer{}
	pruneAll.SetOut(allOut)
	pruneAll.SetErr(allErr)
	pruneAll.SetArgs([]string{"job", "queue", "prune", "SQU-122", "--repo", tmp, "--state", "all", "--older-than", "24h", "--dry-run", "--format", "{{.ID}} {{.DryRun}}"})
	if err := pruneAll.Execute(); err != nil {
		t.Fatalf("job queue prune all dry-run: %v\nstderr=%s", err, allErr.String())
	}
	if got, want := strings.TrimSpace(allOut.String()), "q-job-old-pending true"; got != want {
		t.Fatalf("prune all output = %q, want %q", got, want)
	}
}

func TestJobQueuePruneRuntimeFiltersItems(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	j, err := job.New("SQU-124", "worker", "runtime scoped cleanup", time.Now())
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.Instance = "worker-squ-124"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	now := time.Now().UTC()
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-job-old-codex",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-124",
			Payload: map[string]any{
				"job_id":  "squ-124",
				"runtime": "codex",
				"ticket":  "SQU-124",
				"target":  "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:      now.Add(-47 * time.Hour),
			DeadLetteredAt: now.Add(-47 * time.Hour),
		},
		{
			ID:         "q-job-old-claude",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-124-claude",
			Payload: map[string]any{
				"job_id":  "squ-124",
				"runtime": "claude",
				"ticket":  "SQU-124",
				"target":  "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:      now.Add(-47 * time.Hour),
			DeadLetteredAt: now.Add(-47 * time.Hour),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("WriteQueueItem %s: %v", item.ID, err)
		}
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"job", "queue", "prune", "SQU-124", "--repo", tmp, "--older-than", "24h", "--runtime", "codex", "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("job queue prune runtime dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryResults []queuePruneResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryResults); err != nil {
		t.Fatalf("decode job runtime prune dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(dryResults) != 1 || dryResults[0].ID != "q-job-old-codex" || !dryResults[0].DryRun || dryResults[0].Dropped {
		t.Fatalf("job runtime dry-run results = %+v", dryResults)
	}

	prune := NewRootCmd()
	pruneOut, pruneErr := &bytes.Buffer{}, &bytes.Buffer{}
	prune.SetOut(pruneOut)
	prune.SetErr(pruneErr)
	prune.SetArgs([]string{"job", "queue", "prune", "SQU-124", "--repo", tmp, "--older-than", "24h", "--runtime", "codex", "--json"})
	if err := prune.Execute(); err != nil {
		t.Fatalf("job queue prune runtime: %v\nstderr=%s", err, pruneErr.String())
	}
	var pruneResults []queuePruneResult
	if err := json.Unmarshal(pruneOut.Bytes(), &pruneResults); err != nil {
		t.Fatalf("decode job runtime prune: %v\nbody=%s", err, pruneOut.String())
	}
	if len(pruneResults) != 1 || pruneResults[0].ID != "q-job-old-codex" || !pruneResults[0].Dropped {
		t.Fatalf("job runtime prune results = %+v", pruneResults)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-old-codex"); !os.IsNotExist(err) {
		t.Fatalf("codex job item err=%v, want not exist", err)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-old-claude"); err != nil {
		t.Fatalf("claude job item should remain: %v", err)
	}
}

func TestJobQueuePruneEventTypeFiltersItems(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	j, err := job.New("SQU-125", "worker", "event scoped cleanup", time.Now())
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.Instance = "worker-squ-125"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	now := time.Now().UTC()
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-job-dispatch",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-125",
			Payload: map[string]any{
				"job_id": "squ-125",
				"ticket": "SQU-125",
				"target": "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:      now.Add(-47 * time.Hour),
			DeadLetteredAt: now.Add(-47 * time.Hour),
		},
		{
			ID:         "q-job-schedule",
			State:      daemon.QueueStateDead,
			EventType:  "schedule.fire",
			Instance:   "worker",
			InstanceID: "worker-squ-125-schedule",
			Payload: map[string]any{
				"job_id": "squ-125",
				"ticket": "SQU-125",
				"target": "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:      now.Add(-47 * time.Hour),
			DeadLetteredAt: now.Add(-47 * time.Hour),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("WriteQueueItem %s: %v", item.ID, err)
		}
	}

	prune := NewRootCmd()
	pruneOut, pruneErr := &bytes.Buffer{}, &bytes.Buffer{}
	prune.SetOut(pruneOut)
	prune.SetErr(pruneErr)
	prune.SetArgs([]string{"job", "queue", "prune", "SQU-125", "--repo", tmp, "--event-type", "agent.dispatch", "--format", "{{.ID}} {{.Dropped}}"})
	if err := prune.Execute(); err != nil {
		t.Fatalf("job queue prune event filter: %v\nstderr=%s", err, pruneErr.String())
	}
	if got, want := strings.TrimSpace(pruneOut.String()), "q-job-dispatch true"; got != want {
		t.Fatalf("event filtered prune output = %q, want %q", got, want)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-dispatch"); !os.IsNotExist(err) {
		t.Fatalf("dispatch item err=%v, want not exist", err)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-schedule"); err != nil {
		t.Fatalf("schedule item should remain: %v", err)
	}
}

func TestJobQueuePruneReadyDefaultsToPendingDueItems(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	j, err := job.New("SQU-126", "worker", "ready scoped cleanup", time.Now())
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.Instance = "worker-squ-126"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	now := time.Now().UTC()
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-job-ready",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-126",
			Payload: map[string]any{
				"job_id": "squ-126",
				"ticket": "SQU-126",
				"target": "worker",
			},
			NextRetry: now.Add(-time.Minute),
			QueuedAt:  now.Add(-time.Hour),
			UpdatedAt: now.Add(-time.Hour),
		},
		{
			ID:         "q-job-delayed",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-126",
			Payload: map[string]any{
				"job_id": "squ-126",
				"ticket": "SQU-126",
				"target": "worker",
			},
			NextRetry: now.Add(time.Hour),
			QueuedAt:  now.Add(-time.Hour),
			UpdatedAt: now.Add(-time.Hour),
		},
		{
			ID:         "q-job-dead",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-126",
			Payload: map[string]any{
				"job_id": "squ-126",
				"ticket": "SQU-126",
				"target": "worker",
			},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:      now.Add(-47 * time.Hour),
			DeadLetteredAt: now.Add(-47 * time.Hour),
		},
		{
			ID:         "q-other-ready",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-127",
			Payload: map[string]any{
				"job_id": "squ-127",
				"ticket": "SQU-127",
				"target": "worker",
			},
			NextRetry: now.Add(-time.Minute),
			QueuedAt:  now.Add(-time.Hour),
			UpdatedAt: now.Add(-time.Hour),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("WriteQueueItem %s: %v", item.ID, err)
		}
	}

	prune := NewRootCmd()
	pruneOut, pruneErr := &bytes.Buffer{}, &bytes.Buffer{}
	prune.SetOut(pruneOut)
	prune.SetErr(pruneErr)
	prune.SetArgs([]string{"job", "queue", "prune", "SQU-126", "--repo", tmp, "--ready", "--format", "{{.ID}} {{.State}} {{.Dropped}}"})
	if err := prune.Execute(); err != nil {
		t.Fatalf("job queue prune ready: %v\nstderr=%s", err, pruneErr.String())
	}
	if got, want := strings.TrimSpace(pruneOut.String()), "q-job-ready pending true"; got != want {
		t.Fatalf("ready prune output = %q, want %q", got, want)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-job-ready"); !os.IsNotExist(err) {
		t.Fatalf("ready item err=%v, want not exist", err)
	}
	for _, id := range []string{"q-job-delayed", "q-job-dead", "q-other-ready"} {
		if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), id); err != nil {
			t.Fatalf("queue item %s should remain: %v", id, err)
		}
	}
}

func TestJobQueueRejectsFormatCombinations(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "format with json",
			args: []string{"job", "queue", "SQU-120", "--format", "{{.ID}}", "--json"},
			want: "--format cannot be combined with --json",
		},
		{
			name: "format with summary",
			args: []string{"job", "queue", "SQU-120", "--format", "{{.ID}}", "--summary"},
			want: "--format cannot be combined with --summary",
		},
		{
			name: "commands with json",
			args: []string{"job", "queue", "SQU-120", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "commands with format",
			args: []string{"job", "queue", "SQU-120", "--commands", "--format", "{{.ID}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "commands with summary",
			args: []string{"job", "queue", "SQU-120", "--commands", "--summary"},
			want: "--commands cannot be combined with --summary",
		},
		{
			name: "commands with watch",
			args: []string{"job", "queue", "SQU-120", "--commands", "--watch"},
			want: "--commands cannot be combined with --watch",
		},
		{
			name: "invalid format",
			args: []string{"job", "queue", "SQU-120", "--format", "{{"},
			want: "invalid --format template",
		},
		{
			name: "show commands with json",
			args: []string{"job", "queue", "show", "SQU-120", "q-job-dead", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "show commands with format",
			args: []string{"job", "queue", "show", "SQU-120", "q-job-dead", "--commands", "--format", "{{.ID}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "quarantine show commands with json",
			args: []string{"job", "queue", "quarantine", "show", "SQU-120", "quarantine/pending/q.json", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "quarantine show commands with format",
			args: []string{"job", "queue", "quarantine", "show", "SQU-120", "quarantine/pending/q.json", "--commands", "--format", "{{.ID}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "invalid state",
			args: []string{"job", "queue", "SQU-120", "--state", "stuck"},
			want: "--state must be pending or dead",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("job queue validation succeeded: stdout=%s", out.String())
			}
			var code ExitCode
			if !errors.As(err, &code) || int(code) != 2 {
				t.Fatalf("job queue err = %v, want exit code 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestJobQueuePruneRejectsFormatCombinations(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "format with json",
			args: []string{"job", "queue", "prune", "SQU-122", "--format", "{{.ID}}", "--json"},
			want: "--format cannot be combined with --json",
		},
		{
			name: "commands without dry run",
			args: []string{"job", "queue", "prune", "SQU-122", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "commands with json",
			args: []string{"job", "queue", "prune", "SQU-122", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "commands with format",
			args: []string{"job", "queue", "prune", "SQU-122", "--dry-run", "--commands", "--format", "{{.ID}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "invalid format",
			args: []string{"job", "queue", "prune", "SQU-122", "--format", "{{"},
			want: "invalid --format template",
		},
		{
			name: "negative older than",
			args: []string{"job", "queue", "prune", "SQU-122", "--older-than", "-1s"},
			want: "--older-than must be >= 0",
		},
		{
			name: "negative limit",
			args: []string{"job", "queue", "prune", "SQU-122", "--limit", "-1"},
			want: "--limit must be >= 0",
		},
		{
			name: "invalid state",
			args: []string{"job", "queue", "prune", "SQU-122", "--state", "stuck"},
			want: "--state must be dead, pending, or all",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("job queue prune validation succeeded: stdout=%s", out.String())
			}
			var code ExitCode
			if !errors.As(err, &code) || int(code) != 2 {
				t.Fatalf("job queue prune err = %v, want exit code 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestJobQueueRetryDropRejectsFormatCombinations(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "retry format with json",
			args: []string{"job", "queue", "retry", "SQU-121", "--format", "{{.ID}}", "--json"},
			want: "--format cannot be combined with --json",
		},
		{
			name: "retry commands without dry run",
			args: []string{"job", "queue", "retry", "SQU-121", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "retry commands with json",
			args: []string{"job", "queue", "retry", "SQU-121", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "retry commands with format",
			args: []string{"job", "queue", "retry", "SQU-121", "--dry-run", "--commands", "--format", "{{.ID}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "retry invalid format",
			args: []string{"job", "queue", "retry", "SQU-121", "--format", "{{"},
			want: "invalid --format template",
		},
		{
			name: "retry negative limit",
			args: []string{"job", "queue", "retry", "SQU-121", "--all", "--limit", "-1"},
			want: "--limit must be >= 0",
		},
		{
			name: "retry filter without all",
			args: []string{"job", "queue", "retry", "SQU-121", "q-job-dead", "--state", "dead"},
			want: "--state, --event-type, --runtime, --ready, --sort, and --limit require --all",
		},
		{
			name: "retry runtime without all",
			args: []string{"job", "queue", "retry", "SQU-121", "q-job-dead", "--runtime", "codex"},
			want: "--state, --event-type, --runtime, --ready, --sort, and --limit require --all",
		},
		{
			name: "drop format with json",
			args: []string{"job", "queue", "drop", "SQU-121", "--format", "{{.ID}}", "--json"},
			want: "--format cannot be combined with --json",
		},
		{
			name: "drop commands without dry run",
			args: []string{"job", "queue", "drop", "SQU-121", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "drop commands with json",
			args: []string{"job", "queue", "drop", "SQU-121", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "drop commands with format",
			args: []string{"job", "queue", "drop", "SQU-121", "--dry-run", "--commands", "--format", "{{.ID}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "drop invalid format",
			args: []string{"job", "queue", "drop", "SQU-121", "--format", "{{"},
			want: "invalid --format template",
		},
		{
			name: "drop negative limit",
			args: []string{"job", "queue", "drop", "SQU-121", "--all", "--limit", "-1"},
			want: "--limit must be >= 0",
		},
		{
			name: "drop filter without all",
			args: []string{"job", "queue", "drop", "SQU-121", "q-job-dead", "--ready"},
			want: "--state, --event-type, --runtime, --ready, --sort, and --limit require --all",
		},
		{
			name: "drop runtime without all",
			args: []string{"job", "queue", "drop", "SQU-121", "q-job-dead", "--runtime", "codex"},
			want: "--state, --event-type, --runtime, --ready, --sort, and --limit require --all",
		},
		{
			name: "quarantine restore commands without dry run",
			args: []string{"job", "queue", "quarantine", "restore", "SQU-121", "quarantine/20260619T000000.000000000Z/dead/q.json", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "quarantine restore commands with json",
			args: []string{"job", "queue", "quarantine", "restore", "SQU-121", "quarantine/20260619T000000.000000000Z/dead/q.json", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "quarantine restore commands with format",
			args: []string{"job", "queue", "quarantine", "restore", "SQU-121", "quarantine/20260619T000000.000000000Z/dead/q.json", "--dry-run", "--commands", "--format", "{{.ID}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "quarantine drop commands without dry run",
			args: []string{"job", "queue", "quarantine", "drop", "SQU-121", "quarantine/20260619T000000.000000000Z/dead/q.json", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "quarantine drop commands with json",
			args: []string{"job", "queue", "quarantine", "drop", "SQU-121", "quarantine/20260619T000000.000000000Z/dead/q.json", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "quarantine drop commands with format",
			args: []string{"job", "queue", "quarantine", "drop", "SQU-121", "quarantine/20260619T000000.000000000Z/dead/q.json", "--dry-run", "--commands", "--format", "{{.ID}}"},
			want: "--commands cannot be combined with --format",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("job queue control validation succeeded: stdout=%s", out.String())
			}
			var code ExitCode
			if !errors.As(err, &code) || int(code) != 2 {
				t.Fatalf("job queue control err = %v, want exit code 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}
