package template

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSetDottedAndGetDotted(t *testing.T) {
	tree := Tree{}
	tree.SetDotted("a.b.c", "v")
	got, ok := tree.GetDotted("a.b.c")
	if !ok || got != "v" {
		t.Errorf("SetDotted/GetDotted round-trip failed: %v ok=%v", got, ok)
	}

	// Existing scalar at intermediate path is overwritten by SetDotted.
	tree.SetDotted("a.b", "scalar")
	tree.SetDotted("a.b.c", "v2")
	got, ok = tree.GetDotted("a.b.c")
	if !ok || got != "v2" {
		t.Errorf("after overwrite: %v ok=%v", got, ok)
	}
}

func TestMergeOver_DeepMerge(t *testing.T) {
	base := Tree{
		"linear": map[string]any{
			"team_id":       "base-team",
			"ticket_prefix": "BASE",
		},
		"keep": "kept",
	}
	over := Tree{
		"linear": map[string]any{
			"team_id":       "over-team",
			"initiative_id": "ABC",
		},
	}
	merged := MergeOver(base, over)

	want := Tree{
		"linear": map[string]any{
			"team_id":       "over-team",
			"ticket_prefix": "BASE",
			"initiative_id": "ABC",
		},
		"keep": "kept",
	}
	if !reflect.DeepEqual(map[string]any(merged), map[string]any(want)) {
		t.Errorf("merged:\n got: %#v\nwant: %#v", merged, want)
	}
	// Confirm `base` was not mutated.
	if base["linear"].(map[string]any)["team_id"] != "base-team" {
		t.Error("base mutated by MergeOver")
	}
}

func TestResolveLayers_AllFourLayersExercised(t *testing.T) {
	manifest := &Manifest{
		Template: Header{Name: "x", Version: "0.0.1"},
		Parameters: []Parameter{
			{Key: "linear.ticket_prefix", Type: TypeString, Default: "DEFAULT"},
			{Key: "linear.team_id", Type: TypeString, Required: true},
			{Key: "linear.labels", Type: TypeListString, Default: []any{"baseline"}},
		},
	}
	defaults := DefaultsFromManifest(manifest)

	repo := Tree{}
	repo.SetDotted("linear.team_id", "from-repo")
	repo.SetDotted("linear.ticket_prefix", "REPO")

	instance := Tree{}
	instance.SetDotted("linear.ticket_prefix", "INST")

	sets, _ := ParseSetSpecs([]string{"linear.team_id=from-cli"})
	withSets, err := ApplySets(ResolveLayers(defaults, repo, instance), sets, manifest)
	if err != nil {
		t.Fatal(err)
	}

	prefix, _ := withSets.GetDotted("linear.ticket_prefix")
	if prefix != "INST" {
		t.Errorf("ticket_prefix = %v, want INST (instance over repo over default)", prefix)
	}
	teamID, _ := withSets.GetDotted("linear.team_id")
	if teamID != "from-cli" {
		t.Errorf("team_id = %v, want from-cli (CLI flags win)", teamID)
	}
	labels, _ := withSets.GetDotted("linear.labels")
	if !reflect.DeepEqual(labels, []any{"baseline"}) {
		t.Errorf("labels = %v, want [baseline] from defaults", labels)
	}

	if err := ValidateAgainstManifest(withSets, manifest); err != nil {
		t.Errorf("ValidateAgainstManifest: %v", err)
	}
}

func TestValidate_MissingRequiredSurfaced(t *testing.T) {
	m := &Manifest{
		Template: Header{Name: "x", Version: "0.0.1"},
		Parameters: []Parameter{
			{Key: "linear.team_id", Type: TypeString, Required: true},
		},
	}
	resolved := Tree{}
	err := ValidateAgainstManifest(resolved, m)
	if err == nil {
		t.Fatal("expected missing-required error")
	}
	var mre *MissingRequiredError
	if !errors.As(err, &mre) || len(mre.Keys) != 1 || mre.Keys[0] != "linear.team_id" {
		t.Errorf("missing-required: got %v", err)
	}
}

