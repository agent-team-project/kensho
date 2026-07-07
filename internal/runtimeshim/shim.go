package runtimeshim

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const BinDirName = "bin"

const EnvAuthorityAllowlist = "AGENT_TEAM_AUTHORITY_ALLOWLIST"

type Spec struct {
	Command string
	Skill   string
	Script  string
}

type Options struct {
	// RealAgentTeam is the binary that the generated agent-team shim execs
	// after its verb check. Empty means resolve the current real CLI binary.
	RealAgentTeam string
	// EnforceAuthority bakes closed-world verb enforcement into the generated
	// shim. When false the shim is a pass-through (instances that declare no
	// authority). When true the resolved AuthorityAllowlist is embedded as a
	// literal in the script — the decision and the list are NOT read from the
	// environment, so an agent cannot widen its own authority by unsetting or
	// overriding AGENT_TEAM_AUTHORITY_ALLOWLIST.
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

func Install(root string, skillPaths map[string]string) (string, error) {
	return InstallWithOptions(root, skillPaths, Options{})
}

func InstallWithOptions(root string, skillPaths map[string]string, opts Options) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("runtime shim root is required")
	}
	binDir := filepath.Join(root, BinDirName)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", fmt.Errorf("create runtime shim bin: %w", err)
	}
	if err := installAgentTeamShim(binDir, opts); err != nil {
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
		body := "#!/bin/sh\nexec " + shellQuote(target) + " \"$@\"\n"
		if err := os.WriteFile(link, []byte(body), 0o755); err != nil {
			return "", fmt.Errorf("create runtime shim %s: %w", spec.Command, err)
		}
	}
	return binDir, nil
}

func WithAuthorityAllowlist(env []string, allow []string) []string {
	out := append([]string(nil), env...)
	if envHasKey(out, EnvAuthorityAllowlist) {
		return out
	}
	allow = normalizeAllowlist(allow)
	if len(allow) == 0 {
		return out
	}
	return append(out, EnvAuthorityAllowlist+"="+strings.Join(allow, ","))
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

func installAgentTeamShim(binDir string, opts Options) error {
	link := filepath.Join(binDir, "agent-team")
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("replace runtime shim agent-team: %w", err)
	}
	real, err := resolveRealAgentTeam(opts.RealAgentTeam)
	if err != nil {
		return fmt.Errorf("runtime shim agent-team target: %w", err)
	}
	body := agentTeamShimBody(real, opts)
	if err := os.WriteFile(link, []byte(body), 0o755); err != nil {
		return fmt.Errorf("create runtime shim agent-team: %w", err)
	}
	return nil
}

func resolveRealAgentTeam(explicit string) (string, error) {
	exe, _ := os.Executable()
	return resolveRealAgentTeamFrom(explicit, exe, exec.LookPath)
}

// resolveRealAgentTeamFrom is the testable core. The shim must exec the
// agent-team CLI, never the agent-teamd daemon — critical because the daemon
// itself installs shims, and os.Executable() there is agent-teamd. Matching by
// prefix ("agent-team") wrongly accepts "agent-teamd"; we require an exact base
// name and otherwise prefer the sibling agent-team binary shipped next to the
// current executable.
func resolveRealAgentTeamFrom(explicit, currentExe string, lookPath func(string) (string, error)) (string, error) {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		return explicit, validateExecutableFile(explicit)
	}
	// Current executable is exactly the CLI (the `agent-team run` path).
	if currentExe != "" && filepath.Base(currentExe) == "agent-team" {
		return currentExe, validateExecutableFile(currentExe)
	}
	// Otherwise (daemon-installed shim: currentExe is agent-teamd) prefer the
	// sibling agent-team binary — they are installed together.
	if currentExe != "" {
		sibling := filepath.Join(filepath.Dir(currentExe), "agent-team")
		if validateExecutableFile(sibling) == nil {
			return sibling, nil
		}
	}
	if lookPath != nil {
		if path, err := lookPath("agent-team"); err == nil {
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

func agentTeamShimBody(real string, opts Options) string {
	// No declared authority => the shim is a pass-through. This decision is
	// baked into the generated script at install time; it is never read from
	// the (caller-controlled) environment, so an agent cannot reach the
	// pass-through branch by unsetting AGENT_TEAM_AUTHORITY_ALLOWLIST.
	if !opts.EnforceAuthority {
		return "#!/bin/sh\n" +
			"exec " + shellQuote(real) + " \"$@\"\n"
	}
	baked := strings.Join(normalizeAllowlist(opts.AuthorityAllowlist), ",")
	return "#!/bin/sh\n" +
		"REAL_AGENT_TEAM=" + shellQuote(real) + "\n" +
		"# Closed-world enforcement baked in at install time; the allowlist is a\n" +
		"# script literal, NOT read from the environment (tamper-proof against\n" +
		"# env -u / AGENT_TEAM_AUTHORITY_ALLOWLIST=* self-widening).\n" +
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
		"exit 3\n"
}

func shimAllowCondition(strict bool) string {
	if strict {
		return "authority_allowed \"$verb\""
	}
	return "always_allowed \"$verb\" || authority_allowed \"$verb\""
}

func envHasKey(env []string, key string) bool {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
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
