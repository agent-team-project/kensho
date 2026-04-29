package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/spf13/cobra"
)

// newPsCmd builds `agent-team ps`. It's the daemon-aware single-source view:
// when the daemon is running, every running/stopped/exited instance the
// daemon knows about is listed; entries with a status.toml on disk are
// folded in, so an instance that emitted status without ever being dispatched
// via the daemon (the SQU-25 path) still appears.
//
// When the daemon is not running, this command degrades to the same on-disk
// walk that `agent-team instance ps` does.
func newPsCmd() *cobra.Command {
	var target string
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ps",
		Short: "List instances (daemon-aware: merges live daemon state with on-disk status).",
		Long: "Daemon-aware single-source view of instances. With the daemon " +
			"running, lifecycle status (running/stopped/exited/crashed) comes " +
			"from /v1/instances; phase / summary come from each instance's " +
			"on-disk status.toml. Without a daemon, falls back to the " +
			"`agent-team instance ps` view.",
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			return runPs(cmd.OutOrStdout(), teamDir, time.Now())
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	return cmd
}

func runPs(w io.Writer, teamDir string, now time.Time) error {
	agentNames := loadAgentNames(teamDir)
	rows := loadInstanceRows(teamDir, agentNames, now)
	rowByInstance := map[string]*instanceRow{}
	for i := range rows {
		rowByInstance[rows[i].Instance] = &rows[i]
	}

	// Try the daemon. errDaemonNotRunning → fall back silently to the
	// disk-only view. Other errors are surfaced (something is broken
	// with the daemon — better to know than to hide).
	client, err := newDaemonClient(teamDir)
	switch {
	case err == nil:
		insts, err := client.Instances()
		if err != nil {
			return err
		}
		for _, m := range insts {
			row, ok := rowByInstance[m.Instance]
			if !ok {
				newRow := newRowFromMeta(m, agentNames)
				rows = append(rows, newRow)
				rowByInstance[newRow.Instance] = &rows[len(rows)-1]
				continue
			}
			row.Lifecycle = string(m.Status)
			row.PID = m.PID
		}
	case errors.Is(err, errDaemonNotRunning):
		// Disk-only view; no `STATUS` column data beyond the on-disk phase.
	default:
		return err
	}

	if len(rows) == 0 {
		fmt.Fprintln(w, "(no instances)")
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "INSTANCE\tAGENT\tSTATUS\tPHASE\tAGE\tSUMMARY")
	for _, r := range rows {
		phase := r.Phase
		if r.Stale {
			phase = phase + " (stale)"
		}
		life := r.Lifecycle
		if life == "" {
			life = "—"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Instance, r.Agent, life, phase, r.Age, r.Summary)
	}
	return tw.Flush()
}

// newRowFromMeta builds a row for an instance the daemon knows about but
// which has no state dir / status.toml on disk yet. Phase shows `—` until
// the instance starts emitting status.
func newRowFromMeta(m *daemon.Metadata, agentNames map[string]bool) instanceRow {
	agent := m.Agent
	if !agentNames[agent] {
		// Best-effort: if the agent name isn't recognised, fall back to "—".
		agent = guessAgentName(m.Instance, agentNames)
	}
	return instanceRow{
		Instance:  m.Instance,
		Agent:     agent,
		Phase:     "—",
		Age:       "—",
		Lifecycle: string(m.Status),
		PID:       m.PID,
	}
}
