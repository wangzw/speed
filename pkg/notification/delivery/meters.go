package delivery

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/vislake/speed/pkg/sms/budget"
)

// The metric this module reports under. The name is shared with the
// authentication capability's budget gauge on purpose: an operator watching the
// SMS budget reads one instrument, and the channel attribute says which sending
// capability consumed it. The module's scope name differs because instruments
// belong to the meter that created them.
const (
	meterName       = ModuleName
	utilizationName = "authn.sms.budget.utilization"

	utilizationChannelAttribute   = "channel"
	utilizationPurposeAttribute   = "purpose"
	utilizationDimensionAttribute = "dimension"
)

// The purpose and dimension of the one point that is not a notification budget:
// the cross-channel ceiling spans both sending channels, so it reports under the
// same shared purpose and ceiling dimension the authentication capability's
// gauge names for it.
const (
	purposeShared    = "shared"
	dimensionCeiling = "ceiling"
)

// meters are the instruments the module reports through. Every one is nil when
// the assembly has no metric capability, and the methods then do nothing, which
// is what keeps a report from changing what a dispatch does.
type meters struct {
	utilization metric.Float64Gauge
}

// observeBudget records how much of one window a channel has consumed. The value
// is the count divided by the limit, so the alerting threshold an operator sets
// (0.8, say) means the same thing for every window. The purpose and dimension
// label the point; channel is always notification here and authn on the other
// capability's same-named gauge.
func (m *meters) observeBudget(ctx context.Context, purpose, dimension string, used, limit int64) {
	if m == nil || m.utilization == nil || limit <= 0 {
		return
	}
	m.utilization.Record(ctx, float64(used)/float64(limit), metric.WithAttributes(
		attribute.String(utilizationChannelAttribute, string(budget.ChannelNotification)),
		attribute.String(utilizationPurposeAttribute, purpose),
		attribute.String(utilizationDimensionAttribute, dimension),
	))
}

// newMeters creates the instruments on this module's meter of provider. A nil
// provider is an assembly with no metric capability: a nil set is legal, and
// every report is a no-op.
func newMeters(provider metric.MeterProvider) (*meters, error) {
	if provider == nil {
		return nil, nil
	}
	meter := provider.Meter(meterName)
	gauge, err := meter.Float64Gauge(utilizationName,
		metric.WithUnit("{ratio}"),
		metric.WithDescription("The share of an SMS budget a sending channel has consumed, by purpose and window"))
	if err != nil {
		return nil, err
	}
	return &meters{utilization: gauge}, nil
}
