package job

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/jamesaud/agent-team/internal/archive"
)

const archiveRecordTypeJob = "job"

type archivedJobRecord struct {
	Type       string    `json:"type"`
	ArchivedAt time.Time `json:"archived_at"`
	ID         string    `json:"id"`
	TerminalAt time.Time `json:"terminal_at"`
	Job        *Job      `json:"job"`
	Events     []Event   `json:"events,omitempty"`
}

// TerminalArchiveResult describes one live terminal job matched by archive
// compaction.
type TerminalArchiveResult struct {
	ID          string    `json:"id"`
	Ticket      string    `json:"ticket"`
	Status      Status    `json:"status"`
	TerminalAt  time.Time `json:"terminal_at"`
	ArchivedAt  time.Time `json:"archived_at,omitempty"`
	ArchivePath string    `json:"archive_path,omitempty"`
	Action      string    `json:"action"`
	DryRun      bool      `json:"dry_run,omitempty"`
	JobFile     bool      `json:"job_file"`
	EventLog    bool      `json:"event_log"`
}

// IsTerminalStatus reports whether status is a durable terminal job status.
func IsTerminalStatus(status Status) bool {
	return status == StatusDone || status == StatusFailed
}

// ReadLiveOrArchive loads a live job, falling back to archived job records only
// when the live job file is missing.
func ReadLiveOrArchive(teamDir, rawID string) (*Job, error) {
	j, err := Read(teamDir, rawID)
	if err == nil {
		return j, nil
	}
	if !errors.Is(err, fs.ErrNotExist) && !os.IsNotExist(err) {
		return nil, err
	}
	archived, archiveErr := ReadArchived(teamDir, rawID)
	if archiveErr == nil {
		return archived, nil
	}
	if errors.Is(archiveErr, fs.ErrNotExist) || os.IsNotExist(archiveErr) {
		return nil, err
	}
	return nil, archiveErr
}

// ReadArchived loads a job from the daemon archive.
func ReadArchived(teamDir, rawID string) (*Job, error) {
	record, ok, err := readArchivedJobRecord(teamDir, rawID)
	if err != nil {
		return nil, err
	}
	if !ok || record.Job == nil {
		return nil, fs.ErrNotExist
	}
	j := record.Job
	if j.ID == "" {
		j.ID = IDFromInput(rawID)
	}
	if err := Validate(j); err != nil {
		return nil, fmt.Errorf("archived job %s: %w", IDFromInput(rawID), err)
	}
	return j, nil
}

// ArchivedEvents returns the archived event snapshot for a compacted job.
func ArchivedEvents(teamDir, rawID string) ([]Event, error) {
	record, ok, err := readArchivedJobRecord(teamDir, rawID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return append([]Event(nil), record.Events...), nil
}

// CompactTerminal archives terminal jobs older than retention and removes their
// live job and event files. A retention <= 0 leaves files untouched.
func CompactTerminal(teamDir string, retention time.Duration, now time.Time, dryRun bool) ([]TerminalArchiveResult, error) {
	if retention <= 0 {
		return nil, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	cutoff := now.Add(-retention)
	jobs, err := List(teamDir)
	if err != nil {
		return nil, err
	}
	results := make([]TerminalArchiveResult, 0)
	for _, j := range jobs {
		if j == nil || !IsTerminalStatus(j.Status) {
			continue
		}
		terminalAt := terminalJobTime(j)
		if terminalAt.IsZero() || !terminalAt.Before(cutoff) {
			continue
		}
		result := TerminalArchiveResult{
			ID:         j.ID,
			Ticket:     j.Ticket,
			Status:     j.Status,
			TerminalAt: terminalAt,
			Action:     "archived",
			DryRun:     dryRun,
			JobFile:    true,
		}
		events, err := listLiveEvents(teamDir, j.ID)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) && !os.IsNotExist(err) {
				return nil, err
			}
			events = nil
		}
		if len(events) > 0 {
			result.EventLog = true
		}
		if dryRun {
			result.Action = "would_archive"
			results = append(results, result)
			continue
		}
		archivedAt := now
		path, err := archive.AppendJSON(teamDir, terminalAt, archivedJobRecord{
			Type:       archiveRecordTypeJob,
			ArchivedAt: archivedAt,
			ID:         j.ID,
			TerminalAt: terminalAt,
			Job:        j,
			Events:     events,
		})
		if err != nil {
			return nil, err
		}
		result.ArchivedAt = archivedAt
		result.ArchivePath = path
		if err := removeIfExists(Path(teamDir, j.ID)); err != nil {
			return nil, err
		}
		if err := removeIfExists(EventPath(teamDir, j.ID)); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func readArchivedJobRecord(teamDir, rawID string) (*archivedJobRecord, bool, error) {
	id := IDFromInput(rawID)
	if id == "" {
		return nil, false, fmt.Errorf("job id %q produced an empty normalized id", rawID)
	}
	files, err := archive.Files(teamDir)
	if err != nil {
		return nil, false, err
	}
	var found *archivedJobRecord
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			return nil, false, err
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
		line := 0
		for scanner.Scan() {
			line++
			var record archivedJobRecord
			if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
				_ = f.Close()
				return nil, false, fmt.Errorf("archive %s line %d: %w", path, line, err)
			}
			if record.Type != archiveRecordTypeJob || record.ID != id {
				continue
			}
			copy := record
			found = &copy
		}
		if err := scanner.Err(); err != nil {
			_ = f.Close()
			return nil, false, err
		}
		if err := f.Close(); err != nil {
			return nil, false, err
		}
	}
	if found == nil {
		return nil, false, nil
	}
	return found, true, nil
}

func terminalJobTime(j *Job) time.Time {
	if j == nil {
		return time.Time{}
	}
	if !j.UpdatedAt.IsZero() {
		return j.UpdatedAt.UTC()
	}
	return j.CreatedAt.UTC()
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
