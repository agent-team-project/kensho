package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	texttemplate "text/template"

	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/template"
	"github.com/spf13/cobra"
)

const teamDirName = ".agent_team"

const emptyConfig = `# agent-team config — consumer-specific runtime values your skills read.
# This is the empty-template stub. Add sections as your skills require.

[project]
id = "{{PROJECT_ID}}"
`

// templateAuxFiles are filenames at the root of a template that are NOT
// copied verbatim. They drive the init flow but never land in the consumer's
// .agent_team/ tree.
var templateAuxFiles = map[string]bool{
	template.ManifestFileName:  true,
	template.LockFileName:      true,
	template.CacheMetaFileName: true,
}

func newInitCmd() *cobra.Command {
	var (
		targetFlag   string
		forceFlag    bool
		templateFlag string
		profileFlag  string
		minimalFlag  bool
		setFlags     []string
		noInputFlag  bool
		jsonOut      bool
		format       string
		dryRun       bool
		commands     bool
	)

	cmd := &cobra.Command{
		Use:   "init [<ref>]",
		Short: "Vendor a starter team template into the current repo (creates .agent_team/).",
		Long: "Vendor a template into the current repo (creates .agent_team/). With no ref, the bundled\n" +
			"default template is used. Its default `slim` profile is a consumer starter: manager + worker +\n" +
			"reviewer, core provider skills, and the ticket_to_pr pipeline, with schedules and sentinel /\n" +
			"prod-watch loops omitted. Pass `--minimal` to choose that starter explicitly. Pass\n" +
			"`--profile full` (or `--set template.profile=full`) to render\n" +
			"the self-dogfood topology with ticket-manager, platform/quality/release/docs/comms teams, and\n" +
			"scheduled governance loops. Refs can be local paths, cached refs, or git refs such as\n" +
			"github.com/acme/eng-team@v1.0.0. Pass `--template empty` for a scaffold-only init. `--set k=v`\n" +
			"supplies template parameters; `--dry-run` previews the selected template/profile/provider before\n" +
			"writing files; `--no-input` fails (rather than prompting) when required parameters have no value.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team init: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team init: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team init: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team init: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseTemplateCLIFormat("init-format", format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team init: %v\n", err)
				return exitErr(2)
			}
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			return runInit(cmd, initConfig{
				target:     targetFlag,
				force:      forceFlag,
				kind:       templateFlag,
				profile:    profileFlag,
				minimal:    minimalFlag,
				ref:        ref,
				setStrings: setFlags,
				noInput:    noInputFlag,
				jsonOut:    jsonOut,
				format:     tmpl,
				dryRun:     dryRun,
				commands:   commands,
			})
		},
	}

	cwd, _ := os.Getwd()
	cmd.Flags().StringVar(&targetFlag, "target", cwd, "Target repo root.")
	cmd.Flags().BoolVar(&forceFlag, "force", false, "Overwrite existing .agent_team/ files (config.toml is never overwritten).")
	cmd.Flags().StringVar(&templateFlag, "template", "default", "`default` (uses the supplied/bundled template ref) or `empty` (scaffold only, no manifest).")
	cmd.Flags().StringVar(&profileFlag, "profile", "", "Template profile to render, e.g. `slim` or `full` for the bundled template.")
	cmd.Flags().BoolVar(&minimalFlag, "minimal", false, "Render the slim external-consumer profile (alias for --profile slim).")
	cmd.Flags().StringArrayVar(&setFlags, "set", nil, "Set a template parameter, e.g. --set linear.team_id=<uuid>. Repeatable.")
	cmd.Flags().BoolVar(&noInputFlag, "no-input", false, "Fail with a clear error if required parameters are missing instead of prompting.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview init without writing .agent_team/.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching init apply command.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON on success.")
	cmd.Flags().StringVar(&format, "format", "", "Render the init result with a Go template, e.g. '{{.TeamDir}} {{.Kind}}'.")
	return cmd
}

// initConfig is the parsed input to runInit.
type initConfig struct {
	target     string
	force      bool
	kind       string // "default" or "empty"
	profile    string // convenience alias for --set template.profile=<value>
	minimal    bool   // convenience alias for --set template.profile=slim
	ref        string // template ref ("" = bundled when kind=default)
	setStrings []string
	noInput    bool
	jsonOut    bool
	format     *texttemplate.Template
	dryRun     bool
	commands   bool
}

