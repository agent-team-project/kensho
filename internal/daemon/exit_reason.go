package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
)

const (
	ExitKindSignal   = "signal"
	ExitKindPanic    = "panic"
	ExitKindError    = "error"
	ExitKindShutdown = "shutdown"
)

// ExitReason records why the daemon process last exited. It is intentionally
// daemon-scoped rather than job-scoped: a dead daemon can strand many jobs.
type ExitReason struct {
	Kind       string         `json:"kind"`
	Reason     string         `json:"reason,omitempty"`
	Signal     string         `json:"signal,omitempty"`
	Error      string         `json:"error,omitempty"`
	PID        int            `json:"pid,omitempty"`
	RecordedAt time.Time      `json:"recorded_at"`
	Build      buildinfo.Info `json:"build,omitempty"`
}

// ExitReasonPath returns the last daemon exit/crash reason path for teamDir.
func ExitReasonPath(teamDir string) string {
	return filepath.Join(DaemonRoot(teamDir), "exit-reason.json")
}

// WriteExitReason persists the daemon's last exit/crash reason atomically.
func WriteExitReason(teamDir string, reason ExitReason) error {
	reason = normalizedExitReason(reason)
	if strings.TrimSpace(reason.Kind) == "" {
		return fmt.Errorf("exit-reason: kind is required")
	}
	body, err := json.MarshalIndent(&reason, "", "  ")
	if err != nil {
		return fmt.Errorf("exit-reason: marshal: %w", err)
	}
	body = append(body, '\n')
	if err := writeExitReasonFileAtomic(ExitReasonPath(teamDir), body); err != nil {
		return err
	}
	return nil
}

// ReadExitReason reads the last daemon exit/crash reason. Missing files wrap
// fs.ErrNotExist so callers can branch with errors.Is.
func ReadExitReason(teamDir string) (*ExitReason, error) {
	path := ExitReasonPath(teamDir)
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("exit-reason: read %s: %w", path, err)
	}
	var reason ExitReason
	if err := json.Unmarshal(body, &reason); err != nil {
		return nil, fmt.Errorf("exit-reason: parse %s: %w", path, err)
	}
	reason = normalizedExitReason(reason)
	return &reason, nil
}

func normalizedExitReason(reason ExitReason) ExitReason {
	reason.Kind = strings.TrimSpace(reason.Kind)
	reason.Reason = strings.TrimSpace(reason.Reason)
	reason.Signal = strings.TrimSpace(reason.Signal)
	reason.Error = strings.TrimSpace(reason.Error)
	if reason.RecordedAt.IsZero() {
		reason.RecordedAt = time.Now().UTC()
	} else {
		reason.RecordedAt = reason.RecordedAt.UTC()
	}
	if reason.Reason == "" {
		switch reason.Kind {
		case ExitKindSignal:
			if reason.Signal != "" {
				reason.Reason = "received " + reason.Signal
			}
		case ExitKindPanic:
			reason.Reason = "panic"
		case ExitKindError:
			reason.Reason = reason.Error
		case ExitKindShutdown:
			reason.Reason = "clean shutdown"
		}
	}
	return reason
}

func writeExitReasonFileAtomic(target string, body []byte) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("exit-reason: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "exit-reason-*.json.tmp")
	if err != nil {
		return fmt.Errorf("exit-reason: tempfile: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("exit-reason: chmod tempfile: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("exit-reason: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("exit-reason: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("exit-reason: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return fmt.Errorf("exit-reason: rename: %w", err)
	}
	return nil
}
