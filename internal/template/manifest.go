// Package template implements the templates-as-images model: the
// `template.toml` manifest, parameter validation, layered config resolution,
// and `.tmpl` rendering via Go `text/template`.
//
// See `documentation/templates.md` for the full design.
package template

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/BurntSushi/toml"
)

// tomlDecode is a thin wrapper around toml.Decode so other files in this
// package can avoid importing BurntSushi/toml directly.
func tomlDecode(body []byte, into any) (toml.MetaData, error) {
	return toml.Decode(string(body), into)
}

// ManifestFileName is the name of the file at the root of any template that
// declares the template's identity and parameters.
const ManifestFileName = "template.toml"

// ParameterType is the declared TOML type of a parameter value.
type ParameterType string

const (
	TypeString     ParameterType = "string"
	TypeInt        ParameterType = "int"
	TypeBool       ParameterType = "bool"
	TypeListString ParameterType = "list<string>"
)

// Parameter is one entry under `[[parameter]]` in a template's manifest.
type Parameter struct {
	Key               string        `toml:"key"`
	Type              ParameterType `toml:"type"`
	Required          bool          `toml:"required"`
	RequiredWhenKey   string        `toml:"required_when_key"`
	RequiredWhenValue string        `toml:"required_when_value"`
	Default           any           `toml:"default"`
	Pattern           string        `toml:"pattern"`
	Description       string        `toml:"description"`
}

// Header is the `[template]` block with template-level metadata.
type Header struct {
	Name        string `toml:"name"`
	Version     string `toml:"version"`
	Description string `toml:"description"`
}

// Profile is an optional named rendering profile. Profiles let one template
// tree ship several curated shapes without duplicating files.
type Profile struct {
	Description string   `toml:"description"`
	Exclude     []string `toml:"exclude"`
}

// Manifest is the parsed `template.toml`.
type Manifest struct {
	Template   Header             `toml:"template"`
	Parameters []Parameter        `toml:"parameter"`
	Profiles   map[string]Profile `toml:"profiles"`
}

// LoadManifest reads `<dir>/template.toml`. Returns nil with no error if the
// file does not exist (templates without a manifest are still copyable, just
// without parameter substitution).
func LoadManifest(dir string) (*Manifest, error) {
	p := filepath.Join(dir, ManifestFileName)
	if _, err := os.Stat(p); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var m Manifest
	if _, err := toml.DecodeFile(p, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return &m, nil
}

// Validate enforces the structural rules of the manifest schema. Called
// implicitly by LoadManifest; exposed for fixture-based tests.
func (m *Manifest) Validate() error {
	if m.Template.Name == "" {
		return fmt.Errorf("[template].name is required")
	}
	if m.Template.Version == "" {
		return fmt.Errorf("[template].version is required")
	}
	seen := map[string]struct{}{}
	for i, p := range m.Parameters {
		if p.Key == "" {
			return fmt.Errorf("parameter[%d]: key is required", i)
		}
		if _, dup := seen[p.Key]; dup {
			return fmt.Errorf("parameter[%d]: duplicate key %q", i, p.Key)
		}
		seen[p.Key] = struct{}{}
		switch p.Type {
		case TypeString, TypeInt, TypeBool, TypeListString:
		case "":
			return fmt.Errorf("parameter[%s]: type is required", p.Key)
		default:
			return fmt.Errorf("parameter[%s]: unsupported type %q", p.Key, p.Type)
		}
		if p.Required && p.Default != nil {
			return fmt.Errorf("parameter[%s]: required parameters cannot have a default", p.Key)
		}
		if p.Required && p.RequiredWhenKey != "" {
			return fmt.Errorf("parameter[%s]: required and required_when_key are mutually exclusive", p.Key)
		}
		if (p.RequiredWhenKey == "") != (p.RequiredWhenValue == "") {
			return fmt.Errorf("parameter[%s]: required_when_key and required_when_value must be set together", p.Key)
		}
		if p.Pattern != "" {
			if _, err := regexp.Compile(p.Pattern); err != nil {
				return fmt.Errorf("parameter[%s]: invalid pattern %q: %v", p.Key, p.Pattern, err)
			}
			if p.Type != TypeString {
				return fmt.Errorf("parameter[%s]: pattern only valid on string type", p.Key)
			}
		}
	}
	for name, profile := range m.Profiles {
		if name == "" {
			return fmt.Errorf("profile name is required")
		}
		for i, path := range profile.Exclude {
			if path == "" {
				return fmt.Errorf("profile[%s].exclude[%d]: path is required", name, i)
			}
		}
	}
	return nil
}

// FindParameter returns the parameter declaration for key, or nil if none.
func (m *Manifest) FindParameter(key string) *Parameter {
	if m == nil {
		return nil
	}
	for i := range m.Parameters {
		if m.Parameters[i].Key == key {
			return &m.Parameters[i]
		}
	}
	return nil
}