type initResult struct {
	Target          string `json:"target"`
	TeamDir         string `json:"team_dir"`
	Kind            string `json:"kind"`
	Ref             string `json:"ref,omitempty"`
	TemplateName    string `json:"template_name,omitempty"`
	TemplateVersion string `json:"template_version,omitempty"`
	Profile         string `json:"profile,omitempty"`
	ProfileDesc     string `json:"profile_description,omitempty"`
	PMProvider      string `json:"pm_provider,omitempty"`
	ContentHash     string `json:"content_hash,omitempty"`
	ConfigPath      string `json:"config_path"`
	LockPath        string `json:"lock_path,omitempty"`
	Empty           bool   `json:"empty"`
	Force           bool   `json:"force"`
	DryRun          bool   `json:"dry_run"`
	Action          string `json:"action"`
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
	if initSuppressProgress(cfg) {
		out = io.Discard
	}
	fmt.Fprintf(out, "Vendoring team into %s\n", teamDir)
	result := initResult{
		Target:     filepath.ToSlash(target),
		TeamDir:    filepath.ToSlash(teamDir),
		Kind:       cfg.kind,
		ConfigPath: filepath.ToSlash(filepath.Join(teamDir, "config.toml")),
		Empty:      cfg.kind == "empty",
		Force:      cfg.force,
		DryRun:     cfg.dryRun,
		Action:     "initialized",
	}
	configExistedBefore := false
	if _, err := os.Stat(filepath.Join(teamDir, "config.toml")); err == nil {
		configExistedBefore = true
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if cfg.dryRun {
		result.Action = "would-init"
	}

	if cfg.kind == "empty" {
		if cfg.dryRun {
			return renderInitResult(cmd.OutOrStdout(), result, cfg)
		}
		if err := writeEmpty(out, teamDir); err != nil {
			return err
		}
		if err := writeEmptyConfig(out, teamDir); err != nil {
			return err
		}
		printNextSteps(out, teamDir)
		return renderInitResult(cmd.OutOrStdout(), result, cfg)
	}

	// Default-kind path: resolve template ref → render → write resolved config.
	rt, pull, err := resolveTemplateRefForCLI(cfg.ref)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(2)
	}
	warnTemplatePull(cmd.ErrOrStderr(), pull, cfg.jsonOut, cfg.format != nil)
	result.Ref = rt.Ref
	result.LockPath = filepath.ToSlash(filepath.Join(teamDir, template.LockFileName))
	if rt.Manifest != nil {
		result.TemplateName = rt.Manifest.Template.Name
		result.TemplateVersion = rt.Manifest.Template.Version
	}
	hash, err := template.ContentHash(rt)
	if err != nil {
		return fmt.Errorf("hash template source: %w", err)
	}
	result.ContentHash = hash

	effectiveProfile, profileSource, err := initEffectiveProfile(cfg.profile, cfg.minimal)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(2)
	}
	setStrings, err := initSetStringsWithProfile(cfg.setStrings, effectiveProfile, profileSource)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(2)
	}
	sets, err := template.ParseSetSpecs(setStrings)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(2)
	}

	missingReason := initMissingReason(cfg)
	resolved, err := resolveInitConfig(cmd, rt.Manifest, sets, missingReason)
	if err != nil {
		return err
	}
	result.Profile = selectedTemplateProfile(resolved)
	result.ProfileDesc = selectedTemplateProfileDescription(rt.Manifest, result.Profile)
	result.PMProvider = selectedPMProvider(resolved)

	if cfg.dryRun {
		return renderInitResult(cmd.OutOrStdout(), result, cfg)
	}
	if err := copyTemplate(out, rt, teamDir, resolved, cfg.force); err != nil {
		return err
	}
	if err := writeResolvedConfig(out, teamDir, resolved, rt.Manifest); err != nil {
		return err
	}
	if !configExistedBefore {
		if _, _, err := origin.EnsureProjectID(teamDir); err != nil {
			return fmt.Errorf("backfill project id: %w", err)
		}
	}
	if err := writeTemplateLock(out, teamDir, rt, cfg.force); err != nil {
		return err
	}
	printNextSteps(out, teamDir)
	return renderInitResult(cmd.OutOrStdout(), result, cfg)
}

func initMachineOutput(cfg initConfig) bool {
	return cfg.jsonOut || cfg.format != nil || cfg.commands
}

func initSuppressProgress(cfg initConfig) bool {
	return initMachineOutput(cfg) || cfg.dryRun
}

func initMissingReason(cfg initConfig) string {
	if cfg.noInput {
		return "--no-input given"
	}
	if cfg.dryRun {
		return "--dry-run requested"
	}
	if initMachineOutput(cfg) {
		return "machine-readable output requested"
	}
	return ""
}

