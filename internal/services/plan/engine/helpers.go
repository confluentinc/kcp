package engine

import (
	"strconv"
	"strings"
)

// deref returns the pointed-to string, or "" when nil.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// contains reports whether xs includes s.
func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// joinComma joins with ", "; joinAnd joins with " and "; joinSpace joins with " ".
func joinComma(xs []string) string { return strings.Join(xs, ", ") }
func joinAnd(xs []string) string   { return strings.Join(xs, " and ") }
func joinSpace(xs []string) string { return strings.Join(xs, " ") }

// dedupe returns xs with duplicates removed, order preserved.
func dedupe(xs []string) []string {
	seen := map[string]bool{}
	out := xs[:0:0]
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// containsAnyFold reports whether s contains any of the substrings, case-insensitively.
func containsAnyFold(s string, subs ...string) bool {
	ls := strings.ToLower(s)
	for _, sub := range subs {
		if strings.Contains(ls, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}

// itoa is strconv.Itoa, aliased for brevity at the many call sites that build
// customer copy inline.
func itoa(n int) string { return strconv.Itoa(n) }

// isVowel reports whether r is an English vowel, for choosing "a" vs "an".
func isVowel(r rune) bool {
	switch r {
	case 'a', 'e', 'i', 'o', 'u', 'A', 'E', 'I', 'O', 'U':
		return true
	}
	return false
}
