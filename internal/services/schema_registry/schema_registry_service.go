package schema_registry

import (
	"fmt"
	"log/slog"

	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry"
	"github.com/confluentinc/kcp/internal/types"
)

type SchemaRegistryService struct {
	client schemaregistry.Client
}

func NewSchemaRegistryService(client schemaregistry.Client) *SchemaRegistryService {
	return &SchemaRegistryService{
		client: client,
	}
}

func (sr *SchemaRegistryService) GetDefaultCompatibility() (schemaregistry.Compatibility, error) {
	compatibility, err := sr.client.GetDefaultCompatibility()
	if err != nil {
		return 0, err
	}
	return compatibility, nil
}

func (sr *SchemaRegistryService) GetAllSubjectsWithVersions() ([]types.Subject, error) {
	subjects := []types.Subject{}

	subjectNames, err := sr.client.GetAllSubjects()
	if err != nil {
		return nil, fmt.Errorf("failed to get all subjects: %v", err)
	}

	for _, subjectName := range subjectNames {
		slog.Info("🔍 scanning subject", "subject", subjectName)

		// 1. Get latest schema first - most critical, fail fast if unavailable
		latest, err := sr.client.GetLatestSchemaMetadata(subjectName)
		if err != nil {
			slog.Warn("skipping subject: unable to get latest schema", "subject", subjectName, "error", err)
			continue
		}

		// default to AVRO if no schema type is set
		if latest.SchemaType == "" {
			latest.SchemaType = "AVRO"
		}

		// 2. Get compatibility (optional metadata, doesn't cause skip)
		var subjectLevelCompatibility string
		compatibility, err := sr.client.GetCompatibility(subjectName)
		if err == nil {
			subjectLevelCompatibility = compatibility.String()
		}

		// 3. Get all version numbers
		versionNumbers, err := sr.client.GetAllVersions(subjectName)
		if err != nil {
			slog.Warn("skipping subject: unable to get versions", "subject", subjectName, "error", err)
			continue
		}

		// 4. Fetch each version's metadata
		versions := []schemaregistry.SchemaMetadata{}
		for _, version := range versionNumbers {
			schema, err := sr.client.GetSchemaMetadata(subjectName, version)
			if err != nil {
				slog.Warn("skipping version", "subject", subjectName, "version", version, "error", err)
				continue
			}

			if schema.SchemaType == "" {
				schema.SchemaType = "AVRO"
			}

			versions = append(versions, schema)
		}

		// 5. Build subject with all collected data
		subjects = append(subjects, types.Subject{
			Name:          subjectName,
			SchemaType:    string(latest.SchemaType),
			Compatibility: subjectLevelCompatibility,
			Versions:      versions,
			Latest:        latest,
		})
	}

	return subjects, nil
}
