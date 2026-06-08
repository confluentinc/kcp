package types

import (
	"fmt"
	"strconv"
)

// ----- migration infrastructure type -----

type MigrationType int

const (
	PublicMskEndpoints                   MigrationType = 1
	ExternalOutboundClusterLink          MigrationType = 2
	ExternalOutboundClusterLinkPlaintext MigrationType = 3
	JumpClusterSaslScram                 MigrationType = 4
	JumpClusterIam                       MigrationType = 5
)

func (m MigrationType) IsValid() bool {
	switch m {
	case PublicMskEndpoints, ExternalOutboundClusterLink, ExternalOutboundClusterLinkPlaintext, JumpClusterSaslScram, JumpClusterIam:
		return true
	default:
		return false
	}
}

func (m MigrationType) RequiresSaslScram() bool {
	switch m {
	case PublicMskEndpoints, ExternalOutboundClusterLink, JumpClusterSaslScram:
		return true
	default:
		return false
	}
}

func ToMigrationType(input string) (MigrationType, error) {
	value, err := strconv.Atoi(input)
	if err != nil {
		return 0, fmt.Errorf("invalid input: must be a number")
	}
	m := MigrationType(value)
	if !m.IsValid() {
		return 0, fmt.Errorf("invalid MigrationType value: %d", value)
	}
	return m, nil
}

type Manifest struct {
	MigrationInfraType MigrationType `json:"migration_infra_type"`
}
