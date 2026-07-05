package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/agent-team-project/agent-team/internal/feedback"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/spf13/cobra"
)

func newFeedbackCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "feedback",
		Short: "Record and inspect local agent feedback.",
		Long: "Record and inspect local agent feedback under `.agent_team/feedback/items/`. " +
			"Feedback is local and file-backed; it does not contact Linear or require the daemon.",
	}
	cmd.AddCommand(newFeedbackSubmitCmd())
	cmd.AddCommand(newFeedbackListCmd())
	cmd.AddCommand(newFeedbackShowCmd())
	cmd.AddCommand(newFeedbackResolveCmd())
	return cmd
}

func newFeedbackSubmitCmd() *cobra.Command {
	var (
		repo        string
		categoryRaw string
		route       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "submit <text>",
		Short: "Submit one local feedback item.",
		Long: "Submit one local feedback item. The body is the only required input; " +
			"context is captured automatically from the agent-team environment and local metadata.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			category, err := feedback.ParseCategory(categoryRaw)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team feedback submit: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			input := feedback.SubmitInput{
				Body:     args[0],
				Category: category,
				Context:  feedback.CaptureContext(teamDir, BuildInfo()),
				Origin:   feedback.CaptureOrigin(teamDir, BuildInfo()),
			}
			if strings.TrimSpace(route) != "" {
				res, fallback, err := submitFeedbackRoute(teamDir, route, input)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team feedback submit: %v\n", err)
					return exitErr(1)
				}
				if fallback != "" {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team feedback submit: %s\n", fallback)
					fmt.Fprintf(cmd.OutOrStdout(), "submitted %s\n", res.ID)
					return nil
				}
				fmt.Fprintf(cmd.OutOrStdout(), "delivered %s via %s\n", res.ID, strings.TrimSpace(route))
				return nil
			}
			item, err := feedback.Submit(teamDir, input)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team feedback submit: %v\n", err)
				return exitErr(1)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "submitted %s\n", item.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&categoryRaw, "category", string(feedback.CategoryFriction), "Feedback category: friction, bug, idea, docs, or incident.")
	cmd.Flags().StringVar(&route, "route", "", "Feedback route name from [feedback.routes] in config.toml.")
	return cmd
}

func submitFeedbackRoute(teamDir, routeName string, input feedback.SubmitInput) (*feedback.Item, string, error) {
	route, err := feedback.ResolveRoute(teamDir, routeName)
	if err != nil {
		return retainFeedbackLocally(teamDir, input, fmt.Sprintf("route %q unavailable (%v); retained locally", strings.TrimSpace(routeName), err))
	}
	if route.Type != "local" {
		return retainFeedbackLocally(teamDir, input, fmt.Sprintf("route %q has unsupported type %q for direct submit; retained locally", route.Name, route.Type))
	}
	targetTeamDir := filepath.Join(route.Root, teamDirName)
	client, err := newDaemonClientWithTimeout(targetTeamDir, 5*time.Second)
	if err != nil {
		return retainFeedbackLocally(teamDir, input, fmt.Sprintf("route %q daemon unavailable (%v); retained locally", route.Name, err))
	}
	resp, err := client.FeedbackDeliver(feedback.DeliverInput{
		Body:     input.Body,
		Category: input.Category,
		Context:  input.Context,
		Origin:   input.Origin,
	})
	if err != nil {
		return retainFeedbackLocally(teamDir, input, fmt.Sprintf("route %q delivery failed (%v); retained locally", route.Name, err))
	}
	return &feedback.Item{
		ID:       resp.ID,
		TS:       resp.TS,
		Category: input.Category,
		Body:     strings.TrimSpace(input.Body),
		Status:   feedback.StatusNew,
		Context:  input.Context,
	}, "", nil
}

func retainFeedbackLocally(teamDir string, input feedback.SubmitInput, reason string) (*feedback.Item, string, error) {
	item, err := feedback.Submit(teamDir, input)
	if err != nil {
		return nil, "", err
	}
	return item, reason + " as " + item.ID, nil
}

func newFeedbackListCmd() *cobra.Command {
	var (
		repo      string
		statusRaw string
		group     bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List local feedback items.",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			statusFilter, err := feedback.ParseStatusFilter(statusRaw)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team feedback ls: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			items, err := feedback.List(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team feedback ls: %v\n", err)
				return exitErr(1)
			}
			items = feedback.FilterItems(items, statusFilter)
			if group {
				return renderFeedbackGroups(cmd.OutOrStdout(), feedback.GroupItems(items))
			}
			return renderFeedbackItems(cmd.OutOrStdout(), items)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&statusRaw, "status", string(feedback.StatusNew), "Filter by status: new, ticketed, dismissed, or all.")
	cmd.Flags().BoolVar(&group, "group", false, "Collapse rows by fingerprint and show count plus first/last seen.")
	return cmd
}

func newFeedbackShowCmd() *cobra.Command {
	var repo string
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one local feedback item.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			item, err := feedback.Read(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team feedback show: %v\n", err)
				return exitErr(1)
			}
			renderFeedbackDetail(cmd.OutOrStdout(), item)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	return cmd
}

