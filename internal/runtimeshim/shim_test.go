package runtimeshim

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentTeamShimAllowsReadOnlyVerbs(t *testing.T) {
	// Read-only verbs are always allowed, even under a narrow enforced allowlist.
	shim, _, calls := installEnforcingShim(t, []string{"job.gate.*:own"})

	runShim(t, shim, "--repo", "/tmp/repo", "job", "show", "squ-1")
	runShim(t, shim, "inbox", "check")

	got := strings.TrimSpace(readFile(t, calls))
	want := "--repo /tmp/repo job show squ-1\ninbox check"
	if got != want {
		t.Fatalf("real agent-team args = %q", got)
	}
}

func TestAgentTeamShimAllowsDeclaredAuthorityVerb(t *testing.T) {
	shim, _, calls := installEnforcingShim(t, []string{"job.gate.*:own"})

	runShim(t, shim, "job", "gate", "set", "squ-1", "tests", "--status", "pass")

	got := strings.TrimSpace(readFile(t, calls))
	if got != "job gate set squ-1 tests --status pass" {
		t.Fatalf("real agent-team args = %q", got)
	}
}

func TestAgentTeamShimAllowsKnownLeafVerbsWithPositionals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		allow []string
	}{
		{name: "wildcard", allow: []string{"*"}},
		{name: "explicit", allow: []string{"run", "send"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shim, _, calls := installEnforcingShim(t, tc.allow)

			runShim(t, shim, "run", "worker")
			runShim(t, shim, "send", "manager", "hello")

			got := strings.TrimSpace(readFile(t, calls))
			want := "run worker\nsend manager hello"
			if got != want {
				t.Fatalf("real agent-team args = %q, want %q", got, want)
			}
		})
	}
}

func TestAgentTeamShimPassesThroughWhenNoAuthorityDeclared(t *testing.T) {
	// No declared authority => pass-through shim; every verb reaches the real binary.
	shim, _, calls := installTestAgentTeamShim(t)

	runShim(t, shim, "job", "gate", "set", "squ-1", "review", "--status", "done")

	if got := strings.TrimSpace(readFile(t, calls)); got != "job gate set squ-1 review --status done" {
		t.Fatalf("real agent-team args = %q", got)
	}
}

func TestAgentTeamShimDeniesKnownVerbOutsideAllowlist(t *testing.T) {
	shim, _, calls := installEnforcingShim(t, []string{"job.gate.*:own"})

	stderr, code := runShimExpectExit(t, shim, "job", "merge", "squ-1")
	if code != 3 {
		t.Fatalf("exit code = %d, want 3; stderr=%s", code, stderr)
	}
	if got := strings.TrimSpace(stderr); got != "agent-team shim: denied verb job.merge" {
		t.Fatalf("stderr = %q", got)
	}
	if _, err := os.Stat(calls); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("real agent-team should not be invoked, stat err=%v", err)
	}
}

// TestAgentTeamShimBakedAllowlistIgnoresHostileEnv is the core tamper-resistance
// guarantee: the enforced allowlist is baked into the generated script and never
// read from the environment. An agent cannot widen its own authority by setting
// AGENT_TEAM_AUTHORITY_ALLOWLIST=*, grant itself the exact verb, or reach a
// pass-through branch by unsetting the variable.
func TestAgentTeamShimBakedAllowlistIgnoresHostileEnv(t *testing.T) {
	shim, _, calls := installEnforcingShim(t, []string{"job.gate.*:own"})

	for _, hostile := range [][]string{
		{EnvAuthorityAllowlist + "=*"},         // try to self-widen
		{EnvAuthorityAllowlist + "=job.merge"}, // try to grant the exact verb
		{},                                     // unset entirely (env -u)
	} {
		env := append([]string{"PATH=" + os.Getenv("PATH")}, hostile...)
		cmd := exec.Command(shim, "job", "merge", "squ-1")
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("hostile env %v bypassed enforcement: out=%s", hostile, out)
		}
		if !strings.Contains(string(out), "denied verb job.merge") {
			t.Fatalf("hostile env %v: out=%q, want denial", hostile, out)
		}
	}
	if _, err := os.Stat(calls); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("real agent-team should never be invoked, stat err=%v", err)
	}
}

func TestAgentTeamShimDeniesUnknownVerbEvenWithWildcard(t *testing.T) {
	shim, _, calls := installEnforcingShim(t, []string{"*"})

	stderr, code := runShimExpectExit(t, shim, "future-dangerous-verb")
	if code != 3 {
		t.Fatalf("exit code = %d, want 3; stderr=%s", code, stderr)
	}
	if got := strings.TrimSpace(stderr); got != "agent-team shim: denied unknown verb future-dangerous-verb" {
		t.Fatalf("stderr = %q", got)
	}
	if _, err := os.Stat(calls); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("real agent-team should not be invoked, stat err=%v", err)
	}
}

