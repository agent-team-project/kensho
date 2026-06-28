package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
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
		"agent-team team queue retry delivery --all --job squ-700 --sort attempts --limit 10 --dry-run",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("next team output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "agent-team team queue retry delivery --all --sort attempts --limit 10 --dry-run") {
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
	for _, want := range []string{"next: attention", "team: delivery", "agent-team team queue retry delivery --all --job squ-700 --sort attempts --limit 10 --dry-run"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("team next text missing %q:\n%s", want, textOut.String())
		}
	}
	if strings.Contains(textOut.String(), "agent-team team queue retry delivery --all --sort attempts --limit 10 --dry-run") {
		t.Fatalf("team next text should prefer job-filtered retry:\n%s", textOut.String())
	}
}

func TestNextCommandDetailsTextIncludesSourceAndReason(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"next", "--target", root, "--source", "queue", "--reason", "queue_dead_letter", "--details"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("next details text: %v\nstderr=%s", err, stderr.String())
	}
	for _, want := range []string{
		"[queue/queue_dead_letter] agent-team job queue retry squ-700 --all --sort attempts --limit 10 --dry-run",
		"[queue/queue_dead_letter] agent-team repair --skip-tick --dry-run",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("next details output missing %q:\n%s", want, out.String())
		}
	}

	team := NewRootCmd()
	teamOut, teamErr := &bytes.Buffer{}, &bytes.Buffer{}
	team.SetOut(teamOut)
	team.SetErr(teamErr)
	team.SetArgs([]string{"team", "next", "delivery", "--repo", root, "--source", "queue", "--details"})
	if err := team.Execute(); err != nil {
		t.Fatalf("team next details text: %v\nstderr=%s", err, teamErr.String())
	}
	if !strings.Contains(teamOut.String(), "team: delivery") {
		t.Fatalf("team next details output missing team header:\n%s", teamOut.String())
	}
	if !strings.Contains(teamOut.String(), "[queue/queue_dead_letter] agent-team team queue retry delivery --all --job squ-700 --sort attempts --limit 10 --dry-run") {
		t.Fatalf("team next details output missing queue retry detail:\n%s", teamOut.String())
	}
}

