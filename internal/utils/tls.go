package utils

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// TLSClientConfig assembles a *tls.Config from an already-resolved CA pool
// (nil → system trust roots) and the insecure-skip flag. It is the single place a
// client TLS config is built, so CA trust and skip-verify behave identically for
// every REST, metrics, and broker client. Callers resolve a CA path via
// OptionalCACertPool first, so a bad/unreadable CA fails closed at the call site
// (and this stays infallible, usable from functional-option builders that cannot
// return an error). For mutual TLS, add the client cert with AppendClientCert.
func TLSClientConfig(caPool *x509.CertPool, insecureSkip bool) *tls.Config {
	return &tls.Config{ //nolint:gosec // insecureSkip is a documented, caller-controlled opt-in
		RootCAs:            caPool,
		InsecureSkipVerify: insecureSkip,
	}
}

// AppendClientCert loads a client certificate/key pair and adds it to cfg for
// mutual TLS. Shared by every mTLS-capable client so cert loading is uniform and
// fails closed on a bad pair.
func AppendClientCert(cfg *tls.Config, certFile, keyFile string) error {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("loading client certificate/key: %w", err)
	}
	cfg.Certificates = append(cfg.Certificates, cert)
	return nil
}

// CACertPool reads a PEM CA bundle from path and returns a cert pool trusting
// it. It fails CLOSED: an unreadable file, or a file containing no valid PEM
// certificate, returns an error rather than silently falling back to system
// roots. Call it only when a CA path is configured — an empty path is a caller
// bug and returns an error.
func CACertPool(path string) (*x509.CertPool, error) {
	if path == "" {
		return nil, fmt.Errorf("CA certificate path is empty")
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading CA certificate %q: %w", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("CA certificate %q contains no valid PEM certificate", path)
	}
	return pool, nil
}

// OptionalCACertPool is the "optional CA path" adapter for TLS option builders:
// an empty path yields (nil, nil) — meaning "use the system trust store" — while
// a non-empty path is loaded fail-closed via CACertPool. It collapses the
// repeated `if path != "" { pool, err := CACertPool(path); ... }` block at every
// Jolokia/Prometheus/Kafka TLS call site into one call.
func OptionalCACertPool(path string) (*x509.CertPool, error) {
	if path == "" {
		return nil, nil
	}
	return CACertPool(path)
}
