package daemon

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
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

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/agent-team-project/agent-team/internal/loader"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/runtimebin"
	"github.com/agent-team-project/agent-team/internal/runtimeotel"
	teamtemplate "github.com/agent-team-project/agent-team/internal/template"
	"github.com/agent-team-project/agent-team/internal/topology"
)

var sessionIDFallbackCounter struct {
	sync.Mutex
	value uint32
}

var sessionIDRand = struct {
	sync.RWMutex
	reader io.Reader
}{
	reader: rand.Reader,
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
	Job           string
	Ticket        string
	Branch        string
	PR            string
	Origin        origin.Envelope
	Prompt        string
	Workspace     string
	Runtime       string
	RuntimeBinary string
	Args          []string
	Env           []string
	// EnvComplete means Env is already the full process environment. The
	// normal dispatch path treats Env as an overlay on top of a persisted
	// launch snapshot or os.Environ.
	EnvComplete bool
	// EnvAllow, when non-nil, filters the final process environment by glob
	// before spawn and snapshot persistence. AGENT_TEAM_* is always retained.
	EnvAllow     []string
	StripOTelEnv bool
	Stdin        string
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

// StartOptions controls managed resume behavior. The default Start path keeps
// historical behavior: preflight failures may fall back to a fresh declared
// launch, but missing session IDs are refused. Interrupts tighten that policy
// unless the caller explicitly allows conversation-losing fallback.
type StartOptions struct {
	ResumePrompt             string
	DisallowFreshFallback    bool
	AllowFreshWithoutSession bool
}

// RestartOptions controls the stop half of a restart. By default restart
// waits Timeout for a graceful stop. With Force set, restart escalates through
// StopWithOptions, sending SIGKILL if the child does not exit before Timeout.
type RestartOptions struct {
	Force   bool
	Timeout time.Duration
	StartOptions
}

// InterruptOptions controls a hard mailbox push: durable delivery followed by
// a managed stop+resume of the same runtime session.
type InterruptOptions struct {
	From    string
	Body    string
	Force   bool
	Timeout time.Duration
}

// InterruptResult reports the durable message and resumed runtime metadata.
type InterruptResult struct {
	Message   *Message
	Metadata  *Metadata
	Delivered int
	Truncated bool
}

const (
	stopForceDefaultTimeout        = 10 * time.Second
	stopKillWaitTimeout            = 5 * time.Second
	codexSessionCaptureInitialWait = 50 * time.Millisecond
	codexSessionCaptureTimeout     = 30 * time.Second
	codexSessionCapturePoll        = 25 * time.Millisecond
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
	reapHook     func(instance string)
	terminalHook func(*Metadata)
}

type tracked struct {
	meta           *Metadata
	process        *os.Process
	watchdogUpdate chan struct{}
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

	env, err := m.launchPreparedEnv(in.Name, in.Env, in.EnvComplete, in.StripOTelEnv, in.EnvAllow)
	if err != nil {
		return nil, fmt.Errorf("dispatch: launch env: %w", err)
	}
	stdin := dispatchStdin(rt, in)
	proc, err := m.spawner(args, env, in.Workspace, logPath, logPath, stdin)
	if err != nil {
		return nil, fmt.Errorf("dispatch: spawn: %w", err)
	}

	now := time.Now().UTC()
	meta := &Metadata{
		Instance:      in.Name,
		Agent:         in.Agent,
		Job:           in.Job,
		Ticket:        in.Ticket,
		Branch:        in.Branch,
		PR:            in.PR,
		Origin:        in.Origin,
		Runtime:       string(rt.Kind),
		RuntimeBinary: rt.Binary,
		Workspace:     in.Workspace,
		PID:           proc.Pid,
		SessionID:     sessionID,
		StartedAt:     now,
		Status:        StatusRunning,
		LogPath:       logPath,
	}
	applyRuntimeBudgetMetadata(meta, now, in.Budget)
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
	watchdogUpdate := newWatchdogUpdateChannel(in.Budget)
	m.mu.Lock()
	m.instances[in.Name] = &tracked{meta: meta, process: proc, watchdogUpdate: watchdogUpdate, reaped: reaped}
	m.mu.Unlock()
	out := *meta
	m.recordEvent("dispatch", &out, "instance dispatched")
	capture := m.startCodexSessionCapture(rt.Kind, out)
	m.startBudgetNoticeWatcher(out, proc, reaped)
	go m.reap(in.Name, proc, reaped)
	if watchdogUpdate != nil {
		go m.watchdog(in.Name, proc, reaped, watchdogUpdate)
	}
	if captured := waitForCodexSessionCapture(capture); captured != nil {
		out = *captured
	}
	return &out, nil
}

func applyRuntimeBudgetMetadata(meta *Metadata, now time.Time, budget time.Duration) {
	if meta == nil || budget <= 0 {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	meta.RuntimeBudget = budget.String()
	meta.RuntimeDeadline = now.Add(budget).UTC()
}

func newWatchdogUpdateChannel(budget time.Duration) chan struct{} {
	if budget <= 0 {
		return nil
	}
	return make(chan struct{}, 1)
}

type RuntimeBudgetExtension struct {
	Metadata         *Metadata     `json:"metadata"`
	By               time.Duration `json:"by"`
	PreviousDeadline time.Time     `json:"previous_deadline"`
	NewDeadline      time.Time     `json:"new_deadline"`
	Actor            string        `json:"actor,omitempty"`
}

func (m *InstanceManager) ExtendRuntimeBudget(instance string, by time.Duration, actor string) (*RuntimeBudgetExtension, error) {
	instance = strings.TrimSpace(instance)
	if instance == "" {
		return nil, errors.New("extend: instance is required")
	}
	if by <= 0 {
		return nil, errors.New("extend: --by must be > 0")
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		actor = "cli"
	}

	m.mu.Lock()
	t, ok := m.instances[instance]
	if !ok || t == nil || t.meta == nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("extend: unknown instance %q", instance)
	}
	if t.meta.Status != StatusRunning {
		status := t.meta.Status
		m.mu.Unlock()
		return nil, fmt.Errorf("extend: instance %q is not running (status=%s)", instance, status)
	}
	if t.process == nil || t.watchdogUpdate == nil || strings.TrimSpace(t.meta.RuntimeBudget) == "" || t.meta.RuntimeDeadline.IsZero() {
		m.mu.Unlock()
		return nil, fmt.Errorf("extend: instance %q has no armed watchdog", instance)
	}
	previousDeadline := t.meta.RuntimeDeadline.UTC()
	newDeadline := previousDeadline.Add(by).UTC()
	t.meta.RuntimeDeadline = newDeadline
	if !t.meta.StartedAt.IsZero() {
		if budget := newDeadline.Sub(t.meta.StartedAt.UTC()); budget > 0 {
			t.meta.RuntimeBudget = budget.String()
		}
	}
	out := *t.meta
	update := t.watchdogUpdate
	if err := WriteMetadata(m.daemonRoot, t.meta); err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("extend: persist metadata: %w", err)
	}
	select {
	case update <- struct{}{}:
	default:
	}
	m.mu.Unlock()

	m.recordEvent("extended", &out, fmt.Sprintf("runtime budget extended by %s by %s", by, actor))
	return &RuntimeBudgetExtension{
		Metadata:         &out,
		By:               by,
		PreviousDeadline: previousDeadline,
		NewDeadline:      newDeadline,
		Actor:            actor,
	}, nil
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
	opts := runtimebin.ResolveOptions{
		Explicit: runtimebin.Fields{Kind: in.Runtime, Binary: in.RuntimeBinary},
	}
	if agent := m.agentForRuntime(in.Agent); agent != nil {
		opts.Agent = runtimebin.Fields{Name: agent.Name, Kind: agent.Runtime, Binary: agent.RuntimeBin}
	}
	return runtimebin.Resolve(opts)
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
			return append([]string{rt.Binary}, codexExecArgsWithJSON(in.Args)...), nil
		}
		if strings.TrimSpace(in.Prompt) == "" {
			return nil, errors.New("codex daemon dispatch requires exec args or a prompt")
		}
		codexArgs := []string{rt.Binary, "exec", "--json"}
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

