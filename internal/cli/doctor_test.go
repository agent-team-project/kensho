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
	"github.com/jamesaud/agent-team/internal/runtimebin"
)

func TestDoctor_FailsOnEmptyLinearKeys(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	// Wipe the resolved Linear keys to simulate a freshly-init'd repo where
	// the user hasn't yet supplied real values.
	cfgPath := filepath.Join(tmp, ".agent_team", "config.toml")
	if err := os.WriteFile(cfgPath, []byte(`[team]
pm_tool = "linear"

[linear]
team_id = ""
ticket_prefix = ""
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error: empty Linear team_id/ticket_prefix")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Errorf("expected exit 1, got %v", err)
	}
	if !strings.Contains(errOut.String(), "[linear].team_id missing/empty") {
		t.Errorf("missing team_id complaint: %s", errOut.String())
	}
}

func TestDoctor_PassesWithFilledLinearKeys(t *testing.T) {
	tmp := t.TempDir()
	// initInto supplies linear.team_id and linear.ticket_prefix via --set, so
	// doctor should be happy out of the box.
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("doctor failed unexpectedly: %v\nstderr: %s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "agent-team doctor: OK") {
		t.Errorf("expected OK output, got: %s", out.String())
	}
}

func TestDoctor_WarnsWhenAgentTeamdMissing(t *testing.T) {
	oldFind := findAgentTeamd
	findAgentTeamd = func() (string, error) {
		return "", errors.New("missing")
	}
	defer func() { findAgentTeamd = oldFind }()

	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("missing agent-teamd should warn, not fail: %v\nstderr: %s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "agent-team doctor: OK") {
		t.Fatalf("expected OK output, got: %s", out.String())
	}
	if !strings.Contains(errOut.String(), "agent-teamd binary not found") {
		t.Fatalf("expected agent-teamd warning, got: %s", errOut.String())
	}
}

func TestDoctorStrictDaemonFailsWhenAgentTeamdMissing(t *testing.T) {
	oldFind := findAgentTeamd
	findAgentTeamd = func() (string, error) {
		return "", errors.New("missing")
	}
	defer func() { findAgentTeamd = oldFind }()

	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--strict-daemon", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected strict daemon check to fail")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("expected exit 1, got %v", err)
	}
	if !strings.Contains(errOut.String(), "agent-teamd binary not found") {
		t.Fatalf("expected agent-teamd problem, got: %s", errOut.String())
	}
	if strings.Contains(out.String(), "agent-team doctor: OK") {
		t.Fatalf("strict daemon failure should not print OK: %s", out.String())
	}
}

func TestDoctor_WarnsWhenRuntimeBinaryMissing(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "missing-runtime")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "missing-runtime" {
			t.Fatalf("look path bin = %q, want missing-runtime", bin)
		}
		return "", exec.ErrNotFound
	})

	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("missing runtime should warn, not fail: %v\nstderr: %s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "agent-team doctor: OK") {
		t.Fatalf("expected OK output, got: %s", out.String())
	}
	if !strings.Contains(errOut.String(), "runtime binary \"missing-runtime\"") {
		t.Fatalf("expected runtime warning, got: %s", errOut.String())
	}
}

func TestDoctorJSONReportsWarnings(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "missing-runtime")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		return "", exec.ErrNotFound
	})

	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--target", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("doctor --json warning should not fail: %v\nstderr: %s", err, errOut.String())
	}
	var result doctorResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode doctor json: %v\nbody=%s", err, out.String())
	}
	if !result.OK || len(result.Problems) != 0 || len(result.Warnings) == 0 {
		t.Fatalf("doctor result = %+v, want ok with warnings", result)
	}
	if errOut.Len() != 0 {
		t.Fatalf("doctor --json should not write warnings to stderr: %s", errOut.String())
	}
}

func TestDoctorFormatReportsWarnings(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "missing-runtime")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		return "", exec.ErrNotFound
	})

	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--target", tmp, "--format", "{{.OK}} {{len .Problems}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("doctor --format warning should not fail: %v\nstderr: %s", err, errOut.String())
	}
	if got, want := out.String(), "true 0\n"; got != want {
		t.Fatalf("doctor --format warning output = %q, want %q", got, want)
	}
	if errOut.Len() != 0 {
		t.Fatalf("doctor --format should not write warnings to stderr: %s", errOut.String())
	}
}

func TestDoctorStrictRuntimeFailsWhenRuntimeBinaryMissing(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "missing-runtime")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		return "", exec.ErrNotFound
	})

	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--strict-runtime", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected strict runtime check to fail")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("expected exit 1, got %v", err)
	}
	if !strings.Contains(errOut.String(), "runtime binary \"missing-runtime\"") {
		t.Fatalf("expected runtime problem, got: %s", errOut.String())
	}
	if strings.Contains(out.String(), "agent-team doctor: OK") {
		t.Fatalf("strict runtime failure should not print OK: %s", out.String())
	}
}

func TestDoctorFailsOnInvalidRuntimeEnv(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "bad")

	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected invalid runtime to fail")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("expected exit 1, got %v", err)
	}
	if !strings.Contains(errOut.String(), runtimebin.EnvRuntime+" must be") {
		t.Fatalf("expected invalid runtime problem, got: %s", errOut.String())
	}
}

func TestDoctorJSONReportsProblems(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "bad")

	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--target", tmp, "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected doctor --json with invalid runtime to fail")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("expected exit 1, got %v", err)
	}
	var result doctorResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode doctor json: %v\nbody=%s stderr=%s", err, out.String(), errOut.String())
	}
	if result.OK || len(result.Problems) == 0 {
		t.Fatalf("doctor result = %+v, want problems", result)
	}
	if errOut.Len() != 0 {
		t.Fatalf("doctor --json should not write problems to stderr: %s", errOut.String())
	}
}

func TestDoctorFormatReportsProblems(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "bad")

	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--target", tmp, "--format", "{{.OK}} {{len .Problems}}"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected doctor --format with invalid runtime to fail")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("expected exit 1, got %v", err)
	}
	if got, want := out.String(), "false 1\n"; got != want {
		t.Fatalf("doctor --format problem output = %q, want %q", got, want)
	}
	if errOut.Len() != 0 {
		t.Fatalf("doctor --format should not write problems to stderr: %s", errOut.String())
	}
}

func TestDoctorFormatValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"doctor", "--format", "{{.OK}}", "--json"}, "--format cannot be combined"},
		{[]string{"doctor", "--format", "{{"}, "invalid --format template"},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(errOut)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		var ec ExitCode
		if !errors.As(err, &ec) || int(ec) != 2 {
			t.Fatalf("%v: err = %v, want exit 2", tc.args, err)
		}
		if !strings.Contains(errOut.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, errOut.String(), tc.want)
		}
		if out.Len() != 0 {
			t.Fatalf("%v: validation wrote stdout: %q", tc.args, out.String())
		}
	}
}

func TestDoctorIncludesIntakeLedgerProblems(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.MkdirAll(filepath.Join(teamDir, "daemon"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(intakeDeliveryLogPath(teamDir), []byte("{\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--target", tmp, "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected doctor to fail on corrupt intake ledger")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("expected exit 1, got %v", err)
	}
	var result doctorResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode doctor json: %v\nbody=%s stderr=%s", err, out.String(), errOut.String())
	}
	if result.OK || !containsDoctorMessage(result.Problems, "intake ledger:") || !containsDoctorMessage(result.Problems, "not valid JSON") {
		t.Fatalf("doctor result = %+v", result)
	}
	if errOut.Len() != 0 {
		t.Fatalf("doctor --json should not write intake problems to stderr: %s", errOut.String())
	}
}

func TestDoctorIncludesQueueProblems(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	queueDir := filepath.Join(daemon.QueueRoot(daemon.DaemonRoot(teamDir)), daemon.QueueStatePending)
	if err := os.MkdirAll(queueDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(queueDir, "bad.json"), []byte("{\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--target", tmp, "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected doctor to fail on corrupt queue file")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("expected exit 1, got %v", err)
	}
	var result doctorResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode doctor json: %v\nbody=%s stderr=%s", err, out.String(), errOut.String())
	}
	if result.OK || !containsDoctorMessage(result.Problems, "queue:") || !containsDoctorMessage(result.Problems, "not valid JSON") {
		t.Fatalf("doctor result = %+v", result)
	}
	if errOut.Len() != 0 {
		t.Fatalf("doctor --json should not write queue problems to stderr: %s", errOut.String())
	}
}

func TestDoctorWarnsOnQueueQuarantine(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	writeQuarantinedQueueItem(t, teamDir, "20260619T000000.000000000Z", daemon.QueueStatePending, &daemon.QueueItem{
		ID:         "q-doctor-quarantined",
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-110",
		Payload:    map[string]any{"target": "worker", "ticket": "SQU-110"},
		QueuedAt:   now.Add(-time.Minute),
		UpdatedAt:  now,
	})

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--target", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("doctor queue quarantine warning should not fail: %v\nstderr=%s", err, errOut.String())
	}
	var result doctorResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode doctor json: %v\nbody=%s stderr=%s", err, out.String(), errOut.String())
	}
	if !result.OK || !containsDoctorMessage(result.Warnings, "queue quarantine: 1 file") || !containsDoctorMessage(result.Warnings, "agent-team queue quarantine ls") {
		t.Fatalf("doctor result = %+v", result)
	}
	if errOut.Len() != 0 {
		t.Fatalf("doctor --json should not write quarantine warnings to stderr: %s", errOut.String())
	}
}

func TestDoctorFailsOnPipelineWorkflowProblem(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	instancesPath := filepath.Join(tmp, ".agent_team", "instances.toml")
	if err := os.WriteFile(instancesPath, []byte(`
[instances.worker]
agent = "worker"

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
after = ["review"]

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "worker"
after = ["implement"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--target", tmp, "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected doctor to fail on pipeline dependency cycle")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("expected exit 1, got %v", err)
	}
	var result doctorResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode doctor json: %v\nbody=%s stderr=%s", err, out.String(), errOut.String())
	}
	if result.OK || !containsDoctorMessage(result.Problems, "dependency cycle") {
		t.Fatalf("doctor result = %+v", result)
	}
	if errOut.Len() != 0 {
		t.Fatalf("doctor --json should not write pipeline problems to stderr: %s", errOut.String())
	}
}

func TestDoctorFailsOnTeamTopologyProblem(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	instancesPath := filepath.Join(tmp, ".agent_team", "instances.toml")
	if err := os.WriteFile(instancesPath, []byte(`
[instances.worker]
agent = "worker"

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[instances.other]
agent = "other"

[[instances.other.triggers]]
event = "agent.dispatch"
match.target = "other"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "other"

[teams.delivery]
instances = ["worker"]
pipelines = ["ticket_to_pr"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--target", tmp, "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected doctor to fail on team topology leak")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("expected exit 1, got %v", err)
	}
	var result doctorResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode doctor json: %v\nbody=%s stderr=%s", err, out.String(), errOut.String())
	}
	if result.OK || !containsDoctorMessage(result.Problems, "team topology") || !containsDoctorMessage(result.Problems, `targets "other"`) {
		t.Fatalf("doctor result = %+v", result)
	}
	if errOut.Len() != 0 {
		t.Fatalf("doctor --json should not write team topology problems to stderr: %s", errOut.String())
	}
}

func TestDoctor_NoTeamDir(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when .agent_team/ missing")
	}
	if !strings.Contains(errOut.String(), "not found — run `agent-team init` first") {
		t.Errorf("missing init hint: %s", errOut.String())
	}
}

func TestDoctor_BadTOML(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cfgPath := filepath.Join(tmp, ".agent_team", "config.toml")
	if err := os.WriteFile(cfgPath, []byte("not = valid = toml ===="), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error on invalid TOML")
	}
	if !strings.Contains(errOut.String(), "is not valid TOML") {
		t.Errorf("missing toml-error message: %s", errOut.String())
	}
}

func TestDoctor_WarnsWhenTemplateLockMissing(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	if err := os.Remove(filepath.Join(tmp, ".agent_team", ".template.lock")); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("missing lock should warn, not fail: %v\nstderr: %s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "agent-team doctor: OK") {
		t.Errorf("expected OK output, got: %s", out.String())
	}
	if !strings.Contains(errOut.String(), ".template.lock missing") {
		t.Errorf("expected missing lock warning, got: %s", errOut.String())
	}
}

func TestDoctor_FailsOnInvalidTemplateLock(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	lockPath := filepath.Join(tmp, ".agent_team", ".template.lock")
	if err := os.WriteFile(lockPath, []byte(`[template]
ref = "bundled"
content_hash = "not-sha256"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected invalid lock to fail")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Errorf("expected exit 1, got %v", err)
	}
	if !strings.Contains(errOut.String(), "not valid template provenance") {
		t.Errorf("expected lock validation error, got: %s", errOut.String())
	}
}

func TestDoctorStrictTemplateDetectsLockDrift(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	lockPath := filepath.Join(tmp, ".agent_team", ".template.lock")
	body, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(body), "\n")
	replaced := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "content_hash = ") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[i] = indent + `content_hash = "sha256:` + strings.Repeat("0", 64) + `"`
			replaced = true
			break
		}
	}
	if !replaced {
		t.Fatalf("lock missing content_hash:\n%s", body)
	}
	if err := os.WriteFile(lockPath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	nonStrict := NewRootCmd()
	nonStrict.SetOut(&bytes.Buffer{})
	nonStrict.SetErr(&bytes.Buffer{})
	nonStrict.SetArgs([]string{"doctor", "--target", tmp})
	if err := nonStrict.Execute(); err != nil {
		t.Fatalf("non-strict doctor should not fail on drift: %v", err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"doctor", "--strict-template", "--target", tmp})
	err = cmd.Execute()
	if err == nil {
		t.Fatal("expected strict template drift to fail")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Errorf("expected exit 1, got %v", err)
	}
	for _, want := range []string{"template lock drift", "agent-team upgrade --check --strict"} {
		if !strings.Contains(errOut.String(), want) {
			t.Fatalf("strict template stderr missing %q:\n%s", want, errOut.String())
		}
	}
	if strings.Contains(out.String(), "agent-team doctor: OK") {
		t.Fatalf("strict template drift should not print OK: %s", out.String())
	}
}

func containsDoctorMessage(messages []string, needle string) bool {
	for _, message := range messages {
		if strings.Contains(message, needle) {
			return true
		}
	}
	return false
}
