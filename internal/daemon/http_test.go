package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTP_Dispatch_StopList(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)

	srv := httptest.NewServer(Handler(m))
	defer srv.Close()

	// POST /v1/dispatch
	body := `{"agent":"worker","name":"w-1","prompt":"hi","workspace":"` + t.TempDir() + `"}`
	resp := mustPost(t, srv.URL+"/v1/dispatch", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dispatch status: got %d, body=%s", resp.StatusCode, readBody(t, resp))
	}
	var dispBody struct {
		InstanceID string    `json:"instance_id"`
		StartedAt  time.Time `json:"started_at"`
		PID        int       `json:"pid"`
		SessionID  string    `json:"session_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&dispBody); err != nil {
		t.Fatalf("dispatch body: %v", err)
	}
	if dispBody.InstanceID != "w-1" {
		t.Errorf("instance_id: got %s", dispBody.InstanceID)
	}
	if dispBody.PID == 0 || dispBody.SessionID == "" {
		t.Errorf("missing pid/session: %+v", dispBody)
	}

	// GET /v1/instances
	resp = mustGet(t, srv.URL+"/v1/instances")
	var list []*Metadata
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("instances body: %v", err)
	}
	if len(list) != 1 || list[0].Instance != "w-1" {
		t.Errorf("instances: got %+v", list)
	}
	if list[0].Status != StatusRunning {
		t.Errorf("status: got %s want running", list[0].Status)
	}

	// POST /v1/stop
	resp = mustPost(t, srv.URL+"/v1/stop", `{"instance":"w-1"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stop status: got %d, body=%s", resp.StatusCode, readBody(t, resp))
	}
	waitForStatusNot(t, m, "w-1", StatusRunning)
}

func TestHTTP_DispatchValidation(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, newFakeSpawner(time.Second).spawn)
	srv := httptest.NewServer(Handler(m))
	defer srv.Close()

	cases := []struct {
		name string
		body string
	}{
		{"missing agent", `{"name":"x","workspace":"/tmp"}`},
		{"missing name", `{"agent":"w","workspace":"/tmp"}`},
		{"missing workspace", `{"agent":"w","name":"x"}`},
		{"bad json", `{not-json}`},
		{"unknown field", `{"agent":"w","name":"x","workspace":"/tmp","extra":1}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := mustPost(t, srv.URL+"/v1/dispatch", c.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status: got %d want 400 for %s", resp.StatusCode, c.name)
			}
		})
	}
}

func TestHTTP_StartResumesSession(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	srv := httptest.NewServer(Handler(m))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/dispatch",
		`{"agent":"manager","name":"mgr","workspace":"`+t.TempDir()+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dispatch: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var disp struct {
		SessionID string `json:"session_id"`
	}
	json.NewDecoder(resp.Body).Decode(&disp)

	// Stop and wait for finalisation.
	mustPost(t, srv.URL+"/v1/stop", `{"instance":"mgr"}`)
	waitForStatusNot(t, m, "mgr", StatusRunning)

	// Start.
	resp = mustPost(t, srv.URL+"/v1/start", `{"instance":"mgr"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("start: %d %s", resp.StatusCode, readBody(t, resp))
	}

	args := fake.lastCall()
	foundResume := false
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--resume" && args[i+1] == disp.SessionID {
			foundResume = true
		}
	}
	if !foundResume {
		t.Errorf("expected --resume %s, got: %v", disp.SessionID, args)
	}

	mustPost(t, srv.URL+"/v1/stop", `{"instance":"mgr"}`)
	waitForStatusNot(t, m, "mgr", StatusRunning)
}

func TestHTTP_MethodGuards(t *testing.T) {
	m := NewInstanceManager(t.TempDir(), newFakeSpawner(time.Second).spawn)
	srv := httptest.NewServer(Handler(m))
	defer srv.Close()

	// GET on a POST endpoint
	resp := mustGet(t, srv.URL+"/v1/dispatch")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("dispatch GET: got %d want 405", resp.StatusCode)
	}
	// POST on a GET endpoint
	resp = mustPost(t, srv.URL+"/v1/instances", `{}`)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("instances POST: got %d want 405", resp.StatusCode)
	}
}

func TestHTTP_InstancesEmptyArray(t *testing.T) {
	m := NewInstanceManager(t.TempDir(), nil)
	srv := httptest.NewServer(Handler(m))
	defer srv.Close()
	resp := mustGet(t, srv.URL+"/v1/instances")
	body := readBody(t, resp)
	if !strings.HasPrefix(strings.TrimSpace(body), "[") {
		t.Errorf("expected JSON array, got %q", body)
	}
}

func TestHTTP_Message_AppendsToMailbox(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, nil)
	srv := httptest.NewServer(Handler(m))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/message", `{"to":"worker-1","from":"manager","body":"hello"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var rb struct {
		Delivered bool   `json:"delivered"`
		ID        string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rb); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !rb.Delivered {
		t.Errorf("delivered=false")
	}
	if rb.ID == "" {
		t.Errorf("missing id")
	}

	got, err := ReadMessages(root, "worker-1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("messages: got %d want 1", len(got))
	}
	if got[0].Body != "hello" || got[0].From != "manager" || got[0].To != "worker-1" {
		t.Errorf("message: %+v", got[0])
	}
}

func TestHTTP_Message_Validation(t *testing.T) {
	m := NewInstanceManager(t.TempDir(), nil)
	srv := httptest.NewServer(Handler(m))
	defer srv.Close()

	cases := []struct {
		name string
		body string
	}{
		{"missing to", `{"body":"hi"}`},
		{"missing body", `{"to":"x"}`},
		{"unknown field", `{"to":"x","body":"y","foo":1}`},
		{"bad json", `{not-json}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := mustPost(t, srv.URL+"/v1/message", c.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status: %d want 400", resp.StatusCode)
			}
		})
	}
}

func TestHTTP_Message_MethodGuard(t *testing.T) {
	m := NewInstanceManager(t.TempDir(), nil)
	srv := httptest.NewServer(Handler(m))
	defer srv.Close()
	resp := mustGet(t, srv.URL+"/v1/message")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status: %d want 405", resp.StatusCode)
	}
}

func mustPost(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	return buf.String()
}
