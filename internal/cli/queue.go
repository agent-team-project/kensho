package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/spf13/cobra"
)

func newQueueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "Inspect and control persisted daemon event queue items.",
		Long:  "Inspect and control persisted daemon event queue items under `.agent_team/daemon/queue/`.",
	}
	cmd.AddCommand(newQueueLsCmd())
	cmd.AddCommand(newQueueShowCmd())
	cmd.AddCommand(newQueueRetryCmd())
	cmd.AddCommand(newQueueDropCmd())
	return cmd
}

func newQueueLsCmd() *cobra.Command {
	var (
		target      string
		stateFilter string
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List persisted queue items.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue ls: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseQueueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue ls: %v\n", err)
				return exitErr(2)
			}
			state, err := parseQueueStateFilter(stateFilter)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue ls: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			items, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
			if err != nil {
				return err
			}
			filtered := filterQueueItems(items, state)
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(filtered)
			}
			if tmpl != nil {
				return renderQueueItemsFormat(cmd.OutOrStdout(), filtered, tmpl)
			}
			renderQueueTable(cmd.OutOrStdout(), filtered)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "Filter by queue state: pending or dead.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each queue item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func newQueueShowCmd() *cobra.Command {
	var (
		target  string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one persisted queue item.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue show: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseQueueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue show: %v\n", err)
				return exitErr(2)
			}
			item, err := readQueueItemFromRepo(cmd, target, args[0])
			if err != nil {
				return err
			}
			return renderQueueItemResult(cmd.OutOrStdout(), item, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the queue item as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the queue item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func newQueueDropCmd() *cobra.Command {
	var (
		target  string
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "drop <id>",
		Short: "Drop a pending or dead-letter queue item.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			id := args[0]
			if dc, err := newDaemonClient(teamDir); err == nil {
				err = dc.QueueDrop(id)
				if err != nil {
					return err
				}
			} else if errors.Is(err, errDaemonNotRunning) {
				if err := daemon.RemoveQueueItem(daemon.DaemonRoot(teamDir), id); err != nil {
					if errors.Is(err, fs.ErrNotExist) {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue drop: queue item %q not found.\n", id)
						return exitErr(2)
					}
					return err
				}
			} else {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"dropped": true, "id": id})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Dropped queue item %s\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	return cmd
}

func newQueueRetryCmd() *cobra.Command {
	var (
		target  string
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "retry <id>",
		Short: "Retry a pending or dead-letter queue item.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			id := args[0]
			if dc, err := newDaemonClient(teamDir); err == nil {
				outcome, err := dc.QueueRetry(id)
				if err != nil {
					return err
				}
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(outcome)
				}
				renderQueueRetryOutcome(cmd.OutOrStdout(), outcome)
				return nil
			} else if !errors.Is(err, errDaemonNotRunning) {
				return err
			}

			item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), id)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue retry: queue item %q not found.\n", id)
					return exitErr(2)
				}
				return err
			}
			if err := daemon.ResetQueueItemForRetry(daemon.DaemonRoot(teamDir), item); err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(item)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Queue item %s marked pending; start the daemon to dispatch it.\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	return cmd
}

func readQueueItemFromRepo(cmd *cobra.Command, target, id string) (*daemon.QueueItem, error) {
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return nil, err
	}
	item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), id)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue show: queue item %q not found.\n", id)
			return nil, exitErr(2)
		}
		return nil, err
	}
	return item, nil
}

func parseQueueStateFilter(raw string) (string, error) {
	state := strings.ToLower(strings.TrimSpace(raw))
	switch state {
	case "", daemon.QueueStatePending, daemon.QueueStateDead:
		return state, nil
	default:
		return "", fmt.Errorf("--state must be pending or dead")
	}
}

func filterQueueItems(items []*daemon.QueueItem, state string) []*daemon.QueueItem {
	if state == "" {
		return items
	}
	out := make([]*daemon.QueueItem, 0, len(items))
	for _, item := range items {
		if item.State == state {
			out = append(out, item)
		}
	}
	return out
}

func parseQueueFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("queue-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderQueueItemsFormat(w io.Writer, items []*daemon.QueueItem, tmpl *template.Template) error {
	for _, item := range items {
		if err := renderQueueItemTemplate(w, item, tmpl); err != nil {
			return err
		}
	}
	return nil
}

func renderQueueItemResult(w io.Writer, item *daemon.QueueItem, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(item)
	}
	if tmpl != nil {
		return renderQueueItemTemplate(w, item, tmpl)
	}
	renderQueueDetail(w, item)
	return nil
}

func renderQueueItemTemplate(w io.Writer, item *daemon.QueueItem, tmpl *template.Template) error {
	if err := tmpl.Execute(w, item); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func renderQueueTable(w io.Writer, items []*daemon.QueueItem) {
	if len(items) == 0 {
		fmt.Fprintln(w, "(no queue items)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tINSTANCE\tINSTANCE_ID\tATTEMPTS\tNEXT_RETRY\tLAST_ERROR")
	for _, item := range items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			item.ID, item.State, item.Instance, item.InstanceID, item.Attempts, queueTime(item.NextRetry), emptyDash(item.LastError))
	}
	_ = tw.Flush()
}

func renderQueueDetail(w io.Writer, item *daemon.QueueItem) {
	fmt.Fprintf(w, "ID:          %s\n", item.ID)
	fmt.Fprintf(w, "State:       %s\n", item.State)
	fmt.Fprintf(w, "Event:       %s\n", item.EventType)
	fmt.Fprintf(w, "Instance:    %s\n", item.Instance)
	fmt.Fprintf(w, "Instance ID: %s\n", item.InstanceID)
	fmt.Fprintf(w, "Attempts:    %d\n", item.Attempts)
	if !item.NextRetry.IsZero() {
		fmt.Fprintf(w, "Next Retry:  %s\n", item.NextRetry.Format(time.RFC3339))
	}
	if item.LastError != "" {
		fmt.Fprintf(w, "Last Error:  %s\n", item.LastError)
	}
	fmt.Fprintf(w, "Queued:      %s\n", item.QueuedAt.Format(time.RFC3339))
	fmt.Fprintf(w, "Updated:     %s\n", item.UpdatedAt.Format(time.RFC3339))
	if !item.DeadLetteredAt.IsZero() {
		fmt.Fprintf(w, "Dead:        %s\n", item.DeadLetteredAt.Format(time.RFC3339))
	}
	if len(item.Payload) > 0 {
		body, _ := json.MarshalIndent(item.Payload, "", "  ")
		fmt.Fprintf(w, "Payload:\n%s\n", string(body))
	}
}

func renderQueueRetryOutcome(w io.Writer, outcome *daemon.EventOutcome) {
	switch outcome.Action {
	case "dispatched":
		fmt.Fprintf(w, "Retried %s as %s\n", outcome.Instance, outcome.InstanceID)
	case "queued":
		fmt.Fprintf(w, "Queued %s as %s\n", outcome.Instance, outcome.InstanceID)
	case "rejected":
		fmt.Fprintf(w, "Rejected %s as %s: %s\n", outcome.Instance, outcome.InstanceID, outcome.Reason)
	default:
		fmt.Fprintf(w, "%s %s as %s\n", outcome.Action, outcome.Instance, outcome.InstanceID)
	}
}

func queueTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format(time.RFC3339)
}
