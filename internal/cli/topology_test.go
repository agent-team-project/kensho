package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/topology"
)

const topoFixture = `
[instances.manager]
agent = "manager"

[[instances.manager.triggers]]
event = "user_invocation"

[instances.worker]
agent     = "worker"
ephemeral = true
replicas  = 2

[[instances.worker.triggers]]
event        = "agent.dispatch"
match.target = "worker"
`

// topoTestEnv stands up an in-process daemon Handler with a topology loaded
// from `instances.toml` written to teamDir, plus a daemonClient pointed at
// it. Mirrors channelTestEnv's shape.
type topoTestEnv struct {
	client  *daemonClient
	srv     *httptest.Server
	teamDir string
	mgr     *daemon.InstanceManager
}

func newTopoTestEnv(t *testing.T) *topoTestEnv {
	t.Helper()
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		t.Fatalf("load topology: %v", err)
	}
	mgr := daemon.NewInstanceManager(t.TempDir(), nil)
	resolver := daemon.NewEventResolver(mgr, teamDir, top)
	srv := httptest.NewServer(daemon.Handler(mgr, nil, resolver, teamDir))
	c := &daemonClient{
		hc:      &http.Client{Timeout: 0},
		baseURL: srv.URL,
		teamDir: teamDir,
	}
	t.Cleanup(srv.Close)
	return &topoTestEnv{client: c, srv: srv, teamDir: teamDir, mgr: mgr}
}

func TestClient_Topology(t *testing.T) {
	env := newTopoTestEnv(t)
	res, err := env.client.Topology()
	if err != nil {
		t.Fatalf("topology: %v", err)
	}
	if len(res.Instances) != 2 {
		t.Errorf("instances: %v", res.Instances)
	}
	for _, i := range res.Instances {
		if i.Name == "worker" {
			if !i.Ephemeral || i.Replicas != 2 {
				t.Errorf("worker: %+v", i)
			}
		}
	}
}

func TestClient_TopologyReload(t *testing.T) {
	env := newTopoTestEnv(t)
	// Replace the file and reload.
	if err := os.WriteFile(filepath.Join(env.teamDir, "instances.toml"), []byte(`
[instances.solo]
agent = "manager"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := env.client.TopologyReload()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(res.Instances) != 1 || res.Instances[0].Name != "solo" {
		t.Errorf("after reload: %v", res.Instances)
	}
}

func TestClient_PublishEvent_Persistent(t *testing.T) {
	env := newTopoTestEnv(t)
	res, err := env.client.PublishEvent("user_invocation", map[string]any{"name": "manager"})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if len(res.Matched) != 1 || res.Matched[0] != "manager" {
		t.Errorf("matched: %v", res.Matched)
	}
	if len(res.Messaged) != 1 {
		t.Errorf("messaged: %v", res.Messaged)
	}
}

func TestClient_PublishEvent_NoMatch(t *testing.T) {
	env := newTopoTestEnv(t)
	res, err := env.client.PublishEvent("ticket_webhook", map[string]any{"project": "Mobile"})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if len(res.Matched) != 0 {
		t.Errorf("expected no matches, got %v", res.Matched)
	}
}

func TestTopologyShow_LocalFallback(t *testing.T) {
	// No daemon — `topology show` falls back to file parsing.
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	out := &strings.Builder{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"topology", "show", "--target", target})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	for _, want := range []string{"manager", "worker", "agent.dispatch"} {
		if !strings.Contains(got, want) {
			t.Errorf("topology show missing %q in output: %s", want, got)
		}
	}
}

func TestTopologyShow_NoFile(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	out := &strings.Builder{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"topology", "show", "--target", target})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "no instances declared") {
		t.Errorf("expected helpful empty-state message, got: %s", out.String())
	}
}
