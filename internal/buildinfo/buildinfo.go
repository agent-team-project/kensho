// Package buildinfo exposes immutable source identity embedded in agent-team
// executables. It never consults a source checkout at runtime.
package buildinfo

import (
	bytespkg "bytes"
	gobuildinfo "debug/buildinfo"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
)

// HeaderName is the daemon request header carrying a CLI build identity.
const HeaderName = "X-Agent-Team-Build"

const (
	linkedSourcePrefix = "agent-team-source-v1:git:"
	linkedSourceSuffix = ":end"
	linkedSourceLength = len(linkedSourcePrefix) + 40 + len(linkedSourceSuffix)
)

// LinkedSourceIdentity is set with -ldflags -X for release and explicitly
// revisionless builds. Keep the marker fixed-width so ReadFile can recover it
// from another executable without running that executable.
var LinkedSourceIdentity = "unbound"

// Info is the stable build identity shared by the CLI and daemon API.
type Info struct {
	Version       string `json:"version,omitempty"`
	ModulePath    string `json:"module_path,omitempty"`
	ModuleVersion string `json:"module_version,omitempty"`
	Revision      string `json:"revision,omitempty"`
	Time          string `json:"time,omitempty"`
	Modified      bool   `json:"modified,omitempty"`
	SourceID      string `json:"source_id,omitempty"`
}

// Comparison describes whether two executable identities can be compared and,
// when they can, whether they came from the same source inputs.
type Comparison struct {
	Comparable bool
	Equal      bool
	Reason     string
}

// Current returns identity fixed in the running executable's build metadata.
func Current(version string) Info {
	out := Info{Version: strings.TrimSpace(version)}
	if bi, ok := debug.ReadBuildInfo(); ok {
		out.ModulePath = strings.TrimSpace(bi.Main.Path)
		out.ModuleVersion = strings.TrimSpace(bi.Main.Version)
		applySettings(&out, bi.Settings)
	}
	if sourceID, err := parseLinkedSourceIdentity(LinkedSourceIdentity); err == nil {
		applyLinkedSourceID(&out, sourceID)
	}
	deriveSourceID(&out)
	return out
}

// ReadFile returns identity fixed in another Go executable. In particular it
// recovers the link-time source marker without executing the target or reading
// the checkout from which it was built.
func ReadFile(path string) (Info, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || path == "" {
		return Info{}, errors.New("build identity path is required")
	}
	bi, err := gobuildinfo.ReadFile(path)
	if err != nil {
		return Info{}, err
	}
	out := Info{
		ModulePath:    strings.TrimSpace(bi.Main.Path),
		ModuleVersion: strings.TrimSpace(bi.Main.Version),
	}
	applySettings(&out, bi.Settings)
	marker, err := readLinkedSourceIdentity(path)
	if err != nil {
		return Info{}, err
	}
	if marker != "" {
		applyLinkedSourceID(&out, marker)
	}
	deriveSourceID(&out)
	return out, nil
}

// Empty reports whether the identity has no useful display fields.
func (i Info) Empty() bool {
	return strings.TrimSpace(i.Version) == "" &&
		strings.TrimSpace(i.ModulePath) == "" &&
		strings.TrimSpace(i.ModuleVersion) == "" &&
		strings.TrimSpace(i.Revision) == "" &&
		strings.TrimSpace(i.Time) == "" &&
		!i.Modified &&
		strings.TrimSpace(i.SourceID) == ""
}

// ShortRevision returns the first 12 revision characters when available.
func (i Info) ShortRevision() string {
	rev := strings.TrimSpace(i.Revision)
	if len(rev) <= 12 {
		return rev
	}
	return rev[:12]
}

// ShortSourceID returns a compact immutable source identity for diagnostics.
func (i Info) ShortSourceID() string {
	sourceID, err := immutableSourceID(i)
	if err != nil {
		return ""
	}
	kind, value, _ := strings.Cut(sourceID, ":")
	if kind == "git" && len(value) > 12 {
		value = value[:12]
	}
	return kind + ":" + value
}

// Display returns a compact human-readable identity for status and warnings.
func (i Info) Display() string {
	parts := make([]string, 0, 4)
	if v := strings.TrimSpace(i.Version); v != "" {
		parts = append(parts, v)
	}
	if source := i.ShortSourceID(); source != "" {
		parts = append(parts, "source "+source)
	} else if rev := i.ShortRevision(); rev != "" {
		parts = append(parts, "rev "+rev)
	} else if stableModuleVersion(i.ModuleVersion) != "" {
		parts = append(parts, "module "+strings.TrimSpace(i.ModuleVersion))
	}
	if i.Modified {
		parts = append(parts, "modified")
	}
	if _, err := immutableSourceID(i); err != nil {
		if errors.Is(err, errMissingSourceIdentity) {
			parts = append(parts, "missing provenance")
		} else {
			parts = append(parts, "incomparable provenance")
		}
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, " ")
}

