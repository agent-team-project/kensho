// Package cli wires the agent-team Cobra command tree.
package cli

import (
	"fmt"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/spf13/cobra"
)

// Version is the agent-team release version. The default below is the
// in-tree development value; release builds override it via ldflags
// (`-X github.com/agent-team-project/agent-team/internal/cli.Version=...`) — see
// `.goreleaser.yaml`.
var Version = "0.1.0"

// BuildInfo returns the current CLI binary identity.
func BuildInfo() buildinfo.Info {
	return buildinfo.Current(Version)
}

var enforceActivationBuild bool

// NewExecutableRootCmd builds the command tree for the shipped CLI process.
// Enforcement is selected by the compiled entrypoint, not by the executable
// path, so renaming or copying the binary cannot change its activation policy.
func NewExecutableRootCmd() *cobra.Command {
	enforceActivationBuild = true
	return NewRootCmd()
}

func requireActivationBuild() error {
	if !enforceActivationBuild {
		return nil
	}
	comparison := buildinfo.Compare(BuildInfo(), BuildInfo())
	if comparison.Comparable {
		return nil
	}
	return fmt.Errorf("activation needed: %s; rebuild from clean VCS metadata or with scripts/build.sh", comparison.Reason)
}

const (
	rootRepoFlagName = "repo"
	repoFlagHelp     = "Repo root containing .agent_team."
)

// ExitCode is a sentinel error type used to signal a non-zero process exit
// code from a Cobra `RunE`. `cmd/agent-team/main.go` unwraps it via
// `errors.As` and calls `os.Exit` with the wrapped code.
type ExitCode int

func (e ExitCode) Error() string { return fmt.Sprintf("exit %d", int(e)) }

// NewRootCmd builds an in-process command tree with activation enforcement
// disabled. The shipped executable must use NewExecutableRootCmd; tests and
// embedding helpers opt out through this constructor explicitly.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "agent-team",
		Short:         "Declare and launch a custom set of LLM agents and skills, vendored into any repo.",
		Long:          "agent-team — declare and launch LLM agents and skills, vendored into any repo.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       Version,
	}
	root.SetVersionTemplate("agent-team " + BuildInfo().VersionLine() + "\n")
	root.PersistentFlags().String(rootRepoFlagName, "", "Repo root containing .agent_team for commands that read repo state.")
	root.AddCommand(newResolveVerbCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newUpgradeCmd())
	root.AddCommand(newStartCmd())
	root.AddCommand(newStopCmd())
	root.AddCommand(newKillCmd())
	root.AddCommand(newExtendCmd())
	root.AddCommand(newRestartCmd())
	root.AddCommand(newReloadCmd())
	root.AddCommand(newPlanCmd())
	root.AddCommand(newSyncCmd())
	root.AddCommand(newTickCmd())
	root.AddCommand(newDrainCmd())
	root.AddCommand(newRepairCmd())
	root.AddCommand(newOverviewCmd())
	root.AddCommand(newUICmd())
	root.AddCommand(newNextCmd())
	root.AddCommand(newFeedbackCmd())
	root.AddCommand(newResumePlanCmd())
	root.AddCommand(newAdoptCmd())
	root.AddCommand(newReadCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newHealthCmd())
	root.AddCommand(newMonitorCmd())
	root.AddCommand(newWatchCmd())
	root.AddCommand(newSnapshotCmd())
	root.AddCommand(newGraphCmd())
	root.AddCommand(newSignaturesCmd())
	root.AddCommand(newInspectCmd())
	root.AddCommand(newRmCmd())
	root.AddCommand(newPruneCmd())
	root.AddCommand(newWaitCmd())
	root.AddCommand(newStatsCmd())
	root.AddCommand(newUsageCmd())
	root.AddCommand(newBudgetCmd())
	root.AddCommand(newOutcomesCmd())
	root.AddCommand(newSendCmd())
	root.AddCommand(newInboxCmd())
	root.AddCommand(newDispatchCmd())
	root.AddCommand(newAgentCmd())
	root.AddCommand(newDeploymentsCmd())
	root.AddCommand(newApprovalCmd())
	root.AddCommand(newTicketCmd())
	root.AddCommand(newJobCmd())
	root.AddCommand(newPipelineCmd())
	root.AddCommand(newTeamCmd())
	root.AddCommand(newScheduleCmd())
	root.AddCommand(newOutboxCmd())
	root.AddCommand(newQueueCmd())
	root.AddCommand(newLocksCmd())
	root.AddCommand(newIntakeCmd())
	root.AddCommand(newEventsCmd())
	root.AddCommand(newRuntimeCmd())
	root.AddCommand(newRunCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newInstanceCmd())
	root.AddCommand(newTemplateCmd())
	root.AddCommand(newDocsCmd())
	root.AddCommand(newDaemonCmd())
	root.AddCommand(newPsCmd())
	root.AddCommand(newLogsCmd())
	root.AddCommand(newAttachCmd())
	root.AddCommand(newChannelCmd())
	root.AddCommand(newChannelsCmd())
	root.AddCommand(newTopologyCmd())
	root.AddCommand(newEventCmd())
	return root
}
