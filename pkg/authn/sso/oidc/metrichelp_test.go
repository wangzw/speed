package oidc

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func counterValue(t *testing.T, reader *sdkmetric.ManualReader, name string, pairs ...string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collecting the metrics: %v", err)
	}
	var want []attribute.KeyValue
	for i := 0; i+1 < len(pairs); i += 2 {
		want = append(want, attribute.String(pairs[i], pairs[i+1]))
	}
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("%s is not an int64 sum", name)
			}
			for _, dp := range sum.DataPoints {
				match := true
				for _, kv := range want {
					if v, ok := dp.Attributes.Value(kv.Key); !ok || v != kv.Value {
						match = false
					}
				}
				if match {
					total += dp.Value
				}
			}
		}
	}
	return total
}
