package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/template"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/loader"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newInstanceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "instance",
		Short: "Manage agent instance state (.agent_team/state/<instance>/).",
	}
	cmd.AddCommand(newInstanceLsCmd())
	cmd.AddCommand(newInstancePsCmd())
	cmd.AddCommand(newInstanceShowCmd())
	cmd.AddCommand(newInstanceRmCmd())
	cmd.AddCommand(newInstanceUpCmd())
	cmd.AddCommand(newInstanceDownCmd())
	return cmd
}

func newInstanceLsCmd() *cobra.Command {
	var target string
	cwd, _ := os.Getwd()
	c := &cobra.Command{
		Use:   "ls",
		Short: "List instances (state dirs).",
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			stateRoot := filepath.Join(teamDir, "state")
			st, err := os.Stat(stateRoot)
			if err != nil || !st.IsDir() {
				fmt.Fprintln(cmd.OutOrStdout(), "(no instances)")
				return nil
			}
			entries, err := os.ReadDir(stateRoot)
			if err != nil {
				return err
			}
			var names []string
			for _, e := range entries {
				if e.IsDir() {
					names = append(names, e.Name())
				}
			}
			if len(names) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no instances)")
				return nil
			}
			sort.Strings(names)
			for _, n := range names {
				fmt.Fprintln(cmd.OutOrStdout(), n)
			}
			return nil
		},
	}
	c.Flags().StringVar(&target, "target", cwd, "Repo root.")
	return c
}

func newInstanceShowCmd() *cobra.Command {
	var (
		target  string
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	c := &cobra.Command{
		Use:   "show <name>",
		Short: "Show an instance's state files.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstanceShow(cmd, target, args[0], jsonOut)
		},
	}
	c.Flags().StringVar(&target, "target", cwd, "Repo root.")
	c.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	return c
}

func runInstanceShow(cmd *cobra.Command, target, name string, jsonOut bool) error {
	info, err := collectInstanceInspect(cmd, target, name, time.Now())
	if err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(info)
	}
	renderInstanceInspect(cmd.OutOrStdout(), info)
	return nil
}

func collectInstanceInspect(cmd *cobra.Command, target, name string, now time.Time) (*inspectJSON, error) {
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return nil, err
	}
	stateDir := filepath.Join(teamDir, "state", name)
	st, err := os.Stat(stateDir)
	stateExists := err == nil && st.IsDir()
	meta, err := daemonMetadataFor(teamDir, name)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return nil, exitErr(1)
	}
	if !stateExists && meta == nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: instance not found: %s\n", stateDir)
		return nil, exitErr(2)
	}

	info := &inspectJSON{
		Instance: name,
		State: inspectStateJSON{
			Path:   displayPathFromTeamDir(teamDir, stateDir),
			Exists: stateExists,
		},
		Runtime: inspectRuntimeJSONFromMeta(teamDir, meta),
	}
	if stateExists {
		status, statusErr := inspectStatusJSONFor(teamDir, stateDir, now)
		info.Status = status
		info.StatusError = statusErr
		files, err := inspectFiles(stateDir)
		if err != nil {
			return nil, err
		}
		info.Files = files
	}
	info.Topology = inspectTopologyJSONFor(teamDir, name)
	return info, nil
}

func renderInstanceInspect(w fmtWriter, info *inspectJSON) {
	fmt.Fprintf(w, "instance: %s\n", info.Instance)
	stateDir := info.State.Path
	stateExists := info.State.Exists
	if stateExists {
		fmt.Fprintf(w, "path:     %s/\n\n", stateDir)
	} else {
		fmt.Fprintln(w, "path:     (state dir missing)")
		fmt.Fprintln(w)
	}

	printRuntimeMetadata(w, info.Runtime)
	printInspectStatus(w, info)
	printInspectTopology(w, info.Topology)

	if !stateExists {
		return
	}
	if len(info.Files) == 0 {
		fmt.Fprintln(w, "(empty)")
		return
	}
	fmt.Fprintln(w, "files:")
	for _, f := range info.Files {
		if f.Type == "dir" {
			fmt.Fprintf(w, "  %s/  (dir)\n", f.Name)
		} else {
			fmt.Fprintf(w, "  %s  (%d bytes)\n", f.Name, f.Size)
		}
	}
}

func inspectFiles(stateDir string) ([]inspectFileJSON, error) {
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	files := make([]inspectFileJSON, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		f := inspectFileJSON{Name: e.Name(), Size: info.Size(), Type: "file"}
		if e.IsDir() {
			f.Type = "dir"
		}
		files = append(files, f)
	}
	return files, nil
}

func daemonMetadataFor(teamDir, name string) (*daemon.Metadata, error) {
	dc, err := newDaemonClient(teamDir)
	if err != nil {
		if errors.Is(err, errDaemonNotRunning) {
			meta, err := daemon.ReadMetadata(daemon.DaemonRoot(teamDir), name)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return nil, nil
				}
				return nil, err
			}
			return meta, nil
		}
		return nil, err
	}
	list, err := dc.Instances()
	if err != nil {
		return nil, err
	}
	for _, m := range list {
		if m.Instance == name {
			return m, nil
		}
	}
	return nil, nil
}

type inspectJSON struct {
	Instance    string               `json:"instance"`
	State       inspectStateJSON     `json:"state"`
	Runtime     *inspectRuntimeJSON  `json:"runtime,omitempty"`
	Status      *inspectStatusJSON   `json:"status,omitempty"`
	StatusError string               `json:"status_error,omitempty"`
	Topology    *inspectTopologyJSON `json:"topology,omitempty"`
	Files       []inspectFileJSON    `json:"files,omitempty"`
}

type inspectStateJSON struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

type inspectRuntimeJSON struct {
	Lifecycle     string `json:"lifecycle"`
	Agent         string `json:"agent,omitempty"`
	Runtime       string `json:"runtime,omitempty"`
	RuntimeBinary string `json:"runtime_binary,omitempty"`
	Job           string `json:"job,omitempty"`
	Ticket        string `json:"ticket,omitempty"`
	Branch        string `json:"branch,omitempty"`
	PR            string `json:"pr,omitempty"`
	PID           int    `json:"pid,omitempty"`
	Workspace     string `json:"workspace,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	StartedAt     string `json:"started_at,omitempty"`
	StoppedAt     string `json:"stopped_at,omitempty"`
	ExitedAt      string `json:"exited_at,omitempty"`
	ExitCode      *int   `json:"exit_code,omitempty"`
	LogPath       string `json:"log_path,omitempty"`
}

type inspectStatusJSON struct {
	Phase       string               `json:"phase"`
	Description string               `json:"description,omitempty"`
	Since       string               `json:"since,omitempty"`
	LastAction  string               `json:"last_action,omitempty"`
	Age         string               `json:"age,omitempty"`
	Stale       bool                 `json:"stale"`
	Work        *inspectWorkJSON     `json:"work,omitempty"`
	Blocking    *inspectBlockingJSON `json:"blocking,omitempty"`
}

type inspectWorkJSON struct {
	Job    string `json:"job,omitempty"`
	Ticket string `json:"ticket,omitempty"`
	PR     string `json:"pr,omitempty"`
	Branch string `json:"branch,omitempty"`
}

type inspectBlockingJSON struct {
	Reason string `json:"reason,omitempty"`
	AskTo  string `json:"ask_to,omitempty"`
}

type inspectTopologyJSON struct {
	Agent       string               `json:"agent"`
	Ephemeral   bool                 `json:"ephemeral"`
	Replicas    int                  `json:"replicas,omitempty"`
	Description string               `json:"description,omitempty"`
	Config      map[string]any       `json:"config,omitempty"`
	Triggers    []inspectTriggerJSON `json:"triggers,omitempty"`
}

type inspectTriggerJSON struct {
	Event string         `json:"event"`
	Match map[string]any `json:"match,omitempty"`
}

type inspectFileJSON struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size,omitempty"`
}

func inspectRuntimeJSONFromMeta(teamDir string, meta *daemon.Metadata) *inspectRuntimeJSON {
	if meta == nil {
		return nil
	}
	out := &inspectRuntimeJSON{
		Lifecycle:     metadataStatusKey(meta),
		Agent:         meta.Agent,
		Runtime:       meta.Runtime,
		RuntimeBinary: meta.RuntimeBinary,
		Job:           meta.Job,
		Ticket:        meta.Ticket,
		Branch:        meta.Branch,
		PR:            meta.PR,
		PID:           meta.PID,
		Workspace:     filepath.ToSlash(meta.Workspace),
		SessionID:     meta.SessionID,
		ExitCode:      meta.ExitCode,
	}
	if !meta.StartedAt.IsZero() {
		out.StartedAt = meta.StartedAt.Format(time.RFC3339)
	}
	if !meta.StoppedAt.IsZero() {
		out.StoppedAt = meta.StoppedAt.Format(time.RFC3339)
	}
	if !meta.ExitedAt.IsZero() {
		out.ExitedAt = meta.ExitedAt.Format(time.RFC3339)
	}
	if logPath := logPathForMetadata(teamDir, meta); logPath != "" {
		out.LogPath = displayPathFromTeamDir(teamDir, logPath)
	}
	return out
}

func inspectStatusJSONFor(teamDir, stateDir string, now time.Time) (*inspectStatusJSON, string) {
	path := filepath.Join(stateDir, "status.toml")
	st, err := os.Stat(path)
	if err != nil {
		return nil, ""
	}
	policy, err := loadHealthPolicy(teamDir)
	if err != nil {
		return nil, err.Error()
	}
	var sf statusFile
	if _, err := toml.DecodeFile(path, &sf); err != nil {
		return nil, err.Error()
	}
	out := &inspectStatusJSON{
		Phase:       sf.Status.Phase,
		Description: sf.Status.Description,
		Since:       sf.Status.Since,
		LastAction:  sf.Status.LastAction,
		Age:         formatAge(now.Sub(st.ModTime())),
	}
	if sf.Status.Phase != "idle" && sf.Status.Phase != "done" && policy.StatusStaleAfter > 0 && now.Sub(st.ModTime()) > policy.StatusStaleAfter {
		out.Stale = true
	}
	if sf.Work != nil {
		out.Work = &inspectWorkJSON{
			Job:    sf.Work.Job,
			Ticket: sf.Work.Ticket,
			PR:     sf.Work.PR,
			Branch: sf.Work.Branch,
		}
	}
	if sf.Blocking != nil {
		out.Blocking = &inspectBlockingJSON{
			Reason: sf.Blocking.Reason,
			AskTo:  sf.Blocking.AskTo,
		}
	}
	return out, ""
}

func inspectTopologyJSONFor(teamDir, name string) *inspectTopologyJSON {
	topo, err := topology.LoadFromTeamDir(teamDir)
	if err != nil || topo == nil {
		return nil
	}
	inst := topo.Find(name)
	if inst == nil {
		return nil
	}
	out := &inspectTopologyJSON{
		Agent:       inst.Agent,
		Ephemeral:   inst.Ephemeral,
		Replicas:    inst.Replicas,
		Description: inst.Description,
		Config:      inst.Config,
	}
	for _, t := range inst.Triggers {
		out.Triggers = append(out.Triggers, inspectTriggerJSON{
			Event: t.Event,
			Match: inspectMatchJSON(t.Match),
		})
	}
	return out
}

func inspectMatchJSON(match map[string]topology.MatchValue) map[string]any {
	if len(match) == 0 {
		return nil
	}
	out := make(map[string]any, len(match))
	for key, value := range match {
		if len(value.List) > 0 {
			out[key] = value.List
		} else {
			out[key] = value.Single
		}
	}
	return out
}

