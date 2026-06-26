package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newInboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "inbox",
		Aliases: []string{"mailbox"},
		Short:   "Inspect and acknowledge daemon mailbox messages.",
		Long: "Inspect daemon mailbox messages stored under .agent_team/daemon. " +
			"The inbox commands read local files directly, so they work even when agent-teamd is not running.",
	}
	cmd.AddCommand(newInboxLsCmd())
	cmd.AddCommand(newInboxShowCmd())
	cmd.AddCommand(newInboxAckCmd())
	return cmd
}

func newInboxLsCmd() *cobra.Command {
	var (
		target     string
		teamName   string
		unreadOnly bool
		jsonOut    bool
		format     string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List inbox summaries by instance.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox ls: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseInboxFormat(format, "inbox-ls-format")
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team inbox ls: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			return runInboxLs(cmd.OutOrStdout(), cmd.ErrOrStderr(), teamDir, inboxListOptions{
				TeamName:   teamName,
				UnreadOnly: unreadOnly,
				JSON:       jsonOut,
				Format:     tmpl,
			})
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&teamName, "team", "", "Only list inboxes owned by this declared team.")
	cmd.Flags().BoolVar(&unreadOnly, "unread", false, "Show only inboxes with unread messages.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each inbox summary with a Go template, e.g. '{{.Instance}} {{.Unread}}'.")
	return cmd
}

func newInboxShowCmd() *cobra.Command {
	var (
		target     string
		unreadOnly bool
		tail       int
		jsonOut    bool
		format     string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <instance>",
		Short: "Show messages for one instance inbox.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox show: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if tail < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox show: --tail must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parseInboxFormat(format, "inbox-show-format")
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team inbox show: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			return runInboxShow(cmd.OutOrStdout(), cmd.ErrOrStderr(), teamDir, args[0], inboxShowOptions{
				UnreadOnly: unreadOnly,
				Tail:       tail,
				JSON:       jsonOut,
				Format:     tmpl,
			})
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&unreadOnly, "unread", false, "Show only messages after the inbox cursor.")
	cmd.Flags().IntVar(&tail, "tail", 0, "Show only the N most recent matching messages (0 = all).")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each message with a Go template, e.g. '{{.ID}} {{.Unread}} {{.Body}}'.")
	return cmd
}

func newInboxAckCmd() *cobra.Command {
	var (
		target  string
		all     bool
		dryRun  bool
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ack <instance> <message-id>|--all",
		Short: "Advance an instance inbox cursor.",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox ack: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if all && len(args) != 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox ack: --all cannot be combined with a message id.")
				return exitErr(2)
			}
			if !all && len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox ack: message id is required unless --all is set.")
				return exitErr(2)
			}
			tmpl, err := parseInboxFormat(format, "inbox-ack-format")
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team inbox ack: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			id := ""
			if !all {
				id = args[1]
			}
			return runInboxAck(cmd.OutOrStdout(), cmd.ErrOrStderr(), teamDir, args[0], inboxAckOptions{
				All:    all,
				ID:     id,
				DryRun: dryRun,
				JSON:   jsonOut,
				Format: tmpl,
			})
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Acknowledge every current message in the inbox.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the cursor update without writing it.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the ack result with a Go template, e.g. '{{.Instance}} {{.Acked}}'.")
	return cmd
}

type inboxListOptions struct {
	TeamName   string
	UnreadOnly bool
	JSON       bool
	Format     *template.Template
}

type inboxShowOptions struct {
	UnreadOnly bool
	Tail       int
	JSON       bool
	Format     *template.Template
}

type inboxAckOptions struct {
	All    bool
	ID     string
	DryRun bool
	JSON   bool
	Format *template.Template
}

type inboxSummaryRow struct {
	Instance    string    `json:"instance"`
	Agent       string    `json:"agent,omitempty"`
	Status      string    `json:"status,omitempty"`
	Total       int       `json:"total"`
	Unread      int       `json:"unread"`
	Cursor      string    `json:"cursor,omitempty"`
	LatestID    string    `json:"latest_id,omitempty"`
	LatestFrom  string    `json:"latest_from,omitempty"`
	LatestBody  string    `json:"latest_body,omitempty"`
	LatestTS    time.Time `json:"latest_ts,omitempty"`
	HasMetadata bool      `json:"has_metadata"`
	MailboxPath string    `json:"mailbox_path,omitempty"`
	CursorPath  string    `json:"cursor_path,omitempty"`
}

type inboxMessageRow struct {
	Instance string    `json:"instance"`
	ID       string    `json:"id"`
	From     string    `json:"from"`
	To       string    `json:"to"`
	Body     string    `json:"body"`
	TS       time.Time `json:"ts"`
	Unread   bool      `json:"unread"`
}

