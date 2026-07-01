package managed_connectors

import (
	"github.com/confluentinc/kcp/internal/types"
)

// ManagedConnectorsScannerOpts configures a managed-connectors scan.
type ManagedConnectorsScannerOpts struct {
	StateFile   string
	State       *types.State
	Regions     []string
	ClusterArns []string
}

// ManagedConnectorsScanner scans MSK-managed (MSK Connect) connectors for
// clusters already present in the state file. Fleshed out in Tasks 2–3.
type ManagedConnectorsScanner struct {
	stateFile   string
	state       *types.State
	regions     []string
	clusterArns []string
}

func NewManagedConnectorsScanner(opts ManagedConnectorsScannerOpts) *ManagedConnectorsScanner {
	return &ManagedConnectorsScanner{
		stateFile:   opts.StateFile,
		state:       opts.State,
		regions:     opts.Regions,
		clusterArns: opts.ClusterArns,
	}
}

// Run is a temporary no-op stub so the package compiles and the opts-parsing
// tests in this task can pass. Replaced with the real implementation in Task 3.
func (s *ManagedConnectorsScanner) Run() error { return nil }
