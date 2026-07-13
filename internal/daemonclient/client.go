// Package daemonclient provides the shared typed client for agent-teamd's
// HTTP/JSON API over loopback HTTP or a Unix-domain socket.
package daemonclient

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
	"strings"
	"time"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/feedback"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/topology"
	"github.com/agent-team-project/agent-team/internal/usage"
)

// ErrNotRunning is returned by New when no daemon is alive
// for the target repo. Callers can use errors.Is to choose between routing
// through the daemon and falling back to direct exec.
var ErrNotRunning = errors.New("daemon: not running")

type LogNotFoundError struct {
	Instance string
}

func (e *LogNotFoundError) Error() string {
	return fmt.Sprintf("no log for instance %q (has it been dispatched?)", e.Instance)
}

// TransportKind identifies the selected daemon transport without exposing any
// bearer-token contents.
type TransportKind string

const (
	TransportHTTP TransportKind = "http"
	TransportUnix TransportKind = "unix"
)

// Connection describes the endpoint selected by discovery. TokenFile is the
// path used for authentication, never the token value itself.
type Connection struct {
	Kind      TransportKind
	Endpoint  string
	TokenFile string
}

// Options controls client construction. Zero values preserve the existing CLI
// behavior: no request timeout, short-lived Unix connections, and no build
// header unless the caller supplies Build.
type Options struct {
	Timeout      time.Duration
	Build        buildinfo.Info
	KeepAlive    bool
	RoundTripper http.RoundTripper
}

// Client talks to agent-teamd over loopback HTTP or its Unix socket.
type Client struct {
	hc         *http.Client
	baseURL    string
	connection Connection
	teamDir    string
}

// New uses a configured loopback HTTP endpoint when one is
// advertised, otherwise it probes the daemon's pidfile before dialing the
// unix socket. Returns ErrNotRunning if no usable transport is known.
func New(teamDir string, options Options) (*Client, error) {
	var client *Client
	var err error
	if baseURL := configuredDaemonHTTPURL(teamDir); baseURL != "" {
		client, err = newDaemonHTTPURLClient(teamDir, baseURL, options)
	} else {
		client, err = newDaemonUnixSocketClientForTeamDir(teamDir, options)
	}
	if client != nil {
		client.teamDir = teamDir
	}
	return client, err
}

// NewForTargetTeamDir discovers a transport for another team directory. It
// deliberately ignores inherited endpoint/token environment variables.
func NewForTargetTeamDir(teamDir string, options Options) (*Client, error) {
	if baseURL := persistedDaemonHTTPURL(teamDir); baseURL != "" {
		client := newHTTPClientWithUnixFallback(baseURL, daemon.OperatorTokenPath(teamDir), daemon.SocketPath(teamDir), options)
		client.teamDir = teamDir
		return client, nil
	}
	client, err := newDaemonUnixSocketClientForTeamDir(teamDir, options)
	if client != nil {
		client.teamDir = teamDir
	}
	return client, err
}

func newDaemonUnixSocketClientForTeamDir(teamDir string, options Options) (*Client, error) {
	pid, err := daemon.ReadPidfile(daemon.PidPath(teamDir))
	if err != nil || pid == 0 || !daemon.PidLiveCheck(pid) {
		return nil, ErrNotRunning
	}
	socket := daemon.SocketPath(teamDir)
	if _, err := os.Stat(socket); err == nil {
		return NewUnix(socket, options), nil
	}
	return nil, ErrNotRunning
}

func configuredDaemonHTTPURL(teamDir string) string {
	if baseURL := strings.TrimSpace(os.Getenv("AGENT_TEAM_DAEMON_URL")); baseURL != "" {
		return baseURL
	}
	return persistedDaemonHTTPURL(teamDir)
}

func persistedDaemonHTTPURL(teamDir string) string {
	if httpAddr, err := daemon.ReadHTTPAddr(teamDir); err == nil && strings.TrimSpace(httpAddr) != "" {
		return daemon.DaemonHTTPURL(httpAddr)
	}
	return ""
}

func newDaemonHTTPURLClient(teamDir, baseURL string, options Options) (*Client, error) {
	tokenFile := strings.TrimSpace(os.Getenv(daemon.DaemonTokenFileEnv))
	if tokenFile == "" {
		tokenFile = daemon.OperatorTokenPath(teamDir)
	}
	socket := strings.TrimSpace(os.Getenv("AGENT_TEAM_DAEMON_SOCKET"))
	if socket == "" {
		socket = daemon.SocketPath(teamDir)
	}
	return newHTTPClientWithUnixFallback(baseURL, tokenFile, socket, options), nil
}

func newHTTPClientWithUnixFallback(baseURL, tokenFile, socket string, options Options) *Client {
	primary := options.RoundTripper
	if primary == nil {
		primary = http.DefaultTransport
	}
	options.RoundTripper = &reachabilityFallbackTransport{
		primary:  primary,
		fallback: newUnixTransport(socket, options.KeepAlive),
		socket:   socket,
	}
	return NewHTTP(baseURL, tokenFile, options)
}

// NewHTTP constructs a client for an explicit loopback HTTP endpoint.
func NewHTTP(baseURL, tokenFile string, options Options) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	return &Client{
		hc:      newDaemonHTTPClient(options.RoundTripper, options, tokenFile),
		baseURL: baseURL,
		connection: Connection{
			Kind:      TransportHTTP,
			Endpoint:  baseURL,
			TokenFile: tokenFile,
		},
	}
}

