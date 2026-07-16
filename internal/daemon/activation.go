package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/agent-team-project/agent-team/internal/runtimeshim"
)

const (
	ActivationStateUnknown  = "unknown"
	ActivationStateCoherent = "coherent"
	ActivationStateNeeded   = "activation_needed"
)

const activationAction = "install matching agent-team and agent-teamd builds, restart the daemon, validate/reload topology, then start persistent instances fresh"
const activationProvenanceMissingReason = "instance launch bundle predates activation provenance"

var activationControlPlanePaths = []string{
	"cmd",
	"internal",
	"embed.go",
	"go.mod",
	"go.sum",
	"template",
	".agent_team/agents",
	".agent_team/skills",
	".agent_team/instances.toml",
}

// ActivationStatus is the daemon's read-only launch-coherence verdict. It
// deliberately reports identities and digests, never a source-tree fallback
// or a way around the managed authority shim.
type ActivationStatus struct {
	State                 string         `json:"state"`
	CLIPath               string         `json:"cli_path,omitempty"`
	CLI                   buildinfo.Info `json:"cli,omitempty"`
	Daemon                buildinfo.Info `json:"daemon,omitempty"`
	WorkspaceRevision     string         `json:"workspace_revision,omitempty"`
	MainRevision          string         `json:"main_revision,omitempty"`
	LoadedAssets          string         `json:"loaded_assets,omitempty"`
	CurrentAssets         string         `json:"current_assets,omitempty"`
	ControlPlaneAhead     int            `json:"control_plane_ahead,omitempty"`
	ControlPlaneBehind    int            `json:"control_plane_behind,omitempty"`
	MainControlPlaneAhead int            `json:"main_control_plane_ahead,omitempty"`
	StaleInstances        []string       `json:"stale_instances,omitempty"`
	Reasons               []string       `json:"reasons,omitempty"`
	Action                string         `json:"action,omitempty"`
}

func (s ActivationStatus) Coherent() bool {
	return s.State == ActivationStateCoherent
}

func (s ActivationStatus) Summary() string {
	parts := []string{"state=" + firstNonEmpty(s.State, ActivationStateUnknown)}
	if !s.CLI.Empty() {
		parts = append(parts, "cli="+s.CLI.Display())
	}
	if !s.Daemon.Empty() {
		parts = append(parts, "daemon="+s.Daemon.Display())
	}
	if revision := shortActivationRevision(s.WorkspaceRevision); revision != "" {
		parts = append(parts, "workspace="+revision)
	}
	if revision := shortActivationRevision(s.MainRevision); revision != "" {
		parts = append(parts, "origin/main="+revision)
	}
	if digest := shortActivationRevision(s.LoadedAssets); digest != "" {
		parts = append(parts, "loaded-assets="+digest)
	}
	if digest := shortActivationRevision(s.CurrentAssets); digest != "" {
		parts = append(parts, "current-assets="+digest)
	}
	if s.ControlPlaneAhead != 0 || s.ControlPlaneBehind != 0 || s.MainControlPlaneAhead != 0 {
		parts = append(parts, fmt.Sprintf("build-drift=workspace+%d/-%d,main+%d", s.ControlPlaneAhead, s.ControlPlaneBehind, s.MainControlPlaneAhead))
	}
	if len(s.StaleInstances) > 0 {
		parts = append(parts, "stale-instances="+strings.Join(s.StaleInstances, ","))
	}
	return strings.Join(parts, " ")
}

func (s ActivationStatus) Diagnostic() string {
	reason := strings.Join(s.Reasons, "; ")
	if reason == "" {
		reason = "launch tuple is not coherent"
	}
	action := strings.TrimSpace(s.Action)
	if action == "" {
		action = activationAction
	}
	return fmt.Sprintf("activation needed: %s; action: %s", reason, action)
}

type ActivationNeededError struct {
	Status ActivationStatus
}

func (e *ActivationNeededError) Error() string {
	if e == nil {
		return "activation needed"
	}
	return e.Status.Diagnostic()
}

type activationInspector func(teamDir string, daemonBuild buildinfo.Info, loadedAssets string) ActivationStatus

type activationContext struct {
	Build              buildinfo.Info
	LoadedAssets       string
	TopologyError      string
	Inspect            activationInspector
	PersistentInstance func(string) bool
}

