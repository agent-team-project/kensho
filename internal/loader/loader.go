// Package loader parses agent definitions and resolves skills under a
// `.agent_team/` tree.
package loader

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// TeamDirName is the conventional directory name vendored into a consumer repo.
const TeamDirName = ".agent_team"

func newLoadErr(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

// Agent is one loaded agent definition.
type Agent struct {
	Name        string
	Description string
	Prompt      string
	// Skills maps skill-name → resolved absolute path (symlinks evaluated).
	Skills map[string]string
	// Subscribes is the agent's frontmatter `subscribes:` list — channel
	// names this agent should be auto-subscribed to at spawn time. Empty if
	// not declared. Validation of the channel-name shape happens at
	// subscribe time (the daemon enforces it).
	Subscribes []string
	// Runtime / RuntimeBin are the agent's frontmatter `runtime:` and
	// `runtime_bin:` — the runtime this agent's instances default to when a
	// dispatch does not explicitly override it and no AGENT_TEAM_RUNTIME env
	// override is set. Empty means "inherit the repo/default runtime". This is
	// what lets one team run, e.g., the manager on Claude while workers run on
	// Codex, declared on the agent rather than threaded through every dispatch.
	Runtime    string
	RuntimeBin string
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
	desc := strings.TrimSpace(fm.Scalars["description"])
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
		Subscribes:  fm.Lists["subscribes"],
		Runtime:     strings.TrimSpace(fm.Scalars["runtime"]),
		RuntimeBin:  strings.TrimSpace(fm.Scalars["runtime_bin"]),
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

// skillsConfig matches the `[skills]` section in repo and agent config files.
type skillsConfig struct {
	Skills struct {
		Extra   []string `toml:"extra"`
		Disable []string `toml:"disable"`
		Team    []string `toml:"team"`
	} `toml:"skills"`
}

// ResolveSkills returns {skill-name: absolute path} for the agent. Local
// skills under `<agentDir>/skills/<n>/` are auto-included; `[skills].extra`
// in `<agentDir>/config.toml` pulls in shared (`<teamDir>/skills/<spec>`) or
// path-referenced (`./...`, `foo/bar`) skills. `[skills].disable` opts out of
// local and extra skills. `[skills].team` in `<teamDir>/config.toml` adds shared
// skills for every agent.
func ResolveSkills(agentDir, teamDir string) (map[string]string, error) {
	skills := map[string]string{}
	agentName := filepath.Base(agentDir)

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
					return nil, newLoadErr("%s: cannot resolve %s: %v", agentName, child, err)
				}
				skills[name] = resolved
			}
		}
	}

	cfgPath := filepath.Join(agentDir, "config.toml")
	var cfg skillsConfig
	if isFile(cfgPath) {
		if _, err := toml.DecodeFile(cfgPath, &cfg); err != nil {
			return nil, newLoadErr("%s: invalid config.toml: %v", agentName, err)
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
				agentName, spec, path,
			)
		}
		name := filepath.Base(path)
		if existing, ok := skills[name]; ok && existing != path {
			return nil, newLoadErr(
				"%s: skill name `%s` is already a local skill at %s; can't also import a different `%s`",
				agentName, name, existing, spec,
			)
		}
		skills[name] = path
	}

	for _, name := range cfg.Skills.Disable {
		delete(skills, name)
	}

	teamSkills, err := ResolveTeamSkills(teamDir)
	if err != nil {
		return nil, err
	}
	for name, path := range teamSkills {
		if existing, ok := skills[name]; ok && existing != path {
			return nil, newLoadErr(
				"%s: team skill `%s` resolves to %s but this agent already has `%s` at %s",
				agentName, name, path, name, existing,
			)
		}
		skills[name] = path
	}

	return skills, nil
}

// ResolveTeamSkills returns shared skills configured at repo scope for every
// launched agent via `[skills] team = ["..."]` in `<teamDir>/config.toml`.
func ResolveTeamSkills(teamDir string) (map[string]string, error) {
	cfgPath := filepath.Join(teamDir, "config.toml")
	var cfg skillsConfig
	if isFile(cfgPath) {
		if _, err := toml.DecodeFile(cfgPath, &cfg); err != nil {
			return nil, newLoadErr("%s: invalid config.toml: %v", filepath.Base(teamDir), err)
		}
	}

	skills := map[string]string{}
	sharedRoot := filepath.Join(teamDir, "skills")
	for _, spec := range cfg.Skills.Team {
		name := strings.TrimSpace(spec)
		if name == "" {
			return nil, newLoadErr("%s: team skill name cannot be empty", filepath.Base(teamDir))
		}
		if strings.Contains(name, "/") || strings.Contains(name, string(filepath.Separator)) || strings.HasPrefix(name, ".") {
			return nil, newLoadErr(
				"%s: team skill `%s` must be a shared skill name under skills/, not a path",
				filepath.Base(teamDir), spec,
			)
		}
		path, err := resolvePath(filepath.Join(sharedRoot, name))
		if err != nil || !isDir(path) || !isFile(filepath.Join(path, "SKILL.md")) {
			return nil, newLoadErr(
				"%s: team skill `%s` not found at %s (no SKILL.md)",
				filepath.Base(teamDir), spec, path,
			)
		}
		skills[name] = path
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
