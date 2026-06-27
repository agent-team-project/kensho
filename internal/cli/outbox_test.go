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
	"github.com/jamesaud/agent-team/internal/job"
)

func TestOutboxListShowRetryDrop(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	writeCLIOutboxItem(t, teamDir, &daemon.OutboxItem{
		ID:        "outbox-a",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Payload:   map[string]any{"job_id": "squ-501", "ticket": "SQU-501", "target": "worker"},
		Source:    "manager",
		CreatedAt: now,
		UpdatedAt: now,
	})
	writeCLIOutboxItem(t, teamDir, &daemon.OutboxItem{
		ID:        "outbox-b",
		State:     daemon.OutboxStateFailed,
		Type:      "agent.dispatch",
		Payload:   map[string]any{"job_id": "squ-502", "ticket": "SQU-502", "target": "worker"},
		Source:    "manager",
		LastError: "no route",
		CreatedAt: now.Add(time.Minute),
		UpdatedAt: now.Add(time.Minute),
	})

	out := runRootForOutboxTest(t, "outbox", "ls", "--target", target, "--json")
	var listed []*daemon.OutboxItem
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil {
		t.Fatalf("decode outbox ls: %v\n%s", err, out.String())
	}
	if len(listed) != 2 || listed[0].ID != "outbox-a" || listed[1].ID != "outbox-b" {
		t.Fatalf("outbox list = %+v, want outbox-a/outbox-b", listed)
	}

	filtered := runRootForOutboxTest(t, "outbox", "ls", "--target", target, "--state", "pending", "--job", "SQU-501", "--format", "{{.ID}} {{.State}}")
	if strings.TrimSpace(filtered.String()) != "outbox-a pending" {
		t.Fatalf("filtered output = %q", filtered.String())
	}

	shown := runRootForOutboxTest(t, "outbox", "show", "--target", target, "outbox-b", "--json")
	var shownItem daemon.OutboxItem
	if err := json.Unmarshal(shown.Bytes(), &shownItem); err != nil {
		t.Fatalf("decode outbox show: %v\n%s", err, shown.String())
	}
	if shownItem.ID != "outbox-b" || shownItem.State != daemon.OutboxStateFailed || shownItem.LastError != "no route" {
		t.Fatalf("shown item = %+v", shownItem)
	}

	retry := runRootForOutboxTest(t, "outbox", "retry", "--target", target, "outbox-b", "--json")
	var retryRows []outboxActionResult
	if err := json.Unmarshal(retry.Bytes(), &retryRows); err != nil {
		t.Fatalf("decode retry: %v\n%s", err, retry.String())
	}
	if len(retryRows) != 1 || retryRows[0].Action != "retried" || retryRows[0].State != daemon.OutboxStatePending {
		t.Fatalf("retry rows = %+v", retryRows)
	}
	retried, err := daemon.ReadOutboxItem(teamDir, "outbox-b")
	if err != nil {
		t.Fatalf("read retried item: %v", err)
	}
	if retried.State != daemon.OutboxStatePending || retried.LastError != "" {
		t.Fatalf("retried item = %+v, want pending with cleared error", retried)
	}

	drop := runRootForOutboxTest(t, "outbox", "drop", "--target", target, "outbox-a", "--json")
	var dropRows []outboxActionResult
	if err := json.Unmarshal(drop.Bytes(), &dropRows); err != nil {
		t.Fatalf("decode drop: %v\n%s", err, drop.String())
	}
	if len(dropRows) != 1 || dropRows[0].Action != "dropped" {
		t.Fatalf("drop rows = %+v", dropRows)
	}
	if _, err := daemon.ReadOutboxItem(teamDir, "outbox-a"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outbox-a should be removed, err=%v", err)
	}
}

func TestOutboxDrainDryRunOffline(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Date(2026, 6, 27, 12, 30, 0, 0, time.UTC)
	writeCLIOutboxItem(t, teamDir, &daemon.OutboxItem{
		ID:        "outbox-offline",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Payload:   map[string]any{"job_id": "squ-503", "target": "worker"},
		Source:    "manager",
		CreatedAt: now,
		UpdatedAt: now,
	})

	out := runRootForOutboxTest(t, "outbox", "drain", "--target", target, "--dry-run", "--json")
	var result daemon.OutboxDrainResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode drain dry-run: %v\n%s", err, out.String())
	}
	if !result.DryRun || result.WouldPublish != 1 || result.Pending != 1 || result.Published != 0 {
		t.Fatalf("dry-run result = %+v", result)
	}
	if _, err := daemon.ReadOutboxItem(teamDir, "outbox-offline"); err != nil {
		t.Fatalf("dry-run removed outbox item: %v", err)
	}
}

