package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
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
	cmd.AddCommand(newDocsSiteCmd())
	return cmd
}

func newDocsCLICmd() *cobra.Command {
	var (
		output        string
		check         string
		includeHidden bool
	)
	cmd := &cobra.Command{
		Use:   "cli",
		Short: "Generate a markdown CLI reference.",
		Long: "Generate a markdown CLI reference from the live Cobra command tree. " +
			"Use this to refresh docs after adding or changing commands and flags.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(output) != "" && strings.TrimSpace(check) != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team docs cli: --output cannot be combined with --check.")
				return exitErr(2)
			}
			body, err := generateCLIReferenceMarkdown(cmd.Root(), includeHidden)
			if err != nil {
				return err
			}
			if strings.TrimSpace(check) != "" {
				return checkCLIReferenceMarkdown(cmd, check, body)
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
	cmd.Flags().StringVar(&check, "check", "", "Exit non-zero if this markdown file does not match generated output.")
	cmd.Flags().BoolVar(&includeHidden, "include-hidden", false, "Include hidden commands.")
	return cmd
}

func newDocsSiteCmd() *cobra.Command {
	var (
		commands bool
		jsonOut  bool
	)
	cmd := &cobra.Command{
		Use:   "site",
		Short: "Show developer docs website commands.",
		Long:  "Show the local VitePress developer docs website commands and paths for this source checkout.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team docs site: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			info := collectDocsSiteInfo()
			if commands {
				for _, command := range info.Commands {
					fmt.Fprintln(cmd.OutOrStdout(), command)
				}
				return nil
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(info)
			}
			return renderDocsSiteInfo(cmd.OutOrStdout(), info)
		},
	}
	cmd.Flags().BoolVar(&commands, "commands", false, "Print only shell commands for dev, build, and preview.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit docs site paths and commands as JSON.")
	return cmd
}

type docsSiteInfo struct {
	Root           string   `json:"root"`
	DocsDir        string   `json:"docs_dir"`
	Config         string   `json:"config"`
	PackageJSON    string   `json:"package_json"`
	CLIReference   string   `json:"cli_reference"`
	Available      bool     `json:"available"`
	DevURL         string   `json:"dev_url"`
	DevCommand     string   `json:"dev_command"`
	BuildCommand   string   `json:"build_command"`
	PreviewCommand string   `json:"preview_command"`
	Commands       []string `json:"commands"`
}

func collectDocsSiteInfo() docsSiteInfo {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	root := findDocsSiteRoot(cwd)
	info := docsSiteInfo{
		Root:         root,
		DocsDir:      filepath.Join(root, "docs"),
		Config:       filepath.Join(root, "docs", ".vitepress", "config.mts"),
		PackageJSON:  filepath.Join(root, "package.json"),
		CLIReference: filepath.Join(root, "docs", "reference", "cli.generated.md"),
		DevURL:       "http://localhost:5173/",
	}
	info.Available = fileExists(info.PackageJSON) && fileExists(info.Config)
	info.DevCommand = docsSiteShellCommand(root, "npm", "run", "docs:dev")
	info.BuildCommand = docsSiteShellCommand(root, "npm", "run", "docs:build")
	info.PreviewCommand = docsSiteShellCommand(root, "npm", "run", "docs:preview")
	info.Commands = []string{info.DevCommand, info.BuildCommand, info.PreviewCommand}
	return info
}

func findDocsSiteRoot(start string) string {
	abs, err := filepath.Abs(start)
	if err != nil {
		return start
	}
	if eval, err := filepath.EvalSymlinks(abs); err == nil {
		abs = eval
	}
	for {
		if fileExists(filepath.Join(abs, "package.json")) && fileExists(filepath.Join(abs, "docs", ".vitepress", "config.mts")) {
			return abs
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return filepath.Clean(start)
		}
		abs = parent
	}
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func docsSiteShellCommand(root string, args ...string) string {
	return strings.Join(shellQuoteArgs([]string{"cd", root}), " ") + " && " + strings.Join(shellQuoteArgs(args), " ")
}

func renderDocsSiteInfo(w io.Writer, info docsSiteInfo) error {
	fmt.Fprintln(w, "Developer docs site")
	fmt.Fprintf(w, "root:          %s\n", info.Root)
	fmt.Fprintf(w, "available:     %t\n", info.Available)
	fmt.Fprintf(w, "docs:          %s\n", info.DocsDir)
	fmt.Fprintf(w, "config:        %s\n", info.Config)
	fmt.Fprintf(w, "cli_reference: %s\n", info.CLIReference)
	fmt.Fprintf(w, "dev_url:       %s\n", info.DevURL)
	fmt.Fprintln(w, "commands:")
	for _, command := range info.Commands {
		fmt.Fprintf(w, "  %s\n", command)
	}
	return nil
}

func checkCLIReferenceMarkdown(cmd *cobra.Command, path string, want []byte) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	got, err := os.ReadFile(abs)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team docs cli: cannot read %s: %v\n", abs, err)
		return exitErr(1)
	}
	if !bytes.Equal(got, want) {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team docs cli: %s is stale; rerun `agent-team docs cli --output %s`.\n", abs, abs)
		return exitErr(1)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "CLI reference is up to date: %s\n", abs)
	return nil
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
		fmt.Fprintf(b, "%s\n\n", markdownProse(short))
	}
	if long := strings.TrimSpace(cmd.Long); long != "" && long != strings.TrimSpace(cmd.Short) {
		fmt.Fprintf(b, "%s\n\n", markdownProse(long))
	}
	fmt.Fprintf(b, "```text\n%s\n```\n\n", cmd.UseLine())
	renderFlagUsageReference(b, "Flags", cmd.LocalNonPersistentFlags().FlagUsages())
	renderFlagUsageReference(b, "Persistent Flags", cmd.PersistentFlags().FlagUsages())
	renderFlagUsageReference(b, "Inherited Flags", cmd.InheritedFlags().FlagUsages())
	if children := visibleSubcommands(cmd, includeHidden); len(children) > 0 {
		b.WriteString("Subcommands:\n\n")
		for _, child := range children {
			fmt.Fprintf(b, "- `%s` - %s\n", child.CommandPath(), markdownTableCell(child.Short))
		}
		b.WriteString("\n")
	}
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
	usage = normalizeGeneratedFlagUsage(usage)
	fmt.Fprintf(b, "%s:\n\n", title)
	b.WriteString("```text\n")
	b.WriteString(usage)
	b.WriteString("\n```\n")
	b.WriteString("\n")
}

func normalizeGeneratedFlagUsage(usage string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return usage
	}
	candidates := []string{cwd}
	if abs, err := filepath.Abs(cwd); err == nil && abs != cwd {
		candidates = append(candidates, abs)
	}
	if eval, err := filepath.EvalSymlinks(cwd); err == nil && eval != cwd {
		candidates = append(candidates, eval)
	}
	for _, candidate := range candidates {
		usage = strings.ReplaceAll(usage, candidate, "<repo>")
	}
	return usage
}

func markdownProse(value string) string {
	return html.EscapeString(value)
}

func markdownTableCell(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", "\\|")
	if value == "" {
		return "-"
	}
	return markdownProse(value)
}
