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

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newInboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "Inspect and acknowledge daemon mailbox messages.",
		Long: "Inspect daemon mailbox messages stored under .agent_team/daemon. " +
			"The inbox commands read local files directly, so they work even when agent-teamd is not running.",
	}
	cmd.AddCommand(newInboxCheckCmd())
	cmd.AddCommand(newInboxLsCmd())
	cmd.AddCommand(newInboxShowCmd())
	cmd.AddCommand(newInboxAckCmd())
	cmd.AddCommand(newInboxSendCmd())
	cmd.AddCommand(newInboxPruneCmd())
	return cmd
}

func newInboxCheckCmd() *cobra.Command {
	var (
		target   string
		self     bool
		tail     int
		commands bool
		jsonOut  bool
		format   string
	)
	cmd := &cobra.Command{
		Use:   "check [instance]",
		Short: "Show unread messages for the current instance inbox.",
		Long: "Show unread messages for one inbox. With no instance argument, the target defaults to AGENT_TEAM_INSTANCE; " +
			"use --self to make that selection explicit.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox check: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox check: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox check: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if tail < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox check: --tail must be >= 0.")
				return exitErr(2)
			}
			if self && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox check: --self cannot be combined with an instance argument.")
				return exitErr(2)
			}
			instance, err := resolveInboxCheckInstance(args, self)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team inbox check: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parseInboxFormat(format, "inbox-check-format")
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team inbox check: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			return runInboxShow(cmd.OutOrStdout(), cmd.ErrOrStderr(), teamDir, instance, inboxShowOptions{
				Command:        "check",
				UnreadOnly:     true,
				Tail:           tail,
				Commands:       commands,
				RepoFlag:       inboxRepoFlag(cmd),
				Repo:           inboxRepo(cmd, target),
				RepoSet:        inboxRepoSet(cmd),
				JSON:           jsonOut,
				Format:         tmpl,
				EmptyUnreadMsg: "(no new messages)",
			})
		},
	}
	cmd.Flags().BoolVar(&self, "self", false, "Read the inbox for AGENT_TEAM_INSTANCE.")
	cmd.Flags().IntVar(&tail, "tail", 0, "Show only the N most recent unread messages (0 = all).")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print an inbox ack command for the OLDEST unread message (the next valid ack); independent of --tail.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each message with a Go template, e.g. '{{.ID}} {{.Unread}} {{.Body}}'.")
	return cmd
}

func newInboxLsCmd() *cobra.Command {
	var (
		target     string
		teamName   string
		unreadOnly bool
		sortBy     string
		limit      int
		commands   bool
		jsonOut    bool
		format     string
	)
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List inbox summaries by instance.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox ls: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox ls: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox ls: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox ls: --limit must be >= 0.")
				return exitErr(2)
			}
			sortMode, err := parseInboxListSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team inbox ls: %v\n", err)
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
				Sort:       sortMode,
				Limit:      limit,
				Commands:   commands,
				RepoFlag:   inboxRepoFlag(cmd),
				Repo:       inboxRepo(cmd, target),
				RepoSet:    inboxRepoSet(cmd),
				JSON:       jsonOut,
				Format:     tmpl,
			})
		},
	}
	cmd.Flags().StringVar(&teamName, "team", "", "Only list inboxes owned by this declared team.")
	cmd.Flags().BoolVar(&unreadOnly, "unread", false, "Show only inboxes with unread messages.")
	cmd.Flags().StringVar(&sortBy, "sort", "instance", "Sort inboxes by instance, unread, latest, or total.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit inbox summaries after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print inbox check commands for inboxes with unread messages. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each inbox summary with a Go template, e.g. '{{.Instance}} {{.Unread}}'.")
	return cmd
}

