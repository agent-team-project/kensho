// Package daemon implements `agent-teamd`: the per-repo orchestrator daemon
// that owns claude-subprocess lifecycle and serves a small JSON HTTP API over
// a unix socket.
//
// See `documentation/orchestrator.md` for the design. This package is the
// scaffolding + lifecycle endpoints landed in SQU-28; message routing and
// log streaming come in SQU-29.
package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/topology"
)

// Config is the runtime config for one daemon instance.
type Config struct {
	// TeamDir is the absolute path to the consumer's `.agent_team/`. The
	// socket usually lives at TeamDir/daemon.sock; long paths fall back to a
	// hashed socket under /tmp. Per-instance metadata stays under
	// TeamDir/daemon/<instance>/.
	TeamDir string

	// LogOut is where the daemon's own structured-ish log lines go.
	// nil -> os.Stderr.
	LogOut io.Writer

	// HTTPAddr exposes the daemon API on a loopback TCP listener in addition
	// to the Unix socket. Empty defaults to 127.0.0.1:0. Use explicit
	// addresses such as "127.0.0.1:53117" for a stable local port.
	HTTPAddr string

	// Build is the identity of the running daemon binary.
	Build buildinfo.Info

	// SpawnerOverride lets tests substitute a fake claude. nil -> DefaultSpawner.
	SpawnerOverride Spawner
}

const maxUnixSocketPathLen = 100
const defaultLoopbackHTTPAddr = "127.0.0.1:0"

// SocketPath returns the daemon socket path for teamDir. Unix-domain sockets
// have small platform path limits, so very long repo paths use a deterministic
// short socket path while keeping pidfiles and metadata in the repo.
func SocketPath(teamDir string) string {
	inRepo := filepath.Join(teamDir, "daemon.sock")
	if len(inRepo) <= maxUnixSocketPathLen {
		return inRepo
	}
	sum := sha256.Sum256([]byte(filepath.Clean(teamDir)))
	name := hex.EncodeToString(sum[:8]) + ".sock"
	return filepath.Join(shortSocketBaseDir(), "agent-team-"+strconv.Itoa(os.Getuid()), name)
}

func shortSocketBaseDir() string {
	if st, err := os.Stat("/tmp"); err == nil && st.IsDir() {
		return "/tmp"
	}
	return os.TempDir()
}

// PidPath returns the daemon pidfile path under teamDir.
func PidPath(teamDir string) string {
	return filepath.Join(teamDir, "daemon.pid")
}

// DaemonRoot returns the per-repo daemon-runtime metadata dir.
func DaemonRoot(teamDir string) string {
	return filepath.Join(teamDir, "daemon")
}

// LogPath returns the daemon's own log file path.
func LogPath(teamDir string) string {
	return filepath.Join(DaemonRoot(teamDir), "agent-teamd.log")
}

// HTTPAddrPath stores the actual loopback listener address when HTTPAddr is
// enabled. It is removed on graceful shutdown and at the next daemon boot.
func HTTPAddrPath(teamDir string) string {
	return filepath.Join(DaemonRoot(teamDir), "http.addr")
}

