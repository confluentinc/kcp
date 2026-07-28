package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthMethodConfig_EnabledAuthMethods(t *testing.T) {
	tests := []struct {
		name       string
		config     AuthMethodConfig
		includeIAM bool
		expected   []AuthType
	}{
		{
			name:       "empty config returns no methods",
			config:     AuthMethodConfig{},
			includeIAM: true,
			expected:   []AuthType{},
		},
		{
			name: "single SASL/SCRAM method",
			config: AuthMethodConfig{
				SASLScram: &SASLScramConfig{Use: true},
			},
			includeIAM: true,
			expected:   []AuthType{AuthTypeSASLSCRAM},
		},
		{
			name: "IAM included when includeIAM is true",
			config: AuthMethodConfig{
				IAM: &IAMConfig{Use: true},
			},
			includeIAM: true,
			expected:   []AuthType{AuthTypeIAM},
		},
		{
			name: "IAM ignored when includeIAM is false (OSK)",
			config: AuthMethodConfig{
				IAM: &IAMConfig{Use: true},
			},
			includeIAM: false,
			expected:   []AuthType{},
		},
		{
			name: "disabled methods (Use=false) are excluded",
			config: AuthMethodConfig{
				SASLScram: &SASLScramConfig{Use: false},
				TLS:       &TLSConfig{Use: false},
			},
			includeIAM: true,
			expected:   []AuthType{},
		},
		{
			name: "canonical order preserved across all methods (includeIAM=true)",
			config: AuthMethodConfig{
				UnauthenticatedPlaintext: &UnauthenticatedPlaintextConfig{Use: true},
				UnauthenticatedTLS:       &UnauthenticatedTLSConfig{Use: true},
				IAM:                      &IAMConfig{Use: true},
				SASLScram:                &SASLScramConfig{Use: true},
				SASLPlain:                &SASLPlainConfig{Use: true},
				TLS:                      &TLSConfig{Use: true},
			},
			includeIAM: true,
			expected: []AuthType{
				AuthTypeUnauthenticatedPlaintext,
				AuthTypeUnauthenticatedTLS,
				AuthTypeIAM,
				AuthTypeSASLSCRAM,
				AuthTypeSASLPlain,
				AuthTypeTLS,
			},
		},
		{
			name: "canonical order with IAM filtered out (includeIAM=false)",
			config: AuthMethodConfig{
				UnauthenticatedPlaintext: &UnauthenticatedPlaintextConfig{Use: true},
				UnauthenticatedTLS:       &UnauthenticatedTLSConfig{Use: true},
				IAM:                      &IAMConfig{Use: true},
				SASLScram:                &SASLScramConfig{Use: true},
				SASLPlain:                &SASLPlainConfig{Use: true},
				TLS:                      &TLSConfig{Use: true},
			},
			includeIAM: false,
			expected: []AuthType{
				AuthTypeUnauthenticatedPlaintext,
				AuthTypeUnauthenticatedTLS,
				AuthTypeSASLSCRAM,
				AuthTypeSASLPlain,
				AuthTypeTLS,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.config.EnabledAuthMethods(tt.includeIAM))
		})
	}
}

func TestAuthMethodConfig_SelectedAuthType(t *testing.T) {
	t.Run("returns single enabled method", func(t *testing.T) {
		config := AuthMethodConfig{SASLScram: &SASLScramConfig{Use: true}}
		got, err := config.SelectedAuthType(false)
		require.NoError(t, err)
		assert.Equal(t, AuthTypeSASLSCRAM, got)
	})

	t.Run("errors when no method enabled", func(t *testing.T) {
		config := AuthMethodConfig{}
		_, err := config.SelectedAuthType(true)
		require.Error(t, err)
	})

	t.Run("IAM not selectable when includeIAM is false", func(t *testing.T) {
		config := AuthMethodConfig{IAM: &IAMConfig{Use: true}}
		_, err := config.SelectedAuthType(false)
		require.Error(t, err)
	})

	t.Run("IAM selectable when includeIAM is true", func(t *testing.T) {
		config := AuthMethodConfig{IAM: &IAMConfig{Use: true}}
		got, err := config.SelectedAuthType(true)
		require.NoError(t, err)
		assert.Equal(t, AuthTypeIAM, got)
	})
}
