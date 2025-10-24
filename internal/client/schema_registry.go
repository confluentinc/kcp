package client

import (
	"fmt"

	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry"
	"github.com/confluentinc/kcp/internal/types"
)

// SchemaRegistryConfig holds the configuration for creating a Schema Registry client
type SchemaRegistryConfig struct {
	authType types.SchemaRegistryAuthType
	username string
	password string
}

// SchemaRegistryOption is a function type for configuring the Schema Registry client
type SchemaRegistryOption func(*SchemaRegistryConfig)

// WithBasicAuth configures the Schema Registry client to use basic authentication
func WithBasicAuth(username, password string) SchemaRegistryOption {
	return func(config *SchemaRegistryConfig) {
		config.authType = types.SchemaRegistryAuthTypeBasicAuth
		config.username = username
		config.password = password
	}
}

// WithUnauthenticated configures the Schema Registry client to use no authentication
func WithUnauthenticated() SchemaRegistryOption {
	return func(config *SchemaRegistryConfig) {
		config.authType = types.SchemaRegistryAuthTypeUnauthenticated
	}
}

func configureBasicAuth(srConfig *schemaregistry.Config, username, password string) {
	srConfig.BasicAuthCredentialsSource = "USER_INFO"
	srConfig.BasicAuthUserInfo = fmt.Sprintf("%s:%s", username, password)
}

// NewSchemaRegistryClient creates a new Schema Registry client for the given URL
func NewSchemaRegistryClient(url string, opts ...SchemaRegistryOption) (schemaregistry.Client, error) {
	config := SchemaRegistryConfig{
		authType: types.SchemaRegistryAuthTypeUnauthenticated,
	}

	for _, opt := range opts {
		opt(&config)
	}

	srConfig := schemaregistry.NewConfig(url)

	switch config.authType {
	case types.SchemaRegistryAuthTypeBasicAuth:
		configureBasicAuth(srConfig, config.username, config.password)
	case types.SchemaRegistryAuthTypeUnauthenticated:
		// no authentication configuration needed
	default:
		return nil, fmt.Errorf("auth type: %v not supported", config.authType)
	}

	schemaRegistryClient, err := schemaregistry.NewClient(srConfig)
	if err != nil {
		return nil, err
	}

	schemaRegistryClient.GetAllContexts()
	return schemaRegistryClient, nil
}
