package cli

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
)

type terminalCompactionResult struct {
	DryRun    bool                           `json:"dry_run,omitempty"`
	Retention string                         `json:"retention"`
	Cutoff    time.Time                      `json:"cutoff"`
	Jobs      []job.TerminalArchiveResult    `json:"jobs,omitempty"`
	Instances []daemon.MetadataArchiveResult `json:"instances,omitempty"`
}

func runTerminalCompaction(teamDir string, retention time.Duration, now time.Time, dryRun bool) (*terminalCompactionResult, error) {
	if retention <= 0 {
		return nil, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	jobs, err := job.CompactTerminal(teamDir, retention, now, dryRun)
	if err != nil {
		return nil, err
	}
	instances, err := daemon.CompactTerminalMetadata(teamDir, retention, now, dryRun)
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 && len(instances) == 0 {
		return nil, nil
	}
	return &terminalCompactionResult{
		DryRun:    dryRun,
		Retention: retention.String(),
		Cutoff:    now.Add(-retention),
		Jobs:      jobs,
		Instances: instances,
	}, nil
}

func renderTerminalCompactionResult(w io.Writer, result *terminalCompactionResult) {
	if result == nil {
		return
	}
	action := "archived"
	if result.DryRun {
		action = "would_archive"
	}
	fmt.Fprintf(w, "Terminal compaction: %s jobs=%d instances=%d retention=%s cutoff=%s\n",
		action, len(result.Jobs), len(result.Instances), result.Retention, result.Cutoff.Format(time.RFC3339))
	if len(result.Jobs) > 0 {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "TYPE\tID\tSTATUS\tTERMINAL_AT\tARCHIVE")
		for _, row := range result.Jobs {
			fmt.Fprintf(tw, "job\t%s\t%s\t%s\t%s\n",
				row.ID, row.Status, row.TerminalAt.Format(time.RFC3339), emptyDash(row.ArchivePath))
		}
		_ = tw.Flush()
	}
	if len(result.Instances) > 0 {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "TYPE\tID\tSTATUS\tTERMINAL_AT\tARCHIVE")
		for _, row := range result.Instances {
			fmt.Fprintf(tw, "instance\t%s\t%s\t%s\t%s\n",
				row.Instance, row.Status, row.TerminalAt.Format(time.RFC3339), emptyDash(row.ArchivePath))
		}
		_ = tw.Flush()
	}
}
