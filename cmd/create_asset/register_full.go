//go:build !gov

package create_asset

// prod-only create-asset subcommands. The `gov` edition (kcp-lite) cannot honor
// what target-infra, migration-infra, and migrate-connectors generate, so this
// file is excluded under `-tags=gov`. Because Go compiles only imported
// packages, excluding these blank imports leaves the three packages unreferenced
// and therefore absent from the gov binary — not merely unregistered.
//
// This is the source of truth for the gov-excluded set. Two downstream copies
// must stay in lockstep if it ever changes:
//   - cmd/create_asset/cmd_create_asset_gov_test.go (asserts the excluded set)
//   - cmd/ui/frontend/src/components/GovBanner.tsx (lists them in the banner)
import (
	_ "github.com/confluentinc/kcp/cmd/create_asset/migrate_connectors"
	_ "github.com/confluentinc/kcp/cmd/create_asset/migration_infra"
	_ "github.com/confluentinc/kcp/cmd/create_asset/target_infra"
)
