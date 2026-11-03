package sms

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/vislake/speed/pkg/sms/budget"
)

// The names this module reports under. The metric name and its attribute are
// read by dashboards and are not renamed.
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

// The dimension values the utilization gauge reports. They are a closed set: a
// data point names one budget, and the shared ceiling is the one dimension both
// channels spend.
const (
	dimensionCeiling        = "ceiling"
	dimensionPhone          = "phone"
	dimensionSource         = "source"
	dimensionSourceDistinct = "source-distinct"
	dimensionSourceAttempt  = "source-attempt"
	dimensionGlobal         = "global"
	dimensionGlobalAttempt  = "global-attempt"
	dimensionAccount        = "account"

	// purposeShared is the purpose label of the shared ceiling, which is not any
	// one purpose's budget.
	purposeShared = "shared"
)

// The outcomes of a send, as the counter's outcome attribute takes them. The set
// is closed: a send ends as exactly one of these, and every Send call records at
// most one.
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

// meters are the instruments the module reports through. Every one is nil when
// the assembly has no metric capability, and the methods then do nothing, which
// is the legal assembly.
type meters struct {
	sends         metric.Int64Counter
	auditFailures metric.Int64Counter
	utilization   metric.Float64Gauge
}

// budget records how much of one budget is in use, as a share of its limit. The
// purpose names the sending purpose and the dimension names the budget; the
// shared ceiling reports under purposeShared so it is not summed with a
// purpose's own budgets. The channel attribute is always authn here and
// notification on the other capability's same-named gauge, so an operator reads
// one instrument for both sending channels. A zero or negative limit is not a
// budget and records nothing, and a missing instrument is the legal assembly
// with no metrics.
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

// sent records one send under the outcome it ended as.
func (m *meters) sent(ctx context.Context, outcome string) {
	if m == nil || m.sends == nil {
		return
	}
	m.sends.Add(ctx, 1, metric.WithAttributes(attribute.String(outcomeAttribute, outcome)))
}

// auditFailed records an audit row that could not be written, which the
// relaxation watch writes on a best-effort basis.
func (m *meters) auditFailed(ctx context.Context) {
	if m == nil || m.auditFailures == nil {
		return
	}
	m.auditFailures.Add(ctx, 1, metric.WithAttributes(attribute.Bool(mandatoryAttribute, false)))
}

// newMeters creates the instruments on the authn meter of provider.
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
