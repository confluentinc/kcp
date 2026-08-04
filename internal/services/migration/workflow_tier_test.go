package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// ===========================================================================
// FenceGateway tier-selected verification
//
// The defect these cover: with spec.hotReload.enabled the config applies in
// place, so WaitForGatewayReady reports "no pod restart required" and passes
// without the fence reaching a single pod. Verification must therefore be
// chosen by tier, and the tier must decide whether kcp writes a configId at
// all — stamping one on a non-hot-reload gateway rolls every pod.
// ===========================================================================

// testFencedCR is a parseable fenced CR, unlike the []byte("fenced") sentinel
// the pre-tier tests use: Tier A rewrites this document, so it has to have a
// spec to rewrite.
const testFencedCR = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
spec:
  replicas: 3
  routes:
    - name: route-1
      streamingDomain: sd-1
      fence:
        scope: ALL
        errorCode: BROKER_NOT_AVAILABLE
`

const testInitialCR = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
spec:
  replicas: 3
  hotReload:
    enabled: true
  routes:
    - name: route-1
      streamingDomain: sd-1
`

const testSwitchoverCR = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
spec:
  replicas: 3
  streamingDomains:
    - name: sd-cc
      bootstrapServers: pkc-abcde.us-east-1.aws.confluent.cloud:9092
  routes:
    - name: route-1
      streamingDomain: sd-cc
`

// testFetchedInitialCR is the initial CR as it comes back from the cluster —
// carrying the server-managed metadata that server-side apply refuses.
const testFetchedInitialCR = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: gw-1
  namespace: ns
  generation: 34
  resourceVersion: "1418956"
  uid: 3f0cabc1-0000-0000-0000-000000000000
  creationTimestamp: "2026-07-28T14:49:30Z"
  managedFields:
    - manager: kcp-migration
      operation: Apply
spec:
  replicas: 3
  hotReload:
    enabled: true
  routes:
    - name: route-1
      streamingDomain: sd-1
status:
  conditions:
    - type: platform.confluent.io/cluster-ready
      status: "True"
`

// fenceProbe records what FenceGateway did, so a test can assert on the
// sequence and on the exact bytes and arguments handed to each call.
type fenceProbe struct {
	calls []string

	appliedYAML   []byte
	waitConfigIDs []string

	detectInitialYAML   []byte
	detectCandidateYAML []byte

	waitConfigID   string
	waitSnapshot   gateway.ConditionSnapshot
	waitPort       string
	waitPoll       time.Duration
	waitTimeout    time.Duration
	waitPodUIDs    map[k8stypes.UID]struct{}
	waitPodsCalls  int
	readyWaitCalls int
}

// newTierMock wires a gateway mock that records into probe and reports the
// given tier. Every method is populated so an unexpected call is visible in the
// recorded sequence rather than silently defaulted.
func newTierMock(probe *fenceProbe, tier gateway.VerificationTier, tierErr error) *mockGatewayService {
	return &mockGatewayService{
		detectGatewayVerificationTierFn: func(_ context.Context, _, _ string, initialYAML, candidateYAML []byte) (gateway.VerificationTier, error) {
			probe.calls = append(probe.calls, "detect")
			probe.detectInitialYAML = initialYAML
			probe.detectCandidateYAML = candidateYAML
			return tier, tierErr
		},
		snapshotGatewayConditionsFn: func(_ context.Context, _, _ string) (gateway.ConditionSnapshot, error) {
			probe.calls = append(probe.calls, "snapshot")
			return gateway.ConditionSnapshot{"platform.confluent.io/cluster-ready": {}}, nil
		},
		getGatewayPodUIDsFn: func(_ context.Context, _, _ string) (map[k8stypes.UID]struct{}, error) {
			probe.calls = append(probe.calls, "getUIDs")
			return map[k8stypes.UID]struct{}{"old-pod": {}}, nil
		},
		applyGatewayYAMLFn: func(_ context.Context, _, _ string, y []byte) error {
			probe.calls = append(probe.calls, "apply")
			probe.appliedYAML = y
			return nil
		},
		waitForGatewayConfigAppliedFn: func(_ context.Context, _, _, targetConfigID string, before gateway.ConditionSnapshot, port string, poll, timeout time.Duration, onProgress func(gateway.ConfigApplyProgress)) error {
			probe.calls = append(probe.calls, "waitConfig")
			probe.waitConfigID = targetConfigID
			probe.waitConfigIDs = append(probe.waitConfigIDs, targetConfigID)
			probe.waitSnapshot = before
			probe.waitPort = port
			probe.waitPoll = poll
			probe.waitTimeout = timeout
			if onProgress != nil {
				onProgress(gateway.ConfigApplyProgress{PodsApplied: 2, PodsTotal: 3, Reason: "2 of 3 gateway pods have applied the new config"})
				onProgress(gateway.ConfigApplyProgress{PodsApplied: 3, PodsTotal: 3, Converged: true})
			}
			return nil
		},
		waitForGatewayAcceptedFn: func(_ context.Context, _, _ string, _ gateway.ConditionSnapshot, _, _ time.Duration) error {
			probe.calls = append(probe.calls, "reconcile")
			return nil
		},
		waitForGatewayPodsFn: func(_ context.Context, _, _ string, uids map[k8stypes.UID]struct{}, _, _ time.Duration, _ func(gateway.PodRolloutProgress)) error {
			probe.calls = append(probe.calls, "waitPods")
			probe.waitPodUIDs = uids
			probe.waitPodsCalls++
			return nil
		},
		waitForGatewayReadyFn: func(_ context.Context, _, _ string, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
			probe.calls = append(probe.calls, "waitReady")
			probe.readyWaitCalls++
			return nil
		},
	}
}

