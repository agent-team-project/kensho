package buildinfo

import (
	"os"
	"testing"
)

const testRevision = "3d5921d9c5d8115359ed1519c9d448981cd5abc7"

func TestCompareUsesImmutableSourceIdentity(t *testing.T) {
	for _, tc := range []struct {
		name       string
		left       Info
		right      Info
		comparable bool
		equal      bool
	}{
		{name: "same revision", left: Info{Revision: testRevision}, right: Info{SourceID: "git:" + testRevision}, comparable: true, equal: true},
		{name: "different revision", left: Info{Revision: testRevision}, right: Info{Revision: "b062047f293f61a22433f99587821f50b7ba421a"}, comparable: true},
		{name: "version is diagnostic", left: Info{Version: "v1", Revision: testRevision}, right: Info{Version: "v2", Revision: testRevision}, comparable: true, equal: true},
		{name: "stable module siblings", left: Info{ModulePath: "example.com/tool", ModuleVersion: "v1.2.3"}, right: Info{SourceID: "module:example.com/tool@v1.2.3"}, comparable: true, equal: true},
		{name: "revisionless devel", left: Info{Version: "v1"}, right: Info{Version: "v1"}},
		{name: "dirty revisions", left: Info{Revision: testRevision, Modified: true}, right: Info{Revision: testRevision, Modified: true}},
		{name: "linked marker overrides unreliable VCS metadata", left: Info{Revision: testRevision, Modified: true, SourceID: "git:b062047f293f61a22433f99587821f50b7ba421a"}, right: Info{Revision: "b062047f293f61a22433f99587821f50b7ba421a"}, comparable: true, equal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Compare(tc.left, tc.right)
			if got.Comparable != tc.comparable || got.Equal != tc.equal {
				t.Fatalf("Compare(%+v, %+v) = %+v, want comparable=%t equal=%t", tc.left, tc.right, got, tc.comparable, tc.equal)
			}
		})
	}
}

func TestHeaderRoundTripPreservesSourceIdentity(t *testing.T) {
	want := Info{Version: "v1", ModulePath: "example.com/tool", ModuleVersion: "(devel)", Revision: testRevision, Time: "2026-07-15T00:00:00Z", SourceID: "git:" + testRevision}
	got, err := ParseHeaderValue(want.HeaderValue())
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
	for _, raw := range []string{"source_id=bogus", "source_id=git%3Axyz", "version=1&version=2", "surprise=true"} {
		if _, err := ParseHeaderValue(raw); err == nil {
			t.Fatalf("ParseHeaderValue(%q) succeeded", raw)
		}
	}
}

func TestReadFileNeverUsesCheckoutState(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	before, err := ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	after, err := ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("file identity changed without executable change: before=%+v after=%+v", before, after)
	}
}

func TestParseLinkedSourceIdentity(t *testing.T) {
	marker := linkedSourcePrefix + testRevision + linkedSourceSuffix
	got, err := parseLinkedSourceIdentity(marker)
	if err != nil || got != "git:"+testRevision {
		t.Fatalf("parse marker = %q, %v", got, err)
	}
	for _, marker := range []string{"", "unbound", linkedSourcePrefix + "xyz" + linkedSourceSuffix} {
		if _, err := parseLinkedSourceIdentity(marker); err == nil {
			t.Fatalf("parse marker %q succeeded", marker)
		}
	}
}
