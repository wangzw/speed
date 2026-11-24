package otlp

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	stdlog "log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core/coretest"
)

// handshakes is the log sink of a TLS receiver's server, which writes
// there about the connections it refused, which is how a case tells a
// refused handshake from any other reason an export failed.
type handshakes struct {
	refusal chan struct{}
	once    sync.Once
}

func newHandshakes() *handshakes {
	return &handshakes{refusal: make(chan struct{})}
}

func (h *handshakes) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "TLS handshake error") {
		h.once.Do(func() { close(h.refusal) })
	}
	return len(p), nil
}

// lateWriter delays the first refusal line so a case can reproduce it arriving
// after the export returned.
type lateWriter struct {
	w     io.Writer
	delay time.Duration
	once  sync.Once
}

func (l *lateWriter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "TLS handshake error") {
		l.once.Do(func() { time.Sleep(l.delay) })
	}
	return l.w.Write(p)
}

func tlsReceiver(t *testing.T, w wire, ca *authority, clientAuth bool) (*receiver, *handshakes) {
	t.Helper()
	return tlsReceiverAfter(t, w, ca, clientAuth, 0)
}

// tlsReceiverAfter is tlsReceiver with its first refusal line written delay
// late; a non-positive delay writes at once.
func tlsReceiverAfter(t *testing.T, w wire, ca *authority, clientAuth bool, delay time.Duration) (*receiver, *handshakes) {
	t.Helper()
	rec := &receiver{}
	refused := newHandshakes()
	srv := httptest.NewUnstartedServer(w.handler(rec))
	srv.EnableHTTP2 = true
	var logw io.Writer = refused
	if delay > 0 {
		logw = &lateWriter{w: refused, delay: delay}
	}
	srv.Config.ErrorLog = stdlog.New(logw, "", 0)
	srv.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{ca.issue(t, "server", x509.ExtKeyUsageServerAuth).pair},
		NextProtos:   []string{"h2", "http/1.1"},
	}
	if clientAuth {
		srv.TLS.ClientAuth = tls.RequireAndVerifyClientCert
		srv.TLS.ClientCAs = ca.pool
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	rec.endpoint = srv.Listener.Addr().String()
	return rec, refused
}

func tlsConfig(w wire, rec *receiver) Config {
	return Config{
		Endpoint:     rec.endpoint,
		Protocol:     w.protocol,
		Insecure:     false,
		ProbeTimeout: defaultProbeTimeout,
	}
}

func expectDelivered(t *testing.T, cfg Config, w wire, rec *receiver) {
	t.Helper()
	exporters, err := exportersFor(t, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if spanErr, metricErr := exportOnce(exporters); spanErr != nil || metricErr != nil {
		t.Fatalf("the exports failed: spans %v, metrics %v", spanErr, metricErr)
	}
	if got := paths(rec.requests()); len(got) != 2 || got[0] != w.spanPath || got[1] != w.metricPath {
		t.Fatalf("the receiver was asked for %v, want %s then %s", got, w.spanPath, w.metricPath)
	}
}

func expectRefused(t *testing.T, cfg Config, rec *receiver, refused *handshakes) {
	t.Helper()
	exporters, err := exportersFor(t, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	spanErr, metricErr := exportWithin(exporters, coretest.Scale(time.Second))
	if spanErr == nil || metricErr == nil {
		t.Errorf("an export succeeded: spans %v, metrics %v; want both to fail at the handshake", spanErr, metricErr)
	}
	if got := rec.requests(); len(got) != 0 {
		t.Errorf("the receiver was asked for %v, want nothing past the handshake", paths(got))
	}
	// The server writes the refusal from the connection's own goroutine, with
	// no ordering against the export's return, so wait for the line to be
	// written rather than reading it once.
	timer := time.NewTimer(coretest.Scale(time.Second))
	defer timer.Stop()
	select {
	case <-refused.refusal:
	case <-timer.C:
		t.Error("the receiver's server refused no handshake, so the exports failed for some other reason")
	}
}

// TestRefusedHandshakeLogArrivesLate reproduces the race expectRefused
// guards against.
func TestRefusedHandshakeLogArrivesLate(t *testing.T) {
	for _, w := range wires {
		if w.protocol != ProtocolHTTP {
			continue
		}
		t.Run(w.protocol, func(t *testing.T) {
			ca := newAuthority(t)
			rec, refused := tlsReceiverAfter(t, w, ca, false, coretest.Scale(250*time.Millisecond))
			expectRefused(t, tlsConfig(w, rec), rec, refused)
		})
	}
}

// TestAPrivateAuthorityIsTrustedWhenCAFileNamesIt pins ca-file over both
// protocols.
func TestAPrivateAuthorityIsTrustedWhenCAFileNamesIt(t *testing.T) {
	for _, w := range wires {
		t.Run(w.protocol, func(t *testing.T) {
			ca := newAuthority(t)

			rec, _ := tlsReceiver(t, w, ca, false)
			cfg := tlsConfig(w, rec)
			cfg.CAFile = ca.file
			expectDelivered(t, cfg, w, rec)

			rec, refused := tlsReceiver(t, w, ca, false)
			expectRefused(t, tlsConfig(w, rec), rec, refused)
		})
	}
}

// TestCAFileReplacesTheHostRoots pins what ca-file loads to.
func TestCAFileReplacesTheHostRoots(t *testing.T) {
	ca := newAuthority(t)
	cfg := Config{Endpoint: "collector.example:4317", CAFile: ca.file}
	material, err := cfg.loadTLS()
	if err != nil {
		t.Fatalf("loadTLS: %v", err)
	}
	if material.roots == nil || !material.roots.Equal(ca.pool) {
		t.Error("the roots ca-file loads to are not the file's certificate alone, so the host's root " +
			"certificates still verify the collector")
	}

	// What the exporters are given, not only what loadTLS returns: the roots are
	// the file's pool, and nothing beside them widens what verifies.
	given := clientTLS(material)
	if given.RootCAs == nil || !given.RootCAs.Equal(ca.pool) {
		t.Error("the TLS configuration handed to the exporters does not verify against ca-file alone")
	}
	if given.InsecureSkipVerify || given.VerifyConnection != nil || given.VerifyPeerCertificate != nil {
		t.Error("the TLS configuration handed to the exporters verifies the collector by more than its roots")
	}
	if none := clientTLS(tlsMaterial{}); none.RootCAs != nil || len(none.Certificates) != 0 {
		t.Error("with no certificate item the TLS configuration is not the host's roots and no client certificate")
	}
}

// TestEveryCertificateInACAFileIsTrusted pins a bundle: a collector whose
// authority is not the first certificate of ca-file is reached all the same.
func TestEveryCertificateInACAFileIsTrusted(t *testing.T) {
	for _, w := range wires {
		t.Run(w.protocol, func(t *testing.T) {
			unrelated, ca := newAuthority(t), newAuthority(t)
			rec, _ := tlsReceiver(t, w, ca, false)
			cfg := tlsConfig(w, rec)
			cfg.CAFile = bundle(t, unrelated, ca)
			expectDelivered(t, cfg, w, rec)
		})
	}
}

func bundle(t *testing.T, authorities ...*authority) string {
	t.Helper()
	var data []byte
	for _, a := range authorities {
		data = append(data, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: a.cert.Raw})...)
	}
	path := filepath.Join(t.TempDir(), "bundle.pem")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing the bundle: %v", err)
	}
	return path
}

