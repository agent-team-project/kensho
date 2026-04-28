// Package loader parses agent definitions and resolves skills under a
// `.agent_team/` tree. Mirrors the Python loader at
// `cli/src/agent_team/loader.py` — semantics must match exactly.
package loader

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// TeamDirName is the conventional directory name vendored into a consumer repo.
const TeamDirName = ".agent_team"

// AgentLoadError is returned for any agent / skill resolution failure.
// It mirrors the Python `AgentLoadError(RuntimeError)` — callers can use
// `errors.Is(err, ErrAgentLoad)` to detect it.
type AgentLoadError struct {
	Msg string
}

func (e *AgentLoadError) Error() string { return e.Msg }
func (e *AgentLoadError) Is(target error) bool {
	return target == ErrAgentLoad
}

// ErrAgentLoad is a sentinel for `errors.Is` checks.
var ErrAgentLoad = errors.New("agent load error")

func newLoadErr(format string, args ...any) error {
	return &AgentLoadError{Msg: fmt.Sprintf(format, args...)}
}

// Agent is one loaded agent definition.
type Agent struct {
	Name        string
	Description string
	Prompt      string
	// Skills maps skill-name → resolved absolute path (symlinks evaluated).
	Skills map[string]string
}

// LoadAgent reads `<agentDir>/agent.md` (frontmatter + body) and resolves the
// agent's skills relative to `teamDir`.
func LoadAgent(agentDir, teamDir string) (*Agent, error) {
	mdPath := filepath.Join(agentDir, "agent.md")
	st, err := os.Stat(mdPath)
	if err != nil || st.IsDir() {
		return nil, newLoadErr("%s missing — every agent dir needs an agent.md", mdPath)
	}
	raw, err := os.ReadFile(mdPath)
	if err != nil {
		return nil, newLoadErr("%s: %v", mdPath, err)
	}
	fm, body := ParseFrontmatter(string(raw))
	desc := strings.TrimSpace(fm["description"])
	if desc == "" {
		return nil, newLoadErr("%s has no `description` in frontmatter", mdPath)
	}
	skills, err := ResolveSkills(agentDir, teamDir)
	if err != nil {
		return nil, err
	}
	return &Agent{
		Name:        filepath.Base(agentDir),
		Description: desc,
		Prompt:      body,
		Skills:      skills,
	}, nil
}

// LoadAllAgents returns every loadable agent under `<teamDir>/agents/`,
// sorted by directory name.
func LoadAllAgents(teamDir string) ([]*Agent, error) {
	agentsDir := filepath.Join(teamDir, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return nil, newLoadErr("%s not found", agentsDir)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(agentsDir, e.Name()))
		}
	}
	sort.Strings(dirs)
	out := make([]*Agent, 0, len(dirs))
	for _, d := range dirs {
		a, err := LoadAgent(d, teamDir)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// agentSkillsConfig matches the `[skills]` section of an agent's config.toml.
type agentSkillsConfig struct {
	Skills struct {
		Extra   []string `toml:"extra"`
		Disable []string `toml:"disable"`
	} `toml:"skills"`
}

// ResolveSkills returns {skill-name: absolute path} for the agent. Local
// skills under `<agentDir>/skills/<n>/` are auto-included; `[skills].extra`
// in `<agentDir>/config.toml` pulls in shared (`<teamDir>/skills/<spec>`) or
// path-referenced (`./...`, `foo/bar`) skills. `[skills].disable` opts out.
func ResolveSkills(agentDir, teamDir string) (map[string]string, error) {
	skills := map[string]string{}

	localRoot := filepath.Join(agentDir, "skills")
	if entries, err := os.ReadDir(localRoot); err == nil {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			child := filepath.Join(localRoot, name)
			if isFile(filepath.Join(child, "SKILL.md")) {
				resolved, err := resolvePath(child)
				if err != nil {
					return nil, newLoadErr("%s: cannot resolve %s: %v", filepath.Base(agentDir), child, err)
				}
				skills[name] = resolved
			}
		}
	}

	cfgPath := filepath.Join(agentDir, "config.toml")
	var cfg agentSkillsConfig
	if isFile(cfgPath) {
		if _, err := toml.DecodeFile(cfgPath, &cfg); err != nil {
			return nil, newLoadErr("%s: invalid config.toml: %v", filepath.Base(agentDir), err)
		}
	}

	sharedRoot := filepath.Join(teamDir, "skills")
	for _, spec := range cfg.Skills.Extra {
		var raw string
		if strings.Contains(spec, "/") || strings.HasPrefix(spec, ".") {
			raw = filepath.Join(agentDir, spec)
		} else {
			raw = filepath.Join(sharedRoot, spec)
		}
		path, err := resolvePath(raw)
		if err != nil || !isDir(path) || !isFile(filepath.Join(path, "SKILL.md")) {
			return nil, newLoadErr(
				"%s: skill `%s` not found at %s (no SKILL.md)",
				filepath.Base(agentDir), spec, path,
			)
		}
		name := filepath.Base(path)
		if existing, ok := skills[name]; ok && existing != path {
			return nil, newLoadErr(
				"%s: skill name `%s` is already a local skill at %s; can't also import a different `%s`",
				filepath.Base(agentDir), name, existing, spec,
			)
		}
		skills[name] = path
	}

	for _, name := range cfg.Skills.Disable {
		delete(skills, name)
	}

	return skills, nil
}

// UnionSkills combines all agents' skills, erroring on a name collision that
// resolves to two different paths.
func UnionSkills(agents []*Agent) (map[string]string, error) {
	union := map[string]string{}
	for _, a := range agents {
		for name, path := range a.Skills {
			if existing, ok := union[name]; ok && existing != path {
				return nil, newLoadErr(
					"skill name `%s` resolves to two different paths (%s vs %s); rename one.",
					name, existing, path,
				)
			}
			union[name] = path
		}
	}
	return union, nil
}

// resolvePath mirrors Python's `Path.resolve()` — absolute, symlinks evaluated.
// Falls back to `Abs` if the path does not yet exist on disk.
func resolvePath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// Path doesn't exist or cannot be evaluated — fall back to the
		// abs-cleaned form. The caller will hit a downstream stat error
		// and report the missing path.
		return filepath.Clean(abs), nil
	}
	return resolved, nil
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
