package daemon

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
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
			Agent     string `json:"agent"`
			Name      string `json:"name"`
			Prompt    string `json:"prompt"`
			Workspace string `json:"workspace"`
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
