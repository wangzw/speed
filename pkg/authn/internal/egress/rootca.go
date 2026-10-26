package egress

import (
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
)

// RootCAsFromFile reads the certificates in a PEM file and appends them to the
// system roots, for a deployment whose identity provider chains to a root the
// system does not hold. The system roots stay trusted: the file extends them,
// it does not replace them. An empty path returns a nil pool, which leaves the
// system roots in place; a path that cannot be read or that holds no
// certificate is an error, so a configured file that does not work is never
// silently ignored.
//
// The caller passes the result as [Options].RootCAs. Verification itself is never
// switched off.
func RootCAsFromFile(path string) (*x509.CertPool, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("egress: reading the root CA file: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("egress: reading the system roots the file extends: %w", err)
	}
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("egress: the root CA file holds no certificate")
	}
	return pool, nil
}