func TestValidate_PatternMismatch(t *testing.T) {
	m := &Manifest{
		Template: Header{Name: "x", Version: "0.0.1"},
		Parameters: []Parameter{
			{Key: "linear.ticket_prefix", Type: TypeString, Required: true, Pattern: "^[A-Z]{2,5}$"},
		},
	}
	tree := Tree{}
	tree.SetDotted("linear.ticket_prefix", "lowercase-bad")
	err := ValidateAgainstManifest(tree, m)
	if err == nil || !strings.Contains(err.Error(), "does not match pattern") {
		t.Errorf("expected pattern-mismatch error, got %v", err)
	}
}

func TestApplySets_TypeCoercion(t *testing.T) {
	m := &Manifest{
		Template: Header{Name: "x", Version: "0.0.1"},
		Parameters: []Parameter{
			{Key: "n", Type: TypeInt},
			{Key: "b", Type: TypeBool},
			{Key: "labels", Type: TypeListString},
			{Key: "s", Type: TypeString},
		},
	}
	sets, _ := ParseSetSpecs([]string{"n=42", "b=true", "labels=a,b , c", "s=hello"})
	out, err := ApplySets(Tree{}, sets, m)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := out.GetDotted("n"); v != int64(42) {
		t.Errorf("n = %T %v, want int64(42)", v, v)
	}
	if v, _ := out.GetDotted("b"); v != true {
		t.Errorf("b = %v, want true", v)
	}
	if v, _ := out.GetDotted("labels"); !reflect.DeepEqual(v, []any{"a", "b", "c"}) {
		t.Errorf("labels = %v", v)
	}
	if v, _ := out.GetDotted("s"); v != "hello" {
		t.Errorf("s = %v", v)
	}
}

func TestApplySets_BadInt(t *testing.T) {
	m := &Manifest{
		Template:   Header{Name: "x", Version: "0.0.1"},
		Parameters: []Parameter{{Key: "n", Type: TypeInt}},
	}
	sets, _ := ParseSetSpecs([]string{"n=not-a-number"})
	if _, err := ApplySets(Tree{}, sets, m); err == nil {
		t.Error("expected coercion failure")
	}
}

func TestParseSetSpecs_BadInput(t *testing.T) {
	if _, err := ParseSetSpecs([]string{"no-equals"}); err == nil {
		t.Error("expected parse error")
	}
	if _, err := ParseSetSpecs([]string{"=value-without-key"}); err == nil {
		t.Error("expected parse error for missing key")
	}
}

func TestApplySets_UnknownKeyKeepsString(t *testing.T) {
	m := &Manifest{Template: Header{Name: "x", Version: "0.0.1"}}
	sets, _ := ParseSetSpecs([]string{"random.thing=stringly"})
	out, err := ApplySets(Tree{}, sets, m)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := out.GetDotted("random.thing"); v != "stringly" {
		t.Errorf("got %v", v)
	}
}

func TestLoadTOMLFile_MissingIsEmpty(t *testing.T) {
	dir := t.TempDir()
	tree, err := LoadTOMLFile(filepath.Join(dir, "nope.toml"))
	if err != nil {
		t.Fatalf("missing file should be empty tree, got %v", err)
	}
	if len(tree) != 0 {
		t.Errorf("expected empty, got %v", tree)
	}
}

func TestLoadTOMLFile_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	body := `
[linear]
team_id = "abc"
ticket_prefix = "DEF"
labels = ["a", "b"]
`
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	tree, err := LoadTOMLFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := tree.GetDotted("linear.team_id"); v != "abc" {
		t.Errorf("team_id = %v", v)
	}
}

func TestEncodeTOML(t *testing.T) {
	tree := Tree{}
	tree.SetDotted("linear.team_id", "abc")
	tree.SetDotted("linear.ticket_prefix", "DEF")
	body, err := EncodeTOML(tree)
	if err != nil {
		t.Fatal(err)
	}
	out := string(body)
	if !strings.Contains(out, `team_id = "abc"`) {
		t.Errorf("encoded missing team_id: %s", out)
	}
	if !strings.Contains(out, `[linear]`) {
		t.Errorf("encoded missing section: %s", out)
	}
}
