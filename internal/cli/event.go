package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/template"

	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

// newEventCmd registers `event publish <type>` — manual injection of a
// topology event for testing trigger matching. Routes through the daemon's
// /v1/event endpoint.
func newEventCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "event",
		Short: "Publish manual topology events to the daemon (for testing trigger matching).",
	}
	cmd.AddCommand(newEventPublishCmd())
	return cmd
}

func newEventPublishCmd() *cobra.Command {
	var (
		target      string
		payload     string
		payloadFile string
		dryRun      bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	c := &cobra.Command{
		Use:   "publish <type>",
		Short: "Publish an event of the given type. The daemon resolves it against declared triggers.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team event publish: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if strings.TrimSpace(payload) != "" && strings.TrimSpace(payloadFile) != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team event publish: choose one of --payload or --payload-file.")
				return exitErr(2)
			}
			formatTemplate, err := parseEventPublishFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team event publish: %v\n", err)
				return exitErr(2)
			}
			eventType := args[0]
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			body := map[string]any{}
			payloadBody := []byte(strings.TrimSpace(payload))
			if strings.TrimSpace(payloadFile) != "" {
				payloadBody, err = readPayloadFile(payloadFile)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team event publish: %v\n", err)
					return exitErr(2)
				}
			}
			if len(payloadBody) > 0 {
				if err := json.Unmarshal(payloadBody, &body); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team event publish: payload is not valid JSON: %v\n", err)
					return exitErr(2)
				}
				if body == nil {
					body = map[string]any{}
				}
			}
			if dryRun {
				preview, err := previewEventPublish(teamDir, eventType, body)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team event publish: %v\n", err)
					return exitErr(1)
				}
				return renderEventPublishPreview(cmd.OutOrStdout(), preview, jsonOut, formatTemplate)
			}
			dc, err := newDaemonClient(teamDir)
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: daemon is not running — start it first with `agent-team daemon start`.")
				return exitErr(2)
			}
			res, err := dc.PublishEvent(eventType, body)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(1)
			}
			out := cmd.OutOrStdout()
			if jsonOut {
				return json.NewEncoder(out).Encode(res)
			}
			if formatTemplate != nil {
				return renderEventPublishFormat(out, res, formatTemplate)
			}
			if len(res.Matched) == 0 {
				fmt.Fprintln(out, "(no triggers matched)")
				return nil
			}
			fmt.Fprintf(out, "Matched: %s\n", strings.Join(res.Matched, ", "))
			for _, d := range res.Dispatched {
				name, _ := d["instance"].(string)
				id, _ := d["instance_id"].(string)
				fmt.Fprintf(out, "  dispatched %s as %s\n", name, id)
			}
			for _, n := range res.Queued {
				fmt.Fprintf(out, "  queued %s (at replica capacity)\n", n)
			}
			for _, n := range res.Messaged {
				fmt.Fprintf(out, "  messaged %s\n", n)
			}
			for _, r := range res.Rejected {
				name, _ := r["instance"].(string)
				reason, _ := r["reason"].(string)
				fmt.Fprintf(out, "  rejected %s: %s\n", name, reason)
			}
			return nil
		},
	}
	c.Flags().StringVar(&target, "target", cwd, "Repo root.")
	c.Flags().StringVar(&payload, "payload", "", "JSON object passed as the event payload (e.g. '{\"target\":\"worker\"}').")
	c.Flags().StringVar(&payloadFile, "payload-file", "", "Read event payload JSON from a file, or '-' for stdin.")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Preview matching triggers without publishing to the daemon.")
	c.Flags().BoolVar(&jsonOut, "json", false, "Emit the daemon event outcome as JSON.")
	c.Flags().StringVar(&format, "format", "", "Render the event outcome or dry-run preview with a Go template, e.g. '{{len .Matched}} {{len .Dispatched}}'.")
	return c
}

type eventPublishPreview struct {
	Type      string         `json:"type"`
	Payload   map[string]any `json:"payload"`
	Matched   []string       `json:"matched"`
	Pipelines []string       `json:"pipelines,omitempty"`
	DryRun    bool           `json:"dry_run"`
}

func previewEventPublish(teamDir, eventType string, payload map[string]any) (*eventPublishPreview, error) {
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return nil, err
	}
	preview := &eventPublishPreview{
		Type:    eventType,
		Payload: payload,
		Matched: []string{},
		DryRun:  true,
	}
	if top == nil {
		return preview, nil
	}
	for _, inst := range top.Resolve(eventType, payload) {
		preview.Matched = append(preview.Matched, inst.Name)
	}
	for _, pipeline := range top.ResolvePipelines(eventType, payload) {
		preview.Pipelines = append(preview.Pipelines, pipeline.Name)
	}
	return preview, nil
}

func renderEventPublishPreview(w io.Writer, preview *eventPublishPreview, jsonOut bool, tmpl *template.Template) error {
	if preview == nil {
		preview = &eventPublishPreview{Matched: []string{}, DryRun: true}
	}
	if jsonOut {
		return json.NewEncoder(w).Encode(preview)
	}
	if tmpl != nil {
		if err := tmpl.Execute(w, preview); err != nil {
			return err
		}
		_, err := fmt.Fprintln(w)
		return err
	}
	fmt.Fprintf(w, "Event: %s\n", preview.Type)
	fmt.Fprintln(w, "Dry run: true")
	if len(preview.Matched) == 0 && len(preview.Pipelines) == 0 {
		fmt.Fprintln(w, "(no triggers matched)")
		return nil
	}
	if len(preview.Matched) > 0 {
		fmt.Fprintf(w, "Matched: %s\n", strings.Join(preview.Matched, ", "))
	}
	if len(preview.Pipelines) > 0 {
		fmt.Fprintf(w, "Pipelines: %s\n", strings.Join(preview.Pipelines, ", "))
	}
	return nil
}

func parseEventPublishFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("event-publish-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderEventPublishFormat(w io.Writer, res *eventResponse, tmpl *template.Template) error {
	if err := tmpl.Execute(w, res); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}
