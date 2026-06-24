package cli

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/intake"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/spf13/cobra"
)

func newIntakeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "intake",
		Short: "Normalize external events into topology events.",
		Long:  "Normalize external events such as Linear/GitHub webhooks and schedules into topology events handled by the daemon.",
	}
	cmd.AddCommand(newIntakeLinearCmd())
	cmd.AddCommand(newIntakeGitHubCmd())
	cmd.AddCommand(newIntakeScheduleCmd())
	cmd.AddCommand(newIntakeServiceCmd())
	cmd.AddCommand(newIntakeServeCmd())
	cmd.AddCommand(newIntakeSummaryCmd())
	cmd.AddCommand(newIntakeDuplicatesCmd())
	cmd.AddCommand(newIntakeDoctorCmd())
	cmd.AddCommand(newIntakeDeliveriesCmd())
	cmd.AddCommand(newIntakeReplayCmd())
	cmd.AddCommand(newIntakePruneCmd())
	return cmd
}

var intakeInput io.Reader = os.Stdin

func newIntakeLinearCmd() *cobra.Command {
	return newWebhookIntakeCmd("linear", intake.NormalizeLinear)
}

func newIntakeGitHubCmd() *cobra.Command {
	return newWebhookIntakeCmd("github", intake.NormalizeGitHub)
}

func newWebhookIntakeCmd(provider string, normalize func([]byte) (*intake.Event, error)) *cobra.Command {
	var (
		target        string
		payload       string
		payloadFile   string
		dryRun        bool
		previewRoutes bool
		reconcileJob  bool
		cleanupMerged bool
		verifyPR      bool
		advanceJob    bool
		workspace     string
		runtimeKind   string
		runtimeBin    string
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   provider,
		Short: "Normalize a " + provider + " webhook payload and publish it.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake %s: --format cannot be combined with --json.\n", provider)
				return exitErr(2)
			}
			if provider == "github" && cleanupMerged && !reconcileJob {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake github: --cleanup-merged requires --reconcile-job.")
				return exitErr(2)
			}
			if provider == "github" && verifyPR && !cleanupMerged {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake github: --verify-pr requires --cleanup-merged.")
				return exitErr(2)
			}
			if provider == "github" && advanceJob && !reconcileJob {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake github: --advance requires --reconcile-job.")
				return exitErr(2)
			}
			if previewRoutes && !dryRun {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake %s: --preview-triggers requires --dry-run.\n", provider)
				return exitErr(2)
			}
			tmpl, err := parseIntakeFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake %s: %v\n", provider, err)
				return exitErr(2)
			}
			body, err := intakePayload(payload, payloadFile)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake %s: %v\n", provider, err)
				return exitErr(2)
			}
			ev, err := normalize(body)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake %s: %v\n", provider, err)
				return exitErr(2)
			}
			var reconcile *job.ReconcileResult
			var cleanupPreview *jobCleanupPreview
			var advancePreview *jobAdvancePreview
			var advance *jobAdvanceResult
			var triggerPreview *eventPublishPreview
			cleanup := ""
			if (provider == "github" && reconcileJob) || previewRoutes {
				teamDir, err := resolveTeamDir(cmd, target)
				if err != nil {
					return err
				}
				if provider == "github" && reconcileJob {
					if dryRun {
						reconcile, cleanupPreview, advancePreview, err = previewGitHubIntakeJob(teamDir, ev, cleanupMerged, verifyPR, advanceJob, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin})
					} else {
						if err := preflightIntakeDaemon(teamDir); err != nil {
							fmt.Fprintln(cmd.ErrOrStderr(), err)
							return exitErr(2)
						}
						reconcile, cleanup, advance, err = reconcileGitHubIntakeJob(cmd, teamDir, ev, cleanupMerged, verifyPR, advanceJob, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin})
					}
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake github: %v\n", err)
						return exitErr(1)
					}
				}
				if previewRoutes {
					triggerPreview, err = previewEventPublish(teamDir, ev.Type, ev.Payload)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake %s: %v\n", provider, err)
						return exitErr(1)
					}
				}
			}
			if dryRun {
				return renderIntakeDryRun(cmd.OutOrStdout(), ev, jsonOut, tmpl, reconcile, cleanupPreview, advancePreview, triggerPreview)
			}
			return publishIntakeEventWithJob(cmd, target, ev, jsonOut, tmpl, reconcile, cleanup, advance)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&payload, "payload", "", "Webhook JSON object.")
	cmd.Flags().StringVar(&payloadFile, "payload-file", "", "Read webhook JSON from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Normalize and print the event without publishing to the daemon.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-triggers", false, "With --dry-run, include local topology instance and pipeline matches.")
	if provider == "github" {
		cmd.Flags().BoolVar(&reconcileJob, "reconcile-job", false, "Also reconcile the normalized PR event into the owning durable job.")
		cmd.Flags().BoolVar(&cleanupMerged, "cleanup-merged", false, "With --reconcile-job, remove the job-owned worktree and branch after a merged PR event.")
		cmd.Flags().BoolVar(&verifyPR, "verify-pr", false, "With --cleanup-merged, verify the recorded GitHub PR is merged with gh before cleanup.")
		cmd.Flags().BoolVar(&advanceJob, "advance", false, "With --reconcile-job, dispatch the next ready pipeline step after PR metadata is reconciled.")
		cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for --advance dispatch: auto, worktree, or repo.")
		cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for --advance dispatch (claude or codex). Overrides env and repo config.")
		cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for --advance dispatch. Overrides env and repo config.")
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit normalized event and daemon outcome as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the intake result with a Go template, e.g. '{{.Event.Type}}'.")
	return cmd
}

func newIntakeScheduleCmd() *cobra.Command {
	var (
		target        string
		payload       string
		payloadFile   string
		dryRun        bool
		previewRoutes bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "schedule <name>",
		Short: "Publish a named schedule event.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake schedule: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseIntakeFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake schedule: %v\n", err)
				return exitErr(2)
			}
			if previewRoutes && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake schedule: --preview-triggers requires --dry-run.")
				return exitErr(2)
			}
			override, label, err := optionalPayloadInput(payload, payloadFile)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake schedule: %v\n", err)
				return exitErr(2)
			}
			body := map[string]any{"source": "schedule", "name": args[0]}
			if strings.TrimSpace(string(override)) != "" {
				if err := json.Unmarshal(override, &body); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake schedule: %s is not valid JSON: %v\n", label, err)
					return exitErr(2)
				}
				body["source"] = "schedule"
				body["name"] = args[0]
			}
			ev := &intake.Event{Type: "schedule", Payload: body}
			if dryRun {
				var triggerPreview *eventPublishPreview
				if previewRoutes {
					teamDir, err := resolveTeamDir(cmd, target)
					if err != nil {
						return err
					}
					triggerPreview, err = previewEventPublish(teamDir, ev.Type, ev.Payload)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake schedule: %v\n", err)
						return exitErr(1)
					}
				}
				return renderIntakeDryRun(cmd.OutOrStdout(), ev, jsonOut, tmpl, nil, nil, nil, triggerPreview)
			}
			return publishIntakeEvent(cmd, target, ev, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&payload, "payload", "", "Additional JSON object merged into the schedule payload.")
	cmd.Flags().StringVar(&payloadFile, "payload-file", "", "Read additional schedule payload JSON from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Normalize and print the event without publishing to the daemon.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-triggers", false, "With --dry-run, include local topology instance and pipeline matches.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit normalized event and daemon outcome as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the intake result with a Go template, e.g. '{{.Event.Type}}'.")
	return cmd
}

