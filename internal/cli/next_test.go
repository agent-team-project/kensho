package cli

import (
	"bytes"
	"context"
	"encoding/json"
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
	for _, want := range []string{"agent-team repair --dry-run --jobs", "agent-team daemon start"} {
		if !stringSliceContains(result.Actions, want) {
			t.Fatalf("actions missing %q: %+v", want, result.Actions)
		}
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
		"agent-team team queue retry delivery --all --dry-run",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("next team output missing %q:\n%s", want, out.String())
		}
	}
}

func TestNextActionResultHandlesNoActions(t *testing.T) {
	result := nextActionResultFromOverview(&overviewResult{
		OK:    true,
		State: "ok",
	}, 0)
	if !result.OK || result.State != "ok" || len(result.Actions) != 0 || result.TotalActions != 0 {
		t.Fatalf("result = %+v", result)
	}

	out := &bytes.Buffer{}
	if err := renderNextActionResult(out, result, false); err != nil {
		t.Fatalf("render next: %v", err)
	}
	if !strings.Contains(out.String(), "actions: none") {
		t.Fatalf("rendered next:\n%s", out.String())
	}
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
	}, 0, false, time.Millisecond, false)
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
