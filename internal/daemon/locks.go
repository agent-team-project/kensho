package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jamesaud/agent-team/internal/origin"
)

// LockLease is one durable row recording an instance holding a named dispatch
// lock. PID is filled after spawn; reconcile can recover it from metadata when
// a daemon crash lands between the pre-spawn reservation and post-spawn update.
type LockLease struct {
	Lock       string          `json:"lock"`
	Instance   string          `json:"instance"`
	PID        int             `json:"pid,omitempty"`
	Origin     origin.Envelope `json:"origin,omitempty"`
	AcquiredAt time.Time       `json:"acquired_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// LockSnapshot is the operator-facing state for one declared lock.
type LockSnapshot struct {
	Name      string       `json:"name"`
	Slots     int          `json:"slots"`
	Used      int          `json:"used"`
	Available int          `json:"available"`
	Holders   []LockHolder `json:"holders"`
}

// LockHolder describes one active lock holder in a snapshot.
type LockHolder struct {
	Instance   string    `json:"instance"`
	PID        int       `json:"pid,omitempty"`
	AcquiredAt time.Time `json:"acquired_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func LockRoot(daemonRoot string) string {
	return filepath.Join(daemonRoot, "locks")
}

func LockDir(daemonRoot, name string) string {
	return filepath.Join(LockRoot(daemonRoot), name)
}

func LockPath(daemonRoot, name, instance string) string {
	return filepath.Join(LockDir(daemonRoot, name), instance+".json")
}

func WriteLockLease(daemonRoot string, lease *LockLease) error {
	if err := validateLockLease(lease); err != nil {
		return err
	}
	dir := LockDir(daemonRoot, lease.Lock)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("lock: mkdir: %w", err)
	}
	body, err := json.MarshalIndent(lease, "", "  ")
	if err != nil {
		return fmt.Errorf("lock: marshal: %w", err)
	}
	body = append(body, '\n')
	tmp, err := os.CreateTemp(dir, lease.Instance+"-*.json.tmp")
	if err != nil {
		return fmt.Errorf("lock: tempfile: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("lock: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("lock: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("lock: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), LockPath(daemonRoot, lease.Lock, lease.Instance)); err != nil {
		return fmt.Errorf("lock: rename: %w", err)
	}
	return nil
}

func ReadLockLease(daemonRoot, name, instance string) (*LockLease, error) {
	body, err := os.ReadFile(LockPath(daemonRoot, name, instance))
	if err != nil {
		return nil, err
	}
	var lease LockLease
	if err := json.Unmarshal(body, &lease); err != nil {
		return nil, fmt.Errorf("lock: parse %s/%s: %w", name, instance, err)
	}
	if lease.Lock == "" {
		lease.Lock = name
	}
	if lease.Instance == "" {
		lease.Instance = instance
	}
	if err := validateLockLease(&lease); err != nil {
		return nil, fmt.Errorf("lock: %s/%s: %w", name, instance, err)
	}
	return &lease, nil
}

func ListLockLeases(daemonRoot string) ([]*LockLease, error) {
	root := LockRoot(daemonRoot)
	locks, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []*LockLease
	for _, lockEntry := range locks {
		if !lockEntry.IsDir() {
			continue
		}
		lockName := lockEntry.Name()
		files, err := os.ReadDir(LockDir(daemonRoot, lockName))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
				continue
			}
			instance := strings.TrimSuffix(file.Name(), ".json")
			lease, err := ReadLockLease(daemonRoot, lockName, instance)
			if err != nil {
				return nil, err
			}
			out = append(out, lease)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Lock != out[j].Lock {
			return out[i].Lock < out[j].Lock
		}
		return out[i].Instance < out[j].Instance
	})
	return out, nil
}

func RemoveLockLease(daemonRoot, name, instance string) error {
	err := os.Remove(LockPath(daemonRoot, name, instance))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func validateLockLease(lease *LockLease) error {
	if lease == nil {
		return errors.New("lock: nil lease")
	}
	if err := validateLedgerSegment(lease.Lock, "lock"); err != nil {
		return err
	}
	if err := validateLedgerSegment(lease.Instance, "instance"); err != nil {
		return err
	}
	if lease.AcquiredAt.IsZero() {
		return errors.New("lock: acquired_at is required")
	}
	if lease.UpdatedAt.IsZero() {
		return errors.New("lock: updated_at is required")
	}
	return nil
}

func validateLedgerSegment(value, field string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("lock: %s is required", field)
	}
	if value == "." || value == ".." || strings.Contains(value, "..") || strings.ContainsAny(value, `/\`) {
		return fmt.Errorf("lock: %s must not contain path segments", field)
	}
	return nil
}
