package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	texttemplate "text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	jobpkg "github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/loader"
	"github.com/jamesaud/agent-team/internal/runtimebin"
	"github.com/spf13/cobra"
)

var runtimeLookPath = exec.LookPath

func newRuntimeCmd() *cobra.Command {
	var (
		target        string
		jsonOut       bool
		format        string
		runtimeKind   string
		runtimeBinary string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "Inspect the selected LLM runtime profile.",
		Long: "Inspect the selected LLM runtime profile, binary resolution, and whether " +
			"the runtime supports direct runs, daemon dispatch, direct resume, managed resume, and native subagents.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRuntimeProfileCommand(cmd, "agent-team runtime", target, runtimeKind, runtimeBinary, jsonOut, format)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root or any path under a repo.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render runtime info with a Go template, e.g. '{{.Runtime}} {{.Available}}'.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile to inspect for this invocation (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBinary, "runtime-bin", "", "Runtime binary to inspect for this invocation. Overrides env and repo config.")
	cmd.AddCommand(newRuntimeSetCmd())
	cmd.AddCommand(newRuntimeUnsetCmd())
	cmd.AddCommand(newRuntimeProfileCmd())
	cmd.AddCommand(newRuntimeLsCmd())
	cmd.AddCommand(newRuntimeMetadataCmd())
	cmd.AddCommand(newRuntimeProbeCmd())
	cmd.AddCommand(newRuntimeAdoptCmd())
	cmd.AddCommand(newRuntimeResumePlanCmd())
	return cmd
}

func newRuntimeSetCmd() *cobra.Command {
	var (
		target        string
		runtimeBinary string
		dryRun        bool
		commands      bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "set <runtime>",
		Aliases: []string{"configure", "use"},
		Short:   "Set the repo default runtime profile.",
		Long: "Set the repo default LLM runtime profile in .agent_team/config.toml. " +
			"Command flags and AGENT_TEAM_RUNTIME / AGENT_TEAM_RUNTIME_BIN still override this repo default at runtime.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team runtime set: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team runtime set: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team runtime set: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team runtime set: --commands requires --dry-run.")
				return exitErr(2)
			}
			tmpl, err := parseRuntimeFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team runtime set: %v\n", err)
				return exitErr(2)
			}
			kind, err := runtimebin.ParseKind(args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team runtime set: runtime must be %q or %q.\n", runtimebin.KindClaude, runtimebin.KindCodex)
				return exitErr(2)
			}
			binary := strings.TrimSpace(runtimeBinary)
			if binary == "" {
				binary = runtimebin.DefaultBinaryForKind(kind)
			}
			result, err := runRuntimeSetCommand(cmd, target, kind, binary, dryRun)
			if err != nil {
				var ec ExitCode
				if errors.As(err, &ec) {
					return err
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team runtime set: %v\n", err)
				return exitErr(2)
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			if commands {
				return renderRuntimeSetApplyCommand(cmd.OutOrStdout(), result, runtimeSetApplyCommandOptions{
					Repo:      runtimeCommandRepo(cmd, target),
					RepoSet:   runtimeCommandRepoSet(cmd),
					RepoFlag:  runtimeCommandRepoFlag(cmd),
					BinarySet: cmd.Flags().Changed("runtime-bin") || cmd.Flags().Changed("binary"),
				})
			}
			if tmpl != nil {
				return renderRuntimeFormat(cmd.OutOrStdout(), result, tmpl)
			}
			renderRuntimeSetResult(cmd.OutOrStdout(), result)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root or any path under a repo.")
	cmd.Flags().StringVar(&runtimeBinary, "runtime-bin", "", "Runtime binary or wrapper to store. Defaults to the runtime's built-in binary.")
	cmd.Flags().StringVar(&runtimeBinary, "binary", "", "Alias for --runtime-bin.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the config change without writing.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the apply command. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the set result with a Go template, e.g. '{{.Runtime}} {{.Binary}}'.")
	return cmd
}

func newRuntimeUnsetCmd() *cobra.Command {
	var (
		target   string
		dryRun   bool
		commands bool
		jsonOut  bool
		format   string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "unset",
		Aliases: []string{"reset", "clear"},
		Short:   "Remove the repo default runtime profile.",
		Long: "Remove [runtime].kind, [runtime].binary, and [runtime].bin from .agent_team/config.toml " +
			"so the repo falls back to environment variables or built-in runtime defaults.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team runtime unset: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team runtime unset: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team runtime unset: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team runtime unset: --commands requires --dry-run.")
				return exitErr(2)
			}
			tmpl, err := parseRuntimeFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team runtime unset: %v\n", err)
				return exitErr(2)
			}
			result, err := runRuntimeUnsetCommand(cmd, target, dryRun)
			if err != nil {
				var ec ExitCode
				if errors.As(err, &ec) {
					return err
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team runtime unset: %v\n", err)
				return exitErr(2)
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			if commands {
				return renderRuntimeUnsetApplyCommand(cmd.OutOrStdout(), result, runtimeUnsetApplyCommandOptions{
					Repo:     runtimeCommandRepo(cmd, target),
					RepoSet:  runtimeCommandRepoSet(cmd),
					RepoFlag: runtimeCommandRepoFlag(cmd),
				})
			}
			if tmpl != nil {
				return renderRuntimeFormat(cmd.OutOrStdout(), result, tmpl)
			}
			renderRuntimeUnsetResult(cmd.OutOrStdout(), result)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root or any path under a repo.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the config change without writing.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the apply command. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the unset result with a Go template, e.g. '{{.Changed}} {{.DryRun}}'.")
	return cmd
}

func newRuntimeProfileCmd() *cobra.Command {
	var (
		target        string
		jsonOut       bool
		format        string
		runtimeKind   string
		runtimeBinary string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "profile",
		Aliases: []string{"show"},
		Short:   "Show the selected runtime profile.",
		Long: "Show the selected LLM runtime profile, binary resolution, and whether " +
			"the runtime supports direct runs, daemon dispatch, direct resume, managed resume, and native subagents.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRuntimeProfileCommand(cmd, "agent-team runtime profile", target, runtimeKind, runtimeBinary, jsonOut, format)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root or any path under a repo.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render runtime info with a Go template, e.g. '{{.Runtime}} {{.Available}}'.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile to inspect for this invocation (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBinary, "runtime-bin", "", "Runtime binary to inspect for this invocation. Overrides env and repo config.")
	return cmd
}

func runRuntimeProfileCommand(cmd *cobra.Command, label, target, runtimeKind, runtimeBinary string, jsonOut bool, format string) error {
	if format != "" && jsonOut {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: --format cannot be combined with --json.\n", label)
		return exitErr(2)
	}
	tmpl, err := parseRuntimeFormat(format)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", label, err)
		return exitErr(2)
	}
	info, err := collectRuntimeInfoForTargetWithSelection(effectiveRepoTarget(cmd, target), runtimeSelection{
		Kind:   runtimeKind,
		Binary: runtimeBinary,
	})
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", label, err)
		return exitErr(2)
	}
	if jsonOut {
		if err := json.NewEncoder(cmd.OutOrStdout()).Encode(info); err != nil {
			return err
		}
	} else if tmpl != nil {
		if err := renderRuntimeFormat(cmd.OutOrStdout(), info, tmpl); err != nil {
			return err
		}
	} else {
		renderRuntimeInfo(cmd.OutOrStdout(), info)
	}
	if !info.Available {
		return exitErr(1)
	}
	return nil
}

