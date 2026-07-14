// Package serviceaccounts derives Confluent Cloud service-account display
// names from source Kafka principals.
package serviceaccounts

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

const maxNameLen = 64

// emptyBaseFallback is used in place of the sanitized base when a principal
// contains no alphanumeric characters at all (e.g. "User:", "User:====").
// It guarantees the returned name always starts with an alphanumeric
// character, satisfying the CC display_name contract.
const emptyBaseFallback = "sa"

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*[A-Za-z0-9]$|^[A-Za-z0-9]$`)
var disallowed = regexp.MustCompile(`[^A-Za-z0-9._:-]`)
var edgeTrim = regexp.MustCompile(`^[^A-Za-z0-9]+|[^A-Za-z0-9]+$`)

// DeriveDisplayName converts a source Kafka principal (e.g. "User:app-consumer"
// or a DN-style principal such as "User:CN=payments,OU=svc,O=acme") into a
// Confluent Cloud service-account display_name.
//
// The result always:
//   - starts and ends with an alphanumeric character,
//   - contains only [A-Za-z0-9._:-],
//   - is at most 64 characters long,
//   - is deterministic for a given principal.
//
// If, after stripping the "User:" prefix, the principal is already a valid
// display_name (and fits within the length limit), it is returned verbatim.
// Otherwise the principal is sanitized (disallowed characters replaced with
// "-", leading/trailing non-alphanumerics trimmed), truncated to leave room
// for a deterministic 8-hex-character suffix derived from the full principal,
// and that suffix appended. If sanitization leaves an empty base (e.g. the
// principal contains no alphanumeric characters), a fixed alphanumeric
// fallback ("sa") is used as the base instead, so the result never starts
// with a hyphen.
func DeriveDisplayName(principal string) string {
	name := strings.TrimPrefix(principal, "User:")
	if len(name) <= maxNameLen && validName.MatchString(name) {
		return name
	}
	base := edgeTrim.ReplaceAllString(disallowed.ReplaceAllString(name, "-"), "")
	if len(base) > maxNameLen-9 { // 9 = "-" + 8 hex
		base = strings.TrimRight(base[:maxNameLen-9], "._:-")
	}
	if base == "" {
		base = emptyBaseFallback
	}
	return base + "-" + hash8(principal)
}

func hash8(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}
