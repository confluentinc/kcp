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

	subjectNames, err := sr.client.GetAllSubjects()
	if err != nil {
		return nil, fmt.Errorf("failed to get all subjects: %v", err)
	}

	for _, subjectName := range subjectNames {
		latest, err := sr.client.GetLatestSchemaMetadata(subjectName)
		if err != nil {
			continue
		}

		versionNumbers, err := sr.client.GetAllVersions(subjectName)
		if err != nil {
			continue
		}

		versions := []schemaregistry.SchemaMetadata{}
		for _, version := range versionNumbers {
			schema, err := sr.client.GetSchemaMetadata(subjectName, version)
			if err != nil {
				continue
			}
			versions = append(versions, schema)
		}

		subjects = append(subjects, types.Subject{
			Name:       subjectName,
			// todo not working at moment
			SchemaType: string(latest.SchemaType),
			Versions:   versions,
			Latest:     latest,
		})
	}

	return subjects, nil
}