func TestAgentTeamShimDeniesUnknownTopLevelVerbWithPositionals(t *testing.T) {
	shim, _, calls := installEnforcingShim(t, []string{"*"})

	stderr, code := runShimExpectExit(t, shim, "future-dangerous-verb", "worker")
	if code != 3 {
		t.Fatalf("exit code = %d, want 3; stderr=%s", code, stderr)
	}
	if got := strings.TrimSpace(stderr); got != "agent-team shim: denied unknown verb future-dangerous-verb" {
		t.Fatalf("stderr = %q", got)
	}
	if _, err := os.Stat(calls); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("real agent-team should not be invoked, stat err=%v", err)
	}
}

func TestAgentTeamShimDeniesUnknownSubverbUnderKnownGroup(t *testing.T) {
	shim, _, calls := installEnforcingShim(t, []string{"*"})

	stderr, code := runShimExpectExit(t, shim, "job", "future-dangerous-verb")
	if code != 3 {
		t.Fatalf("exit code = %d, want 3; stderr=%s", code, stderr)
	}
	if got := strings.TrimSpace(stderr); got != "agent-team shim: denied unknown verb job.future-dangerous-verb" {
		t.Fatalf("stderr = %q", got)
	}
	if _, err := os.Stat(calls); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("real agent-team should not be invoked, stat err=%v", err)
	}
}

func TestAgentTeamShimDoesNotAffectDirectBinaryInvocation(t *testing.T) {
	shim, real, calls := installEnforcingShim(t, []string{"job.gate.*:own"})

	stderr, code := runShimExpectExit(t, shim, "job", "merge", "squ-1")
	if code != 3 || !strings.Contains(stderr, "denied verb job.merge") {
		t.Fatalf("shim exit = %d stderr=%q, want denial", code, stderr)
	}

	cmd := exec.Command(real, "job", "merge", "squ-1")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("direct real agent-team: %v\n%s", err, string(out))
	}
	got := strings.TrimSpace(readFile(t, calls))
	if got != "job merge squ-1" {
		t.Fatalf("direct real args = %q", got)
	}
}

func TestWithAuthorityAllowlistAddsAndPreservesEnv(t *testing.T) {
	t.Setenv(EnvAuthorityAllowlist, "")

	got := WithAuthorityAllowlist([]string{"AGENT_TEAM_INSTANCE=worker"}, []string{"job.gate.*:own", "", "job.gate.*:own", "feedback.submit"})
	if !containsString(got, EnvAuthorityAllowlist+"=feedback.submit,job.gate.*:own") {
		t.Fatalf("env = %#v, want sorted deduped allowlist", got)
	}

	got = WithAuthorityAllowlist([]string{EnvAuthorityAllowlist + "=existing"}, []string{"job.merge"})
	if len(got) != 1 || got[0] != EnvAuthorityAllowlist+"=existing" {
		t.Fatalf("preserved env = %#v", got)
	}
}

// installTestAgentTeamShim installs a pass-through shim (no authority declared).
func installTestAgentTeamShim(t *testing.T) (string, string, string) {
	t.Helper()
	return installShim(t, Options{})
}

// installEnforcingShim bakes closed-world enforcement + the allowlist into the
// generated shim, mirroring an instance whose topology declares authority.
func installEnforcingShim(t *testing.T, allow []string) (string, string, string) {
	t.Helper()
	return installShim(t, Options{EnforceAuthority: true, AuthorityAllowlist: allow})
}

func installShim(t *testing.T, opts Options) (string, string, string) {
	t.Helper()
	tmp := t.TempDir()
	calls := filepath.Join(tmp, "calls.txt")
	real := filepath.Join(tmp, "real-agent-team")
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(calls) + "\n"
	if err := os.WriteFile(real, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	opts.RealAgentTeam = real
	bin, err := InstallWithOptions(filepath.Join(tmp, "runtime"), nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(bin, "agent-team"), real, calls
}

func runShim(t *testing.T, shim string, args ...string) {
	t.Helper()
	if stderr, code := runShimExpectExit(t, shim, args...); code != 0 {
		t.Fatalf("shim exit = %d stderr=%q", code, stderr)
	}
}

func runShimExpectExit(t *testing.T, shim string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(shim, args...)
	// Clean env with NO allowlist var: enforcement must come from the baked
	// script, never the environment.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return string(out), exitErr.ExitCode()
	}
	t.Fatalf("run shim: %v\n%s", err, string(out))
	return "", -1
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
