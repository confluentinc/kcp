package client

import (
	"fmt"

	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry"
)

type SchemaRegistryConfig struct {
	URL      string
	Username string
	Password string
}

func NewSchemaRegistryClient(config SchemaRegistryConfig) (schemaregistry.Client, error) {
	schemaRegistryClient, err := schemaregistry.NewClient(schemaregistry.NewConfig(config.URL))
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to create schema registry client: %v", err)
	}

	return schemaRegistryClient, nil
}