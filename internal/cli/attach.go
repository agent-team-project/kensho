package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

// newAttachCmd builds `agent-team attach <instance>` — the interactive-resume
// command described in `documentation/orchestrator.md` § Lifecycle model
// (Shape A, transfer-ownership model).
//
// Flow:
//  1. Daemon must be running. Topology lookup rejects ephemeral instances.
//  2. POST /v1/stop {instance} — daemon SIGTERMs the child and (per SQU-28's
//     reaper-with-channel sync) returns only after metadata is persisted.
//  3. Read the persisted session_id from /v1/instances.
//  4. exec `claude --resume <session-id>` directly with stdin/stdout/stderr
//     wired to the user's terminal — TTY ownership transfers.
//  5. When the user exits: unless --no-resume is set, POST /v1/start to put
//     the instance back under daemon supervision.
//
// Brief downtime is by design (Shape A). Per-instance state files
// (status.toml, channel cursors, mailbox cursor) are untouched throughout.
func newAttachCmd() *cobra.Command {
	var (
		target   string
		noResume bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "attach <instance>",
		Short: "Open an interactive claude session against a daemon-managed persistent instance.",
		Long: "Stop the daemon-managed claude child for <instance>, then exec " +
			"`claude --resume <session-id>` in your terminal so the conversation " +
			"continues interactively. On exit, the daemon resumes supervision " +
			"automatically — pass --no-resume to leave the instance stopped.\n\n" +
			"There is brief downtime during the handoff (Shape A): the daemon " +
			"child is killed before claude --resume reattaches. Channel cursors " +
			"and mailbox state survive the transfer.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAttach(cmd, target, args[0], noResume)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVar(&noResume, "no-resume", false, "Leave the instance in stopped state when claude exits (default: re-dispatch via the daemon).")
	return cmd
}

func runAttach(cmd *cobra.Command, target, instance string, noResume bool) error {
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}

	dc, err := newDaemonClient(teamDir)
	if err != nil {
		if errors.Is(err, errDaemonNotRunning) {
			fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: daemon is not running — start it first with `agent-team daemon start`.")
			return exitErr(2)
		}
		return err
	}

	// Reject ephemeral instances: they spawn-and-exit by design and have no
	// stable session to attach to. Send the user to logs --follow instead.
	if topo, terr := topology.LoadFromTeamDir(teamDir); terr == nil && topo != nil {
		if decl := topo.Find(instance); decl != nil && decl.Ephemeral {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"agent-team: %q is declared ephemeral; cannot attach. Use `agent-team logs %s --follow` to watch its output.\n",
				instance, instance)
			return exitErr(2)
		}
	}

	meta, err := lookupInstanceMeta(dc, instance)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(2)
	}
	if meta.SessionID == "" {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"agent-team: %q has no session id recorded; cannot attach. Has it been dispatched yet?\n",
			instance)
		return exitErr(2)
	}

	// Stop the running child (if any). An already-stopped instance is a no-op
	// on the daemon side — we proceed straight to claude --resume.
	if meta.Status == daemon.StatusRunning {
		if err := dc.StopInstance(instance); err != nil {
			return fmt.Errorf("agent-team: stop %s: %w", instance, err)
		}
	}

	fmt.Fprintf(cmd.OutOrStdout(),
		"agent-team: attaching to %s (session=%s)...\n", instance, meta.SessionID)

	resumeErr := execClaudeAttach(cmd, []string{"--resume", meta.SessionID}, target)

	if noResume {
		fmt.Fprintf(cmd.OutOrStdout(),
			"agent-team: %s left in stopped state. Run `agent-team start %s` to resume under the daemon.\n",
			instance, instance)
		return resumeErr
	}

	if startErr := dc.StartInstance(instance); startErr != nil {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"agent-team: claude session ended but daemon `start` failed: %v\n  Run `agent-team start %s` manually.\n",
			startErr, instance)
		if resumeErr != nil {
			return resumeErr
		}
		return exitErr(1)
	}
	fmt.Fprintf(cmd.OutOrStdout(),
		"agent-team: %s resumed under daemon.\n", instance)
	return resumeErr
}

// lookupInstanceMeta fetches the daemon's metadata for one instance. Returns a
// helpful error when the daemon doesn't know the instance, so the CLI message
// is friendlier than a raw "not found".
func lookupInstanceMeta(dc *daemonClient, instance string) (*daemon.Metadata, error) {
	insts, err := dc.Instances()
	if err != nil {
		return nil, fmt.Errorf("query daemon: %w", err)
	}
	for _, m := range insts {
		if m.Instance == instance {
			return m, nil
		}
	}
	return nil, fmt.Errorf("instance %q not known to the daemon", instance)
}

// execClaudeAttach is split out so tests can intercept the exec without
// requiring a real claude binary. The default wires stdin/stdout/stderr to the
// user's terminal so claude's TUI is fully interactive.
var execClaudeAttach = func(cmd *cobra.Command, args []string, cwd string) error {
	c := exec.Command("claude", args...)
	c.Dir = cwd
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
			fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: `claude` CLI not found in PATH. Install Claude Code first.")
			return exitErr(127)
		}
		var exitErrTyped *exec.ExitError
		if errors.As(err, &exitErrTyped) {
			return exitErr(exitErrTyped.ExitCode())
		}
		return err
	}
	return nil
}
