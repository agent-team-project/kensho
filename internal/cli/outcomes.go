package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
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
		byEpic  bool
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
				Since:   sinceAt,
				Team:    strings.TrimSpace(team),
				Agent:   strings.TrimSpace(agent),
				ByEpic:  byEpic,
				TeamDir: teamDir,
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
	cmd.Flags().BoolVar(&byEpic, "by-epic", false, "Aggregate outcome trends by epic/project attribution instead of week, team, and agent.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the outcome report as JSON.")
	return cmd
}

func renderOutcomesReport(w io.Writer, report outcomes.Report) {
	if len(report.Rows) == 0 {
		fmt.Fprintln(w, "(no outcome records)")
		return
	}
	if report.ByEpic {
		renderOutcomesByEpicReport(w, report)
	} else {
		renderOutcomesTrendReport(w, report)
	}
	renderOutcomesModelTierReport(w, report)
	renderOutcomesBounceClassReport(w, report)
}

func renderOutcomesTrendReport(w io.Writer, report outcomes.Report) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "WEEK\tTEAM\tAGENT\tJOBS\tDONE\tFAILED\tEFF_CONC\tPEAK_CONC\tCAPACITY\tMODEL_TIER\tBOUNCE_CLASS\tBOUNCES\tAVG_BOUNCE\tREVIEWS\tAVG_REVIEW\tTOKENS\tAVG_TTM\tWATCHDOG\tBUDGET")
	for _, row := range report.Rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%d\t%s\t%s\t%s\t%d\t%d\n",
			emptyDash(row.Week),
			emptyDash(row.Team),
			emptyDash(row.Agent),
			row.Jobs,
			row.Done,
			row.Failed,
			formatOutcomeFloat(row.EffectiveConcurrency),
			formatOutcomeInt(row.PeakConcurrentWorkUnits),
			formatOutcomeInt(row.DeclaredReplicaCapacity),
			formatOutcomeCountMap(row.ModelTiers, formatOutcomeModelTierKey),
			formatOutcomeCountMap(row.BounceClasses, formatOutcomePlainKey),
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
	fmt.Fprintf(tw, "TOTAL\t-\t-\t%d\t%d\t%d\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%d\t%s\t%s\t%s\t%d\t%d\n",
		summary.Jobs,
		summary.Done,
		summary.Failed,
		formatOutcomeFloat(summary.EffectiveConcurrency),
		formatOutcomeInt(summary.PeakConcurrentWorkUnits),
		formatOutcomeInt(summary.DeclaredReplicaCapacity),
		formatOutcomeCountMap(summary.ModelTiers, formatOutcomeModelTierKey),
		formatOutcomeCountMap(summary.BounceClasses, formatOutcomePlainKey),
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

func renderOutcomesByEpicReport(w io.Writer, report outcomes.Report) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "EPIC\tJOBS\tDONE\tFAILED\tBOUNCES\tAVG_BOUNCE\tREVIEWS\tAVG_REVIEW\tTOKENS\tEPIC_ALLOC\tAVG_TTM\tWATCHDOG\tBUDGET")
	for _, row := range report.Rows {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%s\t%d\t%s\t%s\t%s\t%s\t%d\t%d\n",
			emptyDash(row.Epic),
			row.Jobs,
			row.Done,
			row.Failed,
			row.Bounces,
			formatOutcomeFloat(row.AverageBounces),
			row.ReviewRounds,
			formatOutcomeFloat(row.AverageReviewRounds),
			formatOutcomeTokenRatio(row.TokensConsumed, row.TokenBudget),
			formatOutcomeTokenRatio(row.TokensConsumed, row.EpicAllocation),
			formatOutcomeDuration(row.AverageTimeToMergeMS),
			row.WatchdogEvents,
			row.BudgetExceededEvents)
	}
	summary := report.Summary
	fmt.Fprintf(tw, "TOTAL\t%d\t%d\t%d\t%d\t%s\t%d\t%s\t%s\t%s\t%s\t%d\t%d\n",
		summary.Jobs,
		summary.Done,
		summary.Failed,
		summary.Bounces,
		formatOutcomeFloat(summary.AverageBounces),
		summary.ReviewRounds,
		formatOutcomeFloat(summary.AverageReviewRounds),
		formatOutcomeTokenRatio(summary.TokensConsumed, summary.TokenBudget),
		formatOutcomeTokenRatio(summary.TokensConsumed, summary.EpicAllocation),
		formatOutcomeDuration(summary.AverageTimeToMergeMS),
		summary.WatchdogEvents,
		summary.BudgetExceededEvents)
	_ = tw.Flush()
}

