package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/spf13/cobra"
)

type extendCommandResult struct {
	Action           string `json:"action"`
	Instance         string `json:"instance"`
	Agent            string `json:"agent,omitempty"`
	Status           string `json:"status,omitempty"`
	PID              int    `json:"pid,omitempty"`
	Actor            string `json:"actor,omitempty"`
	By               string `json:"by"`
	RuntimeBudget    string `json:"runtime_budget,omitempty"`
	RuntimeElapsed   string `json:"runtime_elapsed,omitempty"`
	RuntimeRemaining string `json:"runtime_remaining,omitempty"`
	PreviousDeadline string `json:"previous_deadline,omitempty"`
	NewDeadline      string `json:"new_deadline,omitempty"`
}

func newExtendCmd() *cobra.Command {
	var (
		target  string
		by      time.Duration
		actor   string
		quiet   bool
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "extend <instance>",
		Short: "Extend a running instance watchdog deadline.",
		Long: "Extend the armed watchdog deadline for one running daemon-managed instance. " +
			"The command refuses instances that are not running or do not have an armed watchdog.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tmpl, err := parseExtendFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team extend: %v\n", err)
				return exitErr(2)
			}
			if by <= 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team extend: --by must be > 0.")
				return exitErr(2)
			}
			if quiet && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team extend: choose one of --quiet or --json.")
				return exitErr(2)
			}
			if format != "" && (quiet || jsonOut) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team extend: --format cannot be combined with --quiet or --json.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			res, err := extendInstanceWatchdog(teamDir, strings.TrimSpace(args[0]), by, actor)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team extend: %v\n", err)
				return exitErr(1)
			}
			row := extendCommandResultFromResponse(res, time.Now().UTC())
			if err := writeExtendAuditForMetadata(teamDir, res, by, actor, time.Now().UTC()); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team extend: %v\n", err)
				return exitErr(1)
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(row)
			}
			if tmpl != nil {
				return renderExtendFormat(cmd.OutOrStdout(), row, tmpl)
			}
			if quiet {
				return nil
			}
			renderExtendResult(cmd.OutOrStdout(), row)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().DurationVar(&by, "by", 0, "Amount to add to the running watchdog deadline, for example 30m.")
	cmd.Flags().StringVar(&actor, "actor", "cli", "Actor label recorded in lifecycle/audit events.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the extension result with a Go template, e.g. '{{.Instance}} {{.NewDeadline}}'.")
	_ = cmd.MarkFlagRequired("by")
	return cmd
}

func extendInstanceWatchdog(teamDir, instance string, by time.Duration, actor string) (*runtimeExtensionResponse, error) {
	if strings.TrimSpace(instance) == "" {
		return nil, fmt.Errorf("instance is required")
	}
	dc, err := newDaemonClient(teamDir)
	if err != nil {
		return nil, fmt.Errorf("daemon is not running")
	}
	return dc.ExtendInstance(instance, by, actor)
}

func extendCommandResultFromResponse(res *runtimeExtensionResponse, now time.Time) extendCommandResult {
	row := extendCommandResult{Action: "extended"}
	if res == nil {
		return row
	}
	row.By = (time.Duration(res.ByMillis) * time.Millisecond).String()
	row.Actor = strings.TrimSpace(res.Actor)
	row.PreviousDeadline = formatExtendDeadline(res.PreviousDeadline)
	row.NewDeadline = formatExtendDeadline(res.NewDeadline)
	meta := res.Metadata
	if meta == nil {
		row.Instance = strings.TrimSpace(res.InstanceID)
		return row
	}
	row.Instance = meta.Instance
	row.Agent = meta.Agent
	row.Status = string(meta.Status)
	row.PID = meta.PID
	row.RuntimeBudget = meta.RuntimeBudget
	row.RuntimeElapsed = metadataRuntimeBudgetElapsed(meta, now)
	row.RuntimeRemaining = metadataRuntimeBudgetRemaining(meta, now)
	return row
}

func formatExtendDeadline(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func renderExtendResult(w io.Writer, row extendCommandResult) {
	fmt.Fprintf(w, "extended %s by %s", row.Instance, row.By)
	if row.NewDeadline != "" {
		fmt.Fprintf(w, " deadline=%s", row.NewDeadline)
	}
	if row.RuntimeRemaining != "" {
		fmt.Fprintf(w, " remaining=%s", row.RuntimeRemaining)
	}
	fmt.Fprintln(w)
}

func parseExtendFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("extend-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderExtendFormat(w io.Writer, row extendCommandResult, tmpl *template.Template) error {
	if err := tmpl.Execute(w, row); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func extendAuditData(selection jobInstanceSelection, res *runtimeExtensionResponse, by time.Duration) map[string]string {
	data := jobInstanceSelectionAuditData(selection)
	if data == nil {
		data = map[string]string{}
	}
	data["amount"] = by.String()
	if res != nil {
		if !res.PreviousDeadline.IsZero() {
			data["previous_deadline"] = res.PreviousDeadline.UTC().Format(time.RFC3339)
		}
		if !res.NewDeadline.IsZero() {
			data["new_deadline"] = res.NewDeadline.UTC().Format(time.RFC3339)
		}
		if res.Metadata != nil {
			if res.Metadata.RuntimeBudget != "" {
				data["runtime_budget"] = res.Metadata.RuntimeBudget
			}
			if res.Metadata.Runtime != "" {
				data["runtime"] = res.Metadata.Runtime
			}
		}
	}
	return data
}
