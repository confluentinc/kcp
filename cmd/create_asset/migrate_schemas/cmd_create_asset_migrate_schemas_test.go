package migrate_schemas

import (
	"strings"
	"testing"
)

// TestValidateMigrateSchemasDestination covers the --cc-type gate. The gate
// runs in preRunMigrateSchemas after the --url XOR --glue-registry check, so
// mutual-exclusivity errors are reported independently of the destination.
func TestValidateMigrateSchemasDestination(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ccType  string
		url     string
		wantErr string // substring; empty means no error expected
	}{
		{
			// AE4 + R13/R14: gov + url refused naming the --glue-registry alternative.
			name:    "gov url refused naming glue alternative",
			ccType:  "government",
			url:     "https://sr.example.com",
			wantErr: "--glue-registry",
		},
		{
			name:    "gov url refusal uses exact product name",
			ccType:  "government",
			url:     "https://sr.example.com",
			wantErr: "Confluent Cloud for Government",
		},
		{
			// R13: the refusal names the linking technology it depends on.
			name:    "gov url refusal names Schema Linking",
			ccType:  "government",
			url:     "https://sr.example.com",
			wantErr: "Schema Linking",
		},
		{
			// R2: mixed-case government normalizes and is still refused.
			name:    "GOVERNMENT mixed case url refused",
			ccType:  "GOVERNMENT",
			url:     "https://sr.example.com",
			wantErr: "Confluent Cloud for Government",
		},
		{
			// AE4: gov + glue (no url) proceeds.
			name:   "gov glue is allowed",
			ccType: "government",
			url:    "",
		},
		{
			// AE5: commercial url proceeds.
			name:   "commercial url is allowed",
			ccType: "commercial",
			url:    "https://sr.example.com",
		},
		{
			// AE5: commercial glue proceeds.
			name:   "commercial glue is allowed",
			ccType: "commercial",
			url:    "",
		},
		{
			// AE1: missing declaration is a required error.
			name:    "missing declaration is required error",
			url:     "https://sr.example.com",
			wantErr: "--cc-type is required",
		},
		{
			// R3: invalid value rejected.
			name:    "invalid value rejected",
			ccType:  "fedramp",
			url:     "",
			wantErr: "invalid --cc-type",
		},
		{
			// R3/R6: legacy cc no longer accepted (clean break).
			name:    "legacy cc rejected",
			ccType:  "cc",
			url:     "",
			wantErr: "invalid --cc-type",
		},
		{
			// R3/R6: legacy cc-gov no longer accepted (clean break).
			name:    "legacy cc-gov rejected",
			ccType:  "cc-gov",
			url:     "https://sr.example.com",
			wantErr: "invalid --cc-type",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateMigrateSchemasDestination(tt.ccType, tt.url)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateMigrateSchemasDestination(%q, %q) unexpected error: %v", tt.ccType, tt.url, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateMigrateSchemasDestination(%q, %q) expected error containing %q, got nil", tt.ccType, tt.url, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateMigrateSchemasDestination(%q, %q) error = %q, want substring %q", tt.ccType, tt.url, err.Error(), tt.wantErr)
			}
		})
	}

	t.Run("missing declaration error lists allowed values", func(t *testing.T) {
		t.Parallel()
		err := validateMigrateSchemasDestination("", "https://sr.example.com")
		if err == nil {
			t.Fatal("expected required error for empty --cc-type")
		}
		if !strings.Contains(err.Error(), "commercial") || !strings.Contains(err.Error(), "government") {
			t.Errorf("required error %q should list commercial and government", err.Error())
		}
	})
}
