package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	texttemplate "text/template"

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
	cmd.AddCommand(newRuntimeProfileCmd())
	cmd.AddCommand(newRuntimeLsCmd())
	cmd.AddCommand(newRuntimeProbeCmd())
	cmd.AddCommand(newRuntimeResumePlanCmd())
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

type runtimeInfo struct {
	Runtime        string   `json:"runtime"`
	Selected       bool     `json:"selected,omitempty"`
	Binary         string   `json:"binary"`
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

func renderRuntimeFormat(w fmtWriter, info runtimeInfo, tmpl *texttemplate.Template) error {
	if err := tmpl.Execute(w, info); err != nil {
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
