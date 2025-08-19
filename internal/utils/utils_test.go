package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConvertKafkaVersion(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectedOutput string
	}{
		{
			name:           "4.0.x.kraft should convert to 4.0.0",
			input:          "4.0.x.kraft",
			expectedOutput: "4.0.0",
		},
		{
			name:           "3.9.x should convert to 3.9.0",
			input:          "3.9.x",
			expectedOutput: "3.9.0",
		},
		{
			name:           "3.9.x.kraft should convert to 3.9.0",
			input:          "3.9.x.kraft",
			expectedOutput: "3.9.0",
		},
		{
			name:           "3.7.x.kraft should convert to 3.7.0",
			input:          "3.7.x.kraft",
			expectedOutput: "3.7.0",
		},
		{
			name:           "3.6.0.1 should convert to 3.6.0",
			input:          "3.6.0.1",
			expectedOutput: "3.6.0",
		},
		{
			name:           "3.6.0 should remain 3.6.0",
			input:          "3.6.0",
			expectedOutput: "3.6.0",
		},
		{
			name:           "2.8.2.tiered should convert to 2.8.2",
			input:          "2.8.2.tiered",
			expectedOutput: "2.8.2",
		},
		{
			name:           "2.6.0 should remain 2.6.0",
			input:          "2.6.0",
			expectedOutput: "2.6.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertKafkaVersion(&tt.input)
			assert.Equal(t, tt.expectedOutput, result, "convertKafkaVersion should return expected output")
		})
	}
}
