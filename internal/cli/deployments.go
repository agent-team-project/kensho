package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"text/template"

	"github.com/agent-team-project/agent-team/internal/addressing"
	"github.com/spf13/cobra"
)

type deploymentsListOptions struct {
	Target string
	JSON   bool
	Format string
}

type deploymentsResolveOptions struct {
	Target string
	JSON   bool
	Format string
}

func newDeploymentsCmd() *cobra.Command {
	var opts deploymentsListOptions
	cmd := &cobra.Command{
		Use:   "deployments",
		Short: "Read the deployment address registry view.",
		Long: "Read the deployment address registry view projected from existing repo state. " +
			"This command does not create or update a registry file.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeploymentsLsCommand(cmd, opts, "agent-team deployments")
		},
	}
	addDeploymentsListFlags(cmd, &opts, ".")
	cmd.AddCommand(newDeploymentsLsCmd())
	cmd.AddCommand(newDeploymentsResolveCmd())
	return cmd
}

func newDeploymentsLsCmd() *cobra.Command {
	var opts deploymentsListOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List deployment names and their canonical resource URIs.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeploymentsLsCommand(cmd, opts, "agent-team deployments ls")
		},
	}
	addDeploymentsListFlags(cmd, &opts, ".")
	return cmd
}

func newDeploymentsResolveCmd() *cobra.Command {
	var opts deploymentsResolveOptions
	cmd := &cobra.Command{
		Use:   "resolve <name-or-uri>",
		Short: "Resolve a deployment name to its canonical resource URI.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.Format != "" && opts.JSON {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team deployments resolve: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseDeploymentFormat(opts.Format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team deployments resolve: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, opts.Target)
			if err != nil {
				return err
			}
			entry, err := addressing.Resolve(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team deployments resolve: %v\n", err)
				return exitErr(1)
			}
			return renderDeploymentResolve(cmd.OutOrStdout(), entry, opts.JSON, tmpl)
		},
	}
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&opts.Format, "format", "", "Render the resolved deployment with a Go template, e.g. '{{.URI}}'.")
	return cmd
}

func addDeploymentsListFlags(cmd *cobra.Command, opts *deploymentsListOptions, cwd string) {
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&opts.Format, "format", "", "Render each deployment with a Go template, e.g. '{{.Name}} {{.URI}}'.")
}

func runDeploymentsLsCommand(cmd *cobra.Command, opts deploymentsListOptions, commandName string) error {
	if opts.Format != "" && opts.JSON {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: --format cannot be combined with --json.\n", commandName)
		return exitErr(2)
	}
	tmpl, err := parseDeploymentFormat(opts.Format)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", commandName, err)
		return exitErr(2)
	}
	teamDir, err := resolveTeamDir(cmd, opts.Target)
	if err != nil {
		return err
	}
	entries, err := addressing.View(teamDir)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", commandName, err)
		return exitErr(1)
	}
	return renderDeploymentsList(cmd.OutOrStdout(), entries, opts.JSON, tmpl)
}

func renderDeploymentsList(w io.Writer, entries []addressing.DeploymentEntry, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(entries)
	}
	if tmpl != nil {
		for _, entry := range entries {
			if err := renderDeploymentFormat(w, entry, tmpl); err != nil {
				return err
			}
		}
		return nil
	}
	if len(entries) == 0 {
		fmt.Fprintln(w, "(no deployment resources found)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tURI\tSOURCE\tTRANSPORT\tENDPOINT")
	for _, entry := range entries {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			entry.Name,
			entry.URI,
			entry.Source,
			emptyDash(entry.Transport),
			emptyDash(entry.Endpoint),
		)
	}
	_ = tw.Flush()
	return nil
}

func renderDeploymentResolve(w io.Writer, entry addressing.DeploymentEntry, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(entry)
	}
	if tmpl != nil {
		return renderDeploymentFormat(w, entry, tmpl)
	}
	_, err := fmt.Fprintln(w, entry.URI)
	return err
}

func parseDeploymentFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("deployment-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderDeploymentFormat(w io.Writer, entry addressing.DeploymentEntry, tmpl *template.Template) error {
	if err := tmpl.Execute(w, entry); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}
