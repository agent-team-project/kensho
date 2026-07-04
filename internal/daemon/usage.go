package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/jamesaud/agent-team/internal/jobwrite"
	"github.com/jamesaud/agent-team/internal/usage"
)

func captureUsageForMetadata(meta *Metadata, now time.Time) error {
	if meta == nil {
		return nil
	}
	if meta.Usage != nil && usage.RecordUseful(*meta.Usage) {
		return nil
	}
	record, err := usage.Capture(usage.CaptureInput{
		Instance:  meta.Instance,
		Agent:     meta.Agent,
		Runtime:   meta.Runtime,
		LogPath:   meta.LogPath,
		StartedAt: meta.StartedAt,
		EndedAt:   terminalUsageTime(meta),
		Now:       now,
	})
	if err != nil {
		return err
	}
	if record != nil && usage.RecordUseful(*record) {
		record.Origin = meta.Origin
		meta.Usage = record
	}
	return nil
}

func persistMetadataUsageToJob(daemonRoot string, meta *Metadata) error {
	if meta == nil || meta.Usage == nil || meta.Job == "" {
		return nil
	}
	teamDir := filepath.Dir(daemonRoot)
	_, _, err := jobwrite.RecordUsage(teamDir, meta.Job, *meta.Usage, jobwrite.Options{
		EventType: "usage_captured",
		Actor:     "daemon",
		Message:   fmt.Sprintf("captured usage for %s", meta.Instance),
	})
	if err != nil && (errors.Is(err, fs.ErrNotExist) || os.IsNotExist(err)) {
		return nil
	}
	return err
}

func terminalUsageTime(meta *Metadata) time.Time {
	if meta == nil {
		return time.Time{}
	}
	if !meta.ExitedAt.IsZero() {
		return meta.ExitedAt.UTC()
	}
	if !meta.StoppedAt.IsZero() {
		return meta.StoppedAt.UTC()
	}
	return time.Time{}
}
