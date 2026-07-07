package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/agent-team-project/agent-team/internal/feedback"
	"github.com/agent-team-project/agent-team/internal/intake"
	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/pmprovider"
	"github.com/agent-team-project/agent-team/internal/runtimeotel"
	teamtemplate "github.com/agent-team-project/agent-team/internal/template"
	"github.com/agent-team-project/agent-team/internal/topology"
)

// Handler builds the daemon's http.Handler. Routes are explicit (no library
// router) — the surface is small and `http.ServeMux` is sufficient. All paths
// are versioned `/v1/...` per orchestrator.md Open Q #7.
//
// If channels is nil, a fresh ChannelStore is constructed against the
// instance manager's daemon root — convenient for tests that don't care about
// channel state but still hit `/v1/...`.
//
// If events is nil, the topology endpoints (`/v1/event`, `/v1/topology`,
// `/v1/topology/reload`) return 503 Service Unavailable. Tests that exercise
// the legacy endpoints can pass nil; the real daemon constructs an
// EventResolver up front and always supplies one.
//
// teamDir is the consumer's `.agent_team/` path, used by `/v1/topology/reload`
// to re-read `instances.toml` from disk.
func Handler(m *InstanceManager, channels *ChannelStore, events *EventResolver, teamDir string, builds ...buildinfo.Info) http.Handler {
	return HandlerWithLog(m, channels, events, teamDir, nil, builds...)
}

