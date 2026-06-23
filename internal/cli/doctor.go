package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	texttemplate "text/template"

	"github.com/BurntSushi/toml"
	"github.com/jamesaud/agent-team/internal/loader"
	"github.com/jamesaud/agent-team/internal/runtimebin"
	"github.com/jamesaud/agent-team/internal/template"
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	var (
		target         string
		strictDaemon   bool
		strictRuntime  bool
		strictTemplate bool
		jsonOut        bool
		format         string
		runtimeKind    string
		runtimeBinary  string
	)
	cwd, _ := os.Getwd()

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Sanity-check the vendored team.",
		Long: "Sanity-check the vendored team: .agent_team/ layout, config.toml validity, " +
			"template provenance, each agent's frontmatter, skill resolution across all agents, " +
			"pipeline workflow wiring, the selected runtime binary, and whether the companion agent-teamd binary is available for daemon-backed lifecycle commands.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team doctor: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseDoctorFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team doctor: %v\n", err)
				return exitErr(2)
			}
			return runDoctor(cmd, target, strictDaemon, strictRuntime, strictTemplate, jsonOut, tmpl, runtimeSelection{
				Kind:   runtimeKind,
				Binary: runtimeBinary,
			})
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVar(&strictDaemon, "strict-daemon", false, "Fail when the companion agent-teamd binary is not discoverable.")
	cmd.Flags().BoolVar(&strictRuntime, "strict-runtime", false, "Fail when the selected LLM runtime binary is not discoverable.")
	cmd.Flags().BoolVar(&strictTemplate, "strict-template", false, "Fail when .template.lock no longer matches its resolved template ref.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the doctor result with a Go template, e.g. '{{.OK}} {{len .Problems}}'.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile to validate for this invocation (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBinary, "runtime-bin", "", "Runtime binary to validate for this invocation. Overrides env and repo config.")
	return cmd
}

func runDoctor(cmd *cobra.Command, target string, strictDaemon, strictRuntime, strictTemplate, jsonOut bool, tmpl *texttemplate.Template, selection runtimeSelection) error {
	target = effectiveRepoTarget(cmd, target)
	abs, err := filepath.Abs(target)
	if err != nil {
		return exitErr(2)
	}
	if eval, err := filepath.EvalSymlinks(abs); err == nil {
		abs = eval
	}
	teamDir := filepath.Join(abs, loader.TeamDirName)

	var problems []string
	var warnings []string
	daemonHint := "agent-teamd binary not found — install it alongside agent-team (`go install ./cmd/agent-teamd` if building from source) so `start`, `run --detach`, and other daemon-backed lifecycle commands work."
	if _, err := findAgentTeamd(); err != nil {
		if strictDaemon {
			problems = append(problems, daemonHint)
		} else {
			warnings = append(warnings, daemonHint)
		}
	}
	if st, err := os.Stat(teamDir); err != nil || !st.IsDir() {
		problems = append(problems, fmt.Sprintf("%s not found — run `agent-team init` first.", teamDir))
		return reportDoctor(cmd, problems, warnings, jsonOut, tmpl)
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
		problems = append(problems, fmt.Sprintf("%s missing — copy config.toml.example and fill it in.", cfgPath))
	} else {
		var cfg map[string]any
		if _, err := toml.DecodeFile(cfgPath, &cfg); err != nil {
			problems = append(problems, fmt.Sprintf("%s is not valid TOML: %v", cfgPath, err))
		} else {
			team, _ := cfg["team"].(map[string]any)
			if pmTool, _ := team["pm_tool"].(string); pmTool == "linear" {
				linear, _ := cfg["linear"].(map[string]any)
				for _, k := range []string{"team_id", "ticket_prefix"} {
					v, _ := linear[k].(string)
					if v == "" {
						problems = append(problems, fmt.Sprintf("[linear].%s missing/empty in %s", k, cfgPath))
					}
				}
			}
		}
	}

	lockPath := filepath.Join(teamDir, template.LockFileName)
	if st, err := os.Stat(lockPath); err != nil {
		if os.IsNotExist(err) {
			warnings = append(warnings, fmt.Sprintf("%s missing — re-run `agent-team init` with the original template ref and parameters to record provenance for future upgrades.", lockPath))
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
			}
		}
	}

	if pipelineDoctor, err := collectPipelineDoctor(teamDir, ""); err != nil {
		problems = append(problems, fmt.Sprintf("pipeline workflow validation failed: %v", err))
	} else if pipelineDoctor != nil {
		for _, problem := range pipelineDoctor.Problems {
			problems = append(problems, "pipeline workflow: "+problem.Message)
		}
		for _, warning := range pipelineDoctor.Warnings {
			if warning.Code == "no_pipelines" {
				continue
			}
			warnings = append(warnings, "pipeline workflow: "+warning.Message)
		}
	}
	if teamDoctor, err := collectAllTeamDoctor(teamDir); err != nil {
		problems = append(problems, fmt.Sprintf("team topology validation failed: %v", err))
	} else if teamDoctor != nil {
		for _, problem := range teamDoctor.Problems {
			if isPipelineWorkflowFindingCode(problem.Code) {
				continue
			}
			problems = append(problems, "team topology: "+problem.Message)
		}
		for _, warning := range teamDoctor.Warnings {
			if warning.Code == "no_teams" || isPipelineWorkflowFindingCode(warning.Code) {
				continue
			}
			warnings = append(warnings, "team topology: "+warning.Message)
		}
	}
	if intakeDoctor, err := collectIntakeDoctor(teamDir); err != nil {
		problems = append(problems, fmt.Sprintf("intake ledger validation failed: %v", err))
	} else {
		for _, problem := range intakeDoctor.Problems {
			problems = append(problems, "intake ledger: "+problem.Message)
		}
		for _, warning := range intakeDoctor.Warnings {
			warnings = append(warnings, "intake ledger: "+warning.Message)
		}
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
	}
	if quarantine, err := listQueueQuarantine(teamDir); err != nil {
		problems = append(problems, fmt.Sprintf("queue quarantine validation failed: %v", err))
	} else if len(quarantine) > 0 {
		warnings = append(warnings, fmt.Sprintf("queue quarantine: %d file(s) preserved under .agent_team/daemon/queue/quarantine — inspect with `agent-team queue quarantine ls`.", len(quarantine)))
	}

	return reportDoctor(cmd, problems, warnings, jsonOut, tmpl)
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
	resolver := newResolver()
	rt, err := resolver.Resolve(lock.Template.Ref)
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

type doctorResult struct {
	OK       bool     `json:"ok"`
	Problems []string `json:"problems,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

func reportDoctor(cmd *cobra.Command, problems, warnings []string, jsonOut bool, tmpl *texttemplate.Template) error {
	result := doctorResult{
		OK:       len(problems) == 0,
		Problems: problems,
		Warnings: warnings,
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
