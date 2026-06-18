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
	"github.com/jamesaud/agent-team/internal/job"
)

func TestPipelineListAndShow(t *testing.T) {
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

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
after = ["implement"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ls := NewRootCmd()
	lsOut, lsErr := &bytes.Buffer{}, &bytes.Buffer{}
	ls.SetOut(lsOut)
	ls.SetErr(lsErr)
	ls.SetArgs([]string{"pipeline", "ls", "--repo", root})
	if err := ls.Execute(); err != nil {
		t.Fatalf("pipeline ls: %v\nstderr=%s", err, lsErr.String())
	}
	for _, want := range []string{"PIPELINE", "ticket_to_pr", "ticket.created", "implement:worker", "review:manager after=implement"} {
		if !strings.Contains(lsOut.String(), want) {
			t.Fatalf("pipeline ls missing %q:\n%s", want, lsOut.String())
		}
	}

	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"pipeline", "show", "ticket_to_pr", "--repo", root})
	if err := show.Execute(); err != nil {
		t.Fatalf("pipeline show: %v\nstderr=%s", err, showErr.String())
	}
	for _, want := range []string{"Pipeline: ticket_to_pr", "Trigger:  ticket.created", "implement target=worker after=-", "review target=manager after=implement"} {
		if !strings.Contains(showOut.String(), want) {
			t.Fatalf("pipeline show missing %q:\n%s", want, showOut.String())
		}
	}

	asJSON := NewRootCmd()
	jsonOut, jsonErr := &bytes.Buffer{}, &bytes.Buffer{}
	asJSON.SetOut(jsonOut)
	asJSON.SetErr(jsonErr)
	asJSON.SetArgs([]string{"pipeline", "ls", "--repo", root, "--json"})
	if err := asJSON.Execute(); err != nil {
		t.Fatalf("pipeline ls json: %v\nstderr=%s", err, jsonErr.String())
	}
	var rows []pipelineInfo
	if err := json.Unmarshal(jsonOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline json: %v\nbody=%s", err, jsonOut.String())
	}
	if len(rows) != 1 || rows[0].Name != "ticket_to_pr" || len(rows[0].Steps) != 2 {
		t.Fatalf("pipeline rows = %+v", rows)
	}
}

func TestPipelineShowMissing(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "show", "missing", "--repo", root})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("pipeline show missing succeeded")
	}
	if !strings.Contains(stderr.String(), `pipeline "missing" not found`) {
		t.Fatalf("missing stderr = %q", stderr.String())
	}
}

func TestPipelineJobsListsMatchingJobs(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{ID: "squ-301", Ticket: "SQU-301", Target: "worker", Pipeline: "ticket_to_pr", Status: job.StatusRunning, CreatedAt: now, UpdatedAt: now},
		{ID: "squ-302", Ticket: "SQU-302", Target: "manager", Pipeline: "nightly", Status: job.StatusQueued, CreatedAt: now, UpdatedAt: now},
		{ID: "squ-303", Ticket: "SQU-303", Target: "manager", Pipeline: "ticket_to_pr", Status: job.StatusDone, CreatedAt: now, UpdatedAt: now},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	jsonCmd := NewRootCmd()
	jsonOut, jsonErr := &bytes.Buffer{}, &bytes.Buffer{}
	jsonCmd.SetOut(jsonOut)
	jsonCmd.SetErr(jsonErr)
	jsonCmd.SetArgs([]string{"pipeline", "jobs", "ticket_to_pr", "--repo", root, "--status", "running", "--json"})
	if err := jsonCmd.Execute(); err != nil {
		t.Fatalf("pipeline jobs json: %v\nstderr=%s", err, jsonErr.String())
	}
	var rows []job.Job
	if err := json.Unmarshal(jsonOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline jobs json: %v\nbody=%s", err, jsonOut.String())
	}
	if len(rows) != 1 || rows[0].ID != "squ-301" {
		t.Fatalf("pipeline job rows = %+v", rows)
	}

	formatCmd := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatCmd.SetOut(formatOut)
	formatCmd.SetErr(formatErr)
	formatCmd.SetArgs([]string{"pipeline", "jobs", "ticket_to_pr", "--repo", root, "--format", "{{.ID}} {{.Status}}"})
	if err := formatCmd.Execute(); err != nil {
		t.Fatalf("pipeline jobs format: %v\nstderr=%s", err, formatErr.String())
	}
	if got := strings.Split(strings.TrimSpace(formatOut.String()), "\n"); strings.Join(got, ",") != "squ-301 running,squ-303 done" {
		t.Fatalf("pipeline jobs format output = %q", formatOut.String())
	}

	invalid := NewRootCmd()
	invalidOut, invalidErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalid.SetOut(invalidOut)
	invalid.SetErr(invalidErr)
	invalid.SetArgs([]string{"pipeline", "jobs", "ticket_to_pr", "--repo", root, "--status", "waiting"})
	if err := invalid.Execute(); err == nil {
		t.Fatalf("pipeline jobs invalid status succeeded")
	}
	if !strings.Contains(invalidErr.String(), "unknown job status") {
		t.Fatalf("invalid status stderr = %q", invalidErr.String())
	}
}

