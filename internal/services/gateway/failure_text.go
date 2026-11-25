package gateway

import (
	"errors"
	"fmt"
)

// The texts below describe a fence or switch failure in terms of the gateway's
// pods and the Confluent operator. They live here so the migration FSMs, which
// own only intent, never spell out how the gateway applies a change.

// FenceUnconfirmedMessage is the text of a fence whose CR reached the cluster
// but whose effect on the serving pods was never confirmed.
const FenceUnconfirmedMessage = "fence could not be confirmed on every gateway pod"

// UnconfirmedFenceStopped says where a run stopped when the CR moved before the
// compensation for an unconfirmed fence could put it right.
const UnconfirmedFenceStopped = "fence applied but not confirmed on every gateway pod"

// FenceKeptWarning is the warning for an unconfirmed fence that is kept
// because a rollback is forbidden; reason says why.
func FenceKeptWarning(reason string) string {
	return "Fence could not be confirmed on every gateway pod — keeping the fence: " + reason
}

// WasRejected reports whether err is, or wraps, a *GatewayRejectedError: the
// operator explicitly refused the applied spec.
func WasRejected(err error) bool {
	var rejected *GatewayRejectedError
	return errors.As(err, &rejected)
}

// UnconfirmedFenceWarning is the warning announcing the compensation for an
// unconfirmed fence. A definite rejection is told as such, with the operator's
// own reason; anything else is the ambiguous unconfirmed case. attempt names
// the compensation ("remove it").
func UnconfirmedFenceWarning(stepFailure error, attempt string) string {
	var rejected *GatewayRejectedError
	if errors.As(stepFailure, &rejected) {
		return "Confluent operator rejected the fenced gateway spec (reason: " + rejected.Reason + ") — it never took effect; attempting to " + attempt
	}
	return "Fence could not be confirmed on every gateway pod — attempting to " + attempt
}

// UnconfirmedFenceNotRestored joins a failed compensation for an unconfirmed
// fence onto the fence's own failure. did names the compensation ("removing
// the fence") and held what the pods may still hold ("the fenced rules").
func UnconfirmedFenceNotRestored(stepFailure, err error, did, held string) error {
	return fmt.Errorf("%w; additionally, %s failed: %w; the gateway may still hold %s on some pods, so inspect it before re-running", stepFailure, did, err, held)
}

// SwitchUnconfirmedWarning is the warning for a switch whose patch reached the
// CR but could not be confirmed: held is what the CR now holds, serving what
// the pods may still serve.
func SwitchUnconfirmedWarning(held, serving string) string {
	return "Switch could not be confirmed — the Gateway CR already holds " + held + ", and the gateway pods may still serve " + serving + ". A re-run checks the gateway pods before doing anything"
}
