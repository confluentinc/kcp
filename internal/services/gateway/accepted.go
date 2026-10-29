package gateway

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// CheckAccepted returns a *GatewayRejectedError when the operator refused the stored spec.
func CheckAccepted(gatewayYAML []byte, gatewayName string) error {
	jsonBytes, err := yaml.YAMLToJSON(gatewayYAML)
	if err != nil {
		return fmt.Errorf("failed to parse gateway %q: %w", gatewayName, err)
	}
	gw := &unstructured.Unstructured{}
	if err := gw.UnmarshalJSON(jsonBytes); err != nil {
		return fmt.Errorf("failed to decode gateway %q: %w", gatewayName, err)
	}

	generation := gw.GetGeneration()
	observed, _, err := unstructured.NestedInt64(gw.Object, "status", "observedGeneration")
	if err != nil {
		return fmt.Errorf("failed to read gateway status.observedGeneration: %w", err)
	}
	if observed >= generation {
		return nil
	}

	rejection, err := findGatewayRejection(gw, gatewayName, generation, observed, time.Time{})
	if err != nil {
		return err
	}
	if rejection != nil {
		return rejection
	}
	return fmt.Errorf("gateway %q: the operator has not observed generation %d yet (observedGeneration: %d)",
		gatewayName, generation, observed)
}
