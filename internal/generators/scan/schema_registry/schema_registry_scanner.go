package schema_registry

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/confluentinc/kcp/internal/services/schema_registry"
	"github.com/confluentinc/kcp/internal/types"
)

type SchemaRegistryScanner struct {
	schemaRegistryService *schema_registry.SchemaRegistryService
	url string
}

func NewSchemaRegistryScanner(schemaRegistryService *schema_registry.SchemaRegistryService, url string) *SchemaRegistryScanner {
	return &SchemaRegistryScanner{
		schemaRegistryService: schemaRegistryService,
		url: url,
	}
}

func (srs *SchemaRegistryScanner) Run() error {
	slog.Info("🚀 starting schema registry scan", "url", srs.url)

	result, err := srs.ScanSchemaRegistry()
	if err != nil {
		return err
	}

	if err := result.WriteAsJson(); err != nil {
		return fmt.Errorf("❌ Failed to generate json report: %v", err)
	}

	slog.Info("✅ schema registry scan complete",
		"url", srs.url,
		"subjectCount", len(result.Subjects),
		"filePath", result.GetJsonPath(),
	)

	return nil
}

func (srs *SchemaRegistryScanner) ScanSchemaRegistry() (*types.SchemaRegistryScanResult, error) {
	result := &types.SchemaRegistryScanResult{
		Timestamp: time.Now(),
		URL:       srs.url,
		Subjects:  []types.SubjectExport{},
	}

	subjects, err := srs.schemaRegistryService.GetAllSubjects()
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to get all subjects: %v", err)
	}

	
	for _, subjectName := range subjects {
		latest, err := srs.schemaRegistryService.GetLatestSchema(subjectName)
		if err != nil {
			continue
		}

		versions, err := srs.schemaRegistryService.GetAllSubjectVersions(subjectName)
		if err != nil {
			continue
		}

		subject := types.SubjectExport{
			Name:   subjectName,
			Versions: versions,
			Latest: latest,
		}
		result.Subjects = append(result.Subjects, subject)
	}

	return result, nil
}