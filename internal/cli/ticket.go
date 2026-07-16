package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/agent-team-project/agent-team/internal/pmprovider"
	"github.com/spf13/cobra"
)

type ticketCommandOptions struct {
	repo     string
	jsonOut  bool
	title    string
	body     string
	bodyFile string
	state    string
	labels   []string
}

const githubTicketReferenceHelp = "For GitHub, <ticket> accepts GH-N (case-insensitive), #N, N, owner/repo#N, owner/repo/issues/N, https://github.com/owner/repo/issues/N, or https://api.github.com/repos/owner/repo/issues/N. GH-N, #N, and N use the configured default owner/repo."

func newTicketCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ticket",
		Short: "Create and update PM tickets through the configured provider.",
		Long: "Create and update PM tickets through `[pm].provider`. " +
			"Supported providers are Linear and GitHub.\n\n" + githubTicketReferenceHelp,
	}
	cmd.AddCommand(newTicketCreateCmd())
	cmd.AddCommand(newTicketUpdateCmd())
	cmd.AddCommand(newTicketCommentCmd())
	cmd.AddCommand(newTicketCloseCmd())
	return cmd
}

func newTicketCreateCmd() *cobra.Command {
	opts := newTicketCommandOptions()
	cmd := &cobra.Command{
		Use:   "create --title <title>",
		Short: "Create a ticket using the configured PM provider.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := ticketBody(opts)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team ticket create: %v\n", err)
				return exitErr(2)
			}
			if strings.TrimSpace(opts.title) == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team ticket create: --title is required.")
				return exitErr(2)
			}
			return runTicketAction(cmd, opts, pmprovider.TicketRequest{
				Action: pmprovider.TicketCreate,
				Title:  opts.title,
				Body:   body,
				State:  opts.state,
				Labels: opts.labels,
				Actor:  "cli",
			})
		},
	}
	addTicketCommonFlags(cmd, opts)
	cmd.Flags().StringVar(&opts.title, "title", "", "Ticket title.")
	cmd.Flags().StringVar(&opts.state, "state", "", "Initial provider state.")
	cmd.Flags().StringSliceVar(&opts.labels, "label", nil, "Label to add. Can repeat or comma-separate.")
	return cmd
}

func newTicketUpdateCmd() *cobra.Command {
	opts := newTicketCommandOptions()
	cmd := &cobra.Command{
		Use:   "update <ticket>",
		Short: "Update a ticket using the configured PM provider.",
		Long:  "Update a ticket using the configured PM provider.\n\n" + githubTicketReferenceHelp,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := ticketBody(opts)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team ticket update: %v\n", err)
				return exitErr(2)
			}
			if strings.TrimSpace(opts.title) == "" && strings.TrimSpace(body) == "" && strings.TrimSpace(opts.state) == "" && len(cleanTicketLabels(opts.labels)) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team ticket update: provide --title, --body, --body-file, --state, or --label.")
				return exitErr(2)
			}
			return runTicketAction(cmd, opts, pmprovider.TicketRequest{
				Action: pmprovider.TicketUpdate,
				Ticket: args[0],
				Title:  opts.title,
				Body:   body,
				State:  opts.state,
				Labels: opts.labels,
				Actor:  "cli",
			})
		},
	}
	addTicketCommonFlags(cmd, opts)
	cmd.Flags().StringVar(&opts.title, "title", "", "New ticket title.")
	cmd.Flags().StringVar(&opts.state, "state", "", "Provider state to set.")
	cmd.Flags().StringSliceVar(&opts.labels, "label", nil, "Label to add. Can repeat or comma-separate.")
	return cmd
}