type runtimeSetResult struct {
	ConfigPath string   `json:"config_path"`
	Runtime    string   `json:"runtime"`
	Binary     string   `json:"binary"`
	Changed    bool     `json:"changed"`
	DryRun     bool     `json:"dry_run,omitempty"`
	Notes      []string `json:"notes,omitempty"`
}

type runtimeUnsetResult struct {
	ConfigPath string   `json:"config_path"`
	Changed    bool     `json:"changed"`
	DryRun     bool     `json:"dry_run,omitempty"`
	Notes      []string `json:"notes,omitempty"`
}

type runtimeSetApplyCommandOptions struct {
	Repo      string
	RepoSet   bool
	RepoFlag  string
	BinarySet bool
}

type runtimeUnsetApplyCommandOptions struct {
	Repo     string
	RepoSet  bool
	RepoFlag string
}

type runtimeInfo struct {
	Runtime        string   `json:"runtime"`
	Lifecycle      string   `json:"lifecycle,omitempty"`
	Agent          string   `json:"agent,omitempty"`
	Selected       bool     `json:"selected,omitempty"`
	Binary         string   `json:"binary"`
	RuntimeBinary  string   `json:"runtime_binary,omitempty"`
	Path           string   `json:"path,omitempty"`
	Available      bool     `json:"available"`
	DirectRun      bool     `json:"direct_run"`
	DaemonDispatch bool     `json:"daemon_dispatch"`
	DirectResume   bool     `json:"direct_resume"`
	ManagedResume  bool     `json:"managed_resume"`
	Resume         bool     `json:"resume"`
	Subagents      bool     `json:"subagents"`
	EnvRuntime     string   `json:"env_runtime,omitempty"`
	EnvBinary      string   `json:"env_binary,omitempty"`
	ConfigPath     string   `json:"config_path,omitempty"`
	Job            string   `json:"job,omitempty"`
	Ticket         string   `json:"ticket,omitempty"`
	Branch         string   `json:"branch,omitempty"`
	PR             string   `json:"pr,omitempty"`
	PID            int      `json:"pid,omitempty"`
	Workspace      string   `json:"workspace,omitempty"`
	SessionID      string   `json:"session_id,omitempty"`
	StartedAt      string   `json:"started_at,omitempty"`
	StoppedAt      string   `json:"stopped_at,omitempty"`
	ExitedAt       string   `json:"exited_at,omitempty"`
	ExitCode       *int     `json:"exit_code,omitempty"`
	LogPath        string   `json:"log_path,omitempty"`
	Adopted        bool     `json:"adopted,omitempty"`
	Notes          []string `json:"notes,omitempty"`
}

