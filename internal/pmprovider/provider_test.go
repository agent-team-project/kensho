package pmprovider

import (
	"context"
	"testing"

	"github.com/jamesaud/agent-team/internal/job"
)

func TestConfiguredProviderNamePrefersPMProvider(t *testing.T) {
	name, source := ConfiguredProviderNameWithSource("linear", "none")
	if name != ProviderLinear || source != "pm.provider" {
		t.Fatalf("name/source = %q/%q, want linear from pm.provider", name, source)
	}
}

func TestConfiguredProviderNameFallsBackToLegacyPMTool(t *testing.T) {
	name, source := ConfiguredProviderNameWithSource("", "linear")
	if name != ProviderLinear || source != "team.pm_tool" {
		t.Fatalf("name/source = %q/%q, want linear from team.pm_tool", name, source)
	}
}

func TestLoadConfigUsesPMProvider(t *testing.T) {
	teamDir := testTeamDir(t, `[pm]
provider = "linear"

[team]
pm_tool = "none"
`)
	cfg, err := LoadConfig(teamDir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Provider != ProviderLinear || cfg.Source != "pm.provider" {
		t.Fatalf("config = %+v, want linear from pm.provider", cfg)
	}
}

func TestNoneProviderWriteBackPreservesLegacySkipAudit(t *testing.T) {
	teamDir := testTeamDir(t, `[pm]
provider = "none"

[team]
pm_tool = "none"
`)
	j := testJob()
	result := NoneProvider{}.WriteBack(context.Background(), teamDir, Request{Action: ActionDispatchInProgress, Job: j, Actor: "test"})
	if !result.Skipped || result.Message != "Linear not configured for this repo" || result.AuditErr != nil {
		t.Fatalf("result = %+v, want legacy skip", result)
	}
	events, err := job.ListEvents(teamDir, j.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != "linear_writeback_skipped" || events[0].Message != "Linear not configured for this repo" {
		t.Fatalf("events = %+v, want legacy linear skip audit", events)
	}
}
