package migration

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
)

const (
	offsetSyncEnableKey  = "consumer.offset.sync.enable"
	consumerOffsetPrefix = "consumer.offset."
)

// bookendCallTimeout bounds each individual REST call the disable/restore
// bookends make (one ListConfigs, one PUT per alteration). Applied per-call
// rather than per-batch so a slow REST endpoint on one PUT does not starve
// the rest of the restore.
const bookendCallTimeout = 30 * time.Second

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
	config *MigrationConfig,
	persist func() error,
) error {
	if !config.PauseConsumerOffsetSync {
		return nil
	}
	if config.CurrentState == StateSwitched {
		slog.Debug("disable bookend skipped: migration already switched", "migrationId", config.MigrationId)
		return nil
	}
	if config.PauseConsumerOffsetSyncFlipped {
		slog.Info("resume: consumer.offset.sync.enable already flipped, skipping disable", "migrationId", config.MigrationId)
		return nil
	}

	r := newReporter()
	r.section("⏸  Pausing consumer.offset.sync on cluster link...")

	// Per-call deadlines derived from the parent ctx so signal cancellation
	// still propagates, but a hung REST endpoint cannot block indefinitely.
	listCtx, listCancel := context.WithTimeout(ctx, bookendCallTimeout)
	currentConfigs, err := cl.ListConfigs(listCtx, clCfg)
	listCancel()
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

	alterCtx, alterCancel := context.WithTimeout(ctx, bookendCallTimeout)
	err = cl.AlterConfigs(alterCtx, clCfg, []clusterlink.ConfigAlteration{
		{Name: offsetSyncEnableKey, Value: "false", Operation: clusterlink.OperationSet},
	})
	alterCancel()
	if err != nil {
		return fmt.Errorf("failed to disable %s on cluster link %q: %w", offsetSyncEnableKey, config.ClusterLinkName, err)
	}

	config.PauseConsumerOffsetSyncFlipped = true
	if err := persist(); err != nil {
		return fmt.Errorf("disabled %s on cluster link %q but failed to persist marker: %w (recovery: re-enable on the cluster link or correct the migration state file before re-running)", offsetSyncEnableKey, config.ClusterLinkName, err)
	}

	r.success("%s set to false on cluster link %s", offsetSyncEnableKey, config.ClusterLinkName)
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
// network calls use a fresh background ctx with a per-call timeout. The
// restore must run even when the parent ctx is already cancelled (e.g. a
// signal arrived between orchestrator.Execute returning and the bookend
// firing) — that is the case the soft-fail semantic exists for. Each PUT
// gets its own timeout budget so a slow endpoint on one key cannot starve
// the rest of the restore.
func RestoreOffsetSync(
	_ context.Context,
	cl clusterlink.Service,
	clCfg clusterlink.Config,
	config *MigrationConfig,
	persist func() error,
) {
	if !config.PauseConsumerOffsetSyncFlipped {
		return
	}

	r := newReporter()
	r.section("▶️  Restoring consumer.offset.sync on cluster link...")

	var alterations []clusterlink.ConfigAlteration

	if len(config.ClusterLinkConfigs) == 0 {
		// Legacy fallback: no init snapshot to diff against, fall back to the
		// single-key SET that earlier kcp versions used.
		alterations = []clusterlink.ConfigAlteration{
			{Name: offsetSyncEnableKey, Value: "true", Operation: clusterlink.OperationSet},
		}
	} else {
		listCtx, listCancel := context.WithTimeout(context.Background(), bookendCallTimeout)
		currentConfigs, err := cl.ListConfigs(listCtx, clCfg)
		listCancel()
		if err != nil {
			r.remediation(
				"Migration completed but failed to read current configs on cluster link %q for restore (%v).\n   The cluster link may still be in the paused state — re-apply %s=true and any %s* configs manually before resuming normal operation.",
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
		// AlterConfigs is non-atomic (one PUT per key, short-circuit on error).
		// Order the toggle key LAST so a partial restore failure leaves the
		// link in the safer state: filters re-applied with sync still disabled,
		// rather than sync re-enabled with stale or missing filters.
		sort.Strings(keys)
		toggleLast := make([]string, 0, len(keys))
		var toggleKey string
		for _, k := range keys {
			if k == offsetSyncEnableKey {
				toggleKey = k
				continue
			}
			toggleLast = append(toggleLast, k)
		}
		if toggleKey != "" {
			toggleLast = append(toggleLast, toggleKey)
		}
		for _, k := range toggleLast {
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
		r.success("%s* configs already match init snapshot on cluster link %s", consumerOffsetPrefix, config.ClusterLinkName)
		return
	}

	// Apply alterations one at a time so each PUT gets its own timeout budget
	// and we can report exactly which keys were applied vs. still owed on
	// partial failure. Toggle key is ordered last (see slice build above) so
	// a mid-loop failure leaves the link in the safer state.
	for i, alt := range alterations {
		callCtx, callCancel := context.WithTimeout(context.Background(), bookendCallTimeout)
		err := cl.AlterConfigs(callCtx, clCfg, []clusterlink.ConfigAlteration{alt})
		callCancel()
		if err != nil {
			applied := make([]string, 0, i)
			for _, a := range alterations[:i] {
				applied = append(applied, a.Name)
			}
			remaining := make([]string, 0, len(alterations)-i)
			for _, a := range alterations[i:] {
				remaining = append(remaining, a.Name)
			}
			appliedStr := "none"
			if len(applied) > 0 {
				appliedStr = strings.Join(applied, ", ")
			}
			r.remediation(
				"Migration completed but failed to restore %s* configs on cluster link %q (%v).\n   Applied: %s.\n   Still owed: %s — re-apply manually before resuming normal operation.",
				consumerOffsetPrefix,
				config.ClusterLinkName,
				err,
				appliedStr,
				strings.Join(remaining, ", "),
			)
			return
		}
	}

	config.PauseConsumerOffsetSyncFlipped = false
	if err := persist(); err != nil {
		slog.Warn("restored consumer.offset configs but failed to clear state file marker", "err", err)
	}
	names := make([]string, len(alterations))
	for i, a := range alterations {
		names[i] = a.Name
	}
	r.success("restored %s* configs on cluster link %s: %s", consumerOffsetPrefix, config.ClusterLinkName, strings.Join(names, ", "))
}

// WarnIfPausedOnExecuteFailure prints a stderr remediation message when
// orchestrator.Execute returns an error while the cluster link is still in
// the disabled state (PauseConsumerOffsetSyncFlipped=true). The restore
// bookend only runs after a successful execute, so without this warning the
// operator has no signal that the cluster link is paused.
//
// Soft-fail: never returns an error — this is best-effort messaging on top of
// the underlying execute error.
func WarnIfPausedOnExecuteFailure(config *MigrationConfig, execErr error) {
	if !config.PauseConsumerOffsetSyncFlipped {
		return
	}
	newReporter().remediation(
		"Migration execute failed (%v) while cluster link %q has %s=false.\n   Re-run `kcp migration execute` to resume — the bookend is idempotent and the restore will run after a successful switchover — or manually re-enable %s=true on the cluster link.",
		execErr,
		config.ClusterLinkName,
		offsetSyncEnableKey,
		offsetSyncEnableKey,
	)
}

// BuildClusterLinkConfig assembles a clusterlink.Config from a migration
// config plus runtime API credentials. Centralized here so the bookend
// callers in cmd/migration/execute don't duplicate the field layout.
func BuildClusterLinkConfig(config *MigrationConfig, apiKey, apiSecret string) clusterlink.Config {
	return clusterlink.Config{
		RestEndpoint: config.ClusterRestEndpoint,
		ClusterID:    config.ClusterId,
		LinkName:     config.ClusterLinkName,
		APIKey:       apiKey,
		APISecret:    apiSecret,
		Topics:       config.Topics,
	}
}
