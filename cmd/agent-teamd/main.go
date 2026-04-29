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
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/jamesaud/agent-team/internal/daemon"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "agent-teamd:", err)
		os.Exit(1)
	}
}

func run(argv []string) error {
	fs := flag.NewFlagSet("agent-teamd", flag.ContinueOnError)
	cwd, _ := os.Getwd()
	target := fs.String("target", cwd, "Repo root containing .agent_team/.")
	if err := fs.Parse(argv); err != nil {
		return err
	}

	abs, err := filepath.Abs(*target)
	if err != nil {
		return fmt.Errorf("resolve target: %w", err)
	}
	teamDir := filepath.Join(abs, ".agent_team")
	st, err := os.Stat(teamDir)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("%s not found — run `agent-team init` first", teamDir)
	}

	d, err := daemon.New(daemon.Config{TeamDir: teamDir})
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	return d.Run(ctx)
}
