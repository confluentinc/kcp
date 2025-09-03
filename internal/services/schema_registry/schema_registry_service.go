package schema_registry
// https://docs.confluent.io/platform/current/schema-registry/develop/api.html#schemas

import (
	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry"
)

type SchemaRegistryService struct {
		client schemaregistry.Client
}

func NewSchemaRegistryService(client schemaregistry.Client) *SchemaRegistryService {
	return &SchemaRegistryService{client: client}
}

// Get all subject names
// Returns a list of all subjects in the schema registry
func (sr *SchemaRegistryService) ListSubjects() ([]string, error) {
	return sr.client.GetAllSubjects()
}

// Get all subject versions
// Returns a list of all versions available for a subject
func (sr *SchemaRegistryService) GetAllSubjectVersions(subject string) ([]int, error) {
	return sr.client.GetAllVersions(subject)
}

// Get latest schema for a subject
// Returns full schema details with version, ID, and schema definition
func (sr *SchemaRegistryService) GetLatestSchema(subject string) (schemaregistry.SchemaMetadata, error) {
	return sr.client.GetLatestSchemaMetadata(subject)
}