type intakeServeOptions struct {
	DryRun                  bool
	PreviewTriggers         bool
	GitHubReconcileJob      bool
	GitHubCleanupMerged     bool
	GitHubVerifyPR          bool
	GitHubAdvanceJob        bool
	LinearSecret            string
	GitHubSecret            string
	RequireLinearSecret     bool
	RequireGitHubSecret     bool
	LinearMaxAge            time.Duration
	GitHubReplayWindow      time.Duration
	PruneOKOlderThan        time.Duration
	PruneRecoveredOlderThan time.Duration
	Now                     func() time.Time
	MaxBodyBytes            int64
}

type intakeServiceOptions struct {
	Target                  string
	Addr                    string
	Bin                     string
	Name                    string
	Description             string
	Image                   string
	ContainerWorkdir        string
	Publish                 string
	EnvFile                 string
	KubeSecretName          string
	KubeWorkspaceClaim      string
	KubeIngressHost         string
	KubeIngressClass        string
	KubeTLSSecret           string
	LinearSecretEnv         string
	GitHubSecretEnv         string
	RequireLinearSecret     bool
	RequireGitHubSecret     bool
	GitHubReconcileJob      bool
	GitHubCleanupMerged     bool
	GitHubVerifyPR          bool
	GitHubAdvanceJob        bool
	LinearMaxAge            time.Duration
	GitHubReplayWindow      time.Duration
	MaxBodyBytes            int64
	PruneOKOlderThan        time.Duration
	PruneRecoveredOlderThan time.Duration
}

const defaultGitHubReplayWindow = 24 * time.Hour
const defaultIntakeMaxBodyBytes int64 = 1 << 20

func newIntakeServiceCmd() *cobra.Command {
	var opts intakeServiceOptions
	opts.Addr = "127.0.0.1:8787"
	opts.Bin = "agent-team"
	opts.Name = "agent-team-intake"
	opts.Description = "agent-team intake server"
	opts.Image = "agent-team:local"
	opts.ContainerWorkdir = "/workspace"
	opts.Publish = "127.0.0.1:8787:8787"
	opts.LinearSecretEnv = "LINEAR_WEBHOOK_SECRET"
	opts.GitHubSecretEnv = "GITHUB_WEBHOOK_SECRET"
	opts.LinearMaxAge = time.Minute
	opts.GitHubReplayWindow = defaultGitHubReplayWindow
	opts.MaxBodyBytes = defaultIntakeMaxBodyBytes
	opts.PruneOKOlderThan = 7 * 24 * time.Hour
	opts.PruneRecoveredOlderThan = 7 * 24 * time.Hour
	cwd, _ := os.Getwd()
	opts.Target = cwd
	cmd := &cobra.Command{
		Use:   "service systemd|launchd|compose|kubernetes",
		Short: "Print service or deployment config for intake serve.",
		Long:  "Print a read-only service or deployment configuration for running `agent-team intake serve` against this repo. The command does not install, apply, or write the generated file.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := strings.ToLower(strings.TrimSpace(args[0]))
			if kind == "k8s" {
				kind = "kubernetes"
			}
			if kind != "systemd" && kind != "launchd" && kind != "compose" && kind != "kubernetes" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake service: service kind must be one of: systemd, launchd, compose, kubernetes.")
				return exitErr(2)
			}
			if (kind == "compose" || kind == "kubernetes") && !cmd.Flags().Changed("addr") {
				opts.Addr = "0.0.0.0:8787"
			}
			if err := validateIntakeServiceOptions(kind, opts); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake service: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, opts.Target)
			if err != nil {
				return err
			}
			repoRoot := filepath.Dir(teamDir)
			switch kind {
			case "launchd":
				renderIntakeLaunchdService(cmd.OutOrStdout(), repoRoot, opts)
			case "compose":
				renderIntakeComposeService(cmd.OutOrStdout(), repoRoot, opts)
			case "kubernetes":
				port, err := intakeServicePort(opts.Addr)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake service: %v\n", err)
					return exitErr(2)
				}
				renderIntakeKubernetesService(cmd.OutOrStdout(), repoRoot, opts, port)
			default:
				renderIntakeSystemdService(cmd.OutOrStdout(), repoRoot, opts)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&opts.Addr, "addr", opts.Addr, "Address for the webhook listener.")
	cmd.Flags().StringVar(&opts.Bin, "bin", opts.Bin, "agent-team binary path used in the service.")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "Service unit name/comment stem.")
	cmd.Flags().StringVar(&opts.Description, "description", opts.Description, "Service description.")
	cmd.Flags().StringVar(&opts.Image, "image", opts.Image, "Container image used by compose service generation.")
	cmd.Flags().StringVar(&opts.ContainerWorkdir, "container-workdir", opts.ContainerWorkdir, "Container working directory used by compose service generation.")
	cmd.Flags().StringVar(&opts.Publish, "publish", opts.Publish, "Compose port publication host:container mapping; empty omits ports.")
	cmd.Flags().StringVar(&opts.EnvFile, "env-file", "", "Secret environment file for systemd EnvironmentFile or compose env_file; launchd does not support this.")
	cmd.Flags().StringVar(&opts.KubeSecretName, "secret-name", "", "Kubernetes Secret name used by kubernetes service generation; defaults to <name>-secrets.")
	cmd.Flags().StringVar(&opts.KubeWorkspaceClaim, "workspace-claim", "", "Kubernetes PersistentVolumeClaim name mounted at --container-workdir; defaults to <name>-workspace.")
	cmd.Flags().StringVar(&opts.KubeIngressHost, "ingress-host", "", "Kubernetes Ingress host to expose the generated Service; kubernetes output only.")
	cmd.Flags().StringVar(&opts.KubeIngressClass, "ingress-class", "", "Kubernetes IngressClass name for --ingress-host; kubernetes output only.")
	cmd.Flags().StringVar(&opts.KubeTLSSecret, "tls-secret", "", "Kubernetes TLS Secret name for --ingress-host; kubernetes output only.")
	cmd.Flags().StringVar(&opts.LinearSecretEnv, "linear-secret-env", opts.LinearSecretEnv, "Environment variable name containing the Linear webhook secret; empty omits it.")
	cmd.Flags().StringVar(&opts.GitHubSecretEnv, "github-secret-env", opts.GitHubSecretEnv, "Environment variable name containing the GitHub webhook secret; empty omits it.")
	cmd.Flags().BoolVar(&opts.RequireLinearSecret, "require-linear-secret", false, "Include --require-linear-secret in ExecStart.")
	cmd.Flags().BoolVar(&opts.RequireGitHubSecret, "require-github-secret", false, "Include --require-github-secret in ExecStart.")
	cmd.Flags().BoolVar(&opts.GitHubReconcileJob, "github-reconcile-job", false, "Include --github-reconcile-job in ExecStart.")
	cmd.Flags().BoolVar(&opts.GitHubCleanupMerged, "github-cleanup-merged", false, "Include --github-cleanup-merged in ExecStart; requires --github-reconcile-job.")
	cmd.Flags().BoolVar(&opts.GitHubVerifyPR, "github-verify-pr", false, "Include --github-verify-pr in ExecStart; requires --github-cleanup-merged.")
	cmd.Flags().BoolVar(&opts.GitHubAdvanceJob, "github-advance-job", false, "Include --github-advance-job in ExecStart; requires --github-reconcile-job.")
	cmd.Flags().DurationVar(&opts.LinearMaxAge, "linear-max-age", opts.LinearMaxAge, "Maximum accepted Linear webhook age after signature verification.")
	cmd.Flags().DurationVar(&opts.GitHubReplayWindow, "github-replay-window", opts.GitHubReplayWindow, "Reject signed GitHub delivery IDs already seen within this duration. Use 0 to disable.")
	cmd.Flags().Int64Var(&opts.MaxBodyBytes, "max-body-bytes", opts.MaxBodyBytes, "Maximum webhook request body size accepted by intake serve.")
	cmd.Flags().DurationVar(&opts.PruneOKOlderThan, "prune-ok-older-than", opts.PruneOKOlderThan, "Prune successful delivery history older than this duration after each request. Use 0 to disable.")
	cmd.Flags().DurationVar(&opts.PruneRecoveredOlderThan, "prune-recovered-older-than", opts.PruneRecoveredOlderThan, "Prune recovered failed delivery history older than this duration after each request. Use 0 to disable.")
	return cmd
}

