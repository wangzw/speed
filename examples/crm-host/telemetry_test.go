package main

import (
	"compress/gzip"
	"encoding/hex"
	"fmt"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"sync"
	"testing"

	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/vislake/speed/pkg/observability/otlp"
)

// otlpReceiver is an in-process OTLP/HTTP collector. It decodes the protobuf
// the OTLP backend exports over its HTTP protocol, keeps every span and every
// metric with the resource of the process that exported it, and answers with
// an empty success. A body it cannot decode is answered 400 and kept as a
// failure, which every case that reads the receiver fails on.
type otlpReceiver struct {
	server *httptest.Server
	addr   string

	mu        sync.Mutex
	spans     []receivedSpan
	metrics   []receivedMetric
	undecoded []string
}

// receivedSpan is one exported span, with the resource of the process that
// exported it.
type receivedSpan struct {
	resource map[string]*commonpb.AnyValue
	span     *tracepb.Span
}

// attr is one of the span's attributes, or nil.
func (s receivedSpan) attr(key string) *commonpb.AnyValue {
	return attributesOf(s.span.GetAttributes())[key]
}

// traceID and spanID are the span's identifiers as the trace context header
// writes them.
func (s receivedSpan) traceID() string { return hex.EncodeToString(s.span.GetTraceId()) }
func (s receivedSpan) spanID() string  { return hex.EncodeToString(s.span.GetSpanId()) }

// receivedMetric is one exported metric, with the resource of the process that
// exported it.
type receivedMetric struct {
	resource map[string]*commonpb.AnyValue
	metric   *metricspb.Metric
}

// The paths an OTLP/HTTP exporter posts each signal to.
const (
	otlpTracesPath  = "/v1/traces"
	otlpMetricsPath = "/v1/metrics"
)

// startOTLPReceiver starts a receiver for one case.
func startOTLPReceiver(t *testing.T) *otlpReceiver {
	t.Helper()
	r := &otlpReceiver{}
	r.server = httptest.NewServer(nethttp.HandlerFunc(r.handle))
	t.Cleanup(r.server.Close)
	r.addr = r.server.Listener.Addr().String()
	return r
}

// handle takes one export.
func (r *otlpReceiver) handle(w nethttp.ResponseWriter, req *nethttp.Request) {
	spans, metrics, err := decodeExport(req)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.undecoded = append(r.undecoded, fmt.Sprintf("%s %s: %v", req.Method, req.URL.Path, err))
		w.WriteHeader(nethttp.StatusBadRequest)
		return
	}
	r.spans = append(r.spans, spans...)
	r.metrics = append(r.metrics, metrics...)
	// An empty body is an empty Export*ServiceResponse, which is a success
	// with nothing partial to report.
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(nethttp.StatusOK)
}

// decodeExport reads one export's body as the request the path names.
func decodeExport(req *nethttp.Request) ([]receivedSpan, []receivedMetric, error) {
	var body io.Reader = req.Body
	if req.Header.Get("Content-Encoding") == "gzip" {
		unzipped, err := gzip.NewReader(req.Body)
		if err != nil {
			return nil, nil, err
		}
		defer unzipped.Close()
		body = unzipped
	}
	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, err
	}
	if got := req.Header.Get("Content-Type"); got != "application/x-protobuf" {
		return nil, nil, fmt.Errorf("the export is sent as %q, and the OTLP backend's HTTP protocol is protobuf", got)
	}
	switch req.URL.Path {
	case otlpTracesPath:
		var export collectortrace.ExportTraceServiceRequest
		if err := proto.Unmarshal(raw, &export); err != nil {
			return nil, nil, err
		}
		var out []receivedSpan
		for _, rs := range export.GetResourceSpans() {
			resource := attributesOf(rs.GetResource().GetAttributes())
			for _, scope := range rs.GetScopeSpans() {
				for _, span := range scope.GetSpans() {
					out = append(out, receivedSpan{resource: resource, span: span})
				}
			}
		}
		return out, nil, nil
	case otlpMetricsPath:
		var export collectormetrics.ExportMetricsServiceRequest
		if err := proto.Unmarshal(raw, &export); err != nil {
			return nil, nil, err
		}
		var out []receivedMetric
		for _, rm := range export.GetResourceMetrics() {
			resource := attributesOf(rm.GetResource().GetAttributes())
			for _, scope := range rm.GetScopeMetrics() {
				for _, metric := range scope.GetMetrics() {
					out = append(out, receivedMetric{resource: resource, metric: metric})
				}
			}
		}
		return nil, out, nil
	}
	return nil, nil, fmt.Errorf("no signal is exported to %s", req.URL.Path)
}

