package cli

import "testing"

func TestResolveVerbPath(t *testing.T) {
	root := NewRootCmd()
	for _, tc := range []struct {
		name string
		args []string
		want string
		ok   bool
	}{
		{"canonical nested", []string{"job", "merge", "squ-1"}, "job.merge", true},
		{"leaf with positional", []string{"run", "worker"}, "run", true},
		{"send with positionals", []string{"send", "manager", "hello"}, "send", true},
		{"leading repo flag", []string{"--repo", "/x", "job", "show", "squ-1"}, "job.show", true},
		{"leading repo flag eq", []string{"--repo=/x", "job", "show"}, "job.show", true},
		// Aliases must resolve to their canonical verb — the round-6 finding.
		{"alias ls", []string{"ls"}, "ps", true},
		{"alias top", []string{"top"}, "stats", true},
		{"alias up", []string{"up"}, "start", true},
		{"alias down", []string{"down"}, "stop", true},
		// Unknown verbs do not resolve.
		{"unknown top-level", []string{"future-dangerous-verb"}, "", false},
		{"unknown with positional", []string{"future-dangerous-verb", "worker"}, "", false},
		{"unknown subverb under group is denied", []string{"job", "future-dangerous-verb"}, "", false},
		{"inbox check resolves", []string{"inbox", "check", "--self"}, "inbox.check", true},
		{"inbox ack self resolves", []string{"inbox", "ack", "msg-1"}, "inbox.ack", true},
		{"inbox send resolves", []string{"inbox", "send", "manager", "hello"}, "inbox.send", true},
		{"real inbox subcommand resolves", []string{"inbox", "ls"}, "inbox.ls", true},
		{"unknown token under inbox group denied", []string{"inbox", "future-dangerous-verb"}, "", false},
		{"empty", nil, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := resolveVerbPath(root, tc.args)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("resolveVerbPath(%v) = (%q,%v), want (%q,%v)", tc.args, got, ok, tc.want, tc.ok)
			}
		})
	}
}
