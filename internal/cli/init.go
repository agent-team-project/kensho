package cli

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jamesaud/agent-team/internal/template"
	"github.com/spf13/cobra"
)

const teamDirName = ".agent_team"

const emptyConfig = `# agent-team config — consumer-specific runtime values your skills read.
# This is the empty-template stub. Add sections as your skills require.
`

// templateAuxFiles are filenames at the root of a template that are NOT
// copied verbatim. They drive the init flow but never land in the consumer's
// .agent_team/ tree.
var templateAuxFiles = map[string]bool{
	template.ManifestFileName: true,
	template.LockFileName:     true,
	"config.toml.example":     true, // legacy, retained for back-compat
}

func newInitCmd() *cobra.Command {
	var (
		targetFlag   string
		forceFlag    bool
		templateFlag string
		setFlags     []string
		noInputFlag  bool
	)

	cmd := &cobra.Command{
		Use:   "init [<ref>]",
		Short: "Vendor a starter team template into the current repo (creates .agent_team/).",
		Long: "Vendor a template into the current repo (creates .agent_team/). With no ref, the bundled\n" +
			"default template is used (a software-engineering team — manager + worker + ticket-manager,\n" +
			"plus linear / pull-request / assign-worker skills). Pass `--template empty` for a scaffold-\n" +
			"only init. `--set k=v` supplies template parameters; `--no-input` fails (rather than prompting)\n" +
			"when required parameters have no value.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			return runInit(cmd, initConfig{
				target:     targetFlag,
				force:      forceFlag,
				kind:       templateFlag,
				ref:        ref,
				setStrings: setFlags,
				noInput:    noInputFlag,
			})
		},
	}

	cwd, _ := os.Getwd()
	cmd.Flags().StringVar(&targetFlag, "target", cwd, "Target repo root.")
	cmd.Flags().BoolVar(&forceFlag, "force", false, "Overwrite existing .agent_team/ files (config.toml is never overwritten).")
	cmd.Flags().StringVar(&templateFlag, "template", "default", "`default` (uses the supplied/bundled template ref) or `empty` (scaffold only, no manifest).")
	cmd.Flags().StringArrayVar(&setFlags, "set", nil, "Set a template parameter, e.g. --set linear.team_id=<uuid>. Repeatable.")
	cmd.Flags().BoolVar(&noInputFlag, "no-input", false, "Fail with a clear error if required parameters are missing instead of prompting.")
	return cmd
}

// initConfig is the parsed input to runInit.
type initConfig struct {
	target     string
	force      bool
	kind       string // "default" or "empty"
	ref        string // template ref ("" = bundled when kind=default)
	setStrings []string
	noInput    bool
}

func runInit(cmd *cobra.Command, cfg initConfig) error {
	if cfg.kind != "default" && cfg.kind != "empty" {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: --template must be `default` or `empty`, got '%s'\n", cfg.kind)
		return exitErr(2)
	}
	target, err := resolveAbsTarget(cfg.target)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: target is not a directory: %s\n", target)
		return exitErr(2)
	}
	teamDir := filepath.Join(target, teamDirName)
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Vendoring team into %s\n", teamDir)

	if cfg.kind == "empty" {
		if err := writeEmpty(out, teamDir); err != nil {
			return err
		}
		if err := writeEmptyConfig(out, teamDir); err != nil {
			return err
		}
		printNextSteps(out, teamDir)
		return nil
	}

	// Default-kind path: resolve template ref → render → write resolved config.
	resolver := newResolver()
	rt, err := resolver.Resolve(cfg.ref)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(2)
	}

	sets, err := template.ParseSetSpecs(cfg.setStrings)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(2)
	}

	resolved, err := resolveInitConfig(cmd, rt.Manifest, sets, cfg.noInput)
	if err != nil {
		return err
	}

	if err := copyTemplate(out, rt, teamDir, resolved, cfg.force); err != nil {
		return err
	}
	if err := writeResolvedConfig(out, teamDir, resolved); err != nil {
		return err
	}
	if err := writeTemplateLock(out, teamDir, rt, cfg.force); err != nil {
		return err
	}
	printNextSteps(out, teamDir)
	return nil
}

