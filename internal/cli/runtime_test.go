package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/runtimebin"
)

func withRuntimeLookPath(t *testing.T, fn func(string) (string, error)) {
	t.Helper()
	old := runtimeLookPath
	runtimeLookPath = fn
	t.Cleanup(func() { runtimeLookPath = old })
}

func TestRuntimeCommand_DefaultText(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "claude" {
			t.Fatalf("look path bin = %q, want claude", bin)
		}
		return "/usr/local/bin/claude", nil
	})

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "--target", t.TempDir()})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime failed: %v\nstderr: %s", err, errOut.String())
	}
	for _, want := range []string{
		"runtime:          claude",
		"binary:           claude",
		"path:             /usr/local/bin/claude",
		"daemon_dispatch:  yes",
		"direct_resume:    yes",
		"managed_resume:   yes",
		"resume:           yes",
		"subagents:        yes",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("runtime output missing %q:\n%s", want, out.String())
		}
	}
}

func TestRuntimeCommand_CodexJSON(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "codex")
	t.Setenv(runtimebin.EnvBinary, "")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex" {
			t.Fatalf("look path bin = %q, want codex", bin)
		}
		return "/opt/homebrew/bin/codex", nil
	})

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "--target", t.TempDir(), "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime --json failed: %v\nstderr: %s", err, errOut.String())
	}
	var info runtimeInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if info.Runtime != "codex" || info.Binary != "codex" || info.Path != "/opt/homebrew/bin/codex" {
		t.Fatalf("info = %+v, want codex path", info)
	}
	if !info.DirectRun || !info.DaemonDispatch || !info.DirectResume || !info.ManagedResume || !info.Resume || info.Subagents {
		t.Fatalf("codex capabilities = %+v, want direct plus daemon managed resume without subagents", info)
	}
	if len(info.Notes) == 0 {
		t.Fatalf("codex info missing limitation notes: %+v", info)
	}
}

func TestRuntimeCommand_Format(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "codex")
	t.Setenv(runtimebin.EnvBinary, "")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex" {
			t.Fatalf("look path bin = %q, want codex", bin)
		}
		return "/opt/homebrew/bin/codex", nil
	})

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "--target", t.TempDir(), "--format", "{{.Runtime}} {{.Binary}} {{.Available}} {{.DirectResume}} {{.ManagedResume}} {{.Resume}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime --format failed: %v\nstderr: %s", err, errOut.String())
	}
	if got := strings.TrimSpace(out.String()); got != "codex codex true true true true" {
		t.Fatalf("runtime format = %q", got)
	}
}

func TestRuntimeCommand_FormatRejectsJSON(t *testing.T) {
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "--target", t.TempDir(), "--json", "--format", "{{.Runtime}}"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("runtime --json --format succeeded")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Fatalf("error = %v, want exit 2", err)
	}
	if !strings.Contains(errOut.String(), "--format cannot be combined with --json") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestRuntimeProfileCommand_CodexJSON(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "codex")
	t.Setenv(runtimebin.EnvBinary, "")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex" {
			t.Fatalf("look path bin = %q, want codex", bin)
		}
		return "/opt/homebrew/bin/codex", nil
	})

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "profile", "--target", t.TempDir(), "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime profile --json failed: %v\nstderr: %s", err, errOut.String())
	}
	var info runtimeInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if info.Runtime != "codex" || info.Binary != "codex" || info.Path != "/opt/homebrew/bin/codex" {
		t.Fatalf("info = %+v, want codex path", info)
	}
	if !info.DirectRun || !info.DaemonDispatch || !info.DirectResume || !info.ManagedResume || !info.Resume || info.Subagents {
		t.Fatalf("codex capabilities = %+v, want direct plus daemon managed resume without subagents", info)
	}
}

func TestRuntimeProfileCommand_FormatRejectsJSON(t *testing.T) {
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "profile", "--target", t.TempDir(), "--json", "--format", "{{.Runtime}}"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("runtime profile --json --format succeeded")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Fatalf("error = %v, want exit 2", err)
	}
	if !strings.Contains(errOut.String(), "agent-team runtime profile: --format cannot be combined with --json") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestRuntimeSetUpdatesRepoConfigRuntimeSection(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	cfg := filepath.Join(tmp, ".agent_team", "config.toml")
	before := `[linear]
ticket_prefix = "SQU"

[runtime] # existing selection
# keep runtime notes
kind = "claude"
bin = "claude-dev"
extra = "kept"

[health]
status_stale_after = "10m"
`
	if err := os.WriteFile(cfg, []byte(before), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "set", "codex", "--target", tmp, "--runtime-bin", "codex-dev", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime set failed: %v\nstderr: %s", err, errOut.String())
	}
	var result runtimeSetResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if !result.Changed || result.Runtime != "codex" || result.Binary != "codex-dev" || result.ConfigPath == "" {
		t.Fatalf("result = %+v, want changed codex-dev", result)
	}
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	content := string(body)
	for _, want := range []string{
		`[linear]`,
		`[runtime]`,
		`kind = "codex"`,
		`binary = "codex-dev"`,
		`# keep runtime notes`,
		`extra = "kept"`,
		`[health]`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("config missing %q:\n%s", want, content)
		}
	}
	for _, unwanted := range []string{`kind = "claude"`, `bin = "claude-dev"`} {
		if strings.Contains(content, unwanted) {
			t.Fatalf("config still contains %q:\n%s", unwanted, content)
		}
	}
}

func TestRuntimeSetDryRunDoesNotWriteConfig(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "claude")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	cfg := filepath.Join(tmp, ".agent_team", "config.toml")
	before, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "set", "codex", "--target", tmp, "--dry-run", "--format", "{{.Runtime}} {{.Binary}} {{.Changed}} {{.DryRun}} {{len .Notes}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime set --dry-run failed: %v\nstderr: %s", err, errOut.String())
	}
	if got := strings.TrimSpace(out.String()); got != "codex codex true true 1" {
		t.Fatalf("format output = %q", got)
	}
	after, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read config after: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("dry-run changed config:\nbefore:\n%s\nafter:\n%s", string(before), string(after))
	}
}

func TestRuntimeSetDryRunCommands(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	cfg := filepath.Join(tmp, ".agent_team", "config.toml")
	before, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "set", "--target", tmp, "codex", "--runtime-bin", "codex-dev", "--dry-run", "--commands"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime set --dry-run --commands failed: %v\nstderr: %s", err, errOut.String())
	}
	want := strings.Join(shellQuoteArgs([]string{"agent-team", "runtime", "set", "--repo", tmp, "codex", "--runtime-bin", "codex-dev"}), " ") + "\n"
	if got := out.String(); got != want {
		t.Fatalf("commands output = %q, want %q", got, want)
	}
	after, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read config after: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("dry-run changed config:\nbefore:\n%s\nafter:\n%s", string(before), string(after))
	}

	rootScoped := NewRootCmd()
	rootScopedOut, rootScopedErr := &bytes.Buffer{}, &bytes.Buffer{}
	rootScoped.SetOut(rootScopedOut)
	rootScoped.SetErr(rootScopedErr)
	rootScoped.SetArgs([]string{"--repo", tmp, "runtime", "set", "claude", "--dry-run", "--commands"})
	if err := rootScoped.Execute(); err != nil {
		t.Fatalf("runtime set root --repo --dry-run --commands failed: %v\nstderr: %s", err, rootScopedErr.String())
	}
	wantRootScoped := strings.Join(shellQuoteArgs([]string{"agent-team", "runtime", "set", "--repo", tmp, "claude"}), " ") + "\n"
	if got := rootScopedOut.String(); got != wantRootScoped {
		t.Fatalf("root-scoped commands output = %q, want %q", got, wantRootScoped)
	}

	if err := os.WriteFile(cfg, []byte("[runtime]\nkind = \"codex\"\nbinary = \"codex-dev\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	noop := NewRootCmd()
	noopOut, noopErr := &bytes.Buffer{}, &bytes.Buffer{}
	noop.SetOut(noopOut)
	noop.SetErr(noopErr)
	noop.SetArgs([]string{"runtime", "set", "--target", tmp, "codex", "--runtime-bin", "codex-dev", "--dry-run", "--commands"})
	if err := noop.Execute(); err != nil {
		t.Fatalf("runtime set no-op --dry-run --commands failed: %v\nstderr: %s", err, noopErr.String())
	}
	if got := noopOut.String(); got != "" {
		t.Fatalf("no-op commands output = %q, want empty", got)
	}
}

func TestRuntimeSetRejectsInvalidRuntime(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "set", "llama", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("runtime set llama succeeded")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Fatalf("error = %v, want exit 2", err)
	}
	if !strings.Contains(errOut.String(), `runtime must be "claude" or "codex"`) {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestRuntimeUnsetRemovesRepoConfigRuntimeSection(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	cfg := filepath.Join(tmp, ".agent_team", "config.toml")
	before := `[linear]
ticket_prefix = "SQU"

[runtime]
kind = "codex"
binary = "codex-dev"

[health]
status_stale_after = "10m"
`
	if err := os.WriteFile(cfg, []byte(before), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "unset", "--target", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime unset failed: %v\nstderr: %s", err, errOut.String())
	}
	var result runtimeUnsetResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if !result.Changed || result.ConfigPath == "" {
		t.Fatalf("result = %+v, want changed config path", result)
	}
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	content := string(body)
	if strings.Contains(content, "[runtime]") || strings.Contains(content, `kind = "codex"`) || strings.Contains(content, `binary = "codex-dev"`) {
		t.Fatalf("runtime override still present:\n%s", content)
	}
	for _, want := range []string{`[linear]`, `ticket_prefix = "SQU"`, `[health]`} {
		if !strings.Contains(content, want) {
			t.Fatalf("config missing %q:\n%s", want, content)
		}
	}
}

func TestRuntimeUnsetDryRunCommandsUsesGlobalRepo(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	cfg := filepath.Join(tmp, ".agent_team", "config.toml")
	before := "[runtime]\nkind = \"codex\"\nbinary = \"codex-dev\"\n"
	if err := os.WriteFile(cfg, []byte(before), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"--repo", tmp, "runtime", "unset", "--dry-run", "--commands"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime unset --dry-run --commands failed: %v\nstderr: %s", err, errOut.String())
	}
	want := strings.Join(shellQuoteArgs([]string{"agent-team", "runtime", "unset", "--repo", tmp}), " ") + "\n"
	if got := out.String(); got != want {
		t.Fatalf("commands output = %q, want %q", got, want)
	}
	after, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read config after: %v", err)
	}
	if string(after) != before {
		t.Fatalf("dry-run changed config:\nbefore:\n%s\nafter:\n%s", before, string(after))
	}
}

func TestRuntimeUnsetDryRunPreservesConfigAndReportsEnvOverride(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "codex-wrapper")
	tmp := t.TempDir()
	initInto(t, tmp)
	cfg := filepath.Join(tmp, ".agent_team", "config.toml")
	before := `[runtime]
kind = "codex"
binary = "codex-dev"
extra = "kept"
`
	if err := os.WriteFile(cfg, []byte(before), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "unset", "--target", tmp, "--dry-run", "--format", "{{.Changed}} {{.DryRun}} {{len .Notes}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime unset --dry-run failed: %v\nstderr: %s", err, errOut.String())
	}
	if got := strings.TrimSpace(out.String()); got != "true true 1" {
		t.Fatalf("format output = %q", got)
	}
	after, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(after) != before {
		t.Fatalf("dry-run changed config:\nbefore:\n%s\nafter:\n%s", before, string(after))
	}
	next := unsetRuntimeConfigContent(before)
	if !strings.Contains(next, "[runtime]") || !strings.Contains(next, `extra = "kept"`) {
		t.Fatalf("unset should preserve runtime section with unknown keys:\n%s", next)
	}
	if strings.Contains(next, `kind = "codex"`) || strings.Contains(next, `binary = "codex-dev"`) {
		t.Fatalf("unset retained selector keys:\n%s", next)
	}
}

