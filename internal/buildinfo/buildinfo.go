// Package buildinfo exposes the binary identity Go records for module builds.
package buildinfo

import (
	"fmt"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
)

// HeaderName is the daemon request header carrying a CLI build identity.
const HeaderName = "X-Agent-Team-Build"

// Info is the stable build identity shared by the CLI and daemon API.
type Info struct {
	Version  string `json:"version,omitempty"`
	Revision string `json:"revision,omitempty"`
	Time     string `json:"time,omitempty"`
	Modified bool   `json:"modified,omitempty"`
}

// Current returns the current binary identity, combining the release version
// supplied by the caller with VCS settings embedded by the Go toolchain.
func Current(version string) Info {
	out := Info{Version: strings.TrimSpace(version)}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range bi.Settings {
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
	return out
}

// Empty reports whether the identity has no useful comparison or display
// fields. It is used when talking to older daemons that predate build info.
func (i Info) Empty() bool {
	return strings.TrimSpace(i.Version) == "" &&
		strings.TrimSpace(i.Revision) == "" &&
		strings.TrimSpace(i.Time) == "" &&
		!i.Modified
}

// ShortRevision returns the first 12 hex characters when a VCS revision is
// available. Twelve matches the repo's existing short SHA convention.
func (i Info) ShortRevision() string {
	rev := strings.TrimSpace(i.Revision)
	if len(rev) <= 12 {
		return rev
	}
	return rev[:12]
}

// Display returns a compact human-readable identity for status and warnings.
func (i Info) Display() string {
	parts := make([]string, 0, 3)
	if v := strings.TrimSpace(i.Version); v != "" {
		parts = append(parts, v)
	}
	if rev := i.ShortRevision(); rev != "" {
		parts = append(parts, "rev "+rev)
	}
	if i.Modified {
		parts = append(parts, "modified")
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
	if rev := i.ShortRevision(); rev != "" {
		line += " rev " + rev
	}
	if t := strings.TrimSpace(i.Time); t != "" {
		line += " " + t
	}
	if i.Modified {
		line += " modified"
	}
	return line
}

// HeaderValue serializes the identity for HeaderName.
func (i Info) HeaderValue() string {
	values := url.Values{}
	if v := strings.TrimSpace(i.Version); v != "" {
		values.Set("version", v)
	}
	if rev := strings.TrimSpace(i.Revision); rev != "" {
		values.Set("revision", rev)
	}
	if t := strings.TrimSpace(i.Time); t != "" {
		values.Set("time", t)
	}
	if i.Modified {
		values.Set("modified", "true")
	}
	return values.Encode()
}

// ParseHeaderValue parses a HeaderName value into a build identity.
func ParseHeaderValue(raw string) (Info, error) {
	values, err := url.ParseQuery(strings.TrimSpace(raw))
	if err != nil {
		return Info{}, err
	}
	out := Info{
		Version:  strings.TrimSpace(values.Get("version")),
		Revision: strings.TrimSpace(values.Get("revision")),
		Time:     strings.TrimSpace(values.Get("time")),
	}
	if rawModified := strings.TrimSpace(values.Get("modified")); rawModified != "" {
		modified, err := strconv.ParseBool(rawModified)
		if err != nil {
			return Info{}, err
		}
		out.Modified = modified
	}
	return out, nil
}

// ComparisonKey is stable for equality checks. Revision-bearing builds compare
// by all recorded VCS fields; builds without revision fall back to version.
func (i Info) ComparisonKey() string {
	if rev := strings.TrimSpace(i.Revision); rev != "" {
		return fmt.Sprintf("version=%s revision=%s time=%s modified=%t",
			strings.TrimSpace(i.Version), rev, strings.TrimSpace(i.Time), i.Modified)
	}
	return "version=" + strings.TrimSpace(i.Version)
}

// Equivalent reports whether two non-empty identities represent the same build.
func Equivalent(a, b Info) bool {
	if a.Empty() || b.Empty() {
		return true
	}
	return a.ComparisonKey() == b.ComparisonKey()
}