func validateIntakeServiceOptions(kind string, opts intakeServiceOptions) error {
	if strings.TrimSpace(opts.Name) == "" {
		return fmt.Errorf("--name is required")
	}
	if strings.TrimSpace(opts.Description) == "" {
		return fmt.Errorf("--description is required")
	}
	if kind == "compose" || kind == "kubernetes" {
		if strings.TrimSpace(opts.Image) == "" {
			return fmt.Errorf("--image is required")
		}
		if strings.TrimSpace(opts.ContainerWorkdir) == "" {
			return fmt.Errorf("--container-workdir is required")
		}
	}
	if kind == "kubernetes" {
		if !isKubernetesDNSLabel(opts.Name) {
			return fmt.Errorf("--name must be a Kubernetes DNS label for kubernetes output")
		}
		if secretName := kubernetesSecretName(opts); !isKubernetesDNSLabel(secretName) {
			return fmt.Errorf("--secret-name must be a Kubernetes DNS label")
		}
		if claimName := kubernetesWorkspaceClaim(opts); !isKubernetesDNSLabel(claimName) {
			return fmt.Errorf("--workspace-claim must be a Kubernetes DNS label")
		}
		if _, err := intakeServicePort(opts.Addr); err != nil {
			return err
		}
		if strings.TrimSpace(opts.KubeIngressClass) != "" && strings.TrimSpace(opts.KubeIngressHost) == "" {
			return fmt.Errorf("--ingress-class requires --ingress-host")
		}
		if tlsSecret := strings.TrimSpace(opts.KubeTLSSecret); tlsSecret != "" {
			if strings.TrimSpace(opts.KubeIngressHost) == "" {
				return fmt.Errorf("--tls-secret requires --ingress-host")
			}
			if !isKubernetesDNSLabel(tlsSecret) {
				return fmt.Errorf("--tls-secret must be a Kubernetes DNS label")
			}
		}
	} else if strings.TrimSpace(opts.KubeIngressHost) != "" || strings.TrimSpace(opts.KubeIngressClass) != "" || strings.TrimSpace(opts.KubeTLSSecret) != "" {
		return fmt.Errorf("--ingress-host, --ingress-class, and --tls-secret are only supported for kubernetes output")
	}
	if strings.TrimSpace(opts.Bin) == "" {
		return fmt.Errorf("--bin is required")
	}
	if strings.TrimSpace(opts.Addr) == "" {
		return fmt.Errorf("--addr is required")
	}
	if (kind == "launchd" || kind == "kubernetes") && strings.TrimSpace(opts.EnvFile) != "" {
		return fmt.Errorf("--env-file is not supported for %s", kind)
	}
	if opts.GitHubCleanupMerged && !opts.GitHubReconcileJob {
		return fmt.Errorf("--github-cleanup-merged requires --github-reconcile-job")
	}
	if opts.GitHubVerifyPR && !opts.GitHubCleanupMerged {
		return fmt.Errorf("--github-verify-pr requires --github-cleanup-merged")
	}
	if opts.GitHubAdvanceJob && !opts.GitHubReconcileJob {
		return fmt.Errorf("--github-advance-job requires --github-reconcile-job")
	}
	if opts.LinearMaxAge <= 0 {
		return fmt.Errorf("--linear-max-age must be > 0")
	}
	if opts.GitHubReplayWindow < 0 {
		return fmt.Errorf("--github-replay-window must be >= 0")
	}
	if opts.MaxBodyBytes <= 0 {
		return fmt.Errorf("--max-body-bytes must be > 0")
	}
	if opts.RequireLinearSecret && strings.TrimSpace(opts.LinearSecretEnv) == "" {
		return fmt.Errorf("--require-linear-secret requires --linear-secret-env")
	}
	if opts.RequireGitHubSecret && strings.TrimSpace(opts.GitHubSecretEnv) == "" {
		return fmt.Errorf("--require-github-secret requires --github-secret-env")
	}
	if opts.PruneOKOlderThan < 0 {
		return fmt.Errorf("--prune-ok-older-than must be >= 0")
	}
	if opts.PruneRecoveredOlderThan < 0 {
		return fmt.Errorf("--prune-recovered-older-than must be >= 0")
	}
	return nil
}

func renderIntakeSystemdService(w io.Writer, repoRoot string, opts intakeServiceOptions) {
	fmt.Fprintf(w, "# Save as /etc/systemd/system/%s.service\n", opts.Name)
	fmt.Fprintln(w, "[Unit]")
	fmt.Fprintf(w, "Description=%s\n", opts.Description)
	fmt.Fprintln(w, "After=network-online.target")
	fmt.Fprintln(w, "Wants=network-online.target")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "[Service]")
	fmt.Fprintln(w, "Type=simple")
	fmt.Fprintf(w, "WorkingDirectory=%s\n", repoRoot)
	if envFile := strings.TrimSpace(opts.EnvFile); envFile != "" {
		fmt.Fprintf(w, "EnvironmentFile=%s\n", envFile)
	} else {
		for _, env := range serviceSecretEnvs(opts) {
			fmt.Fprintf(w, "Environment=%s=replace-me\n", env)
		}
	}
	fmt.Fprintf(w, "ExecStartPre=%s daemon start\n", opts.Bin)
	fmt.Fprintf(w, "ExecStart=%s %s\n", opts.Bin, strings.Join(intakeServeArgs(opts), " "))
	fmt.Fprintln(w, "Restart=on-failure")
	fmt.Fprintln(w, "RestartSec=5s")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "[Install]")
	fmt.Fprintln(w, "WantedBy=multi-user.target")
}

func renderIntakeComposeService(w io.Writer, repoRoot string, opts intakeServiceOptions) {
	fmt.Fprintf(w, "# Save as docker-compose.%s.yml\n", opts.Name)
	fmt.Fprintln(w, "services:")
	fmt.Fprintf(w, "  %s:\n", yamlQuote(opts.Name))
	fmt.Fprintf(w, "    image: %s\n", yamlQuote(opts.Image))
	fmt.Fprintf(w, "    working_dir: %s\n", yamlQuote(opts.ContainerWorkdir))
	fmt.Fprintln(w, "    volumes:")
	fmt.Fprintf(w, "      - %s\n", yamlQuote(repoRoot+":"+opts.ContainerWorkdir))
	if publish := strings.TrimSpace(opts.Publish); publish != "" {
		fmt.Fprintln(w, "    ports:")
		fmt.Fprintf(w, "      - %s\n", yamlQuote(publish))
	}
	if envFile := strings.TrimSpace(opts.EnvFile); envFile != "" {
		fmt.Fprintln(w, "    env_file:")
		fmt.Fprintf(w, "      - %s\n", yamlQuote(envFile))
	} else if envs := serviceSecretEnvs(opts); len(envs) > 0 {
		fmt.Fprintln(w, "    environment:")
		for _, env := range envs {
			fmt.Fprintf(w, "      %s: %s\n", yamlQuote(env), yamlQuote("replace-me"))
		}
	}
	fmt.Fprintln(w, "    command:")
	for _, arg := range []string{"/bin/sh", "-lc", serviceShellCommand(opts)} {
		fmt.Fprintf(w, "      - %s\n", yamlQuote(arg))
	}
	fmt.Fprintln(w, "    restart: unless-stopped")
}