func TestRuntimeSetUnsetCommandsRejectInvalidFlagCombos(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "set requires dry run",
			args: []string{"runtime", "set", "codex", "--commands"},
			want: wantCommandsModeRequiresDryRun(),
		},
		{
			name: "set rejects json",
			args: []string{"runtime", "set", "codex", "--dry-run", "--commands", "--json"},
			want: wantCommandsModeConflict("--json"),
		},
		{
			name: "set rejects format",
			args: []string{"runtime", "set", "codex", "--dry-run", "--commands", "--format", "{{.Changed}}"},
			want: wantCommandsModeConflict("--format"),
		},
		{
			name: "unset requires dry run",
			args: []string{"runtime", "unset", "--commands"},
			want: wantCommandsModeRequiresDryRun(),
		},
		{
			name: "unset rejects json",
			args: []string{"runtime", "unset", "--dry-run", "--commands", "--json"},
			want: wantCommandsModeConflict("--json"),
		},
		{
			name: "unset rejects format",
			args: []string{"runtime", "unset", "--dry-run", "--commands", "--format", "{{.Changed}}"},
			want: wantCommandsModeConflict("--format"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(errOut)
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("%v succeeded", tt.args)
			}
			var ec ExitCode
			if !errors.As(err, &ec) || int(ec) != 2 {
				t.Fatalf("error = %v, want exit 2", err)
			}
			if !strings.Contains(errOut.String(), tt.want) {
				t.Fatalf("stderr = %q, want %q", errOut.String(), tt.want)
			}
		})
	}
}

func TestRuntimeLsJSONListsSupportedRuntimes(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		switch bin {
		case "claude":
			return "/usr/local/bin/claude", nil
		case "codex":
			return "", exec.ErrNotFound
		default:
			t.Fatalf("look path bin = %q", bin)
			return "", exec.ErrNotFound
		}
	})

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "ls", "--target", t.TempDir(), "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime ls --json failed: %v\nstderr: %s", err, errOut.String())
	}
	var rows []runtimeInfo
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want claude and codex", rows)
	}
	byRuntime := map[string]runtimeInfo{}
	for _, row := range rows {
		byRuntime[row.Runtime] = row
	}
	if row := byRuntime["claude"]; !row.Selected || !row.Available || row.Path != "/usr/local/bin/claude" {
		t.Fatalf("claude row = %+v, want selected available path", row)
	}
	if row := byRuntime["claude"]; row.ProbeCommand != "agent-team runtime probe --runtime claude" || row.SelectCommand != "agent-team runtime set claude" {
		t.Fatalf("claude commands = %+v, want probe and select hints", row)
	}
	if row := byRuntime["codex"]; row.Selected || row.Available || row.Binary != "codex" {
		t.Fatalf("codex row = %+v, want unselected unavailable default", row)
	}
	if row := byRuntime["codex"]; row.ProbeCommand != "agent-team runtime probe --runtime codex" || row.DaemonProbeCommand != "agent-team runtime probe --codex-daemon-check" || row.SelectCommand != "agent-team runtime set codex" {
		t.Fatalf("codex commands = %+v, want probe, daemon probe, and select hints", row)
	}
}

func TestRuntimeLsUsesRepoSelectedBinary(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	appendRuntimeConfigForRuntimeTest(t, tmp, "codex", "codex-wrapper")
	seen := map[string]bool{}
	withRuntimeLookPath(t, func(bin string) (string, error) {
		seen[bin] = true
		return "/usr/local/bin/" + bin, nil
	})

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "ls", "--target", tmp, "--format", "{{.Runtime}} {{.Selected}} {{.Binary}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime ls --format failed: %v\nstderr: %s", err, errOut.String())
	}
	for _, want := range []string{"claude false claude", "codex true codex-wrapper"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("runtime ls format missing %q:\n%s", want, out.String())
		}
	}
	if !seen["claude"] || !seen["codex-wrapper"] || seen["codex"] {
		t.Fatalf("looked up binaries = %+v, want claude and selected codex-wrapper only", seen)
	}
}

func TestRuntimeLsCommandsPreserveRepoScopeAndCustomBinary(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	appendRuntimeConfigForRuntimeTest(t, tmp, "codex", "codex-wrapper")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		return "/usr/local/bin/" + bin, nil
	})

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"--repo", tmp, "runtime", "ls", "--commands"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime ls --commands failed: %v\nstderr: %s", err, errOut.String())
	}
	want := strings.Join([]string{
		strings.Join(shellQuoteArgs([]string{"agent-team", "--repo", tmp, "runtime", "probe", "--runtime", "claude"}), " "),
		strings.Join(shellQuoteArgs([]string{"agent-team", "--repo", tmp, "runtime", "probe", "--runtime", "codex", "--runtime-bin", "codex-wrapper"}), " "),
		strings.Join(shellQuoteArgs([]string{"agent-team", "--repo", tmp, "runtime", "probe", "--codex-daemon-check", "--runtime-bin", "codex-wrapper"}), " "),
		"",
	}, "\n")
	if out.String() != want {
		t.Fatalf("commands = %q, want %q", out.String(), want)
	}
}

func TestRuntimeLsCommandsRejectsStructuredModes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "json", args: []string{"runtime", "ls", "--commands", "--json"}, want: wantCommandsModeConflict("--json")},
		{name: "format", args: []string{"runtime", "ls", "--commands", "--format", "{{.Runtime}}"}, want: wantCommandsModeConflict("--format")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(errOut)
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("%v succeeded", tt.args)
			}
			var ec ExitCode
			if !errors.As(err, &ec) || int(ec) != 2 {
				t.Fatalf("error = %v, want exit 2", err)
			}
			if !strings.Contains(errOut.String(), tt.want) {
				t.Fatalf("stderr = %q, want %q", errOut.String(), tt.want)
			}
			if out.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", out.String())
			}
		})
	}
}

