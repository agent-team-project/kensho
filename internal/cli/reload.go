package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
)

func newReloadCmd() *cobra.Command {
	var (
		target  string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "reload",
		Short: "Reload daemon topology and reconcile runtime metadata.",
		Long: "Re-read .agent_team/instances.toml in the running daemon and then reconcile daemon " +
			"metadata against the live process table. This is the operator path after editing declarations " +
			"when you do not want to restart agent-teamd.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team reload: --format cannot be combined with --json.")
				return exitErr(2)
			}
			formatTemplate, err := parseReloadFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team reload: %v\n", err)
				return exitErr(2)
			}
			return runReload(cmd, target, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render reload result with a Go template, e.g. '{{len .Topology.Instances}} {{.Reconcile.Changed}}'.")
	return cmd
}

type reloadClient interface {
	TopologyReload() (*topologyResponse, error)
	Reconcile() (*daemonReconcileResponse, error)
}

type reloadJSON struct {
	Topology  *topologyResponse        `json:"topology"`
	Reconcile *daemonReconcileResponse `json:"reconcile"`
}

func runReload(cmd *cobra.Command, target string, jsonOut bool, tmpl *template.Template) error {
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	dc, err := newDaemonClient(teamDir)
	if err != nil {
		if errors.Is(err, errDaemonNotRunning) {
			fmt.Fprintln(cmd.ErrOrStderr(), "agent-team reload: daemon is not running — start it with `agent-team start`.")
			return exitErr(1)
		}
		return err
	}
	if err := runReloadWithClient(cmd.OutOrStdout(), dc, jsonOut, tmpl); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team reload: %v\n", err)
		return exitErr(1)
	}
	return nil
}

func runReloadWithClient(w io.Writer, client reloadClient, jsonOut bool, tmpl *template.Template) error {
	topo, err := client.TopologyReload()
	if err != nil {
		return err
	}
	rec, err := client.Reconcile()
	if err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(w).Encode(reloadJSON{
			Topology:  topo,
			Reconcile: rec,
		})
	}
	if tmpl != nil {
		return renderReloadFormat(w, reloadJSON{Topology: topo, Reconcile: rec}, tmpl)
	}
	count := 0
	if topo != nil {
		count = len(topo.Instances)
	}
	fmt.Fprintf(w, "topology: reloaded %d declared instance(s)\n", count)
	return renderDaemonReconcile(w, rec)
}

func parseReloadFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("reload-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderReloadFormat(w io.Writer, result reloadJSON, tmpl *template.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}
