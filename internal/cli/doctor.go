package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	texttemplate "text/template"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/agent-team-project/agent-team/internal/loader"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/pmprovider"
	"github.com/agent-team-project/agent-team/internal/runtimebin"
	"github.com/agent-team-project/agent-team/internal/template"
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	var (
		target         string
		strict         bool
		strictDaemon   bool
		strictRuntime  bool
		strictTemplate bool
		jsonOut        bool
		commands       bool
		format         string
		runtimeKind    string
		runtimeBinary  string
		canary         bool
		canaryTimeout  time.Duration
		fix            bool
	)
	cwd, _ := os.Getwd()

	cmd := &cobra.Command{
		Use:   "doctor [agent]",
		Short: "Sanity-check the vendored team.",
		Long: "Sanity-check the vendored team: .agent_team/ layout, config.toml validity, " +
			"template provenance, each agent's frontmatter, skill resolution across all agents, " +
			"durable job files, pipeline workflow wiring, the selected runtime binary, whether the companion agent-teamd binary is available for daemon-backed lifecycle commands, and the daemon's running/readiness state when the repo is otherwise valid.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && !canary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team doctor: agent argument requires --canary.")
				return exitErr(2)
			}
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team doctor: accepts at most one canary agent.")
				return exitErr(2)
			}
			if canaryTimeout <= 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team doctor: --canary-timeout must be > 0.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team doctor: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team doctor: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team doctor: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			tmpl, err := parseDoctorFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team doctor: %v\n", err)
				return exitErr(2)
			}
			if strict {
				strictDaemon = true
				strictRuntime = true
				strictTemplate = true
			}
			strictActionFlag := scopedDoctorStrictActionFlag(strict, strictRuntime)
			canaryAgent := ""
			if len(args) == 1 {
				canaryAgent = args[0]
			}
			return runDoctor(cmd, target, strictDaemon, strictRuntime, strictTemplate, strictActionFlag, fix, jsonOut, commands, tmpl, runtimeSelection{
				Kind:   runtimeKind,
				Binary: runtimeBinary,
			}, doctorCanaryOptions{
				Enabled: canary,
				Agent:   canaryAgent,
				Timeout: canaryTimeout,
			})
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&strict, "strict", false, "Fail on daemon binary, selected/runtime-default binary, and template provenance warnings.")
	cmd.Flags().BoolVar(&strictDaemon, "strict-daemon", false, "Fail when the companion agent-teamd binary is not discoverable.")
	cmd.Flags().BoolVar(&strictRuntime, "strict-runtime", false, "Fail when the selected LLM runtime binary or pipeline/team step and agent runtime defaults are not discoverable.")
	cmd.Flags().BoolVar(&strictTemplate, "strict-template", false, "Fail when .template.lock no longer matches its resolved template ref.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print recommended follow-up commands, one per line.")
	cmd.Flags().StringVar(&format, "format", "", "Render the doctor result with a Go template, e.g. '{{.OK}} {{len .Problems}}'.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile to validate for this invocation (claude, codex, or docker). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBinary, "runtime-bin", "", "Runtime binary to validate for this invocation. Overrides env and repo config.")
	cmd.Flags().BoolVar(&canary, "canary", false, "Dispatch a throwaway daemon-backed runtime canary and verify it exits cleanly.")
	cmd.Flags().DurationVar(&canaryTimeout, "canary-timeout", defaultDoctorCanaryTimeout, "Maximum time to wait for the daemon-backed canary to exit.")
	cmd.Flags().BoolVar(&fix, "fix", false, "Apply safe, local repairs such as backfilling [project].id.")
	return cmd
}

