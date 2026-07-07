package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-team-project/agent-team/internal/loader"
)

// initArgsWithRequired is the canonical "init the bundled template into tmp,
// configured for Linear" arg list. It renders the default slim profile; tests
// that need the full self-dogfood topology add --profile full explicitly.
func initArgsWithRequired(target string) []string {
	return []string{
		"init", "--target", target,
		"--set", "linear.team_id=test-team-uuid",
		"--set", "linear.ticket_prefix=TST",
	}
}

func initArgsWithRequiredFull(target string) []string {
	return append(initArgsWithRequired(target), "--profile", "full")
}

func setTeamSkillsForTest(t *testing.T, teamDir string, skills ...string) {
	t.Helper()
	cfgPath := filepath.Join(teamDir, "config.toml")
	bodyBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	quoted := make([]string, 0, len(skills))
	for _, skill := range skills {
		quoted = append(quoted, `"`+strings.ReplaceAll(skill, `"`, `\"`)+`"`)
	}
	body := string(bodyBytes)
	next := strings.Replace(body, "team = []", "team = ["+strings.Join(quoted, ", ")+"]", 1)
	if next == body {
		t.Fatalf("config.toml missing empty team skills list:\n%s", body)
	}
	if err := os.WriteFile(cfgPath, []byte(next), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInit_DefaultTemplate(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs(initArgsWithRequired(tmp))

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v\nstderr: %s", err, errOut.String())
	}

	expected := []string{
		".agent_team/config.toml",
		".agent_team/.template.lock",
		".agent_team/agents/manager/agent.md",
		".agent_team/agents/manager/config.toml",
		".agent_team/agents/manager/skills/assign-worker/SKILL.md",
		".agent_team/agents/reviewer/agent.md",
		".agent_team/agents/reviewer/config.toml",
		".agent_team/agents/worker/agent.md",
		".agent_team/agents/worker/config.toml",
		".agent_team/agents/worker/scripts/git-push-verify.sh",
		".agent_team/skills/github/SKILL.md",
		".agent_team/skills/linear/SKILL.md",
		".agent_team/skills/linear/scripts/linear-graphql.sh",
		".agent_team/skills/pull-request/SKILL.md",
	}
	for _, rel := range expected {
		p := filepath.Join(tmp, rel)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing after init: %s", rel)
		}
	}

	// The resolved config.toml must contain the supplied --set values.
	cfg, err := os.ReadFile(filepath.Join(tmp, ".agent_team", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(cfg)
	if !strings.Contains(body, `team_id = "test-team-uuid"`) {
		t.Errorf("config.toml missing team_id: %s", body)
	}
	if !strings.Contains(body, `ticket_prefix = "TST"`) {
		t.Errorf("config.toml missing ticket_prefix: %s", body)
	}
	if !strings.Contains(body, `pm_tool = "linear"`) {
		t.Errorf("config.toml should auto-enable Linear when linear.* is set: %s", body)
	}
	if !strings.Contains(body, `team = []`) {
		t.Errorf("config.toml missing empty team skills list: %s", body)
	}
	if !strings.Contains(body, `profile = "slim"`) {
		t.Errorf("config.toml missing selected slim profile: %s", body)
	}
	workerConfig, err := os.ReadFile(filepath.Join(tmp, ".agent_team", "agents", "worker", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(workerConfig); !strings.Contains(got, ".agent_team/config.toml") || !strings.Contains(got, `team = ["linear", "github", "status"]`) {
		t.Errorf("worker config missing team-skill guidance: %s", got)
	} else if strings.Contains(got, "docs-freshness") || strings.Contains(got, "release") {
		t.Errorf("slim worker config included full-profile skills: %s", got)
	}
	if !strings.Contains(body, `provider = "linear"`) {
		t.Errorf("config.toml should set pm.provider when linear.* is set: %s", body)
	}
	for _, rel := range []string{
		".agent_team/agents/ticket-manager",
		".agent_team/agents/auditor",
		".agent_team/agents/comms",
		".agent_team/skills/release",
		".agent_team/skills/sentinel",
		".agent_team/skills/docs-freshness",
	} {
		if _, err := os.Stat(filepath.Join(tmp, rel)); !os.IsNotExist(err) {
			t.Errorf("slim init should not render %s (stat err=%v)", rel, err)
		}
	}
	instances, err := os.ReadFile(filepath.Join(tmp, ".agent_team", "instances.toml"))
	if err != nil {
		t.Fatal(err)
	}
	instancesBody := string(instances)
	for _, want := range []string{
		"[instances.manager]",
		"[instances.worker]",
		"[instances.reviewer]",
		"[pipelines.ticket_to_pr]",
		`instances   = ["manager", "worker", "reviewer"]`,
	} {
		if !strings.Contains(instancesBody, want) {
			t.Errorf("slim instances.toml missing %q:\n%s", want, instancesBody)
		}
	}
	for _, unwanted := range []string{
		"ticket-manager",
		"platform_ticket_to_pr",
		"release-worker",
		"sentinel",
	} {
		if strings.Contains(instancesBody, unwanted) {
			t.Errorf("slim instances.toml included %q:\n%s", unwanted, instancesBody)
		}
	}
	if strings.Contains(instancesBody, "\n[schedules.") {
		t.Errorf("slim instances.toml included an active schedule table:\n%s", instancesBody)
	}
	lock, err := os.ReadFile(filepath.Join(tmp, ".agent_team", ".template.lock"))
	if err != nil {
		t.Fatal(err)
	}
	lockBody := string(lock)
	for _, want := range []string{
		`ref = "bundled"`,
		`name = "default"`,
		`version = "1.0.0"`,
		`content_hash = "sha256:`,
	} {
		if !strings.Contains(lockBody, want) {
			t.Errorf(".template.lock missing %q: %s", want, lockBody)
		}
	}

	// template.toml itself must NOT land in the consumer's tree.
	if _, err := os.Stat(filepath.Join(tmp, ".agent_team", "template.toml")); !os.IsNotExist(err) {
		t.Errorf("template.toml leaked into consumer tree (err=%v)", err)
	}

	stdout := out.String()
	for _, want := range []string{
		"Vendoring team into",
		"Done. Next steps:",
		"agent-team run",
		"agent-team doctor",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q\nfull:\n%s", want, stdout)
		}
	}
}

func TestInit_FullProfilePreservesSelfDogfoodTemplate(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs(initArgsWithRequiredFull(tmp))

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init full profile: %v\nstderr: %s", err, errOut.String())
	}

	for _, rel := range []string{
		".agent_team/agents/ticket-manager/agent.md",
		".agent_team/agents/auditor/agent.md",
		".agent_team/agents/comms/agent.md",
		".agent_team/skills/release/SKILL.md",
		".agent_team/skills/sentinel/SKILL.md",
		".agent_team/skills/docs-freshness/SKILL.md",
	} {
		if _, err := os.Stat(filepath.Join(tmp, rel)); err != nil {
			t.Errorf("full profile missing %s: %v", rel, err)
		}
	}
	cfg, err := os.ReadFile(filepath.Join(tmp, ".agent_team", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), `profile = "full"`) {
		t.Fatalf("full profile config missing template.profile:\n%s", string(cfg))
	}
	instances, err := os.ReadFile(filepath.Join(tmp, ".agent_team", "instances.toml"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(instances)
	for _, want := range []string{
		"[instances.ticket-manager]",
		"[schedules.sentinel]",
		"run_on_start = true",
		"[pipelines.platform_ticket_to_pr]",
		"[pipelines.release]",
		`instances   = ["manager", "ticket-manager", "worker", "reviewer", "feedback-triage"]`,
		`schedules   = ["feedback-triage"]`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("full profile instances.toml missing %q:\n%s", want, body)
		}
	}
}

func TestInit_DefaultTemplateNoFlagsTicketless(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"init", "--target", tmp, "--no-input"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("zero-flag init should succeed: %v\nstderr: %s", err, errOut.String())
	}
	cfg, err := os.ReadFile(filepath.Join(tmp, ".agent_team", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(cfg)
	for _, want := range []string{
		`provider = "none"`,
		`pm_tool = "none"`,
		`team_id = ""`,
		`ticket_prefix = ""`,
		`team = []`,
		`profile = "slim"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("ticketless config missing %q:\n%s", want, body)
		}
	}
	if !strings.Contains(out.String(), "Done. Next steps:") {
		t.Fatalf("zero-flag init stdout missing next steps:\n%s", out.String())
	}
}

func TestInit_WorkerPushVerifyHelperRendered(t *testing.T) {
	tmp := initBundledTemplateForTest(t)
	helperPath := filepath.Join(tmp, ".agent_team", "agents", "worker", "scripts", "git-push-verify.sh")
	bodyBytes, err := os.ReadFile(helperPath)
	if err != nil {
		t.Fatalf("read helper: %v", err)
	}
	body := string(bodyBytes)
	for _, want := range []string{
		"github-auth.sh",
		"run_git ls-remote origin",
		"git rev-parse HEAD",
		`[ "$remote_sha" = "$local_sha" ]`,
		"run_git push -u origin",
		"retrying push once",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("helper missing %q:\n%s", want, body)
		}
	}
	st, err := os.Stat(helperPath)
	if err != nil {
		t.Fatalf("stat helper: %v", err)
	}
	if st.Mode()&0o111 == 0 {
		t.Fatalf("rendered helper should be executable, got mode %o", st.Mode())
	}

	agentBytes, err := os.ReadFile(filepath.Join(tmp, ".agent_team", "agents", "worker", "agent.md"))
	if err != nil {
		t.Fatalf("read worker agent: %v", err)
	}
	agent := string(agentBytes)
	for _, want := range []string{
		"git-push-verify.sh",
		"git ls-remote",
		"local `HEAD`",
	} {
		if !strings.Contains(agent, want) {
			t.Errorf("worker prompt missing %q:\n%s", want, agent)
		}
	}
}

func TestWorkerPushVerifyHelperPinsGitHubIdentityForGitHubRemote(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}

	tmp := initBundledTemplateForTest(t)
	setGitHubAgentLoginForInitTest(t, tmp, "agent-team-project")
	helperPath := filepath.Join(tmp, ".agent_team", "agents", "worker", "scripts", "git-push-verify.sh")
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitForInitTest(t, repo, "init")
	runGitForInitTest(t, repo, "config", "user.email", "worker@example.com")
	runGitForInitTest(t, repo, "config", "user.name", "Worker Test")
	runGitForInitTest(t, repo, "checkout", "-b", "bench-718")
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForInitTest(t, repo, "add", "file.txt")
	runGitForInitTest(t, repo, "commit", "-m", "initial")

	localSHA := runGitForInitTest(t, repo, "rev-parse", "HEAD")
	fakeBin := filepath.Join(tmp, "fakebin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeGit := filepath.Join(fakeBin, "git")
	const fakeGitBody = `#!/bin/sh
while [ "$1" = "-c" ]; do
    shift 2
done
case "$1" in
    remote)
        if [ "${2:-}" = "get-url" ] && [ "${3:-}" = "origin" ]; then
            echo "https://github.com/agent-team-project/kensho.git"
            exit 0
        fi
        ;;
    push)
        {
            echo "cmd=push"
            echo "GITHUB_TOKEN=$GITHUB_TOKEN"
            echo "GH_TOKEN=$GH_TOKEN"
            echo "AGENT_TEAM_GITHUB_LOGIN=$AGENT_TEAM_GITHUB_LOGIN"
            echo "GIT_TERMINAL_PROMPT=$GIT_TERMINAL_PROMPT"
            if [ -n "$GIT_ASKPASS" ] && [ -x "$GIT_ASKPASS" ]; then
                echo "GIT_ASKPASS=executable"
            else
                echo "GIT_ASKPASS=missing"
            fi
        } >> "$GIT_PUSH_VERIFY_FAKE_LOG"
        exit 0
        ;;
    ls-remote)
        {
            echo "cmd=ls-remote"
            echo "GITHUB_TOKEN=$GITHUB_TOKEN"
            echo "GH_TOKEN=$GH_TOKEN"
            echo "AGENT_TEAM_GITHUB_LOGIN=$AGENT_TEAM_GITHUB_LOGIN"
            echo "GIT_TERMINAL_PROMPT=$GIT_TERMINAL_PROMPT"
            if [ -n "$GIT_ASKPASS" ] && [ -x "$GIT_ASKPASS" ]; then
                echo "GIT_ASKPASS=executable"
            else
                echo "GIT_ASKPASS=missing"
            fi
        } >> "$GIT_PUSH_VERIFY_FAKE_LOG"
        printf '%s\trefs/heads/bench-718\n' "$LOCAL_SHA"
        exit 0
        ;;
esac
exec "$REAL_GIT" "$@"
`
	if err := os.WriteFile(fakeGit, []byte(fakeGitBody), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeCurl := filepath.Join(fakeBin, "curl")
	const fakeCurlBody = `#!/bin/sh
printf '%s\n' "$*" >> "$GIT_PUSH_VERIFY_CURL_LOG"
printf '{"login":"agent-team-project"}\n'
`
	if err := os.WriteFile(fakeCurl, []byte(fakeCurlBody), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeGh := filepath.Join(fakeBin, "gh")
	if err := os.WriteFile(fakeGh, []byte("#!/bin/sh\nexit 37\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	pushLog := filepath.Join(tmp, "push-auth.log")
	curlLog := filepath.Join(tmp, "curl.log")
	env := []string{
		"PATH=" + fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"REAL_GIT=" + gitPath,
		"LOCAL_SHA=" + localSHA,
		"AGENT_TEAM_ROOT=" + filepath.Join(tmp, ".agent_team"),
		"AGENT_TEAM_GITHUB_TOKEN=bot-token",
		"GITHUB_TOKEN=",
		"GH_TOKEN=",
		"GIT_PUSH_VERIFY_FAKE_LOG=" + pushLog,
		"GIT_PUSH_VERIFY_CURL_LOG=" + curlLog,
	}

	if out, err := runPushVerifyHelperForInitTest(t, helperPath, repo, env, "bench-718"); err != nil {
		t.Fatalf("GitHub auth push helper failed: %v\n%s", err, out)
	}
	logBytes, err := os.ReadFile(pushLog)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logBytes)
	for _, want := range []string{
		"cmd=push",
		"cmd=ls-remote",
		"GITHUB_TOKEN=bot-token",
		"GH_TOKEN=bot-token",
		"AGENT_TEAM_GITHUB_LOGIN=agent-team-project",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=executable",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("pinned git env log missing %q:\n%s", want, log)
		}
	}
	curlBytes, err := os.ReadFile(curlLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(curlBytes), "Authorization: Bearer bot-token") {
		t.Fatalf("actor verification did not use the pinned token:\n%s", string(curlBytes))
	}
}

func TestGitHubAuthHelperRejectsUnexpectedActor(t *testing.T) {
	tmp := initBundledTemplateForTest(t)
	setGitHubAgentLoginForInitTest(t, tmp, "agent-team-project")
	helperPath := filepath.Join(tmp, ".agent_team", "skills", "github", "scripts", "github-auth.sh")
	fakeBin := filepath.Join(tmp, "fakebin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeCurl := filepath.Join(fakeBin, "curl")
	if err := os.WriteFile(fakeCurl, []byte("#!/bin/sh\nprintf '{\"login\":\"jamesaud\"}\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeGh := filepath.Join(fakeBin, "gh")
	if err := os.WriteFile(fakeGh, []byte("#!/bin/sh\nexit 37\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(helperPath, "token")
	cmd.Dir = tmp
	cmd.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"AGENT_TEAM_ROOT="+filepath.Join(tmp, ".agent_team"),
		"AGENT_TEAM_GITHUB_TOKEN=personal-token",
		"GITHUB_TOKEN=",
		"GH_TOKEN=",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("github-auth helper accepted mismatched actor:\n%s", string(out))
	}
	if !strings.Contains(string(out), "GitHub token actor is jamesaud, expected agent-team-project") {
		t.Fatalf("mismatch error did not explain actor mismatch:\n%s", string(out))
	}
}

func TestWorkerPushVerifyHelperUsesRemoteTipAsAuthority(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}

	tmp := initBundledTemplateForTest(t)
	helperPath := filepath.Join(tmp, ".agent_team", "agents", "worker", "scripts", "git-push-verify.sh")
	origin := filepath.Join(tmp, "origin.git")
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	runGitForInitTest(t, origin, "init", "--bare")
	runGitForInitTest(t, repo, "init")
	runGitForInitTest(t, repo, "config", "user.email", "worker@example.com")
	runGitForInitTest(t, repo, "config", "user.name", "Worker Test")
	runGitForInitTest(t, repo, "checkout", "-b", "bench-718")
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForInitTest(t, repo, "add", "file.txt")
	runGitForInitTest(t, repo, "commit", "-m", "initial")
	runGitForInitTest(t, repo, "remote", "add", "origin", origin)

	// Normal push: the helper pushes and then verifies origin/branch == HEAD.
	if out, err := runPushVerifyHelperForInitTest(t, helperPath, repo, nil, "bench-718"); err != nil {
		t.Fatalf("normal push helper failed: %v\n%s", err, out)
	}
	assertRemoteMatchesHeadForInitTest(t, repo, "bench-718")

	fakeBin := filepath.Join(tmp, "fakebin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeGit := filepath.Join(fakeBin, "git")
	const fakeGitBody = `#!/bin/sh
if [ "$1" = "push" ]; then
    printf 'push\n' >> "$GIT_PUSH_VERIFY_FAKE_LOG"
    echo "simulated ambiguous push failure" >&2
    exit 124
fi
exec "$REAL_GIT" "$@"
`
	if err := os.WriteFile(fakeGit, []byte(fakeGitBody), 0o755); err != nil {
		t.Fatal(err)
	}
	pushLog := filepath.Join(tmp, "push.log")
	fakeEnv := []string{
		"PATH=" + fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"REAL_GIT=" + gitPath,
		"GIT_PUSH_VERIFY_FAKE_LOG=" + pushLog,
	}

	// Ambiguous push failure after the ref already landed: one failed push
	// attempt is enough because ls-remote proves the remote tip is HEAD.
	if err := os.Remove(pushLog); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if out, err := runPushVerifyHelperForInitTest(t, helperPath, repo, fakeEnv, "bench-718"); err != nil {
		t.Fatalf("already-landed ambiguous push helper failed: %v\n%s", err, out)
	}
	if got := countPushAttemptsForInitTest(t, pushLog); got != 1 {
		t.Fatalf("ambiguous already-landed push attempts = %d, want 1", got)
	}

	// Real failure: local HEAD differs and every push attempt fails. The
	// helper retries once, then surfaces failure instead of reporting success.
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForInitTest(t, repo, "add", "file.txt")
	runGitForInitTest(t, repo, "commit", "-m", "second")
	if err := os.Remove(pushLog); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	out, err := runPushVerifyHelperForInitTest(t, helperPath, repo, fakeEnv, "bench-718")
	if err == nil {
		t.Fatalf("helper succeeded despite remote tip mismatch:\n%s", out)
	}
	if got := countPushAttemptsForInitTest(t, pushLog); got != 2 {
		t.Fatalf("failed push attempts = %d, want retry once (2 attempts)", got)
	}
	if !strings.Contains(out, "push verification failed") {
		t.Fatalf("failure output missing clear verification error:\n%s", out)
	}
}

func setGitHubAgentLoginForInitTest(t *testing.T, tmp, login string) {
	t.Helper()
	cfgPath := filepath.Join(tmp, ".agent_team", "config.toml")
	cfgBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(cfgBytes)
	old := `agent_login = ""`
	if !strings.Contains(cfg, old) {
		t.Fatalf("rendered config missing %q:\n%s", old, cfg)
	}
	cfg = strings.Replace(cfg, old, fmt.Sprintf(`agent_login = %q`, login), 1)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInitJSONDefaultTemplate(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs(append(initArgsWithRequired(tmp), "--json"))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --json: %v\nstderr: %s", err, errOut.String())
	}
	var result initResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode init json: %v\nbody=%s", err, out.String())
	}
	resolvedTarget, err := resolveAbsTarget(tmp)
	if err != nil {
		t.Fatal(err)
	}
	teamDir := filepath.ToSlash(filepath.Join(resolvedTarget, ".agent_team"))
	if result.Target != filepath.ToSlash(resolvedTarget) || result.TeamDir != teamDir || result.Kind != "default" || result.Ref != "bundled" || result.TemplateName != "default" || result.TemplateVersion != "1.0.0" || result.Profile != "slim" || !strings.HasPrefix(result.ContentHash, "sha256:") || result.Empty || result.Force {
		t.Fatalf("unexpected init json result: %+v", result)
	}
	if result.DryRun || result.Action != "initialized" {
		t.Fatalf("unexpected init action fields: %+v", result)
	}
	if result.ConfigPath != filepath.ToSlash(filepath.Join(resolvedTarget, ".agent_team", "config.toml")) || result.LockPath != filepath.ToSlash(filepath.Join(resolvedTarget, ".agent_team", ".template.lock")) {
		t.Fatalf("unexpected init paths: %+v", result)
	}
	if strings.Contains(out.String(), "Vendoring team into") || strings.Contains(out.String(), "Done. Next steps") {
		t.Fatalf("init --json should not include progress text: %s", out.String())
	}
	if _, err := os.Stat(filepath.Join(tmp, ".agent_team", ".template.lock")); err != nil {
		t.Fatalf("template lock missing after init --json: %v", err)
	}
}

func TestInitDryRunJSONDoesNotWrite(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs(append(initArgsWithRequired(tmp), "--dry-run", "--json"))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --dry-run --json: %v\nstderr: %s", err, errOut.String())
	}
	var result initResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode init dry-run json: %v\nbody=%s", err, out.String())
	}
	if !result.DryRun || result.Action != "would-init" || result.Ref != "bundled" || result.Kind != "default" {
		t.Fatalf("unexpected dry-run init result: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".agent_team")); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create .agent_team, stat err=%v", err)
	}
}

func TestInitDryRunCommands(t *testing.T) {
	tmp := t.TempDir()
	resolvedTarget, err := resolveAbsTarget(tmp)
	if err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{
		"init",
		"--target", tmp,
		"--set", "linear.team_id=test-team-uuid",
		"--set", "linear.ticket_prefix=TST",
		"--dry-run",
		"--commands",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --dry-run --commands: %v\nstderr: %s", err, errOut.String())
	}
	want := strings.Join(shellQuoteArgs([]string{
		"agent-team", "init",
		"--target", filepath.ToSlash(resolvedTarget),
		"--set", "linear.team_id=test-team-uuid",
		"--set", "linear.ticket_prefix=TST",
	}), " ")
	if got := strings.TrimSpace(out.String()); got != want {
		t.Fatalf("init dry-run commands = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".agent_team")); !os.IsNotExist(err) {
		t.Fatalf("commands dry-run should not create .agent_team, stat err=%v", err)
	}
}

func TestInitFormatEmptyTemplate(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"init", "--target", tmp, "--template", "empty", "--format", "{{.Kind}} {{.Empty}} {{.TeamDir}} {{.LockPath}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init empty --format: %v\nstderr: %s", err, errOut.String())
	}
	resolvedTarget, err := resolveAbsTarget(tmp)
	if err != nil {
		t.Fatal(err)
	}
	want := "empty true " + filepath.ToSlash(filepath.Join(resolvedTarget, ".agent_team"))
	if got := strings.TrimSpace(out.String()); got != want {
		t.Fatalf("init empty format = %q, want %q", got, want)
	}
	if strings.Contains(out.String(), "Vendoring team into") || strings.Contains(out.String(), "Done. Next steps") {
		t.Fatalf("init --format should not include progress text: %s", out.String())
	}
}

func TestInit_LoaderReadsBundledTemplate(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(initArgsWithRequiredFull(tmp))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}

	teamDir := filepath.Join(tmp, ".agent_team")
	agents, err := loader.LoadAllAgents(teamDir)
	if err != nil {
		t.Fatalf("LoadAllAgents on bundled template: %v", err)
	}
	if len(agents) != 6 {
		t.Errorf("expected 6 bundled agents, got %d", len(agents))
	}
	if _, err := loader.UnionSkills(agents); err != nil {
		t.Errorf("UnionSkills: %v", err)
	}
}

func TestInit_LinearNoInputFailsListingMissing(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"init", "--target", tmp, "--set", "team.pm_tool=linear", "--no-input"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error: Linear params missing under --no-input")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
	combined := errOut.String()
	for _, want := range []string{
		"--no-input given but required parameters are missing:",
		"linear.team_id",
		"linear.ticket_prefix",
	} {
		if !strings.Contains(combined, want) {
			t.Errorf("error output missing %q\nfull:\n%s", want, combined)
		}
	}
}

func TestInit_PMProviderLinearNoInputFailsListingMissing(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"init", "--target", tmp, "--set", "pm.provider=linear", "--no-input"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error: Linear params missing under --no-input")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
	combined := errOut.String()
	for _, want := range []string{
		"--no-input given but required parameters are missing:",
		"linear.team_id",
		"linear.ticket_prefix",
	} {
		if !strings.Contains(combined, want) {
			t.Errorf("error output missing %q\nfull:\n%s", want, combined)
		}
	}
}

func TestInit_PMProviderLinearSyncsLegacyAlias(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{
		"init", "--target", tmp,
		"--set", "pm.provider=linear",
		"--set", "linear.team_id=test-team-uuid",
		"--set", "linear.ticket_prefix=TST",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v\nstderr: %s", err, errOut.String())
	}
	cfg, err := os.ReadFile(filepath.Join(tmp, ".agent_team", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(cfg)
	for _, want := range []string{`provider = "linear"`, `pm_tool = "linear"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("config missing %q:\n%s", want, body)
		}
	}
}

