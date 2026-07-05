package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jamesaud/agent-team/internal/buildinfo"
	"github.com/jamesaud/agent-team/internal/runtimeotel"
	"github.com/jamesaud/agent-team/internal/topology"
)

// PidLiveCheck reports whether the given PID is alive on this system. We use
// the standard `kill(pid, 0)` probe: signal 0 doesn't deliver, but the kernel
// runs its usual permission/existence checks and returns ESRCH if the process
// is gone. EPERM means alive but owned by another user — for our purposes,
// alive (the daemon writes the metadata, so EPERM should never apply
// normally).
func PidLiveCheck(pid int) bool {
	pidLiveCheckMu.RLock()
	check := pidLiveCheck
	pidLiveCheckMu.RUnlock()
	return check(pid)
}

var (
	pidLiveCheckMu sync.RWMutex
	pidLiveCheck   = defaultPidLiveCheck
)

func defaultPidLiveCheck(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// On unix, ESRCH = no such process. EPERM = alive, different uid.
	if errors.Is(err, syscall.EPERM) {
		return true
	}
	return false
}

func SetPidLiveCheckForTest(check func(pid int) bool) func() {
	if check == nil {
		check = defaultPidLiveCheck
	}
	pidLiveCheckMu.Lock()
	prev := pidLiveCheck
	pidLiveCheck = check
	pidLiveCheckMu.Unlock()
	return func() {
		pidLiveCheckMu.Lock()
		pidLiveCheck = prev
		pidLiveCheckMu.Unlock()
	}
}

const restartBackoffCap = 5 * time.Minute

var (
	restartBackoffDelay = 5 * time.Second
	adoptedPollInterval = time.Second
)

// Reconcile walks on-disk metadata and reconciles each entry against the live
// process table. This is the crash-only design from `documentation/orchestrator.md`
// Open Q #3: we don't try to auto-restart anything; we surface accurate state
// so callers (humans, /v1/instances) can decide.
//
// Outcomes per record:
//
//   - status was running, PID alive   -> stays running (adopted; the daemon
//     can't Wait() on a process it didn't fork, so we just track the
//     metadata; the user must explicitly stop or remove)
//   - status was running, PID gone    -> mark exited (ExitedAt = now)
//   - status was stopped              -> leave as stopped
//   - status was exited / crashed     -> leave as-is
//
// Returns the updated set so the caller can populate the in-memory map.
func Reconcile(daemonRoot string, m *InstanceManager) error {
	return reconcileCrashOnly(daemonRoot, m, "", nil)
}

// ReconcileWithTopology performs the crash-only reconcile pass, then applies
// declared restart policy for persistent instances. The legacy Reconcile entry
// point intentionally remains crash-only so restart=never keeps the old
// behavior and tests/callers that do not opt into topology are unchanged.
func ReconcileWithTopology(teamDir string, m *InstanceManager, topo *topology.Topology) error {
	daemonRoot := DaemonRoot(teamDir)
	if err := reconcileCrashOnly(daemonRoot, m, teamDir, topo); err != nil {
		return err
	}
	return reconcileDesiredState(teamDir, m, topo, time.Now().UTC())
}

