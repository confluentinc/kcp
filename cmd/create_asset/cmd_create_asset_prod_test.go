//go:build !gov

package create_asset

import (
	"sort"
	"testing"
)

// TestDefaultEditionHasAllSubcommands asserts the default (prod) build exposes
// all eight create-asset subcommands. Covers AE2.
func TestDefaultEditionHasAllSubcommands(t *testing.T) {
	want := []string{
		"bastion-host",
		"migrate-acls",
		"migrate-connectors",
		"migrate-schemas",
		"migrate-topics",
		"migration-infra",
		"reverse-proxy",
		"target-infra",
	}

	got := subcommandNames(t)
	sort.Strings(got)

	if len(got) != len(want) {
		t.Fatalf("got %d subcommands %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("subcommand[%d] = %q, want %q (full set: %v)", i, got[i], want[i], got)
		}
	}
}
