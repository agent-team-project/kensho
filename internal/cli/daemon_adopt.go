package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

type daemonAdoptOptions struct {
	Agent         string
	PID           int
	Workspace     string
	Runtime       string
	RuntimeBinary string
	SessionID     string
	StartedAt     string
	Job           string
	Ticket        string
	Branch        string
	PR            string
	LogPath       string
	Force         bool
	DryRun        bool
	JSON          bool
	Format        *template.Template
}

type daemonAdoptResult struct {
	Action     string           `json:"action"`
	Changed    bool             `json:"changed"`
	DryRun     bool             `json:"dry_run,omitempty"`
	Reconciled bool             `json:"reconciled,omitempty"`
	JobChanged bool             `json:"job_changed,omitempty"`
	Message    string           `json:"message,omitempty"`
	Metadata   *daemon.Metadata `json:"metadata"`
	Job        *job.Job         `json:"job,omitempty"`
}

func newDaemonAdoptCmd() *cobra.Command {
	return newAdoptExternalProcessCmd(adoptExternalProcessCommandConfig{
		Short: "Adopt a live external process into daemon metadata.",
		Long: "Adopt a live external process by writing daemon runtime metadata for it. " +
			"Adopted processes become visible to ps, inspect, monitor, stop, and reconcile. " +
			"The daemon cannot wait on an adopted process it did not spawn, so later exits are observed by daemon reconcile.",
	})
}

func newAdoptCmd() *cobra.Command {
	return newAdoptExternalProcessCmd(adoptExternalProcessCommandConfig{
		Short: "Adopt a live external runtime process.",
		Long: "Adopt a live external runtime process by writing daemon runtime metadata for it. " +
			"Adopted processes become visible to ps, inspect, monitor, stop, and reconcile. " +
			"This is a shorter alias for `agent-team runtime adopt`.",
		RepoFlag: true,
	})
}

func newRuntimeAdoptCmd() *cobra.Command {
	return newAdoptExternalProcessCmd(adoptExternalProcessCommandConfig{
		Short: "Adopt a live external runtime process.",
		Long: "Adopt a live external runtime process by writing daemon runtime metadata for it. " +
			"Adopted processes become visible to ps, inspect, monitor, stop, and reconcile. " +
			"Use this when a Claude or Codex process was started outside agent-team but should be tracked by the repo daemon.",
	})
}

type adoptExternalProcessCommandConfig struct {
	Short    string
	Long     string
	RepoFlag bool
}

func newAdoptExternalProcessCmd(cfg adoptExternalProcessCommandConfig) *cobra.Command {
	var (
		target        string
		agent         string
		pid           int
		workspace     string
		runtimeKind   string
		runtimeBinary string
		sessionID     string
		startedAt     string
		jobID         string
		ticket        string
		branch        string
		pr            string
		logPath       string
		force         bool
		dryRun        bool
		jsonOut       bool
		format        string
	)
	cwd, _ := filepath.Abs(".")
	cmd := &cobra.Command{
		Use:   "adopt <instance>",
		Short: cfg.Short,
		Long:  cfg.Long,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			label := daemonAdoptCommandLabel(cmd)
			if jsonOut && format != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: --format cannot be combined with --json.\n", label)
				return exitErr(2)
			}
			tmpl, err := parseDaemonAdoptFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", label, err)
				return exitErr(2)
			}
			return runDaemonAdopt(cmd, target, args[0], daemonAdoptOptions{
				Agent:         agent,
				PID:           pid,
				Workspace:     workspace,
				Runtime:       runtimeKind,
				RuntimeBinary: runtimeBinary,
				SessionID:     sessionID,
				StartedAt:     startedAt,
				Job:           jobID,
				Ticket:        ticket,
				Branch:        branch,
				PR:            pr,
				LogPath:       logPath,
				Force:         force,
				DryRun:        dryRun,
				JSON:          jsonOut,
				Format:        tmpl,
			})
		},
	}
	if cfg.RepoFlag {
		cmd.Flags().StringVar(&target, "repo", cwd, repoFlagHelp)
	} else {
		cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	}
	cmd.Flags().StringVar(&agent, "agent", "", "Agent name for the adopted instance. Inferred from instances.toml when omitted.")
	cmd.Flags().IntVar(&pid, "pid", 0, "Live process PID to adopt.")
	cmd.Flags().StringVar(&workspace, "workspace", "", "Workspace path for the adopted process. Defaults to the repo root.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for the adopted process (claude or codex). Defaults to repo/env selection.")
	cmd.Flags().StringVar(&runtimeBinary, "runtime-bin", "", "Runtime binary or wrapper used by the adopted process.")
	cmd.Flags().StringVar(&sessionID, "session-id", "", "Runtime session id, when known and resumable.")
	cmd.Flags().StringVar(&startedAt, "started-at", "", "Process start time as RFC3339. Defaults to now, or existing metadata for the same PID.")
	cmd.Flags().StringVar(&jobID, "job", "", "Owning job id to record on the adopted metadata.")
	cmd.Flags().StringVar(&ticket, "ticket", "", "Ticket id to record on the adopted metadata.")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch name to record on the adopted metadata.")
	cmd.Flags().StringVar(&pr, "pr", "", "PR URL to record on the adopted metadata.")
	cmd.Flags().StringVar(&logPath, "log-path", "", "Runtime log path, if the external process already writes to one.")
	cmd.Flags().BoolVar(&force, "force", false, "Replace existing live metadata for the instance.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview adoption without writing metadata.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the adoption result with a Go template, e.g. '{{.Metadata.Instance}} {{.Metadata.PID}}'.")
	return cmd
}

