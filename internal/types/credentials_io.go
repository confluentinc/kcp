package types

import (
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
)

// writeYAMLFile marshals v to YAML and writes it to path with 0600 permissions.
// Shared by Credentials.WriteToFile and OSKCredentials.WriteToFile.
func writeYAMLFile(path string, v any) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write YAML file: %w", err)
	}
	return nil
}

// loadCredentialsFile reads a YAML credentials file at path, unmarshals it into T,
// and runs its Validate method. Returns the parsed value or the collected errors.
// Shared by NewCredentialsFromFile and NewOSKCredentialsFromFile.
func loadCredentialsFile[T interface{ Validate() (bool, []error) }](path string) (*T, []error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, []error{fmt.Errorf("failed to read %s: %w", path, err)}
	}

	var out T
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, []error{fmt.Errorf("failed to unmarshal YAML: %w", err)}
	}

	if valid, errs := out.Validate(); !valid {
		return nil, errs
	}

	return &out, nil
}
