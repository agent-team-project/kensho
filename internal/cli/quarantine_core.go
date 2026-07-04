package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/spf13/cobra"
)

type quarantineListColumn[T any] struct {
	Header string
	Value  func(T) string
}

type quarantineSortLess[T any] func(left, right T) (less bool, decided bool)

type quarantineRestoreConfig[T any, R any] struct {
	Root              func(teamDir string) string
	Normalize         func(raw string) (string, error)
	Inspect           func(root, rel string) (T, error)
	SafePath          func(root, rel string) (string, error)
	Path              func(T) string
	RestorePath       func(T) string
	Restorable        func(T) bool
	Problem           func(T) string
	NewResult         func(T, bool, bool) R
	MarkRestored      func(*R)
	PruneAfterRestore bool
	Prune             func(root, dir string)
}

type quarantineDropConfig[T any, R any] struct {
	Root        func(teamDir string) string
	Normalize   func(raw string) (string, error)
	Inspect     func(root, rel string) (T, error)
	SafePath    func(root, rel string) (string, error)
	Path        func(T) string
	NewResult   func(T, bool) R
	MarkDropped func(*R)
	Prune       func(root, dir string)
}

func listQuarantineItems[T any](resourceRoot, quarantineDir, extension string, inspect func(string, string) (T, error), itemPath func(T) string) ([]T, error) {
	root := filepath.Join(resourceRoot, quarantineDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	var items []T
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), extension) {
			return nil
		}
		rel, err := filepath.Rel(resourceRoot, path)
		if err != nil {
			return err
		}
		item, err := inspect(resourceRoot, rel)
		if err != nil {
			return err
		}
		items = append(items, item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		return itemPath(items[i]) < itemPath(items[j])
	})
	return items, nil
}

func filterQuarantineRestorable[T any](items []T, restorableOnly, unrestorableOnly bool, restorable func(T) bool) []T {
	if !restorableOnly && !unrestorableOnly {
		return items
	}
	out := make([]T, 0, len(items))
	for _, item := range items {
		if restorableOnly && !restorable(item) {
			continue
		}
		if unrestorableOnly && restorable(item) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func prepareQuarantineItems[T any](items []T, sortMode string, limit int, sortItems func([]T, string)) []T {
	sortItems(items, sortMode)
	return limitQuarantineItems(items, limit)
}

func limitQuarantineItems[T any](items []T, limit int) []T {
	if limit <= 0 || limit >= len(items) {
		return items
	}
	return items[:limit]
}

func parseQuarantineSort(raw string, allowed []string, errorMessage string) (string, error) {
	sortMode := strings.ToLower(strings.TrimSpace(raw))
	if sortMode == "" {
		return "path", nil
	}
	for _, mode := range allowed {
		if sortMode == mode {
			return sortMode, nil
		}
	}
	return "", errors.New(errorMessage)
}

func sortQuarantineItems[T any](items []T, sortMode string, itemPath func(T) string, lessByMode map[string]quarantineSortLess[T]) {
	sortMode = strings.ToLower(strings.TrimSpace(sortMode))
	if sortMode == "" {
		sortMode = "path"
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if less, ok := lessByMode[sortMode]; ok {
			if result, decided := less(left, right); decided {
				return result
			}
		}
		return itemPath(left) < itemPath(right)
	})
}

func quarantineStringLess[T any](value func(T) string) quarantineSortLess[T] {
	return func(left, right T) (bool, bool) {
		leftValue, rightValue := value(left), value(right)
		if leftValue == rightValue {
			return false, false
		}
		return leftValue < rightValue, true
	}
}

func quarantineRankedStringLess[T any](value func(T) string, rank func(string) int) quarantineSortLess[T] {
	return func(left, right T) (bool, bool) {
		leftValue, rightValue := value(left), value(right)
		if leftValue == rightValue {
			return false, false
		}
		return rank(leftValue) < rank(rightValue), true
	}
}

func quarantineTimeDescLess[T any](value func(T) time.Time) quarantineSortLess[T] {
	return func(left, right T) (bool, bool) {
		leftValue, rightValue := value(left), value(right)
		if leftValue.Equal(rightValue) {
			return false, false
		}
		return leftValue.After(rightValue), true
	}
}

func quarantineIntDescLess[T any](value func(T) int) quarantineSortLess[T] {
	return func(left, right T) (bool, bool) {
		leftValue, rightValue := value(left), value(right)
		if leftValue == rightValue {
			return false, false
		}
		return leftValue > rightValue, true
	}
}

func quarantineInt64DescLess[T any](value func(T) int64) quarantineSortLess[T] {
	return func(left, right T) (bool, bool) {
		leftValue, rightValue := value(left), value(right)
		if leftValue == rightValue {
			return false, false
		}
		return leftValue > rightValue, true
	}
}

func quarantineBoolTrueFirstLess[T any](value func(T) bool) quarantineSortLess[T] {
	return func(left, right T) (bool, bool) {
		leftValue, rightValue := value(left), value(right)
		if leftValue == rightValue {
			return false, false
		}
		return leftValue && !rightValue, true
	}
}

func normalizeQuarantinePath(raw, quarantineDir, extension, shapeMessage string, state func(string) string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(raw))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe quarantine path %q", raw)
	}
	slash := filepath.ToSlash(clean)
	if !strings.HasPrefix(slash, quarantineDir+"/") {
		slash = quarantineDir + "/" + slash
	}
	if state(filepath.FromSlash(slash)) == "" {
		return "", errors.New(shapeMessage)
	}
	if !strings.HasSuffix(slash, extension) {
		return "", fmt.Errorf("quarantine path must name a %s file", extension)
	}
	return filepath.FromSlash(slash), nil
}