func codexExecArgsWithJSON(args []string) []string {
	out := append([]string(nil), args...)
	if len(out) == 0 || out[0] != "exec" || hasArg(out[1:], "--json") {
		return out
	}
	return append([]string{"exec", "--json"}, out[1:]...)
}

func hasArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func (m *InstanceManager) startCodexSessionCapture(kind runtimebin.Kind, meta Metadata) <-chan *Metadata {
	if kind != runtimebin.KindCodex || strings.TrimSpace(meta.LogPath) == "" {
		return nil
	}
	result := make(chan *Metadata, 1)
	go func() {
		defer close(result)
		sessionID, err := waitForCodexThreadStarted(meta.LogPath, codexSessionCaptureTimeout)
		if err != nil || strings.TrimSpace(sessionID) == "" {
			return
		}
		updated, ok := m.recordCodexSessionID(meta, sessionID)
		if !ok {
			return
		}
		result <- updated
	}()
	return result
}

func waitForCodexSessionCapture(capture <-chan *Metadata) *Metadata {
	if capture == nil {
		return nil
	}
	timer := time.NewTimer(codexSessionCaptureInitialWait)
	defer timer.Stop()
	select {
	case meta := <-capture:
		return meta
	case <-timer.C:
		return nil
	}
}

func (m *InstanceManager) recordCodexSessionID(base Metadata, sessionID string) (*Metadata, bool) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, false
	}
	m.mu.Lock()
	t := m.instances[base.Instance]
	if t == nil || !sameTrackedIncarnation(t, &base) || t.meta.SessionID != "" {
		m.mu.Unlock()
		return nil, false
	}
	updated := *t.meta
	updated.SessionID = sessionID
	if err := WriteMetadata(m.daemonRoot, &updated); err != nil {
		m.mu.Unlock()
		return nil, false
	}
	t.meta = &updated
	out := updated
	eventMeta := updated
	m.mu.Unlock()
	m.recordEvent("session_capture", &eventMeta, "codex thread id captured")
	return &out, true
}

