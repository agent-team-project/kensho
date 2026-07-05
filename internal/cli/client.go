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
	"strings"
	"time"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/feedback"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/topology"
)

// errDaemonNotRunning is returned by NewDaemonClient when no daemon is alive
// for the target repo. Callers can use errors.Is to choose between routing
// through the daemon and falling back to direct exec.
var errDaemonNotRunning = errors.New("daemon: not running")

type logNotFoundError struct {
	Instance string
}

func (e *logNotFoundError) Error() string {
	return fmt.Sprintf("no log for instance %q (has it been dispatched?)", e.Instance)
}

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
	return newDaemonClientWithTimeout(teamDir, 0)
}

func newDaemonClientWithTimeout(teamDir string, timeout time.Duration) (*daemonClient, error) {
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
		hc:      newDaemonHTTPClient(transport, timeout),
		baseURL: "http://daemon", // host name is irrelevant — DialContext fixes the socket.
		teamDir: teamDir,
	}, nil
}

func newDaemonHTTPClient(base http.RoundTripper, timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: daemonBuildHeaderTransport{
			base:  base,
			build: BuildInfo(),
		},
		Timeout: timeout,
	}
}

type daemonBuildHeaderTransport struct {
	base  http.RoundTripper
	build buildinfo.Info
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
	if value := daemonOriginHeaderFromEnv(t.build); value != "" && req.Header.Get(origin.HeaderName) == "" {
		req = req.Clone(req.Context())
		req.Header.Set(origin.HeaderName, value)
	}
	return base.RoundTrip(req)
}

func daemonOriginHeaderFromEnv(build buildinfo.Info) string {
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

// dispatchPayload mirrors POST /v1/dispatch's body.
type dispatchPayload struct {
	Agent         string   `json:"agent"`
	Name          string   `json:"name"`
	Prompt        string   `json:"prompt,omitempty"`
	Workspace     string   `json:"workspace"`
	Runtime       string   `json:"runtime,omitempty"`
	RuntimeBinary string   `json:"runtime_binary,omitempty"`
	Args          []string `json:"args,omitempty"`
	Env           []string `json:"env,omitempty"`
	Stdin         string   `json:"stdin,omitempty"`
}

type dispatchResponse struct {
	InstanceID string    `json:"instance_id"`
	StartedAt  time.Time `json:"started_at"`
	PID        int       `json:"pid"`
	Runtime    string    `json:"runtime,omitempty"`
	SessionID  string    `json:"session_id,omitempty"`
}

type messageResponse struct {
	Delivered   bool      `json:"delivered"`
	Interrupted bool      `json:"interrupted,omitempty"`
	ID          string    `json:"id"`
	TS          time.Time `json:"ts"`
	Note        string    `json:"note,omitempty"`
}

type feedbackDeliverResponse struct {
	Delivered     bool      `json:"delivered"`
	ID            string    `json:"id"`
	TS            time.Time `json:"ts"`
	ManagerPinged bool      `json:"manager_pinged,omitempty"`
	Note          string    `json:"note,omitempty"`
}

type daemonReconcileResponse struct {
	Reconciled bool                    `json:"reconciled"`
	Changed    int                     `json:"changed"`
	Instances  []*daemon.Metadata      `json:"instances"`
	Changes    []daemonReconcileChange `json:"changes"`
}

type runtimeExtensionResponse struct {
	InstanceID       string           `json:"instance_id"`
	Metadata         *daemon.Metadata `json:"metadata"`
	ByMillis         int64            `json:"by_ms"`
	PreviousDeadline time.Time        `json:"previous_deadline"`
	NewDeadline      time.Time        `json:"new_deadline"`
	Actor            string           `json:"actor,omitempty"`
}

type daemonAPIStatus struct {
	Ready     bool           `json:"ready"`
	PID       int            `json:"pid,omitempty"`
	Instances int            `json:"instances"`
	TeamDir   string         `json:"team_dir,omitempty"`
	StartedAt time.Time      `json:"started_at,omitempty"`
	Build     buildinfo.Info `json:"build,omitempty"`
}

type daemonReconcileChange struct {
	Instance string        `json:"instance"`
	Agent    string        `json:"agent,omitempty"`
	Before   daemon.Status `json:"before"`
	After    daemon.Status `json:"after"`
	PID      int           `json:"pid,omitempty"`
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

func (c *daemonClient) Status() (*daemonAPIStatus, error) {
	resp, err := c.hc.Get(c.baseURL + "/v1/status")
	if err != nil {
		return nil, fmt.Errorf("daemon: status: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon: status: %s", readErrorBody(resp))
	}
	var out daemonAPIStatus
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: status decode: %w", err)
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

func (c *daemonClient) Reconcile() (*daemonReconcileResponse, error) {
	resp, err := c.hc.Post(c.baseURL+"/v1/reconcile", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("daemon: reconcile: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon: reconcile: %s", readErrorBody(resp))
	}
	var out daemonReconcileResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: reconcile decode: %w", err)
	}
	return &out, nil
}

func (c *daemonClient) Events(ctx context.Context, follow bool, tailLines int) (io.ReadCloser, error) {
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
		return nil, fmt.Errorf("daemon: events: %s", readErrorBody(resp))
	}
	return resp.Body, nil
}

func (c *daemonClient) SendMessage(to, from, body string) (*messageResponse, error) {
	payload, err := json.Marshal(map[string]string{"to": to, "from": from, "body": body})
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Post(c.baseURL+"/v1/message", "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("daemon: message: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon: message: %s", readErrorBody(resp))
	}
	var out messageResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: message decode: %w", err)
	}
	return &out, nil
}

