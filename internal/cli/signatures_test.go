package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignaturesTestDryRun(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[pipelines.ticket_to_pr.infra_signatures]
fixture_reaped = 'Os \{ code: 2, kind: NotFound'
missing_deps = 'deps/[^ ]*: No such file'

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
`), 0o644); err != nil {
		t.Fatalf("write instances.toml: %v", err)
	}
	logPath := filepath.Join(root, "gate.log")
	if err := os.WriteFile(logPath, []byte("failed while opening deps/cache: No such file\nassertion mentioned NotFound but not the infra shape\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"signatures", "test", "ticket_to_pr", "--repo", root, "--against", logPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("signatures test: %v\nstderr=%s", err, stderr.String())
	}
	body := out.String()
	for _, want := range []string{"fixture_reaped", "no-match", "missing_deps", "match", "deps/cache: No such file"} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q\nbody:\n%s", want, body)
		}
	}

	jsonCmd := NewRootCmd()
	jsonOut, jsonErr := &bytes.Buffer{}, &bytes.Buffer{}
	jsonCmd.SetOut(jsonOut)
	jsonCmd.SetErr(jsonErr)
	jsonCmd.SetArgs([]string{"signatures", "test", "ticket_to_pr", "--repo", root, "--against", logPath, "--json"})
	if err := jsonCmd.Execute(); err != nil {
		t.Fatalf("signatures test json: %v\nstderr=%s", err, jsonErr.String())
	}
	var result signatureTestResult
	if err := json.Unmarshal(jsonOut.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\nbody=%s", err, jsonOut.String())
	}
	if result.Pipeline != "ticket_to_pr" || len(result.Signatures) != 2 || !result.Signatures[1].Matched || result.Signatures[1].Excerpt != "deps/cache: No such file" {
		t.Fatalf("json result = %+v", result)
	}
}
