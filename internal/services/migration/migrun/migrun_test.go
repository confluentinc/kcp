package migrun

import "testing"

func TestSentinelsSurviveTheMove(t *testing.T) {
	if got, want := ErrFenceUnconfirmed.Error(), "fence could not be confirmed on every gateway pod"; got != want {
		t.Errorf("ErrFenceUnconfirmed = %q, want %q", got, want)
	}
	if got, want := ErrSwitchUnconfirmed.Error(), "the switchover reached the Gateway CR but could not be confirmed"; got != want {
		t.Errorf("ErrSwitchUnconfirmed = %q, want %q", got, want)
	}
	for got, want := range map[string]string{
		RollbackPathAbortFence:       "abort_fence",
		RollbackPathEarlierRunFence:  "earlier_run_fence",
		RollbackPathUnconfirmedFence: "unconfirmed_fence",
	} {
		if got != want {
			t.Errorf("rollback path = %q, want %q", got, want)
		}
	}
}
