package types

// ConnectAuthMethod identifies how kcp authenticates to a self-managed Kafka
// Connect cluster's REST API.
type ConnectAuthMethod string

const (
	ConnectAuthMethodSaslScram       ConnectAuthMethod = "SASL/SCRAM"
	ConnectAuthMethodTls             ConnectAuthMethod = "TLS"
	ConnectAuthMethodUnauthenticated ConnectAuthMethod = "Unauthenticated"
)

// ConnectSaslScramAuth holds basic-auth credentials for a SASL/SCRAM-protected
// Connect REST endpoint.
type ConnectSaslScramAuth struct {
	Username string
	Password string
}

// ConnectTlsAuth holds the certificate paths for a mTLS-protected Connect REST
// endpoint.
type ConnectTlsAuth struct {
	CACert     string
	ClientCert string
	ClientKey  string
}
