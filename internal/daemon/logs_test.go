package daemon

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// writeChildLog is a test helper that puts content into the path StreamLogs
// expects. Mirrors the on-disk shape the spawner creates.
func writeChildLog(t *testing.T, root, instance, content string) {
	t.Helper()
	dir := instanceDir(root, instance)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "child.log"), []byte(content), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
}

func appendChildLog(t *testing.T, root, instance, content string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(instanceDir(root, instance), "child.log"),
		os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestLogs_NonFollowDumpsCurrent(t *testing.T) {
	root := t.TempDir()
	writeChildLog(t, root, "w", "hello world\nline two\n")
	var buf bytes.Buffer
	if err := StreamLogs(context.Background(), &buf, root, "w", false); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := buf.String(); got != "hello world\nline two\n" {
		t.Errorf("body: got %q", got)
	}
}

func TestLogs_FollowStreamsAppends(t *testing.T) {
	prev := logTailInterval
	logTailInterval = 10 * time.Millisecond
	t.Cleanup(func() { logTailInterval = prev })

	root := t.TempDir()
	writeChildLog(t, root, "w", "first\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use a flushing recorder so we can read incrementally.
	rec := newSyncBuf()
	done := make(chan error, 1)
	go func() {
		done <- StreamLogs(ctx, rec, root, "w", true)
	}()

	// Initial dump should land immediately.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.String(), "first\n") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(rec.String(), "first\n") {
		t.Fatalf("did not see initial content; got %q", rec.String())
	}

	// Append more — follow loop should pick it up.
	appendChildLog(t, root, "w", "second\n")
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.String(), "second\n") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(rec.String(), "second\n") {
		t.Fatalf("did not see appended content; got %q", rec.String())
	}

	// Cancel terminates promptly.
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("StreamLogs did not return after cancel")
	}
}

func TestLogs_MissingInstanceErrors(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer
	err := StreamLogs(context.Background(), &buf, root, "nobody", false)
	if err == nil {
		t.Errorf("want error for missing log")
	}
}

func TestHTTP_Logs_Endpoint(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, nil)
	srv := httptest.NewServer(Handler(m))
	defer srv.Close()

	// 404 when no log file exists.
	resp, err := http.Get(srv.URL + "/v1/logs/missing")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing: got %d want 404", resp.StatusCode)
	}
	resp.Body.Close()

	// Non-follow returns the file.
	writeChildLog(t, root, "w", "abc\ndef\n")
	resp, err = http.Get(srv.URL + "/v1/logs/w")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: %d", resp.StatusCode)
	}
	body := readBody(t, resp)
	if body != "abc\ndef\n" {
		t.Errorf("body: got %q", body)
	}
}

func TestHTTP_Logs_FollowReturnsOnContextEnd(t *testing.T) {
	prev := logTailInterval
	logTailInterval = 10 * time.Millisecond
	t.Cleanup(func() { logTailInterval = prev })

	root := t.TempDir()
	writeChildLog(t, root, "w", "seed\n")
	m := NewInstanceManager(root, nil)
	srv := httptest.NewServer(Handler(m))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/v1/logs/w?follow=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Context cancel during read is OK — that's the success path.
		return
	}
	defer resp.Body.Close()
	buf := make([]byte, 64)
	for {
		_, rerr := resp.Body.Read(buf)
		if rerr != nil {
			break
		}
	}
}

// syncBuf is a thread-safe bytes.Buffer used for tests that race a writer
// goroutine against the assertion goroutine. Implements both io.Writer and
// http.Flusher (the latter as a no-op).
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func newSyncBuf() *syncBuf { return &syncBuf{} }

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func (s *syncBuf) Flush() {}
