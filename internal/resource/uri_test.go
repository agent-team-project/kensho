package resource

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestURIParseRoundTrip(t *testing.T) {
	const dep = "a1290ed4-258f-4706-964b-c1aa3d82f6fc"
	got := URIWithFragment(dep, KindJob, "squ-128", "step=implement")
	if got != "agt://a1290ed4-258f-4706-964b-c1aa3d82f6fc/job/squ-128#step=implement" {
		t.Fatalf("URIWithFragment = %q", got)
	}
	parsed, err := Parse(got)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.DeploymentID != dep || parsed.Kind != KindJob || parsed.ID != "squ-128" || parsed.Fragment != "step=implement" {
		t.Fatalf("parsed = %+v", parsed)
	}
}

func TestURIEscapesPathSegments(t *testing.T) {
	const dep = "dep"
	got := URI(dep, KindWorkspace, "channel/#blocked")
	if got != "agt://dep/workspace/channel%2F%23blocked" {
		t.Fatalf("escaped URI = %q", got)
	}
	parsed, err := Parse(got)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.ID != "channel/#blocked" {
		t.Fatalf("parsed id = %q", parsed.ID)
	}
}

func TestParseRejectsNonCanonicalURIs(t *testing.T) {
	tests := []string{
		"agt://dep/job/squ-1/",
		"agt://dep//job/squ-1",
		"agt://dep/job/squ-1?x=y",
		"agt://dep/job/squ-1?",
		"agt://dep/job/",
		"agt://dep/job",
		"agt://dep//",
		"agt:dep/job/squ-1",
		"agt://user@dep/job/squ-1",
	}
	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			if got, err := Parse(raw); err == nil {
				t.Fatalf("Parse(%q) = %+v, nil; want error", raw, got)
			}
		})
	}
}

func TestWorkspaceIDDeterministicBackfill(t *testing.T) {
	if got := WorkspaceID("/repo/.claude/worktrees/worker-squ-1", "squ-1-abc123", "squ-1", "worker-squ-1"); got != "branch:squ-1-abc123" {
		t.Fatalf("branch workspace id = %q", got)
	}
	first := WorkspaceID("/tmp/example", "", "", "")
	second := WorkspaceID(filepath.Clean("/tmp//example"), "", "", "")
	if first == "" || first != second {
		t.Fatalf("path workspace ids = %q, %q", first, second)
	}
}

func TestUsageURIUsesStartedAtFragment(t *testing.T) {
	got := UsageURI("dep", "worker-squ-1", time.Date(2026, 7, 7, 9, 0, 0, 1, time.UTC))
	if got != "agt://dep/usage/worker-squ-1#started_at=2026-07-07T09:00:00.000000001Z" {
		t.Fatalf("UsageURI = %q", got)
	}
}

func TestDeploymentFromTeamDirParentRoundTrip(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const parent = "agt://parent/project/parent"
	body := []byte("[project]\nid = \"child\"\nparent_uri = \"" + parent + "\"\n")
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	deployment, err := DeploymentFromTeamDir(teamDir)
	if err != nil {
		t.Fatalf("DeploymentFromTeamDir: %v", err)
	}
	if deployment.ID != "child" || deployment.URI != "agt://child/project/child" || deployment.ParentURI != parent {
		t.Fatalf("deployment = %+v", deployment)
	}
}
