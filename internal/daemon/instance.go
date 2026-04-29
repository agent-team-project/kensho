package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Spawner abstracts the claude child-process call so tests can inject a fake.
// args is the full argv (including the binary name in args[0] for clarity);
// stdoutPath / stderrPath are file paths the child should write to; workspace
// is the chdir target.
//
// On success the returned process is already started and detached enough that
// Wait() can be called by the InstanceManager's reaper goroutine.
type Spawner func(args []string, env []string, workspace, stdoutPath, stderrPath string) (*os.Process, error)

// DefaultSpawner spawns claude as an actual subprocess. Stdin is /dev/null;
// stdout and stderr go to per-instance log files. The child gets its own
// process group so the daemon can later signal it independently.
func DefaultSpawner(args []string, env []string, workspace, stdoutPath, stderrPath string) (*os.Process, error) {
	if len(args) == 0 {
		return nil, errors.New("spawn: empty args")
	}
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		return nil, fmt.Errorf("spawn: open devnull: %w", err)
	}
	defer stdin.Close()
	stdout, err := os.OpenFile(stdoutPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("spawn: open stdout %s: %w", stdoutPath, err)
	}
	defer stdout.Close()
	stderr, err := os.OpenFile(stderrPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("spawn: open stderr %s: %w", stderrPath, err)
	}
	defer stderr.Close()

	attr := &os.ProcAttr{
		Dir:   workspace,
		Env:   env,
		Files: []*os.File{stdin, stdout, stderr},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	}
	bin, err := exec.LookPath(args[0])
	if err != nil {
		return nil, fmt.Errorf("spawn: lookup %s: %w", args[0], err)
	}
	return os.StartProcess(bin, args, attr)
}

// DispatchInput is the validated form of POST /v1/dispatch.
type DispatchInput struct {
	Agent     string
	Name      string
	Prompt    string
	Workspace string
}

// InstanceManager owns spawn / track / stop for claude children. Concurrency:
// a single mutex protects the in-memory map; child wait() runs in goroutines.
type InstanceManager struct {
	daemonRoot string
	spawner    Spawner

	mu        sync.Mutex
	instances map[string]*tracked
}

type tracked struct {
	meta    *Metadata
	process *os.Process
	// reaped is closed by the reaper goroutine after it has finalised the
	// in-memory + on-disk metadata for this incarnation of the instance.
	// Each Dispatch / Start replaces it so the channel always reflects the
	// most recent reaper. Tests use waitReaped() for deterministic ordering;
	// production code does not block on it.
	reaped chan struct{}
}

// NewInstanceManager builds a manager rooted at daemonRoot
// (`.agent_team/daemon/` in production). spawner=nil uses DefaultSpawner.
func NewInstanceManager(daemonRoot string, spawner Spawner) *InstanceManager {
	if spawner == nil {
		spawner = DefaultSpawner
	}
	return &InstanceManager{
		daemonRoot: daemonRoot,
		spawner:    spawner,
		instances:  make(map[string]*tracked),
	}
}

