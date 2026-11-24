package otlp

import (
	"bytes"
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	"github.com/vislake/speed/pkg/core/coretest"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
)

type fixedReader struct{ cfg Config }

func (f fixedReader) Decode(namespace string, target any) error {
	if namespace == ModuleName {
		*target.(*Config) = f.cfg
	}
	return nil
}

type seenRequest struct {
	path   string
	header nethttp.Header
}

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

func httpHandler(rec *receiver) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, req *nethttp.Request) {
		rec.record(req)
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(nethttp.StatusOK)
	})
}

func grpcHandler(rec *receiver) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, req *nethttp.Request) {
		rec.record(req)
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
		w.WriteHeader(nethttp.StatusOK)
		_, _ = w.Write([]byte{0, 0, 0, 0, 0})
		w.Header().Set("Grpc-Status", "0")
		w.Header().Set("Grpc-Message", "")
	})
}

func httpReceiver(t *testing.T) *receiver {
	t.Helper()
	rec := &receiver{}
	srv := httptest.NewServer(httpHandler(rec))
	t.Cleanup(srv.Close)
	rec.endpoint = srv.Listener.Addr().String()
	return rec
}

func grpcReceiver(t *testing.T) *receiver {
	t.Helper()
	rec := &receiver{}
	srv := httptest.NewUnstartedServer(grpcHandler(rec))
	srv.Config.Protocols = new(nethttp.Protocols)
	srv.Config.Protocols.SetUnencryptedHTTP2(true)
	srv.Start()
	t.Cleanup(srv.Close)
	rec.endpoint = srv.Listener.Addr().String()
	return rec
}

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
	return exportWithin(exporters, coretest.Scale(3*time.Second))
}

func exportWithin(exporters *observability.Exporters, bound time.Duration) (spanErr, metricErr error) {
	spanCtx, cancelSpan := context.WithTimeout(context.Background(), bound)
	defer cancelSpan()
	spanErr = exporters.ExportSpans(spanCtx, tracetest.SpanStubs{{Name: "place"}}.Snapshots())
	metricCtx, cancelMetric := context.WithTimeout(context.Background(), bound)
	defer cancelMetric()
	metricErr = exporters.Export(metricCtx, oneMetric())
	return spanErr, metricErr
}

type wire struct {
	protocol    string
	receiver    func(*testing.T) *receiver
	handler     func(*receiver) nethttp.Handler
	contentType string
	spanPath    string
	metricPath  string
}

var wires = []wire{
	{
		protocol:    ProtocolHTTP,
		receiver:    httpReceiver,
		handler:     httpHandler,
		contentType: "application/x-protobuf",
		spanPath:    "/v1/traces",
		metricPath:  "/v1/metrics",
	},
	{
		protocol:    ProtocolGRPC,
		receiver:    grpcReceiver,
		handler:     grpcHandler,
		contentType: "application/grpc",
		spanPath:    "/opentelemetry.proto.collector.trace.v1.TraceService/Export",
		metricPath:  "/opentelemetry.proto.collector.metrics.v1.MetricsService/Export",
	},
}

func paths(seen []seenRequest) []string {
	out := make([]string, 0, len(seen))
	for _, s := range seen {
		out = append(out, s.path)
	}
	return out
}

// TestTheExportersDeliverOverEachProtocol pins what a configured backend
// puts on the wire.
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

