package cli

import (
	"strings"

	"github.com/agent-team-project/agent-team/internal/topology"
)

func declaredEphemeralOwner(topo *topology.Topology, instance, agent string) (*topology.Instance, bool) {
	if topo == nil || instance == "" {
		return nil, false
	}
	var owner *topology.Instance
	for _, inst := range topo.Instances {
		if inst == nil || !inst.Ephemeral {
			continue
		}
		if agent != "" && inst.Agent != "" && agent != inst.Agent {
			continue
		}
		if instance != inst.Name && !strings.HasPrefix(instance, inst.Name+"-") {
			continue
		}
		if owner == nil || len(inst.Name) > len(owner.Name) {
			owner = inst
		}
	}
	return owner, owner != nil
}