// VersionLine returns the text appended after a binary name in --version.
func (i Info) VersionLine() string {
	line := strings.TrimSpace(i.Version)
	if line == "" {
		line = "unknown"
	}
	if source := i.ShortSourceID(); source != "" {
		line += " source " + source
	} else if rev := i.ShortRevision(); rev != "" {
		line += " rev " + rev
	}
	if t := strings.TrimSpace(i.Time); t != "" {
		line += " " + t
	}
	if i.Modified {
		line += " modified"
	}
	if _, err := immutableSourceID(i); err != nil {
		if errors.Is(err, errMissingSourceIdentity) {
			line += " missing-provenance"
		} else {
			line += " incomparable-provenance"
		}
	}
	return line
}

// HeaderValue serializes the identity for HeaderName.
func (i Info) HeaderValue() string {
	values := url.Values{}
	setValue(values, "version", i.Version)
	setValue(values, "module_path", i.ModulePath)
	setValue(values, "module_version", i.ModuleVersion)
	setValue(values, "revision", i.Revision)
	setValue(values, "time", i.Time)
	setValue(values, "source_id", i.SourceID)
	if i.Modified {
		values.Set("modified", "true")
	}
	return values.Encode()
}

// ParseHeaderValue parses a HeaderName value without silently accepting
// duplicate, unknown, or malformed identity components.
func ParseHeaderValue(raw string) (Info, error) {
	values, err := url.ParseQuery(strings.TrimSpace(raw))
	if err != nil {
		return Info{}, err
	}
	allowed := map[string]bool{
		"version": true, "module_path": true, "module_version": true,
		"revision": true, "time": true, "modified": true, "source_id": true,
	}
	for key, entries := range values {
		if !allowed[key] {
			return Info{}, fmt.Errorf("unknown build identity field %q", key)
		}
		if len(entries) != 1 {
			return Info{}, fmt.Errorf("duplicate build identity field %q", key)
		}
	}
	out := Info{
		Version:       strings.TrimSpace(values.Get("version")),
		ModulePath:    strings.TrimSpace(values.Get("module_path")),
		ModuleVersion: strings.TrimSpace(values.Get("module_version")),
		Revision:      strings.TrimSpace(values.Get("revision")),
		Time:          strings.TrimSpace(values.Get("time")),
		SourceID:      strings.TrimSpace(values.Get("source_id")),
	}
	if rawModified := strings.TrimSpace(values.Get("modified")); rawModified != "" {
		modified, err := strconv.ParseBool(rawModified)
		if err != nil {
			return Info{}, fmt.Errorf("modified: %w", err)
		}
		out.Modified = modified
	}
	if out.SourceID != "" {
		if err := validateSourceID(out.SourceID); err != nil {
			return Info{}, err
		}
	}
	deriveSourceID(&out)
	return out, nil
}

// Compare applies the one source-identity rule used by wire handshakes and
// executable-file inspection.
func Compare(a, b Info) Comparison {
	aID, aErr := immutableSourceID(a)
	bID, bErr := immutableSourceID(b)
	if aErr != nil || bErr != nil {
		reason := "missing build provenance"
		if aErr != nil && !errors.Is(aErr, errMissingSourceIdentity) || bErr != nil && !errors.Is(bErr, errMissingSourceIdentity) {
			reason = "malformed or mutable build provenance"
		}
		return Comparison{Reason: reason}
	}
	return Comparison{Comparable: true, Equal: aID == bID}
}

// ComparisonKey is stable for logging and de-duplication.
func (i Info) ComparisonKey() string {
	if sourceID, err := immutableSourceID(i); err == nil {
		return sourceID
	}
	return "incomparable"
}

// Equivalent is the compatibility predicate used by read-only diagnostics.
// Activation-sensitive code must use Compare: Equivalent preserves equality
// for identical legacy/test records that predate immutable source identity.
func Equivalent(a, b Info) bool {
	comparison := Compare(a, b)
	if comparison.Comparable {
		return comparison.Equal
	}
	return a == b
}

var errMissingSourceIdentity = errors.New("missing immutable source identity")

