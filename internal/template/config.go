package template

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Tree is a generic nested config tree (the same shape `BurntSushi/toml`
// decodes into when given a `map[string]any`). Dotted keys like
// `linear.team_id` resolve to nested maps.
type Tree map[string]any

// LoadTOMLFile reads a TOML file into a Tree. Missing files yield an empty
// Tree, not an error — this is convenient for layering optional sources.
func LoadTOMLFile(path string) (Tree, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return Tree{}, nil
		}
		return nil, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var t Tree
	if _, err := toml.Decode(string(body), &t); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if t == nil {
		t = Tree{}
	}
	return t, nil
}

// SetDotted sets t[a][b][c] = value for key="a.b.c", creating intermediate
// maps. Existing non-map values at intermediate keys are overwritten.
func (t Tree) SetDotted(key string, value any) {
	parts := strings.Split(key, ".")
	cur := map[string]any(t)
	for i, part := range parts {
		if i == len(parts)-1 {
			cur[part] = value
			return
		}
		next, ok := asMap(cur[part])
		if !ok {
			next = map[string]any{}
			cur[part] = next
		}
		cur = next
	}
}

// GetDotted returns t[a][b][c] for "a.b.c", and ok=true if the key exists.
func (t Tree) GetDotted(key string) (any, bool) {
	parts := strings.Split(key, ".")
	var cur any = map[string]any(t)
	for _, part := range parts {
		m, ok := asMap(cur)
		if !ok {
			return nil, false
		}
		v, present := m[part]
		if !present {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// asMap normalises a value to map[string]any whether it's stored as the named
// `Tree` type or the underlying map. We try Tree first because nested writes
// in this package can produce that dynamic type.
func asMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case Tree:
		return map[string]any(m), true
	}
	return nil, false
}

// MergeOver returns a new Tree that is `over` deep-merged on top of `base`:
// keys present in `over` win; nested maps merge recursively; everything else
// is overwritten wholesale.
func MergeOver(base, over Tree) Tree {
	out := deepCopy(base)
	for k, v := range over {
		if vMap, ok := asMap(v); ok {
			if existing, ok := asMap(out[k]); ok {
				out[k] = map[string]any(MergeOver(existing, vMap))
				continue
			}
			// Source map but no existing map → deep-copy so callers cannot
			// mutate `over` and observe it through the merged tree.
			out[k] = map[string]any(deepCopy(vMap))
			continue
		}
		out[k] = v
	}
	return out
}

func deepCopy(t Tree) Tree {
	out := make(Tree, len(t))
	for k, v := range t {
		if m, ok := asMap(v); ok {
			out[k] = map[string]any(deepCopy(m))
		} else {
			out[k] = v
		}
	}
	return out
}

// ResolveLayers merges N layers in increasing precedence: the first argument
// is lowest, the last is highest. Mirrors the resolution chain in
// `documentation/templates.md`.
func ResolveLayers(layers ...Tree) Tree {
	out := Tree{}
	for _, l := range layers {
		out = MergeOver(out, l)
	}
	return out
}

// DefaultsFromManifest collects all parameters' default values (where set)
// into a Tree, ready to be the lowest layer in ResolveLayers.
func DefaultsFromManifest(m *Manifest) Tree {
	t := Tree{}
	if m == nil {
		return t
	}
	for _, p := range m.Parameters {
		if p.Default == nil {
			continue
		}
		t.SetDotted(p.Key, p.Default)
	}
	return t
}

// SetSpec is a `--set k=v` pair from the CLI.
type SetSpec struct {
	Key   string
	Value string
}

// ParseSetSpecs turns `["a=1", "b.c=hello"]` into typed SetSpecs.
// Manifest-aware coercion (string vs int vs list<string>) happens later in
// CoerceSetsAgainstManifest because we want unknown keys to be allowed (the
// resolution chain is open-ended; the manifest only constrains parameters
// that the template actually declares).
func ParseSetSpecs(raw []string) ([]SetSpec, error) {
	out := make([]SetSpec, 0, len(raw))
	for _, s := range raw {
		eq := strings.IndexByte(s, '=')
		if eq < 1 {
			return nil, fmt.Errorf("--set value %q must be of form key=value", s)
		}
		out = append(out, SetSpec{Key: s[:eq], Value: s[eq+1:]})
	}
	return out, nil
}

// ApplySets layers --set values on top of base. If manifest declares the key,
// the value is coerced to the declared type (int, bool, list<string>);
// otherwise it is stored as a string.
func ApplySets(base Tree, sets []SetSpec, m *Manifest) (Tree, error) {
	out := deepCopy(base)
	for _, s := range sets {
		v, err := coerceForKey(s.Key, s.Value, m)
		if err != nil {
			return nil, err
		}
		out.SetDotted(s.Key, v)
	}
	return out, nil
}

// coerceForKey turns a CLI string value into the right Go type given the
// declared parameter type, falling back to string when the manifest does not
// declare the key.
func coerceForKey(key, raw string, m *Manifest) (any, error) {
	p := m.FindParameter(key)
	if p == nil {
		return raw, nil
	}
	switch p.Type {
	case TypeString:
		return raw, nil
	case TypeInt:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("--set %s=%q: not a valid int: %v", key, raw, err)
		}
		return n, nil
	case TypeBool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("--set %s=%q: not a valid bool: %v", key, raw, err)
		}
		return b, nil
	case TypeListString:
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return []any{}, nil
		}
		parts := strings.Split(raw, ",")
		out := make([]any, 0, len(parts))
		for _, p := range parts {
			out = append(out, strings.TrimSpace(p))
		}
		return out, nil
	}
	return raw, nil
}

// ValidateAgainstManifest fails when:
//   - a `required` parameter has no value in the resolved tree, or
//   - a string value violates the parameter's `pattern` regex.
//
// Unknown keys (present in the tree but not declared) are allowed — the
// resolution tree is open to consumer-added keys beyond what the template
// declares.
func ValidateAgainstManifest(resolved Tree, m *Manifest) error {
	if m == nil {
		return nil
	}
	var missing []string
	for _, p := range m.Parameters {
		v, ok := resolved.GetDotted(p.Key)
		if !ok || isEmpty(v) {
			if p.Required {
				missing = append(missing, p.Key)
			}
			continue
		}
		if p.Pattern != "" && p.Type == TypeString {
			s, _ := v.(string)
			rx, err := regexp.Compile(p.Pattern)
			if err != nil {
				return fmt.Errorf("manifest pattern for %s did not compile: %v", p.Key, err)
			}
			if !rx.MatchString(s) {
				return fmt.Errorf("parameter %s value %q does not match pattern %q", p.Key, s, p.Pattern)
			}
		}
	}
	if len(missing) > 0 {
		return &MissingRequiredError{Keys: missing}
	}
	return nil
}

// MissingRequiredError lists the required parameters that have no value.
// Surfaced separately so the init command can print a help block with each
// parameter's description.
type MissingRequiredError struct {
	Keys []string
}

func (e *MissingRequiredError) Error() string {
	return fmt.Sprintf("missing required parameters: %s", strings.Join(e.Keys, ", "))
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// EncodeTOML renders a Tree as TOML. Used to write resolved configs.
func EncodeTOML(t Tree) ([]byte, error) {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(map[string]any(t)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