func TestInit_PatternViolationFails(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{
		"init", "--target", tmp,
		"--set", "linear.team_id=abc",
		"--set", "linear.ticket_prefix=lowercase-bad",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected pattern-violation error")
	}
	if !strings.Contains(errOut.String(), "does not match pattern") {
		t.Errorf("missing pattern error: %s", errOut.String())
	}
}

func TestInit_PromptFlow(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	// Linear mode has two conditionally required params; supply each on its own input line.
	cmd.SetIn(strings.NewReader("uuid-from-prompt\nABC\n"))
	cmd.SetArgs([]string{"init", "--target", tmp, "--set", "team.pm_tool=linear"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v\nstderr: %s", err, errOut.String())
	}
	cfg, err := os.ReadFile(filepath.Join(tmp, ".agent_team", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(cfg)
	if !strings.Contains(body, `team_id = "uuid-from-prompt"`) {
		t.Errorf("missing team_id from prompt: %s", body)
	}
	if !strings.Contains(body, `ticket_prefix = "ABC"`) {
		t.Errorf("missing ticket_prefix from prompt: %s", body)
	}
	if !strings.Contains(body, `provider = "linear"`) {
		t.Errorf("missing pm.provider alias from prompt: %s", body)
	}
	// stdout should show the prompts.
	if !strings.Contains(out.String(), "This template requires the following parameters") {
		t.Errorf("missing prompt header: %s", out.String())
	}
}

func TestInit_EmptyTemplate(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"init", "--target", tmp, "--template", "empty"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --template empty: %v", err)
	}

	teamDir := filepath.Join(tmp, ".agent_team")
	for _, sub := range []string{"agents", "skills"} {
		st, err := os.Stat(filepath.Join(teamDir, sub))
		if err != nil || !st.IsDir() {
			t.Errorf("expected %s to be a dir", sub)
		}
	}
	cfg, err := os.ReadFile(filepath.Join(teamDir, "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	if !strings.Contains(string(cfg), "empty-template stub") {
		t.Errorf("expected EMPTY_CONFIG marker, got: %s", cfg)
	}
	if _, err := os.Stat(filepath.Join(teamDir, ".template.lock")); !os.IsNotExist(err) {
		t.Errorf("empty template should not write .template.lock, got err=%v", err)
	}
}

func TestInit_BadTemplate(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"init", "--target", tmp, "--template", "bogus"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for bad template")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Errorf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut.String(), "must be `default` or `empty`") {
		t.Errorf("missing error text, got: %s", errOut.String())
	}
}

func TestInitOutputFlagValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "format json",
			args: []string{"init", "--json", "--format", "{{.TeamDir}}"},
			want: "--format cannot be combined with --json",
		},
		{
			name: "invalid format",
			args: []string{"init", "--format", "{{"},
			want: "invalid --format template",
		},
		{
			name: "commands without dry-run",
			args: []string{"init", "--commands"},
			want: wantCommandsModeRequiresDryRun(),
		},
		{
			name: "commands json",
			args: []string{"init", "--dry-run", "--commands", "--json"},
			want: wantCommandsModeConflict("--json"),
		},
		{
			name: "commands format",
			args: []string{"init", "--dry-run", "--commands", "--format", "{{.TeamDir}}"},
			want: wantCommandsModeConflict("--format"),
		},
		{
			name: "machine output no prompt",
			args: []string{"init", "--json", "--target", t.TempDir(), "--set", "team.pm_tool=linear"},
			want: "machine-readable output requested but required parameters are missing",
		},
		{
			name: "dry-run no prompt",
			args: []string{"init", "--dry-run", "--target", t.TempDir(), "--set", "team.pm_tool=linear"},
			want: "--dry-run requested but required parameters are missing",
		},
		{
			name: "bad profile",
			args: []string{"init", "--target", t.TempDir(), "--profile", "bogus", "--no-input"},
			want: `parameter template.profile value "bogus" does not match pattern`,
		},
		{
			name: "conflicting profile set",
			args: []string{"init", "--target", t.TempDir(), "--profile", "slim", "--set", "template.profile=full", "--no-input"},
			want: `--profile "slim" conflicts with --set template.profile="full"`,
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
				t.Fatalf("expected init validation failure, stdout=%s", out.String())
			}
			var code ExitCode
			if !errors.As(err, &code) || int(code) != 2 {
				t.Fatalf("err = %v, want exit 2", err)
			}
			if !strings.Contains(errOut.String(), tt.want) {
				t.Fatalf("stderr = %q, want %q", errOut.String(), tt.want)
			}
		})
	}
}