func summariseInspectMatch(match map[string]any) string {
	if len(match) == 0 {
		return ""
	}
	keys := make([]string, 0, len(match))
	for k := range match {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		switch value := match[key].(type) {
		case []string:
			parts = append(parts, fmt.Sprintf("%s∈%v", key, value))
		case []any:
			parts = append(parts, fmt.Sprintf("%s∈%v", key, value))
		default:
			parts = append(parts, fmt.Sprintf("%s=%q", key, value))
		}
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func printRuntimeMetadata(w fmtWriter, runtime *inspectRuntimeJSON) {
	if runtime == nil {
		return
	}
	fmt.Fprintln(w, "runtime:")
	fmt.Fprintf(w, "  lifecycle:   %s\n", runtime.Lifecycle)
	if runtime.Agent != "" {
		fmt.Fprintf(w, "  agent:       %s\n", runtime.Agent)
	}
	if runtime.Runtime != "" {
		fmt.Fprintf(w, "  runtime:     %s\n", runtime.Runtime)
	}
	if runtime.RuntimeBinary != "" {
		fmt.Fprintf(w, "  binary:      %s\n", runtime.RuntimeBinary)
	}
	if runtime.Job != "" {
		fmt.Fprintf(w, "  job:         %s\n", runtime.Job)
	}
	if runtime.Ticket != "" {
		fmt.Fprintf(w, "  ticket:      %s\n", runtime.Ticket)
	}
	if runtime.Branch != "" {
		fmt.Fprintf(w, "  branch:      %s\n", runtime.Branch)
	}
	if runtime.PR != "" {
		fmt.Fprintf(w, "  pr:          %s\n", runtime.PR)
	}
	if runtime.PID != 0 {
		fmt.Fprintf(w, "  pid:         %d\n", runtime.PID)
	}
	if runtime.Workspace != "" {
		fmt.Fprintf(w, "  workspace:   %s\n", runtime.Workspace)
	}
	if runtime.SessionID != "" {
		fmt.Fprintf(w, "  session_id:  %s\n", runtime.SessionID)
	}
	if runtime.StartedAt != "" {
		fmt.Fprintf(w, "  started_at:  %s\n", runtime.StartedAt)
	}
	if runtime.StoppedAt != "" {
		fmt.Fprintf(w, "  stopped_at:  %s\n", runtime.StoppedAt)
	}
	if runtime.ExitedAt != "" {
		fmt.Fprintf(w, "  exited_at:   %s\n", runtime.ExitedAt)
	}
	if runtime.ExitCode != nil {
		fmt.Fprintf(w, "  exit_code:   %d\n", *runtime.ExitCode)
	}
	if runtime.LogPath != "" {
		fmt.Fprintf(w, "  log:         %s\n", runtime.LogPath)
	}
	fmt.Fprintln(w)
}

func printInspectStatus(w fmtWriter, info *inspectJSON) {
	if info.StatusError != "" {
		fmt.Fprintf(w, "status: (parse error: %s)\n\n", info.StatusError)
		return
	}
	if info.Status == nil {
		return
	}
	status := info.Status
	fmt.Fprintln(w, "status:")
	fmt.Fprintf(w, "  phase:        %s\n", status.Phase)
	if status.Description != "" {
		fmt.Fprintf(w, "  description:  %s\n", status.Description)
	}
	if status.Since != "" {
		fmt.Fprintf(w, "  since:        %s\n", status.Since)
	}
	if status.LastAction != "" {
		fmt.Fprintf(w, "  last_action:  %s\n", status.LastAction)
	}
	if status.Age != "" {
		fmt.Fprintf(w, "  age:          %s\n", status.Age)
	}
	if status.Stale {
		fmt.Fprintln(w, "  stale:        yes (mtime > 10m on a non-idle phase)")
	}
	if status.Work != nil {
		fmt.Fprintln(w, "work:")
		if status.Work.Job != "" {
			fmt.Fprintf(w, "  job:     %s\n", status.Work.Job)
		}
		if status.Work.Ticket != "" {
			fmt.Fprintf(w, "  ticket:  %s\n", status.Work.Ticket)
		}
		if status.Work.PR != "" {
			fmt.Fprintf(w, "  pr:      %s\n", status.Work.PR)
		}
		if status.Work.Branch != "" {
			fmt.Fprintf(w, "  branch:  %s\n", status.Work.Branch)
		}
	}
	if status.Blocking != nil {
		fmt.Fprintln(w, "blocking:")
		fmt.Fprintf(w, "  reason:  %s\n", status.Blocking.Reason)
		fmt.Fprintf(w, "  ask_to:  %s\n", status.Blocking.AskTo)
	}
	fmt.Fprintln(w)
}

func printInspectTopology(w fmtWriter, top *inspectTopologyJSON) {
	if top == nil {
		return
	}
	fmt.Fprintln(w, "topology:")
	fmt.Fprintf(w, "  agent:     %s\n", top.Agent)
	fmt.Fprintf(w, "  ephemeral: %v\n", top.Ephemeral)
	if top.Ephemeral {
		fmt.Fprintf(w, "  replicas:  %d\n", top.Replicas)
	}
	if top.Description != "" {
		fmt.Fprintf(w, "  description: %s\n", top.Description)
	}
	if len(top.Config) > 0 {
		fmt.Fprintln(w, "  config overrides:")
		for k, v := range flattenForPrint(top.Config, "") {
			fmt.Fprintf(w, "    %s = %v\n", k, v)
		}
	}
	if len(top.Triggers) > 0 {
		fmt.Fprintln(w, "  triggers:")
		for _, t := range top.Triggers {
			fmt.Fprintf(w, "    - %s%s\n", t.Event, summariseInspectMatch(t.Match))
		}
	}
	fmt.Fprintln(w)
}

func displayPathFromTeamDir(teamDir, path string) string {
	rel, err := filepath.Rel(filepath.Dir(teamDir), path)
	if err == nil && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".." {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

func newInstanceRmCmd() *cobra.Command {
	var (
		target        string
		all           bool
		force         bool
		dryRun        bool
		finished      bool
		latest        bool
		last          int
		staleOnly     bool
		unhealthyOnly bool
		agents        []string
		statusFilters []string
		phaseFilters  []string
		jsonOut       bool
		summary       bool
		format        string
	)
	cwd, _ := os.Getwd()
	c := &cobra.Command{
		Use:   "rm [<name>...]",
		Short: "Remove an instance's state.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && (jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format cannot be combined with --json or --summary.")
				return exitErr(2)
			}
			formatTemplate, err := parseRmFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			return runInstanceRmWithOptions(cmd, target, args, instanceRmOptions{
				All:           all,
				Force:         force,
				DryRun:        dryRun,
				Finished:      finished,
				Latest:        latest,
				Limit:         last,
				Stale:         staleOnly,
				Unhealthy:     unhealthyOnly,
				AgentFilters:  agents,
				StatusFilters: statusFilters,
				PhaseFilters:  phaseFilters,
				JSON:          jsonOut,
				Summary:       summary,
				Format:        formatTemplate,
			})
		},
	}
	c.Flags().StringVar(&target, "target", cwd, "Repo root.")
	c.Flags().BoolVarP(&all, "all", "a", false, "Remove every daemon-known instance. Can combine with --agent, --status, --phase, --stale, or --unhealthy.")
	c.Flags().BoolVarP(&force, "force", "f", false, "Skip confirmation; if the daemon is running, stop a running instance before removal.")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Preview matching removals without deleting state or daemon metadata.")
	c.Flags().BoolVar(&finished, "finished", false, "Remove every daemon-known exited or crashed instance.")
	c.Flags().BoolVar(&latest, "latest", false, "Remove the most recently started daemon-known instance after other filters.")
	c.Flags().IntVarP(&last, "last", "n", 0, "Remove the N most recently started daemon-known instances after other filters (0 = all).")
	c.Flags().BoolVar(&staleOnly, "stale", false, "Remove only daemon-known instances whose non-idle work phase has stale status telemetry.")
	c.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Remove only daemon-known instances that are crashed or stale.")
	c.Flags().StringSliceVar(&agents, "agent", nil, "With --all, --finished, --latest, --last, --status, --phase, --stale, or --unhealthy, only remove daemon-known instances for this agent. Can repeat or comma-separate.")
	c.Flags().StringSliceVar(&statusFilters, "status", nil, "Remove daemon-known instances currently in this lifecycle status: stopped, exited, crashed, running, or unknown. Can repeat or comma-separate.")
	c.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Remove daemon-known instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	c.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON. Requires --force unless --dry-run is set.")
	c.Flags().BoolVar(&summary, "summary", false, "Show aggregate removal counts instead of per-instance rows.")
	c.Flags().StringVar(&format, "format", "", "Render each removal result with a Go template, e.g. '{{.Instance}} {{.Path}}'. Requires --force unless --dry-run is set.")
	return c
}

func runInstanceRm(cmd *cobra.Command, target string, names []string, force bool) error {
	return runInstanceRmWithOptions(cmd, target, names, instanceRmOptions{Force: force})
}

type instanceRmOptions struct {
	All           bool
	Force         bool
	DryRun        bool
	Finished      bool
	Latest        bool
	Limit         int
	Stale         bool
	Unhealthy     bool
	OlderThan     time.Duration
	OlderThanSet  bool
	AgentFilters  []string
	StatusFilters []string
	PhaseFilters  []string
	Quiet         bool
	JSON          bool
	Summary       bool
	Format        *template.Template
}

type instanceRmResult struct {
	Action        string `json:"action"`
	Instance      string `json:"instance"`
	Removed       bool   `json:"removed"`
	StateRemoved  bool   `json:"state_removed"`
	DaemonRemoved bool   `json:"daemon_removed"`
	Path          string `json:"path,omitempty"`
	Detail        string `json:"detail,omitempty"`
	DryRun        bool   `json:"dry_run,omitempty"`
}

func runInstanceRmWithOptions(cmd *cobra.Command, target string, names []string, opts instanceRmOptions) error {
	if opts.All && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --all cannot be combined with instance names.")
		return exitErr(2)
	}
	if opts.All && opts.Finished {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --all or --finished.")
		return exitErr(2)
	}
	if opts.Finished && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --finished cannot be combined with instance names.")
		return exitErr(2)
	}
	if opts.Latest && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --latest cannot be combined with instance names.")
		return exitErr(2)
	}
	if opts.Limit < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --last must be >= 0.")
		return exitErr(2)
	}
	if opts.Latest && opts.Limit > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --latest or --last.")
		return exitErr(2)
	}
	if opts.Limit > 0 && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --last cannot be combined with instance names.")
		return exitErr(2)
	}
	if opts.Stale && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --stale cannot be combined with instance names.")
		return exitErr(2)
	}
	if opts.Unhealthy && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --unhealthy cannot be combined with instance names.")
		return exitErr(2)
	}
	if len(opts.StatusFilters) > 0 && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --status cannot be combined with instance names.")
		return exitErr(2)
	}
	if len(opts.PhaseFilters) > 0 && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --phase cannot be combined with instance names.")
		return exitErr(2)
	}
	if opts.OlderThanSet && opts.OlderThan < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --older-than must be >= 0.")
		return exitErr(2)
	}
	if len(opts.AgentFilters) > 0 {
		if len(names) > 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --agent cannot be combined with instance names.")
			return exitErr(2)
		}
		if len(lifecycleAgentFilterSet(opts.AgentFilters)) == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --agent requires at least one non-empty agent.")
			return exitErr(2)
		}
		if !opts.All && !opts.Finished && !opts.Latest && opts.Limit == 0 && len(opts.StatusFilters) == 0 && len(opts.PhaseFilters) == 0 && !opts.Stale && !opts.Unhealthy {
			fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --agent requires --all, --finished, --latest, --last, --status, --phase, --stale, or --unhealthy.")
			return exitErr(2)
		}
	}
	statuses, err := lifecycleStatusFilterSet(opts.StatusFilters)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(2)
	}
	phases, err := lifecyclePhaseFilterSet(opts.PhaseFilters)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(2)
	}
	if !opts.All && !opts.Finished && !opts.Latest && opts.Limit == 0 && len(statuses) == 0 && len(phases) == 0 && !opts.Stale && !opts.Unhealthy && len(names) == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: instance is required unless --all, --finished, --latest, --last, --status, --phase, --stale, or --unhealthy is set.")
		return exitErr(2)
	}
	if opts.JSON && !opts.Force && !opts.DryRun {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --json requires --force or --dry-run so removal is non-interactive.")
		return exitErr(2)
	}
	if opts.Quiet && opts.Summary {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --quiet or --summary.")
		return exitErr(2)
	}
	if opts.Quiet && opts.JSON {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --quiet or --json.")
		return exitErr(2)
	}
	if opts.Format != nil && opts.JSON {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format cannot be combined with --json.")
		return exitErr(2)
	}
	if opts.Format != nil && opts.Quiet {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format cannot be combined with --quiet.")
		return exitErr(2)
	}
	if opts.Format != nil && opts.Summary {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format cannot be combined with --summary.")
		return exitErr(2)
	}
	if opts.Quiet && !opts.Force && !opts.DryRun {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --quiet requires --force or --dry-run so removal is non-interactive.")
		return exitErr(2)
	}
	if opts.Format != nil && !opts.Force && !opts.DryRun {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format requires --force or --dry-run so removal is non-interactive.")
		return exitErr(2)
	}
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	var phaseByInstance map[string]string
	var staleInstances map[string]bool
	if len(phases) > 0 || opts.Stale || opts.Unhealthy {
		now := time.Now()
		if len(phases) > 0 {
			phaseByInstance = waitPhaseByInstance(teamDir, now)
		}
		if opts.Stale || opts.Unhealthy {
			staleInstances = staleInstanceSet(teamDir, now)
		}
	}
	daemonByName := map[string]daemonInstanceInfo{}
	if dc, err := newDaemonClient(teamDir); err == nil {
		list, err := dc.Instances()
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
			return exitErr(1)
		}
		for _, m := range list {
			daemonByName[m.Instance] = daemonInstanceInfo{status: string(m.Status), agent: m.Agent, pid: m.PID, startedAt: m.StartedAt, finishedAt: daemonMetadataFinishedAt(m), client: dc}
		}
	} else if errors.Is(err, errDaemonNotRunning) {
		list, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
			return exitErr(1)
		}
		for _, m := range list {
			daemonByName[m.Instance] = daemonInstanceInfo{status: string(m.Status), agent: m.Agent, pid: m.PID, startedAt: m.StartedAt, finishedAt: daemonMetadataFinishedAt(m)}
		}
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(2)
	}

	if opts.All || opts.Finished || opts.Latest || opts.Limit > 0 || len(statuses) > 0 || len(phases) > 0 || opts.Stale || opts.Unhealthy {
		names = selectRmTargetsWithUnhealthy(daemonByName, opts.AgentFilters, statuses, phases, phaseByInstance, opts.Finished, opts.Stale, opts.Unhealthy, staleInstances)
		if opts.OlderThanSet {
			names = filterRmTargetsOlderThan(names, daemonByName, opts.OlderThan, time.Now())
		}
		if opts.Latest {
			names = latestRmTargetsLimit(names, daemonByName, 1)
		} else if opts.Limit > 0 {
			names = latestRmTargetsLimit(names, daemonByName, opts.Limit)
		}
		if len(names) == 0 {
			if opts.JSON {
				if opts.Summary {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(lifecycleActionSummaryResult{Summary: summarizeInstanceRmResults(nil, opts.DryRun)})
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode([]instanceRmResult{})
			}
			if opts.Quiet || opts.Format != nil {
				return nil
			}
			if opts.Summary {
				renderLifecycleActionSummary(cmd.OutOrStdout(), summarizeInstanceRmResults(nil, opts.DryRun))
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), "(nothing to remove)")
			return nil
		}
	}

	results := make([]instanceRmResult, 0, len(names))
	for _, name := range names {
		stateDir := filepath.Join(teamDir, "state", name)
		st, statErr := os.Stat(stateDir)
		stateExists := statErr == nil && st.IsDir()
		daemonInfo, daemonKnown := daemonByName[name]
		if !stateExists && !daemonKnown {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: instance not found: %s\n", stateDir)
			return exitErr(2)
		}
		if daemonKnown && daemonInfo.status == "running" && !opts.Force && !opts.DryRun {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: instance %q is running — stop it first or use --force.\n", name)
			return exitErr(2)
		}

		if opts.DryRun {
			rel := ""
			if stateExists {
				var err error
				rel, err = filepath.Rel(filepath.Dir(teamDir), stateDir)
				if err != nil {
					rel = stateDir
				}
				rel = filepath.ToSlash(rel)
			}
			detail := rmDryRunDetail(stateExists, daemonKnown)
			results = append(results, instanceRmResult{
				Action:        "remove",
				Instance:      name,
				StateRemoved:  stateExists,
				DaemonRemoved: daemonKnown,
				Path:          rel,
				Detail:        detail,
				DryRun:        true,
			})
			if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
				label := rel
				if label == "" {
					label = name
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  would remove %s\n", label)
			}
			continue
		}

		if !opts.Force {
			prompt := stateDir
			if !stateExists {
				prompt = name
			}
			ok, err := confirm(cmd, fmt.Sprintf("Remove %s?", prompt))
			if err != nil {
				return err
			}
			if !ok {
				if !opts.Quiet {
					fmt.Fprintln(cmd.OutOrStdout(), "(aborted)")
				}
				continue
			}
		}

		if daemonKnown {
			if err := removeDaemonMetadataForRm(teamDir, daemonInfo, name, opts.Force); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(1)
			}
		}
		if stateExists {
			if err := os.RemoveAll(stateDir); err != nil {
				return err
			}
			rel, err := filepath.Rel(filepath.Dir(teamDir), stateDir)
			if err != nil {
				rel = stateDir
			}
			path := filepath.ToSlash(rel)
			results = append(results, instanceRmResult{
				Action:        "remove",
				Instance:      name,
				Removed:       true,
				StateRemoved:  true,
				DaemonRemoved: daemonKnown,
				Path:          path,
			})
			if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
				fmt.Fprintf(cmd.OutOrStdout(), "  removed %s\n", path)
			}
			continue
		}
		results = append(results, instanceRmResult{
			Action:        "remove",
			Instance:      name,
			Removed:       true,
			DaemonRemoved: daemonKnown,
		})
		if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
			fmt.Fprintf(cmd.OutOrStdout(), "  removed %s\n", name)
		}
	}
	if opts.JSON {
		if opts.Summary {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(lifecycleActionSummaryResult{Summary: summarizeInstanceRmResults(results, opts.DryRun)})
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(results)
	}
	if opts.Format != nil {
		return renderRmFormat(cmd.OutOrStdout(), results, opts.Format)
	}
	if opts.Summary {
		renderLifecycleActionSummary(cmd.OutOrStdout(), summarizeInstanceRmResults(results, opts.DryRun))
	}
	return nil
}

