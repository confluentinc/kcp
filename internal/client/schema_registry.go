package client

import (
	"crypto/tls"
	"fmt"
	"net/http"

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

func configureUnauthenticated(srConfig *schemaregistry.Config) {
	// No authentication configuration needed
}

// NewSchemaRegistryClient creates a new Schema Registry client for the given URL
func NewSchemaRegistryClient(url string, opts ...SchemaRegistryOption) (schemaregistry.Client, error) {
	// Default configuration (unauthenticated)
	config := SchemaRegistryConfig{
		authType: types.SchemaRegistryAuthTypeUnauthenticated,
	}

	// Apply all options
	for _, opt := range opts {
		opt(&config)
	}

	srConfig := schemaregistry.NewConfig(url)

	// TODO: delete this after dev testing of skip ssl verification
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	srConfig.HTTPClient = &http.Client{Transport: transport}

	switch config.authType {
	case types.SchemaRegistryAuthTypeBasicAuth:
		configureBasicAuth(srConfig, config.username, config.password)
	case types.SchemaRegistryAuthTypeUnauthenticated:
		configureUnauthenticated(srConfig)
	default:
		return nil, fmt.Errorf("❌ Auth type: %v not yet supported", config.authType)
	}

	schemaRegistryClient, err := schemaregistry.NewClient(srConfig)
	if err != nil {
		return nil, err
	}

	return schemaRegistryClient, nil
}
