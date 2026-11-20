package migrun

import (
	"errors"

	"github.com/confluentinc/kcp/internal/services/gateway"
)

// ErrFenceUnconfirmed marks a fence whose CR reached the cluster but whose
// effect on the gateway was never confirmed. The orchestrator catches it to
// restore the initial CR — see the AAO orchestrator's restoreAfterUnconfirmedFence for why that is the
// right compensation even when the fence appears not to have landed at all.
var ErrFenceUnconfirmed = errors.New(gateway.FenceUnconfirmedMessage)

// ErrSwitchUnconfirmed marks a switchover whose patch reached the Gateway CR
// but could not be confirmed (the gateway rejected or never accepted it, the
// stored revision could not be read back, or the gateway never reported it). The
// CR already holds the switchover while the gateway may still serve the fenced
// route. Every FSM (AAO, TBM, d2s) marks its switch with it, and none rolls
// back: a re-run's reconcile checks the live gateway before doing anything.
var ErrSwitchUnconfirmed = errors.New("the switchover reached the Gateway CR but could not be confirmed")
