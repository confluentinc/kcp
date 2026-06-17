package types

import (
	"strings"
	"testing"
)

func TestToDestinationType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    DestinationType
		wantErr bool
	}{
		{name: "cc is commercial", input: "cc", want: DestinationCC},
		{name: "cc-gov is government", input: "cc-gov", want: DestinationCCGov},
		{name: "empty is required-style error", input: "", wantErr: true},
		{name: "leading/trailing whitespace rejected", input: " cc ", wantErr: true},
		{name: "wrong case rejected", input: "CC", wantErr: true},
		{name: "gov alone rejected", input: "gov", wantErr: true},
		{name: "ccgov without hyphen rejected", input: "ccgov", wantErr: true},
		{name: "arbitrary value rejected", input: "commercial", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ToDestinationType(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ToDestinationType(%q) expected error, got nil", tt.input)
				}
				// Error must list the allowed values so callers surface R3 directly.
				msg := err.Error()
				if !strings.Contains(msg, "cc") || !strings.Contains(msg, "cc-gov") {
					t.Errorf("ToDestinationType(%q) error %q should list cc and cc-gov", tt.input, msg)
				}
				return
			}
			if err != nil {
				t.Fatalf("ToDestinationType(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ToDestinationType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDestinationTypeIsGov(t *testing.T) {
	t.Parallel()

	if !DestinationCCGov.IsGov() {
		t.Errorf("DestinationCCGov.IsGov() = false, want true")
	}
	if DestinationCC.IsGov() {
		t.Errorf("DestinationCC.IsGov() = true, want false")
	}
}
