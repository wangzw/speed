package otel

import (
	"context"
	"errors"
	"log/slog"
	nethttp "net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
)

// requestCountExporter keeps how many requests the request histogram counted.
type requestCountExporter struct {
	mu    sync.Mutex
	count uint64
}

func (*requestCountExporter) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(kind)
}

func (*requestCountExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(kind)
}

func (m *requestCountExporter) Export(_ context.Context, rm *metricdata.ResourceMetrics) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sm := range rm.ScopeMetrics {
		for _, metric := range sm.Metrics {
			if metric.Name != requestDurationName {
				continue
			}
			if h, ok := metric.Data.(metricdata.Histogram[float64]); ok {
				for _, point := range h.DataPoints {
					m.count += point.Count
				}
			}
		}
	}
	return nil
}

func (*requestCountExporter) ForceFlush(context.Context) error { return nil }
func (*requestCountExporter) Shutdown(context.Context) error   { return nil }

func (m *requestCountExporter) counted() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.count
}

// endpointCase is one assembly of this module beside whatever the case gives it.
type endpointCase struct {
	// listed is the endpoint list the configuration gives, and setList says
	// whether it gives one at all: a case that leaves it out is the default.
	listed  []string
	setList bool
	// declared names the endpoints the entry module declares. Nothing declared
	// means no entry module in the assembly, which is a background process.
	declared []string
	// extra registers more modules and returns their names.
	extra func(reg *core.Registry) []string
	// serving is the endpoints the case sends one request to while it runs.
	serving []string
}

// endpointConfig is a reading face for an endpointCase: this module's list as
// the case sets it, and the entry module's endpoints as the case declares them.
type endpointConfig struct {
	c endpointCase
}

func (f endpointConfig) Decode(namespace string, target any) error {
	if namespace == ModuleName {
		cfg := target.(*Config)
		cfg.ServiceName = "orders"
		if f.c.setList {
			cfg.Endpoints = f.c.listed
		}
		return nil
	}
	doc := `{"listeners":{`
	for i, name := range f.c.declared {
		if i > 0 {
			doc += ","
		}
		doc += `"` + name + `":{"address":"127.0.0.1:0"}`
	}
	doc += `},"endpoints":{`
	for i, name := range f.c.declared {
		if i > 0 {
			doc += ","
		}
		doc += `"` + name + `":{"listener":"` + name + `"}`
	}
	return jsonConfig(doc+`}}`).Decode(namespace, target)
}

// served is what an endpointCase left at the export backend, and the failure the
// drive ended with. Each span is written as the endpoint it says it was served
// on and its name, "api GET /ping".
type served struct {
	err   error
	spans []string
	count uint64
}

// run assembles the case and drives it to Serve. Every endpoint the case
// declares carries a route another module registered, which is what a request
// to it is answered by.
func (c endpointCase) run(t *testing.T) served {
	t.Helper()
	spans := &recordingSpanExporter{}
	metrics := &requestCountExporter{}
	logs := &syncBuffer{}

	reg := core.New()
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Supply(endpointConfig{c: c}, config.ModuleName)
	reg.Supply(entryLogger{lg: slog.New(slog.NewJSONHandler(logs, nil))}, log.ModuleName)
	reg.Register(Module())
	reg.Register(core.Module{
		Name: "test.backend",
		Provides: []core.Provision{
			{Token: (*observability.SpanExporter)(nil)},
			{Token: (*observability.MetricExporter)(nil)},
		},
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(context.Context, *core.Registry) (any, error) {
			return &observability.Exporters{Span: spans, Metric: metrics}, nil
		},
	})
	participants := []string{ModuleName, "test.backend"}
	if len(c.declared) > 0 {
		reg.Register(speedhttp.Module())
		reg.Register(core.Module{
			Name:     "test.registrant",
			Requires: []core.Requirement{{Token: (*speedhttp.Router)(nil)}},
			New:      func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
			Init: func(_ context.Context, reg *core.Registry, _ any) error {
				router, err := core.Resolve[speedhttp.Router](reg)
				if err != nil {
					return err
				}
				for _, name := range c.declared {
					ep, err := router.Endpoint(name)
					if err != nil {
						return err
					}
					ep.Route(nethttp.MethodGet, "/ping", func(c *gin.Context) { c.Status(nethttp.StatusNoContent) })
				}
				return nil
			},
		})
		participants = append(participants, speedhttp.ModuleName, "test.registrant")
	}
	if c.extra != nil {
		participants = append(participants, c.extra(reg)...)
	}

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageServe,
		Participants: participants,
		Action: func(context.Context, *core.Registry) error {
			for _, name := range c.serving {
				h := &entryHost{
					base: "http://" + listeningAddress(t, logs, name),
					client: &nethttp.Client{
						Timeout:   5 * time.Second,
						Transport: &nethttp.Transport{DisableKeepAlives: true},
					},
				}
				if got := h.status(t, nethttp.MethodGet, "/ping"); got != nethttp.StatusNoContent {
					t.Errorf("%s answered %d, want the 204 the route writes", name, got)
				}
			}
			return nil
		},
	})
	out := served{err: err, count: metrics.counted()}
	for _, span := range spans.recorded() {
		attrs := attribute.NewSet(span.Attributes()...)
		endpoint, _ := attrs.Value(attrEndpointName)
		out.spans = append(out.spans, endpoint.AsString()+" "+span.Name())
	}
	slices.Sort(out.spans)
	return out
}

