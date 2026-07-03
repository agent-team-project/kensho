package daemon

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jamesaud/agent-team/internal/loader"
	"github.com/jamesaud/agent-team/internal/runtimebin"
)

var sessionIDFallbackCounter struct {
	sync.Mutex
	value uint32
}

// Spawner abstracts the child-process call so tests can inject a fake.
// args is the full argv (including the binary name in args[0] for clarity);
// stdoutPath / stderrPath are file paths the child should write to; workspace
// is the chdir target. stdin is optional content to pipe into the child.
//
// On success the returned process is already started and detached enough that
// Wait() can be called by the InstanceManager's reaper goroutine.
type Spawner func(args []string, env []string, workspace, stdoutPath, stderrPath, stdin string) (*os.Process, error)

// DefaultSpawner spawns the runtime as an actual subprocess. Unless stdin
// content is provided, stdin is /dev/null; stdout and stderr go to per-instance
// log files. The child gets its own process group so the daemon can later
// signal it independently.
func DefaultSpawner(args []string, env []string, workspace, stdoutPath, stderrPath, stdinContent string) (*os.Process, error) {
	if len(args) == 0 {
		return nil, errors.New("spawn: empty args")
	}
	stdin, cleanupStdin, err := openSpawnerStdin(stdinContent)
	if err != nil {
		return nil, err
	}
	defer cleanupStdin()
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

func openSpawnerStdin(content string) (*os.File, func(), error) {
	if content == "" {
		f, err := os.Open(os.DevNull)
		if err != nil {
			return nil, nil, fmt.Errorf("spawn: open devnull: %w", err)
		}
		return f, func() { _ = f.Close() }, nil
	}
	f, err := os.CreateTemp("", "agent-team-stdin-")
	if err != nil {
		return nil, nil, fmt.Errorf("spawn: create stdin temp file: %w", err)
	}
	cleanup := func() {
		name := f.Name()
		_ = f.Close()
		_ = os.Remove(name)
	}
	if _, err := f.WriteString(content); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("spawn: write stdin temp file: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("spawn: rewind stdin temp file: %w", err)
	}
	return f, cleanup, nil
}

// DispatchInput is the validated form of POST /v1/dispatch.
//
// Args, if set, is the additional argv passed to claude after `claude
// --session-id <uuid>`. This lets the CLI hand off the full
// `--agents/--add-dir/--append-system-prompt-file` machinery without the
// daemon needing to re-derive agent / skill resolution. When Args is empty,
// the daemon falls back to appending `[-p <Prompt>]` only — the SQU-28
// minimal form, used by clients (CTRL hooks, tests) that just want to spawn
// a one-shot claude.
//
// Env, if set, is appended to os.Environ() for the spawned process. The CLI
// uses this to export AGENT_TEAM_ROOT / AGENT_TEAM_INSTANCE / AGENT_TEAM_STATE_DIR.
// Stdin, if set, is piped to the spawned process. Codex one-shot runs use this
// with `codex exec -` so large agent prompts do not live in argv.
type DispatchInput struct {
	Agent         string
	Name          string
	Prompt        string
	Workspace     string
	Runtime       string
	RuntimeBinary string
	Args          []string
	Env           []string
	Stdin         string
	// Budget, if > 0, is a hard wall-clock runtime budget for the dispatched
	// instance. When it elapses before the process exits on its own, a watchdog
	// finalises the instance as Crashed and force-kills its process group (see
	// watchdog). Zero disables the watchdog — the default, so existing callers
	// are unaffected. The ephemeral pipeline spawn path derives this from a step
	// timeout or AGENT_TEAM_INSTANCE_MAX_RUNTIME; persistent/manual dispatches
	// that leave it zero are never watchdogged.
	Budget time.Duration
}

// StopOptions controls graceful stop escalation. The default Stop path sends
// SIGTERM to the instance process group and returns after persisting
// status=stopped. With Force set, the manager waits for Timeout after SIGTERM,
// then sends SIGKILL if the child is still alive. A zero Timeout uses
// stopForceDefaultTimeout.
type StopOptions struct {
	Force   bool
	Timeout time.Duration
}

// RestartOptions controls the stop half of a restart. By default restart
// waits Timeout for a graceful stop. With Force set, restart escalates through
// StopWithOptions, sending SIGKILL if the child does not exit before Timeout.
type RestartOptions struct {
	Force   bool
	Timeout time.Duration
}

const (
	stopForceDefaultTimeout = 10 * time.Second
	stopKillWaitTimeout     = 5 * time.Second
)

// InstanceManager owns spawn / track / stop for claude children. Concurrency:
// a single mutex protects the in-memory map; child wait() runs in goroutines.
type InstanceManager struct {
	daemonRoot string
	spawner    Spawner

	mu        sync.Mutex
	instances map[string]*tracked
	// reapHook, if set, is invoked after each reaper finalises an instance.
	// Used by the topology event dispatcher to release replica capacity for
	// the declared ephemeral instance whose spawn this was. Hook is called
	// without holding m.mu so the callback may safely call back into the
	// manager.
	reapHook func(instance string)
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

	rt, err := m.dispatchRuntime(in)
	if err != nil {
		return nil, fmt.Errorf("dispatch: %w", err)
	}
	sessionID := ""
	if rt.Kind == runtimebin.KindClaude {
		sessionID = newSessionID()
	}
	if err := os.MkdirAll(instanceDir(m.daemonRoot, in.Name), 0o755); err != nil {
		return nil, err
	}
	logPath := filepath.Join(instanceDir(m.daemonRoot, in.Name), "child.log")

	args, err := dispatchArgs(rt, sessionID, in)
	if err != nil {
		return nil, fmt.Errorf("dispatch: %w", err)
	}

	env := append(os.Environ(), in.Env...)
	stdin := dispatchStdin(rt, in)
	proc, err := m.spawner(args, env, in.Workspace, logPath, logPath, stdin)
	if err != nil {
		return nil, fmt.Errorf("dispatch: spawn: %w", err)
	}

	now := time.Now().UTC()
	meta := &Metadata{
		Instance:      in.Name,
		Agent:         in.Agent,
		Runtime:       string(rt.Kind),
		RuntimeBinary: rt.Binary,
		Workspace:     in.Workspace,
		PID:           proc.Pid,
		SessionID:     sessionID,
		StartedAt:     now,
		Status:        StatusRunning,
		LogPath:       logPath,
	}
	if err := m.writeInstanceLaunchEnv(in.Name, args, env, in.Workspace, proc.Pid, now); err != nil {
		_ = proc.Kill()
		return nil, fmt.Errorf("dispatch: persist launch env: %w", err)
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
	m.recordEvent("dispatch", meta, "instance dispatched")
	go m.reap(in.Name, proc, reaped)
	if in.Budget > 0 {
		go m.watchdog(in.Name, proc, reaped, in.Budget)
	}
	return meta, nil
}

// dispatchRuntime resolves the runtime for a dispatch with this precedence:
//
//	explicit in.Runtime  (CLI --runtime, pipeline step, dispatch payload)
//	  > AGENT_TEAM_RUNTIME env override
//	  > the target agent's frontmatter `runtime:`/`runtime_bin:`
//	  > built-in default (claude)
//
// The agent-level default is what lets a team declare, e.g., `runtime: codex`
// on the worker while the manager stays on Claude, without every dispatch
// having to pass an explicit runtime.
func (m *InstanceManager) dispatchRuntime(in DispatchInput) (runtimebin.Runtime, error) {
	if rt, ok, err := runtimebin.FromFields(in.Runtime, in.RuntimeBinary); err != nil || ok {
		return rt, err
	}
	// A deliberate env override outranks a static per-agent default.
	if strings.TrimSpace(os.Getenv(runtimebin.EnvRuntime)) != "" {
		return runtimebin.Current()
	}
	if agent := m.agentForRuntime(in.Agent); agent != nil {
		if rt, ok, err := runtimebin.FromFields(agent.Runtime, agent.RuntimeBin); err != nil || ok {
			return rt, err
		}
	}
	return runtimebin.Current()
}

// agentForRuntime loads the named agent's definition to read its frontmatter
// runtime hint. A load failure returns nil — runtime resolution then falls back
// to the env/default path, and the dispatch surfaces a clearer error downstream
// if the agent genuinely cannot be loaded.
func (m *InstanceManager) agentForRuntime(name string) *loader.Agent {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	teamDir := filepath.Dir(m.daemonRoot)
	a, err := loader.LoadAgent(filepath.Join(teamDir, "agents", name), teamDir)
	if err != nil {
		return nil
	}
	return a
}

func dispatchArgs(rt runtimebin.Runtime, sessionID string, in DispatchInput) ([]string, error) {
	switch rt.Kind {
	case runtimebin.KindClaude:
		args := []string{rt.Binary, "--session-id", sessionID}
		if len(in.Args) > 0 {
			args = append(args, in.Args...)
		} else if in.Prompt != "" {
			args = append(args, "-p", in.Prompt)
		}
		return args, nil
	case runtimebin.KindCodex:
		if len(in.Args) > 0 {
			if in.Args[0] != "exec" {
				return nil, errors.New("codex daemon dispatch requires args beginning with exec; use agent-team run --prompt for managed Codex runs")
			}
			return append([]string{rt.Binary}, in.Args...), nil
		}
		if strings.TrimSpace(in.Prompt) == "" {
			return nil, errors.New("codex daemon dispatch requires exec args or a prompt")
		}
		codexArgs := []string{rt.Binary, "exec"}
		codexArgs = append(codexArgs, runtimebin.CodexAutonomousExecArgs()...)
		codexArgs = append(codexArgs, "-")
		return codexArgs, nil
	default:
		return nil, fmt.Errorf("unsupported runtime %q", rt.Kind)
	}
}

func dispatchStdin(rt runtimebin.Runtime, in DispatchInput) string {
	if rt.Kind != runtimebin.KindCodex {
		return ""
	}
	if in.Stdin != "" {
		return in.Stdin
	}
	if len(in.Args) == 0 {
		return in.Prompt
	}
	return ""
}

// Stop sends SIGTERM to the instance process group and persists
// status=stopped. The reaper goroutine will pick up the eventual exit and
// finalise.
//
// We mark the in-memory status as Stopped BEFORE signalling so the reaper —
// which can wake up arbitrarily fast on a fast machine, especially under CI
// load — sees Stopped instead of Running and preserves it. (See `reap`'s
// switch: it only flips to Crashed/Exited when prior status was Running.)
func (m *InstanceManager) Stop(instance string) (*Metadata, error) {
	return m.StopWithOptions(instance, StopOptions{})
}

// StopWithOptions sends SIGTERM to the instance process group and optionally
// escalates to SIGKILL if Force is set and the process does not exit within
// the configured timeout. The user-visible lifecycle remains StatusStopped
// because the user requested the stop, even if escalation was required.
func (m *InstanceManager) StopWithOptions(instance string, opts StopOptions) (*Metadata, error) {
	if opts.Timeout < 0 {
		return nil, errors.New("stop: timeout must be >= 0")
	}
	if opts.Force && opts.Timeout == 0 {
		opts.Timeout = stopForceDefaultTimeout
	}

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
	pid := t.meta.PID
	reaped := t.reaped
	out := *t.meta
	m.mu.Unlock()

	if proc == nil {
		var err error
		proc, err = os.FindProcess(pid)
		if err != nil {
			return nil, fmt.Errorf("stop: find pid %d: %w", pid, err)
		}
	}
	if err := signalProcessGroupOrProcess(proc, pid, syscall.SIGTERM); err != nil {
		// Already gone; the reaper will pick up the wait and finalise.
		if !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
			return nil, fmt.Errorf("stop: signal: %w", err)
		}
	}
	if opts.Force {
		stopped := waitForProcessExit(pid, reaped, opts.Timeout)
		if !stopped {
			if err := signalProcessGroupOrProcess(proc, pid, syscall.SIGKILL); err != nil {
				if !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
					return nil, fmt.Errorf("stop: kill: %w", err)
				}
			}
			m.recordEvent("kill", &out, "instance stop escalated to SIGKILL")
			if !waitForProcessExit(pid, reaped, stopKillWaitTimeout) {
				return nil, fmt.Errorf("stop: pid %d did not exit after SIGKILL", pid)
			}
		}
	}
	m.recordEvent("stop", &out, "instance stop requested")
	return &out, nil
}

