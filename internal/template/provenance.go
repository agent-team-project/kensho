package template

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
)

// LockFileName is the generated provenance file written into .agent_team/.
const LockFileName = ".template.lock"

// CacheMetaFileName stores cache-only provenance for pulled templates. It is
// ignored by rendering and hashing so pulled metadata never becomes template
// content.
const CacheMetaFileName = ".agent-team-meta.json"

// Lock is the on-disk shape of .agent_team/.template.lock.
type Lock struct {
	Template LockTemplate `toml:"template"`
}

// LockTemplate records the source template identity used to render a repo.
type LockTemplate struct {
	Ref         string `toml:"ref"`
	Name        string `toml:"name"`
	Version     string `toml:"version"`
	ContentHash string `toml:"content_hash"`
}

// LoadLock reads and validates a .template.lock file.
func LoadLock(path string) (*Lock, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var l Lock
	if _, err := tomlDecode(body, &l); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := l.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &l, nil
}

// Validate enforces the minimal fields needed for reproducibility.
func (l *Lock) Validate() error {
	if l == nil {
		return fmt.Errorf("lock is nil")
	}
	if l.Template.Ref == "" {
		return fmt.Errorf("[template].ref is required")
	}
	if l.Template.ContentHash == "" {
		return fmt.Errorf("[template].content_hash is required")
	}
	if !strings.HasPrefix(l.Template.ContentHash, "sha256:") {
		return fmt.Errorf("[template].content_hash must start with sha256:")
	}
	digest := strings.TrimPrefix(l.Template.ContentHash, "sha256:")
	if len(digest) != sha256.Size*2 {
		return fmt.Errorf("[template].content_hash must be a sha256 hex digest")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return fmt.Errorf("[template].content_hash must be a sha256 hex digest")
	}
	return nil
}

// ContentHash returns a stable content hash for the resolved template source.
// The generated lock file itself is ignored so a checked-in consumer tree can
// be used as a local template without feeding stale provenance back into the
// source hash.
func ContentHash(rt *ResolvedTemplate) (string, error) {
	if rt == nil {
		return "", fmt.Errorf("template content hash: nil template")
	}
	root := rt.Root
	if root == "" {
		root = "."
	}

	h := sha256.New()
	err := fs.WalkDir(rt.FS, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel := p
		if root != "." {
			prefix := root + "/"
			if !strings.HasPrefix(p, prefix) {
				return fmt.Errorf("template content hash: path %q outside root %q", p, root)
			}
			rel = strings.TrimPrefix(p, prefix)
		}
		if path.Dir(rel) == "." && (path.Base(rel) == LockFileName || path.Base(rel) == CacheMetaFileName) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			_, _ = h.Write([]byte("dir\x00"))
			_, _ = h.Write([]byte(rel))
			_, _ = h.Write([]byte{0})
			return nil
		}
		body, err := fs.ReadFile(rt.FS, p)
		if err != nil {
			return err
		}
		_, _ = h.Write([]byte("file\x00"))
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(body)
		_, _ = h.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
