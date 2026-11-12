//go:build integration

package mskemulator

import (
	"context"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmulatorIsTheOnlyAWS(t *testing.T) {
	for _, k := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_PROFILE", "AWS_DEFAULT_PROFILE"} {
		_, set := os.LookupEnv(k)
		assert.False(t, set, "%s must be removed from the environment", k)
	}
	cfg, err := sdkConfig(context.Background(), "us-east-1")
	require.NoError(t, err)
	out, err := sts.NewFromConfig(cfg).GetCallerIdentity(context.Background(), &sts.GetCallerIdentityInput{})
	require.NoError(t, err)
	assert.Equal(t, "000000000000", aws.ToString(out.Account))
}