func TestPipelineStatusSummarizesJobs(t *testing.T) {
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

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
after = ["implement"]

[pipelines.nightly]
trigger.event = "schedule"
trigger.match.name = "nightly"

[[pipelines.nightly.steps]]
id = "triage"
target = "manager"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-610",
			Ticket:    "SQU-610",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusDone},
				{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}},
			},
		},
		{
			ID:        "squ-611",
			Ticket:    "SQU-611",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusFailed,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusFailed},
			},
		},
		{
			ID:        "squ-612",
			Ticket:    "SQU-612",
			Target:    "manager",
			Pipeline:  "nightly",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "triage", Target: "manager", Status: job.StatusQueued},
			},
		},
		{
			ID:        "squ-613",
			Ticket:    "SQU-613",
			Target:    "worker",
			Pipeline:  "ad_hoc",
			Status:    job.StatusDone,
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "status", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline status json: %v\nstderr=%s", err, stderr.String())
	}
	var rows []pipelineStatusRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline status json: %v\nbody=%s", err, out.String())
	}
	byName := map[string]pipelineStatusRow{}
	for _, row := range rows {
		byName[row.Pipeline] = row
	}
	if len(byName) != 3 {
		t.Fatalf("status rows = %+v", rows)
	}
	ticket := byName["ticket_to_pr"]
	if !ticket.Declared || ticket.Steps != 2 || ticket.Jobs != 2 || ticket.Running != 1 || ticket.Failed != 1 || ticket.ReadySteps != 1 || ticket.FailedSteps != 1 {
		t.Fatalf("ticket status = %+v", ticket)
	}
	if !containsString(ticket.Actions, "agent-team pipeline advance ticket_to_pr --dry-run --preview-routes") || !containsString(ticket.Actions, "agent-team pipeline ready ticket_to_pr --state failed") {
		t.Fatalf("ticket actions = %+v", ticket.Actions)
	}
	nightly := byName["nightly"]
	if !nightly.Declared || nightly.Steps != 1 || nightly.Jobs != 1 || nightly.Queued != 1 || nightly.QueuedSteps != 1 {
		t.Fatalf("nightly status = %+v", nightly)
	}
	if !containsString(nightly.Actions, "agent-team tick") {
		t.Fatalf("nightly actions = %+v", nightly.Actions)
	}
	adHoc := byName["ad_hoc"]
	if adHoc.Declared || adHoc.Steps != 0 || adHoc.Jobs != 1 || adHoc.Done != 1 || adHoc.NoStep != 1 {
		t.Fatalf("ad_hoc status = %+v", adHoc)
	}

	one := NewRootCmd()
	oneOut, oneErr := &bytes.Buffer{}, &bytes.Buffer{}
	one.SetOut(oneOut)
	one.SetErr(oneErr)
	one.SetArgs([]string{"pipeline", "status", "ticket_to_pr", "--repo", root, "--format", "{{.Pipeline}} {{.Jobs}} {{.ReadySteps}} {{.FailedSteps}}"})
	if err := one.Execute(); err != nil {
		t.Fatalf("pipeline status one format: %v\nstderr=%s", err, oneErr.String())
	}
	if got := strings.TrimSpace(oneOut.String()); got != "ticket_to_pr 2 1 1" {
		t.Fatalf("formatted pipeline status = %q", got)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"pipeline", "status", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("pipeline status text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"PIPELINE", "ACTION", "ticket_to_pr", "yes", "running=1,failed=1", "agent-team pipeline advance ticket_to_pr --dry-run --preview-routes", "ad_hoc", "no"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("pipeline status text missing %q:\n%s", want, textOut.String())
		}
	}

	invalid := NewRootCmd()
	invalidOut, invalidErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalid.SetOut(invalidOut)
	invalid.SetErr(invalidErr)
	invalid.SetArgs([]string{"pipeline", "status", "ticket_to_pr", "--all", "--repo", root})
	if err := invalid.Execute(); err == nil {
		t.Fatalf("pipeline status <pipeline> --all succeeded")
	}
	if !strings.Contains(invalidErr.String(), "--all cannot be combined") {
		t.Fatalf("invalid all stderr = %q", invalidErr.String())
	}

	missing := NewRootCmd()
	missingOut, missingErr := &bytes.Buffer{}, &bytes.Buffer{}
	missing.SetOut(missingOut)
	missing.SetErr(missingErr)
	missing.SetArgs([]string{"pipeline", "status", "missing", "--repo", root})
	if err := missing.Execute(); err == nil {
		t.Fatalf("pipeline status missing succeeded")
	}
	if !strings.Contains(missingErr.String(), `pipeline "missing" not found`) {
		t.Fatalf("missing stderr = %q", missingErr.String())
	}
}

