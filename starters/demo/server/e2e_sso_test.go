package main

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

const e2eSSOSeamsBlock = `    allow-private-egress: false
    dns-resolver: ""
    oidc-ca-file: ""`

func testResolverAddress(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("binding a loopback UDP socket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn.LocalAddr().String()
}

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

// TestE2ERunStartsWithTheEnterpriseSSOSeams holds that a run over seams pointed at the local fixtures
// reaches ready, lists the enterprise method, and reports the loopback relaxation.
func TestE2ERunStartsWithTheEnterpriseSSOSeams(t *testing.T) {
	t.Parallel()
	binary := e2eBinary(t)
	serve, ports := startServing(t, func(ports []int) *process {
		root := t.TempDir()
		body := e2eConfigBody(t, root,
			fmt.Sprintf("127.0.0.1:%d", ports[backendListener]),
			filepath.Join(root, "e2e.db"))
		if !strings.Contains(body, e2eSSOSeamsBlock) {
			t.Fatalf("the e2e configuration holds no enterprise SSO defaults block (%q)", e2eSSOSeamsBlock)
		}
		body = strings.Replace(body, e2eSSOSeamsBlock, fmt.Sprintf(
			"    allow-private-egress: true\n    dns-resolver: %q\n    oidc-ca-file: %q",
			testResolverAddress(t), testCAFile(t)), 1)
		config := writeConfigFile(t, body)
		seedE2E(t, binary, config)
		return startE2E(t, binary, config)
	})

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

// TestE2ERunStartsWithTheEnterpriseSSOSeamsUnset holds that the deployment default of empty seams and
// no relaxation still reaches ready.
func TestE2ERunStartsWithTheEnterpriseSSOSeamsUnset(t *testing.T) {
	t.Parallel()
	var sso struct {
		AllowPrivateEgress bool   `config:"allow-private-egress"`
		DNSResolver        string `config:"dns-resolver"`
		OIDCCAFile         string `config:"oidc-ca-file"`
	}
	readEffective(t, "e2e/config.yaml", "file:e2e/config.yaml", "authn.sso", &sso)
	if sso.AllowPrivateEgress || sso.DNSResolver != "" || sso.OIDCCAFile != "" {
		t.Fatalf("the e2e configuration states the enterprise SSO seams: %+v; a deployment keeps "+
			"the relaxation off and both seams empty", sso)
	}

	binary := e2eBinary(t)
	serve, _ := startServing(t, func(ports []int) *process {
		config := "file://" + e2eConfig(t,
			fmt.Sprintf("127.0.0.1:%d", ports[backendListener]))
		seedE2E(t, binary, config)
		return startE2E(t, binary, config)
	})

	if strings.Contains(serve.output(), "authn.sso.allow-private-egress") {
		t.Errorf("the run reports the SSO loopback relaxation as on although the configuration keeps "+
			"it off:\n%s", serve.output())
	}
}

// TestE2ERunServesTheSAMLLoginMethod holds that the login discovery lists the enterprise SAML method
// beside OIDC.
func TestE2ERunServesTheSAMLLoginMethod(t *testing.T) {
	t.Parallel()
	binary := e2eBinary(t)
	_, ports := startServing(t, func(ports []int) *process {
		config := "file://" + e2eConfig(t,
			fmt.Sprintf("127.0.0.1:%d", ports[backendListener]))
		seedE2E(t, binary, config)
		return startE2E(t, binary, config)
	})

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
