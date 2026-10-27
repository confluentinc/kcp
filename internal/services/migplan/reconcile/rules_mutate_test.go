package reconcile

import (
	"strings"
	"testing"
)

func mustClone(t *testing.T, rt *RulesTree) *RulesTree {
	t.Helper()
	c, err := rt.Clone()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPrependFencePreservesExisting(t *testing.T) {
	rt := mustClone(t, mustTree(t))
	rt.PrependFence([]string{"a", "b"})
	out, err := rt.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// our entry is first, existing operator entries survive
	if !strings.Contains(s, "legacy-audit") || !strings.Contains(s, "TRANSACTION") {
		t.Error("operator fencing entries must be preserved")
	}
	first := strings.Index(s, "a")
	audit := strings.Index(s, "legacy-audit")
	if first < 0 || audit < 0 || first > audit {
		t.Error("our fence entry must be prepended ahead of operator entries")
	}
}

// TestPrependFenceSetsBlocked proves the batch fence entry declares
// blocked: true — the CRD-required field on rules.fencing[] entries. A
// caller applying FenceYAML/SwitchoverYAML straight to a live Gateway CR
// (e.g. the TBM fence transition) must never need to patch this in itself;
// migplan.Reconcile's own artifact must already satisfy the schema.
func TestPrependFenceSetsBlocked(t *testing.T) {
	rt := mustClone(t, mustTree(t))
	rt.PrependFence([]string{"a", "b"})
	fencing, ok := sliceField(rt.root, "fencing")
	if !ok || len(fencing) == 0 {
		t.Fatal("expected a fencing entry")
	}
	entry, ok := fencing[0].(map[string]any)
	if !ok {
		t.Fatalf("expected the prepended entry to be a map, got %T", fencing[0])
	}
	if blocked, ok := entry["blocked"].(bool); !ok || !blocked {
		t.Errorf("expected the batch fence entry to declare blocked: true, got %v", entry["blocked"])
	}
}

func TestPrependConditionWins(t *testing.T) {
	rt := mustClone(t, mustTree(t))
	rt.PrependCondition([]string{"team-a.orders"}, "cc")
	v := rt.Project()
	if v.Conditions[0].Domain != "cc" || v.Conditions[0].Topics[0] != "team-a.orders" {
		t.Fatalf("our exact condition must be first: %+v", v.Conditions[0])
	}
	// operator's team-a.* pattern is preserved below, still routing others to msk
	if OwnerRouteFromView(v, "team-a.orders") != "cc" {
		t.Error("migrated topic must resolve to cc")
	}
	if OwnerRouteFromView(v, "team-a.payments") != "msk" {
		t.Error("un-migrated sibling must still resolve to msk via the preserved pattern")
	}
}

func TestCloneIsolation(t *testing.T) {
	base := mustTree(t)
	c := mustClone(t, base)
	c.PrependCondition([]string{"x"}, "cc")
	if len(base.Project().Conditions) != 2 {
		t.Error("mutating a clone must not affect the base tree")
	}
}

// TestCloneReturnsErrorOnUnmarshalableValue proves Clone surfaces a marshal
// failure as an error rather than silently returning a corrupt (nil-root)
// copy — a caller writing into that copy via PrependFence/PrependCondition
// would otherwise panic on assigning into a nil map.
func TestCloneReturnsErrorOnUnmarshalableValue(t *testing.T) {
	rt := &RulesTree{root: map[string]any{"bad": make(chan int)}}
	if _, err := rt.Clone(); err == nil {
		t.Fatal("expected an error cloning a tree with an unmarshalable value")
	}
}