func newInboxShowCmd() *cobra.Command {
	var (
		target     string
		unreadOnly bool
		tail       int
		commands   bool
		jsonOut    bool
		format     string
	)
	cmd := &cobra.Command{
		Use:   "show <instance>",
		Short: "Show messages for one instance inbox.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox show: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox show: --commands cannot be combined with --format.")
				return exitErr(2)
			}
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
				Command:    "show",
				UnreadOnly: unreadOnly,
				Tail:       tail,
				Commands:   commands,
				RepoFlag:   inboxRepoFlag(cmd),
				Repo:       inboxRepo(cmd, target),
				RepoSet:    inboxRepoSet(cmd),
				JSON:       jsonOut,
				Format:     tmpl,
			})
		},
	}
	cmd.Flags().BoolVar(&unreadOnly, "unread", false, "Show only messages after the inbox cursor.")
	cmd.Flags().IntVar(&tail, "tail", 0, "Show only the N most recent matching messages (0 = all).")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print an inbox ack command for the OLDEST unread message (the next valid ack); independent of --tail.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each message with a Go template, e.g. '{{.ID}} {{.Unread}} {{.Body}}'.")
	return cmd
}

func newInboxAckCmd() *cobra.Command {
	var (
		target   string
		self     bool
		all      bool
		dryRun   bool
		commands bool
		jsonOut  bool
		format   string
	)
	cmd := &cobra.Command{
		Use:   "ack [instance] <message-id>|--all",
		Short: "Acknowledge unread inbox messages in order.",
		Long: "Acknowledge unread inbox messages by advancing the inbox cursor. Ack-by-id only accepts the next unread message, " +
			"so it cannot accidentally skip earlier unread messages. With no instance argument, the target defaults to AGENT_TEAM_INSTANCE. " +
			"Use --all to acknowledge every current message.",
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox ack: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox ack: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox ack: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox ack: --format cannot be combined with --json.")
				return exitErr(2)
			}
			instance, id, err := resolveInboxAckTarget(args, all, self)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team inbox ack: %v\n", err)
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
			return runInboxAck(cmd.OutOrStdout(), cmd.ErrOrStderr(), teamDir, instance, inboxAckOptions{
				All:      all,
				ID:       id,
				DryRun:   dryRun,
				Commands: commands,
				RepoFlag: inboxRepoFlag(cmd),
				Repo:     inboxRepo(cmd, target),
				RepoSet:  inboxRepoSet(cmd),
				JSON:     jsonOut,
				Format:   tmpl,
			})
		},
	}
	cmd.Flags().BoolVar(&self, "self", false, "Acknowledge the inbox for AGENT_TEAM_INSTANCE.")
	cmd.Flags().BoolVar(&all, "all", false, "Acknowledge every current message in the inbox.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the cursor update without writing it.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching inbox ack apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the ack result with a Go template, e.g. '{{.Instance}} {{.Acked}}'.")
	return cmd
}

func newInboxSendCmd() *cobra.Command {
	var (
		target      string
		from        string
		message     string
		messageFile string
		dryRun      bool
		commands    bool
		jsonOut     bool
		format      string
	)
	cmd := &cobra.Command{
		Use:   "send <to> [message...]",
		Short: "Send a mailbox message to a daemon-managed instance.",
		Long:  "Send a direct message through the daemon mailbox. This is the inbox-scoped alias for `agent-team send <to> <message...>`.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox send: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox send: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox send: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox send: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if len(args) < 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox send: recipient and message body are required.")
				return exitErr(2)
			}
			to := args[0]
			body, err := sendMessageBodyPreservingFile(message, messageFile, args[1:])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team inbox send: %v\n", err)
				return exitErr(2)
			}
			formatTemplate, err := parseSendFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team inbox send: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			topo, err := topology.LoadFromTeamDir(teamDir)
			if err != nil {
				return fmt.Errorf("load topology: %w", err)
			}
			client, err := sendClientForTeamDir(teamDir)
			if err != nil {
				return err
			}
			opts := sendOptions{
				From:     from,
				DryRun:   dryRun,
				JSON:     jsonOut,
				Format:   formatTemplate,
				Topology: topo,
			}
			if commands {
				resolved, err := resolveSendTarget(client, to, opts.Topology)
				if err != nil {
					return err
				}
				if !resolved.Valid() {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team inbox send: %s\n", daemon.MailboxUnknownTargetMessage(to, resolved.Suggestions))
					return exitErr(2)
				}
				scope := operatorCommandScopeFromCommand(cmd, target, rootRepoFlagName)
				return renderScopedSendApplyCommand(cmd.OutOrStdout(), true, scopedSendApplyCommandOptions{
					BaseArgs:       []string{"agent-team", "inbox", "send", to},
					RepoFlag:       "repo",
					Repo:           scope.Repo,
					RepoSet:        scope.Set,
					From:           from,
					FromSet:        cmd.Flags().Changed("from"),
					Message:        message,
					MessageSet:     cmd.Flags().Changed("message"),
					MessageFile:    messageFile,
					MessageFileSet: cmd.Flags().Changed("message-file"),
					Positional:     args[1:],
				})
			}
			return runSendWithClient(cmd.OutOrStdout(), cmd.ErrOrStderr(), client, to, body, opts)
		},
	}
	cmd.Flags().StringVar(&from, "from", "(cli)", "Sender label recorded with the message.")
	cmd.Flags().StringVar(&message, "message", "", "Message text to send.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read message text from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the target without appending a mailbox message.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching inbox send apply command when the preview has an actionable recipient. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each send result with a Go template, e.g. '{{.To}} {{.ID}}'.")
	return cmd
}