func (r *EventResolver) activationStatus() ActivationStatus {
	if r == nil {
		return ActivationStatus{State: ActivationStateUnknown}
	}
	r.mu.Lock()
	ctx := r.activation
	topo := r.topo
	r.mu.Unlock()
	status := ctx.status(r.teamDir)
	if !ctx.enabled() || topo == nil || r.mgr == nil {
		return status
	}
	for _, meta := range r.mgr.List() {
		inst := topo.Find(meta.Instance)
		if inst == nil || inst.Ephemeral {
			continue
		}
		stale, reason, err := r.mgr.launchSnapshotActivationStale(meta.Instance, status)
		if err != nil {
			reason = fmt.Sprintf("instance %s activation provenance cannot be read: %v", meta.Instance, err)
			stale = true
		}
		if !stale {
			continue
		}
		status.StaleInstances = append(status.StaleInstances, meta.Instance)
		status.Reasons = append(status.Reasons, fmt.Sprintf("persistent instance %s is stale: %s", meta.Instance, reason))
	}
	if len(status.StaleInstances) > 0 {
		sort.Strings(status.StaleInstances)
		status.State = ActivationStateNeeded
		status.Action = activationAction
	}
	return status
}

func (r *EventResolver) requireActivation() error {
	status := r.activationStatus()
	if status.State == ActivationStateNeeded {
		return &ActivationNeededError{Status: status}
	}
	return nil
}

func (m *InstanceManager) setActivationContext(ctx activationContext) {
	if m == nil {
		return
	}
	m.activationMu.Lock()
	m.activation = ctx
	m.activationMu.Unlock()
}

func (m *InstanceManager) activationContext() activationContext {
	if m == nil {
		return activationContext{}
	}
	m.activationMu.RLock()
	defer m.activationMu.RUnlock()
	return m.activation
}

func (m *InstanceManager) activationStatus() ActivationStatus {
	ctx := m.activationContext()
	return ctx.status(filepath.Dir(m.daemonRoot))
}

func (m *InstanceManager) requireActivation() error {
	status := m.activationStatus()
	if status.State == ActivationStateNeeded {
		return &ActivationNeededError{Status: status}
	}
	return nil
}

func (m *InstanceManager) launchSnapshotActivationStale(instance string, status ActivationStatus) (bool, string, error) {
	ctx := m.activationContext()
	if !ctx.enabled() {
		return false, "", nil
	}
	snapshot, err := ReadInstanceLaunchEnv(m.daemonRoot, instance)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return true, activationProvenanceMissingReason, nil
		}
		return false, "", err
	}
	requireDurableSurface := true
	if ctx.PersistentInstance != nil {
		requireDurableSurface = ctx.PersistentInstance(instance)
	}
	return activationSnapshotStaleForSurface(snapshot, status, requireDurableSurface)
}

func activationSnapshotStale(snapshot *LaunchEnv, status ActivationStatus) (bool, string, error) {
	return activationSnapshotStaleForSurface(snapshot, status, true)
}

func activationSnapshotStaleForSurface(snapshot *LaunchEnv, status ActivationStatus, requireDurableSurface bool) (bool, string, error) {
	if snapshot == nil {
		return true, "instance launch bundle has no activation provenance", nil
	}
	comparison := buildinfo.Compare(snapshot.Build, status.Daemon)
	if snapshot.Build.Empty() || !comparison.Comparable || !comparison.Equal {
		reason := comparison.Reason
		if reason == "" {
			reason = fmt.Sprintf("instance launch build %s differs from active daemon %s", snapshot.Build.Display(), status.Daemon.Display())
		}
		return true, reason, nil
	}
	if strings.TrimSpace(snapshot.Assets) == "" || snapshot.Assets != status.LoadedAssets {
		return true, "instance prompt, skill, topology, or authority-shim bundle differs from the active asset fingerprint", nil
	}
	if !requireDurableSurface {
		return false, "", nil
	}
	shimPath := strings.TrimSpace(snapshot.ShimPath)
	if shimPath == "" {
		return true, "generated shim path is missing; start the instance fresh to regenerate its managed command surface", nil
	}
	attestation, err := runtimeshim.ReadAttestation(shimPath)
	if err != nil {
		return true, fmt.Sprintf("generated shim %s has no readable immutable build attestation: %v; start the instance fresh", shimPath, err), nil
	}
	skillsPath := strings.TrimSpace(snapshot.SkillsPath)
	if skillsPath == "" {
		return true, "running instance skill path is missing; start the instance fresh to regenerate its skill bundle", nil
	}
	skills, err := runtimeshim.SkillAssetsDigestRoot(skillsPath)
	if err != nil {
		return true, fmt.Sprintf("running instance skill assets at %s cannot be attested: %v; start the instance fresh", skillsPath, err), nil
	}
	if err := attestation.CheckActive(status.Daemon, status.CLI, status.LoadedAssets, skills); err != nil {
		return true, fmt.Sprintf("generated shim %s is stale: %v; start the instance fresh", shimPath, err), nil
	}
	return false, "", nil
}

