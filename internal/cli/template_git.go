package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jamesaud/agent-team/internal/template"
)

const (
	gitRevisionTag     = "tag"
	gitRevisionBranch  = "branch"
	gitRevisionCommit  = "commit"
	gitRevisionHead    = "head"
	gitRevisionUnknown = "unknown"
)

type gitTemplateRef struct {
	Input             string
	Source            string
	CloneURL          string
	Revision          string
	CacheSource       string
	DefaultedRevision bool
}

type gitTemplateCacheMeta struct {
	Ref               string `json:"ref"`
	Source            string `json:"source"`
	CloneURL          string `json:"clone_url"`
	Revision          string `json:"revision"`
	RevisionKind      string `json:"revision_kind"`
	DefaultedRevision bool   `json:"defaulted_revision,omitempty"`
	ResolvedSHA       string `json:"resolved_sha"`
	CacheKey          string `json:"cache_key"`
	PulledAt          string `json:"pulled_at"`
}

func parseGitTemplateRef(ref string) (gitTemplateRef, bool, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return gitTemplateRef{}, false, nil
	}

	var splitErr error
	if at := strings.LastIndex(ref, "@"); at > 0 && at < len(ref)-1 {
		source := strings.TrimSpace(ref[:at])
		revision := strings.TrimSpace(ref[at+1:])
		cloneURL, cacheSource, ok, err := normalizeGitTemplateSource(source)
		if err == nil && ok {
			return gitTemplateRef{
				Input:       ref,
				Source:      source,
				CloneURL:    cloneURL,
				Revision:    revision,
				CacheSource: cacheSource,
			}, true, nil
		}
		if err != nil {
			splitErr = err
		}
	}

	cloneURL, cacheSource, ok, err := normalizeGitTemplateSource(ref)
	if err != nil || ok {
		return gitTemplateRef{
			Input:       ref,
			Source:      ref,
			CloneURL:    cloneURL,
			CacheSource: cacheSource,
		}, ok, err
	}
	if splitErr != nil {
		return gitTemplateRef{}, false, splitErr
	}
	if strings.Contains(ref, "://") || strings.HasPrefix(ref, "git@") {
		return gitTemplateRef{}, false, fmt.Errorf("git template ref %q is not a supported git URL", ref)
	}
	return gitTemplateRef{}, false, nil
}