func TestRuntimeMetadataLsFiltersSummaryAndFormat(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	worktree := filepath.Join(tmp, ".agent_team", "worktrees", "worker-squ-130")
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-130",
		Ticket:    "SQU-130",
		Target:    "worker",
		Instance:  "worker-squ-130",
		Status:    job.StatusRunning,
		Branch:    "worktree-worker-squ-130",
		Worktree:  worktree,
		PR:        "https://github.com/acme/repo/pull/130",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager-squ-130", Job: "squ-130", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: tmp, StartedAt: now.Add(-40 * time.Minute)},
		{Instance: "worker-squ-130", Job: "squ-130", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusCrashed, StartedAt: now.Add(-30 * time.Minute), ExitedAt: now.Add(-5 * time.Minute)},
		{Instance: "adhoc-codex", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: tmp, StartedAt: now.Add(-10 * time.Minute)},
		{Instance: "other-claude", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusStopped, StartedAt: now.Add(-20 * time.Minute), StoppedAt: now.Add(-15 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	codex := NewRootCmd()
	codexOut, codexErr := &bytes.Buffer{}, &bytes.Buffer{}
	codex.SetOut(codexOut)
	codex.SetErr(codexErr)
	codex.SetArgs([]string{"runtime", "metadata", "ls", "--target", tmp, "--runtime", "codex", "--json"})
	if err := codex.Execute(); err != nil {
		t.Fatalf("runtime metadata ls json: %v\nstderr=%s", err, codexErr.String())
	}
	var rows []teamRuntimeRow
	if err := json.Unmarshal(codexOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode runtime metadata rows: %v\nbody=%s", err, codexOut.String())
	}
	if got := teamRuntimeRowInstances(rows); strings.Join(got, ",") != "adhoc-codex,worker-squ-130" {
		t.Fatalf("runtime metadata rows = %v, want all Codex records", got)
	}
	var worker *teamRuntimeRow
	for i := range rows {
		if rows[i].Instance == "worker-squ-130" {
			worker = &rows[i]
			break
		}
	}
	if worker == nil {
		t.Fatalf("worker runtime row missing: %+v", rows)
	}
	if worker.Job != "squ-130" || worker.Ticket != "SQU-130" || worker.Branch != "worktree-worker-squ-130" || worker.PR != "https://github.com/acme/repo/pull/130" || worker.Workspace != filepath.ToSlash(worktree) {
		t.Fatalf("runtime metadata row was not enriched from job: %+v", *worker)
	}

	summary := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(summaryOut)
	summary.SetErr(summaryErr)
	summary.SetArgs([]string{"runtime", "metadata", "ls", "--target", tmp, "--runtime", "codex", "--summary", "--json"})
	if err := summary.Execute(); err != nil {
		t.Fatalf("runtime metadata summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var counts teamRuntimeSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &counts); err != nil {
		t.Fatalf("decode runtime metadata summary: %v\nbody=%s", err, summaryOut.String())
	}
	if counts.Total != 2 || counts.Running != 1 || counts.Crashed != 1 || counts.WithJob != 1 || counts.Runtimes["codex"] != 2 || counts.Agents["worker"] != 2 {
		t.Fatalf("runtime metadata summary = %+v", counts)
	}

	instance := NewRootCmd()
	instanceOut, instanceErr := &bytes.Buffer{}, &bytes.Buffer{}
	instance.SetOut(instanceOut)
	instance.SetErr(instanceErr)
	instance.SetArgs([]string{"runtime", "metadata", "ls", "worker-squ-130", "--target", tmp, "--json"})
	if err := instance.Execute(); err != nil {
		t.Fatalf("runtime metadata positional filter: %v\nstderr=%s", err, instanceErr.String())
	}
	rows = nil
	if err := json.Unmarshal(instanceOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode runtime metadata positional rows: %v\nbody=%s", err, instanceOut.String())
	}
	if got := teamRuntimeRowInstances(rows); strings.Join(got, ",") != "worker-squ-130" {
		t.Fatalf("runtime metadata positional rows = %v", got)
	}

	latest := NewRootCmd()
	latestOut, latestErr := &bytes.Buffer{}, &bytes.Buffer{}
	latest.SetOut(latestOut)
	latest.SetErr(latestErr)
	latest.SetArgs([]string{"runtime", "metadata", "ls", "--target", tmp, "--latest", "--json"})
	if err := latest.Execute(); err != nil {
		t.Fatalf("runtime metadata latest: %v\nstderr=%s", err, latestErr.String())
	}
	rows = nil
	if err := json.Unmarshal(latestOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode runtime metadata latest rows: %v\nbody=%s", err, latestOut.String())
	}
	if got := teamRuntimeRowInstances(rows); strings.Join(got, ",") != "adhoc-codex" {
		t.Fatalf("runtime metadata latest rows = %v", got)
	}

	formatted := NewRootCmd()
	formattedOut, formattedErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatted.SetOut(formattedOut)
	formatted.SetErr(formattedErr)
	formatted.SetArgs([]string{"runtime", "metadata", "ls", "--target", tmp, "--last", "2", "--sort", "started", "--format", "{{.Instance}} {{.Runtime}}"})
	if err := formatted.Execute(); err != nil {
		t.Fatalf("runtime metadata format: %v\nstderr=%s", err, formattedErr.String())
	}
	if got, want := strings.TrimSpace(formattedOut.String()), "adhoc-codex codex\nother-claude claude"; got != want {
		t.Fatalf("runtime metadata format = %q, want %q", got, want)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"--repo", tmp, "runtime", "metadata", "ls", "--runtime", "codex", "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("runtime metadata commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommands := strings.Join([]string{
		strings.Join(shellQuoteArgs([]string{"agent-team", "runtime", "metadata", "show", "--repo", tmp, "adhoc-codex"}), " "),
		strings.Join(shellQuoteArgs([]string{"agent-team", "runtime", "metadata", "show", "--repo", tmp, "worker-squ-130"}), " "),
	}, "\n")
	if got := strings.TrimSpace(commandsOut.String()); got != wantCommands {
		t.Fatalf("runtime metadata commands = %q, want %q", got, wantCommands)
	}

	unhealthy := NewRootCmd()
	unhealthyOut, unhealthyErr := &bytes.Buffer{}, &bytes.Buffer{}
	unhealthy.SetOut(unhealthyOut)
	unhealthy.SetErr(unhealthyErr)
	unhealthy.SetArgs([]string{"runtime", "metadata", "ls", "--target", tmp, "--unhealthy", "--format", "{{.Instance}} {{.Unhealthy}}"})
	if err := unhealthy.Execute(); err != nil {
		t.Fatalf("runtime metadata unhealthy: %v\nstderr=%s", err, unhealthyErr.String())
	}
	if got, want := strings.TrimSpace(unhealthyOut.String()), "worker-squ-130 true"; got != want {
		t.Fatalf("runtime metadata unhealthy = %q, want %q", got, want)
	}
}

func TestRuntimeMetadataLsRejectsInvalidModes(t *testing.T) {
	tmp := t.TempDir()
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "format json",
			args: []string{"runtime", "metadata", "ls", "--target", tmp, "--format", "{{.Instance}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "commands json",
			args: []string{"runtime", "metadata", "ls", "--target", tmp, "--commands", "--json"},
			want: "--commands cannot be combined",
		},
		{
			name: "commands summary",
			args: []string{"runtime", "metadata", "ls", "--target", tmp, "--commands", "--summary"},
			want: "--commands cannot be combined",
		},
		{
			name: "commands format",
			args: []string{"runtime", "metadata", "ls", "--target", tmp, "--commands", "--format", "{{.Instance}}"},
			want: "--commands cannot be combined",
		},
		{
			name: "format summary",
			args: []string{"runtime", "metadata", "ls", "--target", tmp, "--format", "{{.Instance}}", "--summary"},
			want: "--format cannot be combined",
		},
		{
			name: "negative last",
			args: []string{"runtime", "metadata", "ls", "--target", tmp, "--last", "-1"},
			want: "--last must be >= 0",
		},
		{
			name: "latest last",
			args: []string{"runtime", "metadata", "ls", "--target", tmp, "--latest", "--last", "2"},
			want: "choose one of --latest or --last",
		},
		{
			name: "bad sort",
			args: []string{"runtime", "metadata", "ls", "--target", tmp, "--sort", "age"},
			want: "unknown --sort",
		},
		{
			name: "empty instance",
			args: []string{"runtime", "metadata", "ls", " ", "--target", tmp},
			want: "instance names must be non-empty",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil {
				t.Fatalf("%s succeeded unexpectedly; stdout=%s", tc.name, out.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("%s stderr = %q, want %q", tc.name, stderr.String(), tc.want)
			}
			if out.Len() != 0 {
				t.Fatalf("%s wrote stdout: %q", tc.name, out.String())
			}
		})
	}
}

func TestRuntimeMetadataShowEnrichesAndRenders(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Date(2026, 6, 29, 10, 30, 0, 0, time.UTC)
	exited := now.Add(20 * time.Minute)
	exitCode := 17
	worktree := filepath.Join(tmp, ".agent_team", "worktrees", "worker-squ-131")
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-131",
		Ticket:    "SQU-131",
		Target:    "worker",
		Instance:  "worker-squ-131",
		Status:    job.StatusFailed,
		Branch:    "worktree-worker-squ-131",
		Worktree:  worktree,
		PR:        "https://github.com/acme/repo/pull/131",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:      "worker-squ-131",
		Job:           "squ-131",
		Agent:         "worker",
		Runtime:       string(runtimebin.KindCodex),
		RuntimeBinary: "codex-dev",
		Status:        daemon.StatusCrashed,
		PID:           12345,
		SessionID:     "session-131",
		StartedAt:     now,
		ExitedAt:      exited,
		ExitCode:      &exitCode,
		LogPath:       filepath.Join(teamDir, "daemon", "worker-squ-131", "child.log"),
		Adopted:       true,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"runtime", "metadata", "show", "worker-squ-131", "--target", tmp})
	if err := text.Execute(); err != nil {
		t.Fatalf("runtime metadata show text: %v\nstderr=%s", err, textErr.String())
	}
	body := textOut.String()
	for _, want := range []string{
		"runtime:",
		"instance:      worker-squ-131",
		"lifecycle:     crashed",
		"runtime:       codex",
		"binary:        codex-dev",
		"job:           squ-131",
		"ticket:        SQU-131",
		"branch:        worktree-worker-squ-131",
		"pr:            https://github.com/acme/repo/pull/131",
		"pid:           12345",
		"runtime_stale: no",
		"unhealthy:     yes",
		"workspace:     " + filepath.ToSlash(worktree),
		"session_id:    session-131",
		"adopted:       yes",
		"started_at:    2026-06-29T10:30:00Z",
		"exited_at:     2026-06-29T10:50:00Z",
		"exit_code:     17",
		"log:           " + filepath.ToSlash(filepath.Join(teamDir, "daemon", "worker-squ-131", "child.log")),
		"age:",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("runtime metadata show text missing %q:\n%s", want, body)
		}
	}

	jsonCmd := NewRootCmd()
	jsonOut, jsonErr := &bytes.Buffer{}, &bytes.Buffer{}
	jsonCmd.SetOut(jsonOut)
	jsonCmd.SetErr(jsonErr)
	jsonCmd.SetArgs([]string{"runtime", "metadata", "show", "worker-squ-131", "--target", tmp, "--json"})
	if err := jsonCmd.Execute(); err != nil {
		t.Fatalf("runtime metadata show json: %v\nstderr=%s", err, jsonErr.String())
	}
	var row teamRuntimeRow
	if err := json.Unmarshal(jsonOut.Bytes(), &row); err != nil {
		t.Fatalf("decode runtime metadata show json: %v\nbody=%s", err, jsonOut.String())
	}
	if row.Instance != "worker-squ-131" || row.Job != "squ-131" || row.Ticket != "SQU-131" || row.Workspace != filepath.ToSlash(worktree) || !row.Unhealthy || row.ExitCode == nil || *row.ExitCode != 17 {
		t.Fatalf("runtime metadata show row = %+v", row)
	}

	formatted := NewRootCmd()
	formattedOut, formattedErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatted.SetOut(formattedOut)
	formatted.SetErr(formattedErr)
	formatted.SetArgs([]string{"runtime", "metadata", "show", "worker-squ-131", "--target", tmp, "--format", "{{.Instance}} {{.Ticket}} {{.Unhealthy}}"})
	if err := formatted.Execute(); err != nil {
		t.Fatalf("runtime metadata show format: %v\nstderr=%s", err, formattedErr.String())
	}
	if got, want := strings.TrimSpace(formattedOut.String()), "worker-squ-131 SQU-131 true"; got != want {
		t.Fatalf("runtime metadata show format = %q, want %q", got, want)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"--repo", tmp, "runtime", "metadata", "show", "worker-squ-131", "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("runtime metadata show commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommands := strings.Join([]string{
		strings.Join(shellQuoteArgs([]string{"agent-team", "job", "show", "--repo", tmp, "squ-131"}), " "),
		strings.Join(shellQuoteArgs([]string{"agent-team", "inspect", "--repo", tmp, "worker-squ-131"}), " "),
		strings.Join(shellQuoteArgs([]string{"agent-team", "logs", "--repo", tmp, "worker-squ-131"}), " "),
		strings.Join(shellQuoteArgs([]string{"agent-team", "resume-plan", "--repo", tmp, "worker-squ-131"}), " "),
	}, "\n")
	if got := strings.TrimSpace(commandsOut.String()); got != wantCommands {
		t.Fatalf("runtime metadata show commands = %q, want %q", got, wantCommands)
	}
}

func TestRuntimeMetadataShowRejectsInvalidModesAndMissingRecords(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "format json",
			args: []string{"runtime", "metadata", "show", "worker", "--target", tmp, "--format", "{{.Instance}}", "--json"},
			want: "--format cannot be combined with --json",
		},
		{
			name: "commands json",
			args: []string{"runtime", "metadata", "show", "worker", "--target", tmp, "--commands", "--json"},
			want: "--commands cannot be combined",
		},
		{
			name: "commands format",
			args: []string{"runtime", "metadata", "show", "worker", "--target", tmp, "--commands", "--format", "{{.Instance}}"},
			want: "--commands cannot be combined",
		},
		{
			name: "empty instance",
			args: []string{"runtime", "metadata", "show", " ", "--target", tmp},
			want: "instance name must be non-empty",
		},
		{
			name: "missing record",
			args: []string{"runtime", "metadata", "show", "missing", "--target", tmp},
			want: `metadata for instance "missing" not found`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil {
				t.Fatalf("%s succeeded unexpectedly; stdout=%s", tc.name, out.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("%s stderr = %q, want %q", tc.name, stderr.String(), tc.want)
			}
			if out.Len() != 0 {
				t.Fatalf("%s wrote stdout: %q", tc.name, out.String())
			}
		})
	}
}

