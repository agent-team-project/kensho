package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	texttemplate "text/template"

	"github.com/jamesaud/agent-team/internal/template"
	"github.com/spf13/cobra"
)

type upgradeConfig struct {
	target   string
	toRef    string
	check    bool
	apply    bool
	dryRun   bool
	commands bool
	strict   bool
	json     bool
	format   string
}

type upgradeCheckResult struct {
	LockedRef        string `json:"locked_ref"`
	LockedTemplate   string `json:"locked_template,omitempty"`
	LockedVersion    string `json:"locked_version,omitempty"`
	LockedHash       string `json:"locked_hash"`
	TargetRef        string `json:"target_ref"`
	TargetTemplate   string `json:"target_template,omitempty"`
	TargetVersion    string `json:"target_version,omitempty"`
	TargetHash       string `json:"target_hash"`
	UpToDate         bool   `json:"up_to_date"`
	Differs          bool   `json:"differs"`
	ApplyImplemented bool   `json:"apply_implemented"`
}

type upgradeApplyResult struct {
	upgradeCheckResult
	DryRun    bool                 `json:"dry_run,omitempty"`
	Applied   bool                 `json:"applied"`
	Added     int                  `json:"added,omitempty"`
	Updated   int                  `json:"updated,omitempty"`
	Removed   int                  `json:"removed,omitempty"`
	Unchanged int                  `json:"unchanged,omitempty"`
	Conflicts int                  `json:"conflicts,omitempty"`
	Actions   []upgradeApplyAction `json:"actions"`
}

type upgradeApplyAction struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
}

func newUpgradeCmd() *cobra.Command {
	var cfg upgradeConfig
	cwd, _ := os.Getwd()

	cmd := &cobra.Command{
		Use:   "upgrade (--check|--apply) [--to <ref>]",
		Short: "Check or apply a template upgrade using the repo's template lock.",
		Long: "Compare .agent_team/.template.lock against the locked template ref, or --to <ref> when supplied. " +
			"With --apply, agent-team renders the locked and target templates with the current repo config and " +
			"applies only clean three-way changes; local edits are reported as conflicts and left untouched.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfg.check && cfg.apply {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team upgrade: choose exactly one of --check or --apply.")
				return exitErr(2)
			}
			if cfg.commands && (!cfg.apply || !cfg.dryRun) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team upgrade: --commands requires --apply --dry-run.")
				return exitErr(2)
			}
			if cfg.dryRun && !cfg.apply {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team upgrade: --dry-run requires --apply.")
				return exitErr(2)
			}
			if cfg.apply {
				return runUpgradeApply(cmd, cfg)
			}
			if cfg.check {
				return runUpgradeCheck(cmd, cfg)
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "agent-team upgrade: pass --check to inspect or --apply to apply clean template changes.")
			return exitErr(2)
		},
	}
	cfg.target = cwd
	cmd.Flags().StringVar(&cfg.target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&cfg.toRef, "to", "", "Template ref to compare against (defaults to the ref in .template.lock).")
	cmd.Flags().BoolVar(&cfg.check, "check", false, "Compare current template lock against a resolved template ref without writing files.")
	cmd.Flags().BoolVar(&cfg.apply, "apply", false, "Apply clean template changes and update .template.lock; refuses to run when local conflicts are detected.")
	cmd.Flags().BoolVar(&cfg.dryRun, "dry-run", false, "With --apply, preview the clean/conflicting file actions without writing files.")
	cmd.Flags().BoolVar(&cfg.commands, "commands", false, "With --apply --dry-run, print the matching apply command when the preview has actionable work.")
	cmd.Flags().BoolVar(&cfg.strict, "strict", false, "With --check, exit 1 when the target template differs from the lock.")
	cmd.Flags().BoolVar(&cfg.json, "json", false, "Emit the upgrade check result as JSON.")
	cmd.Flags().StringVar(&cfg.format, "format", "", "Render the upgrade check result with a Go template, e.g. '{{.Differs}} {{.TargetVersion}}'.")
	return cmd
}

func runUpgradeCheck(cmd *cobra.Command, cfg upgradeConfig) error {
	if cfg.format != "" && cfg.json {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team upgrade: --format cannot be combined with --json.")
		return exitErr(2)
	}
	tmpl, err := parseUpgradeCheckFormat(cfg.format)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team upgrade: %v\n", err)
		return exitErr(2)
	}

	_, _, _, result, err := loadUpgradeTarget(cmd, cfg)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if cfg.json {
		if err := json.NewEncoder(out).Encode(result); err != nil {
			return err
		}
	} else if tmpl != nil {
		if err := renderUpgradeCheckFormat(out, result, tmpl); err != nil {
			return err
		}
	} else {
		renderUpgradeCheck(out, result)
	}
	if cfg.strict && result.Differs {
		return exitErr(1)
	}
	return nil
}

