package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNextCommandReportsRecommendedActions(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"next", "--target", root, "--limit", "2", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("next json: %v\nstderr=%s", err, stderr.String())
	}

	var result nextActionResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode next json: %v\nbody=%s", err, out.String())
	}
	if result.OK || result.State != "attention" {
		t.Fatalf("result state = ok:%v state:%q", result.OK, result.State)
	}
	if len(result.Actions) != 2 || result.TotalActions <= len(result.Actions) || result.HiddenActions == 0 {
		t.Fatalf("result actions = %+v", result)
	}
	if len(result.ActionDetails) != len(result.Actions) {
		t.Fatalf("action details = %+v, want one detail per visible action", result.ActionDetails)
	}
	for _, want := range []string{"agent-team repair --dry-run --jobs", "agent-team daemon start"} {
		if !stringSliceContains(result.Actions, want) {
			t.Fatalf("actions missing %q: %+v", want, result.Actions)
		}
	}
	if detail, ok := findOperatorActionHint(result.ActionDetails, "agent-team repair --dry-run --jobs"); !ok || detail.Source != "health" || detail.Reason != "unhealthy" {
		t.Fatalf("repair detail = %+v, ok=%v", detail, ok)
	}
}

func TestNextCommandCanScopeToTeam(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"next", "--target", root, "--team", "delivery"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("next team text: %v\nstderr=%s", err, stderr.String())
	}

	for _, want := range []string{
		"next: attention",
		"team: delivery",
		"agent-team team repair delivery --dry-run --jobs",
		"agent-team team queue retry delivery --all --job squ-700 --dry-run",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("next team output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "agent-team team queue retry delivery --all --dry-run") {
		t.Fatalf("next team output should prefer job-filtered retry:\n%s", out.String())
	}
}

func TestTeamNextCommandReportsScopedActions(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "next", "delivery", "--repo", root, "--limit", "2", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team next json: %v\nstderr=%s", err, stderr.String())
	}

	var result nextActionResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode team next json: %v\nbody=%s", err, out.String())
	}
	if result.Team == nil || result.Team.Name != "delivery" || result.OK || result.State != "attention" {
		t.Fatalf("team next result = %+v", result)
	}
	if len(result.Actions) != 2 || result.HiddenActions == 0 {
		t.Fatalf("team next actions = %+v", result)
	}
	if len(result.ActionDetails) != len(result.Actions) {
		t.Fatalf("team next action details = %+v, want one detail per visible action", result.ActionDetails)
	}
	for _, want := range []string{
		"agent-team team repair delivery --dry-run --jobs",
		"agent-team daemon start",
	} {
		if !stringSliceContains(result.Actions, want) {
			t.Fatalf("actions missing %q: %+v", want, result.Actions)
		}
	}
	if detail, ok := findOperatorActionHint(result.ActionDetails, "agent-team team repair delivery --dry-run --jobs"); !ok || detail.Team != "delivery" || detail.Source != "health" {
		t.Fatalf("team repair detail = %+v, ok=%v", detail, ok)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"team", "next", "delivery", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("team next text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"next: attention", "team: delivery", "agent-team team queue retry delivery --all --job squ-700 --dry-run"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("team next text missing %q:\n%s", want, textOut.String())
		}
	}
	if strings.Contains(textOut.String(), "agent-team team queue retry delivery --all --dry-run") {
		t.Fatalf("team next text should prefer job-filtered retry:\n%s", textOut.String())
	}
}

func TestNextCommandFormat(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"next", "--target", root, "--limit", "1", "--format", "{{.State}}|{{.HiddenActions}}|{{index .Actions 0}}|{{(index .ActionDetails 0).Source}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("next format: %v\nstderr=%s", err, stderr.String())
	}
	body := out.String()
	if !strings.HasPrefix(body, "attention|") || !strings.Contains(body, "agent-team repair --dry-run --jobs|health") {
		t.Fatalf("next format output = %q", body)
	}
}

func TestTeamNextCommandFormat(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "next", "delivery", "--repo", root, "--limit", "2", "--format", "{{.Team.Name}} {{.State}} {{len .Actions}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team next format: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "delivery attention 2\n"; got != want {
		t.Fatalf("team next format output = %q, want %q", got, want)
	}
}

