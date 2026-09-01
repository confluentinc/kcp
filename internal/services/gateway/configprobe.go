package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
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
	// this port and kcp has to dial pod IPs directly. The port is configurable
	// because the contract requires it to be.
	DefaultGatewayConfigPort = 9180

	// maxConfigResponseBytes caps the response body read. The documented payload
	// is two short fields; anything larger means we are not talking to the
	// endpoint we think we are, and should not be buffered unbounded.
	maxConfigResponseBytes = 64 << 10
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

	// ProbeUnreachable means the pod could not be dialled at all — the pod CIDR
	// is not routable from where kcp is running. An environment signal.
	ProbeUnreachable ProbeOutcome = "unreachable"

	// ProbeUnexpected means the endpoint answered with something we cannot
	// interpret: a non-200/404 status, a redirect, or an unparseable body.
	ProbeUnexpected ProbeOutcome = "unexpected"
)

// ProbeResult is one pod's config-endpoint state.
type ProbeResult struct {
	// Addr is the host:port probed, for diagnostics.
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

// newConfigProbeClient builds the HTTP client used to poll /config.
//
// Redirects are deliberately not followed. A 30x from /config is not a valid
// response, and following one would let a misconfigured proxy or ingress return
// some other gateway's configId — which, since a matching configId is exactly
// what kcp treats as proof a transition landed, would manufacture a false
// success. Returning ErrUseLastResponse surfaces the 30x as ProbeUnexpected.
func newConfigProbeClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// gatewayConfigAddr builds the host:port for a pod's config endpoint.
// net.JoinHostPort is required rather than string concatenation: an IPv6 pod IP
// has to be bracketed or the resulting URL will not parse.
func gatewayConfigAddr(ip string, port int) string {
	return net.JoinHostPort(ip, strconv.Itoa(port))
}

// probeGatewayConfig reads GET /config from one gateway pod.
//
// The returned error is reserved for context cancellation — meaning abandon the
// whole wait. Every per-pod condition, including an unreachable pod, comes back
// as a ProbeResult so a caller polling several pods can keep going and report
// precisely which pod is in which state.
func probeGatewayConfig(ctx context.Context, client *http.Client, addr string) (ProbeResult, error) {
	if err := ctx.Err(); err != nil {
		return ProbeResult{}, err
	}

	result := ProbeResult{Addr: addr}
	url := "http://" + addr + GatewayConfigEndpointPath

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("failed to build config endpoint request for %s: %w", addr, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		// Distinguish "stop everything" from "this pod is not answering".
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ProbeResult{}, ctxErr
		}
		result.Outcome = ProbeUnreachable
		result.Err = fmt.Errorf("failed to reach the gateway config endpoint at %s: %w", addr, err)
		return result, nil
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		// fall through to body parsing below
	case http.StatusNotFound:
		result.Outcome = ProbeEndpointAbsent
		return result, nil
	default:
		result.Outcome = ProbeUnexpected
		result.Err = fmt.Errorf("gateway config endpoint at %s returned HTTP %d", addr, resp.StatusCode)
		return result, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxConfigResponseBytes))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ProbeResult{}, ctxErr
		}
		result.Outcome = ProbeUnexpected
		result.Err = fmt.Errorf("failed to read the gateway config endpoint response from %s: %w", addr, err)
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

// GatewayPodEndpoint is one gateway pod's dialable config endpoint.
type GatewayPodEndpoint struct {
	Name  string
	IP    string
	Ready bool
}

// listGatewayPodEndpoints returns the current gateway pods that have a pod IP.
//
// Not-ready pods are included rather than filtered: the caller needs the whole
// picture. During a roll a not-ready pod is expected and simply means "keep
// waiting", but a pod set with no ready members must never be mistaken for
// success. Pods with no IP assigned yet are skipped — there is nothing to dial.
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
			IP:    pod.Status.PodIP,
			Ready: isPodReady(&pod),
		})
	}

	return endpoints, nil
}