func runUpgradeApply(cmd *cobra.Command, cfg upgradeConfig) error {
	if cfg.commands && cfg.json {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team upgrade: --commands cannot be combined with --json.")
		return exitErr(2)
	}
	if cfg.commands && cfg.format != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team upgrade: --commands cannot be combined with --format.")
		return exitErr(2)
	}
	if cfg.format != "" && cfg.json {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team upgrade: --format cannot be combined with --json.")
		return exitErr(2)
	}
	tmpl, err := parseUpgradeCheckFormat(cfg.format)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team upgrade: %v\n", err)
		return exitErr(2)
	}
	teamDir, locked, target, check, err := loadUpgradeTarget(cmd, cfg)
	if err != nil {
		return err
	}
	resolved, err := template.LoadTOMLFile(filepath.Join(teamDir, "config.toml"))
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team upgrade: %v\n", err)
		return exitErr(2)
	}
	result, err := planUpgradeApply(teamDir, locked, target, check, resolved, cfg.dryRun)
	if err != nil {
		return err
	}
	if result.Conflicts == 0 && !cfg.dryRun {
		if err := applyUpgradePlan(teamDir, target, result.Actions); err != nil {
			return err
		}
		result.Applied = true
	}

	out := cmd.OutOrStdout()
	if cfg.commands {
		return renderUpgradeApplyCommand(out, result.Conflicts == 0 && result.Added+result.Updated+result.Removed > 0, upgradeApplyCommandOptions{
			Scope:    operatorCommandScopeFromCommand(cmd, cfg.target, "target"),
			ToRef:    cfg.toRef,
			ToRefSet: cmd.Flags().Changed("to"),
		})
	}
	if cfg.json {
		if err := json.NewEncoder(out).Encode(result); err != nil {
			return err
		}
	} else if tmpl != nil {
		if err := renderUpgradeCheckFormat(out, result, tmpl); err != nil {
			return err
		}
	} else {
		renderUpgradeApply(out, result)
	}
	if result.Conflicts > 0 && !cfg.dryRun {
		return exitErr(1)
	}
	return nil
}

type upgradeApplyCommandOptions struct {
	Scope    operatorCommandScope
	ToRef    string
	ToRefSet bool
}

func renderUpgradeApplyCommand(out fmtWriter, hasAction bool, opts upgradeApplyCommandOptions) error {
	if !hasAction {
		return nil
	}
	_, err := fmt.Fprintln(out, strings.Join(shellQuoteArgs(upgradeApplyCommandArgs(opts)), " "))
	return err
}

func upgradeApplyCommandArgs(opts upgradeApplyCommandOptions) []string {
	args := []string{"agent-team", "upgrade", "--apply"}
	if opts.Scope.Set && strings.TrimSpace(opts.Scope.Repo) != "" {
		args = append(args, "--repo", opts.Scope.Repo)
	}
	if opts.ToRefSet && strings.TrimSpace(opts.ToRef) != "" {
		args = append(args, "--to", opts.ToRef)
	}
	return args
}

func loadUpgradeTarget(cmd *cobra.Command, cfg upgradeConfig) (string, *template.ResolvedTemplate, *template.ResolvedTemplate, upgradeCheckResult, error) {
	teamDir, err := resolveTeamDir(cmd, cfg.target)
	if err != nil {
		return "", nil, nil, upgradeCheckResult{}, err
	}
	lockPath := filepath.Join(teamDir, template.LockFileName)
	lock, err := template.LoadLock(lockPath)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: cannot read %s: %v\n", lockPath, err)
		return "", nil, nil, upgradeCheckResult{}, exitErr(2)
	}

	targetRef := cfg.toRef
	if targetRef == "" {
		targetRef = lock.Template.Ref
	}
	resolver := newResolver()
	locked, err := resolver.Resolve(lock.Template.Ref)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: resolve locked template %q: %v\n", lock.Template.Ref, err)
		return "", nil, nil, upgradeCheckResult{}, exitErr(2)
	}
	target, err := resolver.Resolve(targetRef)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return "", nil, nil, upgradeCheckResult{}, exitErr(2)
	}
	targetHash, err := template.ContentHash(target)
	if err != nil {
		return "", nil, nil, upgradeCheckResult{}, fmt.Errorf("hash template source: %w", err)
	}
	result := upgradeCheckResult{
		LockedRef:        lock.Template.Ref,
		LockedTemplate:   lock.Template.Name,
		LockedVersion:    lock.Template.Version,
		LockedHash:       lock.Template.ContentHash,
		TargetRef:        targetRef,
		TargetHash:       targetHash,
		UpToDate:         targetHash == lock.Template.ContentHash,
		ApplyImplemented: true,
	}
	result.Differs = !result.UpToDate
	if target.Manifest != nil {
		result.TargetTemplate = target.Manifest.Template.Name
		result.TargetVersion = target.Manifest.Template.Version
	}
	return teamDir, locked, target, result, nil
}

