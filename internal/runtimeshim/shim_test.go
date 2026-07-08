package runtimeshim

import (
	"errors"
	"fmt"
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
	runShim(t, shim, "inbox", "ls")
	runShim(t, shim, "inbox", "check", "--self")

	got := strings.TrimSpace(readFile(t, calls))
	want := "--repo /tmp/repo job show squ-1\ninbox ls\ninbox check --self"
	if got != want {
		t.Fatalf("real agent-team args = %q", got)
	}
}

func TestInboxRuntimeShimPromptPathCheckAndAckAreOrdered(t *testing.T) {
	tmp := t.TempDir()
	teamDir := filepath.Join(tmp, ".agent_team")
	inboxDir := filepath.Join(teamDir, "daemon", "worker")
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mailbox := filepath.Join(inboxDir, "mailbox.jsonl")
	body := strings.Join([]string{
		`{"id":"msg-1","from":"manager","body":"first"}`,
		`{"id":"msg-2","from":"reviewer","body":"second"}`,
		"",
	}, "\n")
	if err := os.WriteFile(mailbox, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	skillDir, err := filepath.Abs(filepath.Join("..", "..", "template", "skills", "inbox"))
	if err != nil {
		t.Fatal(err)
	}
	bin, err := InstallWithOptions(filepath.Join(tmp, "runtime"), map[string]string{"inbox": skillDir}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	inbox := filepath.Join(bin, "inbox")
	env := append(os.Environ(),
		"AGENT_TEAM_ROOT="+teamDir,
		"AGENT_TEAM_INSTANCE=worker",
	)

	check := exec.Command(inbox, "check")
	check.Env = env
	out, err := check.CombinedOutput()
	if err != nil {
		t.Fatalf("inbox check: %v\n%s", err, out)
	}
	for _, want := range []string{"first", "second", "Ack with: inbox ack <id>"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("inbox check output missing %q:\n%s", want, out)
		}
	}

	ackSecond := exec.Command(inbox, "ack", "msg-2")
	ackSecond.Env = env
	out, err = ackSecond.CombinedOutput()
	if err == nil {
		t.Fatalf("inbox ack msg-2 succeeded unexpectedly:\n%s", out)
	}
	if !strings.Contains(string(out), "not the next unread message") || !strings.Contains(string(out), "msg-1") {
		t.Fatalf("inbox ack msg-2 output = %q, want ordered ack hint", out)
	}
	cursor := strings.TrimSpace(readOptionalFile(t, filepath.Join(inboxDir, "mailbox-cursor.txt")))
	if cursor != "" {
		t.Fatalf("cursor after rejected ack = %q, want empty", cursor)
	}

	ackFirst := exec.Command(inbox, "ack", "msg-1")
	ackFirst.Env = env
	out, err = ackFirst.CombinedOutput()
	if err != nil {
		t.Fatalf("inbox ack msg-1: %v\n%s", err, out)
	}
	cursor = strings.TrimSpace(readOptionalFile(t, filepath.Join(inboxDir, "mailbox-cursor.txt")))
	if cursor != "msg-1" {
		t.Fatalf("cursor after ack msg-1 = %q", cursor)
	}

	ackAll := exec.Command(inbox, "ack", "--all")
	ackAll.Env = env
	out, err = ackAll.CombinedOutput()
	if err != nil {
		t.Fatalf("inbox ack --all: %v\n%s", err, out)
	}
	cursor = strings.TrimSpace(readOptionalFile(t, filepath.Join(inboxDir, "mailbox-cursor.txt")))
	if cursor != "msg-2" {
		t.Fatalf("cursor after ack --all = %q", cursor)
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

func TestAgentTeamShimStrictAuthorityDeniesDefaultAllowlistOutsideGrant(t *testing.T) {
	shim, _, calls := installShim(t, Options{
		EnforceAuthority:   true,
		AuthorityAllowlist: []string{"job.show"},
		StrictAuthority:    true,
	})

	runShim(t, shim, "job", "show", "squ-1")
	stderr, code := runShimExpectExit(t, shim, "inbox", "check")
	if code != 3 {
		t.Fatalf("exit code = %d, want 3; stderr=%s", code, stderr)
	}
	if got := strings.TrimSpace(stderr); got != "agent-team shim: denied verb inbox.check" {
		t.Fatalf("stderr = %q", got)
	}

	got := strings.TrimSpace(readFile(t, calls))
	if got != "job show squ-1" {
		t.Fatalf("real agent-team args = %q", got)
	}
}

func TestReviewerPromptMatchesRuntimeCommandSurface(t *testing.T) {
	root := filepath.Join("..", "..")
	prompt := readFile(t, filepath.Join(root, "template", "agents", "reviewer", "agent.md"))
	config := readFile(t, filepath.Join(root, "template", "agents", "reviewer", "config.toml"))
	topology := readFile(t, filepath.Join(root, "template", "instances.toml.tmpl"))

	for _, want := range []string{
		"inbox check",
		"inbox ack <id>",
		"agent-team budget status --self",
		"agent-team job show $AGENT_TEAM_JOB_ID --json",
		"agent-team job gate set $AGENT_TEAM_JOB_ID",
		"agent-team feedback submit",
		"github-auth.sh",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("reviewer prompt missing allowed command surface %q", want)
		}
	}

	for _, denied := range []string{
		"agent-team job extend",
		"agent-team job step",
		"agent-team job merge",
		"status set",
		"status skill",
		"--description",
	} {
		if strings.Contains(prompt, denied) {
			t.Fatalf("reviewer prompt advertises denied or unavailable command %q", denied)
		}
	}

	for _, deniedSkill := range []string{`"pull-request"`, `"status"`} {
		if strings.Contains(config, deniedSkill) {
			t.Fatalf("reviewer config includes non-reviewer skill %s", deniedSkill)
		}
	}

	if strings.Contains(topology, "Report via `job gate set`") {
		t.Fatal("review pipeline instructions advertise bare `job gate set`; use `agent-team job gate set`")
	}

	reviewerAuthority := sectionAfter(t, topology, "[authority.agents.reviewer]")
	for _, want := range []string{
		`"budget.status"`,
		`"feedback.*"`,
		`"inbox.*"`,
		`"job.gate.*:own"`,
		`"job.show"`,
	} {
		if !strings.Contains(reviewerAuthority, want) {
			t.Fatalf("reviewer authority missing %s in %q", want, reviewerAuthority)
		}
	}
	for _, deniedVerb := range []string{"job.step", "job.extend", "job.merge"} {
		if strings.Contains(reviewerAuthority, deniedVerb) {
			t.Fatalf("reviewer authority grants denied verb %q in %q", deniedVerb, reviewerAuthority)
		}
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
	if got := strings.TrimSpace(stderr); got != "agent-team shim: denied unknown verb" {
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
	if got := strings.TrimSpace(stderr); got != "agent-team shim: denied unknown verb" {
		t.Fatalf("stderr = %q", got)
	}
	if _, err := os.Stat(calls); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("real agent-team should not be invoked, stat err=%v", err)
	}
}

func TestAgentTeamShimDeniesUnknownSubverbUnderKnownGroupEvenWithWildcard(t *testing.T) {
	// Closed-world: an unknown token under a real group is an unknown verb and is
	// denied BEFORE the allowlist is consulted — even a wildcard cannot grant a
	// verb that does not exist.
	shim, _, calls := installEnforcingShim(t, []string{"*"})

	stderr, code := runShimExpectExit(t, shim, "job", "future-dangerous-verb")
	if code != 3 {
		t.Fatalf("exit code = %d, want 3; stderr=%s", code, stderr)
	}
	if got := strings.TrimSpace(stderr); got != "agent-team shim: denied unknown verb" {
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
	// The fake real-binary delegates `__resolve-verb` to the actually-built
	// agent-team so verb resolution is exercised against the true Cobra tree
	// (no stub table to drift); every other invocation is recorded, standing in
	// for the real command running.
	body := "#!/bin/sh\n" +
		"if [ \"$1\" = __resolve-verb ]; then exec " + shellQuote(builtAgentTeam(t)) + " \"$@\"; fi\n" +
		"printf '%s\\n' \"$*\" >> " + shellQuote(calls) + "\n"
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

// builtAgentTeam returns the path to a real agent-team binary built once for the
// whole test binary (see TestMain), so shim tests resolve verbs against the true
// Cobra command tree rather than a stub table that could drift.
func builtAgentTeam(t *testing.T) string {
	t.Helper()
	if builtAgentTeamPath == "" {
		t.Fatal("agent-team not built; TestMain did not run")
	}
	return builtAgentTeamPath
}

var builtAgentTeamPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "runtimeshim-agent-team")
	if err != nil {
		panic(err)
	}
	out := filepath.Join(dir, "agent-team")
	if b, err := exec.Command("go", "build", "-o", out, "github.com/agent-team-project/agent-team/cmd/agent-team").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build agent-team for shim tests: %v\n%s", err, b)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	builtAgentTeamPath = out
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
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

func readOptionalFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
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

func sectionAfter(t *testing.T, body, header string) string {
	t.Helper()
	start := strings.Index(body, header)
	if start < 0 {
		t.Fatalf("missing section %s", header)
	}
	section := body[start+len(header):]
	if next := strings.Index(section, "\n["); next >= 0 {
		section = section[:next]
	}
	return section
}
