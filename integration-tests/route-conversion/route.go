// Package routeconversion is the live e2e suite for a dynamic-to-static route
// conversion (spec.route.convertTo: static) against a real Confluent Gateway,
// cluster link and clients (Minikube profile kcp-e2e-route-conversion). The
// e2e tests (build tag e2e) drive the real `kcp migration execute`, interrupt
// or fail it with the killpoint seams, and run verifiable clients through the
// gateway across the conversion.
//
// This file and checker.go are untagged so `make test-go` unit-tests them
// without a cluster: a wrong answer here would let a live assertion pass
// vacuously.
package routeconversion

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/goccy/go-yaml"
)

// Domains names the route's two streaming domains and their bootstrap server
// ids, as the suite's gateway template declares them.
type Domains struct {
	Source, SourceID string
	Dest, DestID     string
}

// FindRoute returns the named route from a Gateway CR document, or nil when it
// is absent or the CR doesn't parse.
func FindRoute(cr []byte, name string) map[string]any {
	var obj map[string]any
	if err := yaml.Unmarshal(cr, &obj); err != nil {
		return nil
	}
	spec, _ := obj["spec"].(map[string]any)
	routes, _ := spec["routes"].([]any)
	for _, r := range routes {
		if rm, ok := r.(map[string]any); ok && rm["name"] == name {
			return rm
		}
	}
	return nil
}

// ConvertFence is kcp's conversion fence, exactly as reconcile writes it
// (reconcile.RulesTree.PrependConvertFence).
func ConvertFence() map[string]any {
	return map[string]any{"topicPatterns": []any{".*"}, "blocked": true}
}

// isConvertFence reports whether e is exactly kcp's conversion fence: only
// topicPatterns and blocked, blocked true, the single '.*' pattern.
func isConvertFence(e any) bool {
	m, ok := e.(map[string]any)
	if !ok || len(m) != 2 {
		return false
	}
	if b, _ := m["blocked"].(bool); !b {
		return false
	}
	p := stringList(m, "topicPatterns")
	return len(p) == 1 && p[0] == ".*"
}

// fencing is route r's rules.fencing entries (nil when absent).
func fencing(r map[string]any) []any {
	rules, _ := r["rules"].(map[string]any)
	f, _ := rules["fencing"].([]any)
	return f
}

// HasConvertFence reports whether route r carries kcp's conversion fence
// anywhere in rules.fencing.
func HasConvertFence(r map[string]any) bool {
	for _, e := range fencing(r) {
		if isConvertFence(e) {
			return true
		}
	}
	return false
}

// StaticOn reports whether route r is static and bound to domain with
// bootstrap server id id.
func StaticOn(r map[string]any, domain, id string) bool {
	if r == nil || r["mode"] != "static" {
		return false
	}
	sd, _ := r["streamingDomain"].(map[string]any)
	return sd["name"] == domain && sd["bootstrapServerId"] == id
}

// PostTBMRules is the rules block a finished topic-based migration leaves:
// every link topic routed to the destination, everything else to the source,
// group coordination on the source, no fence.
func PostTBMRules(d Domains, linkTopics []string) map[string]any {
	topics := sortedCopy(linkTopics)
	listed := make([]any, len(topics))
	for i, t := range topics {
		listed[i] = t
	}
	return map[string]any{
		"routing": map[string]any{
			"coordination": map[string]any{"group": d.Source},
			"conditions": []any{
				map[string]any{"streamingDomain": d.Dest, "topics": listed},
				map[string]any{"streamingDomain": d.Source, "topicPatterns": []any{".*"}},
			},
		},
		"fencing": []any{},
	}
}

// PostTBMRoute returns a copy of live with its binding and rules set to the
// post-TBM dynamic shape. Every other key (name, endpoint,
// brokerIdentificationStrategy, security) is kept; a route-level fence is
// dropped. The empty singular streamingDomain is what the CRD defaults onto a
// dynamic route anyway; it is written so a converted (static) route's binding
// is replaced, not kept.
func PostTBMRoute(live map[string]any, d Domains, linkTopics []string) map[string]any {
	out := DeepCopy(live)
	delete(out, "fence")
	out["mode"] = "dynamic"
	out["streamingDomains"] = []any{
		map[string]any{"name": d.Source, "bootstrapServerId": d.SourceID},
		map[string]any{"name": d.Dest, "bootstrapServerId": d.DestID},
	}
	out["streamingDomain"] = map[string]any{"name": "", "bootstrapServerId": ""}
	out["rules"] = PostTBMRules(d, linkTopics)
	return out
}

// WithConvertFence returns a copy of route r with kcp's conversion fence at the
// head of rules.fencing, as an interrupted run leaves it.
func WithConvertFence(r map[string]any) map[string]any {
	out := DeepCopy(r)
	rules, _ := out["rules"].(map[string]any)
	if rules == nil {
		rules = map[string]any{}
		out["rules"] = rules
	}
	existing, _ := rules["fencing"].([]any)
	rules["fencing"] = append([]any{ConvertFence()}, existing...)
	return out
}

