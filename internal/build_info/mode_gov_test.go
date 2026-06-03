//go:build gov

package build_info

import "testing"

// TestGovEdition asserts the `-tags=gov` build reports the gov edition. Runs
// only under `go test -tags=gov` (see `make verify-gov`).
func TestGovEdition(t *testing.T) {
	if Mode != "gov" {
		t.Errorf("Mode = %q, want %q for the gov build", Mode, "gov")
	}
	if !IsGov() {
		t.Errorf("IsGov() = false, want true for the gov build")
	}
}
