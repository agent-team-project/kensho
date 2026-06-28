package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	texttemplate "text/template"

	agentteam "github.com/jamesaud/agent-team"
	"github.com/jamesaud/agent-team/internal/template"
	"github.com/spf13/cobra"
)

// defaultCacheRoot returns the path under which `template pull` deposits
// cached templates. Falls back to a hidden dir under cwd if HOME is unset.
func defaultCacheRoot() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return filepath.Join(h, ".agent-team", "cache")
	}
	return ".agent-team-cache"
}

// newResolver wires up the template.Resolver with the binary's embedded
// "default" template and the user's cache root.
func newResolver() *template.Resolver {
	return &template.Resolver{
		BundledFS:   agentteam.TemplateFS(),
		BundledRoot: agentteam.TemplateRoot,
		CacheRoot:   defaultCacheRoot(),
	}
}

func newTemplateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "template",
		Short: "Manage templates (bundled + cached) used by `agent-team init`.",
		Long: "Manage templates: list, inspect, pull, and remove. A template is a parameterised " +
			"directory tree with a `template.toml` manifest. The default template is embedded in the " +
			"binary and can be referenced as `bundled` or `default`; additional templates are pulled " +
			"from local paths into a local cache.",
	}
	cmd.AddCommand(newTemplateLsCmd())
	cmd.AddCommand(newTemplateShowCmd())
	cmd.AddCommand(newTemplatePullCmd())
	cmd.AddCommand(newTemplateRmCmd())
	cmd.AddCommand(newTemplateSmokeCmd())
	cmd.AddCommand(newTemplateRunCmd())
	return cmd
}

func newTemplateLsCmd() *cobra.Command {
	var (
		jsonOut bool
		format  string
	)
	c := &cobra.Command{
		Use:   "ls",
		Short: "List bundled and cached templates.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team template ls: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseTemplateCLIFormat("template-ls-format", format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team template ls: %v\n", err)
				return exitErr(2)
			}
			r := newResolver()
			rows, err := collectTemplateList(r)
			if err != nil {
				return err
			}
			return renderTemplateList(cmd.OutOrStdout(), rows, jsonOut, tmpl)
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	c.Flags().StringVar(&format, "format", "", "Render each template row with a Go template, e.g. '{{.Ref}} {{.Version}}'.")
	return c
}

type templateListRow struct {
	Ref        string `json:"ref"`
	Version    string `json:"version"`
	Name       string `json:"name"`
	Bundled    bool   `json:"bundled"`
	Cached     bool   `json:"cached"`
	Path       string `json:"path,omitempty"`
	Unreadable string `json:"unreadable,omitempty"`
}

func manifestSummary(m *template.Manifest) (version, name string) {
	if m == nil {
		return "(no manifest)", ""
	}
	return m.Template.Version, m.Template.Name
}

func collectTemplateList(r *template.Resolver) ([]templateListRow, error) {
	rt, err := r.Resolve(template.BundledRef)
	if err != nil {
		return nil, fmt.Errorf("resolve bundled: %w", err)
	}
	rows := []templateListRow{templateListRowFromResolved(template.BundledRef, rt, true)}
	cacheEntries, err := listCachedRefs(r.CacheRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return rows, nil
		}
		return nil, err
	}
	for _, ref := range cacheEntries {
		cached, err := r.Resolve(ref)
		if err != nil {
			rows = append(rows, templateListRow{
				Ref:        ref,
				Cached:     true,
				Path:       filepath.ToSlash(filepath.Join(r.CacheRoot, filepath.FromSlash(ref))),
				Unreadable: err.Error(),
			})
			continue
		}
		rows = append(rows, templateListRowFromResolved(ref, cached, false))
	}
	return rows, nil
}

func templateListRowFromResolved(ref string, rt *template.ResolvedTemplate, bundled bool) templateListRow {
	ver, name := manifestSummary(rt.Manifest)
	row := templateListRow{
		Ref:     ref,
		Version: ver,
		Name:    name,
		Bundled: bundled,
		Cached:  !bundled,
	}
	if rt.OnDiskRoot != "" {
		row.Path = filepath.ToSlash(rt.OnDiskRoot)
	}
	return row
}

