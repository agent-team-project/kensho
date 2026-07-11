package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/daemonclient"
)

func TestCommandRuntimeJobsFailureRetainsResourceEnrichment(t *testing.T) {
	teamDir := newCommandRuntimeTeamDir(t)

	const (
		instanceURI = "agt://dep/instance/worker"
		jobURI      = "agt://dep/job/job-1"
		outcomeURI  = "agt://dep/outcome/job-1"
	)
	var jobsFailed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer seed-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/instances":
			writeTUIJSON(t, w, []map[string]any{{"instance": "worker", "agent": "worker", "status": "running", "uri": instanceURI}})
		case "/v1/jobs":
			if jobsFailed.Load() {
				http.Error(w, "jobs temporarily unavailable", http.StatusServiceUnavailable)
				return
			}
			writeTUIJSON(t, w, []map[string]any{{
				"id": "job-1", "ticket": "GH-1", "target": "worker", "status": "running", "uri": jobURI, "outcome_uri": outcomeURI,
				"created_at": fixtureTime.Add(-time.Hour), "updated_at": fixtureTime,
			}})
		case "/v1/topology":
			writeTUIJSON(t, w, map[string]any{"instances": []any{}, "pipelines": []any{}, "schedules": []any{}, "teams": []any{}, "budgets": []any{}})
		case "/v1/resources":
			uri := r.URL.Query().Get("uri")
			data := map[string]any{"generation": 1}
			if uri == outcomeURI {
				data = map[string]any{"model": "gpt-canonical", "tier": "T2", "bounce_classes": map[string]any{"capability": 1}}
			} else if jobsFailed.Load() {
				data["generation"] = 2
			}
			writeTUIJSON(t, w, map[string]any{"uri": uri, "kind": "fixture", "id": uri, "data": data})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("AGENT_TEAM_DAEMON_URL", server.URL)
	t.Setenv("AGENT_TEAM_DAEMON_TOKEN_FILE", "")

	clockAt := fixtureTime
	runtime := &commandRuntime{ctx: context.Background(), teamDir: teamDir, clock: func() time.Time { return clockAt }}
	model := NewModel(clockAt, Capabilities{Dumb: true})
	model.Booted = true
	model.RefreshInFlight = true
	model = applyRefreshBatch(model, runtime.load(false))
	if model.Connection != ConnectionConnected || model.Snapshot.Resources[outcomeURI] == nil {
		t.Fatalf("initial snapshot connection=%s resources=%v", model.Connection, model.Snapshot.Resources)
	}

	jobsFailed.Store(true)
	clockAt = fixtureTime.Add(time.Minute)
	model, _ = Update(model, RefreshStarted{At: clockAt})
	model = applyRefreshBatch(model, runtime.load(false))
	retained := resourceMap(model.Snapshot.Resources[outcomeURI])
	if retained["model"] != "gpt-canonical" || distinctBounceClasses(model.Snapshot) != 1 {
		t.Fatalf("failed URI enrichment was not retained: data=%v summary=%+v", retained, projectOverview(model).Summary)
	}
	state := model.Sources[daemonclient.SourceResources]
	if !state.FetchedAt.Equal(fixtureTime) || !strings.Contains(state.Error, "jobs failed") || !strings.Contains(state.Error, "jobs temporarily unavailable") {
		t.Fatalf("resource freshness = %+v, want retained first timestamp plus current URI error", state)
	}
	if frame := Render(model); !strings.Contains(frame, "RESOURCES retained 12:04:05 ERROR:") || !strings.Contains(frame, "resource discovery") {
		t.Fatalf("mixed resource staleness is not explicit:\n%s", frame)
	}
}

