package daemonclient

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/origin"
)

func TestNewDiscoveryPrecedence(t *testing.T) {
	teamDir := t.TempDir()
	var envHits, persistedHits atomic.Int32
	var envAuth, persistedAuth string
	envServer := statusServer(t, &envHits, &envAuth)
	persistedServer := statusServer(t, &persistedHits, &persistedAuth)

	operatorToken := daemon.OperatorTokenPath(teamDir)
	writeFile(t, operatorToken, "operator-token\n", 0o600)
	envToken := filepath.Join(t.TempDir(), "env.token")
	writeFile(t, envToken, "env-token\n", 0o600)
	writeFile(t, daemon.HTTPAddrPath(teamDir), strings.TrimPrefix(persistedServer.URL, "http://")+"\n", 0o600)

	t.Setenv("AGENT_TEAM_DAEMON_URL", envServer.URL+"/")
	t.Setenv(daemon.DaemonTokenFileEnv, envToken)
	client, err := New(teamDir, Options{})
	if err != nil {
		t.Fatalf("New with environment URL: %v", err)
	}
	if _, err := client.Status(); err != nil {
		t.Fatalf("environment status: %v", err)
	}
	if got := client.Connection(); got.Kind != TransportHTTP || got.Endpoint != envServer.URL || got.TokenFile != envToken {
		t.Fatalf("environment connection = %+v", got)
	}
	if envHits.Load() != 1 || persistedHits.Load() != 0 {
		t.Fatalf("hits after environment discovery = env %d persisted %d", envHits.Load(), persistedHits.Load())
	}
	if envAuth != "Bearer env-token" {
		t.Fatalf("environment Authorization = %q", envAuth)
	}

	unixHits := new(atomic.Int32)
	stopUnix := startUnixStatusServer(t, daemon.SocketPath(teamDir), unixHits)
	defer stopUnix()
	writeFile(t, daemon.PidPath(teamDir), "12345\n", 0o600)
	restorePIDCheck := daemon.SetPidLiveCheckForTest(func(pid int) bool { return pid == 12345 })
	defer restorePIDCheck()

	t.Setenv("AGENT_TEAM_DAEMON_URL", "")
	t.Setenv(daemon.DaemonTokenFileEnv, "")
	client, err = New(teamDir, Options{})
	if err != nil {
		t.Fatalf("New with persisted address: %v", err)
	}
	if _, err := client.Status(); err != nil {
		t.Fatalf("persisted status: %v", err)
	}
	if got := client.Connection(); got.Kind != TransportHTTP || got.Endpoint != persistedServer.URL || got.TokenFile != operatorToken {
		t.Fatalf("persisted connection = %+v", got)
	}
	if persistedHits.Load() != 1 || unixHits.Load() != 0 {
		t.Fatalf("hits after persisted discovery = persisted %d unix %d", persistedHits.Load(), unixHits.Load())
	}
	if persistedAuth != "Bearer operator-token" {
		t.Fatalf("persisted Authorization = %q", persistedAuth)
	}

	if err := os.Remove(daemon.HTTPAddrPath(teamDir)); err != nil {
		t.Fatal(err)
	}

	client, err = New(teamDir, Options{})
	if err != nil {
		t.Fatalf("New with Unix fallback: %v", err)
	}
	if _, err := client.Status(); err != nil {
		t.Fatalf("Unix status: %v", err)
	}
	if got := client.Connection(); got.Kind != TransportUnix || got.Endpoint != daemon.SocketPath(teamDir) || got.TokenFile != "" {
		t.Fatalf("Unix connection = %+v", got)
	}
	if unixHits.Load() != 1 {
		t.Fatalf("Unix hits = %d, want 1", unixHits.Load())
	}
}