// attributesOf indexes a list of attributes by key.
func attributesOf(list []*commonpb.KeyValue) map[string]*commonpb.AnyValue {
	out := make(map[string]*commonpb.AnyValue, len(list))
	for _, kv := range list {
		out[kv.GetKey()] = kv.GetValue()
	}
	return out
}

// spansNamed is every span of one name that arrived, in arrival order.
func (r *otlpReceiver) spansNamed(name string) []receivedSpan {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []receivedSpan
	for _, s := range r.spans {
		if s.span.GetName() == name {
			out = append(out, s)
		}
	}
	return out
}

// metricsNamed is every export of one metric that arrived, in arrival order.
func (r *otlpReceiver) metricsNamed(name string) []receivedMetric {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []receivedMetric
	for _, m := range r.metrics {
		if m.metric.GetName() == name {
			out = append(out, m)
		}
	}
	return out
}

// resources is the resource of every export that arrived.
func (r *otlpReceiver) resources() []map[string]*commonpb.AnyValue {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]*commonpb.AnyValue
	for _, s := range r.spans {
		out = append(out, s.resource)
	}
	for _, m := range r.metrics {
		out = append(out, m.resource)
	}
	return out
}

// requireDecoded fails the case when an export arrived that the receiver could
// not decode.
func (r *otlpReceiver) requireDecoded(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, failure := range r.undecoded {
		t.Errorf("the OTLP receiver could not decode an export: %s", failure)
	}
}

// uuidV4Pattern is the shape of a version 4 UUID, which the telemetry module
// gives each assembly as its service.instance.id.
var uuidV4Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// otlpStack is the stack whose telemetry leaves through the OTLP backend.
func otlpStack(t *testing.T) stack {
	t.Helper()
	for _, s := range stacks {
		if s.exporter == otlp.ModuleName {
			return s
		}
	}
	t.Fatalf("no stack exports through %s", otlp.ModuleName)
	return stack{}
}

