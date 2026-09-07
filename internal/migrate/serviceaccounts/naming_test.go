package serviceaccounts

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeriveDisplayName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"User:app-consumer", "app-consumer"}, // verbatim
		{"User:app.consumer", "app.consumer"}, // dot allowed → verbatim
		{"User:app_consumer", "app_consumer"}, // underscore → verbatim
	}
	for _, c := range cases {
		require.Equal(t, c.want, DeriveDisplayName(c.in), c.in)
	}
	// DN → sanitized + 8-hex suffix, valid, ≤64
	dn := DeriveDisplayName("User:CN=payments,OU=svc,O=acme")
	require.Regexp(t, `^[A-Za-z0-9][A-Za-z0-9._:-]*-[0-9a-f]{8}$`, dn)
	require.LessOrEqual(t, len(dn), 64)
	require.NotContains(t, dn, "=")
	require.NotContains(t, dn, ",")
	// determinism
	require.Equal(t, dn, DeriveDisplayName("User:CN=payments,OU=svc,O=acme"))
	// collision safety: two DNs same sanitized base → different names
	a := DeriveDisplayName("User:CN=payments,OU=svc,O=acme,L=alpha")
	b := DeriveDisplayName("User:CN=payments,OU=svc,O=acme,L=beta")
	require.NotEqual(t, a, b)
	// over-64 verbatim-eligible name still routes to hash branch and fits
	long := DeriveDisplayName("User:" + strings.Repeat("a", 80))
	require.LessOrEqual(t, len(long), 64)
	require.Regexp(t, `-[0-9a-f]{8}$`, long)
}

// TestDeriveDisplayName_EmptyOrAllSymbolBase covers principals that sanitize
// to an empty base (no alphanumeric characters at all). Previously these
// produced a name starting with "-", violating the display_name contract.
func TestDeriveDisplayName_EmptyOrAllSymbolBase(t *testing.T) {
	validSuffix := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*[0-9a-f]$`)
	inputs := []string{
		"User:",
		"User:====",
		"User:,,,,",
		"User:----",
		"User:@@@@",
	}
	for _, in := range inputs {
		got := DeriveDisplayName(in)
		require.Regexp(t, validSuffix, got, in)
		require.LessOrEqual(t, len(got), 64, in)
		// deterministic
		require.Equal(t, got, DeriveDisplayName(in), in)
	}
}

// TestDeriveDisplayName_CollisionSafety_FullPrincipalHash proves hash8 is
// derived from the full principal, not just the truncated base. Two distinct
// ~84-char principals sharing an identical first-80-char prefix truncate to
// the same 55-char sanitized base; the derived names must still differ.
func TestDeriveDisplayName_CollisionSafety_FullPrincipalHash(t *testing.T) {
	prefix := "User:" + strings.Repeat("a", 80)
	a := DeriveDisplayName(prefix + "ALPHA")
	b := DeriveDisplayName(prefix + "BETA")
	require.NotEqual(t, a, b)
}