func newTicketCommentCmd() *cobra.Command {
	opts := newTicketCommandOptions()
	cmd := &cobra.Command{
		Use:   "comment <ticket>",
		Short: "Comment on a ticket using the configured PM provider.",
		Long:  "Comment on a ticket using the configured PM provider.\n\n" + githubTicketReferenceHelp,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := ticketBody(opts)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team ticket comment: %v\n", err)
				return exitErr(2)
			}
			if strings.TrimSpace(body) == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team ticket comment: --body or --body-file is required.")
				return exitErr(2)
			}
			return runTicketAction(cmd, opts, pmprovider.TicketRequest{
				Action: pmprovider.TicketComment,
				Ticket: args[0],
				Body:   body,
				Actor:  "cli",
			})
		},
	}
	addTicketCommonFlags(cmd, opts)
	return cmd
}

func newTicketCloseCmd() *cobra.Command {
	opts := newTicketCommandOptions()
	cmd := &cobra.Command{
		Use:   "close <ticket>",
		Short: "Close a ticket using the configured PM provider.",
		Long:  "Close a ticket using the configured PM provider.\n\n" + githubTicketReferenceHelp,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := ticketBody(opts)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team ticket close: %v\n", err)
				return exitErr(2)
			}
			return runTicketAction(cmd, opts, pmprovider.TicketRequest{
				Action: pmprovider.TicketClose,
				Ticket: args[0],
				Body:   body,
				State:  opts.state,
				Actor:  "cli",
			})
		},
	}
	addTicketCommonFlags(cmd, opts)
	cmd.Flags().StringVar(&opts.state, "state", "", "Provider state to use for close. Required for Linear unless [linear].closed_state is configured.")
	return cmd
}

func newTicketCommandOptions() *ticketCommandOptions {
	return &ticketCommandOptions{repo: "."}
}

func addTicketCommonFlags(cmd *cobra.Command, opts *ticketCommandOptions) {
	cmd.Flags().StringVar(&opts.repo, "repo", opts.repo, repoFlagHelp)
	cmd.Flags().BoolVar(&opts.jsonOut, "json", false, "Emit ticket result as JSON.")
	cmd.Flags().StringVar(&opts.body, "body", "", "Ticket body or comment text.")
	cmd.Flags().StringVar(&opts.bodyFile, "body-file", "", "Read ticket body or comment text from this file. Use '-' for stdin.")
}

func ticketBody(opts *ticketCommandOptions) (string, error) {
	body := strings.TrimSpace(opts.body)
	bodyFile := strings.TrimSpace(opts.bodyFile)
	if body != "" && bodyFile != "" {
		return "", fmt.Errorf("--body cannot be combined with --body-file")
	}
	if bodyFile == "" {
		return body, nil
	}
	var data []byte
	var err error
	if bodyFile == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(bodyFile)
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func cleanTicketLabels(labels []string) []string {
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		if label = strings.TrimSpace(label); label != "" {
			out = append(out, label)
		}
	}
	return out
}

func runTicketAction(cmd *cobra.Command, opts *ticketCommandOptions, req pmprovider.TicketRequest) error {
	teamDir, err := resolveTeamDir(cmd, opts.repo)
	if err != nil {
		return err
	}
	result := pmprovider.ApplyTicket(context.Background(), teamDir, req)
	if opts.jsonOut {
		if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
			return err
		}
	} else {
		renderTicketResult(cmd.OutOrStdout(), result)
	}
	if result.Error != "" || result.Skipped {
		if result.Error != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team ticket %s: %s\n", req.Action, result.Error)
		} else {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team ticket %s: %s\n", req.Action, result.Message)
		}
		return exitErr(1)
	}
	return nil
}

func renderTicketResult(w io.Writer, result pmprovider.TicketResult) {
	message := strings.TrimSpace(result.Message)
	if message == "" {
		message = "ticket action completed"
	}
	if result.Issue != "" {
		fmt.Fprintf(w, "%s: %s\n", message, result.Issue)
	} else {
		fmt.Fprintln(w, message)
	}
	if result.URL != "" {
		fmt.Fprintln(w, result.URL)
	}
}