func reconcileCrashOnly(daemonRoot string, m *InstanceManager, teamDir string, topo *topology.Topology) error {
	all, err := ListMetadata(daemonRoot)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	var terminal []*Metadata
	for _, md := range all {
		switch md.Status {
		case StatusRunning:
			if PidLiveCheck(md.PID) {
				// Adopt: keep status=running. The optional adopted-child
				// watcher installed below restores prompt terminal
				// observation for daemon-owned runs after a daemon restart.
				continue
			}
			md.Status = StatusExited
			md.ExitedAt = now
			usageCaptureErr := captureUsageForMetadata(md, now)
			if err := WriteMetadata(daemonRoot, md); err != nil {
				return err
			}
			if usageCaptureErr != nil {
				_ = AppendLifecycleEvent(daemonRoot, &LifecycleEvent{
					Action:   "usage_capture_failed",
					Instance: md.Instance,
					Agent:    md.Agent,
					Job:      md.Job,
					Ticket:   md.Ticket,
					Branch:   md.Branch,
					PR:       md.PR,
					Status:   md.Status,
					PID:      md.PID,
					ExitCode: md.ExitCode,
					Message:  usageCaptureErr.Error(),
				})
			} else if err := persistMetadataUsageToJob(daemonRoot, md); err != nil {
				_ = AppendLifecycleEvent(daemonRoot, &LifecycleEvent{
					Action:   "usage_capture_failed",
					Instance: md.Instance,
					Agent:    md.Agent,
					Job:      md.Job,
					Ticket:   md.Ticket,
					Branch:   md.Branch,
					PR:       md.PR,
					Status:   md.Status,
					PID:      md.PID,
					ExitCode: md.ExitCode,
					Message:  err.Error(),
				})
			}
			_ = AppendLifecycleEvent(daemonRoot, &LifecycleEvent{
				Action:   "exit",
				Instance: md.Instance,
				Agent:    md.Agent,
				Job:      md.Job,
				Ticket:   md.Ticket,
				Branch:   md.Branch,
				PR:       md.PR,
				Status:   md.Status,
				PID:      md.PID,
				ExitCode: md.ExitCode,
				Message:  "reconciled missing process",
			})
			out := *md
			terminal = append(terminal, &out)
		case StatusStopped, StatusExited, StatusCrashed:
			// Nothing to reconcile.
		default:
			// Unknown status — leave it.
		}
	}
	// Repopulate the manager's in-memory map.
	m.mu.Lock()
	for _, md := range all {
		// Re-read after possible status update.
		fresh, err := ReadMetadata(daemonRoot, md.Instance)
		if err != nil {
			continue
		}
		if existing := m.instances[fresh.Instance]; existing != nil && sameTrackedIncarnation(existing, fresh) {
			meta := *fresh
			existing.meta = &meta
			if existing.meta.Status == StatusRunning {
				m.installAdoptedWatcherLocked(existing, teamDir, topo)
			}
			continue
		}
		meta := *fresh
		t := &tracked{meta: &meta}
		m.instances[fresh.Instance] = t
		if t.meta.Status == StatusRunning {
			m.installAdoptedWatcherLocked(t, teamDir, topo)
		}
	}
	m.mu.Unlock()
	for _, md := range terminal {
		m.notifyTerminal(md)
	}
	return nil
}

func reconcileDesiredState(teamDir string, m *InstanceManager, topo *topology.Topology, now time.Time) error {
	if strings.TrimSpace(teamDir) == "" || topo == nil {
		return nil
	}
	daemonRoot := DaemonRoot(teamDir)
	for _, inst := range topo.SortedInstances() {
		if inst == nil || inst.Ephemeral || inst.Restart == topology.RestartNever {
			continue
		}
		meta, err := ReadMetadata(daemonRoot, inst.Name)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				meta = nil
			} else {
				return err
			}
		}
		if !restartPolicyWantsLaunch(inst.Restart, meta) {
			continue
		}
		if meta != nil && meta.RestartBackoffUntil.After(now) {
			continue
		}
		if _, _, err := relaunchDeclaredInstance(teamDir, m, topo, inst, meta); err != nil {
			if berr := persistRestartBackoff(teamDir, inst, meta, now, err); berr != nil {
				return berr
			}
			continue
		}
	}
	return nil
}

func restartPolicyWantsLaunch(policy string, meta *Metadata) bool {
	switch policy {
	case topology.RestartAlways:
		if meta == nil {
			return true
		}
		return meta.Status == StatusExited || meta.Status == StatusCrashed
	case topology.RestartOnFailure:
		if meta == nil {
			return true
		}
		if meta.Status == StatusCrashed {
			return true
		}
		if meta.Status != StatusExited {
			return false
		}
		return meta.ExitCode == nil || *meta.ExitCode != 0
	default:
		return false
	}
}

