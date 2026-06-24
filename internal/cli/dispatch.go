package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newDispatchCmd() *cobra.Command {
	var (
		repoTarget  string
		name        string
		source      string
		workspace   string
		kickoff     string
		kickoffFile string
		runtimeKind string
		runtimeBin  string
		dryRun      bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "dispatch <target> <ticket> [kickoff...]",
		Short: "Dispatch an agent through daemon topology.",
		Long: "Dispatch an agent through daemon topology by publishing an `agent.dispatch` event. " +
			"This is the human-friendly wrapper for the common manager-to-worker path.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team dispatch: --format cannot be combined with --json.")
				return exitErr(2)
			}
			formatTemplate, err := parseDispatchFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team dispatch: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repoTarget)
			if err != nil {
				return err
			}
			targetAgent := args[0]
			ticket := args[1]
			kickoffText, err := dispatchKickoff(ticket, kickoff, kickoffFile, args[2:])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team dispatch: %v\n", err)
				return exitErr(2)
			}
			payload, requestedName, err := buildDispatchEventPayload(targetAgent, ticket, kickoffText, name, source, workspace)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team dispatch: %v\n", err)
				return exitErr(2)
			}
			if err := applyDispatchRuntimeSelection(teamDir, payload, runtimeSelection{
				Kind:   runtimeKind,
				Binary: runtimeBin,
			}); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team dispatch: %v\n", err)
				return exitErr(2)
			}
			if dryRun {
				preview, err := previewDispatchPayload(teamDir, targetAgent, requestedName, payload)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team dispatch: %v\n", err)
					return exitErr(1)
				}
				return renderDispatchRoutePreview(cmd.OutOrStdout(), preview, jsonOut, formatTemplate)
			}
			dc, err := newDaemonClient(teamDir)
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team dispatch: daemon is not running — start it with `agent-team start`.")
				return exitErr(2)
			}
			res, err := dc.PublishEvent("agent.dispatch", payload)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team dispatch: %v\n", err)
				return exitErr(1)
			}
			out := cmd.OutOrStdout()
			if jsonOut {
				return json.NewEncoder(out).Encode(res)
			}
			if formatTemplate != nil {
				return renderDispatchFormat(out, res, formatTemplate)
			}
			renderDispatchOutcome(out, targetAgent, requestedName, res)
			return nil
		},
	}
	cmd.Flags().StringVar(&repoTarget, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&name, "name", "", "Requested instance name (default: <target>-<ticket-slug>).")
	cmd.Flags().StringVar(&source, "source", "", "Source instance for the dispatch event (default: AGENT_TEAM_INSTANCE or cli).")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for spawned children: auto, worktree, or repo.")
	cmd.Flags().StringVar(&kickoff, "kickoff", "", "Kickoff text for the dispatched agent.")
	cmd.Flags().StringVar(&kickoffFile, "kickoff-file", "", "Read kickoff text from a file.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for the dispatched instance (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for the dispatched instance. Overrides env and repo config.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview topology matches without publishing to the daemon.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the daemon event outcome as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the event outcome or dry-run preview with a Go template.")
	return cmd
}

type dispatchRoutePreview struct {
	Target        string               `json:"target"`
	RequestedName string               `json:"requested_name"`
	Preview       *eventPublishPreview `json:"preview"`
	DryRun        bool                 `json:"dry_run"`
}

func previewDispatchPayload(teamDir, target, requestedName string, payload map[string]any) (*dispatchRoutePreview, error) {
	preview, err := previewEventPublish(teamDir, topology.EventAgentDispatch, payload)
	if err != nil {
		return nil, err
	}
	return &dispatchRoutePreview{
		Target:        target,
		RequestedName: requestedName,
		Preview:       preview,
		DryRun:        true,
	}, nil
}

func renderDispatchRoutePreview(w io.Writer, result *dispatchRoutePreview, jsonOut bool, tmpl *template.Template) error {
	if result == nil {
		result = &dispatchRoutePreview{DryRun: true}
	}
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if tmpl != nil {
		if err := tmpl.Execute(w, result); err != nil {
			return err
		}
		_, err := fmt.Fprintln(w)
		return err
	}
	fmt.Fprintf(w, "Dispatch: %s instance=%s\n", result.Target, result.RequestedName)
	fmt.Fprintln(w, "Dry run: true")
	if result.Preview == nil || !eventPublishPreviewHasRoutes(result.Preview) {
		fmt.Fprintln(w, "(no triggers matched)")
		return nil
	}
	return renderEventPublishRoutePreview(w, result.Preview)
}