func tierTestConfig() *MigrationConfig {
	return &MigrationConfig{
		K8sNamespace:  "ns",
		InitialCrName: "gw-1",
		InitialCrYAML: []byte(testInitialCR),
		FencedCrYAML:  []byte(testFencedCR),
	}
}

// specConfigID reads spec.configId out of an applied CR document.
func specConfigID(t *testing.T, crYAML []byte) (string, bool) {
	t.Helper()
	var obj map[string]any
	require.NoError(t, yaml.Unmarshal(crYAML, &obj))
	spec, ok := obj["spec"].(map[string]any)
	if !ok {
		return "", false
	}
	id, ok := spec["configId"].(string)
	return id, ok
}

// ---------------------------------------------------------------------------
// Tier A — per-pod configId verification
// ---------------------------------------------------------------------------

// The core wiring: the id stamped into the applied CR must be the id the wait
// verifies. A mismatch would poll forever for a value no pod can ever report.
func TestFenceGateway_TierA_StampedIDMatchesVerifiedID(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPerPodConfigID, nil), &mockClusterLinkService{})

	require.NoError(t, wf.FenceGateway(context.Background(), tierTestConfig()))

	stamped, found := specConfigID(t, probe.appliedYAML)
	require.True(t, found, "Tier A must stamp spec.configId on the applied CR")
	assert.Equal(t, stamped, probe.waitConfigID, "the wait must verify the id that was applied")
	assert.True(t, strings.HasPrefix(stamped, "kcp-"), "id should be attributable to kcp: %q", stamped)
}

// The condition baseline is only meaningful if it predates the apply.
func TestFenceGateway_TierA_SnapshotsConditionsBeforeApply(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPerPodConfigID, nil), &mockClusterLinkService{})

	require.NoError(t, wf.FenceGateway(context.Background(), tierTestConfig()))

	assert.Equal(t, []string{"detect", "snapshot", "apply", "reconcile", "waitConfig"}, probe.calls)
	assert.Equal(t, gateway.ConditionSnapshot{"platform.confluent.io/cluster-ready": {}}, probe.waitSnapshot,
		"the pre-apply snapshot must reach the wait")
}

// Under hot reload no pods roll, so the rollout waits prove nothing and must
// not be the gate.
func TestFenceGateway_TierA_DoesNotUseRolloutWaits(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPerPodConfigID, nil), &mockClusterLinkService{})

	require.NoError(t, wf.FenceGateway(context.Background(), tierTestConfig()))

	assert.Zero(t, probe.readyWaitCalls, "Tier A must not gate on WaitForGatewayReady")
	assert.Zero(t, probe.waitPodsCalls, "Tier A must not gate on pod replacement")
	// The acceptance wait is not a rollout wait and does run here: /config proves
	// what the pods serve, but only the operator can say it took the spec at all.
	assert.Contains(t, probe.calls, "reconcile")
}

// Losing the baseline costs the wait its fast-fail, not its correctness, so it
// must not abort a fence.
func TestFenceGateway_TierA_SnapshotFailureIsNonFatal(t *testing.T) {
	probe := &fenceProbe{}
	gw := newTierMock(probe, gateway.TierPerPodConfigID, nil)
	gw.snapshotGatewayConditionsFn = func(_ context.Context, _, _ string) (gateway.ConditionSnapshot, error) {
		probe.calls = append(probe.calls, "snapshot")
		return nil, errors.New("403 forbidden")
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})

	require.NoError(t, wf.FenceGateway(context.Background(), tierTestConfig()))
	assert.Contains(t, probe.calls, "waitConfig", "the fence must still be verified without a baseline")
	assert.Nil(t, probe.waitSnapshot, "a failed snapshot must pass nil, which disables the fast-fail")
}

func TestFenceGateway_TierA_ConfigWaitErrorAborts(t *testing.T) {
	probe := &fenceProbe{}
	gw := newTierMock(probe, gateway.TierPerPodConfigID, nil)
	gw.waitForGatewayConfigAppliedFn = func(_ context.Context, _, _, _ string, _ gateway.ConditionSnapshot, _ string, _, _ time.Duration, _ func(gateway.ConfigApplyProgress)) error {
		return fmt.Errorf("timed out waiting for gateway pods: 1 of 3 gateway pods have applied the new config")
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})

	err := wf.FenceGateway(context.Background(), tierTestConfig())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed verifying gateway pods applied the fence")
	assert.Contains(t, err.Error(), "1 of 3 gateway pods")
}

