package daemon

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// PidLiveCheck reports whether the given PID is alive on this system. We use
// the standard `kill(pid, 0)` probe: signal 0 doesn't deliver, but the kernel
// runs its usual permission/existence checks and returns ESRCH if the process
// is gone. EPERM means alive but owned by another user — for our purposes,
// alive (the daemon writes the metadata, so EPERM should never apply
// normally).
//
// Variable so tests can stub it.
var PidLiveCheck = func(pid int) bool {
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
	all, err := ListMetadata(daemonRoot)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, md := range all {
		switch md.Status {
		case StatusRunning:
			if PidLiveCheck(md.PID) {
				// Adopt: keep status=running, but we can't Wait() so the
				// reaper won't observe its eventual exit. The next
				// Reconcile pass (or a /v1/instances request that
				// triggers a refresh) will catch the transition.
				continue
			}
			md.Status = StatusExited
			md.ExitedAt = now
			if err := WriteMetadata(daemonRoot, md); err != nil {
				return err
			}
		case StatusStopped, StatusExited, StatusCrashed:
			// Nothing to reconcile.
		default:
			// Unknown status — leave it.
		}
	}
	// Repopulate the manager's in-memory map.
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, md := range all {
		// Re-read after possible status update.
		fresh, err := ReadMetadata(daemonRoot, md.Instance)
		if err != nil {
			continue
		}
		m.instances[fresh.Instance] = &tracked{meta: fresh}
	}
	return nil
}
