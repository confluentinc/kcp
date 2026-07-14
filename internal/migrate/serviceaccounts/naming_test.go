package serviceaccounts

import (
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