// HandlerWithLog builds the daemon HTTP handler and writes daemon diagnostics
// such as build-skew warnings to logOut. nil logOut discards diagnostics.
func HandlerWithLog(m *InstanceManager, channels *ChannelStore, events *EventResolver, teamDir string, logOut io.Writer, builds ...buildinfo.Info) http.Handler {
	if channels == nil {
		channels = NewChannelStore(m.daemonRoot)
	}
	build := buildinfo.Current("0.1.0")
	if len(builds) > 0 && !builds[0].Empty() {
		build = builds[0]
	}
	writeError := func(w http.ResponseWriter, code int, msg string) {
		writeErrorWithBuild(w, code, msg, build)
	}
	auditor := newAuthorityAuditor(m, events, teamDir)
	mux := http.NewServeMux()
	ui := daemonUIHandler(build)
	mux.Handle("/ui", ui)
	mux.Handle("/ui/", ui)

	mux.HandleFunc("/v1/dispatch", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			Agent               string   `json:"agent"`
			Name                string   `json:"name"`
			URI                 string   `json:"uri"`
			SpecURI             string   `json:"spec_uri"`
			DeploymentURI       string   `json:"deployment_uri"`
			DeploymentParentURI string   `json:"deployment_parent_uri"`
			JobURI              string   `json:"job_uri"`
			Prompt              string   `json:"prompt"`
			Workspace           string   `json:"workspace"`
			WorkspaceURI        string   `json:"workspace_uri"`
			StateURI            string   `json:"state_uri"`
			Runtime             string   `json:"runtime"`
			RuntimeBinary       string   `json:"runtime_binary"`
			Args                []string `json:"args"`
			Env                 []string `json:"env"`
			Stdin               string   `json:"stdin"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if strings.TrimSpace(body.Agent) == "" || strings.TrimSpace(body.Name) == "" {
			writeError(w, http.StatusBadRequest, "agent and name are required")
			return
		}
		if strings.TrimSpace(body.Workspace) == "" {
			writeError(w, http.StatusBadRequest, "workspace is required")
			return
		}
		auditor.audit(r, "instance.dispatch", "instance:"+body.Name, origin.Envelope{})
		meta, err := m.Dispatch(DispatchInput{
			Agent:               body.Agent,
			Name:                body.Name,
			URI:                 body.URI,
			SpecURI:             body.SpecURI,
			DeploymentURI:       body.DeploymentURI,
			DeploymentParentURI: body.DeploymentParentURI,
			JobURI:              body.JobURI,
			Prompt:              body.Prompt,
			Workspace:           body.Workspace,
			WorkspaceURI:        body.WorkspaceURI,
			StateURI:            body.StateURI,
			StripOTelEnv:        stripOTelForTeamDir(teamDir),
			Runtime:             body.Runtime,
			RuntimeBinary:       body.RuntimeBinary,
			Args:                body.Args,
			Env:                 body.Env,
			Stdin:               body.Stdin,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"instance_id":           meta.Instance,
			"uri":                   meta.URI,
			"spec_uri":              meta.SpecURI,
			"deployment_uri":        meta.DeploymentURI,
			"deployment_parent_uri": meta.DeploymentParentURI,
			"job_uri":               meta.JobURI,
			"workspace_uri":         meta.WorkspaceURI,
			"state_uri":             meta.StateURI,
			"log_uri":               meta.LogURI,
			"started_at":            meta.StartedAt,
			"pid":                   meta.PID,
			"runtime":               meta.Runtime,
			"session_id":            meta.SessionID,
		})
	})

	mux.HandleFunc("/v1/stop", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			Instance      string `json:"instance"`
			Force         bool   `json:"force"`
			TimeoutMillis int64  `json:"timeout_ms"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if strings.TrimSpace(body.Instance) == "" {
			writeError(w, http.StatusBadRequest, "instance is required")
			return
		}
		if body.TimeoutMillis < 0 {
			writeError(w, http.StatusBadRequest, "timeout_ms must be >= 0")
			return
		}
		auditor.audit(r, "instance.stop", "instance:"+body.Instance, origin.Envelope{})
		_, err := m.StopWithOptions(body.Instance, StopOptions{
			Force:   body.Force,
			Timeout: time.Duration(body.TimeoutMillis) * time.Millisecond,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"stopped": true})
	})

	mux.HandleFunc("/v1/extend", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			Instance string `json:"instance"`
			ByMillis int64  `json:"by_ms"`
			Actor    string `json:"actor"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if strings.TrimSpace(body.Instance) == "" {
			writeError(w, http.StatusBadRequest, "instance is required")
			return
		}
		if body.ByMillis <= 0 {
			writeError(w, http.StatusBadRequest, "by_ms must be > 0")
			return
		}
		auditor.audit(r, "instance.extend", "instance:"+body.Instance, origin.Envelope{})
		extension, err := m.ExtendRuntimeBudget(body.Instance, time.Duration(body.ByMillis)*time.Millisecond, body.Actor)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"instance_id":       extension.Metadata.Instance,
			"metadata":          extension.Metadata,
			"by_ms":             extension.By.Milliseconds(),
			"previous_deadline": extension.PreviousDeadline,
			"new_deadline":      extension.NewDeadline,
			"actor":             extension.Actor,
		})
	})

	mux.HandleFunc("/v1/start", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			Instance      string `json:"instance"`
			Force         bool   `json:"force"`
			TimeoutMillis int64  `json:"timeout_ms"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if strings.TrimSpace(body.Instance) == "" {
			writeError(w, http.StatusBadRequest, "instance is required")
			return
		}
		auditor.audit(r, "instance.start", "instance:"+body.Instance, origin.Envelope{})
		meta, err := m.Start(body.Instance)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"instance_id":     meta.Instance,
			"session_resumed": true,
			"pid":             meta.PID,
		})
	})

	mux.HandleFunc("/v1/restart", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			Instance      string `json:"instance"`
			Force         bool   `json:"force"`
			TimeoutMillis int64  `json:"timeout_ms"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if strings.TrimSpace(body.Instance) == "" {
			writeError(w, http.StatusBadRequest, "instance is required")
			return
		}
		if body.TimeoutMillis < 0 {
			writeError(w, http.StatusBadRequest, "timeout_ms must be >= 0")
			return
		}
		auditor.audit(r, "instance.restart", "instance:"+body.Instance, origin.Envelope{})
		meta, err := m.RestartWithOptions(body.Instance, RestartOptions{
			Force:   body.Force,
			Timeout: time.Duration(body.TimeoutMillis) * time.Millisecond,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"instance_id": meta.Instance,
			"restarted":   true,
			"pid":         meta.PID,
		})
	})

	mux.HandleFunc("/v1/interrupt", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			To            string `json:"to"`
			From          string `json:"from"`
			Body          string `json:"body"`
			Force         bool   `json:"force"`
			TimeoutMillis int64  `json:"timeout_ms"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if strings.TrimSpace(body.To) == "" {
			writeError(w, http.StatusBadRequest, "`to` is required")
			return
		}
		if strings.TrimSpace(body.Body) == "" {
			writeError(w, http.StatusBadRequest, "`body` is required")
			return
		}
		if body.TimeoutMillis < 0 {
			writeError(w, http.StatusBadRequest, "timeout_ms must be >= 0")
			return
		}
		auditor.audit(r, "instance.interrupt", "instance:"+body.To, origin.Envelope{Instance: body.From})
		result, err := m.Interrupt(body.To, InterruptOptions{
			From:    body.From,
			Body:    body.Body,
			Force:   body.Force,
			Timeout: time.Duration(body.TimeoutMillis) * time.Millisecond,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp := map[string]any{
			"delivered":   true,
			"interrupted": true,
			"id":          result.Message.ID,
			"ts":          result.Message.TS,
		}
		if result.Metadata != nil {
			resp["instance_id"] = result.Metadata.Instance
			resp["pid"] = result.Metadata.PID
			resp["session_id"] = result.Metadata.SessionID
		}
		writeJSON(w, http.StatusOK, resp)
	})

	mux.HandleFunc("/v1/remove", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			Instance string `json:"instance"`
			Force    bool   `json:"force"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if strings.TrimSpace(body.Instance) == "" {
			writeError(w, http.StatusBadRequest, "instance is required")
			return
		}
		auditor.audit(r, "instance.remove", "instance:"+body.Instance, origin.Envelope{})
		if err := m.Remove(body.Instance, body.Force, 10*time.Second); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"removed": true})
	})

	mux.HandleFunc("/v1/instances", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		list := m.List()
		// Marshal as `[]` not `null` when empty — friendlier for clients.
		if list == nil {
			list = []*Metadata{}
		}
		writeJSON(w, http.StatusOK, list)
	})

	mux.HandleFunc("/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if strings.TrimSpace(teamDir) == "" {
			writeError(w, http.StatusServiceUnavailable, "team directory not configured")
			return
		}
		jobs, err := jobstore.List(teamDir)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, marshalJobs(jobs))
	})

	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		list := m.List()
		if list == nil {
			list = []*Metadata{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ready":      true,
			"pid":        os.Getpid(),
			"instances":  len(list),
			"team_dir":   teamDir,
			"started_at": daemonStartedAt(teamDir),
			"build":      build,
		})
	})

	mux.HandleFunc("/v1/reconcile", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		auditor.audit(r, "daemon.reconcile", "daemon:reconcile", origin.Envelope{})
		before, err := ListMetadata(m.daemonRoot)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if events != nil {
			err = ReconcileWithTopology(teamDir, m, events.Topology())
		} else {
			err = Reconcile(m.daemonRoot, m)
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, buildReconcileResponse(before, m.List()))
	})

	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		follow := r.URL.Query().Get("follow") == "true"
		tailLines := 0
		if rawTail := r.URL.Query().Get("tail"); rawTail != "" {
			n, err := strconv.Atoi(rawTail)
			if err != nil || n < 0 {
				writeError(w, http.StatusBadRequest, "tail must be a non-negative integer")
				return
			}
			tailLines = n
		}
		w.Header().Set("Content-Type", "application/jsonl; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_ = StreamLifecycleEvents(r.Context(), w, m.daemonRoot, follow, tailLines)
	})

	mux.HandleFunc("/v1/message", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			To   string `json:"to"`
			From string `json:"from"`
			Body string `json:"body"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if strings.TrimSpace(body.To) == "" {
			writeError(w, http.StatusBadRequest, "`to` is required")
			return
		}
		if strings.TrimSpace(body.Body) == "" {
			writeError(w, http.StatusBadRequest, "`body` is required")
			return
		}
		target, err := resolveHTTPMailboxTarget(m, events, teamDir, body.To)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !target.Valid() {
			writeError(w, http.StatusBadRequest, MailboxUnknownTargetMessage(body.To, target.Suggestions))
			return
		}
		auditor.audit(r, "inbox.send", "inbox:"+body.To, origin.Envelope{Instance: body.From})
		msg := &Message{From: body.From, To: body.To, Body: body.Body, TS: time.Now().UTC()}
		if err := AppendMessage(m.daemonRoot, body.To, msg); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		resp := map[string]any{
			"delivered": true,
			"id":        msg.ID,
			"ts":        msg.TS,
		}
		if target.Note != "" {
			resp["note"] = target.Note
		}
		writeJSON(w, http.StatusOK, resp)
	})

	mux.HandleFunc("/v1/feedback/deliver", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if strings.TrimSpace(teamDir) == "" {
			writeError(w, http.StatusServiceUnavailable, "team directory not configured")
			return
		}
		var body feedback.DeliverInput
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		category := body.Category
		if category == "" {
			category = feedback.CategoryFriction
		}
		sender, _ := origin.ParseHeaderValue(r.Header.Get(origin.HeaderName))
		sender = origin.Merge(sender, body.Origin).Clean()
		auditor.audit(r, "feedback.deliver", "feedback", sender)
		item, err := feedback.Submit(teamDir, feedback.SubmitInput{
			Body:     body.Body,
			Category: category,
			Context:  body.Context,
			Origin:   sender,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp := map[string]any{
			"delivered": true,
			"id":        item.ID,
			"ts":        item.TS,
		}
		if item.Category == feedback.CategoryIncident {
			pinged, note := deliverIncidentFeedbackPing(m, events, teamDir, item)
			resp["manager_pinged"] = pinged
			if note != "" {
				resp["note"] = note
			}
		}
		writeJSON(w, http.StatusOK, resp)
	})

	// `/v1/logs/{instance}` — chunked text stream of the instance's child.log.
	// Pattern matched as a prefix; the suffix after `/v1/logs/` is the
	// instance name.
	mux.HandleFunc("/v1/logs/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		instance := strings.TrimPrefix(r.URL.Path, "/v1/logs/")
		instance = strings.Trim(instance, "/")
		if instance == "" {
			writeError(w, http.StatusBadRequest, "instance name missing")
			return
		}
		exists, err := logsExist(m.daemonRoot, instance)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !exists {
			writeError(w, http.StatusNotFound, "no log for instance "+instance)
			return
		}
		follow := r.URL.Query().Get("follow") == "true"
		tailLines := 0
		if rawTail := r.URL.Query().Get("tail"); rawTail != "" {
			n, err := strconv.Atoi(rawTail)
			if err != nil || n < 0 {
				writeError(w, http.StatusBadRequest, "tail must be a non-negative integer")
				return
			}
			tailLines = n
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		// `Transfer-Encoding: chunked` is set automatically by net/http when
		// we don't set Content-Length and write incrementally — we still set
		// the Cache-Control hint to make intermediaries unlikely to buffer.
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		if err := StreamLogs(r.Context(), w, m.daemonRoot, instance, follow, tailLines); err != nil {
			// Headers already flushed; we can't switch to a JSON error.
			// The connection will end here — clients see truncated output,
			// which is the best we can do post-headers.
			return
		}
	})

	// `GET /v1/channels` — summary of every known channel. Sorted by name.
	mux.HandleFunc("/v1/channels", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		list, err := channels.List()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if list == nil {
			list = []*ChannelInfo{}
		}
		writeJSON(w, http.StatusOK, list)
	})

	// Channel-scoped routes share a prefix; we dispatch on the suffix verb.
	// Pattern: `/v1/channel/{name}/{verb}` for POST verbs and `messages` for
	// the GET drain. The leading `#` in channel names must be URL-encoded by
	// callers (the CLI client + skill take care of it).
	mux.HandleFunc("/v1/channel/", func(w http.ResponseWriter, r *http.Request) {
		name, verb, ok := splitChannelPath(r.URL.Path)
		if !ok {
			writeError(w, http.StatusBadRequest, "expected /v1/channel/{name}/{verb}")
			return
		}
		dispatchChannelRoute(w, r, channels, events, auditor, name, verb, build)
	})

	// `POST /v1/event` — public trigger entry point. Resolves the inbound
	// event against the declared topology and actuates each matched
	// instance (spawn for ephemeral, mailbox-message for persistent).
	mux.HandleFunc("/v1/event", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if events == nil {
			writeError(w, http.StatusServiceUnavailable, "topology not configured")
			return
		}
		var body struct {
			Type    string         `json:"type"`
			Payload map[string]any `json:"payload"`
			Trace   bool           `json:"trace"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if strings.TrimSpace(body.Type) == "" {
			writeError(w, http.StatusBadRequest, "`type` is required")
			return
		}
		if body.Payload == nil {
			body.Payload = map[string]any{}
		}
		auditor.audit(r, "event.publish", "event:"+body.Type, origin.Envelope{
			Job:     eventJobID(body.Payload),
			Trigger: origin.TriggerFromEvent(body.Type, body.Payload),
		})
		result, err := events.EventWithResult(body.Type, body.Payload)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp := eventResponseMap(result.Outcomes)
		if body.Trace {
			resp["trace"] = result.Trace
		}
		writeJSON(w, http.StatusOK, resp)
	})

	mux.HandleFunc("/v1/intake/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if events == nil {
			writeError(w, http.StatusServiceUnavailable, "topology not configured")
			return
		}
		provider := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/intake/"), "/")
		var ev *intake.Event
		var err error
		switch provider {
		case "linear":
			ev, err = intake.NormalizeLinear(readRequestBody(r))
		case "github":
			ev, err = intake.NormalizeGitHub(readRequestBody(r))
		default:
			writeError(w, http.StatusBadRequest, "unknown intake provider")
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		auditor.audit(r, "intake.publish", "intake:"+provider, origin.Envelope{})
		if provider == "linear" || provider == "github" {
			if reason := providerIntakeIgnoreReason(teamDir, events, provider, ev); reason != "" {
				appendIgnoredIntakeLifecycleEvent(teamDir, provider, reason, ev)
				resp := eventResponseMap([]EventOutcome{{
					Instance: "intake:" + provider,
					Action:   "noop",
					Reason:   reason,
				}})
				resp["event"] = ev
				writeJSON(w, http.StatusOK, resp)
				return
			}
		}
		result, err := events.EventWithResult(ev.Type, ev.Payload)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp := eventResponseMap(result.Outcomes)
		resp["event"] = ev
		if result.Reconcile != nil {
			resp["reconcile"] = result.Reconcile
		}
		writeJSON(w, http.StatusOK, resp)
	})

	mux.HandleFunc("/v1/outbox", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if strings.TrimSpace(teamDir) == "" {
			writeError(w, http.StatusServiceUnavailable, "team directory not configured")
			return
		}
		items, err := ListOutboxItems(teamDir)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if items == nil {
			items = []*OutboxItem{}
		}
		writeJSON(w, http.StatusOK, items)
	})

	mux.HandleFunc("/v1/outbox/drain", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if events == nil {
			writeError(w, http.StatusServiceUnavailable, "topology not configured")
			return
		}
		auditor.audit(r, "outbox.drain", "outbox", origin.Envelope{})
		result, err := events.DrainOutboxWithResult(r.URL.Query().Get("dry_run") == "true")
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	})

	mux.HandleFunc("/v1/queue", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		items, err := ListQueueItems(m.daemonRoot)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if items == nil {
			items = []*QueueItem{}
		}
		writeJSON(w, http.StatusOK, items)
	})

	mux.HandleFunc("/v1/locks", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if events == nil {
			writeError(w, http.StatusServiceUnavailable, "topology not configured")
			return
		}
		snapshots := events.LockSnapshots()
		if snapshots == nil {
			snapshots = []LockSnapshot{}
		}
		writeJSON(w, http.StatusOK, snapshots)
	})

	mux.HandleFunc("/v1/queue/drain", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if events == nil {
			writeError(w, http.StatusServiceUnavailable, "topology not configured")
			return
		}
		auditor.audit(r, "queue.drain", "queue", origin.Envelope{})
		var (
			result *QueueDrainResult
			err    error
		)
		ids := queryValues(r, "id")
		if r.URL.Query().Get("dry_run") == "true" {
			if ids != nil {
				result, err = events.PreviewDrainQueuesWithResultForIDs(ids)
			} else {
				result, err = events.PreviewDrainQueuesWithResult()
			}
		} else {
			if ids != nil {
				result, err = events.DrainQueuesWithResultForIDs(ids)
			} else {
				result, err = events.DrainQueuesWithResult()
			}
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	})

	mux.HandleFunc("/v1/queue/", func(w http.ResponseWriter, r *http.Request) {
		id, action, ok := splitQueuePath(r.URL.Path)
		if !ok {
			writeError(w, http.StatusBadRequest, "expected /v1/queue/{id}[/retry|/drop]")
			return
		}
		switch action {
		case "":
			if r.Method != http.MethodGet {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			item, err := ReadQueueItem(m.daemonRoot, id)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					writeError(w, http.StatusNotFound, "queue item not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, item)
		case "drop":
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			auditor.audit(r, "queue.drop", "queue:"+id, origin.Envelope{})
			var err error
			if events != nil {
				err = events.DropQueueItem(id)
			} else {
				err = RemoveQueueItem(m.daemonRoot, id)
			}
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					writeError(w, http.StatusNotFound, "queue item not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"dropped": true, "id": id})
		case "retry":
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			if events == nil {
				writeError(w, http.StatusServiceUnavailable, "topology not configured")
				return
			}
			auditor.audit(r, "queue.retry", "queue:"+id, origin.Envelope{})
			outcome, err := events.RetryQueueItem(id)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					writeError(w, http.StatusNotFound, "queue item not found")
					return
				}
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, outcome)
		default:
			writeError(w, http.StatusBadRequest, "unknown queue action")
		}
	})

	mux.HandleFunc("/v1/schedules/fire", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if events == nil {
			writeError(w, http.StatusServiceUnavailable, "topology not configured")
			return
		}
		auditor.audit(r, "schedule.fire", "schedules", origin.Envelope{})
		var (
			result *ScheduleFireResult
			err    error
		)
		names := queryValues(r, "name")
		if r.URL.Query().Get("dry_run") == "true" {
			if names != nil {
				result, err = events.PreviewDueSchedulesWithResultForNames(time.Now().UTC(), names)
			} else {
				result, err = events.PreviewDueSchedulesWithResult(time.Now().UTC())
			}
		} else {
			if names != nil {
				result, err = events.FireDueSchedulesWithResultForNames(time.Now().UTC(), names)
			} else {
				result, err = events.FireDueSchedulesWithResult(time.Now().UTC())
			}
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	})

	// `GET /v1/topology` — declared instances + triggers + per-instance
	// running/queued counts. Always 200 with `{instances: []}` even when
	// nothing is declared, so clients can render an empty state.
	mux.HandleFunc("/v1/topology", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			topo := (*topology.Topology)(nil)
			if events != nil {
				topo = events.Topology()
			}
			writeJSON(w, http.StatusOK, marshalTopology(topo, events))
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	// `POST /v1/topology/reload` — re-read instances.toml from disk and swap
	// the live topology pointer. Running instances are not restarted.
	mux.HandleFunc("/v1/topology/reload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if events == nil {
			writeError(w, http.StatusServiceUnavailable, "topology not configured")
			return
		}
		auditor.audit(r, "topology.reload", "topology", origin.Envelope{})
		topo, err := topology.LoadFromTeamDir(teamDir)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		events.SetTopology(topo)
		writeJSON(w, http.StatusOK, marshalTopology(topo, events))
	})

	return buildHandshakeHandler(mux, build, logOut)
}

func daemonStartedAt(teamDir string) time.Time {
	if strings.TrimSpace(teamDir) == "" {
		return time.Time{}
	}
	le, err := ReadLaunchEnv(DaemonRoot(teamDir))
	if err != nil {
		return time.Time{}
	}
	return le.RecordedAt
}

type jobListEntry struct {
	ID                  string          `json:"id"`
	Ticket              string          `json:"ticket"`
	TicketURL           string          `json:"ticket_url,omitempty"`
	Target              string          `json:"target"`
	ImplementationAgent string          `json:"implementation_agent,omitempty"`
	Instance            string          `json:"instance,omitempty"`
	Pipeline            string          `json:"pipeline,omitempty"`
	Status              jobstore.Status `json:"status"`
	Held                bool            `json:"held,omitempty"`
	Branch              string          `json:"branch,omitempty"`
	PR                  string          `json:"pr,omitempty"`
	LastEvent           string          `json:"last_event,omitempty"`
	LastStatus          string          `json:"last_status,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

func marshalJobs(jobs []*jobstore.Job) []jobListEntry {
	out := make([]jobListEntry, 0, len(jobs))
	for _, j := range jobs {
		if j == nil {
			continue
		}
		out = append(out, jobListEntry{
			ID:                  j.ID,
			Ticket:              j.Ticket,
			TicketURL:           j.TicketURL,
			Target:              j.Target,
			ImplementationAgent: j.ImplementationAgent,
			Instance:            j.Instance,
			Pipeline:            j.Pipeline,
			Status:              j.Status,
			Held:                j.Held,
			Branch:              j.Branch,
			PR:                  j.PR,
			LastEvent:           j.LastEvent,
			LastStatus:          j.LastStatus,
			CreatedAt:           j.CreatedAt,
			UpdatedAt:           j.UpdatedAt,
		})
	}
	return out
}

func buildHandshakeHandler(next http.Handler, daemonBuild buildinfo.Info, logOut io.Writer) http.Handler {
	if logOut == nil {
		logOut = io.Discard
	}
	var seen sync.Map
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientBuild, ok := requestBuildIdentity(r)
		if ok && !buildinfo.Equivalent(clientBuild, daemonBuild) {
			key := clientBuild.ComparisonKey()
			if _, loaded := seen.LoadOrStore(key, struct{}{}); !loaded {
				fmt.Fprintf(logOut, "%s daemon build skew: client=%s daemon=%s\n",
					time.Now().UTC().Format(time.RFC3339), clientBuild.Display(), daemonBuild.Display())
			}
		}
		next.ServeHTTP(w, r)
	})
}