func appendRuntimeConfigForRuntimeTest(t *testing.T, root, kind, binary string) {
	t.Helper()
	cfg := filepath.Join(root, ".agent_team", "config.toml")
	f, err := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open config: %v", err)
	}
	if _, err := f.WriteString("\n[runtime]\nkind = \"" + kind + "\"\nbinary = \"" + binary + "\"\n"); err != nil {
		_ = f.Close()
		t.Fatalf("write config: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close config: %v", err)
	}
}

func TestRuntimeCommand_RepoConfigCodexJSON(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	appendRuntimeConfigForRuntimeTest(t, tmp, "codex", "codex-wrapper")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex-wrapper" {
			t.Fatalf("look path bin = %q, want codex-wrapper", bin)
		}
		return "/usr/local/bin/codex-wrapper", nil
	})

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "--target", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime --json failed: %v\nstderr: %s", err, errOut.String())
	}
	var info runtimeInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if info.Runtime != "codex" || info.Binary != "codex-wrapper" || info.ConfigPath == "" {
		t.Fatalf("info = %+v, want config-backed codex", info)
	}
}

func TestRuntimeCommand_RuntimeFlagOverridesEnvRuntimeAndBinary(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "bad-env-runtime")
	t.Setenv(runtimebin.EnvBinary, "claude-env-wrapper")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex" {
			t.Fatalf("look path bin = %q, want codex", bin)
		}
		return "/usr/local/bin/codex", nil
	})

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "--target", t.TempDir(), "--runtime", "codex", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime --runtime codex failed: %v\nstderr: %s", err, errOut.String())
	}
	var info runtimeInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if info.Runtime != "codex" || info.Binary != "codex" {
		t.Fatalf("info = %+v, want codex default binary from runtime flag", info)
	}
}

func TestRuntimeCommand_RuntimeBinFlagOverridesSelectedRuntimeBinary(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	appendRuntimeConfigForRuntimeTest(t, tmp, "codex", "codex-wrapper")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex-dev" {
			t.Fatalf("look path bin = %q, want codex-dev", bin)
		}
		return "/usr/local/bin/codex-dev", nil
	})

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "--target", tmp, "--runtime-bin", "codex-dev", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime --runtime-bin failed: %v\nstderr: %s", err, errOut.String())
	}
	var info runtimeInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if info.Runtime != "codex" || info.Binary != "codex-dev" {
		t.Fatalf("info = %+v, want config kind with explicit binary", info)
	}
}

func TestRuntimeCommand_RepoFlagOverridesTarget(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	appendRuntimeConfigForRuntimeTest(t, tmp, "codex", "codex-wrapper")
	badTarget := t.TempDir()
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex-wrapper" {
			t.Fatalf("look path bin = %q, want codex-wrapper", bin)
		}
		return "/usr/local/bin/codex-wrapper", nil
	})

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"--repo", tmp, "runtime", "--target", badTarget, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime with --repo override: %v\nstderr: %s", err, errOut.String())
	}
	var info runtimeInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	wantRoot := tmp
	if eval, err := filepath.EvalSymlinks(wantRoot); err == nil {
		wantRoot = eval
	}
	wantConfig := filepath.ToSlash(filepath.Join(wantRoot, ".agent_team", "config.toml"))
	if info.Binary != "codex-wrapper" || info.ConfigPath != wantConfig {
		t.Fatalf("info = %+v, want config from --repo %s", info, wantConfig)
	}
}