func rmDryRunDetail(stateExists, daemonKnown bool) string {
	switch {
	case stateExists && daemonKnown:
		return "would remove state and daemon metadata"
	case stateExists:
		return "would remove state"
	case daemonKnown:
		return "would remove daemon metadata"
	default:
		return "would remove"
	}
}

func parseRmFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("rm-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderRmFormat(w io.Writer, rows []instanceRmResult, tmpl *template.Template) error {
	for _, row := range rows {
		if err := tmpl.Execute(w, row); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}

func summarizeInstanceRmResults(results []instanceRmResult, dryRun bool) lifecycleActionSummary {
	summary := newLifecycleActionSummary(dryRun)
	for _, result := range results {
		summary.Total++
		action := strings.TrimSpace(result.Action)
		if action == "" {
			action = "remove"
		}
		summary.Actions[action]++
		if result.DryRun {
			summary.DryRun = true
		}
		if result.Removed {
			summary.Removed++
		}
		if result.StateRemoved {
			summary.StateRemoved++
		}
		if result.DaemonRemoved {
			summary.DaemonRemoved++
		}
	}
	return summary
}

func selectFinishedRmTargets(daemonByName map[string]daemonInstanceInfo, agentFilters []string) []string {
	return selectRmTargets(daemonByName, agentFilters, nil, nil, nil, true, false, nil)
}

func selectRmTargets(daemonByName map[string]daemonInstanceInfo, agentFilters []string, statuses, phases map[string]bool, phaseByInstance map[string]string, finishedOnly, staleOnly bool, staleInstances map[string]bool) []string {
	return selectRmTargetsWithUnhealthy(daemonByName, agentFilters, statuses, phases, phaseByInstance, finishedOnly, staleOnly, false, staleInstances)
}

func selectRmTargetsWithUnhealthy(daemonByName map[string]daemonInstanceInfo, agentFilters []string, statuses, phases map[string]bool, phaseByInstance map[string]string, finishedOnly, staleOnly, unhealthyOnly bool, staleInstances map[string]bool) []string {
	agents := map[string]bool{}
	if len(agentFilters) > 0 {
		agents = lifecycleAgentFilterSet(agentFilters)
		if len(agents) == 0 {
			return nil
		}
	}
	targets := make([]string, 0, len(daemonByName))
	for name, info := range daemonByName {
		if len(agents) > 0 && !agents[info.agent] {
			continue
		}
		if finishedOnly {
			switch info.status {
			case string(daemon.StatusExited), string(daemon.StatusCrashed):
			default:
				continue
			}
		}
		if len(statuses) > 0 && !statuses[daemonInfoStatusKey(info)] {
			continue
		}
		if len(phases) > 0 && !phases[psPhaseKey(instanceRow{Phase: phaseByInstance[name]})] {
			continue
		}
		if staleOnly && !staleInstances[name] {
			continue
		}
		if unhealthyOnly && info.status != string(daemon.StatusCrashed) && !staleInstances[name] {
			continue
		}
		targets = append(targets, name)
	}
	sort.Strings(targets)
	return targets
}

func daemonInfoStatusKey(info daemonInstanceInfo) string {
	if info.status == "" {
		return "unknown"
	}
	return info.status
}

type daemonInstanceInfo struct {
	status     string
	agent      string
	pid        int
	startedAt  time.Time
	finishedAt time.Time
	client     *daemonClient
}

func daemonMetadataFinishedAt(m *daemon.Metadata) time.Time {
	if m == nil {
		return time.Time{}
	}
	if !m.ExitedAt.IsZero() {
		return m.ExitedAt
	}
	if !m.StoppedAt.IsZero() {
		return m.StoppedAt
	}
	return time.Time{}
}

func filterRmTargetsOlderThan(names []string, daemonByName map[string]daemonInstanceInfo, olderThan time.Duration, now time.Time) []string {
	if len(names) == 0 {
		return nil
	}
	targets := make([]string, 0, len(names))
	for _, name := range names {
		info, ok := daemonByName[name]
		if !ok || info.finishedAt.IsZero() {
			continue
		}
		if !info.finishedAt.After(now.Add(-olderThan)) {
			targets = append(targets, name)
		}
	}
	return targets
}

func latestRmTargetsLimit(names []string, daemonByName map[string]daemonInstanceInfo, limit int) []string {
	if limit <= 0 || len(names) == 0 {
		return names
	}
	targets := append([]string(nil), names...)
	sort.SliceStable(targets, func(i, j int) bool {
		a, b := daemonByName[targets[i]], daemonByName[targets[j]]
		if !a.startedAt.Equal(b.startedAt) {
			return psTimeAfter(a.startedAt, b.startedAt)
		}
		return targets[i] < targets[j]
	})
	if limit < len(targets) {
		targets = targets[:limit]
	}
	return targets
}

func removeDaemonMetadataForRm(teamDir string, info daemonInstanceInfo, name string, force bool) error {
	if info.client != nil {
		return info.client.RemoveInstance(name, force)
	}
	if info.status == string(daemon.StatusRunning) && info.pid > 0 && daemon.PidLiveCheck(info.pid) {
		return fmt.Errorf("instance %q appears to still be running (pid=%d); start the daemon and stop it first", name, info.pid)
	}
	return daemon.RemoveInstance(daemon.DaemonRoot(teamDir), name)
}

// newInstanceUpCmd implements `agent-team instance up [<name>...]`. With no
// args, it brings up every non-ephemeral declared instance from
// `instances.toml`. Explicit names may be declared instances or daemon-known
// ad-hoc instances. Idempotent — already-running instances are reported and
// skipped. Requires the daemon to be running.
func newInstanceUpCmd() *cobra.Command {
	var (
		target        string
		prompt        string
		all           bool
		latest        bool
		last          int
		agents        []string
		statusFilters []string
		phaseFilters  []string
		staleOnly     bool
		unhealthyOnly bool
		wait          bool
		timeout       time.Duration
		dryRun        bool
		summary       bool
		attach        bool
		tail          string
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	c := &cobra.Command{
		Use:   "up [<name>...]",
		Short: "Start or resume instances (idempotent). Requires the daemon.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !attach && cmd.Flags().Changed("tail") {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --tail requires --attach.")
				return exitErr(2)
			}
			tailLines, err := parseLogTail(tail)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			if summary && attach {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --summary cannot be combined with --attach.")
				return exitErr(2)
			}
			if format != "" && (jsonOut || attach || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format cannot be combined with --json, --attach, or --summary.")
				return exitErr(2)
			}
			formatTemplate, err := parseLifecycleActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			return runInstanceUpWithOptions(cmd, target, prompt, args, instanceUpOptions{
				All:           all,
				Latest:        latest,
				Limit:         last,
				AgentFilters:  agents,
				StatusFilters: statusFilters,
				PhaseFilters:  phaseFilters,
				Stale:         staleOnly,
				Unhealthy:     unhealthyOnly,
				Wait:          wait,
				Timeout:       timeout,
				DryRun:        dryRun,
				Summary:       summary,
				Attach:        attach,
				AttachTail:    tailLines,
				AttachTailSet: cmd.Flags().Changed("tail"),
				JSON:          jsonOut,
				Format:        formatTemplate,
			})
		},
	}
	c.Flags().StringVar(&target, "target", cwd, "Repo root.")
	c.Flags().StringVar(&prompt, "prompt", "", "Override the default kickoff prompt.")
	c.Flags().BoolVarP(&all, "all", "a", false, "Start or resume every declared persistent and daemon-known instance.")
	c.Flags().BoolVar(&latest, "latest", false, "Start or resume the most recently started instance after other filters.")
	c.Flags().IntVarP(&last, "last", "n", 0, "Start or resume the N most recently started instances after other filters (0 = all).")
	c.Flags().StringSliceVar(&agents, "agent", nil, "Start or resume every declared persistent and daemon-known instance for this agent. Can repeat or comma-separate.")
	c.Flags().StringSliceVar(&statusFilters, "status", nil, "Only start or resume instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	c.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only start or resume instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	c.Flags().BoolVar(&staleOnly, "stale", false, "Only start or resume instances whose status.toml is stale.")
	c.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only start or resume instances that are crashed or stale.")
	c.Flags().BoolVar(&wait, "wait", false, "Wait for selected instances to become healthy after starting. With no scoped selection, waits for the fleet.")
	c.Flags().DurationVar(&timeout, "timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Preview planned start/resume actions without changing daemon state.")
	c.Flags().BoolVar(&summary, "summary", false, "Show aggregate action counts instead of per-instance rows.")
	c.Flags().BoolVar(&attach, "attach", false, "Follow the selected instance log after starting or resuming. Requires exactly one selected instance.")
	c.Flags().StringVar(&tail, "tail", "50", "With --attach, show only the last N lines before following (0 or all = all).")
	c.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	c.Flags().StringVar(&format, "format", "", "Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return c
}

func runInstanceUp(cmd *cobra.Command, target, prompt string, names []string) error {
	return runInstanceUpWithOptions(cmd, target, prompt, names, instanceUpOptions{})
}

type instanceUpOptions struct {
	All           bool
	Latest        bool
	Limit         int
	AgentFilters  []string
	StatusFilters []string
	PhaseFilters  []string
	Stale         bool
	Unhealthy     bool
	Wait          bool
	Timeout       time.Duration
	DryRun        bool
	Summary       bool
	Attach        bool
	AttachTail    int
	AttachTailSet bool
	Quiet         bool
	JSON          bool
	Format        *template.Template
	Health        healthOptions
}

type lifecycleActionResult struct {
	Action   string `json:"action"`
	Instance string `json:"instance"`
	Agent    string `json:"agent,omitempty"`
	Status   string `json:"status"`
	PID      int    `json:"pid,omitempty"`
	Detail   string `json:"detail,omitempty"`
	DryRun   bool   `json:"dry_run,omitempty"`
	Error    string `json:"error,omitempty"`
}

type lifecycleHealthResult struct {
	Actions []lifecycleActionResult `json:"actions"`
	Health  *healthResult           `json:"health,omitempty"`
}

type lifecycleActionSummary struct {
	Total         int            `json:"total"`
	Actions       map[string]int `json:"actions"`
	Statuses      map[string]int `json:"statuses"`
	Errors        int            `json:"errors"`
	DryRun        bool           `json:"dry_run,omitempty"`
	Removed       int            `json:"removed,omitempty"`
	StateRemoved  int            `json:"state_removed,omitempty"`
	DaemonRemoved int            `json:"daemon_removed,omitempty"`
}

type lifecycleActionSummaryResult struct {
	Summary lifecycleActionSummary `json:"summary"`
	Health  *healthResult          `json:"health,omitempty"`
}

func runInstanceUpWithOptions(cmd *cobra.Command, target, prompt string, names []string, opts instanceUpOptions) error {
	if opts.All && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --all cannot be combined with instance names.")
		return exitErr(2)
	}
	if opts.Latest && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --latest cannot be combined with instance names.")
		return exitErr(2)
	}
	if opts.Limit < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --last must be >= 0.")
		return exitErr(2)
	}
	if opts.Latest && opts.Limit > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --latest or --last.")
		return exitErr(2)
	}
	if opts.Limit > 0 && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --last cannot be combined with instance names.")
		return exitErr(2)
	}
	if len(opts.AgentFilters) > 0 && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --agent cannot be combined with instance names.")
		return exitErr(2)
	}
	if len(opts.StatusFilters) > 0 && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --status cannot be combined with instance names.")
		return exitErr(2)
	}
	if len(opts.PhaseFilters) > 0 && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --phase cannot be combined with instance names.")
		return exitErr(2)
	}
	if opts.Stale && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --stale cannot be combined with instance names.")
		return exitErr(2)
	}
	if opts.Unhealthy && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --unhealthy cannot be combined with instance names.")
		return exitErr(2)
	}
	if opts.Timeout < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --timeout must be >= 0.")
		return exitErr(2)
	}
	if opts.DryRun && opts.Wait {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --dry-run cannot be combined with --wait.")
		return exitErr(2)
	}
	if opts.Attach && opts.JSON {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --attach cannot be combined with --json.")
		return exitErr(2)
	}
	if opts.Quiet && opts.JSON {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --quiet or --json.")
		return exitErr(2)
	}
	if opts.Quiet && opts.Summary {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --quiet or --summary.")
		return exitErr(2)
	}
	if opts.Format != nil && opts.JSON {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format cannot be combined with --json.")
		return exitErr(2)
	}
	if opts.Format != nil && opts.Quiet {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format cannot be combined with --quiet.")
		return exitErr(2)
	}
	if opts.Format != nil && opts.Summary {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format cannot be combined with --summary.")
		return exitErr(2)
	}
	if opts.Quiet && opts.Attach {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --quiet cannot be combined with --attach.")
		return exitErr(2)
	}
	if opts.Summary && opts.Attach {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --summary cannot be combined with --attach.")
		return exitErr(2)
	}
	if opts.Format != nil && opts.Attach {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format cannot be combined with --attach.")
		return exitErr(2)
	}
	if opts.Attach && opts.DryRun {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --attach cannot be combined with --dry-run.")
		return exitErr(2)
	}
	if opts.Attach && opts.Wait {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --attach or --wait.")
		return exitErr(2)
	}
	if !opts.Attach && opts.AttachTailSet {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --tail requires --attach.")
		return exitErr(2)
	}
	statuses, err := lifecycleStatusFilterSet(opts.StatusFilters)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(2)
	}
	phases, err := lifecyclePhaseFilterSet(opts.PhaseFilters)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(2)
	}
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	var phaseByInstance map[string]string
	var staleInstances map[string]bool
	if len(phases) > 0 || opts.Stale || opts.Unhealthy {
		now := time.Now()
		if len(phases) > 0 {
			phaseByInstance = waitPhaseByInstance(teamDir, now)
		}
		if opts.Stale || opts.Unhealthy {
			staleInstances = staleInstanceSet(teamDir, now)
		}
	}
	topo, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(1)
	}
	if topo == nil && len(names) == 0 && !opts.All && !opts.Latest && opts.Limit == 0 && len(opts.AgentFilters) == 0 && len(statuses) == 0 && len(phases) == 0 && !opts.Stale && !opts.Unhealthy {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: no instances.toml — nothing to bring up.")
		return exitErr(2)
	}
	dc, err := newDaemonClient(teamDir)
	if err != nil && !(opts.DryRun && errors.Is(err, errDaemonNotRunning)) {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: daemon is not running — start it first with `agent-team daemon start`.")
		return exitErr(2)
	}
	var metas []*daemon.Metadata
	if dc != nil {
		metas, err = dc.Instances()
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
			return exitErr(1)
		}
	} else if opts.DryRun {
		metas, err = daemon.ListMetadata(daemon.DaemonRoot(teamDir))
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
			return exitErr(1)
		}
	}
	daemonRunning := dc != nil
	var targets []lifecycleTarget
	if len(opts.AgentFilters) > 0 {
		targets, err = selectAgentLifecycleTargets(topo, metas, opts.AgentFilters)
	} else if opts.All || opts.Latest || opts.Limit > 0 || len(statuses) > 0 || len(phases) > 0 || opts.Stale || opts.Unhealthy {
		targets, err = selectAllLifecycleTargets(topo, metas)
	} else {
		targets, err = selectLifecycleTargets(topo, metas, names)
	}
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(2)
	}
	targets = filterLifecycleTargetsByStatus(targets, statuses)
	targets = filterLifecycleTargetsByPhase(targets, phases, phaseByInstance)
	targets = filterLifecycleTargetsByStale(targets, opts.Stale, staleInstances)
	targets = filterLifecycleTargetsByUnhealthy(targets, opts.Unhealthy, staleInstances)
	if opts.Latest {
		targets = latestLifecycleTargetsLimit(targets, 1)
	} else if opts.Limit > 0 {
		targets = latestLifecycleTargetsLimit(targets, opts.Limit)
	}
	if opts.Attach && len(targets) != 1 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --attach requires exactly one selected instance.")
		return exitErr(2)
	}
	waitHealth := opts.Health
	if opts.Wait && !healthOptionsConfigured(waitHealth) && instanceUpSelectionScoped(names, opts) {
		waitHealth = lifecycleWaitHealthOptionsForTargets(targets)
	}
	if len(targets) == 0 {
		if opts.JSON {
			if opts.Summary {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(lifecycleActionSummaryResult{
					Summary: summarizeLifecycleActions(nil, opts.DryRun),
				})
			}
			if opts.Wait {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(lifecycleHealthResult{Actions: []lifecycleActionResult{}})
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode([]lifecycleActionResult{})
		}
		if opts.Quiet || opts.Format != nil {
			return nil
		}
		if opts.Summary {
			renderLifecycleActionSummary(cmd.OutOrStdout(), summarizeLifecycleActions(nil, opts.DryRun))
			return nil
		}
		fmt.Fprintln(cmd.OutOrStdout(), "(no instances)")
		return nil
	}
	out := cmd.OutOrStdout()
	results := make([]lifecycleActionResult, 0, len(targets))
	for _, lt := range targets {
		if opts.DryRun {
			result := dryRunStartResultWithDaemonState(lt, daemonRunning)
			results = append(results, result)
			if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
				renderLifecycleDryRun(out, result)
			}
			continue
		}
		if lt.running() {
			result := lifecycleActionResult{
				Action:   "skip",
				Instance: lt.name,
				Agent:    lt.agent,
				Status:   string(daemon.StatusRunning),
				Detail:   "already running",
			}
			if lt.meta != nil {
				result.PID = lt.meta.PID
			}
			results = append(results, result)
			if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
				fmt.Fprintf(out, "  skip   %-20s already running\n", lt.name)
			}
			continue
		}
		if lt.meta != nil {
			if !lifecycleMetadataSupportsManagedResume(lt.meta) {
				result := lifecycleTargetUnsupportedResumeResult(lt)
				results = append(results, result)
				if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
					fmt.Fprintf(out, "  %-7s %-20s %s\n", result.Action, lt.name, result.Detail)
				}
				continue
			}
			if err := dc.StartInstance(lt.name); err != nil {
				results = append(results, lifecycleActionResult{Action: "error", Instance: lt.name, Agent: lt.agent, Status: "error", Error: err.Error()})
				if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
					fmt.Fprintf(out, "  error  %-20s %v\n", lt.name, err)
				}
				continue
			}
			results = append(results, lifecycleActionResult{Action: "resume", Instance: lt.name, Agent: lt.agent})
			if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
				fmt.Fprintf(out, "  resume %-20s %s\n", lt.name, lt.agent)
			}
			continue
		}
		kickoff := prompt
		if kickoff == "" {
			kickoff = fmt.Sprintf("Topology bring-up: you are %q, an instance of %q.", lt.name, lt.agent)
		}
		runErr := runMaybeSuppressStdout(cmd, opts.JSON || opts.Quiet || opts.Format != nil || opts.Summary, func() error {
			return upOne(cmd, target, lt.declared, kickoff)
		})
		if runErr != nil {
			results = append(results, lifecycleActionResult{Action: "error", Instance: lt.name, Agent: lt.agent, Status: "error", Error: runErr.Error()})
			if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
				fmt.Fprintf(out, "  error  %-20s %v\n", lt.name, runErr)
			}
			continue
		}
		results = append(results, lifecycleActionResult{Action: "start", Instance: lt.name, Agent: lt.agent})
		if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
			fmt.Fprintf(out, "  start  %-20s %s\n", lt.name, lt.agent)
		}
	}
	if opts.DryRun {
		if opts.JSON {
			if opts.Summary {
				return json.NewEncoder(out).Encode(lifecycleActionSummaryResult{
					Summary: summarizeLifecycleActions(results, true),
				})
			}
			return json.NewEncoder(out).Encode(results)
		}
		if opts.Format != nil {
			return renderLifecycleActionFormat(out, results, opts.Format)
		}
		if opts.Summary {
			renderLifecycleActionSummary(out, summarizeLifecycleActions(results, true))
		}
		return nil
	}
	enriched := enrichLifecycleResults(dc, results)
	var health *healthResult
	healthWaitTimedOut := false
	if opts.Wait {
		ctx := cmd.Context()
		cancel := func() {}
		if opts.Timeout > 0 {
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		}
		defer cancel()
		health, healthWaitTimedOut, err = runHealthWaitWithOutcome(ctx, teamDir, 500*time.Millisecond, time.Now, waitHealth)
		if err != nil {
			return err
		}
	}
	if opts.Attach {
		if lifecycleActionResultsHaveErrors(enriched) {
			return exitErr(1)
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		fmt.Fprintf(out, "\nattaching to %s (Ctrl-C to detach)\n", targets[0].name)
		return followLifecycleLog(ctx, out, dc, targets[0].name, opts.AttachTail)
	}
	if opts.JSON {
		if opts.Summary {
			body := lifecycleActionSummaryResult{Summary: summarizeLifecycleActions(enriched, false)}
			if opts.Wait {
				body.Health = health
			}
			if err := json.NewEncoder(out).Encode(body); err != nil {
				return err
			}
			if lifecycleActionResultsHaveErrors(enriched) {
				return exitErr(1)
			}
			if opts.Wait && health != nil && !health.Healthy {
				reportLifecycleHealthWaitTimeout(cmd, opts.Quiet, healthWaitTimedOut, health)
				return exitErr(1)
			}
			return nil
		}
		if opts.Wait {
			if err := json.NewEncoder(out).Encode(lifecycleHealthResult{Actions: enriched, Health: health}); err != nil {
				return err
			}
			if lifecycleActionResultsHaveErrors(enriched) {
				return exitErr(1)
			}
			if health != nil && !health.Healthy {
				reportLifecycleHealthWaitTimeout(cmd, opts.Quiet, healthWaitTimedOut, health)
				return exitErr(1)
			}
			return nil
		}
		if err := json.NewEncoder(out).Encode(enriched); err != nil {
			return err
		}
		if lifecycleActionResultsHaveErrors(enriched) {
			return exitErr(1)
		}
		if opts.Wait && health != nil && !health.Healthy {
			reportLifecycleHealthWaitTimeout(cmd, opts.Quiet, healthWaitTimedOut, health)
			return exitErr(1)
		}
		return nil
	}
	if opts.Format != nil {
		if err := renderLifecycleActionFormat(out, enriched, opts.Format); err != nil {
			return err
		}
		if lifecycleActionResultsHaveErrors(enriched) {
			return exitErr(1)
		}
		return nil
	}
	if opts.Summary {
		renderLifecycleActionSummary(out, summarizeLifecycleActions(enriched, false))
		if opts.Wait && !opts.Quiet {
			fmt.Fprintln(out)
			renderHealth(out, health)
		}
		if lifecycleActionResultsHaveErrors(enriched) {
			return exitErr(1)
		}
		if opts.Wait && health != nil && !health.Healthy {
			reportLifecycleHealthWaitTimeout(cmd, opts.Quiet, healthWaitTimedOut, health)
			return exitErr(1)
		}
		return nil
	}
	if opts.Wait {
		if !opts.Quiet {
			fmt.Fprintln(out)
			renderHealth(out, health)
		}
		if lifecycleActionResultsHaveErrors(enriched) {
			return exitErr(1)
		}
		if health != nil && !health.Healthy {
			reportLifecycleHealthWaitTimeout(cmd, opts.Quiet, healthWaitTimedOut, health)
			return exitErr(1)
		}
	}
	if lifecycleActionResultsHaveErrors(enriched) {
		return exitErr(1)
	}
	return nil
}

func parseLifecycleActionFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("lifecycle-action-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderLifecycleActionFormat(w io.Writer, rows []lifecycleActionResult, tmpl *template.Template) error {
	for _, row := range rows {
		if err := tmpl.Execute(w, row); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}

func summarizeLifecycleActions(results []lifecycleActionResult, dryRun bool) lifecycleActionSummary {
	summary := newLifecycleActionSummary(dryRun)
	for _, result := range results {
		summary.Total++
		action := strings.TrimSpace(result.Action)
		if action == "" {
			action = "unknown"
		}
		status := strings.TrimSpace(result.Status)
		if status == "" {
			status = "unknown"
		}
		summary.Actions[action]++
		summary.Statuses[status]++
		if result.DryRun {
			summary.DryRun = true
		}
		if result.Action == "error" || result.Status == "error" || result.Error != "" {
			summary.Errors++
		}
	}
	return summary
}

func summarizeInstanceDownActions(results []instanceDownResult, dryRun bool) lifecycleActionSummary {
	summary := newLifecycleActionSummary(dryRun)
	for _, result := range results {
		summary.Total++
		action := strings.TrimSpace(result.Action)
		if action == "" {
			action = "unknown"
		}
		status := strings.TrimSpace(result.Status)
		if status == "" {
			status = "unknown"
		}
		summary.Actions[action]++
		summary.Statuses[status]++
		if result.DryRun {
			summary.DryRun = true
		}
		if result.Action == "error" || result.Status == "error" || result.Error != "" {
			summary.Errors++
		}
		if result.Removed {
			summary.Removed++
		}
		if result.StateRemoved {
			summary.StateRemoved++
		}
		if result.DaemonRemoved {
			summary.DaemonRemoved++
		}
	}
	return summary
}

func newLifecycleActionSummary(dryRun bool) lifecycleActionSummary {
	return lifecycleActionSummary{
		Actions:  map[string]int{},
		Statuses: map[string]int{},
		DryRun:   dryRun,
	}
}

func renderLifecycleActionSummary(w io.Writer, summary lifecycleActionSummary) {
	fmt.Fprintf(w, "summary: total=%d", summary.Total)
	if summary.DryRun {
		fmt.Fprint(w, " dry_run=true")
	}
	for _, key := range sortedCountKeys(summary.Actions) {
		fmt.Fprintf(w, " %s=%d", key, summary.Actions[key])
	}
	if summary.Errors > 0 && summary.Actions["error"] != summary.Errors {
		fmt.Fprintf(w, " errors=%d", summary.Errors)
	}
	fmt.Fprintln(w)
	if len(summary.Statuses) > 0 {
		fmt.Fprint(w, "statuses:")
		for _, key := range sortedCountKeys(summary.Statuses) {
			fmt.Fprintf(w, " %s=%d", key, summary.Statuses[key])
		}
		fmt.Fprintln(w)
	}
	if summary.Removed > 0 || summary.StateRemoved > 0 || summary.DaemonRemoved > 0 {
		fmt.Fprintf(w, "removed: total=%d state=%d daemon=%d\n", summary.Removed, summary.StateRemoved, summary.DaemonRemoved)
	}
}

func sortedCountKeys(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for key, count := range counts {
		if count == 0 {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func lifecycleActionResultsHaveErrors(results []lifecycleActionResult) bool {
	for _, result := range results {
		if result.Action == "error" || result.Status == "error" || result.Error != "" {
			return true
		}
	}
	return false
}

func reportLifecycleHealthWaitTimeout(cmd *cobra.Command, quiet bool, timedOut bool, health *healthResult) {
	if quiet || !healthWaitTimedOutUnhealthy(timedOut, health) {
		return
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: wait timed out before selected instances became healthy.")
}

func enrichLifecycleResults(lister instanceLister, results []lifecycleActionResult) []lifecycleActionResult {
	metas, err := lister.Instances()
	if err != nil {
		for i := range results {
			if results[i].Status == "" {
				results[i].Status = "unknown"
			}
		}
		return results
	}
	byName := map[string]*daemon.Metadata{}
	for _, meta := range metas {
		byName[meta.Instance] = meta
	}
	for i := range results {
		if results[i].Status == "error" {
			continue
		}
		meta, ok := byName[results[i].Instance]
		if !ok {
			if results[i].Status == "" {
				results[i].Status = "unknown"
			}
			continue
		}
		if results[i].Agent == "" {
			results[i].Agent = meta.Agent
		}
		results[i].Status = string(meta.Status)
		results[i].PID = meta.PID
	}
	return results
}

func healthOptionsConfigured(opts healthOptions) bool {
	filters := opts.filters
	return opts.strictTopology ||
		filters.stale ||
		filters.unhealthy ||
		filters.Limit > 0 ||
		len(filters.statuses) > 0 ||
		len(filters.agents) > 0 ||
		len(filters.phases) > 0 ||
		len(filters.instances) > 0
}

func instanceUpSelectionScoped(names []string, opts instanceUpOptions) bool {
	return lifecycleSelectionScoped(names, opts.AgentFilters, opts.StatusFilters, opts.PhaseFilters, opts.Latest, opts.Limit, opts.Stale, opts.Unhealthy)
}

func lifecycleSelectionScoped(names, agentFilters, statusFilters, phaseFilters []string, latest bool, limit int, stale bool, unhealthy bool) bool {
	return len(names) > 0 ||
		len(agentFilters) > 0 ||
		len(statusFilters) > 0 ||
		len(phaseFilters) > 0 ||
		latest ||
		limit > 0 ||
		stale ||
		unhealthy
}

func lifecycleWaitHealthOptionsForTargets(targets []lifecycleTarget) healthOptions {
	instances := make(map[string]bool, len(targets))
	for _, target := range targets {
		if target.name != "" {
			instances[target.name] = true
		}
	}
	if len(instances) == 0 {
		return healthOptions{}
	}
	return healthOptions{filters: psOptions{instances: instances}}
}

func dryRunStartResult(target lifecycleTarget) lifecycleActionResult {
	return dryRunStartResultWithDaemonState(target, true)
}

func dryRunStartResultWithDaemonState(target lifecycleTarget, daemonRunning bool) lifecycleActionResult {
	result := lifecycleActionResult{
		Action:   "start",
		Instance: target.name,
		Agent:    target.agent,
		Status:   lifecycleTargetStatusKey(target),
		Detail:   "would start",
		DryRun:   true,
	}
	if target.meta != nil {
		result.PID = target.meta.PID
		if lifecycleMetadataSupportsManagedResume(target.meta) {
			result.Action = "resume"
			result.Detail = "would resume"
		} else {
			result.Action = lifecycleActionUnsupported
			result.Detail = lifecycleUnsupportedResumeDetail(target.meta)
		}
	}
	if target.running() {
		if !daemonRunning && (target.meta.PID == 0 || !daemon.PidLiveCheck(target.meta.PID)) {
			result.Status = string(daemon.StatusRunning)
			if lifecycleMetadataSupportsManagedResume(target.meta) {
				result.Action = "resume"
				result.Detail = "would resume; recorded running pid is not live"
			} else {
				result.Action = lifecycleActionUnsupported
				result.Detail = lifecycleStaleUnsupportedResumeDetail(target.meta)
			}
			return result
		}
		result.Action = "skip"
		result.Status = string(daemon.StatusRunning)
		result.Detail = "already running"
	}
	return result
}

func dryRunRestartResult(target lifecycleTarget) lifecycleActionResult {
	result := lifecycleActionResult{
		Action:   "start",
		Instance: target.name,
		Agent:    target.agent,
		Status:   lifecycleTargetStatusKey(target),
		Detail:   "would start",
		DryRun:   true,
	}
	if target.meta != nil {
		result.PID = target.meta.PID
		result.Action = "restart"
		result.Detail = "would restart"
	}
	return result
}

func renderLifecycleDryRun(w fmtWriter, result lifecycleActionResult) {
	if result.Action == "skip" {
		fmt.Fprintf(w, "  skip   %-20s %s\n", result.Instance, result.Detail)
		return
	}
	if result.Action == lifecycleActionUnsupported {
		fmt.Fprintf(w, "  %-7s %-20s %s\n", result.Action, result.Instance, result.Detail)
		return
	}
	detail := strings.TrimSpace(result.Agent)
	if result.Status != "" {
		if detail != "" {
			detail += " "
		}
		detail += "status=" + result.Status
	}
	if detail == "" {
		detail = result.Detail
	}
	fmt.Fprintf(w, "  would %-7s %-20s %s\n", result.Action, result.Instance, detail)
}

func runMaybeSuppressStdout(cmd *cobra.Command, suppress bool, fn func() error) error {
	if !suppress {
		return fn()
	}
	oldOut := cmd.OutOrStdout()
	cmd.SetOut(io.Discard)
	defer cmd.SetOut(oldOut)
	return fn()
}

var lifecycleAttachLogRetryInterval = 100 * time.Millisecond

func followLifecycleLog(ctx context.Context, w io.Writer, dc *daemonClient, instance string, tail int) error {
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := dc.LogsStream(ctx, w, instance, true, tail)
		if err == nil {
			return nil
		}
		var notFound *logNotFoundError
		if !errors.As(err, &notFound) {
			return err
		}
		if time.Now().After(deadline) {
			return err
		}
		timer := time.NewTimer(lifecycleAttachLogRetryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

type lifecycleTarget struct {
	name     string
	agent    string
	declared *topology.Instance
	meta     *daemon.Metadata
}

func (t lifecycleTarget) running() bool {
	return t.meta != nil && t.meta.Status == daemon.StatusRunning
}

func lifecycleMetadataByName(metas []*daemon.Metadata) map[string]*daemon.Metadata {
	out := map[string]*daemon.Metadata{}
	for _, meta := range metas {
		out[meta.Instance] = meta
	}
	return out
}

// selectLifecycleTargets resolves lifecycle command args. With no args, every
// non-ephemeral declared instance is selected. With explicit names, a target
// may be either declared in instances.toml or already known to the daemon.
func selectLifecycleTargets(topo *topology.Topology, metas []*daemon.Metadata, names []string) ([]lifecycleTarget, error) {
	metaByName := map[string]*daemon.Metadata{}
	for _, m := range metas {
		metaByName[m.Instance] = m
	}
	if len(names) == 0 {
		if topo == nil {
			return nil, errors.New("no instances.toml — no declared persistent instances")
		}
		var out []lifecycleTarget
		for _, inst := range topo.SortedInstances() {
			if !inst.Ephemeral {
				out = append(out, lifecycleTarget{
					name:     inst.Name,
					agent:    inst.Agent,
					declared: inst,
					meta:     metaByName[inst.Name],
				})
			}
		}
		if len(out) == 0 {
			return nil, errors.New("no non-ephemeral instances declared in instances.toml")
		}
		return out, nil
	}
	out := make([]lifecycleTarget, 0, len(names))
	for _, n := range names {
		md := metaByName[n]
		var inst *topology.Instance
		if topo != nil {
			inst = topo.Find(n)
		}
		if md != nil {
			agent := md.Agent
			if agent == "" && inst != nil {
				agent = inst.Agent
			}
			out = append(out, lifecycleTarget{name: n, agent: agent, declared: inst, meta: md})
			continue
		}
		if inst == nil {
			return nil, fmt.Errorf("instance %q is not declared in instances.toml and is not known to the daemon", n)
		}
		if inst.Ephemeral {
			return nil, fmt.Errorf("instance %q is ephemeral and has no daemon metadata to resume", n)
		}
		out = append(out, lifecycleTarget{name: inst.Name, agent: inst.Agent, declared: inst})
	}
	return out, nil
}

func selectAllLifecycleTargets(topo *topology.Topology, metas []*daemon.Metadata) ([]lifecycleTarget, error) {
	out := allLifecycleTargets(topo, metas)
	if len(out) == 0 {
		return nil, errors.New("no declared persistent or daemon-known instances")
	}
	return out, nil
}

func selectAgentLifecycleTargets(topo *topology.Topology, metas []*daemon.Metadata, filters []string) ([]lifecycleTarget, error) {
	agents := lifecycleAgentFilterSet(filters)
	if len(agents) == 0 {
		return nil, errors.New("--agent requires at least one non-empty agent")
	}
	all := allLifecycleTargets(topo, metas)
	out := make([]lifecycleTarget, 0, len(all))
	for _, target := range all {
		if agents[target.agent] {
			out = append(out, target)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no declared persistent or daemon-known instances match --agent")
	}
	return out, nil
}

func allLifecycleTargets(topo *topology.Topology, metas []*daemon.Metadata) []lifecycleTarget {
	metaByName := map[string]*daemon.Metadata{}
	for _, m := range metas {
		metaByName[m.Instance] = m
	}
	seen := map[string]bool{}
	var out []lifecycleTarget
	if topo != nil {
		for _, inst := range topo.SortedInstances() {
			if inst.Ephemeral {
				continue
			}
			out = append(out, lifecycleTarget{
				name:     inst.Name,
				agent:    inst.Agent,
				declared: inst,
				meta:     metaByName[inst.Name],
			})
			seen[inst.Name] = true
		}
	}
	extras := make([]*daemon.Metadata, 0, len(metas))
	for _, meta := range metas {
		if !seen[meta.Instance] {
			extras = append(extras, meta)
		}
	}
	sort.Slice(extras, func(i, j int) bool { return extras[i].Instance < extras[j].Instance })
	for _, meta := range extras {
		out = append(out, lifecycleTarget{
			name:  meta.Instance,
			agent: meta.Agent,
			meta:  meta,
		})
	}
	return out
}

func lifecycleAgentFilterSet(filters []string) map[string]bool {
	out := map[string]bool{}
	for _, raw := range splitFilterValues(filters) {
		agent := strings.TrimSpace(raw)
		if agent != "" {
			out[agent] = true
		}
	}
	return out
}

func lifecycleStatusFilterSet(filters []string) (map[string]bool, error) {
	if len(filters) == 0 {
		return nil, nil
	}
	out := map[string]bool{}
	for _, raw := range filters {
		for _, part := range strings.Split(raw, ",") {
			status := strings.ToLower(strings.TrimSpace(part))
			if status == "" {
				continue
			}
			switch status {
			case string(daemon.StatusRunning), string(daemon.StatusStopped), string(daemon.StatusExited), string(daemon.StatusCrashed), "unknown":
				out[status] = true
			default:
				return nil, fmt.Errorf("unknown --status %q (want running, stopped, exited, crashed, or unknown)", part)
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("--status requires at least one non-empty status")
	}
	return out, nil
}

func filterLifecycleTargetsByStatus(targets []lifecycleTarget, statuses map[string]bool) []lifecycleTarget {
	if len(statuses) == 0 {
		return targets
	}
	out := make([]lifecycleTarget, 0, len(targets))
	for _, target := range targets {
		if statuses[lifecycleTargetStatusKey(target)] {
			out = append(out, target)
		}
	}
	return out
}

func filterLifecycleTargetsByPhase(targets []lifecycleTarget, phases map[string]bool, phaseByInstance map[string]string) []lifecycleTarget {
	if len(phases) == 0 {
		return targets
	}
	out := make([]lifecycleTarget, 0, len(targets))
	for _, target := range targets {
		if phases[lifecycleTargetPhaseKey(target, phaseByInstance)] {
			out = append(out, target)
		}
	}
	return out
}

func staleInstanceSet(teamDir string, now time.Time) map[string]bool {
	statusStaleAfter := defaultStatusStaleAfter
	if policy, err := loadHealthPolicy(teamDir); err == nil {
		statusStaleAfter = policy.StatusStaleAfter
	}
	rows := loadInstanceRowsWithStatusStaleAfter(teamDir, loadAgentNames(teamDir), now, statusStaleAfter)
	out := make(map[string]bool, len(rows))
	for _, row := range rows {
		if row.Stale {
			out[row.Instance] = true
		}
	}
	return out
}

func filterLifecycleTargetsByStale(targets []lifecycleTarget, staleOnly bool, staleInstances map[string]bool) []lifecycleTarget {
	if !staleOnly {
		return targets
	}
	out := make([]lifecycleTarget, 0, len(targets))
	for _, target := range targets {
		if staleInstances[target.name] {
			out = append(out, target)
		}
	}
	return out
}

func filterLifecycleTargetsByUnhealthy(targets []lifecycleTarget, unhealthyOnly bool, staleInstances map[string]bool) []lifecycleTarget {
	if !unhealthyOnly {
		return targets
	}
	out := make([]lifecycleTarget, 0, len(targets))
	for _, target := range targets {
		if lifecycleTargetStatusKey(target) == string(daemon.StatusCrashed) || staleInstances[target.name] {
			out = append(out, target)
		}
	}
	return out
}

func lifecycleTargetStatusKey(target lifecycleTarget) string {
	if target.meta == nil || target.meta.Status == "" {
		return "unknown"
	}
	return string(target.meta.Status)
}

func lifecycleTargetPhaseKey(target lifecycleTarget, phaseByInstance map[string]string) string {
	return psPhaseKey(instanceRow{Phase: phaseByInstance[target.name]})
}

func latestLifecycleTargets(targets []lifecycleTarget) []lifecycleTarget {
	return latestLifecycleTargetsLimit(targets, 1)
}

func latestLifecycleTargetsLimit(targets []lifecycleTarget, limit int) []lifecycleTarget {
	if limit <= 0 || len(targets) <= 1 {
		return targets
	}
	out := append([]lifecycleTarget(nil), targets...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		aStarted, bStarted := lifecycleTargetStartedAt(a), lifecycleTargetStartedAt(b)
		if !aStarted.Equal(bStarted) {
			return psTimeAfter(aStarted, bStarted)
		}
		return a.name < b.name
	})
	if limit < len(out) {
		out = out[:limit]
	}
	return out
}

func lifecycleTargetStartedAt(target lifecycleTarget) time.Time {
	if target.meta == nil {
		return time.Time{}
	}
	return target.meta.StartedAt
}

// runningInstanceSet returns the set of instance names whose daemon-tracked
// status is StatusRunning.
func runningInstanceSet(dc *daemonClient) (map[string]bool, error) {
	list, err := dc.Instances()
	if err != nil {
		return nil, err
	}
	return runningInstanceSetFromMetas(list), nil
}

func runningInstanceSetFromMetas(list []*daemon.Metadata) map[string]bool {
	out := map[string]bool{}
	for _, m := range list {
		if string(m.Status) == "running" {
			out[m.Instance] = true
		}
	}
	return out
}

// upOne dispatches one declared instance. It reuses runAgent so the spawn
// path mirrors `agent-team run` exactly — same skill resolution, kickoff,
// declared-overrides folding, etc. We construct a minimal runConfig with
// --prompt set so runAgent routes through /v1/dispatch instead of fronting
// an interactive claude.
func upOne(cmd *cobra.Command, target string, inst *topology.Instance, kickoff string) error {
	if target == "" {
		cwd, _ := os.Getwd()
		target = cwd
	}
	cfg := runConfig{
		target: target,
		name:   inst.Name,
		prompt: kickoff,
	}
	return runAgent(cmd, cfg, inst.Agent, nil)
}

// newInstanceDownCmd implements `agent-team instance down [<name>...]`. With
// no args, stops every running declared persistent instance. Ephemerals are
// left alone (they exit on their own work-completion).
func newInstanceDownCmd() *cobra.Command {
	var (
		target        string
		latest        bool
		last          int
		agents        []string
		statusFilters []string
		phaseFilters  []string
		staleOnly     bool
		unhealthyOnly bool
		force         bool
		wait          bool
		timeout       time.Duration
		waitTimeout   time.Duration
		dryRun        bool
		remove        bool
		summary       bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	c := &cobra.Command{
		Use:   "down [<name>...]",
		Short: "Stop declared persistent instances. With no args, stops all running.",
		RunE: func(cmd *cobra.Command, args []string) error {
			formatTemplate, err := parseLifecycleActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			return runInstanceDownWithOptions(cmd, target, args, instanceDownOptions{
				Latest:         latest,
				Limit:          last,
				AgentFilters:   agents,
				StatusFilters:  statusFilters,
				PhaseFilters:   phaseFilters,
				Stale:          staleOnly,
				Unhealthy:      unhealthyOnly,
				Force:          force,
				Wait:           wait,
				Timeout:        timeout,
				WaitTimeout:    waitTimeout,
				WaitTimeoutSet: cmd.Flags().Changed("wait-timeout"),
				DryRun:         dryRun,
				Remove:         remove,
				Summary:        summary,
				JSON:           jsonOut,
				Format:         formatTemplate,
			})
		},
	}
	c.Flags().StringVar(&target, "target", cwd, "Repo root.")
	c.Flags().BoolVar(&latest, "latest", false, "Stop the most recently started running instance after other filters.")
	c.Flags().IntVarP(&last, "last", "n", 0, "Stop the N most recently started running instances after other filters (0 = all).")
	c.Flags().StringSliceVar(&agents, "agent", nil, "Stop every running instance for this agent. Can repeat or comma-separate.")
	c.Flags().StringSliceVar(&statusFilters, "status", nil, "Stop daemon-known instances currently in this lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	c.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Stop daemon-known instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	c.Flags().BoolVar(&staleOnly, "stale", false, "Only stop instances whose status.toml is stale.")
	c.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only stop instances that are crashed or stale.")
	c.Flags().BoolVarP(&force, "force", "f", false, "Escalate to SIGKILL if an instance does not stop within --timeout.")
	c.Flags().BoolVar(&wait, "wait", false, "Wait for stopped instances to reach a terminal state.")
	c.Flags().DurationVar(&timeout, "timeout", 0, "Grace before --force kills. With --wait and no --wait-timeout, also used as the wait deadline (0 = no wait deadline; force defaults to 10s).")
	c.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait for terminal state with --wait. Defaults to --timeout when unset; set 0 explicitly for no wait timeout.")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Preview planned stop actions without changing daemon state.")
	c.Flags().BoolVar(&remove, "rm", false, "Remove selected instance state and daemon metadata after stopping.")
	c.Flags().BoolVar(&summary, "summary", false, "Show aggregate action counts instead of per-instance rows.")
	c.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	c.Flags().StringVar(&format, "format", "", "Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return c
}

func runInstanceDown(cmd *cobra.Command, target string, names []string) error {
	return runInstanceDownWithOptions(cmd, target, names, instanceDownOptions{})
}

type instanceDownOptions struct {
	All            bool
	Latest         bool
	Limit          int
	AgentFilters   []string
	StatusFilters  []string
	PhaseFilters   []string
	Stale          bool
	Unhealthy      bool
	Force          bool
	Wait           bool
	Timeout        time.Duration
	WaitTimeout    time.Duration
	WaitTimeoutSet bool
	DryRun         bool
	Remove         bool
	Action         string
	Summary        bool
	Quiet          bool
	JSON           bool
	Format         *template.Template
}

type instanceDownResult struct {
	Action        string `json:"action"`
	Instance      string `json:"instance"`
	Status        string `json:"status"`
	Detail        string `json:"detail,omitempty"`
	WaitStatus    string `json:"wait_status,omitempty"`
	Removed       bool   `json:"removed,omitempty"`
	StateRemoved  bool   `json:"state_removed,omitempty"`
	DaemonRemoved bool   `json:"daemon_removed,omitempty"`
	Path          string `json:"path,omitempty"`
	DryRun        bool   `json:"dry_run,omitempty"`
	Error         string `json:"error,omitempty"`
}

func runInstanceDownWithOptions(cmd *cobra.Command, target string, names []string, opts instanceDownOptions) error {
	if opts.All && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --all cannot be combined with instance names.")
		return exitErr(2)
	}
	if opts.Latest && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --latest cannot be combined with instance names.")
		return exitErr(2)
	}
	if opts.Limit < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --last must be >= 0.")
		return exitErr(2)
	}
	if opts.Latest && opts.Limit > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --latest or --last.")
		return exitErr(2)
	}
	if opts.Limit > 0 && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --last cannot be combined with instance names.")
		return exitErr(2)
	}
	if len(opts.AgentFilters) > 0 && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --agent cannot be combined with instance names.")
		return exitErr(2)
	}
	if len(opts.StatusFilters) > 0 && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --status cannot be combined with instance names.")
		return exitErr(2)
	}
	if len(opts.PhaseFilters) > 0 && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --phase cannot be combined with instance names.")
		return exitErr(2)
	}
	if opts.Stale && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --stale cannot be combined with instance names.")
		return exitErr(2)
	}
	if opts.Unhealthy && len(names) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --unhealthy cannot be combined with instance names.")
		return exitErr(2)
	}
	if opts.Timeout < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --timeout must be >= 0.")
		return exitErr(2)
	}
	if opts.WaitTimeout < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --wait-timeout must be >= 0.")
		return exitErr(2)
	}
	if opts.DryRun && opts.Wait {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --dry-run cannot be combined with --wait.")
		return exitErr(2)
	}
	if opts.Quiet && opts.JSON {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --quiet or --json.")
		return exitErr(2)
	}
	if opts.Quiet && opts.Summary {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: choose one of --quiet or --summary.")
		return exitErr(2)
	}
	if opts.Format != nil && opts.JSON {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format cannot be combined with --json.")
		return exitErr(2)
	}
	if opts.Format != nil && opts.Quiet {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format cannot be combined with --quiet.")
		return exitErr(2)
	}
	if opts.Format != nil && opts.Summary {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: --format cannot be combined with --summary.")
		return exitErr(2)
	}
	statuses, err := lifecycleStatusFilterSet(opts.StatusFilters)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(2)
	}
	phases, err := lifecyclePhaseFilterSet(opts.PhaseFilters)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(2)
	}
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	var phaseByInstance map[string]string
	var staleInstances map[string]bool
	if len(phases) > 0 || opts.Stale || opts.Unhealthy {
		now := time.Now()
		if len(phases) > 0 {
			phaseByInstance = waitPhaseByInstance(teamDir, now)
		}
		if opts.Stale || opts.Unhealthy {
			staleInstances = staleInstanceSet(teamDir, now)
		}
	}
	dc, err := newDaemonClient(teamDir)
	if err != nil && !(opts.DryRun && errors.Is(err, errDaemonNotRunning)) {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: daemon is not running.")
		return exitErr(2)
	}
	var metas []*daemon.Metadata
	if dc != nil {
		metas, err = dc.Instances()
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
			return exitErr(1)
		}
	} else if opts.DryRun {
		metas, err = daemon.ListMetadata(daemon.DaemonRoot(teamDir))
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
			return exitErr(1)
		}
	}
	running := runningInstanceSetFromMetas(metas)
	if dc == nil && opts.DryRun {
		running = liveRunningInstanceSetFromMetas(metas)
	}
	targets, err := selectDownTargetsWithOptions(teamDir, running, metas, names, opts.All || opts.Latest || opts.Limit > 0, opts.AgentFilters, statuses, phases, phaseByInstance, downTargetOptions{
		Stale:          opts.Stale,
		Unhealthy:      opts.Unhealthy,
		StaleInstances: staleInstances,
	})
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(1)
	}
	if opts.Latest {
		targets = latestNamesByStartedLimit(targets, metas, 1)
	} else if opts.Limit > 0 {
		targets = latestNamesByStartedLimit(targets, metas, opts.Limit)
	}
	out := cmd.OutOrStdout()
	if len(targets) == 0 {
		if opts.JSON {
			if opts.Summary {
				return json.NewEncoder(out).Encode(lifecycleActionSummaryResult{
					Summary: summarizeInstanceDownActions(nil, opts.DryRun),
				})
			}
			return json.NewEncoder(out).Encode([]instanceDownResult{})
		}
		if opts.Quiet || opts.Format != nil {
			return nil
		}
		if opts.Summary {
			renderLifecycleActionSummary(out, summarizeInstanceDownActions(nil, opts.DryRun))
			return nil
		}
		fmt.Fprintf(out, "(nothing to %s)\n", downAction(opts))
		return nil
	}
	stopped := make([]string, 0, len(targets))
	results := make([]instanceDownResult, 0, len(targets))
	resultByInstance := map[string]int{}
	metaByName := lifecycleMetadataByName(metas)
	for _, name := range targets {
		if opts.DryRun {
			result := dryRunDownResult(name, metaByName[name], running[name], downAction(opts))
			if opts.Remove {
				if result.Action == "skip" {
					result.Detail = "not running; would remove"
				} else {
					result.Detail += " and remove"
				}
			}
			resultByInstance[name] = len(results)
			results = append(results, result)
			if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
				renderDownDryRun(out, result)
			}
			continue
		}
		if !running[name] {
			result := instanceDownResult{
				Action:   "skip",
				Instance: name,
				Status:   "skipped",
				Detail:   "not running",
			}
			resultByInstance[name] = len(results)
			results = append(results, result)
			if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
				fmt.Fprintf(out, "  skip   %-20s not running\n", name)
			}
			continue
		}
		if err := dc.StopInstanceWithOptions(name, opts.Force, opts.Timeout); err != nil {
			result := instanceDownResult{
				Action:   "error",
				Instance: name,
				Status:   "error",
				Error:    err.Error(),
			}
			resultByInstance[name] = len(results)
			results = append(results, result)
			if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
				fmt.Fprintf(out, "  error  %-20s %v\n", name, err)
			}
			continue
		}
		result := instanceDownResult{
			Action:   downAction(opts),
			Instance: name,
			Status:   "stopped",
		}
		resultByInstance[name] = len(results)
		results = append(results, result)
		if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
			fmt.Fprintf(out, "  %-6s %-20s\n", downAction(opts), name)
		}
		stopped = append(stopped, name)
	}
	if opts.DryRun {
		if opts.JSON {
			if opts.Summary {
				return json.NewEncoder(out).Encode(lifecycleActionSummaryResult{
					Summary: summarizeInstanceDownActions(results, true),
				})
			}
			return json.NewEncoder(out).Encode(results)
		}
		if opts.Format != nil {
			return renderInstanceDownFormat(out, results, opts.Format)
		}
		if opts.Summary {
			renderLifecycleActionSummary(out, summarizeInstanceDownActions(results, true))
		}
		return nil
	}
	if opts.Wait && len(stopped) > 0 {
		ctx := cmd.Context()
		cancel := func() {}
		waitTimeout := downWaitTimeout(opts)
		if waitTimeout > 0 {
			ctx, cancel = context.WithTimeout(ctx, waitTimeout)
		}
		defer cancel()
		waitResults, err := waitForInstances(ctx, dc, stopped, 500*time.Millisecond)
		if err != nil {
			var timeoutErr *waitTimeoutError
			if errors.As(err, &timeoutErr) {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: wait timed out: %s still running\n", strings.Join(timeoutErr.PendingNames(), ", "))
				return exitErr(1)
			}
			return err
		}
		for _, result := range waitResults {
			if idx, ok := resultByInstance[result.Instance]; ok {
				results[idx].WaitStatus = result.Status
			}
			if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
				fmt.Fprintf(out, "  wait   %-20s %s\n", result.Instance, result.Status)
			}
		}
	}
	if opts.Remove {
		removeDownResults(cmd, teamDir, dc, results, metaByName, opts.JSON || opts.Quiet || opts.Summary)
	}
	if opts.JSON {
		if opts.Summary {
			if err := json.NewEncoder(out).Encode(lifecycleActionSummaryResult{
				Summary: summarizeInstanceDownActions(results, false),
			}); err != nil {
				return err
			}
			if instanceDownResultsHaveErrors(results) {
				return exitErr(1)
			}
			return nil
		}
		if err := json.NewEncoder(out).Encode(results); err != nil {
			return err
		}
		if instanceDownResultsHaveErrors(results) {
			return exitErr(1)
		}
		return nil
	}
	if opts.Format != nil {
		if err := renderInstanceDownFormat(out, results, opts.Format); err != nil {
			return err
		}
		if instanceDownResultsHaveErrors(results) {
			return exitErr(1)
		}
		return nil
	}
	if opts.Summary {
		renderLifecycleActionSummary(out, summarizeInstanceDownActions(results, false))
		if instanceDownResultsHaveErrors(results) {
			return exitErr(1)
		}
		return nil
	}
	if instanceDownResultsHaveErrors(results) {
		return exitErr(1)
	}
	return nil
}