func waitForCodexThreadStarted(logPath string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var offset int64
	for {
		sessionID, nextOffset, err := readCodexThreadStartedFromLog(logPath, offset)
		offset = nextOffset
		if sessionID != "" || (err != nil && !errors.Is(err, fs.ErrNotExist)) {
			return sessionID, err
		}
		if timeout <= 0 || !time.Now().Before(deadline) {
			return "", nil
		}
		time.Sleep(codexSessionCapturePoll)
	}
}

func readCodexThreadStartedFromLog(logPath string, offset int64) (string, int64, error) {
	f, err := os.Open(logPath)
	if err != nil {
		return "", offset, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", offset, err
	}
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadString('\n')
		complete := err != io.EOF || strings.HasSuffix(line, "\n")
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && (complete || json.Valid([]byte(trimmed))) {
			offset += int64(len(line))
			if sessionID := codexThreadIDFromJSONLine(trimmed); sessionID != "" {
				return sessionID, offset, nil
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return "", offset, nil
		}
		return "", offset, err
	}
}

func codexThreadIDFromJSONLine(line string) string {
	var event struct {
		Type     string `json:"type"`
		ThreadID string `json:"thread_id"`
	}
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return ""
	}
	if event.Type != "thread.started" {
		return ""
	}
	return strings.TrimSpace(event.ThreadID)
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
		return m.StartWithOptions(instance, opts.StartOptions)
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
	meta, err := m.StartWithOptions(instance, opts.StartOptions)
	if err != nil {
		return nil, err
	}
	m.recordEvent("restart", meta, "instance restarted")
	return meta, nil
}

func (m *InstanceManager) Interrupt(instance string, opts InterruptOptions) (*InterruptResult, error) {
	instance = strings.TrimSpace(instance)
	if instance == "" {
		return nil, errors.New("interrupt: instance is required")
	}
	body := strings.TrimSpace(opts.Body)
	if body == "" {
		return nil, errors.New("interrupt: message body is required")
	}
	if opts.Timeout < 0 {
		return nil, errors.New("interrupt: timeout must be >= 0")
	}
	if opts.Timeout == 0 {
		opts.Timeout = 10 * time.Second
	}
	if err := m.ensureTracked(instance, nil); err != nil {
		return nil, errors.New(strings.Replace(err.Error(), "start:", "interrupt:", 1))
	}

	m.mu.Lock()
	t := m.instances[instance]
	if t == nil || t.meta == nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("interrupt: unknown instance %q", instance)
	}
	base := *t.meta
	running := base.Status == StatusRunning
	m.mu.Unlock()

	if err := m.interruptResumePreflight(instance, base, opts.Force); err != nil {
		return nil, err
	}

	from := strings.TrimSpace(opts.From)
	if from == "" {
		from = "(cli)"
	}
	msg := &Message{
		From: from,
		Body: body,
		TS:   time.Now().UTC(),
	}
	if err := AppendMessage(m.daemonRoot, instance, msg); err != nil {
		return nil, fmt.Errorf("interrupt: mailbox: %w", err)
	}

	startOpts := StartOptions{
		DisallowFreshFallback:    !opts.Force,
		AllowFreshWithoutSession: opts.Force,
	}
	delivered := 0
	truncated := false
	if metadataRuntimeKind(&base) == runtimebin.KindCodex {
		prompt, count, wasTruncated, err := m.codexInterruptResumePrompt(instance)
		if err != nil {
			return nil, err
		}
		startOpts.ResumePrompt = prompt
		delivered = count
		truncated = wasTruncated
	}

	var meta *Metadata
	var err error
	if running {
		meta, err = m.RestartWithOptions(instance, RestartOptions{
			Timeout:      opts.Timeout,
			StartOptions: startOpts,
		})
	} else {
		meta, err = m.StartWithOptions(instance, startOpts)
	}
	if err != nil {
		return nil, fmt.Errorf("interrupt: resume: %w", err)
	}
	m.recordEvent("interrupted", meta, fmt.Sprintf("interrupt delivered message %s and resumed session", msg.ID))
	return &InterruptResult{
		Message:   msg,
		Metadata:  meta,
		Delivered: delivered,
		Truncated: truncated,
	}, nil
}