func TestOutboxDrainThroughDaemon(t *testing.T) {
	target, mgr, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Date(2026, 6, 27, 13, 0, 0, 0, time.UTC)
	writeCLIOutboxItem(t, teamDir, &daemon.OutboxItem{
		ID:        "outbox-daemon",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Payload:   map[string]any{"target": "worker", "name": "worker-squ-504", "ticket": "SQU-504", "workspace": "repo"},
		Source:    "manager",
		CreatedAt: now,
		UpdatedAt: now,
	})

	out := runRootForOutboxTest(t, "outbox", "drain", "--target", target, "--json")
	var result daemon.OutboxDrainResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode outbox drain: %v\n%s", err, out.String())
	}
	if result.Published != 1 || result.Pending != 0 || result.Processed != 1 {
		t.Fatalf("drain result = %+v", result)
	}
	processed, err := daemon.ReadOutboxItem(teamDir, "outbox-daemon")
	if err != nil {
		t.Fatalf("read processed item: %v", err)
	}
	if processed.State != daemon.OutboxStateProcessed {
		t.Fatalf("processed state = %s, want processed", processed.State)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-504")
}

func TestTeamOutboxScopesItemsAndActions(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.other]
agent = "other"

[[instances.other.triggers]]
event = "agent.dispatch"
match.target = "other"

[teams.delivery]
instances = ["manager", "worker"]

[teams.platform]
instances = ["other"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 27, 14, 0, 0, 0, time.UTC)
	for _, j := range []*job.Job{
		{
			ID:        "squ-901",
			Ticket:    "SQU-901",
			Target:    "worker",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "oth-901",
			Ticket:    "OTH-901",
			Target:    "other",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}
	for _, item := range []*daemon.OutboxItem{
		{
			ID:        "outbox-delivery-pending",
			State:     daemon.OutboxStatePending,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "squ-901", "ticket": "SQU-901", "target": "worker"},
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "outbox-delivery-failed",
			State:     daemon.OutboxStateFailed,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "squ-901", "ticket": "SQU-901", "target": "worker"},
			LastError: "route missing",
			CreatedAt: now.Add(time.Minute),
			UpdatedAt: now.Add(time.Minute),
		},
		{
			ID:        "outbox-platform-pending",
			State:     daemon.OutboxStatePending,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "oth-901", "ticket": "OTH-901", "target": "other"},
			CreatedAt: now.Add(2 * time.Minute),
			UpdatedAt: now.Add(2 * time.Minute),
		},
	} {
		writeCLIOutboxItem(t, teamDir, item)
	}

	out := runRootForOutboxTest(t, "team", "outbox", "delivery", "--repo", root, "--sort", "id", "--json")
	var listed []*daemon.OutboxItem
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil {
		t.Fatalf("decode team outbox list: %v\n%s", err, out.String())
	}
	if len(listed) != 2 || listed[0].ID != "outbox-delivery-failed" || listed[1].ID != "outbox-delivery-pending" {
		t.Fatalf("team outbox list = %+v", listed)
	}

	summaryOut := runRootForOutboxTest(t, "team", "outbox", "delivery", "--repo", root, "--summary", "--json")
	var summary outboxSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &summary); err != nil {
		t.Fatalf("decode team outbox summary: %v\n%s", err, summaryOut.String())
	}
	if summary.Total != 2 || summary.Pending != 1 || summary.Failed != 1 || summary.Filtered != 2 {
		t.Fatalf("team outbox summary = %+v", summary)
	}

	shown := runRootForOutboxTest(t, "team", "outbox", "show", "delivery", "outbox-delivery-failed", "--repo", root, "--json")
	var shownItem daemon.OutboxItem
	if err := json.Unmarshal(shown.Bytes(), &shownItem); err != nil {
		t.Fatalf("decode team outbox show: %v\n%s", err, shown.String())
	}
	if shownItem.ID != "outbox-delivery-failed" || shownItem.LastError != "route missing" {
		t.Fatalf("shown team outbox item = %+v", shownItem)
	}

	retry := runRootForOutboxTest(t, "team", "outbox", "retry", "delivery", "outbox-delivery-failed", "--repo", root, "--dry-run", "--json")
	var retryRows []outboxActionResult
	if err := json.Unmarshal(retry.Bytes(), &retryRows); err != nil {
		t.Fatalf("decode team outbox retry: %v\n%s", err, retry.String())
	}
	if len(retryRows) != 1 || retryRows[0].Action != "would_retry" || !retryRows[0].DryRun {
		t.Fatalf("retry rows = %+v", retryRows)
	}
	stillFailed, err := daemon.ReadOutboxItem(teamDir, "outbox-delivery-failed")
	if err != nil || stillFailed.State != daemon.OutboxStateFailed {
		t.Fatalf("dry-run retry changed item=%+v err=%v", stillFailed, err)
	}

	drop := runRootForOutboxTest(t, "team", "outbox", "drop", "delivery", "outbox-delivery-pending", "--repo", root, "--dry-run", "--json")
	var dropRows []outboxActionResult
	if err := json.Unmarshal(drop.Bytes(), &dropRows); err != nil {
		t.Fatalf("decode team outbox drop: %v\n%s", err, drop.String())
	}
	if len(dropRows) != 1 || dropRows[0].Action != "would_drop" || !dropRows[0].DryRun {
		t.Fatalf("drop rows = %+v", dropRows)
	}
	if _, err := daemon.ReadOutboxItem(teamDir, "outbox-delivery-pending"); err != nil {
		t.Fatalf("dry-run drop removed item: %v", err)
	}

	_, stderr, err := runRootForOutboxTestErr(t, "team", "outbox", "show", "delivery", "outbox-platform-pending", "--repo", root)
	if err == nil {
		t.Fatalf("out-of-team show succeeded")
	}
	if !strings.Contains(stderr.String(), `outbox item "outbox-platform-pending" is not owned by team "delivery"`) {
		t.Fatalf("out-of-team error = %q", stderr.String())
	}
}

