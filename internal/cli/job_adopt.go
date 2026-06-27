package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jamesaud/agent-team/internal/job"
	"github.com/spf13/cobra"
)

func newJobAdoptCmd() *cobra.Command {
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
		Use:   "adopt <job-id>",
		Short: "Adopt a live external process as a job's owning instance.",
		Long: "Adopt a live external process into daemon metadata and sync the durable job ownership fields. " +
			"Defaults such as agent, workspace, branch, PR, and ticket come from the job file when present.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOut && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job adopt: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commandsOnly && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job adopt: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commandsOnly && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job adopt: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			tmpl, err := parseDaemonAdoptFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job adopt: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			return runJobAdoptForJob(cmd, "agent-team job adopt", teamDir, j, jobAdoptCommandOptions{
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
				},
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&instance, "instance", "", "Instance name that should own the job. Defaults to selected or active step ownership, then job ownership.")
	cmd.Flags().StringVar(&stepID, "step", "", "Pipeline step id to mark as owned by the adopted process.")
	cmd.Flags().StringVar(&agent, "agent", "", "Agent name for the adopted instance. Defaults to the job target.")
	cmd.Flags().IntVar(&pid, "pid", 0, "Live process PID to adopt.")
	cmd.Flags().StringVar(&pidFile, "pid-file", "", "Read the live process PID to adopt from this file. Cannot be combined with --pid.")
	cmd.Flags().StringVar(&workspace, "workspace", "", "Workspace path for the adopted process. Defaults to the job worktree, then repo root.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for the adopted process (claude or codex). Defaults to repo/env selection.")
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

type jobAdoptCommandOptions struct {
	Instance string
	Daemon   daemonAdoptOptions
}

func runJobAdoptForJob(cmd *cobra.Command, label, teamDir string, j *job.Job, opts jobAdoptCommandOptions) error {
	selectedStep, err := jobStepForAdoptionByID(j, opts.Daemon.Step)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", label, err)
		return exitErr(2)
	}
	selectedInstance := strings.TrimSpace(opts.Instance)
	if selectedInstance == "" {
		selectedInstance = defaultJobAdoptInstanceForStep(j, selectedStep)
	}
	if selectedInstance == "" {
		selectedInstance = strings.TrimSpace(j.Instance)
	}
	if selectedInstance == "" {
		selectedInstance = defaultJobAdoptInstanceForJob(j)
	}
	if selectedInstance == "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: --instance is required when it cannot be inferred from the job.\n", label)
		return exitErr(2)
	}
	repoRoot := filepath.Dir(teamDir)
	adopt := opts.Daemon
	if strings.TrimSpace(adopt.Agent) == "" {
		adopt.Agent = defaultJobAdoptAgentForStep(j, selectedStep)
	}
	if strings.TrimSpace(adopt.Workspace) == "" {
		adopt.Workspace = strings.TrimSpace(j.Worktree)
	}
	if strings.TrimSpace(adopt.Workspace) == "" {
		adopt.Workspace = repoRoot
	}
	if strings.TrimSpace(adopt.Branch) == "" {
		adopt.Branch = strings.TrimSpace(j.Branch)
	}
	if strings.TrimSpace(adopt.PR) == "" {
		adopt.PR = strings.TrimSpace(j.PR)
	}
	adopt.Job = j.ID
	adopt.Ticket = j.Ticket
	return runDaemonAdopt(cmd, repoRoot, selectedInstance, adopt)
}

func defaultJobAdoptInstanceForStep(j *job.Job, step *job.Step) string {
	if j == nil || step == nil {
		return ""
	}
	if instance := strings.TrimSpace(step.Instance); instance != "" {
		return instance
	}
	target := job.NormalizeID(step.Target)
	id := job.NormalizeID(j.ID)
	stepID := job.NormalizeID(step.ID)
	if target == "" || id == "" || stepID == "" {
		return ""
	}
	return target + "-" + id + "-" + stepID
}

func defaultJobAdoptInstanceForJob(j *job.Job) string {
	if j == nil {
		return ""
	}
	target := job.NormalizeID(j.Target)
	id := job.NormalizeID(j.ID)
	if target == "" || id == "" {
		return ""
	}
	return target + "-" + id
}

func defaultJobAdoptAgentForStep(j *job.Job, step *job.Step) string {
	if j == nil {
		return ""
	}
	if step != nil {
		if target := strings.TrimSpace(step.Target); target != "" {
			return target
		}
	}
	return strings.TrimSpace(j.Target)
}
