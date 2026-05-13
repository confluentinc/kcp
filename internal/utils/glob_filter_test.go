package utils

import (
	"reflect"
	"testing"
)

func TestFilterByGlob(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    []string
		include  []string
		exclude  []string
		expected []string
	}{
		{
			name:     "include only matches prefix",
			input:    []string{"orders.a", "orders.b", "events.x"},
			include:  []string{"orders.*"},
			expected: []string{"orders.a", "orders.b"},
		},
		{
			name:     "exclude only drops suffix",
			input:    []string{"orders.a", "orders.dlq"},
			exclude:  []string{"*.dlq"},
			expected: []string{"orders.a"},
		},
		{
			name:     "include and exclude — exclude wins on overlap",
			input:    []string{"orders.a", "orders.b", "orders.dlq", "events.x"},
			include:  []string{"orders.*"},
			exclude:  []string{"*.dlq"},
			expected: []string{"orders.a", "orders.b"},
		},
		{
			name:     "empty include defaults to all",
			input:    []string{"a", "b", "c"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "empty input returns empty",
			input:    []string{},
			include:  []string{"*"},
			expected: []string{},
		},
		{
			name:     "no include and no exclude returns input unchanged",
			input:    []string{"a", "b"},
			expected: []string{"a", "b"},
		},
		{
			name:     "multiple include patterns are unioned",
			input:    []string{"orders.a", "events.x", "logs.y"},
			include:  []string{"orders.*", "events.*"},
			expected: []string{"orders.a", "events.x"},
		},
		{
			name:     "multiple exclude patterns are unioned",
			input:    []string{"a", "b", "c", "d"},
			exclude:  []string{"a", "c"},
			expected: []string{"b", "d"},
		},
		{
			name:     "exact name match works as a pattern",
			input:    []string{"orders", "orders.a", "events"},
			include:  []string{"orders"},
			expected: []string{"orders"},
		},
		{
			name:     "preserves input order",
			input:    []string{"z", "a", "m"},
			include:  []string{"*"},
			expected: []string{"z", "a", "m"},
		},
		{
			name:     "malformed pattern is treated as non-matching",
			input:    []string{"orders.a", "events.x"},
			include:  []string{"[unclosed"},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := FilterByGlob(tt.input, tt.include, tt.exclude)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("FilterByGlob(%v, %v, %v) = %v, want %v",
					tt.input, tt.include, tt.exclude, got, tt.expected)
			}
		})
	}
}
