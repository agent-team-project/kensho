package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
			"binary; additional templates are pulled from local paths into a local cache.",
	}
	cmd.AddCommand(newTemplateLsCmd())
	cmd.AddCommand(newTemplateShowCmd())
	cmd.AddCommand(newTemplatePullCmd())
	cmd.AddCommand(newTemplateRmCmd())
	cmd.AddCommand(newTemplateRunCmd())
	return cmd
}

func newTemplateLsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "ls",
		Short: "List bundled and cached templates.",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := newResolver()

			// Bundled
			rt, err := r.Resolve(template.BundledRef)
			if err != nil {
				return fmt.Errorf("resolve bundled: %w", err)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s\t%s\t%s\n", "REF", "VERSION", "NAME")
			ver, name := manifestSummary(rt.Manifest)
			fmt.Fprintf(out, "%s\t%s\t%s\n", template.BundledRef, ver, name)

			// Cached
			cacheEntries, err := listCachedRefs(r.CacheRoot)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			for _, ref := range cacheEntries {
				cached, err := r.Resolve(ref)
				if err != nil {
					fmt.Fprintf(out, "%s\t(unreadable: %v)\n", ref, err)
					continue
				}
				ver, name := manifestSummary(cached.Manifest)
				fmt.Fprintf(out, "%s\t%s\t%s\n", ref, ver, name)
			}
			return nil
		},
	}
	return c
}

func manifestSummary(m *template.Manifest) (version, name string) {
	if m == nil {
		return "(no manifest)", ""
	}
	return m.Template.Version, m.Template.Name
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
	c := &cobra.Command{
		Use:   "show [ref]",
		Short: "Print a template's manifest. Default ref: bundled.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			return printManifest(cmd, rt)
		},
	}
	return c
}

func printManifest(cmd *cobra.Command, rt *template.ResolvedTemplate) error {
	out := cmd.OutOrStdout()
	hash, err := template.ContentHash(rt)
	if err != nil {
		return fmt.Errorf("hash template source: %w", err)
	}
	if rt.Manifest == nil {
		fmt.Fprintf(out, "Ref: %s\nContent hash: %s\n(no template.toml manifest — verbatim copy only)\n", rt.Ref, hash)
		return nil
	}
	m := rt.Manifest
	fmt.Fprintf(out, "Template: %s v%s\n", m.Template.Name, m.Template.Version)
	if m.Template.Description != "" {
		fmt.Fprintf(out, "Description: %s\n", m.Template.Description)
	}
	fmt.Fprintf(out, "Ref: %s\n", rt.Ref)
	fmt.Fprintf(out, "Content hash: %s\n\n", hash)

	if len(m.Parameters) == 0 {
		fmt.Fprintln(out, "Parameters: (none)")
	} else {
		fmt.Fprintln(out, "Parameters:")
		for _, p := range m.Parameters {
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
	agents, skills := listAgentsAndSkills(rt)
	if len(agents) > 0 {
		fmt.Fprintf(out, "Agents in this template: %s\n", strings.Join(agents, ", "))
	}
	if len(skills) > 0 {
		fmt.Fprintf(out, "Skills in this template: %s\n", strings.Join(skills, ", "))
	}
	return nil
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
	var asRef string
	c := &cobra.Command{
		Use:   "pull <ref>",
		Short: "Copy a local template into the cache so it can be referenced later.",
		Long: "Pull a template into ~/.agent-team/cache/<ref>. Today this only supports local-path " +
			"sources — point at a directory on disk and it gets copied. Bundled templates need no pull " +
			"(they're embedded in the binary). Git-URL refs are a planned follow-up.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := args[0]
			if ref == template.BundledRef {
				fmt.Fprintln(cmd.OutOrStdout(), "bundled template needs no pull (embedded in the binary)")
				return nil
			}
			r := newResolver()

			st, err := os.Stat(ref)
			if err != nil || !st.IsDir() {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: pull only supports local directories today; %q is not a directory\n", ref)
				return exitErr(2)
			}
			rt, err := r.Resolve(ref)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			cacheKey := asRef
			if cacheKey == "" {
				cacheKey = inferCacheKey(rt)
			}
			dst := filepath.Join(r.CacheRoot, cacheKey)
			if err := copyDirAll(rt.OnDiskRoot, dst); err != nil {
				return fmt.Errorf("copy to cache: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "pulled %s into %s\n", ref, dst)
			return nil
		},
	}
	c.Flags().StringVar(&asRef, "as", "", "Cache key to store under (defaults to <name>@<version> from manifest, or basename).")
	return c
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
	c := &cobra.Command{
		Use:   "rm <ref>",
		Short: "Remove a template from the cache.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := args[0]
			if ref == template.BundledRef {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: cannot rm the bundled template (it's compiled into the binary)")
				return exitErr(2)
			}
			r := newResolver()
			dst := filepath.Join(r.CacheRoot, ref)
			st, err := os.Stat(dst)
			if err != nil || !st.IsDir() {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: ref %q not found in cache (%s)\n", ref, r.CacheRoot)
				return exitErr(2)
			}
			if err := os.RemoveAll(dst); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", dst)
			return nil
		},
	}
	return c
}
