package reconcile

import (
	"github.com/goccy/go-yaml"
)

// RulesTree is the route's `rules` subtree held as a fidelity-preserving tree
// (map[string]any / []any). Lists keep their order (first-match-wins is
// load-bearing) and every field passes through untouched on serialize.
type RulesTree struct {
	root map[string]any
}

func ParseRules(raw map[string]any) (*RulesTree, error) {
	if raw == nil {
		raw = map[string]any{}
	}
	return &RulesTree{root: raw}, nil
}

func mapField(m map[string]any, k string) (map[string]any, bool) {
	v, ok := m[k].(map[string]any)
	return v, ok
}

func sliceField(m map[string]any, k string) ([]any, bool) {
	v, ok := m[k].([]any)
	return v, ok
}

func stringField(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func stringList(m map[string]any, k string) []string {
	raw, ok := m[k].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func (rt *RulesTree) routing() map[string]any {
	r, _ := mapField(rt.root, "routing")
	return r
}

// Project builds the typed read-only view the core reasons over.
func (rt *RulesTree) Project() RouteView {
	v := RouteView{}
	routing := rt.routing()
	if routing == nil {
		return v
	}
	v.DefaultDomain = stringField(routing, "default")
	conds, _ := sliceField(routing, "conditions")
	for _, raw := range conds {
		c, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		v.Conditions = append(v.Conditions, Condition{
			Topics:        stringList(c, "topics"),
			TopicPatterns: stringList(c, "topicPatterns"),
			Domain:        stringField(c, "streamingDomain"),
		})
	}
	return v
}

func (rt *RulesTree) CoordinationGroup() string {
	routing := rt.routing()
	if routing == nil {
		return ""
	}
	coord, ok := mapField(routing, "coordination")
	if !ok {
		return ""
	}
	return stringField(coord, "group")
}

// Clone deep-copies the tree so fence and switchover mutations are independent.
func (rt *RulesTree) Clone() *RulesTree {
	b, _ := yaml.Marshal(rt.root)
	var cp map[string]any
	_ = yaml.Unmarshal(b, &cp)
	return &RulesTree{root: cp}
}

func (rt *RulesTree) ensureRouting() map[string]any {
	if rt.root["routing"] == nil {
		rt.root["routing"] = map[string]any{}
	}
	r, _ := mapField(rt.root, "routing")
	return r
}

func asAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// PrependFence adds a batch fence entry (all traffic, exact names) at the head
// of rules.fencing, preserving the operator's existing entries.
func (rt *RulesTree) PrependFence(topics []string) {
	entry := map[string]any{"topics": asAnySlice(topics)}
	existing, _ := sliceField(rt.root, "fencing")
	rt.root["fencing"] = append([]any{entry}, existing...)
}

// PrependCondition adds an exact-name routing condition at the head of
// rules.routing.conditions, preserving the operator's existing conditions.
func (rt *RulesTree) PrependCondition(topics []string, domain string) {
	routing := rt.ensureRouting()
	entry := map[string]any{"topics": asAnySlice(topics), "streamingDomain": domain}
	existing, _ := sliceField(routing, "conditions")
	routing["conditions"] = append([]any{entry}, existing...)
}

// Serialize renders the artifact as the whole hot-reloadable `rules` subtree,
// wrapped under a top-level `rules:` key — the KCP-patched subtree the gateway
// consumes (see Gateway Dynamic Routing Configuration). The caller applies it as
// a whole-subtree replacement of the route's `rules`, which is also what reverts
// the batch fence at switchover.
func (rt *RulesTree) Serialize() ([]byte, error) {
	return yaml.Marshal(map[string]any{"rules": rt.root})
}
