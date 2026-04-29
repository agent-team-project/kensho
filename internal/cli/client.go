package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
)

// errDaemonNotRunning is returned by NewDaemonClient when no daemon is alive
// for the target repo. Callers can use errors.Is to choose between routing
// through the daemon and falling back to direct exec.
var errDaemonNotRunning = errors.New("daemon: not running")

// daemonClient talks to agent-teamd over its unix socket. One client per
// command invocation — the underlying http.Client is short-lived.
type daemonClient struct {
	hc      *http.Client
	baseURL string
	teamDir string
}

// newDaemonClient probes the daemon's pidfile to confirm it's alive, then
// constructs an http.Client whose transport dials the unix socket. Returns
// errDaemonNotRunning if the pidfile is missing or stale.
func newDaemonClient(teamDir string) (*daemonClient, error) {
	pid, err := daemon.ReadPidfile(daemon.PidPath(teamDir))
	if err != nil || pid == 0 || !daemon.PidLiveCheck(pid) {
		return nil, errDaemonNotRunning
	}
	socket := daemon.SocketPath(teamDir)
	if _, err := os.Stat(socket); err != nil {
		return nil, errDaemonNotRunning
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		// Avoid keeping a pool around past the command's life.
		DisableKeepAlives: true,
	}
	return &daemonClient{
		hc:      &http.Client{Transport: transport, Timeout: 0}, // 0 = no timeout; logs may stream forever.
		baseURL: "http://daemon", // host name is irrelevant — DialContext fixes the socket.
		teamDir: teamDir,
	}, nil
}

// dispatchPayload mirrors POST /v1/dispatch's body.
type dispatchPayload struct {
	Agent     string   `json:"agent"`
	Name      string   `json:"name"`
	Prompt    string   `json:"prompt,omitempty"`
	Workspace string   `json:"workspace"`
	Args      []string `json:"args,omitempty"`
	Env       []string `json:"env,omitempty"`
}

type dispatchResponse struct {
	InstanceID string    `json:"instance_id"`
	StartedAt  time.Time `json:"started_at"`
	PID        int       `json:"pid"`
	SessionID  string    `json:"session_id"`
}

func (c *daemonClient) Dispatch(in dispatchPayload) (*dispatchResponse, error) {
	body, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", c.baseURL+"/v1/dispatch", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon: dispatch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon: dispatch: %s", readErrorBody(resp))
	}
	var out dispatchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: dispatch decode: %w", err)
	}
	return &out, nil
}

func (c *daemonClient) Instances() ([]*daemon.Metadata, error) {
	resp, err := c.hc.Get(c.baseURL + "/v1/instances")
	if err != nil {
		return nil, fmt.Errorf("daemon: instances: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon: instances: %s", readErrorBody(resp))
	}
	var out []*daemon.Metadata
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: instances decode: %w", err)
	}
	return out, nil
}

// LogsStream pipes the chunked text stream of the instance's child.log to w.
// follow=true keeps the connection open until ctx cancels.
func (c *daemonClient) LogsStream(ctx context.Context, w io.Writer, instance string, follow bool) error {
	u := c.baseURL + "/v1/logs/" + url.PathEscape(instance)
	if follow {
		u += "?follow=true"
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		// Context cancel during read shows up as a wrapped error — surface
		// nil so callers can treat user Ctrl-C as a clean exit.
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return fmt.Errorf("daemon: logs: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("no log for instance %q (has it been dispatched?)", instance)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon: logs: %s", readErrorBody(resp))
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	return nil
}

func readErrorBody(resp *http.Response) string {
	b, _ := io.ReadAll(resp.Body)
	return string(bytes.TrimSpace(b))
}