// An operator rejection must survive the wrapping so callers can distinguish it
// from a timeout.
func TestFenceGateway_TierA_RejectionIsRecoverable(t *testing.T) {
	probe := &fenceProbe{}
	rejection := &gateway.GatewayRejection{
		ConditionType: gateway.ConditionHotReloadStatus,
		Reason:        "ContainerCrashed",
		Message:       "canary exited 1",
	}
	gw := newTierMock(probe, gateway.TierPerPodConfigID, nil)
	gw.waitForGatewayConfigAppliedFn = func(_ context.Context, _, _, _ string, _ gateway.ConditionSnapshot, _ string, _, _ time.Duration, _ func(gateway.ConfigApplyProgress)) error {
		return rejection
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})

	err := wf.FenceGateway(context.Background(), tierTestConfig())
	require.Error(t, err)

	var got *gateway.GatewayRejection
	require.True(t, errors.As(err, &got), "the rejection must remain unwrappable: %v", err)
	assert.Equal(t, "ContainerCrashed", got.Reason)
}

// Unlike a rollout, a hot reload that has not landed is broken rather than
// slow, so this wait is bounded even when the rollout waits are not.
func TestFenceGateway_TierA_ConfigWaitIsBoundedByDefault(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPerPodConfigID, nil), &mockClusterLinkService{})

	require.NoError(t, wf.FenceGateway(context.Background(), tierTestConfig()))

	assert.Equal(t, defaultGatewayConfigTimeout, probe.waitTimeout)
	assert.NotZero(t, probe.waitTimeout, "an unbounded config wait would hang on the licence-gate failure mode")
	assert.Equal(t, gateway.DefaultGatewayConfigPort, probe.waitPort)
	assert.Equal(t, gatewayConfigPollInterval, probe.waitPoll)
}

func TestFenceGateway_TierA_ExplicitRolloutTimeoutWins(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPerPodConfigID, nil), &mockClusterLinkService{})
	wf.SetRolloutTimeout(5 * time.Minute)

	require.NoError(t, wf.FenceGateway(context.Background(), tierTestConfig()))
	assert.Equal(t, 5*time.Minute, probe.waitTimeout)
}

// A fenced CR that also changes something non-hot-reloadable rolls the pods,
// and a terminating pod keeps serving behind the Service while /config no
// longer counts it. When detection was requested, close that gap too.
func TestFenceGateway_TierA_DetectingAlsoDrainsOldPods(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPerPodConfigID, nil), &mockClusterLinkService{})
	config := tierTestConfig()
	config.DetectUnroutedProducersDuration = 10 * time.Second

	require.NoError(t, wf.FenceGateway(context.Background(), config))

	assert.Equal(t, []string{"detect", "snapshot", "getUIDs", "apply", "reconcile", "waitConfig", "waitPods"}, probe.calls,
		"the pre-fence pod set must be captured before the apply and drained after verification")
	assert.Equal(t, map[k8stypes.UID]struct{}{"old-pod": {}}, probe.waitPodUIDs)
}

// If the CR cannot be stamped, applying it anyway would leave the wait polling
// for an id no pod will ever report — a guaranteed timeout on a fence that
// actually landed. Fail before touching the cluster instead.
func TestFenceGateway_TierA_StampFailureAbortsBeforeApply(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPerPodConfigID, nil), &mockClusterLinkService{})
	config := tierTestConfig()
	config.FencedCrYAML = []byte("fenced") // a scalar: no spec to stamp

	err := wf.FenceGateway(context.Background(), config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to prepare the fenced gateway CR")
	assert.NotContains(t, probe.calls, "apply", "a CR that cannot be verified must not be applied")
}

// ---------------------------------------------------------------------------
// Tier B — hot reload without a per-pod handle
// ---------------------------------------------------------------------------

func TestFenceGateway_TierB_NoConfigIDAndNoPerPodWait(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierHotReloadOnly, nil), &mockClusterLinkService{})

	require.NoError(t, wf.FenceGateway(context.Background(), tierTestConfig()))

	assert.Equal(t, []byte(testFencedCR), probe.appliedYAML, "Tier B must apply the CR byte-for-byte")
	_, found := specConfigID(t, probe.appliedYAML)
	assert.False(t, found, "a configId is useless on Tier B and must not be written")
	assert.NotContains(t, probe.calls, "waitConfig")
	assert.Equal(t, []string{"detect", "snapshot", "apply", "reconcile"}, probe.calls)
}