func runDoctor(cmd *cobra.Command, target string, strictDaemon, strictRuntime, strictTemplate bool, strictActionFlag string, fix bool, jsonOut, commands bool, tmpl *texttemplate.Template, selection runtimeSelection, canaryOpts doctorCanaryOptions) error {
	var problems []string
	var warnings []string
	var actions []string
	resolved, err := resolvePrimaryRepo(cmd, target)
	if err != nil {
		candidate, _ := selectedRepoTarget(cmd, target)
		initTarget := strings.TrimSpace(candidate)
		if initTarget == "" {
			initTarget = "."
		}
		if filepath.Base(initTarget) == loader.TeamDirName {
			initTarget = filepath.Dir(initTarget)
		}
		if abs, absErr := cleanAbsPath(initTarget); absErr == nil {
			initTarget = abs
		}
		problems = append(problems, err.Error())
		actions = appendDoctorActions(actions, strings.Join(shellQuoteArgs([]string{"agent-team", "init", "--target", initTarget}), " "))
		return reportDoctor(cmd, problems, warnings, actions, nil, jsonOut, commands, tmpl, operatorCommandScopeFromCommand(cmd, target, "target"))
	}
	abs := resolved.RepoRoot
	teamDir := resolved.TeamDir

	daemonHint := "agent-teamd binary not found — install it alongside agent-team (`go install ./cmd/agent-teamd` if building from source) so `start`, `run --detach`, and other daemon-backed lifecycle commands work."
	if _, err := findAgentTeamd(); err != nil {
		if strictDaemon {
			problems = append(problems, daemonHint)
		} else {
			warnings = append(warnings, daemonHint)
		}
	}
	if info, err := collectRuntimeInfoForConfigWithSelection(filepath.Join(teamDir, "config.toml"), selection); err != nil {
		problems = append(problems, err.Error())
	} else if !info.Available {
		runtimeHint := fmt.Sprintf("runtime binary %q for %s not found — pass --runtime-bin, set [runtime].binary in config.toml, set %s, or install the selected runtime.", info.Binary, info.Runtime, runtimebin.EnvBinary)
		if strictRuntime {
			problems = append(problems, runtimeHint)
		} else {
			warnings = append(warnings, runtimeHint)
		}
	}

	cfgPath := filepath.Join(teamDir, "config.toml")
	if st, err := os.Stat(cfgPath); err != nil || st.IsDir() {
		problems = append(problems, fmt.Sprintf("%s missing — run `agent-team init` to render a repo config.", cfgPath))
	} else {
		var cfg map[string]any
		if _, err := toml.DecodeFile(cfgPath, &cfg); err != nil {
			problems = append(problems, fmt.Sprintf("%s is not valid TOML: %v", cfgPath, err))
		} else {
			pm, _ := cfg["pm"].(map[string]any)
			team, _ := cfg["team"].(map[string]any)
			pmProvider, _ := pm["provider"].(string)
			pmTool, _ := team["pm_tool"].(string)
			project, _ := cfg["project"].(map[string]any)
			projectID, _ := project["id"].(string)
			if strings.TrimSpace(projectID) == "" {
				if fix {
					id, changed, err := origin.EnsureProjectID(teamDir)
					if err != nil {
						problems = append(problems, fmt.Sprintf("[project].id could not be backfilled in %s: %v", cfgPath, err))
					} else if changed {
						actions = appendDoctorActions(actions, fmt.Sprintf("backfilled [project].id = %s", id))
					}
				} else {
					warnings = append(warnings, fmt.Sprintf("[project].id missing/empty in %s — run `agent-team doctor --fix` to backfill it.", cfgPath))
					actions = appendDoctorActions(actions, strings.Join(shellQuoteArgs([]string{"agent-team", "doctor", "--target", abs, "--fix"}), " "))
				}
			}
			provider, providerSource := pmprovider.ConfiguredProviderNameWithSource(pmProvider, pmTool)
			if !pmprovider.KnownProvider(provider) {
				if providerSource == "" {
					providerSource = "pm.provider"
				}
				problems = append(problems, fmt.Sprintf("[%s] has unsupported value %q in %s", providerSource, provider, cfgPath))
			}
			if provider == pmprovider.ProviderLinear {
				linear, _ := cfg["linear"].(map[string]any)
				var missing []string
				for _, k := range []string{"team_id", "ticket_prefix"} {
					v, _ := linear[k].(string)
					if v == "" {
						problems = append(problems, providerRequiredConfigProblem("linear", k, provider, providerSource, cfgPath))
						missing = append(missing, k)
					}
				}
				actions = appendDoctorActions(actions, providerRequiredConfigAction("linear", missing, cfgPath))
			}
			if provider == pmprovider.ProviderGitHub {
				github, _ := cfg["github"].(map[string]any)
				var missing []string
				for _, k := range []string{"owner", "repo"} {
					v, _ := github[k].(string)
					if v == "" {
						problems = append(problems, providerRequiredConfigProblem("github", k, provider, providerSource, cfgPath))
						missing = append(missing, k)
					}
				}
				actions = appendDoctorActions(actions, providerRequiredConfigAction("github", missing, cfgPath))
			}
		}
	}

	lockPath := filepath.Join(teamDir, template.LockFileName)
	if st, err := os.Stat(lockPath); err != nil {
		if os.IsNotExist(err) {
			if !doctorTeamUsesLocalTemplateSource(abs, teamDir) {
				warnings = append(warnings, fmt.Sprintf("%s missing — re-run `agent-team init` with the original template ref and parameters to record provenance for future upgrades.", lockPath))
			}
		} else {
			problems = append(problems, fmt.Sprintf("%s cannot be read: %v", lockPath, err))
		}
	} else if st.IsDir() {
		problems = append(problems, fmt.Sprintf("%s is a directory, expected a lock file", lockPath))
	} else {
		lock, err := template.LoadLock(lockPath)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s is not valid template provenance: %v", lockPath, err))
		} else if strictTemplate {
			if err := checkDoctorTemplateLock(lock); err != nil {
				problems = append(problems, err.Error())
			}
		}
	}

	agentsDir := filepath.Join(teamDir, "agents")
	if st, err := os.Stat(agentsDir); err != nil || !st.IsDir() {
		problems = append(problems, fmt.Sprintf("%s missing — re-run `agent-team init`.", agentsDir))
	} else {
		entries, _ := os.ReadDir(agentsDir)
		var dirs []string
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, filepath.Join(agentsDir, e.Name()))
			}
		}
		if len(dirs) == 0 {
			problems = append(problems, fmt.Sprintf("no agents under %s — `agent-team agent create <name>` to scaffold one.", agentsDir))
		} else {
			sort.Strings(dirs)
			loaded := make([]*loader.Agent, 0, len(dirs))
			for _, d := range dirs {
				a, err := loader.LoadAgent(d, teamDir)
				if err != nil {
					problems = append(problems, err.Error())
					continue
				}
				loaded = append(loaded, a)
			}
			if len(loaded) > 0 {
				if _, err := loader.UnionSkills(loaded); err != nil {
					problems = append(problems, err.Error())
				}
				var agentRuntimeWarnings []agentDoctorFinding
				for _, agent := range loaded {
					agentRuntimeWarnings = append(agentRuntimeWarnings, agentRuntimeFindings(teamDir, agent)...)
				}
				agentRuntimeProblems := []agentDoctorFinding(nil)
				if strictRuntime {
					agentRuntimeProblems, agentRuntimeWarnings = promoteAgentRuntimeFindings(agentRuntimeProblems, agentRuntimeWarnings)
				}
				for _, problem := range agentRuntimeProblems {
					problems = append(problems, "agents: "+problem.Message)
				}
				for _, warning := range agentRuntimeWarnings {
					warnings = append(warnings, "agents: "+warning.Message)
				}
				if len(agentRuntimeProblems) > 0 || len(agentRuntimeWarnings) > 0 {
					actions = appendDoctorActions(actions, agentDoctorDetailActionWithFlag("", strictActionFlag))
				}
			}
		}
	}

	if pipelineDoctor, err := collectPipelineDoctor(teamDir, ""); err != nil {
		problems = append(problems, fmt.Sprintf("pipeline workflow validation failed: %v", err))
	} else if pipelineDoctor != nil {
		if strictRuntime {
			promotePipelineDoctorRuntimeWarnings(pipelineDoctor)
		}
		hasPipelineDoctorFindings := false
		for _, problem := range pipelineDoctor.Problems {
			problems = append(problems, "pipeline workflow: "+problem.Message)
			hasPipelineDoctorFindings = true
		}
		for _, warning := range pipelineDoctor.Warnings {
			if warning.Code == "no_pipelines" {
				continue
			}
			warnings = append(warnings, "pipeline workflow: "+warning.Message)
			hasPipelineDoctorFindings = true
		}
		if hasPipelineDoctorFindings {
			actions = appendDoctorActions(actions, doctorPipelineDetailActionWithFlag(strictActionFlag))
		}
	}
	if teamDoctor, err := collectAllTeamDoctor(teamDir); err != nil {
		problems = append(problems, fmt.Sprintf("team topology validation failed: %v", err))
	} else if teamDoctor != nil {
		if strictRuntime {
			promoteAllTeamDoctorRuntimeWarnings(teamDoctor)
		}
		hasTeamDoctorFindings := false
		for _, problem := range teamDoctor.Problems {
			if isPipelineWorkflowFindingCode(problem.Code) {
				continue
			}
			problems = append(problems, "team topology: "+problem.Message)
			hasTeamDoctorFindings = true
		}
		for _, warning := range teamDoctor.Warnings {
			if warning.Code == "no_teams" || isPipelineWorkflowFindingCode(warning.Code) {
				continue
			}
			warnings = append(warnings, "team topology: "+warning.Message)
			hasTeamDoctorFindings = true
		}
		if hasTeamDoctorFindings {
			actions = appendDoctorActions(actions, doctorTeamDetailActionWithFlag(strictActionFlag))
		}
	}
	if jobDoctor, err := collectJobDoctor(teamDir); err != nil {
		problems = append(problems, fmt.Sprintf("job validation failed: %v", err))
	} else {
		for _, problem := range jobDoctor.Problems {
			problems = append(problems, "jobs: "+problem.Message)
		}
		for _, warning := range jobDoctor.Warnings {
			warnings = append(warnings, "jobs: "+warning.Message)
		}
		actions = appendDoctorActions(actions, jobDoctor.Actions...)
	}
	if intakeDoctor, err := collectIntakeDoctor(teamDir); err != nil {
		problems = append(problems, fmt.Sprintf("intake ledger validation failed: %v", err))
	} else {
		for _, problem := range intakeDoctor.Problems {
			problems = append(problems, "intake ledger: "+intakeDoctorFindingMessage(problem))
		}
		for _, warning := range intakeDoctor.Warnings {
			warnings = append(warnings, "intake ledger: "+intakeDoctorFindingMessage(warning))
		}
		actions = appendDoctorActions(actions, intakeDoctorActions(intakeDoctor)...)
	}
	if queueDoctor, err := collectQueueDoctor(teamDir); err != nil {
		problems = append(problems, fmt.Sprintf("queue validation failed: %v", err))
	} else {
		for _, problem := range queueDoctor.Problems {
			problems = append(problems, "queue: "+problem.Message)
		}
		for _, warning := range queueDoctor.Warnings {
			warnings = append(warnings, "queue: "+warning.Message)
		}
		actions = appendDoctorActions(actions, queueDoctor.Actions...)
	}
	if outboxDoctor, err := collectOutboxDoctor(teamDir); err != nil {
		problems = append(problems, fmt.Sprintf("outbox validation failed: %v", err))
	} else {
		for _, problem := range outboxDoctor.Problems {
			problems = append(problems, "outbox: "+problem.Message)
		}
		for _, warning := range outboxDoctor.Warnings {
			warnings = append(warnings, "outbox: "+warning.Message)
		}
		actions = appendDoctorActions(actions, outboxDoctor.Actions...)
	}
	if quarantine, err := listJobQuarantine(teamDir); err != nil {
		problems = append(problems, fmt.Sprintf("job quarantine validation failed: %v", err))
	} else if len(quarantine) > 0 {
		warnings = append(warnings, fmt.Sprintf("job quarantine: %d file(s) preserved under .agent_team/jobs/quarantine — inspect with `agent-team job quarantine`.", len(quarantine)))
		actions = appendDoctorActions(actions, "agent-team job quarantine")
	}
	if quarantine, err := listQueueQuarantine(teamDir); err != nil {
		problems = append(problems, fmt.Sprintf("queue quarantine validation failed: %v", err))
	} else if len(quarantine) > 0 {
		warnings = append(warnings, fmt.Sprintf("queue quarantine: %d file(s) preserved under .agent_team/daemon/queue/quarantine — inspect with `agent-team queue quarantine ls`.", len(quarantine)))
		actions = appendDoctorActions(actions, "agent-team queue quarantine ls")
	}
	if quarantine, err := listOutboxQuarantine(teamDir); err != nil {
		problems = append(problems, fmt.Sprintf("outbox quarantine validation failed: %v", err))
	} else if len(quarantine) > 0 {
		warnings = append(warnings, fmt.Sprintf("outbox quarantine: %d file(s) preserved under .agent_team/outbox/quarantine — inspect with `agent-team outbox quarantine ls`.", len(quarantine)))
		actions = appendDoctorActions(actions, "agent-team outbox quarantine ls")
	}
	if len(problems) == 0 {
		daemonStatus := collectDaemonStatus(teamDir)
		warnings = append(warnings, doctorDaemonStatusWarnings(daemonStatus)...)
		actions = appendDoctorActions(actions, daemonStatusRemediationActions(daemonStatus)...)
		healthWarnings, healthActions := doctorHealthIssueFindings(teamDir)
		warnings = append(warnings, healthWarnings...)
		actions = appendDoctorActions(actions, healthActions...)
	}
	var canary *doctorCanaryResult
	if canaryOpts.Enabled && len(problems) == 0 {
		canaryOpts.Runtime = selection
		result, err := collectDoctorCanary(cmd, abs, teamDir, canaryOpts)
		if err != nil {
			problems = append(problems, "canary: "+err.Error())
		} else {
			canary = result
			actions = appendDoctorActions(actions, result.Actions...)
			if !result.OK {
				problems = append(problems, "canary: "+doctorCanaryProblemSummary(result))
			}
		}
	}

	return reportDoctor(cmd, problems, warnings, actions, canary, jsonOut, commands, tmpl, operatorCommandScopeFromCommand(cmd, target, "target"))
}

