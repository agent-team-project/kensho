package runtimeshim

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
)

const (
	BinDirName           = "bin"
	EnvBuildHeader       = "AGENT_TEAM_BUILD_HEADER"
	EnvDaemonBuildHeader = "AGENT_TEAM_DAEMON_BUILD_HEADER"
	EnvShimPath          = "AGENT_TEAM_SHIM_PATH"
)

// AttestationEnv returns the immutable CLI and daemon build headers baked into
// the generated shim for bundled direct-HTTP skill transports.
func AttestationEnv(binDir string) ([]string, error) {
	shimPath := filepath.Join(binDir, "agent-team")
	attestation, err := ReadAttestation(shimPath)
	if err != nil {
		return nil, err
	}
	out := []string{EnvShimPath + "=" + filepath.Clean(shimPath)}
	if attestation.CLIHeader != "" {
		out = append(out, EnvBuildHeader+"="+attestation.CLIHeader)
	}
	if attestation.DaemonHeader != "" {
		out = append(out, EnvDaemonBuildHeader+"="+attestation.DaemonHeader)
	}
	return out, nil
}

type Spec struct {
	Command string
	Skill   string
	Script  string
}

type Options struct {
	// RealAgentTeam is the binary that the generated agent-team shim execs
	// after its verb check. Empty means resolve the current real CLI binary.
	RealAgentTeam string
	// RealAgentTeamBuild supplies an already-inspected target identity. Empty
	// makes Install read the immutable identity directly from RealAgentTeam.
	RealAgentTeamBuild buildinfo.Info
	// DaemonBuild is the active daemon identity the generated shim must match.
	// Empty is valid for direct/no-daemon launches and is attested as unchecked.
	DaemonBuild buildinfo.Info
	// Assets is the daemon's loaded activation asset fingerprint.
	Assets string
	// EnforceAuthority bakes closed-world verb enforcement into the generated
	// shim. When false the shim is a pass-through (instances that declare no
	// authority). When true the resolved AuthorityAllowlist is embedded as a
	// literal in the script — the decision and the list are NOT read from the
	// environment, so an agent cannot widen its own authority.
	EnforceAuthority bool
	// AuthorityAllowlist is the resolved verb allowlist baked into the shim
	// when EnforceAuthority is true.
	AuthorityAllowlist []string
	// StrictAuthority disables the shim's default always-allowed verbs. Use it
	// for chartered child deployments, where the attenuated grant is the only
	// authority source and default helper verbs must not widen it.
	StrictAuthority bool
}

var DefaultSpecs = []Spec{
	{Command: "inbox", Skill: "inbox", Script: filepath.Join("scripts", "inbox.sh")},
	{Command: "channel.sh", Skill: "channel", Script: filepath.Join("scripts", "channel.sh")},
}

func Install(root string, skillPaths map[string]string, opts Options) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("runtime shim root is required")
	}
	binDir := filepath.Join(root, BinDirName)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", fmt.Errorf("create runtime shim bin: %w", err)
	}
	if err := installAgentTeamShim(binDir, skillPaths, opts); err != nil {
		return "", err
	}
	for _, spec := range DefaultSpecs {
		link := filepath.Join(binDir, spec.Command)
		if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("replace runtime shim %s: %w", spec.Command, err)
		}
		skillDir := strings.TrimSpace(skillPaths[spec.Skill])
		if skillDir == "" {
			continue
		}
		target := filepath.Join(skillDir, spec.Script)
		if st, err := os.Stat(target); err != nil {
			return "", fmt.Errorf("runtime shim %s target: %w", spec.Command, err)
		} else if st.IsDir() {
			return "", fmt.Errorf("runtime shim %s target is a directory: %s", spec.Command, target)
		}
		body := "#!/bin/sh\n" +
			"if [ \"${1:-}\" = \"--build-attestation\" ]; then\n" +
			"  exec " + shellQuote(filepath.Join(binDir, "agent-team")) + " \"$@\"\n" +
			"fi\n" +
			"exec " + shellQuote(target) + " \"$@\"\n"
		if err := os.WriteFile(link, []byte(body), 0o755); err != nil {
			return "", fmt.Errorf("create runtime shim %s: %w", spec.Command, err)
		}
	}
	return binDir, nil
}

func PrependPath(env []string, dir string) []string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return append([]string(nil), env...)
	}
	out := append([]string(nil), env...)
	key := "PATH="
	for i, entry := range out {
		if strings.HasPrefix(entry, key) {
			current := strings.TrimPrefix(entry, key)
			if current == "" {
				out[i] = key + dir
			} else {
				out[i] = key + dir + string(os.PathListSeparator) + current
			}
			return out
		}
	}
	if current := os.Getenv("PATH"); current != "" {
		return append(out, key+dir+string(os.PathListSeparator)+current)
	}
	return append(out, key+dir)
}