func TestRuntimeResumePlanClaudeText(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:      "manager",
		Agent:         "manager",
		Runtime:       string(runtimebin.KindClaude),
		RuntimeBinary: "claude-dev",
		Workspace:     tmp,
		PID:           1234,
		SessionID:     "sid-manager",
		StartedAt:     time.Now().UTC(),
		Status:        daemon.StatusStopped,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "resume-plan", "manager", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime resume-plan: %v\nstderr=%s", err, errOut.String())
	}
	for _, want := range []string{
		"instance:                 manager",
		"runtime:                  claude",
		"managed_resume:           yes",
		"can_managed_resume:       yes",
		"recommended_action:       start",
		"recommended_command:      agent-team start manager",
		"resume_command:           claude-dev --resume sid-manager",
		"start_command:            agent-team start manager",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("resume plan missing %q:\n%s", want, out.String())
		}
	}
}

func TestRuntimeResumePlanMarksStaleRunningMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	restorePIDLiveCheck := daemon.SetPidLiveCheckForTest(func(pid int) bool {
		return pid == 99
	})
	t.Cleanup(restorePIDLiveCheck)
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-55",
		Ticket:    "SQU-55",
		Target:    "manager",
		Instance:  "stale-manager",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}
	for _, meta := range []*daemon.Metadata{
		{
			Instance:      "live-manager",
			Agent:         "manager",
			Runtime:       string(runtimebin.KindClaude),
			RuntimeBinary: "claude",
			Workspace:     tmp,
			PID:           99,
			SessionID:     "sid-live",
			StartedAt:     now,
			Status:        daemon.StatusRunning,
		},
		{
			Instance:      "stale-manager",
			Agent:         "manager",
			Runtime:       string(runtimebin.KindClaude),
			RuntimeBinary: "claude",
			Workspace:     tmp,
			PID:           4242,
			SessionID:     "sid-stale",
			StartedAt:     now,
			Status:        daemon.StatusRunning,
		},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata: %v", err)
		}
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "resume-plan", "--target", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime resume-plan json: %v\nstderr=%s", err, errOut.String())
	}
	var plans []runtimeResumePlan
	if err := json.Unmarshal(out.Bytes(), &plans); err != nil {
		t.Fatalf("decode resume plans: %v\nbody=%s", err, out.String())
	}
	byInstance := map[string]runtimeResumePlan{}
	for _, plan := range plans {
		byInstance[plan.Instance] = plan
	}
	live := byInstance["live-manager"]
	if live.Stale || live.RecommendedAction != "attach" || live.RecommendedCommand != "agent-team attach live-manager --dry-run" {
		t.Fatalf("live plan = %+v", live)
	}
	stale := byInstance["stale-manager"]
	if !stale.Stale || stale.RecommendedAction != "start" || stale.RecommendedCommand != "agent-team start stale-manager" {
		t.Fatalf("stale plan = %+v", stale)
	}
	if !strings.Contains(stale.Detail, "recorded running pid is not live") {
		t.Fatalf("stale detail = %q", stale.Detail)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"runtime", "resume-plan", "stale-manager", "--target", tmp})
	if err := text.Execute(); err != nil {
		t.Fatalf("runtime resume-plan text: %v\nstderr=%s", err, textErr.String())
	}
	if !strings.Contains(textOut.String(), "stale:                    yes") ||
		!strings.Contains(textOut.String(), "recommended_command:      agent-team start stale-manager") {
		t.Fatalf("stale text = %s", textOut.String())
	}

	summary := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(summaryOut)
	summary.SetErr(summaryErr)
	summary.SetArgs([]string{"runtime", "resume-plan", "--target", tmp, "--summary", "--json"})
	if err := summary.Execute(); err != nil {
		t.Fatalf("runtime resume-plan summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var counts runtimeResumeSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &counts); err != nil {
		t.Fatalf("decode resume-plan summary: %v\nbody=%s", err, summaryOut.String())
	}
	if counts.Total != 2 || counts.Stale != 1 || counts.Unhealthy != 1 || counts.Actions["start"] != 1 || counts.Actions["attach"] != 1 {
		t.Fatalf("resume-plan summary = %+v", counts)
	}

	shortcut := NewRootCmd()
	shortcutOut, shortcutErr := &bytes.Buffer{}, &bytes.Buffer{}
	shortcut.SetOut(shortcutOut)
	shortcut.SetErr(shortcutErr)
	shortcut.SetArgs([]string{"resume-plan", "--repo", tmp, "--summary", "--json"})
	if err := shortcut.Execute(); err != nil {
		t.Fatalf("resume-plan shortcut summary: %v\nstderr=%s", err, shortcutErr.String())
	}
	var shortcutCounts runtimeResumeSummary
	if err := json.Unmarshal(shortcutOut.Bytes(), &shortcutCounts); err != nil {
		t.Fatalf("decode shortcut resume-plan summary: %v\nbody=%s", err, shortcutOut.String())
	}
	if shortcutCounts.Total != counts.Total || shortcutCounts.Stale != counts.Stale || shortcutCounts.Actions["start"] != counts.Actions["start"] || shortcutCounts.Actions["attach"] != counts.Actions["attach"] {
		t.Fatalf("shortcut resume-plan summary = %+v, want %+v", shortcutCounts, counts)
	}

	filtered := NewRootCmd()
	filteredOut, filteredErr := &bytes.Buffer{}, &bytes.Buffer{}
	filtered.SetOut(filteredOut)
	filtered.SetErr(filteredErr)
	filtered.SetArgs([]string{"runtime", "resume-plan", "--target", tmp, "--action", "start", "--stale", "--format", "{{.Instance}} {{.Stale}} {{.RecommendedCommand}}"})
	if err := filtered.Execute(); err != nil {
		t.Fatalf("runtime resume-plan stale action filter: %v\nstderr=%s", err, filteredErr.String())
	}
	if got := strings.TrimSpace(filteredOut.String()); got != "stale-manager true agent-team start stale-manager" {
		t.Fatalf("filtered stale plan = %q", got)
	}

	staleSummary := NewRootCmd()
	staleSummaryOut, staleSummaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	staleSummary.SetOut(staleSummaryOut)
	staleSummary.SetErr(staleSummaryErr)
	staleSummary.SetArgs([]string{"runtime", "resume-plan", "--target", tmp, "--runtime-stale", "--summary", "--json"})
	if err := staleSummary.Execute(); err != nil {
		t.Fatalf("runtime resume-plan runtime-stale summary: %v\nstderr=%s", err, staleSummaryErr.String())
	}
	var staleCounts runtimeResumeSummary
	if err := json.Unmarshal(staleSummaryOut.Bytes(), &staleCounts); err != nil {
		t.Fatalf("decode stale resume-plan summary: %v\nbody=%s", err, staleSummaryOut.String())
	}
	if staleCounts.Total != 1 || staleCounts.Stale != 1 || staleCounts.Unhealthy != 1 || staleCounts.Actions["start"] != 1 {
		t.Fatalf("stale resume-plan summary = %+v", staleCounts)
	}

	jobFiltered := NewRootCmd()
	jobFilteredOut, jobFilteredErr := &bytes.Buffer{}, &bytes.Buffer{}
	jobFiltered.SetOut(jobFilteredOut)
	jobFiltered.SetErr(jobFilteredErr)
	jobFiltered.SetArgs([]string{"job", "resume-plan", "SQU-55", "--repo", tmp, "--runtime-stale", "--format", "{{.Job}} {{.Instance}} {{.Stale}} {{.RecommendedCommand}}"})
	if err := jobFiltered.Execute(); err != nil {
		t.Fatalf("job resume-plan runtime-stale filter: %v\nstderr=%s", err, jobFilteredErr.String())
	}
	if got := strings.TrimSpace(jobFilteredOut.String()); got != "squ-55 stale-manager true agent-team start stale-manager" {
		t.Fatalf("job stale plan = %q", got)
	}

	fallbacks := NewRootCmd()
	fallbacksOut, fallbacksErr := &bytes.Buffer{}, &bytes.Buffer{}
	fallbacks.SetOut(fallbacksOut)
	fallbacks.SetErr(fallbacksErr)
	fallbacks.SetArgs([]string{"resume-plan", "--repo", tmp, "--job", "SQU-55", "--runtime-stale", "--commands", "--fallbacks"})
	if err := fallbacks.Execute(); err != nil {
		t.Fatalf("runtime resume-plan fallbacks: %v\nstderr=%s", err, fallbacksErr.String())
	}
	if got, want := strings.TrimSpace(fallbacksOut.String()), strings.Join([]string{
		strings.Join(shellQuoteArgs([]string{"agent-team", "start", "--repo", tmp, "stale-manager"}), " "),
		strings.Join(shellQuoteArgs([]string{"agent-team", "job", "attach", "--repo", tmp, "squ-55", "--dry-run"}), " "),
		strings.Join(shellQuoteArgs([]string{"agent-team", "attach", "--repo", tmp, "stale-manager", "--dry-run"}), " "),
		strings.Join(shellQuoteArgs([]string{"agent-team", "job", "logs", "--repo", tmp, "squ-55", "--follow"}), " "),
		strings.Join(shellQuoteArgs([]string{"agent-team", "logs", "--repo", tmp, "stale-manager", "--follow"}), " "),
		"claude --resume sid-stale",
	}, "\n"); got != want {
		t.Fatalf("fallback commands = %q, want %q", got, want)
	}
}