func resolveAbsTarget(target string) (string, error) {
	abs, err := filepath.Abs(target)
	if err != nil {
		return target, err
	}
	resolved := filepath.Clean(abs)
	if eval, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = eval
	}
	if st, err := os.Stat(resolved); err != nil || !st.IsDir() {
		return resolved, fmt.Errorf("not a directory")
	}
	return resolved, nil
}

// resolveInitConfig builds the resolved config tree from manifest defaults +
// CLI --set values, prompting interactively for any missing required params
// (unless --no-input is in effect).
func resolveInitConfig(cmd *cobra.Command, m *template.Manifest, sets []template.SetSpec, noInput bool) (template.Tree, error) {
	defaults := template.DefaultsFromManifest(m)
	withSets, err := template.ApplySets(defaults, sets, m)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return nil, exitErr(2)
	}

	// Find missing required params.
	missing := missingRequired(withSets, m)
	if len(missing) == 0 {
		// All set; just validate patterns.
		if err := template.ValidateAgainstManifest(withSets, m); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
			return nil, exitErr(2)
		}
		return withSets, nil
	}

	if noInput {
		printMissingParams(cmd.ErrOrStderr(), m, missing)
		return nil, exitErr(2)
	}

	// Interactive prompt for each missing param.
	withSets, err = promptMissing(cmd, m, withSets, missing)
	if err != nil {
		return nil, err
	}
	if err := template.ValidateAgainstManifest(withSets, m); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return nil, exitErr(2)
	}
	return withSets, nil
}

// missingRequired returns the keys of required parameters that have no
// non-empty value in the resolved tree.
func missingRequired(resolved template.Tree, m *template.Manifest) []string {
	if m == nil {
		return nil
	}
	var missing []string
	for _, p := range m.Parameters {
		if !p.Required {
			continue
		}
		v, ok := resolved.GetDotted(p.Key)
		if !ok || isEmptyForInit(v) {
			missing = append(missing, p.Key)
		}
	}
	return missing
}

func isEmptyForInit(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	}
	return false
}

func printMissingParams(w fmtWriter, m *template.Manifest, keys []string) {
	fmt.Fprintln(w, "agent-team: --no-input given but required parameters are missing:")
	for _, k := range keys {
		p := m.FindParameter(k)
		desc := ""
		if p != nil {
			desc = p.Description
		}
		if desc != "" {
			fmt.Fprintf(w, "  - %s — %s\n", k, desc)
		} else {
			fmt.Fprintf(w, "  - %s\n", k)
		}
	}
	fmt.Fprintln(w, "Pass each via `--set <key>=<value>` and re-run.")
}

func promptMissing(cmd *cobra.Command, m *template.Manifest, base template.Tree, keys []string) (template.Tree, error) {
	out := cmd.OutOrStdout()
	in := bufio.NewReader(cmd.InOrStdin())
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "This template requires the following parameters:")
	fmt.Fprintln(out, "")
	updated := base
	for _, key := range keys {
		p := m.FindParameter(key)
		if p == nil {
			continue
		}
		hint := ""
		if p.Pattern != "" {
			hint = fmt.Sprintf(" [matches %s]", p.Pattern)
		}
		if p.Description != "" {
			fmt.Fprintf(out, "  %s — %s\n", key, p.Description)
		}
		for {
			fmt.Fprintf(out, "  %s%s: ", key, hint)
			line, err := in.ReadString('\n')
			if err != nil && line == "" {
				return nil, fmt.Errorf("prompt for %s aborted: %w", key, err)
			}
			val := strings.TrimSpace(line)
			if val == "" && p.Required {
				fmt.Fprintln(out, "    (required — please supply a value)")
				continue
			}
			coerced, err := template.ApplySets(template.Tree{}, []template.SetSpec{{Key: key, Value: val}}, m)
			if err != nil {
				fmt.Fprintf(out, "    %v\n", err)
				continue
			}
			v, _ := coerced.GetDotted(key)
			temp := template.Tree{}
			temp.SetDotted(key, v)
			merged := template.MergeOver(updated, temp)
			if err := template.ValidateAgainstManifest(merged, m); err != nil {
				if isOnlyMissingFor(err, key) {
					// Can't be missing; we just set it. Some other failure.
					fmt.Fprintf(out, "    %v\n", err)
					continue
				}
				// Pattern violation just for this key?
				if isPatternError(err, key) {
					fmt.Fprintf(out, "    %v\n", err)
					continue
				}
			}
			updated = merged
			break
		}
	}
	fmt.Fprintln(out, "")
	return updated, nil
}

