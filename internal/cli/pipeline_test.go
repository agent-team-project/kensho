package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/runtimebin"
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
label = "Code review"
description = "Review branch and PR state."
instructions = "Check tests, summarize risks, and decide whether the PR can proceed."
target = "manager"
workspace = "repo"
runtime = "codex"
runtime_bin = "codex-dev"
after = ["implement"]
optional = true
timeout = "45m"
max_attempts = 3
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
	for _, want := range []string{"PIPELINE", "ticket_to_pr", "ticket.created", "implement:worker", `review:manager workspace=repo runtime=codex:codex-dev label="Code review" after=implement optional=true timeout=45m0s max_attempts=3`} {
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
	for _, want := range []string{"Pipeline: ticket_to_pr", "Trigger:  ticket.created", "implement target=worker after=-", `review target=manager after=implement workspace=repo runtime=codex:codex-dev label="Code review" description="Review branch and PR state." instructions="Check tests, summarize risks, and decide whether the PR can proceed." optional=true timeout=45m0s max_attempts=3`} {
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
	if len(rows) != 1 || rows[0].Name != "ticket_to_pr" || len(rows[0].Steps) != 2 || rows[0].Steps[1].Label != "Code review" || rows[0].Steps[1].Description != "Review branch and PR state." || rows[0].Steps[1].Instructions != "Check tests, summarize risks, and decide whether the PR can proceed." || rows[0].Steps[1].Workspace != "repo" || rows[0].Steps[1].Runtime != "codex" || rows[0].Steps[1].RuntimeBin != "codex-dev" || !rows[0].Steps[1].Optional || rows[0].Steps[1].Timeout != "45m0s" || rows[0].Steps[1].MaxAttempts != 3 {
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

	inspect := NewRootCmd()
	inspectOut, inspectErr := &bytes.Buffer{}, &bytes.Buffer{}
	inspect.SetOut(inspectOut)
	inspect.SetErr(inspectErr)
	inspect.SetArgs([]string{"pipeline", "inspect", "ticket_to_pr", "--repo", root, "--format", "{{.Name}} {{len .Steps}}"})
	if err := inspect.Execute(); err != nil {
		t.Fatalf("pipeline inspect alias: %v\nstderr=%s", err, inspectErr.String())
	}
	if got, want := inspectOut.String(), "ticket_to_pr 2\n"; got != want {
		t.Fatalf("pipeline inspect alias output = %q, want %q", got, want)
	}
}

func TestPipelineAdoptUsesScopedJobDefaults(t *testing.T) {
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
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-801",
		Ticket:    "SQU-801",
		Target:    "worker",
		Status:    job.StatusRunning,
		Pipeline:  "ticket_to_pr",
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusDone, FinishedAt: now.Add(-10 * time.Minute)},
			{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}},
		},
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "adopt", "ticket_to_pr", "squ-801", "--repo", root, "--step", "review", "--pid", strconv.Itoa(os.Getpid()), "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline adopt: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var result daemonAdoptResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode pipeline adopt result: %v\nbody=%s", err, out.String())
	}
	if result.Metadata == nil || result.Metadata.Instance != "manager-squ-801-review" || result.Metadata.Agent != "manager" || result.Metadata.Job != "squ-801" {
		t.Fatalf("metadata = %+v", result.Metadata)
	}
	if result.Job == nil || !result.JobChanged || result.Job.Pipeline != "ticket_to_pr" || result.Job.Instance != "manager-squ-801-review" {
		t.Fatalf("pipeline adopt result = %+v", result)
	}
	if len(result.Job.Steps) != 2 || result.Job.Steps[1].Status != job.StatusRunning || result.Job.Steps[1].Instance != "manager-squ-801-review" {
		t.Fatalf("adopted steps = %+v", result.Job.Steps)
	}
	for _, want := range []string{
		"agent-team pipeline status ticket_to_pr",
		"agent-team pipeline logs ticket_to_pr --follow",
		"agent-team pipeline resume-plan ticket_to_pr --step review",
	} {
		if !containsString(result.Actions, want) {
			t.Fatalf("pipeline adopt actions = %+v, missing %q", result.Actions, want)
		}
	}
	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"pipeline", "adopt", "ticket_to_pr", "squ-801", "--repo", root, "--step", "review", "--pid", strconv.Itoa(os.Getpid()), "--dry-run", "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("pipeline adopt --commands: %v\nstdout=%s\nstderr=%s", err, commandsOut.String(), commandsErr.String())
	}
	for _, want := range []string{
		"agent-team pipeline status ticket_to_pr",
		"agent-team pipeline logs ticket_to_pr --follow",
		"agent-team pipeline resume-plan ticket_to_pr --step review",
	} {
		if !strings.Contains(commandsOut.String(), want) {
			t.Fatalf("pipeline adopt commands missing %q:\n%s", want, commandsOut.String())
		}
	}
	updated, err := job.Read(teamDir, "squ-801")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.LastEvent != "adopted" || updated.Instance != "manager-squ-801-review" || updated.Steps[1].Instance != "manager-squ-801-review" {
		t.Fatalf("updated job = %+v", updated)
	}
}

func TestPipelineAdoptRejectsJobOutsidePipeline(t *testing.T) {
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

[pipelines.release_review]
trigger.event = "ticket.created"

[[pipelines.release_review.steps]]
id = "review"
target = "manager"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "rel-801",
		Ticket:    "REL-801",
		Target:    "manager",
		Status:    job.StatusQueued,
		Pipeline:  "release_review",
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "review", Target: "manager", Status: job.StatusQueued},
		},
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "adopt", "ticket_to_pr", "rel-801", "--repo", root, "--pid", strconv.Itoa(os.Getpid()), "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("pipeline adopt outside pipeline succeeded: stdout=%s stderr=%s", out.String(), stderr.String())
	}
	var code ExitCode
	if !errors.As(err, &code) || int(code) != 2 {
		t.Fatalf("pipeline adopt err = %v, want exit code 2", err)
	}
	if !strings.Contains(stderr.String(), `belongs to pipeline "release_review", not "ticket_to_pr"`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	unchanged, err := job.Read(teamDir, "rel-801")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if unchanged.Status != job.StatusQueued || unchanged.Instance != "" || unchanged.LastEvent != "" {
		t.Fatalf("pipeline adopt mutated wrong-pipeline job = %+v", unchanged)
	}
	if _, err := daemon.ReadMetadata(daemon.DaemonRoot(teamDir), "manager-rel-801-review"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("metadata should not exist after rejected adoption: %v", err)
	}
}

func TestPipelineRunCopiesOptionalStepMetadata(t *testing.T) {
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
id = "verify"
label = "Verification"
description = "Confirm implementation matches the ticket."
instructions = "Check acceptance criteria before closing the workflow."
target = "manager"
workspace = "repo"
runtime = "codex"
runtime_bin = "codex-dev"
after = ["implement"]
optional = true
timeout = "30m"
max_attempts = 2
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "run", "ticket_to_pr", "SQU-903", "optional stage", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline run: %v\nstderr=%s", err, stderr.String())
	}
	created, err := job.Read(teamDir, "squ-903")
	if err != nil {
		t.Fatalf("read created job: %v", err)
	}
	if len(created.Steps) != 2 || created.Steps[1].ID != "verify" || created.Steps[1].Label != "Verification" || created.Steps[1].Description != "Confirm implementation matches the ticket." || created.Steps[1].Instructions != "Check acceptance criteria before closing the workflow." || created.Steps[1].Workspace != "repo" || created.Steps[1].Runtime != "codex" || created.Steps[1].RuntimeBin != "codex-dev" || !created.Steps[1].Optional || created.Steps[1].Timeout != "30m0s" || created.Steps[1].MaxAttempts != 2 {
		t.Fatalf("optional step metadata was not copied: %+v", created.Steps)
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
label = "Review"
description = "Human review gate."
instructions = "Inspect the branch and make a release recommendation."
target = "manager"
after = ["implement"]
optional = true

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
		`review target=manager after=implement label="Review" description="Human review gate." instructions="Inspect the branch and make a release recommendation." optional=true routes=manager`,
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
	for _, want := range []string{"flowchart TD", "trigger[\"trigger: ticket.created\"]", "step_1_implement", "label: Review", "optional", "--> step_2_review"} {
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
	for _, want := range []string{`digraph "ticket_to_pr"`, `"trigger" -> "implement";`, `"implement" -> "review";`, "label: Review", "optional"} {
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
	if graph.Name != "ticket_to_pr" || len(graph.Nodes) != 3 || len(graph.Edges) != 3 || len(graph.Nodes[0].Routes) != 1 || graph.Nodes[1].Label != "Review" || graph.Nodes[1].Description != "Human review gate." || graph.Nodes[1].Instructions != "Inspect the branch and make a release recommendation." || !graph.Nodes[1].Optional {
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

func TestPipelineDoctorWarnsWhenStepRuntimeUnavailable(t *testing.T) {
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
runtime = "codex"
runtime_bin = "missing-codex"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "missing-codex" {
			t.Fatalf("look path bin = %q, want missing-codex", bin)
		}
		return "", exec.ErrNotFound
	})

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
	if !result.OK || len(result.Problems) != 0 || !hasPipelineDoctorFinding(result.Warnings, "step_runtime_unavailable") {
		t.Fatalf("doctor result = %+v", result)
	}
	if got := result.Warnings[0]; got.Runtime != "codex" || got.RuntimeBin != "missing-codex" || got.Step != "implement" {
		t.Fatalf("runtime warning = %+v", got)
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
		t.Fatalf("doctor text stdout = %q", textOut.String())
	}
	if !strings.Contains(textErr.String(), `runtime "codex" with binary "missing-codex"`) {
		t.Fatalf("doctor text stderr = %q", textErr.String())
	}

	strict := NewRootCmd()
	strictOut, strictErr := &bytes.Buffer{}, &bytes.Buffer{}
	strict.SetOut(strictOut)
	strict.SetErr(strictErr)
	strict.SetArgs([]string{"pipeline", "doctor", "ticket_to_pr", "--repo", root, "--strict-runtime", "--json"})
	err := strict.Execute()
	if err == nil {
		t.Fatal("pipeline doctor strict runtime unexpectedly succeeded")
	}
	var code ExitCode
	if !errors.As(err, &code) || int(code) != 1 {
		t.Fatalf("strict err = %v, want exit 1", err)
	}
	var strictResult pipelineDoctorResult
	if err := json.Unmarshal(strictOut.Bytes(), &strictResult); err != nil {
		t.Fatalf("decode strict pipeline doctor json: %v\nbody=%s", err, strictOut.String())
	}
	if strictResult.OK || !hasPipelineDoctorFinding(strictResult.Problems, "step_runtime_unavailable") || len(strictResult.Warnings) != 0 {
		t.Fatalf("strict doctor result = %+v", strictResult)
	}
	if len(strictResult.Pipelines) != 1 || strictResult.Pipelines[0].OK || !hasPipelineDoctorFinding(strictResult.Pipelines[0].Problems, "step_runtime_unavailable") || len(strictResult.Pipelines[0].Warnings) != 0 {
		t.Fatalf("strict pipeline result = %+v", strictResult.Pipelines)
	}
	if strictErr.Len() != 0 {
		t.Fatalf("strict stderr = %q", strictErr.String())
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
		{ID: "squ-301", Ticket: "SQU-301", Target: "worker", Instance: "worker-squ-301", Pipeline: "ticket_to_pr", Status: job.StatusRunning, CreatedAt: now, UpdatedAt: now},
		{ID: "squ-302", Ticket: "SQU-302", Target: "manager", Instance: "manager-squ-302", Pipeline: "nightly", Status: job.StatusQueued, CreatedAt: now, UpdatedAt: now},
		{ID: "squ-303", Ticket: "SQU-303", Target: "manager", Instance: "manager-squ-303", Pipeline: "ticket_to_pr", Status: job.StatusDone, CreatedAt: now, UpdatedAt: now},
		{ID: "adhoc-301", Ticket: "ADHOC-301", Target: "worker", Instance: "worker-adhoc-301", Status: job.StatusRunning, CreatedAt: now, UpdatedAt: now},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}
	for _, meta := range []*daemon.Metadata{
		{Instance: "worker-squ-301", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
		{Instance: "manager-squ-302", Agent: "manager", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
		{Instance: "manager-squ-303", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
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

	allCmd := NewRootCmd()
	allOut, allErr := &bytes.Buffer{}, &bytes.Buffer{}
	allCmd.SetOut(allOut)
	allCmd.SetErr(allErr)
	allCmd.SetArgs([]string{"pipeline", "jobs", "--repo", root, "--json"})
	if err := allCmd.Execute(); err != nil {
		t.Fatalf("pipeline jobs default all: %v\nstderr=%s", err, allErr.String())
	}
	rows = nil
	if err := json.Unmarshal(allOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline jobs default all json: %v\nbody=%s", err, allOut.String())
	}
	allIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		allIDs = append(allIDs, row.ID)
	}
	if strings.Join(allIDs, ",") != "squ-301,squ-302,squ-303" {
		t.Fatalf("pipeline default all rows = %v", allIDs)
	}

	explicitAllCmd := NewRootCmd()
	explicitAllOut, explicitAllErr := &bytes.Buffer{}, &bytes.Buffer{}
	explicitAllCmd.SetOut(explicitAllOut)
	explicitAllCmd.SetErr(explicitAllErr)
	explicitAllCmd.SetArgs([]string{"pipeline", "jobs", "--all", "--repo", root, "--status", "queued", "--json"})
	if err := explicitAllCmd.Execute(); err != nil {
		t.Fatalf("pipeline jobs --all: %v\nstderr=%s", err, explicitAllErr.String())
	}
	rows = nil
	if err := json.Unmarshal(explicitAllOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline jobs --all json: %v\nbody=%s", err, explicitAllOut.String())
	}
	if len(rows) != 1 || rows[0].ID != "squ-302" {
		t.Fatalf("pipeline --all queued rows = %+v", rows)
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

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	watchCmd := NewRootCmd()
	watchOut, watchErr := &bytes.Buffer{}, &bytes.Buffer{}
	watchCmd.SetContext(ctx)
	watchCmd.SetOut(watchOut)
	watchCmd.SetErr(watchErr)
	watchCmd.SetArgs([]string{"pipeline", "jobs", "ticket_to_pr", "--repo", root, "--watch", "--no-clear", "--interval", "1h", "--format", "{{.ID}} {{.Status}}"})
	if err := watchCmd.Execute(); err != nil {
		t.Fatalf("pipeline jobs watch: %v\nstderr=%s", err, watchErr.String())
	}
	if got := strings.Split(strings.TrimSpace(watchOut.String()), "\n"); strings.Join(got, ",") != "squ-301 running,squ-303 done" || strings.Contains(watchOut.String(), watchClearSequence) {
		t.Fatalf("pipeline jobs watch output = %q", watchOut.String())
	}

	sortCmd := NewRootCmd()
	sortOut, sortErr := &bytes.Buffer{}, &bytes.Buffer{}
	sortCmd.SetOut(sortOut)
	sortCmd.SetErr(sortErr)
	sortCmd.SetArgs([]string{"pipeline", "jobs", "ticket_to_pr", "--repo", root, "--sort", "target", "--format", "{{.ID}} {{.Target}}"})
	if err := sortCmd.Execute(); err != nil {
		t.Fatalf("pipeline jobs sort: %v\nstderr=%s", err, sortErr.String())
	}
	if got := strings.Split(strings.TrimSpace(sortOut.String()), "\n"); strings.Join(got, ",") != "squ-303 manager,squ-301 worker" {
		t.Fatalf("pipeline jobs sorted output = %q", sortOut.String())
	}

	limited := NewRootCmd()
	limitedOut, limitedErr := &bytes.Buffer{}, &bytes.Buffer{}
	limited.SetOut(limitedOut)
	limited.SetErr(limitedErr)
	limited.SetArgs([]string{"pipeline", "jobs", "ticket_to_pr", "--repo", root, "--sort", "target", "--limit", "1", "--format", "{{.ID}} {{.Target}}"})
	if err := limited.Execute(); err != nil {
		t.Fatalf("pipeline jobs limit: %v\nstderr=%s", err, limitedErr.String())
	}
	if got := strings.TrimSpace(limitedOut.String()); got != "squ-303 manager" {
		t.Fatalf("pipeline jobs limited output = %q", limitedOut.String())
	}

	runtimeCmd := NewRootCmd()
	runtimeOut, runtimeErr := &bytes.Buffer{}, &bytes.Buffer{}
	runtimeCmd.SetOut(runtimeOut)
	runtimeCmd.SetErr(runtimeErr)
	runtimeCmd.SetArgs([]string{"pipeline", "jobs", "ticket_to_pr", "--repo", root, "--runtime", "codex", "--json"})
	if err := runtimeCmd.Execute(); err != nil {
		t.Fatalf("pipeline jobs runtime: %v\nstderr=%s", err, runtimeErr.String())
	}
	rows = nil
	if err := json.Unmarshal(runtimeOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline jobs runtime json: %v\nbody=%s", err, runtimeOut.String())
	}
	if len(rows) != 1 || rows[0].ID != "squ-301" {
		t.Fatalf("pipeline runtime rows = %+v", rows)
	}

	summaryCmd := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summaryCmd.SetOut(summaryOut)
	summaryCmd.SetErr(summaryErr)
	summaryCmd.SetArgs([]string{"pipeline", "jobs", "ticket_to_pr", "--repo", root, "--runtime", "codex", "--summary", "--json"})
	if err := summaryCmd.Execute(); err != nil {
		t.Fatalf("pipeline jobs summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var summary jobSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &summary); err != nil {
		t.Fatalf("decode pipeline summary: %v\nbody=%s", err, summaryOut.String())
	}
	if summary.Total != 1 || summary.Runtimes["codex"] != 1 {
		t.Fatalf("pipeline summary = %+v", summary)
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

	invalidSort := NewRootCmd()
	invalidSortOut, invalidSortErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidSort.SetOut(invalidSortOut)
	invalidSort.SetErr(invalidSortErr)
	invalidSort.SetArgs([]string{"pipeline", "jobs", "ticket_to_pr", "--repo", root, "--sort", "priority"})
	if err := invalidSort.Execute(); err == nil {
		t.Fatalf("pipeline jobs invalid sort succeeded")
	}
	if !strings.Contains(invalidSortErr.String(), "--sort must be id, status, target") {
		t.Fatalf("invalid sort stderr = %q", invalidSortErr.String())
	}

	invalidInterval := NewRootCmd()
	invalidIntervalOut, invalidIntervalErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidInterval.SetOut(invalidIntervalOut)
	invalidInterval.SetErr(invalidIntervalErr)
	invalidInterval.SetArgs([]string{"pipeline", "jobs", "ticket_to_pr", "--repo", root, "--watch", "--interval", "-1s"})
	if err := invalidInterval.Execute(); err == nil {
		t.Fatalf("pipeline jobs negative interval succeeded")
	}
	if !strings.Contains(invalidIntervalErr.String(), "--interval must be >= 0") {
		t.Fatalf("negative interval stderr = %q", invalidIntervalErr.String())
	}

	summaryLimit := NewRootCmd()
	summaryLimitOut, summaryLimitErr := &bytes.Buffer{}, &bytes.Buffer{}
	summaryLimit.SetOut(summaryLimitOut)
	summaryLimit.SetErr(summaryLimitErr)
	summaryLimit.SetArgs([]string{"pipeline", "jobs", "ticket_to_pr", "--repo", root, "--summary", "--limit", "1"})
	if err := summaryLimit.Execute(); err == nil {
		t.Fatalf("pipeline jobs summary limit succeeded")
	}
	if !strings.Contains(summaryLimitErr.String(), "--limit cannot be combined with --summary") {
		t.Fatalf("summary limit stderr = %q", summaryLimitErr.String())
	}

	invalidMany := NewRootCmd()
	invalidManyOut, invalidManyErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidMany.SetOut(invalidManyOut)
	invalidMany.SetErr(invalidManyErr)
	invalidMany.SetArgs([]string{"pipeline", "jobs", "ticket_to_pr", "nightly", "--repo", root})
	if err := invalidMany.Execute(); err == nil {
		t.Fatalf("pipeline jobs accepted multiple pipeline names: stdout=%s", invalidManyOut.String())
	}
	if !strings.Contains(invalidManyErr.String(), "pass at most one pipeline name") {
		t.Fatalf("multiple pipeline error = %q", invalidManyErr.String())
	}

	invalidAll := NewRootCmd()
	invalidAllOut, invalidAllErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidAll.SetOut(invalidAllOut)
	invalidAll.SetErr(invalidAllErr)
	invalidAll.SetArgs([]string{"pipeline", "jobs", "ticket_to_pr", "--all", "--repo", root})
	if err := invalidAll.Execute(); err == nil {
		t.Fatalf("pipeline jobs accepted --all with pipeline: stdout=%s", invalidAllOut.String())
	}
	if !strings.Contains(invalidAllErr.String(), "--all cannot be combined with a pipeline argument") {
		t.Fatalf("--all conflict error = %q", invalidAllErr.String())
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
			ID:        "squ-614",
			Ticket:    "SQU-614",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusBlocked,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusDone},
				{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}, Gate: job.StepGateManual},
			},
		},
		{
			ID:        "squ-615",
			Ticket:    "SQU-615",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-90 * time.Minute),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-615", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
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
	if !ticket.Declared || ticket.Steps != 2 || ticket.Jobs != 4 || ticket.Running != 2 || ticket.Blocked != 1 || ticket.Failed != 1 || ticket.ReadySteps != 1 || ticket.ManualGates != 1 || ticket.FailedSteps != 1 || ticket.StaleRunningSteps != 1 {
		t.Fatalf("ticket status = %+v", ticket)
	}
	if !containsString(ticket.Actions, "agent-team pipeline tick ticket_to_pr --dry-run --preview-routes") ||
		!containsString(ticket.Actions, "agent-team job reconcile events --dry-run") ||
		!containsString(ticket.Actions, "agent-team pipeline timeout ticket_to_pr --dry-run") ||
		!containsString(ticket.Actions, "agent-team pipeline repair ticket_to_pr --timeout-jobs --dry-run --preview-routes") ||
		!containsString(ticket.Actions, "agent-team repair --timeout-jobs --dry-run") ||
		!containsString(ticket.Actions, "agent-team pipeline explain ticket_to_pr --state running") ||
		!containsString(ticket.Actions, "agent-team pipeline approve ticket_to_pr --dry-run --dispatch --preview-routes") ||
		!containsString(ticket.Actions, "agent-team pipeline retry ticket_to_pr --dry-run --dispatch --preview-routes") ||
		!containsString(ticket.Actions, "agent-team pipeline repair ticket_to_pr --retry-pipelines --dry-run --preview-routes") ||
		!containsString(ticket.Actions, "agent-team repair --retry-pipelines --dry-run --preview-routes") ||
		!containsString(ticket.Actions, "agent-team pipeline explain ticket_to_pr --state failed") ||
		!containsString(ticket.Actions, "agent-team pipeline ready ticket_to_pr --state failed") {
		t.Fatalf("ticket actions = %+v", ticket.Actions)
	}
	nightly := byName["nightly"]
	if !nightly.Declared || nightly.Steps != 1 || nightly.Jobs != 1 || nightly.Queued != 1 || nightly.ReadySteps != 1 || nightly.QueuedSteps != 1 {
		t.Fatalf("nightly status = %+v", nightly)
	}
	if !containsString(nightly.Actions, "agent-team pipeline tick nightly --dry-run --preview-routes") {
		t.Fatalf("nightly actions = %+v", nightly.Actions)
	}
	adHoc := byName["ad_hoc"]
	if adHoc.Declared || adHoc.Steps != 0 || adHoc.Jobs != 1 || adHoc.Done != 1 || adHoc.NoStep != 1 {
		t.Fatalf("ad_hoc status = %+v", adHoc)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"pipeline", "status", "--repo", root, "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("pipeline status --commands: %v\nstderr=%s", err, commandsErr.String())
	}
	var wantCommands bytes.Buffer
	var expectedActions []string
	for _, row := range rows {
		expectedActions = append(expectedActions, row.Actions...)
	}
	if err := renderActionCommands(&wantCommands, commandActionsOnly(expectedActions)); err != nil {
		t.Fatalf("render expected commands: %v", err)
	}
	if got, want := commandsOut.String(), wantCommands.String(); got != want {
		t.Fatalf("pipeline status --commands = %q, want %q", got, want)
	}

	sorted := NewRootCmd()
	sortedOut, sortedErr := &bytes.Buffer{}, &bytes.Buffer{}
	sorted.SetOut(sortedOut)
	sorted.SetErr(sortedErr)
	sorted.SetArgs([]string{"pipeline", "status", "--repo", root, "--sort", "jobs", "--limit", "1", "--format", "{{.Pipeline}} {{.Jobs}}"})
	if err := sorted.Execute(); err != nil {
		t.Fatalf("pipeline status sort/limit: %v\nstderr=%s", err, sortedErr.String())
	}
	if got := strings.TrimSpace(sortedOut.String()); got != "ticket_to_pr 4" {
		t.Fatalf("sorted pipeline status = %q", got)
	}

	alpha := NewRootCmd()
	alphaOut, alphaErr := &bytes.Buffer{}, &bytes.Buffer{}
	alpha.SetOut(alphaOut)
	alpha.SetErr(alphaErr)
	alpha.SetArgs([]string{"pipeline", "status", "--repo", root, "--sort", "pipeline", "--limit", "2", "--format", "{{.Pipeline}}"})
	if err := alpha.Execute(); err != nil {
		t.Fatalf("pipeline status alpha sort: %v\nstderr=%s", err, alphaErr.String())
	}
	if got := strings.Split(strings.TrimSpace(alphaOut.String()), "\n"); strings.Join(got, ",") != "ad_hoc,nightly" {
		t.Fatalf("alpha pipeline status = %q", alphaOut.String())
	}

	one := NewRootCmd()
	oneOut, oneErr := &bytes.Buffer{}, &bytes.Buffer{}
	one.SetOut(oneOut)
	one.SetErr(oneErr)
	one.SetArgs([]string{"pipeline", "status", "ticket_to_pr", "--repo", root, "--format", "{{.Pipeline}} {{.Jobs}} {{.ReadySteps}} {{.StaleRunningSteps}} {{.FailedSteps}}"})
	if err := one.Execute(); err != nil {
		t.Fatalf("pipeline status one format: %v\nstderr=%s", err, oneErr.String())
	}
	if got := strings.TrimSpace(oneOut.String()); got != "ticket_to_pr 4 1 1 1" {
		t.Fatalf("formatted pipeline status = %q", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	watch := NewRootCmd()
	watchOut, watchErr := &bytes.Buffer{}, &bytes.Buffer{}
	watch.SetContext(ctx)
	watch.SetOut(watchOut)
	watch.SetErr(watchErr)
	watch.SetArgs([]string{"pipeline", "status", "ticket_to_pr", "--repo", root, "--watch", "--no-clear", "--interval", "1ms", "--format", "{{.Pipeline}} {{.Jobs}} {{.ReadySteps}}"})
	if err := watch.Execute(); err != nil {
		t.Fatalf("pipeline status watch: %v\nstderr=%s", err, watchErr.String())
	}
	if got := strings.TrimSpace(watchOut.String()); got != "ticket_to_pr 4 1" || strings.Contains(watchOut.String(), watchClearSequence) {
		t.Fatalf("pipeline status watch output = %q", watchOut.String())
	}

	watchAlias := NewRootCmd()
	watchAliasOut, watchAliasErr := &bytes.Buffer{}, &bytes.Buffer{}
	watchAlias.SetContext(ctx)
	watchAlias.SetOut(watchAliasOut)
	watchAlias.SetErr(watchAliasErr)
	watchAlias.SetArgs([]string{"pipeline", "watch", "ticket_to_pr", "--repo", root, "--no-clear", "--interval", "1ms", "--format", "{{.Pipeline}} {{.Jobs}} {{.ReadySteps}}"})
	if err := watchAlias.Execute(); err != nil {
		t.Fatalf("pipeline watch alias: %v\nstderr=%s", err, watchAliasErr.String())
	}
	if got := strings.TrimSpace(watchAliasOut.String()); got != "ticket_to_pr 4 1" || strings.Contains(watchAliasOut.String(), watchClearSequence) {
		t.Fatalf("pipeline watch alias output = %q", watchAliasOut.String())
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"pipeline", "status", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("pipeline status text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"PIPELINE", "STALE_RUNNING", "MANUAL_GATES", "ACTION", "ticket_to_pr", "yes", "running=2,blocked=1,failed=1", "agent-team pipeline tick ticket_to_pr --dry-run --preview-routes", "agent-team job reconcile events --dry-run", "agent-team pipeline timeout ticket_to_pr --dry-run", "agent-team pipeline repair ticket_to_pr --timeout-jobs --dry-run --preview-routes", "agent-team repair --timeout-jobs --dry-run", "agent-team pipeline approve ticket_to_pr --dry-run --dispatch --preview-routes", "agent-team pipeline repair ticket_to_pr --retry-pipelines --dry-run --preview-routes", "agent-team repair --retry-pipelines --dry-run --preview-routes", "ad_hoc", "no"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("pipeline status text missing %q:\n%s", want, textOut.String())
		}
	}

	explain := NewRootCmd()
	explainOut, explainErr := &bytes.Buffer{}, &bytes.Buffer{}
	explain.SetOut(explainOut)
	explain.SetErr(explainErr)
	explain.SetArgs([]string{"pipeline", "explain", "ticket_to_pr", "--repo", root, "--json"})
	if err := explain.Execute(); err != nil {
		t.Fatalf("pipeline explain json: %v\nstderr=%s", err, explainErr.String())
	}
	var explainedRows []pipelineExplainRow
	if err := json.Unmarshal(explainOut.Bytes(), &explainedRows); err != nil {
		t.Fatalf("decode pipeline explain json: %v\nbody=%s", err, explainOut.String())
	}
	if len(explainedRows) != 1 {
		t.Fatalf("pipeline explain rows = %+v", explainedRows)
	}
	explained := explainedRows[0]
	if explained.Pipeline != "ticket_to_pr" || !explained.Declared || explained.TotalJobs != 4 || explained.ExplainedJobs != 4 || len(explained.Jobs) != 4 {
		t.Fatalf("pipeline explain ticket_to_pr = %+v", explained)
	}
	var readyReview, manualGate, failedImplement bool
	for _, explainedJob := range explained.Jobs {
		for _, step := range explainedJob.Steps {
			switch {
			case explainedJob.JobID == "squ-610" && step.ID == "review":
				readyReview = step.State == "ready" &&
					containsString(step.Actions, "agent-team pipeline tick ticket_to_pr --dry-run --preview-routes") &&
					!containsString(step.Actions, "agent-team job advance squ-610")
			case explainedJob.JobID == "squ-614" && step.ID == "review":
				manualGate = step.State == "waiting" && step.Gate == job.StepGateManual &&
					containsString(step.Actions, "agent-team pipeline approve ticket_to_pr --step review --dry-run --dispatch --preview-routes") &&
					containsString(step.Actions, "agent-team pipeline reject ticket_to_pr --step review --dry-run") &&
					!containsString(step.Actions, "agent-team job approve squ-614 --step review") &&
					!containsString(step.Actions, "agent-team job reject squ-614 --step review")
			case explainedJob.JobID == "squ-611" && step.ID == "implement":
				failedImplement = step.State == "failed" &&
					containsString(step.Actions, "agent-team pipeline retry ticket_to_pr --step implement --dry-run --dispatch --preview-routes") &&
					!containsString(step.Actions, "agent-team job retry squ-611 --dry-run --dispatch")
			}
		}
	}
	if !readyReview || !manualGate || !failedImplement {
		t.Fatalf("pipeline explain did not surface expected step diagnostics: ready=%v gate=%v failed=%v rows=%+v", readyReview, manualGate, failedImplement, explainedRows)
	}

	explainText := NewRootCmd()
	explainTextOut, explainTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	explainText.SetOut(explainTextOut)
	explainText.SetErr(explainTextErr)
	explainText.SetArgs([]string{"pipeline", "explain", "ticket_to_pr", "--repo", root})
	if err := explainText.Execute(); err != nil {
		t.Fatalf("pipeline explain text: %v\nstderr=%s", err, explainTextErr.String())
	}
	for _, want := range []string{"Pipeline: ticket_to_pr", "Jobs:", "Steps:", "squ-610", "review", "agent-team pipeline tick ticket_to_pr --dry-run --preview-routes", "agent-team pipeline approve ticket_to_pr --step review --dry-run --dispatch --preview-routes", "agent-team pipeline reject ticket_to_pr --step review --dry-run"} {
		if !strings.Contains(explainTextOut.String(), want) {
			t.Fatalf("pipeline explain text missing %q:\n%s", want, explainTextOut.String())
		}
	}
	for _, unwanted := range []string{"agent-team job advance squ-610", "agent-team job approve squ-614 --step review", "agent-team job reject squ-614 --step review"} {
		if strings.Contains(explainTextOut.String(), unwanted) {
			t.Fatalf("pipeline explain text included unscoped action %q:\n%s", unwanted, explainTextOut.String())
		}
	}

	explainCommands := NewRootCmd()
	explainCommandsOut, explainCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	explainCommands.SetOut(explainCommandsOut)
	explainCommands.SetErr(explainCommandsErr)
	explainCommands.SetArgs([]string{"pipeline", "explain", "ticket_to_pr", "--repo", root, "--commands"})
	if err := explainCommands.Execute(); err != nil {
		t.Fatalf("pipeline explain --commands: %v\nstderr=%s", err, explainCommandsErr.String())
	}
	var wantExplainCommands bytes.Buffer
	if err := renderPipelineExplainCommands(&wantExplainCommands, explainedRows); err != nil {
		t.Fatalf("render expected pipeline explain commands: %v", err)
	}
	if got, want := explainCommandsOut.String(), wantExplainCommands.String(); got != want {
		t.Fatalf("pipeline explain --commands = %q, want %q", got, want)
	}

	explainFormat := NewRootCmd()
	explainFormatOut, explainFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	explainFormat.SetOut(explainFormatOut)
	explainFormat.SetErr(explainFormatErr)
	explainFormat.SetArgs([]string{"pipeline", "explain", "ticket_to_pr", "--repo", root, "--format", "{{.Pipeline}} {{.TotalJobs}} {{.ExplainedJobs}}"})
	if err := explainFormat.Execute(); err != nil {
		t.Fatalf("pipeline explain format: %v\nstderr=%s", err, explainFormatErr.String())
	}
	if got := strings.TrimSpace(explainFormatOut.String()); got != "ticket_to_pr 4 4" {
		t.Fatalf("pipeline explain format = %q", got)
	}

	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	explainWatch := NewRootCmd()
	explainWatchOut, explainWatchErr := &bytes.Buffer{}, &bytes.Buffer{}
	explainWatch.SetContext(ctx)
	explainWatch.SetOut(explainWatchOut)
	explainWatch.SetErr(explainWatchErr)
	explainWatch.SetArgs([]string{"pipeline", "explain", "ticket_to_pr", "--repo", root, "--state", "failed", "--watch", "--no-clear", "--interval", "1h", "--format", "{{.Pipeline}} {{len .Jobs}}"})
	if err := explainWatch.Execute(); err != nil {
		t.Fatalf("pipeline explain watch: %v\nstderr=%s", err, explainWatchErr.String())
	}
	if got := strings.TrimSpace(explainWatchOut.String()); got != "ticket_to_pr 1" || strings.Contains(explainWatchOut.String(), watchClearSequence) {
		t.Fatalf("pipeline explain watch output = %q", explainWatchOut.String())
	}

	explainLimited := NewRootCmd()
	explainLimitedOut, explainLimitedErr := &bytes.Buffer{}, &bytes.Buffer{}
	explainLimited.SetOut(explainLimitedOut)
	explainLimited.SetErr(explainLimitedErr)
	explainLimited.SetArgs([]string{"pipeline", "explain", "ticket_to_pr", "--repo", root, "--limit", "1", "--json"})
	if err := explainLimited.Execute(); err != nil {
		t.Fatalf("pipeline explain limit: %v\nstderr=%s", err, explainLimitedErr.String())
	}
	var limitedRows []pipelineExplainRow
	if err := json.Unmarshal(explainLimitedOut.Bytes(), &limitedRows); err != nil {
		t.Fatalf("decode limited pipeline explain json: %v\nbody=%s", err, explainLimitedOut.String())
	}
	if len(limitedRows) != 1 || limitedRows[0].TotalJobs != 4 || limitedRows[0].ExplainedJobs != 1 || !limitedRows[0].Truncated || len(limitedRows[0].Jobs) != 1 {
		t.Fatalf("limited pipeline explain = %+v", limitedRows)
	}

	explainSorted := NewRootCmd()
	explainSortedOut, explainSortedErr := &bytes.Buffer{}, &bytes.Buffer{}
	explainSorted.SetOut(explainSortedOut)
	explainSorted.SetErr(explainSortedErr)
	explainSorted.SetArgs([]string{"pipeline", "explain", "ticket_to_pr", "--repo", root, "--sort", "state", "--limit", "1", "--json"})
	if err := explainSorted.Execute(); err != nil {
		t.Fatalf("pipeline explain sort: %v\nstderr=%s", err, explainSortedErr.String())
	}
	var sortedExplainRows []pipelineExplainRow
	if err := json.Unmarshal(explainSortedOut.Bytes(), &sortedExplainRows); err != nil {
		t.Fatalf("decode sorted pipeline explain json: %v\nbody=%s", err, explainSortedOut.String())
	}
	if len(sortedExplainRows) != 1 || sortedExplainRows[0].ExplainedJobs != 1 || !sortedExplainRows[0].Truncated || len(sortedExplainRows[0].Jobs) != 1 || sortedExplainRows[0].Jobs[0].JobID != "squ-610" || sortedExplainRows[0].Jobs[0].State != "ready" {
		t.Fatalf("sorted pipeline explain = %+v", sortedExplainRows)
	}

	explainStep := NewRootCmd()
	explainStepOut, explainStepErr := &bytes.Buffer{}, &bytes.Buffer{}
	explainStep.SetOut(explainStepOut)
	explainStep.SetErr(explainStepErr)
	explainStep.SetArgs([]string{"pipeline", "explain", "ticket_to_pr", "--repo", root, "--step", "review", "--json"})
	if err := explainStep.Execute(); err != nil {
		t.Fatalf("pipeline explain step filter: %v\nstderr=%s", err, explainStepErr.String())
	}
	var stepRows []pipelineExplainRow
	if err := json.Unmarshal(explainStepOut.Bytes(), &stepRows); err != nil {
		t.Fatalf("decode step-filtered pipeline explain json: %v\nbody=%s", err, explainStepOut.String())
	}
	if len(stepRows) != 1 || stepRows[0].TotalJobs != 4 || stepRows[0].ExplainedJobs != 2 || len(stepRows[0].Jobs) != 2 {
		t.Fatalf("step-filtered pipeline explain = %+v", stepRows)
	}
	for _, explainedJob := range stepRows[0].Jobs {
		if len(explainedJob.Steps) != 1 || explainedJob.Steps[0].ID != "review" {
			t.Fatalf("step-filtered job retained unexpected steps: %+v", explainedJob)
		}
	}

	explainFailed := NewRootCmd()
	explainFailedOut, explainFailedErr := &bytes.Buffer{}, &bytes.Buffer{}
	explainFailed.SetOut(explainFailedOut)
	explainFailed.SetErr(explainFailedErr)
	explainFailed.SetArgs([]string{"pipeline", "explain", "ticket_to_pr", "--repo", root, "--state", "failed", "--json"})
	if err := explainFailed.Execute(); err != nil {
		t.Fatalf("pipeline explain failed filter: %v\nstderr=%s", err, explainFailedErr.String())
	}
	var failedRows []pipelineExplainRow
	if err := json.Unmarshal(explainFailedOut.Bytes(), &failedRows); err != nil {
		t.Fatalf("decode failed pipeline explain json: %v\nbody=%s", err, explainFailedOut.String())
	}
	if len(failedRows) != 1 || failedRows[0].TotalJobs != 4 || failedRows[0].ExplainedJobs != 1 || len(failedRows[0].Jobs) != 1 || failedRows[0].Jobs[0].JobID != "squ-611" {
		t.Fatalf("failed pipeline explain = %+v", failedRows)
	}

	explainInvalidState := NewRootCmd()
	explainInvalidStateOut, explainInvalidStateErr := &bytes.Buffer{}, &bytes.Buffer{}
	explainInvalidState.SetOut(explainInvalidStateOut)
	explainInvalidState.SetErr(explainInvalidStateErr)
	explainInvalidState.SetArgs([]string{"pipeline", "explain", "ticket_to_pr", "--repo", root, "--state", "stuck"})
	if err := explainInvalidState.Execute(); err == nil {
		t.Fatalf("pipeline explain invalid state succeeded")
	}
	if !strings.Contains(explainInvalidStateErr.String(), "--state must be ready") {
		t.Fatalf("invalid state stderr = %q", explainInvalidStateErr.String())
	}

	explainInvalidInterval := NewRootCmd()
	explainInvalidIntervalOut, explainInvalidIntervalErr := &bytes.Buffer{}, &bytes.Buffer{}
	explainInvalidInterval.SetOut(explainInvalidIntervalOut)
	explainInvalidInterval.SetErr(explainInvalidIntervalErr)
	explainInvalidInterval.SetArgs([]string{"pipeline", "explain", "ticket_to_pr", "--repo", root, "--watch", "--interval", "-1s"})
	if err := explainInvalidInterval.Execute(); err == nil {
		t.Fatalf("pipeline explain negative interval succeeded")
	}
	if !strings.Contains(explainInvalidIntervalErr.String(), "--interval must be >= 0") {
		t.Fatalf("invalid interval stderr = %q", explainInvalidIntervalErr.String())
	}

	explainInvalidSort := NewRootCmd()
	explainInvalidSortOut, explainInvalidSortErr := &bytes.Buffer{}, &bytes.Buffer{}
	explainInvalidSort.SetOut(explainInvalidSortOut)
	explainInvalidSort.SetErr(explainInvalidSortErr)
	explainInvalidSort.SetArgs([]string{"pipeline", "explain", "ticket_to_pr", "--repo", root, "--sort", "priority"})
	if err := explainInvalidSort.Execute(); err == nil {
		t.Fatalf("pipeline explain invalid sort succeeded")
	}
	if !strings.Contains(explainInvalidSortErr.String(), "--sort must be job") {
		t.Fatalf("invalid sort stderr = %q", explainInvalidSortErr.String())
	}

	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "json",
			args: []string{"pipeline", "explain", "ticket_to_pr", "--repo", root, "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "format",
			args: []string{"pipeline", "explain", "ticket_to_pr", "--repo", root, "--commands", "--format", "{{.Pipeline}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "watch",
			args: []string{"pipeline", "explain", "ticket_to_pr", "--repo", root, "--commands", "--watch"},
			want: "--commands cannot be combined with --watch",
		},
	} {
		cmd := NewRootCmd()
		invalidOut, invalidErr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(invalidOut)
		cmd.SetErr(invalidErr)
		cmd.SetArgs(tt.args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("pipeline explain --commands with %s succeeded", tt.name)
		}
		if !strings.Contains(invalidErr.String(), tt.want) {
			t.Fatalf("pipeline explain --commands with %s stderr = %q", tt.name, invalidErr.String())
		}
	}

	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "json",
			args: []string{"pipeline", "status", "--repo", root, "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "format",
			args: []string{"pipeline", "status", "--repo", root, "--commands", "--format", "{{.Pipeline}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "watch",
			args: []string{"pipeline", "status", "--repo", root, "--commands", "--watch"},
			want: "--commands cannot be combined with --watch",
		},
	} {
		cmd := NewRootCmd()
		invalidOut, invalidErr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(invalidOut)
		cmd.SetErr(invalidErr)
		cmd.SetArgs(tt.args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("pipeline status --commands with %s succeeded", tt.name)
		}
		if !strings.Contains(invalidErr.String(), tt.want) {
			t.Fatalf("pipeline status --commands with %s stderr = %q", tt.name, invalidErr.String())
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

	invalidInterval := NewRootCmd()
	invalidIntervalOut, invalidIntervalErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidInterval.SetOut(invalidIntervalOut)
	invalidInterval.SetErr(invalidIntervalErr)
	invalidInterval.SetArgs([]string{"pipeline", "status", "ticket_to_pr", "--repo", root, "--watch", "--interval", "-1s"})
	if err := invalidInterval.Execute(); err == nil {
		t.Fatalf("pipeline status negative interval succeeded")
	}
	if !strings.Contains(invalidIntervalErr.String(), "--interval must be >= 0") {
		t.Fatalf("invalid interval stderr = %q", invalidIntervalErr.String())
	}

	invalidLimit := NewRootCmd()
	invalidLimitOut, invalidLimitErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidLimit.SetOut(invalidLimitOut)
	invalidLimit.SetErr(invalidLimitErr)
	invalidLimit.SetArgs([]string{"pipeline", "status", "--repo", root, "--limit", "-1"})
	if err := invalidLimit.Execute(); err == nil {
		t.Fatalf("pipeline status negative limit succeeded")
	}
	if !strings.Contains(invalidLimitErr.String(), "--limit must be >= 0") {
		t.Fatalf("invalid limit stderr = %q", invalidLimitErr.String())
	}

	invalidSort := NewRootCmd()
	invalidSortOut, invalidSortErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidSort.SetOut(invalidSortOut)
	invalidSort.SetErr(invalidSortErr)
	invalidSort.SetArgs([]string{"pipeline", "status", "--repo", root, "--sort", "age"})
	if err := invalidSort.Execute(); err == nil {
		t.Fatalf("pipeline status invalid sort succeeded")
	}
	if !strings.Contains(invalidSortErr.String(), "--sort must be declared") {
		t.Fatalf("invalid sort stderr = %q", invalidSortErr.String())
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

func TestPipelineTimeoutMarksStaleRunningSteps(t *testing.T) {
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
timeout = "1h"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-700",
			Ticket:    "SQU-700",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-700", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
			},
		},
		{
			ID:        "squ-701",
			Ticket:    "SQU-701",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-30 * time.Minute),
			UpdatedAt: now.Add(-10 * time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-701", StartedAt: now.Add(-10 * time.Minute), Timeout: "1h0m0s"},
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
	dry.SetArgs([]string{"pipeline", "timeout", "ticket_to_pr", "--repo", root, "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("pipeline timeout dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryRows []pipelineTimeoutResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryRows); err != nil {
		t.Fatalf("decode dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(dryRows) != 1 || dryRows[0].JobID != "squ-700" || dryRows[0].Action != "would_fail" || dryRows[0].StepStatus != job.StatusRunning || dryRows[0].Timeout != "1h0m0s" {
		t.Fatalf("dry rows = %+v", dryRows)
	}
	unchanged, err := job.Read(teamDir, "squ-700")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.Status != job.StatusRunning || unchanged.Steps[0].Status != job.StatusRunning || unchanged.Steps[0].Instance != "worker-squ-700" {
		t.Fatalf("dry-run mutated job: %+v", unchanged)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"pipeline", "timeout", "ticket_to_pr", "--repo", root, "--message", "operator timed out stale step", "--dry-run", "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("pipeline timeout dry-run commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "pipeline", "timeout", "ticket_to_pr", "--repo", root, "--message", "operator timed out stale step"}), " ")
	if got := strings.TrimSpace(commandsOut.String()); got != wantCommand {
		t.Fatalf("pipeline timeout dry-run commands = %q, want %q", got, wantCommand)
	}

	apply := NewRootCmd()
	applyOut, applyErr := &bytes.Buffer{}, &bytes.Buffer{}
	apply.SetOut(applyOut)
	apply.SetErr(applyErr)
	timeoutFile := filepath.Join(root, "pipeline-timeout-message.txt")
	if err := os.WriteFile(timeoutFile, []byte("operator timed out stale step from file\n"), 0o644); err != nil {
		t.Fatalf("write timeout message: %v", err)
	}
	apply.SetArgs([]string{"pipeline", "timeout", "ticket_to_pr", "--repo", root, "--message-file", timeoutFile, "--json"})
	if err := apply.Execute(); err != nil {
		t.Fatalf("pipeline timeout apply: %v\nstderr=%s", err, applyErr.String())
	}
	var applied []pipelineTimeoutResult
	if err := json.Unmarshal(applyOut.Bytes(), &applied); err != nil {
		t.Fatalf("decode apply: %v\nbody=%s", err, applyOut.String())
	}
	if len(applied) != 1 || applied[0].Action != "failed" || applied[0].StepStatus != job.StatusFailed || applied[0].Instance != "" {
		t.Fatalf("applied rows = %+v", applied)
	}
	timedOut, err := job.Read(teamDir, "squ-700")
	if err != nil {
		t.Fatalf("read timed out job: %v", err)
	}
	if timedOut.Status != job.StatusFailed || timedOut.Steps[0].Status != job.StatusFailed || timedOut.Steps[0].Instance != "" || timedOut.LastStatus != "operator timed out stale step from file" {
		t.Fatalf("timed out job = %+v", timedOut)
	}
	stillRunning, err := job.Read(teamDir, "squ-701")
	if err != nil {
		t.Fatalf("read running job: %v", err)
	}
	if stillRunning.Status != job.StatusRunning || stillRunning.Steps[0].Status != job.StatusRunning {
		t.Fatalf("non-stale job changed: %+v", stillRunning)
	}

	retry := NewRootCmd()
	retryOut, retryErr := &bytes.Buffer{}, &bytes.Buffer{}
	retry.SetOut(retryOut)
	retry.SetErr(retryErr)
	retry.SetArgs([]string{"pipeline", "retry", "ticket_to_pr", "--repo", root, "--dry-run", "--json"})
	if err := retry.Execute(); err != nil {
		t.Fatalf("pipeline retry after timeout: %v\nstderr=%s", err, retryErr.String())
	}
	var retryRows []pipelineRetryResult
	if err := json.Unmarshal(retryOut.Bytes(), &retryRows); err != nil {
		t.Fatalf("decode retry: %v\nbody=%s", err, retryOut.String())
	}
	if len(retryRows) != 1 || retryRows[0].JobID != "squ-700" || retryRows[0].Action != "would_retry" {
		t.Fatalf("retry rows = %+v", retryRows)
	}
}

func TestPipelineTimeoutFiltersByTargetAgent(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-702",
			Ticket:    "SQU-702",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-702", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
			},
		},
		{
			ID:        "squ-703",
			Ticket:    "SQU-703",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "review", Target: "manager", Status: job.StatusRunning, Instance: "manager-squ-703", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
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
	cmd.SetArgs([]string{"pipeline", "timeout", "ticket_to_pr", "--repo", root, "--target-agent", "manager", "--message", "manager timeout", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline timeout --target-agent: %v\nstderr=%s", err, stderr.String())
	}
	var rows []pipelineTimeoutResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode target timeout: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].JobID != "squ-703" || rows[0].Target != "manager" || rows[0].Action != "failed" {
		t.Fatalf("rows = %+v", rows)
	}
	worker, err := job.Read(teamDir, "squ-702")
	if err != nil {
		t.Fatalf("read worker job: %v", err)
	}
	if worker.Status != job.StatusRunning || worker.Steps[0].Status != job.StatusRunning || worker.Steps[0].Instance != "worker-squ-702" {
		t.Fatalf("worker job changed = %+v", worker)
	}
	manager, err := job.Read(teamDir, "squ-703")
	if err != nil {
		t.Fatalf("read manager job: %v", err)
	}
	if manager.Status != job.StatusFailed || manager.Steps[0].Status != job.StatusFailed || manager.Steps[0].Instance != "" || manager.LastStatus != "manager timeout" {
		t.Fatalf("manager job = %+v", manager)
	}
}

func TestPipelineTimeoutCommandsValidation(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "without dry-run",
			args: []string{"pipeline", "timeout", "ticket_to_pr", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "json",
			args: []string{"pipeline", "timeout", "ticket_to_pr", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "format",
			args: []string{"pipeline", "timeout", "ticket_to_pr", "--dry-run", "--commands", "--format", "{{.JobID}}"},
			want: "--commands cannot be combined with --format",
		},
	} {
		cmd := NewRootCmd()
		out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(stderr)
		cmd.SetArgs(tt.args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("pipeline timeout --commands with %s succeeded", tt.name)
		}
		if !strings.Contains(stderr.String(), tt.want) {
			t.Fatalf("pipeline timeout --commands with %s stderr = %q, want %q", tt.name, stderr.String(), tt.want)
		}
	}
}

func TestPipelineAndTeamHoldReleaseJobs(t *testing.T) {
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

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr", "nightly"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-701",
			Ticket:    "SQU-701",
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
			ID:        "squ-702",
			Ticket:    "SQU-702",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusDone,
			CreatedAt: now,
			UpdatedAt: now.Add(time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusDone},
			},
		},
		{
			ID:        "squ-703",
			Ticket:    "SQU-703",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusFailed,
			CreatedAt: now,
			UpdatedAt: now.Add(2 * time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusFailed},
			},
		},
		{
			ID:        "squ-704",
			Ticket:    "SQU-704",
			Target:    "manager",
			Pipeline:  "nightly",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now.Add(3 * time.Minute),
			Steps: []job.Step{
				{ID: "triage", Target: "manager", Status: job.StatusBlocked},
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
	dry.SetArgs([]string{"pipeline", "hold", "ticket_to_pr", "release freeze", "--repo", root, "--for", "30m", "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("pipeline hold dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryRows []pipelineHoldResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryRows); err != nil {
		t.Fatalf("decode dry hold json: %v\nbody=%s", err, dryOut.String())
	}
	if len(dryRows) != 2 || dryRows[0].Action != "would_hold" || dryRows[1].Action != "would_hold" {
		t.Fatalf("dry hold rows = %+v", dryRows)
	}
	if dryRows[0].HoldUntil == "" || dryRows[1].HoldUntil == "" {
		t.Fatalf("dry hold rows missing hold_until = %+v", dryRows)
	}
	unchanged, err := job.Read(teamDir, "squ-703")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.Held {
		t.Fatalf("dry-run held job on disk: %+v", unchanged)
	}

	pipelineHoldCommands := NewRootCmd()
	pipelineHoldCommandsOut, pipelineHoldCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	pipelineHoldCommands.SetOut(pipelineHoldCommandsOut)
	pipelineHoldCommands.SetErr(pipelineHoldCommandsErr)
	pipelineHoldCommands.SetArgs([]string{"pipeline", "hold", "ticket_to_pr", "release freeze", "--repo", root, "--for", "30m", "--dry-run", "--commands"})
	if err := pipelineHoldCommands.Execute(); err != nil {
		t.Fatalf("pipeline hold dry-run commands: %v\nstderr=%s", err, pipelineHoldCommandsErr.String())
	}
	wantPipelineHoldCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "pipeline", "hold", "ticket_to_pr", "--repo", root, "--for", "30m0s", "release freeze"}), " ")
	if got := strings.TrimSpace(pipelineHoldCommandsOut.String()); got != wantPipelineHoldCommand {
		t.Fatalf("pipeline hold dry-run commands = %q, want %q", got, wantPipelineHoldCommand)
	}

	holdFailed := NewRootCmd()
	holdFailedOut, holdFailedErr := &bytes.Buffer{}, &bytes.Buffer{}
	holdFailed.SetOut(holdFailedOut)
	holdFailed.SetErr(holdFailedErr)
	holdMessageFile := filepath.Join(root, "pipeline-hold-message.txt")
	if err := os.WriteFile(holdMessageFile, []byte("freeze failed work from file\n"), 0o644); err != nil {
		t.Fatalf("write pipeline hold message: %v", err)
	}
	holdFailed.SetArgs([]string{"pipeline", "hold", "ticket_to_pr", "--repo", root, "--state", "failed", "--message-file", holdMessageFile, "--json"})
	if err := holdFailed.Execute(); err != nil {
		t.Fatalf("pipeline hold failed: %v\nstderr=%s", err, holdFailedErr.String())
	}
	var heldFailed []pipelineHoldResult
	if err := json.Unmarshal(holdFailedOut.Bytes(), &heldFailed); err != nil {
		t.Fatalf("decode failed hold json: %v\nbody=%s", err, holdFailedOut.String())
	}
	if len(heldFailed) != 1 || heldFailed[0].JobID != "squ-703" || heldFailed[0].Action != "held" || !heldFailed[0].HeldAfter || heldFailed[0].Message != "freeze failed work from file" {
		t.Fatalf("held failed rows = %+v", heldFailed)
	}
	heldFailedJob, err := job.Read(teamDir, "squ-703")
	if err != nil {
		t.Fatalf("read held failed job: %v", err)
	}
	if heldFailedJob.HoldReason != "freeze failed work from file" {
		t.Fatalf("held failed job = %+v", heldFailedJob)
	}

	jobList := NewRootCmd()
	jobListOut, jobListErr := &bytes.Buffer{}, &bytes.Buffer{}
	jobList.SetOut(jobListOut)
	jobList.SetErr(jobListErr)
	jobList.SetArgs([]string{"job", "ls", "--repo", root, "--held", "--json"})
	if err := jobList.Execute(); err != nil {
		t.Fatalf("job ls held: %v\nstderr=%s", err, jobListErr.String())
	}
	var heldJobs []job.Job
	if err := json.Unmarshal(jobListOut.Bytes(), &heldJobs); err != nil {
		t.Fatalf("decode held jobs: %v\nbody=%s", err, jobListOut.String())
	}
	if len(heldJobs) != 1 || heldJobs[0].ID != "squ-703" {
		t.Fatalf("held jobs = %+v", heldJobs)
	}

	pipelineJobs := NewRootCmd()
	pipelineJobsOut, pipelineJobsErr := &bytes.Buffer{}, &bytes.Buffer{}
	pipelineJobs.SetOut(pipelineJobsOut)
	pipelineJobs.SetErr(pipelineJobsErr)
	pipelineJobs.SetArgs([]string{"pipeline", "jobs", "ticket_to_pr", "--repo", root, "--held", "--summary", "--json"})
	if err := pipelineJobs.Execute(); err != nil {
		t.Fatalf("pipeline jobs held summary: %v\nstderr=%s", err, pipelineJobsErr.String())
	}
	var heldSummary jobSummary
	if err := json.Unmarshal(pipelineJobsOut.Bytes(), &heldSummary); err != nil {
		t.Fatalf("decode held summary: %v\nbody=%s", err, pipelineJobsOut.String())
	}
	if heldSummary.Total != 1 || heldSummary.Held != 1 {
		t.Fatalf("held summary = %+v", heldSummary)
	}

	teamJobs := NewRootCmd()
	teamJobsOut, teamJobsErr := &bytes.Buffer{}, &bytes.Buffer{}
	teamJobs.SetOut(teamJobsOut)
	teamJobs.SetErr(teamJobsErr)
	teamJobs.SetArgs([]string{"team", "jobs", "delivery", "--repo", root, "--held", "--json"})
	if err := teamJobs.Execute(); err != nil {
		t.Fatalf("team jobs held: %v\nstderr=%s", err, teamJobsErr.String())
	}
	heldJobs = nil
	if err := json.Unmarshal(teamJobsOut.Bytes(), &heldJobs); err != nil {
		t.Fatalf("decode team held jobs: %v\nbody=%s", err, teamJobsOut.String())
	}
	if len(heldJobs) != 1 || heldJobs[0].ID != "squ-703" {
		t.Fatalf("team held jobs = %+v", heldJobs)
	}

	teamNext := NewRootCmd()
	teamNextOut, teamNextErr := &bytes.Buffer{}, &bytes.Buffer{}
	teamNext.SetOut(teamNextOut)
	teamNext.SetErr(teamNextErr)
	teamNext.SetArgs([]string{"pipeline", "next", "ticket_to_pr", "--team", "delivery", "--repo", root, "--format", "{{.Pipeline}}|{{.Reason}}|{{.Action}}"})
	if err := teamNext.Execute(); err != nil {
		t.Fatalf("pipeline next team held: %v\nstderr=%s", err, teamNextErr.String())
	}
	for _, want := range []string{
		"ticket_to_pr|held_steps=1|agent-team team explain delivery --state held",
		"ticket_to_pr|held_steps=1|agent-team team ready delivery --state held",
	} {
		if !strings.Contains(teamNextOut.String(), want) {
			t.Fatalf("pipeline next team held missing %q:\n%s", want, teamNextOut.String())
		}
	}

	retryHeld := NewRootCmd()
	retryHeldOut, retryHeldErr := &bytes.Buffer{}, &bytes.Buffer{}
	retryHeld.SetOut(retryHeldOut)
	retryHeld.SetErr(retryHeldErr)
	retryHeld.SetArgs([]string{"pipeline", "retry", "ticket_to_pr", "--repo", root, "--dry-run", "--json"})
	if err := retryHeld.Execute(); err != nil {
		t.Fatalf("pipeline retry held: %v\nstderr=%s", err, retryHeldErr.String())
	}
	var retryRows []pipelineRetryResult
	if err := json.Unmarshal(retryHeldOut.Bytes(), &retryRows); err != nil {
		t.Fatalf("decode retry held json: %v\nbody=%s", err, retryHeldOut.String())
	}
	if len(retryRows) != 0 {
		t.Fatalf("held failed job was retryable: %+v", retryRows)
	}

	pipelineReleaseCommands := NewRootCmd()
	pipelineReleaseCommandsOut, pipelineReleaseCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	pipelineReleaseCommands.SetOut(pipelineReleaseCommandsOut)
	pipelineReleaseCommands.SetErr(pipelineReleaseCommandsErr)
	pipelineReleaseCommands.SetArgs([]string{"pipeline", "release", "ticket_to_pr", "--repo", root, "--message", "resume failed work", "--dry-run", "--commands"})
	if err := pipelineReleaseCommands.Execute(); err != nil {
		t.Fatalf("pipeline release dry-run commands: %v\nstderr=%s", err, pipelineReleaseCommandsErr.String())
	}
	wantPipelineReleaseCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "pipeline", "release", "ticket_to_pr", "--repo", root, "--message", "resume failed work"}), " ")
	if got := strings.TrimSpace(pipelineReleaseCommandsOut.String()); got != wantPipelineReleaseCommand {
		t.Fatalf("pipeline release dry-run commands = %q, want %q", got, wantPipelineReleaseCommand)
	}

	release := NewRootCmd()
	releaseOut, releaseErr := &bytes.Buffer{}, &bytes.Buffer{}
	release.SetOut(releaseOut)
	release.SetErr(releaseErr)
	releaseMessageFile := filepath.Join(root, "pipeline-release-message.txt")
	if err := os.WriteFile(releaseMessageFile, []byte("resume failed work from file\n"), 0o644); err != nil {
		t.Fatalf("write pipeline release message: %v", err)
	}
	release.SetArgs([]string{"pipeline", "release", "ticket_to_pr", "--repo", root, "--message-file", releaseMessageFile, "--json"})
	if err := release.Execute(); err != nil {
		t.Fatalf("pipeline release: %v\nstderr=%s", err, releaseErr.String())
	}
	var released []pipelineHoldResult
	if err := json.Unmarshal(releaseOut.Bytes(), &released); err != nil {
		t.Fatalf("decode release json: %v\nbody=%s", err, releaseOut.String())
	}
	if len(released) != 1 || released[0].JobID != "squ-703" || released[0].Action != "released" || released[0].HeldAfter || released[0].Message != "resume failed work from file" {
		t.Fatalf("released rows = %+v", released)
	}
	releasedJob, err := job.Read(teamDir, "squ-703")
	if err != nil {
		t.Fatalf("read released failed job: %v", err)
	}
	if releasedJob.LastStatus != "resume failed work from file" {
		t.Fatalf("released failed job = %+v", releasedJob)
	}

	retryReleased := NewRootCmd()
	retryReleasedOut, retryReleasedErr := &bytes.Buffer{}, &bytes.Buffer{}
	retryReleased.SetOut(retryReleasedOut)
	retryReleased.SetErr(retryReleasedErr)
	retryReleased.SetArgs([]string{"pipeline", "retry", "ticket_to_pr", "--repo", root, "--dry-run", "--json"})
	if err := retryReleased.Execute(); err != nil {
		t.Fatalf("pipeline retry released: %v\nstderr=%s", err, retryReleasedErr.String())
	}
	retryRows = nil
	if err := json.Unmarshal(retryReleasedOut.Bytes(), &retryRows); err != nil {
		t.Fatalf("decode retry released json: %v\nbody=%s", err, retryReleasedOut.String())
	}
	if len(retryRows) != 1 || retryRows[0].JobID != "squ-703" {
		t.Fatalf("released failed job was not retryable: %+v", retryRows)
	}

	teamHoldCommands := NewRootCmd()
	teamHoldCommandsOut, teamHoldCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	teamHoldCommands.SetOut(teamHoldCommandsOut)
	teamHoldCommands.SetErr(teamHoldCommandsErr)
	teamHoldCommands.SetArgs([]string{"team", "hold", "delivery", "--repo", root, "--state", "ready", "--limit", "1", "--message", "team freeze", "--dry-run", "--commands"})
	if err := teamHoldCommands.Execute(); err != nil {
		t.Fatalf("team hold dry-run commands: %v\nstderr=%s", err, teamHoldCommandsErr.String())
	}
	wantTeamHoldCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "team", "hold", "delivery", "--repo", root, "--state", "ready", "--limit", "1", "--message", "team freeze"}), " ")
	if got := strings.TrimSpace(teamHoldCommandsOut.String()); got != wantTeamHoldCommand {
		t.Fatalf("team hold dry-run commands = %q, want %q", got, wantTeamHoldCommand)
	}

	teamHold := NewRootCmd()
	teamHoldOut, teamHoldErr := &bytes.Buffer{}, &bytes.Buffer{}
	teamHold.SetOut(teamHoldOut)
	teamHold.SetErr(teamHoldErr)
	teamHoldMessageFile := filepath.Join(root, "team-hold-message.txt")
	if err := os.WriteFile(teamHoldMessageFile, []byte("team hold from file\n"), 0o644); err != nil {
		t.Fatalf("write team hold message: %v", err)
	}
	teamHold.SetArgs([]string{"team", "hold", "delivery", "--repo", root, "--state", "ready", "--limit", "1", "--message-file", teamHoldMessageFile, "--json"})
	if err := teamHold.Execute(); err != nil {
		t.Fatalf("team hold: %v\nstderr=%s", err, teamHoldErr.String())
	}
	var teamHeld []pipelineHoldResult
	if err := json.Unmarshal(teamHoldOut.Bytes(), &teamHeld); err != nil {
		t.Fatalf("decode team hold json: %v\nbody=%s", err, teamHoldOut.String())
	}
	if len(teamHeld) != 1 || teamHeld[0].Action != "held" || !teamHeld[0].HeldAfter || teamHeld[0].Message != "team hold from file" {
		t.Fatalf("team held rows = %+v", teamHeld)
	}

	teamReleaseCommands := NewRootCmd()
	teamReleaseCommandsOut, teamReleaseCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	teamReleaseCommands.SetOut(teamReleaseCommandsOut)
	teamReleaseCommands.SetErr(teamReleaseCommandsErr)
	teamReleaseCommands.SetArgs([]string{"team", "release", "delivery", "--repo", root, "--message", "team resume", "--dry-run", "--commands"})
	if err := teamReleaseCommands.Execute(); err != nil {
		t.Fatalf("team release dry-run commands: %v\nstderr=%s", err, teamReleaseCommandsErr.String())
	}
	wantTeamReleaseCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "team", "release", "delivery", "--repo", root, "--message", "team resume"}), " ")
	if got := strings.TrimSpace(teamReleaseCommandsOut.String()); got != wantTeamReleaseCommand {
		t.Fatalf("team release dry-run commands = %q, want %q", got, wantTeamReleaseCommand)
	}

	teamRelease := NewRootCmd()
	teamReleaseOut, teamReleaseErr := &bytes.Buffer{}, &bytes.Buffer{}
	teamRelease.SetOut(teamReleaseOut)
	teamRelease.SetErr(teamReleaseErr)
	teamReleaseMessageFile := filepath.Join(root, "team-release-message.txt")
	if err := os.WriteFile(teamReleaseMessageFile, []byte("team release from file\n"), 0o644); err != nil {
		t.Fatalf("write team release message: %v", err)
	}
	teamRelease.SetArgs([]string{"team", "release", "delivery", "--repo", root, "--message-file", teamReleaseMessageFile, "--json"})
	if err := teamRelease.Execute(); err != nil {
		t.Fatalf("team release: %v\nstderr=%s", err, teamReleaseErr.String())
	}
	var teamReleased []pipelineHoldResult
	if err := json.Unmarshal(teamReleaseOut.Bytes(), &teamReleased); err != nil {
		t.Fatalf("decode team release json: %v\nbody=%s", err, teamReleaseOut.String())
	}
	if len(teamReleased) != 1 || teamReleased[0].Action != "released" || teamReleased[0].HeldAfter || teamReleased[0].Message != "team release from file" {
		t.Fatalf("team release rows = %+v", teamReleased)
	}
}

func TestPipelineAndTeamHoldReleaseCommandsValidation(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "pipeline hold without dry-run",
			args: []string{"pipeline", "hold", "ticket_to_pr", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "pipeline hold json",
			args: []string{"pipeline", "hold", "ticket_to_pr", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "pipeline release format",
			args: []string{"pipeline", "release", "ticket_to_pr", "--dry-run", "--commands", "--format", "{{.JobID}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "team hold without dry-run",
			args: []string{"team", "hold", "delivery", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "team release json",
			args: []string{"team", "release", "delivery", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "team release format",
			args: []string{"team", "release", "delivery", "--dry-run", "--commands", "--format", "{{.JobID}}"},
			want: "--commands cannot be combined with --format",
		},
	} {
		cmd := NewRootCmd()
		out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(stderr)
		cmd.SetArgs(tt.args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("%s succeeded", tt.name)
		}
		if !strings.Contains(stderr.String(), tt.want) {
			t.Fatalf("%s stderr = %q, want %q", tt.name, stderr.String(), tt.want)
		}
	}
}

func TestPipelineReleaseExpiredHolds(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)
	teamDir := filepath.Join(root, ".agent_team")
	now := time.Now().UTC().Truncate(time.Second)
	jobs := []*job.Job{
		{
			ID:         "squ-750",
			Ticket:     "SQU-750",
			Target:     "worker",
			Pipeline:   "ticket_to_pr",
			Status:     job.StatusRunning,
			Held:       true,
			HoldReason: "expired freeze",
			HoldUntil:  now.Add(-time.Minute),
			CreatedAt:  now,
			UpdatedAt:  now,
			Steps:      []job.Step{{ID: "implement", Target: "worker", Status: job.StatusBlocked}},
		},
		{
			ID:         "squ-751",
			Ticket:     "SQU-751",
			Target:     "worker",
			Pipeline:   "ticket_to_pr",
			Status:     job.StatusRunning,
			Held:       true,
			HoldReason: "active freeze",
			HoldUntil:  now.Add(time.Hour),
			CreatedAt:  now,
			UpdatedAt:  now,
			Steps:      []job.Step{{ID: "implement", Target: "worker", Status: job.StatusBlocked}},
		},
		{
			ID:         "squ-752",
			Ticket:     "SQU-752",
			Target:     "worker",
			Pipeline:   "ticket_to_pr",
			Status:     job.StatusRunning,
			Held:       true,
			HoldReason: "indefinite freeze",
			CreatedAt:  now,
			UpdatedAt:  now,
			Steps:      []job.Step{{ID: "implement", Target: "worker", Status: job.StatusBlocked}},
		},
	}
	for _, j := range jobs {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	release := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	release.SetOut(out)
	release.SetErr(errOut)
	release.SetArgs([]string{"pipeline", "release", "ticket_to_pr", "--repo", root, "--expired", "--json"})
	if err := release.Execute(); err != nil {
		t.Fatalf("pipeline release expired: %v\nstderr=%s", err, errOut.String())
	}
	var rows []pipelineHoldResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode release expired json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].JobID != "squ-750" || rows[0].Action != "released" || rows[0].HoldUntil == "" {
		t.Fatalf("release expired rows = %+v", rows)
	}
	expired, err := job.Read(teamDir, "squ-750")
	if err != nil {
		t.Fatalf("read expired job: %v", err)
	}
	if expired.Held || !expired.HoldUntil.IsZero() {
		t.Fatalf("expired job after release = %+v", expired)
	}
	active, err := job.Read(teamDir, "squ-751")
	if err != nil {
		t.Fatalf("read active job: %v", err)
	}
	if !active.Held || active.HoldUntil.IsZero() {
		t.Fatalf("active job after expired release = %+v", active)
	}
	indefinite, err := job.Read(teamDir, "squ-752")
	if err != nil {
		t.Fatalf("read indefinite job: %v", err)
	}
	if !indefinite.Held || !indefinite.HoldUntil.IsZero() {
		t.Fatalf("indefinite job after expired release = %+v", indefinite)
	}
}

func TestPipelineSnapshotScopesWorkflow(t *testing.T) {
	target, _, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[pipelines.platform_work]
trigger.event = "ticket.created"
trigger.match.team = "platform"

[[pipelines.platform_work.steps]]
id = "implement"
target = "worker"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-704",
			Ticket:    "SQU-704",
			Target:    "worker",
			Kickoff:   "SQU-704: implement",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Instance: "worker-squ-704-implement", Status: job.StatusBlocked},
			},
		},
		{
			ID:        "squ-705",
			Ticket:    "SQU-705",
			Target:    "worker",
			Kickoff:   "SQU-705: platform work",
			Pipeline:  "platform_work",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Instance: "worker-squ-705-implement", Status: job.StatusBlocked},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-ticket-pipeline",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-704-implement",
			Payload: map[string]any{
				"job_id":       "squ-704",
				"target":       "worker",
				"ticket":       "SQU-704",
				"access_token": "ticket-secret",
			},
			QueuedAt:  now.Add(-time.Minute),
			UpdatedAt: now.Add(-time.Minute),
		},
		{
			ID:         "q-platform-pipeline",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-705-implement",
			Payload:    map[string]any{"job_id": "squ-705", "target": "worker", "ticket": "SQU-705"},
			QueuedAt:   now.Add(-time.Minute),
			UpdatedAt:  now.Add(-time.Minute),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("write queue item %s: %v", item.ID, err)
		}
	}
	for _, item := range []*daemon.OutboxItem{
		{
			ID:     "outbox-ticket-pipeline",
			State:  daemon.OutboxStatePending,
			Type:   "agent.dispatch",
			Source: "manager",
			Payload: map[string]any{
				"job_id":       "squ-704",
				"target":       "worker",
				"ticket":       "SQU-704",
				"access_token": "outbox-ticket-secret",
			},
			CreatedAt: now.Add(-3 * time.Minute),
			UpdatedAt: now.Add(-3 * time.Minute),
		},
		{
			ID:        "outbox-platform-pipeline",
			State:     daemon.OutboxStatePending,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "squ-705", "target": "worker", "ticket": "SQU-705"},
			CreatedAt: now.Add(-3 * time.Minute),
			UpdatedAt: now.Add(-3 * time.Minute),
		},
	} {
		if err := daemon.WriteOutboxItem(teamDir, item); err != nil {
			t.Fatalf("write outbox item %s: %v", item.ID, err)
		}
	}
	writeQuarantinedQueueItem(t, teamDir, "20260619T000000.000000000Z", daemon.QueueStatePending, &daemon.QueueItem{
		ID:         "q-ticket-quarantined",
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-704-implement",
		Payload:    map[string]any{"job_id": "squ-704", "target": "worker", "ticket": "SQU-704"},
		QueuedAt:   now.Add(-2 * time.Minute),
		UpdatedAt:  now.Add(-2 * time.Minute),
	})
	writeQuarantinedQueueItem(t, teamDir, "20260619T000000.000000000Z", daemon.QueueStatePending, &daemon.QueueItem{
		ID:         "q-platform-quarantined",
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-705-implement",
		Payload:    map[string]any{"job_id": "squ-705", "target": "worker", "ticket": "SQU-705"},
		QueuedAt:   now.Add(-2 * time.Minute),
		UpdatedAt:  now.Add(-2 * time.Minute),
	})
	writeQuarantinedOutboxFile(t, teamDir, "20260619T010000.000000000Z", daemon.OutboxStatePending, &daemon.OutboxItem{
		ID:        "outbox-ticket-quarantined",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Source:    "manager",
		Payload:   map[string]any{"job_id": "squ-704", "target": "worker", "ticket": "SQU-704"},
		CreatedAt: now.Add(-2 * time.Minute),
		UpdatedAt: now.Add(-2 * time.Minute),
	})
	writeQuarantinedOutboxFile(t, teamDir, "20260619T010000.000000000Z", daemon.OutboxStatePending, &daemon.OutboxItem{
		ID:        "outbox-platform-quarantined",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Source:    "manager",
		Payload:   map[string]any{"job_id": "squ-705", "target": "worker", "ticket": "SQU-705"},
		CreatedAt: now.Add(-2 * time.Minute),
		UpdatedAt: now.Add(-2 * time.Minute),
	})
	for _, msg := range []struct {
		instance string
		id       string
		body     string
	}{
		{instance: "worker-squ-704-implement", id: "msg-ticket-pipeline", body: "ticket pipeline inbox secret"},
		{instance: "worker-squ-705-implement", id: "msg-platform-pipeline", body: "platform pipeline inbox secret"},
	} {
		if err := daemon.AppendMessage(daemon.DaemonRoot(teamDir), msg.instance, &daemon.Message{
			ID:   msg.id,
			From: "manager",
			Body: msg.body,
			TS:   now.Add(-30 * time.Second),
		}); err != nil {
			t.Fatalf("append inbox message %s: %v", msg.id, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "snapshot", "ticket_to_pr", "--repo", target, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline snapshot json: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot pipelineSnapshotResult
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode pipeline snapshot json: %v\nbody=%s", err, out.String())
	}
	if snapshot.Pipeline != "ticket_to_pr" || snapshot.Version == "" || snapshot.CapturedAt == "" || snapshot.Repo == "" || snapshot.TeamDir == "" {
		t.Fatalf("snapshot metadata = %+v", snapshot)
	}
	if snapshot.Provenance == nil || snapshot.Provenance.Command != "agent-team pipeline snapshot" || snapshot.Provenance.Scope != "pipeline" || snapshot.Provenance.Subject != "ticket_to_pr" || !snapshot.Provenance.Options.Redacted {
		t.Fatalf("snapshot provenance = %+v", snapshot.Provenance)
	}
	if !snapshot.Redacted {
		t.Fatalf("snapshot should redact by default")
	}
	if snapshot.Status == nil || snapshot.Status.Pipeline != "ticket_to_pr" || !snapshot.Status.Declared || snapshot.Status.Jobs != 1 || snapshot.Status.ReadySteps != 1 {
		t.Fatalf("snapshot status = %+v", snapshot.Status)
	}
	if snapshot.Explain == nil || snapshot.Explain.Pipeline != "ticket_to_pr" || snapshot.Explain.ExplainedJobs != 1 || len(snapshot.Explain.Jobs) != 1 || snapshot.Explain.Jobs[0].JobID != "squ-704" {
		t.Fatalf("snapshot explain = %+v", snapshot.Explain)
	}
	if len(snapshot.Jobs) != 1 || snapshot.Jobs[0].ID != "squ-704" {
		t.Fatalf("snapshot jobs = %+v", snapshot.Jobs)
	}
	if snapshot.InboxSummary == nil || snapshot.InboxSummary.Total != 1 || snapshot.InboxSummary.Unread != 1 || snapshot.InboxSummary.UnreadInstances != 1 {
		t.Fatalf("snapshot inbox summary = %+v", snapshot.InboxSummary)
	}
	if len(snapshot.Inbox) != 1 || snapshot.Inbox[0].Instance != "worker-squ-704-implement" || snapshot.Inbox[0].LatestID != "msg-ticket-pipeline" || snapshot.Inbox[0].LatestBody != snapshotRedactedValue {
		t.Fatalf("snapshot inbox = %+v", snapshot.Inbox)
	}
	if len(snapshot.Queue) != 1 || snapshot.Queue[0].ID != "q-ticket-pipeline" || snapshot.QueueSummary == nil || snapshot.QueueSummary.Total != 1 || snapshot.QueueSummary.Quarantined != 1 || snapshot.QueueSummary.QuarantineRestorable != 1 {
		t.Fatalf("snapshot queue = %+v summary=%+v", snapshot.Queue, snapshot.QueueSummary)
	}
	if snapshot.Queue[0].Payload["access_token"] != snapshotRedactedValue {
		t.Fatalf("snapshot queue payload not redacted: %+v", snapshot.Queue[0].Payload)
	}
	if len(snapshot.Outbox) != 1 || snapshot.Outbox[0].ID != "outbox-ticket-pipeline" || snapshot.OutboxSummary == nil || snapshot.OutboxSummary.Total != 1 || snapshot.OutboxSummary.Pending != 1 {
		t.Fatalf("snapshot outbox = %+v summary=%+v", snapshot.Outbox, snapshot.OutboxSummary)
	}
	if len(snapshot.OutboxQuarantine) != 1 || snapshot.OutboxQuarantine[0].ID != "outbox-ticket-quarantined" || snapshot.OutboxQuarantine[0].Job != "squ-704" || snapshot.OutboxQuarantineSummary == nil || snapshot.OutboxQuarantineSummary.Quarantined != 1 || snapshot.OutboxQuarantineSummary.Restorable != 1 {
		t.Fatalf("snapshot outbox quarantine = %+v summary=%+v", snapshot.OutboxQuarantine, snapshot.OutboxQuarantineSummary)
	}
	if snapshot.Outbox[0].Payload["access_token"] != snapshotRedactedValue {
		t.Fatalf("snapshot outbox payload not redacted: %+v", snapshot.Outbox[0].Payload)
	}
	if len(snapshot.QueueQuarantine) != 1 || snapshot.QueueQuarantine[0].ID != "q-ticket-quarantined" || snapshot.QueueQuarantine[0].Job != "squ-704" {
		t.Fatalf("snapshot queue quarantine = %+v", snapshot.QueueQuarantine)
	}
	if len(snapshot.AdvancePreview) != 1 || snapshot.AdvancePreview[0].JobID != "squ-704" || snapshot.AdvancePreview[0].Action != "would_advance" || !snapshot.AdvancePreview[0].DryRun {
		t.Fatalf("snapshot advance = %+v", snapshot.AdvancePreview)
	}
	preview := snapshot.AdvancePreview[0].Preview
	if preview == nil || preview.Step == nil || preview.Step.ID != "implement" || preview.Dispatch == nil || preview.Dispatch.RequestedName != "worker-squ-704-implement" {
		t.Fatalf("snapshot route preview = %+v", preview)
	}
	if strings.Contains(out.String(), "platform_work") || strings.Contains(out.String(), "squ-705") || strings.Contains(out.String(), "q-platform") || strings.Contains(out.String(), "outbox-platform") || strings.Contains(out.String(), "ticket-secret") || strings.Contains(out.String(), "outbox-ticket-secret") || strings.Contains(out.String(), "ticket pipeline inbox secret") || strings.Contains(out.String(), "platform pipeline inbox secret") {
		t.Fatalf("pipeline snapshot leaked unrelated workflow:\n%s", out.String())
	}

	raw := NewRootCmd()
	rawOut, rawErr := &bytes.Buffer{}, &bytes.Buffer{}
	raw.SetOut(rawOut)
	raw.SetErr(rawErr)
	raw.SetArgs([]string{"pipeline", "snapshot", "ticket_to_pr", "--repo", target, "--no-redact", "--json"})
	if err := raw.Execute(); err != nil {
		t.Fatalf("pipeline snapshot no-redact: %v\nstderr=%s", err, rawErr.String())
	}
	var rawSnapshot pipelineSnapshotResult
	if err := json.Unmarshal(rawOut.Bytes(), &rawSnapshot); err != nil {
		t.Fatalf("decode raw pipeline snapshot json: %v\nbody=%s", err, rawOut.String())
	}
	if len(rawSnapshot.Inbox) != 1 || rawSnapshot.Inbox[0].LatestBody != "ticket pipeline inbox secret" {
		t.Fatalf("raw pipeline inbox = %+v", rawSnapshot.Inbox)
	}
	if len(rawSnapshot.Outbox) != 1 || rawSnapshot.Outbox[0].Payload["access_token"] != "outbox-ticket-secret" {
		t.Fatalf("raw pipeline outbox = %+v", rawSnapshot.Outbox)
	}
	if strings.Contains(rawOut.String(), "platform pipeline inbox secret") || strings.Contains(rawOut.String(), "outbox-platform") {
		t.Fatalf("raw pipeline snapshot leaked unrelated workflow:\n%s", rawOut.String())
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"pipeline", "snapshot", "ticket_to_pr", "--repo", target})
	if err := text.Execute(); err != nil {
		t.Fatalf("pipeline snapshot text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"pipeline snapshot:", "pipeline: ticket_to_pr", "command: agent-team pipeline snapshot scope=pipeline subject=ticket_to_pr", "status: jobs=1 ready_steps=1", "explain: jobs=1 steps=1", "jobs: total=1", "inbox: instances=1 total=1 unread=1 unread_instances=1", "queue: total=1 pending=1 dead=0 delayed=0 attempts=0 quarantined=1 restorable=1 unrestorable=0", "outbox: total=1 pending=1 failed=0 processed=0", "outbox quarantine: quarantined=1 restorable=1 unrestorable=0", "advance: ready=1 route_previews=1"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("pipeline snapshot text missing %q:\n%s", want, textOut.String())
		}
	}
	for _, leak := range []string{"platform_work", "squ-705", "q-platform", "outbox-platform", "outbox-ticket-secret", "ticket pipeline inbox secret", "platform pipeline inbox secret"} {
		if strings.Contains(textOut.String(), leak) {
			t.Fatalf("pipeline snapshot text leaked %q:\n%s", leak, textOut.String())
		}
	}

	outputPath := filepath.Join(target, "ticket-to-pr.snapshot.json")
	fileCmd := NewRootCmd()
	fileOut, fileErr := &bytes.Buffer{}, &bytes.Buffer{}
	fileCmd.SetOut(fileOut)
	fileCmd.SetErr(fileErr)
	fileCmd.SetArgs([]string{"pipeline", "snapshot", "ticket_to_pr", "--repo", target, "--output", outputPath})
	if err := fileCmd.Execute(); err != nil {
		t.Fatalf("pipeline snapshot output: %v\nstderr=%s", err, fileErr.String())
	}
	if !strings.Contains(fileOut.String(), "Wrote pipeline snapshot to ") {
		t.Fatalf("pipeline snapshot output message = %q", fileOut.String())
	}
	fileBody, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read pipeline snapshot output: %v", err)
	}
	var fileSnapshot pipelineSnapshotResult
	if err := json.Unmarshal(fileBody, &fileSnapshot); err != nil {
		t.Fatalf("decode pipeline snapshot output: %v\nbody=%s", err, string(fileBody))
	}
	if fileSnapshot.Pipeline != "ticket_to_pr" || len(fileSnapshot.Jobs) != 1 || fileSnapshot.Jobs[0].ID != "squ-704" {
		t.Fatalf("pipeline snapshot file = %+v", fileSnapshot)
	}
}

func TestPipelineNextReportsRecommendedActions(t *testing.T) {
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

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-710",
			Ticket:    "SQU-710",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusBlocked},
				{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}},
			},
		},
		{
			ID:        "squ-711",
			Ticket:    "SQU-711",
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
			ID:        "squ-712",
			Ticket:    "SQU-712",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusQueued, Instance: "worker-squ-712-implement"},
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
	cmd.SetArgs([]string{"pipeline", "next", "--repo", root, "--limit", "2", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline next json: %v\nstderr=%s", err, stderr.String())
	}
	var actions []pipelineNextAction
	if err := json.Unmarshal(out.Bytes(), &actions); err != nil {
		t.Fatalf("decode pipeline next json: %v\nbody=%s", err, out.String())
	}
	if len(actions) != 2 {
		t.Fatalf("actions = %+v, want two limited actions", actions)
	}
	if actions[0].Pipeline != "ticket_to_pr" || actions[0].Reason != "ready_steps=1" || actions[0].Action != "agent-team pipeline tick ticket_to_pr --dry-run --preview-routes" || actions[0].Status.ReadySteps != 1 {
		t.Fatalf("first action = %+v, want ready tick", actions[0])
	}
	if actions[1].Reason != "failed_steps=1" || actions[1].Action != "agent-team pipeline retry ticket_to_pr --dry-run --dispatch --preview-routes" || actions[1].Status.FailedSteps != 1 {
		t.Fatalf("second action = %+v, want failed retry", actions[1])
	}

	formatCmd := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatCmd.SetOut(formatOut)
	formatCmd.SetErr(formatErr)
	formatCmd.SetArgs([]string{"pipeline", "next", "ticket_to_pr", "--repo", root, "--format", "{{.Pipeline}}|{{.Reason}}|{{.Action}}"})
	if err := formatCmd.Execute(); err != nil {
		t.Fatalf("pipeline next format: %v\nstderr=%s", err, formatErr.String())
	}
	for _, want := range []string{
		"ticket_to_pr|ready_steps=1|agent-team pipeline tick ticket_to_pr --dry-run --preview-routes",
		"ticket_to_pr|failed_steps=1|agent-team pipeline repair ticket_to_pr --retry-pipelines --dry-run --preview-routes",
		"ticket_to_pr|failed_steps=1|agent-team repair --retry-pipelines --dry-run --preview-routes",
		"ticket_to_pr|failed_steps=1|agent-team pipeline explain ticket_to_pr --state failed",
		"ticket_to_pr|failed_steps=1|agent-team pipeline ready ticket_to_pr --state failed",
	} {
		if !strings.Contains(formatOut.String(), want) {
			t.Fatalf("pipeline next format missing %q:\n%s", want, formatOut.String())
		}
	}

	reasonCmd := NewRootCmd()
	reasonOut, reasonErr := &bytes.Buffer{}, &bytes.Buffer{}
	reasonCmd.SetOut(reasonOut)
	reasonCmd.SetErr(reasonErr)
	reasonCmd.SetArgs([]string{"pipeline", "next", "ticket_to_pr", "--repo", root, "--reason", "failed_steps", "--limit", "1", "--json"})
	if err := reasonCmd.Execute(); err != nil {
		t.Fatalf("pipeline next reason json: %v\nstderr=%s", err, reasonErr.String())
	}
	var reasonActions []pipelineNextAction
	if err := json.Unmarshal(reasonOut.Bytes(), &reasonActions); err != nil {
		t.Fatalf("decode pipeline next reason json: %v\nbody=%s", err, reasonOut.String())
	}
	if len(reasonActions) != 1 || reasonActions[0].Reason != "failed_steps=1" || reasonActions[0].Action != "agent-team pipeline retry ticket_to_pr --dry-run --dispatch --preview-routes" {
		t.Fatalf("reason-filtered actions = %+v, want first failed action", reasonActions)
	}

	commandsCmd := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commandsCmd.SetOut(commandsOut)
	commandsCmd.SetErr(commandsErr)
	commandsCmd.SetArgs([]string{"pipeline", "next", "ticket_to_pr", "--repo", root, "--reason", "ready_steps", "--commands"})
	if err := commandsCmd.Execute(); err != nil {
		t.Fatalf("pipeline next commands: %v\nstderr=%s", err, commandsErr.String())
	}
	if got, want := commandsOut.String(), "agent-team pipeline tick ticket_to_pr --dry-run --preview-routes\n"; got != want {
		t.Fatalf("pipeline next commands = %q, want %q", got, want)
	}

	multiReasonCmd := NewRootCmd()
	multiReasonOut, multiReasonErr := &bytes.Buffer{}, &bytes.Buffer{}
	multiReasonCmd.SetOut(multiReasonOut)
	multiReasonCmd.SetErr(multiReasonErr)
	multiReasonCmd.SetArgs([]string{"pipeline", "next", "ticket_to_pr", "--repo", root, "--reason", "ready_steps,failed_steps", "--format", "{{.Reason}}|{{.Action}}"})
	if err := multiReasonCmd.Execute(); err != nil {
		t.Fatalf("pipeline next multi-reason format: %v\nstderr=%s", err, multiReasonErr.String())
	}
	for _, want := range []string{
		"ready_steps=1|agent-team pipeline tick ticket_to_pr --dry-run --preview-routes",
		"failed_steps=1|agent-team pipeline retry ticket_to_pr --dry-run --dispatch --preview-routes",
	} {
		if !strings.Contains(multiReasonOut.String(), want) {
			t.Fatalf("pipeline next multi-reason missing %q:\n%s", want, multiReasonOut.String())
		}
	}
	if strings.Contains(multiReasonOut.String(), "queue_dead=1|") {
		t.Fatalf("pipeline next multi-reason included queue action:\n%s", multiReasonOut.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	watchCmd := NewRootCmd()
	watchOut, watchErr := &bytes.Buffer{}, &bytes.Buffer{}
	watchCmd.SetContext(ctx)
	watchCmd.SetOut(watchOut)
	watchCmd.SetErr(watchErr)
	watchCmd.SetArgs([]string{"pipeline", "next", "ticket_to_pr", "--repo", root, "--reason", "ready_steps", "--limit", "1", "--watch", "--no-clear", "--interval", "1h", "--format", "{{.Pipeline}}|{{.Reason}}|{{.Action}}"})
	if err := watchCmd.Execute(); err != nil {
		t.Fatalf("pipeline next watch: %v\nstderr=%s", err, watchErr.String())
	}
	if got := strings.TrimSpace(watchOut.String()); got != "ticket_to_pr|ready_steps=1|agent-team pipeline tick ticket_to_pr --dry-run --preview-routes" || strings.Contains(watchOut.String(), watchClearSequence) {
		t.Fatalf("pipeline next watch output = %q", watchOut.String())
	}

	teamCmd := NewRootCmd()
	teamOut, teamErr := &bytes.Buffer{}, &bytes.Buffer{}
	teamCmd.SetOut(teamOut)
	teamCmd.SetErr(teamErr)
	teamCmd.SetArgs([]string{"pipeline", "next", "ticket_to_pr", "--team", "delivery", "--repo", root, "--reason", "failed_steps", "--format", "{{.Pipeline}}|{{.Reason}}|{{.Action}}"})
	if err := teamCmd.Execute(); err != nil {
		t.Fatalf("pipeline next team format: %v\nstderr=%s", err, teamErr.String())
	}
	for _, want := range []string{
		"ticket_to_pr|failed_steps=1|agent-team team repair delivery --retry-pipelines --dry-run --preview-routes",
		"ticket_to_pr|failed_steps=1|agent-team team explain delivery --state failed",
		"ticket_to_pr|failed_steps=1|agent-team team ready delivery --state failed",
	} {
		if !strings.Contains(teamOut.String(), want) {
			t.Fatalf("pipeline next team format missing %q:\n%s", want, teamOut.String())
		}
	}
	if strings.Contains(teamOut.String(), "ready_steps=1|") {
		t.Fatalf("pipeline next team reason filter included ready action:\n%s", teamOut.String())
	}

	teamCommands := NewRootCmd()
	teamCommandsOut, teamCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	teamCommands.SetOut(teamCommandsOut)
	teamCommands.SetErr(teamCommandsErr)
	teamCommands.SetArgs([]string{"pipeline", "next", "ticket_to_pr", "--team", "delivery", "--repo", root, "--reason", "failed_steps", "--commands"})
	if err := teamCommands.Execute(); err != nil {
		t.Fatalf("pipeline next team commands: %v\nstderr=%s", err, teamCommandsErr.String())
	}
	if strings.Contains(teamCommandsOut.String(), "PIPELINE") || strings.Contains(teamCommandsOut.String(), "failed_steps=") {
		t.Fatalf("pipeline next team commands should not include table headers or reasons:\n%s", teamCommandsOut.String())
	}
	if !strings.Contains(teamCommandsOut.String(), "agent-team team repair delivery --retry-pipelines --dry-run --preview-routes") {
		t.Fatalf("pipeline next team commands missing team-scoped repair:\n%s", teamCommandsOut.String())
	}

	invalidInterval := NewRootCmd()
	invalidIntervalOut, invalidIntervalErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidInterval.SetOut(invalidIntervalOut)
	invalidInterval.SetErr(invalidIntervalErr)
	invalidInterval.SetArgs([]string{"pipeline", "next", "ticket_to_pr", "--repo", root, "--watch", "--interval", "-1s"})
	if err := invalidInterval.Execute(); err == nil {
		t.Fatalf("pipeline next negative interval succeeded")
	}
	if !strings.Contains(invalidIntervalErr.String(), "--interval must be >= 0") {
		t.Fatalf("invalid interval stderr = %q", invalidIntervalErr.String())
	}

	invalidReason := NewRootCmd()
	invalidReasonOut, invalidReasonErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidReason.SetOut(invalidReasonOut)
	invalidReason.SetErr(invalidReasonErr)
	invalidReason.SetArgs([]string{"pipeline", "next", "ticket_to_pr", "--repo", root, "--reason", ","})
	if err := invalidReason.Execute(); err == nil {
		t.Fatalf("pipeline next empty reason succeeded")
	}
	if !strings.Contains(invalidReasonErr.String(), "--reason requires") {
		t.Fatalf("invalid reason stderr = %q stdout=%q", invalidReasonErr.String(), invalidReasonOut.String())
	}

	invalidSort := NewRootCmd()
	invalidSortOut, invalidSortErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidSort.SetOut(invalidSortOut)
	invalidSort.SetErr(invalidSortErr)
	invalidSort.SetArgs([]string{"pipeline", "next", "ticket_to_pr", "--repo", root, "--sort", "age"})
	if err := invalidSort.Execute(); err == nil {
		t.Fatalf("pipeline next invalid sort succeeded")
	}
	if !strings.Contains(invalidSortErr.String(), "--sort must be declared") {
		t.Fatalf("invalid sort stderr = %q stdout=%q", invalidSortErr.String(), invalidSortOut.String())
	}

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "json",
			args: []string{"pipeline", "next", "ticket_to_pr", "--repo", root, "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "format",
			args: []string{"pipeline", "next", "ticket_to_pr", "--repo", root, "--commands", "--format", "{{.Action}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "watch",
			args: []string{"pipeline", "next", "ticket_to_pr", "--repo", root, "--commands", "--watch"},
			want: "--commands cannot be combined with --watch",
		},
	} {
		t.Run("commands-conflict-"+tc.name, func(t *testing.T) {
			conflict := NewRootCmd()
			conflictOut, conflictErr := &bytes.Buffer{}, &bytes.Buffer{}
			conflict.SetOut(conflictOut)
			conflict.SetErr(conflictErr)
			conflict.SetArgs(tc.args)
			if err := conflict.Execute(); err == nil {
				t.Fatalf("pipeline next accepted %s conflict: stdout=%s", tc.name, conflictOut.String())
			}
			if !strings.Contains(conflictErr.String(), tc.want) {
				t.Fatalf("pipeline next %s conflict stderr = %q, want %q", tc.name, conflictErr.String(), tc.want)
			}
		})
	}
}

func TestPipelineNextSortsPipelinesBeforeActionLimit(t *testing.T) {
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
id = "audit"
target = "worker"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-721",
			Ticket:    "SQU-721",
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
			ID:        "ops-721",
			Ticket:    "OPS-721",
			Target:    "worker",
			Pipeline:  "ops_review",
			Status:    job.StatusFailed,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "audit", Target: "worker", Status: job.StatusFailed},
			},
		},
		{
			ID:        "ops-722",
			Ticket:    "OPS-722",
			Target:    "worker",
			Pipeline:  "ops_review",
			Status:    job.StatusFailed,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "audit", Target: "worker", Status: job.StatusFailed},
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
	cmd.SetArgs([]string{"pipeline", "next", "--repo", root, "--sort", "failed", "--limit", "1", "--format", "{{.Pipeline}}|{{.Reason}}|{{.Action}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline next sort: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := strings.TrimSpace(out.String()), "ops_review|failed_steps=2|agent-team pipeline retry ops_review --dry-run --dispatch --preview-routes"; got != want {
		t.Fatalf("pipeline next sort = %q, want %q", got, want)
	}
}

func TestPipelineStatusNextReportsOutboxReasons(t *testing.T) {
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

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 27, 17, 0, 0, 0, time.UTC)
	for _, j := range []*job.Job{
		{
			ID:        "squ-920",
			Ticket:    "SQU-920",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning},
			},
		},
		{
			ID:        "ops-920",
			Ticket:    "OPS-920",
			Target:    "worker",
			Pipeline:  "ops_review",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning},
			},
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
			Payload:   map[string]any{"job_id": "squ-920", "target": "worker"},
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "outbox-ticket-failed",
			State:     daemon.OutboxStateFailed,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "squ-920", "target": "worker"},
			LastError: "route missing",
			CreatedAt: now.Add(time.Minute),
			UpdatedAt: now.Add(time.Minute),
		},
		{
			ID:        "outbox-ticket-processed",
			State:     daemon.OutboxStateProcessed,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "squ-920", "target": "worker"},
			CreatedAt: now.Add(2 * time.Minute),
			UpdatedAt: now.Add(2 * time.Minute),
		},
		{
			ID:        "outbox-ops-pending",
			State:     daemon.OutboxStatePending,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"job_id": "ops-920", "target": "worker"},
			CreatedAt: now.Add(3 * time.Minute),
			UpdatedAt: now.Add(3 * time.Minute),
		},
	} {
		if err := daemon.WriteOutboxItem(teamDir, item); err != nil {
			t.Fatalf("write outbox item %s: %v", item.ID, err)
		}
	}
	writeQuarantinedOutboxFile(t, teamDir, "20260627T170000.000000000Z", daemon.OutboxStatePending, &daemon.OutboxItem{
		ID:        "outbox-ticket-quarantined-a",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Source:    "manager",
		Payload:   map[string]any{"job_id": "squ-920", "target": "worker"},
		CreatedAt: now.Add(4 * time.Minute),
		UpdatedAt: now.Add(4 * time.Minute),
	})
	writeQuarantinedOutboxFile(t, teamDir, "20260627T170000.000000000Z", daemon.OutboxStateFailed, &daemon.OutboxItem{
		ID:        "outbox-ticket-quarantined-b",
		State:     daemon.OutboxStateFailed,
		Type:      "agent.dispatch",
		Source:    "manager",
		Payload:   map[string]any{"job_id": "squ-920", "target": "worker"},
		CreatedAt: now.Add(5 * time.Minute),
		UpdatedAt: now.Add(5 * time.Minute),
		FailedAt:  now.Add(5 * time.Minute),
	})
	writeQuarantinedOutboxFile(t, teamDir, "20260627T170000.000000000Z", daemon.OutboxStatePending, &daemon.OutboxItem{
		ID:        "outbox-ops-quarantined",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Source:    "manager",
		Payload:   map[string]any{"job_id": "ops-920", "target": "worker"},
		CreatedAt: now.Add(6 * time.Minute),
		UpdatedAt: now.Add(6 * time.Minute),
	})

	status := NewRootCmd()
	statusOut, statusErr := &bytes.Buffer{}, &bytes.Buffer{}
	status.SetOut(statusOut)
	status.SetErr(statusErr)
	status.SetArgs([]string{"pipeline", "status", "--repo", root, "--json"})
	if err := status.Execute(); err != nil {
		t.Fatalf("pipeline status outbox json: %v\nstderr=%s", err, statusErr.String())
	}
	var rows []pipelineStatusRow
	if err := json.Unmarshal(statusOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline status outbox: %v\nbody=%s", err, statusOut.String())
	}
	byName := map[string]pipelineStatusRow{}
	for _, row := range rows {
		byName[row.Pipeline] = row
	}
	ticket := byName["ticket_to_pr"]
	if ticket.OutboxPending != 1 || ticket.OutboxFailed != 1 || ticket.OutboxProcessed != 1 || ticket.OutboxQuarantined != 2 || ticket.OutboxRestorable != 2 || ticket.OutboxUnrestorable != 0 {
		t.Fatalf("ticket outbox status = %+v", ticket)
	}
	for _, want := range []string{
		"agent-team pipeline outbox quarantine ticket_to_pr",
		"agent-team pipeline outbox quarantine ticket_to_pr --restorable",
		"agent-team pipeline snapshot ticket_to_pr --json",
		"agent-team pipeline outbox ticket_to_pr --state failed",
		"agent-team pipeline outbox ticket_to_pr --state pending",
	} {
		if !containsString(ticket.Actions, want) {
			t.Fatalf("ticket outbox actions missing %q: %+v", want, ticket.Actions)
		}
	}
	ops := byName["ops_review"]
	if ops.OutboxPending != 1 || ops.OutboxFailed != 0 || ops.OutboxProcessed != 0 || ops.OutboxQuarantined != 1 || ops.OutboxRestorable != 1 || ops.OutboxUnrestorable != 0 {
		t.Fatalf("ops outbox status = %+v", ops)
	}

	next := NewRootCmd()
	nextOut, nextErr := &bytes.Buffer{}, &bytes.Buffer{}
	next.SetOut(nextOut)
	next.SetErr(nextErr)
	next.SetArgs([]string{"pipeline", "next", "ticket_to_pr", "--repo", root, "--reason", "outbox_quarantined,outbox_failed,outbox_pending", "--format", "{{.Reason}}|{{.Action}}"})
	if err := next.Execute(); err != nil {
		t.Fatalf("pipeline next outbox reasons: %v\nstderr=%s", err, nextErr.String())
	}
	for _, want := range []string{
		"outbox_quarantined=2|agent-team pipeline outbox quarantine ticket_to_pr",
		"outbox_failed=1|agent-team pipeline outbox ticket_to_pr --state failed",
		"outbox_pending=1|agent-team pipeline outbox ticket_to_pr --state pending",
	} {
		if !strings.Contains(nextOut.String(), want) {
			t.Fatalf("pipeline next outbox reason missing %q:\n%s", want, nextOut.String())
		}
	}

	nextAlias := NewRootCmd()
	nextAliasOut, nextAliasErr := &bytes.Buffer{}, &bytes.Buffer{}
	nextAlias.SetOut(nextAliasOut)
	nextAlias.SetErr(nextAliasErr)
	nextAlias.SetArgs([]string{"pipeline", "next", "ticket_to_pr", "--repo", root, "--reason", "quarantined", "--format", "{{.Reason}}|{{.Action}}"})
	if err := nextAlias.Execute(); err != nil {
		t.Fatalf("pipeline next quarantined alias: %v\nstderr=%s", err, nextAliasErr.String())
	}
	if want := "outbox_quarantined=2|agent-team pipeline outbox quarantine ticket_to_pr"; !strings.Contains(nextAliasOut.String(), want) {
		t.Fatalf("pipeline next quarantined alias missing %q:\n%s", want, nextAliasOut.String())
	}
	if strings.Contains(nextAliasOut.String(), "outbox_failed=") || strings.Contains(nextAliasOut.String(), "outbox_pending=") {
		t.Fatalf("pipeline next quarantined alias included active outbox reasons:\n%s", nextAliasOut.String())
	}

	teamNext := NewRootCmd()
	teamNextOut, teamNextErr := &bytes.Buffer{}, &bytes.Buffer{}
	teamNext.SetOut(teamNextOut)
	teamNext.SetErr(teamNextErr)
	teamNext.SetArgs([]string{"pipeline", "next", "ticket_to_pr", "--team", "delivery", "--repo", root, "--reason", "outbox_quarantined,outbox_failed,outbox_pending", "--format", "{{.Reason}}|{{.Action}}"})
	if err := teamNext.Execute(); err != nil {
		t.Fatalf("pipeline next team outbox reasons: %v\nstderr=%s", err, teamNextErr.String())
	}
	for _, want := range []string{
		"outbox_quarantined=2|agent-team team outbox quarantine delivery",
		"outbox_failed=1|agent-team team outbox delivery --state failed",
		"outbox_pending=1|agent-team team outbox delivery --state pending",
	} {
		if !strings.Contains(teamNextOut.String(), want) {
			t.Fatalf("pipeline next team outbox reason missing %q:\n%s", want, teamNextOut.String())
		}
	}

	sorted := NewRootCmd()
	sortedOut, sortedErr := &bytes.Buffer{}, &bytes.Buffer{}
	sorted.SetOut(sortedOut)
	sorted.SetErr(sortedErr)
	sorted.SetArgs([]string{"pipeline", "status", "--repo", root, "--sort", "outbox", "--limit", "1", "--format", "{{.Pipeline}} {{.OutboxPending}} {{.OutboxFailed}} {{.OutboxProcessed}} {{.OutboxQuarantined}}"})
	if err := sorted.Execute(); err != nil {
		t.Fatalf("pipeline status sort outbox: %v\nstderr=%s", err, sortedErr.String())
	}
	if got, want := strings.TrimSpace(sortedOut.String()), "ticket_to_pr 1 1 1 2"; got != want {
		t.Fatalf("pipeline status outbox sort = %q, want %q", got, want)
	}

	sortedQuarantine := NewRootCmd()
	sortedQuarantineOut, sortedQuarantineErr := &bytes.Buffer{}, &bytes.Buffer{}
	sortedQuarantine.SetOut(sortedQuarantineOut)
	sortedQuarantine.SetErr(sortedQuarantineErr)
	sortedQuarantine.SetArgs([]string{"pipeline", "status", "--repo", root, "--sort", "outbox-quarantined", "--limit", "1", "--format", "{{.Pipeline}} {{.OutboxQuarantined}} {{.OutboxRestorable}} {{.OutboxUnrestorable}}"})
	if err := sortedQuarantine.Execute(); err != nil {
		t.Fatalf("pipeline status sort outbox quarantine: %v\nstderr=%s", err, sortedQuarantineErr.String())
	}
	if got, want := strings.TrimSpace(sortedQuarantineOut.String()), "ticket_to_pr 2 2 0"; got != want {
		t.Fatalf("pipeline status outbox quarantine sort = %q, want %q", got, want)
	}

	sortedAnyQuarantine := NewRootCmd()
	sortedAnyQuarantineOut, sortedAnyQuarantineErr := &bytes.Buffer{}, &bytes.Buffer{}
	sortedAnyQuarantine.SetOut(sortedAnyQuarantineOut)
	sortedAnyQuarantine.SetErr(sortedAnyQuarantineErr)
	sortedAnyQuarantine.SetArgs([]string{"pipeline", "status", "--repo", root, "--sort", "quarantined", "--limit", "1", "--format", "{{.Pipeline}} {{.QueueQuarantined}} {{.OutboxQuarantined}}"})
	if err := sortedAnyQuarantine.Execute(); err != nil {
		t.Fatalf("pipeline status sort quarantined alias: %v\nstderr=%s", err, sortedAnyQuarantineErr.String())
	}
	if got, want := strings.TrimSpace(sortedAnyQuarantineOut.String()), "ticket_to_pr 0 2"; got != want {
		t.Fatalf("pipeline status quarantined alias sort = %q, want %q", got, want)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"pipeline", "status", "ticket_to_pr", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("pipeline status outbox text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"OUTBOX", "pending=1,failed=1,processed=1,quarantined=2(restorable=2,unrestorable=0)"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("pipeline status outbox text missing %q:\n%s", want, textOut.String())
		}
	}
}

func TestPipelineStatusSurfacesQueueState(t *testing.T) {
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
id = "audit"
target = "worker"

[teams.delivery]
instances = ["worker"]
pipelines = ["ticket_to_pr"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, j := range []*job.Job{
		{
			ID:        "squ-620",
			Ticket:    "SQU-620",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Instance:  "worker-squ-620",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "ops-620",
			Ticket:    "OPS-620",
			Target:    "worker",
			Pipeline:  "ops_review",
			Instance:  "worker-ops-620",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}
	queueRoot := daemon.DaemonRoot(teamDir)
	for _, item := range []*daemon.QueueItem{
		{
			ID:             "q-ticket-dead",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     "worker-squ-620",
			Payload:        map[string]any{"job_id": "squ-620", "ticket": "SQU-620"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-2 * time.Hour),
			DeadLetteredAt: now.Add(-2 * time.Hour),
		},
		{
			ID:         "q-ticket-pending",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-620",
			Payload:    map[string]any{"job_id": "squ-620", "ticket": "SQU-620"},
			QueuedAt:   now.Add(-time.Hour),
			UpdatedAt:  now.Add(-time.Hour),
		},
		{
			ID:             "q-ops-dead",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     "worker-ops-620",
			Payload:        map[string]any{"job_id": "ops-620", "ticket": "OPS-620"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "foreign",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-2 * time.Hour),
			DeadLetteredAt: now.Add(-2 * time.Hour),
		},
	} {
		if err := daemon.WriteQueueItem(queueRoot, item); err != nil {
			t.Fatalf("write queue item %s: %v", item.ID, err)
		}
	}
	stamp := "20260619T030000.000000000Z"
	writeQuarantinedQueueItem(t, teamDir, stamp, daemon.QueueStatePending, &daemon.QueueItem{
		ID:         "q-ticket-quarantine-restorable",
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-620",
		Payload:    map[string]any{"job_id": "squ-620", "ticket": "SQU-620"},
		QueuedAt:   now.Add(-2 * time.Hour),
		UpdatedAt:  now.Add(-time.Hour),
	})
	writeQuarantinedQueueItem(t, teamDir, stamp, daemon.QueueStateDead, &daemon.QueueItem{
		ID:        "q-ticket-quarantine-unrestorable",
		EventType: "agent.dispatch",
		Instance:  "worker",
		Payload:   map[string]any{"job_id": "squ-620", "ticket": "SQU-620"},
		QueuedAt:  now.Add(-3 * time.Hour),
		UpdatedAt: now.Add(-2 * time.Hour),
	})
	writeQuarantinedQueueItem(t, teamDir, stamp, daemon.QueueStateDead, &daemon.QueueItem{
		ID:             "q-ops-quarantine",
		EventType:      "agent.dispatch",
		Instance:       "worker",
		InstanceID:     "worker-ops-620",
		Payload:        map[string]any{"job_id": "ops-620", "ticket": "OPS-620"},
		Attempts:       daemon.MaxQueueAttempts,
		LastError:      "foreign",
		QueuedAt:       now.Add(-4 * time.Hour),
		UpdatedAt:      now.Add(-3 * time.Hour),
		DeadLetteredAt: now.Add(-3 * time.Hour),
	})

	status := NewRootCmd()
	statusOut, statusErr := &bytes.Buffer{}, &bytes.Buffer{}
	status.SetOut(statusOut)
	status.SetErr(statusErr)
	status.SetArgs([]string{"pipeline", "status", "--repo", root, "--json"})
	if err := status.Execute(); err != nil {
		t.Fatalf("pipeline status json: %v\nstderr=%s", err, statusErr.String())
	}
	var rows []pipelineStatusRow
	if err := json.Unmarshal(statusOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline status json: %v\nbody=%s", err, statusOut.String())
	}
	byName := map[string]pipelineStatusRow{}
	for _, row := range rows {
		byName[row.Pipeline] = row
	}
	ticket := byName["ticket_to_pr"]
	if ticket.QueuePending != 1 || ticket.QueueDead != 1 || ticket.QueueQuarantined != 2 || ticket.QueueRestorable != 1 || ticket.QueueUnrestorable != 1 {
		t.Fatalf("ticket queue status = %+v", ticket)
	}
	for _, want := range []string{
		"agent-team pipeline queue ticket_to_pr --state dead --summary",
		"agent-team pipeline queue retry ticket_to_pr --all --sort attempts --limit 10 --dry-run",
		"agent-team pipeline queue quarantine ticket_to_pr",
		"agent-team pipeline queue quarantine ticket_to_pr --unrestorable",
		"agent-team pipeline queue quarantine ticket_to_pr --restorable",
		"agent-team pipeline snapshot ticket_to_pr --json",
		"agent-team pipeline queue ticket_to_pr --state pending",
	} {
		if !containsString(ticket.Actions, want) {
			t.Fatalf("ticket queue actions missing %q: %+v", want, ticket.Actions)
		}
	}
	ops := byName["ops_review"]
	if ops.QueuePending != 0 || ops.QueueDead != 1 || ops.QueueQuarantined != 1 || ops.QueueRestorable != 1 || ops.QueueUnrestorable != 0 {
		t.Fatalf("ops queue status = %+v", ops)
	}

	sorted := NewRootCmd()
	sortedOut, sortedErr := &bytes.Buffer{}, &bytes.Buffer{}
	sorted.SetOut(sortedOut)
	sorted.SetErr(sortedErr)
	sorted.SetArgs([]string{"pipeline", "status", "--repo", root, "--sort", "queue", "--limit", "1", "--format", "{{.Pipeline}} {{.QueuePending}} {{.QueueDead}} {{.QueueQuarantined}}"})
	if err := sorted.Execute(); err != nil {
		t.Fatalf("pipeline status sort queue: %v\nstderr=%s", err, sortedErr.String())
	}
	if got, want := strings.TrimSpace(sortedOut.String()), "ticket_to_pr 1 1 2"; got != want {
		t.Fatalf("pipeline status queue sort = %q, want %q", got, want)
	}

	sortedQueueQuarantine := NewRootCmd()
	sortedQueueQuarantineOut, sortedQueueQuarantineErr := &bytes.Buffer{}, &bytes.Buffer{}
	sortedQueueQuarantine.SetOut(sortedQueueQuarantineOut)
	sortedQueueQuarantine.SetErr(sortedQueueQuarantineErr)
	sortedQueueQuarantine.SetArgs([]string{"pipeline", "status", "--repo", root, "--sort", "queue-quarantined", "--limit", "1", "--format", "{{.Pipeline}} {{.QueueQuarantined}} {{.OutboxQuarantined}}"})
	if err := sortedQueueQuarantine.Execute(); err != nil {
		t.Fatalf("pipeline status sort queue quarantine: %v\nstderr=%s", err, sortedQueueQuarantineErr.String())
	}
	if got, want := strings.TrimSpace(sortedQueueQuarantineOut.String()), "ticket_to_pr 2 0"; got != want {
		t.Fatalf("pipeline status queue quarantine sort = %q, want %q", got, want)
	}

	next := NewRootCmd()
	nextOut, nextErr := &bytes.Buffer{}, &bytes.Buffer{}
	next.SetOut(nextOut)
	next.SetErr(nextErr)
	next.SetArgs([]string{"pipeline", "next", "ticket_to_pr", "--repo", root, "--format", "{{.Reason}}|{{.Action}}"})
	if err := next.Execute(); err != nil {
		t.Fatalf("pipeline next queue reasons: %v\nstderr=%s", err, nextErr.String())
	}
	for _, want := range []string{
		"queue_dead=1|agent-team pipeline queue ticket_to_pr --state dead --summary",
		"queue_dead=1|agent-team pipeline queue retry ticket_to_pr --all --sort attempts --limit 10 --dry-run",
		"queue_quarantined=2|agent-team pipeline queue quarantine ticket_to_pr",
		"queue_pending=1|agent-team pipeline queue ticket_to_pr --state pending",
	} {
		if !strings.Contains(nextOut.String(), want) {
			t.Fatalf("pipeline next queue reason missing %q:\n%s", want, nextOut.String())
		}
	}

	nextAlias := NewRootCmd()
	nextAliasOut, nextAliasErr := &bytes.Buffer{}, &bytes.Buffer{}
	nextAlias.SetOut(nextAliasOut)
	nextAlias.SetErr(nextAliasErr)
	nextAlias.SetArgs([]string{"pipeline", "next", "ticket_to_pr", "--repo", root, "--reason", "quarantined", "--format", "{{.Reason}}|{{.Action}}"})
	if err := nextAlias.Execute(); err != nil {
		t.Fatalf("pipeline next queue quarantined alias: %v\nstderr=%s", err, nextAliasErr.String())
	}
	if want := "queue_quarantined=2|agent-team pipeline queue quarantine ticket_to_pr"; !strings.Contains(nextAliasOut.String(), want) {
		t.Fatalf("pipeline next queue quarantined alias missing %q:\n%s", want, nextAliasOut.String())
	}
	if strings.Contains(nextAliasOut.String(), "queue_dead=") || strings.Contains(nextAliasOut.String(), "queue_pending=") {
		t.Fatalf("pipeline next queue quarantined alias included non-quarantine reasons:\n%s", nextAliasOut.String())
	}

	teamNext := NewRootCmd()
	teamNextOut, teamNextErr := &bytes.Buffer{}, &bytes.Buffer{}
	teamNext.SetOut(teamNextOut)
	teamNext.SetErr(teamNextErr)
	teamNext.SetArgs([]string{"pipeline", "next", "ticket_to_pr", "--team", "delivery", "--repo", root, "--format", "{{.Reason}}|{{.Action}}"})
	if err := teamNext.Execute(); err != nil {
		t.Fatalf("pipeline next team queue reasons: %v\nstderr=%s", err, teamNextErr.String())
	}
	for _, want := range []string{
		"queue_dead=1|agent-team team queue delivery --state dead --summary",
		"queue_dead=1|agent-team team queue retry delivery --all --sort attempts --limit 10 --dry-run",
		"queue_quarantined=2|agent-team team queue quarantine delivery",
		"queue_pending=1|agent-team team queue delivery --state pending",
	} {
		if !strings.Contains(teamNextOut.String(), want) {
			t.Fatalf("pipeline next team queue reason missing %q:\n%s", want, teamNextOut.String())
		}
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"pipeline", "status", "ticket_to_pr", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("pipeline status text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"QUEUE", "pending=1,dead=1,quarantined=2(restorable=1,unrestorable=1)", "agent-team pipeline queue quarantine ticket_to_pr --restorable"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("pipeline status text missing %q:\n%s", want, textOut.String())
		}
	}
}

func TestPipelineTriageScopesJobsQueueAndActions(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "triage"
target = "ticket-manager"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
after = ["triage"]

[pipelines.ops_review]
trigger.event = "ops.created"

[[pipelines.ops_review.steps]]
id = "audit"
target = "worker"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-48 * time.Hour)
	for _, j := range []*job.Job{
		{
			ID:        "squ-830",
			Ticket:    "SQU-830",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusFailed,
			CreatedAt: old,
			UpdatedAt: old,
			Steps: []job.Step{
				{ID: "triage", Target: "ticket-manager", Status: job.StatusDone, StartedAt: old, FinishedAt: old.Add(time.Hour)},
				{ID: "implement", Target: "worker", Status: job.StatusFailed, After: []string{"triage"}, StartedAt: old.Add(time.Hour), FinishedAt: old.Add(2 * time.Hour)},
			},
		},
		{
			ID:        "squ-831",
			Ticket:    "SQU-831",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: old,
			UpdatedAt: old,
			Steps: []job.Step{
				{ID: "triage", Target: "ticket-manager", Status: job.StatusDone, StartedAt: old, FinishedAt: old.Add(time.Hour)},
				{ID: "implement", Target: "worker", Status: job.StatusRunning, After: []string{"triage"}, StartedAt: old.Add(time.Hour)},
			},
		},
		{
			ID:        "squ-832",
			Ticket:    "SQU-832",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusQueued},
			},
		},
		{
			ID:        "squ-833",
			Ticket:    "SQU-833",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusQueued},
			},
		},
		{
			ID:        "squ-834",
			Ticket:    "SQU-834",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "triage", Target: "ticket-manager", Status: job.StatusDone},
				{ID: "implement", Target: "worker", Status: job.StatusQueued, After: []string{"triage"}},
			},
		},
		{
			ID:        "ops-830",
			Ticket:    "OPS-830",
			Target:    "worker",
			Pipeline:  "ops_review",
			Status:    job.StatusFailed,
			CreatedAt: old,
			UpdatedAt: old,
			Steps: []job.Step{
				{ID: "audit", Target: "worker", Status: job.StatusFailed},
			},
		},
		{
			ID:        "adhoc-830",
			Ticket:    "ADHOC-830",
			Target:    "worker",
			Status:    job.StatusFailed,
			CreatedAt: old,
			UpdatedAt: old,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), &daemon.QueueItem{
		ID:             "q-pipeline-triage-dead",
		State:          daemon.QueueStateDead,
		EventType:      "agent.dispatch",
		Instance:       "worker",
		InstanceID:     "worker-squ-832",
		Payload:        map[string]any{"job_id": "squ-832", "ticket": "SQU-832", "target": "worker"},
		Attempts:       daemon.MaxQueueAttempts,
		LastError:      "spawn failed",
		QueuedAt:       old,
		UpdatedAt:      now,
		DeadLetteredAt: now,
	}); err != nil {
		t.Fatalf("write dead queue item: %v", err)
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), &daemon.QueueItem{
		ID:             "q-ops-triage-dead",
		State:          daemon.QueueStateDead,
		EventType:      "agent.dispatch",
		Instance:       "worker",
		InstanceID:     "worker-ops-830",
		Payload:        map[string]any{"job_id": "ops-830", "ticket": "OPS-830", "target": "worker"},
		Attempts:       daemon.MaxQueueAttempts,
		LastError:      "foreign",
		QueuedAt:       old,
		UpdatedAt:      now,
		DeadLetteredAt: now,
	}); err != nil {
		t.Fatalf("write foreign queue item: %v", err)
	}
	quarantineStamp := "20260619T040000.000000000Z"
	quarantinePath := filepath.Join("quarantine", quarantineStamp, daemon.QueueStatePending, "q-pipeline-triage-quarantined.json")
	writeQuarantinedQueueItem(t, teamDir, quarantineStamp, daemon.QueueStatePending, &daemon.QueueItem{
		ID:         "q-pipeline-triage-quarantined",
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-833",
		Payload:    map[string]any{"job_id": "squ-833", "ticket": "SQU-833", "target": "worker"},
		QueuedAt:   old,
		UpdatedAt:  now,
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "triage", "ticket_to_pr", "--repo", root, "--stale-after", "24h", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline triage: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot jobTriageSnapshot
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode pipeline triage json: %v\nbody=%s", err, out.String())
	}
	if snapshot.Summary.Total != 5 || snapshot.Queue.Dead != 1 || snapshot.Queue.Quarantined != 1 || snapshot.Queue.QuarantineRestorable != 1 {
		t.Fatalf("pipeline triage summary = %+v queue=%+v", snapshot.Summary, snapshot.Queue)
	}
	if len(snapshot.Attention) != 4 {
		t.Fatalf("attention = %+v", snapshot.Attention)
	}
	attention := map[string]jobTriageItem{}
	for _, item := range snapshot.Attention {
		attention[item.JobID] = item
		if strings.HasPrefix(item.JobID, "ops-") {
			t.Fatalf("pipeline triage leaked foreign job: %+v", item)
		}
	}
	if !containsString(attention["squ-830"].Actions, "agent-team pipeline retry ticket_to_pr --step implement --dry-run --dispatch --preview-routes") ||
		containsString(attention["squ-830"].Actions, "agent-team job retry squ-830 --dispatch") {
		t.Fatalf("failed job actions = %+v", attention["squ-830"].Actions)
	}
	if !containsString(attention["squ-831"].Actions, "agent-team pipeline timeout ticket_to_pr --step implement --target-agent worker --dry-run") ||
		!containsString(attention["squ-831"].Actions, "agent-team pipeline adopt ticket_to_pr squ-831 --step implement --pid <pid> --dry-run") {
		t.Fatalf("stale running actions = %+v", attention["squ-831"].Actions)
	}
	if containsString(attention["squ-831"].Actions, "agent-team job timeout squ-831 --dry-run") ||
		containsString(attention["squ-831"].Actions, "agent-team job adopt squ-831 --step implement --pid <pid> --dry-run") {
		t.Fatalf("stale running actions should be pipeline-scoped: %+v", attention["squ-831"].Actions)
	}
	if !containsString(attention["squ-832"].Actions, "agent-team pipeline queue retry ticket_to_pr q-pipeline-triage-dead") ||
		containsString(attention["squ-832"].Actions, "agent-team job queue retry squ-832 q-pipeline-triage-dead") {
		t.Fatalf("dead queue actions = %+v", attention["squ-832"].Actions)
	}
	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"pipeline", "triage", "ticket_to_pr", "--repo", root, "--stale-after", "24h", "--reason", "queue_dead", "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("pipeline triage commands: %v\nstderr=%s", err, commandsErr.String())
	}
	if !strings.Contains(commandsOut.String(), "agent-team pipeline queue retry ticket_to_pr q-pipeline-triage-dead") ||
		strings.Contains(commandsOut.String(), "agent-team job queue retry squ-832 q-pipeline-triage-dead") ||
		strings.Contains(commandsOut.String(), "Attention:") {
		t.Fatalf("pipeline triage commands = %q", commandsOut.String())
	}
	if !containsString(attention["squ-833"].Actions, "agent-team pipeline queue quarantine ticket_to_pr --job squ-833") ||
		!containsString(attention["squ-833"].Actions, fmt.Sprintf("agent-team pipeline queue quarantine restore ticket_to_pr %s --dry-run", quarantinePath)) {
		t.Fatalf("quarantine actions = %+v", attention["squ-833"].Actions)
	}
	if len(snapshot.ReadySteps) != 3 {
		t.Fatalf("ready steps = %+v", snapshot.ReadySteps)
	}
	ready := map[string]jobReadyRow{}
	for _, row := range snapshot.ReadySteps {
		ready[row.JobID] = row
	}
	if !containsString(ready["squ-832"].Actions, "agent-team pipeline tick ticket_to_pr --dry-run --preview-routes") ||
		!containsString(ready["squ-834"].Actions, "agent-team pipeline tick ticket_to_pr --dry-run --preview-routes") {
		t.Fatalf("ready actions = %+v", snapshot.ReadySteps)
	}

	allCmd := NewRootCmd()
	allOut, allErr := &bytes.Buffer{}, &bytes.Buffer{}
	allCmd.SetOut(allOut)
	allCmd.SetErr(allErr)
	allCmd.SetArgs([]string{"pipeline", "triage", "--repo", root, "--stale-after", "24h", "--json"})
	if err := allCmd.Execute(); err != nil {
		t.Fatalf("pipeline triage all default: %v\nstderr=%s", err, allErr.String())
	}
	var allSnapshot jobTriageSnapshot
	if err := json.Unmarshal(allOut.Bytes(), &allSnapshot); err != nil {
		t.Fatalf("decode pipeline triage all json: %v\nbody=%s", err, allOut.String())
	}
	if allSnapshot.Summary.Total != 6 || allSnapshot.Queue.Dead != 2 || allSnapshot.Queue.Quarantined != 1 {
		t.Fatalf("all pipeline triage summary = %+v queue=%+v", allSnapshot.Summary, allSnapshot.Queue)
	}
	allAttention := map[string]jobTriageItem{}
	for _, item := range allSnapshot.Attention {
		allAttention[item.JobID] = item
	}
	if _, ok := allAttention["adhoc-830"]; ok {
		t.Fatalf("all pipeline triage included non-pipeline job: %+v", allSnapshot.Attention)
	}
	if !containsString(allAttention["squ-830"].Actions, "agent-team pipeline retry ticket_to_pr --step implement --dry-run --dispatch --preview-routes") {
		t.Fatalf("all pipeline triage ticket actions = %+v", allAttention["squ-830"].Actions)
	}
	if !containsString(allAttention["ops-830"].Actions, "agent-team pipeline retry ops_review --step audit --dry-run --dispatch --preview-routes") {
		t.Fatalf("all pipeline triage ops actions = %+v", allAttention["ops-830"].Actions)
	}

	explicitAllCmd := NewRootCmd()
	explicitAllOut, explicitAllErr := &bytes.Buffer{}, &bytes.Buffer{}
	explicitAllCmd.SetOut(explicitAllOut)
	explicitAllCmd.SetErr(explicitAllErr)
	explicitAllCmd.SetArgs([]string{"pipeline", "triage", "--all", "--repo", root, "--stale-after", "24h", "--json"})
	if err := explicitAllCmd.Execute(); err != nil {
		t.Fatalf("pipeline triage --all: %v\nstderr=%s", err, explicitAllErr.String())
	}
	var explicitAllSnapshot jobTriageSnapshot
	if err := json.Unmarshal(explicitAllOut.Bytes(), &explicitAllSnapshot); err != nil {
		t.Fatalf("decode pipeline triage --all json: %v\nbody=%s", err, explicitAllOut.String())
	}
	if explicitAllSnapshot.Summary.Total != allSnapshot.Summary.Total ||
		len(explicitAllSnapshot.Attention) != len(allSnapshot.Attention) {
		t.Fatalf("pipeline triage --all = %+v, want equivalent to default all %+v", explicitAllSnapshot, allSnapshot)
	}

	invalidMany := NewRootCmd()
	invalidManyOut, invalidManyErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidMany.SetOut(invalidManyOut)
	invalidMany.SetErr(invalidManyErr)
	invalidMany.SetArgs([]string{"pipeline", "triage", "ticket_to_pr", "ops_review", "--repo", root})
	if err := invalidMany.Execute(); err == nil {
		t.Fatalf("pipeline triage accepted multiple pipeline names: stdout=%s", invalidManyOut.String())
	}
	if !strings.Contains(invalidManyErr.String(), "pass at most one pipeline name") {
		t.Fatalf("multiple pipeline error = %q", invalidManyErr.String())
	}

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "json",
			args: []string{"pipeline", "triage", "ticket_to_pr", "--repo", root, "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "format",
			args: []string{"pipeline", "triage", "ticket_to_pr", "--repo", root, "--commands", "--format", "{{.Summary.Total}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "watch",
			args: []string{"pipeline", "triage", "ticket_to_pr", "--repo", root, "--commands", "--watch"},
			want: "--commands cannot be combined with --watch",
		},
	} {
		t.Run("triage-commands-conflict-"+tc.name, func(t *testing.T) {
			conflict := NewRootCmd()
			conflictOut, conflictErr := &bytes.Buffer{}, &bytes.Buffer{}
			conflict.SetOut(conflictOut)
			conflict.SetErr(conflictErr)
			conflict.SetArgs(tc.args)
			if err := conflict.Execute(); err == nil {
				t.Fatalf("pipeline triage accepted %s conflict: stdout=%s", tc.name, conflictOut.String())
			}
			if !strings.Contains(conflictErr.String(), tc.want) {
				t.Fatalf("pipeline triage %s conflict stderr = %q, want %q", tc.name, conflictErr.String(), tc.want)
			}
		})
	}
}

func TestPipelineReadyRowActionsPreservesNonAdvanceableQueuedHint(t *testing.T) {
	blocked := jobReadyRow{
		JobID:      "squ-835",
		State:      "queued",
		WaitingFor: []string{"triage"},
		Actions:    []string{"agent-team tick"},
	}
	blockedActions := pipelineReadyRowActions("ticket_to_pr", blocked)
	if !containsString(blockedActions, "agent-team pipeline tick ticket_to_pr --dry-run --preview-routes") ||
		containsString(blockedActions, "agent-team pipeline advance ticket_to_pr --dry-run --preview-routes") {
		t.Fatalf("blocked queued actions = %+v", blockedActions)
	}

	advanceable := jobReadyRow{
		JobID:   "squ-836",
		State:   "queued",
		Actions: []string{"agent-team job advance squ-836"},
	}
	advanceActions := pipelineReadyRowActions("ticket_to_pr", advanceable)
	if !containsString(advanceActions, "agent-team pipeline tick ticket_to_pr --dry-run --preview-routes") ||
		containsString(advanceActions, "agent-team job advance squ-836") {
		t.Fatalf("advanceable queued actions = %+v", advanceActions)
	}

	gated := jobReadyRow{
		JobID:  "squ-837",
		State:  "blocked",
		StepID: "review",
		Gate:   job.StepGateManual,
		Actions: []string{
			"agent-team job approve squ-837 --step review",
			"agent-team job reject squ-837 --step review",
		},
	}
	gatedActions := pipelineReadyRowActions("ticket_to_pr", gated)
	if !containsString(gatedActions, "agent-team pipeline approve ticket_to_pr --step review --dry-run --dispatch --preview-routes") ||
		!containsString(gatedActions, "agent-team pipeline reject ticket_to_pr --step review --dry-run") ||
		containsString(gatedActions, "agent-team job approve squ-837 --step review") ||
		containsString(gatedActions, "agent-team job reject squ-837 --step review") {
		t.Fatalf("gated actions = %+v", gatedActions)
	}

	failed := jobReadyRow{
		JobID:   "squ-838",
		State:   "failed",
		StepID:  "implement",
		Actions: []string{"agent-team job retry squ-838 --dispatch"},
	}
	failedActions := pipelineReadyRowActions("ticket_to_pr", failed)
	if !containsString(failedActions, "agent-team pipeline retry ticket_to_pr --step implement --dry-run --dispatch --preview-routes") ||
		containsString(failedActions, "agent-team job retry squ-838 --dispatch") {
		t.Fatalf("failed actions = %+v", failedActions)
	}

	held := jobReadyRow{
		JobID:   "squ-839",
		State:   "held",
		Actions: []string{"agent-team job release squ-839"},
	}
	heldActions := pipelineReadyRowActions("ticket_to_pr", held)
	if !containsString(heldActions, "agent-team pipeline release ticket_to_pr --dry-run") ||
		containsString(heldActions, "agent-team job release squ-839") {
		t.Fatalf("held actions = %+v", heldActions)
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
			UpdatedAt: now.Add(time.Minute),
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
		{
			ID:        "squ-313",
			Ticket:    "SQU-313",
			Target:    "manager",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now.Add(2 * time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusDone},
				{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}},
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
	if !containsString(rows[0].Actions, "agent-team pipeline tick ticket_to_pr --dry-run --preview-routes") ||
		containsString(rows[0].Actions, "agent-team job advance squ-310") {
		t.Fatalf("ready actions = %+v", rows[0].Actions)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"pipeline", "ready", "ticket_to_pr", "--repo", root, "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("pipeline ready commands: %v\nstderr=%s", err, commandsErr.String())
	}
	if got := strings.TrimSpace(commandsOut.String()); got != "agent-team pipeline tick ticket_to_pr --dry-run --preview-routes" {
		t.Fatalf("pipeline ready commands = %q", commandsOut.String())
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

	sorted := NewRootCmd()
	sortedOut, sortedErr := &bytes.Buffer{}, &bytes.Buffer{}
	sorted.SetOut(sortedOut)
	sorted.SetErr(sortedErr)
	sorted.SetArgs([]string{"pipeline", "ready", "ticket_to_pr", "--repo", root, "--state", "all", "--sort", "updated", "--format", "{{.JobID}}"})
	if err := sorted.Execute(); err != nil {
		t.Fatalf("pipeline ready sort updated: %v\nstderr=%s", err, sortedErr.String())
	}
	if got := strings.Split(strings.TrimSpace(sortedOut.String()), "\n"); strings.Join(got, ",") != "squ-311,squ-310" {
		t.Fatalf("pipeline ready sorted output = %q", sortedOut.String())
	}

	limited := NewRootCmd()
	limitedOut, limitedErr := &bytes.Buffer{}, &bytes.Buffer{}
	limited.SetOut(limitedOut)
	limited.SetErr(limitedErr)
	limited.SetArgs([]string{"pipeline", "ready", "--all", "--repo", root, "--state", "all", "--sort", "updated", "--limit", "1", "--format", "{{.JobID}}"})
	if err := limited.Execute(); err != nil {
		t.Fatalf("pipeline ready limit: %v\nstderr=%s", err, limitedErr.String())
	}
	if got := strings.TrimSpace(limitedOut.String()); got != "squ-311" {
		t.Fatalf("pipeline ready limited output = %q", limitedOut.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	watch := NewRootCmd()
	watchOut, watchErr := &bytes.Buffer{}, &bytes.Buffer{}
	watch.SetContext(ctx)
	watch.SetOut(watchOut)
	watch.SetErr(watchErr)
	watch.SetArgs([]string{"pipeline", "ready", "ticket_to_pr", "--repo", root, "--state", "all", "--sort", "updated", "--limit", "1", "--watch", "--no-clear", "--interval", "1ms", "--format", "{{.JobID}}"})
	if err := watch.Execute(); err != nil {
		t.Fatalf("pipeline ready watch: %v\nstderr=%s", err, watchErr.String())
	}
	if got := strings.TrimSpace(watchOut.String()); got != "squ-311" || strings.Contains(watchOut.String(), watchClearSequence) {
		t.Fatalf("pipeline ready watch output = %q", watchOut.String())
	}

	step := NewRootCmd()
	stepOut, stepErr := &bytes.Buffer{}, &bytes.Buffer{}
	step.SetOut(stepOut)
	step.SetErr(stepErr)
	step.SetArgs([]string{"pipeline", "ready", "ticket_to_pr", "--repo", root, "--state", "all", "--step", "implement", "--format", "{{.JobID}} {{.State}} {{.StepID}}"})
	if err := step.Execute(); err != nil {
		t.Fatalf("pipeline ready step filter: %v\nstderr=%s", err, stepErr.String())
	}
	if got := strings.TrimSpace(stepOut.String()); got != "squ-311 running implement" {
		t.Fatalf("pipeline ready step-filtered output = %q", stepOut.String())
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
	if !containsString(allRows[0].Actions, "agent-team pipeline tick ticket_to_pr --dry-run --preview-routes") ||
		!containsString(allRows[1].Actions, "agent-team pipeline tick nightly --dry-run --preview-routes") {
		t.Fatalf("all ready actions = %+v", allRows)
	}

	implicitAll := NewRootCmd()
	implicitAllOut, implicitAllErr := &bytes.Buffer{}, &bytes.Buffer{}
	implicitAll.SetOut(implicitAllOut)
	implicitAll.SetErr(implicitAllErr)
	implicitAll.SetArgs([]string{"pipeline", "ready", "--repo", root, "--json"})
	if err := implicitAll.Execute(); err != nil {
		t.Fatalf("pipeline ready implicit all json: %v\nstderr=%s", err, implicitAllErr.String())
	}
	var implicitRows []jobReadyRow
	if err := json.Unmarshal(implicitAllOut.Bytes(), &implicitRows); err != nil {
		t.Fatalf("decode pipeline ready implicit all json: %v\nbody=%s", err, implicitAllOut.String())
	}
	if len(implicitRows) != 2 || implicitRows[0].JobID != "squ-310" || implicitRows[1].JobID != "squ-312" {
		t.Fatalf("implicit all ready rows = %+v", implicitRows)
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

	invalidMany := NewRootCmd()
	invalidManyOut, invalidManyErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidMany.SetOut(invalidManyOut)
	invalidMany.SetErr(invalidManyErr)
	invalidMany.SetArgs([]string{"pipeline", "ready", "ticket_to_pr", "nightly", "--repo", root})
	if err := invalidMany.Execute(); err == nil {
		t.Fatalf("pipeline ready multiple pipeline args succeeded")
	}
	if !strings.Contains(invalidManyErr.String(), "pass at most one pipeline name") {
		t.Fatalf("invalid many stderr = %q", invalidManyErr.String())
	}

	invalidInterval := NewRootCmd()
	invalidIntervalOut, invalidIntervalErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidInterval.SetOut(invalidIntervalOut)
	invalidInterval.SetErr(invalidIntervalErr)
	invalidInterval.SetArgs([]string{"pipeline", "ready", "ticket_to_pr", "--repo", root, "--watch", "--interval", "-1s"})
	if err := invalidInterval.Execute(); err == nil {
		t.Fatalf("pipeline ready negative interval succeeded")
	}
	if !strings.Contains(invalidIntervalErr.String(), "--interval must be >= 0") {
		t.Fatalf("invalid interval stderr = %q", invalidIntervalErr.String())
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

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "json",
			args: []string{"pipeline", "ready", "ticket_to_pr", "--repo", root, "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "format",
			args: []string{"pipeline", "ready", "ticket_to_pr", "--repo", root, "--commands", "--format", "{{.JobID}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "watch",
			args: []string{"pipeline", "ready", "ticket_to_pr", "--repo", root, "--commands", "--watch"},
			want: "--commands cannot be combined with --watch",
		},
	} {
		t.Run("ready-commands-conflict-"+tc.name, func(t *testing.T) {
			conflict := NewRootCmd()
			conflictOut, conflictErr := &bytes.Buffer{}, &bytes.Buffer{}
			conflict.SetOut(conflictOut)
			conflict.SetErr(conflictErr)
			conflict.SetArgs(tc.args)
			if err := conflict.Execute(); err == nil {
				t.Fatalf("pipeline ready accepted %s conflict: stdout=%s", tc.name, conflictOut.String())
			}
			if !strings.Contains(conflictErr.String(), tc.want) {
				t.Fatalf("pipeline ready %s conflict stderr = %q, want %q", tc.name, conflictErr.String(), tc.want)
			}
		})
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
	if len(blocked.Actions) != 2 ||
		!containsString(blocked.Actions, "agent-team job approve squ-901 --step review") ||
		!containsString(blocked.Actions, "agent-team job reject squ-901 --step review") {
		t.Fatalf("blocked manual actions = %+v", blocked.Actions)
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
	if len(rows) != 1 || rows[0].Gate != job.StepGateManual || len(rows[0].Actions) != 2 ||
		!containsString(rows[0].Actions, "agent-team job approve squ-901 --step review") ||
		!containsString(rows[0].Actions, "agent-team job reject squ-901 --step review") {
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
	approve.SetArgs([]string{"job", "approve", "squ-901", "--repo", root, "--format", "{{.Job.ID}} {{.Step.ID}} {{.Step.Status}}"})
	if err := approve.Execute(); err != nil {
		t.Fatalf("approve gate: %v\nstderr=%s", err, approveErr.String())
	}
	if got := approveOut.String(); got != "squ-901 review queued\n" {
		t.Fatalf("approve format = %q", got)
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

func TestPipelineApproveManualGate(t *testing.T) {
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
	create.SetArgs([]string{"pipeline", "run", "ticket_to_pr", "SQU-902", "manual gate approval", "--repo", root, "--json"})
	if err := create.Execute(); err != nil {
		t.Fatalf("pipeline run: %v\nstderr=%s", err, createErr.String())
	}
	markDone := NewRootCmd()
	markDoneOut, markDoneErr := &bytes.Buffer{}, &bytes.Buffer{}
	markDone.SetOut(markDoneOut)
	markDone.SetErr(markDoneErr)
	markDone.SetArgs([]string{"job", "step", "squ-902", "implement", "--status", "done", "--repo", root, "--json"})
	if err := markDone.Execute(); err != nil {
		t.Fatalf("mark implement done: %v\nstderr=%s", err, markDoneErr.String())
	}

	dryRun := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dryRun.SetOut(dryOut)
	dryRun.SetErr(dryErr)
	dryRun.SetArgs([]string{"pipeline", "approve", "ticket_to_pr", "--repo", root, "--dry-run", "--dispatch", "--preview-routes", "--json"})
	if err := dryRun.Execute(); err != nil {
		t.Fatalf("pipeline approve dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview []pipelineApproveResult
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode approve dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(preview) != 1 || preview[0].Action != "would_dispatch" || preview[0].StepID != "review" || preview[0].StepStatus != job.StatusQueued || preview[0].Preview == nil || preview[0].Preview.Dispatch == nil {
		t.Fatalf("approve preview = %+v", preview)
	}
	if preview[0].Preview.Dispatch.RequestedName != "manager-squ-902-review" {
		t.Fatalf("dispatch preview = %+v", preview[0].Preview.Dispatch)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"pipeline", "approve", "ticket_to_pr", "--repo", root, "--step", "review", "--limit", "1", "--dispatch", "--workspace", "repo", "--runtime", "codex", "--runtime-bin", "codex-dev", "--message", "manual review approved", "--dry-run", "--preview-routes", "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("pipeline approve commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "pipeline", "approve", "ticket_to_pr", "--repo", root, "--dispatch", "--workspace", "repo", "--runtime", "codex", "--runtime-bin", "codex-dev", "--step", "review", "--limit", "1", "--message", "manual review approved"}), " ")
	if got := strings.TrimSpace(commandsOut.String()); got != wantCommand {
		t.Fatalf("pipeline approve commands = %q, want %q", got, wantCommand)
	}

	unchanged, err := job.Read(teamDir, "squ-902")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.Steps[1].Status != job.StatusBlocked {
		t.Fatalf("dry-run mutated manual gate = %+v", unchanged.Steps[1])
	}
	approvalFile := filepath.Join(root, "approval.txt")
	if err := os.WriteFile(approvalFile, []byte("manual review approved from file\n"), 0o644); err != nil {
		t.Fatalf("write approval file: %v", err)
	}

	approve := NewRootCmd()
	approveOut, approveErr := &bytes.Buffer{}, &bytes.Buffer{}
	approve.SetOut(approveOut)
	approve.SetErr(approveErr)
	approve.SetArgs([]string{"pipeline", "approve", "ticket_to_pr", "--repo", root, "--message-file", approvalFile, "--format", "{{.JobID}} {{.Action}} {{.StepID}} {{.Message}}"})
	if err := approve.Execute(); err != nil {
		t.Fatalf("pipeline approve: %v\nstderr=%s", err, approveErr.String())
	}
	if got := approveOut.String(); got != "squ-902 approved review manual review approved from file\n" {
		t.Fatalf("approve format = %q", got)
	}
	updated, err := job.Read(teamDir, "squ-902")
	if err != nil {
		t.Fatalf("read approved job: %v", err)
	}
	if updated.Status != job.StatusQueued || updated.Steps[1].Status != job.StatusQueued || updated.LastStatus != "manual review approved from file" {
		t.Fatalf("approved job = %+v", updated)
	}
}

func TestPipelineApproveDispatchWaitsForRequestedStatus(t *testing.T) {
	root, mgr, cleanup := setupManualGateApprovalRepo(t, false)
	defer cleanup()
	teamDir := filepath.Join(root, ".agent_team")
	writeManualGateApprovalJob(t, teamDir, "squ-905")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"pipeline", "approve", "ticket_to_pr",
		"--repo", root,
		"--dispatch",
		"--workspace", "repo",
		"--wait",
		"--wait-status", "running",
		"--wait-timeout", "2s",
		"--wait-interval", "10ms",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline approve --dispatch --wait: %v\nstderr=%s", err, stderr.String())
	}
	var rows []pipelineApproveResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline approve wait json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Action != "dispatched" || rows[0].Job == nil || rows[0].Job.Status != job.StatusRunning || rows[0].Job.LastEvent != "advance_dispatched" {
		t.Fatalf("approval wait rows = %+v", rows)
	}
	if rows[0].Step == nil || rows[0].Step.ID != "review" || rows[0].Step.Status != job.StatusRunning || rows[0].Step.Instance != "worker-squ-905-review" {
		t.Fatalf("approval wait step = %+v", rows[0].Step)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-905-review")
}

func TestPipelineApproveDispatchWaitsForNextStepState(t *testing.T) {
	root, mgr, cleanup := setupManualGateApprovalRepo(t, false)
	defer cleanup()
	teamDir := filepath.Join(root, ".agent_team")
	writeManualGateApprovalJob(t, teamDir, "squ-918")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"pipeline", "approve", "ticket_to_pr",
		"--repo", root,
		"--dispatch",
		"--workspace", "repo",
		"--wait",
		"--wait-next-state", "running",
		"--wait-step", "review",
		"--wait-timeout", "2s",
		"--wait-interval", "10ms",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline approve --dispatch --wait-next-state: %v\nstderr=%s", err, stderr.String())
	}
	var rows []pipelineApproveResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline approve next-state wait json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Action != "dispatched" || rows[0].Job == nil || rows[0].Job.Status != job.StatusRunning {
		t.Fatalf("approval next-state wait rows = %+v", rows)
	}
	if rows[0].Step == nil || rows[0].Step.ID != "review" || rows[0].Step.Status != job.StatusRunning || rows[0].Step.Instance != "worker-squ-918-review" {
		t.Fatalf("approval next-state wait step = %+v", rows[0].Step)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-918-review")
}

func TestPipelineApproveDispatchWaitTimesOutForEvent(t *testing.T) {
	root, mgr, cleanup := setupManualGateApprovalRepo(t, false)
	defer cleanup()
	teamDir := filepath.Join(root, ".agent_team")
	writeManualGateApprovalJob(t, teamDir, "squ-906")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"pipeline", "approve", "ticket_to_pr",
		"--repo", root,
		"--dispatch",
		"--workspace", "repo",
		"--wait",
		"--wait-event", "closed",
		"--wait-timeout", "1ms",
		"--wait-interval", "10ms",
	})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("pipeline approve --dispatch --wait succeeded unexpectedly")
	}
	if out.Len() != 0 {
		t.Fatalf("approve wait timeout wrote stdout=%q", out.String())
	}
	if !strings.Contains(stderr.String(), "timed out waiting for approved jobs to reach event=closed") ||
		!strings.Contains(stderr.String(), "pending=squ-906=running event=advance_dispatched") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	stopAndWaitForTest(t, mgr, "worker-squ-906-review")
}

func TestPipelineApproveValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"pipeline", "approve", "ticket_to_pr", "--wait-next-state", "running"}, "wait-related flags require --wait"},
		{[]string{"pipeline", "approve", "ticket_to_pr", "--wait-step", "review"}, "wait-related flags require --wait"},
		{[]string{"pipeline", "approve", "ticket_to_pr", "--wait", "--wait-next-state", "missing"}, "--wait-next-state must be ready, queued, running, blocked, failed, held, done, none, or all"},
		{[]string{"pipeline", "approve", "ticket_to_pr", "--commands"}, "--commands requires --dry-run"},
		{[]string{"pipeline", "approve", "ticket_to_pr", "--dry-run", "--commands", "--json"}, "--commands cannot be combined with --json"},
		{[]string{"pipeline", "approve", "ticket_to_pr", "--dry-run", "--commands", "--format", "{{.JobID}}"}, "--commands cannot be combined with --format"},
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

func setupManualGateApprovalRepo(t *testing.T, includeTeam bool) (root string, mgr *daemon.InstanceManager, cleanup func()) {
	t.Helper()
	root = t.TempDir()
	if eval, err := filepath.EvalSymlinks(root); err == nil {
		root = eval
	}
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(filepath.Join(teamDir, "agents", "worker"), 0o755); err != nil {
		t.Fatal(err)
	}
	agent := "---\ndescription: test worker\n---\n\nYou are a test worker.\n"
	if err := os.WriteFile(filepath.Join(teamDir, "agents", "worker", "agent.md"), []byte(agent), 0o644); err != nil {
		t.Fatal(err)
	}
	topologyText := topoFixture + `
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "worker"
after = ["implement"]
gate = "manual"
`
	if includeTeam {
		topologyText += `
[teams.delivery]
instances = ["worker"]
pipelines = ["ticket_to_pr"]
`
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topologyText), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr = daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), fakeSpawnerForTest(t, 2*time.Second))
	cleanupDaemon := startRunTestDaemon(t, teamDir, mgr)
	return root, mgr, cleanupDaemon
}

func writeManualGateApprovalJob(t *testing.T, teamDir, id string) {
	t.Helper()
	now := time.Now().UTC()
	j := &job.Job{
		ID:         id,
		Ticket:     strings.ToUpper(id),
		Target:     "worker",
		Kickoff:    "manual gate approval",
		Pipeline:   "ticket_to_pr",
		Status:     job.StatusBlocked,
		LastEvent:  "step_blocked",
		LastStatus: "review blocked",
		CreatedAt:  now,
		UpdatedAt:  now,
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusDone, StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
			{ID: "review", Target: "worker", Status: job.StatusBlocked, After: []string{"implement"}, Gate: job.StepGateManual},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write manual gate job: %v", err)
	}
}

func writeFailedRetryJob(t *testing.T, teamDir, id string) {
	t.Helper()
	now := time.Now().UTC()
	j := &job.Job{
		ID:         id,
		Ticket:     strings.ToUpper(id),
		Target:     "worker",
		Kickoff:    "retry failed implementation",
		Pipeline:   "ticket_to_pr",
		Status:     job.StatusFailed,
		LastEvent:  "step_failed",
		LastStatus: "implement failed",
		CreatedAt:  now,
		UpdatedAt:  now,
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusFailed, Instance: "worker-old", StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write failed retry job: %v", err)
	}
}

func writeReadyAdvanceJob(t *testing.T, teamDir, id string) {
	t.Helper()
	now := time.Now().UTC()
	j := &job.Job{
		ID:         id,
		Ticket:     strings.ToUpper(id),
		Target:     "worker",
		Kickoff:    "advance ready implementation",
		Pipeline:   "ticket_to_pr",
		Status:     job.StatusQueued,
		LastEvent:  "created",
		LastStatus: "ready",
		CreatedAt:  now,
		UpdatedAt:  now,
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusBlocked},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write ready advance job: %v", err)
	}
}

func TestPipelineRejectManualGate(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[[instances.manager.triggers]]
event        = "agent.dispatch"
payload.target = "manager"

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
	create.SetArgs([]string{"pipeline", "run", "ticket_to_pr", "SQU-903", "manual gate rejection", "--repo", root, "--json"})
	if err := create.Execute(); err != nil {
		t.Fatalf("pipeline run: %v\nstderr=%s", err, createErr.String())
	}
	markDone := NewRootCmd()
	markDoneOut, markDoneErr := &bytes.Buffer{}, &bytes.Buffer{}
	markDone.SetOut(markDoneOut)
	markDone.SetErr(markDoneErr)
	markDone.SetArgs([]string{"job", "step", "squ-903", "implement", "--status", "done", "--repo", root, "--json"})
	if err := markDone.Execute(); err != nil {
		t.Fatalf("mark implement done: %v\nstderr=%s", err, markDoneErr.String())
	}

	dryRun := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dryRun.SetOut(dryOut)
	dryRun.SetErr(dryErr)
	dryRun.SetArgs([]string{"job", "reject", "squ-903", "manual review rejected", "--repo", root, "--dry-run", "--json"})
	if err := dryRun.Execute(); err != nil {
		t.Fatalf("reject dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview jobStepPreview
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode reject dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if !preview.DryRun || preview.Job == nil || preview.Job.Status != job.StatusFailed || preview.Job.LastEvent != "manual_gate_rejected" || preview.Job.Steps[1].Status != job.StatusFailed {
		t.Fatalf("reject preview = %+v", preview)
	}
	unchanged, err := job.Read(teamDir, "squ-903")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.Status != job.StatusRunning || unchanged.Steps[1].Status != job.StatusBlocked {
		t.Fatalf("dry-run mutated manual gate = %+v", unchanged)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"job", "reject", "squ-903", "manual", "review", "rejected", "--repo", root, "--dry-run", "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("reject dry-run commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "job", "reject", "squ-903", "--repo", root, "--step", "review", "manual", "review", "rejected"}), " ")
	if got := strings.TrimSpace(commandsOut.String()); got != wantCommand {
		t.Fatalf("reject dry-run commands = %q, want %q", got, wantCommand)
	}

	reject := NewRootCmd()
	rejectOut, rejectErr := &bytes.Buffer{}, &bytes.Buffer{}
	reject.SetOut(rejectOut)
	reject.SetErr(rejectErr)
	reject.SetArgs([]string{"job", "reject", "squ-903", "--repo", root, "--step", "review", "--message", "manual review rejected", "--format", "{{.ID}} {{.Status}} {{.LastEvent}} {{.LastStatus}}"})
	if err := reject.Execute(); err != nil {
		t.Fatalf("reject gate: %v\nstderr=%s", err, rejectErr.String())
	}
	if got := rejectOut.String(); got != "squ-903 failed manual_gate_rejected manual review rejected\n" {
		t.Fatalf("reject format = %q", got)
	}
	rejected, err := job.Read(teamDir, "squ-903")
	if err != nil {
		t.Fatalf("read rejected job: %v", err)
	}
	if rejected.Status != job.StatusFailed || rejected.Steps[1].Status != job.StatusFailed || rejected.LastEvent != "manual_gate_rejected" || rejected.LastStatus != "manual review rejected" {
		t.Fatalf("rejected job = %+v", rejected)
	}
	events, err := job.ListEvents(teamDir, "squ-903")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) == 0 || events[len(events)-1].Type != "manual_gate_rejected" || events[len(events)-1].Message != "manual review rejected" {
		t.Fatalf("reject events = %+v", events)
	}
}

func TestPipelineRejectManualGateBatch(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[[instances.manager.triggers]]
event        = "agent.dispatch"
payload.target = "manager"

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
	create.SetArgs([]string{"pipeline", "run", "ticket_to_pr", "SQU-904", "manual gate batch rejection", "--repo", root, "--json"})
	if err := create.Execute(); err != nil {
		t.Fatalf("pipeline run: %v\nstderr=%s", err, createErr.String())
	}
	markDone := NewRootCmd()
	markDoneOut, markDoneErr := &bytes.Buffer{}, &bytes.Buffer{}
	markDone.SetOut(markDoneOut)
	markDone.SetErr(markDoneErr)
	markDone.SetArgs([]string{"job", "step", "squ-904", "implement", "--status", "done", "--repo", root, "--json"})
	if err := markDone.Execute(); err != nil {
		t.Fatalf("mark implement done: %v\nstderr=%s", err, markDoneErr.String())
	}

	dryRun := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dryRun.SetOut(dryOut)
	dryRun.SetErr(dryErr)
	dryRun.SetArgs([]string{"pipeline", "reject", "ticket_to_pr", "--repo", root, "--dry-run", "--json"})
	if err := dryRun.Execute(); err != nil {
		t.Fatalf("pipeline reject dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview []pipelineApproveResult
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode reject dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(preview) != 1 || preview[0].Action != "would_reject" || preview[0].StepID != "review" || preview[0].StepStatus != job.StatusFailed || preview[0].Job == nil || preview[0].Job.Status != job.StatusFailed {
		t.Fatalf("reject preview = %+v", preview)
	}
	unchanged, err := job.Read(teamDir, "squ-904")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.Status != job.StatusRunning || unchanged.Steps[1].Status != job.StatusBlocked {
		t.Fatalf("dry-run mutated manual gate = %+v", unchanged)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"pipeline", "reject", "ticket_to_pr", "--repo", root, "--message", "manual batch rejected", "--dry-run", "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("pipeline reject dry-run commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "pipeline", "reject", "ticket_to_pr", "--repo", root, "--message", "manual batch rejected"}), " ")
	if got := strings.TrimSpace(commandsOut.String()); got != wantCommand {
		t.Fatalf("pipeline reject dry-run commands = %q, want %q", got, wantCommand)
	}
	rejectionFile := filepath.Join(root, "rejection.txt")
	if err := os.WriteFile(rejectionFile, []byte("manual batch rejected from file\n"), 0o644); err != nil {
		t.Fatalf("write rejection file: %v", err)
	}

	reject := NewRootCmd()
	rejectOut, rejectErr := &bytes.Buffer{}, &bytes.Buffer{}
	reject.SetOut(rejectOut)
	reject.SetErr(rejectErr)
	reject.SetArgs([]string{"pipeline", "reject", "ticket_to_pr", "--repo", root, "--message-file", rejectionFile, "--format", "{{.JobID}} {{.Action}} {{.StepID}} {{.Message}}"})
	if err := reject.Execute(); err != nil {
		t.Fatalf("pipeline reject: %v\nstderr=%s", err, rejectErr.String())
	}
	if got := rejectOut.String(); got != "squ-904 rejected review manual batch rejected from file\n" {
		t.Fatalf("reject format = %q", got)
	}
	rejected, err := job.Read(teamDir, "squ-904")
	if err != nil {
		t.Fatalf("read rejected job: %v", err)
	}
	if rejected.Status != job.StatusFailed || rejected.Steps[1].Status != job.StatusFailed || rejected.LastEvent != "manual_gate_rejected" || rejected.LastStatus != "manual batch rejected from file" {
		t.Fatalf("rejected job = %+v", rejected)
	}
	events, err := job.ListEvents(teamDir, "squ-904")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) == 0 || events[len(events)-1].Type != "manual_gate_rejected" || events[len(events)-1].Message != "manual batch rejected from file" {
		t.Fatalf("reject events = %+v", events)
	}
}

func TestPipelineUnblockBlockedStepBatch(t *testing.T) {
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
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-970",
			Ticket:    "SQU-970",
			Target:    "worker",
			Kickoff:   "blocked pipeline worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusBlocked, Instance: "worker-squ-970-implement", StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
			},
		},
		{
			ID:        "squ-971",
			Ticket:    "SQU-971",
			Target:    "worker",
			Kickoff:   "foreign blocked worker",
			Pipeline:  "ops_review",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusBlocked, Instance: "worker-squ-971-implement", StartedAt: now.Add(-time.Hour)},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}
	for _, meta := range []*daemon.Metadata{
		{Instance: "worker-squ-970-implement", Agent: "worker", Status: daemon.StatusRunning, Runtime: string(runtimebin.KindCodex), PID: os.Getpid(), StartedAt: now.Add(-time.Hour), Job: "squ-970", Ticket: "SQU-970", Workspace: root},
		{Instance: "worker-squ-971-implement", Agent: "worker", Status: daemon.StatusRunning, Runtime: string(runtimebin.KindCodex), PID: os.Getpid(), StartedAt: now.Add(-time.Hour), Job: "squ-971", Ticket: "SQU-971", Workspace: root},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	dryRun := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dryRun.SetOut(dryOut)
	dryRun.SetErr(dryErr)
	dryRun.SetArgs([]string{"pipeline", "unblock", "ticket_to_pr", "--repo", root, "--dry-run", "--json", "credentials", "configured"})
	if err := dryRun.Execute(); err != nil {
		t.Fatalf("pipeline unblock dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview []pipelineUnblockResult
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode unblock dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(preview) != 1 || preview[0].JobID != "squ-970" || preview[0].Action != "would_unblock" || preview[0].StepID != "implement" || preview[0].StepStatus != job.StatusRunning || preview[0].Job == nil || preview[0].Job.Status != job.StatusRunning {
		t.Fatalf("unblock preview = %+v", preview)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"pipeline", "unblock", "ticket_to_pr", "--repo", root, "--step", "implement", "--status", "queued", "--from", "operator", "--limit", "1", "--dry-run", "--commands", "credentials", "configured"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("pipeline unblock commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "pipeline", "unblock", "ticket_to_pr", "--repo", root, "--step", "implement", "--status", "queued", "--from", "operator", "--limit", "1", "credentials", "configured"}), " ")
	if got := strings.TrimSpace(commandsOut.String()); got != wantCommand {
		t.Fatalf("pipeline unblock commands = %q, want %q", got, wantCommand)
	}

	unchanged, err := job.Read(teamDir, "squ-970")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.Steps[0].Status != job.StatusBlocked || unchanged.Steps[0].FinishedAt.IsZero() {
		t.Fatalf("dry-run mutated job = %+v", unchanged)
	}

	messageFile := filepath.Join(root, "pipeline-unblock.txt")
	if err := os.WriteFile(messageFile, []byte("credentials configured from file\n"), 0o644); err != nil {
		t.Fatalf("write unblock message: %v", err)
	}
	run := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	run.SetOut(out)
	run.SetErr(stderr)
	run.SetArgs([]string{"pipeline", "unblock", "ticket_to_pr", "--repo", root, "--from", "operator", "--status", "queued", "--message-file", messageFile, "--format", "{{.JobID}} {{.Action}} {{.StepID}} {{.StatusAfter}} {{.Instance}} {{.Message}}"})
	if err := run.Execute(); err != nil {
		t.Fatalf("pipeline unblock: %v\nstderr=%s", err, stderr.String())
	}
	if got := out.String(); got != "squ-970 unblocked implement queued worker-squ-970-implement credentials configured from file\n" {
		t.Fatalf("unblock format = %q", got)
	}
	updated, err := job.Read(teamDir, "squ-970")
	if err != nil {
		t.Fatalf("read updated job: %v", err)
	}
	if updated.Status != job.StatusQueued || updated.LastEvent != "unblocked" || updated.LastStatus != "credentials configured from file" || updated.Steps[0].Status != job.StatusQueued || !updated.Steps[0].FinishedAt.IsZero() {
		t.Fatalf("updated job = %+v", updated)
	}
	messages, err := daemon.ReadMessages(daemon.DaemonRoot(teamDir), "worker-squ-970-implement")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 1 || messages[0].From != "operator" || messages[0].Body != "credentials configured from file" {
		t.Fatalf("messages = %+v", messages)
	}
	foreignMessages, err := daemon.ReadMessages(daemon.DaemonRoot(teamDir), "worker-squ-971-implement")
	if err != nil {
		t.Fatalf("read foreign messages: %v", err)
	}
	if len(foreignMessages) != 0 {
		t.Fatalf("foreign messages = %+v", foreignMessages)
	}
	events, err := job.ListEvents(teamDir, "squ-970")
	if err != nil {
		t.Fatalf("list unblock events: %v", err)
	}
	if len(events) == 0 || events[len(events)-1].Type != "unblocked" || events[len(events)-1].Data["step"] != "implement" || events[len(events)-1].Data["instance"] != "worker-squ-970-implement" {
		t.Fatalf("unblock events = %+v", events)
	}
	foreign, err := job.Read(teamDir, "squ-971")
	if err != nil {
		t.Fatalf("read foreign job: %v", err)
	}
	if foreign.Steps[0].Status != job.StatusBlocked || foreign.LastEvent == "unblocked" {
		t.Fatalf("foreign job changed = %+v", foreign)
	}
}

func TestPipelineSkipStepBatch(t *testing.T) {
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
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-905",
			Ticket:    "SQU-905",
			Target:    "worker",
			Kickoff:   "skip review",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusBlocked,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusDone, StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
				{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}},
			},
		},
		{
			ID:        "squ-906",
			Ticket:    "SQU-906",
			Target:    "worker",
			Kickoff:   "running review",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusDone, StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
				{ID: "review", Target: "manager", Status: job.StatusRunning, After: []string{"implement"}, Instance: "manager-squ-906-review", StartedAt: now.Add(-10 * time.Minute)},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	missingStep := NewRootCmd()
	missingStepOut, missingStepErr := &bytes.Buffer{}, &bytes.Buffer{}
	missingStep.SetOut(missingStepOut)
	missingStep.SetErr(missingStepErr)
	missingStep.SetArgs([]string{"pipeline", "skip", "ticket_to_pr", "--repo", root})
	if err := missingStep.Execute(); err == nil {
		t.Fatal("pipeline skip without --step succeeded")
	}

	dryRun := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dryRun.SetOut(dryOut)
	dryRun.SetErr(dryErr)
	dryRun.SetArgs([]string{"pipeline", "skip", "ticket_to_pr", "--repo", root, "--step", "review", "--message", "review covered elsewhere", "--dry-run", "--json"})
	if err := dryRun.Execute(); err != nil {
		t.Fatalf("pipeline skip dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview []pipelineSkipResult
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode skip dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(preview) != 2 || preview[0].JobID != "squ-905" || preview[0].Action != "would_skip" || preview[0].StepStatus != job.StatusDone || !preview[0].Skipped || preview[0].SkipReason != "review covered elsewhere" {
		t.Fatalf("skip preview[0] = %+v", preview)
	}
	if preview[1].JobID != "squ-906" || preview[1].Action != "skipped" || preview[1].StepStatus != job.StatusRunning || preview[1].Skipped {
		t.Fatalf("skip preview[1] = %+v", preview)
	}
	unchanged, err := job.Read(teamDir, "squ-905")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.Status != job.StatusBlocked || unchanged.Steps[1].Status != job.StatusBlocked || unchanged.Steps[1].Skipped {
		t.Fatalf("dry-run mutated skipped job = %+v", unchanged)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"pipeline", "skip", "ticket_to_pr", "--repo", root, "--step", "review", "--limit", "1", "--message", "review covered elsewhere", "--dry-run", "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("pipeline skip dry-run commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "pipeline", "skip", "ticket_to_pr", "--repo", root, "--step", "review", "--limit", "1", "--message", "review covered elsewhere"}), " ")
	if got := strings.TrimSpace(commandsOut.String()); got != wantCommand {
		t.Fatalf("pipeline skip dry-run commands = %q, want %q", got, wantCommand)
	}
	skipFile := filepath.Join(root, "skip-reason.txt")
	if err := os.WriteFile(skipFile, []byte("review covered from file\n"), 0o644); err != nil {
		t.Fatalf("write skip reason file: %v", err)
	}

	run := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	run.SetOut(out)
	run.SetErr(stderr)
	run.SetArgs([]string{"pipeline", "skip", "ticket_to_pr", "--repo", root, "--step", "review", "--message-file", skipFile, "--format", "{{.JobID}} {{.Action}} {{.StepID}} {{.Skipped}} {{.Message}}"})
	if err := run.Execute(); err != nil {
		t.Fatalf("pipeline skip: %v\nstderr=%s", err, stderr.String())
	}
	if got := out.String(); got != "squ-905 skipped review true review covered from file\nsqu-906 skipped review false step is running; timeout or stop the owner before skipping\n" {
		t.Fatalf("skip format = %q", got)
	}
	skipped, err := job.Read(teamDir, "squ-905")
	if err != nil {
		t.Fatalf("read skipped job: %v", err)
	}
	if skipped.Status != job.StatusDone || skipped.Steps[1].Status != job.StatusDone || !skipped.Steps[1].Skipped || skipped.Steps[1].SkipReason != "review covered from file" {
		t.Fatalf("skipped job = %+v", skipped)
	}
	events, err := job.ListEvents(teamDir, "squ-905")
	if err != nil {
		t.Fatalf("list skip events: %v", err)
	}
	if len(events) == 0 || events[len(events)-1].Type != "step_skipped" || events[len(events)-1].Message != "review covered from file" {
		t.Fatalf("skip events = %+v", events)
	}
	running, err := job.Read(teamDir, "squ-906")
	if err != nil {
		t.Fatalf("read running job: %v", err)
	}
	if running.Status != job.StatusRunning || running.Steps[1].Status != job.StatusRunning || running.Steps[1].Skipped {
		t.Fatalf("running job changed = %+v", running)
	}
}

func TestPipelineCancelBatch(t *testing.T) {
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
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-907",
			Ticket:    "SQU-907",
			Target:    "worker",
			Kickoff:   "obsolete running job",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-907",
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-907", StartedAt: now.Add(-time.Hour)},
			},
		},
		{
			ID:        "squ-908",
			Ticket:    "SQU-908",
			Target:    "worker",
			Kickoff:   "already done",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusDone,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusDone, StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	dryRun := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dryRun.SetOut(dryOut)
	dryRun.SetErr(dryErr)
	dryRun.SetArgs([]string{"pipeline", "cancel", "ticket_to_pr", "--repo", root, "--message", "duplicate ticket", "--dry-run", "--json"})
	if err := dryRun.Execute(); err != nil {
		t.Fatalf("pipeline cancel dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview []pipelineCancelResult
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode cancel dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(preview) != 1 || preview[0].JobID != "squ-907" || preview[0].Action != "would_cancel" || preview[0].StatusBefore != job.StatusRunning || preview[0].StatusAfter != job.StatusFailed || preview[0].Instance != "worker-squ-907" {
		t.Fatalf("cancel preview = %+v", preview)
	}
	unchanged, err := job.Read(teamDir, "squ-907")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.Status != job.StatusRunning || unchanged.LastEvent == "cancelled" {
		t.Fatalf("dry-run mutated job = %+v", unchanged)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"pipeline", "cancel", "ticket_to_pr", "--repo", root, "--message", "duplicate ticket", "--actor", "ops", "--limit", "1", "--dry-run", "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("pipeline cancel dry-run commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "pipeline", "cancel", "ticket_to_pr", "--repo", root, "--limit", "1", "--actor", "ops", "--message", "duplicate ticket"}), " ")
	if got := strings.TrimSpace(commandsOut.String()); got != wantCommand {
		t.Fatalf("pipeline cancel dry-run commands = %q, want %q", got, wantCommand)
	}
	cancelFile := filepath.Join(root, "cancel-reason.txt")
	if err := os.WriteFile(cancelFile, []byte("duplicate ticket from file\n"), 0o644); err != nil {
		t.Fatalf("write cancel reason file: %v", err)
	}

	run := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	run.SetOut(out)
	run.SetErr(stderr)
	run.SetArgs([]string{"pipeline", "cancel", "ticket_to_pr", "--repo", root, "--message-file", cancelFile, "--format", "{{.JobID}} {{.Action}} {{.StatusBefore}} {{.StatusAfter}} {{.Instance}} {{.Message}}"})
	if err := run.Execute(); err != nil {
		t.Fatalf("pipeline cancel: %v\nstderr=%s", err, stderr.String())
	}
	if got := out.String(); got != "squ-907 cancelled running failed worker-squ-907 duplicate ticket from file\n" {
		t.Fatalf("cancel format = %q", got)
	}
	cancelled, err := job.Read(teamDir, "squ-907")
	if err != nil {
		t.Fatalf("read cancelled job: %v", err)
	}
	if cancelled.Status != job.StatusFailed || cancelled.LastEvent != "cancelled" || cancelled.LastStatus != "duplicate ticket from file" || cancelled.Instance != "worker-squ-907" {
		t.Fatalf("cancelled job = %+v", cancelled)
	}
	done, err := job.Read(teamDir, "squ-908")
	if err != nil {
		t.Fatalf("read done job: %v", err)
	}
	if done.Status != job.StatusDone || done.LastEvent == "cancelled" {
		t.Fatalf("terminal job changed = %+v", done)
	}
	events, err := job.ListEvents(teamDir, "squ-907")
	if err != nil {
		t.Fatalf("list cancel events: %v", err)
	}
	if len(events) == 0 || events[len(events)-1].Type != "cancelled" || events[len(events)-1].Message != "duplicate ticket from file" || events[len(events)-1].Data["instance"] != "worker-squ-907" {
		t.Fatalf("cancel events = %+v", events)
	}
}

func TestPipelineBatchMutationCommandsValidation(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "reject without dry-run",
			args: []string{"pipeline", "reject", "ticket_to_pr", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "reject with json",
			args: []string{"pipeline", "reject", "ticket_to_pr", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "reject with format",
			args: []string{"pipeline", "reject", "ticket_to_pr", "--dry-run", "--commands", "--format", "{{.JobID}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "skip without dry-run",
			args: []string{"pipeline", "skip", "ticket_to_pr", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "skip with json",
			args: []string{"pipeline", "skip", "ticket_to_pr", "--step", "review", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "skip with format",
			args: []string{"pipeline", "skip", "ticket_to_pr", "--step", "review", "--dry-run", "--commands", "--format", "{{.JobID}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "cancel without dry-run",
			args: []string{"pipeline", "cancel", "ticket_to_pr", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "cancel with json",
			args: []string{"pipeline", "cancel", "ticket_to_pr", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "cancel with format",
			args: []string{"pipeline", "cancel", "ticket_to_pr", "--dry-run", "--commands", "--format", "{{.JobID}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "unblock without dry-run",
			args: []string{"pipeline", "unblock", "ticket_to_pr", "--commands", "ready"},
			want: "--commands requires --dry-run",
		},
		{
			name: "unblock with json",
			args: []string{"pipeline", "unblock", "ticket_to_pr", "--dry-run", "--commands", "--json", "ready"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "unblock with format",
			args: []string{"pipeline", "unblock", "ticket_to_pr", "--dry-run", "--commands", "--format", "{{.JobID}}", "ready"},
			want: "--commands cannot be combined with --format",
		},
	} {
		cmd := NewRootCmd()
		out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(stderr)
		cmd.SetArgs(tt.args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("%s succeeded unexpectedly; stdout=%s", tt.name, out.String())
		}
		if !strings.Contains(stderr.String(), tt.want) {
			t.Fatalf("%s stderr = %q, want %q", tt.name, stderr.String(), tt.want)
		}
	}
}

func TestPipelineResumePlanScopesRuntimeMetadata(t *testing.T) {
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

[pipelines.ops_review]
trigger.event = "ops.created"

[[pipelines.ops_review.steps]]
id = "audit"
target = "worker"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	oldPIDLiveCheck := daemon.PidLiveCheck
	daemon.PidLiveCheck = func(pid int) bool {
		return pid != 4242
	}
	t.Cleanup(func() {
		daemon.PidLiveCheck = oldPIDLiveCheck
	})
	for _, j := range []*job.Job{
		{
			ID:        "squ-940",
			Ticket:    "SQU-940",
			Target:    "worker",
			Kickoff:   "recover runtime",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-940",
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-940", StartedAt: now.Add(-time.Hour)},
				{ID: "review", Target: "manager", Status: job.StatusRunning, Instance: "manager-squ-940", StartedAt: now.Add(-30 * time.Minute)},
			},
		},
		{
			ID:        "squ-941",
			Ticket:    "SQU-941",
			Target:    "worker",
			Kickoff:   "foreign pipeline",
			Pipeline:  "ops_review",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-941",
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "audit", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-941", StartedAt: now.Add(-20 * time.Minute)},
			},
		},
		{
			ID:        "squ-942",
			Ticket:    "SQU-942",
			Target:    "worker",
			Kickoff:   "stale worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-942",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "squ-943",
			Ticket:    "SQU-943",
			Target:    "worker",
			Kickoff:   "ad hoc runtime",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-943",
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}
	for _, meta := range []*daemon.Metadata{
		{Instance: "worker-squ-940", Agent: "worker", Runtime: "codex", RuntimeBinary: "codex", Status: daemon.StatusCrashed, StartedAt: now.Add(-time.Hour), ExitedAt: now.Add(-10 * time.Minute)},
		{Instance: "manager-squ-940", Job: "squ-940", Agent: "manager", Runtime: "claude", RuntimeBinary: "claude", Status: daemon.StatusCrashed, StartedAt: now.Add(-30 * time.Minute), ExitedAt: now.Add(-5 * time.Minute)},
		{Instance: "worker-squ-941", Job: "squ-941", Agent: "worker", Runtime: "codex", RuntimeBinary: "codex", Status: daemon.StatusCrashed, StartedAt: now.Add(-20 * time.Minute), ExitedAt: now.Add(-2 * time.Minute)},
		{Instance: "worker-squ-942", Job: "squ-942", Agent: "worker", Runtime: "claude", RuntimeBinary: "claude", PID: 4242, SessionID: "stale-session", Status: daemon.StatusRunning, StartedAt: now.Add(-15 * time.Minute)},
		{Instance: "worker-squ-943", Job: "squ-943", Agent: "worker", Runtime: "codex", RuntimeBinary: "codex", Status: daemon.StatusCrashed, StartedAt: now.Add(-10 * time.Minute), ExitedAt: now.Add(-time.Minute)},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "resume-plan", "ticket_to_pr", "--repo", root, "--status", "crashed", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline resume-plan json: %v\nstderr=%s", err, stderr.String())
	}
	var plans []runtimeResumePlan
	if err := json.Unmarshal(out.Bytes(), &plans); err != nil {
		t.Fatalf("decode pipeline resume-plan: %v\nbody=%s", err, out.String())
	}
	if len(plans) != 2 || plans[0].Instance != "manager-squ-940" || plans[1].Instance != "worker-squ-940" {
		t.Fatalf("plans = %+v, want manager-squ-940 and worker-squ-940 only", plans)
	}
	if plans[0].Job != "squ-940" || plans[0].Pipeline != "ticket_to_pr" || plans[0].StepID != "review" || plans[1].Job != "squ-940" || plans[1].Pipeline != "ticket_to_pr" || plans[1].StepID != "implement" || plans[1].JobLogsCommand != "agent-team job logs squ-940 --follow" || plans[1].JobLastMessageCommand != "agent-team job logs squ-940 --last-message" {
		t.Fatalf("job-scoped commands not populated: %+v", plans)
	}

	all := NewRootCmd()
	allOut, allErr := &bytes.Buffer{}, &bytes.Buffer{}
	all.SetOut(allOut)
	all.SetErr(allErr)
	all.SetArgs([]string{"pipeline", "resume-plan", "--repo", root, "--status", "crashed", "--json"})
	if err := all.Execute(); err != nil {
		t.Fatalf("pipeline resume-plan all json: %v\nstderr=%s", err, allErr.String())
	}
	var allPlans []runtimeResumePlan
	if err := json.Unmarshal(allOut.Bytes(), &allPlans); err != nil {
		t.Fatalf("decode pipeline resume-plan all: %v\nbody=%s", err, allOut.String())
	}
	if len(allPlans) != 3 || allPlans[0].Instance != "manager-squ-940" || allPlans[1].Instance != "worker-squ-940" || allPlans[2].Instance != "worker-squ-941" {
		t.Fatalf("all plans = %+v, want only pipeline-owned crashed metadata", allPlans)
	}
	if allPlans[2].Job != "squ-941" || allPlans[2].Pipeline != "ops_review" || allPlans[2].StepID != "audit" {
		t.Fatalf("all-pipeline plan missing foreign pipeline context: %+v", allPlans[2])
	}

	allSummary := NewRootCmd()
	allSummaryOut, allSummaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	allSummary.SetOut(allSummaryOut)
	allSummary.SetErr(allSummaryErr)
	allSummary.SetArgs([]string{"pipeline", "resume-plan", "--all", "--repo", root, "--status", "crashed", "--summary", "--json"})
	if err := allSummary.Execute(); err != nil {
		t.Fatalf("pipeline resume-plan --all summary: %v\nstderr=%s", err, allSummaryErr.String())
	}
	var allCounts runtimeResumeSummary
	if err := json.Unmarshal(allSummaryOut.Bytes(), &allCounts); err != nil {
		t.Fatalf("decode pipeline resume-plan --all summary: %v\nbody=%s", err, allSummaryOut.String())
	}
	if allCounts.Total != 3 || allCounts.Actions["logs"] != 3 || allCounts.Runtimes["claude"] != 1 || allCounts.Runtimes["codex"] != 2 || allCounts.Statuses["crashed"] != 3 {
		t.Fatalf("pipeline resume-plan --all summary = %+v", allCounts)
	}

	format := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	format.SetOut(formatOut)
	format.SetErr(formatErr)
	format.SetArgs([]string{"pipeline", "resume-plan", "ticket_to_pr", "--repo", root, "--step", "implement", "--runtime", "codex", "--action", "logs", "--format", "{{.Instance}} {{.Runtime}} {{.RecommendedAction}} {{.Pipeline}} {{.StepID}} {{.JobLogsCommand}}"})
	if err := format.Execute(); err != nil {
		t.Fatalf("pipeline resume-plan format: %v\nstderr=%s", err, formatErr.String())
	}
	if got, want := strings.TrimSpace(formatOut.String()), "worker-squ-940 codex logs ticket_to_pr implement agent-team job logs squ-940 --follow"; got != want {
		t.Fatalf("formatted pipeline resume-plan = %q, want %q", got, want)
	}

	summary := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(summaryOut)
	summary.SetErr(summaryErr)
	summary.SetArgs([]string{"pipeline", "resume-plan", "ticket_to_pr", "--repo", root, "--status", "crashed", "--summary", "--json"})
	if err := summary.Execute(); err != nil {
		t.Fatalf("pipeline resume-plan summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var counts runtimeResumeSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &counts); err != nil {
		t.Fatalf("decode pipeline resume-plan summary: %v\nbody=%s", err, summaryOut.String())
	}
	if counts.Total != 2 || counts.Actions["logs"] != 2 || counts.Runtimes["claude"] != 1 || counts.Runtimes["codex"] != 1 || counts.Statuses["crashed"] != 2 {
		t.Fatalf("pipeline resume-plan summary = %+v", counts)
	}

	stale := NewRootCmd()
	staleOut, staleErr := &bytes.Buffer{}, &bytes.Buffer{}
	stale.SetOut(staleOut)
	stale.SetErr(staleErr)
	stale.SetArgs([]string{"pipeline", "resume-plan", "ticket_to_pr", "--repo", root, "--runtime-stale", "--format", "{{.Job}} {{.Instance}} {{.Stale}} {{.RecommendedAction}}"})
	if err := stale.Execute(); err != nil {
		t.Fatalf("pipeline resume-plan runtime-stale filter: %v\nstderr=%s", err, staleErr.String())
	}
	if got, want := strings.TrimSpace(staleOut.String()), "squ-942 worker-squ-942 true start"; got != want {
		t.Fatalf("pipeline stale resume-plan = %q, want %q", got, want)
	}

	unhealthy := NewRootCmd()
	unhealthyOut, unhealthyErr := &bytes.Buffer{}, &bytes.Buffer{}
	unhealthy.SetOut(unhealthyOut)
	unhealthy.SetErr(unhealthyErr)
	unhealthy.SetArgs([]string{"pipeline", "resume-plan", "ticket_to_pr", "--repo", root, "--unhealthy", "--format", "{{.Instance}} {{.RecommendedAction}} {{.Stale}}"})
	if err := unhealthy.Execute(); err != nil {
		t.Fatalf("pipeline resume-plan unhealthy filter: %v\nstderr=%s", err, unhealthyErr.String())
	}
	if got, want := strings.TrimSpace(unhealthyOut.String()), strings.Join([]string{
		"manager-squ-940 logs false",
		"worker-squ-940 logs false",
		"worker-squ-942 start true",
	}, "\n"); got != want {
		t.Fatalf("pipeline unhealthy resume-plan = %q, want %q", got, want)
	}

	sortStale := NewRootCmd()
	sortStaleOut, sortStaleErr := &bytes.Buffer{}, &bytes.Buffer{}
	sortStale.SetOut(sortStaleOut)
	sortStale.SetErr(sortStaleErr)
	sortStale.SetArgs([]string{"pipeline", "resume-plan", "ticket_to_pr", "--repo", root, "--unhealthy", "--sort", "stale", "--format", "{{.Instance}} {{.RecommendedAction}} {{.Stale}}"})
	if err := sortStale.Execute(); err != nil {
		t.Fatalf("pipeline resume-plan sort stale: %v\nstderr=%s", err, sortStaleErr.String())
	}
	if got, want := strings.TrimSpace(sortStaleOut.String()), strings.Join([]string{
		"worker-squ-942 start true",
		"manager-squ-940 logs false",
		"worker-squ-940 logs false",
	}, "\n"); got != want {
		t.Fatalf("pipeline stale-sorted resume-plan = %q, want %q", got, want)
	}

	limited := NewRootCmd()
	limitedOut, limitedErr := &bytes.Buffer{}, &bytes.Buffer{}
	limited.SetOut(limitedOut)
	limited.SetErr(limitedErr)
	limited.SetArgs([]string{"pipeline", "resume-plan", "ticket_to_pr", "--repo", root, "--unhealthy", "--sort", "stale", "--limit", "2", "--format", "{{.Instance}} {{.RecommendedAction}} {{.Stale}}"})
	if err := limited.Execute(); err != nil {
		t.Fatalf("pipeline resume-plan sort stale limit: %v\nstderr=%s", err, limitedErr.String())
	}
	if got, want := strings.TrimSpace(limitedOut.String()), strings.Join([]string{
		"worker-squ-942 start true",
		"manager-squ-940 logs false",
	}, "\n"); got != want {
		t.Fatalf("pipeline limited resume-plan = %q, want %q", got, want)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"pipeline", "resume-plan", "ticket_to_pr", "--repo", root, "--unhealthy", "--sort", "stale", "--limit", "2", "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("pipeline resume-plan commands: %v\nstderr=%s", err, commandsErr.String())
	}
	if got, want := strings.TrimSpace(commandsOut.String()), strings.Join([]string{
		"agent-team start worker-squ-942",
		"agent-team logs manager-squ-940 --follow",
	}, "\n"); got != want {
		t.Fatalf("pipeline commands resume-plan = %q, want %q", got, want)
	}

	invalidMany := NewRootCmd()
	invalidManyOut, invalidManyErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidMany.SetOut(invalidManyOut)
	invalidMany.SetErr(invalidManyErr)
	invalidMany.SetArgs([]string{"pipeline", "resume-plan", "ticket_to_pr", "ops_review", "--repo", root})
	if err := invalidMany.Execute(); err == nil {
		t.Fatalf("pipeline resume-plan accepted multiple pipeline names: stdout=%s", invalidManyOut.String())
	}
	if !strings.Contains(invalidManyErr.String(), "pass at most one pipeline name") {
		t.Fatalf("multiple pipeline error = %q", invalidManyErr.String())
	}

	invalidAll := NewRootCmd()
	invalidAllOut, invalidAllErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidAll.SetOut(invalidAllOut)
	invalidAll.SetErr(invalidAllErr)
	invalidAll.SetArgs([]string{"pipeline", "resume-plan", "ticket_to_pr", "--all", "--repo", root})
	if err := invalidAll.Execute(); err == nil {
		t.Fatalf("pipeline resume-plan accepted --all with pipeline: stdout=%s", invalidAllOut.String())
	}
	if !strings.Contains(invalidAllErr.String(), "--all cannot be combined with a pipeline argument") {
		t.Fatalf("--all conflict error = %q", invalidAllErr.String())
	}

	invalidSort := NewRootCmd()
	invalidSortOut, invalidSortErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidSort.SetOut(invalidSortOut)
	invalidSort.SetErr(invalidSortErr)
	invalidSort.SetArgs([]string{"pipeline", "resume-plan", "ticket_to_pr", "--repo", root, "--sort", "age"})
	if err := invalidSort.Execute(); err == nil {
		t.Fatalf("pipeline resume-plan accepted invalid sort: stdout=%s", invalidSortOut.String())
	}
	if !strings.Contains(invalidSortErr.String(), "--sort must be instance") {
		t.Fatalf("invalid sort error = %q", invalidSortErr.String())
	}

	invalidLimit := NewRootCmd()
	invalidLimitOut, invalidLimitErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidLimit.SetOut(invalidLimitOut)
	invalidLimit.SetErr(invalidLimitErr)
	invalidLimit.SetArgs([]string{"pipeline", "resume-plan", "ticket_to_pr", "--repo", root, "--limit", "-1"})
	if err := invalidLimit.Execute(); err == nil {
		t.Fatalf("pipeline resume-plan accepted invalid limit: stdout=%s", invalidLimitOut.String())
	}
	if !strings.Contains(invalidLimitErr.String(), "--limit must be >= 0") {
		t.Fatalf("invalid limit error = %q", invalidLimitErr.String())
	}
}

func TestPipelineSendScopesRecipients(t *testing.T) {
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

[pipelines.ops_review]
trigger.event = "ops.created"

[[pipelines.ops_review.steps]]
id = "audit"
target = "worker"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-960",
			Ticket:    "SQU-960",
			Target:    "worker",
			Kickoff:   "pipeline send",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-960",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "squ-961",
			Ticket:    "SQU-961",
			Target:    "worker",
			Kickoff:   "stopped pipeline recipient",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-961",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "squ-962",
			Ticket:    "SQU-962",
			Target:    "worker",
			Kickoff:   "foreign pipeline",
			Pipeline:  "ops_review",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-962",
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager-squ-960", Job: "squ-960", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(time.Minute), Workspace: root},
		{Instance: "worker-squ-960", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(2 * time.Minute), Workspace: root},
		{Instance: "worker-squ-961", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusStopped, PID: os.Getpid(), StartedAt: now.Add(3 * time.Minute), Workspace: root},
		{Instance: "worker-squ-962", Job: "squ-962", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(4 * time.Minute), Workspace: root},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"pipeline", "send", "ticket_to_pr", "--repo", root, "--dry-run", "--json", "hello", "pipeline"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("pipeline send dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryRows []sendJSON
	if err := json.Unmarshal(dryOut.Bytes(), &dryRows); err != nil {
		t.Fatalf("decode pipeline send dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if got := sendTargets(dryRows); strings.Join(got, ",") != "manager-squ-960,worker-squ-960" {
		t.Fatalf("pipeline send dry-run targets = %v", got)
	}

	codex := NewRootCmd()
	codexOut, codexErr := &bytes.Buffer{}, &bytes.Buffer{}
	codex.SetOut(codexOut)
	codex.SetErr(codexErr)
	codex.SetArgs([]string{"pipeline", "send", "ticket_to_pr", "--repo", root, "--runtime", "codex", "--dry-run", "--json", "hello"})
	if err := codex.Execute(); err != nil {
		t.Fatalf("pipeline send --runtime dry-run: %v\nstderr=%s", err, codexErr.String())
	}
	var codexRows []sendJSON
	if err := json.Unmarshal(codexOut.Bytes(), &codexRows); err != nil {
		t.Fatalf("decode pipeline send --runtime: %v\nbody=%s", err, codexOut.String())
	}
	if got := sendTargets(codexRows); strings.Join(got, ",") != "worker-squ-960" {
		t.Fatalf("pipeline send --runtime targets = %v", got)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"pipeline", "send", "ticket_to_pr", "--repo", root, "--from", "ops", "--runtime", "codex", "--message", "hello", "--dry-run", "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("pipeline send dry-run commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "pipeline", "send", "ticket_to_pr", "--repo", root, "--from", "ops", "--message", "hello", "--runtime", "codex"}), " ")
	if got := strings.TrimSpace(commandsOut.String()); got != wantCommand {
		t.Fatalf("pipeline send dry-run commands = %q, want %q", got, wantCommand)
	}

	noRecipients := NewRootCmd()
	noRecipientsOut, noRecipientsErr := &bytes.Buffer{}, &bytes.Buffer{}
	noRecipients.SetOut(noRecipientsOut)
	noRecipients.SetErr(noRecipientsErr)
	noRecipients.SetArgs([]string{"pipeline", "send", "ticket_to_pr", "--repo", root, "--status", "crashed", "--dry-run", "--commands", "hello"})
	if err := noRecipients.Execute(); err != nil {
		t.Fatalf("pipeline send no-recipient dry-run commands: %v\nstderr=%s", err, noRecipientsErr.String())
	}
	if got := strings.TrimSpace(noRecipientsOut.String()); got != "" {
		t.Fatalf("pipeline send no-recipient dry-run commands = %q, want empty", got)
	}

	allStatuses := NewRootCmd()
	allOut, allErr := &bytes.Buffer{}, &bytes.Buffer{}
	allStatuses.SetOut(allOut)
	allStatuses.SetErr(allErr)
	allStatuses.SetArgs([]string{"pipeline", "send", "ticket_to_pr", "--repo", root, "--all", "--dry-run", "--json", "hello"})
	if err := allStatuses.Execute(); err != nil {
		t.Fatalf("pipeline send --all dry-run: %v\nstderr=%s", err, allErr.String())
	}
	var allRows []sendJSON
	if err := json.Unmarshal(allOut.Bytes(), &allRows); err != nil {
		t.Fatalf("decode pipeline send --all: %v\nbody=%s", err, allOut.String())
	}
	if got := sendTargets(allRows); strings.Join(got, ",") != "manager-squ-960,worker-squ-960,worker-squ-961" {
		t.Fatalf("pipeline send --all targets = %v", got)
	}

	send := NewRootCmd()
	sendOut, sendErr := &bytes.Buffer{}, &bytes.Buffer{}
	send.SetOut(sendOut)
	send.SetErr(sendErr)
	send.SetArgs([]string{"pipeline", "send", "ticket_to_pr", "--repo", root, "--from", "operator", "please", "sync"})
	if err := send.Execute(); err != nil {
		t.Fatalf("pipeline send: %v\nstderr=%s", err, sendErr.String())
	}
	for _, instance := range []string{"manager-squ-960", "worker-squ-960"} {
		messages, err := daemon.ReadMessages(daemon.DaemonRoot(teamDir), instance)
		if err != nil {
			t.Fatalf("read messages %s: %v", instance, err)
		}
		if len(messages) != 1 || messages[0].From != "operator" || messages[0].Body != "please sync" {
			t.Fatalf("messages %s = %+v", instance, messages)
		}
	}
	for _, instance := range []string{"worker-squ-961", "worker-squ-962"} {
		messages, err := daemon.ReadMessages(daemon.DaemonRoot(teamDir), instance)
		if err != nil {
			t.Fatalf("read messages %s: %v", instance, err)
		}
		if len(messages) != 0 {
			t.Fatalf("unexpected messages %s = %+v", instance, messages)
		}
	}
}

func TestPipelinePsScopesRows(t *testing.T) {
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

[pipelines.ops_review]
trigger.event = "ops.created"

[[pipelines.ops_review.steps]]
id = "audit"
target = "worker"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-994",
			Ticket:    "SQU-994",
			Target:    "worker",
			Kickoff:   "pipeline ps",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-994",
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "review", Target: "manager", Status: job.StatusBlocked, Instance: "reviewer-squ-994-review", StartedAt: now.Add(2 * time.Minute)},
			},
		},
		{
			ID:        "squ-995",
			Ticket:    "SQU-995",
			Target:    "worker",
			Kickoff:   "foreign pipeline",
			Pipeline:  "ops_review",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-995",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "squ-996",
			Ticket:    "SQU-996",
			Target:    "worker",
			Kickoff:   "ad hoc ps",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-996",
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager-squ-994", Job: "squ-994", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(time.Minute)},
		{Instance: "worker-squ-994", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(2 * time.Minute)},
		{Instance: "reviewer-squ-994-review", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusStopped, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(3 * time.Minute), StoppedAt: now.Add(4 * time.Minute)},
		{Instance: "worker-squ-995", Job: "squ-995", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(5 * time.Minute)},
		{Instance: "worker-squ-996", Job: "squ-996", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(6 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	one := NewRootCmd()
	oneOut, oneErr := &bytes.Buffer{}, &bytes.Buffer{}
	one.SetOut(oneOut)
	one.SetErr(oneErr)
	one.SetArgs([]string{"pipeline", "ps", "ticket_to_pr", "--repo", root, "--json"})
	if err := one.Execute(); err != nil {
		t.Fatalf("pipeline ps json: %v\nstderr=%s", err, oneErr.String())
	}
	var rows []psJSONRow
	if err := json.Unmarshal(oneOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline ps: %v\nbody=%s", err, oneOut.String())
	}
	if got := psJSONRowNames(rows); strings.Join(got, ",") != "manager-squ-994,reviewer-squ-994-review,worker-squ-994" {
		t.Fatalf("pipeline ps rows = %v", got)
	}

	running := NewRootCmd()
	runningOut, runningErr := &bytes.Buffer{}, &bytes.Buffer{}
	running.SetOut(runningOut)
	running.SetErr(runningErr)
	running.SetArgs([]string{"pipeline", "ps", "ticket_to_pr", "--repo", root, "--status", "running", "--json"})
	if err := running.Execute(); err != nil {
		t.Fatalf("pipeline ps running: %v\nstderr=%s", err, runningErr.String())
	}
	rows = nil
	if err := json.Unmarshal(runningOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline ps running: %v\nbody=%s", err, runningOut.String())
	}
	if got := psJSONRowNames(rows); strings.Join(got, ",") != "manager-squ-994,worker-squ-994" {
		t.Fatalf("pipeline ps running rows = %v", got)
	}

	allCodex := NewRootCmd()
	allCodexOut, allCodexErr := &bytes.Buffer{}, &bytes.Buffer{}
	allCodex.SetOut(allCodexOut)
	allCodex.SetErr(allCodexErr)
	allCodex.SetArgs([]string{"pipeline", "ps", "--all", "--repo", root, "--runtime", "codex", "--format", "{{.Instance}}"})
	if err := allCodex.Execute(); err != nil {
		t.Fatalf("pipeline ps all runtime format: %v\nstderr=%s", err, allCodexErr.String())
	}
	if got, want := strings.TrimSpace(allCodexOut.String()), "worker-squ-994\nworker-squ-995"; got != want {
		t.Fatalf("pipeline ps all runtime format = %q, want %q", got, want)
	}

	quiet := NewRootCmd()
	quietOut, quietErr := &bytes.Buffer{}, &bytes.Buffer{}
	quiet.SetOut(quietOut)
	quiet.SetErr(quietErr)
	quiet.SetArgs([]string{"pipeline", "ps", "ticket_to_pr", "--repo", root, "--quiet", "--runtime", "codex"})
	if err := quiet.Execute(); err != nil {
		t.Fatalf("pipeline ps quiet: %v\nstderr=%s", err, quietErr.String())
	}
	if got := strings.TrimSpace(quietOut.String()); got != "worker-squ-994" {
		t.Fatalf("pipeline ps quiet = %q", got)
	}

	summary := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(summaryOut)
	summary.SetErr(summaryErr)
	summary.SetArgs([]string{"pipeline", "ps", "ticket_to_pr", "--repo", root, "--summary", "--json"})
	if err := summary.Execute(); err != nil {
		t.Fatalf("pipeline ps summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var counts psSummaryJSON
	if err := json.Unmarshal(summaryOut.Bytes(), &counts); err != nil {
		t.Fatalf("decode pipeline ps summary: %v\nbody=%s", err, summaryOut.String())
	}
	if counts.Total != 3 || counts.Running != 2 || counts.Stopped != 1 {
		t.Fatalf("pipeline ps summary = %+v", counts)
	}

	invalidMany := NewRootCmd()
	invalidManyOut, invalidManyErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidMany.SetOut(invalidManyOut)
	invalidMany.SetErr(invalidManyErr)
	invalidMany.SetArgs([]string{"pipeline", "ps", "ticket_to_pr", "ops_review", "--repo", root})
	if err := invalidMany.Execute(); err == nil {
		t.Fatalf("pipeline ps accepted multiple pipeline names: stdout=%s", invalidManyOut.String())
	}
	if !strings.Contains(invalidManyErr.String(), "pass at most one pipeline name") {
		t.Fatalf("multiple pipeline error = %q", invalidManyErr.String())
	}

	invalidAll := NewRootCmd()
	invalidAllOut, invalidAllErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidAll.SetOut(invalidAllOut)
	invalidAll.SetErr(invalidAllErr)
	invalidAll.SetArgs([]string{"pipeline", "ps", "ticket_to_pr", "--all", "--repo", root})
	if err := invalidAll.Execute(); err == nil {
		t.Fatalf("pipeline ps accepted --all with pipeline: stdout=%s", invalidAllOut.String())
	}
	if !strings.Contains(invalidAllErr.String(), "--all cannot be combined with a pipeline argument") {
		t.Fatalf("--all conflict error = %q", invalidAllErr.String())
	}
}

func TestPipelineStatsScopesRowsAndSummary(t *testing.T) {
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

[pipelines.ops_review]
trigger.event = "ops.created"

[[pipelines.ops_review.steps]]
id = "audit"
target = "worker"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-990",
			Ticket:    "SQU-990",
			Target:    "worker",
			Kickoff:   "pipeline stats",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-990",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "squ-991",
			Ticket:    "SQU-991",
			Target:    "worker",
			Kickoff:   "stopped pipeline stats",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-991",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "squ-992",
			Ticket:    "SQU-992",
			Target:    "worker",
			Kickoff:   "foreign pipeline",
			Pipeline:  "ops_review",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-992",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "squ-993",
			Ticket:    "SQU-993",
			Target:    "worker",
			Kickoff:   "ad hoc stats",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-993",
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager-squ-990", Job: "squ-990", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(time.Minute)},
		{Instance: "worker-squ-990", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(2 * time.Minute)},
		{Instance: "worker-squ-991", Job: "squ-991", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusStopped, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(3 * time.Minute), StoppedAt: now.Add(4 * time.Minute)},
		{Instance: "worker-squ-992", Job: "squ-992", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(5 * time.Minute)},
		{Instance: "worker-squ-993", Job: "squ-993", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(6 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	one := NewRootCmd()
	oneOut, oneErr := &bytes.Buffer{}, &bytes.Buffer{}
	one.SetOut(oneOut)
	one.SetErr(oneErr)
	one.SetArgs([]string{"pipeline", "stats", "ticket_to_pr", "--repo", root, "--json"})
	if err := one.Execute(); err != nil {
		t.Fatalf("pipeline stats json: %v\nstderr=%s", err, oneErr.String())
	}
	var rows []statsJSONRow
	if err := json.Unmarshal(oneOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline stats: %v\nbody=%s", err, oneOut.String())
	}
	if got := statsJSONRowNames(rows); strings.Join(got, ",") != "manager-squ-990,worker-squ-990" {
		t.Fatalf("pipeline stats rows = %v", got)
	}

	all := NewRootCmd()
	allOut, allErr := &bytes.Buffer{}, &bytes.Buffer{}
	all.SetOut(allOut)
	all.SetErr(allErr)
	all.SetArgs([]string{"pipeline", "stats", "--repo", root, "--json"})
	if err := all.Execute(); err != nil {
		t.Fatalf("pipeline stats all json: %v\nstderr=%s", err, allErr.String())
	}
	rows = nil
	if err := json.Unmarshal(allOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline stats all: %v\nbody=%s", err, allOut.String())
	}
	if got := statsJSONRowNames(rows); strings.Join(got, ",") != "manager-squ-990,worker-squ-990,worker-squ-992" {
		t.Fatalf("pipeline all stats rows = %v, want every running pipeline-owned instance only", got)
	}

	codex := NewRootCmd()
	codexOut, codexErr := &bytes.Buffer{}, &bytes.Buffer{}
	codex.SetOut(codexOut)
	codex.SetErr(codexErr)
	codex.SetArgs([]string{"pipeline", "stats", "--all", "--repo", root, "--runtime", "codex", "--format", "{{.Instance}}"})
	if err := codex.Execute(); err != nil {
		t.Fatalf("pipeline stats --all runtime format: %v\nstderr=%s", err, codexErr.String())
	}
	if got, want := strings.TrimSpace(codexOut.String()), "worker-squ-990\nworker-squ-992"; got != want {
		t.Fatalf("pipeline stats --all runtime format = %q, want %q", got, want)
	}

	top := NewRootCmd()
	topOut, topErr := &bytes.Buffer{}, &bytes.Buffer{}
	top.SetOut(topOut)
	top.SetErr(topErr)
	top.SetArgs([]string{"pipeline", "top", "--all", "--repo", root, "--runtime", "codex", "--format", "{{.Instance}}"})
	if err := top.Execute(); err != nil {
		t.Fatalf("pipeline top alias: %v\nstderr=%s", err, topErr.String())
	}
	if got, want := strings.TrimSpace(topOut.String()), "worker-squ-990\nworker-squ-992"; got != want {
		t.Fatalf("pipeline top alias output = %q, want %q", got, want)
	}

	stopped := NewRootCmd()
	stoppedOut, stoppedErr := &bytes.Buffer{}, &bytes.Buffer{}
	stopped.SetOut(stoppedOut)
	stopped.SetErr(stoppedErr)
	stopped.SetArgs([]string{"pipeline", "stats", "ticket_to_pr", "--repo", root, "--status", "stopped", "--json"})
	if err := stopped.Execute(); err != nil {
		t.Fatalf("pipeline stats stopped json: %v\nstderr=%s", err, stoppedErr.String())
	}
	rows = nil
	if err := json.Unmarshal(stoppedOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline stats stopped: %v\nbody=%s", err, stoppedOut.String())
	}
	if got := statsJSONRowNames(rows); strings.Join(got, ",") != "worker-squ-991" {
		t.Fatalf("pipeline stopped stats rows = %v", got)
	}

	summary := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(summaryOut)
	summary.SetErr(summaryErr)
	summary.SetArgs([]string{"pipeline", "stats", "ticket_to_pr", "--repo", root, "--summary", "--json"})
	if err := summary.Execute(); err != nil {
		t.Fatalf("pipeline stats summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var statsSummary statsSummaryJSON
	if err := json.Unmarshal(summaryOut.Bytes(), &statsSummary); err != nil {
		t.Fatalf("decode pipeline stats summary: %v\nbody=%s", err, summaryOut.String())
	}
	if statsSummary.Total != 2 || statsSummary.Running != 2 {
		t.Fatalf("pipeline stats summary = %+v", statsSummary)
	}

	invalidMany := NewRootCmd()
	invalidManyOut, invalidManyErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidMany.SetOut(invalidManyOut)
	invalidMany.SetErr(invalidManyErr)
	invalidMany.SetArgs([]string{"pipeline", "stats", "ticket_to_pr", "ops_review", "--repo", root})
	if err := invalidMany.Execute(); err == nil {
		t.Fatalf("pipeline stats accepted multiple pipeline names: stdout=%s", invalidManyOut.String())
	}
	if !strings.Contains(invalidManyErr.String(), "pass at most one pipeline name") {
		t.Fatalf("multiple pipeline error = %q", invalidManyErr.String())
	}

	invalidAll := NewRootCmd()
	invalidAllOut, invalidAllErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidAll.SetOut(invalidAllOut)
	invalidAll.SetErr(invalidAllErr)
	invalidAll.SetArgs([]string{"pipeline", "stats", "ticket_to_pr", "--all", "--repo", root})
	if err := invalidAll.Execute(); err == nil {
		t.Fatalf("pipeline stats accepted --all with pipeline: stdout=%s", invalidAllOut.String())
	}
	if !strings.Contains(invalidAllErr.String(), "--all cannot be combined with a pipeline argument") {
		t.Fatalf("--all conflict error = %q", invalidAllErr.String())
	}
}

func TestPipelineLogsScopesRowsAndStreams(t *testing.T) {
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

[pipelines.ops_review]
trigger.event = "ops.created"

[[pipelines.ops_review.steps]]
id = "audit"
target = "worker"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-980",
			Ticket:    "SQU-980",
			Target:    "worker",
			Kickoff:   "pipeline logs",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-980",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "squ-981",
			Ticket:    "SQU-981",
			Target:    "worker",
			Kickoff:   "foreign pipeline",
			Pipeline:  "ops_review",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-981",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "squ-982",
			Ticket:    "SQU-982",
			Target:    "worker",
			Kickoff:   "ad hoc logs",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-982",
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}
	daemonRoot := daemon.DaemonRoot(teamDir)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager-squ-980", Job: "squ-980", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(time.Minute)},
		{Instance: "worker-squ-980", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(2 * time.Minute)},
		{Instance: "worker-squ-981", Job: "squ-981", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(3 * time.Minute)},
		{Instance: "worker-squ-982", Job: "squ-982", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(4 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(daemonRoot, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeChildLogForTest(t, daemonRoot, "manager-squ-980", "manager first\nmanager latest\n")
	writeChildLogForTest(t, daemonRoot, "worker-squ-980", "worker first\nworker latest\n")
	writeChildLogForTest(t, daemonRoot, "worker-squ-981", "foreign first\nforeign latest\n")
	writeChildLogForTest(t, daemonRoot, "worker-squ-982", "adhoc first\nadhoc latest\n")
	writeLastMessageForTest(t, teamDir, "manager-squ-980", "manager final")
	writeLastMessageForTest(t, teamDir, "worker-squ-980", "worker final")
	writeLastMessageForTest(t, teamDir, "worker-squ-981", "foreign final")
	writeLastMessageForTest(t, teamDir, "worker-squ-982", "adhoc final")

	list := NewRootCmd()
	listOut, listErr := &bytes.Buffer{}, &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetErr(listErr)
	list.SetArgs([]string{"pipeline", "logs", "ticket_to_pr", "--repo", root, "--list", "--json"})
	if err := list.Execute(); err != nil {
		t.Fatalf("pipeline logs list: %v\nstderr=%s", err, listErr.String())
	}
	var rows []logListRow
	if err := json.Unmarshal(listOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline logs list: %v\nbody=%s", err, listOut.String())
	}
	if got := logRowInstances(rows); strings.Join(got, ",") != "manager-squ-980,worker-squ-980" {
		t.Fatalf("pipeline log rows = %v", got)
	}

	allList := NewRootCmd()
	allListOut, allListErr := &bytes.Buffer{}, &bytes.Buffer{}
	allList.SetOut(allListOut)
	allList.SetErr(allListErr)
	allList.SetArgs([]string{"pipeline", "logs", "--repo", root, "--list", "--json"})
	if err := allList.Execute(); err != nil {
		t.Fatalf("pipeline logs all list: %v\nstderr=%s", err, allListErr.String())
	}
	rows = nil
	if err := json.Unmarshal(allListOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline all logs list: %v\nbody=%s", err, allListOut.String())
	}
	if got := logRowInstances(rows); strings.Join(got, ",") != "manager-squ-980,worker-squ-980,worker-squ-981" {
		t.Fatalf("pipeline all log rows = %v, want every pipeline-owned stream only", got)
	}

	codexList := NewRootCmd()
	codexListOut, codexListErr := &bytes.Buffer{}, &bytes.Buffer{}
	codexList.SetOut(codexListOut)
	codexList.SetErr(codexListErr)
	codexList.SetArgs([]string{"pipeline", "logs", "ticket_to_pr", "--repo", root, "--runtime", "codex", "--list", "--json"})
	if err := codexList.Execute(); err != nil {
		t.Fatalf("pipeline logs runtime list: %v\nstderr=%s", err, codexListErr.String())
	}
	rows = nil
	if err := json.Unmarshal(codexListOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline runtime logs list: %v\nbody=%s", err, codexListOut.String())
	}
	if got := logRowInstances(rows); strings.Join(got, ",") != "worker-squ-980" {
		t.Fatalf("pipeline runtime log rows = %v", got)
	}

	allCodexList := NewRootCmd()
	allCodexListOut, allCodexListErr := &bytes.Buffer{}, &bytes.Buffer{}
	allCodexList.SetOut(allCodexListOut)
	allCodexList.SetErr(allCodexListErr)
	allCodexList.SetArgs([]string{"pipeline", "logs", "--all", "--repo", root, "--runtime", "codex", "--list", "--format", "{{.Instance}}"})
	if err := allCodexList.Execute(); err != nil {
		t.Fatalf("pipeline logs --all runtime format: %v\nstderr=%s", err, allCodexListErr.String())
	}
	if got, want := strings.TrimSpace(allCodexListOut.String()), "worker-squ-980\nworker-squ-981"; got != want {
		t.Fatalf("pipeline logs --all runtime format = %q, want %q", got, want)
	}

	logs := NewRootCmd()
	logsOut, logsErr := &bytes.Buffer{}, &bytes.Buffer{}
	logs.SetOut(logsOut)
	logs.SetErr(logsErr)
	logs.SetArgs([]string{"pipeline", "logs", "ticket_to_pr", "--repo", root, "--tail", "1"})
	if err := logs.Execute(); err != nil {
		t.Fatalf("pipeline logs: %v\nstderr=%s", err, logsErr.String())
	}
	body := logsOut.String()
	for _, want := range []string{"manager-squ-980      | manager latest", "worker-squ-980       | worker latest"} {
		if !strings.Contains(body, want) {
			t.Fatalf("pipeline logs missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "foreign latest") {
		t.Fatalf("pipeline logs leaked unrelated content:\n%s", body)
	}

	runtimeLogs := NewRootCmd()
	runtimeLogsOut, runtimeLogsErr := &bytes.Buffer{}, &bytes.Buffer{}
	runtimeLogs.SetOut(runtimeLogsOut)
	runtimeLogs.SetErr(runtimeLogsErr)
	runtimeLogs.SetArgs([]string{"pipeline", "logs", "ticket_to_pr", "--repo", root, "--runtime", "codex", "--tail", "1"})
	if err := runtimeLogs.Execute(); err != nil {
		t.Fatalf("pipeline logs runtime: %v\nstderr=%s", err, runtimeLogsErr.String())
	}
	if got := runtimeLogsOut.String(); got != "worker latest\n" {
		t.Fatalf("pipeline logs runtime = %q", got)
	}

	lastMessages := NewRootCmd()
	lastOut, lastErr := &bytes.Buffer{}, &bytes.Buffer{}
	lastMessages.SetOut(lastOut)
	lastMessages.SetErr(lastErr)
	lastMessages.SetArgs([]string{"pipeline", "logs", "ticket_to_pr", "--repo", root, "--last-message"})
	if err := lastMessages.Execute(); err != nil {
		t.Fatalf("pipeline logs last-message: %v\nstderr=%s", err, lastErr.String())
	}
	lastBody := lastOut.String()
	for _, want := range []string{"manager-squ-980      | manager final", "worker-squ-980       | worker final"} {
		if !strings.Contains(lastBody, want) {
			t.Fatalf("pipeline last-message missing %q:\n%s", want, lastBody)
		}
	}
	if strings.Contains(lastBody, "foreign final") {
		t.Fatalf("pipeline last-message leaked unrelated content:\n%s", lastBody)
	}

	invalidMany := NewRootCmd()
	invalidManyOut, invalidManyErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidMany.SetOut(invalidManyOut)
	invalidMany.SetErr(invalidManyErr)
	invalidMany.SetArgs([]string{"pipeline", "logs", "ticket_to_pr", "ops_review", "--repo", root})
	if err := invalidMany.Execute(); err == nil {
		t.Fatalf("pipeline logs accepted multiple pipeline names: stdout=%s", invalidManyOut.String())
	}
	if !strings.Contains(invalidManyErr.String(), "pass at most one pipeline name") {
		t.Fatalf("multiple pipeline error = %q", invalidManyErr.String())
	}

	invalidAll := NewRootCmd()
	invalidAllOut, invalidAllErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidAll.SetOut(invalidAllOut)
	invalidAll.SetErr(invalidAllErr)
	invalidAll.SetArgs([]string{"pipeline", "logs", "ticket_to_pr", "--all", "--repo", root})
	if err := invalidAll.Execute(); err == nil {
		t.Fatalf("pipeline logs accepted --all with pipeline: stdout=%s", invalidAllOut.String())
	}
	if !strings.Contains(invalidAllErr.String(), "--all cannot be combined with a pipeline argument") {
		t.Fatalf("--all conflict error = %q", invalidAllErr.String())
	}
}

func TestPipelineSendRuntimeStaleScopesRecipients(t *testing.T) {
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
id = "audit"
target = "worker"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-970",
			Ticket:    "SQU-970",
			Target:    "worker",
			Kickoff:   "pipeline runtime stale",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-970",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "squ-971",
			Ticket:    "SQU-971",
			Target:    "worker",
			Kickoff:   "pipeline fresh",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-971",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "squ-972",
			Ticket:    "SQU-972",
			Target:    "worker",
			Kickoff:   "foreign pipeline",
			Pipeline:  "ops_review",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-972",
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}
	for _, meta := range []*daemon.Metadata{
		{Instance: "worker-squ-970", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: 99999999, StartedAt: now.Add(time.Minute), Workspace: root},
		{Instance: "worker-squ-971", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(2 * time.Minute), Workspace: root},
		{Instance: "worker-squ-972", Job: "squ-972", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: 99999999, StartedAt: now.Add(3 * time.Minute), Workspace: root},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "send", "ticket_to_pr", "--repo", root, "--runtime-stale", "--dry-run", "--json", "hello"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline send --runtime-stale dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var rows []sendJSON
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline send --runtime-stale: %v\nbody=%s", err, out.String())
	}
	if got := sendTargets(rows); strings.Join(got, ",") != "worker-squ-970" {
		t.Fatalf("pipeline send --runtime-stale targets = %v", got)
	}
}

func TestScopedSendCommandValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"pipeline", "send", "ticket_to_pr", "--commands"}, "pipeline send: --commands requires --dry-run"},
		{[]string{"pipeline", "send", "ticket_to_pr", "--dry-run", "--commands", "--json"}, "pipeline send: --commands cannot be combined with --json"},
		{[]string{"pipeline", "send", "ticket_to_pr", "--dry-run", "--commands", "--format", "{{.To}}"}, "pipeline send: --commands cannot be combined with --format"},
		{[]string{"team", "send", "delivery", "--commands"}, "team send: --commands requires --dry-run"},
		{[]string{"team", "send", "delivery", "--dry-run", "--commands", "--json"}, "team send: --commands cannot be combined with --json"},
		{[]string{"team", "send", "delivery", "--dry-run", "--commands", "--format", "{{.To}}"}, "team send: --commands cannot be combined with --format"},
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

func TestPipelineEventsScopesLifecycleEvents(t *testing.T) {
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

[pipelines.ops_review]
trigger.event = "ops.created"

[[pipelines.ops_review.steps]]
id = "audit"
target = "worker"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	for _, j := range []*job.Job{
		{
			ID:        "squ-995",
			Ticket:    "SQU-995",
			Target:    "worker",
			Kickoff:   "pipeline events",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-995",
			CreatedAt: base,
			UpdatedAt: base,
		},
		{
			ID:        "squ-996",
			Ticket:    "SQU-996",
			Target:    "worker",
			Kickoff:   "foreign pipeline",
			Pipeline:  "ops_review",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-996",
			CreatedAt: base,
			UpdatedAt: base,
		},
		{
			ID:        "squ-997",
			Ticket:    "SQU-997",
			Target:    "worker",
			Kickoff:   "ad hoc events",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-997",
			CreatedAt: base,
			UpdatedAt: base,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}
	daemonRoot := daemon.DaemonRoot(teamDir)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager-squ-995", Job: "squ-995", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: base},
		{Instance: "worker-squ-995", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusStopped, PID: os.Getpid(), Workspace: root, StartedAt: base.Add(2 * time.Minute), StoppedAt: base.Add(4 * time.Minute)},
		{Instance: "worker-squ-996", Job: "squ-996", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusStopped, PID: os.Getpid(), Workspace: root, StartedAt: base.Add(time.Minute), StoppedAt: base.Add(time.Minute)},
		{Instance: "worker-squ-997", Job: "squ-997", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusStopped, PID: os.Getpid(), Workspace: root, StartedAt: base.Add(3 * time.Minute), StoppedAt: base.Add(3 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(daemonRoot, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	for _, ev := range []*daemon.LifecycleEvent{
		{TS: base, Action: "start", Instance: "manager-squ-995", Agent: "manager", Status: daemon.StatusRunning, Message: "manager up"},
		{TS: base.Add(time.Minute), Action: "stop", Instance: "worker-squ-996", Agent: "worker", Status: daemon.StatusStopped, Message: "foreign stop"},
		{TS: base.Add(2 * time.Minute), Action: "dispatch", Instance: "worker-squ-995", Agent: "worker", Status: daemon.StatusRunning, Message: "pipeline worker"},
		{TS: base.Add(3 * time.Minute), Action: "stop", Instance: "worker-squ-997", Agent: "worker", Status: daemon.StatusStopped, Message: "adhoc stop"},
		{TS: base.Add(4 * time.Minute), Action: "stop", Instance: "worker-squ-995", Agent: "worker", Status: daemon.StatusStopped, Message: "pipeline done"},
	} {
		if err := daemon.AppendLifecycleEvent(daemonRoot, ev); err != nil {
			t.Fatalf("append event: %v", err)
		}
	}

	list := NewRootCmd()
	listOut, listErr := &bytes.Buffer{}, &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetErr(listErr)
	list.SetArgs([]string{"pipeline", "events", "ticket_to_pr", "--repo", root, "--json"})
	if err := list.Execute(); err != nil {
		t.Fatalf("pipeline events json: %v\nstderr=%s", err, listErr.String())
	}
	events := decodeLifecycleEventJSONL(t, listOut.String())
	if got := lifecycleEventInstances(events); strings.Join(got, ",") != "manager-squ-995,worker-squ-995,worker-squ-995" {
		t.Fatalf("pipeline events instances = %v\nbody=%s", got, listOut.String())
	}
	if strings.Contains(listOut.String(), "foreign stop") {
		t.Fatalf("pipeline events leaked unrelated event:\n%s", listOut.String())
	}

	allEvents := NewRootCmd()
	allOut, allErr := &bytes.Buffer{}, &bytes.Buffer{}
	allEvents.SetOut(allOut)
	allEvents.SetErr(allErr)
	allEvents.SetArgs([]string{"pipeline", "events", "--repo", root, "--json"})
	if err := allEvents.Execute(); err != nil {
		t.Fatalf("pipeline events all json: %v\nstderr=%s", err, allErr.String())
	}
	events = decodeLifecycleEventJSONL(t, allOut.String())
	if got := lifecycleEventInstances(events); strings.Join(got, ",") != "manager-squ-995,worker-squ-996,worker-squ-995,worker-squ-995" {
		t.Fatalf("pipeline all events instances = %v\nbody=%s", got, allOut.String())
	}
	if strings.Contains(allOut.String(), "adhoc stop") {
		t.Fatalf("pipeline all events leaked ad hoc event:\n%s", allOut.String())
	}

	formatted := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatted.SetOut(formatOut)
	formatted.SetErr(formatErr)
	formatted.SetArgs([]string{"pipeline", "events", "ticket_to_pr", "--repo", root, "--tail", "1", "--format", "{{.Instance}} {{.Action}}"})
	if err := formatted.Execute(); err != nil {
		t.Fatalf("pipeline events format: %v\nstderr=%s", err, formatErr.String())
	}
	if got := strings.TrimSpace(formatOut.String()); got != "worker-squ-995 stop" {
		t.Fatalf("pipeline events tail format = %q", got)
	}

	summary := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(summaryOut)
	summary.SetErr(summaryErr)
	summary.SetArgs([]string{"pipeline", "events", "ticket_to_pr", "--repo", root, "--summary", "--action", "stop", "--json"})
	if err := summary.Execute(); err != nil {
		t.Fatalf("pipeline events summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var eventSummary eventSummaryJSON
	if err := json.Unmarshal(summaryOut.Bytes(), &eventSummary); err != nil {
		t.Fatalf("decode pipeline events summary: %v\nbody=%s", err, summaryOut.String())
	}
	if eventSummary.Total != 1 || eventSummary.Actions["stop"] != 1 || eventSummary.Instances["worker-squ-995"] != 1 {
		t.Fatalf("pipeline events summary = %+v", eventSummary)
	}

	allSummary := NewRootCmd()
	allSummaryOut, allSummaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	allSummary.SetOut(allSummaryOut)
	allSummary.SetErr(allSummaryErr)
	allSummary.SetArgs([]string{"pipeline", "events", "--all", "--repo", root, "--summary", "--action", "stop", "--json"})
	if err := allSummary.Execute(); err != nil {
		t.Fatalf("pipeline events --all summary: %v\nstderr=%s", err, allSummaryErr.String())
	}
	var allEventSummary eventSummaryJSON
	if err := json.Unmarshal(allSummaryOut.Bytes(), &allEventSummary); err != nil {
		t.Fatalf("decode pipeline events --all summary: %v\nbody=%s", err, allSummaryOut.String())
	}
	if allEventSummary.Total != 2 || allEventSummary.Actions["stop"] != 2 || allEventSummary.Instances["worker-squ-995"] != 1 || allEventSummary.Instances["worker-squ-996"] != 1 {
		t.Fatalf("pipeline events --all summary = %+v", allEventSummary)
	}

	codex := NewRootCmd()
	codexOut, codexErr := &bytes.Buffer{}, &bytes.Buffer{}
	codex.SetOut(codexOut)
	codex.SetErr(codexErr)
	codex.SetArgs([]string{"pipeline", "events", "ticket_to_pr", "--repo", root, "--runtime", "codex", "--json"})
	if err := codex.Execute(); err != nil {
		t.Fatalf("pipeline events runtime: %v\nstderr=%s", err, codexErr.String())
	}
	events = decodeLifecycleEventJSONL(t, codexOut.String())
	if got := lifecycleEventInstances(events); strings.Join(got, ",") != "worker-squ-995,worker-squ-995" {
		t.Fatalf("pipeline events runtime instances = %v\nbody=%s", got, codexOut.String())
	}
	if strings.Contains(codexOut.String(), "manager up") || strings.Contains(codexOut.String(), "foreign stop") {
		t.Fatalf("pipeline events runtime leaked unrelated event:\n%s", codexOut.String())
	}

	invalidMany := NewRootCmd()
	invalidManyOut, invalidManyErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidMany.SetOut(invalidManyOut)
	invalidMany.SetErr(invalidManyErr)
	invalidMany.SetArgs([]string{"pipeline", "events", "ticket_to_pr", "ops_review", "--repo", root})
	if err := invalidMany.Execute(); err == nil {
		t.Fatalf("pipeline events accepted multiple pipeline names: stdout=%s", invalidManyOut.String())
	}
	if !strings.Contains(invalidManyErr.String(), "pass at most one pipeline name") {
		t.Fatalf("multiple pipeline error = %q", invalidManyErr.String())
	}

	invalidAll := NewRootCmd()
	invalidAllOut, invalidAllErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidAll.SetOut(invalidAllOut)
	invalidAll.SetErr(invalidAllErr)
	invalidAll.SetArgs([]string{"pipeline", "events", "ticket_to_pr", "--all", "--repo", root})
	if err := invalidAll.Execute(); err == nil {
		t.Fatalf("pipeline events accepted --all with pipeline: stdout=%s", invalidAllOut.String())
	}
	if !strings.Contains(invalidAllErr.String(), "--all cannot be combined with a pipeline argument") {
		t.Fatalf("--all conflict error = %q", invalidAllErr.String())
	}
}

func TestPipelineEventsCurrentStateFilters(t *testing.T) {
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
id = "audit"
target = "worker"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-777",
			Ticket:    "SQU-777",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-777",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "ops-777",
			Ticket:    "OPS-777",
			Target:    "worker",
			Pipeline:  "ops_review",
			Status:    job.StatusRunning,
			Instance:  "worker-ops-777",
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}
	daemonRoot := daemon.DaemonRoot(teamDir)
	for _, meta := range []*daemon.Metadata{
		{Instance: "worker-squ-777", Job: "squ-777", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: 99999999, Workspace: root, StartedAt: now.Add(-3 * time.Minute)},
		{Instance: "manager-squ-777", Job: "squ-777", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(-2 * time.Minute)},
		{Instance: "worker-ops-777", Job: "ops-777", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: 99999999, Workspace: root, StartedAt: now.Add(-time.Minute)},
	} {
		if err := daemon.WriteMetadata(daemonRoot, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		if err := daemon.AppendLifecycleEvent(daemonRoot, &daemon.LifecycleEvent{
			TS:       meta.StartedAt,
			Action:   "dispatch",
			Instance: meta.Instance,
			Agent:    meta.Agent,
			Status:   meta.Status,
			Message:  meta.Instance,
		}); err != nil {
			t.Fatalf("append event %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "worker-squ-777"), `
[status]
phase = "blocked"
description = "blocked implementation"
`, now)
	writeStatus(t, filepath.Join(teamDir, "state", "manager-squ-777"), `
[status]
phase = "idle"
description = "idle review"
`, now)
	writeStatus(t, filepath.Join(teamDir, "state", "worker-ops-777"), `
[status]
phase = "blocked"
description = "foreign pipeline"
`, now)

	phase := NewRootCmd()
	phaseOut, phaseErr := &bytes.Buffer{}, &bytes.Buffer{}
	phase.SetOut(phaseOut)
	phase.SetErr(phaseErr)
	phase.SetArgs([]string{"pipeline", "events", "ticket_to_pr", "--repo", root, "--phase", "blocked", "--format", "{{.Instance}}"})
	if err := phase.Execute(); err != nil {
		t.Fatalf("pipeline events phase filter: %v\nstderr=%s", err, phaseErr.String())
	}
	if got, want := phaseOut.String(), "worker-squ-777\n"; got != want {
		t.Fatalf("pipeline events phase output = %q, want %q", got, want)
	}

	runtimeStale := NewRootCmd()
	runtimeStaleOut, runtimeStaleErr := &bytes.Buffer{}, &bytes.Buffer{}
	runtimeStale.SetOut(runtimeStaleOut)
	runtimeStale.SetErr(runtimeStaleErr)
	runtimeStale.SetArgs([]string{"pipeline", "events", "ticket_to_pr", "--repo", root, "--runtime-stale", "--format", "{{.Instance}}"})
	if err := runtimeStale.Execute(); err != nil {
		t.Fatalf("pipeline events runtime-stale filter: %v\nstderr=%s", err, runtimeStaleErr.String())
	}
	if got, want := runtimeStaleOut.String(), "worker-squ-777\n"; got != want {
		t.Fatalf("pipeline events runtime-stale output = %q, want %q", got, want)
	}
}

func TestPipelineCleanupScopesJobs(t *testing.T) {
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
id = "audit"
target = "worker"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	initGitRepoForJobTest(t, root)
	makeMergedBranch := func(branch string) {
		t.Helper()
		runGitForJobTest(t, root, "checkout", "-b", branch)
		runGitForJobTest(t, root, "checkout", "main")
	}
	deliveryBranch := "worktree-worker-squ-730"
	opsBranch := "worktree-worker-ops-730"
	makeMergedBranch(deliveryBranch)
	makeMergedBranch(opsBranch)
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-730",
			Ticket:    "SQU-730",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusDone,
			Branch:    deliveryBranch,
			PR:        "https://github.com/acme/repo/pull/730",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "ops-730",
			Ticket:    "OPS-730",
			Target:    "worker",
			Pipeline:  "ops_review",
			Status:    job.StatusDone,
			Branch:    opsBranch,
			PR:        "https://github.com/acme/repo/pull/731",
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}

	preview := NewRootCmd()
	previewOut, previewErr := &bytes.Buffer{}, &bytes.Buffer{}
	preview.SetOut(previewOut)
	preview.SetErr(previewErr)
	preview.SetArgs([]string{"pipeline", "cleanup", "ticket_to_pr", "--repo", root, "--dry-run", "--json"})
	if err := preview.Execute(); err != nil {
		t.Fatalf("pipeline cleanup dry-run: %v\nstderr=%s", err, previewErr.String())
	}
	var previewResult jobCleanupBatchResult
	if err := json.Unmarshal(previewOut.Bytes(), &previewResult); err != nil {
		t.Fatalf("decode pipeline cleanup preview: %v\nbody=%s", err, previewOut.String())
	}
	if previewResult.Pipeline != "ticket_to_pr" || !previewResult.DryRun || previewResult.Total != 1 || len(previewResult.Items) != 1 || previewResult.Items[0].JobID != "squ-730" {
		t.Fatalf("pipeline cleanup preview = %+v", previewResult)
	}
	if !branchExists(t, root, deliveryBranch) || !branchExists(t, root, opsBranch) {
		t.Fatalf("dry-run removed a branch")
	}

	previewFormat := NewRootCmd()
	previewFormatOut, previewFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	previewFormat.SetOut(previewFormatOut)
	previewFormat.SetErr(previewFormatErr)
	previewFormat.SetArgs([]string{"pipeline", "cleanup", "ticket_to_pr", "--repo", root, "--dry-run", "--format", "{{.Pipeline}} {{.Total}} {{.Previewed}} {{len .Items}}"})
	if err := previewFormat.Execute(); err != nil {
		t.Fatalf("pipeline cleanup dry-run format: %v\nstderr=%s", err, previewFormatErr.String())
	}
	if got, want := previewFormatOut.String(), "ticket_to_pr 1 1 1\n"; got != want {
		t.Fatalf("pipeline cleanup dry-run format = %q, want %q", got, want)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"pipeline", "cleanup", "ticket_to_pr", "--repo", root, "--dry-run", "--force-branch", "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("pipeline cleanup dry-run commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "pipeline", "cleanup", "ticket_to_pr", "--repo", root, "--merged", "--force-branch"}), " ")
	if got := strings.TrimSpace(commandsOut.String()); got != wantCommand {
		t.Fatalf("pipeline cleanup dry-run commands = %q, want %q", got, wantCommand)
	}

	apply := NewRootCmd()
	applyOut, applyErr := &bytes.Buffer{}, &bytes.Buffer{}
	apply.SetOut(applyOut)
	apply.SetErr(applyErr)
	apply.SetArgs([]string{"pipeline", "cleanup", "ticket_to_pr", "--repo", root, "--merged", "--json"})
	if err := apply.Execute(); err != nil {
		t.Fatalf("pipeline cleanup apply: %v\nstderr=%s", err, applyErr.String())
	}
	var applied jobCleanupBatchResult
	if err := json.Unmarshal(applyOut.Bytes(), &applied); err != nil {
		t.Fatalf("decode pipeline cleanup apply: %v\nbody=%s", err, applyOut.String())
	}
	if applied.Pipeline != "ticket_to_pr" || !applied.Merged || applied.Total != 1 || applied.Cleaned != 1 || len(applied.Items) != 1 || applied.Items[0].JobID != "squ-730" {
		t.Fatalf("pipeline cleanup applied = %+v", applied)
	}
	cleaned, err := job.Read(teamDir, "squ-730")
	if err != nil {
		t.Fatalf("read cleaned job: %v", err)
	}
	untouched, err := job.Read(teamDir, "ops-730")
	if err != nil {
		t.Fatalf("read untouched job: %v", err)
	}
	if cleaned.Branch != "" || cleaned.LastEvent != "cleanup" {
		t.Fatalf("cleaned job = %+v", cleaned)
	}
	if untouched.Branch != opsBranch || untouched.LastEvent == "cleanup" {
		t.Fatalf("outside pipeline job mutated = %+v", untouched)
	}
	if branchExists(t, root, deliveryBranch) {
		t.Fatalf("pipeline branch still exists")
	}
	if !branchExists(t, root, opsBranch) {
		t.Fatalf("outside pipeline branch was removed")
	}
}

func TestPipelineCleanupRejectsFormatCombinations(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "format with json",
			args: []string{"pipeline", "cleanup", "ticket_to_pr", "--dry-run", "--format", "{{.Pipeline}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "commands without dry run",
			args: []string{"pipeline", "cleanup", "ticket_to_pr", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "commands with json",
			args: []string{"pipeline", "cleanup", "ticket_to_pr", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "commands with format",
			args: []string{"pipeline", "cleanup", "ticket_to_pr", "--dry-run", "--commands", "--format", "{{.Pipeline}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "invalid format",
			args: []string{"pipeline", "cleanup", "ticket_to_pr", "--dry-run", "--format", "{{"},
			want: "invalid --format template",
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
				t.Fatalf("pipeline cleanup validation succeeded: stdout=%s", out.String())
			}
			var code ExitCode
			if !errors.As(err, &code) || int(code) != 2 {
				t.Fatalf("pipeline cleanup err = %v, want exit code 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestPipelineWaitPollsScopedJobs(t *testing.T) {
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
id = "audit"
target = "worker"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-801",
			Ticket:    "SQU-801",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "squ-802",
			Ticket:    "SQU-802",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusBlocked,
			CreatedAt: now.Add(time.Second),
			UpdatedAt: now.Add(time.Second),
		},
		{
			ID:        "ops-801",
			Ticket:    "OPS-801",
			Target:    "worker",
			Pipeline:  "ops_review",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "adhoc-801",
			Ticket:    "ADHOC-801",
			Target:    "worker",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(25 * time.Millisecond)
		updates := []struct {
			id     string
			status job.Status
			event  string
		}{
			{id: "squ-801", status: job.StatusDone, event: "pipeline_done"},
			{id: "squ-802", status: job.StatusFailed, event: "pipeline_failed"},
			{id: "ops-801", status: job.StatusDone, event: "foreign_done"},
		}
		for _, update := range updates {
			updated, err := job.Read(teamDir, update.id)
			if err != nil {
				t.Errorf("read job %s in updater: %v", update.id, err)
				return
			}
			updated.Status = update.status
			updated.LastEvent = update.event
			updated.UpdatedAt = time.Now().UTC()
			if err := job.Write(teamDir, updated); err != nil {
				t.Errorf("write job %s in updater: %v", update.id, err)
				return
			}
		}
	}()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "wait", "ticket_to_pr", "--repo", root, "--timeout", "2s", "--interval", "10ms", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline wait: %v\nstderr=%s", err, stderr.String())
	}
	<-done
	var got []job.Job
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode pipeline wait json: %v\nbody=%s", err, out.String())
	}
	statuses := map[string]job.Status{}
	events := map[string]string{}
	for _, j := range got {
		statuses[j.ID] = j.Status
		events[j.ID] = j.LastEvent
	}
	if len(got) != 2 || statuses["squ-801"] != job.StatusDone || statuses["squ-802"] != job.StatusFailed || events["ops-801"] != "" {
		t.Fatalf("pipeline wait jobs = %+v", got)
	}

	allCmd := NewRootCmd()
	allOut, allErr := &bytes.Buffer{}, &bytes.Buffer{}
	allCmd.SetOut(allOut)
	allCmd.SetErr(allErr)
	allCmd.SetArgs([]string{"pipeline", "wait", "--repo", root, "--timeout", "2s", "--interval", "10ms", "--json"})
	if err := allCmd.Execute(); err != nil {
		t.Fatalf("pipeline wait all default: %v\nstderr=%s", err, allErr.String())
	}
	got = nil
	if err := json.Unmarshal(allOut.Bytes(), &got); err != nil {
		t.Fatalf("decode pipeline wait all json: %v\nbody=%s", err, allOut.String())
	}
	allStatuses := map[string]job.Status{}
	for _, j := range got {
		allStatuses[j.ID] = j.Status
	}
	if len(got) != 3 ||
		allStatuses["squ-801"] != job.StatusDone ||
		allStatuses["squ-802"] != job.StatusFailed ||
		allStatuses["ops-801"] != job.StatusDone ||
		allStatuses["adhoc-801"] != "" {
		t.Fatalf("pipeline wait all jobs = %+v", got)
	}

	explicitAll := NewRootCmd()
	explicitAllOut, explicitAllErr := &bytes.Buffer{}, &bytes.Buffer{}
	explicitAll.SetOut(explicitAllOut)
	explicitAll.SetErr(explicitAllErr)
	explicitAll.SetArgs([]string{"pipeline", "wait", "--all", "--repo", root, "--job", "ops-801", "--status", "done", "--format", "{{.ID}} {{.Status}}"})
	if err := explicitAll.Execute(); err != nil {
		t.Fatalf("pipeline wait --all format: %v\nstderr=%s", err, explicitAllErr.String())
	}
	if got, want := explicitAllOut.String(), "ops-801 done\n"; got != want {
		t.Fatalf("pipeline wait --all format = %q, want %q", got, want)
	}

	formatted := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatted.SetOut(formatOut)
	formatted.SetErr(formatErr)
	formatted.SetArgs([]string{"pipeline", "wait", "ticket_to_pr", "--repo", root, "--job", "SQU-801", "--status", "done", "--format", "{{.ID}} {{.Status}}"})
	if err := formatted.Execute(); err != nil {
		t.Fatalf("pipeline wait format: %v\nstderr=%s", err, formatErr.String())
	}
	if got, want := formatOut.String(), "squ-801 done\n"; got != want {
		t.Fatalf("pipeline wait format = %q, want %q", got, want)
	}
}

func TestPipelineWaitPollsNextStepState(t *testing.T) {
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
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-806",
			Ticket:    "SQU-806",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusQueued},
				{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}},
			},
		},
		{
			ID:        "squ-807",
			Ticket:    "SQU-807",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(time.Second),
			UpdatedAt: now.Add(time.Second),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning},
				{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}

	queued := NewRootCmd()
	queuedOut, queuedErr := &bytes.Buffer{}, &bytes.Buffer{}
	queued.SetOut(queuedOut)
	queued.SetErr(queuedErr)
	queued.SetArgs([]string{"pipeline", "wait", "ticket_to_pr", "--repo", root, "--job", "squ-806", "--next-state", "all", "--step", "implement", "--format", "{{.ID}} {{.Status}}"})
	if err := queued.Execute(); err != nil {
		t.Fatalf("pipeline wait next-state all: %v\nstderr=%s", err, queuedErr.String())
	}
	if got, want := queuedOut.String(), "squ-806 running\n"; got != want {
		t.Fatalf("pipeline wait next-state all output = %q, want %q", got, want)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(25 * time.Millisecond)
		for _, id := range []string{"squ-806", "squ-807"} {
			updated, err := job.Read(teamDir, id)
			if err != nil {
				t.Errorf("read job %s in updater: %v", id, err)
				return
			}
			updated.Status = job.StatusBlocked
			updated.Steps[0].Status = job.StatusDone
			updated.Steps[0].FinishedAt = time.Now().UTC()
			updated.UpdatedAt = time.Now().UTC()
			if err := job.Write(teamDir, updated); err != nil {
				t.Errorf("write job %s in updater: %v", id, err)
				return
			}
		}
	}()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "wait", "ticket_to_pr", "--repo", root, "--next-state", "ready", "--step", "review", "--timeout", "2s", "--interval", "10ms", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline wait next-state ready: %v\nstderr=%s", err, stderr.String())
	}
	<-done
	var got []job.Job
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode pipeline wait next-state json: %v\nbody=%s", err, out.String())
	}
	nextByJob := map[string]jobNextResult{}
	for i := range got {
		nextByJob[got[i].ID] = inspectNextJobStep(&got[i])
	}
	if len(got) != 2 ||
		nextByJob["squ-806"].State != "ready" ||
		jobWaitNextStep(nextByJob["squ-806"]) != "review" ||
		nextByJob["squ-807"].State != "ready" ||
		jobWaitNextStep(nextByJob["squ-807"]) != "review" {
		t.Fatalf("pipeline wait next-state jobs = %+v next=%+v", got, nextByJob)
	}
}

func TestPipelineWaitTimesOut(t *testing.T) {
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
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-803",
		Ticket:    "SQU-803",
		Target:    "worker",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		LastEvent: "dispatched",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "wait", "ticket_to_pr", "--repo", root, "--timeout", "1ms", "--interval", "10ms"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("pipeline wait succeeded unexpectedly")
	}
	if !strings.Contains(stderr.String(), "timed out waiting for ticket_to_pr") || !strings.Contains(stderr.String(), "squ-803=running event=dispatched") {
		t.Fatalf("stderr = %q", stderr.String())
	}

	allCmd := NewRootCmd()
	allOut, allErr := &bytes.Buffer{}, &bytes.Buffer{}
	allCmd.SetOut(allOut)
	allCmd.SetErr(allErr)
	allCmd.SetArgs([]string{"pipeline", "wait", "--repo", root, "--timeout", "1ms", "--interval", "10ms"})
	if err := allCmd.Execute(); err == nil {
		t.Fatalf("pipeline wait all succeeded unexpectedly")
	}
	if !strings.Contains(allErr.String(), "timed out waiting for all pipelines") || !strings.Contains(allErr.String(), "squ-803=running event=dispatched") {
		t.Fatalf("all stderr = %q", allErr.String())
	}

	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-808",
		Ticket:    "SQU-808",
		Target:    "worker",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		LastEvent: "dispatched",
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusQueued},
			{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}},
		},
	}); err != nil {
		t.Fatalf("write step job: %v", err)
	}

	nextCmd := NewRootCmd()
	nextOut, nextErr := &bytes.Buffer{}, &bytes.Buffer{}
	nextCmd.SetOut(nextOut)
	nextCmd.SetErr(nextErr)
	nextCmd.SetArgs([]string{"pipeline", "wait", "ticket_to_pr", "--repo", root, "--job", "squ-808", "--next-state", "ready", "--step", "review", "--timeout", "1ms", "--interval", "10ms"})
	if err := nextCmd.Execute(); err == nil {
		t.Fatalf("pipeline wait next-state timeout succeeded unexpectedly")
	}
	for _, want := range []string{"next-state=ready", "step=review", "squ-808=running", "next_state=queued", "step=implement"} {
		if !strings.Contains(nextErr.String(), want) {
			t.Fatalf("next-state stderr = %q, want %q", nextErr.String(), want)
		}
	}
}

func TestPipelineWaitFailOnFailed(t *testing.T) {
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
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-804",
		Ticket:    "SQU-804",
		Target:    "worker",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusFailed,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "wait", "ticket_to_pr", "--repo", root, "--quiet", "--fail-on-failed"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("pipeline wait succeeded unexpectedly")
	}
	if out.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("quiet wait produced stdout=%q stderr=%q", out.String(), stderr.String())
	}
}

func TestPipelineWaitRejectsValidation(t *testing.T) {
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
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-805",
		Ticket:    "SQU-805",
		Target:    "worker",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusDone,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "format with json",
			args: []string{"pipeline", "wait", "ticket_to_pr", "--repo", root, "--format", "{{.ID}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "missing job",
			args: []string{"pipeline", "wait", "ticket_to_pr", "--repo", root, "--job", "squ-missing"},
			want: "job(s) not owned by pipeline: squ-missing",
		},
		{
			name: "all with pipeline",
			args: []string{"pipeline", "wait", "ticket_to_pr", "--all", "--repo", root},
			want: "--all cannot be combined with a pipeline argument",
		},
		{
			name: "invalid next state",
			args: []string{"pipeline", "wait", "ticket_to_pr", "--repo", root, "--next-state", "stuck"},
			want: "--next-state must be ready",
		},
		{
			name: "multiple pipelines",
			args: []string{"pipeline", "wait", "ticket_to_pr", "ops_review", "--repo", root},
			want: "pass at most one pipeline name",
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
				t.Fatalf("pipeline wait validation succeeded: stdout=%s", out.String())
			}
			var code ExitCode
			if !errors.As(err, &code) || int(code) != 2 {
				t.Fatalf("pipeline wait err = %v, want exit code 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestPipelineQueueScopesItems(t *testing.T) {
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
id = "audit"
target = "worker"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-901",
			Ticket:    "SQU-901",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "ops-901",
			Ticket:    "OPS-901",
			Target:    "worker",
			Pipeline:  "ops_review",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "adhoc-901",
			Ticket:    "ADHOC-901",
			Target:    "worker",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}
	queueRoot := daemon.DaemonRoot(teamDir)
	for _, item := range []*daemon.QueueItem{
		{
			ID:             "q-pipeline-dead",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     "worker-squ-901",
			Payload:        map[string]any{"job_id": "squ-901", "ticket": "SQU-901", "runtime": "codex"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "worker unavailable",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-2 * time.Hour),
			DeadLetteredAt: now.Add(-2 * time.Hour),
		},
		{
			ID:         "q-pipeline-pending",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-901-review",
			Payload:    map[string]any{"job": "squ-901", "ticket": "SQU-901"},
			QueuedAt:   now.Add(-time.Hour),
			UpdatedAt:  now.Add(-time.Hour),
		},
		{
			ID:             "q-foreign-dead",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     "worker-ops-901",
			Payload:        map[string]any{"job_id": "ops-901", "ticket": "OPS-901"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "foreign",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-2 * time.Hour),
			DeadLetteredAt: now.Add(-2 * time.Hour),
		},
		{
			ID:             "q-adhoc-dead",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     "worker-adhoc-901",
			Payload:        map[string]any{"job_id": "adhoc-901", "ticket": "ADHOC-901"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "adhoc",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-2 * time.Hour),
			DeadLetteredAt: now.Add(-2 * time.Hour),
		},
	} {
		if err := daemon.WriteQueueItem(queueRoot, item); err != nil {
			t.Fatalf("write queue item %s: %v", item.ID, err)
		}
	}

	list := NewRootCmd()
	listOut, listErr := &bytes.Buffer{}, &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetErr(listErr)
	list.SetArgs([]string{"pipeline", "queue", "ticket_to_pr", "--repo", root, "--sort", "id", "--json"})
	if err := list.Execute(); err != nil {
		t.Fatalf("pipeline queue list: %v\nstderr=%s", err, listErr.String())
	}
	var listed []daemon.QueueItem
	if err := json.Unmarshal(listOut.Bytes(), &listed); err != nil {
		t.Fatalf("decode pipeline queue list: %v\nbody=%s", err, listOut.String())
	}
	if got := queueItemIDsForTest(listed); strings.Join(got, ",") != "q-pipeline-dead,q-pipeline-pending" {
		t.Fatalf("pipeline queue list IDs = %v\nbody=%s", got, listOut.String())
	}

	allList := NewRootCmd()
	allListOut, allListErr := &bytes.Buffer{}, &bytes.Buffer{}
	allList.SetOut(allListOut)
	allList.SetErr(allListErr)
	allList.SetArgs([]string{"pipeline", "queue", "--repo", root, "--sort", "id", "--json"})
	if err := allList.Execute(); err != nil {
		t.Fatalf("pipeline queue all list: %v\nstderr=%s", err, allListErr.String())
	}
	listed = nil
	if err := json.Unmarshal(allListOut.Bytes(), &listed); err != nil {
		t.Fatalf("decode pipeline queue all list: %v\nbody=%s", err, allListOut.String())
	}
	if got := queueItemIDsForTest(listed); strings.Join(got, ",") != "q-foreign-dead,q-pipeline-dead,q-pipeline-pending" {
		t.Fatalf("pipeline queue all IDs = %v\nbody=%s", got, allListOut.String())
	}

	allText := NewRootCmd()
	allTextOut, allTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	allText.SetOut(allTextOut)
	allText.SetErr(allTextErr)
	allText.SetArgs([]string{"pipeline", "queue", "--all", "--repo", root, "--sort", "id"})
	if err := allText.Execute(); err != nil {
		t.Fatalf("pipeline queue --all text: %v\nstderr=%s", err, allTextErr.String())
	}
	for _, want := range []string{
		"agent-team pipeline queue retry ops_review q-foreign-dead",
		"agent-team pipeline queue retry ticket_to_pr q-pipeline-dead",
		"agent-team pipeline queue drop ticket_to_pr q-pipeline-pending",
	} {
		if !strings.Contains(allTextOut.String(), want) {
			t.Fatalf("pipeline queue --all text missing %q:\n%s", want, allTextOut.String())
		}
	}

	summary := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(summaryOut)
	summary.SetErr(summaryErr)
	summary.SetArgs([]string{"pipeline", "queue", "ticket_to_pr", "--repo", root, "--summary", "--json"})
	if err := summary.Execute(); err != nil {
		t.Fatalf("pipeline queue summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var summarized queueSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &summarized); err != nil {
		t.Fatalf("decode pipeline queue summary: %v\nbody=%s", err, summaryOut.String())
	}
	if summarized.Total != 2 || summarized.Dead != 1 || summarized.Pending != 1 {
		t.Fatalf("pipeline queue summary = %+v", summarized)
	}

	allSummary := NewRootCmd()
	allSummaryOut, allSummaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	allSummary.SetOut(allSummaryOut)
	allSummary.SetErr(allSummaryErr)
	allSummary.SetArgs([]string{"pipeline", "queue", "--all", "--repo", root, "--summary", "--json"})
	if err := allSummary.Execute(); err != nil {
		t.Fatalf("pipeline queue all summary: %v\nstderr=%s", err, allSummaryErr.String())
	}
	var allSummarized queueSummary
	if err := json.Unmarshal(allSummaryOut.Bytes(), &allSummarized); err != nil {
		t.Fatalf("decode pipeline queue all summary: %v\nbody=%s", err, allSummaryOut.String())
	}
	if allSummarized.Total != 3 || allSummarized.Dead != 2 || allSummarized.Pending != 1 {
		t.Fatalf("pipeline queue all summary = %+v", allSummarized)
	}

	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"pipeline", "queue", "show", "ticket_to_pr", "q-pipeline-dead", "--repo", root, "--format", "{{.ID}} {{.State}}"})
	if err := show.Execute(); err != nil {
		t.Fatalf("pipeline queue show: %v\nstderr=%s", err, showErr.String())
	}
	if got, want := showOut.String(), "q-pipeline-dead dead\n"; got != want {
		t.Fatalf("pipeline queue show format = %q, want %q", got, want)
	}

	showCommands := NewRootCmd()
	showCommandsOut, showCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	showCommands.SetOut(showCommandsOut)
	showCommands.SetErr(showCommandsErr)
	showCommands.SetArgs([]string{"pipeline", "queue", "show", "ticket_to_pr", "q-pipeline-dead", "--repo", root, "--commands"})
	if err := showCommands.Execute(); err != nil {
		t.Fatalf("pipeline queue show --commands: %v\nstderr=%s", err, showCommandsErr.String())
	}
	if got, want := showCommandsOut.String(), "agent-team pipeline queue retry ticket_to_pr q-pipeline-dead\nagent-team pipeline queue drop ticket_to_pr q-pipeline-dead\n"; got != want {
		t.Fatalf("pipeline queue show --commands = %q, want %q", got, want)
	}

	showCommandsJSON := NewRootCmd()
	showCommandsJSONOut, showCommandsJSONErr := &bytes.Buffer{}, &bytes.Buffer{}
	showCommandsJSON.SetOut(showCommandsJSONOut)
	showCommandsJSON.SetErr(showCommandsJSONErr)
	showCommandsJSON.SetArgs([]string{"pipeline", "queue", "show", "ticket_to_pr", "q-pipeline-dead", "--repo", root, "--commands", "--json"})
	if err := showCommandsJSON.Execute(); err == nil {
		t.Fatalf("pipeline queue show --commands --json succeeded: stdout=%s", showCommandsJSONOut.String())
	}
	if !strings.Contains(showCommandsJSONErr.String(), "--commands cannot be combined with --json") {
		t.Fatalf("pipeline queue show --commands --json stderr = %q", showCommandsJSONErr.String())
	}

	foreign := NewRootCmd()
	foreignOut, foreignErr := &bytes.Buffer{}, &bytes.Buffer{}
	foreign.SetOut(foreignOut)
	foreign.SetErr(foreignErr)
	foreign.SetArgs([]string{"pipeline", "queue", "show", "ticket_to_pr", "q-foreign-dead", "--repo", root})
	if err := foreign.Execute(); err == nil {
		t.Fatalf("pipeline queue foreign show succeeded: stdout=%s", foreignOut.String())
	}
	if !strings.Contains(foreignErr.String(), `queue item "q-foreign-dead" is not owned by pipeline "ticket_to_pr"`) {
		t.Fatalf("foreign stderr = %q", foreignErr.String())
	}

	retry := NewRootCmd()
	retryOut, retryErr := &bytes.Buffer{}, &bytes.Buffer{}
	retry.SetOut(retryOut)
	retry.SetErr(retryErr)
	retry.SetArgs([]string{"pipeline", "queue", "retry", "ticket_to_pr", "--all", "--repo", root, "--dry-run", "--json"})
	if err := retry.Execute(); err != nil {
		t.Fatalf("pipeline queue retry dry-run: %v\nstderr=%s", err, retryErr.String())
	}
	var retryRows []queueRetryResult
	if err := json.Unmarshal(retryOut.Bytes(), &retryRows); err != nil {
		t.Fatalf("decode retry rows: %v\nbody=%s", err, retryOut.String())
	}
	if len(retryRows) != 1 || retryRows[0].ID != "q-pipeline-dead" || retryRows[0].Action != "would_retry" {
		t.Fatalf("retry rows = %+v", retryRows)
	}

	retryCommands := NewRootCmd()
	retryCommandsOut, retryCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	retryCommands.SetOut(retryCommandsOut)
	retryCommands.SetErr(retryCommandsErr)
	retryCommands.SetArgs([]string{"pipeline", "queue", "retry", "ticket_to_pr", "--all", "--repo", root, "--runtime", "codex", "--limit", "1", "--dry-run", "--commands"})
	if err := retryCommands.Execute(); err != nil {
		t.Fatalf("pipeline queue retry commands: %v\nstderr=%s", err, retryCommandsErr.String())
	}
	wantRetryCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "pipeline", "queue", "retry", "ticket_to_pr", "--repo", root, "--all", "--runtime", "codex", "--limit", "1"}), " ")
	if got := strings.TrimSpace(retryCommandsOut.String()); got != wantRetryCommand {
		t.Fatalf("pipeline queue retry commands = %q, want %q", got, wantRetryCommand)
	}

	retryOneCommands := NewRootCmd()
	retryOneCommandsOut, retryOneCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	retryOneCommands.SetOut(retryOneCommandsOut)
	retryOneCommands.SetErr(retryOneCommandsErr)
	retryOneCommands.SetArgs([]string{"pipeline", "queue", "retry", "ticket_to_pr", "q-pipeline-dead", "--repo", root, "--dry-run", "--commands"})
	if err := retryOneCommands.Execute(); err != nil {
		t.Fatalf("pipeline queue retry single commands: %v\nstderr=%s", err, retryOneCommandsErr.String())
	}
	wantRetryOneCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "pipeline", "queue", "retry", "ticket_to_pr", "q-pipeline-dead", "--repo", root}), " ")
	if got := strings.TrimSpace(retryOneCommandsOut.String()); got != wantRetryOneCommand {
		t.Fatalf("pipeline queue retry single commands = %q, want %q", got, wantRetryOneCommand)
	}

	drop := NewRootCmd()
	dropOut, dropErr := &bytes.Buffer{}, &bytes.Buffer{}
	drop.SetOut(dropOut)
	drop.SetErr(dropErr)
	drop.SetArgs([]string{"pipeline", "queue", "drop", "ticket_to_pr", "--all", "--repo", root, "--dry-run", "--format", "{{.ID}} {{.Action}}"})
	if err := drop.Execute(); err != nil {
		t.Fatalf("pipeline queue drop dry-run: %v\nstderr=%s", err, dropErr.String())
	}
	if got, want := dropOut.String(), "q-pipeline-dead would_drop\n"; got != want {
		t.Fatalf("drop format = %q, want %q", got, want)
	}

	dropCommands := NewRootCmd()
	dropCommandsOut, dropCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropCommands.SetOut(dropCommandsOut)
	dropCommands.SetErr(dropCommandsErr)
	dropCommands.SetArgs([]string{"pipeline", "queue", "drop", "ticket_to_pr", "--all", "--repo", root, "--dry-run", "--commands"})
	if err := dropCommands.Execute(); err != nil {
		t.Fatalf("pipeline queue drop commands: %v\nstderr=%s", err, dropCommandsErr.String())
	}
	wantDropCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "pipeline", "queue", "drop", "ticket_to_pr", "--repo", root, "--all"}), " ")
	if got := strings.TrimSpace(dropCommandsOut.String()); got != wantDropCommand {
		t.Fatalf("pipeline queue drop commands = %q, want %q", got, wantDropCommand)
	}

	dropOneCommands := NewRootCmd()
	dropOneCommandsOut, dropOneCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropOneCommands.SetOut(dropOneCommandsOut)
	dropOneCommands.SetErr(dropOneCommandsErr)
	dropOneCommands.SetArgs([]string{"pipeline", "queue", "drop", "ticket_to_pr", "q-pipeline-dead", "--repo", root, "--dry-run", "--commands"})
	if err := dropOneCommands.Execute(); err != nil {
		t.Fatalf("pipeline queue drop single commands: %v\nstderr=%s", err, dropOneCommandsErr.String())
	}
	wantDropOneCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "pipeline", "queue", "drop", "ticket_to_pr", "q-pipeline-dead", "--repo", root}), " ")
	if got := strings.TrimSpace(dropOneCommandsOut.String()); got != wantDropOneCommand {
		t.Fatalf("pipeline queue drop single commands = %q, want %q", got, wantDropOneCommand)
	}

	pruneCommands := NewRootCmd()
	pruneCommandsOut, pruneCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	pruneCommands.SetOut(pruneCommandsOut)
	pruneCommands.SetErr(pruneCommandsErr)
	pruneCommands.SetArgs([]string{"pipeline", "queue", "prune", "ticket_to_pr", "--repo", root, "--dry-run", "--commands"})
	if err := pruneCommands.Execute(); err != nil {
		t.Fatalf("pipeline queue prune commands: %v\nstderr=%s", err, pruneCommandsErr.String())
	}
	wantPruneCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "pipeline", "queue", "prune", "ticket_to_pr", "--repo", root}), " ")
	if got := strings.TrimSpace(pruneCommandsOut.String()); got != wantPruneCommand {
		t.Fatalf("pipeline queue prune commands = %q, want %q", got, wantPruneCommand)
	}

	prune := NewRootCmd()
	pruneOut, pruneErr := &bytes.Buffer{}, &bytes.Buffer{}
	prune.SetOut(pruneOut)
	prune.SetErr(pruneErr)
	prune.SetArgs([]string{"pipeline", "queue", "prune", "ticket_to_pr", "--repo", root, "--json"})
	if err := prune.Execute(); err != nil {
		t.Fatalf("pipeline queue prune: %v\nstderr=%s", err, pruneErr.String())
	}
	var pruned []queuePruneResult
	if err := json.Unmarshal(pruneOut.Bytes(), &pruned); err != nil {
		t.Fatalf("decode prune rows: %v\nbody=%s", err, pruneOut.String())
	}
	if len(pruned) != 1 || pruned[0].ID != "q-pipeline-dead" || !pruned[0].Dropped {
		t.Fatalf("prune rows = %+v", pruned)
	}
	if _, err := daemon.ReadQueueItem(queueRoot, "q-pipeline-dead"); err == nil {
		t.Fatalf("pipeline dead queue item still exists after prune")
	}
	if _, err := daemon.ReadQueueItem(queueRoot, "q-foreign-dead"); err != nil {
		t.Fatalf("foreign queue item was removed: %v", err)
	}

	invalidMany := NewRootCmd()
	invalidManyOut, invalidManyErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidMany.SetOut(invalidManyOut)
	invalidMany.SetErr(invalidManyErr)
	invalidMany.SetArgs([]string{"pipeline", "queue", "ticket_to_pr", "ops_review", "--repo", root})
	if err := invalidMany.Execute(); err == nil {
		t.Fatalf("pipeline queue accepted multiple pipeline names: stdout=%s", invalidManyOut.String())
	}
	if !strings.Contains(invalidManyErr.String(), "pass at most one pipeline name") {
		t.Fatalf("multiple pipeline error = %q", invalidManyErr.String())
	}

	invalidAll := NewRootCmd()
	invalidAllOut, invalidAllErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidAll.SetOut(invalidAllOut)
	invalidAll.SetErr(invalidAllErr)
	invalidAll.SetArgs([]string{"pipeline", "queue", "ticket_to_pr", "--all", "--repo", root})
	if err := invalidAll.Execute(); err == nil {
		t.Fatalf("pipeline queue accepted --all with pipeline: stdout=%s", invalidAllOut.String())
	}
	if !strings.Contains(invalidAllErr.String(), "--all cannot be combined with a pipeline argument") {
		t.Fatalf("--all conflict error = %q", invalidAllErr.String())
	}
}

func TestPipelineQueueRetryAllSortsBeforeLimit(t *testing.T) {
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
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-902",
		Ticket:    "SQU-902",
		Target:    "worker",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	queueRoot := daemon.DaemonRoot(teamDir)
	for _, item := range []*daemon.QueueItem{
		{
			ID:             "q-pipeline-low-attempts",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     "worker-squ-902-low",
			Payload:        map[string]any{"job_id": "squ-902", "ticket": "SQU-902"},
			Attempts:       1,
			LastError:      "first failure",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-2 * time.Hour),
			DeadLetteredAt: now.Add(-2 * time.Hour),
		},
		{
			ID:             "q-pipeline-high-attempts",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     "worker-squ-902-high",
			Payload:        map[string]any{"job_id": "squ-902", "ticket": "SQU-902"},
			Attempts:       6,
			LastError:      "repeated failure",
			QueuedAt:       now.Add(-time.Hour),
			UpdatedAt:      now.Add(-30 * time.Minute),
			DeadLetteredAt: now.Add(-30 * time.Minute),
		},
	} {
		if err := daemon.WriteQueueItem(queueRoot, item); err != nil {
			t.Fatalf("write queue item %s: %v", item.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "queue", "retry", "ticket_to_pr", "--repo", root, "--all", "--sort", "attempts", "--limit", "1", "--dry-run", "--format", "{{.ID}} {{.Action}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline queue retry sort/limit: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := strings.TrimSpace(out.String()), "q-pipeline-high-attempts would_retry"; got != want {
		t.Fatalf("pipeline queue retry sort/limit output = %q, want %q", got, want)
	}
}

func TestPipelineQueuePruneFiltersByEventAndJob(t *testing.T) {
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
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-930",
			Ticket:    "SQU-930",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "squ-931",
			Ticket:    "SQU-931",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}
	queueRoot := daemon.DaemonRoot(teamDir)
	for _, item := range []*daemon.QueueItem{
		{
			ID:             "q-pipeline-target",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     "worker-squ-930",
			Payload:        map[string]any{"job_id": "squ-930", "ticket": "SQU-930"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "worker unavailable",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-2 * time.Hour),
			DeadLetteredAt: now.Add(-2 * time.Hour),
		},
		{
			ID:             "q-pipeline-wrong-event",
			State:          daemon.QueueStateDead,
			EventType:      "schedule.fire",
			Instance:       "worker",
			InstanceID:     "worker-squ-930-schedule",
			Payload:        map[string]any{"job_id": "squ-930", "ticket": "SQU-930"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "worker unavailable",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-2 * time.Hour),
			DeadLetteredAt: now.Add(-2 * time.Hour),
		},
		{
			ID:             "q-pipeline-wrong-job",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     "worker-squ-931",
			Payload:        map[string]any{"job_id": "squ-931", "ticket": "SQU-931"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "worker unavailable",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-2 * time.Hour),
			DeadLetteredAt: now.Add(-2 * time.Hour),
		},
	} {
		if err := daemon.WriteQueueItem(queueRoot, item); err != nil {
			t.Fatalf("write queue item %s: %v", item.ID, err)
		}
	}

	prune := NewRootCmd()
	pruneOut, pruneErr := &bytes.Buffer{}, &bytes.Buffer{}
	prune.SetOut(pruneOut)
	prune.SetErr(pruneErr)
	prune.SetArgs([]string{"pipeline", "queue", "prune", "ticket_to_pr", "--repo", root, "--job", "SQU-930", "--event-type", "agent.dispatch", "--format", "{{.ID}} {{.Dropped}}"})
	if err := prune.Execute(); err != nil {
		t.Fatalf("pipeline queue prune filtered: %v\nstderr=%s", err, pruneErr.String())
	}
	if got, want := strings.TrimSpace(pruneOut.String()), "q-pipeline-target true"; got != want {
		t.Fatalf("pipeline filtered prune output = %q, want %q", got, want)
	}
	if _, err := daemon.ReadQueueItem(queueRoot, "q-pipeline-target"); !os.IsNotExist(err) {
		t.Fatalf("target item err=%v, want not exist", err)
	}
	for _, id := range []string{"q-pipeline-wrong-event", "q-pipeline-wrong-job"} {
		if _, err := daemon.ReadQueueItem(queueRoot, id); err != nil {
			t.Fatalf("queue item %s should remain: %v", id, err)
		}
	}
}

func TestPipelineQueuePruneRejectsNegativeLimit(t *testing.T) {
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "queue", "prune", "ticket_to_pr", "--limit", "-1"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("pipeline queue prune negative limit succeeded: stdout=%s", out.String())
	}
	var code ExitCode
	if !errors.As(err, &code) || int(code) != 2 {
		t.Fatalf("pipeline queue prune err = %v, want exit code 2", err)
	}
	if !strings.Contains(stderr.String(), "--limit must be >= 0") {
		t.Fatalf("stderr = %q, want negative limit message", stderr.String())
	}
}

func TestPipelineQueueControlRejectsCommandsCombinations(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "retry commands without dry run",
			args: []string{"pipeline", "queue", "retry", "ticket_to_pr", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "retry commands with json",
			args: []string{"pipeline", "queue", "retry", "ticket_to_pr", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "retry commands with format",
			args: []string{"pipeline", "queue", "retry", "ticket_to_pr", "--dry-run", "--commands", "--format", "{{.ID}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "drop commands without dry run",
			args: []string{"pipeline", "queue", "drop", "ticket_to_pr", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "drop commands with json",
			args: []string{"pipeline", "queue", "drop", "ticket_to_pr", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "drop commands with format",
			args: []string{"pipeline", "queue", "drop", "ticket_to_pr", "--dry-run", "--commands", "--format", "{{.ID}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "prune commands without dry run",
			args: []string{"pipeline", "queue", "prune", "ticket_to_pr", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "prune commands with json",
			args: []string{"pipeline", "queue", "prune", "ticket_to_pr", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "prune commands with format",
			args: []string{"pipeline", "queue", "prune", "ticket_to_pr", "--dry-run", "--commands", "--format", "{{.ID}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "quarantine restore commands without dry run",
			args: []string{"pipeline", "queue", "quarantine", "restore", "ticket_to_pr", "quarantine/20260619T000000.000000000Z/dead/q.json", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "quarantine restore commands with json",
			args: []string{"pipeline", "queue", "quarantine", "restore", "ticket_to_pr", "quarantine/20260619T000000.000000000Z/dead/q.json", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "quarantine restore commands with format",
			args: []string{"pipeline", "queue", "quarantine", "restore", "ticket_to_pr", "quarantine/20260619T000000.000000000Z/dead/q.json", "--dry-run", "--commands", "--format", "{{.ID}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "quarantine drop commands without dry run",
			args: []string{"pipeline", "queue", "quarantine", "drop", "ticket_to_pr", "quarantine/20260619T000000.000000000Z/dead/q.json", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "quarantine drop commands with json",
			args: []string{"pipeline", "queue", "quarantine", "drop", "ticket_to_pr", "quarantine/20260619T000000.000000000Z/dead/q.json", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "quarantine drop commands with format",
			args: []string{"pipeline", "queue", "quarantine", "drop", "ticket_to_pr", "quarantine/20260619T000000.000000000Z/dead/q.json", "--dry-run", "--commands", "--format", "{{.ID}}"},
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
				t.Fatalf("pipeline queue validation succeeded: stdout=%s", out.String())
			}
			var code ExitCode
			if !errors.As(err, &code) || int(code) != 2 {
				t.Fatalf("pipeline queue err = %v, want exit code 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestPipelineRepairScopesQueueAndRetry(t *testing.T) {
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
	timeout = "1h"

	[pipelines.ops_review]
	trigger.event = "ops.created"

	[[pipelines.ops_review.steps]]
	id = "audit"
	target = "worker"
	`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:         "squ-921",
			Ticket:     "SQU-921",
			Target:     "worker",
			Pipeline:   "ticket_to_pr",
			Status:     job.StatusFailed,
			LastEvent:  "step_failed",
			LastStatus: "implement failed",
			CreatedAt:  now,
			UpdatedAt:  now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusFailed, Instance: "worker-squ-921"},
			},
		},
		{
			ID:         "ops-921",
			Ticket:     "OPS-921",
			Target:     "worker",
			Pipeline:   "ops_review",
			Status:     job.StatusFailed,
			LastEvent:  "step_failed",
			LastStatus: "audit failed",
			CreatedAt:  now,
			UpdatedAt:  now,
			Steps: []job.Step{
				{ID: "audit", Target: "worker", Status: job.StatusFailed, Instance: "worker-ops-921"},
			},
		},
		{
			ID:        "squ-922",
			Ticket:    "SQU-922",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-922", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}
	for _, item := range []*daemon.QueueItem{
		{
			ID:             "q-pipeline-repair",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     "worker-squ-921",
			Payload:        map[string]any{"job_id": "squ-921", "ticket": "SQU-921"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-2 * time.Hour),
			DeadLetteredAt: now.Add(-2 * time.Hour),
		},
		{
			ID:             "q-ops-repair",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     "worker-ops-921",
			Payload:        map[string]any{"job_id": "ops-921", "ticket": "OPS-921"},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-3 * time.Hour),
			UpdatedAt:      now.Add(-2 * time.Hour),
			DeadLetteredAt: now.Add(-2 * time.Hour),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("write queue item %s: %v", item.ID, err)
		}
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	retryMessageFile := filepath.Join(root, "pipeline-repair-retry.txt")
	if err := os.WriteFile(retryMessageFile, []byte("pipeline repair retry from file\n"), 0o644); err != nil {
		t.Fatalf("write retry message: %v", err)
	}
	timeoutMessageFile := filepath.Join(root, "pipeline-repair-timeout.txt")
	if err := os.WriteFile(timeoutMessageFile, []byte("pipeline repair timeout from file\n"), 0o644); err != nil {
		t.Fatalf("write timeout message: %v", err)
	}
	dry.SetArgs([]string{
		"pipeline", "repair", "ticket_to_pr",
		"--repo", root,
		"--dry-run",
		"--timeout-pipelines",
		"--timeout-message-file", timeoutMessageFile,
		"--retry-pipelines",
		"--retry-message-file", retryMessageFile,
		"--preview-routes",
		"--skip-daemon",
		"--runtime", "codex",
		"--runtime-bin", "codex-dev",
		"--json",
	})
	if err := dry.Execute(); err != nil {
		t.Fatalf("pipeline repair dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var result pipelineRepairResult
	if err := json.Unmarshal(dryOut.Bytes(), &result); err != nil {
		t.Fatalf("decode pipeline repair: %v\nbody=%s", err, dryOut.String())
	}
	if result.Pipeline != "ticket_to_pr" || !result.DryRun || result.Daemon.Action != "skipped" {
		t.Fatalf("pipeline repair result = %+v", result)
	}
	if result.Queue.Action != "would_retry" || len(result.Queue.Results) != 1 || result.Queue.Results[0].ID != "q-pipeline-repair" {
		t.Fatalf("pipeline repair queue = %+v", result.Queue)
	}
	if result.PipelineTimeout.Action != "would_fail" || len(result.PipelineTimeout.Results) != 1 {
		t.Fatalf("pipeline repair timeout = %+v", result.PipelineTimeout)
	}
	timeoutRow := result.PipelineTimeout.Results[0]
	if timeoutRow.JobID != "squ-922" || timeoutRow.Message != "pipeline repair timeout from file" {
		t.Fatalf("pipeline repair timeout row = %+v", timeoutRow)
	}
	if result.PipelineRetry.Action != "would_dispatch" || len(result.PipelineRetry.Results) != 1 {
		t.Fatalf("pipeline repair retry = %+v", result.PipelineRetry)
	}
	retryRow := result.PipelineRetry.Results[0]
	if retryRow.JobID != "squ-921" || retryRow.StepID != "implement" || retryRow.Preview == nil || retryRow.Preview.Dispatch == nil {
		t.Fatalf("pipeline repair retry row = %+v", retryRow)
	}
	payload := retryRow.Preview.Dispatch.Preview.Payload
	if payload["runtime"] != "codex" || payload["runtime_binary"] != "codex-dev" {
		t.Fatalf("pipeline repair retry payload = %+v", payload)
	}
	if result.Advance.Action != "none" {
		t.Fatalf("pipeline repair advance = %+v", result.Advance)
	}
	if strings.Contains(dryOut.String(), "ops-921") || strings.Contains(dryOut.String(), "q-ops-repair") {
		t.Fatalf("pipeline repair leaked other pipeline:\n%s", dryOut.String())
	}
	unchanged, err := job.Read(teamDir, "squ-921")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.Status != job.StatusFailed || unchanged.Steps[0].Status != job.StatusFailed || unchanged.Steps[0].Instance != "worker-squ-921" {
		t.Fatalf("dry-run mutated job = %+v", unchanged)
	}
	unchangedRunning, err := job.Read(teamDir, "squ-922")
	if err != nil {
		t.Fatalf("read unchanged running job: %v", err)
	}
	if unchangedRunning.Status != job.StatusRunning || unchangedRunning.Steps[0].Instance != "worker-squ-922" {
		t.Fatalf("dry-run mutated running job = %+v", unchangedRunning)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{
		"pipeline", "repair", "ticket_to_pr",
		"--repo", root,
		"--dry-run",
		"--timeout-pipelines",
		"--timeout-message-file", timeoutMessageFile,
		"--retry-pipelines",
		"--retry-message-file", retryMessageFile,
		"--preview-routes",
		"--skip-daemon",
		"--runtime", "codex",
		"--runtime-bin", "codex-dev",
		"--commands",
	})
	if err := commands.Execute(); err != nil {
		t.Fatalf("pipeline repair dry-run commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{
		"agent-team", "pipeline", "repair", "ticket_to_pr",
		"--repo", root,
		"--runtime", "codex",
		"--runtime-bin", "codex-dev",
		"--skip-daemon",
		"--timeout-pipelines",
		"--retry-pipelines",
		"--timeout-message-file", timeoutMessageFile,
		"--retry-message-file", retryMessageFile,
	}), " ")
	if got := strings.TrimSpace(commandsOut.String()); got != wantCommand {
		t.Fatalf("pipeline repair dry-run commands = %q, want %q", got, wantCommand)
	}
}

func TestPipelineRepairWaitsForRepairedJobs(t *testing.T) {
	root, mgr, cleanup := setupManualGateApprovalRepo(t, false)
	defer cleanup()
	teamDir := filepath.Join(root, ".agent_team")
	writeFailedRetryJob(t, teamDir, "squ-923")
	writeReadyAdvanceJob(t, teamDir, "squ-924")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"pipeline", "repair", "ticket_to_pr",
		"--repo", root,
		"--workspace", "repo",
		"--skip-queue",
		"--retry-pipelines",
		"--wait",
		"--wait-status", "running",
		"--wait-next-state", "running",
		"--wait-step", "implement",
		"--wait-timeout", "2s",
		"--wait-interval", "10ms",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline repair --wait: %v\nstderr=%s", err, stderr.String())
	}
	var result pipelineRepairResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode pipeline repair wait json: %v\nbody=%s", err, out.String())
	}
	if result.Pipeline != "ticket_to_pr" || result.DryRun {
		t.Fatalf("pipeline repair wait result = %+v", result)
	}
	if result.PipelineRetry.Action != "retried" || len(result.PipelineRetry.Results) != 1 {
		t.Fatalf("pipeline repair retry = %+v", result.PipelineRetry)
	}
	retryRow := result.PipelineRetry.Results[0]
	if retryRow.JobID != "squ-923" || retryRow.Action != "dispatched" || retryRow.Job == nil || retryRow.Job.Status != job.StatusRunning || retryRow.Job.LastEvent != "advance_dispatched" {
		t.Fatalf("pipeline repair retry row = %+v", retryRow)
	}
	if retryRow.Step == nil || retryRow.Step.ID != "implement" || retryRow.Step.Status != job.StatusRunning || retryRow.Step.Instance != "worker-squ-923-implement" {
		t.Fatalf("pipeline repair retry step = %+v", retryRow.Step)
	}
	if result.Advance.Action != "advanced" || len(result.Advance.Results) != 1 {
		t.Fatalf("pipeline repair advance = %+v", result.Advance)
	}
	advanceRow := result.Advance.Results[0]
	if advanceRow.JobID != "squ-924" || advanceRow.Action != "advanced" || advanceRow.Job == nil || advanceRow.Job.Status != job.StatusRunning || advanceRow.Job.LastEvent != "advance_dispatched" {
		t.Fatalf("pipeline repair advance row = %+v", advanceRow)
	}
	if advanceRow.Step == nil || advanceRow.Step.ID != "implement" || advanceRow.Step.Status != job.StatusRunning || advanceRow.Step.Instance != "worker-squ-924-implement" {
		t.Fatalf("pipeline repair advance step = %+v", advanceRow.Step)
	}
	if len(result.StatusAfter) != 1 || result.StatusAfter[0].Pipeline != "ticket_to_pr" || result.StatusAfter[0].Running != 2 || result.StatusAfter[0].RunningSteps != 2 {
		t.Fatalf("pipeline repair status after = %+v", result.StatusAfter)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-923-implement")
	stopAndWaitForTest(t, mgr, "worker-squ-924-implement")
}

func TestPipelineTickDryRunScopesQueueAndPreviewRoutes(t *testing.T) {
	root, _, cleanup := setupManualGateApprovalRepo(t, false)
	defer cleanup()
	teamDir := filepath.Join(root, ".agent_team")
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-306",
			Ticket:    "SQU-306",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "ops-306",
			Ticket:    "OPS-306",
			Target:    "worker",
			Pipeline:  "ops_review",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-pipeline-tick-preview",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-pipeline-tick-preview",
			Payload:    map[string]any{"target": "worker", "name": "worker-pipeline-tick-preview", "job_id": "squ-306", "ticket": "SQU-306"},
			QueuedAt:   now.Add(-time.Minute),
			UpdatedAt:  now.Add(-time.Minute),
		},
		{
			ID:         "q-ops-tick-preview",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-ops-tick-preview",
			Payload:    map[string]any{"target": "worker", "name": "worker-ops-tick-preview", "job_id": "ops-306", "ticket": "OPS-306"},
			QueuedAt:   now.Add(-time.Minute),
			UpdatedAt:  now.Add(-time.Minute),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("write queue item %s: %v", item.ID, err)
		}
	}
	writeReadyAdvanceJob(t, teamDir, "squ-307")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"pipeline", "tick", "ticket_to_pr",
		"--repo", root,
		"--workspace", "repo",
		"--dry-run",
		"--preview-routes",
		"--runtime", "codex",
		"--runtime-bin", "codex-dev",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline tick dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var result pipelineTickResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode pipeline tick dry-run json: %v\nbody=%s", err, out.String())
	}
	if result.Pipeline != "ticket_to_pr" || !result.Tick.DryRun {
		t.Fatalf("pipeline tick result = %+v", result)
	}
	if result.Tick.Queue == nil || !result.Tick.Queue.DryRun || result.Tick.Queue.WouldDispatch != 1 || result.Tick.Queue.Pending != 1 || len(result.Tick.Queue.Outcomes) != 1 || result.Tick.Queue.Outcomes[0].InstanceID != "worker-pipeline-tick-preview" {
		t.Fatalf("pipeline tick queue preview = %+v", result.Tick.Queue)
	}
	if len(result.Tick.Advance) != 1 || result.Tick.Advance[0].JobID != "squ-307" || result.Tick.Advance[0].Action != "would_advance" || !result.Tick.Advance[0].DryRun || result.Tick.Advance[0].Preview == nil {
		t.Fatalf("pipeline tick advance preview = %+v", result.Tick.Advance)
	}
	dispatch := result.Tick.Advance[0].Preview.Dispatch
	if dispatch == nil || dispatch.Preview == nil {
		t.Fatalf("pipeline tick dispatch preview = %+v", result.Tick.Advance[0].Preview)
	}
	payload := dispatch.Preview.Payload
	if payload["job_id"] != "squ-307" || payload["pipeline"] != "ticket_to_pr" || payload["pipeline_step"] != "implement" || payload["workspace"] != "repo" || payload["runtime"] != "codex" || payload["runtime_binary"] != "codex-dev" {
		t.Fatalf("pipeline tick route preview payload = %+v", payload)
	}
	if strings.Contains(out.String(), "ops-306") || strings.Contains(out.String(), "q-ops-tick-preview") {
		t.Fatalf("pipeline tick dry-run leaked other pipeline:\n%s", out.String())
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-pipeline-tick-preview"); err != nil {
		t.Fatalf("pipeline tick dry-run removed queue item: %v", err)
	}
	unchanged, err := job.Read(teamDir, "squ-307")
	if err != nil {
		t.Fatalf("read dry-run job: %v", err)
	}
	if unchanged.Status != job.StatusQueued || len(unchanged.Steps) != 1 || unchanged.Steps[0].Status != job.StatusBlocked {
		t.Fatalf("pipeline tick dry-run mutated job = %+v", unchanged)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{
		"pipeline", "tick", "ticket_to_pr",
		"--repo", root,
		"--workspace", "repo",
		"--dry-run",
		"--preview-routes",
		"--runtime", "codex",
		"--runtime-bin", "codex-dev",
		"--limit", "2",
		"--commands",
	})
	if err := commands.Execute(); err != nil {
		t.Fatalf("pipeline tick dry-run commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "pipeline", "tick", "ticket_to_pr", "--repo", root, "--workspace", "repo", "--runtime", "codex", "--runtime-bin", "codex-dev", "--limit", "2"}), " ")
	if got := strings.TrimSpace(commandsOut.String()); got != wantCommand {
		t.Fatalf("pipeline tick dry-run commands = %q, want %q", got, wantCommand)
	}

	idleCommands := NewRootCmd()
	idleCommandsOut, idleCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	idleCommands.SetOut(idleCommandsOut)
	idleCommands.SetErr(idleCommandsErr)
	idleCommands.SetArgs([]string{"pipeline", "tick", "ticket_to_pr", "--repo", root, "--dry-run", "--skip-drain", "--skip-advance", "--commands"})
	if err := idleCommands.Execute(); err != nil {
		t.Fatalf("idle pipeline tick dry-run commands: %v\nstderr=%s", err, idleCommandsErr.String())
	}
	if got := strings.TrimSpace(idleCommandsOut.String()); got != "" {
		t.Fatalf("idle pipeline tick dry-run commands = %q, want no output", got)
	}
}

func TestPipelineTickScopesQueueAndWaitsForAdvancedJobs(t *testing.T) {
	root, mgr, cleanup := setupManualGateApprovalRepo(t, false)
	defer cleanup()
	teamDir := filepath.Join(root, ".agent_team")
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-308",
			Ticket:    "SQU-308",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "ops-308",
			Ticket:    "OPS-308",
			Target:    "worker",
			Pipeline:  "ops_review",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-pipeline-tick",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-pipeline-tick",
			Payload:    map[string]any{"target": "worker", "name": "worker-pipeline-tick", "job_id": "squ-308", "ticket": "SQU-308"},
			QueuedAt:   now.Add(-time.Minute),
			UpdatedAt:  now.Add(-time.Minute),
		},
		{
			ID:         "q-ops-tick",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-ops-tick",
			Payload:    map[string]any{"target": "worker", "name": "worker-ops-tick", "job_id": "ops-308", "ticket": "OPS-308"},
			QueuedAt:   now.Add(-time.Minute),
			UpdatedAt:  now.Add(-time.Minute),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("write queue item %s: %v", item.ID, err)
		}
	}
	writeReadyAdvanceJob(t, teamDir, "squ-309")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"pipeline", "tick", "ticket_to_pr",
		"--repo", root,
		"--workspace", "repo",
		"--wait",
		"--wait-status", "running",
		"--wait-next-state", "running",
		"--wait-step", "implement",
		"--wait-timeout", "2s",
		"--wait-interval", "10ms",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline tick --wait: %v\nstderr=%s", err, stderr.String())
	}
	var result pipelineTickResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode pipeline tick wait json: %v\nbody=%s", err, out.String())
	}
	if result.Pipeline != "ticket_to_pr" || result.Tick.DryRun {
		t.Fatalf("pipeline tick result = %+v", result)
	}
	if result.Tick.Queue == nil || result.Tick.Queue.Dispatched != 1 {
		t.Fatalf("pipeline tick queue = %+v", result.Tick.Queue)
	}
	if len(result.Tick.Advance) != 1 || result.Tick.Advance[0].JobID != "squ-309" || result.Tick.Advance[0].Action != "advanced" || result.Tick.Advance[0].Job == nil || result.Tick.Advance[0].Job.Status != job.StatusRunning || result.Tick.Advance[0].Job.LastEvent != "advance_dispatched" {
		t.Fatalf("pipeline tick advance = %+v", result.Tick.Advance)
	}
	if result.Tick.Advance[0].Step == nil || result.Tick.Advance[0].Step.ID != "implement" || result.Tick.Advance[0].Step.Status != job.StatusRunning || result.Tick.Advance[0].Step.Instance != "worker-squ-309-implement" {
		t.Fatalf("pipeline tick advance step = %+v", result.Tick.Advance[0].Step)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-pipeline-tick"); !os.IsNotExist(err) {
		t.Fatalf("pipeline queue item still exists or unexpected err=%v", err)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-ops-tick"); err != nil {
		t.Fatalf("foreign queue item changed: %v", err)
	}
	stopAndWaitForTest(t, mgr, "worker-pipeline-tick")
	stopAndWaitForTest(t, mgr, "worker-squ-309-implement")
}

func TestPipelineTickRejectsInvalidFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "format with json",
			args: []string{"pipeline", "tick", "ticket_to_pr", "--format", "{{.Pipeline}}", "--json"},
			want: "--format cannot be combined with --json",
		},
		{
			name: "negative wait timeout",
			args: []string{"pipeline", "tick", "ticket_to_pr", "--wait", "--wait-timeout", "-1s"},
			want: "--wait-timeout must be >= 0",
		},
		{
			name: "negative wait interval",
			args: []string{"pipeline", "tick", "ticket_to_pr", "--wait", "--wait-interval", "-1s"},
			want: "--wait-interval must be >= 0",
		},
		{
			name: "wait dry run",
			args: []string{"pipeline", "tick", "ticket_to_pr", "--wait", "--dry-run"},
			want: "--wait cannot be combined with --dry-run",
		},
		{
			name: "wait skip advance",
			args: []string{"pipeline", "tick", "ticket_to_pr", "--wait", "--skip-advance"},
			want: "--wait requires pipeline advancement",
		},
		{
			name: "wait flag without wait",
			args: []string{"pipeline", "tick", "ticket_to_pr", "--wait-status", "running"},
			want: "wait-related flags require --wait",
		},
		{
			name: "wait next-state without wait",
			args: []string{"pipeline", "tick", "ticket_to_pr", "--wait-next-state", "running"},
			want: "wait-related flags require --wait",
		},
		{
			name: "wait step without wait",
			args: []string{"pipeline", "tick", "ticket_to_pr", "--wait-step", "implement"},
			want: "wait-related flags require --wait",
		},
		{
			name: "invalid wait next-state",
			args: []string{"pipeline", "tick", "ticket_to_pr", "--wait", "--wait-next-state", "missing"},
			want: "--wait-next-state must be ready, queued, running, blocked, failed, held, done, none, or all",
		},
		{
			name: "preview without dry run",
			args: []string{"pipeline", "tick", "ticket_to_pr", "--preview-routes"},
			want: "--preview-routes requires --dry-run",
		},
		{
			name: "commands requires dry run",
			args: []string{"pipeline", "tick", "ticket_to_pr", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "commands rejects json",
			args: []string{"pipeline", "tick", "ticket_to_pr", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "commands rejects format",
			args: []string{"pipeline", "tick", "ticket_to_pr", "--dry-run", "--commands", "--format", "{{.Pipeline}}"},
			want: "--commands cannot be combined with --format",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			stderr := &bytes.Buffer{}
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil {
				t.Fatalf("pipeline tick %s succeeded", tc.name)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestPipelineDrainScopesQueueAndWaitsForAdvancedJobs(t *testing.T) {
	root, mgr, cleanup := setupManualGateApprovalRepo(t, false)
	defer cleanup()
	teamDir := filepath.Join(root, ".agent_team")
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-304",
			Ticket:    "SQU-304",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "ops-304",
			Ticket:    "OPS-304",
			Target:    "worker",
			Pipeline:  "ops_review",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-pipeline-drain",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-pipeline-drain",
			Payload:    map[string]any{"target": "worker", "name": "worker-pipeline-drain", "job_id": "squ-304", "ticket": "SQU-304"},
			QueuedAt:   now.Add(-time.Minute),
			UpdatedAt:  now.Add(-time.Minute),
		},
		{
			ID:         "q-ops-drain",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-ops-drain",
			Payload:    map[string]any{"target": "worker", "name": "worker-ops-drain", "job_id": "ops-304", "ticket": "OPS-304"},
			QueuedAt:   now.Add(-time.Minute),
			UpdatedAt:  now.Add(-time.Minute),
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("write queue item %s: %v", item.ID, err)
		}
	}
	writeReadyAdvanceJob(t, teamDir, "squ-305")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"pipeline", "drain", "ticket_to_pr",
		"--repo", root,
		"--workspace", "repo",
		"--wait",
		"--wait-status", "running",
		"--wait-next-state", "running",
		"--wait-step", "implement",
		"--wait-timeout", "2s",
		"--wait-interval", "10ms",
		"--interval", "0s",
		"--max-cycles", "3",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline drain --wait: %v\nstderr=%s", err, stderr.String())
	}
	var result pipelineDrainResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode pipeline drain wait json: %v\nbody=%s", err, out.String())
	}
	if result.Pipeline != "ticket_to_pr" || !result.Idle || result.HitLimit || result.CyclesRun != 2 || len(result.Cycles) != 2 {
		t.Fatalf("pipeline drain result = %+v", result)
	}
	if result.Cycles[0].Tick.Queue == nil || result.Cycles[0].Tick.Queue.Dispatched != 1 {
		t.Fatalf("first pipeline drain queue = %+v", result.Cycles[0].Tick.Queue)
	}
	if result.Cycles[1].Tick.Queue == nil || result.Cycles[1].Tick.Queue.Dispatched != 0 || result.Cycles[1].Tick.Queue.Pending != 0 {
		t.Fatalf("second pipeline drain queue = %+v", result.Cycles[1].Tick.Queue)
	}
	if len(result.Cycles[0].Tick.Advance) != 1 || result.Cycles[0].Tick.Advance[0].Action != "advanced" || result.Cycles[0].Tick.Advance[0].Job == nil || result.Cycles[0].Tick.Advance[0].Job.Status != job.StatusRunning || result.Cycles[0].Tick.Advance[0].Job.LastEvent != "advance_dispatched" {
		t.Fatalf("pipeline drain advance = %+v", result.Cycles[0].Tick.Advance)
	}
	if result.Cycles[0].Tick.Advance[0].Step == nil || result.Cycles[0].Tick.Advance[0].Step.ID != "implement" || result.Cycles[0].Tick.Advance[0].Step.Status != job.StatusRunning || result.Cycles[0].Tick.Advance[0].Step.Instance != "worker-squ-305-implement" {
		t.Fatalf("pipeline drain advance step = %+v", result.Cycles[0].Tick.Advance[0].Step)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-pipeline-drain"); !os.IsNotExist(err) {
		t.Fatalf("pipeline queue item still exists or unexpected err=%v", err)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-ops-drain"); err != nil {
		t.Fatalf("foreign queue item changed: %v", err)
	}
	stopAndWaitForTest(t, mgr, "worker-pipeline-drain")
	stopAndWaitForTest(t, mgr, "worker-squ-305-implement")
}

func TestPipelineDrainRejectsInvalidFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "negative interval",
			args: []string{"pipeline", "drain", "ticket_to_pr", "--interval", "-1s"},
			want: "--interval must be >= 0",
		},
		{
			name: "zero max cycles",
			args: []string{"pipeline", "drain", "ticket_to_pr", "--max-cycles", "0"},
			want: "--max-cycles must be > 0",
		},
		{
			name: "format with json",
			args: []string{"pipeline", "drain", "ticket_to_pr", "--format", "{{.CyclesRun}}", "--json"},
			want: "--format cannot be combined with --json",
		},
		{
			name: "negative wait timeout",
			args: []string{"pipeline", "drain", "ticket_to_pr", "--wait", "--wait-timeout", "-1s"},
			want: "--wait-timeout must be >= 0",
		},
		{
			name: "negative wait interval",
			args: []string{"pipeline", "drain", "ticket_to_pr", "--wait", "--wait-interval", "-1s"},
			want: "--wait-interval must be >= 0",
		},
		{
			name: "wait skip advance",
			args: []string{"pipeline", "drain", "ticket_to_pr", "--wait", "--skip-advance"},
			want: "--wait requires pipeline advancement",
		},
		{
			name: "wait flag without wait",
			args: []string{"pipeline", "drain", "ticket_to_pr", "--wait-status", "running"},
			want: "wait-related flags require --wait",
		},
		{
			name: "wait next-state without wait",
			args: []string{"pipeline", "drain", "ticket_to_pr", "--wait-next-state", "running"},
			want: "wait-related flags require --wait",
		},
		{
			name: "wait step without wait",
			args: []string{"pipeline", "drain", "ticket_to_pr", "--wait-step", "implement"},
			want: "wait-related flags require --wait",
		},
		{
			name: "invalid wait next-state",
			args: []string{"pipeline", "drain", "ticket_to_pr", "--wait", "--wait-next-state", "missing"},
			want: "--wait-next-state must be ready, queued, running, blocked, failed, held, done, none, or all",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			stderr := &bytes.Buffer{}
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil {
				t.Fatalf("pipeline drain %s succeeded", tc.name)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestPipelineQueueQuarantineScopesOwnedFiles(t *testing.T) {
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
id = "audit"
target = "worker"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, j := range []*job.Job{
		{
			ID:        "squ-902",
			Ticket:    "SQU-902",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Instance:  "worker-squ-902",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "squ-903",
			Ticket:    "SQU-903",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Instance:  "worker-squ-903",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "ops-902",
			Ticket:    "OPS-902",
			Target:    "worker",
			Pipeline:  "ops_review",
			Instance:  "worker-ops-902",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "adhoc-902",
			Ticket:    "ADHOC-902",
			Target:    "worker",
			Instance:  "worker-adhoc-902",
			Status:    job.StatusQueued,
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}

	stamp := "20260619T020000.000000000Z"
	writeQuarantinedQueueItem(t, teamDir, stamp, daemon.QueueStatePending, &daemon.QueueItem{
		ID:         "q-pipeline-quarantined",
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-902",
		Payload:    map[string]any{"job_id": "squ-902", "ticket": "SQU-902", "target": "worker"},
		QueuedAt:   now.Add(-2 * time.Hour),
		UpdatedAt:  now.Add(-time.Hour),
	})
	writeQuarantinedQueueItem(t, teamDir, stamp, daemon.QueueStatePending, &daemon.QueueItem{
		ID:         "q-pipeline-quarantined-extra",
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-903",
		Payload:    map[string]any{"job_id": "squ-903", "ticket": "SQU-903", "target": "worker"},
		Attempts:   2,
		QueuedAt:   now.Add(-2 * time.Hour),
		UpdatedAt:  now.Add(-time.Hour),
	})
	writeQuarantinedQueueItem(t, teamDir, stamp, daemon.QueueStateDead, &daemon.QueueItem{
		ID:        "q-pipeline-unrestorable",
		EventType: "agent.dispatch",
		Instance:  "worker",
		Payload:   map[string]any{"job_id": "squ-902", "ticket": "SQU-902", "target": "worker"},
		QueuedAt:  now.Add(-3 * time.Hour),
		UpdatedAt: now.Add(-2 * time.Hour),
	})
	writeQuarantinedQueueItem(t, teamDir, stamp, daemon.QueueStateDead, &daemon.QueueItem{
		ID:             "q-foreign-quarantined",
		EventType:      "agent.dispatch",
		Instance:       "worker",
		InstanceID:     "worker-ops-902",
		Payload:        map[string]any{"job_id": "ops-902", "ticket": "OPS-902", "target": "worker"},
		Attempts:       daemon.MaxQueueAttempts,
		LastError:      "foreign",
		QueuedAt:       now.Add(-4 * time.Hour),
		UpdatedAt:      now.Add(-3 * time.Hour),
		DeadLetteredAt: now.Add(-3 * time.Hour),
	})
	writeQuarantinedQueueItem(t, teamDir, stamp, daemon.QueueStateDead, &daemon.QueueItem{
		ID:             "q-adhoc-quarantined",
		EventType:      "agent.dispatch",
		Instance:       "worker",
		InstanceID:     "worker-adhoc-902",
		Payload:        map[string]any{"job_id": "adhoc-902", "ticket": "ADHOC-902", "target": "worker"},
		Attempts:       daemon.MaxQueueAttempts,
		LastError:      "adhoc",
		QueuedAt:       now.Add(-4 * time.Hour),
		UpdatedAt:      now.Add(-3 * time.Hour),
		DeadLetteredAt: now.Add(-3 * time.Hour),
	})
	restorePath := filepath.Join("quarantine", stamp, daemon.QueueStatePending, "q-pipeline-quarantined.json")
	unrestorablePath := filepath.Join("quarantine", stamp, daemon.QueueStateDead, "q-pipeline-unrestorable.json")
	foreignPath := filepath.Join("quarantine", stamp, daemon.QueueStateDead, "q-foreign-quarantined.json")

	list := NewRootCmd()
	listOut, listErr := &bytes.Buffer{}, &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetErr(listErr)
	list.SetArgs([]string{"pipeline", "queue", "quarantine", "ticket_to_pr", "--repo", root, "--json"})
	if err := list.Execute(); err != nil {
		t.Fatalf("pipeline queue quarantine list: %v\nstderr=%s", err, listErr.String())
	}
	var listed []queueQuarantineItem
	if err := json.Unmarshal(listOut.Bytes(), &listed); err != nil {
		t.Fatalf("decode pipeline quarantine list: %v\nbody=%s", err, listOut.String())
	}
	if got := queueQuarantineItemIDs(listed); got != "q-pipeline-quarantined,q-pipeline-quarantined-extra,q-pipeline-unrestorable" {
		t.Fatalf("listed pipeline quarantined items = %s\nbody=%s", got, listOut.String())
	}

	summary := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(summaryOut)
	summary.SetErr(summaryErr)
	summary.SetArgs([]string{"pipeline", "queue", "quarantine", "ticket_to_pr", "--repo", root, "--summary", "--json"})
	if err := summary.Execute(); err != nil {
		t.Fatalf("pipeline queue quarantine summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var summaryBody queueQuarantineSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &summaryBody); err != nil {
		t.Fatalf("decode pipeline queue quarantine summary: %v\nbody=%s", err, summaryOut.String())
	}
	if summaryBody.Quarantined != 3 || summaryBody.Restorable != 2 || summaryBody.Unrestorable != 1 || summaryBody.States[daemon.QueueStatePending] != 2 || summaryBody.States[daemon.QueueStateDead] != 1 || summaryBody.Jobs["squ-902"] != 2 || summaryBody.Jobs["squ-903"] != 1 {
		t.Fatalf("pipeline queue quarantine summary = %+v", summaryBody)
	}

	summaryText := NewRootCmd()
	summaryTextOut, summaryTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	summaryText.SetOut(summaryTextOut)
	summaryText.SetErr(summaryTextErr)
	summaryText.SetArgs([]string{"pipeline", "queue", "quarantine", "ticket_to_pr", "--repo", root, "--restorable", "--summary"})
	if err := summaryText.Execute(); err != nil {
		t.Fatalf("pipeline queue quarantine summary text: %v\nstderr=%s", err, summaryTextErr.String())
	}
	if got, want := summaryTextOut.String(), "queue quarantine: quarantined=2 restorable=2 unrestorable=0\n"; got != want {
		t.Fatalf("pipeline queue quarantine summary text = %q, want %q", got, want)
	}

	invalidSummary := NewRootCmd()
	invalidSummaryOut, invalidSummaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidSummary.SetOut(invalidSummaryOut)
	invalidSummary.SetErr(invalidSummaryErr)
	invalidSummary.SetArgs([]string{"pipeline", "queue", "quarantine", "ticket_to_pr", "--repo", root, "--summary", "--limit", "1"})
	if err := invalidSummary.Execute(); err == nil {
		t.Fatalf("pipeline queue quarantine summary accepted --limit; stdout=%s stderr=%s", invalidSummaryOut.String(), invalidSummaryErr.String())
	}
	if !strings.Contains(invalidSummaryErr.String(), "--sort and --limit cannot be combined with --summary") {
		t.Fatalf("pipeline queue quarantine summary invalid stderr = %q", invalidSummaryErr.String())
	}

	allList := NewRootCmd()
	allListOut, allListErr := &bytes.Buffer{}, &bytes.Buffer{}
	allList.SetOut(allListOut)
	allList.SetErr(allListErr)
	allList.SetArgs([]string{"pipeline", "queue", "quarantine", "--repo", root, "--sort", "id", "--json"})
	if err := allList.Execute(); err != nil {
		t.Fatalf("pipeline queue quarantine all list: %v\nstderr=%s", err, allListErr.String())
	}
	listed = nil
	if err := json.Unmarshal(allListOut.Bytes(), &listed); err != nil {
		t.Fatalf("decode pipeline quarantine all list: %v\nbody=%s", err, allListOut.String())
	}
	if got := queueQuarantineItemIDs(listed); got != "q-foreign-quarantined,q-pipeline-quarantined,q-pipeline-quarantined-extra,q-pipeline-unrestorable" {
		t.Fatalf("listed all pipeline quarantined items = %s\nbody=%s", got, allListOut.String())
	}

	allRestorable := NewRootCmd()
	allRestorableOut, allRestorableErr := &bytes.Buffer{}, &bytes.Buffer{}
	allRestorable.SetOut(allRestorableOut)
	allRestorable.SetErr(allRestorableErr)
	allRestorable.SetArgs([]string{"pipeline", "queue", "quarantine", "--all", "--repo", root, "--restorable", "--sort", "id", "--format", "{{.ID}} {{.Job}}"})
	if err := allRestorable.Execute(); err != nil {
		t.Fatalf("pipeline queue quarantine --all restorable: %v\nstderr=%s", err, allRestorableErr.String())
	}
	if got, want := allRestorableOut.String(), "q-foreign-quarantined ops-902\nq-pipeline-quarantined squ-902\nq-pipeline-quarantined-extra squ-903\n"; got != want {
		t.Fatalf("pipeline quarantine --all restorable = %q, want %q", got, want)
	}

	listSorted := NewRootCmd()
	listSortedOut, listSortedErr := &bytes.Buffer{}, &bytes.Buffer{}
	listSorted.SetOut(listSortedOut)
	listSorted.SetErr(listSortedErr)
	listSorted.SetArgs([]string{"pipeline", "queue", "quarantine", "ticket_to_pr", "--repo", root, "--sort", "attempts", "--limit", "1", "--format", "{{.ID}}"})
	if err := listSorted.Execute(); err != nil {
		t.Fatalf("pipeline queue quarantine sorted limit list: %v\nstderr=%s", err, listSortedErr.String())
	}
	if got, want := listSortedOut.String(), "q-pipeline-quarantined-extra\n"; got != want {
		t.Fatalf("pipeline queue quarantine sorted limit list = %q, want %q", got, want)
	}

	restorable := NewRootCmd()
	restorableOut, restorableErr := &bytes.Buffer{}, &bytes.Buffer{}
	restorable.SetOut(restorableOut)
	restorable.SetErr(restorableErr)
	restorable.SetArgs([]string{"pipeline", "queue", "quarantine", "ticket_to_pr", "--repo", root, "--restorable", "--json"})
	if err := restorable.Execute(); err != nil {
		t.Fatalf("pipeline queue quarantine restorable list: %v\nstderr=%s", err, restorableErr.String())
	}
	var restorableRows []queueQuarantineItem
	if err := json.Unmarshal(restorableOut.Bytes(), &restorableRows); err != nil {
		t.Fatalf("decode restorable rows: %v\nbody=%s", err, restorableOut.String())
	}
	if got := queueQuarantineItemIDs(restorableRows); got != "q-pipeline-quarantined,q-pipeline-quarantined-extra" {
		t.Fatalf("restorable rows = %s\nbody=%s", got, restorableOut.String())
	}

	unrestorable := NewRootCmd()
	unrestorableOut, unrestorableErr := &bytes.Buffer{}, &bytes.Buffer{}
	unrestorable.SetOut(unrestorableOut)
	unrestorable.SetErr(unrestorableErr)
	unrestorable.SetArgs([]string{"pipeline", "queue", "quarantine", "ticket_to_pr", "--repo", root, "--unrestorable", "--json"})
	if err := unrestorable.Execute(); err != nil {
		t.Fatalf("pipeline queue quarantine unrestorable list: %v\nstderr=%s", err, unrestorableErr.String())
	}
	var unrestorableRows []queueQuarantineItem
	if err := json.Unmarshal(unrestorableOut.Bytes(), &unrestorableRows); err != nil {
		t.Fatalf("decode unrestorable rows: %v\nbody=%s", err, unrestorableOut.String())
	}
	if got := queueQuarantineItemIDs(unrestorableRows); got != "q-pipeline-unrestorable" {
		t.Fatalf("unrestorable rows = %s\nbody=%s", got, unrestorableOut.String())
	}

	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"pipeline", "queue", "quarantine", "show", "ticket_to_pr", restorePath, "--repo", root, "--format", "{{.Pipeline}} {{.ID}} {{.QueueItem.Instance}}"})
	if err := show.Execute(); err != nil {
		t.Fatalf("pipeline queue quarantine show: %v\nstderr=%s", err, showErr.String())
	}
	if got, want := showOut.String(), "ticket_to_pr q-pipeline-quarantined worker\n"; got != want {
		t.Fatalf("show format = %q, want %q", got, want)
	}

	showCommands := NewRootCmd()
	showCommandsOut, showCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	showCommands.SetOut(showCommandsOut)
	showCommands.SetErr(showCommandsErr)
	showCommands.SetArgs([]string{"pipeline", "queue", "quarantine", "show", "ticket_to_pr", restorePath, "--repo", root, "--commands"})
	if err := showCommands.Execute(); err != nil {
		t.Fatalf("pipeline queue quarantine show --commands: %v\nstderr=%s", err, showCommandsErr.String())
	}
	wantCommands := "agent-team pipeline queue quarantine restore ticket_to_pr " + restorePath + "\nagent-team pipeline queue quarantine drop ticket_to_pr " + restorePath + "\n"
	if got := showCommandsOut.String(); got != wantCommands {
		t.Fatalf("pipeline queue quarantine show --commands = %q, want %q", got, wantCommands)
	}

	showForeign := NewRootCmd()
	showForeignOut, showForeignErr := &bytes.Buffer{}, &bytes.Buffer{}
	showForeign.SetOut(showForeignOut)
	showForeign.SetErr(showForeignErr)
	showForeign.SetArgs([]string{"pipeline", "queue", "quarantine", "show", "ticket_to_pr", foreignPath, "--repo", root})
	if err := showForeign.Execute(); err == nil {
		t.Fatalf("pipeline queue quarantine foreign show succeeded: stdout=%s", showForeignOut.String())
	}
	if !strings.Contains(showForeignErr.String(), `not owned by pipeline "ticket_to_pr"`) {
		t.Fatalf("foreign show stderr = %q", showForeignErr.String())
	}

	restoreAll := NewRootCmd()
	restoreAllOut, restoreAllErr := &bytes.Buffer{}, &bytes.Buffer{}
	restoreAll.SetOut(restoreAllOut)
	restoreAll.SetErr(restoreAllErr)
	restoreAll.SetArgs([]string{"pipeline", "queue", "quarantine", "restore", "ticket_to_pr", "--repo", root, "--all", "--job", "SQU-902", "--dry-run", "--json"})
	if err := restoreAll.Execute(); err != nil {
		t.Fatalf("pipeline queue quarantine restore --all dry-run: %v\nstderr=%s", err, restoreAllErr.String())
	}
	var restoreRows []queueQuarantineRestoreResult
	if err := json.Unmarshal(restoreAllOut.Bytes(), &restoreRows); err != nil {
		t.Fatalf("decode restore rows: %v\nbody=%s", err, restoreAllOut.String())
	}
	if len(restoreRows) != 1 || restoreRows[0].ID != "q-pipeline-quarantined" || restoreRows[0].Action != "would_restore" {
		t.Fatalf("restore rows = %+v", restoreRows)
	}

	restoreAllCommands := NewRootCmd()
	restoreAllCommandsOut, restoreAllCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	restoreAllCommands.SetOut(restoreAllCommandsOut)
	restoreAllCommands.SetErr(restoreAllCommandsErr)
	restoreAllCommands.SetArgs([]string{"pipeline", "queue", "quarantine", "restore", "ticket_to_pr", "--repo", root, "--all", "--job", "SQU-902", "--dry-run", "--commands"})
	if err := restoreAllCommands.Execute(); err != nil {
		t.Fatalf("pipeline queue quarantine restore --all commands: %v\nstderr=%s", err, restoreAllCommandsErr.String())
	}
	wantRestoreAllCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "pipeline", "queue", "quarantine", "restore", "ticket_to_pr", "--repo", root, "--all", "--job", "SQU-902"}), " ")
	if got := strings.TrimSpace(restoreAllCommandsOut.String()); got != wantRestoreAllCommand {
		t.Fatalf("pipeline queue quarantine restore --all commands = %q, want %q", got, wantRestoreAllCommand)
	}

	restoreLimit := NewRootCmd()
	restoreLimitOut, restoreLimitErr := &bytes.Buffer{}, &bytes.Buffer{}
	restoreLimit.SetOut(restoreLimitOut)
	restoreLimit.SetErr(restoreLimitErr)
	restoreLimit.SetArgs([]string{"pipeline", "queue", "quarantine", "restore", "ticket_to_pr", "--repo", root, "--all", "--limit", "1", "--dry-run", "--format", "{{.ID}}"})
	if err := restoreLimit.Execute(); err != nil {
		t.Fatalf("pipeline queue quarantine restore --all limit dry-run: %v\nstderr=%s", err, restoreLimitErr.String())
	}
	if got, want := restoreLimitOut.String(), "q-pipeline-quarantined-extra\n"; got != want {
		t.Fatalf("pipeline restore --limit output = %q, want %q", got, want)
	}

	restoreSorted := NewRootCmd()
	restoreSortedOut, restoreSortedErr := &bytes.Buffer{}, &bytes.Buffer{}
	restoreSorted.SetOut(restoreSortedOut)
	restoreSorted.SetErr(restoreSortedErr)
	restoreSorted.SetArgs([]string{"pipeline", "queue", "quarantine", "restore", "ticket_to_pr", "--repo", root, "--all", "--sort", "attempts", "--limit", "1", "--dry-run", "--format", "{{.ID}}"})
	if err := restoreSorted.Execute(); err != nil {
		t.Fatalf("pipeline queue quarantine restore --all sorted limit dry-run: %v\nstderr=%s", err, restoreSortedErr.String())
	}
	if got, want := restoreSortedOut.String(), "q-pipeline-quarantined-extra\n"; got != want {
		t.Fatalf("pipeline restore --sort attempts --limit output = %q, want %q", got, want)
	}

	restoreOne := NewRootCmd()
	restoreOneOut, restoreOneErr := &bytes.Buffer{}, &bytes.Buffer{}
	restoreOne.SetOut(restoreOneOut)
	restoreOne.SetErr(restoreOneErr)
	restoreOne.SetArgs([]string{"pipeline", "queue", "quarantine", "restore", "ticket_to_pr", restorePath, "--repo", root, "--dry-run", "--json"})
	if err := restoreOne.Execute(); err != nil {
		t.Fatalf("pipeline queue quarantine restore one dry-run: %v\nstderr=%s", err, restoreOneErr.String())
	}
	var restoreOneRow queueQuarantineRestoreResult
	if err := json.Unmarshal(restoreOneOut.Bytes(), &restoreOneRow); err != nil {
		t.Fatalf("decode restore one row: %v\nbody=%s", err, restoreOneOut.String())
	}
	if restoreOneRow.ID != "q-pipeline-quarantined" || restoreOneRow.Action != "would_restore" {
		t.Fatalf("restore one row = %+v", restoreOneRow)
	}

	restoreOneCommands := NewRootCmd()
	restoreOneCommandsOut, restoreOneCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	restoreOneCommands.SetOut(restoreOneCommandsOut)
	restoreOneCommands.SetErr(restoreOneCommandsErr)
	restoreOneCommands.SetArgs([]string{"pipeline", "queue", "quarantine", "restore", "ticket_to_pr", restorePath, "--repo", root, "--dry-run", "--commands"})
	if err := restoreOneCommands.Execute(); err != nil {
		t.Fatalf("pipeline queue quarantine restore one commands: %v\nstderr=%s", err, restoreOneCommandsErr.String())
	}
	wantRestoreOneCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "pipeline", "queue", "quarantine", "restore", "ticket_to_pr", restorePath, "--repo", root}), " ")
	if got := strings.TrimSpace(restoreOneCommandsOut.String()); got != wantRestoreOneCommand {
		t.Fatalf("pipeline queue quarantine restore one commands = %q, want %q", got, wantRestoreOneCommand)
	}

	dropOne := NewRootCmd()
	dropOneOut, dropOneErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropOne.SetOut(dropOneOut)
	dropOne.SetErr(dropOneErr)
	dropOne.SetArgs([]string{"pipeline", "queue", "quarantine", "drop", "ticket_to_pr", restorePath, "--repo", root, "--dry-run", "--json"})
	if err := dropOne.Execute(); err != nil {
		t.Fatalf("pipeline queue quarantine drop one dry-run: %v\nstderr=%s", err, dropOneErr.String())
	}
	var dropRows []queueQuarantineDropResult
	if err := json.Unmarshal(dropOneOut.Bytes(), &dropRows); err != nil {
		t.Fatalf("decode drop one rows: %v\nbody=%s", err, dropOneOut.String())
	}
	if len(dropRows) != 1 || dropRows[0].ID != "q-pipeline-quarantined" || dropRows[0].Action != "would_drop" {
		t.Fatalf("drop one rows = %+v", dropRows)
	}

	dropOneCommands := NewRootCmd()
	dropOneCommandsOut, dropOneCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropOneCommands.SetOut(dropOneCommandsOut)
	dropOneCommands.SetErr(dropOneCommandsErr)
	dropOneCommands.SetArgs([]string{"pipeline", "queue", "quarantine", "drop", "ticket_to_pr", restorePath, "--repo", root, "--dry-run", "--commands"})
	if err := dropOneCommands.Execute(); err != nil {
		t.Fatalf("pipeline queue quarantine drop one commands: %v\nstderr=%s", err, dropOneCommandsErr.String())
	}
	wantDropOneCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "pipeline", "queue", "quarantine", "drop", "ticket_to_pr", restorePath, "--repo", root}), " ")
	if got := strings.TrimSpace(dropOneCommandsOut.String()); got != wantDropOneCommand {
		t.Fatalf("pipeline queue quarantine drop one commands = %q, want %q", got, wantDropOneCommand)
	}

	dropUnrestorable := NewRootCmd()
	dropUnrestorableOut, dropUnrestorableErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropUnrestorable.SetOut(dropUnrestorableOut)
	dropUnrestorable.SetErr(dropUnrestorableErr)
	dropUnrestorable.SetArgs([]string{"pipeline", "queue", "quarantine", "drop", "ticket_to_pr", "--repo", root, "--all", "--unrestorable", "--dry-run", "--format", "{{.ID}} {{.Action}} {{.Restorable}}"})
	if err := dropUnrestorable.Execute(); err != nil {
		t.Fatalf("pipeline queue quarantine drop unrestorable dry-run: %v\nstderr=%s", err, dropUnrestorableErr.String())
	}
	if got, want := dropUnrestorableOut.String(), "q-pipeline-unrestorable would_drop false\n"; got != want {
		t.Fatalf("drop unrestorable format = %q, want %q", got, want)
	}

	dropUnrestorableCommands := NewRootCmd()
	dropUnrestorableCommandsOut, dropUnrestorableCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropUnrestorableCommands.SetOut(dropUnrestorableCommandsOut)
	dropUnrestorableCommands.SetErr(dropUnrestorableCommandsErr)
	dropUnrestorableCommands.SetArgs([]string{"pipeline", "queue", "quarantine", "drop", "ticket_to_pr", "--repo", root, "--all", "--unrestorable", "--dry-run", "--commands"})
	if err := dropUnrestorableCommands.Execute(); err != nil {
		t.Fatalf("pipeline queue quarantine drop unrestorable commands: %v\nstderr=%s", err, dropUnrestorableCommandsErr.String())
	}
	wantDropUnrestorableCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "pipeline", "queue", "quarantine", "drop", "ticket_to_pr", "--repo", root, "--all", "--unrestorable"}), " ")
	if got := strings.TrimSpace(dropUnrestorableCommandsOut.String()); got != wantDropUnrestorableCommand {
		t.Fatalf("pipeline queue quarantine drop unrestorable commands = %q, want %q", got, wantDropUnrestorableCommand)
	}

	dropForeign := NewRootCmd()
	dropForeignOut, dropForeignErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropForeign.SetOut(dropForeignOut)
	dropForeign.SetErr(dropForeignErr)
	dropForeign.SetArgs([]string{"pipeline", "queue", "quarantine", "drop", "ticket_to_pr", foreignPath, "--repo", root, "--dry-run"})
	if err := dropForeign.Execute(); err == nil {
		t.Fatalf("pipeline queue quarantine foreign drop succeeded: stdout=%s", dropForeignOut.String())
	}
	if !strings.Contains(dropForeignErr.String(), `not owned by pipeline "ticket_to_pr"`) {
		t.Fatalf("foreign drop stderr = %q", dropForeignErr.String())
	}

	invalidMany := NewRootCmd()
	invalidManyOut, invalidManyErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidMany.SetOut(invalidManyOut)
	invalidMany.SetErr(invalidManyErr)
	invalidMany.SetArgs([]string{"pipeline", "queue", "quarantine", "ticket_to_pr", "ops_review", "--repo", root})
	if err := invalidMany.Execute(); err == nil {
		t.Fatalf("pipeline queue quarantine accepted multiple pipeline names: stdout=%s", invalidManyOut.String())
	}
	if !strings.Contains(invalidManyErr.String(), "pass at most one pipeline name") {
		t.Fatalf("multiple pipeline error = %q", invalidManyErr.String())
	}

	invalidAll := NewRootCmd()
	invalidAllOut, invalidAllErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidAll.SetOut(invalidAllOut)
	invalidAll.SetErr(invalidAllErr)
	invalidAll.SetArgs([]string{"pipeline", "queue", "quarantine", "ticket_to_pr", "--all", "--repo", root})
	if err := invalidAll.Execute(); err == nil {
		t.Fatalf("pipeline queue quarantine accepted --all with pipeline: stdout=%s", invalidAllOut.String())
	}
	if !strings.Contains(invalidAllErr.String(), "--all cannot be combined with a pipeline argument") {
		t.Fatalf("--all conflict error = %q", invalidAllErr.String())
	}

	if _, err := os.Stat(filepath.Join(daemon.QueueRoot(daemon.DaemonRoot(teamDir)), unrestorablePath)); err != nil {
		t.Fatalf("dry-run changed unrestorable file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(daemon.QueueRoot(daemon.DaemonRoot(teamDir)), foreignPath)); err != nil {
		t.Fatalf("dry-run changed foreign file: %v", err)
	}
}

func queueItemIDsForTest(items []daemon.QueueItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.ID)
	}
	return out
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
	if len(rows) != 1 || rows[0].Gate != job.StepGatePR || strings.Join(rows[0].WaitingFor, ",") != "pr" ||
		!containsString(rows[0].Actions, "agent-team job update squ-902 --pr <url> --advance --dry-run") ||
		!containsString(rows[0].Actions, "agent-team intake github --payload-file github-webhook.json --reconcile-job --advance --dry-run") {
		t.Fatalf("blocked ready rows = %+v", rows)
	}

	explainBlocked := NewRootCmd()
	explainBlockedOut, explainBlockedErr := &bytes.Buffer{}, &bytes.Buffer{}
	explainBlocked.SetOut(explainBlockedOut)
	explainBlocked.SetErr(explainBlockedErr)
	explainBlocked.SetArgs([]string{"job", "explain", "squ-902", "--repo", root, "--json"})
	if err := explainBlocked.Execute(); err != nil {
		t.Fatalf("job explain blocked: %v\nstderr=%s", err, explainBlockedErr.String())
	}
	var explained jobExplainResult
	if err := json.Unmarshal(explainBlockedOut.Bytes(), &explained); err != nil {
		t.Fatalf("decode explain blocked: %v\nbody=%s", err, explainBlockedOut.String())
	}
	if len(explained.Steps) != 2 ||
		!containsString(explained.Steps[1].Actions, "agent-team job update squ-902 --pr <url> --advance --dry-run") ||
		!containsString(explained.Steps[1].Actions, "agent-team intake github --payload-file github-webhook.json --reconcile-job --advance --dry-run") {
		t.Fatalf("blocked explain actions = %+v", explained)
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

	updatePreview := NewRootCmd()
	updatePreviewOut, updatePreviewErr := &bytes.Buffer{}, &bytes.Buffer{}
	updatePreview.SetOut(updatePreviewOut)
	updatePreview.SetErr(updatePreviewErr)
	updatePreview.SetArgs([]string{"job", "update", "squ-902", "--pr", "https://github.com/acme/app/pull/42", "--advance", "--dry-run", "--repo", root, "--json"})
	if err := updatePreview.Execute(); err != nil {
		t.Fatalf("job update pr advance dry-run: %v\nstderr=%s", err, updatePreviewErr.String())
	}
	var updateAdvance jobAdvancePreview
	if err := json.Unmarshal(updatePreviewOut.Bytes(), &updateAdvance); err != nil {
		t.Fatalf("decode update advance preview: %v\nbody=%s", err, updatePreviewOut.String())
	}
	if !updateAdvance.DryRun || updateAdvance.Step == nil || updateAdvance.Step.ID != "review" || updateAdvance.Dispatch == nil || updateAdvance.Dispatch.RequestedName != "manager-squ-902-review" {
		t.Fatalf("update advance preview = %+v", updateAdvance)
	}
	stillBlocked, err := job.Read(teamDir, "squ-902")
	if err != nil {
		t.Fatalf("read dry-run job: %v", err)
	}
	if stillBlocked.PR != "" {
		t.Fatalf("dry-run update mutated PR: %+v", stillBlocked)
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
	if len(ready.Actions) != 1 || ready.Actions[0] != "agent-team job advance squ-902" {
		t.Fatalf("ready PR actions = %+v", ready.Actions)
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
	kickoffPath := filepath.Join(root, "kickoff.txt")
	if err := os.WriteFile(kickoffPath, []byte("run pipeline from file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"pipeline", "run", "ticket_to_pr", "https://linear.app/squirtlesquad/issue/SQU-304/run-pipeline",
		"--repo", root,
		"--kickoff-file", kickoffPath,
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
	if created.Kickoff != "https://linear.app/squirtlesquad/issue/SQU-304/run-pipeline: run pipeline from file" {
		t.Fatalf("created kickoff = %q", created.Kickoff)
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

	commandsCmd := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commandsCmd.SetOut(commandsOut)
	commandsCmd.SetErr(commandsErr)
	commandsCmd.SetArgs([]string{
		"pipeline", "run", "ticket_to_pr", "SQU-309",
		"review", "runner",
		"--repo", root,
		"--id", "SQU 309 Custom",
		"--ticket-url", "https://linear.app/squirtlesquad/issue/SQU-309/review-runner",
		"--dry-run",
		"--commands",
	})
	if err := commandsCmd.Execute(); err != nil {
		t.Fatalf("pipeline run --commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCreateCommand := strings.Join(shellQuoteArgs([]string{
		"agent-team", "pipeline", "run", "ticket_to_pr", "SQU-309",
		"--repo", root,
		"--id", "SQU 309 Custom",
		"--ticket-url", "https://linear.app/squirtlesquad/issue/SQU-309/review-runner",
		"review", "runner",
	}), " ") + "\n"
	if got := commandsOut.String(); got != wantCreateCommand {
		t.Fatalf("pipeline run --commands = %q, want %q", got, wantCreateCommand)
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
	if payload["pipeline"] != "ticket_to_pr" || payload["pipeline_step"] != "implement" || payload["job_id"] != "squ-308" || payload["workspace"] != "worktree" {
		t.Fatalf("dispatch payload = %+v", payload)
	}
	if _, err := os.Stat(filepath.Join(root, ".agent_team", "jobs", "squ-308.toml")); !os.IsNotExist(err) {
		t.Fatalf("dispatch dry-run wrote pipeline job file, err=%v", err)
	}

	dispatchCommandsCmd := NewRootCmd()
	dispatchCommandsOut, dispatchCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	dispatchCommandsCmd.SetOut(dispatchCommandsOut)
	dispatchCommandsCmd.SetErr(dispatchCommandsErr)
	dispatchCommandsCmd.SetArgs([]string{
		"pipeline", "run", "ticket_to_pr", "SQU-310",
		"--repo", root,
		"--dispatch",
		"--workspace", "repo",
		"--runtime", "codex",
		"--runtime-bin", "codex-dev",
		"--kickoff", "quote 'this' safely",
		"--dry-run",
		"--commands",
	})
	if err := dispatchCommandsCmd.Execute(); err != nil {
		t.Fatalf("pipeline run --dispatch --commands: %v\nstderr=%s", err, dispatchCommandsErr.String())
	}
	wantDispatchCommand := strings.Join(shellQuoteArgs([]string{
		"agent-team", "pipeline", "run", "ticket_to_pr", "SQU-310",
		"--repo", root,
		"--dispatch",
		"--workspace", "repo",
		"--runtime", "codex",
		"--runtime-bin", "codex-dev",
		"--kickoff", "quote 'this' safely",
	}), " ") + "\n"
	if got := dispatchCommandsOut.String(); got != wantDispatchCommand {
		t.Fatalf("pipeline run --dispatch --commands = %q, want %q", got, wantDispatchCommand)
	}
}

func TestPipelineRunStepWorkspaceOverridesAuto(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.repo_worker]
trigger.event = "ticket.created"

[[pipelines.repo_worker.steps]]
id = "implement"
target = "worker"
workspace = "repo"
runtime = "codex"
runtime_bin = "codex-dev"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "run", "repo_worker", "SQU-312", "--repo", root, "--dry-run", "--dispatch", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline run step workspace dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var preview jobAdvancePreview
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil {
		t.Fatalf("decode step workspace preview: %v\nbody=%s", err, out.String())
	}
	if preview.Step == nil || preview.Step.Workspace != "repo" || preview.Step.Runtime != "codex" || preview.Step.RuntimeBin != "codex-dev" || len(preview.Job.Steps) != 1 || preview.Job.Steps[0].Workspace != "repo" || preview.Job.Steps[0].Runtime != "codex" || preview.Job.Steps[0].RuntimeBin != "codex-dev" {
		t.Fatalf("preview step workspace was not preserved: %+v", preview)
	}
	if got := preview.Dispatch.Preview.Payload["workspace"]; got != "repo" {
		t.Fatalf("dispatch workspace = %v, want repo; payload=%+v", got, preview.Dispatch.Preview.Payload)
	}
	if got := preview.Dispatch.Preview.Payload["runtime"]; got != "codex" {
		t.Fatalf("dispatch runtime = %v, want codex; payload=%+v", got, preview.Dispatch.Preview.Payload)
	}
	if got := preview.Dispatch.Preview.Payload["runtime_binary"]; got != "codex-dev" {
		t.Fatalf("dispatch runtime_binary = %v, want codex-dev; payload=%+v", got, preview.Dispatch.Preview.Payload)
	}

	override := NewRootCmd()
	overrideOut, overrideErr := &bytes.Buffer{}, &bytes.Buffer{}
	override.SetOut(overrideOut)
	override.SetErr(overrideErr)
	override.SetArgs([]string{"pipeline", "run", "repo_worker", "SQU-313", "--repo", root, "--dry-run", "--dispatch", "--workspace", "worktree", "--runtime", "claude", "--runtime-bin", "claude-dev", "--json"})
	if err := override.Execute(); err != nil {
		t.Fatalf("pipeline run explicit workspace dry-run: %v\nstderr=%s", err, overrideErr.String())
	}
	var overridden jobAdvancePreview
	if err := json.Unmarshal(overrideOut.Bytes(), &overridden); err != nil {
		t.Fatalf("decode overridden workspace preview: %v\nbody=%s", err, overrideOut.String())
	}
	if got := overridden.Dispatch.Preview.Payload["workspace"]; got != "worktree" {
		t.Fatalf("dispatch workspace = %v, want worktree; payload=%+v", got, overridden.Dispatch.Preview.Payload)
	}
	if got := overridden.Dispatch.Preview.Payload["runtime"]; got != "claude" {
		t.Fatalf("dispatch runtime = %v, want claude; payload=%+v", got, overridden.Dispatch.Preview.Payload)
	}
	if got := overridden.Dispatch.Preview.Payload["runtime_binary"]; got != "claude-dev" {
		t.Fatalf("dispatch runtime_binary = %v, want claude-dev; payload=%+v", got, overridden.Dispatch.Preview.Payload)
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

func TestPipelineRunDispatchWaitsForRequestedStatus(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "agent-team-pipeline-run-wait-")
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
	cmd.SetArgs([]string{
		"pipeline", "run", "ticket_to_pr", "SQU-315",
		"--repo", root,
		"--dispatch",
		"--workspace", "repo",
		"--wait",
		"--wait-status", "running",
		"--wait-next-state", "running",
		"--wait-step", "implement",
		"--wait-timeout", "2s",
		"--wait-interval", "10ms",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline run --dispatch --wait: %v\nstderr=%s", err, stderr.String())
	}
	var result jobAdvanceResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode pipeline run dispatch wait json: %v\nbody=%s", err, out.String())
	}
	if result.Job == nil || result.Step == nil {
		t.Fatalf("result missing job/step = %+v", result)
	}
	if result.Job.Status != job.StatusRunning || result.Job.Instance != "worker-squ-315-implement" || result.Job.LastEvent != "advance_dispatched" {
		t.Fatalf("waited job = %+v", result.Job)
	}
	if result.Step.ID != "implement" || result.Step.Status != job.StatusRunning || result.Step.Instance != "worker-squ-315-implement" {
		t.Fatalf("waited step = %+v", result.Step)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-315-implement")
}

func TestPipelineRunRejectsInvalidWaitFlags(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)
	cases := []struct {
		args []string
		want string
	}{
		{
			args: []string{"pipeline", "run", "ticket_to_pr", "SQU-316", "--repo", root, "--wait-next-state", "running"},
			want: "wait-related flags require --wait",
		},
		{
			args: []string{"pipeline", "run", "ticket_to_pr", "SQU-317", "--repo", root, "--wait-step", "implement"},
			want: "wait-related flags require --wait",
		},
		{
			args: []string{"pipeline", "run", "ticket_to_pr", "SQU-318", "--repo", root, "--wait", "--wait-next-state", "missing"},
			want: "--wait-next-state must be ready, queued, running, blocked, failed, held, done, none, or all",
		},
		{
			args: []string{"pipeline", "run", "ticket_to_pr", "SQU-319", "--repo", root, "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			args: []string{"pipeline", "run", "ticket_to_pr", "SQU-320", "--repo", root, "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			args: []string{"pipeline", "run", "ticket_to_pr", "SQU-321", "--repo", root, "--dry-run", "--commands", "--format", "{{.ID}}"},
			want: "--commands cannot be combined with --format",
		},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
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

func TestPipelineAdvanceAllReadyStepsDryRun(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-310",
		Ticket:    "SQU-310",
		Target:    "worker",
		Kickoff:   "SQU-310: parallel checks",
		Pipeline:  "parallel_checks",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "lint", Target: "worker", Status: job.StatusBlocked},
			{ID: "test", Target: "worker", Status: job.StatusBlocked},
			{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"lint", "test"}},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	one := NewRootCmd()
	oneOut, oneErr := &bytes.Buffer{}, &bytes.Buffer{}
	one.SetOut(oneOut)
	one.SetErr(oneErr)
	one.SetArgs([]string{"pipeline", "advance", "parallel_checks", "--repo", root, "--dry-run", "--json"})
	if err := one.Execute(); err != nil {
		t.Fatalf("pipeline advance dry-run: %v\nstderr=%s", err, oneErr.String())
	}
	var oneRows []pipelineAdvanceResult
	if err := json.Unmarshal(oneOut.Bytes(), &oneRows); err != nil {
		t.Fatalf("decode one-step rows: %v\nbody=%s", err, oneOut.String())
	}
	if len(oneRows) != 1 || oneRows[0].StepID != "lint" {
		t.Fatalf("default advance rows = %+v, want only first ready step", oneRows)
	}

	all := NewRootCmd()
	allOut, allErr := &bytes.Buffer{}, &bytes.Buffer{}
	all.SetOut(allOut)
	all.SetErr(allErr)
	all.SetArgs([]string{"pipeline", "advance", "parallel_checks", "--repo", root, "--dry-run", "--all-ready-steps", "--json"})
	if err := all.Execute(); err != nil {
		t.Fatalf("pipeline advance all-ready dry-run: %v\nstderr=%s", err, allErr.String())
	}
	var allRows []pipelineAdvanceResult
	if err := json.Unmarshal(allOut.Bytes(), &allRows); err != nil {
		t.Fatalf("decode all-ready rows: %v\nbody=%s", err, allOut.String())
	}
	if len(allRows) != 2 || allRows[0].StepID != "lint" || allRows[1].StepID != "test" {
		t.Fatalf("all-ready rows = %+v, want lint and test", allRows)
	}

	allCommands := NewRootCmd()
	allCommandsOut, allCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	allCommands.SetOut(allCommandsOut)
	allCommands.SetErr(allCommandsErr)
	allCommands.SetArgs([]string{"pipeline", "advance", "parallel_checks", "--repo", root, "--dry-run", "--all-ready-steps", "--limit", "1", "--commands"})
	if err := allCommands.Execute(); err != nil {
		t.Fatalf("pipeline advance all-ready --commands: %v\nstderr=%s", err, allCommandsErr.String())
	}
	wantAllReadyCommand := strings.Join(shellQuoteArgs([]string{
		"agent-team",
		"pipeline",
		"advance",
		"parallel_checks",
		"--repo",
		root,
		"--all-ready-steps",
		"--limit",
		"1",
	}), " ")
	if got := strings.TrimSpace(allCommandsOut.String()); got != wantAllReadyCommand {
		t.Fatalf("pipeline advance all-ready --commands output = %q, want %q", got, wantAllReadyCommand)
	}

	limited := NewRootCmd()
	limitedOut, limitedErr := &bytes.Buffer{}, &bytes.Buffer{}
	limited.SetOut(limitedOut)
	limited.SetErr(limitedErr)
	limited.SetArgs([]string{"pipeline", "advance", "parallel_checks", "--repo", root, "--dry-run", "--all-ready-steps", "--limit", "1", "--json"})
	if err := limited.Execute(); err != nil {
		t.Fatalf("pipeline advance all-ready limited dry-run: %v\nstderr=%s", err, limitedErr.String())
	}
	var limitedRows []pipelineAdvanceResult
	if err := json.Unmarshal(limitedOut.Bytes(), &limitedRows); err != nil {
		t.Fatalf("decode limited rows: %v\nbody=%s", err, limitedOut.String())
	}
	if len(limitedRows) != 1 || limitedRows[0].StepID != "lint" {
		t.Fatalf("limited rows = %+v, want first ready step", limitedRows)
	}
}

func TestPipelineAdvanceAllReadyStepsPreservesQueuedStepOrder(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
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
`), 0o644); err != nil {
		t.Fatal(err)
	}

	create := NewRootCmd()
	createOut, createErr := &bytes.Buffer{}, &bytes.Buffer{}
	create.SetOut(createOut)
	create.SetErr(createErr)
	create.SetArgs([]string{"pipeline", "run", "parallel_checks", "SQU-311", "--repo", root, "--json"})
	if err := create.Execute(); err != nil {
		t.Fatalf("pipeline run: %v\nstderr=%s", err, createErr.String())
	}

	ready := NewRootCmd()
	readyOut, readyErr := &bytes.Buffer{}, &bytes.Buffer{}
	ready.SetOut(readyOut)
	ready.SetErr(readyErr)
	ready.SetArgs([]string{"pipeline", "ready", "parallel_checks", "--repo", root, "--json"})
	if err := ready.Execute(); err != nil {
		t.Fatalf("pipeline ready: %v\nstderr=%s", err, readyErr.String())
	}
	var readyRows []jobReadyRow
	if err := json.Unmarshal(readyOut.Bytes(), &readyRows); err != nil {
		t.Fatalf("decode ready rows: %v\nbody=%s", err, readyOut.String())
	}
	if len(readyRows) != 1 || readyRows[0].ParallelReadySteps != 2 || !containsString(readyRows[0].Actions, "agent-team pipeline tick parallel_checks --all-ready-steps --dry-run --preview-routes") {
		t.Fatalf("ready rows = %+v, want parallel-ready action", readyRows)
	}

	status := NewRootCmd()
	statusOut, statusErr := &bytes.Buffer{}, &bytes.Buffer{}
	status.SetOut(statusOut)
	status.SetErr(statusErr)
	status.SetArgs([]string{"pipeline", "status", "parallel_checks", "--repo", root, "--json"})
	if err := status.Execute(); err != nil {
		t.Fatalf("pipeline status: %v\nstderr=%s", err, statusErr.String())
	}
	var statusRows []pipelineStatusRow
	if err := json.Unmarshal(statusOut.Bytes(), &statusRows); err != nil {
		t.Fatalf("decode status rows: %v\nbody=%s", err, statusOut.String())
	}
	if len(statusRows) != 1 || statusRows[0].ParallelReadySteps != 2 || !containsString(statusRows[0].Actions, "agent-team pipeline tick parallel_checks --all-ready-steps --dry-run --preview-routes") {
		t.Fatalf("status rows = %+v, want parallel-ready action", statusRows)
	}

	next := NewRootCmd()
	nextOut, nextErr := &bytes.Buffer{}, &bytes.Buffer{}
	next.SetOut(nextOut)
	next.SetErr(nextErr)
	next.SetArgs([]string{"pipeline", "next", "parallel_checks", "--repo", root, "--json"})
	if err := next.Execute(); err != nil {
		t.Fatalf("pipeline next all-ready: %v\nstderr=%s", err, nextErr.String())
	}
	var nextRows []pipelineNextAction
	if err := json.Unmarshal(nextOut.Bytes(), &nextRows); err != nil {
		t.Fatalf("decode pipeline next all-ready: %v\nbody=%s", err, nextOut.String())
	}
	foundAllReady := false
	for _, row := range nextRows {
		if row.Action == "agent-team pipeline tick parallel_checks --all-ready-steps --dry-run --preview-routes" {
			foundAllReady = true
			if row.Reason != "parallel_ready_steps=2" {
				t.Fatalf("all-ready reason = %q, want parallel_ready_steps=2", row.Reason)
			}
		}
	}
	if !foundAllReady {
		t.Fatalf("pipeline next rows missing all-ready action: %+v", nextRows)
	}

	all := NewRootCmd()
	allOut, allErr := &bytes.Buffer{}, &bytes.Buffer{}
	all.SetOut(allOut)
	all.SetErr(allErr)
	all.SetArgs([]string{"pipeline", "advance", "parallel_checks", "--repo", root, "--dry-run", "--all-ready-steps", "--json"})
	if err := all.Execute(); err != nil {
		t.Fatalf("pipeline advance all-ready dry-run: %v\nstderr=%s", err, allErr.String())
	}
	var allRows []pipelineAdvanceResult
	if err := json.Unmarshal(allOut.Bytes(), &allRows); err != nil {
		t.Fatalf("decode all-ready rows: %v\nbody=%s", err, allOut.String())
	}
	if len(allRows) != 2 || allRows[0].StepID != "lint" || allRows[0].StepStatus != job.StatusQueued || allRows[1].StepID != "test" {
		t.Fatalf("all-ready rows = %+v, want queued lint then ready test", allRows)
	}

	limited := NewRootCmd()
	limitedOut, limitedErr := &bytes.Buffer{}, &bytes.Buffer{}
	limited.SetOut(limitedOut)
	limited.SetErr(limitedErr)
	limited.SetArgs([]string{"pipeline", "advance", "parallel_checks", "--repo", root, "--dry-run", "--all-ready-steps", "--limit", "1", "--json"})
	if err := limited.Execute(); err != nil {
		t.Fatalf("pipeline advance all-ready limited dry-run: %v\nstderr=%s", err, limitedErr.String())
	}
	var limitedRows []pipelineAdvanceResult
	if err := json.Unmarshal(limitedOut.Bytes(), &limitedRows); err != nil {
		t.Fatalf("decode limited rows: %v\nbody=%s", err, limitedOut.String())
	}
	if len(limitedRows) != 1 || limitedRows[0].StepID != "lint" {
		t.Fatalf("limited rows = %+v, want queued first step", limitedRows)
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

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"pipeline", "advance", "ticket_triage", "--repo", target, "--limit", "1", "--workspace", "repo", "--runtime", "codex", "--runtime-bin", "codex-dev", "--dry-run", "--preview-routes", "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("pipeline advance dry-run --commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{
		"agent-team",
		"pipeline",
		"advance",
		"ticket_triage",
		"--repo",
		target,
		"--workspace",
		"repo",
		"--runtime",
		"codex",
		"--runtime-bin",
		"codex-dev",
		"--limit",
		"1",
	}), " ")
	if got := strings.TrimSpace(commandsOut.String()); got != wantCommand {
		t.Fatalf("pipeline advance dry-run --commands output = %q, want %q", got, wantCommand)
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

func TestPipelineAdvanceWaitsForRequestedStatus(t *testing.T) {
	root, mgr, cleanup := setupManualGateApprovalRepo(t, false)
	defer cleanup()
	teamDir := filepath.Join(root, ".agent_team")
	writeReadyAdvanceJob(t, teamDir, "squ-911")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"pipeline", "advance", "ticket_to_pr",
		"--repo", root,
		"--workspace", "repo",
		"--wait",
		"--wait-status", "running",
		"--wait-timeout", "2s",
		"--wait-interval", "10ms",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline advance --wait: %v\nstderr=%s", err, stderr.String())
	}
	var rows []pipelineAdvanceResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline advance wait json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Action != "advanced" || rows[0].Job == nil || rows[0].Job.Status != job.StatusRunning || rows[0].Job.LastEvent != "advance_dispatched" {
		t.Fatalf("advance wait rows = %+v", rows)
	}
	if rows[0].Step == nil || rows[0].Step.ID != "implement" || rows[0].Step.Status != job.StatusRunning || rows[0].Step.Instance != "worker-squ-911-implement" {
		t.Fatalf("advance wait step = %+v", rows[0].Step)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-911-implement")
}

func TestPipelineAdvanceWaitsForNextStepState(t *testing.T) {
	root, mgr, cleanup := setupManualGateApprovalRepo(t, false)
	defer cleanup()
	teamDir := filepath.Join(root, ".agent_team")
	writeReadyAdvanceJob(t, teamDir, "squ-916")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"pipeline", "advance", "ticket_to_pr",
		"--repo", root,
		"--workspace", "repo",
		"--wait",
		"--wait-next-state", "running",
		"--wait-step", "implement",
		"--wait-timeout", "2s",
		"--wait-interval", "10ms",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline advance --wait-next-state: %v\nstderr=%s", err, stderr.String())
	}
	var rows []pipelineAdvanceResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline advance next-state wait json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Action != "advanced" || rows[0].Job == nil || rows[0].Job.Status != job.StatusRunning {
		t.Fatalf("advance next-state wait rows = %+v", rows)
	}
	if rows[0].Step == nil || rows[0].Step.ID != "implement" || rows[0].Step.Status != job.StatusRunning || rows[0].Step.Instance != "worker-squ-916-implement" {
		t.Fatalf("advance next-state wait step = %+v", rows[0].Step)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-916-implement")
}

func TestPipelineAdvanceWaitTimesOutForEvent(t *testing.T) {
	root, mgr, cleanup := setupManualGateApprovalRepo(t, false)
	defer cleanup()
	teamDir := filepath.Join(root, ".agent_team")
	writeReadyAdvanceJob(t, teamDir, "squ-912")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"pipeline", "advance", "ticket_to_pr",
		"--repo", root,
		"--workspace", "repo",
		"--wait",
		"--wait-event", "closed",
		"--wait-timeout", "1ms",
		"--wait-interval", "10ms",
	})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("pipeline advance --wait succeeded unexpectedly")
	}
	if out.Len() != 0 {
		t.Fatalf("advance wait timeout wrote stdout=%q", out.String())
	}
	if !strings.Contains(stderr.String(), "timed out waiting for advanced jobs to reach event=closed") ||
		!strings.Contains(stderr.String(), "pending=squ-912=running event=advance_dispatched") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	stopAndWaitForTest(t, mgr, "worker-squ-912-implement")
}

func TestPipelineAdvanceWaitValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"pipeline", "advance", "ticket_to_pr", "--wait", "--dry-run"}, "--wait cannot be combined with --dry-run"},
		{[]string{"pipeline", "advance", "ticket_to_pr", "--commands"}, "--commands requires --dry-run"},
		{[]string{"pipeline", "advance", "ticket_to_pr", "--dry-run", "--commands", "--json"}, "--commands cannot be combined with --json"},
		{[]string{"pipeline", "advance", "ticket_to_pr", "--dry-run", "--commands", "--format", "{{.JobID}}"}, "--commands cannot be combined with --format"},
		{[]string{"pipeline", "advance", "ticket_to_pr", "--wait-status", "running"}, "wait-related flags require --wait"},
		{[]string{"pipeline", "advance", "ticket_to_pr", "--wait-next-state", "running"}, "wait-related flags require --wait"},
		{[]string{"pipeline", "advance", "ticket_to_pr", "--wait-step", "review"}, "wait-related flags require --wait"},
		{[]string{"pipeline", "advance", "ticket_to_pr", "--wait-timeout", "-1s", "--wait"}, "--wait-timeout must be >= 0"},
		{[]string{"pipeline", "advance", "ticket_to_pr", "--wait", "--wait-next-state", "missing"}, "--wait-next-state must be ready, queued, running, blocked, failed, held, done, none, or all"},
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

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"pipeline", "retry", "ticket_triage", "--repo", target, "--limit", "1", "--step", "triage", "--dispatch", "--workspace", "repo", "--runtime", "codex", "--runtime-bin", "codex-dev", "--force", "--message", "operator retry approved", "--dry-run", "--preview-routes", "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("pipeline retry commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "pipeline", "retry", "ticket_triage", "--repo", target, "--dispatch", "--workspace", "repo", "--runtime", "codex", "--runtime-bin", "codex-dev", "--step", "triage", "--limit", "1", "--force", "--message", "operator retry approved"}), " ")
	if got := strings.TrimSpace(commandsOut.String()); got != wantCommand {
		t.Fatalf("pipeline retry commands = %q, want %q", got, wantCommand)
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
	retryFile := filepath.Join(target, "retry-message.txt")
	if err := os.WriteFile(retryFile, []byte("operator retry approved from file\n"), 0o644); err != nil {
		t.Fatalf("write retry message file: %v", err)
	}

	run := NewRootCmd()
	runOut, runErr := &bytes.Buffer{}, &bytes.Buffer{}
	run.SetOut(runOut)
	run.SetErr(runErr)
	run.SetArgs([]string{"pipeline", "retry", "ticket_triage", "--repo", target, "--limit", "1", "--message-file", retryFile, "--json"})
	if err := run.Execute(); err != nil {
		t.Fatalf("pipeline retry: %v\nstderr=%s", err, runErr.String())
	}
	var runRows []pipelineRetryResult
	if err := json.Unmarshal(runOut.Bytes(), &runRows); err != nil {
		t.Fatalf("decode retry: %v\nbody=%s", err, runOut.String())
	}
	if len(runRows) != 1 || runRows[0].Action != "retried" || runRows[0].StepStatus != job.StatusBlocked || runRows[0].Instance != "" || runRows[0].Message != "operator retry approved from file" {
		t.Fatalf("run rows = %+v", runRows)
	}
	retried, err := job.Read(teamDir, "squ-601")
	if err != nil {
		t.Fatalf("read retried: %v", err)
	}
	if retried.Status != job.StatusQueued || retried.LastEvent != "reopened" || retried.LastStatus != "operator retry approved from file" || retried.Steps[0].Status != job.StatusBlocked || retried.Steps[0].Instance != "" || !retried.Steps[0].FinishedAt.IsZero() {
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

func TestPipelineRetryDispatchWaitsForRequestedStatus(t *testing.T) {
	root, mgr, cleanup := setupManualGateApprovalRepo(t, false)
	defer cleanup()
	teamDir := filepath.Join(root, ".agent_team")
	writeFailedRetryJob(t, teamDir, "squ-908")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"pipeline", "retry", "ticket_to_pr",
		"--repo", root,
		"--dispatch",
		"--workspace", "repo",
		"--wait",
		"--wait-status", "running",
		"--wait-timeout", "2s",
		"--wait-interval", "10ms",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline retry --dispatch --wait: %v\nstderr=%s", err, stderr.String())
	}
	var rows []pipelineRetryResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline retry wait json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Action != "dispatched" || rows[0].Job == nil || rows[0].Job.Status != job.StatusRunning || rows[0].Job.LastEvent != "advance_dispatched" {
		t.Fatalf("retry wait rows = %+v", rows)
	}
	if rows[0].Step == nil || rows[0].Step.ID != "implement" || rows[0].Step.Status != job.StatusRunning || rows[0].Step.Instance != "worker-squ-908-implement" {
		t.Fatalf("retry wait step = %+v", rows[0].Step)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-908-implement")
}

func TestPipelineRetryDispatchWaitsForNextStepState(t *testing.T) {
	root, mgr, cleanup := setupManualGateApprovalRepo(t, false)
	defer cleanup()
	teamDir := filepath.Join(root, ".agent_team")
	writeFailedRetryJob(t, teamDir, "squ-919")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"pipeline", "retry", "ticket_to_pr",
		"--repo", root,
		"--dispatch",
		"--workspace", "repo",
		"--wait",
		"--wait-next-state", "running",
		"--wait-step", "implement",
		"--wait-timeout", "2s",
		"--wait-interval", "10ms",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pipeline retry --dispatch --wait-next-state: %v\nstderr=%s", err, stderr.String())
	}
	var rows []pipelineRetryResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode pipeline retry next-state wait json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Action != "dispatched" || rows[0].Job == nil || rows[0].Job.Status != job.StatusRunning {
		t.Fatalf("retry next-state wait rows = %+v", rows)
	}
	if rows[0].Step == nil || rows[0].Step.ID != "implement" || rows[0].Step.Status != job.StatusRunning || rows[0].Step.Instance != "worker-squ-919-implement" {
		t.Fatalf("retry next-state wait step = %+v", rows[0].Step)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-919-implement")
}

func TestPipelineRetryDispatchWaitTimesOutForEvent(t *testing.T) {
	root, mgr, cleanup := setupManualGateApprovalRepo(t, false)
	defer cleanup()
	teamDir := filepath.Join(root, ".agent_team")
	writeFailedRetryJob(t, teamDir, "squ-909")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"pipeline", "retry", "ticket_to_pr",
		"--repo", root,
		"--dispatch",
		"--workspace", "repo",
		"--wait",
		"--wait-event", "closed",
		"--wait-timeout", "1ms",
		"--wait-interval", "10ms",
	})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("pipeline retry --dispatch --wait succeeded unexpectedly")
	}
	if out.Len() != 0 {
		t.Fatalf("retry wait timeout wrote stdout=%q", out.String())
	}
	if !strings.Contains(stderr.String(), "timed out waiting for retried jobs to reach event=closed") ||
		!strings.Contains(stderr.String(), "pending=squ-909=running event=advance_dispatched") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	stopAndWaitForTest(t, mgr, "worker-squ-909-implement")
}

func TestPipelineRetryStepFilter(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:         "squ-611",
			Ticket:     "SQU-611",
			Target:     "worker",
			Kickoff:    "retry implement only when selected",
			Pipeline:   "ticket_to_pr",
			Status:     job.StatusFailed,
			LastEvent:  "step_failed",
			LastStatus: "implement failed",
			CreatedAt:  now,
			UpdatedAt:  now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusFailed, Instance: "worker-implement", StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
			},
		},
		{
			ID:         "squ-612",
			Ticket:     "SQU-612",
			Target:     "manager",
			Kickoff:    "retry review",
			Pipeline:   "ticket_to_pr",
			Status:     job.StatusFailed,
			LastEvent:  "step_failed",
			LastStatus: "review failed",
			CreatedAt:  now,
			UpdatedAt:  now,
			Steps: []job.Step{
				{ID: "review", Target: "manager", Status: job.StatusFailed, Instance: "manager-review", StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
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
	dry.SetArgs([]string{"pipeline", "retry", "ticket_to_pr", "--repo", root, "--step", "review", "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("pipeline retry --step dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryRows []pipelineRetryResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryRows); err != nil {
		t.Fatalf("decode retry --step dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(dryRows) != 1 || dryRows[0].JobID != "squ-612" || dryRows[0].StepID != "review" || dryRows[0].Action != "would_retry" || dryRows[0].StepStatus != job.StatusBlocked {
		t.Fatalf("dry rows = %+v", dryRows)
	}

	run := NewRootCmd()
	runOut, runErr := &bytes.Buffer{}, &bytes.Buffer{}
	run.SetOut(runOut)
	run.SetErr(runErr)
	run.SetArgs([]string{"pipeline", "retry", "ticket_to_pr", "--repo", root, "--step", "review", "--message", "review retry only", "--json"})
	if err := run.Execute(); err != nil {
		t.Fatalf("pipeline retry --step: %v\nstderr=%s", err, runErr.String())
	}
	var runRows []pipelineRetryResult
	if err := json.Unmarshal(runOut.Bytes(), &runRows); err != nil {
		t.Fatalf("decode retry --step: %v\nbody=%s", err, runOut.String())
	}
	if len(runRows) != 1 || runRows[0].JobID != "squ-612" || runRows[0].StepID != "review" || runRows[0].Action != "retried" || runRows[0].Message != "review retry only" {
		t.Fatalf("run rows = %+v", runRows)
	}
	implement, err := job.Read(teamDir, "squ-611")
	if err != nil {
		t.Fatalf("read implement job: %v", err)
	}
	review, err := job.Read(teamDir, "squ-612")
	if err != nil {
		t.Fatalf("read review job: %v", err)
	}
	if implement.Status != job.StatusFailed || implement.Steps[0].Status != job.StatusFailed || implement.Steps[0].Instance != "worker-implement" {
		t.Fatalf("implement job should be untouched = %+v", implement)
	}
	if review.Status != job.StatusQueued || review.Steps[0].Status != job.StatusBlocked || review.Steps[0].Instance != "" || review.LastStatus != "review retry only" {
		t.Fatalf("review job = %+v", review)
	}
	events, err := job.ListEvents(teamDir, "squ-612")
	if err != nil {
		t.Fatalf("list review events: %v", err)
	}
	if len(events) != 1 || events[0].Type != "reopened" || events[0].Data["step"] != "review" {
		t.Fatalf("events = %+v", events)
	}
}

func TestPipelineRetrySkipsStepsAtMaxAttempts(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:         "squ-616",
			Ticket:     "SQU-616",
			Target:     "worker",
			Kickoff:    "retry cap reached",
			Pipeline:   "ticket_to_pr",
			Status:     job.StatusFailed,
			LastEvent:  "step_failed",
			LastStatus: "implement failed",
			CreatedAt:  now,
			UpdatedAt:  now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusFailed, Instance: "worker-squ-616", MaxAttempts: 1, StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
			},
		},
		{
			ID:         "squ-617",
			Ticket:     "SQU-617",
			Target:     "worker",
			Kickoff:    "retry cap still allows one more",
			Pipeline:   "ticket_to_pr",
			Status:     job.StatusFailed,
			LastEvent:  "step_failed",
			LastStatus: "implement failed",
			CreatedAt:  now,
			UpdatedAt:  now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusFailed, Instance: "worker-squ-617", Attempts: 1, MaxAttempts: 2, StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
			},
		},
		{
			ID:         "squ-618",
			Ticket:     "SQU-618",
			Target:     "worker",
			Kickoff:    "retry cap override",
			Pipeline:   "capped_only",
			Status:     job.StatusFailed,
			LastEvent:  "step_failed",
			LastStatus: "implement failed",
			CreatedAt:  now,
			UpdatedAt:  now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusFailed, Instance: "worker-squ-618", Attempts: 1, MaxAttempts: 1, StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
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
	dry.SetArgs([]string{"pipeline", "retry", "ticket_to_pr", "--repo", root, "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("pipeline retry max attempts dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var rows []pipelineRetryResult
	if err := json.Unmarshal(dryOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode retry max attempts: %v\nbody=%s", err, dryOut.String())
	}
	byJob := map[string]pipelineRetryResult{}
	for _, row := range rows {
		byJob[row.JobID] = row
	}
	if byJob["squ-616"].Action != "skipped" || byJob["squ-616"].Message != "max attempts reached (1/1)" || byJob["squ-616"].Attempts != 1 || byJob["squ-616"].MaxAttempts != 1 {
		t.Fatalf("capped row = %+v", byJob["squ-616"])
	}
	if byJob["squ-617"].Action != "would_retry" || byJob["squ-617"].StepStatus != job.StatusBlocked || byJob["squ-617"].Attempts != 1 || byJob["squ-617"].MaxAttempts != 2 {
		t.Fatalf("eligible row = %+v", byJob["squ-617"])
	}

	capped, err := job.Read(teamDir, "squ-616")
	if err != nil {
		t.Fatalf("read capped: %v", err)
	}
	if capped.Status != job.StatusFailed || capped.Steps[0].Status != job.StatusFailed || capped.Steps[0].Instance != "worker-squ-616" {
		t.Fatalf("dry-run mutated capped job = %+v", capped)
	}

	forceDry := NewRootCmd()
	forceDryOut, forceDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	forceDry.SetOut(forceDryOut)
	forceDry.SetErr(forceDryErr)
	forceDry.SetArgs([]string{"pipeline", "retry", "ticket_to_pr", "--repo", root, "--force", "--dry-run", "--json"})
	if err := forceDry.Execute(); err != nil {
		t.Fatalf("pipeline retry --force dry-run: %v\nstderr=%s", err, forceDryErr.String())
	}
	var forceDryRows []pipelineRetryResult
	if err := json.Unmarshal(forceDryOut.Bytes(), &forceDryRows); err != nil {
		t.Fatalf("decode force retry dry-run: %v\nbody=%s", err, forceDryOut.String())
	}
	forceByJob := map[string]pipelineRetryResult{}
	for _, row := range forceDryRows {
		forceByJob[row.JobID] = row
	}
	if forceByJob["squ-616"].Action != "would_retry" || forceByJob["squ-616"].StepStatus != job.StatusBlocked || forceByJob["squ-616"].Attempts != 1 || forceByJob["squ-616"].MaxAttempts != 1 {
		t.Fatalf("forced capped row = %+v", forceByJob["squ-616"])
	}

	forceRun := NewRootCmd()
	forceRunOut, forceRunErr := &bytes.Buffer{}, &bytes.Buffer{}
	forceRun.SetOut(forceRunOut)
	forceRun.SetErr(forceRunErr)
	forceRun.SetArgs([]string{"pipeline", "retry", "capped_only", "--repo", root, "--force", "--message", "operator override", "--json"})
	if err := forceRun.Execute(); err != nil {
		t.Fatalf("pipeline retry --force: %v\nstderr=%s", err, forceRunErr.String())
	}
	var forceRunRows []pipelineRetryResult
	if err := json.Unmarshal(forceRunOut.Bytes(), &forceRunRows); err != nil {
		t.Fatalf("decode force retry: %v\nbody=%s", err, forceRunOut.String())
	}
	if len(forceRunRows) != 1 || forceRunRows[0].JobID != "squ-618" || forceRunRows[0].Action != "retried" || forceRunRows[0].StepStatus != job.StatusBlocked || forceRunRows[0].Attempts != 1 || forceRunRows[0].MaxAttempts != 1 || forceRunRows[0].Message != "operator override" {
		t.Fatalf("force run rows = %+v", forceRunRows)
	}
	forced, err := job.Read(teamDir, "squ-618")
	if err != nil {
		t.Fatalf("read forced: %v", err)
	}
	if forced.Status != job.StatusQueued || forced.Steps[0].Status != job.StatusBlocked || forced.Steps[0].Instance != "" || forced.LastStatus != "operator override" {
		t.Fatalf("forced job = %+v", forced)
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
		{[]string{"pipeline", "retry", "ticket_triage", "--wait", "--dry-run"}, "--wait cannot be combined with --dry-run"},
		{[]string{"pipeline", "retry", "ticket_triage", "--wait-status", "running"}, "wait-related flags require --wait"},
		{[]string{"pipeline", "retry", "ticket_triage", "--wait-next-state", "running"}, "wait-related flags require --wait"},
		{[]string{"pipeline", "retry", "ticket_triage", "--wait-step", "review"}, "wait-related flags require --wait"},
		{[]string{"pipeline", "retry", "ticket_triage", "--wait-timeout", "-1s", "--wait"}, "--wait-timeout must be >= 0"},
		{[]string{"pipeline", "retry", "ticket_triage", "--wait", "--wait-next-state", "missing"}, "--wait-next-state must be ready, queued, running, blocked, failed, held, done, none, or all"},
		{[]string{"pipeline", "retry", "ticket_triage", "--format", "{{.JobID}}", "--json"}, "--format cannot be combined with --json"},
		{[]string{"pipeline", "retry", "ticket_triage", "--commands"}, "--commands requires --dry-run"},
		{[]string{"pipeline", "retry", "ticket_triage", "--dry-run", "--commands", "--json"}, "--commands cannot be combined with --json"},
		{[]string{"pipeline", "retry", "ticket_triage", "--dry-run", "--commands", "--format", "{{.JobID}}"}, "--commands cannot be combined with --format"},
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

func TestPipelineRepairValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"pipeline", "repair", "ticket_to_pr", "--limit", "-1"}, "--limit must be >= 0"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--ready-timeout", "-1s"}, "--ready-timeout must be >= 0"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--wait-timeout", "-1s", "--wait"}, "--wait-timeout must be >= 0"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--wait-interval", "-1s", "--wait"}, "--wait-interval must be >= 0"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--preview-routes"}, "--preview-routes requires --dry-run"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--wait", "--dry-run"}, "--wait cannot be combined with --dry-run"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--wait", "--skip-advance"}, "--wait requires repair dispatch"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--wait-status", "running"}, "wait-related flags require --wait"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--wait-next-state", "running"}, "wait-related flags require --wait"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--wait-step", "implement"}, "wait-related flags require --wait"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--wait", "--wait-next-state", "missing"}, "--wait-next-state must be ready, queued, running, blocked, failed, held, done, none, or all"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--retry-pipelines", "--skip-daemon"}, "--retry-pipelines requires daemon access"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--timeout-jobs", "--timeout-pipelines"}, "--timeout-jobs cannot be combined with --timeout-pipelines"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--timeout-message", "incident"}, "--timeout-message requires --timeout-pipelines or --timeout-jobs"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--timeout-message-file", "incident.txt"}, "--timeout-message-file requires --timeout-pipelines or --timeout-jobs"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--timeout-step", "review"}, "--timeout-step requires --timeout-pipelines or --timeout-jobs"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--timeout-target-agent", "worker"}, "--timeout-target-agent requires --timeout-pipelines or --timeout-jobs"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--retry-message", "incident"}, "--retry-message requires --retry-pipelines"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--retry-message-file", "incident.txt"}, "--retry-message-file requires --retry-pipelines"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--retry-step", "review"}, "--retry-step requires --retry-pipelines"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--retry-force"}, "--retry-force requires --retry-pipelines"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--format", "{{.Pipeline}}", "--json"}, "--format cannot be combined with --json"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--commands"}, "--commands requires --dry-run"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--dry-run", "--commands", "--json"}, "--commands cannot be combined with --json"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--dry-run", "--commands", "--format", "{{.Pipeline}}"}, "--commands cannot be combined with --format"},
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