// NewUnix constructs a client for an explicit Unix-domain socket.
func NewUnix(socket string, options Options) *Client {
	transport := newUnixTransport(socket, options.KeepAlive)
	return &Client{
		hc:         newDaemonHTTPClient(transport, options, ""),
		baseURL:    "http://daemon", // host name is irrelevant — DialContext fixes the socket.
		connection: Connection{Kind: TransportUnix, Endpoint: socket},
	}
}

func newUnixTransport(socket string, keepAlive bool) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		// Avoid keeping a pool around past the command's life.
		DisableKeepAlives: !keepAlive,
	}
}

// reachabilityFallbackTransport retries a request over the repository Unix
// socket only when the selected HTTP endpoint cannot be reached during dial.
// An HTTP response, including 401/403/5xx, is authoritative and never reaches
// this fallback. Read/write failures are also left untouched because the
// daemon may already have accepted a non-idempotent request.
type reachabilityFallbackTransport struct {
	primary  http.RoundTripper
	fallback http.RoundTripper
	socket   string
}

func (t *reachabilityFallbackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.primary.RoundTrip(req)
	if err == nil || req.Context().Err() != nil || !dialReachabilityError(err) {
		return resp, err
	}

	retry := req.Clone(req.Context())
	if req.Body != nil {
		if req.GetBody == nil {
			return resp, err
		}
		body, bodyErr := req.GetBody()
		if bodyErr != nil {
			return resp, err
		}
		retry.Body = body
	}
	fallbackResp, fallbackErr := t.fallback.RoundTrip(retry)
	if fallbackErr != nil {
		return nil, fmt.Errorf("%w; daemon Unix fallback %s: %v", err, t.socket, fallbackErr)
	}
	return fallbackResp, nil
}

func (t *reachabilityFallbackTransport) CloseIdleConnections() {
	for _, transport := range []http.RoundTripper{t.primary, t.fallback} {
		if closer, ok := transport.(interface{ CloseIdleConnections() }); ok {
			closer.CloseIdleConnections()
		}
	}
}

func dialReachabilityError(err error) bool {
	for current := err; current != nil; current = errors.Unwrap(current) {
		if opErr, ok := current.(*net.OpError); ok && opErr.Op == "dial" {
			return true
		}
		if _, ok := current.(*net.DNSError); ok {
			return true
		}
	}
	return false
}

func newDaemonHTTPClient(base http.RoundTripper, options Options, tokenFile string) *http.Client {
	return &http.Client{
		Transport: daemonBuildHeaderTransport{
			base:      base,
			build:     options.Build,
			tokenFile: tokenFile,
		},
		Timeout: options.Timeout,
	}
}

// Connection reports the selected transport and token-file path.
func (c *Client) Connection() Connection { return c.connection }

// CloseIdleConnections releases any pooled HTTP or Unix connections.
func (c *Client) CloseIdleConnections() { c.hc.CloseIdleConnections() }

type daemonBuildHeaderTransport struct {
	base      http.RoundTripper
	build     buildinfo.Info
	tokenFile string
}

func (t daemonBuildHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if value := t.build.HeaderValue(); value != "" && req.Header.Get(buildinfo.HeaderName) == "" {
		req = req.Clone(req.Context())
		req.Header.Set(buildinfo.HeaderName, value)
	}
	if value := OriginHeaderFromEnv(t.build); value != "" && req.Header.Get(origin.HeaderName) == "" {
		req = req.Clone(req.Context())
		req.Header.Set(origin.HeaderName, value)
	}
	if tokenFile := strings.TrimSpace(t.tokenFile); tokenFile != "" && req.Header.Get("Authorization") == "" {
		token, err := daemon.ReadTokenFile(tokenFile)
		if err != nil {
			return nil, fmt.Errorf("daemon: read token file: %w", err)
		}
		if strings.TrimSpace(token) != "" {
			req = req.Clone(req.Context())
			req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
		}
	}
	return base.RoundTrip(req)
}

