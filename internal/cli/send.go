package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/runtimebin"
	"github.com/agent-team-project/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

var sendMessageInput io.Reader = os.Stdin

func newSendCmd() *cobra.Command {
	var (
		target        string
		from          string
		message       string
		messageFile   string
		all           bool
		latest        bool
		last          int
		agents        []string
		runtimes      []string
		statusFilters []string
		phaseFilters  []string
		staleOnly     bool
		runtimeStale  bool
		unhealthyOnly bool
		allowMissing  bool
		interrupt     bool
		force         bool
		dryRun        bool
		commands      bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "send [<instance>] <message...>",
		Short: "Send a mailbox message to a daemon-managed instance.",
		Long: "Send a direct message through the daemon mailbox. The target must be daemon-known or declared in instances.toml; " +
			"declared instances that are not currently running are queued for their next spawn or resume. Unknown undeclared targets fail with typo suggestions. " +
			"Use --all, --latest, --last, --agent, --runtime, --status, --phase, --stale, --runtime-stale, or --unhealthy to send the same message to a selected set of daemon-known instances.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team send: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team send: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team send: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team send: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team send: --last must be >= 0.")
				return exitErr(2)
			}
			if force && !interrupt {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team send: --force requires --interrupt.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team send: choose one of --latest or --last.")
				return exitErr(2)
			}
			formatTemplate, err := parseSendFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team send: %v\n", err)
				return exitErr(2)
			}
			opts := sendOptions{
				From:           from,
				All:            all,
				Latest:         latest,
				Limit:          last,
				AgentFilters:   agents,
				RuntimeFilters: runtimes,
				StatusFilters:  statusFilters,
				PhaseFilters:   phaseFilters,
				Stale:          staleOnly,
				RuntimeStale:   runtimeStale,
				Unhealthy:      unhealthyOnly,
				AllowMissing:   allowMissing,
				Interrupt:      interrupt,
				Force:          force,
				DryRun:         dryRun,
				JSON:           jsonOut,
				Format:         formatTemplate,
			}
			if interrupt && opts.selectingSet() {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team send: --interrupt requires a single instance target.")
				return exitErr(2)
			}
			var (
				to   string
				body string
			)
			if opts.selectingSet() {
				body, err = sendMessageBody(message, messageFile, args)
			} else {
				if len(args) < 2 && strings.TrimSpace(message) == "" && strings.TrimSpace(messageFile) == "" {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team send: instance and message body are required unless --all, --latest, --last, --agent, --runtime, --status, --phase, --stale, --runtime-stale, or --unhealthy is set.")
					return exitErr(2)
				}
				if len(args) < 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team send: instance and message body are required unless --all, --latest, --last, --agent, --runtime, --status, --phase, --stale, --runtime-stale, or --unhealthy is set.")
					return exitErr(2)
				}
				to = args[0]
				body, err = sendMessageBody(message, messageFile, args[1:])
			}
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team send: %v\n", err)
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
			opts.Topology = topo
			if allowMissing {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team send: --allow-missing is deprecated and no longer changes recipient validation; declared instances queue automatically.")
			}
			client, err := sendClientForTeamDir(teamDir)
			if err != nil {
				return err
			}
			if interrupt && !dryRun {
				if _, ok := client.(localSendClient); ok {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team send: --interrupt requires a running daemon.")
					return exitErr(2)
				}
			}
			if len(phaseFilters) > 0 {
				opts.PhaseByInstance = sendPhaseByInstance(teamDir, time.Now())
			}
			if staleOnly || unhealthyOnly {
				opts.StaleByInstance = staleInstanceSet(teamDir, time.Now())
			}
			if commands {
				scope := operatorCommandScopeFromCommand(cmd, target, "target")
				if opts.selectingSet() {
					targets, err := selectSendTargets(client, opts)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team send: %v\n", err)
						return exitErr(2)
					}
					return renderScopedSendApplyCommand(cmd.OutOrStdout(), len(targets) > 0, scopedSendApplyCommandOptions{
						BaseArgs:       []string{"agent-team", "send"},
						RepoFlag:       "repo",
						Repo:           scope.Repo,
						RepoSet:        scope.Set,
						From:           from,
						FromSet:        cmd.Flags().Changed("from"),
						Message:        message,
						MessageSet:     cmd.Flags().Changed("message"),
						MessageFile:    messageFile,
						MessageFileSet: cmd.Flags().Changed("message-file"),
						Positional:     args,
						All:            all,
						Latest:         latest,
						Last:           last,
						AgentFilters:   agents,
						AgentSet:       cmd.Flags().Changed("agent"),
						RuntimeFilters: runtimes,
						RuntimeSet:     cmd.Flags().Changed("runtime"),
						StatusFilters:  statusFilters,
						StatusSet:      cmd.Flags().Changed("status"),
						PhaseFilters:   phaseFilters,
						PhaseSet:       cmd.Flags().Changed("phase"),
						Stale:          staleOnly,
						RuntimeStale:   runtimeStale,
						Unhealthy:      unhealthyOnly,
						Interrupt:      interrupt,
						Force:          force,
					})
				}
				target, err := resolveSendTarget(client, to, opts.Topology)
				if err != nil {
					return err
				}
				if !target.Valid() {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team send: %s\n", daemon.MailboxUnknownTargetMessage(to, target.Suggestions))
					return exitErr(2)
				}
				return renderScopedSendApplyCommand(cmd.OutOrStdout(), true, scopedSendApplyCommandOptions{
					BaseArgs:       []string{"agent-team", "send", to},
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
					AllowMissing:   allowMissing,
					Interrupt:      interrupt,
					Force:          force,
				})
			}
			if opts.selectingSet() {
				return runSendSelectionWithClient(cmd.OutOrStdout(), cmd.ErrOrStderr(), client, body, opts)
			}
			return runSendWithClient(cmd.OutOrStdout(), cmd.ErrOrStderr(), client, to, body, opts)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&from, "from", "(cli)", "Sender label recorded with the message.")
	cmd.Flags().StringVar(&message, "message", "", "Message text to send.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read message text from a file, or '-' for stdin.")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Send to every daemon-known instance.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Send to the most recently started daemon-known instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Send to the N most recently started daemon-known instances after other filters (0 = all).")
	cmd.Flags().StringSliceVar(&agents, "agent", nil, "Send to daemon-known instances for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimes, "runtime", nil, "Send to daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Send to daemon-known instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Send to daemon-known instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Send to daemon-known instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&runtimeStale, "runtime-stale", false, "Send to daemon-known running instances whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Send to daemon-known instances that are crashed, status-stale, or runtime-stale.")
	cmd.Flags().BoolVar(&allowMissing, "allow-missing", false, "Deprecated no-op; declared instances queue automatically.")
	cmd.Flags().BoolVar(&interrupt, "interrupt", false, "Deliver the message, gracefully stop the instance, and managed-resume the same captured session.")
	cmd.Flags().BoolVar(&force, "force", false, "With --interrupt, allow fresh fallback when no captured session can be resumed.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview matching recipients without appending mailbox messages.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching send apply command when the preview has actionable recipients. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each send result with a Go template, e.g. '{{.To}} {{.ID}}'.")
	return cmd
}