func TestPipelineReadyListsMatchingReadyJobs(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-310",
			Ticket:    "SQU-310",
			Target:    "manager",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusDone},
				{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}},
			},
		},
		{
			ID:        "squ-311",
			Ticket:    "SQU-311",
			Target:    "manager",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-311"},
				{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}},
			},
		},
		{
			ID:        "squ-312",
			Ticket:    "SQU-312",
			Target:    "manager",
			Pipeline:  "nightly",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "triage", Target: "manager", Status: job.StatusBlocked},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "ready", "ticket_to_pr", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline ready json: %v\nstderr=%s", err, stderr.String())
	}
	var rows []jobReadyRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline ready json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].JobID != "squ-310" || rows[0].State != "ready" || rows[0].StepID != "review" {
		t.Fatalf("ready rows = %+v", rows)
	}

	format := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	format.SetOut(formatOut)
	format.SetErr(formatErr)
	format.SetArgs([]string{"pipeline", "ready", "ticket_to_pr", "--repo", root, "--state", "all", "--format", "{{.JobID}} {{.State}} {{.StepID}}"})
	if err := format.Execute(); err != nil {
		t.Fatalf("pipeline ready format: %v\nstderr=%s", err, formatErr.String())
	}
	if got := strings.Split(strings.TrimSpace(formatOut.String()), "\n"); strings.Join(got, ",") != "squ-310 ready review,squ-311 running implement" {
		t.Fatalf("pipeline ready format output = %q", formatOut.String())
	}

	all := NewRootCmd()
	allOut, allErr := &bytes.Buffer{}, &bytes.Buffer{}
	all.SetOut(allOut)
	all.SetErr(allErr)
	all.SetArgs([]string{"pipeline", "ready", "--all", "--repo", root, "--json"})
	if err := all.Execute(); err != nil {
		t.Fatalf("pipeline ready --all json: %v\nstderr=%s", err, allErr.String())
	}
	var allRows []jobReadyRow
	if err := json.Unmarshal(allOut.Bytes(), &allRows); err != nil {
		t.Fatalf("decode pipeline ready all json: %v\nbody=%s", err, allOut.String())
	}
	if len(allRows) != 2 || allRows[0].JobID != "squ-310" || allRows[1].JobID != "squ-312" || allRows[1].Pipeline != "nightly" {
		t.Fatalf("all ready rows = %+v", allRows)
	}

	invalidAll := NewRootCmd()
	invalidAllOut, invalidAllErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidAll.SetOut(invalidAllOut)
	invalidAll.SetErr(invalidAllErr)
	invalidAll.SetArgs([]string{"pipeline", "ready", "ticket_to_pr", "--all", "--repo", root})
	if err := invalidAll.Execute(); err == nil {
		t.Fatalf("pipeline ready <pipeline> --all succeeded")
	}
	if !strings.Contains(invalidAllErr.String(), "--all cannot be combined") {
		t.Fatalf("invalid all stderr = %q", invalidAllErr.String())
	}

	invalid := NewRootCmd()
	invalidOut, invalidErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalid.SetOut(invalidOut)
	invalid.SetErr(invalidErr)
	invalid.SetArgs([]string{"pipeline", "ready", "ticket_to_pr", "--repo", root, "--state", ","})
	if err := invalid.Execute(); err == nil {
		t.Fatalf("pipeline ready empty state succeeded")
	}
	if !strings.Contains(invalidErr.String(), "--state requires at least one non-empty state") {
		t.Fatalf("invalid state stderr = %q", invalidErr.String())
	}
}