type runtimeSelection struct {
	Kind   string
	Binary string
}

func newRuntimeLsCmd() *cobra.Command {
	var (
		target  string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List supported runtime profiles.",
		Long: "List supported runtime profiles, binary resolution, availability, and runtime capabilities. " +
			"The selected row is the profile the current environment or repo config would use by default.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team runtime ls: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseRuntimeFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team runtime ls: %v\n", err)
				return exitErr(2)
			}
			rows, err := collectRuntimeListForTarget(effectiveRepoTarget(cmd, target))
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team runtime ls: %v\n", err)
				return exitErr(2)
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(rows)
			}
			if tmpl != nil {
				return renderRuntimeListFormat(cmd.OutOrStdout(), rows, tmpl)
			}
			renderRuntimeList(cmd.OutOrStdout(), rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root or any path under a repo.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each runtime row with a Go template, e.g. '{{.Runtime}} {{.Available}}'.")
	return cmd
}

func newRuntimeMetadataCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "metadata",
		Aliases: []string{"meta"},
		Short:   "Inspect persisted daemon runtime metadata.",
		Long:    "Inspect raw daemon runtime metadata persisted under .agent_team/daemon without adding declared-but-not-started placeholders.",
	}
	cmd.AddCommand(newRuntimeMetadataLsCmd())
	return cmd
}

