package daemon

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Handler builds the daemon's http.Handler. Routes are explicit (no library
// router) — the surface is small and `http.ServeMux` is sufficient. All paths
// are versioned `/v1/...` per orchestrator.md Open Q #7.
//
// If channels is nil, a fresh ChannelStore is constructed against the
// instance manager's daemon root — convenient for tests that don't care about
// channel state but still hit `/v1/...`.
func Handler(m *InstanceManager, channels *ChannelStore) http.Handler {
	if channels == nil {
		channels = NewChannelStore(m.daemonRoot)
	}
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/dispatch", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			Agent     string   `json:"agent"`
			Name      string   `json:"name"`
			Prompt    string   `json:"prompt"`
			Workspace string   `json:"workspace"`
			Args      []string `json:"args"`
			Env       []string `json:"env"`
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
		meta, err := m.Dispatch(DispatchInput{
			Agent:     body.Agent,
			Name:      body.Name,
			Prompt:    body.Prompt,
			Workspace: body.Workspace,
			Args:      body.Args,
			Env:       body.Env,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"instance_id": meta.Instance,
			"started_at":  meta.StartedAt,
			"pid":         meta.PID,
			"session_id":  meta.SessionID,
		})
	})

	mux.HandleFunc("/v1/stop", func(w http.ResponseWriter, r *http.Request) {
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
		if strings.TrimSpace(body.Instance) == "" {
			writeError(w, http.StatusBadRequest, "instance is required")
			return
		}
		_, err := m.Stop(body.Instance)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"stopped": true})
	})

	mux.HandleFunc("/v1/start", func(w http.ResponseWriter, r *http.Request) {
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
		if strings.TrimSpace(body.Instance) == "" {
			writeError(w, http.StatusBadRequest, "instance is required")
			return
		}
		meta, err := m.Start(body.Instance)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"instance_id":      meta.Instance,
			"session_resumed":  true,
			"pid":              meta.PID,
		})
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
		msg := &Message{From: body.From, To: body.To, Body: body.Body, TS: time.Now().UTC()}
		if err := AppendMessage(m.daemonRoot, body.To, msg); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"delivered": true,
			"id":        msg.ID,
			"ts":        msg.TS,
		})
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
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		// `Transfer-Encoding: chunked` is set automatically by net/http when
		// we don't set Content-Length and write incrementally — we still set
		// the Cache-Control hint to make intermediaries unlikely to buffer.
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		if err := StreamLogs(r.Context(), w, m.daemonRoot, instance, follow); err != nil {
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
		dispatchChannelRoute(w, r, channels, name, verb)
	})

	return mux
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
func dispatchChannelRoute(w http.ResponseWriter, r *http.Request, channels *ChannelStore, name, verb string) {
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
		res, err := channels.Publish(name, body.Sender, body.Body)
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
		cursor, fresh, err := channels.Subscribe(name, body.Instance)
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
		removed, err := channels.Unsubscribe(name, body.Instance)
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
		if err := channels.Ack(name, body.Instance, body.Cursor); err != nil {
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
		res, err := channels.Drain(r.Context(), name, instance, since, wait)
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
		removed, err := channels.Delete(name)
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

func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return errors.New("empty body")
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