func normalizeGitTemplateSource(source string) (cloneURL, cacheSource string, ok bool, err error) {
	if strings.Contains(source, "://") {
		u, parseErr := url.Parse(source)
		if parseErr != nil {
			return "", "", true, fmt.Errorf("git template ref %q: %w", source, parseErr)
		}
		switch u.Scheme {
		case "http", "https", "ssh":
			path := strings.TrimSuffix(u.EscapedPath(), ".git")
			if u.Host == "" || strings.Trim(path, "/") == "" {
				return "", "", true, fmt.Errorf("git template ref %q: expected host and repository path", source)
			}
			cacheSource = strings.TrimPrefix(u.Host+path, "/")
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

func resolveTemplateRefForCLI(ref string) (*template.ResolvedTemplate, templatePullResult, error) {
	resolver := newResolver()
	if template.IsBundledRef(ref) || isLocalTemplateRef(ref) {
		rt, err := resolver.Resolve(ref)
		return rt, templatePullResult{}, err
	}
	gitRef, ok, err := parseGitTemplateRef(ref)
	if err != nil {
		return nil, templatePullResult{}, err
	}
	if ok {
		pull, err := pullGitTemplate(gitRef, resolver.CacheRoot, "", false)
		if err != nil {
			return nil, pull, err
		}
		rt, err := resolver.Resolve(pull.CacheKey)
		return rt, pull, err
	}
	rt, err := resolver.Resolve(ref)
	return rt, templatePullResult{}, err
}

func isLocalTemplateRef(ref string) bool {
	if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../") || strings.HasPrefix(ref, "/") {
		return true
	}
	st, err := os.Stat(ref)
	return err == nil && st.IsDir()
}

func pullGitTemplate(ref gitTemplateRef, cacheRoot, asRef string, dryRun bool) (templatePullResult, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return templatePullResult{}, fmt.Errorf("git is required to pull %s: %w", ref.Input, err)
	}
	if asRef == "" && ref.Revision != "" {
		if cached, ok := findCachedGitTemplate(cacheRoot, ref); ok {
			cached.DryRun = dryRun
			return cached, nil
		}
	}
	prepared, kind, defaultWarning, err := prepareGitTemplateRef(ref)
	if err != nil {
		return templatePullResult{}, err
	}
	ref = prepared

	if asRef == "" {
		if cached, ok := findCachedGitTemplate(cacheRoot, ref); ok {
			cached.DryRun = dryRun
			if dryRun {
				cached.Action = "cached"
			}
			if defaultWarning != "" {
				cached.Warning = defaultWarning
			}
			return cached, nil
		}
	}

	if dryRun {
		sha, resolvedKind, err := resolveGitRevisionSHA(ref.CloneURL, ref.Revision, kind)
		if err != nil {
			return templatePullResult{}, err
		}
		if resolvedKind != "" {
			kind = resolvedKind
		}
		cacheKey := asRef
		if cacheKey == "" {
			cacheKey = gitTemplateCacheKey(ref.CacheSource, sha)
		}
		dst, err := cacheDestination(cacheRoot, cacheKey)
		if err != nil {
			return templatePullResult{}, err
		}
		result := gitTemplatePullResult(ref, cacheKey, dst, sha, kind)
		result.DryRun = true
		result.Pulled = true
		result.Action = "would-pull"
		result.Warning = gitTemplateWarning(result, defaultWarning)
		return result, nil
	}

	tmp, sha, err := fetchGitTemplateToTemp(ref, cacheRoot)
	if err != nil {
		return templatePullResult{}, err
	}
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.RemoveAll(tmp)
		}
	}()

	cacheKey := asRef
	if cacheKey == "" {
		cacheKey = gitTemplateCacheKey(ref.CacheSource, sha)
	}
	dst, err := cacheDestination(cacheRoot, cacheKey)
	if err != nil {
		return templatePullResult{}, err
	}
	if asRef == "" {
		if st, err := os.Stat(dst); err == nil && st.IsDir() {
			removeTmp = true
			result := gitTemplatePullResult(ref, cacheKey, dst, sha, kind)
			result.Cached = true
			result.Action = "cached"
			result.Warning = gitTemplateWarning(result, defaultWarning)
			return result, nil
		}
	}
	if err := os.RemoveAll(filepath.Join(tmp, ".git")); err != nil {
		return templatePullResult{}, err
	}
	if err := writeGitTemplateCacheMeta(tmp, gitTemplateCacheMeta{
		Ref:               ref.Input,
		Source:            ref.CacheSource,
		CloneURL:          ref.CloneURL,
		Revision:          ref.Revision,
		RevisionKind:      kind,
		DefaultedRevision: ref.DefaultedRevision,
		ResolvedSHA:       sha,
		CacheKey:          cacheKey,
		PulledAt:          time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		return templatePullResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return templatePullResult{}, err
	}
	if err := os.RemoveAll(dst); err != nil {
		return templatePullResult{}, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return templatePullResult{}, err
	}
	removeTmp = false

	result := gitTemplatePullResult(ref, cacheKey, dst, sha, kind)
	result.Pulled = true
	result.Action = "pulled"
	result.Warning = gitTemplateWarning(result, defaultWarning)
	return result, nil
}

func prepareGitTemplateRef(ref gitTemplateRef) (gitTemplateRef, string, string, error) {
	if ref.Revision == "" {
		tag, ok, err := latestGitTag(ref.CloneURL)
		if err != nil {
			return ref, "", "", err
		}
		if ok {
			ref.Revision = tag
			ref.DefaultedRevision = true
			ref.Input = ref.Source + "@" + tag
			return ref, gitRevisionTag, "", nil
		}
		ref.Revision = "HEAD"
		ref.DefaultedRevision = true
		ref.Input = ref.Source + "@HEAD"
		return ref, gitRevisionHead, fmt.Sprintf("no tags found for %s; defaulting to HEAD", ref.Source), nil
	}
	kind := classifyGitRevision(ref.CloneURL, ref.Revision)
	return ref, kind, "", nil
}

