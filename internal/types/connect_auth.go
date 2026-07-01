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

// ConnectTlsAuth holds the TLS transport options for reaching a Connect REST
// endpoint over HTTPS. CACert (verify the server against a private/internal CA)
// and InsecureSkipVerify apply to ANY auth method; ClientCert/ClientKey are the
// client identity for mTLS (--use-tls).
type ConnectTlsAuth struct {
	CACert             string
	ClientCert         string
	ClientKey          string
	InsecureSkipVerify bool
}