func (t daemonBuildHeaderTransport) CloseIdleConnections() {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if closer, ok := base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

// OriginHeaderFromEnv returns the operator-origin header derived from the
// standard AGENT_TEAM_ORIGIN_* environment variables.
func OriginHeaderFromEnv(build buildinfo.Info) string {
	env := origin.Envelope{
		Project:  os.Getenv("AGENT_TEAM_PROJECT"),
		Team:     os.Getenv("AGENT_TEAM_TEAM"),
		Instance: firstNonEmpty(os.Getenv("AGENT_TEAM_ORIGIN_INSTANCE"), os.Getenv("AGENT_TEAM_INSTANCE")),
		Agent:    os.Getenv("AGENT_TEAM_ORIGIN_AGENT"),
		Job:      firstNonEmpty(os.Getenv("AGENT_TEAM_ORIGIN_JOB"), os.Getenv("AGENT_TEAM_JOB_ID")),
		Trigger:  os.Getenv("AGENT_TEAM_ORIGIN_TRIGGER"),
		Build:    os.Getenv("AGENT_TEAM_ORIGIN_BUILD"),
	}
	identity := env
	identity.Build = ""
	if identity.Clean().Empty() {
		return ""
	}
	if strings.TrimSpace(env.Build) == "" {
		env.Build = build.Display()
	}
	return origin.HeaderValue(env)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// DispatchInput mirrors POST /v1/dispatch's body.
type DispatchInput struct {
	Agent               string   `json:"agent"`
	Name                string   `json:"name"`
	URI                 string   `json:"uri,omitempty"`
	SpecURI             string   `json:"spec_uri,omitempty"`
	DeploymentURI       string   `json:"deployment_uri,omitempty"`
	DeploymentParentURI string   `json:"deployment_parent_uri,omitempty"`
	JobURI              string   `json:"job_uri,omitempty"`
	Prompt              string   `json:"prompt,omitempty"`
	Workspace           string   `json:"workspace"`
	WorkspaceURI        string   `json:"workspace_uri,omitempty"`
	StateURI            string   `json:"state_uri,omitempty"`
	Runtime             string   `json:"runtime,omitempty"`
	RuntimeBinary       string   `json:"runtime_binary,omitempty"`
	Model               string   `json:"model,omitempty"`
	Effort              string   `json:"effort,omitempty"`
	Args                []string `json:"args,omitempty"`
	Env                 []string `json:"env,omitempty"`
	Stdin               string   `json:"stdin,omitempty"`
	CleanupPaths        []string `json:"cleanup_paths,omitempty"`
}

type DispatchResponse struct {
	InstanceID          string    `json:"instance_id"`
	URI                 string    `json:"uri,omitempty"`
	SpecURI             string    `json:"spec_uri,omitempty"`
	DeploymentURI       string    `json:"deployment_uri,omitempty"`
	DeploymentParentURI string    `json:"deployment_parent_uri,omitempty"`
	JobURI              string    `json:"job_uri,omitempty"`
	WorkspaceURI        string    `json:"workspace_uri,omitempty"`
	StateURI            string    `json:"state_uri,omitempty"`
	LogURI              string    `json:"log_uri,omitempty"`
	StartedAt           time.Time `json:"started_at"`
	PID                 int       `json:"pid"`
	Runtime             string    `json:"runtime,omitempty"`
	Model               string    `json:"model,omitempty"`
	Effort              string    `json:"effort,omitempty"`
	SessionID           string    `json:"session_id,omitempty"`
}

type MessageResponse struct {
	Delivered   bool      `json:"delivered"`
	Interrupted bool      `json:"interrupted,omitempty"`
	ID          string    `json:"id"`
	TS          time.Time `json:"ts"`
	Note        string    `json:"note,omitempty"`
}

type FeedbackDeliverResponse struct {
	Delivered     bool      `json:"delivered"`
	ID            string    `json:"id"`
	TS            time.Time `json:"ts"`
	ManagerPinged bool      `json:"manager_pinged,omitempty"`
	Note          string    `json:"note,omitempty"`
}

type ReconcileResponse struct {
	Reconciled bool               `json:"reconciled"`
	Changed    int                `json:"changed"`
	Instances  []*daemon.Metadata `json:"instances"`
	Changes    []ReconcileChange  `json:"changes"`
}

type RuntimeExtensionResponse struct {
	InstanceID       string           `json:"instance_id"`
	Metadata         *daemon.Metadata `json:"metadata"`
	ByMillis         int64            `json:"by_ms"`
	PreviousDeadline time.Time        `json:"previous_deadline"`
	NewDeadline      time.Time        `json:"new_deadline"`
	Actor            string           `json:"actor,omitempty"`
}

type DaemonStatus struct {
	Ready     bool           `json:"ready"`
	PID       int            `json:"pid,omitempty"`
	Instances int            `json:"instances"`
	TeamDir   string         `json:"team_dir,omitempty"`
	StartedAt time.Time      `json:"started_at,omitempty"`
	Build     buildinfo.Info `json:"build,omitempty"`
}

type Resource struct {
	URI      string          `json:"uri"`
	Kind     string          `json:"kind"`
	ID       string          `json:"id"`
	Fragment string          `json:"fragment,omitempty"`
	Data     json.RawMessage `json:"data"`
}

type ReconcileChange struct {
	Instance string        `json:"instance"`
	Agent    string        `json:"agent,omitempty"`
	Before   daemon.Status `json:"before"`
	After    daemon.Status `json:"after"`
	PID      int           `json:"pid,omitempty"`
}

func (c *Client) Dispatch(in DispatchInput) (*DispatchResponse, error) {
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
		return nil, responseError("dispatch", resp)
	}
	var out DispatchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: dispatch decode: %w", err)
	}
	return &out, nil
}

func (c *Client) Status() (*DaemonStatus, error) {
	resp, err := c.hc.Get(c.baseURL + "/v1/status")
	if err != nil {
		return nil, fmt.Errorf("daemon: status: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("status", resp)
	}
	var out DaemonStatus
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: status decode: %w", err)
	}
	return &out, nil
}

// Instance is the stable `/v1/instances` response DTO. It intentionally does
// not reuse daemon.Metadata, whose separate responsibility is persistence.
type InstanceStatus string

const (
	InstanceRunning InstanceStatus = "running"
	InstanceStopped InstanceStatus = "stopped"
	InstanceExited  InstanceStatus = "exited"
	InstanceCrashed InstanceStatus = "crashed"
)