func (c *daemonClient) FeedbackDeliver(input feedback.DeliverInput) (*feedbackDeliverResponse, error) {
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
		return nil, fmt.Errorf("daemon: feedback deliver: %s", readErrorBody(resp))
	}
	var out feedbackDeliverResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: feedback deliver decode: %w", err)
	}
	return &out, nil
}

func (c *daemonClient) InterruptMessage(to, from, body string, force bool) (*messageResponse, error) {
	payload, err := json.Marshal(map[string]any{"to": to, "from": from, "body": body, "force": force})
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Post(c.baseURL+"/v1/interrupt", "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("daemon: interrupt: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon: interrupt: %s", readErrorBody(resp))
	}
	var out messageResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: interrupt decode: %w", err)
	}
	return &out, nil
}

// LogsStream pipes the chunked text stream of the instance's child.log to w.
// follow=true keeps the connection open until ctx cancels. tailLines>0 limits
// the initial dump to the last N lines before following.
func (c *daemonClient) LogsStream(ctx context.Context, w io.Writer, instance string, follow bool, tailLines int) error {
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
		return &logNotFoundError{Instance: instance}
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon: logs: %s", readErrorBody(resp))
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
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

// --- channel client helpers ----------------------------------------------

// channelInfo mirrors daemon.ChannelInfo on the wire.
type channelInfo struct {
	Name          string    `json:"name"`
	Subscribers   int       `json:"subscribers"`
	MessageCount  int64     `json:"message_count"`
	LastMessageTS time.Time `json:"last_message_ts"`
}

// channelMessage mirrors daemon.ChannelMessage on the wire.
type channelMessage struct {
	Seq    int64     `json:"seq"`
	Sender string    `json:"sender"`
	Body   string    `json:"body"`
	TS     time.Time `json:"ts"`
}

// publishResp / subscribeResp / drainResp mirror the JSON the daemon returns.
type publishResp struct {
	Seq int64     `json:"seq"`
	TS  time.Time `json:"ts"`
}

type subscribeResp struct {
	OK         bool  `json:"ok"`
	Cursor     int64 `json:"cursor"`
	Subscribed bool  `json:"subscribed"`
}

type unsubscribeResp struct {
	OK           bool `json:"ok"`
	Unsubscribed bool `json:"unsubscribed"`
}

type drainResp struct {
	Messages []*channelMessage `json:"messages"`
	Cursor   int64             `json:"cursor"`
}

// channelURL builds the path for a channel-scoped endpoint, URL-encoding the
// `#` so the daemon's path parser receives `%23name`.
func (c *daemonClient) channelURL(name, verb string) string {
	enc := url.PathEscape(name)
	if verb == "" {
		return c.baseURL + "/v1/channel/" + enc
	}
	return c.baseURL + "/v1/channel/" + enc + "/" + verb
}

func (c *daemonClient) ChannelPublish(name, sender, body string) (*publishResp, error) {
	payload, _ := json.Marshal(map[string]string{"sender": sender, "body": body})
	resp, err := c.hc.Post(c.channelURL(name, "publish"), "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("daemon: channel publish: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon: channel publish: %s", readErrorBody(resp))
	}
	var out publishResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: channel publish decode: %w", err)
	}
	return &out, nil
}