func renderTemplateList(w io.Writer, rows []templateListRow, jsonOut bool, tmpl *texttemplate.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(rows)
	}
	if tmpl != nil {
		for _, row := range rows {
			if err := tmpl.Execute(w, row); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	fmt.Fprintf(w, "%s\t%s\t%s\n", "REF", "VERSION", "NAME")
	for _, row := range rows {
		if row.Unreadable != "" {
			fmt.Fprintf(w, "%s\t(unreadable: %s)\n", row.Ref, row.Unreadable)
			continue
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", row.Ref, row.Version, row.Name)
	}
	return nil
}

// listCachedRefs walks `cacheRoot` and returns ref-shaped paths
// (`<host>/<owner>/<repo>@<version>` or any directory containing
// `template.toml`). One pass — anywhere a `template.toml` is found, the
// path-relative-to-cacheRoot is the ref.
func listCachedRefs(cacheRoot string) ([]string, error) {
	st, err := os.Stat(cacheRoot)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, nil
	}
	var out []string
	err = filepath.WalkDir(cacheRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() != template.ManifestFileName {
			return nil
		}
		dir := filepath.Dir(p)
		rel, err := filepath.Rel(cacheRoot, dir)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func newTemplateShowCmd() *cobra.Command {
	var (
		jsonOut bool
		format  string
	)
	c := &cobra.Command{
		Use:   "show [ref]",
		Short: "Print a template's manifest. Default ref: bundled (alias: default).",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team template show: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseTemplateCLIFormat("template-show-format", format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team template show: %v\n", err)
				return exitErr(2)
			}
			ref := template.BundledRef
			if len(args) == 1 {
				ref = args[0]
			}
			r := newResolver()
			rt, err := r.Resolve(ref)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			result, err := templateShowResultFromResolved(rt)
			if err != nil {
				return err
			}
			return renderTemplateShow(cmd.OutOrStdout(), result, jsonOut, tmpl)
		},
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	c.Flags().StringVar(&format, "format", "", "Render the template summary with a Go template, e.g. '{{.Ref}} {{.ContentHash}}'.")
	return c
}

func newTemplateSmokeCmd() *cobra.Command {
	var (
		setFlags       []string
		keep           bool
		jsonOut        bool
		format         string
		strictDaemon   bool
		strictRuntime  bool
		strictTemplate bool
	)
	c := &cobra.Command{
		Use:   "smoke [ref]",
		Short: "Render a template into a temp repo and validate it.",
		Long:  "Render a template into a temporary repo with init --no-input semantics, then run doctor, pipeline doctor, and team doctor. Pass --set for required parameters.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team template smoke: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseTemplateCLIFormat("template-smoke-format", format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team template smoke: %v\n", err)
				return exitErr(2)
			}
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			result, err := runTemplateSmoke(cmd, ref, setFlags, templateSmokeOptions{
				Keep:           keep,
				StrictDaemon:   strictDaemon,
				StrictRuntime:  strictRuntime,
				StrictTemplate: strictTemplate,
			})
			if err != nil {
				return err
			}
			if err := renderTemplateSmoke(cmd.OutOrStdout(), result, jsonOut, tmpl); err != nil {
				return err
			}
			if !result.OK {
				return exitErr(1)
			}
			return nil
		},
	}
	c.Flags().StringArrayVar(&setFlags, "set", nil, "Set a template parameter, e.g. --set linear.team_id=<uuid>. Repeatable.")
	c.Flags().BoolVar(&keep, "keep", false, "Keep the temporary rendered repo for inspection.")
	c.Flags().BoolVar(&jsonOut, "json", false, "Emit smoke results as JSON.")
	c.Flags().StringVar(&format, "format", "", "Render the smoke result with a Go template, e.g. '{{.OK}} {{len .Steps}}'.")
	c.Flags().BoolVar(&strictDaemon, "strict-daemon", false, "Fail doctor when the companion agent-teamd binary is not discoverable.")
	c.Flags().BoolVar(&strictRuntime, "strict-runtime", false, "Fail doctor when the selected LLM runtime binary or pipeline/team step runtime defaults are not discoverable.")
	c.Flags().BoolVar(&strictTemplate, "strict-template", false, "Fail doctor when rendered template provenance does not resolve cleanly.")
	return c
}

type templateSmokeOptions struct {
	Keep           bool
	StrictDaemon   bool
	StrictRuntime  bool
	StrictTemplate bool
}