func gitTemplatePullResult(ref gitTemplateRef, cacheKey, dst, sha, kind string) templatePullResult {
	return templatePullResult{
		Ref:               ref.Input,
		Source:            "git",
		CacheKey:          cacheKey,
		Path:              filepath.ToSlash(dst),
		CloneURL:          ref.CloneURL,
		Revision:          ref.Revision,
		RevisionKind:      kind,
		DefaultedRevision: ref.DefaultedRevision,
		ResolvedSHA:       sha,
		MutableRevision:   isMutableGitRevisionKind(kind, ref.Revision),
	}
}

func gitTemplateWarning(result templatePullResult, existing string) string {
	if existing != "" {
		return existing
	}
	if result.MutableRevision {
		return fmt.Sprintf("pulling mutable git revision %q; prefer an immutable tag or commit", result.Revision)
	}
	return ""
}

func gitTemplateCacheKey(cacheSource, sha string) string {
	return cacheSource + "@" + sha
}

func findCachedGitTemplate(cacheRoot string, ref gitTemplateRef) (templatePullResult, bool) {
	if ref.Revision != "" && looksLikeGitSHA(ref.Revision) {
		cacheKey := gitTemplateCacheKey(ref.CacheSource, ref.Revision)
		if dst, ok := cachedTemplateDir(cacheRoot, cacheKey); ok {
			result := gitTemplatePullResult(ref, cacheKey, dst, ref.Revision, gitRevisionCommit)
			result.Cached = true
			result.Action = "cached"
			return result, true
		}
	}

	parent := filepath.Join(cacheRoot, filepath.Dir(filepath.FromSlash(ref.CacheSource)))
	prefix := filepath.Base(filepath.FromSlash(ref.CacheSource)) + "@"
	entries, err := os.ReadDir(parent)
	if err != nil {
		return templatePullResult{}, false
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		dir := filepath.Join(parent, e.Name())
		meta, err := readGitTemplateCacheMeta(dir)
		if err != nil {
			continue
		}
		if meta.Source != ref.CacheSource || meta.Revision != ref.Revision {
			continue
		}
		if meta.RevisionKind != gitRevisionTag && meta.RevisionKind != gitRevisionCommit {
			continue
		}
		cacheKey := meta.CacheKey
		if cacheKey == "" {
			rel, err := filepath.Rel(cacheRoot, dir)
			if err != nil {
				continue
			}
			cacheKey = filepath.ToSlash(rel)
		}
		result := templatePullResult{
			Ref:               ref.Input,
			Source:            "git",
			CacheKey:          cacheKey,
			Path:              filepath.ToSlash(dir),
			CloneURL:          meta.CloneURL,
			Revision:          meta.Revision,
			RevisionKind:      meta.RevisionKind,
			DefaultedRevision: meta.DefaultedRevision,
			ResolvedSHA:       meta.ResolvedSHA,
			Cached:            true,
			Action:            "cached",
		}
		return result, true
	}
	return templatePullResult{}, false
}

func cachedTemplateDir(cacheRoot, cacheKey string) (string, bool) {
	dst, err := cacheDestination(cacheRoot, cacheKey)
	if err != nil {
		return "", false
	}
	st, err := os.Stat(dst)
	return dst, err == nil && st.IsDir()
}

