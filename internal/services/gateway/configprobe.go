package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

const (
	// GatewayConfigEndpointPath is the gateway's config-revision endpoint. It
	// reports the last configId the pod successfully applied, which is how kcp
	// confirms a state transition landed without depending on whether CFK chose
	// a hot-reload or a pod roll.
	GatewayConfigEndpointPath = "/config"

	// DefaultGatewayConfigPort is the gateway's dedicated HTTP endpoint port.
	//
	// CFK declares neither a containerPort nor a Service for it — its
	// ContainerPorts() covers only the admin and route ports — so nothing fronts
	// this port. The port is configurable because the contract requires it to be.
	DefaultGatewayConfigPort = 9180
)

// ProbeOutcome classifies one pod's answer to GET /config.
//
// The distinctions matter because they call for different operator messages and
// different verdicts: an unreachable pod is an environment problem, a 404 is a
// gateway-version problem, and both are unrelated to whether a revision has
// propagated.
type ProbeOutcome string

const (
	// ProbeApplied means the pod reported a configId.
	ProbeApplied ProbeOutcome = "applied"

	// ProbeNeverSet means the endpoint answered but no revision has ever been
	// applied (configId null or absent). Expected before kcp's first apply.
	ProbeNeverSet ProbeOutcome = "never-set"

	// ProbeEndpointAbsent means the endpoint 404s: the gateway image predates
	// 1.3.0, which is the first release to serve /config. A capability signal.
	ProbeEndpointAbsent ProbeOutcome = "endpoint-absent"

	// ProbeUnreachable means the API server's pods/proxy request itself
	// failed: the pod is genuinely unreachable, the kcp identity lacks
	// pods/proxy RBAC, or the proxy backend errored. These are not reliably
	// distinguishable once the request goes through the API server, and
	// nothing downstream needs them to be. An environment signal.
	ProbeUnreachable ProbeOutcome = "unreachable"

	// ProbeUnexpected means the proxied request succeeded but the response
	// body could not be interpreted.
	ProbeUnexpected ProbeOutcome = "unexpected"
)

// ProbeResult is one pod's config-endpoint state.
type ProbeResult struct {
	// Addr is the pod:port probed, for diagnostics.
	Addr string

	// Outcome classifies the answer.
	Outcome ProbeOutcome

	// ConfigID is the revision the pod reports, empty unless Outcome is
	// ProbeApplied.
	ConfigID string

	// AppliedAt is when the pod applied that revision. Informational, and zero
	// whenever the gateway omitted it or sent something unparseable.
	AppliedAt time.Time

	// Err carries the underlying cause for ProbeUnreachable and ProbeUnexpected.
	Err error
}

// configEndpointResponse is the documented GET /config payload. Both fields are
// pointers so a JSON null is distinguishable from an empty string.
type configEndpointResponse struct {
	ConfigID  *string `json:"configId"`
	AppliedAt *string `json:"appliedAt"`
}

// probeGatewayConfig reads GET /config from one gateway pod through the
// Kubernetes API server's pods/proxy subresource, rather than dialing the
// pod's IP directly. This is what lets kcp verify hot-reload convergence
// without needing a network route into the pod CIDR: the kube client already
// authenticates to the API server, so nothing further needs to be reachable.
//
// The returned error is reserved for context cancellation — meaning abandon the
// whole wait. Every per-pod condition, including a proxy failure, comes back as
// a ProbeResult so a caller polling several pods can keep going and report
// precisely which pod is in which state.
func probeGatewayConfig(ctx context.Context, pods typedcorev1.PodInterface, podName string, port int) (ProbeResult, error) {
	if err := ctx.Err(); err != nil {
		return ProbeResult{}, err
	}

	addr := fmt.Sprintf("%s:%d", podName, port)
	result := ProbeResult{Addr: addr}

	body, err := pods.ProxyGet("http", podName, strconv.Itoa(port), GatewayConfigEndpointPath, nil).DoRaw(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ProbeResult{}, ctxErr
		}

		// A 404 is a capability signal, not an environment one: /config
		// arrived in gateway 1.3.0, so it means the image is too old rather
		// than unreachable. The API server relays the pod's own 404 through
		// DoRaw as a NotFound StatusError.
		if apierrors.IsNotFound(err) {
			result.Outcome = ProbeEndpointAbsent
			return result, nil
		}

		// Every other proxy failure — pod genuinely unreachable, RBAC denied,
		// the API server's own proxy backend erroring — folds into one
		// outcome: once the request goes through the API server, these are
		// not reliably distinguishable, and nothing downstream treats them
		// differently.
		result.Outcome = ProbeUnreachable
		result.Err = fmt.Errorf("failed to reach the gateway config endpoint at %s via the API server proxy: %w", addr, err)
		return result, nil
	}

	var payload configEndpointResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		result.Outcome = ProbeUnexpected
		result.Err = fmt.Errorf("failed to parse the gateway config endpoint response from %s: %w", addr, err)
		return result, nil
	}

	if payload.ConfigID == nil || *payload.ConfigID == "" {
		result.Outcome = ProbeNeverSet
		return result, nil
	}

	result.Outcome = ProbeApplied
	result.ConfigID = *payload.ConfigID

	// appliedAt is informational in the contract, so a value we cannot parse must
	// not invalidate the configId that verifies the transition.
	if payload.AppliedAt != nil {
		appliedAt, err := time.Parse(time.RFC3339, *payload.AppliedAt)
		if err != nil {
			slog.Debug("gateway config endpoint returned an unparseable appliedAt", "addr", addr, "error", err)
		} else {
			result.AppliedAt = appliedAt
		}
	}

	return result, nil
}

// GatewayPodEndpoint is one gateway pod the config endpoint can be probed on,
// addressed by name through the API server's proxy subresource.
type GatewayPodEndpoint struct {
	Name  string
	Ready bool
}

// listGatewayPodEndpoints returns the current gateway pods that have a pod IP.
//
// Not-ready pods are included rather than filtered: the caller needs the whole
// picture. During a roll a not-ready pod is expected and simply means "keep
// waiting", but a pod set with no ready members must never be mistaken for
// success. Pods with no IP assigned yet are skipped: that means not yet
// scheduled, regardless of how the endpoint is addressed once it is.
func listGatewayPodEndpoints(ctx context.Context, clientset kubernetes.Interface, namespace, gatewayName string) ([]GatewayPodEndpoint, error) {
	// CFK labels gateway pods app=<gateway-cr-name>, matching the other waits.
	labelSelector := fmt.Sprintf("app=%s", gatewayName)

	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list gateway pods: %w", err)
	}

	endpoints := make([]GatewayPodEndpoint, 0, len(pods.Items))
	for _, pod := range pods.Items {
		if pod.Status.PodIP == "" {
			slog.Debug("⏭️ skipping gateway pod with no IP assigned yet", "pod", pod.Name)
			continue
		}
		endpoints = append(endpoints, GatewayPodEndpoint{
			Name:  pod.Name,
			Ready: isPodReady(&pod),
		})
	}

	return endpoints, nil
}
