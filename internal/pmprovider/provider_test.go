package pmprovider

import (
	"context"
	"testing"

	"github.com/agent-team-project/agent-team/internal/job"
)

func TestLoadConfigUsesPMProvider(t *testing.T) {
	teamDir := testTeamDir(t, `[pm]
provider = "linear"
`)
	cfg, err := LoadConfig(teamDir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Provider != ProviderLinear || cfg.Source != "pm.provider" {
		t.Fatalf("config = %+v, want linear from pm.provider", cfg)
	}
}

func TestNormalizeProviderNameSupportsGitHub(t *testing.T) {
	if name := NormalizeProviderName("github"); name != ProviderGitHub {
		t.Fatalf("name = %q, want github", name)
	}
	if !KnownProvider(ProviderGitHub) {
		t.Fatalf("KnownProvider(github) = false, want true")
	}
}

func TestNoneProviderWriteBackPreservesSkipAudit(t *testing.T) {
	teamDir := testTeamDir(t, `[pm]
provider = "none"
`)
	j := testJob()
	result := NoneProvider{}.WriteBack(context.Background(), teamDir, Request{Action: ActionDispatchInProgress, Job: j, Actor: "test"})
	if !result.Skipped || result.Message != "Linear not configured for this repo" || result.AuditErr != nil {
		t.Fatalf("result = %+v, want provider-disabled skip", result)
	}
	events, err := job.ListEvents(teamDir, j.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != "linear_writeback_skipped" || events[0].Message != "Linear not configured for this repo" {
		t.Fatalf("events = %+v, want provider-disabled skip audit", events)
	}
}
