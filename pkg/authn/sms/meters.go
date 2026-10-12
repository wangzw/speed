package sms

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// The names this module reports under. The metric name and its attribute are
// read by dashboards and are not renamed.
const (
	meterName         = "authn"
	sendsName         = "authn.sms.sends"
	auditFailuresName = "authn.audit.write-failures"

	outcomeAttribute   = "outcome"
	mandatoryAttribute = "mandatory"
)

// The outcomes of a send, as the counter's outcome attribute takes them. The set
// is closed: a send ends as exactly one of these, and every Send call records at
// most one.
const (
	outcomeSent              = "sent"
	outcomeUncertain         = "uncertain"
	outcomeThrottled         = "throttled"
	outcomeRejected          = "rejected"
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
	return &meters{sends: sends, auditFailures: failures}, nil
}
