package gateway

import (
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/goccy/go-yaml"
	corev1 "k8s.io/api/core/v1"
)

// Gateway hot-reload verification primitives.
//
// When a Gateway has spec.hotReload.enabled: true, a config change applies in
// place: no pod restart, no rollout, no Deployment generation bump. Every
// Kubernetes-side signal is therefore blind to whether the change landed, and
// status.observedGeneration in particular goes true seconds before the pods
// actually serve the new config (measured: ~1.2s after apply, 4.6s before CFK
// even published a rejection for a config no pod ever applied).
//
// The contract's remedy is a caller-supplied spec.configId plus the gateway's
// GET /config endpoint, polled per pod. This file holds the pure decision
// logic for that; the polling loop and its Kubernetes I/O live separately.

const (
	// GatewayConfigEndpointPath is the gateway's applied-config endpoint.
	GatewayConfigEndpointPath = "/config"

	// DefaultGatewayConfigPort is the gateway's dedicated HTTP endpoint port.
	// Configurable per the contract: it is not a declared containerPort and no
	// Service exposes it, so it is reached via the API-server pod proxy.
	DefaultGatewayConfigPort = "9180"

	// maxConfigIDLength mirrors the Gateway CRD's maxLength on spec.configId.
	maxConfigIDLength = 64

	// configIDPrefix makes a generated id attributable to kcp on sight.
	configIDPrefix = "kcp-"
)

// configIDPattern mirrors the Gateway CRD's pattern on spec.configId.
// Note this rejects the base64 padding characters + / = , so ids must be hex
// or unpadded base64url.
var configIDPattern = regexp.MustCompile(`^[a-zA-Z0-9._:-]+$`)

// configIDCounter guarantees uniqueness between two ids generated in the same
// second by the same process, which the timestamp alone cannot.
var configIDCounter atomic.Uint64

// podConfig is one pod's answer to GET /config.
//
// ConfigID is a pointer because the endpoint documents null for "never set",
// which is distinct from an empty string. Err records a probe that could not be
// completed at all — an unreachable pod must never read as a converged one.
type podConfig struct {
	Pod       string
	ConfigID  *string
	AppliedAt time.Time
	Err       error
}

// generateConfigID returns a fresh, CRD-valid spec.configId.
//
// kcp owns generation and verification end to end: CFK treats configId as an
// opaque pass-through, and no Gateway CR template in this repo carries one, so
// the value is always injected rather than read from user input.
func generateConfigID(now time.Time) string {
	seq := configIDCounter.Add(1)

	buf := make([]byte, 4)
	suffix := ""
	if _, err := cryptorand.Read(buf); err == nil {
		suffix = hex.EncodeToString(buf)
	} else {
		// crypto/rand is effectively infallible; the counter and timestamp
		// already carry in-process uniqueness, so degrade rather than fail.
		suffix = fmt.Sprintf("%08x", uint32(now.UnixNano()))
	}

	return fmt.Sprintf("%s%d-%06x-%s", configIDPrefix, now.Unix(), seq&0xffffff, suffix)
}

// GenerateConfigID returns a fresh, CRD-valid spec.configId for the caller to
// stamp on a CR and then verify against GET /config.
//
// Only stamp one when the verification tier allows it — see
// VerificationTier.InjectsConfigID.
func GenerateConfigID() string {
	return generateConfigID(time.Now())
}

// InjectConfigID sets spec.configId on a Gateway CR, returning the re-encoded
// document. See injectConfigID for the constraints and the hot-reload caveat.
func InjectConfigID(crYAML []byte, id string) ([]byte, error) {
	return injectConfigID(crYAML, id)
}

