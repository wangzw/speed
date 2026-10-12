package saml

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/observability"
)

// The meter every module of the capability directory reports under, the
// instrument this module reports through, and its two attributes, whose sets
// design-authn-events.md makes a closed set.
const (
	meterName            = "authn"
	assertionsInstrument = "authn.sso.assertions"
	protocolAttribute    = "protocol"
	outcomeAttribute     = "outcome"
)

// The outcomes of a SAML assertion, the closed set of the instrument's outcome
// attribute. accepted is a valid assertion; the others name the check that
// turned one away, one per row of the design's validation table.
const (
	outcomeAccepted    = "accepted"
	outcomeSignature   = "signature"
	outcomeAudience    = "audience"
	outcomeTime        = "time"
	outcomeReplay      = "replay"
	outcomeInResponse  = "in-response-to"
	outcomeIssuer      = "issuer"
	outcomeDestination = "destination"
	outcomeOther       = "other"
)

// The audit action a rejected assertion is recorded under.
const actionSSOReject = "authn.sso.reject"

// meters is the instrument this module reports through. The zero value reports
// nothing, which is the state of an assembly with no metric capability.
type meters struct {
	assertions metric.Int64Counter
}

// newMeters creates the instrument on the meter of the capability directory.
func newMeters(provider observability.MeterProvider) (meters, error) {
	var m meters
	counter, err := provider.Meter(meterName).Int64Counter(assertionsInstrument,
		metric.WithUnit("{assertion}"),
		metric.WithDescription("SAML and enterprise OIDC assertion outcomes, by protocol and outcome"))
	if err != nil {
		return meters{}, fmt.Errorf("creating the %s counter: %w", assertionsInstrument, err)
	}
	m.assertions = counter
	return m, nil
}

// assertion counts one assertion outcome.
func (m meters) assertion(ctx context.Context, protocol, outcome string) {
	if m.assertions != nil {
		m.assertions.Add(ctx, 1, metric.WithAttributes(
			attribute.String(protocolAttribute, protocol),
			attribute.String(outcomeAttribute, outcome)))
	}
}

// reject turns an assertion check into the answer the design gives it: the
// client is answered the one login-failed whatever went wrong, while the cause
// reaches the log by way of the answer, the metric and, best-effort, the audit
// row. The cause carries no value the client sent.
func (p *product) reject(ctx context.Context, idpID, outcome string, reason string) authn.Step {
	p.meters.assertion(ctx, providerProtocol, outcome)
	p.auditReject(ctx, idpID, outcome)
	return authn.StepRefused(fmt.Errorf("%s: %s: %w", ModuleName, reason, authn.ErrInvalidCredentials))
}

// accept counts one accepted assertion.
func (p *product) accept(ctx context.Context) {
	p.meters.assertion(ctx, providerProtocol, outcomeAccepted)
}

// stepUnavailable answers that the method cannot say right now, because a
// dependency it needs cannot be reached. The cause stays in the chain so that
// the engine finds an authn.ErrUnavailable in it and a 503 is counted apart from
// a refusal.
func stepUnavailable(doing string, err error) authn.Step {
	return authn.StepUnavailable(fmt.Errorf("%w: %s: %w", authn.ErrUnavailable, ModuleName+" "+doing, err))
}

// auditReject writes the best-effort audit row of a rejection. A failure is
// never the login's, and no cause names an assertion value.
func (p *product) auditReject(ctx context.Context, idpID, outcome string) {
	if p.audit == nil {
		return
	}
	_ = p.audit.Record(ctx, audit.Entry{
		Action:  actionSSOReject,
		Object:  idpID,
		Outcome: outcome,
	})
}
