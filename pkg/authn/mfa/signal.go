package mfa

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

const (
	meterName         = "authn"
	verificationsName = "authn.mfa.verifications"
	engagementsName   = "authn.throttle.engagements"
	auditFailuresName = "authn.audit.write-failures"

	factorAttribute    = "factor"
	outcomeAttribute   = "outcome"
	scopeAttribute     = "scope"
	mandatoryAttribute = "mandatory"
)

const (
	verifyOK          = "ok"
	verifyBad         = "bad"
	verifyReplay      = "replay"
	verifyThrottled   = "throttled"
	verifyUnavailable = "unavailable"
)

const (
	actionEnroll   = "authn.factor.enroll"
	actionRemove   = "authn.factor.remove"
	outcomeSuccess = "success"
	outcomeDenied  = "denied"

	changeEnrolled = "enrolled"
	changeRemoved  = "removed"
	changeReset    = "reset"
	changeFlagged  = "flagged"

	byClassSelf   = "self"
	byClassAdmin  = "admin"
	byClassSystem = "system"
)

const (
	detailFactorID = "factor_id"
	detailKind     = "kind"
	detailBy       = "by"
)

const throttleScopeFactor = string(authn.ScopeFactor)

type meters struct {
	verifications metric.Int64Counter
	engagements   metric.Int64Counter
	auditFailures metric.Int64Counter
}

func (s *meters) verified(ctx context.Context, kind authn.FactorKind, outcome string) {
	if s == nil || s.verifications == nil {
		return
	}
	s.verifications.Add(ctx, 1, metric.WithAttributes(
		attribute.String(factorAttribute, string(kind)), attribute.String(outcomeAttribute, outcome)))
}

func (s *meters) engaged(ctx context.Context) {
	if s == nil || s.engagements == nil {
		return
	}
	s.engagements.Add(ctx, 1, metric.WithAttributes(attribute.String(scopeAttribute, throttleScopeFactor)))
}

func (s *meters) auditFailed(ctx context.Context) {
	if s == nil || s.auditFailures == nil {
		return
	}
	s.auditFailures.Add(ctx, 1, metric.WithAttributes(attribute.Bool(mandatoryAttribute, false)))
}

func newMeters(provider metric.MeterProvider) (*meters, error) {
	meter := provider.Meter(meterName)
	verifications, err := meter.Int64Counter(verificationsName,
		metric.WithUnit("{verification}"), metric.WithDescription("Second-factor verifications, by the kind of factor and how each ended"))
	if err != nil {
		return nil, err
	}
	engagements, err := meter.Int64Counter(engagementsName,
		metric.WithUnit("{engagement}"), metric.WithDescription("Failure back-offs that began, by the scope of the key"))
	if err != nil {
		return nil, err
	}
	failures, err := meter.Int64Counter(auditFailuresName,
		metric.WithUnit("{failure}"), metric.WithDescription("Audit rows that could not be written"))
	if err != nil {
		return nil, err
	}
	return &meters{verifications: verifications, engagements: engagements, auditFailures: failures}, nil
}

func by(ctx context.Context, account authn.AccountID) string {
	id, ok := authn.FromContext(ctx)
	switch {
	case !ok:
		return byClassSystem
	case !id.Impersonating() && id.Account() == account:
		return byClassSelf
	default:
		return byClassAdmin
	}
}

func (m *product) publishChanged(ctx context.Context, rec authn.FactorRecord, change, byClass string) {
	if m.bus == nil {
		return
	}
	payload := authn.FactorChanged{
		AccountID: string(rec.Account),
		FactorID:  rec.ID,
		Kind:      string(rec.Kind),
		Change:    change,
		By:        byClass,
	}
	if err := m.bus.Publish(ctx, event.Event{Type: authn.EventFactorChanged, Payload: payload}); err != nil {
		m.logger.Warn(ModuleName+": an event could not be published",
			slog.String("account", string(rec.Account)), slog.String("cause", err.Error()))
	}
}

func (m *product) publishEngaged(ctx context.Context, account authn.AccountID) {
	if m.bus == nil {
		return
	}
	payload := authn.ThrottleEngaged{Scope: throttleScopeFactor, AccountID: string(account)}
	if err := m.bus.Publish(ctx, event.Event{Type: authn.EventThrottleEngaged, Payload: payload}); err != nil {
		m.logger.Warn(ModuleName+": an event could not be published",
			slog.String("account", string(account)), slog.String("cause", err.Error()))
	}
}

func (m *product) record(ctx context.Context, action, outcome string, rec authn.FactorRecord, byClass string) {
	if m.audit == nil {
		return
	}
	detail := map[string]string{detailKind: string(rec.Kind), detailBy: byClass}
	if rec.ID != "" {
		detail[detailFactorID] = rec.ID
	}
	err := m.audit.Record(ctx, audit.Entry{
		Action:  action,
		Object:  audit.AccountObject(string(rec.Account)),
		Outcome: outcome,
		Detail:  detail,
	})
	if err != nil {
		m.meters.auditFailed(ctx)
		m.logger.Error(ModuleName+": an audit row could not be written",
			slog.String("account", string(rec.Account)), slog.String("cause", err.Error()))
	}
}