func TestCommandRuntimeMixedResourceRefreshDropsNoLongerRequestedURI(t *testing.T) {
	teamDir := newCommandRuntimeTeamDir(t)
	const (
		instanceURI = "agt://dep/instance/worker"
		job1URI     = "agt://dep/job/job-1"
		outcome1URI = "agt://dep/outcome/job-1"
		job2URI     = "agt://dep/job/job-2"
		outcome2URI = "agt://dep/outcome/job-2"
	)
	var mixed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer seed-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/instances":
			writeTUIJSON(t, w, []map[string]any{{"instance": "worker", "agent": "worker", "status": "running", "uri": instanceURI}})
		case "/v1/jobs":
			jobs := []map[string]any{{
				"id": "job-1", "ticket": "GH-1", "target": "worker", "status": "running", "uri": job1URI, "outcome_uri": outcome1URI,
				"created_at": fixtureTime.Add(-time.Hour), "updated_at": fixtureTime,
			}}
			if !mixed.Load() {
				jobs = append(jobs, map[string]any{
					"id": "job-2", "ticket": "GH-2", "target": "worker", "status": "done", "uri": job2URI, "outcome_uri": outcome2URI,
					"created_at": fixtureTime.Add(-time.Hour), "updated_at": fixtureTime.Add(-time.Minute),
				})
			}
			writeTUIJSON(t, w, jobs)
		case "/v1/topology":
			writeTUIJSON(t, w, map[string]any{"instances": []any{}, "pipelines": []any{}, "schedules": []any{}, "teams": []any{}, "budgets": []any{}})
		case "/v1/resources":
			uri := r.URL.Query().Get("uri")
			if mixed.Load() && uri == outcome1URI {
				http.Error(w, "outcome temporarily unavailable", http.StatusServiceUnavailable)
				return
			}
			data := map[string]any{"generation": 1}
			if uri == outcome1URI {
				data = map[string]any{"model": "gpt-canonical", "tier": "T2"}
			}
			writeTUIJSON(t, w, map[string]any{"uri": uri, "kind": "fixture", "id": uri, "data": data})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("AGENT_TEAM_DAEMON_URL", server.URL)
	t.Setenv("AGENT_TEAM_DAEMON_TOKEN_FILE", "")

	clockAt := fixtureTime
	runtime := &commandRuntime{ctx: context.Background(), teamDir: teamDir, clock: func() time.Time { return clockAt }}
	model := NewModel(clockAt, Capabilities{Dumb: true})
	model.Booted = true
	model.RefreshInFlight = true
	model = applyRefreshBatch(model, runtime.load(false))
	if len(model.Snapshot.Resources) != 5 {
		t.Fatalf("initial resources = %d, want 5", len(model.Snapshot.Resources))
	}

	mixed.Store(true)
	clockAt = fixtureTime.Add(time.Minute)
	model, _ = Update(model, RefreshStarted{At: clockAt})
	model = applyRefreshBatch(model, runtime.load(false))
	if retained := resourceMap(model.Snapshot.Resources[outcome1URI]); retained["model"] != "gpt-canonical" {
		t.Fatalf("failed current URI was not retained: %v", retained)
	}
	if model.Snapshot.Resources[job2URI] != nil || model.Snapshot.Resources[outcome2URI] != nil {
		t.Fatalf("removed job resources survived current-set merge: %v", model.Snapshot.Resources)
	}
	if len(model.Snapshot.Resources) != 3 || model.Snapshot.ResourcesRequested != 3 {
		t.Fatalf("current resources = %d/%d, want 3/3", len(model.Snapshot.Resources), model.Snapshot.ResourcesRequested)
	}
	state := model.Sources[daemonclient.SourceResources]
	if !state.FetchedAt.Equal(fixtureTime) || !strings.Contains(state.Error, outcome1URI) {
		t.Fatalf("resource freshness = %+v", state)
	}
}

func newCommandRuntimeTeamDir(t *testing.T) string {
	t.Helper()
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte("[project]\nid = \"dep\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(daemon.OperatorTokenPath(teamDir)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon.OperatorTokenPath(teamDir), []byte("seed-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return teamDir
}

func applyRefreshBatch(model Model, batch refreshBatch) Model {
	for _, message := range batch.messages {
		model, _ = Update(model, message)
	}
	return model
}

func writeTUIJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}