func installAgentTeamShim(binDir string, skillPaths map[string]string, opts Options) error {
	link := filepath.Join(binDir, "agent-team")
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("replace runtime shim agent-team: %w", err)
	}
	real, err := ResolveRealAgentTeamForBuild(opts.RealAgentTeam, opts.DaemonBuild)
	if err != nil {
		return fmt.Errorf("runtime shim agent-team target: %w", err)
	}
	cliBuild := buildinfo.Info{}
	if filepath.Clean(real) == filepath.Clean(strings.TrimSpace(opts.RealAgentTeam)) {
		cliBuild = opts.RealAgentTeamBuild
	}
	if cliBuild.Empty() {
		cliBuild, _ = buildinfo.ReadFile(real)
	}
	skills, err := SkillAssetsDigest(skillPaths)
	if err != nil {
		return fmt.Errorf("runtime shim skill assets: %w", err)
	}
	attestation := newAttestation(real, cliBuild, opts.DaemonBuild, opts.Assets, skills)
	body, err := agentTeamShimBody(real, opts, attestation)
	if err != nil {
		return fmt.Errorf("runtime shim attestation: %w", err)
	}
	if err := os.WriteFile(link, []byte(body), 0o755); err != nil {
		return fmt.Errorf("create runtime shim agent-team: %w", err)
	}
	return nil
}

// ResolveRealAgentTeam returns the exact CLI executable a generated managed
// shim must invoke. Unlike the install helper's single-binary test fallback,
// this strict surface never accepts agent-teamd (or an arbitrary host binary)
// as the CLI.
func ResolveRealAgentTeam(explicit string) (string, error) {
	return ResolveRealAgentTeamForBuild(explicit, buildinfo.Info{})
}

// ResolveRealAgentTeamForBuild applies the managed CLI precedence rule:
// explicit native CLI, the current CLI/sibling pair, then PATH candidates.
// Generated per-instance shims are never eligible. When daemonBuild is known,
// a source-comparable candidate wins over an earlier stale native candidate;
// the first native candidate is returned only for activation diagnostics when
// no coherent candidate exists.
func ResolveRealAgentTeamForBuild(explicit string, daemonBuild buildinfo.Info) (string, error) {
	exe, _ := os.Executable()
	candidates := make([]string, 0, 8)
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		candidates = append(candidates, explicit)
	}
	if exe != "" && filepath.Base(exe) == "agent-team" {
		candidates = append(candidates, exe)
	} else if exe != "" {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "agent-team"))
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if strings.TrimSpace(dir) == "" {
			dir = "."
		}
		candidates = append(candidates, filepath.Join(dir, "agent-team"))
	}
	seen := map[string]bool{}
	firstNative := ""
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		if validateExecutableFile(candidate) != nil || isGeneratedShim(candidate) {
			continue
		}
		if firstNative == "" {
			firstNative = candidate
		}
		if daemonBuild.Empty() {
			return candidate, nil
		}
		candidateBuild, err := buildinfo.ReadFile(candidate)
		if err != nil {
			continue
		}
		comparison := buildinfo.Compare(candidateBuild, daemonBuild)
		if comparison.Comparable && comparison.Equal {
			return candidate, nil
		}
	}
	if firstNative != "" {
		return firstNative, nil
	}
	return "", fmt.Errorf("managed agent-team CLI binary not found alongside %s or on PATH", exe)
}

// resolveRealAgentTeamFrom is the testable core. The shim must exec the
// agent-team CLI, never the agent-teamd daemon — critical because the daemon
// itself installs shims, and os.Executable() there is agent-teamd. Matching by
// prefix ("agent-team") wrongly accepts "agent-teamd"; we require an exact base
// name and otherwise prefer the sibling agent-team binary shipped next to the
// current executable.
func resolveRealAgentTeamFrom(explicit, currentExe string, lookPath func(string) (string, error)) (string, error) {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		if isGeneratedShim(explicit) {
			return "", fmt.Errorf("generated runtime shim is not a managed CLI: %s", explicit)
		}
		return explicit, validateExecutableFile(explicit)
	}
	// Current executable is exactly the CLI (the `agent-team run` path).
	if currentExe != "" && filepath.Base(currentExe) == "agent-team" && !isGeneratedShim(currentExe) {
		return currentExe, validateExecutableFile(currentExe)
	}
	// Otherwise (daemon-installed shim: currentExe is agent-teamd) prefer the
	// sibling agent-team binary — they are installed together.
	if currentExe != "" {
		sibling := filepath.Join(filepath.Dir(currentExe), "agent-team")
		if validateExecutableFile(sibling) == nil && !isGeneratedShim(sibling) {
			return sibling, nil
		}
	}
	if lookPath != nil {
		if path, err := lookPath("agent-team"); err == nil && !isGeneratedShim(path) {
			return path, validateExecutableFile(path)
		}
	}
	// Last resort (single-binary / in-process test contexts where no separate
	// agent-team CLI exists): use the current executable. Production never
	// reaches here — the daemon ships alongside agent-team, so the sibling
	// lookup above resolves first; the earlier exact-base + sibling checks are
	// what prevent agent-teamd from being chosen when a real CLI is present.
	if currentExe != "" {
		return currentExe, validateExecutableFile(currentExe)
	}
	return "", fmt.Errorf("agent-team CLI binary not found")
}