func (m *InstanceManager) interruptResumePreflight(instance string, base Metadata, force bool) error {
	kind := metadataRuntimeKind(&base)
	if !runtimeKindSupportsManagedResume(kind) {
		return fmt.Errorf("interrupt: runtime %q does not support managed resume", kind)
	}
	if strings.TrimSpace(base.Workspace) == "" {
		return fmt.Errorf("interrupt: %q has no workspace recorded; cannot resume", instance)
	}
	if strings.TrimSpace(base.SessionID) == "" {
		if force {
			return nil
		}
		return fmt.Errorf("interrupt: %q has no session_id; use --force to allow a fresh fallback", instance)
	}
	env, err := m.startEnv(instance)
	if err != nil {
		return fmt.Errorf("interrupt: launch env: %w", err)
	}
	if err := managedResumePreflight(base, env); err != nil {
		if force {
			return nil
		}
		return fmt.Errorf("interrupt: managed resume preflight failed: %w; use --force to allow a fresh fallback", err)
	}
	return nil
}

func (m *InstanceManager) codexInterruptResumePrompt(instance string) (string, int, bool, error) {
	unread, err := ReadUnacked(m.daemonRoot, instance)
	if err != nil {
		return "", 0, false, fmt.Errorf("interrupt: read mailbox: %w", err)
	}
	section, delivered, truncated, cursor := formatKickoffMailbox(unread, kickoffMailboxMaxBytes)
	if delivered == 0 {
		return "Interrupted by agent-teamd. Run `inbox check` for pending messages, then continue your work.", 0, false, nil
	}
	if err := WriteCursor(m.daemonRoot, instance, cursor); err != nil {
		return "", 0, false, fmt.Errorf("interrupt: advance mailbox cursor: %w", err)
	}
	return section, delivered, truncated, nil
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

// Start resumes a previously-stopped persistent instance. It re-spawns the
// recorded runtime with its managed resume command. Ephemeral instances cannot
// be resumed; the caller is expected not to ask. (We don't track
// ephemeral-vs-persistent here — the agent's frontmatter gates that, and
// SQU-29 wires it in.)
func (m *InstanceManager) Start(instance string) (*Metadata, error) {
	return m.StartWithOptions(instance, StartOptions{})
}

func (m *InstanceManager) StartWithOptions(instance string, opts StartOptions) (*Metadata, error) {
	return m.start(instance, nil, opts)
}

func (m *InstanceManager) start(instance string, expected *Metadata, opts StartOptions) (*Metadata, error) {
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
	if !runtimeKindSupportsManagedResume(baseRuntime) {
		m.mu.Unlock()
		return nil, fmt.Errorf("start: runtime %q does not support managed resume; create a new run instead", baseRuntime)
	}
	if base.SessionID == "" {
		if opts.AllowFreshWithoutSession {
			m.mu.Unlock()
			return m.resumeFallbackFresh(instance, &base, errors.New("no session_id; cannot resume"), opts.ResumePrompt)
		}
		m.mu.Unlock()
		return nil, fmt.Errorf("start: %q has no session_id; cannot resume", instance)
	}
	env, otelCodexArgs, err := m.startEnvWithOTelArgs(instance)
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("start: launch env: %w", err)
	}
	if err := managedResumePreflight(base, env); err != nil {
		if opts.DisallowFreshFallback {
			m.mu.Unlock()
			return nil, fmt.Errorf("start: resume preflight failed: %w", err)
		}
		m.mu.Unlock()
		return m.resumeFallbackFresh(instance, &base, err, opts.ResumePrompt)
	}
	brief, err := InstanceBriefLaunchText(filepath.Dir(m.daemonRoot), instance)
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("start: brief: %w", err)
	}
	stdin := ""
	if brief != "" && baseRuntime == runtimebin.KindClaude {
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
	if baseRuntime == runtimebin.KindCodex {
		stdin = codexManagedResumeStdin(brief, opts.ResumePrompt)
	}

	logPath := base.LogPath
	if logPath == "" {
		logPath = filepath.Join(instanceDir(m.daemonRoot, instance), "child.log")
	}
	bin := strings.TrimSpace(base.RuntimeBinary)
	if bin == "" {
		bin = runtimebin.DefaultBinaryForKind(baseRuntime)
	}
	args := managedResumeArgs(baseRuntime, bin, base.SessionID)
	if baseRuntime == runtimebin.KindCodex && len(otelCodexArgs) > 0 {
		// Codex exporter selection/config live in argv, not env — a resumed
		// child needs the CURRENT config's -c otel.* args like a dispatch does.
		args = append(append([]string(nil), args[:2]...), append(append([]string(nil), otelCodexArgs...), args[2:]...)...)
	}
	proc, err := m.spawner(args, env, base.Workspace, logPath, logPath, stdin)
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
	meta.ResumeCount++
	meta.FreshFallback = false
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

	out := meta
	m.recordEvent("start", &out, "instance resumed")
	go m.reap(instance, proc, reaped)
	return &out, nil
}