func TestNextCommandFormatValidation(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "next-json-conflict",
			args: []string{"next", "--format", "{{.State}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "next-invalid-template",
			args: []string{"next", "--format", "{{"},
			want: "invalid --format template",
		},
		{
			name: "team-next-json-conflict",
			args: []string{"team", "next", "delivery", "--format", "{{.State}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "team-next-invalid-template",
			args: []string{"team", "next", "delivery", "--format", "{{"},
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
		var ec ExitCode
		if !errors.As(err, &ec) || int(ec) != 2 {
			t.Fatalf("%s: err=%v, want exit 2", tc.name, err)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%s: stderr=%q, want %q", tc.name, stderr.String(), tc.want)
		}
		if out.Len() != 0 {
			t.Fatalf("%s: validation should not write stdout: %q", tc.name, out.String())
		}
	}
}

func TestNextCommandReportsIntakeReplayAction(t *testing.T) {
	root := writeIntakeErrorFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"next", "--target", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("next intake json: %v\nstderr=%s", err, stderr.String())
	}

	var result nextActionResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode next intake json: %v\nbody=%s", err, out.String())
	}
	for _, want := range []string{
		"agent-team intake summary",
		"agent-team intake deliveries --status error",
		"agent-team intake replay --all --dry-run --preview-triggers",
	} {
		if !stringSliceContains(result.Actions, want) {
			t.Fatalf("actions missing %q: %+v", want, result.Actions)
		}
	}
}

func TestNextCommandReportsBatchCleanupAction(t *testing.T) {
	root := writeOverviewCleanupFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"next", "--target", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("next cleanup json: %v\nstderr=%s", err, stderr.String())
	}

	var result nextActionResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode next cleanup json: %v\nbody=%s", err, out.String())
	}
	if !stringSliceContains(result.Actions, "agent-team job cleanup --all --dry-run") {
		t.Fatalf("actions missing batch cleanup: %+v", result.Actions)
	}
}

func TestNextCommandReportsQueueDoctorAction(t *testing.T) {
	root := writeOverviewCorruptQueueFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"next", "--target", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("next queue doctor json: %v\nstderr=%s", err, stderr.String())
	}

	var result nextActionResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode next queue doctor json: %v\nbody=%s", err, out.String())
	}
	if !stringSliceContains(result.Actions, "agent-team queue doctor") {
		t.Fatalf("actions missing queue doctor: %+v", result.Actions)
	}
}

func TestNextCommandReportsQueueQuarantineAction(t *testing.T) {
	root := writeOverviewQuarantineFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"next", "--target", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("next queue quarantine json: %v\nstderr=%s", err, stderr.String())
	}

	var result nextActionResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode next queue quarantine json: %v\nbody=%s", err, out.String())
	}
	if !stringSliceContains(result.Actions, "agent-team queue quarantine ls") {
		t.Fatalf("actions missing queue quarantine ls: %+v", result.Actions)
	}
}

func TestNextActionResultHandlesNoActions(t *testing.T) {
	result := nextActionResultFromOverview(&overviewResult{
		OK:    true,
		State: "ok",
	}, 0)
	if !result.OK || result.State != "ok" || len(result.Actions) != 0 || len(result.ActionDetails) != 0 || result.TotalActions != 0 {
		t.Fatalf("result = %+v", result)
	}

	out := &bytes.Buffer{}
	if err := renderNextActionResult(out, result, false, nil); err != nil {
		t.Fatalf("render next: %v", err)
	}
	if !strings.Contains(out.String(), "actions: none") {
		t.Fatalf("rendered next:\n%s", out.String())
	}
}

func findOperatorActionHint(hints []operatorActionHint, command string) (operatorActionHint, bool) {
	for _, hint := range hints {
		if hint.Command == command {
			return hint, true
		}
	}
	return operatorActionHint{}, false
}

func TestNextWatchRendersUntilContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &bytes.Buffer{}
	calls := 0

	err := runNextWatch(ctx, out, func(now time.Time) (*overviewResult, error) {
		calls++
		cancel()
		return &overviewResult{
			OK:         false,
			State:      "active",
			CapturedAt: now.UTC().Format(time.RFC3339),
			Actions:    []string{"agent-team queue drain --dry-run"},
		}, nil
	}, 0, false, nil, time.Millisecond, false)
	if err != nil {
		t.Fatalf("runNextWatch: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if !strings.Contains(out.String(), "next: active") || !strings.Contains(out.String(), "agent-team queue drain --dry-run") {
		t.Fatalf("next watch output:\n%s", out.String())
	}
}
