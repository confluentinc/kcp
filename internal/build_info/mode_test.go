package build_info

import "testing"

// TestModeIsKnownEdition guards against typos in either tagged Mode constant:
// the edition must always be exactly one of the two known values, regardless of
// which build tag this test binary was compiled with.
func TestModeIsKnownEdition(t *testing.T) {
	if Mode != "prod" && Mode != "gov" {
		t.Fatalf("Mode = %q, want one of {\"prod\", \"gov\"}", Mode)
	}
}

// TestIsGovMatchesMode verifies IsGov is consistent with Mode under whichever
// build tag this test is compiled with. The default `go test` run exercises the
// prod branch; the `-tags=gov` run (see `make verify-gov`) exercises the gov
// branch via mode_gov_test.go.
func TestIsGovMatchesMode(t *testing.T) {
	if got, want := IsGov(), Mode == "gov"; got != want {
		t.Fatalf("IsGov() = %v, but Mode = %q", got, Mode)
	}
}
