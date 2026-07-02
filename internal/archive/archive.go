package archive

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Directory returns the shared daemon archive directory for a team root.
func Directory(teamDir string) string {
	return filepath.Join(teamDir, "daemon", "archive")
}

// PathForTime returns the monthly archive JSONL path for t.
func PathForTime(teamDir string, t time.Time) string {
	if t.IsZero() {
		t = time.Now().UTC()
	}
	t = t.UTC()
	return filepath.Join(Directory(teamDir), fmt.Sprintf("%04d-%02d.jsonl", t.Year(), int(t.Month())))
}

// AppendJSON appends one JSON value to the monthly archive file selected by t.
func AppendJSON(teamDir string, t time.Time, value any) (string, error) {
	dir := Directory(teamDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("archive: mkdir: %w", err)
	}
	path := PathForTime(teamDir, t)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("archive: open: %w", err)
	}
	enc := json.NewEncoder(f)
	if err := enc.Encode(value); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("archive: encode: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("archive: close: %w", err)
	}
	return path, nil
}

// Files returns archive JSONL files in deterministic order.
func Files(teamDir string) ([]string, error) {
	entries, err := os.ReadDir(Directory(teamDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		files = append(files, filepath.Join(Directory(teamDir), entry.Name()))
	}
	sort.Strings(files)
	return files, nil
}
