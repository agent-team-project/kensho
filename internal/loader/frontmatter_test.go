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