func TestNewFallsBackFromUnreachableHTTPToConfiguredUnixSocket(t *testing.T) {
	teamDir := t.TempDir()
	socketDir, err := os.MkdirTemp("/tmp", "agent-team-daemonclient-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socket := filepath.Join(socketDir, "daemon.sock")

	var hits atomic.Int32
	var got struct {
		To      string `json:"to"`
		From    string `json:"from"`
		Body    string `json:"body"`
		ReplyTo string `json:"reply_to"`
	}
	stopUnix := startUnixHTTPServer(t, socket, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/v1/message" {
			t.Errorf("path = %q, want /v1/message", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode message: %v", err)
		}
		writeJSON(t, w, map[string]any{"delivered": true, "id": "msg-fallback"})
	}))
	defer stopUnix()

	tokenFile := filepath.Join(t.TempDir(), "daemon.token")
	writeFile(t, tokenFile, "instance-token\n", 0o600)
	t.Setenv("AGENT_TEAM_DAEMON_URL", unreachableLoopbackURL(t))
	t.Setenv("AGENT_TEAM_DAEMON_SOCKET", socket)
	t.Setenv(daemon.DaemonTokenFileEnv, tokenFile)

	client, err := New(teamDir, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := client.SendMessage("manager", "worker-gh391", "fallback body", "manager")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if !res.Delivered || res.ID != "msg-fallback" {
		t.Fatalf("response = %+v", res)
	}
	if hits.Load() != 1 {
		t.Fatalf("Unix socket hits = %d, want 1", hits.Load())
	}
	if got.To != "manager" || got.From != "worker-gh391" || got.Body != "fallback body" || got.ReplyTo != "manager" {
		t.Fatalf("Unix socket payload = %+v", got)
	}
}

func TestNewDoesNotFallbackFromHTTPResponseFailures(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			teamDir := t.TempDir()
			var unixHits atomic.Int32
			stopUnix := startUnixHTTPServer(t, daemon.SocketPath(teamDir), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				unixHits.Add(1)
				writeJSON(t, w, map[string]any{"delivered": true, "id": "must-not-deliver"})
			}))
			defer stopUnix()

			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, http.StatusText(status), status)
			}))
			defer httpServer.Close()
			tokenFile := filepath.Join(t.TempDir(), "daemon.token")
			writeFile(t, tokenFile, "instance-token\n", 0o600)
			t.Setenv("AGENT_TEAM_DAEMON_URL", httpServer.URL)
			t.Setenv("AGENT_TEAM_DAEMON_SOCKET", daemon.SocketPath(teamDir))
			t.Setenv(daemon.DaemonTokenFileEnv, tokenFile)

			client, err := New(teamDir, Options{})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = client.SendMessage("manager", "worker-gh391", "must stay failed", "")
			var responseErr *ResponseError
			if !errors.As(err, &responseErr) || responseErr.StatusCode != status {
				t.Fatalf("SendMessage error = %T %v, want HTTP %d ResponseError", err, err, status)
			}
			if unixHits.Load() != 0 {
				t.Fatalf("Unix socket hits = %d, want 0", unixHits.Load())
			}
		})
	}

	t.Run("missing token file", func(t *testing.T) {
		teamDir := t.TempDir()
		var unixHits atomic.Int32
		stopUnix := startUnixHTTPServer(t, daemon.SocketPath(teamDir), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			unixHits.Add(1)
			writeJSON(t, w, map[string]any{"delivered": true, "id": "must-not-deliver"})
		}))
		defer stopUnix()
		t.Setenv("AGENT_TEAM_DAEMON_URL", unreachableLoopbackURL(t))
		t.Setenv("AGENT_TEAM_DAEMON_SOCKET", daemon.SocketPath(teamDir))
		t.Setenv(daemon.DaemonTokenFileEnv, filepath.Join(t.TempDir(), "missing.token"))

		client, err := New(teamDir, Options{})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = client.SendMessage("manager", "worker-gh391", "must stay failed", "")
		if err == nil || !strings.Contains(err.Error(), "read token file") {
			t.Fatalf("SendMessage error = %v, want token-file error", err)
		}
		if unixHits.Load() != 0 {
			t.Fatalf("Unix socket hits = %d, want 0", unixHits.Load())
		}
	})
}

func TestCloseIdleConnectionsForwardsToUnderlyingTransport(t *testing.T) {
	transport := new(closeTrackingRoundTripper)
	client := NewHTTP("http://daemon", "", Options{RoundTripper: transport})

	client.CloseIdleConnections()

	if got := transport.closeCalls.Load(); got != 1 {
		t.Fatalf("underlying CloseIdleConnections calls = %d, want 1", got)
	}
}

