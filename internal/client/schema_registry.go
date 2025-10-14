package client

import (
	"crypto/tls"
	"fmt"
	"net/http"

	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry"
)

type SchemaRegistryConfig struct {
	URL                 string
	Username            string
	Password            string
	SkipSSLVerification bool // TODO: delete this dev flag
}

func NewSchemaRegistryClient(config SchemaRegistryConfig) (schemaregistry.Client, error) {
	srConfig := schemaregistry.NewConfig(config.URL)

	// TODO: delete this if block after dev testing of skip ssl verification
	if config.SkipSSLVerification {
		transport := &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
		srConfig.HTTPClient = &http.Client{Transport: transport}
	}

	// Configure basic auth if credentials provided
	if config.Username != "" && config.Password != "" {
		srConfig.BasicAuthCredentialsSource = "USER_INFO"
		srConfig.BasicAuthUserInfo = fmt.Sprintf("%s:%s", config.Username, config.Password)
	}

	schemaRegistryClient, err := schemaregistry.NewClient(srConfig)
	if err != nil {
		return nil, err
	}

	return schemaRegistryClient, nil
}
