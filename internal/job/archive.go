package job

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"time"

	"github.com/agent-team-project/agent-team/internal/archive"
)

const archiveRecordTypeJob = "job"

type archivedJobRecord struct {
	Type       string       `json:"type"`
	ArchivedAt time.Time    `json:"archived_at"`
	ID         string       `json:"id"`
	TerminalAt time.Time    `json:"terminal_at"`
	Job        *Job         `json:"job"`
	Events     []Event      `json:"events,omitempty"`
	Gates      []GateRecord `json:"gates,omitempty"`
}

// ArchiveDiagnostic identifies one malformed or invalid archived job record
// omitted by an explicitly isolated archive read.
type ArchiveDiagnostic struct {
	Record string `json:"record"`
	Error  string `json:"error"`
}

type archivedJobRecordSource struct {
	record archivedJobRecord
	path   string
	line   int
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
	GateLog     bool      `json:"gate_log"`
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

// ListArchived loads the latest archived snapshot for each compacted job.
func ListArchived(teamDir string) ([]*Job, error) {
	records, err := readArchivedJobRecords(teamDir)
	if err != nil {
		return nil, err
	}
	latest := map[string]*archivedJobRecord{}
	for _, record := range records {
		if record.Job == nil || record.ID == "" {
			continue
		}
		if prior := latest[record.ID]; prior == nil || record.ArchivedAt.After(prior.ArchivedAt) {
			copy := record
			latest[record.ID] = &copy
		}
	}
	out := make([]*Job, 0, len(latest))
	for id, record := range latest {
		j := record.Job
		if j.ID == "" {
			j.ID = id
		}
		if err := Validate(j); err != nil {
			return nil, fmt.Errorf("archived job %s: %w", id, err)
		}
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ListArchivedIsolated loads valid archived job snapshots while isolating
// malformed archive lines and invalid records unrelated to targetJobID. The
// ordinary ListArchived path remains strict.
func ListArchivedIsolated(teamDir, targetJobID string) ([]*Job, []ArchiveDiagnostic, error) {
	targetJobID = NormalizeID(targetJobID)
	sources, diagnostics, err := readArchivedJobRecordSources(teamDir, true, targetJobID)
	if err != nil {
		return nil, nil, err
	}
	latest := map[string]*archivedJobRecordSource{}
	for i := range sources {
		source := &sources[i]
		record := source.record
		if record.Job == nil || record.ID == "" {
			continue
		}
		if prior := latest[record.ID]; prior == nil || record.ArchivedAt.After(prior.record.ArchivedAt) {
			latest[record.ID] = source
		}
	}
	ids := make([]string, 0, len(latest))
	for id := range latest {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]*Job, 0, len(latest))
	for _, id := range ids {
		source := latest[id]
		j := source.record.Job
		if j.ID == "" {
			j.ID = id
		}
		if err := Validate(j); err != nil {
			err = fmt.Errorf("archived job %s: %w", id, err)
			if targetJobID != "" && NormalizeID(id) == targetJobID {
				return nil, nil, err
			}
			diagnostics = append(diagnostics, ArchiveDiagnostic{
				Record: fmt.Sprintf("%s line %d (archived job %s)", source.path, source.line, id),
				Error:  err.Error(),
			})
			continue
		}
		out = append(out, j)
	}
	return out, diagnostics, nil
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

// ArchivedGates returns the archived gate-result snapshot for a compacted job.
func ArchivedGates(teamDir, rawID string) ([]GateRecord, error) {
	record, ok, err := readArchivedJobRecord(teamDir, rawID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return append([]GateRecord(nil), record.Gates...), nil
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
		gates, err := listLiveGateRecords(teamDir, j.ID)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) && !os.IsNotExist(err) {
				return nil, err
			}
			gates = nil
		}
		if len(gates) > 0 {
			result.GateLog = true
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
			Gates:      gates,
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
		if err := removeIfExists(GatePath(teamDir, j.ID)); err != nil {
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
	records, err := readArchivedJobRecords(teamDir)
	if err != nil {
		return nil, false, err
	}
	var found *archivedJobRecord
	for _, record := range records {
		if record.ID != id {
			continue
		}
		copy := record
		found = &copy
	}
	return found, found != nil, nil
}

func readArchivedJobRecords(teamDir string) ([]archivedJobRecord, error) {
	sources, _, err := readArchivedJobRecordSources(teamDir, false, "")
	if err != nil {
		return nil, err
	}
	records := make([]archivedJobRecord, 0, len(sources))
	for _, source := range sources {
		records = append(records, source.record)
	}
	return records, nil
}

func readArchivedJobRecordSources(teamDir string, isolateMalformed bool, targetJobID string) ([]archivedJobRecordSource, []ArchiveDiagnostic, error) {
	files, err := archive.Files(teamDir)
	if err != nil {
		return nil, nil, err
	}
	records := make([]archivedJobRecordSource, 0)
	diagnostics := make([]ArchiveDiagnostic, 0)
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			return nil, nil, err
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
		line := 0
		for scanner.Scan() {
			line++
			var record archivedJobRecord
			if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
				if !isolateMalformed {
					_ = f.Close()
					return nil, nil, fmt.Errorf("archive %s line %d: %w", path, line, err)
				}
				if record.Type == archiveRecordTypeJob && targetJobID != "" && NormalizeID(record.ID) == targetJobID {
					_ = f.Close()
					return nil, nil, fmt.Errorf("archived job %s at %s line %d: %w", targetJobID, path, line, err)
				}
				diagnostics = append(diagnostics, ArchiveDiagnostic{
					Record: fmt.Sprintf("%s line %d", path, line),
					Error:  err.Error(),
				})
				continue
			}
			if record.Type != archiveRecordTypeJob {
				continue
			}
			records = append(records, archivedJobRecordSource{record: record, path: path, line: line})
		}
		if err := scanner.Err(); err != nil {
			_ = f.Close()
			return nil, nil, err
		}
		if err := f.Close(); err != nil {
			return nil, nil, err
		}
	}
	return records, diagnostics, nil
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
