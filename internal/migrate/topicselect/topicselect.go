// Package topicselect resolves a desired topic set from a live source topic
// list using glob include/exclude patterns. Internal topics (leading "_") are
// excluded by default but can be explicitly opted in (see SelectTopics).
package topicselect

import (
	"log/slog"
	"path"
	"sort"
	"strings"
)

// SelectTopics returns the names in all that match any include glob and no
// exclude glob.
//
// Internal topics — those with a leading "_" (Kafka's "__"-prefixed topics plus
// Confluent / Schema-Registry "_schemas", "_confluent-*", …) — are excluded by
// default so a broad include like "*" never mirrors system topics. An internal
// topic is admitted only when an include pattern that ITSELF starts with "_"
// matches it (e.g. include ["_foo"] or ["_*"]) — the explicit opt-in. A broad
// "*" does not opt internal topics in.
//
// An exclude glob ALWAYS wins, even over an opted-in internal topic. Patterns
// use path.Match semantics. The result is sorted and deduplicated. A malformed
// pattern returns an error.
func SelectTopics(all, include, exclude []string) ([]string, error) {
	matchAny := func(name string, pats []string) (bool, error) {
		for _, p := range pats {
			ok, err := path.Match(p, name)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	}

	// optedInternal reports whether an include pattern that targets underscore
	// names (starts with "_") matches the topic — the explicit opt-in that lets
	// an internal topic through the default leading-"_" exclusion.
	optedInternal := func(name string, pats []string) (bool, error) {
		for _, p := range pats {
			if !strings.HasPrefix(p, "_") {
				continue
			}
			ok, err := path.Match(p, name)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	}

	seen := map[string]struct{}{}
	var out []string
	for _, name := range all {
		inc, err := matchAny(name, include)
		if err != nil {
			return nil, err
		}
		if !inc {
			continue
		}
		// Internal topics are excluded unless an underscore-leading include
		// pattern explicitly opted them in.
		if strings.HasPrefix(name, "_") {
			opted, err := optedInternal(name, include)
			if err != nil {
				return nil, err
			}
			if !opted {
				slog.Debug(`excluding internal topic; add an underscore include pattern (the literal name or "_*") to opt in`, "topic", name)
				continue
			}
		}
		exc, err := matchAny(name, exclude)
		if err != nil {
			return nil, err
		}
		if exc {
			continue // exclude always wins, even over an opted-in internal topic
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}