func TestPipelineRunCreatesDurableJob(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)
	teamDir := filepath.Join(root, ".agent_team")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"pipeline", "run", "ticket_to_pr", "https://linear.app/squirtlesquad/issue/SQU-304/run-pipeline",
		"--repo", root,
		"--kickoff", "run pipeline",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline run: %v\nstderr=%s", err, stderr.String())
	}
	var created job.Job
	if err := json.Unmarshal(out.Bytes(), &created); err != nil {
		t.Fatalf("decode pipeline run json: %v\nbody=%s", err, out.String())
	}
	if created.ID != "squ-304" || created.Pipeline != "ticket_to_pr" || created.Target != "worker" || len(created.Steps) != 2 {
		t.Fatalf("created job = %+v", created)
	}
	if created.TicketURL != "https://linear.app/squirtlesquad/issue/SQU-304/run-pipeline" {
		t.Fatalf("created ticket_url = %q", created.TicketURL)
	}
	if created.Steps[0].ID != "implement" || created.Steps[0].Status != job.StatusQueued {
		t.Fatalf("first step = %+v", created.Steps[0])
	}
	if created.Steps[1].ID != "review" || created.Steps[1].Status != job.StatusBlocked {
		t.Fatalf("second step = %+v", created.Steps[1])
	}
	events, err := job.ListEvents(teamDir, "squ-304")
	if err != nil {
		t.Fatalf("list pipeline run events: %v", err)
	}
	if len(events) != 1 || events[0].Data["pipeline"] != "ticket_to_pr" || events[0].Data["ticket_url"] != "https://linear.app/squirtlesquad/issue/SQU-304/run-pipeline" {
		t.Fatalf("events = %+v", events)
	}

	duplicate := NewRootCmd()
	dupOut, dupErr := &bytes.Buffer{}, &bytes.Buffer{}
	duplicate.SetOut(dupOut)
	duplicate.SetErr(dupErr)
	duplicate.SetArgs([]string{"pipeline", "run", "ticket_to_pr", "SQU-304", "--repo", root})
	if err := duplicate.Execute(); err == nil {
		t.Fatalf("pipeline run duplicate succeeded")
	}
	if !strings.Contains(dupErr.String(), `job "squ-304" already exists`) {
		t.Fatalf("duplicate stderr = %q", dupErr.String())
	}
}