func doctorDaemonStatusWarnings(status daemonStatusJSON) []string {
	if !status.Running {
		return []string{"daemon unreachable: not running — run `agent-team daemon start` before dispatching jobs, ticking pipelines, or managing instances."}
	}
	if !status.Reachable {
		msg := "daemon process is running but its socket/API ping failed"
		if status.Error != "" {
			msg += ": " + status.Error
		}
		return []string{msg + " — run `agent-team daemon restart` or inspect `agent-team daemon logs --tail 80`."}
	}
	if !status.Ready {
		return []string{"daemon running but not ready — run `agent-team daemon restart` or inspect `agent-team daemon logs --tail 80`."}
	}
	return daemonBuildMismatchWarnings(status)
}

func doctorHealthIssueFindings(teamDir string) ([]string, []string) {
	result, err := collectHealthWithOptions(teamDir, time.Now(), healthOptions{})
	if err != nil {
		return []string{fmt.Sprintf("health check unavailable — run `agent-team health` for details: %v", err)}, nil
	}
	var warnings []string
	var actions []string
	for _, issue := range result.Issues {
		if doctorHealthIssueHandledElsewhere(issue.Code) {
			continue
		}
		if strings.TrimSpace(issue.Message) != "" {
			warnings = append(warnings, "health: "+issue.Message)
		}
		actions = appendDoctorActions(actions, issue.Actions...)
	}
	return warnings, actions
}