func immutableSourceID(i Info) (string, error) {
	sourceID := strings.TrimSpace(i.SourceID)
	if sourceID != "" {
		if err := validateSourceID(sourceID); err != nil {
			return "", err
		}
		// The link marker is authoritative. Linked worktrees can make Go report
		// stale/modified VCS settings for a clean build; rejecting that mutable
		// metadata is exactly why the immutable marker exists.
		return sourceID, nil
	}
	if i.Modified {
		return "", errors.New("mutable VCS build")
	}
	copy := i
	deriveSourceID(&copy)
	sourceID = copy.SourceID
	if sourceID == "" {
		return "", errMissingSourceIdentity
	}
	if err := validateSourceID(sourceID); err != nil {
		return "", err
	}
	return sourceID, nil
}

func deriveSourceID(out *Info) {
	if out == nil || strings.TrimSpace(out.SourceID) != "" || out.Modified {
		return
	}
	if revision := strings.ToLower(strings.TrimSpace(out.Revision)); validHex(revision, 12, 64) {
		out.SourceID = "git:" + revision
		return
	}
	if version := stableModuleVersion(out.ModuleVersion); version != "" && strings.TrimSpace(out.ModulePath) != "" {
		out.SourceID = "module:" + strings.TrimSpace(out.ModulePath) + "@" + version
	}
}

func applyLinkedSourceID(out *Info, sourceID string) {
	if out == nil {
		return
	}
	out.SourceID = sourceID
	linkedRevision := strings.TrimPrefix(sourceID, "git:")
	if !strings.EqualFold(strings.TrimSpace(out.Revision), linkedRevision) {
		// Linked worktrees can point Go's VCS probe at a different checkout.
		// Do not expose that stale tuple alongside the authoritative marker.
		out.Revision = ""
		out.Time = ""
		out.Modified = false
	}
}

func validateSourceID(sourceID string) error {
	sourceID = strings.TrimSpace(sourceID)
	if strings.HasPrefix(sourceID, "git:") {
		if !validHex(strings.TrimPrefix(sourceID, "git:"), 12, 64) {
			return errors.New("malformed git source identity")
		}
		return nil
	}
	if strings.HasPrefix(sourceID, "module:") {
		value := strings.TrimPrefix(sourceID, "module:")
		path, version, ok := strings.Cut(value, "@")
		if !ok || strings.TrimSpace(path) == "" || stableModuleVersion(version) == "" {
			return errors.New("malformed module source identity")
		}
		return nil
	}
	return errors.New("unknown source identity kind")
}

func parseLinkedSourceIdentity(marker string) (string, error) {
	marker = strings.TrimSpace(marker)
	if len(marker) != linkedSourceLength || !strings.HasPrefix(marker, linkedSourcePrefix) || !strings.HasSuffix(marker, linkedSourceSuffix) {
		return "", errors.New("unbound source identity")
	}
	revision := marker[len(linkedSourcePrefix) : len(marker)-len(linkedSourceSuffix)]
	if !validHex(revision, 40, 40) {
		return "", errors.New("malformed linked source identity")
	}
	return "git:" + strings.ToLower(revision), nil
}

func readLinkedSourceIdentity(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	prefix := []byte(linkedSourcePrefix)
	found := ""
	for offset := 0; ; {
		index := bytespkg.Index(body[offset:], prefix)
		if index < 0 {
			break
		}
		start := offset + index
		end := start + linkedSourceLength
		if end <= len(body) {
			if sourceID, parseErr := parseLinkedSourceIdentity(string(body[start:end])); parseErr == nil {
				if found != "" && found != sourceID {
					return "", errors.New("executable contains conflicting linked source identities")
				}
				found = sourceID
			}
		}
		offset = start + len(prefix)
	}
	return found, nil
}

func applySettings(out *Info, settings []debug.BuildSetting) {
	for _, setting := range settings {
		switch setting.Key {
		case "vcs.revision":
			out.Revision = strings.TrimSpace(setting.Value)
		case "vcs.time":
			out.Time = strings.TrimSpace(setting.Value)
		case "vcs.modified":
			out.Modified = setting.Value == "true"
		}
	}
}

func stableModuleVersion(raw string) string {
	version := strings.TrimSpace(raw)
	if version == "" || version == "(devel)" {
		return ""
	}
	return version
}

func validHex(value string, minLength, maxLength int) bool {
	if len(value) < minLength || len(value) > maxLength {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') && !(r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

func setValue(values url.Values, key, value string) {
	if value = strings.TrimSpace(value); value != "" {
		values.Set(key, value)
	}
}