func newRuntimeMetadataLsCmd() *cobra.Command {
	var (
		target           string
		statusFilters    []string
		runtimeFilters   []string
		agentFilters     []string
		instanceFilters  []string
		runtimeStaleOnly bool
		unhealthyOnly    bool
		latest           bool
		last             int
		sortBy           string
		summary          bool
		jsonOut          bool
		format           string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "ls [<instance>...]",
		Aliases: []string{"list", "ps"},
		Short:   "List persisted daemon runtime metadata.",
		Long:    "List raw daemon runtime metadata persisted for this repo without adding declared-but-not-started placeholders.",
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && (jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team runtime metadata ls: --format cannot be combined with --json or --summary.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team runtime metadata ls: --last must be >= 0.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team runtime metadata ls: choose one of --latest or --last.")
				return exitErr(2)
			}
			for _, arg := range args {
				if strings.TrimSpace(arg) == "" {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team runtime metadata ls: instance names must be non-empty.")
					return exitErr(2)
				}
			}
			tmpl, err := parseTeamRuntimeFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team runtime metadata ls: %v\n", err)
				return exitErr(2)
			}
			effectiveInstances := append([]string(nil), instanceFilters...)
			effectiveInstances = append(effectiveInstances, args...)
			opts, err := newTeamRuntimeListOptions(statusFilters, runtimeFilters, agentFilters, effectiveInstances, runtimeStaleOnly, unhealthyOnly)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team runtime metadata ls: %v\n", err)
				return exitErr(2)
			}
			sortMode, err := parseTeamRuntimeSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team runtime metadata ls: %v\n", err)
				return exitErr(2)
			}
			opts.Sort = sortMode
			opts.SortSet = cmd.Flags().Changed("sort")
			opts.Limit = last
			if latest {
				opts.Limit = 1
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			rows, err := collectRuntimeMetadataRows(teamDir, time.Now().UTC(), opts)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team runtime metadata ls: %v\n", err)
				return exitErr(1)
			}
			if summary {
				out := summarizeTeamRuntimeRows(rows)
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
				}
				return renderTeamRuntimeSummary(cmd.OutOrStdout(), out)
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(rows)
			}
			if tmpl != nil {
				return renderTeamRuntimeFormat(cmd.OutOrStdout(), rows, tmpl)
			}
			return renderTeamRuntimeRows(cmd.OutOrStdout(), rows)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root containing .agent_team (legacy; prefer global --repo).")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only show metadata with this status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only show metadata for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&agentFilters, "agent", nil, "Only show metadata for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&instanceFilters, "instance", nil, "Only show metadata with this instance name. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&runtimeStaleOnly, "runtime-stale", false, "Only show running metadata whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only show crashed or runtime-stale metadata.")
	cmd.Flags().BoolVarP(&latest, "latest", "l", false, "Show only the most recently started runtime metadata record after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Show only the N most recently started runtime metadata records after other filters (0 = all).")
	cmd.Flags().StringVar(&sortBy, "sort", "instance", "Sort runtime metadata rows by instance, status, runtime, agent, stale, unhealthy, job, started, stopped, or exited.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Summarize matching runtime metadata by status, runtime, and agent.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit runtime metadata as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each runtime metadata row with a Go template, e.g. '{{.Instance}} {{.Runtime}} {{.Status}}'.")
	return cmd
}

func collectRuntimeMetadataRows(teamDir string, now time.Time, opts teamRuntimeListOptions) ([]teamRuntimeRow, error) {
	metas, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	jobsByID := runtimeMetadataJobsByID(teamDir)
	rows := make([]teamRuntimeRow, 0, len(metas))
	for _, meta := range metas {
		row := teamRuntimeRowFromMetadata(meta, now)
		if j := jobsByID[jobpkg.NormalizeID(row.Job)]; j != nil {
			enrichJobRuntimeRow(&row, j)
		}
		if !teamRuntimeRowMatches(row, opts) {
			continue
		}
		rows = append(rows, row)
	}
	return filterLimitSortTeamRuntimeRows(rows, opts), nil
}

func runtimeMetadataJobsByID(teamDir string) map[string]*jobpkg.Job {
	out := map[string]*jobpkg.Job{}
	jobs, err := jobpkg.List(teamDir)
	if err != nil {
		return out
	}
	for _, j := range jobs {
		if j == nil {
			continue
		}
		if id := jobpkg.NormalizeID(j.ID); id != "" {
			out[id] = j
		}
	}
	return out
}

func runtimeFromConfigWithOverrides(configPath string, selection runtimeSelection) (runtimebin.Runtime, error) {
	if kindRaw := strings.TrimSpace(selection.Kind); kindRaw != "" {
		kind, err := runtimebin.ParseKind(kindRaw)
		if err != nil {
			return runtimebin.Runtime{}, fmt.Errorf("--runtime must be %q or %q", runtimebin.KindClaude, runtimebin.KindCodex)
		}
		rt := runtimebin.Runtime{Kind: kind, Binary: runtimebin.DefaultBinaryForKind(kind)}
		if bin := strings.TrimSpace(selection.Binary); bin != "" {
			rt.Binary = bin
		}
		return rt, nil
	}
	rt, err := runtimebin.CurrentFromConfig(configPath)
	if err != nil {
		return runtimebin.Runtime{}, err
	}
	if bin := strings.TrimSpace(selection.Binary); bin != "" {
		rt.Binary = bin
	}
	if strings.TrimSpace(rt.Binary) == "" {
		rt.Binary = runtimebin.DefaultBinaryForKind(rt.Kind)
	}
	return rt, nil
}