func renderOutcomesModelTierReport(w io.Writer, report outcomes.Report) {
	if len(report.ModelTierRows) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "MODEL/TIER")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if report.ByEpic {
		fmt.Fprintln(tw, "EPIC\tRUNTIME\tMODEL\tTIER\tJOBS\tDONE\tFAILED\tBOUNCES\tAVG_BOUNCE\tREVIEWS\tAVG_REVIEW\tTOKENS")
		for _, row := range report.ModelTierRows {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%s\t%d\t%s\t%s\n",
				emptyDash(row.Epic),
				emptyDash(row.Runtime),
				emptyDash(row.Model),
				emptyDash(row.Tier),
				row.Jobs,
				row.Done,
				row.Failed,
				row.Bounces,
				formatOutcomeFloat(row.AverageBounces),
				row.ReviewRounds,
				formatOutcomeFloat(row.AverageReviewRounds),
				formatOutcomeTokenRatio(row.TokensConsumed, row.TokenBudget))
		}
	} else {
		fmt.Fprintln(tw, "WEEK\tTEAM\tAGENT\tRUNTIME\tMODEL\tTIER\tJOBS\tDONE\tFAILED\tBOUNCES\tAVG_BOUNCE\tREVIEWS\tAVG_REVIEW\tTOKENS")
		for _, row := range report.ModelTierRows {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%s\t%d\t%s\t%s\n",
				emptyDash(row.Week),
				emptyDash(row.Team),
				emptyDash(row.Agent),
				emptyDash(row.Runtime),
				emptyDash(row.Model),
				emptyDash(row.Tier),
				row.Jobs,
				row.Done,
				row.Failed,
				row.Bounces,
				formatOutcomeFloat(row.AverageBounces),
				row.ReviewRounds,
				formatOutcomeFloat(row.AverageReviewRounds),
				formatOutcomeTokenRatio(row.TokensConsumed, row.TokenBudget))
		}
	}
	_ = tw.Flush()
}

func renderOutcomesBounceClassReport(w io.Writer, report outcomes.Report) {
	if len(report.BounceClassRows) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "BOUNCE_CLASS")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if report.ByEpic {
		fmt.Fprintln(tw, "EPIC\tCLASS\tJOBS\tDONE\tFAILED\tBOUNCES\tAVG_BOUNCE")
		for _, row := range report.BounceClassRows {
			fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\t%s\n",
				emptyDash(row.Epic),
				emptyDash(row.Class),
				row.Jobs,
				row.Done,
				row.Failed,
				row.Bounces,
				formatOutcomeFloat(row.AverageBounces))
		}
	} else {
		fmt.Fprintln(tw, "WEEK\tTEAM\tAGENT\tCLASS\tJOBS\tDONE\tFAILED\tBOUNCES\tAVG_BOUNCE")
		for _, row := range report.BounceClassRows {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%s\n",
				emptyDash(row.Week),
				emptyDash(row.Team),
				emptyDash(row.Agent),
				emptyDash(row.Class),
				row.Jobs,
				row.Done,
				row.Failed,
				row.Bounces,
				formatOutcomeFloat(row.AverageBounces))
		}
	}
	_ = tw.Flush()
}

func formatOutcomeFloat(v float64) string {
	if v == 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f", v)
}

func formatOutcomeInt(v int) string {
	if v == 0 {
		return "-"
	}
	return fmt.Sprintf("%d", v)
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

func formatOutcomeCountMap(values map[string]int, label func(string) string) string {
	if len(values) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(values))
	for key, count := range values {
		if count > 0 {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return "-"
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", label(key), values[key]))
	}
	return strings.Join(parts, ",")
}

func formatOutcomePlainKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return "-"
	}
	return key
}

func formatOutcomeModelTierKey(key string) string {
	parts := strings.Split(key, "\x00")
	for len(parts) < 3 {
		parts = append(parts, "")
	}
	runtime := strings.TrimSpace(parts[0])
	model := strings.TrimSpace(parts[1])
	tier := strings.TrimSpace(parts[2])
	switch {
	case runtime != "" && model != "" && tier != "":
		if runtime == model {
			return runtime + "/" + tier
		}
		return runtime + ":" + model + "/" + tier
	case model != "" && tier != "":
		return model + "/" + tier
	case runtime != "" && tier != "":
		return runtime + "/" + tier
	case tier != "":
		return tier
	case model != "":
		return model
	case runtime != "":
		return runtime
	default:
		return "-"
	}
}