func relaunchDeclaredInstance(teamDir string, m *InstanceManager, topo *topology.Topology, inst *topology.Instance, meta *Metadata) (*Metadata, bool, error) {
	if managedResumeSupported(meta) {
		out, err := m.start(inst.Name, meta, StartOptions{})
		installWatcherForRevivedAdoption(m, out, teamDir, topo)
		return out, err == nil, err
	}
	out, launched, err := launchDeclaredFresh(teamDir, m, topo, inst, meta)
	installWatcherForRevivedAdoption(m, out, teamDir, topo)
	return out, launched, err
}

func managedResumeSupported(meta *Metadata) bool {
	if meta == nil {
		return false
	}
	return runtimeKindSupportsManagedResume(metadataRuntimeKind(meta)) && meta.SessionID != "" && meta.Workspace != ""
}

func launchDeclaredFresh(teamDir string, m *InstanceManager, topo *topology.Topology, inst *topology.Instance, expected *Metadata) (*Metadata, bool, error) {
	return launchDeclaredFreshWithPrompt(teamDir, m, topo, inst, expected, "")
}

func launchDeclaredFreshWithPrompt(teamDir string, m *InstanceManager, topo *topology.Topology, inst *topology.Instance, expected *Metadata, extraPrompt string) (*Metadata, bool, error) {
	if inst == nil {
		return nil, false, errors.New("restart: declared instance is required")
	}
	r := &EventResolver{mgr: m, teamDir: teamDir, topo: topo}
	runtime, err := r.prepareEphemeralRuntime(inst, inst.Name)
	if err != nil {
		return nil, false, err
	}
	workspace := r.teamDirParent()
	prompt := fmt.Sprintf("Topology restart for declared instance %q (agent=%s, restart=%s).", inst.Name, inst.Agent, inst.Restart)
	if strings.TrimSpace(extraPrompt) != "" {
		prompt += "\n\n" + extraPrompt
	}
	env := append([]string(nil), runtime.env...)
	envComplete := false
	if snapshotEnv, ok, err := m.instanceLaunchEnv(inst.Name); err != nil {
		return nil, false, fmt.Errorf("restart: launch env: %w", err)
	} else if ok {
		env = snapshotEnv
		envComplete = true
	}
	otelCtx := runtimeotel.Context{
		Agent:    inst.Agent,
		Instance: inst.Name,
		Team:     r.teamForInstance(inst.Name, nil),
		Worktree: workspace,
		Build:    buildinfo.Current(""),
	}
	args, stdin, rt, env, err := r.prepareEphemeralAgentArgs(inst.Agent, inst.Name, runtime.stateDir, workspace, prompt, env, runtime.mailboxInjection, nil, runtime.otelConfig, otelCtx, "")
	if err != nil {
		return nil, false, err
	}
	return m.launchPrepared(DispatchInput{
		Agent:         inst.Agent,
		Name:          inst.Name,
		Workspace:     workspace,
		Runtime:       string(rt.Kind),
		RuntimeBinary: rt.Binary,
		Args:          args,
		Env:           env,
		EnvComplete:   envComplete,
		StripOTelEnv:  runtime.otelConfig.Configured(),
		Stdin:         stdin,
	}, expected)
}

func installWatcherForRevivedAdoption(m *InstanceManager, meta *Metadata, teamDir string, topo *topology.Topology) {
	if m == nil || meta == nil || !meta.Adopted || meta.Status != StatusRunning {
		return
	}
	m.mu.Lock()
	if t := m.instances[meta.Instance]; t != nil && sameTrackedIncarnation(t, meta) {
		m.installAdoptedWatcherLocked(t, teamDir, topo)
	}
	m.mu.Unlock()
}