func sendMessageBody(flagValue, fileValue string, positional []string) (string, error) {
	return messageBodyWithFlagNames(flagValue, fileValue, positional, "--message", "--message-file")
}

func optionalMessageBodyWithFlagNames(flagValue, fileValue string, positional []string, flagName, fileFlagName string) (string, error) {
	if strings.TrimSpace(flagValue) == "" && strings.TrimSpace(fileValue) == "" && len(positional) == 0 {
		return "", nil
	}
	return messageBodyWithFlagNames(flagValue, fileValue, positional, flagName, fileFlagName)
}

func messageBodyWithFlagNames(flagValue, fileValue string, positional []string, flagName, fileFlagName string) (string, error) {
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
	if sources == 0 {
		return "", fmt.Errorf("message body is required")
	}
	if sources > 1 {
		return "", fmt.Errorf("provide message text using only one of positional args, %s, or %s", flagName, fileFlagName)
	}
	var body string
	switch {
	case strings.TrimSpace(fileValue) != "":
		data, err := readMessageFile(fileValue, fileFlagName)
		if err != nil {
			return "", err
		}
		body = string(data)
	case strings.TrimSpace(flagValue) != "":
		body = flagValue
	default:
		body = strings.Join(positional, " ")
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return "", fmt.Errorf("message body is required")
	}
	return body, nil
}

