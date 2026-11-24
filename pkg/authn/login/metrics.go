package login

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/observability"
)

const (
	meterName               = "authn"
	completionsInstrument   = "authn.login.completions"
	auditFailuresInstrument = "authn.audit.write-failures"
	resetOpsInstrument      = "authn.password.reset.operations"
	methodAttribute         = "method"
	outcomeAttribute        = "outcome"
	mandatoryAttribute      = "mandatory"
	operationAttribute      = "operation"
	channelAttribute        = "channel"
)

const (
	resetOpSend     = "send"
	resetOpVerify   = "verify"
	resetOpComplete = "complete"

	resetSent       = "sent"
	resetSuppressed = "suppressed"
	resetVerified   = "verified"
	resetCompleted  = "completed"
)

const (
	outcomeSuccess     = "success"
	outcomeFailed      = "failed"
	outcomeThrottled   = "throttled"
	outcomeRefused     = "refused"
	outcomeUnavailable = "unavailable"
	outcomeExpired     = "expired"
)

type meters struct {
	logins        metric.Int64Counter
	auditFailures metric.Int64Counter
	resetOps      metric.Int64Counter
}

func newMeters(provider observability.MeterProvider) (meters, error) {
	var m meters
	meter := provider.Meter(meterName)
	counter, err := meter.Int64Counter(completionsInstrument,
		metric.WithUnit("{login}"),
		metric.WithDescription("Login outcomes, by login method and outcome"))
	if err != nil {
		return meters{}, fmt.Errorf("creating the %s counter: %w", completionsInstrument, err)
	}
	m.logins = counter
	failures, err := meter.Int64Counter(auditFailuresInstrument,
		metric.WithUnit("{failure}"),
		metric.WithDescription("Audit rows that could not be written, by whether the action required one"))
	if err != nil {
		return meters{}, fmt.Errorf("creating the %s counter: %w", auditFailuresInstrument, err)
	}
	m.auditFailures = failures
	resetOps, err := meter.Int64Counter(resetOpsInstrument,
		metric.WithUnit("{operation}"),
		metric.WithDescription("Anonymous password reset operations, by operation, channel and outcome"))
	if err != nil {
		return meters{}, fmt.Errorf("creating the %s counter: %w", resetOpsInstrument, err)
	}
	m.resetOps = resetOps
	return m, nil
}

func (m meters) resetOperation(ctx context.Context, operation, channel, outcome string) {
	if m.resetOps == nil {
		return
	}
	m.resetOps.Add(ctx, 1, metric.WithAttributes(
		attribute.String(operationAttribute, operation),
		attribute.String(channelAttribute, channel),
		attribute.String(outcomeAttribute, outcome)))
}

func (m meters) login(ctx context.Context, method, outcome string) {
	if m.logins != nil {
		m.logins.Add(ctx, 1, metric.WithAttributes(
			attribute.String(methodAttribute, method),
			attribute.String(outcomeAttribute, outcome)))
	}
}

func (m meters) auditFailed(ctx context.Context) { m.auditFailedFor(ctx, false) }

func (m meters) auditFailedFor(ctx context.Context, mandatory bool) {
	if m.auditFailures == nil {
		return
	}
	m.auditFailures.Add(ctx, 1, metric.WithAttributes(attribute.Bool(mandatoryAttribute, mandatory)))
}

func (r *routes) reportLogin(ctx context.Context, rec record, outcome string) {
	if rec.Purpose != authn.PurposeLogin {
		return
	}
	r.meters.login(ctx, rec.Method, outcome)
	if auditOutcome, ok := loginAuditOutcome(outcome); ok {
		r.auditLoginOutcome(ctx, rec, auditOutcome)
	}
}

func loginOutcomeOf(err error) string {
	var throttled *authn.ThrottledError
	switch {
	case errors.As(err, &throttled), errors.Is(err, authn.ErrThrottled):
		return outcomeThrottled
	case errors.Is(err, authn.ErrBusy), errors.Is(err, authn.ErrUnavailable):
		return outcomeUnavailable
	case errors.Is(err, authn.ErrAccountUnavailable), errors.Is(err, authn.ErrLinkRequired),
		errors.Is(err, authn.ErrContactTaken), errors.Is(err, authn.ErrBindingConflict):
		return outcomeRefused
	default:
		return outcomeFailed
	}
}
