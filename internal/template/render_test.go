package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderTreeFromOS_VerbatimAndTmpl(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	// verbatim file
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	// .tmpl file with substitution
	if err := os.MkdirAll(filepath.Join(src, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	tmplBody := `prefix={{ .linear.ticket_prefix }} team={{ .linear.team_id }}`
	if err := os.WriteFile(filepath.Join(src, "agents", "config.toml.tmpl"), []byte(tmplBody), 0o644); err != nil {
		t.Fatal(err)
	}
	// executable .sh.tmpl preserves +x
	if err := os.WriteFile(filepath.Join(src, "scripts.sh.tmpl"), []byte("#!/bin/sh\necho {{ .linear.ticket_prefix }}\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// the manifest itself should be skippable via skipNames
	if err := os.WriteFile(filepath.Join(src, "template.toml"), []byte("[template]\nname=\"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "agents", "__pycache__"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "agents", "__pycache__", "agent.cpython-313.pyc"), []byte("bytecode"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".DS_Store"), []byte("finder"), 0o644); err != nil {
		t.Fatal(err)
	}

	data := Tree{}
	data.SetDotted("linear.team_id", "abc")
	data.SetDotted("linear.ticket_prefix", "ENG")

	results, err := RenderTreeFromOS(src, dst, data, map[string]bool{"template.toml": true})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	// Verbatim copy.
	got, err := os.ReadFile(filepath.Join(dst, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello world" {
		t.Errorf("verbatim = %q", got)
	}

	// Rendered .tmpl, suffix stripped.
	rendered, err := os.ReadFile(filepath.Join(dst, "agents", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(rendered) != "prefix=ENG team=abc" {
		t.Errorf("rendered = %q", rendered)
	}

	// .sh.tmpl rendered + executable.
	shPath := filepath.Join(dst, "scripts.sh")
	body, err := os.ReadFile(shPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "echo ENG") {
		t.Errorf("shell render = %q", body)
	}
	st, err := os.Stat(shPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&0o111 == 0 {
		t.Errorf("expected +x on rendered .sh.tmpl, got %o", st.Mode())
	}

	// template.toml must be skipped.
	if _, err := os.Stat(filepath.Join(dst, "template.toml")); !os.IsNotExist(err) {
		t.Errorf("template.toml should be skipped, found err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "agents", "__pycache__")); !os.IsNotExist(err) {
		t.Errorf("__pycache__ should be skipped, found err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".DS_Store")); !os.IsNotExist(err) {
		t.Errorf(".DS_Store should be skipped, found err=%v", err)
	}

	// Audit list.
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d (%v)", len(results), results)
	}
	rendCount := 0
	for _, r := range results {
		if r.Rendered {
			rendCount++
		}
	}
	if rendCount != 2 {
		t.Errorf("expected 2 rendered, got %d", rendCount)
	}
}

func TestRenderBytes_MissingKeyZero(t *testing.T) {
	body := `start={{ .missing }}end`
	out, err := RenderBytes("x", []byte(body), Tree{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), "start=") || !strings.HasSuffix(string(out), "end") {
		t.Errorf("missingkey=zero render = %q", out)
	}
}

func TestRenderBytes_BadTemplate(t *testing.T) {
	if _, err := RenderBytes("x", []byte("{{ unclosed"), Tree{}); err == nil {
		t.Error("expected parse error")
	}
}