// Dispatch spawns a claude child for in. On success the metadata is persisted
// before this returns, so a daemon crash immediately after spawn still surfaces
// the running child via reconciliation.
//
// SQU-28 keeps the spawn surface intentionally minimal: just `claude
// --session-id <uuid> -p <prompt>`. Agent-resolution / `--agents` / skills
// stay in `agent-team run` and ship to the daemon path in SQU-29.
func (m *InstanceManager) Dispatch(in DispatchInput) (*Metadata, error) {
	if in.Name == "" {
		return nil, errors.New("dispatch: name is required")
	}
	if in.Agent == "" {
		return nil, errors.New("dispatch: agent is required")
	}
	if in.Workspace == "" {
		return nil, errors.New("dispatch: workspace is required")
	}

	m.mu.Lock()
	if t, ok := m.instances[in.Name]; ok && t.meta.Status == StatusRunning {
		m.mu.Unlock()
		return nil, fmt.Errorf("dispatch: instance %q already running (pid=%d)", in.Name, t.meta.PID)
	}
	m.mu.Unlock()

	sessionID := newSessionID()
	if err := os.MkdirAll(instanceDir(m.daemonRoot, in.Name), 0o755); err != nil {
		return nil, err
	}
	logPath := filepath.Join(instanceDir(m.daemonRoot, in.Name), "child.log")

	args := []string{"claude", "--session-id", sessionID}
	if in.Prompt != "" {
		args = append(args, "-p", in.Prompt)
	}

	proc, err := m.spawner(args, os.Environ(), in.Workspace, logPath, logPath)
	if err != nil {
		return nil, fmt.Errorf("dispatch: spawn: %w", err)
	}

	now := time.Now().UTC()
	meta := &Metadata{
		Instance:  in.Name,
		Agent:     in.Agent,
		Workspace: in.Workspace,
		PID:       proc.Pid,
		SessionID: sessionID,
		StartedAt: now,
		Status:    StatusRunning,
		LogPath:   logPath,
	}
	if err := WriteMetadata(m.daemonRoot, meta); err != nil {
		// We've already spawned. Best effort: kill, return error.
		_ = proc.Kill()
		return nil, fmt.Errorf("dispatch: persist metadata: %w", err)
	}

	reaped := make(chan struct{})
	m.mu.Lock()
	m.instances[in.Name] = &tracked{meta: meta, process: proc, reaped: reaped}
	m.mu.Unlock()
	go m.reap(in.Name, proc, reaped)
	return meta, nil
}

// Stop sends SIGTERM to the child and persists status=stopped. The reaper
// goroutine will pick up the eventual exit and finalise.
//
// We mark the in-memory status as Stopped BEFORE signalling so the reaper —
// which can wake up arbitrarily fast on a fast machine, especially under CI
// load — sees Stopped instead of Running and preserves it. (See `reap`'s
// switch: it only flips to Crashed/Exited when prior status was Running.)
func (m *InstanceManager) Stop(instance string) (*Metadata, error) {
	m.mu.Lock()
	t, ok := m.instances[instance]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("stop: unknown instance %q", instance)
	}
	if t.meta.Status != StatusRunning {
		out := *t.meta
		m.mu.Unlock()
		return &out, nil
	}
	now := time.Now().UTC()
	t.meta.Status = StatusStopped
	t.meta.StoppedAt = now
	if err := WriteMetadata(m.daemonRoot, t.meta); err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("stop: persist: %w", err)
	}
	proc := t.process
	out := *t.meta
	m.mu.Unlock()

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// Already gone; the reaper will pick up the wait and finalise.
		if !errors.Is(err, os.ErrProcessDone) {
			return nil, fmt.Errorf("stop: signal: %w", err)
		}
	}
	return &out, nil
}

// Start resumes a previously-stopped persistent instance. It re-spawns claude
// with `--resume <session-id>`. Ephemeral instances cannot be resumed; the
// caller is expected not to ask. (We don't track ephemeral-vs-persistent here
// — the agent's frontmatter gates that, and SQU-29 wires it in.)
func (m *InstanceManager) Start(instance string) (*Metadata, error) {
	m.mu.Lock()
	t, ok := m.instances[instance]
	m.mu.Unlock()
	if !ok {
		// Try loading from disk in case daemon was restarted between stop+start.
		mdisk, err := ReadMetadata(m.daemonRoot, instance)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("start: unknown instance %q", instance)
			}
			return nil, err
		}
		t = &tracked{meta: mdisk}
	}
	if t.meta.Status == StatusRunning {
		return t.meta, nil
	}
	if t.meta.SessionID == "" {
		return nil, fmt.Errorf("start: %q has no session_id; cannot resume", instance)
	}
	if t.meta.Workspace == "" {
		return nil, fmt.Errorf("start: %q has no workspace; cannot resume", instance)
	}

	logPath := t.meta.LogPath
	if logPath == "" {
		logPath = filepath.Join(instanceDir(m.daemonRoot, instance), "child.log")
	}
	args := []string{"claude", "--resume", t.meta.SessionID}
	proc, err := m.spawner(args, os.Environ(), t.meta.Workspace, logPath, logPath)
	if err != nil {
		return nil, fmt.Errorf("start: spawn: %w", err)
	}

	now := time.Now().UTC()
	t.meta.PID = proc.Pid
	t.meta.StartedAt = now
	t.meta.StoppedAt = time.Time{}
	t.meta.ExitedAt = time.Time{}
	t.meta.ExitCode = nil
	t.meta.Status = StatusRunning
	t.meta.LogPath = logPath
	if err := WriteMetadata(m.daemonRoot, t.meta); err != nil {
		_ = proc.Kill()
		return nil, fmt.Errorf("start: persist: %w", err)
	}
	t.process = proc
	reaped := make(chan struct{})
	t.reaped = reaped

	m.mu.Lock()
	m.instances[instance] = t
	m.mu.Unlock()
	go m.reap(instance, proc, reaped)
	return t.meta, nil
}

