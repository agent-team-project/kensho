// Package cli wires the agent-team Cobra command tree.
package cli

import (
	"fmt"

	"github.com/jamesaud/agent-team/internal/buildinfo"
	"github.com/spf13/cobra"
)

// Version is the agent-team release version. The default below is the
// in-tree development value; release builds override it via ldflags
// (`-X github.com/jamesaud/agent-team/internal/cli.Version=...`) — see
// `.goreleaser.yaml`.
var Version = "0.1.0"

// BuildInfo returns the current CLI binary identity.
func BuildInfo() buildinfo.Info {
	return buildinfo.Current(Version)
}

const (
	rootRepoFlagName         = "repo"
	repoFlagHelp             = "Repo root containing .agent_team."
	legacyRepoTargetFlagHelp = "Repo root containing .agent_team (legacy; prefer global --repo)."
)

// ExitCode is a sentinel error type used to signal a non-zero process exit
// code from a Cobra `RunE`. `cmd/agent-team/main.go` unwraps it via
// `errors.As` and calls `os.Exit` with the wrapped code.
type ExitCode int

func (e ExitCode) Error() string { return fmt.Sprintf("exit %d", int(e)) }

// NewRootCmd builds the root `agent-team` command with all subcommands attached.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "agent-team",
		Short: "Declare and launch a custom set of LLM agents and skills, vendored into any repo.",
		Long: "agent-team — declare and launch LLM agents and skills, vendored into any repo.\n\n" +
			"Docker-like shortcuts:\n" +
			"  agent-team up    = agent-team start\n" +
			"  agent-team down  = agent-team stop\n" +
			"  agent-team ls    = agent-team ps\n" +
			"  agent-team top   = agent-team stats\n" +
			"  agent-team exec  = agent-team attach",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       Version,
	}
	root.SetVersionTemplate("agent-team " + BuildInfo().VersionLine() + "\n")
	root.PersistentFlags().String(rootRepoFlagName, "", "Repo root containing .agent_team for commands that read repo state; overrides legacy repo-root --target flags.")
	root.AddCommand(newInitCmd())
	root.AddCommand(newUpgradeCmd())
	root.AddCommand(newStartCmd())
	root.AddCommand(newStopCmd())
	root.AddCommand(newKillCmd())
	root.AddCommand(newRestartCmd())
	root.AddCommand(newReloadCmd())
	root.AddCommand(newPlanCmd())
	root.AddCommand(newSyncCmd())
	root.AddCommand(newTickCmd())
	root.AddCommand(newDrainCmd())
	root.AddCommand(newRepairCmd())
	root.AddCommand(newOverviewCmd())
	root.AddCommand(newNextCmd())
	root.AddCommand(newResumePlanCmd())
	root.AddCommand(newAdoptCmd())
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
	root.AddCommand(newSendCmd())
	root.AddCommand(newInboxCmd())
	root.AddCommand(newDispatchCmd())
	root.AddCommand(newAgentCmd())
	root.AddCommand(newApprovalCmd())
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
	root.AddCommand(newShortcutsCmd())
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
