package cli

import (
	"encoding/json"
	"fmt"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/spf13/cobra"
)

const managedBuildAttestationSchema = "agent-team.managed-cli-attestation.v1"

type managedBuildAttestation struct {
	Schema    string         `json:"schema"`
	Kind      string         `json:"kind"`
	CLI       buildinfo.Info `json:"cli"`
	CLIHeader string         `json:"cli_header"`
}

// newBuildAttestationCmd is a hidden, read-only bootstrap surface used by
// bundled skills while deciding which PATH candidate is daemon-comparable.
// Generated shims expose their own launch-bound attestation before Cobra; a
// native managed CLI reaches this command directly.
func newBuildAttestationCmd() *cobra.Command {
	var headerOnly bool
	cmd := &cobra.Command{
		Use:    "__build-attestation",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			build := BuildInfo()
			if headerOnly {
				fmt.Fprintln(cmd.OutOrStdout(), build.HeaderValue())
				return nil
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(managedBuildAttestation{
				Schema:    managedBuildAttestationSchema,
				Kind:      "managed_cli",
				CLI:       build,
				CLIHeader: build.HeaderValue(),
			})
		},
	}
	cmd.Flags().BoolVar(&headerOnly, "header", false, "Print only the serialized immutable build header.")
	return cmd
}
