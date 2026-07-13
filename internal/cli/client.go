package cli

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/daemonclient"
)

// daemonClient is the CLI compatibility facade over the shared typed client.
// Keeping the local name avoids a broad, behavior-free churn across command
// files while every call is implemented by internal/daemonclient.
type daemonClient struct {
	*daemonclient.Client
}

var errDaemonNotRunning = daemonclient.ErrNotRunning

type logNotFoundError = daemonclient.LogNotFoundError
type dispatchPayload = daemonclient.DispatchInput
type dispatchResponse = daemonclient.DispatchResponse
type messageResponse = daemonclient.MessageResponse
type feedbackDeliverResponse = daemonclient.FeedbackDeliverResponse
type daemonReconcileResponse = daemonclient.ReconcileResponse
type runtimeExtensionResponse = daemonclient.RuntimeExtensionResponse
type daemonAPIStatus = daemonclient.DaemonStatus
type resourceReadResponse = daemonclient.Resource
type daemonReconcileChange = daemonclient.ReconcileChange
type channelInfo = daemonclient.ChannelInfo
type channelMessage = daemonclient.ChannelMessage
type publishResp = daemonclient.ChannelPublishResponse
type subscribeResp = daemonclient.ChannelSubscribeResponse
type unsubscribeResp = daemonclient.ChannelUnsubscribeResponse
type drainResp = daemonclient.ChannelDrainResponse
type eventResponse = daemonclient.EventResponse

// These aliases retain the CLI's existing map-oriented topology projection.
// daemonclient itself exposes the strongly typed equivalent for new callers.
type topologyResponse struct {
	Instances            []topologyInstance `json:"instances"`
	Pipelines            []topologyPipeline `json:"pipelines"`
	Schedules            []topologySchedule `json:"schedules"`
	Teams                []topologyTeam     `json:"teams"`
	Budgets              []topologyBudget   `json:"budgets"`
	BudgetReminderLevels []int              `json:"budget_reminder_levels,omitempty"`
}

type topologyInstance struct {
	Name          string                   `json:"name"`
	Agent         string                   `json:"agent"`
	Ephemeral     bool                     `json:"ephemeral"`
	Description   string                   `json:"description"`
	Replicas      int                      `json:"replicas"`
	ReapWorktree  string                   `json:"reap_worktree"`
	RequiredVerbs []string                 `json:"required_verbs,omitempty"`
	Config        map[string]interface{}   `json:"config"`
	Triggers      []map[string]interface{} `json:"triggers"`
	Running       int                      `json:"running"`
	Queued        int                      `json:"queued"`
}

type topologyPipeline struct {
	Name                string                   `json:"name"`
	Trigger             map[string]interface{}   `json:"trigger"`
	Steps               []map[string]interface{} `json:"steps"`
	AutoAdvance         bool                     `json:"auto_advance"`
	ReapWorktree        string                   `json:"reap_worktree"`
	RedispatchOnReentry bool                     `json:"redispatch_on_reentry,omitempty"`
	Merge               map[string]interface{}   `json:"merge,omitempty"`
}

type topologySchedule struct {
	Name        string                 `json:"name"`
	StateName   string                 `json:"state_name,omitempty"`
	Every       string                 `json:"every"`
	RunOnStart  bool                   `json:"run_on_start"`
	Payload     map[string]interface{} `json:"payload"`
	Team        string                 `json:"team,omitempty"`
	LastSeenAt  *time.Time             `json:"last_seen_at,omitempty"`
	LastFiredAt *time.Time             `json:"last_fired_at,omitempty"`
}

type topologyTeam struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Instances   []string `json:"instances"`
	Pipelines   []string `json:"pipelines"`
	Schedules   []string `json:"schedules"`
	Channels    []string `json:"channels"`
}

type topologyBudget struct {
	Team         string  `json:"team"`
	TokensPerDay int64   `json:"tokens_per_day"`
	JobsInFlight int     `json:"jobs_in_flight"`
	Allocation   string  `json:"allocation"`
	LoadWeight   float64 `json:"load_weight"`
}

func daemonClientOptions(timeout time.Duration) daemonclient.Options {
	return daemonclient.Options{Timeout: timeout, Build: BuildInfo()}
}

func newDaemonClient(teamDir string) (*daemonClient, error) {
	return newDaemonClientWithTimeout(teamDir, 0)
}

func newDaemonClientWithTimeout(teamDir string, timeout time.Duration) (*daemonClient, error) {
	client, err := daemonclient.New(teamDir, daemonClientOptions(timeout))
	if err != nil {
		return nil, err
	}
	return &daemonClient{Client: client}, nil
}

func newDaemonClientForTargetTeamDirWithTimeout(teamDir string, timeout time.Duration) (*daemonClient, error) {
	client, err := daemonclient.NewForTargetTeamDir(teamDir, daemonClientOptions(timeout))
	if err != nil {
		return nil, err
	}
	return &daemonClient{Client: client}, nil
}

func newDaemonHTTPURLClientWithTokenFile(_ string, baseURL string, timeout time.Duration, tokenFile string) *daemonClient {
	return &daemonClient{Client: daemonclient.NewHTTP(baseURL, tokenFile, daemonClientOptions(timeout))}
}

func newDaemonHTTPURLClientWithTransport(_ string, baseURL, tokenFile string, timeout time.Duration, transport http.RoundTripper) *daemonClient {
	options := daemonClientOptions(timeout)
	options.RoundTripper = transport
	return &daemonClient{Client: daemonclient.NewHTTP(baseURL, tokenFile, options)}
}

func newDaemonUnixSocketClient(_ string, socket string, timeout time.Duration) *daemonClient {
	return &daemonClient{Client: daemonclient.NewUnix(socket, daemonClientOptions(timeout))}
}

func daemonOriginHeaderFromEnv(build buildinfo.Info) string {
	return daemonclient.OriginHeaderFromEnv(build)
}

// Instances preserves the daemon.Metadata result expected by existing CLI
// projection code. The shared client decodes into its independent API DTO so
// future frontends do not couple to daemon persistence structs.
func (c *daemonClient) Instances() ([]*daemon.Metadata, error) {
	instances, err := c.Client.Instances()
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(instances)
	if err != nil {
		return nil, err
	}
	var out []*daemon.Metadata
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *daemonClient) Topology() (*topologyResponse, error) {
	topology, err := c.Client.Topology()
	if err != nil {
		return nil, err
	}
	return cliTopologyResponse(topology)
}

func (c *daemonClient) TopologyReload() (*topologyResponse, error) {
	topology, err := c.Client.TopologyReload()
	if err != nil {
		return nil, err
	}
	return cliTopologyResponse(topology)
}

func cliTopologyResponse(topology *daemonclient.Topology) (*topologyResponse, error) {
	body, err := json.Marshal(topology)
	if err != nil {
		return nil, err
	}
	var out topologyResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