type inboxAckResult struct {
	Instance      string `json:"instance"`
	ID            string `json:"id,omitempty"`
	All           bool   `json:"all,omitempty"`
	DryRun        bool   `json:"dry_run,omitempty"`
	Total         int    `json:"total"`
	Acked         int    `json:"acked"`
	UnreadBefore  int    `json:"unread_before"`
	UnreadAfter   int    `json:"unread_after"`
	CursorBefore  string `json:"cursor_before,omitempty"`
	CursorAfter   string `json:"cursor_after,omitempty"`
	CursorChanged bool   `json:"cursor_changed"`
}

func runInboxLs(stdout, stderr io.Writer, teamDir string, opts inboxListOptions) error {
	daemonRoot := daemon.DaemonRoot(teamDir)
	instances, metaByInstance, err := listInboxInstances(daemonRoot)
	if err != nil {
		return err
	}
	if strings.TrimSpace(opts.TeamName) != "" {
		top, team, err := loadTopologyTeam(teamDir, opts.TeamName)
		if err != nil {
			return err
		}
		instances = filterInboxInstancesForTeam(top, team, instances, metaByInstance)
	}
	rows, err := collectInboxSummaryRows(daemonRoot, instances, metaByInstance, opts.UnreadOnly)
	if err != nil {
		return err
	}
	if opts.JSON {
		return json.NewEncoder(stdout).Encode(rows)
	}
	if opts.Format != nil {
		return renderInboxFormat(stdout, rows, opts.Format)
	}
	if len(rows) == 0 {
		if opts.UnreadOnly {
			fmt.Fprintln(stdout, "(no unread inboxes)")
		} else {
			fmt.Fprintln(stdout, "(no inboxes)")
		}
		return nil
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "INSTANCE\tAGENT\tSTATUS\tTOTAL\tUNREAD\tLATEST\tAGE\tFROM\tMESSAGE")
	now := time.Now()
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%s\t%s\t%s\t%s\n",
			row.Instance,
			emptyDash(row.Agent),
			emptyDash(row.Status),
			row.Total,
			row.Unread,
			emptyDash(row.LatestID),
			inboxAge(now, row.LatestTS),
			emptyDash(row.LatestFrom),
			compactInboxBody(row.LatestBody, 72),
		)
	}
	return tw.Flush()
}