func runRuntimeSetCommand(cmd *cobra.Command, target string, kind runtimebin.Kind, binary string, dryRun bool) (runtimeSetResult, error) {
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return runtimeSetResult{}, err
	}
	configPath := filepath.Join(teamDir, "config.toml")
	result := runtimeSetResult{
		ConfigPath: filepath.ToSlash(configPath),
		Runtime:    string(kind),
		Binary:     binary,
		DryRun:     dryRun,
		Notes:      runtimeConfigEnvOverrideNotes(),
	}
	body, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return runtimeSetResult{}, err
	}
	current := ""
	if err == nil {
		current = string(body)
	}
	next := updateRuntimeConfigContent(current, kind, binary)
	result.Changed = next != current
	if !dryRun && result.Changed {
		mode := os.FileMode(0o644)
		if st, statErr := os.Stat(configPath); statErr == nil {
			mode = st.Mode().Perm()
		}
		if err := os.WriteFile(configPath, []byte(next), mode); err != nil {
			return runtimeSetResult{}, err
		}
	}
	return result, nil
}

func runRuntimeUnsetCommand(cmd *cobra.Command, target string, dryRun bool) (runtimeUnsetResult, error) {
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return runtimeUnsetResult{}, err
	}
	configPath := filepath.Join(teamDir, "config.toml")
	result := runtimeUnsetResult{
		ConfigPath: filepath.ToSlash(configPath),
		DryRun:     dryRun,
		Notes:      runtimeConfigEnvOverrideNotes(),
	}
	body, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return runtimeUnsetResult{}, err
	}
	current := string(body)
	next := unsetRuntimeConfigContent(current)
	result.Changed = next != current
	if !dryRun && result.Changed {
		mode := os.FileMode(0o644)
		if st, statErr := os.Stat(configPath); statErr == nil {
			mode = st.Mode().Perm()
		}
		if err := os.WriteFile(configPath, []byte(next), mode); err != nil {
			return runtimeUnsetResult{}, err
		}
	}
	return result, nil
}

func runtimeConfigEnvOverrideNotes() []string {
	var notes []string
	if strings.TrimSpace(os.Getenv(runtimebin.EnvRuntime)) != "" {
		notes = append(notes, runtimebin.EnvRuntime+" is set; it overrides repo config in the current environment")
	}
	if strings.TrimSpace(os.Getenv(runtimebin.EnvBinary)) != "" {
		notes = append(notes, runtimebin.EnvBinary+" is set; it overrides repo config in the current environment")
	}
	return notes
}

func updateRuntimeConfigContent(current string, kind runtimebin.Kind, binary string) string {
	newline := runtimeConfigNewline(current)
	replacement := []string{
		"[runtime]" + newline,
		"kind = " + strconv.Quote(string(kind)) + newline,
		"binary = " + strconv.Quote(binary) + newline,
	}
	lines := splitLinesAfter(current)
	for i, line := range lines {
		if !isRuntimeTableHeader(line) {
			continue
		}
		end := i + 1
		for end < len(lines) && !isTOMLTableHeader(lines[end]) {
			end++
		}
		out := make([]string, 0, len(lines)+len(replacement))
		out = append(out, lines[:i]...)
		out = append(out, replacement...)
		for _, existing := range lines[i+1 : end] {
			if isRuntimeConfigKeyLine(existing) {
				continue
			}
			out = append(out, existing)
		}
		out = append(out, lines[end:]...)
		return strings.Join(out, "")
	}
	if strings.TrimSpace(current) == "" {
		return strings.Join(replacement, "")
	}
	out := current
	if !strings.HasSuffix(out, "\n") {
		out += newline
	}
	if !strings.HasSuffix(out, newline+newline) {
		out += newline
	}
	out += strings.Join(replacement, "")
	return out
}