func TestNextCommandCommandsPrintsOnlyActions(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"next", "--target", root, "--source", "queue", "--reason", "queue_dead_letter", "--sort", "command", "--commands"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("next commands: %v\nstderr=%s", err, stderr.String())
	}
	want := strings.Join([]string{
		strings.Join(shellQuoteArgs([]string{"agent-team", "--repo", root, "job", "queue", "retry", "squ-700", "--all", "--sort", "attempts", "--limit", "10", "--dry-run"}), " "),
		strings.Join(shellQuoteArgs([]string{"agent-team", "--repo", root, "repair", "--skip-tick", "--dry-run"}), " "),
	}, "\n") + "\n"
	if got := out.String(); got != want {
		t.Fatalf("next commands output = %q, want %q", got, want)
	}

	team := NewRootCmd()
	teamOut, teamErr := &bytes.Buffer{}, &bytes.Buffer{}
	team.SetOut(teamOut)
	team.SetErr(teamErr)
	team.SetArgs([]string{"team", "next", "delivery", "--repo", root, "--source", "queue", "--commands"})
	if err := team.Execute(); err != nil {
		t.Fatalf("team next commands: %v\nstderr=%s", err, teamErr.String())
	}
	if strings.Contains(teamOut.String(), "next:") || strings.Contains(teamOut.String(), "team:") || strings.Contains(teamOut.String(), "[queue/") {
		t.Fatalf("team next commands should not include headers or detail labels:\n%s", teamOut.String())
	}
	wantTeamRetry := strings.Join(shellQuoteArgs([]string{"agent-team", "--repo", root, "team", "queue", "retry", "delivery", "--all", "--job", "squ-700", "--sort", "attempts", "--limit", "10", "--dry-run"}), " ")
	if !strings.Contains(teamOut.String(), wantTeamRetry) {
		t.Fatalf("team next commands missing scoped queue retry:\n%s", teamOut.String())
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

func TestNextCommandSortsActionsBeforeLimit(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"next", "--target", root, "--sort", "command", "--limit", "1", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("next sort command json: %v\nstderr=%s", err, stderr.String())
	}

	var result nextActionResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode next sort command json: %v\nbody=%s", err, out.String())
	}
	if len(result.Actions) != 1 || result.Actions[0] != "agent-team daemon start" || result.HiddenActions == 0 {
		t.Fatalf("sorted result = %+v, want daemon start first with hidden actions", result)
	}
	if len(result.ActionDetails) != 1 || result.ActionDetails[0].Command != result.Actions[0] || result.ActionDetails[0].Source != "health" {
		t.Fatalf("sorted detail = %+v, want aligned health detail", result.ActionDetails)
	}

	team := NewRootCmd()
	teamOut, teamErr := &bytes.Buffer{}, &bytes.Buffer{}
	team.SetOut(teamOut)
	team.SetErr(teamErr)
	team.SetArgs([]string{"team", "next", "delivery", "--repo", root, "--sort", "source", "--limit", "1", "--details"})
	if err := team.Execute(); err != nil {
		t.Fatalf("team next sort source text: %v\nstderr=%s", err, teamErr.String())
	}
	if !strings.Contains(teamOut.String(), "[health/daemon_not_running] agent-team daemon start") || !strings.Contains(teamOut.String(), "... ") {
		t.Fatalf("team next sort source output:\n%s", teamOut.String())
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

func TestNextCommandFiltersBySourceAndReason(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"next", "--target", root, "--source", "queue", "--reason", "queue_dead_letter", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("next filtered json: %v\nstderr=%s", err, stderr.String())
	}

	var result nextActionResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode filtered next json: %v\nbody=%s", err, out.String())
	}
	if len(result.Actions) != 2 || !stringSliceContains(result.Actions, "agent-team job queue retry squ-700 --all --sort attempts --limit 10 --dry-run") || !stringSliceContains(result.Actions, "agent-team repair --skip-tick --dry-run") || result.TotalActions != 2 || result.HiddenActions != 0 {
		t.Fatalf("filtered result = %+v", result)
	}
	if len(result.ActionDetails) != 2 {
		t.Fatalf("filtered details = %+v", result.ActionDetails)
	}
	for _, detail := range result.ActionDetails {
		if detail.Source != "queue" || detail.Reason != "queue_dead_letter" {
			t.Fatalf("filtered detail = %+v, want queue queue_dead_letter", detail)
		}
	}

	reasonPrefix := NewRootCmd()
	prefixOut, prefixErr := &bytes.Buffer{}, &bytes.Buffer{}
	reasonPrefix.SetOut(prefixOut)
	reasonPrefix.SetErr(prefixErr)
	reasonPrefix.SetArgs([]string{"next", "--target", root, "--source", "schedules", "--reason", "due", "--json"})
	if err := reasonPrefix.Execute(); err != nil {
		t.Fatalf("next reason prefix json: %v\nstderr=%s", err, prefixErr.String())
	}
	var prefixResult nextActionResult
	if err := json.Unmarshal(prefixOut.Bytes(), &prefixResult); err != nil {
		t.Fatalf("decode prefix next json: %v\nbody=%s", err, prefixOut.String())
	}
	if len(prefixResult.Actions) != 1 || prefixResult.Actions[0] != "agent-team schedule fire --dry-run --preview-triggers" {
		t.Fatalf("prefix-filtered result = %+v", prefixResult)
	}

	overviewSource := NewRootCmd()
	overviewSourceOut, overviewSourceErr := &bytes.Buffer{}, &bytes.Buffer{}
	overviewSource.SetOut(overviewSourceOut)
	overviewSource.SetErr(overviewSourceErr)
	overviewSource.SetArgs([]string{"next", "--target", root, "--source", "overview", "--reason", "drainable_work", "--json"})
	if err := overviewSource.Execute(); err != nil {
		t.Fatalf("next overview source json: %v\nstderr=%s", err, overviewSourceErr.String())
	}
	var overviewSourceResult nextActionResult
	if err := json.Unmarshal(overviewSourceOut.Bytes(), &overviewSourceResult); err != nil {
		t.Fatalf("decode overview source next json: %v\nbody=%s", err, overviewSourceOut.String())
	}
	if len(overviewSourceResult.Actions) != 1 || overviewSourceResult.Actions[0] != "agent-team drain" {
		t.Fatalf("overview-source result = %+v", overviewSourceResult)
	}

	staleRoot := writeOverviewStaleRunningFixture(t)
	stale := NewRootCmd()
	staleOut, staleErr := &bytes.Buffer{}, &bytes.Buffer{}
	stale.SetOut(staleOut)
	stale.SetErr(staleErr)
	stale.SetArgs([]string{"next", "--target", staleRoot, "--source", "jobs", "--reason", "stale_running", "--json"})
	if err := stale.Execute(); err != nil {
		t.Fatalf("next stale-running json: %v\nstderr=%s", err, staleErr.String())
	}
	var staleResult nextActionResult
	if err := json.Unmarshal(staleOut.Bytes(), &staleResult); err != nil {
		t.Fatalf("decode stale-running next json: %v\nbody=%s", err, staleOut.String())
	}
	if len(staleResult.Actions) != 1 || staleResult.Actions[0] != "agent-team repair --timeout-jobs --dry-run" {
		t.Fatalf("stale-running filtered result = %+v", staleResult)
	}

	teamStale := NewRootCmd()
	teamStaleOut, teamStaleErr := &bytes.Buffer{}, &bytes.Buffer{}
	teamStale.SetOut(teamStaleOut)
	teamStale.SetErr(teamStaleErr)
	teamStale.SetArgs([]string{"team", "next", "delivery", "--repo", staleRoot, "--source", "jobs", "--reason", "stale_running", "--json"})
	if err := teamStale.Execute(); err != nil {
		t.Fatalf("team next stale-running json: %v\nstderr=%s", err, teamStaleErr.String())
	}
	var teamStaleResult nextActionResult
	if err := json.Unmarshal(teamStaleOut.Bytes(), &teamStaleResult); err != nil {
		t.Fatalf("decode team stale-running next json: %v\nbody=%s", err, teamStaleOut.String())
	}
	if len(teamStaleResult.Actions) != 1 || teamStaleResult.Actions[0] != "agent-team team repair delivery --timeout-jobs --dry-run" {
		t.Fatalf("team stale-running filtered result = %+v", teamStaleResult)
	}
}

