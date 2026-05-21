package migration

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/fatih/color"
)

const (
	offsetSyncEnableKey  = "consumer.offset.sync.enable"
	consumerOffsetPrefix = "consumer.offset."
)

// restoreTimeout caps the post-switchover restore call so a hung REST endpoint
// can't keep the operator's terminal stuck after a successful migration.
const restoreTimeout = 30 * time.Second

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
// semantics: ListConfigs or AlterConfigs failure prints a remediation message
// to stderr but does NOT propagate an error, because the switchover itself
// succeeded (R13). The PauseConsumerOffsetSyncFlipped marker stays true on
// failure so the state file records that a restore is still owed.
//
// Diff-based restore: when MigrationConfig.ClusterLinkConfigs holds an init
// snapshot, restore queries the cluster link for its current consumer.offset.*
// state and re-applies snapshot values for keys that the disable bookend
// cleared (current is missing, empty, or "false"). Keys whose current value
// looks like a deliberate post-disable operator change (non-empty,
// non-"false", differs from snapshot) are left alone. When the snapshot is
// empty (defensive fallback for unforeseen state-file lifecycles), restore
// performs a single SET consumer.offset.sync.enable=true.
//
// The ctx argument is accepted for symmetry with DisableOffsetSync but the
// network calls use a fresh background ctx with a bounded timeout. The
// restore must run even when the parent ctx is already cancelled (e.g. a
// signal arrived between orchestrator.Execute returning and the bookend
// firing) — that is the case the soft-fail semantic exists for.
func RestoreOffsetSync(
	_ context.Context,
	cl clusterlink.Service,
	clCfg clusterlink.Config,
	config *types.MigrationConfig,
	persist func() error,
) {
	if !config.PauseConsumerOffsetSyncFlipped {
		return
	}

	fmt.Printf("\n%s\n", color.CyanString("▶️  Restoring consumer.offset.sync on cluster link..."))

	restoreCtx, cancel := context.WithTimeout(context.Background(), restoreTimeout)
	defer cancel()

	var alterations []clusterlink.ConfigAlteration

	if len(config.ClusterLinkConfigs) == 0 {
		// Legacy fallback: no init snapshot to diff against, fall back to the
		// single-key SET that earlier kcp versions used.
		alterations = []clusterlink.ConfigAlteration{
			{Name: offsetSyncEnableKey, Value: "true", Operation: clusterlink.OperationSet},
		}
	} else {
		currentConfigs, err := cl.ListConfigs(restoreCtx, clCfg)
		if err != nil {
			fmt.Fprintf(os.Stderr,
				"%s Migration completed but failed to read current configs on cluster link %q for restore (%v).\n   The cluster link may still be in the paused state — re-apply %s=true and any %s* configs manually before resuming normal operation.\n",
				color.YellowString("⚠️"),
				config.ClusterLinkName,
				err,
				offsetSyncEnableKey,
				consumerOffsetPrefix,
			)
			return
		}

		// Restore keys the disable bookend cleared. "false" is treated as the
		// disabled-state marker for the toggle; any other non-empty, differing
		// current value is taken as a deliberate post-disable operator change
		// and left alone.
		var keys []string
		for k, snapVal := range config.ClusterLinkConfigs {
			if !strings.HasPrefix(k, consumerOffsetPrefix) {
				continue
			}
			if snapVal == "" {
				continue
			}
			curVal, present := currentConfigs[k]
			if present && curVal == snapVal {
				continue
			}
			if present && curVal != "" && curVal != "false" {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			alterations = append(alterations, clusterlink.ConfigAlteration{
				Name:      k,
				Value:     config.ClusterLinkConfigs[k],
				Operation: clusterlink.OperationSet,
			})
		}
	}

	if len(alterations) == 0 {
		// Snapshot matched current state for every consumer.offset.* key —
		// nothing to restore. Still clear the marker so the state file
		// reflects that the bookend cycle is complete.
		config.PauseConsumerOffsetSyncFlipped = false
		if err := persist(); err != nil {
			slog.Warn("cleared restore marker but failed to persist state file", "err", err)
		}
		fmt.Printf("   %s %s* configs already match init snapshot on cluster link %s\n", color.GreenString("✔"), consumerOffsetPrefix, config.ClusterLinkName)
		return
	}

	if err := cl.AlterConfigs(restoreCtx, clCfg, alterations); err != nil {
		attempted := make([]string, len(alterations))
		for i, a := range alterations {
			attempted[i] = a.Name
		}
		fmt.Fprintf(os.Stderr,
			"%s Migration completed but failed to restore %s* configs on cluster link %q (%v).\n   The cluster link may still be in the paused state — re-apply the following configs manually before resuming normal operation: %s\n",
			color.YellowString("⚠️"),
			consumerOffsetPrefix,
			config.ClusterLinkName,
			err,
			strings.Join(attempted, ", "),
		)
		return
	}

	config.PauseConsumerOffsetSyncFlipped = false
	if err := persist(); err != nil {
		slog.Warn("restored consumer.offset configs but failed to clear state file marker", "err", err)
	}
	names := make([]string, len(alterations))
	for i, a := range alterations {
		names[i] = a.Name
	}
	fmt.Printf("   %s restored %s* configs on cluster link %s: %s\n", color.GreenString("✔"), consumerOffsetPrefix, config.ClusterLinkName, strings.Join(names, ", "))
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
