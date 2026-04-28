package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestResolver_Bundled(t *testing.T) {
	manifest := []byte(`[template]
name = "x"
version = "0.0.1"

[[parameter]]
key = "k"
type = "string"
default = "v"
`)
	bundled := fstest.MapFS{
		"root/template.toml": &fstest.MapFile{Data: manifest},
		"root/agents/x":      &fstest.MapFile{Data: []byte("body")},
	}
	r := &Resolver{BundledFS: bundled, BundledRoot: "root"}
	rt, err := r.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if rt.Ref != BundledRef {
		t.Errorf("ref = %s", rt.Ref)
	}
	if rt.Manifest == nil || rt.Manifest.Template.Name != "x" {
		t.Errorf("manifest: %+v", rt.Manifest)
	}
}

func TestResolver_LocalPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "template.toml"),
		[]byte("[template]\nname=\"local\"\nversion=\"0.0.1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Resolver{}
	rt, err := r.Resolve(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rt.Manifest.Template.Name != "local" {
		t.Errorf("manifest = %+v", rt.Manifest)
	}
	if rt.OnDiskRoot == "" {
		t.Errorf("expected OnDiskRoot to be set")
	}
}

func TestResolver_LocalPath_NotADir(t *testing.T) {
	r := &Resolver{}
	_, err := r.Resolve("/this/should/not/exist/anywhere/under/test")
	if err == nil {
		t.Fatal("expected error for missing local path")
	}
}

func TestResolver_UnknownBareRef(t *testing.T) {
	r := &Resolver{CacheRoot: t.TempDir()}
	_, err := r.Resolve("github.com/some/template@v1")
	if err == nil || !strings.Contains(err.Error(), "not bundled, not a local path, and not in cache") {
		t.Errorf("expected unresolved error, got %v", err)
	}
}

func TestResolver_CacheLookup(t *testing.T) {
	cache := t.TempDir()
	cached := filepath.Join(cache, "github.com/acme/eng-team@v1.0.0")
	if err := os.MkdirAll(cached, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cached, "template.toml"),
		[]byte("[template]\nname=\"cached\"\nversion=\"1.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Resolver{CacheRoot: cache}
	rt, err := r.Resolve("github.com/acme/eng-team@v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if rt.Manifest.Template.Name != "cached" {
		t.Errorf("manifest = %+v", rt.Manifest)
	}
}
