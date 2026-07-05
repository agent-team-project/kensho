package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/loader"
	"github.com/agent-team-project/agent-team/internal/runtimebin"
	"github.com/agent-team-project/agent-team/internal/runtimeotel"
	"github.com/spf13/cobra"
)

const (
	defaultDoctorCanaryAgent   = "worker"
	defaultDoctorCanaryTimeout = 30 * time.Second
	doctorCanaryMarker         = "agent-team doctor canary ok"
)

type doctorCanaryOptions struct {
	Enabled bool
	Agent   string
	Timeout time.Duration
	Runtime runtimeSelection
}

type doctorCanaryResult struct {
	OK            bool                `json:"ok"`
	Agent         string              `json:"agent"`
	Instance      string              `json:"instance,omitempty"`
	Runtime       string              `json:"runtime,omitempty"`
	RuntimeBinary string              `json:"runtime_binary,omitempty"`
	RuntimePath   string              `json:"runtime_path,omitempty"`
	PID           int                 `json:"pid,omitempty"`
	StartedAt     string              `json:"started_at,omitempty"`
	DurationMS    int64               `json:"duration_ms,omitempty"`
	Status        string              `json:"status,omitempty"`
	ExitCode      *int                `json:"exit_code,omitempty"`
	StateDir      string              `json:"state_dir,omitempty"`
	LastMessage   string              `json:"last_message,omitempty"`
	LogExcerpt    string              `json:"log_excerpt,omitempty"`
	RuntimeBanner bool                `json:"runtime_banner,omitempty"`
	CleanupOK     bool                `json:"cleanup_ok"`
	CleanupError  string              `json:"cleanup_error,omitempty"`
	Daemon        *daemonStatusJSON   `json:"daemon,omitempty"`
	Issues        []runtimeProbeIssue `json:"issues,omitempty"`
	Actions       []string            `json:"actions,omitempty"`
}