func newInboxPruneCmd() *cobra.Command {
	var (
		target    string
		teamName  string
		all       bool
		olderThan time.Duration
		limit     int
		dryRun    bool
		commands  bool
		jsonOut   bool
		format    string
	)
	cmd := &cobra.Command{
		Use:   "prune <instance>...|--all",
		Short: "Prune acknowledged inbox messages.",
		Long: "Prune acknowledged inbox messages while preserving the cursor anchor message. " +
			"Unread messages are never removed.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox prune: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox prune: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox prune: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox prune: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox prune: --all cannot be combined with explicit instances.")
				return exitErr(2)
			}
			if !all && len(args) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox prune: instance or --all is required.")
				return exitErr(2)
			}
			if strings.TrimSpace(teamName) != "" && !all {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox prune: --team requires --all.")
				return exitErr(2)
			}
			if olderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox prune: --older-than must be >= 0.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team inbox prune: --limit must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parseInboxFormat(format, "inbox-prune-format")
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team inbox prune: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			instances, err := resolveInboxPruneInstances(teamDir, args, all, teamName)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team inbox prune: %v\n", err)
				return exitErr(2)
			}
			opts := inboxPruneOptions{
				OlderThan: olderThan,
				Limit:     limit,
				Now:       time.Now().UTC(),
				DryRun:    dryRun,
			}
			results, err := runInboxPrune(teamDir, instances, opts)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team inbox prune: %v\n", err)
				return exitErr(2)
			}
			if commands {
				return renderInboxPruneApplyCommand(cmd.OutOrStdout(), inboxPruneResultsHaveDryRunAction(results), inboxPruneApplyCommandOptions{
					Instances:    args,
					All:          all,
					TeamName:     teamName,
					TeamSet:      cmd.Flags().Changed("team"),
					OlderThan:    olderThan,
					OlderThanSet: cmd.Flags().Changed("older-than"),
					Limit:        limit,
					RepoFlag:     inboxRepoFlag(cmd),
					Repo:         inboxRepo(cmd, target),
					RepoSet:      inboxRepoSet(cmd),
				})
			}
			return renderInboxPruneResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&teamName, "team", "", "With --all, only prune inboxes owned by this declared team.")
	cmd.Flags().BoolVar(&all, "all", false, "Prune every current inbox.")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "Only prune acknowledged messages older than this duration.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Prune at most this many acknowledged messages per inbox; 0 means no limit.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview inbox compaction without rewriting mailbox files.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching inbox prune apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each prune result with a Go template, e.g. '{{.Instance}} {{.Dropped}}'.")
	return cmd
}

type inboxListOptions struct {
	TeamName   string
	UnreadOnly bool
	Sort       string
	Limit      int
	Commands   bool
	RepoFlag   string
	Repo       string
	RepoSet    bool
	JSON       bool
	Format     *template.Template
}

type inboxShowOptions struct {
	Command        string
	UnreadOnly     bool
	Tail           int
	Commands       bool
	RepoFlag       string
	Repo           string
	RepoSet        bool
	JSON           bool
	Format         *template.Template
	EmptyUnreadMsg string
}