func doctorHealthIssueHandledElsewhere(code string) bool {
	switch code {
	case "daemon_not_running",
		"daemon_not_ready",
		"job_quarantined",
		"queue_quarantined",
		"outbox_quarantined":
		return true
	default:
		return false
	}
}

func providerRequiredConfigProblem(section, key string, provider pmprovider.ProviderName, providerSource, cfgPath string) string {
	if providerSource == "" {
		providerSource = "pm.provider"
	}
	return fmt.Sprintf("[%s].%s is required when %s = %q in %s", section, key, dottedConfigLabel(providerSource), provider, cfgPath)
}

func providerRequiredConfigAction(section string, keys []string, cfgPath string) string {
	if len(keys) == 0 {
		return ""
	}
	labels := make([]string, 0, len(keys))
	for _, key := range keys {
		labels = append(labels, fmt.Sprintf("[%s].%s", section, key))
	}
	return "echo " + shellQuote(fmt.Sprintf("Set %s in %s.", joinConfigLabels(labels), cfgPath))
}

func joinConfigLabels(labels []string) string {
	switch len(labels) {
	case 0:
		return ""
	case 1:
		return labels[0]
	case 2:
		return labels[0] + " and " + labels[1]
	default:
		return strings.Join(labels[:len(labels)-1], ", ") + ", and " + labels[len(labels)-1]
	}
}