// TestTheDefaultRegistersTheLayerOnEveryDeclaredEndpoint pins what an endpoint
// list nobody wrote means. The entry module declares two endpoints and the
// configuration says nothing about this module's list, so a request to either
// one has to leave a server span and a count in the request histogram. A default
// that registers on none, or on one, leaves a request unseen and nothing saying
// so; the counts are read at shutdown, so a layer on one endpoint counts one
// request of the two.
func TestTheDefaultRegistersTheLayerOnEveryDeclaredEndpoint(t *testing.T) {
	got := endpointCase{
		declared: []string{"api", "admin"},
		serving:  []string{"api", "admin"},
	}.run(t)
	if got.err != nil {
		t.Fatalf("drive: %v", got.err)
	}
	if want := []string{"admin GET /ping", "api GET /ping"}; !slices.Equal(got.spans, want) {
		t.Errorf("the backend received the spans %v, want %v: an endpoint the entry module declares went uninstrumented", got.spans, want)
	}
	if got.count != 2 {
		t.Errorf("the request histogram counted %d requests, want 2: one per endpoint", got.count)
	}
}

// TestAnEmptyListRegistersOnNoEndpointWhateverTheEntryModuleDeclares pins that
// an empty list is an answer and not the absence of one: the entry module is
// there and declares an endpoint, and no request to it is observed.
func TestAnEmptyListRegistersOnNoEndpointWhateverTheEntryModuleDeclares(t *testing.T) {
	got := endpointCase{
		listed: []string{}, setList: true,
		declared: []string{"api"},
		serving:  []string{"api"},
	}.run(t)
	if got.err != nil {
		t.Fatalf("drive: %v", got.err)
	}
	if len(got.spans) != 0 || got.count != 0 {
		t.Errorf("an empty list left %v spans and %d counted requests, want none", got.spans, got.count)
	}
}

// TestAnEmptyListDoesNotTakeUpTheEntryModule pins that an empty list reaches for
// nothing. The two modules delivering the router are ones a lookup cannot choose
// between, so a lookup made anyway would fail the startup; the empty list is what
// lets a process that carries this module and happens to hold such an assembly
// start.
func TestAnEmptyListDoesNotTakeUpTheEntryModule(t *testing.T) {
	got := endpointCase{
		listed: []string{}, setList: true,
		extra: func(reg *core.Registry) []string {
			reg.Register(entryStandIn("test.first-entry", false))
			reg.Register(entryStandIn("test.second-entry", false))
			return []string{"test.first-entry", "test.second-entry"}
		},
	}.run(t)
	if got.err != nil {
		t.Fatalf("an empty endpoint list took the entry module up: %v", got.err)
	}
}

// TestTheDefaultWithAnEntryModuleDeclaringNoEndpointStarts pins that an entry
// module which is there and declares nothing is not a missing one: the default
// has no name to register on, and the startup succeeds. Reading the empty
// declaration as a missing provider would fail a process that is only waiting
// for its endpoints to be declared.
func TestTheDefaultWithAnEntryModuleDeclaringNoEndpointStarts(t *testing.T) {
	got := endpointCase{
		extra: func(reg *core.Registry) []string {
			reg.Register(entryStandIn("test.only-entry", false))
			return []string{"test.only-entry"}
		},
	}.run(t)
	if got.err != nil {
		t.Fatalf("an entry module declaring no endpoint failed the default: %v", got.err)
	}
	if len(got.spans) != 0 || got.count != 0 {
		t.Errorf("the default left %v spans and %d counted requests with no endpoint declared, want none", got.spans, got.count)
	}
}

// TestAListOfNamesRegistersOnThoseAndNoOthers pins that naming an endpoint
// narrows the default instead of adding to it.
func TestAListOfNamesRegistersOnThoseAndNoOthers(t *testing.T) {
	got := endpointCase{
		listed: []string{"admin"}, setList: true,
		declared: []string{"api", "admin"},
		serving:  []string{"api", "admin"},
	}.run(t)
	if got.err != nil {
		t.Fatalf("drive: %v", got.err)
	}
	if want := []string{"admin GET /ping"}; !slices.Equal(got.spans, want) {
		t.Errorf("the backend received the spans %v, want only %v", got.spans, want)
	}
	if got.count != 1 {
		t.Errorf("the request histogram counted %d requests, want 1: the unlisted endpoint was counted too", got.count)
	}
}