func renderInitResult(w io.Writer, result initResult, cfg initConfig) error {
	if cfg.commands {
		_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(initApplyCommandArgs(cfg, result)), " "))
		return err
	}
	if cfg.jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if cfg.format != nil {
		if err := cfg.format.Execute(w, result); err != nil {
			return err
		}
		_, err := fmt.Fprintln(w)
		return err
	}
	if cfg.dryRun {
		printInitDryRunPreview(w, result)
	}
	return nil
}

func initApplyCommandArgs(cfg initConfig, result initResult) []string {
	args := []string{"agent-team", "init"}
	if cfg.ref != "" {
		args = append(args, cfg.ref)
	}
	args = append(args, "--target", result.Target)
	if cfg.kind != "default" {
		args = append(args, "--template", cfg.kind)
	}
	if cfg.minimal {
		args = append(args, "--minimal")
	}
	if cfg.profile != "" {
		args = append(args, "--profile", cfg.profile)
	}
	if cfg.force {
		args = append(args, "--force")
	}
	if cfg.noInput {
		args = append(args, "--no-input")
	}
	for _, set := range cfg.setStrings {
		args = append(args, "--set", set)
	}
	return args
}

func printInitDryRunPreview(w io.Writer, result initResult) {
	fmt.Fprintf(w, "would vendor team into %s\n", result.TeamDir)
	if result.Empty {
		return
	}
	if result.TemplateName != "" {
		label := result.TemplateName
		if result.TemplateVersion != "" {
			label += " " + result.TemplateVersion
		}
		if result.Ref != "" {
			label += " (" + result.Ref + ")"
		}
		fmt.Fprintf(w, "template: %s\n", label)
	}
	if result.Profile != "" {
		if result.ProfileDesc != "" {
			fmt.Fprintf(w, "profile: %s - %s\n", result.Profile, result.ProfileDesc)
		} else {
			fmt.Fprintf(w, "profile: %s\n", result.Profile)
		}
	}
	if result.PMProvider != "" {
		fmt.Fprintf(w, "pm provider: %s\n", result.PMProvider)
	}
}

func initEffectiveProfile(profile string, minimal bool) (string, string, error) {
	profile = strings.TrimSpace(profile)
	if minimal {
		if profile != "" && profile != "slim" {
			return "", "", fmt.Errorf("--minimal cannot be combined with --profile %q", profile)
		}
		return "slim", "--minimal", nil
	}
	if profile == "" {
		return "", "", nil
	}
	return profile, "--profile", nil
}

func initSetStringsWithProfile(raw []string, profile, source string) ([]string, error) {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return raw, nil
	}
	const key = "template.profile"
	out := append([]string{}, raw...)
	for _, s := range raw {
		if !strings.HasPrefix(s, key+"=") {
			continue
		}
		value := strings.TrimPrefix(s, key+"=")
		if value != profile {
			return nil, fmt.Errorf("%s conflicts with --set %s=%q", initProfileSourceLabel(source, profile), key, value)
		}
		return out, nil
	}
	return append(out, key+"="+profile), nil
}

