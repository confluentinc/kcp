package clusterlink

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/types"
)

// LinkAuth is the Surface-3 link→source authentication, derived from the source
// credentials and applied to the cluster-link config (security.protocol, SASL,
// and TLS material). It is target-agnostic.
type LinkAuth struct {
	SecurityProtocol string // PLAINTEXT | SSL | SASL_SSL
	SaslMechanism    string // SCRAM-SHA-256 | SCRAM-SHA-512 | PLAIN | ""
	SaslJaasConfig   string // "" unless SASL
	// TLS material (paths to PEM files) for SSL/SASL_SSL truststore and mTLS keystore.
	// CACertPath is populated from the source's ca_cert for the mTLS (tls),
	// sasl_scram, sasl_plain, and unauthenticated_tls credential methods.
	CACertPath     string // truststore CA path
	ClientCertPath string // mTLS keystore cert chain ("" unless mTLS)
	ClientKeyPath  string // mTLS keystore key ("" unless mTLS)
}

// LinkAuthFromSource maps the source's single enabled auth method to link config.
func LinkAuthFromSource(c types.OSKClusterAuth) (LinkAuth, error) {
	authType, err := c.GetSelectedAuthType()
	if err != nil {
		return LinkAuth{}, err
	}
	switch authType {
	case types.AuthTypeUnauthenticatedPlaintext:
		return LinkAuth{SecurityProtocol: "PLAINTEXT"}, nil
	case types.AuthTypeUnauthenticatedTLS:
		// TLS encryption, no client auth. CACertPath is populated from the source's
		// ca_cert when set; required when the source uses a private CA that the link
		// must trust.
		return LinkAuth{SecurityProtocol: "SSL", CACertPath: c.AuthMethod.UnauthenticatedTLS.CACert}, nil
	case types.AuthTypeSASLSCRAM:
		mech := types.NormalizeSaslMechanism(c.AuthMethod.SASLScram.Mechanism) // "SCRAM-SHA-256"/"512"
		return LinkAuth{
			SecurityProtocol: "SASL_SSL",
			SaslMechanism:    mech,
			SaslJaasConfig:   scramJaas(c.AuthMethod.SASLScram.Username, c.AuthMethod.SASLScram.Password),
			CACertPath:       c.AuthMethod.SASLScram.CACert,
		}, nil
	case types.AuthTypeSASLPlain:
		return LinkAuth{
			SecurityProtocol: "SASL_SSL",
			SaslMechanism:    "PLAIN",
			SaslJaasConfig:   plainJaas(c.AuthMethod.SASLPlain.Username, c.AuthMethod.SASLPlain.Password),
			CACertPath:       c.AuthMethod.SASLPlain.CACert,
		}, nil
	case types.AuthTypeTLS: // mTLS
		return LinkAuth{
			SecurityProtocol: "SSL",
			CACertPath:       c.AuthMethod.TLS.CACert,
			ClientCertPath:   c.AuthMethod.TLS.ClientCert,
			ClientKeyPath:    c.AuthMethod.TLS.ClientKey,
		}, nil
	default:
		return LinkAuth{}, fmt.Errorf("unsupported source auth type for cluster link: %v", authType)
	}
}

func scramJaas(u, p string) string {
	return fmt.Sprintf(`org.apache.kafka.common.security.scram.ScramLoginModule required username="%s" password="%s";`, u, p)
}

func plainJaas(u, p string) string {
	return fmt.Sprintf(`org.apache.kafka.common.security.plain.PlainLoginModule required username="%s" password="%s";`, u, p)
}