func TestInit_BadTarget(t *testing.T) {
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"init", "--target", "/this/does/not/exist/anywhere"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing target")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Errorf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut.String(), "is not a directory") {
		t.Errorf("missing error text, got: %s", errOut.String())
	}
}

func TestInit_SkipsExistingWithoutForce(t *testing.T) {
	tmp := t.TempDir()
	for i := 0; i < 2; i++ {
		cmd := NewRootCmd()
		out := &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(initArgsWithRequired(tmp))
		if err := cmd.Execute(); err != nil {
			t.Fatalf("init pass %d: %v", i, err)
		}
		if i == 1 && !strings.Contains(out.String(), "skip .agent_team/agents") {
			t.Fatalf("expected skip output on second init, got:\n%s", out.String())
		}
	}
}

func TestInit_PreservesTemplateLockWithoutForce(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(initArgsWithRequired(tmp))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init1: %v", err)
	}

	lockPath := filepath.Join(tmp, ".agent_team", ".template.lock")
	if err := os.WriteFile(lockPath, []byte("consumer lock edit"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd2 := NewRootCmd()
	cmd2.SetOut(&bytes.Buffer{})
	cmd2.SetErr(&bytes.Buffer{})
	cmd2.SetArgs(initArgsWithRequired(tmp))
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("init2: %v", err)
	}
	got, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "consumer lock edit" {
		t.Errorf("lock was overwritten without --force: %s", got)
	}
}