type templateSmokeResult struct {
	OK             bool                  `json:"ok"`
	Ref            string                `json:"ref"`
	Target         string                `json:"target"`
	Kept           bool                  `json:"kept"`
	Steps          []templateSmokeStep   `json:"steps"`
	Doctor         *doctorResult         `json:"doctor,omitempty"`
	PipelineDoctor *pipelineDoctorResult `json:"pipeline_doctor,omitempty"`
	TeamDoctor     *allTeamDoctorResult  `json:"team_doctor,omitempty"`
}

type templateSmokeStep struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func runTemplateSmoke(cmd *cobra.Command, ref string, sets []string, opts templateSmokeOptions) (templateSmokeResult, error) {
	displayRef := strings.TrimSpace(ref)
	if displayRef == "" {
		displayRef = template.BundledRef
	}
	target, err := os.MkdirTemp("", "agent-team-template-smoke-")
	if err != nil {
		return templateSmokeResult{}, err
	}
	result := templateSmokeResult{
		OK:     true,
		Ref:    displayRef,
		Target: filepath.ToSlash(target),
		Kept:   opts.Keep,
	}
	defer func() {
		if !opts.Keep {
			_ = os.RemoveAll(target)
		}
	}()

	initStep := runTemplateSmokeInit(ref, target, sets)
	result.addStep(initStep)
	if !initStep.OK {
		result.OK = false
		return result, nil
	}

	teamDir := filepath.Join(target, teamDirName)
	doctor, doctorStep := runTemplateSmokeDoctor(target, opts)
	result.Doctor = doctor
	result.addStep(doctorStep)

	pipelineDoctor, pipelineStep := runTemplateSmokePipelineDoctor(teamDir, opts.StrictRuntime)
	result.PipelineDoctor = pipelineDoctor
	result.addStep(pipelineStep)

	teamDoctor, teamStep := runTemplateSmokeTeamDoctor(teamDir, opts.StrictRuntime)
	result.TeamDoctor = teamDoctor
	result.addStep(teamStep)

	for _, step := range result.Steps {
		if !step.OK {
			result.OK = false
			break
		}
	}
	return result, nil
}

func runTemplateSmokeInit(ref, target string, sets []string) templateSmokeStep {
	smokeCmd := &cobra.Command{}
	var out, stderr bytes.Buffer
	smokeCmd.SetOut(&out)
	smokeCmd.SetErr(&stderr)
	err := runInit(smokeCmd, initConfig{
		target:     target,
		kind:       "default",
		ref:        ref,
		setStrings: append([]string(nil), sets...),
		noInput:    true,
	})
	return templateSmokeStep{Name: "init", OK: err == nil, Error: smokeStepError(err, stderr.String())}
}

func runTemplateSmokeDoctor(target string, opts templateSmokeOptions) (*doctorResult, templateSmokeStep) {
	smokeCmd := &cobra.Command{}
	var out, stderr bytes.Buffer
	smokeCmd.SetOut(&out)
	smokeCmd.SetErr(&stderr)
	err := runDoctor(smokeCmd, target, opts.StrictDaemon, opts.StrictRuntime, opts.StrictTemplate, true, nil, runtimeSelection{})
	var result doctorResult
	if out.Len() > 0 {
		_ = json.Unmarshal(out.Bytes(), &result)
	}
	return &result, templateSmokeStep{Name: "doctor", OK: err == nil && result.OK, Error: smokeStepError(err, firstDoctorProblem(result.Problems, stderr.String()))}
}

func runTemplateSmokePipelineDoctor(teamDir string, strictRuntime bool) (*pipelineDoctorResult, templateSmokeStep) {
	result, err := collectPipelineDoctor(teamDir, "")
	if strictRuntime {
		promotePipelineDoctorRuntimeWarnings(result)
	}
	ok := err == nil && result != nil && result.OK
	return result, templateSmokeStep{Name: "pipeline doctor", OK: ok, Error: smokeStepError(err, firstPipelineProblem(result))}
}

func runTemplateSmokeTeamDoctor(teamDir string, strictRuntime bool) (*allTeamDoctorResult, templateSmokeStep) {
	result, err := collectAllTeamDoctor(teamDir)
	if strictRuntime {
		promoteAllTeamDoctorRuntimeWarnings(result)
	}
	ok := err == nil && result != nil && result.OK
	return result, templateSmokeStep{Name: "team doctor", OK: ok, Error: smokeStepError(err, firstTeamProblem(result))}
}

