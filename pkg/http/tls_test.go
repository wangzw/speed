package http

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	nethttp "net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeSelfSignedCertificate writes a certificate for the loopback address and
// its key into dir, and returns the two paths and the pool that trusts it.
func writeSelfSignedCertificate(t *testing.T, dir string) (certFile, keyFile string, roots *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating the certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("encoding the key: %v", err)
	}
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	if err = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("writing the certificate: %v", err)
	}
	if err = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatalf("writing the key: %v", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing the certificate back: %v", err)
	}
	roots = x509.NewCertPool()
	roots.AddCert(parsed)
	return certFile, keyFile, roots
}

// getOverTLS sends one GET for the root of the endpoint at addr through client,
// whose Timeout is the request's deadline.
func getOverTLS(client *nethttp.Client, addr string) (*nethttp.Response, error) {
	req, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodGet, "https://"+addr+"/", nil)
	if err != nil {
		return nil, err
	}
	return client.Do(req)
}

// TestATLSEndpointOffersHTTP2 pins that serving TLS through a listener this
// module builds itself — which it does so that a certificate that cannot be
// loaded fails the startup — keeps what the standard library's own TLS path
// offers: HTTP/2, negotiated through ALPN, with HTTP/1.1 beside it.
func TestATLSEndpointOffersHTTP2(t *testing.T) {
	certFile, keyFile, roots := writeSelfSignedCertificate(t, t.TempDir())
	s := testListener("public")
	s.tlsCertFile, s.tlsKeyFile = certFile, keyFile
	logger, _ := newRecordingLogger()
	_, addr := startTestEndpoint(t, s, nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusNoContent)
	}), logger)

	for _, c := range []struct {
		name      string
		protocols []string
		want      int
	}{
		{"a client offering h2", []string{"h2", "http/1.1"}, 2},
		{"a client offering only HTTP/1.1", []string{"http/1.1"}, 1},
	} {
		transport := &nethttp.Transport{
			TLSClientConfig:   &tls.Config{RootCAs: roots, NextProtos: c.protocols, MinVersion: tls.VersionTLS12},
			ForceAttemptHTTP2: len(c.protocols) > 1,
		}
		client := &nethttp.Client{Transport: transport, Timeout: waitDeadline}
		resp, err := getOverTLS(client, addr)
		if err != nil {
			t.Fatalf("%s: the request failed: %v", c.name, err)
		}
		_ = resp.Body.Close()
		transport.CloseIdleConnections()
		if resp.ProtoMajor != c.want {
			t.Errorf("%s: the endpoint answered over %s, want HTTP/%d", c.name, resp.Proto, c.want)
		}
	}
}

// TestAnHTTP2RequestIsDrained pins that a request in flight over HTTP/2 is held
// by the drain like one over HTTP/1.1, and learns that the drain has begun.
// An HTTP/2 connection carries its requests as streams, and its shutdown is
// the protocol's own; a drain that saw only HTTP/1.1 connections would end
// with those streams still running.
func TestAnHTTP2RequestIsDrained(t *testing.T) {
	certFile, keyFile, roots := writeSelfSignedCertificate(t, t.TempDir())
	s := testListener("public")
	s.tlsCertFile, s.tlsKeyFile = certFile, keyFile
	logger, _ := newRecordingLogger()

	entered := make(chan struct{}, 1)
	signalled := make(chan bool, 1)
	l, addr := startTestEndpoint(t, s, nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		entered <- struct{}{}
		select {
		case <-Draining(r.Context()):
			signalled <- true
		case <-time.After(waitDeadline):
			signalled <- false
		}
		w.WriteHeader(nethttp.StatusNoContent)
	}), logger)

	transport := &nethttp.Transport{
		TLSClientConfig:   &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2: true,
	}
	t.Cleanup(transport.CloseIdleConnections)
	client := &nethttp.Client{Transport: transport, Timeout: 2 * waitDeadline}
	answered := make(chan *nethttp.Response, 1)
	go func() {
		resp, err := getOverTLS(client, addr)
		if err != nil {
			answered <- nil
			return
		}
		_ = resp.Body.Close()
		answered <- resp
	}()
	waitFor(t, entered, "the HTTP/2 request to reach the handler")

	if err := l.stop(t.Context()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := l.close(t.Context()); err != nil {
		t.Fatalf("the drain of an HTTP/2 request reported %v", err)
	}
	if !<-signalled {
		t.Error("the HTTP/2 request never learnt that the drain had begun")
	}
	resp := <-answered
	switch {
	case resp == nil:
		t.Error("the HTTP/2 request in flight was cut rather than drained")
	case resp.ProtoMajor != 2:
		t.Errorf("the request went over %s, so this case did not exercise HTTP/2", resp.Proto)
	case resp.StatusCode != nethttp.StatusNoContent:
		t.Errorf("the drained request was answered %d, want %d", resp.StatusCode, nethttp.StatusNoContent)
	}
}
