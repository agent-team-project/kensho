package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-team-project/agent-team/internal/addressing"
)

func TestDeploymentsLsText(t *testing.T) {
	root := setupDeploymentsCommandRepo(t)
	stdout, stderr, err := executeDeploymentsCommand("deployments", "ls", "--repo", root)
	if err != nil {
		t.Fatalf("deployments ls: %v\nstderr=%s", err, stderr)
	}
	for _, want := range []string{
		"NAME",
		"URI",
		"self",
		"agt://primary-dep/project/primary-dep",
		"receiver",
		"agt://receiver-dep/project/receiver-dep",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("deployments ls missing %q:\n%s", want, stdout)
		}
	}
}

func TestDeploymentsLsJSON(t *testing.T) {
	root := setupDeploymentsCommandRepo(t)
	stdout, stderr, err := executeDeploymentsCommand("deployments", "--repo", root, "--json")
	if err != nil {
		t.Fatalf("deployments json: %v\nstderr=%s", err, stderr)
	}
	var entries []addressing.DeploymentEntry
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("decode deployments json: %v\nbody=%s", err, stdout)
	}
	if len(entries) != 2 {
		t.Fatalf("entries len = %d, want 2: %+v", len(entries), entries)
	}
	if entries[0].Name != "self" || entries[0].URI != "agt://primary-dep/project/primary-dep" {
		t.Fatalf("first entry = %+v", entries[0])
	}
}

func TestDeploymentsResolveNameAndAlias(t *testing.T) {
	root := setupDeploymentsCommandRepo(t)
	stdout, stderr, err := executeDeploymentsCommand("deployments", "resolve", "receiver", "--repo", root)
	if err != nil {
		t.Fatalf("deployment resolve: %v\nstderr=%s", err, stderr)
	}
	if got, want := strings.TrimSpace(stdout), "agt://receiver-dep/project/receiver-dep"; got != want {
		t.Fatalf("resolve receiver = %q, want %q", got, want)
	}

	stdout, stderr, err = executeDeploymentsCommand("deployments", "resolve", "local", "--repo", root, "--format", "{{.Name}} {{.URI}}")
	if err != nil {
		t.Fatalf("deployments resolve format: %v\nstderr=%s", err, stderr)
	}
	if got, want := strings.TrimSpace(stdout), "self agt://primary-dep/project/primary-dep"; got != want {
		t.Fatalf("resolve local format = %q, want %q", got, want)
	}
}

func TestDeploymentsResolveMissing(t *testing.T) {
	root := setupDeploymentsCommandRepo(t)
	_, stderr, err := executeDeploymentsCommand("deployments", "resolve", "missing", "--repo", root)
	if err == nil {
		t.Fatal("deployments resolve missing succeeded")
	}
	if !strings.Contains(stderr, `deployment "missing" is not in the registry view`) {
		t.Fatalf("stderr = %q", stderr)
	}
}

func setupDeploymentsCommandRepo(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "primary")
	receiver := filepath.Join(base, "receiver")
	writeDeploymentsCommandConfig(t, filepath.Join(root, ".agent_team"), `[project]
id = "primary-dep"

[feedback.routes.receiver]
type = "local"
root = "../receiver"
`)
	writeDeploymentsCommandConfig(t, filepath.Join(receiver, ".agent_team"), `[project]
id = "receiver-dep"
`)
	return root
}

func writeDeploymentsCommandConfig(t *testing.T, teamDir, body string) {
	t.Helper()
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func executeDeploymentsCommand(args ...string) (string, string, error) {
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}