// injectConfigID sets spec.configId on a Gateway CR, returning the re-encoded
// document.
//
// The fenced and switchover CRs are user-supplied files applied byte-for-byte
// via server-side apply, and none of them carries a configId, so kcp must add
// one to have anything to verify against. Adding the field cannot trip CR
// validation: checkSpecsDiffer is a pure identity test and never compares the
// initial CR against the fenced one.
//
// Only call this when hot reload is actually active. With hotReload.enabled
// false, CFK folds configId into the pod-template config-revision-hash, so a
// bare bump rolls every gateway pod — measured as a full rolling restart.
func injectConfigID(crYAML []byte, id string) ([]byte, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("cannot inject an empty configId")
	}
	if !configIDPattern.MatchString(id) || len(id) > maxConfigIDLength {
		return nil, fmt.Errorf("configId %q violates the Gateway CRD constraints (max %d chars, pattern %s)",
			id, maxConfigIDLength, configIDPattern)
	}

	var obj map[string]any
	if err := yaml.Unmarshal(crYAML, &obj); err != nil {
		return nil, fmt.Errorf("failed to parse gateway CR YAML: %w", err)
	}

	spec, ok := obj["spec"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("gateway CR has no spec object to set configId on")
	}
	spec["configId"] = id

	out, err := yaml.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to re-encode gateway CR YAML: %w", err)
	}
	return out, nil
}

// eligiblePods returns the sorted names of the pods that must answer /config.
//
// The filter is load-bearing, not defensive. Evicted and terminating pods keep
// the app= label and a stale status.podIP: a 3-replica gateway was measured
// returning 6 pods, and the dead ones fail the probe in two different ways
// (connection refused, and HTTP 200 with an empty body). Polling them means a
// wait that can never converge, so a correct fence is reported as a failure.
func eligiblePods(pods []corev1.Pod) []string {
	var names []string
	for i := range pods {
		p := &pods[i]
		if p.DeletionTimestamp != nil {
			continue
		}
		if !isPodReady(p) {
			continue
		}
		names = append(names, p.Name)
	}
	sort.Strings(names)
	return names
}

// configResponse is the documented GET /config payload. Both fields are
// pointers so a JSON null is distinguishable from an absent or empty value.
type configResponse struct {
	ConfigID  *string `json:"configId"`
	AppliedAt *string `json:"appliedAt"`
}

// parseConfigResponse decodes one pod's GET /config body.
//
// Strict about what gates the decision (a non-empty, well-formed body) and
// lenient about appliedAt, which is corroborating only: appliedAt is the
// instant that pod applied its config, which for a freshly started pod is its
// own boot time rather than the time of any hot reload. configId is the field
// that proves which revision a pod is running.
func parseConfigResponse(raw []byte) (*string, time.Time, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, time.Time{}, fmt.Errorf("gateway returned an empty response body")
	}

	var resp configResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, time.Time{}, fmt.Errorf("failed to decode gateway config response: %w", err)
	}

	var appliedAt time.Time
	if resp.AppliedAt != nil && *resp.AppliedAt != "" {
		// Observed precision is nanoseconds, though the contract documents
		// milliseconds; RFC3339Nano accepts both, and no fraction at all.
		if parsed, err := time.Parse(time.RFC3339Nano, *resp.AppliedAt); err == nil {
			appliedAt = parsed
		}
	}

	return resp.ConfigID, appliedAt, nil
}

// converged reports whether every gateway pod is running the target config,
// and if not, a human reason carrying counts only — these strings reach the
// terminal, so they must not embed pod name lists.
//
// wantReplicas is the Deployment's ready-replica count, or 0 when it could not
// be read. Two guards matter beyond "all pods agree":
//
//   - An empty pod set makes "every pod reports the new configId" vacuously
//     true. A gateway scaled to zero would otherwise pass instantly.
//   - Mid-rollout the label selector returns old, new and failed pods at once,
//     so the eligible count drifts from the ready count. Converging on a subset
//     would report success while other pods still serve the old config.
func converged(results []podConfig, target string, wantReplicas int) (bool, string) {
	if len(results) == 0 {
		return false, "no eligible gateway pods found to verify"
	}

	if wantReplicas > 0 && len(results) != wantReplicas {
		return false, fmt.Sprintf("%d of %d gateway pods ready to verify", len(results), wantReplicas)
	}

	applied, failed := 0, 0
	for _, r := range results {
		switch {
		case r.Err != nil:
			failed++
		case r.ConfigID != nil && *r.ConfigID == target:
			applied++
		}
	}

	if applied == len(results) {
		return true, ""
	}

	reason := fmt.Sprintf("%d of %d gateway pods have applied the new config", applied, len(results))
	if failed > 0 {
		reason += fmt.Sprintf(" (%d unreachable)", failed)
	}
	return false, reason
}
