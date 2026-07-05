package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/agent-team-project/agent-team/internal/daemon"
)

type fakeReloadClient struct {
	topology     *topologyResponse
	reconcile    *daemonReconcileResponse
	topologyErr  error
	reconcileErr error
}

func (f *fakeReloadClient) TopologyReload() (*topologyResponse, error) {
	if f.topologyErr != nil {
		return nil, f.topologyErr
	}
	if f.topology != nil {
		return f.topology, nil
	}
	return &topologyResponse{}, nil
}

func (f *fakeReloadClient) Reconcile() (*daemonReconcileResponse, error) {
	if f.reconcileErr != nil {
		return nil, f.reconcileErr
	}
	if f.reconcile != nil {
		return f.reconcile, nil
	}
	return &daemonReconcileResponse{}, nil
}

func TestRunReloadWithClientText(t *testing.T) {
	client := &fakeReloadClient{
		topology: &topologyResponse{
			Instances: []topologyInstance{{Name: "manager"}, {Name: "ticket-manager"}},
		},
		reconcile: &daemonReconcileResponse{
			Changed: 1,
			Instances: []*daemon.Metadata{
				{Instance: "manager", Status: daemon.StatusExited},
			},
			Changes: []daemonReconcileChange{
				{Instance: "manager", Agent: "manager", Before: daemon.StatusRunning, After: daemon.StatusExited, PID: 999999},
			},
		},
	}
	out := &bytes.Buffer{}
	if err := runReloadWithClient(out, client, false, nil); err != nil {
		t.Fatalf("runReloadWithClient: %v", err)
	}
	body := out.String()
	for _, want := range []string{"topology: reloaded 2 declared instance(s)", "reconciled 1 instances (1 changed)", "running -> exited"} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q:\n%s", want, body)
		}
	}
}

func TestRunReloadWithClientJSON(t *testing.T) {
	client := &fakeReloadClient{
		topology: &topologyResponse{
			Instances: []topologyInstance{{Name: "manager"}},
		},
		reconcile: &daemonReconcileResponse{Reconciled: true},
	}
	out := &bytes.Buffer{}
	if err := runReloadWithClient(out, client, true, nil); err != nil {
		t.Fatalf("runReloadWithClient: %v", err)
	}
	var body reloadJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode reload json: %v\nbody=%s", err, out.String())
	}
	if body.Topology == nil || len(body.Topology.Instances) != 1 || body.Topology.Instances[0].Name != "manager" {
		t.Fatalf("topology = %+v, want manager instance", body.Topology)
	}
	if body.Reconcile == nil || !body.Reconcile.Reconciled {
		t.Fatalf("reconcile = %+v, want reconciled true", body.Reconcile)
	}
}

func TestRunReloadWithClientErrors(t *testing.T) {
	topoErr := errors.New("topology boom")
	if err := runReloadWithClient(&bytes.Buffer{}, &fakeReloadClient{topologyErr: topoErr}, false, nil); !errors.Is(err, topoErr) {
		t.Fatalf("err = %v, want topology error", err)
	}
	reconcileErr := errors.New("reconcile boom")
	if err := runReloadWithClient(&bytes.Buffer{}, &fakeReloadClient{reconcileErr: reconcileErr}, false, nil); !errors.Is(err, reconcileErr) {
		t.Fatalf("err = %v, want reconcile error", err)
	}
}

func TestRunReloadWithClientFormat(t *testing.T) {
	client := &fakeReloadClient{
		topology: &topologyResponse{
			Instances: []topologyInstance{{Name: "manager"}, {Name: "ticket-manager"}},
		},
		reconcile: &daemonReconcileResponse{
			Reconciled: true,
			Changed:    1,
			Instances: []*daemon.Metadata{
				{Instance: "manager", Status: daemon.StatusExited},
			},
		},
	}
	tmpl, err := parseReloadFormat("{{len .Topology.Instances}}:{{.Reconcile.Reconciled}}:{{.Reconcile.Changed}}:{{len .Reconcile.Instances}}")
	if err != nil {
		t.Fatalf("parse reload format: %v", err)
	}
	out := &bytes.Buffer{}
	if err := runReloadWithClient(out, client, false, tmpl); err != nil {
		t.Fatalf("runReloadWithClient format: %v", err)
	}
	if got, want := out.String(), "2:true:1:1\n"; got != want {
		t.Fatalf("reload format output = %q, want %q", got, want)
	}
}

func TestReloadFormatRejectsConflictingModes(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"reload", "--format", "{{.Reconcile.Changed}}", "--json"}, "--format cannot be combined with --json"},
		{[]string{"reload", "--format", "{{"}, "invalid --format template"},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
	}
}

func TestReloadRequiresRunningDaemon(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"reload", "--target", tmp})
	err := cmd.Execute()
	var code ExitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if !strings.Contains(stderr.String(), "daemon is not running") {
		t.Fatalf("stderr = %q, want daemon not running", stderr.String())
	}
}