func (r *templateSmokeResult) addStep(step templateSmokeStep) {
	r.Steps = append(r.Steps, step)
}

func smokeStepError(err error, detail string) string {
	detail = strings.TrimSpace(detail)
	if detail != "" {
		return detail
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

func firstDoctorProblem(problems []string, fallback string) string {
	if len(problems) > 0 {
		return problems[0]
	}
	return fallback
}

func firstPipelineProblem(result *pipelineDoctorResult) string {
	if result == nil {
		return "pipeline doctor returned no result"
	}
	if len(result.Problems) > 0 {
		return result.Problems[0].Message
	}
	return ""
}

func firstTeamProblem(result *allTeamDoctorResult) string {
	if result == nil {
		return "team doctor returned no result"
	}
	if len(result.Problems) > 0 {
		return result.Problems[0].Message
	}
	return ""
}

func renderTemplateSmoke(w io.Writer, result templateSmokeResult, jsonOut bool, tmpl *texttemplate.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if tmpl != nil {
		if err := tmpl.Execute(w, result); err != nil {
			return err
		}
		_, err := fmt.Fprintln(w)
		return err
	}
	state := "OK"
	if !result.OK {
		state = "failed"
	}
	fmt.Fprintf(w, "agent-team template smoke: %s\n", state)
	fmt.Fprintf(w, "ref: %s\n", result.Ref)
	fmt.Fprintf(w, "target: %s\n", result.Target)
	if !result.Kept {
		fmt.Fprintln(w, "target removed after smoke")
	}
	fmt.Fprintln(w, "steps:")
	for _, step := range result.Steps {
		status := "OK"
		if !step.OK {
			status = "failed"
		}
		fmt.Fprintf(w, "  %s: %s\n", step.Name, status)
		if step.Error != "" {
			fmt.Fprintf(w, "    %s\n", step.Error)
		}
	}
	return nil
}

type templateShowResult struct {
	Ref         string                  `json:"ref"`
	ContentHash string                  `json:"content_hash"`
	HasManifest bool                    `json:"has_manifest"`
	Name        string                  `json:"name,omitempty"`
	Version     string                  `json:"version,omitempty"`
	Description string                  `json:"description,omitempty"`
	Path        string                  `json:"path,omitempty"`
	Parameters  []templateParameterInfo `json:"parameters,omitempty"`
	Agents      []string                `json:"agents"`
	Skills      []string                `json:"skills"`
}

type templateParameterInfo struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Default     any    `json:"default"`
	Pattern     string `json:"pattern,omitempty"`
	Description string `json:"description,omitempty"`
}

func templateShowResultFromResolved(rt *template.ResolvedTemplate) (templateShowResult, error) {
	hash, err := template.ContentHash(rt)
	if err != nil {
		return templateShowResult{}, fmt.Errorf("hash template source: %w", err)
	}
	agents, skills := listAgentsAndSkills(rt)
	result := templateShowResult{
		Ref:         rt.Ref,
		ContentHash: hash,
		Agents:      agents,
		Skills:      skills,
	}
	if rt.OnDiskRoot != "" {
		result.Path = filepath.ToSlash(rt.OnDiskRoot)
	}
	if rt.Manifest == nil {
		return result, nil
	}
	result.HasManifest = true
	m := rt.Manifest
	result.Name = m.Template.Name
	result.Version = m.Template.Version
	result.Description = m.Template.Description
	for _, p := range m.Parameters {
		result.Parameters = append(result.Parameters, templateParameterInfo{
			Key:         p.Key,
			Type:        string(p.Type),
			Required:    p.Required,
			Default:     p.Default,
			Pattern:     p.Pattern,
			Description: p.Description,
		})
	}
	return result, nil
}

func renderTemplateShow(w io.Writer, result templateShowResult, jsonOut bool, tmpl *texttemplate.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if tmpl != nil {
		if err := tmpl.Execute(w, result); err != nil {
			return err
		}
		_, err := fmt.Fprintln(w)
		return err
	}
	return renderTemplateShowText(w, result)
}

func renderTemplateShowText(out io.Writer, result templateShowResult) error {
	if !result.HasManifest {
		fmt.Fprintf(out, "Ref: %s\nContent hash: %s\n(no template.toml manifest - verbatim copy only)\n", result.Ref, result.ContentHash)
		return nil
	}
	fmt.Fprintf(out, "Template: %s v%s\n", result.Name, result.Version)
	if result.Description != "" {
		fmt.Fprintf(out, "Description: %s\n", result.Description)
	}
	fmt.Fprintf(out, "Ref: %s\n", result.Ref)
	fmt.Fprintf(out, "Content hash: %s\n\n", result.ContentHash)

	if len(result.Parameters) == 0 {
		fmt.Fprintln(out, "Parameters: (none)")
	} else {
		fmt.Fprintln(out, "Parameters:")
		for _, p := range result.Parameters {
			req := "optional"
			if p.Required {
				req = "required"
			}
			pat := ""
			if p.Pattern != "" {
				pat = fmt.Sprintf(", %s", p.Pattern)
			}
			def := ""
			if p.Default != nil {
				def = fmt.Sprintf(" (default: %v)", p.Default)
			}
			fmt.Fprintf(out, "  %s\t%s\t(%s%s)%s\t%s\n",
				p.Key, p.Type, req, pat, def, p.Description)
		}
	}

	fmt.Fprintln(out, "")
	if len(result.Agents) > 0 {
		fmt.Fprintf(out, "Agents in this template: %s\n", strings.Join(result.Agents, ", "))
	}
	if len(result.Skills) > 0 {
		fmt.Fprintf(out, "Skills in this template: %s\n", strings.Join(result.Skills, ", "))
	}
	return nil
}

func parseTemplateCLIFormat(name, format string) (*texttemplate.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := texttemplate.New(name).Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

// listAgentsAndSkills lists immediate children of <root>/agents and
// <root>/skills, ignoring missing dirs.
func listAgentsAndSkills(rt *template.ResolvedTemplate) (agents, skills []string) {
	agents = listChildDirs(rt.FS, rt.Root+"/agents")
	skills = listChildDirs(rt.FS, rt.Root+"/skills")
	return
}

func listChildDirs(srcFS fs.FS, dir string) []string {
	entries, err := fs.ReadDir(srcFS, dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func newTemplatePullCmd() *cobra.Command {
	var (
		asRef    string
		dryRun   bool
		commands bool
		jsonOut  bool
		format   string
	)
	c := &cobra.Command{
		Use:   "pull <ref>",
		Short: "Fetch a template into the cache so it can be referenced later.",
		Long: "Pull a template into ~/.agent-team/cache/<ref>. Local directory refs are copied. " +
			"Git refs such as github.com/acme/eng-team@v1.0.0 or https://github.com/acme/eng-team.git@v1.0.0 " +
			"are cloned at the requested revision. Bundled templates need no pull because they are embedded in the binary.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team template pull: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team template pull: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team template pull: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team template pull: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseTemplateCLIFormat("template-pull-format", format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team template pull: %v\n", err)
				return exitErr(2)
			}
			ref := args[0]
			if template.IsBundledRef(ref) {
				return renderTemplatePull(cmd.OutOrStdout(), templatePullResult{
					Ref:     template.BundledRef,
					Source:  "bundled",
					Bundled: true,
					DryRun:  dryRun,
					Action:  "noop",
				}, jsonOut, tmpl)
			}
			r := newResolver()

			st, err := os.Stat(ref)
			if err == nil && st.IsDir() {
				rt, err := r.Resolve(ref)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
					return exitErr(2)
				}
				cacheKey := asRef
				if cacheKey == "" {
					cacheKey = inferCacheKey(rt)
				}
				dst, err := cacheDestination(r.CacheRoot, cacheKey)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
					return exitErr(2)
				}
				result := templatePullResult{
					Ref:        ref,
					Source:     "local",
					CacheKey:   cacheKey,
					Path:       filepath.ToSlash(dst),
					SourcePath: filepath.ToSlash(rt.OnDiskRoot),
					DryRun:     dryRun,
					Pulled:     true,
					Action:     "pulled",
				}
				if dryRun {
					result.Action = "would-pull"
				} else if err := copyDirAll(rt.OnDiskRoot, dst); err != nil {
					return fmt.Errorf("copy to cache: %w", err)
				}
				if commands {
					return renderTemplatePullApplyCommand(cmd.OutOrStdout(), true, ref, asRef)
				}
				if err := renderTemplatePull(cmd.OutOrStdout(), result, jsonOut, tmpl); err != nil {
					return err
				}
				return nil
			}

			gitRef, ok, parseErr := parseGitTemplateRef(ref)
			if parseErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", parseErr)
				return exitErr(2)
			}
			if !ok {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %q is not a local template directory or supported git ref\n", ref)
				return exitErr(2)
			}
			cacheKey := asRef
			if cacheKey == "" {
				cacheKey = gitRef.CacheKey
			}
			dst, err := cacheDestination(r.CacheRoot, cacheKey)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			result := templatePullResult{
				Ref:      ref,
				Source:   "git",
				CacheKey: cacheKey,
				Path:     filepath.ToSlash(dst),
				CloneURL: gitRef.CloneURL,
				Revision: gitRef.Revision,
				DryRun:   dryRun,
				Pulled:   true,
				Action:   "pulled",
			}
			if isMutableGitRevision(gitRef.Revision) {
				result.MutableRevision = true
				result.Warning = fmt.Sprintf("pulling mutable git revision %q; prefer an immutable tag or commit", gitRef.Revision)
				if !jsonOut && tmpl == nil && !commands {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: warning: %s\n", result.Warning)
				}
			}
			if dryRun {
				result.Action = "would-pull"
			} else if err := cloneGitTemplate(gitRef, r.CacheRoot, dst); err != nil {
				return err
			}
			if commands {
				return renderTemplatePullApplyCommand(cmd.OutOrStdout(), true, ref, asRef)
			}
			if err := renderTemplatePull(cmd.OutOrStdout(), result, jsonOut, tmpl); err != nil {
				return err
			}
			return nil
		},
	}
	c.Flags().StringVar(&asRef, "as", "", "Cache key to store under (defaults to <name>@<version> from manifest, or basename).")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Preview template pull without copying or cloning into the cache.")
	c.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching template pull apply command when the preview has actionable work.")
	c.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	c.Flags().StringVar(&format, "format", "", "Render the pull result with a Go template, e.g. '{{.CacheKey}} {{.Action}}'.")
	return c
}