// ReadHTTPAddr returns the currently advertised loopback address, if present.
func ReadHTTPAddr(teamDir string) (string, error) {
	body, err := os.ReadFile(HTTPAddrPath(teamDir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

// DaemonHTTPURL formats an address returned by ReadHTTPAddr as an HTTP base
// URL. The address is expected to already be net.JoinHostPort-compatible.
func DaemonHTTPURL(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	return "http://" + addr
}

// NormalizeLoopbackHTTPAddr validates that addr is a loopback-only TCP address
// accepted for the optional daemon HTTP listener.
func NormalizeLoopbackHTTPAddr(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", nil
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("daemon: --http-addr must be a loopback host:port address such as 127.0.0.1:0: %w", err)
	}
	host = strings.TrimSpace(host)
	port = strings.TrimSpace(port)
	if host == "" || port == "" {
		return "", errors.New("daemon: --http-addr must include a loopback host and port")
	}
	if strings.EqualFold(host, "localhost") {
		return net.JoinHostPort(host, port), nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("daemon: --http-addr host must be loopback-only, got %q", host)
	}
	return net.JoinHostPort(ip.String(), port), nil
}

// Daemon is one running daemon. Build with New, then call Run.
type Daemon struct {
	mu       sync.Mutex
	cfg      Config
	manager  *InstanceManager
	channels *ChannelStore
	events   *EventResolver
	server   *http.Server
	listen   net.Listener
	httpAddr string
}

// New constructs a Daemon. Defaults are filled in from cfg.TeamDir.
func New(cfg Config) (*Daemon, error) {
	if cfg.TeamDir == "" {
		return nil, errors.New("daemon: TeamDir is required")
	}
	if cfg.LogOut == nil {
		cfg.LogOut = os.Stderr
	}
	if err := os.MkdirAll(DaemonRoot(cfg.TeamDir), 0o755); err != nil {
		return nil, fmt.Errorf("daemon: mkdir runtime dir: %w", err)
	}
	if _, _, err := origin.EnsureProjectID(cfg.TeamDir); err != nil {
		fmt.Fprintf(cfg.LogOut, "%s project: backfill failed: %v\n",
			time.Now().UTC().Format(time.RFC3339), err)
	}
	httpAddr := strings.TrimSpace(cfg.HTTPAddr)
	if httpAddr == "" {
		httpAddr = defaultLoopbackHTTPAddr
	}
	httpAddr, err := NormalizeLoopbackHTTPAddr(httpAddr)
	if err != nil {
		return nil, err
	}
	cfg.HTTPAddr = httpAddr
	if cfg.Build.Empty() {
		cfg.Build = buildinfo.Current("0.1.0")
	}
	mgr := NewInstanceManager(DaemonRoot(cfg.TeamDir), cfg.SpawnerOverride)
	channels := NewChannelStore(DaemonRoot(cfg.TeamDir))
	// Topology is best-effort: missing or malformed `instances.toml` shouldn't
	// abort daemon boot — the trigger / event paths simply produce empty
	// match sets until the operator fixes and reloads. Boot-time parse errors
	// are surfaced to the daemon log.
	topo, terr := topology.LoadFromTeamDir(cfg.TeamDir)
	if terr != nil {
		fmt.Fprintf(cfg.LogOut, "%s topology: load failed: %v\n",
			time.Now().UTC().Format(time.RFC3339), terr)
		topo = nil
	}
	events := NewEventResolver(mgr, cfg.TeamDir, topo)
	events.SetLogOutput(cfg.LogOut)
	return &Daemon{cfg: cfg, manager: mgr, channels: channels, events: events}, nil
}

// Manager returns the underlying InstanceManager. Useful for tests.
func (d *Daemon) Manager() *InstanceManager { return d.manager }

// Channels returns the ChannelStore. Useful for tests.
func (d *Daemon) Channels() *ChannelStore { return d.channels }

// Events returns the EventResolver. Useful for tests.
func (d *Daemon) Events() *EventResolver { return d.events }

// Run starts the listener and blocks until ctx is cancelled or Shutdown is
// called. It performs orphan reconciliation before accepting connections.
func (d *Daemon) Run(ctx context.Context) error {
	runCtx, cancelSchedules := context.WithCancel(ctx)
	defer cancelSchedules()
	var topo *topology.Topology
	if d.events != nil {
		topo = d.events.Topology()
	}
	if err := ReconcileWithTopology(d.cfg.TeamDir, d.manager, topo); err != nil {
		return fmt.Errorf("daemon: reconcile: %w", err)
	}
	if d.events != nil {
		d.events.RecoverQueueState()
	}

	// Pidfile first, then socket. Tests (and external probes like
	// `agent-team daemon status`) treat the socket as the
	// "daemon is fully up" signal — so the pidfile must already exist by
	// then to avoid a "socket present, pidfile not yet written" race.
	if err := writePidfile(PidPath(d.cfg.TeamDir), os.Getpid()); err != nil {
		return fmt.Errorf("daemon: pidfile: %w", err)
	}
	defer os.Remove(PidPath(d.cfg.TeamDir))
	if _, err := EnsureOperatorToken(d.cfg.TeamDir); err != nil {
		return fmt.Errorf("daemon: operator token: %w", err)
	}
	d.recordLaunchEnv()

	socket := SocketPath(d.cfg.TeamDir)
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return fmt.Errorf("daemon: mkdir socket dir: %w", err)
	}
	_ = os.Remove(HTTPAddrPath(d.cfg.TeamDir))
	// Stale socket from a prior crashed daemon would cause `bind: address in
	// use`. Best-effort remove before listen.
	_ = os.Remove(socket)
	l, err := net.Listen("unix", socket)
	if err != nil {
		return fmt.Errorf("daemon: listen %s: %w", socket, err)
	}
	baseHandler := HandlerWithLog(d.manager, d.channels, d.events, d.cfg.TeamDir, d.cfg.LogOut, d.cfg.Build)
	srv := &http.Server{
		Handler:           loopbackAuthHandler(baseHandler, d.cfg.TeamDir, d.manager, d.cfg.Build),
		ConnContext:       daemonConnContext,
		ReadHeaderTimeout: 5 * time.Second,
	}
	d.mu.Lock()
	d.listen = l
	d.server = srv
	d.mu.Unlock()
	defer os.Remove(socket)
	defer os.Remove(HTTPAddrPath(d.cfg.TeamDir))

	listeners := []net.Listener{l}
	if d.cfg.HTTPAddr != "" {
		httpListen, err := net.Listen("tcp", d.cfg.HTTPAddr)
		if err != nil {
			_ = l.Close()
			return fmt.Errorf("daemon: listen http %s: %w", d.cfg.HTTPAddr, err)
		}
		listeners = append(listeners, httpListen)
		httpAddr := httpListen.Addr().String()
		d.mu.Lock()
		d.httpAddr = httpAddr
		d.mu.Unlock()
		if err := os.WriteFile(HTTPAddrPath(d.cfg.TeamDir), []byte(httpAddr+"\n"), 0o644); err != nil {
			_ = l.Close()
			_ = httpListen.Close()
			return fmt.Errorf("daemon: write http addr: %w", err)
		}
	}

	d.logf("agent-teamd listening on %s (pid=%d)", socket, os.Getpid())
	if httpAddr := d.HTTPAddr(); httpAddr != "" {
		d.logf("agent-teamd loopback http listening on %s", httpAddr)
	}

	errCh := make(chan error, len(listeners))
	for _, listener := range listeners {
		listener := listener
		d.goWithPanicReason("serve "+listener.Addr().String(), func() {
			err := srv.Serve(listener)
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
				return
			}
			errCh <- nil
		})
	}
	if d.events != nil {
		d.goWithPanicReason("schedules", func() { d.events.RunSchedules(runCtx) })
		d.goWithPanicReason("budget queue drains", func() { d.events.RunBudgetQueueDrains(runCtx) })
	}
	if topo != nil {
		notifications, err := loadNotificationConfig(d.cfg.TeamDir)
		if err != nil {
			d.logf("notifications: load config failed: %v; using defaults", err)
			notifications = defaultNotificationConfig()
		}
		d.goWithPanicReason("phase transition watcher", func() {
			runPhaseTransitionWatcher(runCtx, d.cfg.TeamDir, topo, d.channels, notifications, d.logf)
		})
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if err != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdownCtx)
		}
		return err
	}
}

