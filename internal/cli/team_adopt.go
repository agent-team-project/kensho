package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/agent-team-project/agent-team/internal/job"
	"github.com/spf13/cobra"
)

func newTeamAdoptCmd() *cobra.Command {
	var (
		repo          string
		instance      string
		stepID        string
		agent         string
		pid           int
		pidFile       string
		workspace     string
		runtimeKind   string
		runtimeBinary string
		sessionID     string
		startedAt     string
		branch        string
		pr            string
		logPath       string
		force         bool
		dryRun        bool
		commandsOnly  bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "adopt <team> <job-id>",
		Short: "Adopt a live external process for a team-owned job.",
		Long: "Adopt a live external process into daemon metadata and sync the durable job ownership fields, " +
			"after verifying the job belongs to the named team.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOut && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team adopt: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commandsOnly && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team adopt: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commandsOnly && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team adopt: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			tmpl, err := parseDaemonAdoptFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team adopt: %v\n", err)
				return exitErr(2)
			}
			teamName := strings.TrimSpace(args[0])
			if teamName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team adopt: team name is required.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			top, team, err := loadTopologyTeam(teamDir, teamName)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team adopt: %v\n", err)
				return exitErr(1)
			}
			j, err := job.Read(teamDir, args[1])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team adopt: %v\n", err)
				return exitErr(1)
			}
			if len(teamJobs(top, team, []*job.Job{j})) == 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team adopt: job %q is not owned by team %q.\n", j.ID, team.Name)
				return exitErr(2)
			}
			return runJobAdoptForJob(cmd, "agent-team team adopt", teamDir, j, jobAdoptCommandOptions{
				Instance: strings.TrimSpace(instance),
				Daemon: daemonAdoptOptions{
					Agent:         strings.TrimSpace(agent),
					PID:           pid,
					PIDFile:       pidFile,
					Workspace:     strings.TrimSpace(workspace),
					Runtime:       runtimeKind,
					RuntimeBinary: runtimeBinary,
					SessionID:     sessionID,
					StartedAt:     startedAt,
					Step:          stepID,
					Branch:        strings.TrimSpace(branch),
					PR:            strings.TrimSpace(pr),
					LogPath:       logPath,
					Force:         force,
					DryRun:        dryRun,
					Commands:      commandsOnly,
					JSON:          jsonOut,
					Format:        tmpl,
					CommandScope:  operatorCommandScopeFromCommand(cmd, repo, "repo"),
					FollowUp: []daemonAdoptFollowUpScope{{
						Kind: "team",
						Name: team.Name,
						Step: stepID,
					}},
				},
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&instance, "instance", "", "Instance name that should own the job. Defaults to selected or active step ownership, then job ownership.")
	cmd.Flags().StringVar(&stepID, "step", "", "Pipeline step id to mark as owned by the adopted process.")
	cmd.Flags().StringVar(&agent, "agent", "", "Agent name for the adopted instance. Defaults to the selected step target or job target.")
	cmd.Flags().IntVar(&pid, "pid", 0, "Live process PID to adopt.")
	cmd.Flags().StringVar(&pidFile, "pid-file", "", "Read the live process PID to adopt from this file. Cannot be combined with --pid.")
	cmd.Flags().StringVar(&workspace, "workspace", "", "Workspace path for the adopted process. Defaults to the job worktree, then repo root.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for the adopted process (claude, codex, or docker). Defaults to repo/env selection.")
	cmd.Flags().StringVar(&runtimeBinary, "runtime-bin", "", "Runtime binary or wrapper used by the adopted process.")
	cmd.Flags().StringVar(&sessionID, "session-id", "", "Runtime session id, when known and resumable.")
	cmd.Flags().StringVar(&startedAt, "started-at", "", "Process start time as RFC3339. Defaults to now, or existing metadata for the same PID.")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch name to record. Defaults to the job branch.")
	cmd.Flags().StringVar(&pr, "pr", "", "PR URL to record. Defaults to the job PR.")
	cmd.Flags().StringVar(&logPath, "log-path", "", "Runtime log path, if the external process already writes to one.")
	cmd.Flags().BoolVar(&force, "force", false, "Replace existing live metadata for the instance.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview adoption without writing metadata or job state.")
	cmd.Flags().BoolVar(&commandsOnly, "commands", false, "Print only follow-up commands, one per line, after adoption planning or apply.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the adoption result with a Go template, e.g. '{{.Job.ID}} {{.Metadata.Instance}}'.")
	return cmd
}
