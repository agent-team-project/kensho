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
)

const inboxSkillHelper = "template/skills/inbox/scripts/inbox.sh"

func TestInboxSkillSendReadsMessageFileWithoutShellRoundTrip(t *testing.T) {
	type payload struct {
		To   string `json:"to"`
		From string `json:"from"`
		Body string `json:"body"`
	}
	type received struct {
		payload payload
		err     error
	}

	requests := make(chan received, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got payload
		err := json.NewDecoder(r.Body).Decode(&got)
		requests <- received{payload: got, err: err}
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
			cmd.Env = append(os.Environ(),
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
		cmd.Env = append(os.Environ(),
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
	cmd.Env = append(os.Environ(),
		"AGENT_TEAM_ROOT="+t.TempDir(),
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
			cmd.Env = append(os.Environ(),
				"AGENT_TEAM_ROOT="+t.TempDir(),
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
			cmd.Env = append(os.Environ(),
				"AGENT_TEAM_ROOT="+t.TempDir(),
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
