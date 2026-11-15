package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beevik/etree"
	dsig "github.com/russellhaering/goxmldsig"
)

// writeIdentityProvider writes a throwaway RSA key and its self-signed
// certificate as the key.pem and cert.pem the command reads, and returns the
// directory and the certificate.
func writeIdentityProvider(t *testing.T) (string, *x509.Certificate) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating the key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "samlsign test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating the certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing the certificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("encoding the key: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, keyFile), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatalf("writing the key: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, certFile), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("writing the certificate: %v", err)
	}
	return dir, cert
}

const unsignedResponse = `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ` +
	`xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_r1" Version="2.0" ` +
	`IssueInstant="2026-11-16T00:00:00Z"><saml:Issuer>https://idp.example/entity</saml:Issuer></samlp:Response>`

// TestTheSignedResponseValidatesAgainstTheCertificate runs the command the way
// the harness does and validates what it wrote with the library the product
// validates with: a response the command signed is accepted under the
// certificate it was given and refused under another.
func TestTheSignedResponseValidatesAgainstTheCertificate(t *testing.T) {
	dir, cert := writeIdentityProvider(t)
	_, other := writeIdentityProvider(t)

	var out bytes.Buffer
	if err := run([]string{"-dir", dir}, strings.NewReader(unsignedResponse), &out); err != nil {
		t.Fatalf("signing: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(out.String())
	if err != nil {
		t.Fatalf("the output is not base64: %v", err)
	}
	doc := etree.NewDocument()
	if err := doc.ReadFromBytes(raw); err != nil {
		t.Fatalf("the output is not XML: %v", err)
	}

	validate := func(trusted *x509.Certificate) error {
		ctx := dsig.NewDefaultValidationContext(&dsig.MemoryX509CertificateStore{Roots: []*x509.Certificate{trusted}})
		_, err := ctx.Validate(doc.Root())
		return err
	}
	if err := validate(cert); err != nil {
		t.Errorf("the response does not validate under the certificate it was signed for: %v", err)
	}
	if err := validate(other); err == nil {
		t.Error("the response validates under a certificate it was not signed for")
	}
}

// TestTheCommandRefusesWhatItCannotSign holds the failure answers: no directory
// named, and a document that is not XML.
func TestTheCommandRefusesWhatItCannotSign(t *testing.T) {
	dir, _ := writeIdentityProvider(t)
	if err := run(nil, strings.NewReader(unsignedResponse), &bytes.Buffer{}); err == nil {
		t.Error("a run with no -dir signed a response")
	}
	if err := run([]string{"-dir", dir}, strings.NewReader("not xml"), &bytes.Buffer{}); err == nil {
		t.Error("a document that is not XML was signed")
	}
}
