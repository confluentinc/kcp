package client

import (
	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry"
)

type SchemaRegistryConfig struct {
	URL      string
	Username string
	Password string
}

func NewSchemaRegistryClient(config SchemaRegistryConfig) (schemaregistry.Client, error) {
	return schemaregistry.NewClient(schemaregistry.NewConfig(config.URL))
}