func newFeedbackResolveCmd() *cobra.Command {
	var (
		repo    string
		ticket  string
		dismiss string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "resolve <id>",
		Short: "Resolve a local feedback item.",
		Long:  "Resolve a local feedback item as ticketed or dismissed. Exactly one of --ticket or --dismiss is required.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if (strings.TrimSpace(ticket) == "") == (strings.TrimSpace(dismiss) == "") {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team feedback resolve: exactly one of --ticket or --dismiss is required.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			item, err := feedback.Resolve(teamDir, args[0], feedback.ResolveInput{
				Ticket: ticket,
				Reason: dismiss,
			})
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team feedback resolve: %v\n", err)
				return exitErr(1)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "resolved %s as %s\n", item.ID, item.Status)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&ticket, "ticket", "", "Mark feedback ticketed with a ticket id or URL.")
	cmd.Flags().StringVar(&dismiss, "dismiss", "", "Mark feedback dismissed with a reason.")
	return cmd
}

func renderFeedbackItems(w io.Writer, items []*feedback.Item) error {
	if len(items) == 0 {
		_, err := fmt.Fprintln(w, "(no feedback)")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tCATEGORY\tTS\tINSTANCE\tJOB\tTICKET\tBODY")
	for _, item := range items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			item.ID,
			item.Status,
			item.Category,
			formatFeedbackTime(item.TS),
			emptyDash(item.Context.Instance),
			emptyDash(item.Context.Job),
			emptyDash(item.Context.Ticket),
			truncateFeedbackBody(item.Body, 96),
		)
	}
	return tw.Flush()
}

func renderFeedbackGroups(w io.Writer, groups []feedback.Group) error {
	if len(groups) == 0 {
		_, err := fmt.Fprintln(w, "(no feedback groups)")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "FINGERPRINT\tCOUNT\tFIRST\tLAST\tCATEGORY\tBODY")
	for _, group := range groups {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\n",
			shortFeedbackFingerprint(group.Fingerprint),
			group.Count,
			formatFeedbackTime(group.FirstSeen),
			formatFeedbackTime(group.LastSeen),
			group.Category,
			truncateFeedbackBody(group.Body, 96),
		)
	}
	return tw.Flush()
}

func renderFeedbackDetail(w io.Writer, item *feedback.Item) {
	fmt.Fprintf(w, "ID:          %s\n", item.ID)
	fmt.Fprintf(w, "Status:      %s\n", item.Status)
	fmt.Fprintf(w, "Category:    %s\n", item.Category)
	fmt.Fprintf(w, "TS:          %s\n", formatFeedbackTime(item.TS))
	fmt.Fprintf(w, "Fingerprint: %s\n", item.Fingerprint)
	fmt.Fprintf(w, "Body:        %s\n", item.Body)
	if item.Origin != nil && !item.Origin.Clean().Empty() {
		fmt.Fprintf(w, "Origin:      %s\n", origin.HeaderValue(*item.Origin))
	}
	renderFeedbackContext(w, item.Context)
	if item.Resolution != nil {
		fmt.Fprintln(w, "Resolution:")
		if item.Resolution.Ticket != "" {
			fmt.Fprintf(w, "  ticket: %s\n", item.Resolution.Ticket)
		}
		if item.Resolution.Reason != "" {
			fmt.Fprintf(w, "  reason: %s\n", item.Resolution.Reason)
		}
		fmt.Fprintf(w, "  by:     %s\n", item.Resolution.By)
		fmt.Fprintf(w, "  ts:     %s\n", formatFeedbackTime(item.Resolution.TS))
	}
}

func renderFeedbackContext(w io.Writer, ctx feedback.Context) {
	if ctx.Instance == "" && ctx.Agent == "" && ctx.Job == "" && ctx.Ticket == "" &&
		ctx.Pipeline == "" && ctx.Step == "" && ctx.Runtime == "" && ctx.Build == "" {
		return
	}
	fmt.Fprintln(w, "Context:")
	if ctx.Instance != "" {
		fmt.Fprintf(w, "  instance: %s\n", ctx.Instance)
	}
	if ctx.Agent != "" {
		fmt.Fprintf(w, "  agent:    %s\n", ctx.Agent)
	}
	if ctx.Job != "" {
		fmt.Fprintf(w, "  job:      %s\n", ctx.Job)
	}
	if ctx.Ticket != "" {
		fmt.Fprintf(w, "  ticket:   %s\n", ctx.Ticket)
	}
	if ctx.Pipeline != "" {
		fmt.Fprintf(w, "  pipeline: %s\n", ctx.Pipeline)
	}
	if ctx.Step != "" {
		fmt.Fprintf(w, "  step:     %s\n", ctx.Step)
	}
	if ctx.Runtime != "" {
		fmt.Fprintf(w, "  runtime:  %s\n", ctx.Runtime)
	}
	if ctx.Build != "" {
		fmt.Fprintf(w, "  build:    %s\n", ctx.Build)
	}
}

func formatFeedbackTime(ts time.Time) string {
	if ts.IsZero() {
		return "-"
	}
	return ts.UTC().Format(time.RFC3339)
}

func shortFeedbackFingerprint(fp string) string {
	fp = strings.TrimSpace(fp)
	if len(fp) <= 12 {
		return fp
	}
	return fp[:12]
}

func truncateFeedbackBody(body string, limit int) string {
	body = strings.Join(strings.Fields(body), " ")
	if limit <= 0 || len(body) <= limit {
		return body
	}
	if limit <= 3 {
		return body[:limit]
	}
	return body[:limit-3] + "..."
}
