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
target = "manager"
after = ["implement"]
optional = true
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
	for _, want := range []string{"PIPELINE", "ticket_to_pr", "ticket.created", "implement:worker", "review:manager after=implement optional=true"} {
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
	for _, want := range []string{"Pipeline: ticket_to_pr", "Trigger:  ticket.created", "implement target=worker after=-", "review target=manager after=implement optional=true"} {
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
	if len(rows) != 1 || rows[0].Name != "ticket_to_pr" || len(rows[0].Steps) != 2 || !rows[0].Steps[1].Optional {
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
		"review target=manager after=implement optional=true routes=manager",
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
	for _, want := range []string{"flowchart TD", "trigger[\"trigger: ticket.created\"]", "step_1_implement", "optional", "--> step_2_review"} {
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
	for _, want := range []string{`digraph "ticket_to_pr"`, `"trigger" -> "implement";`, `"implement" -> "review";`, "optional"} {
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
	if graph.Name != "ticket_to_pr" || len(graph.Nodes) != 3 || len(graph.Edges) != 3 || len(graph.Nodes[0].Routes) != 1 || !graph.Nodes[1].Optional {
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
	if !ticket.Declared || ticket.Steps != 2 || ticket.Jobs != 3 || ticket.Running != 1 || ticket.Blocked != 1 || ticket.Failed != 1 || ticket.ReadySteps != 1 || ticket.ManualGates != 1 || ticket.FailedSteps != 1 {
		t.Fatalf("ticket status = %+v", ticket)
	}
	if !containsString(ticket.Actions, "agent-team pipeline advance ticket_to_pr --dry-run --preview-routes") ||
		!containsString(ticket.Actions, "agent-team pipeline approve ticket_to_pr --dry-run --dispatch --preview-routes") ||
		!containsString(ticket.Actions, "agent-team pipeline retry ticket_to_pr --dry-run --dispatch --preview-routes") ||
		!containsString(ticket.Actions, "agent-team repair --retry-pipelines --dry-run --preview-routes") ||
		!containsString(ticket.Actions, "agent-team pipeline explain ticket_to_pr --state failed") ||
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
	if got := strings.TrimSpace(oneOut.String()); got != "ticket_to_pr 3 1 1" {
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
	for _, want := range []string{"PIPELINE", "MANUAL_GATES", "ACTION", "ticket_to_pr", "yes", "running=1,blocked=1,failed=1", "agent-team pipeline advance ticket_to_pr --dry-run --preview-routes", "agent-team pipeline approve ticket_to_pr --dry-run --dispatch --preview-routes", "agent-team repair --retry-pipelines --dry-run --preview-routes", "ad_hoc", "no"} {
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
	if explained.Pipeline != "ticket_to_pr" || !explained.Declared || explained.TotalJobs != 3 || explained.ExplainedJobs != 3 || len(explained.Jobs) != 3 {
		t.Fatalf("pipeline explain ticket_to_pr = %+v", explained)
	}
	var readyReview, manualGate, failedImplement bool
	for _, explainedJob := range explained.Jobs {
		for _, step := range explainedJob.Steps {
			switch {
			case explainedJob.JobID == "squ-610" && step.ID == "review":
				readyReview = step.State == "ready" && containsString(step.Actions, "agent-team job advance squ-610")
			case explainedJob.JobID == "squ-614" && step.ID == "review":
				manualGate = step.State == "waiting" && step.Gate == job.StepGateManual && containsString(step.Actions, "agent-team job step squ-614 review --status queued")
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
	for _, want := range []string{"Pipeline: ticket_to_pr", "Jobs:", "Steps:", "squ-610", "review", "agent-team job advance squ-610", "agent-team job step squ-614 review --status queued"} {
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
	if got := strings.TrimSpace(explainFormatOut.String()); got != "ticket_to_pr 3 3" {
		t.Fatalf("pipeline explain format = %q", got)
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
	if len(limitedRows) != 1 || limitedRows[0].TotalJobs != 3 || limitedRows[0].ExplainedJobs != 1 || !limitedRows[0].Truncated || len(limitedRows[0].Jobs) != 1 {
		t.Fatalf("limited pipeline explain = %+v", limitedRows)
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
	if len(failedRows) != 1 || failedRows[0].TotalJobs != 3 || failedRows[0].ExplainedJobs != 1 || len(failedRows[0].Jobs) != 1 || failedRows[0].Jobs[0].JobID != "squ-611" {
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
	holdFailed.SetArgs([]string{"pipeline", "hold", "ticket_to_pr", "--repo", root, "--state", "failed", "--message", "freeze failed work", "--json"})
	if err := holdFailed.Execute(); err != nil {
		t.Fatalf("pipeline hold failed: %v\nstderr=%s", err, holdFailedErr.String())
	}
	var heldFailed []pipelineHoldResult
	if err := json.Unmarshal(holdFailedOut.Bytes(), &heldFailed); err != nil {
		t.Fatalf("decode failed hold json: %v\nbody=%s", err, holdFailedOut.String())
	}
	if len(heldFailed) != 1 || heldFailed[0].JobID != "squ-703" || heldFailed[0].Action != "held" || !heldFailed[0].HeldAfter {
		t.Fatalf("held failed rows = %+v", heldFailed)
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
	release.SetArgs([]string{"pipeline", "release", "ticket_to_pr", "--repo", root, "--message", "resume failed work", "--json"})
	if err := release.Execute(); err != nil {
		t.Fatalf("pipeline release: %v\nstderr=%s", err, releaseErr.String())
	}
	var released []pipelineHoldResult
	if err := json.Unmarshal(releaseOut.Bytes(), &released); err != nil {
		t.Fatalf("decode release json: %v\nbody=%s", err, releaseOut.String())
	}
	if len(released) != 1 || released[0].JobID != "squ-703" || released[0].Action != "released" || released[0].HeldAfter {
		t.Fatalf("released rows = %+v", released)
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
	teamHold.SetArgs([]string{"team", "hold", "delivery", "--repo", root, "--state", "ready", "--limit", "1", "--json"})
	if err := teamHold.Execute(); err != nil {
		t.Fatalf("team hold: %v\nstderr=%s", err, teamHoldErr.String())
	}
	var teamHeld []pipelineHoldResult
	if err := json.Unmarshal(teamHoldOut.Bytes(), &teamHeld); err != nil {
		t.Fatalf("decode team hold json: %v\nbody=%s", err, teamHoldOut.String())
	}
	if len(teamHeld) != 1 || teamHeld[0].Action != "held" || !teamHeld[0].HeldAfter {
		t.Fatalf("team held rows = %+v", teamHeld)
	}

	teamRelease := NewRootCmd()
	teamReleaseOut, teamReleaseErr := &bytes.Buffer{}, &bytes.Buffer{}
	teamRelease.SetOut(teamReleaseOut)
	teamRelease.SetErr(teamReleaseErr)
	teamRelease.SetArgs([]string{"team", "release", "delivery", "--repo", root, "--json"})
	if err := teamRelease.Execute(); err != nil {
		t.Fatalf("team release: %v\nstderr=%s", err, teamReleaseErr.String())
	}
	var teamReleased []pipelineHoldResult
	if err := json.Unmarshal(teamReleaseOut.Bytes(), &teamReleased); err != nil {
		t.Fatalf("decode team release json: %v\nbody=%s", err, teamReleaseOut.String())
	}
	if len(teamReleased) != 1 || teamReleased[0].Action != "released" || teamReleased[0].HeldAfter {
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
		"ticket_to_pr|failed_steps=1|agent-team repair --retry-pipelines --dry-run --preview-routes",
		"ticket_to_pr|failed_steps=1|agent-team pipeline explain ticket_to_pr --state failed",
		"ticket_to_pr|failed_steps=1|agent-team pipeline ready ticket_to_pr --state failed",
		"ticket_to_pr|queued_steps=1|agent-team tick",
	} {
		if !strings.Contains(formatOut.String(), want) {
			t.Fatalf("pipeline next format missing %q:\n%s", want, formatOut.String())
		}
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
	if len(blocked.Actions) != 1 || blocked.Actions[0] != "agent-team job step squ-901 review --status queued" {
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

	approve := NewRootCmd()
	approveOut, approveErr := &bytes.Buffer{}, &bytes.Buffer{}
	approve.SetOut(approveOut)
	approve.SetErr(approveErr)
	approve.SetArgs([]string{"pipeline", "approve", "ticket_to_pr", "--repo", root, "--message", "manual review approved", "--format", "{{.JobID}} {{.Action}} {{.StepID}} {{.Message}}"})
	if err := approve.Execute(); err != nil {
		t.Fatalf("pipeline approve: %v\nstderr=%s", err, approveErr.String())
	}
	if got := approveOut.String(); got != "squ-902 approved review manual review approved\n" {
		t.Fatalf("approve format = %q", got)
	}
	updated, err := job.Read(teamDir, "squ-902")
	if err != nil {
		t.Fatalf("read approved job: %v", err)
	}
	if updated.Status != job.StatusQueued || updated.Steps[1].Status != job.StatusQueued || updated.LastStatus != "manual review approved" {
		t.Fatalf("approved job = %+v", updated)
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
	if len(rows) != 1 || rows[0].Gate != job.StepGatePR || strings.Join(rows[0].WaitingFor, ",") != "pr" || len(rows[0].Actions) != 1 || rows[0].Actions[0] != "agent-team job update squ-902 --pr <url> --advance --dry-run" {
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
	if len(explained.Steps) != 2 || !containsString(explained.Steps[1].Actions, "agent-team job update squ-902 --pr <url> --advance --dry-run") {
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
