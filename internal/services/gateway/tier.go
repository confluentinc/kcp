package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/goccy/go-yaml"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

// How a given gateway's config change can be verified.
//
// Hot-reload support and spec.configId support are separate questions: there is
// a shipping CFK window with the first and not the second, and the currently
// public-latest chart sits in it. So the verification strategy is chosen per
// cluster, from two facts read off the cluster itself, rather than assumed.
//
// The tier is not cosmetic. Injecting a configId on a gateway whose hot reload
// is off triggers a full rolling restart of every gateway pod — CFK folds the
// field into the pod-template config-revision-hash — so the tier decides
// whether kcp writes to the CR at all, not merely how it waits afterwards.

// VerificationTier names a config-change verification strategy. The string
// values are stable: they are logged and will be persisted with a migration.
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
// gateway fields under. Must match the literal in ApplyGatewayYAML: the probe
// dry-runs as the same manager so an ownership conflict surfaces at detection
// time rather than mid-fence.
const gatewayFieldManager = "kcp-migration"

// InjectsConfigID reports whether this tier may stamp spec.configId. Only the
// full contract tier may: on any other, the field either does nothing or rolls
// every pod.
func (t VerificationTier) InjectsConfigID() bool {
	return t == TierPerPodConfigID
}

// DetectGatewayVerificationTier decides how a config change to this gateway can
// be verified.
//
// initialYAML is the live CR (as fetched by GetGatewayYAML) and supplies the
// hot-reload flag; candidateYAML is the CR about to be applied and is what the
// configId probe dry-runs, so the verdict is authoritative for the exact bytes
// that will be sent.
//
// On error the returned tier is still the safe fallback for a caller that
// chooses to continue — see detectGatewayVerificationTier.
func (s *K8sService) DetectGatewayVerificationTier(ctx context.Context, namespace, gatewayName string, initialYAML, candidateYAML []byte) (VerificationTier, error) {
	config, err := clientcmd.BuildConfigFromFlags("", s.kubeConfigPath)
	if err != nil {
		return TierPodRollout, fmt.Errorf("failed to build config: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return TierPodRollout, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	return detectGatewayVerificationTier(ctx, dynamicClient, namespace, gatewayName, initialYAML, candidateYAML)
}

// detectGatewayVerificationTier is the inner orchestration used by
// DetectGatewayVerificationTier. Split from the method so unit tests can inject
// a fake dynamic client.
//
// The fallback returned alongside an error is chosen so that continuing past it
// is never worse than not knowing:
//
//   - Hot-reload state unreadable ⇒ TierPodRollout, the tier that never writes
//     a configId. Guessing the other way could roll a production gateway.
//   - Hot reload known on but the probe failed ⇒ TierHotReloadOnly, never
//     TierPodRollout. Under hot reload the rollout wait reports "no rollout
//     detected, treating patch as a no-op" and passes, which is precisely the
//     false success being designed out; Tier B is blind but says so.
func detectGatewayVerificationTier(ctx context.Context, dynamicClient dynamic.Interface, namespace, gatewayName string, initialYAML, candidateYAML []byte) (VerificationTier, error) {
	hotReload, err := hotReloadEnabled(initialYAML)
	if err != nil {
		return TierPodRollout, fmt.Errorf("failed to read spec.hotReload.enabled from the live gateway CR: %w", err)
	}

	if !hotReload {
		slog.Debug("gateway hot reload is off; config changes roll the pods",
			"namespace", namespace, "gateway", gatewayName, "tier", string(TierPodRollout))
		return TierPodRollout, nil
	}

	supported, err := dryRunSupportsConfigID(ctx, dynamicClient, namespace, gatewayName, candidateYAML)
	if err != nil {
		return TierHotReloadOnly, fmt.Errorf("failed to probe gateway spec.configId support: %w", err)
	}

	if supported {
		slog.Debug("gateway accepts spec.configId; per-pod config verification available",
			"namespace", namespace, "gateway", gatewayName, "tier", string(TierPerPodConfigID))
		return TierPerPodConfigID, nil
	}

	slog.Debug("gateway hot reload is on but spec.configId is unsupported; no per-pod handle available",
		"namespace", namespace, "gateway", gatewayName, "tier", string(TierHotReloadOnly),
		"minCFKVersion", MinCFKVersionForConfigID)
	return TierHotReloadOnly, nil
}

// hotReloadEnabled reads spec.hotReload.enabled from a Gateway CR.
//
// An absent hotReload block, or a block without enabled, is a clean false: that
// is the shape of every gateway CR this repo currently generates, and of every
// gateway predating the feature. A present-but-unreadable value is an error
// instead, because it leaves the one decision that must not be guessed —
// whether writing a configId would roll the pods — unresolved.
func hotReloadEnabled(crYAML []byte) (bool, error) {
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

// dryRunSupportsConfigID reports whether this cluster will actually store a
// spec.configId on this gateway.
//
// The question cannot be answered by inspecting the CR or the chart version.
// Structural CRD schemas prune undeclared fields silently, so a write that
// appears to succeed can leave nothing behind — and a pruned configId means
// every pod reports null forever, which would turn a healthy fence into a
// timeout. So the field is written under dryRun=All and read back off the
// server's response. This needs no CRD read permission, which is cluster-scoped
// and often not granted, and it is authoritative for the exact bytes that are
// about to be applied.
//
// A rejected apply is ambiguous — the CRD may refuse the unknown field, or the
// CR may simply be invalid — so it is disambiguated by re-applying the
// untouched CR. If that passes, configId was the problem and the cluster is
// merely Tier B; if it fails too, the CR is bad and the real apply would fail
// as well, so the error is surfaced now rather than after fencing has begun.
func dryRunSupportsConfigID(ctx context.Context, dynamicClient dynamic.Interface, namespace, gatewayName string, crYAML []byte) (bool, error) {
	probeID := generateConfigID(time.Now())

	probeYAML, err := injectConfigID(crYAML, probeID)
	if err != nil {
		return false, fmt.Errorf("failed to build a configId support probe: %w", err)
	}

	applied, applyErr := dryRunApplyGateway(ctx, dynamicClient, namespace, gatewayName, probeYAML)
	if applyErr == nil {
		got, found, err := unstructured.NestedString(applied.Object, "spec", "configId")
		switch {
		case err != nil:
			slog.Debug("gateway returned a non-string spec.configId; treating it as unsupported",
				"namespace", namespace, "gateway", gatewayName, "error", err)
			return false, nil
		case !found:
			slog.Debug("gateway pruned spec.configId on dry-run apply",
				"namespace", namespace, "gateway", gatewayName)
			return false, nil
		case got != probeID:
			// Not a usable handle: a field that comes back holding something
			// other than what was written cannot identify our revision.
			slog.Debug("gateway altered spec.configId on dry-run apply; treating it as unsupported",
				"namespace", namespace, "gateway", gatewayName, "sent", probeID, "returned", got)
			return false, nil
		}
		return true, nil
	}

	// A cancelled or expired probe says nothing about the CR, and retrying it
	// would report the cancellation as a CR validation failure.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, fmt.Errorf("configId support probe did not complete: %w", ctxErr)
	}

	if _, err := dryRunApplyGateway(ctx, dynamicClient, namespace, gatewayName, crYAML); err != nil {
		return false, fmt.Errorf("gateway CR failed a dry-run apply: %w", err)
	}

	slog.Debug("gateway rejected spec.configId but accepted the CR without it",
		"namespace", namespace, "gateway", gatewayName, "error", applyErr)
	return false, nil
}

// dryRunApplyGateway server-side applies a Gateway CR with dryRun=All, changing
// nothing and returning what the server would have stored.
//
// Deliberately mirrors ApplyGatewayYAML — same parse, same forced name and
// namespace, same field manager — rather than sharing its body, so this work
// stays in new files and does not textually collide with the in-flight
// refactor of that function.
func dryRunApplyGateway(ctx context.Context, dynamicClient dynamic.Interface, namespace, gatewayName string, crYAML []byte) (*unstructured.Unstructured, error) {
	var obj unstructured.Unstructured
	if err := yaml.Unmarshal(crYAML, &obj.Object); err != nil {
		return nil, fmt.Errorf("failed to parse gateway CR YAML: %w", err)
	}

	obj.SetName(gatewayName)
	obj.SetNamespace(namespace)

	applied, err := dynamicClient.Resource(gatewayConfigGVR()).Namespace(namespace).
		Apply(ctx, gatewayName, &obj, metav1.ApplyOptions{
			DryRun:       []string{metav1.DryRunAll},
			FieldManager: gatewayFieldManager,
			Force:        true,
		})
	if err != nil {
		return nil, err
	}
	if applied == nil {
		return nil, fmt.Errorf("dry-run apply of gateway %q returned no object", gatewayName)
	}
	return applied, nil
}
