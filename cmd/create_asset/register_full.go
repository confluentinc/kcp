//go:build !gov

package create_asset

// prod-only create-asset subcommands. The `gov` edition (kcp-lite) cannot honor
// what target-infra, migration-infra, and migrate-connectors generate, so this
// file is excluded under `-tags=gov`. Because Go compiles only imported
// packages, excluding these blank imports leaves the three packages unreferenced
// and therefore absent from the gov binary — not merely unregistered.
import (
	_ "github.com/confluentinc/kcp/cmd/create_asset/migrate_connectors"
	_ "github.com/confluentinc/kcp/cmd/create_asset/migration_infra"
	_ "github.com/confluentinc/kcp/cmd/create_asset/target_infra"
)
