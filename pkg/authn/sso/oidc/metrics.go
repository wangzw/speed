package oidc

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// The names this module reports under. The metric name and its attributes are
// read by dashboards and are not renamed.
const (
	meterName         = "authn"
	assertionsName    = "authn.sso.assertions"
	protocolAttribute = "protocol"
	outcomeAttribute  = "outcome"
	protocolOIDC      = "oidc"
)

// The outcomes an identity token's judgement ends as. The set is the closed set
// the authn.sso.assertions metric takes for its outcome attribute; this method
// produces the ones below (a SAML assertion produces the others).
const (
	outcomeAccepted  = "accepted"
	outcomeSignature = "signature"
	outcomeAudience  = "audience"
	outcomeTime      = "time"
	outcomeIssuer    = "issuer"
	outcomeOther     = "other"
)

// meters are the instruments the module reports through. Every one is nil when
// the assembly has no metric capability, and the methods then do nothing, which
// is the legal assembly.
type meters struct {
	assertions metric.Int64Counter
}

// assertion records one identity token's judgement under its outcome.
func (m *meters) assertion(ctx context.Context, protocol, outcome string) {
	if m == nil || m.assertions == nil {
		return
	}
	m.assertions.Add(ctx, 1, metric.WithAttributes(
		attribute.String(protocolAttribute, protocol),
		attribute.String(outcomeAttribute, outcome)))
}

// newMeters creates the instruments on the authn meter of provider.
func newMeters(provider metric.MeterProvider) (*meters, error) {
	meter := provider.Meter(meterName)
	assertions, err := meter.Int64Counter(assertionsName,
		metric.WithUnit("{assertion}"),
		metric.WithDescription("Enterprise SSO identity tokens and assertions, by protocol and how each was judged"))
	if err != nil {
		return nil, err
	}
	return &meters{assertions: assertions}, nil
}
