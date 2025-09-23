package client

import (
	"fmt"
	
	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry"

	// TODO: delete this dev import
	"crypto/tls"
	"net/http"
)

type SchemaRegistryConfig struct {
	URL                 string
	Username            string
	Password            string
	SkipSSLVerification bool  // TODO: delete this dev flag
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