func newActivationContext(teamDir string, build buildinfo.Info) activationContext {
	if build.Empty() {
		return activationContext{}
	}
	digest, err := activationAssetDigest(teamDir)
	ctx := activationContext{Build: build, LoadedAssets: digest, Inspect: InspectActivation}
	if err != nil {
		ctx.TopologyError = fmt.Sprintf("activation asset fingerprint failed: %v", err)
	}
	return ctx
}

func (c activationContext) enabled() bool {
	return !c.Build.Empty() && c.Inspect != nil
}

func (c activationContext) status(teamDir string) ActivationStatus {
	if !c.enabled() {
		return ActivationStatus{State: ActivationStateUnknown}
	}
	status := c.Inspect(teamDir, c.Build, c.LoadedAssets)
	if status.State == "" {
		status.State = ActivationStateCoherent
	}
	if message := strings.TrimSpace(c.TopologyError); message != "" {
		status.Reasons = append(status.Reasons, message)
		status.State = ActivationStateNeeded
		status.Action = activationAction
	}
	return status
}

// InspectActivation compares the live daemon with the exact CLI selected for
// managed shims, the activation-path source revision (when this is the
// framework's own repository), and the asset fingerprint pinned at topology
// load time.
func InspectActivation(teamDir string, daemonBuild buildinfo.Info, loadedAssets string) ActivationStatus {
	status := ActivationStatus{
		State:        ActivationStateCoherent,
		Daemon:       daemonBuild,
		LoadedAssets: strings.TrimSpace(loadedAssets),
	}
	addReason := func(format string, args ...any) {
		status.Reasons = append(status.Reasons, fmt.Sprintf(format, args...))
	}
	repoRoot := filepath.Dir(filepath.Clean(teamDir))

	cliPath, err := runtimeshim.ResolveRealAgentTeamForBuild("", daemonBuild)
	if err != nil {
		addReason("managed CLI cannot be resolved: %v", err)
	} else {
		status.CLIPath = cliPath
		status.CLI, err = buildinfo.ReadFile(cliPath)
		if err != nil {
			addReason("managed CLI build cannot be read from %s: %v", cliPath, err)
		} else {
			matched, matchErr := activationBuildsMatch(status.CLI, daemonBuild)
			if !matched {
				if matchErr != nil {
					addReason("managed CLI %s does not match daemon %s: %v", status.CLI.Display(), daemonBuild.Display(), matchErr)
				} else {
					addReason("managed CLI %s does not match daemon %s", status.CLI.Display(), daemonBuild.Display())
				}
			}
		}
	}

	status.CurrentAssets, err = activationAssetDigest(teamDir)
	if err != nil {
		addReason("current activation assets cannot be fingerprinted: %v", err)
	} else if status.LoadedAssets == "" {
		addReason("daemon has no loaded activation fingerprint")
	} else if status.CurrentAssets != status.LoadedAssets {
		addReason("topology, config, prompt, or skill assets changed after the daemon loaded them")
	}

	inspectActivationGit(repoRoot, daemonBuild, &status, addReason)
	if len(status.Reasons) > 0 {
		status.State = ActivationStateNeeded
		status.Action = activationAction
	}
	return status
}

func activationBuildsMatch(cliBuild, daemonBuild buildinfo.Info) (bool, error) {
	comparison := buildinfo.Compare(cliBuild, daemonBuild)
	if !comparison.Comparable {
		return false, errors.New(comparison.Reason)
	}
	return comparison.Equal, nil
}

