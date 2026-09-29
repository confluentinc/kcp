package reconcile

import (
	"testing"

	"github.com/goccy/go-yaml"
)

const sampleRules = `
fencing:
  - trafficType: TRANSACTION
  - trafficType: PRODUCE
    topics: ["legacy-audit"]
routing:
  coordination: { group: msk }
  conditions:
    - topicPatterns: ["team-a.*"]
      streamingDomain: msk
    - topics: ["orders-legacy"]
      streamingDomain: cc
  default: msk
`

func mustTree(t *testing.T) *RulesTree {
	t.Helper()
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(sampleRules), &raw); err != nil {
		t.Fatal(err)
	}
	rt, err := ParseRules(raw)
	if err != nil {
		t.Fatal(err)
	}
	return rt
}

func TestProject(t *testing.T) {
	v := mustTree(t).Project()
	if v.DefaultDomain != "msk" {
		t.Errorf("default = %q, want msk", v.DefaultDomain)
	}
	if len(v.Conditions) != 2 {
		t.Fatalf("conditions = %d, want 2", len(v.Conditions))
	}
	if v.Conditions[0].Domain != "msk" || v.Conditions[0].TopicPatterns[0] != "team-a.*" {
		t.Errorf("condition 0 mis-projected: %+v", v.Conditions[0])
	}
	if v.Conditions[1].Domain != "cc" || v.Conditions[1].Topics[0] != "orders-legacy" {
		t.Errorf("condition 1 mis-projected: %+v", v.Conditions[1])
	}
}

func TestCoordinationGroup(t *testing.T) {
	if g := mustTree(t).CoordinationGroup(); g != "msk" {
		t.Errorf("coordination.group = %q, want msk", g)
	}
}

func fencingEntries(t *testing.T, rt *RulesTree) []map[string]any {
	t.Helper()
	raw, _ := sliceField(rt.root, "fencing")
	out := make([]map[string]any, 0, len(raw))
	for _, e := range raw {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func TestPrependFenceIdempotent(t *testing.T) {
	rt, err := ParseRules(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	rt.PrependFence([]string{"t1", "t2"})
	rt.PrependFence([]string{"t2", "t1"}) // same set, different order — must NOT double
	fe := fencingEntries(t, rt)
	if len(fe) != 1 {
		t.Fatalf("fencing entries = %d, want 1 (idempotent): %+v", len(fe), fe)
	}
	if b, _ := fe[0]["blocked"].(bool); !b {
		t.Fatalf("fence entry not blocked:true: %+v", fe[0])
	}
	if !sameStringSet(stringList(fe[0], "topics"), []string{"t1", "t2"}) {
		t.Fatalf("fence topics = %v, want {t1,t2}", stringList(fe[0], "topics"))
	}
}

func TestPrependFencePreservesOperatorEntries(t *testing.T) {
	rt := mustTree(t) // has fencing: [{trafficType: TRANSACTION}, {trafficType: PRODUCE, topics:[legacy-audit]}]
	before := len(fencingEntries(t, rt))
	rt.PrependFence([]string{"orders"})
	rt.PrependFence([]string{"orders"}) // twice
	fe := fencingEntries(t, rt)
	// exactly one new kcp entry added on top of the two operator entries, no dupes
	if len(fe) != before+1 {
		t.Fatalf("fencing entries = %d, want %d (operators preserved + 1 kcp): %+v", len(fe), before+1, fe)
	}
	// head is the kcp entry; operator entries still present untouched
	if !sameStringSet(stringList(fe[0], "topics"), []string{"orders"}) {
		t.Fatalf("head entry = %+v, want the kcp {orders} fence", fe[0])
	}
	if fe[1]["trafficType"] != "TRANSACTION" {
		t.Fatalf("operator entry 0 lost/reordered: %+v", fe[1])
	}
}

func TestDropFenceRemovesKcpEntryPreservesOperator(t *testing.T) {
	rt := mustTree(t) // 2 operator fences
	rt.PrependFence([]string{"orders"})
	before := len(fencingEntries(t, rt)) // 2 operator + 1 kcp

	rt.DropFence([]string{"orders"})

	fe := fencingEntries(t, rt)
	if len(fe) != before-1 {
		t.Fatalf("after DropFence: %d entries, want %d (kcp's dropped, operators kept): %+v", len(fe), before-1, fe)
	}
	for _, e := range fe {
		if isKcpFenceFor(e, []string{"orders"}) {
			t.Fatalf("kcp's {orders} fence must be dropped, still present: %+v", e)
		}
	}
	foundOperator := false
	for _, e := range fe {
		if e["trafficType"] == "TRANSACTION" {
			foundOperator = true
		}
	}
	if !foundOperator {
		t.Fatalf("operator TRANSACTION fence must survive DropFence: %+v", fe)
	}
}

func TestDropFenceAbsentKcpEntryIsNoop(t *testing.T) {
	rt := mustTree(t) // operator fences only, no kcp fence
	before := len(fencingEntries(t, rt))
	rt.DropFence([]string{"orders"})
	if got := len(fencingEntries(t, rt)); got != before {
		t.Fatalf("DropFence with no matching kcp fence must be a no-op: %d != %d", got, before)
	}
}

func conditionEntries(t *testing.T, rt *RulesTree) []map[string]any {
	t.Helper()
	routing, _ := mapField(rt.root, "routing")
	raw, _ := sliceField(routing, "conditions")
	out := make([]map[string]any, 0, len(raw))
	for _, c := range raw {
		if m, ok := c.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func TestPrependConditionIdempotent(t *testing.T) {
	rt, err := ParseRules(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	rt.PrependCondition([]string{"t1", "t2"}, "cc")
	rt.PrependCondition([]string{"t2", "t1"}, "cc") // same set + domain — must NOT double
	ce := conditionEntries(t, rt)
	if len(ce) != 1 {
		t.Fatalf("conditions = %d, want 1 (idempotent): %+v", len(ce), ce)
	}
	if ce[0]["streamingDomain"] != "cc" || !sameStringSet(stringList(ce[0], "topics"), []string{"t1", "t2"}) {
		t.Fatalf("condition = %+v, want {t1,t2}->cc", ce[0])
	}
}

func TestPrependConditionPreservesDifferentDomain(t *testing.T) {
	rt, err := ParseRules(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	rt.PrependCondition([]string{"orders"}, "msk") // an operator-style condition to a DIFFERENT domain
	rt.PrependCondition([]string{"orders"}, "cc")  // kcp's switchover to the target — must NOT drop the msk one
	ce := conditionEntries(t, rt)
	if len(ce) != 2 {
		t.Fatalf("conditions = %d, want 2 (different domains coexist): %+v", len(ce), ce)
	}
	if ce[0]["streamingDomain"] != "cc" { // fresh kcp entry at head
		t.Fatalf("head = %+v, want orders->cc", ce[0])
	}
	if ce[1]["streamingDomain"] != "msk" {
		t.Fatalf("preserved entry = %+v, want orders->msk", ce[1])
	}
}
