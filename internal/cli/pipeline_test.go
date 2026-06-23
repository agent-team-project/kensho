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

	formatList := NewRootCmd()
	formatListOut, formatListErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatList.SetOut(formatListOut)
	formatList.SetErr(formatListErr)
	formatList.SetArgs([]string{"pipeline", "ls", "--repo", root, "--format", "{{.Name}} {{len .Steps}}"})
	if err := formatList.Execute(); err != nil {
		t.Fatalf("pipeline ls format: %v\nstderr=%s", err, formatListErr.String())
	}
	if got, want := formatListOut.String(), "ticket_to_pr 2\n"; got != want {
		t.Fatalf("pipeline ls format output = %q, want %q", got, want)
	}

	formatShow := NewRootCmd()
	formatShowOut, formatShowErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatShow.SetOut(formatShowOut)
	formatShow.SetErr(formatShowErr)
	formatShow.SetArgs([]string{"pipeline", "show", "ticket_to_pr", "--repo", root, "--format", "{{.Name}} {{len .Steps}} {{range .Steps}}{{.ID}};{{end}}"})
	if err := formatShow.Execute(); err != nil {
		t.Fatalf("pipeline show format: %v\nstderr=%s", err, formatShowErr.String())
	}
	if got, want := formatShowOut.String(), "ticket_to_pr 2 implement;review;\n"; got != want {
		t.Fatalf("pipeline show format output = %q, want %q", got, want)
	}
}

func TestPipelineGraphFormats(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[[instances.manager.triggers]]
event        = "agent.dispatch"
match.target = "manager"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
after = ["implement"]

[[pipelines.ticket_to_pr.steps]]
id = "announce"
target = "manager"
after = ["review"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"pipeline", "graph", "ticket_to_pr", "--repo", root, "--routes"})
	if err := text.Execute(); err != nil {
		t.Fatalf("pipeline graph text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{
		"Pipeline: ticket_to_pr",
		"Trigger:  ticket.created",
		"implement target=worker after=- routes=worker",
		"review target=manager after=implement routes=manager",
		"<trigger> -> implement",
		"implement -> review",
		"review -> announce",
	} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("pipeline graph text missing %q:\n%s", want, textOut.String())
		}
	}

	mermaid := NewRootCmd()
	mermaidOut, mermaidErr := &bytes.Buffer{}, &bytes.Buffer{}
	mermaid.SetOut(mermaidOut)
	mermaid.SetErr(mermaidErr)
	mermaid.SetArgs([]string{"pipeline", "graph", "ticket_to_pr", "--repo", root, "--format", "mermaid"})
	if err := mermaid.Execute(); err != nil {
		t.Fatalf("pipeline graph mermaid: %v\nstderr=%s", err, mermaidErr.String())
	}
	for _, want := range []string{"flowchart TD", "trigger[\"trigger: ticket.created\"]", "step_1_implement", "--> step_2_review"} {
		if !strings.Contains(mermaidOut.String(), want) {
			t.Fatalf("pipeline graph mermaid missing %q:\n%s", want, mermaidOut.String())
		}
	}

	dot := NewRootCmd()
	dotOut, dotErr := &bytes.Buffer{}, &bytes.Buffer{}
	dot.SetOut(dotOut)
	dot.SetErr(dotErr)
	dot.SetArgs([]string{"pipeline", "graph", "ticket_to_pr", "--repo", root, "--format", "dot"})
	if err := dot.Execute(); err != nil {
		t.Fatalf("pipeline graph dot: %v\nstderr=%s", err, dotErr.String())
	}
	for _, want := range []string{`digraph "ticket_to_pr"`, `"trigger" -> "implement";`, `"implement" -> "review";`} {
		if !strings.Contains(dotOut.String(), want) {
			t.Fatalf("pipeline graph dot missing %q:\n%s", want, dotOut.String())
		}
	}

	asJSON := NewRootCmd()
	jsonOut, jsonErr := &bytes.Buffer{}, &bytes.Buffer{}
	asJSON.SetOut(jsonOut)
	asJSON.SetErr(jsonErr)
	asJSON.SetArgs([]string{"pipeline", "graph", "ticket_to_pr", "--repo", root, "--routes", "--json"})
	if err := asJSON.Execute(); err != nil {
		t.Fatalf("pipeline graph json: %v\nstderr=%s", err, jsonErr.String())
	}
	var graph pipelineGraph
	if err := json.Unmarshal(jsonOut.Bytes(), &graph); err != nil {
		t.Fatalf("decode graph json: %v\nbody=%s", err, jsonOut.String())
	}
	if graph.Name != "ticket_to_pr" || len(graph.Nodes) != 3 || len(graph.Edges) != 3 || len(graph.Nodes[0].Routes) != 1 {
		t.Fatalf("graph json = %+v", graph)
	}
}

func TestPipelineGraphValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"pipeline", "graph", "ticket_to_pr", "--format", "svg"}, "--format must be text, mermaid, or dot"},
		{[]string{"pipeline", "graph", "ticket_to_pr", "--format", "text", "--json"}, "--format cannot be combined with --json"},
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

func TestPipelineInfoFormatValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"pipeline", "ls", "--format", "{{.Name}}", "--json"}, "--format cannot be combined"},
		{[]string{"pipeline", "ls", "--format", "{{"}, "invalid --format template"},
		{[]string{"pipeline", "show", "ticket_to_pr", "--format", "{{.Name}}", "--json"}, "--format cannot be combined"},
		{[]string{"pipeline", "show", "ticket_to_pr", "--format", "{{"}, "invalid --format template"},
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

func TestPipelineDoctorReportsWorkflowHealth(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[[instances.manager.triggers]]
event        = "agent.dispatch"
match.target = "manager"

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

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "doctor", "ticket_to_pr", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline doctor json: %v\nstderr=%s", err, stderr.String())
	}
	var result pipelineDoctorResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode pipeline doctor json: %v\nbody=%s", err, out.String())
	}
	if !result.OK || len(result.Pipelines) != 1 || result.Pipelines[0].Name != "ticket_to_pr" || len(result.Problems) != 0 || len(result.Warnings) != 0 {
		t.Fatalf("doctor result = %+v", result)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"pipeline", "doctor", "ticket_to_pr", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("pipeline doctor text: %v\nstderr=%s", err, textErr.String())
	}
	if !strings.Contains(textOut.String(), "agent-team pipeline doctor: OK (ticket_to_pr)") {
		t.Fatalf("doctor text = %q", textOut.String())
	}

	format := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	format.SetOut(formatOut)
	format.SetErr(formatErr)
	format.SetArgs([]string{"pipeline", "doctor", "ticket_to_pr", "--repo", root, "--format", "{{.OK}} {{len .Pipelines}} {{len .Problems}} {{len .Warnings}}"})
	if err := format.Execute(); err != nil {
		t.Fatalf("pipeline doctor format: %v\nstderr=%s", err, formatErr.String())
	}
	if got, want := formatOut.String(), "true 1 0 0\n"; got != want {
		t.Fatalf("pipeline doctor format output = %q, want %q", got, want)
	}
}

func TestPipelineDoctorFindsWorkflowProblems(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.broken]
trigger.event = "schedule"
trigger.match.name = "weekly"

[[pipelines.broken.steps]]
id = "implement"
target = "worker"
after = ["review"]

[[pipelines.broken.steps]]
id = "review"
target = "ghost"
after = ["implement"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "doctor", "broken", "--repo", root, "--json"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("pipeline doctor unexpectedly succeeded")
	}
	var result pipelineDoctorResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode pipeline doctor json: %v\nbody=%s", err, out.String())
	}
	if result.OK || !hasPipelineDoctorFinding(result.Problems, "dependency_cycle") || !hasPipelineDoctorFinding(result.Problems, "target_has_no_dispatch_route") {
		t.Fatalf("doctor problems = %+v", result.Problems)
	}
	for _, code := range []string{"schedule_trigger_has_no_source", "first_step_has_dependencies"} {
		if !hasPipelineDoctorFinding(result.Warnings, code) {
			t.Fatalf("doctor warnings missing %s: %+v", code, result.Warnings)
		}
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"pipeline", "doctor", "broken", "--repo", root})
	if err := text.Execute(); err == nil {
		t.Fatal("pipeline doctor text unexpectedly succeeded")
	}
	if textOut.Len() != 0 {
		t.Fatalf("doctor failure wrote stdout = %q", textOut.String())
	}
	for _, want := range []string{"dependency cycle", `targets "ghost"`, "no declared schedule payload matches"} {
		if !strings.Contains(textErr.String(), want) {
			t.Fatalf("doctor stderr missing %q:\n%s", want, textErr.String())
		}
	}

	format := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	format.SetOut(formatOut)
	format.SetErr(formatErr)
	format.SetArgs([]string{"pipeline", "doctor", "broken", "--repo", root, "--format", "{{.OK}} {{len .Problems}} {{len .Warnings}}"})
	if err := format.Execute(); err == nil {
		t.Fatal("pipeline doctor format unexpectedly succeeded")
	}
	if got, want := formatOut.String(), "false 2 2\n"; got != want {
		t.Fatalf("pipeline doctor failure format output = %q, want %q", got, want)
	}
	if formatErr.Len() != 0 {
		t.Fatalf("pipeline doctor failure format stderr = %q", formatErr.String())
	}
}