func TestRuntimeResumePlanUnhealthyFilter(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	restorePIDLiveCheck := daemon.SetPidLiveCheckForTest(func(pid int) bool {
		return pid == 99
	})
	t.Cleanup(restorePIDLiveCheck)
	now := time.Now().UTC()
	for _, meta := range []*daemon.Metadata{
		{
			Instance:      "crashed-worker",
			Agent:         "worker",
			Runtime:       string(runtimebin.KindCodex),
			RuntimeBinary: "codex",
			Workspace:     tmp,
			StartedAt:     now.Add(-20 * time.Minute),
			Status:        daemon.StatusCrashed,
		},
		{
			Instance:      "live-manager",
			Agent:         "manager",
			Runtime:       string(runtimebin.KindClaude),
			RuntimeBinary: "claude",
			Workspace:     tmp,
			PID:           99,
			SessionID:     "sid-live",
			StartedAt:     now,
			Status:        daemon.StatusRunning,
		},
		{
			Instance:      "stale-manager",
			Agent:         "manager",
			Runtime:       string(runtimebin.KindClaude),
			RuntimeBinary: "claude",
			Workspace:     tmp,
			PID:           4242,
			SessionID:     "sid-stale",
			StartedAt:     now,
			Status:        daemon.StatusRunning,
		},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "resume-plan", "--target", tmp, "--unhealthy", "--format", "{{.Instance}} {{.RecommendedAction}} {{.Stale}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime resume-plan unhealthy filter: %v\nstderr=%s", err, errOut.String())
	}
	got := strings.TrimSpace(out.String())
	want := strings.Join([]string{
		"crashed-worker logs false",
		"stale-manager start true",
	}, "\n")
	if got != want {
		t.Fatalf("unhealthy resume-plan = %q, want %q", got, want)
	}

	sorted := NewRootCmd()
	sortedOut, sortedErr := &bytes.Buffer{}, &bytes.Buffer{}
	sorted.SetOut(sortedOut)
	sorted.SetErr(sortedErr)
	sorted.SetArgs([]string{"runtime", "resume-plan", "--target", tmp, "--unhealthy", "--sort", "stale", "--format", "{{.Instance}} {{.RecommendedAction}} {{.Stale}}"})
	if err := sorted.Execute(); err != nil {
		t.Fatalf("runtime resume-plan unhealthy sort: %v\nstderr=%s", err, sortedErr.String())
	}
	sortedWant := strings.Join([]string{
		"stale-manager start true",
		"crashed-worker logs false",
	}, "\n")
	if got := strings.TrimSpace(sortedOut.String()); got != sortedWant {
		t.Fatalf("sorted unhealthy resume-plan = %q, want %q", got, sortedWant)
	}

	limited := NewRootCmd()
	limitedOut, limitedErr := &bytes.Buffer{}, &bytes.Buffer{}
	limited.SetOut(limitedOut)
	limited.SetErr(limitedErr)
	limited.SetArgs([]string{"runtime", "resume-plan", "--target", tmp, "--unhealthy", "--sort", "stale", "--limit", "1", "--format", "{{.Instance}} {{.RecommendedAction}} {{.Stale}}"})
	if err := limited.Execute(); err != nil {
		t.Fatalf("runtime resume-plan unhealthy limit: %v\nstderr=%s", err, limitedErr.String())
	}
	if got := strings.TrimSpace(limitedOut.String()); got != "stale-manager start true" {
		t.Fatalf("limited unhealthy resume-plan = %q", got)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"runtime", "resume-plan", "--target", tmp, "--unhealthy", "--sort", "stale", "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("runtime resume-plan unhealthy commands: %v\nstderr=%s", err, commandsErr.String())
	}
	commandsWant := strings.Join([]string{
		strings.Join(shellQuoteArgs([]string{"agent-team", "start", "--repo", tmp, "stale-manager"}), " "),
		strings.Join(shellQuoteArgs([]string{"agent-team", "logs", "--repo", tmp, "crashed-worker", "--follow"}), " "),
	}, "\n")
	if got := strings.TrimSpace(commandsOut.String()); got != commandsWant {
		t.Fatalf("command-only unhealthy resume-plan = %q, want %q", got, commandsWant)
	}

	summary := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(summaryOut)
	summary.SetErr(summaryErr)
	summary.SetArgs([]string{"runtime", "resume-plan", "--target", tmp, "--unhealthy", "--summary", "--json"})
	if err := summary.Execute(); err != nil {
		t.Fatalf("runtime resume-plan unhealthy summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var counts runtimeResumeSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &counts); err != nil {
		t.Fatalf("decode unhealthy summary: %v\nbody=%s", err, summaryOut.String())
	}
	if counts.Total != 2 || counts.Unhealthy != 2 || counts.Stale != 1 || counts.Actions["logs"] != 1 || counts.Actions["start"] != 1 {
		t.Fatalf("unhealthy summary = %+v", counts)
	}
}

func TestRuntimeResumePlanCodexJobJSON(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-42",
		Ticket:    "SQU-42",
		Target:    "worker",
		Instance:  "worker-squ-42",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:      "worker-squ-42",
		Agent:         "worker",
		Runtime:       string(runtimebin.KindCodex),
		RuntimeBinary: "codex",
		Workspace:     tmp,
		PID:           4321,
		SessionID:     "codex-session",
		StartedAt:     now,
		Status:        daemon.StatusExited,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "resume-plan", "--target", tmp, "--job", "SQU-42", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime resume-plan --job --json: %v\nstderr=%s", err, errOut.String())
	}
	var plans []runtimeResumePlan
	if err := json.Unmarshal(out.Bytes(), &plans); err != nil {
		t.Fatalf("decode resume plans: %v\nbody=%s", err, out.String())
	}
	if len(plans) != 1 {
		t.Fatalf("plans = %+v, want one", plans)
	}
	plan := plans[0]
	if plan.Instance != "worker-squ-42" || plan.Job != "squ-42" || plan.Runtime != "codex" || !plan.ManagedResume || !plan.CanManagedResume || !plan.DirectResume {
		t.Fatalf("plan = %+v", plan)
	}
	wantStartCommand := "agent-team start worker-squ-42"
	if plan.RecommendedAction != "start" || plan.RecommendedCommand != wantStartCommand || plan.JobLogsCommand != "agent-team job logs squ-42 --follow" || plan.JobLastMessageCommand != "agent-team job logs squ-42 --last-message" {
		t.Fatalf("commands = %+v", plan)
	}
	if !strings.Contains(plan.Detail, "managed start can resume") {
		t.Fatalf("detail = %q", plan.Detail)
	}

	jobCmd := NewRootCmd()
	jobOut, jobErr := &bytes.Buffer{}, &bytes.Buffer{}
	jobCmd.SetOut(jobOut)
	jobCmd.SetErr(jobErr)
	jobCmd.SetArgs([]string{"job", "resume-plan", "SQU-42", "--repo", tmp, "--json"})
	if err := jobCmd.Execute(); err != nil {
		t.Fatalf("job resume-plan --json: %v\nstderr=%s", err, jobErr.String())
	}
	var jobPlans []runtimeResumePlan
	if err := json.Unmarshal(jobOut.Bytes(), &jobPlans); err != nil {
		t.Fatalf("decode job resume plans: %v\nbody=%s", err, jobOut.String())
	}
	if len(jobPlans) != 1 || jobPlans[0].Instance != "worker-squ-42" || jobPlans[0].Job != "squ-42" || jobPlans[0].JobLastMessageCommand != "agent-team job logs squ-42 --last-message" {
		t.Fatalf("job plans = %+v", jobPlans)
	}

	jobCommands := NewRootCmd()
	jobCommandsOut, jobCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	jobCommands.SetOut(jobCommandsOut)
	jobCommands.SetErr(jobCommandsErr)
	jobCommands.SetArgs([]string{"job", "resume-plan", "SQU-42", "--repo", tmp, "--commands"})
	if err := jobCommands.Execute(); err != nil {
		t.Fatalf("job resume-plan --commands: %v\nstderr=%s", err, jobCommandsErr.String())
	}
	wantJobStartCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "start", "--repo", tmp, "worker-squ-42"}), " ")
	if got, want := strings.TrimSpace(jobCommandsOut.String()), wantJobStartCommand; got != want {
		t.Fatalf("job resume-plan commands = %q, want %q", got, want)
	}

	jobDirect := NewRootCmd()
	jobDirectOut, jobDirectErr := &bytes.Buffer{}, &bytes.Buffer{}
	jobDirect.SetOut(jobDirectOut)
	jobDirect.SetErr(jobDirectErr)
	jobDirect.SetArgs([]string{"job", "resume-plan", "SQU-42", "--repo", tmp, "--direct", "--format", "{{.Instance}} {{.DirectResume}} {{.RecommendedAction}}"})
	if err := jobDirect.Execute(); err != nil {
		t.Fatalf("job resume-plan --direct: %v\nstderr=%s", err, jobDirectErr.String())
	}
	if got, want := strings.TrimSpace(jobDirectOut.String()), "worker-squ-42 true start"; got != want {
		t.Fatalf("job direct resume-plan = %q, want %q", got, want)
	}

	jobManaged := NewRootCmd()
	jobManagedOut, jobManagedErr := &bytes.Buffer{}, &bytes.Buffer{}
	jobManaged.SetOut(jobManagedOut)
	jobManaged.SetErr(jobManagedErr)
	jobManaged.SetArgs([]string{"job", "resume-plan", "SQU-42", "--repo", tmp, "--managed", "--format", "{{.Instance}}"})
	if err := jobManaged.Execute(); err != nil {
		t.Fatalf("job resume-plan --managed: %v\nstderr=%s", err, jobManagedErr.String())
	}
	if got, want := strings.TrimSpace(jobManagedOut.String()), "worker-squ-42"; got != want {
		t.Fatalf("job managed resume-plan = %q, want %q", got, want)
	}
}

