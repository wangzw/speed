package otlp

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/observability"
)

// fixedReader is a reading face that hands this module one configuration.
type fixedReader struct{ cfg Config }

func (f fixedReader) Decode(namespace string, target any) error {
	if namespace == ModuleName {
		*target.(*Config) = f.cfg
	}
	return nil
}

// seenRequest is one export request as a receiver saw it.
type seenRequest struct {
	path   string
	header nethttp.Header
}

// receiver is an in-process collector: it records every request and answers
// each one with an empty success, in the framing of the protocol it serves.
type receiver struct {
	endpoint string

	mu   sync.Mutex
	seen []seenRequest
}

func (r *receiver) record(req *nethttp.Request) {
	_, _ = io.Copy(io.Discard, req.Body)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, seenRequest{path: req.URL.Path, header: req.Header.Clone()})
}

func (r *receiver) requests() []seenRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]seenRequest(nil), r.seen...)
}

// httpReceiver serves OTLP over HTTP: a success is a 200 with an empty
// protobuf body.
func httpReceiver(t *testing.T) *receiver {
	t.Helper()
	rec := &receiver{}
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, req *nethttp.Request) {
		rec.record(req)
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(nethttp.StatusOK)
	}))
	t.Cleanup(srv.Close)
	rec.endpoint = srv.Listener.Addr().String()
	return rec
}

// grpcReceiver serves OTLP over gRPC, which is HTTP/2 without TLS here: a
// success is an empty message frame followed by a zero grpc-status trailer.
// The standard library's server speaks unencrypted HTTP/2, so no gRPC server
// is needed to stand in for a collector.
func grpcReceiver(t *testing.T) *receiver {
	t.Helper()
	rec := &receiver{}
	srv := httptest.NewUnstartedServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, req *nethttp.Request) {
		rec.record(req)
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
		w.WriteHeader(nethttp.StatusOK)
		_, _ = w.Write([]byte{0, 0, 0, 0, 0})
		w.Header().Set("Grpc-Status", "0")
		w.Header().Set("Grpc-Message", "")
	}))
	srv.Config.Protocols = new(nethttp.Protocols)
	srv.Config.Protocols.SetUnencryptedHTTP2(true)
	srv.Start()
	t.Cleanup(srv.Close)
	rec.endpoint = srv.Listener.Addr().String()
	return rec
}

// exportersFor builds this module's product from a configuration, the way New
// does in an assembly.
func exportersFor(t *testing.T, cfg Config) (*observability.Exporters, error) {
	t.Helper()
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(fixedReader{cfg: cfg}, config.ModuleName)
	product, err := newExporters(context.Background(), reg)
	if err != nil {
		return nil, err
	}
	exporters, ok := product.(*observability.Exporters)
	if !ok {
		t.Fatalf("New delivered %T, want *observability.Exporters", product)
	}
	t.Cleanup(func() { _ = exporters.Shutdown(context.Background()) })
	return exporters, nil
}

// oneMetric is a batch holding a single counter point.
func oneMetric() *metricdata.ResourceMetrics {
	return &metricdata.ResourceMetrics{
		Resource: resource.Empty(),
		ScopeMetrics: []metricdata.ScopeMetrics{{
			Scope: instrumentation.Scope{Name: "orders"},
			Metrics: []metricdata.Metrics{{
				Name: "orders.placed",
				Data: metricdata.Sum[int64]{
					Temporality: metricdata.CumulativeTemporality,
					IsMonotonic: true,
					DataPoints:  []metricdata.DataPoint[int64]{{Value: 1}},
				},
			}},
		}},
	}
}

// exportOnce sends one span and one metric batch through the product, bounded
// so that a destination the exporter cannot reach fails the call rather than
// the test binary.
func exportOnce(exporters *observability.Exporters) (spanErr, metricErr error) {
	return exportWithin(exporters, 3*time.Second)
}

// exportWithin is exportOnce under a bound of the caller's choosing.
func exportWithin(exporters *observability.Exporters, bound time.Duration) (spanErr, metricErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	spanErr = exporters.ExportSpans(ctx, tracetest.SpanStubs{{Name: "place"}}.Snapshots())
	metricErr = exporters.Export(ctx, oneMetric())
	return spanErr, metricErr
}

// wire is what one protocol looks like to a receiver.
type wire struct {
	protocol    string
	receiver    func(*testing.T) *receiver
	contentType string
	spanPath    string
	metricPath  string
}

var wires = []wire{
	{
		protocol:    ProtocolHTTP,
		receiver:    httpReceiver,
		contentType: "application/x-protobuf",
		spanPath:    "/v1/traces",
		metricPath:  "/v1/metrics",
	},
	{
		protocol:    ProtocolGRPC,
		receiver:    grpcReceiver,
		contentType: "application/grpc",
		spanPath:    "/opentelemetry.proto.collector.trace.v1.TraceService/Export",
		metricPath:  "/opentelemetry.proto.collector.metrics.v1.MetricsService/Export",
	},
}

