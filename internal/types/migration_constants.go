package types

// FSM State constants
const (
	StateUninitialized = "uninitialized"
	StateInitialized   = "initialized"
	StateLagsOk        = "lags_ok"
	StateFenced        = "fenced"
	StatePromoting     = "promoting"
	StatePromoted      = "promoted"
	StateSwitched      = "switched"
)

// FSM Event constants
const (
	EventInitialize                 = "initialize"
	EventWaitForLags                = "wait_for_lags"
	EventFence                      = "fence"
	EventPromote                    = "promote"
	EventWaitForPromotionCompletion = "wait_for_promotion_completion"
	EventSwitch                     = "switch"
)