func runtimeKindSupportsManagedResume(kind runtimebin.Kind) bool {
	return kind == runtimebin.KindClaude || kind == runtimebin.KindCodex
}

func managedResumeArgs(kind runtimebin.Kind, bin, sessionID string) []string {
	if kind == runtimebin.KindCodex {
		return []string{bin, "exec", "resume", sessionID, "-"}
	}
	return []string{bin, "--resume", sessionID}
}

func codexManagedResumeStdin(brief, resumePrompt string) string {
	parts := make([]string, 0, 2)
	if strings.TrimSpace(brief) != "" {
		parts = append(parts, brief)
	}
	if strings.TrimSpace(resumePrompt) != "" {
		parts = append(parts, resumePrompt)
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n\n")
	}
	// `codex exec resume <id> -` requires a non-empty stdin prompt; an empty
	// brief (ad-hoc instance, no daemon-owned state yet) must not fail resume.
	return "Resumed by agent-teamd. Run `inbox check` for pending messages, then continue your work."
}

func managedResumePreflight(meta Metadata, env []string) error {
	workspace := strings.TrimSpace(meta.Workspace)
	if workspace == "" {
		return errors.New("no workspace recorded for managed resume")
	}
	st, err := os.Stat(workspace)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("workspace %s does not exist", workspace)
		}
		return fmt.Errorf("stat workspace %s: %w", workspace, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("workspace %s is not a directory", workspace)
	}
	if metadataRuntimeKind(&meta) != runtimebin.KindCodex {
		return nil
	}
	ok, root, err := codexSessionRolloutExists(meta.SessionID, env)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("codex session rollout %q not found under %s", meta.SessionID, root)
	}
	return nil
}

func codexSessionRolloutExists(sessionID string, env []string) (bool, string, error) {
	sessionID = strings.TrimSpace(sessionID)
	root := codexSessionsRoot(env)
	if sessionID == "" {
		return false, root, nil
	}
	found := false
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fs.SkipDir
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.Contains(filepath.Base(path), sessionID) {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return false, root, nil
	}
	return found, root, err
}

func codexSessionsRoot(env []string) string {
	if codexHome := envValue(env, "CODEX_HOME"); codexHome != "" {
		return filepath.Join(codexHome, "sessions")
	}
	if home := envValue(env, "HOME"); home != "" {
		return filepath.Join(home, ".codex", "sessions")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".codex", "sessions")
	}
	return filepath.Join(".codex", "sessions")
}

func envValue(env []string, key string) string {
	prefix := key + "="
	value := ""
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			value = strings.TrimPrefix(entry, prefix)
		}
	}
	return strings.TrimSpace(value)
}