// Shutdown stops the http server and cleans up the socket / pidfile. Safe to
// call multiple times. Tests use this to stop in-process daemons.
func (d *Daemon) Shutdown(ctx context.Context) error {
	d.mu.Lock()
	srv := d.server
	d.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

// Addr returns the listening socket path. Empty if Run hasn't started yet.
func (d *Daemon) Addr() string {
	d.mu.Lock()
	listen := d.listen
	d.mu.Unlock()
	if listen == nil {
		return ""
	}
	return listen.Addr().String()
}

// HTTPAddr returns the optional loopback HTTP address. Empty when disabled or
// before Run has started.
func (d *Daemon) HTTPAddr() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.httpAddr
}

func (d *Daemon) logf(format string, args ...any) {
	if d.cfg.LogOut == nil {
		return
	}
	fmt.Fprintf(d.cfg.LogOut, "%s "+format+"\n", append([]any{time.Now().UTC().Format(time.RFC3339)}, args...)...)
}

func (d *Daemon) goWithPanicReason(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				d.recordExitReason(ExitReason{
					Kind:   ExitKindPanic,
					Reason: strings.TrimSpace(name + ": " + fmt.Sprint(r)),
					Error:  string(debug.Stack()),
				})
				panic(r)
			}
		}()
		fn()
	}()
}

func (d *Daemon) recordExitReason(reason ExitReason) {
	reason.PID = os.Getpid()
	reason.Build = d.cfg.Build
	if err := WriteExitReason(d.cfg.TeamDir, reason); err != nil {
		d.logf("exit-reason: write failed: %v", err)
	}
}

func (d *Daemon) recordLaunchEnv() {
	if err := preservePreviousLaunchEnv(d.cfg.TeamDir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		d.logf("launch-env: preserve previous snapshot failed: %v", err)
	}
	bin, err := os.Executable()
	if err != nil {
		bin = os.Args[0]
	}
	dir, err := os.Getwd()
	if err != nil {
		d.logf("launch-env: get working directory failed: %v", err)
	}
	le := &LaunchEnv{
		Bin:        bin,
		Args:       append([]string(nil), os.Args...),
		Dir:        dir,
		Env:        os.Environ(),
		RecordedAt: time.Now().UTC(),
		PID:        os.Getpid(),
		Version:    1,
		Build:      d.cfg.Build,
	}
	if err := WriteLaunchEnv(DaemonRoot(d.cfg.TeamDir), le); err != nil {
		d.logf("launch-env: write snapshot failed: %v", err)
	}
}

func preservePreviousLaunchEnv(teamDir string) error {
	body, err := os.ReadFile(LaunchEnvPath(teamDir))
	if err != nil {
		return err
	}
	return writeLaunchEnvFileAtomic(PrevLaunchEnvPath(teamDir), body)
}

// writePidfile writes pid to path atomically. Caller is responsible for
// removing on graceful shutdown; orphaned pidfiles are tolerated by status
// checks (which probe liveness via kill(pid,0)).
func writePidfile(path string, pid int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body := []byte(strconv.Itoa(pid) + "\n")
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pid-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ReadPidfile parses path. Returns 0,nil if missing.
func ReadPidfile(path string) (int, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil {
		return 0, fmt.Errorf("pidfile %s: not an integer: %w", path, err)
	}
	return pid, nil
}
