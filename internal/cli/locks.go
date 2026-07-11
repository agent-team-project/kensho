package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"text/template"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newLocksCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "locks",
		Short: "Inspect declared dispatch lock utilization.",
		Long:  "Inspect named dispatch locks declared in .agent_team/instances.toml and their active daemon ledger holders.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team locks: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseLocksFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team locks: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			snapshots, err := collectLockSnapshots(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team locks: %v\n", err)
				return exitErr(1)
			}
			return renderLockSnapshots(cmd.OutOrStdout(), snapshots, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit lock snapshots as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each lock with a Go template, e.g. '{{.Name}} {{.Used}}/{{.Slots}}'.")
	return cmd
}

func parseLocksFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("locks").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("--format: %w", err)
	}
	return tmpl, nil
}

func collectLockSnapshots(teamDir string) ([]daemon.LockSnapshot, error) {
	if dc, err := newDaemonClient(teamDir); err == nil {
		return dc.Locks()
	} else if !errors.Is(err, errDaemonNotRunning) {
		return nil, err
	}
	return collectLocalLockSnapshots(teamDir)
}

func collectLocalLockSnapshots(teamDir string) ([]daemon.LockSnapshot, error) {
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return nil, err
	}
	if top == nil {
		return []daemon.LockSnapshot{}, nil
	}
	leases, err := daemon.ListLockLeases(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	type lockBucket struct {
		Name    string
		Storage string
		Scope   string
		Team    string
		Job     string
		Holders []daemon.LockHolder
	}
	byStorage := map[string]*lockBucket{}
	for _, lease := range leases {
		if lease == nil {
			continue
		}
		declaredName := firstNonEmpty(lease.Name, lease.Lock)
		declared := top.Locks[declaredName]
		if declared == nil {
			continue
		}
		pid := liveLockLeasePID(teamDir, lease)
		if pid <= 0 {
			continue
		}
		storage := firstNonEmpty(lease.Lock, topology.ScopedResourceName(declaredName, declared.Scope, lease.Origin.Team, lease.Origin.Job))
		bucket := byStorage[storage]
		if bucket == nil {
			bucket = &lockBucket{
				Name:    declaredName,
				Storage: storage,
				Scope:   firstNonEmpty(lease.Scope, declared.Scope),
				Team:    lease.Origin.Team,
				Job:     lease.Origin.Job,
			}
			byStorage[storage] = bucket
		}
		bucket.Holders = append(bucket.Holders, daemon.LockHolder{
			Instance:   lease.Instance,
			PID:        pid,
			AcquiredAt: lease.AcquiredAt,
			UpdatedAt:  lease.UpdatedAt,
		})
	}
	out := make([]daemon.LockSnapshot, 0, len(top.Locks)+len(byStorage))
	seenDeclared := map[string]bool{}
	storageNames := make([]string, 0, len(byStorage))
	for storage := range byStorage {
		storageNames = append(storageNames, storage)
	}
	sort.Strings(storageNames)
	for _, storage := range storageNames {
		bucket := byStorage[storage]
		declared := top.Locks[bucket.Name]
		if declared == nil {
			continue
		}
		holders := bucket.Holders
		sort.Slice(holders, func(i, j int) bool { return holders[i].Instance < holders[j].Instance })
		available := declared.Slots - len(holders)
		if available < 0 {
			available = 0
		}
		out = append(out, daemon.LockSnapshot{
			Name:      bucket.Name,
			Storage:   bucket.Storage,
			Scope:     bucket.Scope,
			Team:      bucket.Team,
			Job:       bucket.Job,
			Slots:     declared.Slots,
			Used:      len(holders),
			Available: available,
			Holders:   holders,
		})
		seenDeclared[bucket.Name] = true
	}
	for _, lock := range top.SortedLocks() {
		if seenDeclared[lock.Name] {
			continue
		}
		out = append(out, daemon.LockSnapshot{
			Name:      lock.Name,
			Storage:   topology.ScopedResourceName(lock.Name, lock.Scope, "", ""),
			Scope:     lock.Scope,
			Slots:     lock.Slots,
			Available: lock.Slots,
			Holders:   []daemon.LockHolder{},
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].Storage < out[j].Storage
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func liveLockLeasePID(teamDir string, lease *daemon.LockLease) int {
	if lease == nil {
		return 0
	}
	if lease.PID > 0 && daemon.PidLiveCheck(lease.PID) {
		return lease.PID
	}
	meta, err := daemon.ReadMetadata(daemon.DaemonRoot(teamDir), lease.Instance)
	if err != nil || meta == nil || meta.Status != daemon.StatusRunning || meta.PID <= 0 {
		return 0
	}
	if !daemon.PidLiveCheck(meta.PID) {
		return 0
	}
	return meta.PID
}

func renderLockSnapshots(w io.Writer, snapshots []daemon.LockSnapshot, jsonOut bool, tmpl *template.Template) error {
	if snapshots == nil {
		snapshots = []daemon.LockSnapshot{}
	}
	if jsonOut {
		return json.NewEncoder(w).Encode(snapshots)
	}
	if tmpl != nil {
		for _, snapshot := range snapshots {
			if err := tmpl.Execute(w, snapshot); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	if len(snapshots) == 0 {
		fmt.Fprintln(w, "(no locks)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSLOTS\tUSED\tAVAILABLE\tHOLDERS")
	for _, snapshot := range snapshots {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%s\n",
			snapshot.Name, snapshot.Slots, snapshot.Used, snapshot.Available, lockHolderList(snapshot.Holders))
	}
	return tw.Flush()
}

func lockHolderList(holders []daemon.LockHolder) string {
	if len(holders) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(holders))
	for _, holder := range holders {
		if holder.PID > 0 {
			parts = append(parts, fmt.Sprintf("%s(pid=%d)", holder.Instance, holder.PID))
			continue
		}
		parts = append(parts, holder.Instance)
	}
	return strings.Join(parts, ",")
}
