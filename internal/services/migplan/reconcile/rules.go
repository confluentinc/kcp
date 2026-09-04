package reconcile

import "fmt"

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

var _ = fmt.Sprintf // keep fmt import available for later error messages