func TestPipelineRunDryRunDoesNotWrite(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"pipeline", "run", "ticket_to_pr", "SQU-306",
		"--repo", root,
		"--kickoff", "preview pipeline",
		"--dry-run",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline run dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var preview jobCreatePreview
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil {
		t.Fatalf("decode pipeline run dry-run json: %v\nbody=%s", err, out.String())
	}
	if !preview.DryRun || preview.Job == nil || preview.Job.ID != "squ-306" || preview.Job.Pipeline != "ticket_to_pr" || len(preview.Job.Steps) != 2 {
		t.Fatalf("preview = %+v", preview)
	}
	if preview.Job.Steps[0].ID != "implement" || preview.Job.Steps[0].Status != job.StatusQueued || preview.Job.Steps[1].Status != job.StatusBlocked {
		t.Fatalf("preview steps = %+v", preview.Job.Steps)
	}
	if _, err := os.Stat(filepath.Join(root, ".agent_team", "jobs", "squ-306.toml")); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote pipeline job file, err=%v", err)
	}

	textCmd := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	textCmd.SetOut(textOut)
	textCmd.SetErr(textErr)
	textCmd.SetArgs([]string{"pipeline", "run", "ticket_to_pr", "SQU-307", "--repo", root, "--dry-run"})
	if err := textCmd.Execute(); err != nil {
		t.Fatalf("pipeline run dry-run text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"Dry run: true", "ID:          squ-307", "Pipeline:    ticket_to_pr", "implement"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("pipeline dry-run text missing %q:\n%s", want, textOut.String())
		}
	}

	dispatchCmd := NewRootCmd()
	dispatchOut, dispatchErr := &bytes.Buffer{}, &bytes.Buffer{}
	dispatchCmd.SetOut(dispatchOut)
	dispatchCmd.SetErr(dispatchErr)
	dispatchCmd.SetArgs([]string{"pipeline", "run", "ticket_to_pr", "SQU-308", "--repo", root, "--dry-run", "--dispatch", "--json"})
	if err := dispatchCmd.Execute(); err != nil {
		t.Fatalf("pipeline run --dry-run --dispatch: %v\nstderr=%s", err, dispatchErr.String())
	}
	var advancePreview jobAdvancePreview
	if err := json.Unmarshal(dispatchOut.Bytes(), &advancePreview); err != nil {
		t.Fatalf("decode pipeline dispatch dry-run json: %v\nbody=%s", err, dispatchOut.String())
	}
	if !advancePreview.DryRun || advancePreview.Job == nil || advancePreview.Job.ID != "squ-308" || advancePreview.Step == nil || advancePreview.Step.ID != "implement" {
		t.Fatalf("advance preview = %+v", advancePreview)
	}
	if advancePreview.Dispatch == nil || advancePreview.Dispatch.RequestedName != "worker-squ-308-implement" {
		t.Fatalf("dispatch preview = %+v", advancePreview.Dispatch)
	}
	payload := advancePreview.Dispatch.Preview.Payload
	if payload["pipeline"] != "ticket_to_pr" || payload["pipeline_step"] != "implement" || payload["job_id"] != "squ-308" {
		t.Fatalf("dispatch payload = %+v", payload)
	}
	if _, err := os.Stat(filepath.Join(root, ".agent_team", "jobs", "squ-308.toml")); !os.IsNotExist(err) {
		t.Fatalf("dispatch dry-run wrote pipeline job file, err=%v", err)
	}
}

func TestPipelineRunDispatchesFirstStep(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "agent-team-pipeline-run-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	initInto(t, root)
	teamDir := filepath.Join(root, ".agent_team")
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), fakeSpawnerForTest(t, 2*time.Second))
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "run", "ticket_to_pr", "SQU-305", "--repo", root, "--dispatch", "--workspace", "repo", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline run --dispatch: %v\nstderr=%s", err, stderr.String())
	}
	var result jobAdvanceResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode pipeline run dispatch json: %v\nbody=%s", err, out.String())
	}
	if result.Job == nil || result.Step == nil {
		t.Fatalf("result missing job/step = %+v", result)
	}
	if result.Job.Status != job.StatusRunning || result.Job.Pipeline != "ticket_to_pr" {
		t.Fatalf("advanced job = %+v", result.Job)
	}
	if result.Step.ID != "implement" || result.Step.Status != job.StatusRunning || result.Step.Instance != "worker-squ-305-implement" {
		t.Fatalf("advanced step = %+v", result.Step)
	}
}