type templatePullResult struct {
	Ref             string `json:"ref"`
	Source          string `json:"source"`
	CacheKey        string `json:"cache_key,omitempty"`
	Path            string `json:"path,omitempty"`
	SourcePath      string `json:"source_path,omitempty"`
	CloneURL        string `json:"clone_url,omitempty"`
	Revision        string `json:"revision,omitempty"`
	Bundled         bool   `json:"bundled,omitempty"`
	MutableRevision bool   `json:"mutable_revision,omitempty"`
	Warning         string `json:"warning,omitempty"`
	DryRun          bool   `json:"dry_run"`
	Pulled          bool   `json:"pulled"`
	Action          string `json:"action"`
}

func renderTemplatePull(w io.Writer, result templatePullResult, jsonOut bool, tmpl *texttemplate.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if tmpl != nil {
		if err := tmpl.Execute(w, result); err != nil {
			return err
		}
		_, err := fmt.Fprintln(w)
		return err
	}
	if result.Action == "noop" {
		fmt.Fprintln(w, "bundled template needs no pull (embedded in the binary)")
		return nil
	}
	if result.DryRun {
		fmt.Fprintf(w, "would pull %s into %s\n", result.Ref, result.Path)
		return nil
	}
	fmt.Fprintf(w, "pulled %s into %s\n", result.Ref, result.Path)
	return nil
}