func renderIntakeKubernetesService(w io.Writer, repoRoot string, opts intakeServiceOptions, port int) {
	envs := serviceSecretEnvs(opts)
	secretName := kubernetesSecretName(opts)
	fmt.Fprintf(w, "# Save as kubernetes.%s.yaml\n", opts.Name)
	fmt.Fprintf(w, "# Mount a workspace PVC containing %s at %s.\n", repoRoot, opts.ContainerWorkdir)
	if len(envs) > 0 {
		fmt.Fprintln(w, "apiVersion: v1")
		fmt.Fprintln(w, "kind: Secret")
		fmt.Fprintln(w, "metadata:")
		fmt.Fprintf(w, "  name: %s\n", yamlQuote(secretName))
		fmt.Fprintln(w, "type: Opaque")
		fmt.Fprintln(w, "stringData:")
		for _, env := range envs {
			fmt.Fprintf(w, "  %s: %s\n", yamlQuote(env), yamlQuote("replace-me"))
		}
		fmt.Fprintln(w, "---")
	}
	fmt.Fprintln(w, "apiVersion: apps/v1")
	fmt.Fprintln(w, "kind: Deployment")
	fmt.Fprintln(w, "metadata:")
	fmt.Fprintf(w, "  name: %s\n", yamlQuote(opts.Name))
	fmt.Fprintln(w, "spec:")
	fmt.Fprintln(w, "  replicas: 1")
	fmt.Fprintln(w, "  selector:")
	fmt.Fprintln(w, "    matchLabels:")
	fmt.Fprintf(w, "      app.kubernetes.io/name: %s\n", yamlQuote(opts.Name))
	fmt.Fprintln(w, "  template:")
	fmt.Fprintln(w, "    metadata:")
	fmt.Fprintln(w, "      labels:")
	fmt.Fprintf(w, "        app.kubernetes.io/name: %s\n", yamlQuote(opts.Name))
	fmt.Fprintln(w, "    spec:")
	fmt.Fprintln(w, "      containers:")
	fmt.Fprintln(w, "        - name: \"intake\"")
	fmt.Fprintf(w, "          image: %s\n", yamlQuote(opts.Image))
	fmt.Fprintf(w, "          workingDir: %s\n", yamlQuote(opts.ContainerWorkdir))
	fmt.Fprintln(w, "          command:")
	for _, arg := range []string{"/bin/sh", "-lc", serviceShellCommand(opts)} {
		fmt.Fprintf(w, "            - %s\n", yamlQuote(arg))
	}
	fmt.Fprintln(w, "          ports:")
	fmt.Fprintln(w, "            - name: \"http\"")
	fmt.Fprintf(w, "              containerPort: %d\n", port)
	if len(envs) > 0 {
		fmt.Fprintln(w, "          env:")
		for _, env := range envs {
			fmt.Fprintf(w, "            - name: %s\n", yamlQuote(env))
			fmt.Fprintln(w, "              valueFrom:")
			fmt.Fprintln(w, "                secretKeyRef:")
			fmt.Fprintf(w, "                  name: %s\n", yamlQuote(secretName))
			fmt.Fprintf(w, "                  key: %s\n", yamlQuote(env))
		}
	}
	fmt.Fprintln(w, "          volumeMounts:")
	fmt.Fprintln(w, "            - name: \"workspace\"")
	fmt.Fprintf(w, "              mountPath: %s\n", yamlQuote(opts.ContainerWorkdir))
	fmt.Fprintln(w, "      volumes:")
	fmt.Fprintln(w, "        - name: \"workspace\"")
	fmt.Fprintln(w, "          persistentVolumeClaim:")
	fmt.Fprintf(w, "            claimName: %s\n", yamlQuote(kubernetesWorkspaceClaim(opts)))
	fmt.Fprintln(w, "---")
	fmt.Fprintln(w, "apiVersion: v1")
	fmt.Fprintln(w, "kind: Service")
	fmt.Fprintln(w, "metadata:")
	fmt.Fprintf(w, "  name: %s\n", yamlQuote(opts.Name))
	fmt.Fprintln(w, "spec:")
	fmt.Fprintln(w, "  selector:")
	fmt.Fprintf(w, "    app.kubernetes.io/name: %s\n", yamlQuote(opts.Name))
	fmt.Fprintln(w, "  ports:")
	fmt.Fprintln(w, "    - name: \"http\"")
	fmt.Fprintf(w, "      port: %d\n", port)
	fmt.Fprintf(w, "      targetPort: %d\n", port)
	if host := strings.TrimSpace(opts.KubeIngressHost); host != "" {
		renderIntakeKubernetesIngress(w, opts, port, host)
	}
}

func renderIntakeKubernetesIngress(w io.Writer, opts intakeServiceOptions, port int, host string) {
	fmt.Fprintln(w, "---")
	fmt.Fprintln(w, "apiVersion: networking.k8s.io/v1")
	fmt.Fprintln(w, "kind: Ingress")
	fmt.Fprintln(w, "metadata:")
	fmt.Fprintf(w, "  name: %s\n", yamlQuote(opts.Name))
	fmt.Fprintln(w, "spec:")
	if className := strings.TrimSpace(opts.KubeIngressClass); className != "" {
		fmt.Fprintf(w, "  ingressClassName: %s\n", yamlQuote(className))
	}
	if tlsSecret := strings.TrimSpace(opts.KubeTLSSecret); tlsSecret != "" {
		fmt.Fprintln(w, "  tls:")
		fmt.Fprintln(w, "    - hosts:")
		fmt.Fprintf(w, "        - %s\n", yamlQuote(host))
		fmt.Fprintf(w, "      secretName: %s\n", yamlQuote(tlsSecret))
	}
	fmt.Fprintln(w, "  rules:")
	fmt.Fprintf(w, "    - host: %s\n", yamlQuote(host))
	fmt.Fprintln(w, "      http:")
	fmt.Fprintln(w, "        paths:")
	fmt.Fprintln(w, "          - path: \"/\"")
	fmt.Fprintln(w, "            pathType: \"Prefix\"")
	fmt.Fprintln(w, "            backend:")
	fmt.Fprintln(w, "              service:")
	fmt.Fprintf(w, "                name: %s\n", yamlQuote(opts.Name))
	fmt.Fprintln(w, "                port:")
	fmt.Fprintf(w, "                  number: %d\n", port)
}

func renderIntakeLaunchdService(w io.Writer, repoRoot string, opts intakeServiceOptions) {
	fmt.Fprintf(w, "# Save as ~/Library/LaunchAgents/%s.plist\n", opts.Name)
	fmt.Fprintln(w, `<?xml version="1.0" encoding="UTF-8"?>`)
	fmt.Fprintln(w, `<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "https://www.apple.com/DTDs/PropertyList-1.0.dtd">`)
	fmt.Fprintln(w, `<plist version="1.0">`)
	fmt.Fprintln(w, `<dict>`)
	writeLaunchdKeyString(w, "  ", "Label", opts.Name)
	writeLaunchdKeyString(w, "  ", "WorkingDirectory", repoRoot)
	fmt.Fprintln(w, "  <key>EnvironmentVariables</key>")
	fmt.Fprintln(w, "  <dict>")
	for _, env := range serviceSecretEnvs(opts) {
		writeLaunchdKeyString(w, "    ", env, "replace-me")
	}
	fmt.Fprintln(w, "  </dict>")
	fmt.Fprintln(w, "  <key>ProgramArguments</key>")
	fmt.Fprintln(w, "  <array>")
	for _, arg := range []string{"/bin/sh", "-lc", serviceShellCommand(opts)} {
		fmt.Fprintf(w, "    <string>%s</string>\n", xmlEscape(arg))
	}
	fmt.Fprintln(w, "  </array>")
	fmt.Fprintln(w, "  <key>RunAtLoad</key>")
	fmt.Fprintln(w, "  <true/>")
	fmt.Fprintln(w, "  <key>KeepAlive</key>")
	fmt.Fprintln(w, "  <true/>")
	fmt.Fprintln(w, "</dict>")
	fmt.Fprintln(w, "</plist>")
}

