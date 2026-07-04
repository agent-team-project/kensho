package topology

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// LoadFromFile parses a single instances.toml file. Missing file returns
// (nil, nil) — the caller decides whether absence is an error.
func LoadFromFile(path string) (*Topology, error) {
	return loadFromFileWithTeamValidation(path, true)
}

func loadFromFileWithTeamValidation(path string, validateTeamRefs bool) (*Topology, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	t, err := parseWithTeamValidation(body, validateTeamRefs)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// LoadLayered reads template-shipped defaults (if any) and the consumer's
// repo-level `instances.toml` (if any), and returns the merged topology.
// Repo entries override template entries on a whole-instance basis: the
// consumer wholesale-replaces a declared instance rather than per-field
// merging — keeps the merge semantics simple and predictable.
//
// Either argument may be empty string to skip that layer. If both layers are
// missing, returns (nil, nil) — callers treat that as "no topology declared".
func LoadLayered(templatePath, repoPath string) (*Topology, error) {
	templateLayer, err := loadFromFileWithTeamValidation(templatePath, false)
	if err != nil {
		return nil, err
	}
	repoLayer, err := loadFromFileWithTeamValidation(repoPath, false)
	if err != nil {
		return nil, err
	}
	if templateLayer == nil && repoLayer == nil {
		return nil, nil
	}
	merged := &Topology{Instances: map[string]*Instance{}, Locks: map[string]*Lock{}, Channels: map[string]*Channel{}, Pipelines: map[string]*Pipeline{}, Schedules: map[string]*Schedule{}, Teams: map[string]*Team{}}
	if templateLayer != nil {
		for name, inst := range templateLayer.Instances {
			merged.Instances[name] = inst
		}
		for name, lock := range templateLayer.Locks {
			merged.Locks[name] = lock
		}
		for name, channel := range templateLayer.Channels {
			merged.Channels[name] = channel
		}
		for name, pipeline := range templateLayer.Pipelines {
			merged.Pipelines[name] = pipeline
		}
		for name, schedule := range templateLayer.Schedules {
			merged.Schedules[name] = schedule
		}
		for name, team := range templateLayer.Teams {
			merged.Teams[name] = team
		}
		merged.Authority = templateLayer.Authority
	}
	if repoLayer != nil {
		for name, inst := range repoLayer.Instances {
			merged.Instances[name] = inst
		}
		for name, lock := range repoLayer.Locks {
			merged.Locks[name] = lock
		}
		for name, channel := range repoLayer.Channels {
			merged.Channels[name] = channel
		}
		for name, pipeline := range repoLayer.Pipelines {
			merged.Pipelines[name] = pipeline
		}
		for name, schedule := range repoLayer.Schedules {
			merged.Schedules[name] = schedule
		}
		for name, team := range repoLayer.Teams {
			merged.Teams[name] = team
		}
		if repoLayer.Authority != nil {
			merged.Authority = repoLayer.Authority
		}
	}
	if err := validateTopologyTeams(merged); err != nil {
		return nil, err
	}
	if err := validateLockReferences(merged); err != nil {
		return nil, err
	}
	return merged, nil
}

// LoadFromTeamDir is the production entry point: it reads
// `<teamDir>/instances.toml` only. Template defaults are bundled into the
// consumer's `.agent_team/` at `init` time, so the consumer-visible file is
// the single source of truth at runtime. Returns (nil, nil) on absence.
//
// (Daemon callers that need the layered template + repo merge during init
// should call LoadLayered explicitly.)
func LoadFromTeamDir(teamDir string) (*Topology, error) {
	return LoadFromFile(filepath.Join(teamDir, FileName))
}
