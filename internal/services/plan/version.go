package plan

import (
	"strconv"
	"strings"
)

// versionAtLeast reports whether `have` is >= `floor` using
// dot-separated integer-segment comparison. Versions are compared
// pairwise; missing segments on either side are treated as 0 so "7.0"
// vs "7.0.1" works ("7.0.1" wins) and "7" vs "7.0" is equal.
//
// Special cases:
//   - "latest" / "current" (case-insensitive) → treated as >= any
//     floor (the most current release always clears every floor).
//   - Pre-release suffixes (e.g. "7.0.0-rc1", "7.0+build5") → stripped
//     before parsing; "7.0.0-rc1" compares as "7.0.0".
//
// Returns false on still-unparseable input — that's the safe direction:
// an unrecognised version doesn't claim it clears the floor.
//
// Lives at the package level so any future version-floor check (e.g.
// Cluster Linking's source Kafka version when that rule lands) can
// reuse the same comparator without duplication. Today only the
// Schema Linking CP-version check calls it.
func versionAtLeast(have, floor string) bool {
	switch strings.ToLower(strings.TrimSpace(have)) {
	case "latest", "current":
		return true
	}
	h := parseVersionSegments(have)
	f := parseVersionSegments(floor)
	if h == nil || f == nil {
		return false
	}
	n := len(h)
	if len(f) > n {
		n = len(f)
	}
	for i := 0; i < n; i++ {
		var hv, fv int
		if i < len(h) {
			hv = h[i]
		}
		if i < len(f) {
			fv = f[i]
		}
		if hv != fv {
			return hv > fv
		}
	}
	return true
}

// parseVersionSegments splits a dotted version into integer segments,
// dropping any pre-release / build suffix introduced by '-' or '+'.
// Returns nil on any non-integer or negative segment so the caller's
// fallback path can fire safely.
func parseVersionSegments(s string) []int {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 0 {
			return nil
		}
		out = append(out, n)
	}
	return out
}