func writeLaunchdKeyString(w io.Writer, indent string, key string, value string) {
	fmt.Fprintf(w, "%s<key>%s</key>\n", indent, xmlEscape(key))
	fmt.Fprintf(w, "%s<string>%s</string>\n", indent, xmlEscape(value))
}

func serviceShellCommand(opts intakeServiceOptions) string {
	start := append([]string{opts.Bin}, "daemon", "start")
	serve := append([]string{opts.Bin}, intakeServeArgs(opts)...)
	return strings.Join(shellQuoteArgs(start), " ") + " && exec " + strings.Join(shellQuoteArgs(serve), " ")
}

func serviceSecretEnvs(opts intakeServiceOptions) []string {
	envs := make([]string, 0, 2)
	if env := strings.TrimSpace(opts.LinearSecretEnv); env != "" {
		envs = append(envs, env)
	}
	if env := strings.TrimSpace(opts.GitHubSecretEnv); env != "" {
		envs = append(envs, env)
	}
	return envs
}

func kubernetesSecretName(opts intakeServiceOptions) string {
	if name := strings.TrimSpace(opts.KubeSecretName); name != "" {
		return name
	}
	return strings.TrimSpace(opts.Name) + "-secrets"
}

func kubernetesWorkspaceClaim(opts intakeServiceOptions) string {
	if name := strings.TrimSpace(opts.KubeWorkspaceClaim); name != "" {
		return name
	}
	return strings.TrimSpace(opts.Name) + "-workspace"
}

func intakeServicePort(addr string) (int, error) {
	_, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, fmt.Errorf("--addr must include a host and port for kubernetes output")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("--addr port must be between 1 and 65535")
	}
	return port, nil
}

func isKubernetesDNSLabel(value string) bool {
	if len(value) == 0 || len(value) > 63 {
		return false
	}
	for i, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			continue
		}
		if r == '-' && i > 0 && i < len(value)-1 {
			continue
		}
		return false
	}
	return true
}

func shellQuoteArgs(args []string) []string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return quoted
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool {
		return !(r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || r == '@' || r == '%' || r == '+' || r == '=' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'))
	}) == -1 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func yamlQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func intakeServeArgs(opts intakeServiceOptions) []string {
	args := []string{
		"intake", "serve",
		"--addr", opts.Addr,
		"--linear-max-age", opts.LinearMaxAge.String(),
		"--github-replay-window", opts.GitHubReplayWindow.String(),
		"--max-body-bytes", strconv.FormatInt(opts.MaxBodyBytes, 10),
		"--prune-ok-older-than", opts.PruneOKOlderThan.String(),
		"--prune-recovered-older-than", opts.PruneRecoveredOlderThan.String(),
	}
	if opts.GitHubReconcileJob {
		args = append(args, "--github-reconcile-job")
	}
	if opts.GitHubCleanupMerged {
		args = append(args, "--github-cleanup-merged")
	}
	if opts.GitHubVerifyPR {
		args = append(args, "--github-verify-pr")
	}
	if opts.GitHubAdvanceJob {
		args = append(args, "--github-advance-job")
	}
	if opts.RequireLinearSecret {
		args = append(args, "--require-linear-secret")
	}
	if opts.RequireGitHubSecret {
		args = append(args, "--require-github-secret")
	}
	return args
}

func newIntakeServeCmd() *cobra.Command {
	var (
		target string
		addr   string
		opts   intakeServeOptions
	)
	opts.PruneOKOlderThan = 7 * 24 * time.Hour
	opts.PruneRecoveredOlderThan = 7 * 24 * time.Hour
	opts.GitHubReplayWindow = defaultGitHubReplayWindow
	opts.MaxBodyBytes = defaultIntakeMaxBodyBytes
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run a local HTTP listener for external webhook intake.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.PreviewTriggers && !opts.DryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake serve: --preview-triggers requires --dry-run.")
				return exitErr(2)
			}
			if opts.GitHubCleanupMerged && !opts.GitHubReconcileJob {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake serve: --github-cleanup-merged requires --github-reconcile-job.")
				return exitErr(2)
			}
			if opts.GitHubVerifyPR && !opts.GitHubCleanupMerged {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake serve: --github-verify-pr requires --github-cleanup-merged.")
				return exitErr(2)
			}
			if opts.GitHubAdvanceJob && !opts.GitHubReconcileJob {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake serve: --github-advance-job requires --github-reconcile-job.")
				return exitErr(2)
			}
			if opts.LinearMaxAge <= 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake serve: --linear-max-age must be > 0.")
				return exitErr(2)
			}
			if opts.GitHubReplayWindow < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake serve: --github-replay-window must be >= 0.")
				return exitErr(2)
			}
			if opts.MaxBodyBytes <= 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake serve: --max-body-bytes must be > 0.")
				return exitErr(2)
			}
			if opts.PruneOKOlderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake serve: --prune-ok-older-than must be >= 0.")
				return exitErr(2)
			}
			if opts.PruneRecoveredOlderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake serve: --prune-recovered-older-than must be >= 0.")
				return exitErr(2)
			}
			opts.LinearSecret = firstNonEmpty(opts.LinearSecret, os.Getenv("LINEAR_WEBHOOK_SECRET"))
			opts.GitHubSecret = firstNonEmpty(opts.GitHubSecret, os.Getenv("GITHUB_WEBHOOK_SECRET"))
			if err := validateIntakeRequiredSecrets(opts); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake serve: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake serve: listen %s: %v\n", addr, err)
				return exitErr(1)
			}
			srv := &http.Server{
				Handler:           newIntakeServeHandler(teamDir, opts),
				ReadHeaderTimeout: 5 * time.Second,
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			errCh := make(chan error, 1)
			go func() {
				errCh <- srv.Serve(ln)
			}()
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake serve: listening on http://%s (POST /linear, POST /github, GET /healthz)\n", ln.Addr().String())
			select {
			case <-ctx.Done():
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := srv.Shutdown(shutdownCtx); err != nil {
					return err
				}
				if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
					return err
				}
				return nil
			case err := <-errCh:
				if err != nil && !errors.Is(err, http.ErrServerClosed) {
					return err
				}
				return nil
			}
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8787", "Address for the webhook listener.")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Normalize requests and return previews without publishing to the daemon.")
	cmd.Flags().BoolVar(&opts.PreviewTriggers, "preview-triggers", false, "With --dry-run, include local topology instance and pipeline matches.")
	cmd.Flags().BoolVar(&opts.GitHubReconcileJob, "github-reconcile-job", false, "For GitHub PR events, also reconcile the owning durable job.")
	cmd.Flags().BoolVar(&opts.GitHubCleanupMerged, "github-cleanup-merged", false, "With --github-reconcile-job, remove the job-owned worktree and branch after a merged PR event.")
	cmd.Flags().BoolVar(&opts.GitHubVerifyPR, "github-verify-pr", false, "With --github-cleanup-merged, verify recorded GitHub PRs are merged with gh before cleanup.")
	cmd.Flags().BoolVar(&opts.GitHubAdvanceJob, "github-advance-job", false, "With --github-reconcile-job, dispatch the next ready pipeline step after PR metadata is reconciled.")
	cmd.Flags().StringVar(&opts.LinearSecret, "linear-secret", "", "Linear webhook signing secret. Defaults to LINEAR_WEBHOOK_SECRET when set.")
	cmd.Flags().StringVar(&opts.GitHubSecret, "github-secret", "", "GitHub webhook secret. Defaults to GITHUB_WEBHOOK_SECRET when set.")
	cmd.Flags().BoolVar(&opts.RequireLinearSecret, "require-linear-secret", false, "Fail startup unless --linear-secret or LINEAR_WEBHOOK_SECRET is set.")
	cmd.Flags().BoolVar(&opts.RequireGitHubSecret, "require-github-secret", false, "Fail startup unless --github-secret or GITHUB_WEBHOOK_SECRET is set.")
	cmd.Flags().DurationVar(&opts.LinearMaxAge, "linear-max-age", time.Minute, "Maximum accepted Linear webhook age after signature verification.")
	cmd.Flags().DurationVar(&opts.GitHubReplayWindow, "github-replay-window", opts.GitHubReplayWindow, "Reject signed GitHub delivery IDs already seen within this duration. Use 0 to disable.")
	cmd.Flags().Int64Var(&opts.MaxBodyBytes, "max-body-bytes", opts.MaxBodyBytes, "Maximum webhook request body size accepted by the intake server.")
	cmd.Flags().DurationVar(&opts.PruneOKOlderThan, "prune-ok-older-than", opts.PruneOKOlderThan, "Prune successful delivery history older than this duration after each request. Use 0 to disable.")
	cmd.Flags().DurationVar(&opts.PruneRecoveredOlderThan, "prune-recovered-older-than", opts.PruneRecoveredOlderThan, "Prune recovered failed delivery history older than this duration after each request. Use 0 to disable.")
	return cmd
}

