package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-team-project/agent-team/internal/topology"
)

func TestScheduledObserverRequiredVerbsResolveThroughCommandTree(t *testing.T) {
	teamDir := filepath.Join("..", "..", ".agent_team")
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		t.Fatalf("load self-dogfood topology: %v", err)
	}
	root := NewRootCmd()
	for _, name := range []string{
		"feedback-triage",
		"debt-auditor",
		"harness-reviewer",
		"org-review",
		"sentinel",
		"product-verifier",
	} {
		inst := top.Instances[name]
		if inst == nil {
			t.Fatalf("scheduled observer %q is missing", name)
		}
		for _, required := range inst.RequiredVerbs {
			got, ok := resolveVerbPath(root, strings.Split(required, "."))
			if !ok || got != required {
				t.Fatalf("%s required verb %q resolved as (%q, %t)", name, required, got, ok)
			}
		}
	}
}
