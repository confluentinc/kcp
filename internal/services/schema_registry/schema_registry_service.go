package schema_registry

import (
	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry"
)

type SchemaRegistryService struct {
		client schemaregistry.Client
		confluentCloudRegistryEndpoint string
    confluentCloudApiKey           string
    confluentCloudApiSecret        string
}

func NewSchemaRegistryService(client schemaregistry.Client) *SchemaRegistryService {
	return &SchemaRegistryService{client: client}
}

// Returns a list of all subjects in the schema registry
func (sr *SchemaRegistryService) GetAllSubjects() ([]string, error) {
	return sr.client.GetAllSubjects()
}

// Returns a list of all versions available for a subject
func (sr *SchemaRegistryService) GetAllSubjectVersions(subject string) ([]int, error) {
	return sr.client.GetAllVersions(subject)
}

// Returns full schema details with version, ID, and schema definition
func (sr *SchemaRegistryService) GetLatestSchema(subject string) (schemaregistry.SchemaMetadata, error) {
	return sr.client.GetLatestSchemaMetadata(subject)
}
