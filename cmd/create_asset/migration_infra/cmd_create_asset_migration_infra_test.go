package migration_infra

import (
	"strings"
	"testing"
)

// TestValidateMigrationInfraDestination covers the --cc-environment gate.
// The gate is evaluated in preRunMigrationInfra before --type is parsed, so a
// Confluent Cloud for Government refusal precedes any type-specific flag errors
// (the function intentionally takes only the destination value).
func TestValidateMigrationInfraDestination(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		ccEnvironment string
		wantErr       string // substring; empty means no error expected
	}{
		{
			// AE1: omitting the declaration errors and lists the allowed values.
			name:    "missing declaration is required error",
			wantErr: "--cc-environment is required",
		},
		{
			// AE2 + R13/R14: gov refused, naming Cluster Linking and the exact product name.
			name:          "cc-gov refused naming Cluster Linking",
			ccEnvironment: "cc-gov",
			wantErr:       "Cluster Linking",
		},
		{
			name:          "cc-gov refusal uses exact product name",
			ccEnvironment: "cc-gov",
			wantErr:       "Confluent Cloud for Government",
		},
		{
			// AE5: commercial passes the gate.
			name:          "cc passes the gate",
			ccEnvironment: "cc",
		},
		{
			// R3: invalid value rejected, listing allowed values.
			name:          "invalid value rejected",
			ccEnvironment: "ccgov",
			wantErr:       "invalid --cc-environment",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateMigrationInfraDestination(tt.ccEnvironment)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateMigrationInfraDestination(%q) unexpected error: %v", tt.ccEnvironment, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateMigrationInfraDestination(%q) expected error containing %q, got nil", tt.ccEnvironment, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateMigrationInfraDestination(%q) error = %q, want substring %q", tt.ccEnvironment, err.Error(), tt.wantErr)
			}
		})
	}

	t.Run("invalid value error lists allowed values", func(t *testing.T) {
		t.Parallel()
		err := validateMigrationInfraDestination("commercial")
		if err == nil {
			t.Fatal("expected error for invalid value")
		}
		if !strings.Contains(err.Error(), "cc") || !strings.Contains(err.Error(), "cc-gov") {
			t.Errorf("invalid-value error %q should list cc and cc-gov", err.Error())
		}
	})
}