func TestRuntimeResumePlanJobStepFilter(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-43",
		Ticket:    "SQU-43",
		Target:    "manager",
		Instance:  "manager-squ-43",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-43-implement", StartedAt: now.Add(-time.Hour)},
			{ID: "review", Target: "manager", Status: job.StatusRunning, Instance: "manager-squ-43-review", StartedAt: now.Add(-30 * time.Minute)},
		},
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}
	for _, meta := range []*daemon.Metadata{
		{
			Instance:      "manager-squ-43",
			Agent:         "manager",
			Runtime:       string(runtimebin.KindClaude),
			RuntimeBinary: "claude",
			Workspace:     tmp,
			StartedAt:     now.Add(-2 * time.Hour),
			Status:        daemon.StatusCrashed,
		},
		{
			Instance:      "worker-squ-43-implement",
			Agent:         "worker",
			Runtime:       string(runtimebin.KindCodex),
			RuntimeBinary: "codex",
			Workspace:     tmp,
			SessionID:     "implement-session",
			StartedAt:     now.Add(-time.Hour),
			Status:        daemon.StatusExited,
		},
		{
			Instance:      "manager-squ-43-review",
			Agent:         "manager",
			Runtime:       string(runtimebin.KindClaude),
			RuntimeBinary: "claude",
			Workspace:     tmp,
			SessionID:     "review-session",
			StartedAt:     now.Add(-30 * time.Minute),
			Status:        daemon.StatusExited,
		},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"job", "resume-plan", "SQU-43", "--repo", tmp, "--step", "implement", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job resume-plan --step: %v\nstderr=%s", err, errOut.String())
	}
	var plans []runtimeResumePlan
	if err := json.Unmarshal(out.Bytes(), &plans); err != nil {
		t.Fatalf("decode job step resume plans: %v\nbody=%s", err, out.String())
	}
	if len(plans) != 1 {
		t.Fatalf("plans = %+v, want one implement plan", plans)
	}
	plan := plans[0]
	if plan.Instance != "worker-squ-43-implement" || plan.Job != "squ-43" || plan.Pipeline != "ticket_to_pr" || plan.StepID != "implement" || plan.JobLastMessageCommand != "agent-team job logs squ-43 --step implement --last-message" {
		t.Fatalf("plan = %+v", plan)
	}
	if !strings.Contains(plan.Detail, "managed start can resume") {
		t.Fatalf("step-aware detail = %q, want managed start detail", plan.Detail)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"runtime", "resume-plan", "--target", tmp, "--job", "SQU-43", "--step", "review"})
	if err := text.Execute(); err != nil {
		t.Fatalf("runtime resume-plan --job --step text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"pipeline:                 ticket_to_pr", "step:                     review", "manager-squ-43-review"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("resume-plan text missing %q:\n%s", want, textOut.String())
		}
	}

	jobCommands := NewRootCmd()
	jobCommandsOut, jobCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	jobCommands.SetOut(jobCommandsOut)
	jobCommands.SetErr(jobCommandsErr)
	jobCommands.SetArgs([]string{"job", "resume-plan", "SQU-43", "--repo", tmp, "--step", "review", "--commands"})
	if err := jobCommands.Execute(); err != nil {
		t.Fatalf("job resume-plan --commands --step: %v\nstderr=%s", err, jobCommandsErr.String())
	}
	jobCommandsWant := strings.Join(shellQuoteArgs([]string{"agent-team", "start", "--repo", tmp, "manager-squ-43-review"}), " ")
	if got := strings.TrimSpace(jobCommandsOut.String()); got != jobCommandsWant {
		t.Fatalf("job step resume-plan commands = %q, want %q", got, jobCommandsWant)
	}
}

func TestRuntimeResumePlanFormatAndFilters(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	for _, meta := range []*daemon.Metadata{
		{
			Instance:      "manager",
			Agent:         "manager",
			Runtime:       string(runtimebin.KindClaude),
			RuntimeBinary: "claude",
			Workspace:     tmp,
			SessionID:     "sid-manager",
			StartedAt:     now,
			Status:        daemon.StatusStopped,
		},
		{
			Instance:      "worker",
			Agent:         "worker",
			Runtime:       string(runtimebin.KindCodex),
			RuntimeBinary: "codex",
			Workspace:     tmp,
			SessionID:     "sid-worker",
			StartedAt:     now,
			Status:        daemon.StatusExited,
		},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata: %v", err)
		}
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "resume-plan", "--target", tmp, "--runtime", "codex", "--status", "exited", "--format", "{{.Instance}} {{.Runtime}} {{.RecommendedCommand}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime resume-plan --format: %v\nstderr=%s", err, errOut.String())
	}
	got := strings.TrimSpace(out.String())
	if got != "worker codex agent-team start worker" {
		t.Fatalf("formatted resume plan = %q", got)
	}
}

func TestRuntimeResumePlanActionFilter(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	for _, meta := range []*daemon.Metadata{
		{
			Instance:      "attach-claude",
			Agent:         "manager",
			Runtime:       string(runtimebin.KindClaude),
			RuntimeBinary: "claude",
			Workspace:     tmp,
			SessionID:     "sid-attach",
			StartedAt:     now,
			Status:        daemon.StatusRunning,
		},
		{
			Instance:      "logs-codex",
			Agent:         "worker",
			Runtime:       string(runtimebin.KindCodex),
			RuntimeBinary: "codex",
			Workspace:     tmp,
			StartedAt:     now,
			Status:        daemon.StatusCrashed,
		},
		{
			Instance:      "resume-codex",
			Agent:         "worker",
			Runtime:       string(runtimebin.KindCodex),
			RuntimeBinary: "codex",
			Workspace:     tmp,
			SessionID:     "sid-resume",
			StartedAt:     now,
			Status:        daemon.StatusExited,
		},
		{
			Instance:      "start-claude",
			Agent:         "manager",
			Runtime:       string(runtimebin.KindClaude),
			RuntimeBinary: "claude",
			Workspace:     tmp,
			SessionID:     "sid-start",
			StartedAt:     now,
			Status:        daemon.StatusStopped,
		},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata: %v", err)
		}
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "resume-plan", "--target", tmp, "--action", "resume,logs", "--format", "{{.Instance}} {{.RecommendedAction}} {{.RecommendedCommand}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime resume-plan --action: %v\nstderr=%s", err, errOut.String())
	}
	got := strings.TrimSpace(out.String())
	want := strings.Join([]string{
		"logs-codex logs agent-team logs logs-codex --follow",
	}, "\n")
	if got != want {
		t.Fatalf("runtime resume-plan --action = %q, want %q", got, want)
	}

	lastMessage := NewRootCmd()
	lastMessageOut, lastMessageErr := &bytes.Buffer{}, &bytes.Buffer{}
	lastMessage.SetOut(lastMessageOut)
	lastMessage.SetErr(lastMessageErr)
	lastMessage.SetArgs([]string{"runtime", "resume-plan", "--target", tmp, "--action", "logs", "--last-message", "--format", "{{.Instance}} {{.RecommendedAction}} {{.RecommendedCommand}}"})
	if err := lastMessage.Execute(); err != nil {
		t.Fatalf("runtime resume-plan --last-message: %v\nstderr=%s", err, lastMessageErr.String())
	}
	if got, want := strings.TrimSpace(lastMessageOut.String()), "logs-codex logs agent-team logs logs-codex --last-message"; got != want {
		t.Fatalf("runtime resume-plan --last-message = %q, want %q", got, want)
	}

	summary := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(summaryOut)
	summary.SetErr(summaryErr)
	summary.SetArgs([]string{"runtime", "resume-plan", "--target", tmp, "--summary", "--json"})
	if err := summary.Execute(); err != nil {
		t.Fatalf("runtime resume-plan --summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var counts runtimeResumeSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &counts); err != nil {
		t.Fatalf("decode resume-plan summary: %v\nbody=%s", err, summaryOut.String())
	}
	if counts.Total != 4 || counts.Actions["attach"] != 1 || counts.Actions["logs"] != 1 || counts.Actions["resume"] != 0 || counts.Actions["start"] != 2 || counts.Runtimes["claude"] != 2 || counts.Runtimes["codex"] != 2 || counts.Statuses["running"] != 1 || counts.Statuses["crashed"] != 1 || counts.Statuses["exited"] != 1 || counts.Statuses["stopped"] != 1 || counts.ManagedResume != 4 || counts.CanManagedResume != 3 || counts.DirectResume != 3 || counts.Unhealthy != 1 {
		t.Fatalf("resume-plan summary = %+v", counts)
	}

	managed := NewRootCmd()
	managedOut, managedErr := &bytes.Buffer{}, &bytes.Buffer{}
	managed.SetOut(managedOut)
	managed.SetErr(managedErr)
	managed.SetArgs([]string{"runtime", "resume-plan", "--target", tmp, "--managed", "--format", "{{.Instance}} {{.ManagedResume}} {{.CanManagedResume}} {{.DirectResume}}"})
	if err := managed.Execute(); err != nil {
		t.Fatalf("runtime resume-plan --managed: %v\nstderr=%s", err, managedErr.String())
	}
	if got, want := strings.TrimSpace(managedOut.String()), strings.Join([]string{
		"attach-claude true true true",
		"logs-codex true false false",
		"resume-codex true true true",
		"start-claude true true true",
	}, "\n"); got != want {
		t.Fatalf("managed resume-plan = %q, want %q", got, want)
	}

	canManaged := NewRootCmd()
	canManagedOut, canManagedErr := &bytes.Buffer{}, &bytes.Buffer{}
	canManaged.SetOut(canManagedOut)
	canManaged.SetErr(canManagedErr)
	canManaged.SetArgs([]string{"runtime", "resume-plan", "--target", tmp, "--can-managed", "--summary", "--json"})
	if err := canManaged.Execute(); err != nil {
		t.Fatalf("runtime resume-plan --can-managed summary: %v\nstderr=%s", err, canManagedErr.String())
	}
	var canManagedCounts runtimeResumeSummary
	if err := json.Unmarshal(canManagedOut.Bytes(), &canManagedCounts); err != nil {
		t.Fatalf("decode can-managed resume-plan summary: %v\nbody=%s", err, canManagedOut.String())
	}
	if canManagedCounts.Total != 3 || canManagedCounts.ManagedResume != 3 || canManagedCounts.CanManagedResume != 3 || canManagedCounts.DirectResume != 3 || canManagedCounts.Actions["attach"] != 1 || canManagedCounts.Actions["start"] != 2 {
		t.Fatalf("can-managed resume-plan summary = %+v", canManagedCounts)
	}

	direct := NewRootCmd()
	directOut, directErr := &bytes.Buffer{}, &bytes.Buffer{}
	direct.SetOut(directOut)
	direct.SetErr(directErr)
	direct.SetArgs([]string{"runtime", "resume-plan", "--target", tmp, "--runtime", "codex", "--direct", "--action", "start", "--format", "{{.Instance}} {{.DirectResume}} {{.RecommendedAction}}"})
	if err := direct.Execute(); err != nil {
		t.Fatalf("runtime resume-plan --direct --action resume: %v\nstderr=%s", err, directErr.String())
	}
	if got, want := strings.TrimSpace(directOut.String()), "resume-codex true start"; got != want {
		t.Fatalf("direct resume-plan = %q, want %q", got, want)
	}
}