func requestBuildIdentity(r *http.Request) (buildinfo.Info, bool) {
	raw := strings.TrimSpace(r.Header.Get(buildinfo.HeaderName))
	if raw == "" {
		return buildinfo.Info{}, false
	}
	info, err := buildinfo.ParseHeaderValue(raw)
	if err != nil || info.Empty() {
		return buildinfo.Info{}, false
	}
	return info, true
}

func providerIntakeIgnoreReason(teamDir string, events *EventResolver, provider string, ev *intake.Event) string {
	if !statusChangeWouldDispatch(events, ev) {
		return ""
	}
	pm, err := pmprovider.ForName(pmprovider.NormalizeProviderName(provider))
	if err != nil {
		return ""
	}
	agentUserID, err := pm.ResolveActorID(teamDir)
	if err != nil || strings.TrimSpace(agentUserID) == "" {
		return providerLoopProtectionUnavailableReason(pm.Name())
	}
	if ignored, reason := pm.SelfStatusChangeForActor(ev, agentUserID); ignored {
		return reason
	}
	return ""
}

func statusChangeWouldDispatch(events *EventResolver, ev *intake.Event) bool {
	if ev == nil || ev.Type != "ticket.status_changed" || events == nil || events.topo == nil {
		return false
	}
	if len(events.topo.ResolvePipelines(ev.Type, ev.Payload)) > 0 {
		return true
	}
	return len(events.topo.Resolve(ev.Type, ev.Payload)) > 0
}

