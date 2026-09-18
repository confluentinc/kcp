package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
)

const (
	// maxConfigIDLength mirrors the CRD's maxLength on spec.configId.
	maxConfigIDLength = 64

	// configIDPrefix makes a configId appearing in a gateway log or a GET
	// /config response traceable back to kcp rather than to the user.
	configIDPrefix = "kcp-"
)

// configIDPattern mirrors the CRD-enforced pattern on spec.configId:
// alphanumerics plus '.', '_', ':' and '-'.
//
// Note what this excludes: the base64 padding characters '+', '/' and '=' are
// all rejected, so a base64-derived id cannot be sent as-is — it has to be
// re-encoded (hex, or base64url without padding). Hence the hex generator below.
var configIDPattern = regexp.MustCompile(`^[a-zA-Z0-9._:-]+$`)

// NewConfigID returns a fresh, opaque config revision id for one Gateway apply.
//
// The contract requires only that each id differs from the last value sent —
// UUID is a convention, not a requirement — so this uses 128 bits of hex behind
// a kcp- prefix: unique in practice, well inside the 64-character limit, and
// free of the characters the CRD pattern rejects.
func NewConfigID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate a gateway configId: %w", err)
	}

	return configIDPrefix + hex.EncodeToString(buf), nil
}

// validateConfigID checks an id against the CRD's constraints so a bad value
// fails locally with a clear message, rather than as an API server rejection
// part-way through a migration.
func validateConfigID(id string) error {
	if id == "" {
		return fmt.Errorf("gateway configId must not be empty")
	}
	if len(id) > maxConfigIDLength {
		return fmt.Errorf("gateway configId is %d characters, exceeding the CRD limit of %d", len(id), maxConfigIDLength)
	}
	if !configIDPattern.MatchString(id) {
		return fmt.Errorf("gateway configId does not match the CRD-enforced pattern %s", configIDPattern)
	}

	return nil
}
