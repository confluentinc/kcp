package migration

import (
	"context"
	"time"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
)

const offsetSyncEnableKey = "consumer.offset.sync.enable"

// bookendCallTimeout bounds the single AlterConfigs PUT the pause/restore
// bookends each make.
const bookendCallTimeout = 30 * time.Second

// RestoreOffsetSync runs the post-execute restore bookend: an idempotent
// AlterConfigs SET that re-applies the operator's declared baseline
// (config.ConsumerOffsetSyncBaseline). Soft-failure semantics: an
// AlterConfigs failure prints a remediation message to stderr but does NOT
// propagate an error, because the switchover itself succeeded (R13).
//
// The ctx argument is accepted for symmetry with the pause bookend but the
// network call uses a fresh background ctx with a per-call timeout. The
// restore must run even when the parent ctx is already cancelled (e.g. a
// signal arrived between orchestrator.Execute returning and the bookend
// firing) — that is the case the soft-fail semantic exists for.
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
//
// Manifest- and plan-driven only: it never reads the live cluster link to
// decide anything — no ListConfigs, no diff, no marker. It is a no-op unless
// the operator opted into pausing (config.PauseConsumerOffsetSync), and
// otherwise always applies a single idempotent SET of
// consumer.offset.sync.enable to the declared baseline
// (config.ConsumerOffsetSyncBaseline: "disabled" -> "false", anything else,
// including "enabled" or unset, -> "true"), whether or not the pause bookend
// actually ran or landed. Re-running it (a retry, a second rollback attempt)
// simply re-applies the same SET.
func restoreOffsetSync(
	cl clusterlink.Service,
	clCfg clusterlink.Config,
	config *MigrationConfig,
	persist func() error,
	situation string,
) {
	if !config.PauseConsumerOffsetSync {
		return
	}

	want := "true"
	if config.ConsumerOffsetSyncBaseline == manifest.OffsetSyncBaselineDisabled {
		want = "false"
	}

	r := newReporter()
	r.section("▶️  Restoring consumer.offset.sync on cluster link...")

	callCtx, cancel := context.WithTimeout(context.Background(), bookendCallTimeout)
	defer cancel()
	if err := cl.AlterConfigs(callCtx, clCfg, []clusterlink.ConfigAlteration{
		{Name: offsetSyncEnableKey, Value: want, Operation: clusterlink.OperationSet},
	}); err != nil {
		r.Remediation(
			"%s failed to set %s=%s on cluster link %q (%v) — re-apply manually.",
			situation,
			offsetSyncEnableKey,
			want,
			config.ClusterLinkName,
			err,
		)
		return
	}
	r.Success("%s set to %s on cluster link %s", offsetSyncEnableKey, want, config.ClusterLinkName)
}

// WarnIfPausedOnExecuteFailure prints a stderr remediation message when
// orchestrator.Execute returns an error and the operator opted into
// offset-sync pausing (config.PauseConsumerOffsetSync). It deliberately does
// NOT shape its wording from config.CurrentState or the state-file
// PauseConsumerOffsetSyncFlipped marker — the pause/restore bookends are now
// idempotent applies with no state-file-derived signal to branch on, and both
// fields are removed once the state file itself goes (Task 2) — so the
// guidance is a single generic reminder gated only on the manifest-declared
// intent: verify the cluster link matches the declared baseline before
// resuming normal operation.
//
// Soft-fail: never returns an error — this is best-effort messaging on top of
// the underlying execute error.
func WarnIfPausedOnExecuteFailure(config *MigrationConfig, execErr error) {
	if !config.PauseConsumerOffsetSync {
		return
	}
	newReporter().Remediation(
		"Migration execute failed (%v).\n   If offset-sync pause was applied on cluster link %q, verify %s matches your declared baseline (spec.clusterLink.consumerOffsetSyncBaseline) before resuming normal operation.\n   Re-run `kcp migration execute` to retry — the pause/restore bookends are idempotent — or re-apply manually.",
		execErr,
		config.ClusterLinkName,
		offsetSyncEnableKey,
	)
}

// BuildClusterLinkConfig assembles a clusterlink.Config from a migration
// config plus a runtime REST Authenticator. Centralized here so the bookend
// callers in cmd/migration/execute don't duplicate the field layout. auth
// carries whichever REST auth form the manifest resolved — basic, bearer or
// mtls — not only the api_key/api_secret pair BasicAuth wraps.
func BuildClusterLinkConfig(config *MigrationConfig, auth clusterlink.Authenticator) clusterlink.Config {
	return clusterlink.Config{
		RestEndpoint: config.ClusterRestEndpoint,
		ClusterID:    config.ClusterId,
		LinkName:     config.ClusterLinkName,
		Topics:       config.Topics,
		Auth:         auth,
	}
}
