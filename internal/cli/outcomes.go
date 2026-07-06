package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/agent-team-project/agent-team/internal/outcomes"
	"github.com/spf13/cobra"
)

func newOutcomesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "outcomes",
		Short: "Inspect terminal job outcome trends.",
		Long:  "Inspect terminal job outcome records captured at durable job finalization.",
	}
	cmd.AddCommand(newOutcomesReportCmd())
	return cmd
}

func newOutcomesReportCmd() *cobra.Command {
	var (
		target  string
		since   string
		team    string
		agent   string
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Render outcome trends by week, team, and agent.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var sinceAt time.Time
			if strings.TrimSpace(since) != "" {
				ts, err := parseUsageSince(since, time.Now)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outcomes report: %v\n", err)
					return exitErr(2)
				}
				sinceAt = ts
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			records, err := outcomes.LoadRecords(teamDir)
			if err != nil {
				return err
			}
			report := outcomes.BuildReport(records, outcomes.ReportOptions{
				Since: sinceAt,
				Team:  strings.TrimSpace(team),
				Agent: strings.TrimSpace(agent),
			})
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(report)
			}
			renderOutcomesReport(cmd.OutOrStdout(), report)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&since, "since", "", "Only include outcomes finalized since a duration ago (for example 7d, 24h) or an RFC3339 timestamp.")
	cmd.Flags().StringVar(&team, "team", "", "Only include outcomes for one team.")
	cmd.Flags().StringVar(&agent, "agent", "", "Only include outcomes for one agent type.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the outcome report as JSON.")
	return cmd
}

func renderOutcomesReport(w io.Writer, report outcomes.Report) {
	if len(report.Rows) == 0 {
		fmt.Fprintln(w, "(no outcome records)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "WEEK\tTEAM\tAGENT\tJOBS\tDONE\tFAILED\tBOUNCES\tAVG_BOUNCE\tREVIEWS\tAVG_REVIEW\tTOKENS\tAVG_TTM\tWATCHDOG\tBUDGET")
	for _, row := range report.Rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%d\t%s\t%d\t%s\t%s\t%s\t%d\t%d\n",
			emptyDash(row.Week),
			emptyDash(row.Team),
			emptyDash(row.Agent),
			row.Jobs,
			row.Done,
			row.Failed,
			row.Bounces,
			formatOutcomeFloat(row.AverageBounces),
			row.ReviewRounds,
			formatOutcomeFloat(row.AverageReviewRounds),
			formatOutcomeTokenRatio(row.TokensConsumed, row.TokenBudget),
			formatOutcomeDuration(row.AverageTimeToMergeMS),
			row.WatchdogEvents,
			row.BudgetExceededEvents)
	}
	summary := report.Summary
	fmt.Fprintf(tw, "TOTAL\t-\t-\t%d\t%d\t%d\t%d\t%s\t%d\t%s\t%s\t%s\t%d\t%d\n",
		summary.Jobs,
		summary.Done,
		summary.Failed,
		summary.Bounces,
		formatOutcomeFloat(summary.AverageBounces),
		summary.ReviewRounds,
		formatOutcomeFloat(summary.AverageReviewRounds),
		formatOutcomeTokenRatio(summary.TokensConsumed, summary.TokenBudget),
		formatOutcomeDuration(summary.AverageTimeToMergeMS),
		summary.WatchdogEvents,
		summary.BudgetExceededEvents)
	_ = tw.Flush()
}

func formatOutcomeFloat(v float64) string {
	if v == 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f", v)
}

func formatOutcomeTokenRatio(consumed, budget int64) string {
	if consumed == 0 && budget == 0 {
		return "-"
	}
	if budget == 0 {
		return fmt.Sprintf("%d/-", consumed)
	}
	return fmt.Sprintf("%d/%d", consumed, budget)
}

func formatOutcomeDuration(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	return (time.Duration(ms) * time.Millisecond).String()
}
