package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/jamesaud/agent-team/internal/runtimebin"
	"github.com/spf13/cobra"
)

var runtimeLookPath = exec.LookPath

func newRuntimeCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "Inspect the selected LLM runtime profile.",
		Long: "Inspect the selected LLM runtime profile, binary resolution, and whether " +
			"the runtime supports direct runs, daemon dispatch, resume, and native subagents.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			info, err := collectRuntimeInfo()
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team runtime: %v\n", err)
				return exitErr(2)
			}
			if jsonOut {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(info); err != nil {
					return err
				}
			} else {
				renderRuntimeInfo(cmd.OutOrStdout(), info)
			}
			if !info.Available {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	return cmd
}

type runtimeInfo struct {
	Runtime        string   `json:"runtime"`
	Binary         string   `json:"binary"`
	Path           string   `json:"path,omitempty"`
	Available      bool     `json:"available"`
	DirectRun      bool     `json:"direct_run"`
	DaemonDispatch bool     `json:"daemon_dispatch"`
	Resume         bool     `json:"resume"`
	Subagents      bool     `json:"subagents"`
	EnvRuntime     string   `json:"env_runtime,omitempty"`
	EnvBinary      string   `json:"env_binary,omitempty"`
	Notes          []string `json:"notes,omitempty"`
}

func collectRuntimeInfo() (runtimeInfo, error) {
	rt, err := runtimebin.Current()
	if err != nil {
		return runtimeInfo{}, err
	}
	info := runtimeInfo{
		Runtime:    string(rt.Kind),
		Binary:     rt.Binary,
		EnvRuntime: os.Getenv(runtimebin.EnvRuntime),
		EnvBinary:  os.Getenv(runtimebin.EnvBinary),
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
		info.Resume = true
		info.Subagents = true
	case runtimebin.KindCodex:
		info.Notes = append(info.Notes, "codex direct adapter passes the agent prompt as the initial Codex prompt; daemon dispatch, resume, and native subagent registration are not available yet")
	default:
		return runtimeInfo{}, fmt.Errorf("unsupported runtime %q", rt.Kind)
	}
	if !info.Available {
		info.Notes = append(info.Notes, fmt.Sprintf("runtime binary %q was not found in PATH", rt.Binary))
	}
	return info, nil
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
	fmt.Fprintf(w, "resume:           %s\n", runtimeYesNo(info.Resume))
	fmt.Fprintf(w, "subagents:        %s\n", runtimeYesNo(info.Subagents))
	if info.EnvRuntime != "" {
		fmt.Fprintf(w, "%s: %s\n", runtimebin.EnvRuntime, info.EnvRuntime)
	}
	if info.EnvBinary != "" {
		fmt.Fprintf(w, "%s: %s\n", runtimebin.EnvBinary, info.EnvBinary)
	}
	for _, note := range info.Notes {
		fmt.Fprintf(w, "note:             %s\n", note)
	}
}

func runtimeYesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