// TestFenceGateway_TierB_SnapshotsConditionsForTheAcceptanceWait: the baseline is
// not per-pod-only. The acceptance wait reads conditions on every tier, and
// without a pre-apply baseline it cannot tell a fresh rejection from a failure
// condition that has been sitting there for days — so it would silently give up
// its fast-fail on exactly the tier that has no other proof.
func TestFenceGateway_TierB_SnapshotsConditionsForTheAcceptanceWait(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierHotReloadOnly, nil), &mockClusterLinkService{})

	require.NoError(t, wf.FenceGateway(context.Background(), tierTestConfig()))
	// That it lands *before* the apply — the property that makes it a baseline at
	// all — is pinned by the call-order assertion in
	// TestFenceGateway_TierB_NoConfigIDAndNoPerPodWait.
	assert.Contains(t, probe.calls, "snapshot")
}

func TestFenceGateway_TierB_ReconcileErrorAborts(t *testing.T) {
	probe := &fenceProbe{}
	gw := newTierMock(probe, gateway.TierHotReloadOnly, nil)
	gw.waitForGatewayAcceptedFn = func(_ context.Context, _, _ string, _ gateway.ConditionSnapshot, _, _ time.Duration) error {
		return errors.New("operator never reconciled")
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})

	err := wf.FenceGateway(context.Background(), tierTestConfig())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed waiting for gateway reconcile")
}

func TestFenceGateway_TierB_DetectingDrainsOldPods(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierHotReloadOnly, nil), &mockClusterLinkService{})
	config := tierTestConfig()
	config.DetectUnroutedProducersDuration = 10 * time.Second

	require.NoError(t, wf.FenceGateway(context.Background(), config))
	assert.Equal(t, []string{"detect", "snapshot", "getUIDs", "apply", "reconcile", "waitPods"}, probe.calls)
}

// ---------------------------------------------------------------------------
// Tier C — the pre-existing rollout path, which must not change
// ---------------------------------------------------------------------------

// The E7 guard. CFK folds configId into the pod-template config-revision-hash,
// so stamping one with hot reload off was measured rolling every gateway pod —
// turning an idempotent re-apply into a client-visible outage.
func TestFenceGateway_TierC_NeverStampsConfigID(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPodRollout, nil), &mockClusterLinkService{})

	require.NoError(t, wf.FenceGateway(context.Background(), tierTestConfig()))

	assert.Equal(t, []byte(testFencedCR), probe.appliedYAML, "Tier C must apply the CR byte-for-byte")
	_, found := specConfigID(t, probe.appliedYAML)
	assert.False(t, found, "stamping a configId with hot reload off rolls every gateway pod")
}

func TestFenceGateway_TierC_DetectionOff_UnchangedBehaviour(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPodRollout, nil), &mockClusterLinkService{})

	require.NoError(t, wf.FenceGateway(context.Background(), tierTestConfig()))
	assert.Equal(t, []string{"detect", "snapshot", "apply", "reconcile", "waitReady"}, probe.calls)
}

func TestFenceGateway_TierC_DetectionOn_UnchangedBehaviour(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPodRollout, nil), &mockClusterLinkService{})
	config := tierTestConfig()
	config.DetectUnroutedProducersDuration = 10 * time.Second

	require.NoError(t, wf.FenceGateway(context.Background(), config))

	assert.Equal(t, []string{"detect", "snapshot", "getUIDs", "apply", "reconcile", "waitPods"}, probe.calls)
	assert.Equal(t, 1, probe.waitPodsCalls, "the drain add-on must not double up on the rollout tier")
}

// ---------------------------------------------------------------------------
// Detection failure
// ---------------------------------------------------------------------------

// Refusing to fence because the gateway could not be classified would be worse
// than fencing with the verification that shipped before tiers existed.
func TestFenceGateway_DetectFailure_StillFencesOnFallbackTier(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPodRollout, errors.New("connection reset")), &mockClusterLinkService{})

	require.NoError(t, wf.FenceGateway(context.Background(), tierTestConfig()))
	assert.Equal(t, []string{"detect", "snapshot", "apply", "reconcile", "waitReady"}, probe.calls)
	_, found := specConfigID(t, probe.appliedYAML)
	assert.False(t, found, "an unclassified gateway must never be stamped")
}

// A probe that failed after establishing hot reload is on must not drop to the
// rollout path, whose wait passes without proving anything under hot reload.
func TestFenceGateway_DetectFailure_HotReloadFallbackUsesTierB(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierHotReloadOnly, errors.New("probe failed")), &mockClusterLinkService{})

	require.NoError(t, wf.FenceGateway(context.Background(), tierTestConfig()))
	assert.Equal(t, []string{"detect", "snapshot", "apply", "reconcile"}, probe.calls)
	assert.Zero(t, probe.readyWaitCalls)
}

// The detector needs the live CR for the hot-reload flag and the candidate for
// the configId probe; swapping them would classify the wrong document.
func TestFenceGateway_DetectReceivesInitialAndFencedCRs(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPodRollout, nil), &mockClusterLinkService{})

	require.NoError(t, wf.FenceGateway(context.Background(), tierTestConfig()))
	assert.Equal(t, []byte(testInitialCR), probe.detectInitialYAML)
	assert.Equal(t, []byte(testFencedCR), probe.detectCandidateYAML)
}