func unsetRuntimeConfigContent(current string) string {
	lines := splitLinesAfter(current)
	for i, line := range lines {
		if !isRuntimeTableHeader(line) {
			continue
		}
		end := i + 1
		for end < len(lines) && !isTOMLTableHeader(lines[end]) {
			end++
		}
		kept := make([]string, 0, end-i-1)
		for _, existing := range lines[i+1 : end] {
			if isRuntimeConfigKeyLine(existing) {
				continue
			}
			kept = append(kept, existing)
		}
		out := make([]string, 0, len(lines))
		out = append(out, lines[:i]...)
		if runtimeSectionHasNonCommentContent(kept) {
			out = append(out, lines[i])
			out = append(out, kept...)
		}
		out = append(out, lines[end:]...)
		return strings.Join(out, "")
	}
	return current
}

func runtimeSectionHasNonCommentContent(lines []string) bool {
	for _, line := range lines {
		trimmed := strings.TrimSpace(stripTOMLLineComment(line))
		if trimmed != "" {
			return true
		}
	}
	return false
}

func splitLinesAfter(value string) []string {
	if value == "" {
		return nil
	}
	return strings.SplitAfter(value, "\n")
}

func runtimeConfigNewline(value string) string {
	if strings.Contains(value, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

func isRuntimeTableHeader(line string) bool {
	return normalizedTOMLHeader(line) == "[runtime]"
}

func isTOMLTableHeader(line string) bool {
	header := normalizedTOMLHeader(line)
	return strings.HasPrefix(header, "[") && strings.HasSuffix(header, "]")
}

func normalizedTOMLHeader(line string) string {
	line = stripTOMLLineComment(line)
	return strings.TrimSpace(line)
}

func stripTOMLLineComment(line string) string {
	if idx := strings.Index(line, "#"); idx >= 0 {
		return line[:idx]
	}
	return line
}

func isRuntimeConfigKeyLine(line string) bool {
	line = strings.TrimSpace(stripTOMLLineComment(line))
	if line == "" {
		return false
	}
	key, _, ok := strings.Cut(line, "=")
	if !ok {
		return false
	}
	switch strings.TrimSpace(key) {
	case "kind", "binary", "bin":
		return true
	default:
		return false
	}
}

func collectRuntimeInfo() (runtimeInfo, error) {
	return collectRuntimeInfoForConfig("")
}

func collectRuntimeInfoForTarget(target string) (runtimeInfo, error) {
	return collectRuntimeInfoForConfig(runtimeConfigPathForTarget(target))
}

func collectRuntimeInfoForTargetWithSelection(target string, selection runtimeSelection) (runtimeInfo, error) {
	return collectRuntimeInfoForConfigWithSelection(runtimeConfigPathForTarget(target), selection)
}

func collectRuntimeInfoForTeam(teamDir string) (runtimeInfo, error) {
	if teamDir == "" {
		return collectRuntimeInfo()
	}
	return collectRuntimeInfoForConfig(filepath.Join(teamDir, "config.toml"))
}

func collectRuntimeInfoForConfig(configPath string) (runtimeInfo, error) {
	return collectRuntimeInfoForConfigWithSelection(configPath, runtimeSelection{})
}

func collectRuntimeInfoForConfigWithSelection(configPath string, selection runtimeSelection) (runtimeInfo, error) {
	rt, err := runtimeFromConfigWithOverrides(configPath, selection)
	if err != nil {
		return runtimeInfo{}, err
	}
	info := runtimeInfo{
		Runtime:    string(rt.Kind),
		Binary:     rt.Binary,
		EnvRuntime: os.Getenv(runtimebin.EnvRuntime),
		EnvBinary:  os.Getenv(runtimebin.EnvBinary),
		ConfigPath: filepath.ToSlash(configPath),
		DirectRun:  true,
	}
	if path, err := runtimeLookPath(rt.Binary); err == nil {
		info.Path = path
		info.Available = true
	} else if errors.Is(err, exec.ErrNotFound) {
		info.Available = false
	} else if err != nil {
		info.Notes = append(info.Notes, "binary lookup failed: "+err.Error())
	}
	switch rt.Kind {
	case runtimebin.KindClaude:
		info.DaemonDispatch = true
		info.DirectResume = true
		info.ManagedResume = true
		info.Resume = true
		info.Subagents = true
	case runtimebin.KindCodex:
		info.DaemonDispatch = true
		info.DirectResume = true
		info.Notes = append(info.Notes, "codex adapter supports direct launches and daemon-managed one-shot exec runs with --prompt; AGENT_TEAM_* vars are exposed to Codex shell commands; direct codex resume is available outside agent-team managed instances; managed resume and native subagent registration are not available")
	default:
		return runtimeInfo{}, fmt.Errorf("unsupported runtime %q", rt.Kind)
	}
	if !info.Available {
		info.Notes = append(info.Notes, fmt.Sprintf("runtime binary %q was not found in PATH", rt.Binary))
	}
	return info, nil
}

func collectRuntimeListForTarget(target string) ([]runtimeInfo, error) {
	return collectRuntimeListForConfig(runtimeConfigPathForTarget(target))
}

func collectRuntimeListForConfig(configPath string) ([]runtimeInfo, error) {
	selected, err := runtimeFromConfigWithOverrides(configPath, runtimeSelection{})
	if err != nil {
		return nil, err
	}
	kinds := []runtimebin.Kind{runtimebin.KindClaude, runtimebin.KindCodex}
	rows := make([]runtimeInfo, 0, len(kinds))
	for _, kind := range kinds {
		binary := runtimebin.DefaultBinaryForKind(kind)
		isSelected := selected.Kind == kind
		if isSelected {
			binary = selected.Binary
		}
		info, err := collectRuntimeInfoForConfigWithSelection(configPath, runtimeSelection{
			Kind:   string(kind),
			Binary: binary,
		})
		if err != nil {
			return nil, err
		}
		info.Selected = isSelected
		rows = append(rows, info)
	}
	return rows, nil
}

func runtimeConfigPathForTarget(target string) string {
	abs, err := filepath.Abs(target)
	if err != nil {
		return ""
	}
	if eval, err := filepath.EvalSymlinks(abs); err == nil {
		abs = eval
	}
	for {
		candidate := filepath.Join(abs, loader.TeamDirName, "config.toml")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return ""
		}
		abs = parent
	}
}

func renderRuntimeInfo(w fmtWriter, info runtimeInfo) {
	fmt.Fprintf(w, "runtime:          %s\n", info.Runtime)
	fmt.Fprintf(w, "binary:           %s\n", info.Binary)
	if info.Path != "" {
		fmt.Fprintf(w, "path:             %s\n", info.Path)
	} else {
		fmt.Fprintln(w, "path:             (not found)")
	}
	fmt.Fprintf(w, "available:        %s\n", runtimeYesNo(info.Available))
	fmt.Fprintf(w, "direct_run:       %s\n", runtimeYesNo(info.DirectRun))
	fmt.Fprintf(w, "daemon_dispatch:  %s\n", runtimeYesNo(info.DaemonDispatch))
	fmt.Fprintf(w, "direct_resume:    %s\n", runtimeYesNo(info.DirectResume))
	fmt.Fprintf(w, "managed_resume:   %s\n", runtimeYesNo(info.ManagedResume))
	fmt.Fprintf(w, "resume:           %s\n", runtimeYesNo(info.Resume))
	fmt.Fprintf(w, "subagents:        %s\n", runtimeYesNo(info.Subagents))
	if info.EnvRuntime != "" {
		fmt.Fprintf(w, "%s: %s\n", runtimebin.EnvRuntime, info.EnvRuntime)
	}
	if info.EnvBinary != "" {
		fmt.Fprintf(w, "%s: %s\n", runtimebin.EnvBinary, info.EnvBinary)
	}
	if info.ConfigPath != "" {
		fmt.Fprintf(w, "config:           %s\n", info.ConfigPath)
	}
	for _, note := range info.Notes {
		fmt.Fprintf(w, "note:             %s\n", note)
	}
}

func renderRuntimeSetResult(w fmtWriter, result runtimeSetResult) {
	fmt.Fprintf(w, "config:  %s\n", result.ConfigPath)
	fmt.Fprintf(w, "runtime: %s\n", result.Runtime)
	fmt.Fprintf(w, "binary:  %s\n", result.Binary)
	fmt.Fprintf(w, "changed: %s\n", runtimeYesNo(result.Changed))
	if result.DryRun {
		fmt.Fprintln(w, "dry_run: yes")
	}
	for _, note := range result.Notes {
		fmt.Fprintf(w, "note:    %s\n", note)
	}
}

func renderRuntimeUnsetResult(w fmtWriter, result runtimeUnsetResult) {
	fmt.Fprintf(w, "config:  %s\n", result.ConfigPath)
	fmt.Fprintf(w, "changed: %s\n", runtimeYesNo(result.Changed))
	if result.DryRun {
		fmt.Fprintln(w, "dry_run: yes")
	}
	for _, note := range result.Notes {
		fmt.Fprintf(w, "note:    %s\n", note)
	}
}

func renderRuntimeSetApplyCommand(w fmtWriter, result runtimeSetResult, opts runtimeSetApplyCommandOptions) error {
	if !result.Changed {
		return nil
	}
	_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(runtimeSetApplyCommandArgs(result, opts)), " "))
	return err
}

