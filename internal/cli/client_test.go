package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/agent-team-project/agent-team/internal/daemon"
)

// newTestClient wires a daemonClient at an httptest server. The unix-socket
// transport in newDaemonClient is the only piece we don't exercise here; the
// JSON wire format is identical, so this is enough coverage for the CLI
// layer.
func newTestClient(t *testing.T, h http.Handler) (*daemonClient, func()) {
	t.Helper()
	srv := httptest.NewServer(h)
	c := &daemonClient{
		hc:      newDaemonHTTPClient(srv.Client().Transport, 0, ""),
		baseURL: srv.URL,
		teamDir: t.TempDir(),
	}
	return c, srv.Close
}

func writeClientClaudeSession(t *testing.T, configDir, workspace, sessionID string) {
	t.Helper()
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		t.Fatal(err)
	}
	var encoded strings.Builder
	for _, r := range absWorkspace {
		if (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			encoded.WriteRune(r)
			continue
		}
		encoded.WriteByte('-')
	}
	dir := filepath.Join(configDir, "projects", encoded.String())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionID+".jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestClient_AttachesBuildHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get(buildinfo.HeaderName)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"ready": true, "instances": 0}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer srv.Close()
	c := &daemonClient{
		hc:      newDaemonHTTPClient(srv.Client().Transport, 0, ""),
		baseURL: srv.URL,
		teamDir: t.TempDir(),
	}

	if _, err := c.Status(); err != nil {
		t.Fatalf("status: %v", err)
	}
	if got == "" {
		t.Fatal("missing build header")
	}
	parsed, err := buildinfo.ParseHeaderValue(got)
	if err != nil {
		t.Fatalf("parse build header: %v", err)
	}
	if !buildinfo.Equivalent(parsed, BuildInfo()) {
		t.Fatalf("header build = %+v, want current CLI build %+v", parsed, BuildInfo())
	}
}

func TestClient_AttachesBearerFromTokenFile(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"ready": true, "instances": 0}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer srv.Close()
	tokenFile := filepath.Join(t.TempDir(), "daemon.token")
	if err := os.WriteFile(tokenFile, []byte("secret-token\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	c := &daemonClient{
		hc:      newDaemonHTTPClient(srv.Client().Transport, 0, tokenFile),
		baseURL: srv.URL,
		teamDir: t.TempDir(),
	}

	if _, err := c.Status(); err != nil {
		t.Fatalf("status: %v", err)
	}
	if got != "Bearer secret-token" {
		t.Fatalf("Authorization = %q, want bearer token", got)
	}
}

