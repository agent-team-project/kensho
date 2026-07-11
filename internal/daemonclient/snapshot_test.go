package daemonclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSnapshotCollectsCollectionsAndResourceEnrichment(t *testing.T) {
	resourceHits := map[string]int{}
	var resourceHitsMu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/instances", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, []map[string]any{{
			"instance": "worker-1", "agent": "worker", "status": "running",
			"uri": "agt://dep/instance/worker-1", "state_uri": "agt://dep/state/worker-1",
		}})
	})
	mux.HandleFunc("/v1/jobs", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, []map[string]any{{
			"id": "job-1", "ticket": "GH-1", "target": "worker", "status": "running",
			"uri": "agt://dep/job/job-1", "outcome_uri": "agt://dep/outcome/job-1",
			"created_at": "2026-07-10T12:00:00Z", "updated_at": "2026-07-10T12:01:00Z",
		}})
	})
	mux.HandleFunc("/v1/topology", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{"instances": []any{}, "pipelines": []any{}, "schedules": []any{}, "teams": []any{}, "budgets": []any{}})
	})
	mux.HandleFunc("/v1/resources", func(w http.ResponseWriter, r *http.Request) {
		uri := r.URL.Query().Get("uri")
		resourceHitsMu.Lock()
		resourceHits[uri]++
		resourceHitsMu.Unlock()
		writeJSON(t, w, map[string]any{"uri": uri, "kind": "test", "id": uri, "data": map[string]any{"ok": true}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := NewHTTP(server.URL, "", Options{RoundTripper: server.Client().Transport})
	at := time.Date(2026, 7, 10, 12, 4, 5, 0, time.UTC)

	snapshot := client.Snapshot(context.Background(), at)
	if !snapshot.Complete() {
		t.Fatalf("snapshot incomplete: %+v", snapshot.SourceErrors)
	}
	if len(snapshot.Instances) != 1 || len(snapshot.Jobs) != 1 || snapshot.Topology == nil {
		t.Fatalf("snapshot collections = %+v", snapshot)
	}
	resourceHitsMu.Lock()
	resourceHitCount := len(resourceHits)
	resourceHitsMu.Unlock()
	if resourceHitCount != 4 || len(snapshot.Resources) != 4 {
		t.Fatalf("resource hits=%v snapshot=%d", resourceHits, len(snapshot.Resources))
	}
	for _, source := range SnapshotSources() {
		if got := snapshot.SourceTimes[source]; !got.Equal(at) {
			t.Fatalf("source %s time = %v", source, got)
		}
	}
}

func TestSnapshotPreservesPartialResourceResults(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/instances", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, []map[string]any{{"instance": "worker", "agent": "worker", "status": "running", "uri": "agt://dep/instance/worker", "state_uri": "agt://dep/state/worker"}})
	})
	mux.HandleFunc("/v1/jobs", func(w http.ResponseWriter, _ *http.Request) { writeJSON(t, w, []any{}) })
	mux.HandleFunc("/v1/topology", func(w http.ResponseWriter, _ *http.Request) { writeJSON(t, w, map[string]any{}) })
	mux.HandleFunc("/v1/resources", func(w http.ResponseWriter, r *http.Request) {
		uri := r.URL.Query().Get("uri")
		if strings.Contains(uri, "/state/") {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(t, w, map[string]any{"uri": uri, "kind": "instance", "id": "worker", "data": map[string]any{}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := NewHTTP(server.URL, "", Options{RoundTripper: server.Client().Transport})

	snapshot := client.Snapshot(context.Background(), time.Now())
	if snapshot.Complete() || !snapshot.Usable() {
		t.Fatalf("complete=%v usable=%v errors=%v", snapshot.Complete(), snapshot.Usable(), snapshot.SourceErrors)
	}
	if len(snapshot.Resources) != 1 || snapshot.SourceErrors[SourceResources] == "" {
		t.Fatalf("resources=%d errors=%v", len(snapshot.Resources), snapshot.SourceErrors)
	}
}

func TestSnapshotAllCollectionFailuresDoNotClaimResourceSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "transport unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client := NewHTTP(server.URL, "", Options{RoundTripper: server.Client().Transport})

	snapshot := client.Snapshot(context.Background(), time.Now())
	if snapshot.Usable() || snapshot.SourceErrors[SourceResources] == "" || !snapshot.SourceTimes[SourceResources].IsZero() {
		t.Fatalf("all-failed snapshot usable=%v resource_time=%v errors=%v", snapshot.Usable(), snapshot.SourceTimes[SourceResources], snapshot.SourceErrors)
	}
}

func TestSnapshotCollectionFailureMarksResourceDiscoveryPartial(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/instances", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, []map[string]any{{"instance": "worker", "agent": "worker", "status": "running", "uri": "agt://dep/instance/worker"}})
	})
	mux.HandleFunc("/v1/jobs", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "jobs unavailable", http.StatusServiceUnavailable)
	})
	mux.HandleFunc("/v1/topology", func(w http.ResponseWriter, _ *http.Request) { writeJSON(t, w, map[string]any{}) })
	mux.HandleFunc("/v1/resources", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"uri": r.URL.Query().Get("uri"), "kind": "instance", "id": "worker", "data": map[string]any{}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := NewHTTP(server.URL, "", Options{RoundTripper: server.Client().Transport})
	at := time.Date(2026, 7, 10, 12, 4, 5, 0, time.UTC)

	snapshot := client.Snapshot(context.Background(), at)
	if snapshot.SourceErrors[SourceJobs] == "" || snapshot.SourceErrors[SourceResources] == "" {
		t.Fatalf("partial discovery errors = %v", snapshot.SourceErrors)
	}
	if !strings.Contains(snapshot.SourceErrors[SourceResources], "jobs failed") || !snapshot.SourceTimes[SourceResources].IsZero() {
		t.Fatalf("resource discovery state = time %v error %q", snapshot.SourceTimes[SourceResources], snapshot.SourceErrors[SourceResources])
	}
	if len(snapshot.Resources) != 1 || snapshot.ResourcesRequested != 1 {
		t.Fatalf("successful resource subset = %d/%d", len(snapshot.Resources), snapshot.ResourcesRequested)
	}
}

func TestSnapshotCacheIdentityPermissionsAndInvalidation(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(teamDir, "config.toml"), "[project]\nid = \"dep-a\"\n", 0o644)
	at := time.Date(2026, 7, 10, 12, 4, 5, 0, time.UTC)
	snapshot := &Snapshot{
		Schema: SnapshotSchema, CapturedAt: at, Instances: []*Instance{}, Jobs: []*Job{}, Topology: &Topology{}, Resources: map[string]*Resource{},
		SourceTimes:  map[SnapshotSource]time.Time{SourceInstances: at, SourceJobs: at, SourceTopology: at, SourceResources: at},
		SourceErrors: map[SnapshotSource]string{}, Connection: Connection{Kind: TransportHTTP, Endpoint: "http://127.0.0.1:1", TokenFile: "/secret"},
	}
	if err := SaveSnapshotCache(teamDir, snapshot); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(SnapshotCachePath(teamDir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("cache mode = %o", info.Mode().Perm())
	}
	loaded, err := LoadSnapshotCache(teamDir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DeploymentID != "dep-a" || loaded.Connection.Endpoint != "" || !loaded.CapturedAt.Equal(at) {
		t.Fatalf("loaded cache = %+v", loaded)
	}
	body, err := os.ReadFile(SnapshotCachePath(teamDir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "/secret") || strings.Contains(string(body), "127.0.0.1") {
		t.Fatalf("cache leaked connection data: %s", body)
	}
	writeFile(t, filepath.Join(teamDir, "config.toml"), "[project]\nid = \"dep-b\"\n", 0o644)
	if _, err := LoadSnapshotCache(teamDir); err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("identity mismatch error = %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	raw["schema"] = "future"
	changed, _ := json.Marshal(raw)
	if err := os.WriteFile(SnapshotCachePath(teamDir), changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSnapshotCache(teamDir); err == nil {
		t.Fatal("schema mismatch accepted")
	}
}

func TestSaveSnapshotCacheRejectsPartialSnapshot(t *testing.T) {
	err := SaveSnapshotCache(t.TempDir(), &Snapshot{SourceErrors: map[SnapshotSource]string{SourceJobs: "down"}})
	if err == nil {
		t.Fatal("partial snapshot was cached")
	}
}