// paths lists the paths a receiver was asked for.
func paths(seen []seenRequest) []string {
	out := make([]string, 0, len(seen))
	for _, s := range seen {
		out = append(out, s.path)
	}
	return out
}

// TestTheExportersDeliverOverEachProtocol pins what a configured backend puts
// on the wire: both signals, at the collector's paths for the protocol, in its
// encoding, each request carrying the headers the configuration gave — the
// headers are where a collector's credential travels.
func TestTheExportersDeliverOverEachProtocol(t *testing.T) {
	for _, w := range wires {
		t.Run(w.protocol, func(t *testing.T) {
			rec := w.receiver(t)
			exporters, err := exportersFor(t, Config{
				Endpoint:     rec.endpoint,
				Protocol:     w.protocol,
				Insecure:     true,
				Headers:      []string{"x-api-key=secret"},
				ProbeTimeout: defaultProbeTimeout,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if spanErr, metricErr := exportOnce(exporters); spanErr != nil || metricErr != nil {
				t.Fatalf("the exports failed: spans %v, metrics %v", spanErr, metricErr)
			}

			seen := rec.requests()
			if got := paths(seen); len(got) != 2 || got[0] != w.spanPath || got[1] != w.metricPath {
				t.Fatalf("the receiver was asked for %v, want %s then %s", got, w.spanPath, w.metricPath)
			}
			for _, s := range seen {
				if got := s.header.Get("x-api-key"); got != "secret" {
					t.Errorf("the request to %s carried x-api-key %q, want the configured value", s.path, got)
				}
				if got := s.header.Get("Content-Type"); !strings.HasPrefix(got, w.contentType) {
					t.Errorf("the request to %s was %q, want %s", s.path, got, w.contentType)
				}
			}
		})
	}
}

// certificateFile writes a certificate a server really presents to a file, so
// that a variable naming a certificate names one that loads.
func certificateFile(t *testing.T) string {
	t.Helper()
	srv := httptest.NewTLSServer(nethttp.NotFoundHandler())
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "ca.pem")
	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("writing the certificate: %v", err)
	}
	return path
}

// TestNoUpstreamVariableOverridesTheConfiguration pins that the configuration
// is the only input for what it declares. The upstream exporters read the
// standard OTEL_EXPORTER_OTLP_* variables for every setting they are not given
// explicitly, which would be a second environment surface outside this
// module's prefix, its diagnostics and its sensitive marking: a header
// variable injects a credential the configuration never gave, a per-signal
// endpoint moves the path, a protocol variable switches the encoding, a
// certificate variable switches a plaintext exporter to TLS, and the metric
// variables change the temporality and the histogram the instruments are
// aggregated into.
func TestNoUpstreamVariableOverridesTheConfiguration(t *testing.T) {
	for name, value := range map[string]string{
		"OTEL_EXPORTER_OTLP_HEADERS":                               "x-injected=from-the-environment",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":                       "http://127.0.0.1:1/elsewhere",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT":                      "http://127.0.0.1:1/elsewhere",
		"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL":                       "http/json",
		"OTEL_EXPORTER_OTLP_CERTIFICATE":                           certificateFile(t),
		"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE":        "delta",
		"OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION": "base2_exponential_bucket_histogram",
	} {
		t.Setenv(name, value)
	}

	for _, w := range wires {
		t.Run(w.protocol, func(t *testing.T) {
			rec := w.receiver(t)
			exporters, err := exportersFor(t, Config{
				Endpoint:     rec.endpoint,
				Protocol:     w.protocol,
				Insecure:     true,
				ProbeTimeout: defaultProbeTimeout,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			counter, histogram := sdkmetric.InstrumentKindCounter, sdkmetric.InstrumentKindHistogram
			if got, want := exporters.Temporality(counter), sdkmetric.DefaultTemporalitySelector(counter); got != want {
				t.Errorf("a counter is exported with %v temporality, want the default %v", got, want)
			}
			if got, want := exporters.Aggregation(histogram), sdkmetric.DefaultAggregationSelector(histogram); fmt.Sprintf("%T", got) != fmt.Sprintf("%T", want) {
				t.Errorf("a histogram is aggregated as %T, want the default %T, which carries the boundaries the "+
					"instruments declare", got, want)
			}

			if spanErr, metricErr := exportOnce(exporters); spanErr != nil || metricErr != nil {
				t.Fatalf("the exports failed: spans %v, metrics %v", spanErr, metricErr)
			}
			seen := rec.requests()
			if got := paths(seen); len(got) != 2 || got[0] != w.spanPath || got[1] != w.metricPath {
				t.Fatalf("the receiver was asked for %v, want %s then %s", got, w.spanPath, w.metricPath)
			}
			for _, s := range seen {
				if got := s.header.Get("x-injected"); got != "" {
					t.Errorf("the request to %s carried a header only the environment gave: %q", s.path, got)
				}
				if got := s.header.Get("Content-Type"); !strings.HasPrefix(got, w.contentType) {
					t.Errorf("the request to %s was %q, want %s", s.path, got, w.contentType)
				}
			}
		})
	}
}

// firstBytes is a listener that records the first byte of every connection made
// to it and closes the connection: a TLS client opens with a handshake record,
// 0x16, and a plaintext HTTP/1.1 or HTTP/2 client with the letter of its method
// or preface.
type firstBytes struct {
	endpoint string

	mu    sync.Mutex
	bytes []byte
}

func listenForFirstBytes(t *testing.T) *firstBytes {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	fb := &firstBytes{endpoint: l.Addr().String()}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			first := make([]byte, 1)
			_ = conn.SetReadDeadline(time.Now().Add(time.Second))
			if n, _ := conn.Read(first); n == 1 {
				fb.mu.Lock()
				fb.bytes = append(fb.bytes, first[0])
				fb.mu.Unlock()
			}
			_ = conn.Close()
		}
	}()
	return fb
}