func validateIntakeRequiredSecrets(opts intakeServeOptions) error {
	if opts.RequireLinearSecret && strings.TrimSpace(opts.LinearSecret) == "" {
		return errors.New("--require-linear-secret set but Linear webhook secret is empty; pass --linear-secret or set LINEAR_WEBHOOK_SECRET")
	}
	if opts.RequireGitHubSecret && strings.TrimSpace(opts.GitHubSecret) == "" {
		return errors.New("--require-github-secret set but GitHub webhook secret is empty; pass --github-secret or set GITHUB_WEBHOOK_SECRET")
	}
	return nil
}

func newIntakeServeHandler(teamDir string, opts intakeServeOptions) http.Handler {
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = defaultIntakeMaxBodyBytes
	}
	if opts.LinearMaxAge == 0 {
		opts.LinearMaxAge = time.Minute
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				writeIntakeServeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			writeIntakeServeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		case "/linear":
			handleIntakeServeWebhook(w, r, teamDir, "linear", intake.NormalizeLinear, opts)
		case "/github":
			handleIntakeServeWebhook(w, r, teamDir, "github", intake.NormalizeGitHub, opts)
		default:
			writeIntakeServeError(w, http.StatusNotFound, "unknown intake endpoint")
		}
	})
}

func handleIntakeServeWebhook(w http.ResponseWriter, r *http.Request, teamDir, provider string, normalize func([]byte) (*intake.Event, error), opts intakeServeOptions) {
	delivery := newIntakeDeliveryRecord(provider, r, opts.Now().UTC(), opts.DryRun)
	fail := func(status int, message string) {
		delivery.Status = intakeDeliveryStatusError
		delivery.HTTPStatus = status
		delivery.Error = message
		_ = recordIntakeServeDelivery(teamDir, delivery, opts)
		writeIntakeServeError(w, status, message)
	}
	if r.Method != http.MethodPost {
		fail(http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, opts.MaxBodyBytes))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			fail(http.StatusRequestEntityTooLarge, "payload too large")
			return
		}
		fail(http.StatusBadRequest, fmt.Sprintf("read request body: %v", err))
		return
	}
	if status, err := verifyIntakeServeWebhook(provider, r.Header, body, opts); err != nil {
		fail(status, err.Error())
		return
	}
	if status, err := verifyIntakeServeReplay(teamDir, provider, delivery, opts); err != nil {
		fail(status, err.Error())
		return
	}
	ev, err := normalize(body)
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	delivery.EventType = ev.Type
	delivery.Payload = cloneIntakePayload(ev.Payload)
	delivery.Ticket = previewPayloadString(ev.Payload, "ticket")
	delivery.PR = previewPayloadString(ev.Payload, "pr_url")
	result, status, err := processIntakeServeEvent(teamDir, provider, ev, opts)
	if err != nil {
		fail(status, err.Error())
		return
	}
	delivery.Status = intakeDeliveryStatusOK
	delivery.HTTPStatus = status
	if result != nil {
		if result.Reconcile != nil && result.Reconcile.Job != nil {
			delivery.JobID = result.Reconcile.Job.ID
		}
		if result.Outcome != nil {
			delivery.Matched = append([]string(nil), result.Outcome.Matched...)
		}
		if result.Preview != nil {
			delivery.Matched = append([]string(nil), result.Preview.Matched...)
			delivery.Pipelines = append([]string(nil), result.Preview.Pipelines...)
		}
	}
	_ = recordIntakeServeDelivery(teamDir, delivery, opts)
	writeIntakeServeJSON(w, status, result)
}

func recordIntakeServeDelivery(teamDir string, delivery intakeDelivery, opts intakeServeOptions) error {
	if err := appendIntakeDelivery(teamDir, delivery); err != nil {
		return err
	}
	return pruneIntakeServeDeliveries(teamDir, opts, delivery.Time)
}

func pruneIntakeServeDeliveries(teamDir string, opts intakeServeOptions, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if opts.PruneOKOlderThan > 0 {
		if _, err := pruneIntakeDeliveries(teamDir, intakeDeliveryFilter{
			Status:       intakeDeliveryStatusOK,
			ReplayStatus: "any",
		}, opts.PruneOKOlderThan, now.UTC(), false); err != nil {
			return err
		}
	}
	if opts.PruneRecoveredOlderThan > 0 {
		if _, err := pruneIntakeDeliveries(teamDir, intakeDeliveryFilter{
			Status:       "all",
			ReplayStatus: intakeDeliveryReplayStatusOK,
		}, opts.PruneRecoveredOlderThan, now.UTC(), false); err != nil {
			return err
		}
	}
	return nil
}

func verifyIntakeServeWebhook(provider string, header http.Header, body []byte, opts intakeServeOptions) (int, error) {
	switch provider {
	case "linear":
		if opts.LinearSecret == "" {
			return http.StatusOK, nil
		}
		signature := header.Get("Linear-Signature")
		if signature == "" {
			return http.StatusUnauthorized, errors.New("missing Linear-Signature header")
		}
		if !verifyHexHMACSHA256(opts.LinearSecret, body, signature, "") {
			return http.StatusUnauthorized, errors.New("invalid Linear-Signature header")
		}
		sentAt, err := linearWebhookTimestamp(body)
		if err != nil {
			return http.StatusUnauthorized, err
		}
		now := opts.Now().UTC()
		if sentAt.After(now.Add(opts.LinearMaxAge)) || sentAt.Before(now.Add(-opts.LinearMaxAge)) {
			return http.StatusUnauthorized, errors.New("stale Linear webhook timestamp")
		}
	case "github":
		if opts.GitHubSecret == "" {
			return http.StatusOK, nil
		}
		signature := header.Get("X-Hub-Signature-256")
		if signature == "" {
			return http.StatusUnauthorized, errors.New("missing X-Hub-Signature-256 header")
		}
		if !verifyHexHMACSHA256(opts.GitHubSecret, body, signature, "sha256=") {
			return http.StatusUnauthorized, errors.New("invalid X-Hub-Signature-256 header")
		}
	}
	return http.StatusOK, nil
}