func providerLoopProtectionUnavailableReason(provider pmprovider.ProviderName) string {
	switch provider {
	case pmprovider.ProviderGitHub:
		return intake.GitHubLoopProtectionUnavailableReason
	default:
		return intake.LinearLoopProtectionUnavailableReason
	}
}

func appendIgnoredIntakeLifecycleEvent(teamDir, provider, reason string, ev *intake.Event) {
	payload := map[string]any{}
	if ev != nil && ev.Payload != nil {
		payload = ev.Payload
	}
	_ = AppendLifecycleEvent(DaemonRoot(teamDir), &LifecycleEvent{
		Action:   "intake_ignored",
		Instance: "intake:" + provider,
		Ticket:   payloadString(payload, "ticket"),
		Message:  reason,
	})
}

func eventResponseMap(outcomes []EventOutcome) map[string]any {
	matched := make([]string, 0, len(outcomes))
	dispatched := make([]map[string]any, 0)
	queued := make([]string, 0)
	messaged := make([]string, 0)
	noop := make([]map[string]string, 0)
	blocked := make([]map[string]string, 0)
	rejected := make([]map[string]string, 0)
	for _, oc := range outcomes {
		matched = append(matched, oc.Instance)
		switch oc.Action {
		case "dispatched":
			dispatched = append(dispatched, map[string]any{
				"instance":    oc.Instance,
				"instance_id": oc.InstanceID,
			})
		case "queued":
			if oc.InstanceID != "" {
				queued = append(queued, oc.InstanceID)
			} else {
				queued = append(queued, oc.Instance)
			}
		case "messaged":
			messaged = append(messaged, oc.Instance)
		case "noop":
			noop = append(noop, map[string]string{
				"instance": oc.Instance,
				"reason":   oc.Reason,
			})
		case "blocked":
			blocked = append(blocked, map[string]string{
				"instance": oc.Instance,
				"reason":   oc.Reason,
			})
		case "rejected":
			rejected = append(rejected, map[string]string{
				"instance": oc.Instance,
				"reason":   oc.Reason,
			})
		}
	}
	return map[string]any{
		"matched":    matched,
		"dispatched": dispatched,
		"queued":     queued,
		"messaged":   messaged,
		"noop":       noop,
		"blocked":    blocked,
		"rejected":   rejected,
		"outcomes":   outcomes,
	}
}