func initProfileSourceLabel(source, profile string) string {
	switch source {
	case "--minimal":
		return "--minimal"
	case "--profile":
		return fmt.Sprintf("--profile %q", profile)
	default:
		return fmt.Sprintf("template profile %q", profile)
	}
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
func resolveInitConfig(cmd *cobra.Command, m *template.Manifest, sets []template.SetSpec, missingReason string) (template.Tree, error) {
	defaults := template.DefaultsFromManifest(m)
	withSets, err := template.ApplySets(defaults, sets, m)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return nil, exitErr(2)
	}
	if initShouldAutoEnableLinear(m, withSets, sets) {
		withSets.SetDotted("team.pm_tool", "linear")
		withSets.SetDotted("pm.provider", "linear")
	}
	if projectID, ok := withSets.GetDotted("project.id"); !ok || strings.TrimSpace(fmt.Sprint(projectID)) == "" {
		id, err := origin.GenerateProjectID()
		if err != nil {
			return nil, err
		}
		withSets.SetDotted("project.id", id)
	}
	syncPMProviderAliases(withSets, sets)

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

	if missingReason != "" {
		printMissingParams(cmd.ErrOrStderr(), m, missing, missingReason)
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
	return template.MissingRequiredKeys(resolved, m)
}

func initShouldAutoEnableLinear(m *template.Manifest, resolved template.Tree, sets []template.SetSpec) bool {
	if m == nil || m.FindParameter("team.pm_tool") == nil {
		return false
	}
	explicitPMTool := false
	linearSet := false
	for _, s := range sets {
		if s.Key == "team.pm_tool" || s.Key == "pm.provider" {
			explicitPMTool = true
			continue
		}
		if strings.HasPrefix(s.Key, "linear.") {
			linearSet = true
		}
	}
	if explicitPMTool || !linearSet {
		return false
	}
	pmTool, ok := resolved.GetDotted("team.pm_tool")
	if !ok || pmTool == nil {
		return true
	}
	pm, ok := pmTool.(string)
	return ok && (pm == "" || pm == "none")
}

func syncPMProviderAliases(resolved template.Tree, sets []template.SetSpec) {
	explicitProvider := false
	explicitPMTool := false
	for _, s := range sets {
		switch s.Key {
		case "pm.provider":
			explicitProvider = true
		case "team.pm_tool":
			explicitPMTool = true
		}
	}
	if explicitProvider && !explicitPMTool {
		if value, ok := resolved.GetDotted("pm.provider"); ok {
			resolved.SetDotted("team.pm_tool", value)
		}
		return
	}
	if explicitPMTool && !explicitProvider {
		if value, ok := resolved.GetDotted("team.pm_tool"); ok {
			resolved.SetDotted("pm.provider", value)
		}
	}
}

func printMissingParams(w fmtWriter, m *template.Manifest, keys []string, reason string) {
	fmt.Fprintf(w, "agent-team: %s but required parameters are missing:\n", reason)
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
	excludes, err := selectedTemplateProfileExcludes(rt.Manifest, resolved)
	if err != nil {
		return err
	}
	results, err := renderInto(rt, teamDir, resolved, force, excludes)
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

func selectedTemplateProfile(resolved template.Tree) string {
	value, ok := resolved.GetDotted("template.profile")
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func selectedTemplateProfileExcludes(m *template.Manifest, resolved template.Tree) ([]string, error) {
	profile := selectedTemplateProfile(resolved)
	if profile == "" || m == nil || len(m.Profiles) == 0 {
		return nil, nil
	}
	decl, ok := m.Profiles[profile]
	if !ok {
		return nil, fmt.Errorf("template profile %q is not declared", profile)
	}
	return decl.Exclude, nil
}

func selectedTemplateProfileDescription(m *template.Manifest, profile string) string {
	if m == nil || profile == "" || len(m.Profiles) == 0 {
		return ""
	}
	decl, ok := m.Profiles[profile]
	if !ok {
		return ""
	}
	return strings.TrimSpace(decl.Description)
}

func selectedPMProvider(resolved template.Tree) string {
	value, ok := resolved.GetDotted("pm.provider")
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

// renderInto walks the template root and writes each entry into teamDir.
// Existing top-level entries are preserved unless `force` is true (matches
// SQU-21's behaviour). Non-top-level paths inside an entry are always
// overwritten — they're considered part of the entry that was already
// accepted-or-skipped at the top level.
func renderInto(rt *template.ResolvedTemplate, teamDir string, resolved template.Tree, force bool, excludes []string) ([]initRenderResult, error) {
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
			rendered, err := renderEntry(rt, name, teamDir, resolved, excludes)
			if err != nil {
				return nil, err
			}
			results = append(results, rendered...)
		} else {
			rendered, err := renderEntry(rt, name, teamDir, resolved, excludes)
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
func renderEntry(rt *template.ResolvedTemplate, entry, teamDir string, resolved template.Tree, excludes []string) ([]initRenderResult, error) {
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
	subResults, err := template.RenderTreeFromFSWithExcludes(rt.FS, src, dstRoot, resolved, nil, excludesForEntry(entry, excludes))
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

func excludesForEntry(entry string, excludes []string) []string {
	if len(excludes) == 0 {
		return nil
	}
	prefix := filepath.ToSlash(entry) + "/"
	out := make([]string, 0, len(excludes))
	for _, exclude := range excludes {
		exclude = strings.TrimPrefix(filepath.ToSlash(exclude), "./")
		if exclude == entry {
			out = append(out, ".")
			continue
		}
		if strings.HasPrefix(exclude, prefix) {
			out = append(out, strings.TrimPrefix(exclude, prefix))
		}
	}
	return out
}

// writeResolvedConfig writes the merged config tree to <teamDir>/config.toml.
// Skipped if the file already exists (consumer's edits are sacrosanct).
func writeResolvedConfig(out fmtWriter, teamDir string, resolved template.Tree, manifest *template.Manifest) error {
	cfg := filepath.Join(teamDir, "config.toml")
	if _, err := os.Stat(cfg); err == nil {
		fmt.Fprintf(out, "  keep %s/config.toml (untouched)\n", teamDirName)
		return nil
	}
	body, err := template.EncodeTOML(resolved)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	body = prependResolvedConfigGuide(body, resolved, manifest)
	if err := os.WriteFile(cfg, body, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "  + %s/config.toml (resolved)\n", teamDirName)
	return nil
}

func prependResolvedConfigGuide(body []byte, resolved template.Tree, manifest *template.Manifest) []byte {
	guide := resolvedConfigGuide(resolved, manifest)
	if guide == "" {
		return body
	}
	out := make([]byte, 0, len(guide)+len(body))
	out = append(out, []byte(guide)...)
	out = append(out, body...)
	return out
}

func resolvedConfigGuide(resolved template.Tree, manifest *template.Manifest) string {
	provider := resolvedConfigString(resolved, "pm.provider")
	if provider == "" {
		provider = resolvedConfigString(resolved, "team.pm_tool")
	}
	if provider == "" {
		provider = "none"
	}
	profile := selectedTemplateProfile(resolved)
	required := resolvedConfigRequiredParams(manifest, provider)

	var b strings.Builder
	fmt.Fprintln(&b, "# Generated by agent-team init. Commit this file; keep API keys and tokens in .env.")
	fmt.Fprintln(&b, "#")
	if profile != "" {
		fmt.Fprintf(&b, "# Template profile: %q. The slim profile is the external-consumer starter; full adds the self-dogfood topology.\n", profile)
	}
	fmt.Fprintf(&b, "# PM provider: %q.\n", provider)
	if len(required) == 0 {
		fmt.Fprintln(&b, "# Required provider keys now: none. Ticketless jobs use the job kickoff as the work item.")
	} else {
		fmt.Fprintln(&b, "# Required provider keys now:")
		for _, p := range required {
			desc := strings.TrimSpace(p.Description)
			if desc != "" {
				fmt.Fprintf(&b, "# - %s: %s\n", p.Key, desc)
			} else {
				fmt.Fprintf(&b, "# - %s\n", p.Key)
			}
		}
	}
	fmt.Fprintln(&b, "# Provider sections for modes you are not using can stay blank.")
	fmt.Fprintln(&b, "# Optional runtime, health, notification, label, and project fields can stay at defaults until needed.")
	fmt.Fprintln(&b, "# After editing provider settings, run: agent-team doctor --commands")
	fmt.Fprintln(&b)
	return b.String()
}

func resolvedConfigString(resolved template.Tree, key string) string {
	value, ok := resolved.GetDotted(key)
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func resolvedConfigRequiredParams(manifest *template.Manifest, provider string) []template.Parameter {
	if manifest == nil {
		return nil
	}
	var required []template.Parameter
	for _, p := range manifest.Parameters {
		if p.Required {
			required = append(required, p)
			continue
		}
		if p.RequiredWhenKey == "pm.provider" && p.RequiredWhenValue == provider {
			required = append(required, p)
		}
	}
	return required
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

	body, err := templateLockBytes(rt)
	if err != nil {
		return err
	}
	if err := os.WriteFile(lockPath, body, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "  + %s/%s (template provenance)\n", teamDirName, template.LockFileName)
	return nil
}

func templateLockBytes(rt *template.ResolvedTemplate) ([]byte, error) {
	hash, err := template.ContentHash(rt)
	if err != nil {
		return nil, fmt.Errorf("hash template source: %w", err)
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
		return nil, fmt.Errorf("encode template lock: %w", err)
	}
	body = append([]byte("# Generated by agent-team init. Used by `agent-team upgrade`.\n"), body...)
	return body, nil
}

func writeEmptyConfig(out fmtWriter, teamDir string) error {
	cfg := filepath.Join(teamDir, "config.toml")
	if _, err := os.Stat(cfg); err == nil {
		fmt.Fprintf(out, "  keep %s/config.toml (untouched)\n", teamDirName)
		return nil
	}
	projectID, err := origin.GenerateProjectID()
	if err != nil {
		return err
	}
	body := strings.ReplaceAll(emptyConfig, "{{PROJECT_ID}}", projectID)
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
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
	fmt.Fprintf(out, "  1. Review the first-run checklist at the top of %s/config.toml.\n", teamDir)
	fmt.Fprintln(out, "  2. Run `agent-team doctor --commands` to verify the layout and print safe follow-up commands.")
	fmt.Fprintln(out, "  3. Add or edit agents under .agent_team/agents/<name>/ — each is a dir with agent.md, config.toml, optional skills/.")
	fmt.Fprintln(out, "  4. Run `agent-team run` to launch the selected runtime with your team registered.")
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