func dottedConfigLabel(key string) string {
	parts := strings.Split(strings.TrimSpace(key), ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return key
	}
	return fmt.Sprintf("[%s].%s", parts[0], parts[1])
}

func appendDoctorActions(actions []string, next ...string) []string {
	seen := make(map[string]struct{}, len(actions)+len(next))
	for _, action := range actions {
		seen[strings.TrimSpace(action)] = struct{}{}
	}
	for _, action := range next {
		action = strings.TrimSpace(action)
		if action == "" {
			continue
		}
		if _, ok := seen[action]; ok {
			continue
		}
		seen[action] = struct{}{}
		actions = append(actions, action)
	}
	return actions
}

func doctorPipelineDetailAction(strictRuntime bool) string {
	return doctorPipelineDetailActionWithFlag(strictRuntimeActionFlag(strictRuntime))
}

func doctorPipelineDetailActionWithFlag(strictActionFlag string) string {
	args := []string{"agent-team", "pipeline", "doctor", "--all"}
	if strictActionFlag != "" {
		args = append(args, strictActionFlag)
	}
	args = append(args, "--json")
	return strings.Join(shellQuoteArgs(args), " ")
}

func doctorTeamDetailAction(strictRuntime bool) string {
	return doctorTeamDetailActionWithFlag(strictRuntimeActionFlag(strictRuntime))
}

