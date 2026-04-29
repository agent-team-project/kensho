package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
)

// newTestClient wires a daemonClient at an httptest server. The unix-socket
// transport in newDaemonClient is the only piece we don't exercise here; the
// JSON wire format is identical, so this is enough coverage for the CLI
// layer.
func newTestClient(t *testing.T, h http.Handler) (*daemonClient, func()) {
	t.Helper()
	srv := httptest.NewServer(h)
	c := &daemonClient{
		hc:      &http.Client{Timeout: 0},
		baseURL: srv.URL,
		teamDir: t.TempDir(),
	}
	return c, srv.Close
}

func TestClient_Dispatch(t *testing.T) {
	root := t.TempDir()
	m := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, time.Second))
	c, cleanup := newTestClient(t, daemon.Handler(m, nil))
	defer cleanup()

	resp, err := c.Dispatch(dispatchPayload{
		Agent:     "worker",
		Name:      "w-1",
		Workspace: t.TempDir(),
		Args:      []string{"--add-dir", "/tmp/x"},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if resp.InstanceID != "w-1" || resp.PID == 0 || resp.SessionID == "" {
		t.Errorf("response: %+v", resp)
	}
	stopAndWaitForTest(t, m, "w-1")
}

func TestClient_Instances(t *testing.T) {
	root := t.TempDir()
	m := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, time.Second))
	c, cleanup := newTestClient(t, daemon.Handler(m, nil))
	defer cleanup()

	_, err := c.Dispatch(dispatchPayload{Agent: "w", Name: "x", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer stopAndWaitForTest(t, m, "x")

	insts, err := c.Instances()
	if err != nil {
		t.Fatalf("instances: %v", err)
	}
	if len(insts) != 1 || insts[0].Instance != "x" {
		t.Errorf("instances: %+v", insts)
	}
}

func TestClient_LogsStream_NotFound(t *testing.T) {
	root := t.TempDir()
	m := daemon.NewInstanceManager(root, nil)
	c, cleanup := newTestClient(t, daemon.Handler(m, nil))
	defer cleanup()

	var buf bytes.Buffer
	err := c.LogsStream(context.Background(), &buf, "missing", false)
	if err == nil || !strings.Contains(err.Error(), "no log") {
		t.Errorf("err: %v", err)
	}
}

func TestClient_LogsStream_NonFollow(t *testing.T) {
	root := t.TempDir()
	m := daemon.NewInstanceManager(root, nil)
	c, cleanup := newTestClient(t, daemon.Handler(m, nil))
	defer cleanup()

	// Seed a child.log file.
	writeChildLogForTest(t, root, "w", "alpha\nbeta\n")

	var buf bytes.Buffer
	if err := c.LogsStream(context.Background(), &buf, "w", false); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := buf.String(); got != "alpha\nbeta\n" {
		t.Errorf("body: got %q", got)
	}
}
