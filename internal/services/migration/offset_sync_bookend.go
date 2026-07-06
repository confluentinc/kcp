package migration

import (
	"context"
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
	restoreOffsetSync(cl, clCfg, config, persist, "Migration completed but")
}

// restoreOffsetSync is the shared restore engine behind the post-switchover
// bookend (RestoreOffsetSync) and the abort_fence rollback
// (MigrationActions.restoreOffsetSyncAfterRollback). The situation prefix
// keeps the operator-facing remediation wording honest about which flow the
// restore failed in ("Migration completed but" vs "Gateway unfenced but").
func restoreOffsetSync(
	cl clusterlink.Service,
	clCfg clusterlink.Config,
	config *MigrationConfig,
	persist func() error,
	situation string,
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
				"%s failed to read current configs on cluster link %q for restore (%v).\n   The cluster link may still be in the paused state — re-apply %s=true and any %s* configs manually before resuming normal operation.",
				situation,
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
				"%s failed to restore %s* configs on cluster link %q (%v).\n   Applied: %s.\n   Still owed: %s — re-apply manually before resuming normal operation.",
				situation,
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
// The wording is shaped by the persisted state. The fenced CR applied at
// EventFence stays live until SwitchGateway replaces it (or the abort_fence
// rollback restores the initial CR), so at fenced, offset_sync_paused,
// fence_verified and promoted the gateway is still blocking client traffic —
// all four deserve urgent copy describing the observable state. The
// remediation differs by shape:
//   - fenced / offset_sync_paused: the same signature also arises from
//     routine resumable stops (ctx-cancel mid-detection, a verify fetch
//     error), so the copy deliberately does not claim a rollback failed; a
//     re-run retries the pause or completes the rollback, and manually
//     re-applying the initial gateway CR is a safe abort.
//   - fence_verified: topics are not yet promoted, so the manual abort is
//     still safe; a re-run re-asserts the fence and resumes forward.
//   - promoted: topics are already promoted, so routing clients back to the
//     source would diverge data — completing the switchover is the only way
//     forward that unblocks clients, and the copy explicitly warns against
//     re-applying the initial CR.
//
// Every other state keeps the softer restore-owed wording: at initialized and
// lags_ok the fence is not up (a completed rollback whose restore is still
// owed, or the legacy pre-FSM-pause cohort failing before the fence), and at
// switched the switchover CR is live.
//
// Soft-fail: never returns an error — this is best-effort messaging on top of
// the underlying execute error.
func WarnIfPausedOnExecuteFailure(config *MigrationConfig, execErr error) {
	if !config.PauseConsumerOffsetSyncFlipped {
		return
	}
	switch config.CurrentState {
	case StateFenced, StateOffsetSyncPaused:
		newReporter().remediation(
			"Migration execute failed (%v) while the gateway is still fenced and cluster link %q has %s=false.\n   Client traffic through the gateway is blocked and consumer offsets are not syncing.\n   Re-run `kcp migration execute` to resume — it retries the pause or completes the rollback as needed.\n   If a re-run is impossible, manually re-apply the initial gateway CR and re-enable %s=true on the cluster link.",
			execErr,
			config.ClusterLinkName,
			offsetSyncEnableKey,
			offsetSyncEnableKey,
		)
	case StateFenceVerified:
		newReporter().remediation(
			"Migration execute failed (%v) while the gateway is still fenced and cluster link %q has %s=false.\n   Client traffic through the gateway is blocked and consumer offsets are not syncing.\n   Re-run `kcp migration execute` to resume — traffic stays blocked until the switchover completes.\n   If a re-run is impossible, manually re-apply the initial gateway CR and re-enable %s=true on the cluster link.",
			execErr,
			config.ClusterLinkName,
			offsetSyncEnableKey,
			offsetSyncEnableKey,
		)
	case StatePromoted:
		newReporter().remediation(
			"Migration execute failed (%v) while the gateway is still fenced and cluster link %q has %s=false.\n   Client traffic through the gateway is blocked and consumer offsets are not syncing.\n   Re-run `kcp migration execute` to complete the switchover and restore client traffic.\n   Do not re-apply the initial gateway CR: topics are already promoted, and routing clients back to the source would diverge data.",
			execErr,
			config.ClusterLinkName,
			offsetSyncEnableKey,
		)
	default:
		newReporter().remediation(
			"Migration execute failed (%v) while cluster link %q has %s=false.\n   Re-run `kcp migration execute` to resume — the bookend is idempotent and the restore will run after a successful switchover — or manually re-enable %s=true on the cluster link.",
			execErr,
			config.ClusterLinkName,
			offsetSyncEnableKey,
			offsetSyncEnableKey,
		)
	}
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