func (c *daemonClient) ChannelSubscribe(name, instance string) (*subscribeResp, error) {
	payload, _ := json.Marshal(map[string]string{"instance": instance})
	resp, err := c.hc.Post(c.channelURL(name, "subscribe"), "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("daemon: channel subscribe: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon: channel subscribe: %s", readErrorBody(resp))
	}
	var out subscribeResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: channel subscribe decode: %w", err)
	}
	return &out, nil
}

func (c *daemonClient) ChannelUnsubscribe(name, instance string) (*unsubscribeResp, error) {
	payload, _ := json.Marshal(map[string]string{"instance": instance})
	resp, err := c.hc.Post(c.channelURL(name, "unsubscribe"), "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("daemon: channel unsubscribe: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon: channel unsubscribe: %s", readErrorBody(resp))
	}
	var out unsubscribeResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: channel unsubscribe decode: %w", err)
	}
	return &out, nil
}

func (c *daemonClient) ChannelAck(name, instance string, cursor int64) error {
	payload, _ := json.Marshal(map[string]any{"instance": instance, "cursor": cursor})
	resp, err := c.hc.Post(c.channelURL(name, "ack"), "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("daemon: channel ack: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon: channel ack: %s", readErrorBody(resp))
	}
	return nil
}

// ChannelDrain calls GET /v1/channel/{name}/messages. Pass since=nil to use
// the subscriber's stored cursor; pass wait=0 for non-blocking. Cancel via
// ctx for early termination of long-polls.
func (c *daemonClient) ChannelDrain(ctx context.Context, name, instance string, since *int64, wait time.Duration) (*drainResp, error) {
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
		return nil, fmt.Errorf("daemon: channel drain: %s", readErrorBody(resp))
	}
	var out drainResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: channel drain decode: %w", err)
	}
	return &out, nil
}

func (c *daemonClient) ChannelList() ([]*channelInfo, error) {
	resp, err := c.hc.Get(c.baseURL + "/v1/channels")
	if err != nil {
		return nil, fmt.Errorf("daemon: channels: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon: channels: %s", readErrorBody(resp))
	}
	var out []*channelInfo
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: channels decode: %w", err)
	}
	return out, nil
}

// --- topology / event client helpers ------------------------------------

// eventOutcome mirrors the matched/dispatched/queued/messaged/blocked/rejected lists
// returned by POST /v1/event.
type eventResponse struct {
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
func (c *daemonClient) PublishEvent(eventType string, payload map[string]any) (*eventResponse, error) {
	return c.PublishEventWithTrace(eventType, payload, false)
}

func (c *daemonClient) PublishEventWithTrace(eventType string, payload map[string]any, trace bool) (*eventResponse, error) {
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
		return nil, fmt.Errorf("daemon: event: %s", readErrorBody(resp))
	}
	var out eventResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: event decode: %w", err)
	}
	return &out, nil
}

