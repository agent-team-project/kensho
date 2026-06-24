package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
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
	Message    string           `json:"message,omitempty"`
	Metadata   *daemon.Metadata `json:"metadata"`
}

func newDaemonAdoptCmd() *cobra.Command {
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
		Short: "Adopt a live external process into daemon metadata.",
		Long: "Adopt a live external process by writing daemon runtime metadata for it. " +
			"Adopted processes become visible to ps, inspect, monitor, stop, and reconcile. " +
			"The daemon cannot wait on an adopted process it did not spawn, so later exits are observed by daemon reconcile.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOut && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon adopt: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseDaemonAdoptFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team daemon adopt: %v\n", err)
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
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
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

func parseDaemonAdoptFormat(format string) (*template.Template, error) {
	return parseDaemonFormat("daemon-adopt-format", format)
}

func runDaemonAdopt(cmd *cobra.Command, target, instance string, opts daemonAdoptOptions) error {
	if opts.PID <= 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon adopt: --pid is required and must be > 0.")
		return exitErr(2)
	}
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	repoRoot := filepath.Dir(teamDir)
	workspace := strings.TrimSpace(opts.Workspace)
	if workspace == "" {
		workspace = repoRoot
	}
	if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}
	agent, err := inferDaemonAdoptAgent(teamDir, instance, opts.Agent)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team daemon adopt: %v\n", err)
		return exitErr(2)
	}
	rt, err := runtimeFromConfigWithOverrides(filepath.Join(teamDir, "config.toml"), runtimeSelection{
		Kind:   opts.Runtime,
		Binary: opts.RuntimeBinary,
	})
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team daemon adopt: %v\n", err)
		return exitErr(2)
	}
	startedAt, err := parseDaemonAdoptStartedAt(opts.StartedAt)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team daemon adopt: %v\n", err)
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
		Ticket:        opts.Ticket,
		Branch:        opts.Branch,
		PR:            opts.PR,
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
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team daemon adopt: %v\n", err)
		return exitErr(1)
	}
	result := daemonAdoptResult{
		Action:   "adopt",
		Changed:  changed,
		DryRun:   opts.DryRun,
		Metadata: meta,
	}
	if !opts.DryRun {
		result.Reconciled, result.Message, err = reconcileAfterDaemonAdopt(teamDir)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team daemon adopt: %v\n", err)
			return exitErr(1)
		}
	}
	return renderDaemonAdoptResult(cmd.OutOrStdout(), result, opts)
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
	return nil
}
