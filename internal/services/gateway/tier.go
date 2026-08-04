package gateway

import (
	"fmt"

	"github.com/goccy/go-yaml"
)

// How a given gateway's config change can be verified.
//
// Hot-reload support and spec.configId support are separate questions: there is
// a shipping CFK window with the first and not the second, and the currently
// public-latest chart sits in it. So the verification strategy is chosen per
// cluster, from two facts read off the cluster itself, rather than assumed.
//
// Note the hot-reload flag alone decides whether kcp writes a configId, and it
// has to be settled before the apply: injecting one on a gateway whose hot reload
// is off triggers a full rolling restart of every pod, because CFK folds the field
// into the pod-template config-revision-hash. Whether the field then survived is
// observed from the apply response — see ResolveVerificationTier.

// VerificationTier names a config-change verification strategy. The string
// values are stable because they are logged.
type VerificationTier string

const (
	// TierPerPodConfigID is the full contract (doc "Tier A"): hot reload is on
	// and the CRD accepts spec.configId, so kcp stamps an id and polls GET
	// /config on every pod until they all echo it. The only sound gate.
	TierPerPodConfigID VerificationTier = "per-pod-configid"

	// TierHotReloadOnly (doc "Tier B") is hot reload without spec.configId:
	// CFK ≤ 0.1718.10, which includes public-latest. No per-pod handle exists,
	// so verification degrades to the operator's own signals and the caller
	// must warn that per-pod confirmation is unavailable.
	TierHotReloadOnly VerificationTier = "hot-reload-only"

	// TierPodRollout (doc "Tier C") is hot reload off: a config change rolls
	// the pods, so the existing rollout wait is a real gate. No configId.
	TierPodRollout VerificationTier = "pod-rollout"
)

// MinCFKVersionForConfigID is the earliest CFK version observed to accept
// spec.configId, named in the Tier B warning so the operator knows what to
// upgrade to. Detection is empirical, never version-sniffed — this is for the
// message only.
const MinCFKVersionForConfigID = "0.1718.34"

// gatewayFieldManager is the server-side-apply field manager kcp owns its
// gateway fields under.
const gatewayFieldManager = "kcp-migration"

// ResolveVerificationTier decides how a config change can be verified, from the
// only two facts that matter: whether the gateway hot-reloads, and whether the
// spec.configId kcp sent is the one the server stored.
//
// Both are known without asking the cluster anything extra. hotReload is read
// off the live CR that GetGatewayYAML already fetched; storedConfigID comes back
// on the apply response, because a structural CRD schema prunes an undeclared
// field silently and the response is what says whether it survived.
//
// Reading it back afterwards rather than probing beforehand is safe because
// stamping is only ever harmful when hot reload is OFF: there CFK folds configId
// into the pod-template config-revision-hash and a bare bump rolls every pod
// (measured as a full rolling restart). With hot reload on, a pruned configId is
// simply inert. So the one decision that must be made before writing —
// whether to stamp at all — needs only hotReload, and the rest can be observed.
//
// wantConfigID empty means kcp did not stamp one, which is the hot-reload-off
// case; the mismatch branch also covers a server that returns something other
// than what was written, since that is not a usable handle either.
func ResolveVerificationTier(hotReload bool, wantConfigID, storedConfigID string) VerificationTier {
	if !hotReload {
		return TierPodRollout
	}
	if wantConfigID != "" && storedConfigID == wantConfigID {
		return TierPerPodConfigID
	}
	return TierHotReloadOnly
}

// HotReloadEnabled reads spec.hotReload.enabled from a Gateway CR. It is what
// decides whether kcp may stamp a spec.configId at all.
//
// An absent hotReload block, or a block without enabled, is a clean false: that
// is the shape of every gateway CR this repo currently generates, and of every
// gateway predating the feature. A present-but-unreadable value is an error
// instead, because it leaves the one decision that must not be guessed —
// whether writing a configId would roll the pods — unresolved.
func HotReloadEnabled(crYAML []byte) (bool, error) {
	var obj map[string]any
	if err := yaml.Unmarshal(crYAML, &obj); err != nil {
		return false, fmt.Errorf("failed to parse gateway CR YAML: %w", err)
	}

	spec, ok := obj["spec"].(map[string]any)
	if !ok {
		return false, fmt.Errorf("gateway CR has no spec object to read hotReload from")
	}

	raw, present := spec["hotReload"]
	if !present || raw == nil {
		return false, nil
	}
	hotReload, ok := raw.(map[string]any)
	if !ok {
		return false, fmt.Errorf("gateway CR spec.hotReload is not an object")
	}

	rawEnabled, present := hotReload["enabled"]
	if !present || rawEnabled == nil {
		return false, nil
	}
	enabled, ok := rawEnabled.(bool)
	if !ok {
		return false, fmt.Errorf("gateway CR spec.hotReload.enabled is not a boolean")
	}
	return enabled, nil
}