func pruneEmptyQuarantineDirs(resourceRoot, dir, quarantineDir string) {
	stop := filepath.Join(resourceRoot, quarantineDir)
	for {
		if dir == "" || dir == "." || dir == stop || !strings.HasPrefix(dir, stop) {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

func parseQuarantineCommandFormat(cmd *cobra.Command, command, format, templateName string, jsonOut bool) (*template.Template, error) {
	if format != "" && jsonOut {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: --format cannot be combined with --json.\n", command)
		return nil, exitErr(2)
	}
	tmpl, err := parseQuarantineFormat(format, templateName)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", command, err)
		return nil, exitErr(2)
	}
	return tmpl, nil
}

func parseQuarantineFormat(format, templateName string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New(templateName).Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderQuarantineList[T any](w io.Writer, items []T, jsonOut bool, tmpl *template.Template, emptyMessage string, columns []quarantineListColumn[T]) error {
	if jsonOut {
		if items == nil {
			items = []T{}
		}
		return json.NewEncoder(w).Encode(items)
	}
	if tmpl != nil {
		for _, item := range items {
			if err := renderQuarantineTemplate(w, item, tmpl); err != nil {
				return err
			}
		}
		return nil
	}
	if len(items) == 0 {
		fmt.Fprintln(w, emptyMessage)
		return nil
	}
	headers := make([]string, 0, len(columns))
	for _, column := range columns {
		headers = append(headers, column.Header)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, item := range items {
		values := make([]string, 0, len(columns))
		for _, column := range columns {
			values = append(values, column.Value(item))
		}
		fmt.Fprintln(tw, strings.Join(values, "\t"))
	}
	return tw.Flush()
}

func renderQuarantineSummary[S any](w io.Writer, summary S, jsonOut bool, line func(S) string) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(summary)
	}
	fmt.Fprintln(w, line(summary))
	return nil
}

func quarantineRestorableText(restorable bool) string {
	if restorable {
		return "yes"
	}
	return "no"
}

func renderQuarantineResult[R any](w io.Writer, result R, jsonOut bool, tmpl *template.Template, renderLine func(io.Writer, R)) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if tmpl != nil {
		return renderQuarantineTemplate(w, result, tmpl)
	}
	renderLine(w, result)
	return nil
}

func renderQuarantineResults[R any](w io.Writer, results []R, jsonOut bool, tmpl *template.Template, emptyMessage string, renderLine func(io.Writer, R)) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(results)
	}
	if tmpl != nil {
		for _, result := range results {
			if err := renderQuarantineTemplate(w, result, tmpl); err != nil {
				return err
			}
		}
		return nil
	}
	if len(results) == 0 {
		fmt.Fprintln(w, emptyMessage)
		return nil
	}
	for _, result := range results {
		renderLine(w, result)
	}
	return nil
}

func quarantineResultsHaveDryRunAction[R any](results []R, action string, matches func(R, string) bool) bool {
	for _, result := range results {
		if matches(result, action) {
			return true
		}
	}
	return false
}

func renderQuarantineListCommands[T any](w io.Writer, items []T, actions func(T) []string, defaultActions func(T) []string, scope operatorCommandScope) error {
	out := make([]string, 0, len(items)*2)
	for _, item := range items {
		if actions != nil {
			out = append(out, actions(item)...)
			continue
		}
		out = append(out, defaultActions(item)...)
	}
	return renderOperatorActionCommands(w, out, scope)
}