func collectDoctorCanary(cmd *cobra.Command, repo, teamDir string, opts doctorCanaryOptions) (*doctorCanaryResult, error) {
	agentName := strings.TrimSpace(opts.Agent)
	if agentName == "" {
		agentName = defaultDoctorCanaryAgent
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultDoctorCanaryTimeout
	}
	instance := doctorCanaryInstanceName(agentName)
	stateDir := filepath.Join(teamDir, "state", instance)
	result := &doctorCanaryResult{
		OK:        true,
		Agent:     agentName,
		Instance:  instance,
		StateDir:  filepath.ToSlash(stateDir),
		CleanupOK: true,
	}

	status := collectDaemonStatus(teamDir)
	result.Daemon = &status
	if !status.Running {
		result.addIssue("fail", "daemon", "not_running", "agent-teamd is not running; canary dispatch requires the daemon", "Run `agent-team daemon start`, then rerun `agent-team doctor --canary`.")
		return result.finalize(), nil
	}
	if !status.Ready {
		summary := "agent-teamd is running but not ready"
		if strings.TrimSpace(status.Error) != "" {
			summary += ": " + status.Error
		}
		result.addIssue("fail", "daemon", "not_ready", summary, "Run `agent-team daemon status --wait` or inspect `agent-team daemon logs --tail 80`.")
		return result.finalize(), nil
	}

	dc, err := newDaemonClientWithTimeout(teamDir, timeout)
	if err != nil {
		result.addIssue("fail", "daemon", "connect_failed", err.Error(), "Run `agent-team daemon status --wait` and inspect the daemon log if it is not ready.")
		return result.finalize(), nil
	}

	agents, err := loader.LoadAllAgents(teamDir)
	if err != nil {
		result.addIssue("fail", "agents", "load_failed", err.Error(), "Run `agent-team doctor` without --canary to inspect agent loading errors.")
		return result.finalize(), nil
	}
	chosen := findDoctorCanaryAgent(agents, agentName)
	if chosen == nil {
		result.addIssue("fail", "agents", "agent_missing", fmt.Sprintf("agent %q not found", agentName), "Run `agent-team agent ls` and pass a valid agent name to `agent-team doctor --canary <agent>`.")
		return result.finalize(), nil
	}

	rt, err := doctorCanaryRuntime(teamDir, chosen, opts.Runtime)
	if err != nil {
		result.addIssue("fail", "runtime", "resolution_failed", err.Error(), "Fix the runtime selection or pass --runtime/--runtime-bin explicitly.")
		return result.finalize(), nil
	}
	result.Runtime = string(rt.Kind)
	result.RuntimeBinary = rt.Binary
	if path, err := runtimeLookPath(rt.Binary); err == nil {
		result.RuntimePath = path
	}

	tmpdir, err := os.MkdirTemp("", "agent-team-doctor-canary-")
	if err != nil {
		result.addIssue("fail", "canary", "prepare_failed", "create canary temp dir: "+err.Error(), "Check local temp directory permissions and free space.")
		return result.finalize(), nil
	}
	defer os.RemoveAll(tmpdir)
	defer doctorCanaryCleanup(result, dc, stateDir)

	runtimeArgs, runtimeStdin, lastMessagePath, err := prepareDoctorCanaryDispatch(repo, teamDir, stateDir, tmpdir, instance, agentName, rt, agents)
	if err != nil {
		result.addIssue("fail", "canary", "prepare_failed", err.Error(), "Run `agent-team doctor` without --canary to inspect the static team configuration.")
		return result.finalize(), nil
	}
	result.RuntimeBanner = strings.Contains(runtimeStdin, "--- agent-team runtime ---")
	if rt.Kind == runtimebin.KindCodex && !result.RuntimeBanner {
		result.addIssue("fail", "exec_probe", "runtime_banner_missing", "Codex canary dispatch did not include the agent-team runtime banner", doctorCanaryIssueRemediation(result, "runtime_banner_missing"))
		return result.finalize(), nil
	}

	start := time.Now()
	disp, err := dc.Dispatch(dispatchPayload{
		Agent:         agentName,
		Name:          instance,
		Prompt:        doctorCanaryPrompt(),
		Workspace:     repo,
		Runtime:       string(rt.Kind),
		RuntimeBinary: rt.Binary,
		Args:          runtimeArgs,
		Env:           doctorCanaryTeamEnv(teamDir, instance, stateDir),
		Stdin:         runtimeStdin,
	})
	if err != nil {
		result.addIssue("fail", "dispatch", doctorCanaryDispatchIssueID(err.Error()), "daemon canary dispatch failed: "+err.Error(), doctorCanaryIssueRemediation(result, doctorCanaryDispatchIssueID(err.Error())))
		return result.finalize(), nil
	}
	result.PID = disp.PID
	result.StartedAt = disp.StartedAt.Format(time.RFC3339)
	if disp.Runtime != "" {
		result.Runtime = disp.Runtime
	}

	meta, timedOut, err := waitDoctorCanaryExit(cmd.Context(), dc, instance, timeout)
	result.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		result.addIssue("fail", "daemon", "poll_failed", err.Error(), "Inspect `agent-team daemon logs --tail 80` and rerun the canary.")
		return result.finalize(), nil
	}
	if timedOut {
		result.addIssue("fail", "canary", "timeout", fmt.Sprintf("canary instance %q did not exit within %s", instance, timeout), "Inspect the canary log with `agent-team logs "+instance+" --tail all`, then increase --canary-timeout only after checking runtime/provider health.")
		return result.finalize(), nil
	}
	if meta == nil {
		result.addIssue("fail", "daemon", "metadata_missing", fmt.Sprintf("canary instance %q disappeared before it reached a terminal status", instance), "Inspect daemon lifecycle events with `agent-team events --instance "+instance+" --tail 20`.")
		return result.finalize(), nil
	}
	result.Status = metadataStatusKey(meta)
	result.ExitCode = meta.ExitCode
	if strings.TrimSpace(meta.Runtime) != "" {
		result.Runtime = meta.Runtime
	}
	if strings.TrimSpace(meta.RuntimeBinary) != "" {
		result.RuntimeBinary = meta.RuntimeBinary
	}
	result.LogExcerpt = doctorCanaryLogExcerpt(meta)

	if meta.Status != daemon.StatusExited || (meta.ExitCode != nil && *meta.ExitCode != 0) {
		id := doctorCanaryRuntimeFailureIssueID(result)
		result.addIssue("fail", "runtime", id, doctorCanaryRuntimeFailureSummary(result, id), doctorCanaryIssueRemediation(result, id))
		return result.finalize(), nil
	}
	if rt.Kind == runtimebin.KindCodex {
		result.LastMessage = doctorCanaryReadLastMessage(lastMessagePath)
		if result.LastMessage == "" {
			result.addIssue("fail", "exec_probe", "last_message_missing", "Codex canary exited cleanly but did not write the expected last-message sidecar", runtimeExecProbeRemediation(&runtimeExecProbe{Runtime: string(runtimebin.KindCodex)}, "last_message_missing"))
		} else if strings.TrimSpace(result.LastMessage) != doctorCanaryMarker {
			result.addIssue("fail", "exec_probe", "marker_mismatch", fmt.Sprintf("Codex canary final message did not match %q", doctorCanaryMarker), "Inspect the canary log and rerun `agent-team runtime probe --runtime codex --exec --timeout 2m`.")
		}
	} else if !strings.Contains(result.LogExcerpt, doctorCanaryMarker) {
		result.addIssue("fail", "runtime", "marker_missing", fmt.Sprintf("canary log did not contain marker %q", doctorCanaryMarker), "Inspect the canary log and rerun the canary with a longer --canary-timeout.")
	}
	return result.finalize(), nil
}