func verifyIntakeServeReplay(teamDir, provider string, delivery intakeDelivery, opts intakeServeOptions) (int, error) {
	if provider != "github" || strings.TrimSpace(opts.GitHubSecret) == "" || opts.GitHubReplayWindow <= 0 {
		return http.StatusOK, nil
	}
	requestID := strings.TrimSpace(delivery.RequestID)
	if requestID == "" {
		return http.StatusUnauthorized, errors.New("missing X-GitHub-Delivery header")
	}
	deliveries, err := listIntakeDeliveries(teamDir)
	if err != nil {
		return http.StatusInternalServerError, fmt.Errorf("read intake delivery history: %w", err)
	}
	cutoff := delivery.Time.Add(-opts.GitHubReplayWindow)
	for _, previous := range deliveries {
		if previous.Provider != "github" || strings.TrimSpace(previous.RequestID) != requestID {
			continue
		}
		if previous.HTTPStatus == http.StatusUnauthorized {
			continue
		}
		if previous.Time.IsZero() || previous.Time.Before(cutoff) {
			continue
		}
		return http.StatusConflict, fmt.Errorf("duplicate X-GitHub-Delivery %q", requestID)
	}
	return http.StatusOK, nil
}

func verifyHexHMACSHA256(secret string, body []byte, headerValue, prefix string) bool {
	value := strings.TrimSpace(headerValue)
	if prefix != "" {
		if !strings.HasPrefix(value, prefix) {
			return false
		}
		value = strings.TrimPrefix(value, prefix)
	}
	actual, err := hex.DecodeString(value)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	expected := mac.Sum(nil)
	return hmac.Equal(actual, expected)
}

