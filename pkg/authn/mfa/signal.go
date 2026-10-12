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

// The names the module reports under. The metric names and attributes are read
// by dashboards and are not renamed.
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

// The outcomes of a verification, as the counter's outcome attribute takes them.
const (
	verifyOK          = "ok"
	verifyBad         = "bad"
	verifyReplay      = "replay"
	verifyThrottled   = "throttled"
	verifyUnavailable = "unavailable"
)

// The audit actions and their outcomes, the changes an event announces, and the
// classes of whoever acted. The sets are closed by the events design.
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

// The keys of an audit row's detail, which are among the closed set the events
// design lists.
const (
	detailFactorID = "factor_id"
	detailKind     = "kind"
	detailBy       = "by"
)

// throttleScopeFactor is the scope of the back-off this module keeps, as the
// engagement counter and the event write it.
const throttleScopeFactor = string(authn.ScopeFactor)

// meters are the instruments the module reports through. Every one is nil when
// the assembly has no metric capability, and the methods then do nothing, which
// is the legal assembly.
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

// newMeters creates the instruments on the authn meter of provider.
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

// by is the class of whoever acted, for the event's by field and the audit
// row's: the account holder, another account, or nobody in the request, which is
// a command on the Go interface or a login that is still registering.
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

// publishChanged announces a change to the account's factors after it has been
// committed. The event is a hint and the register is the authority, so a failure
// to publish is a Warn and changes nothing for the caller.
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

// publishEngaged announces that a failure put a factor into back-off.
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

// record writes a best-effort audit row: a failure is an Error and a count, and
// the action goes on, because the actions audited here are the account holder's
// own and a table that is slow must not stop them. A mandatory row is not
// written here at all: the administration handler that resets factors writes it
// and refuses the reset when it cannot.
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
		Object:  string(rec.Account),
		Outcome: outcome,
		Detail:  detail,
	})
	if err != nil {
		m.meters.auditFailed(ctx)
		m.logger.Error(ModuleName+": an audit row could not be written",
			slog.String("account", string(rec.Account)), slog.String("cause", err.Error()))
	}
}
