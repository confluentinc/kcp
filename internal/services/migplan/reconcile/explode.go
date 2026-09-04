package reconcile

import (
	"fmt"
	"regexp"
	"sort"
)

// Explode resolves a selector into concrete topic names: the de-duplicated,
// sorted union of exactTopics and every sourceTopics entry matching any pattern.
// Patterns are anchored full-match (RE2). Determinism (sort) keeps artifacts
// stable across runs.
func Explode(exactTopics, patterns, sourceTopics []string) ([]string, error) {
	set := map[string]struct{}{}
	for _, t := range exactTopics {
		set[t] = struct{}{}
	}
	for _, p := range patterns {
		re, err := regexp.Compile("^(?:" + p + ")$")
		if err != nil {
			return nil, fmt.Errorf("invalid topicPattern %q: %w", p, err)
		}
		for _, t := range sourceTopics {
			if re.MatchString(t) {
				set[t] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out, nil
}