func TestPipelineAdvanceDryRunAndDispatch(t *testing.T) {
	target, _, cleanup := setupIntakePipelineRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-401",
			Ticket:    "SQU-401",
			Target:    "manager",
			Kickoff:   "SQU-401: review implementation",
			Pipeline:  "ticket_triage",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "triage", Target: "manager", Status: job.StatusDone, Instance: "manager", StartedAt: now, FinishedAt: now},
				{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"triage"}},
			},
		},
		{
			ID:        "squ-402",
			Ticket:    "SQU-402",
			Target:    "manager",
			Kickoff:   "SQU-402: review implementation",
			Pipeline:  "ticket_triage",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "triage", Target: "manager", Status: job.StatusDone, Instance: "manager", StartedAt: now, FinishedAt: now},
				{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"triage"}},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"pipeline", "advance", "ticket_triage", "--repo", target, "--limit", "1", "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("pipeline advance dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview []pipelineAdvanceResult
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode advance dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(preview) != 1 || preview[0].JobID != "squ-401" || preview[0].StepID != "review" || preview[0].Action != "would_advance" || !preview[0].DryRun {
		t.Fatalf("preview = %+v", preview)
	}
	if preview[0].Preview != nil {
		t.Fatalf("plain dry-run unexpectedly included route preview = %+v", preview[0].Preview)
	}
	unchanged, err := job.Read(teamDir, "squ-401")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.Steps[1].Status != job.StatusBlocked {
		t.Fatalf("dry-run changed job = %+v", unchanged)
	}

	routeDry := NewRootCmd()
	routeDryOut, routeDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	routeDry.SetOut(routeDryOut)
	routeDry.SetErr(routeDryErr)
	routeDry.SetArgs([]string{"pipeline", "advance", "ticket_triage", "--repo", target, "--limit", "1", "--dry-run", "--preview-routes", "--json"})
	if err := routeDry.Execute(); err != nil {
		t.Fatalf("pipeline advance dry-run preview-routes: %v\nstderr=%s", err, routeDryErr.String())
	}
	var routePreview []pipelineAdvanceResult
	if err := json.Unmarshal(routeDryOut.Bytes(), &routePreview); err != nil {
		t.Fatalf("decode advance dry-run preview-routes: %v\nbody=%s", err, routeDryOut.String())
	}
	if len(routePreview) != 1 || routePreview[0].Preview == nil || routePreview[0].Preview.Step == nil || routePreview[0].Preview.Step.ID != "review" {
		t.Fatalf("route preview = %+v", routePreview)
	}
	if routePreview[0].Preview.Dispatch == nil || routePreview[0].Preview.Dispatch.RequestedName != "manager-squ-401-review" {
		t.Fatalf("route dispatch preview = %+v", routePreview[0].Preview.Dispatch)
	}
	dispatchPreview := routePreview[0].Preview.Dispatch.Preview
	if dispatchPreview == nil || dispatchPreview.Type != "agent.dispatch" || len(dispatchPreview.Matched) != 1 || dispatchPreview.Matched[0] != "manager" {
		t.Fatalf("dispatch route preview = %+v", dispatchPreview)
	}
	payload := dispatchPreview.Payload
	if payload["job_id"] != "squ-401" || payload["pipeline"] != "ticket_triage" || payload["pipeline_step"] != "review" || payload["workspace"] != "repo" {
		t.Fatalf("route preview payload = %+v", payload)
	}
	routeUnchanged, err := job.Read(teamDir, "squ-401")
	if err != nil {
		t.Fatalf("read route preview unchanged job: %v", err)
	}
	if routeUnchanged.Steps[1].Status != job.StatusBlocked {
		t.Fatalf("route preview changed job = %+v", routeUnchanged)
	}

	textDry := NewRootCmd()
	textDryOut, textDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	textDry.SetOut(textDryOut)
	textDry.SetErr(textDryErr)
	textDry.SetArgs([]string{"pipeline", "advance", "ticket_triage", "--repo", target, "--limit", "1", "--dry-run", "--preview-routes"})
	if err := textDry.Execute(); err != nil {
		t.Fatalf("pipeline advance dry-run preview-routes text: %v\nstderr=%s", err, textDryErr.String())
	}
	for _, want := range []string{"Routes:", "squ-401 step=review target=manager instance=manager-squ-401-review", "Matched: manager"} {
		if !strings.Contains(textDryOut.String(), want) {
			t.Fatalf("route preview text missing %q:\n%s", want, textDryOut.String())
		}
	}

	invalidRoutes := NewRootCmd()
	invalidRoutesOut, invalidRoutesErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidRoutes.SetOut(invalidRoutesOut)
	invalidRoutes.SetErr(invalidRoutesErr)
	invalidRoutes.SetArgs([]string{"pipeline", "advance", "ticket_triage", "--repo", target, "--preview-routes"})
	if err := invalidRoutes.Execute(); err == nil {
		t.Fatalf("pipeline advance --preview-routes without --dry-run succeeded")
	}
	if !strings.Contains(invalidRoutesErr.String(), "--preview-routes requires --dry-run") {
		t.Fatalf("invalid preview-routes stderr = %q", invalidRoutesErr.String())
	}

	run := NewRootCmd()
	runOut, runErr := &bytes.Buffer{}, &bytes.Buffer{}
	run.SetOut(runOut)
	run.SetErr(runErr)
	run.SetArgs([]string{"pipeline", "advance", "ticket_triage", "--repo", target, "--limit", "1", "--json"})
	if err := run.Execute(); err != nil {
		t.Fatalf("pipeline advance: %v\nstderr=%s", err, runErr.String())
	}
	var advanced []pipelineAdvanceResult
	if err := json.Unmarshal(runOut.Bytes(), &advanced); err != nil {
		t.Fatalf("decode advance json: %v\nbody=%s", err, runOut.String())
	}
	if len(advanced) != 1 || advanced[0].JobID != "squ-401" || advanced[0].Action != "advanced" || advanced[0].StepStatus != job.StatusRunning || advanced[0].Instance != "manager" {
		t.Fatalf("advanced = %+v", advanced)
	}
	first, err := job.Read(teamDir, "squ-401")
	if err != nil {
		t.Fatalf("read first job: %v", err)
	}
	second, err := job.Read(teamDir, "squ-402")
	if err != nil {
		t.Fatalf("read second job: %v", err)
	}
	if first.Steps[1].Status != job.StatusRunning || second.Steps[1].Status != job.StatusBlocked {
		t.Fatalf("jobs after advance first=%+v second=%+v", first, second)
	}
}