func prepareDoctorCanaryDispatch(repo, teamDir, stateDir, tmpdir, instance, agentName string, rt runtimebin.Runtime, agents []*loader.Agent) ([]string, string, string, error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, "", "", fmt.Errorf("create state dir: %w", err)
	}
	resolved, err := resolveRunConfig(teamDir, stateDir, instance, runConfig{target: repo, name: instance})
	if err != nil {
		return nil, "", "", err
	}
	if err := writeStateConfig(stateDir, resolved); err != nil {
		return nil, "", "", fmt.Errorf("write state config: %w", err)
	}
	if err := rerenderTmplFiles(teamDir, stateDir, resolved); err != nil {
		return nil, "", "", fmt.Errorf("re-render .tmpl files: %w", err)
	}

	skillPaths, err := loader.UnionSkills(agents)
	if err != nil {
		return nil, "", "", err
	}
	skillsRoot := filepath.Join(tmpdir, ".claude", "skills")
	if err := os.MkdirAll(skillsRoot, 0o755); err != nil {
		return nil, "", "", fmt.Errorf("create skills root: %w", err)
	}
	for sname, spath := range skillPaths {
		if err := os.Symlink(spath, filepath.Join(skillsRoot, sname)); err != nil {
			return nil, "", "", fmt.Errorf("symlink skill %s: %w", sname, err)
		}
	}
	stateRel, err := filepath.Rel(repo, stateDir)
	if err != nil {
		stateRel = stateDir
	}
	kickoff := fmt.Sprintf(
		"You are the `%s` canary instance for the `%s` agent.\n"+
			"Your state dir is `%s` (absolute: `%s`).\n\n"+
			"--- agent-team doctor canary ---\n\n"+
			"This is a throwaway runtime smoke test. Do not inspect files, run tools, modify state, or start ticket work. Reply exactly with: %s",
		instance, agentName, filepath.ToSlash(stateRel), stateDir, doctorCanaryMarker,
	)
	promptFile := filepath.Join(tmpdir, "system_prompt.md")
	if err := os.WriteFile(promptFile, []byte(kickoff), 0o644); err != nil {
		return nil, "", "", fmt.Errorf("write prompt file: %w", err)
	}
	agentsJSON, err := buildAgentsJSON(agents)
	if err != nil {
		return nil, "", "", err
	}
	lastMessagePath := ""
	if rt.Kind == runtimebin.KindCodex {
		lastMessagePath = filepath.Join(stateDir, runtimebin.CodexLastMessageFile)
		_ = os.Remove(lastMessagePath)
	}
	forwarded := []string(nil)
	if rt.Kind == runtimebin.KindCodex {
		forwarded = append(forwarded, "--skip-git-repo-check")
	}
	args, stdin, err := buildRuntimeArgs(rt, repo, tmpdir, agentsJSON, promptFile, kickoff, doctorCanaryPrompt(), forwarded, agents, doctorCanaryTeamEnv(teamDir, instance, stateDir), lastMessagePath, nil, runtimeotel.Launch{})
	if err != nil {
		return nil, "", "", err
	}
	return args, stdin, lastMessagePath, nil
}