type Instance struct {
	Instance            string          `json:"instance"`
	URI                 string          `json:"uri,omitempty"`
	SpecURI             string          `json:"spec_uri,omitempty"`
	DeploymentURI       string          `json:"deployment_uri,omitempty"`
	DeploymentParentURI string          `json:"deployment_parent_uri,omitempty"`
	Chartered           bool            `json:"chartered,omitempty"`
	CharterURI          string          `json:"charter_uri,omitempty"`
	CapabilityURI       string          `json:"capability_uri,omitempty"`
	Agent               string          `json:"agent"`
	Job                 string          `json:"job,omitempty"`
	JobURI              string          `json:"job_uri,omitempty"`
	Ticket              string          `json:"ticket,omitempty"`
	Branch              string          `json:"branch,omitempty"`
	PR                  string          `json:"pr,omitempty"`
	Origin              origin.Envelope `json:"origin,omitempty"`
	Runtime             string          `json:"runtime,omitempty"`
	RuntimeBinary       string          `json:"runtime_binary,omitempty"`
	Model               string          `json:"model,omitempty"`
	Effort              string          `json:"effort,omitempty"`
	EffectiveRuntime    string          `json:"effective_runtime,omitempty"`
	Workspace           string          `json:"workspace"`
	WorkspaceURI        string          `json:"workspace_uri,omitempty"`
	StateURI            string          `json:"state_uri,omitempty"`
	PID                 int             `json:"pid"`
	SessionID           string          `json:"session_id,omitempty"`
	StartedAt           time.Time       `json:"started_at"`
	RuntimeBudget       string          `json:"runtime_budget,omitempty"`
	RuntimeDeadline     time.Time       `json:"runtime_deadline,omitempty"`
	ResumeCount         int             `json:"resume_count,omitempty"`
	FreshFallback       bool            `json:"fresh_fallback,omitempty"`
	FreshFallbacks      int             `json:"fresh_fallback_count,omitempty"`
	StoppedAt           time.Time       `json:"stopped_at,omitempty"`
	ExitedAt            time.Time       `json:"exited_at,omitempty"`
	Status              InstanceStatus  `json:"status"`
	LogPath             string          `json:"log_path,omitempty"`
	LogURI              string          `json:"log_uri,omitempty"`
	ExitCode            *int            `json:"exit_code,omitempty"`
	Usage               *usage.Record   `json:"usage,omitempty"`
	Adopted             bool            `json:"adopted,omitempty"`
	RestartBackoffUntil time.Time       `json:"restart_backoff_until,omitempty"`
}

// Job is the stable, redacted `/v1/jobs` list-entry DTO.
type JobStatus string

const (
	JobQueued  JobStatus = "queued"
	JobRunning JobStatus = "running"
	JobBlocked JobStatus = "blocked"
	JobDone    JobStatus = "done"
	JobFailed  JobStatus = "failed"
)