func renderTemplatePullApplyCommand(w io.Writer, hasAction bool, ref, asRef string) error {
	if !hasAction {
		return nil
	}
	_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(templatePullApplyCommandArgs(ref, asRef)), " "))
	return err
}

func templatePullApplyCommandArgs(ref, asRef string) []string {
	args := []string{"agent-team", "template", "pull", ref}
	if asRef != "" {
		args = append(args, "--as", asRef)
	}
	return args
}

type gitTemplateRef struct {
	Input    string
	Source   string
	CloneURL string
	Revision string
	CacheKey string
}

func parseGitTemplateRef(ref string) (gitTemplateRef, bool, error) {
	ref = strings.TrimSpace(ref)
	at := strings.LastIndex(ref, "@")
	if at <= 0 || at == len(ref)-1 {
		if strings.Contains(ref, "://") || strings.HasPrefix(ref, "git@") {
			return gitTemplateRef{}, false, fmt.Errorf("git template ref %q must include @<revision>", ref)
		}
		return gitTemplateRef{}, false, nil
	}
	source := strings.TrimSpace(ref[:at])
	revision := strings.TrimSpace(ref[at+1:])
	if source == "" || revision == "" {
		return gitTemplateRef{}, false, fmt.Errorf("git template ref %q must include source and revision", ref)
	}
	cloneURL, cacheSource, ok, err := normalizeGitTemplateSource(source)
	if err != nil || !ok {
		return gitTemplateRef{}, ok, err
	}
	return gitTemplateRef{
		Input:    ref,
		Source:   source,
		CloneURL: cloneURL,
		Revision: revision,
		CacheKey: cacheSource + "@" + revision,
	}, true, nil
}

