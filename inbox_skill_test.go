package agentteam

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/agent-team-project/agent-team/internal/runtimeshim"
)

const inboxSkillHelper = "template/skills/inbox/scripts/inbox.sh"

const (
	staleInboxBuildHeader   = "source_id=git%3Ab062047f11111111111111111111111111111111&version=0.1.0"
	currentInboxBuildHeader = "source_id=git%3Ad45bb80522222222222222222222222222222222&version=0.1.0"
)

func TestInboxSkillSendReadsMessageFileWithoutShellRoundTrip(t *testing.T) {
	type payload struct {
		To   string `json:"to"`
		From string `json:"from"`
		Body string `json:"body"`
	}
	type received struct {
		payload payload
		header  string
		err     error
	}

	requests := make(chan received, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got payload
		err := json.NewDecoder(r.Body).Decode(&got)
		requests <- received{payload: got, header: r.Header.Get("X-Agent-Team-Build"), err: err}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"delivered":true}`)
	}))
	t.Cleanup(server.Close)

	teamRoot := t.TempDir()
	helperDir := filepath.Join(teamRoot, "skills", "inbox", "scripts")
	if err := os.MkdirAll(helperDir, 0o755); err != nil {
		t.Fatal(err)
	}
	helperPath, err := filepath.Abs(inboxSkillHelper)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(helperPath, filepath.Join(helperDir, "inbox.sh")); err != nil {
		t.Fatal(err)
	}
	runtimeEnv := inboxSkillRuntimeEnv(t, teamRoot, currentInboxBuildHeader)
	tokenFile := filepath.Join(t.TempDir(), "daemon-token")
	if err := os.WriteFile(tokenFile, []byte("test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	message := "\r\nline one\r\n\r\n$(printf 'false FAIL') ; * ? [x] {a,b}\r\n`uname` \\\"double\\\" 'single' $HOME | & < >\r\n"

	tests := []struct {
		name  string
		args  func(t *testing.T) []string
		stdin string
		want  string
	}{
		{
			name: "file",
			args: func(t *testing.T) []string {
				path := filepath.Join(t.TempDir(), "steering.txt")
				if err := os.WriteFile(path, []byte(message), 0o600); err != nil {
					t.Fatal(err)
				}
				return []string{"send", "manager", "--message-file", path}
			},
			want: message,
		},
		{
			name:  "stdin",
			args:  func(_ *testing.T) []string { return []string{"send", "manager", "--message-file", "-"} },
			stdin: message,
			want:  message,
		},
		{
			name: "simple positional compatibility",
			args: func(_ *testing.T) []string {
				return []string{"send", "manager", "short", "message"}
			},
			want: "short message",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("bash", append([]string{inboxSkillHelper}, tc.args(t)...)...)
			cmd.Env = append(append(os.Environ(), runtimeEnv...),
				"AGENT_TEAM_ROOT="+teamRoot,
				"AGENT_TEAM_INSTANCE=worker-gh409",
				"AGENT_TEAM_DAEMON_URL="+server.URL,
				"AGENT_TEAM_DAEMON_TOKEN_FILE="+tokenFile,
			)
			cmd.Stdin = strings.NewReader(tc.stdin)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("inbox helper: %v\n%s", err, output)
			}

			request := <-requests
			if request.err != nil {
				t.Fatalf("decode helper payload: %v", request.err)
			}
			if request.payload.To != "manager" || request.payload.From != "worker-gh409" || request.payload.Body != tc.want {
				t.Fatalf("payload = %#v, want exact body %q", request.payload, tc.want)
			}
			if request.header != currentInboxBuildHeader {
				t.Fatalf("build header = %q, want coherent %q", request.header, currentInboxBuildHeader)
			}
		})
	}

	t.Run("documented assign-worker follow-up", func(t *testing.T) {
		const (
			placeholder = "<the user's follow-up ask>"
			recipe      = `"$AGENT_TEAM_ROOT"/skills/inbox/scripts/inbox.sh send worker-squ-14 --message-file - <<'FOLLOW_UP'
<the user's follow-up ask>
FOLLOW_UP`
		)
		skill, err := os.ReadFile("template/agents/manager/skills/assign-worker/SKILL.md")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(skill), recipe) {
			t.Fatalf("assign-worker follow-up recipe must use a single-quoted heredoc with --message-file -")
		}

		followUp := "Please preserve $(printf INJECTED) $HOME and * ? [x]\r\nsecond line"
		cmd := exec.Command("bash", "-c", strings.Replace(recipe, placeholder, followUp, 1))
		cmd.Env = append(append(os.Environ(), runtimeEnv...),
			"AGENT_TEAM_ROOT="+teamRoot,
			"AGENT_TEAM_INSTANCE=manager",
			"AGENT_TEAM_DAEMON_URL="+server.URL,
			"AGENT_TEAM_DAEMON_TOKEN_FILE="+tokenFile,
		)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("assign-worker follow-up recipe: %v\n%s", err, output)
		}

		request := <-requests
		want := followUp + "\n"
		if request.err != nil {
			t.Fatalf("decode helper payload: %v", request.err)
		}
		if request.payload.To != "worker-squ-14" || request.payload.From != "manager" || request.payload.Body != want {
			t.Fatalf("payload = %#v, want exact body %q", request.payload, want)
		}
	})
}

func TestInboxSkillSelectsDaemonComparableManagedCLIOverStaleShim(t *testing.T) {
	var gotHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Agent-Team-Build")
		fmt.Fprint(w, `{"delivered":true}`)
	}))
	t.Cleanup(server.Close)
	tokenFile := filepath.Join(t.TempDir(), "daemon.token")
	if err := os.WriteFile(tokenFile, []byte("instance-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	staleDir := t.TempDir()
	currentDir := t.TempDir()
	writeAttestationCandidate(t, filepath.Join(staleDir, "agent-team"), "--build-attestation", staleInboxBuildHeader)
	writeAttestationCandidate(t, filepath.Join(currentDir, "agent-team"), "__build-attestation", currentInboxBuildHeader)

	cmd := exec.Command("bash", inboxSkillHelper, "send", "manager", "coherent selection")
	cmd.Env = append(os.Environ(),
		"AGENT_TEAM_ROOT="+t.TempDir(),
		"AGENT_TEAM_INSTANCE=worker-gh481",
		"AGENT_TEAM_DAEMON_URL="+server.URL,
		"AGENT_TEAM_DAEMON_TOKEN_FILE="+tokenFile,
		"AGENT_TEAM_SHIM_PATH=",
		"AGENT_TEAM_BUILD_HEADER="+staleInboxBuildHeader,
		"AGENT_TEAM_DAEMON_BUILD_HEADER="+currentInboxBuildHeader,
		"PATH="+staleDir+string(os.PathListSeparator)+currentDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("inbox helper: %v\n%s", err, output)
	}
	if gotHeader != currentInboxBuildHeader {
		t.Fatalf("build header = %q, want managed current %q", gotHeader, currentInboxBuildHeader)
	}
}

func TestInboxSkillFailsClosedWithoutComparableProvenance(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, `{"delivered":true}`)
	}))
	t.Cleanup(server.Close)
	tokenFile := filepath.Join(t.TempDir(), "daemon.token")
	if err := os.WriteFile(tokenFile, []byte("instance-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	provenanceFreeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(provenanceFreeDir, "agent-team"), []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", inboxSkillHelper, "send", "manager", "must fail closed")
	cmd.Env = append(os.Environ(),
		"AGENT_TEAM_ROOT="+t.TempDir(),
		"AGENT_TEAM_INSTANCE=worker-gh481",
		"AGENT_TEAM_DAEMON_URL="+server.URL,
		"AGENT_TEAM_DAEMON_TOKEN_FILE="+tokenFile,
		"AGENT_TEAM_SHIM_PATH="+filepath.Join(provenanceFreeDir, "agent-team"),
		"AGENT_TEAM_BUILD_HEADER=",
		"AGENT_TEAM_DAEMON_BUILD_HEADER="+currentInboxBuildHeader,
		"PATH="+provenanceFreeDir+string(os.PathListSeparator)+"/usr/bin:/bin",
	)
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "activation needed") {
		t.Fatalf("provenance-free helper error = %v output=%s", err, output)
	}
	if hits.Load() != 0 {
		t.Fatalf("daemon write hits = %d, want 0", hits.Load())
	}
}

func TestInboxSkillSendFallsBackFromStaleHTTPToLiveUnixSocket(t *testing.T) {
	type payload struct {
		To   string `json:"to"`
		From string `json:"from"`
		Body string `json:"body"`
	}
	requests := make(chan payload, 1)
	socket, stopUnix := startInboxUnixServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got payload
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		requests <- got
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"delivered":true,"id":"unix-message"}`)
	}))
	defer stopUnix()

	tokenFile := filepath.Join(t.TempDir(), "daemon.token")
	if err := os.WriteFile(tokenFile, []byte("instance-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", inboxSkillHelper, "send", "manager", "stale HTTP fallback")
	teamRoot := t.TempDir()
	cmd.Env = append(append(os.Environ(), inboxSkillRuntimeEnv(t, teamRoot, currentInboxBuildHeader)...),
		"AGENT_TEAM_ROOT="+teamRoot,
		"AGENT_TEAM_INSTANCE=worker-gh391",
		"AGENT_TEAM_DAEMON_URL="+closedInboxLoopbackURL(t),
		"AGENT_TEAM_DAEMON_SOCKET="+socket,
		"AGENT_TEAM_DAEMON_TOKEN_FILE="+tokenFile,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("inbox helper: %v\n%s", err, output)
	}
	got := <-requests
	if got.To != "manager" || got.From != "worker-gh391" || got.Body != "stale HTTP fallback" {
		t.Fatalf("Unix socket payload = %+v", got)
	}
}

