package shimexec

import "github.com/gluk-w/claworc/control-plane/internal/agentshim"

func init() {
	agentshim.RegisterAdapter(agentshim.ShimAdapterType, func(deps agentshim.InstanceDeps) agentshim.Client {
		return NewFromSSH(deps.SSHClient)
	})
}
