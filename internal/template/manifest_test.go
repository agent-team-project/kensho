package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadManifest_Valid(t *testing.T) {
	dir := t.TempDir()
	body := `
[template]
name = "demo"
version = "1.0.0"
description = "demo template"

[[parameter]]
key = "linear.team_id"
type = "string"
required = true
description = "Linear team UUID"

[[parameter]]
key = "linear.ticket_prefix"
type = "string"
required = true
pattern = "^[A-Z]{2,5}$"

[[parameter]]
key = "linear.labels"
type = "list<string>"
default = ["agent-work"]
`
	if err := os.WriteFile(filepath.Join(dir, "template.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Template.Name != "demo" || m.Template.Version != "1.0.0" {
		t.Errorf("header: %+v", m.Template)
	}
	if len(m.Parameters) != 3 {
		t.Fatalf("expected 3 parameters, got %d", len(m.Parameters))
	}
	if m.FindParameter("linear.team_id") == nil {
		t.Error("FindParameter team_id missed")
	}
	if m.FindParameter("nope") != nil {
		t.Error("FindParameter nope should be nil")
	}
}

func TestLoadManifest_Missing(t *testing.T) {
	dir := t.TempDir()
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("missing manifest should not error, got: %v", err)
	}
	if m != nil {
		t.Errorf("expected nil manifest for missing file, got %+v", m)
	}
}

func TestValidate_RequiredWithDefaultRejected(t *testing.T) {
	m := &Manifest{
		Template: Header{Name: "x", Version: "0.0.1"},
		Parameters: []Parameter{
			{Key: "k", Type: TypeString, Required: true, Default: "oops"},
		},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "required parameters cannot have a default") {
		t.Errorf("expected validation error, got %v", err)
	}
}

func TestValidate_DuplicateKey(t *testing.T) {
	m := &Manifest{
		Template: Header{Name: "x", Version: "0.0.1"},
		Parameters: []Parameter{
			{Key: "k", Type: TypeString},
			{Key: "k", Type: TypeString},
		},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate key") {
		t.Errorf("expected duplicate-key error, got %v", err)
	}
}

func TestValidate_BadPattern(t *testing.T) {
	m := &Manifest{
		Template: Header{Name: "x", Version: "0.0.1"},
		Parameters: []Parameter{
			{Key: "k", Type: TypeString, Pattern: "[oops"},
		},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "invalid pattern") {
		t.Errorf("expected invalid-pattern error, got %v", err)
	}
}

func TestValidate_PatternOnNonString(t *testing.T) {
	m := &Manifest{
		Template: Header{Name: "x", Version: "0.0.1"},
		Parameters: []Parameter{
			{Key: "k", Type: TypeInt, Pattern: "^[0-9]+$"},
		},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "pattern only valid on string type") {
		t.Errorf("expected pattern-on-int error, got %v", err)
	}
}