func doctorCanaryRuntime(teamDir string, agent *loader.Agent, selection runtimeSelection) (runtimebin.Runtime, error) {
	if strings.TrimSpace(selection.Kind) != "" || strings.TrimSpace(selection.Binary) != "" {
		return runtimeFromConfigWithOverrides(filepath.Join(teamDir, "config.toml"), selection)
	}
	if strings.TrimSpace(os.Getenv(runtimebin.EnvRuntime)) != "" {
		return runtimebin.Current()
	}
	if agent != nil {
		if rt, ok, err := runtimebin.FromFields(agent.Runtime, agent.RuntimeBin); err != nil || ok {
			return rt, err
		}
	}
	return runtimebin.CurrentFromConfig(filepath.Join(teamDir, "config.toml"))
}

func doctorCanaryPrompt() string {
	return "Reply exactly with: " + doctorCanaryMarker
}

func doctorCanaryTeamEnv(teamDir, instance, stateDir string) []string {
	env := []string{
		"AGENT_TEAM_ROOT=" + teamDir,
		"AGENT_TEAM_INSTANCE=" + instance,
		"AGENT_TEAM_STATE_DIR=" + stateDir,
		"AGENT_TEAM_DAEMON_SOCKET=" + daemon.SocketPath(teamDir),
	}
	if httpAddr, err := daemon.ReadHTTPAddr(teamDir); err == nil && strings.TrimSpace(httpAddr) != "" {
		env = append(env, "AGENT_TEAM_DAEMON_URL="+daemon.DaemonHTTPURL(httpAddr))
	}
	return env
}

func waitDoctorCanaryExit(ctx context.Context, dc *daemonClient, instance string, timeout time.Duration) (*daemon.Metadata, bool, error) {
	if timeout <= 0 {
		timeout = defaultDoctorCanaryTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		meta, err := doctorCanaryMetadata(dc, instance)
		if err != nil {
			return nil, false, err
		}
		if meta != nil && meta.Status != daemon.StatusRunning {
			return meta, false, nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return meta, true, nil
			}
			return meta, false, ctx.Err()
		case <-ticker.C:
		}
	}
}

func doctorCanaryMetadata(dc *daemonClient, instance string) (*daemon.Metadata, error) {
	rows, err := dc.Instances()
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row != nil && row.Instance == instance {
			return row, nil
		}
	}
	return nil, nil
}

