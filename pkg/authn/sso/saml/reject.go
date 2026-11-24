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

const (
	meterName            = "authn"
	assertionsInstrument = "authn.sso.assertions"
	protocolAttribute    = "protocol"
	outcomeAttribute     = "outcome"
)

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

const actionSSOReject = "authn.sso.reject"

type meters struct {
	assertions metric.Int64Counter
}

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

func (m meters) assertion(ctx context.Context, protocol, outcome string) {
	if m.assertions != nil {
		m.assertions.Add(ctx, 1, metric.WithAttributes(
			attribute.String(protocolAttribute, protocol),
			attribute.String(outcomeAttribute, outcome)))
	}
}

func (p *product) reject(ctx context.Context, idpID, outcome string, reason string) authn.Step {
	p.meters.assertion(ctx, providerProtocol, outcome)
	p.auditReject(ctx, idpID, outcome)
	return authn.StepRefused(fmt.Errorf("%s: %s: %w", ModuleName, reason, authn.ErrInvalidCredentials))
}

func (p *product) accept(ctx context.Context) {
	p.meters.assertion(ctx, providerProtocol, outcomeAccepted)
}

func stepUnavailable(doing string, err error) authn.Step {
	return authn.StepUnavailable(fmt.Errorf("%w: %s: %w", authn.ErrUnavailable, ModuleName+" "+doing, err))
}

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
