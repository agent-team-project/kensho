// Package main is the agent-team CLI entrypoint.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/jamesaud/agent-team/internal/cli"
)

func main() {
	err := cli.NewRootCmd().Execute()
	if err == nil {
		return
	}
	var ec cli.ExitCode
	if errors.As(err, &ec) {
		os.Exit(int(ec))
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
