package reconcile

import "regexp"

// OwnerRoute resolves the domain that owns topic per the routing conditions:
// the first condition it matches (exact Topics OR anchored TopicPatterns, in
// list order), else defaultDomain. Returns ok=false only on a strict route
// (empty defaultDomain) with no matching condition.
//
// Mirrors the Gateway's DynamicRouterState.ownerRoute. Patterns are compiled
// here anchored full-match; compilation is assumed to have been validated
// upstream (preconditions/explode), so a bad pattern is treated as non-matching.
func OwnerRoute(topic string, conditions []Condition, defaultDomain string) (string, bool) {
	for _, c := range conditions {
		for _, t := range c.Topics {
			if t == topic {
				return c.Domain, true
			}
		}
		for _, p := range c.TopicPatterns {
			re, err := regexp.Compile("^(?:" + p + ")$")
			if err != nil {
				continue
			}
			if re.MatchString(topic) {
				return c.Domain, true
			}
		}
	}
	if defaultDomain == "" {
		return "", false
	}
	return defaultDomain, true
}
