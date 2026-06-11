package types

// AuthType represents the different authentication types supported by MSK clusters
type AuthType string

const (
	AuthTypeSASLSCRAM                AuthType = "SASL/SCRAM"
	AuthTypeIAM                      AuthType = "SASL/IAM"
	AuthTypeTLS                      AuthType = "TLS"
	AuthTypeUnauthenticatedPlaintext AuthType = "Unauthenticated (Plaintext)"
	AuthTypeUnauthenticatedTLS       AuthType = "Unauthenticated (TLS Encryption)"
	AuthTypeSASLPlain                AuthType = "SASL/PLAIN"
)

// SchemaRegistryAuthType represents the different authentication types supported by Schema Registry
type SchemaRegistryAuthType string

const (
	SchemaRegistryAuthTypeUnauthenticated SchemaRegistryAuthType = "Unauthenticated"
	SchemaRegistryAuthTypeBasicAuth       SchemaRegistryAuthType = "BasicAuth"
)

func (a AuthType) IsValid() bool {
	switch a {
	case AuthTypeSASLSCRAM, AuthTypeIAM, AuthTypeTLS, AuthTypeUnauthenticatedPlaintext, AuthTypeUnauthenticatedTLS, AuthTypeSASLPlain:
		return true
	default:
		return false
	}
}

// Values returns all possible AuthType values as strings
func (a AuthType) Values() []string {
	return AllAuthTypes()
}

// AllAuthTypes returns all possible AuthType values as strings
// This can be called statically without needing an AuthType instance
func AllAuthTypes() []string {
	return []string{
		string(AuthTypeSASLSCRAM),
		string(AuthTypeIAM),
		string(AuthTypeTLS),
		string(AuthTypeUnauthenticatedPlaintext),
		string(AuthTypeUnauthenticatedTLS),
		string(AuthTypeSASLPlain),
	}
}
