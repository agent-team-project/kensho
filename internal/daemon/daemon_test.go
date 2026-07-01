package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// shortTempDir returns a tempdir under /tmp so unix-socket paths stay within
// macOS's 104-char limit. t.TempDir() lives under /var/folders/... which is
// often >100 chars and overflows for nested socket files.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "agt-")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestSocketPathFallsBackForLongTeamDir(t *testing.T) {
	root := filepath.Join("/private/var/folders", strings.Repeat("x", 96), "repo", ".agent_team")
	inRepo := filepath.Join(root, "daemon.sock")
	if len(inRepo) <= maxUnixSocketPathLen {
		t.Fatalf("test path is not long enough: %d", len(inRepo))
	}
	got := SocketPath(root)
	if got == inRepo || strings.Contains(got, root) {
		t.Fatalf("SocketPath(%q) = %q, want hashed fallback", root, got)
	}
	if len(got) > maxUnixSocketPathLen {
		t.Fatalf("fallback socket path len=%d path=%q, want <= %d", len(got), got, maxUnixSocketPathLen)
	}
	if !strings.HasSuffix(got, ".sock") {
		t.Fatalf("fallback socket path = %q, want .sock suffix", got)
	}
	if again := SocketPath(root); again != got {
		t.Fatalf("SocketPath not deterministic: %q then %q", got, again)
	}
}

func TestDaemonBootsWithLongTeamDir(t *testing.T) {
	teamDir := filepath.Join(shortTempDir(t), strings.Repeat("very-long-segment-", 8), ".agent_team")
	if len(filepath.Join(teamDir, "daemon.sock")) <= maxUnixSocketPathLen {
		t.Fatalf("test path is not long enough: %s", teamDir)
	}
	d := startDaemon(t, teamDir, newFakeSpawner(30*time.Second).spawn)
	defer d.Shutdown(context.Background())

	client := unixClient(SocketPath(teamDir))
	resp, err := client.Get("http://daemon/v1/instances")
	if err != nil {
		t.Fatalf("GET /v1/instances: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/instances status = %d", resp.StatusCode)
	}
}

func TestDaemonOptionalLoopbackHTTPListener(t *testing.T) {
	teamDir := shortTempDir(t)
	d, err := New(Config{
		TeamDir:         teamDir,
		LogOut:          io.Discard,
		HTTPAddr:        "127.0.0.1:0",
		SpawnerOverride: newFakeSpawner(30 * time.Second).spawn,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_ = d.Run(ctx)
	}()
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	deadline := time.Now().Add(5 * time.Second)
	var httpAddr string
	for time.Now().Before(deadline) {
		var err error
		httpAddr, err = ReadHTTPAddr(teamDir)
		if err == nil && httpAddr != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if httpAddr == "" {
		t.Fatalf("daemon HTTP address never appeared at %s", HTTPAddrPath(teamDir))
	}
	if d.HTTPAddr() != httpAddr {
		t.Fatalf("daemon HTTPAddr() = %q, want %q", d.HTTPAddr(), httpAddr)
	}
	resp, err := http.Get(DaemonHTTPURL(httpAddr) + "/v1/instances")
	if err != nil {
		t.Fatalf("GET /v1/instances over HTTP: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/instances status = %d", resp.StatusCode)
	}
}

func TestNormalizeLoopbackHTTPAddrRejectsNonLoopback(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", "192.168.1.10:9000", ":9000", "example.com:9000"} {
		if got, err := NormalizeLoopbackHTTPAddr(addr); err == nil {
			t.Fatalf("NormalizeLoopbackHTTPAddr(%q) = %q, want error", addr, got)
		}
	}
	for _, addr := range []string{"127.0.0.1:0", "localhost:9000", "[::1]:0"} {
		if got, err := NormalizeLoopbackHTTPAddr(addr); err != nil || got == "" {
			t.Fatalf("NormalizeLoopbackHTTPAddr(%q) = %q, %v; want normalized address", addr, got, err)
		}
	}
}

// startDaemon boots a daemon in-process against teamDir and returns it. The
// caller MUST defer Shutdown.
func startDaemon(t *testing.T, teamDir string, spawner Spawner) *Daemon {
	t.Helper()
	d, err := New(Config{
		TeamDir:         teamDir,
		LogOut:          io.Discard,
		SpawnerOverride: spawner,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_ = d.Run(ctx)
	}()
	// Wait for socket to appear.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(SocketPath(teamDir)); err == nil {
			return d
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("daemon socket never appeared at %s", SocketPath(teamDir))
	return nil
}

// unixClient builds an http.Client that dials the daemon socket. URL host is
// arbitrary ("./") because Go's http library requires one.
func unixClient(socket string) *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			},
		},
	}
}

func TestDaemon_BootsAndServesInstances(t *testing.T) {
	teamDir := shortTempDir(t)
	d := startDaemon(t, teamDir, newFakeSpawner(30*time.Second).spawn)
	defer d.Shutdown(context.Background())

	client := unixClient(SocketPath(teamDir))

	// Empty list at boot.
	resp, err := client.Get("http://./v1/instances")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("empty list: got %q want []", string(body))
	}

	// Pidfile written.
	pid, err := ReadPidfile(PidPath(teamDir))
	if err != nil {
		t.Fatalf("pidfile: %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("pidfile pid: got %d want %d", pid, os.Getpid())
	}
}

