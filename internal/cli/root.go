// Package cli wires the agent-team Cobra command tree.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

const Version = "0.1.0"

// ExitCode is a sentinel error type used to signal a non-zero process exit
// code from a Cobra `RunE`. `cmd/agent-team/main.go` unwraps it via
// `errors.As` and calls `os.Exit` with the wrapped code.
type ExitCode int

func (e ExitCode) Error() string { return fmt.Sprintf("exit %d", int(e)) }

// NewRootCmd builds the root `agent-team` command with all subcommands attached.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "agent-team",
		Short:         "Declare and launch a custom set of Claude Code subagents and skills, vendored into any repo.",
		Long:          "agent-team — declare and launch Claude Code subagents and skills, vendored into any repo.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       Version,
	}
	root.SetVersionTemplate("agent-team " + Version + "\n")
	root.AddCommand(newInitCmd())
	root.AddCommand(newRunCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newInstanceCmd())
	root.AddCommand(newTemplateCmd())
	return root
}
