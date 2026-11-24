package impersonation

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

// The metric name and its attribute are read by dashboards and must not be
// renamed.
const (
	meterName          = "authn"
	sessionsInstrument = "authn.impersonation.sessions"
	outcomeAttribute   = "outcome"
)

const (
	outcomeStarted = "started"
	outcomeDenied  = "denied"
	outcomeEnded   = "ended"
)

const (
	actionStart  = "authn.impersonation.start"
	actionEnd    = "authn.impersonation.end"
	actionRevoke = "authn.impersonation.revoke"

	outcomeGranted = "granted"
	outcomeFailed  = "failed"
	outcomeSuccess = "success"
)

// The audit detail keys. The session is written as sid, never session, which the
// journal owns on an impersonating row.
const (
	detailTenant     = "tenant"
	detailTTLSeconds = "ttl_seconds"
	detailSID        = "sid"
	detailGrantID    = "grant_id"
	detailBy         = "by"
)

type meters struct {
	sessions metric.Int64Counter
}

func newMeters(provider metric.MeterProvider) (*meters, error) {
	counter, err := provider.Meter(meterName).Int64Counter(sessionsInstrument,
		metric.WithUnit("{session}"),
		metric.WithDescription("Impersonation sessions that were started, refused or ended"))
	if err != nil {
		return nil, err
	}
	return &meters{sessions: counter}, nil
}

func (m *product) recordSession(ctx context.Context, outcome string) {
	if m.meters == nil || m.meters.sessions == nil {
		return
	}
	m.meters.sessions.Add(context.WithoutCancel(ctx), 1,
		metric.WithAttributes(attribute.String(outcomeAttribute, outcome)))
}

func (m *product) writeAudit(ctx context.Context, e audit.Entry) {
	if m.audit == nil {
		return
	}
	if err := m.audit.Record(ctx, e); err != nil {
		m.logger.Error(ModuleName+": an audit row of an impersonation could not be written",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
	}
}

func (m *product) publish(ctx context.Context, eventType, tenant string, payload any) {
	if m.bus == nil {
		return
	}
	if err := m.bus.Publish(ctx, event.Event{Type: eventType, TenantID: tenant, Payload: payload}); err != nil {
		m.logger.Warn(ModuleName+": an impersonation event could not be published",
			slog.String("event_type", eventType), slog.String("cause", err.Error()))
	}
}

func (m *product) publishEnded(ctx context.Context, g authn.ImpersonationGrant) {
	if endedBy, ok := endedByCategory(g.EndReason); ok {
		m.publish(ctx, authn.EventImpersonationEnded, g.Tenant, authn.ImpersonationEnded{
			GrantID: g.ID,
			EndedBy: endedBy,
		})
	}
}

func endedByCategory(endReason string) (string, bool) {
	switch endReason {
	case authn.EndExplicit:
		return "actor", true
	case authn.EndRevoked:
		return "revoked", true
	case authn.EndSessionEnded:
		return "session-ended", true
	case authn.EndActorDisabled, authn.EndTargetDisabled:
		return "disabled", true
	case authn.EndMembershipRemoved:
		return "membership-removed", true
	default:
		return "", false
	}
}
