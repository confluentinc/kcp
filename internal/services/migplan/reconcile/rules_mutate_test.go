package reconcile

import (
	"strings"
	"testing"
)

func TestPrependFencePreservesExisting(t *testing.T) {
	rt := mustTree(t).Clone()
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

func TestPrependConditionWins(t *testing.T) {
	rt := mustTree(t).Clone()
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
	c := base.Clone()
	c.PrependCondition([]string{"x"}, "cc")
	if len(base.Project().Conditions) != 2 {
		t.Error("mutating a clone must not affect the base tree")
	}
}