type Job struct {
	ID                  string    `json:"id"`
	URI                 string    `json:"uri,omitempty"`
	OutcomeURI          string    `json:"outcome_uri,omitempty"`
	DeploymentURI       string    `json:"deployment_uri,omitempty"`
	DeploymentParentURI string    `json:"deployment_parent_uri,omitempty"`
	Ticket              string    `json:"ticket"`
	TicketURL           string    `json:"ticket_url,omitempty"`
	Epic                string    `json:"epic,omitempty"`
	Target              string    `json:"target"`
	ImplementationAgent string    `json:"implementation_agent,omitempty"`
	Instance            string    `json:"instance,omitempty"`
	InstanceURI         string    `json:"instance_uri,omitempty"`
	WorkspaceURI        string    `json:"workspace_uri,omitempty"`
	Pipeline            string    `json:"pipeline,omitempty"`
	Status              JobStatus `json:"status"`
	Held                bool      `json:"held,omitempty"`
	TokenBudget         int64     `json:"token_budget,omitempty"`
	TimeBudget          string    `json:"time_budget,omitempty"`
	Hard                bool      `json:"hard,omitempty"`
	HardMultiplier      float64   `json:"hard_multiplier,omitempty"`
	Branch              string    `json:"branch,omitempty"`
	PR                  string    `json:"pr,omitempty"`
	LastEvent           string    `json:"last_event,omitempty"`
	LastStatus          string    `json:"last_status,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

func (c *Client) Instances() ([]*Instance, error) {
	resp, err := c.hc.Get(c.baseURL + "/v1/instances")
	if err != nil {
		return nil, fmt.Errorf("daemon: instances: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("instances", resp)
	}
	var out []*Instance
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: instances decode: %w", err)
	}
	return out, nil
}

// Jobs returns the redacted durable job collection used by the current
// dashboard-parity surface.
func (c *Client) Jobs() ([]*Job, error) {
	resp, err := c.hc.Get(c.baseURL + "/v1/jobs")
	if err != nil {
		return nil, fmt.Errorf("daemon: jobs: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("jobs", resp)
	}
	var out []*Job
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: jobs decode: %w", err)
	}
	return out, nil
}

func (c *Client) Resource(uri string) (*Resource, error) {
	u := c.baseURL + "/v1/resources?" + url.Values{"uri": []string{uri}}.Encode()
	resp, err := c.hc.Get(u)
	if err != nil {
		return nil, fmt.Errorf("daemon: resource read: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("resource read", resp)
	}
	var out Resource
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: resource read decode: %w", err)
	}
	return &out, nil
}

func (c *Client) Reconcile() (*ReconcileResponse, error) {
	resp, err := c.hc.Post(c.baseURL+"/v1/reconcile", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("daemon: reconcile: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("reconcile", resp)
	}
	var out ReconcileResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: reconcile decode: %w", err)
	}
	return &out, nil
}

func (c *Client) Events(ctx context.Context, follow bool, tailLines int) (io.ReadCloser, error) {
	u := c.baseURL + "/v1/events"
	q := url.Values{}
	if follow {
		q.Set("follow", "true")
	}
	if tailLines > 0 {
		q.Set("tail", fmt.Sprintf("%d", tailLines))
	}
	if encoded := q.Encode(); encoded != "" {
		u += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return io.NopCloser(bytes.NewReader(nil)), nil
		}
		return nil, fmt.Errorf("daemon: events: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, responseError("events", resp)
	}
	return resp.Body, nil
}

func (c *Client) SendMessage(to, from, body, replyTo string) (*MessageResponse, error) {
	payload, err := json.Marshal(map[string]string{"to": to, "from": from, "body": body, "reply_to": replyTo})
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Post(c.baseURL+"/v1/message", "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("daemon: message: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("message", resp)
	}
	var out MessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: message decode: %w", err)
	}
	return &out, nil
}

func (c *Client) FeedbackDeliver(input feedback.DeliverInput) (*FeedbackDeliverResponse, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/v1/feedback/deliver", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if value := origin.HeaderValue(input.Origin); value != "" {
		req.Header.Set(origin.HeaderName, value)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon: feedback deliver: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("feedback deliver", resp)
	}
	var out FeedbackDeliverResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: feedback deliver decode: %w", err)
	}
	return &out, nil
}

func (c *Client) InterruptMessage(to, from, body, replyTo string, force bool) (*MessageResponse, error) {
	payload, err := json.Marshal(map[string]any{"to": to, "from": from, "body": body, "reply_to": replyTo, "force": force})
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Post(c.baseURL+"/v1/interrupt", "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("daemon: interrupt: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("interrupt", resp)
	}
	var out MessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: interrupt decode: %w", err)
	}
	return &out, nil
}

// LogsStream pipes the chunked text stream of the instance's child.log to w.
// follow=true keeps the connection open until ctx cancels. tailLines>0 limits
// the initial dump to the last N lines before following.
func (c *Client) LogsStream(ctx context.Context, w io.Writer, instance string, follow bool, tailLines int) error {
	u := c.baseURL + "/v1/logs/" + url.PathEscape(instance)
	q := url.Values{}
	if follow {
		q.Set("follow", "true")
	}
	if tailLines > 0 {
		q.Set("tail", fmt.Sprintf("%d", tailLines))
	}
	if encoded := q.Encode(); encoded != "" {
		u += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		// Context cancel during read shows up as a wrapped error — surface
		// nil so callers can treat user Ctrl-C as a clean exit.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		return fmt.Errorf("daemon: logs: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return &LogNotFoundError{Instance: instance}
	}
	if resp.StatusCode != http.StatusOK {
		return responseError("logs", resp)
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		return err
	}
	return nil
}

// ResponseError is returned for a non-successful daemon HTTP response. Its
// Error text preserves the CLI's historical message while StatusCode and Body
// give typed consumers enough information to distinguish 401, 403, 503, and
// other daemon failures without parsing text.
type ResponseError struct {
	Operation  string
	StatusCode int
	Body       string
}

func (e *ResponseError) Error() string {
	return fmt.Sprintf("daemon: %s: %s", e.Operation, e.Body)
}

func responseError(operation string, resp *http.Response) error {
	b, _ := io.ReadAll(resp.Body)
	return &ResponseError{
		Operation:  operation,
		StatusCode: resp.StatusCode,
		Body:       string(bytes.TrimSpace(b)),
	}
}

// --- channel client helpers ----------------------------------------------

// ChannelInfo mirrors daemon.ChannelInfo on the wire.
type ChannelInfo struct {
	Name          string    `json:"name"`
	Subscribers   int       `json:"subscribers"`
	MessageCount  int64     `json:"message_count"`
	LastMessageTS time.Time `json:"last_message_ts"`
}

// ChannelMessage mirrors daemon.ChannelMessage on the wire.
type ChannelMessage struct {
	Seq    int64     `json:"seq"`
	Sender string    `json:"sender"`
	Body   string    `json:"body"`
	TS     time.Time `json:"ts"`
}

// ChannelPublishResponse / ChannelSubscribeResponse / ChannelDrainResponse mirror the JSON the daemon returns.
type ChannelPublishResponse struct {
	Seq int64     `json:"seq"`
	TS  time.Time `json:"ts"`
}

type ChannelSubscribeResponse struct {
	OK         bool  `json:"ok"`
	Cursor     int64 `json:"cursor"`
	Subscribed bool  `json:"subscribed"`
}

type ChannelUnsubscribeResponse struct {
	OK           bool `json:"ok"`
	Unsubscribed bool `json:"unsubscribed"`
}

type ChannelDrainResponse struct {
	Messages []*ChannelMessage `json:"messages"`
	Cursor   int64             `json:"cursor"`
}

// channelURL builds the path for a channel-scoped endpoint, URL-encoding the
// `#` so the daemon's path parser receives `%23name`.
func (c *Client) channelURL(name, verb string) string {
	enc := url.PathEscape(name)
	if verb == "" {
		return c.baseURL + "/v1/channel/" + enc
	}
	return c.baseURL + "/v1/channel/" + enc + "/" + verb
}

func (c *Client) ChannelPublish(name, sender, body string) (*ChannelPublishResponse, error) {
	payload, _ := json.Marshal(map[string]string{"sender": sender, "body": body})
	resp, err := c.hc.Post(c.channelURL(name, "publish"), "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("daemon: channel publish: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("channel publish", resp)
	}
	var out ChannelPublishResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: channel publish decode: %w", err)
	}
	return &out, nil
}

