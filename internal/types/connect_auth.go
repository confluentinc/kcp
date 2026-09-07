package types

// ConnectAuthMethod identifies how kcp authenticates to a self-managed Kafka
// Connect cluster's REST API.
type ConnectAuthMethod string

const (
	ConnectAuthMethodBasicAuth       ConnectAuthMethod = "BasicAuth"
	ConnectAuthMethodTls             ConnectAuthMethod = "TLS"
	ConnectAuthMethodUnauthenticated ConnectAuthMethod = "Unauthenticated"
)

// ConnectBasicAuth holds HTTP Basic credentials for a Basic-auth-protected
// Connect REST endpoint.
type ConnectBasicAuth struct {
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