// ReadActivationStatus reconstructs the last live daemon tuple from its
// durable launch snapshot. It is used by instance briefs when no HTTP request
// context is available.
func ReadActivationStatus(teamDir string) (*ActivationStatus, error) {
	launch, err := ReadLaunchEnv(DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	status := InspectActivation(teamDir, launch.Build, launch.Assets)
	return &status, nil
}

func persistLoadedActivationAssets(teamDir, assets string) error {
	launch, err := ReadLaunchEnv(DaemonRoot(teamDir))
	if err != nil {
		return err
	}
	launch.Assets = strings.TrimSpace(assets)
	return WriteLaunchEnv(DaemonRoot(teamDir), launch)
}

func inspectActivationGit(repoRoot string, daemonBuild buildinfo.Info, status *ActivationStatus, addReason func(string, ...any)) {
	if status == nil || strings.TrimSpace(repoRoot) == "" {
		return
	}
	if revision, err := activationGitOutput(repoRoot, "rev-parse", "HEAD"); err == nil {
		status.WorkspaceRevision = revision
	}
	if revision, err := activationGitOutput(repoRoot, "rev-parse", "origin/main"); err == nil {
		status.MainRevision = revision
	}
	daemonRevision := strings.TrimSpace(daemonBuild.Revision)
	if daemonRevision == "" || activationGitRun(repoRoot, "cat-file", "-e", daemonRevision+"^{commit}") != nil {
		return
	}
	if status.WorkspaceRevision != "" {
		status.ControlPlaneAhead = activationGitCount(repoRoot, daemonRevision+".."+status.WorkspaceRevision, activationControlPlanePaths)
		status.ControlPlaneBehind = activationGitCount(repoRoot, status.WorkspaceRevision+".."+daemonRevision, activationControlPlanePaths)
		if status.ControlPlaneAhead > 0 || status.ControlPlaneBehind > 0 {
			addReason("workspace %s and daemon %s differ across activation paths (%d ahead, %d behind)",
				shortActivationRevision(status.WorkspaceRevision), daemonBuild.ShortRevision(), status.ControlPlaneAhead, status.ControlPlaneBehind)
		}
	}
	if status.MainRevision != "" {
		status.MainControlPlaneAhead = activationGitCount(repoRoot, daemonRevision+".."+status.MainRevision, activationControlPlanePaths)
		if status.MainControlPlaneAhead > 0 {
			addReason("origin/main contains %d newer activation-path commit(s) than daemon %s", status.MainControlPlaneAhead, daemonBuild.ShortRevision())
		}
	}
}

func activationGitCount(repoRoot, revisionRange string, paths []string) int {
	args := []string{"rev-list", "--count", revisionRange, "--"}
	args = append(args, paths...)
	out, err := activationGitOutput(repoRoot, args...)
	if err != nil {
		return 0
	}
	count, _ := strconv.Atoi(strings.TrimSpace(out))
	return count
}

func activationGitOutput(repoRoot string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func activationGitRun(repoRoot string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	return cmd.Run()
}

type activationAsset struct {
	Path string
	Body []byte
}

func activationAssetDigest(teamDir string) (string, error) {
	teamDir = filepath.Clean(strings.TrimSpace(teamDir))
	if teamDir == "" || teamDir == "." {
		return "", errors.New("team directory is required")
	}
	roots := []struct {
		logical string
		path    string
	}{
		{logical: "instances.toml", path: filepath.Join(teamDir, "instances.toml")},
		{logical: "config.toml", path: filepath.Join(teamDir, "config.toml")},
		{logical: "agents", path: filepath.Join(teamDir, "agents")},
		{logical: "skills", path: filepath.Join(teamDir, "skills")},
	}
	assets := make([]activationAsset, 0, 64)
	for _, root := range roots {
		resolved, err := filepath.EvalSymlinks(root.path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return "", fmt.Errorf("resolve %s: %w", root.logical, err)
		}
		st, err := os.Stat(resolved)
		if err != nil {
			return "", fmt.Errorf("stat %s: %w", root.logical, err)
		}
		if !st.IsDir() {
			body, err := os.ReadFile(resolved)
			if err != nil {
				return "", fmt.Errorf("read %s: %w", root.logical, err)
			}
			assets = append(assets, activationAsset{Path: root.logical, Body: body})
			continue
		}
		err = filepath.WalkDir(resolved, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(resolved, path)
			if err != nil {
				return err
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			assets = append(assets, activationAsset{Path: filepath.ToSlash(filepath.Join(root.logical, rel)), Body: body})
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("fingerprint %s: %w", root.logical, err)
		}
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Path < assets[j].Path })
	hash := sha256.New()
	for _, asset := range assets {
		fmt.Fprintf(hash, "%d:%s\n%d:", len(asset.Path), asset.Path, len(asset.Body))
		_, _ = hash.Write(asset.Body)
		_, _ = hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func shortActivationRevision(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 12 {
		return value[:12]
	}
	return value
}