func (c *Client) ChannelSubscribe(name, instance string) (*ChannelSubscribeResponse, error) {
	payload, _ := json.Marshal(map[string]string{"instance": instance})
	resp, err := c.hc.Post(c.channelURL(name, "subscribe"), "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("daemon: channel subscribe: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("channel subscribe", resp)
	}
	var out ChannelSubscribeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: channel subscribe decode: %w", err)
	}
	return &out, nil
}

func (c *Client) ChannelUnsubscribe(name, instance string) (*ChannelUnsubscribeResponse, error) {
	payload, _ := json.Marshal(map[string]string{"instance": instance})
	resp, err := c.hc.Post(c.channelURL(name, "unsubscribe"), "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("daemon: channel unsubscribe: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("channel unsubscribe", resp)
	}
	var out ChannelUnsubscribeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: channel unsubscribe decode: %w", err)
	}
	return &out, nil
}

func (c *Client) ChannelAck(name, instance string, cursor int64) error {
	payload, _ := json.Marshal(map[string]any{"instance": instance, "cursor": cursor})
	resp, err := c.hc.Post(c.channelURL(name, "ack"), "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("daemon: channel ack: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return responseError("channel ack", resp)
	}
	return nil
}

// ChannelDrain calls GET /v1/channel/{name}/messages. Pass since=nil to use
// the subscriber's stored cursor; pass wait=0 for non-blocking. Cancel via
// ctx for early termination of long-polls.
func (c *Client) ChannelDrain(ctx context.Context, name, instance string, since *int64, wait time.Duration) (*ChannelDrainResponse, error) {
	q := url.Values{}
	q.Set("instance", instance)
	if since != nil {
		q.Set("since", fmt.Sprintf("%d", *since))
	}
	if wait > 0 {
		q.Set("wait", wait.String())
	}
	u := c.channelURL(name, "messages") + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon: channel drain: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("channel drain", resp)
	}
	var out ChannelDrainResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: channel drain decode: %w", err)
	}
	return &out, nil
}

func (c *Client) ChannelList() ([]*ChannelInfo, error) {
	resp, err := c.hc.Get(c.baseURL + "/v1/channels")
	if err != nil {
		return nil, fmt.Errorf("daemon: channels: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("channels", resp)
	}
	var out []*ChannelInfo
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: channels decode: %w", err)
	}
	return out, nil
}

// --- topology / event client helpers ------------------------------------

// eventOutcome mirrors the matched/dispatched/queued/messaged/blocked/rejected lists
// returned by POST /v1/event.
type EventResponse struct {
	Matched    []string                 `json:"matched"`
	Dispatched []map[string]interface{} `json:"dispatched"`
	Queued     []string                 `json:"queued"`
	Messaged   []string                 `json:"messaged"`
	Noop       []map[string]interface{} `json:"noop,omitempty"`
	Blocked    []map[string]interface{} `json:"blocked"`
	Rejected   []map[string]interface{} `json:"rejected"`
	Outcomes   []daemon.EventOutcome    `json:"outcomes,omitempty"`
	Trace      *topology.EventTrace     `json:"trace,omitempty"`
}

// PublishEvent posts an event to the daemon's resolver and returns the
// per-instance outcomes. payload may be nil — the daemon treats absent
// payload as an empty match scope (matches triggers with an empty `match`
// table).
func (c *Client) PublishEvent(eventType string, payload map[string]any) (*EventResponse, error) {
	return c.PublishEventWithTrace(eventType, payload, false)
}

func (c *Client) PublishEventWithTrace(eventType string, payload map[string]any, trace bool) (*EventResponse, error) {
	// Omit `trace` unless requested so newer CLIs stay publishable against
	// older daemons whose decoders reject unknown fields (SQU-55).
	req := map[string]any{"type": eventType, "payload": payload}
	if trace {
		req["trace"] = true
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Post(c.baseURL+"/v1/event", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("daemon: event: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("event", resp)
	}
	var out EventResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: event decode: %w", err)
	}
	return &out, nil
}

func (c *Client) QueueItems() ([]*daemon.QueueItem, error) {
	resp, err := c.hc.Get(c.baseURL + "/v1/queue")
	if err != nil {
		return nil, fmt.Errorf("daemon: queue: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("queue", resp)
	}
	var out []*daemon.QueueItem
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: queue decode: %w", err)
	}
	return out, nil
}

func (c *Client) Locks() ([]daemon.LockSnapshot, error) {
	resp, err := c.hc.Get(c.baseURL + "/v1/locks")
	if err != nil {
		return nil, fmt.Errorf("daemon: locks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("locks", resp)
	}
	var out []daemon.LockSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: locks decode: %w", err)
	}
	return out, nil
}

func (c *Client) QueueItem(id string) (*daemon.QueueItem, error) {
	resp, err := c.hc.Get(c.baseURL + "/v1/queue/" + url.PathEscape(id))
	if err != nil {
		return nil, fmt.Errorf("daemon: queue show: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("queue show", resp)
	}
	var out daemon.QueueItem
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: queue show decode: %w", err)
	}
	return &out, nil
}

func (c *Client) QueueDrop(id string) error {
	resp, err := c.hc.Post(c.baseURL+"/v1/queue/"+url.PathEscape(id)+"/drop", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return fmt.Errorf("daemon: queue drop: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return responseError("queue drop", resp)
	}
	return nil
}

func (c *Client) QueueRetry(id string) (*daemon.EventOutcome, error) {
	resp, err := c.hc.Post(c.baseURL+"/v1/queue/"+url.PathEscape(id)+"/retry", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("daemon: queue retry: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("queue retry", resp)
	}
	var out daemon.EventOutcome
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: queue retry decode: %w", err)
	}
	return &out, nil
}

func (c *Client) QueueDrain(dryRun bool) (*daemon.QueueDrainResult, error) {
	return c.queueDrain(dryRun, nil, false)
}

func (c *Client) OutboxDrain(dryRun bool) (*daemon.OutboxDrainResult, error) {
	u := daemonQueryURL(c.baseURL+"/v1/outbox/drain", dryRun, "", nil, false)
	resp, err := c.hc.Post(u, "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("daemon: outbox drain: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("outbox drain", resp)
	}
	var out daemon.OutboxDrainResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: outbox drain decode: %w", err)
	}
	return &out, nil
}

func (c *Client) QueueDrainScoped(dryRun bool, ids []string) (*daemon.QueueDrainResult, error) {
	return c.queueDrain(dryRun, ids, true)
}

func (c *Client) queueDrain(dryRun bool, ids []string, scoped bool) (*daemon.QueueDrainResult, error) {
	u := daemonQueryURL(c.baseURL+"/v1/queue/drain", dryRun, "id", ids, scoped)
	resp, err := c.hc.Post(u, "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("daemon: queue drain: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("queue drain", resp)
	}
	var out daemon.QueueDrainResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: queue drain decode: %w", err)
	}
	return &out, nil
}

