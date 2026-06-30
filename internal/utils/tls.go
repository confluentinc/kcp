package utils

import (
	"crypto/x509"
	"fmt"
	"os"
)

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