func collectInboxSummaryRows(daemonRoot string, instances []string, metaByInstance map[string]*daemon.Metadata, unreadOnly bool) ([]inboxSummaryRow, error) {
	rows := make([]inboxSummaryRow, 0, len(instances))
	for _, instance := range instances {
		messages, err := daemon.ReadMessages(daemonRoot, instance)
		if err != nil {
			return nil, fmt.Errorf("read inbox %s: %w", instance, err)
		}
		cursor, err := daemon.ReadCursor(daemonRoot, instance)
		if err != nil {
			return nil, fmt.Errorf("read inbox cursor %s: %w", instance, err)
		}
		row := inboxSummaryRow{
			Instance:    instance,
			Total:       len(messages),
			Unread:      inboxUnreadCount(messages, cursor),
			Cursor:      cursor,
			HasMetadata: metaByInstance[instance] != nil,
			MailboxPath: daemon.MailboxPath(daemonRoot, instance),
			CursorPath:  daemon.MailboxCursorPath(daemonRoot, instance),
		}
		if meta := metaByInstance[instance]; meta != nil {
			row.Agent = meta.Agent
			row.Status = string(meta.Status)
		}
		if len(messages) > 0 {
			latest := messages[len(messages)-1]
			row.LatestID = latest.ID
			row.LatestFrom = latest.From
			row.LatestBody = latest.Body
			row.LatestTS = latest.TS
		}
		if unreadOnly && row.Unread == 0 {
			continue
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func runInboxShow(stdout, stderr io.Writer, teamDir, instance string, opts inboxShowOptions) error {
	instance = strings.TrimSpace(instance)
	if instance == "" {
		fmt.Fprintln(stderr, "agent-team inbox show: instance is required.")
		return exitErr(2)
	}
	daemonRoot := daemon.DaemonRoot(teamDir)
	exists, err := inboxInstanceExists(daemonRoot, instance)
	if err != nil {
		return err
	}
	if !exists {
		fmt.Fprintf(stderr, "agent-team inbox show: no such inbox: %s\n", instance)
		return exitErr(2)
	}
	messages, err := daemon.ReadMessages(daemonRoot, instance)
	if err != nil {
		return err
	}
	cursor, err := daemon.ReadCursor(daemonRoot, instance)
	if err != nil {
		return err
	}
	rows := inboxMessageRows(instance, messages, cursor, opts.UnreadOnly)
	if opts.Tail > 0 && len(rows) > opts.Tail {
		rows = rows[len(rows)-opts.Tail:]
	}
	if opts.JSON {
		return json.NewEncoder(stdout).Encode(rows)
	}
	if opts.Format != nil {
		return renderInboxFormat(stdout, rows, opts.Format)
	}
	if len(rows) == 0 {
		if opts.UnreadOnly {
			fmt.Fprintln(stdout, "(no unread messages)")
		} else {
			fmt.Fprintln(stdout, "(no messages)")
		}
		return nil
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tUNREAD\tAGE\tFROM\tMESSAGE")
	now := time.Now()
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%t\t%s\t%s\t%s\n",
			row.ID,
			row.Unread,
			inboxAge(now, row.TS),
			emptyDash(row.From),
			compactInboxBody(row.Body, 96),
		)
	}
	return tw.Flush()
}

func runInboxAck(stdout, stderr io.Writer, teamDir, instance string, opts inboxAckOptions) error {
	instance = strings.TrimSpace(instance)
	if instance == "" {
		fmt.Fprintln(stderr, "agent-team inbox ack: instance is required.")
		return exitErr(2)
	}
	opts.ID = strings.TrimSpace(opts.ID)
	if !opts.All && opts.ID == "" {
		fmt.Fprintln(stderr, "agent-team inbox ack: message id is required unless --all is set.")
		return exitErr(2)
	}
	daemonRoot := daemon.DaemonRoot(teamDir)
	exists, err := inboxInstanceExists(daemonRoot, instance)
	if err != nil {
		return err
	}
	if !exists {
		fmt.Fprintf(stderr, "agent-team inbox ack: no such inbox: %s\n", instance)
		return exitErr(2)
	}
	messages, err := daemon.ReadMessages(daemonRoot, instance)
	if err != nil {
		return err
	}
	cursorBefore, err := daemon.ReadCursor(daemonRoot, instance)
	if err != nil {
		return err
	}
	cursorAfter, acked, err := planInboxAck(messages, cursorBefore, opts)
	if err != nil {
		fmt.Fprintf(stderr, "agent-team inbox ack: %v\n", err)
		return exitErr(2)
	}
	if !opts.DryRun && cursorAfter != cursorBefore && cursorAfter != "" {
		if err := daemon.WriteCursor(daemonRoot, instance, cursorAfter); err != nil {
			return err
		}
	}
	result := inboxAckResult{
		Instance:      instance,
		ID:            opts.ID,
		All:           opts.All,
		DryRun:        opts.DryRun,
		Total:         len(messages),
		Acked:         acked,
		UnreadBefore:  inboxUnreadCount(messages, cursorBefore),
		UnreadAfter:   inboxUnreadCount(messages, cursorAfter),
		CursorBefore:  cursorBefore,
		CursorAfter:   cursorAfter,
		CursorChanged: cursorAfter != cursorBefore,
	}
	if opts.JSON {
		return json.NewEncoder(stdout).Encode(result)
	}
	if opts.Format != nil {
		if err := opts.Format.Execute(stdout, result); err != nil {
			return err
		}
		_, err := fmt.Fprintln(stdout)
		return err
	}
	action := "acknowledged"
	if opts.DryRun {
		action = "would-ack"
	}
	fmt.Fprintf(stdout, "  %s   %-20s acked=%d unread=%d cursor=%s\n",
		action, instance, result.Acked, result.UnreadAfter, emptyDash(result.CursorAfter))
	return nil
}

func listInboxInstances(daemonRoot string) ([]string, map[string]*daemon.Metadata, error) {
	out := map[string]bool{}
	metaByInstance := map[string]*daemon.Metadata{}
	metas, err := daemon.ListMetadata(daemonRoot)
	if err != nil {
		return nil, nil, err
	}
	for _, meta := range metas {
		if meta == nil || strings.TrimSpace(meta.Instance) == "" {
			continue
		}
		out[meta.Instance] = true
		metaByInstance[meta.Instance] = meta
	}
	entries, err := os.ReadDir(daemonRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return sortedInboxInstances(out), metaByInstance, nil
		}
		return nil, nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		instance := entry.Name()
		if _, err := os.Stat(daemon.MailboxPath(daemonRoot, instance)); err == nil {
			out[instance] = true
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, nil, err
		}
	}
	return sortedInboxInstances(out), metaByInstance, nil
}

func sortedInboxInstances(instances map[string]bool) []string {
	out := make([]string, 0, len(instances))
	for instance := range instances {
		out = append(out, instance)
	}
	sort.Strings(out)
	return out
}

func filterInboxInstancesForTeam(top *topology.Topology, team *topology.Team, instances []string, metaByInstance map[string]*daemon.Metadata) []string {
	if top == nil || team == nil {
		return nil
	}
	declared := stringSliceSet(team.Instances)
	ephemeralOwners := map[string]bool{}
	for _, name := range team.Instances {
		if inst := top.Instances[name]; inst != nil && inst.Ephemeral {
			ephemeralOwners[inst.Name] = true
		}
	}
	out := make([]string, 0, len(instances))
	for _, instance := range instances {
		if declared[instance] {
			out = append(out, instance)
			continue
		}
		meta := metaByInstance[instance]
		agent := ""
		if meta != nil {
			agent = meta.Agent
		}
		if owner, ok := declaredEphemeralOwner(top, instance, agent); ok && ephemeralOwners[owner.Name] {
			out = append(out, instance)
		}
	}
	return out
}

func inboxInstanceExists(daemonRoot, instance string) (bool, error) {
	if _, err := daemon.ReadMetadata(daemonRoot, instance); err == nil {
		return true, nil
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if _, err := os.Stat(daemon.MailboxPath(daemonRoot, instance)); err == nil {
		return true, nil
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if _, err := os.Stat(daemon.MailboxCursorPath(daemonRoot, instance)); err == nil {
		return true, nil
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	return false, nil
}

func inboxMessageRows(instance string, messages []*daemon.Message, cursor string, unreadOnly bool) []inboxMessageRow {
	cursorIndex := inboxCursorIndex(messages, cursor)
	rows := make([]inboxMessageRow, 0, len(messages))
	for i, msg := range messages {
		if msg == nil {
			continue
		}
		unread := cursorIndex < 0 || i > cursorIndex
		if unreadOnly && !unread {
			continue
		}
		rows = append(rows, inboxMessageRow{
			Instance: instance,
			ID:       msg.ID,
			From:     msg.From,
			To:       msg.To,
			Body:     msg.Body,
			TS:       msg.TS,
			Unread:   unread,
		})
	}
	return rows
}

func planInboxAck(messages []*daemon.Message, cursorBefore string, opts inboxAckOptions) (string, int, error) {
	if len(messages) == 0 {
		if opts.All {
			return cursorBefore, 0, nil
		}
		return "", 0, fmt.Errorf("message id %q not found in inbox", opts.ID)
	}
	targetIndex := len(messages) - 1
	if !opts.All {
		targetIndex = -1
		for i, msg := range messages {
			if msg != nil && msg.ID == opts.ID {
				targetIndex = i
				break
			}
		}
		if targetIndex < 0 {
			return "", 0, fmt.Errorf("message id %q not found in inbox", opts.ID)
		}
	}
	cursorIndex := inboxCursorIndex(messages, cursorBefore)
	if cursorIndex >= targetIndex && cursorIndex >= 0 {
		return cursorBefore, 0, nil
	}
	cursorAfter := messages[targetIndex].ID
	acked := targetIndex - cursorIndex
	if cursorIndex < 0 {
		acked = targetIndex + 1
	}
	return cursorAfter, acked, nil
}

func inboxUnreadCount(messages []*daemon.Message, cursor string) int {
	if len(messages) == 0 {
		return 0
	}
	cursorIndex := inboxCursorIndex(messages, cursor)
	if cursorIndex < 0 {
		return len(messages)
	}
	return len(messages) - cursorIndex - 1
}

func inboxCursorIndex(messages []*daemon.Message, cursor string) int {
	if strings.TrimSpace(cursor) == "" {
		return -1
	}
	for i, msg := range messages {
		if msg != nil && msg.ID == cursor {
			return i
		}
	}
	return -1
}

func parseInboxFormat(format, name string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New(name).Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderInboxFormat[T any](w io.Writer, rows []T, tmpl *template.Template) error {
	for _, row := range rows {
		if err := tmpl.Execute(w, row); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}

func inboxAge(now, ts time.Time) string {
	if ts.IsZero() {
		return "-"
	}
	d := now.Sub(ts)
	if d < 0 {
		d = 0
	}
	return humanAge(d)
}

func compactInboxBody(body string, max int) string {
	body = strings.Join(strings.Fields(body), " ")
	if body == "" {
		return "-"
	}
	if max <= 0 || len(body) <= max {
		return body
	}
	if max <= 3 {
		return body[:max]
	}
	return body[:max-3] + "..."
}