func readMessageFile(fileValue, fileFlagName string) ([]byte, error) {
	if strings.TrimSpace(fileValue) == "-" {
		body, err := io.ReadAll(sendMessageInput)
		if err != nil {
			return nil, fmt.Errorf("%s -: %w", fileFlagName, err)
		}
		return body, nil
	}
	body, err := os.ReadFile(filepath.Clean(fileValue))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fileFlagName, err)
	}
	return body, nil
}

type sendOptions struct {
	From            string
	All             bool
	Latest          bool
	Limit           int
	AgentFilters    []string
	RuntimeFilters  []string
	StatusFilters   []string
	PhaseFilters    []string
	PhaseByInstance map[string]string
	Stale           bool
	RuntimeStale    bool
	Unhealthy       bool
	StaleByInstance map[string]bool
	AllowMissing    bool
	Interrupt       bool
	Force           bool
	DryRun          bool
	JSON            bool
	Format          *template.Template
	Topology        *topology.Topology
	SkipValidation  bool
}

func (o sendOptions) selectingSet() bool {
	return o.All || o.Latest || o.Limit != 0 || len(o.AgentFilters) > 0 || len(o.RuntimeFilters) > 0 || len(o.StatusFilters) > 0 || len(o.PhaseFilters) > 0 || o.Stale || o.RuntimeStale || o.Unhealthy
}

type sendClient interface {
	Instances() ([]*daemon.Metadata, error)
	SendMessage(to, from, body string) (*messageResponse, error)
	InterruptMessage(to, from, body string, force bool) (*messageResponse, error)
}

func sendClientForTeamDir(teamDir string) (sendClient, error) {
	client, err := newDaemonClient(teamDir)
	if err == nil {
		return client, nil
	}
	if errors.Is(err, errDaemonNotRunning) {
		return localSendClient{daemonRoot: daemon.DaemonRoot(teamDir)}, nil
	}
	return nil, err
}

type localSendClient struct {
	daemonRoot string
}

func (c localSendClient) Instances() ([]*daemon.Metadata, error) {
	return daemon.ListMetadata(c.daemonRoot)
}

func (c localSendClient) SendMessage(to, from, body string) (*messageResponse, error) {
	msg := &daemon.Message{
		From: from,
		Body: body,
		TS:   time.Now().UTC(),
	}
	if err := daemon.AppendMessage(c.daemonRoot, to, msg); err != nil {
		return nil, err
	}
	return &messageResponse{
		Delivered: true,
		ID:        msg.ID,
		TS:        msg.TS,
	}, nil
}

func (c localSendClient) InterruptMessage(to, from, body string, force bool) (*messageResponse, error) {
	return nil, errors.New("agent-team send: --interrupt requires a running daemon")
}

type sendJSON struct {
	Delivered   bool      `json:"delivered"`
	Interrupted bool      `json:"interrupted,omitempty"`
	DryRun      bool      `json:"dry_run,omitempty"`
	To          string    `json:"to"`
	From        string    `json:"from"`
	ID          string    `json:"id"`
	TS          time.Time `json:"ts"`
	Note        string    `json:"note,omitempty"`
}