// TestTheOTLPExportCarriesWhatEachReplicaMeasured is the case for the telemetry
// the OTLP backend exports, decoded, on the stack that exports through it.
//
// Two replicas serve, and the telemetry module derives a span from every
// deal.won. Each replica serves a member listing, one of them a move into won
// of a deal whose amount is past what a float64 holds, and the webhook of the
// won deal is posted; then both are stopped, which flushes what they held.
//
// What arrived is held to what the telemetry module states. Every export names
// the configured service, and each replica its own version 4 instance
// identifier, so the two replicas' exports are two sets. The served route is a
// server span named for the method and the route, carrying the method, the
// status, the route and the endpoint. The webhook's traceparent names an
// exported client span of the delivery. Each replica derives a consumer span
// from the deal.won the bus handed it, carrying the event's identifier as the
// move recorded it, the tenant, and the amount as the integer it was, which a
// payload decoded into floats would round to 2^53. The request duration is a
// histogram in seconds whose bucket bounds are the fourteen the module fixes,
// which the SDK's millisecond defaults are not.
func TestTheOTLPExportCarriesWhatEachReplicaMeasured(t *testing.T) {
	c := newCase(t, otlpStack(t))
	c.config.set("observability.otel.events", []string{dealWonEvent})
	receiver := startWebhookReceiver(t)
	w := startDealsOn(t, c, 2, func(w *dealsWorld) {
		c.configureWebhook(receiver, w.acme, "the secret acme signs with")
	})
	acme, alice := w.acme, w.asAlice
	definePipeline(t, w.h, acme, alice, pipelineOf("acme"))
	for _, h := range w.hosts {
		expect(t, "listing acme's members on "+h.name, h.get(t, apiEndpoint, "/members", acme.Alias, alice),
			nethttp.StatusOK)
	}
	won := winDeal(t, w.h, acme, alice, "traced", "JPY", pastFloat)
	calls := receiver.awaitCalls(t, webhookPath(acme), 1)
	// Every replica is handed the deal.won; once each has written what its
	// projection did with it, the span derived from it has been started, and
	// the stop flushes it.
	runs := make([]*process, 0, len(w.hosts))
	for _, h := range w.hosts {
		runs = append(runs, h.process)
	}
	awaitConsumers(t, runs, revenueModuleName, revenueOutcomes, map[eventKey]int{{acme.ID, *won.WonEventID}: 1})
	for _, h := range w.hosts {
		h.stop(t)
	}
	c.receiver.requireDecoded(t)

	t.Run("every export names the service and its replica", func(t *testing.T) {
		instances := map[string]bool{}
		for _, resource := range c.receiver.resources() {
			if got := resource["service.name"].GetStringValue(); got != hostName {
				t.Errorf("an export names the service %q, want %q", got, hostName)
			}
			id := resource["service.instance.id"].GetStringValue()
			if !uuidV4Pattern.MatchString(id) {
				t.Errorf("an export names the instance %q, which is not a version 4 UUID", id)
			}
			instances[id] = true
		}
		if len(instances) != len(w.hosts) {
			t.Errorf("the exports name %d instances, want one per replica, %d", len(instances), len(w.hosts))
		}
	})

	t.Run("a served route is a server span with its method, status, route and endpoint", func(t *testing.T) {
		name := nethttp.MethodGet + " /members"
		servers := map[string]bool{}
		for _, s := range c.receiver.spansNamed(name) {
			if s.span.GetKind() != tracepb.Span_SPAN_KIND_SERVER {
				t.Errorf("a %q span is of kind %v, want a server span", name, s.span.GetKind())
				continue
			}
			if s.attr("http.request.method").GetStringValue() == nethttp.MethodGet &&
				s.attr("http.response.status_code").GetIntValue() == nethttp.StatusOK &&
				s.attr("http.route").GetStringValue() == "/members" &&
				s.attr("speed.endpoint.name").GetStringValue() == apiEndpoint {
				servers[s.resource["service.instance.id"].GetStringValue()] = true
			}
		}
		if len(servers) != len(w.hosts) {
			t.Errorf("%d replicas exported a %q server span of a GET answered 200 on the route /members of %s, "+
				"want every one of the %d", len(servers), name, apiEndpoint, len(w.hosts))
		}
	})

	t.Run("the webhook carries the trace context of an exported client span", func(t *testing.T) {
		m := traceparentPattern.FindStringSubmatch(calls[0].header.Get("Traceparent"))
		if m == nil {
			t.Fatalf("the webhook carries the traceparent %q", calls[0].header.Get("Traceparent"))
		}
		for _, s := range c.receiver.spansNamed("webhooks POST") {
			if s.traceID() == m[1] && s.spanID() == m[2] {
				if s.span.GetKind() != tracepb.Span_SPAN_KIND_CLIENT {
					t.Errorf("the webhook's span is of kind %v, want a client span", s.span.GetKind())
				}
				return
			}
		}
		t.Errorf("the traceparent %q names no exported webhooks POST span", calls[0].header.Get("Traceparent"))
	})

	t.Run("each replica derives a span from the deal.won, with the event's identifier and the exact amount", func(t *testing.T) {
		derived := map[string]bool{}
		for _, s := range c.receiver.spansNamed(dealWonEvent) {
			id := s.attr("messaging.message.id").GetStringValue()
			if id != *won.WonEventID {
				continue
			}
			if s.span.GetKind() != tracepb.Span_SPAN_KIND_CONSUMER {
				t.Errorf("the deal.won span is of kind %v, want a consumer span", s.span.GetKind())
			}
			if got := s.attr("tenant_id").GetStringValue(); got != acme.ID {
				t.Errorf("the deal.won span carries the tenant %q, want %q", got, acme.ID)
			}
			amount := s.attr("amount")
			if _, isInt := amount.GetValue().(*commonpb.AnyValue_IntValue); !isInt || amount.GetIntValue() != pastFloat {
				t.Errorf("the deal.won span carries the amount %v, want the integer %d", amount, pastFloat)
			}
			derived[s.resource["service.instance.id"].GetStringValue()] = true
		}
		if len(derived) != len(w.hosts) {
			t.Errorf("%d replicas derived a span from the deal.won %s, want every one of the %d the bus handed it to",
				len(derived), *won.WonEventID, len(w.hosts))
		}
	})

	t.Run("the request duration is a histogram in seconds with the fixed bounds", func(t *testing.T) {
		exports := c.receiver.metricsNamed("http.server.request.duration")
		if len(exports) == 0 {
			t.Fatal("no export carried http.server.request.duration")
		}
		for _, m := range exports {
			if m.metric.GetUnit() != "s" {
				t.Errorf("the request duration is exported in %q, want s", m.metric.GetUnit())
			}
			histogram := m.metric.GetHistogram()
			if histogram == nil {
				t.Errorf("the request duration is exported as %T, want a histogram", m.metric.GetData())
				continue
			}
			for _, point := range histogram.GetDataPoints() {
				if !slices.Equal(point.GetExplicitBounds(), requestDurationBounds) {
					t.Errorf("a data point of the request duration has the bounds %v, want %v",
						point.GetExplicitBounds(), requestDurationBounds)
				}
			}
		}
	})
}

// spanNamedIn reports whether an OTLP receiver holds a span of one name with
// the identifiers a traceparent header names.
func spanNamedIn(r *otlpReceiver, name, traceID, spanID string) bool {
	for _, s := range r.spansNamed(name) {
		if s.traceID() == traceID && s.spanID() == spanID {
			return true
		}
	}
	return false
}
