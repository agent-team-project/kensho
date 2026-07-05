package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/agent-team-project/agent-team/internal/allowance"
	"github.com/agent-team-project/agent-team/internal/budget"
	"github.com/agent-team-project/agent-team/internal/daemon"
	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/topology"
	"github.com/agent-team-project/agent-team/internal/usage"
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
		jobID   string
		self    bool
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
			if self {
				if strings.TrimSpace(jobID) != "" {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team budget status: --self cannot be combined with --job.")
					return exitErr(2)
				}
				jobID = strings.TrimSpace(os.Getenv("AGENT_TEAM_JOB_ID"))
				if jobID == "" {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team budget status: --self requires AGENT_TEAM_JOB_ID.")
					return exitErr(2)
				}
				if root := strings.TrimSpace(os.Getenv("AGENT_TEAM_ROOT")); root != "" && !cmd.Flags().Changed("target") {
					teamDir = root
				}
			}
			if strings.TrimSpace(jobID) != "" {
				row, err := buildJobBudgetStatus(teamDir, jobID, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team budget status: %v\n", err)
					return exitErr(1)
				}
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(row)
				}
				renderJobBudgetStatus(cmd.OutOrStdout(), row)
				return nil
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
	cmd.Flags().StringVar(&jobID, "job", "", "Show one job's soft allowance status.")
	cmd.Flags().BoolVar(&self, "self", false, "Show the current runtime job's soft allowance status from AGENT_TEAM_JOB_ID.")
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
	fmt.Fprintln(tw, "TEAM\tTOKENS\tALLOCATED\tTOKEN_CAP\tTOKEN_RESET\tJOBS\tJOB_CAP")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%s\t%d\t%s\n",
			row.Team,
			row.TokensUsed,
			row.TokensAllocated,
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

type jobBudgetStatus struct {
	JobID             string `json:"job_id"`
	Ticket            string `json:"ticket,omitempty"`
	Instance          string `json:"instance,omitempty"`
	Step              string `json:"step,omitempty"`
	Runtime           string `json:"runtime,omitempty"`
	TokenBudget       int64  `json:"token_budget,omitempty"`
	TokensUsed        int64  `json:"tokens_used,omitempty"`
	TokensRemaining   int64  `json:"tokens_remaining,omitempty"`
	TokensAvailable   bool   `json:"tokens_available"`
	TimeBudget        string `json:"time_budget,omitempty"`
	TimeElapsed       string `json:"time_elapsed,omitempty"`
	TimeRemaining     string `json:"time_remaining,omitempty"`
	ReminderLevels    []int  `json:"reminder_levels,omitempty"`
	TokenNoticeLevels []int  `json:"token_notice_levels,omitempty"`
	TimeNoticeLevels  []int  `json:"time_notice_levels,omitempty"`
}

func buildJobBudgetStatus(teamDir, rawID string, now time.Time) (jobBudgetStatus, error) {
	j, err := jobstore.ReadLiveOrArchive(teamDir, rawID)
	if err != nil {
		return jobBudgetStatus{}, err
	}
	row := jobBudgetStatus{JobID: j.ID, Ticket: j.Ticket}
	meta := runningMetadataForJob(teamDir, j.ID)
	target := jobBudgetTargetForStatus(j, meta)
	row.Instance = target.instance
	row.Step = target.stepID
	row.Runtime = target.runtime
	row.TokenBudget = target.tokenBudget
	row.TimeBudget = target.timeBudget.String()
	if target.timeBudget <= 0 {
		row.TimeBudget = ""
	}
	row.ReminderLevels, _ = allowance.NormalizeReminderLevels(target.reminderLevels)
	row.TokenNoticeLevels = append([]int(nil), target.tokenNotices...)
	row.TimeNoticeLevels = append([]int(nil), target.timeNotices...)
	if meta != nil {
		rec := liveJobBudgetUsage(*meta, now)
		row.TokensAvailable = rec.TokensAvailable
		if rec.TokensAvailable {
			row.TokensUsed = rec.InputTokens + rec.OutputTokens
		}
		if !meta.StartedAt.IsZero() {
			elapsed := now.Sub(meta.StartedAt.UTC())
			if elapsed < 0 {
				elapsed = 0
			}
			row.TimeElapsed = elapsed.String()
		}
	} else if j.Usage != nil {
		row.TokensAvailable = j.Usage.Summary.TokenAvailableRuns > 0
		row.TokensUsed = j.Usage.Summary.InputTokens + j.Usage.Summary.OutputTokens
		if j.Usage.Summary.DurationMS > 0 {
			row.TimeElapsed = (time.Duration(j.Usage.Summary.DurationMS) * time.Millisecond).String()
		}
	}
	if row.TokenBudget > 0 && row.TokensAvailable && row.TokensUsed < row.TokenBudget {
		row.TokensRemaining = row.TokenBudget - row.TokensUsed
	}
	if target.timeBudget > 0 && row.TimeElapsed != "" {
		if elapsed, err := time.ParseDuration(row.TimeElapsed); err == nil && elapsed < target.timeBudget {
			row.TimeRemaining = (target.timeBudget - elapsed).String()
		}
	}
	return row, nil
}

