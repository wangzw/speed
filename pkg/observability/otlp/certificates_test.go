package otlp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// authority is a private certificate authority a case makes on the spot, so
// that no certificate is kept in the repository.
type authority struct {
	dir  string
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	file string
	pool *x509.CertPool
}

func newAuthority(t *testing.T) *authority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating the authority's key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          serialNumber(t),
		Subject:               pkix.Name{CommonName: "otlp test authority"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("signing the authority's certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing the authority's certificate: %v", err)
	}
	a := &authority{dir: t.TempDir(), cert: cert, key: key, pool: x509.NewCertPool()}
	a.pool.AddCert(cert)
	a.file = a.write(t, "ca.pem", "CERTIFICATE", der)
	return a
}

type issued struct {
	pair     tls.Certificate
	certFile string
	keyFile  string
}

func (a *authority) issue(t *testing.T, name string, usage x509.ExtKeyUsage) issued {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating the %s key: %v", name, err)
	}
	template := &x509.Certificate{
		SerialNumber: serialNumber(t),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.cert, &key.PublicKey, a.key)
	if err != nil {
		t.Fatalf("signing the %s certificate: %v", name, err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("encoding the %s key: %v", name, err)
	}
	out := issued{
		certFile: a.write(t, name+".pem", "CERTIFICATE", der),
		keyFile:  a.write(t, name+"-key.pem", "PRIVATE KEY", keyDER),
	}
	out.pair = tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	return out
}

func (a *authority) write(t *testing.T, name, blockType string, der []byte) string {
	t.Helper()
	path := filepath.Join(a.dir, name)
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return path
}

func serialNumber(t *testing.T) *big.Int {
	t.Helper()
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		t.Fatalf("drawing a serial number: %v", err)
	}
	return n
}