func TestInit_ForceOverwritesDirs(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(initArgsWithRequired(tmp))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init1: %v", err)
	}

	target := filepath.Join(tmp, ".agent_team", "agents", "worker", "agent.md")
	if err := os.WriteFile(target, []byte("MUTATED"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd2 := NewRootCmd()
	cmd2.SetOut(&bytes.Buffer{})
	cmd2.SetErr(&bytes.Buffer{})
	args := append(initArgsWithRequired(tmp), "--force")
	cmd2.SetArgs(args)
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("init2: %v", err)
	}

	got, _ := os.ReadFile(target)
	if string(got) == "MUTATED" {
		t.Errorf("--force did not overwrite agent.md")
	}
}

func TestInit_ForceOverwritesTemplateLock(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(initArgsWithRequired(tmp))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init1: %v", err)
	}

	lockPath := filepath.Join(tmp, ".agent_team", ".template.lock")
	if err := os.WriteFile(lockPath, []byte("stale lock"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd2 := NewRootCmd()
	cmd2.SetOut(&bytes.Buffer{})
	cmd2.SetErr(&bytes.Buffer{})
	cmd2.SetArgs(append(initArgsWithRequired(tmp), "--force"))
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("init2: %v", err)
	}
	got, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == "stale lock" {
		t.Fatal("--force did not overwrite .template.lock")
	}
	if !strings.Contains(string(got), `ref = "bundled"`) {
		t.Errorf("rewritten lock missing bundled ref: %s", got)
	}
}

func initBundledTemplateForTest(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(initArgsWithRequiredFull(tmp))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init bundled template: %v", err)
	}
	return tmp
}

func runGitForInitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func runPushVerifyHelperForInitTest(t *testing.T, helperPath, repo string, extraEnv []string, branch string) (string, error) {
	t.Helper()
	cmd := exec.Command(helperPath, branch)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), extraEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), err
	}
	return string(out), nil
}

func assertRemoteMatchesHeadForInitTest(t *testing.T, repo, branch string) {
	t.Helper()
	local := runGitForInitTest(t, repo, "rev-parse", "HEAD")
	remote := runGitForInitTest(t, repo, "ls-remote", "origin", "refs/heads/"+branch)
	fields := strings.Fields(remote)
	if len(fields) == 0 {
		t.Fatalf("origin/%s missing after push", branch)
	}
	if fields[0] != local {
		t.Fatalf("origin/%s = %s, local HEAD = %s", branch, fields[0], local)
	}
}

func countPushAttemptsForInitTest(t *testing.T, path string) int {
	t.Helper()
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if line == "push" {
			count++
		}
	}
	return count
}