func TestNewFailsClosedForStalePidfile(t *testing.T) {
	teamDir := t.TempDir()
	t.Setenv("AGENT_TEAM_DAEMON_URL", "")
	writeFile(t, daemon.PidPath(teamDir), "99999\n", 0o600)
	restorePIDCheck := daemon.SetPidLiveCheckForTest(func(int) bool { return false })
	defer restorePIDCheck()

	_, err := New(teamDir, Options{})
	if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("New error = %v, want ErrNotRunning", err)
	}
}

func TestHTTPAuthenticationAndAttributionHeaders(t *testing.T) {
	var authorization, buildHeader, originHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		buildHeader = r.Header.Get(buildinfo.HeaderName)
		originHeader = r.Header.Get(origin.HeaderName)
		writeJSON(t, w, map[string]any{"ready": true, "instances": 0})
	}))
	defer srv.Close()

	tokenFile := filepath.Join(t.TempDir(), "operator.token")
	writeFile(t, tokenFile, "secret-token\n", 0o600)
	t.Setenv("AGENT_TEAM_PROJECT", "project-1")
	t.Setenv("AGENT_TEAM_TEAM", "")
	t.Setenv("AGENT_TEAM_INSTANCE", "frontend-worker")
	t.Setenv("AGENT_TEAM_ORIGIN_INSTANCE", "")
	t.Setenv("AGENT_TEAM_ORIGIN_AGENT", "")
	t.Setenv("AGENT_TEAM_JOB_ID", "gh385-daemon-client")
	t.Setenv("AGENT_TEAM_ORIGIN_JOB", "")
	t.Setenv("AGENT_TEAM_ORIGIN_TRIGGER", "")
	t.Setenv("AGENT_TEAM_ORIGIN_BUILD", "")
	build := buildinfo.Info{Version: "v-test", Revision: "0123456789abcdef"}
	client := NewHTTP(srv.URL, tokenFile, Options{Build: build, RoundTripper: srv.Client().Transport})

	if _, err := client.Status(); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if authorization != "Bearer secret-token" {
		t.Fatalf("Authorization = %q", authorization)
	}
	parsedBuild, err := buildinfo.ParseHeaderValue(buildHeader)
	if err != nil || !buildinfo.Equivalent(parsedBuild, build) {
		t.Fatalf("build header = %q, parsed=%+v err=%v", buildHeader, parsedBuild, err)
	}
	parsedOrigin, err := origin.ParseHeaderValue(originHeader)
	if err != nil {
		t.Fatalf("parse origin header: %v", err)
	}
	if parsedOrigin.Project != "project-1" || parsedOrigin.Instance != "frontend-worker" || parsedOrigin.Job != "gh385-daemon-client" || parsedOrigin.Build != build.Display() {
		t.Fatalf("origin header = %+v", parsedOrigin)
	}
}

func TestHTTPTimeoutAndResponseErrorMapping(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(50 * time.Millisecond)
			writeJSON(t, w, map[string]any{"ready": true})
		}))
		defer srv.Close()
		client := NewHTTP(srv.URL, "", Options{Timeout: 5 * time.Millisecond, RoundTripper: srv.Client().Transport})
		if _, err := client.Status(); err == nil || !strings.Contains(err.Error(), "daemon: status:") {
			t.Fatalf("timeout error = %v", err)
		}
	})

	t.Run("status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "token required", http.StatusUnauthorized)
		}))
		defer srv.Close()
		client := NewHTTP(srv.URL, "", Options{RoundTripper: srv.Client().Transport})
		_, err := client.Instances()
		var responseErr *ResponseError
		if !errors.As(err, &responseErr) {
			t.Fatalf("error = %T %v, want ResponseError", err, err)
		}
		if responseErr.Operation != "instances" || responseErr.StatusCode != http.StatusUnauthorized || responseErr.Body != "token required" {
			t.Fatalf("ResponseError = %+v", responseErr)
		}
		if got := err.Error(); got != "daemon: instances: token required" {
			t.Fatalf("Error() = %q", got)
		}
	})
}