// DynamicProblems lists why route r is not the post-TBM dynamic route over
// linkTopics, with kcp's conversion fence (and nothing else) in rules.fencing
// when fenced is true and no fence at all when it is false. Empty when it is.
func DynamicProblems(r map[string]any, d Domains, linkTopics []string, fenced bool) []string {
	if r == nil {
		return []string{"route not found in the gateway CR"}
	}
	var p []string
	if r["mode"] != "dynamic" {
		p = append(p, fmt.Sprintf("mode is %v, want dynamic", r["mode"]))
	}
	if !bindsBoth(r, d) {
		p = append(p, fmt.Sprintf("streamingDomains is %v, want %s/%s and %s/%s", r["streamingDomains"], d.Source, d.SourceID, d.Dest, d.DestID))
	}
	if f, ok := r["fence"]; ok && !isEmpty(f) {
		p = append(p, fmt.Sprintf("route-level fence %v present", f))
	}
	rules, _ := r["rules"].(map[string]any)
	routing, _ := rules["routing"].(map[string]any)
	coord, _ := routing["coordination"].(map[string]any)
	if coord["group"] != d.Source {
		p = append(p, fmt.Sprintf("coordination.group is %v, want %s", coord["group"], d.Source))
	}
	conds, _ := routing["conditions"].([]any)
	if len(conds) != 2 {
		p = append(p, fmt.Sprintf("%d routing conditions, want 2", len(conds)))
	} else {
		first, _ := conds[0].(map[string]any)
		second, _ := conds[1].(map[string]any)
		if first["streamingDomain"] != d.Dest || !sameStrings(stringList(first, "topics"), linkTopics) || len(stringList(first, "topicPatterns")) > 0 {
			p = append(p, fmt.Sprintf("first condition is %v, want the link topics %v to %s", first, sortedCopy(linkTopics), d.Dest))
		}
		pats := stringList(second, "topicPatterns")
		if second["streamingDomain"] != d.Source || len(pats) != 1 || pats[0] != ".*" || len(stringList(second, "topics")) > 0 {
			p = append(p, fmt.Sprintf("second condition is %v, want topicPatterns ['.*'] to %s", second, d.Source))
		}
	}
	f := fencing(r)
	switch {
	case fenced && (len(f) != 1 || !isConvertFence(f[0])):
		p = append(p, fmt.Sprintf("rules.fencing is %v, want exactly kcp's conversion fence", f))
	case !fenced && len(f) != 0:
		p = append(p, fmt.Sprintf("rules.fencing is %v, want empty", f))
	}
	return p
}

// ConvertedProblems lists why route r is not a finished conversion: static on
// d.Dest with d.DestID, no rules, no streamingDomains, no route-level fence, and
// security equal to wantSecurity (the route's security before the run). Empty
// when it is.
func ConvertedProblems(r map[string]any, d Domains, wantSecurity any) []string {
	if r == nil {
		return []string{"route not found in the gateway CR"}
	}
	var p []string
	if !StaticOn(r, d.Dest, d.DestID) {
		p = append(p, fmt.Sprintf("mode %v, streamingDomain %v; want static on %s/%s", r["mode"], r["streamingDomain"], d.Dest, d.DestID))
	}
	for _, k := range []string{"rules", "streamingDomains", "fence"} {
		if v, ok := r[k]; ok && !isEmpty(v) {
			p = append(p, fmt.Sprintf("%s is %v, want none", k, v))
		}
	}
	if !jsonEqual(r["security"], wantSecurity) {
		p = append(p, fmt.Sprintf("security is %v, want it unchanged (%v)", r["security"], wantSecurity))
	}
	return p
}

// DeepCopy copies a route through JSON. Routes hold only strings, bools,
// lists and maps, so nothing is lost.
func DeepCopy(m map[string]any) map[string]any {
	raw, err := json.Marshal(m)
	if err != nil {
		panic(fmt.Sprintf("route is not JSON-encodable: %v", err))
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		panic(fmt.Sprintf("route does not round-trip through JSON: %v", err))
	}
	return out
}

func bindsBoth(r map[string]any, d Domains) bool {
	sds, _ := r["streamingDomains"].([]any)
	if len(sds) != 2 {
		return false
	}
	want := map[string]string{d.Source: d.SourceID, d.Dest: d.DestID}
	for _, e := range sds {
		m, _ := e.(map[string]any)
		name, _ := m["name"].(string)
		id, ok := want[name]
		if !ok || m["bootstrapServerId"] != id {
			return false
		}
		delete(want, name)
	}
	return len(want) == 0
}

// stringList is m[key] as strings; nil when absent or empty.
func stringList(m map[string]any, key string) []string {
	raw, _ := m[key].([]any)
	var out []string
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func sameStrings(a, b []string) bool {
	a, b = sortedCopy(a), sortedCopy(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// isEmpty is true for nil and for an empty map, list or string.
func isEmpty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case map[string]any:
		return len(t) == 0
	case []any:
		return len(t) == 0
	case string:
		return t == ""
	}
	return false
}

// jsonEqual compares two decoded values by their JSON encoding, so a value read
// back from the CR equals one built in Go.
func jsonEqual(a, b any) bool {
	ra, errA := json.Marshal(a)
	rb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ra) == string(rb)
}
