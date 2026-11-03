package migration_infra

import (
	"strings"
	"testing"
)

// TestValidateMigrationInfraDestination covers the --cc-type gate. The gate is
// evaluated in preRunMigrationInfra before --type is parsed, so a Confluent
// Cloud for Government refusal precedes any type-specific flag errors (the
// function intentionally takes only the destination value).
func TestValidateMigrationInfraDestination(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ccType  string
		wantErr string // substring; empty means no error expected
	}{
		{
			// AE1: omitting the declaration errors and lists the allowed values.
			name:    "missing declaration is required error",
			wantErr: "--cc-type is required",
		},
		{
			// AE2 + R13/R14: gov refused, naming Cluster Linking and the exact product name.
			name:    "government refused naming Cluster Linking",
			ccType:  "government",
			wantErr: "Cluster Linking",
		},
		{
			name:    "government refusal uses exact product name",
			ccType:  "government",
			wantErr: "Confluent Cloud for Government",
		},
		{
			// R2: mixed-case government normalizes and is still refused.
			name:    "GOVERNMENT mixed case refused",
			ccType:  "GOVERNMENT",
			wantErr: "Confluent Cloud for Government",
		},
		{
			// AE5: commercial passes the gate.
			name:   "commercial passes the gate",
			ccType: "commercial",
		},
		{
			// R2: mixed-case commercial normalizes and passes.
			name:   "Commercial mixed case passes the gate",
			ccType: "Commercial",
		},
		{
			// R3: invalid value rejected.
			name:    "invalid value rejected",
			ccType:  "fedramp",
			wantErr: "invalid --cc-type",
		},
		{
			// R3/R6: legacy cc value no longer accepted (clean break).
			name:    "legacy cc rejected",
			ccType:  "cc",
			wantErr: "invalid --cc-type",
		},
		{
			// R3/R6: legacy cc-gov value no longer accepted (clean break).
			name:    "legacy cc-gov rejected",
			ccType:  "cc-gov",
			wantErr: "invalid --cc-type",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateMigrationInfraDestination(tt.ccType)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateMigrationInfraDestination(%q) unexpected error: %v", tt.ccType, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateMigrationInfraDestination(%q) expected error containing %q, got nil", tt.ccType, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateMigrationInfraDestination(%q) error = %q, want substring %q", tt.ccType, err.Error(), tt.wantErr)
			}
		})
	}

	t.Run("invalid value error lists allowed values", func(t *testing.T) {
		t.Parallel()
		err := validateMigrationInfraDestination("fedramp")
		if err == nil {
			t.Fatal("expected error for invalid value")
		}
		if !strings.Contains(err.Error(), "commercial") || !strings.Contains(err.Error(), "government") {
			t.Errorf("invalid-value error %q should list commercial and government", err.Error())
		}
	})
}