// ===========================================================================
// SwitchGateway and unfenceGateway tier-selected verification
//
// These two differ in a way that matters: removing a fence from an existing
// route is hot-reloadable, but the switchover CR changes streamingDomains and
// secretStores, which CFK cannot apply in place. So one migration run needs
// both strategies, and the switchover has to settle its new pod set before
// asking the pods what they serve.
// ===========================================================================

func switchTestConfig() *MigrationConfig {
	c := tierTestConfig()
	c.SwitchoverCrYAML = []byte(testSwitchoverCR)
	return c
}

// ---------------------------------------------------------------------------
// SwitchGateway
// ---------------------------------------------------------------------------

// The ordering that matters: the readiness wait settles the replacement pods
// before the per-pod check asks what they are serving. Asking first would
// interrogate pods that are about to be deleted.
func TestSwitchGateway_TierA_SettlesPodsThenVerifiesPerPod(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPerPodConfigID, nil), &mockClusterLinkService{})

	require.NoError(t, wf.SwitchGateway(context.Background(), switchTestConfig()))
	assert.Equal(t, []string{"detect", "snapshot", "apply", "reconcile", "waitReady", "waitConfig"}, probe.calls)
}

func TestSwitchGateway_TierA_StampedIDMatchesVerifiedID(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPerPodConfigID, nil), &mockClusterLinkService{})

	require.NoError(t, wf.SwitchGateway(context.Background(), switchTestConfig()))

	stamped, found := specConfigID(t, probe.appliedYAML)
	require.True(t, found, "Tier A must stamp spec.configId on the switchover CR")
	assert.Equal(t, stamped, probe.waitConfigID)
}

// TestSwitchGateway_TierB_AcceptanceGatesReadiness: the acceptance wait precedes
// the readiness wait even though the switchover rolls the pods.
//
// The reasoning that once made it look redundant — a rollout cannot start without
// the operator having reconciled — only holds when a rollout actually starts. When
// the operator *refuses* the spec it never touches the Deployment, so the
// readiness wait finds a healthy Deployment on the previous generation and reports
// "no pod restart required". Ordering acceptance first is what turns that silent
// false success into the operator's own error.
func TestSwitchGateway_TierB_AcceptanceGatesReadiness(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierHotReloadOnly, nil), &mockClusterLinkService{})

	require.NoError(t, wf.SwitchGateway(context.Background(), switchTestConfig()))
	assert.Equal(t, []string{"detect", "snapshot", "apply", "reconcile", "waitReady"}, probe.calls)
}

func TestSwitchGateway_TierC_UnchangedBehaviour(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPodRollout, nil), &mockClusterLinkService{})

	require.NoError(t, wf.SwitchGateway(context.Background(), switchTestConfig()))
	assert.Equal(t, []string{"detect", "snapshot", "apply", "reconcile", "waitReady"}, probe.calls)
	assert.Equal(t, []byte(testSwitchoverCR), probe.appliedYAML, "Tier C must apply the CR byte-for-byte")
	_, found := specConfigID(t, probe.appliedYAML)
	assert.False(t, found, "stamping a configId with hot reload off rolls every gateway pod")
}

// The switchover CR is the candidate for the configId probe, not the fenced one:
// they are different documents and only the one being applied can be classified.
func TestSwitchGateway_DetectReceivesSwitchoverCR(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPodRollout, nil), &mockClusterLinkService{})

	require.NoError(t, wf.SwitchGateway(context.Background(), switchTestConfig()))
	assert.Equal(t, []byte(testSwitchoverCR), probe.detectCandidateYAML)
	assert.Equal(t, []byte(testInitialCR), probe.detectInitialYAML)
}

// All three gateway applies could otherwise fail with the same bare
// "failed waiting for gateway readiness".
func TestSwitchGateway_ErrorsNameTheStage(t *testing.T) {
	probe := &fenceProbe{}
	gw := newTierMock(probe, gateway.TierPodRollout, nil)
	gw.waitForGatewayReadyFn = func(_ context.Context, _, _ string, _, _ time.Duration, _ func(gateway.GatewayReadinessProgress)) error {
		return errors.New("pods never became ready")
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})

	err := wf.SwitchGateway(context.Background(), switchTestConfig())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed waiting for gateway readiness after switchover")
	assert.Contains(t, err.Error(), "pods never became ready")
}

func TestSwitchGateway_TierA_ConfigWaitErrorNamesTheStage(t *testing.T) {
	probe := &fenceProbe{}
	gw := newTierMock(probe, gateway.TierPerPodConfigID, nil)
	gw.waitForGatewayConfigAppliedFn = func(_ context.Context, _, _, _ string, _ gateway.ConditionSnapshot, _ string, _, _ time.Duration, _ func(gateway.ConfigApplyProgress)) error {
		return errors.New("2 of 3 gateway pods have applied the new config")
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})

	err := wf.SwitchGateway(context.Background(), switchTestConfig())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed verifying gateway pods applied the switchover")
}

