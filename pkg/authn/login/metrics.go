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

// The meter every module of the capability directory reports under, the
// instruments this module reports through, and the attributes they carry, whose
// sets design-authn-events.md makes a closed set.
const (
	meterName               = "authn"
	completionsInstrument   = "authn.login.completions"
	auditFailuresInstrument = "authn.audit.write-failures"
	methodAttribute         = "method"
	outcomeAttribute        = "outcome"
	mandatoryAttribute      = "mandatory"
)

// The outcomes of a login, the closed set of the instrument's outcome attribute.
// success and failed are the two a login ends on; throttled, refused and
// unavailable are the three a terminal answer outside those carries; expired is
// reserved for a flow that reached its deadline, which this build lets the key
// expire rather than observe.
const (
	outcomeSuccess     = "success"
	outcomeFailed      = "failed"
	outcomeThrottled   = "throttled"
	outcomeRefused     = "refused"
	outcomeUnavailable = "unavailable"
	outcomeExpired     = "expired"
)

// meters is the instrument this module reports through. The zero value reports
// nothing, which is the state of an assembly with no metric capability.
type meters struct {
	logins        metric.Int64Counter
	auditFailures metric.Int64Counter
}

// newMeters creates the instruments on the meter of the capability directory.
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
	return m, nil
}

// login counts one login outcome. method is the login method's name and outcome
// one of the closed set above.
func (m meters) login(ctx context.Context, method, outcome string) {
	if m.logins != nil {
		m.logins.Add(ctx, 1, metric.WithAttributes(
			attribute.String(methodAttribute, method),
			attribute.String(outcomeAttribute, outcome)))
	}
}

// auditFailed counts one audit row that could not be written. Every row this
// module writes is in the best-effort tier, so the mandatory attribute is false.
func (m meters) auditFailed(ctx context.Context) {
	if m.auditFailures == nil {
		return
	}
	m.auditFailures.Add(ctx, 1, metric.WithAttributes(attribute.Bool(mandatoryAttribute, false)))
}

// reportLogin counts a terminal outcome of a login flow and writes the audit row
// of one that did not succeed. A flow of any other purpose is not a login and
// neither happens. The success row is auditLogin's, which carries the session,
// the methods and the derived identity.
func (r *routes) reportLogin(ctx context.Context, rec record, outcome string) {
	if rec.Purpose != authn.PurposeLogin {
		return
	}
	r.meters.login(ctx, rec.Method, outcome)
	if auditOutcome, ok := loginAuditOutcome(outcome); ok {
		r.auditLoginOutcome(ctx, rec, auditOutcome)
	}
}

// loginOutcomeOf maps an error a login flow failed with to the counter's
// outcome, the way fail maps it to an answer: a back-off is throttled, a
// dependency that cannot answer is unavailable, a policy refusal is refused, and
// every other terminal failure is failed.
func loginOutcomeOf(err error) string {
	var throttled *authn.ThrottledError
	switch {
	case errors.As(err, &throttled), errors.Is(err, authn.ErrThrottled):
		return outcomeThrottled
	case errors.Is(err, authn.ErrBusy), errors.Is(err, authn.ErrUnavailable):
		return outcomeUnavailable
	case errors.Is(err, authn.ErrAccountUnavailable), errors.Is(err, authn.ErrLinkRequired),
		errors.Is(err, authn.ErrBindingConflict):
		return outcomeRefused
	default:
		return outcomeFailed
	}
}