func TestPipelineDoctorRejectsInvalidArguments(t *testing.T) {
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
	cmd.SetArgs([]string{"pipeline", "doctor", "ticket_to_pr", "--all", "--repo", root})
	if err := cmd.Execute(); err == nil {
		t.Fatal("pipeline doctor <pipeline> --all succeeded")
	}
	if !strings.Contains(stderr.String(), "--all cannot be combined") {
		t.Fatalf("invalid all stderr = %q", stderr.String())
	}

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"pipeline", "doctor", "--format", "{{.OK}}", "--json", "--repo", root}, "--format cannot be combined"},
		{[]string{"pipeline", "doctor", "--format", "{{", "--repo", root}, "invalid --format template"},
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
	if !containsString(ticket.Actions, "agent-team pipeline advance ticket_to_pr --dry-run --preview-routes") ||
		!containsString(ticket.Actions, "agent-team pipeline retry ticket_to_pr --dry-run --dispatch --preview-routes") ||
		!containsString(ticket.Actions, "agent-team repair --retry-pipelines --dry-run --preview-routes") ||
		!containsString(ticket.Actions, "agent-team pipeline ready ticket_to_pr --state failed") {
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
	for _, want := range []string{"PIPELINE", "ACTION", "ticket_to_pr", "yes", "running=1,failed=1", "agent-team pipeline advance ticket_to_pr --dry-run --preview-routes", "agent-team repair --retry-pipelines --dry-run --preview-routes", "ad_hoc", "no"} {
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

func TestPipelineManualGateRequiresOperatorApproval(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[[instances.manager.triggers]]
event        = "agent.dispatch"
match.target = "manager"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
after = ["implement"]
gate = "manual"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	create := NewRootCmd()
	createOut, createErr := &bytes.Buffer{}, &bytes.Buffer{}
	create.SetOut(createOut)
	create.SetErr(createErr)
	create.SetArgs([]string{"pipeline", "run", "ticket_to_pr", "SQU-901", "manual gate test", "--repo", root, "--json"})
	if err := create.Execute(); err != nil {
		t.Fatalf("pipeline run: %v\nstderr=%s", err, createErr.String())
	}
	j, err := job.Read(teamDir, "squ-901")
	if err != nil {
		t.Fatalf("read created job: %v", err)
	}
	if len(j.Steps) != 2 || j.Steps[1].ID != "review" || j.Steps[1].Gate != job.StepGateManual || j.Steps[1].Status != job.StatusBlocked {
		t.Fatalf("created steps = %+v", j.Steps)
	}

	markDone := NewRootCmd()
	markDoneOut, markDoneErr := &bytes.Buffer{}, &bytes.Buffer{}
	markDone.SetOut(markDoneOut)
	markDone.SetErr(markDoneErr)
	markDone.SetArgs([]string{"job", "step", "squ-901", "implement", "--status", "done", "--repo", root, "--json"})
	if err := markDone.Execute(); err != nil {
		t.Fatalf("mark implement done: %v\nstderr=%s", err, markDoneErr.String())
	}

	next := NewRootCmd()
	nextOut, nextErr := &bytes.Buffer{}, &bytes.Buffer{}
	next.SetOut(nextOut)
	next.SetErr(nextErr)
	next.SetArgs([]string{"job", "next", "squ-901", "--repo", root, "--json"})
	if err := next.Execute(); err != nil {
		t.Fatalf("job next gated: %v\nstderr=%s", err, nextErr.String())
	}
	var blocked jobNextResult
	if err := json.Unmarshal(nextOut.Bytes(), &blocked); err != nil {
		t.Fatalf("decode blocked next: %v\nbody=%s", err, nextOut.String())
	}
	if blocked.State != "blocked" || blocked.Step == nil || blocked.Step.ID != "review" || blocked.Step.Gate != job.StepGateManual || !strings.Contains(blocked.Message, "manual approval") {
		t.Fatalf("blocked next = %+v", blocked)
	}

	ready := NewRootCmd()
	readyOut, readyErr := &bytes.Buffer{}, &bytes.Buffer{}
	ready.SetOut(readyOut)
	ready.SetErr(readyErr)
	ready.SetArgs([]string{"job", "ready", "--repo", root, "--state", "blocked", "--json"})
	if err := ready.Execute(); err != nil {
		t.Fatalf("job ready blocked: %v\nstderr=%s", err, readyErr.String())
	}
	var rows []jobReadyRow
	if err := json.Unmarshal(readyOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode ready rows: %v\nbody=%s", err, readyOut.String())
	}
	if len(rows) != 1 || rows[0].Gate != job.StepGateManual || len(rows[0].Actions) != 1 || rows[0].Actions[0] != "agent-team job step squ-901 review --status queued" {
		t.Fatalf("blocked ready rows = %+v", rows)
	}

	advanceBlocked := NewRootCmd()
	advanceBlockedOut, advanceBlockedErr := &bytes.Buffer{}, &bytes.Buffer{}
	advanceBlocked.SetOut(advanceBlockedOut)
	advanceBlocked.SetErr(advanceBlockedErr)
	advanceBlocked.SetArgs([]string{"job", "advance", "squ-901", "--repo", root, "--dry-run", "--json"})
	if err := advanceBlocked.Execute(); err != nil {
		t.Fatalf("advance gated dry-run: %v\nstderr=%s", err, advanceBlockedErr.String())
	}
	var blockedPreview jobAdvancePreview
	if err := json.Unmarshal(advanceBlockedOut.Bytes(), &blockedPreview); err != nil {
		t.Fatalf("decode blocked advance preview: %v\nbody=%s", err, advanceBlockedOut.String())
	}
	if blockedPreview.Step != nil || blockedPreview.Message != "no ready steps" {
		t.Fatalf("blocked advance preview = %+v", blockedPreview)
	}

	approve := NewRootCmd()
	approveOut, approveErr := &bytes.Buffer{}, &bytes.Buffer{}
	approve.SetOut(approveOut)
	approve.SetErr(approveErr)
	approve.SetArgs([]string{"job", "step", "squ-901", "review", "--status", "queued", "--repo", root, "--json"})
	if err := approve.Execute(); err != nil {
		t.Fatalf("approve gate: %v\nstderr=%s", err, approveErr.String())
	}

	advanceReady := NewRootCmd()
	advanceReadyOut, advanceReadyErr := &bytes.Buffer{}, &bytes.Buffer{}
	advanceReady.SetOut(advanceReadyOut)
	advanceReady.SetErr(advanceReadyErr)
	advanceReady.SetArgs([]string{"job", "advance", "squ-901", "--repo", root, "--dry-run", "--json"})
	if err := advanceReady.Execute(); err != nil {
		t.Fatalf("advance approved dry-run: %v\nstderr=%s", err, advanceReadyErr.String())
	}
	var readyPreview jobAdvancePreview
	if err := json.Unmarshal(advanceReadyOut.Bytes(), &readyPreview); err != nil {
		t.Fatalf("decode ready advance preview: %v\nbody=%s", err, advanceReadyOut.String())
	}
	if readyPreview.Step == nil || readyPreview.Step.ID != "review" || readyPreview.Dispatch == nil || readyPreview.Dispatch.RequestedName != "manager-squ-901-review" {
		t.Fatalf("ready advance preview = %+v", readyPreview)
	}
}

func TestPipelinePRGateWaitsForJobPR(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[[instances.manager.triggers]]
event        = "agent.dispatch"
match.target = "manager"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
after = ["implement"]
gate = "pr"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	create := NewRootCmd()
	createOut, createErr := &bytes.Buffer{}, &bytes.Buffer{}
	create.SetOut(createOut)
	create.SetErr(createErr)
	create.SetArgs([]string{"pipeline", "run", "ticket_to_pr", "SQU-902", "pr gate test", "--repo", root, "--json"})
	if err := create.Execute(); err != nil {
		t.Fatalf("pipeline run: %v\nstderr=%s", err, createErr.String())
	}
	created, err := job.Read(teamDir, "squ-902")
	if err != nil {
		t.Fatalf("read created job: %v", err)
	}
	if len(created.Steps) != 2 || created.Steps[1].Gate != job.StepGatePR || created.Steps[1].Status != job.StatusBlocked {
		t.Fatalf("created steps = %+v", created.Steps)
	}

	markDone := NewRootCmd()
	markDoneOut, markDoneErr := &bytes.Buffer{}, &bytes.Buffer{}
	markDone.SetOut(markDoneOut)
	markDone.SetErr(markDoneErr)
	markDone.SetArgs([]string{"job", "step", "squ-902", "implement", "--status", "done", "--repo", root, "--json"})
	if err := markDone.Execute(); err != nil {
		t.Fatalf("mark implement done: %v\nstderr=%s", err, markDoneErr.String())
	}

	nextBlockedCmd := NewRootCmd()
	nextBlockedOut, nextBlockedErr := &bytes.Buffer{}, &bytes.Buffer{}
	nextBlockedCmd.SetOut(nextBlockedOut)
	nextBlockedCmd.SetErr(nextBlockedErr)
	nextBlockedCmd.SetArgs([]string{"job", "next", "squ-902", "--repo", root, "--json"})
	if err := nextBlockedCmd.Execute(); err != nil {
		t.Fatalf("job next blocked: %v\nstderr=%s", err, nextBlockedErr.String())
	}
	var blocked jobNextResult
	if err := json.Unmarshal(nextBlockedOut.Bytes(), &blocked); err != nil {
		t.Fatalf("decode blocked next: %v\nbody=%s", err, nextBlockedOut.String())
	}
	if blocked.State != "blocked" || blocked.Step == nil || blocked.Step.Gate != job.StepGatePR || strings.Join(blocked.WaitingFor, ",") != "pr" {
		t.Fatalf("blocked next = %+v", blocked)
	}

	readyBlocked := NewRootCmd()
	readyBlockedOut, readyBlockedErr := &bytes.Buffer{}, &bytes.Buffer{}
	readyBlocked.SetOut(readyBlockedOut)
	readyBlocked.SetErr(readyBlockedErr)
	readyBlocked.SetArgs([]string{"job", "ready", "--repo", root, "--state", "blocked", "--json"})
	if err := readyBlocked.Execute(); err != nil {
		t.Fatalf("job ready blocked: %v\nstderr=%s", err, readyBlockedErr.String())
	}
	var rows []jobReadyRow
	if err := json.Unmarshal(readyBlockedOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode ready rows: %v\nbody=%s", err, readyBlockedOut.String())
	}
	if len(rows) != 1 || rows[0].Gate != job.StepGatePR || strings.Join(rows[0].WaitingFor, ",") != "pr" || len(rows[0].Actions) != 1 || rows[0].Actions[0] != "agent-team job update squ-902 --pr <url>" {
		t.Fatalf("blocked ready rows = %+v", rows)
	}

	advanceBlocked := NewRootCmd()
	advanceBlockedOut, advanceBlockedErr := &bytes.Buffer{}, &bytes.Buffer{}
	advanceBlocked.SetOut(advanceBlockedOut)
	advanceBlocked.SetErr(advanceBlockedErr)
	advanceBlocked.SetArgs([]string{"job", "advance", "squ-902", "--repo", root, "--dry-run", "--json"})
	if err := advanceBlocked.Execute(); err != nil {
		t.Fatalf("advance blocked dry-run: %v\nstderr=%s", err, advanceBlockedErr.String())
	}
	var blockedPreview jobAdvancePreview
	if err := json.Unmarshal(advanceBlockedOut.Bytes(), &blockedPreview); err != nil {
		t.Fatalf("decode blocked advance preview: %v\nbody=%s", err, advanceBlockedOut.String())
	}
	if blockedPreview.Step != nil || blockedPreview.Message != "no ready steps" {
		t.Fatalf("blocked advance preview = %+v", blockedPreview)
	}

	update := NewRootCmd()
	updateOut, updateErr := &bytes.Buffer{}, &bytes.Buffer{}
	update.SetOut(updateOut)
	update.SetErr(updateErr)
	update.SetArgs([]string{"job", "update", "squ-902", "--pr", "https://github.com/acme/app/pull/42", "--repo", root, "--json"})
	if err := update.Execute(); err != nil {
		t.Fatalf("job update pr: %v\nstderr=%s", err, updateErr.String())
	}

	nextReadyCmd := NewRootCmd()
	nextReadyOut, nextReadyErr := &bytes.Buffer{}, &bytes.Buffer{}
	nextReadyCmd.SetOut(nextReadyOut)
	nextReadyCmd.SetErr(nextReadyErr)
	nextReadyCmd.SetArgs([]string{"job", "next", "squ-902", "--repo", root, "--json"})
	if err := nextReadyCmd.Execute(); err != nil {
		t.Fatalf("job next ready: %v\nstderr=%s", err, nextReadyErr.String())
	}
	var ready jobNextResult
	if err := json.Unmarshal(nextReadyOut.Bytes(), &ready); err != nil {
		t.Fatalf("decode ready next: %v\nbody=%s", err, nextReadyOut.String())
	}
	if ready.State != "ready" || ready.Step == nil || ready.Step.ID != "review" || ready.Step.Gate != job.StepGatePR || len(ready.WaitingFor) != 0 {
		t.Fatalf("ready next = %+v", ready)
	}

	advanceReady := NewRootCmd()
	advanceReadyOut, advanceReadyErr := &bytes.Buffer{}, &bytes.Buffer{}
	advanceReady.SetOut(advanceReadyOut)
	advanceReady.SetErr(advanceReadyErr)
	advanceReady.SetArgs([]string{"job", "advance", "squ-902", "--repo", root, "--dry-run", "--json"})
	if err := advanceReady.Execute(); err != nil {
		t.Fatalf("advance ready dry-run: %v\nstderr=%s", err, advanceReadyErr.String())
	}
	var readyPreview jobAdvancePreview
	if err := json.Unmarshal(advanceReadyOut.Bytes(), &readyPreview); err != nil {
		t.Fatalf("decode ready advance preview: %v\nbody=%s", err, advanceReadyOut.String())
	}
	if readyPreview.Step == nil || readyPreview.Step.ID != "review" || readyPreview.Dispatch == nil || readyPreview.Dispatch.RequestedName != "manager-squ-902-review" {
		t.Fatalf("ready advance preview = %+v", readyPreview)
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

func TestPipelineAdvanceIncludesQueuedReadyFirstStep(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)

	create := NewRootCmd()
	createOut, createErr := &bytes.Buffer{}, &bytes.Buffer{}
	create.SetOut(createOut)
	create.SetErr(createErr)
	create.SetArgs([]string{"pipeline", "run", "ticket_to_pr", "SQU-309", "--repo", root, "--json"})
	if err := create.Execute(); err != nil {
		t.Fatalf("pipeline run: %v\nstderr=%s", err, createErr.String())
	}

	advance := NewRootCmd()
	advanceOut, advanceErr := &bytes.Buffer{}, &bytes.Buffer{}
	advance.SetOut(advanceOut)
	advance.SetErr(advanceErr)
	advance.SetArgs([]string{"pipeline", "advance", "ticket_to_pr", "--repo", root, "--dry-run", "--json"})
	if err := advance.Execute(); err != nil {
		t.Fatalf("pipeline advance dry-run: %v\nstderr=%s", err, advanceErr.String())
	}
	var rows []pipelineAdvanceResult
	if err := json.Unmarshal(advanceOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode advance dry-run json: %v\nbody=%s", err, advanceOut.String())
	}
	if len(rows) != 1 || rows[0].JobID != "squ-309" || rows[0].StepID != "implement" || rows[0].StepStatus != job.StatusQueued || rows[0].Action != "would_advance" {
		t.Fatalf("advance rows = %+v", rows)
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
	if len(advanced) != 1 || advanced[0].JobID != "squ-401" || advanced[0].Action != "advanced" || advanced[0].StepStatus != job.StatusQueued || advanced[0].Instance != "manager" {
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
	if first.Steps[1].Status != job.StatusQueued || second.Steps[1].Status != job.StatusBlocked {
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

func TestPipelineRetryFailedSteps(t *testing.T) {
	target, _, cleanup := setupIntakePipelineRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:         "squ-601",
			Ticket:     "SQU-601",
			Target:     "manager",
			Kickoff:    "retry triage",
			Pipeline:   "ticket_triage",
			Status:     job.StatusFailed,
			LastEvent:  "step_failed",
			LastStatus: "triage failed",
			CreatedAt:  now,
			UpdatedAt:  now,
			Steps: []job.Step{
				{ID: "triage", Target: "manager", Status: job.StatusFailed, Instance: "manager-old", StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
			},
		},
		{
			ID:         "squ-602",
			Ticket:     "SQU-602",
			Target:     "manager",
			Kickoff:    "retry and dispatch triage",
			Pipeline:   "ticket_triage",
			Status:     job.StatusFailed,
			LastEvent:  "step_failed",
			LastStatus: "triage failed",
			CreatedAt:  now,
			UpdatedAt:  now,
			Steps: []job.Step{
				{ID: "triage", Target: "manager", Status: job.StatusFailed, Instance: "manager-old", StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
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
	dry.SetArgs([]string{"pipeline", "retry", "ticket_triage", "--repo", target, "--limit", "1", "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("pipeline retry dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryRows []pipelineRetryResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryRows); err != nil {
		t.Fatalf("decode retry dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(dryRows) != 1 || dryRows[0].JobID != "squ-601" || dryRows[0].Action != "would_retry" || dryRows[0].Step == nil || dryRows[0].Step.Status != job.StatusBlocked {
		t.Fatalf("dry rows = %+v", dryRows)
	}
	unchanged, err := job.Read(teamDir, "squ-601")
	if err != nil {
		t.Fatalf("read unchanged: %v", err)
	}
	if unchanged.Status != job.StatusFailed || unchanged.Steps[0].Status != job.StatusFailed || unchanged.Steps[0].Instance != "manager-old" || unchanged.Steps[0].FinishedAt.IsZero() {
		t.Fatalf("dry-run mutated job = %+v", unchanged)
	}

	preview := NewRootCmd()
	previewOut, previewErr := &bytes.Buffer{}, &bytes.Buffer{}
	preview.SetOut(previewOut)
	preview.SetErr(previewErr)
	preview.SetArgs([]string{"pipeline", "retry", "ticket_triage", "--repo", target, "--limit", "1", "--dispatch", "--workspace", "repo", "--dry-run", "--preview-routes", "--json"})
	if err := preview.Execute(); err != nil {
		t.Fatalf("pipeline retry dispatch preview: %v\nstderr=%s", err, previewErr.String())
	}
	var previewRows []pipelineRetryResult
	if err := json.Unmarshal(previewOut.Bytes(), &previewRows); err != nil {
		t.Fatalf("decode retry dispatch preview: %v\nbody=%s", err, previewOut.String())
	}
	if len(previewRows) != 1 || previewRows[0].Action != "would_dispatch" || previewRows[0].Preview == nil || previewRows[0].Preview.Dispatch == nil {
		t.Fatalf("preview rows = %+v", previewRows)
	}
	if previewRows[0].Preview.Dispatch.RequestedName != "manager-squ-601-triage" {
		t.Fatalf("requested name = %q", previewRows[0].Preview.Dispatch.RequestedName)
	}
	payload := previewRows[0].Preview.Dispatch.Preview.Payload
	if payload["pipeline"] != "ticket_triage" || payload["pipeline_step"] != "triage" || payload["job_id"] != "squ-601" || payload["workspace"] != "repo" {
		t.Fatalf("preview payload = %+v", payload)
	}

	format := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	format.SetOut(formatOut)
	format.SetErr(formatErr)
	format.SetArgs([]string{"pipeline", "retry", "ticket_triage", "--repo", target, "--limit", "1", "--dry-run", "--format", "{{.JobID}} {{.Action}} {{.StepStatus}}"})
	if err := format.Execute(); err != nil {
		t.Fatalf("pipeline retry format: %v\nstderr=%s", err, formatErr.String())
	}
	if got := formatOut.String(); got != "squ-601 would_retry blocked\n" {
		t.Fatalf("retry format = %q", got)
	}

	run := NewRootCmd()
	runOut, runErr := &bytes.Buffer{}, &bytes.Buffer{}
	run.SetOut(runOut)
	run.SetErr(runErr)
	run.SetArgs([]string{"pipeline", "retry", "ticket_triage", "--repo", target, "--limit", "1", "--message", "operator retry approved", "--json"})
	if err := run.Execute(); err != nil {
		t.Fatalf("pipeline retry: %v\nstderr=%s", err, runErr.String())
	}
	var runRows []pipelineRetryResult
	if err := json.Unmarshal(runOut.Bytes(), &runRows); err != nil {
		t.Fatalf("decode retry: %v\nbody=%s", err, runOut.String())
	}
	if len(runRows) != 1 || runRows[0].Action != "retried" || runRows[0].StepStatus != job.StatusBlocked || runRows[0].Instance != "" || runRows[0].Message != "operator retry approved" {
		t.Fatalf("run rows = %+v", runRows)
	}
	retried, err := job.Read(teamDir, "squ-601")
	if err != nil {
		t.Fatalf("read retried: %v", err)
	}
	if retried.Status != job.StatusQueued || retried.LastEvent != "reopened" || retried.LastStatus != "operator retry approved" || retried.Steps[0].Status != job.StatusBlocked || retried.Steps[0].Instance != "" || !retried.Steps[0].FinishedAt.IsZero() {
		t.Fatalf("retried job = %+v", retried)
	}
	events, err := job.ListEvents(teamDir, "squ-601")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 || events[0].Type != "reopened" || events[0].Data["step"] != "triage" {
		t.Fatalf("events = %+v", events)
	}

	dispatch := NewRootCmd()
	dispatchOut, dispatchErr := &bytes.Buffer{}, &bytes.Buffer{}
	dispatch.SetOut(dispatchOut)
	dispatch.SetErr(dispatchErr)
	dispatch.SetArgs([]string{"pipeline", "retry", "ticket_triage", "--repo", target, "--dispatch", "--workspace", "repo", "--json"})
	if err := dispatch.Execute(); err != nil {
		t.Fatalf("pipeline retry dispatch: %v\nstderr=%s", err, dispatchErr.String())
	}
	var dispatchRows []pipelineRetryResult
	if err := json.Unmarshal(dispatchOut.Bytes(), &dispatchRows); err != nil {
		t.Fatalf("decode retry dispatch: %v\nbody=%s", err, dispatchOut.String())
	}
	if len(dispatchRows) != 1 || dispatchRows[0].JobID != "squ-602" || dispatchRows[0].Action != "dispatched" || dispatchRows[0].StepStatus != job.StatusQueued || dispatchRows[0].Instance != "manager" {
		t.Fatalf("dispatch rows = %+v", dispatchRows)
	}
	dispatched, err := job.Read(teamDir, "squ-602")
	if err != nil {
		t.Fatalf("read dispatched: %v", err)
	}
	if dispatched.Status != job.StatusQueued || dispatched.Steps[0].Status != job.StatusQueued || dispatched.Steps[0].Instance != "manager" {
		t.Fatalf("dispatched job = %+v", dispatched)
	}
}

func TestPipelineRetryValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"pipeline", "retry"}, "pipeline name is required"},
		{[]string{"pipeline", "retry", "ticket_triage", "--all"}, "--all cannot be combined"},
		{[]string{"pipeline", "retry", "ticket_triage", "--limit", "-1"}, "--limit must be >= 0"},
		{[]string{"pipeline", "retry", "ticket_triage", "--preview-routes", "--dry-run"}, "--preview-routes requires --dry-run and --dispatch"},
		{[]string{"pipeline", "retry", "ticket_triage", "--format", "{{.JobID}}", "--json"}, "--format cannot be combined with --json"},
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
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
	}
}

func hasPipelineDoctorFinding(findings []pipelineDoctorFinding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
