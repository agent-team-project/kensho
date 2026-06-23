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
	"strconv"
	"strings"
	"time"

	"github.com/jamesaud/agent-team/internal/topology"
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

	// SpawnerOverride lets tests substitute a fake claude. nil -> DefaultSpawner.
	SpawnerOverride Spawner
}

const maxUnixSocketPathLen = 100

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

// Daemon is one running daemon. Build with New, then call Run.
type Daemon struct {
	cfg      Config
	manager  *InstanceManager
	channels *ChannelStore
	events   *EventResolver
	server   *http.Server
	listen   net.Listener
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
	if err := Reconcile(DaemonRoot(d.cfg.TeamDir), d.manager); err != nil {
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

	socket := SocketPath(d.cfg.TeamDir)
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return fmt.Errorf("daemon: mkdir socket dir: %w", err)
	}
	// Stale socket from a prior crashed daemon would cause `bind: address in
	// use`. Best-effort remove before listen.
	_ = os.Remove(socket)
	l, err := net.Listen("unix", socket)
	if err != nil {
		return fmt.Errorf("daemon: listen %s: %w", socket, err)
	}
	d.listen = l
	d.server = &http.Server{
		Handler:           Handler(d.manager, d.channels, d.events, d.cfg.TeamDir),
		ReadHeaderTimeout: 5 * time.Second,
	}
	defer os.Remove(socket)

	d.logf("agent-teamd listening on %s (pid=%d)", socket, os.Getpid())

	errCh := make(chan error, 1)
	go func() {
		err := d.server.Serve(l)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	if d.events != nil {
		go d.events.RunSchedules(runCtx)
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = d.server.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		return err
	}
}

// Shutdown stops the http server and cleans up the socket / pidfile. Safe to
// call multiple times. Tests use this to stop in-process daemons.
func (d *Daemon) Shutdown(ctx context.Context) error {
	if d.server == nil {
		return nil
	}
	return d.server.Shutdown(ctx)
}

// Addr returns the listening socket path. Empty if Run hasn't started yet.
func (d *Daemon) Addr() string {
	if d.listen == nil {
		return ""
	}
	return d.listen.Addr().String()
}

func (d *Daemon) logf(format string, args ...any) {
	if d.cfg.LogOut == nil {
		return
	}
	fmt.Fprintf(d.cfg.LogOut, "%s "+format+"\n", append([]any{time.Now().UTC().Format(time.RFC3339)}, args...)...)
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
