package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"text/template"
	"time"

	"github.com/spf13/cobra"
)

func newNextCmd() *cobra.Command {
	var (
		target        string
		teamName      string
		limit         int
		scheduleLimit int
		watch         bool
		noClear       bool
		interval      time.Duration
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "next",
		Short: "Print recommended next operator actions.",
		Long: "Print recommended next operator actions from the read-only overview. " +
			"Use --team to scope recommendations to one declared team.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team next: --interval must be >= 0.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team next: --limit must be >= 0.")
				return exitErr(2)
			}
			if scheduleLimit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team next: --schedule-limit must be >= 0.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team next: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseNextFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team next: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			collect := func(now time.Time) (*overviewResult, error) {
				if strings.TrimSpace(teamName) != "" {
					return collectTeamOverview(teamDir, teamName, now, scheduleLimit)
				}
				return collectOverview(teamDir, now, scheduleLimit), nil
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				if err := runNextWatch(ctx, cmd.OutOrStdout(), collect, limit, jsonOut, tmpl, interval, !noClear && !jsonOut); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team next: %v\n", err)
					return exitErr(1)
				}
				return nil
			}
			overview, err := collect(time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team next: %v\n", err)
				return exitErr(1)
			}
			return renderNextActionResult(cmd.OutOrStdout(), nextActionResultFromOverview(overview, limit), jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().StringVar(&teamName, "team", "", "Scope recommendations to this declared team.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Show at most this many actions; 0 means all.")
	cmd.Flags().IntVar(&scheduleLimit, "schedule-limit", 5, "Upcoming schedules to inspect while building recommendations; 0 means all.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh recommended actions until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit recommended actions as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the next-action result with a Go template, e.g. '{{.State}} {{len .Actions}}'.")
	return cmd
}

func newTeamNextCmd() *cobra.Command {
	var (
		repo          string
		limit         int
		scheduleLimit int
		watch         bool
		noClear       bool
		interval      time.Duration
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "next <team>",
		Short: "Print recommended next actions scoped to one team.",
		Long:  "Print recommended next operator actions from the read-only team overview.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team next: --interval must be >= 0.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team next: --limit must be >= 0.")
				return exitErr(2)
			}
			if scheduleLimit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team next: --schedule-limit must be >= 0.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team next: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseNextFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team next: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			teamName := args[0]
			collect := func(now time.Time) (*overviewResult, error) {
				return collectTeamOverview(teamDir, teamName, now, scheduleLimit)
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				if err := runNextWatch(ctx, cmd.OutOrStdout(), collect, limit, jsonOut, tmpl, interval, !noClear && !jsonOut); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team next: %v\n", err)
					return exitErr(1)
				}
				return nil
			}
			overview, err := collect(time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team next: %v\n", err)
				return exitErr(1)
			}
			return renderNextActionResult(cmd.OutOrStdout(), nextActionResultFromOverview(overview, limit), jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Show at most this many actions; 0 means all.")
	cmd.Flags().IntVar(&scheduleLimit, "schedule-limit", 5, "Upcoming schedules to inspect while building recommendations; 0 means all.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh recommended actions until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit recommended actions as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the next-action result with a Go template, e.g. '{{.Team.Name}} {{len .Actions}}'.")
	return cmd
}

type nextActionResult struct {
	OK            bool                 `json:"ok"`
	State         string               `json:"state"`
	CapturedAt    string               `json:"captured_at,omitempty"`
	Team          *teamInfo            `json:"team,omitempty"`
	Actions       []string             `json:"actions"`
	ActionDetails []operatorActionHint `json:"action_details,omitempty"`
	TotalActions  int                  `json:"total_actions"`
	HiddenActions int                  `json:"hidden_actions,omitempty"`
}

func nextActionResultFromOverview(overview *overviewResult, limit int) nextActionResult {
	if overview == nil {
		return nextActionResult{OK: true, State: "ok", Actions: []string{}}
	}
	actions := append([]string{}, overview.Actions...)
	details := nextActionDetailsFromOverview(overview, actions)
	total := len(actions)
	hidden := 0
	if limit > 0 && len(actions) > limit {
		hidden = len(actions) - limit
		actions = actions[:limit]
		if len(details) > limit {
			details = details[:limit]
		}
	}
	return nextActionResult{
		OK:            overview.OK,
		State:         overview.State,
		CapturedAt:    overview.CapturedAt,
		Team:          overview.Team,
		Actions:       actions,
		ActionDetails: details,
		TotalActions:  total,
		HiddenActions: hidden,
	}
}

func nextActionDetailsFromOverview(overview *overviewResult, actions []string) []operatorActionHint {
	if overview == nil || len(actions) == 0 {
		return nil
	}
	if len(overview.ActionDetails) > 0 {
		detailsByCommand := make(map[string]operatorActionHint, len(overview.ActionDetails))
		for _, detail := range overview.ActionDetails {
			if strings.TrimSpace(detail.Command) == "" {
				continue
			}
			if _, exists := detailsByCommand[detail.Command]; !exists {
				detailsByCommand[detail.Command] = detail
			}
		}
		details := make([]operatorActionHint, 0, len(actions))
		for _, action := range actions {
			if detail, ok := detailsByCommand[action]; ok {
				details = append(details, detail)
			}
		}
		if len(details) == len(actions) {
			return details
		}
	}
	team := ""
	if overview.Team != nil {
		team = overview.Team.Name
	}
	details := make([]operatorActionHint, 0, len(actions))
	for _, action := range actions {
		detail := operatorActionHint{Command: action, Source: "overview"}
		if team != "" {
			detail.Team = team
		}
		details = append(details, detail)
	}
	return details
}

func renderNextActionResult(w io.Writer, result nextActionResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if tmpl != nil {
		return renderNextActionFormat(w, result, tmpl)
	}
	fmt.Fprintf(w, "next: %s\n", result.State)
	if result.Team != nil {
		fmt.Fprintf(w, "team: %s\n", result.Team.Name)
	}
	if len(result.Actions) == 0 {
		fmt.Fprintln(w, "actions: none")
		return nil
	}
	fmt.Fprintln(w, "actions:")
	for _, action := range result.Actions {
		fmt.Fprintf(w, "  %s\n", action)
	}
	if result.HiddenActions > 0 {
		fmt.Fprintf(w, "  ... %d more\n", result.HiddenActions)
	}
	return nil
}

func parseNextFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("next-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderNextActionFormat(w io.Writer, result nextActionResult, tmpl *template.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func runNextWatch(ctx context.Context, w io.Writer, collect func(time.Time) (*overviewResult, error), limit int, jsonOut bool, tmpl *template.Template, interval time.Duration, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if !jsonOut {
			if err := writeWatchClear(w, clear); err != nil {
				return err
			}
		}
		overview, err := collect(time.Now().UTC())
		if err != nil {
			return err
		}
		if err := renderNextActionResult(w, nextActionResultFromOverview(overview, limit), jsonOut, tmpl); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if !jsonOut && !clear {
				fmt.Fprintln(w)
			}
		}
	}
}