func validateExecutableFile(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.IsDir() {
		return fmt.Errorf("target is a directory: %s", path)
	}
	if st.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("target is not executable: %s", path)
	}
	return nil
}

func agentTeamShimBody(real string, opts Options, attestation Attestation) (string, error) {
	marker, attestationJSON, err := encodeAttestation(attestation)
	if err != nil {
		return "", err
	}
	attestationSurface := marker + "\n" +
		"if [ \"${1:-}\" = \"--build-attestation\" ]; then\n" +
		"  case \"${2:---json}\" in\n" +
		"    --json) printf '%s\\n' " + shellQuote(attestationJSON) + " ;;\n" +
		"    --header) printf '%s\\n' " + shellQuote(attestation.CLIHeader) + " ;;\n" +
		"    *) echo \"agent-team shim: usage: --build-attestation [--json|--header]\" >&2; exit 2 ;;\n" +
		"  esac\n" +
		"  exit 0\n" +
		"fi\n"
	// No declared authority => the shim is a pass-through. This decision is
	// baked into the generated script at install time; it is never read from
	// the (caller-controlled) environment, so an agent cannot reach the
	// pass-through branch through caller-controlled launch state.
	if !opts.EnforceAuthority {
		return "#!/bin/sh\n" + attestationSurface +
			"exec " + shellQuote(real) + " \"$@\"\n", nil
	}
	baked := strings.Join(normalizeAllowlist(opts.AuthorityAllowlist), ",")
	return "#!/bin/sh\n" + attestationSurface +
		"REAL_AGENT_TEAM=" + shellQuote(real) + "\n" +
		"# Closed-world enforcement baked in at install time; the allowlist is a\n" +
		"# script literal, NOT read from the environment.\n" +
		"AUTHORITY_ALLOWLIST=" + shellQuote(baked) + "\n" +
		"\n" +
		"# Resolve the invocation to its canonical dotted verb via the REAL\n" +
		"# binary's own Cobra tree (aliases, positionals, subcommands handled\n" +
		"# there). The shim never replicates the command tree, so it cannot\n" +
		"# drift from it. Unknown verbs exit non-zero and are denied.\n" +
		"always_allowed() {\n" +
		"  case \"$1\" in\n" +
		"    status|inbox|inbox.check|inbox.*|feedback.submit|budget.status|job.show) return 0 ;;\n" +
		"  esac\n" +
		"  return 1\n" +
		"}\n" +
		"\n" +
		"authority_allowed() {\n" +
		"  remaining=$AUTHORITY_ALLOWLIST\n" +
		"  while [ -n \"$remaining\" ]; do\n" +
		"    case \"$remaining\" in\n" +
		"      *,*) allow=${remaining%%,*}; remaining=${remaining#*,} ;;\n" +
		"      *) allow=$remaining; remaining= ;;\n" +
		"    esac\n" +
		"    # Scoped grants (for example :own) are enforced by the real CLI's\n" +
		"    # authority audit using trusted origin metadata; the shim only gates\n" +
		"    # whether a known verb may reach that audited command implementation.\n" +
		"    pattern=${allow%%:*}\n" +
		"    case \"$pattern\" in\n" +
		"      '') ;;\n" +
		"      '*') return 0 ;;\n" +
		"      *'.*') prefix=${pattern%\\*}; case \"$1\" in \"$prefix\"*) return 0 ;; esac ;;\n" +
		"      *) [ \"$1\" = \"$pattern\" ] && return 0 ;;\n" +
		"    esac\n" +
		"  done\n" +
		"  return 1\n" +
		"}\n" +
		"\n" +
		"if ! verb=$(\"$REAL_AGENT_TEAM\" __resolve-verb \"$@\" 2>/dev/null) || [ -z \"$verb\" ]; then\n" +
		"  echo \"agent-team shim: denied unknown verb\" >&2\n" +
		"  exit 3\n" +
		"fi\n" +
		"if " + shimAllowCondition(opts.StrictAuthority) + "; then\n" +
		"  exec \"$REAL_AGENT_TEAM\" \"$@\"\n" +
		"fi\n" +
		"echo \"agent-team shim: denied verb $verb\" >&2\n" +
		"exit 3\n", nil
}

func shimAllowCondition(strict bool) string {
	if strict {
		return "authority_allowed \"$verb\""
	}
	return "always_allowed \"$verb\" || authority_allowed \"$verb\""
}

func normalizeAllowlist(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