func TestPipelineAdvanceAllDryRun(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-501",
			Ticket:    "SQU-501",
			Target:    "worker",
			Kickoff:   "SQU-501: implement",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusBlocked},
			},
		},
		{
			ID:        "squ-502",
			Ticket:    "SQU-502",
			Target:    "manager",
			Kickoff:   "SQU-502: triage",
			Pipeline:  "nightly",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "triage", Target: "manager", Status: job.StatusBlocked},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "advance", "--all", "--repo", root, "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline advance --all dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var preview []pipelineAdvanceResult
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil {
		t.Fatalf("decode advance all dry-run: %v\nbody=%s", err, out.String())
	}
	if len(preview) != 2 {
		t.Fatalf("preview len = %d, want 2: %+v", len(preview), preview)
	}
	got := preview[0].JobID + ":" + preview[0].Pipeline + "," + preview[1].JobID + ":" + preview[1].Pipeline
	if got != "squ-501:ticket_to_pr,squ-502:nightly" {
		t.Fatalf("preview order = %s, rows=%+v", got, preview)
	}

	invalid := NewRootCmd()
	invalidOut, invalidErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalid.SetOut(invalidOut)
	invalid.SetErr(invalidErr)
	invalid.SetArgs([]string{"pipeline", "advance", "ticket_to_pr", "--all", "--repo", root})
	if err := invalid.Execute(); err == nil {
		t.Fatal("pipeline advance <pipeline> --all succeeded")
	}
	if !strings.Contains(invalidErr.String(), "--all cannot be combined") {
		t.Fatalf("invalid stderr = %q", invalidErr.String())
	}
}
