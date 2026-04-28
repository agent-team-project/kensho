package cli

import (
	"github.com/spf13/cobra"
)

// newTemplateCmd is the placeholder for the templates-as-images verb. Filled
// in by the Half B commit; stubbed here so Half A's root.go compiles.
func newTemplateCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "template",
		Short:  "Manage templates (TODO: filled in by Half B).",
		Hidden: true,
	}
}
