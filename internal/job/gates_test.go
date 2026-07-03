package job

import "testing"

func TestGateSignatureMatchersClassifyAndDryRun(t *testing.T) {
	matchers, err := CompileGateSignatureMatchers(map[string]string{
		"fixture_reaped": `Os \{ code: 2, kind: NotFound`,
		"missing_deps":   `deps/[^ ]*: No such file`,
	})
	if err != nil {
		t.Fatalf("CompileGateSignatureMatchers: %v", err)
	}

	classification := ClassifyGateRecord(matchers, GateRecord{
		Name:      "runtime-check",
		Status:    GateStatusFail,
		Signature: "panicked at Os { code: 2, kind: NotFound, message: \"No such file or directory\" }",
	})
	if classification.Class != GateClassInfra || classification.MatchedSignature != "fixture_reaped" || classification.MatchedPattern != `Os \{ code: 2, kind: NotFound` {
		t.Fatalf("classification = %+v", classification)
	}

	content := ClassifyGateRecord(matchers, GateRecord{
		Name:      "unit-tests",
		Status:    GateStatusFail,
		Signature: "assertion failed: expected NotFound error text",
	})
	if content.Class != GateClassContent || content.MatchedSignature != "" || content.MatchedPattern != "" {
		t.Fatalf("content classification = %+v", content)
	}

	results := TestGateSignatureMatchers(matchers, "cargo test\npanicked at Os { code: 2, kind: NotFound, message: \"No such file or directory\" }\n")
	if len(results) != 2 {
		t.Fatalf("results = %+v", results)
	}
	if results[0].Name != "fixture_reaped" || !results[0].Matched || results[0].Excerpt != "Os { code: 2, kind: NotFound" {
		t.Fatalf("first result = %+v", results[0])
	}
	if results[1].Name != "missing_deps" || results[1].Matched {
		t.Fatalf("second result = %+v", results[1])
	}
}
