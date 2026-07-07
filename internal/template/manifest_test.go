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

func TestLoadManifest_Profiles(t *testing.T) {
	dir := t.TempDir()
	body := `
[template]
name = "demo"
version = "1.0.0"

[[parameter]]
key = "template.profile"
type = "string"
default = "slim"
pattern = "^(slim|full)$"

[profiles.slim]
description = "minimal starter"
exclude = ["agents/auditor", "skills/release"]

[profiles.full]
description = "full tree"
exclude = []
`
	if err := os.WriteFile(filepath.Join(dir, "template.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if got := m.Profiles["slim"].Description; got != "minimal starter" {
		t.Fatalf("slim profile description = %q", got)
	}
	if got := strings.Join(m.Profiles["slim"].Exclude, ","); got != "agents/auditor,skills/release" {
		t.Fatalf("slim profile excludes = %q", got)
	}
	if len(m.Profiles["full"].Exclude) != 0 {
		t.Fatalf("full profile excludes = %v", m.Profiles["full"].Exclude)
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

func TestValidate_ConditionalRequiredFields(t *testing.T) {
	valid := &Manifest{
		Template: Header{Name: "x", Version: "0.0.1"},
		Parameters: []Parameter{
			{
				Key:               "linear.team_id",
				Type:              TypeString,
				Default:           "",
				RequiredWhenKey:   "team.pm_tool",
				RequiredWhenValue: "linear",
			},
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("conditional required with default should validate: %v", err)
	}

	badPair := &Manifest{
		Template: Header{Name: "x", Version: "0.0.1"},
		Parameters: []Parameter{
			{Key: "linear.team_id", Type: TypeString, RequiredWhenKey: "team.pm_tool"},
		},
	}
	if err := badPair.Validate(); err == nil || !strings.Contains(err.Error(), "must be set together") {
		t.Fatalf("expected required_when pair error, got %v", err)
	}

	badRequired := &Manifest{
		Template: Header{Name: "x", Version: "0.0.1"},
		Parameters: []Parameter{
			{Key: "linear.team_id", Type: TypeString, Required: true, RequiredWhenKey: "team.pm_tool", RequiredWhenValue: "linear"},
		},
	}
	if err := badRequired.Validate(); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected required/conditional error, got %v", err)
	}
}

func TestBundledManifestPMProvidersAreConditional(t *testing.T) {
	m, err := LoadManifest(filepath.Join("..", "..", "template"))
	if err != nil {
		t.Fatalf("load bundled manifest: %v", err)
	}
	pm := m.FindParameter("pm.provider")
	if pm == nil {
		t.Fatal("bundled manifest missing pm.provider")
	}
	if pm.Default != "none" || pm.Pattern != "^(none|linear|github)$" {
		t.Fatalf("pm.provider defaults = default:%v pattern:%q", pm.Default, pm.Pattern)
	}
	alias := m.FindParameter("team.pm_tool")
	if alias == nil {
		t.Fatal("bundled manifest missing team.pm_tool alias")
	}
	if alias.Default != "none" || alias.Pattern != "^(none|linear|github)$" {
		t.Fatalf("team.pm_tool alias defaults = default:%v pattern:%q", alias.Default, alias.Pattern)
	}
	for _, tc := range []struct {
		key      string
		provider string
	}{
		{key: "linear.team_id", provider: "linear"},
		{key: "linear.ticket_prefix", provider: "linear"},
		{key: "github.owner", provider: "github"},
		{key: "github.repo", provider: "github"},
	} {
		key := tc.key
		p := m.FindParameter(key)
		if p == nil {
			t.Fatalf("bundled manifest missing %s", key)
		}
		if p.Required {
			t.Fatalf("%s should not be unconditionally required", key)
		}
		if p.RequiredWhenKey != "pm.provider" || p.RequiredWhenValue != tc.provider {
			t.Fatalf("%s conditional = %s=%s", key, p.RequiredWhenKey, p.RequiredWhenValue)
		}
		if p.Default != "" {
			t.Fatalf("%s default = %#v, want empty string", key, p.Default)
		}
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
