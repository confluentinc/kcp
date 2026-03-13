package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCamelToScreamingSnake(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "single word lowercase",
			input:    "read",
			expected: "READ",
		},
		{
			name:     "single word capitalized",
			input:    "Read",
			expected: "READ",
		},
		{
			name:     "two words camelCase - DescribeConfigs",
			input:    "DescribeConfigs",
			expected: "DESCRIBE_CONFIGS",
		},
		{
			name:     "two words camelCase - AlterConfigs",
			input:    "AlterConfigs",
			expected: "ALTER_CONFIGS",
		},
		{
			name:     "two words camelCase - IdempotentWrite",
			input:    "IdempotentWrite",
			expected: "IDEMPOTENT_WRITE",
		},
		{
			name:     "already uppercase single word",
			input:    "READ",
			expected: "READ",
		},
		{
			name:     "Alter single word",
			input:    "Alter",
			expected: "ALTER",
		},
		{
			name:     "Describe single word",
			input:    "Describe",
			expected: "DESCRIBE",
		},
		{
			name:     "Create single word",
			input:    "Create",
			expected: "CREATE",
		},
		{
			name:     "Delete single word",
			input:    "Delete",
			expected: "DELETE",
		},
		{
			name:     "Write single word",
			input:    "Write",
			expected: "WRITE",
		},
		{
			name:     "ClusterAction two words",
			input:    "ClusterAction",
			expected: "CLUSTER_ACTION",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CamelToScreamingSnake(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
