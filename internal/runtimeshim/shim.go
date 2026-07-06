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
}

var DefaultSpecs = []Spec{
	{Command: "inbox", Skill: "inbox", Script: filepath.Join("scripts", "inbox.sh")},
	{Command: "channel.sh", Skill: "channel", Script: filepath.Join("scripts", "channel.sh")},
}

var KnownAgentTeamVerbs = []string{
	"adopt",
	"agent",
	"agent.doctor",
	"agent.ls",
	"agent.show",
	"approval",
	"approval.approve",
	"approval.ls",
	"approval.reject",
	"approval.request",
	"approval.show",
	"attach",
	"budget",
	"budget.status",
	"channel",
	"channel.ls",
	"channel.publish",
	"channel.rm",
	"channel.show",
	"channels",
	"completion",
	"completion.bash",
	"completion.fish",
	"completion.powershell",
	"completion.zsh",
	"daemon",
	"daemon.adopt",
	"daemon.env",
	"daemon.logs",
	"daemon.reconcile",
	"daemon.restart",
	"daemon.start",
	"daemon.status",
	"daemon.stop",
	"dispatch",
	"docs",
	"docs.cli",
	"docs.site",
	"doctor",
	"drain",
	"event",
	"event.publish",
	"event.trace",
	"events",
	"extend",
	"feedback",
	"feedback.ls",
	"feedback.resolve",
	"feedback.show",
	"feedback.submit",
	"graph",
	"health",
	"inbox",
	"inbox.ack",
	"inbox.ls",
	"inbox.prune",
	"inbox.show",
	"init",
	"inspect",
	"instance",
	"instance.brief",
	"instance.down",
	"instance.ls",
	"instance.ps",
	"instance.rm",
	"instance.show",
	"instance.up",
	"intake",
	"intake.deliveries",
	"intake.doctor",
	"intake.duplicates",
	"intake.github",
	"intake.linear",
	"intake.prune",
	"intake.replay",
	"intake.schedule",
	"intake.serve",
	"intake.service",
	"intake.summary",
	"job",
	"job.adopt",
	"job.advance",
	"job.approve",
	"job.attach",
	"job.block",
	"job.bounce",
	"job.cancel",
	"job.cleanup",
	"job.close",
	"job.create",
	"job.dispatch",
	"job.doctor",
	"job.events",
	"job.explain",
	"job.extend",
	"job.gate",
	"job.gate.set",
	"job.gates",
	"job.graph",
	"job.hold",
	"job.keep-worktree",
	"job.kill",
	"job.logs",
	"job.ls",
	"job.merge",
	"job.next",
	"job.note",
	"job.outbox",
	"job.outbox.drop",
	"job.outbox.prune",
	"job.outbox.quarantine",
	"job.outbox.quarantine.drop",
	"job.outbox.quarantine.restore",
	"job.outbox.quarantine.show",
	"job.outbox.retry",
	"job.outbox.show",
	"job.prune",
	"job.ps",
	"job.quarantine",
	"job.quarantine.drop",
	"job.quarantine.restore",
	"job.quarantine.show",
	"job.queue",
	"job.queue.drop",
	"job.queue.prune",
	"job.queue.quarantine",
	"job.queue.quarantine.drop",
	"job.queue.quarantine.restore",
	"job.queue.quarantine.show",
	"job.queue.retry",
	"job.queue.show",
	"job.ready",
	"job.reconcile",
	"job.reconcile.events",
	"job.reconcile.github",
	"job.reconcile.queue",
	"job.reconcile.status",
	"job.reject",
	"job.release",
	"job.reopen",
	"job.resume-plan",
	"job.rm",
	"job.runtime",
	"job.runtime.ls",
	"job.send",
	"job.show",
	"job.snapshot",
	"job.start",
	"job.stats",
	"job.step",
	"job.stop",
	"job.timeline",
	"job.timeout",
	"job.triage",
	"job.unblock",
	"job.update",
	"job.wait",
	"kill",
	"locks",
	"logs",
	"monitor",
	"next",
	"outbox",
	"outbox.doctor",
	"outbox.drain",
	"outbox.drop",
	"outbox.ls",
	"outbox.prune",
	"outbox.quarantine",
	"outbox.quarantine.drop",
	"outbox.quarantine.ls",
	"outbox.quarantine.restore",
	"outbox.quarantine.show",
	"outbox.retry",
	"outbox.show",
	"overview",
	"pipeline",
	"pipeline.adopt",
	"pipeline.advance",
	"pipeline.approve",
	"pipeline.cancel",
	"pipeline.cleanup",
	"pipeline.doctor",
	"pipeline.drain",
	"pipeline.events",
	"pipeline.explain",
	"pipeline.graph",
	"pipeline.hold",
	"pipeline.job-events",
	"pipeline.jobs",
	"pipeline.logs",
	"pipeline.ls",
	"pipeline.next",
	"pipeline.outbox",
	"pipeline.outbox.drop",
	"pipeline.outbox.prune",
	"pipeline.outbox.quarantine",
	"pipeline.outbox.quarantine.drop",
	"pipeline.outbox.quarantine.restore",
	"pipeline.outbox.quarantine.show",
	"pipeline.outbox.retry",
	"pipeline.outbox.show",
	"pipeline.ps",
	"pipeline.queue",
	"pipeline.queue.drop",
	"pipeline.queue.prune",
	"pipeline.queue.quarantine",
	"pipeline.queue.quarantine.drop",
	"pipeline.queue.quarantine.restore",
	"pipeline.queue.quarantine.show",
	"pipeline.queue.retry",
	"pipeline.queue.show",
	"pipeline.ready",
	"pipeline.reject",
	"pipeline.release",
	"pipeline.repair",
	"pipeline.resume-plan",
	"pipeline.retry",
	"pipeline.run",
	"pipeline.runtime",
	"pipeline.runtime.ls",
	"pipeline.send",
	"pipeline.show",
	"pipeline.skip",
	"pipeline.snapshot",
	"pipeline.stats",
	"pipeline.status",
	"pipeline.tick",
	"pipeline.timeline",
	"pipeline.timeout",
	"pipeline.triage",
	"pipeline.unblock",
	"pipeline.wait",
	"plan",
	"prune",
	"ps",
	"queue",
	"queue.doctor",
	"queue.drain",
	"queue.drop",
	"queue.ls",
	"queue.prune",
	"queue.quarantine",
	"queue.quarantine.drop",
	"queue.quarantine.ls",
	"queue.quarantine.restore",
	"queue.quarantine.show",
	"queue.retry",
	"queue.show",
	"reload",
	"repair",
	"restart",
	"resume-plan",
	"rm",
	"run",
	"runtime",
	"runtime.adopt",
	"runtime.ls",
	"runtime.metadata",
	"runtime.metadata.ls",
	"runtime.metadata.show",
	"runtime.probe",
	"runtime.profile",
	"runtime.resume-plan",
	"runtime.set",
	"runtime.unset",
	"schedule",
	"schedule.due",
	"schedule.fire",
	"schedule.ls",
	"schedule.next",
	"schedule.run",
	"schedule.show",
	"send",
	"shortcuts",
	"signatures",
	"signatures.test",
	"snapshot",
	"snapshot.diff",
	"start",
	"stats",
	"status",
	"stop",
	"sync",
	"team",
	"team.adopt",
	"team.advance",
	"team.approve",
	"team.cancel",
	"team.cleanup",
	"team.doctor",
	"team.down",
	"team.drain",
	"team.events",
	"team.explain",
	"team.graph",
	"team.health",
	"team.hold",
	"team.job-events",
	"team.jobs",
	"team.logs",
	"team.ls",
	"team.monitor",
	"team.next",
	"team.outbox",
	"team.outbox.drop",
	"team.outbox.prune",
	"team.outbox.quarantine",
	"team.outbox.quarantine.drop",
	"team.outbox.quarantine.restore",
	"team.outbox.quarantine.show",
	"team.outbox.retry",
	"team.outbox.show",
	"team.overview",
	"team.pipelines",
	"team.plan",
	"team.prune",
	"team.ps",
	"team.queue",
	"team.queue.drop",
	"team.queue.prune",
	"team.queue.quarantine",
	"team.queue.quarantine.drop",
	"team.queue.quarantine.restore",
	"team.queue.quarantine.show",
	"team.queue.retry",
	"team.queue.show",
	"team.ready",
	"team.reject",
	"team.release",
	"team.repair",
	"team.restart",
	"team.resume-plan",
	"team.retry",
	"team.run",
	"team.runtime",
	"team.runtime.ls",
	"team.runtime.resume-plan",
	"team.schedules",
	"team.send",
	"team.show",
	"team.skip",
	"team.snapshot",
	"team.stats",
	"team.status",
	"team.sync",
	"team.tick",
	"team.timeline",
	"team.timeout",
	"team.triage",
	"team.unblock",
	"team.up",
	"team.wait",
	"team.wait-jobs",
	"template",
	"template.ls",
	"template.pull",
	"template.rm",
	"template.run",
	"template.show",
	"template.smoke",
	"tick",
	"topology",
	"topology.graph",
	"topology.reload",
	"topology.show",
	"topology.summary",
	"upgrade",
	"usage",
	"wait",
	"watch",
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
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		return explicit, validateExecutableFile(explicit)
	}
	if exe, err := os.Executable(); err == nil && strings.HasPrefix(filepath.Base(exe), "agent-team") {
		return exe, validateExecutableFile(exe)
	}
	if path, err := exec.LookPath("agent-team"); err == nil {
		return path, validateExecutableFile(path)
	}
	if exe, err := os.Executable(); err == nil {
		return exe, validateExecutableFile(exe)
	}
	return "", fmt.Errorf("agent-team binary not found")
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
		"KNOWN_VERBS=" + shellQuote(strings.Join(KnownAgentTeamVerbs, "\n")) + "\n" +
		"\n" +
		"known_verb() {\n" +
		"  printf '%s\\n' \"$KNOWN_VERBS\" | grep -Fx -- \"$1\" >/dev/null 2>&1\n" +
		"}\n" +
		"\n" +
		"verb_has_children() {\n" +
		"  prefix=$1.\n" +
		"  old_ifs=$IFS\n" +
		"  IFS='\n'\n" +
		"  for known in $KNOWN_VERBS; do\n" +
		"    IFS=$old_ifs\n" +
		"    case \"$known\" in \"$prefix\"*) return 0 ;; esac\n" +
		"    IFS='\n'\n" +
		"  done\n" +
		"  IFS=$old_ifs\n" +
		"  return 1\n" +
		"}\n" +
		"\n" +
		"known_leaf_verb() {\n" +
		"  known_verb \"$1\" || return 1\n" +
		"  verb_has_children \"$1\" && return 1\n" +
		"  return 0\n" +
		"}\n" +
		"\n" +
		"candidate_verb() {\n" +
		"  while [ \"$#\" -gt 0 ]; do\n" +
		"    case \"$1\" in\n" +
		"      --) shift; break ;;\n" +
		"      --repo|--target) shift; [ \"$#\" -gt 0 ] && shift ;;\n" +
		"      --repo=*|--target=*) shift ;;\n" +
		"      -*) shift ;;\n" +
		"      *) break ;;\n" +
		"    esac\n" +
		"  done\n" +
		"  [ \"$#\" -eq 0 ] && return 0\n" +
		"  if [ \"$1\" = inbox ]; then printf '%s\\n' inbox; return 0; fi\n" +
		"  path=\n" +
		"  while [ \"$#\" -gt 0 ]; do\n" +
		"    case \"$1\" in -*) break ;; esac\n" +
		"    if [ -n \"$path\" ]; then candidate=$path.$1; else candidate=$1; fi\n" +
		"    if known_leaf_verb \"$candidate\"; then printf '%s\\n' \"$candidate\"; return 0; fi\n" +
		"    if known_verb \"$candidate\"; then path=$candidate; shift; continue; fi\n" +
		"    printf '%s\\n' \"$candidate\"\n" +
		"    return 0\n" +
		"  done\n" +
		"  [ -n \"$path\" ] && printf '%s\\n' \"$path\"\n" +
		"}\n" +
		"\n" +
		"always_allowed() {\n" +
		"  case \"$1\" in\n" +
		"    status|inbox|inbox.*|feedback.submit|budget.status|job.show) return 0 ;;\n" +
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
		"verb=$(candidate_verb \"$@\")\n" +
		"if [ -z \"$verb\" ] || ! known_verb \"$verb\"; then\n" +
		"  echo \"agent-team shim: denied unknown verb ${verb:-<none>}\" >&2\n" +
		"  exit 3\n" +
		"fi\n" +
		"if always_allowed \"$verb\" || authority_allowed \"$verb\"; then\n" +
		"  exec \"$REAL_AGENT_TEAM\" \"$@\"\n" +
		"fi\n" +
		"echo \"agent-team shim: denied verb $verb\" >&2\n" +
		"exit 3\n"
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
