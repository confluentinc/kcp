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
		wantStr string // canonical (normalized, lowercase) value when valid
		wantGov bool
		wantErr bool
	}{
		// Happy path.
		{name: "commercial", input: "commercial", wantStr: "commercial", wantGov: false},
		{name: "government", input: "government", wantStr: "government", wantGov: true},

		// Case-insensitive: accepted and normalized to canonical lowercase (R2).
		{name: "Commercial title case", input: "Commercial", wantStr: "commercial", wantGov: false},
		{name: "COMMERCIAL upper", input: "COMMERCIAL", wantStr: "commercial", wantGov: false},
		{name: "Government title case", input: "Government", wantStr: "government", wantGov: true},
		{name: "GOVERNMENT upper", input: "GOVERNMENT", wantStr: "government", wantGov: true},
		{name: "gOvErNmEnT scrambled case", input: "gOvErNmEnT", wantStr: "government", wantGov: true},

		// Legacy values rejected — clean break (R3).
		{name: "legacy cc rejected", input: "cc", wantErr: true},
		{name: "legacy cc-gov rejected", input: "cc-gov", wantErr: true},

		// Empty and unknown rejected (R3).
		{name: "empty rejected", input: "", wantErr: true},
		{name: "unknown fedramp rejected", input: "fedramp", wantErr: true},
		{name: "typo comercial rejected", input: "comercial", wantErr: true},
		{name: "unknown prod rejected", input: "prod", wantErr: true},

		// Whitespace rejected — case-fold only, no trimming (R3, D4).
		{name: "leading and trailing space rejected", input: " government ", wantErr: true},
		{name: "trailing tab rejected", input: "government\t", wantErr: true},
		{name: "leading space rejected", input: " commercial", wantErr: true},

		// Overlong arbitrary input rejected, no panic (fail-closed).
		{name: "overlong arbitrary rejected", input: strings.Repeat("x", 10000), wantErr: true},
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
				if !strings.Contains(msg, "commercial") || !strings.Contains(msg, "government") {
					t.Errorf("ToDestinationType(%q) error %q should list commercial and government", tt.input, msg)
				}
				return
			}
			if err != nil {
				t.Fatalf("ToDestinationType(%q) unexpected error: %v", tt.input, err)
			}
			if string(got) != tt.wantStr {
				t.Errorf("ToDestinationType(%q) = %q, want %q", tt.input, got, tt.wantStr)
			}
			if got.IsGov() != tt.wantGov {
				t.Errorf("ToDestinationType(%q).IsGov() = %v, want %v", tt.input, got.IsGov(), tt.wantGov)
			}
		})
	}
}

func TestDestinationTypeIsGov(t *testing.T) {
	t.Parallel()

	gov, err := ToDestinationType("government")
	if err != nil {
		t.Fatalf("ToDestinationType(\"government\") unexpected error: %v", err)
	}
	if !gov.IsGov() {
		t.Errorf("government IsGov() = false, want true")
	}

	com, err := ToDestinationType("commercial")
	if err != nil {
		t.Fatalf("ToDestinationType(\"commercial\") unexpected error: %v", err)
	}
	if com.IsGov() {
		t.Errorf("commercial IsGov() = true, want false")
	}
}
