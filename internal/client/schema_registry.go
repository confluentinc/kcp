package client

import (
	"fmt"
	"net/http"

	"github.com/confluentinc/confluent-kafka-go/v2/schemaregistry"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
)

// SchemaRegistryConfig holds the configuration for creating a Schema Registry client
type SchemaRegistryConfig struct {
	authType types.SchemaRegistryAuthType
	username string
	password string
	// TLS transport options (orthogonal to authType): caCert verifies an HTTPS SR
	// behind a private/internal CA; insecureSkip disables endpoint (hostname)
	// verification for test environments.
	caCert       string
	insecureSkip bool
	// mTLS client identity (authType == MTLS): the cert/key kcp presents.
	clientCert string
	clientKey  string
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

// WithTLS configures TLS transport for an HTTPS Schema Registry endpoint. It is
// orthogonal to the auth method: caCert (empty → system trust roots) verifies the
// server's certificate against a private/internal CA, and insecureSkip disables
// certificate verification entirely (test environments only). Both are applied by
// injecting a custom HTTPClient in NewSchemaRegistryClient — NOT via the confluent
// client's SslCaLocation/SslDisableEndpointVerification fields (the latter only logs
// and never actually skips verification).
func WithTLS(caCert string, insecureSkip bool) SchemaRegistryOption {
	return func(config *SchemaRegistryConfig) {
		config.caCert = caCert
		config.insecureSkip = insecureSkip
	}
}

// WithMTLS configures mutual-TLS authentication: kcp presents clientCert/clientKey
// to the schema registry. Pair with WithTLS to trust a private/internal server CA.
func WithMTLS(clientCert, clientKey string) SchemaRegistryOption {
	return func(config *SchemaRegistryConfig) {
		config.authType = types.SchemaRegistryAuthTypeMTLS
		config.clientCert = clientCert
		config.clientKey = clientKey
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
	case types.SchemaRegistryAuthTypeMTLS:
		// mTLS client cert is applied via the injected HTTPClient's TLS config below.
	default:
		return nil, fmt.Errorf("auth type: %v not supported", config.authType)
	}

	// TLS transport (orthogonal to the auth method): custom CA, insecure-skip, and the
	// mTLS client cert are all applied by injecting our own *http.Client built via the
	// shared utils.TLSClientConfig helper. This is deliberate: the confluent v2 client's
	// SslDisableEndpointVerification only logs a warning (it never sets
	// tls.Config.InsecureSkipVerify), so relying on it makes --insecure-skip-tls-verify a
	// no-op. NewRestService uses conf.HTTPClient verbatim when set, so this makes skip real
	// and routes CA + mTLS through the same helper as every other client.
	if config.caCert != "" || config.insecureSkip || config.clientCert != "" {
		pool, err := utils.OptionalCACertPool(config.caCert)
		if err != nil {
			return nil, err
		}
		tlsCfg := utils.TLSClientConfig(pool, config.insecureSkip)
		if config.clientCert != "" || config.clientKey != "" {
			if err := utils.AppendClientCert(tlsCfg, config.clientCert, config.clientKey); err != nil {
				return nil, err
			}
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = tlsCfg
		srConfig.HTTPClient = &http.Client{Transport: transport}
	}

	schemaRegistryClient, err := schemaregistry.NewClient(srConfig)
	if err != nil {
		return nil, err
	}

	return schemaRegistryClient, nil
}
