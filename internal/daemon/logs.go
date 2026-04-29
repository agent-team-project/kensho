package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"time"
)

// logTailInterval controls the polling cadence for follow-mode log streaming.
// Variable so tests can shorten it to keep deterministic runs fast.
var logTailInterval = 200 * time.Millisecond

// StreamLogs writes the contents of an instance's child.log to w. With
// follow=false, it dumps the file from start to its current end and returns.
// With follow=true, after the initial dump it polls for newly-appended bytes
// and writes them until ctx is cancelled or the underlying connection is
// closed.
//
// Encoding contract: chunked Transfer-Encoding text. The handler must already
// have set the response headers (Content-Type / Transfer-Encoding) before
// calling. We rely on http.ResponseWriter being a Flusher (httptest, http2,
// chi all implement it for chunked text); if it's not, we still send the
// whole thing in one shot — correct, just unbuffered.
func StreamLogs(ctx context.Context, w io.Writer, daemonRoot, instance string, follow bool) error {
	if instance == "" {
		return errors.New("logs: instance is required")
	}
	logPath := childLogPath(daemonRoot, instance)
	f, err := os.Open(logPath)
	if err != nil {
		return err
	}
	defer f.Close()

	flusher, _ := w.(http.Flusher)

	// Initial dump up to current EOF.
	if _, err := io.Copy(w, f); err != nil {
		return err
	}
	if flusher != nil {
		flusher.Flush()
	}
	if !follow {
		return nil
	}

	// Tail loop: read any newly-appended bytes since last position, write,
	// flush, sleep. ctx cancel terminates.
	buf := make([]byte, 32*1024)
	ticker := time.NewTicker(logTailInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		for {
			n, rerr := f.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return werr
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
			if rerr != nil {
				if errors.Is(rerr, io.EOF) {
					break
				}
				return rerr
			}
		}
	}
}

// childLogPath is the on-disk path for an instance's stdout/stderr log,
// matching what the spawner writes. Kept here so mailbox/logs/instance code
// can use it without crossing package files needlessly.
func childLogPath(daemonRoot, instance string) string {
	// The spawner writes to filepath.Join(instanceDir, "child.log"). We
	// duplicate that constant here rather than exporting an accessor — the
	// path is part of the on-disk contract documented in orchestrator.md.
	return instanceDir(daemonRoot, instance) + string(os.PathSeparator) + "child.log"
}

// logsExist reports whether the log file is present (so the http layer can
// distinguish 404 from 500). Returns nil-error/true for present, nil-error/
// false for absent, non-nil error for unexpected stat failures.
func logsExist(daemonRoot, instance string) (bool, error) {
	_, err := os.Stat(childLogPath(daemonRoot, instance))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("logs: stat: %w", err)
}
