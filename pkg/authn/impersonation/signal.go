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

// The names the module reports under. The metric name and its attribute are read
// by dashboards and are not renamed.
const (
	meterName          = "authn"
	sessionsInstrument = "authn.impersonation.sessions"
	outcomeAttribute   = "outcome"
)

// The outcomes of the sessions counter, the closed set a dashboard reads.
const (
	outcomeStarted = "started"
	outcomeDenied  = "denied"
	outcomeEnded   = "ended"
)

// The seam-level audit actions this module writes, and their outcomes, the
// closed sets the events design states.
const (
	actionStart  = "authn.impersonation.start"
	actionEnd    = "authn.impersonation.end"
	actionRevoke = "authn.impersonation.revoke"

	outcomeGranted = "granted"
	outcomeFailed  = "failed"
	outcomeSuccess = "success"
)

// The keys of an audit row's detail, which are among the closed set the events
// design lists. The session identifier is written as sid and never as session,
// which the journal owns on an impersonating row.
const (
	detailTenant     = "tenant"
	detailTTLSeconds = "ttl_seconds"
	detailSID        = "sid"
	detailGrantID    = "grant_id"
	detailBy         = "by"
)

// meters holds the instruments this module reports through.
type meters struct {
	sessions metric.Int64Counter
}

// newMeters builds the instruments on the provider.
func newMeters(provider metric.MeterProvider) (*meters, error) {
	counter, err := provider.Meter(meterName).Int64Counter(sessionsInstrument,
		metric.WithUnit("{session}"),
		metric.WithDescription("Impersonation sessions that were started, refused or ended"))
	if err != nil {
		return nil, err
	}
	return &meters{sessions: counter}, nil
}

// recordSession counts one impersonation event. When the assembly carries no
// meter provider the counter is nil and nothing is recorded.
func (m *product) recordSession(ctx context.Context, outcome string) {
	if m.meters == nil || m.meters.sessions == nil {
		return
	}
	m.meters.sessions.Add(context.WithoutCancel(ctx), 1,
		metric.WithAttributes(attribute.String(outcomeAttribute, outcome)))
}

// writeAudit writes one best-effort seam-level audit row: a failure is an Error
// and never changes the outcome of the action. The mandatory row of a start is
// written where it is decided and is not through here.
func (m *product) writeAudit(ctx context.Context, e audit.Entry) {
	if m.audit == nil {
		return
	}
	if err := m.audit.Record(ctx, e); err != nil {
		m.logger.Error(ModuleName+": an audit row of an impersonation could not be written",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
	}
}

// publish hands an event to the bus when there is one. A failure to publish is a
// Warn and never changes the outcome: an event is a hint and the authority is
// the record.
func (m *product) publish(ctx context.Context, eventType, tenant string, payload any) {
	if m.bus == nil {
		return
	}
	if err := m.bus.Publish(ctx, event.Event{Type: eventType, TenantID: tenant, Payload: payload}); err != nil {
		m.logger.Warn(ModuleName+": an impersonation event could not be published",
			slog.String("event_type", eventType), slog.String("cause", err.Error()))
	}
}

// publishEnded publishes the ended event of a record, with the cause category its
// end_reason maps to. An expiry publishes nothing: it has no owner to compute a
// signal from, and whoever needs the expiry computes it from the started event.
func (m *product) publishEnded(ctx context.Context, g authn.ImpersonationGrant) {
	if endedBy, ok := endedByCategory(g.EndReason); ok {
		m.publish(ctx, authn.EventImpersonationEnded, g.Tenant, authn.ImpersonationEnded{
			GrantID: g.ID,
			EndedBy: endedBy,
		})
	}
}

// endedByCategory maps an end_reason to the ended_by value the ended event
// carries. The value is a category of the cause and not an account: a lapse has
// no initiator to name. An expiry has no category, so its second result is
// false and no event is published.
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
		// expired and start-failed publish nothing.
		return "", false
	}
}