func runSendWithClient(stdout, stderr io.Writer, client sendClient, to, body string, opts sendOptions) error {
	to = strings.TrimSpace(to)
	body = strings.TrimSpace(body)
	from := strings.TrimSpace(opts.From)
	if from == "" {
		from = "(cli)"
	}
	if to == "" {
		fmt.Fprintln(stderr, "agent-team send: instance is required.")
		return exitErr(2)
	}
	if body == "" {
		fmt.Fprintln(stderr, "agent-team send: message body is required.")
		return exitErr(2)
	}
	var target daemon.MailboxTargetResolution
	var err error
	if !opts.SkipValidation {
		resolved, err := resolveSendTarget(client, to, opts.Topology)
		if err != nil {
			return err
		}
		if !resolved.Valid() {
			fmt.Fprintf(stderr, "agent-team send: %s\n", daemon.MailboxUnknownTargetMessage(to, resolved.Suggestions))
			return exitErr(2)
		}
		target = resolved
	}
	if opts.DryRun {
		row := sendDryRunRow(to, from)
		row.Interrupted = opts.Interrupt
		row.Note = target.Note
		if opts.JSON {
			return json.NewEncoder(stdout).Encode(row)
		}
		if opts.Format != nil {
			return renderSendFormat(stdout, []sendJSON{row}, opts.Format)
		}
		if opts.Interrupt {
			fmt.Fprintf(stdout, "  would-interrupt   %-20s%s\n", to, sendNoteSuffix(target.Note))
		} else {
			fmt.Fprintf(stdout, "  would-send   %-20s%s\n", to, sendNoteSuffix(target.Note))
		}
		return nil
	}
	var res *messageResponse
	if opts.Interrupt {
		res, err = client.InterruptMessage(to, from, body, opts.Force)
	} else {
		res, err = client.SendMessage(to, from, body)
	}
	if err != nil {
		return err
	}
	if opts.JSON {
		return json.NewEncoder(stdout).Encode(sendJSON{
			Delivered:   res.Delivered,
			Interrupted: opts.Interrupt || res.Interrupted,
			To:          to,
			From:        from,
			ID:          res.ID,
			TS:          res.TS,
			Note:        firstNonEmpty(res.Note, target.Note),
		})
	}
	row := sendJSON{
		Delivered:   res.Delivered,
		Interrupted: opts.Interrupt || res.Interrupted,
		To:          to,
		From:        from,
		ID:          res.ID,
		TS:          res.TS,
		Note:        firstNonEmpty(res.Note, target.Note),
	}
	if opts.Format != nil {
		return renderSendFormat(stdout, []sendJSON{row}, opts.Format)
	}
	if row.Interrupted {
		fmt.Fprintf(stdout, "  interrupted   %-20s id=%s%s\n", to, res.ID, sendNoteSuffix(row.Note))
	} else {
		fmt.Fprintf(stdout, "  sent   %-20s id=%s%s\n", to, res.ID, sendNoteSuffix(row.Note))
	}
	return nil
}

func runSendSelectionWithClient(stdout, stderr io.Writer, client sendClient, body string, opts sendOptions) error {
	body = strings.TrimSpace(body)
	if body == "" {
		fmt.Fprintln(stderr, "agent-team send: message body is required.")
		return exitErr(2)
	}
	if opts.Interrupt {
		fmt.Fprintln(stderr, "agent-team send: --interrupt requires a single instance target.")
		return exitErr(2)
	}
	if opts.Limit < 0 {
		fmt.Fprintln(stderr, "agent-team send: --last must be >= 0.")
		return exitErr(2)
	}
	if opts.Latest && opts.Limit > 0 {
		fmt.Fprintln(stderr, "agent-team send: choose one of --latest or --last.")
		return exitErr(2)
	}
	targets, err := selectSendTargets(client, opts)
	if err != nil {
		fmt.Fprintf(stderr, "agent-team send: %v\n", err)
		return exitErr(2)
	}
	if len(targets) == 0 {
		if opts.JSON {
			return json.NewEncoder(stdout).Encode([]sendJSON{})
		}
		if opts.Format != nil {
			return nil
		}
		fmt.Fprintln(stdout, "(no instances)")
		return nil
	}
	from := strings.TrimSpace(opts.From)
	if from == "" {
		from = "(cli)"
	}
	rows := make([]sendJSON, 0, len(targets))
	for _, target := range targets {
		if opts.DryRun {
			rows = append(rows, sendDryRunRow(target, from))
			if !opts.JSON && opts.Format == nil {
				fmt.Fprintf(stdout, "  would-send   %-20s\n", target)
			}
			continue
		}
		res, err := client.SendMessage(target, from, body)
		if err != nil {
			return err
		}
		row := sendJSON{
			Delivered: res.Delivered,
			To:        target,
			From:      from,
			ID:        res.ID,
			TS:        res.TS,
		}
		rows = append(rows, row)
		if !opts.JSON && opts.Format == nil {
			fmt.Fprintf(stdout, "  sent   %-20s id=%s\n", target, res.ID)
		}
	}
	if opts.JSON {
		return json.NewEncoder(stdout).Encode(rows)
	}
	if opts.Format != nil {
		return renderSendFormat(stdout, rows, opts.Format)
	}
	return nil
}

func sendDryRunRow(to, from string) sendJSON {
	return sendJSON{
		Delivered: false,
		DryRun:    true,
		To:        to,
		From:      from,
		TS:        time.Now().UTC(),
	}
}

func parseSendFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("send-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderSendFormat(w io.Writer, rows []sendJSON, tmpl *template.Template) error {
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

type scopedSendApplyCommandOptions struct {
	BaseArgs       []string
	RepoFlag       string
	Repo           string
	RepoSet        bool
	From           string
	FromSet        bool
	Message        string
	MessageSet     bool
	MessageFile    string
	MessageFileSet bool
	Positional     []string
	All            bool
	Latest         bool
	Last           int
	AgentFilters   []string
	AgentSet       bool
	StatusFilters  []string
	StatusSet      bool
	RuntimeFilters []string
	RuntimeSet     bool
	PhaseFilters   []string
	PhaseSet       bool
	Stale          bool
	RuntimeStale   bool
	Unhealthy      bool
	AllowMissing   bool
	Interrupt      bool
	Force          bool
}

func renderScopedSendApplyCommand(w io.Writer, hasRecipients bool, opts scopedSendApplyCommandOptions) error {
	if !hasRecipients {
		return nil
	}
	_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(scopedSendApplyCommandArgs(opts)), " "))
	return err
}

func scopedSendApplyCommandArgs(opts scopedSendApplyCommandOptions) []string {
	args := append([]string{}, opts.BaseArgs...)
	messageSet := opts.MessageSet && strings.TrimSpace(opts.Message) != ""
	messageFileSet := opts.MessageFileSet && strings.TrimSpace(opts.MessageFile) != ""
	if opts.RepoSet && strings.TrimSpace(opts.Repo) != "" {
		flag := strings.TrimSpace(opts.RepoFlag)
		if flag == "" {
			flag = "repo"
		}
		args = append(args, "--"+flag, opts.Repo)
	}
	if opts.FromSet && strings.TrimSpace(opts.From) != "" {
		args = append(args, "--from", opts.From)
	}
	if messageSet {
		args = append(args, "--message", opts.Message)
	}
	if messageFileSet {
		args = append(args, "--message-file", opts.MessageFile)
	}
	if opts.All {
		args = append(args, "--all")
	}
	if opts.Latest {
		args = append(args, "--latest")
	}
	if opts.Last > 0 {
		args = append(args, "--last", fmt.Sprint(opts.Last))
	}
	if opts.AgentSet {
		if filters := normalizeCommandList(opts.AgentFilters); len(filters) > 0 {
			args = append(args, "--agent", strings.Join(filters, ","))
		}
	}
	if opts.StatusSet {
		if filters := normalizeCommandList(opts.StatusFilters); len(filters) > 0 {
			args = append(args, "--status", strings.Join(filters, ","))
		}
	}
	if opts.RuntimeSet {
		if filters := normalizeCommandList(opts.RuntimeFilters); len(filters) > 0 {
			args = append(args, "--runtime", strings.Join(filters, ","))
		}
	}
	if opts.PhaseSet {
		if filters := normalizeCommandList(opts.PhaseFilters); len(filters) > 0 {
			args = append(args, "--phase", strings.Join(filters, ","))
		}
	}
	if opts.Stale {
		args = append(args, "--stale")
	}
	if opts.RuntimeStale {
		args = append(args, "--runtime-stale")
	}
	if opts.Unhealthy {
		args = append(args, "--unhealthy")
	}
	if opts.AllowMissing {
		args = append(args, "--allow-missing")
	}
	if opts.Interrupt {
		args = append(args, "--interrupt")
	}
	if opts.Force {
		args = append(args, "--force")
	}
	if !messageSet && !messageFileSet && len(opts.Positional) > 0 {
		args = append(args, opts.Positional...)
	}
	return args
}

func normalizeCommandList(raw []string) []string {
	seen := map[string]bool{}
	values := make([]string, 0, len(raw))
	for _, item := range raw {
		for _, part := range strings.Split(item, ",") {
			part = strings.TrimSpace(part)
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			values = append(values, part)
		}
	}
	return values
}

