package clusterlink

import (
	"fmt"
	"os"

	svclink "github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/types"
)

// LinkAuth is the Surface-3 link→source authentication, derived from the source
// credentials and applied to the cluster-link config (security.protocol, SASL,
// and TLS material). It is target-agnostic.
type LinkAuth struct {
	SecurityProtocol string // PLAINTEXT | SSL | SASL_SSL | SASL_PLAINTEXT
	SaslMechanism    string // SCRAM-SHA-256 | SCRAM-SHA-512 | PLAIN | ""
	SaslJaasConfig   string // "" unless SASL
	// TLS material (paths to PEM files) for SSL/SASL_SSL truststore and mTLS keystore.
	// CACertPath is populated from the source's ca_cert for the mTLS (tls),
	// sasl_scram, and unauthenticated_tls credential methods.
	// sasl_plain does NOT carry a truststore — it uses SASL_PLAINTEXT (no TLS).
	CACertPath     string // truststore CA path
	ClientCertPath string // mTLS keystore cert chain ("" unless mTLS)
	ClientKeyPath  string // mTLS keystore key ("" unless mTLS)
}

// LoadTLS reads the PEM material referenced by the auth's cert paths into the
// inline form a cluster-link request needs. Returns (nil, nil) when no TLS
// material is required (PLAINTEXT).
func (a LinkAuth) LoadTLS() (*svclink.SourceTLSMaterial, error) {
	if a.CACertPath == "" && a.ClientCertPath == "" && a.ClientKeyPath == "" {
		return nil, nil
	}
	read := func(p string) (string, error) {
		if p == "" {
			return "", nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return "", fmt.Errorf("reading TLS material %s: %w", p, err)
		}
		return string(b), nil
	}
	m := &svclink.SourceTLSMaterial{}
	var err error
	if m.CACertPEM, err = read(a.CACertPath); err != nil {
		return nil, err
	}
	if m.ClientCertPEM, err = read(a.ClientCertPath); err != nil {
		return nil, err
	}
	if m.ClientKeyPEM, err = read(a.ClientKeyPath); err != nil {
		return nil, err
	}
	return m, nil
}

// LinkAuthFromSource maps the source's single enabled auth method to link config.
func LinkAuthFromSource(c types.KafkaSourceConn) (LinkAuth, error) {
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
		// SASL/PLAIN uses SASL_PLAINTEXT to match KCP's source read path
		// (WithSASLPlainAuthNoTLS): no TLS, so no truststore/CA.
		return LinkAuth{
			SecurityProtocol: "SASL_PLAINTEXT",
			SaslMechanism:    "PLAIN",
			SaslJaasConfig:   plainJaas(c.AuthMethod.SASLPlain.Username, c.AuthMethod.SASLPlain.Password),
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
