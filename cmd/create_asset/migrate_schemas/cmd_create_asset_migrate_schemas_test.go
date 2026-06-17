package migrate_schemas

import (
	"strings"
	"testing"
)

// TestValidateMigrateSchemasDestination covers the --cc-environment gate. The
// gate runs in preRunMigrateSchemas after the --url XOR --glue-registry check,
// so mutual-exclusivity errors are reported independently of the destination.
func TestValidateMigrateSchemasDestination(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		ccEnvironment string
		url           string
		wantErr       string // substring; empty means no error expected
	}{
		{
			// AE4 + R13/R14: gov + url refused naming the --glue-registry alternative.
			name:          "gov url refused naming glue alternative",
			ccEnvironment: "cc-gov",
			url:           "https://sr.example.com",
			wantErr:       "--glue-registry",
		},
		{
			name:          "gov url refusal uses exact product name",
			ccEnvironment: "cc-gov",
			url:           "https://sr.example.com",
			wantErr:       "Confluent Cloud for Government",
		},
		{
			// R13: the refusal names the linking technology it depends on.
			name:          "gov url refusal names Schema Linking",
			ccEnvironment: "cc-gov",
			url:           "https://sr.example.com",
			wantErr:       "Schema Linking",
		},
		{
			// AE4: gov + glue (no url) proceeds.
			name:          "gov glue is allowed",
			ccEnvironment: "cc-gov",
			url:           "",
		},
		{
			// AE5: commercial url proceeds.
			name:          "cc url is allowed",
			ccEnvironment: "cc",
			url:           "https://sr.example.com",
		},
		{
			// AE5: commercial glue proceeds.
			name:          "cc glue is allowed",
			ccEnvironment: "cc",
			url:           "",
		},
		{
			// AE1: missing declaration is a required error.
			name:    "missing declaration is required error",
			url:     "https://sr.example.com",
			wantErr: "--cc-environment is required",
		},
		{
			// R3: invalid value rejected.
			name:          "invalid value rejected",
			ccEnvironment: "ccgov",
			url:           "",
			wantErr:       "invalid --cc-environment",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateMigrateSchemasDestination(tt.ccEnvironment, tt.url)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateMigrateSchemasDestination(%q, %q) unexpected error: %v", tt.ccEnvironment, tt.url, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateMigrateSchemasDestination(%q, %q) expected error containing %q, got nil", tt.ccEnvironment, tt.url, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateMigrateSchemasDestination(%q, %q) error = %q, want substring %q", tt.ccEnvironment, tt.url, err.Error(), tt.wantErr)
			}
		})
	}

	t.Run("missing declaration error lists allowed values", func(t *testing.T) {
		t.Parallel()
		err := validateMigrateSchemasDestination("", "https://sr.example.com")
		if err == nil {
			t.Fatal("expected required error for empty --cc-environment")
		}
		if !strings.Contains(err.Error(), "cc") || !strings.Contains(err.Error(), "cc-gov") {
			t.Errorf("required error %q should list cc and cc-gov", err.Error())
		}
	})
}
