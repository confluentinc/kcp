package types

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry"
)

type SchemaRegistryScanResult struct {
	Timestamp time.Time 			`json:"timestamp"`
	URL       string          `json:"schema_registry_url"`
	Subjects  []SubjectExport `json:"subjects"`
}

type SubjectExport struct {
	Name     string                              `json:"name"`
	Versions []int     													 `json:"versions"`
	Latest   schemaregistry.SchemaMetadata       `json:"latest_schema"`
}

func (s *SchemaRegistryScanResult) GetDirPath() string {
	return filepath.Join("kcp-scan", "schema-registry")
}

func (s *SchemaRegistryScanResult) GetJsonPath() string {
	return filepath.Join(s.GetDirPath(), "schema-registry-scan.json")
}

func (s *SchemaRegistryScanResult) AsJson() ([]byte, error) {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to marshal scan results: %v", err)
	}
	return data, nil
}

func (s *SchemaRegistryScanResult) WriteAsJson() error {
	dirPath := s.GetDirPath()
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return fmt.Errorf("❌ Failed to create directory structure: %v", err)
	}

	filePath := s.GetJsonPath()

	data, err := s.AsJson()
	if err != nil {
		return err
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("❌ Failed to write file: %v", err)
	}

	return nil
}