func renderUpgradeCheck(out fmtWriter, result upgradeCheckResult) {
	fmt.Fprintf(out, "Locked ref: %s\n", result.LockedRef)
	if result.LockedTemplate != "" || result.LockedVersion != "" {
		fmt.Fprintf(out, "Locked template: %s v%s\n", result.LockedTemplate, result.LockedVersion)
	}
	fmt.Fprintf(out, "Locked hash: %s\n", result.LockedHash)
	fmt.Fprintf(out, "Target ref: %s\n", result.TargetRef)
	if result.TargetTemplate != "" || result.TargetVersion != "" {
		fmt.Fprintf(out, "Target template: %s v%s\n", result.TargetTemplate, result.TargetVersion)
	}
	fmt.Fprintf(out, "Target hash: %s\n", result.TargetHash)
	if result.UpToDate {
		fmt.Fprintln(out, "agent-team upgrade: already up to date")
		return
	}
	fmt.Fprintln(out, "agent-team upgrade: template differs; run `agent-team upgrade --apply --dry-run` to preview clean changes and conflicts")
}

func renderUpgradeApply(out fmtWriter, result upgradeApplyResult) {
	renderUpgradeCheck(out, result.upgradeCheckResult)
	if result.DryRun {
		fmt.Fprintln(out, "agent-team upgrade: dry-run plan")
	}
	if len(result.Actions) == 0 {
		fmt.Fprintln(out, "agent-team upgrade: no file changes")
		return
	}
	for _, action := range result.Actions {
		switch action.Action {
		case "add":
			fmt.Fprintf(out, "  + %s\n", action.Path)
		case "update":
			fmt.Fprintf(out, "  ~ %s\n", action.Path)
		case "remove":
			fmt.Fprintf(out, "  - %s\n", action.Path)
		case "conflict":
			fmt.Fprintf(out, "  ! %s (%s)\n", action.Path, action.Reason)
		}
	}
	if result.Conflicts > 0 {
		fmt.Fprintf(out, "agent-team upgrade: %d conflict(s); no files changed\n", result.Conflicts)
		return
	}
	if result.DryRun {
		fmt.Fprintln(out, "agent-team upgrade: clean dry-run; rerun without --dry-run to apply")
		return
	}
	if result.Applied {
		fmt.Fprintln(out, "agent-team upgrade: applied clean template changes")
	}
}

