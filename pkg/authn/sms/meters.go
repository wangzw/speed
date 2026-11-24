package sms

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/vislake/speed/pkg/sms/budget"
)

const (
	meterName         = "authn"
	sendsName         = "authn.sms.sends"
	auditFailuresName = "authn.audit.write-failures"
	utilizationName   = "authn.sms.budget.utilization"

	outcomeAttribute   = "outcome"
	mandatoryAttribute = "mandatory"
	channelAttribute   = "channel"
	purposeAttribute   = "purpose"
	dimensionAttribute = "dimension"
)

const (
	dimensionCeiling        = "ceiling"
	dimensionPhone          = "phone"
	dimensionSource         = "source"
	dimensionSourceDistinct = "source-distinct"
	dimensionSourceAttempt  = "source-attempt"
	dimensionGlobal         = "global"
	dimensionGlobalAttempt  = "global-attempt"
	dimensionAccount        = "account"

	purposeShared = "shared"
)

const (
	outcomeSent              = "sent"
	outcomeUncertain         = "uncertain"
	outcomeThrottled         = "throttled"
	outcomeRejected          = "rejected"
	outcomeUnroutable        = "unroutable"
	outcomeNotSendable       = "not-sendable"
	outcomeUnsupportedRegion = "unsupported-region"
	outcomeBudgetExhausted   = "budget-exhausted"
	outcomeSuppressed        = "suppressed"
	outcomeDropped           = "dropped"
)

type meters struct {
	sends         metric.Int64Counter
	auditFailures metric.Int64Counter
	utilization   metric.Float64Gauge
}

func (m *meters) budget(ctx context.Context, purpose, dimension string, used int64, limit int) {
	if m == nil || m.utilization == nil || limit <= 0 {
		return
	}
	m.utilization.Record(ctx, float64(used)/float64(limit),
		metric.WithAttributes(
			attribute.String(channelAttribute, string(budget.ChannelAuthn)),
			attribute.String(purposeAttribute, purpose),
			attribute.String(dimensionAttribute, dimension)))
}

func (m *meters) sent(ctx context.Context, outcome string) {
	if m == nil || m.sends == nil {
		return
	}
	m.sends.Add(ctx, 1, metric.WithAttributes(attribute.String(outcomeAttribute, outcome)))
}

func (m *meters) auditFailed(ctx context.Context) {
	if m == nil || m.auditFailures == nil {
		return
	}
	m.auditFailures.Add(ctx, 1, metric.WithAttributes(attribute.Bool(mandatoryAttribute, false)))
}

func newMeters(provider metric.MeterProvider) (*meters, error) {
	meter := provider.Meter(meterName)
	sends, err := meter.Int64Counter(sendsName,
		metric.WithUnit("{message}"), metric.WithDescription("SMS verification codes, by how each send ended"))
	if err != nil {
		return nil, err
	}
	failures, err := meter.Int64Counter(auditFailuresName,
		metric.WithUnit("{failure}"), metric.WithDescription("Audit rows that could not be written"))
	if err != nil {
		return nil, err
	}
	utilization, err := meter.Float64Gauge(utilizationName,
		metric.WithUnit("{ratio}"),
		metric.WithDescription("The share of an SMS budget a sending channel has consumed, by purpose and window"))
	if err != nil {
		return nil, err
	}
	return &meters{sends: sends, auditFailures: failures, utilization: utilization}, nil
}
