package gate

import (
	"context"
	"net/http/httptest"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/observability"
)

// countsIn reads the conclusion counter out of one collection, by outcome.
// It reports false when the instrument is not in the collection at all.
func countsIn(rm metricdata.ResourceMetrics) (map[string]int64, bool) {
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "authn.gate.conclusions" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				return nil, false
			}
			out := make(map[string]int64, len(sum.DataPoints))
			for _, point := range sum.DataPoints {
				outcome, _ := point.Attributes.Value("outcome")
				out[outcome.AsString()] = point.Value
			}
			return out, true
		}
	}
	return nil, false
}

// TestTheConclusionCountsReachTheMetricCapability holds the one surface the
// four buckets are read from.
//
// The buckets are fixed so that a dashboard and an alert can be written against
// them, and a count held in the process with no reader outside it is one no
// dashboard ever sees: the outage bucket an operator is told to alert on would
// be a number nobody can alert on. Where the assembly carries the metric
// capability, each bucket is reported through it under the outcome it names,
// the empty ones included, so a series exists before its first conclusion.
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
	var found bool
	err := assembly{
		body:     guarding("public"),
		backends: []authn.Verifier{passes(enumerating("local", "alice"))},
		router:   router,
		supplied: []supply{{
			module:   "stand-in.meter",
			token:    (*observability.MeterProvider)(nil),
			instance: provider,
		}},
	}.drive(t, func(ctx context.Context, _ *core.Registry) error {
		engine, _ := engineWith(router.endpoints["public"].middleware[0].Handler)
		engine.ServeHTTP(httptest.NewRecorder(), withBasic(request("GET", "/orders"), "alice", "secret"))
		engine.ServeHTTP(httptest.NewRecorder(), request("GET", "/orders"))
		engine.ServeHTTP(httptest.NewRecorder(), request("GET", "/orders"))

		var rm metricdata.ResourceMetrics
		if err := collected.Collect(ctx, &rm); err != nil {
			return err
		}
		counts, found = countsIn(rm)
		return nil
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}

	if !found {
		t.Fatalf("no %q instrument was collected: the conclusion counts are held in the process "+
			"and reach no dashboard", "authn.gate.conclusions")
	}
	want := map[string]int64{"authenticated": 1, "denied": 2, "unspoken": 0, "unavailable": 0}
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
		t.Errorf("the counter reports the outcomes %v, want exactly the four buckets", counts)
	}
}
