// Package main is the agent-teamd daemon entrypoint.
//
// agent-teamd is the per-repo orchestrator daemon that owns claude
// subprocess lifecycle (spawn / track / stop / resume) and serves a small
// JSON API over a unix socket. It is intentionally a separate binary from
// `agent-team` (the user-facing CLI) — the merge decision is deferred per
// `documentation/orchestrator.md` § Implementation language.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/agent-team-project/agent-team/internal/daemon"
)

// Version is overridden for release builds. It lives in the daemon entrypoint
// so agent-teamd does not import the CLI command tree (and therefore the TUI).
var Version = "0.1.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "agent-teamd:", err)
		os.Exit(1)
	}
}

func run(argv []string) (err error) {
	fs := flag.NewFlagSet("agent-teamd", flag.ContinueOnError)
	cwd, _ := os.Getwd()
	repo := fs.String("repo", cwd, "Repo root containing .agent_team/.")
	httpAddr := fs.String("http-addr", "", "Optional loopback HTTP listen address, e.g. 127.0.0.1:0.")
	showVersion := fs.Bool("version", false, "Print version and exit.")
	if err := fs.Parse(argv); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println("agent-teamd", buildinfo.Current(Version).VersionLine())
		return nil
	}
	build := buildinfo.Current(Version)
	if err := requireManagedCLI(build); err != nil {
		return err
	}

	abs, err := filepath.Abs(*repo)
	if err != nil {
		return fmt.Errorf("resolve repo: %w", err)
	}
	teamDir := filepath.Join(abs, ".agent_team")
	st, err := os.Stat(teamDir)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("%s not found — run `agent-team init` first", teamDir)
	}
	writeExitReason := func(reason daemon.ExitReason) {
		reason.PID = os.Getpid()
		reason.Build = build
		if writeErr := daemon.WriteExitReason(teamDir, reason); writeErr != nil {
			fmt.Fprintln(os.Stderr, "agent-teamd: write exit reason:", writeErr)
		}
	}
	defer func() {
		if r := recover(); r != nil {
			writeExitReason(daemon.ExitReason{
				Kind:   daemon.ExitKindPanic,
				Reason: fmt.Sprint(r),
				Error:  string(debug.Stack()),
			})
			panic(r)
		}
	}()

	d, err := daemon.New(daemon.Config{
		TeamDir:              teamDir,
		HTTPAddr:             *httpAddr,
		Build:                build,
		EnforceBuildIdentity: true,
	})
	if err != nil {
		writeExitReason(daemon.ExitReason{
			Kind:   daemon.ExitKindError,
			Reason: err.Error(),
			Error:  err.Error(),
		})
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signalCh)
	signalReasonCh := make(chan daemon.ExitReason, 1)
	go func() {
		sig := <-signalCh
		signalReasonCh <- daemon.ExitReason{
			Kind:   daemon.ExitKindSignal,
			Signal: sig.String(),
			Reason: "received " + sig.String(),
		}
		cancel()
	}()

	err = d.Run(ctx)
	select {
	case reason := <-signalReasonCh:
		writeExitReason(reason)
	default:
		if err != nil {
			writeExitReason(daemon.ExitReason{
				Kind:   daemon.ExitKindError,
				Reason: err.Error(),
				Error:  err.Error(),
			})
		} else {
			writeExitReason(daemon.ExitReason{
				Kind:   daemon.ExitKindShutdown,
				Reason: "clean shutdown",
			})
		}
	}
	return err
}

func requireManagedCLI(daemonBuild buildinfo.Info) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("activation needed: locate agent-teamd executable: %w", err)
	}
	cliPath := filepath.Join(filepath.Dir(executable), "agent-team")
	if st, statErr := os.Stat(cliPath); statErr != nil || st.IsDir() {
		cliPath, err = exec.LookPath("agent-team")
		if err != nil {
			return fmt.Errorf("activation needed: matching agent-team CLI is not installed alongside agent-teamd or on PATH")
		}
	}
	cliBuild, err := buildinfo.ReadFile(cliPath)
	if err != nil {
		return fmt.Errorf("activation needed: inspect managed CLI build provenance: %w", err)
	}
	comparison := buildinfo.Compare(cliBuild, daemonBuild)
	if !comparison.Comparable {
		return fmt.Errorf("activation needed: managed CLI has %s", comparison.Reason)
	}
	if !comparison.Equal {
		return fmt.Errorf("activation needed: managed CLI %s does not match daemon %s", cliBuild.Display(), daemonBuild.Display())
	}
	return nil
}
