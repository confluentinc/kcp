package migration

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/fatih/color"
)

const offsetSyncEnableKey = "consumer.offset.sync.enable"

// DisableOffsetSync runs the pre-execute disable bookend for the
// --pause-consumer-offset-sync flow.
//
// No-op cases (return nil without contacting the cluster link):
//   - intent flag not set
//   - migration is already at StateSwitched (re-run after success)
//   - PauseConsumerOffsetSyncFlipped is already true (resume after partial failure)
//
// Active path: re-queries the cluster link for drift, calls AlterConfigs to
// set consumer.offset.sync.enable=false, sets PauseConsumerOffsetSyncFlipped,
// and persists the marker before returning.
//
// A non-nil error stops execution before the FSM runs (R12).
func DisableOffsetSync(
	ctx context.Context,
	cl clusterlink.Service,
	clCfg clusterlink.Config,
	config *types.MigrationConfig,
	persist func() error,
) error {
	if !config.PauseConsumerOffsetSync {
		return nil
	}
	if config.CurrentState == types.StateSwitched {
		slog.Debug("disable bookend skipped: migration already switched", "migrationId", config.MigrationId)
		return nil
	}
	if config.PauseConsumerOffsetSyncFlipped {
		slog.Info("resume: consumer.offset.sync.enable already flipped, skipping disable", "migrationId", config.MigrationId)
		return nil
	}

	fmt.Printf("\n%s\n", color.CyanString("⏸  Pausing consumer.offset.sync on cluster link..."))

	currentConfigs, err := cl.ListConfigs(ctx, clCfg)
	if err != nil {
		return fmt.Errorf("failed to query cluster link %q for drift detection: %w", config.ClusterLinkName, err)
	}
	observed, present := currentConfigs[offsetSyncEnableKey]
	switch {
	case !present:
		return fmt.Errorf("cluster link %q drift detected: no %s key found (expected %q) — refusing to flip", config.ClusterLinkName, offsetSyncEnableKey, "true")
	case observed != "true":
		return fmt.Errorf("cluster link %q drift detected: %s=%q, expected %q — refusing to flip", config.ClusterLinkName, offsetSyncEnableKey, observed, "true")
	}

	if err := cl.AlterConfigs(ctx, clCfg, []clusterlink.ConfigAlteration{
		{Name: offsetSyncEnableKey, Value: "false", Operation: clusterlink.OperationSet},
	}); err != nil {
		return fmt.Errorf("failed to disable %s on cluster link %q: %w", offsetSyncEnableKey, config.ClusterLinkName, err)
	}

	config.PauseConsumerOffsetSyncFlipped = true
	if err := persist(); err != nil {
		return fmt.Errorf("disabled %s on cluster link %q but failed to persist marker: %w (recovery: re-enable on the cluster link or correct the migration state file before re-running)", offsetSyncEnableKey, config.ClusterLinkName, err)
	}

	fmt.Printf("   %s %s set to false on cluster link %s\n", color.GreenString("✔"), offsetSyncEnableKey, config.ClusterLinkName)
	return nil
}

// RestoreOffsetSync runs the post-execute restore bookend. Soft-failure
// semantics: AlterConfigs failure prints a remediation message to stderr but
// does NOT propagate an error, because the switchover itself succeeded (R13).
// The PauseConsumerOffsetSyncFlipped marker stays true on failure so the
// state file records that a restore is still owed.
func RestoreOffsetSync(
	ctx context.Context,
	cl clusterlink.Service,
	clCfg clusterlink.Config,
	config *types.MigrationConfig,
	persist func() error,
) {
	if !config.PauseConsumerOffsetSyncFlipped {
		return
	}

	fmt.Printf("\n%s\n", color.CyanString("▶️  Restoring consumer.offset.sync on cluster link..."))

	if err := cl.AlterConfigs(ctx, clCfg, []clusterlink.ConfigAlteration{
		{Name: offsetSyncEnableKey, Value: "true", Operation: clusterlink.OperationSet},
	}); err != nil {
		fmt.Fprintf(os.Stderr,
			"%s Migration completed but failed to restore %s on cluster link %q (%v).\n   Run: confluent kafka link configuration update --link %s --cluster %s %s=true\n",
			color.YellowString("⚠️"),
			offsetSyncEnableKey,
			config.ClusterLinkName,
			err,
			config.ClusterLinkName,
			config.ClusterId,
			offsetSyncEnableKey,
		)
		return
	}

	config.PauseConsumerOffsetSyncFlipped = false
	if err := persist(); err != nil {
		slog.Warn("restored consumer.offset.sync.enable but failed to clear state file marker", "err", err)
	}
	fmt.Printf("   %s %s restored to true on cluster link %s\n", color.GreenString("✔"), offsetSyncEnableKey, config.ClusterLinkName)
}

// BuildClusterLinkConfig assembles a clusterlink.Config from a migration
// config plus runtime API credentials. Centralized here so the bookend
// callers in cmd/migration/execute don't duplicate the field layout.
func BuildClusterLinkConfig(config *types.MigrationConfig, apiKey, apiSecret string) clusterlink.Config {
	return clusterlink.Config{
		RestEndpoint: config.ClusterRestEndpoint,
		ClusterID:    config.ClusterId,
		LinkName:     config.ClusterLinkName,
		APIKey:       apiKey,
		APISecret:    apiSecret,
		Topics:       config.Topics,
	}
}