// TestAnUndeclaredEndpointNameFailsTheStartupAndNamesTheKey pins the failure of
// a listed name the entry module never declared: it is the entry module's
// sentinel still, and the text says which setting to correct.
func TestAnUndeclaredEndpointNameFailsTheStartupAndNamesTheKey(t *testing.T) {
	got := endpointCase{
		listed: []string{"api", "metrics"}, setList: true,
		declared: []string{"api"},
	}.run(t)
	if !errors.Is(got.err, speedhttp.ErrUnknownEndpoint) {
		t.Fatalf("the drive ended with %v, want the entry module's unknown-endpoint failure", got.err)
	}
	if !strings.Contains(got.err.Error(), "observability.otel.endpoints") {
		t.Errorf("the failure %q does not name the key observability.otel.endpoints", got.err)
	}
	if !strings.Contains(got.err.Error(), `"metrics"`) {
		t.Errorf("the failure %q does not name the endpoint that was not declared", got.err)
	}
}

// TestAListOfNamesWithNoEntryModuleFailsTheStartupAndNamesTheKey pins that only
// the default is lenient: a list that names an endpoint needs the entry module,
// and the failure says so and says how to get out of it.
func TestAListOfNamesWithNoEntryModuleFailsTheStartupAndNamesTheKey(t *testing.T) {
	got := endpointCase{listed: []string{"api"}, setList: true}.run(t)
	if !errors.Is(got.err, core.ErrMissingProvider) {
		t.Fatalf("the drive ended with %v, want the missing-provider failure", got.err)
	}
	for _, want := range []string{"observability.otel.endpoints", "clear the list", "command tree"} {
		if !strings.Contains(got.err.Error(), want) {
			t.Errorf("the failure %q lacks %q", got.err, want)
		}
	}
}

// entryStandIn is a module that delivers the router capability and declares no
// endpoint. Without the exclusive claim the entry module makes, two of them
// reach this module's wiring as two providers a lookup cannot choose between,
// which the exclusive claim would otherwise have turned into a resolution
// failure before any lookup.
func entryStandIn(name string, exclusive bool) core.Module {
	return core.Module{
		Name:     name,
		Provides: []core.Provision{{Token: (*speedhttp.Router)(nil), Exclusive: exclusive}},
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(context.Context, *core.Registry) (any, error) { return standInRouter{}, nil },
	}
}

type standInRouter struct{}

func (standInRouter) Endpoint(string) (speedhttp.Endpoint, error) {
	return nil, speedhttp.ErrUnknownEndpoint
}
func (standInRouter) Endpoints() []string { return nil }

// TestAnAmbiguousEntryModuleIsNotReadAsAMissingOne is the other half of the
// default's leniency. Two modules deliver the router and a lookup cannot choose;
// that is a failure whatever the list says, and reading it as "no entry module"
// would start a process that serves requests with a layer on none of them.
func TestAnAmbiguousEntryModuleIsNotReadAsAMissingOne(t *testing.T) {
	got := endpointCase{
		extra: func(reg *core.Registry) []string {
			reg.Register(entryStandIn("test.first-entry", false))
			reg.Register(entryStandIn("test.second-entry", false))
			return []string{"test.first-entry", "test.second-entry"}
		},
	}.run(t)
	if !errors.Is(got.err, core.ErrAmbiguousProvider) {
		t.Fatalf("the drive ended with %v, want the ambiguous-provider failure", got.err)
	}
	if errors.Is(got.err, core.ErrMissingProvider) {
		t.Errorf("the failure %q also reads as a missing provider", got.err)
	}
	if !strings.Contains(got.err.Error(), "observability.otel.endpoints") {
		t.Errorf("the failure %q does not name the key observability.otel.endpoints", got.err)
	}
	if strings.Contains(got.err.Error(), "command tree") {
		t.Errorf("the failure %q tells the reader to take the entry module in, which is not what is wrong", got.err)
	}
}

// TestAListedNameWrittenTwiceRegistersTheLayerOnce pins that a repeated name in
// a list of names is one endpoint: a second registration would open two spans
// and count two requests for the one that came.
func TestAListedNameWrittenTwiceRegistersTheLayerOnce(t *testing.T) {
	got := endpointCase{
		listed: []string{"api", "api"}, setList: true,
		declared: []string{"api"},
		serving:  []string{"api"},
	}.run(t)
	if got.err != nil {
		t.Fatalf("drive: %v", got.err)
	}
	if want := []string{"api GET /ping"}; !slices.Equal(got.spans, want) {
		t.Errorf("the backend received the spans %v, want exactly %v", got.spans, want)
	}
	if got.count != 1 {
		t.Errorf("the request histogram counted %d requests, want 1", got.count)
	}
}

// TestTwoExclusiveEntryModulesStillFailTheStartup pins that two entry modules
// remain a failure on the default list. The registry refuses the pair before
// this module's wiring runs, so the case holds that no setting of this module
// turns the conflict into a skipped endpoint list.
func TestTwoExclusiveEntryModulesStillFailTheStartup(t *testing.T) {
	got := endpointCase{
		declared: []string{"api"},
		extra: func(reg *core.Registry) []string {
			reg.Register(entryStandIn("test.rival-entry", true))
			return []string{"test.rival-entry"}
		},
	}.run(t)
	if !errors.Is(got.err, core.ErrExclusiveViolated) {
		t.Fatalf("the drive ended with %v, want the exclusive-delivery violation", got.err)
	}
}
