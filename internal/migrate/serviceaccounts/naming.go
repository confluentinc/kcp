package serviceaccounts

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

const maxNameLen = 64

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*[A-Za-z0-9]$|^[A-Za-z0-9]$`)
var disallowed = regexp.MustCompile(`[^A-Za-z0-9._:-]`)
var edgeTrim = regexp.MustCompile(`^[^A-Za-z0-9]+|[^A-Za-z0-9]+$`)

func DeriveDisplayName(principal string) string {
	name := strings.TrimPrefix(principal, "User:")
	if len(name) <= maxNameLen && validName.MatchString(name) {
		return name
	}
	base := edgeTrim.ReplaceAllString(disallowed.ReplaceAllString(name, "-"), "")
	if len(base) > maxNameLen-9 { // 9 = "-" + 8 hex
		base = strings.TrimRight(base[:maxNameLen-9], "._:-")
	}
	return base + "-" + hash8(principal)
}

func hash8(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}
