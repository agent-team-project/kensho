// Package origin stores and renders provenance for agent-team resources.
package origin

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Envelope identifies where a resource came from and who owns it.
type Envelope struct {
	Project  string `json:"project,omitempty" toml:"project,omitempty"`
	Team     string `json:"team,omitempty" toml:"team,omitempty"`
	Instance string `json:"instance,omitempty" toml:"instance,omitempty"`
	Agent    string `json:"agent,omitempty" toml:"agent,omitempty"`
	Job      string `json:"job,omitempty" toml:"job,omitempty"`
	Trigger  string `json:"trigger,omitempty" toml:"trigger,omitempty"`
	Build    string `json:"build,omitempty" toml:"build,omitempty"`
}

// Clean trims whitespace from every field.
func (e Envelope) Clean() Envelope {
	return Envelope{
		Project:  strings.TrimSpace(e.Project),
		Team:     strings.TrimSpace(e.Team),
		Instance: strings.TrimSpace(e.Instance),
		Agent:    strings.TrimSpace(e.Agent),
		Job:      strings.TrimSpace(e.Job),
		Trigger:  strings.TrimSpace(e.Trigger),
		Build:    strings.TrimSpace(e.Build),
	}
}

// Empty reports whether no origin fields are populated.
func (e Envelope) Empty() bool {
	e = e.Clean()
	return e.Project == "" && e.Team == "" && e.Instance == "" &&
		e.Agent == "" && e.Job == "" && e.Trigger == "" && e.Build == ""
}

// Merge fills blank fields in primary from fallback.
func Merge(primary, fallback Envelope) Envelope {
	primary = primary.Clean()
	fallback = fallback.Clean()
	if primary.Project == "" {
		primary.Project = fallback.Project
	}
	if primary.Team == "" {
		primary.Team = fallback.Team
	}
	if primary.Instance == "" {
		primary.Instance = fallback.Instance
	}
	if primary.Agent == "" {
		primary.Agent = fallback.Agent
	}
	if primary.Job == "" {
		primary.Job = fallback.Job
	}
	if primary.Trigger == "" {
		primary.Trigger = fallback.Trigger
	}
	if primary.Build == "" {
		primary.Build = fallback.Build
	}
	return primary
}

// Footer renders the machine-parseable footer used on external writes.
func Footer(e Envelope) string {
	e = e.Clean()
	if e.Empty() {
		return ""
	}
	parts := []string{}
	for _, item := range []struct {
		key   string
		value string
	}{
		{"project", e.Project},
		{"team", e.Team},
		{"instance", e.Instance},
		{"agent", e.Agent},
		{"job", e.Job},
		{"trigger", e.Trigger},
		{"build", e.Build},
	} {
		if item.value != "" {
			parts = append(parts, item.key+"="+quoteFooterValue(item.value))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "agent-team-origin: " + strings.Join(parts, " ")
}

// AppendFooter adds the provenance footer unless one is already present.
func AppendFooter(body string, e Envelope) string {
	footer := Footer(e)
	body = strings.TrimRight(body, "\n")
	if footer == "" || strings.Contains(body, "agent-team-origin:") {
		return body
	}
	if body == "" {
		return footer
	}
	return body + "\n\n---\n" + footer
}

func quoteFooterValue(value string) string {
	if value == "" {
		return ""
	}
	if strings.ContainsAny(value, " \t\r\n") {
		return strconv.Quote(value)
	}
	return value
}

// TriggerFromEvent returns the stable trigger id for a topology event payload.
func TriggerFromEvent(eventType string, payload map[string]any) string {
	eventType = strings.TrimSpace(eventType)
	if eventType == "schedule" || payloadString(payload, "source") == "schedule" {
		if name := payloadString(payload, "name"); name != "" {
			return "schedule:" + name
		}
	}
	if pipeline := payloadString(payload, "pipeline"); pipeline != "" {
		if step := payloadString(payload, "pipeline_step"); step != "" {
			return "pipeline:" + pipeline + ":" + step
		}
		return "pipeline:" + pipeline
	}
	if source := payloadString(payload, "source"); source != "" && eventType == "" {
		return source
	}
	return eventType
}

// ConfigPath returns the repo-local config path for a team directory.
func ConfigPath(teamDir string) string {
	return filepath.Join(teamDir, "config.toml")
}

// ProjectID reads [project].id from config.toml. Missing config or key returns
// an empty id with no error.
func ProjectID(teamDir string) (string, error) {
	cfg := ConfigPath(teamDir)
	if _, err := os.Stat(cfg); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	var raw map[string]any
	if _, err := toml.DecodeFile(cfg, &raw); err != nil {
		return "", err
	}
	project, _ := raw["project"].(map[string]any)
	id, _ := project["id"].(string)
	return strings.TrimSpace(id), nil
}

// EnsureProjectID creates [project].id when it is missing or empty.
func EnsureProjectID(teamDir string) (string, bool, error) {
	if id, err := ProjectID(teamDir); err != nil {
		return "", false, err
	} else if id != "" {
		return id, false, nil
	}
	id, err := GenerateProjectID()
	if err != nil {
		return "", false, err
	}
	if err := WriteProjectID(teamDir, id); err != nil {
		return "", false, err
	}
	return id, true, nil
}

// GenerateProjectID returns a stable random project id suitable for config.
func GenerateProjectID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	hexed := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexed[:8], hexed[8:12], hexed[12:16], hexed[16:20], hexed[20:]), nil
}

// WriteProjectID inserts or replaces [project].id while preserving the rest of
// the config file.
func WriteProjectID(teamDir, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("origin: project id is required")
	}
	cfg := ConfigPath(teamDir)
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		return err
	}
	body, err := os.ReadFile(cfg)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.WriteFile(cfg, []byte("[project]\nid = "+strconv.Quote(id)+"\n"), 0o644)
		}
		return err
	}
	var raw map[string]any
	if _, err := toml.Decode(string(body), &raw); err != nil {
		return err
	}
	updated := upsertProjectIDText(string(body), id)
	return os.WriteFile(cfg, []byte(updated), 0o644)
}

func upsertProjectIDText(body, id string) string {
	lines := strings.SplitAfter(body, "\n")
	projectStart := -1
	projectEnd := len(lines)
	sectionRE := regexp.MustCompile(`^\s*\[[^\]]+\]\s*(?:#.*)?$`)
	projectRE := regexp.MustCompile(`^\s*\[project\]\s*(?:#.*)?$`)
	idRE := regexp.MustCompile(`^\s*id\s*=`)
	for i, line := range lines {
		trimmed := strings.TrimRight(line, "\r\n")
		if projectStart == -1 {
			if projectRE.MatchString(trimmed) {
				projectStart = i
			}
			continue
		}
		if sectionRE.MatchString(trimmed) {
			projectEnd = i
			break
		}
	}
	idLine := "id = " + strconv.Quote(id) + "\n"
	if projectStart == -1 {
		if body != "" && !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		if strings.TrimSpace(body) != "" {
			body += "\n"
		}
		return body + "[project]\n" + idLine
	}
	for i := projectStart + 1; i < projectEnd; i++ {
		if idRE.MatchString(strings.TrimRight(lines[i], "\r\n")) {
			lines[i] = idLine
			return strings.Join(lines, "")
		}
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:projectStart+1]...)
	out = append(out, idLine)
	out = append(out, lines[projectStart+1:]...)
	return strings.Join(out, "")
}

func payloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	switch v := payload[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		if v == nil {
			return ""
		}
		return strings.TrimSpace(fmt.Sprint(v))
	}
}