func daemonAdoptCommandLabel(cmd *cobra.Command) string {
	if cmd == nil {
		return "agent-team daemon adopt"
	}
	if path := strings.TrimSpace(cmd.CommandPath()); path != "" {
		return path
	}
	return "agent-team daemon adopt"
}

func parseDaemonAdoptFormat(format string) (*template.Template, error) {
	return parseDaemonFormat("daemon-adopt-format", format)
}

func runDaemonAdopt(cmd *cobra.Command, target, instance string, opts daemonAdoptOptions) error {
	label := daemonAdoptCommandLabel(cmd)
	if opts.PID <= 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: --pid is required and must be > 0.\n", label)
		return exitErr(2)
	}
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	repoRoot := filepath.Dir(teamDir)
	jobDefaults, err := loadDaemonAdoptJobDefaults(teamDir, opts.Job)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", label, err)
		return exitErr(1)
	}
	workspace := strings.TrimSpace(opts.Workspace)
	if workspace == "" {
		workspace = strings.TrimSpace(jobDefaults.Workspace)
	}
	if workspace == "" {
		workspace = repoRoot
	}
	if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}
	agentDefault := strings.TrimSpace(opts.Agent)
	if agentDefault == "" {
		agentDefault = strings.TrimSpace(jobDefaults.Agent)
	}
	agent, err := inferDaemonAdoptAgent(teamDir, instance, agentDefault)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", label, err)
		return exitErr(2)
	}
	rt, err := runtimeFromConfigWithOverrides(filepath.Join(teamDir, "config.toml"), runtimeSelection{
		Kind:   opts.Runtime,
		Binary: opts.RuntimeBinary,
	})
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", label, err)
		return exitErr(2)
	}
	startedAt, err := parseDaemonAdoptStartedAt(opts.StartedAt)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", label, err)
		return exitErr(2)
	}
	logPath := strings.TrimSpace(opts.LogPath)
	if logPath != "" {
		if abs, err := filepath.Abs(logPath); err == nil {
			logPath = abs
		}
	}
	input := daemon.AdoptInput{
		Instance:      instance,
		Agent:         agent,
		Job:           opts.Job,
		Ticket:        adoptDefaultString(opts.Ticket, jobDefaults.Ticket),
		Branch:        adoptDefaultString(opts.Branch, jobDefaults.Branch),
		PR:            adoptDefaultString(opts.PR, jobDefaults.PR),
		Runtime:       string(rt.Kind),
		RuntimeBinary: rt.Binary,
		Workspace:     workspace,
		PID:           opts.PID,
		SessionID:     opts.SessionID,
		StartedAt:     startedAt,
		LogPath:       logPath,
		Force:         opts.Force,
	}
	var meta *daemon.Metadata
	changed := false
	if opts.DryRun {
		meta, changed, err = daemon.PrepareAdoptMetadata(daemon.DaemonRoot(teamDir), input, time.Now().UTC())
	} else {
		meta, changed, err = daemon.AdoptMetadata(daemon.DaemonRoot(teamDir), input, time.Now().UTC())
	}
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", label, err)
		return exitErr(1)
	}
	result := daemonAdoptResult{
		Action:   "adopt",
		Changed:  changed,
		DryRun:   opts.DryRun,
		Metadata: meta,
	}
	if meta != nil {
		result.Job, result.JobChanged, err = updateJobAfterDaemonAdopt(teamDir, meta, opts.DryRun, time.Now().UTC())
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", label, err)
			return exitErr(1)
		}
	}
	if !opts.DryRun {
		result.Reconciled, result.Message, err = reconcileAfterDaemonAdopt(teamDir)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", label, err)
			return exitErr(1)
		}
	}
	return renderDaemonAdoptResult(cmd.OutOrStdout(), result, opts)
}

type daemonAdoptJobDefaults struct {
	Agent     string
	Ticket    string
	Branch    string
	PR        string
	Workspace string
}

func loadDaemonAdoptJobDefaults(teamDir, rawID string) (daemonAdoptJobDefaults, error) {
	id := job.IDFromInput(rawID)
	if id == "" {
		return daemonAdoptJobDefaults{}, nil
	}
	j, err := job.Read(teamDir, id)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return daemonAdoptJobDefaults{}, nil
		}
		return daemonAdoptJobDefaults{}, err
	}
	return daemonAdoptJobDefaults{
		Agent:     strings.TrimSpace(j.Target),
		Ticket:    strings.TrimSpace(j.Ticket),
		Branch:    strings.TrimSpace(j.Branch),
		PR:        strings.TrimSpace(j.PR),
		Workspace: strings.TrimSpace(j.Worktree),
	}, nil
}