func normalizeGitTemplateSource(source string) (cloneURL, cacheSource string, ok bool, err error) {
	if strings.Contains(source, "://") {
		u, parseErr := url.Parse(source)
		if parseErr != nil {
			return "", "", true, fmt.Errorf("git template ref %q: %w", source, parseErr)
		}
		switch u.Scheme {
		case "http", "https", "ssh":
			cacheSource = strings.TrimPrefix(u.Host+strings.TrimSuffix(u.EscapedPath(), ".git"), "/")
		case "file":
			path := filepath.ToSlash(filepath.Clean(u.Path))
			cacheSource = "file/" + strings.Trim(strings.TrimSuffix(path, ".git"), "/")
		default:
			return "", "", true, fmt.Errorf("git template ref %q: unsupported scheme %q", source, u.Scheme)
		}
		cacheSource = strings.Trim(cacheSource, "/")
		if cacheSource == "" {
			return "", "", true, fmt.Errorf("git template ref %q: could not infer cache key", source)
		}
		return source, cacheSource, true, nil
	}
	if strings.HasPrefix(source, "git@") {
		colon := strings.Index(source, ":")
		if colon < 0 {
			return "", "", true, fmt.Errorf("git template ref %q: expected git@host:path form", source)
		}
		host := strings.TrimPrefix(source[:colon], "git@")
		path := strings.TrimSuffix(source[colon+1:], ".git")
		if host == "" || path == "" {
			return "", "", true, fmt.Errorf("git template ref %q: expected git@host:path form", source)
		}
		return source, host + "/" + strings.Trim(path, "/"), true, nil
	}
	parts := strings.Split(source, "/")
	if len(parts) >= 3 && (strings.Contains(parts[0], ".") || parts[0] == "localhost") {
		return "https://" + source, strings.TrimSuffix(source, ".git"), true, nil
	}
	return "", "", false, nil
}

func cacheDestination(cacheRoot, cacheKey string) (string, error) {
	rawKey := strings.TrimSpace(cacheKey)
	if rawKey == "" {
		return "", fmt.Errorf("cache key is required")
	}
	for _, part := range strings.FieldsFunc(rawKey, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return "", fmt.Errorf("cache key %q cannot contain '..'", rawKey)
		}
	}
	cacheKey = filepath.Clean(filepath.FromSlash(rawKey))
	if cacheKey == "." || cacheKey == "" {
		return "", fmt.Errorf("cache key is required")
	}
	if filepath.IsAbs(cacheKey) {
		return "", fmt.Errorf("cache key %q must be relative", cacheKey)
	}
	return filepath.Join(cacheRoot, cacheKey), nil
}

func cloneGitTemplate(ref gitTemplateRef, cacheRoot, dst string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git is required to pull %s: %w", ref.Input, err)
	}
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(cacheRoot, ".pull-*")
	if err != nil {
		return err
	}
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.RemoveAll(tmp)
		}
	}()
	if err := runGitClone(ref.CloneURL, ref.Revision, tmp); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(tmp, ".git")); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	removeTmp = false
	return nil
}

func runGitClone(cloneURL, revision, dst string) error {
	cmd := exec.Command("git", "clone", "--depth", "1", "--branch", revision, cloneURL, dst)
	if _, err := cmd.CombinedOutput(); err == nil {
		return nil
	} else {
		_ = os.RemoveAll(dst)
		if mkdirErr := os.MkdirAll(dst, 0o755); mkdirErr != nil {
			return mkdirErr
		}
		full := exec.Command("git", "clone", cloneURL, dst)
		if fullOut, fullErr := full.CombinedOutput(); fullErr != nil {
			return fmt.Errorf("git clone: %w: %s", fullErr, strings.TrimSpace(string(fullOut)))
		}
		checkout := exec.Command("git", "-C", dst, "checkout", revision)
		if checkoutOut, checkoutErr := checkout.CombinedOutput(); checkoutErr != nil {
			return fmt.Errorf("git checkout %s: %w: %s", revision, checkoutErr, strings.TrimSpace(string(checkoutOut)))
		}
		return nil
	}
}

