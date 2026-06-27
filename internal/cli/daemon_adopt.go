package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/runtimebin"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

type daemonAdoptOptions struct {
	Agent         string
	PID           int
	PIDFile       string
	Workspace     string
	Runtime       string
	RuntimeBinary string
	SessionID     string
	StartedAt     string
	Job           string
	Step          string
	Ticket        string
	Branch        string
	PR            string
	LogPath       string
	Force         bool
	DryRun        bool
	JSON          bool
	Format        *template.Template
	FollowUp      []daemonAdoptFollowUpScope
}

type daemonAdoptFollowUpScope struct {
	Kind string
	Name string
	Step string
}

type daemonAdoptResult struct {
	Action     string           `json:"action"`
	Changed    bool             `json:"changed"`
	DryRun     bool             `json:"dry_run,omitempty"`
	Reconciled bool             `json:"reconciled,omitempty"`
	JobChanged bool             `json:"job_changed,omitempty"`
	Message    string           `json:"message,omitempty"`
	Actions    []string         `json:"actions,omitempty"`
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
		pidFile       string
		workspace     string
		runtimeKind   string
		runtimeBinary string
		sessionID     string
		startedAt     string
		jobID         string
		stepID        string
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
				PIDFile:       pidFile,
				Workspace:     workspace,
				Runtime:       runtimeKind,
				RuntimeBinary: runtimeBinary,
				SessionID:     sessionID,
				StartedAt:     startedAt,
				Job:           jobID,
				Step:          stepID,
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
	cmd.Flags().StringVar(&pidFile, "pid-file", "", "Read the live process PID to adopt from this file. Cannot be combined with --pid.")
	cmd.Flags().StringVar(&workspace, "workspace", "", "Workspace path for the adopted process. Defaults to the repo root.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for the adopted process (claude or codex). Defaults to repo/env selection.")
	cmd.Flags().StringVar(&runtimeBinary, "runtime-bin", "", "Runtime binary or wrapper used by the adopted process.")
	cmd.Flags().StringVar(&sessionID, "session-id", "", "Runtime session id, when known and resumable.")
	cmd.Flags().StringVar(&startedAt, "started-at", "", "Process start time as RFC3339. Defaults to now, or existing metadata for the same PID.")
	cmd.Flags().StringVar(&jobID, "job", "", "Owning job id to record on the adopted metadata.")
	cmd.Flags().StringVar(&stepID, "step", "", "Pipeline step id to mark as owned by the adopted process. Requires --job.")
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

func resolveDaemonAdoptPID(pid int, pidFile string) (int, error) {
	pidFile = strings.TrimSpace(pidFile)
	if pid > 0 && pidFile != "" {
		return 0, fmt.Errorf("--pid and --pid-file cannot be combined")
	}
	if pidFile == "" {
		if pid <= 0 {
			return 0, fmt.Errorf("--pid or --pid-file is required and must be > 0")
		}
		return pid, nil
	}
	body, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, fmt.Errorf("read --pid-file: %w", err)
	}
	raw := strings.TrimSpace(string(body))
	if raw == "" {
		return 0, fmt.Errorf("--pid-file must contain a positive integer PID")
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("--pid-file must contain a positive integer PID")
	}
	return parsed, nil
}

