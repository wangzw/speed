package oidc

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	meterName         = "authn"
	assertionsName    = "authn.sso.assertions"
	protocolAttribute = "protocol"
	outcomeAttribute  = "outcome"
	protocolOIDC      = "oidc"
)

const (
	outcomeAccepted  = "accepted"
	outcomeSignature = "signature"
	outcomeAudience  = "audience"
	outcomeTime      = "time"
	outcomeIssuer    = "issuer"
	outcomeOther     = "other"
)

type meters struct {
	assertions metric.Int64Counter
}

func (m *meters) assertion(ctx context.Context, protocol, outcome string) {
	if m == nil || m.assertions == nil {
		return
	}
	m.assertions.Add(ctx, 1, metric.WithAttributes(
		attribute.String(protocolAttribute, protocol),
		attribute.String(outcomeAttribute, outcome)))
}

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