func selectSendTargets(client sendClient, opts sendOptions) ([]string, error) {
	agents := lifecycleAgentFilterSet(opts.AgentFilters)
	if len(opts.AgentFilters) > 0 && len(agents) == 0 {
		return nil, errors.New("--agent requires at least one non-empty agent")
	}
	runtimes, err := sendRuntimeFilterSet(opts.RuntimeFilters)
	if err != nil {
		return nil, err
	}
	statuses, err := lifecycleStatusFilterSet(opts.StatusFilters)
	if err != nil {
		return nil, err
	}
	var phases map[string]bool
	if len(opts.PhaseFilters) > 0 {
		phases, err = lifecyclePhaseFilterSet(opts.PhaseFilters)
		if err != nil {
			return nil, err
		}
	}
	metas, err := client.Instances()
	if err != nil {
		return nil, err
	}
	filtered := make([]*daemon.Metadata, 0, len(metas))
	for _, meta := range metas {
		if len(agents) > 0 && !agents[meta.Agent] {
			continue
		}
		if len(runtimes) > 0 && !runtimes[sendRuntimeKey(meta)] {
			continue
		}
		if len(statuses) > 0 && !statuses[sendStatusKey(meta)] {
			continue
		}
		if len(phases) > 0 && !phases[sendPhaseForInstance(opts.PhaseByInstance, meta.Instance)] {
			continue
		}
		if opts.Stale && !opts.StaleByInstance[meta.Instance] {
			continue
		}
		if opts.RuntimeStale && !runtimeResumeMetadataIsStale(meta) {
			continue
		}
		if opts.Unhealthy && sendStatusKey(meta) != string(daemon.StatusCrashed) && !opts.StaleByInstance[meta.Instance] && !runtimeResumeMetadataIsStale(meta) {
			continue
		}
		filtered = append(filtered, meta)
	}
	if opts.Latest {
		filtered = latestSendTargetMetasLimit(filtered, 1)
	} else if opts.Limit > 0 {
		filtered = latestSendTargetMetasLimit(filtered, opts.Limit)
	}
	names := make([]string, 0, len(filtered))
	for _, meta := range filtered {
		names = append(names, meta.Instance)
	}
	if opts.Latest || opts.Limit > 0 {
		return names, nil
	}
	sort.Strings(names)
	return names, nil
}

func sendRuntimeFilterSet(filters []string) (map[string]bool, error) {
	if len(filters) == 0 {
		return nil, nil
	}
	out := map[string]bool{}
	for _, raw := range splitFilterValues(filters) {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		kind, err := runtimebin.ParseKind(raw)
		if err != nil {
			return nil, fmt.Errorf("unknown --runtime %q (want claude or codex)", raw)
		}
		out[string(kind)] = true
	}
	if len(out) == 0 {
		return nil, errors.New("--runtime requires at least one non-empty runtime")
	}
	return out, nil
}

func sendRuntimeKey(meta *daemon.Metadata) string {
	if meta == nil {
		return "unknown"
	}
	runtime := strings.ToLower(strings.TrimSpace(meta.Runtime))
	if runtime == "" {
		return "unknown"
	}
	return runtime
}

func latestSendTargetMetasLimit(metas []*daemon.Metadata, limit int) []*daemon.Metadata {
	if limit <= 0 || len(metas) == 0 {
		return metas
	}
	out := append([]*daemon.Metadata(nil), metas...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.StartedAt.Equal(b.StartedAt) {
			return psTimeAfter(a.StartedAt, b.StartedAt)
		}
		return a.Instance < b.Instance
	})
	if limit < len(out) {
		out = out[:limit]
	}
	return out
}

func sendStatusKey(meta *daemon.Metadata) string {
	if meta == nil || meta.Status == "" {
		return "unknown"
	}
	return string(meta.Status)
}

func sendPhaseByInstance(teamDir string, now time.Time) map[string]string {
	rows := loadInstanceRows(teamDir, loadAgentNames(teamDir), now)
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Instance] = psPhaseKey(row)
	}
	return out
}

func sendPhaseForInstance(phaseByInstance map[string]string, instance string) string {
	if phaseByInstance == nil {
		return "unknown"
	}
	return psPhaseKey(instanceRow{Phase: phaseByInstance[instance]})
}

func resolveSendTarget(client sendClient, to string, topo *topology.Topology) (daemon.MailboxTargetResolution, error) {
	metas, err := client.Instances()
	if err != nil {
		return daemon.MailboxTargetResolution{}, err
	}
	return daemon.ResolveMailboxTarget(metas, topo, to), nil
}

func sendNoteSuffix(note string) string {
	note = strings.TrimSpace(note)
	if note == "" {
		return ""
	}
	return " (" + note + ")"
}