type inboxAckOptions struct {
	All      bool
	ID       string
	DryRun   bool
	Commands bool
	RepoFlag string
	Repo     string
	RepoSet  bool
	JSON     bool
	Format   *template.Template
}

type inboxPruneOptions struct {
	OlderThan time.Duration
	Limit     int
	Now       time.Time
	DryRun    bool
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
	ReplyTo  string    `json:"reply_to,omitempty"`
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

type inboxPruneResult struct {
	Instance    string `json:"instance"`
	Total       int    `json:"total"`
	Dropped     int    `json:"dropped"`
	Kept        int    `json:"kept"`
	Unread      int    `json:"unread"`
	Cursor      string `json:"cursor,omitempty"`
	CursorFound bool   `json:"cursor_found"`
	DryRun      bool   `json:"dry_run,omitempty"`
	Changed     bool   `json:"changed"`
	Action      string `json:"action"`
	MailboxPath string `json:"mailbox_path,omitempty"`
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
	sortInboxSummaryRows(rows, opts.Sort)
	rows = limitInboxSummaryRows(rows, opts.Limit)
	if opts.Commands {
		return renderInboxListCommands(stdout, rows, inboxListCommandOptions{
			RepoFlag: opts.RepoFlag,
			Repo:     opts.Repo,
			RepoSet:  opts.RepoSet,
		})
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

func resolveInboxCheckInstance(args []string, self bool) (string, error) {
	if len(args) > 0 {
		instance := strings.TrimSpace(args[0])
		if instance == "" {
			return "", fmt.Errorf("instance is required")
		}
		return instance, nil
	}
	instance := strings.TrimSpace(os.Getenv("AGENT_TEAM_INSTANCE"))
	if instance == "" {
		if self {
			return "", fmt.Errorf("--self requires AGENT_TEAM_INSTANCE to be set")
		}
		return "", fmt.Errorf("instance is required when AGENT_TEAM_INSTANCE is not set")
	}
	return instance, nil
}

func resolveInboxAckTarget(args []string, all, self bool) (string, string, error) {
	if self && len(args) > 1 {
		return "", "", fmt.Errorf("--self cannot be combined with an instance argument")
	}
	if all {
		if len(args) > 1 {
			return "", "", fmt.Errorf("--all cannot be combined with a message id")
		}
		if self && len(args) > 0 {
			return "", "", fmt.Errorf("--self cannot be combined with an instance argument")
		}
		instance, err := resolveInboxSelfOrInstance(args)
		return instance, "", err
	}
	switch len(args) {
	case 2:
		if self {
			return "", "", fmt.Errorf("--self cannot be combined with an instance argument")
		}
		instance := strings.TrimSpace(args[0])
		id := strings.TrimSpace(args[1])
		if instance == "" {
			return "", "", fmt.Errorf("instance is required")
		}
		if id == "" {
			return "", "", fmt.Errorf("message id is required unless --all is set")
		}
		return instance, id, nil
	case 1:
		instance, err := resolveInboxSelfOrInstance(nil)
		if err != nil {
			return "", "", err
		}
		id := strings.TrimSpace(args[0])
		if id == "" {
			return "", "", fmt.Errorf("message id is required unless --all is set")
		}
		return instance, id, nil
	default:
		return "", "", fmt.Errorf("message id is required unless --all is set")
	}
}

func resolveInboxSelfOrInstance(args []string) (string, error) {
	if len(args) > 0 {
		instance := strings.TrimSpace(args[0])
		if instance == "" {
			return "", fmt.Errorf("instance is required")
		}
		return instance, nil
	}
	instance := strings.TrimSpace(os.Getenv("AGENT_TEAM_INSTANCE"))
	if instance == "" {
		return "", fmt.Errorf("instance is required when AGENT_TEAM_INSTANCE is not set")
	}
	return instance, nil
}

const (
	inboxListSortInstance = "instance"
	inboxListSortUnread   = "unread"
	inboxListSortLatest   = "latest"
	inboxListSortTotal    = "total"
)

func parseInboxListSort(raw string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", inboxListSortInstance:
		return inboxListSortInstance, nil
	case inboxListSortUnread:
		return inboxListSortUnread, nil
	case inboxListSortLatest:
		return inboxListSortLatest, nil
	case inboxListSortTotal:
		return inboxListSortTotal, nil
	default:
		return "", fmt.Errorf("--sort must be instance, unread, latest, or total")
	}
}

func sortInboxSummaryRows(rows []inboxSummaryRow, sortBy string) {
	sort.SliceStable(rows, func(i, j int) bool {
		left, right := rows[i], rows[j]
		tie := func() bool {
			return left.Instance < right.Instance
		}
		switch sortBy {
		case inboxListSortUnread:
			if left.Unread != right.Unread {
				return left.Unread > right.Unread
			}
			return tie()
		case inboxListSortLatest:
			if left.LatestTS.IsZero() != right.LatestTS.IsZero() {
				return !left.LatestTS.IsZero()
			}
			if !left.LatestTS.Equal(right.LatestTS) {
				return left.LatestTS.After(right.LatestTS)
			}
			return tie()
		case inboxListSortTotal:
			if left.Total != right.Total {
				return left.Total > right.Total
			}
			return tie()
		default:
			return tie()
		}
	})
}

func limitInboxSummaryRows(rows []inboxSummaryRow, limit int) []inboxSummaryRow {
	if limit <= 0 || len(rows) <= limit {
		return rows
	}
	return rows[:limit]
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
	command := opts.Command
	if command == "" {
		command = "show"
	}
	instance = strings.TrimSpace(instance)
	if instance == "" {
		fmt.Fprintf(stderr, "agent-team inbox %s: instance is required.\n", command)
		return exitErr(2)
	}
	daemonRoot := daemon.DaemonRoot(teamDir)
	exists, err := inboxInstanceExists(daemonRoot, instance)
	if err != nil {
		return err
	}
	if !exists {
		fmt.Fprintf(stderr, "agent-team inbox %s: no such inbox: %s\n", command, instance)
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
	firstUnreadID := firstUnreadInboxMessageID(rows)
	if opts.Tail > 0 && len(rows) > opts.Tail {
		rows = rows[len(rows)-opts.Tail:]
	}
	if opts.Commands {
		return renderInboxAckApplyCommand(stdout, firstUnreadID != "", inboxAckApplyCommandOptions{
			Instance: instance,
			ID:       firstUnreadID,
			RepoFlag: opts.RepoFlag,
			Repo:     opts.Repo,
			RepoSet:  opts.RepoSet,
		})
	}
	if opts.JSON {
		return json.NewEncoder(stdout).Encode(rows)
	}
	if opts.Format != nil {
		return renderInboxFormat(stdout, rows, opts.Format)
	}
	if len(rows) == 0 {
		if opts.UnreadOnly {
			msg := opts.EmptyUnreadMsg
			if msg == "" {
				msg = "(no unread messages)"
			}
			fmt.Fprintln(stdout, msg)
		} else {
			fmt.Fprintln(stdout, "(no messages)")
		}
		return nil
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tUNREAD\tAGE\tFROM\tREPLY_TO\tMESSAGE")
	now := time.Now()
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%t\t%s\t%s\t%s\t%s\n",
			row.ID,
			row.Unread,
			inboxAge(now, row.TS),
			emptyDash(row.From),
			emptyDash(row.ReplyTo),
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
	if opts.Commands {
		return renderInboxAckApplyCommand(stdout, result.CursorChanged && result.Acked > 0, inboxAckApplyCommandOptions{
			Instance: instance,
			ID:       opts.ID,
			All:      opts.All,
			RepoFlag: opts.RepoFlag,
			Repo:     opts.Repo,
			RepoSet:  opts.RepoSet,
		})
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

func resolveInboxPruneInstances(teamDir string, args []string, all bool, teamName string) ([]string, error) {
	if all {
		daemonRoot := daemon.DaemonRoot(teamDir)
		instances, metaByInstance, err := listInboxInstances(daemonRoot)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(teamName) != "" {
			top, team, err := loadTopologyTeam(teamDir, teamName)
			if err != nil {
				return nil, err
			}
			instances = filterInboxInstancesForTeam(top, team, instances, metaByInstance)
		}
		return instances, nil
	}
	return uniqueNonEmptyStrings(args), nil
}

func runInboxPrune(teamDir string, instances []string, opts inboxPruneOptions) ([]inboxPruneResult, error) {
	daemonRoot := daemon.DaemonRoot(teamDir)
	results := make([]inboxPruneResult, 0, len(instances))
	for _, instance := range instances {
		instance = strings.TrimSpace(instance)
		if instance == "" {
			continue
		}
		exists, err := inboxInstanceExists(daemonRoot, instance)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("no such inbox: %s", instance)
		}
		messages, err := daemon.ReadMessages(daemonRoot, instance)
		if err != nil {
			return nil, err
		}
		cursor, err := daemon.ReadCursor(daemonRoot, instance)
		if err != nil {
			return nil, err
		}
		kept, dropped, cursorFound := planInboxPrune(messages, cursor, opts)
		result := inboxPruneResult{
			Instance:    instance,
			Total:       len(messages),
			Dropped:     dropped,
			Kept:        len(kept),
			Unread:      inboxUnreadCount(messages, cursor),
			Cursor:      cursor,
			CursorFound: cursorFound,
			DryRun:      opts.DryRun,
			Changed:     dropped > 0,
			MailboxPath: daemon.MailboxPath(daemonRoot, instance),
		}
		result.Action = inboxPruneAction(result)
		if dropped > 0 && !opts.DryRun {
			if err := daemon.RewriteMessages(daemonRoot, instance, kept); err != nil {
				return nil, err
			}
		}
		results = append(results, result)
	}
	return results, nil
}

func planInboxPrune(messages []*daemon.Message, cursor string, opts inboxPruneOptions) ([]*daemon.Message, int, bool) {
	cursorIndex := inboxCursorIndex(messages, cursor)
	if strings.TrimSpace(cursor) == "" || cursorIndex < 0 {
		return messages, 0, false
	}
	kept := make([]*daemon.Message, 0, len(messages))
	dropped := 0
	for i, msg := range messages {
		if i < cursorIndex && inboxPruneMessageEligible(msg, opts) && (opts.Limit <= 0 || dropped < opts.Limit) {
			dropped++
			continue
		}
		kept = append(kept, msg)
	}
	return kept, dropped, true
}

func inboxPruneMessageEligible(msg *daemon.Message, opts inboxPruneOptions) bool {
	if opts.OlderThan <= 0 {
		return true
	}
	if msg == nil || msg.TS.IsZero() {
		return false
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return !msg.TS.After(now.Add(-opts.OlderThan))
}

func renderInboxPruneResults(w io.Writer, results []inboxPruneResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(results)
	}
	if tmpl != nil {
		for _, result := range results {
			if err := tmpl.Execute(w, result); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	if len(results) == 0 {
		fmt.Fprintln(w, "(no inboxes pruned)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "INSTANCE\tTOTAL\tDROPPED\tKEPT\tUNREAD\tACTION\tCURSOR")
	for _, result := range results {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%s\t%s\n",
			result.Instance,
			result.Total,
			result.Dropped,
			result.Kept,
			result.Unread,
			result.Action,
			emptyDash(result.Cursor),
		)
	}
	return tw.Flush()
}

func inboxPruneAction(result inboxPruneResult) string {
	if result.Dropped == 0 {
		return "kept"
	}
	if result.DryRun {
		return "would-prune"
	}
	return "pruned"
}

func inboxPruneResultsHaveDryRunAction(results []inboxPruneResult) bool {
	for _, result := range results {
		if result.DryRun && result.Dropped > 0 {
			return true
		}
	}
	return false
}

type inboxListCommandOptions struct {
	RepoFlag string
	Repo     string
	RepoSet  bool
}

func renderInboxListCommands(w io.Writer, rows []inboxSummaryRow, opts inboxListCommandOptions) error {
	for _, row := range rows {
		if row.Unread <= 0 {
			continue
		}
		args := []string{"agent-team", "inbox", "check", row.Instance}
		args = appendInboxRepoArgs(args, opts.RepoFlag, opts.Repo, opts.RepoSet)
		if _, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(args), " ")); err != nil {
			return err
		}
	}
	return nil
}

func firstUnreadInboxMessageID(rows []inboxMessageRow) string {
	for _, row := range rows {
		if row.Unread {
			return row.ID
		}
	}
	return ""
}

type inboxAckApplyCommandOptions struct {
	Instance string
	ID       string
	All      bool
	RepoFlag string
	Repo     string
	RepoSet  bool
}

type inboxPruneApplyCommandOptions struct {
	Instances    []string
	All          bool
	TeamName     string
	TeamSet      bool
	OlderThan    time.Duration
	OlderThanSet bool
	Limit        int
	RepoFlag     string
	Repo         string
	RepoSet      bool
}

func renderInboxAckApplyCommand(w io.Writer, hasAction bool, opts inboxAckApplyCommandOptions) error {
	if !hasAction {
		return nil
	}
	_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(inboxAckApplyCommandArgs(opts)), " "))
	return err
}

func renderInboxPruneApplyCommand(w io.Writer, hasAction bool, opts inboxPruneApplyCommandOptions) error {
	if !hasAction {
		return nil
	}
	_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(inboxPruneApplyCommandArgs(opts)), " "))
	return err
}

func inboxAckApplyCommandArgs(opts inboxAckApplyCommandOptions) []string {
	args := []string{"agent-team", "inbox", "ack", opts.Instance}
	if opts.All {
		args = append(args, "--all")
	} else {
		args = append(args, opts.ID)
	}
	return appendInboxRepoArgs(args, opts.RepoFlag, opts.Repo, opts.RepoSet)
}

func inboxPruneApplyCommandArgs(opts inboxPruneApplyCommandOptions) []string {
	args := []string{"agent-team", "inbox", "prune"}
	if !opts.All {
		args = append(args, uniqueNonEmptyStrings(opts.Instances)...)
	}
	args = appendInboxRepoArgs(args, opts.RepoFlag, opts.Repo, opts.RepoSet)
	if opts.All {
		args = append(args, "--all")
	}
	if opts.TeamSet && strings.TrimSpace(opts.TeamName) != "" {
		args = append(args, "--team", opts.TeamName)
	}
	if opts.OlderThanSet {
		args = append(args, "--older-than", opts.OlderThan.String())
	}
	if opts.Limit > 0 {
		args = append(args, "--limit", fmt.Sprint(opts.Limit))
	}
	return args
}

func appendInboxRepoArgs(args []string, repoFlag, repo string, repoSet bool) []string {
	if !repoSet || strings.TrimSpace(repo) == "" {
		return args
	}
	flag := strings.TrimSpace(repoFlag)
	if flag == "" {
		flag = rootRepoFlagName
	}
	return append(args, "--"+flag, repo)
}

func inboxRepoSet(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	if flag := cmd.Root().PersistentFlags().Lookup(rootRepoFlagName); flag != nil && flag.Changed {
		return true
	}
	return cmd.Flags().Changed(rootRepoFlagName)
}

func inboxRepoFlag(cmd *cobra.Command) string {
	return rootRepoFlagName
}

func inboxRepo(cmd *cobra.Command, target string) string {
	if cmd != nil {
		if flag := cmd.Root().PersistentFlags().Lookup(rootRepoFlagName); flag != nil && flag.Changed {
			if value := strings.TrimSpace(flag.Value.String()); value != "" {
				return value
			}
		}
	}
	return target
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
			ReplyTo:  msg.ReplyTo,
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
	nextUnreadIndex := cursorIndex + 1
	if cursorIndex < 0 {
		nextUnreadIndex = 0
	}
	if !opts.All && targetIndex > nextUnreadIndex {
		nextID := ""
		if nextUnreadIndex >= 0 && nextUnreadIndex < len(messages) && messages[nextUnreadIndex] != nil {
			nextID = messages[nextUnreadIndex].ID
		}
		if nextID != "" {
			return "", 0, fmt.Errorf("message id %q is not the next unread message; handle %q first or use --all to acknowledge every current message", opts.ID, nextID)
		}
		return "", 0, fmt.Errorf("message id %q is not the next unread message; use --all to acknowledge every current message", opts.ID)
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
