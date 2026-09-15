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