func planUpgradeApply(teamDir string, locked, target *template.ResolvedTemplate, check upgradeCheckResult, resolved template.Tree, dryRun bool) (upgradeApplyResult, error) {
	lockedRoot, cleanupLocked, err := renderUpgradeSnapshot(locked, resolved)
	if err != nil {
		return upgradeApplyResult{}, err
	}
	defer cleanupLocked()
	targetRoot, cleanupTarget, err := renderUpgradeSnapshot(target, resolved)
	if err != nil {
		return upgradeApplyResult{}, err
	}
	defer cleanupTarget()

	oldFiles, _, err := readUpgradeSnapshotFiles(lockedRoot)
	if err != nil {
		return upgradeApplyResult{}, err
	}
	newFiles, _, err := readUpgradeSnapshotFiles(targetRoot)
	if err != nil {
		return upgradeApplyResult{}, err
	}
	currentFiles, _, err := readUpgradeSnapshotFiles(teamDir)
	if err != nil {
		return upgradeApplyResult{}, err
	}

	paths := map[string]bool{}
	for path := range oldFiles {
		paths[path] = true
	}
	for path := range newFiles {
		paths[path] = true
	}
	sorted := make([]string, 0, len(paths))
	for path := range paths {
		sorted = append(sorted, path)
	}
	sort.Strings(sorted)

	result := upgradeApplyResult{
		upgradeCheckResult: check,
		DryRun:             dryRun,
		Actions:            []upgradeApplyAction{},
	}
	for _, path := range sorted {
		oldBody, oldOK := oldFiles[path]
		newBody, newOK := newFiles[path]
		currentBody, currentOK := currentFiles[path]

		switch {
		case !oldOK && newOK:
			if !currentOK {
				result.addUpgradeAction(path, "add", "")
			} else if bytes.Equal(currentBody, newBody) {
				result.Unchanged++
			} else {
				result.addUpgradeAction(path, "conflict", "file exists locally but was not in the locked template")
			}
		case oldOK && !newOK:
			if !currentOK {
				result.Unchanged++
			} else if bytes.Equal(currentBody, oldBody) {
				result.addUpgradeAction(path, "remove", "")
			} else {
				result.addUpgradeAction(path, "conflict", "local edits to a file removed by the target template")
			}
		case oldOK && newOK:
			if bytes.Equal(oldBody, newBody) {
				result.Unchanged++
				continue
			}
			if !currentOK {
				result.addUpgradeAction(path, "conflict", "file was deleted locally but changed in the target template")
			} else if bytes.Equal(currentBody, oldBody) {
				result.addUpgradeAction(path, "update", "")
			} else if bytes.Equal(currentBody, newBody) {
				result.Unchanged++
			} else {
				result.addUpgradeAction(path, "conflict", "local edits overlap a target template change")
			}
		}
	}
	return result, nil
}

func (r *upgradeApplyResult) addUpgradeAction(path, action, reason string) {
	r.Actions = append(r.Actions, upgradeApplyAction{
		Path:   filepath.ToSlash(path),
		Action: action,
		Reason: reason,
	})
	switch action {
	case "add":
		r.Added++
	case "update":
		r.Updated++
	case "remove":
		r.Removed++
	case "conflict":
		r.Conflicts++
	}
}

func renderUpgradeSnapshot(rt *template.ResolvedTemplate, resolved template.Tree) (string, func(), error) {
	root, err := os.MkdirTemp("", "agent-team-upgrade-render-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	if _, err := template.RenderTreeFromFS(rt.FS, rt.Root, root, resolved, templateAuxFiles); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return root, cleanup, nil
}

func readUpgradeSnapshotFiles(root string) (map[string][]byte, map[string]os.FileMode, error) {
	files := map[string][]byte{}
	modes := map[string]os.FileMode{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == "__pycache__" || name == ".mypy_cache" || name == ".pytest_cache" || name == ".ruff_cache" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "config.toml" || rel == template.LockFileName {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files[rel] = body
		modes[rel] = info.Mode().Perm()
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return files, modes, nil
}

func applyUpgradePlan(teamDir string, target *template.ResolvedTemplate, actions []upgradeApplyAction) error {
	resolved, err := template.LoadTOMLFile(filepath.Join(teamDir, "config.toml"))
	if err != nil {
		return err
	}
	targetRoot, cleanupTarget, err := renderUpgradeSnapshot(target, resolved)
	if err != nil {
		return err
	}
	defer cleanupTarget()
	newFiles, modes, err := readUpgradeSnapshotFiles(targetRoot)
	if err != nil {
		return err
	}
	for _, action := range actions {
		dst := filepath.Join(teamDir, filepath.FromSlash(action.Path))
		switch action.Action {
		case "add", "update":
			body, ok := newFiles[action.Path]
			if !ok {
				return fmt.Errorf("upgrade apply: missing rendered target file %s", action.Path)
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			mode := modes[action.Path]
			if mode == 0 {
				mode = 0o644
			}
			if err := os.WriteFile(dst, body, mode); err != nil {
				return err
			}
		case "remove":
			if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
				return err
			}
			pruneEmptyUpgradeDirs(teamDir, filepath.Dir(dst))
		}
	}
	body, err := templateLockBytes(target)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(teamDir, template.LockFileName), body, 0o644)
}

func pruneEmptyUpgradeDirs(root, dir string) {
	root = filepath.Clean(root)
	for {
		dir = filepath.Clean(dir)
		if dir == root || !strings.HasPrefix(dir, root+string(os.PathSeparator)) {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

func parseUpgradeCheckFormat(format string) (*texttemplate.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := texttemplate.New("upgrade-check-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderUpgradeCheckFormat(out fmtWriter, result any, tmpl *texttemplate.Template) error {
	if err := tmpl.Execute(out, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(out)
	return err
}