func (c *Client) ScheduleFire(dryRun bool) (*daemon.ScheduleFireResult, error) {
	return c.scheduleFire(dryRun, nil, false)
}

func (c *Client) ScheduleFireScoped(dryRun bool, names []string) (*daemon.ScheduleFireResult, error) {
	return c.scheduleFire(dryRun, names, true)
}

func (c *Client) ManagerWakeSweep(dryRun bool) (*daemon.ManagerWakeSweepResult, error) {
	u := daemonQueryURL(c.baseURL+"/v1/manager-wake/sweep", dryRun, "", nil, false)
	resp, err := c.hc.Post(u, "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("daemon: manager wake sweep: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("manager wake sweep", resp)
	}
	var out daemon.ManagerWakeSweepResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: manager wake sweep decode: %w", err)
	}
	return &out, nil
}

func (c *Client) scheduleFire(dryRun bool, names []string, scoped bool) (*daemon.ScheduleFireResult, error) {
	u := daemonQueryURL(c.baseURL+"/v1/schedules/fire", dryRun, "name", names, scoped)
	resp, err := c.hc.Post(u, "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("daemon: schedules fire: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("schedules fire", resp)
	}
	var out daemon.ScheduleFireResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: schedules fire decode: %w", err)
	}
	return &out, nil
}

func daemonQueryURL(base string, dryRun bool, scopedKey string, scopedValues []string, scoped bool) string {
	values := url.Values{}
	if dryRun {
		values.Set("dry_run", "true")
	}
	if scoped {
		if len(scopedValues) == 0 {
			values.Add(scopedKey, "")
		} else {
			for _, value := range scopedValues {
				if value != "" {
					values.Add(scopedKey, value)
				}
			}
		}
	}
	if encoded := values.Encode(); encoded != "" {
		return base + "?" + encoded
	}
	return base
}

// Topology mirrors the wire shape of /v1/topology.
type Topology struct {
	Instances            []TopologyInstance `json:"instances"`
	Pipelines            []TopologyPipeline `json:"pipelines"`
	Schedules            []TopologySchedule `json:"schedules"`
	Teams                []TopologyTeam     `json:"teams"`
	Budgets              []TopologyBudget   `json:"budgets"`
	BudgetReminderLevels []int              `json:"budget_reminder_levels,omitempty"`
}

type TopologyInstance struct {
	Name         string            `json:"name"`
	Agent        string            `json:"agent"`
	Ephemeral    bool              `json:"ephemeral"`
	Description  string            `json:"description"`
	Replicas     int               `json:"replicas"`
	ReapWorktree string            `json:"reap_worktree"`
	Config       map[string]any    `json:"config"`
	Triggers     []TopologyTrigger `json:"triggers"`
	Running      int               `json:"running"`
	Queued       int               `json:"queued"`
}

type TopologyPipeline struct {
	Name                string                 `json:"name"`
	Trigger             *TopologyTrigger       `json:"trigger"`
	Steps               []TopologyPipelineStep `json:"steps"`
	AutoAdvance         bool                   `json:"auto_advance"`
	ReapWorktree        string                 `json:"reap_worktree"`
	RedispatchOnReentry bool                   `json:"redispatch_on_reentry,omitempty"`
	Merge               *TopologyPipelineMerge `json:"merge,omitempty"`
}

type TopologyTrigger struct {
	Event string         `json:"event"`
	Match map[string]any `json:"match"`
}

type TopologyPipelineStep struct {
	ID               string   `json:"id"`
	Target           string   `json:"target"`
	After            []string `json:"after"`
	Label            string   `json:"label,omitempty"`
	Description      string   `json:"description,omitempty"`
	Instructions     string   `json:"instructions,omitempty"`
	Gate             string   `json:"gate,omitempty"`
	ApprovalRequired bool     `json:"approval_required,omitempty"`
	Optional         bool     `json:"optional,omitempty"`
	Timeout          string   `json:"timeout,omitempty"`
	TokenBudget      int64    `json:"token_budget,omitempty"`
	TimeBudget       string   `json:"time_budget,omitempty"`
	Hard             bool     `json:"hard,omitempty"`
	HardMultiplier   float64  `json:"hard_multiplier,omitempty"`
	ReminderLevels   []int    `json:"reminder_levels,omitempty"`
	MaxAttempts      int      `json:"max_attempts,omitempty"`
	RetryOnCrash     bool     `json:"retry_on_crash,omitempty"`
}

