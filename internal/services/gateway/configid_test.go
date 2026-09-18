package gateway

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConfigID(t *testing.T) {
	t.Run("always satisfies the CRD-enforced format", func(t *testing.T) {
		for range 200 {
			id, err := NewConfigID()
			require.NoError(t, err)

			assert.Regexp(t, configIDPattern, id)
			assert.LessOrEqual(t, len(id), maxConfigIDLength)
			assert.NotEmpty(t, id)
		}
	})

	t.Run("never emits base64 padding characters", func(t *testing.T) {
		// The CRD pattern rejects +, / and =, so a base64-derived id would be
		// refused by the API server. Guard the generator against regressing to one.
		for range 200 {
			id, err := NewConfigID()
			require.NoError(t, err)

			assert.NotContains(t, id, "+")
			assert.NotContains(t, id, "/")
			assert.NotContains(t, id, "=")
		}
	})

	t.Run("is unique across calls", func(t *testing.T) {
		// The contract requires each configId to differ from the last value sent;
		// a repeat would make a transition unverifiable.
		seen := make(map[string]struct{}, 1000)
		for range 1000 {
			id, err := NewConfigID()
			require.NoError(t, err)
			require.NotContains(t, seen, id, "generated a duplicate configId")
			seen[id] = struct{}{}
		}
	})

	t.Run("is identifiable as kcp's", func(t *testing.T) {
		id, err := NewConfigID()
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(id, configIDPrefix),
			"a configId in a gateway log should be traceable back to kcp")
	})
}

func TestValidateConfigID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{name: "generated-style hex id", id: "kcp-0123456789abcdef0123456789abcdef"},
		{name: "uuid form", id: "3f1a9c8e-1b2c-4d5e-8f90-a1b2c3d4e5f6"},
		{name: "all permitted specials", id: "a.b_c:d-e"},
		{name: "single character", id: "a"},
		{name: "exactly 64 characters", id: strings.Repeat("a", 64)},

		{name: "empty", id: "", wantErr: true},
		{name: "65 characters", id: strings.Repeat("a", 65), wantErr: true},
		{name: "base64 plus", id: "abc+def", wantErr: true},
		{name: "base64 slash", id: "abc/def", wantErr: true},
		{name: "base64 padding", id: "abcdef==", wantErr: true},
		{name: "contains a space", id: "abc def", wantErr: true},
		{name: "contains a newline", id: "abc\ndef", wantErr: true},
		{name: "leading newline only", id: "\n", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfigID(tt.id)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}