func TestNewDaemonClientUsesEnvHTTPURLWhenPidfileStale(t *testing.T) {
	teamDir := t.TempDir()
	if err := os.WriteFile(daemon.PidPath(teamDir), []byte("999999999\n"), 0o644); err != nil {
		t.Fatalf("write pidfile: %v", err)
	}
	t.Setenv("AGENT_TEAM_DAEMON_URL", "")
	t.Setenv(daemon.DaemonTokenFileEnv, "")
	restorePIDLiveCheck := daemon.SetPidLiveCheckForTest(func(pid int) bool { return false })
	defer restorePIDLiveCheck()

	tokenFile := filepath.Join(t.TempDir(), "daemon.token")
	if err := os.WriteFile(tokenFile, []byte("env-token\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/status" {
			t.Fatalf("path = %s, want /v1/status", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"ready": true, "instances": 0}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer srv.Close()
	t.Setenv("AGENT_TEAM_DAEMON_URL", srv.URL)
	t.Setenv(daemon.DaemonTokenFileEnv, tokenFile)

	c, err := newDaemonClientWithTimeout(teamDir, time.Second)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	status, err := c.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Ready {
		t.Fatalf("status ready = false, want true")
	}
	if gotAuth != "Bearer env-token" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
}

func TestNewDaemonClientUsesPersistedHTTPAddrWhenPidfileStale(t *testing.T) {
	teamDir := t.TempDir()
	if err := os.WriteFile(daemon.PidPath(teamDir), []byte("999999999\n"), 0o644); err != nil {
		t.Fatalf("write pidfile: %v", err)
	}
	t.Setenv("AGENT_TEAM_DAEMON_URL", "")
	t.Setenv(daemon.DaemonTokenFileEnv, "")
	restorePIDLiveCheck := daemon.SetPidLiveCheckForTest(func(pid int) bool { return false })
	defer restorePIDLiveCheck()

	tokenFile := daemon.OperatorTokenPath(teamDir)
	if err := os.MkdirAll(filepath.Dir(tokenFile), 0o700); err != nil {
		t.Fatalf("mkdir token dir: %v", err)
	}
	if err := os.WriteFile(tokenFile, []byte("operator-token\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/status" {
			t.Fatalf("path = %s, want /v1/status", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"ready": true, "instances": 0}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer srv.Close()
	if err := os.WriteFile(daemon.HTTPAddrPath(teamDir), []byte(strings.TrimPrefix(srv.URL, "http://")+"\n"), 0o644); err != nil {
		t.Fatalf("write http addr: %v", err)
	}

	c, err := newDaemonClientWithTimeout(teamDir, time.Second)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	status, err := c.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Ready {
		t.Fatalf("status ready = false, want true")
	}
	if gotAuth != "Bearer operator-token" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
}

func TestClient_Dispatch(t *testing.T) {
	root := t.TempDir()
	m := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, time.Second))
	c, cleanup := newTestClient(t, daemon.Handler(m, nil, nil, ""))
	defer cleanup()

	resp, err := c.Dispatch(dispatchPayload{
		Agent:     "worker",
		Name:      "w-1",
		Workspace: t.TempDir(),
		Args:      []string{"--add-dir", "/tmp/x"},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if resp.InstanceID != "w-1" || resp.PID == 0 || resp.SessionID == "" {
		t.Errorf("response: %+v", resp)
	}
	stopAndWaitForTest(t, m, "w-1")
}

func TestClient_Instances(t *testing.T) {
	root := t.TempDir()
	m := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, time.Second))
	c, cleanup := newTestClient(t, daemon.Handler(m, nil, nil, ""))
	defer cleanup()

	_, err := c.Dispatch(dispatchPayload{Agent: "w", Name: "x", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer stopAndWaitForTest(t, m, "x")

	insts, err := c.Instances()
	if err != nil {
		t.Fatalf("instances: %v", err)
	}
	if len(insts) != 1 || insts[0].Instance != "x" {
		t.Errorf("instances: %+v", insts)
	}
}

func TestClient_Reconcile(t *testing.T) {
	root := t.TempDir()
	restorePIDLiveCheck := daemon.SetPidLiveCheckForTest(func(pid int) bool { return false })
	defer restorePIDLiveCheck()

	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "orphan",
		Agent:    "manager",
		Status:   daemon.StatusRunning,
		PID:      999999,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	m := daemon.NewInstanceManager(root, nil)
	c, cleanup := newTestClient(t, daemon.Handler(m, nil, nil, ""))
	defer cleanup()

	resp, err := c.Reconcile()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !resp.Reconciled || resp.Changed != 1 {
		t.Fatalf("response = %+v, want one change", resp)
	}
	if len(resp.Changes) != 1 || resp.Changes[0].Instance != "orphan" || resp.Changes[0].After != daemon.StatusExited {
		t.Fatalf("changes = %+v, want orphan exited", resp.Changes)
	}
}

func TestClient_Events(t *testing.T) {
	root := t.TempDir()
	m := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, time.Second))
	c, cleanup := newTestClient(t, daemon.Handler(m, nil, nil, ""))
	defer cleanup()

	if _, err := c.Dispatch(dispatchPayload{Agent: "manager", Name: "mgr", Workspace: t.TempDir()}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	defer stopAndWaitForTest(t, m, "mgr")

	rc, err := c.Events(context.Background(), false, 10)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	defer rc.Close()
	body := new(bytes.Buffer)
	if _, err := body.ReadFrom(rc); err != nil {
		t.Fatalf("read events: %v", err)
	}
	if !strings.Contains(body.String(), `"action":"dispatch"`) || !strings.Contains(body.String(), `"instance":"mgr"`) {
		t.Fatalf("events body = %s", body.String())
	}
}

func TestClient_StartInstance(t *testing.T) {
	claudeConfigDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfigDir)
	root := t.TempDir()
	m := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, time.Second))
	c, cleanup := newTestClient(t, daemon.Handler(m, nil, nil, ""))
	defer cleanup()

	workspace := t.TempDir()
	disp, err := c.Dispatch(dispatchPayload{Agent: "manager", Name: "mgr", Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	writeClientClaudeSession(t, claudeConfigDir, workspace, disp.SessionID)
	if err := c.StopInstance("mgr"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := m.WaitForReaper("mgr", 10*time.Second); err != nil {
		t.Fatalf("wait stopped: %v", err)
	}
	if err := c.StartInstance("mgr"); err != nil {
		t.Fatalf("start: %v", err)
	}
	stopAndWaitForTest(t, m, "mgr")
}

func TestClient_StartInstanceWithFreshOption(t *testing.T) {
	var payload struct {
		Instance string `json:"instance"`
		Fresh    bool   `json:"fresh"`
		Force    bool   `json:"force"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/start" {
			t.Fatalf("path = %s, want /v1/start", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"instance_id":     "mgr",
			"session_resumed": false,
			"fresh_fallback":  true,
			"pid":             123,
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer srv.Close()
	c := &daemonClient{
		hc:      newDaemonHTTPClient(srv.Client().Transport, 0, ""),
		baseURL: srv.URL,
		teamDir: t.TempDir(),
	}

	if err := c.StartInstanceWithOptions("mgr", true, true); err != nil {
		t.Fatalf("start fresh: %v", err)
	}
	if payload.Instance != "mgr" || !payload.Fresh || !payload.Force {
		t.Fatalf("payload = %+v, want forced fresh start for mgr", payload)
	}
}

func TestClient_StopInstanceWithOptions(t *testing.T) {
	root := t.TempDir()
	m := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, time.Second))
	c, cleanup := newTestClient(t, daemon.Handler(m, nil, nil, ""))
	defer cleanup()

	_, err := c.Dispatch(dispatchPayload{Agent: "manager", Name: "mgr", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.StopInstanceWithOptions("mgr", true, 50*time.Millisecond); err != nil {
		t.Fatalf("force stop: %v", err)
	}
	if err := m.WaitForReaper("mgr", 10*time.Second); err != nil {
		t.Fatalf("wait stopped: %v", err)
	}
}

func TestClient_RestartInstance(t *testing.T) {
	claudeConfigDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfigDir)
	root := t.TempDir()
	m := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, time.Second))
	c, cleanup := newTestClient(t, daemon.Handler(m, nil, nil, ""))
	defer cleanup()

	workspace := t.TempDir()
	disp, err := c.Dispatch(dispatchPayload{Agent: "manager", Name: "mgr", Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	writeClientClaudeSession(t, claudeConfigDir, workspace, disp.SessionID)
	if err := c.RestartInstance("mgr"); err != nil {
		t.Fatalf("restart: %v", err)
	}
	stopAndWaitForTest(t, m, "mgr")
}

func TestClient_RestartInstanceWithOptions(t *testing.T) {
	claudeConfigDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfigDir)
	root := t.TempDir()
	m := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, time.Second))
	c, cleanup := newTestClient(t, daemon.Handler(m, nil, nil, ""))
	defer cleanup()

	workspace := t.TempDir()
	disp, err := c.Dispatch(dispatchPayload{Agent: "manager", Name: "mgr", Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	writeClientClaudeSession(t, claudeConfigDir, workspace, disp.SessionID)
	if err := c.RestartInstanceWithOptions("mgr", false, time.Second); err != nil {
		t.Fatalf("restart with options: %v", err)
	}
	stopAndWaitForTest(t, m, "mgr")
}

func TestClient_RestartInstanceWithOptionsSendsForceAndTimeout(t *testing.T) {
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/restart" {
			t.Fatalf("path = %s, want /v1/restart", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"restarted":true}`))
	}))
	defer srv.Close()
	c := &daemonClient{hc: srv.Client(), baseURL: srv.URL}

	if err := c.RestartInstanceWithOptions("mgr", true, 2*time.Second); err != nil {
		t.Fatalf("restart with force options: %v", err)
	}
	if payload["instance"] != "mgr" || payload["force"] != true || payload["timeout_ms"] != float64(2000) {
		t.Fatalf("payload = %+v, want instance/force/timeout_ms", payload)
	}
}

func TestClient_RemoveInstance(t *testing.T) {
	root := t.TempDir()
	m := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, time.Second))
	c, cleanup := newTestClient(t, daemon.Handler(m, nil, nil, ""))
	defer cleanup()

	_, err := c.Dispatch(dispatchPayload{Agent: "manager", Name: "mgr", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveInstance("mgr", true); err != nil {
		t.Fatalf("remove: %v", err)
	}
	insts, err := c.Instances()
	if err != nil {
		t.Fatalf("instances: %v", err)
	}
	if len(insts) != 0 {
		t.Fatalf("instances after remove = %+v, want empty", insts)
	}
}

func TestClient_LogsStream_NotFound(t *testing.T) {
	root := t.TempDir()
	m := daemon.NewInstanceManager(root, nil)
	c, cleanup := newTestClient(t, daemon.Handler(m, nil, nil, ""))
	defer cleanup()

	var buf bytes.Buffer
	err := c.LogsStream(context.Background(), &buf, "missing", false, 0)
	if err == nil || !strings.Contains(err.Error(), "no log") {
		t.Errorf("err: %v", err)
	}
}

func TestClient_LogsStream_NonFollow(t *testing.T) {
	root := t.TempDir()
	m := daemon.NewInstanceManager(root, nil)
	c, cleanup := newTestClient(t, daemon.Handler(m, nil, nil, ""))
	defer cleanup()

	// Seed a child.log file.
	writeChildLogForTest(t, root, "w", "alpha\nbeta\n")

	var buf bytes.Buffer
	if err := c.LogsStream(context.Background(), &buf, "w", false, 0); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := buf.String(); got != "alpha\nbeta\n" {
		t.Errorf("body: got %q", got)
	}

	buf.Reset()
	if err := c.LogsStream(context.Background(), &buf, "w", false, 1); err != nil {
		t.Fatalf("tail stream: %v", err)
	}
	if got := buf.String(); got != "beta\n" {
		t.Errorf("tail body: got %q", got)
	}
}
