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
	shim, _, calls := installTestAgentTeamShim(t)

	runShim(t, shim, "", "--repo", "/tmp/repo", "job", "show", "squ-1")
	runShim(t, shim, "", "inbox", "check")

	got := strings.TrimSpace(readFile(t, calls))
	want := "--repo /tmp/repo job show squ-1\ninbox check"
	if got != want {
		t.Fatalf("real agent-team args = %q", got)
	}
}

func TestAgentTeamShimAllowsDeclaredAuthorityVerb(t *testing.T) {
	shim, _, calls := installTestAgentTeamShim(t)

	runShim(t, shim, "job.gate.*:own", "job", "gate", "set", "squ-1", "tests", "--status", "pass")

	got := strings.TrimSpace(readFile(t, calls))
	if got != "job gate set squ-1 tests --status pass" {
		t.Fatalf("real agent-team args = %q", got)
	}
}

func TestAgentTeamShimAllowsKnownLeafVerbsWithPositionals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		allow string
	}{
		{name: "wildcard", allow: "*"},
		{name: "explicit", allow: "run,send"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shim, _, calls := installTestAgentTeamShim(t)

			runShim(t, shim, tc.allow, "run", "worker")
			runShim(t, shim, tc.allow, "send", "manager", "hello")

			got := strings.TrimSpace(readFile(t, calls))
			want := "run worker\nsend manager hello"
			if got != want {
				t.Fatalf("real agent-team args = %q, want %q", got, want)
			}
		})
	}
}

func TestAgentTeamShimDeniesKnownVerbOutsideAllowlist(t *testing.T) {
	shim, _, calls := installTestAgentTeamShim(t)

	stderr, code := runShimExpectExit(t, shim, "", "job", "merge", "squ-1")
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

func TestAgentTeamShimDeniesUnknownVerbEvenWithWildcard(t *testing.T) {
	shim, _, calls := installTestAgentTeamShim(t)

	stderr, code := runShimExpectExit(t, shim, "*", "future-dangerous-verb")
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
	shim, _, calls := installTestAgentTeamShim(t)

	stderr, code := runShimExpectExit(t, shim, "*", "future-dangerous-verb", "worker")
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
	shim, _, calls := installTestAgentTeamShim(t)

	stderr, code := runShimExpectExit(t, shim, "*", "job", "future-dangerous-verb")
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
	shim, real, calls := installTestAgentTeamShim(t)

	stderr, code := runShimExpectExit(t, shim, "", "job", "merge", "squ-1")
	if code != 3 || !strings.Contains(stderr, "denied verb job.merge") {
		t.Fatalf("shim exit = %d stderr=%q, want denial", code, stderr)
	}

	cmd := exec.Command(real, "job", "merge", "squ-1")
	cmd.Env = cleanShimEnv("")
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

func installTestAgentTeamShim(t *testing.T) (string, string, string) {
	t.Helper()
	tmp := t.TempDir()
	calls := filepath.Join(tmp, "calls.txt")
	real := filepath.Join(tmp, "real-agent-team")
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(calls) + "\n"
	if err := os.WriteFile(real, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	bin, err := InstallWithOptions(filepath.Join(tmp, "runtime"), nil, Options{RealAgentTeam: real})
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(bin, "agent-team"), real, calls
}

func runShim(t *testing.T, shim, allow string, args ...string) {
	t.Helper()
	if stderr, code := runShimExpectExit(t, shim, allow, args...); code != 0 {
		t.Fatalf("shim exit = %d stderr=%q", code, stderr)
	}
}

func runShimExpectExit(t *testing.T, shim, allow string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(shim, args...)
	cmd.Env = cleanShimEnv(allow)
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

func cleanShimEnv(allow string) []string {
	env := []string{"PATH=" + os.Getenv("PATH")}
	if allow != "" {
		env = append(env, EnvAuthorityAllowlist+"="+allow)
	}
	return env
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
