package self_managed_connectors

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetClusterDisplayName_MSK(t *testing.T) {
	displayName := GetClusterDisplayName("msk", "arn:aws:kafka:us-east-1:123456789012:cluster/my-cluster/abc-123", "")
	assert.Equal(t, "my-cluster", displayName)
}

func TestGetClusterDisplayName_OSK(t *testing.T) {
	displayName := GetClusterDisplayName("osk", "", "production-kafka")
	assert.Equal(t, "production-kafka", displayName)
}

func TestGetClusterDisplayName_MSK_MalformedArn(t *testing.T) {
	displayName := GetClusterDisplayName("msk", "not-an-arn", "")
	assert.Equal(t, "unknown-cluster", displayName)
}

func TestGetClusterDisplayName_InvalidSourceType(t *testing.T) {
	displayName := GetClusterDisplayName("invalid", "arn", "id")
	assert.Equal(t, "unknown-cluster", displayName)
}
