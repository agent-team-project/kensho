package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/jamesaud/agent-team/internal/topology"
)

// newTopologyCmd registers the `topology` group: read-only inspection of the
// declared topology plus an explicit `reload`. Uses the daemon's
// /v1/topology endpoints when running; falls back to local file parsing so
// `agent-team topology` is useful even before the daemon is started.
func newTopologyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "topology",
		Short: "Show declared instances and triggers (reads .agent_team/instances.toml).",
	}
	cmd.AddCommand(newTopologyShowCmd())
	cmd.AddCommand(newTopologyReloadCmd())
	return cmd
}

func newTopologyShowCmd() *cobra.Command {
	var (
		target  string
		asJSON  bool
	)
	cwd, _ := os.Getwd()
	c := &cobra.Command{
		Use:   "show",
		Short: "Print the resolved topology (declared instances + triggers).",
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			return runTopologyShow(cmd, teamDir, asJSON)
		},
	}
	c.Flags().StringVar(&target, "target", cwd, "Repo root.")
	c.Flags().BoolVar(&asJSON, "json", false, "Emit raw JSON.")
	return c
}

func newTopologyReloadCmd() *cobra.Command {
	var target string
	cwd, _ := os.Getwd()
	c := &cobra.Command{
		Use:   "reload",
		Short: "Re-read instances.toml from disk (daemon must be running).",
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			dc, err := newDaemonClient(teamDir)
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: daemon is not running — start it first with `agent-team daemon start`.")
				return exitErr(2)
			}
			res, err := dc.TopologyReload()
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(1)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Reloaded — %d instance(s) declared.\n", len(res.Instances))
			return nil
		},
	}
	c.Flags().StringVar(&target, "target", cwd, "Repo root.")
	return c
}

// runTopologyShow prints either the daemon's view (if running, includes
// runtime running/queued counts) or a file-only view.
func runTopologyShow(cmd *cobra.Command, teamDir string, asJSON bool) error {
	// Prefer daemon-sourced topology — it includes per-instance running
	// counters. Fall back to parsing instances.toml ourselves so the command
	// is useful before the daemon is started.
	if dc, err := newDaemonClient(teamDir); err == nil {
		res, err := dc.Topology()
		if err == nil {
			if asJSON {
				body, _ := json.MarshalIndent(res, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(body))
				return nil
			}
			printDaemonTopology(cmd.OutOrStdout(), res)
			return nil
		}
	}
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(1)
	}
	if top == nil || len(top.Instances) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(no instances declared — add .agent_team/instances.toml)")
		return nil
	}
	if asJSON {
		// Mirror the daemon shape so consumers don't branch.
		body, _ := json.MarshalIndent(toResponseLike(top), "", "  ")
		fmt.Fprintln(cmd.OutOrStdout(), string(body))
		return nil
	}
	printLocalTopology(cmd.OutOrStdout(), top)
	return nil
}

func toResponseLike(top *topology.Topology) map[string]any {
	out := make([]map[string]any, 0, len(top.Instances))
	for _, inst := range top.SortedInstances() {
		out = append(out, map[string]any{
			"name":        inst.Name,
			"agent":       inst.Agent,
			"ephemeral":   inst.Ephemeral,
			"description": inst.Description,
			"replicas":    inst.Replicas,
			"config":      map[string]any(inst.Config),
			"triggers":    triggersAsMaps(inst.Triggers),
		})
	}
	return map[string]any{"instances": out}
}

func triggersAsMaps(triggers []*topology.Trigger) []map[string]any {
	out := make([]map[string]any, 0, len(triggers))
	for _, t := range triggers {
		match := map[string]any{}
		for k, mv := range t.Match {
			if mv.Single != "" {
				match[k] = mv.Single
			} else if len(mv.List) > 0 {
				match[k] = mv.List
			}
		}
		out = append(out, map[string]any{"event": t.Event, "match": match})
	}
	return out
}

func printDaemonTopology(w io.Writer, res *topologyResponse) {
	if len(res.Instances) == 0 {
		fmt.Fprintln(w, "(no instances declared)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tAGENT\tEPHEMERAL\tREPLICAS\tTRIGGERS\tRUNNING\tQUEUED")
	for _, i := range res.Instances {
		eph := "no"
		if i.Ephemeral {
			eph = "yes"
		}
		trigSummary := summariseTriggers(i.Triggers)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%d\t%d\n",
			i.Name, i.Agent, eph, i.Replicas, trigSummary, i.Running, i.Queued)
	}
	_ = tw.Flush()
}

func printLocalTopology(w io.Writer, top *topology.Topology) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tAGENT\tEPHEMERAL\tREPLICAS\tTRIGGERS")
	for _, inst := range top.SortedInstances() {
		eph := "no"
		if inst.Ephemeral {
			eph = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n",
			inst.Name, inst.Agent, eph, inst.Replicas, summariseLocalTriggers(inst.Triggers))
	}
	_ = tw.Flush()
}

func summariseTriggers(triggers []map[string]interface{}) string {
	if len(triggers) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(triggers))
	for _, t := range triggers {
		ev, _ := t["event"].(string)
		match, _ := t["match"].(map[string]interface{})
		if len(match) > 0 {
			keys := make([]string, 0, len(match))
			for k := range match {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			parts = append(parts, fmt.Sprintf("%s(%s)", ev, strings.Join(keys, ",")))
		} else {
			parts = append(parts, ev)
		}
	}
	return strings.Join(parts, ", ")
}

func summariseLocalTriggers(triggers []*topology.Trigger) string {
	if len(triggers) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(triggers))
	for _, t := range triggers {
		if len(t.Match) > 0 {
			keys := make([]string, 0, len(t.Match))
			for k := range t.Match {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			parts = append(parts, fmt.Sprintf("%s(%s)", t.Event, strings.Join(keys, ",")))
		} else {
			parts = append(parts, t.Event)
		}
	}
	return strings.Join(parts, ", ")
}