func TestPipelineOutboxScopesItemsAndActions(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[pipelines.ops_review]
trigger.event = "ops.created"

[[pipelines.ops_review.steps]]
id = "implement"
target = "worker"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 27, 16, 0, 0, 0, time.UTC)
	for _, j := range []*job.Job{
		{
			ID:        "squ-902",
			Ticket:    "SQU-902",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "ops-902",
			Ticket:    "OPS-902",
			Target:    "worker",
			Pipeline:  "ops_review",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}
	for _, item := range []*daemon.OutboxItem{
		{
			ID:        "outbox-ticket-pending",
			State:     daemon.OutboxStatePending,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "squ-902", "ticket": "SQU-902", "target": "worker"},
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "outbox-ticket-failed",
			State:     daemon.OutboxStateFailed,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "squ-902", "ticket": "SQU-902", "target": "worker"},
			LastError: "route missing",
			CreatedAt: now.Add(time.Minute),
			UpdatedAt: now.Add(time.Minute),
		},
		{
			ID:        "outbox-ops-pending",
			State:     daemon.OutboxStatePending,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "ops-902", "ticket": "OPS-902", "target": "worker"},
			CreatedAt: now.Add(2 * time.Minute),
			UpdatedAt: now.Add(2 * time.Minute),
		},
	} {
		writeCLIOutboxItem(t, teamDir, item)
	}

	out := runRootForOutboxTest(t, "pipeline", "outbox", "ticket_to_pr", "--repo", root, "--sort", "id", "--json")
	var listed []*daemon.OutboxItem
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil {
		t.Fatalf("decode pipeline outbox list: %v\n%s", err, out.String())
	}
	if len(listed) != 2 || listed[0].ID != "outbox-ticket-failed" || listed[1].ID != "outbox-ticket-pending" {
		t.Fatalf("pipeline outbox list = %+v", listed)
	}

	allOut := runRootForOutboxTest(t, "pipeline", "outbox", "--repo", root, "--sort", "id", "--json")
	var allListed []*daemon.OutboxItem
	if err := json.Unmarshal(allOut.Bytes(), &allListed); err != nil {
		t.Fatalf("decode all pipeline outbox list: %v\n%s", err, allOut.String())
	}
	if len(allListed) != 3 {
		t.Fatalf("all pipeline outbox list = %+v", allListed)
	}

	summaryOut := runRootForOutboxTest(t, "pipeline", "outbox", "ticket_to_pr", "--repo", root, "--summary", "--json")
	var summary outboxSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &summary); err != nil {
		t.Fatalf("decode pipeline outbox summary: %v\n%s", err, summaryOut.String())
	}
	if summary.Total != 2 || summary.Pending != 1 || summary.Failed != 1 || summary.Filtered != 2 {
		t.Fatalf("pipeline outbox summary = %+v", summary)
	}

	shown := runRootForOutboxTest(t, "pipeline", "outbox", "show", "ticket_to_pr", "outbox-ticket-failed", "--repo", root, "--json")
	var shownItem daemon.OutboxItem
	if err := json.Unmarshal(shown.Bytes(), &shownItem); err != nil {
		t.Fatalf("decode pipeline outbox show: %v\n%s", err, shown.String())
	}
	if shownItem.ID != "outbox-ticket-failed" || shownItem.LastError != "route missing" {
		t.Fatalf("shown pipeline outbox item = %+v", shownItem)
	}

	retry := runRootForOutboxTest(t, "pipeline", "outbox", "retry", "ticket_to_pr", "outbox-ticket-failed", "--repo", root, "--dry-run", "--json")
	var retryRows []outboxActionResult
	if err := json.Unmarshal(retry.Bytes(), &retryRows); err != nil {
		t.Fatalf("decode pipeline outbox retry: %v\n%s", err, retry.String())
	}
	if len(retryRows) != 1 || retryRows[0].Action != "would_retry" || !retryRows[0].DryRun {
		t.Fatalf("retry rows = %+v", retryRows)
	}
	stillFailed, err := daemon.ReadOutboxItem(teamDir, "outbox-ticket-failed")
	if err != nil || stillFailed.State != daemon.OutboxStateFailed {
		t.Fatalf("dry-run retry changed item=%+v err=%v", stillFailed, err)
	}

	drop := runRootForOutboxTest(t, "pipeline", "outbox", "drop", "ticket_to_pr", "outbox-ticket-pending", "--repo", root, "--dry-run", "--json")
	var dropRows []outboxActionResult
	if err := json.Unmarshal(drop.Bytes(), &dropRows); err != nil {
		t.Fatalf("decode pipeline outbox drop: %v\n%s", err, drop.String())
	}
	if len(dropRows) != 1 || dropRows[0].Action != "would_drop" || !dropRows[0].DryRun {
		t.Fatalf("drop rows = %+v", dropRows)
	}
	if _, err := daemon.ReadOutboxItem(teamDir, "outbox-ticket-pending"); err != nil {
		t.Fatalf("dry-run drop removed item: %v", err)
	}

	_, stderr, err := runRootForOutboxTestErr(t, "pipeline", "outbox", "show", "ticket_to_pr", "outbox-ops-pending", "--repo", root)
	if err == nil {
		t.Fatalf("out-of-pipeline show succeeded")
	}
	if !strings.Contains(stderr.String(), `outbox item "outbox-ops-pending" is not owned by pipeline "ticket_to_pr"`) {
		t.Fatalf("out-of-pipeline error = %q", stderr.String())
	}
}

