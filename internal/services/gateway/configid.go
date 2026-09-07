package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"

	"github.com/goccy/go-yaml"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	// maxConfigIDLength mirrors the CRD's maxLength on spec.configId.
	maxConfigIDLength = 64

	// configIDPrefix makes a configId appearing in a gateway log or a GET
	// /config response traceable back to kcp rather than to the user.
	configIDPrefix = "kcp-"
)

// configIDPattern mirrors the CRD-enforced pattern on spec.configId:
// alphanumerics plus '.', '_', ':' and '-'.
//
// Note what this excludes: the base64 padding characters '+', '/' and '=' are
// all rejected, so a base64-derived id cannot be sent as-is — it has to be
// re-encoded (hex, or base64url without padding). Hence the hex generator below.
var configIDPattern = regexp.MustCompile(`^[a-zA-Z0-9._:-]+$`)

// NewConfigID returns a fresh, opaque config revision id for one Gateway apply.
//
// The contract requires only that each id differs from the last value sent —
// UUID is a convention, not a requirement — so this uses 128 bits of hex behind
// a kcp- prefix: unique in practice, well inside the 64-character limit, and
// free of the characters the CRD pattern rejects.
func NewConfigID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate a gateway configId: %w", err)
	}

	return configIDPrefix + hex.EncodeToString(buf), nil
}

// validateConfigID checks an id against the CRD's constraints so a bad value
// fails locally with a clear message, rather than as an API server rejection
// part-way through a migration.
func validateConfigID(id string) error {
	if id == "" {
		return fmt.Errorf("gateway configId must not be empty")
	}
	if len(id) > maxConfigIDLength {
		return fmt.Errorf("gateway configId is %d characters, exceeding the CRD limit of %d", len(id), maxConfigIDLength)
	}
	if !configIDPattern.MatchString(id) {
		return fmt.Errorf("gateway configId does not match the CRD-enforced pattern %s", configIDPattern)
	}

	return nil
}

// prepareGatewayApply parses a Gateway CR and returns the object to hand to
// server-side apply: retargeted at the migration's gateway, and carrying
// configID at spec.configId when one is supplied.
//
// An empty configID leaves spec.configId untouched, which is what the
// VerifyRollout path needs — on a pre-hot-reload cluster the field is not in
// the CRD, and server-side apply rejects an undeclared field outright.
//
// Kept as a pure function so the whole of the object preparation is unit
// testable without a cluster or a fake client.
func prepareGatewayApply(yamlData []byte, namespace, gatewayName, configID string) (*unstructured.Unstructured, error) {
	if configID != "" {
		if err := validateConfigID(configID); err != nil {
			return nil, err
		}
	}

	var obj unstructured.Unstructured
	if err := yaml.Unmarshal(yamlData, &obj.Object); err != nil {
		return nil, fmt.Errorf("failed to parse gateway YAML: %w", err)
	}

	// Ensure metadata matches the expected resource.
	obj.SetName(gatewayName)
	obj.SetNamespace(namespace)

	if configID != "" {
		if err := injectConfigID(&obj, configID); err != nil {
			return nil, err
		}
	}

	return &obj, nil
}

// prepareConfigIDOnlyApply builds the minimal Gateway object for the
// hot-reload capability check: apiVersion, kind, name, namespace and
// spec.configId — nothing else. Applying this instead of the live spec is
// what lets the check run under its own field manager without ever taking
// ownership of a field the fence, switchover or unfence CR would need to
// prune (see hotReloadCheckFieldManager).
func prepareConfigIDOnlyApply(namespace, gatewayName, configID string) (*unstructured.Unstructured, error) {
	if err := validateConfigID(configID); err != nil {
		return nil, err
	}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": GatewayGroup + "/" + GatewayVersion,
		"kind":       GatewayKind,
		"metadata": map[string]any{
			"name":      gatewayName,
			"namespace": namespace,
		},
		"spec": map[string]any{
			gatewayConfigIDField: configID,
		},
	}}, nil
}

// injectConfigID sets spec.configId on obj, creating the spec block if needed.
//
// The map is written directly rather than via unstructured.SetNestedField:
// goccy decodes YAML integers as uint64, which is not one of the types
// runtime.DeepCopyJSON accepts, so the deep copy inside SetNestedField would
// panic on any CR carrying a number (replicas, ports, nodeIdRanges...). The
// apply path itself is unaffected — the dynamic client JSON-encodes the object
// instead of deep-copying it.
func injectConfigID(obj *unstructured.Unstructured, configID string) error {
	if obj.Object == nil {
		obj.Object = map[string]any{}
	}

	existing, found := obj.Object["spec"]
	if !found || existing == nil {
		obj.Object["spec"] = map[string]any{gatewayConfigIDField: configID}
		return nil
	}

	spec, ok := existing.(map[string]any)
	if !ok {
		return fmt.Errorf("gateway CR spec is %T, expected a mapping", existing)
	}
	spec[gatewayConfigIDField] = configID

	return nil
}
