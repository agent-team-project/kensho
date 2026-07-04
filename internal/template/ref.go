package template

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// BundledRef is the reserved keyword for the binary's embedded default
// template. Passed as the ref to `init`, `template show`, etc.
const BundledRef = "bundled"

// DefaultRef is a friendlier alias for the embedded default template. The
// resolved template still reports Ref=BundledRef so provenance stays stable.
const DefaultRef = "default"

// IsBundledRef reports whether ref selects the embedded default template.
func IsBundledRef(ref string) bool {
	switch strings.TrimSpace(ref) {
	case "", BundledRef, DefaultRef:
		return true
	default:
		return false
	}
}

// ResolvedTemplate is the result of resolving a template ref. The caller
// gets enough info to walk the template tree and read files from it without
// caring whether the source is the embedded FS or a directory on disk.
type ResolvedTemplate struct {
	// Ref is the original input string ("bundled", "./local", etc.).
	Ref string
	// FS is the filesystem the template lives in.
	FS fs.FS
	// Root is the path inside FS at which the template begins. Pass into
	// RenderTreeFromFS or fs.WalkDir.
	Root string
	// OnDiskRoot is non-empty when the template lives on disk. Some callers
	// (e.g. `RenderTreeFromOS`) want a real path for performance / mode
	// preservation reasons.
	OnDiskRoot string
	// Manifest is the parsed `template.toml` (nil if absent).
	Manifest *Manifest
}

// Resolver decides how a ref string maps to a ResolvedTemplate. It is wired
// up at the cli layer with the bundled embed.FS pre-bound.
type Resolver struct {
	// BundledFS holds the binary-embedded "default" template tree.
	BundledFS fs.FS
	// BundledRoot is the path inside BundledFS the template begins at
	// (e.g. "template").
	BundledRoot string
	// CacheRoot is the on-disk directory where pulled templates live
	// (e.g. ~/.agent-team/cache).
	CacheRoot string
}

// Resolve maps a ref to a ResolvedTemplate. Three forms are supported:
//   - "", "bundled", or "default" — the embedded default template.
//   - "./...", "../...", "/...", or any path that exists on disk — a local
//     directory.
//   - any other string — looked up in the cache root as a relative path.
//
// Git sources are fetched by the CLI layer into the cache first; after that,
// init/show/run resolve them through the same cache-relative path.
func (r *Resolver) Resolve(ref string) (*ResolvedTemplate, error) {
	if IsBundledRef(ref) {
		return r.resolveBundled()
	}
	if isLocalPath(ref) {
		return r.resolveLocal(ref)
	}
	// Treat as a cache-relative path.
	cached := filepath.Join(r.CacheRoot, ref)
	if st, err := os.Stat(cached); err == nil && st.IsDir() {
		rt, err := r.resolveLocal(cached)
		if err != nil {
			return nil, err
		}
		rt.Ref = ref
		return rt, nil
	}
	return nil, fmt.Errorf("template ref %q: not bundled, not a local path, and not in cache (%s)", ref, r.CacheRoot)
}

func (r *Resolver) resolveBundled() (*ResolvedTemplate, error) {
	m, err := loadManifestFromFS(r.BundledFS, r.BundledRoot)
	if err != nil {
		return nil, err
	}
	return &ResolvedTemplate{
		Ref:      BundledRef,
		FS:       r.BundledFS,
		Root:     r.BundledRoot,
		Manifest: m,
	}, nil
}

func (r *Resolver) resolveLocal(path string) (*ResolvedTemplate, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if eval, err := filepath.EvalSymlinks(abs); err == nil {
		abs = eval
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("template ref %q: %v", path, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("template ref %q is not a directory", path)
	}
	m, err := LoadManifest(abs)
	if err != nil {
		return nil, err
	}
	return &ResolvedTemplate{
		Ref:        path,
		FS:         os.DirFS(abs),
		Root:       ".",
		OnDiskRoot: abs,
		Manifest:   m,
	}, nil
}

// loadManifestFromFS reads `<root>/template.toml` from an fs.FS. Mirrors
// LoadManifest but works against embedded FS, where filepath operations don't
// apply.
func loadManifestFromFS(srcFS fs.FS, root string) (*Manifest, error) {
	body, err := fs.ReadFile(srcFS, root+"/"+ManifestFileName)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return parseManifestBytes(body)
}

func parseManifestBytes(body []byte) (*Manifest, error) {
	var m Manifest
	if _, err := tomlDecode(body, &m); err != nil {
		return nil, fmt.Errorf("template.toml: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("template.toml: %w", err)
	}
	return &m, nil
}

func isLocalPath(ref string) bool {
	if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../") || strings.HasPrefix(ref, "/") {
		return true
	}
	// Bare path that exists on disk → treat as local.
	if st, err := os.Stat(ref); err == nil && st.IsDir() {
		return true
	}
	return false
}