func isOnlyMissingFor(err error, key string) bool {
	if mre, ok := err.(*template.MissingRequiredError); ok {
		for _, k := range mre.Keys {
			if k != key {
				return false
			}
		}
		return true
	}
	return false
}

func isPatternError(err error, key string) bool {
	return strings.Contains(err.Error(), fmt.Sprintf("parameter %s value", key))
}

// copyTemplate renders the resolved tree into teamDir, walking the source
// FS via the package-internal renderer.
func copyTemplate(out fmtWriter, rt *template.ResolvedTemplate, teamDir string, resolved template.Tree, force bool) error {
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		return err
	}
	results, err := renderInto(rt, teamDir, resolved, force)
	if err != nil {
		return err
	}
	for _, r := range results {
		if r.Skipped {
			fmt.Fprintf(out, "  skip %s/%s (already exists; --force to overwrite)\n", teamDirName, r.DestRel)
			continue
		}
		mark := "+"
		if r.Rendered {
			mark = "+ (rendered)"
		}
		fmt.Fprintf(out, "  %s %s/%s\n", mark, teamDirName, r.DestRel)
	}
	return nil
}

// initRenderResult augments template.RenderResult with a `Skipped` flag, set
// when the destination already exists and `--force` was not passed.
type initRenderResult struct {
	template.RenderResult
	Skipped bool
}

// renderInto walks the template root and writes each entry into teamDir.
// Existing top-level entries are preserved unless `force` is true (matches
// SQU-21's behaviour). Non-top-level paths inside an entry are always
// overwritten — they're considered part of the entry that was already
// accepted-or-skipped at the top level.
func renderInto(rt *template.ResolvedTemplate, teamDir string, resolved template.Tree, force bool) ([]initRenderResult, error) {
	entries, err := fs.ReadDir(rt.FS, rt.Root)
	if err != nil {
		return nil, fmt.Errorf("read template root: %w", err)
	}
	var results []initRenderResult
	for _, e := range entries {
		name := e.Name()
		if templateAuxFiles[name] {
			continue
		}
		dstName := strings.TrimSuffix(name, template.TmplSuffix)
		dstPath := filepath.Join(teamDir, dstName)
		if !force {
			if _, err := os.Stat(dstPath); err == nil {
				results = append(results, initRenderResult{
					RenderResult: template.RenderResult{SourceRel: name, DestRel: dstName},
					Skipped:      true,
				})
				continue
			}
		}
		if e.IsDir() {
			if force {
				_ = os.RemoveAll(dstPath)
			}
			rendered, err := renderEntry(rt, name, teamDir, resolved)
			if err != nil {
				return nil, err
			}
			results = append(results, rendered...)
		} else {
			rendered, err := renderEntry(rt, name, teamDir, resolved)
			if err != nil {
				return nil, err
			}
			results = append(results, rendered...)
		}
	}
	return results, nil
}

// renderEntry routes a single top-level entry (file or directory) into the
// renderer, preserving the original SQU-21 file-by-file output.
func renderEntry(rt *template.ResolvedTemplate, entry, teamDir string, resolved template.Tree) ([]initRenderResult, error) {
	src := joinFS(rt.Root, entry)
	dstName := strings.TrimSuffix(entry, template.TmplSuffix)

	st, err := fs.Stat(rt.FS, src)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		body, err := fs.ReadFile(rt.FS, src)
		if err != nil {
			return nil, err
		}
		dstPath := filepath.Join(teamDir, dstName)
		mode := os.FileMode(0o644)
		if strings.HasSuffix(entry, ".sh") || strings.HasSuffix(entry, ".sh"+template.TmplSuffix) {
			mode = 0o755
		}
		if strings.HasSuffix(entry, template.TmplSuffix) {
			rendered, err := template.RenderBytes(entry, body, resolved)
			if err != nil {
				return nil, err
			}
			body = rendered
		}
		if err := os.WriteFile(dstPath, body, mode); err != nil {
			return nil, err
		}
		return []initRenderResult{{
			RenderResult: template.RenderResult{
				SourceRel: entry,
				DestRel:   dstName,
				Rendered:  strings.HasSuffix(entry, template.TmplSuffix),
			},
		}}, nil
	}

	// Directory: render its full subtree.
	dstRoot := filepath.Join(teamDir, dstName)
	subResults, err := template.RenderTreeFromFS(rt.FS, src, dstRoot, resolved, nil)
	if err != nil {
		return nil, err
	}
	out := make([]initRenderResult, 0, len(subResults)+1)
	out = append(out, initRenderResult{
		RenderResult: template.RenderResult{SourceRel: entry, DestRel: dstName},
	})
	for _, s := range subResults {
		out = append(out, initRenderResult{
			RenderResult: template.RenderResult{
				SourceRel: entry + "/" + s.SourceRel,
				DestRel:   dstName + "/" + s.DestRel,
				Rendered:  s.Rendered,
			},
		})
	}
	return out, nil
}

