package egress

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// rootCAFile writes a self-signed root certificate in PEM and returns its path.
func rootCAFile(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "the test root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating a certificate: %v", err)
	}
	path := filepath.Join(t.TempDir(), "root.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("writing the root CA file: %v", err)
	}
	return path
}

// TestAnEmptyRootCAPathLeavesTheSystemRoots holds that no configured file means
// a nil pool, which is the system roots.
func TestAnEmptyRootCAPathLeavesTheSystemRoots(t *testing.T) {
	pool, err := RootCAsFromFile("")
	if err != nil {
		t.Fatalf("an empty path: %v", err)
	}
	if pool != nil {
		t.Errorf("an empty path returned a pool, want nil for the system roots")
	}
}

// TestRootCAsFromFileReadsAPEMFile holds that a file of certificates becomes a
// pool.
func TestRootCAsFromFileReadsAPEMFile(t *testing.T) {
	pool, err := RootCAsFromFile(rootCAFile(t))
	if err != nil {
		t.Fatalf("a valid PEM file: %v", err)
	}
	if pool == nil {
		t.Fatal("a valid PEM file returned no pool")
	}
}

// TestRootCAsFromFileRefusesAMissingFile holds that a configured path that does
// not exist is an error, never a silent fallback to the system roots.
func TestRootCAsFromFileRefusesAMissingFile(t *testing.T) {
	if _, err := RootCAsFromFile(filepath.Join(t.TempDir(), "absent.pem")); err == nil {
		t.Fatal("a missing file returned no error")
	}
}

// TestRootCAsFromFileRefusesAFileWithNoCertificate holds that a file that holds
// no certificate is an error rather than an empty pool.
func TestRootCAsFromFileRefusesAFileWithNoCertificate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-certificate.pem")
	if err := os.WriteFile(path, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	if _, err := RootCAsFromFile(path); err == nil {
		t.Fatal("a file with no certificate returned no error")
	}
}
