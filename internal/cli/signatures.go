package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

type signatureTestResult struct {
	Pipeline   string                        `json:"pipeline"`
	LogFile    string                        `json:"log_file"`
	Signatures []job.GateSignatureTestResult `json:"signatures"`
}

func newSignaturesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "signatures",
		Short: "Inspect pipeline infra signatures.",
		Long:  "Inspect pipeline infra_signatures without writing job state.",
	}
	cmd.AddCommand(newSignaturesTestCmd())
	return cmd
}

func newSignaturesTestCmd() *cobra.Command {
	var (
		repo    string
		against string
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "test <pipeline>",
		Short: "Dry-run a pipeline's infra signatures against a log file.",
		Long: "Dry-run a pipeline's infra_signatures against a log file. " +
			"Each signature is reported as match or no-match, and matches include the matched excerpt.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(against) == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team signatures test: --against is required.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			top, err := topology.LoadFromTeamDir(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team signatures test: %v\n", err)
				return exitErr(1)
			}
			if top == nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team signatures test: topology is empty.")
				return exitErr(1)
			}
			pipelineName := strings.TrimSpace(args[0])
			pipeline := top.Pipelines[pipelineName]
			if pipeline == nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team signatures test: unknown pipeline %q.\n", pipelineName)
				return exitErr(2)
			}
			logBody, err := os.ReadFile(against)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team signatures test: read %s: %v\n", against, err)
				return exitErr(1)
			}
			matchers, err := job.CompileGateSignatureMatchers(pipeline.InfraSignatures)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team signatures test: pipeline %q %v\n", pipeline.Name, err)
				return exitErr(1)
			}
			result := signatureTestResult{
				Pipeline:   pipeline.Name,
				LogFile:    against,
				Signatures: job.TestGateSignatureMatchers(matchers, string(logBody)),
			}
			return renderSignatureTestResult(cmd.OutOrStdout(), result, jsonOut)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&against, "against", "", "Log file to test against the pipeline infra signatures.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit signature test results as JSON.")
	return cmd
}

func renderSignatureTestResult(w io.Writer, result signatureTestResult, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	fmt.Fprintf(w, "Pipeline: %s\n", result.Pipeline)
	fmt.Fprintf(w, "Log:      %s\n", result.LogFile)
	if len(result.Signatures) == 0 {
		fmt.Fprintln(w, "(no infra signatures)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SIGNATURE\tRESULT\tPATTERN\tEXCERPT")
	for _, signature := range result.Signatures {
		status := "no-match"
		if signature.Matched {
			status = "match"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			signature.Name,
			status,
			signature.Pattern,
			emptyDash(signature.Excerpt),
		)
	}
	return tw.Flush()
}
