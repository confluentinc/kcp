package gateway

import (
	"errors"
	"fmt"
)

// ErrGatewayChanged reports that the Gateway CR's metadata.generation is no
// longer the value kcp expected, so kcp declined to write. Match it with
// errors.Is; errors.As on *GenerationChangedError gives the two values.
var ErrGatewayChanged = errors.New("the gateway CR changed since kcp last knew it")

// GenerationChangedError carries the generation kcp expected and the one it
// observed when a guarded patch was refused or lost a race. It states what was
// seen, not who changed the CR or why.
type GenerationChangedError struct {
	Namespace, Gateway string
	Expected, Observed int64
}

func (e *GenerationChangedError) Error() string {
	return fmt.Sprintf("gateway CR %s/%s changed since kcp last knew it: metadata.generation is %d, kcp expected %d",
		e.Namespace, e.Gateway, e.Observed, e.Expected)
}

// Is makes errors.Is(err, ErrGatewayChanged) true for every GenerationChangedError.
func (e *GenerationChangedError) Is(target error) bool { return target == ErrGatewayChanged }

// Summary is the terminal line for a stop caused by this error. stopped says
// where the run stopped (for example "fence applied, not promoted").
func (e *GenerationChangedError) Summary(stopped string) string {
	return fmt.Sprintf("The gateway CR changed since kcp last knew it: metadata.generation is %d, kcp expected %d. Stopping with no gateway write — %s.",
		e.Observed, e.Expected, stopped)
}

// InspectCommands returns the kubectl commands that show the Gateway CR and its pods.
func InspectCommands(namespace, gatewayName string) string {
	return fmt.Sprintf("kubectl -n %s get gateway %s -o yaml; kubectl -n %s get pods -l app=%s",
		namespace, gatewayName, namespace, gatewayName)
}

// generationTestOp is the JSON Patch op that makes a write fail unless the CR's
// metadata.generation is still the expected value.
func generationTestOp(expected int64) jsonPatchOp {
	return jsonPatchOp{Op: "test", Path: "/metadata/generation", Value: expected}
}