// TestAClientCertificateIsPresentedWhenTheConfigurationGivesOne pins the
// client certificate over both protocols.
func TestAClientCertificateIsPresentedWhenTheConfigurationGivesOne(t *testing.T) {
	for _, w := range wires {
		t.Run(w.protocol, func(t *testing.T) {
			ca := newAuthority(t)
			client := ca.issue(t, "client", x509.ExtKeyUsageClientAuth)

			rec, _ := tlsReceiver(t, w, ca, true)
			cfg := tlsConfig(w, rec)
			cfg.CAFile = ca.file
			cfg.ClientCertFile = client.certFile
			cfg.ClientKeyFile = client.keyFile
			expectDelivered(t, cfg, w, rec)

			rec, refused := tlsReceiver(t, w, ca, true)
			cfg = tlsConfig(w, rec)
			cfg.CAFile = ca.file
			expectRefused(t, cfg, rec, refused)
		})
	}
}

// TestAnUpstreamCertificateVariableDoesNotReplaceTheCAFile pins the other
// half of the variables' exclusion.
func TestAnUpstreamCertificateVariableDoesNotReplaceTheCAFile(t *testing.T) {
	unrelated, ca := newAuthority(t), newAuthority(t)
	for _, prefix := range []string{"OTEL_EXPORTER_OTLP_", "OTEL_EXPORTER_OTLP_TRACES_", "OTEL_EXPORTER_OTLP_METRICS_"} {
		t.Setenv(prefix+"CERTIFICATE", unrelated.file)
	}
	for _, w := range wires {
		t.Run(w.protocol, func(t *testing.T) {
			rec, _ := tlsReceiver(t, w, ca, false)
			cfg := tlsConfig(w, rec)
			cfg.CAFile = ca.file
			expectDelivered(t, cfg, w, rec)
		})
	}
}

// TestNoUpstreamCertificateVariableStandsInForTheConfiguration pins the
// certificate variables as the second source they must not be.
func TestNoUpstreamCertificateVariableStandsInForTheConfiguration(t *testing.T) {
	ca := newAuthority(t)
	client := ca.issue(t, "client", x509.ExtKeyUsageClientAuth)
	for _, prefix := range []string{"OTEL_EXPORTER_OTLP_", "OTEL_EXPORTER_OTLP_TRACES_", "OTEL_EXPORTER_OTLP_METRICS_"} {
		t.Setenv(prefix+"CERTIFICATE", ca.file)
		t.Setenv(prefix+"CLIENT_CERTIFICATE", client.certFile)
		t.Setenv(prefix+"CLIENT_KEY", client.keyFile)
	}

	for _, w := range wires {
		t.Run(w.protocol, func(t *testing.T) {
			t.Run("no certificate item", func(t *testing.T) {
				rec, refused := tlsReceiver(t, w, ca, false)
				expectRefused(t, tlsConfig(w, rec), rec, refused)
			})
			t.Run("ca-file without a client certificate", func(t *testing.T) {
				rec, refused := tlsReceiver(t, w, ca, true)
				cfg := tlsConfig(w, rec)
				cfg.CAFile = ca.file
				expectRefused(t, cfg, rec, refused)
			})
		})
	}
}
