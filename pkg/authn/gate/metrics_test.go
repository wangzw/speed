package gate

import (
	"context"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/observability"
)

// countsIn reads the conclusion counter out of one collection, by outcome.
// It reports false when the instrument is not in the collection at all, and it
// reports the name of the meter the instrument came from.
func countsIn(rm metricdata.ResourceMetrics) (map[string]int64, string, bool) {
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "authn.gate.conclusions" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				return nil, "", false
			}
			out := make(map[string]int64, len(sum.DataPoints))
			for _, point := range sum.DataPoints {
				outcome, _ := point.Attributes.Value("outcome")
				out[outcome.AsString()] = point.Value
			}
			return out, scope.Scope.Name, true
		}
	}
	return nil, "", false
}

// TestTheConclusionCountsReachTheMetricCapability holds the one surface the
// buckets are read from.
func TestTheConclusionCountsReachTheMetricCapability(t *testing.T) {
	collected := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(collected))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutting the meter provider down: %v", err)
		}
	})

	router := routerWith("public")
	var counts map[string]int64
	var meterName string
	var found bool
	registrant := core.Module{
		Name: "stand-in.registrant",
		New:  func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			gate, err := core.Resolve[authn.Gate](reg)
			if err != nil {
				return err
			}
			gate.Optional("public", "GET", "/catalog")
			gate.Exempt("public", "GET", "/status")
			return nil
		},
	}
	err := assembly{
		body:     guarding("public"),
		backends: []authn.Verifier{passes(basicBackend("static", "static."))},
		router:   router,
		modules:  []core.Module{registrant},
		supplied: []supply{{
			module:   "stand-in.meter",
			token:    (*observability.MeterProvider)(nil),
			instance: provider,
		}},
	}.drive(t, func(ctx context.Context, _ *core.Registry) error {
		engine := gin.New()
		engine.Use(router.endpoints["public"].middleware[0].Handler)
		for _, path := range []string{"/orders", "/catalog", "/status"} {
			engine.GET(path, func(c *gin.Context) { c.Status(nethttp.StatusOK) })
		}
		engine.ServeHTTP(httptest.NewRecorder(), withBasic(request("GET", "/orders"), "alice", "secret"))
		engine.ServeHTTP(httptest.NewRecorder(), request("GET", "/orders"))
		engine.ServeHTTP(httptest.NewRecorder(), request("GET", "/orders"))
		engine.ServeHTTP(httptest.NewRecorder(), request("GET", "/catalog"))
		engine.ServeHTTP(httptest.NewRecorder(), request("GET", "/catalog"))
		engine.ServeHTTP(httptest.NewRecorder(), request("GET", "/status"))

		var rm metricdata.ResourceMetrics
		if err := collected.Collect(ctx, &rm); err != nil {
			return err
		}
		counts, meterName, found = countsIn(rm)
		return nil
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}

	if !found {
		t.Fatalf("no %q instrument was collected: the conclusion counts are held in the process "+
			"and reach no dashboard", "authn.gate.conclusions")
	}
	if meterName != "authn" {
		t.Errorf("the counter is reported on the meter %q, want %q: the meter is the capability "+
			"directory's, and only the instrument name carries the module's", meterName, "authn")
	}
	want := map[string]int64{"authenticated": 1, "denied": 2, "unspoken": 0, "unavailable": 0, "anonymous": 2}
	for outcome, n := range want {
		got, present := counts[outcome]
		if !present {
			t.Errorf("the %q bucket is not reported; every bucket is, the empty ones included",
				outcome)
			continue
		}
		if got != n {
			t.Errorf("the %q bucket reports %d, want %d", outcome, got, n)
		}
	}
	if len(counts) != len(want) {
		t.Errorf("the counter reports the outcomes %v, want exactly the five buckets", counts)
	}
}