func TestTypedDashboardParityResponses(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/instances", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, []map[string]any{{
			"instance": "worker-1", "agent": "worker", "status": "running", "job": "gh385",
			"uri": "agt://project/instance/worker-1", "state_uri": "agt://project/state/worker-1",
		}})
	})
	mux.HandleFunc("/v1/jobs", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, []map[string]any{{
			"id": "gh385", "ticket": "GH-385", "target": "worker", "status": "running",
			"pipeline": "frontend_ticket_to_pr", "instance": "worker-1",
			"uri": "agt://project/job/gh385", "outcome_uri": "agt://project/outcome/gh385",
			"created_at": "2026-07-10T12:00:00Z", "updated_at": "2026-07-10T12:05:00Z",
		}})
	})
	mux.HandleFunc("/v1/topology", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"instances": []map[string]any{{"name": "worker", "agent": "worker", "ephemeral": true, "replicas": 2, "config": map[string]any{}, "triggers": []map[string]any{{"event": "agent.dispatch", "match": map[string]any{"target": "worker"}}}, "running": 1, "queued": 1}},
			"pipelines": []map[string]any{{"name": "frontend_ticket_to_pr", "trigger": map[string]any{"event": "agent.dispatch", "match": map[string]any{}}, "steps": []map[string]any{{"id": "implement", "target": "worker", "after": []string{}, "token_budget": 60000000, "time_budget": "1h0m0s"}}, "auto_advance": true, "reap_worktree": "on_merge"}},
			"schedules": []any{}, "teams": []any{}, "budgets": []any{},
		})
	})
	mux.HandleFunc("/v1/resources", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("uri"); got != "agt://project/job/gh385#step=implement" {
			t.Fatalf("resource URI = %q", got)
		}
		writeJSON(t, w, map[string]any{"uri": r.URL.Query().Get("uri"), "kind": "step", "id": "implement", "fragment": "step=implement", "data": map[string]any{"status": "running"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := NewHTTP(srv.URL, "", Options{RoundTripper: srv.Client().Transport})

	instances, err := client.Instances()
	if err != nil || len(instances) != 1 || instances[0].Instance != "worker-1" || instances[0].Status != "running" || instances[0].StateURI == "" {
		t.Fatalf("Instances = %+v, err=%v", instances, err)
	}
	jobs, err := client.Jobs()
	if err != nil || len(jobs) != 1 || jobs[0].ID != "gh385" || jobs[0].Pipeline != "frontend_ticket_to_pr" || jobs[0].OutcomeURI == "" {
		t.Fatalf("Jobs = %+v, err=%v", jobs, err)
	}
	topology, err := client.Topology()
	if err != nil || len(topology.Instances) != 1 || topology.Instances[0].Running != 1 || len(topology.Pipelines) != 1 || topology.Pipelines[0].Steps[0].TokenBudget != 60000000 {
		t.Fatalf("Topology = %+v, err=%v", topology, err)
	}
	resource, err := client.Resource("agt://project/job/gh385#step=implement")
	if err != nil || resource.Kind != "step" || resource.Fragment != "step=implement" {
		t.Fatalf("Resource = %+v, err=%v", resource, err)
	}
	var resourceData map[string]any
	if err := json.Unmarshal(resource.Data, &resourceData); err != nil || resourceData["status"] != "running" {
		t.Fatalf("resource data = %+v, err=%v", resourceData, err)
	}
}

func statusServer(t *testing.T, hits *atomic.Int32, auth *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if auth != nil {
			*auth = r.Header.Get("Authorization")
		}
		writeJSON(t, w, map[string]any{"ready": true, "instances": 0})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func startUnixStatusServer(t *testing.T, socket string, hits *atomic.Int32) func() {
	t.Helper()
	return startUnixHTTPServer(t, socket, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		writeJSON(t, w, map[string]any{"ready": true, "instances": 0})
	}))
}

func startUnixHTTPServer(t *testing.T, socket string, handler http.Handler) func() {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	return func() {
		_ = server.Close()
		_ = listener.Close()
	}
}

func unreachableLoopbackURL(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return "http://" + addr
}

func writeFile(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

type closeTrackingRoundTripper struct {
	closeCalls atomic.Int32
}

func (*closeTrackingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("unexpected RoundTrip")
}

func (t *closeTrackingRoundTripper) CloseIdleConnections() {
	t.closeCalls.Add(1)
}
