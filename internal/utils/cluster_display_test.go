package utils

import (
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestGetClusterDisplayName_MSK(t *testing.T) {
	displayName := GetClusterDisplayName(types.SourceTypeMSK, "arn:aws:kafka:us-east-1:123456789012:cluster/my-cluster/abc-123", "")
	assert.Equal(t, "my-cluster", displayName)
}

func TestGetClusterDisplayName_OSK(t *testing.T) {
	displayName := GetClusterDisplayName(types.SourceTypeOSK, "", "production-kafka")
	assert.Equal(t, "production-kafka", displayName)
}

func TestGetClusterDisplayName_MSK_MalformedArn(t *testing.T) {
	displayName := GetClusterDisplayName(types.SourceTypeMSK, "not-an-arn", "")
	assert.Equal(t, "unknown-cluster", displayName)
}

func TestGetClusterDisplayName_InvalidSourceType(t *testing.T) {
	displayName := GetClusterDisplayName("invalid", "arn", "id")
	assert.Equal(t, "unknown-cluster", displayName)
}
