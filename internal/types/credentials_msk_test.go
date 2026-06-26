package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIAMConfig_CarriesRegion(t *testing.T) {
	c := IAMConfig{Use: true, Region: "us-east-1"}
	require.Equal(t, "us-east-1", c.Region)
}