func TestInboxSkillSendFallbackPropagatesUnixHTTPFailures(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var unixHits atomic.Int32
			socket, stopUnix := startInboxUnixServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				unixHits.Add(1)
				http.Error(w, http.StatusText(status), status)
			}))
			defer stopUnix()
			tokenFile := filepath.Join(t.TempDir(), "daemon.token")
			if err := os.WriteFile(tokenFile, []byte("instance-token\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", inboxSkillHelper, "send", "manager", "must stay failed")
			teamRoot := t.TempDir()
			cmd.Env = append(append(os.Environ(), inboxSkillRuntimeEnv(t, teamRoot, currentInboxBuildHeader)...),
				"AGENT_TEAM_ROOT="+teamRoot,
				"AGENT_TEAM_INSTANCE=worker-gh391",
				"AGENT_TEAM_DAEMON_URL="+closedInboxLoopbackURL(t),
				"AGENT_TEAM_DAEMON_SOCKET="+socket,
				"AGENT_TEAM_DAEMON_TOKEN_FILE="+tokenFile,
			)
			output, err := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 22 {
				t.Fatalf("inbox helper error after Unix HTTP %d = %v, want curl exit 22\n%s", status, err, output)
			}
			if unixHits.Load() != 1 {
				t.Fatalf("Unix socket hits = %d, want 1", unixHits.Load())
			}
			if !strings.Contains(string(output), http.StatusText(status)) {
				t.Fatalf("inbox helper output = %q, want daemon error body containing %q", output, http.StatusText(status))
			}
		})
	}
}

