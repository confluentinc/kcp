// Package redact removes sensitive values from connector configurations before
// they are persisted to the state file or written to logs. Redaction is
// always-on and one-way: the original value is never recoverable from the
// redacted output. Matching is fail-closed — a key that looks sensitive is
// redacted even at the cost of occasionally redacting a benign value.
package redact

import "strings"

// Placeholder is the literal value substituted for a redacted config value.
const Placeholder = "<kcp-redacted>"

// IsSensitive reports whether a config key should have its value redacted.
// A key is sensitive if any blacklist entry (a static sensitive-config name or
// a broad pattern) appears as a case-insensitive substring of the key.
func IsSensitive(key string) bool {
	k := strings.ToLower(key)
	for _, entry := range blacklist {
		if strings.Contains(k, entry) {
			return true
		}
	}
	return false
}

// RedactStringMap returns a copy of in with the values of sensitive keys
// replaced by Placeholder, plus the number of values redacted. The input map is
// not mutated. A nil input yields an empty map and a count of zero.
func RedactStringMap(in map[string]string) (map[string]string, int) {
	out := make(map[string]string, len(in))
	count := 0
	for k, v := range in {
		if IsSensitive(k) {
			out[k] = Placeholder
			count++
			continue
		}
		out[k] = v
	}
	return out, count
}

// RedactAnyMap returns a copy of in with sensitive values replaced by
// Placeholder, plus the number of values redacted. It recurses into nested maps
// and lists so that secrets nested inside structured values are also caught.
// A sensitive key is redacted wholesale regardless of its value's type
// (fail-closed); non-sensitive keys whose values are containers are recursed
// into. The input is not mutated.
func RedactAnyMap(in map[string]any) (map[string]any, int) {
	out := make(map[string]any, len(in))
	count := 0
	for k, v := range in {
		if IsSensitive(k) {
			out[k] = Placeholder
			count++
			continue
		}
		redacted, n := redactValue(v)
		out[k] = redacted
		count += n
	}
	return out, count
}

// redactValue recurses into container values, returning the redacted value and
// the number of redactions performed within it. Scalars are returned unchanged
// (their key already passed the non-sensitive check in the caller).
func redactValue(v any) (any, int) {
	switch t := v.(type) {
	case map[string]any:
		return RedactAnyMap(t)
	case []any:
		out := make([]any, len(t))
		count := 0
		for i, item := range t {
			redacted, n := redactValue(item)
			out[i] = redacted
			count += n
		}
		return out, count
	default:
		return v, 0
	}
}