func (m *InstanceManager) resumeFallbackFresh(instance string, base *Metadata, cause error, extraPrompt string) (*Metadata, error) {
	teamDir := filepath.Dir(m.daemonRoot)
	topo, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return nil, fmt.Errorf("start: resume preflight failed (%v); load topology for fallback: %w", cause, err)
	}
	if topo == nil {
		return nil, fmt.Errorf("start: resume preflight failed (%v); topology not configured for fallback", cause)
	}
	inst := topo.Find(instance)
	if inst == nil || inst.Ephemeral {
		return nil, fmt.Errorf("start: resume preflight failed (%v); %q is not a declared persistent instance", cause, instance)
	}
	if base != nil {
		m.recordEvent("resume_fallback", base, fmt.Sprintf("managed resume preflight failed; launching fresh: %v", cause))
	}
	meta, launched, err := launchDeclaredFreshWithPrompt(teamDir, m, topo, inst, base, extraPrompt)
	if err != nil {
		return nil, fmt.Errorf("start: resume fallback: %w", err)
	}
	if launched && base != nil {
		return m.markFreshFallbackMetadata(instance, base, meta)
	}
	return meta, nil
}

func (m *InstanceManager) markFreshFallbackMetadata(instance string, base, meta *Metadata) (*Metadata, error) {
	if meta == nil {
		return nil, nil
	}
	updated := *meta
	updated.ResumeCount = base.ResumeCount + 1
	updated.FreshFallback = true
	updated.FreshFallbacks = base.FreshFallbacks + 1
	if updated.Job == "" {
		updated.Job = base.Job
	}
	if updated.Ticket == "" {
		updated.Ticket = base.Ticket
	}
	if updated.Branch == "" {
		updated.Branch = base.Branch
	}
	if updated.PR == "" {
		updated.PR = base.PR
	}

	m.mu.Lock()
	if t := m.instances[instance]; t != nil && sameTrackedIncarnation(t, &updated) {
		t.meta = &updated
	}
	if err := WriteMetadata(m.daemonRoot, &updated); err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("start: persist fresh fallback metadata: %w", err)
	}
	m.mu.Unlock()
	return &updated, nil
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
	env, err := m.launchPreparedEnv(in.Name, in.Env, in.EnvComplete, in.StripOTelEnv, in.EnvAllow)
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
		Job:           in.Job,
		Ticket:        in.Ticket,
		Branch:        in.Branch,
		PR:            in.PR,
		Origin:        in.Origin,
		Runtime:       string(rt.Kind),
		RuntimeBinary: rt.Binary,
		Workspace:     in.Workspace,
		PID:           proc.Pid,
		SessionID:     sessionID,
		StartedAt:     now,
		Status:        StatusRunning,
		LogPath:       logPath,
	}
	applyRuntimeBudgetMetadata(meta, now, in.Budget)
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
	watchdogUpdate := newWatchdogUpdateChannel(in.Budget)
	m.instances[in.Name] = &tracked{meta: meta, process: proc, watchdogUpdate: watchdogUpdate, reaped: reaped}
	m.mu.Unlock()

	out := *meta
	m.recordEvent("dispatch", &out, "instance dispatched")
	capture := m.startCodexSessionCapture(rt.Kind, out)
	m.startBudgetNoticeWatcher(out, proc, reaped)
	go m.reap(in.Name, proc, reaped)
	if watchdogUpdate != nil {
		go m.watchdog(in.Name, proc, reaped, watchdogUpdate)
	}
	if captured := waitForCodexSessionCapture(capture); captured != nil {
		meta = captured
	}
	return meta, true, nil
}

func (m *InstanceManager) startEnv(instance string) ([]string, error) {
	env, _, err := m.startEnvWithOTelArgs(instance)
	return env, err
}

func (m *InstanceManager) startEnvWithOTelArgs(instance string) ([]string, []string, error) {
	env, ok, err := m.instanceLaunchEnv(instance)
	if err != nil {
		return nil, nil, err
	}
	if ok {
		out, codexArgs := m.applyCurrentOTelConfigWithArgs(instance, env)
		return out, codexArgs, nil
	}
	return os.Environ(), nil, nil
}

// applyCurrentOTelConfig reconciles a persisted launch env with the repo's
// CURRENT [otel] config before a managed resume. A snapshot from an earlier
// enabled launch must not replay telemetry vars after [otel] is disabled or
// changed — the current config alone decides what the resumed child sees. No
// [otel] table keeps legacy passthrough, mirroring dispatch semantics.
func (m *InstanceManager) applyCurrentOTelConfig(instance string, env []string) []string {
	out, _ := m.applyCurrentOTelConfigWithArgs(instance, env)
	return out
}

