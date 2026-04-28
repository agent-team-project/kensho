package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	agentteam "github.com/jamesaud/agent-team"
	"github.com/spf13/cobra"
)

const teamDirName = ".agent_team"

const emptyConfig = `# agent-team config — consumer-specific runtime values your skills read.
# This is the empty-template stub. Add sections as your skills require.
`

func newInitCmd() *cobra.Command {
	var (
		targetFlag   string
		forceFlag    bool
		templateFlag string
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Vendor a starter team template into the current repo (creates .agent_team/).",
		Long: "Vendor a starter team template into the current repo (creates .agent_team/). " +
			"The default template ships a software-engineering team (ticket-manager, " +
			"manager, worker, plus linear / pull-request / assign-worker skills). " +
			"`--template empty` writes only the directory scaffold + a stub config.toml. " +
			"Run `agent-team run` afterwards to launch Claude Code with the team registered.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd, targetFlag, forceFlag, templateFlag)
		},
	}

	cwd, _ := os.Getwd()
	cmd.Flags().StringVar(&targetFlag, "target", cwd, "Target repo root.")
	cmd.Flags().BoolVar(&forceFlag, "force", false, "Overwrite existing .agent_team/ files (config.toml is never overwritten).")
	cmd.Flags().StringVar(&templateFlag, "template", "default", "`default` (bundled software-eng team) or `empty` (scaffold only).")
	return cmd
}

func runInit(cmd *cobra.Command, target string, force bool, template string) error {
	if template != "default" && template != "empty" {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: --template must be `default` or `empty`, got '%s'\n", template)
		return exitErr(2)
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return exitErr(2)
	}
	resolvedTarget := filepath.Clean(abs)
	if eval, err := filepath.EvalSymlinks(resolvedTarget); err == nil {
		resolvedTarget = eval
	}
	if st, err := os.Stat(resolvedTarget); err != nil || !st.IsDir() {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: target is not a directory: %s\n", resolvedTarget)
		return exitErr(2)
	}

	teamDir := filepath.Join(resolvedTarget, teamDirName)
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Vendoring team into %s\n", teamDir)

	if template == "empty" {
		if err := writeEmpty(out, teamDir); err != nil {
			return err
		}
	} else {
		if err := copyTemplate(out, teamDir, force); err != nil {
			return err
		}
	}

	if err := writeConfig(out, teamDir, template); err != nil {
		return err
	}

	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Done. Next steps:")
	fmt.Fprintf(out, "  1. Edit %s (team_id, ticket_prefix, etc.).\n", filepath.Join(teamDir, "config.toml"))
	fmt.Fprintln(out, "  2. Add or edit agents under .agent_team/agents/<name>/ — each is a dir with agent.md, config.toml, optional skills/.")
	fmt.Fprintln(out, "  3. Run `agent-team run` to launch Claude Code with your team registered.")
	fmt.Fprintln(out, "  4. Run `agent-team doctor` to verify the layout is well-formed.")
	return nil
}

func copyTemplate(out fmtWriter, teamDir string, force bool) error {
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		return err
	}
	srcFS := agentteam.TemplateFS()
	root := agentteam.TemplateRoot

	entries, err := fs.ReadDir(srcFS, root)
	if err != nil {
		return fmt.Errorf("read embedded template: %w", err)
	}

	for _, entry := range entries {
		if entry.Name() == "config.toml.example" {
			continue
		}
		srcPath := root + "/" + entry.Name()
		dstPath := filepath.Join(teamDir, entry.Name())
		rel := relFrom(filepath.Dir(teamDir), dstPath)

		if _, err := os.Stat(dstPath); err == nil && !force {
			fmt.Fprintf(out, "  skip %s (already exists; --force to overwrite)\n", rel)
			continue
		}

		if entry.IsDir() {
			if _, err := os.Stat(dstPath); err == nil {
				if err := os.RemoveAll(dstPath); err != nil {
					return err
				}
			}
			if err := copyEmbeddedTree(srcFS, srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyEmbeddedFile(srcFS, srcPath, dstPath); err != nil {
				return err
			}
		}
		fmt.Fprintf(out, "  + %s\n", rel)
	}
	return nil
}

func copyEmbeddedTree(srcFS fs.FS, srcRoot, dstRoot string) error {
	return fs.WalkDir(srcFS, srcRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := relFromFS(srcRoot, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(dstRoot, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		return copyEmbeddedFile(srcFS, p, dst)
	})
}

func copyEmbeddedFile(srcFS fs.FS, src, dst string) error {
	data, err := fs.ReadFile(srcFS, src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if isExecutable(src) {
		mode = 0o755
	}
	return os.WriteFile(dst, data, mode)
}

// isExecutable mirrors the Python `shutil.copy2` behaviour of preserving the
// executable bit on shell scripts. The embedded FS does not retain modes, so
// we restore +x for `.sh` files in `scripts/` directories.
func isExecutable(p string) bool {
	return strings.HasSuffix(p, ".sh")
}

func writeEmpty(out fmtWriter, teamDir string) error {
	if err := os.MkdirAll(filepath.Join(teamDir, "agents"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(teamDir, "skills"), 0o755); err != nil {
		return err
	}
	dirName := filepath.Base(teamDir)
	fmt.Fprintf(out, "  + %s/agents/\n", dirName)
	fmt.Fprintf(out, "  + %s/skills/\n", dirName)
	return nil
}

func writeConfig(out fmtWriter, teamDir, template string) error {
	realConfig := filepath.Join(teamDir, "config.toml")
	if _, err := os.Stat(realConfig); err == nil {
		fmt.Fprintf(out, "  keep %s (untouched)\n", relFrom(filepath.Dir(teamDir), realConfig))
		return nil
	}
	if template == "empty" {
		if err := os.WriteFile(realConfig, []byte(emptyConfig), 0o644); err != nil {
			return err
		}
	} else {
		srcFS := agentteam.TemplateFS()
		exampleSrc := agentteam.TemplateRoot + "/config.toml.example"
		if err := copyEmbeddedFile(srcFS, exampleSrc, filepath.Join(teamDir, "config.toml.example")); err != nil {
			return err
		}
		if err := copyEmbeddedFile(srcFS, exampleSrc, realConfig); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "  + %s (starter; edit before use)\n", relFrom(filepath.Dir(teamDir), realConfig))
	return nil
}

// relFrom returns `target` expressed relative to `base`, using forward slashes
// to match Python's `Path.relative_to` echo format on Linux/macOS.
func relFrom(base, target string) string {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return target
	}
	return filepath.ToSlash(rel)
}

func relFromFS(base, target string) (string, error) {
	if !strings.HasPrefix(target, base) {
		return "", fmt.Errorf("path %q not under %q", target, base)
	}
	if target == base {
		return ".", nil
	}
	rel := strings.TrimPrefix(target, base+"/")
	return rel, nil
}

// fmtWriter is the minimal interface the helpers need from `cmd.OutOrStdout()`.
type fmtWriter interface {
	Write(p []byte) (int, error)
}

func exitErr(code int) error { return ExitCode(code) }
