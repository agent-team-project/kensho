package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"text/template"

	"github.com/jamesaud/agent-team/internal/intake"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newScheduleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schedule",
		Short: "Inspect and run declared schedule events.",
		Long:  "Inspect schedules declared in .agent_team/instances.toml and manually publish their schedule events.",
	}
	cmd.AddCommand(newScheduleLsCmd())
	cmd.AddCommand(newScheduleShowCmd())
	cmd.AddCommand(newScheduleRunCmd())
	return cmd
}

func newScheduleLsCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List declared schedules.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			schedules, err := loadScheduleInfos(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule ls: %v\n", err)
				return exitErr(1)
			}
			return renderScheduleList(cmd.OutOrStdout(), schedules, jsonOut)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit schedules as JSON.")
	return cmd
}

func newScheduleShowCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <schedule>",
		Short: "Show one declared schedule.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			info, err := loadScheduleInfo(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule show: %v\n", err)
				return exitErr(1)
			}
			return renderScheduleDetail(cmd.OutOrStdout(), info, jsonOut)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the schedule as JSON.")
	return cmd
}

func newScheduleRunCmd() *cobra.Command {
	var (
		repo    string
		dryRun  bool
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "run <schedule>",
		Short: "Publish one declared schedule event now.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule run: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseIntakeFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule run: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			info, err := loadScheduleInfo(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule run: %v\n", err)
				return exitErr(1)
			}
			ev := &intake.Event{Type: topology.EventSchedule, Payload: copyMap(info.Payload)}
			if dryRun {
				return renderIntakeDryRun(cmd.OutOrStdout(), ev, jsonOut, tmpl)
			}
			return publishScheduleEvent(cmd, repo, ev, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the schedule event without publishing it.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the event and outcome as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the event result with a Go template, e.g. '{{.Event.Type}} {{.DryRun}}'.")
	return cmd
}

type scheduleInfo struct {
	Name       string         `json:"name"`
	Event      string         `json:"event"`
	Every      string         `json:"every"`
	RunOnStart bool           `json:"run_on_start"`
	Payload    map[string]any `json:"payload"`
}

func loadScheduleInfos(teamDir string) ([]scheduleInfo, error) {
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return nil, err
	}
	if top == nil {
		return nil, nil
	}
	infos := make([]scheduleInfo, 0, len(top.Schedules))
	for _, s := range top.SortedSchedules() {
		infos = append(infos, scheduleInfoFromTopology(s))
	}
	return infos, nil
}

func loadScheduleInfo(teamDir, name string) (scheduleInfo, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return scheduleInfo{}, fmt.Errorf("schedule name is required")
	}
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return scheduleInfo{}, err
	}
	if top == nil || top.Schedules[name] == nil {
		return scheduleInfo{}, fmt.Errorf("schedule %q not found", name)
	}
	return scheduleInfoFromTopology(top.Schedules[name]), nil
}

func scheduleInfoFromTopology(s *topology.Schedule) scheduleInfo {
	return scheduleInfo{
		Name:       s.Name,
		Event:      topology.EventSchedule,
		Every:      s.Every.String(),
		RunOnStart: s.RunOnStart,
		Payload:    s.EventPayload(),
	}
}

func renderScheduleList(w io.Writer, schedules []scheduleInfo, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(schedules)
	}
	if len(schedules) == 0 {
		fmt.Fprintln(w, "(no schedules declared)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SCHEDULE\tEVERY\tRUN_ON_START\tPAYLOAD")
	for _, info := range schedules {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", info.Name, info.Every, yesNo(info.RunOnStart), summariseSchedulePayload(info.Payload))
	}
	_ = tw.Flush()
	return nil
}

func renderScheduleDetail(w io.Writer, info scheduleInfo, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(info)
	}
	fmt.Fprintf(w, "Schedule:     %s\n", info.Name)
	fmt.Fprintf(w, "Event:        %s\n", info.Event)
	fmt.Fprintf(w, "Every:        %s\n", info.Every)
	fmt.Fprintf(w, "Run On Start: %s\n", yesNo(info.RunOnStart))
	fmt.Fprintf(w, "Payload:      %s\n", summariseSchedulePayload(info.Payload))
	return nil
}

func publishScheduleEvent(cmd *cobra.Command, target string, ev *intake.Event, jsonOut bool, tmpl *template.Template) error {
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	dc, err := newDaemonClient(teamDir)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule run: daemon is not running — start it first with `agent-team daemon start`.")
		return exitErr(2)
	}
	res, err := dc.PublishEvent(ev.Type, ev.Payload)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule run: %v\n", err)
		return exitErr(1)
	}
	result := intakePublishResult{Event: ev, Outcome: res}
	if jsonOut {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}
	if tmpl != nil {
		return renderIntakeTemplate(cmd.OutOrStdout(), result, tmpl)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Event: %s\n", ev.Type)
	return renderIntakeOutcome(cmd.OutOrStdout(), res)
}

func summariseSchedulePayload(payload map[string]any) string {
	if len(payload) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(payload))
	for key := range payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", key, payload[key]))
	}
	return strings.Join(parts, ",")
}

func copyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
