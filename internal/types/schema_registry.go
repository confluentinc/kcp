package types

import (
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry"
)

type SchemaRegistryInformation struct {
	Type                 string                       `json:"type"`
	URL                  string                       `json:"url"`
	DefaultCompatibility schemaregistry.Compatibility `json:"default_compatibility"`
	Contexts             []string                     `json:"contexts"`
	Subjects             []Subject                    `json:"subjects"`
}

type Subject struct {
	Name          string                          `json:"name"`
	SchemaType    string                          `json:"schema_type"`
	Compatibility string                          `json:"compatibility,omitempty"`
	Versions      []schemaregistry.SchemaMetadata `json:"versions"`
	Latest        schemaregistry.SchemaMetadata   `json:"latest_schema"`
}

// SchemaRegistriesState holds schema registries organized by type
type SchemaRegistriesState struct {
	ConfluentSchemaRegistry []SchemaRegistryInformation     `json:"confluent_schema_registry,omitempty"`
	AWSGlue                 []GlueSchemaRegistryInformation `json:"aws_glue,omitempty"`
}

// UpsertConfluentSchemaRegistry inserts or updates a Confluent SR entry, matched by URL
func (s *SchemaRegistriesState) UpsertConfluentSchemaRegistry(sr SchemaRegistryInformation) {
	for i, existing := range s.ConfluentSchemaRegistry {
		if existing.URL == sr.URL {
			s.ConfluentSchemaRegistry[i] = sr
			return
		}
	}
	s.ConfluentSchemaRegistry = append(s.ConfluentSchemaRegistry, sr)
}

// UpsertGlueSchemaRegistry inserts or updates a Glue SR entry, matched by RegistryName+Region
func (s *SchemaRegistriesState) UpsertGlueSchemaRegistry(gr GlueSchemaRegistryInformation) {
	for i, existing := range s.AWSGlue {
		if existing.RegistryName == gr.RegistryName && existing.Region == gr.Region {
			s.AWSGlue[i] = gr
			return
		}
	}
	s.AWSGlue = append(s.AWSGlue, gr)
}

type GlueSchemaRegistryInformation struct {
	RegistryName string       `json:"registry_name"`
	RegistryArn  string       `json:"registry_arn"`
	Region       string       `json:"region"`
	Schemas      []GlueSchema `json:"schemas"`
}

type GlueSchema struct {
	SchemaName string              `json:"schema_name"`
	SchemaArn  string              `json:"schema_arn"`
	DataFormat string              `json:"data_format"`
	Versions   []GlueSchemaVersion `json:"versions"`
	Latest     *GlueSchemaVersion  `json:"latest_version"`
}

type GlueSchemaVersion struct {
	SchemaDefinition string    `json:"schema_definition"`
	DataFormat       string    `json:"data_format"`
	VersionNumber    int64     `json:"version_number"`
	Status           string    `json:"status"`
	CreatedDate      time.Time `json:"created_date"`
}
