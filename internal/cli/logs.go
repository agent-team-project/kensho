package cli

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

// newLogsCmd builds `agent-team logs <instance> [--follow]`.
//
// Without a running daemon, log capture isn't centralised — a no-daemon
// `agent-team run` exec's claude directly with stdout/stderr to the user's
// terminal. We surface that distinction with a clear error rather than
// silently doing the wrong thing.
func newLogsCmd() *cobra.Command {
	var (
		target string
		follow bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "logs <instance>",
		Short: "Stream an instance's log via the daemon (--follow tails until Ctrl-C).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			client, err := newDaemonClient(teamDir)
			if err != nil {
				if errors.Is(err, errDaemonNotRunning) {
					fmt.Fprintln(cmd.ErrOrStderr(),
						"agent-team logs: no daemon running — start it with `agent-team daemon start`.")
					fmt.Fprintln(cmd.ErrOrStderr(),
						"  (without the daemon, instances stream to your terminal directly; logs are not centrally captured.)")
					return exitErr(1)
				}
				return err
			}

			ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			return client.LogsStream(ctx, cmd.OutOrStdout(), args[0], follow)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Tail the log; print new bytes as they appear.")
	return cmd
}