func TestSwitchGateway_StampFailureAbortsBeforeApply(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPerPodConfigID, nil), &mockClusterLinkService{})
	config := switchTestConfig()
	config.SwitchoverCrYAML = []byte("switchover") // a scalar: no spec to stamp

	err := wf.SwitchGateway(context.Background(), config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to prepare the switchover gateway CR")
	assert.NotContains(t, probe.calls, "apply")
}

// ---------------------------------------------------------------------------
// unfenceGateway
// ---------------------------------------------------------------------------

func unfenceTestConfig() *MigrationConfig {
	return &MigrationConfig{
		K8sNamespace:  "ns",
		InitialCrName: "gw-1",
		InitialCrYAML: []byte(testFetchedInitialCR),
	}
}

// Removing a fence from an existing route is hot-reloadable, so there is no
// rollout to wait for — and waiting for one would report "no pod restart
// required" and prove nothing.
func TestUnfenceGateway_TierA_VerifiesPerPodWithoutRolloutWait(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPerPodConfigID, nil), &mockClusterLinkService{})

	require.NoError(t, wf.unfenceGateway(context.Background(), unfenceTestConfig()))
	assert.Equal(t, []string{"detect", "snapshot", "apply", "reconcile", "waitConfig"}, probe.calls)
	assert.Zero(t, probe.readyWaitCalls)
}

// The rollback path must be verified too, or a failed rollback reports as
// recovered — the migration would look rolled back while the fence still held.
func TestUnfenceGateway_TierA_ConfigWaitErrorNamesTheStage(t *testing.T) {
	probe := &fenceProbe{}
	gw := newTierMock(probe, gateway.TierPerPodConfigID, nil)
	gw.waitForGatewayConfigAppliedFn = func(_ context.Context, _, _, _ string, _ gateway.ConditionSnapshot, _ string, _, _ time.Duration, _ func(gateway.ConfigApplyProgress)) error {
		return errors.New("0 of 3 gateway pods have applied the new config")
	}
	wf := NewMigrationActions(gw, &mockClusterLinkService{})

	err := wf.unfenceGateway(context.Background(), unfenceTestConfig())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed verifying gateway pods applied the unfence")
}

// A migration with no record yet mints its own id, and two independent
// migrations must not collide.
func TestUnfenceGateway_TierA_MintsAnIDWhenNoneRecorded(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPerPodConfigID, nil), &mockClusterLinkService{})

	require.NoError(t, wf.unfenceGateway(context.Background(), unfenceTestConfig()))
	require.NoError(t, wf.unfenceGateway(context.Background(), unfenceTestConfig()))

	require.Len(t, probe.waitConfigIDs, 2)
	assert.NotEmpty(t, probe.waitConfigIDs[0])
	assert.NotEqual(t, probe.waitConfigIDs[0], probe.waitConfigIDs[1],
		"two migrations with no recorded revision must not collide")
}

// The load-bearing one. client-go's dynamic Apply rejects managedFields
// client-side, before any request leaves the process, so probing the CR as
// fetched would fail deterministically and silently downgrade the tier.
func TestUnfenceGateway_ProbesTheStrippedCR(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPerPodConfigID, nil), &mockClusterLinkService{})

	require.NoError(t, wf.unfenceGateway(context.Background(), unfenceTestConfig()))

	candidate := string(probe.detectCandidateYAML)
	require.NotEmpty(t, candidate)
	for _, field := range []string{"managedFields", "resourceVersion", "uid:", "creationTimestamp", "generation", "status"} {
		assert.NotContains(t, candidate, field, "the probed CR must be stripped of %s", field)
	}
	assert.Contains(t, candidate, "hotReload", "stripping must not damage the spec")
}

func TestUnfenceGateway_TierB_ReconcilesWithoutReadinessWait(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierHotReloadOnly, nil), &mockClusterLinkService{})

	require.NoError(t, wf.unfenceGateway(context.Background(), unfenceTestConfig()))
	assert.Equal(t, []string{"detect", "snapshot", "apply", "reconcile"}, probe.calls)
	assert.Zero(t, probe.readyWaitCalls)
}

func TestUnfenceGateway_TierC_UnchangedBehaviour(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPodRollout, nil), &mockClusterLinkService{})

	require.NoError(t, wf.unfenceGateway(context.Background(), unfenceTestConfig()))
	assert.Equal(t, []string{"detect", "snapshot", "apply", "reconcile", "waitReady"}, probe.calls)
	_, found := specConfigID(t, probe.appliedYAML)
	assert.False(t, found, "stamping a configId with hot reload off rolls every gateway pod")
}

func TestUnfenceGateway_MalformedInitialCRFailsBeforeDetect(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPodRollout, nil), &mockClusterLinkService{})
	config := unfenceTestConfig()
	config.InitialCrYAML = []byte("metadata: [unterminated")

	err := wf.unfenceGateway(context.Background(), config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse initial CR YAML")
	assert.Empty(t, probe.calls, "nothing should reach the cluster")
}

// ---------------------------------------------------------------------------
// strippedInitialCR
// ---------------------------------------------------------------------------