func buildDispatchEventPayload(targetAgent, ticket, kickoff, name, source, workspace string) (map[string]any, string, error) {
	targetAgent = strings.TrimSpace(targetAgent)
	ticket = strings.TrimSpace(ticket)
	if targetAgent == "" {
		return nil, "", fmt.Errorf("target is required")
	}
	if ticket == "" {
		return nil, "", fmt.Errorf("ticket is required")
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = os.Getenv("AGENT_TEAM_INSTANCE")
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "cli"
	}
	if strings.TrimSpace(name) == "" {
		slug := dispatchSlug(ticket)
		if slug == "" {
			return nil, "", fmt.Errorf("ticket %q produced an empty instance suffix", ticket)
		}
		name = targetAgent + "-" + slug
	}
	workspace, err := dispatchWorkspace(targetAgent, workspace)
	if err != nil {
		return nil, "", err
	}
	payload := map[string]any{
		"source":    source,
		"target":    targetAgent,
		"name":      name,
		"ticket":    ticket,
		"kickoff":   kickoff,
		"workspace": workspace,
	}
	return payload, name, nil
}

func applyDispatchRuntimeSelection(teamDir string, payload map[string]any, selection runtimeSelection) error {
	if strings.TrimSpace(selection.Kind) == "" && strings.TrimSpace(selection.Binary) == "" {
		return nil
	}
	rt, err := runtimeFromConfigWithOverrides(filepath.Join(teamDir, "config.toml"), selection)
	if err != nil {
		return err
	}
	payload["runtime"] = string(rt.Kind)
	payload["runtime_binary"] = rt.Binary
	return nil
}

func dispatchWorkspace(targetAgent, workspace string) (string, error) {
	mode := strings.TrimSpace(workspace)
	switch mode {
	case "", "auto":
		if targetAgent == "worker" {
			return "worktree", nil
		}
		return "repo", nil
	case "worktree", "repo":
		return mode, nil
	default:
		return "", fmt.Errorf("--workspace must be auto, worktree, or repo")
	}
}

func dispatchKickoff(ticket, flagValue, fileValue string, positional []string) (string, error) {
	sources := 0
	if strings.TrimSpace(flagValue) != "" {
		sources++
	}
	if strings.TrimSpace(fileValue) != "" {
		sources++
	}
	if len(positional) > 0 {
		sources++
	}
	if sources > 1 {
		return "", fmt.Errorf("provide kickoff text using only one of positional args, --kickoff, or --kickoff-file")
	}
	var text string
	switch {
	case strings.TrimSpace(fileValue) != "":
		body, err := os.ReadFile(filepath.Clean(fileValue))
		if err != nil {
			return "", fmt.Errorf("--kickoff-file: %w", err)
		}
		text = string(body)
	case strings.TrimSpace(flagValue) != "":
		text = flagValue
	case len(positional) > 0:
		text = strings.Join(positional, " ")
	default:
		text = ticket
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("kickoff text is empty")
	}
	if strings.Contains(strings.ToLower(text), strings.ToLower(ticket)) {
		return text, nil
	}
	return ticket + ": " + text, nil
}

func dispatchSlug(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func parseDispatchFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("dispatch-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderDispatchFormat(w io.Writer, res *eventResponse, tmpl *template.Template) error {
	if err := tmpl.Execute(w, res); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func renderDispatchOutcome(w io.Writer, targetAgent, requestedName string, res *eventResponse) {
	if len(res.Matched) == 0 {
		fmt.Fprintf(w, "(no triggers matched agent.dispatch target=%s)\n", targetAgent)
		return
	}
	fmt.Fprintf(w, "Matched: %s\n", strings.Join(res.Matched, ", "))
	for _, d := range res.Dispatched {
		name, _ := d["instance"].(string)
		id, _ := d["instance_id"].(string)
		fmt.Fprintf(w, "  dispatched %s as %s\n", name, id)
		fmt.Fprintf(w, "  follow: agent-team logs %s --follow\n", id)
	}
	for _, n := range res.Queued {
		fmt.Fprintf(w, "  queued %s (at replica capacity)\n", n)
		if requestedName != "" {
			fmt.Fprintf(w, "  requested instance: %s\n", requestedName)
		}
	}
	for _, n := range res.Messaged {
		fmt.Fprintf(w, "  messaged %s\n", n)
	}
	for _, r := range res.Rejected {
		name, _ := r["instance"].(string)
		reason, _ := r["reason"].(string)
		fmt.Fprintf(w, "  rejected %s: %s\n", name, reason)
		if requestedName != "" && (strings.Contains(reason, "already running") || strings.Contains(reason, "already queued")) {
			fmt.Fprintf(w, "  follow-up: agent-team send %s <message>\n", requestedName)
		}
	}
}