func signalProcessGroupOrProcess(proc *os.Process, pid int, sig syscall.Signal) error {
	if pid > 0 {
		if err := syscall.Kill(-pid, sig); err == nil {
			return nil
		} else if !errors.Is(err, syscall.ESRCH) {
			return err
		}
	}
	if proc == nil {
		if pid <= 0 {
			return os.ErrProcessDone
		}
		var err error
		proc, err = os.FindProcess(pid)
		if err != nil {
			return err
		}
	}
	return proc.Signal(sig)
}

func waitForProcessExit(pid int, reaped <-chan struct{}, timeout time.Duration) bool {
	if timeout < 0 {
		timeout = 0
	}
	if reaped != nil {
		if timeout == 0 {
			select {
			case <-reaped:
				return true
			default:
				return false
			}
		}
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-reaped:
			return true
		case <-timer.C:
			return false
		}
	}
	if pid == 0 {
		return true
	}
	if timeout == 0 {
		return !PidLiveCheck(pid)
	}
	deadline := time.Now().Add(timeout)
	for {
		if !PidLiveCheck(pid) {
			return true
		}
		if !time.Now().Before(deadline) {
			return !PidLiveCheck(pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Restart stops a running instance, waits for the old child to exit when the
// daemon owns a reaper for it, then resumes the same session. If the instance
// is already stopped/exited/crashed, Restart behaves like Start.
func (m *InstanceManager) Restart(instance string, timeout time.Duration) (*Metadata, error) {
	return m.RestartWithOptions(instance, RestartOptions{Timeout: timeout})
}

func (m *InstanceManager) RestartWithOptions(instance string, opts RestartOptions) (*Metadata, error) {
	if opts.Timeout < 0 {
		return nil, errors.New("restart: timeout must be >= 0")
	}
	if opts.Timeout == 0 {
		opts.Timeout = 10 * time.Second
	}

	m.mu.Lock()
	t, ok := m.instances[instance]
	running := ok && t.meta.Status == StatusRunning
	pid := 0
	var reaped <-chan struct{}
	if running {
		pid = t.meta.PID
		reaped = t.reaped
	}
	m.mu.Unlock()

	if !ok {
		return m.Start(instance)
	}
	if running {
		if opts.Force {
			if _, err := m.StopWithOptions(instance, StopOptions{Force: true, Timeout: opts.Timeout}); err != nil {
				return nil, err
			}
		} else if _, err := m.Stop(instance); err != nil {
			return nil, err
		}
		if !opts.Force && reaped != nil {
			select {
			case <-reaped:
			case <-time.After(opts.Timeout):
				return nil, fmt.Errorf("restart: %q did not stop within %s", instance, opts.Timeout)
			}
		} else if !opts.Force {
			deadline := time.Now().Add(opts.Timeout)
			for time.Now().Before(deadline) {
				if !PidLiveCheck(pid) {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if PidLiveCheck(pid) {
				return nil, fmt.Errorf("restart: %q pid %d did not stop within %s", instance, pid, opts.Timeout)
			}
		}
	}
	meta, err := m.Start(instance)
	if err != nil {
		return nil, err
	}
	m.recordEvent("restart", meta, "instance restarted")
	return meta, nil
}

// Remove deletes daemon-owned runtime metadata for an instance. Running
// instances are refused unless force=true; with force, Remove stops the child
// first and waits for it to exit before deleting metadata.
func (m *InstanceManager) Remove(instance string, force bool, timeout time.Duration) error {
	if instance == "" {
		return errors.New("remove: instance is required")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	m.mu.Lock()
	t, ok := m.instances[instance]
	m.mu.Unlock()
	if !ok {
		mdisk, err := ReadMetadata(m.daemonRoot, instance)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("remove: unknown instance %q", instance)
			}
			return err
		}
		t = &tracked{meta: mdisk}
		m.mu.Lock()
		if current, exists := m.instances[instance]; exists {
			t = current
		} else {
			m.instances[instance] = t
		}
		m.mu.Unlock()
	}

	m.mu.Lock()
	running := t.meta.Status == StatusRunning
	pid := t.meta.PID
	reaped := t.reaped
	m.mu.Unlock()

	if running {
		if !force {
			return fmt.Errorf("remove: instance %q is running; stop it first or use force", instance)
		}
		if _, err := m.Stop(instance); err != nil {
			return err
		}
		if reaped != nil {
			select {
			case <-reaped:
			case <-time.After(timeout):
				return fmt.Errorf("remove: %q did not stop within %s", instance, timeout)
			}
		} else {
			deadline := time.Now().Add(timeout)
			for time.Now().Before(deadline) {
				if !PidLiveCheck(pid) {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if PidLiveCheck(pid) {
				return fmt.Errorf("remove: %q pid %d did not stop within %s", instance, pid, timeout)
			}
		}
	}

	m.mu.Lock()
	eventMeta := *t.meta
	delete(m.instances, instance)
	m.mu.Unlock()
	if err := RemoveInstance(m.daemonRoot, instance); err != nil {
		return fmt.Errorf("remove: metadata: %w", err)
	}
	m.recordEvent("remove", &eventMeta, "instance metadata removed")
	return nil
}

// Start resumes a previously-stopped persistent instance. It re-spawns claude
// with `--resume <session-id>`. Ephemeral instances cannot be resumed; the
// caller is expected not to ask. (We don't track ephemeral-vs-persistent here
// — the agent's frontmatter gates that, and SQU-29 wires it in.)
func (m *InstanceManager) Start(instance string) (*Metadata, error) {
	return m.start(instance, nil)
}

func (m *InstanceManager) start(instance string, expected *Metadata) (*Metadata, error) {
	if instance == "" {
		return nil, errors.New("start: instance is required")
	}
	if err := m.ensureTracked(instance, expected); err != nil {
		return nil, err
	}

	m.mu.Lock()
	t := m.instances[instance]
	if t == nil || t.meta == nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("start: unknown instance %q", instance)
	}
	if expected != nil && !sameTrackedIncarnation(t, expected) {
		if t.meta.Status == StatusRunning && PidLiveCheck(t.meta.PID) {
			out := *t.meta
			m.mu.Unlock()
			return &out, nil
		}
		m.mu.Unlock()
		return nil, fmt.Errorf("start: instance %q changed concurrently", instance)
	}
	if revived, out, err := m.reviveLiveIncarnationLocked(t, expected); revived || err != nil {
		m.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	if t.meta.Status == StatusRunning {
		if PidLiveCheck(t.meta.PID) {
			out := *t.meta
			m.mu.Unlock()
			return &out, nil
		}
		t.meta.Status = StatusExited
		t.meta.ExitedAt = time.Now().UTC()
		if err := WriteMetadata(m.daemonRoot, t.meta); err != nil {
			m.mu.Unlock()
			return nil, fmt.Errorf("start: reconcile stale pid: %w", err)
		}
	}

	base := *t.meta
	baseRuntime := metadataRuntimeKind(&base)
	if baseRuntime != runtimebin.KindClaude {
		m.mu.Unlock()
		return nil, fmt.Errorf("start: runtime %q does not support managed resume; create a new run instead", baseRuntime)
	}
	if base.SessionID == "" {
		m.mu.Unlock()
		return nil, fmt.Errorf("start: %q has no session_id; cannot resume", instance)
	}
	if base.Workspace == "" {
		m.mu.Unlock()
		return nil, fmt.Errorf("start: %q has no workspace; cannot resume", instance)
	}
	if brief, err := InstanceBriefLaunchText(filepath.Dir(m.daemonRoot), instance); err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("start: brief: %w", err)
	} else if brief != "" {
		if err := AppendMessage(m.daemonRoot, instance, &Message{
			From: "agent-team",
			To:   instance,
			Body: brief,
			TS:   time.Now().UTC(),
		}); err != nil {
			m.mu.Unlock()
			return nil, fmt.Errorf("start: brief mailbox: %w", err)
		}
	}

	logPath := base.LogPath
	if logPath == "" {
		logPath = filepath.Join(instanceDir(m.daemonRoot, instance), "child.log")
	}
	bin := strings.TrimSpace(base.RuntimeBinary)
	if bin == "" {
		var err error
		bin, err = runtimebin.ClaudeCompatibleBinary()
		if err != nil {
			m.mu.Unlock()
			return nil, fmt.Errorf("start: %w", err)
		}
	}
	args := []string{bin, "--resume", base.SessionID}
	env, err := m.startEnv(instance)
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("start: launch env: %w", err)
	}
	proc, err := m.spawner(args, env, base.Workspace, logPath, logPath, "")
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("start: spawn: %w", err)
	}

	now := time.Now().UTC()
	meta := base
	meta.PID = proc.Pid
	meta.StartedAt = now
	meta.StoppedAt = time.Time{}
	meta.ExitedAt = time.Time{}
	meta.ExitCode = nil
	meta.Status = StatusRunning
	meta.LogPath = logPath
	meta.Adopted = false
	meta.RestartBackoffUntil = time.Time{}
	reaped := make(chan struct{})
	next := &tracked{meta: &meta, process: proc, reaped: reaped}

	m.instances[instance] = next
	if err := m.writeInstanceLaunchEnv(instance, args, env, base.Workspace, proc.Pid, now); err != nil {
		m.mu.Unlock()
		_ = proc.Kill()
		return nil, fmt.Errorf("start: persist launch env: %w", err)
	}
	if err := WriteMetadata(m.daemonRoot, &meta); err != nil {
		m.mu.Unlock()
		_ = proc.Kill()
		return nil, fmt.Errorf("start: persist: %w", err)
	}
	m.mu.Unlock()

	m.recordEvent("start", &meta, "instance resumed")
	go m.reap(instance, proc, reaped)
	out := meta
	return &out, nil
}

func (m *InstanceManager) ensureTracked(instance string, expected *Metadata) error {
	m.mu.Lock()
	if _, ok := m.instances[instance]; ok {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	mdisk := expected
	if mdisk == nil {
		var err error
		mdisk, err = ReadMetadata(m.daemonRoot, instance)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("start: unknown instance %q", instance)
			}
			return err
		}
	}
	m.mu.Lock()
	if _, ok := m.instances[instance]; !ok {
		meta := *mdisk
		m.instances[instance] = &tracked{meta: &meta}
	}
	m.mu.Unlock()
	return nil
}

func (m *InstanceManager) reviveLiveIncarnationLocked(t *tracked, expected *Metadata) (bool, *Metadata, error) {
	if t == nil || t.meta == nil || expected == nil || !sameTrackedIncarnation(t, expected) || expected.PID <= 0 {
		return false, nil, nil
	}
	if !PidLiveCheck(expected.PID) {
		return false, nil, nil
	}
	t.meta.Status = StatusRunning
	t.meta.ExitedAt = time.Time{}
	t.meta.ExitCode = nil
	t.meta.Adopted = true
	t.meta.RestartBackoffUntil = time.Time{}
	if err := WriteMetadata(m.daemonRoot, t.meta); err != nil {
		return true, nil, fmt.Errorf("start: revive live pid: %w", err)
	}
	out := *t.meta
	return true, &out, nil
}

func (m *InstanceManager) launchPrepared(in DispatchInput, expected *Metadata) (*Metadata, bool, error) {
	if in.Name == "" {
		return nil, false, errors.New("dispatch: name is required")
	}
	if in.Agent == "" {
		return nil, false, errors.New("dispatch: agent is required")
	}
	if in.Workspace == "" {
		return nil, false, errors.New("dispatch: workspace is required")
	}

	rt, err := m.dispatchRuntime(in)
	if err != nil {
		return nil, false, fmt.Errorf("dispatch: %w", err)
	}
	sessionID := ""
	if rt.Kind == runtimebin.KindClaude {
		sessionID = newSessionID()
	}
	if err := os.MkdirAll(instanceDir(m.daemonRoot, in.Name), 0o755); err != nil {
		return nil, false, err
	}
	logPath := filepath.Join(instanceDir(m.daemonRoot, in.Name), "child.log")
	args, err := dispatchArgs(rt, sessionID, in)
	if err != nil {
		return nil, false, fmt.Errorf("dispatch: %w", err)
	}
	env, err := m.launchPreparedEnv(in.Name, in.Env)
	if err != nil {
		return nil, false, fmt.Errorf("dispatch: launch env: %w", err)
	}
	stdin := dispatchStdin(rt, in)

	m.mu.Lock()
	if expected != nil {
		if err := m.ensureExpectedTrackedLocked(expected); err != nil {
			m.mu.Unlock()
			return nil, false, err
		}
	}
	t := m.instances[in.Name]
	if expected != nil && t != nil && !sameTrackedIncarnation(t, expected) {
		if t.meta.Status == StatusRunning && PidLiveCheck(t.meta.PID) {
			out := *t.meta
			m.mu.Unlock()
			return &out, false, nil
		}
		m.mu.Unlock()
		return nil, false, fmt.Errorf("dispatch: instance %q changed concurrently", in.Name)
	}
	if revived, out, err := m.reviveLiveIncarnationLocked(t, expected); revived || err != nil {
		m.mu.Unlock()
		if err != nil {
			return nil, false, err
		}
		return out, false, nil
	}
	if t != nil && t.meta.Status == StatusRunning {
		if PidLiveCheck(t.meta.PID) {
			out := *t.meta
			m.mu.Unlock()
			return &out, false, nil
		}
		t.meta.Status = StatusExited
		t.meta.ExitedAt = time.Now().UTC()
		if err := WriteMetadata(m.daemonRoot, t.meta); err != nil {
			m.mu.Unlock()
			return nil, false, fmt.Errorf("dispatch: reconcile stale pid: %w", err)
		}
	}

	proc, err := m.spawner(args, env, in.Workspace, logPath, logPath, stdin)
	if err != nil {
		m.mu.Unlock()
		return nil, false, fmt.Errorf("dispatch: spawn: %w", err)
	}
	now := time.Now().UTC()
	meta := &Metadata{
		Instance:      in.Name,
		Agent:         in.Agent,
		Runtime:       string(rt.Kind),
		RuntimeBinary: rt.Binary,
		Workspace:     in.Workspace,
		PID:           proc.Pid,
		SessionID:     sessionID,
		StartedAt:     now,
		Status:        StatusRunning,
		LogPath:       logPath,
	}
	if err := m.writeInstanceLaunchEnv(in.Name, args, env, in.Workspace, proc.Pid, now); err != nil {
		m.mu.Unlock()
		_ = proc.Kill()
		return nil, false, fmt.Errorf("dispatch: persist launch env: %w", err)
	}
	if err := WriteMetadata(m.daemonRoot, meta); err != nil {
		m.mu.Unlock()
		_ = proc.Kill()
		return nil, false, fmt.Errorf("dispatch: persist metadata: %w", err)
	}
	reaped := make(chan struct{})
	m.instances[in.Name] = &tracked{meta: meta, process: proc, reaped: reaped}
	m.mu.Unlock()

	m.recordEvent("dispatch", meta, "instance dispatched")
	go m.reap(in.Name, proc, reaped)
	if in.Budget > 0 {
		go m.watchdog(in.Name, proc, reaped, in.Budget)
	}
	return meta, true, nil
}

func (m *InstanceManager) startEnv(instance string) ([]string, error) {
	env, ok, err := m.instanceLaunchEnv(instance)
	if err != nil {
		return nil, err
	}
	if ok {
		return env, nil
	}
	return os.Environ(), nil
}

func (m *InstanceManager) launchPreparedEnv(instance string, overlay []string) ([]string, error) {
	env, ok, err := m.instanceLaunchEnv(instance)
	if err != nil {
		return nil, err
	}
	if ok {
		return env, nil
	}
	return append(os.Environ(), overlay...), nil
}

func (m *InstanceManager) instanceLaunchEnv(instance string) ([]string, bool, error) {
	snapshot, err := ReadInstanceLaunchEnv(m.daemonRoot, instance)
	if err == nil {
		return append([]string(nil), snapshot.Env...), true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return nil, false, err
}

func (m *InstanceManager) writeInstanceLaunchEnv(instance string, args, env []string, workspace string, pid int, recordedAt time.Time) error {
	bin := ""
	if len(args) > 0 {
		bin = args[0]
	}
	snapshot := &LaunchEnv{
		Bin:        bin,
		Args:       append([]string(nil), args...),
		Dir:        workspace,
		Env:        append([]string(nil), env...),
		RecordedAt: recordedAt,
		PID:        pid,
		Version:    1,
	}
	return WriteInstanceLaunchEnv(m.daemonRoot, instance, snapshot)
}

func (m *InstanceManager) ensureExpectedTrackedLocked(expected *Metadata) error {
	if expected == nil {
		return nil
	}
	if current := m.instances[expected.Instance]; current == nil {
		meta := *expected
		m.instances[expected.Instance] = &tracked{meta: &meta}
	}
	return nil
}

func metadataRuntimeKind(meta *Metadata) runtimebin.Kind {
	if meta == nil || strings.TrimSpace(meta.Runtime) == "" {
		return runtimebin.KindClaude
	}
	kind, err := runtimebin.ParseKind(meta.Runtime)
	if err != nil {
		return runtimebin.KindClaude
	}
	return kind
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

func (m *InstanceManager) isRunning(instance string) bool {
	m.mu.Lock()
	t, ok := m.instances[instance]
	if !ok || t.meta.Status != StatusRunning {
		m.mu.Unlock()
		return false
	}
	if PidLiveCheck(t.meta.PID) {
		m.mu.Unlock()
		return true
	}
	// Adopted records loaded from disk have no reaper to observe the missing
	// process. Reconcile this one record so event routing does not message a
	// dead persistent target.
	if t.reaped != nil {
		m.mu.Unlock()
		return false
	}
	now := time.Now().UTC()
	t.meta.Status = StatusExited
	t.meta.ExitedAt = now
	meta := *t.meta
	m.mu.Unlock()
	if err := WriteMetadata(m.daemonRoot, &meta); err == nil {
		m.recordEvent("exit", &meta, "reconciled missing process")
	}
	return false
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
	t, ok := m.instances[instance]
	if !ok {
		m.mu.Unlock()
		return
	}
	if t.process != proc {
		// A newer incarnation of this instance has already been spawned.
		// This stale reaper must not overwrite the current metadata.
		m.mu.Unlock()
		return
	}
	now := time.Now().UTC()
	t.meta.ExitedAt = now
	eventAction := ""

	switch {
	case err != nil:
		// Wait failed (rare). Mark crashed.
		t.meta.Status = StatusCrashed
		eventAction = "crash"
	case state == nil:
		t.meta.Status = StatusExited
		eventAction = "exit"
	case state.ExitCode() == 0:
		// Clean exit. Only promote from Running; never clobber a status that was
		// already finalised before the reaper ran (StatusStopped from a stop, or
		// StatusCrashed from the watchdog force-killing a hung instance — a wedged
		// child that traps SIGTERM and exits 0 must still be treated as a crash so
		// the pipeline retries rather than advancing as if it succeeded).
		if t.meta.Status == StatusRunning {
			t.meta.Status = StatusExited
			eventAction = "exit"
		}
		ec := 0
		t.meta.ExitCode = &ec
	default:
		ec := state.ExitCode()
		t.meta.ExitCode = &ec
		// Only promote from Running. A pre-set terminal status is authoritative:
		// StatusStopped means the user issued a stop (the non-zero exit is the
		// SIGTERM result); StatusCrashed means the watchdog already finalised it.
		if t.meta.Status == StatusRunning {
			t.meta.Status = StatusCrashed
			eventAction = "crash"
		}
	}
	if err := WriteMetadata(m.daemonRoot, t.meta); err != nil {
		// Reaper has nowhere to surface this; the next reconcile will catch
		// any drift. Don't block the goroutine.
		_ = err
	}
	hook := m.reapHook
	eventMeta := *t.meta
	m.mu.Unlock()
	if eventAction != "" {
		m.recordEvent(eventAction, &eventMeta, "instance process exited")
	}
	if hook != nil {
		hook(instance)
	}
}

// watchdog enforces a hard wall-clock runtime budget on a dispatched instance.
// Codex/Claude children can wedge on the model's streaming backend between turns
// with no client-side timeout, holding a replica slot and stalling the pipeline
// indefinitely. When the budget elapses before the reaper fires, the watchdog
// finalises the instance as Crashed — deliberately NOT Stopped, because Stopped
// suppresses pipeline auto-advance, which is the exact stall we are breaking —
// then force-kills the process group. The already-running reaper observes the
// exit and fires the reap hook, so the pipeline retries the step (MaxAttempts)
// exactly as it would for any other crash.
//
// The reaper remains the SOLE finaliser that fires the hook: the watchdog only
// pre-marks status and kills, so the pipeline still advances exactly once. A
// non-positive budget disables the watchdog (the default).
func (m *InstanceManager) watchdog(instance string, proc *os.Process, reaped <-chan struct{}, budget time.Duration) {
	if budget <= 0 || proc == nil {
		return
	}
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case <-reaped:
		// Exited on its own within budget — nothing to enforce.
		return
	case <-timer.C:
	}

	// Budget elapsed. Re-validate under the lock that THIS process is still the
	// live, running incarnation before touching anything: the reaper may have
	// just finalised it, a stop may have set Stopped, or a newer dispatch may
	// have replaced it. Any of those → no-op (the watchdog never double-kills).
	m.mu.Lock()
	t, ok := m.instances[instance]
	if !ok || t.process != proc || t.meta.Status != StatusRunning {
		m.mu.Unlock()
		return
	}
	pid := t.meta.PID
	// Mark Crashed and persist BEFORE killing: the terminal intent is then durable
	// across a daemon restart in the kill→reap window, and the reaper (which only
	// promotes from Running) preserves Crashed instead of recording a plain exit
	// if the child happens to exit 0 on the signal.
	t.meta.Status = StatusCrashed
	out := *t.meta
	if err := WriteMetadata(m.daemonRoot, t.meta); err != nil {
		// Nowhere to surface this; the reaper + next reconcile still finalise.
		_ = err
	}
	m.mu.Unlock()
	m.recordEvent("watchdog", &out, fmt.Sprintf("instance exceeded runtime budget %s; killing", budget))

	// SIGTERM the process group, allow a short grace, then SIGKILL. A wedged child
	// commonly ignores SIGTERM, so escalation is expected. Signal errors are
	// best-effort and unactionable from this goroutine: if the process is already
	// gone (ErrProcessDone/ESRCH) the reaper handles the wait; any other failure
	// still leaves the reaper as the finaliser.
	_ = signalProcessGroupOrProcess(proc, pid, syscall.SIGTERM)
	if waitForProcessExit(pid, reaped, stopKillWaitTimeout) {
		return
	}
	_ = signalProcessGroupOrProcess(proc, pid, syscall.SIGKILL)
}

// SetReapHook installs (or replaces) a callback invoked after each reaper
// finalises an instance. Pass nil to clear. The hook runs outside the
// manager's lock, so callbacks may safely call into the manager.
func (m *InstanceManager) SetReapHook(fn func(instance string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reapHook = fn
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

func (m *InstanceManager) recordEvent(action string, meta *Metadata, message string) {
	if meta == nil {
		return
	}
	_ = AppendLifecycleEvent(m.daemonRoot, &LifecycleEvent{
		Action:   action,
		Instance: meta.Instance,
		Agent:    meta.Agent,
		Job:      meta.Job,
		Ticket:   meta.Ticket,
		Branch:   meta.Branch,
		PR:       meta.PR,
		Status:   meta.Status,
		PID:      meta.PID,
		ExitCode: meta.ExitCode,
		Message:  message,
	})
}

// WaitForReaper blocks until the per-instance reaper goroutine has finalised
// its metadata (in-memory + on-disk meta.json + the spawner's stdout/stderr
// fd close). Returns nil after a successful wait, or an error if the timeout
// elapses or the instance has no in-flight reaper.
//
// Exported for cli-package tests that drive an in-process InstanceManager
// against a t.TempDir(); without it, t.TempDir's cleanup races the reaper's
// WriteMetadata rename. Production code paths don't need to call this.
func (m *InstanceManager) WaitForReaper(instance string, timeout time.Duration) error {
	ch := m.reapedChan(instance)
	if ch == nil {
		return fmt.Errorf("daemon: no reaper for instance %q", instance)
	}
	select {
	case <-ch:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("daemon: reaper for %q did not finish in %s", instance, timeout)
	}
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
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return fallbackSessionIDBytes()
	}
	return formatSessionIDBytes(b)
}

func fallbackSessionIDBytes() string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[0:8], uint64(time.Now().UTC().UnixNano()))
	binary.BigEndian.PutUint32(b[8:12], uint32(os.Getpid()))
	binary.BigEndian.PutUint32(b[12:16], nextSessionIDFallbackCounter())
	return formatSessionIDBytes(b)
}

func nextSessionIDFallbackCounter() uint32 {
	sessionIDFallbackCounter.Lock()
	defer sessionIDFallbackCounter.Unlock()
	sessionIDFallbackCounter.value++
	return sessionIDFallbackCounter.value
}

func formatSessionIDBytes(b [16]byte) string {
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	hexed := hex.EncodeToString(b[:])
	return hexed[0:8] + "-" + hexed[8:12] + "-" + hexed[12:16] + "-" + hexed[16:20] + "-" + hexed[20:32]
}