func (fb *firstBytes) seen() []byte {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	return append([]byte(nil), fb.bytes...)
}

// tlsHandshakeRecord is the first byte of a TLS connection.
const tlsHandshakeRecord = 0x16

// TestNoUpstreamVariableTurnsTLSOff pins the variable that matters most: a
// configuration that asks for TLS gets TLS, whatever OTEL_EXPORTER_OTLP_INSECURE
// says. A downgraded exporter would send the configured credential in the
// clear, so every connection it opens has to begin with a TLS handshake.
func TestNoUpstreamVariableTurnsTLSOff(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")
	for _, w := range wires {
		t.Run(w.protocol, func(t *testing.T) {
			fb := listenForFirstBytes(t)
			exporters, err := exportersFor(t, Config{
				Endpoint:     fb.endpoint,
				Protocol:     w.protocol,
				Insecure:     false,
				Headers:      []string{"x-api-key=secret"},
				ProbeTimeout: defaultProbeTimeout,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, _ = exportWithin(exporters, time.Second)
			seen := fb.seen()
			if len(seen) == 0 {
				t.Fatal("the exporters opened no connection, so nothing here says what they would have sent")
			}
			for _, b := range seen {
				if b != tlsHandshakeRecord {
					t.Errorf("a connection from exporters configured for TLS opened with %q, want a TLS "+
						"handshake", b)
				}
			}
		})
	}
}

// closedEndpoint is an address nothing listens on: a port that was bound and
// released again.
func closedEndpoint(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// TestTheProbeFailsTheStartupWhenNothingListens pins the switch that turns an
// unreachable collector from a runtime condition into a deployment premise.
func TestTheProbeFailsTheStartupWhenNothingListens(t *testing.T) {
	endpoint := closedEndpoint(t)
	_, err := exportersFor(t, Config{
		Endpoint:       endpoint,
		Protocol:       ProtocolGRPC,
		Insecure:       true,
		ProbeAtStartup: true,
		ProbeTimeout:   time.Second,
	})
	if !errors.Is(err, ErrExporterUnreachable) {
		t.Fatalf("a probe against a closed port gave %v, want %v", err, ErrExporterUnreachable)
	}
	if !strings.Contains(err.Error(), endpoint) {
		t.Errorf("the failure %q does not name the endpoint it could not reach", err)
	}
}

// TestTheProbePassesAndTheSwitchStaysOffByDefault pins both other outcomes: a
// probe against a listening endpoint lets the startup go on, and with the
// probe off an endpoint nobody listens on is not a startup failure — the
// exporters are lazy, and a collector that starts later is an ordinary
// deployment.
func TestTheProbePassesAndTheSwitchStaysOffByDefault(t *testing.T) {
	rec := httpReceiver(t)
	if _, err := exportersFor(t, Config{
		Endpoint:       rec.endpoint,
		Protocol:       ProtocolHTTP,
		Insecure:       true,
		ProbeAtStartup: true,
		ProbeTimeout:   time.Second,
	}); err != nil {
		t.Errorf("a probe against a listening endpoint failed the startup: %v", err)
	}
	if _, err := exportersFor(t, Config{
		Endpoint:     closedEndpoint(t),
		Protocol:     ProtocolGRPC,
		Insecure:     true,
		ProbeTimeout: time.Second,
	}); err != nil {
		t.Errorf("with the probe off, an endpoint nobody listens on failed the startup: %v", err)
	}
}