func linearWebhookTimestamp(body []byte) (time.Time, error) {
	var raw struct {
		WebhookTimestamp json.RawMessage `json:"webhookTimestamp"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return time.Time{}, fmt.Errorf("decode Linear webhook timestamp: %w", err)
	}
	if len(raw.WebhookTimestamp) == 0 {
		return time.Time{}, errors.New("missing Linear webhook timestamp")
	}
	var millis int64
	if err := json.Unmarshal(raw.WebhookTimestamp, &millis); err != nil {
		var asString string
		if stringErr := json.Unmarshal(raw.WebhookTimestamp, &asString); stringErr != nil {
			return time.Time{}, errors.New("invalid Linear webhook timestamp")
		}
		parsed, parseErr := strconv.ParseInt(asString, 10, 64)
		if parseErr != nil {
			return time.Time{}, errors.New("invalid Linear webhook timestamp")
		}
		millis = parsed
	}
	return time.UnixMilli(millis).UTC(), nil
}

func processIntakeServeEvent(teamDir, provider string, ev *intake.Event, opts intakeServeOptions) (*intakePublishResult, int, error) {
	if opts.DryRun {
		result := &intakePublishResult{Event: ev, DryRun: true}
		if provider == "github" && opts.GitHubReconcileJob {
			reconcile, cleanupPreview, advancePreview, err := previewGitHubIntakeJob(teamDir, ev, opts.GitHubCleanupMerged, opts.GitHubVerifyPR, opts.GitHubAdvanceJob, "auto", runtimeSelection{})
			if err != nil {
				return nil, http.StatusInternalServerError, err
			}
			result.Reconcile = reconcile
			result.CleanupPreview = cleanupPreview
			result.AdvancePreview = advancePreview
		}
		if opts.PreviewTriggers {
			preview, err := previewEventPublish(teamDir, ev.Type, ev.Payload)
			if err != nil {
				return nil, http.StatusInternalServerError, err
			}
			result.Preview = preview
		}
		return result, http.StatusOK, nil
	}

	var reconcile *job.ReconcileResult
	var advance *jobAdvanceResult
	cleanup := ""
	if provider == "github" && opts.GitHubReconcileJob {
		if err := preflightIntakeDaemon(teamDir); err != nil {
			return nil, http.StatusServiceUnavailable, err
		}
		var err error
		reconcile, cleanup, advance, err = reconcileGitHubIntakeJob(nil, teamDir, ev, opts.GitHubCleanupMerged, opts.GitHubVerifyPR, opts.GitHubAdvanceJob, "auto", runtimeSelection{})
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
	}
	dc, err := newDaemonClient(teamDir)
	if err != nil {
		if errors.Is(err, errDaemonNotRunning) {
			return nil, http.StatusServiceUnavailable, errors.New("agent-team intake: daemon is not running — start it first with `agent-team daemon start`")
		}
		return nil, http.StatusInternalServerError, err
	}
	outcome, err := dc.PublishEvent(ev.Type, ev.Payload)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	return &intakePublishResult{Event: ev, Outcome: outcome, Reconcile: reconcile, Cleanup: cleanup, Advance: advance}, http.StatusOK, nil
}

func writeIntakeServeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeIntakeServeError(w http.ResponseWriter, status int, message string) {
	writeIntakeServeJSON(w, status, map[string]string{"error": message})
}

func intakePayload(payload, payloadFile string) ([]byte, error) {
	hasPayload := strings.TrimSpace(payload) != ""
	hasFile := strings.TrimSpace(payloadFile) != ""
	if hasPayload == hasFile {
		return nil, fmt.Errorf("provide exactly one of --payload or --payload-file")
	}
	if hasPayload {
		return []byte(payload), nil
	}
	return readPayloadFile(payloadFile)
}

func optionalPayloadInput(payload, payloadFile string) ([]byte, string, error) {
	hasPayload := strings.TrimSpace(payload) != ""
	hasFile := strings.TrimSpace(payloadFile) != ""
	if hasPayload && hasFile {
		return nil, "", fmt.Errorf("choose one of --payload or --payload-file")
	}
	if hasPayload {
		return []byte(payload), "--payload", nil
	}
	if hasFile {
		body, err := readPayloadFile(payloadFile)
		if err != nil {
			return nil, "", err
		}
		return body, "--payload-file", nil
	}
	return nil, "", nil
}

func readPayloadFile(payloadFile string) ([]byte, error) {
	if strings.TrimSpace(payloadFile) == "-" {
		body, err := io.ReadAll(intakeInput)
		if err != nil {
			return nil, fmt.Errorf("--payload-file -: %w", err)
		}
		return body, nil
	}
	body, err := os.ReadFile(filepath.Clean(payloadFile))
	if err != nil {
		return nil, fmt.Errorf("--payload-file: %w", err)
	}
	return body, nil
}

type intakePublishResult struct {
	Event          *intake.Event        `json:"event"`
	Outcome        *eventResponse       `json:"outcome"`
	Reconcile      *job.ReconcileResult `json:"reconcile,omitempty"`
	Cleanup        string               `json:"cleanup,omitempty"`
	CleanupPreview *jobCleanupPreview   `json:"cleanup_preview,omitempty"`
	Advance        *jobAdvanceResult    `json:"advance,omitempty"`
	AdvancePreview *jobAdvancePreview   `json:"advance_preview,omitempty"`
	Preview        *eventPublishPreview `json:"preview,omitempty"`
	DryRun         bool                 `json:"dry_run,omitempty"`
}

func parseIntakeFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("intake-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderIntakeDryRun(w io.Writer, ev *intake.Event, jsonOut bool, tmpl *template.Template, reconcile *job.ReconcileResult, cleanupPreview *jobCleanupPreview, advancePreview *jobAdvancePreview, triggerPreview *eventPublishPreview) error {
	result := intakePublishResult{Event: ev, Reconcile: reconcile, CleanupPreview: cleanupPreview, AdvancePreview: advancePreview, Preview: triggerPreview, DryRun: true}
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if tmpl != nil {
		return renderIntakeTemplate(w, result, tmpl)
	}
	fmt.Fprintf(w, "Event: %s\n", ev.Type)
	if len(ev.Payload) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tVALUE")
	keys := make([]string, 0, len(ev.Payload))
	for key := range ev.Payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(tw, "%s\t%v\n", key, ev.Payload[key])
	}
	_ = tw.Flush()
	if reconcile != nil && reconcile.Job != nil {
		fmt.Fprintf(w, "Job: %s would reconcile by %s status=%s\n", reconcile.Job.ID, reconcile.MatchedBy, reconcile.Job.Status)
	}
	if cleanupPreview != nil {
		fmt.Fprintf(w, "Cleanup: %s\n", cleanupPreview.Summary)
	}
	if advancePreview != nil {
		if advancePreview.Message != "" {
			fmt.Fprintf(w, "Advance: %s\n", advancePreview.Message)
		} else if advancePreview.Step != nil {
			fmt.Fprintf(w, "Advance: would dispatch step %s\n", advancePreview.Step.ID)
		}
	}
	if triggerPreview != nil {
		if !eventPublishPreviewHasRoutes(triggerPreview) {
			fmt.Fprintln(w, "Triggers: none")
		} else {
			return renderEventPublishRoutePreview(w, triggerPreview)
		}
	}
	return nil
}

func preflightIntakeDaemon(teamDir string) error {
	if _, err := newDaemonClient(teamDir); err != nil {
		if errors.Is(err, errDaemonNotRunning) {
			return errors.New("agent-team intake: daemon is not running — start it first with `agent-team daemon start`.")
		}
		return err
	}
	return nil
}

func publishIntakeEvent(cmd *cobra.Command, target string, ev *intake.Event, jsonOut bool, tmpl *template.Template) error {
	return publishIntakeEventWithJob(cmd, target, ev, jsonOut, tmpl, nil, "", nil)
}

func publishIntakeEventWithJob(cmd *cobra.Command, target string, ev *intake.Event, jsonOut bool, tmpl *template.Template, reconcile *job.ReconcileResult, cleanup string, advance *jobAdvanceResult) error {
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	dc, err := newDaemonClient(teamDir)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake: daemon is not running — start it first with `agent-team daemon start`.")
		return exitErr(2)
	}
	res, err := dc.PublishEvent(ev.Type, ev.Payload)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake: %v\n", err)
		return exitErr(1)
	}
	out := cmd.OutOrStdout()
	result := intakePublishResult{Event: ev, Outcome: res, Reconcile: reconcile, Cleanup: cleanup, Advance: advance}
	if jsonOut {
		return json.NewEncoder(out).Encode(result)
	}
	if tmpl != nil {
		return renderIntakeTemplate(out, result, tmpl)
	}
	fmt.Fprintf(out, "Event: %s\n", ev.Type)
	if err := renderIntakeOutcome(out, res); err != nil {
		return err
	}
	if reconcile != nil && reconcile.Job != nil {
		fmt.Fprintf(out, "Job: %s reconciled by %s status=%s\n", reconcile.Job.ID, reconcile.MatchedBy, reconcile.Job.Status)
	}
	if cleanup != "" {
		fmt.Fprintf(out, "Cleanup: %s\n", cleanup)
	}
	if advance != nil {
		if advance.Message != "" {
			fmt.Fprintf(out, "Advance: %s\n", advance.Message)
		} else if advance.Step != nil {
			fmt.Fprintf(out, "Advance: dispatched step %s status=%s\n", advance.Step.ID, advance.Step.Status)
		}
	}
	return nil
}

func reconcileGitHubIntakeJob(cmd *cobra.Command, teamDir string, ev *intake.Event, cleanupMerged bool, verifyPR bool, advance bool, workspace string, selection runtimeSelection) (*job.ReconcileResult, string, *jobAdvanceResult, error) {
	if cmd == nil {
		cmd = &cobra.Command{}
	}
	result, err := job.ReconcilePR(teamDir, job.ReconcileInputFromPayload(ev.Type, ev.Payload), time.Now().UTC())
	if err != nil {
		return nil, "", nil, err
	}
	cleanup := ""
	if cleanupMerged && result.Job.Status == job.StatusDone {
		repoRoot := filepath.Dir(teamDir)
		cleanup, err = cleanupJobOwnedWorktree(repoRoot, result.Job, false, verifyPR)
		if err != nil {
			return nil, "", nil, err
		}
		result.Job.Worktree = ""
		result.Job.Branch = ""
		result.Job.LastStatus = strings.TrimSpace(result.Job.LastStatus + "; cleanup: " + cleanup)
		result.Job.UpdatedAt = time.Now().UTC()
		if err := writeJobWithAudit(teamDir, result.Job, "cleanup", "cli", cleanup, nil); err != nil {
			return nil, "", nil, err
		}
	}
	var advanceResult *jobAdvanceResult
	if advance {
		advanceResult, err = advanceJob(cmd, teamDir, result.Job, workspace, selection)
		if err != nil {
			return nil, "", nil, err
		}
		if advanceResult != nil && advanceResult.Job != nil {
			result.Job = advanceResult.Job
		}
	}
	return result, cleanup, advanceResult, nil
}

func previewGitHubIntakeJob(teamDir string, ev *intake.Event, cleanupMerged bool, verifyPR bool, advance bool, workspace string, selection runtimeSelection) (*job.ReconcileResult, *jobCleanupPreview, *jobAdvancePreview, error) {
	result, err := job.PreviewReconcilePR(teamDir, job.ReconcileInputFromPayload(ev.Type, ev.Payload), time.Now().UTC())
	if err != nil {
		return nil, nil, nil, err
	}
	var cleanupPreview *jobCleanupPreview
	if cleanupMerged && result.Job.Status == job.StatusDone {
		preview, err := previewJobCleanup(filepath.Dir(teamDir), result.Job, false, verifyPR)
		if err != nil {
			return nil, nil, nil, err
		}
		cleanupPreview = &preview
	}
	var advancePreview *jobAdvancePreview
	if advance {
		advancePreview, err = previewJobAdvanceDispatch(teamDir, result.Job, workspace, selection)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	return result, cleanupPreview, advancePreview, nil
}

func renderIntakeTemplate(w io.Writer, result intakePublishResult, tmpl *template.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func renderIntakeOutcome(w io.Writer, res *eventResponse) error {
	if len(res.Matched) == 0 {
		_, err := fmt.Fprintln(w, "(no triggers matched)")
		return err
	}
	fmt.Fprintf(w, "Matched: %s\n", strings.Join(res.Matched, ", "))
	for _, d := range res.Dispatched {
		name, _ := d["instance"].(string)
		id, _ := d["instance_id"].(string)
		fmt.Fprintf(w, "  dispatched %s as %s\n", name, id)
	}
	for _, n := range res.Queued {
		fmt.Fprintf(w, "  queued %s (at replica capacity)\n", n)
	}
	for _, n := range res.Messaged {
		fmt.Fprintf(w, "  messaged %s\n", n)
	}
	for _, r := range res.Rejected {
		name, _ := r["instance"].(string)
		reason, _ := r["reason"].(string)
		fmt.Fprintf(w, "  rejected %s: %s\n", name, reason)
	}
	return nil
}
