package utils

import "path"

// FilterByGlob applies include then exclude glob patterns to a list of names.
// Patterns use the syntax of path.Match (supports *, ?, [a-z], etc., but not **).
//
// Semantics:
//   - An empty include list means "include everything" (default-all).
//   - A name is kept if at least one include pattern matches it.
//   - A name is dropped if at least one exclude pattern matches it.
//   - Exclude wins on overlap.
//   - Patterns that error from path.Match are treated as non-matching for that
//     name; callers should validate patterns up front if they want hard errors.
//
// Order is preserved.
func FilterByGlob(names []string, includes []string, excludes []string) []string {
	result := make([]string, 0, len(names))
	for _, name := range names {
		if !matchesAny(name, includes, true) {
			continue
		}
		if matchesAny(name, excludes, false) {
			continue
		}
		result = append(result, name)
	}
	return result
}

// matchesAny reports whether name matches any of the patterns. When the pattern
// list is empty, the result is emptyDefault — true for include (default-all),
// false for exclude (default-none).
func matchesAny(name string, patterns []string, emptyDefault bool) bool {
	if len(patterns) == 0 {
		return emptyDefault
	}
	for _, p := range patterns {
		matched, err := path.Match(p, name)
		if err == nil && matched {
			return true
		}
	}
	return false
}
