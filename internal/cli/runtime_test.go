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

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/runtimebin"
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
	if !info.DirectRun || !info.DaemonDispatch || !info.DirectResume || info.ManagedResume || info.Resume || info.Subagents {
		t.Fatalf("codex capabilities = %+v, want direct plus daemon one-shot", info)
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
	if got := strings.TrimSpace(out.String()); got != "codex codex true true false false" {
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
	if !info.DirectRun || !info.DaemonDispatch || !info.DirectResume || info.ManagedResume || info.Resume || info.Subagents {
		t.Fatalf("codex capabilities = %+v, want direct plus daemon one-shot", info)
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
	if row := byRuntime["codex"]; row.Selected || row.Available || row.Binary != "codex" {
		t.Fatalf("codex row = %+v, want unselected unavailable default", row)
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
	if plan.Instance != "worker-squ-42" || plan.Job != "squ-42" || plan.Runtime != "codex" || plan.ManagedResume || plan.CanManagedResume || !plan.DirectResume {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.RecommendedAction != "resume" || plan.RecommendedCommand != "codex resume codex-session" || plan.JobLogsCommand != "agent-team job logs squ-42 --follow" || plan.JobLastMessageCommand != "agent-team job logs squ-42 --last-message" {
		t.Fatalf("commands = %+v", plan)
	}
	if !strings.Contains(plan.Detail, `runtime "codex" does not support managed resume`) {
		t.Fatalf("detail = %q", plan.Detail)
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
	if got != "worker codex codex resume sid-worker" {
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
		"resume-codex resume codex resume sid-resume",
	}, "\n")
	if got != want {
		t.Fatalf("runtime resume-plan --action = %q, want %q", got, want)
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
	if counts.Total != 4 || counts.Actions["attach"] != 1 || counts.Actions["logs"] != 1 || counts.Actions["resume"] != 1 || counts.Actions["start"] != 1 || counts.Runtimes["claude"] != 2 || counts.Runtimes["codex"] != 2 || counts.Statuses["running"] != 1 || counts.Statuses["crashed"] != 1 || counts.Statuses["exited"] != 1 || counts.Statuses["stopped"] != 1 || counts.ManagedResume != 2 || counts.CanManagedResume != 2 || counts.DirectResume != 3 {
		t.Fatalf("resume-plan summary = %+v", counts)
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
