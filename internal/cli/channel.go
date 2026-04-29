package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// newChannelCmd builds `agent-team channel` — the resource group for the
// daemon-managed pub/sub channels (SQU-26). Mirrors the shape of `instance`.
func newChannelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "channel",
		Short: "Manage daemon-managed pub/sub channels.",
	}
	cmd.AddCommand(newChannelLsCmd())
	cmd.AddCommand(newChannelShowCmd())
	cmd.AddCommand(newChannelPublishCmd())
	cmd.AddCommand(newChannelRmCmd())
	return cmd
}

// newChannelsCmd is the top-level alias `agent-team channels`, mirroring the
// `agent-team ps` shortcut for `instance ps`.
func newChannelsCmd() *cobra.Command {
	var target string
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "channels",
		Short: "List all pub/sub channels (alias for `channel ls`).",
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			return runChannelLs(cmd.OutOrStdout(), cmd.ErrOrStderr(), teamDir)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	return cmd
}

func newChannelLsCmd() *cobra.Command {
	var target string
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List all channels: subscriber count, message count, last activity.",
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			return runChannelLs(cmd.OutOrStdout(), cmd.ErrOrStderr(), teamDir)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	return cmd
}

func newChannelShowCmd() *cobra.Command {
	var target string
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show one channel's summary plus its tail of recent messages.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			return runChannelShow(cmd.OutOrStdout(), cmd.ErrOrStderr(), teamDir, args[0])
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	return cmd
}

func newChannelPublishCmd() *cobra.Command {
	var (
		target string
		sender string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "publish <name> <body...>",
		Short: "Publish a message to a channel from the CLI (creates the channel if missing).",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			body := strings.Join(args[1:], " ")
			return runChannelPublish(cmd.OutOrStdout(), cmd.ErrOrStderr(), teamDir, args[0], sender, body)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().StringVar(&sender, "sender", "(cli)", "Sender label recorded with the message.")
	return cmd
}

func newChannelRmCmd() *cobra.Command {
	var (
		target string
		force  bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Delete a channel and all of its on-disk state.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			if !force {
				ok, err := confirm(cmd, fmt.Sprintf("Delete channel %s?", args[0]))
				if err != nil {
					return err
				}
				if !ok {
					fmt.Fprintln(cmd.OutOrStdout(), "(aborted)")
					return nil
				}
			}
			return runChannelRm(cmd.OutOrStdout(), cmd.ErrOrStderr(), teamDir, args[0])
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Skip confirmation.")
	return cmd
}

// --- run* helpers --------------------------------------------------------

func runChannelLs(stdout, stderr io.Writer, teamDir string) error {
	client, err := requireDaemon(stderr, teamDir)
	if err != nil {
		return err
	}
	infos, err := client.ChannelList()
	if err != nil {
		return err
	}
	if len(infos) == 0 {
		fmt.Fprintln(stdout, "(no channels)")
		return nil
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CHANNEL\tSUBSCRIBERS\tMESSAGES\tLAST")
	now := time.Now()
	for _, info := range infos {
		last := "—"
		if !info.LastMessageTS.IsZero() {
			last = humanAge(now.Sub(info.LastMessageTS))
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\n", info.Name, info.Subscribers, info.MessageCount, last)
	}
	return tw.Flush()
}

func runChannelShow(stdout, stderr io.Writer, teamDir, name string) error {
	client, err := requireDaemon(stderr, teamDir)
	if err != nil {
		return err
	}
	infos, err := client.ChannelList()
	if err != nil {
		return err
	}
	var info *channelInfo
	for _, i := range infos {
		if i.Name == name {
			info = i
			break
		}
	}
	if info == nil {
		fmt.Fprintf(stderr, "agent-team: no such channel: %s\n", name)
		return exitErr(2)
	}
	fmt.Fprintf(stdout, "channel:       %s\n", info.Name)
	fmt.Fprintf(stdout, "subscribers:   %d\n", info.Subscribers)
	fmt.Fprintf(stdout, "messages:      %d\n", info.MessageCount)
	if !info.LastMessageTS.IsZero() {
		fmt.Fprintf(stdout, "last message:  %s\n", info.LastMessageTS.Format(time.RFC3339))
	} else {
		fmt.Fprintln(stdout, "last message:  —")
	}

	// Tail the most recent up-to-10 messages by querying since=0 then
	// keeping the tail. We pass a synthetic instance label "(cli-show)" with
	// since=0 — the server doesn't require subscription when since is given.
	// A real subscriber's cursor is unaffected.
	since := int64(0)
	dr, err := client.ChannelDrain(context.Background(), name, "(cli-show)", &since, 0)
	if err != nil {
		return err
	}
	tail := dr.Messages
	const maxTail = 10
	if len(tail) > maxTail {
		tail = tail[len(tail)-maxTail:]
	}
	if len(tail) == 0 {
		return nil
	}
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "recent (%d shown):\n", len(tail))
	for _, m := range tail {
		fmt.Fprintf(stdout, "  [seq=%d] %s  %s\n     %s\n",
			m.Seq, m.Sender, m.TS.Format(time.RFC3339), m.Body)
	}
	return nil
}

func runChannelPublish(stdout, stderr io.Writer, teamDir, name, sender, body string) error {
	client, err := requireDaemon(stderr, teamDir)
	if err != nil {
		return err
	}
	res, err := client.ChannelPublish(name, sender, body)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "  published seq=%d  %s\n", res.Seq, res.TS.Format(time.RFC3339))
	return nil
}

func runChannelRm(stdout, stderr io.Writer, teamDir, name string) error {
	client, err := requireDaemon(stderr, teamDir)
	if err != nil {
		return err
	}
	if err := client.ChannelDelete(name); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "  removed %s\n", name)
	return nil
}

// requireDaemon centralises the "daemon must be running for this command"
// error path. Channels are a daemon-only feature; without one there's nothing
// to talk to.
func requireDaemon(stderr io.Writer, teamDir string) (*daemonClient, error) {
	client, err := newDaemonClient(teamDir)
	if err != nil {
		if errors.Is(err, errDaemonNotRunning) {
			fmt.Fprintln(stderr,
				"agent-team: no daemon running — start it with `agent-team daemon start`.")
			return nil, exitErr(1)
		}
		return nil, err
	}
	return client, nil
}

// humanAge returns a compact human-readable duration ("3m", "2h", "1d").
func humanAge(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