func TestInboxSkillSendDoesNotFallbackFromHTTPFailures(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var unixHits atomic.Int32
			socket, stopUnix := startInboxUnixServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				unixHits.Add(1)
				fmt.Fprint(w, `{"delivered":true}`)
			}))
			defer stopUnix()
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, http.StatusText(status), status)
			}))
			defer httpServer.Close()
			tokenFile := filepath.Join(t.TempDir(), "daemon.token")
			if err := os.WriteFile(tokenFile, []byte("instance-token\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", inboxSkillHelper, "send", "manager", "must stay failed")
			teamRoot := t.TempDir()
			cmd.Env = append(append(os.Environ(), inboxSkillRuntimeEnv(t, teamRoot, currentInboxBuildHeader)...),
				"AGENT_TEAM_ROOT="+teamRoot,
				"AGENT_TEAM_INSTANCE=worker-gh391",
				"AGENT_TEAM_DAEMON_URL="+httpServer.URL,
				"AGENT_TEAM_DAEMON_SOCKET="+socket,
				"AGENT_TEAM_DAEMON_TOKEN_FILE="+tokenFile,
			)
			output, err := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 22 {
				t.Fatalf("inbox helper error for HTTP %d = %v, want curl exit 22\n%s", status, err, output)
			}
			if unixHits.Load() != 0 {
				t.Fatalf("Unix socket hits = %d, want 0", unixHits.Load())
			}
		})
	}
}

func inboxSkillRuntimeEnv(t *testing.T, teamRoot, header string) []string {
	t.Helper()
	helper, err := filepath.Abs("template/scripts/skills/daemon-build.sh")
	if err != nil {
		t.Fatal(err)
	}
	helperDir := filepath.Join(teamRoot, "scripts", "skills")
	if err := os.MkdirAll(helperDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(helperDir, "daemon-build.sh")
	if err := os.Symlink(helper, link); err != nil && !errors.Is(err, os.ErrExist) {
		t.Fatal(err)
	}
	build, err := buildinfo.ParseHeaderValue(header)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "agent-team")
	if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	binDir, err := runtimeshim.Install(t.TempDir(), map[string]string{}, runtimeshim.Options{
		RealAgentTeam:      target,
		RealAgentTeamBuild: build,
		DaemonBuild:        build,
		Assets:             "inbox-skill-test-assets",
	})
	if err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(binDir, "agent-team")
	return []string{
		"AGENT_TEAM_SHIM_PATH=" + shim,
		"AGENT_TEAM_BUILD_HEADER=" + header,
		"AGENT_TEAM_DAEMON_BUILD_HEADER=" + header,
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
}

func writeAttestationCandidate(t *testing.T, path, command, header string) {
	t.Helper()
	marker := ""
	if command == "--build-attestation" {
		marker = "# Closed-world enforcement baked in at install time\n"
	}
	body := "#!/bin/sh\n" + marker +
		"if [ \"$1\" = \"" + command + "\" ] && [ \"$2\" = \"--header\" ]; then\n" +
		"  printf '%s\\n' '" + header + "'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 3\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func startInboxUnixServer(t *testing.T, handler http.Handler) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "agent-team-inbox-skill-")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "daemon.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		_ = os.RemoveAll(dir)
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	return socket, func() {
		_ = server.Close()
		_ = listener.Close()
		_ = os.RemoveAll(dir)
	}
}

func closedInboxLoopbackURL(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return "http://" + addr
}