// List returns a snapshot of every instance the manager knows about.
func (m *InstanceManager) List() []*Metadata {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Metadata, 0, len(m.instances))
	for _, t := range m.instances {
		// Defensive copy so callers can mutate without racing.
		c := *t.meta
		out = append(out, &c)
	}
	return out
}

// reap waits for the child to exit and finalises its metadata. Non-zero exit
// or signal-based exit becomes status=crashed; clean exit becomes
// status=exited UNLESS the prior status was StatusStopped (a stop we issued),
// in which case we leave it as StatusStopped — the user-visible meaning is
// "I stopped this", not "it crashed after my SIGTERM".
//
// Closing `reaped` is the LAST thing reap does, after both the in-memory
// metadata and on-disk metadata have been finalised. Tests block on this
// channel for deterministic ordering.
func (m *InstanceManager) reap(instance string, proc *os.Process, reaped chan<- struct{}) {
	defer close(reaped)
	state, err := proc.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.instances[instance]
	if !ok {
		return
	}
	now := time.Now().UTC()
	t.meta.ExitedAt = now

	switch {
	case err != nil:
		// Wait failed (rare). Mark crashed.
		t.meta.Status = StatusCrashed
	case state == nil:
		t.meta.Status = StatusExited
	case state.ExitCode() == 0:
		// Clean exit. Preserve StatusStopped if the user asked for stop.
		if t.meta.Status != StatusStopped {
			t.meta.Status = StatusExited
		}
		ec := 0
		t.meta.ExitCode = &ec
	default:
		ec := state.ExitCode()
		t.meta.ExitCode = &ec
		// If we issued a stop, the non-zero exit is the SIGTERM result —
		// keep it as stopped.
		if t.meta.Status != StatusStopped {
			t.meta.Status = StatusCrashed
		}
	}
	if err := WriteMetadata(m.daemonRoot, t.meta); err != nil {
		// Reaper has nowhere to surface this; the next reconcile will catch
		// any drift. Don't block the goroutine.
		_ = err
	}
}

// reapedChan returns the per-instance reaper-completion channel snapshotted
// under the lock, so the caller can select on it without racing the next
// dispatch. Returns nil if the instance is unknown or has no in-flight
// reaper (e.g. loaded from disk on startup, never spawned).
//
// Exposed for tests; production code does not need to wait on the reaper.
func (m *InstanceManager) reapedChan(instance string) <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.instances[instance]
	if !ok {
		return nil
	}
	return t.reaped
}

// LoadFromDisk repopulates the manager's in-memory map from on-disk metadata,
// without spawning anything. Used at daemon startup before reconciliation
// runs.
func (m *InstanceManager) LoadFromDisk() error {
	all, err := ListMetadata(m.daemonRoot)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, md := range all {
		m.instances[md.Instance] = &tracked{meta: md}
	}
	return nil
}

// newSessionID generates a UUIDv4-shaped string. We don't use a UUID library
// to keep deps minimal — claude's --session-id accepts any UUID-shape value.
func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Crypto/rand failure on a posix system would be catastrophic;
		// panic'ing matches stdlib idiom.
		panic(fmt.Sprintf("session-id: rand: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	hexed := hex.EncodeToString(b[:])
	return hexed[0:8] + "-" + hexed[8:12] + "-" + hexed[12:16] + "-" + hexed[16:20] + "-" + hexed[20:32]
}
