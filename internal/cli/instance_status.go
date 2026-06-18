package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"
)

// statusFile mirrors the schema documented in
// `documentation/orchestrator.md` § "Instance status / observability".
//
// Only the fields the reader needs for rendering / staleness logic are
// declared. `omitempty` lets the encoder skip absent sections.
type statusFile struct {
	Status   statusSection    `toml:"status"`
	Work     *workSection     `toml:"work,omitempty"`
	Blocking *blockingSection `toml:"blocking,omitempty"`
}

type statusSection struct {
	Phase       string `toml:"phase"`
	Description string `toml:"description"`
	Since       string `toml:"since"`
	LastAction  string `toml:"last_action"`
}

type workSection struct {
	Job    string `toml:"job"`
	Ticket string `toml:"ticket"`
	PR     string `toml:"pr"`
	Branch string `toml:"branch"`
}

type blockingSection struct {
	Reason string `toml:"reason"`
	AskTo  string `toml:"ask_to"`
}

// instanceRow is what the reader hands the table renderer. Every instance
// with a state dir produces one row, even those without a status.toml — the
// design says we render a placeholder rather than hiding them.
type instanceRow struct {
	Instance  string
	Agent     string // best-effort guess; "—" if unknown
	Phase     string
	Age       string
	Summary   string
	Stale     bool
	HasFile   bool   // false → row was inferred from the empty state dir
	Lifecycle string // daemon-reported (running/stopped/exited/crashed); empty when no daemon
	Job       string
	Ticket    string
	Branch    string
	PR        string
	Workspace string
	PID       int // daemon-reported; 0 when no daemon
	StartedAt time.Time
	StoppedAt time.Time
	ExitedAt  time.Time
}

// staleAfter is the threshold past which a non-idle/non-done instance is
// flagged `(stale)` in the ps output. Documented in orchestrator.md.
const staleAfter = 10 * time.Minute

// loadInstanceRows walks <teamDir>/state/*/ and produces one row per
// directory. Reading-side errors on individual instances are degraded into
// placeholder rows (with the parse error in the SUMMARY column) rather than
// failing the whole `ps` — one corrupt status.toml shouldn't blind the
// operator to every other instance.
func loadInstanceRows(teamDir string, agentNames map[string]bool, now time.Time) []instanceRow {
	stateRoot := filepath.Join(teamDir, "state")
	entries, err := os.ReadDir(stateRoot)
	if err != nil {
		return nil
	}
	var rows []instanceRow
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rows = append(rows, instanceRowFor(stateRoot, e.Name(), agentNames, now))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Instance < rows[j].Instance })
	return rows
}

func statusPhaseByInstance(teamDir string, now time.Time) map[string]string {
	rows := loadInstanceRows(teamDir, loadAgentNames(teamDir), now)
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Instance] = psPhaseKey(row)
	}
	return out
}

func instanceRowFor(stateRoot, instance string, agentNames map[string]bool, now time.Time) instanceRow {
	row := instanceRow{
		Instance: instance,
		Agent:    guessAgentName(instance, agentNames),
		Phase:    "—",
		Age:      "—",
		Summary:  "",
	}

	statusPath := filepath.Join(stateRoot, instance, "status.toml")
	st, err := os.Stat(statusPath)
	if err != nil {
		// No status.toml — fine, this is an instance that hasn't emitted
		// (yet). Leave placeholders.
		return row
	}
	row.HasFile = true

	var sf statusFile
	if _, err := toml.DecodeFile(statusPath, &sf); err != nil {
		row.Phase = "?"
		row.Summary = "parse error: " + err.Error()
		return row
	}

	row.Phase = sf.Status.Phase
	row.Age = formatAge(now.Sub(st.ModTime()))
	row.Summary = formatSummary(sf)
	if sf.Work != nil {
		row.Job = sf.Work.Job
		row.Ticket = sf.Work.Ticket
		row.Branch = sf.Work.Branch
		row.PR = sf.Work.PR
	}

	if sf.Status.Phase != "idle" && sf.Status.Phase != "done" {
		if now.Sub(st.ModTime()) > staleAfter {
			row.Stale = true
		}
	}
	return row
}

// guessAgentName best-effort: instance name == agent dir name (singleton
// case) or strip after the last `-` and try again (worker-squ-25 → worker).
// Walks back hyphen-by-hyphen; falls back to "—".
func guessAgentName(instance string, agentNames map[string]bool) string {
	if agentNames[instance] {
		return instance
	}
	parts := strings.Split(instance, "-")
	for i := len(parts); i > 0; i-- {
		candidate := strings.Join(parts[:i], "-")
		if agentNames[candidate] {
			return candidate
		}
	}
	return "—"
}

func formatSummary(sf statusFile) string {
	if sf.Blocking != nil && sf.Status.Phase == "blocked" {
		ask := sf.Blocking.AskTo
		if ask == "" {
			ask = "?"
		}
		reason := sf.Blocking.Reason
		if reason == "" {
			reason = sf.Status.Description
		}
		return fmt.Sprintf("asks %s: %s", ask, reason)
	}
	if sf.Status.Description != "" {
		return sf.Status.Description
	}
	return sf.Status.LastAction
}

// formatAge renders a duration as "8s" / "12m" / "3h" / "2d" — single
// largest unit, matching the docker-ps shorthand readers expect.
func formatAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func newInstancePsCmd() *cobra.Command {
	var target string
	cwd, _ := os.Getwd()
	c := &cobra.Command{
		Use:   "ps",
		Short: "List instances with their current status (Docker-ps style).",
		Long: "Walks .agent_team/state/*/status.toml and renders one row per " +
			"instance. Instances with a state dir but no status.toml render " +
			"with `—` placeholders so they remain visible. Non-idle/non-done " +
			"rows whose status.toml is older than 10 minutes are flagged " +
			"`(stale)`.",
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			return runInstancePs(cmd.OutOrStdout(), teamDir, time.Now())
		},
	}
	c.Flags().StringVar(&target, "target", cwd, "Repo root.")
	return c
}

func runInstancePs(w io.Writer, teamDir string, now time.Time) error {
	agentNames := loadAgentNames(teamDir)
	rows := loadInstanceRows(teamDir, agentNames, now)
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no instances)")
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "INSTANCE\tAGENT\tPHASE\tAGE\tSUMMARY")
	for _, r := range rows {
		phase := r.Phase
		if r.Stale {
			phase = phase + " (stale)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			r.Instance, r.Agent, phase, r.Age, r.Summary)
	}
	return tw.Flush()
}

// loadAgentNames returns the set of agent dir names under <teamDir>/agents/
// for guessAgentName to consult. Failures are non-fatal: an empty set just
// means every row's AGENT column shows `—`.
func loadAgentNames(teamDir string) map[string]bool {
	out := map[string]bool{}
	entries, err := os.ReadDir(filepath.Join(teamDir, "agents"))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			out[e.Name()] = true
		}
	}
	return out
}