func quarantineShowActions(resource, path string, restorable bool, scopeJob, pipeline, team string) []string {
	if path == "" {
		return nil
	}
	var prefix string
	if scopeJob != "" {
		prefix = fmt.Sprintf("agent-team job %s quarantine %%s %s %s", resource, scopeJob, path)
	} else if pipeline != "" {
		prefix = fmt.Sprintf("agent-team pipeline %s quarantine %%s %s %s", resource, pipeline, path)
	} else if team != "" {
		prefix = fmt.Sprintf("agent-team team %s quarantine %%s %s %s", resource, team, path)
	} else {
		prefix = fmt.Sprintf("agent-team %s quarantine %%s %s", resource, path)
	}
	actions := []string{}
	if restorable {
		actions = append(actions, fmt.Sprintf(prefix, "restore"))
	}
	actions = append(actions, fmt.Sprintf(prefix, "drop"))
	return actions
}

func restoreQuarantineItem[T any, R any](teamDir, rawPath string, dryRun, force bool, cfg quarantineRestoreConfig[T, R]) (R, error) {
	var zero R
	root := cfg.Root(teamDir)
	rel, err := cfg.Normalize(rawPath)
	if err != nil {
		return zero, err
	}
	item, err := cfg.Inspect(root, rel)
	if err != nil {
		return zero, err
	}
	if !cfg.Restorable(item) {
		return zero, fmt.Errorf("%s is not restorable: %s", cfg.Path(item), cfg.Problem(item))
	}
	source, err := cfg.SafePath(root, cfg.Path(item))
	if err != nil {
		return zero, err
	}
	destination, err := cfg.SafePath(root, cfg.RestorePath(item))
	if err != nil {
		return zero, err
	}
	if _, err := os.Stat(destination); err == nil && !force {
		return zero, fmt.Errorf("%s already exists; pass --force to overwrite it", cfg.RestorePath(item))
	} else if err != nil && !os.IsNotExist(err) {
		return zero, err
	}
	result := cfg.NewResult(item, dryRun, force)
	if dryRun {
		return result, nil
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return result, err
	}
	if force {
		_ = os.Remove(destination)
	}
	if err := os.Rename(source, destination); err != nil {
		return result, err
	}
	cfg.MarkRestored(&result)
	if cfg.PruneAfterRestore && cfg.Prune != nil {
		cfg.Prune(root, filepath.Dir(source))
	}
	return result, nil
}

func restoreQuarantineItemBatch[T any, R any](teamDir string, items []T, dryRun, force bool, sortMode string, limit int, prepare func([]T, string, int) []T, path func(T) string, restore func(string, string, bool, bool) (R, error)) ([]R, error) {
	items = prepare(items, sortMode, limit)
	results := make([]R, 0, len(items))
	for _, item := range items {
		result, err := restore(teamDir, path(item), dryRun, force)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func dropQuarantinePath[T any, R any](teamDir, rawPath string, dryRun bool, cfg quarantineDropConfig[T, R]) (R, error) {
	var zero R
	root := cfg.Root(teamDir)
	rel, err := cfg.Normalize(rawPath)
	if err != nil {
		return zero, err
	}
	item, err := cfg.Inspect(root, rel)
	if err != nil {
		return zero, err
	}
	return dropQuarantineItem(root, item, dryRun, cfg)
}

func dropQuarantineItem[T any, R any](root string, item T, dryRun bool, cfg quarantineDropConfig[T, R]) (R, error) {
	result := cfg.NewResult(item, dryRun)
	if dryRun {
		return result, nil
	}
	source, err := cfg.SafePath(root, cfg.Path(item))
	if err != nil {
		return result, err
	}
	if err := os.Remove(source); err != nil {
		return result, err
	}
	if cfg.Prune != nil {
		cfg.Prune(root, filepath.Dir(source))
	}
	cfg.MarkDropped(&result)
	return result, nil
}

func dropQuarantineItemBatch[T any, R any](root string, items []T, dryRun bool, olderThan time.Duration, sortMode string, limit int, now time.Time, sortItems func([]T, string), modTime func(T) time.Time, drop func(string, T, bool) (R, error)) ([]R, error) {
	sortItems(items, sortMode)
	matches := make([]T, 0, len(items))
	for _, item := range items {
		if olderThan > 0 && modTime(item).After(now.Add(-olderThan)) {
			continue
		}
		matches = append(matches, item)
		if limit > 0 && len(matches) >= limit {
			break
		}
	}
	results := make([]R, 0, len(matches))
	for _, item := range matches {
		result, err := drop(root, item, dryRun)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

func renderQuarantineTemplate(w io.Writer, value any, tmpl *template.Template) error {
	if err := tmpl.Execute(w, value); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}