func (c *daemonClient) QueueItems() ([]*daemon.QueueItem, error) {
	resp, err := c.hc.Get(c.baseURL + "/v1/queue")
	if err != nil {
		return nil, fmt.Errorf("daemon: queue: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon: queue: %s", readErrorBody(resp))
	}
	var out []*daemon.QueueItem
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: queue decode: %w", err)
	}
	return out, nil
}

func (c *daemonClient) Locks() ([]daemon.LockSnapshot, error) {
	resp, err := c.hc.Get(c.baseURL + "/v1/locks")
	if err != nil {
		return nil, fmt.Errorf("daemon: locks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon: locks: %s", readErrorBody(resp))
	}
	var out []daemon.LockSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: locks decode: %w", err)
	}
	return out, nil
}

func (c *daemonClient) QueueItem(id string) (*daemon.QueueItem, error) {
	resp, err := c.hc.Get(c.baseURL + "/v1/queue/" + url.PathEscape(id))
	if err != nil {
		return nil, fmt.Errorf("daemon: queue show: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon: queue show: %s", readErrorBody(resp))
	}
	var out daemon.QueueItem
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: queue show decode: %w", err)
	}
	return &out, nil
}

func (c *daemonClient) QueueDrop(id string) error {
	resp, err := c.hc.Post(c.baseURL+"/v1/queue/"+url.PathEscape(id)+"/drop", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return fmt.Errorf("daemon: queue drop: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon: queue drop: %s", readErrorBody(resp))
	}
	return nil
}

func (c *daemonClient) QueueRetry(id string) (*daemon.EventOutcome, error) {
	resp, err := c.hc.Post(c.baseURL+"/v1/queue/"+url.PathEscape(id)+"/retry", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("daemon: queue retry: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon: queue retry: %s", readErrorBody(resp))
	}
	var out daemon.EventOutcome
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: queue retry decode: %w", err)
	}
	return &out, nil
}

func (c *daemonClient) QueueDrain(dryRun bool) (*daemon.QueueDrainResult, error) {
	return c.queueDrain(dryRun, nil, false)
}

func (c *daemonClient) OutboxDrain(dryRun bool) (*daemon.OutboxDrainResult, error) {
	u := daemonQueryURL(c.baseURL+"/v1/outbox/drain", dryRun, "", nil, false)
	resp, err := c.hc.Post(u, "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("daemon: outbox drain: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon: outbox drain: %s", readErrorBody(resp))
	}
	var out daemon.OutboxDrainResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: outbox drain decode: %w", err)
	}
	return &out, nil
}

func (c *daemonClient) QueueDrainScoped(dryRun bool, ids []string) (*daemon.QueueDrainResult, error) {
	return c.queueDrain(dryRun, ids, true)
}

func (c *daemonClient) queueDrain(dryRun bool, ids []string, scoped bool) (*daemon.QueueDrainResult, error) {
	u := daemonQueryURL(c.baseURL+"/v1/queue/drain", dryRun, "id", ids, scoped)
	resp, err := c.hc.Post(u, "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("daemon: queue drain: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon: queue drain: %s", readErrorBody(resp))
	}
	var out daemon.QueueDrainResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: queue drain decode: %w", err)
	}
	return &out, nil
}

func (c *daemonClient) ScheduleFire(dryRun bool) (*daemon.ScheduleFireResult, error) {
	return c.scheduleFire(dryRun, nil, false)
}

func (c *daemonClient) ScheduleFireScoped(dryRun bool, names []string) (*daemon.ScheduleFireResult, error) {
	return c.scheduleFire(dryRun, names, true)
}