func TestDaemonRecordsLaunchEnvOnBoot(t *testing.T) {
	teamDir := shortTempDir(t)
	daemonRoot := DaemonRoot(teamDir)
	old := &LaunchEnv{
		Bin:        "/tmp/old-agent-teamd",
		Args:       []string{"/tmp/old-agent-teamd", "--target", "/old"},
		Dir:        "/old",
		Env:        []string{"OLD=1"},
		RecordedAt: time.Now().UTC(),
		PID:        111,
		Version:    1,
	}
	if err := WriteLaunchEnv(daemonRoot, old); err != nil {
		t.Fatalf("seed old launch env: %v", err)
	}

	d := startDaemon(t, teamDir, newFakeSpawner(30*time.Second).spawn)
	defer d.Shutdown(context.Background())

	le, err := ReadLaunchEnv(daemonRoot)
	if err != nil {
		t.Fatalf("ReadLaunchEnv: %v", err)
	}
	wantBin, err := os.Executable()
	if err != nil {
		wantBin = os.Args[0]
	}
	wantDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if le.Bin != wantBin || le.Dir != wantDir || le.PID != os.Getpid() || le.Version != 1 {
		t.Fatalf("launch env = %+v, want bin=%q dir=%q pid=%d version=1", le, wantBin, wantDir, os.Getpid())
	}
	if len(le.Args) != len(os.Args) || le.Args[0] != os.Args[0] {
		t.Fatalf("launch env args = %+v, want os.Args starting with %q", le.Args, os.Args[0])
	}
	if envHasKey(le.Env, DefaultStrippedEnvKeys[0]) {
		t.Fatalf("denied env key recorded: %+v", le.Env)
	}
	if !containsLaunchEnvString(le.Stripped, DefaultStrippedEnvKeys[0]) {
		t.Fatalf("stripped keys = %+v, want %s", le.Stripped, DefaultStrippedEnvKeys[0])
	}
	prevBody, err := os.ReadFile(PrevLaunchEnvPath(teamDir))
	if err != nil {
		t.Fatalf("read previous launch env: %v", err)
	}
	var prev LaunchEnv
	if err := json.Unmarshal(prevBody, &prev); err != nil {
		t.Fatalf("parse previous launch env: %v", err)
	}
	if prev.Bin != old.Bin || prev.PID != old.PID {
		t.Fatalf("previous launch env = %+v, want old snapshot %+v", prev, old)
	}
}

func TestDaemon_DispatchEndToEnd(t *testing.T) {
	teamDir := shortTempDir(t)
	fake := newFakeSpawner(30 * time.Second)
	d := startDaemon(t, teamDir, fake.spawn)
	defer d.Shutdown(context.Background())

	client := unixClient(SocketPath(teamDir))
	body := `{"agent":"worker","name":"w","workspace":"` + t.TempDir() + `"}`
	resp, err := client.Post("http://./v1/dispatch", "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("dispatch status: %d body=%s", resp.StatusCode, string(bodyBytes))
	}

	resp, _ = client.Get("http://./v1/instances")
	var list []*Metadata
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list) != 1 || list[0].Instance != "w" {
		t.Errorf("instances: %+v", list)
	}

	// Cleanup: stop the child so reaper doesn't outlive the test.
	resp, _ = client.Post("http://./v1/stop", "application/json", bytes.NewReader([]byte(`{"instance":"w"}`)))
	resp.Body.Close()
	waitForStatusNot(t, d.Manager(), "w", StatusRunning)
}

func TestDaemon_ReconcilesOnStartup(t *testing.T) {
	teamDir := shortTempDir(t)
	daemonRoot := DaemonRoot(teamDir)
	if err := os.MkdirAll(daemonRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	// A long-dead PID to test exited-marking.
	if err := WriteMetadata(daemonRoot, &Metadata{
		Instance: "ghost", Agent: "x", Workspace: "/tmp",
		PID: 999_999_999, Status: StatusRunning, StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	d := startDaemon(t, teamDir, newFakeSpawner(time.Second).spawn)
	defer d.Shutdown(context.Background())

	client := unixClient(SocketPath(teamDir))
	resp, _ := client.Get("http://./v1/instances")
	var list []*Metadata
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list) != 1 {
		t.Fatalf("expected 1 reconciled instance, got %d", len(list))
	}
	if list[0].Status != StatusExited {
		t.Errorf("reconciled status: got %s want exited", list[0].Status)
	}
}

func TestDaemon_ShutdownRemovesPidfileAndSocket(t *testing.T) {
	teamDir := shortTempDir(t)
	d, err := New(Config{TeamDir: teamDir, LogOut: io.Discard, SpawnerOverride: newFakeSpawner(time.Second).spawn})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	// Wait for socket.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(SocketPath(teamDir)); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(PidPath(teamDir)); err != nil {
		t.Fatalf("pidfile missing while running: %v", err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not shut down")
	}

	if _, err := os.Stat(PidPath(teamDir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("pidfile not removed: %v", err)
	}
	if _, err := os.Stat(SocketPath(teamDir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket not removed: %v", err)
	}
}

func TestPidfile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.pid")
	if err := writePidfile(path, 4321); err != nil {
		t.Fatal(err)
	}
	pid, err := ReadPidfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if pid != 4321 {
		t.Errorf("got %d want 4321", pid)
	}
}

func TestPidfile_Missing(t *testing.T) {
	pid, err := ReadPidfile(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Errorf("missing pidfile should not error: %v", err)
	}
	if pid != 0 {
		t.Errorf("missing pidfile pid: got %d want 0", pid)
	}
}