func runDaemonAdopt(cmd *cobra.Command, target, instance string, opts daemonAdoptOptions) error {
	label := daemonAdoptCommandLabel(cmd)
	pid, err := resolveDaemonAdoptPID(opts.PID, opts.PIDFile)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", label, err)
		return exitErr(2)
	}
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	if strings.TrimSpace(opts.Step) != "" && job.IDFromInput(opts.Job) == "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: --step requires --job.\n", label)
		return exitErr(2)
	}
	repoRoot := filepath.Dir(teamDir)
	jobDefaults, err := loadDaemonAdoptJobDefaults(teamDir, opts.Job, opts.Step)
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
		PID:           pid,
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
		Actions:  daemonAdoptFollowUpActions(meta, opts.FollowUp),
		Metadata: meta,
	}
	if meta != nil {
		result.Job, result.JobChanged, err = updateJobAfterDaemonAdopt(teamDir, meta, opts.Step, opts.DryRun, time.Now().UTC())
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

func loadDaemonAdoptJobDefaults(teamDir, rawID, stepID string) (daemonAdoptJobDefaults, error) {
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
	step, err := jobStepForAdoptionByID(j, stepID)
	if err != nil {
		return daemonAdoptJobDefaults{}, err
	}
	return daemonAdoptJobDefaults{
		Agent:     defaultJobAdoptAgentForStep(j, step),
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

func updateJobAfterDaemonAdopt(teamDir string, meta *daemon.Metadata, stepID string, dryRun bool, now time.Time) (*job.Job, bool, error) {
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
	before := cloneJobForEventReconcile(j)
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
	adoptedStepID, adoptedStepChanged, err := applyDaemonAdoptToPipelineStep(j, strings.TrimSpace(meta.Instance), stepID, now)
	if err != nil {
		return nil, false, err
	}
	j.LastEvent = "adopted"
	j.LastStatus = "adopted external process " + strings.TrimSpace(meta.Instance)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	j.UpdatedAt = now.UTC()
	changed := jobEventReconcileChanged(before, j)
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
	if adoptedStepID != "" {
		data["step"] = adoptedStepID
		data["step_changed"] = fmt.Sprint(adoptedStepChanged)
	}
	if err := writeJobWithAudit(teamDir, j, "adopted", "cli", j.LastStatus, data); err != nil {
		return nil, false, err
	}
	return j, true, nil
}

func applyDaemonAdoptToPipelineStep(j *job.Job, instance string, stepID string, now time.Time) (string, bool, error) {
	instance = strings.TrimSpace(instance)
	if j == nil || instance == "" {
		return "", false, nil
	}
	step, err := jobStepForAdoptionByID(j, stepID)
	if err != nil {
		return "", false, err
	}
	if step == nil {
		return "", false, nil
	}
	beforeStatus := step.Status
	beforeInstance := strings.TrimSpace(step.Instance)
	beforeStartedAt := step.StartedAt
	step.Status = job.StatusRunning
	step.Instance = instance
	if step.StartedAt.IsZero() {
		step.StartedAt = now.UTC()
	}
	step.FinishedAt = time.Time{}
	changed := beforeStatus != step.Status || beforeInstance != instance || !beforeStartedAt.Equal(step.StartedAt)
	return step.ID, changed, nil
}

func jobStepForAdoption(j *job.Job) *job.Step {
	if j == nil || len(j.Steps) == 0 {
		return nil
	}
	if step := firstJobStepWithStatus(j, job.StatusRunning); step != nil {
		return step
	}
	return nextReadyJobStep(j)
}

func jobStepForAdoptionByID(j *job.Job, stepID string) (*job.Step, error) {
	stepID = strings.TrimSpace(stepID)
	if stepID == "" {
		return jobStepForAdoption(j), nil
	}
	idx := jobStepIndex(j, stepID)
	if idx == -1 {
		return nil, fmt.Errorf("step %q not found", stepID)
	}
	return &j.Steps[idx], nil
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
	if len(result.Actions) > 0 {
		label := "next"
		if result.DryRun {
			label = "after apply"
		}
		fmt.Fprintf(w, "%s:\n", label)
		for _, action := range result.Actions {
			fmt.Fprintf(w, "  %s\n", action)
		}
	}
	return nil
}

func daemonAdoptFollowUpActions(meta *daemon.Metadata, scopes []daemonAdoptFollowUpScope) []string {
	if meta == nil {
		return nil
	}
	var actions []string
	instance := strings.TrimSpace(meta.Instance)
	if instance != "" {
		actions = append(actions,
			"agent-team inspect "+instance,
			"agent-team logs "+instance+" --follow",
		)
		if strings.TrimSpace(meta.Runtime) == string(runtimebin.KindCodex) {
			actions = append(actions, "agent-team logs "+instance+" --last-message")
		}
		actions = append(actions, "agent-team resume-plan "+instance)
	}
	if id := job.NormalizeID(meta.Job); id != "" {
		actions = append(actions,
			"agent-team job show "+id,
			"agent-team job logs "+id+" --follow",
		)
		if strings.TrimSpace(meta.Runtime) == string(runtimebin.KindCodex) {
			actions = append(actions, "agent-team job logs "+id+" --last-message")
		}
		actions = append(actions, "agent-team job resume-plan "+id)
	}
	for _, scope := range scopes {
		actions = append(actions, daemonAdoptScopedFollowUpActions(meta, scope)...)
	}
	return actions
}

func daemonAdoptScopedFollowUpActions(meta *daemon.Metadata, scope daemonAdoptFollowUpScope) []string {
	name := strings.TrimSpace(scope.Name)
	if meta == nil || name == "" {
		return nil
	}
	stepFlag := ""
	if step := strings.TrimSpace(scope.Step); step != "" {
		stepFlag = " --step " + step
	}
	codex := strings.TrimSpace(meta.Runtime) == string(runtimebin.KindCodex)
	switch strings.TrimSpace(scope.Kind) {
	case "pipeline":
		actions := []string{
			"agent-team pipeline status " + name,
			"agent-team pipeline logs " + name + " --follow",
		}
		if codex {
			actions = append(actions, "agent-team pipeline logs "+name+" --last-message")
		}
		return append(actions, "agent-team pipeline resume-plan "+name+stepFlag)
	case "team":
		actions := []string{
			"agent-team team status " + name,
			"agent-team team logs " + name + " --follow",
		}
		if codex {
			actions = append(actions, "agent-team team logs "+name+" --last-message")
		}
		return append(actions, "agent-team team resume-plan "+name+stepFlag)
	default:
		return nil
	}
}
