package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/agent-team-project/agent-team/internal/addressing"
	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/resource"
	"github.com/spf13/cobra"
)

type readOptions struct {
	Target string
	JSON   bool
}

func newReadCmd() *cobra.Command {
	var opts readOptions
	cmd := &cobra.Command{
		Use:   "read <agt-uri>",
		Short: "Read a daemon resource by canonical agt:// URI.",
		Long: "Read a daemon-owned resource by canonical agt:// URI through the daemon API. " +
			"The command never falls back to reading `.agent_team/` files directly.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Target = strings.TrimSpace(opts.Target)
			return runReadCommand(cmd, opts, args[0])
		},
	}
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Emit the full machine-readable resource envelope.")
	return cmd
}

func runReadCommand(cmd *cobra.Command, opts readOptions, rawURI string) error {
	teamDir, err := resolveTeamDir(cmd, opts.Target)
	if err != nil {
		return err
	}
	client, err := daemonClientForResourceURI(teamDir, rawURI)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team read: %v\n", err)
		return exitErr(1)
	}
	read, err := client.Resource(rawURI)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team read: %v\n", err)
		return exitErr(1)
	}
	if err := renderResourceRead(cmd.OutOrStdout(), read, opts.JSON); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team read: %v\n", err)
		return exitErr(1)
	}
	return nil
}

func daemonClientForResourceURI(teamDir, rawURI string) (*daemonClient, error) {
	parsed, err := resource.Parse(rawURI)
	if err != nil {
		return nil, err
	}
	entries, err := addressing.View(teamDir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		entryParsed, err := resource.Parse(entry.URI)
		if err != nil || entryParsed.DeploymentID != parsed.DeploymentID {
			continue
		}
		if strings.TrimSpace(entry.TeamDir) == "" {
			return nil, fmt.Errorf("deployment %q has no local daemon endpoint", entry.Name)
		}
		if entry.Source == addressing.DeploymentSourceSelf {
			client, err := newDaemonClient(entry.TeamDir)
			if err != nil {
				return nil, err
			}
			return client, nil
		}
		switch entry.Transport {
		case "http":
			return newDaemonHTTPURLClientWithTokenFile(entry.TeamDir, entry.Endpoint, 0, daemon.OperatorTokenPath(entry.TeamDir)), nil
		case "unix":
			return newDaemonUnixSocketClient(entry.TeamDir, entry.Endpoint, 0), nil
		default:
			return nil, fmt.Errorf("deployment %q has no local daemon endpoint", entry.Name)
		}
	}
	return nil, fmt.Errorf("deployment %q is not in the registry view", parsed.DeploymentID)
}

func renderResourceRead(w io.Writer, read *resourceReadResponse, envelope bool) error {
	if read == nil {
		_, err := fmt.Fprintln(w, "null")
		return err
	}
	if envelope {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(read)
	}
	body := bytes.TrimSpace(read.Data)
	if len(body) == 0 {
		_, err := fmt.Fprintln(w, "null")
		return err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, body, "", "  "); err == nil {
		pretty.WriteByte('\n')
		_, err = w.Write(pretty.Bytes())
		return err
	}
	_, err := w.Write(append(append([]byte{}, body...), '\n'))
	return err
}