func isMutableGitRevision(revision string) bool {
	switch strings.ToLower(strings.TrimSpace(revision)) {
	case "head", "main", "master", "trunk", "develop", "dev":
		return true
	default:
		return false
	}
}

func inferCacheKey(rt *template.ResolvedTemplate) string {
	if rt.Manifest != nil && rt.Manifest.Template.Name != "" {
		v := rt.Manifest.Template.Version
		if v == "" {
			v = "unversioned"
		}
		return fmt.Sprintf("%s@%s", rt.Manifest.Template.Name, v)
	}
	return filepath.Base(rt.OnDiskRoot)
}

// copyDirAll mirrors `cp -r src/. dst/` — overwrites files, creates dirs.
// Skips symlinks at the source (we only ship plain files in templates).
func copyDirAll(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(out, body, info.Mode().Perm())
	})
}

func newTemplateRmCmd() *cobra.Command {
	var (
		dryRun   bool
		commands bool
		jsonOut  bool
		format   string
	)
	c := &cobra.Command{
		Use:   "rm <ref>",
		Short: "Remove a template from the cache.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team template rm: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team template rm: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team template rm: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team template rm: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseTemplateCLIFormat("template-rm-format", format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team template rm: %v\n", err)
				return exitErr(2)
			}
			ref := args[0]
			if template.IsBundledRef(ref) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: cannot rm the bundled template (it's compiled into the binary)")
				return exitErr(2)
			}
			r := newResolver()
			result, err := runTemplateRm(cmd.OutOrStdout(), r.CacheRoot, ref, templateRmOptions{
				DryRun: dryRun,
				Quiet:  commands,
				JSON:   jsonOut,
				Format: tmpl,
			})
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			if commands {
				return renderTemplateRmApplyCommand(cmd.OutOrStdout(), result.DryRun && result.Removed, ref)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Preview template removal without deleting it.")
	c.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching template rm apply command when the preview has actionable work.")
	c.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	c.Flags().StringVar(&format, "format", "", "Render the removal result with a Go template, e.g. '{{.Ref}} {{.Action}}'.")
	return c
}

type templateRmOptions struct {
	DryRun bool
	Quiet  bool
	JSON   bool
	Format *texttemplate.Template
}

type templateRmResult struct {
	Ref     string `json:"ref"`
	Path    string `json:"path"`
	DryRun  bool   `json:"dry_run"`
	Removed bool   `json:"removed"`
	Action  string `json:"action"`
}

func runTemplateRm(stdout io.Writer, cacheRoot, ref string, opts templateRmOptions) (templateRmResult, error) {
	dst, err := cacheDestination(cacheRoot, ref)
	if err != nil {
		return templateRmResult{}, err
	}
	st, err := os.Stat(dst)
	if err != nil || !st.IsDir() {
		return templateRmResult{}, fmt.Errorf("ref %q not found in cache (%s)", ref, cacheRoot)
	}
	result := templateRmResult{
		Ref:     ref,
		Path:    filepath.ToSlash(dst),
		DryRun:  opts.DryRun,
		Removed: true,
		Action:  "removed",
	}
	if opts.DryRun {
		result.Action = "would-remove"
	} else if err := os.RemoveAll(dst); err != nil {
		return templateRmResult{}, err
	}
	if opts.JSON {
		return result, json.NewEncoder(stdout).Encode(result)
	}
	if opts.Format != nil {
		if err := opts.Format.Execute(stdout, result); err != nil {
			return result, err
		}
		_, err := fmt.Fprintln(stdout)
		return result, err
	}
	if opts.Quiet {
		return result, nil
	}
	if opts.DryRun {
		fmt.Fprintf(stdout, "would remove %s\n", result.Path)
		return result, nil
	}
	fmt.Fprintf(stdout, "removed %s\n", result.Path)
	return result, nil
}

func renderTemplateRmApplyCommand(w io.Writer, hasAction bool, ref string) error {
	if !hasAction {
		return nil
	}
	_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs([]string{"agent-team", "template", "rm", ref}), " "))
	return err
}