// TestTheSpecificationsNameForHTTPExportsOverHTTP pins that http/protobuf
// is read as http all the way to the wire.
func TestTheSpecificationsNameForHTTPExportsOverHTTP(t *testing.T) {
	rec := httpReceiver(t)
	exporters, err := exportersFor(t, Config{
		Endpoint:     rec.endpoint,
		Protocol:     "http/protobuf",
		Insecure:     true,
		ProbeTimeout: defaultProbeTimeout,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if spanErr, metricErr := exportOnce(exporters); spanErr != nil || metricErr != nil {
		t.Fatalf("the exports failed: spans %v, metrics %v", spanErr, metricErr)
	}
	seen := rec.requests()
	if got := paths(seen); len(got) != 2 || got[0] != httpTracesPath || got[1] != httpMetricsPath {
		t.Fatalf("the receiver was asked for %v, want %s then %s", got, httpTracesPath, httpMetricsPath)
	}
	for _, s := range seen {
		if got := s.header.Get("Content-Type"); !strings.HasPrefix(got, "application/x-protobuf") {
			t.Errorf("the request to %s was %q, want application/x-protobuf", s.path, got)
		}
	}
}

type stubLogger struct {
	lg    *slog.Logger
	names *[]string
}

func (s stubLogger) Named(name string) *slog.Logger {
	*s.names = append(*s.names, name)
	return s.lg
}

// Redaction hands back a face that takes a rule and keeps nothing: New
// registers the header values through it, so a face that can take a rule
// is all this stub has to be.
func (s stubLogger) Redaction() log.Redaction { return noopRedaction{} }

type noopRedaction struct{}

func (noopRedaction) AddKeys(...string)              {}
func (noopRedaction) AddPattern(string, log.Matcher) {}

// The header a case configures to tell whether a line repeats it: a key and a
// value nothing else in a line would contain.
const (
	markedHeaderKey   = "x-marker-key-5d1c"
	markedHeaderValue = "marker-value-9e2b"
)

func exportersLogging(t *testing.T, cfg Config) (logged string, names []string) {
	t.Helper()
	buf := &bytes.Buffer{}
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Supply(fixedReader{cfg: cfg}, config.ModuleName)
	reg.Supply(stubLogger{lg: slog.New(slog.NewTextHandler(buf, nil)), names: &names}, log.ModuleName)
	product, err := newExporters(context.Background(), reg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	exporters, ok := product.(*observability.Exporters)
	if !ok {
		t.Fatalf("New delivered %T, want *observability.Exporters", product)
	}
	t.Cleanup(func() { _ = exporters.Shutdown(context.Background()) })
	return buf.String(), names
}

// TestHeadersOverPlaintextToANonLoopbackHostAreWarnedAbout pins the warning that
// a credential crosses the network in the clear, carrying none of it.
func TestHeadersOverPlaintextToANonLoopbackHostAreWarnedAbout(t *testing.T) {
	withoutOverriddenVariables(t)
	for _, endpoint := range []struct{ value, host string }{
		{"collector-7a41.example:4317", "collector-7a41"},
		{"10.0.0.7:4317", "10.0.0.7"},
		{"0.0.0.0:4317", "0.0.0.0"},
		{"[2001:db8::1]:4317", "2001:db8::1"},
		{"localhost.collector-7a41.example:4317", "collector-7a41"},
		{"127.collector-7a41.example:4317", "collector-7a41"},
		{"mylocalhost:4317", "mylocalhost"},
		{":4317", ":4317"},
	} {
		for _, protocol := range []string{ProtocolGRPC, ProtocolHTTP} {
			t.Run(endpoint.value+" over "+protocol, func(t *testing.T) {
				logged, names := exportersLogging(t, Config{
					Endpoint:     endpoint.value,
					Protocol:     protocol,
					Insecure:     true,
					Headers:      []string{markedHeaderKey + "=" + markedHeaderValue},
					ProbeTimeout: defaultProbeTimeout,
				})
				lines := strings.Split(strings.TrimSpace(logged), "\n")
				if len(lines) != 1 || !strings.Contains(lines[0], "level=WARN") {
					t.Fatalf("the module logged %q, want one warning line", logged)
				}
				if len(names) != 1 || names[0] != ModuleName {
					t.Errorf("the logger was asked for under %q, want %q alone", names, ModuleName)
				}
				if !strings.Contains(lines[0], "plaintext") || !strings.Contains(lines[0], ModuleName+".insecure") {
					t.Errorf("the line is %q, want it to say the headers go over plaintext and that %s.insecure "+
						"set to false is the fix", lines[0], ModuleName)
				}
				for _, leaked := range []string{markedHeaderKey, markedHeaderValue, endpoint.host} {
					if strings.Contains(logged, leaked) {
						t.Errorf("the line is %q, and it repeats %q", lines[0], leaked)
					}
				}
			})
		}
	}
}

// TestNoPlaintextWarningWhereNothingCrossesANetworkInTheClear pins the cases the
// warning is not for: a loopback collector, TLS, and no headers at all.
func TestNoPlaintextWarningWhereNothingCrossesANetworkInTheClear(t *testing.T) {
	withoutOverriddenVariables(t)
	headers := []string{markedHeaderKey + "=" + markedHeaderValue}
	for name, cfg := range map[string]Config{
		"localhost":             {Endpoint: "localhost:4317", Insecure: true, Headers: headers},
		"localhost in capitals": {Endpoint: "LOCALHOST:4317", Insecure: true, Headers: headers},
		"an IPv4 loopback":      {Endpoint: "127.0.0.1:4317", Insecure: true, Headers: headers},
		"another IPv4 loopback": {Endpoint: "127.8.9.10", Insecure: true, Headers: headers},
		"the IPv6 loopback":     {Endpoint: "[::1]:4317", Insecure: true, Headers: headers},
		"the bare IPv6 host":    {Endpoint: "[::1]", Insecure: true, Headers: headers},
		"TLS":                   {Endpoint: "collector.example:4317", Insecure: false, Headers: headers},
		"no headers":            {Endpoint: "collector.example:4317", Insecure: true},
	} {
		t.Run(name, func(t *testing.T) {
			cfg.Protocol = ProtocolGRPC
			cfg.ProbeTimeout = defaultProbeTimeout
			if logged, _ := exportersLogging(t, cfg); logged != "" {
				t.Errorf("the module logged %q, want nothing", logged)
			}
		})
	}
}

// TestTheLoggerIsAnOptionalDependency pins how this module takes a
// logger.
func TestTheLoggerIsAnOptionalDependency(t *testing.T) {
	withoutOverriddenVariables(t)
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", markedHeaderKey+"="+markedHeaderValue)
	declared := false
	for _, req := range Module().Requires {
		if _, ok := req.Token.(*log.Logger); ok {
			declared = true
			if !req.Optional {
				t.Error("the logger is a required dependency, so a command without the logging module " +
					"would fail over a warning this module can do without")
			}
		}
	}
	if !declared {
		t.Error("the logger is not declared, so nothing orders a constructed logging module ahead of " +
			"this one and New may find no logger to warn through")
	}

	cfg := Config{
		Endpoint:     "collector.example:4317",
		Protocol:     ProtocolGRPC,
		Insecure:     true,
		Headers:      []string{markedHeaderKey + "=" + markedHeaderValue},
		ProbeTimeout: defaultProbeTimeout,
	}
	release := func(_ context.Context, reg *core.Registry) error {
		exporters, err := core.Resolve[observability.SpanExporter](reg)
		if err != nil {
			return err
		}
		return exporters.Shutdown(context.Background())
	}

	root := &bytes.Buffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(root, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	without := core.New()
	without.Register(config.Module())
	without.Register(Module())
	without.Supply(fixedReader{cfg: cfg}, config.ModuleName)
	if err := without.Drive(context.Background(), core.Drive{
		Depth:        core.StageNew,
		Participants: []string{ModuleName},
		Action:       release,
	}); err != nil {
		t.Errorf("a command without the logging module failed: %v", err)
	}
	if root.Len() != 0 {
		t.Errorf("without a logger the module wrote %q through the process root logger, want nothing", root)
	}

	logged := &bytes.Buffer{}
	var names []string
	with := core.New()
	with.Register(config.Module())
	with.Register(log.Module())
	with.Register(Module())
	with.Supply(fixedReader{cfg: cfg}, config.ModuleName)
	with.Supply(stubLogger{lg: slog.New(slog.NewTextHandler(logged, nil)), names: &names}, log.ModuleName)
	if err := with.Drive(context.Background(), core.Drive{
		Depth:        core.StageNew,
		Participants: []string{ModuleName},
		Action:       release,
	}); err != nil {
		t.Fatalf("a command with the logging module failed: %v", err)
	}
	for _, phrase := range []string{"plaintext", ignoredLineText} {
		if !strings.Contains(logged.String(), phrase) {
			t.Errorf("the module logged %q, want a warning saying %q through the logger in the participating set",
				logged, phrase)
		}
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

// TestNoUpstreamVariableOverridesTheConfiguration pins that the
// configuration is the only input for what it declares.
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

// firstBytes records the first byte of every connection and closes it: 0x16 is a
// TLS handshake, and a letter is plaintext.
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
			_ = conn.SetReadDeadline(time.Now().Add(coretest.Scale(time.Second)))
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

// TestNoUpstreamVariableTurnsTLSOff pins the variable that matters most.
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

// TestTheProbeDialsTheFallbackPortOfAnEndpointWithoutOne pins the probe
// of an endpoint the shape check lets through without a port.
func TestTheProbeDialsTheFallbackPortOfAnEndpointWithoutOne(t *testing.T) {
	_, err := exportersFor(t, Config{
		Endpoint:       "127.0.0.1",
		Protocol:       ProtocolHTTP,
		Insecure:       true,
		ProbeAtStartup: true,
		ProbeTimeout:   time.Second,
	})
	if err == nil {
		return
	}
	if !errors.Is(err, ErrExporterUnreachable) {
		t.Fatalf("a probe of 127.0.0.1 gave %v, want %v", err, ErrExporterUnreachable)
	}
	var dial *net.OpError
	if !errors.As(err, &dial) || dial.Addr == nil || dial.Addr.String() != "127.0.0.1:80" {
		t.Fatalf("a probe of 127.0.0.1 gave %v, want the failure of a dial of 127.0.0.1:80", err)
	}
	if want := ErrExporterUnreachable.Error() + ": 127.0.0.1:80: "; !strings.HasPrefix(err.Error(), want) {
		t.Errorf("the text is %q, want it to start %q", err, want)
	}
}

// TestTheProbeAddressFallsBackToTheUpstreamsPort pins the port the probe adds to
// an endpoint without one, the port the exporters use.
func TestTheProbeAddressFallsBackToTheUpstreamsPort(t *testing.T) {
	for _, c := range []struct {
		endpoint, protocol string
		insecure           bool
		want               string
	}{
		{"collector.example", ProtocolGRPC, true, "collector.example:443"},
		{"collector.example", ProtocolGRPC, false, "collector.example:443"},
		{"collector.example", ProtocolHTTP, true, "collector.example:80"},
		{"collector.example", ProtocolHTTP, false, "collector.example:443"},
		{"[2001:db8::1]", ProtocolGRPC, false, "[2001:db8::1]:443"},
		{"collector.example:4318", ProtocolHTTP, true, "collector.example:4318"},
		{"[::1]:4317", ProtocolGRPC, true, "[::1]:4317"},
	} {
		cfg := &Config{Endpoint: c.endpoint, Protocol: c.protocol, Insecure: c.insecure}
		if got := dialAddress(cfg); got != c.want {
			t.Errorf("the probe of %q over %s (insecure %v) dials %q, want %q",
				c.endpoint, c.protocol, c.insecure, got, c.want)
		}
	}
}

// TestTheProbePassesAndTheSwitchStaysOffByDefault pins both other
// outcomes.
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