func TestTeamNextCommandFiltersBySource(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "next", "delivery", "--repo", root, "--source", "pipelines", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team next filtered json: %v\nstderr=%s", err, stderr.String())
	}

	var result nextActionResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode team filtered next json: %v\nbody=%s", err, out.String())
	}
	if len(result.Actions) != 1 || result.Actions[0] != "agent-team team tick delivery --dry-run --preview-routes" {
		t.Fatalf("team filtered result = %+v", result)
	}
	if len(result.ActionDetails) != 1 || result.ActionDetails[0].Team != "delivery" || result.ActionDetails[0].Source != "pipelines" {
		t.Fatalf("team filtered details = %+v", result.ActionDetails)
	}
}

func TestNextCommandFiltersRuntimeSource(t *testing.T) {
	root := writeOverviewRuntimeFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"next", "--target", root, "--source", "runtime", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("next runtime filtered json: %v\nstderr=%s", err, stderr.String())
	}
	var result nextActionResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode runtime next json: %v\nbody=%s", err, out.String())
	}
	if len(result.Actions) != 1 || result.Actions[0] != "agent-team resume-plan --status crashed --sort action --limit 10" {
		t.Fatalf("runtime filtered result = %+v", result)
	}
	if len(result.ActionDetails) != 1 || result.ActionDetails[0].Source != "runtime" || result.ActionDetails[0].Reason != "crashed=3" {
		t.Fatalf("runtime filtered details = %+v", result.ActionDetails)
	}

	team := NewRootCmd()
	teamOut, teamErr := &bytes.Buffer{}, &bytes.Buffer{}
	team.SetOut(teamOut)
	team.SetErr(teamErr)
	team.SetArgs([]string{"team", "next", "delivery", "--repo", root, "--source", "runtime", "--json"})
	if err := team.Execute(); err != nil {
		t.Fatalf("team next runtime filtered json: %v\nstderr=%s", err, teamErr.String())
	}
	var teamResult nextActionResult
	if err := json.Unmarshal(teamOut.Bytes(), &teamResult); err != nil {
		t.Fatalf("decode team runtime next json: %v\nbody=%s", err, teamOut.String())
	}
	if len(teamResult.Actions) != 1 || teamResult.Actions[0] != "agent-team team resume-plan delivery --status crashed --sort action --limit 10" {
		t.Fatalf("team runtime filtered result = %+v", teamResult)
	}
	if len(teamResult.ActionDetails) != 1 || teamResult.ActionDetails[0].Team != "delivery" || teamResult.ActionDetails[0].Source != "runtime" || teamResult.ActionDetails[0].Reason != "crashed=2" {
		t.Fatalf("team runtime filtered details = %+v", teamResult.ActionDetails)
	}
}