func TestRuntimeResumePlanRejectsJSONFormat(t *testing.T) {
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "resume-plan", "--target", t.TempDir(), "--json", "--format", "{{.Instance}}"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("runtime resume-plan --json --format succeeded")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Fatalf("error = %v, want exit 2", err)
	}
	if !strings.Contains(errOut.String(), "--format cannot be combined with --json") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestRuntimeResumePlanRejectsCommandsWithStructuredRenderers(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "json",
			args: []string{"runtime", "resume-plan", "--target", t.TempDir(), "--commands", "--json"},
			want: wantCommandsModeConflict("--json"),
		},
		{
			name: "format",
			args: []string{"runtime", "resume-plan", "--target", t.TempDir(), "--commands", "--format", "{{.RecommendedCommand}}"},
			want: wantCommandsModeConflict("--format"),
		},
		{
			name: "summary",
			args: []string{"runtime", "resume-plan", "--target", t.TempDir(), "--commands", "--summary"},
			want: "--summary cannot be combined with --commands",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(errOut)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("runtime resume-plan accepted %s conflict: stdout=%s", tc.name, out.String())
			}
			var ec ExitCode
			if !errors.As(err, &ec) || int(ec) != 2 {
				t.Fatalf("error = %v, want exit 2", err)
			}
			if !strings.Contains(errOut.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", errOut.String(), tc.want)
			}
		})
	}
}

func TestRuntimeResumePlanRejectsSummaryFormat(t *testing.T) {
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "resume-plan", "--target", t.TempDir(), "--summary", "--format", "{{.Total}}"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("runtime resume-plan --summary --format succeeded")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Fatalf("error = %v, want exit 2", err)
	}
	if !strings.Contains(errOut.String(), "--summary cannot be combined with --format") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestRuntimeResumePlanRejectsInvalidAction(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "resume-plan", "--target", tmp, "--action", "restart"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("runtime resume-plan --action restart succeeded")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("error = %v, want exit 1", err)
	}
	if !strings.Contains(errOut.String(), "--action accepts start, attach, resume, logs, or all") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestRuntimeResumePlanRejectsInvalidSort(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "resume-plan", "--target", tmp, "--sort", "age"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("runtime resume-plan --sort age succeeded")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Fatalf("error = %v, want exit 2", err)
	}
	if !strings.Contains(errOut.String(), "--sort must be instance") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestRuntimeResumePlanRejectsInvalidLimit(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "resume-plan", "--target", tmp, "--limit", "-1"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("runtime resume-plan --limit -1 succeeded")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Fatalf("error = %v, want exit 2", err)
	}
	if !strings.Contains(errOut.String(), "--limit must be >= 0") {
		t.Fatalf("stderr = %q", errOut.String())
	}

	summary := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(summaryOut)
	summary.SetErr(summaryErr)
	summary.SetArgs([]string{"runtime", "resume-plan", "--target", tmp, "--summary", "--limit", "1"})
	err = summary.Execute()
	if err == nil {
		t.Fatal("runtime resume-plan --summary --limit 1 succeeded")
	}
	if !strings.Contains(summaryErr.String(), "--limit cannot be combined with --summary") {
		t.Fatalf("summary limit stderr = %q", summaryErr.String())
	}

	jobCmd := NewRootCmd()
	jobOut, jobErr := &bytes.Buffer{}, &bytes.Buffer{}
	jobCmd.SetOut(jobOut)
	jobCmd.SetErr(jobErr)
	jobCmd.SetArgs([]string{"job", "resume-plan", "SQU-1", "--repo", tmp, "--limit", "-1"})
	err = jobCmd.Execute()
	if err == nil {
		t.Fatal("job resume-plan --limit -1 succeeded")
	}
	if !strings.Contains(jobErr.String(), "--limit must be >= 0") {
		t.Fatalf("job limit stderr = %q", jobErr.String())
	}
}

func TestRuntimeResumePlanFallbacksRequireCommands(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	for _, args := range [][]string{
		{"runtime", "resume-plan", "--target", tmp, "--fallbacks"},
		{"job", "resume-plan", "SQU-1", "--repo", tmp, "--fallbacks"},
		{"pipeline", "resume-plan", "--repo", tmp, "--fallbacks"},
		{"team", "resume-plan", "delivery", "--repo", tmp, "--fallbacks"},
	} {
		cmd := NewRootCmd()
		out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(errOut)
		cmd.SetArgs(args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v unexpectedly succeeded", args)
		}
		var ec ExitCode
		if !errors.As(err, &ec) || int(ec) != 2 {
			t.Fatalf("%v error = %v, want exit 2", args, err)
		}
		if !strings.Contains(errOut.String(), "--fallbacks requires --commands") {
			t.Fatalf("%v stderr = %q", args, errOut.String())
		}
		if out.Len() != 0 {
			t.Fatalf("%v wrote stdout: %s", args, out.String())
		}
	}
}

func TestRuntimeCommand_MissingBinaryExitsOne(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "missing-runtime")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "missing-runtime" {
			t.Fatalf("look path bin = %q, want missing-runtime", bin)
		}
		return "", exec.ErrNotFound
	})

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "--target", t.TempDir()})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("runtime succeeded with missing binary, want exit 1")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("error = %v, want exit 1", err)
	}
	if !strings.Contains(out.String(), "available:        no") {
		t.Fatalf("missing binary output = %q, want available no", out.String())
	}
}

func TestRuntimeCommand_InvalidRuntimeExitsTwo(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "bad")

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "--target", t.TempDir()})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("runtime succeeded with invalid env, want exit 2")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Fatalf("error = %v, want exit 2", err)
	}
	if !strings.Contains(errOut.String(), runtimebin.EnvRuntime+" must be") {
		t.Fatalf("stderr = %q, want invalid runtime error", errOut.String())
	}
}

func TestRuntimeCommand_InvalidRuntimeFlagExitsTwo(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "--target", t.TempDir(), "--runtime", "bad-runtime"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("runtime succeeded with invalid flag, want exit 2")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Fatalf("error = %v, want exit 2", err)
	}
	if !strings.Contains(errOut.String(), `--runtime must be "claude" or "codex"`) {
		t.Fatalf("stderr = %q, want invalid runtime flag error", errOut.String())
	}
}
