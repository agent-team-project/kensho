package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jamesaud/agent-team/internal/budget"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newBudgetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "budget",
		Short: "Inspect per-team resource budgets.",
		Long:  "Inspect per-team resource budgets declared in `.agent_team/instances.toml` under `[budgets.<team>]`.",
	}
	cmd.AddCommand(newBudgetStatusCmd())
	return cmd
}

func newBudgetStatusCmd() *cobra.Command {
	var (
		target  string
		team    string
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show spend and in-flight counts for configured team budgets.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			top, err := topology.LoadFromTeamDir(teamDir)
			if err != nil {
				return err
			}
			rows, err := budget.Statuses(teamDir, top, time.Now().UTC())
			if err != nil {
				return err
			}
			rows = filterBudgetRows(rows, team)
			if rows == nil {
				rows = []budget.TeamStatus{}
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(rows)
			}
			renderBudgetStatus(cmd.OutOrStdout(), rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&team, "team", "", "Show only one team budget.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit budget status rows as JSON.")
	return cmd
}

func filterBudgetRows(rows []budget.TeamStatus, team string) []budget.TeamStatus {
	team = strings.TrimSpace(team)
	if team == "" {
		return rows
	}
	out := make([]budget.TeamStatus, 0, 1)
	for _, row := range rows {
		if row.Team == team {
			out = append(out, row)
		}
	}
	return out
}

func renderBudgetStatus(w io.Writer, rows []budget.TeamStatus) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no budgets configured)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TEAM\tTOKENS\tTOKEN_CAP\tTOKEN_RESET\tJOBS\tJOB_CAP")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%d\t%s\n",
			row.Team,
			row.TokensUsed,
			formatBudgetCap(row.TokensPerDay),
			formatBudgetTime(row.TokenAvailableAt),
			row.JobsInFlight,
			formatBudgetCap(int64(row.JobsInFlightCap)))
	}
	_ = tw.Flush()
}

func formatBudgetCap(cap int64) string {
	if cap <= 0 {
		return "-"
	}
	return fmt.Sprint(cap)
}

func formatBudgetTime(ts time.Time) string {
	if ts.IsZero() {
		return "-"
	}
	return ts.UTC().Format(time.RFC3339)
}