func TestNextCommandFiltersStaleRuntimeSource(t *testing.T) {
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
	cmd.SetArgs([]string{"next", "--target", root, "--source", "runtime", "--reason", "stale", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("next stale runtime filtered json: %v\nstderr=%s", err, stderr.String())
	}
	var result nextActionResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode stale runtime next json: %v\nbody=%s", err, out.String())
	}
	if len(result.Actions) != 1 || result.Actions[0] != "agent-team resume-plan --runtime-stale --sort stale --limit 10" {
		t.Fatalf("stale runtime filtered result = %+v", result)
	}
	if len(result.ActionDetails) != 1 || result.ActionDetails[0].Source != "runtime" || result.ActionDetails[0].Reason != "stale=2" {
		t.Fatalf("stale runtime filtered details = %+v", result.ActionDetails)
	}

	team := NewRootCmd()
	teamOut, teamErr := &bytes.Buffer{}, &bytes.Buffer{}
	team.SetOut(teamOut)
	team.SetErr(teamErr)
	team.SetArgs([]string{"team", "next", "delivery", "--repo", root, "--source", "runtime", "--reason", "stale", "--json"})
	if err := team.Execute(); err != nil {
		t.Fatalf("team next stale runtime filtered json: %v\nstderr=%s", err, teamErr.String())
	}
	var teamResult nextActionResult
	if err := json.Unmarshal(teamOut.Bytes(), &teamResult); err != nil {
		t.Fatalf("decode team stale runtime next json: %v\nbody=%s", err, teamOut.String())
	}
	if len(teamResult.Actions) != 1 || teamResult.Actions[0] != "agent-team team resume-plan delivery --runtime-stale --sort stale --limit 10" {
		t.Fatalf("team stale runtime filtered result = %+v", teamResult)
	}
	if len(teamResult.ActionDetails) != 1 || teamResult.ActionDetails[0].Team != "delivery" || teamResult.ActionDetails[0].Source != "runtime" || teamResult.ActionDetails[0].Reason != "stale=1" {
		t.Fatalf("team stale runtime filtered details = %+v", teamResult.ActionDetails)
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
			name: "next-commands-json-conflict",
			args: []string{"next", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "next-commands-format-conflict",
			args: []string{"next", "--commands", "--format", "{{.State}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "next-commands-watch-conflict",
			args: []string{"next", "--commands", "--watch"},
			want: "--commands cannot be combined with --watch",
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
			name: "team-next-commands-json-conflict",
			args: []string{"team", "next", "delivery", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "team-next-commands-format-conflict",
			args: []string{"team", "next", "delivery", "--commands", "--format", "{{.State}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "team-next-commands-watch-conflict",
			args: []string{"team", "next", "delivery", "--commands", "--watch"},
			want: "--commands cannot be combined with --watch",
		},
		{
			name: "team-next-invalid-template",
			args: []string{"team", "next", "delivery", "--format", "{{"},
			want: "invalid --format template",
		},
		{
			name: "next-invalid-source",
			args: []string{"next", "--source", "unknown"},
			want: "unknown --source",
		},
		{
			name: "next-empty-reason",
			args: []string{"next", "--reason", ","},
			want: "--reason requires",
		},
		{
			name: "next-invalid-sort",
			args: []string{"next", "--sort", "age"},
			want: "--sort must be default",
		},
		{
			name: "team-next-invalid-source",
			args: []string{"team", "next", "delivery", "--source", "unknown"},
			want: "unknown --source",
		},
		{
			name: "team-next-invalid-sort",
			args: []string{"team", "next", "delivery", "--sort", "age"},
			want: "--sort must be default",
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
		"agent-team intake replay --all --dedupe-request-id --dry-run --preview-triggers",
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

	alias := NewRootCmd()
	aliasOut, aliasErr := &bytes.Buffer{}, &bytes.Buffer{}
	alias.SetOut(aliasOut)
	alias.SetErr(aliasErr)
	alias.SetArgs([]string{"next", "--target", root, "--reason", "queue_quarantined", "--json"})
	if err := alias.Execute(); err != nil {
		t.Fatalf("next queue quarantine alias json: %v\nstderr=%s", err, aliasErr.String())
	}
	var aliasResult nextActionResult
	if err := json.Unmarshal(aliasOut.Bytes(), &aliasResult); err != nil {
		t.Fatalf("decode next queue quarantine alias: %v\nbody=%s", err, aliasOut.String())
	}
	for _, want := range []string{
		"agent-team queue quarantine ls",
		"agent-team queue quarantine ls --unrestorable",
		"agent-team snapshot --json",
	} {
		if !stringSliceContains(aliasResult.Actions, want) {
			t.Fatalf("queue quarantine alias actions missing %q: %+v", want, aliasResult)
		}
	}
	if aliasResult.TotalActions != len(aliasResult.Actions) || aliasResult.TotalActions != 3 {
		t.Fatalf("queue quarantine alias actions = %+v", aliasResult)
	}
	if len(aliasResult.ActionDetails) != len(aliasResult.Actions) {
		t.Fatalf("queue quarantine alias details = %+v", aliasResult.ActionDetails)
	}
	for _, detail := range aliasResult.ActionDetails {
		if detail.Source != "queue" || detail.Reason != "queue_quarantined" {
			t.Fatalf("queue quarantine alias detail = %+v", detail)
		}
	}

	outboxAlias := NewRootCmd()
	outboxAliasOut, outboxAliasErr := &bytes.Buffer{}, &bytes.Buffer{}
	outboxAlias.SetOut(outboxAliasOut)
	outboxAlias.SetErr(outboxAliasErr)
	outboxAlias.SetArgs([]string{"next", "--target", root, "--reason", "outbox_quarantined", "--json"})
	if err := outboxAlias.Execute(); err != nil {
		t.Fatalf("next outbox quarantine alias on queue fixture: %v\nstderr=%s", err, outboxAliasErr.String())
	}
	var outboxAliasResult nextActionResult
	if err := json.Unmarshal(outboxAliasOut.Bytes(), &outboxAliasResult); err != nil {
		t.Fatalf("decode next outbox quarantine alias on queue fixture: %v\nbody=%s", err, outboxAliasOut.String())
	}
	if len(outboxAliasResult.Actions) != 0 || outboxAliasResult.TotalActions != 0 {
		t.Fatalf("outbox quarantine alias should not match queue actions: %+v", outboxAliasResult)
	}
}

func TestNextCommandReportsJobQuarantineAction(t *testing.T) {
	root := writeNextJobQuarantineFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"next", "--target", root, "--reason", "job_quarantined", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("next job quarantine alias json: %v\nstderr=%s", err, stderr.String())
	}

	var result nextActionResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode next job quarantine alias: %v\nbody=%s", err, out.String())
	}
	for _, want := range []string{
		"agent-team job quarantine",
		"agent-team job quarantine --unrestorable",
		"agent-team job quarantine --restorable",
		"agent-team snapshot --json",
	} {
		if !stringSliceContains(result.Actions, want) {
			t.Fatalf("job quarantine alias actions missing %q: %+v", want, result)
		}
	}
	if result.TotalActions != len(result.Actions) || result.TotalActions != 4 {
		t.Fatalf("job quarantine alias actions = %+v", result)
	}
	if len(result.ActionDetails) != len(result.Actions) {
		t.Fatalf("job quarantine alias details = %+v", result.ActionDetails)
	}
	for _, detail := range result.ActionDetails {
		if detail.Source != "jobs" || detail.Reason != "job_quarantined" {
			t.Fatalf("job quarantine alias detail = %+v", detail)
		}
	}

	broad := NewRootCmd()
	broadOut, broadErr := &bytes.Buffer{}, &bytes.Buffer{}
	broad.SetOut(broadOut)
	broad.SetErr(broadErr)
	broad.SetArgs([]string{"next", "--target", root, "--reason", "quarantined", "--json"})
	if err := broad.Execute(); err != nil {
		t.Fatalf("next broad quarantine alias json: %v\nstderr=%s", err, broadErr.String())
	}
	var broadResult nextActionResult
	if err := json.Unmarshal(broadOut.Bytes(), &broadResult); err != nil {
		t.Fatalf("decode next broad quarantine alias: %v\nbody=%s", err, broadOut.String())
	}
	if !stringSliceContains(broadResult.Actions, "agent-team job quarantine") {
		t.Fatalf("broad quarantine alias should include job quarantine actions: %+v", broadResult)
	}
}

func writeNextJobQuarantineFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	initInto(t, root)
	teamDir := filepath.Join(root, ".agent_team")
	writeQuarantinedJobFile(t, teamDir, "20260627T231500.000000000Z", "squ-231.toml", []byte(`id = "squ-231"
ticket = "SQU-231"
target = "worker"
status = "queued"
created_at = 2026-06-27T23:15:00Z
updated_at = 2026-06-27T23:15:00Z
`))
	writeQuarantinedJobFile(t, teamDir, "20260627T231500.000000000Z", "broken.toml", []byte("id = [\n"))
	return root
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
	if err := renderNextActionResult(out, result, false, nil, false, false, operatorCommandScope{}); err != nil {
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
	}, "", 0, nextActionFilters{}, false, nil, false, time.Millisecond, false)
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