func TestJobOutboxScopesItemsAndActions(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 27, 17, 0, 0, 0, time.UTC)
	for _, j := range []*job.Job{
		{
			ID:        "squ-903",
			Ticket:    "SQU-903",
			Target:    "worker",
			Instance:  "worker-squ-903",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "ops-903",
			Ticket:    "OPS-903",
			Target:    "worker",
			Instance:  "worker-ops-903",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}
	for _, item := range []*daemon.OutboxItem{
		{
			ID:        "outbox-job-pending",
			State:     daemon.OutboxStatePending,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "squ-903", "ticket": "SQU-903", "target": "worker"},
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "outbox-job-failed",
			State:     daemon.OutboxStateFailed,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"ticket": "SQU-903", "target": "worker"},
			LastError: "route missing",
			CreatedAt: now.Add(time.Minute),
			UpdatedAt: now.Add(time.Minute),
		},
		{
			ID:        "outbox-other-pending",
			State:     daemon.OutboxStatePending,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "ops-903", "ticket": "OPS-903", "target": "worker"},
			CreatedAt: now.Add(2 * time.Minute),
			UpdatedAt: now.Add(2 * time.Minute),
		},
	} {
		writeCLIOutboxItem(t, teamDir, item)
	}

	out := runRootForOutboxTest(t, "job", "outbox", "SQU-903", "--repo", root, "--sort", "id", "--json")
	var listed []*daemon.OutboxItem
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil {
		t.Fatalf("decode job outbox list: %v\n%s", err, out.String())
	}
	if len(listed) != 2 || listed[0].ID != "outbox-job-failed" || listed[1].ID != "outbox-job-pending" {
		t.Fatalf("job outbox list = %+v", listed)
	}

	summaryOut := runRootForOutboxTest(t, "job", "outbox", "squ-903", "--repo", root, "--summary", "--json")
	var summary outboxSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &summary); err != nil {
		t.Fatalf("decode job outbox summary: %v\n%s", err, summaryOut.String())
	}
	if summary.Total != 2 || summary.Pending != 1 || summary.Failed != 1 || summary.Filtered != 2 {
		t.Fatalf("job outbox summary = %+v", summary)
	}

	shown := runRootForOutboxTest(t, "job", "outbox", "show", "squ-903", "outbox-job-failed", "--repo", root, "--json")
	var shownItem daemon.OutboxItem
	if err := json.Unmarshal(shown.Bytes(), &shownItem); err != nil {
		t.Fatalf("decode job outbox show: %v\n%s", err, shown.String())
	}
	if shownItem.ID != "outbox-job-failed" || shownItem.LastError != "route missing" {
		t.Fatalf("shown job outbox item = %+v", shownItem)
	}

	retry := runRootForOutboxTest(t, "job", "outbox", "retry", "squ-903", "outbox-job-failed", "--repo", root, "--dry-run", "--json")
	var retryRows []outboxActionResult
	if err := json.Unmarshal(retry.Bytes(), &retryRows); err != nil {
		t.Fatalf("decode job outbox retry: %v\n%s", err, retry.String())
	}
	if len(retryRows) != 1 || retryRows[0].Action != "would_retry" || !retryRows[0].DryRun {
		t.Fatalf("retry rows = %+v", retryRows)
	}
	stillFailed, err := daemon.ReadOutboxItem(teamDir, "outbox-job-failed")
	if err != nil || stillFailed.State != daemon.OutboxStateFailed {
		t.Fatalf("dry-run retry changed item=%+v err=%v", stillFailed, err)
	}

	drop := runRootForOutboxTest(t, "job", "outbox", "drop", "squ-903", "outbox-job-pending", "--repo", root, "--dry-run", "--json")
	var dropRows []outboxActionResult
	if err := json.Unmarshal(drop.Bytes(), &dropRows); err != nil {
		t.Fatalf("decode job outbox drop: %v\n%s", err, drop.String())
	}
	if len(dropRows) != 1 || dropRows[0].Action != "would_drop" || !dropRows[0].DryRun {
		t.Fatalf("drop rows = %+v", dropRows)
	}
	if _, err := daemon.ReadOutboxItem(teamDir, "outbox-job-pending"); err != nil {
		t.Fatalf("dry-run drop removed item: %v", err)
	}

	_, stderr, err := runRootForOutboxTestErr(t, "job", "outbox", "show", "squ-903", "outbox-other-pending", "--repo", root)
	if err == nil {
		t.Fatalf("out-of-job show succeeded")
	}
	if !strings.Contains(stderr.String(), `outbox item "outbox-other-pending" is not owned by job "squ-903"`) {
		t.Fatalf("out-of-job error = %q", stderr.String())
	}
}

func writeCLIOutboxItem(t *testing.T, teamDir string, item *daemon.OutboxItem) {
	t.Helper()
	if err := daemon.WriteOutboxItem(teamDir, item); err != nil {
		t.Fatalf("WriteOutboxItem(%s): %v", item.ID, err)
	}
}

func runRootForOutboxTest(t *testing.T, args ...string) *bytes.Buffer {
	t.Helper()
	out, stderr, err := runRootForOutboxTestErr(t, args...)
	if err != nil {
		t.Fatalf("agent-team %s: %v\nstderr=%s\nstdout=%s", strings.Join(args, " "), err, stderr.String(), out.String())
	}
	return out
}

func runRootForOutboxTestErr(t *testing.T, args ...string) (*bytes.Buffer, *bytes.Buffer, error) {
	t.Helper()
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out, stderr, err
}
