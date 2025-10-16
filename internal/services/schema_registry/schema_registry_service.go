package schema_registry

import (
	"fmt"

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

func (sr *SchemaRegistryService) ExportAllSubjects() ([]types.Subject, error) {
	subjects := []types.Subject{}

	allSubjects, err := sr.client.GetAllSubjects()
	if err != nil {
		return nil, fmt.Errorf("failed to get all subjects: %v", err)
	}

	for _, subjectName := range allSubjects {
		latest, err := sr.GetLatestSchema(subjectName)
		if err != nil {
			continue
		}

		versions, err := sr.GetAllSubjectVersions(subjectName)
		if err != nil {
			continue
		}

		subjects = append(subjects, types.Subject{
			Name:     subjectName,
			Versions: versions,
			Latest:   latest,
		})
	}

	return subjects, nil
}

func (sr *SchemaRegistryService) GetAllSubjectVersions(subject string) ([]int, error) {
	return sr.client.GetAllVersions(subject)
}

func (sr *SchemaRegistryService) GetLatestSchema(subject string) (schemaregistry.SchemaMetadata, error) {
	return sr.client.GetLatestSchemaMetadata(subject)
}