func persistRestartBackoff(teamDir string, inst *topology.Instance, meta *Metadata, now time.Time, cause error) error {
	if inst == nil {
		return nil
	}
	daemonRoot := DaemonRoot(teamDir)
	next := restartBackoffUntil(now)
	if meta == nil {
		meta = &Metadata{
			Instance:  inst.Name,
			Agent:     inst.Agent,
			Workspace: workspaceForTeamDir(teamDir),
			Status:    StatusCrashed,
			StartedAt: now,
			ExitedAt:  now,
		}
	}
	updated := *meta
	if updated.Agent == "" {
		updated.Agent = inst.Agent
	}
	if updated.Workspace == "" {
		updated.Workspace = workspaceForTeamDir(teamDir)
	}
	if updated.Status == "" {
		updated.Status = StatusCrashed
	}
	updated.RestartBackoffUntil = next
	if err := WriteMetadata(daemonRoot, &updated); err != nil {
		return err
	}
	_ = AppendLifecycleEvent(daemonRoot, &LifecycleEvent{
		Action:   "restart_backoff",
		Instance: updated.Instance,
		Agent:    updated.Agent,
		Status:   updated.Status,
		PID:      updated.PID,
		Message:  fmt.Sprintf("restart failed; backing off until %s: %v", next.Format(time.RFC3339), cause),
	})
	return nil
}

func restartBackoffUntil(now time.Time) time.Time {
	delay := restartBackoffDelay
	if delay <= 0 {
		delay = time.Second
	}
	if delay > restartBackoffCap {
		delay = restartBackoffCap
	}
	return now.Add(delay)
}

func workspaceForTeamDir(teamDir string) string {
	if filepath.Base(teamDir) == ".agent_team" {
		return filepath.Dir(teamDir)
	}
	return strings.TrimSuffix(teamDir, "/.agent_team")
}

func (m *InstanceManager) installAdoptedWatcherLocked(t *tracked, teamDir string, topo *topology.Topology) {
	if t == nil || t.meta == nil || teamDir == "" || t.meta.Status != StatusRunning || t.reaped != nil || t.meta.PID <= 0 {
		return
	}
	if !PidLiveCheck(t.meta.PID) {
		return
	}
	t.meta.Adopted = true
	_ = WriteMetadata(m.daemonRoot, t.meta)
	reaped := make(chan struct{})
	t.reaped = reaped
	meta := *t.meta
	go m.watchAdopted(meta, teamDir, topo, reaped)
}

func (m *InstanceManager) watchAdopted(meta Metadata, teamDir string, topo *topology.Topology, reaped chan<- struct{}) {
	defer close(reaped)
	ticker := time.NewTicker(adoptedPollInterval)
	defer ticker.Stop()
	for range ticker.C {
		if PidLiveCheck(meta.PID) {
			continue
		}
		if m.finalizeAdoptedExit(meta) && teamDir != "" {
			_ = ReconcileWithTopology(teamDir, m, topo)
		}
		return
	}
}

func (m *InstanceManager) finalizeAdoptedExit(meta Metadata) bool {
	m.mu.Lock()
	t := m.instances[meta.Instance]
	if t == nil || t.meta == nil || t.meta.PID != meta.PID || !t.meta.StartedAt.Equal(meta.StartedAt) || t.meta.Status != StatusRunning {
		m.mu.Unlock()
		return false
	}
	now := time.Now().UTC()
	t.meta.Status = StatusExited
	t.meta.ExitedAt = now
	usageCaptureErr := captureUsageForMetadata(t.meta, now)
	out := *t.meta
	if err := WriteMetadata(m.daemonRoot, t.meta); err != nil {
		m.mu.Unlock()
		return false
	}
	m.mu.Unlock()
	if usageCaptureErr != nil {
		m.recordEvent("usage_capture_failed", &out, usageCaptureErr.Error())
	} else if usageErr := persistMetadataUsageToJob(m.daemonRoot, &out); usageErr != nil {
		m.recordEvent("usage_capture_failed", &out, usageErr.Error())
	}
	m.recordEvent("exit", &out, "adopted process exited")
	m.notifyTerminal(&out)
	return true
}

func sameTrackedIncarnation(existing *tracked, fresh *Metadata) bool {
	if existing == nil || existing.meta == nil || fresh == nil {
		return false
	}
	if existing.meta.Instance != fresh.Instance || existing.meta.PID != fresh.PID {
		return false
	}
	return existing.meta.StartedAt.Equal(fresh.StartedAt)
}
