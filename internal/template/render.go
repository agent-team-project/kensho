package template

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// TmplSuffix is the on-disk marker that a file should be rendered through
// `text/template` rather than copied verbatim.
const TmplSuffix = ".tmpl"

// RenderResult records what the renderer did to a single file. Returned in
// declaration order from RenderTree so callers can print a one-line audit.
type RenderResult struct {
	// SourceRel is the path of the source file relative to the template root.
	SourceRel string
	// DestRel is the path written, relative to the destination root, with any
	// `.tmpl` suffix stripped.
	DestRel string
	// Rendered is true if the file was processed through text/template.
	Rendered bool
}

// RenderTreeFromOS walks an on-disk template at `srcRoot`, copying each file
// to `dstRoot` (creating directories as needed). Files whose names end in
// `.tmpl` are rendered against `data` and written with the suffix stripped.
//
// Files at the template root that the caller doesn't want copied (e.g.
// `template.toml` itself) should be filtered via skipNames.
func RenderTreeFromOS(srcRoot, dstRoot string, data Tree, skipNames map[string]bool) ([]RenderResult, error) {
	return renderTree(osFS(srcRoot), dstRoot, ".", data, skipNames)
}

// RenderTreeFromFS is the embed.FS variant. The `srcRoot` should already be a
// path inside the FS (e.g. "template").
func RenderTreeFromFS(srcFS fs.FS, srcRoot, dstRoot string, data Tree, skipNames map[string]bool) ([]RenderResult, error) {
	return renderTree(srcFS, dstRoot, srcRoot, data, skipNames)
}

func renderTree(srcFS fs.FS, dstRoot, srcRoot string, data Tree, skipNames map[string]bool) ([]RenderResult, error) {
	var results []RenderResult
	err := fs.WalkDir(srcFS, srcRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == srcRoot {
			return nil
		}
		rel, err := filepath.Rel(srcRoot, p)
		if err != nil {
			return err
		}
		if skipGeneratedArtifact(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		// Top-level filter (e.g. `template.toml`).
		if filepath.Dir(rel) == "." && skipNames[filepath.Base(rel)] {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		dstRel := rel
		rendered := strings.HasSuffix(rel, TmplSuffix)
		if rendered {
			dstRel = strings.TrimSuffix(rel, TmplSuffix)
		}
		dstPath := filepath.Join(dstRoot, dstRel)
		if d.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return err
		}
		body, err := fs.ReadFile(srcFS, p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if isExecutableTemplate(rel) {
			mode = 0o755
		}
		if rendered {
			out, err := RenderBytes(rel, body, data)
			if err != nil {
				return err
			}
			if err := os.WriteFile(dstPath, out, mode); err != nil {
				return err
			}
		} else {
			if err := os.WriteFile(dstPath, body, mode); err != nil {
				return err
			}
		}
		results = append(results, RenderResult{
			SourceRel: filepath.ToSlash(rel),
			DestRel:   filepath.ToSlash(dstRel),
			Rendered:  rendered,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func skipGeneratedArtifact(rel string) bool {
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		switch part {
		case "__pycache__", ".mypy_cache", ".pytest_cache", ".ruff_cache", "node_modules":
			return true
		}
	}
	switch filepath.Base(rel) {
	case ".DS_Store", "Thumbs.db":
		return true
	}
	switch filepath.Ext(rel) {
	case ".pyc", ".pyo":
		return true
	}
	return false
}

// isExecutableTemplate mirrors the behaviour the SQU-21 init had: the
// embedded FS does not retain mode bits, so we restore +x for `.sh` files.
func isExecutableTemplate(p string) bool {
	return strings.HasSuffix(p, ".sh") || strings.HasSuffix(p, ".sh.tmpl")
}

func RenderBytes(name string, body []byte, data Tree) ([]byte, error) {
	tmpl, err := template.New(name).Option("missingkey=zero").Parse(string(body))
	if err != nil {
		return nil, fmt.Errorf("%s: parse template: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]any(data)); err != nil {
		return nil, fmt.Errorf("%s: execute template: %w", name, err)
	}
	return buf.Bytes(), nil
}

// osFS adapts an os filesystem rooted at `root` to fs.FS so renderTree can
// share its walk code with the embed.FS path. We use os.DirFS via the
// stdlib helper.
func osFS(root string) fs.FS {
	return osFSImpl{root: root}
}

type osFSImpl struct{ root string }

func (o osFSImpl) Open(name string) (fs.File, error) {
	if name == "." {
		f, err := os.Open(o.root)
		return f, err
	}
	return os.Open(filepath.Join(o.root, name))
}

func (o osFSImpl) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == "." {
		return os.ReadDir(o.root)
	}
	return os.ReadDir(filepath.Join(o.root, name))
}

func (o osFSImpl) ReadFile(name string) ([]byte, error) {
	if name == "." {
		return nil, fmt.Errorf("cannot read directory as file")
	}
	return os.ReadFile(filepath.Join(o.root, name))
}

// Used to keep the renderer independent of whether the source is on disk or
// embed.FS. embed.FS satisfies fs.FS directly; on-disk paths use osFS above.
var _ io.Reader = (*bytes.Buffer)(nil)