func deliverIncidentFeedbackPing(m *InstanceManager, events *EventResolver, teamDir string, item *feedback.Item) (bool, string) {
	if m == nil || item == nil {
		return false, "manager ping skipped"
	}
	target, err := resolveHTTPMailboxTarget(m, events, teamDir, "manager")
	if err != nil {
		return false, err.Error()
	}
	if !target.Valid() {
		return false, MailboxUnknownTargetMessage("manager", target.Suggestions)
	}
	msg := &Message{
		From: "feedback.deliver",
		To:   "manager",
		Body: formatIncidentFeedbackMessage(item),
		TS:   time.Now().UTC(),
	}
	if err := AppendMessage(m.daemonRoot, "manager", msg); err != nil {
		return false, err.Error()
	}
	if target.Note != "" {
		return true, target.Note
	}
	return true, ""
}

func formatIncidentFeedbackMessage(item *feedback.Item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Incident feedback delivered: %s\n", item.ID)
	if item.Origin != nil {
		if rendered := origin.HeaderValue(*item.Origin); rendered != "" {
			fmt.Fprintf(&b, "Origin: %s\n", rendered)
		}
	}
	if item.Context.Job != "" || item.Context.Ticket != "" || item.Context.Instance != "" {
		fmt.Fprintf(&b, "Context: instance=%s job=%s ticket=%s\n", item.Context.Instance, item.Context.Job, item.Context.Ticket)
	}
	fmt.Fprintf(&b, "Body: %s", item.Body)
	return b.String()
}

