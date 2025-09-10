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
	srConfig := schemaregistry.NewConfig(config.URL)
	
	if config.Username != "" && config.Password != "" {
			srConfig.BasicAuthCredentialsSource = "USER_INFO"
			srConfig.BasicAuthUserInfo = config.Username + ":" + config.Password
			fmt.Printf("Setting BasicAuthUserInfo: %s\n", srConfig.BasicAuthUserInfo)
	}
	
	schemaRegistryClient, err := schemaregistry.NewClient(srConfig)
	if err != nil {
			return nil, fmt.Errorf("❌ Failed to create schema registry client: %v", err)
	}

	return schemaRegistryClient, nil
}