func TestStrippedInitialCR_RemovesServerManagedFieldsAndKeepsSpec(t *testing.T) {
	clean, err := strippedInitialCR([]byte(testFetchedInitialCR))
	require.NoError(t, err)

	var obj map[string]any
	require.NoError(t, yaml.Unmarshal(clean, &obj))

	_, hasStatus := obj["status"]
	assert.False(t, hasStatus, "status must go: server-side apply owns it")

	metadata, ok := obj["metadata"].(map[string]any)
	require.True(t, ok, "metadata must survive — the name and namespace are needed")
	for _, field := range []string{"managedFields", "resourceVersion", "uid", "creationTimestamp", "generation"} {
		_, present := metadata[field]
		assert.False(t, present, "%s must be stripped", field)
	}
	assert.Equal(t, "gw-1", metadata["name"])

	spec, ok := obj["spec"].(map[string]any)
	require.True(t, ok, "spec must survive intact")
	assert.Contains(t, spec, "hotReload")
	assert.Contains(t, spec, "routes")
}

func TestStrippedInitialCR_NoMetadataIsNotAnError(t *testing.T) {
	clean, err := strippedInitialCR([]byte("apiVersion: v1\nkind: Gateway\nspec:\n  replicas: 1\n"))
	require.NoError(t, err)
	assert.Contains(t, string(clean), "replicas")
}

// ===========================================================================
// Config-revision persistence and reuse
//
// The gateway CRs are captured at init and never re-read, so a recorded id can
// never be paired with changed bytes. That is what makes reuse safe: a resume
// re-applies byte-identical YAML, which CFK treats as a no-op instead of running
// another hot reload and canary.
// ===========================================================================

func TestFenceGateway_TierA_RecordsTheVerifiedRevision(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPerPodConfigID, nil), &mockClusterLinkService{})
	config := tierTestConfig()

	require.NoError(t, wf.FenceGateway(context.Background(), config))

	assert.NotEmpty(t, config.FenceConfigId, "the fence must record the revision it verified")
	assert.Equal(t, config.FenceConfigId, probe.waitConfigID)
	assert.Empty(t, config.SwitchoverConfigId, "the fence must not touch other stages' records")
	assert.Empty(t, config.UnfenceConfigId)
}

// The point of recording it: a resume re-verifies the same revision, so the
// re-apply is byte-identical and CFK does not bump the generation again.
func TestFenceGateway_TierA_ReusesTheRecordedRevision(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPerPodConfigID, nil), &mockClusterLinkService{})
	config := tierTestConfig()
	config.FenceConfigId = "kcp-1754300000-000001-deadbeef"

	require.NoError(t, wf.FenceGateway(context.Background(), config))

	assert.Equal(t, "kcp-1754300000-000001-deadbeef", probe.waitConfigID, "a recorded revision must be reused, not replaced")
	assert.Equal(t, "kcp-1754300000-000001-deadbeef", config.FenceConfigId)

	stamped, found := specConfigID(t, probe.appliedYAML)
	require.True(t, found)
	assert.Equal(t, "kcp-1754300000-000001-deadbeef", stamped, "the applied CR must carry the reused id")
}

// Two applies of the same migration reuse one id; different stages keep their
// own. Both halves matter: reuse makes a resume idempotent, separation keeps an
// unfence distinguishable from the fence it undid.
func TestGatewayApplies_ReusePerStageAndSeparateAcrossStages(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPerPodConfigID, nil), &mockClusterLinkService{})
	config := switchTestConfig()

	require.NoError(t, wf.FenceGateway(context.Background(), config))
	require.NoError(t, wf.FenceGateway(context.Background(), config)) // a resume
	require.NoError(t, wf.SwitchGateway(context.Background(), config))

	require.Len(t, probe.waitConfigIDs, 3)
	assert.Equal(t, probe.waitConfigIDs[0], probe.waitConfigIDs[1], "re-fencing must reuse the recorded revision")
	assert.NotEqual(t, probe.waitConfigIDs[0], probe.waitConfigIDs[2], "switchover must not reuse the fence's revision")

	assert.Equal(t, probe.waitConfigIDs[0], config.FenceConfigId)
	assert.Equal(t, probe.waitConfigIDs[2], config.SwitchoverConfigId)
}

func TestUnfenceGateway_TierA_RecordsAndReusesItsOwnRevision(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPerPodConfigID, nil), &mockClusterLinkService{})
	config := unfenceTestConfig()

	require.NoError(t, wf.unfenceGateway(context.Background(), config))
	first := config.UnfenceConfigId
	require.NotEmpty(t, first)

	require.NoError(t, wf.unfenceGateway(context.Background(), config))
	assert.Equal(t, first, config.UnfenceConfigId)
	assert.Equal(t, first, probe.waitConfigIDs[1])
}

// Nothing is stamped on a rollout-tier gateway, so nothing should be recorded
// either — an id in the file would imply a verification that never happened.
func TestFenceGateway_TierC_RecordsNoRevision(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPodRollout, nil), &mockClusterLinkService{})
	config := tierTestConfig()

	require.NoError(t, wf.FenceGateway(context.Background(), config))
	assert.Empty(t, config.FenceConfigId)
}

