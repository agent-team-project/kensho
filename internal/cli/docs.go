package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

func newDocsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Generate developer documentation from the command tree.",
	}
	cmd.AddCommand(newDocsCLICmd())
	return cmd
}

func newDocsCLICmd() *cobra.Command {
	var (
		output        string
		includeHidden bool
	)
	cmd := &cobra.Command{
		Use:   "cli",
		Short: "Generate a markdown CLI reference.",
		Long: "Generate a markdown CLI reference from the live Cobra command tree. " +
			"Use this to refresh docs after adding or changing commands and flags.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := generateCLIReferenceMarkdown(cmd.Root(), includeHidden)
			if err != nil {
				return err
			}
			if strings.TrimSpace(output) == "" || output == "-" {
				_, err := io.Copy(cmd.OutOrStdout(), bytes.NewReader(body))
				return err
			}
			path, err := filepath.Abs(output)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return fmt.Errorf("create docs output dir: %w", err)
			}
			if err := os.WriteFile(path, body, 0o644); err != nil {
				return fmt.Errorf("write docs output: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Wrote CLI reference to %s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "Write markdown to this path instead of stdout. Use '-' for stdout.")
	cmd.Flags().BoolVar(&includeHidden, "include-hidden", false, "Include hidden commands.")
	return cmd
}

func generateCLIReferenceMarkdown(root *cobra.Command, includeHidden bool) ([]byte, error) {
	if root == nil {
		return nil, fmt.Errorf("root command is required")
	}
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()

	var b strings.Builder
	b.WriteString("# agent-team CLI Reference\n\n")
	b.WriteString("Generated from the live Cobra command tree. Run `agent-team docs cli --output docs/reference/cli.generated.md` after changing commands or flags.\n\n")

	for _, cmd := range commandReferenceList(root, includeHidden) {
		renderCommandReference(&b, cmd, includeHidden)
	}
	return []byte(b.String()), nil
}

func commandReferenceList(root *cobra.Command, includeHidden bool) []*cobra.Command {
	var out []*cobra.Command
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd == nil {
			return
		}
		if cmd.Hidden && !includeHidden {
			return
		}
		out = append(out, cmd)
		children := append([]*cobra.Command(nil), cmd.Commands()...)
		sort.SliceStable(children, func(i, j int) bool {
			return children[i].Name() < children[j].Name()
		})
		for _, child := range children {
			walk(child)
		}
	}
	walk(root)
	return out
}

func renderCommandReference(b *strings.Builder, cmd *cobra.Command, includeHidden bool) {
	fmt.Fprintf(b, "## `%s`\n\n", cmd.CommandPath())
	if short := strings.TrimSpace(cmd.Short); short != "" {
		fmt.Fprintf(b, "%s\n\n", short)
	}
	if long := strings.TrimSpace(cmd.Long); long != "" && long != strings.TrimSpace(cmd.Short) {
		fmt.Fprintf(b, "%s\n\n", long)
	}
	fmt.Fprintf(b, "```text\n%s\n```\n\n", cmd.UseLine())
	if aliases := visibleAliases(cmd); len(aliases) > 0 {
		fmt.Fprintf(b, "Aliases: `%s`\n\n", strings.Join(aliases, "`, `"))
	}
	renderFlagUsageReference(b, "Flags", cmd.LocalNonPersistentFlags().FlagUsages())
	renderFlagUsageReference(b, "Persistent Flags", cmd.PersistentFlags().FlagUsages())
	renderFlagUsageReference(b, "Inherited Flags", cmd.InheritedFlags().FlagUsages())
	if children := visibleSubcommands(cmd, includeHidden); len(children) > 0 {
		b.WriteString("Subcommands:\n\n")
		for _, child := range children {
			fmt.Fprintf(b, "- `%s` - %s\n", child.CommandPath(), tableCell(child.Short))
		}
		b.WriteString("\n")
	}
}

func visibleAliases(cmd *cobra.Command) []string {
	var aliases []string
	for _, alias := range cmd.Aliases {
		alias = strings.TrimSpace(alias)
		if alias != "" {
			aliases = append(aliases, alias)
		}
	}
	sort.Strings(aliases)
	return aliases
}

func visibleSubcommands(cmd *cobra.Command, includeHidden bool) []*cobra.Command {
	children := append([]*cobra.Command(nil), cmd.Commands()...)
	children = slicesWithoutHiddenCommands(children, includeHidden)
	sort.SliceStable(children, func(i, j int) bool {
		return children[i].Name() < children[j].Name()
	})
	return children
}

func slicesWithoutHiddenCommands(commands []*cobra.Command, includeHidden bool) []*cobra.Command {
	out := commands[:0]
	for _, cmd := range commands {
		if cmd == nil {
			continue
		}
		if cmd.Hidden && !includeHidden {
			continue
		}
		out = append(out, cmd)
	}
	return out
}

func renderFlagUsageReference(b *strings.Builder, title, usage string) {
	usage = strings.TrimRight(usage, "\n")
	if strings.TrimSpace(usage) == "" {
		return
	}
	fmt.Fprintf(b, "%s:\n\n", title)
	b.WriteString("```text\n")
	b.WriteString(usage)
	b.WriteString("\n```\n")
	b.WriteString("\n")
}

func tableCell(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", "\\|")
	if value == "" {
		return "-"
	}
	return value
}