func readRequestBody(r *http.Request) []byte {
	body, _ := io.ReadAll(r.Body)
	return body
}

func queryValues(r *http.Request, key string) []string {
	if r == nil {
		return nil
	}
	raw, ok := r.URL.Query()[key]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func splitQueuePath(path string) (id, action string, ok bool) {
	const prefix = "/v1/queue/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if rest == "" {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) > 2 {
		return "", "", false
	}
	decoded, err := url.PathUnescape(parts[0])
	if err != nil || strings.TrimSpace(decoded) == "" {
		return "", "", false
	}
	if len(parts) == 2 {
		action = parts[1]
	}
	return decoded, action, true
}

type reconcileResponse struct {
	Reconciled bool              `json:"reconciled"`
	Changed    int               `json:"changed"`
	Instances  []*Metadata       `json:"instances"`
	Changes    []reconcileChange `json:"changes"`
}

type reconcileChange struct {
	Instance string `json:"instance"`
	Agent    string `json:"agent,omitempty"`
	Before   Status `json:"before"`
	After    Status `json:"after"`
	PID      int    `json:"pid,omitempty"`
}

func buildReconcileResponse(before, after []*Metadata) reconcileResponse {
	beforeByName := map[string]*Metadata{}
	for _, meta := range before {
		beforeByName[meta.Instance] = meta
	}
	if after == nil {
		after = []*Metadata{}
	}
	sort.Slice(after, func(i, j int) bool { return after[i].Instance < after[j].Instance })
	resp := reconcileResponse{
		Reconciled: true,
		Instances:  after,
		Changes:    []reconcileChange{},
	}
	for _, meta := range after {
		beforeStatus := Status("")
		if prior := beforeByName[meta.Instance]; prior != nil {
			beforeStatus = prior.Status
		}
		if beforeStatus == meta.Status {
			continue
		}
		resp.Changes = append(resp.Changes, reconcileChange{
			Instance: meta.Instance,
			Agent:    meta.Agent,
			Before:   beforeStatus,
			After:    meta.Status,
			PID:      meta.PID,
		})
	}
	resp.Changed = len(resp.Changes)
	return resp
}