func runtimeSetApplyCommandArgs(result runtimeSetResult, opts runtimeSetApplyCommandOptions) []string {
	args := []string{"agent-team", "runtime", "set"}
	args = appendRuntimeRepoArgs(args, opts.RepoFlag, opts.Repo, opts.RepoSet)
	args = append(args, result.Runtime)
	if opts.BinarySet && strings.TrimSpace(result.Binary) != "" {
		args = append(args, "--runtime-bin", result.Binary)
	}
	return args
}

func renderRuntimeUnsetApplyCommand(w fmtWriter, result runtimeUnsetResult, opts runtimeUnsetApplyCommandOptions) error {
	if !result.Changed {
		return nil
	}
	args := []string{"agent-team", "runtime", "unset"}
	args = appendRuntimeRepoArgs(args, opts.RepoFlag, opts.Repo, opts.RepoSet)
	_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(args), " "))
	return err
}

func appendRuntimeRepoArgs(args []string, repoFlag, repo string, repoSet bool) []string {
	if !repoSet || strings.TrimSpace(repo) == "" {
		return args
	}
	flag := strings.TrimSpace(repoFlag)
	if flag == "" {
		flag = "target"
	}
	return append(args, "--"+flag, repo)
}

func runtimeCommandRepoSet(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	if flag := cmd.Root().PersistentFlags().Lookup(rootRepoFlagName); flag != nil && flag.Changed {
		return true
	}
	return cmd.Flags().Changed("target")
}

