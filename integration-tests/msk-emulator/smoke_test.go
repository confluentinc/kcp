//go:build integration

package mskemulator

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmulatorIsTheOnlyAWS(t *testing.T) {
	want := emulatorEnv()
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(k, "AWS_") {
			continue
		}
		if w, ok := want[k]; assert.True(t, ok, "%s must be removed from the environment", k) {
			assert.Equal(t, w, v, "%s", k)
		}
	}
	cfg, err := sdkConfig(context.Background(), "us-east-1")
	require.NoError(t, err)
	out, err := sts.NewFromConfig(cfg).GetCallerIdentity(context.Background(), &sts.GetCallerIdentityInput{})
	require.NoError(t, err)
	assert.Equal(t, "000000000000", aws.ToString(out.Account))
}
