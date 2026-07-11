package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/agent-team-project/agent-team/internal/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var uiTerminalOK = func(input io.Reader, output io.Writer) bool {
	in, inputOK := input.(interface{ Fd() uintptr })
	out, outputOK := output.(interface{ Fd() uintptr })
	return inputOK && outputOK && term.IsTerminal(int(in.Fd())) && term.IsTerminal(int(out.Fd()))
}

func newUICmd() *cobra.Command {
	var (
		target string
		once   bool
	)
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Open the read-only terminal command center.",
		Long: "Open the keyboard-complete read-only terminal UI over agent-teamd. " +
			"Endpoint and token discovery use the same shared daemon client as other CLI commands.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			capabilities := tui.Capabilities{
				Color: !environmentPresent("NO_COLOR") && !strings.EqualFold(os.Getenv("TERM"), "dumb"),
				Dumb:  strings.EqualFold(os.Getenv("TERM"), "dumb"),
			}
			options := tui.RunOptions{
				TeamDir: teamDir, Build: BuildInfo(), Capabilities: capabilities,
				Input: cmd.InOrStdin(), Output: cmd.OutOrStdout(), Clock: func() time.Time { return time.Now().UTC() },
			}
			if once {
				frame, code := tui.RunOnce(cmd.Context(), options)
				if _, err := io.WriteString(cmd.OutOrStdout(), frame); err != nil {
					return err
				}
				if code != 0 {
					return exitErr(code)
				}
				return nil
			}
			if !uiTerminalOK(cmd.InOrStdin(), cmd.OutOrStdout()) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team ui: interactive mode requires TTY stdin and stdout; use --once for pipes.")
				return exitErr(2)
			}
			return tui.Run(cmd.Context(), options)
		},
	}
	cmd.Flags().BoolVar(&once, "once", false, "Load one snapshot, render a plain 120x30 Overview frame, and exit.")
	return cmd
}

func environmentPresent(name string) bool {
	_, ok := os.LookupEnv(name)
	return ok
}
