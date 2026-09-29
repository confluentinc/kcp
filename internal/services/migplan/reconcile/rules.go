package reconcile

import (
	"fmt"

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

// Clone deep-copies the tree (via a marshal/unmarshal round trip) so fence and
// switchover mutations are independent. Returns an error rather than a
// silently corrupt copy if the tree contains something that cannot be
// marshaled — callers must not proceed to mutate a clone built from a
// discarded error, since a nil/empty root would panic on the first write.
func (rt *RulesTree) Clone() (*RulesTree, error) {
	b, err := yaml.Marshal(rt.root)
	if err != nil {
		return nil, fmt.Errorf("marshaling rules tree to clone: %w", err)
	}
	var cp map[string]any
	if err := yaml.Unmarshal(b, &cp); err != nil {
		return nil, fmt.Errorf("unmarshaling cloned rules tree: %w", err)
	}
	return &RulesTree{root: cp}, nil
}

func (rt *RulesTree) ensureRouting() map[string]any {
	// Replace routing when it is absent OR present but not a map[string]any (a
	// scalar/list/map[any]any from hand-written YAML): mapField returns ok=false
	// for both, and returning a nil map here would panic the callers' writes.
	r, ok := mapField(rt.root, "routing")
	if !ok {
		r = map[string]any{}
		rt.root["routing"] = r
	}
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
// of rules.fencing, preserving the operator's existing entries. blocked: true
// is set directly on the entry — the CFK Gateway CRD requires
// rules.fencing[].blocked, and a batch fence entry always means "block these
// topics," so every consumer applying FenceYAML/SwitchoverYAML to a live
// Gateway CR can do so unmodified.
//
// Idempotent: any pre-existing entry kcp itself would have authored for this
// exact topic set (a blocked:true fence over the same names) is dropped
// before the fresh one is prepended, so reconciling an already-fenced route
// on a resume does not accumulate duplicate fences. Operator entries — a
// different topic set, or blocked absent/false — never match and are
// preserved in order.
func (rt *RulesTree) PrependFence(topics []string) {
	entry := map[string]any{"topics": asAnySlice(topics), "blocked": true}
	existing, _ := sliceField(rt.root, "fencing")
	kept := make([]any, 0, len(existing))
	for _, e := range existing {
		if isKcpFenceFor(e, topics) {
			continue // drop our own prior identical fence to prevent doubling
		}
		kept = append(kept, e)
	}
	rt.root["fencing"] = append([]any{entry}, kept...)
}

// DropFence removes kcp's own fence entry for exactly this topic set, leaving
// operator-authored fences untouched. The switchover artifact uses it so the
// switched state is unfenced for our topics: on a resume the base rules already
// carry kcp's fence (from the interrupted run), which must not persist onto the
// switched route. A no-op when kcp's fence is absent (e.g. a first run built
// from a pristine base).
func (rt *RulesTree) DropFence(topics []string) {
	existing, ok := sliceField(rt.root, "fencing")
	if !ok {
		return
	}
	kept := make([]any, 0, len(existing))
	for _, e := range existing {
		if isKcpFenceFor(e, topics) {
			continue
		}
		kept = append(kept, e)
	}
	rt.root["fencing"] = kept
}

// isKcpFenceFor reports whether e is a fence entry kcp itself would author for
// exactly this topic set: blocked==true and the same topics as a set. Operator
// entries never match, so they are preserved by PrependFence's dedupe.
//
// The exact-set match is kcp's only notion of fence ownership on a shared
// route, so an interrupted batch must be resumed with the same resolved topic
// set: a run with a different set treats the earlier run's fence as
// operator-authored, and it survives the switchover.
func isKcpFenceFor(e any, topics []string) bool {
	m, ok := e.(map[string]any)
	if !ok {
		return false
	}
	if b, _ := m["blocked"].(bool); !b {
		return false
	}
	return sameStringSet(stringList(m, "topics"), topics)
}

// sameStringSet reports whether a and b contain the same elements irrespective
// of order (topic lists carry no duplicates, so multiplicity is not compared).
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]struct{}, len(a))
	for _, s := range a {
		seen[s] = struct{}{}
	}
	for _, s := range b {
		if _, ok := seen[s]; !ok {
			return false
		}
	}
	return true
}

// PrependCondition adds an exact-name routing condition at the head of
// rules.routing.conditions, preserving the operator's existing conditions.
//
// Idempotent: a pre-existing condition kcp itself would author for this exact
// topic set AND target domain is dropped before the fresh one is prepended,
// so a resume does not accumulate duplicate switchover conditions. A
// condition for the same topics to a DIFFERENT domain is an operator's and is
// preserved.
func (rt *RulesTree) PrependCondition(topics []string, domain string) {
	routing := rt.ensureRouting()
	entry := map[string]any{"topics": asAnySlice(topics), "streamingDomain": domain}
	existing, _ := sliceField(routing, "conditions")
	kept := make([]any, 0, len(existing))
	for _, c := range existing {
		if isKcpConditionFor(c, topics, domain) {
			continue // drop our own prior identical condition to prevent doubling
		}
		kept = append(kept, c)
	}
	routing["conditions"] = append([]any{entry}, kept...)
}

// isKcpConditionFor reports whether c is a routing condition kcp itself would
// author for exactly this topic set and target domain. A condition to a
// different domain, or over a different topic set, never matches.
func isKcpConditionFor(c any, topics []string, domain string) bool {
	m, ok := c.(map[string]any)
	if !ok {
		return false
	}
	if stringField(m, "streamingDomain") != domain {
		return false
	}
	return sameStringSet(stringList(m, "topics"), topics)
}

// Serialize renders the artifact as the whole hot-reloadable `rules` subtree,
// wrapped under a top-level `rules:` key — the KCP-patched subtree the gateway
// consumes (see Gateway Dynamic Routing Configuration). The caller applies it as
// a whole-subtree replacement of the route's `rules`, which is also what reverts
// the batch fence at switchover.
func (rt *RulesTree) Serialize() ([]byte, error) {
	return yaml.Marshal(map[string]any{"rules": rt.root})
}