func runtimeCommandRepoFlag(cmd *cobra.Command) string {
	return rootRepoFlagName
}

func runtimeCommandRepo(cmd *cobra.Command, target string) string {
	if cmd != nil {
		if flag := cmd.Root().PersistentFlags().Lookup(rootRepoFlagName); flag != nil && flag.Changed {
			if value := strings.TrimSpace(flag.Value.String()); value != "" {
				return value
			}
		}
	}
	return target
}

func renderRuntimeList(w fmtWriter, rows []runtimeInfo) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no runtimes)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RUNTIME\tSELECTED\tBINARY\tPATH\tAVAILABLE\tDIRECT\tDAEMON\tRESUME\tMANAGED\tSUBAGENTS")
	for _, row := range rows {
		path := row.Path
		if path == "" {
			path = "(not found)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.Runtime,
			runtimeYesNo(row.Selected),
			row.Binary,
			path,
			runtimeYesNo(row.Available),
			runtimeYesNo(row.DirectRun),
			runtimeYesNo(row.DaemonDispatch),
			runtimeYesNo(row.Resume),
			runtimeYesNo(row.ManagedResume),
			runtimeYesNo(row.Subagents),
		)
	}
	_ = tw.Flush()
}

func parseRuntimeFormat(format string) (*texttemplate.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := texttemplate.New("runtime-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderRuntimeListFormat(w fmtWriter, rows []runtimeInfo, tmpl *texttemplate.Template) error {
	for _, row := range rows {
		if err := renderRuntimeFormat(w, row, tmpl); err != nil {
			return err
		}
	}
	return nil
}

func renderRuntimeFormat(w fmtWriter, data any, tmpl *texttemplate.Template) error {
	if err := tmpl.Execute(w, data); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func runtimeYesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