type jobBudgetStatusTarget struct {
	instance       string
	stepID         string
	runtime        string
	tokenBudget    int64
	timeBudget     time.Duration
	reminderLevels []int
	tokenNotices   []int
	timeNotices    []int
}

func jobBudgetTargetForStatus(j *jobstore.Job, meta *daemon.Metadata) jobBudgetStatusTarget {
	if j == nil {
		return jobBudgetStatusTarget{}
	}
	instance := ""
	runtime := ""
	if meta != nil {
		instance = meta.Instance
		runtime = meta.Runtime
	}
	for i := range j.Steps {
		step := &j.Steps[i]
		if instance == "" || strings.TrimSpace(step.Instance) != instance {
			continue
		}
		return jobBudgetStatusTarget{
			instance:       instance,
			stepID:         step.ID,
			runtime:        runtime,
			tokenBudget:    step.TokenBudget,
			timeBudget:     parseBudgetDuration(step.TimeBudget),
			reminderLevels: step.ReminderLevels,
			tokenNotices:   step.TokenBudgetNotices,
			timeNotices:    step.TimeBudgetNotices,
		}
	}
	return jobBudgetStatusTarget{
		instance:       strings.TrimSpace(j.Instance),
		runtime:        runtime,
		tokenBudget:    j.TokenBudget,
		timeBudget:     parseBudgetDuration(j.TimeBudget),
		reminderLevels: j.ReminderLevels,
		tokenNotices:   j.TokenBudgetNotices,
		timeNotices:    j.TimeBudgetNotices,
	}
}

func runningMetadataForJob(teamDir, jobID string) *daemon.Metadata {
	rows, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil
	}
	for _, meta := range rows {
		if meta == nil || meta.Status != daemon.StatusRunning {
			continue
		}
		if jobstore.NormalizeID(meta.Job) == jobstore.NormalizeID(jobID) {
			return meta
		}
	}
	return nil
}

func liveJobBudgetUsage(meta daemon.Metadata, now time.Time) usage.Record {
	rec := usage.Record{
		Instance:   meta.Instance,
		Agent:      meta.Agent,
		Runtime:    meta.Runtime,
		StartedAt:  meta.StartedAt.UTC(),
		EndedAt:    now.UTC(),
		CapturedAt: now.UTC(),
		Source:     meta.LogPath,
		Origin:     meta.Origin,
	}
	if !rec.StartedAt.IsZero() && !rec.EndedAt.Before(rec.StartedAt) {
		rec.DurationMS = rec.EndedAt.Sub(rec.StartedAt).Milliseconds()
	}
	if !strings.EqualFold(meta.Runtime, "codex") {
		return rec
	}
	f, err := os.Open(meta.LogPath)
	if err != nil {
		return rec
	}
	defer f.Close()
	_ = usage.ParseCodexJSONL(&rec, f)
	return rec
}

func parseBudgetDuration(raw string) time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

func renderJobBudgetStatus(w io.Writer, row jobBudgetStatus) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB\tSTEP\tINSTANCE\tTOKENS\tTOKEN_BUDGET\tTOKEN_REMAINING\tTIME\tTIME_BUDGET\tTIME_REMAINING\tNOTICES")
	fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
		row.JobID,
		emptyDash(row.Step),
		emptyDash(row.Instance),
		formatJobBudgetTokens(row),
		formatBudgetCap(row.TokenBudget),
		formatBudgetCap(row.TokensRemaining),
		emptyDash(row.TimeElapsed),
		emptyDash(row.TimeBudget),
		emptyDash(row.TimeRemaining),
		formatNoticeLevels(row))
	_ = tw.Flush()
}

func formatJobBudgetTokens(row jobBudgetStatus) string {
	if !row.TokensAvailable {
		return "-"
	}
	return fmt.Sprint(row.TokensUsed)
}

func formatNoticeLevels(row jobBudgetStatus) string {
	parts := []string{}
	if len(row.TokenNoticeLevels) > 0 {
		parts = append(parts, "tokens="+formatIntList(row.TokenNoticeLevels))
	}
	if len(row.TimeNoticeLevels) > 0 {
		parts = append(parts, "time="+formatIntList(row.TimeNoticeLevels))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}

func formatIntList(values []int) string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		items = append(items, fmt.Sprintf("%d%%", value))
	}
	return strings.Join(items, "/")
}