func downWaitTimeout(opts instanceDownOptions) time.Duration {
	if opts.WaitTimeoutSet {
		return opts.WaitTimeout
	}
	return opts.Timeout
}

func renderInstanceDownFormat(w io.Writer, rows []instanceDownResult, tmpl *template.Template) error {
	for _, row := range rows {
		if err := tmpl.Execute(w, row); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}

func removeDownResults(cmd *cobra.Command, teamDir string, dc *daemonClient, results []instanceDownResult, metaByName map[string]*daemon.Metadata, jsonOut bool) {
	for i := range results {
		if results[i].Action == "error" || results[i].Error != "" {
			continue
		}
		_, daemonKnown := metaByName[results[i].Instance]
		removed, err := removeDownTarget(teamDir, dc, results[i].Instance, daemonKnown)
		if err != nil {
			results[i].Action = "error"
			results[i].Status = "error"
			results[i].Error = err.Error()
			if !jsonOut {
				fmt.Fprintf(cmd.OutOrStdout(), "  error  %-20s %v\n", results[i].Instance, err)
			}
			continue
		}
		results[i].Removed = removed.Removed
		results[i].StateRemoved = removed.StateRemoved
		results[i].DaemonRemoved = removed.DaemonRemoved
		results[i].Path = removed.Path
		if !jsonOut {
			label := removed.Path
			if label == "" {
				label = results[i].Instance
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  removed %s\n", label)
		}
	}
}

type downRemoval struct {
	Removed       bool
	StateRemoved  bool
	DaemonRemoved bool
	Path          string
}

func removeDownTarget(teamDir string, dc *daemonClient, name string, daemonKnown bool) (downRemoval, error) {
	result := downRemoval{}
	stateDir := filepath.Join(teamDir, "state", name)
	st, statErr := os.Stat(stateDir)
	stateExists := statErr == nil && st.IsDir()
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return result, statErr
	}
	if !stateExists && !daemonKnown {
		return result, fmt.Errorf("instance not found: %s", stateDir)
	}
	if daemonKnown {
		if err := dc.RemoveInstance(name, true); err != nil {
			return result, err
		}
		result.DaemonRemoved = true
	}
	if stateExists {
		if err := os.RemoveAll(stateDir); err != nil {
			return result, err
		}
		result.StateRemoved = true
		rel, err := filepath.Rel(filepath.Dir(teamDir), stateDir)
		if err != nil {
			rel = stateDir
		}
		result.Path = filepath.ToSlash(rel)
	}
	result.Removed = true
	return result, nil
}

func instanceDownResultsHaveErrors(results []instanceDownResult) bool {
	for _, result := range results {
		if result.Action == "error" || result.Status == "error" || result.Error != "" {
			return true
		}
	}
	return false
}

func dryRunDownResult(name string, meta *daemon.Metadata, running bool, action string) instanceDownResult {
	result := instanceDownResult{
		Action:   action,
		Instance: name,
		Status:   "unknown",
		Detail:   "would " + action,
		DryRun:   true,
	}
	if meta != nil && meta.Status != "" {
		result.Status = string(meta.Status)
	}
	if !running {
		result.Action = "skip"
		result.Status = "skipped"
		result.Detail = "not running"
	}
	return result
}

func liveRunningInstanceSetFromMetas(metas []*daemon.Metadata) map[string]bool {
	out := map[string]bool{}
	for _, meta := range metas {
		if meta.Status != daemon.StatusRunning || meta.PID == 0 {
			continue
		}
		if daemon.PidLiveCheck(meta.PID) {
			out[meta.Instance] = true
		}
	}
	return out
}

func renderDownDryRun(w fmtWriter, result instanceDownResult) {
	if result.Action == "skip" {
		fmt.Fprintf(w, "  skip   %-20s %s\n", result.Instance, result.Detail)
		return
	}
	fmt.Fprintf(w, "  would %-7s %-20s status=%s\n", result.Action, result.Instance, result.Status)
}

func downAction(opts instanceDownOptions) string {
	if opts.Action != "" {
		return opts.Action
	}
	return "stop"
}

type downTargetOptions struct {
	Stale          bool
	Unhealthy      bool
	StaleInstances map[string]bool
}

func selectDownTargets(teamDir string, running map[string]bool, metas []*daemon.Metadata, names []string, all bool, agentFilters []string, statuses, phases map[string]bool, phaseByInstance map[string]string, staleFilters ...map[string]bool) ([]string, error) {
	opts := downTargetOptions{}
	if len(staleFilters) > 0 {
		opts.Stale = true
		opts.StaleInstances = staleFilters[0]
	}
	return selectDownTargetsWithOptions(teamDir, running, metas, names, all, agentFilters, statuses, phases, phaseByInstance, opts)
}

func selectDownTargetsWithOptions(teamDir string, running map[string]bool, metas []*daemon.Metadata, names []string, all bool, agentFilters []string, statuses, phases map[string]bool, phaseByInstance map[string]string, opts downTargetOptions) ([]string, error) {
	if len(agentFilters) > 0 || len(statuses) > 0 || len(phases) > 0 || opts.Stale || opts.Unhealthy {
		if len(names) > 0 {
			return nil, errors.New("--agent, --status, --phase, --stale, and --unhealthy cannot be combined with instance names")
		}
		agents, err := downAgentFilterSet(agentFilters)
		if err != nil {
			return nil, err
		}
		targets := make([]string, 0, len(metas))
		for _, meta := range metas {
			if len(statuses) == 0 && !opts.Unhealthy && meta.Status != daemon.StatusRunning {
				continue
			}
			if len(agents) > 0 && !agents[meta.Agent] {
				continue
			}
			if len(statuses) > 0 && !statuses[metadataStatusKey(meta)] {
				continue
			}
			if len(phases) > 0 && !phases[psPhaseKey(instanceRow{Phase: phaseByInstance[meta.Instance]})] {
				continue
			}
			if opts.Stale && !opts.StaleInstances[meta.Instance] {
				continue
			}
			if opts.Unhealthy && metadataStatusKey(meta) != string(daemon.StatusCrashed) && !opts.StaleInstances[meta.Instance] {
				continue
			}
			targets = append(targets, meta.Instance)
		}
		sort.Strings(targets)
		return targets, nil
	}
	if all {
		targets := make([]string, 0, len(running))
		for name := range running {
			targets = append(targets, name)
		}
		sort.Strings(targets)
		return targets, nil
	}
	if len(names) == 0 {
		topo, err := topology.LoadFromTeamDir(teamDir)
		if err != nil {
			return nil, err
		}
		declared := map[string]bool{}
		if topo != nil {
			for _, inst := range topo.SortedInstances() {
				if !inst.Ephemeral {
					declared[inst.Name] = true
				}
			}
		}
		var targets []string
		for name := range running {
			if declared[name] {
				targets = append(targets, name)
			}
		}
		sort.Strings(targets)
		return targets, nil
	}
	return names, nil
}

func latestNamesByStarted(names []string, metas []*daemon.Metadata) []string {
	return latestNamesByStartedLimit(names, metas, 1)
}

func latestNamesByStartedLimit(names []string, metas []*daemon.Metadata, limit int) []string {
	if limit <= 0 || len(names) <= 1 {
		return names
	}
	metaByName := lifecycleMetadataByName(metas)
	out := append([]string(nil), names...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		aStarted, bStarted := time.Time{}, time.Time{}
		if metaByName[a] != nil {
			aStarted = metaByName[a].StartedAt
		}
		if metaByName[b] != nil {
			bStarted = metaByName[b].StartedAt
		}
		if !aStarted.Equal(bStarted) {
			return psTimeAfter(aStarted, bStarted)
		}
		return a < b
	})
	if limit < len(out) {
		out = out[:limit]
	}
	return out
}