// marshalTopology renders the wire format for `/v1/topology` and the
// `/v1/topology/reload` response. Pulls running/queued counts from `events`
// so the client can render Docker-ps-style status without a second call.
func marshalTopology(topo *topology.Topology, events *EventResolver) map[string]any {
	if topo == nil {
		return map[string]any{"instances": []any{}, "pipelines": []any{}}
	}
	out := make([]map[string]any, 0, len(topo.Instances))
	for _, inst := range topo.SortedInstances() {
		entry := map[string]any{
			"name":          inst.Name,
			"agent":         inst.Agent,
			"ephemeral":     inst.Ephemeral,
			"description":   inst.Description,
			"replicas":      inst.Replicas,
			"reap_worktree": inst.ReapWorktree,
			"config":        map[string]any(inst.Config),
			"triggers":      marshalTriggers(inst.Triggers),
		}
		if events != nil && inst.Ephemeral {
			running, queued := events.QueueDepth(inst.Name)
			entry["running"] = running
			entry["queued"] = queued
		}
		out = append(out, entry)
	}
	pipelines := make([]map[string]any, 0, len(topo.Pipelines))
	for _, pipeline := range topo.SortedPipelines() {
		entry := map[string]any{
			"name":          pipeline.Name,
			"trigger":       marshalTrigger(pipeline.Trigger),
			"steps":         marshalPipelineSteps(pipeline.Steps),
			"reap_worktree": pipeline.ReapWorktree,
		}
		if merge := marshalPipelineMerge(pipeline.Merge); merge != nil {
			entry["merge"] = merge
		}
		pipelines = append(pipelines, entry)
	}
	schedules := make([]map[string]any, 0, len(topo.Schedules))
	for _, schedule := range topo.SortedSchedules() {
		schedules = append(schedules, map[string]any{
			"name":         schedule.Name,
			"every":        schedule.Every.String(),
			"run_on_start": schedule.RunOnStart,
			"payload":      schedule.Payload,
		})
	}
	return map[string]any{"instances": out, "pipelines": pipelines, "schedules": schedules}
}

func marshalPipelineMerge(merge *topology.PipelineMerge) map[string]any {
	if merge == nil {
		return nil
	}
	out := map[string]any{"strategy": merge.Strategy}
	if merge.Script != "" {
		out["script"] = merge.Script
	}
	if merge.Land != "" {
		out["land"] = merge.Land
	}
	if len(merge.OwnedPaths) > 0 {
		out["owned_paths"] = merge.OwnedPaths
	}
	return out
}

func marshalTriggers(triggers []*topology.Trigger) []map[string]any {
	out := make([]map[string]any, 0, len(triggers))
	for _, t := range triggers {
		match := map[string]any{}
		for k, mv := range t.Match {
			if mv.Single != "" {
				match[k] = mv.Single
			} else if len(mv.List) > 0 {
				match[k] = mv.List
			}
		}
		out = append(out, map[string]any{
			"event": t.Event,
			"match": match,
		})
	}
	return out
}

func marshalTrigger(t *topology.Trigger) map[string]any {
	if t == nil {
		return nil
	}
	match := map[string]any{}
	for k, mv := range t.Match {
		if mv.Single != "" {
			match[k] = mv.Single
		} else if len(mv.List) > 0 {
			match[k] = mv.List
		}
	}
	return map[string]any{"event": t.Event, "match": match}
}

func marshalPipelineSteps(steps []*topology.PipelineStep) []map[string]any {
	out := make([]map[string]any, 0, len(steps))
	for _, step := range steps {
		row := map[string]any{
			"id":     step.ID,
			"target": step.Target,
			"after":  step.After,
		}
		if step.Label != "" {
			row["label"] = step.Label
		}
		if step.Description != "" {
			row["description"] = step.Description
		}
		if step.Instructions != "" {
			row["instructions"] = step.Instructions
		}
		if step.Gate != "" {
			row["gate"] = step.Gate
		}
		if step.ApprovalRequired {
			row["approval_required"] = true
		}
		if step.Optional {
			row["optional"] = true
		}
		if step.Timeout > 0 {
			row["timeout"] = step.Timeout.String()
		}
		if step.MaxAttempts > 0 {
			row["max_attempts"] = step.MaxAttempts
		}
		if step.RetryOnCrash {
			row["retry_on_crash"] = true
		}
		out = append(out, row)
	}
	return out
}

// splitChannelPath parses `/v1/channel/{name}[/{verb}]` into its parts.
// The name is URL-decoded so `%23foo` round-trips to `#foo`. With no `{verb}`,
// returns verb="" — used for `DELETE /v1/channel/{name}`. Returns ok=false on
// a malformed (empty) shape.
func splitChannelPath(path string) (name, verb string, ok bool) {
	const prefix = "/v1/channel/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	rest = strings.TrimSuffix(rest, "/")
	if rest == "" {
		return "", "", false
	}
	// rest is either `{encoded-name}` or `{encoded-name}/{verb}`. Split on
	// the *last* slash; if there isn't one, the whole thing is the name and
	// verb is empty (DELETE form).
	idx := strings.LastIndex(rest, "/")
	var encName string
	switch {
	case idx == -1:
		encName = rest
	case idx == 0 || idx == len(rest)-1:
		return "", "", false
	default:
		encName = rest[:idx]
		verb = rest[idx+1:]
	}
	dec, err := url.PathUnescape(encName)
	if err != nil {
		return "", "", false
	}
	return dec, verb, true
}

