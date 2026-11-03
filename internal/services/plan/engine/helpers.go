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

// joinAndList reads "A", "A and B", "A, B, and C" (Oxford comma before the final "and").
func joinAndList(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	case 2:
		return xs[0] + " and " + xs[1]
	}
	return strings.Join(xs[:len(xs)-1], ", ") + ", and " + xs[len(xs)-1]
}

// joinOr reads "A", "A or B", "A, B, or C" (Oxford comma before the final "or").
func joinOr(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	case 2:
		return xs[0] + " or " + xs[1]
	}
	return strings.Join(xs[:len(xs)-1], ", ") + ", or " + xs[len(xs)-1]
}

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

// MetricsScanCommand names the runnable command that collects throughput metrics
// for a source, to size from real ingress/egress. Amazon MSK collects CloudWatch
// metrics during `kcp discover`; Apache Kafka and Confluent Platform collect them
// with `kcp scan clusters --metrics jolokia|prometheus` (credentials required).
// With no scan the source is unknown (MSK is only the default), so both are named.
// stateFile, when known, is passed through as given.
func MetricsScanCommand(p Profile) string {
	const discover = "`kcp discover --region <your-region>` (without `--skip-metrics`)"
	if p.isOSKorCP() {
		cmd := "kcp scan clusters --source-type apache-kafka --credentials-file apache-kafka-credentials.yaml"
		if p.StateFile != "" {
			cmd += " --state-file " + p.StateFile
		}
		return "`" + cmd + " --metrics prometheus --metrics-range 7d` (or `--metrics jolokia --metrics-duration 1h` to poll live)"
	}
	if p.Scanless && !p.SourcePlatformDeclared {
		// Both sources named: a scanless run that never named its source, or a mixed fleet.
		osk := "kcp scan clusters --source-type apache-kafka --credentials-file apache-kafka-credentials.yaml"
		if p.StateFile != "" {
			osk += " --state-file " + p.StateFile
		}
		return discover + " for Amazon MSK, or `" + osk + " --metrics prometheus --metrics-range 7d` (or `--metrics jolokia --metrics-duration 1h`) for Apache Kafka and Confluent Platform"
	}
	return discover
}
