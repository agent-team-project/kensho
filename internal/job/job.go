package job

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Status is the durable lifecycle state of a work unit.
type Status string

const (
	StatusQueued  Status = "queued"
	StatusRunning Status = "running"
	StatusBlocked Status = "blocked"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// Job is one durable work unit under `.agent_team/jobs/<id>.toml`.
type Job struct {
	ID         string    `toml:"id"`
	Ticket     string    `toml:"ticket"`
	TicketURL  string    `toml:"ticket_url,omitempty"`
	Target     string    `toml:"target"`
	Kickoff    string    `toml:"kickoff,omitempty"`
	Instance   string    `toml:"instance,omitempty"`
	Status     Status    `toml:"status"`
	Branch     string    `toml:"branch,omitempty"`
	Worktree   string    `toml:"worktree,omitempty"`
	PR         string    `toml:"pr,omitempty"`
	LastEvent  string    `toml:"last_event,omitempty"`
	LastStatus string    `toml:"last_status,omitempty"`
	CreatedAt  time.Time `toml:"created_at"`
	UpdatedAt  time.Time `toml:"updated_at"`
}

// Directory returns the jobs directory for a team root.
func Directory(teamDir string) string {
	return filepath.Join(teamDir, "jobs")
}

// Path returns the TOML path for id. The caller should pass a normalized id.
func Path(teamDir, id string) string {
	return filepath.Join(Directory(teamDir), id+".toml")
}

// NormalizeID turns a ticket or user-supplied id into the canonical filename id.
func NormalizeID(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// ValidStatus reports whether s is a supported job lifecycle state.
func ValidStatus(s Status) bool {
	switch s {
	case StatusQueued, StatusRunning, StatusBlocked, StatusDone, StatusFailed:
		return true
	default:
		return false
	}
}

// ParseStatus validates a status string.
func ParseStatus(raw string) (Status, error) {
	s := Status(strings.ToLower(strings.TrimSpace(raw)))
	if !ValidStatus(s) {
		return "", fmt.Errorf("unknown job status %q", raw)
	}
	return s, nil
}

// New builds a queued job with normalized defaults.
func New(ticket, target, kickoff string, now time.Time) (*Job, error) {
	ticket = strings.TrimSpace(ticket)
	target = strings.TrimSpace(target)
	kickoff = strings.TrimSpace(kickoff)
	if ticket == "" {
		return nil, errors.New("ticket is required")
	}
	if target == "" {
		return nil, errors.New("target is required")
	}
	id := NormalizeID(ticket)
	if id == "" {
		return nil, fmt.Errorf("ticket %q produced an empty job id", ticket)
	}
	now = now.UTC()
	return &Job{
		ID:        id,
		Ticket:    ticket,
		Target:    target,
		Kickoff:   kickoff,
		Status:    StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// Validate checks the persisted job invariants.
func Validate(j *Job) error {
	if j == nil {
		return errors.New("job is nil")
	}
	if strings.TrimSpace(j.ID) == "" {
		return errors.New("job id is required")
	}
	if normalized := NormalizeID(j.ID); normalized != j.ID {
		return fmt.Errorf("job id %q must be normalized as %q", j.ID, normalized)
	}
	if strings.TrimSpace(j.Ticket) == "" {
		return errors.New("ticket is required")
	}
	if strings.TrimSpace(j.Target) == "" {
		return errors.New("target is required")
	}
	if !ValidStatus(j.Status) {
		return fmt.Errorf("unknown job status %q", j.Status)
	}
	if j.CreatedAt.IsZero() {
		return errors.New("created_at is required")
	}
	if j.UpdatedAt.IsZero() {
		return errors.New("updated_at is required")
	}
	return nil
}

// Read loads a single job by normalized or raw id.
func Read(teamDir, rawID string) (*Job, error) {
	id := NormalizeID(rawID)
	if id == "" {
		return nil, fmt.Errorf("job id %q produced an empty normalized id", rawID)
	}
	var j Job
	if _, err := toml.DecodeFile(Path(teamDir, id), &j); err != nil {
		return nil, err
	}
	if j.ID == "" {
		j.ID = id
	}
	if err := Validate(&j); err != nil {
		return nil, fmt.Errorf("job %s: %w", id, err)
	}
	return &j, nil
}

// Write stores a job atomically.
func Write(teamDir string, j *Job) error {
	if err := Validate(j); err != nil {
		return err
	}
	dir := Directory(teamDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("job: mkdir: %w", err)
	}
	target := Path(teamDir, j.ID)
	tmp, err := os.CreateTemp(dir, j.ID+"-*.toml.tmp")
	if err != nil {
		return fmt.Errorf("job: tempfile: %w", err)
	}
	defer os.Remove(tmp.Name())
	enc := toml.NewEncoder(tmp)
	if err := enc.Encode(j); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("job: encode: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("job: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("job: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return fmt.Errorf("job: rename: %w", err)
	}
	return nil
}

// List loads all valid job files in deterministic id order.
func List(teamDir string) ([]*Job, error) {
	dir := Directory(teamDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]*Job, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".toml")
		j, err := Read(teamDir, id)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}