// A CFK downgrade mid-migration drops the gateway to a lower tier, which stamps
// nothing. Erasing what an earlier run verified would lose support history for
// no gain.
func TestFenceGateway_TierDowngrade_KeepsTheEarlierRecord(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPodRollout, nil), &mockClusterLinkService{})
	config := tierTestConfig()
	config.FenceConfigId = "kcp-1754300000-000001-deadbeef"

	require.NoError(t, wf.FenceGateway(context.Background(), config))
	assert.Equal(t, "kcp-1754300000-000001-deadbeef", config.FenceConfigId, "an earlier record must survive a tier downgrade")
	_, found := specConfigID(t, probe.appliedYAML)
	assert.False(t, found, "the downgraded tier must still not stamp the CR")
}

// ===========================================================================
// --gateway-config-port / --gateway-config-timeout resolution
// ===========================================================================

func TestResolveGatewayConfigTimeout_Precedence(t *testing.T) {
	wf := NewMigrationActions(&mockGatewayService{}, &mockClusterLinkService{})
	assert.Equal(t, defaultGatewayConfigTimeout, wf.resolveGatewayConfigTimeout(), "unset falls back to the built-in default")

	wf.SetRolloutTimeout(4 * time.Minute)
	assert.Equal(t, 4*time.Minute, wf.resolveGatewayConfigTimeout(), "the rollout timeout applies when nothing more specific is set")

	wf.SetGatewayConfigTimeout(30 * time.Second)
	assert.Equal(t, 30*time.Second, wf.resolveGatewayConfigTimeout(), "the explicit config timeout wins")
}

func TestResolveGatewayConfigPort_DefaultAndOverride(t *testing.T) {
	wf := NewMigrationActions(&mockGatewayService{}, &mockClusterLinkService{})
	assert.Equal(t, gateway.DefaultGatewayConfigPort, wf.resolveGatewayConfigPort())

	wf.SetGatewayConfigPort("19180")
	assert.Equal(t, "19180", wf.resolveGatewayConfigPort())
}

// The resolved values have to actually reach the wait, not just resolve.
func TestFenceGateway_TierA_HonoursGatewayConfigOverrides(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPerPodConfigID, nil), &mockClusterLinkService{})
	wf.SetGatewayConfigPort("19180")
	wf.SetGatewayConfigTimeout(45 * time.Second)

	require.NoError(t, wf.FenceGateway(context.Background(), tierTestConfig()))
	assert.Equal(t, "19180", probe.waitPort)
	assert.Equal(t, 45*time.Second, probe.waitTimeout)
}

func TestSwitchGateway_TierA_HonoursGatewayConfigOverrides(t *testing.T) {
	probe := &fenceProbe{}
	wf := NewMigrationActions(newTierMock(probe, gateway.TierPerPodConfigID, nil), &mockClusterLinkService{})
	wf.SetGatewayConfigPort("19180")
	wf.SetGatewayConfigTimeout(45 * time.Second)

	require.NoError(t, wf.SwitchGateway(context.Background(), switchTestConfig()))
	assert.Equal(t, "19180", probe.waitPort)
	assert.Equal(t, 45*time.Second, probe.waitTimeout)
}

// A denial is only actionable if it survives the workflow's error wrapping —
// switching any of these wraps to %v would silently reduce it to prose.
func TestGatewayApplies_AccessDenialStaysUnwrappable(t *testing.T) {
	denial := &gateway.GatewayConfigAccessDeniedError{Namespace: "confluent"}

	newDenyingMock := func(probe *fenceProbe) *mockGatewayService {
		gw := newTierMock(probe, gateway.TierPerPodConfigID, nil)
		gw.waitForGatewayConfigAppliedFn = func(_ context.Context, _, _, _ string, _ gateway.ConditionSnapshot, _ string, _, _ time.Duration, _ func(gateway.ConfigApplyProgress)) error {
			return denial
		}
		return gw
	}

	for _, tc := range []struct {
		stage string
		run   func(wf *MigrationActions) error
	}{
		{"fence", func(wf *MigrationActions) error {
			return wf.FenceGateway(context.Background(), tierTestConfig())
		}},
		{"switchover", func(wf *MigrationActions) error {
			return wf.SwitchGateway(context.Background(), switchTestConfig())
		}},
		{"unfence", func(wf *MigrationActions) error {
			return wf.unfenceGateway(context.Background(), unfenceTestConfig())
		}},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			wf := NewMigrationActions(newDenyingMock(&fenceProbe{}), &mockClusterLinkService{})

			err := tc.run(wf)
			require.Error(t, err)

			var got *gateway.GatewayConfigAccessDeniedError
			require.True(t, errors.As(err, &got), "the denial must survive wrapping: %v", err)
			assert.Contains(t, err.Error(), "pods/proxy")
			assert.Contains(t, err.Error(), tc.stage, "the error must still say which apply failed")
		})
	}
}