func doctorCanaryCleanup(result *doctorCanaryResult, dc *daemonClient, stateDir string) {
	if result == nil {
		return
	}
	var parts []string
	if dc != nil && strings.TrimSpace(result.Instance) != "" {
		if err := dc.RemoveInstance(result.Instance, true); err != nil && !strings.Contains(err.Error(), "unknown instance") {
			parts = append(parts, err.Error())
		}
	}
	if strings.TrimSpace(stateDir) != "" {
		if err := os.RemoveAll(stateDir); err != nil {
			parts = append(parts, "remove state: "+err.Error())
		}
	}
	if len(parts) > 0 {
		result.CleanupOK = false
		result.CleanupError = strings.Join(parts, "; ")
		result.addIssue("fail", "cleanup", "cleanup_failed", result.CleanupError, "Remove the canary instance with `agent-team stop "+result.Instance+" --rm --force` and delete its state directory if it remains.")
	}
	result.finalize()
}

func (r *doctorCanaryResult) addIssue(severity, source, id, summary, remediation string) {
	if r == nil {
		return
	}
	r.Issues = append(r.Issues, runtimeProbeIssue{
		Severity:    strings.TrimSpace(severity),
		Source:      strings.TrimSpace(source),
		ID:          strings.TrimSpace(id),
		Summary:     strings.TrimSpace(summary),
		Remediation: strings.TrimSpace(remediation),
	})
}

func (r *doctorCanaryResult) finalize() *doctorCanaryResult {
	if r == nil {
		return nil
	}
	r.OK = runtimeProbeOK(r.Issues)
	r.Actions = doctorCanaryActions(r)
	return r
}

func doctorCanaryActions(result *doctorCanaryResult) []string {
	added := map[string]bool{}
	add := func(action string) {
		action = strings.TrimSpace(action)
		if action != "" {
			added[action] = true
		}
	}
	if result == nil || len(result.Issues) == 0 {
		return nil
	}
	add("agent-team runtime --json")
	for _, issue := range result.Issues {
		switch issue.Source + "." + issue.ID {
		case "daemon.not_running":
			add("agent-team daemon start")
		case "daemon.not_ready", "daemon.connect_failed", "daemon.poll_failed":
			add("agent-team daemon status --wait")
			add("agent-team daemon logs --tail 80")
		case "runtime.binary_missing", "dispatch.binary_missing":
			add("agent-team runtime ls --json")
		case "cleanup.cleanup_failed":
			if result.Instance != "" {
				add("agent-team stop " + result.Instance + " --rm --force")
			}
		}
		if issue.Remediation != "" {
			switch issue.ID {
			case "provider_unreachable", "auth_failed", "last_message_missing", "marker_mismatch", "runtime_banner_missing":
				if result.Runtime == string(runtimebin.KindCodex) {
					add("agent-team runtime probe --runtime codex --exec --timeout 2m")
					add("codex doctor --summary")
				}
			}
		}
	}
	out := make([]string, 0, len(added))
	for action := range added {
		out = append(out, action)
	}
	sort.Strings(out)
	return out
}

func doctorCanaryDispatchIssueID(text string) string {
	lower := strings.ToLower(text)
	if doctorCanaryTextIndicatesMissingBinary(lower) {
		return "binary_missing"
	}
	probe := &runtimeExecProbe{Error: text}
	return runtimeExecProbeIssueID(probe)
}

func doctorCanaryRuntimeFailureIssueID(result *doctorCanaryResult) string {
	probe := &runtimeExecProbe{
		Runtime:     result.Runtime,
		Error:       fmt.Sprintf("canary exited with status %s", result.Status),
		Stdout:      result.LogExcerpt,
		Stderr:      result.LogExcerpt,
		LastMessage: result.LastMessage,
	}
	id := runtimeExecProbeIssueID(probe)
	if id == "exec_failed" && doctorCanaryTextIndicatesMissingBinary(result.LogExcerpt) {
		return "binary_missing"
	}
	return id
}

