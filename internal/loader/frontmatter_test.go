package loader

import (
	"reflect"
	"testing"
)

func TestParseFrontmatter_NoFrontmatter(t *testing.T) {
	fm, body := ParseFrontmatter("just text\nno front")
	if len(fm) != 0 {
		t.Errorf("expected empty fm, got %v", fm)
	}
	if body != "just text\nno front" {
		t.Errorf("expected body unchanged, got %q", body)
	}
}

func TestParseFrontmatter_ScalarValues(t *testing.T) {
	in := "---\nname: foo\ndescription: a one liner\n---\nbody here\n"
	fm, body := ParseFrontmatter(in)
	if fm["name"] != "foo" {
		t.Errorf("name=%q", fm["name"])
	}
	if fm["description"] != "a one liner" {
		t.Errorf("description=%q", fm["description"])
	}
	if body != "body here\n" {
		t.Errorf("body=%q", body)
	}
}

func TestParseFrontmatter_QuotedValues(t *testing.T) {
	in := "---\ndouble: \"value with: colon\"\nsingle: 'another val'\n---\nbody"
	fm, _ := ParseFrontmatter(in)
	if fm["double"] != "value with: colon" {
		t.Errorf("double=%q", fm["double"])
	}
	if fm["single"] != "another val" {
		t.Errorf("single=%q", fm["single"])
	}
}

func TestParseFrontmatter_BlockScalar(t *testing.T) {
	in := "---\ndescription: |\n  first line\n  second line\n  third line\n---\nbody"
	fm, body := ParseFrontmatter(in)
	want := "first line\nsecond line\nthird line"
	if fm["description"] != want {
		t.Errorf("description=%q want %q", fm["description"], want)
	}
	if body != "body" {
		t.Errorf("body=%q", body)
	}
}

func TestParseFrontmatter_BlockScalarWithBlankLines(t *testing.T) {
	in := "---\ndescription: |\n  para one\n\n  para two\n---\nbody"
	fm, _ := ParseFrontmatter(in)
	want := "para one\n\npara two"
	if fm["description"] != want {
		t.Errorf("description=%q want %q", fm["description"], want)
	}
}

func TestParseFrontmatter_BlockScalarMixedScalar(t *testing.T) {
	in := "---\nname: foo\ndescription: |\n  multi\n  line\nstatus: ok\n---\nbody"
	fm, _ := ParseFrontmatter(in)
	want := map[string]string{
		"name":        "foo",
		"description": "multi\nline",
		"status":      "ok",
	}
	if !reflect.DeepEqual(fm, want) {
		t.Errorf("fm=%v want %v", fm, want)
	}
}

func TestParseFrontmatter_SkipsListsCommentsIndented(t *testing.T) {
	in := "---\n# a comment\nname: foo\n- list-item\n  indented\nbar: baz\n---\nbody"
	fm, _ := ParseFrontmatter(in)
	want := map[string]string{"name": "foo", "bar": "baz"}
	if !reflect.DeepEqual(fm, want) {
		t.Errorf("fm=%v want %v", fm, want)
	}
}

func TestParseFrontmatter_TrailingTripleDashOnly(t *testing.T) {
	// `---` at very end with no closing newline.
	in := "---\nname: foo\n---"
	fm, body := ParseFrontmatter(in)
	if fm["name"] != "foo" {
		t.Errorf("name=%q", fm["name"])
	}
	if body != "" {
		t.Errorf("body=%q want empty", body)
	}
}

func TestParseFrontmatter_NoClosingDelimiter(t *testing.T) {
	in := "---\nname: foo\nbody continues here"
	fm, body := ParseFrontmatter(in)
	if len(fm) != 0 {
		t.Errorf("expected empty fm without closing ---, got %v", fm)
	}
	if body != in {
		t.Errorf("body=%q want full input", body)
	}
}

func TestParseFrontmatterRich_ListValues(t *testing.T) {
	in := "---\nname: manager\nsubscribes:\n  - \"#blocked\"\n  - \"#review-requests\"\ndescription: a desc\n---\nbody"
	rich, body := ParseFrontmatterRich(in)
	if rich.Scalars["name"] != "manager" {
		t.Errorf("name=%q", rich.Scalars["name"])
	}
	if rich.Scalars["description"] != "a desc" {
		t.Errorf("desc=%q", rich.Scalars["description"])
	}
	got := rich.Lists["subscribes"]
	want := []string{"#blocked", "#review-requests"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("subscribes=%v want %v", got, want)
	}
	if body != "body" {
		t.Errorf("body=%q", body)
	}
}

func TestParseFrontmatterRich_UnquotedListItems(t *testing.T) {
	in := "---\nsubscribes:\n  - alpha\n  - beta\n---\n"
	rich, _ := ParseFrontmatterRich(in)
	got := rich.Lists["subscribes"]
	want := []string{"alpha", "beta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestParseFrontmatterRich_EmptyKeyDropsIfNoList(t *testing.T) {
	// `key:` with neither a value nor a `- ...` follow-up should drop
	// (matches the previous parser's behaviour for nested-mapping shapes).
	in := "---\nname: foo\nempty:\n  nested: thing\ndescription: d\n---\n"
	rich, _ := ParseFrontmatterRich(in)
	if _, ok := rich.Scalars["empty"]; ok {
		t.Errorf("empty: should not be in scalars")
	}
	if _, ok := rich.Lists["empty"]; ok {
		t.Errorf("empty: should not be in lists")
	}
	if rich.Scalars["description"] != "d" {
		t.Errorf("description=%q", rich.Scalars["description"])
	}
}

func TestParseFrontmatterRich_ListThenScalar(t *testing.T) {
	in := "---\nsubscribes:\n  - \"#one\"\n  - \"#two\"\nname: after\n---\n"
	rich, _ := ParseFrontmatterRich(in)
	if rich.Scalars["name"] != "after" {
		t.Errorf("scalar after list lost: %q", rich.Scalars["name"])
	}
	if !reflect.DeepEqual(rich.Lists["subscribes"], []string{"#one", "#two"}) {
		t.Errorf("subs=%v", rich.Lists["subscribes"])
	}
}

func TestParseFrontmatter_BackcompatScalarsOnly(t *testing.T) {
	// Old API still returns just the scalar map and ignores list-typed keys
	// — frontmatters with `subscribes: [...]` shouldn't break callers that
	// don't yet care.
	in := "---\nname: foo\nsubscribes:\n  - \"#x\"\ndescription: d\n---\n"
	fm, _ := ParseFrontmatter(in)
	if fm["name"] != "foo" || fm["description"] != "d" {
		t.Errorf("scalars: %v", fm)
	}
	if _, ok := fm["subscribes"]; ok {
		t.Errorf("subscribes leaked into scalar map")
	}
}
