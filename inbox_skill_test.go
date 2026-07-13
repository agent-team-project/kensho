package agentteam

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	requests := make(chan received, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got payload
		err := json.NewDecoder(r.Body).Decode(&got)
		requests <- received{payload: got, err: err}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"delivered":true}`)
	}))
	t.Cleanup(server.Close)

	teamRoot := t.TempDir()
	tokenFile := filepath.Join(t.TempDir(), "daemon-token")
	if err := os.WriteFile(tokenFile, []byte("test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	message := "line one\n\n$(printf 'false FAIL') ; * ? [x] {a,b}\n`uname` \\\"double\\\" 'single' $HOME | & < >"

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
}