func doctorTeamDetailActionWithFlag(strictActionFlag string) string {
	args := []string{"agent-team", "team", "doctor", "--all"}
	if strictActionFlag != "" {
		args = append(args, strictActionFlag)
	}
	args = append(args, "--json")
	return strings.Join(shellQuoteArgs(args), " ")
}

func isPipelineWorkflowFindingCode(code string) bool {
	switch code {
	case "pipeline_nil",
		"pipeline_no_steps",
		"dependency_cycle",
		"target_has_no_dispatch_route",
		"target_matches_multiple_routes",
		"schedule_trigger_has_no_source",
		"first_step_has_dependencies":
		return true
	default:
		return false
	}
}

func checkDoctorTemplateLock(lock *template.Lock) error {
	if lock == nil {
		return fmt.Errorf("template lock is nil")
	}
	rt, _, err := resolveTemplateRefForCLI(lock.Template.Ref)
	if err != nil {
		return fmt.Errorf("template lock ref %q cannot be resolved: %v", lock.Template.Ref, err)
	}
	currentHash, err := template.ContentHash(rt)
	if err != nil {
		return fmt.Errorf("template lock ref %q cannot be hashed: %v", lock.Template.Ref, err)
	}
	if currentHash != lock.Template.ContentHash {
		return fmt.Errorf("template lock drift: ref %q recorded %s but resolves to %s; run `agent-team upgrade --check --strict` to inspect the drift", lock.Template.Ref, lock.Template.ContentHash, currentHash)
	}
	return nil
}

func doctorTeamUsesLocalTemplateSource(repoRoot, teamDir string) bool {
	for _, name := range []string{"agents", "skills"} {
		if doctorSymlinkResolvesInside(filepath.Join(teamDir, name), repoRoot) {
			return true
		}
	}
	return false
}

func doctorSymlinkResolvesInside(path, root string) bool {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

type doctorResult struct {
	OK       bool                `json:"ok"`
	Problems []string            `json:"problems,omitempty"`
	Warnings []string            `json:"warnings,omitempty"`
	Actions  []string            `json:"actions,omitempty"`
	Canary   *doctorCanaryResult `json:"canary,omitempty"`
}

func reportDoctor(cmd *cobra.Command, problems, warnings, actions []string, canary *doctorCanaryResult, jsonOut, commands bool, tmpl *texttemplate.Template, scope operatorCommandScope) error {
	result := doctorResult{
		OK:       len(problems) == 0,
		Problems: problems,
		Warnings: warnings,
		Actions:  actions,
		Canary:   canary,
	}
	if jsonOut {
		if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
			return err
		}
		if !result.OK {
			return exitErr(1)
		}
		return nil
	}
	if commands {
		if err := renderOperatorActionCommands(cmd.OutOrStdout(), result.Actions, scope); err != nil {
			return err
		}
		if !result.OK {
			return exitErr(1)
		}
		return nil
	}
	if tmpl != nil {
		if err := renderDoctorFormat(cmd.OutOrStdout(), result, tmpl); err != nil {
			return err
		}
		if !result.OK {
			return exitErr(1)
		}
		return nil
	}
	if len(problems) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "agent-team doctor: OK")
		if canary != nil {
			renderDoctorCanaryText(cmd.OutOrStdout(), canary)
		}
		for _, w := range warnings {
			fmt.Fprintf(cmd.ErrOrStderr(), "  warning: %s\n", w)
		}
		return nil
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "agent-team doctor: problems found:")
	for _, p := range problems {
		fmt.Fprintf(cmd.ErrOrStderr(), "  - %s\n", p)
	}
	for _, w := range warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "  warning: %s\n", w)
	}
	if canary != nil {
		renderDoctorCanaryText(cmd.ErrOrStderr(), canary)
	}
	return exitErr(1)
}

func parseDoctorFormat(format string) (*texttemplate.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := texttemplate.New("doctor-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderDoctorFormat(w fmtWriter, result doctorResult, tmpl *texttemplate.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}
