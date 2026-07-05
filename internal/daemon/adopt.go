package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/agent-team-project/agent-team/internal/runtimebin"
)

// AdoptInput describes an externally started process that should be tracked
// by daemon metadata. Adopted processes are visible to normal inspection and
// stop/reconcile flows, but the daemon cannot wait on their exit until a later
// reconcile pass observes the PID state.
type AdoptInput struct {
	Instance      string
	Agent         string
	Job           string
	Ticket        string
	Branch        string
	PR            string
	Runtime       string
	RuntimeBinary string
	Workspace     string
	PID           int
	SessionID     string
	StartedAt     time.Time
	LogPath       string
	Force         bool
}

// PrepareAdoptMetadata validates an adoption request and returns the metadata
// that would be written. The bool reports whether the resulting record differs
// from the current on-disk metadata.
func PrepareAdoptMetadata(daemonRoot string, in AdoptInput, now time.Time) (*Metadata, bool, error) {
	instance := strings.TrimSpace(in.Instance)
	if instance == "" {
		return nil, false, errors.New("adopt: instance is required")
	}
	agent := strings.TrimSpace(in.Agent)
	if agent == "" {
		return nil, false, errors.New("adopt: agent is required")
	}
	if in.PID <= 0 {
		return nil, false, errors.New("adopt: pid must be > 0")
	}
	if !PidLiveCheck(in.PID) {
		return nil, false, fmt.Errorf("adopt: pid %d is not running", in.PID)
	}
	workspace := strings.TrimSpace(in.Workspace)
	if workspace == "" {
		return nil, false, errors.New("adopt: workspace is required")
	}
	if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	kindRaw := strings.TrimSpace(in.Runtime)
	if kindRaw == "" {
		kindRaw = string(runtimebin.KindClaude)
	}
	kind, err := runtimebin.ParseKind(kindRaw)
	if err != nil {
		return nil, false, fmt.Errorf("adopt: %w", err)
	}
	binary := strings.TrimSpace(in.RuntimeBinary)
	if binary == "" {
		binary = runtimebin.DefaultBinaryForKind(kind)
	}

	var existing *Metadata
	if current, err := ReadMetadata(daemonRoot, instance); err == nil {
		existing = current
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, false, err
	}
	if existing != nil && !in.Force && existing.Status == StatusRunning && existing.PID != in.PID && PidLiveCheck(existing.PID) {
		return nil, false, fmt.Errorf("adopt: instance %q already tracks live pid %d; use --force to replace it", instance, existing.PID)
	}

	startedAt := in.StartedAt
	if startedAt.IsZero() {
		if existing != nil && existing.PID == in.PID && !existing.StartedAt.IsZero() {
			startedAt = existing.StartedAt
		} else {
			startedAt = now
		}
	} else {
		startedAt = startedAt.UTC()
	}

	meta := &Metadata{
		Instance:      instance,
		Agent:         agent,
		Job:           preserveAdoptField(in.Job, existing, func(m *Metadata) string { return m.Job }, in.PID),
		Ticket:        preserveAdoptField(in.Ticket, existing, func(m *Metadata) string { return m.Ticket }, in.PID),
		Branch:        preserveAdoptField(in.Branch, existing, func(m *Metadata) string { return m.Branch }, in.PID),
		PR:            preserveAdoptField(in.PR, existing, func(m *Metadata) string { return m.PR }, in.PID),
		Runtime:       string(kind),
		RuntimeBinary: binary,
		Workspace:     workspace,
		PID:           in.PID,
		SessionID:     preserveAdoptField(in.SessionID, existing, func(m *Metadata) string { return m.SessionID }, in.PID),
		StartedAt:     startedAt,
		Status:        StatusRunning,
		LogPath:       preserveAdoptField(in.LogPath, existing, func(m *Metadata) string { return m.LogPath }, in.PID),
		Adopted:       true,
	}
	if existing == nil {
		return meta, true, nil
	}
	return meta, !reflect.DeepEqual(existing, meta), nil
}

func preserveAdoptField(value string, existing *Metadata, field func(*Metadata) string, pid int) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	if existing != nil && existing.PID == pid {
		return field(existing)
	}
	return ""
}

// AdoptMetadata writes an adoption record and appends a lifecycle event when
// metadata changes.
func AdoptMetadata(daemonRoot string, in AdoptInput, now time.Time) (*Metadata, bool, error) {
	meta, changed, err := PrepareAdoptMetadata(daemonRoot, in, now)
	if err != nil || !changed {
		return meta, changed, err
	}
	if err := WriteMetadata(daemonRoot, meta); err != nil {
		return nil, false, err
	}
	_ = AppendLifecycleEvent(daemonRoot, &LifecycleEvent{
		Action:   "adopt",
		Instance: meta.Instance,
		Agent:    meta.Agent,
		Job:      meta.Job,
		Ticket:   meta.Ticket,
		Branch:   meta.Branch,
		PR:       meta.PR,
		Status:   meta.Status,
		PID:      meta.PID,
		Message:  "external process adopted",
	})
	return meta, true, nil
}
