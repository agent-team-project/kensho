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
	cmd.SetArgs([]string{"pipeline", "run", "ticket_to_pr", "SQU-304", "--repo", root, "--kickoff", "run pipeline", "--json"})
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
	if len(events) != 1 || events[0].Data["pipeline"] != "ticket_to_pr" {
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
	unchanged, err := job.Read(teamDir, "squ-401")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.Steps[1].Status != job.StatusBlocked {
		t.Fatalf("dry-run changed job = %+v", unchanged)
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
