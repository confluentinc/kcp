package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIAMConfig_CarriesRegion(t *testing.T) {
	c := IAMConfig{Use: true, Region: "us-east-1"}
	require.Equal(t, "us-east-1", c.Region)
}

func TestAuthMethodConfig_Selection(t *testing.T) {
	amc := AuthMethodConfig{SASLScram: &SASLScramConfig{Use: true}}
	require.Equal(t, []AuthType{AuthTypeSASLSCRAM}, amc.GetAuthMethods())
	got, err := amc.GetSelectedAuthType()
	require.NoError(t, err)
	require.Equal(t, AuthTypeSASLSCRAM, got)

	_, err = AuthMethodConfig{}.GetSelectedAuthType()
	require.Error(t, err)
}
