package cli

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/resource"
)

func TestReadCommandPrintsResourceData(t *testing.T) {
	root, cleanup := setupReadCommandDeployment(t, "dep", "SQU-124", "")
	defer cleanup()

	stdout, stderr, err := executeReadCommand("read", resource.JobURI("dep", "squ-124"), "--repo", root)
	if err != nil {
		t.Fatalf("read command: %v\nstderr=%s", err, stderr)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(stdout), &data); err != nil {
		t.Fatalf("decode stdout: %v\nbody=%s", err, stdout)
	}
	if data["id"] != "squ-124" || data["kickoff"] != "read resources" {
		t.Fatalf("resource data = %+v", data)
	}
	if strings.Contains(stdout, `"kind"`) || strings.Contains(stdout, `"data"`) {
		t.Fatalf("default output should be resource data only, got %s", stdout)
	}
}

func TestReadCommandJSONPrintsEnvelope(t *testing.T) {
	root, cleanup := setupReadCommandDeployment(t, "dep", "SQU-124", "")
	defer cleanup()

	stdout, stderr, err := executeReadCommand("read", resource.JobURI("dep", "squ-124"), "--repo", root, "--json")
	if err != nil {
		t.Fatalf("read command json: %v\nstderr=%s", err, stderr)
	}
	var body struct {
		URI  string         `json:"uri"`
		Kind string         `json:"kind"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &body); err != nil {
		t.Fatalf("decode stdout: %v\nbody=%s", err, stdout)
	}
	if body.URI != resource.JobURI("dep", "squ-124") || body.Kind != resource.KindJob || body.Data["id"] != "squ-124" {
		t.Fatalf("resource envelope = %+v", body)
	}
}

func TestReadCommandUsesAddressingRoute(t *testing.T) {
	base := t.TempDir()
	primary := filepath.Join(base, "primary")
	receiver, cleanup := setupReadCommandDeployment(t, "receiver-dep", "SQU-225", filepath.Join(base, "receiver"))
	defer cleanup()
	writeReadCommandConfig(t, filepath.Join(primary, ".agent_team"), `[project]
id = "primary-dep"

[feedback.routes.receiver]
type = "local"
root = "../receiver"
`)
	t.Setenv("AGENT_TEAM_DAEMON_URL", "http://127.0.0.1:1")
	t.Setenv(daemon.DaemonTokenFileEnv, filepath.Join(base, "missing-token"))

	stdout, stderr, err := executeReadCommand("read", resource.JobURI("receiver-dep", "squ-225"), "--repo", primary)
	if err != nil {
		t.Fatalf("read routed command: %v\nstderr=%s receiver=%s", err, stderr, receiver)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(stdout), &data); err != nil {
		t.Fatalf("decode stdout: %v\nbody=%s", err, stdout)
	}
	if data["id"] != "squ-225" {
		t.Fatalf("routed resource data = %+v", data)
	}
}

func setupReadCommandDeployment(t *testing.T, deploymentID, ticket, root string) (string, func()) {
	t.Helper()
	if root == "" {
		root = t.TempDir()
	}
	teamDir := filepath.Join(root, ".agent_team")
	writeReadCommandConfig(t, teamDir, "[project]\nid = \""+deploymentID+"\"\n")
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	j, err := jobstore.New(ticket, "worker", "read resources", now)
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	m := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), nil)
	srv := httptest.NewServer(daemon.Handler(m, nil, nil, teamDir))
	if err := os.MkdirAll(filepath.Dir(daemon.HTTPAddrPath(teamDir)), 0o755); err != nil {
		t.Fatalf("mkdir daemon dir: %v", err)
	}
	if err := os.WriteFile(daemon.HTTPAddrPath(teamDir), []byte(strings.TrimPrefix(srv.URL, "http://")+"\n"), 0o644); err != nil {
		t.Fatalf("write http addr: %v", err)
	}
	if err := os.WriteFile(daemon.OperatorTokenPath(teamDir), []byte("operator-token\n"), 0o600); err != nil {
		t.Fatalf("write operator token: %v", err)
	}
	return root, srv.Close
}

func writeReadCommandConfig(t *testing.T, teamDir, body string) {
	t.Helper()
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatalf("mkdir team dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func executeReadCommand(args ...string) (string, string, error) {
	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}
