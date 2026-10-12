package migration

import (
	"context"
	"fmt"
	"time"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
)

const offsetSyncEnableKey = "consumer.offset.sync.enable"

// offsetSyncCallTimeout bounds the single AlterConfigs PUT each offset-sync step
// makes.
const offsetSyncCallTimeout = 30 * time.Second

// setOffsetSyncBaseline applies one idempotent AlterConfigs SET of
// consumer.offset.sync.enable to config's declared baseline
// (config.ConsumerOffsetSyncBaseline: "disabled" -> "false", anything else,
// including "enabled" or unset, -> "true") and returns the value it set.
// Manifest-driven only: it never reads the live cluster link, so re-running it
// (a retry, a resume) simply re-applies the same SET. Shared by the
// restore_offset_sync step and the abort_fence rollback.
func setOffsetSyncBaseline(
	ctx context.Context,
	cl clusterlink.Service,
	clCfg clusterlink.Config,
	config *MigrationConfig,
) (string, error) {
	want := "true"
	if config.ConsumerOffsetSyncBaseline == manifest.OffsetSyncBaselineDisabled {
		want = "false"
	}

	callCtx, cancel := context.WithTimeout(ctx, offsetSyncCallTimeout)
	defer cancel()
	if err := cl.AlterConfigs(callCtx, clCfg, []clusterlink.ConfigAlteration{
		{Name: offsetSyncEnableKey, Value: want, Operation: clusterlink.OperationSet},
	}); err != nil {
		return want, fmt.Errorf("failed to set %s=%s on cluster link %q: %w", offsetSyncEnableKey, want, config.ClusterLinkName, err)
	}
	return want, nil
}

// WarnIfPausedOnExecuteFailure prints a stderr remediation message when
// orchestrator.Execute returns an error and the operator opted into
// offset-sync pausing (config.PauseConsumerOffsetSync). The pause and restore
// steps are idempotent applies with nothing to branch on, so the guidance
// is a single generic reminder gated only on the manifest-declared intent:
// verify the cluster link matches the declared baseline before resuming normal
// operation.
//
// Soft-fail: never returns an error — this is best-effort messaging on top of
// the underlying execute error.
func WarnIfPausedOnExecuteFailure(config *MigrationConfig, execErr error) {
	if !config.PauseConsumerOffsetSync {
		return
	}
	newReporter().Remediation(
		"Migration execute failed (%v).\n   If offset-sync pause was applied on cluster link %q, verify %s matches your declared baseline (spec.clusterLink.consumerOffsetSyncBaseline) before resuming normal operation.\n   Re-run `kcp migration execute` to resume — the pause and restore steps are idempotent — or re-apply manually.",
		execErr,
		config.ClusterLinkName,
		offsetSyncEnableKey,
	)
}

// BuildClusterLinkConfig assembles a clusterlink.Config from a migration
// config plus a runtime REST Authenticator, shared by the offset-sync pause,
// restore and rollback so none duplicates the field layout. auth
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
