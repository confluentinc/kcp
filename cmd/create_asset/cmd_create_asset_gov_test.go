//go:build gov

package create_asset

import (
	"sort"
	"testing"
)

// TestGovEditionStripsThreeCommands asserts the gov build excludes exactly
// target-infra, migration-infra, and migrate-connectors while keeping the other
// five subcommands. Runs only under `go test -tags=gov` (see `make
// verify-gov`). Covers AE1.
func TestGovEditionStripsThreeCommands(t *testing.T) {
	want := []string{
		"bastion-host",
		"migrate-acls",
		"migrate-schemas",
		"migrate-topics",
		"reverse-proxy",
	}
	excluded := []string{"target-infra", "migration-infra", "migrate-connectors"}

	got := subcommandNames(t)
	sort.Strings(got)

	if len(got) != len(want) {
		t.Fatalf("gov build: got %d subcommands %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("gov subcommand[%d] = %q, want %q (full set: %v)", i, got[i], want[i], got)
		}
	}

	present := make(map[string]bool, len(got))
	for _, n := range got {
		present[n] = true
	}
	for _, n := range excluded {
		if present[n] {
			t.Errorf("gov build must not contain %q", n)
		}
	}
}