// writeResolvedConfig writes the merged config tree to <teamDir>/config.toml.
// Skipped if the file already exists (consumer's edits are sacrosanct).
func writeResolvedConfig(out fmtWriter, teamDir string, resolved template.Tree) error {
	cfg := filepath.Join(teamDir, "config.toml")
	if _, err := os.Stat(cfg); err == nil {
		fmt.Fprintf(out, "  keep %s/config.toml (untouched)\n", teamDirName)
		return nil
	}
	body, err := template.EncodeTOML(resolved)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.WriteFile(cfg, body, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "  + %s/config.toml (resolved)\n", teamDirName)
	return nil
}

// writeTemplateLock writes source provenance for future upgrade flows. Existing
// lock files are preserved unless --force is passed, mirroring authored files.
func writeTemplateLock(out fmtWriter, teamDir string, rt *template.ResolvedTemplate, force bool) error {
	lockPath := filepath.Join(teamDir, template.LockFileName)
	if _, err := os.Stat(lockPath); err == nil && !force {
		fmt.Fprintf(out, "  keep %s/%s (untouched)\n", teamDirName, template.LockFileName)
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}

	hash, err := template.ContentHash(rt)
	if err != nil {
		return fmt.Errorf("hash template source: %w", err)
	}
	info := map[string]any{
		"ref":          rt.Ref,
		"content_hash": hash,
	}
	if rt.Manifest != nil {
		if rt.Manifest.Template.Name != "" {
			info["name"] = rt.Manifest.Template.Name
		}
		if rt.Manifest.Template.Version != "" {
			info["version"] = rt.Manifest.Template.Version
		}
	}
	body, err := template.EncodeTOML(template.Tree{"template": info})
	if err != nil {
		return fmt.Errorf("encode template lock: %w", err)
	}
	body = append([]byte("# Generated by agent-team init. Used by future `agent-team upgrade`.\n"), body...)
	if err := os.WriteFile(lockPath, body, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "  + %s/%s (template provenance)\n", teamDirName, template.LockFileName)
	return nil
}

func writeEmptyConfig(out fmtWriter, teamDir string) error {
	cfg := filepath.Join(teamDir, "config.toml")
	if _, err := os.Stat(cfg); err == nil {
		fmt.Fprintf(out, "  keep %s/config.toml (untouched)\n", teamDirName)
		return nil
	}
	if err := os.WriteFile(cfg, []byte(emptyConfig), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "  + %s/config.toml (starter; edit before use)\n", teamDirName)
	return nil
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

func printNextSteps(out fmtWriter, teamDir string) {
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Done. Next steps:")
	fmt.Fprintf(out, "  1. Review %s/config.toml.\n", teamDir)
	fmt.Fprintln(out, "  2. Add or edit agents under .agent_team/agents/<name>/ — each is a dir with agent.md, config.toml, optional skills/.")
	fmt.Fprintln(out, "  3. Run `agent-team run` to launch the selected runtime with your team registered.")
	fmt.Fprintln(out, "  4. Run `agent-team doctor` to verify the layout is well-formed.")
}

// fmtWriter is the minimal interface the helpers need from `cmd.OutOrStdout()`.
type fmtWriter interface {
	Write(p []byte) (int, error)
}

// joinFS joins fs.FS-style path components, where "." is the current dir and
// has special meaning. Returns "<entry>" when root == "." and "<root>/<entry>"
// otherwise — fs.Stat rejects "./entry".
func joinFS(root, entry string) string {
	if root == "" || root == "." {
		return entry
	}
	return root + "/" + entry
}

func exitErr(code int) error { return ExitCode(code) }
