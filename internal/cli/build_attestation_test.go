package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
)

func TestManagedCLIBuildAttestationIsReadOnlyAndMachineReadable(t *testing.T) {
	headerCmd := NewRootCmd()
	headerOut := &bytes.Buffer{}
	headerCmd.SetOut(headerOut)
	headerCmd.SetArgs([]string{"__build-attestation", "--header"})
	if err := headerCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	gotBuild, err := buildinfo.ParseHeaderValue(headerOut.String())
	if err != nil {
		t.Fatalf("parse header: %v", err)
	}
	if !buildinfo.Equivalent(gotBuild, BuildInfo()) {
		t.Fatalf("header build = %+v, want %+v", gotBuild, BuildInfo())
	}

	jsonCmd := NewRootCmd()
	jsonOut := &bytes.Buffer{}
	jsonCmd.SetOut(jsonOut)
	jsonCmd.SetArgs([]string{"__build-attestation"})
	if err := jsonCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got managedBuildAttestation
	if err := json.Unmarshal(jsonOut.Bytes(), &got); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, jsonOut.String())
	}
	if got.Schema != managedBuildAttestationSchema || got.Kind != "managed_cli" || got.CLIHeader == "" {
		t.Fatalf("attestation = %+v", got)
	}
}
