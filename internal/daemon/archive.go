package daemon

import (
	"time"

	"github.com/agent-team-project/agent-team/internal/archive"
)

const archiveRecordTypeMetadata = "daemon_metadata"

type archivedMetadataRecord struct {
	Type       string    `json:"type"`
	ArchivedAt time.Time `json:"archived_at"`
	ID         string    `json:"id"`
	TerminalAt time.Time `json:"terminal_at"`
	Metadata   *Metadata `json:"metadata"`
}

// MetadataArchiveResult describes one terminal daemon metadata entry matched by
// archive compaction.
type MetadataArchiveResult struct {
	Instance    string    `json:"instance"`
	Agent       string    `json:"agent"`
	Job         string    `json:"job,omitempty"`
	Status      Status    `json:"status"`
	TerminalAt  time.Time `json:"terminal_at"`
	ArchivedAt  time.Time `json:"archived_at,omitempty"`
	ArchivePath string    `json:"archive_path,omitempty"`
	Action      string    `json:"action"`
	DryRun      bool      `json:"dry_run,omitempty"`
}

// IsTerminalStatus reports whether status is a terminal daemon lifecycle state.
func IsTerminalStatus(status Status) bool {
	return status == StatusExited || status == StatusCrashed
}

// CompactTerminalMetadata archives terminal instance metadata older than
// retention and removes those entries from the live daemon registry.
func CompactTerminalMetadata(teamDir string, retention time.Duration, now time.Time, dryRun bool) ([]MetadataArchiveResult, error) {
	if retention <= 0 {
		return nil, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	cutoff := now.Add(-retention)
	daemonRoot := DaemonRoot(teamDir)
	records, err := ListMetadata(daemonRoot)
	if err != nil {
		return nil, err
	}
	results := make([]MetadataArchiveResult, 0)
	for _, meta := range records {
		if meta == nil || !IsTerminalStatus(meta.Status) {
			continue
		}
		terminalAt := terminalMetadataTime(meta)
		if terminalAt.IsZero() || !terminalAt.Before(cutoff) {
			continue
		}
		result := MetadataArchiveResult{
			Instance:   meta.Instance,
			Agent:      meta.Agent,
			Job:        meta.Job,
			Status:     meta.Status,
			TerminalAt: terminalAt,
			Action:     "archived",
			DryRun:     dryRun,
		}
		if dryRun {
			result.Action = "would_archive"
			results = append(results, result)
			continue
		}
		archivedAt := now
		path, err := archive.AppendJSON(teamDir, terminalAt, archivedMetadataRecord{
			Type:       archiveRecordTypeMetadata,
			ArchivedAt: archivedAt,
			ID:         meta.Instance,
			TerminalAt: terminalAt,
			Metadata:   meta,
		})
		if err != nil {
			return nil, err
		}
		result.ArchivedAt = archivedAt
		result.ArchivePath = path
		if err := RemoveInstance(daemonRoot, meta.Instance); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func terminalMetadataTime(meta *Metadata) time.Time {
	if meta == nil {
		return time.Time{}
	}
	if !meta.ExitedAt.IsZero() {
		return meta.ExitedAt.UTC()
	}
	if !meta.StoppedAt.IsZero() {
		return meta.StoppedAt.UTC()
	}
	return meta.StartedAt.UTC()
}
