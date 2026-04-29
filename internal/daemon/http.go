package daemon

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// Handler builds the daemon's http.Handler. Routes are explicit (no library
// router) — the surface is small and `http.ServeMux` is sufficient. All paths
// are versioned `/v1/...` per orchestrator.md Open Q #7.
func Handler(m *InstanceManager) http.Handler {
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

	return mux
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