type TopologyPipelineMerge struct {
	Strategy   string   `json:"strategy"`
	Script     string   `json:"script,omitempty"`
	Land       string   `json:"land,omitempty"`
	OwnedPaths []string `json:"owned_paths,omitempty"`
}

type TopologySchedule struct {
	Name        string         `json:"name"`
	StateName   string         `json:"state_name,omitempty"`
	Every       string         `json:"every"`
	RunOnStart  bool           `json:"run_on_start"`
	Payload     map[string]any `json:"payload"`
	Team        string         `json:"team,omitempty"`
	LastSeenAt  *time.Time     `json:"last_seen_at,omitempty"`
	LastFiredAt *time.Time     `json:"last_fired_at,omitempty"`
}

type TopologyTeam struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Instances   []string `json:"instances"`
	Pipelines   []string `json:"pipelines"`
	Schedules   []string `json:"schedules"`
	Channels    []string `json:"channels"`
}

type TopologyBudget struct {
	Team         string  `json:"team"`
	TokensPerDay int64   `json:"tokens_per_day"`
	JobsInFlight int     `json:"jobs_in_flight"`
	Allocation   string  `json:"allocation"`
	LoadWeight   float64 `json:"load_weight"`
}

func (c *Client) Topology() (*Topology, error) {
	resp, err := c.hc.Get(c.baseURL + "/v1/topology")
	if err != nil {
		return nil, fmt.Errorf("daemon: topology: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("topology", resp)
	}
	var out Topology
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: topology decode: %w", err)
	}
	return &out, nil
}

func (c *Client) TopologyReload() (*Topology, error) {
	resp, err := c.hc.Post(c.baseURL+"/v1/topology/reload", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("daemon: topology reload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("topology reload", resp)
	}
	var out Topology
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: topology reload decode: %w", err)
	}
	return &out, nil
}

// StopInstance hits POST /v1/stop. Used by `instance down`.
func (c *Client) StopInstance(instance string) error {
	return c.StopInstanceWithOptions(instance, false, 0)
}

func (c *Client) StopInstanceWithOptions(instance string, force bool, timeout time.Duration) error {
	payload := map[string]any{"instance": instance}
	if force {
		payload["force"] = true
	}
	if timeout > 0 {
		payload["timeout_ms"] = timeout.Milliseconds()
	}
	body, _ := json.Marshal(payload)
	resp, err := c.hc.Post(c.baseURL+"/v1/stop", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("daemon: stop: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return responseError("stop", resp)
	}
	return nil
}

func (c *Client) ExtendInstance(instance string, by time.Duration, actor string) (*RuntimeExtensionResponse, error) {
	payload := map[string]any{
		"instance": instance,
		"by_ms":    by.Milliseconds(),
	}
	if actor != "" {
		payload["actor"] = actor
	}
	body, _ := json.Marshal(payload)
	resp, err := c.hc.Post(c.baseURL+"/v1/extend", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("daemon: extend: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("extend", resp)
	}
	var out RuntimeExtensionResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: extend decode: %w", err)
	}
	return &out, nil
}

// StartInstance hits POST /v1/start to resume a previously-stopped persistent
// instance via `claude --resume <session-id>`. Used by `start` / `instance up`
// and by interactive `attach` to re-adopt the instance after the user exits.
func (c *Client) StartInstance(instance string) error {
	return c.StartInstanceWithOptions(instance, false, false)
}

func (c *Client) StartInstanceWithOptions(instance string, fresh bool, force bool) error {
	payload := map[string]any{"instance": instance}
	if fresh {
		payload["fresh"] = true
	}
	if force {
		payload["force"] = true
	}
	body, _ := json.Marshal(payload)
	resp, err := c.hc.Post(c.baseURL+"/v1/start", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("daemon: start: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return responseError("start", resp)
	}
	return nil
}

// RestartInstance hits POST /v1/restart.
func (c *Client) RestartInstance(instance string) error {
	return c.RestartInstanceWithOptions(instance, false, 0)
}

func (c *Client) RestartInstanceWithOptions(instance string, force bool, timeout time.Duration) error {
	payload := map[string]any{"instance": instance}
	if force {
		payload["force"] = true
	}
	if timeout > 0 {
		payload["timeout_ms"] = timeout.Milliseconds()
	}
	body, _ := json.Marshal(payload)
	resp, err := c.hc.Post(c.baseURL+"/v1/restart", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("daemon: restart: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return responseError("restart", resp)
	}
	return nil
}

// RemoveInstance hits POST /v1/remove. With force=true, the daemon stops a
// running process before deleting its metadata.
func (c *Client) RemoveInstance(instance string, force bool) error {
	body, _ := json.Marshal(map[string]any{"instance": instance, "force": force})
	resp, err := c.hc.Post(c.baseURL+"/v1/remove", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("daemon: remove: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return responseError("remove", resp)
	}
	return nil
}

func (c *Client) ChannelDelete(name string) error {
	req, err := http.NewRequest(http.MethodDelete, c.channelURL(name, ""), nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("daemon: channel delete: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("no such channel %q", name)
	}
	if resp.StatusCode != http.StatusOK {
		return responseError("channel delete", resp)
	}
	return nil
}