func doctorCanaryRuntimeFailureSummary(result *doctorCanaryResult, id string) string {
	if result == nil {
		return "canary runtime failed"
	}
	switch id {
	case "binary_missing":
		return fmt.Sprintf("daemon could not start runtime binary %q for %s", result.RuntimeBinary, result.Runtime)
	case "provider_unreachable", "auth_failed", "sandbox_blocked":
		probe := &runtimeExecProbe{Runtime: result.Runtime, Error: "canary runtime failed", Stderr: result.LogExcerpt, Stdout: result.LogExcerpt}
		return runtimeExecProbeIssueSummary(probe, id)
	default:
		detail := fmt.Sprintf("canary exited with status %s", emptyDash(result.Status))
		if result.ExitCode != nil {
			detail += fmt.Sprintf(" exit_code=%d", *result.ExitCode)
		}
		if strings.TrimSpace(result.LogExcerpt) != "" {
			detail += ": " + firstLine(result.LogExcerpt)
		}
		return detail
	}
}

func doctorCanaryIssueRemediation(result *doctorCanaryResult, id string) string {
	switch id {
	case "binary_missing":
		bin := "<runtime>"
		runtimeName := "<runtime>"
		if result != nil {
			if strings.TrimSpace(result.RuntimeBinary) != "" {
				bin = result.RuntimeBinary
			}
			if strings.TrimSpace(result.Runtime) != "" {
				runtimeName = result.Runtime
			}
		}
		return fmt.Sprintf("Install %q in the daemon launch PATH, restart agent-teamd, or pass --runtime-bin for %s.", bin, runtimeName)
	case "provider_unreachable", "auth_failed", "sandbox_blocked", "exec_timeout", "last_message_missing", "runtime_banner_missing":
		probe := &runtimeExecProbe{}
		if result != nil {
			probe.Runtime = result.Runtime
		}
		return runtimeExecProbeRemediation(probe, id)
	default:
		return "Inspect `agent-team daemon logs --tail 80` and the canary log, then rerun `agent-team doctor --canary`."
	}
}

func doctorCanaryTextIndicatesMissingBinary(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "executable file not found") ||
		strings.Contains(lower, "exec: not found") ||
		strings.Contains(lower, "no such file or directory") ||
		strings.Contains(lower, "cannot find") ||
		strings.Contains(lower, "lookup ") && strings.Contains(lower, "executable file not found")
}

func doctorCanaryLogExcerpt(meta *daemon.Metadata) string {
	if meta == nil || strings.TrimSpace(meta.LogPath) == "" {
		return ""
	}
	body, err := os.ReadFile(meta.LogPath)
	if err != nil {
		return ""
	}
	return runtimeProbeExcerpt(body)
}

func doctorCanaryReadLastMessage(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return runtimeProbeExcerpt(bytes.TrimSpace(body))
}

func doctorCanaryInstanceName(agent string) string {
	slug := dispatchSlug(agent)
	if slug == "" {
		slug = "agent"
	}
	return "doctor-canary-" + slug + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func findDoctorCanaryAgent(agents []*loader.Agent, name string) *loader.Agent {
	for _, agent := range agents {
		if agent != nil && agent.Name == name {
			return agent
		}
	}
	return nil
}

func doctorCanaryProblemSummary(result *doctorCanaryResult) string {
	if result == nil {
		return "canary failed"
	}
	for _, issue := range result.Issues {
		if issue.Severity == "fail" || issue.Severity == "error" {
			return issue.Summary
		}
	}
	return "canary failed"
}

func renderDoctorCanaryText(w fmtWriter, result *doctorCanaryResult) {
	if result == nil {
		return
	}
	if result.OK {
		fmt.Fprintf(w, "  canary: OK agent=%s runtime=%s instance=%s duration=%dms\n", result.Agent, result.Runtime, result.Instance, result.DurationMS)
		return
	}
	fmt.Fprintf(w, "  canary: failed agent=%s runtime=%s instance=%s\n", result.Agent, emptyDash(result.Runtime), result.Instance)
	for _, issue := range result.Issues {
		fmt.Fprintf(w, "    - %s/%s: %s\n", issue.Source, issue.ID, issue.Summary)
	}
}

func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line)
		}
	}
	return strings.TrimSpace(text)
}
