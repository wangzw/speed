//go:build review

package main

// The review build's enterprise SSO assembly: the OIDC login method, the two
// outbound seams the end-to-end harness points at its local fixtures, and the
// deployment default the review configuration keeps when nothing points them.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// reviewSSOSeamsBlock is the review configuration's enterprise SSO block with
// the deployment defaults: the loopback relaxation off and both outbound seams
// empty. The end-to-end harness replaces this block whole when a spec asks for
// the local fixtures, so the block is one needle in both places.
const reviewSSOSeamsBlock = `    allow-private-egress: false
    dns-resolver: ""
    oidc-ca-file: ""`

// testResolverAddress binds a loopback UDP socket and answers its address as a
// resolver would be named. Nothing answers through it: a start reads the
// address and validates its shape, and the TXT lookup is a request-time path.
func testResolverAddress(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("binding a loopback UDP socket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn.LocalAddr().String()
}

// testCAFile mints a self-signed CA and writes its PEM to a file of the case's
// own. The enterprise OIDC method appends the file's certificates to the system
// roots as it starts, so a named but unreadable or empty file fails the start;
// this is the smallest file that is a real deployment's extra root.
func testCAFile(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating the test CA key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "speed e2e enterprise SSO CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("signing the test CA certificate: %v", err)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("writing the test CA file: %v", err)
	}
	return path
}

// TestReviewRunStartsWithTheEnterpriseSSOSeams runs the review binary over the
// review configuration with the enterprise SSO block pointed at a resolver
// address, a CA file and the loopback relaxation the local fixtures need. The
// run reaches its ready record, the login discovery lists the enterprise method
// the assembly now carries, and the startup diagnostics report the relaxation on
// the SSO module's own item — the evidence that the module read this section.
func TestReviewRunStartsWithTheEnterpriseSSOSeams(t *testing.T) {
	binary := reviewBinary(t)
	ports := freePorts(t, listenerCount)
	root := t.TempDir()
	body := reviewConfigBody(t, root,
		fmt.Sprintf("127.0.0.1:%d", ports[backendListener]),
		filepath.Join(root, "review.db"))
	if !strings.Contains(body, reviewSSOSeamsBlock) {
		t.Fatalf("the review configuration holds no enterprise SSO defaults block (%q)", reviewSSOSeamsBlock)
	}
	body = strings.Replace(body, reviewSSOSeamsBlock, fmt.Sprintf(
		"    allow-private-egress: true\n    dns-resolver: %q\n    oidc-ca-file: %q",
		testResolverAddress(t), testCAFile(t)), 1)
	config := writeConfigFile(t, body)
	seedReview(t, binary, config)

	serve := startReview(t, binary, config)
	serve.waitReady(t)

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	listing := get(t, base+"/api/v1/auth/methods")
	if listing.status != http.StatusOK {
		t.Fatalf("GET /api/v1/auth/methods answered %d: %s", listing.status, listing.body)
	}
	var discovery struct {
		Methods []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"methods"`
	}
	if err := json.Unmarshal(listing.body, &discovery); err != nil {
		t.Fatalf("reading the login discovery: %v\n%s", err, listing.body)
	}
	enterprise := false
	for _, method := range discovery.Methods {
		if method.Name == "oidc" && method.Kind == "enterprise" {
			enterprise = true
		}
	}
	if !enterprise {
		t.Errorf("the login discovery lists no enterprise oidc method: %+v", discovery.Methods)
	}

	if !strings.Contains(serve.output(), "authn.sso.allow-private-egress") {
		t.Errorf("the run reports no startup diagnostic for the SSO loopback relaxation, so the module "+
			"did not read the section the case wrote:\n%s", serve.output())
	}
}

// TestReviewRunStartsWithTheEnterpriseSSOSeamsUnset holds the deployment
// default: the review configuration leaves both seams empty and the loopback
// relaxation off, and a run over it still reaches its ready record. The values
// are read as the binary's own configuration layer resolves them, then the
// binary is started over the same file.
func TestReviewRunStartsWithTheEnterpriseSSOSeamsUnset(t *testing.T) {
	var sso struct {
		AllowPrivateEgress bool   `config:"allow-private-egress"`
		DNSResolver        string `config:"dns-resolver"`
		OIDCCAFile         string `config:"oidc-ca-file"`
	}
	readEffective(t, "review/sqlite.yaml", "file:review/sqlite.yaml", "authn.sso", &sso)
	if sso.AllowPrivateEgress || sso.DNSResolver != "" || sso.OIDCCAFile != "" {
		t.Fatalf("the review configuration states the enterprise SSO seams: %+v; a deployment keeps "+
			"the relaxation off and both seams empty", sso)
	}

	binary := reviewBinary(t)
	ports := freePorts(t, listenerCount)
	config := "file://" + reviewConfig(t,
		fmt.Sprintf("127.0.0.1:%d", ports[backendListener]))
	seedReview(t, binary, config)

	serve := startReview(t, binary, config)
	serve.waitReady(t)

	if strings.Contains(serve.output(), "authn.sso.allow-private-egress") {
		t.Errorf("the run reports the SSO loopback relaxation as on although the configuration keeps "+
			"it off:\n%s", serve.output())
	}
}

// TestReviewRunServesTheSAMLLoginMethod runs the review binary over the review
// configuration and reads the login discovery: the enterprise SAML method is
// listed beside the enterprise OIDC one. The review build names authn.sso.saml
// for a run, which a default build does not, so the method's presence is what
// shows the roster reached the assembly.
func TestReviewRunServesTheSAMLLoginMethod(t *testing.T) {
	binary := reviewBinary(t)
	ports := freePorts(t, listenerCount)
	config := "file://" + reviewConfig(t,
		fmt.Sprintf("127.0.0.1:%d", ports[backendListener]))
	seedReview(t, binary, config)

	serve := startReview(t, binary, config)
	serve.waitReady(t)

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	listing := get(t, base+"/api/v1/auth/methods")
	if listing.status != http.StatusOK {
		t.Fatalf("GET /api/v1/auth/methods answered %d: %s", listing.status, listing.body)
	}
	var discovery struct {
		Methods []struct {
			Name     string `json:"name"`
			Kind     string `json:"kind"`
			Protocol string `json:"protocol"`
		} `json:"methods"`
	}
	if err := json.Unmarshal(listing.body, &discovery); err != nil {
		t.Fatalf("reading the login discovery: %v\n%s", err, listing.body)
	}
	for _, method := range discovery.Methods {
		if method.Name == "saml" {
			if method.Kind != "enterprise" {
				t.Errorf("the saml method is listed with kind %q, want enterprise", method.Kind)
			}
			return
		}
	}
	t.Errorf("the login discovery lists no saml method: %+v", discovery.Methods)
}
