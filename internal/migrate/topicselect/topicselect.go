// Package topicselect resolves a desired topic set from a live source topic
// list using glob include/exclude patterns, always excluding internal topics.
package topicselect

import (
	"path"
	"sort"
	"strings"
)

// SelectTopics returns the names in all that match any include glob and no
// exclude glob, with internal topics (leading "_") removed first. Patterns use
// path.Match semantics. Result is sorted and deduplicated. A malformed pattern
// returns an error.
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

	seen := map[string]struct{}{}
	var out []string
	for _, name := range all {
		if strings.HasPrefix(name, "_") {
			continue
		}
		inc, err := matchAny(name, include)
		if err != nil {
			return nil, err
		}
		if !inc {
			continue
		}
		exc, err := matchAny(name, exclude)
		if err != nil {
			return nil, err
		}
		if exc {
			continue
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
