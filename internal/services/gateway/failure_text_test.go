package gateway

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// These assertions pin the exact texts the AAO, TBM and d2s orchestrators
// printed inline before the wording moved here, one per helper and per FSM
// argument pair. Only AAO's "may still be holding the fenced config" changed,
// to "may still hold the fenced config", so one helper serves all three.

func TestFenceUnconfirmedTexts(t *testing.T) {
	assert.Equal(t, "fence could not be confirmed on every gateway pod", FenceUnconfirmedMessage)
	assert.Equal(t, "fence applied but not confirmed on every gateway pod", UnconfirmedFenceStopped)
}

func TestFenceKeptWarning(t *testing.T) {
	// AAO, TBM and d2s each pass their own rollbackForbiddenReason.
	assert.Equal(t,
		"Fence could not be confirmed on every gateway pod — keeping the fence: part of this batch is already promoted",
		FenceKeptWarning("part of this batch is already promoted"))
}

func TestUnconfirmedFenceWarning(t *testing.T) {
	timeout := fmt.Errorf("failed during fence: %w", errors.New("timed out"))
	rejected := fmt.Errorf("failed during fence: %w: %w", errors.New("fence could not be confirmed on every gateway pod"),
		&GatewayRejectedError{Reason: "InvalidSpec", Message: "route not found"})

	tests := []struct {
		name        string
		stepFailure error
		attempt     string
		want        string
	}{
		{"AAO timeout", timeout, "restore the initial gateway CR",
			"Fence could not be confirmed on every gateway pod — attempting to restore the initial gateway CR"},
		{"AAO rejection", rejected, "restore the initial gateway CR",
			"Confluent operator rejected the fenced gateway spec (reason: InvalidSpec) — it never took effect; attempting to restore the initial gateway CR"},
		{"TBM and d2s timeout", timeout, "remove it",
			"Fence could not be confirmed on every gateway pod — attempting to remove it"},
		{"TBM and d2s rejection", rejected, "remove it",
			"Confluent operator rejected the fenced gateway spec (reason: InvalidSpec) — it never took effect; attempting to remove it"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, UnconfirmedFenceWarning(tt.stepFailure, tt.attempt))
		})
	}
}

func TestWasRejected(t *testing.T) {
	assert.True(t, WasRejected(fmt.Errorf("wrapped: %w", &GatewayRejectedError{Reason: "InvalidSpec"})))
	assert.False(t, WasRejected(errors.New("timed out")))
	assert.False(t, WasRejected(nil))
}

func TestUnconfirmedFenceNotRestored(t *testing.T) {
	stepFailure := errors.New("failed during fence: timed out")
	restoreErr := errors.New("patch refused")

	tests := []struct {
		name      string
		did, held string
		want      string
	}{
		{"AAO", "restoring the initial gateway CR", "the fenced config",
			"failed during fence: timed out; additionally, restoring the initial gateway CR failed: patch refused; the gateway may still hold the fenced config on some pods, so inspect it before re-running"},
		{"TBM and d2s", "removing the fence", "the fenced rules",
			"failed during fence: timed out; additionally, removing the fence failed: patch refused; the gateway may still hold the fenced rules on some pods, so inspect it before re-running"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := UnconfirmedFenceNotRestored(stepFailure, restoreErr, tt.did, tt.held)
			assert.Equal(t, tt.want, err.Error())
			assert.ErrorIs(t, err, stepFailure, "the fence's own failure must survive")
			assert.ErrorIs(t, err, restoreErr, "the restore's failure must survive")
		})
	}
}

func TestSwitchUnconfirmedWarning(t *testing.T) {
	tests := []struct {
		name          string
		held, serving string
		want          string
	}{
		{"AAO and TBM", "the switchover", "the fenced route",
			"Switch could not be confirmed — the Gateway CR already holds the switchover, and the gateway pods may still serve the fenced route. A re-run checks the gateway pods before doing anything"},
		{"d2s", "the static route", "the fenced dynamic route",
			"Switch could not be confirmed — the Gateway CR already holds the static route, and the gateway pods may still serve the fenced dynamic route. A re-run checks the gateway pods before doing anything"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, SwitchUnconfirmedWarning(tt.held, tt.serving))
		})
	}
}