func readGitTemplateCacheMeta(dir string) (*gitTemplateCacheMeta, error) {
	body, err := os.ReadFile(filepath.Join(dir, template.CacheMetaFileName))
	if err != nil {
		return nil, err
	}
	var meta gitTemplateCacheMeta
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func writeGitTemplateCacheMeta(dir string, meta gitTemplateCacheMeta) error {
	body, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	return os.WriteFile(filepath.Join(dir, template.CacheMetaFileName), body, 0o644)
}

func latestGitTag(cloneURL string) (string, bool, error) {
	out, err := exec.Command("git", "ls-remote", "--tags", "--refs", "--sort=-version:refname", cloneURL).CombinedOutput()
	sortedByGit := err == nil
	if err != nil {
		out, err = exec.Command("git", "ls-remote", "--tags", "--refs", cloneURL).CombinedOutput()
		if err != nil {
			return "", false, fmt.Errorf("git ls-remote tags: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}
	var tags []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || !strings.HasPrefix(fields[1], "refs/tags/") {
			continue
		}
		tags = append(tags, strings.TrimPrefix(fields[1], "refs/tags/"))
	}
	if len(tags) == 0 {
		return "", false, nil
	}
	if !sortedByGit {
		sort.Sort(sort.Reverse(sort.StringSlice(tags)))
	}
	return tags[0], true, nil
}

func classifyGitRevision(cloneURL, revision string) string {
	if strings.EqualFold(revision, "HEAD") {
		return gitRevisionHead
	}
	if looksLikeGitSHA(revision) {
		return gitRevisionCommit
	}
	_, kind, err := resolveGitRevisionSHA(cloneURL, revision, gitRevisionUnknown)
	if err != nil {
		return gitRevisionUnknown
	}
	return kind
}

func resolveGitRevisionSHA(cloneURL, revision, fallbackKind string) (string, string, error) {
	if strings.EqualFold(revision, "HEAD") {
		out, err := exec.Command("git", "ls-remote", cloneURL, "HEAD").CombinedOutput()
		if err != nil {
			return "", "", fmt.Errorf("git ls-remote HEAD: %w: %s", err, strings.TrimSpace(string(out)))
		}
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[1] == "HEAD" {
				return fields[0], gitRevisionHead, nil
			}
		}
		return "", "", fmt.Errorf("git ref %q did not resolve to a commit", revision)
	}

	out, err := exec.Command("git", "ls-remote", "--heads", "--tags", cloneURL, revision, "refs/heads/"+revision, "refs/tags/"+revision).CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("git ls-remote %s: %w: %s", revision, err, strings.TrimSpace(string(out)))
	}
	var tagSHA, branchSHA string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		switch fields[1] {
		case "refs/tags/" + revision + "^{}":
			return fields[0], gitRevisionTag, nil
		case "refs/tags/" + revision:
			tagSHA = fields[0]
		case "refs/heads/" + revision:
			branchSHA = fields[0]
		}
	}
	if tagSHA != "" {
		return tagSHA, gitRevisionTag, nil
	}
	if branchSHA != "" {
		return branchSHA, gitRevisionBranch, nil
	}
	if looksLikeGitSHA(revision) {
		return revision, gitRevisionCommit, nil
	}
	if fallbackKind == "" {
		fallbackKind = gitRevisionUnknown
	}
	return "", fallbackKind, fmt.Errorf("git ref %q did not resolve to a branch, tag, or commit", revision)
}

func fetchGitTemplateToTemp(ref gitTemplateRef, cacheRoot string) (string, string, error) {
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return "", "", err
	}
	tmp, err := os.MkdirTemp(cacheRoot, ".pull-*")
	if err != nil {
		return "", "", err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tmp)
		}
	}()
	if err := runTemplateGit(tmp, "init", "-q"); err != nil {
		return "", "", err
	}
	if err := runTemplateGit(tmp, "remote", "add", "origin", ref.CloneURL); err != nil {
		return "", "", err
	}
	if err := runTemplateGit(tmp, "fetch", "--depth", "1", "origin", ref.Revision); err != nil {
		return "", "", err
	}
	if err := runTemplateGit(tmp, "checkout", "--detach", "-q", "FETCH_HEAD"); err != nil {
		return "", "", err
	}
	shaOut, err := runTemplateGitOutput(tmp, "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	cleanup = false
	return tmp, strings.TrimSpace(string(shaOut)), nil
}

func runTemplateGit(dir string, args ...string) error {
	_, err := runTemplateGitOutput(dir, args...)
	return err
}

func runTemplateGitOutput(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func isMutableGitRevisionKind(kind, revision string) bool {
	switch kind {
	case gitRevisionBranch, gitRevisionHead, gitRevisionUnknown:
		return true
	default:
		return isMutableGitRevision(revision)
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

func looksLikeGitSHA(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, r := range s {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			continue
		}
		return false
	}
	return true
}