// applyCurrentOTelConfigWithArgs additionally returns the Codex `-c otel.*`
// args the current config requires, so managed Codex resume can attach the
// exporter configuration that lives in argv rather than env.
func (m *InstanceManager) applyCurrentOTelConfigWithArgs(instance string, env []string) ([]string, []string) {
	teamDir := filepath.Dir(m.daemonRoot)
	tree, err := teamtemplate.LoadTOMLFile(filepath.Join(teamDir, "config.toml"))
	if err != nil {
		return env, nil
	}
	cfg, err := runtimeotel.FromTree(tree)
	if err != nil || !cfg.Configured() {
		return env, nil
	}
	env = runtimeotel.StripOwnedEnv(env)
	if !cfg.Enabled {
		return env, nil
	}
	meta, err := ReadMetadata(m.daemonRoot, instance)
	if err != nil {
		return env, nil
	}
	launch, err := runtimeotel.BuildLaunch(cfg, metadataRuntimeKind(meta), runtimeotel.Context{
		Agent:    meta.Agent,
		Instance: meta.Instance,
		JobID:    meta.Job,
		Ticket:   meta.Ticket,
		Branch:   meta.Branch,
		Runtime:  meta.Runtime,
		Build:    buildinfo.Current(""),
	})
	if err != nil {
		return env, nil
	}
	return append(env, launch.Env...), launch.CodexArgs
}

func (m *InstanceManager) launchPreparedEnv(instance string, overlay []string, complete, stripOTel bool, envAllow []string) ([]string, error) {
	if complete {
		return filterEnvAllow(append([]string(nil), overlay...), envAllow)
	}
	env, ok, err := m.instanceLaunchEnv(instance)
	if err != nil {
		return nil, err
	}
	if ok {
		if stripOTel {
			env = runtimeotel.StripOwnedEnv(env)
		}
		// The snapshot is the base, never the whole story: the caller's
		// overlay carries the freshly generated dispatch context (current
		// AGENT_TEAM_*, TRACEPARENT, exporter env). Appending after the
		// snapshot lets current values win on duplicate keys.
		return filterEnvAllow(mergeEnv(env, overlay), envAllow)
	}
	env = os.Environ()
	if stripOTel {
		env = runtimeotel.StripOwnedEnv(env)
	}
	return filterEnvAllow(mergeEnv(env, overlay), envAllow)
}

