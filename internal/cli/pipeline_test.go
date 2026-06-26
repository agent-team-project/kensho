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
	for _, want := range []string{"PIPELINE", "ticket_to_pr", "ticket.created", "implement:worker", `review:manager label="Code review" after=implement optional=true timeout=45m0s max_attempts=3`} {
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
	for _, want := range []string{"Pipeline: ticket_to_pr", "Trigger:  ticket.created", "implement target=worker after=-", `review target=manager after=implement label="Code review" description="Review branch and PR state." instructions="Check tests, summarize risks, and decide whether the PR can proceed." optional=true timeout=45m0s max_attempts=3`} {
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
	if len(rows) != 1 || rows[0].Name != "ticket_to_pr" || len(rows[0].Steps) != 2 || rows[0].Steps[1].Label != "Code review" || rows[0].Steps[1].Description != "Review branch and PR state." || rows[0].Steps[1].Instructions != "Check tests, summarize risks, and decide whether the PR can proceed." || !rows[0].Steps[1].Optional || rows[0].Steps[1].Timeout != "45m0s" || rows[0].Steps[1].MaxAttempts != 3 {
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
	if len(created.Steps) != 2 || created.Steps[1].ID != "verify" || created.Steps[1].Label != "Verification" || created.Steps[1].Description != "Confirm implementation matches the ticket." || created.Steps[1].Instructions != "Check acceptance criteria before closing the workflow." || !created.Steps[1].Optional || created.Steps[1].Timeout != "30m0s" || created.Steps[1].MaxAttempts != 2 {
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
	if !containsString(ticket.Actions, "agent-team pipeline advance ticket_to_pr --dry-run --preview-routes") ||
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
	if !containsString(nightly.Actions, "agent-team pipeline advance nightly --dry-run --preview-routes") ||
		!containsString(nightly.Actions, "agent-team tick") {
		t.Fatalf("nightly actions = %+v", nightly.Actions)
	}
	adHoc := byName["ad_hoc"]
	if adHoc.Declared || adHoc.Steps != 0 || adHoc.Jobs != 1 || adHoc.Done != 1 || adHoc.NoStep != 1 {
		t.Fatalf("ad_hoc status = %+v", adHoc)
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

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"pipeline", "status", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("pipeline status text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"PIPELINE", "STALE_RUNNING", "MANUAL_GATES", "ACTION", "ticket_to_pr", "yes", "running=2,blocked=1,failed=1", "agent-team pipeline advance ticket_to_pr --dry-run --preview-routes", "agent-team job reconcile events --dry-run", "agent-team pipeline timeout ticket_to_pr --dry-run", "agent-team pipeline repair ticket_to_pr --timeout-jobs --dry-run --preview-routes", "agent-team repair --timeout-jobs --dry-run", "agent-team pipeline approve ticket_to_pr --dry-run --dispatch --preview-routes", "agent-team pipeline repair ticket_to_pr --retry-pipelines --dry-run --preview-routes", "agent-team repair --retry-pipelines --dry-run --preview-routes", "ad_hoc", "no"} {
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
				readyReview = step.State == "ready" && containsString(step.Actions, "agent-team job advance squ-610")
			case explainedJob.JobID == "squ-614" && step.ID == "review":
				manualGate = step.State == "waiting" && step.Gate == job.StepGateManual &&
					containsString(step.Actions, "agent-team job approve squ-614 --step review") &&
					containsString(step.Actions, "agent-team job reject squ-614 --step review")
			case explainedJob.JobID == "squ-611" && step.ID == "implement":
				failedImplement = step.State == "failed" && containsString(step.Actions, "agent-team job retry squ-611 --dry-run --dispatch")
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
	for _, want := range []string{"Pipeline: ticket_to_pr", "Jobs:", "Steps:", "squ-610", "review", "agent-team job advance squ-610", "agent-team job approve squ-614 --step review", "agent-team job reject squ-614 --step review"} {
		if !strings.Contains(explainTextOut.String(), want) {
			t.Fatalf("pipeline explain text missing %q:\n%s", want, explainTextOut.String())
		}
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
				{ID: "implement", Target: "worker", Status: job.StatusBlocked},
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
				{ID: "implement", Target: "worker", Status: job.StatusBlocked},
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
	if len(snapshot.Queue) != 1 || snapshot.Queue[0].ID != "q-ticket-pipeline" || snapshot.QueueSummary == nil || snapshot.QueueSummary.Total != 1 || snapshot.QueueSummary.Quarantined != 1 || snapshot.QueueSummary.QuarantineRestorable != 1 {
		t.Fatalf("snapshot queue = %+v summary=%+v", snapshot.Queue, snapshot.QueueSummary)
	}
	if snapshot.Queue[0].Payload["access_token"] != snapshotRedactedValue {
		t.Fatalf("snapshot queue payload not redacted: %+v", snapshot.Queue[0].Payload)
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
	if strings.Contains(out.String(), "platform_work") || strings.Contains(out.String(), "squ-705") || strings.Contains(out.String(), "q-platform") || strings.Contains(out.String(), "ticket-secret") {
		t.Fatalf("pipeline snapshot leaked unrelated workflow:\n%s", out.String())
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"pipeline", "snapshot", "ticket_to_pr", "--repo", target})
	if err := text.Execute(); err != nil {
		t.Fatalf("pipeline snapshot text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"pipeline snapshot:", "pipeline: ticket_to_pr", "status: jobs=1 ready_steps=1", "explain: jobs=1 steps=1", "jobs: total=1", "queue: total=1 pending=1 dead=0 delayed=0 attempts=0 quarantined=1 restorable=1 unrestorable=0", "advance: ready=1 route_previews=1"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("pipeline snapshot text missing %q:\n%s", want, textOut.String())
		}
	}
	for _, leak := range []string{"platform_work", "squ-705", "q-platform"} {
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
	if actions[0].Pipeline != "ticket_to_pr" || actions[0].Reason != "ready_steps=1" || actions[0].Action != "agent-team pipeline advance ticket_to_pr --dry-run --preview-routes" || actions[0].Status.ReadySteps != 1 {
		t.Fatalf("first action = %+v, want ready advance", actions[0])
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
		"ticket_to_pr|ready_steps=1|agent-team pipeline advance ticket_to_pr --dry-run --preview-routes",
		"ticket_to_pr|failed_steps=1|agent-team pipeline repair ticket_to_pr --retry-pipelines --dry-run --preview-routes",
		"ticket_to_pr|failed_steps=1|agent-team repair --retry-pipelines --dry-run --preview-routes",
		"ticket_to_pr|failed_steps=1|agent-team pipeline explain ticket_to_pr --state failed",
		"ticket_to_pr|failed_steps=1|agent-team pipeline ready ticket_to_pr --state failed",
		"ticket_to_pr|queued_steps=1|agent-team tick",
	} {
		if !strings.Contains(formatOut.String(), want) {
			t.Fatalf("pipeline next format missing %q:\n%s", want, formatOut.String())
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	watchCmd := NewRootCmd()
	watchOut, watchErr := &bytes.Buffer{}, &bytes.Buffer{}
	watchCmd.SetContext(ctx)
	watchCmd.SetOut(watchOut)
	watchCmd.SetErr(watchErr)
	watchCmd.SetArgs([]string{"pipeline", "next", "ticket_to_pr", "--repo", root, "--limit", "1", "--watch", "--no-clear", "--interval", "1h", "--format", "{{.Pipeline}}|{{.Reason}}|{{.Action}}"})
	if err := watchCmd.Execute(); err != nil {
		t.Fatalf("pipeline next watch: %v\nstderr=%s", err, watchErr.String())
	}
	if got := strings.TrimSpace(watchOut.String()); got != "ticket_to_pr|ready_steps=1|agent-team pipeline advance ticket_to_pr --dry-run --preview-routes" || strings.Contains(watchOut.String(), watchClearSequence) {
		t.Fatalf("pipeline next watch output = %q", watchOut.String())
	}

	teamCmd := NewRootCmd()
	teamOut, teamErr := &bytes.Buffer{}, &bytes.Buffer{}
	teamCmd.SetOut(teamOut)
	teamCmd.SetErr(teamErr)
	teamCmd.SetArgs([]string{"pipeline", "next", "ticket_to_pr", "--team", "delivery", "--repo", root, "--format", "{{.Pipeline}}|{{.Reason}}|{{.Action}}"})
	if err := teamCmd.Execute(); err != nil {
		t.Fatalf("pipeline next team format: %v\nstderr=%s", err, teamErr.String())
	}
	for _, want := range []string{
		"ticket_to_pr|ready_steps=1|agent-team team advance delivery --dry-run --preview-routes",
		"ticket_to_pr|failed_steps=1|agent-team team repair delivery --retry-pipelines --dry-run --preview-routes",
		"ticket_to_pr|failed_steps=1|agent-team team explain delivery --state failed",
		"ticket_to_pr|failed_steps=1|agent-team team ready delivery --state failed",
		"ticket_to_pr|queued_steps=1|agent-team team tick delivery",
	} {
		if !strings.Contains(teamOut.String(), want) {
			t.Fatalf("pipeline next team format missing %q:\n%s", want, teamOut.String())
		}
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
		"agent-team pipeline queue retry ticket_to_pr --all --dry-run",
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
		"queue_dead=1|agent-team pipeline queue retry ticket_to_pr --all --dry-run",
		"queue_quarantined=2|agent-team pipeline queue quarantine ticket_to_pr",
		"queue_pending=1|agent-team pipeline queue ticket_to_pr --state pending",
	} {
		if !strings.Contains(nextOut.String(), want) {
			t.Fatalf("pipeline next queue reason missing %q:\n%s", want, nextOut.String())
		}
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
		"queue_dead=1|agent-team team queue retry delivery --all --dry-run",
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
	if plans[0].Job != "squ-940" || plans[1].Job != "squ-940" || plans[1].JobLogsCommand != "agent-team job logs squ-940 --follow" || plans[1].JobLastMessageCommand != "agent-team job logs squ-940 --last-message" {
		t.Fatalf("job-scoped commands not populated: %+v", plans)
	}

	format := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	format.SetOut(formatOut)
	format.SetErr(formatErr)
	format.SetArgs([]string{"pipeline", "resume-plan", "ticket_to_pr", "--repo", root, "--runtime", "codex", "--action", "logs", "--format", "{{.Instance}} {{.Runtime}} {{.RecommendedAction}} {{.JobLogsCommand}}"})
	if err := format.Execute(); err != nil {
		t.Fatalf("pipeline resume-plan format: %v\nstderr=%s", err, formatErr.String())
	}
	if got, want := strings.TrimSpace(formatOut.String()), "worker-squ-940 codex logs agent-team job logs squ-940 --follow"; got != want {
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
	} {
		if err := daemon.WriteMetadata(daemonRoot, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeChildLogForTest(t, daemonRoot, "manager-squ-980", "manager first\nmanager latest\n")
	writeChildLogForTest(t, daemonRoot, "worker-squ-980", "worker first\nworker latest\n")
	writeChildLogForTest(t, daemonRoot, "worker-squ-981", "foreign first\nforeign latest\n")
	writeLastMessageForTest(t, teamDir, "manager-squ-980", "manager final")
	writeLastMessageForTest(t, teamDir, "worker-squ-980", "worker final")
	writeLastMessageForTest(t, teamDir, "worker-squ-981", "foreign final")

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
	} {
		if err := daemon.WriteMetadata(daemonRoot, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	for _, ev := range []*daemon.LifecycleEvent{
		{TS: base, Action: "start", Instance: "manager-squ-995", Agent: "manager", Status: daemon.StatusRunning, Message: "manager up"},
		{TS: base.Add(time.Minute), Action: "stop", Instance: "worker-squ-996", Agent: "worker", Status: daemon.StatusStopped, Message: "foreign stop"},
		{TS: base.Add(2 * time.Minute), Action: "dispatch", Instance: "worker-squ-995", Agent: "worker", Status: daemon.StatusRunning, Message: "pipeline worker"},
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
			ID:        "ops-902",
			Ticket:    "OPS-902",
			Target:    "worker",
			Pipeline:  "ops_review",
			Instance:  "worker-ops-902",
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
	if got := queueQuarantineItemIDs(listed); got != "q-pipeline-quarantined,q-pipeline-unrestorable" {
		t.Fatalf("listed pipeline quarantined items = %s\nbody=%s", got, listOut.String())
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
	if got := queueQuarantineItemIDs(restorableRows); got != "q-pipeline-quarantined" {
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
	if len(readyRows) != 1 || readyRows[0].ParallelReadySteps != 2 || !containsString(readyRows[0].Actions, "agent-team pipeline advance parallel_checks --all-ready-steps --dry-run --preview-routes") {
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
	if len(statusRows) != 1 || statusRows[0].ParallelReadySteps != 2 || !containsString(statusRows[0].Actions, "agent-team pipeline advance parallel_checks --all-ready-steps --dry-run --preview-routes") {
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
		if row.Action == "agent-team pipeline advance parallel_checks --all-ready-steps --dry-run --preview-routes" {
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

func TestPipelineRepairValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"pipeline", "repair", "ticket_to_pr", "--limit", "-1"}, "--limit must be >= 0"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--ready-timeout", "-1s"}, "--ready-timeout must be >= 0"},
		{[]string{"pipeline", "repair", "ticket_to_pr", "--preview-routes"}, "--preview-routes requires --dry-run"},
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
