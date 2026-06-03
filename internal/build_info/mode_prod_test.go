//go:build !gov

package build_info

import "testing"

// TestProdEdition asserts the default (untagged) build reports the prod
// edition. Runs on every plain `go test` invocation.
func TestProdEdition(t *testing.T) {
	if Mode != "prod" {
		t.Errorf("Mode = %q, want %q for the default build", Mode, "prod")
	}
	if IsGov() {
		t.Errorf("IsGov() = true, want false for the default build")
	}
}