func mergeEnv(base, overlay []string) []string {
	out := make([]string, 0, len(base)+len(overlay))
	index := map[string]int{}
	add := func(entry string) {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			out = append(out, entry)
			return
		}
		if i, exists := index[key]; exists {
			out[i] = entry
			return
		}
		index[key] = len(out)
		out = append(out, entry)
	}
	for _, entry := range base {
		add(entry)
	}
	for _, entry := range overlay {
		add(entry)
	}
	return out
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
	usageCaptureErr := captureUsageForMetadata(t.meta, now)
	meta := *t.meta
	m.mu.Unlock()
	if err := WriteMetadata(m.daemonRoot, &meta); err == nil {
		if usageCaptureErr != nil {
			m.recordEvent("usage_capture_failed", &meta, usageCaptureErr.Error())
		} else if usageErr := persistMetadataUsageToJob(m.daemonRoot, &meta); usageErr != nil {
			m.recordEvent("usage_capture_failed", &meta, usageErr.Error())
		}
		m.recordEvent("exit", &meta, "reconciled missing process")
		m.notifyTerminal(&meta)
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
	eventMessage := "instance process exited"

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
	usageCaptureErr := captureUsageForMetadata(t.meta, now)
	if err := WriteMetadata(m.daemonRoot, t.meta); err != nil {
		// Reaper has nowhere to surface this; the next reconcile will catch
		// any drift. Don't block the goroutine.
		_ = err
	}
	hook := m.reapHook
	eventMeta := *t.meta
	m.mu.Unlock()
	if usageCaptureErr != nil {
		m.recordEvent("usage_capture_failed", &eventMeta, usageCaptureErr.Error())
	} else if usageErr := persistMetadataUsageToJob(m.daemonRoot, &eventMeta); usageErr != nil {
		m.recordEvent("usage_capture_failed", &eventMeta, usageErr.Error())
	}
	// Fast runtimes can exit before the live watcher reaches its first tick.
	// Run one final sweep after usage capture and before `reaped` closes so
	// terminal token crossings are still durable. Hard cutoffs remain absolute:
	// if a child exits cleanly after crossing the hard line before the poller saw
	// it, classify the incarnation as a watchdog crash before the reap hook
	// reconciles the job.
	if cutoff, err := m.checkBudgetThresholds(eventMeta, now); err == nil && cutoff != nil && eventMeta.Status != StatusCrashed {
		if updated, ok := m.markReapedInstanceCrashedForBudgetCutoff(instance, proc, *cutoff); ok {
			eventMeta = *updated
			eventAction = "watchdog"
			eventMessage = budgetHardCutoffMessage(eventMeta.Job, eventMeta.Instance, *cutoff)
		}
	}
	if eventAction != "" {
		m.recordEvent(eventAction, &eventMeta, eventMessage)
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
// exit and fires the reap hook, so eligible read-only pipeline steps can use
// the daemon's retry_on_crash policy.
//
// The reaper remains the SOLE finaliser that fires the hook: the watchdog only
// pre-marks status and kills, so the pipeline still advances exactly once. A
// non-positive budget disables the watchdog (the default).
func (m *InstanceManager) watchdog(instance string, proc *os.Process, reaped <-chan struct{}, updates <-chan struct{}) {
	if proc == nil || updates == nil {
		return
	}
	for {
		deadline, ok := m.watchdogDeadline(instance, proc)
		if !ok {
			return
		}
		if wait := time.Until(deadline); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-reaped:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				// Exited on its own within budget — nothing to enforce.
				return
			case <-updates:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				continue
			case <-timer.C:
			}
		}

		// Budget elapsed. Re-validate under the lock that THIS process is still the
		// live, running incarnation before touching anything: the reaper may have
		// just finalised it, a stop may have set Stopped, an operator may have
		// extended the deadline, or a newer dispatch may have replaced it. Any of
		// those → no-op/loop (the watchdog never double-kills).
		m.mu.Lock()
		t, ok := m.instances[instance]
		if !ok || t.meta == nil || t.process != proc || t.meta.Status != StatusRunning {
			m.mu.Unlock()
			return
		}
		if t.meta.RuntimeDeadline.After(time.Now().UTC()) {
			m.mu.Unlock()
			continue
		}
		pid := t.meta.PID
		budget := strings.TrimSpace(t.meta.RuntimeBudget)
		if budget == "" && !t.meta.StartedAt.IsZero() && !t.meta.RuntimeDeadline.IsZero() {
			budget = t.meta.RuntimeDeadline.Sub(t.meta.StartedAt).String()
		}
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
		return
	}
}

func (m *InstanceManager) watchdogDeadline(instance string, proc *os.Process) (time.Time, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.instances[instance]
	if !ok || t.meta == nil || t.process != proc || t.meta.Status != StatusRunning {
		return time.Time{}, false
	}
	if t.meta.RuntimeDeadline.IsZero() {
		return time.Time{}, false
	}
	return t.meta.RuntimeDeadline, true
}

// SetReapHook installs (or replaces) a callback invoked after each reaper
// finalises an instance. Pass nil to clear. The hook runs outside the
// manager's lock, so callbacks may safely call into the manager.
func (m *InstanceManager) SetReapHook(fn func(instance string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reapHook = fn
}

// SetTerminalHook installs (or replaces) a callback invoked when reconcile
// finalises terminal metadata without a live reaper.
func (m *InstanceManager) SetTerminalHook(fn func(*Metadata)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.terminalHook = fn
}

func (m *InstanceManager) notifyTerminal(meta *Metadata) {
	if meta == nil {
		return
	}
	m.mu.Lock()
	hook := m.terminalHook
	m.mu.Unlock()
	if hook == nil {
		return
	}
	copyMeta := *meta
	hook(&copyMeta)
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
		Origin:   meta.Origin,
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
	sessionIDRand.RLock()
	reader := sessionIDRand.reader
	_, err := io.ReadFull(reader, b[:])
	sessionIDRand.RUnlock()
	if err != nil {
		return fallbackSessionIDBytes()
	}
	return formatSessionIDBytes(b)
}

func setSessionIDRandReaderForTest(reader io.Reader) func() {
	if reader == nil {
		reader = rand.Reader
	}
	sessionIDRand.Lock()
	prev := sessionIDRand.reader
	sessionIDRand.reader = reader
	sessionIDRand.Unlock()
	return func() {
		sessionIDRand.Lock()
		sessionIDRand.reader = prev
		sessionIDRand.Unlock()
	}
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
