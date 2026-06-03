package create_asset

import (
	"sort"
	"testing"
)

// subcommandNames returns the Name() of each create-asset subcommand present in
// the current edition's command tree.
func subcommandNames(t *testing.T) []string {
	t.Helper()
	cmd := NewCreateAssetCmd()
	var names []string
	for _, c := range cmd.Commands() {
		names = append(names, c.Name())
	}
	return names
}

// TestSubcommandOrderingIsDeterministic guards against init()-order flakiness:
// the registry sorts by Use, so repeated builds must yield the same order. Runs
// under both editions.
func TestSubcommandOrderingIsDeterministic(t *testing.T) {
	first := subcommandNames(t)
	for i := 0; i < 5; i++ {
		next := subcommandNames(t)
		if len(next) != len(first) {
			t.Fatalf("run %d: length changed %d -> %d", i, len(first), len(next))
		}
		for j := range first {
			if next[j] != first[j] {
				t.Fatalf("run %d: order changed at %d: %q != %q", i, j, next[j], first[j])
			}
		}
	}
	if !sort.SliceIsSorted(first, func(a, b int) bool { return first[a] < first[b] }) {
		t.Errorf("subcommands not in sorted order: %v", first)
	}
}
