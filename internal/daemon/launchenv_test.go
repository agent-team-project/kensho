package daemon

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/buildinfo"
)

func TestLaunchEnvWriteReadRoundTripStripsDeniedKeys(t *testing.T) {
	root := t.TempDir()
	recordedAt := time.Now().UTC().Truncate(time.Second)
	le := &LaunchEnv{
		Bin:        "/tmp/agent-teamd",
		Args:       []string{"/tmp/agent-teamd", "--target", "/repo"},
		Dir:        "/repo",
		Env:        []string{"PATH=/bin", "OPENAI_API_KEY=must-not-persist", "OPENAI_API_KEY_EXTRA=keep", "TOKEN=value"},
		RecordedAt: recordedAt,
		PID:        1234,
		Version:    1,
		Build:      buildinfo.Info{Version: "0.1.0", Revision: "abc123def4567890"},
	}

	if err := WriteLaunchEnv(root, le); err != nil {
		t.Fatalf("WriteLaunchEnv: %v", err)
	}
	got, err := ReadLaunchEnv(root)
	if err != nil {
		t.Fatalf("ReadLaunchEnv: %v", err)
	}
	if got.Bin != le.Bin || got.Dir != le.Dir || got.PID != le.PID || got.Version != le.Version || !got.RecordedAt.Equal(recordedAt) {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, le)
	}
	if got.Build.ShortRevision() != "abc123def456" {
		t.Fatalf("build = %+v, want snapshot build", got.Build)
	}
	if envHasKey(got.Env, DefaultStrippedEnvKeys[0]) {
		t.Fatalf("denied key persisted in env: %+v", got.Env)
	}
	if !envHasKey(got.Env, "OPENAI_API_KEY_EXTRA") {
		t.Fatalf("exact-key strip removed prefix match: %+v", got.Env)
	}
	if !containsLaunchEnvString(got.Stripped, DefaultStrippedEnvKeys[0]) {
		t.Fatalf("stripped keys = %+v, want %s", got.Stripped, DefaultStrippedEnvKeys[0])
	}
	body, err := os.ReadFile(launchEnvPathForRoot(root))
	if err != nil {
		t.Fatalf("read raw snapshot: %v", err)
	}
	if strings.Contains(string(body), "must-not-persist") {
		t.Fatalf("denied value persisted in snapshot: %s", string(body))
	}
	st, err := os.Stat(launchEnvPathForRoot(root))
	if err != nil {
		t.Fatalf("stat snapshot: %v", err)
	}
	if got, want := st.Mode().Perm(), fs.FileMode(0o600); got != want {
		t.Fatalf("snapshot mode = %o, want %o", got, want)
	}
}

func TestReadLaunchEnvMissingIsDetectable(t *testing.T) {
	_, err := ReadLaunchEnv(t.TempDir())
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadLaunchEnv missing err = %v, want fs.ErrNotExist", err)
	}
}

func TestInstanceLaunchEnvWriteReadRoundTripStripsDeniedKeys(t *testing.T) {
	root := t.TempDir()
	recordedAt := time.Now().UTC().Truncate(time.Second)
	le := &LaunchEnv{
		Bin:        "claude",
		Args:       []string{"codex", "exec", "-c", `otel.exporter={ otlp-http = { endpoint = "http://collector", headers = { "authorization" = "header-secret" } } }`},
		Dir:        "/repo",
		Env:        []string{"PATH=/bin", "OPENAI_API_KEY=must-not-persist", "OPENAI_API_KEY_EXTRA=keep", "OTEL_EXPORTER_OTLP_HEADERS=authorization=header-secret", "AGENTTEAM_OTEL_HEADER_0=header-secret", "MARKER=dispatch"},
		RecordedAt: recordedAt,
		PID:        4321,
		Version:    1,
	}

	if err := WriteInstanceLaunchEnv(root, "manager", le); err != nil {
		t.Fatalf("WriteInstanceLaunchEnv: %v", err)
	}
	got, err := ReadInstanceLaunchEnv(root, "manager")
	if err != nil {
		t.Fatalf("ReadInstanceLaunchEnv: %v", err)
	}
	if got.Bin != le.Bin || got.Dir != le.Dir || got.PID != le.PID || !got.RecordedAt.Equal(recordedAt) {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, le)
	}
	if envHasKey(got.Env, DefaultStrippedEnvKeys[0]) {
		t.Fatalf("denied key persisted in env: %+v", got.Env)
	}
	if !envHasKey(got.Env, "OPENAI_API_KEY_EXTRA") || !envHasKey(got.Env, "MARKER") {
		t.Fatalf("allowed keys missing from env: %+v", got.Env)
	}
	if envHasKey(got.Env, "OTEL_EXPORTER_OTLP_HEADERS") {
		t.Fatalf("otel header key persisted in env: %+v", got.Env)
	}
	if envHasKey(got.Env, "AGENTTEAM_OTEL_HEADER_0") {
		t.Fatalf("generated otel header key persisted in env: %+v", got.Env)
	}
	if strings.Contains(strings.Join(got.Args, " "), "header-secret") {
		t.Fatalf("otel header secret persisted in args: %+v", got.Args)
	}
	body, err := os.ReadFile(InstanceLaunchEnvPath(root, "manager"))
	if err != nil {
		t.Fatalf("read raw instance snapshot: %v", err)
	}
	if strings.Contains(string(body), "must-not-persist") || strings.Contains(string(body), "header-secret") {
		t.Fatalf("denied value persisted in instance snapshot: %s", string(body))
	}
	st, err := os.Stat(InstanceLaunchEnvPath(root, "manager"))
	if err != nil {
		t.Fatalf("stat instance snapshot: %v", err)
	}
	if got, want := st.Mode().Perm(), fs.FileMode(0o600); got != want {
		t.Fatalf("instance snapshot mode = %o, want %o", got, want)
	}
}

func TestStripEnvRemovesOnlyExactKeys(t *testing.T) {
	got := stripEnv([]string{
		"KEY=value",
		"KEY_EXTRA=value",
		"key=value",
		"NOVALUE",
	}, []string{"KEY", "NOVALUE"})
	want := []string{"KEY_EXTRA=value", "key=value"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("stripEnv = %+v, want %+v", got, want)
	}
}

func TestMergeEnvOverlayWinsAndCollapsesDuplicateKeys(t *testing.T) {
	got := mergeEnv(
		[]string{"PATH=/old", "KEEP=base", "PATH=/snapshot", "NOVALUE"},
		[]string{"PATH=/runtime/bin:/snapshot", "KEEP=overlay", "NEW=value", "BROKEN"},
	)
	want := []string{"PATH=/runtime/bin:/snapshot", "KEEP=overlay", "NOVALUE", "NEW=value", "BROKEN"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("mergeEnv = %+v, want %+v", got, want)
	}
}

func TestLaunchEnvGitignoreGuard(t *testing.T) {
	root := repoRootForTest(t)
	body, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	ignore := string(body)
	if strings.Contains(ignore, ".agent_team/daemon/") {
		return
	}
	if strings.Contains(ignore, "launch-env.json") && strings.Contains(ignore, "launch-env.prev.json") {
		return
	}
	t.Fatalf(".gitignore must ignore .agent_team/daemon/ or both launch-env snapshots")
}

func envHasKey(env []string, key string) bool {
	for _, item := range env {
		gotKey, _, _ := strings.Cut(item, "=")
		if gotKey == key {
			return true
		}
	}
	return false
}

func containsLaunchEnvString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func repoRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find repo root from %s", dir)
		}
		dir = parent
	}
}