func downAgentFilterSet(agentFilters []string) (map[string]bool, error) {
	agents := lifecycleAgentFilterSet(agentFilters)
	if len(agentFilters) > 0 && len(agents) == 0 {
		return nil, errors.New("--agent requires at least one non-empty agent")
	}
	return agents, nil
}

func flattenForPrint(t map[string]any, prefix string) map[string]any {
	out := map[string]any{}
	for k, v := range t {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if m, ok := v.(map[string]any); ok {
			for kk, vv := range flattenForPrint(m, key) {
				out[kk] = vv
			}
			continue
		}
		out[key] = v
	}
	return out
}

// resolveTeamDir resolves cfg.target into the absolute .agent_team/ path,
// emitting a stderr message and ExitCode(2) if missing — matches the Python
// helper of the same name.
func resolveTeamDir(cmd *cobra.Command, target string) (string, error) {
	target = effectiveRepoTarget(cmd, target)
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", exitErr(2)
	}
	if eval, err := filepath.EvalSymlinks(abs); err == nil {
		abs = eval
	}
	teamDir := filepath.Join(abs, loader.TeamDirName)
	st, err := os.Stat(teamDir)
	if err != nil || !st.IsDir() {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %s not found — run `agent-team init` first.\n", teamDir)
		return "", exitErr(2)
	}
	return teamDir, nil
}

func effectiveRepoTarget(cmd *cobra.Command, target string) string {
	if cmd != nil {
		if flag := cmd.Root().PersistentFlags().Lookup(rootRepoFlagName); flag != nil && flag.Changed {
			if value := strings.TrimSpace(flag.Value.String()); value != "" {
				return value
			}
		}
	}
	return target
}

// confirm reads a yes/no answer from cmd.InOrStdin(). Returns true on y/yes
// (case-insensitive), false on n/no/empty/EOF. Default-no.
func confirm(cmd *cobra.Command, prompt string) (bool, error) {
	fmt.Fprintf(cmd.OutOrStdout(), "%s [y/N]: ", prompt)
	r := bufio.NewReader(cmd.InOrStdin())
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	ans := strings.ToLower(strings.TrimSpace(line))
	return ans == "y" || ans == "yes", nil
}