func adoptDefaultString(explicit, fallback string) string {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		return explicit
	}
	return strings.TrimSpace(fallback)
}

func updateJobAfterDaemonAdopt(teamDir string, meta *daemon.Metadata, dryRun bool, now time.Time) (*job.Job, bool, error) {
	if meta == nil {
		return nil, false, nil
	}
	id := job.IDFromInput(meta.Job)
	if id == "" {
		return nil, false, nil
	}
	j, err := job.Read(teamDir, id)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	before := *j
	if strings.TrimSpace(meta.Instance) != "" {
		j.Instance = strings.TrimSpace(meta.Instance)
	}
	if strings.TrimSpace(meta.Ticket) != "" && strings.TrimSpace(j.Ticket) == "" {
		j.Ticket = strings.TrimSpace(meta.Ticket)
	}
	if strings.TrimSpace(meta.Branch) != "" {
		j.Branch = strings.TrimSpace(meta.Branch)
	}
	if strings.TrimSpace(meta.PR) != "" {
		j.PR = strings.TrimSpace(meta.PR)
	}
	if j.Status != job.StatusDone {
		j.Status = job.StatusRunning
	}
	j.LastEvent = "adopted"
	j.LastStatus = "adopted external process " + strings.TrimSpace(meta.Instance)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	j.UpdatedAt = now.UTC()
	changed := jobEventReconcileChanged(&before, j)
	if !changed || dryRun {
		return j, changed, nil
	}
	data := map[string]string{
		"instance": strings.TrimSpace(meta.Instance),
		"pid":      fmt.Sprintf("%d", meta.PID),
		"runtime":  strings.TrimSpace(meta.Runtime),
	}
	if strings.TrimSpace(meta.Branch) != "" {
		data["branch"] = strings.TrimSpace(meta.Branch)
	}
	if strings.TrimSpace(meta.PR) != "" {
		data["pr"] = strings.TrimSpace(meta.PR)
	}
	if err := writeJobWithAudit(teamDir, j, "adopted", "cli", j.LastStatus, data); err != nil {
		return nil, false, err
	}
	return j, true, nil
}

func inferDaemonAdoptAgent(teamDir, instance, explicit string) (string, error) {
	if agent := strings.TrimSpace(explicit); agent != "" {
		return agent, nil
	}
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return "", fmt.Errorf("load instances.toml to infer --agent: %w", err)
	}
	if top != nil {
		if inst := top.Find(instance); inst != nil && strings.TrimSpace(inst.Agent) != "" {
			return inst.Agent, nil
		}
		if inst, ok := declaredEphemeralOwner(top, instance, ""); ok && strings.TrimSpace(inst.Agent) != "" {
			return inst.Agent, nil
		}
	}
	return "", errors.New("--agent is required when the instance is not declared in instances.toml")
}

func parseDaemonAdoptStartedAt(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("--started-at must be RFC3339: %w", err)
	}
	return parsed.UTC(), nil
}

func reconcileAfterDaemonAdopt(teamDir string) (bool, string, error) {
	client, err := newDaemonClient(teamDir)
	if err != nil {
		if errors.Is(err, errDaemonNotRunning) {
			return false, "daemon not running; metadata will be loaded on next daemon start", nil
		}
		return false, "", err
	}
	if _, err := client.Reconcile(); err != nil {
		return false, "", err
	}
	return true, "daemon reconciled adoption", nil
}

func renderDaemonAdoptResult(w fmtWriter, result daemonAdoptResult, opts daemonAdoptOptions) error {
	if opts.JSON {
		return json.NewEncoder(w).Encode(result)
	}
	if opts.Format != nil {
		if err := opts.Format.Execute(w, result); err != nil {
			return err
		}
		_, err := fmt.Fprintln(w)
		return err
	}
	if result.Metadata == nil {
		return nil
	}
	prefix := "adopted"
	if result.DryRun {
		prefix = "would adopt"
	} else if !result.Changed {
		prefix = "already adopted"
	}
	fmt.Fprintf(w, "%s %s (pid=%d, agent=%s, runtime=%s)\n",
		prefix,
		result.Metadata.Instance,
		result.Metadata.PID,
		result.Metadata.Agent,
		result.Metadata.Runtime,
	)
	if result.Message != "" {
		fmt.Fprintf(w, "%s\n", result.Message)
	}
	if result.Job != nil {
		prefix := "job unchanged"
		if result.DryRun && result.JobChanged {
			prefix = "would update job"
		} else if result.JobChanged {
			prefix = "updated job"
		}
		fmt.Fprintf(w, "%s %s (status=%s, instance=%s)\n",
			prefix,
			result.Job.ID,
			result.Job.Status,
			result.Job.Instance,
		)
	}
	return nil
}