func (c *daemonClient) scheduleFire(dryRun bool, names []string, scoped bool) (*daemon.ScheduleFireResult, error) {
	u := daemonQueryURL(c.baseURL+"/v1/schedules/fire", dryRun, "name", names, scoped)
	resp, err := c.hc.Post(u, "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("daemon: schedules fire: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon: schedules fire: %s", readErrorBody(resp))
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

// topologyResponse mirrors the wire shape of /v1/topology.
type topologyResponse struct {
	Instances []topologyInstance `json:"instances"`
	Pipelines []topologyPipeline `json:"pipelines"`
	Schedules []topologySchedule `json:"schedules"`
}

type topologyInstance struct {
	Name         string                   `json:"name"`
	Agent        string                   `json:"agent"`
	Ephemeral    bool                     `json:"ephemeral"`
	Description  string                   `json:"description"`
	Replicas     int                      `json:"replicas"`
	ReapWorktree string                   `json:"reap_worktree"`
	Config       map[string]interface{}   `json:"config"`
	Triggers     []map[string]interface{} `json:"triggers"`
	Running      int                      `json:"running"`
	Queued       int                      `json:"queued"`
}

type topologyPipeline struct {
	Name         string                   `json:"name"`
	Trigger      map[string]interface{}   `json:"trigger"`
	Steps        []map[string]interface{} `json:"steps"`
	ReapWorktree string                   `json:"reap_worktree"`
}

type topologySchedule struct {
	Name       string                 `json:"name"`
	Every      string                 `json:"every"`
	RunOnStart bool                   `json:"run_on_start"`
	Payload    map[string]interface{} `json:"payload"`
}

func (c *daemonClient) Topology() (*topologyResponse, error) {
	resp, err := c.hc.Get(c.baseURL + "/v1/topology")
	if err != nil {
		return nil, fmt.Errorf("daemon: topology: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon: topology: %s", readErrorBody(resp))
	}
	var out topologyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: topology decode: %w", err)
	}
	return &out, nil
}

func (c *daemonClient) TopologyReload() (*topologyResponse, error) {
	resp, err := c.hc.Post(c.baseURL+"/v1/topology/reload", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("daemon: topology reload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon: topology reload: %s", readErrorBody(resp))
	}
	var out topologyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: topology reload decode: %w", err)
	}
	return &out, nil
}

// StopInstance hits POST /v1/stop. Used by `instance down`.
func (c *daemonClient) StopInstance(instance string) error {
	return c.StopInstanceWithOptions(instance, false, 0)
}

func (c *daemonClient) StopInstanceWithOptions(instance string, force bool, timeout time.Duration) error {
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
		return fmt.Errorf("daemon: stop: %s", readErrorBody(resp))
	}
	return nil
}

func (c *daemonClient) ExtendInstance(instance string, by time.Duration, actor string) (*runtimeExtensionResponse, error) {
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
		return nil, fmt.Errorf("daemon: extend: %s", readErrorBody(resp))
	}
	var out runtimeExtensionResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("daemon: extend decode: %w", err)
	}
	return &out, nil
}

// StartInstance hits POST /v1/start to resume a previously-stopped persistent
// instance via `claude --resume <session-id>`. Used by `start` / `instance up`
// and by interactive `attach` to re-adopt the instance after the user exits.
func (c *daemonClient) StartInstance(instance string) error {
	body, _ := json.Marshal(map[string]string{"instance": instance})
	resp, err := c.hc.Post(c.baseURL+"/v1/start", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("daemon: start: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon: start: %s", readErrorBody(resp))
	}
	return nil
}

// RestartInstance hits POST /v1/restart.
func (c *daemonClient) RestartInstance(instance string) error {
	return c.RestartInstanceWithOptions(instance, false, 0)
}

func (c *daemonClient) RestartInstanceWithOptions(instance string, force bool, timeout time.Duration) error {
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
		return fmt.Errorf("daemon: restart: %s", readErrorBody(resp))
	}
	return nil
}

// RemoveInstance hits POST /v1/remove. With force=true, the daemon stops a
// running process before deleting its metadata.
func (c *daemonClient) RemoveInstance(instance string, force bool) error {
	body, _ := json.Marshal(map[string]any{"instance": instance, "force": force})
	resp, err := c.hc.Post(c.baseURL+"/v1/remove", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("daemon: remove: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon: remove: %s", readErrorBody(resp))
	}
	return nil
}

func (c *daemonClient) ChannelDelete(name string) error {
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
		return fmt.Errorf("daemon: channel delete: %s", readErrorBody(resp))
	}
	return nil
}