// dispatchChannelRoute handles every channel-scoped endpoint. Centralising
// the dispatch keeps the route registrations small and lets us share JSON
// decoding + name validation.
func dispatchChannelRoute(w http.ResponseWriter, r *http.Request, channels *ChannelStore, events *EventResolver, auditor *authorityAuditor, name, verb string, build buildinfo.Info) {
	writeError := func(w http.ResponseWriter, code int, msg string) {
		writeErrorWithBuild(w, code, msg, build)
	}
	requestActor := func(fallback origin.Envelope) origin.Envelope {
		if auditor == nil {
			return fallback.Clean()
		}
		return auditor.originForRequest(r, fallback)
	}
	switch verb {
	case "publish":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			Sender string `json:"sender"`
			Body   string `json:"body"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if strings.TrimSpace(body.Body) == "" {
			writeError(w, http.StatusBadRequest, "`body` is required")
			return
		}
		actor := requestActor(origin.Envelope{Instance: body.Sender})
		storage, err := scopedChannelNameForRequest(events, name, actor)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		auditor.audit(r, "channel.publish", "channel:"+storage, actor)
		res, err := channels.Publish(storage, body.Sender, body.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"seq": res.Seq,
			"ts":  res.TS,
		})

	case "subscribe":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			Instance string `json:"instance"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		actor := requestActor(origin.Envelope{Instance: body.Instance})
		storage, err := scopedChannelNameForRequest(events, name, actor)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		auditor.audit(r, "channel.subscribe", "channel:"+storage, actor)
		cursor, fresh, err := channels.Subscribe(storage, body.Instance)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":         true,
			"cursor":     cursor,
			"subscribed": fresh,
		})

	case "unsubscribe":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			Instance string `json:"instance"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		actor := requestActor(origin.Envelope{Instance: body.Instance})
		storage, err := scopedChannelNameForRequest(events, name, actor)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		auditor.audit(r, "channel.unsubscribe", "channel:"+storage, actor)
		removed, err := channels.Unsubscribe(storage, body.Instance)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           true,
			"unsubscribed": removed,
		})

	case "ack":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			Instance string `json:"instance"`
			Cursor   int64  `json:"cursor"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		actor := requestActor(origin.Envelope{Instance: body.Instance})
		storage, err := scopedChannelNameForRequest(events, name, actor)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		auditor.audit(r, "channel.ack", "channel:"+storage, actor)
		if err := channels.Ack(storage, body.Instance, body.Cursor); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "messages":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		instance := r.URL.Query().Get("instance")
		if strings.TrimSpace(instance) == "" {
			writeError(w, http.StatusBadRequest, "`instance` query param is required")
			return
		}
		actor := requestActor(origin.Envelope{Instance: instance})
		storage, err := scopedChannelNameForRequest(events, name, actor)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		var since *int64
		if raw := r.URL.Query().Get("since"); raw != "" {
			v, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				writeError(w, http.StatusBadRequest, "`since` must be an integer")
				return
			}
			since = &v
		}
		var wait time.Duration
		if raw := r.URL.Query().Get("wait"); raw != "" {
			d, err := time.ParseDuration(raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "`wait` must be a Go duration (e.g. 10s)")
				return
			}
			if d > 60*time.Second {
				d = 60 * time.Second
			}
			if d < 0 {
				d = 0
			}
			wait = d
		}
		res, err := channels.Drain(r.Context(), storage, instance, since, wait)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// Marshal as `[]` not `null` for empty drains so clients can iterate
		// unconditionally.
		msgs := res.Messages
		if msgs == nil {
			msgs = []*ChannelMessage{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"messages": msgs,
			"cursor":   res.Cursor,
		})

	case "":
		// `DELETE /v1/channel/{name}` — no verb suffix.
		if r.Method != http.MethodDelete {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		actor := requestActor(origin.Envelope{})
		storage, err := scopedChannelNameForRequest(events, name, actor)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		auditor.audit(r, "channel.delete", "channel:"+storage, actor)
		removed, err := channels.Delete(storage)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !removed {
			writeError(w, http.StatusNotFound, "no such channel")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		writeError(w, http.StatusNotFound, "unknown channel verb: "+verb)
	}
}

func resolveHTTPMailboxTarget(m *InstanceManager, events *EventResolver, teamDir, target string) (MailboxTargetResolution, error) {
	var topo *topology.Topology
	if events != nil {
		topo = events.Topology()
	} else if strings.TrimSpace(teamDir) != "" {
		loaded, err := topology.LoadFromTeamDir(teamDir)
		if err != nil {
			return MailboxTargetResolution{}, fmt.Errorf("load topology: %w", err)
		}
		topo = loaded
	}
	return ResolveMailboxTarget(m.List(), topo, target), nil
}

func scopedChannelNameForRequest(events *EventResolver, requested string, actor origin.Envelope) (string, error) {
	canonical, err := topology.CanonicalChannelName(requested)
	if err != nil {
		return "", err
	}
	if events == nil {
		return canonical, nil
	}
	topo := events.Topology()
	if topo == nil {
		return canonical, nil
	}
	declared := topo.Channels[canonical]
	if declared == nil {
		return canonical, nil
	}
	team := topo.TeamForChannel(canonical)
	return topology.ScopedChannelName(canonical, declared.Scope, team, actor.Job)
}

// decodeJSON tolerates unknown fields by design: wire requests come from CLIs
// that may be newer than this daemon, and rejecting additive fields turns
// every CLI upgrade into a breaking change for running daemons (SQU-55).
// Strict validation belongs to config/topology parsing, not the wire.
// stripOTelForTeamDir mirrors the topology-dispatch rule for the direct
// /v1/dispatch path: whenever the repo declares an [otel] table (enabled or
// not), inherited OTel env is stripped so the current config alone decides
// what the child sees. No table keeps legacy passthrough. Errors fail open to
// passthrough — identical to a repo without the table.
func stripOTelForTeamDir(teamDir string) bool {
	if strings.TrimSpace(teamDir) == "" {
		return false
	}
	tree, err := teamtemplate.LoadTOMLFile(filepath.Join(teamDir, "config.toml"))
	if err != nil {
		return false
	}
	cfg, err := runtimeotel.FromTree(tree)
	if err != nil {
		return false
	}
	return cfg.Configured()
}

func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return errors.New("empty body")
	}
	return json.NewDecoder(r.Body).Decode(v)
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeErrorWithBuild(w, code, msg, buildinfo.Current("0.1.0"))
}

func writeErrorWithBuild(w http.ResponseWriter, code int, msg string, build buildinfo.Info) {
	writeJSON(w, code, map[string]any{
		"error":        msg,
		"daemon_build": build,
	})
}
